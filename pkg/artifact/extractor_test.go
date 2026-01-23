package artifact

import (
	"testing"
)

func TestExtractEmails(t *testing.T) {
	extractor := NewExtractor()

	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "Simple email",
			input:    "Contact us at test@example.org for more info",
			expected: []string{"test@example.org"},
		},
		{
			name:     "Multiple emails",
			input:    "Send to admin@site.com or support@company.net",
			expected: []string{"admin@site.com", "support@company.net"},
		},
		{
			name:     "Email with numbers",
			input:    "user123@domain456.co.uk is valid",
			expected: []string{"user123@domain456.co.uk"},
		},
		{
			name:     "No emails",
			input:    "This text has no email addresses",
			expected: nil,
		},
		{
			name:     "Email with special chars",
			input:    "test.user+tag@gmail.com works",
			expected: []string{"test.user+tag@gmail.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractor.ExtractEmails(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d emails, got %d", len(tt.expected), len(result))
				return
			}
			for i, email := range result {
				if email != tt.expected[i] {
					t.Errorf("Expected email %s, got %s", tt.expected[i], email)
				}
			}
		})
	}
}

func TestExtractBitcoinAddresses(t *testing.T) {
	extractor := NewExtractor()

	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "Legacy P2PKH address",
			input:    "Send BTC to 1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
			expected: 1,
		},
		{
			name:     "P2SH address",
			input:    "Multi-sig: 3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy",
			expected: 1,
		},
		{
			name:     "Bech32 address",
			input:    "Native segwit: bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq",
			expected: 1,
		},
		{
			name:     "No Bitcoin address",
			input:    "No crypto addresses here",
			expected: 0,
		},
		{
			name:     "Invalid - too short",
			input:    "Invalid: 1abc123",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractor.ExtractBitcoinAddresses(tt.input)
			if len(result) != tt.expected {
				t.Errorf("Expected %d Bitcoin addresses, got %d: %v", tt.expected, len(result), result)
			}
		})
	}
}

func TestExtractIPs(t *testing.T) {
	extractor := NewExtractor()

	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "Valid public IP",
			input:    "Server at 203.0.113.50",
			expected: 1,
		},
		{
			name:     "Multiple IPs",
			input:    "DNS: 8.8.8.8 and 8.8.4.4",
			expected: 2,
		},
		{
			name:     "Private IP filtered",
			input:    "Local: 192.168.1.1",
			expected: 0, // Private IPs are filtered
		},
		{
			name:     "Localhost filtered",
			input:    "Localhost: 127.0.0.1",
			expected: 0,
		},
		{
			name:     "Invalid IP format",
			input:    "Not an IP: 999.999.999.999",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractor.ExtractIPs(tt.input)
			if len(result) != tt.expected {
				t.Errorf("Expected %d IPs, got %d: %v", tt.expected, len(result), result)
			}
		})
	}
}

func TestExtractMoneroAddresses(t *testing.T) {
	extractor := NewExtractor()

	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "Valid Monero address",
			input:    "XMR: 4AdUndXHHZ6cfufTMvppY6JwXNouMBzSkbLYfpAV5Usx3skxNgYeYTRj5UzqtReoS44qo9mtmXCqY45DJ852K5Jv2684Rge",
			expected: 1,
		},
		{
			name:     "No Monero address",
			input:    "No monero here",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractor.ExtractMoneroAddresses(tt.input)
			if len(result) != tt.expected {
				t.Errorf("Expected %d Monero addresses, got %d", tt.expected, len(result))
			}
		})
	}
}

func TestExtractOnionAddresses(t *testing.T) {
	extractor := NewExtractor()

	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "v3 onion address",
			input:    "Visit duckduckgogg42xjoc72x3sjasowoarfbgcmvfimaftt6twagswzczad.onion",
			expected: 1,
		},
		{
			name:     "v2 onion address (16 chars)",
			input:    "Old style: expyuzzwqqyqhjna.onion",
			expected: 1,
		},
		{
			name:     "Multiple onion addresses",
			input:    "Sites: abcdefghijklmnop.onion and qrstuvwxyzabcdef.onion",
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractor.ExtractOnionAddresses(tt.input)
			if len(result) != tt.expected {
				t.Errorf("Expected %d onion addresses, got %d: %v", tt.expected, len(result), result)
			}
		})
	}
}

func TestExtractAll(t *testing.T) {
	extractor := NewExtractor()

	input := `
		Contact: admin@darkmarket.org
		Bitcoin: 1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa
		Server: 203.0.113.100
		Visit our site: abcdefghijklmnop.onion
	`

	artifacts := extractor.Extract(input, "http://test.onion")

	// En az 4 artifact bulunmalı
	if len(artifacts) < 4 {
		t.Errorf("Expected at least 4 artifacts, got %d", len(artifacts))
	}

	// Type kontrolü
	types := make(map[ArtifactType]bool)
	for _, a := range artifacts {
		types[a.Type] = true
	}

	expectedTypes := []ArtifactType{TypeEmail, TypeBitcoin, TypeIP, TypeOnion}
	for _, et := range expectedTypes {
		if !types[et] {
			t.Errorf("Expected to find artifact type %s", et)
		}
	}
}

func TestFilterByConfidence(t *testing.T) {
	artifacts := []Artifact{
		{Type: TypeEmail, Value: "test@gmail.com", Confidence: 0.9},
		{Type: TypeEmail, Value: "low@unknown.xyz", Confidence: 0.3},
		{Type: TypeIP, Value: "8.8.8.8", Confidence: 0.7},
	}

	filtered := FilterByConfidence(artifacts, 0.5)

	if len(filtered) != 2 {
		t.Errorf("Expected 2 artifacts with confidence >= 0.5, got %d", len(filtered))
	}
}

func TestGetSummary(t *testing.T) {
	artifacts := []Artifact{
		{Type: TypeEmail, Value: "a@b.com"},
		{Type: TypeEmail, Value: "c@d.com"},
		{Type: TypeBitcoin, Value: "1abc..."},
		{Type: TypeIP, Value: "1.2.3.4"},
	}

	summary := GetSummary(artifacts)

	if summary[TypeEmail] != 2 {
		t.Errorf("Expected 2 emails, got %d", summary[TypeEmail])
	}
	if summary[TypeBitcoin] != 1 {
		t.Errorf("Expected 1 bitcoin, got %d", summary[TypeBitcoin])
	}
}

func TestIsValidEmail(t *testing.T) {
	tests := []struct {
		email    string
		expected bool
	}{
		{"real@gmail.com", true},
		{"user@protonmail.com", true},
		{"test@example.com", false}, // example.com filtered
		{"a@b.c", false},            // too short
		{"admin@localhost", false},  // localhost filtered
	}

	for _, tt := range tests {
		result := isValidEmail(tt.email)
		if result != tt.expected {
			t.Errorf("isValidEmail(%s) = %v, expected %v", tt.email, result, tt.expected)
		}
	}
}

func TestIsValidIP(t *testing.T) {
	tests := []struct {
		ip       string
		expected bool
	}{
		{"8.8.8.8", true},
		{"203.0.113.50", true},
		{"192.168.1.1", false}, // Private
		{"10.0.0.1", false},    // Private
		{"127.0.0.1", false},   // Localhost
		{"172.16.0.1", false},  // Private
	}

	for _, tt := range tests {
		result := isValidIP(tt.ip)
		if result != tt.expected {
			t.Errorf("isValidIP(%s) = %v, expected %v", tt.ip, result, tt.expected)
		}
	}
}

// Benchmark tests
func BenchmarkExtract(b *testing.B) {
	extractor := NewExtractor()
	text := `
		Contact: admin@darkmarket.org and support@hidden.onion
		Bitcoin: 1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa
		Server: 203.0.113.100
		Visit: abc123def456ghij.onion
		Hash: 5d41402abc4b2a76b9719d911017c592
	`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractor.Extract(text, "http://test.onion")
	}
}

func BenchmarkExtractEmails(b *testing.B) {
	extractor := NewExtractor()
	text := "Contact us at admin@site.com or support@company.net for assistance."

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractor.ExtractEmails(text)
	}
}
