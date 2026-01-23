package scraper

import (
	"crypto/md5"
	"encoding/hex"
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

// New yeni Scraper oluşturur - shared paketi kullanır
func New(torProxy string) (*Scraper, error) {
	client, err := shared.NewHTTPClient(torProxy)
	if err != nil {
		return nil, fmt.Errorf("HTTP client oluşturma hatası: %w", err)
	}

	logger.Info("🕷️ Scraper başlatıldı (timeout: %v)", shared.HTTPTimeout)

	return &Scraper{
		torProxy:   torProxy,
		client:     client,
		maxChars:   8000,
		seenHashes: make(map[string]bool),
	}, nil
}

// isLowValueURL düşük değerli URL kontrolü - shared paketi kullanır
func (s *Scraper) isLowValueURL(urlStr string) (bool, string) {
	return shared.IsLowValueURL(urlStr)
}

// calculateQualityScore içerik kalite skoru hesaplar - shared paketi kullanır
func (s *Scraper) calculateQualityScore(text string, title string) int {
	lowerText := strings.ToLower(text)
	lowerTitle := strings.ToLower(title)
	score := 0

	// 1. Uzunluk bazlı skor
	textLen := len(text)
	if textLen > 3000 {
		score += 15
	} else if textLen > 1500 {
		score += 10
	} else if textLen > 500 {
		score += 5
	} else if textLen < 200 {
		score -= 10
	}

	// 2. Yüksek değerli anahtar kelime kontrolü - shared paketi
	keywordMatches := 0
	for _, keyword := range shared.CTIKeywords {
		if strings.Contains(lowerText, keyword) || strings.Contains(lowerTitle, keyword) {
			keywordMatches++
			score += 5
		}
	}
	if keywordMatches >= 5 {
		score += 15
	} else if keywordMatches >= 3 {
		score += 10
	}

	// 3. Potansiyel veri sızıntısı belirteçleri
	if containsEmailPattern(lowerText) {
		score += 20
	}
	if containsIPAddress(lowerText) {
		score += 10
	}
	if containsHashPattern(lowerText) {
		score += 15
	}
	if containsBase64Pattern(text) {
		score += 10
	}

	// 4. Spam içerik cezası - shared paketi
	spamCount := 0
	for _, spam := range shared.SpamIndicators {
		if strings.Contains(lowerText, spam) {
			spamCount++
			score -= 10
		}
	}
	if spamCount >= 3 {
		score -= 20
	}

	// 5. Unique içerik bonusu
	words := strings.Fields(lowerText)
	if len(words) > 50 {
		uniqueWords := make(map[string]bool)
		for _, w := range words {
			uniqueWords[w] = true
		}
		uniqueRatio := float64(len(uniqueWords)) / float64(len(words))
		if uniqueRatio > 0.4 {
			score += 10
		} else if uniqueRatio < 0.2 {
			score -= 10
		}
	}

	// 6. Market/ürün sayfası cezası
	if strings.Contains(lowerText, "add to cart") ||
		strings.Contains(lowerText, "buy now") ||
		strings.Contains(lowerText, "&#36;") ||
		strings.Contains(lowerText, "quantity:") {
		score -= 15
	}

	if score < 0 {
		score = 0
	}

	return score
}

// containsEmailPattern email pattern kontrolü
func containsEmailPattern(text string) bool {
	emailRegex := regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	matches := emailRegex.FindAllString(text, -1)
	return len(matches) >= 2
}

// containsIPAddress IP adresi kontrolü
func containsIPAddress(text string) bool {
	ipRegex := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	matches := ipRegex.FindAllString(text, -1)
	return len(matches) >= 2
}

// containsHashPattern hash pattern kontrolü
func containsHashPattern(text string) bool {
	hashRegex := regexp.MustCompile(`\b[a-fA-F0-9]{32,64}\b`)
	matches := hashRegex.FindAllString(text, -1)
	return len(matches) >= 2
}

// containsBase64Pattern Base64 içerik kontrolü
func containsBase64Pattern(text string) bool {
	base64Regex := regexp.MustCompile(`[A-Za-z0-9+/]{50,}={0,2}`)
	return base64Regex.MatchString(text)
}

// generateContentHash içerik hash'i oluşturur
func generateContentHash(text string) string {
	normalizedText := strings.ToLower(strings.TrimSpace(text))
	if len(normalizedText) > 1000 {
		normalizedText = normalizedText[:1000]
	}
	hash := md5.Sum([]byte(normalizedText))
	return hex.EncodeToString(hash[:])
}

// isDuplicate duplicate içerik kontrolü
func (s *Scraper) isDuplicate(hash string) bool {
	s.mu.RLock()
	exists := s.seenHashes[hash]
	s.mu.RUnlock()
	return exists
}

// markAsSeen hash'i görülmüş olarak işaretle
func (s *Scraper) markAsSeen(hash string) {
	s.mu.Lock()
	s.seenHashes[hash] = true
	s.mu.Unlock()
}

// ScrapeURL tek bir URL'yi scrape eder - retry mekanizması ile
func (s *Scraper) ScrapeURL(urlStr, title string) Content {
	content := Content{
		URL:       urlStr,
		Title:     title,
		ScrapedAt: time.Now(),
	}

	// 1. .onion kontrolü
	if !strings.Contains(urlStr, ".onion") {
		content.Error = "Bu bir .onion adresi değil. Derinleştirme sadece .onion adresleri için çalışır."
		logger.Warn("🔗 .onion olmayan URL: %s", urlStr)
		return content
	}

	// 2. URL kalite kontrolü
	if isLow, reason := s.isLowValueURL(urlStr); isLow {
		content.Error = fmt.Sprintf("Atlanan: %s", reason)
		logger.Debug("⏭️ Atlanan URL: %s (%s)", urlStr, reason)
		return content
	}

	// 3. HTTP isteği - shared paketi ile retry
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		content.Error = fmt.Sprintf("İstek hatası: %v", err)
		logger.Error("❌ İstek oluşturma hatası: %s - %v", urlStr, err)
		return content
	}

	req.Header.Set("User-Agent", shared.RandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// DoWithRetry kullan - exponential backoff ile
	resp, err := shared.DoWithRetry(s.client, req)
	if err != nil {
		// Hata sınıflandırması shared paketinden
		content.Error = shared.ClassifyError(err).Error()
		logger.Warn("⚠️ Bağlantı hatası: %s - %s", urlStr, content.Error)
		return content
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		content.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		logger.Warn("⚠️ HTTP hata: %s - %d", urlStr, resp.StatusCode)
		return content
	}

	// 4. Content-Type kontrolü
	contentType := resp.Header.Get("Content-Type")
	if contentType != "" && !strings.Contains(contentType, "text/html") && !strings.Contains(contentType, "text/plain") {
		content.Error = fmt.Sprintf("Skipped: non-HTML (%s)", contentType)
		return content
	}

	// 5. Body oku
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		content.Error = fmt.Sprintf("Read error: %v", err)
		return content
	}

	// 6. Binary içerik kontrolü (magic bytes)
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

	// 7. HTML'i text'e çevir
	text := s.htmlToText(string(body))

	// 8. Minimum uzunluk kontrolü
	if len(text) < 100 {
		content.Error = "Skipped: content too short (<100 chars)"
		return content
	}

	// 9. Duplicate kontrolü
	contentHash := generateContentHash(text)
	if s.isDuplicate(contentHash) {
		content.Error = "Skipped: duplicate content"
		return content
	}

	// 10. Kalite skoru hesapla
	qualityScore := s.calculateQualityScore(text, title)

	// Minimum kalite skoru: 15
	if qualityScore < 15 {
		content.Error = fmt.Sprintf("Skipped: low quality (score: %d)", qualityScore)
		return content
	}

	// 11. İçeriği hazırla
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

// htmlToText HTML'i temiz text'e çevirir
func (s *Scraper) htmlToText(html string) string {
	scriptRegex := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRegex.ReplaceAllString(html, " ")

	styleRegex := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRegex.ReplaceAllString(html, " ")

	headRegex := regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	html = headRegex.ReplaceAllString(html, " ")

	tagRegex := regexp.MustCompile(`<[^>]+>`)
	text := tagRegex.ReplaceAllString(html, " ")

	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&#x27;", "'")
	text = strings.ReplaceAll(text, "&#x2F;", "/")
	text = strings.ReplaceAll(text, "&#36;", "$")
	text = strings.ReplaceAll(text, "&ndash;", "-")
	text = strings.ReplaceAll(text, "&mdash;", "-")
	text = strings.ReplaceAll(text, "&copy;", "©")
	text = strings.ReplaceAll(text, "&trade;", "™")
	text = strings.ReplaceAll(text, "&reg;", "®")

	spaceRegex := regexp.MustCompile(`\s+`)
	text = spaceRegex.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

// ScrapeMultiple birden fazla URL'yi concurrent scrape eder
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

// ResetSeenHashes görülmüş hash'leri sıfırlar
func (s *Scraper) ResetSeenHashes() {
	s.mu.Lock()
	s.seenHashes = make(map[string]bool)
	s.mu.Unlock()
}

// ============================================================================
// LİNK EXTRACTION - DERİNLEŞTİR ÖZELLİĞİ İÇİN
// ============================================================================

// ExtractedLink çıkarılan link bilgisi
type ExtractedLink struct {
	URL      string
	Title    string
	Domain   string
	LinkType string // "internal" veya "external"
}

// ExtractLinks bir sayfadan tüm linkleri çıkarır ve iç/dış olarak ayırır
func (s *Scraper) ExtractLinks(pageURL string, htmlContent string) ([]ExtractedLink, error) {
	// Sayfa domain'ini al
	pageDomain := extractDomainFromURL(pageURL)

	var links []ExtractedLink
	seen := make(map[string]bool)

	// <a> taglarını bul
	linkRegex := regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	matches := linkRegex.FindAllStringSubmatch(htmlContent, -1)

	// HTML tag temizleme
	tagRegex := regexp.MustCompile(`<[^>]*>`)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		href := strings.TrimSpace(match[1])
		titleHTML := match[2]

		// URL'yi normalize et
		normalizedURL := normalizeURL(href, pageURL)
		if normalizedURL == "" {
			continue
		}

		// Daha önce gördük mü?
		if seen[normalizedURL] {
			continue
		}
		seen[normalizedURL] = true

		// Binary dosyaları atla
		if isBinaryExtension(normalizedURL) {
			continue
		}

		// Title çıkar
		title := strings.TrimSpace(tagRegex.ReplaceAllString(titleHTML, ""))
		if title == "" {
			title = extractTitleFromURLPath(normalizedURL)
		}
		if len(title) > 100 {
			title = title[:97] + "..."
		}

		// Domain çıkar
		linkDomain := extractDomainFromURL(normalizedURL)

		// İç/dış link belirle
		linkType := "external"
		if linkDomain == pageDomain {
			linkType = "internal"
		}

		links = append(links, ExtractedLink{
			URL:      normalizedURL,
			Title:    title,
			Domain:   linkDomain,
			LinkType: linkType,
		})
	}

	return links, nil
}

// ExtractLinksFromURL bir URL'yi scrape edip linklerini çıkarır
func (s *Scraper) ExtractLinksFromURL(targetURL string) ([]ExtractedLink, error) {
	// Sayfayı çek - derinleştirme için özel (kalite kontrolü atlanır)
	content := s.scrapeURLForExpand(targetURL)
	if !content.Success {
		return nil, fmt.Errorf("%s", content.Error)
	}

	// Linkleri çıkar
	return s.ExtractLinks(targetURL, content.RawContent)
}

// scrapeURLForExpand derinleştirme için özel scrape fonksiyonu (kalite kontrolü yok, daha yüksek timeout)
func (s *Scraper) scrapeURLForExpand(urlStr string) Content {
	content := Content{
		URL:       urlStr,
		ScrapedAt: time.Now(),
	}

	// 1. .onion kontrolü
	if !strings.Contains(urlStr, ".onion") {
		content.Error = "Bu bir .onion adresi değil. Derinleştirme sadece Dark Web (.onion) adresleri için çalışır."
		logger.Warn("🔗 .onion olmayan URL: %s", urlStr)
		return content
	}

	// 2. HTTP isteği - shared paketi ile retry
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		content.Error = fmt.Sprintf("İstek oluşturulamadı: %v", err)
		logger.Error("❌ İstek oluşturma hatası: %s - %v", urlStr, err)
		return content
	}

	req.Header.Set("User-Agent", shared.RandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	// DoWithRetry kullan - exponential backoff ile
	resp, err := shared.DoWithRetry(s.client, req)
	if err != nil {
		// Hata sınıflandırması shared paketinden
		content.Error = shared.ClassifyError(err).Error()
		logger.Warn("⚠️ Derinleştirme bağlantı hatası: %s - %s", urlStr, content.Error)
		return content
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		if resp.StatusCode == 503 {
			content.Error = "Site şu anda kullanılamıyor (503). Daha sonra tekrar deneyin."
		} else if resp.StatusCode == 404 {
			content.Error = "Sayfa bulunamadı (404). Link geçersiz olabilir."
		} else {
			content.Error = fmt.Sprintf("HTTP hatası: %d", resp.StatusCode)
		}
		logger.Warn("⚠️ HTTP hata: %s - %d", urlStr, resp.StatusCode)
		return content
	}

	// 3. Body oku
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		content.Error = fmt.Sprintf("Sayfa içeriği okunamadı: %v", err)
		logger.Error("❌ Body okuma hatası: %s - %v", urlStr, err)
		return content
	}

	// 4. Binary içerik kontrolü (magic bytes)
	if len(body) > 4 {
		if (body[0] == 0x89 && body[1] == 0x50) ||
			(body[0] == 0xFF && body[1] == 0xD8) ||
			(body[0] == 0x47 && body[1] == 0x49) ||
			(body[0] == 0x50 && body[1] == 0x4B) ||
			(body[0] == 0x25 && body[1] == 0x50) {
			content.Error = "Bu bir resim/binary dosya, HTML sayfası değil."
			return content
		}
	}

	// 5. Minimum uzunluk kontrolü
	if len(body) < 50 {
		content.Error = "Sayfa içeriği çok kısa veya boş."
		return content
	}

	// Başarılı
	content.RawContent = string(body)
	content.ContentSize = len(body)
	content.Success = true
	return content
}

// extractDomainFromURL URL'den domain çıkarır - shared paketi kullanır
func extractDomainFromURL(urlStr string) string {
	return shared.ExtractDomain(urlStr)
}

// normalizeURL URL'yi normalize eder
func normalizeURL(href, baseURL string) string {
	href = strings.TrimSpace(href)

	// JavaScript, mailto, tel gibi protokolleri atla
	if strings.HasPrefix(href, "javascript:") ||
		strings.HasPrefix(href, "mailto:") ||
		strings.HasPrefix(href, "tel:") ||
		strings.HasPrefix(href, "#") ||
		href == "" {
		return ""
	}

	// Zaten tam URL mi?
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}

	// Relative URL'yi absolute yap
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}

	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}

	resolved := base.ResolveReference(ref)
	return resolved.String()
}

// isBinaryExtension binary dosya uzantısı kontrolü
func isBinaryExtension(urlStr string) bool {
	lower := strings.ToLower(urlStr)
	for _, ext := range shared.BinaryExtensions {
		if strings.HasSuffix(lower, ext) || strings.Contains(lower, ext+"?") {
			return true
		}
	}
	return false
}

// extractTitleFromURLPath URL path'inden title çıkarır
func extractTitleFromURLPath(urlStr string) string {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return "[Link]"
	}

	path := parsed.Path
	path = strings.Trim(path, "/")

	if path == "" {
		return "[Ana Sayfa]"
	}

	// Son parçayı al
	parts := strings.Split(path, "/")
	lastPart := parts[len(parts)-1]

	// Uzantıyı kaldır
	if idx := strings.LastIndex(lastPart, "."); idx != -1 {
		lastPart = lastPart[:idx]
	}

	// Güzelleştir
	lastPart = strings.ReplaceAll(lastPart, "-", " ")
	lastPart = strings.ReplaceAll(lastPart, "_", " ")

	if lastPart == "" {
		return "[Link]"
	}

	// İlk harfi büyük yap
	if len(lastPart) > 0 {
		lastPart = strings.ToUpper(string(lastPart[0])) + lastPart[1:]
	}

	return lastPart
}
