package cti

import "testing"

func TestAnalyze_CategorizesStrongSignals(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		url     string
		query   string
		tags    []string
		wantCat string
		minCrit int
		minConf int
	}{
		{
			name:    "ransomware",
			title:   "LockBit ransomware leak site update",
			url:     "http://example.onion/ransom",
			query:   "lockbit",
			tags:    []string{"ransomware", "extortion"},
			wantCat: "Ransomware",
			minCrit: 5,
			minConf: 70,
		},
		{
			name:    "data leak",
			title:   "Fresh database dump with credentials",
			url:     "http://example.onion/leak",
			query:   "credential dump",
			tags:    []string{"database", "credentials"},
			wantCat: "Veri Sızıntısı",
			minCrit: 5,
			minConf: 65,
		},
		{
			name:    "initial access",
			title:   "Corporate RDP access for sale",
			url:     "http://example.onion/access",
			query:   "rdp admin access",
			tags:    []string{"rdp", "access"},
			wantCat: "Initial Access",
			minCrit: 4,
			minConf: 60,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(tc.title, tc.url, tc.query, tc.tags, 3)
			if got.Category != tc.wantCat {
				t.Fatalf("category = %q, want %q", got.Category, tc.wantCat)
			}
			if got.Criticality < tc.minCrit {
				t.Fatalf("criticality = %d, want >= %d", got.Criticality, tc.minCrit)
			}
			if got.Confidence < tc.minConf {
				t.Fatalf("confidence = %d, want >= %d", got.Confidence, tc.minConf)
			}
			if len(got.MatchedSignals) == 0 {
				t.Fatalf("expected matched signals")
			}
		})
	}
}

func TestAnalyze_FallbackGeneral(t *testing.T) {
	got := Analyze("Welcome page", "http://example.onion/home", "news", nil, 0)
	if got.Category != "Genel" {
		t.Fatalf("category = %q, want Genel", got.Category)
	}
	if got.Criticality != 1 {
		t.Fatalf("criticality = %d, want 1", got.Criticality)
	}
}

func TestMergeTags_PrioritizesSignalsAndDeduplicates(t *testing.T) {
	merged := MergeTags(
		[]string{"home", "ransomware", "ransomware", "panel"},
		[]string{"lockbit", "ransom-note", "home"},
		"rdp access",
		5,
	)

	if len(merged) == 0 {
		t.Fatalf("expected merged tags")
	}
	if merged[0] != "lockbit" {
		t.Fatalf("first tag = %q, want lockbit", merged[0])
	}
	seen := map[string]bool{}
	for _, tag := range merged {
		if seen[tag] {
			t.Fatalf("duplicate tag found: %s", tag)
		}
		seen[tag] = true
	}
}
