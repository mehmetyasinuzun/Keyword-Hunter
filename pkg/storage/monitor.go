package storage

import (
	"database/sql"
	"time"
)

// EngineStatDB arama motoru istatistik modeli
type EngineStatDB struct {
	Name             string
	URL              string
	IsActive         bool
	LastCheckedAt    *time.Time
	LastStatus       string // "up", "down", "unknown"
	LastResponseMs   int
	SuccessCount     int
	FailCount        int
	TotalResults     int
	ConsecutiveFails int
	AddedAt          time.Time
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

// GetAlertConfig bir ayar değeri döndürür
func (db *DB) GetAlertConfig(key string) (string, error) {
	var value string
	err := db.conn.QueryRow("SELECT value FROM alert_config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetAlertConfig bir ayar değeri kaydeder
func (db *DB) SetAlertConfig(key, value string) error {
	_, err := db.conn.Exec(`
		INSERT INTO alert_config (key, value, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, key, value)
	return err
}

// GetAllAlertConfig tüm ayarları döndürür
func (db *DB) GetAllAlertConfig() (map[string]string, error) {
	rows, err := db.conn.Query("SELECT key, value FROM alert_config")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	config := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			config[k] = v
		}
	}
	return config, nil
}
