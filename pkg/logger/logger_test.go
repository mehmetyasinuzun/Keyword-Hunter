package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLogger_LevelFilterAndDailyFile: minLevel altındaki satırlar yazılmamalı,
// üstündekiler bugünün tarih adlı dosyasına düşmeli (günlük döndürme sözleşmesi).
func TestLogger_LevelFilterAndDailyFile(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, WARN, false); err != nil {
		t.Fatal(err)
	}
	l := GetInstance()
	l.Info("gizli-INFO-satiri-%d", 1)     // WARN eşiğinin altında → yazılmamalı
	l.Warn("gorunur-WARN-satiri-%d", 2)   // yazılmalı
	l.Error("gorunur-ERROR-satiri-%d", 3) // yazılmalı
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(dir, time.Now().Format("2006-01-02")+".log")
	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("bugünün log dosyası yok (%s): %v", want, err)
	}
	s := string(b)
	if strings.Contains(s, "gizli-INFO-satiri") {
		t.Errorf("INFO satırı WARN eşiğine rağmen yazıldı:\n%s", s)
	}
	for _, w := range []string{"gorunur-WARN-satiri-2", "gorunur-ERROR-satiri-3", "[WARN]", "[ERROR]"} {
		if !strings.Contains(s, w) {
			t.Errorf("log %q içermeli:\n%s", w, s)
		}
	}
	// Dosyaya ANSI renk kodu sızmamalı (yalnız konsol için)
	if strings.Contains(s, "\033[") {
		t.Errorf("dosyaya ANSI kaçış kodu yazıldı:\n%s", s)
	}
}
