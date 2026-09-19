package export

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/storage"
)

type fakeArts map[int64][]storage.ResultArtifact

func (f fakeArts) GetArtifacts(id int64) ([]storage.ResultArtifact, error) { return f[id], nil }

func TestBuildBundle_ValidStixShape(t *testing.T) {
	results := []storage.SearchResult{
		{ID: 1, Title: "Fresh DB dump", URL: "http://aaaa.onion/x?a='b'", Source: "Tordex", Query: "leak", Criticality: 5, Category: "Veri Sızıntısı", AutoTags: "leak, dump", CreatedAt: time.Now()},
		{ID: 2, Title: "Dup", URL: "http://aaaa.onion/x?a='b'", Source: "Tor66", Query: "leak", Criticality: 3},
	}
	arts := fakeArts{1: {
		{Type: "email", Value: "x@proton.me", Confidence: .8},
		{Type: "hash", Value: strings.Repeat("a", 64), Confidence: .6},
		{Type: "bitcoin", Value: "1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", Confidence: .7},
		{Type: "username", Value: "admin", Confidence: .5}, // desteklenmez → atlanır
	}}
	b := BuildBundle(results, arts, true)

	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]interface{}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back["type"] != "bundle" || !strings.HasPrefix(back["id"].(string), "bundle--") {
		t.Fatal("bundle kökü hatalı")
	}
	objs := back["objects"].([]interface{})
	// identity + 1 url indicator (dedup) + 3 artifact indicators + 3 relationships = 8
	if len(objs) != 8 {
		t.Fatalf("obje sayısı = %d, want 8", len(objs))
	}
	types := map[string]int{}
	for _, o := range objs {
		m := o.(map[string]interface{})
		types[m["type"].(string)]++
		if m["spec_version"] != "2.1" {
			t.Fatalf("spec_version eksik: %v", m)
		}
		if p, ok := m["pattern"].(string); ok && strings.Contains(p, "url:value") && !strings.Contains(p, `\'b\'`) {
			t.Fatalf("pattern kaçışı hatalı: %s", p)
		}
	}
	if types["indicator"] != 4 || types["relationship"] != 3 || types["identity"] != 1 {
		t.Fatalf("tür dağılımı: %v", types)
	}
	// Determinizm: aynı girdi aynı indicator ID'lerini üretmeli
	b2 := BuildBundle(results, arts, true)
	id1 := b.Objects[1].(indicator).ID
	id2 := b2.Objects[1].(indicator).ID
	if id1 != id2 {
		t.Fatal("indicator ID deterministik olmalı")
	}
	if BuildBundle(results, arts, false).Objects[1].(indicator).Confidence != 90 {
		t.Fatal("confidence eşlemesi")
	}
}
