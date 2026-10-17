package web

import (
	"keywordhunter-mvp/pkg/shared"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/notify"
	"keywordhunter-mvp/pkg/search"
)

// ───────────────────────────────────────────────────────
// MOTOR SAĞLIK İZLEME (Engine Monitor)
// ───────────────────────────────────────────────────────

// handleEnginesAPI tüm motorların durumunu JSON olarak döndürür
func (s *Server) handleEnginesAPI(c *gin.Context) {
	stats, err := s.db.GetAllEngineStats()
	if err != nil {
		apiError(c, http.StatusInternalServerError, "engine_stats_failed")
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

	notes := map[string]string{}
	for _, e := range search.SearchEngines {
		if e.Note != "" {
			notes[e.Name] = e.Note
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"engines":      stats,
		"notes":        notes,
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
		s.audit(c, "engine.check_now", "engines", nil, true)
		c.JSON(http.StatusOK, gin.H{"success": true, "message": apiText(c, "engine_check_started")})
		return
	}
	s.audit(c, "engine.check_now", "engines", map[string]any{"error": "monitor_inactive"}, false)
	apiError(c, http.StatusServiceUnavailable, "engine_monitor_inactive")
}

// handleEngineToggle motoru aktif/pasif yapar
func (s *Server) handleEngineToggle(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		apiError(c, http.StatusBadRequest, "engine_name_required")
		return
	}
	var req struct {
		Active bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := s.db.SetEngineActive(name, req.Active); err != nil {
		s.audit(c, "engine.toggle", name, map[string]any{"active": req.Active, "error": "internal"}, false)
		apiError(c, http.StatusInternalServerError, "update_failed")
		return
	}
	s.invalidateEngineCache()
	s.audit(c, "engine.toggle", name, map[string]any{"active": req.Active}, true)
	logger.Info("ENGINE TOGGLE: %s → active=%v", name, req.Active)
	c.JSON(http.StatusOK, gin.H{"success": true, "active": req.Active})
}

// handleMonitorSummary dashboard/izleme merkezi özeti
func (s *Server) handleMonitorSummary(c *gin.Context) {
	stats, err := s.db.GetAllEngineStats()
	if err != nil {
		apiError(c, http.StatusInternalServerError, "summary_failed")
		return
	}
	upCount, activeCount, activeUp := 0, 0, 0
	for _, e := range stats {
		if e.LastStatus == "up" {
			upCount++
		}
		if e.IsActive {
			activeCount++
			if e.LastStatus == "up" {
				activeUp++
			}
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
		"enginesActive":   activeCount,
		"enginesActiveUp": activeUp,
		"schedulerBusy":   s.scheduler != nil && s.scheduler.Busy(),
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
		apiError(c, http.StatusInternalServerError, "list_failed")
		return
	}
	// Tarama-özel webhook adresleri sırdır; admin dışı roller gizlenmiş görür.
	if roleRank(c.GetString("role")) < roleRank("admin") {
		for i := range searches {
			searches[i].WebhookURL = shared.RedactURL(searches[i].WebhookURL)
		}
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
		apiError(c, http.StatusBadRequest, "invalid_request")
		return
	}

	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" || len(req.Query) > scheduledMaxQueryLen {
		apiError(c, http.StatusBadRequest, "scheduled_query_invalid")
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
	if req.WebhookURL != "" && !notify.ValidTarget(req.WebhookURL) {
		apiError(c, http.StatusBadRequest, "webhook_invalid")
		return
	}

	ss, err := s.db.CreateScheduledSearch(req.Query, req.IntervalMinutes, req.WebhookURL, req.AlertThreshold)
	if err != nil {
		logger.Error("Zamanlanmış tarama oluşturulamadı: %v", err)
		apiError(c, http.StatusInternalServerError, "create_failed")
		return
	}
	logger.Info("SCHEDULED SEARCH CREATED: ID=%d, Query=%s, Interval=%dm", ss.ID, ss.Query, ss.IntervalMinutes)
	// Tarama-özel webhook bir sırdır: yalnız ana makinesi kaydedilir.
	details := map[string]any{"scheduledId": ss.ID, "intervalMinutes": ss.IntervalMinutes, "alertThreshold": ss.AlertThreshold}
	if req.WebhookURL != "" {
		details["target"] = shared.RedactURL(req.WebhookURL)
	}
	s.audit(c, "scheduled.create", ss.Query, details, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "search": ss})
}

// handleToggleScheduled aktif/pasif durumunu değiştirir
func (s *Server) handleToggleScheduled(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid_id")
		return
	}
	enabled, err := s.db.ToggleScheduledSearch(id)
	if err != nil {
		s.audit(c, "scheduled.toggle", "scheduled:"+strconv.FormatInt(id, 10), map[string]any{"error": "internal"}, false)
		apiError(c, http.StatusInternalServerError, "update_failed")
		return
	}
	s.audit(c, "scheduled.toggle", "scheduled:"+strconv.FormatInt(id, 10), map[string]any{"enabled": enabled}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "enabled": enabled})
}

// handleDeleteScheduled zamanlanmış taramayı siler
func (s *Server) handleDeleteScheduled(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid_id")
		return
	}
	query := ""
	if ss, err := s.db.GetScheduledSearch(id); err == nil && ss != nil {
		query = ss.Query
	}
	if err := s.db.DeleteScheduledSearch(id); err != nil {
		s.audit(c, "scheduled.delete", "scheduled:"+strconv.FormatInt(id, 10), map[string]any{"error": "internal"}, false)
		apiError(c, http.StatusInternalServerError, "delete_failed")
		return
	}
	s.audit(c, "scheduled.delete", "scheduled:"+strconv.FormatInt(id, 10), map[string]any{"query": query}, true)
	logger.Info("SCHEDULED SEARCH DELETED: ID=%d", id)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleRunScheduledNow zamanlanmış taramayı hemen çalıştırır
func (s *Server) handleRunScheduledNow(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, http.StatusBadRequest, "invalid_id")
		return
	}
	if s.scheduler == nil {
		apiError(c, http.StatusServiceUnavailable, "scheduler_inactive")
		return
	}
	if _, err := s.db.GetScheduledSearch(id); err != nil {
		apiError(c, http.StatusNotFound, "scheduled_not_found")
		return
	}
	if !s.scheduler.RunNow(id) {
		s.audit(c, "scheduled.run_now", "scheduled:"+strconv.FormatInt(id, 10), map[string]any{"error": "busy"}, false)
		apiError(c, http.StatusConflict, "scheduled_busy")
		return
	}
	logger.Info("SCHEDULED SEARCH RUN NOW: ID=%d", id)
	s.audit(c, "scheduled.run_now", "scheduled:"+strconv.FormatInt(id, 10), nil, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": apiText(c, "scan_started")})
}
