package backup

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"keywordhunter-mvp/pkg/storage"
)

func TestCLI_BackupThenVerify(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "kh.db")
	db, err := storage.New(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendAuditEvent(storage.AuditEvent{Actor: "admin", Action: "results.delete", Success: true}); err != nil {
		t.Fatal(err)
	}
	db.Close()

	// Kaynak DB_PATH'ten (ortam) çözülür
	t.Setenv("DB_PATH", src)
	out := filepath.Join(dir, "copy.db")
	var stdout, stderr bytes.Buffer
	if err := RunCommand("backup", []string{"-out", out}, "0.17.0", filepath.Join(dir, "missing.env"), &stdout, &stderr); err != nil {
		t.Fatalf("backup: %v (%s)", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "integrity_check: ok") || !strings.Contains(stdout.String(), "APP_SECRET") {
		t.Fatalf("çıktı: %s", stdout.String())
	}
	// Var olan dosyanın üzerine yazılmaz
	if err := RunCommand("backup", []string{"-out", out}, "0.17.0", "", &stdout, &stderr); err == nil {
		t.Fatal("var olan hedef reddedilmeli")
	}

	stdout.Reset()
	if err := RunCommand("verify-db", []string{out}, "", "", &stdout, &stderr); err != nil {
		t.Fatalf("verify-db: %v\n%s", err, stdout.String())
	}
	for _, want := range []string{"integrity_check:  ok", fmt.Sprintf("Şema sürümü:      %d", storage.LatestSchemaVersion()), "Denetim zinciri:  ok (1 olay", "Sonuç:            OK"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("rapor %q içermiyor:\n%s", want, stdout.String())
		}
	}
	stdout.Reset()
	if err := RunCommand("verify-db", []string{"-json", out}, "", "", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var rep map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil || rep["ok"] != true || rep["schemaVersion"].(float64) != float64(storage.LatestSchemaVersion()) {
		t.Fatalf("JSON rapor: %v %s", err, stdout.String())
	}

	// Kurcalanmış zincir: verify-db başarısız (çıkış kodu 1)
	c, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(`DROP TRIGGER audit_events_no_update; UPDATE audit_events SET actor = 'x'`); err != nil {
		t.Fatal(err)
	}
	c.Close()
	stdout.Reset()
	if err := RunCommand("verify-db", []string{out}, "", "", &stdout, &stderr); err == nil || !strings.Contains(stdout.String(), "KIRIK") {
		t.Fatalf("kırık zincir raporlanmalı: %v\n%s", err, stdout.String())
	}
}

func TestCLI_ArgumentErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := RunCommand("verify-db", nil, "", "", &stdout, &stderr); err == nil {
		t.Fatal("dosyasız verify-db hata vermeli")
	}
	if err := RunCommand("verify-db", []string{filepath.Join(t.TempDir(), "nope.db")}, "", "", &stdout, &stderr); err == nil {
		t.Fatal("olmayan dosya hata vermeli")
	}
	if err := RunCommand("backup", []string{"-db", filepath.Join(t.TempDir(), "nope.db"), "-out", filepath.Join(t.TempDir(), "x.db")}, "", "", &stdout, &stderr); err == nil {
		t.Fatal("olmayan kaynak hata vermeli")
	}
	if err := RunCommand("backup", []string{"extra"}, "", "", &stdout, &stderr); err == nil {
		t.Fatal("fazla argüman reddedilmeli")
	}
	if err := RunCommand("restore", nil, "", "", &stdout, &stderr); err == nil {
		t.Fatal("bilinmeyen komut")
	}
	if Commands["serve"] || !Commands["backup"] || !Commands["verify-db"] {
		t.Fatal("komut listesi")
	}
	if _, err := os.Stat("nope.db"); err == nil {
		t.Fatal("yan etki: dosya oluşturuldu")
	}
}
