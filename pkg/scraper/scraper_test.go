package scraper

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

var (
	siteHost  = strings.Repeat("s", 56) + ".onion"
	otherHost = strings.Repeat("o", 56) + ".onion"
	threadURL = "http://" + siteHost + "/forum/thread/9"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newOnionScraper gerçek New() ile kurulan Scraper'ın Tor istemcisini, her
// .onion adresini httptest sunucusuna bağlayan bir dialer'la değiştirir. Çekim,
// başlık, yeniden deneme ve ayrıştırma yolu üretimdekiyle aynıdır; yalnız ağ sahtedir.
func newOnionScraper(t *testing.T, h http.Handler) *Scraper {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s, err := New("127.0.0.1:9050")
	if err != nil {
		t.Fatal(err)
	}
	addr := srv.Listener.Addr().String()
	s.client = &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, addr)
		}},
	}
	return s
}

// pages host+path → (durum, gövde) eşlemesiyle sahte onion ağı.
type pages map[string]struct {
	code int
	body string
}

func (p pages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pg, ok := p[r.Host+r.URL.RequestURI()]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(pg.code)
	_, _ = w.Write([]byte(pg.body))
}

func TestHTMLToText_DropsHeadScriptStyleAndDecodes(t *testing.T) {
	text := HTMLToText(readFixture(t, "forum_thread.html"))
	for _, bad := range []string{"tracking", "background", "Shadow Board", "<", "&amp;", "  "} {
		if strings.Contains(text, bad) {
			t.Errorf("görünür metin %q içermemeli", bad)
		}
	}
	for _, want := range []string{"Next & more", "Fresh database leak from a European bank", "sızıntı veritabanı şifreler"} {
		if !strings.Contains(text, want) {
			t.Errorf("görünür metin %q içermeli", want)
		}
	}
	if text != strings.TrimSpace(text) {
		t.Error("metin kırpılmış olmalı")
	}
}

func TestExtractHTMLTitle(t *testing.T) {
	if got := extractHTMLTitle(readFixture(t, "forum_thread.html")); got != "Fresh Database Leak & Combo | Shadow Board" {
		t.Fatalf("başlık = %q", got)
	}
	if got := extractHTMLTitle("<p>yok</p>"); got != "" {
		t.Fatalf("başlıksız sayfa = %q", got)
	}
	if got := extractHTMLTitle("<title>" + strings.Repeat("ş", 300) + "</title>"); utf8.RuneCountInString(got) != 200 {
		t.Fatalf("başlık 200 rune'a kısalmalı: %d", utf8.RuneCountInString(got))
	}
}

// TestExtractLinks_Fixture sayfadan bağlantı çıkarımı: göreli çözümleme, varlık
// çözme, tekilleştirme, iç/dış ayrımı, ikili dosya ve http(s) dışı şemaların atılması.
func TestExtractLinks_Fixture(t *testing.T) {
	links, err := (&Scraper{}).ExtractLinks(threadURL, readFixture(t, "forum_thread.html"))
	if err != nil {
		t.Fatal(err)
	}
	type want struct{ title, typ string }
	expected := map[string]want{
		"http://" + siteHost + "/":                             {"Home", "internal"},
		"http://" + siteHost + "/forum/leaks/":                 {"Leaks", "internal"},
		"http://" + siteHost + "/thread?id=42&page=2":          {"Next & more", "internal"},
		"http://" + siteHost + "/forum/market":                 {"Market section", "internal"},
		"http://" + otherHost + "/escrow":                      {"escrow", "external"},
		"https://clearnet.example.com/news":                    {"Clearnet news", "external"},
		"http://" + siteHost + "/members/list-of-vendors_2026": {"list of vendors 2026", "internal"},
	}
	got := map[string]ExtractedLink{}
	for _, l := range links {
		if _, dup := got[l.URL]; dup {
			t.Errorf("tekrarlanan bağlantı: %s", l.URL)
		}
		got[l.URL] = l
	}
	for u, w := range expected {
		l, ok := got[u]
		if !ok {
			t.Errorf("eksik bağlantı %s", u)
			continue
		}
		if l.Title != w.title || l.LinkType != w.typ {
			t.Errorf("%s: başlık=%q tür=%q, beklenen %q/%q", u, l.Title, l.LinkType, w.title, w.typ)
		}
	}
	if len(links) != len(expected) {
		t.Errorf("bağlantı sayısı %d, beklenen %d: %+v", len(links), len(expected), links)
	}
	for _, l := range links {
		if !strings.HasPrefix(l.URL, "http://") && !strings.HasPrefix(l.URL, "https://") {
			t.Errorf("http(s) dışı bağlantı sızdı: %q", l.URL)
		}
	}
}

func TestExtractLinks_TitleTruncatedRuneSafe(t *testing.T) {
	html := `<a href="/x">` + strings.Repeat("ğ", 150) + `</a>`
	links, _ := (&Scraper{}).ExtractLinks(threadURL, html)
	if len(links) != 1 || utf8.RuneCountInString(links[0].Title) != 100 || !strings.HasSuffix(links[0].Title, "...") || !utf8.ValidString(links[0].Title) {
		t.Fatalf("uzun başlık 97 rune + ... olmalı: %+v", links)
	}
}

func TestNormalizeURL(t *testing.T) {
	base := "http://" + siteHost + "/a/b/page.php?x=1"
	cases := map[string]string{
		"":                          "",
		"   ":                       "",
		"#frag":                     "",
		"javascript:alert(1)":       "",
		"JavaScript:alert(1)":       "",
		" jAvAsCrIpT:alert(1)":      "",
		"mailto:x@y.z":              "",
		"MAILTO:x@y.z":              "",
		"data:text/html,<b>x</b>":   "",
		"ftp://" + siteHost + "/f":  "",
		"file:///etc/passwd":        "",
		"http://" + otherHost + "/": "http://" + otherHost + "/",
		"HTTP://" + otherHost + "/": "http://" + otherHost + "/",
		"../up":                     "http://" + siteHost + "/a/up",
		"sib":                       "http://" + siteHost + "/a/b/sib",
		"/root?q=1":                 "http://" + siteHost + "/root?q=1",
		"?only=query":               "http://" + siteHost + "/a/b/page.php?only=query",
		"//" + otherHost + "/proto": "http://" + otherHost + "/proto",
		"http://[::1":               "http://[::1", // mutlak http(s) olduğu gibi döner
		"%zz":                       "",
		"http:0":                    "", // host'suz opak http
		"//::":                      "", // geri ayrıştırılamayan sonuç
		"https:x/y":                 "",
	}
	for in, want := range cases {
		if got := normalizeURL(in, base); got != want {
			t.Errorf("normalizeURL(%q) = %q, beklenen %q", in, got, want)
		}
	}
	if got := normalizeURL("/x", "::bozuk"); got != "" {
		t.Errorf("bozuk taban URL boş dönmeli: %q", got)
	}
}

func TestExtractTitleFromURLPath(t *testing.T) {
	cases := map[string]string{
		"http://" + siteHost + "/":                           siteHost,
		"http://" + siteHost:                                 siteHost,
		"http://" + siteHost + "/dir/my-page_name":           "my page name",
		"http://" + siteHost + "/dir/sub/":                   "sub",
		"http://" + siteHost + "/-_-":                        "Link",
		"/relative-only":                                     "relative only",
		"::bozuk":                                            "Link",
		"http://" + siteHost + "/" + strings.Repeat("a", 90): strings.Repeat("a", 77) + "...",
	}
	for in, want := range cases {
		if got := extractTitleFromURLPath(in); got != want {
			t.Errorf("extractTitleFromURLPath(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

func TestContainsWholeWord(t *testing.T) {
	cases := []struct {
		text, kw string
		want     bool
	}{
		{"contact the administrator", "rat", false},
		{"history of the story", "tor", false},
		{"use tor browser", "tor", true},
		{"tor", "tor", true},
		{"rat: remote access", "rat", true},
		{"a new rat.", "rat", true},
		{"ratings and rat", "rat", true},
		{"sell credit card dumps", "credit card", true},
		{"0day exploit", "0day", true},
		{"x0day", "0day", false},
		{"şrat", "rat", false},
		{"", "rat", false},
		{"rat", "", false},
	}
	for _, c := range cases {
		if got := containsWholeWord(c.text, c.kw); got != c.want {
			t.Errorf("containsWholeWord(%q,%q) = %v, beklenen %v", c.text, c.kw, got, c.want)
		}
	}
}

// TestExtractTopKeywordsFromHTML_Fixture etiketler: durdurma kelimeleri ve arama
// sorgusu dışlanır, kök tekrarı (leak/leaks) tek sayılır, CTI kelimeleri öne
// çıkar; script içindeki metin hit sayısına girmez; alt dizgi eşleşmesi ("rat"
// ⊂ "moderators") yanlış etiket üretmez.
func TestExtractTopKeywordsFromHTML_Fixture(t *testing.T) {
	s := &Scraper{}
	res := s.ExtractTopKeywordsFromHTML(readFixture(t, "forum_thread.html"), "leak", 0)
	if !res.Success {
		t.Fatalf("başarısız: %s", res.Error)
	}
	if res.KeywordHits != 5 {
		t.Errorf("görünür metinde 'leak' 5 kez geçer (script hariç), %d", res.KeywordHits)
	}
	if len(res.Tags) != 5 {
		t.Fatalf("maxTags<=0 → 5 etiket: %v", res.Tags)
	}
	tagSet := map[string]bool{}
	for i, tag := range res.Tags {
		if tag != strings.ToLower(tag) || tag == "leak" || stopWords[tag] || turkishStopWords[tag] {
			t.Errorf("geçersiz etiket %q", tag)
		}
		for _, prev := range res.Tags[:i] {
			if strings.HasPrefix(tag, prev) || strings.HasPrefix(prev, tag) {
				t.Errorf("kök tekrarı: %q / %q", prev, tag)
			}
		}
		tagSet[tag] = true
	}
	for _, want := range []string{"database", "dump"} {
		if !tagSet[want] {
			t.Errorf("CTI etiketi %q bekleniyordu: %v", want, res.Tags)
		}
	}
	for _, bad := range []string{"rat", "tor"} {
		if tagSet[bad] {
			t.Errorf("alt dizgiden yanlış etiket %q: %v", bad, res.Tags)
		}
	}

	if r := s.ExtractTopKeywordsFromHTML("<p>kısa</p>", "x", 3); r.Success || r.Error == "" {
		t.Fatalf("kısa içerik hata vermeli: %+v", r)
	}
	if r := s.ExtractTopKeywordsFromHTML(readFixture(t, "forum_thread.html"), "  ", 2); len(r.Tags) != 2 || r.KeywordHits != 0 {
		t.Fatalf("boş sorgu hit 0, etiket 2: %+v", r)
	}
}

func TestQualityScoreAndContentHash(t *testing.T) {
	s := &Scraper{}
	rich := HTMLToText(readFixture(t, "forum_thread.html"))
	if got := s.calculateQualityScore(rich, "Database leak"); got < 15 {
		t.Errorf("CTI içerikli sayfa eşiği geçmeli: %d", got)
	}
	spam := strings.Repeat("free download click here to download 100% working no survey ", 5)
	if got := s.calculateQualityScore(spam, ""); got != 0 {
		t.Errorf("spam sayfa 0 almalı: %d", got)
	}
	shop := strings.Repeat("nice shoes buy now add to cart quantity: 1 ", 20)
	if got := s.calculateQualityScore(shop, ""); got != 0 {
		t.Errorf("market/ürün sayfası cezalı olmalı: %d", got)
	}

	if !containsEmailPattern("a.b@x.org c.d@y.net") || containsEmailPattern("tek@x.org") {
		t.Error("e-posta deseni en az 2 eşleşme ister")
	}
	if !containsIPAddress("1.2.3.4 5.6.7.8") || containsIPAddress("1.2.3.4") {
		t.Error("IP deseni en az 2 eşleşme ister")
	}
	if !containsHashPattern("5f4dcc3b5aa765d61d8327deb882cf99 e10adc3949ba59abbe56e057f20f883e") || containsHashPattern("deadbeef") {
		t.Error("hash deseni en az 2 eşleşme ister")
	}
	if !containsBase64Pattern(strings.Repeat("QUJD", 20)) || containsBase64Pattern("short==") {
		t.Error("base64 deseni ≥50 karakter ister")
	}

	if generateContentHash("  Hello World ") != generateContentHash("hello world") {
		t.Error("hash büyük/küçük harf ve kenar boşluğuna duyarsız olmalı")
	}
	a := strings.Repeat("x", 1000)
	if generateContentHash(a+"tail-1") != generateContentHash(a+"tail-2") {
		t.Error("hash yalnız ilk 1000 karaktere bakar")
	}
}

// TestScrapeURL_Pipeline tek URL çekimi: onion dışı ve düşük değerli URL ağa
// çıkmadan reddedilir; HTTP hatası, ikili içerik, kısa/düşük kaliteli içerik ve
// tekrar eden içerik atlanır; başarılı sonuç başlık önekli ve sınırlıdır.
func TestScrapeURL_Pipeline(t *testing.T) {
	good := readFixture(t, "forum_thread.html")
	long := "<p>" + strings.Repeat("leak database password dump credentials breach ", 400) + "</p>"
	s := newOnionScraper(t, pages{
		siteHost + "/good":  {200, good},
		siteHost + "/same":  {200, strings.Replace(good, "Mirror servers", "mirror servers", 1)},
		siteHost + "/png":   {200, "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 200)},
		siteHost + "/short": {200, "<p>hi</p>"},
		siteHost + "/spam":  {200, "<p>" + strings.Repeat("free download no survey 100% working ", 10) + "</p>"},
		siteHost + "/gone":  {404, "nope"},
		siteHost + "/long":  {200, long},
	})
	ctx := context.Background()
	base := "http://" + siteHost

	if c := s.ScrapeURL(ctx, "http://example.com/", "x"); c.Success || !strings.Contains(c.Error, ".onion") {
		t.Errorf("onion dışı: %+v", c)
	}
	if c := s.ScrapeURL(ctx, base+"/search?q=leak", "x"); c.Success || !strings.HasPrefix(c.Error, "Atlanan") {
		t.Errorf("düşük değerli URL: %+v", c)
	}
	if c := s.ScrapeURL(ctx, base+"/gone", "x"); c.Error != "HTTP 404" {
		t.Errorf("404: %+v", c)
	}
	if c := s.ScrapeURL(ctx, base+"/png", "x"); !strings.Contains(c.Error, "binary") {
		t.Errorf("ikili içerik: %+v", c)
	}
	if c := s.ScrapeURL(ctx, base+"/short", "x"); !strings.Contains(c.Error, "too short") {
		t.Errorf("kısa içerik: %+v", c)
	}
	if c := s.ScrapeURL(ctx, base+"/spam", "x"); !strings.Contains(c.Error, "low quality") {
		t.Errorf("düşük kalite: %+v", c)
	}

	c := s.ScrapeURL(ctx, base+"/good", "Leak thread")
	if !c.Success || !strings.HasPrefix(c.RawContent, "Leak thread - ") || c.ContentHash == "" || c.QualityScore < 15 || c.ContentSize != len(c.RawContent) {
		t.Fatalf("başarılı çekim: %+v", c)
	}
	if dup := s.ScrapeURL(ctx, base+"/same", "Leak thread"); !strings.Contains(dup.Error, "duplicate") {
		t.Errorf("aynı içerik (büyük/küçük harf farkı) tekrar sayılmalı: %+v", dup)
	}
	if c := s.ScrapeURL(ctx, base+"/good", "http://başlık-değil"); !strings.Contains(c.Error, "duplicate") {
		t.Errorf("tekrar çekim: %+v", c)
	}

	lc := s.ScrapeURL(ctx, base+"/long", "Found Link")
	if !lc.Success || strings.HasPrefix(lc.RawContent, "Found Link") || !strings.HasSuffix(lc.RawContent, "...(truncated)") ||
		utf8.RuneCountInString(lc.RawContent) != s.maxChars+len("...(truncated)") {
		t.Fatalf("uzun içerik maxChars'ta kesilmeli, jenerik başlık eklenmemeli: len=%d", utf8.RuneCountInString(lc.RawContent))
	}
}

type fakeRenderer struct {
	mu    sync.Mutex
	calls []string
	html  string
	err   error
	gotUA string
	gotCk string
}

func (f *fakeRenderer) Render(ctx context.Context, targetURL, cookieHeader, userAgent string) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, targetURL)
	f.gotUA, f.gotCk = userAgent, cookieHeader
	if f.err != nil {
		return "", "", "", f.err
	}
	return f.html, HTMLToText(f.html), "Rendered", nil
}

func (f *fakeRenderer) n() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

// TestExpandFetch_ProfilesAndRenderer derinleştirme çekimi: site profili çerez ve
// UA'yı uygular; 403 bot koruması, JS-kabuğu sayfa ve RenderJS profili headless
// tarayıcıya düşer; tarayıcı hatasında ham HTML'e dönülür.
func TestExpandFetch_ProfilesAndRenderer(t *testing.T) {
	var mu sync.Mutex
	var gotCookie, gotUA string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotCookie, gotUA = r.Header.Get("Cookie"), r.Header.Get("User-Agent")
		mu.Unlock()
		switch r.URL.Path {
		case "/blocked":
			w.WriteHeader(http.StatusForbidden)
		case "/spa":
			_, _ = w.Write([]byte(`<html><body><div id="root"></div><script src="/app.js"></script></body></html>`))
		default:
			_, _ = w.Write([]byte(`<html><body><a href="/next">next</a></body></html>`))
		}
	})
	s := newOnionScraper(t, h)
	ctx := context.Background()
	base := "http://" + siteHost

	if _, err := s.ExtractLinksFromURL(ctx, "http://example.com/"); err == nil {
		t.Fatal("onion dışı derinleştirme reddedilmeli")
	}

	s.SetProfileResolver(func(host string) *SiteProfile {
		if host == siteHost {
			return &SiteProfile{Cookies: "sess=abc", UserAgent: "KH-Test/1.0"}
		}
		return nil
	})
	links, err := s.ExtractLinksFromURL(ctx, base+"/plain")
	if err != nil || len(links) != 1 || links[0].URL != base+"/next" {
		t.Fatalf("bağlantılar: %+v err=%v", links, err)
	}
	mu.Lock()
	if gotCookie != "sess=abc" || gotUA != "KH-Test/1.0" {
		t.Errorf("profil uygulanmadı: cookie=%q ua=%q", gotCookie, gotUA)
	}
	mu.Unlock()

	// Tarayıcı yokken 403 düz hata; JS-kabuğu ham döner.
	if s.RendererAvailable() {
		t.Fatal("varsayılan renderer yok")
	}
	if _, err := s.ExtractLinksFromURL(ctx, base+"/blocked"); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("403: %v", err)
	}

	r := &fakeRenderer{html: `<html><body><a href="/rendered">r</a></body></html>`}
	s.SetRenderer(r)
	for _, p := range []string{"/blocked", "/spa"} {
		c := s.scrapeURLForExpand(ctx, base+p)
		if !c.Success || !c.Rendered || c.Title != "Rendered" || !strings.Contains(c.RawContent, "/rendered") {
			t.Errorf("%s tarayıcıya düşmeli: %+v", p, c)
		}
	}
	if r.gotCk != "sess=abc" || r.gotUA != "KH-Test/1.0" {
		t.Errorf("profil tarayıcıya da geçmeli: %q %q", r.gotCk, r.gotUA)
	}
	if c := s.scrapeURLForExpand(ctx, base+"/plain"); c.Rendered {
		t.Error("normal sayfa tarayıcıya gitmemeli")
	}

	// RenderJS profili: doğrudan tarayıcı.
	s.SetProfileResolver(func(string) *SiteProfile { return &SiteProfile{RenderJS: true} })
	before := r.n()
	if c := s.scrapeURLForExpand(ctx, base+"/plain"); !c.Rendered || r.n() != before+1 {
		t.Errorf("RenderJS profili tarayıcıyı kullanmalı: %+v", c)
	}

	// Tarayıcı hatası → ham HTTP içeriğine dönüş.
	r.err = errors.New("chrome yok")
	if c := s.scrapeURLForExpand(ctx, base+"/plain"); !c.Success || c.Rendered || !strings.Contains(c.RawContent, "/next") {
		t.Errorf("tarayıcı hatasında HTTP gövdesi kullanılmalı: %+v", c)
	}
	s.SetRenderer(nil)
	if s.RendererAvailable() {
		t.Error("SetRenderer(nil) devre dışı bırakmalı")
	}
}

func TestCountKeywordsAndExtractTopKeywords_OverHTTP(t *testing.T) {
	fixture := readFixture(t, "forum_thread.html")
	big := "<p>" + strings.Repeat("şifre sızıntısı leak ", 12000) + "</p>"
	s := newOnionScraper(t, pages{
		siteHost + "/t":   {200, fixture},
		siteHost + "/big": {200, big},
	})
	ctx := context.Background()
	base := "http://" + siteHost

	n, err := s.CountKeywords(ctx, base+"/t", "  LEAK ")
	if err != nil || n != 5 {
		t.Fatalf("CountKeywords = %d, %v (script içi sayılmamalı)", n, err)
	}
	if n, err := s.CountKeywords(ctx, base+"/t", " "); err != nil || n != 0 {
		t.Fatalf("boş anahtar kelime 0 olmalı: %d %v", n, err)
	}
	if _, err := s.CountKeywords(ctx, base+"/missing", "x"); err == nil {
		t.Fatal("404 hata vermeli")
	}

	res := s.ExtractTopKeywords(ctx, base+"/t", "leak", 3)
	if !res.Success || len(res.Tags) != 3 || res.KeywordHits != 5 || res.Title != "Fresh Database Leak & Combo | Shadow Board" {
		t.Fatalf("ExtractTopKeywords: %+v", res)
	}
	if !strings.Contains(res.Text, "seller.ops@protonmail.com") {
		t.Error("IOC çıkarımı için düz metin döndürülmeli")
	}
	if fail := s.ExtractTopKeywords(ctx, base+"/missing", "x", 3); fail.Success || fail.Error == "" || fail.Tags == nil {
		t.Fatalf("çekim hatası: %+v", fail)
	}

	bigRes := s.ExtractTopKeywords(ctx, base+"/big", "leak", 0)
	if len(bigRes.Text) > maxTextForArtifacts || !utf8.ValidString(bigRes.Text) || len(bigRes.Text) < maxTextForArtifacts-3 {
		t.Fatalf("metin %d bayta karakter bölmeden kısalmalı: len=%d valid=%v", maxTextForArtifacts, len(bigRes.Text), utf8.ValidString(bigRes.Text))
	}
}

func TestScrapeMultiple_CollectsAllAndHonoursCancel(t *testing.T) {
	s := newOnionScraper(t, pages{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	urls := []struct{ URL, Title string }{
		{"http://example.com/", "a"},
		{"http://" + siteHost + "/search?q=x", "b"},
	}
	var progress []int
	var mu sync.Mutex
	res := s.ScrapeMultiple(ctx, urls, 2, func(done, total int) {
		mu.Lock()
		progress = append(progress, done)
		mu.Unlock()
		if done == total {
			cancel() // istekler arası gecikmeyi kısa kes
		}
	})
	if len(res) != 2 {
		t.Fatalf("sonuç sayısı %d", len(res))
	}
	for _, c := range res {
		if c.Success || c.Error == "" {
			t.Errorf("geçersiz URL'ler başarısız olmalı: %+v", c)
		}
	}
	if len(progress) != 2 || progress[len(progress)-1] != 2 {
		t.Errorf("ilerleme: %v", progress)
	}
}
