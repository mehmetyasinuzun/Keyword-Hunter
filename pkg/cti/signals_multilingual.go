package cti

// multilingualSignals kategori → desen → ağırlık. Kategoriler categoryRules'taki
// adlarla birebir aynıdır (mevcut kategori/kritiklik şeması); ayrı kategori
// açılmaz. Eski "PredictCTI v2" (PR #2) çok dilli sözlüğünden şu eşleme ile
// taşındı:
//
//	Ransomware / Extortion                     → Ransomware
//	Veri Sızıntısı, Doxxing (пробив), stealer  → Veri Sızıntısı
//	Finansal, Phishing/Scam, Kripto Aklaması   → Finansal Dolandırıcılık
//	Exploit / 0day, Malware, Hacking Araçları  → Exploit / Vulnerability
//	İlk Erişim Satışı, botnet/loader, brute    → Initial Access
//	Uyuşturucu, Silah, Sahte Belge/Para,
//	Suç Hizmeti, Karanlık Market, DDoS hizmeti → Illegal Market
//	Hacker Forumu, Forum / Topluluk            → Siber Forum
//	VPN/Proxy, Bulletproof Hosting, İletişim   → İletişim / Ağ
//
// Desen sözdizimi ve eşleşme kuralları için bkz. multilingual.go. Kurallar:
//   - Anahtar "desen" ya da "desen|etiket" biçimindedir. Etiket, sinyalin
//     kayıtlara/etiket bulutuna düşen SABİT adıdır; çekimler ve
//     transliterasyonlar aynı etiketi paylaşır (пробива|пробив, probiv|пробив)
//     ve tek sinyal olarak sayılır. Kesik kökler okunur bir etiket alır
//     (утечк* данных|утечка данных). Aynı etiketi paylaşan desenler aynı
//     kategori, ağırlık ve biçimde (kelime/ifade) olmak zorundadır.
//   - Genel/çok anlamlı kelimeler (карты, схема, развод, трава, соль, закладки,
//     автомат, speed, apt …) eklenmez ya da ağırlık <= supportMaxWeight (3)
//     ile yalnızca DESTEKLEYİCİ sinyal olur: tekrar etse ya da sorguda geçse
//     bile tek başına puan getirmez (выкуп, слив, golpe, ddos, heroína …).
//   - Kısa ya da başka kelimelerin öneki olan kökler (мет, крипт, скам, брут,
//     доступ, waffen, munic …) '*' ile değil, tam biçim/çekim listesiyle yazılır.
//   - Eski TR/EN anahtar kelimeleriyle aynı tokenı tanıyan desen eklenmez (çift
//     sayım); TestMultilingualSignals_NoLegacyOverlap bunu denetler.
var multilingualSignals = map[string]map[string]int{
	"Ransomware": {
		// RU
		"вымогател*|вымогатель": 7, // вымогатель, вымогатели, вымогательство
		"шифровальщик*":         7,
		"декриптор*":            5,
		"шифрование файлов":     4,
		"выкуп*":                3, // "выкуп авто" gibi genel kullanım → yalnız destekleyici
		"vymogatel*|вымогатель": 7, // RU translit
		// DE
		"erpressungstrojaner*":      7,
		"verschlüsselungstrojaner*": 7,
		"erpressersoftware":         7,
		"lösegeld*":                 5,
		"erpressung*":               4,
		// ES / PT
		"secuestro de datos": 6,
		"sequestro de dados": 6,
		"extors*|extorsión":  5, // extorsión, extorsionar, extorsão
		// EN (eski sözlükteki yanlış pozitifsiz grup adları)
		"blackbasta": 7,
		"rhysida":    7,
		"ryuk":       7,
		"sodinokibi": 7,
		"cl0p":       7,
		"raas":       6,
	},

	"Veri Sızıntısı": {
		// RU — пробив: kişi/kurum sorgulama hizmeti (TR "sorgu paneli" karşılığı)
		"пробив":          7,
		"пробива|пробив":  7,
		"пробивка|пробив": 7,
		"пробивки|пробив": 7,
		"пробивку|пробив": 7,
		"probiv|пробив":   7, // RU translit: Kiril biçimle aynı etiket/sayaç
		"пробить по номер*|пробить по номеру": 7,
		"утечк* данных|утечка данных":         7,
		"слив баз*|слив базы":                 7,
		"слив* данных|слив данных":            7,
		"слит* баз*|слитые базы":              7,
		"слив": 3, // tesisat "слив" da var → yalnız destekleyici; "сливы" = erik (слива) çekimi, eklenmez
		"баз* данных|база данных": 4,
		"дамп":          5,
		"дампы|дамп":    5,
		"стилер*":       6,
		"деанон*":       5,
		"парол*|пароль": 4,
		"персональн* данн*|персональные данные": 4,
		// DE
		"datenleck*":             7,
		"datenpanne*":            6,
		"datendiebstahl*":        6,
		"gestohlene daten":       6,
		"zugangsdaten":           5,
		"datenbank*":             4,
		"passwort*":              4,
		"kennwort*":              4,
		"personenbezogene daten": 4,
		// ES
		"datos filtrados":      7,
		"fuga de datos":        7,
		"datos robados":        6,
		"filtraci*|filtración": 3, // filtración (de agua) da olabilir → yalnız destekleyici
		"base de datos":        4,
		"contraseña*":          5, // contraseña / contrasena
		"credenciales":         5,
		"datos personales":     4,
		// PT
		"vazamento*":     6,
		"vazad*|vazado":  5,
		"dados roubados": 6,
		"banco de dados": 4,
		"senha*":         5,
		"credenciais":    5,
		"dados pessoais": 4,
		// EN (eski kurallara ek)
		"infostealer*":   6,
		"leaked":         5,
		"doxxing":        5,
		"doxing|doxxing": 5,
		"pwned":          4,
	},

	"Finansal Dolandırıcılık": {
		// RU — кардинг / обнал / дропы: RU underground'ın finans terimleri
		"кардинг*":              7,
		"кардер*|кардер":        6,
		"обнал*":                7, // обнал, обналичивание, обнальщик
		"obnal*|обнал":          7, // RU translit: Kiril biçimle aynı etiket/sayaç
		"нальщик*":              5,
		"дроповод*":             6,
		"дропы":                 4,
		"дамп* карт*|дамп карт": 7,
		"кредитн* карт*|кредитная карта":   5,
		"банковск* карт*|банковская карта": 5,
		"чёрный нал": 5, // ё → е katlanır: "черный нал" da eşleşir
		"фишинг*":    6,
		"мошенни*|мошенничество": 5,
		"скам":                5,
		"скамер*":             6,
		"отмывани*|отмывание": 6,
		"криптомиксер*":       6,
		// DE
		"kreditkart*|kreditkarte": 5,
		"kartendaten":             5,
		"bankbetrug*":             6,
		"betrug*":                 4,
		"geldwäsche*":             6,
		// ES
		"tarjetas robadas":                       7,
		"tarjeta* clonad*|tarjetas clonadas":     7,
		"tarjeta* de credito|tarjeta de crédito": 6,
		"fraude*":               5, // ES + PT
		"estafa*":               5,
		"lavado de dinero":      7,
		"blanqueo de capitales": 6,
		// PT
		"carto* clonad*|cartões clonados":     7,
		"carto* de credito|cartão de crédito": 6,
		"lavagem de dinheiro":                 7,
		"estelionat*|estelionato":             6,
		"conta* laranja*|conta laranja":       6,
		"golpe*":                              3, // "golpe de estado" → yalnız destekleyici
		// TR (eski kurallara ek)
		"kara para akla*|kara para aklama": 7,
		"oltalama*":                        6,
		"dolandırıcı":                      5,
		"dolandırıcılar|dolandırıcı":       5,
		// EN (eski kurallara ek)
		"phishing*":                    6,
		"money laundering":             6,
		"bitcoin mixer*|bitcoin mixer": 6,
		"btc mixer*|btc mixer":         6,
		"crypto mixer*|crypto mixer":   6,
		"cloned card*|cloned card":     7,
		"skimmer*":                     6,
		"vbv":                          5,
		"money mule*|money mule":       6,
		"scam page*|scam page":         5,
	},

	"Exploit / Vulnerability": {
		// RU — эксплойт / уязвимость / малварь
		"эксплойт*":             7,
		"эксплоит*|эксплойт":    7,
		"уязвимост*|уязвимость": 6,
		"0день":        7,
		"нулевой день": 7,
		"нулевого дня|нулевой день":                         7,
		"удален* выполнени* кода|удаленное выполнение кода": 8,
		"повышени* привилеги*|повышение привилегий":         7,
		"бэкдор*":            6,
		"бекдор*|бэкдор":     6,
		"руткит*":            6,
		"шеллкод*":           6,
		"троян*":             5,
		"кейлоггер*":         6,
		"малвар*|малварь":    6,
		"малвер*|малварь":    6,
		"вредонос*|вредонос": 6,
		"криптор":            5, // крипт* değil: криптовалюта ile çakışır
		"крипторы|криптор":   5,
		"криптора|криптор":   5,
		"ратник*":            6,
		"инжект*":            5,
		// DE
		"sicherheitslück*|sicherheitslücke": 6,
		"schwachstell*|schwachstelle":       6,
		"schadsoftware":                     6,
		"trojaner*":                         6,
		"hintertür*":                        5,
		"rechteausweitung":                  7,
		// ES / PT
		"vulnerabilidad*|vulnerabilidad": 6, // ES vulnerabilidad + PT vulnerabilidade
		"dia cero":                       7,
		"dia zero":                       7,
		"ejecucion remota":               7,
		"execucao remota":                7,
		"escalada de privilegios":        7,
		"escalonamento de privilegios":   7,
		"troyano*":                       6,
		"puerta trasera":                 5,
		"cavalo de troia":                6,
		"software malicioso":             6,
		// TR (eski kurallara ek)
		"zararlı yazılım*|zararlı yazılım":             6,
		"truva atı":                                    6,
		"güvenlik açığı":                               5,
		"güvenlik açıkları|güvenlik açığı":             5,
		"sıfırıncı gün":                                7,
		"arka kapı*|arka kapı":                         5,
		"uzaktan kod çalıştır*|uzaktan kod çalıştırma": 8,
		// EN (eski kurallara ek)
		"zero day":                    7,
		"exploits|exploit":            6,
		"vulnerabilit*|vulnerability": 5,
		"malware*":                    6,
		"trojan*":                     5,
		"keylogger*":                  6,
		"crypter*":                    6,
		"cobalt strike":               6,
	},

	"Initial Access": {
		// RU — продам доступ / дедики / шелл
		"продам доступ": 7,
		"продаю доступ": 7,
		"куплю доступ":  6,
		"корпоративн* доступ*|корпоративный доступ": 7,
		"рут доступ":         6,
		"rdp доступ":         7,
		"доступ":             4, // доступ* değil: доступный (uygun fiyatlı) ile çakışır
		"доступы|доступ":     4,
		"доступа|доступ":     4,
		"дедик*":             6,
		"шелл":               5,
		"шеллы|шелл":         5,
		"веб шелл*|веб шелл": 6,
		"вебшелл*":           6,
		"ботнет*":            6,
		"лоадер*":            5,
		"брут":               5, // брут* değil: брутальный ile çakışır
		"брутер":             5,
		"брутеры|брутер":     5,
		"брутфорс*":          5,
		// DE
		"zugang kaufen":    6,
		"zugang verkaufen": 6,
		"zugänge":          4,
		"rdp zugang":       7,
		"fernzugriff*":     5,
		// ES
		"acceso inicial":                   7,
		"vendo acceso":                     7,
		"venta de acceso*|venta de acceso": 7,
		"acceso remoto":                    5,
		// PT
		"acesso inicial":                   7,
		"vendo acesso":                     7,
		"venda de acesso*|venda de acesso": 7,
		"acesso remoto":                    5,
		// TR (eski kurallara ek)
		"ilk erişim":                   7,
		"erişim satış*|erişim satışı":  7,
		"uzak masaüstü*|uzak masaüstü": 5,
		// EN (eski kurallara ek)
		"access broker*|access broker": 7,
		"webshells|webshell":           6,
		"domain admin":                 6,
	},

	"Illegal Market": {
		// RU — наркотики / оружие / подделки / заказные услуги / ДДоС
		"наркотик*":           7,
		"наркота":             7,
		"кокаин*":             7,
		"героин*":             7,
		"мефедрон*":           7,
		"метамфетамин*":       7,
		"амфетамин*":          7,
		"фентанил*":           7,
		"экстази":             6,
		"гашиш*":              6,
		"марихуан*|марихуана": 6,
		"каннабис*":           5,
		"лсд":                 6,
		"закладчик*":          6, // закладки (yer imi) değil: yalnız kurye
		"оружи*|оружие":       6, // оружие / оружия
		"огнестрел*|огнестрельное оружие": 6,
		"пистолет*":           5,
		"патроны":             4,
		"глушител*|глушитель": 3, // araba susturucusu da olabilir → yalnız destekleyici
		"фальшив*|фальшивка":  5,
		"поддельн*|подделка":  5,
		"ксива":               5,
		"киллер*":             7,
		"заказн* убийств*|заказное убийство": 8,
		"даркнет*":     4,
		"маркетплейс*": 6,
		// ДДоС: голый "ддос" çoğunlukla koruma/anti-DDoS sayfasıdır → yalnız
		// destekleyici; kiralık saldırıyı yalnız sipariş/satış ifadeleri taşır.
		"ддос*|ддос":    3,
		"ддос на заказ": 7,
		"ддос атак* на заказ|ддос на заказ": 7,
		"заказать ддос":                     7,
		"купить ддос":                       7,
		"аренда ддос*|аренда ддос":          7,
		"стрессер*|стрессер":                6,
		// DE
		"drogen*":                            6,
		"kokain*":                            7, // DE + TR kokain
		"betäubungsmittel*|betäubungsmittel": 6,
		"waffen":                             5, // waffen* değil: Waffenstillstand (ateşkes)
		"waffenhandel*":                      7,
		"schusswaff*|schusswaffe":            6,
		"gewehr*":                            5,
		"munition":                           4,
		"falschgeld*":                        7,
		"gefälscht*|gefälscht":               5,
		"urkundenfälschung*":                 7,
		"auftragsmörder*":                    8,
		"darknet markt*|darknet markt":       6,
		// ES / PT
		"cocain*|cocaína":                    7, // ES/PT cocaína + EN cocaine
		"heroina|heroína":                    3, // heroína = kahraman kadın da → yalnız destekleyici
		"heroina pura|heroína pura":          7,
		"anfetamina*|anfetamina":             7,
		"metanfetamin*|metanfetamina":        7,
		"drogas":                             6, // drogas* değil: drogaria (eczane)
		"marihuana":                          6,
		"maconha":                            6,
		"armas":                              5, // armas* değil: armazém
		"armas de fuego":                     7,
		"arma de fogo":                       7,
		"armas de fogo|arma de fogo":         7,
		"munición":                           4,
		"municiones|munición":                4,
		"munição":                            4,
		"munições|munição":                   4,
		"pasaporte* fals*|pasaporte falso":   7,
		"passaporte* fals*|passaporte falso": 7,
		"document* fals*|documentos falsos":  6, // ES + PT
		"dinero falso":                       7,
		"dinheiro falso":                     7,
		"sicario*":                           7,
		"matador de aluguel":                 8,
		"mercado negro":                      6,
		// TR (eski kurallara ek)
		"uyuşturucu*":                    7,
		"eroin*":                         7,
		"metamfetamin*|metamfetamin":     7,
		"silah*":                         5,
		"tabanca*":                       5,
		"mühimmat*":                      5,
		"sahte pasaport*|sahte pasaport": 7,
		"sahte para":                     7,
		"tetikçi*":                       7,
		"karaborsa*":                     5,
		// EN (eski kurallara ek)
		"heroin":                           7, // heroin* değil: heroine
		"methamphetamin*|methamphetamine":  7,
		"amphetamin*|amphetamine":          7, // EN amphetamine + DE Amphetamin
		"fentanyl*":                        7,
		"mdma":                             6,
		"ecstasy":                          5,
		"ketamin*":                         6,
		"oxycodon*":                        6,
		"xanax":                            5,
		"narcotic*|narcotics":              6,
		"drugs":                            5,
		"weapon*|weapons":                  5,
		"firearm*|firearms":                6,
		"handgun*|handguns":                5,
		"pistol*":                          5, // EN pistol + DE Pistole + ES/PT pistola
		"ammunition":                       4,
		"ammo":                             4,
		"glock*":                           5,
		"ak47":                             5,
		"counterfeit*|counterfeit":         6,
		"fake passport*|fake passport":     7,
		"fake id":                          6,
		"fake ids|fake id":                 6,
		"hitman":                           3, // video oyunu da → yalnız destekleyici
		"hitman for hire":                  8,
		"hire a hitman":                    8,
		"contract killer*|contract killer": 8,
		"murder for hire":                  8,
		"darknet market*|darknet market":   6,
		"ddos":                             3, // DDoS-Guard / anti-DDoS sayfaları → yalnız destekleyici
		"ddos for hire":                    7,
		"ddos as a service":                7,
		"booter":                           5,
		"stresser*|stresser":               6,
	},

	"Siber Forum": {
		// RU
		"форум*": 6,
		"хакер*": 5, // хакер, хакеры, хакерский
		"андеграунд*|андеграунд": 4,
		"топик*": 3,
		// DE
		"untergrund*|untergrund":   4,
		"hackerforum*|hackerforum": 7,
		// ES
		"foro":           5, // PT "foro privilegiado" (hukuk) → tek başına eşik altı
		"foros|foro":     5,
		"foro privado":   6,
		"hackeo*|hackeo": 4,
		// EN (eski kurallara ek)
		"forums|forum": 6,
		"hackforums":   7,
		"breachforums": 7,
		"nulled":       5,
	},

	"İletişim / Ağ": {
		// RU
		// телеграм* değil: телеграмма (telgraf) ile çakışır
		"телеграм":                5,
		"телеграма|телеграм":      5,
		"телеграму|телеграм":      5,
		"телеграме|телеграм":      5,
		"телеграмом|телеграм":     5,
		"жаббер*":                 5,
		"джаббер*":                5,
		"прокси":                  3,
		"впн":                     3,
		"сокс5":                   4,
		"соксы|сокс5":             4,
		"анонимност*|анонимность": 3,
		"абузоустойчив*|абузоустойчивый хостинг":  6, // bulletproof hosting
		"абьюзоустойчив*|абузоустойчивый хостинг": 6,
		// DE / ES / PT: çoğunlukla EN terimler kullanılıyor
		"anonymes hosting": 5,
		// EN (eski kurallara ek)
		"bulletproof hosting":                 7,
		"socks5":                              4,
		"residential prox*|residential proxy": 5,
	},
}
