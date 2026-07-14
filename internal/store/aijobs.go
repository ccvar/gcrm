package store

import (
	"database/sql"
	"errors"
)

// AIJob 是异步 AI 任务：交互提取、草稿生成等都走这张队列表，
// 失败自动重试（最多 3 次），不阻塞页面请求。
type AIJob struct {
	ID        int64
	Kind      string // interaction_extract / ...
	Payload   string // JSON
	Status    string // pending / running / done / failed
	Attempts  int
	LastError string
	Result    string
}

func (s *Store) EnqueueAIJob(kind, payload string) (int64, error) {
	t := now()
	res, err := s.db.Exec(`INSERT INTO ai_jobs(kind, payload, status, created_at, updated_at)
		VALUES(?,?, 'pending', ?, ?)`, kind, payload, t, t)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ClaimAIJob 取下一个待执行任务并标记 running；无任务时返回 nil。
// 单进程单连接（MaxOpenConns=1），SELECT+UPDATE 无竞态。
func (s *Store) ClaimAIJob() (*AIJob, error) {
	j := &AIJob{}
	err := s.db.QueryRow(`SELECT id, kind, payload, attempts FROM ai_jobs
		WHERE status = 'pending' ORDER BY id ASC LIMIT 1`).
		Scan(&j.ID, &j.Kind, &j.Payload, &j.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`UPDATE ai_jobs SET status='running', attempts=attempts+1, updated_at=? WHERE id=?`, now(), j.ID)
	if err != nil {
		return nil, err
	}
	j.Attempts++
	return j, nil
}

func (s *Store) FinishAIJob(id int64, result string) error {
	_, err := s.db.Exec(`UPDATE ai_jobs SET status='done', result=?, last_error='', updated_at=? WHERE id=?`, result, now(), id)
	return err
}

// FailAIJob 记录错误；未达重试上限回到 pending，否则终态 failed。
func (s *Store) FailAIJob(id int64, attempts int, errMsg string) error {
	status := "pending"
	if attempts >= 3 {
		status = "failed"
	}
	_, err := s.db.Exec(`UPDATE ai_jobs SET status=?, last_error=?, updated_at=? WHERE id=?`, status, errMsg, now(), id)
	return err
}
