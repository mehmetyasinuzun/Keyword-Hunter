package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// Sıkı CSP (script-src 'self') ancak hiçbir şablon/betik satır içi kod
// üretmezse çalışır. Bu test regresyonu derleme zamanında yakalar:
//   - şablonlarda src'siz <script> (application/json veri blokları hariç)
//   - herhangi bir on*= olay işleyicisi özniteliği (şablon veya JS içinde
//     üretilen HTML dizgileri dahil)
//   - javascript: URL'leri
var (
	reScriptTag   = regexp.MustCompile(`(?is)<script\b([^>]*)>`)
	reInlineEvent = regexp.MustCompile(`(?i)<[^>]*\son(click|change|input|submit|load|error|key\w*|mouse\w*|focus|blur|dblclick|contextmenu)\s*=`)
	reJSURL       = regexp.MustCompile(`(?i)(href|src|action)\s*=\s*["']?\s*javascript:`)
)

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

func TestCSP_PolicyIsStrictForScripts(t *testing.T) {
	for _, dir := range strings.Split(contentSecurityPolicy, ";") {
		dir = strings.TrimSpace(dir)
		if strings.HasPrefix(dir, "script-src") && (strings.Contains(dir, "unsafe-inline") || strings.Contains(dir, "unsafe-eval")) {
			t.Fatalf("script-src gevşek: %q", dir)
		}
	}
	for _, must := range []string{"object-src 'none'", "frame-ancestors 'none'", "base-uri 'self'", "form-action 'self'"} {
		if !strings.Contains(contentSecurityPolicy, must) {
			t.Errorf("CSP eksik yönerge: %s", must)
		}
	}
}
