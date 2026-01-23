// Package tests - JSON Serialization ve API testleri
package tests

import (
	"encoding/json"
	"strings"
	"testing"

	storage "keywordhunter-mvp/pkg/storage"
)

// JSON serialization doğrulama testi
func TestJSONSerialization(t *testing.T) {
	// QueryInfo struct'ının doğru serialize edildiğini kontrol et
	qi := storage.QueryInfo{
		Query: "test query",
		Count: 42,
	}

	data, err := json.Marshal(qi)
	if err != nil {
		t.Fatalf("JSON marshal hatası: %v", err)
	}

	jsonStr := string(data)

	// JSON'da "query" ve "count" küçük harfle olmalı
	if !strings.Contains(jsonStr, `"query"`) {
		t.Errorf("JSON'da 'query' field'ı yok: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"count"`) {
		t.Errorf("JSON'da 'count' field'ı yok: %s", jsonStr)
	}

	// Büyük harfle olmamalı
	if strings.Contains(jsonStr, `"Query"`) {
		t.Errorf("JSON'da büyük harfli 'Query' var: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"Count"`) {
		t.Errorf("JSON'da büyük harfli 'Count' var: %s", jsonStr)
	}

	t.Logf("Serialized JSON: %s", jsonStr)

	// Deserialize test
	var qi2 storage.QueryInfo
	if err := json.Unmarshal(data, &qi2); err != nil {
		t.Fatalf("JSON unmarshal hatası: %v", err)
	}

	if qi2.Query != "test query" {
		t.Errorf("Deserialize sonrası Query hatalı: %s", qi2.Query)
	}
	if qi2.Count != 42 {
		t.Errorf("Deserialize sonrası Count hatalı: %d", qi2.Count)
	}
}

// GraphNode serialization testi
func TestGraphNodeSerialization(t *testing.T) {
	gn := storage.GraphNode{
		Name:  "test node",
		URL:   "http://test.onion",
		Type:  "result",
		Count: 5,
	}

	data, err := json.Marshal(gn)
	if err != nil {
		t.Fatalf("JSON marshal hatası: %v", err)
	}

	jsonStr := string(data)
	t.Logf("GraphNode JSON: %s", jsonStr)

	// JSON'da field'lar küçük harfle olmalı
	if !strings.Contains(jsonStr, `"name"`) {
		t.Errorf("JSON'da 'name' field'ı yok: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"url"`) {
		t.Errorf("JSON'da 'url' field'ı yok: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"type"`) {
		t.Errorf("JSON'da 'type' field'ı yok: %s", jsonStr)
	}
}

// Content struct serialization testi
func TestContentSerialization(t *testing.T) {
	c := storage.Content{
		ID:          1,
		URL:         "http://test.onion",
		Title:       "Test Title",
		RawContent:  "Test content",
		ContentSize: 100,
	}

	data, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("JSON marshal hatası: %v", err)
	}

	t.Logf("Content JSON: %s", string(data))
}

// Frontend için API response format testi
func TestAPIResponseFormat(t *testing.T) {
	// Frontend'in beklediği format: [{query: "...", count: ...}]
	queries := []storage.QueryInfo{
		{Query: "ransomware", Count: 10},
		{Query: "bitcoin", Count: 5},
	}

	data, err := json.Marshal(queries)
	if err != nil {
		t.Fatalf("JSON marshal hatası: %v", err)
	}

	jsonStr := string(data)
	t.Logf("Queries API response: %s", jsonStr)

	// Frontend'in beklediği format kontrolü
	// [{"query":"ransomware","count":10},{"query":"bitcoin","count":5}]
	if !strings.Contains(jsonStr, `"query":"ransomware"`) {
		t.Errorf("Beklenen format bulunamadı: %s", jsonStr)
	}
	if !strings.Contains(jsonStr, `"count":10`) {
		t.Errorf("Beklenen count format bulunamadı: %s", jsonStr)
	}

	// Deserialize test - Frontend'in yapacağı gibi
	var parsed []map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("JSON parse hatası: %v", err)
	}

	for _, q := range parsed {
		query, ok := q["query"].(string)
		if !ok || query == "" {
			t.Errorf("query field'ı string değil veya boş: %v", q)
		}
		count, ok := q["count"].(float64) // JSON numbers are float64
		if !ok {
			t.Errorf("count field'ı number değil: %v", q)
		}
		t.Logf("Parsed: query=%s, count=%.0f", query, count)
	}
}
