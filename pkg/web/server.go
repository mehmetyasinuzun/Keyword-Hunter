package web

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/capture"
	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/crawler"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/monitor"
	"keywordhunter-mvp/pkg/scheduler"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/tagging"
	"keywordhunter-mvp/pkg/tor"
	"keywordhunter-mvp/pkg/totp"
)

// Version uygulama sürümü (derlemede -ldflags ile geçersiz kılınabilir).
var Version = "0.15.0"

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// iconSVG özgün ikon sprite'ından <svg><use> döndürür (currentColor ile tema uyumlu).
func iconSVG(name, cls string) template.HTML {
	c := "ico"
	if cls != "" {
		c += " " + cls
	}
	return template.HTML(`<svg class="` + template.HTMLEscapeString(c) + `" aria-hidden="true"><use href="#i-` + template.HTMLEscapeString(name) + `"/></svg>`)
}

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
	creds         *credentialStore
	loginGuard    *loginGuard
	startedAt     time.Time
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
	torCtl        *tor.Controller
	crawler       *crawler.Runner

	engineCacheMu sync.Mutex
	engineCache   map[string]bool
	engineCacheAt time.Time

	// Denetim kaydı: saklama süresi (0 = süresiz), zincir durumu önbelleği ve
	// kilitli IP'den gelen tekrar denemeler için kayıt seyreltme.
	auditRetentionDays int
	auditChainMu       sync.Mutex
	auditChainCache    *storage.AuditChainStatus
	auditChainAt       time.Time
	auditNoise         auditThrottle
	auditWriteFails    atomic.Int64 // son başlatmadan beri yazılamayan olay
	auditWriteFailAt   atomic.Int64 // son yazma hatası (unix ms)

	// İki adımlı doğrulama: TOTP sırlarını şifreleyen mühürleyici (APP_SECRET
	// yoksa nil → kayıt kapalı), zorunluluk politikası ve bekleyen girişler.
	totpSealer         *totp.Sealer
	appSecretEphemeral bool
	require2FA         string
	pendingLogins      *pendingLogins
}

// Config sunucu yapılandırması
type Config struct {
	DB             *storage.DB
	Searcher       *search.Searcher
	Scraper        *scraper.Scraper
	Username       string
	Password       string // düz metin (boş olabilir)
	PasswordHash   string // bcrypt (tercih edilen)
	CookieSecure   bool
	SessionTTL     time.Duration
	RateLimitRPS   float64
	RateLimitBurst int
	EnvStore       *config.EnvStore
	TorProxy       string
	TorControlAddr string
	TorControlPass string
	// AuditRetentionDays denetim kaydı saklama süresi (gün); 0 = süresiz.
	AuditRetentionDays int
	// AppSecret TOTP sırlarını şifreleyen anahtarın kaynağı (64 hex).
	AppSecret string
	// AppSecretEphemeral sır .env'e yazılamadı (bu çalıştırmayla sınırlı):
	// yeni 2FA kaydı kapatılır.
	AppSecretEphemeral bool
	// Require2FA none | admins | all.
	Require2FA string
}

// New yeni web sunucusu oluşturur
func New(cfg Config) (*Server, error) {
	creds, err := newCredentialStore(cfg.Username, cfg.Password, cfg.PasswordHash)
	if err != nil {
		return nil, fmt.Errorf("kimlik deposu oluşturulamadı: %w", err)
	}
	// Çoklu kullanıcı: users tablosu boşsa .env admin'ini bootstrap admin olarak taşı.
	bootstrapped := ""
	if n, err := cfg.DB.CountUsers(); err == nil && n == 0 {
		hash, _ := creds.Update("", "") // mevcut hash'i al (parola değiştirmeden)
		if u, err := cfg.DB.CreateUser(cfg.Username, hash, storage.RoleAdmin); err != nil {
			logger.Warn("Bootstrap admin oluşturulamadı: %v", err)
		} else {
			bootstrapped = u.Username
			logger.Info("Bootstrap admin '%s' users tablosuna eklendi (rol: admin)", cfg.Username)
		}
	}

	sessionTTL := cfg.SessionTTL
	if sessionTTL <= 0 {
		sessionTTL = 24 * time.Hour
	}

	rateLimitRPS := cfg.RateLimitRPS
	if rateLimitRPS <= 0 {
		rateLimitRPS = 25
	}

	rateLimitBurst := cfg.RateLimitBurst
	if rateLimitBurst <= 0 {
		rateLimitBurst = 80
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	// Proxy başlıklarına (X-Forwarded-For) güvenme - ClientIP spoofing önlenir
	_ = router.SetTrustedProxies(nil)
	router.MaxMultipartMemory = 1 << 20
	router.Use(gin.Recovery())
	router.Use(securityHeaders(cfg.CookieSecure))
	router.Use(bodyLimit(1 << 20))
	router.Use(requestLogger())

	s := &Server{
		db:            cfg.DB,
		searcher:      cfg.Searcher,
		scraper:       cfg.Scraper,
		creds:         creds,
		loginGuard:    newLoginGuard(),
		startedAt:     time.Now(),
		cookieSecure:  cfg.CookieSecure,
		sessionTTL:    sessionTTL,
		envStore:      cfg.EnvStore,
		rateLimiter:   NewIPRateLimiter(rateLimitRPS, rateLimitBurst),
		router:        router,
		cleanupStop:   make(chan struct{}),
		watchlistStop: make(chan struct{}),
		torProxy:      cfg.TorProxy,

		auditRetentionDays: cfg.AuditRetentionDays,
		appSecretEphemeral: cfg.AppSecretEphemeral,
		require2FA:         cfg.Require2FA,
		pendingLogins:      newPendingLogins(),
	}
	if cfg.AppSecret != "" {
		sealer, err := totp.NewSealer(cfg.AppSecret)
		if err != nil {
			logger.Error("APP_SECRET geçersiz; 2FA kaydı ve TOTP doğrulaması kapalı: %v", err)
		} else {
			s.totpSealer = sealer
		}
	} else {
		logger.Warn("APP_SECRET tanımlı değil; 2FA kaydı ve TOTP doğrulaması kapalı (yalnız kurtarma kodları çalışır)")
	}
	if bootstrapped != "" {
		s.auditSystem("user.bootstrap", "user:"+bootstrapped, map[string]any{"role": storage.RoleAdmin}, true)
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
		logger.Info("Ekran görüntüsü + JS render alt sistemi hazır (chromium bulundu)")
		// Scraper'a JS render ve site profili (çerez/UA) yeteneği ver
		if r := s.capturer.AsScraperRenderer(); r != nil {
			cfg.Scraper.SetRenderer(r)
		}
	} else {
		logger.Warn("Ekran görüntüsü + JS render devre dışı: Chrome/Chromium bulunamadı. Kurun veya CHROME_BIN ile yolunu verin (Docker imajında hazır gelir)")
	}
	// Site profillerini (çerez/UA/renderJS) DB'den çöz
	cfg.Scraper.SetProfileResolver(func(host string) *scraper.SiteProfile {
		p, err := s.db.GetSiteProfile(host)
		if err != nil || p == nil {
			return nil
		}
		return &scraper.SiteProfile{Cookies: p.Cookies, UserAgent: p.UserAgent, RenderJS: p.RenderJS}
	})

	// Tor kontrol portu (NEWNYM) — yapılandırıldıysa
	if cfg.TorControlAddr != "" {
		s.torCtl = tor.New(cfg.TorControlAddr, cfg.TorControlPass, 2*time.Minute)
		if err := s.torCtl.Ping(); err != nil {
			logger.Warn("Tor kontrol portu (%s) yanıt vermiyor: %v", cfg.TorControlAddr, err)
		} else {
			logger.Info("Tor kontrol portu hazır (%s) — devre yenileme etkin", cfg.TorControlAddr)
		}
	}

	// Örümcek (site tarama) işçisi
	s.crawler = crawler.New(cfg.DB, cfg.Scraper)

	// Motor aktif/pasif durumu DB'den okunur ve aramayı gerçekten etkiler
	if s.searcher != nil {
		s.searcher.SetEngineFilter(s.engineEnabled)
	}

	s.setupRoutes()
	s.startSessionCleanup()
	s.startWatchlistMonitor()
	s.startScheduler()
	s.startAuditMaintenance()
	return s, nil
}

// engineEnabled engine_stats tablosundaki is_active bayrağını (kısa süreli önbellekle) döndürür.
func (s *Server) engineEnabled(name string) bool {
	s.engineCacheMu.Lock()
	defer s.engineCacheMu.Unlock()
	if time.Since(s.engineCacheAt) > 30*time.Second || s.engineCache == nil {
		stats, err := s.db.GetAllEngineStats()
		if err == nil {
			cache := make(map[string]bool, len(stats))
			for _, st := range stats {
				cache[st.Name] = st.IsActive
			}
			s.engineCache = cache
			s.engineCacheAt = time.Now()
		}
	}
	if s.engineCache == nil {
		return true
	}
	active, known := s.engineCache[name]
	if !known {
		return true
	}
	return active
}

// invalidateEngineCache motor aktiflik önbelleğini düşürür (toggle sonrası).
func (s *Server) invalidateEngineCache() {
	s.engineCacheMu.Lock()
	s.engineCache = nil
	s.engineCacheMu.Unlock()
}

// requestLogger yavaş veya hatalı istekleri loglar (her isteği değil: gürültü).
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		elapsed := time.Since(start)
		if status >= 500 || (status >= 400 && status != 401 && status != 404) || elapsed > 5*time.Second {
			if strings.HasPrefix(c.Request.URL.Path, "/events") {
				return
			}
			logger.WebRequest(c.Request.Method, c.Request.URL.Path, status, elapsed)
		}
	}
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
	// Tüm motorlar düşerse yeni Tor devresi iste (yapılandırıldıysa)
	if s.torCtl != nil {
		em.SetAllDownHook(func() {
			if err := s.torCtl.NewNym(false); err != nil {
				logger.Debug("Otomatik NEWNYM atlandı: %v", err)
			} else {
				s.invalidateEngineCache()
				s.auditSystem("tor.newnym", s.torCtl.Addr(), map[string]any{"auto": true, "trigger": "all_engines_down"}, true)
			}
		})
	}
	em.Start()
	s.engineMonitor = em
}

// contentSecurityPolicy tüm kaynakları aynı origin'e kilitler.
//
// script-src yalnızca 'self'tir: şablonlarda satır içi <script> bloğu ya da
// on*= olay işleyicisi YOKTUR (templates_csp_test.go bunu her şablon için
// zorlar). Tüm davranış /static/js altındaki dosyalardan gelir ve DOM'a
// data-action öznitelikleriyle bağlanır; sayfa verisi çalıştırılmayan
// <script type="application/json"> bloklarıyla taşınır. Böylece dark web'den
// gelen bir metin bir şekilde HTML'e sızsa bile tarayıcı onu betik olarak
// çalıştırmaz (XSS'e karşı ikinci savunma hattı).
//
// style-src 'unsafe-inline' bilinçli bir ödünleşimdir: D3/Chart.js ve dinamik
// ilerleme çubukları style özniteliği kullanır; stil enjeksiyonu betik
// çalıştıramaz.
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; " +
	"base-uri 'self'; form-action 'self'; object-src 'none'"

// securityHeaders temel güvenlik başlıklarını ekler
func securityHeaders(secure bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", contentSecurityPolicy)
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		c.Header("Cross-Origin-Opener-Policy", "same-origin")
		c.Header("Cross-Origin-Resource-Policy", "same-origin")
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
				if s.loginGuard != nil {
					s.loginGuard.Cleanup()
				}
				s.auditNoise.cleanup(30 * time.Minute)
				if s.pendingLogins != nil {
					s.pendingLogins.cleanup()
				}
				if _, err := s.db.PurgeStaleAPITokens(time.Now().Add(-tokenRetention)); err != nil {
					logger.Warn("API token temizliği başarısız: %v", err)
				}
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

// screenshotCooldown tetiklenen görüntüler için aynı hedefe uygulanan bekleme süresi.
const screenshotCooldown = 6 * time.Hour

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
		// (görsel kanıt). Asenkron — kontrol döngüsünü bloklamaz. Aynı hedef için
		// en fazla 6 saatte bir (dinamik sayfalar her kontrolde "değişti" üretebilir).
		if result.Changed && s.capturer != nil && s.capturer.Available() {
			last, _ := s.db.LastScreenshotAt(item.URL)
			if last.IsZero() || time.Since(last) > screenshotCooldown {
				logger.Info("WATCHLIST DEĞİŞİKLİK: %s — ekran görüntüsü tetiklendi", item.Name)
				go s.captureAndStore("watchlist", item.ID, item.URL)
			} else {
				logger.Debug("WATCHLIST DEĞİŞİKLİK: %s — görüntü bekleme süresinde, atlandı", item.Name)
			}
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
	if s.pendingLogins == nil {
		s.pendingLogins = newPendingLogins()
	}

	// Template'leri yükle (ana sayfalar + partials)
	tmpl := template.Must(template.New("").Funcs(templateFuncs()).ParseFS(templateFS, "templates/*.html", "templates/partials/*.html"))
	s.router.SetHTMLTemplate(tmpl)

	// Static files - use sub filesystem to strip "static" prefix
	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		logger.Error("Static dosya sistemi oluşturulamadı: %v", err)
	}
	static := s.router.Group("/static", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=86400, immutable")
		c.Next()
	})
	static.StaticFS("/", http.FS(staticSubFS))

	// Bilinmeyen adresler: markalı 404 (API için JSON)
	s.router.NoRoute(func(c *gin.Context) { s.renderError(c, http.StatusNotFound) })

	// Public routes
	s.router.GET("/", s.handleIndex)
	s.router.GET("/login", s.handleLoginPage)
	s.router.POST("/login", s.handleLogin)
	// İkinci adım (2FA): oturum yok; bekleyen giriş çerezi yalnız /login altında geçer.
	s.router.GET("/login/2fa", s.handleLogin2FAPage)
	s.router.POST("/login/2fa", s.handleLogin2FA)
	s.router.GET("/login/2fa/cancel", s.handleLogin2FACancel)
	s.router.GET("/logout", s.handleLogout)
	s.router.POST("/logout", s.handleLogout)
	s.router.GET("/healthz", s.handleHealthz)
	s.router.GET("/favicon.ico", s.handleFavicon)
	s.router.GET("/favicon.svg", s.handleFavicon)
	s.router.GET("/api/i18n", s.handleI18nCatalog)
	s.router.POST("/api/lang", s.handleSetLang)

	// Protected routes
	protected := s.router.Group("/")
	protected.Use(s.authMiddleware(), s.twoFactorGate())
	{
		protected.GET("/dashboard", s.handleDashboard)
		protected.GET("/search", s.handleSearchPage)
		// Arama DB'ye yazar ve Tor trafiği üretir: analyst+ ve CSRF zorunlu
		// (form alanı _csrf ya da X-CSRF-Token başlığı).
		protected.POST("/search", s.requireRole("analyst"), s.csrfMiddleware(), s.handleSearch)
		protected.GET("/results", s.handleResults)
		protected.GET("/results/graph", s.handleResultsGraph)
		protected.GET("/settings", s.requireRole("admin"), s.handleSettingsPage)
		protected.GET("/scheduled", s.handleScheduledPage)
		protected.GET("/watchlist", s.handleWatchlistPage)
		protected.GET("/monitor", s.handleMonitorPage)
		protected.GET("/crawl", s.handleCrawlPage)
		protected.GET("/users", s.requireRole("admin"), s.handleUsersPage)
		protected.GET("/audit", s.requireRole("admin"), s.handleAuditPage)
		protected.GET("/account", s.handleAccountPage)
		protected.GET("/screenshot/file/:id", s.handleServeScreenshot)

		// SSE Events - Gerçek zamanlı loglar için
		protected.GET("/events", s.handleEvents)

		protected.GET("/analytics", s.handleAnalytics)

		api := protected.Group("/api")
		api.Use(s.csrfMiddleware())
		// Yazma işlemleri (POST/DELETE) en az analyst; viewer yalnız okur.
		// İstisna: öz-hizmet uçları (kendi parolası, kendi API token'ları) her
		// rol için açıktır (bkz. selfServiceRoutes). "read" kapsamlı API token
		// viewer'a indirgenir ve burada yazmadan döner.
		api.Use(func(c *gin.Context) {
			if selfServiceRoutes[c.FullPath()] {
				c.Next()
				return
			}
			if !isSafeMethod(c.Request.Method) && roleRank(c.GetString("role")) < roleRank("analyst") {
				if c.GetString("tokenScope") == storage.TokenScopeRead {
					s.auditDenied(c, "token_scope", storage.TokenScopeWrite)
					c.AbortWithStatusJSON(403, gin.H{"error": "Okuma (read) kapsamlı API token yazma işlemi yapamaz"})
					return
				}
				s.auditDenied(c, "role", "analyst")
				c.AbortWithStatusJSON(403, gin.H{"error": "Salt-okunur (viewer) rol bu işlemi yapamaz"})
				return
			}
			c.Next()
		})
		{
			// Kullanıcı yönetimi + sistem ayarları: yalnız admin
			admin := api.Group("", s.requireRole("admin"))
			admin.GET("/users", s.handleUsersList)
			admin.POST("/users", s.handleUserCreate)
			admin.POST("/users/:id", s.handleUserUpdate)
			admin.POST("/users/:id/delete", s.handleUserDelete)
			admin.POST("/users/:id/2fa/reset", s.handleUserTwoFAReset)

			// Denetim kaydı (hash zincirli): yalnız admin okur/doğrular/dışa aktarır
			admin.GET("/audit", s.handleAuditList)
			admin.GET("/audit/export", s.handleAuditExport)
			admin.POST("/audit/verify", s.handleAuditVerify)

			// Herkes: kimlik + kendi parolası
			api.GET("/whoami", s.handleWhoami)
			api.POST("/me/password", s.handleMyPassword)

			// İki adımlı doğrulama (öz-hizmet; yalnız etkileşimli oturum)
			api.GET("/me/2fa", s.handleTwoFAStatus)
			api.POST("/me/2fa/setup", s.handleTwoFASetup)
			api.POST("/me/2fa/enable", s.handleTwoFAEnable)
			api.POST("/me/2fa/disable", s.handleTwoFADisable)
			api.POST("/me/2fa/recovery-codes", s.handleTwoFARecoveryCodes)

			// Kişisel API token'ları (yalnız etkileşimli oturum; token ile çağrılamaz)
			api.GET("/tokens", s.handleTokensList)
			api.POST("/tokens", s.handleTokenCreate)
			api.POST("/tokens/:id/revoke", s.handleTokenRevoke)

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
			api.GET("/alert-config", s.handleAlertConfigGet) // admin dışı gizlenmiş döner
			api.POST("/alert-config", s.handleAlertConfigSave)
			api.POST("/alert-config/test", s.handleAlertConfigTest)
			api.GET("/export/results", s.handleExportResults)
			api.GET("/results", s.handleResultsAPI)
			api.POST("/results/delete", s.handleDeleteResults)
			api.POST("/results/:id/case", s.handleUpdateCase)
			api.GET("/results/case-counts", s.handleCaseCounts)
			api.GET("/results/:id/artifacts", s.handleArtifactsForResult)
			api.GET("/artifacts", s.handleArtifactSearch)
			api.GET("/artifacts/counts", s.handleArtifactCounts)

			// STIX 2.1 export
			api.GET("/export/stix", s.handleExportSTIX)

			// Örümcek (site tarama)
			api.GET("/crawl", s.handleCrawlList)
			api.POST("/crawl", s.handleCrawlSubmit)
			api.POST("/crawl/:id/cancel", s.handleCrawlCancel)
			api.GET("/crawl/:id", s.handleCrawlStatus)
			api.GET("/artifacts/stats", s.handleArtifactStats)
			admin.GET("/settings/env", s.handleEnvSettingsGet)
			admin.POST("/settings/env", s.handleEnvSettingsUpdate)
			admin.GET("/site-profiles", s.handleSiteProfilesList)
			admin.POST("/site-profiles", s.handleSiteProfileSave)
			admin.POST("/site-profiles/delete", s.handleSiteProfileDelete)
			admin.GET("/tor/status", s.handleTorStatus)
			admin.POST("/tor/newnym", s.handleTorNewNym)
			api.GET("/watchlist", s.handleWatchlistList)
			api.POST("/watchlist", s.handleWatchlistAdd)
			api.POST("/watchlist/:id/toggle", s.handleWatchlistToggle)
			api.POST("/watchlist/:id/delete", s.handleWatchlistDelete)
			api.POST("/watchlist/:id/check", s.handleWatchlistCheck)
			api.POST("/watchlist/seed", s.handleWatchlistSeed)

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
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Uzun süren işlemler (Tor araması ~90sn, ekran görüntüsü ~90sn) için geniş;
		// SSE akışı kendi yazma süresini handler içinde sıfırlar.
		WriteTimeout:   5 * time.Minute,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 64 << 10,
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
	if s.crawler != nil {
		s.crawler.Stop()
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
