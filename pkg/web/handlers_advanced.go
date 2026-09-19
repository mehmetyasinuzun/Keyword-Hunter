package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/crawler"
	"keywordhunter-mvp/pkg/export"
	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── STIX 2.1 export ───────────────────────────

// handleExportSTIX filtreli bulguları ve IOC'lerini STIX 2.1 paketi olarak indirir.
func (s *Server) handleExportSTIX(c *gin.Context) {
	f := parseResultFilter(c)
	f.Offset = 0
	f.Limit = 2000
	if raw := c.Query("limit"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 && v <= 20000 {
			f.Limit = v
		}
	}
	withIOC := c.DefaultQuery("artifacts", "true") != "false"

	all := make([]storage.SearchResult, 0, f.Limit)
	for len(all) < f.Limit {
		page := f
		page.Offset = len(all)
		page.Limit = 500
		batch, _, err := s.db.GetResultsFiltered(page)
		if err != nil {
			respondInternalError(c, "ExportSTIX", err)
			return
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		if len(batch) < 500 {
			break
		}
	}
	if len(all) > f.Limit {
		all = all[:f.Limit]
	}

	bundle := export.BuildBundle(all, s.db, withIOC)
	stamp := time.Now().Format("20060102-1504")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"keywordhunter-stix-%s.json\"", stamp))
	c.Header("Content-Type", "application/stix+json;version=2.1")
	enc := json.NewEncoder(c.Writer)
	enc.SetIndent("", "  ")
	_ = enc.Encode(bundle)
}

// ─────────────────────────── Site profilleri ───────────────────────────

// handleSiteProfilesList tüm profilleri döndürür (çerezler maskeli).
func (s *Server) handleSiteProfilesList(c *gin.Context) {
	profiles, err := s.db.ListSiteProfiles()
	if err != nil {
		respondInternalError(c, "ListSiteProfiles", err)
		return
	}
	type view struct {
		Host       string    `json:"host"`
		HasCookies bool      `json:"hasCookies"`
		CookiePeek string    `json:"cookiePeek"`
		UserAgent  string    `json:"userAgent"`
		RenderJS   bool      `json:"renderJs"`
		Notes      string    `json:"notes"`
		UpdatedAt  time.Time `json:"updatedAt"`
	}
	out := make([]view, 0, len(profiles))
	for _, p := range profiles {
		peek := ""
		if p.Cookies != "" {
			names := []string{}
			for _, part := range strings.Split(p.Cookies, ";") {
				if k, _, ok := strings.Cut(strings.TrimSpace(part), "="); ok {
					names = append(names, k)
				}
			}
			peek = strings.Join(names, ", ")
			if len(peek) > 80 {
				peek = peek[:80] + "…"
			}
		}
		out = append(out, view{
			Host: p.Host, HasCookies: p.Cookies != "", CookiePeek: peek,
			UserAgent: p.UserAgent, RenderJS: p.RenderJS, Notes: p.Notes, UpdatedAt: p.UpdatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{"profiles": out, "renderAvailable": s.scraper.RendererAvailable()})
}

// handleSiteProfileSave profil oluşturur/günceller.
func (s *Server) handleSiteProfileSave(c *gin.Context) {
	var req struct {
		Host      string `json:"host"`
		Cookies   string `json:"cookies"`
		UserAgent string `json:"userAgent"`
		RenderJS  bool   `json:"renderJs"`
		Notes     string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}
	host := storage.NormalizeHost(req.Host)
	if host == "" || !strings.Contains(host, ".") {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçerli bir host girin (örn. abcd…xyz.onion)"})
		return
	}
	if len(req.Cookies) > 8192 || len(req.UserAgent) > 512 || len(req.Notes) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Alanlar çok uzun"})
		return
	}
	if err := s.db.UpsertSiteProfile(storage.SiteProfile{
		Host: host, Cookies: req.Cookies, UserAgent: req.UserAgent, RenderJS: req.RenderJS, Notes: req.Notes,
	}); err != nil {
		respondInternalError(c, "UpsertSiteProfile", err)
		return
	}
	logger.Info("SITE PROFILE: %s güncellendi (cookie=%v, renderJS=%v)", host, req.Cookies != "", req.RenderJS)
	c.JSON(http.StatusOK, gin.H{"success": true, "host": host})
}

// handleSiteProfileDelete profili siler.
func (s *Server) handleSiteProfileDelete(c *gin.Context) {
	var req struct {
		Host string `json:"host"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Host) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Host gerekli"})
		return
	}
	if err := s.db.DeleteSiteProfile(req.Host); err != nil {
		respondInternalError(c, "DeleteSiteProfile", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ─────────────────────────── Tor devre yenileme ───────────────────────────

func (s *Server) handleTorStatus(c *gin.Context) {
	if s.torCtl == nil {
		c.JSON(http.StatusOK, gin.H{"configured": false, "hint": "TOR_CONTROL ve TOR_CONTROL_PASSWORD ayarlayın (Tor Browser: 9151, servis: 9051)"})
		return
	}
	last, lastErr := s.torCtl.Status()
	ok, wait := s.torCtl.CanRenew()
	c.JSON(http.StatusOK, gin.H{
		"configured": true, "addr": s.torCtl.Addr(),
		"lastRenew": last, "lastError": lastErr,
		"canRenew": ok, "waitSeconds": int(wait.Seconds()),
	})
}

func (s *Server) handleTorNewNym(c *gin.Context) {
	if s.torCtl == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "Tor kontrol portu yapılandırılmamış (TOR_CONTROL)"})
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := s.torCtl.NewNym(req.Force); err != nil {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": err.Error()})
		return
	}
	s.invalidateEngineCache()
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Yeni Tor devresi istendi (çıkış düğümleri değişecek)"})
}

// ─────────────────────────── Örümcek (crawl) ───────────────────────────

func (s *Server) handleCrawlPage(c *gin.Context) {
	c.HTML(http.StatusOK, "crawl.html", gin.H{"ActivePage": "crawl"})
}

func (s *Server) handleCrawlSubmit(c *gin.Context) {
	if s.crawler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "Örümcek devre dışı"})
		return
	}
	var req struct {
		SeedURL    string `json:"seedUrl"`
		Depth      int    `json:"depth"`
		MaxPages   int    `json:"maxPages"`
		SameDomain bool   `json:"sameDomain"`
		Query      string `json:"query"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}
	if !shared.IsOnionURL(strings.TrimSpace(req.SeedURL)) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Tohum URL geçerli bir v3 .onion adresi olmalı"})
		return
	}
	job, err := s.crawler.Submit(crawler.Request{
		SeedURL: req.SeedURL, Depth: req.Depth, MaxPages: req.MaxPages, SameDomain: req.SameDomain, Query: req.Query,
	})
	if err != nil {
		if errors.Is(err, crawler.ErrBusy) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "Örümcek meşgul; mevcut tarama bitince tekrar deneyin"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	logger.Info("CRAWL SUBMIT: %s (derinlik %d, %d sayfa)", job.SeedURL, job.Depth, job.MaxPages)
	c.JSON(http.StatusOK, gin.H{"success": true, "job": job})
}

func (s *Server) handleCrawlList(c *gin.Context) {
	jobs, err := s.db.ListCrawlJobs(50)
	if err != nil {
		respondInternalError(c, "ListCrawlJobs", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"jobs": jobs})
}

func (s *Server) handleCrawlStatus(c *gin.Context) {
	job, err := s.db.GetCrawlJob(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "İş bulunamadı"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (s *Server) handleCrawlCancel(c *gin.Context) {
	if s.crawler == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "Örümcek devre dışı"})
		return
	}
	if err := s.crawler.Cancel(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "İptal edilemedi"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
