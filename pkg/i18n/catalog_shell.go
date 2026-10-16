package i18n

// Uygulama kabuğu (yan menü, üst bar, komut paleti, ortak bileşenler).
func init() {
	register("tr", map[string]string{
		"shell.group.ops": "Operasyon", "shell.group.auto": "Otomasyon", "shell.group.system": "Sistem",
		"shell.skip":        "İçeriğe geç",
		"shell.collapse":    "Menüyü daralt",
		"shell.menu":        "Menü",
		"shell.palette":     "Komut paleti",
		"shell.paletteHint": "Sayfa, eylem ya da arama…",
		"shell.shortcuts":   "Klavye kısayolları",
		"shell.engines":     "motor",
		"shell.signedInAs":  "Oturum",
		"shell.footer":      "Tüm trafik Tor üzerinden · Veriler yalnızca bu makinede",
		"nav.scheduledLong": "Planlı Taramalar", "nav.watchlistLong": "İzleme Listesi",
		"nav.monitorLong": "Arama Motorları", "nav.crawlLong": "Örümcek",
		"pal.goto": "Git", "pal.action": "Eylem", "pal.searchDark": "Dark web'de ara",
		"pal.filterFindings": "Bulgularda filtrele", "pal.noMatch": "Eşleşme yok",
		"pal.toggleLang": "Dili değiştir (TR/EN)", "pal.logout": "Oturumu kapat",
		"pal.criticalFindings": "Kritik bulgular (Sev 4+)", "pal.newSchedule": "Yeni planlı tarama",
		"kbd.title": "Klavye kısayolları", "kbd.palette": "Komut paleti", "kbd.search": "Arama sayfası",
		"kbd.goto": "Sayfaya git (ardından harf)", "kbd.help": "Bu yardım", "kbd.close": "Kapat / iptal",
	})
	register("en", map[string]string{
		"shell.group.ops": "Operations", "shell.group.auto": "Automation", "shell.group.system": "System",
		"shell.skip":        "Skip to content",
		"shell.collapse":    "Collapse menu",
		"shell.menu":        "Menu",
		"shell.palette":     "Command palette",
		"shell.paletteHint": "Page, action or search…",
		"shell.shortcuts":   "Keyboard shortcuts",
		"shell.engines":     "engines",
		"shell.signedInAs":  "Signed in",
		"shell.footer":      "All traffic via Tor · Data stays on this machine",
		"nav.scheduledLong": "Scheduled Scans", "nav.watchlistLong": "Watchlist",
		"nav.monitorLong": "Search Engines", "nav.crawlLong": "Crawler",
		"pal.goto": "Go to", "pal.action": "Action", "pal.searchDark": "Search the dark web for",
		"pal.filterFindings": "Filter findings for", "pal.noMatch": "No matches",
		"pal.toggleLang": "Switch language (TR/EN)", "pal.logout": "Sign out",
		"pal.criticalFindings": "Critical findings (Sev 4+)", "pal.newSchedule": "New scheduled scan",
		"kbd.title": "Keyboard shortcuts", "kbd.palette": "Command palette", "kbd.search": "Search page",
		"kbd.goto": "Go to page (then a letter)", "kbd.help": "This help", "kbd.close": "Close / cancel",
	})
}
