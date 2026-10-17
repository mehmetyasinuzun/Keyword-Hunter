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
	s.render(c, http.StatusOK, "users.html", gin.H{"ActivePage": "users", "role": c.GetString("role")})
}

// userView yönetim listesindeki kullanıcı: 2FA özeti (sır yok) ve politika
// gereği 2FA zorunlu olup olmadığı eklenir.
type userView struct {
	storage.User
	TwoFactor         storage.TwoFactorStatus `json:"twoFactor"`
	TwoFactorRequired bool                    `json:"twoFactorRequired"`
}

// handleUsersList tüm kullanıcıları (2FA durumlarıyla) döndürür.
func (s *Server) handleUsersList(c *gin.Context) {
	users, err := s.db.ListUsers()
	if err != nil {
		respondInternalError(c, "ListUsers", err)
		return
	}
	tfa, err := s.db.ListTwoFactorStatus()
	if err != nil {
		respondInternalError(c, "ListTwoFactorStatus", err)
		return
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, userView{User: u, TwoFactor: tfa[u.ID], TwoFactorRequired: s.twoFactorRequiredFor(u.Role)})
	}
	policy := s.require2FA
	if policy == "" {
		policy = "none"
	}
	c.JSON(http.StatusOK, gin.H{"users": out, "me": c.GetString("username"), "require2fa": policy})
}

// handleUserCreate yeni kullanıcı ekler.
func (s *Server) handleUserCreate(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 || len(req.Username) > 64 || strings.ContainsAny(req.Username, " \t\r\n") {
		apiFail(c, http.StatusBadRequest, "username_invalid")
		return
	}
	if len([]rune(req.Password)) < 8 || len(req.Password) > 256 {
		apiFail(c, http.StatusBadRequest, "password_length")
		return
	}
	if !storage.ValidRole(req.Role) {
		apiFail(c, http.StatusBadRequest, "invalid_role")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		respondInternalError(c, "hashPassword", err)
		return
	}
	u, err := s.db.CreateUser(req.Username, hash, req.Role)
	if err != nil {
		s.audit(c, "user.create", "user:"+req.Username, map[string]any{"role": req.Role, "error": auditErrKind(err, storage.ErrUserExists, "exists")}, false)
		if err == storage.ErrUserExists {
			apiFail(c, http.StatusConflict, "username_taken")
			return
		}
		respondInternalError(c, "CreateUser", err)
		return
	}
	logger.Info("USER CREATED: %s (rol: %s) — %s tarafından", u.Username, u.Role, c.GetString("username"))
	s.audit(c, "user.create", "user:"+u.Username, map[string]any{"role": u.Role, "userId": u.ID}, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "user": u})
}

// handleUserUpdate rol/parola/aktiflik günceller.
func (s *Server) handleUserUpdate(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_id")
		return
	}
	target, err := s.db.GetUserByID(id)
	if err != nil {
		apiFail(c, http.StatusNotFound, "user_not_found")
		return
	}
	var req struct {
		Password *string `json:"password"`
		Role     *string `json:"role"`
		Enabled  *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}

	// Son admini koruma: rol düşürme veya pasifleştirme engellenir.
	if target.Role == storage.RoleAdmin {
		demoting := req.Role != nil && *req.Role != storage.RoleAdmin
		disabling := req.Enabled != nil && !*req.Enabled
		if demoting || disabling {
			if n, _ := s.db.CountAdmins(); n <= 1 {
				s.audit(c, "user.update", "user:"+target.Username, map[string]any{"error": "last_admin", "demote": demoting, "disable": disabling}, false)
				apiFail(c, http.StatusBadRequest, "last_admin_demote")
				return
			}
		}
	}

	if req.Password != nil && *req.Password != "" {
		if len([]rune(*req.Password)) < 8 || len(*req.Password) > 256 {
			apiFail(c, http.StatusBadRequest, "password_length")
			return
		}
		hash, err := hashPassword(*req.Password)
		if err != nil {
			respondInternalError(c, "hashPassword", err)
			return
		}
		if err := s.db.UpdateUserPassword(id, hash); err != nil {
			s.audit(c, "user.password.reset", "user:"+target.Username, map[string]any{"error": "internal"}, false)
			respondInternalError(c, "UpdateUserPassword", err)
			return
		}
		// Parola değişince o kullanıcının diğer oturumları düşer. Bu başarısız
		// olursa eski oturumlar canlı kalır — sessizce yutulmamalı.
		revoked := int64(0)
		if res, err := s.db.GetDBConn().Exec(`DELETE FROM sessions WHERE username = ? COLLATE NOCASE`, target.Username); err != nil {
			logger.Error("SESSION REVOKE FAILED (parola sıfırlama, kullanıcı=%s): %v", target.Username, err)
		} else {
			revoked, _ = res.RowsAffected()
		}
		// Admin sıfırlaması bir olay müdahalesi adımıdır: ele geçirilmiş bir
		// oturumla basılmış API token'ları da sıfırlamadan sağ çıkmamalı.
		// Eski parolayla başlamış, ikinci adımı bekleyen girişler de düşer.
		s.pendingLogins.removeUser(target.ID)
		tokensRevoked, err := s.db.RevokeUserAPITokens(target.ID)
		if err != nil {
			logger.Error("API TOKEN REVOKE FAILED (parola sıfırlama, kullanıcı=%s): %v", target.Username, err)
		}
		s.audit(c, "user.password.reset", "user:"+target.Username, map[string]any{"userId": target.ID, "sessionsRevoked": revoked, "tokensRevoked": tokensRevoked}, true)
	}
	changes := map[string]any{"userId": target.ID}
	if req.Role != nil {
		if !storage.ValidRole(*req.Role) {
			apiFail(c, http.StatusBadRequest, "invalid_role")
			return
		}
		if err := s.db.UpdateUserRole(id, *req.Role); err != nil {
			changes["requestedRole"] = *req.Role
			changes["error"] = "internal"
			s.audit(c, "user.update", "user:"+target.Username, changes, false)
			respondInternalError(c, "UpdateUserRole", err)
			return
		}
		changes["role"] = *req.Role
		changes["prevRole"] = target.Role
	}
	if req.Enabled != nil {
		if err := s.db.SetUserEnabled(id, *req.Enabled); err != nil {
			// Rol değişikliği uygulanmış olabilir: başarısız olay onu da taşır.
			changes["error"] = "internal"
			s.audit(c, "user.update", "user:"+target.Username, changes, false)
			respondInternalError(c, "SetUserEnabled", err)
			return
		}
		changes["enabled"] = *req.Enabled
		changes["prevEnabled"] = target.Enabled
		if !*req.Enabled {
			s.pendingLogins.removeUser(target.ID)
		}
	}
	if req.Role != nil || req.Enabled != nil {
		s.audit(c, "user.update", "user:"+target.Username, changes, true)
	}
	logger.Info("USER UPDATED: %s — %s tarafından", target.Username, c.GetString("username"))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleUserDelete kullanıcıyı siler (kendini veya son admini silemez).
func (s *Server) handleUserDelete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_id")
		return
	}
	target, err := s.db.GetUserByID(id)
	if err != nil {
		apiFail(c, http.StatusNotFound, "user_not_found")
		return
	}
	if strings.EqualFold(target.Username, c.GetString("username")) {
		s.audit(c, "user.delete", "user:"+target.Username, map[string]any{"error": "self"}, false)
		apiFail(c, http.StatusBadRequest, "delete_self")
		return
	}
	if target.Role == storage.RoleAdmin {
		if n, _ := s.db.CountAdmins(); n <= 1 {
			s.audit(c, "user.delete", "user:"+target.Username, map[string]any{"error": "last_admin"}, false)
			apiFail(c, http.StatusBadRequest, "last_admin_delete")
			return
		}
	}
	if err := s.db.DeleteUser(id); err != nil {
		s.audit(c, "user.delete", "user:"+target.Username, map[string]any{"error": "internal"}, false)
		respondInternalError(c, "DeleteUser", err)
		return
	}
	s.pendingLogins.removeUser(target.ID)
	logger.Info("USER DELETED: %s — %s tarafından", target.Username, c.GetString("username"))
	s.audit(c, "user.delete", "user:"+target.Username, map[string]any{"userId": target.ID, "role": target.Role}, true)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// auditErrKind denetim ayrıntısı için hatayı kaba bir türe indirger: ham hata
// metni (SQL ayrıntısı, dosya yolu) kayda taşınmaz.
func auditErrKind(err, known error, kind string) string {
	if err == known {
		return kind
	}
	return "internal"
}

// handleWhoami mevcut kimliğin kullanıcı adı, etkin rolü ve kimlik yöntemini
// döndürür (UI yetki gizleme; betikler token'ı bununla sınar).
func (s *Server) handleWhoami(c *gin.Context) {
	out := gin.H{
		"username":   c.GetString("username"),
		"role":       c.GetString("role"),
		"authMethod": c.GetString("authMethod"),
	}
	if scope := c.GetString("tokenScope"); scope != "" {
		out["tokenScope"] = scope
		out["tokenPrefix"] = c.GetString("tokenPrefix")
	}
	c.JSON(http.StatusOK, out)
}

// handleMyPassword kullanıcının kendi parolasını değiştirmesi (her rol).
func (s *Server) handleMyPassword(c *gin.Context) {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
		// RevokeTokens true ise kullanıcının tüm API token'ları da iptal edilir.
		// Varsayılan kapalıdır: rutin parola değişikliği entegrasyonları
		// bozmasın; ele geçirme şüphesinde kullanıcı bunu açıkça seçer.
		RevokeTokens bool `json:"revokeTokens"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		apiFail(c, http.StatusBadRequest, "invalid_request")
		return
	}
	if len([]rune(req.New)) < 8 || len(req.New) > 256 {
		apiFail(c, http.StatusBadRequest, "new_password_length")
		return
	}
	username := c.GetString("username")
	u, err := s.db.GetUserByUsername(username)
	if err != nil || u == nil {
		// bootstrap kimlik deposu (users tablosunda yoksa)
		if s.creds == nil || !s.creds.Verify(username, req.Current) {
			s.audit(c, "me.password.change", "user:"+username, map[string]any{"error": "wrong_current"}, false)
			apiFail(c, http.StatusForbidden, "wrong_password")
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
				s.audit(c, "me.password.change", "user:"+username, map[string]any{"error": "env_persist"}, false)
				apiFail(c, http.StatusInternalServerError, "password_env_persist")
				return
			}
		}
	} else {
		if !verifyBcrypt(u.PasswordHash, req.Current) {
			s.audit(c, "me.password.change", "user:"+username, map[string]any{"error": "wrong_current"}, false)
			apiFail(c, http.StatusForbidden, "wrong_password")
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
		// Eski (belki sızmış) parolayla başlatılmış ikinci adım da düşer.
		if s.pendingLogins != nil {
			s.pendingLogins.removeUser(u.ID)
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
	tokensRevoked := int64(0)
	if req.RevokeTokens && u != nil {
		n, err := s.db.RevokeUserAPITokens(u.ID)
		if err != nil {
			// Parola değişti; token iptali başarısızsa kullanıcı bunu bilmeli.
			logger.Error("API TOKEN REVOKE FAILED (öz parola, kullanıcı=%s): %v", username, err)
			s.audit(c, "me.password.change", "user:"+username, map[string]any{"error": "token_revoke"}, false)
			apiFail(c, http.StatusInternalServerError, "password_tokens_revoke_failed")
			return
		}
		tokensRevoked = n
	}
	logger.Info("SELF PASSWORD CHANGE: %s", username)
	var details map[string]any
	if req.RevokeTokens {
		details = map[string]any{"tokensRevoked": tokensRevoked}
	}
	s.audit(c, "me.password.change", "user:"+username, details, true)
	c.JSON(http.StatusOK, gin.H{"success": true, "tokensRevoked": tokensRevoked})
}
