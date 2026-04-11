package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/tagging"
)

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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz istek"})
		return
	}

	table := "search_results"
	if req.Type == "" {
		req.Type = "result"
	}
	if req.Type != "result" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz type değeri"})
		return
	}

	query := fmt.Sprintf("UPDATE %s SET criticality = ?, category = ? WHERE id = ?", table)
	_, err := s.db.GetDBConn().Exec(query, req.Criticality, req.Category, req.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Info("CRITICALITY UPDATE: ID=%d, Type=%s, Crit=%d, Cat=%s", req.ID, req.Type, req.Criticality, req.Category)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAutoTag tek bir sonucu etiketler
func (s *Server) handleAutoTag(c *gin.Context) {
	var req AutoTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}

	result, err := s.tagEngine.TagResultByID(c.Request.Context(), req.ID)
	if err != nil {
		logger.Warn("AUTO TAG FAILED: ID=%d, Error=%v", req.ID, err)
		switch {
		case errors.Is(err, tagging.ErrResultNotFound):
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Sonuç bulunamadı"})
		case errors.Is(err, tagging.ErrNoTaggableSignal):
			c.JSON(http.StatusUnprocessableEntity, gin.H{"success": false, "error": "Etiket çıkarılamadı"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		}
		return
	}

	logger.Info("AUTO TAG SUCCESS: ID=%d, Tags=%s, Hits=%d", req.ID, result.TagsStr, result.KeywordHits)
	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"id":          result.ResultID,
		"tags":        result.Tags,
		"tagsStr":     result.TagsStr,
		"keywordHits": result.KeywordHits,
		"category":    result.Category,
		"criticality": result.Criticality,
		"confidence":  result.Confidence,
	})
}

// handleBatchAutoTag toplu etiketleme işini kuyruğa alır
func (s *Server) handleBatchAutoTag(c *gin.Context) {
	var req BatchAutoTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}

	if len(req.ResultIDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Etiketlenecek kayıt bulunamadı"})
		return
	}

	job, err := s.batchRunner.Submit(c.Request.Context(), req.ResultIDs, req.Query)
	if err != nil {
		logger.Warn("BATCH AUTO TAG SUBMIT FAILED: %v", err)
		if errors.Is(err, tagging.ErrNoValidResultIDs) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçerli result ID bulunamadı"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		}
		return
	}

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
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Job ID gerekli"})
		return
	}

	job, err := s.db.GetTaggingJob(jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Job bulunamadı"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Job durumu alınamadı"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"jobId":      job.ID,
		"status":     job.Status,
		"total":      job.TotalCount,
		"processed":  job.ProcessedCount,
		"successes":  job.SuccessCount,
		"failures":   job.FailureCount,
		"error":      job.ErrorMessage,
		"createdAt":  job.CreatedAt,
		"startedAt":  job.StartedAt,
		"finishedAt": job.FinishedAt,
	})
}

// handleBatchAutoTagCancel çalışan/queued işi iptal eder
func (s *Server) handleBatchAutoTagCancel(c *gin.Context) {
	jobID := c.Param("id")
	if jobID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Job ID gerekli"})
		return
	}

	if err := s.batchRunner.Cancel(jobID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Job bulunamadı"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	_ = s.db.MarkTaggingJobFinished(jobID, tagging.StatusCancelled, "Kullanıcı tarafından iptal edildi")

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"jobId":   jobID,
		"status":  tagging.StatusCancelled,
	})
}

// handleAnalyzeResult belirli bir bulguyu tarayıp anahtar kelime sayısını günceller
func (s *Server) handleAnalyzeResult(c *gin.Context) {
	var req struct {
		ID int64 `json:"id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}

	// Sonucu getir
	result, err := s.db.GetResultByID(req.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bulgu bulunamadı"})
		return
	}

	logger.Info("ANALYZE START: ID=%d, URL=%s, Query=%s", result.ID, result.URL, result.Query)
	// Kelime sayısını bul
	count, err := s.scraper.CountKeywords(result.URL, result.Query)
	if err != nil {
		logger.Warn("ANALYZE FAILED: ID=%d, Error=%v", req.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Tarama hatası: %v", err)})
		return
	}

	logger.Info("ANALYZE SUCCESS: ID=%d, MatchCount=%d", req.ID, count)
	// Güncelle
	if err := s.db.UpdateKeywordCount(req.ID, count); err != nil {
		logger.DatabaseError("UpdateKeywordCount", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Veritabanı güncelleme hatası"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "count": count})
}

// handleGraphAPI graf verisi API endpoint
func (s *Server) handleGraphAPI(c *gin.Context) {
	query := c.Query("q")
	options := storage.GraphDataOptions{}

	if raw := c.Query("maxQueries"); raw != "" {
		value, err := parseGraphLimit(raw, 500)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		options.MaxQueries = value
	}

	if raw := c.Query("maxResultsPerEngine"); raw != "" {
		value, err := parseGraphLimit(raw, 1000)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		options.MaxResultsPerEngine = value
	}

	graphData, err := s.db.GetGraphData(query, options)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Sorgu ozeti alinamadi"})
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

	c.JSON(http.StatusOK, gin.H{"queries": items})
}

func (s *Server) handleGraphEnginesAPI(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q parametresi zorunludur"})
		return
	}

	limit := 100
	if raw := c.Query("limit"); raw != "" {
		value, err := parseGraphLimit(raw, 500)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Engine ozeti alinamadi"})
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

	c.JSON(http.StatusOK, gin.H{"query": query, "engines": items})
}

func (s *Server) handleGraphResultsAPI(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	engine := strings.TrimSpace(c.Query("engine"))
	if query == "" || engine == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q ve engine parametreleri zorunludur"})
		return
	}

	limit := 200
	if raw := c.Query("limit"); raw != "" {
		value, err := parseGraphLimit(raw, 1000)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "gecersiz offset"})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Result ozeti alinamadi"})
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
		return 0, fmt.Errorf("geçersiz limit değeri: %s", raw)
	}
	if value < 0 {
		return 0, fmt.Errorf("limit negatif olamaz")
	}
	if value > maxAllowed {
		value = maxAllowed
	}
	return value, nil
}

func (s *Server) handleEnvSettingsGet(c *gin.Context) {
	if s.envStore == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Env store tanimli degil"})
		return
	}

	values, err := s.envStore.Read()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Env dosyasi okunamadi"})
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

	payload := gin.H{
		"adminUser":       firstNonEmpty(values["ADMIN_USER"], s.username),
		"adminPassMasked": "********",
		"torProxy":        firstNonEmpty(values["TOR_PROXY"], "127.0.0.1:9150"),
		"dbPath":          firstNonEmpty(values["DB_PATH"], "keywordhunter.db"),
		"webAddr":         firstNonEmpty(values["WEB_ADDR"], ":8080"),
		"logDir":          firstNonEmpty(values["LOG_DIR"], "logs"),
		"secureCookies":   strings.EqualFold(values["WEB_SECURE_COOKIES"], "true") || values["WEB_SECURE_COOKIES"] == "1",
		"sessionTtlHours": toInt(values["SESSION_TTL_HOURS"], int(s.sessionTTL.Hours())),
		"rateLimitRps":    toFloat(values["RATE_LIMIT_RPS"], 12),
		"rateLimitBurst":  toInt(values["RATE_LIMIT_BURST"], 30),
		"envFilePath":     s.envStore.Path(),
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "settings": payload})
}

func (s *Server) handleEnvSettingsUpdate(c *gin.Context) {
	if s.envStore == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Env store tanimli degil"})
		return
	}

	var req envSettingsPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Gecersiz ayar payload"})
		return
	}

	req.AdminUser = strings.TrimSpace(req.AdminUser)
	req.TorProxy = strings.TrimSpace(req.TorProxy)
	req.DBPath = strings.TrimSpace(req.DBPath)
	req.WebAddr = strings.TrimSpace(req.WebAddr)
	req.LogDir = strings.TrimSpace(req.LogDir)

	if req.AdminUser == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Admin kullanici adi bos olamaz"})
		return
	}
	if req.TorProxy == "" || req.DBPath == "" || req.WebAddr == "" || req.LogDir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "TOR/DB/WEB/LOG alanlari bos olamaz"})
		return
	}
	if req.SessionTTL < 1 || req.SessionTTL > 720 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Session TTL 1-720 saat araliginda olmalidir"})
		return
	}
	if req.RateLimitRPS < 1 || req.RateLimitRPS > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Rate limit RPS 1-200 araliginda olmalidir"})
		return
	}
	if req.RateLimitBurst < 1 || req.RateLimitBurst > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Rate limit burst 1-500 araliginda olmalidir"})
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

	if strings.TrimSpace(req.AdminPass) != "" {
		updates["ADMIN_PASS"] = strings.TrimSpace(req.AdminPass)
	}

	if err := s.envStore.Update(updates); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Env dosyasi guncellenemedi"})
		return
	}

	if s.rateLimiter != nil {
		s.rateLimiter.UpdatePolicy(req.RateLimitRPS, req.RateLimitBurst)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"message":         "Ayarlar kaydedildi",
		"appliedRuntime":  []string{"RATE_LIMIT_RPS", "RATE_LIMIT_BURST"},
		"requiresRestart": true,
	})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tag parametresi gerekli"})
		return
	}

	limitStr := c.DefaultQuery("limit", "50")
	limit, _ := strconv.Atoi(limitStr)

	results, err := s.db.GetResultsByTag(tag, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, data)
}

// handleExpandNode bir node'u derinleştirir
func (s *Server) handleExpandNode(c *gin.Context) {
	var req ExpandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}

	logger.Info("🔍 Derinleştirme başlatıldı: %s", req.URL)

	// URL'yi scrape et ve linkleri çıkar
	links, err := s.scraper.ExtractLinksFromURL(req.URL)
	if err != nil {
		logger.ExpandNode(req.URL, 0, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Sayfa taranamadı: %v", err),
		})
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
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}

	children, err := s.db.GetGraphChildren(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, cfg)
}

// handleAlertConfigSave bildirim ayarlarını kaydeder
func (s *Server) handleAlertConfigSave(c *gin.Context) {
	var req struct {
		WebhookURL     string `json:"webhookUrl"`
		MinCriticality int    `json:"minCriticality"`
		Enabled        bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
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
	if err := s.db.SaveAlertConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
