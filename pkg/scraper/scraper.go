package scraper

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
)

// isOnionURL URL'nin geçerli bir http/https .onion adresi olup olmadığını doğrular (SSRF koruması)
func isOnionURL(urlStr string) bool {
	u, err := url.Parse(strings.TrimSpace(urlStr))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.HasSuffix(u.Hostname(), ".onion")
}

// Content scrape edilmiş içerik
type Content struct {
	URL          string
	Title        string
	RawContent   string
	ContentSize  int
	ContentHash  string // Duplicate kontrolü için
	QualityScore int    // Kalite skoru
	ScrapedAt    time.Time
	Success      bool
	Error        string
}

// Scraper URL scraper yapısı
type Scraper struct {
	torProxy   string
	client     *http.Client
	maxChars   int
	seenHashes map[string]bool // Duplicate kontrolü
	mu         sync.RWMutex
}

// New yeni Scraper oluşturur
func New(torProxy string) (*Scraper, error) {
	client, err := shared.NewHTTPClient(torProxy)
	if err != nil {
		return nil, fmt.Errorf("HTTP client oluşturma hatası: %w", err)
	}

	logger.Info("Scraper initialized (timeout: %v)", shared.HTTPTimeout)

	return &Scraper{
		torProxy:   torProxy,
		client:     client,
		maxChars:   8000,
		seenHashes: make(map[string]bool),
	}, nil
}

// isLowValueURL düşük değerli URL kontrolü
func (s *Scraper) isLowValueURL(urlStr string) (bool, string) {
	return shared.IsLowValueURL(urlStr)
}

// HTTPClient Tor üzerinden istek atan paylaşılan HTTP client'ı döndürür
func (s *Scraper) HTTPClient() *http.Client {
	return s.client
}

// ScrapeURL tek bir URL'yi scrape eder
func (s *Scraper) ScrapeURL(ctx context.Context, urlStr, title string) Content {
	content := Content{
		URL:       urlStr,
		Title:     title,
		ScrapedAt: time.Now(),
	}

	if !isOnionURL(urlStr) {
		content.Error = "Bu bir .onion adresi değil."
		logger.Warn("Non-onion URL: %s", urlStr)
		return content
	}

	if isLow, reason := s.isLowValueURL(urlStr); isLow {
		content.Error = fmt.Sprintf("Atlanan: %s", reason)
		logger.Debug("Skipped URL: %s (%s)", urlStr, reason)
		return content
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		content.Error = fmt.Sprintf("İstek hatası: %v", err)
		return content
	}

	req.Header.Set("User-Agent", shared.RandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := shared.DoWithRetry(s.client, req)
	if err != nil {
		content.Error = shared.ClassifyError(err).Error()
		return content
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		content.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		return content
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, shared.MaxResponseBytes))
	if err != nil {
		content.Error = fmt.Sprintf("Read error: %v", err)
		return content
	}

	// 6. Binary içerik kontrolü
	if len(body) > 4 {
		if (body[0] == 0x89 && body[1] == 0x50) ||
			(body[0] == 0xFF && body[1] == 0xD8) ||
			(body[0] == 0x47 && body[1] == 0x49) ||
			(body[0] == 0x50 && body[1] == 0x4B) ||
			(body[0] == 0x25 && body[1] == 0x50) {
			content.Error = "Skipped: binary content"
			return content
		}
	}

	text := s.htmlToText(string(body))

	if len(text) < 100 {
		content.Error = "Skipped: content too short (<100 chars)"
		return content
	}

	contentHash := generateContentHash(text)

	qualityScore := s.calculateQualityScore(text, title)

	if qualityScore < 15 {
		content.Error = fmt.Sprintf("Skipped: low quality (score: %d)", qualityScore)
		return content
	}

	// Atomik duplicate kontrolü + işaretleme (TOCTOU önlenir)
	if !s.markIfNew(contentHash) {
		content.Error = "Skipped: duplicate content"
		return content
	}

	if title != "" && title != "Found Link" && title != "Untitled" && !strings.HasPrefix(title, "http") {
		text = title + " - " + text
	}

	if len([]rune(text)) > s.maxChars {
		text = shared.TruncateRunes(text, s.maxChars) + "...(truncated)"
	}

	content.RawContent = text
	content.ContentSize = len(text)
	content.ContentHash = contentHash
	content.QualityScore = qualityScore
	content.Success = true

	return content
}

func (s *Scraper) htmlToText(html string) string {
	scriptRegex := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRegex.ReplaceAllString(html, " ")

	styleRegex := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRegex.ReplaceAllString(html, " ")

	headRegex := regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	html = headRegex.ReplaceAllString(html, " ")

	tagRegex := regexp.MustCompile(`<[^>]+>`)
	text := tagRegex.ReplaceAllString(html, " ")

	// HTML entity replacements (simplified)
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	// ... Add detailed replacements if needed, but basic text is fine for new structure

	spaceRegex := regexp.MustCompile(`\s+`)
	text = spaceRegex.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

func (s *Scraper) ScrapeMultiple(ctx context.Context, urls []struct{ URL, Title string }, maxWorkers int, progressFn func(done, total int)) []Content {
	var results []Content
	var mu sync.Mutex
	var wg sync.WaitGroup

	jobs := make(chan struct{ URL, Title string }, len(urls))
	done := 0

	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}

				content := s.ScrapeURL(ctx, job.URL, job.Title)

				mu.Lock()
				results = append(results, content)
				done++
				if progressFn != nil {
					progressFn(done, len(urls))
				}
				mu.Unlock()

				// İstekler arası gecikme (iptal-edilebilir)
				select {
				case <-time.After(time.Duration(500+rand.Intn(1000)) * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	for _, u := range urls {
		jobs <- u
	}
	close(jobs)

	wg.Wait()
	return results
}
