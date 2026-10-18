package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadWith geçici bir .env ile Load çağırır; süreç ortamındaki ilgili
// değişkenler testi etkilemesin diye boşaltılır.
func loadWith(t *testing.T, content string) (AppConfig, *EnvStore, error) {
	t.Helper()
	for _, k := range []string{"ADMIN_USER", "ADMIN_PASS", "ADMIN_PASS_HASH", "APP_SECRET", "REQUIRE_2FA",
		"BACKUP_DIR", "BACKUP_INTERVAL_HOURS", "BACKUP_KEEP", "RESULT_RETENTION_DAYS", "SCREENSHOT_RETENTION_DAYS"} {
		t.Setenv(k, "")
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	return cfg, NewEnvStore(path), err
}

func TestLoad_Require2FA(t *testing.T) {
	cfg, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\n")
	if err != nil || cfg.Require2FA != Require2FANone {
		t.Fatalf("varsayılan none olmalı: %q %v", cfg.Require2FA, err)
	}
	cfg, _, err = loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\nREQUIRE_2FA=Admins\n")
	if err != nil || cfg.Require2FA != Require2FAAdmins {
		t.Fatalf("admins: %q %v", cfg.Require2FA, err)
	}
	if _, _, err = loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\nREQUIRE_2FA=yes\n"); err == nil {
		t.Fatal("geçersiz REQUIRE_2FA reddedilmeli")
	}
}

func TestLoad_AppSecretValidation(t *testing.T) {
	good := strings.Repeat("Ab", 32)
	cfg, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\nAPP_SECRET="+good+"\n")
	if err != nil || cfg.AppSecret != strings.ToLower(good) {
		t.Fatalf("geçerli APP_SECRET: %v", err)
	}
	for _, bad := range []string{"short", strings.Repeat("zz", 32), strings.Repeat("a", 66)} {
		if _, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\nAPP_SECRET="+bad+"\n"); err == nil {
			t.Errorf("%q reddedilmeli", bad)
		} else if strings.Contains(err.Error(), bad) {
			t.Errorf("hata mesajı sır değerini içermemeli: %v", err)
		}
	}
}

func TestEnsureAppSecret_GeneratesAndPersists(t *testing.T) {
	cfg, store, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\n")
	if err != nil {
		t.Fatal(err)
	}
	created, err := EnsureAppSecret(store, &cfg)
	if err != nil || !created || !validAppSecret(cfg.AppSecret) {
		t.Fatalf("üretim: %v %v %q", created, err, cfg.AppSecret)
	}
	vals, _ := store.Read()
	if vals["APP_SECRET"] != cfg.AppSecret || vals["ADMIN_USER"] != "a" {
		t.Fatalf(".env'e yazılmalı ve diğer anahtarlar korunmalı: %v", vals)
	}
	// Yeniden yükleme aynı sırrı okur; ikinci çağrı yeni sır üretmez
	cfg2, err := Load(store.Path())
	if err != nil || cfg2.AppSecret != cfg.AppSecret {
		t.Fatalf("kalıcı sır yeniden okunmalı: %v", err)
	}
	if created, err := EnsureAppSecret(store, &cfg2); created || err != nil {
		t.Fatalf("var olan sır yeniden üretilmemeli: %v %v", created, err)
	}
	// Yazılamayan depo: sır üretilir ama hata döner (çağıran geçici sayar)
	cfg3 := AppConfig{}
	ro := NewEnvStore(filepath.Join(t.TempDir(), "missing-dir", "sub", ".env"))
	if err := os.WriteFile(filepath.Join(filepath.Dir(filepath.Dir(ro.Path()))), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	created, err = EnsureAppSecret(ro, &cfg3)
	if !created || err == nil || !validAppSecret(cfg3.AppSecret) {
		t.Fatalf("yazılamayan depo hata döndürmeli: %v %v", created, err)
	}
	if strings.Contains(err.Error(), cfg3.AppSecret) {
		t.Fatal("hata mesajı sırrı içermemeli")
	}
}

// APP_SECRET üretimi .env'deki yorumları ve diğer satırları silmemeli.
func TestEnsureAppSecret_PreservesComments(t *testing.T) {
	body := "# Yönetici hesabı\nADMIN_USER=a\n\n# Parola (ilk girişte değiştirin)\nADMIN_PASS=bbbbbbbb\n# REQUIRE_2FA=all\n"
	cfg, store, err := loadWith(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if created, err := EnsureAppSecret(store, &cfg); err != nil || !created {
		t.Fatalf("üretim: %v %v", created, err)
	}
	raw, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.HasPrefix(got, body) || got != body+"APP_SECRET="+cfg.AppSecret+"\n" {
		t.Fatalf("yorumlar korunmalı, APP_SECRET sona eklenmeli:\n%s", got)
	}
	// Var olan anahtar yerinde değişir, diğer satırlar korunur
	if err := store.SetPreserving("ADMIN_USER", "b"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(store.Path())
	if !strings.Contains(string(raw), "# Yönetici hesabı\nADMIN_USER=b\n\n# Parola") {
		t.Fatalf("yerinde güncelleme: %s", raw)
	}
	if err := store.SetPreserving("BAD=KEY", "x"); err == nil {
		t.Fatal("geçersiz anahtar reddedilmeli")
	}
}

func TestCheckRequire2FAStartup(t *testing.T) {
	cases := []struct {
		policy    string
		persisted bool
		wantErr   bool
	}{
		{Require2FANone, false, false},
		{"", false, false},
		{Require2FAAdmins, true, false},
		{Require2FAAll, true, false},
		{Require2FAAdmins, false, true},
		{Require2FAAll, false, true},
	}
	for _, c := range cases {
		if err := CheckRequire2FAStartup(c.policy, c.persisted); (err != nil) != c.wantErr {
			t.Errorf("%s persisted=%v: %v", c.policy, c.persisted, err)
		}
	}
}

func TestLoad_BackupSettings(t *testing.T) {
	cfg, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\n")
	if err != nil || cfg.BackupDir != "" || cfg.BackupIntervalHours != 24 || cfg.BackupKeep != 7 {
		t.Fatalf("varsayılanlar (kapalı, 24 saat, 7): %+v %v", cfg, err)
	}
	cfg, _, err = loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\nBACKUP_DIR=/data/backups\nBACKUP_INTERVAL_HOURS=6\nBACKUP_KEEP=30\n")
	if err != nil || cfg.BackupDir != "/data/backups" || cfg.BackupIntervalHours != 6 || cfg.BackupKeep != 30 {
		t.Fatalf("özel değerler: %+v %v", cfg, err)
	}
	for _, bad := range []string{"BACKUP_INTERVAL_HOURS=0", "BACKUP_INTERVAL_HOURS=x", "BACKUP_KEEP=0", "BACKUP_KEEP=1001"} {
		if _, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\n"+bad+"\n"); err == nil {
			t.Errorf("%s reddedilmeli", bad)
		}
	}
}

func TestResolveDBPath(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	t.Setenv("DB_PATH", "")
	if p, err := ResolveDBPath(filepath.Join(dir, "missing.env")); err != nil || p != "keywordhunter.db" {
		t.Fatalf("varsayılan: %q %v", p, err)
	}
	if err := os.WriteFile(env, []byte("DB_PATH=/data/kh.db\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := ResolveDBPath(env); err != nil || p != "/data/kh.db" {
		t.Fatalf(".env: %q %v", p, err)
	}
	t.Setenv("DB_PATH", "/env/kh.db")
	if p, err := ResolveDBPath(env); err != nil || p != "/env/kh.db" {
		t.Fatalf("ortam önceliği: %q %v", p, err)
	}
}

func TestLoad_RetentionSettings(t *testing.T) {
	cfg, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\n")
	if err != nil || cfg.ResultRetentionDays != 0 || cfg.ScreenshotRetentionDays != 0 {
		t.Fatalf("varsayılan 0 (süresiz): %+v %v", cfg, err)
	}
	cfg, _, err = loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\nRESULT_RETENTION_DAYS=180\nSCREENSHOT_RETENTION_DAYS=30\n")
	if err != nil || cfg.ResultRetentionDays != 180 || cfg.ScreenshotRetentionDays != 30 {
		t.Fatalf("özel değerler: %+v %v", cfg, err)
	}
	for _, bad := range []string{"RESULT_RETENTION_DAYS=-1", "SCREENSHOT_RETENTION_DAYS=abc", "RESULT_RETENTION_DAYS=36501"} {
		if _, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\n"+bad+"\n"); err == nil {
			t.Errorf("%s reddedilmeli", bad)
		}
	}
}

func TestLoad_MetricsEnabled(t *testing.T) {
	cfg, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\n")
	if err != nil || cfg.MetricsEnabled {
		t.Fatalf("varsayılan kapalı olmalı: %v %v", cfg.MetricsEnabled, err)
	}
	for raw, want := range map[string]bool{"true": true, "1": true, "TRUE": true, "false": false, "0": false, "ture": false} {
		cfg, _, err := loadWith(t, "ADMIN_USER=a\nADMIN_PASS=bbbbbbbb\nMETRICS_ENABLED="+raw+"\n")
		if err != nil || cfg.MetricsEnabled != want {
			t.Errorf("METRICS_ENABLED=%s → %v (%v), beklenen %v", raw, cfg.MetricsEnabled, err, want)
		}
	}
}
