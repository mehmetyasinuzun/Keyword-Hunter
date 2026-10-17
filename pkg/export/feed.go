package export

import (
	"time"

	"keywordhunter-mvp/pkg/storage"
)

// ─────────────────────────── TAXII akış nesneleri ───────────────────────────
//
// TAXII sunucusu koleksiyonlarını BuildBundle ile aynı STIX nesnelerinden
// (aynı deterministik kimlikler, aynı desenler) kurar; fark, zaman
// damgalarının istek anına değil veriye bağlı olmasıdır: aynı veri her istekte
// birebir aynı nesneyi (aynı id + modified = aynı sürüm) üretir.

// Object TAXII koleksiyonunda sunulan tek bir STIX nesnesi ve manifest bilgisi.
type Object struct {
	ID        string
	Type      string
	Version   string    // STIX "modified" (TAXII sürüm kimliği)
	DateAdded time.Time // koleksiyona eklenme anı (= bulgunun feed_at'i)
	STIX      any       // JSON'a serileşen STIX nesnesi
}

// FeedOptions bir koleksiyonun hangi nesne türlerini içerdiği.
type FeedOptions struct {
	FindingIndicators bool // bulgu URL göstergeleri
	Relationships     bool // IOC → bulgu "related-to" ilişkileri
}

// feedSkipTypes TAXII ile paylaşılmayan IOC türleri: kart numarası PCI
// kapsamında, telefon numarası da gerçek kişilere ait olabilecek kişisel
// veridir; dark web metninden doğrulanmadan çıkarılmış bu değerleri üçüncü
// taraf platformlara yaymak risktir (MISP gönderimi de ikisini atlar). STIX
// dosya dışa aktarımı analistin bilinçli eylemidir ve onları içermeye devam eder.
var feedSkipTypes = map[string]bool{"credit_card": true, "phone": true}

// FeedSkipsType IOC türünün TAXII akışında paylaşılıp paylaşılmadığı.
func FeedSkipsType(t string) bool { return feedSkipTypes[t] }

// IdentityObject koleksiyonların başındaki sabit üretici kimliği.
func IdentityObject() Object {
	id := producerIdentity()
	return Object{ID: id.ID, Type: id.Type, Version: id.Modified, DateAdded: identityEpoch, STIX: id}
}

// IdentityDateAdded üretici kimliğinin koleksiyona eklenme anı.
func IdentityDateAdded() time.Time { return identityEpoch }

// FindingObjects bir akış bulgusunun koleksiyona kattığı nesneleri kararlı
// sırayla döndürür: [URL göstergesi] ve her IOC için [gösterge, ilişki].
// Bir nesne yalnız koleksiyonda ilk geçtiği bulguda üretilir (bkz.
// storage.FeedFindings).
func FindingObjects(f storage.FeedFinding, o FeedOptions) []Object {
	created := f.CreatedAt
	// date_added = feed_at: bulgu eklenince created_at, sonradan yeni IOC
	// çıkarılınca ya da kritiklik yükselince o an (bkz. storage.FeedFindings).
	added := f.FeedAt
	if added.Before(created) {
		added = created
	}
	// Son analiz anı (IOC'ler her etiketlemede yeniden yazılır; kritiklik
	// yükselince feed_at ilerler): kritiklik, kategori ve etiketler bu anda
	// değişmiş olabilir; "modified" onu izler.
	lastAnalysis := added
	for _, a := range f.Artifacts {
		if a.CreatedAt.After(lastAnalysis) {
			lastAnalysis = a.CreatedAt
		}
	}
	urlID := URLIndicatorID(f.URL)
	out := make([]Object, 0, 1+2*len(f.Artifacts))
	if o.FindingIndicators && f.FirstURL {
		ind := findingIndicator(f.SearchResult, created, lastAnalysis)
		out = append(out, Object{ID: ind.ID, Type: ind.Type, Version: ind.Modified, DateAdded: added, STIX: ind})
	}
	for _, a := range f.Artifacts {
		if feedSkipTypes[a.Type] {
			continue
		}
		modified := created
		if a.CreatedAt.After(modified) {
			modified = a.CreatedAt
		}
		ind, ok := artifactIndicator(a.ResultArtifact, created, modified)
		if !ok {
			continue
		}
		if a.FirstIndicator {
			out = append(out, Object{ID: ind.ID, Type: ind.Type, Version: ind.Modified, DateAdded: added, STIX: ind})
		}
		if o.Relationships && a.FirstRelation {
			rel := relatedTo(ind.ID, urlID, created, created)
			out = append(out, Object{ID: rel.ID, Type: rel.Type, Version: rel.Modified, DateAdded: added, STIX: rel})
		}
	}
	return out
}

// Timestamp TAXII/STIX zaman damgası biçimi (UTC, milisaniye).
func Timestamp(t time.Time) string { return ts(t) }
