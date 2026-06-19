package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
)

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
		if len([]rune(title)) > 100 {
			title = shared.TruncateRunes(title, 97) + "..."
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
func (s *Scraper) ExtractLinksFromURL(ctx context.Context, targetURL string) ([]ExtractedLink, error) {
	// Sayfayı çek - derinleştirme için özel (kalite kontrolü atlanır)
	content := s.scrapeURLForExpand(ctx, targetURL)
	if !content.Success {
		return nil, fmt.Errorf("%s", content.Error)
	}

	// Linkleri çıkar
	return s.ExtractLinks(targetURL, content.RawContent)
}

// scrapeURLForExpand derinleştirme için özel scrape fonksiyonu
func (s *Scraper) scrapeURLForExpand(ctx context.Context, urlStr string) Content {
	content := Content{
		URL:       urlStr,
		ScrapedAt: time.Now(),
	}

	if !isOnionURL(urlStr) {
		content.Error = "Bu bir .onion adresi değil. Derinleştirme sadece Dark Web (.onion) adresleri için çalışır."
		return content
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		content.Error = fmt.Sprintf("İstek oluşturulamadı: %v", err)
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
		content.Error = fmt.Sprintf("HTTP hatası: %d", resp.StatusCode)
		return content
	}

	// Body oku (boyut sınırlı)
	body, err := io.ReadAll(io.LimitReader(resp.Body, shared.MaxResponseBytes))
	if err != nil {
		content.Error = fmt.Sprintf("Sayfa içeriği okunamadı: %v", err)
		logger.Error("Body read error during expand: %s - %v", urlStr, err)
		return content
	}

	content.RawContent = string(body)
	content.ContentSize = len(body)
	content.Success = true

	return content
}

// extractDomainFromURL URL'den domain çıkarır
func extractDomainFromURL(urlStr string) string {
	return shared.ExtractDomain(urlStr)
}

// normalizeURL URL'yi normalize eder
// Bu fonksiyon shared.NormalizeURL gibi ama burada yerel tanımlanmış, scraper.go'dan taşındı
// Eğer shared.NormalizeURL varsa onu kullanabiliriz ama scraper.go'da özel implementasyon vardı.
// Scraper.go'daki orijinal implementasyon:
// ... (Copied logic)
func normalizeURL(href, baseURL string) string {
	href = strings.TrimSpace(href)
	if strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") || strings.HasPrefix(href, "#") || href == "" {
		return ""
	}
	if strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://") {
		return href
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

// CountKeywords bir sayfadaki anahtar kelime sayısını döndürür
func (s *Scraper) CountKeywords(ctx context.Context, urlStr, keyword string) (int, error) {
	// Sayfayı çek
	content := s.scrapeURLForExpand(ctx, urlStr) // reuse for reuse simple raw content fetch
	if !content.Success {
		return 0, fmt.Errorf("%s", content.Error)
	}

	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return 0, nil
	}

	// HTML temizle
	text := strings.ToLower(s.htmlToText(content.RawContent))

	// Say
	count := strings.Count(text, keyword)
	return count, nil
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
	parsed, err := url.Parse(strings.TrimSpace(urlStr))
	if err != nil {
		return "Link"
	}

	path := strings.Trim(parsed.Path, "/")
	if path == "" {
		host := strings.TrimSpace(parsed.Host)
		if host == "" {
			return "Link"
		}
		return host
	}

	segments := strings.Split(path, "/")
	candidate := strings.TrimSpace(segments[len(segments)-1])
	if candidate == "" && len(segments) > 1 {
		candidate = strings.TrimSpace(segments[len(segments)-2])
	}

	candidate = strings.ReplaceAll(candidate, "-", " ")
	candidate = strings.ReplaceAll(candidate, "_", " ")
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "Link"
	}

	if len([]rune(candidate)) > 80 {
		candidate = shared.TruncateRunes(candidate, 77) + "..."
	}

	return candidate
}
