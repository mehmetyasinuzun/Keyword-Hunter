package metrics

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestLabelNormalization(t *testing.T) {
	for in, want := range map[string]string{
		"GET": "GET", "POST": "POST", "DELETE": "DELETE", "get": "OTHER", "PROPFIND": "OTHER", "": "OTHER",
	} {
		if got := HTTPMethod(in); got != want {
			t.Errorf("HTTPMethod(%q) = %q, beklenen %q", in, got, want)
		}
	}
	for in, want := range map[int]string{200: "2xx", 302: "3xx", 404: "4xx", 503: "5xx", 0: "other", 99: "other", 600: "other"} {
		if got := StatusClass(in); got != want {
			t.Errorf("StatusClass(%d) = %q, beklenen %q", in, got, want)
		}
	}
	for in, want := range map[int]string{-3: "1", 0: "1", 1: "1", 3: "3", 5: "5", 9: "5"} {
		if got := Severity(in); got != want {
			t.Errorf("Severity(%d) = %q, beklenen %q", in, got, want)
		}
	}
}

// Keyfi girdiler (saldırganın seçtiği yöntem, bilinmeyen tetikleyici/kanal…)
// seri sayısını büyütemez: hepsi sabit kümelere ya da "other"a düşer.
func TestBoundedCardinality(t *testing.T) {
	before := testutil.CollectAndCount(httpRequests)
	for i := range 500 {
		ObserveHTTP(fmt.Sprintf("M%d", i), "/api/results/:id/artifacts", 200+i%400, time.Millisecond, true)
	}
	// 500 farklı yöntem → tek "OTHER" yöntemi × en çok 4 durum sınıfı.
	if grown := testutil.CollectAndCount(httpRequests) - before; grown > 4 {
		t.Fatalf("http_requests_total %d yeni seri üretti (≤4 beklenir)", grown)
	}

	cases := []struct {
		name string
		c    prometheus.Collector
		fire func(i int)
		max  int
	}{
		{"search_runs", searchRuns, func(i int) { SearchRun(fmt.Sprintf("t%d", i), time.Second) }, len(searchTriggers) + 1},
		{"scheduled", scheduledRuns, func(i int) { ScheduledRun(fmt.Sprintf("r%d", i)) }, len(scheduledResults) + 1},
		{"watchlist", watchlistChecks, func(i int) { WatchlistCheck(fmt.Sprintf("s%d", i), i%2 == 0) }, len(watchResults)},
		{"tor", torRenewals, func(i int) { TorRenewal(fmt.Sprintf("x%d", i), fmt.Sprintf("y%d", i)) }, (len(torReasons) + 1) * (len(torResults) + 1)},
		{"retention", retentionPurged, func(i int) { RetentionPurged(fmt.Sprintf("k%d", i), 1) }, len(RetentionKinds) + 1},
		{"notify", notificationSends, func(i int) { NotificationSent(fmt.Sprintf("c%d", i), i%2 == 0) }, (len(notifyChannels) + 1) * 2},
		{"login", loginAttempts, func(i int) { LoginAttempt(fmt.Sprintf("m%d", i), fmt.Sprintf("r%d", i)) }, (len(loginMethods) + 1) * (len(loginResults) + 1)},
		{"findings", newFindings, func(i int) { NewFinding(i - 250) }, 5},
	}
	for _, tc := range cases {
		for i := range 500 {
			tc.fire(i)
		}
		if n := testutil.CollectAndCount(tc.c); n > tc.max {
			t.Errorf("%s: %d seri (en çok %d)", tc.name, n, tc.max)
		}
	}
}

func TestCountersAndPreinitialisedSeries(t *testing.T) {
	// Sıfırla başlatılan seriler ilk olaydan önce de vardır.
	if v := testutil.ToFloat64(scheduledRuns.WithLabelValues(ResultTimeout)); v < 0 {
		t.Fatal("seri yok")
	}
	b := testutil.ToFloat64(watchlistChecks.WithLabelValues(WatchBlocked))
	WatchlistCheck("engel", false)
	if a := testutil.ToFloat64(watchlistChecks.WithLabelValues(WatchBlocked)); a != b+1 {
		t.Fatalf("engel → blocked: %v → %v", b, a)
	}
	b = testutil.ToFloat64(watchlistChecks.WithLabelValues(WatchChanged))
	WatchlistCheck("up", true)
	if a := testutil.ToFloat64(watchlistChecks.WithLabelValues(WatchChanged)); a != b+1 {
		t.Fatalf("up+changed → changed: %v → %v", b, a)
	}
	b = testutil.ToFloat64(retentionPurged.WithLabelValues("results"))
	RetentionPurged("results", 7)
	RetentionPurged("results", 0) // sıfır/negatif eklenmez
	RetentionPurged("results", -2)
	if a := testutil.ToFloat64(retentionPurged.WithLabelValues("results")); a != b+7 {
		t.Fatalf("retention results: %v → %v", b, a)
	}
	b = testutil.ToFloat64(loginAttempts.WithLabelValues(LoginTOTP, ResultLocked))
	LoginAttempt(LoginTOTP, ResultLocked)
	if a := testutil.ToFloat64(loginAttempts.WithLabelValues(LoginTOTP, ResultLocked)); a != b+1 {
		t.Fatalf("login: %v → %v", b, a)
	}
}

// Her sunucu kendi kayıt defterini kurar; olay metrikleri birden çok kayıt
// defterine eklenebilir (testlerde birden çok sunucu).
func TestNewRegistry(t *testing.T) {
	for range 2 {
		reg, err := NewRegistry("9.9.9-test")
		if err != nil {
			t.Fatal(err)
		}
		fams, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, f := range fams {
			names[f.GetName()] = true
			if !strings.HasPrefix(f.GetName(), Namespace+"_") && !strings.HasPrefix(f.GetName(), "go_") && !strings.HasPrefix(f.GetName(), "process_") {
				t.Errorf("beklenmeyen metrik ailesi: %s", f.GetName())
			}
			if f.GetName() == Namespace+"_build_info" {
				lbl := map[string]string{}
				for _, l := range f.GetMetric()[0].GetLabel() {
					lbl[l.GetName()] = l.GetValue()
				}
				if lbl["version"] != "9.9.9-test" || lbl["goversion"] != runtime.Version() || f.GetMetric()[0].GetGauge().GetValue() != 1 {
					t.Errorf("build_info: %v", f.GetMetric()[0])
				}
			}
		}
		for _, want := range []string{"keywordhunter_build_info", "go_goroutines", "keywordhunter_search_runs_total", "keywordhunter_login_attempts_total"} {
			if !names[want] {
				t.Errorf("%s eksik", want)
			}
		}
		if runtime.GOOS == "linux" && !names["process_cpu_seconds_total"] {
			t.Error("süreç toplayıcısı eksik")
		}
	}
}
