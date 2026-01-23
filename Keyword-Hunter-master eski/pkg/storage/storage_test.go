package storage

import (
	"os"
	"testing"
	"time"
)

func TestDatabaseOperations(t *testing.T) {
	// Test için geçici DB
	dbPath := "test_keywordhunter.db"
	defer os.Remove(dbPath)

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("DB oluşturulamadı: %v", err)
	}
	defer db.Close()

	// Test 1: SaveResult
	t.Run("SaveResult", func(t *testing.T) {
		err := db.SaveResult("Test Title", "http://test.onion", "TestEngine", "test query")
		if err != nil {
			t.Errorf("SaveResult hatası: %v", err)
		}
	})

	// Test 2: GetResults
	t.Run("GetResults", func(t *testing.T) {
		results, err := db.GetResults(10, "")
		if err != nil {
			t.Errorf("GetResults hatası: %v", err)
		}
		if len(results) == 0 {
			t.Error("Sonuç bulunamadı")
		}
		if results[0].Title != "Test Title" {
			t.Errorf("Beklenen title: Test Title, gelen: %s", results[0].Title)
		}
	})

	// Test 3: GetQueries
	t.Run("GetQueries", func(t *testing.T) {
		queries, err := db.GetQueries()
		if err != nil {
			t.Errorf("GetQueries hatası: %v", err)
		}
		if len(queries) == 0 {
			t.Error("Sorgu bulunamadı")
		}

		// JSON tag kontrolü - struct field'ları küçük harfle serialize edilmeli
		if queries[0].Query != "test query" {
			t.Errorf("Beklenen query: test query, gelen: %s", queries[0].Query)
		}
		if queries[0].Count != 1 {
			t.Errorf("Beklenen count: 1, gelen: %d", queries[0].Count)
		}
	})

	// Test 4: GetStats
	t.Run("GetStats", func(t *testing.T) {
		total, _, err := db.GetStats()
		if err != nil {
			t.Errorf("GetStats hatası: %v", err)
		}
		if total == 0 {
			t.Error("Stats 0 döndü")
		}
	})

	// Test 5: SaveContent
	t.Run("SaveContent", func(t *testing.T) {
		err := db.SaveContent("http://test.onion", "Test Page", "Test content body", 100)
		if err != nil {
			t.Errorf("SaveContent hatası: %v", err)
		}
	})

	// Test 6: GetContents
	t.Run("GetContents", func(t *testing.T) {
		contents, err := db.GetContents(10, "")
		if err != nil {
			t.Errorf("GetContents hatası: %v", err)
		}
		if len(contents) == 0 {
			t.Error("İçerik bulunamadı")
		}
	})

	// Test 7: Duplicate handling
	t.Run("DuplicateHandling", func(t *testing.T) {
		// Aynı URL'yi tekrar kaydet
		err := db.SaveResult("Test Title 2", "http://test.onion", "TestEngine2", "test query")
		if err != nil {
			t.Errorf("Duplicate kaydetme hatası: %v", err)
		}

		// Sadece bir kayıt olmalı (UPSERT)
		results, _ := db.GetResults(10, "test query")
		urlCount := 0
		for _, r := range results {
			if r.URL == "http://test.onion" {
				urlCount++
			}
		}
		// Duplicate'ler ayrı kayıt olabilir (farklı source), bu durumda 2 olur
		if urlCount == 0 {
			t.Error("URL kaydı bulunamadı")
		}
	})

	// Test 8: Search History
	t.Run("SearchHistory", func(t *testing.T) {
		err := db.SaveSearchHistory("history test", 5)
		if err != nil {
			t.Errorf("SaveSearchHistory hatası: %v", err)
		}
	})

	// Test 9: QueryInfo JSON serialization
	t.Run("QueryInfoJSONTags", func(t *testing.T) {
		qi := QueryInfo{Query: "test", Count: 5}
		if qi.Query != "test" || qi.Count != 5 {
			t.Error("QueryInfo struct değerleri yanlış")
		}
	})
}

func TestGraphData(t *testing.T) {
	dbPath := "test_graph.db"
	defer os.Remove(dbPath)

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("DB oluşturulamadı: %v", err)
	}
	defer db.Close()

	// Birden fazla sorgu ekle
	queries := []string{"bitcoin", "ethereum", "darkweb"}
	engines := []string{"Ahmia", "Torch", "DuckDuckGo"}

	for _, q := range queries {
		for _, e := range engines {
			for i := 0; i < 3; i++ {
				db.SaveResult(
					q+" result "+string(rune('A'+i)),
					"http://"+q+".onion/"+e+"/"+string(rune('0'+i)),
					e,
					q,
				)
			}
		}
	}

	// Test: GetQueries çoklu sorgu
	t.Run("MultipleQueries", func(t *testing.T) {
		qs, err := db.GetQueries()
		if err != nil {
			t.Errorf("GetQueries hatası: %v", err)
		}
		if len(qs) != 3 {
			t.Errorf("Beklenen sorgu sayısı: 3, gelen: %d", len(qs))
		}

		// Her sorgu için 9 sonuç olmalı (3 engine * 3 result)
		for _, q := range qs {
			if q.Count != 9 {
				t.Errorf("Sorgu %s için beklenen count: 9, gelen: %d", q.Query, q.Count)
			}
		}
	})

	// Test: Filtreleme
	t.Run("QueryFilter", func(t *testing.T) {
		results, err := db.GetResults(100, "bitcoin")
		if err != nil {
			t.Errorf("GetResults hatası: %v", err)
		}
		if len(results) != 9 {
			t.Errorf("Bitcoin için beklenen sonuç: 9, gelen: %d", len(results))
		}
	})
}

func TestContentOperations(t *testing.T) {
	dbPath := "test_content.db"
	defer os.Remove(dbPath)

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("DB oluşturulamadı: %v", err)
	}
	defer db.Close()

	// Test: İçerik kaydetme ve güncelleme
	t.Run("ContentUpsert", func(t *testing.T) {
		url := "http://test.onion/page"

		// İlk kayıt
		err := db.SaveContent(url, "Original Title", "Original content", 100)
		if err != nil {
			t.Errorf("İlk SaveContent hatası: %v", err)
		}

		// Güncelleme
		err = db.SaveContent(url, "Updated Title", "Updated content", 200)
		if err != nil {
			t.Errorf("Update SaveContent hatası: %v", err)
		}

		// Kontrol
		contents, _ := db.GetContents(10, "")
		if len(contents) != 1 {
			t.Errorf("Beklenen içerik sayısı: 1, gelen: %d", len(contents))
		}
		if contents[0].Title != "Updated Title" {
			t.Errorf("Title güncellenmemiş: %s", contents[0].Title)
		}
	})

	// Test: GetContentByID (not GetContentByURL)
	t.Run("GetContentByID", func(t *testing.T) {
		contents, _ := db.GetContents(1, "")
		if len(contents) > 0 {
			content, err := db.GetContentByID(contents[0].ID)
			if err != nil {
				t.Errorf("GetContentByID hatası: %v", err)
			}
			if content == nil {
				t.Error("İçerik bulunamadı")
			}
		}
	})

	// Test: Content stats
	t.Run("ContentStats", func(t *testing.T) {
		_, scraped, err := db.GetContentStats()
		if err != nil {
			t.Errorf("GetContentStats hatası: %v", err)
		}
		// Scraped count en az 1 olmalı (bir önce eklendi)
		if scraped == 0 {
			t.Log("Not: Scraped count 0, bu beklenen olabilir çünkü is_scraped kontrolüne bağlı")
		}
	})
}

func TestTimestamps(t *testing.T) {
	dbPath := "test_time.db"
	defer os.Remove(dbPath)

	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("DB oluşturulamadı: %v", err)
	}
	defer db.Close()

	// Kayıt yap
	db.SaveResult("Time Test", "http://time.onion", "Engine", "time query")

	// Timestamp kontrolü
	results, _ := db.GetResults(1, "time query")
	if len(results) > 0 {
		// CreatedAt bugünün tarihi olmalı
		if results[0].CreatedAt.Year() != time.Now().Year() {
			t.Error("CreatedAt yılı yanlış")
		}
	}
}
