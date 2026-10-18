package scheduler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"keywordhunter-mvp/pkg/metrics"
	"keywordhunter-mvp/pkg/search"
)

// counter süreç geneli sayacın güncel değeri (yeni bir kayıt defteri üzerinden).
func counter(t *testing.T, name string, labels ...string) float64 {
	t.Helper()
	reg, err := metrics.NewRegistry("test")
	if err != nil {
		t.Fatal(err)
	}
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fams {
		if f.GetName() != name {
			continue
		}
	next:
		for _, m := range f.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			for i := 0; i+1 < len(labels); i += 2 {
				if got[labels[i]] != labels[i+1] {
					continue next
				}
			}
			if m.GetHistogram() != nil {
				return float64(m.GetHistogram().GetSampleCount())
			}
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// Planlı tarama: arama süresi/sayısı "scheduled" tetikleyicisiyle, sonuç
// scheduled_scan_runs_total'a, webhook gönderimi kanal + sonuçla sayılır.
func TestRunSearch_RecordsMetrics(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOW_PRIVATE", "true")
	ok := httptest.NewServer(&hookCatcher{})
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	db := newAlertDB(t)
	fs := &fakeSearcher{results: map[string][]search.Result{
		"m1": {{Title: "x", URL: onionB, Source: "E", Criticality: 5}},
		"m2": {{Title: "y", URL: onionC, Source: "E", Criticality: 5}},
	}}
	s := newTestScheduler(db, fs)

	runs := counter(t, "keywordhunter_search_runs_total", "trigger", "scheduled")
	hist := counter(t, "keywordhunter_search_duration_seconds", "trigger", "scheduled")
	success := counter(t, "keywordhunter_scheduled_scan_runs_total", "result", "success")
	sentOK := counter(t, "keywordhunter_notification_sends_total", "channel", "webhook", "result", "success")
	sentFail := counter(t, "keywordhunter_notification_sends_total", "channel", "webhook", "result", "failure")
	sev5 := counter(t, "keywordhunter_new_findings_total", "severity", "5")

	s.runSearch(mustSchedule(t, db, "m1", 3, ok.URL))
	s.runSearch(mustSchedule(t, db, "m2", 3, bad.URL))

	for _, c := range []struct {
		name   string
		labels []string
		before float64
		delta  float64
	}{
		{"keywordhunter_search_runs_total", []string{"trigger", "scheduled"}, runs, 2},
		{"keywordhunter_search_duration_seconds", []string{"trigger", "scheduled"}, hist, 2},
		{"keywordhunter_scheduled_scan_runs_total", []string{"result", "success"}, success, 2},
		{"keywordhunter_notification_sends_total", []string{"channel", "webhook", "result", "success"}, sentOK, 1},
		{"keywordhunter_notification_sends_total", []string{"channel", "webhook", "result", "failure"}, sentFail, 1},
		{"keywordhunter_new_findings_total", []string{"severity", "5"}, sev5, 2},
	} {
		if d := counter(t, c.name, c.labels...) - c.before; d != c.delta {
			t.Errorf("%s%v artışı %v, beklenen %v", c.name, c.labels, d, c.delta)
		}
	}
}

// Hiçbir motor yanıt vermediğinde (Tor kapalı, etkin motor yok) planlı tarama
// "success" değil "engines_failed" sayılır; en az bir motor yanıt verip 0
// sonuç dönen tarama ise başarıdır.
func TestRunSearch_NoEngineAnsweredIsNotSuccess(t *testing.T) {
	db := newAlertDB(t)
	for _, c := range []struct {
		name  string
		stats search.SearchStats
		want  string
	}{
		{"all-failed", search.SearchStats{Engines: 10, Answered: 0}, metrics.ResultEnginesFailed},
		{"no-engines", search.SearchStats{}, metrics.ResultEnginesFailed},
		{"answered-empty", search.SearchStats{Engines: 10, Answered: 1}, metrics.ResultSuccess},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := c.stats
			s := newTestScheduler(db, &fakeSearcher{stats: &st})
			before := map[string]float64{}
			for _, r := range []string{metrics.ResultSuccess, metrics.ResultEnginesFailed, metrics.ResultError, metrics.ResultTimeout} {
				before[r] = counter(t, "keywordhunter_scheduled_scan_runs_total", "result", r)
			}
			s.runSearch(mustSchedule(t, db, "q-"+c.name, 3, ""))
			for r, b := range before {
				want := 0.0
				if r == c.want {
					want = 1
				}
				if d := counter(t, "keywordhunter_scheduled_scan_runs_total", "result", r) - b; d != want {
					t.Errorf("result=%s artışı %v, beklenen %v", r, d, want)
				}
			}
		})
	}
}
