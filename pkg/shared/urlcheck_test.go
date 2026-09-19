package shared

import "testing"

func TestIsOnionURL(t *testing.T) {
	v3 := "http://tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/search?q=x"
	cases := map[string]bool{
		v3:                                true,
		"https://abcdefghijklmnop.onion/": true,  // v2 (eski kayıt uyumu)
		"http://example.com/":             false, // onion değil
		"ftp://tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/": false,
		"javascript:alert(1).onion":      false,
		"http://not-base32-UPPER.onion/": false,
		"http://short.onion/":            false,
		"":                               false,
		"http://sub.tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/x": true,
	}
	for in, want := range cases {
		if got := IsOnionURL(in); got != want {
			t.Errorf("IsOnionURL(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsPublicWebURL(t *testing.T) {
	cases := []struct {
		raw        string
		allowOnion bool
		want       bool
	}{
		{"https://hooks.slack.com/services/T/B/x", false, true},
		{"https://discord.com/api/webhooks/1/abc", false, true},
		{"http://127.0.0.1:9050/", false, false},
		{"http://localhost:8080/", false, false},
		{"http://10.0.0.5/", false, false},
		{"http://192.168.1.1/", false, false},
		{"http://169.254.169.254/latest/meta-data", false, false},
		{"http://100.64.1.1/", false, false},
		{"http://[::1]/", false, false},
		{"http://tor:9050/", false, true}, // tek etiketli host; DNS çözümü SafeDialControl'de reddedilir
		{"http://user:pass@example.com/", false, false},
		{"ftp://example.com/", false, false},
		{"http://tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/", false, false},
		{"http://tordexu73joywapk2txdr54jed4imqledpcvcuf75qsas2gwdgksvnyd.onion/", true, true},
		{"http://x.internal/", false, false},
		{"", false, false},
	}
	for _, tc := range cases {
		if got := IsPublicWebURL(tc.raw, tc.allowOnion); got != tc.want {
			t.Errorf("IsPublicWebURL(%q, %v) = %v, want %v", tc.raw, tc.allowOnion, got, tc.want)
		}
	}
}

func TestSafeDialControl_RejectsPrivate(t *testing.T) {
	if err := SafeDialControl("tcp", "127.0.0.1:80", nil); err == nil {
		t.Fatal("loopback reddedilmeli")
	}
	if err := SafeDialControl("tcp", "10.1.2.3:443", nil); err == nil {
		t.Fatal("özel ağ reddedilmeli")
	}
	if err := SafeDialControl("tcp", "93.184.216.34:443", nil); err != nil {
		t.Fatalf("genel adres kabul edilmeli: %v", err)
	}
}

func TestSplitHostPort(t *testing.T) {
	if _, _, err := SplitHostPort("127.0.0.1:9150"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "127.0.0.1", ":9050", "host:0", "host:99999", "http://x:1"} {
		if _, _, err := SplitHostPort(bad); err == nil {
			t.Errorf("%q kabul edilmemeli", bad)
		}
	}
}

func TestDetectChallenge(t *testing.T) {
	if ok, kind := DetectChallenge("Just a moment...", "<html>"); !ok || kind != "Cloudflare" {
		t.Fatalf("Cloudflare tespit edilmeli: %v %s", ok, kind)
	}
	if ok, _ := DetectChallenge("Forum", "welcome to the forum, please login"); ok {
		t.Fatal("normal sayfa engel sayılmamalı")
	}
}

func TestTruncateRuneSafe(t *testing.T) {
	s := "Şifreli veritabanı sızıntısı"
	got := Truncate(s, 10)
	if got != "Şifreli ve..." {
		t.Fatalf("Truncate = %q", got)
	}
	if TruncateRunes("abc", -1) != "" {
		t.Fatal("negatif uzunluk boş dönmeli")
	}
}

func TestIsHTTPURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"http://10.0.0.5:8080/hook": true, "https://siem.corp.local/in": true,
		"ftp://x/": false, "http://user:pw@x/": false, "": false, "http:///": false,
	} {
		if got := IsHTTPURL(raw); got != want {
			t.Errorf("IsHTTPURL(%q)=%v want %v", raw, got, want)
		}
	}
}
