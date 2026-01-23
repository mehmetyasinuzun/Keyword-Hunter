package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// authMiddleware oturum kontrolü
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID, err := c.Cookie("session")
		if err != nil {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		s.mu.RLock()
		expiry, exists := s.sessions[sessionID]
		s.mu.RUnlock()

		if !exists || time.Now().After(expiry) {
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}

		c.Next()
	}
}
