package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"keywordhunter-mvp/pkg/storage"
)

// bearer Authorization: Bearer başlığıyla (ve isteğe bağlı çerez/CSRF ile) istek yapar.
func bearer(s *Server, method, path, body, token string, cookies []*http.Cookie, csrf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

// mkTokenUser users tablosuna bir hesap ekler ve oturum çerezlerini döndürür.
func mkTokenUser(t *testing.T, s *Server, name, pass, role string) []*http.Cookie {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.CreateUser(name, string(hash), role); err != nil {
		t.Fatal(err)
	}
	return loginAs(t, s, name, pass)
}

type createdToken struct {
	Token string `json:"token"`
	Meta  struct {
		ID       int64  `json:"id"`
		Prefix   string `json:"prefix"`
		Scope    string `json:"scope"`
		Status   string `json:"status"`
		Username string `json:"username"`
	} `json:"meta"`
}

// createToken oturumla (CSRF'li) token oluşturur ve yanıtı döndürür.
func createToken(t *testing.T, s *Server, ck []*http.Cookie, name, scope string, days int) createdToken {
	t.Helper()
	body := fmt.Sprintf(`{"name":%q,"scope":%q,"expiresInDays":%d}`, name, scope, days)
	w := doJSON(s, http.MethodPost, "/api/tokens", body, ck, csrfOf(ck))
	if w.Code != http.StatusCreated {
		t.Fatalf("token oluşturma: %d %s", w.Code, w.Body.String())
	}
	var out createdToken
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Token, storage.APITokenPrefix) || len(out.Token) < 40 || out.Meta.Prefix != out.Token[:storage.APITokenDisplayLen] {
		t.Fatalf("beklenmeyen token biçimi: %q / %q", out.Token, out.Meta.Prefix)
	}
	if out.Meta.Status != storage.TokenStatusActive || out.Meta.Scope != scope {
		t.Fatalf("meta: %+v", out.Meta)
	}
	return out
}

func assert401(t *testing.T, w *httptest.ResponseRecorder, ctx string) {
	t.Helper()
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("%s: %d (401 bekleniyordu) %s", ctx, w.Code, w.Body.String())
	}
	if !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("%s: WWW-Authenticate: Bearer başlığı eksik: %q", ctx, w.Header().Get("WWW-Authenticate"))
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == nil {
		t.Fatalf("%s: JSON {\"error\"} bekleniyordu: %s", ctx, w.Body.String())
	}
}

// Oluştur → kullan → iptal et → 401; son kullanım yazılır; denetim kaydı token'ı içermez.
func TestAPIToken_CreateUseRevoke(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	tok := createToken(t, s, admin, "Splunk SIEM", storage.TokenScopeRead, 30)

	before, err := s.db.GetAPIToken(tok.Meta.ID)
	if err != nil || before.LastUsedAt != nil {
		t.Fatalf("yeni token kullanılmamış olmalı: %+v %v", before, err)
	}
	if before.ExpiresAt == nil || time.Until(*before.ExpiresAt) < 29*24*time.Hour {
		t.Fatalf("30 günlük bitiş beklenirdi: %v", before.ExpiresAt)
	}

	w := bearer(s, http.MethodGet, "/api/results", "", tok.Token, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("token ile /api/results: %d %s", w.Code, w.Body.String())
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("token yanıtı no-store olmalı: %q", cc)
	}

	// Kimlik: kullanıcı adı, okuma kapsamı viewer'a indirger, yöntem token
	w = bearer(s, http.MethodGet, "/api/whoami", "", tok.Token, nil, "")
	var who map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &who)
	if w.Code != http.StatusOK || who["username"] != "admin" || who["role"] != storage.RoleViewer ||
		who["authMethod"] != storage.AuthMethodToken || who["tokenScope"] != "read" || who["tokenPrefix"] != tok.Meta.Prefix {
		t.Fatalf("whoami: %d %v", w.Code, who)
	}

	// Son kullanım (zaman + IP) yazıldı
	after, _ := s.db.GetAPIToken(tok.Meta.ID)
	if after.LastUsedAt == nil || after.LastUsedIP == "" {
		t.Fatalf("last_used güncellenmedi: %+v", after)
	}

	// İptal (oturumla) → sonraki istek 401
	if w := doJSON(s, http.MethodPost, fmt.Sprintf("/api/tokens/%d/revoke", tok.Meta.ID), `{}`, admin, csrfOf(admin)); w.Code != http.StatusOK {
		t.Fatalf("iptal: %d %s", w.Code, w.Body.String())
	}
	assert401(t, bearer(s, http.MethodGet, "/api/results", "", tok.Token, nil, ""), "iptal edilmiş token")
	// İkinci iptal idempotent
	w = doJSON(s, http.MethodPost, fmt.Sprintf("/api/tokens/%d/revoke", tok.Meta.ID), `{}`, admin, csrfOf(admin))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "alreadyRevoked") {
		t.Fatalf("ikinci iptal: %d %s", w.Code, w.Body.String())
	}

	c := lastAudit(t, s, "token.create")
	if !c.Success || c.Actor != "admin" || !strings.Contains(c.Details, tok.Meta.Prefix) || !strings.Contains(c.Details, `"scope":"read"`) {
		t.Fatalf("token.create olayı: %+v", c)
	}
	r := lastAudit(t, s, "token.revoke")
	if !r.Success || !strings.Contains(r.Details, tok.Meta.Prefix) {
		t.Fatalf("token.revoke olayı: %+v", r)
	}
	if n := countAudit(t, s, "token.revoke"); n != 1 {
		t.Fatalf("idempotent iptal yeni olay yazmamalı: %d", n)
	}
	f := lastAudit(t, s, "auth.token.failure")
	if f.Success || !strings.Contains(f.Details, "revoked") || f.Target != "user:admin" {
		t.Fatalf("reddedilen token olayı: %+v", f)
	}
	assertNoSecretsInAudit(t, s, tok.Token, storage.HashAPIToken(tok.Token))
}

// Düz metin token hiçbir sütunda saklanmaz; yalnız SHA-256 özeti.
func TestAPIToken_PlaintextNeverStored(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	tok := createToken(t, s, admin, "store-check", storage.TokenScopeWrite, 0)

	rows, err := s.db.GetDBConn().Query(`SELECT * FROM api_tokens`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	n := 0
	for rows.Next() {
		n++
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			if strings.Contains(fmt.Sprint(v), tok.Token) {
				t.Fatalf("düz metin token %s sütununda saklanmış", cols[i])
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("1 satır bekleniyordu: %d", n)
	}
	var hash string
	if err := s.db.GetDBConn().QueryRow(`SELECT token_hash FROM api_tokens WHERE id = ?`, tok.Meta.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != storage.HashAPIToken(tok.Token) || len(hash) != 64 {
		t.Fatalf("token_hash SHA-256 hex olmalı: %q", hash)
	}
	// Süresiz token: expires_at NULL
	got, _ := s.db.GetAPIToken(tok.Meta.ID)
	if got.ExpiresAt != nil {
		t.Fatalf("expiresInDays=0 süresiz olmalı: %v", got.ExpiresAt)
	}
	// Listeleme yanıtı da özet/düz metin taşımaz
	w := do(s, http.MethodGet, "/api/tokens", nil, admin, nil)
	if strings.Contains(w.Body.String(), hash) || strings.Contains(w.Body.String(), tok.Token) || strings.Contains(w.Body.String(), "tokenHash") {
		t.Fatalf("liste yanıtı sır sızdırıyor: %s", w.Body.String())
	}
}

func TestAPIToken_ExpiredRejected(t *testing.T) {
	s := newMultiUserServer(t)
	admin, _ := s.db.GetUserByUsername("admin")
	past := time.Now().Add(-time.Minute)
	plain, _, err := s.db.CreateAPIToken(admin.ID, admin.Username, "old", storage.TokenScopeRead, &past)
	if err != nil {
		t.Fatal(err)
	}
	w := bearer(s, http.MethodGet, "/api/results", "", plain, nil, "")
	assert401(t, w, "süresi dolmuş token")
	if apiCode(w) != "token_expired" {
		t.Fatalf("süre dolumu mesajı bekleniyordu: %s", w.Body.String())
	}
}

func TestAPIToken_DisabledOrDeletedUserRejected(t *testing.T) {
	s := newMultiUserServer(t)
	ck := mkTokenUser(t, s, "soar", "soar-pass-123", storage.RoleAnalyst)
	tok := createToken(t, s, ck, "soar playbook", storage.TokenScopeWrite, 90)
	if w := bearer(s, http.MethodGet, "/api/stats", "", tok.Token, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("etkin kullanıcı token'ı: %d", w.Code)
	}

	id := mustUserID(t, s, "soar")
	if err := s.db.SetUserEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	assert401(t, bearer(s, http.MethodGet, "/api/stats", "", tok.Token, nil, ""), "pasif kullanıcı")
	if err := s.db.SetUserEnabled(id, true); err != nil {
		t.Fatal(err)
	}
	if w := bearer(s, http.MethodGet, "/api/stats", "", tok.Token, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("yeniden etkinleştirilen kullanıcı: %d", w.Code)
	}

	// Silinen kullanıcı: token'lar da silinir; aynı adla açılan yeni hesap devralmaz
	if err := s.db.DeleteUser(id); err != nil {
		t.Fatal(err)
	}
	assert401(t, bearer(s, http.MethodGet, "/api/stats", "", tok.Token, nil, ""), "silinmiş kullanıcı")
	mkTokenUser(t, s, "soar", "soar-pass-456", storage.RoleAnalyst)
	assert401(t, bearer(s, http.MethodGet, "/api/stats", "", tok.Token, nil, ""), "aynı adla yeniden açılan hesap")
}

// Token aynı ada sahip ama farklı kimlikli bir hesaba geçmez (user_id bağı),
// tablo dışarıdan temizlenmemiş olsa bile.
func TestAPIToken_BoundToUserID(t *testing.T) {
	s := newMultiUserServer(t)
	mkTokenUser(t, s, "bob", "bob-pass-123", storage.RoleAnalyst)
	bob, _ := s.db.GetUserByUsername("bob")
	plain, _, err := s.db.CreateAPIToken(bob.ID+1000, "bob", "forged", storage.TokenScopeWrite, nil)
	if err != nil {
		t.Fatal(err)
	}
	assert401(t, bearer(s, http.MethodGet, "/api/stats", "", plain, nil, ""), "farklı user_id")
}

func TestAPIToken_ScopesAndCSRF(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	read := createToken(t, s, admin, "reader", storage.TokenScopeRead, 7)
	write := createToken(t, s, admin, "writer", storage.TokenScopeWrite, 7)
	const del = `{"ids":[987654]}`

	// Okuma kapsamı: yazma 403 (admin olsa bile)
	w := bearer(s, http.MethodPost, "/api/results/delete", del, read.Token, nil, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "read") {
		t.Fatalf("read token POST: %d %s", w.Code, w.Body.String())
	}
	d := lastAudit(t, s, "access.denied")
	if d.AuthMethod != storage.AuthMethodToken || !strings.Contains(d.Details, `"tokenPrefix":"`+read.Meta.Prefix+`"`) || !strings.Contains(d.Details, "token_scope") {
		t.Fatalf("token reddi denetim olayı: %+v", d)
	}

	// Yazma kapsamı, çerezsiz: CSRF olmadan çalışır ve token önekiyle kaydedilir
	w = bearer(s, http.MethodPost, "/api/results/delete", del, write.Token, nil, "")
	if w.Code != http.StatusOK {
		t.Fatalf("write token CSRF'siz POST: %d %s", w.Code, w.Body.String())
	}
	e := lastAudit(t, s, "results.delete")
	if e.AuthMethod != storage.AuthMethodToken || e.Actor != "admin" || e.Role != storage.RoleAdmin ||
		!strings.Contains(e.Details, `"tokenPrefix":"`+write.Meta.Prefix+`"`) {
		t.Fatalf("token ile yazma denetim olayı: %+v", e)
	}

	// Çerez + Bearer birlikte: oturum yolu → CSRF zorunlu
	if w := bearer(s, http.MethodPost, "/api/results/delete", del, write.Token, admin, ""); w.Code != http.StatusForbidden {
		t.Fatalf("çerez+bearer CSRF'siz POST reddedilmeli: %d", w.Code)
	}
	if w := bearer(s, http.MethodPost, "/api/results/delete", del, write.Token, admin, csrfOf(admin)); w.Code != http.StatusOK {
		t.Fatalf("çerez+bearer+CSRF: %d", w.Code)
	}
	// Geçersiz oturum çerezi + geçerli token: yine oturum yolu (token'a düşmez)
	stale := []*http.Cookie{{Name: "session", Value: "yok-boyle-oturum"}}
	if w := bearer(s, http.MethodGet, "/api/stats", "", write.Token, stale, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("geçersiz çerez + token: %d (401 bekleniyordu)", w.Code)
	}

	// Analist rolündeki kullanıcının write token'ı admin uçlarına çıkamaz
	an := mkTokenUser(t, s, "ana1", "ana1-pass-123", storage.RoleAnalyst)
	anTok := createToken(t, s, an, "an-writer", storage.TokenScopeWrite, 7)
	if w := bearer(s, http.MethodGet, "/api/audit", "", anTok.Token, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("analist token'ı admin ucu: %d", w.Code)
	}
	// Rol düşürülürse token anında viewer olur
	if err := s.db.UpdateUserRole(mustUserID(t, s, "ana1"), storage.RoleViewer); err != nil {
		t.Fatal(err)
	}
	if w := bearer(s, http.MethodPost, "/api/results/delete", del, anTok.Token, nil, ""); w.Code != http.StatusForbidden {
		t.Fatalf("rolü düşürülen kullanıcının token'ı yazabildi: %d", w.Code)
	}
}

// Bearer yalnız /api/ altında; HTML sayfaları, SSE ve sorgu dizgisi token kabul etmez.
func TestAPIToken_OnlyAPIAndHeader(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	tok := createToken(t, s, admin, "scope-check", storage.TokenScopeWrite, 7)

	for _, page := range []string{"/dashboard", "/account", "/users", "/events"} {
		w := bearer(s, http.MethodGet, page, "", tok.Token, nil, "")
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
			t.Fatalf("bearer ile %s: %d %s (giriş yönlendirmesi bekleniyordu)", page, w.Code, w.Header().Get("Location"))
		}
	}
	// Sorgu dizgisi / form alanı token taşımaz
	if w := do(s, http.MethodGet, "/api/results?token="+tok.Token, nil, nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("?token= kabul edilmemeli: %d", w.Code)
	}
	if w := do(s, http.MethodGet, "/api/results?access_token="+tok.Token, nil, nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("?access_token= kabul edilmemeli: %d", w.Code)
	}
	// Bearer olmayan şema ve bozuk token
	if w := do(s, http.MethodGet, "/api/results", nil, nil, map[string]string{"Authorization": "Basic " + tok.Token}); w.Code != http.StatusUnauthorized {
		t.Fatalf("Basic şema: %d", w.Code)
	}
	for _, bad := range []string{"kh_", "kh_abc$def", "xx_" + tok.Token[3:], strings.Repeat("k", 300), tok.Token + "x"} {
		assert401(t, bearer(s, http.MethodGet, "/api/results", "", bad, nil, ""), "bozuk token "+bad[:3])
	}
	// Şema büyük/küçük harf duyarsız
	if w := do(s, http.MethodGet, "/api/stats", nil, nil, map[string]string{"Authorization": "bearer " + tok.Token}); w.Code != http.StatusOK {
		t.Fatalf("küçük harf bearer: %d", w.Code)
	}
}

// Token hesap/güvenlik yönetimi uçlarını çağıramaz (yazma kapsamlı admin token'ı bile).
func TestAPIToken_CannotManageAccount(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	tok := createToken(t, s, admin, "admin-writer", storage.TokenScopeWrite, 7)

	cases := []struct{ method, path, body string }{
		{http.MethodGet, "/api/tokens", ""},
		{http.MethodGet, "/api/tokens?all=1", ""},
		{http.MethodPost, "/api/tokens", `{"name":"x","scope":"write","expiresInDays":0}`},
		{http.MethodPost, fmt.Sprintf("/api/tokens/%d/revoke", tok.Meta.ID), `{}`},
		{http.MethodPost, "/api/me/password", `{"current":"adminpass1","new":"hijacked-pass"}`},
		{http.MethodGet, "/api/users", ""},
		{http.MethodPost, "/api/users", `{"username":"evil","password":"evil-pass-1","role":"admin"}`},
		{http.MethodGet, "/api/settings/env", ""},
		{http.MethodPost, "/api/settings/env", `{}`},
		{http.MethodGet, "/api/alert-config", ""},
		{http.MethodPost, "/api/alert-config", `{"enabled":false}`},
		{http.MethodGet, "/api/site-profiles", ""},
	}
	for _, tc := range cases {
		w := bearer(s, tc.method, tc.path, tc.body, tok.Token, nil, "")
		if w.Code != http.StatusForbidden || apiCode(w) != "session_required" {
			t.Errorf("%s %s token ile: %d %s (403 bekleniyordu)", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	// Hiçbiri yan etki bırakmadı
	if _, err := s.db.GetUserByUsername("evil"); err == nil {
		t.Fatal("token ile kullanıcı oluşturuldu")
	}
	loginAs(t, s, "admin", "adminpass1") // parola değişmedi
	if got, _ := s.db.GetAPIToken(tok.Meta.ID); got.RevokedAt != nil {
		t.Fatal("token kendini iptal edebildi")
	}
	if list, _ := s.db.ListAPITokens("admin"); len(list) != 1 {
		t.Fatalf("token yeni token basabildi: %d", len(list))
	}
	if d := lastAudit(t, s, "access.denied"); !strings.Contains(d.Details, "token_session_required") || d.AuthMethod != storage.AuthMethodToken {
		t.Fatalf("token reddi denetim olayı: %+v", d)
	}
}

func TestAPIToken_ListAllAndRevokeOwnership(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	an := mkTokenUser(t, s, "ana2", "ana2-pass-123", storage.RoleAnalyst)
	other := mkTokenUser(t, s, "ana3", "ana3-pass-123", storage.RoleAnalyst)
	adminTok := createToken(t, s, admin, "admin-tok", storage.TokenScopeRead, 7)
	anTok := createToken(t, s, an, "an-tok", storage.TokenScopeRead, 7)

	// ?all=1 yalnız admin
	if w := do(s, http.MethodGet, "/api/tokens?all=1", nil, an, nil); w.Code != http.StatusForbidden {
		t.Fatalf("analist ?all=1: %d", w.Code)
	}
	var list struct {
		Tokens []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"tokens"`
	}
	w := do(s, http.MethodGet, "/api/tokens", nil, an, nil)
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != http.StatusOK || len(list.Tokens) != 1 || list.Tokens[0].ID != anTok.Meta.ID {
		t.Fatalf("analist yalnız kendi token'ını görmeli: %d %s", w.Code, w.Body.String())
	}
	w = do(s, http.MethodGet, "/api/tokens?all=1", nil, admin, nil)
	list.Tokens = nil
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != http.StatusOK || len(list.Tokens) != 2 {
		t.Fatalf("admin ?all=1 herkesi görmeli: %d %s", w.Code, w.Body.String())
	}

	// Başkasının token'ı: sahip olmayan analist 404 (varlık sızdırılmaz), admin iptal edebilir
	if w := doJSON(s, http.MethodPost, fmt.Sprintf("/api/tokens/%d/revoke", adminTok.Meta.ID), `{}`, other, csrfOf(other)); w.Code != http.StatusNotFound {
		t.Fatalf("başkasının token'ını iptal: %d", w.Code)
	}
	if f := lastAudit(t, s, "token.revoke"); f.Success || !strings.Contains(f.Details, "not_owner") {
		t.Fatalf("yetkisiz iptal denemesi kaydedilmeli: %+v", f)
	}
	if w := doJSON(s, http.MethodPost, fmt.Sprintf("/api/tokens/%d/revoke", anTok.Meta.ID), `{}`, admin, csrfOf(admin)); w.Code != http.StatusOK {
		t.Fatalf("admin başkasının token'ını iptal: %d", w.Code)
	}
	if r := lastAudit(t, s, "token.revoke"); !r.Success || !strings.Contains(r.Details, `"byAdmin":true`) || !strings.Contains(r.Details, `"owner":"ana2"`) {
		t.Fatalf("admin iptal olayı: %+v", r)
	}
	if w := doJSON(s, http.MethodPost, "/api/tokens/999999/revoke", `{}`, admin, csrfOf(admin)); w.Code != http.StatusNotFound {
		t.Fatalf("olmayan token: %d", w.Code)
	}
	// Token yönetimi CSRF ister
	if w := doJSON(s, http.MethodPost, "/api/tokens", `{"name":"x","scope":"read"}`, an, ""); w.Code != http.StatusForbidden {
		t.Fatalf("CSRF'siz token oluşturma: %d", w.Code)
	}
}

func TestAPIToken_CreateValidation(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	for _, body := range []string{
		`{"name":"","scope":"read"}`,
		`{"name":"   ","scope":"read"}`,
		`{"name":"` + strings.Repeat("a", 65) + `","scope":"read"}`,
		`{"name":"a\u0007b","scope":"read"}`,
		`{"name":"ok","scope":"admin"}`,
		`{"name":"ok","scope":"read","expiresInDays":366}`,
		`{"name":"ok","scope":"read","expiresInDays":-1}`,
		`not json`,
	} {
		if w := doJSON(s, http.MethodPost, "/api/tokens", body, admin, csrfOf(admin)); w.Code != http.StatusBadRequest {
			t.Errorf("geçersiz istek %s: %d", body, w.Code)
		}
	}
	// 64 karakter Türkçe ad geçerli
	createToken(t, s, admin, strings.Repeat("ş", 64), storage.TokenScopeRead, 365)

	// Viewer: öz-hizmet olarak read token oluşturabilir ama write oluşturamaz
	viewer := mkTokenUser(t, s, "vtok", "vtok-pass-123", storage.RoleViewer)
	if w := doJSON(s, http.MethodPost, "/api/tokens", `{"name":"w","scope":"write"}`, viewer, csrfOf(viewer)); w.Code != http.StatusBadRequest {
		t.Fatalf("viewer write token: %d", w.Code)
	}
	vt := createToken(t, s, viewer, "viewer-reader", storage.TokenScopeRead, 7)
	if w := doJSON(s, http.MethodPost, fmt.Sprintf("/api/tokens/%d/revoke", vt.Meta.ID), `{}`, viewer, csrfOf(viewer)); w.Code != http.StatusOK {
		t.Fatalf("viewer kendi token'ını iptal: %d", w.Code)
	}
	// Viewer genel yazma kapısı hâlâ kapalı
	if w := doJSON(s, http.MethodPost, "/api/results/delete", `{"ids":[1]}`, viewer, csrfOf(viewer)); w.Code != http.StatusForbidden {
		t.Fatalf("viewer yazma: %d", w.Code)
	}

	// Etkin token sınırı
	for i := 0; i < maxActiveTokensPerUser; i++ {
		if _, _, err := s.db.CreateAPIToken(mustUserID(t, s, "vtok"), "vtok", fmt.Sprintf("t%d", i), storage.TokenScopeRead, nil); err != nil {
			t.Fatal(err)
		}
	}
	if w := doJSON(s, http.MethodPost, "/api/tokens", `{"name":"one-too-many","scope":"read"}`, viewer, csrfOf(viewer)); w.Code != http.StatusConflict {
		t.Fatalf("sınır aşımı: %d", w.Code)
	}
}

// .env önyükleme hesabı (users tablosu dışı) token alamaz.
func TestAPIToken_BootstrapAccountCannotCreate(t *testing.T) {
	s := newTestServer(t)
	ck := loginAs(t, s, "admin", "correct-horse")
	if w := doJSON(s, http.MethodPost, "/api/tokens", `{"name":"x","scope":"read"}`, ck, csrfOf(ck)); w.Code != http.StatusBadRequest {
		t.Fatalf("önyükleme hesabı token: %d", w.Code)
	}
	w := do(s, http.MethodGet, "/account", nil, ck, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-managed="0"`) {
		t.Fatalf("/account önyükleme: %d", w.Code)
	}
}

// last_used her istekte değil, en fazla dakikada bir yazılır.
func TestAPIToken_LastUsedThrottled(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	tok := createToken(t, s, admin, "touch", storage.TokenScopeRead, 7)

	recent := time.Now().Add(-10 * time.Second)
	if err := s.db.TouchAPIToken(tok.Meta.ID, "198.51.100.7", recent); err != nil {
		t.Fatal(err)
	}
	bearer(s, http.MethodGet, "/api/stats", "", tok.Token, nil, "")
	got, _ := s.db.GetAPIToken(tok.Meta.ID)
	if got.LastUsedIP != "198.51.100.7" || got.LastUsedAt.Sub(recent).Abs() > time.Second {
		t.Fatalf("dakika dolmadan yeniden yazıldı: %+v", got)
	}

	old := time.Now().Add(-2 * time.Minute)
	if err := s.db.TouchAPIToken(tok.Meta.ID, "198.51.100.7", old); err != nil {
		t.Fatal(err)
	}
	bearer(s, http.MethodGet, "/api/stats", "", tok.Token, nil, "")
	got, _ = s.db.GetAPIToken(tok.Meta.ID)
	if got.LastUsedIP == "198.51.100.7" || time.Since(*got.LastUsedAt) > 5*time.Second {
		t.Fatalf("bir dakikadan eski kullanım güncellenmedi: %+v", got)
	}
}

// Rastgele token yağmuru denetim kaydını şişirmez (IP + neden başına dakikada bir).
func TestAPIToken_FailureAuditThrottled(t *testing.T) {
	s := newMultiUserServer(t)
	for i := 0; i < 20; i++ {
		bearer(s, http.MethodGet, "/api/stats", "", fmt.Sprintf("kh_unknownToken%02dAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", i), nil, "")
	}
	if n := countAudit(t, s, "auth.token.failure"); n != 1 {
		t.Fatalf("seyreltilmiş tek olay beklenirdi: %d", n)
	}
}

// Hesabım sayfası: her rol erişir, kabuk menüsünde bağlantı var, 2FA alanı ayrılmış.
func TestAccountPage_RendersForEveryRole(t *testing.T) {
	s := newMultiUserServer(t)
	viewer := mkTokenUser(t, s, "accviewer", "accviewer-pass", storage.RoleViewer)
	viewer = append(viewer, &http.Cookie{Name: "lang", Value: "en"})
	w := do(s, http.MethodGet, "/account", nil, viewer, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/account viewer: %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{`id="security-2fa"`, `href="/account"`, "My Account", `/static/js/pages/account.js`, `data-managed="1"`, `value="write" disabled`} {
		if !strings.Contains(body, want) {
			t.Errorf("/account HTML eksik: %s", want)
		}
	}
	if strings.Contains(body, `id="tok-all"`) {
		t.Error("viewer 'tüm kullanıcılar' anahtarını görmemeli")
	}
	admin := loginAs(t, s, "admin", "adminpass1")
	w = do(s, http.MethodGet, "/account", nil, admin, nil)
	if !strings.Contains(w.Body.String(), `id="tok-all"`) || strings.Contains(w.Body.String(), `value="write" disabled`) {
		t.Error("admin: tüm kullanıcılar anahtarı ve write kapsamı beklenirdi")
	}
	// Kullanıcı menüsü eski parola diyaloğunu değil sayfayı açar
	if strings.Contains(w.Body.String(), `data-action="my-password"`) {
		t.Error("eski parola menü öğesi kaldırılmalı")
	}
}

// Admin parola sıfırlaması (olay müdahalesi) kullanıcının tüm API token'larını
// iptal eder: ele geçirilmiş oturumla basılmış token sıfırlamadan sağ çıkamaz.
func TestAPIToken_AdminPasswordResetRevokesTokens(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	bob := mkTokenUser(t, s, "bobreset", "bobreset-pass-1", storage.RoleAnalyst)
	tok := createToken(t, s, bob, "persist", storage.TokenScopeWrite, 0)
	other := createToken(t, s, admin, "admin-own", storage.TokenScopeRead, 0)
	if w := bearer(s, http.MethodGet, "/api/whoami", "", tok.Token, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("sıfırlama öncesi token: %d", w.Code)
	}
	id := itoa(mustUserID(t, s, "bobreset"))
	if w := doJSON(s, http.MethodPost, "/api/users/"+id, `{"password":"New-Reset-Pass-999"}`, admin, csrfOf(admin)); w.Code != http.StatusOK {
		t.Fatalf("parola sıfırlama: %d %s", w.Code, w.Body.String())
	}
	assert401(t, bearer(s, http.MethodGet, "/api/whoami", "", tok.Token, nil, ""), "sıfırlama sonrası token")
	if got, _ := s.db.GetAPIToken(tok.Meta.ID); got.Status(time.Now()) != storage.TokenStatusRevoked {
		t.Fatalf("token iptal edilmeli: %+v", got)
	}
	if e := lastAudit(t, s, "user.password.reset"); !e.Success || !strings.Contains(e.Details, `"tokensRevoked":1`) {
		t.Fatalf("denetim tokensRevoked içermeli: %+v", e)
	}
	// Başka kullanıcının token'ı etkilenmez
	if w := bearer(s, http.MethodGet, "/api/whoami", "", other.Token, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("ilgisiz token iptal edilmemeli: %d", w.Code)
	}
	// Yalnız rol/aktiflik güncellemesi token'lara dokunmaz
	tok2 := createToken(t, s, loginAs(t, s, "bobreset", "New-Reset-Pass-999"), "after", storage.TokenScopeRead, 0)
	if w := doJSON(s, http.MethodPost, "/api/users/"+id, `{"role":"viewer"}`, admin, csrfOf(admin)); w.Code != http.StatusOK {
		t.Fatalf("rol güncelleme: %d", w.Code)
	}
	if w := bearer(s, http.MethodGet, "/api/whoami", "", tok2.Token, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("rol değişikliği token'ı iptal etmemeli: %d", w.Code)
	}
}

// Kendi parola değişikliği: token'lar varsayılan olarak geçerli kalır;
// revokeTokens:true ile hepsi iptal edilir.
func TestAPIToken_SelfPasswordChangeOptionalRevoke(t *testing.T) {
	s := newMultiUserServer(t)
	ck := mkTokenUser(t, s, "selfpw", "selfpw-pass-1", storage.RoleAnalyst)
	a := createToken(t, s, ck, "a", storage.TokenScopeRead, 0)
	b := createToken(t, s, ck, "b", storage.TokenScopeWrite, 30)

	w := doJSON(s, http.MethodPost, "/api/me/password", `{"current":"selfpw-pass-1","new":"selfpw-pass-2"}`, ck, csrfOf(ck))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tokensRevoked":0`) {
		t.Fatalf("parola değişikliği: %d %s", w.Code, w.Body.String())
	}
	for _, tk := range []string{a.Token, b.Token} {
		if w := bearer(s, http.MethodGet, "/api/whoami", "", tk, nil, ""); w.Code != http.StatusOK {
			t.Fatalf("seçenek kapalıyken token geçerli kalmalı: %d", w.Code)
		}
	}

	// Yanlış mevcut parola ile token'lar iptal edilmez
	if w := doJSON(s, http.MethodPost, "/api/me/password", `{"current":"wrong-pass-x","new":"selfpw-pass-3","revokeTokens":true}`, ck, csrfOf(ck)); w.Code != http.StatusForbidden {
		t.Fatalf("yanlış parola: %d", w.Code)
	}
	if w := bearer(s, http.MethodGet, "/api/whoami", "", a.Token, nil, ""); w.Code != http.StatusOK {
		t.Fatalf("başarısız parola değişikliği token iptal etmemeli: %d", w.Code)
	}

	w = doJSON(s, http.MethodPost, "/api/me/password", `{"current":"selfpw-pass-2","new":"selfpw-pass-3","revokeTokens":true}`, ck, csrfOf(ck))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tokensRevoked":2`) {
		t.Fatalf("iptalli parola değişikliği: %d %s", w.Code, w.Body.String())
	}
	assert401(t, bearer(s, http.MethodGet, "/api/whoami", "", a.Token, nil, ""), "iptal sonrası a")
	assert401(t, bearer(s, http.MethodGet, "/api/whoami", "", b.Token, nil, ""), "iptal sonrası b")
	if e := lastAudit(t, s, "me.password.change"); !e.Success || !strings.Contains(e.Details, `"tokensRevoked":2`) {
		t.Fatalf("denetim: %+v", e)
	}
	// Mevcut oturum açık kalır ve yeni token oluşturabilir
	createToken(t, s, ck, "fresh", storage.TokenScopeRead, 7)
}

// Sahiplik ada göre değil hesap kimliğine göre: SQLite NOCASE yalnız ASCII
// katlar, bu yüzden "Şule" ve "şule" iki ayrı hesaptır ve birbirinin
// token'ını iptal edemez.
func TestAPIToken_RevokeOwnershipByAccountNotName(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	upper := mkTokenUser(t, s, "Şule", "sule-upper-pass", storage.RoleAnalyst)
	lower := mkTokenUser(t, s, "şule", "sule-lower-pass", storage.RoleAnalyst)
	if mustUserID(t, s, "Şule") == mustUserID(t, s, "şule") {
		t.Skip("bu SQLite derlemesi Unicode harf katlıyor; ayrı hesap açılamadı")
	}
	tok := createToken(t, s, upper, "upper-tok", storage.TokenScopeRead, 0)

	w := doJSON(s, http.MethodPost, fmt.Sprintf("/api/tokens/%d/revoke", tok.Meta.ID), `{}`, lower, csrfOf(lower))
	if w.Code != http.StatusNotFound {
		t.Fatalf("başka hesabın token'ı iptal edilmemeli: %d %s", w.Code, w.Body.String())
	}
	if got, _ := s.db.GetAPIToken(tok.Meta.ID); got.RevokedAt != nil {
		t.Fatal("token iptal edilmiş")
	}

	type listResp struct {
		Tokens []struct {
			ID   int64 `json:"id"`
			Mine bool  `json:"mine"`
		} `json:"tokens"`
	}
	var l listResp
	_ = json.Unmarshal(do(s, http.MethodGet, "/api/tokens", nil, upper, nil).Body.Bytes(), &l)
	if len(l.Tokens) != 1 || !l.Tokens[0].Mine {
		t.Fatalf("sahip kendi token'ını mine=true görmeli: %+v", l)
	}
	l = listResp{}
	_ = json.Unmarshal(do(s, http.MethodGet, "/api/tokens?all=1", nil, admin, nil).Body.Bytes(), &l)
	if len(l.Tokens) != 1 || l.Tokens[0].Mine {
		t.Fatalf("admin için başkasının token'ı mine=false olmalı: %+v", l)
	}

	// Sahip kendi token'ını iptal edebilir; olay byAdmin=false
	if w := doJSON(s, http.MethodPost, fmt.Sprintf("/api/tokens/%d/revoke", tok.Meta.ID), `{}`, upper, csrfOf(upper)); w.Code != http.StatusOK {
		t.Fatalf("sahip iptali: %d", w.Code)
	}
	if e := lastAudit(t, s, "token.revoke"); !e.Success || !strings.Contains(e.Details, `"byAdmin":false`) {
		t.Fatalf("sahip iptal olayı: %+v", e)
	}
}

// Etkin token sınırı eşzamanlı oluşturma isteklerinde de aşılamaz.
func TestAPIToken_ActiveLimitHoldsUnderConcurrency(t *testing.T) {
	s := newMultiUserServer(t)
	ck := mkTokenUser(t, s, "racer", "racer-pass-123", storage.RoleAnalyst)
	csrf := csrfOf(ck)
	const n = 60
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := doJSON(s, http.MethodPost, "/api/tokens", fmt.Sprintf(`{"name":"r%d","scope":"read"}`, i), ck, csrf)
			codes <- w.Code
		}(i)
	}
	wg.Wait()
	close(codes)
	created, conflict := 0, 0
	for c := range codes {
		switch c {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		default:
			t.Errorf("beklenmeyen durum: %d", c)
		}
	}
	active, err := s.db.CountActiveAPITokens("racer", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if created != maxActiveTokensPerUser || conflict != n-maxActiveTokensPerUser || active != maxActiveTokensPerUser {
		t.Fatalf("sınır aşıldı: created=%d conflict=%d active=%d (sınır %d)", created, conflict, active, maxActiveTokensPerUser)
	}
}

// Oluştur-iptal et döngüsü tabloyu sınırsız büyütemez: etkin olmayan satırlar
// kullanıcı başına maxInactiveTokensKept ile budanır.
func TestAPIToken_InactiveRowsPruned(t *testing.T) {
	s := newMultiUserServer(t)
	ck := mkTokenUser(t, s, "churn", "churn-pass-123", storage.RoleAnalyst)
	uid := mustUserID(t, s, "churn")
	for i := 0; i < maxInactiveTokensKept+20; i++ {
		_, tk, err := s.db.CreateAPIToken(uid, "churn", fmt.Sprintf("c%d", i), storage.TokenScopeRead, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.RevokeAPIToken(tk.ID); err != nil {
			t.Fatal(err)
		}
	}
	live := createToken(t, s, ck, "live", storage.TokenScopeRead, 0)
	list, err := s.db.ListAPITokens("churn")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != maxInactiveTokensKept+1 {
		t.Fatalf("budama sonrası satır: %d (beklenen %d)", len(list), maxInactiveTokensKept+1)
	}
	if list[0].ID != live.Meta.ID || list[0].RevokedAt != nil {
		t.Fatalf("etkin token korunmalı: %+v", list[0])
	}
	// Korunan iptal satırları en yenileridir
	if list[len(list)-1].Name != "c20" {
		t.Fatalf("en eski korunan satır c20 olmalı: %s", list[len(list)-1].Name)
	}
}

// Gösterim modalı yalnız düğmeyle kapanır; parola formu token iptal seçeneğini
// sunar; curl blokları klavyeyle kaydırılabilir.
func TestAccountPage_RevealStaticAndPasswordRevokeOption(t *testing.T) {
	s := newMultiUserServer(t)
	admin := loginAs(t, s, "admin", "adminpass1")
	body := do(s, http.MethodGet, "/account", nil, admin, nil).Body.String()
	for _, want := range []string{
		`id="token-reveal" data-static`,
		`id="pw-revoke-tokens"`,
		`<pre id="how-curl" tabindex="0"`,
		`<pre id="tok-curl" tabindex="0"`,
		`aria-describedby="tok-name-h"`,
		`id="tok-new-btn"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/account HTML eksik: %s", want)
		}
	}
	// Önyükleme hesabı (token'ı olamaz) iptal seçeneğini görmez
	bs := newTestServer(t)
	ck := loginAs(t, bs, "admin", "correct-horse")
	if strings.Contains(do(bs, http.MethodGet, "/account", nil, ck, nil).Body.String(), `id="pw-revoke-tokens"`) {
		t.Error("önyükleme hesabı token iptal seçeneğini görmemeli")
	}
}
