package web

import (
	"io"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"

	"github.com/gin-gonic/gin"
)

// handleEvents Server-Sent Events (SSE) endpoint'i
func (s *Server) handleEvents(c *gin.Context) {
	// Header'ları ayarla
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	// Yeni bir client kanalı oluştur (tamponlu - yavaş istemcide mesaj düşmesini azaltır)
	clientChan := make(chan string, 64)

	// Broadcaster'a kaydol
	shared.Streamer.Register(clientChan)

	// Bağlantı kopunca temizle
	defer func() {
		shared.Streamer.Unregister(clientChan)
		logger.Debug("SSE Client disconnected")
	}()

	logger.Debug("SSE Client connected")

	// Periyodik heartbeat - ölü bağlantıları tespit etmeye yardımcı olur
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Client'a veri gönder
	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-clientChan:
			if !ok {
				return false
			}
			c.SSEvent("message", msg)
			return true
		case <-ticker.C:
			c.SSEvent("heartbeat", "ping")
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}
