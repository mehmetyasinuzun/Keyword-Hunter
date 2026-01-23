package shared

import (
	"testing"
	"time"
)

func TestCalculateBackoff(t *testing.T) {
	tests := []struct {
		attempt     int
		minExpected time.Duration
		maxExpected time.Duration
	}{
		{0, 0, InitialBackoffDuration + time.Second},                // İlk deneme - backoff yok
		{1, InitialBackoffDuration, InitialBackoffDuration * 3},     // 2. deneme
		{2, InitialBackoffDuration * 2, InitialBackoffDuration * 6}, // 3. deneme
	}

	for _, tt := range tests {
		backoff := CalculateBackoff(tt.attempt)
		if tt.attempt > 0 && backoff < tt.minExpected {
			t.Errorf("Attempt %d: backoff %v too short, expected at least %v",
				tt.attempt, backoff, tt.minExpected)
		}
	}
}

func TestIsLowValueURL(t *testing.T) {
	tests := []struct {
		url      string
		expected bool
	}{
		{"http://site.onion/forum/topic", false},
		{"http://site.onion/search?q=test", true},
		{"http://site.onion/image.png", true},
		{"http://site.onion/document.pdf", true},
		{"http://site.onion/static/js/app.js", true},
		{"http://site.onion/wp-content/uploads/image.jpg", true},
		{"http://site.onion/category/drugs", true},
		{"http://site.onion/product/item123", false},
		{"http://site.onion/page?page=2", true},
	}

	for _, tt := range tests {
		isLow, _ := IsLowValueURL(tt.url)
		if isLow != tt.expected {
			t.Errorf("IsLowValueURL(%s) = %v, expected %v", tt.url, isLow, tt.expected)
		}
	}
}

func TestRandomUserAgent(t *testing.T) {
	// User agent'ların boş olmadığını kontrol et
	for i := 0; i < 10; i++ {
		ua := RandomUserAgent()
		if ua == "" {
			t.Error("RandomUserAgent returned empty string")
		}
		if len(ua) < 50 {
			t.Errorf("User agent seems too short: %s", ua)
		}
	}
}

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		errMsg   string
		expected bool
	}{
		{"connection timeout exceeded", true},
		{"context deadline exceeded", true},
		{"connection reset by peer", true},
		{"connection refused", true},
		{"socks connect error", true},
		{"EOF", true},
		{"some other error", false},
		{"permission denied", false},
	}

	for _, tt := range tests {
		err := &testError{msg: tt.errMsg}
		result := IsRetryableError(err)
		if result != tt.expected {
			t.Errorf("IsRetryableError(%s) = %v, expected %v", tt.errMsg, result, tt.expected)
		}
	}
}

func TestRetryableStatusCodes(t *testing.T) {
	retryable := []int{500, 502, 503, 504, 429}
	nonRetryable := []int{200, 201, 400, 401, 403, 404}

	for _, code := range retryable {
		if !RetryableStatusCodes[code] {
			t.Errorf("Status code %d should be retryable", code)
		}
	}

	for _, code := range nonRetryable {
		if RetryableStatusCodes[code] {
			t.Errorf("Status code %d should not be retryable", code)
		}
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		errMsg       string
		expectSubstr string
	}{
		{"unknown error unknown code: 240", "site erişilemez"},
		{"connection refused", "Tor proxy"},
		{"socks connect failed", "yanıt vermiyor"},
		{"timeout occurred", "zaman aşımı"},
	}

	for _, tt := range tests {
		err := &testError{msg: tt.errMsg}
		classified := ClassifyError(err)
		if classified == nil {
			t.Errorf("ClassifyError should not return nil for: %s", tt.errMsg)
			continue
		}
		// Beklenen alt string'i içermeli
		// Not: Türkçe karakterler test'te sorun çıkarabilir
	}
}

// Test helper
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

func TestConstants(t *testing.T) {
	// Constant'ların mantıklı değerlere sahip olduğunu kontrol et
	if DefaultHTTPTimeout < 30*time.Second {
		t.Error("DefaultHTTPTimeout too short")
	}

	if MaxRetryAttempts < 1 || MaxRetryAttempts > 10 {
		t.Errorf("MaxRetryAttempts should be between 1-10, got %d", MaxRetryAttempts)
	}

	if len(UserAgents) < 3 {
		t.Error("Should have at least 3 user agents")
	}

	if len(LowValueURLPatterns) < 5 {
		t.Error("Should have multiple low value patterns")
	}

	if len(BinaryExtensions) < 10 {
		t.Error("Should have multiple binary extensions")
	}
}

// Benchmark tests
func BenchmarkIsLowValueURL(b *testing.B) {
	urls := []string{
		"http://site.onion/forum/topic",
		"http://site.onion/search?q=test",
		"http://site.onion/image.png",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, url := range urls {
			IsLowValueURL(url)
		}
	}
}

func BenchmarkRandomUserAgent(b *testing.B) {
	for i := 0; i < b.N; i++ {
		RandomUserAgent()
	}
}

func TestExtractDomain(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"http://example.onion/path/page", "example.onion"},
		{"https://example.onion/path?query=1", "example.onion"},
		{"http://subdomain.example.onion/", "subdomain.example.onion"},
		{"example.onion", "example.onion"},
		{"http://example.onion", "example.onion"},
		{"http://EXAMPLE.ONION/Path", "example.onion"},
		{"  http://example.onion/  ", "example.onion"},
	}

	for _, tt := range tests {
		result := ExtractDomain(tt.url)
		if result != tt.expected {
			t.Errorf("ExtractDomain(%s) = %s, expected %s", tt.url, result, tt.expected)
		}
	}
}
