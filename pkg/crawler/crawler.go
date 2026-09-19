// Package crawler onion siteleri için sınırlı, nazik bir örümcek: tohum
// URL'den başlar, BFS ile en fazla `depth` seviye ve `maxPages` sayfa gezer,
// keşfedilen sayfaları bulgu (search_results, Source="Crawler") ve graf düğümü
// olarak kaydeder. Tek işçi, istekler arası gecikme, iptal edilebilir.
package crawler

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/scraper"
	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

const (
	MaxDepth     = 3
	MaxPages     = 300
	pageTimeout  = 90 * time.Second
	minDelay     = 700 * time.Millisecond
	jitterDelay  = 900 * time.Millisecond
	queueSize    = 16
	StatusQueued = "pending"
)

var ErrBusy = errors.New("örümcek kuyruğu dolu")

// Request yeni bir tarama isteği.
type Request struct {
	SeedURL    string
	Depth      int
	MaxPages   int
	SameDomain bool
	Query      string // bulgulara yazılacak sorgu etiketi (boşsa "crawl:<host>")
}

// Runner tek işçili örümcek kuyruğu.
type Runner struct {
	db      *storage.DB
	scraper *scraper.Scraper
	queue   chan string
	wg      sync.WaitGroup
	once    sync.Once

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	stopped bool
}

// New kuyruğu oluşturur ve işçiyi başlatır.
func New(db *storage.DB, sc *scraper.Scraper) *Runner {
	r := &Runner{db: db, scraper: sc, queue: make(chan string, queueSize), cancels: map[string]context.CancelFunc{}}
	_ = db.ResetRunningCrawlJobs()
	r.wg.Add(1)
	go r.loop()
	return r
}

// Stop kuyruğu kapatır, çalışan işi iptal eder ve bekler.
func (r *Runner) Stop() {
	r.once.Do(func() {
		r.mu.Lock()
		r.stopped = true
		for _, c := range r.cancels {
			c()
		}
		r.mu.Unlock()
		close(r.queue)
	})
	r.wg.Wait()
}

// Submit işi doğrular, kaydeder ve kuyruğa alır.
func (r *Runner) Submit(req Request) (*storage.CrawlJob, error) {
	req.SeedURL = strings.TrimSpace(req.SeedURL)
	if !shared.IsOnionURL(req.SeedURL) {
		return nil, fmt.Errorf("tohum URL geçerli bir .onion adresi olmalı")
	}
	if req.Depth < 1 {
		req.Depth = 1
	}
	if req.Depth > MaxDepth {
		req.Depth = MaxDepth
	}
	if req.MaxPages < 1 {
		req.MaxPages = 50
	}
	if req.MaxPages > MaxPages {
		req.MaxPages = MaxPages
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		query = "crawl:" + shared.ExtractDomain(req.SeedURL)
	}
	job := storage.CrawlJob{
		ID: uuid.NewString(), SeedURL: req.SeedURL, Query: query,
		Depth: req.Depth, MaxPages: req.MaxPages, SameDomain: req.SameDomain, Status: StatusQueued,
	}
	if err := r.db.CreateCrawlJob(job); err != nil {
		return nil, err
	}
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return nil, ErrBusy
	}
	select {
	case r.queue <- job.ID:
	default:
		_ = r.db.MarkCrawlFinished(job.ID, "failed", "Kuyruk dolu")
		return nil, ErrBusy
	}
	shared.Streamer.BroadcastLog("info", fmt.Sprintf("Örümcek kuyruğa alındı: %s (derinlik %d, en çok %d sayfa)", shared.Truncate(req.SeedURL, 60), req.Depth, req.MaxPages), "")
	return &job, nil
}

// Cancel bekleyen/çalışan işi iptal eder.
func (r *Runner) Cancel(id string) error {
	job, err := r.db.GetCrawlJob(id)
	if err != nil {
		return err
	}
	switch job.Status {
	case "pending":
		return r.db.MarkCrawlFinished(id, "cancelled", "Kullanıcı tarafından iptal edildi")
	case "running":
		r.mu.Lock()
		c, ok := r.cancels[id]
		r.mu.Unlock()
		if ok {
			c()
		}
	}
	return nil
}

func (r *Runner) loop() {
	defer r.wg.Done()
	for id := range r.queue {
		r.run(id)
	}
}

type frame struct {
	url    string
	depth  int
	parent int64 // parent graph node id (0 = kök)
}

func (r *Runner) run(id string) {
	job, err := r.db.GetCrawlJob(id)
	if err != nil {
		logger.Warn("CRAWL GET ERROR: %s - %v", id, err)
		return
	}
	if job.Status != StatusQueued {
		return
	}
	if err := r.db.MarkCrawlRunning(id); err != nil {
		logger.Warn("CRAWL RUNNING ERROR: %s - %v", id, err)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.cancels[id] = cancel
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		delete(r.cancels, id)
		r.mu.Unlock()
	}()

	seedDomain := shared.ExtractDomain(job.SeedURL)
	visited := map[string]bool{}
	queue := []frame{{url: job.SeedURL, depth: 0, parent: 0}}
	pages, found, saved := 0, 0, 0
	start := time.Now()

	logger.Info("CRAWL START: %s (derinlik %d, en çok %d sayfa, aynı-domain=%v)", job.SeedURL, job.Depth, job.MaxPages, job.SameDomain)

	for len(queue) > 0 && pages < job.MaxPages {
		select {
		case <-ctx.Done():
			r.finish(id, "cancelled", "İptal edildi", pages, found, saved, start)
			return
		default:
		}

		f := queue[0]
		queue = queue[1:]
		norm := normalizeURL(f.url)
		if norm == "" || visited[norm] {
			continue
		}
		visited[norm] = true

		pctx, pcancel := context.WithTimeout(ctx, pageTimeout)
		links, err := r.scraper.ExtractLinksFromURL(pctx, f.url)
		pcancel()
		pages++

		if err != nil {
			shared.Streamer.BroadcastLog("engine_end", fmt.Sprintf("✗ %s: %v", shared.Truncate(f.url, 50), err), "Crawler")
			r.db.UpdateCrawlProgress(id, pages, found, saved)
			r.politeDelay(ctx)
			continue
		}

		// Bu sayfayı bulgu olarak kaydet (arama sonucu gibi, sınıflandırmalı)
		if n, _ := r.savePage(f.url, job.Query); n > 0 {
			saved += n
		}
		found++

		// Bir sonraki seviye
		if f.depth < job.Depth {
			for _, link := range links {
				if !shared.IsOnionURL(link.URL) {
					continue
				}
				if job.SameDomain && shared.ExtractDomain(link.URL) != seedDomain {
					continue
				}
				nu := normalizeURL(link.URL)
				if nu == "" || visited[nu] {
					continue
				}
				queue = append(queue, frame{url: link.URL, depth: f.depth + 1, parent: 0})
			}
		}

		shared.Streamer.BroadcastLog("engine_end", fmt.Sprintf("✓ sayfa %d/%d · %d link · derinlik %d", pages, job.MaxPages, len(links), f.depth), "Crawler")
		r.db.UpdateCrawlProgress(id, pages, found, saved)
		r.politeDelay(ctx)
	}

	elapsed := time.Since(start).Round(time.Second)
	logger.Info("CRAWL DONE: %s → %d sayfa, %d bulgu, %d yeni (%v)", job.SeedURL, pages, found, saved, elapsed)
	r.finish(id, "completed", "", pages, found, saved, start)
}

// savePage keşfedilen bir sayfayı search_results'a yazar (Source="Crawler").
func (r *Runner) savePage(pageURL, query string) (int, error) {
	title := titleFromURL(pageURL)
	res := storage.SearchResult{Title: title, URL: pageURL, Source: "Crawler", Query: query}
	// CTI sınıflandırması title+URL üzerinden (scraper zaten içeriği değerlendirdi)
	return r.db.SaveResults([]storage.SearchResult{res})
}

func (r *Runner) finish(id, status, msg string, pages, found, saved int, start time.Time) {
	r.db.UpdateCrawlProgress(id, pages, found, saved)
	_ = r.db.MarkCrawlFinished(id, status, msg)
	if status == "completed" {
		shared.Streamer.BroadcastLog("success", fmt.Sprintf("Örümcek tamamlandı: %d sayfa, %d yeni bulgu", pages, saved), "Crawler")
	}
}

func (r *Runner) politeDelay(ctx context.Context) {
	d := minDelay + time.Duration(rand.Int63n(int64(jitterDelay)))
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

// normalizeURL fragment ve sondaki / kaldırılmış karşılaştırma anahtarı.
func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if i := strings.Index(u, "#"); i >= 0 {
		u = u[:i]
	}
	u = strings.TrimRight(u, "/")
	return strings.ToLower(u)
}

func titleFromURL(u string) string {
	p := strings.TrimPrefix(strings.TrimPrefix(u, "http://"), "https://")
	if i := strings.Index(p, "?"); i >= 0 {
		p = p[:i]
	}
	parts := strings.SplitN(p, "/", 2)
	if len(parts) == 2 && parts[1] != "" {
		seg := strings.Trim(parts[1], "/")
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "-", " "), "_", " ")
		return "[Crawl] " + shared.Truncate(seg, 90)
	}
	return "[Crawl] " + shared.Truncate(parts[0], 40)
}
