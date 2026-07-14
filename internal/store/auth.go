package store

import (
	"database/sql"
	"errors"
	"time"
)

func (s *Store) UserCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(username, passHash string) error {
	_, err := s.db.Exec(`INSERT INTO users(username, pass_hash, created_at) VALUES(?,?,?)`,
		username, passHash, now())
	return err
}

// UserByName 返回用户 ID 与密码哈希；不存在时 ok=false。
func (s *Store) UserByName(username string) (id int64, passHash string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT id, pass_hash FROM users WHERE username = ?`, username).
		Scan(&id, &passHash)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", false, nil
	}
	if err != nil {
		return 0, "", false, err
	}
	return id, passHash, true, nil
}

type Session struct {
	Token  string
	UserID int64
	CSRF   string
}

func (s *Store) CreateSession(token string, userID int64, csrf string, ttl time.Duration) error {
	t := now()
	_, err := s.db.Exec(`INSERT INTO sessions(token, user_id, csrf, created_at, expires_at) VALUES(?,?,?,?,?)`,
		token, userID, csrf, t, t+int64(ttl.Seconds()))
	return err
}

// SessionByToken 返回未过期的会话；不存在或已过期时 nil。
func (s *Store) SessionByToken(token string) (*Session, error) {
	sess := &Session{Token: token}
	err := s.db.QueryRow(`SELECT user_id, csrf FROM sessions WHERE token = ? AND expires_at > ?`, token, now()).
		Scan(&sess.UserID, &sess.CSRF)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) PurgeExpiredSessions() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now())
	return err
}
