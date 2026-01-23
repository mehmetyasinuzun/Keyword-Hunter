package search

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

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
}

// Searcher dark web arama yapan yapı
type Searcher struct {
	torProxy string
	client   *http.Client
}

// Arama motorlarının kendi domain'leri - bunları sonuçlardan çıkacak
var searchEngineDomains = []string{
	"juhanurmihxlp77nkq76byazcldy2hlmovfu2epvl5ankdibsot4csyd.onion", // Ahmia
	"3bbad7fauom4d6sgppalyqddsqbf5u5p56b5k5uk2zxsy3d6ey2jobad.onion", // OnionLand
	"darkhuntyla64h75a3re5e2l3367lqn7ltmdzpgmr6b4nbz3q2iaxrid.onion", // DarkHunt
	"iy3544gmoeclh5de6gez2256v6pjh4omhpqdh2wpeeppjtvqmjhkfwad.onion", // Torgle
	"amnesia7u5odx5xbwtpnqk3edybgud5bmiagu75bnqx2crntw5kry7ad.onion", // Amnesia
	"kaizerwfvp5gxu6cppibp7jhcqptavq3iqef66wbxenh6a2fklibdvid.onion", // Kaizer
	"anima4ffe27xmakwnseih3ic2y7y3l6e7fucwk4oerdn4odf7k74tbid.onion", // Anima
	"tornadoxn3viscgz647shlysdy7ea5zqzwda7hierekeuokh5eh5b3qd.onion", // Tornado
	"tornetupfu7gcgidt33ftnungxzyfq2pygui5qdoyss34xbgx2qruzid.onion", // TorNet
	"torlbmqwtudkorme6prgfpmsnile7ug2zm4u3ejpcncxuhpu4k2j4kyd.onion", // Torland
	"findtorroveq5wdnipkaojfpqulxnkhblymc7aramjzajcvpptd4rjqd.onion", // FindTor
	"2fd6cemt4gmccflhm6imvdfvli3nf7zn6rfrwpsy7uhxrgbypvwf5fad.onion", // Excavator
	"oniwayzz74cv2puhsgx4dpjwieww4wdphsydqvf5q7eyz4myjvyw26ad.onion", // Onionway
	"tor66sewebgixwhcqfnp5inzp5x5uohhdy3kvtnyfxc2e5mxiuh34iid.onion", // Tor66
	"3fzh7yuupdfyjhwt3ugzqqof6ulbcl27ecev33knxe3u7goi3vfn2qqd.onion", // OSS
	"torgolnpeouim56dykfob6jh5r2ps2j73enc42s2um4ufob3ny4fcdyd.onion", // Torgol
	"searchgf7gdtauh7bhnbyed4ivxqmuoat3nm6zfrg3ymkq6mtnpye3ad.onion", // DeepSearches
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

	// 1. Arama motoru domain'lerini kontrol et
	for _, domain := range searchEngineDomains {
		if strings.Contains(lowerURL, domain) {
			return false
		}
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
func (s *Searcher) SearchAll(query string) []Result {
	var results []Result
	var mu sync.Mutex
	var wg sync.WaitGroup
	startTime := time.Now()

	logger.SearchStarted(query, len(SearchEngines))

	for _, engine := range SearchEngines {
		wg.Add(1)
		go func(eng Engine) {
			defer wg.Done()

			engineResults, err := s.searchEngine(eng, query)
			if err != nil {
				logger.SearchEngineResult(eng.Name, 0, err)
				return
			}

			if len(engineResults) > 0 {
				logger.SearchEngineResult(eng.Name, len(engineResults), nil)
				mu.Lock()
				results = append(results, engineResults...)
				mu.Unlock()
			} else {
				logger.SearchEngineResult(eng.Name, 0, nil)
			}
		}(engine)
	}

	wg.Wait()

	// Duplicate URL'leri kaldır
	deduped := s.deduplicate(results)

	logger.SearchCompleted(query, len(deduped), time.Since(startTime))

	return deduped
}

// searchEngine tek bir arama motorunda arama yapar - retry ile
func (s *Searcher) searchEngine(engine Engine, query string) ([]Result, error) {
	// URL'yi oluştur
	searchURL := strings.Replace(engine.URL, "{query}", url.QueryEscape(query), 1)

	// HTTP request oluştur
	req, err := http.NewRequest("GET", searchURL, nil)
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

	// Body'yi oku
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// HTML'den .onion linklerini çıkar
	return s.parseResults(string(body), engine.Name), nil
}

// parseResults HTML'den .onion linklerini çıkarır (Robin tarzı)
func (s *Searcher) parseResults(html, sourceName string) []Result {
	var results []Result

	// <a> taglarını bul - iç HTML dahil (nested taglar için)
	// Önce tüm <a> bloklarını bul
	linkRegex := regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	matches := linkRegex.FindAllStringSubmatch(html, -1)

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
				res := Result{
					Title:  title,
					URL:    foundURL,
					Source: sourceName,
				}
				res.PredictCTI()
				results = append(results, res)
			}
		}
	}

	// Ayrıca düz .onion URL'leri de tara (href dışında olanlar)
	allOnions := onionRegex.FindAllString(html, -1)
	seen := make(map[string]bool)
	for _, r := range results {
		seen[r.URL] = true
	}

	for _, onionURL := range allOnions {
		// ✅ URL filtreleme - düşük değerli URL'leri kaydetme
		if !seen[onionURL] && !strings.Contains(onionURL, "javascript:") && isValidResultURL(onionURL) {
			res := Result{
				Title:  extractTitleFromURL(onionURL),
				URL:    onionURL,
				Source: sourceName,
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
	text := strings.ToLower(r.Title + " " + r.URL)

	// Default values
	r.Criticality = 1
	r.Category = "Genel"

	// Ransomware (Crit: 5)
	if matchAny(text, "ransomware", "lockbit", "blackcat", "hive", "conti", "leak", "decrypt") {
		r.Criticality = 5
		r.Category = "Ransomware"
		return
	}

	// Data Leak (Crit: 5)
	if matchAny(text, "database", "sql", "dump", "full access", "selling data", "user list", "combolist") {
		r.Criticality = 5
		r.Category = "Veri Sızıntısı"
		return
	}

	// Fraud / CC (Crit: 5)
	if matchAny(text, "carding", "cc dump", "cvv", "cashout", "bank account", "cloned") {
		r.Criticality = 5
		r.Category = "Finansal Dolandırıcılık"
		return
	}

	// Dark Market (Crit: 4)
	if matchAny(text, "market", "shop", "store", "buy", "sell", "vendor", "escrow") {
		r.Criticality = 4
		r.Category = "Illegal Market"
		return
	}

	// Hacking Forum (Crit: 3)
	if matchAny(text, "forum", "board", "community", "hacking", "exploit", "0day") {
		r.Criticality = 3
		r.Category = "Siber Forum"
		return
	}

	// Social / Network (Crit: 2)
	if matchAny(text, "chat", "message", "telegram", "channel", "vpn", "proxy", "host") {
		r.Criticality = 2
		r.Category = "İletişim / Ağ"
		return
	}
}

func matchAny(text string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

// decodeHTMLEntities HTML entity'leri decode eder
func decodeHTMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	s = strings.ReplaceAll(s, "&#x2F;", "/")
	// Numeric entities
	numericRegex := regexp.MustCompile(`&#(\d+);`)
	s = numericRegex.ReplaceAllStringFunc(s, func(match string) string {
		var num int
		fmt.Sscanf(match, "&#%d;", &num)
		if num > 0 && num < 65536 {
			return string(rune(num))
		}
		return match
	})
	return s
}

// cleanTitle title'ı temizler ve anlamlı hale getirir
func cleanTitle(title, url string) string {
	// Boş veya çok kısa title
	if len(title) < 3 {
		return extractTitleFromURL(url)
	}

	// Title URL ile aynı mı? (veya URL'nin bir parçası mı?)
	if strings.Contains(url, title) || strings.Contains(title, ".onion") {
		return extractTitleFromURL(url)
	}

	// Title sadece "..." veya benzeri mi?
	cleaned := strings.Trim(title, ".")
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) < 3 {
		return extractTitleFromURL(url)
	}

	// Title çok uzunsa kısalt
	if len(title) > 150 {
		title = title[:147] + "..."
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
			// İlk harfi büyük yap
			if len(path) > 0 {
				path = strings.ToUpper(string(path[0])) + path[1:]
			}
			return "[" + path + "]"
		}
	}

	// Sadece domain var, kısa hash göster
	domain := parts[0]
	if strings.Contains(domain, ".onion") {
		// xxx.onion -> [Onion: xxx...]
		onionPart := strings.TrimSuffix(domain, ".onion")
		if len(onionPart) > 8 {
			onionPart = onionPart[:8]
		}
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
