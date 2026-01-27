package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

// ExpandRequest expand isteği
type ExpandRequest struct {
	URL      string `json:"url" binding:"required"`
	ParentID int64  `json:"parentId"`
	Query    string `json:"query"`
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
	if req.Type == "content" {
		table = "contents"
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
	graphData, err := s.db.GetGraphData(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, graphData)
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
	baseDomain := extractDomainFromStr(req.URL)

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
	children := buildChildrenNodes(links, baseDomain)

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"totalLinks":    len(links),
		"savedLinks":    savedCount,
		"internalCount": countByType(links, "internal"),
		"externalCount": countByType(links, "external"),
		"children":      children,
	})
}

// handleGetChildren bir node'un children'larını getirir
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

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"children": children,
	})
}
