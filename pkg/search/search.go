package search

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"keywordhunter-mvp/pkg/logger"
	"keywordhunter-mvp/pkg/shared"
)

// Result arama sonucu
type Result struct {
	Title       string
	URL         string
	Source      string // Hangi arama motorundan geldi
	Criticality int
	Category    string
	KeywordHits int    // Arama kelimesinin bu sonuçta kaç kez geçtiği
}

// Searcher dark web arama yapan yapı
type Searcher struct {
	torProxy string
	client   *http.Client
}

// Arama motorlarının kendi domain'leri - bunları sonuçlardan çıkacak
var searchEngineDomains = []string{
	"juhanurmihxlp77nkq76byazcldy2hlmovfu2epvl5ankdibsot4csyd.onion", // Ahmia
	"3bbad7fauom4d6sgppalyqddsqbf5u5p56b5k5uk2zxsy3d6ey2jobad.onion", // OnionLand
	"darkhuntyla64h75a3re5e2l3367lqn7ltmdzpgmr6b4nbz3q2iaxrid.onion", // DarkHunt
	"iy3544gmoeclh5de6gez2256v6pjh4omhpqdh2wpeeppjtvqmjhkfwad.onion", // Torgle
	"amnesia7u5odx5xbwtpnqk3edybgud5bmiagu75bnqx2crntw5kry7ad.onion", // Amnesia
	"kaizerwfvp5gxu6cppibp7jhcqptavq3iqef66wbxenh6a2fklibdvid.onion", // Kaizer
	"anima4ffe27xmakwnseih3ic2y7y3l6e7fucwk4oerdn4odf7k74tbid.onion", // Anima
	"tornadoxn3viscgz647shlysdy7ea5zqzwda7hierekeuokh5eh5b3qd.onion", // Tornado
	"tornetupfu7gcgidt33ftnungxzyfq2pygui5qdoyss34xbgx2qruzid.onion", // TorNet
	"torlbmqwtudkorme6prgfpmsnile7ug2zm4u3ejpcncxuhpu4k2j4kyd.onion", // Torland
	"findtorroveq5wdnipkaojfpqulxnkhblymc7aramjzajcvpptd4rjqd.onion", // FindTor
	"2fd6cemt4gmccflhm6imvdfvli3nf7zn6rfrwpsy7uhxrgbypvwf5fad.onion", // Excavator
	"oniwayzz74cv2puhsgx4dpjwieww4wdphsydqvf5q7eyz4myjvyw26ad.onion", // Onionway
	"tor66sewebgixwhcqfnp5inzp5x5uohhdy3kvtnyfxc2e5mxiuh34iid.onion", // Tor66
	"3fzh7yuupdfyjhwt3ugzqqof6ulbcl27ecev33knxe3u7goi3vfn2qqd.onion", // OSS
	"torgolnpeouim56dykfob6jh5r2ps2j73enc42s2um4ufob3ny4fcdyd.onion", // Torgol
	"searchgf7gdtauh7bhnbyed4ivxqmuoat3nm6zfrg3ymkq6mtnpye3ad.onion", // DeepSearches
}

// cleanURL URL'deki boşlukları ve geçersiz karakterleri temizler
func cleanURL(urlStr string) string {
	// Boşlukları kaldır
	urlStr = strings.ReplaceAll(urlStr, " ", "")
	urlStr = strings.ReplaceAll(urlStr, "\t", "")
	urlStr = strings.ReplaceAll(urlStr, "\n", "")
	urlStr = strings.ReplaceAll(urlStr, "\r", "")
	return strings.TrimSpace(urlStr)
}

// isValidResultURL URL'in geçerli bir sonuç olup olmadığını kontrol eder
func isValidResultURL(urlStr string) bool {
	// Önce URL'yi temizle
	urlStr = cleanURL(urlStr)
	lowerURL := strings.ToLower(urlStr)

	// 0. URL'de boşluk varsa geçersiz
	if strings.Contains(urlStr, " ") {
		return false
	}

	// 1. Arama motoru domain'lerini kontrol et
	for _, domain := range searchEngineDomains {
		if strings.Contains(lowerURL, domain) {
			return false
		}
	}

	// 2. Shared paketten düşük değerli URL kontrolü
	isLow, _ := shared.IsLowValueURL(urlStr)
	if isLow {
		return false
	}

	// 3. Çok kısa URL'leri atla (sadece domain)
	// http://xxx.onion veya http://xxx.onion/ gibi
	onionIdx := strings.Index(lowerURL, ".onion")
	if onionIdx > 0 {
		afterOnion := lowerURL[onionIdx+6:]
		// Sadece / veya boş ise, ana sayfa - bunlar genelde index sayfaları
		if afterOnion == "" || afterOnion == "/" {
			// Ana sayfalar bazı durumlarda değerli olabilir, geçir
			return true
		}
	}

	return true
}

// New yeni Searcher oluşturur - shared paketi kullanır
func New(torProxy string) (*Searcher, error) {
	client, err := shared.NewHTTPClient(torProxy)
	if err != nil {
		return nil, fmt.Errorf("HTTP client oluşturma hatası: %w", err)
	}

	// Arama için özel timeout ayarla
	client.Timeout = shared.SearchTimeout

	logger.Info("Searcher initialized (timeout: %v)", shared.SearchTimeout)

	return &Searcher{
		torProxy: torProxy,
		client:   client,
	}, nil
}

// SearchAll tüm arama motorlarında arama yapar
func (s *Searcher) SearchAll(query string) []Result {
	var results []Result
	var mu sync.Mutex
	var wg sync.WaitGroup
	startTime := time.Now()

	logger.SearchStarted(query, len(SearchEngines))

	for _, engine := range SearchEngines {
		wg.Add(1)
		go func(eng Engine) {
			defer wg.Done()

			// Broadcast start
			shared.Streamer.BroadcastLog("engine_start", "Starting search...", eng.Name)

			engineResults, err := s.searchEngine(eng, query)
			if err != nil {
				logger.SearchEngineResult(eng.Name, 0, err)
				shared.Streamer.BroadcastLog("engine_end", fmt.Sprintf("SEARCH ENGINE ERROR: %v", err), eng.Name)
				return
			}

			if len(engineResults) > 0 {
				// Toplam hit sayısını hesapla
				totalHits := 0
				for _, r := range engineResults {
					totalHits += r.KeywordHits
				}
				logger.SearchEngineResult(eng.Name, len(engineResults), nil)
				shared.Streamer.BroadcastLog("engine_end", fmt.Sprintf("✅ %d sonuç, %d hit bulundu", len(engineResults), totalHits), eng.Name)
				mu.Lock()
				results = append(results, engineResults...)
				mu.Unlock()
			} else {
				logger.SearchEngineResult(eng.Name, 0, nil)
				shared.Streamer.BroadcastLog("engine_end", "SEARCH ENGINE SUCCESS: 0 results found", eng.Name)
			}
		}(engine)
	}

	wg.Wait()

	// Duplicate URL'leri kaldır
	deduped := s.deduplicate(results)

	logger.SearchCompleted(query, len(deduped), time.Since(startTime))
	shared.Streamer.BroadcastLog("success", fmt.Sprintf("SEARCH COMPLETED: %d unique results found in %v", len(deduped), time.Since(startTime).Round(time.Millisecond)), "")

	return deduped
}

// searchEngine tek bir arama motorunda arama yapar - retry ile
func (s *Searcher) searchEngine(engine Engine, query string) ([]Result, error) {
	// URL'yi oluştur
	searchURL := strings.Replace(engine.URL, "{query}", url.QueryEscape(query), 1)

	// HTTP request oluştur
	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", shared.RandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	logger.Debug("ENGINE REQUEST: %s URL: %s", engine.Name, searchURL)

	// DoWithRetry kullan - exponential backoff ile
	resp, err := shared.DoWithRetry(s.client, req)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", engine.Name, shared.ClassifyError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		logger.Warn("ENGINE HTTP STATUS: %s returned %d", engine.Name, resp.StatusCode)
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Body'yi oku
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// HTML'den .onion linklerini çıkar ve kelime sıklığını say
	return s.parseResults(string(body), engine.Name, query), nil
}

// parseResults HTML'den .onion linklerini çıkarır ve kelime sıklığını sayar
func (s *Searcher) parseResults(html, sourceName, query string) []Result {
	var results []Result
	queryLower := strings.ToLower(query)
	htmlLower := strings.ToLower(html)

	// <a> taglarını bul - iç HTML dahil (nested taglar için)
	// Önce tüm <a> bloklarını bul
	linkRegex := regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	matches := linkRegex.FindAllStringSubmatch(html, -1)

	// .onion URL pattern
	onionRegex := regexp.MustCompile(`https?://[^/]*\.onion[^\s"'<>]*`)

	// HTML tag temizleme regex
	htmlTagRegex := regexp.MustCompile(`<[^>]*>`)
	for _, match := range matches {
		if len(match) >= 3 {
			href := match[1]
			innerHTML := match[2]
			onionURLs := onionRegex.FindAllString(href, -1)
			if len(onionURLs) > 0 {
				foundURL := cleanURL(onionURLs[0])
				if !isValidResultURL(foundURL) {
					continue
				}
				title := cleanTitle(decodeHTMLEntities(htmlTagRegex.ReplaceAllString(innerHTML, "")), foundURL)
				// Kelime sıklığını say (title + innerHTML içinde)
				hits := strings.Count(strings.ToLower(innerHTML), queryLower)
				hits += strings.Count(strings.ToLower(title), queryLower)
				res := Result{
					Title:       title,
					URL:         foundURL,
					Source:      sourceName,
					KeywordHits: hits,
				}
				res.PredictCTI()
				results = append(results, res)
			}
		}
	}

	// Ayrıca düz .onion URL'leri de tara (href dışında olanlar)
	allOnions := onionRegex.FindAllString(html, -1)
	seen := make(map[string]bool)
	for _, r := range results {
		seen[r.URL] = true
	}

	for _, onionURL := range allOnions {
		// ✅ URL filtreleme - düşük değerli URL'leri kaydetme
		if !seen[onionURL] && !strings.Contains(onionURL, "javascript:") && isValidResultURL(onionURL) {
			// URL çevresinde kelime geçiyor mu kontrol et (context-aware)
			hits := 0
			urlIdx := strings.Index(htmlLower, strings.ToLower(onionURL))
			if urlIdx > 0 {
				// URL öncesi ve sonrasını al (max 200 karakter)
				start := max(0, urlIdx-200)
				end := min(len(htmlLower), urlIdx+len(onionURL)+200)
				context := htmlLower[start:end]
				hits = strings.Count(context, queryLower)
			}
			res := Result{
				Title:       extractTitleFromURL(onionURL),
				URL:         onionURL,
				Source:      sourceName,
				KeywordHits: hits,
			}
			res.PredictCTI()
			results = append(results, res)
			seen[onionURL] = true
		}
	}

	return results
}

// ctiRule sınıflandırma kuralı
type ctiRule struct {
	keywords    []string
	category    string
	criticality int
	weight      int // Ağırlık: bu kategori diğerinden daha güçlü mü?
}

// ctiRules CTI sınıflandırma kuralları — çok dilli (EN + RU + DE + ES/PT)
// Dark web dil dağılımı: İngilizce > Rusça > Almanca > İspanyolca/Portekizce
// Yüksek weight = daha güçlü eşleşme, düşük weight üzerine yazar
var ctiRules = []ctiRule{

	// ════════════════════════════════════════════════════════════
	// KRİTİKLİK 5 — En tehlikeli kategoriler
	// ════════════════════════════════════════════════════════════

	{
		// EN + RU: вымогатель / шифровальщик / ransomware grupları
		keywords: []string{
			// İngilizce
			"ransomware", "lockbit", "blackcat", "alphv", "hive", "conti", "clop", "revil",
			"darkside", "ragnar", "medusa", "akira", "bl00dy", "royal", "play ransomware",
			"ransom", "decrypt files", "decryptor", "victim blog", "data leak site",
			"rhysida", "blackbasta", "blackmatter", "avoslocker", "grief", "cuba ransomware",
			"lorenz", "maze", "ryuk", "sodinokibi", "egregor",
			// Rusça (Kiril)
			"вымогатель", "шифровальщик", "выкуп", "расшифровка", "шифрование файлов",
			"программа-вымогатель", "заблокированы файлы", "декриптор",
			// Rusça transliterasyon (Ru underground forumlarda Latin kullanımı)
			"vymogatel", "shifrovalshhik", "vikup",
		},
		category:    "Ransomware / Extortion",
		criticality: 5,
		weight:      100,
	},

	{
		// EN + RU: эксплойт / 0день / уязвимость
		keywords: []string{
			// İngilizce
			"0day", "zero-day", "zero day", "zeroday", "rce exploit", "lpe exploit", "cve-",
			"critical exploit", "poc exploit", "nday", "n-day", "weaponized", "working exploit",
			"exploit kit", "privesc", "privilege escalation", "remote code execution",
			"arbitrary code", "heap spray", "use-after-free", "buffer overflow exploit",
			// Rusça
			"эксплойт", "уязвимость", "нулевой день", "0день", "удалённое выполнение",
			"эскалация привилегий", "рабочий эксплойт", "критическая уязвимость",
			// Almanca
			"exploit", "sicherheitslücke", "schwachstelle", "zero-day exploit",
			// İspanyolca
			"exploit", "vulnerabilidad crítica", "día cero",
		},
		category:    "Exploit / 0day",
		criticality: 5,
		weight:      95,
	},

	{
		// EN + RU: утечка / слив / база данных
		keywords: []string{
			// İngilizce
			"database dump", "db dump", "sql dump", "full dump", "combolist", "combo list",
			"credential dump", "passwd dump", "password dump", "breach data", "leaked database",
			"data breach", "user dump", "email dump", "customer data", "corporate data leak",
			"confidential data", "stolen data", "hacked database", "data leak", "pwned",
			"credentials leaked", "logs stealer", "fresh logs",
			// Rusça
			"утечка данных", "слив базы", "слив данных", "база данных", "дамп базы",
			"пробив", "пробивка", "слитая база", "украденные данные", "взломанная база",
			"персональные данные", "утекли данные", "инсайд",
			// Rusça (yaygın kısaltma/argo)
			"пробив бд", "слив бд", "бд слив",
			// Almanca
			"datenleck", "datenpanne", "datendiebstahl", "gestohlene daten", "datenbankdump",
			// İspanyolca/Portekizce
			"datos filtrados", "base de datos robada", "vazamento de dados", "banco de dados vazado",
		},
		category:    "Veri Sızıntısı",
		criticality: 5,
		weight:      90,
	},

	{
		// EN + RU: кардинг / обнал / дропы — Rus underground'ın kalbi
		keywords: []string{
			// İngilizce
			"carding", "cc dump", "cvv shop", "fullz", "dumps+pin", "track 1", "track 2",
			"skimmer", "atm skimmer", "bank account", "cashout service", "money mule",
			"wire transfer", "swift", "paypal hack", "cloned card", "credit card shop",
			"debit card", "dumps shop", "bins", "non-vbv", "verified by visa bypass",
			// Rusça — обнал (nakit çevirme) Rus dw'ın kendine özgü terimleri
			"кардинг", "обнал", "обналичивание", "нальщик", "дроп", "дропы",
			"карты", "cc shop", "фулл", "фуллз", "дамп карты", "банковский аккаунт",
			"обнал крипты", "чёрный нал", "прогон денег", "мул",
			// Almanca
			"kreditkartenbetrug", "kartendump", "bankbetrug", "geldwäsche",
			// İspanyolca/Portekizce
			"carding", "tarjetas robadas", "cartões clonados", "lavagem de dinheiro",
			"fraude bancário", "fraude tarjeta",
		},
		category:    "Finansal Dolandırıcılık",
		criticality: 5,
		weight:      88,
	},

	{
		// EN + RU: малварь / ботнет / стилер — Rus/EN underground paylaşımları
		keywords: []string{
			// İngilizce
			"botnet", "c2 panel", "c&c server", "command and control", "rat panel",
			"loader", "stealer", "infostealer", "redline", "raccoon stealer", "vidar",
			"azorult", "formbook", "agent tesla", "njrat", "darkcomet", "remcos",
			"asyncrat", "dcrat", "malware source", "crypter", "fud crypter", "packer",
			"binder", "clipper", "grabber", "hvnc", "reverse shell", "cobalt strike",
			"metasploit beacon", "mythic", "brute ratel",
			// Rusça — стилер, лоадер, крипт — Rus forum terminolojisi
			"малварь", "малвара", "малвер", "ботнет", "стилер", "стилеры", "лоадер",
			"крипт", "криптор", "фуд", "ратник", "рат", "бэкдор", "трояны", "троян",
			"кейлоггер", "граббер", "клиппер", "пробивщик", "чекер", "брутер",
			"брутфорс", "хвнц", "vnc хак", "купить стилер", "аренда ботнета",
			// Almanca
			"trojaner", "schadsoftware", "fernzugriff", "keylogger",
		},
		category:    "Malware / Botnet",
		criticality: 5,
		weight:      92,
	},

	{
		// EN + RU: APT / государственный актор / кибершпионаж
		keywords: []string{
			// İngilizce
			"apt", "nation state", "government hack", "military leak", "classified document",
			"intelligence leak", "nsa", "cia", "fbi leak", "state actor", "cyber espionage",
			"supply chain attack", "solarwinds", "hafnium", "fancy bear", "cozy bear",
			"lazarus group", "equation group", "turla", "sandworm", "apt28", "apt29",
			"apt41", "charcoal typhoon", "volt typhoon",
			// Rusça
			"государственная атака", "кибершпионаж", "секретные документы",
			"разведывательная операция", "военная утечка", "фсб", "гру атака",
			// Almanca
			"staatliche hacker", "geheimdienstangriff", "staatsspionage",
		},
		category:    "APT / Devlet Aktörü",
		criticality: 5,
		weight:      98,
	},

	// ════════════════════════════════════════════════════════════
	// KRİTİKLİK 4
	// ════════════════════════════════════════════════════════════

	{
		// EN + RU: DDoS — ддос, стресер
		keywords: []string{
			// İngilizce
			"ddos service", "ddos for hire", "booter", "stresser", "ddos attack",
			"layer7", "layer4 ddos", "http flood", "syn flood", "amplification attack",
			"udp flood", "ntp amplification", "dns amplification",
			// Rusça
			"ддос", "ддос атака", "ддосим", "стрессер", "бутер", "флуд сервер",
			"купить ддос", "аренда ддос", "http флуд", "l7 атака", "l4 атака",
			// Almanca
			"ddos angriff", "ddos dienst",
			// İspanyolca
			"ataque ddos", "servicio ddos",
		},
		category:    "DDoS Hizmeti",
		criticality: 4,
		weight:      75,
	},

	{
		// EN + RU: фишинг / скам / развод
		keywords: []string{
			// İngilizce
			"phishing kit", "phishing panel", "phishing page", "scam page", "fake login",
			"credential harvester", "evilginx", "modlishka", "phishing-as-a-service",
			"webmail phishing", "spear phishing", "smishing kit", "vishing script",
			// Rusça
			"фишинг", "фишинг кит", "фишинговая страница", "скам", "развод",
			"схема развода", "схема", "мошенничество", "кидалово", "лохотрон",
			"поддельная страница", "угон аккаунтов", "кража аккаунтов",
			// Almanca
			"phishing", "betrug seite", "gefälschte login",
			// İspanyolca/Portekizce
			"phishing", "fraude", "estafa", "golpe", "página falsa",
		},
		category:    "Phishing / Scam",
		criticality: 4,
		weight:      74,
	},

	{
		// EN + RU: продажа доступа / начальный доступ
		keywords: []string{
			// İngilizce
			"initial access", "access broker", "rdp access", "vpn access", "corporate access",
			"shell access", "webshell", "ssh access", "domain admin", "network access",
			"selling access", "buy access", "citrix access", "outlook access", "exchange access",
			"root access", "admin access", "panel access",
			// Rusça — продам доступ, рут шелл, rdp
			"продам доступ", "куплю доступ", "рут доступ", "рут шелл", "шелл",
			"веб шелл", "rdp доступ", "rdp сервер", "доступ к серверу",
			"корпоративный доступ", "vpn доступ", "домен администратор",
			"продам rdp", "арендую rdp", "фреш rdp",
			// Almanca
			"zugang kaufen", "rdp zugang", "shell zugang",
		},
		category:    "İlk Erişim Satışı",
		criticality: 4,
		weight:      85,
	},

	{
		// EN + RU + DE: наркотики / Drogen / drogas
		keywords: []string{
			// İngilizce
			"drugs", "cocaine", "heroin", "meth", "mdma", "fentanyl", "lsd", "cannabis",
			"weed", "narcotics", "pills", "xanax", "oxycodone", "ketamine", "psychedelics",
			"drug shop", "drug market", "amphetamine", "speed", "crystal meth",
			"2cb", "ecstasy", "shrooms", "mushrooms", "opioids", "tramadol",
			// Rusça
			"наркотики", "наркота", "героин", "кокаин", "метамфетамин", "мефедрон",
			"мет", "спайс", "скорость амф", "экстази", "каннабис", "трава", "ганджубас",
			"купить наркотики", "продам наркотики", "закладки", "закладчик", "соль наркотик",
			"альфа пвп", "лсд", "грибы", "псилоцибин", "фентанил",
			// Almanca
			"drogen", "drogenmarkt", "kokain", "amphetamin", "heroin kaufen",
			"cannabis kaufen", "mdma kaufen", "betäubungsmittel",
			// İspanyolca/Portekizce
			"drogas", "cocaína", "heroína", "anfetamina", "cannabis comprar",
		},
		category:    "Uyuşturucu Market",
		criticality: 4,
		weight:      70,
	},

	{
		// EN + RU + DE: оружие / Waffen / armas
		keywords: []string{
			// İngilizce
			"weapons", "guns", "pistol", "rifle", "ammunition", "silencer", "ar-15",
			"glock", "ak47", "firearms", "illegal weapons", "arms dealer", "gun shop",
			"fully automatic", "suppressor", "ghost gun", "unregistered firearm",
			// Rusça
			"оружие", "огнестрельное оружие", "пистолет", "автомат", "ак47",
			"купить оружие", "продам оружие", "патроны", "глушитель", "нарезное",
			"нелегальное оружие", "сделки с оружием",
			// Almanca
			"waffen", "schusswaffe", "pistole", "gewehr", "munition kaufen",
			"waffenhandel", "illegale waffen",
			// İspanyolca/Portekizce
			"armas", "pistola", "fusil", "munición ilegal", "armas ilegales",
		},
		category:    "Silah Satışı",
		criticality: 4,
		weight:      80,
	},

	{
		// EN + RU: поддельные документы / фейк паспорт
		keywords: []string{
			// İngilizce
			"counterfeit", "fake passport", "fake id", "forged document", "fake driver",
			"fake currency", "counterfeit money", "fake diploma", "fake certificate",
			"id card shop", "document forgery", "novelty id", "scannable fake id",
			// Rusça
			"поддельный документ", "фейк паспорт", "фальшивый паспорт",
			"поддельные права", "фальшивые деньги", "подделка документов",
			"купить паспорт", "левые документы", "ксива",
			// Almanca
			"gefälschte dokumente", "falscher pass", "falsche identität", "urkundenfälschung",
			// İspanyolca
			"documentos falsos", "pasaporte falso", "dinero falso",
		},
		category:    "Sahte Belge / Para",
		criticality: 4,
		weight:      72,
	},

	{
		// EN + RU: заказное убийство / наёмники
		keywords: []string{
			"hitman", "murder for hire", "contract killer", "assassination", "kill service",
			"harm service", "wet work", "hire assassin",
			// Rusça
			"заказное убийство", "киллер", "наёмный убийца", "устранить человека",
		},
		category:    "Suç Hizmeti",
		criticality: 4,
		weight:      99,
	},

	// ════════════════════════════════════════════════════════════
	// KRİTİKLİK 3
	// ════════════════════════════════════════════════════════════

	{
		// EN + RU: хакерский форум — xss.is / exploit.in / breachforums
		keywords: []string{
			// İngilizce
			"hacking forum", "cracking forum", "hackforums", "exploit forum",
			"underground forum", "dark forum", "forum hack", "leaks forum",
			"breach forum", "raidforums", "nulled", "cracked.to", "xss.is",
			"exploit.in", "lolzteam", "rf.ws",
			// Rusça — форум, топик, раздел
			"хакерский форум", "взломанный форум", "подпольный форум",
			"форум хакеров", "темный форум", "ру андеграунд",
			"xss форум", "эксплойт форум", "carding форум",
			// Almanca
			"hacker forum", "untergrund forum", "darknet forum",
		},
		category:    "Hacker Forumu",
		criticality: 3,
		weight:      55,
	},

	{
		// EN + RU: спам / рассылка
		keywords: []string{
			// İngilizce
			"spamming", "spam service", "email spam", "sms spam", "sms bomber",
			"call flooder", "spam panel", "bulk mailer", "mailer service",
			"smtp server", "bulletproof smtp", "mass mailing",
			// Rusça
			"спам", "спам рассылка", "массовая рассылка", "смс спам",
			"спам сервис", "смс бомбер", "звонки флуд", "спамер",
			"почтовый спам", "smtp рассылка",
			// Almanca
			"spam dienst", "massenmailing", "spam versand",
		},
		category:    "Spam / Sosyal Mühendislik",
		criticality: 3,
		weight:      50,
	},

	{
		// EN + RU: пробив — Rusya'ya özgü kişisel veri sorgulama hizmeti
		keywords: []string{
			// İngilizce
			"doxxing", "dox service", "personal info lookup", "find address",
			"find person", "stalkerware", "stalking service", "osint for hire",
			"background check darknet", "people finder dark",
			// Rusça — ПРОБИВ: Rusya'daki en yaygın doxxing hizmet türü
			"пробив", "пробивка", "пробить человека", "найти адрес",
			"пробить по номеру", "пробить паспорт", "пробить авто",
			"досье на человека", "слежка за человеком", "пробить банк",
			"пробить фсб", "деанон", "деанонимизация",
			// Almanca
			"doxing dienst", "personensuche darknet",
		},
		category:    "Doxxing / Stalkerware",
		criticality: 3,
		weight:      60,
	},

	{
		// EN + RU: маркетплейс / рынок
		keywords: []string{
			// İngilizce
			"dark market", "darknet market", "illegal market", "vendor shop",
			"escrow market", "onion market", "tor market", "hidden market",
			"marketplace", "shop onion", "hydra market", "mega market",
			// Rusça — ГИДРА, МЕГА, darknet рынок
			"тёмный рынок", "даркнет маркет", "маркетплейс тор",
			"гидра", "мега маркет", "omg маркет", "купить на гидре",
			"тор магазин", "анонимный магазин",
			// Almanca
			"darknet markt", "illegaler markt", "drogenmarkt tor",
			// İspanyolca
			"mercado darknet", "tienda dark web",
		},
		category:    "Karanlık Market",
		criticality: 3,
		weight:      48,
	},

	{
		// EN + RU: хакерские инструменты / брутфорс
		keywords: []string{
			// İngilizce
			"hacking tool", "pentesting tool", "exploit tool", "vulnerability scanner",
			"password cracker", "bruteforce tool", "keylogger", "trojan", "backdoor tool",
			"network scanner", "wifi cracker", "hash cracker", "rat tool", "pentest framework",
			// Rusça
			"хакерский инструмент", "брутфорс", "брутер", "чекер", "сканер уязвимостей",
			"взломщик паролей", "генератор паролей взлом", "sql инжекция инструмент",
			"инструмент для взлома", "купить брутер", "чекер аккаунтов",
			// Almanca
			"hacking tool", "brute force programm", "passwort knacken",
		},
		category:    "Hacking Araçları",
		criticality: 3,
		weight:      52,
	},

	{
		// EN + RU: отмывание крипты / биткоин миксер
		keywords: []string{
			// İngilizce
			"money laundering", "crypto launder", "bitcoin mixer", "crypto tumbler",
			"btc mixer", "xmr mixer", "coin mixer", "wash trading", "dirty coins",
			"bitcoin cleaner", "anonymize bitcoin", "chain hopping",
			// Rusça
			"отмывание денег", "отмывание крипты", "биткоин миксер", "миксер крипты",
			"криптомиксер", "чистая крипта", "анонимизация биткоина",
			"обмен грязной крипты", "миксер btc", "монеро миксер",
			// Almanca
			"geldwäsche krypto", "bitcoin mixer", "krypto wäsche",
		},
		category:    "Kripto Aklaması",
		criticality: 3,
		weight:      65,
	},

	// ════════════════════════════════════════════════════════════
	// KRİTİKLİK 2
	// ════════════════════════════════════════════════════════════

	{
		// EN + RU: криптообменник / биржа без KYC
		keywords: []string{
			"crypto exchange", "bitcoin exchange", "p2p exchange", "no kyc exchange",
			"anonymous exchange", "dex", "swap service", "crypto swap",
			// Rusça
			"обменник крипты", "биткоин обмен", "крипто обменник", "без kyc обмен",
			"анонимный обмен", "p2p крипта",
		},
		category:    "Kripto / Finans",
		criticality: 2,
		weight:      30,
	},

	{
		// EN + RU: впн / прокси / анонимность
		keywords: []string{
			"vpn service", "anonymous vpn", "no log vpn", "private vpn", "proxy service",
			"socks5 proxy", "residential proxy", "anonymous proxy", "tor vpn",
			// Rusça
			"впн", "анонимный впн", "прокси", "сокс5", "резидентный прокси",
			"анонимность", "скрыть ip",
		},
		category:    "VPN / Proxy",
		criticality: 2,
		weight:      25,
	},

	{
		// EN + RU: информатор / утечка в прессу
		keywords: []string{
			"whistleblower", "secure drop", "leak platform", "anonymous submission",
			"journalist", "press freedom", "secure submit",
			// Rusça
			"информатор", "анонимная утечка", "слив в прессу",
		},
		category:    "Whistleblower / Basın",
		criticality: 2,
		weight:      20,
	},

	{
		// EN + RU: пулпрофхостинг / bulletproof
		keywords: []string{
			"hosting", "bulletproof hosting", "anonymous hosting", "offshore hosting",
			"dark hosting", "hidden service hosting", "onion hosting", "abuse resistant hosting",
			// Rusça
			"пуленепробиваемый хостинг", "абьюзоустойчивый хостинг", "тёмный хостинг",
			"анонимный хостинг", "оффшорный хостинг",
			// Almanca
			"bulletproof hosting", "anonymes hosting",
		},
		category:    "Bulletproof Hosting",
		criticality: 2,
		weight:      35,
	},

	{
		// EN + RU: анонимное общение / encrypted chat
		keywords: []string{
			"anonymous chat", "encrypted chat", "darknet email", "secure messaging",
			"jabber", "xmpp server", "i2p mail", "onion mail",
			// Rusça
			"анонимный чат", "зашифрованный мессенджер", "тёмный чат",
		},
		category:    "İletişim / Ağ",
		criticality: 2,
		weight:      18,
	},

	// ════════════════════════════════════════════════════════════
	// KRİTİKLİK 1
	// ════════════════════════════════════════════════════════════

	{
		keywords:    []string{"blog", "news", "article", "library", "books", "wiki", "mirror", "archive", "search engine", "directory", "новости", "библиотека", "архив"},
		category:    "Bilgi / Medya",
		criticality: 1,
		weight:      10,
	},
	{
		keywords:    []string{"forum", "community", "discussion", "board", "topic", "thread", "форум", "сообщество", "обсуждение", "топик"},
		category:    "Forum / Topluluk",
		criticality: 1,
		weight:      8,
	},
}

// PredictCTI başlık ve URL'ye göre çok-sinyalli CTI kategori/kritiklik tahmini yapar.
// Tüm kurallar skorlanır, en yüksek weight × kritiklik kazanır.
func (r *Result) PredictCTI() {
	text := strings.ToLower(r.Title + " " + r.URL)

	type match struct {
		category    string
		criticality int
		score       int
	}

	var best match
	best.category = "Genel"
	best.criticality = 1
	best.score = -1

	for _, rule := range ctiRules {
		hits := 0
		for _, kw := range rule.keywords {
			if strings.Contains(text, kw) {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		// Score = weight * hits (birden fazla keyword eşleşirse daha güvenilir)
		score := rule.weight * hits
		if score > best.score {
			best.score = score
			best.category = rule.category
			best.criticality = rule.criticality
		}
	}

	r.Criticality = best.criticality
	r.Category = best.category
}

func matchAny(text string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

// CategoryColor kategori için CSS renk sınıfı döndürür
func CategoryColor(category string) string {
	switch category {
	case "Ransomware / Extortion", "APT / Devlet Aktörü", "Exploit / 0day":
		return "danger"
	case "Malware / Botnet", "İlk Erişim Satışı", "Finansal Dolandırıcılık", "Suç Hizmeti":
		return "critical"
	case "Veri Sızıntısı", "DDoS Hizmeti", "Silah Satışı", "Kripto Aklaması":
		return "high"
	case "Phishing / Scam", "Uyuşturucu Market", "Sahte Belge / Para", "Hacker Forumu":
		return "medium"
	case "Spam / Sosyal Mühendislik", "Doxxing / Stalkerware", "Hacking Araçları", "Karanlık Market":
		return "medium"
	case "Kripto / Finans", "Bulletproof Hosting", "VPN / Proxy":
		return "low"
	default:
		return "neutral"
	}
}

// decodeHTMLEntities HTML entity'leri decode eder
func decodeHTMLEntities(s string) string {
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	s = strings.ReplaceAll(s, "&#x2F;", "/")
	// Numeric entities
	numericRegex := regexp.MustCompile(`&#(\d+);`)
	s = numericRegex.ReplaceAllStringFunc(s, func(match string) string {
		var num int
		fmt.Sscanf(match, "&#%d;", &num)
		if num > 0 && num < 65536 {
			return string(rune(num))
		}
		return match
	})
	return s
}

// cleanTitle title'ı temizler ve anlamlı hale getirir
func cleanTitle(title, url string) string {
	// Boş veya çok kısa title
	if len(title) < 3 {
		return extractTitleFromURL(url)
	}

	// Title URL ile aynı mı? (veya URL'nin bir parçası mı?)
	if strings.Contains(url, title) || strings.Contains(title, ".onion") {
		return extractTitleFromURL(url)
	}

	// Title sadece "..." veya benzeri mi?
	cleaned := strings.Trim(title, ".")
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) < 3 {
		return extractTitleFromURL(url)
	}

	// Title çok uzunsa kısalt
	if len(title) > 150 {
		title = title[:147] + "..."
	}

	return title
}

// extractTitleFromURL URL'den anlamlı bir title çıkarır
func extractTitleFromURL(url string) string {
	// http://xxx.onion/path/to/page -> path/to/page veya domain

	// Protocol'ü kaldır
	cleanURL := strings.TrimPrefix(url, "http://")
	cleanURL = strings.TrimPrefix(cleanURL, "https://")

	// Query string'i kaldır
	if idx := strings.Index(cleanURL, "?"); idx != -1 {
		cleanURL = cleanURL[:idx]
	}

	// Parçala
	parts := strings.Split(cleanURL, "/")

	if len(parts) > 1 && len(parts[1]) > 0 {
		// Path var, path'i kullan
		path := strings.Join(parts[1:], "/")
		path = strings.Trim(path, "/")

		// Path'i güzelleştir
		path = strings.ReplaceAll(path, "-", " ")
		path = strings.ReplaceAll(path, "_", " ")
		path = strings.ReplaceAll(path, ".php", "")
		path = strings.ReplaceAll(path, ".html", "")
		path = strings.ReplaceAll(path, ".htm", "")

		if len(path) > 3 {
			// İlk harfi büyük yap
			if len(path) > 0 {
				path = strings.ToUpper(string(path[0])) + path[1:]
			}
			return "[" + path + "]"
		}
	}

	// Sadece domain var, kısa hash göster
	domain := parts[0]
	if strings.Contains(domain, ".onion") {
		// xxx.onion -> [Onion: xxx...]
		onionPart := strings.TrimSuffix(domain, ".onion")
		if len(onionPart) > 8 {
			onionPart = onionPart[:8]
		}
		return "[Onion: " + onionPart + "...]"
	}

	return "[Unknown Site]"
}

// deduplicate tekrar eden URL'leri kaldırır ve son bir filtreleme yapar
func (s *Searcher) deduplicate(results []Result) []Result {
	seen := make(map[string]bool)
	var unique []Result
	var filtered int

	for _, r := range results {
		if !seen[r.URL] {
			// Son kontrol - isValidResultURL
			if !isValidResultURL(r.URL) {
				filtered++
				continue
			}
			seen[r.URL] = true
			unique = append(unique, r)
		}
	}

	if filtered > 0 {
		logger.Debug("DEDUPLICATION: %d low value URLs filtered", filtered)
	}

	logger.Debug("DEDUPLICATION: %d unique results", len(unique))

	return unique
}
