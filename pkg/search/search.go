package search

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"keywordhunter-mvp/pkg/cti"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
)

// Result arama sonucu
type Result struct {
	Title       string
	URL         string
	Source      string // Hangi arama motorundan geldi
	Criticality int
	Category    string
	KeywordHits int // Arama kelimesinin bu sonuçta kaç kez geçtiği
}

// Searcher dark web arama yapan yapı
type Searcher struct {
	torProxy string
	client   *http.Client
}

// cleanURL URL'deki boşlukları ve geçersiz karakterleri temizler
func cleanURL(urlStr string) string {
	// Boşlukları kaldır
	urlStr = strings.ReplaceAll(urlStr, " ", "")
	urlStr = strings.ReplaceAll(urlStr, "\t", "")
	urlStr = strings.ReplaceAll(urlStr, "\n", "")
	urlStr = strings.ReplaceAll(urlStr, "\r", "")
	return strings.TrimSpace(urlStr)
}

// isValidResultURL URL'in geçerli bir sonuç olup olmadığını kontrol eder
func isValidResultURL(urlStr string) bool {
	// Önce URL'yi temizle
	urlStr = cleanURL(urlStr)
	lowerURL := strings.ToLower(urlStr)

	// 0. URL'de boşluk varsa geçersiz
	if strings.Contains(urlStr, " ") {
		return false
	}

	// 1. Arama motoru domain'lerini filtrele
	if isSearchEngineDomain(urlStr) {
		return false
	}

	// 2. Shared paketten düşük değerli URL kontrolü
	isLow, _ := shared.IsLowValueURL(urlStr)
	if isLow {
		return false
	}

	// 3. Çok kısa URL'leri atla (sadece domain)
	// http://xxx.onion veya http://xxx.onion/ gibi
	onionIdx := strings.Index(lowerURL, ".onion")
	if onionIdx > 0 {
		afterOnion := lowerURL[onionIdx+6:]
		// Sadece / veya boş ise, ana sayfa - bunlar genelde index sayfaları
		if afterOnion == "" || afterOnion == "/" {
			// Ana sayfalar bazı durumlarda değerli olabilir, geçir
			return true
		}
	}

	return true
}

// New yeni Searcher oluşturur - shared paketi kullanır
func New(torProxy string) (*Searcher, error) {
	client, err := shared.NewHTTPClient(torProxy)
	if err != nil {
		return nil, fmt.Errorf("HTTP client oluşturma hatası: %w", err)
	}

	// Arama için özel timeout ayarla
	client.Timeout = shared.SearchTimeout

	logger.Info("Searcher initialized (timeout: %v)", shared.SearchTimeout)

	return &Searcher{
		torProxy: torProxy,
		client:   client,
	}, nil
}

// SearchAll tüm arama motorlarında arama yapar
func (s *Searcher) SearchAll(ctx context.Context, query string) []Result {
	var results []Result
	var mu sync.Mutex
	var wg sync.WaitGroup
	startTime := time.Now()

	logger.SearchStarted(query, len(SearchEngines))

	for _, engine := range SearchEngines {
		wg.Add(1)
		go func(eng Engine) {
			defer wg.Done()

			// Bütçe/iptal kontrolü
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Broadcast start
			shared.Streamer.BroadcastLog("engine_start", "Starting search...", eng.Name)

			engineResults, err := s.searchEngine(ctx, eng, query)
			if err != nil {
				logger.SearchEngineResult(eng.Name, 0, err)
				shared.Streamer.BroadcastLog("engine_end", fmt.Sprintf("SEARCH ENGINE ERROR: %v", err), eng.Name)
				return
			}

			if len(engineResults) > 0 {
				// Toplam hit sayısını hesapla
				totalHits := 0
				for _, r := range engineResults {
					totalHits += r.KeywordHits
				}
				logger.SearchEngineResult(eng.Name, len(engineResults), nil)
				shared.Streamer.BroadcastLog("engine_end", fmt.Sprintf("✅ %d sonuç, %d hit bulundu", len(engineResults), totalHits), eng.Name)
				mu.Lock()
				results = append(results, engineResults...)
				mu.Unlock()
			} else {
				logger.SearchEngineResult(eng.Name, 0, nil)
				shared.Streamer.BroadcastLog("engine_end", "SEARCH ENGINE SUCCESS: 0 results found", eng.Name)
			}
		}(engine)
	}

	wg.Wait()

	// Duplicate URL'leri kaldır
	deduped := s.deduplicate(results)

	logger.SearchCompleted(query, len(deduped), time.Since(startTime))
	shared.Streamer.BroadcastLog("success", fmt.Sprintf("SEARCH COMPLETED: %d unique results found in %v", len(deduped), time.Since(startTime).Round(time.Millisecond)), "")

	return deduped
}

// searchEngine tek bir arama motorunda arama yapar - retry ile
func (s *Searcher) searchEngine(ctx context.Context, engine Engine, query string) ([]Result, error) {
	// URL'yi oluştur
	searchURL := strings.Replace(engine.URL, "{query}", url.QueryEscape(query), 1)

	// HTTP request oluştur
	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", shared.RandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	logger.Debug("ENGINE REQUEST: %s URL: %s", engine.Name, searchURL)

	// DoWithRetry kullan - exponential backoff ile
	resp, err := shared.DoWithRetry(s.client, req)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", engine.Name, shared.ClassifyError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logger.Warn("ENGINE HTTP STATUS: %s returned %d", engine.Name, resp.StatusCode)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Body'yi oku (boyut sınırlı)
	body, err := io.ReadAll(io.LimitReader(resp.Body, shared.MaxResponseBytes))
	if err != nil {
		return nil, err
	}

	// HTML'den .onion linklerini çıkar ve kelime sıklığını say
	return s.parseResults(string(body), engine.Name, query), nil
}

// parseResults HTML'den .onion linklerini çıkarır ve kelime sıklığını sayar
func (s *Searcher) parseResults(htmlContent, sourceName, query string) []Result {
	var results []Result
	queryLower := strings.ToLower(strings.TrimSpace(query))
	htmlLower := strings.ToLower(htmlContent)

	// countHits boş query'de strings.Count şişmesini önler
	countHits := func(haystack string) int {
		if queryLower == "" {
			return 0
		}
		return strings.Count(haystack, queryLower)
	}

	// <a> taglarını bul - iç HTML dahil (nested taglar için)
	// Önce tüm <a> bloklarını bul
	linkRegex := regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	matches := linkRegex.FindAllStringSubmatch(htmlContent, -1)

	// .onion URL pattern
	onionRegex := regexp.MustCompile(`https?://[^/]*\.onion[^\s"'<>]*`)

	// HTML tag temizleme regex
	htmlTagRegex := regexp.MustCompile(`<[^>]*>`)
	for _, match := range matches {
		if len(match) >= 3 {
			href := match[1]
			innerHTML := match[2]
			onionURLs := onionRegex.FindAllString(href, -1)
			if len(onionURLs) > 0 {
				foundURL := cleanURL(onionURLs[0])
				if !isValidResultURL(foundURL) {
					continue
				}
				title := cleanTitle(decodeHTMLEntities(htmlTagRegex.ReplaceAllString(innerHTML, "")), foundURL)
				// Kelime sıklığını say (title + innerHTML içinde)
				hits := countHits(strings.ToLower(innerHTML))
				hits += countHits(strings.ToLower(title))
				res := Result{
					Title:       title,
					URL:         foundURL,
					Source:      sourceName,
					KeywordHits: hits,
				}
				res.PredictCTI()
				results = append(results, res)
			}
		}
	}

	// Ayrıca düz .onion URL'leri de tara (href dışında olanlar)
	allOnions := onionRegex.FindAllString(htmlContent, -1)
	seen := make(map[string]bool)
	for _, r := range results {
		seen[r.URL] = true
	}

	for _, onionURL := range allOnions {
		// ✅ URL filtreleme - düşük değerli URL'leri kaydetme
		if !seen[onionURL] && !strings.Contains(onionURL, "javascript:") && isValidResultURL(onionURL) {
			// URL çevresinde kelime geçiyor mu kontrol et (context-aware)
			hits := 0
			urlIdx := strings.Index(htmlLower, strings.ToLower(onionURL))
			if urlIdx > 0 {
				// URL öncesi ve sonrasını al (max 200 karakter)
				start := max(0, urlIdx-200)
				end := min(len(htmlLower), urlIdx+len(onionURL)+200)
				hits = countHits(htmlLower[start:end])
			}
			res := Result{
				Title:       extractTitleFromURL(onionURL),
				URL:         onionURL,
				Source:      sourceName,
				KeywordHits: hits,
			}
			res.PredictCTI()
			results = append(results, res)
			seen[onionURL] = true
		}
	}

	return results
}

// PredictCTI başlık ve URL'ye göre kategori/kritiklik tahmini yapar
func (r *Result) PredictCTI() {
	analysis := cti.Analyze(r.Title, r.URL, "", nil, r.KeywordHits)
	r.Criticality = analysis.Criticality
	r.Category = analysis.Category
}

// decodeHTMLEntities HTML entity'leri decode eder (surrogate-güvenli)
func decodeHTMLEntities(s string) string {
	return html.UnescapeString(s)
}

// cleanTitle title'ı temizler ve anlamlı hale getirir
func cleanTitle(title, url string) string {
	// Boş veya çok kısa title
	if len(title) < 3 {
		return extractTitleFromURL(url)
	}

	// Title URL ile aynı mı? (veya URL'nin bir parçası mı?)
	// Çok kısa title'lar URL içinde tesadüfen geçebilir; en az 8 karakterde kontrol et.
	if (len(title) >= 8 && strings.Contains(url, title)) || strings.Contains(title, ".onion") {
		return extractTitleFromURL(url)
	}

	// Title sadece "..." veya benzeri mi?
	cleaned := strings.Trim(title, ".")
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) < 3 {
		return extractTitleFromURL(url)
	}

	// Title çok uzunsa kısalt (rune-güvenli)
	if len([]rune(title)) > 150 {
		title = shared.TruncateRunes(title, 147) + "..."
	}

	return title
}

// extractTitleFromURL URL'den anlamlı bir title çıkarır
func extractTitleFromURL(url string) string {
	// http://xxx.onion/path/to/page -> path/to/page veya domain

	// Protocol'ü kaldır
	cleanURL := strings.TrimPrefix(url, "http://")
	cleanURL = strings.TrimPrefix(cleanURL, "https://")

	// Query string'i kaldır
	if idx := strings.Index(cleanURL, "?"); idx != -1 {
		cleanURL = cleanURL[:idx]
	}

	// Parçala
	parts := strings.Split(cleanURL, "/")

	if len(parts) > 1 && len(parts[1]) > 0 {
		// Path var, path'i kullan
		path := strings.Join(parts[1:], "/")
		path = strings.Trim(path, "/")

		// Path'i güzelleştir
		path = strings.ReplaceAll(path, "-", " ")
		path = strings.ReplaceAll(path, "_", " ")
		path = strings.ReplaceAll(path, ".php", "")
		path = strings.ReplaceAll(path, ".html", "")
		path = strings.ReplaceAll(path, ".htm", "")

		if len(path) > 3 {
			// İlk harfi büyük yap (rune-güvenli)
			pathRunes := []rune(path)
			if len(pathRunes) > 0 {
				path = strings.ToUpper(string(pathRunes[0])) + string(pathRunes[1:])
			}
			return "[" + path + "]"
		}
	}

	// Sadece domain var, kısa hash göster
	domain := parts[0]
	if strings.Contains(domain, ".onion") {
		// xxx.onion -> [Onion: xxx...]
		onionPart := strings.TrimSuffix(domain, ".onion")
		onionPart = shared.TruncateRunes(onionPart, 8)
		return "[Onion: " + onionPart + "...]"
	}

	return "[Unknown Site]"
}

// deduplicate tekrar eden URL'leri kaldırır ve son bir filtreleme yapar
func (s *Searcher) deduplicate(results []Result) []Result {
	seen := make(map[string]bool)
	var unique []Result
	var filtered int

	for _, r := range results {
		if !seen[r.URL] {
			// Son kontrol - isValidResultURL
			if !isValidResultURL(r.URL) {
				filtered++
				continue
			}
			seen[r.URL] = true
			unique = append(unique, r)
		}
	}

	if filtered > 0 {
		logger.Debug("DEDUPLICATION: %d low value URLs filtered", filtered)
	}

	logger.Debug("DEDUPLICATION: %d unique results", len(unique))

	return unique
}
