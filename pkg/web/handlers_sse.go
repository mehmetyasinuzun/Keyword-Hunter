package web

import (
	"io"
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

	// Yeni bir client kanalı oluştur
	clientChan := make(chan string)

	// Broadcaster'a kaydol
	shared.Streamer.Register(clientChan)

	// Bağlantı kopunca temizle
	defer func() {
		shared.Streamer.Unregister(clientChan)
		logger.Debug("SSE Client disconnected")
	}()

	logger.Debug("SSE Client connected")

	// Client'a veri gönder
	c.Stream(func(w io.Writer) bool {
		// Kanaldan mesaj bekle
		if msg, ok := <-clientChan; ok {
			c.SSEvent("message", msg)
			return true
		}
		return false
	})
}
