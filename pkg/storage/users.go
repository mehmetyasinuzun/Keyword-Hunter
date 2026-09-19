package storage

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Rol sabitleri.
const (
	RoleAdmin   = "admin"   // her şey + kullanıcı yönetimi + ayarlar
	RoleAnalyst = "analyst" // arama, etiketleme, örümcek, izleme, planlı (yazma) — ayar/kullanıcı hariç
	RoleViewer  = "viewer"  // yalnız okuma
)

// ValidRole rolün geçerli olup olmadığını döndürür.
func ValidRole(r string) bool {
	switch r {
	case RoleAdmin, RoleAnalyst, RoleViewer:
		return true
	}
	return false
}

// ErrUserExists kullanıcı adı zaten kayıtlı.
var ErrUserExists = errors.New("kullanıcı adı zaten var")

// User yönetici/analist hesabı.
type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	Enabled      bool       `json:"enabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt"`
}

// EnsureUserSchema users tablosunu oluşturur.
func (db *DB) EnsureUserSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'analyst',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_login_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_users_role ON users(role);
	`)
	return err
}

// CountUsers toplam kullanıcı sayısı (bootstrap kararı için).
func (db *DB) CountUsers() (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser yeni kullanıcı ekler. Kullanıcı adı benzersizdir (büyük/küçük harf duyarsız).
func (db *DB) CreateUser(username, passwordHash, role string) (*User, error) {
	username = strings.TrimSpace(username)
	if !ValidRole(role) {
		role = RoleAnalyst
	}
	res, err := db.conn.Exec(`INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, username, passwordHash, role)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrUserExists
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return db.GetUserByID(id)
}

// GetUserByUsername kullanıcıyı ada göre getirir (giriş için).
func (db *DB) GetUserByUsername(username string) (*User, error) {
	return scanUser(db.conn.QueryRow(`
		SELECT id, username, password_hash, role, enabled, created_at, last_login_at
		FROM users WHERE username = ? COLLATE NOCASE`, strings.TrimSpace(username)))
}

// GetUserByID kullanıcıyı ID ile getirir.
func (db *DB) GetUserByID(id int64) (*User, error) {
	return scanUser(db.conn.QueryRow(`
		SELECT id, username, password_hash, role, enabled, created_at, last_login_at
		FROM users WHERE id = ?`, id))
}

// ListUsers tüm kullanıcıları döndürür.
func (db *DB) ListUsers() ([]User, error) {
	rows, err := db.conn.Query(`
		SELECT id, username, password_hash, role, enabled, created_at, last_login_at
		FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]User, 0)
	for rows.Next() {
		u, err := scanUserRows(rows)
		if err == nil {
			list = append(list, *u)
		}
	}
	return list, rows.Err()
}

// UpdateUserPassword parolayı değiştirir.
func (db *DB) UpdateUserPassword(id int64, passwordHash string) error {
	_, err := db.conn.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	return err
}

// UpdateUserRole rolü değiştirir.
func (db *DB) UpdateUserRole(id int64, role string) error {
	if !ValidRole(role) {
		return errors.New("geçersiz rol")
	}
	_, err := db.conn.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, id)
	return err
}

// SetUserEnabled kullanıcıyı aktif/pasif yapar.
func (db *DB) SetUserEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := db.conn.Exec(`UPDATE users SET enabled = ? WHERE id = ?`, v, id)
	return err
}

// DeleteUser kullanıcıyı ve oturumlarını siler.
func (db *DB) DeleteUser(id int64) error {
	u, err := db.GetUserByID(id)
	if err != nil {
		return err
	}
	if _, err := db.conn.Exec(`DELETE FROM sessions WHERE username = ?`, u.Username); err != nil {
		return err
	}
	_, err = db.conn.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// TouchUserLogin son giriş zamanını günceller.
func (db *DB) TouchUserLogin(username string) error {
	_, err := db.conn.Exec(`UPDATE users SET last_login_at = ? WHERE username = ? COLLATE NOCASE`, time.Now().UTC(), strings.TrimSpace(username))
	return err
}

// CountAdmins etkin admin sayısı (son admini silmeyi/pasifleştirmeyi önlemek için).
func (db *DB) CountAdmins() (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND enabled = 1`).Scan(&n)
	return n, err
}

type scanner interface{ Scan(...interface{}) error }

func scanUser(sc scanner) (*User, error) {
	var u User
	var enabled int
	var last sql.NullTime
	err := sc.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &enabled, &u.CreatedAt, &last)
	if err != nil {
		return nil, err
	}
	u.Enabled = enabled == 1
	if last.Valid {
		u.LastLoginAt = &last.Time
	}
	return &u, nil
}

func scanUserRows(rows *sql.Rows) (*User, error) { return scanUser(rows) }
