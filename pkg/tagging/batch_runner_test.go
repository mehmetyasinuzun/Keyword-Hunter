package tagging

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"keywordhunter-mvp/pkg/storage"
)

func newTestDB(t *testing.T) *storage.DB {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "tagging_test.db")
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("test db create error: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func seedSearchResult(t *testing.T, db *storage.DB) int64 {
	t.Helper()
	return seedSearchResultWith(t, db, "seed title", "seed")
}

func seedSearchResultWith(t *testing.T, db *storage.DB, title, query string) int64 {
	t.Helper()

	res, err := db.GetDBConn().Exec(`
		INSERT INTO search_results (title, url, source, query, criticality, category, keyword_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, title, "http://seedtestvalidaddress1234567890abcdef.onion/path", "TestEngine", query, 1, "Genel", 0)
	if err != nil {
		t.Fatalf("seed insert error: %v", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("seed id error: %v", err)
	}
	return id
}

func TestBatchRunnerSubmit_NoValidIDs(t *testing.T) {
	db := newTestDB(t)
	r := &BatchRunner{
		db:      db,
		queue:   make(chan string, 2),
		cancels: make(map[string]context.CancelFunc),
	}

	_, err := r.Submit(context.Background(), []int64{-1, 0, 999999}, "x")
	if !errors.Is(err, ErrNoValidResultIDs) {
		t.Fatalf("expected ErrNoValidResultIDs, got %v", err)
	}
}

func TestBatchRunnerSubmit_FiltersInvalidIDs(t *testing.T) {
	db := newTestDB(t)
	validID := seedSearchResult(t, db)

	r := &BatchRunner{
		db:      db,
		queue:   make(chan string, 2),
		cancels: make(map[string]context.CancelFunc),
	}

	job, err := r.Submit(context.Background(), []int64{validID, 999999, validID}, "seed")
	if err != nil {
		t.Fatalf("submit error: %v", err)
	}
	if job.TotalCount != 1 {
		t.Fatalf("job total = %d, want 1", job.TotalCount)
	}

	stored, err := db.GetTaggingJob(job.ID)
	if err != nil {
		t.Fatalf("get job error: %v", err)
	}
	if stored.TotalCount != 1 {
		t.Fatalf("stored total = %d, want 1", stored.TotalCount)
	}

	ids, err := storage.DecodeTaggingJobIDs(stored)
	if err != nil {
		t.Fatalf("decode ids error: %v", err)
	}
	if len(ids) != 1 || ids[0] != validID {
		t.Fatalf("stored ids = %v, want [%d]", ids, validID)
	}
}
