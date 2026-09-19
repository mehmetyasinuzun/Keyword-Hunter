// Package export bulguları ve IOC'leri standart CTI formatlarına (STIX 2.1)
// dönüştürür; MISP, OpenCTI, Splunk ES, Sentinel gibi platformlara aktarım için.
package export

import (
	"crypto/sha1"
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

// BuildBundle bulgular ve IOC'lerinden STIX 2.1 paketi üretir.
func BuildBundle(results []storage.SearchResult, src ArtifactSource, includeArtifacts bool) *Bundle {
	now := time.Now()
	ident := identity{
		Type: "identity", SpecVersion: specVersion, ID: deterministicID("identity", producerNS),
		Created: ts(now), Modified: ts(now), Name: "KeywordHunter", IdentityClass: "system",
		Description: "Dark web CTI platform — Tor search engine findings and extracted IOCs",
	}
	b := &Bundle{Type: "bundle", ID: "bundle--" + uuid.NewString(), Objects: []interface{}{ident}}
	seen := map[string]bool{}

	for _, r := range results {
		urlID := deterministicID("indicator", "url:"+r.URL)
		labels := []string{"dark-web", "tor"}
		if r.Category != "" && r.Category != "Genel" {
			labels = append(labels, strings.ToLower(strings.ReplaceAll(r.Category, " ", "-")))
		}
		for _, t := range strings.Split(r.AutoTags, ",") {
			if t = strings.TrimSpace(t); t != "" {
				labels = append(labels, t)
			}
		}
		if !seen[urlID] {
			seen[urlID] = true
			b.Objects = append(b.Objects, indicator{
				Type: "indicator", SpecVersion: specVersion, ID: urlID, CreatedByRef: ident.ID,
				Created: ts(r.CreatedAt), Modified: ts(now),
				Name:           fmt.Sprintf("[%s] %s", r.Category, truncate(r.Title, 120)),
				Description:    fmt.Sprintf("Query: %q · Engine: %s · Criticality: %d/5 · Keyword hits: %d", r.Query, r.Source, r.Criticality, r.KeywordCount),
				IndicatorTypes: []string{"malicious-activity"},
				Pattern:        fmt.Sprintf("[url:value = '%s']", escape(r.URL)),
				PatternType:    "stix", ValidFrom: ts(r.CreatedAt),
				Confidence: confidenceFromCriticality(r.Criticality), Labels: labels,
				ExternalRefs: []extRef{{SourceName: "keywordhunter", ExtID: fmt.Sprintf("result-%d", r.ID)}},
			})
		}
		if !includeArtifacts || src == nil {
			continue
		}
		arts, err := src.GetArtifacts(r.ID)
		if err != nil {
			continue
		}
		for _, a := range arts {
			pattern, types := artifactPattern(a)
			if pattern == "" {
				continue
			}
			aid := deterministicID("indicator", a.Type+":"+a.Value)
			if !seen[aid] {
				seen[aid] = true
				b.Objects = append(b.Objects, indicator{
					Type: "indicator", SpecVersion: specVersion, ID: aid, CreatedByRef: ident.ID,
					Created: ts(a.CreatedAt), Modified: ts(now),
					Name: fmt.Sprintf("%s: %s", a.Type, truncate(a.Value, 80)), Description: truncate(a.Context, 300),
					IndicatorTypes: types, Pattern: pattern, PatternType: "stix", ValidFrom: ts(a.CreatedAt),
					Confidence: int(a.Confidence * 100), Labels: []string{"dark-web", "ioc", a.Type},
				})
			}
			relID := deterministicID("relationship", aid+">"+urlID)
			if !seen[relID] {
				seen[relID] = true
				b.Objects = append(b.Objects, relationship{
					Type: "relationship", SpecVersion: specVersion, ID: relID, Created: ts(now), Modified: ts(now),
					RelationshipType: "related-to", SourceRef: aid, TargetRef: urlID,
				})
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
	h := sha1.New()
	for _, o := range b.Objects {
		fmt.Fprintf(h, "%v", o)
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}
