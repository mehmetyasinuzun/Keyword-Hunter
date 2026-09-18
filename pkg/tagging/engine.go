package tagging

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"keywordhunter-mvp/pkg/artifact"
	"keywordhunter-mvp/pkg/cti"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/storage"
)

// AutoTagResult tekil etiketleme çıktısı.
type AutoTagResult struct {
	ResultID    int64    `json:"resultId"`
	Tags        []string `json:"tags"`
	TagsStr     string   `json:"tagsStr"`
	KeywordHits int      `json:"keywordHits"`
	Category    string   `json:"category"`
	Criticality int      `json:"criticality"`
	Confidence  int      `json:"confidence"`
	Artifacts   int      `json:"artifacts"` // çıkarılan IOC sayısı
}

// Engine ortak etiketleme iş kurallarını içerir.
type Engine struct {
	db      *storage.DB
	scraper KeywordExtractor
	maxTags int
}

type KeywordExtractor interface {
	ExtractTopKeywords(ctx context.Context, urlStr, searchQuery string, maxTags int) scraper.TagResult
}

var (
	ErrResultNotFound   = errors.New("sonuç bulunamadı")
	ErrNoTaggableSignal = errors.New("etiket çıkarılamadı")
	// ErrFetchFailed sayfa Tor üzerinden çekilemedi ve meta verilerden de sinyal çıkmadı.
	ErrFetchFailed = errors.New("sayfa çekilemedi")
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

	tagResult := e.scraper.ExtractTopKeywords(ctx, result.URL, query, e.maxTags)
	extractedTags := []string{}
	keywordHits := 0
	extractionErr := ""

	if tagResult.Success {
		extractedTags = tagResult.Tags
		keywordHits = tagResult.KeywordHits
	} else if tagResult.Error != "" {
		extractionErr = tagResult.Error
	}

	analysis := cti.Analyze(result.Title, result.URL, query, extractedTags, keywordHits)
	finalTags := cti.MergeTags(extractedTags, analysis.MatchedSignals, query, e.maxTags)

	if len(finalTags) == 0 && keywordHits == 0 && analysis.Category == "Genel" {
		if extractionErr != "" {
			return nil, fmt.Errorf("%w: %s", ErrFetchFailed, extractionErr)
		}
		return nil, ErrNoTaggableSignal
	}

	finalCategory := analysis.Category
	finalCriticality := analysis.Criticality

	// Mevcut kayıt zaten daha özel bir kategoriye sahipse, zayıf sınıflandırmada geri düşürme.
	if finalCategory == "Genel" {
		if strings.TrimSpace(result.Category) != "" && !strings.EqualFold(result.Category, "Genel") {
			finalCategory = result.Category
		}
		if result.Criticality >= 1 && result.Criticality <= 5 {
			finalCriticality = result.Criticality
		}
	}

	tagsStr := strings.Join(finalTags, ", ")

	if err := e.db.ApplyTagging(result.ID, tagsStr, keywordHits, finalCriticality, finalCategory); err != nil {
		return nil, fmt.Errorf("etiketleme sonucu kaydedilemedi: %w", err)
	}

	// IOC / artifact çıkarımı (sayfa metni alınabildiyse)
	artifactCount := 0
	if tagResult.Success && tagResult.Text != "" {
		found := artifact.NewExtractor().Extract(tagResult.Text, result.URL)
		found = artifact.FilterByConfidence(found, 0.5)
		items := make([]storage.ResultArtifact, 0, len(found))
		for _, a := range found {
			items = append(items, storage.ResultArtifact{
				Type: string(a.Type), Value: a.Value, Context: a.Context, Confidence: a.Confidence,
			})
		}
		if n, err := e.db.ReplaceArtifacts(result.ID, items); err == nil {
			artifactCount = n
		}
	}

	return &AutoTagResult{
		ResultID:    result.ID,
		Tags:        finalTags,
		TagsStr:     tagsStr,
		KeywordHits: keywordHits,
		Category:    finalCategory,
		Criticality: finalCriticality,
		Confidence:  analysis.Confidence,
		Artifacts:   artifactCount,
	}, nil
}
