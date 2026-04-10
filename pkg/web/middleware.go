package web

import (
	"crypto/subtle"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
)

// authMiddleware oturum kontrolü
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := c.Cookie("session")
		if err != nil {
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
		if now.After(session.ExpiresAt) {
			_ = s.db.DeleteSession(sessionID)
			clearAuthCookies(c, s.cookieSecure)
			unauthorized(c)
			c.Abort()
			return
		}

		newExpiry := now.Add(s.sessionTTL)
		if err := s.db.TouchSession(sessionID, newExpiry); err != nil {
			logger.Warn("Session touch hatasi: %v", err)
		}

		maxAge := int(s.sessionTTL.Seconds())
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("session", session.ID, maxAge, "/", "", s.cookieSecure, true)
		c.SetCookie("csrf_token", session.CSRFToken, maxAge, "/", "", s.cookieSecure, false)

		c.Set("sessionID", session.ID)
		c.Set("csrfToken", session.CSRFToken)
		c.Set("username", session.Username)

		c.Next()
	}
}

// csrfMiddleware API yazma isteklerinde CSRF token zorunluluğu sağlar.
func (s *Server) csrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if isSafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		expected, ok := c.Get("csrfToken")
		if !ok {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF token bulunamadi"})
			return
		}

		provided := strings.TrimSpace(c.GetHeader("X-CSRF-Token"))
		if provided == "" {
			provided = strings.TrimSpace(c.PostForm("_csrf"))
		}

		expectedToken := expected.(string)
		if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expectedToken)) != 1 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "CSRF dogrulamasi basarisiz"})
			return
		}

		c.Next()
	}
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
