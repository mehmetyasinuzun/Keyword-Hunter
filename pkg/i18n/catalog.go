package i18n

// catalog[dil][anahtar] = çeviri. Türkçe tam; İngilizce kapsar. Eksik EN anahtarı
// otomatik Türkçe'ye düşer.
var catalog = map[string]map[string]string{
	"tr": {
		// Navigasyon
		"nav.dashboard": "Panel", "nav.search": "Arama", "nav.results": "Bulgular",
		"nav.graph": "Harita", "nav.analytics": "Analiz", "nav.scheduled": "Planlı",
		"nav.watchlist": "İzleme", "nav.monitor": "Motorlar", "nav.crawl": "Örümcek",
		"nav.users": "Kullanıcılar", "nav.settings": "Ayarlar", "nav.logout": "Çıkış",
		// Ortak
		"common.refresh": "Yenile", "common.save": "Kaydet", "common.cancel": "Vazgeç",
		"common.delete": "Sil", "common.add": "Ekle", "common.close": "Kapat",
		"common.search": "Ara", "common.filter": "Filtrele", "common.loading": "Yükleniyor…",
		"common.new": "Yeni", "common.export": "Dışa Aktar", "common.copy": "Kopyala",
		"common.language": "Dil", "common.all": "Tümü", "common.status": "Durum",
		"common.actions": "İşlemler", "common.category": "Kategori", "common.date": "Tarih",
		// Giriş
		"login.subtitle": "Dark Web Tehdit İstihbarat Platformu",
		"login.username": "Kullanıcı Adı", "login.password": "Şifre", "login.submit": "Giriş Yap",
		"login.footer": "Yetkili analistlere özel · CTI Operasyon Konsolu",
		"login.error":  "Kullanıcı adı veya şifre hatalı.",
		"login.locked": "Çok fazla başarısız deneme. Bir süre sonra tekrar deneyin.",
		// Panel
		"dash.title":         "Operasyon Paneli",
		"dash.subtitle":      "Bulgu hacmi, sınıflandırma kalitesi, motor sağlığı ve son 24 saatin özeti",
		"dash.totalFindings": "Toplam Bulgu", "dash.totalSearches": "Toplam Arama",
		"dash.critical": "Acil (Sev 5)", "dash.engineHealth": "Motor Sağlığı",
		"dash.newSearch": "Yeni Arama", "dash.planScan": "Tarama Planla",
		// Arama
		"search.title":       "Dark Web Arama",
		"search.placeholder": "ransomware, leak, tc kimlik, şirket adı, e-posta alanı…",
		// Bulgular
		"results.title": "Bulgular", "results.empty": "Filtreyle eşleşen bulgu yok",
		// Roller
		"role.admin": "Admin", "role.analyst": "Analist", "role.viewer": "İzleyici",
	},
	"en": {
		"nav.dashboard": "Dashboard", "nav.search": "Search", "nav.results": "Findings",
		"nav.graph": "Map", "nav.analytics": "Analytics", "nav.scheduled": "Scheduled",
		"nav.watchlist": "Watchlist", "nav.monitor": "Engines", "nav.crawl": "Crawler",
		"nav.users": "Users", "nav.settings": "Settings", "nav.logout": "Log out",
		"common.refresh": "Refresh", "common.save": "Save", "common.cancel": "Cancel",
		"common.delete": "Delete", "common.add": "Add", "common.close": "Close",
		"common.search": "Search", "common.filter": "Filter", "common.loading": "Loading…",
		"common.new": "New", "common.export": "Export", "common.copy": "Copy",
		"common.language": "Language", "common.all": "All", "common.status": "Status",
		"common.actions": "Actions", "common.category": "Category", "common.date": "Date",
		"login.subtitle": "Dark Web Cyber Threat Intelligence Platform",
		"login.username": "Username", "login.password": "Password", "login.submit": "Sign in",
		"login.footer":       "For authorized analysts · CTI Operations Console",
		"login.error":        "Invalid username or password.",
		"login.locked":       "Too many failed attempts. Try again shortly.",
		"dash.title":         "Operations Dashboard",
		"dash.subtitle":      "Finding volume, classification quality, engine health, and last-24h summary",
		"dash.totalFindings": "Total Findings", "dash.totalSearches": "Total Searches",
		"dash.critical": "Urgent (Sev 5)", "dash.engineHealth": "Engine Health",
		"dash.newSearch": "New Search", "dash.planScan": "Schedule Scan",
		"search.title":       "Dark Web Search",
		"search.placeholder": "ransomware, leak, national id, company name, email domain…",
		"results.title":      "Findings", "results.empty": "No findings match the filter",
		"role.admin": "Admin", "role.analyst": "Analyst", "role.viewer": "Viewer",
	},
}
