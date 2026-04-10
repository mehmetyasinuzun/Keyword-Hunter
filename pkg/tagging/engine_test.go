package tagging

import (
	"context"
	"errors"
	"strings"
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

	if !containsTag(result.Tags, "leak") || !containsTag(result.Tags, "ransomware") {
		t.Fatalf("tags = %v, expected leak and ransomware", result.Tags)
	}
	if result.KeywordHits != 7 {
		t.Fatalf("keywordHits = %d, want 7", result.KeywordHits)
	}
	if result.Category == "Genel" {
		t.Fatalf("category should not stay Genel on strong CTI tags")
	}
	if result.Criticality < 4 {
		t.Fatalf("criticality = %d, want >= 4", result.Criticality)
	}
	if result.Confidence <= 0 {
		t.Fatalf("confidence should be populated")
	}

	stored, err := db.GetResultByID(id)
	if err != nil {
		t.Fatalf("stored result fetch error: %v", err)
	}
	if !strings.Contains(stored.AutoTags, "leak") || !strings.Contains(stored.AutoTags, "ransomware") {
		t.Fatalf("stored auto_tags = %q, expected leak and ransomware", stored.AutoTags)
	}
	if stored.KeywordCount != 7 {
		t.Fatalf("stored keyword_count = %d, want 7", stored.KeywordCount)
	}
	if stored.Category == "Genel" {
		t.Fatalf("stored category should be updated")
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

func TestEngineTagResultByID_MetadataFallbackWhenExtractorFails(t *testing.T) {
	db := newTestDB(t)
	id := seedSearchResultWith(t, db, "Corporate RDP Access for Sale", "rdp admin access")
	engine := NewEngine(db, fakeExtractor{result: scraper.TagResult{Success: false, Error: "fetch failed"}})

	result, err := engine.TagResultByID(context.Background(), id)
	if err != nil {
		t.Fatalf("expected metadata fallback success, got error: %v", err)
	}

	if result.Category != "Initial Access" {
		t.Fatalf("category = %q, want %q", result.Category, "Initial Access")
	}
	if result.Criticality < 4 {
		t.Fatalf("criticality = %d, want >= 4", result.Criticality)
	}
	if len(result.Tags) == 0 {
		t.Fatalf("expected fallback tags from classification signals")
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

func containsTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}
