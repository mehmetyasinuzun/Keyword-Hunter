package web

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/capture"
	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/monitor"
	"keywordhunter-mvp/pkg/scheduler"
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
	Stop()
}

// Server web sunucusu
type Server struct {
	db            *storage.DB
	searcher      *search.Searcher
	scraper       *scraper.Scraper
	username      string
	password      string
	cookieSecure  bool
	sessionTTL    time.Duration
	envStore      *config.EnvStore
	rateLimiter   *IPRateLimiter
	tagEngine     TagEngine
	batchRunner   BatchRunner
	router        *gin.Engine
	httpServer    *http.Server
	cleanupStop   chan struct{}
	watchlistStop chan struct{}
	torProxy      string
	scheduler     *scheduler.Scheduler
	engineMonitor *monitor.EngineMonitor
	capturer      *capture.Capturer
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
	TorProxy       string
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
	// Proxy başlıklarına (X-Forwarded-For) güvenme - ClientIP spoofing önlenir
	_ = router.SetTrustedProxies(nil)
	router.MaxMultipartMemory = 1 << 20
	router.Use(gin.Recovery())
	router.Use(securityHeaders(cfg.CookieSecure))
	router.Use(bodyLimit(1 << 20))

	s := &Server{
		db:            cfg.DB,
		searcher:      cfg.Searcher,
		scraper:       cfg.Scraper,
		username:      cfg.Username,
		password:      cfg.Password,
		cookieSecure:  cfg.CookieSecure,
		sessionTTL:    sessionTTL,
		envStore:      cfg.EnvStore,
		rateLimiter:   NewIPRateLimiter(rateLimitRPS, rateLimitBurst),
		router:        router,
		cleanupStop:   make(chan struct{}),
		watchlistStop: make(chan struct{}),
		torProxy:      cfg.TorProxy,
	}

	s.router.Use(s.rateLimiter.Middleware())

	engine := tagging.NewEngine(cfg.DB, cfg.Scraper)
	runner := tagging.NewBatchRunner(cfg.DB, engine, 1)

	s.tagEngine = engine
	s.batchRunner = runner
	if err := s.batchRunner.RecoverPendingJobs(); err != nil {
		logger.Warn("Tagging job recovery başarısız: %v", err)
	}

	s.capturer = capture.New(cfg.TorProxy, "", "")
	if s.capturer.Available() {
		logger.Info("Ekran görüntüsü alt sistemi hazır (chromium bulundu)")
	} else {
		logger.Warn("Ekran görüntüsü devre dışı: chromium bulunamadı (yalnız Docker imajında mevcut)")
	}

	s.setupRoutes()
	s.startSessionCleanup()
	s.startWatchlistMonitor()
	s.startScheduler()
	return s
}

// startScheduler planlı arama motorunu ve arama motoru sağlık izleyicisini başlatır.
// Her ikisi de kaynak-verimli arka plan worker'larıdır; hata olsa bile uygulama açılır.
func (s *Server) startScheduler() {
	sched := scheduler.New(s.db, s.searcher)
	sched.Start()
	s.scheduler = sched

	em, err := monitor.New(s.db, s.torProxy, 5*time.Minute)
	if err != nil {
		logger.Warn("Motor izleyici başlatılamadı: %v", err)
		return
	}
	em.Start()
	s.engineMonitor = em
}

// securityHeaders temel güvenlik başlıklarını ekler
func securityHeaders(secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		if secure {
			c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		c.Next()
	}
}

// bodyLimit istek gövdesini sınırlar (DoS koruması)
func bodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
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

// startWatchlistMonitor izlenen siteleri periyodik olarak kontrol eden worker'ı başlatır
func (s *Server) startWatchlistMonitor() {
	interval := watchlistInterval()
	logger.Info("Watchlist monitor başlatıldı (interval: %v)", interval)

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.runWatchlistChecks()
			case <-s.watchlistStop:
				return
			}
		}
	}()
}

// watchlistInterval kontrol aralığını döndürür (env WATCHLIST_INTERVAL_MIN, varsayılan 15dk)
func watchlistInterval() time.Duration {
	if raw := strings.TrimSpace(os.Getenv("WATCHLIST_INTERVAL_MIN")); raw != "" {
		if mins, err := strconv.Atoi(raw); err == nil && mins > 0 {
			return time.Duration(mins) * time.Minute
		}
	}
	return 15 * time.Minute
}

// runWatchlistChecks tüm aktif izleme öğelerini sırayla kontrol eder
func (s *Server) runWatchlistChecks() {
	items, err := s.db.GetWatchlistItems(true)
	if err != nil {
		logger.Warn("Watchlist öğeleri alınamadı: %v", err)
		return
	}

	for _, item := range items {
		select {
		case <-s.watchlistStop:
			return
		default:
		}

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		result := s.checkWatchlistItem(ctx, item)
		cancel()

		if err := s.db.RecordWatchlistCheck(item.ID, result.Status, result.HTTPCode, result.ResponseMs, result.ContentHash, result.Title, result.Changed); err != nil {
			logger.Warn("Watchlist kontrol kaydedilemedi (ID: %d): %v", item.ID, err)
		}

		// Tetikleyici: izlenen sitede içerik değiştiyse otomatik ekran görüntüsü al
		// (görsel kanıt). Asenkron — kontrol döngüsünü bloklamaz.
		if result.Changed && s.capturer != nil && s.capturer.Available() {
			logger.Info("WATCHLIST DEĞİŞİKLİK: %s — ekran görüntüsü tetiklendi", item.Name)
			go s.captureAndStore("watchlist", item.ID, item.URL)
		}
	}
}

// captureAndStore bir hedefin ekran görüntüsünü alıp DB'ye kaydeder (tetikleyici eylemler için).
// Hata olsa bile 'error' kaydı düşer; sessiz yutma yok.
func (s *Server) captureAndStore(source string, refID int64, targetURL string) {
	if s.capturer == nil || !s.capturer.Available() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()

	shot, err := s.capturer.Capture(ctx, targetURL)
	if err != nil {
		logger.Warn("Tetiklenen ekran görüntüsü başarısız (%s): %v", targetURL, err)
		_, _ = s.db.SaveScreenshot(storage.Screenshot{
			TargetURL: targetURL, Source: source, RefID: refID,
			Status: "error", ErrorMsg: truncateErr(err.Error()),
		})
		return
	}
	if shot.Challenge {
		logger.Info("TETİKLENEN GÖRÜNTÜ: %s — engel (%s), elle müdahale gerekebilir", targetURL, shot.ChallengeKind)
	}
	if _, err := s.db.SaveScreenshot(storage.Screenshot{
		TargetURL: targetURL, Source: source, RefID: refID,
		FilePath: shot.Path, SHA256: shot.SHA256,
		Width: shot.Width, Height: shot.Height, Bytes: shot.Bytes,
		Status: "ok", Title: shot.Title,
		Challenge: shot.Challenge, ChallengeKind: shot.ChallengeKind,
		TakenAt: shot.TakenAt,
	}); err != nil {
		logger.Warn("Tetiklenen ekran görüntüsü kaydedilemedi: %v", err)
	}
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
		protected.GET("/watchlist", s.handleWatchlistPage)
		protected.GET("/monitor", s.handleMonitorPage)
		protected.GET("/screenshot/file/:id", s.handleServeScreenshot)

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
			api.GET("/watchlist", s.handleWatchlistList)
			api.POST("/watchlist", s.handleWatchlistAdd)
			api.POST("/watchlist/:id/toggle", s.handleWatchlistToggle)
			api.POST("/watchlist/:id/delete", s.handleWatchlistDelete)
			api.POST("/watchlist/:id/check", s.handleWatchlistCheck)

			// Planlı arama (scheduler)
			api.GET("/scheduled", s.handleGetScheduledSearches)
			api.POST("/scheduled", s.handleCreateScheduled)
			api.POST("/scheduled/:id/toggle", s.handleToggleScheduled)
			api.DELETE("/scheduled/:id", s.handleDeleteScheduled)
			api.POST("/scheduled/:id/run-now", s.handleRunScheduledNow)

			// Arama motoru sağlık izleme
			api.GET("/engines", s.handleEnginesAPI)
			api.POST("/engines/check-now", s.handleEngineCheckNow)
			api.POST("/engines/:name/toggle", s.handleEngineToggle)
			api.GET("/monitor/summary", s.handleMonitorSummary)

			// Ekran görüntüsü
			api.POST("/screenshot", s.handleCaptureNow)
			api.GET("/screenshots", s.handleScreenshotsList)
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

	select {
	case <-s.watchlistStop:
		// already closed
	default:
		close(s.watchlistStop)
	}

	if s.scheduler != nil {
		s.scheduler.Stop()
	}
	if s.engineMonitor != nil {
		s.engineMonitor.Stop()
	}

	if s.httpServer == nil {
		if s.batchRunner != nil {
			s.batchRunner.Stop()
		}
		return nil
	}

	err := s.httpServer.Shutdown(ctx)

	// HTTP sunucusu kapandıktan sonra batch worker'ları drain et
	if s.batchRunner != nil {
		s.batchRunner.Stop()
	}

	return err
}
