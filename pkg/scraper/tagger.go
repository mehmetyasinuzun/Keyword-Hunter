package scraper

import (
	"regexp"
	"sort"
	"strings"

	"keywordhunter-mvp/pkg/shared"
)

// TagResult otomatik etiket sonucu
type TagResult struct {
	Tags        []string // Bulunan etiketler (en önemli 5)
	KeywordHits int      // Aranan kelime kaç kez bulundu
	Success     bool
	Error       string
}

// stopWords - İngilizce ve genel durdurma kelimeleri (filtrelenecek)
var stopWords = map[string]bool{
	// İngilizce yaygın kelimeler
	"the": true, "be": true, "to": true, "of": true, "and": true, "a": true,
	"in": true, "that": true, "have": true, "i": true, "it": true, "for": true,
	"not": true, "on": true, "with": true, "he": true, "as": true, "you": true,
	"do": true, "at": true, "this": true, "but": true, "his": true, "by": true,
	"from": true, "they": true, "we": true, "say": true, "her": true, "she": true,
	"or": true, "an": true, "will": true, "my": true, "one": true, "all": true,
	"would": true, "there": true, "their": true, "what": true, "so": true, "up": true,
	"out": true, "if": true, "about": true, "who": true, "get": true, "which": true,
	"go": true, "me": true, "when": true, "make": true, "can": true, "like": true,
	"time": true, "no": true, "just": true, "him": true, "know": true, "take": true,
	"people": true, "into": true, "year": true, "your": true, "good": true, "some": true,
	"them": true, "see": true, "other": true, "than": true, "then": true, "now": true,
	"look": true, "only": true, "come": true, "its": true, "over": true, "think": true,
	"also": true, "back": true, "after": true, "use": true, "two": true, "how": true,
	"our": true, "work": true, "first": true, "well": true, "way": true, "even": true,
	"new": true, "want": true, "because": true, "any": true, "these": true, "give": true,
	"day": true, "most": true, "us": true, "is": true, "are": true, "was": true,
	"were": true, "been": true, "has": true, "had": true, "does": true, "did": true,
	"could": true, "should": true, "may": true, "might": true, "must": true,
	// Genel web kelimeleri (düşük değerli)
	"home": true, "contact": true, "page": true, "click": true, "here": true,
	"read": true, "more": true, "view": true, "next": true, "previous": true,
	"menu": true, "search": true, "login": true, "register": true, "sign": true,
	"copyright": true, "privacy": true, "terms": true, "policy": true,
	"skip": true, "content": true, "main": true, "loading": true,
}

// ctiRelevantWords - CTI için önemli kelimelerin ağırlığı (önceliklendirilecek)
var ctiRelevantWords = map[string]int{
	// Veri Sızıntısı
	"leak": 10, "leaked": 10, "breach": 10, "dump": 10, "exposed": 10,
	"database": 9, "sql": 9, "mysql": 9, "mongodb": 9, "postgresql": 8,
	"credentials": 9, "password": 9, "passwords": 9, "hash": 8, "hashed": 8,
	"fullz": 10, "ssn": 9, "dob": 8,

	// Carding / Finansal
	"carding": 10, "cvv": 10, "cc": 9, "visa": 8, "mastercard": 8,
	"bank": 9, "paypal": 9, "cashout": 9, "money": 7, "bitcoin": 8, "btc": 8,
	"crypto": 8, "wallet": 8, "transfer": 7,

	// Ransomware / Malware
	"ransomware": 10, "malware": 10, "trojan": 9, "virus": 8, "botnet": 9,
	"rat": 9, "keylogger": 9, "stealer": 9, "crypter": 9, "fud": 9,
	"lockbit": 10, "blackcat": 10, "conti": 10, "hive": 9, "revil": 9,

	// Hacking / Exploit
	"exploit": 10, "0day": 10, "zeroday": 10, "vulnerability": 9, "cve": 9,
	"rce": 10, "lfi": 9, "sqli": 9, "xss": 8, "injection": 8,
	"shell": 8, "backdoor": 9, "rootkit": 9, "privilege": 8, "escalation": 8,

	// Dark Web Marketplace
	"market": 8, "marketplace": 8, "vendor": 8, "escrow": 8, "listings": 7,
	"shop": 7, "store": 7, "buy": 6, "sell": 6, "price": 6,

	// Forum / Community
	"forum": 7, "board": 6, "community": 6, "member": 5, "thread": 5,
	"post": 5, "hacker": 8, "hacking": 8, "cracker": 7, "cracking": 7,

	// Kimlikler / PII
	"email": 7, "emails": 7, "phone": 6, "address": 6, "identity": 8,
	"passport": 8, "driver": 7, "license": 7, "document": 6, "documents": 6,

	// Diğer CTI terimleri
	"admin": 8, "root": 8, "access": 7, "account": 7, "accounts": 7,
	"login": 7, "smtp": 8, "rdp": 8, "vpn": 7, "ssh": 8, "ftp": 7,
	"combo": 8, "combolist": 9, "list": 5, "fresh": 7, "verified": 7,
	"private": 8, "premium": 6, "vip": 7, "exclusive": 7,
	"tutorial": 5, "method": 6, "tool": 6, "tools": 6, "software": 5,
	"crack": 8, "cracked": 8, "keygen": 7, "serial": 6,
}

// ExtractTopKeywords bir URL'deki içerikten en önemli kelimeleri çıkarır
func (s *Scraper) ExtractTopKeywords(urlStr, searchQuery string, maxTags int) TagResult {
	result := TagResult{
		Tags:    []string{},
		Success: false,
	}

	if maxTags <= 0 {
		maxTags = 5
	}

	// Sayfayı çek
	content := s.scrapeURLForExpand(urlStr)
	if !content.Success {
		result.Error = content.Error
		return result
	}

	// HTML'i temiz metne çevir
	text := s.htmlToText(content.RawContent)
	textLower := strings.ToLower(text)

	// Aranan kelime kaç kez geçiyor?
	result.KeywordHits = strings.Count(textLower, strings.ToLower(searchQuery))

	// Kelimeleri say
	wordCounts := make(map[string]int)

	// Sadece harflerden oluşan kelimeleri al
	wordRegex := regexp.MustCompile(`[a-zA-Z]+`)
	words := wordRegex.FindAllString(textLower, -1)

	for _, word := range words {
		// Çok kısa veya çok uzun kelimeleri atla
		if len(word) < 3 || len(word) > 20 {
			continue
		}

		// Stop words'leri atla
		if stopWords[word] {
			continue
		}

		wordCounts[word]++
	}

	// Kelimeleri skorla ve sırala
	type wordScore struct {
		word  string
		score float64
	}

	var scoredWords []wordScore

	for word, count := range wordCounts {
		// Temel skor: kelime sayısı
		score := float64(count)

		// CTI için önemli kelimeler bonus alır
		if bonus, ok := ctiRelevantWords[word]; ok {
			score *= float64(bonus)
		}

		// Çok nadir kelimeler (1-2 kez geçen) düşürülür
		if count < 3 {
			score *= 0.5
		}

		// Arama kelimesiyle aynı kelimeyi listeye ekleme
		if word == strings.ToLower(searchQuery) {
			continue
		}

		scoredWords = append(scoredWords, wordScore{word: word, score: score})
	}

	// CTI anahtar kelimelerinden içerikte geçenleri ekle (yoksa bile)
	for _, keyword := range shared.HighValueKeywords {
		keywordLower := strings.ToLower(keyword)
		if strings.Contains(textLower, keywordLower) {
			// Daha önce eklenmemişse ekle
			found := false
			for _, sw := range scoredWords {
				if sw.word == keywordLower {
					found = true
					break
				}
			}
			if !found {
				scoredWords = append(scoredWords, wordScore{word: keywordLower, score: 100})
			}
		}
	}

	// Skora göre sırala (büyükten küçüğe)
	sort.Slice(scoredWords, func(i, j int) bool {
		return scoredWords[i].score > scoredWords[j].score
	})

	// En yüksek skorlu kelimeleri al
	for i := 0; i < len(scoredWords) && len(result.Tags) < maxTags; i++ {
		// Aynı kelime root'undan birden fazla eklememek için kontrol
		word := scoredWords[i].word
		duplicate := false
		for _, existing := range result.Tags {
			// Basit stem kontrolü (passwords/password gibi)
			if strings.HasPrefix(word, existing) || strings.HasPrefix(existing, word) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.Tags = append(result.Tags, word)
		}
	}

	result.Success = true
	return result
}

// ExtractTopKeywordsFromHTML HTML içeriğinden doğrudan etiket çıkarır (preloaded content için)
func (s *Scraper) ExtractTopKeywordsFromHTML(htmlContent, searchQuery string, maxTags int) TagResult {
	result := TagResult{
		Tags:    []string{},
		Success: false,
	}

	if maxTags <= 0 {
		maxTags = 5
	}

	// HTML'i temiz metne çevir
	text := s.htmlToText(htmlContent)
	textLower := strings.ToLower(text)

	if len(text) < 50 {
		result.Error = "İçerik çok kısa"
		return result
	}

	// Aranan kelime kaç kez geçiyor?
	result.KeywordHits = strings.Count(textLower, strings.ToLower(searchQuery))

	// Kelimeleri say
	wordCounts := make(map[string]int)
	wordRegex := regexp.MustCompile(`[a-zA-Z]+`)
	words := wordRegex.FindAllString(textLower, -1)

	for _, word := range words {
		if len(word) < 3 || len(word) > 20 {
			continue
		}
		if stopWords[word] {
			continue
		}
		wordCounts[word]++
	}

	// Skorlama
	type wordScore struct {
		word  string
		score float64
	}
	var scoredWords []wordScore

	for word, count := range wordCounts {
		score := float64(count)
		if bonus, ok := ctiRelevantWords[word]; ok {
			score *= float64(bonus)
		}
		if count < 3 {
			score *= 0.5
		}
		if word == strings.ToLower(searchQuery) {
			continue
		}
		scoredWords = append(scoredWords, wordScore{word: word, score: score})
	}

	// CTI anahtar kelimeleri ekle
	for _, keyword := range shared.HighValueKeywords {
		keywordLower := strings.ToLower(keyword)
		if strings.Contains(textLower, keywordLower) {
			found := false
			for _, sw := range scoredWords {
				if sw.word == keywordLower {
					found = true
					break
				}
			}
			if !found {
				scoredWords = append(scoredWords, wordScore{word: keywordLower, score: 100})
			}
		}
	}

	// Sırala
	sort.Slice(scoredWords, func(i, j int) bool {
		return scoredWords[i].score > scoredWords[j].score
	})

	// En iyileri al
	for i := 0; i < len(scoredWords) && len(result.Tags) < maxTags; i++ {
		word := scoredWords[i].word
		duplicate := false
		for _, existing := range result.Tags {
			if strings.HasPrefix(word, existing) || strings.HasPrefix(existing, word) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.Tags = append(result.Tags, word)
		}
	}

	result.Success = true
	return result
}
