package misp

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/shared"
	"keywordhunter-mvp/pkg/storage"
)

// Event MISP olay gövdesi (POST /events/add → {"Event": …}). MISP sayısal
// alanları metin olarak da kabul eder; kendi dışa aktarımıyla aynı biçim
// kullanılır.
type Event struct {
	Info          string      `json:"info"`
	Date          string      `json:"date"`
	Distribution  string      `json:"distribution"`
	ThreatLevelID string      `json:"threat_level_id"`
	Analysis      string      `json:"analysis"`
	Published     bool        `json:"published"`
	Attribute     []Attribute `json:"Attribute"`
	Tag           []Tag       `json:"Tag,omitempty"`
}

// Attribute MISP özniteliği.
type Attribute struct {
	Type         string `json:"type"`
	Category     string `json:"category"`
	Value        string `json:"value"`
	Comment      string `json:"comment,omitempty"`
	ToIDS        bool   `json:"to_ids"`
	Distribution string `json:"distribution"`
}

// Tag MISP etiketi.
type Tag struct {
	Name string `json:"name"`
}

// Finding gönderilecek bir bulgu ve IOC'leri.
type Finding struct {
	Result    storage.SearchResult
	Artifacts []storage.ResultArtifact
}

// EventOptions olay düzeyi ayarlar.
type EventOptions struct {
	Distribution int
	Tags         []string
	Now          time.Time
}

// BuildStats eşlemenin özeti (yanıt ve denetim kaydı için).
type BuildStats struct {
	Findings   int            `json:"findings"`
	Attributes int            `json:"attributes"`
	Skipped    map[string]int `json:"skipped,omitempty"`
}

// distributionInherit özniteliğin dağıtımını olaydan alması (MISP "5").
const distributionInherit = "5"

// ThreatLevel bulguların en yüksek kritikliğini MISP tehdit düzeyine çevirir:
// 4–5 → 1 (High), 3 → 2 (Medium), 1–2 → 3 (Low).
func ThreatLevel(maxCriticality int) int {
	switch {
	case maxCriticality >= 4:
		return 1
	case maxCriticality == 3:
		return 2
	default:
		return 3
	}
}

// mapArtifact bir IOC'yi MISP öznitelik türü + kategorisine çevirir. Boş tür
// "gönderilmez" demektir; nedeni ikinci dönüş değeridir. Kart numaraları (PCI
// kişisel veri) ve sır niteliğindeki türler (API anahtarı, SSH anahtarı)
// hiçbir koşulda gönderilmez.
func mapArtifact(a storage.ResultArtifact) (typ, category, skip string) {
	v := strings.TrimSpace(a.Value)
	switch a.Type {
	case "email":
		return "email-src", "Payload delivery", ""
	case "bitcoin":
		return "btc", "Financial fraud", ""
	case "monero":
		return "xmr", "Financial fraud", ""
	case "ip":
		return "ip-dst", "Network activity", ""
	case "hash":
		switch len(v) {
		case 32:
			return "md5", "Payload delivery", ""
		case 40:
			return "sha1", "Payload delivery", ""
		case 64:
			return "sha256", "Payload delivery", ""
		}
		return "", "", "hash_length"
	case "onion":
		if strings.Contains(v, "/") {
			return "url", "Network activity", ""
		}
		return "domain", "Network activity", ""
	case "credit_card":
		return "", "", "credit_card"
	default:
		return "", "", a.Type
	}
}

// BuildEvent bulgulardan tek bir MISP olayı kurar: her bulgunun URL'si
// (to_ids=false, yorum = başlık) ve eşlenebilen IOC'leri. Aynı (tür, değer)
// ikinci kez eklenmez (MISP yinelenen özniteliği reddeder).
func BuildEvent(findings []Finding, o EventOptions) (Event, BuildStats) {
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	st := BuildStats{Skipped: map[string]int{}}
	ev := Event{
		Date:         now.UTC().Format("2006-01-02"),
		Distribution: strconv.Itoa(max(0, min(3, o.Distribution))),
		Analysis:     "0", // Initial: dark web bulgusu henüz doğrulanmadı
		Published:    false,
		Attribute:    []Attribute{},
	}
	seen := map[string]bool{}
	add := func(at Attribute) bool {
		key := at.Type + "\x00" + at.Value
		if seen[key] {
			return false
		}
		if len(ev.Attribute) >= MaxAttributes {
			st.Skipped["limit"]++
			return false
		}
		seen[key] = true
		ev.Attribute = append(ev.Attribute, at)
		return true
	}
	maxCrit := 0
	queries := map[string]bool{}
	for _, f := range findings {
		r := f.Result
		st.Findings++
		maxCrit = max(maxCrit, r.Criticality)
		if q := strings.TrimSpace(r.Query); q != "" {
			queries[q] = true
		}
		title := cleanComment(r.Title)
		if u := strings.TrimSpace(r.URL); u != "" {
			add(Attribute{Type: "url", Category: "Network activity", Value: u, Comment: title, ToIDS: false, Distribution: distributionInherit})
		}
		for _, a := range f.Artifacts {
			typ, cat, skip := mapArtifact(a)
			if typ == "" {
				st.Skipped[skip]++
				continue
			}
			v := strings.TrimSpace(a.Value)
			if v == "" {
				continue
			}
			// IOC'ler dark web metninden otomatik çıkarıldı ve doğrulanmadı:
			// IDS dışa aktarımına girmez (analist MISP'te bilinçli açar).
			add(Attribute{Type: typ, Category: cat, Value: v, Comment: cleanComment("KeywordHunter: " + r.Title), ToIDS: false, Distribution: distributionInherit})
		}
	}
	ev.ThreatLevelID = strconv.Itoa(ThreatLevel(maxCrit))
	ev.Info = eventInfo(st.Findings, queries)
	for _, t := range o.Tags {
		ev.Tag = append(ev.Tag, Tag{Name: t})
	}
	st.Attributes = len(ev.Attribute)
	if len(st.Skipped) == 0 {
		st.Skipped = nil
	}
	return ev, st
}

// eventInfo olay başlığı: bulgu sayısı ve (en fazla 3) sorgu.
func eventInfo(n int, queries map[string]bool) string {
	qs := make([]string, 0, len(queries))
	for q := range queries {
		qs = append(qs, q)
	}
	sort.Strings(qs)
	info := fmt.Sprintf("KeywordHunter: %d dark web finding(s)", n)
	if len(qs) > 0 {
		more := ""
		if len(qs) > 3 {
			more = fmt.Sprintf(" +%d", len(qs)-3)
			qs = qs[:3]
		}
		info += " — query: " + strings.Join(qs, ", ") + more
	}
	return shared.TruncateRunes(cleanComment(info), 250)
}

// cleanComment kontrol karakterlerini atar ve yorumu kısaltır (dark web metni).
func cleanComment(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	return shared.TruncateRunes(s, 250)
}
