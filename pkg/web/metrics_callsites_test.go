package web

import (
	"bufio"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"keywordhunter-mvp/pkg/metrics"
	"keywordhunter-mvp/pkg/search"
	"keywordhunter-mvp/pkg/storage"
	"keywordhunter-mvp/pkg/tor"
)

// Olay sayaçlarının uygulamadaki ÇAĞRI YERLERİ: yardımcıların kendisi
// pkg/metrics'te sınanır; burada her işleyicinin/işin sayacı gerçekten
// artırdığı doğrulanır (çağrı silinirse test düşer).

// counterDelta süreç geneli sayacın iki anlık görüntü arasındaki artışı.
func counterDelta(t *testing.T, before, after map[string]*dto.MetricFamily, name string, labels ...string) float64 {
	t.Helper()
	b, _ := sample(before, name, labels...)
	a := mustSample(t, after, name, labels...)
	return a - b
}

// fakeTorControl her komuta "250 OK" dönen sahte Tor kontrol portu.
func fakeTorControl(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				r := bufio.NewReader(c)
				for {
					if _, err := r.ReadString('\n'); err != nil {
						return
					}
					if _, err := c.Write([]byte("250 OK\r\n")); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// POST /search: search_runs_total ve search_duration_seconds "manual"
// tetikleyicisiyle artar.
func TestMetrics_ManualSearchCounted(t *testing.T) {
	s := newMetricsServer(t)
	sr, err := search.New("127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	sr.SetEngineFilter(func(string) bool { return false }) // Tor'suz, anında biter
	s.searcher = sr
	ck := loginAs(t, s, "admin", "correct-horse")

	before := globalFamilies(t)
	form := url.Values{"query": {"acme leak"}, "_csrf": {csrfOf(ck)}}
	if w := do(s, http.MethodPost, "/search", form, ck, nil); w.Code != http.StatusOK {
		t.Fatalf("arama: %d", w.Code)
	}
	after := globalFamilies(t)
	if d := counterDelta(t, before, after, "keywordhunter_search_runs_total", "trigger", "manual"); d != 1 {
		t.Errorf("search_runs_total{manual} artışı %v", d)
	}
	if d := counterDelta(t, before, after, "keywordhunter_search_duration_seconds", "trigger", "manual"); d != 1 {
		t.Errorf("search_duration_seconds{manual} gözlem artışı %v", d)
	}
	if d := counterDelta(t, before, after, "keywordhunter_search_runs_total", "trigger", "scheduled"); d != 0 {
		t.Errorf("elle arama scheduled sayılmamalı: %v", d)
	}
}

// Tor devre yenileme: Ayarlar'dan (manual) başarı, bekleme süresi ve hata;
// motor izleyicisinin tüm-motorlar-düşük kancası (all_engines_down).
func TestMetrics_TorRenewalsCounted(t *testing.T) {
	s := newMetricsServer(t)
	s.torCtl = tor.New(fakeTorControl(t), "", time.Hour)
	ck := loginAs(t, s, "admin", "correct-horse")
	newnym := func(force bool) int {
		w := doJSON(s, http.MethodPost, "/api/tor/newnym", `{"force":`+strconv.FormatBool(force)+`}`, ck, csrfOf(ck))
		return w.Code
	}
	key := func(reason, result string) []string { return []string{"reason", reason, "result", result} }

	before := globalFamilies(t)
	if code := newnym(false); code != http.StatusOK {
		t.Fatalf("ilk NEWNYM: %d", code)
	}
	if code := newnym(false); code == http.StatusOK {
		t.Fatal("bekleme süresinde NEWNYM reddedilmeli")
	}
	s.renewTorAllEnginesDown() // bekleme süresinde: cooldown
	s.torCtl = tor.New("127.0.0.1:1", "", time.Hour)
	if code := newnym(true); code == http.StatusOK {
		t.Fatal("erişilemeyen kontrol portunda NEWNYM başarısız olmalı")
	}
	s.renewTorAllEnginesDown() // bağlanamaz: failure
	s.torCtl = tor.New(fakeTorControl(t), "", time.Hour)
	s.renewTorAllEnginesDown() // success
	after := globalFamilies(t)

	for _, c := range []struct {
		reason, result string
		delta          float64
	}{
		{metrics.TorReasonManual, metrics.ResultSuccess, 1},
		{metrics.TorReasonManual, metrics.ResultCooldown, 1},
		{metrics.TorReasonManual, metrics.ResultFailure, 1},
		{metrics.TorReasonAllEnginesDown, metrics.ResultCooldown, 1},
		{metrics.TorReasonAllEnginesDown, metrics.ResultFailure, 1},
		{metrics.TorReasonAllEnginesDown, metrics.ResultSuccess, 1},
	} {
		if d := counterDelta(t, before, after, "keywordhunter_tor_circuit_renewals_total", key(c.reason, c.result)...); d != c.delta {
			t.Errorf("tor_circuit_renewals_total{%s,%s} artışı %v, beklenen %v", c.reason, c.result, d, c.delta)
		}
	}
}

// İzleme listesi kontrolü iki yoldan da sayılır: anında kontrol (API) ve
// periyodik iş.
func TestMetrics_WatchlistChecksCounted(t *testing.T) {
	s := newMetricsServer(t)
	// Geçersiz URL: istek kurulamaz → ağa çıkmadan "down".
	id, err := s.db.AddWatchlistItem("bad", "http://bad host/", "Genel", "")
	if err != nil {
		t.Fatal(err)
	}
	ck := loginAs(t, s, "admin", "correct-horse")

	before := globalFamilies(t)
	if w := doJSON(s, http.MethodPost, "/api/watchlist/"+strconv.FormatInt(id, 10)+"/check", `{}`, ck, csrfOf(ck)); w.Code != http.StatusOK {
		t.Fatalf("anında kontrol: %d %s", w.Code, w.Body.String())
	}
	s.runWatchlistChecks()
	after := globalFamilies(t)
	if d := counterDelta(t, before, after, "keywordhunter_watchlist_checks_total", "result", metrics.WatchDown); d != 2 {
		t.Errorf("watchlist_checks_total{down} artışı %v, beklenen 2", d)
	}
	if d := counterDelta(t, before, after, "keywordhunter_watchlist_checks_total", "result", metrics.WatchUp); d != 0 {
		t.Errorf("watchlist_checks_total{up} artışı %v", d)
	}
}

// Denetim kaydı budaması retention_purged_total{kind="audit_events"}'i
// silinen satır kadar artırır (saklama temizliğinin türleri
// TestRetention_PurgesWithFakeClock'ta).
func TestMetrics_AuditPruneCounted(t *testing.T) {
	s := newMetricsServer(t)
	old := time.Now().Add(-72 * time.Hour)
	for range 3 {
		if _, err := s.db.AppendAuditEvent(storage.AuditEvent{Action: "test.old", Actor: "system", AuthMethod: storage.AuthMethodSystem, Success: true, Time: old}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.AppendAuditEvent(storage.AuditEvent{Action: "test.new", Actor: "system", AuthMethod: storage.AuthMethodSystem, Success: true}); err != nil {
		t.Fatal(err)
	}
	s.auditRetentionDays = 1

	before := globalFamilies(t)
	s.runAuditMaintenance()
	after := globalFamilies(t)
	if d := counterDelta(t, before, after, "keywordhunter_retention_purged_total", "kind", "audit_events"); d != 3 {
		t.Errorf("retention_purged_total{audit_events} artışı %v, beklenen 3", d)
	}
	ev, _, err := s.db.ListAuditEvents(storage.AuditFilter{ActionPrefixes: []string{"test."}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 1 || !strings.HasPrefix(ev[0].Action, "test.new") {
		t.Fatalf("budama sonrası olaylar: %+v", ev)
	}
}
