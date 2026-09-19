package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/i18n"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/notify"
	"keywordhunter-mvp/pkg/scheduler"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

// handleIndex ana sayfa: geçerli oturum varsa panele, yoksa girişe yönlendirir.
func (s *Server) handleIndex(c *gin.Context) {
	if sid, err := c.Cookie("session"); err == nil && sid != "" {
		if sess, err := s.db.GetSession(sid); err == nil && time.Now().Before(sess.ExpiresAt) {
			c.Redirect(http.StatusFound, "/dashboard")
			return
		}
	}
	c.Redirect(http.StatusFound, "/login")
}

// brandFaviconSVG yeni marka logosuyla uyumlu favicon (hedef halkası + tarama + düğüm).
const brandFaviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#63b3ed"/><stop offset="1" stop-color="#805ad5"/></linearGradient></defs><rect width="32" height="32" rx="7" fill="#07080d"/><circle cx="14" cy="14" r="7.5" fill="none" stroke="url(#g)" stroke-width="2.2"/><circle cx="14" cy="14" r="2.3" fill="url(#g)"/><path d="M14 4.5V7M14 21v2.5M4.5 14H7M21 14h2.5" stroke="url(#g)" stroke-width="1.8" stroke-linecap="round"/><path d="M19.5 19.5l6 6" stroke="url(#g)" stroke-width="2.4" stroke-linecap="round"/></svg>`

// handleI18nCatalog seçili (veya sorgudaki) dilin çeviri kataloğunu döndürür.
func (s *Server) handleI18nCatalog(c *gin.Context) {
	lang := c.Query("lang")
	if lang == "" {
		lang, _ = c.Cookie("lang")
	}
	lang = i18n.Normalize(lang)
	c.Header("Cache-Control", "public, max-age=3600")
	c.JSON(http.StatusOK, gin.H{"lang": lang, "supported": i18n.Supported, "messages": i18n.Catalog(lang)})
}

// handleSetLang dil çerezini ayarlar (1 yıl).
func (s *Server) handleSetLang(c *gin.Context) {
	var req struct {
		Lang string `json:"lang"`
	}
	_ = c.ShouldBindJSON(&req)
	lang := i18n.Normalize(req.Lang)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("lang", lang, 365*24*3600, "/", "", s.cookieSecure, false)
	c.JSON(http.StatusOK, gin.H{"success": true, "lang": lang})
}

// handleFavicon marka SVG favicon'unu servis eder (tarayıcının /favicon.ico probu için).
func (s *Server) handleFavicon(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=604800")
	c.Data(http.StatusOK, "image/svg+xml", []byte(brandFaviconSVG))
}

// handleHealthz kimlik doğrulaması gerektirmeyen sağlık ucu (Docker/izleme için).
func (s *Server) handleHealthz(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	dbOK := true
	if _, _, err := s.db.GetStats(); err != nil {
		dbOK = false
	}
	status := http.StatusOK
	state := "ok"
	if !dbOK {
		status = http.StatusServiceUnavailable
		state = "degraded"
	}
	c.JSON(status, gin.H{
		"status":        state,
		"version":       Version,
		"uptimeSeconds": int(time.Since(s.startedAt).Seconds()),
		"db":            dbOK,
		"screenshots":   s.capturer != nil && s.capturer.Available(),
	})
}

// handleLoginPage login sayfası
func (s *Server) handleLoginPage(c *gin.Context) {
	wait, _ := strconv.Atoi(c.Query("wait"))
	if wait < 0 || wait > 3600 {
		wait = 0
	}
	c.HTML(http.StatusOK, "login.html", gin.H{
		"error":   c.Query("error"),
		"message": c.Query("message"),
		"wait":    wait,
	})
}

// handleLogin giriş işlemi
func (s *Server) handleLogin(c *gin.Context) {
	username := strings.TrimSpace(c.PostForm("username"))
	password := c.PostForm("password")
	clientIP := c.ClientIP()

	if len(username) > 128 || len(password) > 512 {
		c.Redirect(http.StatusFound, "/login?error=1")
		return
	}

	if s.loginGuard != nil {
		if blocked, wait := s.loginGuard.Blocked(clientIP); blocked {
			logger.Warn("USER LOGIN BLOCKED: IP=%s kilitli (%s kaldı)", clientIP, wait.Round(time.Second))
			c.Header("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			c.Redirect(http.StatusFound, "/login?error=locked&wait="+strconv.Itoa(int(wait.Seconds())+1))
			return
		}
	}

	authed := false
	if u, err := s.db.GetUserByUsername(username); err == nil && u != nil && u.Enabled {
		authed = verifyBcrypt(u.PasswordHash, password) && subtleEqual(u.Username, username)
	} else if s.creds != nil {
		// users tablosunda yoksa bootstrap kimlik deposu (eski kurulum)
		authed = s.creds.Verify(username, password)
	}
	if authed {
		if s.loginGuard != nil {
			s.loginGuard.Success(clientIP)
		}
		_ = s.db.TouchUserLogin(username)
		// Session oluştur
		sessionID := generateSessionID()
		csrfToken := generateSessionID()
		expiresAt := time.Now().Add(s.sessionTTL)
		if err := s.db.CreateSession(sessionID, username, csrfToken, expiresAt); err != nil {
			logger.Error("Session kaydı oluşturulamadı: %v", err)
			c.Redirect(http.StatusFound, "/login?error=1&message=session")
			return
		}

		logger.UserLogin(username, true, clientIP)

		maxAge := int(s.sessionTTL.Seconds())
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("session", sessionID, maxAge, "/", "", s.cookieSecure, true)
		c.SetCookie("csrf_token", csrfToken, maxAge, "/", "", s.cookieSecure, false)
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}

	if s.loginGuard != nil {
		s.loginGuard.Fail(clientIP)
	}
	logger.UserLogin(shared.TruncateRunes(username, 32), false, clientIP)
	c.Redirect(http.StatusFound, "/login?error=1")
}

// handleLogout çıkış işlemi. GET isteklerinde siteler-arası tetiklemeye
// (CSRF ile oturum düşürme) karşı Fetch Metadata kontrolü yapılır; POST'ta
// CSRF token beklenir.
func (s *Server) handleLogout(c *gin.Context) {
	if c.Request.Method == http.MethodGet && isCrossSiteNavigation(c) {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	sessionID, err := c.Cookie("session")
	if err != nil {
		logger.Debug("Logout: session cookie bulunamadı: %v", err)
	} else {
		if err := s.db.DeleteSession(sessionID); err != nil {
			logger.Warn("Logout: session silinemedi: %v", err)
		}
	}

	clearAuthCookies(c, s.cookieSecure)
	c.Redirect(http.StatusFound, "/login")
}

// handleDashboard dashboard sayfası
func (s *Server) handleDashboard(c *gin.Context) {
	totalResults, totalSearches, err := s.db.GetStats()
	if err != nil {
		logger.Error("Dashboard istatistik hatası: %v", err)
	}
	// Son sonuçları getir
	recentResults, err := s.db.GetResults(10, "")
	if err != nil {
		logger.Error("Dashboard son sonuçlar hatası: %v", err)
	}

	// Arama geçmişini getir
	searchHistory, err := s.db.GetSearchHistory(10)
	if err != nil {
		logger.Error("Arama geçmişi getirilemedi: %v", err)
	}

	// Kategori ve kritiklik istatistiklerini getir
	categoryStats, _ := s.getCategoryStats()
	criticalityStats, _ := s.getCriticalityStats()

	// Kritiklik tanımları
	critDescs := map[int]string{
		1: "Seviye 1 (Düşük): Genel forum tartışmaları, haberler ve düşük riskli içerikler.",
		2: "Seviye 2 (Orta): Şüpheli aktiviteler, doğrulanmamış sızıntı iddiaları.",
		3: "Seviye 3 (Yüksek): Doğrulanmış veri sızıntıları, hassas kişisel bilgiler (PII).",
		4: "Seviye 4 (Kritik): Veritabanı sızıntıları, kredi kartı bilgileri, illegal ticaret.",
		5: "Seviye 5 (Acil): Ransomware, 0day exploitler, devlet sırları ve çok yüksek riskli içerikler.",
	}

	// ═══════════════════════════════════════════════════════════════════════
	// ALGORITHM HEALTH METRICS - Kategorizasyon etkinliğini ölç
	// ═══════════════════════════════════════════════════════════════════════

	// 1. Sınıflandırma Oranı: "Genel" dışı kategorilerin yüzdesi
	classifiedCount := 0
	genelCount := 0
	for cat, count := range categoryStats {
		if cat == "Genel" || cat == "" {
			genelCount += count
		} else {
			classifiedCount += count
		}
	}
	classificationRate := 0.0
	if totalResults > 0 {
		classificationRate = float64(classifiedCount) / float64(totalResults) * 100
	}

	// 2. Kritiklik Çeşitliliği: Tüm seviyelerin kullanım dengesi (0-100)
	// Sadece Seviye 1'e yığılma = düşük çeşitlilik
	critDiversity := 0.0
	usedLevels := 0
	for i := 1; i <= 5; i++ {
		if criticalityStats[i] > 0 {
			usedLevels++
		}
	}
	critDiversity = float64(usedLevels) / 5.0 * 100

	// 3. Yüksek Riskli Tespit Oranı: Seviye 3-5 oranı
	highRiskCount := criticalityStats[3] + criticalityStats[4] + criticalityStats[5]
	highRiskRate := 0.0
	if totalResults > 0 {
		highRiskRate = float64(highRiskCount) / float64(totalResults) * 100
	}

	// 4. Genel Sağlık Skoru (0-100)
	// Formül: (%40 sınıflandırma + %30 çeşitlilik + %30 yüksek risk tespiti)
	riskContribution := highRiskRate * 3
	if riskContribution > 30.0 {
		riskContribution = 30.0
	}
	healthScore := (classificationRate * 0.4) + (critDiversity * 0.3) + riskContribution
	if healthScore > 100 {
		healthScore = 100
	}

	// Sağlık durumu metni
	healthStatus := "iyi"
	healthColor := "green"
	if healthScore < 40 {
		healthStatus = "zayıf"
		healthColor = "red"
	} else if healthScore < 70 {
		healthStatus = "orta"
		healthColor = "yellow"
	}

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"ActivePage":       "dashboard",
		"totalResults":     totalResults,
		"totalSearches":    totalSearches,
		"totalURLs":        totalResults,
		"recentResults":    recentResults,
		"searchHistory":    searchHistory,
		"categoryStats":    categoryStats,
		"criticalityStats": criticalityStats,
		"critDescs":        critDescs,
		// Algorithm Health Metrics
		"classificationRate": int(classificationRate),
		"classifiedCount":    classifiedCount,
		"genelCount":         genelCount,
		"genelRate":          100 - int(classificationRate),
		"critDiversity":      int(critDiversity),
		"usedLevels":         usedLevels,
		"highRiskCount":      highRiskCount,
		"highRiskRate":       int(highRiskRate),
		"healthScore":        int(healthScore),
		"healthStatus":       healthStatus,
		"healthColor":        healthColor,
	})
}

// getCategoryStats kategori bazlı sayıları getirir
func (s *Server) getCategoryStats() (map[string]int, error) {
	rows, err := s.db.GetDBConn().Query("SELECT category, COUNT(*) FROM search_results GROUP BY category")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stats := make(map[string]int)
	for rows.Next() {
		var cat string
		var count int
		if err := rows.Scan(&cat, &count); err == nil {
			stats[cat] = count
		}
	}
	return stats, nil
}

// getCriticalityStats kritiklik bazlı sayıları getirir
func (s *Server) getCriticalityStats() (map[int]int, error) {
	rows, err := s.db.GetDBConn().Query("SELECT criticality, COUNT(*) FROM search_results GROUP BY criticality")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stats := make(map[int]int)
	for rows.Next() {
		var crit, count int
		if err := rows.Scan(&crit, &count); err == nil {
			stats[crit] = count
		}
	}
	return stats, nil
}

// handleSearchPage arama sayfası
func (s *Server) handleSearchPage(c *gin.Context) {
	c.HTML(http.StatusOK, "search.html", gin.H{
		"ActivePage": "search",
	})
}

// (pages parametresi POST /search içinde okunur)

// SearchStatus arama durumu
type SearchStatus struct {
	Query       string
	IsSearching bool
	Results     []storage.SearchResult
	TotalFound  int
	NewSaved    int
	Duration    string
	Error       string
}

// maxSearchQueryLen arama sorgusu için üst sınır (URL ve log güvenliği).
const maxSearchQueryLen = 200

// handleSearch arama işlemi
func (s *Server) handleSearch(c *gin.Context) {
	query := strings.Join(strings.Fields(c.PostForm("query")), " ")
	if query == "" || len([]rune(query)) > maxSearchQueryLen {
		c.HTML(http.StatusBadRequest, "search.html", gin.H{
			"ActivePage": "search",
			"error":      "Arama sorgusu boş olamaz ve 200 karakteri aşamaz",
		})
		return
	}

	logger.Info("Web'den arama baslatildi: '%s'", query)

	// Arama yap (90sn bütçeli, iptal-edilebilir context)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	knownURLs, err := s.db.GetKnownURLsForQuery(query)
	if err != nil {
		knownURLs = map[string]bool{}
	}

	pages, _ := strconv.Atoi(c.PostForm("pages"))
	if pages < 1 {
		pages = 1
	}
	startTime := time.Now()
	results := s.searcher.SearchAllPages(ctx, query, pages)
	elapsed := time.Since(startTime)

	// Sonuçları kaydet (KeywordHits ile birlikte)
	var storageResults []storage.SearchResult
	var newFindings []notify.Finding
	totalHits := 0
	for _, r := range results {
		if !knownURLs[r.URL] {
			newFindings = append(newFindings, notify.Finding{Title: r.Title, URL: r.URL, Category: r.Category, Criticality: r.Criticality})
		}
		storageResults = append(storageResults, storage.SearchResult{
			Title:        r.Title,
			URL:          r.URL,
			Source:       r.Source,
			Query:        query,
			Criticality:  r.Criticality,
			Category:     r.Category,
			KeywordCount: r.KeywordHits, // Anlık kelime sıklığı
		})
		totalHits += r.KeywordHits
	}

	savedCount, err := s.db.SaveResults(storageResults)
	if err != nil {
		logger.Error("Sonuçlar kaydedilemedi: %v", err)
	}
	if err := s.db.SaveSearchHistory(query, len(results)); err != nil {
		logger.Error("Arama geçmişi kaydedilemedi: %v", err)
	}

	// Genel bildirim merkezi: eşik üstü yeni bulgu varsa webhook (asenkron)
	if len(newFindings) > 0 {
		go scheduler.DispatchGlobalAlert(s.db, query, len(results), len(newFindings), newFindings, startTime)
	}

	c.HTML(http.StatusOK, "search.html", gin.H{
		"ActivePage": "search",
		"query":      query,
		"results":    results[:min(20, len(results))],
		"totalFound": len(results),
		"newSaved":   savedCount,
		"totalHits":  totalHits,
		"duration":   elapsed.Round(time.Millisecond).String(),
		"pages":      pages,
	})
}

// parseResultFilter sorgu parametrelerinden filtre üretir (HTML sayfası, JSON API ve export ortak).
func parseResultFilter(c *gin.Context) storage.ResultFilter {
	f := storage.ResultFilter{
		Query:      strings.TrimSpace(c.Query("q")),
		Text:       strings.TrimSpace(c.Query("text")),
		Source:     strings.TrimSpace(c.Query("source")),
		Category:   strings.TrimSpace(c.Query("category")),
		Tag:        strings.TrimSpace(c.Query("tag")),
		CaseStatus: strings.TrimSpace(c.Query("case")),
		Sort:       strings.TrimSpace(c.DefaultQuery("sort", "newest")),
	}
	f.MinCriticality, _ = strconv.Atoi(c.DefaultQuery("minCriticality", "1"))
	if f.MinCriticality < 1 || f.MinCriticality > 5 {
		f.MinCriticality = 1
	}
	f.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "50"))
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	f.Offset = (page - 1) * f.Limit
	return f
}

// handleResults kayıtlı sonuçlar (filtre + sayfalama)
func (s *Server) handleResults(c *gin.Context) {
	f := parseResultFilter(c)
	results, total, err := s.db.GetResultsFiltered(f)
	if err != nil {
		logger.Error("Sonuçlar getirilemedi: %v", err)
	}
	totalResults, _, err := s.db.GetStats()
	if err != nil {
		logger.Error("İstatistikler getirilemedi: %v", err)
	}
	sources, categories, _ := s.db.DistinctSourcesAndCategories()

	page := f.Offset/f.Limit + 1
	totalPages := (total + f.Limit - 1) / f.Limit
	if totalPages < 1 {
		totalPages = 1
	}

	// Sayfalama bağlantıları için mevcut filtreyi koru
	qs := c.Request.URL.Query()
	qs.Del("page")
	baseQuery := qs.Encode()

	c.HTML(http.StatusOK, "results.html", gin.H{
		"ActivePage":   "results",
		"results":      results,
		"query":        f.Query,
		"filter":       f,
		"total":        total,
		"totalResults": totalResults,
		"limit":        f.Limit,
		"page":         page,
		"totalPages":   totalPages,
		"hasPrev":      page > 1,
		"hasNext":      page < totalPages,
		"prevPage":     page - 1,
		"nextPage":     page + 1,
		"baseQuery":    baseQuery,
		"sources":      sources,
		"categories":   categories,
	})
}

// handleResultsGraph sonuçları graf olarak gösterir
func (s *Server) handleResultsGraph(c *gin.Context) {
	query := c.Query("q")
	c.HTML(http.StatusOK, "results_graph.html", gin.H{
		"ActivePage": "graph",
		"query":      query,
	})
}

// handleAnalytics analitik sayfasini sunar
func (s *Server) handleAnalytics(c *gin.Context) {
	c.HTML(http.StatusOK, "analytics.html", gin.H{
		"ActivePage": "analytics",
	})
}

func (s *Server) handleSettingsPage(c *gin.Context) {
	c.HTML(http.StatusOK, "settings.html", gin.H{
		"ActivePage": "settings",
	})
}

// handleScheduledPage planlı arama (İzleme Merkezi) sayfası
func (s *Server) handleScheduledPage(c *gin.Context) {
	cfg, _ := s.db.GetAlertConfig()
	searches, err := s.db.GetAllScheduledSearches()
	if err != nil {
		logger.Warn("Planlı taramalar alınamadı: %v", err)
	}
	c.HTML(http.StatusOK, "scheduled.html", gin.H{
		"ActivePage":  "scheduled",
		"alertConfig": cfg,
		"searches":    searches,
	})
}

// handleMonitorPage arama motoru sağlık izleme sayfası
func (s *Server) handleMonitorPage(c *gin.Context) {
	c.HTML(http.StatusOK, "monitor.html", gin.H{
		"ActivePage": "monitor",
	})
}

// handleWatchlistPage izleme listesi sayfası
func (s *Server) handleWatchlistPage(c *gin.Context) {
	c.HTML(http.StatusOK, "watchlist.html", gin.H{
		"ActivePage": "watchlist",
		"interval":   int(watchlistInterval().Minutes()),
	})
}
