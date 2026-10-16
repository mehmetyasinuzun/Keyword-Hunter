package web

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── Kişisel API token'ları ───────────────────────────
//
// SOAR/SIEM/betikler /api/ uçlarını "Authorization: Bearer kh_…" ile çağırır.
// Kimlik yolu seçimi ve CSRF kuralı authMiddleware açıklamasındadır. Burada:
// token doğrulama, token ile çağrılamayan uçlar ve token yönetim uçları.

const (
	maxTokenNameRunes      = 64
	maxTokenExpiryDays     = 365
	maxActiveTokensPerUser = 25
	// maxInactiveTokensKept kullanıcı başına saklanan en fazla etkin olmayan
	// (iptal/süresi dolmuş) token satırı; eskileri oluşturmada budanır.
	maxInactiveTokensKept = 50
	// tokenRetention etkin olmayan token satırlarının periyodik temizlikten
	// önce saklandığı süre (geçmiş ayrıca denetim kaydındadır).
	tokenRetention = 90 * 24 * time.Hour
	// tokenTouchInterval son kullanım bilgisini her istekte yazmamak için.
	tokenTouchInterval = time.Minute
)

// tokenForbiddenRoutes API token ile ASLA çağrılamayan hesap/güvenlik yönetimi
// uçları (rota kalıbı kökleri; alt yollar dahil). Sızmış bir token yeni token
// basamamalı, parola/2FA değiştirememeli, kullanıcı yönetememeli, ayar ya da
// entegrasyonları (webhook, site profilleri) değiştirememeli. Yeni bir hesap
// veya güvenlik uç noktası eklendiğinde buraya da eklenmelidir.
var tokenForbiddenRoutes = []string{
	"/api/tokens",
	"/api/me",
	"/api/users",
	"/api/settings",
	"/api/site-profiles",
	"/api/alert-config",
}

// tokenForbiddenRoute rota kalıbının token ile çağrılamayacağını söyler.
func tokenForbiddenRoute(route string) bool {
	for _, p := range tokenForbiddenRoutes {
		if route == p || strings.HasPrefix(route, p+"/") {
			return true
		}
	}
	return false
}

// isAPIRoute eşleşen rota kalıbı /api/ altında mı. Ham yol yerine kalıp
// kullanılır: "/api/../dashboard" gibi yollar bir API rotasıyla eşleşmez.
func isAPIRoute(c *gin.Context) bool {
	return strings.HasPrefix(c.FullPath(), "/api/")
}

// bearerToken Authorization başlığından Bearer token'ı okur (şema büyük/küçük
// harf duyarsız). Sorgu dizgisi ya da form alanı bilerek desteklenmez: URL'ler
// loglara, geçmişe ve Referer'a sızar.
func bearerToken(c *gin.Context) (string, bool) {
	h := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(h) < 7 || !strings.EqualFold(h[:7], "bearer ") {
		return "", false
	}
	tok := strings.TrimSpace(h[7:])
	return tok, tok != ""
}

// authenticateToken Bearer token'ı doğrular, bağlamı kurar ve zinciri sürdürür
// ya da 401/403 ile keser. Yalnız çerezsiz /api/ isteklerinde çağrılır.
func (s *Server) authenticateToken(c *gin.Context, raw string) {
	fail := func(reason, msg string, t *storage.APIToken) {
		// Geçersiz token denemeleri IP + neden başına dakikada bir kaydedilir:
		// rastgele token yağmuru denetim tablosunu şişiremez.
		if s.auditNoise.allow("token-fail:"+c.ClientIP()+":"+reason, time.Minute) {
			details := map[string]any{"reason": reason, "method": c.Request.Method}
			target := c.FullPath()
			if t != nil {
				details["prefix"] = t.Prefix
				details["tokenId"] = t.ID
				target = "user:" + t.Username
			}
			s.recordAudit(c, storage.AuditEvent{Action: "auth.token.failure", Target: target, AuthMethod: storage.AuthMethodAnonymous}, details)
		}
		c.Header("WWW-Authenticate", `Bearer realm="KeywordHunter", error="invalid_token"`)
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": msg})
	}

	if !storage.LooksLikeAPIToken(raw) {
		fail("malformed", "Geçersiz API token", nil)
		return
	}
	hash := storage.HashAPIToken(raw)
	t, err := s.db.GetAPITokenByHash(hash)
	if err != nil {
		if !errors.Is(err, storage.ErrTokenNotFound) {
			logger.Warn("API token okunamadı: %v", err)
		}
		fail("unknown", "Geçersiz API token", nil)
		return
	}
	// Arama özetle yapıldı; sabit-zamanlı karşılaştırma ikinci savunma hattıdır.
	if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(hash)) != 1 {
		fail("unknown", "Geçersiz API token", nil)
		return
	}
	now := time.Now()
	switch t.Status(now) {
	case storage.TokenStatusRevoked:
		fail("revoked", "API token iptal edilmiş", t)
		return
	case storage.TokenStatusExpired:
		fail("expired", "API token süresi dolmuş", t)
		return
	}
	// Sahip hâlâ var, etkin ve AYNI hesap olmalı (silinip aynı adla yeniden
	// açılan hesap eski token'ı devralmaz). Rol her istekte tazelenir.
	u, err := s.db.GetUserByUsername(t.Username)
	if err != nil || u == nil || u.ID != t.UserID || !u.Enabled {
		fail("user_unavailable", "Geçersiz API token", t)
		return
	}
	role := u.Role
	if t.Scope != storage.TokenScopeWrite {
		role = storage.RoleViewer // okuma kapsamı: rol ne olursa olsun yazma yok
	}

	c.Set("username", u.Username)
	c.Set("role", role)
	c.Set("authMethod", storage.AuthMethodToken)
	c.Set("tokenID", t.ID)
	c.Set("tokenPrefix", t.Prefix)
	c.Set("tokenScope", t.Scope)

	if tokenForbiddenRoute(c.FullPath()) {
		s.auditDenied(c, "token_session_required", "")
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Bu uç nokta etkileşimli oturum gerektirir; API token ile çağrılamaz (this endpoint requires an interactive session)"})
		return
	}

	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= tokenTouchInterval {
		if err := s.db.TouchAPIToken(t.ID, c.ClientIP(), now); err != nil {
			logger.Warn("API token touch hatası: %v", err)
		}
	}
	c.Next()
}

// tokenView API yanıtındaki token (hesaplanan durum ile; özet ve düz metin yok).
// Mine sahipliği hesap kimliğiyle (user_id) bildirir: istemci kullanıcı
// adlarını kendi harf katlama kuralıyla karşılaştırmak zorunda kalmaz.
type tokenView struct {
	storage.APIToken
	Status string `json:"status"`
	Mine   bool   `json:"mine"`
}

func viewToken(t storage.APIToken, now time.Time, myID int64) tokenView {
	return tokenView{APIToken: t, Status: t.Status(now), Mine: myID > 0 && t.UserID == myID}
}

// sessionUserID oturum kullanıcısının hesap kimliği (users tablosunda yoksa 0:
// .env bootstrap hesabı). Sahiplik kararları ada göre değil buna göre verilir:
// SQLite NOCASE yalnız ASCII katlar, Go'nun EqualFold'u Unicode katlar; adla
// karşılaştırma "Şule" ile "şule" gibi iki ayrı hesabı aynı sayardı.
func (s *Server) sessionUserID(c *gin.Context) int64 {
	u, err := s.db.GetUserByUsername(c.GetString("username"))
	if err != nil || u == nil {
		return 0
	}
	return u.ID
}

// handleTokensList kendi token'larını döndürür; admin ?all=1 ile herkesinkini.
func (s *Server) handleTokensList(c *gin.Context) {
	me := c.GetString("username")
	role := c.GetString("role")
	all := c.Query("all") == "1"
	if all && role != storage.RoleAdmin {
		s.auditDenied(c, "role", storage.RoleAdmin)
		c.JSON(http.StatusForbidden, gin.H{"error": "Tüm kullanıcıların token'larını yalnız admin görebilir"})
		return
	}
	owner := me
	if all {
		owner = ""
	}
	list, err := s.db.ListAPITokens(owner)
	if err != nil {
		respondInternalError(c, "ListAPITokens", err)
		return
	}
	now := time.Now()
	myID := s.sessionUserID(c)
	out := make([]tokenView, 0, len(list))
	for _, t := range list {
		out = append(out, viewToken(t, now, myID))
	}
	c.JSON(http.StatusOK, gin.H{
		"tokens":   out,
		"me":       me,
		"all":      all,
		"canWrite": roleRank(role) >= roleRank(storage.RoleAnalyst),
		"max":      maxActiveTokensPerUser,
	})
}

// validTokenName ad 1–64 karakter, kontrol karakteri içermez.
func validTokenName(name string) bool {
	n := utf8.RuneCountInString(name)
	if n < 1 || n > maxTokenNameRunes || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// handleTokenCreate yeni token üretir; düz metin yalnız bu yanıtta bir kez döner.
func (s *Server) handleTokenCreate(c *gin.Context) {
	var req struct {
		Name          string `json:"name"`
		Scope         string `json:"scope"`
		ExpiresInDays int    `json:"expiresInDays"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if !validTokenName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Token adı 1-64 karakter olmalı"})
		return
	}
	if !storage.ValidTokenScope(req.Scope) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Kapsam read ya da write olmalı"})
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > maxTokenExpiryDays {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçerlilik süresi 0 (süresiz) ile 365 gün arasında olmalı"})
		return
	}
	me := c.GetString("username")
	u, err := s.db.GetUserByUsername(me)
	if err != nil || u == nil {
		// Bootstrap kimlik deposu (users tablosu dışı) hesabına token bağlanamaz.
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "API token yalnız kullanıcı hesapları için oluşturulabilir"})
		return
	}
	if req.Scope == storage.TokenScopeWrite && roleRank(u.Role) < roleRank(storage.RoleAnalyst) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "İzleyici rolü yalnız okuma (read) kapsamlı token oluşturabilir"})
		return
	}
	now := time.Now()
	var expiresAt *time.Time
	if req.ExpiresInDays > 0 {
		e := now.Add(time.Duration(req.ExpiresInDays) * 24 * time.Hour)
		expiresAt = &e
	}
	// Sınır denetimi ve ekleme tek SQL ifadesidir (eşzamanlı isteklerle aşılamaz).
	plain, t, err := s.db.CreateAPITokenLimited(u.ID, u.Username, name, req.Scope, expiresAt, maxActiveTokensPerUser)
	if errors.Is(err, storage.ErrTokenLimit) {
		s.audit(c, "token.create", "user:"+u.Username, map[string]any{"error": "limit", "max": maxActiveTokensPerUser}, false)
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "En fazla " + strconv.Itoa(maxActiveTokensPerUser) + " etkin token olabilir; kullanılmayanları iptal edin"})
		return
	}
	if err != nil {
		s.audit(c, "token.create", "user:"+u.Username, map[string]any{"error": "internal", "scope": req.Scope}, false)
		respondInternalError(c, "CreateAPIToken", err)
		return
	}
	// Oluştur-iptal et döngüsü tabloyu sınırsız büyütmesin.
	if _, err := s.db.PruneUserInactiveAPITokens(u.ID, maxInactiveTokensKept, now); err != nil {
		logger.Warn("API token budama hatası: %v", err)
	}
	logger.Info("API TOKEN CREATED: %s… (kapsam=%s) — %s", t.Prefix, t.Scope, u.Username)
	s.audit(c, "token.create", "token:"+strconv.FormatInt(t.ID, 10), map[string]any{
		"owner": t.Username, "prefix": t.Prefix, "name": t.Name, "scope": t.Scope, "expiresInDays": req.ExpiresInDays,
	}, true)
	c.JSON(http.StatusCreated, gin.H{"success": true, "token": plain, "meta": viewToken(*t, now, u.ID)})
}

// handleTokenRevoke token'ı iptal eder (sahibi ya da admin).
func (s *Server) handleTokenRevoke(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}
	target := "token:" + strconv.FormatInt(id, 10)
	t, err := s.db.GetAPIToken(id)
	if err != nil {
		if errors.Is(err, storage.ErrTokenNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Token bulunamadı"})
			return
		}
		respondInternalError(c, "GetAPIToken", err)
		return
	}
	me := c.GetString("username")
	// Sahiplik hesap kimliğiyle belirlenir (bkz. sessionUserID), adla değil.
	myID := s.sessionUserID(c)
	owner := myID > 0 && t.UserID == myID
	if !owner && c.GetString("role") != storage.RoleAdmin {
		// Başkasının token'ının varlığı sızdırılmaz: bulunamadı ile aynı yanıt.
		s.audit(c, "token.revoke", target, map[string]any{"error": "not_owner"}, false)
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Token bulunamadı"})
		return
	}
	changed, err := s.db.RevokeAPIToken(id)
	if err != nil {
		s.audit(c, "token.revoke", target, map[string]any{"error": "internal"}, false)
		respondInternalError(c, "RevokeAPIToken", err)
		return
	}
	if !changed {
		c.JSON(http.StatusOK, gin.H{"success": true, "alreadyRevoked": true})
		return
	}
	logger.Info("API TOKEN REVOKED: %s… (sahip=%s) — %s tarafından", t.Prefix, t.Username, me)
	s.audit(c, "token.revoke", target, map[string]any{
		"owner": t.Username, "prefix": t.Prefix, "name": t.Name, "scope": t.Scope, "byAdmin": !owner,
	}, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAccountPage "Hesabım": profil, parola değişikliği, API token'ları.
func (s *Server) handleAccountPage(c *gin.Context) {
	me := c.GetString("username")
	profile := gin.H{"username": me, "managed": false}
	if u, err := s.db.GetUserByUsername(me); err == nil && u != nil {
		profile["username"] = u.Username
		profile["managed"] = true
		profile["createdAt"] = u.CreatedAt
		if u.LastLoginAt != nil {
			profile["lastLoginAt"] = *u.LastLoginAt
		}
	}
	s.render(c, http.StatusOK, "account.html", gin.H{
		"ActivePage": "account",
		"profile":    profile,
		"tokenMax":   maxActiveTokensPerUser,
	})
}
