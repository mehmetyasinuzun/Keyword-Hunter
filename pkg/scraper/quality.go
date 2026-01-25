package scraper

import (
	"crypto/md5"
	"encoding/hex"
	"regexp"
	"strings"

	"keywordhunter-mvp/pkg/shared"
)

// calculateQualityScore içerik kalite skoru hesaplar
func (s *Scraper) calculateQualityScore(text string, title string) int {
	lowerText := strings.ToLower(text)
	lowerTitle := strings.ToLower(title)
	score := 0

	// 1. Uzunluk bazlı skor
	textLen := len(text)
	if textLen > 3000 {
		score += 15
	} else if textLen > 1500 {
		score += 10
	} else if textLen > 500 {
		score += 5
	} else if textLen < 200 {
		score -= 10
	}

	// 2. Yüksek değerli anahtar kelime kontrolü
	keywordMatches := 0
	for _, keyword := range shared.CTIKeywords {
		if strings.Contains(lowerText, keyword) || strings.Contains(lowerTitle, keyword) {
			keywordMatches++
			score += 5
		}
	}
	if keywordMatches >= 5 {
		score += 15
	} else if keywordMatches >= 3 {
		score += 10
	}

	// 3. Potansiyel veri sızıntısı belirteçleri
	if containsEmailPattern(lowerText) {
		score += 20
	}
	if containsIPAddress(lowerText) {
		score += 10
	}
	if containsHashPattern(lowerText) {
		score += 15
	}
	if containsBase64Pattern(text) {
		score += 10
	}

	// 4. Spam içerik cezası
	spamCount := 0
	for _, spam := range shared.SpamIndicators {
		if strings.Contains(lowerText, spam) {
			spamCount++
			score -= 10
		}
	}
	if spamCount >= 3 {
		score -= 20
	}

	// 5. Unique içerik bonusu
	words := strings.Fields(lowerText)
	if len(words) > 50 {
		uniqueWords := make(map[string]bool)
		for _, w := range words {
			uniqueWords[w] = true
		}
		uniqueRatio := float64(len(uniqueWords)) / float64(len(words))
		if uniqueRatio > 0.4 {
			score += 10
		} else if uniqueRatio < 0.2 {
			score -= 10
		}
	}

	// 6. Market/ürün sayfası cezası
	if strings.Contains(lowerText, "add to cart") ||
		strings.Contains(lowerText, "buy now") ||
		strings.Contains(lowerText, "&#36;") ||
		strings.Contains(lowerText, "quantity:") {
		score -= 15
	}

	if score < 0 {
		score = 0
	}

	return score
}

// containsEmailPattern email pattern kontrolü
func containsEmailPattern(text string) bool {
	emailRegex := regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`)
	matches := emailRegex.FindAllString(text, -1)
	return len(matches) >= 2
}

// containsIPAddress IP adresi kontrolü
func containsIPAddress(text string) bool {
	ipRegex := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	matches := ipRegex.FindAllString(text, -1)
	return len(matches) >= 2
}

// containsHashPattern hash pattern kontrolü
func containsHashPattern(text string) bool {
	hashRegex := regexp.MustCompile(`\b[a-fA-F0-9]{32,64}\b`)
	matches := hashRegex.FindAllString(text, -1)
	return len(matches) >= 2
}

// containsBase64Pattern Base64 içerik kontrolü
func containsBase64Pattern(text string) bool {
	base64Regex := regexp.MustCompile(`[A-Za-z0-9+/]{50,}={0,2}`)
	return base64Regex.MatchString(text)
}

// generateContentHash içerik hash'i oluşturur
func generateContentHash(text string) string {
	normalizedText := strings.ToLower(strings.TrimSpace(text))
	if len(normalizedText) > 1000 {
		normalizedText = normalizedText[:1000]
	}
	hash := md5.Sum([]byte(normalizedText))
	return hex.EncodeToString(hash[:])
}

// isDuplicate duplicate içerik kontrolü
func (s *Scraper) isDuplicate(hash string) bool {
	s.mu.RLock()
	exists := s.seenHashes[hash]
	s.mu.RUnlock()
	return exists
}

// markAsSeen hash'i görülmüş olarak işaretle
func (s *Scraper) markAsSeen(hash string) {
	s.mu.Lock()
	s.seenHashes[hash] = true
	s.mu.Unlock()
}
