package storage

import (
	"database/sql"
	"time"
)

// Session kalici oturum kaydi.
type Session struct {
	ID         string
	Username   string
	CSRFToken  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
}

func (db *DB) CreateSession(id, username, csrfToken string, expiresAt time.Time) error {
	_, err := db.conn.Exec(`
		INSERT INTO sessions (id, username, csrf_token, expires_at)
		VALUES (?, ?, ?, ?)
	`, id, username, csrfToken, expiresAt)
	return err
}

func (db *DB) GetSession(id string) (*Session, error) {
	var s Session
	err := db.conn.QueryRow(`
		SELECT id, username, csrf_token, expires_at, created_at, last_seen_at
		FROM sessions
		WHERE id = ?
	`, id).Scan(&s.ID, &s.Username, &s.CSRFToken, &s.ExpiresAt, &s.CreatedAt, &s.LastSeenAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (db *DB) TouchSession(id string, expiresAt time.Time) error {
	_, err := db.conn.Exec(`
		UPDATE sessions
		SET last_seen_at = CURRENT_TIMESTAMP, expires_at = ?
		WHERE id = ?
	`, expiresAt, id)
	return err
}

func (db *DB) DeleteSession(id string) error {
	_, err := db.conn.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

func (db *DB) CleanupExpiredSessions(now time.Time) (int64, error) {
	res, err := db.conn.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (db *DB) SessionExists(id string) (bool, error) {
	var dummy int
	err := db.conn.QueryRow(`SELECT 1 FROM sessions WHERE id = ?`, id).Scan(&dummy)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}
