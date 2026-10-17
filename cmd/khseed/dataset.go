package main

// Demo veri kümesinin sabit içeriği. Kurum adları, alan adları ve kişiler
// KURGUSALDIR: e-postalar .example (RFC 2606), IP'ler belgeleme blokları
// (RFC 5737), onion adresleri ve cüzdanlar deterministik rastgele üretilir;
// gerçek bir kuruma, kişiye ya da hizmete işaret etmez.

// DefaultSeed varsayılan deterministik çekirdek.
const DefaultSeed uint64 = 20261017

// DemoUser bilinen parolalı demo hesabı (uçtan uca testler bunları kullanır).
type DemoUser struct {
	Username string
	Password string
	Role     string
}

// DemoUsers admin dışındaki demo hesapları. Parolalar herkesçe bilinir;
// e2e/README ve CONTRIBUTING.md ile aynı tutulmalıdır.
var DemoUsers = []DemoUser{
	{Username: "analyst", Password: "Analyst-Demo-Pass-1!", Role: "analyst"},
	{Username: "viewer", Password: "Viewer-Demo-Pass-1!", Role: "viewer"},
}

// querySpec bir arama sorgusunun ürettiği bulguların şablonu.
type querySpec struct {
	Query    string
	Org      string // kurgusal kurum adı (başlıklarda)
	Domain   string // kurgusal e-posta alan adı (.example)
	Category string
	BaseCrit int // kritiklik tabanı (±1 sapma)
	Count    int // üretilecek bulgu sayısı
	Tags     []string
	Titles   []string // %s → Org, %d → sayı
}

var querySpecs = []querySpec{
	{
		Query: "anadolu telekom", Org: "Anadolu Telekom", Domain: "anadolutelekom.example",
		Category: "Veri Sızıntısı", BaseCrit: 4, Count: 18,
		Tags:   []string{"leak", "database", "pii", "tc-kimlik", "telefon", "csv"},
		Titles: []string{"%s müşteri veritabanı satışta — %d kayıt", "%s abone CSV dökümü (ad, TC, GSM)", "[LEAK] %s fatura sistemi yedeği %d satır", "%s çağrı merkezi paneli erişimi", "%s iç ağ VPN kimlik bilgileri"},
	},
	{
		Query: "marmara bank", Org: "Marmara Bank", Domain: "marmarabank.example",
		Category: "Finansal Dolandırıcılık", BaseCrit: 4, Count: 16,
		Tags:   []string{"bank", "fraud", "carding", "otp-bypass", "phishing", "iban"},
		Titles: []string{"%s internet şubesi phishing kiti v%d", "%s hesap satışı — doğrulanmış %d adet", "%s SMS OTP bypass servisi", "%s kart BIN listesi ve dump", "%s mobil uygulama sahte APK"},
	},
	{
		Query: "lockbit türkiye", Org: "Ege Lojistik", Domain: "egelojistik.example",
		Category: "Ransomware", BaseCrit: 5, Count: 14,
		Tags:   []string{"ransomware", "lockbit", "extortion", "leak-site", "double-extortion"},
		Titles: []string{"LockBit sızıntı sitesi: %s — geri sayım %d gün", "%s dosyaları yayımlandı (%d GB)", "Yeni kurban listesi: %s ve 4 Türk şirketi", "%s fidye müzakere sohbeti sızdı", "Affiliate ilanı: Türkiye hedefleri (%d%% pay)"},
	},
	{
		Query: "vpn access turkey", Org: "Boğaziçi Sigorta", Domain: "bogazicisigorta.example",
		Category: "Initial Access", BaseCrit: 4, Count: 14,
		Tags:   []string{"initial-access", "vpn", "rdp", "iab", "fortinet", "citrix"},
		Titles: []string{"[IAB] %s — Fortinet VPN, domain admin, %d host", "Türkiye RDP erişimleri paketi (%d adet)", "%s Citrix erişimi açık artırmada", "Sigorta sektörü TR — kurumsal ağ erişimi", "%s webmail + VPN combo"},
	},
	{
		Query: "combo list", Org: "Kuzey Perakende", Domain: "kuzeyperakende.example",
		Category: "Veri Sızıntısı", BaseCrit: 3, Count: 16,
		Tags:   []string{"combo", "credentials", "stealer-logs", "email", "password"},
		Titles: []string{"TR combo list %dK satır (mail:pass)", "Stealer log paketi — %s çalışanları", "%s müşteri e-posta + parola eşleşmeleri", "Günlük combo dump #%d", "Redline log arşivi — Türkiye filtreli"},
	},
	{
		Query: "cve-2026 exploit", Org: "Delta Yazılım", Domain: "deltayazilim.example",
		Category: "Exploit / Vulnerability", BaseCrit: 3, Count: 14,
		Tags:   []string{"exploit", "cve", "rce", "0day", "poc", "webshell"},
		Titles: []string{"CVE-2026-%d RCE PoC satışta", "%s ERP yazılımında kimlik doğrulamasız RCE", "0day: kurumsal e-posta sunucusu (fiyat %d BTC)", "Webshell paketi — %s portalı", "Toplu tarama betiği CVE-2026-%d"},
	},
	{
		Query: "e-devlet panel", Org: "Kurgusal Sorgu Paneli", Domain: "sorgupaneli.example",
		Category: "Illegal Market", BaseCrit: 4, Count: 14,
		Tags:   []string{"panel", "pii", "tc-kimlik", "adres", "market"},
		Titles: []string{"%s — TC/adres/aile sorgu (aylık %d$)", "Sorgu paneli üyelik satışı v%d", "Plaka + adres sorgu servisi", "%s API erişimi — günlük %d sorgu", "Vesika fotoğrafı sorgu botu"},
	},
	{
		Query: "hacker forum türkçe", Org: "Karanlık Kule", Domain: "karanlikkule.example",
		Category: "Siber Forum", BaseCrit: 2, Count: 16,
		Tags:   []string{"forum", "community", "recruitment", "tutorial", "türkçe"},
		Titles: []string{"%s forumu yeni üye alımı (%d davet)", "Türkçe SQLi eğitimi — bölüm %d", "%s: hedef tartışması başlığı", "Ekip arıyoruz — pentest / red team", "%s forumu yedek adres duyurusu"},
	},
}

// analystNotes vaka notu örnekleri (yalnız bir kısmı kullanılır).
var analystNotes = []string{
	"Örneklem doğrulandı; kayıtların bir kısmı 2024 sızıntısıyla örtüşüyor. Kurum CERT'ine bildirildi.",
	"Satıcı daha önce aynı forumda dolandırıcılıkla suçlanmış; güvenilirlik düşük.",
	"IOC'ler SIEM'e aktarıldı, son 30 günde eşleşme yok.",
	"Ekran görüntüsü kanıt olarak arşivlendi. Müşteri ile toplantı planlandı.",
	"Tekrarlanan ilan; önceki bulguyla birleştirildi.",
	"Paylaşılan örnek veri sahte görünüyor (TC kimlik numaraları sağlama testinden geçmiyor).",
	"Erişim satışı canlı: satıcıyla iletişime geçilmedi, kolluk birimine iletildi.",
}

// watchSpec izleme listesi demo hedefi.
type watchSpec struct {
	Name, Category, Notes string
	Uptime                float64 // 0..1 ulaşılabilirlik olasılığı
	Changes               bool    // kontrol geçmişinde içerik değişikliği olsun mu
	Enabled               bool
}

var watchSpecs = []watchSpec{
	{Name: "LockBit sızıntı sitesi", Category: "Ransomware", Notes: "Yeni kurban yayınlarını izle", Uptime: 0.92, Changes: true, Enabled: true},
	{Name: "Karanlık Kule forumu", Category: "Forum", Notes: "Türkçe tehdit aktörü topluluğu", Uptime: 0.97, Changes: true, Enabled: true},
	{Name: "Sorgu paneli vitrin", Category: "Sorgu Paneli", Notes: "Vatandaş PII sorgu hizmeti", Uptime: 0.80, Changes: false, Enabled: true},
	{Name: "Combo pazar yeri", Category: "Market", Notes: "", Uptime: 0.88, Changes: true, Enabled: true},
	{Name: "IAB ilan panosu", Category: "Initial Access", Notes: "Kurumsal erişim satışları", Uptime: 0.55, Changes: false, Enabled: true},
	{Name: "Eski forum aynası", Category: "Forum", Notes: "Kapandı mı? Haftalık kontrol", Uptime: 0.10, Changes: false, Enabled: false},
}

// scheduledSpec planlı tarama demo kaydı.
type scheduledSpec struct {
	Query     string
	Interval  int // dakika
	Threshold int
	Runs      int // geçmiş çalıştırma sayısı (0 = hiç çalışmadı)
	Enabled   bool
}

var scheduledSpecs = []scheduledSpec{
	{Query: "anadolu telekom", Interval: 60, Threshold: 4, Runs: 3, Enabled: true},
	{Query: "lockbit türkiye", Interval: 30, Threshold: 5, Runs: 5, Enabled: true},
	{Query: "marmara bank", Interval: 360, Threshold: 3, Runs: 1, Enabled: true},
	{Query: "combo list", Interval: 1440, Threshold: 3, Runs: 0, Enabled: false},
}
