package storage

import (
	"database/sql"
	"errors"
	"time"
)

// Bakım işlerinin (zamanlanmış yedekleme, saklama budaması) son çalışma
// durumu. İş başına tek satır tutulur; geçmiş denetim kaydındadır
// (backup.scheduled, retention.purge).

// Bakım işi türleri.
const (
	MaintenanceBackup    = "backup"
	MaintenanceRetention = "retention"
)

// MaintenanceRun bir bakım işinin son çalışması.
type MaintenanceRun struct {
	Kind          string     `json:"kind"`
	StartedAt     time.Time  `json:"startedAt"`
	FinishedAt    time.Time  `json:"finishedAt"`
	OK            bool       `json:"ok"`
	Message       string     `json:"message,omitempty"` // hata özeti (sır içermez)
	Details       string     `json:"details,omitempty"` // JSON: dosya adı, boyut, sayımlar
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
}

// migrateMaintenanceRuns şema 2: maintenance_runs tablosu.
func migrateMaintenanceRuns(q runner) error {
	_, err := q.Exec(`
		CREATE TABLE maintenance_runs (
			kind TEXT PRIMARY KEY,
			started_at DATETIME NOT NULL,
			finished_at DATETIME NOT NULL,
			ok INTEGER NOT NULL DEFAULT 0,
			message TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT '',
			last_success_at DATETIME
		)`)
	return err
}

// RecordMaintenanceRun işin son çalışmasını yazar; başarılıysa
// last_success_at ilerler, değilse önceki başarı zamanı korunur.
func (db *DB) RecordMaintenanceRun(r MaintenanceRun) error {
	var success any
	if r.OK {
		success = r.FinishedAt.UTC()
	}
	_, err := db.conn.Exec(`
		INSERT INTO maintenance_runs (kind, started_at, finished_at, ok, message, details, last_success_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind) DO UPDATE SET
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			ok = excluded.ok,
			message = excluded.message,
			details = excluded.details,
			last_success_at = COALESCE(excluded.last_success_at, maintenance_runs.last_success_at)
	`, r.Kind, r.StartedAt.UTC(), r.FinishedAt.UTC(), boolInt(r.OK), r.Message, r.Details, success)
	return err
}

// GetMaintenanceRun işin son çalışması; hiç çalışmadıysa nil (hata değil).
func (db *DB) GetMaintenanceRun(kind string) (*MaintenanceRun, error) {
	r := MaintenanceRun{Kind: kind}
	var ok int
	var success sql.NullTime
	err := db.conn.QueryRow(`
		SELECT started_at, finished_at, ok, message, details, last_success_at
		FROM maintenance_runs WHERE kind = ?`, kind).
		Scan(&r.StartedAt, &r.FinishedAt, &ok, &r.Message, &r.Details, &success)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.OK = ok == 1
	if success.Valid {
		t := success.Time
		r.LastSuccessAt = &t
	}
	return &r, nil
}
