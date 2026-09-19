package storage

import (
	"database/sql"
	"time"
)

// CrawlJob örümcek (site tarama) iş kaydı.
type CrawlJob struct {
	ID         string     `json:"id"`
	SeedURL    string     `json:"seedUrl"`
	Query      string     `json:"query"`
	Depth      int        `json:"depth"`
	MaxPages   int        `json:"maxPages"`
	SameDomain bool       `json:"sameDomain"`
	Status     string     `json:"status"` // pending, running, completed, failed, cancelled
	PagesDone  int        `json:"pagesDone"`
	FoundCount int        `json:"foundCount"`
	SavedCount int        `json:"savedCount"`
	Message    string     `json:"message,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
}

// EnsureCrawlSchema crawl_jobs tablosunu oluşturur.
func (db *DB) EnsureCrawlSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS crawl_jobs (
			id TEXT PRIMARY KEY,
			seed_url TEXT NOT NULL,
			query TEXT DEFAULT '',
			depth INTEGER DEFAULT 1,
			max_pages INTEGER DEFAULT 50,
			same_domain INTEGER DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'pending',
			pages_done INTEGER DEFAULT 0,
			found_count INTEGER DEFAULT 0,
			saved_count INTEGER DEFAULT 0,
			message TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_crawl_jobs_status ON crawl_jobs(status);
		CREATE INDEX IF NOT EXISTS idx_crawl_jobs_created ON crawl_jobs(created_at);
	`)
	return err
}

func (db *DB) CreateCrawlJob(j CrawlJob) error {
	sd := 0
	if j.SameDomain {
		sd = 1
	}
	_, err := db.conn.Exec(`
		INSERT INTO crawl_jobs (id, seed_url, query, depth, max_pages, same_domain, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, j.ID, j.SeedURL, j.Query, j.Depth, j.MaxPages, sd, j.Status)
	return err
}

func (db *DB) MarkCrawlRunning(id string) error {
	_, err := db.conn.Exec(`UPDATE crawl_jobs SET status='running', started_at=COALESCE(started_at, CURRENT_TIMESTAMP) WHERE id=?`, id)
	return err
}

func (db *DB) UpdateCrawlProgress(id string, pages, found, saved int) error {
	_, err := db.conn.Exec(`UPDATE crawl_jobs SET pages_done=?, found_count=?, saved_count=? WHERE id=?`, pages, found, saved, id)
	return err
}

func (db *DB) MarkCrawlFinished(id, status, message string) error {
	_, err := db.conn.Exec(`UPDATE crawl_jobs SET status=?, message=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`, status, message, id)
	return err
}

// ResetRunningCrawlJobs açılışta yarım kalan işleri iptal işaretler (tek işçi,
// devam ettirilmez; kullanıcı yeniden başlatır).
func (db *DB) ResetRunningCrawlJobs() error {
	_, err := db.conn.Exec(`UPDATE crawl_jobs SET status='cancelled', message='Uygulama yeniden başlatıldı' WHERE status IN ('running','pending')`)
	return err
}

func (db *DB) GetCrawlJob(id string) (*CrawlJob, error) {
	return scanCrawlJob(db.conn.QueryRow(`
		SELECT id, seed_url, query, depth, max_pages, same_domain, status, pages_done, found_count, saved_count, message, created_at, started_at, finished_at
		FROM crawl_jobs WHERE id=?`, id))
}

func (db *DB) ListCrawlJobs(limit int) ([]CrawlJob, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.conn.Query(`
		SELECT id, seed_url, query, depth, max_pages, same_domain, status, pages_done, found_count, saved_count, message, created_at, started_at, finished_at
		FROM crawl_jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]CrawlJob, 0)
	for rows.Next() {
		j, err := scanCrawlJob(rows)
		if err == nil {
			list = append(list, *j)
		}
	}
	return list, rows.Err()
}

func scanCrawlJob(sc interface{ Scan(...interface{}) error }) (*CrawlJob, error) {
	var j CrawlJob
	var sd int
	var started, finished sql.NullTime
	err := sc.Scan(&j.ID, &j.SeedURL, &j.Query, &j.Depth, &j.MaxPages, &sd, &j.Status,
		&j.PagesDone, &j.FoundCount, &j.SavedCount, &j.Message, &j.CreatedAt, &started, &finished)
	if err != nil {
		return nil, err
	}
	j.SameDomain = sd == 1
	if started.Valid {
		j.StartedAt = &started.Time
	}
	if finished.Valid {
		j.FinishedAt = &finished.Time
	}
	return &j, nil
}
