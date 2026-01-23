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
		"error": c.Query("error"),
	})
}

// handleLogin giriş işlemi
func (s *Server) handleLogin(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")
	clientIP := c.ClientIP()

	if username == serverConfig.Username && password == serverConfig.Password {
		// Session oluştur
		sessionID := generateSessionID()
		s.mu.Lock()
		s.sessions[sessionID] = time.Now().Add(24 * time.Hour)
		s.mu.Unlock()

		logger.UserLogin(username, true, clientIP)

		c.SetCookie("session", sessionID, 86400, "/", "", false, true)
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
	}
	s.mu.Lock()
	delete(s.sessions, sessionID)
	s.mu.Unlock()

	c.SetCookie("session", "", -1, "/", "", false, true)
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

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"totalResults":     totalResults,
		"totalSearches":    totalSearches,
		"totalURLs":        totalResults,
		"recentResults":    recentResults,
		"searchHistory":    searchHistory, // Yeni veri
		"categoryStats":    categoryStats,
		"criticalityStats": criticalityStats,
		"critDescs":        critDescs,
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
	c.HTML(http.StatusOK, "search.html", gin.H{})
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

	logger.Info("🔍 Web'den arama başlatıldı: '%s'", query)

	// Arama yap
	startTime := time.Now()
	results := s.searcher.SearchAll(query)
	elapsed := time.Since(startTime)

	// Sonuçları kaydet
	var storageResults []storage.SearchResult
	for _, r := range results {
		storageResults = append(storageResults, storage.SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Source:      r.Source,
			Query:       query,
			Criticality: r.Criticality,
			Category:    r.Category,
		})
	}

	savedCount, err := s.db.SaveResults(storageResults)
	if err != nil {
		logger.Error("Sonuçlar kaydedilemedi: %v", err)
	}
	if err := s.db.SaveSearchHistory(query, len(results)); err != nil {
		logger.Error("Arama geçmişi kaydedilemedi: %v", err)
	}

	c.HTML(http.StatusOK, "search.html", gin.H{
		"query":      query,
		"results":    results[:min(20, len(results))],
		"totalFound": len(results),
		"newSaved":   savedCount,
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
		"query": query,
	})
}

// handleAnalytics analitik sayfasını sunar
func (s *Server) handleAnalytics(c *gin.Context) {
	c.HTML(http.StatusOK, "analytics.html", gin.H{})
}
