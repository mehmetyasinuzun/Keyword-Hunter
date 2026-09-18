package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// handleArtifactsForResult bir bulgunun çıkarılmış IOC'lerini döndürür.
func (s *Server) handleArtifactsForResult(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Geçersiz ID"})
		return
	}
	items, err := s.db.GetArtifacts(id)
	if err != nil {
		respondInternalError(c, "GetArtifacts", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"resultId": id, "count": len(items), "artifacts": items})
}

// handleArtifactSearch IOC pivotu: tür ve/veya değer parçasıyla arama.
func (s *Server) handleArtifactSearch(c *gin.Context) {
	typ := strings.TrimSpace(c.Query("type"))
	value := strings.TrimSpace(c.Query("value"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if typ == "" && len(value) < 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "type veya en az 3 karakterlik value gerekli"})
		return
	}
	items, err := s.db.SearchArtifacts(typ, value, limit)
	if err != nil {
		respondInternalError(c, "SearchArtifacts", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": len(items), "artifacts": items})
}

// handleArtifactStats tür bazlı toplamlar (dashboard/analytics).
func (s *Server) handleArtifactStats(c *gin.Context) {
	stats, err := s.db.ArtifactCounts()
	if err != nil {
		respondInternalError(c, "ArtifactCounts", err)
		return
	}
	total := 0
	for _, st := range stats {
		total += st.Count
	}
	c.JSON(http.StatusOK, gin.H{"total": total, "types": stats})
}

// handleArtifactCounts verilen ID'ler için artifact sayılarını tek istekte döndürür
// (liste sayfası satır başına istek atarak hız limitine takılıyordu).
func (s *Server) handleArtifactCounts(c *gin.Context) {
	raw := strings.Split(c.Query("ids"), ",")
	ids := make([]int64, 0, len(raw))
	for _, r := range raw {
		if id, err := strconv.ParseInt(strings.TrimSpace(r), 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
		if len(ids) >= 500 {
			break
		}
	}
	counts, err := s.db.ArtifactCountByResult(ids)
	if err != nil {
		respondInternalError(c, "ArtifactCountByResult", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"counts": counts})
}
