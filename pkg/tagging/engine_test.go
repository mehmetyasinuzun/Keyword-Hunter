package tagging

import (
	"context"
	"errors"
	"testing"

	"keywordhunter-mvp/pkg/scraper"
)

type fakeExtractor struct {
	result scraper.TagResult
}

func (f fakeExtractor) ExtractTopKeywords(urlStr, searchQuery string, maxTags int) scraper.TagResult {
	return f.result
}

func TestEngineTagResultByID_Success(t *testing.T) {
	db := newTestDB(t)
	id := seedSearchResult(t, db)

	engine := NewEngine(db, fakeExtractor{result: scraper.TagResult{
		Tags:        []string{"leak", "ransomware"},
		KeywordHits: 7,
		Success:     true,
	}})

	result, err := engine.TagResultByID(context.Background(), id)
	if err != nil {
		t.Fatalf("tagging error: %v", err)
	}

	if result.TagsStr != "leak, ransomware" {
		t.Fatalf("tagsStr = %q, want %q", result.TagsStr, "leak, ransomware")
	}
	if result.KeywordHits != 7 {
		t.Fatalf("keywordHits = %d, want 7", result.KeywordHits)
	}

	stored, err := db.GetResultByID(id)
	if err != nil {
		t.Fatalf("stored result fetch error: %v", err)
	}
	if stored.AutoTags != "leak, ransomware" {
		t.Fatalf("stored auto_tags = %q, want %q", stored.AutoTags, "leak, ransomware")
	}
	if stored.KeywordCount != 7 {
		t.Fatalf("stored keyword_count = %d, want 7", stored.KeywordCount)
	}
}

func TestEngineTagResultByID_NotFound(t *testing.T) {
	db := newTestDB(t)
	engine := NewEngine(db, fakeExtractor{result: scraper.TagResult{Success: true}})

	_, err := engine.TagResultByID(context.Background(), 999999)
	if !errors.Is(err, ErrResultNotFound) {
		t.Fatalf("expected ErrResultNotFound, got %v", err)
	}
}

func TestEngineTagResultByID_NoTagSignal(t *testing.T) {
	db := newTestDB(t)
	id := seedSearchResult(t, db)
	engine := NewEngine(db, fakeExtractor{result: scraper.TagResult{Success: true}})

	_, err := engine.TagResultByID(context.Background(), id)
	if !errors.Is(err, ErrNoTaggableSignal) {
		t.Fatalf("expected ErrNoTaggableSignal, got %v", err)
	}
}

func TestEngineTagResultByID_ExtractorFailure(t *testing.T) {
	db := newTestDB(t)
	id := seedSearchResult(t, db)
	engine := NewEngine(db, fakeExtractor{result: scraper.TagResult{Success: false, Error: "fetch failed"}})

	_, err := engine.TagResultByID(context.Background(), id)
	if err == nil {
		t.Fatalf("expected extractor failure error")
	}
}

func TestEngineTagResultByID_NoExtractorConfigured(t *testing.T) {
	db := newTestDB(t)
	id := seedSearchResult(t, db)
	engine := &Engine{db: db, scraper: nil, maxTags: 5}

	_, err := engine.TagResultByID(context.Background(), id)
	if err == nil {
		t.Fatalf("expected no extractor error")
	}
}
