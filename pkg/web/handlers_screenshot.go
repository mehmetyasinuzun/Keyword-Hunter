package web

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

const captureMaxWait = 90 * time.Second

// handleCaptureNow verilen hedefin ekran görüntüsünü Tor üzerinden hemen alır.
// Senkron çalışır (Tor yavaş; eşzamanlılık capturer içinde sınırlı). Hata olsa bile
// DB'ye 'error' kaydı düşülür — sessiz yutma yok.
func (s *Server) handleCaptureNow(c *gin.Context) {
	if s.capturer == nil || !s.capturer.Available() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Ekran görüntüsü bu ortamda kullanılamıyor (chromium yok)"})
		return
	}

	var req struct {
		TargetURL string `json:"targetUrl" binding:"required"`
		Source    string `json:"source"`
		RefID     int64  `json:"refId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz istek"})
		return
	}
	req.TargetURL = strings.TrimSpace(req.TargetURL)
	if !isCaptureURL(req.TargetURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Hedef yalnızca http(s) adresi olabilir"})
		return
	}
	if req.Source == "" {
		req.Source = "manual"
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), captureMaxWait)
	defer cancel()

	shot, err := s.capturer.Capture(ctx, req.TargetURL)
	if err != nil {
		logger.Warn("Ekran görüntüsü hatası (%s): %v", req.TargetURL, err)
		_, _ = s.db.SaveScreenshot(storage.Screenshot{
			TargetURL: req.TargetURL, Source: req.Source, RefID: req.RefID,
			Status: "error", ErrorMsg: truncateErr(err.Error()),
		})
		c.JSON(http.StatusBadGateway, gin.H{"error": "Ekran görüntüsü alınamadı: " + truncateErr(err.Error())})
		return
	}

	id, dbErr := s.db.SaveScreenshot(storage.Screenshot{
		TargetURL: req.TargetURL, Source: req.Source, RefID: req.RefID,
		FilePath: shot.Path, SHA256: shot.SHA256,
		Width: shot.Width, Height: shot.Height, Bytes: shot.Bytes,
		Status: "ok", Title: shot.Title,
		Challenge: shot.Challenge, ChallengeKind: shot.ChallengeKind,
		TakenAt: shot.TakenAt,
	})
	if dbErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Kayıt başarısız"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"id":            id,
		"file":          shot.Path,
		"sha256":        shot.SHA256,
		"bytes":         shot.Bytes,
		"title":         shot.Title,
		"challenge":     shot.Challenge,
		"challengeKind": shot.ChallengeKind,
		"takenAt":       shot.TakenAt,
	})
}

// handleScreenshotsList son görüntüleri veya bir hedefin zaman çizelgesini döndürür
func (s *Server) handleScreenshotsList(c *gin.Context) {
	target := strings.TrimSpace(c.Query("target"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "60"))

	var (
		shots []storage.Screenshot
		err   error
	)
	if target != "" {
		shots, err = s.db.GetScreenshotsForTarget(target, limit)
	} else {
		shots, err = s.db.GetRecentScreenshots(limit)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "liste alınamadı"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"screenshots": shots, "count": len(shots), "available": s.capturer != nil && s.capturer.Available()})
}

// handleServeScreenshot kaydedilen PNG dosyasını servis eder (path-traversal korumalı)
func (s *Server) handleServeScreenshot(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}
	shot, err := s.db.GetScreenshotByID(id)
	if err != nil || shot.Status != "ok" || shot.FilePath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Görüntü bulunamadı"})
		return
	}
	// Yalnız dizin içindeki dosya adına izin ver (traversal engeli)
	safe := filepath.Base(shot.FilePath)
	full := filepath.Join(s.capturer.OutDir(), safe)
	c.Header("Cache-Control", "private, max-age=86400")
	c.File(full)
}

// isCaptureURL hedef adresin geçerli bir http(s) URL'si olduğunu doğrular
func isCaptureURL(raw string) bool {
	if len(raw) == 0 || len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func truncateErr(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
