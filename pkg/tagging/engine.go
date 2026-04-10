package tagging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/storage"
)

// AutoTagResult tekil etiketleme çıktısı.
type AutoTagResult struct {
	ResultID    int64    `json:"resultId"`
	Tags        []string `json:"tags"`
	TagsStr     string   `json:"tagsStr"`
	KeywordHits int      `json:"keywordHits"`
}

// Engine ortak etiketleme iş kurallarını içerir.
type Engine struct {
	db      *storage.DB
	scraper KeywordExtractor
	maxTags int
}

type KeywordExtractor interface {
	ExtractTopKeywords(urlStr, searchQuery string, maxTags int) scraper.TagResult
}

var (
	ErrResultNotFound   = errors.New("sonuç bulunamadı")
	ErrNoTaggableSignal = errors.New("etiket çıkarılamadı")
)

// NewEngine yeni tagging engine oluşturur.
func NewEngine(db *storage.DB, scraperClient KeywordExtractor) *Engine {
	return &Engine{
		db:      db,
		scraper: scraperClient,
		maxTags: 5,
	}
}

// TagResultByID tek bir sonucu etiketler ve veritabanına yazar.
func (e *Engine) TagResultByID(ctx context.Context, resultID int64) (*AutoTagResult, error) {
	if resultID <= 0 {
		return nil, fmt.Errorf("geçersiz result ID")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	result, err := e.db.GetResultByID(resultID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrResultNotFound
		}
		return nil, fmt.Errorf("sonuç alınamadı: %w", err)
	}

	if e.scraper == nil {
		return nil, fmt.Errorf("tag extractor yapılandırılmamış")
	}

	query := strings.TrimSpace(result.Query)
	if query == "" {
		query = strings.TrimSpace(result.Title)
	}

	tagResult := e.scraper.ExtractTopKeywords(result.URL, query, e.maxTags)
	if !tagResult.Success {
		if tagResult.Error != "" {
			return nil, fmt.Errorf("etiketleme başarısız: %s", tagResult.Error)
		}
		return nil, fmt.Errorf("etiketleme başarısız")
	}

	if len(tagResult.Tags) == 0 && tagResult.KeywordHits == 0 {
		return nil, ErrNoTaggableSignal
	}

	tagsStr := strings.Join(tagResult.Tags, ", ")

	if err := e.db.UpdateAutoTags(result.ID, tagsStr); err != nil {
		return nil, fmt.Errorf("etiketler kaydedilemedi: %w", err)
	}
	if err := e.db.UpdateKeywordCount(result.ID, tagResult.KeywordHits); err != nil {
		return nil, fmt.Errorf("keyword_count güncellenemedi: %w", err)
	}

	return &AutoTagResult{
		ResultID:    result.ID,
		Tags:        tagResult.Tags,
		TagsStr:     tagsStr,
		KeywordHits: tagResult.KeywordHits,
	}, nil
}
