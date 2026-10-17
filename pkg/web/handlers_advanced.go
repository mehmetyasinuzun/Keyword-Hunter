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
	"keywordhunter-mvp/pkg/tor"
)

// ─────────────────────────── STIX 2.1 export ───────────────────────────

// handleExportSTIX filtreli bulguları ve IOC'lerini STIX 2.1 paketi olarak indirir.
func (s *Server) handleExportSTIX(c *gin.Context) {
	if refuseCrossSiteExport(c, "/results") {
		return
	}
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

	summary := resultFilterSummary(f, len(all))
	summary["artifacts"] = withIOC
	s.audit(c, "export.stix", "results", summary, true)

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
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}
	host := storage.NormalizeHost(req.Host)
	if host == "" || !strings.Contains(host, ".") {
		apiFail(c, http.StatusBadRequest, "host_invalid")
		return
	}
	if len(req.Cookies) > 8192 || len(req.UserAgent) > 512 || len(req.Notes) > 500 {
		apiFail(c, http.StatusBadRequest, "fields_too_long")
		return
	}
	if err := s.db.UpsertSiteProfile(storage.SiteProfile{
		Host: host, Cookies: req.Cookies, UserAgent: req.UserAgent, RenderJS: req.RenderJS, Notes: req.Notes,
	}); err != nil {
		s.audit(c, "site_profile.save", host, map[string]any{"error": "internal"}, false)
		respondInternalError(c, "UpsertSiteProfile", err)
		return
	}
	// Yalnız host (+ JS render bayrağı): oturum çerezleri ve UA kayda ASLA girmez.
	s.audit(c, "site_profile.save", host, map[string]any{"renderJs": req.RenderJS}, true)
	logger.Info("SITE PROFILE: %s güncellendi (cookie=%v, renderJS=%v)", host, req.Cookies != "", req.RenderJS)
	c.JSON(http.StatusOK, gin.H{"success": true, "host": host})
}

// handleSiteProfileDelete profili siler.
func (s *Server) handleSiteProfileDelete(c *gin.Context) {
	var req struct {
		Host string `json:"host"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Host) == "" {
		apiFail(c, http.StatusBadRequest, "host_required")
		return
	}
	if err := s.db.DeleteSiteProfile(req.Host); err != nil {
		s.audit(c, "site_profile.delete", storage.NormalizeHost(req.Host), map[string]any{"error": "internal"}, false)
		respondInternalError(c, "DeleteSiteProfile", err)
		return
	}
	s.audit(c, "site_profile.delete", storage.NormalizeHost(req.Host), nil, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ─────────────────────────── Tor devre yenileme ───────────────────────────

func (s *Server) handleTorStatus(c *gin.Context) {
	if s.torCtl == nil {
		c.JSON(http.StatusOK, gin.H{"configured": false, "hint": apiText(c, "tor_hint")})
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
		s.audit(c, "tor.newnym", "tor", map[string]any{"error": "not_configured"}, false)
		apiFail(c, http.StatusServiceUnavailable, "tor_control_unconfigured")
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := s.torCtl.NewNym(req.Force); err != nil {
		s.audit(c, "tor.newnym", s.torCtl.Addr(), map[string]any{"force": req.Force, "error": "rejected"}, false)
		var cd *tor.CooldownError
		if errors.As(err, &cd) {
			apiFail(c, http.StatusConflict, "tor_cooldown", ceilSeconds(cd.Wait))
			return
		}
		apiFail(c, http.StatusConflict, "tor_newnym_failed", truncateErr(err.Error()))
		return
	}
	s.invalidateEngineCache()
	s.audit(c, "tor.newnym", s.torCtl.Addr(), map[string]any{"force": req.Force}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": apiText(c, "tor_newnym")})
}

// ─────────────────────────── Örümcek (crawl) ───────────────────────────

func (s *Server) handleCrawlPage(c *gin.Context) {
	s.render(c, http.StatusOK, "crawl.html", gin.H{"ActivePage": "crawl"})
}

func (s *Server) handleCrawlSubmit(c *gin.Context) {
	if s.crawler == nil {
		apiFail(c, http.StatusServiceUnavailable, "crawler_disabled")
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
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if !shared.IsOnionURL(strings.TrimSpace(req.SeedURL)) {
		apiFail(c, http.StatusBadRequest, "crawl_seed_invalid")
		return
	}
	job, err := s.crawler.Submit(crawler.Request{
		SeedURL: req.SeedURL, Depth: req.Depth, MaxPages: req.MaxPages, SameDomain: req.SameDomain, Query: req.Query,
	})
	if err != nil {
		kind := "rejected"
		if errors.Is(err, crawler.ErrBusy) {
			kind = "busy"
		}
		s.audit(c, "crawl.submit", auditURL(req.SeedURL), map[string]any{"depth": req.Depth, "maxPages": req.MaxPages, "error": kind}, false)
		if errors.Is(err, crawler.ErrBusy) {
			apiFail(c, http.StatusServiceUnavailable, "crawler_busy")
			return
		}
		logger.Warn("CRAWL SUBMIT reddedildi: %v", err)
		apiFail(c, http.StatusBadRequest, "crawl_rejected")
		return
	}
	logger.Info("CRAWL SUBMIT: %s (derinlik %d, %d sayfa)", job.SeedURL, job.Depth, job.MaxPages)
	s.audit(c, "crawl.submit", auditURL(job.SeedURL), map[string]any{"jobId": job.ID, "depth": job.Depth, "maxPages": job.MaxPages, "sameDomain": req.SameDomain, "query": req.Query}, true)
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
		apiError(c, http.StatusNotFound, "job_not_found")
		return
	}
	c.JSON(http.StatusOK, job)
}

func (s *Server) handleCrawlCancel(c *gin.Context) {
	if s.crawler == nil {
		apiFail(c, http.StatusServiceUnavailable, "crawler_disabled")
		return
	}
	if err := s.crawler.Cancel(c.Param("id")); err != nil {
		s.audit(c, "crawl.cancel", "job:"+c.Param("id"), map[string]any{"error": "internal"}, false)
		apiFail(c, http.StatusInternalServerError, "cancel_failed")
		return
	}
	s.audit(c, "crawl.cancel", "job:"+c.Param("id"), nil, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}
