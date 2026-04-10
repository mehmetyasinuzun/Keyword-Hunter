package web

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/tagging"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

type TagEngine interface {
	TagResultByID(ctx context.Context, resultID int64) (*tagging.AutoTagResult, error)
}

type BatchRunner interface {
	Submit(ctx context.Context, resultIDs []int64, query string) (*storage.TaggingJob, error)
	Cancel(jobID string) error
	RecoverPendingJobs() error
}

// Server web sunucusu
type Server struct {
	db          *storage.DB
	searcher    *search.Searcher
	scraper     *scraper.Scraper
	tagEngine   TagEngine
	batchRunner BatchRunner
	router      *gin.Engine
	httpServer  *http.Server
	sessions    map[string]time.Time
	cleanupStop chan struct{}
	cleanupOnce sync.Once
	mu          sync.RWMutex
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
		db:          cfg.DB,
		searcher:    cfg.Searcher,
		scraper:     cfg.Scraper,
		router:      router,
		sessions:    make(map[string]time.Time),
		cleanupStop: make(chan struct{}),
	}

	engine := tagging.NewEngine(cfg.DB, cfg.Scraper)
	runner := tagging.NewBatchRunner(cfg.DB, engine, 1)

	s.tagEngine = engine
	s.batchRunner = runner
	if err := s.batchRunner.RecoverPendingJobs(); err != nil {
		logger.Warn("Tagging job recovery başarısız: %v", err)
	}

	s.setupRoutes()
	s.startSessionCleanup()
	return s
}

func (s *Server) startSessionCleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				s.mu.Lock()
				for id, expiry := range s.sessions {
					if now.After(expiry) {
						delete(s.sessions, id)
					}
				}
				s.mu.Unlock()
			case <-s.cleanupStop:
				return
			}
		}
	}()
}

// setupRoutes rotaları ayarlar
func (s *Server) setupRoutes() {
	// Template'leri yükle (ana sayfalar + partials)
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
		"split": func(s, sep string) []string {
			if s == "" {
				return []string{}
			}
			return strings.Split(s, sep)
		},
		"eq": func(a, b interface{}) bool {
			return a == b
		},
	}).ParseFS(templateFS, "templates/*.html", "templates/partials/*.html"))
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
		protected.POST("/api/auto-tag", s.handleAutoTag)
		protected.POST("/api/batch-auto-tag", s.handleBatchAutoTag)
		protected.GET("/api/batch-auto-tag/:id", s.handleBatchAutoTagStatus)
		protected.POST("/api/batch-auto-tag/:id/cancel", s.handleBatchAutoTagCancel)
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

		// Etiket İstatistikleri API
		protected.GET("/api/tags", s.handleTagStats)
		protected.GET("/api/results-by-tag", s.handleResultsByTag)
	}
}

// Run sunucuyu başlatır
func (s *Server) Run(addr string) error {
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	err := s.httpServer.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		return err
	}

	return nil
}

// Shutdown sunucuyu graceful şekilde kapatır.
func (s *Server) Shutdown(ctx context.Context) error {
	s.cleanupOnce.Do(func() {
		close(s.cleanupStop)
	})

	if s.httpServer == nil {
		return nil
	}

	return s.httpServer.Shutdown(ctx)
}
