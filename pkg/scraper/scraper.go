package scraper

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
)

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

// ScrapeURL tek bir URL'yi scrape eder
func (s *Scraper) ScrapeURL(urlStr, title string) Content {
	content := Content{
		URL:       urlStr,
		Title:     title,
		ScrapedAt: time.Now(),
	}

	if !strings.Contains(urlStr, ".onion") {
		content.Error = "Bu bir .onion adresi değil."
		logger.Warn("Non-onion URL: %s", urlStr)
		return content
	}

	if isLow, reason := s.isLowValueURL(urlStr); isLow {
		content.Error = fmt.Sprintf("Atlanan: %s", reason)
		logger.Debug("Skipped URL: %s (%s)", urlStr, reason)
		return content
	}

	req, err := http.NewRequest("GET", urlStr, nil)
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

	body, err := io.ReadAll(resp.Body)
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
	if s.isDuplicate(contentHash) {
		content.Error = "Skipped: duplicate content"
		return content
	}

	qualityScore := s.calculateQualityScore(text, title)

	if qualityScore < 15 {
		content.Error = fmt.Sprintf("Skipped: low quality (score: %d)", qualityScore)
		return content
	}

	if title != "" && title != "Found Link" && title != "Untitled" && !strings.HasPrefix(title, "http") {
		text = title + " - " + text
	}

	if len(text) > s.maxChars {
		text = text[:s.maxChars] + "...(truncated)"
	}

	s.markAsSeen(contentHash)

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

func (s *Scraper) ScrapeMultiple(urls []struct{ URL, Title string }, maxWorkers int, progressFn func(done, total int)) []Content {
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
				content := s.ScrapeURL(job.URL, job.Title)

				mu.Lock()
				results = append(results, content)
				done++
				if progressFn != nil {
					progressFn(done, len(urls))
				}
				mu.Unlock()

				time.Sleep(time.Duration(500+rand.Intn(1000)) * time.Millisecond)
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

func (s *Scraper) ResetSeenHashes() {
	s.mu.Lock()
	s.seenHashes = make(map[string]bool)
	s.mu.Unlock()
}
