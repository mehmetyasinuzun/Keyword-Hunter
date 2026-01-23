package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Server web sunucusu
type Server struct {
	db       *storage.DB
	searcher *search.Searcher
	scraper  *scraper.Scraper
	router   *gin.Engine
	sessions map[string]time.Time
	mu       sync.RWMutex
}

// Config sunucu yapılandırması
type Config struct {
	DB       *storage.DB
	Searcher *search.Searcher
	Scraper  *scraper.Scraper
	Username string
	Password string
}

var serverConfig Config

// New yeni web sunucusu oluşturur
func New(cfg Config) *Server {
	serverConfig = cfg

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	s := &Server{
		db:       cfg.DB,
		searcher: cfg.Searcher,
		scraper:  cfg.Scraper,
		router:   router,
		sessions: make(map[string]time.Time),
	}

	s.setupRoutes()
	return s
}

// setupRoutes rotaları ayarlar
func (s *Server) setupRoutes() {
	// Template'leri yükle
	tmpl := template.Must(template.New("").Funcs(template.FuncMap{
		"truncate": func(str string, length int) string {
			if len(str) <= length {
				return str
			}
			return str[:length] + "..."
		},
		"formatTime": func(t time.Time) string {
			return t.Format("02.01.2006 15:04")
		},
	}).ParseFS(templateFS, "templates/*.html"))
	s.router.SetHTMLTemplate(tmpl)

	// Static files - use sub filesystem to strip "static" prefix
	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		logger.Error("Static dosya sistemi oluşturulamadı: %v", err)
	}
	s.router.StaticFS("/static", http.FS(staticSubFS))

	// Public routes
	s.router.GET("/", s.handleIndex)
	s.router.GET("/login", s.handleLoginPage)
	s.router.POST("/login", s.handleLogin)
	s.router.GET("/logout", s.handleLogout)

	// Protected routes
	protected := s.router.Group("/")
	protected.Use(s.authMiddleware())
	{
		protected.GET("/dashboard", s.handleDashboard)
		protected.GET("/search", s.handleSearchPage)
		protected.POST("/search", s.handleSearch)
		protected.GET("/results", s.handleResults)
		protected.GET("/results/graph", s.handleResultsGraph)
		protected.GET("/contents", s.handleContents)
		protected.GET("/content/:id", s.handleContentDetail)
		protected.GET("/scrape", s.handleScrapePage)
		protected.POST("/scrape", s.handleScrape)
		protected.GET("/api/stats", s.handleStats)
		protected.GET("/api/graph", s.handleGraphAPI)
		protected.GET("/api/queries", s.handleQueriesAPI)
		protected.GET("/api/duplicates", s.handleDuplicatesAPI)

		// Derinleştir (Expand) API
		protected.POST("/api/expand", s.handleExpandNode)
		protected.GET("/api/graph/children/:id", s.handleGetChildren)
	}
}

// authMiddleware oturum kontrolü
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := c.Cookie("session")
		if err != nil {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		s.mu.RLock()
		expiry, exists := s.sessions[sessionID]
		s.mu.RUnlock()

		if !exists || time.Now().After(expiry) {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		c.Next()
	}
}

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
	totalURLs, scrapedCount, err := s.db.GetContentStats()
	if err != nil {
		logger.Error("Dashboard içerik istatistik hatası: %v", err)
	}

	// Son sonuçları getir
	recentResults, err := s.db.GetResults(10, "")
	if err != nil {
		logger.Error("Dashboard son sonuçlar hatası: %v", err)
	}

	// Son içerikleri getir
	recentContents, err := s.db.GetContents(5, "")
	if err != nil {
		logger.Error("Dashboard son içerikler hatası: %v", err)
	}

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"totalResults":   totalResults,
		"totalSearches":  totalSearches,
		"totalURLs":      totalURLs,
		"scrapedCount":   scrapedCount,
		"recentResults":  recentResults,
		"recentContents": recentContents,
	})
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
			Title:  r.Title,
			URL:    r.URL,
			Source: r.Source,
			Query:  query,
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

// handleGraphAPI graf verisi API endpoint
func (s *Server) handleGraphAPI(c *gin.Context) {
	query := c.Query("q")
	graphData, err := s.db.GetGraphData(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, graphData)
}

// handleStats istatistikler API
func (s *Server) handleStats(c *gin.Context) {
	totalResults, totalSearches, err := s.db.GetStats()
	if err != nil {
		logger.Error("İstatistikler getirilemedi: %v", err)
	}
	c.JSON(http.StatusOK, gin.H{
		"totalResults":  totalResults,
		"totalSearches": totalSearches,
	})
}

// handleQueriesAPI mevcut sorguları döndürür
func (s *Server) handleQueriesAPI(c *gin.Context) {
	queries, err := s.db.GetQueries()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"queries": queries,
	})
}

// handleContents scrape edilmiş içerikler sayfası
func (s *Server) handleContents(c *gin.Context) {
	query := c.Query("q")
	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Warn("Contents: Geçersiz limit değeri: %s", limitStr)
		limit = 50
	}
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	contents, err := s.db.GetContents(limit, query)
	if err != nil {
		logger.Error("İçerikler getirilemedi: %v", err)
	}
	_, scrapedCount, err := s.db.GetContentStats()
	if err != nil {
		logger.Error("İçerik istatistikleri getirilemedi: %v", err)
	}

	c.HTML(http.StatusOK, "contents.html", gin.H{
		"contents":     contents,
		"query":        query,
		"scrapedCount": scrapedCount,
		"limit":        limit,
	})
}

// handleScrapePage scrape sayfasını gösterir (GET)
func (s *Server) handleScrapePage(c *gin.Context) {
	// Son içerikleri getir
	contents, err := s.db.GetContents(5, "")
	if err != nil {
		logger.Error("Scrape sayfası içerik hatası: %v", err)
	}
	c.HTML(http.StatusOK, "scrape.html", gin.H{
		"contents": contents,
	})
}

// handleContentDetail içerik detay sayfası
func (s *Server) handleContentDetail(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.Redirect(http.StatusFound, "/contents")
		return
	}

	content, err := s.db.GetContentByID(id)
	if err != nil {
		c.Redirect(http.StatusFound, "/contents")
		return
	}

	c.HTML(http.StatusOK, "content_detail.html", gin.H{
		"content": content,
	})
}

// handleScrape scrape işlemi
func (s *Server) handleScrape(c *gin.Context) {
	limitStr := c.DefaultPostForm("limit", "10")
	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		logger.Warn("Geçersiz limit değeri: %s, varsayılan 10 kullanılıyor", limitStr)
		limit = 10
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	// Henüz scrape edilmemiş URL'leri al
	unscraped, err := s.db.GetUnscrapedURLs(limit)
	if err != nil || len(unscraped) == 0 {
		c.HTML(http.StatusOK, "scrape.html", gin.H{
			"error": "Scrape edilecek URL bulunamadı. Önce arama yapın.",
		})
		return
	}

	// URL listesi hazırla
	var urls []struct{ URL, Title string }
	for _, r := range unscraped {
		urls = append(urls, struct{ URL, Title string }{r.URL, r.Title})
	}

	logger.ScrapeStarted(len(urls))

	// Scrape başlat
	startTime := time.Now()
	results := s.scraper.ScrapeMultiple(urls, 3, nil) // 3 concurrent worker
	elapsed := time.Since(startTime)

	// Sonuçları kaydet
	successCount := 0
	for _, content := range results {
		if content.Success {
			err := s.db.SaveContent(content.URL, content.Title, content.RawContent, content.ContentSize)
			if err == nil {
				successCount++
			}
		}
	}

	logger.ScrapeCompleted(len(urls), successCount, len(urls)-successCount, elapsed)

	// Son içerikleri getir
	contents, err := s.db.GetContents(10, "")
	if err != nil {
		logger.Error("Scrape sonrası içerikler getirilemedi: %v", err)
	}

	c.HTML(http.StatusOK, "scrape.html", gin.H{
		"scraped":      true,
		"total":        len(urls),
		"success":      successCount,
		"failed":       len(urls) - successCount,
		"duration":     elapsed.Round(time.Millisecond).String(),
		"contents":     contents,
		"scrapeErrors": getScrapeErrors(results),
	})
}

// getScrapeErrors hata listesi döndürür
func getScrapeErrors(results []scraper.Content) []string {
	var errors []string
	for _, r := range results {
		if !r.Success && r.Error != "" {
			errors = append(errors, fmt.Sprintf("%s: %s", truncateStr(r.URL, 50), r.Error))
		}
	}
	return errors
}

func truncateStr(s string, length int) string {
	if len(s) <= length {
		return s
	}
	return s[:length] + "..."
}

// Run sunucuyu başlatır
func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}

// generateSessionID rastgele session ID üretir
func generateSessionID() string {
	return time.Now().Format("20060102150405") + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ============================================================================
// DERİNLEŞTİR (EXPAND) API HANDLERS
// ============================================================================

// ExpandRequest expand isteği
type ExpandRequest struct {
	URL      string `json:"url" binding:"required"`
	ParentID int64  `json:"parentId"`
	Query    string `json:"query"`
}

// handleExpandNode bir node'u derinleştirir
func (s *Server) handleExpandNode(c *gin.Context) {
	var req ExpandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}

	logger.Info("🔍 Derinleştirme başlatıldı: %s", req.URL)

	// URL'yi scrape et ve linkleri çıkar
	links, err := s.scraper.ExtractLinksFromURL(req.URL)
	if err != nil {
		logger.ExpandNode(req.URL, 0, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Sayfa taranamadı: %v", err),
		})
		return
	}

	// Parent node'u bul veya oluştur
	parentNode, err := s.db.GetGraphNodeByURL(req.URL)
	var parentID int64 = 0
	var parentDepth int = 0

	if err == nil && parentNode != nil {
		parentID = parentNode.ID
		parentDepth = parentNode.Depth
	} else if req.ParentID > 0 {
		// ParentID sağlandıysa onu kullan
		parentID = req.ParentID
		pNode, err := s.db.GetGraphNodeByID(req.ParentID)
		if err != nil {
			logger.Warn("Parent node bulunamadı (ID: %d): %v", req.ParentID, err)
		}
		if pNode != nil {
			parentDepth = pNode.Depth
		}
	}

	// Linkleri graph_nodes tablosuna kaydet
	var graphNodes []storage.GraphNodeDB
	baseDomain := extractDomainFromStr(req.URL)

	for _, link := range links {
		// Kendine link veriyorsa atla
		if link.URL == req.URL {
			continue
		}

		// ParentID pointer olarak ayarla
		var parentIDPtr *int64
		if parentID > 0 {
			parentIDPtr = &parentID
		}

		node := storage.GraphNodeDB{
			URL:         link.URL,
			Title:       link.Title,
			Domain:      link.Domain,
			ParentID:    parentIDPtr,
			Depth:       parentDepth + 1,
			LinkType:    link.LinkType,
			SourceQuery: req.Query,
		}
		graphNodes = append(graphNodes, node)
	}

	// Batch save
	savedCount := 0
	if len(graphNodes) > 0 {
		savedCount, err = s.db.SaveGraphNodes(graphNodes)
		if err != nil {
			logger.Warn("Graph nodes kaydedilemedi: %v", err)
		}
	}

	logger.ExpandNode(req.URL, len(links), nil)

	// Parent'ı expanded olarak işaretle
	if parentID > 0 {
		s.db.MarkNodeExpanded(parentID)
	}

	// Children node'ları getir
	children := buildChildrenNodes(links, baseDomain)

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"totalLinks":    len(links),
		"savedLinks":    savedCount,
		"internalCount": countByType(links, "internal"),
		"externalCount": countByType(links, "external"),
		"children":      children,
	})
}

// handleGetChildren bir node'un children'larını getirir
func (s *Server) handleGetChildren(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}

	children, err := s.db.GetGraphChildren(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"children": children,
	})
}

// buildChildrenNodes linkleri GraphNode formatına çevirir
func buildChildrenNodes(links []scraper.ExtractedLink, baseDomain string) []*storage.GraphNode {
	var internalNodes []*storage.GraphNode
	var externalNodes []*storage.GraphNode

	for _, link := range links {
		node := &storage.GraphNode{
			Name:   link.Title,
			URL:    link.URL,
			Type:   "link",
			Domain: link.Domain,
		}

		if link.LinkType == "internal" {
			internalNodes = append(internalNodes, node)
		} else {
			externalNodes = append(externalNodes, node)
		}
	}

	// Grup node'ları oluştur
	var children []*storage.GraphNode

	if len(internalNodes) > 0 {
		children = append(children, &storage.GraphNode{
			Name:     fmt.Sprintf("🔗 İç Linkler (%d)", len(internalNodes)),
			Type:     "internal-group",
			Children: internalNodes,
			Count:    len(internalNodes),
		})
	}

	if len(externalNodes) > 0 {
		children = append(children, &storage.GraphNode{
			Name:     fmt.Sprintf("🌐 Dış Linkler (%d)", len(externalNodes)),
			Type:     "external-group",
			Children: externalNodes,
			Count:    len(externalNodes),
		})
	}

	return children
}

// countByType link tipine göre sayar
func countByType(links []scraper.ExtractedLink, linkType string) int {
	count := 0
	for _, l := range links {
		if l.LinkType == linkType {
			count++
		}
	}
	return count
}

// extractDomainFromStr URL'den domain çıkarır - shared paketi kullanır
func extractDomainFromStr(urlStr string) string {
	return shared.ExtractDomain(urlStr)
}

// handleDuplicatesAPI çoklu URL'leri listele
func (s *Server) handleDuplicatesAPI(c *gin.Context) {
	duplicates, err := s.db.GetDuplicateURLs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"duplicates": duplicates,
		"count":      len(duplicates),
	})
}
