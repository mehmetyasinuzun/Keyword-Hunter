// Package export bulguları ve IOC'leri standart CTI formatlarına (STIX 2.1)
// dönüştürür; MISP, OpenCTI, Splunk ES, Sentinel gibi platformlara aktarım için.
package export

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"keywordhunter-mvp/pkg/storage"
)

// STIX 2.1 sabitleri
const (
	specVersion = "2.1"
	producerNS  = "keywordhunter"
)

// Bundle STIX 2.1 paket kökü.
type Bundle struct {
	Type    string        `json:"type"`
	ID      string        `json:"id"`
	Objects []interface{} `json:"objects"`
}

type identity struct {
	Type          string `json:"type"`
	SpecVersion   string `json:"spec_version"`
	ID            string `json:"id"`
	Created       string `json:"created"`
	Modified      string `json:"modified"`
	Name          string `json:"name"`
	IdentityClass string `json:"identity_class"`
	Description   string `json:"description,omitempty"`
}

type indicator struct {
	Type           string   `json:"type"`
	SpecVersion    string   `json:"spec_version"`
	ID             string   `json:"id"`
	CreatedByRef   string   `json:"created_by_ref"`
	Created        string   `json:"created"`
	Modified       string   `json:"modified"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	IndicatorTypes []string `json:"indicator_types"`
	Pattern        string   `json:"pattern"`
	PatternType    string   `json:"pattern_type"`
	ValidFrom      string   `json:"valid_from"`
	Confidence     int      `json:"confidence"`
	Labels         []string `json:"labels,omitempty"`
	ExternalRefs   []extRef `json:"external_references,omitempty"`
}

type extRef struct {
	SourceName string `json:"source_name"`
	URL        string `json:"url,omitempty"`
	ExtID      string `json:"external_id,omitempty"`
}

type relationship struct {
	Type             string `json:"type"`
	SpecVersion      string `json:"spec_version"`
	ID               string `json:"id"`
	Created          string `json:"created"`
	Modified         string `json:"modified"`
	RelationshipType string `json:"relationship_type"`
	SourceRef        string `json:"source_ref"`
	TargetRef        string `json:"target_ref"`
}

// ArtifactSource bir bulgunun IOC'lerini sağlar (storage.DB uyar).
type ArtifactSource interface {
	GetArtifacts(resultID int64) ([]storage.ResultArtifact, error)
}

// deterministicID aynı değer için hep aynı STIX kimliğini üretir (yeniden
// export'ta çift kayıt oluşmaz): UUIDv5(namespace, "type:value").
var nsUUID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://github.com/mehmetyasinuzun/Keyword-Hunter"))

func deterministicID(typ, value string) string {
	return typ + "--" + uuid.NewSHA1(nsUUID, []byte(typ+":"+value)).String()
}

func ts(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// escape STIX pattern string literal kaçışı (' ve \).
func escape(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	return strings.ReplaceAll(v, `'`, `\'`)
}

// confidenceFromCriticality 1-5 → 0-100.
func confidenceFromCriticality(c int) int {
	switch {
	case c >= 5:
		return 90
	case c == 4:
		return 75
	case c == 3:
		return 60
	case c == 2:
		return 40
	default:
		return 20
	}
}

// artifactPattern IOC türünü STIX desenine çevirir; desteklenmeyen tür → "".
func artifactPattern(a storage.ResultArtifact) (pattern string, types []string) {
	v := escape(a.Value)
	switch a.Type {
	case "email":
		return fmt.Sprintf("[email-addr:value = '%s']", v), []string{"compromised"}
	case "ip":
		return fmt.Sprintf("[ipv4-addr:value = '%s']", v), []string{"malicious-activity"}
	case "onion":
		return fmt.Sprintf("[domain-name:value = '%s']", v), []string{"malicious-activity"}
	case "hash":
		algo := map[int]string{32: "MD5", 40: "SHA-1", 64: "SHA-256"}[len(a.Value)]
		if algo == "" {
			return "", nil
		}
		return fmt.Sprintf("[file:hashes.'%s' = '%s']", algo, v), []string{"malicious-activity"}
	case "bitcoin", "monero":
		return fmt.Sprintf("[x-cryptocurrency-address:value = '%s' AND x-cryptocurrency-address:currency = '%s']", v, a.Type), []string{"attribution"}
	case "credit_card":
		return fmt.Sprintf("[x-payment-card:number = '%s']", v), []string{"compromised"}
	case "phone":
		return fmt.Sprintf("[x-phone-number:value = '%s']", v), []string{"attribution"}
	default:
		return "", nil
	}
}

// IdentityID KeywordHunter üretici kimliğinin sabit STIX kimliği.
var IdentityID = deterministicID("identity", producerNS)

// identityEpoch kimlik nesnesinin sabit oluşturulma/değişiklik zamanı: nesne
// her dışa aktarmada ve her TAXII isteğinde birebir aynı kalmalı ki tüketiciler
// (OpenCTI, Sentinel) her çekişte "yeni sürüm" görmesin.
var identityEpoch = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func producerIdentity() identity {
	return identity{
		Type: "identity", SpecVersion: specVersion, ID: IdentityID,
		Created: ts(identityEpoch), Modified: ts(identityEpoch), Name: "KeywordHunter", IdentityClass: "system",
		Description: "Dark web CTI platform — Tor search engine findings and extracted IOCs",
	}
}

// URLIndicatorID bir bulgu URL'sinin göstergesinin deterministik kimliği.
func URLIndicatorID(u string) string { return deterministicID("indicator", "url:"+u) }

// ArtifactIndicatorID bir IOC göstergesinin deterministik kimliği.
func ArtifactIndicatorID(typ, value string) string {
	return deterministicID("indicator", typ+":"+value)
}

// relationshipID IOC → bulgu ilişkisinin deterministik kimliği.
func relationshipID(src, dst string) string { return deterministicID("relationship", src+">"+dst) }

// findingLabels bulgunun STIX etiketleri (kategori + otomatik etiketler).
func findingLabels(r storage.SearchResult) []string {
	labels := []string{"dark-web", "tor"}
	if r.Category != "" && r.Category != "Genel" {
		labels = append(labels, strings.ToLower(strings.ReplaceAll(r.Category, " ", "-")))
	}
	for _, t := range strings.Split(r.AutoTags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			labels = append(labels, t)
		}
	}
	return labels
}

// findingIndicator bulgunun URL göstergesini üretir.
func findingIndicator(r storage.SearchResult, created, modified time.Time) indicator {
	return indicator{
		Type: "indicator", SpecVersion: specVersion, ID: URLIndicatorID(r.URL), CreatedByRef: IdentityID,
		Created: ts(created), Modified: ts(modified),
		Name:           fmt.Sprintf("[%s] %s", r.Category, truncate(r.Title, 120)),
		Description:    fmt.Sprintf("Query: %q · Engine: %s · Criticality: %d/5 · Keyword hits: %d", r.Query, r.Source, r.Criticality, r.KeywordCount),
		IndicatorTypes: []string{"malicious-activity"},
		Pattern:        fmt.Sprintf("[url:value = '%s']", escape(r.URL)),
		PatternType:    "stix", ValidFrom: ts(created),
		Confidence: confidenceFromCriticality(r.Criticality), Labels: findingLabels(r),
		ExternalRefs: []extRef{{SourceName: "keywordhunter", ExtID: fmt.Sprintf("result-%d", r.ID)}},
	}
}

// artifactIndicator IOC göstergesini üretir; desteklenmeyen tür → false.
func artifactIndicator(a storage.ResultArtifact, created, modified time.Time) (indicator, bool) {
	pattern, types := artifactPattern(a)
	if pattern == "" {
		return indicator{}, false
	}
	return indicator{
		Type: "indicator", SpecVersion: specVersion, ID: ArtifactIndicatorID(a.Type, a.Value), CreatedByRef: IdentityID,
		Created: ts(created), Modified: ts(modified),
		Name: fmt.Sprintf("%s: %s", a.Type, truncate(a.Value, 80)), Description: truncate(a.Context, 300),
		IndicatorTypes: types, Pattern: pattern, PatternType: "stix", ValidFrom: ts(created),
		Confidence: confidenceFromArtifact(a.Confidence), Labels: []string{"dark-web", "ioc", a.Type},
	}, true
}

// confidenceFromArtifact 0–1 güven skorunu STIX'in 0–100 aralığına sabitler.
func confidenceFromArtifact(c float64) int {
	v := int(c * 100)
	return max(0, min(100, v))
}

func relatedTo(aid, urlID string, created, modified time.Time) relationship {
	return relationship{
		Type: "relationship", SpecVersion: specVersion, ID: relationshipID(aid, urlID), Created: ts(created), Modified: ts(modified),
		RelationshipType: "related-to", SourceRef: aid, TargetRef: urlID,
	}
}

// BuildBundle bulgular ve IOC'lerinden STIX 2.1 paketi üretir.
func BuildBundle(results []storage.SearchResult, src ArtifactSource, includeArtifacts bool) *Bundle {
	now := time.Now()
	b := &Bundle{Type: "bundle", ID: "bundle--" + uuid.NewString(), Objects: []interface{}{producerIdentity()}}
	seen := map[string]bool{}

	for _, r := range results {
		urlID := URLIndicatorID(r.URL)
		if !seen[urlID] {
			seen[urlID] = true
			b.Objects = append(b.Objects, findingIndicator(r, r.CreatedAt, now))
		}
		if !includeArtifacts || src == nil {
			continue
		}
		arts, err := src.GetArtifacts(r.ID)
		if err != nil {
			continue
		}
		for _, a := range arts {
			ind, ok := artifactIndicator(a, a.CreatedAt, now)
			if !ok {
				continue
			}
			if !seen[ind.ID] {
				seen[ind.ID] = true
				b.Objects = append(b.Objects, ind)
			}
			rel := relatedTo(ind.ID, urlID, now, now)
			if !seen[rel.ID] {
				seen[rel.ID] = true
				b.Objects = append(b.Objects, rel)
			}
		}
	}
	return b
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Fingerprint paketin içerik özetini döndürür (loglama/ETag için).
func Fingerprint(b *Bundle) string {
	h := sha256.New()
	for _, o := range b.Objects {
		fmt.Fprintf(h, "%v", o)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}
