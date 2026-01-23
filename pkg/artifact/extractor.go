package artifact

import (
	"regexp"
	"strings"
	"sync"
)

// ArtifactType artifact türü
type ArtifactType string

const (
	TypeEmail      ArtifactType = "email"
	TypeBitcoin    ArtifactType = "bitcoin"
	TypeMonero     ArtifactType = "monero"
	TypeIP         ArtifactType = "ip"
	TypeOnion      ArtifactType = "onion"
	TypeCreditCard ArtifactType = "credit_card"
	TypePhone      ArtifactType = "phone"
	TypeSSH        ArtifactType = "ssh_key"
	TypeAPIKey     ArtifactType = "api_key"
	TypeUsername   ArtifactType = "username"
	TypeHash       ArtifactType = "hash"
)

// Artifact tespit edilen artifact
type Artifact struct {
	Type       ArtifactType `json:"type"`
	Value      string       `json:"value"`
	Context    string       `json:"context"` // Bulunduğu bağlam (50 karakter öncesi/sonrası)
	SourceURL  string       `json:"source_url"`
	Confidence float64      `json:"confidence"` // 0-1 arası güven skoru
}

// Extractor artifact çıkarıcı
type Extractor struct {
	patterns map[ArtifactType]*regexp.Regexp
	mu       sync.RWMutex
}

// Compiled regex patterns (thread-safe singleton)
var (
	extractorInstance *Extractor
	extractorOnce     sync.Once
)

// NewExtractor yeni extractor oluşturur (singleton)
func NewExtractor() *Extractor {
	extractorOnce.Do(func() {
		extractorInstance = &Extractor{
			patterns: compilePatterns(),
		}
	})
	return extractorInstance
}

// compilePatterns tüm regex pattern'lerini derler
func compilePatterns() map[ArtifactType]*regexp.Regexp {
	patterns := make(map[ArtifactType]*regexp.Regexp)

	// Email - RFC 5322 basitleştirilmiş
	patterns[TypeEmail] = regexp.MustCompile(`(?i)[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// Bitcoin Address (Legacy P2PKH ve P2SH, Bech32)
	// Legacy: 1 ile başlar, 25-34 karakter
	// P2SH: 3 ile başlar, 25-34 karakter
	// Bech32: bc1 ile başlar
	patterns[TypeBitcoin] = regexp.MustCompile(`\b([13][a-km-zA-HJ-NP-Z1-9]{25,34}|bc1[a-zA-HJ-NP-Z0-9]{25,90})\b`)

	// Monero Address - 95 karakter, 4 ile başlar
	patterns[TypeMonero] = regexp.MustCompile(`\b4[0-9AB][1-9A-HJ-NP-Za-km-z]{93}\b`)

	// IPv4 Address
	patterns[TypeIP] = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)

	// Onion Address (v2 ve v3)
	// v2: 16 karakter + .onion
	// v3: 56 karakter + .onion
	patterns[TypeOnion] = regexp.MustCompile(`\b[a-z2-7]{16,56}\.onion\b`)

	// Credit Card (basit - Luhn kontrolü ayrı yapılabilir)
	// Visa: 4xxx, MC: 5xxx, Amex: 34/37
	patterns[TypeCreditCard] = regexp.MustCompile(`\b(?:4[0-9]{12}(?:[0-9]{3})?|5[1-5][0-9]{14}|3[47][0-9]{13})\b`)

	// Phone (uluslararası format)
	patterns[TypePhone] = regexp.MustCompile(`\+?[1-9]\d{1,14}`)

	// SSH Private Key marker
	patterns[TypeSSH] = regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`)

	// Common API Key patterns
	patterns[TypeAPIKey] = regexp.MustCompile(`(?i)(?:api[_-]?key|apikey|api[_-]?secret|access[_-]?token)["\s:=]+["']?([a-zA-Z0-9_\-]{20,})["']?`)

	// Username patterns (common formats)
	patterns[TypeUsername] = regexp.MustCompile(`(?i)(?:user(?:name)?|login|account)["\s:=]+["']?([a-zA-Z0-9_\-.]{3,32})["']?`)

	// Hash patterns (MD5, SHA1, SHA256)
	patterns[TypeHash] = regexp.MustCompile(`\b([a-fA-F0-9]{32}|[a-fA-F0-9]{40}|[a-fA-F0-9]{64})\b`)

	return patterns
}

// Extract metinden tüm artifact'ları çıkarır
func (e *Extractor) Extract(text, sourceURL string) []Artifact {
	var artifacts []Artifact
	seen := make(map[string]bool) // Duplicate kontrolü

	for artifactType, pattern := range e.patterns {
		matches := pattern.FindAllStringIndex(text, -1)

		for _, match := range matches {
			value := text[match[0]:match[1]]

			// Duplicate kontrolü
			key := string(artifactType) + ":" + value
			if seen[key] {
				continue
			}
			seen[key] = true

			// Bazı false positive'leri filtrele
			if !isValidArtifact(artifactType, value) {
				continue
			}

			// Context çıkar (50 karakter öncesi/sonrası)
			context := extractContext(text, match[0], match[1], 50)

			// Confidence hesapla
			confidence := calculateConfidence(artifactType, value, context)

			artifacts = append(artifacts, Artifact{
				Type:       artifactType,
				Value:      value,
				Context:    context,
				SourceURL:  sourceURL,
				Confidence: confidence,
			})
		}
	}

	return artifacts
}

// ExtractByType belirli türdeki artifact'ları çıkarır
func (e *Extractor) ExtractByType(text, sourceURL string, types ...ArtifactType) []Artifact {
	var artifacts []Artifact
	seen := make(map[string]bool)

	for _, artifactType := range types {
		pattern, exists := e.patterns[artifactType]
		if !exists {
			continue
		}

		matches := pattern.FindAllStringIndex(text, -1)

		for _, match := range matches {
			value := text[match[0]:match[1]]

			key := string(artifactType) + ":" + value
			if seen[key] {
				continue
			}
			seen[key] = true

			if !isValidArtifact(artifactType, value) {
				continue
			}

			context := extractContext(text, match[0], match[1], 50)
			confidence := calculateConfidence(artifactType, value, context)

			artifacts = append(artifacts, Artifact{
				Type:       artifactType,
				Value:      value,
				Context:    context,
				SourceURL:  sourceURL,
				Confidence: confidence,
			})
		}
	}

	return artifacts
}

// ExtractEmails sadece email adreslerini çıkarır
func (e *Extractor) ExtractEmails(text string) []string {
	return e.patterns[TypeEmail].FindAllString(text, -1)
}

// ExtractBitcoinAddresses sadece Bitcoin adreslerini çıkarır
func (e *Extractor) ExtractBitcoinAddresses(text string) []string {
	return e.patterns[TypeBitcoin].FindAllString(text, -1)
}

// ExtractMoneroAddresses sadece Monero adreslerini çıkarır
func (e *Extractor) ExtractMoneroAddresses(text string) []string {
	return e.patterns[TypeMonero].FindAllString(text, -1)
}

// ExtractIPs sadece IP adreslerini çıkarır
func (e *Extractor) ExtractIPs(text string) []string {
	matches := e.patterns[TypeIP].FindAllString(text, -1)
	var validIPs []string

	for _, ip := range matches {
		if isValidIP(ip) {
			validIPs = append(validIPs, ip)
		}
	}

	return validIPs
}

// ExtractOnionAddresses sadece .onion adreslerini çıkarır
func (e *Extractor) ExtractOnionAddresses(text string) []string {
	return e.patterns[TypeOnion].FindAllString(text, -1)
}

// isValidArtifact artifact'ın geçerli olup olmadığını kontrol eder
func isValidArtifact(artifactType ArtifactType, value string) bool {
	switch artifactType {
	case TypeEmail:
		return isValidEmail(value)
	case TypeIP:
		return isValidIP(value)
	case TypeBitcoin:
		return isValidBitcoinAddress(value)
	case TypeHash:
		return isValidHash(value)
	default:
		return true
	}
}

// isValidEmail email doğrulama
func isValidEmail(email string) bool {
	// Çok kısa email'leri filtrele
	if len(email) < 6 {
		return false
	}

	// Yaygın false positive'leri filtrele
	invalidDomains := []string{
		"example.com", "test.com", "localhost", "127.0.0.1",
		"your-domain.com", "yourdomain.com", "domain.com",
		"email.com", "mail.com", "sample.com",
	}

	emailLower := strings.ToLower(email)
	for _, domain := range invalidDomains {
		if strings.HasSuffix(emailLower, "@"+domain) {
			return false
		}
	}

	return true
}

// isValidIP IP doğrulama
func isValidIP(ip string) bool {
	// Private/reserved IP'leri filtrele (CTI için genellikle anlamsız)
	privateRanges := []string{
		"10.", "192.168.", "172.16.", "172.17.", "172.18.", "172.19.",
		"172.20.", "172.21.", "172.22.", "172.23.", "172.24.", "172.25.",
		"172.26.", "172.27.", "172.28.", "172.29.", "172.30.", "172.31.",
		"127.", "0.", "255.",
	}

	for _, prefix := range privateRanges {
		if strings.HasPrefix(ip, prefix) {
			return false
		}
	}

	return true
}

// isValidBitcoinAddress Bitcoin adresi doğrulama (basit)
func isValidBitcoinAddress(addr string) bool {
	// Uzunluk kontrolü
	if len(addr) < 26 || len(addr) > 90 {
		return false
	}
	return true
}

// isValidHash hash doğrulama
func isValidHash(hash string) bool {
	// Sadece hex karakterleri içermeli
	for _, c := range hash {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}

	// Yaygın false positive'leri filtrele (örn: sıralı karakterler)
	if hash == strings.Repeat("0", len(hash)) ||
		hash == strings.Repeat("f", len(hash)) ||
		hash == strings.Repeat("F", len(hash)) {
		return false
	}

	return true
}

// extractContext değerin etrafındaki bağlamı çıkarır
func extractContext(text string, start, end, contextLen int) string {
	// Başlangıç pozisyonu
	contextStart := start - contextLen
	if contextStart < 0 {
		contextStart = 0
	}

	// Bitiş pozisyonu
	contextEnd := end + contextLen
	if contextEnd > len(text) {
		contextEnd = len(text)
	}

	context := text[contextStart:contextEnd]

	// Whitespace normalize
	context = strings.Join(strings.Fields(context), " ")

	return context
}

// calculateConfidence güven skoru hesaplar
func calculateConfidence(artifactType ArtifactType, value, context string) float64 {
	confidence := 0.5 // Base confidence

	contextLower := strings.ToLower(context)

	switch artifactType {
	case TypeEmail:
		// Bilinen domain'ler güveni artırır
		knownDomains := []string{"gmail.com", "yahoo.com", "outlook.com", "protonmail.com", "tutanota.com"}
		for _, domain := range knownDomains {
			if strings.Contains(strings.ToLower(value), domain) {
				confidence += 0.2
				break
			}
		}
		// Bağlamda "email", "contact" gibi kelimeler
		if strings.Contains(contextLower, "email") || strings.Contains(contextLower, "contact") {
			confidence += 0.1
		}

	case TypeBitcoin:
		// Bağlamda "bitcoin", "btc", "wallet" gibi kelimeler
		if strings.Contains(contextLower, "bitcoin") ||
			strings.Contains(contextLower, "btc") ||
			strings.Contains(contextLower, "wallet") ||
			strings.Contains(contextLower, "address") {
			confidence += 0.3
		}

	case TypeMonero:
		if strings.Contains(contextLower, "monero") ||
			strings.Contains(contextLower, "xmr") ||
			strings.Contains(contextLower, "wallet") {
			confidence += 0.3
		}

	case TypeIP:
		// Bağlamda "server", "ip", "host" gibi kelimeler
		if strings.Contains(contextLower, "server") ||
			strings.Contains(contextLower, "ip") ||
			strings.Contains(contextLower, "host") {
			confidence += 0.2
		}

	case TypeOnion:
		// Onion adresleri genellikle güvenilir
		confidence = 0.9
	}

	// Max 1.0
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// GetSummary tüm artifact'ların özetini döndürür
func GetSummary(artifacts []Artifact) map[ArtifactType]int {
	summary := make(map[ArtifactType]int)
	for _, a := range artifacts {
		summary[a.Type]++
	}
	return summary
}

// FilterByConfidence belirli güven eşiğinin üzerindeki artifact'ları döndürür
func FilterByConfidence(artifacts []Artifact, minConfidence float64) []Artifact {
	var filtered []Artifact
	for _, a := range artifacts {
		if a.Confidence >= minConfidence {
			filtered = append(filtered, a)
		}
	}
	return filtered
}
