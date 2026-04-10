package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// TaggingJob toplu etiketleme iş kaydı.
type TaggingJob struct {
	ID             string     `json:"jobId"`
	Query          string     `json:"query"`
	TotalCount     int        `json:"total"`
	ProcessedCount int        `json:"processed"`
	SuccessCount   int        `json:"success"`
	FailureCount   int        `json:"failure"`
	Status         string     `json:"status"`
	ErrorMessage   string     `json:"error,omitempty"`
	ResultIDsJSON  string     `json:"-"`
	CreatedAt      time.Time  `json:"createdAt"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
}

// CreateTaggingJob yeni bir toplu etiketleme işi oluşturur.
func (db *DB) CreateTaggingJob(job TaggingJob) error {
	_, err := db.conn.Exec(`
		INSERT INTO tagging_jobs (
			id, query, total_count, processed_count, success_count, failure_count,
			status, error_message, result_ids_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		job.ID,
		job.Query,
		job.TotalCount,
		job.ProcessedCount,
		job.SuccessCount,
		job.FailureCount,
		job.Status,
		job.ErrorMessage,
		job.ResultIDsJSON,
	)
	return err
}

// GetTaggingJob ID ile iş durumunu getirir.
func (db *DB) GetTaggingJob(jobID string) (*TaggingJob, error) {
	row := db.conn.QueryRow(`
		SELECT
			id, query, total_count, processed_count, success_count, failure_count,
			status, error_message, result_ids_json, created_at, started_at, finished_at
		FROM tagging_jobs
		WHERE id = ?
	`, jobID)

	return scanTaggingJob(row)
}

// ListTaggingJobsByStatuses durumlara göre işleri listeler.
func (db *DB) ListTaggingJobsByStatuses(statuses []string, limit int) ([]TaggingJob, error) {
	if len(statuses) == 0 {
		return []TaggingJob{}, nil
	}
	if limit <= 0 {
		limit = 100
	}

	placeholders := make([]string, 0, len(statuses))
	args := make([]interface{}, 0, len(statuses)+1)
	for _, status := range statuses {
		placeholders = append(placeholders, "?")
		args = append(args, status)
	}
	args = append(args, limit)

	query := fmt.Sprintf(`
		SELECT
			id, query, total_count, processed_count, success_count, failure_count,
			status, error_message, result_ids_json, created_at, started_at, finished_at
		FROM tagging_jobs
		WHERE status IN (%s)
		ORDER BY created_at ASC
		LIMIT ?
	`, strings.Join(placeholders, ","))

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := make([]TaggingJob, 0)
	for rows.Next() {
		job, err := scanTaggingJob(rows)
		if err != nil {
			continue
		}
		jobs = append(jobs, *job)
	}

	return jobs, nil
}

// ResetRunningTaggingJobs uygulama kapanmasından kalan running işleri pending'e çeker.
func (db *DB) ResetRunningTaggingJobs() error {
	_, err := db.conn.Exec(`
		UPDATE tagging_jobs
		SET status = 'pending', error_message = 'Uygulama yeniden başlatıldı'
		WHERE status = 'running'
	`)
	return err
}

// MarkTaggingJobRunning işi running durumuna geçirir.
func (db *DB) MarkTaggingJobRunning(jobID string) error {
	_, err := db.conn.Exec(`
		UPDATE tagging_jobs
		SET status = 'running', error_message = '', started_at = COALESCE(started_at, CURRENT_TIMESTAMP)
		WHERE id = ?
	`, jobID)
	return err
}

// IncrementTaggingJobProgress iş ilerlemesini artırır.
func (db *DB) IncrementTaggingJobProgress(jobID string, success bool) error {
	if success {
		_, err := db.conn.Exec(`
			UPDATE tagging_jobs
			SET processed_count = processed_count + 1,
				success_count = success_count + 1
			WHERE id = ?
		`, jobID)
		return err
	}

	_, err := db.conn.Exec(`
		UPDATE tagging_jobs
		SET processed_count = processed_count + 1,
			failure_count = failure_count + 1
		WHERE id = ?
	`, jobID)
	return err
}

// MarkTaggingJobFinished işi final duruma geçirir.
func (db *DB) MarkTaggingJobFinished(jobID, status, errorMessage string) error {
	_, err := db.conn.Exec(`
		UPDATE tagging_jobs
		SET status = ?, error_message = ?, finished_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, errorMessage, jobID)
	return err
}

// DecodeTaggingJobIDs job içinde saklanan sonuç ID listesini parse eder.
func DecodeTaggingJobIDs(job *TaggingJob) ([]int64, error) {
	if job == nil || strings.TrimSpace(job.ResultIDsJSON) == "" {
		return []int64{}, nil
	}

	var ids []int64
	if err := json.Unmarshal([]byte(job.ResultIDsJSON), &ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func scanTaggingJob(scanner interface {
	Scan(dest ...interface{}) error
}) (*TaggingJob, error) {
	var (
		job      TaggingJob
		started  sql.NullTime
		finished sql.NullTime
	)

	err := scanner.Scan(
		&job.ID,
		&job.Query,
		&job.TotalCount,
		&job.ProcessedCount,
		&job.SuccessCount,
		&job.FailureCount,
		&job.Status,
		&job.ErrorMessage,
		&job.ResultIDsJSON,
		&job.CreatedAt,
		&started,
		&finished,
	)
	if err != nil {
		return nil, err
	}

	if started.Valid {
		start := started.Time
		job.StartedAt = &start
	}
	if finished.Valid {
		end := finished.Time
		job.FinishedAt = &end
	}

	return &job, nil
}
