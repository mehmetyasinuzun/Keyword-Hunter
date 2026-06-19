package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

var watchlistTitleRegex = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// watchlistCheckResult tek bir kontrol sonucunu taşır
type watchlistCheckResult struct {
	Status      string
	HTTPCode    int
	ResponseMs  int
	Title       string
	ContentHash string
	Changed     bool
}

// checkWatchlistItem bir izleme öğesini Tor üzerinden kontrol eder
func (s *Server) checkWatchlistItem(ctx context.Context, item storage.WatchlistItem) watchlistCheckResult {
	result := watchlistCheckResult{Status: "down"}

	prevHash, _ := s.db.GetLastWatchlistHash(item.ID)

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		result.ResponseMs = int(time.Since(start).Milliseconds())
		return result
	}
	req.Header.Set("User-Agent", shared.RandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := shared.DoWithRetry(s.scraper.HTTPClient(), req)
	if err != nil {
		result.ResponseMs = int(time.Since(start).Milliseconds())
		logger.Debug("Watchlist kontrol başarısız (%s): %v", item.URL, err)
		return result
	}
	defer resp.Body.Close()

	result.HTTPCode = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, shared.MaxResponseBytes))
	result.ResponseMs = int(time.Since(start).Milliseconds())
	if err != nil {
		return result
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		result.Status = "up"
	}

	result.Title = extractWatchlistTitle(body)
	result.ContentHash = hashWatchlistContent(body)

	// Engel/doğrulama (captcha, Cloudflare) sayfası mı? Öyleyse "up" değil "engel".
	if result.Status == "up" {
		if blocked, kind := shared.DetectChallenge(result.Title, string(body)); blocked {
			result.Status = "engel"
			if result.Title == "" {
				result.Title = kind + " doğrulama ekranı"
			}
		}
	}

	if result.Status == "up" && prevHash != "" && result.ContentHash != "" && prevHash != result.ContentHash {
		result.Changed = true
	}

	return result
}

// extractWatchlistTitle HTML body'den <title> içeriğini çıkarır
func extractWatchlistTitle(body []byte) string {
	m := watchlistTitleRegex.FindSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	title := strings.TrimSpace(string(m[1]))
	title = strings.Join(strings.Fields(title), " ")
	if len(title) > 200 {
		title = title[:200]
	}
	return title
}

// hashWatchlistContent body'nin ilk 4KB'sinden sha256 hash üretir
func hashWatchlistContent(body []byte) string {
	limit := 4096
	if len(body) < limit {
		limit = len(body)
	}
	sum := sha256.Sum256(body[:limit])
	return hex.EncodeToString(sum[:])
}

// handleWatchlistList izlenen siteleri durumlarıyla döndürür
func (s *Server) handleWatchlistList(c *gin.Context) {
	items, err := s.db.GetWatchlistWithStatus()
	if err != nil {
		respondInternalError(c, "GetWatchlistWithStatus", err)
		return
	}
	if items == nil {
		items = []storage.WatchlistStatus{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "items": items})
}

// handleWatchlistAdd yeni bir izleme öğesi ekler
func (s *Server) handleWatchlistAdd(c *gin.Context) {
	var req struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Category string `json:"category"`
		Notes    string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Site adı boş olamaz"})
		return
	}
	if !strings.Contains(strings.ToLower(req.URL), ".onion") {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "URL geçerli bir .onion adresi olmalı"})
		return
	}
	if len(req.Name) > 128 || len(req.Category) > 64 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Ad veya kategori çok uzun"})
		return
	}

	id, err := s.db.AddWatchlistItem(req.Name, req.URL, req.Category, req.Notes)
	if err != nil {
		respondInternalError(c, "AddWatchlistItem", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "id": id})
}

// handleWatchlistToggle bir öğenin aktiflik durumunu değiştirir
func (s *Server) handleWatchlistToggle(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}

	item, err := s.db.GetWatchlistItem(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Öğe bulunamadı"})
		return
	}

	if err := s.db.SetWatchlistEnabled(id, !item.Enabled); err != nil {
		respondInternalError(c, "SetWatchlistEnabled", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "enabled": !item.Enabled})
}

// handleWatchlistDelete bir öğeyi siler
func (s *Server) handleWatchlistDelete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}

	if err := s.db.DeleteWatchlistItem(id); err != nil {
		respondInternalError(c, "DeleteWatchlistItem", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleWatchlistCheck bir öğeyi anında (senkron) kontrol eder
func (s *Server) handleWatchlistCheck(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}

	item, err := s.db.GetWatchlistItem(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Öğe bulunamadı"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
	defer cancel()

	result := s.checkWatchlistItem(ctx, *item)
	if err := s.db.RecordWatchlistCheck(item.ID, result.Status, result.HTTPCode, result.ResponseMs, result.ContentHash, result.Title, result.Changed); err != nil {
		logger.Warn("Watchlist kontrol kaydedilemedi (ID: %d): %v", item.ID, err)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"status":     result.Status,
		"httpCode":   result.HTTPCode,
		"title":      result.Title,
		"changed":    result.Changed,
		"responseMs": result.ResponseMs,
	})
}
