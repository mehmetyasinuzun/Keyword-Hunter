package scheduler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/notify"
	"keywordhunter-mvp/pkg/storage"
)

type hookCatcher struct {
	mu     sync.Mutex
	calls  int
	bodies []string
}

func (h *hookCatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	h.mu.Lock()
	h.calls++
	h.bodies = append(h.bodies, string(b))
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (h *hookCatcher) snapshot() (int, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls, strings.Join(h.bodies, "\n")
}

func newAlertDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.New(filepath.Join(t.TempDir(), "alert_test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestDispatchGlobalAlert_ThresholdAndEnabled: genel bildirim merkezi yalnız
// etkinken ve eşik üstü YENİ bulgu varken tek bir webhook çağrısı yapmalı; gövde
// eşik üstü başlıkları içermeli, eşik altını içermemeli.
func TestDispatchGlobalAlert_ThresholdAndEnabled(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOW_PRIVATE", "true") // httptest 127.0.0.1 hedefine izin ver
	catcher := &hookCatcher{}
	srv := httptest.NewServer(catcher)
	defer srv.Close()

	db := newAlertDB(t)
	findings := []notify.Finding{
		{Title: "LOW-sev1-title", URL: "http://a.onion/1", Category: "Genel", Criticality: 1},
		{Title: "MID-sev3-title", URL: "http://a.onion/3", Category: "breach", Criticality: 3},
		{Title: "HIGH-sev5-title", URL: "http://a.onion/5", Category: "leak", Criticality: 5},
	}
	now := time.Now()

	// 1) Devre dışı → hiç çağrı yok
	if err := db.SaveAlertConfig(storage.AlertConfig{WebhookURL: srv.URL, MinCriticality: 3, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	DispatchGlobalAlert(db, "q", 10, 3, findings, now)
	if n, _ := catcher.snapshot(); n != 0 {
		t.Fatalf("devre dışıyken çağrı yapıldı: %d", n)
	}

	// 2) Etkin ama yalnız eşik altı bulgu → hiç çağrı yok
	if err := db.SaveAlertConfig(storage.AlertConfig{WebhookURL: srv.URL, MinCriticality: 3, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	DispatchGlobalAlert(db, "q", 10, 1, findings[:1], now)
	if n, _ := catcher.snapshot(); n != 0 {
		t.Fatalf("yalnız eşik altı bulguyla çağrı yapıldı: %d", n)
	}

	// 3) Karışık → tam 1 çağrı; gövde sev3+sev5 içerir, sev1 içermez
	DispatchGlobalAlert(db, "leak-query", 10, 3, findings, now)
	n, body := catcher.snapshot()
	if n != 1 {
		t.Fatalf("tam 1 çağrı bekleniyordu, %d", n)
	}
	for _, want := range []string{"MID-sev3-title", "HIGH-sev5-title"} {
		if !strings.Contains(body, want) {
			t.Errorf("gövde %q içermeli: %s", want, body)
		}
	}
	if strings.Contains(body, "LOW-sev1-title") {
		t.Errorf("eşik altı başlık gönderilmemeli: %s", body)
	}
	if !strings.Contains(body, "leak-query") {
		t.Errorf("gövde sorguyu içermeli: %s", body)
	}

	// 4) Boş webhook → çağrı yok (etkin olsa bile)
	if err := db.SaveAlertConfig(storage.AlertConfig{WebhookURL: "", MinCriticality: 1, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	DispatchGlobalAlert(db, "q", 10, 3, findings, now)
	if n, _ := catcher.snapshot(); n != 1 {
		t.Fatalf("boş webhook ile ek çağrı yapıldı: %d", n)
	}
}
