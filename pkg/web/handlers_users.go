package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/storage"
)

// handleUsersPage kullanıcı yönetimi sayfası (yalnız admin).
func (s *Server) handleUsersPage(c *gin.Context) {
	c.HTML(http.StatusOK, "users.html", gin.H{"ActivePage": "users", "role": c.GetString("role")})
}

// handleUsersList tüm kullanıcıları döndürür.
func (s *Server) handleUsersList(c *gin.Context) {
	users, err := s.db.ListUsers()
	if err != nil {
		respondInternalError(c, "ListUsers", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"users": users, "me": c.GetString("username")})
}

// handleUserCreate yeni kullanıcı ekler.
func (s *Server) handleUserCreate(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Username) > 64 || strings.ContainsAny(req.Username, " \t\r\n") {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Kullanıcı adı 3-64 karakter, boşluksuz olmalı"})
		return
	}
	if len([]rune(req.Password)) < 8 || len(req.Password) > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Parola 8-256 karakter olmalı"})
		return
	}
	if !storage.ValidRole(req.Role) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz rol"})
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		respondInternalError(c, "hashPassword", err)
		return
	}
	u, err := s.db.CreateUser(req.Username, hash, req.Role)
	if err != nil {
		if err == storage.ErrUserExists {
			c.JSON(http.StatusConflict, gin.H{"success": false, "error": "Bu kullanıcı adı zaten var"})
			return
		}
		respondInternalError(c, "CreateUser", err)
		return
	}
	logger.Info("USER CREATED: %s (rol: %s) — %s tarafından", u.Username, u.Role, c.GetString("username"))
	c.JSON(http.StatusOK, gin.H{"success": true, "user": u})
}

// handleUserUpdate rol/parola/aktiflik günceller.
func (s *Server) handleUserUpdate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}
	target, err := s.db.GetUserByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Kullanıcı bulunamadı"})
		return
	}
	var req struct {
		Password *string `json:"password"`
		Role     *string `json:"role"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}

	// Son admini koruma: rol düşürme veya pasifleştirme engellenir.
	if target.Role == storage.RoleAdmin {
		demoting := req.Role != nil && *req.Role != storage.RoleAdmin
		disabling := req.Enabled != nil && !*req.Enabled
		if demoting || disabling {
			if n, _ := s.db.CountAdmins(); n <= 1 {
				c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Son etkin admin düşürülemez/pasifleştirilemez"})
				return
			}
		}
	}

	if req.Password != nil && *req.Password != "" {
		if len([]rune(*req.Password)) < 8 || len(*req.Password) > 256 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Parola 8-256 karakter olmalı"})
			return
		}
		hash, err := hashPassword(*req.Password)
		if err != nil {
			respondInternalError(c, "hashPassword", err)
			return
		}
		if err := s.db.UpdateUserPassword(id, hash); err != nil {
			respondInternalError(c, "UpdateUserPassword", err)
			return
		}
		// Parola değişince o kullanıcının diğer oturumları düşer. Bu başarısız
		// olursa eski oturumlar canlı kalır — sessizce yutulmamalı.
		if _, err := s.db.GetDBConn().Exec(`DELETE FROM sessions WHERE username = ? COLLATE NOCASE`, target.Username); err != nil {
			logger.Error("SESSION REVOKE FAILED (parola sıfırlama, kullanıcı=%s): %v", target.Username, err)
		}
	}
	if req.Role != nil {
		if !storage.ValidRole(*req.Role) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz rol"})
			return
		}
		if err := s.db.UpdateUserRole(id, *req.Role); err != nil {
			respondInternalError(c, "UpdateUserRole", err)
			return
		}
	}
	if req.Enabled != nil {
		if err := s.db.SetUserEnabled(id, *req.Enabled); err != nil {
			respondInternalError(c, "SetUserEnabled", err)
			return
		}
	}
	logger.Info("USER UPDATED: %s — %s tarafından", target.Username, c.GetString("username"))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleUserDelete kullanıcıyı siler (kendini veya son admini silemez).
func (s *Server) handleUserDelete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz ID"})
		return
	}
	target, err := s.db.GetUserByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Kullanıcı bulunamadı"})
		return
	}
	if strings.EqualFold(target.Username, c.GetString("username")) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Kendinizi silemezsiniz"})
		return
	}
	if target.Role == storage.RoleAdmin {
		if n, _ := s.db.CountAdmins(); n <= 1 {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Son admin silinemez"})
			return
		}
	}
	if err := s.db.DeleteUser(id); err != nil {
		respondInternalError(c, "DeleteUser", err)
		return
	}
	logger.Info("USER DELETED: %s — %s tarafından", target.Username, c.GetString("username"))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleWhoami mevcut oturumun kullanıcı adı ve rolünü döndürür (UI yetki gizleme).
func (s *Server) handleWhoami(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"username": c.GetString("username"),
		"role":     c.GetString("role"),
	})
}

// handleMyPassword kullanıcının kendi parolasını değiştirmesi (her rol).
func (s *Server) handleMyPassword(c *gin.Context) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Geçersiz istek"})
		return
	}
	if len([]rune(req.New)) < 8 || len(req.New) > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Yeni parola 8-256 karakter olmalı"})
		return
	}
	username := c.GetString("username")
	u, err := s.db.GetUserByUsername(username)
	if err != nil || u == nil {
		// bootstrap kimlik deposu (users tablosunda yoksa)
		if s.creds == nil || !s.creds.Verify(username, req.Current) {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Mevcut parola yanlış"})
			return
		}
		// Update hatası yutulursa boş hash .env'e yazılıp admin kilitlenebilir.
		hash, err := s.creds.Update("", req.New)
		if err == nil && hash == "" {
			err = fmt.Errorf("boş hash üretildi")
		}
		if err != nil {
			respondInternalError(c, "creds.Update", err)
			return
		}
		if s.envStore != nil {
			// .env'e yazılamazsa yeni parola yalnız bellekte kalır; yeniden
			// başlatmada ESKİ parola geri gelir. Kullanıcı bunu bilmeli.
			if err := s.envStore.Update(map[string]string{"ADMIN_PASS_HASH": hash, "ADMIN_PASS": ""}); err != nil {
				logger.Error("ENV PERSIST FAILED (admin parola): %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"success": false,
					"error": "Parola bu oturum için değişti ancak .env dosyasına kalıcı yazılamadı; yeniden başlatmada eski parola geri gelir. Dosya izinlerini kontrol edin."})
				return
			}
		}
	} else {
		if !verifyBcrypt(u.PasswordHash, req.Current) {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "Mevcut parola yanlış"})
			return
		}
		hash, err := hashPassword(req.New)
		if err != nil {
			respondInternalError(c, "hashPassword", err)
			return
		}
		if err := s.db.UpdateUserPassword(u.ID, hash); err != nil {
			respondInternalError(c, "UpdateUserPassword", err)
			return
		}
	}
	// Diğer oturumları düşür, mevcut oturumu koru
	// comma-ok şart: tip uyuşmazsa "" ile devam etmek `id <> ""` üzerinden çağıranın
	// KENDİ oturumunu da düşürürdü; bu yüzden yalnız gerçek bir kimlikle çalış.
	if sid, ok := c.Get("sessionID"); ok {
		if sidStr, ok := sid.(string); ok && sidStr != "" {
			if _, err := s.db.GetDBConn().Exec(`DELETE FROM sessions WHERE username = ? COLLATE NOCASE AND id <> ?`, username, sidStr); err != nil {
				logger.Error("SESSION REVOKE FAILED (öz parola, kullanıcı=%s): %v", username, err)
			}
		}
	}
	logger.Info("SELF PASSWORD CHANGE: %s", username)
	c.JSON(http.StatusOK, gin.H{"success": true})
}
