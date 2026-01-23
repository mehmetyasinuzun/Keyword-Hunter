package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"keywordhunter-mvp/pkg/storage"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	// Test DB
	dbPath := "test_web.db"
	db, err := storage.New(dbPath)
	if err != nil {
		t.Fatalf("Test DB oluşturulamadı: %v", err)
	}

	// Test verileri ekle
	db.SaveResult("Test Result 1", "http://test1.onion", "Ahmia", "test query")
	db.SaveResult("Test Result 2", "http://test2.onion", "Torch", "test query")
	db.SaveResult("Bitcoin Market", "http://btc.onion", "Ahmia", "bitcoin")
	db.SaveContent("http://test1.onion", "Test Page", "Test content", 100)

	server := New(Config{
		DB:       db,
		Username: "admin",
		Password: "test123",
	})

	cleanup := func() {
		db.Close()
		os.Remove(dbPath)
	}

	return server, cleanup
}

func TestQueriesAPI(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Login yaparak session al
	loginReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=test123"))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	server.router.ServeHTTP(loginRec, loginReq)

	// Cookie'yi al
	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Skip("Session cookie alınamadı - login test'i geçildi")
		return
	}

	// API isteği
	req := httptest.NewRequest("GET", "/api/queries", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	server.router.ServeHTTP(rec, req)

	// Yanıt kontrolü
	if rec.Code != http.StatusOK {
		t.Errorf("Beklenen status: 200, gelen: %d", rec.Code)
	}

	// JSON parse
	var response map[string]interface{}
	body, _ := io.ReadAll(rec.Body)
	if err := json.Unmarshal(body, &response); err != nil {
		t.Errorf("JSON parse hatası: %v", err)
	}

	// queries field kontrolü
	queries, ok := response["queries"].([]interface{})
	if !ok {
		t.Error("queries field bulunamadı veya yanlış tipte")
		return
	}

	if len(queries) == 0 {
		t.Error("Sorgu listesi boş")
		return
	}

	// Her sorgu için query ve count field'ları olmalı
	for i, q := range queries {
		qMap, ok := q.(map[string]interface{})
		if !ok {
			t.Errorf("Sorgu %d map değil", i)
			continue
		}

		// query field (küçük harf - JSON tag)
		if _, ok := qMap["query"]; !ok {
			t.Errorf("Sorgu %d: 'query' field eksik (JSON tag çalışmıyor olabilir)", i)
		}

		// count field (küçük harf - JSON tag)
		if _, ok := qMap["count"]; !ok {
			t.Errorf("Sorgu %d: 'count' field eksik (JSON tag çalışmıyor olabilir)", i)
		}

		// Query ve Count büyük harfle gelmemeli
		if _, ok := qMap["Query"]; ok {
			t.Errorf("Sorgu %d: 'Query' (büyük Q) field var - JSON tag çalışmıyor!", i)
		}
		if _, ok := qMap["Count"]; ok {
			t.Errorf("Sorgu %d: 'Count' (büyük C) field var - JSON tag çalışmıyor!", i)
		}
	}
}

func TestGraphAPI(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Login
	loginReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=test123"))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	server.router.ServeHTTP(loginRec, loginReq)

	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Skip("Session cookie alınamadı")
		return
	}

	// Test cases
	testCases := []struct {
		name       string
		url        string
		expectCode int
	}{
		{"AllQueries", "/api/graph", http.StatusOK},
		{"FilteredQuery", "/api/graph?q=test+query", http.StatusOK},
		{"NonExistentQuery", "/api/graph?q=nonexistent", http.StatusOK},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.url, nil)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			rec := httptest.NewRecorder()
			server.router.ServeHTTP(rec, req)

			if rec.Code != tc.expectCode {
				t.Errorf("Beklenen status: %d, gelen: %d", tc.expectCode, rec.Code)
			}
		})
	}
}

func TestStatsAPI(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Login
	loginReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=test123"))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	server.router.ServeHTTP(loginRec, loginReq)

	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Skip("Session cookie alınamadı")
		return
	}

	req := httptest.NewRequest("GET", "/api/stats", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	server.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Beklenen status: 200, gelen: %d", rec.Code)
	}

	var response map[string]interface{}
	body, _ := io.ReadAll(rec.Body)
	json.Unmarshal(body, &response)

	if _, ok := response["totalResults"]; !ok {
		t.Error("totalResults field eksik")
	}
}

func TestAuthMiddleware(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Korumalı route'a session olmadan erişim
	protectedRoutes := []string{
		"/dashboard",
		"/search",
		"/results",
		"/results/graph",
		"/api/queries",
		"/api/graph",
	}

	for _, route := range protectedRoutes {
		t.Run("Unauthorized_"+route, func(t *testing.T) {
			req := httptest.NewRequest("GET", route, nil)
			rec := httptest.NewRecorder()
			server.router.ServeHTTP(rec, req)

			// 302 redirect to login bekleniyor
			if rec.Code != http.StatusFound {
				t.Errorf("Route %s: Beklenen status: 302, gelen: %d", route, rec.Code)
			}
		})
	}
}

func TestLoginFlow(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Test: Geçersiz credentials
	t.Run("InvalidLogin", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("username=wrong&password=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		server.router.ServeHTTP(rec, req)

		// Hatalı girişte redirect (302) veya login sayfası (200) dönebilir
		if rec.Code != http.StatusOK && rec.Code != http.StatusFound {
			t.Errorf("Beklenen status: 200 veya 302, gelen: %d", rec.Code)
		}
	})

	// Test: Geçerli credentials
	t.Run("ValidLogin", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=test123"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		server.router.ServeHTTP(rec, req)

		// Başarılı girişte redirect (302)
		if rec.Code != http.StatusFound {
			t.Errorf("Beklenen status: 302, gelen: %d", rec.Code)
		}

		// Session cookie olmalı
		cookies := rec.Result().Cookies()
		found := false
		for _, c := range cookies {
			if c.Name == "session" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Session cookie bulunamadı")
		}
	})
}

func TestStaticFiles(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Static dosya route'ları test et
	staticFiles := []string{
		"/static/css/graph.css",
		"/static/js/graph.js",
	}

	for _, file := range staticFiles {
		t.Run("Static_"+file, func(t *testing.T) {
			req := httptest.NewRequest("GET", file, nil)
			rec := httptest.NewRecorder()
			server.router.ServeHTTP(rec, req)

			// 200 veya 404 olabilir (dosya varsa 200)
			if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
				t.Errorf("Route %s: Beklenmeyen status: %d", file, rec.Code)
			}
		})
	}
}

func TestSearchEndpoint(t *testing.T) {
	server, cleanup := setupTestServer(t)
	defer cleanup()

	// Login
	loginReq := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&password=test123"))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	server.router.ServeHTTP(loginRec, loginReq)

	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		t.Skip("Session cookie alınamadı")
		return
	}

	// Search page GET
	t.Run("SearchPageGET", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/search", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		rec := httptest.NewRecorder()
		server.router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Beklenen status: 200, gelen: %d", rec.Code)
		}
	})
}
