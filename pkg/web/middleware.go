package web

import (
	"crypto/subtle"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

const (
	// sessionAbsoluteMax kayan (sliding) sürenin ötesinde bir oturumun toplam
	// yaşayabileceği üst sınır: sürekli aktif olsa bile yeniden giriş istenir.
	sessionAbsoluteMax = 30 * 24 * time.Hour
	// sessionTouchInterval her istekte DB'ye yazmamak için dokunma aralığı.
	sessionTouchInterval = time.Minute
)

// authMiddleware oturum kontrolü
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Kimlik doğrulamalı sayfalar/yanıtlar tarayıcı ve ara önbelleklerde saklanmasın
		c.Header("Cache-Control", "no-store")

		sessionID, err := c.Cookie("session")
		if err != nil || sessionID == "" {
			clearAuthCookies(c, s.cookieSecure)
			unauthorized(c)
			c.Abort()
			return
		}

		session, err := s.db.GetSession(sessionID)
		if err != nil {
			if err != sql.ErrNoRows {
				logger.Warn("Session okunamadi: %v", err)
			}
			clearAuthCookies(c, s.cookieSecure)
			unauthorized(c)
			c.Abort()
			return
		}

		now := time.Now()
		if now.After(session.ExpiresAt) || (!session.CreatedAt.IsZero() && now.Sub(session.CreatedAt) > sessionAbsoluteMax) {
			_ = s.db.DeleteSession(sessionID)
			clearAuthCookies(c, s.cookieSecure)
			unauthorized(c)
			c.Abort()
			return
		}

		// Kayan süre: yalnızca son dokunuştan bu yana yeterli zaman geçtiyse yaz
		if now.Sub(session.LastSeenAt) >= sessionTouchInterval {
			newExpiry := now.Add(s.sessionTTL)
			if err := s.db.TouchSession(sessionID, newExpiry); err != nil {
				logger.Warn("Session touch hatasi: %v", err)
			}
			maxAge := int(s.sessionTTL.Seconds())
			c.SetSameSite(http.SameSiteLaxMode)
			c.SetCookie("session", session.ID, maxAge, "/", "", s.cookieSecure, true)
			c.SetCookie("csrf_token", session.CSRFToken, maxAge, "/", "", s.cookieSecure, false)
		}

		c.Set("sessionID", session.ID)
		c.Set("csrfToken", session.CSRFToken)
		c.Set("username", session.Username)

		// Rol her istekte users tablosundan tazelenir (rol/pasif değişikliği anında etkindir).
		role := storage.RoleViewer
		if u, err := s.db.GetUserByUsername(session.Username); err == nil && u != nil {
			if !u.Enabled {
				_ = s.db.DeleteSession(sessionID)
				clearAuthCookies(c, s.cookieSecure)
				unauthorized(c)
				c.Abort()
				return
			}
			role = u.Role
		} else {
			// users tablosunda yoksa (bootstrap edilmemiş eski oturum) admin varsay
			role = storage.RoleAdmin
		}
		c.Set("role", role)

		c.Next()
	}
}

// requireRole verilen minimum rolü şart koşar (admin > analyst > viewer).
// Yazma uçları analyst+, yönetim uçları admin ister; viewer yalnız okur.
func (s *Server) requireRole(min string) gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("role")
		r, _ := role.(string)
		if roleRank(r) < roleRank(min) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Bu işlem için yetkiniz yok (" + min + " rolü gerekir)"})
			return
		}
		c.Next()
	}
}

func roleRank(r string) int {
	switch r {
	case storage.RoleAdmin:
		return 3
	case storage.RoleAnalyst:
		return 2
	case storage.RoleViewer:
		return 1
	default:
		return 0
	}
}

// csrfMiddleware API yazma isteklerinde CSRF token zorunluluğu sağlar.
func (s *Server) csrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}
		if !s.csrfValid(c) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF dogrulamasi basarisiz"})
			return
		}
		c.Next()
	}
}

// csrfValid X-CSRF-Token başlığını veya _csrf form alanını oturumun token'ıyla karşılaştırır.
func (s *Server) csrfValid(c *gin.Context) bool {
	expected, ok := c.Get("csrfToken")
	if !ok {
		return false
	}
	provided := strings.TrimSpace(c.GetHeader("X-CSRF-Token"))
	if provided == "" {
		provided = strings.TrimSpace(c.PostForm("_csrf"))
	}
	expectedToken, _ := expected.(string)
	return provided != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(expectedToken)) == 1
}

// isCrossSiteNavigation Fetch Metadata başlıklarıyla siteler-arası tetiklenen
// istekleri tanır (GET /logout gibi durum değiştiren basit uç noktalar için).
func isCrossSiteNavigation(c *gin.Context) bool {
	site := strings.ToLower(strings.TrimSpace(c.GetHeader("Sec-Fetch-Site")))
	return site == "cross-site"
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func clearAuthCookies(c *gin.Context, secure bool) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("session", "", -1, "/", "", secure, true)
	c.SetCookie("csrf_token", "", -1, "/", "", secure, false)
}

func unauthorized(c *gin.Context) {
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Oturum suresi doldu"})
		return
	}
	c.Redirect(http.StatusFound, "/login")
}
