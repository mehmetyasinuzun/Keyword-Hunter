package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/i18n"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/notify"
	"keywordhunter-mvp/pkg/scheduler"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

// handleIndex ana sayfa: geçerli oturum varsa panele, yoksa girişe yönlendirir.
func (s *Server) handleIndex(c *gin.Context) {
	if sid, err := c.Cookie("session"); err == nil && sid != "" {
		if sess, err := s.db.GetSession(sid); err == nil && time.Now().Before(sess.ExpiresAt) {
			c.Redirect(http.StatusFound, "/dashboard")
			return
		}
	}
	c.Redirect(http.StatusFound, "/login")
}

// brandFaviconSVG yeni marka logosuyla uyumlu favicon (hedef halkası + tarama + düğüm).
const brandFaviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><defs><linearGradient id="g" x1="10" y1="6" x2="54" y2="60" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="#22d3ee"/><stop offset=".55" stop-color="#6366f1"/><stop offset="1" stop-color="#c084fc"/></linearGradient></defs><rect width="64" height="64" rx="15" fill="#070a12"/><path d="M32 8C36 15.5 50.5 23 50.5 38A18.5 18.5 0 0 1 13.5 38C13.5 23 28 15.5 32 8Z" fill="none" stroke="url(#g)" stroke-width="4.4" stroke-linejoin="round"/><path d="M32 27.5v4.5M32 44v4.5M21.5 38H26M38 38h4.5" stroke="url(#g)" stroke-width="3" stroke-linecap="round"/><circle cx="32" cy="38" r="4.4" fill="url(#g)"/></svg>`

// handleI18nCatalog seçili (veya sorgudaki) dilin çeviri kataloğunu döndürür.
func (s *Server) handleI18nCatalog(c *gin.Context) {
	lang := c.Query("lang")
	if lang == "" {
		lang, _ = c.Cookie("lang")
	}
	lang = i18n.Normalize(lang)
	c.Header("Cache-Control", "public, max-age=3600")
	c.JSON(http.StatusOK, gin.H{"lang": lang, "supported": i18n.Supported, "messages": i18n.Catalog(lang)})
}

// handleSetLang dil çerezini ayarlar (1 yıl).
func (s *Server) handleSetLang(c *gin.Context) {
	var req struct {
		Lang string `json:"lang"`
	}
	_ = c.ShouldBindJSON(&req)
	lang := i18n.Normalize(req.Lang)
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("lang", lang, 365*24*3600, "/", "", s.cookieSecure, false)
	c.JSON(http.StatusOK, gin.H{"success": true, "lang": lang})
}

// handleFavicon marka SVG favicon'unu servis eder (tarayıcının /favicon.ico probu için).
func (s *Server) handleFavicon(c *gin.Context) {
	c.Header("Cache-Control", "public, max-age=604800")
	c.Data(http.StatusOK, "image/svg+xml", []byte(brandFaviconSVG))
}

// handleHealthz kimlik doğrulaması gerektirmeyen sağlık ucu (Docker/izleme için).
func (s *Server) handleHealthz(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	dbOK := true
	if _, _, err := s.db.GetStats(); err != nil {
		dbOK = false
	}
	status := http.StatusOK
	state := "ok"
	if !dbOK {
		status = http.StatusServiceUnavailable
		state = "degraded"
	}
	c.JSON(status, gin.H{
		"status":        state,
		"version":       Version,
		"uptimeSeconds": int(time.Since(s.startedAt).Seconds()),
		"db":            dbOK,
		"screenshots":   s.capturer != nil && s.capturer.Available(),
	})
}

// handleLoginPage login sayfası
func (s *Server) handleLoginPage(c *gin.Context) {
	wait, _ := strconv.Atoi(c.Query("wait"))
	if wait < 0 || wait > 3600 {
		wait = 0
	}
	s.render(c, http.StatusOK, "login.html", gin.H{
		"error":   c.Query("error"),
		"message": c.Query("message"),
		"wait":    wait,
	})
}

// handleLogin giriş işlemi
func (s *Server) handleLogin(c *gin.Context) {
	username := strings.TrimSpace(c.PostForm("username"))
	password := c.PostForm("password")
	clientIP := c.ClientIP()
	// Denetim kaydı için denenen kullanıcı adı (kısaltılmış; parola ASLA kaydedilmez)
	attempted := "user:" + shared.TruncateRunes(username, 64)

	// Kilit denetimi her şeyden önce: kilitli IP'nin hiçbir denemesi (aşırı
	// uzun alanlar dahil) dakikada birden fazla denetim satırı üretemez.
	// Deneme doğrulamadan ÖNCE atomik olarak ayrılır (Acquire): eşzamanlı
	// istekler kilidin izin verdiğinden fazla parola denemesi yaptıramaz.
	if s.loginGuard != nil {
		if ok, wait := s.loginGuard.Acquire(clientIP); !ok {
			logger.Warn("USER LOGIN BLOCKED: IP=%s kilitli (%s kaldı)", clientIP, wait.Round(time.Second))
			// Kilitliyken her deneme kaydedilirse saldırgan tabloyu şişirir: IP başına dakikada bir.
			if s.auditNoise.allow("login-locked:"+clientIP, time.Minute) {
				s.audit(c, "auth.login.locked", attempted, map[string]any{"retryAfterSec": int(wait.Seconds()) + 1}, false)
			}
			c.Header("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			c.Redirect(http.StatusFound, "/login?error=locked&wait="+strconv.Itoa(int(wait.Seconds())+1))
			return
		}
		defer s.loginGuard.Release(clientIP)
	}

	if len(username) > 128 || len(password) > 512 {
		// Aşırı uzun alan da başarısız denemedir: kilide sayılır, yoksa kilitsiz
		// sınırsız denetim satırı yazdırılabilirdi.
		if s.loginGuard != nil {
			s.loginGuard.Fail(clientIP)
		}
		s.audit(c, "auth.login.failure", attempted, map[string]any{"reason": "too_long"}, false)
		c.Redirect(http.StatusFound, "/login?error=1")
		return
	}

	authed := false
	role := storage.RoleAdmin // bootstrap kimlik deposu (users tablosu dışı) admin'dir
	reason := "bad_credentials"
	var managed *storage.User
	u, err := s.db.GetUserByUsername(username)
	switch {
	case err == nil && u != nil && u.Enabled:
		authed = verifyBcrypt(u.PasswordHash, password) && subtleEqual(u.Username, username)
		role = u.Role
		managed = u
	case err == nil && u != nil:
		reason = "account_disabled"
		// Pasif hesap: yanıt süresi diğer yollarla aynı kalsın diye bcrypt yine çalışır.
		_ = verifyBcrypt(u.PasswordHash, password)
	case errors.Is(err, sql.ErrNoRows):
		if s.creds != nil {
			// users tablosunda yoksa bootstrap kimlik deposu (eski kurulum)
			authed = s.creds.Verify(username, password)
			if authed && s.twoFactorPolicyActive() {
				// Bu hesap users tablosunda olmadığından 2FA kaydı yapamaz:
				// REQUIRE_2FA etkinken ikinci faktörsüz bir admin girişi olmasın.
				authed = false
				reason = "bootstrap_blocked_by_2fa_policy"
				logger.Warn("Login: .env (bootstrap) hesabı '%s' REQUIRE_2FA=%s etkinken reddedildi; users tablosundaki bir hesapla giriş yapın", shared.TruncateRunes(username, 32), s.require2FA)
			}
		}
	default:
		// DB hatasında bootstrap deposuna DÜŞÜLMEZ: aksi halde geçici bir hata
		// 2FA'lı bir hesabın .env parolasıyla ikinci adımsız girişine yol açabilirdi.
		logger.Error("Login: kullanıcı okunamadı: %v", err)
		c.Redirect(http.StatusFound, "/login?error=1&message=session")
		return
	}
	if authed {
		// 2FA etkinse oturum açılmaz: ikinci adım (parola doğrulandıktan SONRA
		// sorgulanır; giriş sayfası 2FA'nın varlığını parola öncesi sızdırmaz).
		if managed != nil {
			on, err := s.db.TwoFactorEnabled(managed.ID)
			if err != nil {
				logger.Error("Login: 2FA durumu okunamadı: %v", err)
				c.Redirect(http.StatusFound, "/login?error=1&message=session")
				return
			}
			if on {
				s.beginTwoFactorLogin(c, managed)
				return
			}
		}
		if s.startSession(c, username, role, nil) {
			c.Redirect(http.StatusFound, "/dashboard")
		}
		return
	}

	if s.loginGuard != nil {
		s.loginGuard.Fail(clientIP)
	}
	logger.UserLogin(shared.TruncateRunes(username, 32), false, clientIP)
	s.audit(c, "auth.login.failure", attempted, map[string]any{"reason": reason}, false)
	c.Redirect(http.StatusFound, "/login?error=1")
}

// startSession kimliği tamamen doğrulanmış (parola + gerekiyorsa 2FA)
// kullanıcı için oturumu açar: IP sayacını sıfırlar, son girişi işler, oturum
// kaydı + çerezleri (session HttpOnly, csrf_token okunabilir) yazar ve
// auth.login.success olayını bırakır. Normal giriş ve 2FA ikinci adımı AYNI
// bu yolu kullanır. Başarısızsa yönlendirmeyi kendisi yapar ve false döner.
func (s *Server) startSession(c *gin.Context, username, role string, details map[string]any) bool {
	clientIP := c.ClientIP()
	if s.loginGuard != nil {
		s.loginGuard.Success(clientIP)
	}
	_ = s.db.TouchUserLogin(username)
	sessionID := generateSessionID()
	csrfToken := generateSessionID()
	expiresAt := time.Now().Add(s.sessionTTL)
	if err := s.db.CreateSession(sessionID, username, csrfToken, expiresAt); err != nil {
		logger.Error("Session kaydı oluşturulamadı: %v", err)
		s.recordAudit(c, storage.AuditEvent{Action: "auth.login.failure", Target: "user:" + username, Actor: username, Role: role,
			AuthMethod: storage.AuthMethodAnonymous}, map[string]any{"reason": "session_error"})
		c.Redirect(http.StatusFound, "/login?error=1&message=session")
		return false
	}

	logger.UserLogin(username, true, clientIP)
	s.recordAudit(c, storage.AuditEvent{Action: "auth.login.success", Target: "user:" + username, Actor: username, Role: role,
		AuthMethod: storage.AuthMethodSession, Success: true}, details)

	maxAge := int(s.sessionTTL.Seconds())
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", sessionID, maxAge, "/", "", s.cookieSecure, true)
	c.SetCookie("csrf_token", csrfToken, maxAge, "/", "", s.cookieSecure, false)
	return true
}

// handleLogout çıkış işlemi. GET isteklerinde siteler-arası tetiklemeye
// (CSRF ile oturum düşürme) karşı Fetch Metadata kontrolü yapılır; POST'ta
// CSRF token beklenir.
func (s *Server) handleLogout(c *gin.Context) {
	if c.Request.Method == http.MethodGet && isCrossSiteNavigation(c) {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	sessionID, err := c.Cookie("session")
	if err != nil {
		logger.Debug("Logout: session cookie bulunamadı: %v", err)
	} else {
		// Denetim için oturumun sahibi silmeden önce okunur (oturum kimliği kayda girmez).
		if sess, err := s.db.GetSession(sessionID); err == nil && sess != nil {
			role := storage.RoleAdmin
			if u, err := s.db.GetUserByUsername(sess.Username); err == nil && u != nil {
				role = u.Role
			}
			s.recordAudit(c, storage.AuditEvent{Action: "auth.logout", Target: "user:" + sess.Username, Actor: sess.Username, Role: role,
				AuthMethod: storage.AuthMethodSession, Success: true}, nil)
		}
		if err := s.db.DeleteSession(sessionID); err != nil {
			logger.Warn("Logout: session silinemedi: %v", err)
		}
	}

	clearAuthCookies(c, s.cookieSecure)
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
		1: "Seviye 1 (Düşük): Genel forum tartışmaları, haberler ve düşük riskli içerikler.",
		2: "Seviye 2 (Orta): Şüpheli aktiviteler, doğrulanmamış sızıntı iddiaları.",
		3: "Seviye 3 (Yüksek): Doğrulanmış veri sızıntıları, hassas kişisel bilgiler (PII).",
		4: "Seviye 4 (Kritik): Veritabanı sızıntıları, kredi kartı bilgileri, illegal ticaret.",
		5: "Seviye 5 (Acil): Ransomware, 0day exploitler, devlet sırları ve çok yüksek riskli içerikler.",
	}

	// ═══════════════════════════════════════════════════════════════════════
	// ALGORITHM HEALTH METRICS - Kategorizasyon etkinliğini ölç
	// ═══════════════════════════════════════════════════════════════════════

	// 1. Sınıflandırma Oranı: "Genel" dışı kategorilerin yüzdesi
	classifiedCount := 0
	genelCount := 0
	for cat, count := range categoryStats {
		if cat == "Genel" || cat == "" {
			genelCount += count
		} else {
			classifiedCount += count
		}
	}
	classificationRate := 0.0
	if totalResults > 0 {
		classificationRate = float64(classifiedCount) / float64(totalResults) * 100
	}

	// 2. Kritiklik Çeşitliliği: Tüm seviyelerin kullanım dengesi (0-100)
	// Sadece Seviye 1'e yığılma = düşük çeşitlilik
	critDiversity := 0.0
	usedLevels := 0
	for i := 1; i <= 5; i++ {
		if criticalityStats[i] > 0 {
			usedLevels++
		}
	}
	critDiversity = float64(usedLevels) / 5.0 * 100

	// 3. Yüksek Riskli Tespit Oranı: Seviye 3-5 oranı
	highRiskCount := criticalityStats[3] + criticalityStats[4] + criticalityStats[5]
	highRiskRate := 0.0
	if totalResults > 0 {
		highRiskRate = float64(highRiskCount) / float64(totalResults) * 100
	}

	// 4. Genel Sağlık Skoru (0-100)
	// Formül: (%40 sınıflandırma + %30 çeşitlilik + %30 yüksek risk tespiti)
	riskContribution := highRiskRate * 3
	if riskContribution > 30.0 {
		riskContribution = 30.0
	}
	healthScore := (classificationRate * 0.4) + (critDiversity * 0.3) + riskContribution
	if healthScore > 100 {
		healthScore = 100
	}

	// Sağlık durumu metni
	healthStatus := "iyi"
	healthColor := "green"
	if healthScore < 40 {
		healthStatus = "zayıf"
		healthColor = "red"
	} else if healthScore < 70 {
		healthStatus = "orta"
		healthColor = "yellow"
	}

	// Kategori listesi: sayıya göre azalan, yüzde ile (şablonda ilerleme çubuğu)
	type catRow struct {
		Name  string
		Count int
		Pct   int
	}
	cats := make([]catRow, 0, len(categoryStats))
	for name, n := range categoryStats {
		if name == "" {
			name = "Genel"
		}
		cats = append(cats, catRow{Name: name, Count: n, Pct: percent(n, totalResults)})
	}
	sort.Slice(cats, func(i, j int) bool {
		if cats[i].Count != cats[j].Count {
			return cats[i].Count > cats[j].Count
		}
		return cats[i].Name < cats[j].Name
	})
	if len(cats) > 8 {
		cats = cats[:8]
	}
	// Kritiklik seviyeleri: yığılmış çubuk + hücreler için
	type sevRow struct {
		Level, Count, Pct int
		Desc              string
	}
	sevs := make([]sevRow, 0, 5)
	for i := 1; i <= 5; i++ {
		sevs = append(sevs, sevRow{Level: i, Count: criticalityStats[i], Pct: percent(criticalityStats[i], totalResults), Desc: critDescs[i]})
	}

	s.render(c, http.StatusOK, "dashboard.html", gin.H{
		"categoryList":     cats,
		"sevList":          sevs,
		"ActivePage":       "dashboard",
		"totalResults":     totalResults,
		"totalSearches":    totalSearches,
		"totalURLs":        totalResults,
		"recentResults":    recentResults,
		"searchHistory":    searchHistory,
		"categoryStats":    categoryStats,
		"criticalityStats": criticalityStats,
		"critDescs":        critDescs,
		// Algorithm Health Metrics
		"classificationRate": int(classificationRate),
		"classifiedCount":    classifiedCount,
		"genelCount":         genelCount,
		"genelRate":          100 - int(classificationRate),
		"critDiversity":      int(critDiversity),
		"usedLevels":         usedLevels,
		"highRiskCount":      highRiskCount,
		"highRiskRate":       int(highRiskRate),
		"healthScore":        int(healthScore),
		"healthStatus":       healthStatus,
		"healthColor":        healthColor,
	})
}

// percent n/total oranını 0-100 tamsayı yüzdeye çevirir (total 0 ise 0).
func percent(n, total int) int {
	if total <= 0 {
		return 0
	}
	return int(float64(n)/float64(total)*100 + 0.5)
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
	return stats, rows.Err()
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
	return stats, rows.Err()
}

// handleSearchPage arama sayfası
func (s *Server) handleSearchPage(c *gin.Context) {
	// ?q= ile gelen sorgu (komut paleti / derin bağlantı) kutuya önceden doldurulur;
	// arama yine kullanıcının onayıyla (Ara) başlar.
	q := strings.Join(strings.Fields(c.Query("q")), " ")
	if len([]rune(q)) > maxSearchQueryLen {
		q = ""
	}
	s.render(c, http.StatusOK, "search.html", gin.H{
		"ActivePage": "search",
		"query":      q,
	})
}

// (pages parametresi POST /search içinde okunur)

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

// maxSearchQueryLen arama sorgusu için üst sınır (URL ve log güvenliği).
const maxSearchQueryLen = 200

// handleSearch arama işlemi
func (s *Server) handleSearch(c *gin.Context) {
	query := strings.Join(strings.Fields(c.PostForm("query")), " ")
	if query == "" || len([]rune(query)) > maxSearchQueryLen {
		s.render(c, http.StatusBadRequest, "search.html", gin.H{
			"ActivePage": "search",
			"error":      i18n.T(requestLang(c), "search.errQuery"),
		})
		return
	}

	logger.Info("Web'den arama baslatildi: '%s'", query)

	// Arama yap (90sn bütçeli, iptal-edilebilir context)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	knownURLs, err := s.db.GetKnownURLsForQuery(query)
	if err != nil {
		knownURLs = map[string]bool{}
	}

	pages, _ := strconv.Atoi(c.PostForm("pages"))
	if pages < 1 {
		pages = 1
	}
	startTime := time.Now()
	results := s.searcher.SearchAllPages(ctx, query, pages)
	elapsed := time.Since(startTime)

	// Sonuçları kaydet (KeywordHits ile birlikte)
	var storageResults []storage.SearchResult
	var newFindings []notify.Finding
	totalHits := 0
	for _, r := range results {
		if !knownURLs[r.URL] {
			newFindings = append(newFindings, notify.Finding{Title: r.Title, URL: r.URL, Category: r.Category, Criticality: r.Criticality})
		}
		storageResults = append(storageResults, storage.SearchResult{
			Title:        r.Title,
			URL:          r.URL,
			Source:       r.Source,
			Query:        query,
			Criticality:  r.Criticality,
			Category:     r.Category,
			KeywordCount: r.KeywordHits, // Anlık kelime sıklığı
		})
		totalHits += r.KeywordHits
	}

	savedCount, saveErr := s.db.SaveResults(storageResults)
	if saveErr != nil {
		logger.Error("Sonuçlar kaydedilemedi: %v", saveErr)
	}
	s.audit(c, "search.run", query, map[string]any{
		"pages": pages, "found": len(results), "newSaved": savedCount, "durationMs": elapsed.Milliseconds(),
	}, saveErr == nil)
	if err := s.db.SaveSearchHistory(query, len(results)); err != nil {
		logger.Error("Arama geçmişi kaydedilemedi: %v", err)
	}

	// Genel bildirim merkezi: eşik üstü yeni bulgu varsa webhook (asenkron)
	if len(newFindings) > 0 {
		go scheduler.DispatchGlobalAlert(s.db, query, len(results), len(newFindings), newFindings, startTime)
	}

	s.render(c, http.StatusOK, "search.html", gin.H{
		"ActivePage": "search",
		"query":      query,
		"results":    results[:min(20, len(results))],
		"totalFound": len(results),
		"newSaved":   savedCount,
		"totalHits":  totalHits,
		"duration":   elapsed.Round(100 * time.Millisecond).String(),
		"pages":      pages,
		"searched":   true, // GET ?q= ön doldurmasından ayırmak için (boş sonuç durumu)
	})
}

// parseResultFilter sorgu parametrelerinden filtre üretir (HTML sayfası, JSON API ve export ortak).
func parseResultFilter(c *gin.Context) storage.ResultFilter {
	f := storage.ResultFilter{
		Query:      strings.TrimSpace(c.Query("q")),
		Text:       strings.TrimSpace(c.Query("text")),
		Source:     strings.TrimSpace(c.Query("source")),
		Category:   strings.TrimSpace(c.Query("category")),
		Tag:        strings.TrimSpace(c.Query("tag")),
		CaseStatus: strings.TrimSpace(c.Query("case")),
		Sort:       strings.TrimSpace(c.DefaultQuery("sort", "newest")),
	}
	f.MinCriticality, _ = strconv.Atoi(c.DefaultQuery("minCriticality", "1"))
	if f.MinCriticality < 1 || f.MinCriticality > 5 {
		f.MinCriticality = 1
	}
	f.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "50"))
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	f.Offset = (page - 1) * f.Limit
	return f
}

// handleResults kayıtlı sonuçlar (filtre + sayfalama)
func (s *Server) handleResults(c *gin.Context) {
	f := parseResultFilter(c)
	results, total, err := s.db.GetResultsFiltered(f)
	if err != nil {
		logger.Error("Sonuçlar getirilemedi: %v", err)
	}
	totalResults, _, err := s.db.GetStats()
	if err != nil {
		logger.Error("İstatistikler getirilemedi: %v", err)
	}
	sources, categories, _ := s.db.DistinctSourcesAndCategories()

	page := f.Offset/f.Limit + 1
	totalPages := (total + f.Limit - 1) / f.Limit
	if totalPages < 1 {
		totalPages = 1
	}

	// Sayfalama bağlantıları için mevcut filtreyi koru
	qs := c.Request.URL.Query()
	qs.Del("page")
	baseQuery := qs.Encode()
	// Bağlantılar tam URL olarak hazırlanır: html/template, sorgu bölümüne gömülen
	// düz dizeyi "&"/"=" dahil kaçışladığı için (?limit%3d25%26…) filtreler dışa
	// aktarma ve sayfalamada kayboluyordu; özniteliğin başındaki değer yalnızca
	// normalleştirilir.
	withQS := func(path, sep string) string {
		if baseQuery == "" {
			return path
		}
		return path + sep + baseQuery
	}
	pageURL := func(p int) string {
		if baseQuery == "" {
			return "/results?page=" + strconv.Itoa(p)
		}
		return "/results?" + baseQuery + "&page=" + strconv.Itoa(p)
	}

	s.render(c, http.StatusOK, "results.html", gin.H{
		"ActivePage":   "results",
		"results":      results,
		"query":        f.Query,
		"filter":       f,
		"total":        total,
		"totalResults": totalResults,
		"limit":        f.Limit,
		"page":         page,
		"totalPages":   totalPages,
		"hasPrev":      page > 1,
		"hasNext":      page < totalPages,
		"prevPage":     page - 1,
		"nextPage":     page + 1,
		"baseQuery":    baseQuery,
		"exportCSV":    withQS("/api/export/results?format=csv", "&"),
		"exportJSON":   withQS("/api/export/results?format=json", "&"),
		"exportSTIX":   withQS("/api/export/stix", "?"),
		"firstURL":     pageURL(1),
		"prevURL":      pageURL(page - 1),
		"nextURL":      pageURL(page + 1),
		"lastURL":      pageURL(totalPages),
		"rangeFrom":    min(f.Offset+1, total),
		"rangeTo":      min(f.Offset+len(results), total),
		"sources":      sources,
		"categories":   categories,
	})
}

// handleResultsGraph sonuçları graf olarak gösterir
func (s *Server) handleResultsGraph(c *gin.Context) {
	query := c.Query("q")
	s.render(c, http.StatusOK, "results_graph.html", gin.H{
		"ActivePage": "graph",
		"query":      query,
	})
}

// handleAnalytics analitik sayfasini sunar
func (s *Server) handleAnalytics(c *gin.Context) {
	s.render(c, http.StatusOK, "analytics.html", gin.H{
		"ActivePage": "analytics",
	})
}

func (s *Server) handleSettingsPage(c *gin.Context) {
	// Ayarlar yalnız admin içindir: API zaten korunuyordu ama sayfanın kendisi
	// her role açıktı (menüde gizli olsa da URL ile erişilebiliyordu).
	if roleRank(c.GetString("role")) < roleRank("admin") {
		s.renderError(c, http.StatusForbidden)
		return
	}
	s.render(c, http.StatusOK, "settings.html", gin.H{
		"ActivePage": "settings",
	})
}

// handleScheduledPage planlı arama (İzleme Merkezi) sayfası
func (s *Server) handleScheduledPage(c *gin.Context) {
	cfg, _ := s.db.GetAlertConfig()
	searches, err := s.db.GetAllScheduledSearches()
	if err != nil {
		logger.Warn("Planlı taramalar alınamadı: %v", err)
	}
	// Sayfaya gömülen genel webhook adresi bir sırdır; admin dışı roller
	// HTML kaynağında bile tam adresi görmemeli (istemci gizleme yetmez).
	isAdmin := roleRank(c.GetString("role")) >= roleRank("admin")
	if !isAdmin && cfg != nil {
		cp := *cfg
		cp.WebhookURL = shared.RedactURL(cp.WebhookURL)
		cfg = &cp
	}
	s.render(c, http.StatusOK, "scheduled.html", gin.H{
		"ActivePage":  "scheduled",
		"alertConfig": cfg,
		"searches":    searches,
		"isAdmin":     isAdmin,
	})
}

// handleMonitorPage arama motoru sağlık izleme sayfası
func (s *Server) handleMonitorPage(c *gin.Context) {
	s.render(c, http.StatusOK, "monitor.html", gin.H{
		"ActivePage": "monitor",
	})
}

// handleWatchlistPage izleme listesi sayfası
func (s *Server) handleWatchlistPage(c *gin.Context) {
	s.render(c, http.StatusOK, "watchlist.html", gin.H{
		"ActivePage": "watchlist",
		"interval":   int(watchlistInterval().Minutes()),
	})
}
