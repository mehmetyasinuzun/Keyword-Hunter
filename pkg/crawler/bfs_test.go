package crawler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/storage"
)

var (
	seedHost  = strings.Repeat("s", 56) + ".onion"
	otherHost = strings.Repeat("o", 56) + ".onion"
	seedURL   = "http://" + seedHost + "/"
)

// fakeSite Tor'suz bellek içi site grafı: sayfa HTML'i gerçek
// scraper.ExtractLinks ile ayrıştırılır, yalnız ağ katmanı sahtedir.
type fakeSite struct {
	mu      sync.Mutex
	pages   map[string]string // URL → HTML
	fetched []string
	block   chan struct{} // verilirse her çekim ctx bitene ya da kapanana dek bekler
	entered chan string
}

func (f *fakeSite) ExtractLinksFromURL(ctx context.Context, u string) ([]scraper.ExtractedLink, error) {
	f.mu.Lock()
	f.fetched = append(f.fetched, u)
	html, ok := f.pages[u]
	block, entered := f.block, f.entered
	f.mu.Unlock()
	if entered != nil {
		select {
		case entered <- u:
		default:
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if !ok {
		return nil, errors.New("site erişilemez")
	}
	return (&scraper.Scraper{}).ExtractLinks(u, html)
}

func (f *fakeSite) fetchedURLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.fetched...)
}

func page(links ...string) string {
	var b strings.Builder
	b.WriteString("<html><body>")
	for _, l := range links {
		fmt.Fprintf(&b, `<a href="%s">link</a>`, l)
	}
	b.WriteString("</body></html>")
	return b.String()
}

func newTestRunner(t *testing.T, site *fakeSite) (*Runner, *storage.DB) {
	t.Helper()
	db := newDB(t)
	r := newRunner(db, site)
	r.delay = func(context.Context) {}
	t.Cleanup(r.Stop)
	return r, db
}

func waitJob(t *testing.T, db *storage.DB, id string, statuses ...string) *storage.CrawlJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := db.GetCrawlJob(id)
		if err == nil {
			for _, s := range statuses {
				if j.Status == s {
					return j
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := db.GetCrawlJob(id)
	t.Fatalf("iş %s %v durumuna geçmedi: %+v", id, statuses, j)
	return nil
}

// TestRun_BFSRespectsDepth tohum derinlik 0'dır; Depth=N iken en fazla N
// seviye aşağı inilir, sıra genişlik-öncedir.
func TestRun_BFSRespectsDepth(t *testing.T) {
	base := "http://" + seedHost
	site := &fakeSite{pages: map[string]string{
		seedURL:            page("/a", "/b"),
		base + "/a":        page("/a/deep"),
		base + "/b":        page(),
		base + "/a/deep":   page("/a/deeper"),
		base + "/a/deeper": page(),
	}}
	r, db := newTestRunner(t, site)
	job, err := r.Submit(Request{SeedURL: seedURL, Depth: 2, MaxPages: 100, SameDomain: true})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, db, job.ID, "completed")

	want := []string{seedURL, base + "/a", base + "/b", base + "/a/deep"}
	if got := site.fetchedURLs(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("BFS sırası/derinliği:\n got %v\nwant %v", got, want)
	}
	if j.PagesDone != 4 || j.FoundCount != 4 || j.SavedCount != 4 {
		t.Fatalf("sayaçlar: pages=%d found=%d saved=%d", j.PagesDone, j.FoundCount, j.SavedCount)
	}
	if j.StartedAt == nil || j.FinishedAt == nil {
		t.Fatalf("başlangıç/bitiş zamanı yazılmalı: %+v", j)
	}

	// Sayfalar bulgu olarak kaydedilir: Source=Crawler, sorgu "crawl:<host>".
	res, total, err := db.GetResultsFiltered(storage.ResultFilter{Source: "Crawler", Limit: 50})
	if err != nil || total != 4 {
		t.Fatalf("kaydedilen bulgu=%d err=%v", total, err)
	}
	for _, r := range res {
		if r.Query != "crawl:"+seedHost || !strings.HasPrefix(r.Title, "[Crawl] ") {
			t.Errorf("bulgu alanları: %+v", r)
		}
	}
}

// TestRun_MaxPagesCap sayfa sınırı başarısız çekimleri de sayar ve kesin uygulanır.
func TestRun_MaxPagesCap(t *testing.T) {
	base := "http://" + seedHost
	var links []string
	pages := map[string]string{}
	for i := 0; i < 20; i++ {
		p := fmt.Sprintf("/p%d", i)
		links = append(links, p)
		if i%2 == 0 { // tekler "erişilemez"
			pages[base+p] = page()
		}
	}
	pages[seedURL] = page(links...)
	site := &fakeSite{pages: pages}
	r, db := newTestRunner(t, site)
	job, err := r.Submit(Request{SeedURL: seedURL, Depth: 3, MaxPages: 5, SameDomain: true})
	if err != nil {
		t.Fatal(err)
	}
	j := waitJob(t, db, job.ID, "completed")
	if n := len(site.fetchedURLs()); n != 5 {
		t.Fatalf("MaxPages=5 iken %d sayfa çekildi", n)
	}
	// seed + p0 + p2 başarılı; p1, p3 başarısız → pages 5, found 3
	if j.PagesDone != 5 || j.FoundCount != 3 {
		t.Fatalf("pages=%d found=%d", j.PagesDone, j.FoundCount)
	}
}

// TestRun_SameDomainAndOnionOnly aynı-domain kipinde yalnız tohum host'u
// gezilir; kapalıyken diğer .onion'lar da gezilir ama clearnet/IP/geçersiz
// onion asla (SSRF/kapsam), ayrıca aynı sayfanın varyantları bir kez çekilir.
func TestRun_SameDomainAndOnionOnly(t *testing.T) {
	base := "http://" + seedHost
	other := "http://" + otherHost + "/x"
	seedPage := page(
		"/in", "/in#frag", "/in/", base+"/IN", // aynı sayfa varyantları
		other,
		"http://example.com/", "http://127.0.0.1:8080/admin", "http://short.onion/",
		"mailto:a@b.c", "javascript:alert(1)", "ftp://"+seedHost+"/f",
	)
	newSite := func() *fakeSite {
		return &fakeSite{pages: map[string]string{
			seedURL:      seedPage,
			base + "/in": page(),
			other:        page(),
		}}
	}

	t.Run("same-domain", func(t *testing.T) {
		site := newSite()
		r, db := newTestRunner(t, site)
		job, err := r.Submit(Request{SeedURL: seedURL, Depth: 1, MaxPages: 50, SameDomain: true})
		if err != nil {
			t.Fatal(err)
		}
		waitJob(t, db, job.ID, "completed")
		got := site.fetchedURLs()
		if len(got) != 2 || got[0] != seedURL || got[1] != base+"/in" {
			t.Fatalf("aynı-domain gezinti: %v", got)
		}
	})

	t.Run("any-onion", func(t *testing.T) {
		site := newSite()
		r, db := newTestRunner(t, site)
		job, err := r.Submit(Request{SeedURL: seedURL, Depth: 1, MaxPages: 50, SameDomain: false})
		if err != nil {
			t.Fatal(err)
		}
		waitJob(t, db, job.ID, "completed")
		got := site.fetchedURLs()
		if len(got) != 3 || got[0] != seedURL || got[1] != base+"/in" || got[2] != other {
			t.Fatalf("onion-only gezinti: %v", got)
		}
		for _, u := range got {
			if strings.Contains(u, "example.com") || strings.Contains(u, "127.0.0.1") || strings.Contains(u, "short.onion") {
				t.Fatalf("onion dışı/geçersiz hedef çekildi: %s", u)
			}
		}
	})
}

func TestRun_CustomQueryLabel(t *testing.T) {
	site := &fakeSite{pages: map[string]string{seedURL: page()}}
	r, db := newTestRunner(t, site)
	job, err := r.Submit(Request{SeedURL: "  " + seedURL + " ", Depth: 1, MaxPages: 1, Query: "  vaka-42 "})
	if err != nil {
		t.Fatal(err)
	}
	if job.Query != "vaka-42" || job.SeedURL != seedURL {
		t.Fatalf("girdi kırpılmalı: %+v", job)
	}
	waitJob(t, db, job.ID, "completed")
	if known, _ := db.GetKnownURLsForQuery("vaka-42"); !known[seedURL] {
		t.Fatal("bulgu özel sorgu etiketiyle kaydedilmeli")
	}
}

// TestCancel_RunningAndPending çalışan iş bağlam iptaliyle durur ("cancelled");
// kuyrukta bekleyen iş hiç çalışmadan iptal edilir.
func TestCancel_RunningAndPending(t *testing.T) {
	site := &fakeSite{
		pages:   map[string]string{seedURL: page("/next")},
		block:   make(chan struct{}),
		entered: make(chan string, 4),
	}
	r, db := newTestRunner(t, site)
	running, err := r.Submit(Request{SeedURL: seedURL, Depth: 1, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}
	<-site.entered // işçi ilk sayfada bekliyor
	pending, err := r.Submit(Request{SeedURL: "http://" + otherHost + "/", Depth: 1, MaxPages: 10})
	if err != nil {
		t.Fatal(err)
	}

	if err := r.Cancel(pending.ID); err != nil {
		t.Fatal(err)
	}
	if j := waitJob(t, db, pending.ID, "cancelled"); j.Message == "" {
		t.Error("iptal iletisi yazılmalı")
	}
	if err := r.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
	waitJob(t, db, running.ID, "cancelled")

	// Bekleyen iş kuyruktan çıktığında çalıştırılmaz.
	time.Sleep(20 * time.Millisecond)
	for _, u := range site.fetchedURLs() {
		if strings.Contains(u, otherHost) {
			t.Fatalf("iptal edilmiş bekleyen iş çalıştırıldı: %v", site.fetchedURLs())
		}
	}
	if err := r.Cancel("yok"); err == nil {
		t.Error("bilinmeyen iş için hata beklenirdi")
	}
	// Bitmiş işi iptal etmek no-op'tur.
	if err := r.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
}

// TestSubmit_QueueFull işçi meşgulken kuyruk taşarsa iş "failed" yazılır ve ErrBusy döner.
func TestSubmit_QueueFull(t *testing.T) {
	site := &fakeSite{pages: map[string]string{}, block: make(chan struct{}), entered: make(chan string, 1)}
	r, db := newTestRunner(t, site)
	if _, err := r.Submit(Request{SeedURL: seedURL}); err != nil {
		t.Fatal(err)
	}
	<-site.entered
	for i := 0; i < queueSize; i++ {
		if _, err := r.Submit(Request{SeedURL: seedURL}); err != nil {
			t.Fatalf("kuyruk %d. işte doldu: %v", i, err)
		}
	}
	if _, err := r.Submit(Request{SeedURL: seedURL}); !errors.Is(err, ErrBusy) {
		t.Fatalf("taşan iş ErrBusy dönmeli: %v", err)
	}
	jobs, _ := db.ListCrawlJobs(100)
	failed := 0
	for _, j := range jobs {
		if j.Status == "failed" && j.Message == "Kuyruk dolu" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("1 'Kuyruk dolu' işi beklenirdi, %d", failed)
	}
	close(site.block)
}

// TestSubmitStop_NoSendOnClosedQueue kapanış sırasında gelen Submit, kapanmış
// kuyruğa gönderip panik etmemeli (Stop ile Submit eşzamanlı; -race ile koşar).
func TestSubmitStop_NoSendOnClosedQueue(t *testing.T) {
	db := newDB(t)
	for round := 0; round < 20; round++ {
		r := newRunner(db, &fakeSite{pages: map[string]string{}})
		r.delay = func(context.Context) {}
		var wg sync.WaitGroup
		panics := make(chan any, 8)
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() {
					if p := recover(); p != nil {
						panics <- p
					}
				}()
				for k := 0; k < 5; k++ {
					_, _ = r.Submit(Request{SeedURL: seedURL})
				}
			}()
		}
		r.Stop()
		wg.Wait()
		close(panics)
		if p, ok := <-panics; ok {
			t.Fatalf("Stop sırasında Submit panik etti: %v", p)
		}
	}
}
