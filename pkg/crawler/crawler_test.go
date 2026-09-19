package crawler

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"keywordhunter-mvp/pkg/storage"
)

func TestNormalizeURL(t *testing.T) {
	cases := map[string]string{
		"http://abc.onion/page#frag": "http://abc.onion/page",
		"http://abc.onion/page/":     "http://abc.onion/page",
		"  HTTP://ABC.onion/X/  ":    "http://abc.onion/x",
		"http://abc.onion/":          "http://abc.onion",
		"":                           "",
		"http://abc.onion/a?b=1#c":   "http://abc.onion/a?b=1",
	}
	for in, want := range cases {
		if got := normalizeURL(in); got != want {
			t.Errorf("normalizeURL(%q)=%q, want %q", in, got, want)
		}
	}
	// Aynı sayfanın varyantları aynı anahtara inmeli (ziyaret-tekilleştirme)
	a, b := normalizeURL("http://abc.onion/p/#x"), normalizeURL("http://abc.onion/p")
	if a != b {
		t.Errorf("varyantlar eşit olmalı: %q vs %q", a, b)
	}
}

func TestTitleFromURL(t *testing.T) {
	cases := map[string]string{
		"http://abc.onion/some-page_name":  "[Crawl] some page name",
		"http://abc.onion/dir/leak-db?x=1": "[Crawl] dir/leak db",
		"https://abc.onion/":               "[Crawl] abc.onion",
		"http://abc.onion":                 "[Crawl] abc.onion",
	}
	for in, want := range cases {
		if got := titleFromURL(in); got != want {
			t.Errorf("titleFromURL(%q)=%q, want %q", in, got, want)
		}
	}
}

func newDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "crawler_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestSubmit_RejectsNonOnion: tohum .onion değilse hiç iş satırı oluşmamalı (SSRF/kapsam).
func TestSubmit_RejectsNonOnion(t *testing.T) {
	db := newDB(t)
	r := New(db, nil)
	r.Stop() // işçi durur; nil scraper'a asla dokunulmaz

	for _, seed := range []string{"http://example.com/", "http://127.0.0.1/", "not a url", ""} {
		if _, err := r.Submit(Request{SeedURL: seed}); err == nil {
			t.Errorf("Submit(%q): hata bekleniyordu", seed)
		}
	}
	if jobs, _ := db.ListCrawlJobs(10); len(jobs) != 0 {
		t.Fatalf("reddedilen tohumlar iş satırı üretmemeli, %d satır var", len(jobs))
	}
}

// TestSubmit_ClampsLimits: derinlik/sayfa sınırları MaxDepth/MaxPages'e kırpılır,
// boş sorgu "crawl:<host>" olur. Runner durdurulduğu için Submit ErrBusy döner ama
// satır (kırpılmış değerlerle) yazılmış olur — gerçek kırpma kodu bu yoldan sınanır.
func TestSubmit_ClampsLimits(t *testing.T) {
	db := newDB(t)
	r := New(db, nil)
	r.Stop()

	seed := "http://submariwvjhyg7acbrw3thxjxxsatwpieoioenyd4lde6y2rrnzkcwad.onion/"
	_, err := r.Submit(Request{SeedURL: seed, Depth: 99, MaxPages: 99999})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("durdurulmuş runner ErrBusy dönmeli, err=%v", err)
	}
	jobs, err := db.ListCrawlJobs(10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("1 iş satırı bekleniyordu: n=%d err=%v", len(jobs), err)
	}
	j := jobs[0]
	if j.Depth != MaxDepth || j.MaxPages != MaxPages {
		t.Errorf("kırpma yanlış: depth=%d (want %d) pages=%d (want %d)", j.Depth, MaxDepth, j.MaxPages, MaxPages)
	}
	if !strings.HasPrefix(j.Query, "crawl:") || !strings.Contains(j.Query, ".onion") {
		t.Errorf("boş sorgu 'crawl:<host>' olmalı: %q", j.Query)
	}
	if j.Status != StatusQueued {
		t.Errorf("başlangıç durumu %q olmalı, %q", StatusQueued, j.Status)
	}

	// Alt sınır: depth<1 → 1, pages<1 → 50
	if _, err := r.Submit(Request{SeedURL: seed, Depth: 0, MaxPages: 0}); !errors.Is(err, ErrBusy) {
		t.Fatalf("ErrBusy bekleniyordu, %v", err)
	}
	jobs, _ = db.ListCrawlJobs(10)
	var low *storage.CrawlJob
	for i := range jobs {
		if jobs[i].Depth == 1 {
			low = &jobs[i]
		}
	}
	if low == nil || low.MaxPages != 50 {
		t.Errorf("alt sınır kırpması yanlış: %+v", low)
	}
}
