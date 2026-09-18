package config

import (
	"path/filepath"
	"testing"
)

func TestEnvStore_RoundTripsSpecialCharacters(t *testing.T) {
	store := NewEnvStore(filepath.Join(t.TempDir(), ".env"))
	cases := map[string]string{
		"ADMIN_USER":  "admin",
		"ADMIN_PASS":  `pa"ss wo\rd#1=2'x`,
		"TOR_PROXY":   "tor:9050",
		"LOG_LEVEL":   "info",
		"CUSTOM_NOTE": "değer ile boşluk ve türkçe",
	}
	if err := store.Update(cases); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := store.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	// Boş değer anahtarı siler
	if err := store.Update(map[string]string{"CUSTOM_NOTE": ""}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Read()
	if _, ok := got["CUSTOM_NOTE"]; ok {
		t.Fatalf("boş değer anahtarı silmeli")
	}
}

func TestUnquoteEnvValue(t *testing.T) {
	cases := map[string]string{
		`plain`:          "plain",
		`"quoted value"`: "quoted value",
		`'single q'`:     "single q",
		`"esc\"aped"`:    `esc"aped`,
		`val # comment`:  "val",
		`  spaced  `:     "spaced",
	}
	for in, want := range cases {
		if got := unquoteEnvValue(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoad_RequiresCredentialsAndValidatesRanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	store := NewEnvStore(path)
	if _, err := Load(path); err == nil {
		t.Fatalf("kimlik bilgisi olmadan Load başarılı olmamalı")
	}
	_ = store.Update(map[string]string{"ADMIN_USER": "a", "ADMIN_PASS_HASH": "not-bcrypt"})
	if _, err := Load(path); err == nil {
		t.Fatalf("geçersiz hash kabul edilmemeli")
	}
	_ = store.Update(map[string]string{"ADMIN_PASS_HASH": "", "ADMIN_PASS": "x", "RATE_LIMIT_RPS": "999"})
	if _, err := Load(path); err == nil {
		t.Fatalf("aralık dışı RATE_LIMIT_RPS kabul edilmemeli")
	}
	_ = store.Update(map[string]string{"RATE_LIMIT_RPS": "20", "SESSION_TTL_HOURS": "48"})
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("geçerli yapılandırma yüklenemedi: %v", err)
	}
	if cfg.SessionTTLHours != 48 || cfg.RateLimitRPS != 20 || cfg.AdminUser != "a" {
		t.Fatalf("beklenmeyen config: %+v", cfg)
	}
}
