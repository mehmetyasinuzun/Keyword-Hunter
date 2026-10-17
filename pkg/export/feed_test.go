package export

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"keywordhunter-mvp/pkg/storage"
)

func TestFindingObjects_FlagsOptionsAndStability(t *testing.T) {
	t0 := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	f := storage.FeedFinding{
		SearchResult: storage.SearchResult{ID: 7, Title: "Leak", URL: "http://a.onion/x", Category: "Veri Sızıntısı", Criticality: 5, CreatedAt: t0},
		FirstURL:     true,
		Artifacts: []storage.FeedArtifact{
			{ResultArtifact: storage.ResultArtifact{Type: "credit_card", Value: "4111111111111111", CreatedAt: t0}, FirstIndicator: true, FirstRelation: true},
			{ResultArtifact: storage.ResultArtifact{Type: "email", Value: "x@proton.me", Confidence: .8, CreatedAt: t0.Add(time.Hour)}, FirstIndicator: true, FirstRelation: true},
			{ResultArtifact: storage.ResultArtifact{Type: "ip", Value: "1.2.3.4", CreatedAt: t0}, FirstIndicator: false, FirstRelation: true},
			{ResultArtifact: storage.ResultArtifact{Type: "username", Value: "bob", CreatedAt: t0}, FirstIndicator: true, FirstRelation: true},
		},
	}
	objs := FindingObjects(f, FeedOptions{FindingIndicators: true, Relationships: true})
	var kinds []string
	for _, o := range objs {
		kinds = append(kinds, o.Type)
		if !o.DateAdded.Equal(t0) {
			t.Fatalf("date_added bulgunun created_at'i olmalı: %v", o.DateAdded)
		}
		b, _ := json.Marshal(o.STIX)
		if strings.Contains(string(b), "4111111111111111") {
			t.Fatal("kart numarası akışa girmemeli")
		}
	}
	// URL göstergesi, e-posta göstergesi + ilişkisi, IP ilişkisi (gösterge daha önce yayımlandı)
	if strings.Join(kinds, ",") != "indicator,indicator,relationship,relationship" {
		t.Fatalf("nesne dizisi: %v", kinds)
	}
	url := objs[0].STIX.(indicator)
	if url.Created != "2026-02-03T04:05:06.000Z" || url.Modified != "2026-02-03T05:05:06.000Z" || objs[0].Version != url.Modified {
		t.Fatalf("URL göstergesi zamanları (modified = son analiz): %s / %s", url.Created, url.Modified)
	}
	if url.CreatedByRef != IdentityID || IdentityObject().ID != IdentityID || IdentityObject().Version != "2024-01-01T00:00:00.000Z" {
		t.Fatal("üretici kimliği sabit olmalı")
	}
	// Kararlılık: aynı girdi → aynı bayt dizisi.
	a, _ := json.Marshal(objs[1].STIX)
	b, _ := json.Marshal(FindingObjects(f, FeedOptions{FindingIndicators: true, Relationships: true})[1].STIX)
	if string(a) != string(b) {
		t.Fatal("nesneler kararlı olmalı")
	}
	// Seçenekler: yalnız IOC göstergeleri
	only := FindingObjects(f, FeedOptions{})
	if len(only) != 1 || only[0].ID != ArtifactIndicatorID("email", "x@proton.me") {
		t.Fatalf("yalnız göstergeler: %+v", only)
	}
	// İlk geçiş değilse URL göstergesi üretilmez.
	f.FirstURL = false
	if objs := FindingObjects(f, FeedOptions{FindingIndicators: true}); len(objs) != 1 || objs[0].Type != "indicator" || objs[0].ID == URLIndicatorID(f.URL) {
		t.Fatalf("FirstURL=false: %+v", objs)
	}
}

// Sonradan çıkarılan IOC ya da yükselen kritiklik feed_at'i ilerletir: tüm
// nesnelerin date_added'ı ve URL göstergesinin sürümü onu izler. Telefon
// numaraları (kişisel veri) kart numaraları gibi akışa girmez.
func TestFindingObjects_DateAddedFollowsFeedAtAndSkipsPhone(t *testing.T) {
	t0 := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	later := t0.Add(48 * time.Hour)
	f := storage.FeedFinding{
		SearchResult: storage.SearchResult{ID: 9, Title: "Leak", URL: "http://b.onion/", Criticality: 4, CreatedAt: t0},
		FeedAt:       later,
		FirstURL:     true,
		Artifacts: []storage.FeedArtifact{
			{ResultArtifact: storage.ResultArtifact{Type: "phone", Value: "+905551112233", CreatedAt: later}, FirstIndicator: true, FirstRelation: true},
			{ResultArtifact: storage.ResultArtifact{Type: "email", Value: "late@x.io", CreatedAt: later}, FirstIndicator: true, FirstRelation: true},
		},
	}
	objs := FindingObjects(f, FeedOptions{FindingIndicators: true, Relationships: true})
	if len(objs) != 3 {
		t.Fatalf("URL göstergesi + e-posta göstergesi + ilişki beklenirdi: %d", len(objs))
	}
	for _, o := range objs {
		if !o.DateAdded.Equal(later) {
			t.Fatalf("date_added feed_at olmalı: %v", o.DateAdded)
		}
		b, _ := json.Marshal(o.STIX)
		if strings.Contains(string(b), "905551112233") {
			t.Fatal("telefon numarası akışa girmemeli")
		}
	}
	if objs[0].Version != Timestamp(later) || objs[0].STIX.(indicator).Created != Timestamp(t0) {
		t.Fatalf("URL göstergesi: created = created_at, modified = feed_at: %+v", objs[0].STIX)
	}
	if !FeedSkipsType("phone") || !FeedSkipsType("credit_card") || FeedSkipsType("email") {
		t.Fatal("FeedSkipsType")
	}
	// feed_at boş/eski ise created_at kullanılır.
	f.FeedAt = time.Time{}
	if o := FindingObjects(f, FeedOptions{FindingIndicators: true}); !o[0].DateAdded.Equal(t0) {
		t.Fatalf("feed_at yoksa created_at: %v", o[0].DateAdded)
	}
}
