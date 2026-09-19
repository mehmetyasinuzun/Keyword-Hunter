package shared

import (
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	secret := "https://hooks.slack.com/services/T0AAA/B0BBB/SuperSecretToken123"
	got := RedactURL(secret)
	if strings.Contains(got, "SuperSecret") || strings.Contains(got, "T0AAA") {
		t.Fatalf("sır sızdı: %q", got)
	}
	if got != "https://hooks.slack.com/…" {
		t.Fatalf("beklenmeyen biçim: %q", got)
	}
	cases := map[string]string{
		"":                                      "",
		"https://example.com":                   "https://example.com",
		"https://example.com/":                  "https://example.com",
		"https://u:p@example.com/x?k=v#f":       "https://example.com/…",
		"not a url":                             "<gizlendi>",
		"http://discord.com/api/webhooks/1/abc": "http://discord.com/…",
	}
	for in, want := range cases {
		if g := RedactURL(in); g != want {
			t.Errorf("RedactURL(%q)=%q, want %q", in, g, want)
		}
	}
}
