package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
)

// ───────────────────────────────────────────────────────
// MOTOR SAĞLIK İZLEME (Engine Monitor)
// ───────────────────────────────────────────────────────

// handleEnginesAPI tüm motorların durumunu JSON olarak döndürür
func (s *Server) handleEnginesAPI(c *gin.Context) {
	stats, err := s.db.GetAllEngineStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "motor istatistikleri alınamadı"})
		return
	}

	upCount, downCount, unknownCount := 0, 0, 0
	totalSuccess, totalFail := 0, 0
	for _, e := range stats {
		switch e.LastStatus {
		case "up":
			upCount++
		case "down":
			downCount++
		default:
			unknownCount++
		}
		totalSuccess += e.SuccessCount
		totalFail += e.FailCount
	}

	c.JSON(http.StatusOK, gin.H{
		"engines":      stats,
		"total":        len(stats),
		"upCount":      upCount,
		"downCount":    downCount,
		"unknownCount": unknownCount,
		"totalSuccess": totalSuccess,
		"totalFail":    totalFail,
	})
}

// handleEngineCheckNow tüm motorları hemen kontrol eder
func (s *Server) handleEngineCheckNow(c *gin.Context) {
	if s.engineMonitor != nil {
		s.engineMonitor.CheckNow()
		logger.Info("ENGINE CHECK: Manuel kontrol başlatıldı")
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "Kontrol başlatıldı, ~30 saniye içinde sonuçlar güncellenir"})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Motor izleyici aktif değil"})
}

// handleEngineToggle motoru aktif/pasif yapar
func (s *Server) handleEngineToggle(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Motor adı gerekli"})
		return
	}
	var req struct {
		Active bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz istek"})
		return
	}
	if err := s.db.SetEngineActive(name, req.Active); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "güncelleme başarısız"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleMonitorSummary dashboard/izleme merkezi özeti
func (s *Server) handleMonitorSummary(c *gin.Context) {
	stats, err := s.db.GetAllEngineStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "özet alınamadı"})
		return
	}
	upCount := 0
	for _, e := range stats {
		if e.LastStatus == "up" {
			upCount++
		}
	}
	searches, _ := s.db.GetAllScheduledSearches()
	activeScheduled := 0
	for _, ss := range searches {
		if ss.Enabled {
			activeScheduled++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"enginesUp":       upCount,
		"enginesTotal":    len(stats),
		"scheduledActive": activeScheduled,
		"scheduledTotal":  len(searches),
	})
}

// ───────────────────────────────────────────────────────
// PLANLI ARAMA (Scheduled Search)
// ───────────────────────────────────────────────────────

const (
	scheduledMinInterval = 5    // dakika
	scheduledMaxInterval = 1440 // 24 saat
	scheduledMaxQueryLen = 200
)

// handleGetScheduledSearches tüm zamanlanmış taramaları JSON olarak döndürür
func (s *Server) handleGetScheduledSearches(c *gin.Context) {
	searches, err := s.db.GetAllScheduledSearches()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "liste alınamadı"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"searches": searches, "count": len(searches)})
}

// handleCreateScheduled yeni zamanlanmış tarama oluşturur
func (s *Server) handleCreateScheduled(c *gin.Context) {
	var req struct {
		Query           string `json:"query" binding:"required"`
		IntervalMinutes int    `json:"intervalMinutes"`
		WebhookURL      string `json:"webhookUrl"`
		AlertThreshold  int    `json:"alertThreshold"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz istek"})
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" || len(req.Query) > scheduledMaxQueryLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Sorgu boş olamaz ve 200 karakteri aşamaz"})
		return
	}
	if req.IntervalMinutes <= 0 {
		req.IntervalMinutes = 60
	}
	if req.IntervalMinutes < scheduledMinInterval {
		req.IntervalMinutes = scheduledMinInterval
	}
	if req.IntervalMinutes > scheduledMaxInterval {
		req.IntervalMinutes = scheduledMaxInterval
	}
	if req.AlertThreshold < 1 || req.AlertThreshold > 5 {
		req.AlertThreshold = 3
	}
	req.WebhookURL = strings.TrimSpace(req.WebhookURL)
	if req.WebhookURL != "" && !isValidWebhookURL(req.WebhookURL) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Webhook yalnızca http(s) ve genel bir adres olabilir"})
		return
	}

	ss, err := s.db.CreateScheduledSearch(req.Query, req.IntervalMinutes, req.WebhookURL, req.AlertThreshold)
	if err != nil {
		logger.Error("Zamanlanmış tarama oluşturulamadı: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Oluşturma hatası"})
		return
	}
	logger.Info("SCHEDULED SEARCH CREATED: ID=%d, Query=%s, Interval=%dm", ss.ID, ss.Query, ss.IntervalMinutes)
	c.JSON(http.StatusOK, gin.H{"success": true, "search": ss})
}

// handleToggleScheduled aktif/pasif durumunu değiştirir
func (s *Server) handleToggleScheduled(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}
	enabled, err := s.db.ToggleScheduledSearch(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "güncelleme başarısız"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "enabled": enabled})
}

// handleDeleteScheduled zamanlanmış taramayı siler
func (s *Server) handleDeleteScheduled(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}
	if err := s.db.DeleteScheduledSearch(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "silme başarısız"})
		return
	}
	logger.Info("SCHEDULED SEARCH DELETED: ID=%d", id)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleRunScheduledNow zamanlanmış taramayı hemen çalıştırır
func (s *Server) handleRunScheduledNow(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}
	if s.scheduler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Scheduler aktif değil"})
		return
	}
	s.scheduler.RunNow(id)
	logger.Info("SCHEDULED SEARCH RUN NOW: ID=%d", id)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Tarama başlatıldı"})
}
