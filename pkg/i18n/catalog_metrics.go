package i18n

// Ayarlar → Prometheus metrikleri (salt okunur durum).
func init() {
	register("tr", map[string]string{
		"set.met.title":    "Prometheus metrikleri",
		"set.met.desc":     "GET /metrics: HTTP, arama, motor sağlığı, bulgu, izleme, Tor, denetim zinciri, yedek, giriş ve bildirim metrikleri (Prometheus metin biçimi).",
		"set.met.on":       "Açık",
		"set.met.off":      "Kapalı",
		"set.met.endpoint": "Uç nokta",
		"set.met.auth":     "Kimlik doğrulama",
		"set.met.authVal":  "Admin hesabına ait metrics kapsamlı API token'ı (Bearer). Bu token yalnız /metrics'i kazır, bulgu okuyamaz; read/write token'ları, anonim erişim ve oturum çerezi kabul edilmez.",
		"set.met.offHint":  "Uç kapalı (404). Açmak için .env'e ekleyip yeniden başlatın:",
		"set.met.hint":     "Metrik listesi, örnek prometheus.yml ve Grafana panosu: docs/METRICS.md. Token'ı Hesabım → API token'ları bölümünden Metrik kapsamıyla oluşturun.",
	})
	register("en", map[string]string{
		"set.met.title":    "Prometheus metrics",
		"set.met.desc":     "GET /metrics: HTTP, search, engine health, findings, watchlist, Tor, audit chain, backup, login and notification metrics (Prometheus text format).",
		"set.met.on":       "On",
		"set.met.off":      "Off",
		"set.met.endpoint": "Endpoint",
		"set.met.auth":     "Authentication",
		"set.met.authVal":  "A metrics-scoped API token owned by an admin account (Bearer). It can only scrape /metrics and cannot read findings; read/write tokens, anonymous access and session cookies are not accepted.",
		"set.met.offHint":  "The endpoint is off (404). To enable it, add to .env and restart:",
		"set.met.hint":     "Metric list, sample prometheus.yml and Grafana dashboard: docs/METRICS.md. Create the token under My Account → API tokens with the Metrics scope.",
	})
}
