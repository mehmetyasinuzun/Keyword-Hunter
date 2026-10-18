package scheduler

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
)

const (
	onionA = "http://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.onion/known"
	onionB = "http://bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.onion/new-high"
	onionC = "http://cccccccccccccccccccccccccccccccccccccccccccccccccccccccc.onion/new-low"
)

// fakeSearcher Tor'suz arama: sabit sonuç döner; gate verilirse kapanana dek bekler.
type fakeSearcher struct {
	mu      sync.Mutex
	results map[string][]search.Result
	queries []string
	gate    chan struct{}
	started chan string
	// stats verilirse motor özeti olarak döner (varsayılan: 3/3 yanıt).
	stats *search.SearchStats
}

func (f *fakeSearcher) SearchAllStats(ctx context.Context, query string) ([]search.Result, search.SearchStats) {
	f.mu.Lock()
	f.queries = append(f.queries, query)
	res := f.results[query]
	gate, started := f.gate, f.started
	stats := search.SearchStats{Engines: 3, Answered: 3}
	if f.stats != nil {
		stats = *f.stats
	}
	f.mu.Unlock()
	if started != nil {
		started <- query
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
		}
	}
	return res, stats
}

func (f *fakeSearcher) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

func newTestScheduler(db *storage.DB, fs *fakeSearcher) *Scheduler {
	s := New(db, nil)
	s.searcher = fs
	return s
}

func mustSchedule(t *testing.T, db *storage.DB, query string, threshold int, webhook string) *storage.ScheduledSearch {
	t.Helper()
	ss, err := db.CreateScheduledSearch(query, 30, webhook, threshold)
	if err != nil {
		t.Fatal(err)
	}
	return ss
}

func makeDue(t *testing.T, db *storage.DB, id int64) {
	t.Helper()
	if _, err := db.GetDBConn().Exec("UPDATE scheduled_searches SET next_run_at = ? WHERE id = ?", time.Now().Add(-time.Minute), id); err != nil {
		t.Fatal(err)
	}
}

func waitRuns(t *testing.T, db *storage.DB, id int64, runs int) *storage.ScheduledSearch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ss, err := db.GetScheduledSearch(id)
		if err == nil && ss.TotalRuns >= runs {
			return ss
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("schedule %d %d kez çalışmadı", id, runs)
	return nil
}

// TestRunSearch_DiffAlertsOnlyNewURLsAboveThreshold planlı taramanın "yalnız yeni
// URL'ler" mantığını uçtan uca sabitler: sorgu için önceden bilinen URL yeni
// sayılmaz; yeni ama eşik altı bulgu kaydedilir ama tarama webhook'una girmez;
// istatistikler (sonuç/yeni/çalışma sayısı, sonraki çalışma) ve geçmiş güncellenir;
// genel bildirim merkezi kendi eşiğiyle ayrıca çalışır.
func TestRunSearch_DiffAlertsOnlyNewURLsAboveThreshold(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOW_PRIVATE", "true")
	perSchedule := &hookCatcher{}
	global := &hookCatcher{}
	psSrv := httptest.NewServer(perSchedule)
	defer psSrv.Close()
	glSrv := httptest.NewServer(global)
	defer glSrv.Close()

	db := newAlertDB(t)
	// Aynı URL başka bir sorgu altında bilinse de bu sorgu için yenidir.
	if _, err := db.SaveResults([]storage.SearchResult{
		{Title: "eski", URL: onionA, Source: "Tordex", Query: "leak"},
		{Title: "başka sorgu", URL: onionB, Source: "Tordex", Query: "other"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAlertConfig(storage.AlertConfig{WebhookURL: glSrv.URL, MinCriticality: 1, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ss := mustSchedule(t, db, "leak", 3, psSrv.URL)

	fs := &fakeSearcher{results: map[string][]search.Result{"leak": {
		{Title: "KNOWN-title", URL: onionA, Source: "Tordex", Criticality: 5, Category: "leak"},
		{Title: "NEWHIGH-title", URL: onionB, Source: "Tor66", Criticality: 4, Category: "breach"},
		{Title: "NEWLOW-title", URL: onionC, Source: "Tor66", Criticality: 1, Category: "Genel"},
	}}}
	s := newTestScheduler(db, fs)
	before := time.Now().UTC()
	s.runSearch(ss)

	got, err := db.GetScheduledSearch(ss.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalRuns != 1 || got.LastResultCount != 3 || got.LastNewCount != 2 {
		t.Fatalf("istatistik: runs=%d results=%d new=%d", got.TotalRuns, got.LastResultCount, got.LastNewCount)
	}
	if got.LastRunAt == nil || got.NextRunAt == nil || !got.NextRunAt.After(before.Add(29*time.Minute)) {
		t.Fatalf("sonraki çalışma ~30 dk sonra olmalı: last=%v next=%v", got.LastRunAt, got.NextRunAt)
	}
	if due, _ := db.GetDueScheduledSearches(); len(due) != 0 {
		t.Fatalf("çalışma sonrası due olmamalı: %d", len(due))
	}

	known, err := db.GetKnownURLsForQuery("leak")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{onionA, onionB, onionC} {
		if !known[u] {
			t.Errorf("%s sorgu için kaydedilmeli", u)
		}
	}
	hist, _ := db.GetSearchHistory(5)
	if len(hist) != 1 || hist[0].Query != "leak" || hist[0].ResultCount != 3 {
		t.Fatalf("arama geçmişi: %+v", hist)
	}

	n, body := perSchedule.snapshot()
	if n != 1 {
		t.Fatalf("tarama webhook'u tam 1 kez çağrılmalı: %d", n)
	}
	if !strings.Contains(body, "NEWHIGH-title") {
		t.Errorf("eşik üstü yeni bulgu gönderilmeli: %s", body)
	}
	for _, bad := range []string{"KNOWN-title", "NEWLOW-title"} {
		if strings.Contains(body, bad) {
			t.Errorf("%s tarama webhook'una girmemeli: %s", bad, body)
		}
	}
	gn, gbody := global.snapshot()
	if gn != 1 || !strings.Contains(gbody, "NEWHIGH-title") || !strings.Contains(gbody, "NEWLOW-title") || strings.Contains(gbody, "KNOWN-title") {
		t.Fatalf("genel bildirim (eşik 1) yalnız yeni bulguları içermeli: n=%d %s", gn, gbody)
	}

	// İkinci tur: aynı sonuçlar artık bilinir → yeni 0, hiçbir webhook çağrılmaz.
	s.runSearch(got)
	got2, _ := db.GetScheduledSearch(ss.ID)
	if got2.TotalRuns != 2 || got2.LastNewCount != 0 || got2.LastResultCount != 3 {
		t.Fatalf("ikinci tur: %+v", got2)
	}
	if n, _ := perSchedule.snapshot(); n != 1 {
		t.Fatalf("yeni bulgu yokken tarama webhook'u çağrılmamalı: %d", n)
	}
	if n, _ := global.snapshot(); n != 1 {
		t.Fatalf("yeni bulgu yokken genel bildirim gönderilmemeli: %d", n)
	}
}

func TestRunSearch_NoWebhookConfiguredStillRecords(t *testing.T) {
	db := newAlertDB(t)
	ss := mustSchedule(t, db, "market", 1, "")
	fs := &fakeSearcher{results: map[string][]search.Result{"market": {{Title: "x", URL: onionB, Source: "E", Criticality: 5}}}}
	newTestScheduler(db, fs).runSearch(ss)
	got := waitRuns(t, db, ss.ID, 1)
	if got.LastNewCount != 1 {
		t.Fatalf("yeni=%d", got.LastNewCount)
	}
}

// TestCheckAndRun_OnlyDueAndOneAtATime yalnız vakti gelmiş, etkin taramalar
// başlatılır ve aynı anda en fazla bir tarama çalışır (kaynak kısıtı).
func TestCheckAndRun_OnlyDueAndOneAtATime(t *testing.T) {
	db := newAlertDB(t)
	notDue := mustSchedule(t, db, "notdue", 3, "")
	disabled := mustSchedule(t, db, "disabled", 3, "")
	due1 := mustSchedule(t, db, "due1", 3, "")
	due2 := mustSchedule(t, db, "due2", 3, "")
	makeDue(t, db, disabled.ID)
	if on, err := db.ToggleScheduledSearch(disabled.ID); err != nil || on {
		t.Fatalf("toggle: on=%v err=%v", on, err)
	}
	makeDue(t, db, due1.ID)
	makeDue(t, db, due2.ID)

	fs := &fakeSearcher{results: map[string][]search.Result{}, gate: make(chan struct{}), started: make(chan string, 4)}
	s := newTestScheduler(db, fs)
	s.checkAndRun()

	first := <-fs.started
	if !s.Busy() {
		t.Fatal("tarama sürerken Busy true olmalı")
	}
	if s.RunNow(notDue.ID) {
		t.Fatal("tarama sürerken RunNow reddedilmeli (409)")
	}
	close(fs.gate)

	firstID := due1.ID
	if first == "due2" {
		firstID = due2.ID
	}
	waitRuns(t, db, firstID, 1)
	deadline := time.Now().Add(5 * time.Second)
	for s.Busy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	calls := fs.calls()
	if len(calls) != 1 || (calls[0] != "due1" && calls[0] != "due2") {
		t.Fatalf("tek tur yalnız bir due tarama başlatmalı: %v", calls)
	}

	// Sonraki tick kalan due taramayı alır; çalışmış olan artık due değildir.
	s.checkAndRun()
	other := due2.ID
	if firstID == due2.ID {
		other = due1.ID
	}
	waitRuns(t, db, other, 1)
	for _, c := range fs.calls() {
		if c == "notdue" || c == "disabled" {
			t.Fatalf("vakti gelmemiş/pasif tarama çalıştı: %v", fs.calls())
		}
	}
}

func TestRunNow_RunsImmediatelyAndToleratesMissingID(t *testing.T) {
	db := newAlertDB(t)
	ss := mustSchedule(t, db, "now", 3, "")
	fs := &fakeSearcher{results: map[string][]search.Result{}}
	s := newTestScheduler(db, fs)

	if !s.RunNow(ss.ID) {
		t.Fatal("boşta RunNow kabul edilmeli")
	}
	waitRuns(t, db, ss.ID, 1)

	deadline := time.Now().Add(5 * time.Second)
	for s.Busy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !s.RunNow(999999) {
		t.Fatal("olmayan ID yine de kabul edilir (arka planda loglanır)")
	}
	for s.Busy() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Busy() {
		t.Fatal("olmayan ID semaforu bırakmalı")
	}
	if len(fs.calls()) != 1 {
		t.Fatalf("olmayan ID için arama yapılmamalı: %v", fs.calls())
	}
}

func TestStartStop_Idempotent(t *testing.T) {
	s := newTestScheduler(newAlertDB(t), &fakeSearcher{})
	s.Start()
	s.Stop()
	s.Stop()
}
