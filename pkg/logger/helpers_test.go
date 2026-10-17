package logger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPackageHelpers_WriteStructuredLines işlem bazlı yardımcılar (arama, çekim,
// derinleştirme, giriş, sistem) beklenen önek ve seviyelerle dosyaya yazar; Gin
// Writer'ı satırı kırpar; uzun URL'ler kısaltılır.
func TestPackageHelpers_WriteStructuredLines(t *testing.T) {
	dir := t.TempDir()
	// Init tekildir (sync.Once); paket yardımcılarını izole bir örnekle sına.
	prev := instance
	instance = &Logger{logDir: dir, minLevel: DEBUG}
	t.Cleanup(func() { instance = prev })
	if err := instance.rotateIfNeeded(); err != nil {
		t.Fatal(err)
	}
	long := "http://" + strings.Repeat("a", 56) + ".onion/" + strings.Repeat("p", 80)

	SearchStarted("leak", 3)
	SearchEngineResult("Tordex", 5, nil)
	SearchEngineResult("Tor66", 0, nil)
	SearchEngineResult("Torch", 0, errors.New("zaman aşımı"))
	SearchCompleted("leak", 5, 1500*time.Millisecond)
	ScrapeStarted(2)
	ScrapeResult(long, true, "")
	ScrapeResult(long, false, "HTTP 404")
	ScrapeCompleted(2, 1, 1, time.Second)
	DatabaseOperation("insert", "1 satır")
	DatabaseError("insert", errors.New("kilit"))
	WebRequest("GET", "/api/x", 200, 3*time.Millisecond)
	UserLogin("admin", true, "127.0.0.1")
	UserLogin("admin", false, "127.0.0.1")
	ExpandNode(long, 4, nil)
	ExpandNode(long, 0, errors.New("erişilemez"))
	SystemStartup("127.0.0.1:9050", "data/kh.db", ":8080")
	SystemReady(":8080", "admin")
	SystemShutdown()
	_, _ = GetInstance().Writer().Write([]byte("  [GIN] 200 GET /  \n"))
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	if err := Close(); err != nil {
		t.Fatalf("ikinci Close hata vermemeli: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, time.Now().Format("2006-01-02")+".log"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"SEARCH STARTED: Query='leak' at 3 engines", "SEARCH ENGINE SUCCESS: Tordex - 5", "SEARCH ENGINE NO RESULTS: Tor66",
		"[WARN] SEARCH ENGINE ERROR: Torch", "SCRAPE FAILED", "DATABASE ERROR: insert - kilit", "USER LOGIN FAILED",
		"GRAPH EXPAND ERROR", "URL: http://localhost:8080", "SYSTEM SHUTDOWN", "[GIN] 200 GET /",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("log %q içermeli", want)
		}
	}
	if strings.Contains(s, strings.Repeat("p", 80)) {
		t.Error("uzun URL kısaltılmalı")
	}
	if strings.Contains(s, "  [GIN]") {
		t.Error("Writer satırı kırpmalı")
	}
}
