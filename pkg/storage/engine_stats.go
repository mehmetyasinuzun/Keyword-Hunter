package storage

import (
	"database/sql"
	"time"
)

// EngineStatDB arama motoru sağlık/istatistik modeli
type EngineStatDB struct {
	Name             string     `json:"name"`
	URL              string     `json:"url"`
	IsActive         bool       `json:"isActive"`
	LastCheckedAt    *time.Time `json:"lastCheckedAt"`
	LastStatus       string     `json:"lastStatus"` // "up", "down", "unknown"
	LastResponseMs   int        `json:"lastResponseMs"`
	SuccessCount     int        `json:"successCount"`
	FailCount        int        `json:"failCount"`
	TotalResults     int        `json:"totalResults"`
	ConsecutiveFails int        `json:"consecutiveFails"`
	AddedAt          time.Time  `json:"addedAt"`
}

// EnsureEngineStatsSchema engine_stats tablosunu oluşturur
func (db *DB) EnsureEngineStatsSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS engine_stats (
			name TEXT PRIMARY KEY,
			url TEXT NOT NULL,
			is_active INTEGER DEFAULT 1,
			last_checked_at DATETIME,
			last_status TEXT DEFAULT 'unknown',
			last_response_ms INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			fail_count INTEGER DEFAULT 0,
			total_results INTEGER DEFAULT 0,
			consecutive_fails INTEGER DEFAULT 0,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

// UpsertEngineStat motor kaydını oluşturur veya günceller
func (db *DB) UpsertEngineStat(name, url string) error {
	_, err := db.conn.Exec(`
		INSERT INTO engine_stats (name, url, is_active)
		VALUES (?, ?, 1)
		ON CONFLICT(name) DO UPDATE SET url = excluded.url
	`, name, url)
	return err
}

// UpdateEngineCheck engine health check sonucunu kaydeder
func (db *DB) UpdateEngineCheck(name, status string, responseMs int, resultCount int) error {
	now := time.Now()
	if status == "up" {
		_, err := db.conn.Exec(`
			UPDATE engine_stats SET
				last_checked_at = ?,
				last_status = ?,
				last_response_ms = ?,
				success_count = success_count + 1,
				total_results = total_results + ?,
				consecutive_fails = 0
			WHERE name = ?
		`, now, status, responseMs, resultCount, name)
		return err
	}
	_, err := db.conn.Exec(`
		UPDATE engine_stats SET
			last_checked_at = ?,
			last_status = ?,
			last_response_ms = ?,
			fail_count = fail_count + 1,
			consecutive_fails = consecutive_fails + 1
		WHERE name = ?
	`, now, status, responseMs, name)
	return err
}

// SetEngineActive motoru aktif/pasif yapar
func (db *DB) SetEngineActive(name string, active bool) error {
	v := 0
	if active {
		v = 1
	}
	_, err := db.conn.Exec("UPDATE engine_stats SET is_active = ? WHERE name = ?", v, name)
	return err
}

// GetAllEngineStats tüm motorların istatistiklerini döndürür
func (db *DB) GetAllEngineStats() ([]EngineStatDB, error) {
	rows, err := db.conn.Query(`
		SELECT name, url, is_active, last_checked_at, last_status,
		       last_response_ms, success_count, fail_count, total_results,
		       consecutive_fails, added_at
		FROM engine_stats
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []EngineStatDB
	for rows.Next() {
		var s EngineStatDB
		var lastChecked sql.NullTime
		var isActiveInt int
		err := rows.Scan(
			&s.Name, &s.URL, &isActiveInt, &lastChecked, &s.LastStatus,
			&s.LastResponseMs, &s.SuccessCount, &s.FailCount, &s.TotalResults,
			&s.ConsecutiveFails, &s.AddedAt,
		)
		if err != nil {
			continue
		}
		s.IsActive = isActiveInt == 1
		if lastChecked.Valid {
			s.LastCheckedAt = &lastChecked.Time
		}
		stats = append(stats, s)
	}
	return stats, nil
}
