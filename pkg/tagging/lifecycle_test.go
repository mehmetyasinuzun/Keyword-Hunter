package tagging

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

// scriptedExtractor URL başına sonuç döner; block verilirse ctx bitene ya da
// kapanana dek bekler (iptal/kapanış senaryoları).
type scriptedExtractor struct {
	mu      sync.Mutex
	byURL   map[string]scraper.TagResult
	calls   []string
	block   chan struct{}
	entered chan string
}

func (s *scriptedExtractor) ExtractTopKeywords(ctx context.Context, urlStr, searchQuery string, maxTags int) scraper.TagResult {
	s.mu.Lock()
	s.calls = append(s.calls, urlStr)
	res := s.byURL[urlStr]
	block, entered := s.block, s.entered
	s.mu.Unlock()
	if entered != nil {
		select {
		case entered <- urlStr:
		default:
		}
	}
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return scraper.TagResult{Error: ctx.Err().Error()}
		}
	}
	return res
}

func (s *scriptedExtractor) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func resultURL(i int) string {
	return fmt.Sprintf("http://%s.onion/r%d", strings.Repeat("t", 56), i)
}

// seedResults farklı URL'li n bulgu ekler (title "Genel", sinyalsiz).
func seedResults(t *testing.T, db *storage.DB, n int) []int64 {
	t.Helper()
	var ids []int64
	for i := 0; i < n; i++ {
		var saved []int64
		if _, err := db.SaveResultsWithIDs([]storage.SearchResult{{Title: "sayfa", URL: resultURL(i), Source: "E", Query: "q"}}, &saved); err != nil || len(saved) != 1 {
			t.Fatalf("seed: %v %v", saved, err)
		}
		ids = append(ids, saved[0])
	}
	return ids
}

func waitTagJob(t *testing.T, db *storage.DB, id string, statuses ...string) *storage.TaggingJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := db.GetTaggingJob(id)
		if err == nil {
			for _, s := range statuses {
				if j.Status == s {
					return j
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := db.GetTaggingJob(id)
	t.Fatalf("iş %s %v durumuna geçmedi: %+v", id, statuses, j)
	return nil
}

func newRunner(t *testing.T, ex KeywordExtractor) (*BatchRunner, *storage.DB) {
	t.Helper()
	db := newTestDB(t)
	r := NewBatchRunner(db, NewEngine(db, ex), 0) // 0 → 1 işçi
	t.Cleanup(r.Stop)
	return r, db
}

// TestBatchRunner_CompletesWithMixedOutcomes uçtan uca toplu etiketleme: başarılı
// kayıtlar etiket + IOC alır, sinyalsiz kayıt başarısız sayılır, iş "completed" biter.
func TestBatchRunner_CompletesWithMixedOutcomes(t *testing.T) {
	ex := &scriptedExtractor{byURL: map[string]scraper.TagResult{
		resultURL(0): {Success: true, Tags: []string{"ransomware", "leak"}, KeywordHits: 3,
			Text: "Contact: seller.ops@protonmail.com — pay to bitcoin wallet 1BoatSLRHtKNngkdXEeobR76b53LETtpyT now"},
		resultURL(1): {Success: true}, // sinyal yok → hata
		resultURL(2): {Success: true, Tags: []string{"carding", "cvv"}},
	}}
	r, db := newRunner(t, ex)
	ids := seedResults(t, db, 3)

	job, err := r.Submit(context.Background(), ids, "q")
	if err != nil {
		t.Fatal(err)
	}
	j := waitTagJob(t, db, job.ID, StatusCompleted, StatusFailed)
	if j.Status != StatusCompleted || j.ProcessedCount != 3 || j.SuccessCount != 2 || j.FailureCount != 1 {
		t.Fatalf("iş sonucu: %+v", j)
	}
	if j.StartedAt == nil || j.FinishedAt == nil {
		t.Fatalf("zaman damgaları: %+v", j)
	}

	first, _ := db.GetResultByID(ids[0])
	if !strings.Contains(first.AutoTags, "ransomware") || first.KeywordCount != 3 || first.Category == "Genel" {
		t.Fatalf("etiketlenen kayıt: %+v", first)
	}
	arts, err := db.GetArtifacts(ids[0])
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range arts {
		got[a.Type] = a.Value
	}
	if got["email"] != "seller.ops@protonmail.com" || got["bitcoin"] != "1BoatSLRHtKNngkdXEeobR76b53LETtpyT" {
		t.Fatalf("sayfa metninden IOC çıkarılmalı: %+v", arts)
	}
	if second, _ := db.GetResultByID(ids[1]); second.AutoTags != "" {
		t.Fatalf("sinyalsiz kayıt etiketlenmemeli: %q", second.AutoTags)
	}
	if tags, _ := db.GetTagStats(); len(tags) == 0 {
		t.Fatal("result_tags senkronlanmalı")
	}
}

func TestBatchRunner_AllFailedMarksJobFailed(t *testing.T) {
	ex := &scriptedExtractor{byURL: map[string]scraper.TagResult{}}
	r, db := newRunner(t, ex)
	ids := seedResults(t, db, 2)
	job, err := r.Submit(context.Background(), ids, "q")
	if err != nil {
		t.Fatal(err)
	}
	j := waitTagJob(t, db, job.ID, StatusCompleted, StatusFailed)
	if j.Status != StatusFailed || j.ErrorMessage != JobMsgAllFailed || j.FailureCount != 2 {
		t.Fatalf("tümü başarısız iş: %+v", j)
	}
}

// TestBatchRunner_ResumesFromProcessedCount yeniden başlatmada iş kaldığı yerden
// sürer: işlenmiş kayıtlar tekrar çekilmez, sayaçlar korunur.
func TestBatchRunner_ResumesFromProcessedCount(t *testing.T) {
	ex := &scriptedExtractor{byURL: map[string]scraper.TagResult{
		resultURL(1): {Success: true, Tags: []string{"exploit"}},
		resultURL(2): {Success: true, Tags: []string{"malware"}},
	}}
	db := newTestDB(t)
	ids := seedResults(t, db, 3)
	if err := db.CreateTaggingJob(storage.TaggingJob{
		ID: "resume-1", Query: "q", TotalCount: 3, ProcessedCount: 1, SuccessCount: 1,
		Status: StatusRunning, ResultIDsJSON: fmt.Sprintf("[%d,%d,%d]", ids[0], ids[1], ids[2]),
	}); err != nil {
		t.Fatal(err)
	}
	r := NewBatchRunner(db, NewEngine(db, ex), 1)
	t.Cleanup(r.Stop)
	if err := r.RecoverPendingJobs(); err != nil {
		t.Fatal(err)
	}
	j := waitTagJob(t, db, "resume-1", StatusCompleted, StatusFailed)
	if j.ProcessedCount != 3 || j.SuccessCount != 3 || j.Status != StatusCompleted {
		t.Fatalf("devam eden iş: %+v", j)
	}
	calls := ex.called()
	if len(calls) != 2 || calls[0] != resultURL(1) || calls[1] != resultURL(2) {
		t.Fatalf("işlenmiş kayıt tekrar çekildi: %v", calls)
	}
}

func TestBatchRunner_BadPayloadsFail(t *testing.T) {
	db := newTestDB(t)
	for id, payload := range map[string]string{"bad-json": "{not json", "empty": "[]"} {
		if err := db.CreateTaggingJob(storage.TaggingJob{ID: id, Status: StatusPending, ResultIDsJSON: payload}); err != nil {
			t.Fatal(err)
		}
	}
	r := NewBatchRunner(db, NewEngine(db, &scriptedExtractor{}), 1)
	t.Cleanup(r.Stop)
	if err := r.RecoverPendingJobs(); err != nil {
		t.Fatal(err)
	}
	if j := waitTagJob(t, db, "bad-json", StatusFailed); j.ErrorMessage != JobMsgPayloadInvalid {
		t.Fatalf("bozuk yük: %+v", j)
	}
	if j := waitTagJob(t, db, "empty", StatusFailed); j.ErrorMessage != JobMsgPayloadEmpty {
		t.Fatalf("boş yük: %+v", j)
	}
}

// TestBatchRunner_CancelRunningAndPending çalışan iş bağlamla durur, bekleyen iş
// hiç başlamadan iptal edilir; bitmiş işi iptal etmek no-op'tur.
func TestBatchRunner_CancelRunningAndPending(t *testing.T) {
	ex := &scriptedExtractor{byURL: map[string]scraper.TagResult{}, block: make(chan struct{}), entered: make(chan string, 1)}
	r, db := newRunner(t, ex)
	ids := seedResults(t, db, 3)

	running, err := r.Submit(context.Background(), ids[:2], "q")
	if err != nil {
		t.Fatal(err)
	}
	<-ex.entered
	pending, err := r.Submit(context.Background(), ids[2:], "q")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel(pending.ID); err != nil {
		t.Fatal(err)
	}
	if j := waitTagJob(t, db, pending.ID, StatusCancelled); j.ErrorMessage != JobMsgCancelled {
		t.Fatalf("bekleyen iptal: %+v", j)
	}
	if err := r.Cancel(running.ID); err != nil {
		t.Fatal(err)
	}
	j := waitTagJob(t, db, running.ID, StatusCancelled)
	if j.ProcessedCount > 1 {
		t.Fatalf("iptal sonrası kayıt işlenmemeli: %+v", j)
	}
	if err := r.Cancel(running.ID); err != nil {
		t.Fatalf("bitmiş iş iptali no-op olmalı: %v", err)
	}
	if err := r.Cancel("yok"); err == nil {
		t.Fatal("bilinmeyen iş hata vermeli")
	}
	for _, u := range ex.called() {
		if u == resultURL(2) {
			t.Fatal("iptal edilen bekleyen iş çalıştırıldı")
		}
	}
}

// TestBatchRunner_StopPausesRunningJobForResume kapanışta çalışan iş "pending"e
// çekilir (bir sonraki açılışta RecoverPendingJobs devam ettirir).
func TestBatchRunner_StopPausesRunningJobForResume(t *testing.T) {
	ex := &scriptedExtractor{byURL: map[string]scraper.TagResult{}, block: make(chan struct{}), entered: make(chan string, 1)}
	db := newTestDB(t)
	r := NewBatchRunner(db, NewEngine(db, ex), 1)
	ids := seedResults(t, db, 2)
	job, err := r.Submit(context.Background(), ids, "q")
	if err != nil {
		t.Fatal(err)
	}
	<-ex.entered
	r.Stop()
	j, err := db.GetTaggingJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusPending || j.ErrorMessage != storage.TaggingJobRestartedMsg {
		t.Fatalf("kapanışta iş devam için pending olmalı: %+v", j)
	}
}

// TestBatchRunner_SubmitAfterStopDoesNotPanic kapanış sırasında gelen istek
// kapalı kuyruğa gönderip panik etmemeli; iş "Kuyruk dolu" ile reddedilir.
func TestBatchRunner_SubmitAfterStopDoesNotPanic(t *testing.T) {
	db := newTestDB(t)
	r := NewBatchRunner(db, NewEngine(db, &scriptedExtractor{}), 1)
	ids := seedResults(t, db, 1)
	r.Stop()

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("Stop sonrası Submit panik etti: %v", p)
		}
	}()
	_, err := r.Submit(context.Background(), ids, "q")
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("ErrQueueFull bekleniyordu: %v", err)
	}
	if err := r.RecoverPendingJobs(); err != nil {
		t.Fatalf("Stop sonrası kurtarma panik/hata vermemeli: %v", err)
	}
}

func TestBatchRunner_SubmitRejectsCancelledContextAndFullQueue(t *testing.T) {
	db := newTestDB(t)
	ids := seedResults(t, db, 1)
	r := &BatchRunner{db: db, queue: make(chan string), cancels: map[string]context.CancelFunc{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Submit(ctx, ids, "q"); !errors.Is(err, context.Canceled) {
		t.Fatalf("iptal edilmiş bağlam: %v", err)
	}
	if jobs, _ := db.ListTaggingJobsByStatuses([]string{StatusPending, StatusFailed}, 10); len(jobs) != 0 {
		t.Fatalf("iptal edilmiş istek iş üretmemeli: %d", len(jobs))
	}

	// Alıcısız kuyruk = dolu
	if _, err := r.Submit(context.Background(), ids, "q"); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("dolu kuyruk: %v", err)
	}
	jobs, _ := db.ListTaggingJobsByStatuses([]string{StatusFailed}, 10)
	if len(jobs) != 1 || jobs[0].ErrorMessage != JobMsgQueueFull {
		t.Fatalf("reddedilen iş failed/Kuyruk dolu olmalı: %+v", jobs)
	}
}

func TestNormalizeIDs(t *testing.T) {
	got := normalizeIDs([]int64{3, -1, 0, 3, 7, 7, 1})
	if fmt.Sprint(got) != "[3 7 1]" {
		t.Fatalf("normalizeIDs = %v", got)
	}
	if got := normalizeIDs(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil girdi boş dilim olmalı: %v", got)
	}
}
