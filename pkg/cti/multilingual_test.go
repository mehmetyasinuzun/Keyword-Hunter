package cti

import (
	"strings"
	"testing"
)

type mlCase struct {
	name    string
	title   string
	url     string
	wantCat string
	minCrit int
}

func runMLCases(t *testing.T, cases []mlCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Analyze(tc.title, tc.url, "", nil, 0)
			if got.Category != tc.wantCat {
				t.Fatalf("%q: category = %q, want %q (score=%d signals=%v)", tc.title, got.Category, tc.wantCat, got.Score, got.MatchedSignals)
			}
			if got.Criticality < tc.minCrit {
				t.Fatalf("%q: criticality = %d, want >= %d", tc.title, got.Criticality, tc.minCrit)
			}
			if len(got.MatchedSignals) == 0 {
				t.Fatalf("%q: matched signals boş", tc.title)
			}
		})
	}
}

func TestFoldText(t *testing.T) {
	cases := map[string]string{
		"Contraseña":            "contrasena",
		"CONTRASEÑA":            "contrasena",
		"contraseña":           "contrasena", // NFD (ayrışık) girdi
		"Geldwäsche":            "geldwasche",
		"STRAßE":                "strasse",
		"Schußwaffe":            "schusswaffe",
		"Cartões Clonados":      "cartoes clonados",
		"Lavagem de Dinheiro €": "lavagem de dinheiro €",
		"ЧЁРНЫЙ НАЛ":            "черный нал",
		"Стилер":                "стилер",
		"UYUŞTURUCU":            "uyusturucu",
		"SİLAH":                 "silah",
		"Kredi Kartı":           "kredi karti",
		"Déjà vu":               "deja vu",
	}
	for in, want := range cases {
		if got := foldText(in); got != want {
			t.Errorf("foldText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFoldTokens_SplitsURLsAndPunctuation(t *testing.T) {
	got := foldTokens("http://abc.onion/обнал/карты?q=Contraseña_dump")
	want := []string{"http", "abc", "onion", "обнал", "карты", "q", "contrasena", "dump"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tokens = %v, want %v", got, want)
	}
}

func TestFoldTokens_KeepsCombiningMarksInsideTokens(t *testing.T) {
	if got := foldTokens("Contrasen\u0303a FILTRADA \u0303"); strings.Join(got, "|") != "contrasena|filtrada" {
		t.Fatalf("folded = %v", got)
	}
	if got := len(foldTokens(strings.Repeat("a1 ", maxMultiTokens+10))); got != maxMultiTokens {
		t.Fatalf("token sınırı = %d, want %d", got, maxMultiTokens)
	}
}

func TestParsePattern_RejectsMalformed(t *testing.T) {
	bad := []string{"", "  ", "a", "мет*", "ab*c", "*", "a*b c"}
	for _, p := range bad {
		if _, err := parsePattern(p); err == nil {
			t.Errorf("parsePattern(%q) hata vermeliydi", p)
		}
	}
	good := []string{"обнал*", "слив баз*", "contraseña*", "lavagem de dinheiro", "ak47"}
	for _, p := range good {
		if _, err := parsePattern(p); err != nil {
			t.Errorf("parsePattern(%q) = %v", p, err)
		}
	}
}

func TestCompileMultilingual_UnknownCategory(t *testing.T) {
	_, err := compileMultilingual(categoryRules, map[string]map[string]int{"Yok Böyle": {"test": 5}})
	if err == nil {
		t.Fatal("bilinmeyen kategori hata vermeliydi")
	}
	_, err = compileMultilingual(categoryRules, map[string]map[string]int{"Ransomware": {"test": 0}})
	if err == nil {
		t.Fatal("sıfır ağırlık hata vermeliydi")
	}
}

// Aynı katlanmış desen iki kategoriye birden yazılmamalı (belirsiz sınıflandırma).
func TestMultilingualSignals_NoDuplicatePatterns(t *testing.T) {
	seen := map[string]string{}
	for category, signals := range multilingualSignals {
		for pattern := range signals {
			pat, _ := splitLabel(pattern)
			parts, err := parsePattern(pat)
			if err != nil {
				t.Fatalf("%s: %v", category, err)
			}
			key := partsKey(parts)
			if prev, ok := seen[key]; ok {
				t.Errorf("desen %q hem %q hem %q kategorisinde", pattern, prev, category)
			}
			seen[key] = category
		}
	}
}

func partsKey(parts []multiPart) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.text)
		if p.stem {
			b.WriteByte('*')
		}
		b.WriteByte(' ')
	}
	return b.String()
}

// Eski TR/EN anahtar kelime/ifadesini birebir tanıyan çok dilli sinyal aynı
// kanıtı iki kez puanlar; tablo buna izin vermemeli.
func TestMultilingualSignals_NoLegacyOverlap(t *testing.T) {
	check := func(legacy string) {
		tokens := foldTokens(legacy)
		for _, variants := range multilingual.byFirst {
			for _, v := range variants {
				if len(v.parts) != len(tokens) {
					continue
				}
				all := true
				for j, p := range v.parts {
					if !p.matches(tokens[j]) {
						all = false
						break
					}
				}
				if all {
					t.Errorf("çok dilli sinyal %q eski sinyal %q ile çakışıyor", multilingual.signals[v.signal].name, legacy)
				}
			}
		}
	}
	for _, r := range categoryRules {
		for k := range r.keywords {
			check(k)
		}
		for p := range r.phrases {
			check(p)
		}
	}
}

// Mevcut TR/EN test girdileri yeni motordan hiç puan almamalı: eski davranış
// (kategori, skor, kritiklik, güven) birebir korunur.
func TestAnalyze_LegacyFixturesUnaffected(t *testing.T) {
	fixtures := []string{
		"LockBit ransomware leak site update http://example.onion/ransom lockbit ransomware extortion",
		"Fresh database dump with credentials http://example.onion/leak credential dump database credentials",
		"Corporate RDP access for sale http://example.onion/access rdp admin access rdp access",
		"Welcome page http://example.onion/home news",
		"TC Kimlik ve MERNIS vatandaş sorgu paneli veri sızıntısı http://x.onion/sorgu",
		"Kredi kartı bilgisi satılık - banka hesabı havale http://x.onion/kart",
		"Kiralık katil hizmeti - güvenilir satıcı http://x.onion/market",
		"Türk hacker forumu yeni konu http://x.onion/forum",
		"Trusted vendor marketplace listings http://abc.onion/market/vendor",
		"Welcome to hidden service http://abc.onion/home",
	}
	for _, f := range fixtures {
		if hits := multilingual.scan(foldTokens(f)); len(hits) != 0 {
			names := []string{}
			for id := range hits {
				names = append(names, multilingual.signals[id].name)
			}
			t.Errorf("%q: beklenmeyen çok dilli eşleşme %v", f, names)
		}
	}
}

func TestAnalyze_RussianSignals(t *testing.T) {
	runMLCases(t, []mlCase{
		{"шифровальщик", "Новый шифровальщик: вымогатели требуют выкуп", "http://x.onion/blog", "Ransomware", 5},
		{"слив базы", "Слив базы клиентов банка, утечка данных 2026", "http://x.onion/", "Veri Sızıntısı", 5},
		{"пробив", "Пробив по номеру телефона, пробив паспорта — быстро", "http://x.onion/", "Veri Sızıntısı", 5},
		{"стилер логи", "Свежие логи стилера, пароли и куки", "http://x.onion/", "Veri Sızıntısı", 5},
		{"обнал кардинг", "Обнал карт и кардинг: обналичивание через дропов", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"фишинг", "Фишинг страницы под банки, мошенничество", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"эксплойт", "Продам рабочий эксплойт, уязвимость нулевого дня", "http://x.onion/", "Exploit / Vulnerability", 5},
		{"малварь", "Малварь и троян с кейлоггером", "http://x.onion/", "Exploit / Vulnerability", 5},
		{"доступ", "Продам доступ к корпоративной сети, дедики и шеллы", "http://x.onion/", "Initial Access", 4},
		{"ботнет", "Аренда ботнета, лоадер в комплекте", "http://x.onion/", "Initial Access", 4},
		{"наркотики", "Купить наркотики: кокаин, мефедрон", "http://x.onion/shop", "Illegal Market", 4},
		{"оружие", "Огнестрельное оружие и пистолеты без документов", "http://x.onion/", "Illegal Market", 4},
		{"киллер", "Услуги киллера, заказное убийство", "http://x.onion/", "Illegal Market", 4},
		{"ддос", "ДДоС атака на заказ, стрессер L7", "http://x.onion/", "Illegal Market", 4},
		{"форум", "Хакерский форум — андеграунд", "http://x.onion/", "Siber Forum", 3},
		{"хостинг", "Абузоустойчивый хостинг, сокс5 прокси", "http://x.onion/", "İletişim / Ağ", 2},
		{"büyük harf kiril", "СТИЛЕР ЛОГИ ПРОДАМ", "http://x.onion/", "Veri Sızıntısı", 5},
		{"url yolunda", "Главная", "http://x.onion/обнал/кардинг", "Finansal Dolandırıcılık", 5},
		{"translit", "probiv / obnal service", "http://x.onion/", "Veri Sızıntısı", 5},
	})
}

func TestAnalyze_GermanSignals(t *testing.T) {
	runMLCases(t, []mlCase{
		{"erpressungstrojaner", "Neuer Erpressungstrojaner fordert Lösegeld", "http://x.onion/", "Ransomware", 5},
		{"datenleck", "Datenleck: gestohlene Daten und Zugangsdaten", "http://x.onion/", "Veri Sızıntısı", 5},
		{"geldwäsche", "Kreditkartenbetrug und Geldwäsche", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"geldwaesche ascii", "Geldwaesche Service", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"schwachstelle", "Kritische Sicherheitslücke, Schwachstelle mit Exploit", "http://x.onion/", "Exploit / Vulnerability", 5},
		{"zugang", "RDP-Zugang kaufen, Fernzugriff auf Firmennetz", "http://x.onion/", "Initial Access", 4},
		{"drogen", "Drogen kaufen: Kokain und Betäubungsmittel", "http://x.onion/", "Illegal Market", 4},
		{"betaeubungsmittel", "Betaeubungsmittel Versand", "http://x.onion/", "Illegal Market", 4},
		{"schusswaffe ß", "Schußwaffen und Waffenhandel", "http://x.onion/", "Illegal Market", 4},
		{"falschgeld", "Falschgeld und gefälschte Ausweise", "http://x.onion/", "Illegal Market", 4},
		{"hackerforum", "Deutsches Hackerforum im Untergrund", "http://x.onion/", "Siber Forum", 3},
	})
}

func TestAnalyze_SpanishSignals(t *testing.T) {
	runMLCases(t, []mlCase{
		{"secuestro", "Secuestro de datos y extorsión a empresas", "http://x.onion/", "Ransomware", 5},
		{"contraseña", "Base de datos filtrada: contraseñas y credenciales", "http://x.onion/", "Veri Sızıntısı", 5},
		{"contrasena ascii", "Base de datos filtrada: contrasenas y credenciales", "http://x.onion/", "Veri Sızıntısı", 5},
		{"tarjetas", "Tarjetas robadas y tarjetas clonadas", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"lavado", "Lavado de dinero, estafa garantizada", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"vulnerabilidad", "Vulnerabilidad de día cero con ejecución remota", "http://x.onion/", "Exploit / Vulnerability", 5},
		{"acceso", "Vendo acceso inicial a red corporativa", "http://x.onion/", "Initial Access", 4},
		{"drogas", "Cocaína y drogas con envío discreto", "http://x.onion/", "Illegal Market", 4},
		{"armas", "Armas de fuego y munición", "http://x.onion/", "Illegal Market", 4},
		{"pasaporte", "Pasaportes falsos y dinero falso", "http://x.onion/", "Illegal Market", 4},
		{"sicario", "Sicario disponible", "http://x.onion/", "Illegal Market", 4},
		{"foro", "Foro de hackeo en español", "http://x.onion/", "Siber Forum", 3},
	})
}

func TestAnalyze_PortugueseSignals(t *testing.T) {
	runMLCases(t, []mlCase{
		{"sequestro", "Sequestro de dados: extorsão de empresas", "http://x.onion/", "Ransomware", 5},
		{"vazamento", "Vazamento de dados: senhas e credenciais vazadas", "http://x.onion/", "Veri Sızıntısı", 5},
		{"cartões", "Cartões clonados e lavagem de dinheiro", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"cartoes ascii", "cartoes clonados", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"estelionato", "Estelionato e fraude bancária com conta laranja", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"vulnerabilidade", "Vulnerabilidade dia zero, execução remota de código", "http://x.onion/", "Exploit / Vulnerability", 5},
		{"acesso", "Vendo acesso remoto e acesso inicial", "http://x.onion/", "Initial Access", 4},
		{"maconha", "Maconha e cocaína entrega", "http://x.onion/", "Illegal Market", 4},
		{"arma de fogo", "Arma de fogo e munições", "http://x.onion/", "Illegal Market", 4},
		{"matador", "Matador de aluguel", "http://x.onion/", "Illegal Market", 4},
	})
}

func TestAnalyze_TurkishAndEnglishExtensions(t *testing.T) {
	runMLCases(t, []mlCase{
		{"tr uyuşturucu", "Uyuşturucu ve silah satışı", "http://x.onion/", "Illegal Market", 4},
		{"tr ascii uyusturucu", "UYUSTURUCU satis", "http://x.onion/", "Illegal Market", 4},
		{"tr kara para", "Kara para aklama ve oltalama sayfaları", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"tr zararlı", "Zararlı yazılım ve truva atı", "http://x.onion/", "Exploit / Vulnerability", 5},
		{"tr ilk erişim", "İlk erişim satışı", "http://x.onion/", "Initial Access", 4},
		{"en drugs", "Cocaine, heroin and MDMA shop", "http://x.onion/", "Illegal Market", 4},
		{"en weapons", "Firearms and handguns for sale", "http://x.onion/", "Illegal Market", 4},
		{"en phishing", "Phishing kit with bitcoin mixer", "http://x.onion/", "Finansal Dolandırıcılık", 5},
		{"en malware", "Malware crypter and keylogger", "http://x.onion/", "Exploit / Vulnerability", 5},
		{"en zero day", "Zero-day exploits for sale", "http://x.onion/", "Exploit / Vulnerability", 5},
		{"en infostealer", "Infostealer leaked logs", "http://x.onion/", "Veri Sızıntısı", 5},
	})
}

// Kısa kökler ve eş yazımlı kelimeler alakasız metinde HİÇ eşleşmemeli: kök
// yalnız token başında, tam biçimler yalnız tam token olarak tanınır.
func TestAnalyze_MultilingualFalsePositiveGuards(t *testing.T) {
	cases := []struct{ name, title string }{
		{"мет ≠ метод/метро", "Метод расчёта стоимости проезда в метро"},
		{"крипт ≠ криптовалюта", "Курс криптовалюты и криптовалютные новости"},
		{"скам ≠ скамейка", "Скамейка в парке, новая скамья"},
		{"брут ≠ брутальный", "Брутальный дизайн интерьера"},
		{"доступ ≠ доступный", "Доступный тариф, доступные цены"},
		{"слив ≠ слива", "Слива и сливовый сок, сорт сливы"},
		{"пробив ≠ непробиваемый", "Непробиваемый чехол для телефона"},
		{"кардинг ≠ кардинал", "Кардинал посетил собор"},
		{"обнал ≠ обнаружен", "Обнаружена ошибка в приложении"},
		{"закладки (yer imi)", "Закладки браузера и бутерброд"},
		{"waffen ≠ waffenstillstand", "Waffenstillstand vereinbart"},
		{"heroin ≠ heroine", "The heroine of the story"},
		{"drogas ≠ drogaria", "Drogaria e farmácia 24 horas"},
		{"armas ≠ armazém", "Armazém de móveis"},
		{"munic ≠ municipal", "Biblioteca municipal"},
		{"foro ≠ forofo", "Un forofo del fútbol"},
		{"телеграм ≠ телеграмма", "Телеграмма от мамы, телеграммы и телеграмму"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if hits := multilingual.scan(foldTokens(tc.title)); len(hits) != 0 {
				t.Fatalf("%q: beklenmeyen eşleşme %v", tc.title, hits)
			}
			got := Analyze(tc.title, "http://x.onion/", "", nil, 0)
			if got.Category != "Genel" {
				t.Fatalf("%q: category = %q, want Genel (signals=%v)", tc.title, got.Category, got.MatchedSignals)
			}
		})
	}
}

// Çok anlamlı kelimeler yalnız destekleyici sinyaldir: tek başına, TEKRAR
// ettiğinde ya da sorgu olarak da geçtiğinde sınıflandırma üretmez; metin
// "Genel" kalır (zayıf tehdit etiketi de almaz).
func TestAnalyze_AmbiguousWordsStayBelowThreshold(t *testing.T) {
	cases := []struct{ title, query string }{
		{"Выкуп авто в любом состоянии", ""},
		{"Golpe de estado en la historia", ""},
		{"Filtración de agua en casa", ""},
		{"Глушитель для автомобиля", ""},
		{"Слив воды в ванной", ""},
		// tekrar + sorgu: eskiden 3×2 = 6 ile eşiği aşıyordu
		{"Выкуп авто", "выкуп"},
		{"Срочный выкуп авто — выкуп автомобилей", ""},
		{"Golpe de estado: el golpe militar", ""},
		{"Слив воды, слив в ванной", "слив"},
		{"Filtración de agua, filtración en el techo", "filtración"},
		{"Глушитель, глушитель, глушитель", ""},
		{"La heroína de la película, la heroína del pueblo", ""},
		{"Hitman 3 download — Hitman", "hitman"},
	}
	for _, tc := range cases {
		got := Analyze(tc.title, "http://x.onion/", tc.query, nil, 0)
		if got.Score != 0 || got.Criticality != 1 || got.Category != "Genel" {
			t.Errorf("%q (q=%q): %s score=%d criticality=%d, want Genel 0/1 (signals=%v)", tc.title, tc.query, got.Category, got.Score, got.Criticality, got.MatchedSignals)
		}
	}
}

// Destekleyici sinyal aynı kuralda başka bir kanıt varsa puana katılır.
func TestAnalyze_SupportSignalsBoostOtherEvidence(t *testing.T) {
	cases := []struct {
		title, wantCat, wantSignal string
	}{
		{"Ransom and выкуп", "Ransomware", "выкуп"},              // eski 'ransom' (4) + выкуп (3)
		{"Слив воды и дамп", "Veri Sızıntısı", "слив"},           // дамп (5) + слив (3)
		{"Heroína y cocaína", "Illegal Market", "heroína"},       // cocaína (7) + heroína (3)
		{"DDoS for hire — cheap DDoS", "Illegal Market", "ddos"}, // ddos for hire (7) + ddos (3)
		{"ДДоС атака на заказ, ддос", "Illegal Market", "ддос"},  // ддос на заказ (7) + ддос (3)
		{"Hitman for hire, professional hitman", "Illegal Market", "hitman"},
	}
	for _, tc := range cases {
		got := Analyze(tc.title, "", "", nil, 0)
		if got.Category != tc.wantCat {
			t.Errorf("%q: category = %q, want %q (score=%d signals=%v)", tc.title, got.Category, tc.wantCat, got.Score, got.MatchedSignals)
			continue
		}
		found := false
		for _, s := range got.MatchedSignals {
			found = found || s == tc.wantSignal
		}
		if !found {
			t.Errorf("%q: destekleyici %q sinyallerde yok: %v", tc.title, tc.wantSignal, got.MatchedSignals)
		}
	}
	// Destekleyici sinyal tekrar etse de bir kez sayılır.
	once := Analyze("cocaína heroína", "", "", nil, 0)
	many := Analyze("cocaína heroína heroína heroína", "", "", nil, 0)
	if once.Score != many.Score {
		t.Fatalf("destekleyici tekrar puanı artırdı: %d → %d", once.Score, many.Score)
	}
}

// Eşik altı sonuçta kategori adı yalnız eski TR/EN puanlarından gelir: çok
// dilli sinyaller zayıf tehdit etiketi üretmez, eski zayıf etiket davranışı
// ise aynen korunur.
func TestAnalyze_SubThresholdMultilingualStaysGeneral(t *testing.T) {
	for _, title := range []string{
		"Доступ к сайту",
		"Телеграмма от мамы",
		"Weapons of mass destruction",
		"Pistoleiro",
		"Foro privilegiado no STF",
		"Parol",
		"Пистолет Макарова в музее",
	} {
		got := Analyze(title, "", "", nil, 0)
		if got.Category != "Genel" || got.Score != 0 || got.Criticality != 1 {
			t.Errorf("%q: %s score=%d crit=%d, want Genel", title, got.Category, got.Score, got.Criticality)
		}
	}
	// Eski zayıf etiket (eşik altı 'admin' → Initial Access) çok dilli bir
	// sinyal başka kuralda daha yüksek puan alsa bile değişmez.
	legacyOnly := Analyze("admin panel", "", "", nil, 0)
	mixed := Analyze("admin panel пистолет", "", "", nil, 0)
	if legacyOnly.Category != "Initial Access" || mixed.Category != legacyOnly.Category || mixed.Score != 0 {
		t.Fatalf("legacy=%+v mixed=%+v", legacyOnly, mixed)
	}
}

func TestAnalyze_AccentAndCaseEquivalence(t *testing.T) {
	pairs := [][2]string{
		{"Contraseñas filtradas y credenciales", "CONTRASENAS FILTRADAS Y CREDENCIALES"},
		{"Geldwäsche Kreditkartenbetrug", "geldwaesche kreditkartenbetrug"},
		{"Cartões clonados", "cartoes clonados"},
		{"Чёрный нал, обнал", "черный нал, ОБНАЛ"},
		{"Uyuşturucu satışı", "uyusturucu satisi"},
	}
	for _, p := range pairs {
		a := Analyze(p[0], "", "", nil, 0)
		b := Analyze(p[1], "", "", nil, 0)
		if a.Category != b.Category || a.Score != b.Score {
			t.Errorf("%q → %s/%d, %q → %s/%d", p[0], a.Category, a.Score, p[1], b.Category, b.Score)
		}
		if a.Category == "Genel" {
			t.Errorf("%q: kategori bulunamadı", p[0])
		}
	}
}

// Sinyaller tablodaki SABİT etiket adıyla raporlanır; kesik kök etikete düşmez.
func TestAnalyze_MultilingualSignalNames(t *testing.T) {
	got := Analyze("Contraseñas y CREDENCIALES, утечки данных", "", "", nil, 0)
	want := map[string]bool{"contraseña": true, "credenciales": true, "утечка-данных": true}
	for _, s := range got.MatchedSignals {
		delete(want, s)
	}
	if len(want) != 0 {
		t.Fatalf("matched = %v, eksik %v", got.MatchedSignals, want)
	}
}

func TestAnalyze_MultilingualHitsAreCapped(t *testing.T) {
	once := Analyze("стилер", "", "", nil, 0)
	many := Analyze(strings.Repeat("стилер ", 50), "", "", nil, 0)
	if many.Score != once.Score*3 {
		t.Fatalf("score = %d, want %d (adet 3 ile sınırlı)", many.Score, once.Score*3)
	}
}

func TestMergeTags_MultilingualQueryTokens(t *testing.T) {
	merged := MergeTags(nil, nil, "пробив обналичивание погода probiv", 5)
	got := strings.Join(merged, ",")
	if got != "пробив,обнал" {
		t.Fatalf("merged = %q, want пробив,обнал", got)
	}
}

// Sorgu tokenı (başlıktan da gelebilir: tagging motoru sorgu yoksa başlığı
// kullanır) yalnız TEK kelimeyse ve bir sinyalse etiket olur; ham token değil
// sinyalin sabit adı eklenir. URL/onion ekli, görünmez harfli ya da
// destekleyici/benzer kelimeler etikete dönüşmez.
func TestMergeTags_RejectsHostileQueryTokens(t *testing.T) {
	cases := []struct{ query, want string }{
		{"extorsion.evil.onion/x", ""},
		{"обнал-evil.onion", ""},
		{"stealer.ru/logs стилер_x", ""},
		{"обналㅤㅤ обнал", "обнал"}, // U+3164 dolgu harfi: tek etiket, sahte kopya yok
		{"телеграмма телеграмму", ""},
		{"выкуп слив golpe ddos hitman", ""},
		{"Contrasenas", "contraseña"},
		{"Geldwaesche", "geldwäsche"},
	}
	for _, tc := range cases {
		got := strings.Join(MergeTags(nil, nil, tc.query, 10), ",")
		if got != tc.want {
			t.Errorf("MergeTags(q=%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

// Aynı sinyalin çekimleri, aksanlı/aksansız ya da umlaut/ae yazımları ve
// transliterasyonu TEK etiket üretir; ?tag= süzgeci hepsini bulur.
func TestAnalyze_CanonicalSignalNames(t *testing.T) {
	groups := []struct {
		want   string
		titles []string
	}{
		{"ботнет", []string{"Продаю ботнет", "Аренда ботнета", "Связь с ботнету"}},
		{"geldwäsche", []string{"Geldwäsche Service", "Geldwaesche Service", "GELDWÄSCHE"}},
		{"утечка-данных", []string{"Утечка данных", "утечки данных клиентов", "утечку данных"}},
		{"пробив", []string{"пробив", "пробивка", "probiv", "ПРОБИВА"}},
		{"contraseña", []string{"contraseñas", "contrasenas", "CONTRASEÑA"}},
		{"обнал", []string{"обнал", "обналичивание", "obnal"}},
		{"вымогатель", []string{"вымогатели", "vymogatel"}},
		{"доступ", []string{"продам доступы доступы", "доступа нет доступа"}},
	}
	for _, g := range groups {
		for _, title := range g.titles {
			// İki kez: tek geçişte eşik altı kalan (ağırlık 4-5) sinyaller de raporlansın.
			got := Analyze(title+" "+title, "", "", nil, 0)
			if strings.Join(got.MatchedSignals, ",") != g.want {
				t.Errorf("%q: signals = %v, want yalnız %q", title, got.MatchedSignals, g.want)
			}
		}
	}
}

// Kiril başlık + transliterasyonlu URL aynı sinyalin iki geçişidir: tek etiket
// ve tek sayaç (adet sınırı ortak).
func TestAnalyze_TransliterationIsAliasNotSecondSignal(t *testing.T) {
	got := Analyze("Свежий пробив по базам ФНС", "http://x.onion/thread/probiv", "", nil, 0)
	n := 0
	for _, s := range got.MatchedSignals {
		if s == "probiv" {
			t.Fatalf("transliterasyon ayrı etiket üretti: %v", got.MatchedSignals)
		}
		if s == "пробив" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("signals = %v, want tek пробив", got.MatchedSignals)
	}
	capped := Analyze("пробив пробива пробивка probiv probiv", "", "", nil, 0)
	if capped.Score != 7*maxHitsPerSignal {
		t.Fatalf("score = %d, want %d (varyantlar ortak sayaçta)", capped.Score, 7*maxHitsPerSignal)
	}
}

func TestCompileMultilingual_LabelValidation(t *testing.T) {
	bad := []map[string]map[string]int{
		// aynı etiket iki kategoride
		{"Ransomware": {"foo|ortak": 5}, "Veri Sızıntısı": {"bar|ortak": 5}},
		// aynı etiket farklı ağırlık
		{"Ransomware": {"foo|ortak": 5, "bar|ortak": 6}},
		// aynı etiket kelime + ifade karışık
		{"Ransomware": {"foo|ortak": 5, "bar baz|ortak": 5}},
		// geçersiz etiketler
		{"Ransomware": {"foo|": 5}},
		{"Ransomware": {"foo|a*": 5}},
		// başka kategorinin eski sinyal adı
		{"Ransomware": {"foo|carding": 5}},
	}
	for i, table := range bad {
		if _, err := compileMultilingual(categoryRules, table); err == nil {
			t.Errorf("tablo %d hata vermeliydi: %v", i, table)
		}
	}
	m, err := compileMultilingual(categoryRules, map[string]map[string]int{"Ransomware": {"foo|ortak": 5, "bar|ortak": 5, "ransom|ransom": 4}})
	if err != nil || len(m.signals) != 2 {
		t.Fatalf("geçerli tablo: err=%v signals=%d", err, len(m.signals))
	}
}

func BenchmarkAnalyze_Multilingual(b *testing.B) {
	title := "Слив базы клиентов банка — Datenleck, contraseñas filtradas, vazamento de dados, LockBit ransomware"
	url := "http://abcdefghijklmnopqrstuvwxyz234567abcdefghijklmnopqrstuv.onion/forum/thread/12345"
	for b.Loop() {
		Analyze(title, url, "leak", []string{"database", "dump"}, 3)
	}
}

// Bare "DDoS" genellikle koruma/anti-DDoS sayfasıdır; yalnız kiralık saldırı
// ifadeleri Illegal Market üretir.
func TestAnalyze_DDoSProtectionIsNotIllegalMarket(t *testing.T) {
	for _, title := range []string{
		"DDoS protection",
		"DDoS-Guard protection - checking your browser",
		"Anti-DDoS service",
		"Защита от ДДоС-атак",
		"DDoS DDoS DDoS",
	} {
		got := Analyze(title, "http://x.onion/", "ddos", nil, 0)
		if got.Category != "Genel" || got.Score != 0 {
			t.Errorf("%q: %s score=%d signals=%v, want Genel", title, got.Category, got.Score, got.MatchedSignals)
		}
	}
	if tags := MergeTags(nil, nil, "DDoS protection", 10); len(tags) != 0 {
		t.Errorf("ddos sorgusu etiket üretti: %v", tags)
	}
	runMLCases(t, []mlCase{
		{"ddos for hire", "DDoS-for-hire service", "http://x.onion/", "Illegal Market", 4},
		{"ddos as a service", "DDoS as a service, booter panel", "http://x.onion/", "Illegal Market", 4},
		{"заказать ддос", "Заказать ДДоС недорого", "http://x.onion/", "Illegal Market", 4},
		{"аренда ддос", "Аренда ддос-ботнета", "http://x.onion/", "Illegal Market", 4},
	})
}

// Eşik üstü tek kelimelik sinyallerin yaygın masum anlamları.
func TestAnalyze_CommonMeaningsAreNotThreats(t *testing.T) {
	for _, title := range []string{
		"La heroína de la película",
		"Hitman 3 download",
		"Foro privilegiado no STF",
		"Телеграмма от мамы",
	} {
		got := Analyze(title, "", "", nil, 0)
		if got.Category != "Genel" {
			t.Errorf("%q: %s score=%d signals=%v, want Genel", title, got.Category, got.Score, got.MatchedSignals)
		}
	}
	runMLCases(t, []mlCase{
		{"heroína pura", "Heroína pura, envío discreto", "http://x.onion/", "Illegal Market", 4},
		{"hire a hitman", "Hire a hitman", "http://x.onion/", "Illegal Market", 4},
		{"foro privado", "Foro privado", "http://x.onion/", "Siber Forum", 3},
		{"телеграм", "Пиши в телеграм, телеграме отвечу", "http://x.onion/", "İletişim / Ağ", 2},
	})
}
