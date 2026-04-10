package search

import "testing"

func TestResultPredictCTI_UsesSharedClassifier(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		url     string
		wantCat string
		minCrit int
	}{
		{
			name:    "ransomware",
			title:   "LockBit ransomware negotiation portal",
			url:     "http://abc.onion/ransomware",
			wantCat: "Ransomware",
			minCrit: 5,
		},
		{
			name:    "market",
			title:   "Trusted vendor marketplace listings",
			url:     "http://abc.onion/market/vendor",
			wantCat: "Illegal Market",
			minCrit: 4,
		},
		{
			name:    "general fallback",
			title:   "Welcome to hidden service",
			url:     "http://abc.onion/home",
			wantCat: "Genel",
			minCrit: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Result{Title: tc.title, URL: tc.url}
			r.PredictCTI()
			if r.Category != tc.wantCat {
				t.Fatalf("category = %q, want %q", r.Category, tc.wantCat)
			}
			if r.Criticality < tc.minCrit {
				t.Fatalf("criticality = %d, want >= %d", r.Criticality, tc.minCrit)
			}
		})
	}
}
