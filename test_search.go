package main

import (
	"fmt"
	"log"

	"keywordhunter-mvp/pkg/search"
)

func main() {
	fmt.Println("=== KeywordHunter Test - Dark Web Arama Testi ===")
	fmt.Println("Tor Proxy: 127.0.0.1:9150")
	fmt.Println()

	// Searcher oluştur
	fmt.Println("Searcher başlatılıyor...")
	searcher, err := search.New("127.0.0.1:9150")
	if err != nil {
		log.Fatal("❌ Searcher oluşturulamadı:", err)
	}
	fmt.Println("✅ Searcher hazır")
	fmt.Println()

	// Test sorgusu
	query := "bitcoin"
	fmt.Printf("🔍 Test aranıyor: '%s'\n", query)
	fmt.Println("Lütfen bekleyin... (Bu işlem 30-60 saniye sürebilir)")
	fmt.Println()

	// Arama yap
	results := searcher.SearchAll(query)

	// Sonuçları göster
	fmt.Println("=== ARAMA SONUÇLARI ===")
	fmt.Printf("Toplam Sonuç: %d\n", len(results))
	fmt.Println()

	if len(results) == 0 {
		fmt.Println("❌ SORUN: Hiç sonuç bulunamadı!")
		fmt.Println("Olası nedenler:")
		fmt.Println("1. Tor Browser kapalı (127.0.0.1:9150 açık olmalı)")
		fmt.Println("2. Tor bağlantısı çalışmıyor")
		fmt.Println("3. Dark web arama motorları erişilemiyor")
		return
	}

	fmt.Println("✅ BAŞARILI: Sonuçlar bulundu!")
	fmt.Println()
	fmt.Println("İlk 5 sonuç:")
	for i, r := range results {
		if i >= 5 {
			break
		}
		fmt.Printf("%d. [%s] %s\n", i+1, r.Source, r.Title)
		fmt.Printf("   URL: %s\n", r.URL)
		fmt.Println()
	}
}
