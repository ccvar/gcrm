package store

import (
	"database/sql"
	"errors"
)

// ---------- 设置（KV） ----------

func (s *Store) Setting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	// 方言无关的 upsert：先 UPDATE，无行再 INSERT（单连接下无竞态）
	res, err := s.db.Exec(`UPDATE settings SET value=? WHERE key=?`, value, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = s.db.Exec(`INSERT INTO settings(key, value) VALUES(?,?)`, key, value)
	}
	return err
}

// ---------- 自动化密钥（ccrm_ 前缀，参考 GCMS 的 gcms_/gcmsp_ 体系） ----------

type AutomationKey struct {
	ID         int64
	Name       string
	Prefix     string // 明文前 13 位，仅供辨认
	TokenHash  string // sha256 hex，明文只在创建时展示一次
	Scopes     string
	Status     string // enabled / disabled
	CreatedAt  int64
	LastUsedAt int64
}

func (s *Store) CreateAutomationKey(name, prefix, tokenHash, scopes string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO automation_keys(name, prefix, token_hash, scopes, status, created_at)
		VALUES(?,?,?,?, 'enabled', ?)`, name, prefix, tokenHash, scopes, now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) AutomationKeys() ([]AutomationKey, error) {
	rows, err := s.db.Query(`SELECT id, name, prefix, token_hash, scopes, status, created_at, last_used_at
		FROM automation_keys ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutomationKey
	for rows.Next() {
		var k AutomationKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.TokenHash, &k.Scopes, &k.Status, &k.CreatedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// AutomationKeyByHash 按哈希取启用中的密钥，供 API 鉴权。
func (s *Store) AutomationKeyByHash(tokenHash string) (*AutomationKey, error) {
	k := &AutomationKey{}
	err := s.db.QueryRow(`SELECT id, name, prefix, token_hash, scopes, status, created_at, last_used_at
		FROM automation_keys WHERE token_hash = ? AND status = 'enabled'`, tokenHash).
		Scan(&k.ID, &k.Name, &k.Prefix, &k.TokenHash, &k.Scopes, &k.Status, &k.CreatedAt, &k.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Store) TouchAutomationKey(id int64) error {
	_, err := s.db.Exec(`UPDATE automation_keys SET last_used_at=? WHERE id=?`, now(), id)
	return err
}

func (s *Store) DisableAutomationKey(id int64) error {
	_, err := s.db.Exec(`UPDATE automation_keys SET status='disabled' WHERE id=?`, id)
	return err
}
