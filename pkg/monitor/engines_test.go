package monitor

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
)

func newTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "monitor_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// engineServer yol başına sabit durum kodu döner ve gelen sorgu parametresini kaydeder.
type engineServer struct {
	*httptest.Server
	mu      sync.Mutex
	queries []string
	codes   map[string]int
}

func newEngineServer(t *testing.T, codes map[string]int) *engineServer {
	t.Helper()
	es := &engineServer{codes: codes}
	es.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		es.mu.Lock()
		es.queries = append(es.queries, r.URL.Query().Get("q"))
		code, ok := es.codes[r.URL.Path]
		es.mu.Unlock()
		if !ok {
			code = http.StatusNotFound
		}
		if code == http.StatusFound {
			w.Header().Set("Location", "/elsewhere")
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(es.Close)
	return es
}

func (es *engineServer) setCode(path string, code int) {
	es.mu.Lock()
	es.codes[path] = code
	es.mu.Unlock()
}

// newTestMonitor gerçek New() ile kurar (Tor'a hiç bağlanmaz: yalnız transport
// üretilir), ardından taşıyıcıyı httptest'e çevirir ve motor listesini enjekte eder.
func newTestMonitor(t *testing.T, db *storage.DB, srv *httptest.Server, engines []search.Engine) *EngineMonitor {
	t.Helper()
	em, err := New(db, "127.0.0.1:1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	em.client.Transport = srv.Client().Transport
	em.engines = engines
	for _, e := range engines {
		if err := db.UpsertEngineStat(e.Name, e.URL, true); err != nil {
			t.Fatal(err)
		}
	}
	// Gerçek motor kayıtları "unknown" kalır; tüm-düşük sayımına karışmasınlar.
	for _, e := range search.SearchEngines {
		_ = db.SetEngineActive(e.Name, false)
	}
	return em
}

func statsByName(t *testing.T, db *storage.DB) map[string]storage.EngineStatDB {
	t.Helper()
	stats, err := db.GetAllEngineStats()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]storage.EngineStatDB{}
	for _, s := range stats {
		out[s.Name] = s
	}
	return out
}

func TestNew_RegistersEnginesAndPrunesUnknown(t *testing.T) {
	db := newTestDB(t)
	if err := db.UpsertEngineStat("Eski Motor", "http://old.onion/?q={query}", true); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db, "", time.Minute); err == nil {
		t.Fatal("boş TOR_PROXY ile New hata vermeli")
	}
	if _, err := New(db, "127.0.0.1:9050", time.Minute); err != nil {
		t.Fatal(err)
	}
	got := statsByName(t, db)
	if _, ok := got["Eski Motor"]; ok {
		t.Error("listeden çıkarılmış motor budanmalı")
	}
	if len(got) != len(search.SearchEngines) {
		t.Fatalf("kayıtlı motor = %d, beklenen %d", len(got), len(search.SearchEngines))
	}
	for _, e := range search.SearchEngines {
		st, ok := got[e.Name]
		if !ok {
			t.Fatalf("%s kaydedilmeli", e.Name)
		}
		if st.IsActive != e.DefaultActive || st.LastStatus != "unknown" {
			t.Errorf("%s: aktif=%v durum=%q", e.Name, st.IsActive, st.LastStatus)
		}
	}
}

// TestCheckEngine_StatusClassification HTTP durumunun up/down eşlemesini ve
// sorgu yer tutucusunun "test" ile doldurulduğunu sabitler. Yönlendirme takip
// edilmez (302 motorun kendisi cevap verdi demektir); 429 hız sınırı da "up"tır.
func TestCheckEngine_StatusClassification(t *testing.T) {
	db := newTestDB(t)
	srv := newEngineServer(t, map[string]int{
		"/ok": 200, "/moved": 301, "/found": 302, "/limited": 429,
		"/err": 500, "/gone": 404, "/unavail": 503,
	})
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	cases := []struct {
		name, url, want string
	}{
		{"OK", srv.URL + "/ok?q={query}", "up"},
		{"Moved", srv.URL + "/moved?q={query}", "up"},
		{"Found", srv.URL + "/found?q={query}", "up"},
		{"Limited", srv.URL + "/limited?q={query}", "up"},
		{"Err", srv.URL + "/err?q={query}", "down"},
		{"Gone", srv.URL + "/gone?q={query}", "down"},
		{"Unavail", srv.URL + "/unavail?q={query}", "down"},
		{"Refused", closedURL + "/x?q={query}", "down"},
		{"BadURL", "http://[::1/%zz?q={query}", "down"},
	}
	var engines []search.Engine
	for _, c := range cases {
		engines = append(engines, search.Engine{Name: c.name, URL: c.url})
	}
	em := newTestMonitor(t, db, srv.Server, engines)
	for _, e := range engines {
		em.checkEngine(e)
	}

	got := statsByName(t, db)
	for _, c := range cases {
		st := got[c.name]
		if st.LastStatus != c.want {
			t.Errorf("%s: durum=%q, beklenen %q", c.name, st.LastStatus, c.want)
		}
		if st.LastCheckedAt == nil {
			t.Errorf("%s: last_checked_at yazılmalı", c.name)
		}
		if c.want == "up" && (st.SuccessCount != 1 || st.ConsecutiveFails != 0) {
			t.Errorf("%s: success=%d consecutive=%d", c.name, st.SuccessCount, st.ConsecutiveFails)
		}
		if c.want == "down" && (st.FailCount != 1 || st.ConsecutiveFails != 1) {
			t.Errorf("%s: fail=%d consecutive=%d", c.name, st.FailCount, st.ConsecutiveFails)
		}
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.queries) == 0 {
		t.Fatal("sunucuya istek gelmedi")
	}
	for _, q := range srv.queries {
		if q != "test" {
			t.Errorf("{query} yer tutucusu %q ile doldu, beklenen \"test\"", q)
		}
	}
}

// TestCheckAll_ConsecutiveFailsAndRecovery art arda düşüşte sayaçların birikmesini,
// tek başarıda consecutive_fails'in sıfırlanmasını doğrular (uptime geçmişi).
func TestCheckAll_ConsecutiveFailsAndRecovery(t *testing.T) {
	db := newTestDB(t)
	srv := newEngineServer(t, map[string]int{"/a": 500})
	em := newTestMonitor(t, db, srv.Server, []search.Engine{{Name: "A", URL: srv.URL + "/a?q={query}"}})

	em.checkAll()
	em.checkAll()
	em.checkAll()
	st := statsByName(t, db)["A"]
	if st.FailCount != 3 || st.ConsecutiveFails != 3 || st.LastStatus != "down" {
		t.Fatalf("3 düşüş sonrası: %+v", st)
	}
	srv.setCode("/a", 200)
	em.checkAll()
	st = statsByName(t, db)["A"]
	if st.SuccessCount != 1 || st.FailCount != 3 || st.ConsecutiveFails != 0 || st.LastStatus != "up" {
		t.Fatalf("toparlanma sonrası: %+v", st)
	}
}

// TestCheckAll_AllDownHookNeedsTwoConsecutiveRounds tüm aktif motorlar iki tur üst
// üste düşünce Tor devre yenileme kancası bir kez tetiklenir; tek bir "up" tur
// sayacı sıfırlar; pasif motorlar sayıma girmez.
func TestCheckAll_AllDownHookNeedsTwoConsecutiveRounds(t *testing.T) {
	db := newTestDB(t)
	srv := newEngineServer(t, map[string]int{"/a": 500, "/b": 503, "/p": 200})
	engines := []search.Engine{
		{Name: "A", URL: srv.URL + "/a?q={query}"},
		{Name: "B", URL: srv.URL + "/b?q={query}"},
		{Name: "Pasif", URL: srv.URL + "/p?q={query}"},
	}
	em := newTestMonitor(t, db, srv.Server, engines)
	if err := db.SetEngineActive("Pasif", false); err != nil {
		t.Fatal(err)
	}
	var fired atomic.Int32
	em.SetAllDownHook(func() { fired.Add(1) })

	em.checkAll() // 1. tam-düşük tur
	if fired.Load() != 0 {
		t.Fatal("tek turda kanca tetiklenmemeli")
	}
	em.checkAll() // 2. tur → tetikle, sayaç sıfırlanır
	if fired.Load() != 1 {
		t.Fatalf("iki ardışık turda kanca 1 kez tetiklenmeli, %d", fired.Load())
	}
	em.checkAll() // sayaç 1
	if fired.Load() != 1 {
		t.Fatal("tetikleme sonrası sayaç sıfırlanmalı")
	}

	srv.setCode("/a", 200)
	em.checkAll() // up var → sayaç 0
	srv.setCode("/a", 500)
	em.checkAll() // sayaç 1
	if fired.Load() != 1 {
		t.Fatalf("araya giren up turu sayacı sıfırlamalı, kanca=%d", fired.Load())
	}
	em.checkAll() // sayaç 2 → tetikle
	if fired.Load() != 2 {
		t.Fatalf("yeniden iki ardışık düşüşte kanca tetiklenmeli, %d", fired.Load())
	}
}

// TestCheckAll_ConcurrentRoundsAreRaceFree CheckNow (API) ile zamanlayıcı turu
// aynı anda checkAll çalıştırabilir; downStreak yarışmamalı (-race ile koşar).
func TestCheckAll_ConcurrentRoundsAreRaceFree(t *testing.T) {
	db := newTestDB(t)
	srv := newEngineServer(t, map[string]int{"/a": 500})
	em := newTestMonitor(t, db, srv.Server, []search.Engine{{Name: "A", URL: srv.URL + "/a?q={query}"}})
	var fired atomic.Int32
	em.SetAllDownHook(func() { fired.Add(1) })

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			em.checkAll()
		}()
	}
	wg.Wait()
	// 6 tam-düşük tur → sayaç her 2'de bir sıfırlanır: tam 3 tetikleme
	if fired.Load() != 3 {
		t.Fatalf("6 tam-düşük turda 3 tetikleme beklenirdi, %d", fired.Load())
	}
}

func TestStartCheckNowStop(t *testing.T) {
	db := newTestDB(t)
	srv := newEngineServer(t, map[string]int{"/a": 200})
	em := newTestMonitor(t, db, srv.Server, []search.Engine{{Name: "A", URL: srv.URL + "/a?q={query}"}})

	em.Start() // ilk tur hemen
	waitFor(t, func() bool { return statsByName(t, db)["A"].SuccessCount >= 1 })
	em.CheckNow()
	waitFor(t, func() bool { return statsByName(t, db)["A"].SuccessCount >= 2 })
	em.Stop()
	em.Stop() // ikinci Stop panik etmemeli
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("koşul 5 sn içinde sağlanmadı")
}
