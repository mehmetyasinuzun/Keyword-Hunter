package i18n

func init() {
	register("tr", map[string]string{
		"err.404.title": "Sayfa bulunamadı",
		"err.404.body":  "Aradığınız adres taşınmış, silinmiş ya da hiç var olmamış olabilir. Onion uzayında bile her iz bir yere çıkmaz.",
		"err.403.title": "Bu alana erişiminiz yok",
		"err.403.body":  "Bu sayfa daha yüksek bir rol gerektiriyor. Erişime ihtiyacınız varsa bir yöneticiyle görüşün.",
		"err.home":      "Panele dön",
		"err.back":      "Geri",
	})
	register("en", map[string]string{
		"err.404.title": "Page not found",
		"err.404.body":  "The address may have moved, been removed, or never existed. Even in onion space, not every trail leads somewhere.",
		"err.403.title": "You don't have access here",
		"err.403.body":  "This page requires a higher role. Ask an administrator if you need access.",
		"err.home":      "Back to dashboard",
		"err.back":      "Back",
	})
}
