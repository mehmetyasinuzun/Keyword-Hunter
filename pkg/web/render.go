package web

import (
	"html/template"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"keywordhunter-mvp/pkg/i18n"
	"keywordhunter-mvp/pkg/shared"
)

// render bir sayfa şablonunu istek bağlamıyla (dil, rol, kullanıcı, CSRF)
// zenginleştirip basar. Kabuk (yan menü, üst bar) bu alanları sunucu tarafında
// kullanır: rol bazlı menü gizleme ve çeviri, sayfa yüklendikten sonra JS ile
// değil ilk baytta doğru gelir (yanıp sönme / yetki sızıntısı yok).
func (s *Server) render(c *gin.Context, status int, name string, data gin.H) {
	if data == nil {
		data = gin.H{}
	}
	data["Lang"] = requestLang(c)
	data["Theme"] = requestTheme(c)
	data["User"] = c.GetString("username")
	role := c.GetString("role")
	data["Role"] = role
	data["IsAdmin"] = role == "admin"
	data["CanWrite"] = roleRank(role) >= roleRank("analyst")
	data["CSRF"] = c.GetString("csrfToken")
	_, collapsed := c.Cookie("sb")
	data["SidebarCollapsed"] = collapsed == nil
	data["I18n"] = i18n.Catalog(data["Lang"].(string))
	active, _ := data["ActivePage"].(string)
	data["Nav"], data["Crumb"] = buildNav(active, role)
	c.HTML(status, name, data)
}

// navItem yan menüdeki tek bağlantı. Menü yapısı burada tek kaynaktır: şablon
// ve komut paleti (DOM'dan okur) aynı listeyi kullanır.
type navItem struct {
	Key, Href, Icon, LabelKey, Kbd string
	AdminOnly                      bool
	Active                         bool
}

type navGroup struct {
	LabelKey string
	Items    []navItem
}

// navCrumb üst bardaki konum göstergesi (grup › sayfa).
type navCrumb struct{ GroupKey, LabelKey string }

var navLayout = []navGroup{
	{"shell.group.ops", []navItem{
		{Key: "dashboard", Href: "/dashboard", Icon: "dashboard", LabelKey: "nav.dashboard", Kbd: "G D"},
		{Key: "search", Href: "/search", Icon: "search", LabelKey: "nav.search", Kbd: "/"},
		{Key: "results", Href: "/results", Icon: "results", LabelKey: "nav.results", Kbd: "G R"},
		{Key: "graph", Href: "/results/graph", Icon: "graph", LabelKey: "nav.graph", Kbd: "G H"},
		{Key: "analytics", Href: "/analytics", Icon: "analytics", LabelKey: "nav.analytics", Kbd: "G A"},
	}},
	{"shell.group.auto", []navItem{
		{Key: "scheduled", Href: "/scheduled", Icon: "scheduled", LabelKey: "nav.scheduledLong", Kbd: "G P"},
		{Key: "watchlist", Href: "/watchlist", Icon: "watchlist", LabelKey: "nav.watchlistLong", Kbd: "G I"},
		{Key: "crawl", Href: "/crawl", Icon: "crawl", LabelKey: "nav.crawlLong", Kbd: "G C"},
	}},
	{"shell.group.system", []navItem{
		{Key: "monitor", Href: "/monitor", Icon: "monitor", LabelKey: "nav.monitorLong", Kbd: "G M"},
		{Key: "users", Href: "/users", Icon: "user", LabelKey: "nav.users", Kbd: "G U", AdminOnly: true},
		{Key: "settings", Href: "/settings", Icon: "settings", LabelKey: "nav.settings", Kbd: "G Y", AdminOnly: true},
	}},
}

// buildNav rol bazlı süzülmüş menüyü ve etkin sayfanın konumunu üretir.
// Yönetici olmayanlar admin bağlantılarını HTML'de hiç almaz.
func buildNav(active, role string) ([]navGroup, navCrumb) {
	var crumb navCrumb
	out := make([]navGroup, 0, len(navLayout))
	for _, g := range navLayout {
		ng := navGroup{LabelKey: g.LabelKey}
		for _, it := range g.Items {
			if it.AdminOnly && role != "admin" {
				continue
			}
			if it.Key == active {
				it.Active = true
				crumb = navCrumb{GroupKey: g.LabelKey, LabelKey: it.LabelKey}
			}
			ng.Items = append(ng.Items, it)
		}
		if len(ng.Items) > 0 {
			out = append(out, ng)
		}
	}
	return out, crumb
}

// requestLang dil çerezini doğrulanmış dil koduna çevirir.
func requestLang(c *gin.Context) string {
	lang, _ := c.Cookie("lang")
	return i18n.Normalize(lang)
}

// requestTheme tema çerezini (light|dark|auto) doğrular. Tema sunucu tarafında
// <html data-theme> olarak basılır; böylece satır içi betik olmadan ilk boyamada
// doğru tema gelir (yanıp sönme yok, sıkı CSP korunur).
func requestTheme(c *gin.Context) string {
	switch t, _ := c.Cookie("theme"); t {
	case "light", "dark":
		return t
	default:
		return "auto"
	}
}

// templateFuncs tüm şablonlarda kullanılabilen yardımcılar.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"truncate": func(str string, length int) string {
			return shared.Truncate(str, length)
		},
		"formatTime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Local().Format("02.01.2006 15:04")
		},
		"isoTime": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.UTC().Format(time.RFC3339)
		},
		"version": func() string { return Version },
		"seq": func(start, end int) []int {
			var res []int
			for i := start; i <= end; i++ {
				res = append(res, i)
			}
			return res
		},
		"split": func(s, sep string) []string {
			if s == "" {
				return []string{}
			}
			return strings.Split(s, sep)
		},
		// tags virgülle ayrılmış etiket dizesini kırpılmış, boşsuz listeye çevirir
		// ("a, b" ve "a,b" biçimlerinin ikisi de desteklenir).
		"tags": func(s string) []string {
			var out []string
			for _, t := range strings.Split(s, ",") {
				if t = strings.TrimSpace(t); t != "" {
					out = append(out, t)
				}
			}
			return out
		},
		"eq": func(a, b interface{}) bool {
			return a == b
		},
		// t çeviri anahtarını verilen dilde döndürür: {{t .Lang "nav.search"}}
		"t": func(lang, key string) string { return i18n.T(lang, key) },
		"initials": func(name string) string {
			r := []rune(strings.TrimSpace(name))
			if len(r) == 0 {
				return "?"
			}
			if len(r) == 1 {
				return strings.ToUpper(string(r))
			}
			return strings.ToUpper(string(r[:2]))
		},
		// icon özgün SVG ikon setinden bir simgeyi güvenli HTML olarak basar.
		"icon": func(name string) template.HTML {
			return iconSVG(name, "")
		},
		"iconc": func(name, cls string) template.HTML {
			return iconSVG(name, cls)
		},
	}
}

// renderError markaya uygun hata sayfası basar; /api/ altında JSON döner.
func (s *Server) renderError(c *gin.Context, status int) {
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		msg := "Bulunamadı"
		if status == 403 {
			msg = "Bu işlem için yetkiniz yok"
		}
		c.AbortWithStatusJSON(status, gin.H{"error": msg})
		return
	}
	key := "err.404"
	if status == 403 {
		key = "err.403"
	}
	c.Header("Cache-Control", "no-store")
	s.render(c, status, "error.html", gin.H{
		"code":     status,
		"titleKey": key + ".title",
		"bodyKey":  key + ".body",
		"path":     shared.Truncate(c.Request.URL.Path, 120),
	})
	c.Abort()
}
