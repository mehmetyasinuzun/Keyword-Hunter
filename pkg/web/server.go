package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/search"
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
		"seq": func(start, end int) []int {
			var res []int
			for i := start; i <= end; i++ {
				res = append(res, i)
			}
			return res
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
		protected.POST("/api/update-criticality", s.handleUpdateCriticality)
		protected.POST("/api/analyze-result", s.handleAnalyzeResult)
		protected.GET("/api/stats", s.handleStats)
		protected.GET("/api/graph", s.handleGraphAPI)
		protected.GET("/api/queries", s.handleQueriesAPI)

		protected.GET("/api/analytics", s.handleAnalyticsAPI)

		// SSE Events - Gerçek zamanlı loglar için
		protected.GET("/events", s.handleEvents)

		protected.GET("/analytics", s.handleAnalytics)

		// Derinleştir (Expand) API
		protected.POST("/api/expand", s.handleExpandNode)
		protected.GET("/api/graph/children/:id", s.handleGetChildren)
	}
}

// Run sunucuyu başlatır
func (s *Server) Run(addr string) error {
	return s.router.Run(addr)
}
