package web

import (
	"net/http"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/storage"
)

// Silinen kullanıcının (ör. silme sırasında yarışan bir girişle) kalan
// oturumu admin sayılmamalı: users tablosunda olmayan bir ad yalnız .env
// (bootstrap) hesabıysa admin'dir; aksi halde oturum düşer.
func TestAuthMiddleware_OrphanSessionNotAdmin(t *testing.T) {
	s := newMultiUserServer(t)
	hash, err := hashPassword("bobpass-123")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.db.CreateUser("bob", hash, storage.RoleViewer)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.DeleteUser(bob.ID); err != nil {
		t.Fatal(err)
	}
	// Silmeden sonra yazılmış oturum (girişin bcrypt'i silmeyle çakıştı)
	if err := s.db.CreateSession("orphan-sess", "bob", "orphan-csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	ck := []*http.Cookie{{Name: "session", Value: "orphan-sess"}, {Name: "csrf_token", Value: "orphan-csrf"}}
	if w := do(s, http.MethodGet, "/api/users", nil, ck, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("sahipsiz oturum 401 almalı, alınan %d %s", w.Code, w.Body.String())
	}
	if sess, _ := s.db.GetSession("orphan-sess"); sess != nil {
		t.Fatal("sahipsiz oturum silinmeli")
	}

	// .env (bootstrap) hesabı users tablosunda değilse oturumu admin kalır.
	if _, err := s.creds.Update("root", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.db.CreateSession("root-sess", "root", "root-csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rootCk := []*http.Cookie{{Name: "session", Value: "root-sess"}, {Name: "csrf_token", Value: "root-csrf"}}
	if w := do(s, http.MethodGet, "/api/users", nil, rootCk, nil); w.Code != http.StatusOK {
		t.Fatalf("bootstrap oturumu admin olmalı: %d %s", w.Code, w.Body.String())
	}
}

// Oturum kullanıcısının satırı okunamazsa (ErrNoRows dışı hata) rol en düşüğe
// düşer: geçici bir veritabanı hatası yetki yükseltmesine dönüşmemeli.
func TestAuthMiddleware_UserReadErrorFailsClosed(t *testing.T) {
	s := newMultiUserServer(t)
	ck := loginAs(t, s, "admin", "adminpass1")
	if w := do(s, http.MethodGet, "/api/users", nil, ck, nil); w.Code != http.StatusOK {
		t.Fatalf("admin /api/users: %d", w.Code)
	}
	conn := s.db.GetDBConn()
	if _, err := conn.Exec(`ALTER TABLE users RENAME TO users_x`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`ALTER TABLE users_x RENAME TO users`) })

	w := do(s, http.MethodGet, "/api/users", nil, ck, nil)
	if w.Code != http.StatusForbidden || apiCode(w) != "role_required" {
		t.Fatalf("okuma hatasında admin ucu 403 role_required olmalı, alınan %d %s", w.Code, w.Body.String())
	}
	if w := do(s, http.MethodGet, "/api/stats", nil, ck, nil); w.Code != http.StatusOK {
		t.Fatalf("okuma hatasında viewer ucu açık kalmalı: %d %s", w.Code, w.Body.String())
	}
}

// Parola değişikliğinde users satırı okunamazsa bootstrap (.env) deposuna
// düşülmez: aksi halde yalnız .env parolası değişir, users satırı eski kalırdı.
func TestMePassword_UserReadErrorDoesNotTouchBootstrap(t *testing.T) {
	s := newMultiUserServer(t)
	ck := loginAs(t, s, "admin", "adminpass1")
	conn := s.db.GetDBConn()
	if _, err := conn.Exec(`ALTER TABLE users RENAME TO users_x`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(`ALTER TABLE users_x RENAME TO users`) })

	w := doJSON(s, http.MethodPost, "/api/me/password", `{"current":"adminpass1","new":"adminpass2-new"}`, ck, csrfOf(ck))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("okuma hatasında 500 beklenir, alınan %d %s", w.Code, w.Body.String())
	}
	if !s.creds.Verify("admin", "adminpass1") {
		t.Fatal("bootstrap parolası değişmemeli")
	}
}
