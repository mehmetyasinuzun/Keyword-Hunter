package search

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
)

func onion(c string) string { return "http://" + strings.Repeat(c, 56) + ".onion" }

func loadEngineFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/engines/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestParseResults_EngineFixtures her motor biçimini taklit eden küçük HTML
// örnekleri: doğrudan bağlantı + yeşil URL satırı (Tordex), göreli yönlendirme
// içinde gömülü hedef (Ahmia), dl/dt listesi ve reklam (Torch), yalnız düz metin
// URL listesi (Excavator/TorNet tarzı). Motorun kendi sayfalama bağlantıları,
// reklam/ikili dosyalar ve geçersiz onion'lar sonuç sayılmaz.
func TestParseResults_EngineFixtures(t *testing.T) {
	type want struct {
		title   string
		minHits int
	}
	cases := []struct {
		file string
		want map[string]want
	}{
		{"tordex.html", map[string]want{
			onion("d") + "/":            {"Hidden Wiki & Links", 0},
			onion("e") + "/forum/leaks": {"Leak Forum — fresh leak dumps", 4},
		}},
		{"ahmia.html", map[string]want{
			onion("f") + "/market": {"Market – Leak DB", 2},
			onion("g") + "/":       {"[Onion: gggggggg...]", 0},
		}},
		{"torch.html", map[string]want{
			onion("h") + "/paste/abc": {"Paste: leak dump", 2},
		}},
		{"textlist.html", map[string]want{
			onion("j") + "/forum/leak-thread":  {"[Forum/leak thread]", 1},
			onion("k") + "/some_page.php?id=1": {"[Some page]", 0},
		}},
	}
	s := &Searcher{}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			res := s.deduplicate(s.parseResults(loadEngineFixture(t, c.file), "Fixture", "leak"))
			got := map[string]Result{}
			for _, r := range res {
				got[r.URL] = r
			}
			if len(got) != len(c.want) {
				var urls []string
				for u := range got {
					urls = append(urls, u)
				}
				sort.Strings(urls)
				t.Fatalf("sonuç sayısı %d, beklenen %d: %v", len(got), len(c.want), urls)
			}
			for u, w := range c.want {
				r, ok := got[u]
				if !ok {
					t.Errorf("eksik sonuç %s", u)
					continue
				}
				if r.Title != w.title {
					t.Errorf("%s başlık = %q, beklenen %q", u, r.Title, w.title)
				}
				if r.KeywordHits < w.minHits {
					t.Errorf("%s hit = %d, beklenen >= %d", u, r.KeywordHits, w.minHits)
				}
				if r.Source != "Fixture" || r.Criticality < 1 || r.Category == "" {
					t.Errorf("%s alanları: %+v", u, r)
				}
			}
		})
	}
}

func TestDeduplicate_KeepsFirstAndDropsInvalid(t *testing.T) {
	s := &Searcher{}
	in := []Result{
		{URL: onion("d") + "/", Source: "A"},
		{URL: onion("d") + "/", Source: "B"},
		{URL: "http://short.onion/", Source: "A"},
		{URL: onion("e") + "/x.png", Source: "A"},
		{URL: onion("e") + "/", Source: "C"},
	}
	out := s.deduplicate(in)
	if len(out) != 2 || out[0].Source != "A" || out[1].URL != onion("e")+"/" {
		t.Fatalf("tekilleştirme: %+v", out)
	}
}

func TestExtractTitleFromURL(t *testing.T) {
	cases := map[string]string{
		onion("d"):                        "[Onion: dddddddd...]",
		onion("d") + "/":                  "[Onion: dddddddd...]",
		onion("d") + "/a":                 "[Onion: dddddddd...]", // ≤3 karakterlik yol
		onion("d") + "/leak-db_2026.html": "[Leak db 2026]",
		onion("d") + "/çarşı-pazar?x=1":   "[Çarşı pazar]",
		"https://example.com/":            "[Unknown Site]",
	}
	for in, want := range cases {
		if got := extractTitleFromURL(in); got != want {
			t.Errorf("extractTitleFromURL(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

// engineHandler yol + sayfa → onion listesi; bilinmeyen yol 404 (yeniden denenmez).
type engineHandler struct {
	mu       sync.Mutex
	requests []string
	pages    map[string][]string
}

func (h *engineHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Path + "#" + r.URL.Query().Get("page")
	h.mu.Lock()
	h.requests = append(h.requests, key+"?q="+r.URL.Query().Get("q"))
	links, ok := h.pages[key]
	h.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	var b strings.Builder
	for _, l := range links {
		fmt.Fprintf(&b, `<div><a href="%s">result about leak</a></div>`, l)
	}
	_, _ = w.Write([]byte(b.String()))
}

// TestSearchAllPages_PaginationAndDedup sayfalama: yeni sonuç üretmeyen sayfada
// durulur, sayfalamasız motora 2. sayfa istenmez, motorlar arası tekrarlar
// tekilleşir, hatalı motor diğerlerini etkilemez, sayfa sayısı 1..MaxSearchPages'e
// kırpılır.
func TestSearchAllPages_PaginationAndDedup(t *testing.T) {
	h := &engineHandler{pages: map[string][]string{
		"/paged#":  {onion("d") + "/1", onion("e") + "/2"},
		"/paged#2": {onion("e") + "/2", onion("f") + "/3"},
		"/paged#3": {onion("f") + "/3"},
		"/paged#4": {onion("g") + "/never"},
		"/single#": {onion("d") + "/1", onion("h") + "/single"},
	}}
	srv := httptest.NewServer(h)
	defer srv.Close()

	orig := SearchEngines
	t.Cleanup(func() { SearchEngines = orig })
	SearchEngines = []Engine{
		{Name: "Paged", URL: srv.URL + "/paged?q={query}", PageParam: "page", DefaultActive: true},
		{Name: "Single", URL: srv.URL + "/single?q={query}", DefaultActive: true},
		{Name: "Broken", URL: srv.URL + "/broken?q={query}", DefaultActive: true},
		{Name: "Off", URL: srv.URL + "/single?q={query}", DefaultActive: false},
	}
	s := &Searcher{client: srv.Client()}

	res := s.SearchAllPages(context.Background(), "leak db", 99)
	var urls []string
	for _, r := range res {
		urls = append(urls, r.URL)
	}
	sort.Strings(urls)
	want := []string{onion("d") + "/1", onion("e") + "/2", onion("f") + "/3", onion("h") + "/single"}
	if strings.Join(urls, ",") != strings.Join(want, ",") {
		t.Fatalf("sonuçlar:\n got %v\nwant %v", urls, want)
	}

	h.mu.Lock()
	reqs := append([]string(nil), h.requests...)
	h.mu.Unlock()
	count := map[string]int{}
	for _, r := range reqs {
		if !strings.HasSuffix(r, "?q=leak db") {
			t.Errorf("sorgu kodlanıp iletilmeli: %s", r)
		}
		count[strings.SplitN(r, "?", 2)[0]]++
	}
	if count["/paged#4"] != 0 || count["/paged#3"] != 1 || count["/single#2"] != 0 || count["/single#"] != 1 {
		t.Fatalf("sayfa istekleri: %v", reqs)
	}

	// Motor özeti: 3 etkin motordan Broken hata verir → 2 yanıt.
	if _, st := s.SearchAllStats(context.Background(), "leak"); st.Engines != 3 || st.Answered != 2 {
		t.Fatalf("motor özeti: %+v", st)
	}

	// Filtre: yalnız "Single" etkin → yalnız onun sonuçları
	s.SetEngineFilter(func(name string) bool { return name == "Single" })
	if got := s.SearchAll(context.Background(), "leak"); len(got) != 2 {
		t.Fatalf("filtrelenmiş arama: %+v", got)
	}
	// Hiç etkin motor yok → nil
	s.SetEngineFilter(func(string) bool { return false })
	if got := s.SearchAll(context.Background(), "leak"); got != nil {
		t.Fatalf("motorsuz arama nil dönmeli: %+v", got)
	}
	if got, st := s.SearchAllStats(context.Background(), "leak"); got != nil || st != (SearchStats{}) {
		t.Fatalf("motorsuz arama özeti: %v %+v", got, st)
	}
}

func TestSearchEnginePage_Errors(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	s := &Searcher{client: srv.Client()}
	if _, err := s.searchEnginePage(context.Background(), Engine{Name: "N", URL: srv.URL + "/?q={query}"}, "x", 2); err == nil {
		t.Fatal("sayfalamasız motorda 2. sayfa hata vermeli")
	}
	if _, err := s.searchEnginePage(context.Background(), Engine{Name: "N", URL: srv.URL + "/?q={query}"}, "x", 1); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("404 hata vermeli: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.searchEnginePage(ctx, Engine{Name: "N", URL: srv.URL + "/?q={query}"}, "x", 1); err == nil {
		t.Fatal("iptal edilmiş bağlam hata vermeli")
	}
}

func TestEngineHelpers(t *testing.T) {
	if EngineCount() != len(SearchEngines) || len(EngineNames()) != EngineCount() {
		t.Fatal("motor sayısı yardımcıları tutarsız")
	}
	if !isSearchEngineDomain("http://TORDEXU73JOYWAPK2TXDR54JED4IMQLEDPCVCUF75QSAS2GWDGKSVNYD.onion/x") {
		t.Error("motor alan adı büyük/küçük harften bağımsız tanınmalı")
	}
	if !isSearchEngineDomain("https://ahmia.fi/search") || isSearchEngineDomain(onion("d")) {
		t.Error("eski/ayna motor alan adları")
	}
	if _, err := New("127.0.0.1:9050"); err != nil {
		t.Fatal(err)
	}
}
