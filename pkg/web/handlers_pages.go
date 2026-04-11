package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

// handleIndex ana sayfa
func (s *Server) handleIndex(c *gin.Context) {
	c.Redirect(http.StatusFound, "/login")
}

// handleLoginPage login sayfası
func (s *Server) handleLoginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", gin.H{
		"error":   c.Query("error"),
		"message": c.Query("message"),
	})
}

// handleLogin giriş işlemi
func (s *Server) handleLogin(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	clientIP := c.ClientIP()

	if username == s.username && password == s.password {
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

	logger.UserLogin(username, false, clientIP)
	c.Redirect(http.StatusFound, "/login?error=1")
}

// handleLogout çıkış işlemi
func (s *Server) handleLogout(c *gin.Context) {
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
		1: "🟢 Seviye 1 (Düşük): Genel forum tartışmaları, haberler ve düşük riskli içerikler.",
		2: "🔵 Seviye 2 (Orta): Şüpheli aktiviteler, doğrulanmamış sızıntı iddiaları.",
		3: "🟡 Seviye 3 (Yüksek): Doğrulanmış veri sızıntıları, hassas kişisel bilgiler (PII).",
		4: "🟠 Seviye 4 (Kritik): Veritabanı sızıntıları, kredi kartı bilgileri, illegal ticaret.",
		5: "🔴 Seviye 5 (Acil): Ransomware, 0day exploitler, devlet sırları ve çok yüksek riskli içerikler.",
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

// handleSearch arama işlemi
func (s *Server) handleSearch(c *gin.Context) {
	query := strings.TrimSpace(c.PostForm("query"))
	if query == "" {
		c.HTML(http.StatusOK, "search.html", gin.H{
			"error": "Arama sorgusu boş olamaz",
		})
		return
	}

	logger.Info("Web'den arama baslatildi: '%s'", query)

	// Arama yap
	startTime := time.Now()
	results := s.searcher.SearchAll(query)
	elapsed := time.Since(startTime)

	// Sonuçları kaydet (KeywordHits ile birlikte)
	var storageResults []storage.SearchResult
	totalHits := 0
	for _, r := range results {
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

	c.HTML(http.StatusOK, "search.html", gin.H{
		"ActivePage": "search",
		"query":      query,
		"results":    results[:min(20, len(results))],
		"totalFound": len(results),
		"newSaved":   savedCount,
		"totalHits":  totalHits,
		"duration":   elapsed.Round(time.Millisecond).String(),
	})
}

// handleResults kayıtlı sonuçlar
func (s *Server) handleResults(c *gin.Context) {
	query := c.Query("q")
	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Warn("Results: Geçersiz limit değeri: %s", limitStr)
		limit = 50
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	results, err := s.db.GetResults(limit, query)
	if err != nil {
		logger.Error("Sonuçlar getirilemedi: %v", err)
	}
	totalResults, _, err := s.db.GetStats()
	if err != nil {
		logger.Error("İstatistikler getirilemedi: %v", err)
	}

	c.HTML(http.StatusOK, "results.html", gin.H{
		"ActivePage":   "results",
		"results":      results,
		"query":        query,
		"totalResults": totalResults,
		"limit":        limit,
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

// handleScheduledPage scheduler ve bildirim ayarları sayfası
func (s *Server) handleScheduledPage(c *gin.Context) {
	cfg, _ := s.db.GetAlertConfig()
	c.HTML(http.StatusOK, "scheduled.html", gin.H{
		"ActivePage":  "scheduled",
		"alertConfig": cfg,
	})
}
