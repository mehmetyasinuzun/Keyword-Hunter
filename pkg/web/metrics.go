package web

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/metrics"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── Prometheus metrikleri ───────────────────────────
//
// GET /metrics yalnız METRICS_ENABLED=true iken kaydedilir (kapalıyken 404).
// Kimlik: ASLA anonim değildir. Mevcut kişisel API token sistemi kullanılır —
// "Authorization: Bearer kh_…" — ama YALNIZ ayrılmış "metrics" kapsamlı token
// kabul edilir ve token'ın SAHİBİ admin rolünde olmalıdır. "metrics" token'ı
// /api/ ve /taxii2/'de reddedilir: Prometheus sunucusundan sızan kimlik bulgu
// ve IOC okuyamaz; "read"/"write" token'ları da burada reddedilir (genel API
// kimliği kazıma yapılandırmasına konmasın). Ayrı bir METRICS_TOKEN yerine bu
// seçildi çünkü token'lar zaten özetle saklanır, süresi dolar, tek tıkla iptal
// edilir, son kullanımı izlenir, geçersiz denemeler denetlenir ve sahibi
// pasifleşince/silinince kendiliğinden geçersizleşir; .env'de düz metin,
// dönüşümsüz ikinci bir sır tutulmaz. Admin şartı: metrikler operasyonel durum
// açığa çıkarır (denetim zinciri, yedek, giriş hataları), bunlar da arayüzde
// yalnız admin'e açıktır. Oturum çerezi bu uçta okunmaz.
//
// Olay sayaçları pkg/metrics'te süreç geneldir; veritabanından türetilen
// göstergeler (bulgu sayıları, motor sağlığı, oturumlar…) aşağıdaki
// toplayıcıda en fazla metricsCacheTTL'de bir okunur: sık kazıma SQLite'ı
// yormaz. Ayrıntı ve metrik listesi: docs/METRICS.md.

const (
	// metricsCacheTTL veritabanı göstergelerinin yenilenme aralığı (en fazla).
	metricsCacheTTL = 30 * time.Second
	// metricsMaxEngines motor etiketli serilerin üst sınırı (kardinalite emniyeti).
	metricsMaxEngines = 64
	// torProbeTimeout Tor SOCKS ucuna erişilebilirlik denemesinin süresi.
	torProbeTimeout = 2 * time.Second
	// metricsScrapeTimeout tek bir kazımanın üst süresi.
	metricsScrapeTimeout = 20 * time.Second
)

// auditChainSample son zincir doğrulamasının metrik özeti (kilitsiz okunur:
// tam doğrulama auditChainMu'yu uzun tutabilir, kazıma onu beklememeli).
type auditChainSample struct {
	OK          bool
	VerifyError bool      // son doğrulama hata verdi (OK da false'tur)
	VerifiedAt  time.Time // son başarılı TAM doğrulama (yoksa sıfır)
}

// httpMetricsMiddleware isteği yöntem, Gin rota kalıbı ve durum sınıfıyla
// sayar, süresini ölçer. Ham yol ASLA etiket olmaz (eşleşmeyen rota
// "unmatched"). En dışta (Recovery'den önce) kurulur ki panik sonrası 500'ler
// de sayılsın. Uzun ömürlü SSE akışının süresi histograma yazılmaz.
func httpMetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		metrics.ObserveHTTP(c.Request.Method, route, c.Writer.Status(), time.Since(start), route != "/events")
	}
}

// initMetrics sunucuya özel kayıt defterini ve /metrics işleyicisini kurar.
func (s *Server) initMetrics() error {
	s.metricsCollector = newDBMetricsCollector(s)
	reg, err := metrics.NewRegistry(Version, s.metricsCollector)
	if err != nil {
		return err
	}
	s.metricsHandler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog:            promLogger{},
		ErrorHandling:       promhttp.ContinueOnError,
		MaxRequestsInFlight: 4,
		Timeout:             metricsScrapeTimeout,
	})
	return nil
}

// promLogger promhttp hata günlüğünü uygulama günlüğüne bağlar.
type promLogger struct{}

func (promLogger) Println(v ...any) { logger.Warn("Prometheus metrikleri: %s", fmt.Sprint(v...)) }

// metricsAuth /metrics kimlik doğrulaması (bkz. dosya başı açıklaması).
func (s *Server) metricsAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		raw, ok := bearerToken(c)
		if !ok {
			c.Header("WWW-Authenticate", `Bearer realm="KeywordHunter metrics"`)
			apiAbort(c, http.StatusUnauthorized, "metrics_token_required")
			return
		}
		t, u, failure := s.verifyAPIToken(c, raw)
		if failure != nil {
			c.Header("WWW-Authenticate", `Bearer realm="KeywordHunter metrics", error="invalid_token"`)
			apiAbort(c, http.StatusUnauthorized, failure.Code)
			return
		}
		setTokenContext(c, t, u)
		// Yalnız ayrılmış kazıma kapsamı: genel amaçlı read/write token'ı
		// (tüm bulgulara erişen API kimliği) Prometheus'a konmasın.
		if t.Scope != storage.TokenScopeMetrics {
			if s.auditNoise.allow(fmt.Sprintf("metrics-scope:%d", t.ID), time.Minute) {
				s.auditDenied(c, "token_scope", storage.TokenScopeMetrics)
			}
			apiAbort(c, http.StatusForbidden, "metrics_scope_required")
			return
		}
		// Sahibinin GÜNCEL rolü admin olmalı (her istekte users tablosundan
		// okunur): admin'likten düşürülen hesabın kazıma token'ı anında çalışmaz.
		if u.Role != storage.RoleAdmin {
			// Yanlış yapılandırılmış bir Prometheus her 15 sn'de dener: denetim
			// kaydı token başına dakikada bir.
			if s.auditNoise.allow(fmt.Sprintf("metrics-denied:%d", t.ID), time.Minute) {
				s.auditDenied(c, "metrics_role", storage.RoleAdmin)
			}
			apiAbort(c, http.StatusForbidden, "metrics_admin_only")
			return
		}
		// REQUIRE_2FA kapsamındaki ama 2FA'sı olmayan hesabın token'ı /api/ ve
		// /taxii2/'de olduğu gibi burada da çalışmaz.
		if s.twoFactorPolicyActive() && s.twoFactorRequiredFor(u.Role) {
			on, err := s.db.TwoFactorEnabled(u.ID)
			if err != nil {
				respondInternalError(c, "metrics 2FA", err)
				c.Abort()
				return
			}
			if !on {
				if s.auditNoise.allow("2fa-required:"+strings.ToLower(u.Username)+":metrics", time.Minute) {
					s.auditDenied(c, "2fa_required", "")
				}
				apiAbort(c, http.StatusForbidden, "2fa_required")
				return
			}
		}
		s.touchAPIToken(c, t)
		c.Next()
	}
}

// handleMetrics Prometheus metin biçimini (ya da istenirse protobuf) yazar.
func (s *Server) handleMetrics(c *gin.Context) {
	if s.metricsHandler == nil {
		apiError(c, http.StatusNotFound, "not_found")
		return
	}
	s.metricsHandler.ServeHTTP(c.Writer, c.Request)
}

// ─────────────────────────── Veritabanı göstergeleri ───────────────────────────

var (
	descFindings = prometheus.NewDesc(metrics.Namespace+"_findings",
		"Stored findings by severity (criticality 1-5).", []string{"severity"}, nil)
	descActiveSessions = prometheus.NewDesc(metrics.Namespace+"_active_sessions",
		"Unexpired interactive login sessions.", nil, nil)
	descWatchSites = prometheus.NewDesc(metrics.Namespace+"_watchlist_sites",
		"Enabled watchlist sites.", nil, nil)
	descWatchDown = prometheus.NewDesc(metrics.Namespace+"_watchlist_sites_down",
		"Enabled watchlist sites whose latest check was 'down' (blocked/captcha pages are not counted).", nil, nil)
	descEngineUp = prometheus.NewDesc(metrics.Namespace+"_engine_up",
		"1 if the engine's latest health check succeeded, 0 if it failed (absent before the first check).", []string{"engine"}, nil)
	descEngineActive = prometheus.NewDesc(metrics.Namespace+"_engine_active",
		"1 if the engine is enabled for searches on /monitor.", []string{"engine"}, nil)
	descEngineResp = prometheus.NewDesc(metrics.Namespace+"_engine_response_seconds",
		"Response time of the engine's latest health check, only while that check succeeded (absent when the engine is down).", []string{"engine"}, nil)
	descEngineRatio = prometheus.NewDesc(metrics.Namespace+"_engine_success_ratio",
		"Share of successful health checks since the engine was added (0-1).", []string{"engine"}, nil)
	descBackupEnabled = prometheus.NewDesc(metrics.Namespace+"_backup_enabled",
		"1 if scheduled backups are configured (BACKUP_DIR).", nil, nil)
	descBackupStatus = prometheus.NewDesc(metrics.Namespace+"_backup_last_status",
		"1 if the latest scheduled backup succeeded, 0 if it failed (absent before the first run).", nil, nil)
	descBackupLastRun = prometheus.NewDesc(metrics.Namespace+"_backup_last_run_timestamp_seconds",
		"Unix time the latest scheduled backup finished.", nil, nil)
	descBackupLastOK = prometheus.NewDesc(metrics.Namespace+"_backup_last_success_timestamp_seconds",
		"Unix time of the latest successful scheduled backup.", nil, nil)
	descTorReachable = prometheus.NewDesc(metrics.Namespace+"_tor_reachable",
		"1 if the Tor SOCKS proxy (TOR_PROXY) answered a SOCKS5 handshake.", nil, nil)
	descAuditOK = prometheus.NewDesc(metrics.Namespace+"_audit_chain_ok",
		"1 if the latest audit log hash chain verification found it intact, 0 if it was broken or could not be verified (absent before the first verification).", nil, nil)
	descAuditVerifyErr = prometheus.NewDesc(metrics.Namespace+"_audit_chain_verify_error",
		"1 if the latest audit chain verification failed with an error (unreadable row, missing chain state), 0 otherwise.", nil, nil)
	descAuditVerified = prometheus.NewDesc(metrics.Namespace+"_audit_chain_last_verify_timestamp_seconds",
		"Unix time of the latest full audit chain verification.", nil, nil)
	descSnapshotAt = prometheus.NewDesc(metrics.Namespace+"_metrics_snapshot_timestamp_seconds",
		"Unix time the database-derived gauges were last refreshed (refreshed at most every 30 s).", nil, nil)
	descSnapshotErrors = prometheus.NewDesc(metrics.Namespace+"_metrics_snapshot_errors_total",
		"Failed refreshes of the database-derived gauges (the previous values are kept).", nil, nil)
)

// dbMetricsCollector veritabanından türetilen göstergeleri önbellekle sunar.
type dbMetricsCollector struct {
	s   *Server
	ttl time.Duration

	mu        sync.Mutex
	at        time.Time // son yenileme denemesi
	okAt      time.Time // son başarılı yenileme
	snap      *storage.MetricsSnapshot
	torOK     bool
	errors    float64
	refreshes int // test gözlemi: veritabanı kaç kez okundu
}

func newDBMetricsCollector(s *Server) *dbMetricsCollector {
	return &dbMetricsCollector{s: s, ttl: metricsCacheTTL}
}

func (m *dbMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{
		descFindings, descActiveSessions, descWatchSites, descWatchDown,
		descEngineUp, descEngineActive, descEngineResp, descEngineRatio,
		descBackupEnabled, descBackupStatus, descBackupLastRun, descBackupLastOK,
		descTorReachable, descAuditOK, descAuditVerifyErr, descAuditVerified, descSnapshotAt, descSnapshotErrors,
	} {
		ch <- d
	}
}

// refresh önbellek süresi dolduysa veritabanını (ve Tor ucunu) yeniden okur.
// Kilit yenileme boyunca tutulur: eşzamanlı kazımalar tek okuma paylaşır.
func (m *dbMetricsCollector) refresh(now time.Time) {
	if !m.at.IsZero() && now.Sub(m.at) < m.ttl {
		return
	}
	m.at = now
	m.refreshes++
	snap, err := m.s.db.MetricsSnapshot(now)
	if err != nil {
		m.errors++
		logger.Warn("Metrik göstergeleri okunamadı (önceki değerler korunuyor): %v", err)
	} else {
		m.snap = &snap
		m.okAt = now
	}
	if m.s.torProxy != "" {
		m.torOK = probeSOCKS5(m.s.torProxy, torProbeTimeout)
	}
}

func gauge(ch chan<- prometheus.Metric, d *prometheus.Desc, v float64, labels ...string) {
	ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (m *dbMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	m.mu.Lock()
	m.refresh(time.Now())
	snap, okAt, torOK, errs := m.snap, m.okAt, m.torOK, m.errors
	m.mu.Unlock()

	ch <- prometheus.MustNewConstMetric(descSnapshotErrors, prometheus.CounterValue, errs)
	gauge(ch, descBackupEnabled, boolFloat(m.s.backupDir != ""))
	if m.s.torProxy != "" {
		gauge(ch, descTorReachable, boolFloat(torOK))
	}
	if a := m.s.auditChainSample.Load(); a != nil {
		gauge(ch, descAuditOK, boolFloat(a.OK))
		gauge(ch, descAuditVerifyErr, boolFloat(a.VerifyError))
		if !a.VerifiedAt.IsZero() {
			gauge(ch, descAuditVerified, float64(a.VerifiedAt.Unix()))
		}
	}
	if snap == nil {
		return
	}
	gauge(ch, descSnapshotAt, float64(okAt.Unix()))
	for sev := 1; sev <= 5; sev++ {
		gauge(ch, descFindings, float64(snap.FindingsBySeverity[sev]), metrics.Severity(sev))
	}
	gauge(ch, descActiveSessions, float64(snap.ActiveSessions))
	gauge(ch, descWatchSites, float64(snap.WatchlistEnabled))
	gauge(ch, descWatchDown, float64(snap.WatchlistDown))
	for i, e := range snap.Engines {
		if i >= metricsMaxEngines {
			break
		}
		gauge(ch, descEngineActive, boolFloat(e.IsActive), e.Name)
		switch e.LastStatus {
		case "up":
			gauge(ch, descEngineUp, 1, e.Name)
		case "down":
			gauge(ch, descEngineUp, 0, e.Name)
		}
		// Yalnız başarılı kontrolün süresi: başarısız kontrolde kayıtlı süre
		// hatanın ne kadar sürdüğüdür (reddedilen bağlantı ≈ 0 sn) ve düşük
		// motoru "en hızlı" gösterirdi.
		if e.LastCheckedAt != nil && e.LastStatus == "up" {
			gauge(ch, descEngineResp, float64(e.LastResponseMs)/1000, e.Name)
		}
		if total := e.SuccessCount + e.FailCount; total > 0 {
			gauge(ch, descEngineRatio, float64(e.SuccessCount)/float64(total), e.Name)
		}
	}
	if b := snap.Backup; b != nil {
		gauge(ch, descBackupStatus, boolFloat(b.OK))
		gauge(ch, descBackupLastRun, float64(b.FinishedAt.Unix()))
		if b.LastSuccessAt != nil {
			gauge(ch, descBackupLastOK, float64(b.LastSuccessAt.Unix()))
		}
	}
}

// probeSOCKS5 Tor SOCKS ucunun gerçekten bir SOCKS5 sunucusu olarak yanıt
// verdiğini doğrular: kimliksiz selamlama (05 01 00) → 05 00 beklenir. Hiçbir
// hedefe bağlantı istenmez; yalnız yerel/yapılandırılmış proxy'ye dokunulur.
func probeSOCKS5(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return false
	}
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return false
	}
	var resp [2]byte
	if _, err := io.ReadFull(conn, resp[:]); err != nil {
		return false
	}
	return resp[0] == 0x05 && resp[1] == 0x00
}
