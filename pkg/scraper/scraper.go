package scraper

import (
	"context"
	"fmt"
	"html"
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

// isOnionURL URL'nin geçerli bir http/https .onion adresi olup olmadığını doğrular (SSRF koruması)
func isOnionURL(urlStr string) bool {
	return shared.IsOnionURL(urlStr)
}

// Sık kullanılan regex'ler bir kez derlenir (her sayfada yeniden derleme maliyeti yoktu).
var (
	reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reHead   = regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	reTag    = regexp.MustCompile(`<[^>]+>`)
	reSpace  = regexp.MustCompile(`\s+`)
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
	Rendered     bool // headless tarayıcı (JS) ile alındı
}

// SiteProfile host bazlı çekim ayarları (storage.SiteProfile'ın scraper görünümü).
type SiteProfile struct {
	Cookies   string
	UserAgent string
	RenderJS  bool
}

// ProfileResolver host için profil döndürür (yoksa nil).
type ProfileResolver func(host string) *SiteProfile

// Renderer headless tarayıcı ile JS çalıştırıp DOM döndürür (capture paketi).
type Renderer interface {
	Render(ctx context.Context, targetURL, cookieHeader, userAgent string) (html, text, title string, err error)
}

// Scraper URL scraper yapısı
type Scraper struct {
	torProxy   string
	client     *http.Client
	maxChars   int
	seenHashes map[string]bool // Duplicate kontrolü
	mu         sync.RWMutex

	profiles ProfileResolver
	renderer Renderer
}

// SetProfileResolver site profillerini (çerez/UA/JS) sağlayan çözücüyü atar.
func (s *Scraper) SetProfileResolver(r ProfileResolver) { s.mu.Lock(); s.profiles = r; s.mu.Unlock() }

// SetRenderer JS render için headless tarayıcıyı atar (nil = devre dışı).
func (s *Scraper) SetRenderer(r Renderer) { s.mu.Lock(); s.renderer = r; s.mu.Unlock() }

// RendererAvailable JS render'ın etkin olup olmadığını döndürür.
func (s *Scraper) RendererAvailable() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.renderer != nil
}

// profileFor URL'nin host'u için profil döndürür.
func (s *Scraper) profileFor(rawURL string) *SiteProfile {
	s.mu.RLock()
	r := s.profiles
	s.mu.RUnlock()
	if r == nil {
		return nil
	}
	return r(shared.ExtractDomain(rawURL))
}

// ApplyProfile isteğe host profilinin çerez ve User-Agent'ını uygular.
func (s *Scraper) ApplyProfile(req *http.Request) {
	p := s.profileFor(req.URL.String())
	if p == nil {
		return
	}
	if p.Cookies != "" {
		req.Header.Set("Cookie", p.Cookies)
	}
	if p.UserAgent != "" {
		req.Header.Set("User-Agent", p.UserAgent)
	}
}

// LooksJSOnly HTML'in JavaScript olmadan anlamlı içerik vermediğini sezer:
// görünür metin çok kısa ve sayfa script/noscript/"enable javascript" izleri taşıyor.
func LooksJSOnly(htmlContent string) bool {
	text := HTMLToText(htmlContent)
	if len(text) > 600 {
		return false
	}
	lower := strings.ToLower(htmlContent)
	if !strings.Contains(lower, "<script") {
		return false
	}
	markers := []string{"<noscript", "enable javascript", "javascript is required", "requires javascript", "javascript'i etkinleştir", "id=\"root\"", "id=\"app\"", "__next", "ng-app", "data-reactroot"}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return len(text) < 120
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

// HTMLToText HTML'i düz metne çevirir (script/style/head atılır, etiketler
// kaldırılır, HTML varlıkları çözülür, boşluklar sadeleştirilir).
func HTMLToText(htmlContent string) string {
	htmlContent = reScript.ReplaceAllString(htmlContent, " ")
	htmlContent = reStyle.ReplaceAllString(htmlContent, " ")
	htmlContent = reHead.ReplaceAllString(htmlContent, " ")
	text := reTag.ReplaceAllString(htmlContent, " ")
	text = html.UnescapeString(text)
	text = reSpace.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func (s *Scraper) htmlToText(htmlContent string) string {
	return HTMLToText(htmlContent)
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
