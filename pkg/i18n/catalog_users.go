package i18n

func init() {
	register("tr", map[string]string{
		"usr.title":    "Kullanıcılar ve Roller",
		"usr.eyebrow":  "Erişim yönetimi",
		"usr.subtitle": "Ekibin kimin neyi yapabileceğini yönetin: yöneticiler sistemi yönetir, analistler arama ve izleme yürütür, izleyiciler yalnızca okur.",
		"usr.new":      "Yeni Kullanıcı",
		"usr.roles":    "Rol dağılımı",

		"usr.r.adminS":   "tam yetki + kullanıcı ve ayar yönetimi",
		"usr.r.analystS": "arama, etiketleme, örümcek, izleme",
		"usr.r.viewerS":  "salt-okunur erişim",
		"usr.r.admin":    "Tam yetki; kullanıcıları ve sistem ayarlarını yönetir.",
		"usr.r.analyst":  "Arama, etiketleme, örümcek ve izleme listesi üzerinde yazma yetkisi.",
		"usr.r.viewer":   "Bulguları ve panoları görür; hiçbir şeyi değiştiremez.",

		"usr.team":        "Ekip",
		"usr.filterPh":    "Kullanıcı ara…",
		"usr.c.user":      "Kullanıcı",
		"usr.c.role":      "Rol",
		"usr.c.lastLogin": "Son giriş",
		"usr.c.created":   "Oluşturma",
		"usr.info":        "Parolalar bcrypt ile saklanır. Rol ve etkinlik değişiklikleri bir sonraki istekte anında geçerli olur; parola sıfırlandığında kullanıcının açık oturumları kapanır. Son etkin yönetici düşürülemez, pasifleştirilemez ya da silinemez.",

		"usr.m.title":        "Yeni kullanıcı",
		"usr.m.lead":         "Hesap hemen kullanılabilir; ilk girişten sonra kullanıcı kendi parolasını değiştirebilir.",
		"usr.m.username":     "Kullanıcı adı",
		"usr.m.usernameHint": "3–64 karakter, boşluk içermez.",
		"usr.m.password":     "Parola",
		"usr.m.passwordHint": "En az 8 karakter; harf, rakam ve sembol karıştırın.",
		"usr.m.show":         "Parolayı göster",
		"usr.m.submit":       "Oluştur",
		"usr.m.creating":     "Oluşturuluyor…",
	})
	register("en", map[string]string{
		"usr.title":    "Users & Roles",
		"usr.eyebrow":  "Access management",
		"usr.subtitle": "Control who can do what: admins manage the system, analysts run searches and monitoring, viewers can only read.",
		"usr.new":      "New User",
		"usr.roles":    "Role distribution",

		"usr.r.adminS":   "full access + user and settings management",
		"usr.r.analystS": "search, tagging, crawler, monitoring",
		"usr.r.viewerS":  "read-only access",
		"usr.r.admin":    "Full access; manages users and system settings.",
		"usr.r.analyst":  "Write access to search, tagging, crawler and watchlist.",
		"usr.r.viewer":   "Sees findings and dashboards; cannot change anything.",

		"usr.team":        "Team",
		"usr.filterPh":    "Search users…",
		"usr.c.user":      "User",
		"usr.c.role":      "Role",
		"usr.c.lastLogin": "Last sign-in",
		"usr.c.created":   "Created",
		"usr.info":        "Passwords are stored with bcrypt. Role and status changes apply on the user’s next request; resetting a password signs out their open sessions. The last active admin cannot be demoted, disabled or deleted.",

		"usr.m.title":        "New user",
		"usr.m.lead":         "The account is usable right away; the user can change their own password after signing in.",
		"usr.m.username":     "Username",
		"usr.m.usernameHint": "3–64 characters, no spaces.",
		"usr.m.password":     "Password",
		"usr.m.passwordHint": "At least 8 characters; mix letters, digits and symbols.",
		"usr.m.show":         "Show password",
		"usr.m.submit":       "Create",
		"usr.m.creating":     "Creating…",
	})
}
