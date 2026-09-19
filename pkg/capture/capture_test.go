package capture

import (
	"path/filepath"
	"testing"
)

// findChrome hiçbir platformda panic etmemeli; CHROME_BIN öncelikli olmalı.
func TestNewCapturer_ChromeDiscovery(t *testing.T) {
	_ = findChrome()
	t.Setenv("CHROME_BIN", filepath.Join(t.TempDir(), "yok"))
	c := New("127.0.0.1:9150", "", t.TempDir())
	if c.Available() {
		t.Fatal("var olmayan CHROME_BIN ile Available() false olmalı")
	}
	if c.OutDir() == "" {
		t.Fatal("outDir boş olmamalı")
	}
	if _, err := c.Capture(t.Context(), "http://x.onion"); err == nil {
		t.Fatal("chrome yokken Capture hata döndürmeli")
	}
	if c.safeHost("http://ABC.onion/p?q=1") != "abc.onion" || c.safeHost("garbage") != "garbage" {
		t.Fatal("safeHost beklenen gibi değil")
	}
}
