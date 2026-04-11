package web

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

// ═══════════════════════════════════════════════════════
// ENGINE MONITOR HANDLERS
// ═══════════════════════════════════════════════════════

// handleMonitorPage engine monitoring sayfası
func (s *Server) handleMonitorPage(c *gin.Context) {
	c.HTML(http.StatusOK, "monitor.html", gin.H{
		"ActivePage": "monitor",
	})
}

// handleEnginesAPI tüm motorların durumunu JSON olarak döndürür
func (s *Server) handleEnginesAPI(c *gin.Context) {
	stats, err := s.db.GetAllEngineStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Özet istatistikler hesapla
	upCount := 0
	downCount := 0
	unknownCount := 0
	totalSuccess := 0
	totalFail := 0

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
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Kontrol başlatıldı, ~30 saniye içinde sonuçlar güncellenir"})
}

// handleEngineToggle motoru aktif/pasif yapar
func (s *Server) handleEngineToggle(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		Active bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz istek"})
		return
	}
	if err := s.db.SetEngineActive(name, req.Active); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ═══════════════════════════════════════════════════════
// SCHEDULED SEARCH HANDLERS
// ═══════════════════════════════════════════════════════

// handleScheduledPage zamanlanmış taramalar sayfası
func (s *Server) handleScheduledPage(c *gin.Context) {
	searches, err := s.db.GetAllScheduledSearches()
	if err != nil {
		logger.Error("Zamanlanmış taramalar alınamadı: %v", err)
	}
	c.HTML(http.StatusOK, "scheduled.html", gin.H{
		"ActivePage": "scheduled",
		"searches":   searches,
	})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz istek: " + err.Error()})
		return
	}

	// Varsayılan değerler
	if req.IntervalMinutes <= 0 {
		req.IntervalMinutes = 60
	}
	if req.AlertThreshold <= 0 {
		req.AlertThreshold = 3
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
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}

	enabled, err := s.db.ToggleScheduledSearch(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	status := "durduruldu"
	if enabled {
		status = "aktif edildi"
	}
	logger.Info("SCHEDULED SEARCH TOGGLE: ID=%d → %s", id, status)
	c.JSON(http.StatusOK, gin.H{"success": true, "enabled": enabled})
}

// handleDeleteScheduled zamanlanmış taramayı siler
func (s *Server) handleDeleteScheduled(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}

	if err := s.db.DeleteScheduledSearch(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("SCHEDULED SEARCH DELETED: ID=%d", id)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleRunScheduledNow zamanlanmış taramayı hemen çalıştırır
func (s *Server) handleRunScheduledNow(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}

	if s.scheduler != nil {
		s.scheduler.RunNow(id)
		logger.Info("SCHEDULED SEARCH RUN NOW: ID=%d", id)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "Tarama başlatıldı"})
	} else {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Scheduler aktif değil"})
	}
}

// handleGetScheduledSearches tüm zamanlanmış taramaları JSON olarak döndürür
func (s *Server) handleGetScheduledSearches(c *gin.Context) {
	searches, err := s.db.GetAllScheduledSearches()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"searches": searches, "count": len(searches)})
}

// ═══════════════════════════════════════════════════════
// ALERT CONFIG HANDLERS
// ═══════════════════════════════════════════════════════

// handleGetAlertConfig bildirim ayarlarını döndürür
func (s *Server) handleGetAlertConfig(c *gin.Context) {
	config, err := s.db.GetAllAlertConfig()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"config": config})
}

// handleSaveAlertConfig bildirim ayarlarını kaydeder
func (s *Server) handleSaveAlertConfig(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz istek"})
		return
	}

	allowed := map[string]bool{
		"global_webhook_url":     true,
		"global_alert_threshold": true,
		"notify_on_new_only":     true,
	}

	for k, v := range req {
		if !allowed[k] {
			continue
		}
		if err := s.db.SetAlertConfig(k, v); err != nil {
			logger.Warn("Alert config kayıt hatası (%s): %v", k, err)
		}
	}

	logger.Info("ALERT CONFIG UPDATED")
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ═══════════════════════════════════════════════════════
// MONITOR SUMMARY (Dashboard widget için)
// ═══════════════════════════════════════════════════════

// handleMonitorSummary dashboard widget için özet
func (s *Server) handleMonitorSummary(c *gin.Context) {
	stats, err := s.db.GetAllEngineStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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

// handleNewResultsAPI "is_new" olarak işaretlenmiş son bulgular (dashboard için)
func (s *Server) handleNewResultsAPI(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", "20")
	limit, _ := strconv.Atoi(limitStr)
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	rows, err := s.db.GetDBConn().Query(`
		SELECT id, title, url, source, query, criticality, category, keyword_count, COALESCE(auto_tags,''), created_at
		FROM search_results
		WHERE is_new = 1
		ORDER BY created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var results []storage.SearchResult
	for rows.Next() {
		var r storage.SearchResult
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.AutoTags, &r.CreatedAt); err == nil {
			results = append(results, r)
		}
	}

	c.JSON(http.StatusOK, gin.H{"results": results, "count": len(results)})
}
