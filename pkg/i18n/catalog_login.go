package i18n

func init() {
	register("tr", map[string]string{
		"login.title":      "Giriş",
		"login.welcome":    "Tekrar hoş geldiniz",
		"login.lede":       "Operasyon konsoluna erişmek için kimliğinizi doğrulayın.",
		"login.showPw":     "Şifreyi göster/gizle",
		"login.caps":       "Caps Lock açık",
		"login.lockedWait": "saniye sonra tekrar deneyin.",
		"login.session":    "Oturum başlatılamadı. Lütfen tekrar deneyin.",
		"login.signingIn":  "Doğrulanıyor…",
		"login.hero":       "Dark web'i tek konsoldan izleyin.",
		"login.heroLede":   "Onion arama motorlarında eşzamanlı tarama, otomatik kritiklik sınıflandırması, IOC çıkarımı ve sürekli izleme — tamamı Tor üzerinden, verileriniz yalnızca sizde.",
		"login.f1":         "10+ onion arama motorunda paralel tarama",
		"login.f2":         "IOC çıkarımı ve STIX 2.1 dışa aktarım",
		"login.f3":         "Uptime, içerik değişikliği ve captcha izleme",
		"login.f4":         "Rol tabanlı ekip erişimi, sıkı CSP, bcrypt",
	})
	register("en", map[string]string{
		"login.title":      "Sign in",
		"login.welcome":    "Welcome back",
		"login.lede":       "Authenticate to access the operations console.",
		"login.showPw":     "Show/hide password",
		"login.caps":       "Caps Lock is on",
		"login.lockedWait": "seconds before trying again.",
		"login.session":    "Could not start a session. Please try again.",
		"login.signingIn":  "Verifying…",
		"login.hero":       "Watch the dark web from a single console.",
		"login.heroLede":   "Concurrent onion search, automatic severity classification, IOC extraction and continuous monitoring — all over Tor, your data never leaves your machine.",
		"login.f1":         "Parallel search across 10+ onion engines",
		"login.f2":         "IOC extraction and STIX 2.1 export",
		"login.f3":         "Uptime, content-change and captcha monitoring",
		"login.f4":         "Role-based team access, strict CSP, bcrypt",
	})
}
