// Package metrics KeywordHunter'ın Prometheus metriklerini tanımlar.
//
// Olay sayaçları (HTTP istekleri, aramalar, giriş denemeleri, bildirimler…)
// süreç genelinde tektir: onları artıran paketler (web, scheduler, storage,
// notify) buradaki yardımcıları çağırır. Kayıt defteri (registry) ise
// sunucuya özeldir ve yalnız METRICS_ENABLED=true iken kurulur (NewRegistry):
// varsayılan küresel prometheus kayıt defteri KULLANILMAZ, böylece bir
// bağımlılığın kendi kendine kaydettiği metrik /metrics'e sızmaz.
//
// Kardinalite kuralı: etiket değerleri yalnız bu dosyadaki sabit kümelerden
// gelir; bilinmeyen değer "other" olur. Sorgu, URL, kullanıcı adı, IP gibi
// yüksek kardinaliteli ya da hassas değerler ASLA etiket olmaz.
package metrics

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Namespace tüm metrik adlarının öneki (keywordhunter_…).
const Namespace = "keywordhunter"

// Etiket değerleri (sabit kümeler).
const (
	TriggerManual    = "manual"
	TriggerScheduled = "scheduled"

	ResultSuccess   = "success"
	ResultFailure   = "failure"
	ResultError     = "error"
	ResultTimeout   = "timeout"
	ResultLocked    = "locked"
	ResultChallenge = "challenge" // parola doğru, ikinci adım (2FA) bekleniyor
	ResultCooldown  = "cooldown"
	// ResultEnginesFailed zamanlanmış taramada hiçbir motor yanıt vermedi
	// (etkin motor yok ya da hepsi hata verdi, ör. Tor kapalı).
	ResultEnginesFailed = "engines_failed"

	WatchUp      = "up"
	WatchDown    = "down"
	WatchBlocked = "blocked"
	WatchChanged = "changed"

	TorReasonManual         = "manual"
	TorReasonAllEnginesDown = "all_engines_down"

	ChannelWebhook = "webhook"

	LoginPassword     = "password"
	LoginTOTP         = "totp"
	LoginRecoveryCode = "recovery_code"

	other = "other"
)

// RetentionKinds retention_purged_total'ın "kind" değerleri.
var RetentionKinds = []string{
	"results", "tags", "artifacts", "misp_pushed", "graph_nodes",
	"screenshots", "screenshot_files", "audit_events",
}

var (
	searchTriggers   = []string{TriggerManual, TriggerScheduled}
	scheduledResults = []string{ResultSuccess, ResultEnginesFailed, ResultError, ResultTimeout}
	watchResults     = []string{WatchUp, WatchDown, WatchBlocked, WatchChanged}
	torReasons       = []string{TorReasonManual, TorReasonAllEnginesDown}
	torResults       = []string{ResultSuccess, ResultCooldown, ResultFailure}
	notifyResults    = []string{ResultSuccess, ResultFailure}
	// notifyChannels izin verilen kanallar; yalnız mevcut olanlar (webhook)
	// sıfırla başlatılır, diğerleri ilk gönderimde görünür.
	notifyChannels = []string{ChannelWebhook, "teams", "email", "telegram"}
	loginMethods   = []string{LoginPassword, LoginTOTP, LoginRecoveryCode, "oidc"}
	loginResults   = []string{ResultSuccess, ResultFailure, ResultLocked, ResultChallenge, ResultError}
	httpMethods    = []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions,
	}
)

// Kova sınırları (saniye). HTTP: tipik uçlar milisaniye, eşzamanlı Tor araması
// (POST /search) 90 sn'ye, yedek indirme dakikalara uzar.
var (
	httpBuckets   = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}
	searchBuckets = []float64{1, 2.5, 5, 10, 20, 30, 45, 60, 90, 120, 180, 240, 300}
)

var (
	httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "http_requests_total",
		Help: "HTTP requests by method, Gin route template ('unmatched' when no route matched) and status class.",
	}, []string{"method", "route", "status_class"})
	httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Name: "http_request_duration_seconds",
		Help:    "HTTP request latency by method and Gin route template (the long-lived SSE stream /events is not observed).",
		Buckets: httpBuckets,
	}, []string{"method", "route"})

	searchRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "search_runs_total",
		Help: "Completed keyword searches by trigger (manual or scheduled).",
	}, []string{"trigger"})
	searchDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: Namespace, Name: "search_duration_seconds",
		Help:    "Keyword search duration (all engines over Tor) by trigger.",
		Buckets: searchBuckets,
	}, []string{"trigger"})
	newFindings = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "new_findings_total",
		Help: "Findings stored for the first time (any source: search, scheduled scan, crawl) by severity 1-5.",
	}, []string{"severity"})
	scheduledRuns = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "scheduled_scan_runs_total",
		Help: "Scheduled scan runs by result (success: at least one engine answered and results were saved; engines_failed: no engine answered; error: results could not be saved; timeout).",
	}, []string{"result"})
	watchlistChecks = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "watchlist_checks_total",
		Help: "Watchlist site checks by result (up, changed, blocked, down).",
	}, []string{"result"})
	torRenewals = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "tor_circuit_renewals_total",
		Help: "Tor circuit renewal (NEWNYM) attempts by reason and result.",
	}, []string{"reason", "result"})
	retentionPurged = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "retention_purged_total",
		Help: "Rows/files deleted by data retention and audit-log pruning, by kind.",
	}, []string{"kind"})
	notificationSends = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "notification_sends_total",
		Help: "Outgoing alert notifications by channel and result.",
	}, []string{"channel", "result"})
	loginAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: Namespace, Name: "login_attempts_total",
		Help: "Interactive login attempts by method (password, totp, recovery_code) and result.",
	}, []string{"method", "result"})
)

// eventCollectors süreç geneli olay metrikleri (her kayıt defterine eklenir).
var eventCollectors = []prometheus.Collector{
	httpRequests, httpDuration, searchRuns, searchDuration, newFindings,
	scheduledRuns, watchlistChecks, torRenewals, retentionPurged,
	notificationSends, loginAttempts,
}

func init() {
	// Bilinen etiket birleşimleri sıfırla başlatılır: seri ilk olaydan önce de
	// vardır (rate()/increase() ve "hiç olmadı" alarmları doğru çalışır).
	for _, t := range searchTriggers {
		searchRuns.WithLabelValues(t)
		searchDuration.WithLabelValues(t)
	}
	for sev := 1; sev <= 5; sev++ {
		newFindings.WithLabelValues(strconv.Itoa(sev))
	}
	for _, r := range scheduledResults {
		scheduledRuns.WithLabelValues(r)
	}
	for _, r := range watchResults {
		watchlistChecks.WithLabelValues(r)
	}
	for _, reason := range torReasons {
		for _, r := range torResults {
			torRenewals.WithLabelValues(reason, r)
		}
	}
	for _, k := range RetentionKinds {
		retentionPurged.WithLabelValues(k)
	}
	for _, r := range notifyResults {
		notificationSends.WithLabelValues(ChannelWebhook, r)
	}
	// "oidc" henüz yok: ilk girişte görünür.
	for _, m := range []string{LoginPassword, LoginTOTP, LoginRecoveryCode} {
		for _, r := range loginResults {
			loginAttempts.WithLabelValues(m, r)
		}
	}
}

// NewRegistry sunucuya özel kayıt defterini kurar: Go çalışma zamanı ve süreç
// toplayıcıları, build_info, olay metrikleri ve verilen ek toplayıcılar
// (ör. veritabanından türetilen önbellekli göstergeler).
func NewRegistry(version string, extra ...prometheus.Collector) (*prometheus.Registry, error) {
	reg := prometheus.NewRegistry()
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: Namespace, Name: "build_info",
		Help: "Always 1; labels carry the KeywordHunter version and the Go toolchain it was built with.",
	}, []string{"version", "goversion"})
	buildInfo.WithLabelValues(version, runtime.Version()).Set(1)

	cs := []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
	}
	cs = append(cs, eventCollectors...)
	cs = append(cs, extra...)
	for _, c := range cs {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// oneOf v izin verilen kümedeyse kendisini, değilse "other" döndürür.
func oneOf(v string, allowed []string) string {
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return other
}

// HTTPMethod istek yöntemini sabit kümeye indirger: istemci keyfi yöntem
// adlarıyla (ör. "FOO1", "FOO2"…) seri patlatamaz.
func HTTPMethod(m string) string {
	if v := oneOf(m, httpMethods); v != other {
		return v
	}
	return "OTHER"
}

// StatusClass HTTP durum kodunun sınıfı ("2xx" … "5xx"; aralık dışı "other").
func StatusClass(code int) string {
	if code < 100 || code > 599 {
		return other
	}
	return strconv.Itoa(code/100) + "xx"
}

// Severity kritiklik değerini 1-5 aralığına sıkıştırıp etiket yapar.
func Severity(crit int) string {
	switch {
	case crit < 1:
		crit = 1
	case crit > 5:
		crit = 5
	}
	return strconv.Itoa(crit)
}

// ObserveHTTP tamamlanan bir HTTP isteğini kaydeder. route Gin rota kalıbıdır
// (FullPath; ham yol ASLA) — boşsa "unmatched". observeLatency false ise
// (uzun ömürlü akışlar) yalnız sayaç artar.
func ObserveHTTP(method, route string, status int, d time.Duration, observeLatency bool) {
	if route == "" {
		route = "unmatched"
	}
	m := HTTPMethod(method)
	httpRequests.WithLabelValues(m, route, StatusClass(status)).Inc()
	if observeLatency {
		httpDuration.WithLabelValues(m, route).Observe(d.Seconds())
	}
}

// SearchRun tamamlanan bir arama çalışmasını (tetikleyiciye göre) kaydeder.
func SearchRun(trigger string, d time.Duration) {
	t := oneOf(trigger, searchTriggers)
	searchRuns.WithLabelValues(t).Inc()
	searchDuration.WithLabelValues(t).Observe(d.Seconds())
}

// NewFinding ilk kez kaydedilen bulguyu kritikliğine göre sayar.
func NewFinding(criticality int) {
	newFindings.WithLabelValues(Severity(criticality)).Inc()
}

// ScheduledRun zamanlanmış taramanın sonucunu sayar.
func ScheduledRun(result string) {
	scheduledRuns.WithLabelValues(oneOf(result, scheduledResults)).Inc()
}

// WatchlistCheck izleme kontrolünü sonucuna göre sayar. status depolanan
// durumdur ("up", "down", "engel"); changed içerik değişimidir.
func WatchlistCheck(status string, changed bool) {
	r := WatchDown
	switch status {
	case "up":
		r = WatchUp
		if changed {
			r = WatchChanged
		}
	case "engel":
		r = WatchBlocked
	}
	watchlistChecks.WithLabelValues(r).Inc()
}

// TorRenewal Tor devre yenileme denemesini nedeni ve sonucuyla sayar.
func TorRenewal(reason, result string) {
	torRenewals.WithLabelValues(oneOf(reason, torReasons), oneOf(result, torResults)).Inc()
}

// RetentionPurged saklama temizliğinin sildiği satır/dosya sayısını ekler.
func RetentionPurged(kind string, n int64) {
	if n <= 0 {
		return
	}
	retentionPurged.WithLabelValues(oneOf(kind, RetentionKinds)).Add(float64(n))
}

// NotificationSent giden bildirimi kanal ve sonuca göre sayar.
func NotificationSent(channel string, ok bool) {
	r := ResultFailure
	if ok {
		r = ResultSuccess
	}
	notificationSends.WithLabelValues(oneOf(channel, notifyChannels), r).Inc()
}

// LoginAttempt etkileşimli giriş denemesini yöntem ve sonuca göre sayar.
func LoginAttempt(method, result string) {
	loginAttempts.WithLabelValues(oneOf(method, loginMethods), oneOf(result, loginResults)).Inc()
}
