package cti

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Çok dilli sinyal motoru.
//
// Eski TR/EN kuralları (rule.keywords / rule.phrases) bilinçli olarak olduğu gibi
// korunur; bu dosya onların yanına, aynı kategori/kritiklik şemasına puan ekleyen
// ikinci bir eşleştirici koyar. Farkları:
//
//   - Metin ve desenler aynı katlamadan geçer: Unicode küçük harf (Kiril dahil),
//     aksan/işaret silme (contraseña → contrasena, Geldwäsche → geldwasche,
//     fórum → forum), ß → ss, ё → е, Türkçe ı/İ → i. Böylece aksanlı ve aksansız
//     (ASCII ile yazılmış) biçimler aynı sinyale düşer.
//   - Eşleşme her zaman kelime sınırlıdır: metin harf/rakam dışı her karakterden
//     bölünür (URL yolları dahil) ve desen parçaları tam token ile karşılaştırılır.
//     Kısa bir Rusça kök başka bir kelimenin ORTASINDA asla tetiklenmez.
//   - Sonu '*' ile biten parça köktür: token bu önekle BAŞLAMALIDIR (обнал* →
//     обнал, обналичивание, обнальщик). Tek başına kök en az minStemRunes,
//     ifade içindeki kök en az minPhraseStemRunes harf olmak zorundadır; daha
//     kısa / belirsiz kökler (мет, крипт, скам …) tam biçim ve çekim listesiyle
//     yazılır.
//   - Birden çok parçalı desen ardışık token dizisidir ("слив баз*",
//     "lavagem de dinheiro"). Tek parçalı sinyal ağırlık × min(adet, 3),
//     ifade ise ağırlık × 1 puan getirir (eski motorla aynı ölçek).
//   - Almanca umlaut'lu desenler için ae/oe/ue yazımı otomatik eklenir
//     (Geldwäsche hem "geldwasche" hem "geldwaesche" olarak yakalanır; Türkçe
//     ö/ü içeren desenlerde bu ek varyant zararsızdır).
//   - Her sinyalin SABİT bir etiket adı vardır: "desen|etiket" ile açıkça
//     verilir, verilmezse desenin '*' atılmış küçük harfli yazımıdır. Aynı
//     etiketi paylaşan desenler (çekimler, transliterasyonlar: пробива|пробив,
//     probiv|пробив) TEK sinyaldir: adetleri toplanır, 3 ile sınırlanır ve tek
//     etiket üretir. Böylece "ботнет", "ботнета", "ботнету" ya da "Geldwäsche"
//     ve "Geldwaesche" aynı etikete düşer; ?tag= süzgeci hepsini bulur.
//   - Ağırlığı supportMaxWeight ve altındaki sinyaller yalnız DESTEKLEYİCİDİR
//     (выкуп, слив, golpe, ddos …): tek kez sayılır ve aynı kuralda başka
//     (eski ya da çok dilli) bir sinyal yoksa hiç puan getirmez. Tekrar ya da
//     sorgu metni bunları eşik üstüne taşıyamaz.
//
// Desenler paket yüklenirken bir kez derlenir; Analyze başına maliyet tek bir
// katlama + bölme ve ilk harfe göre indekslenmiş küçük bir taramadır.

const (
	// minStemRunes tek başına duran kökün ('*') katlanmış en kısa uzunluğu.
	minStemRunes = 4
	// minPhraseStemRunes ifade içindeki kök için alt sınır; komşu kelime bağlamı
	// sağladığından bir harf kısa olabilir ("слив баз*" → слив базы / базу).
	minPhraseStemRunes = 3
	// maxMultiTokens tek analizde taranacak en fazla token (sınırlı girdi).
	maxMultiTokens = 4096
	// supportMaxWeight bu ağırlık ve altındaki sinyaller yalnız destekleyicidir.
	supportMaxWeight = 3
	// maxHitsPerSignal tek parçalı sinyalin puanlanan en fazla adedi (eski motorla aynı).
	maxHitsPerSignal = 3
)

// foldReplacements tek harfin katlanmış karşılığı (küçük harfe çevrildikten sonra).
var foldReplacements = map[rune]string{
	// Latin aksanları (DE / ES / PT / FR) ve Türkçe harfler
	'á': "a", 'à': "a", 'â': "a", 'ä': "a", 'ã': "a", 'å': "a", 'ā': "a",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e", 'ē': "e",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i", 'ı': "i",
	'ó': "o", 'ò': "o", 'ô': "o", 'ö': "o", 'õ': "o", 'ø': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ü': "u",
	'ý': "y", 'ÿ': "y",
	'ñ': "n", 'ç': "c", 'ş': "s", 'ğ': "g",
	'ß': "ss", 'æ': "ae", 'œ': "oe",
	// Kiril: ё yazımda çoğunlukla е ile yazılır
	'ё': "е",
}

// foldText metni karşılaştırma biçimine indirger (bkz. dosya başı).
func foldText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		r = unicode.ToLower(r)
		if rep, ok := foldReplacements[r]; ok {
			b.WriteString(rep)
			continue
		}
		// Ayrışık (NFD) gelen metindeki birleşen işaretler: "ñ" → "n"
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// isWordRune token gövdesine dahil edilen karakterler.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// foldTokens metni kelime sınırlarından böler ve her tokenı katlar. Birleşen
// işaretler (NFD) tokenın parçası sayılır ki "contrasen\u0303a" bölünmesin.
// En fazla maxMultiTokens token döner.
func foldTokens(s string) []string {
	raw := strings.FieldsFunc(s, func(r rune) bool { return !isWordRune(r) && !unicode.Is(unicode.Mn, r) })
	out := make([]string, 0, minInt(len(raw), maxMultiTokens))
	for _, tok := range raw {
		if len(out) == maxMultiTokens {
			break
		}
		if f := foldText(tok); f != "" {
			out = append(out, f)
		}
	}
	return out
}

type multiPart struct {
	text string // katlanmış
	stem bool   // önek (kök) eşleşmesi
}

func (p multiPart) matches(token string) bool {
	if p.stem {
		return strings.HasPrefix(token, p.text)
	}
	return token == p.text
}

// multiSignal bir etiket adı altında toplanan sinyal. Aynı adı paylaşan bütün
// desenler (çekim/transliterasyon/umlaut varyantları) bu kaydı paylaşır.
type multiSignal struct {
	name    string // sabit etiket adı (küçük harf, '*' yok)
	rule    int    // categoryRules indeksi
	weight  int
	phrase  bool // birden çok parça → ağırlık bir kez
	support bool // weight <= supportMaxWeight → yalnız destekleyici
}

type multiVariant struct {
	signal int
	parts  []multiPart
}

type multiMatcher struct {
	signals []multiSignal
	byFirst map[rune][]multiVariant // ilk parçanın ilk harfine göre indeks
}

// ruleMulti bir kurala düşen çok dilli puanlar (etiket adı → puan).
type ruleMulti struct {
	scores  map[string]int // kendi başına sayılan sinyaller
	support map[string]int // yalnız kuralda başka sinyal varsa sayılanlar
}

// splitLabel "desen|etiket" anahtarını ayırır; etiket yoksa desenden türetilir.
func splitLabel(key string) (pattern, label string) {
	pattern, label, ok := strings.Cut(key, "|")
	if !ok {
		label = strings.ReplaceAll(pattern, "*", "")
	}
	return pattern, strings.Join(strings.Fields(strings.ToLower(label)), " ")
}

// parsePattern bir deseni katlayıp parçalara ayırır. Hatalı desen programcı
// hatasıdır; derleme anında panik ile yakalanır (testler de doğrular).
func parsePattern(pattern string) ([]multiPart, error) {
	folded := foldText(pattern)
	fields := strings.FieldsFunc(folded, func(r rune) bool { return !isWordRune(r) && r != '*' })
	if len(fields) == 0 {
		return nil, fmt.Errorf("boş desen %q", pattern)
	}
	parts := make([]multiPart, 0, len(fields))
	for _, f := range fields {
		stem := strings.HasSuffix(f, "*")
		text := strings.TrimSuffix(f, "*")
		if strings.Contains(text, "*") || text == "" {
			return nil, fmt.Errorf("desen %q: '*' yalnız parça sonunda olabilir", pattern)
		}
		n := utf8.RuneCountInString(text)
		minStem := minStemRunes
		if len(fields) > 1 {
			minStem = minPhraseStemRunes
		}
		switch {
		case stem && n < minStem:
			return nil, fmt.Errorf("desen %q: kök %q çok kısa (en az %d harf)", pattern, text, minStem)
		case len(fields) == 1 && n < 2:
			return nil, fmt.Errorf("desen %q: tek parçalı sinyal en az 2 harf olmalı", pattern)
		}
		parts = append(parts, multiPart{text: text, stem: stem})
	}
	return parts, nil
}

// umlautVariant Almanca umlaut'ları ae/oe/ue biçimine çevirir; umlaut yoksa "".
func umlautVariant(pattern string) string {
	lower := strings.ToLower(pattern)
	if !strings.ContainsAny(lower, "äöü") {
		return ""
	}
	return strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue").Replace(lower)
}

// legacySignalRules eski anahtar kelime/ifade adlarının (normalize) kural indeksleri.
func legacySignalRules(rules []rule) map[string]int {
	out := make(map[string]int)
	for i, r := range rules {
		for k := range r.keywords {
			out[normalizeSignal(k)] = i
		}
		for p := range r.phrases {
			out[normalizeSignal(p)] = i
		}
	}
	return out
}

// compileMultilingual çok dilli sinyal tablosunu kategori kurallarına bağlar.
func compileMultilingual(rules []rule, table map[string]map[string]int) (*multiMatcher, error) {
	index := make(map[string]int, len(rules))
	for i, r := range rules {
		index[r.category] = i
	}
	legacy := legacySignalRules(rules)
	byName := make(map[string]int)
	m := &multiMatcher{byFirst: make(map[rune][]multiVariant)}
	for category, signals := range table {
		ri, ok := index[category]
		if !ok {
			return nil, fmt.Errorf("bilinmeyen kategori %q", category)
		}
		for key, weight := range signals {
			if weight <= 0 {
				return nil, fmt.Errorf("desen %q: ağırlık pozitif olmalı", key)
			}
			pattern, label := splitLabel(key)
			parts, err := parsePattern(pattern)
			if err != nil {
				return nil, err
			}
			if tag := normalizeSignal(label); tag == "" || strings.Contains(label, "*") || strings.Contains(label, "|") {
				return nil, fmt.Errorf("desen %q: geçersiz etiket %q", key, label)
			}
			if lr, ok := legacy[normalizeSignal(label)]; ok && lr != ri {
				return nil, fmt.Errorf("desen %q: etiket %q başka kategorinin eski sinyali", key, label)
			}
			sig := multiSignal{name: label, rule: ri, weight: weight, phrase: len(parts) > 1, support: weight <= supportMaxWeight}
			id, seen := byName[label]
			switch {
			case !seen:
				id = len(m.signals)
				byName[label] = id
				m.signals = append(m.signals, sig)
			case m.signals[id] != sig:
				return nil, fmt.Errorf("desen %q: etiket %q farklı kategori/ağırlık/biçimle tekrar kullanılmış", key, label)
			}
			m.add(multiVariant{signal: id, parts: parts})
			if alt := umlautVariant(pattern); alt != "" {
				altParts, err := parsePattern(alt)
				if err != nil {
					return nil, err
				}
				if !samePartsList(parts, altParts) {
					m.add(multiVariant{signal: id, parts: altParts})
				}
			}
		}
	}
	return m, nil
}

func samePartsList(a, b []multiPart) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m *multiMatcher) add(v multiVariant) {
	first, _ := utf8.DecodeRuneInString(v.parts[0].text)
	m.byFirst[first] = append(m.byFirst[first], v)
}

// scan katlanmış token dizisindeki eşleşmeleri sinyal (etiket) indeksine göre
// sayar. Aynı etiketin farklı varyantları aynı sayaca yazar.
func (m *multiMatcher) scan(tokens []string) map[int]int {
	var hits map[int]int
	for i, tok := range tokens {
		first, _ := utf8.DecodeRuneInString(tok)
		for _, v := range m.byFirst[first] {
			if i+len(v.parts) > len(tokens) {
				continue
			}
			ok := true
			for j, p := range v.parts {
				if !p.matches(tokens[i+j]) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			if hits == nil {
				hits = make(map[int]int)
			}
			hits[v.signal]++
		}
	}
	return hits
}

// queryTag sorgudan gelen tek bir tokenın çok dilli bir sinyal olup olmadığını
// söyler ve sinyalin SABİT etiket adını döndürür (ham token değil). Token
// kelime sınırlarından bölündüğünde tam olarak tek kelime olmalıdır;
// "extorsion.evil.onion/x" gibi ekli tokenlar ve destekleyici sinyaller reddedilir.
func (m *multiMatcher) queryTag(token string) (string, bool) {
	tokens := foldTokens(token)
	if len(tokens) != 1 {
		return "", false
	}
	tok := tokens[0]
	first, _ := utf8.DecodeRuneInString(tok)
	for _, v := range m.byFirst[first] {
		sig := m.signals[v.signal]
		if len(v.parts) == 1 && !sig.support && v.parts[0].matches(tok) {
			return sig.name, true
		}
	}
	return "", false
}

// ruleScores tarama sonucunu kural bazında etiket adı → puan haritalarına çevirir.
// Tek parçalı sinyal ağırlık × min(adet, 3), ifade ağırlık × 1; destekleyici
// sinyal ağırlık × 1 ve ayrı tutulur (bkz. scoreRule).
func (m *multiMatcher) ruleScores(hits map[int]int) map[int]*ruleMulti {
	if len(hits) == 0 {
		return nil
	}
	out := make(map[int]*ruleMulti)
	for id, count := range hits {
		sig := m.signals[id]
		rm := out[sig.rule]
		if rm == nil {
			rm = &ruleMulti{scores: map[string]int{}, support: map[string]int{}}
			out[sig.rule] = rm
		}
		switch {
		case sig.support:
			rm.support[sig.name] = sig.weight
		case sig.phrase:
			rm.scores[sig.name] = sig.weight
		default:
			rm.scores[sig.name] = sig.weight * minInt(count, maxHitsPerSignal)
		}
	}
	return out
}

// analyze metni tarar ve kural bazında çok dilli puanları döndürür.
func (m *multiMatcher) analyze(text string) map[int]*ruleMulti {
	return m.ruleScores(m.scan(foldTokens(text)))
}

func mustCompileMultilingual() *multiMatcher {
	m, err := compileMultilingual(categoryRules, multilingualSignals)
	if err != nil {
		panic("cti: çok dilli sinyaller derlenemedi: " + err.Error())
	}
	return m
}
