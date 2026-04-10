package cti

import (
	"regexp"
	"sort"
	"strings"
)

// Analysis represents CTI classification output.
type Analysis struct {
	Category       string   `json:"category"`
	Criticality    int      `json:"criticality"`
	Confidence     int      `json:"confidence"`
	Score          int      `json:"score"`
	MatchedSignals []string `json:"matchedSignals"`
}

type rule struct {
	category        string
	baseCriticality int
	keywords        map[string]int
	phrases         map[string]int
}

var tokenRegex = regexp.MustCompile(`[a-z0-9][a-z0-9._/-]{1,40}`)

var lowValueSignals = map[string]bool{
	"home":  true,
	"index": true,
	"page":  true,
	"main":  true,
	"click": true,
	"read":  true,
	"more":  true,
	"post":  true,
	"news":  true,
}

var categoryRules = []rule{
	{
		category:        "Ransomware",
		baseCriticality: 5,
		keywords: map[string]int{
			"ransomware": 6,
			"ransom":     4,
			"lockbit":    7,
			"blackcat":   7,
			"alphv":      7,
			"conti":      6,
			"revil":      6,
			"decryptor":  5,
			"extortion":  5,
			"leaksite":   4,
		},
		phrases: map[string]int{
			"double extortion": 7,
			"ransom note":      6,
			"data leak site":   6,
		},
	},
	{
		category:        "Veri Sızıntısı",
		baseCriticality: 5,
		keywords: map[string]int{
			"leak":        4,
			"breach":      6,
			"dump":        6,
			"database":    5,
			"credentials": 6,
			"password":    5,
			"combolist":   6,
			"fullz":       6,
			"stealer":     5,
			"logs":        4,
			"exposed":     4,
			"dox":         4,
		},
		phrases: map[string]int{
			"data leak":       7,
			"credential dump": 7,
			"combo list":      6,
			"sql dump":        6,
			"user database":   6,
			"fresh logs":      5,
		},
	},
	{
		category:        "Finansal Dolandırıcılık",
		baseCriticality: 5,
		keywords: map[string]int{
			"carding":    7,
			"cvv":        7,
			"visa":       4,
			"mastercard": 4,
			"amex":       4,
			"cashout":    6,
			"paypal":     5,
			"bank":       5,
			"iban":       5,
			"swift":      5,
			"wallet":     4,
			"bitcoin":    4,
			"btc":        4,
			"xmr":        4,
		},
		phrases: map[string]int{
			"credit card":   7,
			"bank login":    6,
			"wire transfer": 6,
			"bank account":  6,
		},
	},
	{
		category:        "Exploit / Vulnerability",
		baseCriticality: 5,
		keywords: map[string]int{
			"exploit":   6,
			"0day":      7,
			"zeroday":   7,
			"cve":       6,
			"rce":       7,
			"lfi":       5,
			"sqli":      6,
			"xss":       5,
			"backdoor":  5,
			"rootkit":   6,
			"privesc":   6,
			"payload":   4,
			"shellcode": 6,
		},
		phrases: map[string]int{
			"remote code execution": 8,
			"proof of concept":      5,
			"privilege escalation":  7,
		},
	},
	{
		category:        "Initial Access",
		baseCriticality: 4,
		keywords: map[string]int{
			"rdp":      6,
			"vpn":      4,
			"ssh":      5,
			"cpanel":   5,
			"access":   5,
			"admin":    4,
			"foothold": 5,
			"webshell": 6,
			"loader":   4,
			"botnet":   5,
		},
		phrases: map[string]int{
			"initial access":   7,
			"admin access":     6,
			"corporate access": 7,
			"rdp access":       7,
		},
	},
	{
		category:        "Illegal Market",
		baseCriticality: 4,
		keywords: map[string]int{
			"market":      5,
			"marketplace": 6,
			"vendor":      5,
			"escrow":      5,
			"listing":     4,
			"listings":    4,
			"shop":        4,
			"store":       4,
			"product":     3,
			"shipping":    3,
		},
		phrases: map[string]int{
			"trusted vendor": 6,
			"escrow service": 6,
			"vendor shop":    5,
		},
	},
	{
		category:        "Siber Forum",
		baseCriticality: 3,
		keywords: map[string]int{
			"forum":    6,
			"board":    5,
			"thread":   5,
			"tutorial": 4,
			"method":   4,
			"cracked":  4,
			"cracking": 4,
			"member":   3,
		},
		phrases: map[string]int{
			"private forum": 6,
			"new thread":    5,
			"forum post":    4,
		},
	},
	{
		category:        "İletişim / Ağ",
		baseCriticality: 2,
		keywords: map[string]int{
			"telegram": 5,
			"jabber":   5,
			"tox":      4,
			"xmpp":     4,
			"proxy":    3,
			"tor":      3,
			"onion":    3,
			"channel":  3,
			"opsec":    5,
			"pgp":      5,
		},
		phrases: map[string]int{
			"contact me":   4,
			"pgp key":      6,
			"secure comms": 5,
		},
	},
}

var knownSignalSet = buildKnownSignalSet()

// Analyze classifies textual signals into a CTI category.
func Analyze(title, url, query string, tags []string, keywordHits int) Analysis {
	normalizedTags := normalizeTagList(tags)
	combinedText := strings.ToLower(strings.Join([]string{title, url, query, strings.Join(normalizedTags, " ")}, " "))
	tokenCounts := countTokens(combinedText)

	best := Analysis{
		Category:       "Genel",
		Criticality:    1,
		Confidence:     20,
		Score:          0,
		MatchedSignals: []string{},
	}

	bestScore := 0
	bestMatched := []string{}
	bestRuleCriticality := 1

	for _, r := range categoryRules {
		score, matched := scoreRule(r, tokenCounts, combinedText)
		if score > bestScore {
			bestScore = score
			bestMatched = matched
			bestRuleCriticality = r.baseCriticality
			best.Category = r.category
		}
	}

	if bestScore < 6 {
		if keywordHits > 0 {
			best.Confidence = 25 + minInt(keywordHits, 20)
		}
		return best
	}

	criticality := bestRuleCriticality
	if bestScore >= 14 && criticality < 5 {
		criticality++
	}
	if keywordHits >= 10 && criticality < 5 {
		criticality++
	}

	confidence := 45 + minInt(bestScore*3, 45) + minInt(keywordHits, 10)
	if confidence > 99 {
		confidence = 99
	}

	best.Criticality = criticality
	best.Confidence = confidence
	best.Score = bestScore
	best.MatchedSignals = bestMatched

	return best
}

// MergeTags merges extracted tags with CTI signals.
func MergeTags(extracted, matchedSignals []string, query string, maxTags int) []string {
	if maxTags <= 0 {
		maxTags = 5
	}

	result := make([]string, 0, maxTags)
	seen := make(map[string]bool)

	add := func(raw string) {
		tag := normalizeSignal(raw)
		if tag == "" || seen[tag] || lowValueSignals[tag] {
			return
		}
		seen[tag] = true
		result = append(result, tag)
	}

	for _, signal := range matchedSignals {
		if len(result) >= maxTags {
			break
		}
		add(signal)
	}

	for _, tag := range extracted {
		if len(result) >= maxTags {
			break
		}
		add(tag)
	}

	for _, token := range tokenRegex.FindAllString(strings.ToLower(query), -1) {
		if len(result) >= maxTags {
			break
		}
		normalized := normalizeSignal(token)
		if !knownSignalSet[normalized] {
			continue
		}
		add(token)
	}

	if len(result) > maxTags {
		return result[:maxTags]
	}
	return result
}

func scoreRule(r rule, tokenCounts map[string]int, text string) (int, []string) {
	score := 0
	signalScores := make(map[string]int)

	for keyword, weight := range r.keywords {
		count := tokenCounts[keyword]
		if count <= 0 {
			continue
		}

		hits := minInt(count, 3)
		weighted := weight * hits
		score += weighted
		signalScores[keyword] += weighted
	}

	for phrase, weight := range r.phrases {
		if !strings.Contains(text, phrase) {
			continue
		}
		score += weight
		signalScores[phrase] += weight
	}

	type signal struct {
		name  string
		score int
	}

	signals := make([]signal, 0, len(signalScores))
	for name, s := range signalScores {
		signals = append(signals, signal{name: name, score: s})
	}

	sort.Slice(signals, func(i, j int) bool {
		if signals[i].score == signals[j].score {
			return signals[i].name < signals[j].name
		}
		return signals[i].score > signals[j].score
	})

	matched := make([]string, 0, minInt(6, len(signals)))
	for i := 0; i < len(signals) && i < 6; i++ {
		matched = append(matched, normalizeSignal(signals[i].name))
	}

	return score, matched
}

func countTokens(text string) map[string]int {
	counts := make(map[string]int)
	tokens := tokenRegex.FindAllString(strings.ToLower(text), -1)
	for _, token := range tokens {
		norm := normalizeSignal(token)
		if norm == "" {
			continue
		}
		counts[norm]++
	}
	return counts
}

func normalizeTagList(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		norm := normalizeSignal(tag)
		if norm != "" {
			out = append(out, norm)
		}
	}
	return out
}

func normalizeSignal(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.Trim(s, "-./")
	if len(s) < 3 || len(s) > 40 {
		return ""
	}
	return s
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func buildKnownSignalSet() map[string]bool {
	set := make(map[string]bool)
	for _, r := range categoryRules {
		for keyword := range r.keywords {
			norm := normalizeSignal(keyword)
			if norm != "" {
				set[norm] = true
			}
		}
	}
	return set
}
