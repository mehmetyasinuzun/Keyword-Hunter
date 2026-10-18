package web

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── Denetim kaydı (audit log) ───────────────────────────
//
// Durum değiştiren her uç, isteğin sonunda s.audit(...) ile tek satırlık bir
// olay bırakır: kim (aktör + rol + kimlik yöntemi), nereden (IP), ne (noktalı
// eylem kodu + hedef + ayrıntı) ve sonuç. Kayıt hash zincirlidir (bkz.
// storage/audit.go). Kayıt yazılamazsa istek ASLA başarısız olmaz; hata loglanır.
//
// Ayrıntılar (details) sır taşımaz: anahtar adı parola/çerez/token/webhook vb.
// içeren metin değerler "[redacted]" olur, metinler kısaltılır, iç içe yapı
// derinliği sınırlanır. Yine de çağıran taraf yalnız gereken özeti vermelidir
// (ör. ayar değişikliğinde değerler değil yalnız anahtar adları).

const (
	auditChainCacheTTL = 2 * time.Minute
	auditExportMax     = 50000
	auditMaintEvery    = 6 * time.Hour
	auditRedacted      = "[redacted]"
)

// auditSecretKeyParts anahtar adında geçtiğinde metin değeri gizlenen parçalar.
var auditSecretKeyParts = []string{
	"pass", "secret", "token", "cookie", "authorization", "apikey", "api_key",
	"session", "csrf", "hash", "webhook", "credential", "private", "otp",
}

// auditCategories UI rozet rengi ve kategori filtresi için tek kaynak:
// kategori → eylem önekleri. Listede olmayan eylem "data" sayılır.
var auditCategories = []struct {
	Name     string
	Prefixes []string
}{
	{"auth", []string{"auth.", "access."}},
	{"user", []string{"user.", "me.", "token.", "2fa."}},
	{"export", []string{"export.", "taxii.", "misp.push", "backup.download"}},
	{"system", []string{"settings.", "alert.", "engine.", "tor.", "site_profile.", "audit.", "misp.", "backup.", "retention."}},
	{"data", []string{"results.", "result.", "search.", "tagging.", "watchlist.", "scheduled.", "crawl.", "screenshot.", "graph."}},
}

// auditCategory eylem kodunun kategorisini döndürür.
func auditCategory(action string) string {
	for _, c := range auditCategories {
		for _, p := range c.Prefixes {
			if strings.HasPrefix(action, p) {
				return c.Name
			}
		}
	}
	return "data"
}

func auditCategoryPrefixes(name string) []string {
	for _, c := range auditCategories {
		if c.Name == name {
			return c.Prefixes
		}
	}
	return nil
}

// audit isteği yapan oturumun adına bir olay kaydeder.
func (s *Server) audit(c *gin.Context, action, target string, details map[string]any, success bool) {
	s.recordAudit(c, storage.AuditEvent{Action: action, Target: target, Success: success}, details)
}

// auditSystem arka plan işleri (saklama budaması, açılış) için "system" aktörüyle kaydeder.
func (s *Server) auditSystem(action, target string, details map[string]any, success bool) {
	s.recordAudit(nil, storage.AuditEvent{
		Action: action, Target: target, Success: success,
		Actor: "system", AuthMethod: storage.AuthMethodSystem,
	}, details)
}

// recordAudit boş bırakılan aktör/rol/yöntem/IP alanlarını gin bağlamından
// doldurur ve olayı zincire ekler. Hiçbir durumda paniğe ya da istek hatasına
// yol açmaz.
func (s *Server) recordAudit(c *gin.Context, e storage.AuditEvent, details map[string]any) {
	if s == nil || s.db == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Error("AUDIT PANIC (%s): %v", e.Action, r)
		}
	}()
	tokenPrefix, tokenID := "", int64(0)
	if c != nil {
		if e.Actor == "" {
			e.Actor = c.GetString("username")
		}
		if e.Role == "" {
			e.Role = c.GetString("role")
		}
		if e.AuthMethod == "" {
			e.AuthMethod = c.GetString("authMethod")
		}
		if e.IP == "" {
			e.IP = c.ClientIP()
		}
		if e.AuthMethod == storage.AuthMethodToken {
			tokenPrefix, tokenID = c.GetString("tokenPrefix"), c.GetInt64("tokenID")
		}
	}
	if e.AuthMethod == "" {
		e.AuthMethod = storage.AuthMethodAnonymous
		if e.Actor != "" {
			e.AuthMethod = storage.AuthMethodSession
		}
	}
	var clean map[string]any
	if len(details) > 0 {
		clean = sanitizeAuditDetails(details, 0)
	}
	// API token ile yapılan isteklerde hangi token'ın kullanıldığı (yalnız
	// gösterim öneki + kimlik; asla token'ın kendisi) her olaya eklenir.
	// Temizlikten SONRA eklenir: anahtar adı "token" içerdiği için aksi halde
	// gizlenirdi; önek tek başına kimlik doğrulamaz.
	if tokenPrefix != "" {
		if clean == nil {
			clean = make(map[string]any, 2)
		}
		clean["tokenPrefix"] = tokenPrefix
		clean["tokenId"] = tokenID
	}
	if len(clean) > 0 {
		b, err := json.Marshal(clean)
		if err != nil {
			logger.Error("AUDIT DETAILS (%s) serileştirilemedi: %v", e.Action, err)
		} else {
			e.Details = string(b)
		}
	}
	if _, err := s.db.AppendAuditEvent(e); err != nil {
		// Yazılamayan olay yalnız loglanmaz: sayaç /audit sayfasında uyarı olarak
		// görünür (ör. zincir durumu dışarıdan bozulduğu için kalıcı ekleme hatası).
		s.auditWriteFails.Add(1)
		s.auditWriteFailAt.Store(time.Now().UnixMilli())
		logger.Error("AUDIT WRITE FAILED (%s, aktör=%q): %v", e.Action, e.Actor, err)
	}
}

// auditWriteFailures son başlatmadan beri yazılamayan olay sayısı ve son hata zamanı.
func (s *Server) auditWriteFailures() gin.H {
	out := gin.H{"count": s.auditWriteFails.Load()}
	if ms := s.auditWriteFailAt.Load(); ms > 0 {
		out["lastAt"] = time.UnixMilli(ms).UTC().Format(time.RFC3339)
	}
	return out
}

// auditURL URL değerli denetim hedeflerini sırlardan arındırır: kullanıcı
// bilgisi (user:parola@) ve parça atılır, sorgu değerleri "…" olur (anahtar
// adları kalır). Kayıt değiştirilemez ve uzun süre saklanır; bir kez
// yapıştırılan kimlik bilgisi kaynağı silinse bile orada kalmamalıdır.
func auditURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		// Ayrıştırılamayan hedef: sorgu/parçayı ve "...@" kullanıcı bilgisini kaba kes.
		if i := strings.IndexAny(raw, "?#"); i >= 0 {
			raw = raw[:i]
		}
		if i := strings.LastIndex(raw, "@"); i >= 0 {
			pre := ""
			if j := strings.Index(raw, "://"); j >= 0 && j < i {
				pre = raw[:j+3]
			}
			raw = pre + raw[i+1:]
		}
		return raw
	}
	u.User = nil
	u.Fragment, u.RawFragment = "", ""
	q := u.RawQuery
	u.RawQuery, u.ForceQuery = "", false
	out := u.String()
	if q != "" {
		keys := make([]string, 0, 4)
		for _, part := range strings.Split(q, "&") {
			k, _, _ := strings.Cut(part, "=")
			if k == "" {
				continue
			}
			if dk, err := url.QueryUnescape(k); err == nil {
				k = dk
			}
			keys = append(keys, url.QueryEscape(k)+"=…")
			if len(keys) >= 20 {
				break
			}
		}
		if len(keys) > 0 {
			out += "?" + strings.Join(keys, "&")
		}
	}
	return out
}

// isAuditSecretKey anahtar adının sır taşıyabileceğini söyler.
func isAuditSecretKey(k string) bool {
	k = strings.ToLower(k)
	for _, p := range auditSecretKeyParts {
		if strings.Contains(k, p) {
			return true
		}
	}
	return false
}

// sanitizeAuditDetails ayrıntı haritasını güvenli, sınırlı bir kopyaya çevirir.
func sanitizeAuditDetails(in map[string]any, depth int) map[string]any {
	out := make(map[string]any, len(in))
	n := 0
	for k, v := range in {
		if n >= 32 {
			out["_more"] = true
			break
		}
		key := auditText(k, 48)
		out[key] = sanitizeAuditValue(key, v, depth)
		n++
	}
	return out
}

func sanitizeAuditValue(key string, v any, depth int) any {
	secret := isAuditSecretKey(key)
	switch x := v.(type) {
	case nil, bool, int, int32, int64, uint, uint32, uint64, float32, float64:
		return x // sayı/bool sır taşımaz (ör. "passwordChanged": true)
	case string:
		if secret {
			return auditRedacted
		}
		return auditText(x, 256)
	case []string:
		if secret {
			return auditRedacted
		}
		out := make([]string, 0, min(len(x), 50))
		for i, s := range x {
			if i >= 50 {
				out = append(out, "…")
				break
			}
			out = append(out, auditText(s, 128))
		}
		return out
	case []int64:
		if len(x) > 50 {
			return x[:50] // çağıran toplam sayıyı ayrıca verir
		}
		return x
	case []any:
		if secret {
			return auditRedacted
		}
		out := make([]any, 0, min(len(x), 50))
		for i, it := range x {
			if i >= 50 {
				out = append(out, "…")
				break
			}
			out = append(out, sanitizeAuditValue(key, it, depth+1))
		}
		return out
	case map[string]any:
		if secret {
			return auditRedacted
		}
		if depth >= 2 {
			return "…"
		}
		return sanitizeAuditDetails(x, depth+1)
	default:
		if secret {
			return auditRedacted
		}
		return auditText(fmt.Sprint(x), 256)
	}
}

// auditText kontrol karakterlerini atar ve rune sınırına kırpar.
func auditText(s string, maxRunes int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if t := shared.TruncateRunes(s, maxRunes); t != s {
		return t + "…"
	}
	return s
}

// auditThrottle gürültülü olayları (kilitli IP'den tekrar deneme) anahtar
// başına sınırlar; sıfır değeri kullanıma hazırdır.
type auditThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// allow anahtar için son kayıttan bu yana every geçtiyse true döndürür.
func (t *auditThrottle) allow(key string, every time.Duration) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if t.last == nil {
		t.last = make(map[string]time.Time)
	}
	if last, ok := t.last[key]; ok && now.Sub(last) < every {
		return false
	}
	t.last[key] = now
	return true
}

// cleanup eski anahtarları atar (periyodik).
func (t *auditThrottle) cleanup(maxAge time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, at := range t.last {
		if now.Sub(at) > maxAge {
			delete(t.last, k)
		}
	}
}

// auditChain zincir durumunu döndürür. Tam doğrulama tüm tabloyu yürüdüğü
// için en fazla auditChainCacheTTL'de bir yapılır (force ile zorlanır); arada
// yalnız yeni eklenen satırlar önceki geçerli başa bağlanarak doğrulanır.
func (s *Server) auditChain(force bool) (storage.AuditChainStatus, error) {
	s.auditChainMu.Lock()
	defer s.auditChainMu.Unlock()
	var (
		st  storage.AuditChainStatus
		err error
	)
	wasOK := s.auditChainCache == nil || s.auditChainCache.OK
	if !force && s.auditChainCache != nil && time.Since(s.auditChainAt) < auditChainCacheTTL {
		// Son tam doğrulamadan beri eklenen kuyruğu doğrula (ucuz); sayaç
		// "toplam olay" ile tutarlı kalır.
		st, err = s.db.ExtendAuditChainReport(*s.auditChainCache)
		if err != nil {
			return st, err
		}
		s.auditChainCache = &st
	} else {
		if st, err = s.db.AuditChainReport(); err != nil {
			return st, err
		}
		s.auditChainCache = &st
		s.auditChainAt = time.Now() // TTL yalnız tam doğrulamayla yenilenir
	}
	if !st.OK && wasOK { // her listede değil, yalnız kırılma ilk görüldüğünde
		logger.Error("AUDIT CHAIN BROKEN: #%d satırında kırık (%s) — denetim kaydı kurcalanmış olabilir", st.BrokenAt, st.Reason)
	}
	return st, nil
}

// startAuditMaintenance saklama budamasını ve periyodik zincir doğrulamasını
// arka planda çalıştırır (açılışta bir kez, sonra 6 saatte bir).
func (s *Server) startAuditMaintenance() {
	go func() {
		ticker := time.NewTicker(auditMaintEvery)
		defer ticker.Stop()
		s.runAuditMaintenance()
		for {
			select {
			case <-ticker.C:
				s.runAuditMaintenance()
			case <-s.cleanupStop:
				return
			}
		}
	}()
}

func (s *Server) runAuditMaintenance() {
	if s.auditRetentionDays > 0 {
		cutoff := time.Now().Add(-time.Duration(s.auditRetentionDays) * 24 * time.Hour)
		n, err := s.db.PruneAuditEvents(cutoff)
		switch {
		case err != nil:
			logger.Error("Denetim kaydı budanamadı: %v", err)
		case n > 0:
			logger.Info("AUDIT PRUNE: %d olay (> %d gün) silindi; zincir çapası güncellendi", n, s.auditRetentionDays)
			s.auditSystem("audit.prune", "audit_events", map[string]any{"deleted": n, "retentionDays": s.auditRetentionDays}, true)
		}
	}
	if _, err := s.auditChain(true); err != nil {
		logger.Warn("Denetim zinciri doğrulanamadı: %v", err)
	}
}

// ─────────────────────────── Sayfa + API ───────────────────────────

// handleAuditPage denetim kaydı sayfası (yalnız admin). Bütünlük durumu ve
// özet ilk baytta sunucudan gelir; tablo istemcide /api/audit ile dolar.
func (s *Server) handleAuditPage(c *gin.Context) {
	chain, err := s.auditChain(false)
	if err != nil {
		logger.Warn("Denetim zinciri durumu alınamadı: %v", err)
	}
	stats, _ := s.db.GetAuditStats(time.Now())
	s.render(c, http.StatusOK, "audit.html", gin.H{
		"ActivePage":    "audit",
		"chain":         chain,
		"chainErr":      err != nil,
		"stats":         stats,
		"retentionDays": s.auditRetentionDays,
		"writeFails":    s.auditWriteFails.Load(),
	})
}

// parseAuditFilter sorgu parametrelerini doğrulanmış filtreye çevirir.
func parseAuditFilter(c *gin.Context) (storage.AuditFilter, error) {
	var f storage.AuditFilter
	f.Actor = strings.TrimSpace(c.Query("actor"))
	if len(f.Actor) > 128 {
		return f, errCode("audit_actor_too_long")
	}
	if cat := strings.TrimSpace(c.Query("category")); cat != "" {
		f.ActionPrefixes = auditCategoryPrefixes(cat)
		if f.ActionPrefixes == nil {
			return f, errCode("audit_category_invalid")
		}
	}
	if a := strings.ToLower(strings.TrimSpace(c.Query("action"))); a != "" {
		if len(a) > 64 {
			return f, errCode("audit_action_too_long")
		}
		// Kategori + eylem birlikte verilirse eylem daha dar olandır.
		f.ActionPrefixes = []string{a}
	}
	switch c.Query("result") {
	case "":
	case "success":
		v := true
		f.Success = &v
	case "failure":
		v := false
		f.Success = &v
	default:
		return f, errCode("audit_result_invalid")
	}
	var err error
	if f.From, err = parseAuditTime(c.Query("from"), false); err != nil {
		return f, err
	}
	if f.To, err = parseAuditTime(c.Query("to"), true); err != nil {
		return f, err
	}
	f.Text = strings.TrimSpace(c.Query("q"))
	if len([]rune(f.Text)) > 200 {
		return f, errCode("audit_text_too_long")
	}
	return f, nil
}

// parseAuditTime RFC3339 ya da YYYY-MM-DD (UTC) kabul eder. Bitiş tarihi gün
// olarak verilirse o gün dahil edilir (ertesi gün 00:00, hariç).
func parseAuditTime(raw string, end bool) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, errCode("audit_date_invalid", shared.TruncateRunes(raw, 32))
	}
	if end {
		t = t.Add(24 * time.Hour)
	}
	return t, nil
}

// auditEventView API yanıtında olay + kategori.
type auditEventView struct {
	storage.AuditEvent
	Category string `json:"category"`
}

// handleAuditList GET /api/audit — filtreli, sayfalı olaylar + toplam + zincir durumu.
func (s *Server) handleAuditList(c *gin.Context) {
	f, err := parseAuditFilter(c)
	if err != nil {
		apiErrorFrom(c, http.StatusBadRequest, err)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 || page > 100000 {
		page = 1
	}
	f.Limit, f.Offset = limit, (page-1)*limit

	events, total, err := s.db.ListAuditEvents(f)
	if err != nil {
		respondInternalError(c, "ListAuditEvents", err)
		return
	}
	pages := (total + limit - 1) / limit
	if pages < 1 {
		pages = 1
	}
	// Son sayfanın ötesi (eski bağlantı, budanmış veri): son sayfaya sabitle.
	if page > pages {
		page = pages
		f.Offset = (page - 1) * limit
		if events, total, err = s.db.ListAuditEvents(f); err != nil {
			respondInternalError(c, "ListAuditEvents", err)
			return
		}
	}
	views := make([]auditEventView, 0, len(events))
	for _, e := range events {
		views = append(views, auditEventView{AuditEvent: e, Category: auditCategory(e.Action)})
	}
	chain, err := s.auditChain(false)
	if err != nil {
		respondInternalError(c, "AuditChain", err)
		return
	}
	stats, _ := s.db.GetAuditStats(time.Now())
	actors, _ := s.db.DistinctAuditActors(200)
	c.JSON(http.StatusOK, gin.H{
		"events": views, "total": total, "page": page, "limit": limit, "pages": pages,
		"chain": chain, "stats": stats, "actors": actors, "retentionDays": s.auditRetentionDays,
		"writeFailures": s.auditWriteFailures(),
	})
}

// handleAuditVerify POST /api/audit/verify — tam zincir doğrulamasını zorlar.
func (s *Server) handleAuditVerify(c *gin.Context) {
	st, err := s.auditChain(true)
	if err != nil {
		respondInternalError(c, "AuditVerify", err)
		return
	}
	details := map[string]any{"checked": st.Checked, "headId": st.HeadID}
	if !st.OK {
		details["brokenAt"] = st.BrokenAt
		details["reason"] = st.Reason
	}
	s.audit(c, "audit.verify", "audit_events", details, st.OK)
	c.JSON(http.StatusOK, gin.H{"chain": st})
}

// handleAuditExport GET /api/audit/export?format=csv|json — filtreli olayları
// kronolojik sırayla indirir (en yeni 50.000). Dışa aktarmanın kendisi de
// denetlenir. CSV hücreleri formül enjeksiyonuna karşı korunur; zinciri harici
// olarak doğrulamak için birebir değer taşıyan JSON biçimini kullanın.
func (s *Server) handleAuditExport(c *gin.Context) {
	if refuseCrossSiteExport(c, "/audit") {
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "csv")))
	if format != "csv" && format != "json" {
		apiError(c, http.StatusBadRequest, "invalid_export_format")
		return
	}
	f, err := parseAuditFilter(c)
	if err != nil {
		apiErrorFrom(c, http.StatusBadRequest, err)
		return
	}
	total, err := s.db.CountAuditEvents(f)
	if err != nil {
		respondInternalError(c, "CountAuditEvents", err)
		return
	}
	s.audit(c, "export.audit", "audit_events", auditFilterSummary(c, format, min(total, auditExportMax)), true)

	stamp := time.Now().Format("20060102-1504")
	c.Header("Cache-Control", "no-store")
	if format == "json" {
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"keywordhunter-audit-%s.json\"", stamp))
		c.Header("Content-Type", "application/json; charset=utf-8")
		_, _ = c.Writer.Write([]byte("[\n"))
		first := true
		enc := json.NewEncoder(c.Writer)
		err = s.db.EachAuditEvent(f, auditExportMax, func(e storage.AuditEvent) error {
			if !first {
				if _, err := c.Writer.Write([]byte(",")); err != nil {
					return err
				}
			}
			first = false
			return enc.Encode(auditEventView{AuditEvent: e, Category: auditCategory(e.Action)})
		})
		_, _ = c.Writer.Write([]byte("]\n"))
	} else {
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"keywordhunter-audit-%s.csv\"", stamp))
		c.Header("Content-Type", "text/csv; charset=utf-8")
		_, _ = c.Writer.Write([]byte("\xEF\xBB\xBF"))
		w := csv.NewWriter(c.Writer)
		_ = w.Write([]string{"id", "ts", "actor", "role", "auth_method", "ip", "action", "category", "target", "details", "success", "prev_hash", "hash"})
		err = s.db.EachAuditEvent(f, auditExportMax, func(e storage.AuditEvent) error {
			return w.Write([]string{
				strconv.FormatInt(e.ID, 10), e.TS, csvSafe(e.Actor), e.Role,
				e.AuthMethod, csvSafe(e.IP), e.Action, auditCategory(e.Action), csvSafe(e.Target), csvSafe(e.Details),
				strconv.FormatBool(e.Success), e.PrevHash, e.Hash,
			})
		})
		w.Flush()
	}
	if err != nil {
		logger.Error("Denetim dışa aktarma yarıda kaldı: %v", err)
	}
}

// refuseCrossSiteExport başka bir siteden tetiklenen dışa aktarmayı reddeder.
// Dışa aktarma uçları GET'tir ve SameSite=Lax çerez üst düzey gezinmede
// gönderilir: kötü niyetli bir sayfa oturumu açık admini bu adrese yönlendirip
// adına, seçtiği filtre metniyle denetim olayı ürettirebilir ve büyük
// dışa aktarmalar başlatabilir. İstek yazılmadan ve denetlenmeden ilgili
// sayfaya yönlendirilir (çıkış ucundaki Fetch Metadata kontrolüyle aynı).
func refuseCrossSiteExport(c *gin.Context, page string) bool {
	if !isCrossSiteNavigation(c) {
		return false
	}
	logger.Warn("CROSS-SITE EXPORT REFUSED: %s (kullanıcı=%s)", c.FullPath(), c.GetString("username"))
	c.Redirect(http.StatusSeeOther, page)
	c.Abort()
	return true
}

// auditFilterSummary dışa aktarma olayına yazılacak filtre özeti (yalnız dolu alanlar).
func auditFilterSummary(c *gin.Context, format string, rows int) map[string]any {
	d := map[string]any{"format": format, "rows": rows}
	for _, k := range []string{"actor", "category", "action", "result", "from", "to", "q"} {
		if v := strings.TrimSpace(c.Query(k)); v != "" {
			d[k] = v
		}
	}
	return d
}

// resultFilterSummary bulgu dışa aktarma olayları için filtre özeti.
func resultFilterSummary(f storage.ResultFilter, rows int) map[string]any {
	d := map[string]any{"rows": rows}
	add := func(k, v string) {
		if v != "" {
			d[k] = v
		}
	}
	add("q", f.Query)
	add("text", f.Text)
	add("source", f.Source)
	add("category", f.Category)
	add("tag", f.Tag)
	add("case", f.CaseStatus)
	if f.MinCriticality > 1 {
		d["minCriticality"] = f.MinCriticality
	}
	return d
}
