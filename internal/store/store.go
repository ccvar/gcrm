// Package store 是 CRM 的数据层。纪律（为后期切 MySQL/云数据库留余地）：
//  1. 所有 SQL 收敛在本包，handler 不碰 SQL；
//  2. 只写方言无关的 SQL 子集，时间一律存 unix 秒（INTEGER）；
//  3. 迁移带版本号，只允许追加，不允许改历史。
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	// SQLite 单写者：串行化连接，省去 SQLITE_BUSY 处理
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().Unix() }

func (s *Store) migrate() error {
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("迁移 v%d 失败: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

var migrations = []string{schemaV1, schemaV2}

// v2：商机复盘 —— 关单后 AI 回溯 timeline 写复盘，沉淀 playbook。
const schemaV2 = `
ALTER TABLE deals ADD COLUMN review TEXT NOT NULL DEFAULT '';
ALTER TABLE deals ADD COLUMN review_lessons TEXT NOT NULL DEFAULT '';
`

const schemaV1 = `
CREATE TABLE users (
  id         INTEGER PRIMARY KEY,
  username   TEXT NOT NULL UNIQUE,
  pass_hash  TEXT NOT NULL,
  created_at INTEGER NOT NULL
);

CREATE TABLE sessions (
  token      TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  csrf       TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE TABLE customers (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL,
  company       TEXT NOT NULL DEFAULT '',
  phone         TEXT NOT NULL DEFAULT '',
  wechat        TEXT NOT NULL DEFAULT '',
  email         TEXT NOT NULL DEFAULT '',
  source        TEXT NOT NULL DEFAULT '',
  stage         TEXT NOT NULL DEFAULT 'lead',
  intent        TEXT NOT NULL DEFAULT 'unknown',
  intent_reason TEXT NOT NULL DEFAULT '',
  notes         TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

CREATE TABLE interactions (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL,
  channel     TEXT NOT NULL DEFAULT 'other',
  direction   TEXT NOT NULL DEFAULT 'out',
  content     TEXT NOT NULL,
  occurred_at INTEGER NOT NULL,
  ai_summary  TEXT NOT NULL DEFAULT '',
  ai_json     TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL
);
CREATE INDEX idx_interactions_customer ON interactions(customer_id, occurred_at);

CREATE TABLE deals (
  id             INTEGER PRIMARY KEY,
  customer_id    INTEGER NOT NULL,
  title          TEXT NOT NULL,
  amount_cents   INTEGER NOT NULL DEFAULT 0,
  stage          TEXT NOT NULL DEFAULT 'open',
  expected_close INTEGER NOT NULL DEFAULT 0,
  closed_at      INTEGER NOT NULL DEFAULT 0,
  lost_reason    TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
);
CREATE INDEX idx_deals_customer ON deals(customer_id);

CREATE TABLE tasks (
  id          INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL,
  deal_id     INTEGER NOT NULL DEFAULT 0,
  title       TEXT NOT NULL,
  detail      TEXT NOT NULL DEFAULT '',
  ai_draft    TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT 'manual',
  status      TEXT NOT NULL DEFAULT 'open',
  due_at      INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  done_at     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_tasks_status_due ON tasks(status, due_at);

CREATE TABLE ai_jobs (
  id         INTEGER PRIMARY KEY,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL DEFAULT '',
  status     TEXT NOT NULL DEFAULT 'pending',
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  result     TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE INDEX idx_ai_jobs_status ON ai_jobs(status, id);

CREATE TABLE automation_keys (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL,
  prefix       TEXT NOT NULL,
  token_hash   TEXT NOT NULL,
  scopes       TEXT NOT NULL DEFAULT 'read,write',
  status       TEXT NOT NULL DEFAULT 'enabled',
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);
`
