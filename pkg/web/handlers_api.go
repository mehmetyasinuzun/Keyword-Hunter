package web

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/config"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/notify"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/tagging"
)

// autoTagMaxWait tekil etiketleme için üst süre (Tor yavaş; ama sonsuz değil).
const autoTagMaxWait = 100 * time.Second

// respondInternalError istemciye generic mesaj döndürür, gerçek hatayı loglar (bilgi sızıntısı önlenir)
func respondInternalError(c *gin.Context, op string, err error) {
	logger.Error("%s hatası: %v", op, err)
	apiError(c, http.StatusInternalServerError, "internal")
}

// ExpandRequest expand isteği
type ExpandRequest struct {
	URL      string `json:"url" binding:"required"`
	ParentID int64  `json:"parentId"`
	Query    string `json:"query"`
}

// AutoTagRequest tekil etiketleme isteği
type AutoTagRequest struct {
	ID int64 `json:"id" binding:"required"`
}

// BatchAutoTagRequest toplu etiketleme isteği
type BatchAutoTagRequest struct {
	ResultIDs []int64 `json:"resultIds" binding:"required"`
	Query     string  `json:"query"`
}

type envSettingsPayload struct {
	AdminUser      string  `json:"adminUser"`
	AdminPass      string  `json:"adminPass"`
	TorProxy       string  `json:"torProxy"`
	DBPath         string  `json:"dbPath"`
	WebAddr        string  `json:"webAddr"`
	LogDir         string  `json:"logDir"`
	SecureCookies  bool    `json:"secureCookies"`
	SessionTTL     int     `json:"sessionTtlHours"`
	RateLimitRPS   float64 `json:"rateLimitRps"`
	RateLimitBurst int     `json:"rateLimitBurst"`
}

type GraphQuerySummary struct {
	Query string `json:"query"`
	Count int    `json:"count"`
}

type GraphEngineSummary struct {
	Engine string `json:"engine"`
	Count  int    `json:"count"`
}

type GraphResultSummary struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	SourceCount int    `json:"sourceCount"`
	IsExpanded  bool   `json:"isExpanded"`
	Domain      string `json:"domain"`
}

// handleUpdateCriticality kritiklik ve kategori güncelleme
func (s *Server) handleUpdateCriticality(c *gin.Context) {
	var req struct {
		ID          int64  `json:"id"`
		Type        string `json:"type"` // "result" or "content"
		Criticality int    `json:"criticality"`
		Category    string `json:"category"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "invalid_request")
		return
	}

	if req.Type == "" {
		req.Type = "result"
	}
	if req.Type != "result" {
		apiError(c, http.StatusBadRequest, "invalid_type")
		return
	}

	if req.Criticality < 1 || req.Criticality > 5 {
		apiError(c, http.StatusBadRequest, "criticality_range")
		return
	}
	req.Category = strings.TrimSpace(req.Category)
	if len(req.Category) > 64 {
		apiError(c, http.StatusBadRequest, "category_too_long")
		return
	}

	if req.Category == "" {
		req.Category = "Genel"
	}
	// Kritiklik yükselirse bulgu TAXII akışına yeniden girer (bkz. storage.UpdateCriticality).
	found, err := s.db.UpdateCriticality(req.ID, req.Criticality, req.Category)
	if err != nil {
		respondInternalError(c, "UpdateCriticality", err)
		return
	}
	if !found {
		apiError(c, http.StatusNotFound, "record_not_found")
		return
	}

	logger.Info("CRITICALITY UPDATE: ID=%d, Type=%s, Crit=%d, Cat=%s", req.ID, req.Type, req.Criticality, req.Category)
	s.audit(c, "result.criticality.update", "result:"+strconv.FormatInt(req.ID, 10), map[string]any{"criticality": req.Criticality, "category": req.Category}, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAutoTag tek bir sonucu etiketler
func (s *Server) handleAutoTag(c *gin.Context) {
	var req AutoTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}

	// Ölü bir .onion 3 deneme × 60 sn ile handler'ı dakikalarca tutuyordu; üst sınır koy.
	ctx, cancel := context.WithTimeout(c.Request.Context(), autoTagMaxWait)
	defer cancel()

	result, err := s.tagEngine.TagResultByID(ctx, req.ID)
	if err != nil {
		logger.Warn("AUTO TAG FAILED: ID=%d, Error=%v", req.ID, err)
		s.audit(c, "tagging.run", "result:"+strconv.FormatInt(req.ID, 10), map[string]any{"error": tagErrKind(ctx, err)}, false)
		switch {
		case errors.Is(err, tagging.ErrResultNotFound):
			apiFail(c, http.StatusNotFound, "result_not_found")
		case errors.Is(err, tagging.ErrNoTaggableSignal):
			apiFail(c, http.StatusUnprocessableEntity, "no_taggable_signal")
		case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
			apiFail(c, http.StatusGatewayTimeout, "site_timeout")
		case errors.Is(err, tagging.ErrFetchFailed):
			body := fetchErrBody(c, "tag_fetch_failed", err, tagging.ErrFetchFailed)
			body["success"] = false
			c.JSON(http.StatusBadGateway, body)
		default:
			logger.Error("AutoTag hatası: ID=%d, err=%v", req.ID, err)
			apiFail(c, http.StatusInternalServerError, "tagging_failed")
		}
		return
	}

	logger.Info("AUTO TAG SUCCESS: ID=%d, Tags=%s, Hits=%d", req.ID, result.TagsStr, result.KeywordHits)
	s.audit(c, "tagging.run", "result:"+strconv.FormatInt(req.ID, 10), map[string]any{"tags": len(result.Tags), "criticality": result.Criticality}, true)
	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"id":          result.ResultID,
		"tags":        result.Tags,
		"tagsStr":     result.TagsStr,
		"keywordHits": result.KeywordHits,
		"category":    result.Category,
		"criticality": result.Criticality,
		"confidence":  result.Confidence,
		"artifacts":   result.Artifacts,
	})
}

// handleBatchAutoTag toplu etiketleme işini kuyruğa alır
func (s *Server) handleBatchAutoTag(c *gin.Context) {
	var req BatchAutoTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}

	if len(req.ResultIDs) == 0 {
		apiFail(c, http.StatusBadRequest, "no_results_to_tag")
		return
	}

	job, err := s.batchRunner.Submit(c.Request.Context(), req.ResultIDs, req.Query)
	if err != nil {
		logger.Warn("BATCH AUTO TAG SUBMIT FAILED: %v", err)
		kind := "internal"
		if errors.Is(err, tagging.ErrNoValidResultIDs) {
			kind = "no_valid_ids"
		} else if errors.Is(err, tagging.ErrQueueFull) {
			kind = "queue_full"
		}
		s.audit(c, "tagging.batch.submit", req.Query, map[string]any{"requested": len(req.ResultIDs), "error": kind}, false)
		if errors.Is(err, tagging.ErrNoValidResultIDs) {
			apiFail(c, http.StatusBadRequest, "no_valid_result_ids")
		} else if errors.Is(err, tagging.ErrQueueFull) {
			apiFail(c, http.StatusServiceUnavailable, "tagging_queue_full")
		} else {
			logger.Error("BatchAutoTag submit hatası: %v", err)
			apiFail(c, http.StatusInternalServerError, "batch_tag_failed")
		}
		return
	}

	s.audit(c, "tagging.batch.submit", "job:"+job.ID, map[string]any{"requested": len(req.ResultIDs), "queued": job.TotalCount, "query": req.Query}, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"jobId":   job.ID,
		"status":  job.Status,
		"total":   job.TotalCount,
	})
}

// handleBatchAutoTagStatus toplu etiketleme iş durumunu döndürür
func (s *Server) handleBatchAutoTagStatus(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		apiFail(c, http.StatusBadRequest, "job_id_required")
		return
	}

	job, err := s.db.GetTaggingJob(jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			apiFail(c, http.StatusNotFound, "job_not_found")
			return
		}
		apiFail(c, http.StatusInternalServerError, "job_status_failed")
		return
	}

	// error_message veritabanında sabit Türkçe metin olarak durur; istemciye
	// kararlı kod (errorCode) ve istek dilindeki iletisi gider.
	jobErr, jobErrCode := "", ""
	if job.ErrorMessage != "" {
		jobErrCode = taggingJobErrCode(job.ErrorMessage)
		jobErr = apiMsg(apiLang(c), jobErrCode)
	}
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"jobId":      job.ID,
		"status":     job.Status,
		"total":      job.TotalCount,
		"processed":  job.ProcessedCount,
		"successes":  job.SuccessCount,
		"failures":   job.FailureCount,
		"error":      jobErr,
		"errorCode":  jobErrCode,
		"createdAt":  job.CreatedAt,
		"startedAt":  job.StartedAt,
		"finishedAt": job.FinishedAt,
	})
}

// taggingJobErrCode etiketleme işinin kayıtlı hata iletisini kararlı koda
// eşler. Bilinmeyen (eski sürümden kalma vb.) bir ileti ham haliyle
// istemciye gitmez; genel "tagging_job_failed" koduna düşer.
func taggingJobErrCode(msg string) (code string) {
	switch msg {
	case tagging.JobMsgCancelled:
		code = "job_cancelled_by_user"
	case tagging.JobMsgQueueFull:
		code = "tagging_queue_full"
	case tagging.JobMsgPayloadInvalid:
		code = "job_payload_invalid"
	case tagging.JobMsgPayloadEmpty:
		code = "job_payload_empty"
	case tagging.JobMsgAllFailed:
		code = "job_all_failed"
	case storage.TaggingJobRestartedMsg:
		code = "job_app_restarted"
	default:
		code = "tagging_job_failed"
	}
	return code
}

// handleBatchAutoTagCancel çalışan/queued işi iptal eder
func (s *Server) handleBatchAutoTagCancel(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		apiFail(c, http.StatusBadRequest, "job_id_required")
		return
	}

	if err := s.batchRunner.Cancel(jobID); err != nil {
		s.audit(c, "tagging.batch.cancel", "job:"+jobID, map[string]any{"error": auditErrKind(err, sql.ErrNoRows, "not_found")}, false)
		if errors.Is(err, sql.ErrNoRows) {
			apiFail(c, http.StatusNotFound, "job_not_found")
			return
		}
		logger.Error("BatchAutoTag cancel hatası: jobID=%s, err=%v", jobID, err)
		apiFail(c, http.StatusInternalServerError, "cancel_failed")
		return
	}

	status := tagging.StatusCancelled
	if job, err := s.db.GetTaggingJob(jobID); err == nil && job != nil {
		switch job.Status {
		case tagging.StatusPending:
			// Kuyrukta bekleyen iş hemen iptal edilir (worker hiç almadan).
			_ = s.db.MarkTaggingJobFinished(jobID, tagging.StatusCancelled, "Kullanıcı tarafından iptal edildi")
		case tagging.StatusRunning:
			// Worker mevcut kaydı bitirince "cancelled" yazar.
			status = "cancelling"
		default:
			// Tamamlanmış/başarısız iş iptalle geçersiz kılınmaz.
			status = job.Status
		}
	}

	s.audit(c, "tagging.batch.cancel", "job:"+jobID, map[string]any{"status": status}, true)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"jobId":   jobID,
		"status":  status,
	})
}

// handleAnalyzeResult belirli bir bulguyu tarayıp anahtar kelime sayısını günceller
func (s *Server) handleAnalyzeResult(c *gin.Context) {
	var req struct {
		ID int64 `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiError(c, http.StatusBadRequest, "invalid_id")
		return
	}

	// Sonucu getir
	result, err := s.db.GetResultByID(req.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			apiError(c, http.StatusNotFound, "result_not_found")
			return
		}
		logger.Error("Bulgu getirilemedi: ID=%d, err=%v", req.ID, err)
		apiError(c, http.StatusInternalServerError, "result_load_failed")
		return
	}

	logger.Info("ANALYZE START: ID=%d, URL=%s, Query=%s", result.ID, result.URL, result.Query)
	// Kelime sayısını bul (süre sınırlı)
	actx, acancel := context.WithTimeout(c.Request.Context(), autoTagMaxWait)
	defer acancel()
	count, err := s.scraper.CountKeywords(actx, result.URL, result.Query)
	if err != nil {
		logger.Warn("ANALYZE FAILED: ID=%d, Error=%v", req.ID, err)
		s.audit(c, "result.analyze", "result:"+strconv.FormatInt(req.ID, 10), map[string]any{"error": "fetch_failed"}, false)
		c.JSON(http.StatusInternalServerError, fetchErrBody(c, "analyze_failed", err, nil))
		return
	}

	logger.Info("ANALYZE SUCCESS: ID=%d, MatchCount=%d", req.ID, count)
	// Güncelle
	if err := s.db.UpdateKeywordCount(req.ID, count); err != nil {
		logger.DatabaseError("UpdateKeywordCount", err)
		apiError(c, http.StatusInternalServerError, "db_update_failed")
		return
	}

	s.audit(c, "result.analyze", "result:"+strconv.FormatInt(req.ID, 10), map[string]any{"keywordCount": count}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "count": count})
}

// handleGraphAPI graf verisi API endpoint
func (s *Server) handleGraphAPI(c *gin.Context) {
	query := c.Query("q")
	options := storage.GraphDataOptions{}

	if raw := c.Query("maxQueries"); raw != "" {
		value, err := parseGraphLimit(raw, 500)
		if err != nil {
			apiErrorFrom(c, http.StatusBadRequest, err)
			return
		}
		options.MaxQueries = value
	}

	if raw := c.Query("maxResultsPerEngine"); raw != "" {
		value, err := parseGraphLimit(raw, 1000)
		if err != nil {
			apiErrorFrom(c, http.StatusBadRequest, err)
			return
		}
		options.MaxResultsPerEngine = value
	}

	graphData, err := s.db.GetGraphData(query, options)
	if err != nil {
		respondInternalError(c, "GetGraphData", err)
		return
	}
	c.JSON(http.StatusOK, graphData)
}

func (s *Server) handleGraphQueriesAPI(c *gin.Context) {
	queryFilter := strings.TrimSpace(c.Query("q"))

	limit := 50
	if raw := c.Query("limit"); raw != "" {
		value, err := parseGraphLimit(raw, 500)
		if err != nil {
			apiErrorFrom(c, http.StatusBadRequest, err)
			return
		}
		if value > 0 {
			limit = value
		}
	}

	querySQL := `
		SELECT query, COUNT(*) as count
		FROM search_results
	`
	args := []interface{}{}
	if queryFilter != "" {
		querySQL += " WHERE query = ?\n"
		args = append(args, queryFilter)
	}
	querySQL += `
		GROUP BY query
		ORDER BY count DESC
		LIMIT ?
	`
	args = append(args, limit)

	rows, err := s.db.GetDBConn().Query(querySQL, args...)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "graph_queries_failed")
		return
	}
	defer rows.Close()

	items := make([]GraphQuerySummary, 0, limit)
	for rows.Next() {
		var q GraphQuerySummary
		if err := rows.Scan(&q.Query, &q.Count); err == nil {
			items = append(items, q)
		}
	}
	if err := rows.Err(); err != nil {
		respondInternalError(c, "GraphQueries", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"queries": items})
}

func (s *Server) handleGraphEnginesAPI(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		apiError(c, http.StatusBadRequest, "q_required")
		return
	}

	limit := 100
	if raw := c.Query("limit"); raw != "" {
		value, err := parseGraphLimit(raw, 500)
		if err != nil {
			apiErrorFrom(c, http.StatusBadRequest, err)
			return
		}
		if value > 0 {
			limit = value
		}
	}

	rows, err := s.db.GetDBConn().Query(`
		SELECT source, COUNT(*) as count
		FROM search_results
		WHERE query = ?
		GROUP BY source
		ORDER BY count DESC
		LIMIT ?
	`, query, limit)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "graph_engines_failed")
		return
	}
	defer rows.Close()

	items := make([]GraphEngineSummary, 0, limit)
	for rows.Next() {
		var e GraphEngineSummary
		if err := rows.Scan(&e.Engine, &e.Count); err == nil {
			items = append(items, e)
		}
	}
	if err := rows.Err(); err != nil {
		respondInternalError(c, "GraphEngines", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"query": query, "engines": items})
}

func (s *Server) handleGraphResultsAPI(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	engine := strings.TrimSpace(c.Query("engine"))
	if query == "" || engine == "" {
		apiError(c, http.StatusBadRequest, "q_engine_required")
		return
	}

	limit := 200
	if raw := c.Query("limit"); raw != "" {
		value, err := parseGraphLimit(raw, 1000)
		if err != nil {
			apiErrorFrom(c, http.StatusBadRequest, err)
			return
		}
		if value > 0 {
			limit = value
		}
	}

	offset := 0
	if raw := c.Query("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			apiError(c, http.StatusBadRequest, "invalid_offset")
			return
		}
		offset = value
	}

	queryLimit := limit + 1

	rows, err := s.db.GetDBConn().Query(`
		WITH dedup AS (
			SELECT
				MIN(sr.id) as id,
				MAX(COALESCE(NULLIF(sr.title, ''), sr.url)) as title,
				sr.url as url
			FROM search_results sr
			WHERE sr.query = ? AND sr.source = ?
			GROUP BY sr.url
		)
		SELECT
			d.id,
			d.title,
			d.url,
			(
				SELECT COUNT(DISTINCT sr2.source)
				FROM search_results sr2
				WHERE sr2.url = d.url
			) as source_count,
			COALESCE((
				SELECT MAX(gn.is_expanded)
				FROM graph_nodes gn
				WHERE gn.url = d.url
			), 0) as is_expanded,
			COALESCE((
				SELECT gn.domain
				FROM graph_nodes gn
				WHERE gn.url = d.url
				ORDER BY gn.id DESC
				LIMIT 1
			), '') as domain
		FROM dedup d
		ORDER BY d.title, d.url
		LIMIT ? OFFSET ?
	`, query, engine, queryLimit, offset)
	if err != nil {
		apiError(c, http.StatusInternalServerError, "graph_results_failed")
		return
	}
	defer rows.Close()

	items := make([]GraphResultSummary, 0, limit)
	for rows.Next() {
		var r GraphResultSummary
		var expandedRaw int
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.SourceCount, &expandedRaw, &r.Domain); err == nil {
			r.IsExpanded = expandedRaw > 0
			items = append(items, r)
		}
	}
	if err := rows.Err(); err != nil {
		respondInternalError(c, "GraphResults", err)
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	nextOffset := 0
	if hasMore {
		nextOffset = offset + limit
	}

	c.JSON(http.StatusOK, gin.H{
		"query":      query,
		"engine":     engine,
		"offset":     offset,
		"limit":      limit,
		"nextOffset": nextOffset,
		"results":    items,
	})
}

func parseGraphLimit(raw string, maxAllowed int) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, errCode("invalid_limit")
	}
	if value < 0 {
		return 0, errCode("invalid_limit")
	}
	if value > maxAllowed {
		value = maxAllowed
	}
	return value, nil
}

func (s *Server) handleEnvSettingsGet(c *gin.Context) {
	if s.envStore == nil {
		apiFail(c, http.StatusInternalServerError, "env_store_unavailable")
		return
	}

	values, err := s.envStore.Read()
	if err != nil {
		apiFail(c, http.StatusInternalServerError, "env_read_failed")
		return
	}

	toInt := func(raw string, fallback int) int {
		v, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return fallback
		}
		return v
	}

	toFloat := func(raw string, fallback float64) float64 {
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return fallback
		}
		return v
	}

	passwordStorage := "bcrypt"
	if strings.TrimSpace(values["ADMIN_PASS_HASH"]) == "" && strings.TrimSpace(values["ADMIN_PASS"]) != "" {
		passwordStorage = "plaintext"
	}
	adminUser := ""
	if s.creds != nil {
		adminUser = s.creds.Username()
	}
	payload := gin.H{
		"adminUser":       firstNonEmpty(values["ADMIN_USER"], adminUser),
		"adminPassMasked": "********",
		"passwordStorage": passwordStorage,
		"version":         Version,
		"torProxy":        firstNonEmpty(values["TOR_PROXY"], "127.0.0.1:9150"),
		"dbPath":          firstNonEmpty(values["DB_PATH"], "keywordhunter.db"),
		"webAddr":         firstNonEmpty(values["WEB_ADDR"], ":8080"),
		"logDir":          firstNonEmpty(values["LOG_DIR"], "logs"),
		"secureCookies":   strings.EqualFold(values["WEB_SECURE_COOKIES"], "true") || values["WEB_SECURE_COOKIES"] == "1",
		"sessionTtlHours": toInt(values["SESSION_TTL_HOURS"], int(s.sessionTTL.Hours())),
		"rateLimitRps":    toFloat(values["RATE_LIMIT_RPS"], 25),
		"rateLimitBurst":  toInt(values["RATE_LIMIT_BURST"], 80),
		"envFilePath":     s.envStore.Path(),
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "settings": payload})
}

func (s *Server) handleEnvSettingsUpdate(c *gin.Context) {
	if s.envStore == nil {
		apiFail(c, http.StatusInternalServerError, "env_store_unavailable")
		return
	}

	var req envSettingsPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "settings_payload_invalid")
		return
	}

	req.AdminUser = strings.TrimSpace(req.AdminUser)
	req.TorProxy = strings.TrimSpace(req.TorProxy)
	req.DBPath = strings.TrimSpace(req.DBPath)
	req.WebAddr = strings.TrimSpace(req.WebAddr)
	req.LogDir = strings.TrimSpace(req.LogDir)

	// Path alanlarını normalize et (path traversal/temizlik)
	if req.DBPath != "" {
		req.DBPath = filepath.Clean(req.DBPath)
	}
	if req.LogDir != "" {
		req.LogDir = filepath.Clean(req.LogDir)
	}

	if req.AdminUser == "" {
		apiFail(c, http.StatusBadRequest, "admin_username_required")
		return
	}
	if req.TorProxy == "" || req.DBPath == "" || req.WebAddr == "" || req.LogDir == "" {
		apiFail(c, http.StatusBadRequest, "env_paths_required")
		return
	}
	if req.SessionTTL < 1 || req.SessionTTL > 720 {
		apiFail(c, http.StatusBadRequest, "session_ttl_range")
		return
	}
	if req.RateLimitRPS < 1 || req.RateLimitRPS > 200 {
		apiFail(c, http.StatusBadRequest, "rate_rps_range")
		return
	}
	if req.RateLimitBurst < 1 || req.RateLimitBurst > 500 {
		apiFail(c, http.StatusBadRequest, "rate_burst_range")
		return
	}

	newPass := req.AdminPass
	if newPass != "" && (len([]rune(newPass)) < 8 || len(newPass) > 256) {
		apiFail(c, http.StatusBadRequest, "admin_password_length")
		return
	}
	if strings.ContainsAny(req.AdminUser, " \t\r\n") || len(req.AdminUser) > 64 {
		apiFail(c, http.StatusBadRequest, "admin_username_invalid")
		return
	}
	if _, _, err := netSplitHostPort(req.TorProxy); err != nil {
		apiFail(c, http.StatusBadRequest, "tor_proxy_invalid")
		return
	}

	updates := map[string]string{
		"ADMIN_USER":         req.AdminUser,
		"TOR_PROXY":          req.TorProxy,
		"DB_PATH":            req.DBPath,
		"WEB_ADDR":           req.WebAddr,
		"LOG_DIR":            req.LogDir,
		"WEB_SECURE_COOKIES": strconv.FormatBool(req.SecureCookies),
		"SESSION_TTL_HOURS":  strconv.Itoa(req.SessionTTL),
		"RATE_LIMIT_RPS":     fmt.Sprintf("%.2f", req.RateLimitRPS),
		"RATE_LIMIT_BURST":   strconv.Itoa(req.RateLimitBurst),
	}

	// Denetim için hangi anahtarların değiştiğini mevcut dosyayla karşılaştır.
	changedKeys := changedEnvKeys(s.envStore, updates)
	if newPass != "" {
		changedKeys = append(changedKeys, "ADMIN_PASS")
	}

	// Kimlik bilgileri anında uygulanır ve parola yalnızca bcrypt hash olarak saklanır
	// (düz metin ADMIN_PASS dosyadan kaldırılır).
	applied := []string{"RATE_LIMIT_RPS", "RATE_LIMIT_BURST", "SESSION_TTL_HOURS", "ADMIN_USER"}
	if s.creds != nil {
		hash, err := s.creds.Update(req.AdminUser, newPass)
		if err != nil {
			respondInternalError(c, "CredentialUpdate", err)
			return
		}
		if newPass != "" {
			updates["ADMIN_PASS_HASH"] = hash
			updates["ADMIN_PASS"] = "" // sil
			applied = append(applied, "ADMIN_PASS")
		}
	}

	if err := s.envStore.Update(updates); err != nil {
		s.audit(c, "settings.env.update", "env", map[string]any{"changed": changedKeys, "error": "env_persist"}, false)
		apiFail(c, http.StatusInternalServerError, "env_write_failed")
		return
	}
	// Yalnız değişen anahtar ADLARI kaydedilir; değerler (parola hash'i dahil) asla.
	s.audit(c, "settings.env.update", "env", map[string]any{"changed": changedKeys}, true)

	if s.rateLimiter != nil {
		s.rateLimiter.UpdatePolicy(req.RateLimitRPS, req.RateLimitBurst)
	}
	s.sessionTTL = time.Duration(req.SessionTTL) * time.Hour

	// Parola değiştiyse diğer tüm oturumları düşür
	if newPass != "" {
		if sid, ok := c.Get("sessionID"); ok {
			if sidStr, ok := sid.(string); ok { // bağlam değeri: bare assert yerine comma-ok
				if n, err := s.db.DeleteOtherSessions(sidStr); err == nil && n > 0 {
					logger.Info("Parola değişti: %d diğer oturum sonlandırıldı", n)
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"message":         apiText(c, "settings_saved"),
		"appliedRuntime":  applied,
		"requiresRestart": []string{"TOR_PROXY", "DB_PATH", "WEB_ADDR", "LOG_DIR", "WEB_SECURE_COOKIES"},
	})
}

// changedEnvKeys mevcut .env değerleriyle karşılaştırıp değişecek anahtar
// adlarını sıralı döndürür (değerler döndürülmez). ADMIN_PASS_HASH/ADMIN_PASS
// çağıran tarafından "ADMIN_PASS" olarak ayrıca eklenir.
func changedEnvKeys(store *config.EnvStore, updates map[string]string) []string {
	current, err := store.Read()
	if err != nil {
		current = map[string]string{}
	}
	keys := make([]string, 0, len(updates))
	for k, v := range updates {
		if k == "ADMIN_PASS_HASH" || k == "ADMIN_PASS" {
			continue
		}
		if strings.TrimSpace(current[k]) != strings.TrimSpace(v) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// tagErrKind etiketleme hatasını denetim için kaba türe indirger.
func tagErrKind(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, tagging.ErrResultNotFound):
		return "not_found"
	case errors.Is(err, tagging.ErrNoTaggableSignal):
		return "no_signal"
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, tagging.ErrFetchFailed):
		return "fetch_failed"
	default:
		return "internal"
	}
}

// netSplitHostPort net.SplitHostPort sarmalayıcısı (test edilebilirlik için).
func netSplitHostPort(v string) (string, string, error) {
	return shared.SplitHostPort(v)
}

func firstNonEmpty(primary string, fallback string) string {
	primary = strings.TrimSpace(primary)
	if primary != "" {
		return primary
	}
	return fallback
}

// handleTagStats etiket istatistiklerini döndürür (tag cloud için)
func (s *Server) handleTagStats(c *gin.Context) {
	stats, err := s.db.GetTagStats()
	if err != nil {
		respondInternalError(c, "GetTagStats", err)
		return
	}

	// Etiketli sonuç sayısı
	tagged, total, _ := s.db.GetTaggedResultsCount()

	c.JSON(http.StatusOK, gin.H{
		"tags":        stats,
		"taggedCount": tagged,
		"totalCount":  total,
		"taggedPercent": func() int {
			if total == 0 {
				return 0
			}
			return (tagged * 100) / total
		}(),
	})
}

// handleResultsByTag belirli bir etikete sahip sonuçları döndürür
func (s *Server) handleResultsByTag(c *gin.Context) {
	tag := c.Query("tag")
	if tag == "" {
		apiError(c, http.StatusBadRequest, "tag_required")
		return
	}

	limitStr := c.DefaultQuery("limit", "50")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 500 {
		limit = 50
	}

	results, err := s.db.GetResultsByTag(tag, limit)
	if err != nil {
		respondInternalError(c, "GetResultsByTag", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"tag":     tag,
		"count":   len(results),
		"results": results,
	})
}

// handleStats istatistikler API
func (s *Server) handleStats(c *gin.Context) {
	totalResults, totalSearches, err := s.db.GetStats()
	if err != nil {
		logger.Error("İstatistikler getirilemedi: %v", err)
	}
	c.JSON(http.StatusOK, gin.H{
		"totalResults":  totalResults,
		"totalSearches": totalSearches,
	})
}

// handleNewResults son N saatte scheduler tarafından bulunan yeni sonuçları döndürür
func (s *Server) handleNewResults(c *gin.Context) {
	hoursStr := c.DefaultQuery("hours", "24")
	limitStr := c.DefaultQuery("limit", "20")

	hours, err := strconv.Atoi(hoursStr)
	if err != nil || hours <= 0 || hours > 168 {
		hours = 24
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 100 {
		limit = 20
	}

	results, err := s.db.GetNewResults(hours, limit)
	if err != nil {
		respondInternalError(c, "GetNewResults", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"hours":   hours,
		"count":   len(results),
		"results": results,
	})
}

// handleQueriesAPI mevcut sorguları döndürür
func (s *Server) handleQueriesAPI(c *gin.Context) {
	queries, err := s.db.GetQueries()
	if err != nil {
		respondInternalError(c, "GetQueries", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"queries": queries,
	})
}

// handleAnalyticsAPI grafik verilerini JSON olarak döndürür
func (s *Server) handleAnalyticsAPI(c *gin.Context) {
	interval := c.DefaultQuery("interval", "day")
	query := c.Query("query") // Sorgu bazlı filtreleme için
	data, err := s.db.GetAnalyticsData(interval, query)
	if err != nil {
		respondInternalError(c, "GetAnalyticsData", err)
		return
	}
	c.JSON(http.StatusOK, data)
}

// handleExpandNode bir node'u derinleştirir
func (s *Server) handleExpandNode(c *gin.Context) {
	var req ExpandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}

	// Doğrulama hatası istemci hatasıdır (400): Tor'a gitmeden reddedilir.
	if !shared.IsOnionURL(req.URL) {
		apiFail(c, http.StatusBadRequest, "expand_onion_only")
		return
	}

	logger.Info("Derinleştirme başlatıldı: %s", req.URL)

	// URL'yi scrape et ve linkleri çıkar
	links, err := s.scraper.ExtractLinksFromURL(c.Request.Context(), req.URL)
	if err != nil {
		logger.ExpandNode(req.URL, 0, err)
		s.audit(c, "graph.expand", auditURL(req.URL), map[string]any{"error": "fetch_failed"}, false)
		body := fetchErrBody(c, "expand_failed", err, nil)
		body["success"] = false
		c.JSON(http.StatusInternalServerError, body)
		return
	}

	// Parent node'u bul veya oluştur
	parentNode, err := s.db.GetGraphNodeByURL(req.URL)
	var parentID int64 = 0
	var parentDepth int = 0

	if err == nil && parentNode != nil {
		parentID = parentNode.ID
		parentDepth = parentNode.Depth
	} else if req.ParentID > 0 {
		// ParentID sağlandıysa onu kullan
		parentID = req.ParentID
		pNode, err := s.db.GetGraphNodeByID(req.ParentID)
		if err != nil {
			logger.Warn("Parent node bulunamadı (ID: %d): %v", req.ParentID, err)
		}
		if pNode != nil {
			parentDepth = pNode.Depth
		}
	}

	// Linkleri graph_nodes tablosuna kaydet
	var graphNodes []storage.GraphNodeDB
	for _, link := range links {
		// Kendine link veriyorsa atla
		if link.URL == req.URL {
			continue
		}

		// ParentID pointer olarak ayarla
		var parentIDPtr *int64
		if parentID > 0 {
			parentIDPtr = &parentID
		}

		node := storage.GraphNodeDB{
			URL:         link.URL,
			Title:       link.Title,
			Domain:      link.Domain,
			ParentID:    parentIDPtr,
			Depth:       parentDepth + 1,
			LinkType:    link.LinkType,
			SourceQuery: req.Query,
		}
		graphNodes = append(graphNodes, node)
	}

	// Batch save
	savedCount := 0
	if len(graphNodes) > 0 {
		savedCount, err = s.db.SaveGraphNodes(graphNodes)
		if err != nil {
			logger.Warn("Graph nodes kaydedilemedi: %v", err)
		}
	}

	logger.ExpandNode(req.URL, len(links), nil)

	// Parent'ı expanded olarak işaretle
	if parentID > 0 {
		s.db.MarkNodeExpanded(parentID)
	}

	s.audit(c, "graph.expand", auditURL(req.URL), map[string]any{"links": len(links), "saved": savedCount}, true)

	// Children node'ları getir
	children := buildChildrenNodes(links)

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"totalLinks":    len(links),
		"savedLinks":    savedCount,
		"internalCount": countByType(links, "internal"),
		"externalCount": countByType(links, "external"),
		"children":      children,
		"graphNodeId":   parentID,
	})
}

// handleGetChildren bir node'un children'larını D3-uyumlu GraphNode formatında döndürür
func (s *Server) handleGetChildren(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_id")
		return
	}

	children, err := s.db.GetGraphChildren(id)
	if err != nil {
		logger.Error("GetGraphChildren hatası: id=%d, err=%v", id, err)
		apiFail(c, http.StatusInternalServerError, "graph_children_failed")
		return
	}

	nodes := make([]storage.GraphNode, 0, len(children))
	for _, child := range children {
		nodes = append(nodes, storage.GraphNode{
			Name:       child.Title,
			URL:        child.URL,
			Type:       child.LinkType,
			NodeID:     child.ID,
			IsExpanded: child.IsExpanded,
			Domain:     child.Domain,
			Children:   []*storage.GraphNode{},
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"children": nodes,
	})
}

// handleAlertConfigGet mevcut bildirim ayarlarını döndürür
func (s *Server) handleAlertConfigGet(c *gin.Context) {
	cfg, err := s.db.GetAlertConfig()
	if err != nil {
		respondInternalError(c, "GetAlertConfig", err)
		return
	}
	// Webhook adresi bir sırdır (Slack/Discord yol belirteci); yalnız admin tam görür.
	hook := cfg.WebhookURL
	if roleRank(c.GetString("role")) < roleRank("admin") {
		hook = shared.RedactURL(hook)
	}
	c.JSON(http.StatusOK, gin.H{"webhookUrl": hook, "minCriticality": cfg.MinCriticality, "enabled": cfg.Enabled, "updatedAt": cfg.UpdatedAt, "allowPrivateTargets": notify.AllowPrivateTargets()})
}

// handleAlertConfigSave bildirim ayarlarını kaydeder
func (s *Server) handleAlertConfigSave(c *gin.Context) {
	var req struct {
		WebhookURL     string `json:"webhookUrl"`
		MinCriticality int    `json:"minCriticality"`
		Enabled        bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if req.MinCriticality < 1 || req.MinCriticality > 5 {
		req.MinCriticality = 3
	}

	cfg := storage.AlertConfig{
		WebhookURL:     strings.TrimSpace(req.WebhookURL),
		MinCriticality: req.MinCriticality,
		Enabled:        req.Enabled,
	}
	// Admin dışı roller GET/sayfada gizlenmiş adresi görür; maskeyi olduğu gibi
	// geri gönderirlerse (yalnız eşik/etkin değiştirmek için) gerçek sır
	// maskeyle EZİLMESİN — kayıtlı adres korunur. Yeni bir adres ise normal kaydedilir.
	if cfg.WebhookURL != "" {
		if cur, err := s.db.GetAlertConfig(); err == nil && cur != nil && cur.WebhookURL != "" &&
			cfg.WebhookURL == shared.RedactURL(cur.WebhookURL) {
			cfg.WebhookURL = cur.WebhookURL
		}
	}
	if cfg.WebhookURL != "" && !notify.ValidTarget(cfg.WebhookURL) {
		apiFail(c, http.StatusBadRequest, "webhook_invalid")
		return
	}
	if cfg.Enabled && cfg.WebhookURL == "" {
		apiFail(c, http.StatusBadRequest, "webhook_required_enable")
		return
	}
	prev, _ := s.db.GetAlertConfig()
	if err := s.db.SaveAlertConfig(cfg); err != nil {
		logger.Error("SaveAlertConfig hatası: %v", err)
		s.audit(c, "alert.config.update", "alert_config", map[string]any{"error": "internal"}, false)
		apiFail(c, http.StatusInternalServerError, "settings_save_failed")
		return
	}
	// Webhook adresi bir sırdır: yalnız ana makine (RedactURL) ve değişip değişmediği kaydedilir.
	s.audit(c, "alert.config.update", "alert_config", map[string]any{
		"enabled": cfg.Enabled, "minCriticality": cfg.MinCriticality,
		"target": shared.RedactURL(cfg.WebhookURL), "targetChanged": prev == nil || prev.WebhookURL != cfg.WebhookURL,
	}, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAlertConfigTest kayıtlı veya gönderilen webhook adresine test mesajı yollar.
func (s *Server) handleAlertConfigTest(c *gin.Context) {
	var req struct {
		WebhookURL string `json:"webhookUrl"`
	}
	_ = c.ShouldBindJSON(&req)
	target := strings.TrimSpace(req.WebhookURL)
	if target == "" {
		if cfg, err := s.db.GetAlertConfig(); err == nil {
			target = cfg.WebhookURL
		}
	}
	if !notify.ValidTarget(target) {
		s.audit(c, "alert.test", shared.RedactURL(target), map[string]any{"error": "invalid_target"}, false)
		apiFail(c, http.StatusBadRequest, "webhook_required")
		return
	}
	if err := notify.SendTest(target); err != nil {
		s.audit(c, "alert.test", shared.RedactURL(target), map[string]any{"error": "delivery_failed"}, false)
		apiFail(c, http.StatusBadGateway, "webhook_test_failed", truncateErr(err.Error()))
		return
	}
	s.audit(c, "alert.test", shared.RedactURL(target), nil, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": apiText(c, "webhook_test_sent")})
}

// handleExportResults bulguları CSV veya JSON olarak dışa aktarır
// (SIEM/SOAR/rapor akışları için). ?format=csv|json&q=&limit=&minCriticality=
func (s *Server) handleExportResults(c *gin.Context) {
	if refuseCrossSiteExport(c, "/results") {
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "csv")))
	f := parseResultFilter(c)
	f.Offset = 0
	f.Limit, _ = strconv.Atoi(c.DefaultQuery("limit", "5000"))
	if f.Limit <= 0 || f.Limit > 50000 {
		f.Limit = 5000
	}
	query := f.Query

	filtered, _, err := s.db.GetResultsFiltered(storage.ResultFilter{
		Query: f.Query, Text: f.Text, Source: f.Source, Category: f.Category,
		MinCriticality: f.MinCriticality, Tag: f.Tag, Sort: f.Sort, Limit: 500, Offset: 0,
	})
	if err != nil {
		respondInternalError(c, "ExportResults", err)
		return
	}
	// 500'lük sayfalarla istenen limite kadar topla (tek büyük LIMIT yerine)
	for len(filtered) < f.Limit {
		more, _, err := s.db.GetResultsFiltered(storage.ResultFilter{
			Query: f.Query, Text: f.Text, Source: f.Source, Category: f.Category,
			MinCriticality: f.MinCriticality, Tag: f.Tag, Sort: f.Sort, Limit: 500, Offset: len(filtered),
		})
		if err != nil || len(more) == 0 {
			break
		}
		filtered = append(filtered, more...)
	}
	if len(filtered) > f.Limit {
		filtered = filtered[:f.Limit]
	}

	if format != "json" {
		format = "csv"
	}
	s.audit(c, "export."+format, "results", resultFilterSummary(f, len(filtered)), true)

	stamp := time.Now().Format("20060102-1504")
	switch format {
	case "json":
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"keywordhunter-results-%s.json\"", stamp))
		c.Header("Content-Type", "application/json; charset=utf-8")
		type row struct {
			ID           int64     `json:"id"`
			Title        string    `json:"title"`
			URL          string    `json:"url"`
			Source       string    `json:"source"`
			Query        string    `json:"query"`
			Criticality  int       `json:"criticality"`
			Category     string    `json:"category"`
			KeywordCount int       `json:"keywordCount"`
			Tags         []string  `json:"tags"`
			CaseStatus   string    `json:"caseStatus,omitempty"`
			Note         string    `json:"note,omitempty"`
			CreatedAt    time.Time `json:"createdAt"`
		}
		out := make([]row, 0, len(filtered))
		for _, r := range filtered {
			out = append(out, row{r.ID, r.Title, r.URL, r.Source, r.Query, r.Criticality, r.Category, r.KeywordCount, splitTagList(r.AutoTags), r.CaseStatus, r.Note, r.CreatedAt})
		}
		enc := json.NewEncoder(c.Writer)
		enc.SetIndent("", "  ")
		_ = enc.Encode(gin.H{"exportedAt": time.Now().UTC(), "count": len(out), "query": query, "results": out})
	default:
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"keywordhunter-results-%s.csv\"", stamp))
		c.Header("Content-Type", "text/csv; charset=utf-8")
		_, _ = c.Writer.Write([]byte("\xEF\xBB\xBF")) // Excel için BOM
		w := csv.NewWriter(c.Writer)
		_ = w.Write([]string{"id", "title", "url", "source", "query", "criticality", "category", "keyword_count", "tags", "case_status", "note", "created_at"})
		for _, r := range filtered {
			_ = w.Write([]string{
				strconv.FormatInt(r.ID, 10), csvSafe(r.Title), csvSafe(r.URL), r.Source, csvSafe(r.Query),
				strconv.Itoa(r.Criticality), csvSafe(r.Category), strconv.Itoa(r.KeywordCount), r.AutoTags,
				r.CaseStatus, csvSafe(r.Note),
				r.CreatedAt.UTC().Format(time.RFC3339),
			})
		}
		w.Flush()
	}
}

// csvSafe formül enjeksiyonuna (=, +, -, @ ile başlayan hücreler) karşı önek ekler.
func csvSafe(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}

func splitTagList(tags string) []string {
	out := []string{}
	for _, t := range strings.Split(tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// handleResultsAPI filtrelenmiş/sayfalanmış bulguları JSON döndürür.
func (s *Server) handleResultsAPI(c *gin.Context) {
	f := parseResultFilter(c)
	results, total, err := s.db.GetResultsFiltered(f)
	if err != nil {
		respondInternalError(c, "ResultsAPI", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"total":   total,
		"limit":   f.Limit,
		"offset":  f.Offset,
		"results": results,
	})
}

// handleDeleteResults seçilen bulguları siler (analist ilgisiz kayıtları ayıklayabilir).
func (s *Server) handleDeleteResults(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) == 0 || len(req.IDs) > 500 {
		apiFail(c, http.StatusBadRequest, "ids_range")
		return
	}
	n, err := s.db.DeleteResults(req.IDs)
	if err != nil {
		s.audit(c, "results.delete", "results", map[string]any{"requested": len(req.IDs), "error": "internal"}, false)
		respondInternalError(c, "DeleteResults", err)
		return
	}
	logger.Info("RESULTS DELETED: %d kayıt", n)
	s.audit(c, "results.delete", "results", map[string]any{"requested": len(req.IDs), "deleted": n, "ids": req.IDs}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "deleted": n})
}

// handleUpdateCase bir bulgunun vaka durumunu ve notunu günceller (analist iş akışı).
func (s *Server) handleUpdateCase(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		apiFail(c, http.StatusBadRequest, "invalid_id")
		return
	}
	var req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if !storage.ValidCaseStatus(req.Status) {
		apiFail(c, http.StatusBadRequest, "invalid_case_status")
		return
	}
	n, err := s.db.UpdateResultCase(id, req.Status, strings.TrimSpace(req.Note))
	if err != nil {
		respondInternalError(c, "UpdateResultCase", err)
		return
	}
	if n == 0 {
		apiFail(c, http.StatusNotFound, "record_not_found")
		return
	}
	// Notun kendisi değil uzunluğu kaydedilir (analist notu hassas olabilir).
	s.audit(c, "result.case.update", "result:"+strconv.FormatInt(id, 10), map[string]any{"status": req.Status, "noteLength": len([]rune(strings.TrimSpace(req.Note)))}, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleCaseCounts vaka durumu dağılımını döndürür.
func (s *Server) handleCaseCounts(c *gin.Context) {
	counts, err := s.db.CaseCounts()
	if err != nil {
		respondInternalError(c, "CaseCounts", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"counts": counts})
}
