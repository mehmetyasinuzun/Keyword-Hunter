package storage

import (
	"database/sql"
	"time"
)

// ScheduledSearch zamanlanmış tarama modeli
type ScheduledSearch struct {
	ID              int64
	Query           string
	IntervalMinutes int
	Enabled         bool
	WebhookURL      string
	AlertThreshold  int
	LastRunAt       *time.Time
	NextRunAt       *time.Time
	LastResultCount int
	LastNewCount    int
	TotalRuns       int
	CreatedAt       time.Time
}

// CreateScheduledSearch yeni zamanlanmış tarama oluşturur
func (db *DB) CreateScheduledSearch(query string, intervalMinutes int, webhookURL string, threshold int) (*ScheduledSearch, error) {
	now := time.Now()
	nextRun := now.Add(time.Duration(intervalMinutes) * time.Minute)

	result, err := db.conn.Exec(`
		INSERT INTO scheduled_searches (query, interval_minutes, enabled, webhook_url, alert_threshold, next_run_at)
		VALUES (?, ?, 1, ?, ?, ?)
	`, query, intervalMinutes, webhookURL, threshold, nextRun)
	if err != nil {
		return nil, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}

	return db.GetScheduledSearch(id)
}

// GetScheduledSearch ID ile zamanlanmış tarama getirir
func (db *DB) GetScheduledSearch(id int64) (*ScheduledSearch, error) {
	var s ScheduledSearch
	var enabledInt int
	var lastRun, nextRun sql.NullTime

	err := db.conn.QueryRow(`
		SELECT id, query, interval_minutes, enabled, webhook_url, alert_threshold,
		       last_run_at, next_run_at, last_result_count, last_new_count, total_runs, created_at
		FROM scheduled_searches WHERE id = ?
	`, id).Scan(
		&s.ID, &s.Query, &s.IntervalMinutes, &enabledInt, &s.WebhookURL, &s.AlertThreshold,
		&lastRun, &nextRun, &s.LastResultCount, &s.LastNewCount, &s.TotalRuns, &s.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	s.Enabled = enabledInt == 1
	if lastRun.Valid {
		s.LastRunAt = &lastRun.Time
	}
	if nextRun.Valid {
		s.NextRunAt = &nextRun.Time
	}
	return &s, nil
}

// GetAllScheduledSearches tüm zamanlanmış taramaları getirir
func (db *DB) GetAllScheduledSearches() ([]ScheduledSearch, error) {
	rows, err := db.conn.Query(`
		SELECT id, query, interval_minutes, enabled, webhook_url, alert_threshold,
		       last_run_at, next_run_at, last_result_count, last_new_count, total_runs, created_at
		FROM scheduled_searches
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ScheduledSearch
	for rows.Next() {
		var s ScheduledSearch
		var enabledInt int
		var lastRun, nextRun sql.NullTime
		err := rows.Scan(
			&s.ID, &s.Query, &s.IntervalMinutes, &enabledInt, &s.WebhookURL, &s.AlertThreshold,
			&lastRun, &nextRun, &s.LastResultCount, &s.LastNewCount, &s.TotalRuns, &s.CreatedAt,
		)
		if err != nil {
			continue
		}
		s.Enabled = enabledInt == 1
		if lastRun.Valid {
			s.LastRunAt = &lastRun.Time
		}
		if nextRun.Valid {
			s.NextRunAt = &nextRun.Time
		}
		list = append(list, s)
	}
	return list, nil
}

// GetDueScheduledSearches çalışması gereken zamanlanmış taramaları döndürür
func (db *DB) GetDueScheduledSearches() ([]ScheduledSearch, error) {
	rows, err := db.conn.Query(`
		SELECT id, query, interval_minutes, enabled, webhook_url, alert_threshold,
		       last_run_at, next_run_at, last_result_count, last_new_count, total_runs, created_at
		FROM scheduled_searches
		WHERE enabled = 1 AND (next_run_at IS NULL OR next_run_at <= CURRENT_TIMESTAMP)
		ORDER BY next_run_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []ScheduledSearch
	for rows.Next() {
		var s ScheduledSearch
		var enabledInt int
		var lastRun, nextRun sql.NullTime
		err := rows.Scan(
			&s.ID, &s.Query, &s.IntervalMinutes, &enabledInt, &s.WebhookURL, &s.AlertThreshold,
			&lastRun, &nextRun, &s.LastResultCount, &s.LastNewCount, &s.TotalRuns, &s.CreatedAt,
		)
		if err != nil {
			continue
		}
		s.Enabled = enabledInt == 1
		if lastRun.Valid {
			s.LastRunAt = &lastRun.Time
		}
		if nextRun.Valid {
			s.NextRunAt = &nextRun.Time
		}
		list = append(list, s)
	}
	return list, nil
}

// UpdateScheduledSearchAfterRun tarama tamamlandıktan sonra istatistikleri günceller
func (db *DB) UpdateScheduledSearchAfterRun(id int64, resultCount, newCount int) error {
	now := time.Now()

	var intervalMinutes int
	err := db.conn.QueryRow("SELECT interval_minutes FROM scheduled_searches WHERE id = ?", id).Scan(&intervalMinutes)
	if err != nil {
		return err
	}

	nextRun := now.Add(time.Duration(intervalMinutes) * time.Minute)

	_, err = db.conn.Exec(`
		UPDATE scheduled_searches SET
			last_run_at = ?,
			next_run_at = ?,
			last_result_count = ?,
			last_new_count = ?,
			total_runs = total_runs + 1
		WHERE id = ?
	`, now, nextRun, resultCount, newCount, id)
	return err
}

// ToggleScheduledSearch aktif/pasif durumunu değiştirir
func (db *DB) ToggleScheduledSearch(id int64) (bool, error) {
	var current int
	err := db.conn.QueryRow("SELECT enabled FROM scheduled_searches WHERE id = ?", id).Scan(&current)
	if err != nil {
		return false, err
	}
	newVal := 1
	if current == 1 {
		newVal = 0
	}
	_, err = db.conn.Exec("UPDATE scheduled_searches SET enabled = ? WHERE id = ?", newVal, id)
	return newVal == 1, err
}

// DeleteScheduledSearch zamanlanmış taramayı siler
func (db *DB) DeleteScheduledSearch(id int64) error {
	_, err := db.conn.Exec("DELETE FROM scheduled_searches WHERE id = ?", id)
	return err
}

// GetKnownURLsForQuery belirli bir sorgu için bilinen URL'leri set olarak döndürür (diff için)
func (db *DB) GetKnownURLsForQuery(query string) (map[string]bool, error) {
	rows, err := db.conn.Query("SELECT url FROM search_results WHERE query = ?", query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := make(map[string]bool)
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err == nil {
			known[u] = true
		}
	}
	return known, nil
}
