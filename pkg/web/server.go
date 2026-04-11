package web

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/config"
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
	db           *storage.DB
	searcher     *search.Searcher
	scraper      *scraper.Scraper
	username     string
	password     string
	cookieSecure bool
	sessionTTL   time.Duration
	envStore     *config.EnvStore
	rateLimiter  *IPRateLimiter
	tagEngine    TagEngine
	batchRunner  BatchRunner
	router       *gin.Engine
	httpServer   *http.Server
	cleanupStop  chan struct{}
}

// Config sunucu yapılandırması
type Config struct {
	DB             *storage.DB
	Searcher       *search.Searcher
	Scraper        *scraper.Scraper
	Username       string
	Password       string
	CookieSecure   bool
	SessionTTL     time.Duration
	RateLimitRPS   float64
	RateLimitBurst int
	EnvStore       *config.EnvStore
}

// New yeni web sunucusu oluşturur
func New(cfg Config) *Server {
	sessionTTL := cfg.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}

	rateLimitRPS := cfg.RateLimitRPS
	if rateLimitRPS <= 0 {
		rateLimitRPS = 12
	}

	rateLimitBurst := cfg.RateLimitBurst
	if rateLimitBurst <= 0 {
		rateLimitBurst = 30
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	s := &Server{
		db:           cfg.DB,
		searcher:     cfg.Searcher,
		scraper:      cfg.Scraper,
		username:     cfg.Username,
		password:     cfg.Password,
		cookieSecure: cfg.CookieSecure,
		sessionTTL:   sessionTTL,
		envStore:     cfg.EnvStore,
		rateLimiter:  NewIPRateLimiter(rateLimitRPS, rateLimitBurst),
		router:       router,
		cleanupStop:  make(chan struct{}),
	}

	s.router.Use(s.rateLimiter.Middleware())

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
				_, err := s.db.CleanupExpiredSessions(time.Now())
				if err != nil {
					logger.Warn("Session cleanup başarısız: %v", err)
				}
				s.rateLimiter.Cleanup(30 * time.Minute)
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
		protected.GET("/settings", s.handleSettingsPage)
		protected.GET("/scheduled", s.handleScheduledPage)

		// SSE Events - Gerçek zamanlı loglar için
		protected.GET("/events", s.handleEvents)

		protected.GET("/analytics", s.handleAnalytics)

		api := protected.Group("/api")
		api.Use(s.csrfMiddleware())
		{
			api.POST("/update-criticality", s.handleUpdateCriticality)
			api.POST("/analyze-result", s.handleAnalyzeResult)
			api.POST("/auto-tag", s.handleAutoTag)
			api.POST("/batch-auto-tag", s.handleBatchAutoTag)
			api.GET("/batch-auto-tag/:id", s.handleBatchAutoTagStatus)
			api.POST("/batch-auto-tag/:id/cancel", s.handleBatchAutoTagCancel)
			api.GET("/stats", s.handleStats)
			api.GET("/graph", s.handleGraphAPI)
			api.GET("/graph/queries", s.handleGraphQueriesAPI)
			api.GET("/graph/engines", s.handleGraphEnginesAPI)
			api.GET("/graph/results", s.handleGraphResultsAPI)
			api.GET("/queries", s.handleQueriesAPI)
			api.GET("/analytics", s.handleAnalyticsAPI)
			api.POST("/expand", s.handleExpandNode)
			api.GET("/graph/children/:id", s.handleGetChildren)
			api.GET("/tags", s.handleTagStats)
			api.GET("/results-by-tag", s.handleResultsByTag)
			api.GET("/new-results", s.handleNewResults)
			api.GET("/alert-config", s.handleAlertConfigGet)
			api.POST("/alert-config", s.handleAlertConfigSave)
			api.GET("/settings/env", s.handleEnvSettingsGet)
			api.POST("/settings/env", s.handleEnvSettingsUpdate)
		}
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
	select {
	case <-s.cleanupStop:
		// already closed
	default:
		close(s.cleanupStop)
	}

	if s.httpServer == nil {
		return nil
	}

	return s.httpServer.Shutdown(ctx)
}
