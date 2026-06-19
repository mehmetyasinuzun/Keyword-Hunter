package storage

import (
	"database/sql"
	"time"
)

// Screenshot alınan bir ekran görüntüsü kaydı
type Screenshot struct {
	ID        int64     `json:"id"`
	TargetURL string    `json:"targetUrl"`
	Source    string    `json:"source"` // "watchlist", "scheduled", "manual"
	RefID     int64     `json:"refId"`  // ilgili watchlist/scheduled id (0=manuel)
	FilePath  string    `json:"filePath"`
	SHA256    string    `json:"sha256"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	Bytes     int       `json:"bytes"`
	Status        string    `json:"status"` // "ok", "error"
	ErrorMsg      string    `json:"errorMsg"`
	Title         string    `json:"title"`
	Challenge     bool      `json:"challenge"`     // captcha/Cloudflare/doğrulama ekranı mı
	ChallengeKind string    `json:"challengeKind"` // "Cloudflare", "captcha" ...
	TakenAt       time.Time `json:"takenAt"`
}

// EnsureScreenshotSchema screenshots tablosunu oluşturur
func (db *DB) EnsureScreenshotSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS screenshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target_url TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'manual',
			ref_id INTEGER NOT NULL DEFAULT 0,
			file_path TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			width INTEGER DEFAULT 0,
			height INTEGER DEFAULT 0,
			bytes INTEGER DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'ok',
			error_msg TEXT DEFAULT '',
			title TEXT DEFAULT '',
			challenge INTEGER DEFAULT 0,
			challenge_kind TEXT DEFAULT '',
			taken_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE INDEX IF NOT EXISTS idx_screenshots_target ON screenshots(target_url, taken_at);
		CREATE INDEX IF NOT EXISTS idx_screenshots_taken ON screenshots(taken_at);
	`)
	if err != nil {
		return err
	}
	// Mevcut tablolar için sütun ekleme (idempotent — "duplicate column" yutulur)
	for _, col := range []string{
		"ALTER TABLE screenshots ADD COLUMN title TEXT DEFAULT ''",
		"ALTER TABLE screenshots ADD COLUMN challenge INTEGER DEFAULT 0",
		"ALTER TABLE screenshots ADD COLUMN challenge_kind TEXT DEFAULT ''",
	} {
		_, _ = db.conn.Exec(col)
	}
	return nil
}

// SaveScreenshot yeni ekran görüntüsü kaydı ekler, ID döndürür
func (db *DB) SaveScreenshot(s Screenshot) (int64, error) {
	if s.Status == "" {
		s.Status = "ok"
	}
	if s.TakenAt.IsZero() {
		s.TakenAt = time.Now()
	}
	chInt := 0
	if s.Challenge {
		chInt = 1
	}
	res, err := db.conn.Exec(`
		INSERT INTO screenshots (target_url, source, ref_id, file_path, sha256, width, height, bytes, status, error_msg, title, challenge, challenge_kind, taken_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.TargetURL, s.Source, s.RefID, s.FilePath, s.SHA256, s.Width, s.Height, s.Bytes, s.Status, s.ErrorMsg, s.Title, chInt, s.ChallengeKind, s.TakenAt)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetRecentScreenshots son N başarılı ekran görüntüsünü döndürür
func (db *DB) GetRecentScreenshots(limit int) ([]Screenshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 60
	}
	return db.queryScreenshots(`
		SELECT id, target_url, source, ref_id, file_path, sha256, width, height, bytes, status, error_msg, title, challenge, challenge_kind, taken_at
		FROM screenshots WHERE status = 'ok'
		ORDER BY taken_at DESC LIMIT ?
	`, limit)
}

// GetScreenshotsForTarget bir hedefin görüntü geçmişini (zaman çizelgesi) döndürür
func (db *DB) GetScreenshotsForTarget(targetURL string, limit int) ([]Screenshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 30
	}
	return db.queryScreenshots(`
		SELECT id, target_url, source, ref_id, file_path, sha256, width, height, bytes, status, error_msg, title, challenge, challenge_kind, taken_at
		FROM screenshots WHERE target_url = ? AND status = 'ok'
		ORDER BY taken_at DESC LIMIT ?
	`, targetURL, limit)
}

// GetScreenshotByID tek bir görüntü kaydını döndürür (dosya servisi için)
func (db *DB) GetScreenshotByID(id int64) (*Screenshot, error) {
	rows, err := db.queryScreenshots(`
		SELECT id, target_url, source, ref_id, file_path, sha256, width, height, bytes, status, error_msg, title, challenge, challenge_kind, taken_at
		FROM screenshots WHERE id = ? LIMIT 1
	`, id)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, sql.ErrNoRows
	}
	return &rows[0], nil
}

// LatestScreenshotHash bir hedefin son başarılı görüntüsünün hash'ini döndürür
// (görsel değişiklik tespiti için). Kayıt yoksa boş string döner.
func (db *DB) LatestScreenshotHash(targetURL string) (string, error) {
	var hash string
	err := db.conn.QueryRow(`
		SELECT sha256 FROM screenshots
		WHERE target_url = ? AND status = 'ok'
		ORDER BY taken_at DESC LIMIT 1
	`, targetURL).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return hash, err
}

func (db *DB) queryScreenshots(q string, args ...interface{}) ([]Screenshot, error) {
	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []Screenshot
	for rows.Next() {
		var s Screenshot
		var chInt int
		if err := rows.Scan(&s.ID, &s.TargetURL, &s.Source, &s.RefID, &s.FilePath, &s.SHA256,
			&s.Width, &s.Height, &s.Bytes, &s.Status, &s.ErrorMsg, &s.Title, &chInt, &s.ChallengeKind, &s.TakenAt); err != nil {
			continue
		}
		s.Challenge = chInt == 1
		list = append(list, s)
	}
	return list, nil
}
