package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"keywordhunter-mvp/pkg/storage"
)

// Kabuk sunucu tarafında role göre süzülür: viewer admin menü bağlantılarını
// HTML'de hiç almamalı; admin almalı. Tema ve dil çerezleri ilk baytta basılmalı.
func TestShell_ServerSideRoleThemeLang(t *testing.T) {
	s := newTestServer(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("viewer-pass-1"), bcrypt.MinCost)
	if _, err := s.db.CreateUser("shellviewer", string(hash), storage.RoleViewer); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, s, "shellviewer", "viewer-pass-1")
	viewer = append(viewer, &http.Cookie{Name: "theme", Value: "light"}, &http.Cookie{Name: "lang", Value: "en"})

	w := do(s, http.MethodGet, "/dashboard", nil, viewer, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: %d", w.Code)
	}
	body := w.Body.String()
	for _, admin := range []string{`href="/users"`, `href="/settings"`} {
		if strings.Contains(body, admin) {
			t.Errorf("viewer HTML'inde admin bağlantısı var: %s", admin)
		}
	}
	if !strings.Contains(body, `data-theme="light"`) || !strings.Contains(body, `lang="en"`) {
		t.Error("tema/dil sunucu tarafında basılmadı")
	}
	if !strings.Contains(body, `class="role-viewer"`) {
		t.Error("viewer rol kancası eksik")
	}

	// Geçersiz tema değeri enjekte edilemez → auto
	bad := append(viewer[:len(viewer)-2:len(viewer)-2], &http.Cookie{Name: "theme", Value: `x" onload="alert(1)`})
	w = do(s, http.MethodGet, "/dashboard", nil, bad, nil)
	if !strings.Contains(w.Body.String(), `data-theme="auto"`) {
		t.Error("geçersiz tema çerezi auto'ya düşmeli")
	}
}

func TestErrorPages(t *testing.T) {
	s := newTestServer(t)
	w := do(s, http.MethodGet, "/boyle-bir-sayfa-yok", nil, nil, nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "KeywordHunter") {
		t.Fatalf("404 sayfası: %d", w.Code)
	}
	w = do(s, http.MethodGet, "/api/boyle-bir-uc-yok", nil, nil, nil)
	if w.Code == http.StatusOK || strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("API 404 HTML dönmemeli: %d", w.Code)
	}

	hash, _ := bcrypt.GenerateFromPassword([]byte("analyst-pass-1"), bcrypt.MinCost)
	if _, err := s.db.CreateUser("erranalyst", string(hash), storage.RoleAnalyst); err != nil {
		t.Fatal(err)
	}
	ck := loginAs(t, s, "erranalyst", "analyst-pass-1")
	w = do(s, http.MethodGet, "/users", nil, ck, nil)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "<html") {
		t.Fatalf("analist /users: %d (markalı 403 bekleniyordu)", w.Code)
	}
}

// POST /search viewer için yasak, CSRF'siz istek reddedilir.
func TestSearchPost_RequiresAnalystAndCSRF(t *testing.T) {
	s := newTestServer(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte("viewer-pass-1"), bcrypt.MinCost)
	if _, err := s.db.CreateUser("searchviewer", string(hash), storage.RoleViewer); err != nil {
		t.Fatal(err)
	}
	viewer := loginAs(t, s, "searchviewer", "viewer-pass-1")
	form := url.Values{"query": {"test"}, "_csrf": {csrfOf(viewer)}}
	if w := do(s, http.MethodPost, "/search", form, viewer, nil); w.Code != http.StatusForbidden {
		t.Fatalf("viewer arama: %d (403 bekleniyordu)", w.Code)
	}
	admin := loginAs(t, s, "admin", "correct-horse")
	if w := do(s, http.MethodPost, "/search", url.Values{"query": {"test"}}, admin, nil); w.Code != http.StatusForbidden {
		t.Fatalf("CSRF'siz arama: %d (403 bekleniyordu)", w.Code)
	}
}

// Kabuk ve panodaki erişilebilir adlar (axe: label-content-name-mismatch,
// link-name, nested-interactive). Uçtan uca axe taraması e2e/'de; bu test aynı
// gerilemeyi tarayıcısız, her `go test`te yakalar.
func TestShell_AccessibleNames(t *testing.T) {
	s := newTestServer(t)
	if _, err := s.db.SaveResults([]storage.SearchResult{
		{Title: "örnek bulgu", URL: "http://" + strings.Repeat("a", 55) + "d.onion/x", Source: "Tordex", Query: "erişilebilirlik", Criticality: 4},
	}); err != nil {
		t.Fatal(err)
	}
	// "Son aramalar" tablosu: satır başına ikon-tek harita bağlantısı
	if err := s.db.SaveSearchHistory("erişilebilirlik", 1); err != nil {
		t.Fatal(err)
	}
	admin := loginAs(t, s, "admin", "correct-horse")
	w := do(s, http.MethodGet, "/dashboard", nil, admin, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("dashboard: %d", w.Code)
	}
	body := w.Body.String()

	mustContain := map[string]string{
		// Avatar: görünen baş harfler gizli, ad ekran okuyucu metninden (kullanıcı adı).
		"avatar adı": `<span aria-hidden="true">AD</span><span class="kh-sr">admin</span>`,
		// ":" .txt içinde, görünen ipucuyla birlikte gizlenir: dar ekranda ad "Komut paleti:" olmaz.
		"palet adı":      `<span class="kh-sr">Komut paleti<span class="txt">:</span> </span><span class="txt">`,
		"palet kısayolu": `aria-keyshortcuts="Control+K Meta+K"`,
		"palet tuşları":  `<span class="kbds" aria-hidden="true">`,
		// aria-label data-tip (::after) içeriğini addan dışlar: "Dil: TR Dil" olmaz.
		"dil düğmesi adı":    `data-action="lang-toggle" aria-label="Dil: TR" data-tip="Dil"><span class="lbl">TR</span>`,
		"harita bağlantısı":  `aria-label="Harita: erişilebilirlik"`,
		"kritiklik bölütü":   `<span class="sev-seg s4" data-action="sev-go" data-level="4"`,
		"marka (görünen ad)": `<a class="kh-brand" href="/dashboard">`,
	}
	for what, want := range mustContain {
		if !strings.Contains(body, want) {
			t.Errorf("%s: %q bulunamadı", what, want)
		}
	}
	for what, bad := range map[string]string{
		"avatar aria-label (görünen metni içermiyor)": `class="kh-avatar" type="button" data-action="menu-toggle" data-target="kh-user-menu" aria-haspopup="menu" aria-expanded="false" aria-label=`,
		"role=img içinde bağlantı":                    `<a class="sev-seg`,
		"marka aria-label":                            `aria-label="KeywordHunter"`,
		"dil düğmesinde yinelenen ad":                 `<span class="kh-sr">Dil: </span>`,
		"palet adında sarkan iki nokta":               `<span class="kh-sr">Komut paleti: </span>`,
	} {
		if strings.Contains(body, bad) {
			t.Errorf("%s: %q hâlâ var", what, bad)
		}
	}
}
