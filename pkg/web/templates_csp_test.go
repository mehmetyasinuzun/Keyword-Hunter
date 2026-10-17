package web

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"keywordhunter-mvp/pkg/config"
)

// Sıkı CSP (script-src 'self'; style-src 'self') ancak hiçbir şablon/betik
// satır içi kod ya da stil üretmezse çalışır. Bu testler regresyonu derleme
// zamanında yakalar:
//   - şablonlarda src'siz <script> (application/json veri blokları hariç)
//   - herhangi bir on*= olay işleyicisi özniteliği (şablon veya JS içinde
//     üretilen HTML dizgileri dahil)
//   - javascript: URL'leri
//   - <style> blokları ve style="" öznitelikleri (şablonlarda ve JS'nin
//     innerHTML ile eklediği HTML dizgilerinde), setAttribute('style', …),
//     D3 .attr('style', …) ve createElement('style')
var (
	reScriptTag   = regexp.MustCompile(`(?is)<script\b([^>]*)>`)
	reInlineEvent = regexp.MustCompile(`(?i)<[^>]*\son(click|change|input|submit|load|error|key\w*|mouse\w*|focus|blur|dblclick|contextmenu)\s*=`)
	reJSURL       = regexp.MustCompile(`(?i)(href|src|action)\s*=\s*["']?\s*javascript:`)

	// Şablonlar HTML'dir: "style=" ancak bir öznitelik olarak geçebilir.
	reTplStyleTag  = regexp.MustCompile(`(?i)<style\b`)
	reTplStyleAttr = regexp.MustCompile(`(?i)\sstyle\s*=`)

	// JS: HTML dizgisi içindeki style özniteliği. Önünde nokta/kelime
	// karakteri olmayan "style=" + değer; böylece CSSOM atamaları
	// (el.style = …, el.style.width = …) ve data-style=/list-style= eşleşmez.
	reJSStyleAttr = regexp.MustCompile(`(?i)(?:^|[^\w.$-])style\s*=\s*[^=\s]`)
	// "const style = …" gibi bir değişken bildirimi HTML değildir.
	reJSDeclTail = regexp.MustCompile(`\b(?:const|let|var)\s+$`)
	reJSStyleAPI = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<style\b`),
		regexp.MustCompile("(?i)\\b(?:setAttribute|attr)\\(\\s*['\"`]style['\"`]"),
		regexp.MustCompile("(?i)\\bsetAttributeNS\\([^,()]*,\\s*['\"`]style['\"`]"),
		regexp.MustCompile("(?i)\\bcreateElement(?:NS)?\\((?:[^,()]*,\\s*)?['\"`]style['\"`]"),
	}
)

// jsInlineStyle JS kaynağında CSP'nin (style-src 'self') engelleyeceği ilk
// satır içi stil kullanımını döndürür; yoksa "". CSSOM (el.style.x = …,
// el.style.setProperty, D3 .style(…)) serbesttir — CSP onu engellemez.
func jsInlineStyle(src string) string {
	for _, re := range reJSStyleAPI {
		if m := re.FindString(src); m != "" {
			return m
		}
	}
	for _, loc := range reJSStyleAttr.FindAllStringIndex(src, -1) {
		start := loc[0]
		if lo := max(0, start-24); reJSDeclTail.MatchString(src[lo : start+1]) {
			continue
		}
		return src[loc[0]:loc[1]]
	}
	return ""
}

func TestCSP_NoInlineScriptsInTemplates(t *testing.T) {
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		b, _ := templateFS.ReadFile(path)
		src := string(b)
		for _, m := range reScriptTag.FindAllStringSubmatch(src, -1) {
			attrs := strings.ToLower(m[1])
			if strings.Contains(attrs, "src=") || strings.Contains(attrs, `type="application/json"`) {
				continue
			}
			t.Errorf("%s: satır içi <script%s> — betiği /static/js altına taşıyın", path, m[1])
		}
		if loc := reInlineEvent.FindString(src); loc != "" {
			t.Errorf("%s: satır içi olay işleyicisi: %.80q — data-action kullanın", path, loc)
		}
		if loc := reJSURL.FindString(src); loc != "" {
			t.Errorf("%s: javascript: URL: %q", path, loc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCSP_NoInlineStylesInTemplates(t *testing.T) {
	n := 0
	err := fs.WalkDir(templateFS, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		n++
		b, _ := templateFS.ReadFile(path)
		src := string(b)
		if loc := reTplStyleTag.FindString(src); loc != "" {
			t.Errorf("%s: <style> bloğu — stili static/css/pages/<sayfa>.css'e taşıyın", path)
		}
		if loc := reTplStyleAttr.FindString(src); loc != "" {
			t.Errorf("%s: style özniteliği %q — kh.css yardımcı sınıfı ya da sayfa CSS'i kullanın", path, loc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("hiç şablon taranmadı")
	}
}

func TestCSP_NoInlineHandlersGeneratedByJS(t *testing.T) {
	reJSHandler := regexp.MustCompile(`(?i)\son(click|change|input|submit|load|error|key\w*|mouse\w*)\s*=\s*\\?["'$]`)
	err := fs.WalkDir(staticFS, "static/js", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".js") || strings.Contains(path, "/vendor/") {
			return err
		}
		b, _ := staticFS.ReadFile(path)
		if loc := reJSHandler.FindString(string(b)); loc != "" {
			t.Errorf("%s: üretilen HTML'de satır içi işleyici %q — CSP bunu engeller, data-action kullanın", path, loc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCSP_NoInlineStylesGeneratedByJS(t *testing.T) {
	n := 0
	err := fs.WalkDir(staticFS, "static/js", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".js") || strings.Contains(path, "/vendor/") {
			return err
		}
		n++
		b, _ := staticFS.ReadFile(path)
		if loc := jsInlineStyle(string(b)); loc != "" {
			t.Errorf("%s: satır içi stil %q — CSP (style-src 'self') bunu engeller; sınıf kullanın ya da eklemeden sonra CSSOM ile atayın (el.style.width = …)", path, loc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("hiç JS dosyası taranmadı")
	}
}

// Dedektörün kendisi: yakalaması ve serbest bırakması gerekenler.
func TestCSP_JSInlineStyleDetector(t *testing.T) {
	blocked := []string{
		`'<div style="width:0"></div>'`,
		`'<a class="x" style=\"color:red\">'`,
		"`<div data-to=\"${p}\" style=\"width:${p}%\"></div>`",
		"`<i style=${s}></i>`",
		`'<span style=\'x\'>'`,
		`"<b style=color:red>"`,
		`el.setAttribute('style', 'color:red')`,
		`el.setAttribute("style", s)`,
		`el.setAttributeNS(null, 'style', s)`,
		"sel.attr(`style`, d => d.s)",
		`d3.select(n).attr('style', 'opacity:0')`,
		`document.createElement('style')`,
		`document.createElementNS(ns, "style")`,
		`box.innerHTML = '<style>.x{}</style>'`,
	}
	for _, src := range blocked {
		if jsInlineStyle(src) == "" {
			t.Errorf("yakalanmalıydı: %s", src)
		}
	}
	allowed := []string{
		`el.style.width = pct + '%'`,
		`el.style.setProperty('--p', v)`,
		`el.style.animationDelay = (i * 35) + 'ms'`,
		`el.style = ''`,
		`sel.style('opacity', 0)`,
		`d3.select(n).attr('class', 'style')`,
		`const style = 'short'`,
		`let style = opts.style`,
		`new Intl.RelativeTimeFormat(l, { numeric: 'auto', style: 'short' })`,
		`'<div data-style="x" class="list-style=none"></div>'`,
		`if (a.style == b) {}`,
		`// satır içi style özniteliği CSP'de engellenir`,
	}
	for _, src := range allowed {
		if m := jsInlineStyle(src); m != "" {
			t.Errorf("yanlış pozitif %q: %s", m, src)
		}
	}
}

// cspDirectives politikayı yönerge adı → kaynak listesi eşlemine çevirir.
func cspDirectives(policy string) map[string]string {
	out := map[string]string{}
	for _, dir := range strings.Split(policy, ";") {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		name, val, _ := strings.Cut(dir, " ")
		out[name] = strings.TrimSpace(val)
	}
	return out
}

func TestCSP_PolicyIsStrict(t *testing.T) {
	// Hiçbir yönergede gevşetme anahtar sözcüğü olmamalı ('unsafe-inline',
	// 'unsafe-eval', 'unsafe-hashes' …); nonce/hash da kullanılmıyor.
	if strings.Contains(contentSecurityPolicy, "unsafe-") {
		t.Fatalf("CSP 'unsafe-*' içeriyor: %q", contentSecurityPolicy)
	}
	for _, bad := range []string{"'nonce-", "'sha256-", "'sha384-", "'sha512-", "'strict-dynamic'", "*", "http:", "https:"} {
		if strings.Contains(contentSecurityPolicy, bad) {
			t.Errorf("CSP beklenmeyen kaynak içeriyor %q: %q", bad, contentSecurityPolicy)
		}
	}
	d := cspDirectives(contentSecurityPolicy)
	want := map[string]string{
		"default-src":     "'self'",
		"script-src":      "'self'",
		"style-src":       "'self'",
		"connect-src":     "'self'",
		"object-src":      "'none'",
		"frame-ancestors": "'none'",
		"base-uri":        "'self'",
		"form-action":     "'self'",
	}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("CSP %s = %q, beklenen %q", k, d[k], v)
		}
	}
	// Özniteliğe/öğeye özgü alt yönergeler style-src'yi gevşetmesin
	for _, k := range []string{"style-src-attr", "style-src-elem", "script-src-attr", "script-src-elem"} {
		if v, ok := d[k]; ok {
			t.Errorf("CSP %s tanımlı (%q) — style-src/script-src 'self' yeterli", k, v)
		}
	}
}

// Sunucunun gerçekten bastığı sayfalar: başlık sabitle aynı olmalı ve
// çıktıda (şablon yardımcıları dahil) satır içi stil bulunmamalı.
func TestCSP_RenderedPagesHaveNoInlineStyles(t *testing.T) {
	s := newTwoFAServer(t, config.Require2FANone)
	admin := loginAs(t, s, "admin", "adminpass1")

	// 2FA'lı ikinci kullanıcı: /login/2fa ekranı için bekleyen giriş çerezi
	const tfaPass = "Str0ng-Viewer-Pass!"
	if w := doJSON(s, http.MethodPost, "/api/users", `{"username":"tfauser","password":"`+tfaPass+`","role":"viewer"}`, admin, csrfOf(admin)); w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("kullanıcı oluşturulamadı: %d %s", w.Code, w.Body.String())
	}
	viewer := loginAs(t, s, "tfauser", tfaPass)
	enroll2FA(t, s, viewer, tfaPass)
	pending := passwordStep(t, s, "tfauser", tfaPass)

	type page struct {
		path   string
		ck     []*http.Cookie
		status int
	}
	pages := []page{
		{"/login", nil, http.StatusOK},
		{"/login/2fa", []*http.Cookie{pending}, http.StatusOK},
		{"/login/2fa?mode=recovery", []*http.Cookie{pending}, http.StatusOK},
		{"/account", admin, http.StatusOK},
		{"/does-not-exist", admin, http.StatusNotFound},
		{"/users", viewer, http.StatusForbidden},
	}
	for _, g := range navLayout {
		for _, it := range g.Items {
			pages = append(pages, page{it.Href, admin, http.StatusOK})
		}
	}
	for _, p := range pages {
		w := do(s, http.MethodGet, p.path, nil, p.ck, nil)
		if w.Code != p.status {
			t.Errorf("%s: durum %d, beklenen %d", p.path, w.Code, p.status)
			continue
		}
		if got := w.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
			t.Errorf("%s: CSP başlığı %q", p.path, got)
		}
		body := w.Body.String()
		if !strings.Contains(body, "<html") {
			t.Errorf("%s: HTML sayfası beklenirdi", p.path)
			continue
		}
		if loc := reTplStyleTag.FindString(body); loc != "" {
			t.Errorf("%s: çıktıda <style> bloğu", p.path)
		}
		if i := reTplStyleAttr.FindStringIndex(body); i != nil {
			t.Errorf("%s: çıktıda style özniteliği: %q", p.path, body[max(0, i[0]-60):min(len(body), i[1]+40)])
		}
	}
}
