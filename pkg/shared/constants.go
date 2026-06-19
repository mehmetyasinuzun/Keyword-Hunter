package shared

import "time"

// ==================== TIMEOUT CONSTANTS ====================

const (
	// HTTP Timeouts
	DefaultHTTPTimeout  = 60 * time.Second
	SearchHTTPTimeout   = 45 * time.Second
	TLSHandshakeTimeout = 30 * time.Second

	// Retry Settings
	MaxRetryAttempts       = 3
	InitialBackoffDuration = 1 * time.Second
	MaxBackoffDuration     = 30 * time.Second
	BackoffMultiplier      = 2.0
)

// ==================== CONTENT LIMITS ====================

const (
	MaxContentChars     = 8000
	MinContentLength    = 100
	MinQualityScore     = 15
	MaxResultsPerEngine = 100

	// MaxResponseBytes HTTP yanıt gövdesi için üst sınır (DoS koruması)
	MaxResponseBytes = 10 << 20
)

// ==================== TOR PROXY ====================

const (
	DefaultTorProxy = "127.0.0.1:9150"
	TorProxyScheme  = "socks5h://"
)

// ==================== USER AGENTS ====================

// UserAgents rotasyon için kullanılacak güncel, gerçekçi User-Agent listesi.
// Hepsi gerçek tarayıcı sürümlerine karşılık gelir; bot/headless izi taşımaz.
var UserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36 Edg/142.0.0.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 14.7; rv:133.0) Gecko/20100101 Firefox/133.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	// Tor Browser standardı (Firefox 128 ESR) — .onion için en iyi kamuflaj,
	// çünkü tüm Tor Browser kullanıcıları aynı UA'yı paylaşır (parmak izi yok).
	"Mozilla/5.0 (Windows NT 10.0; rv:128.0) Gecko/20100101 Firefox/128.0",
}

// ChromeUserAgent chromedp/Chromium ile motor-tutarlı gerçek Chrome UA'sı.
// chromedp varsayılanı "HeadlessChrome" içerir ve anında engellenir; bunu kullan.
const ChromeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// TorBrowserUserAgent gerçek Tor Browser User-Agent'ı (.onion HTTP istemcileri için ideal).
const TorBrowserUserAgent = "Mozilla/5.0 (Windows NT 10.0; rv:128.0) Gecko/20100101 Firefox/128.0"

// ==================== RETRYABLE STATUS CODES ====================

// RetryableStatusCodes yeniden denenebilir HTTP status kodları
var RetryableStatusCodes = map[int]bool{
	500: true, // Internal Server Error
	502: true, // Bad Gateway
	503: true, // Service Unavailable
	504: true, // Gateway Timeout
	429: true, // Too Many Requests
}

// ==================== LOW VALUE PATTERNS ====================

// LowValueURLPatterns düşük değerli URL pattern'leri
var LowValueURLPatterns = []string{
	"/search?",
	"?query=",
	"?q=",
	"/search/",
	"?s=",
	"/t?ad=",
	"/ad/",
	"/ads/",
	"/banner",
	"ad=banner",
	"/tags/",
	"/tag/",
	"/category/",
	"/product-tag/",
	"?page=",
	"&page=",
	"/static/",
	"/assets/",
	"/cdn/",
	"/wp-content/uploads/",
	"/wp-content/",
	"/wp-includes/",
	"/favicon",
	"/robots.txt",
	"/sitemap",
	"/uploads/banner",
}

// ==================== BINARY EXTENSIONS ====================

// BinaryExtensions atlanacak dosya uzantıları
var BinaryExtensions = []string{
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".svg", ".bmp",
	".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
	".zip", ".rar", ".tar", ".gz", ".7z",
	".mp3", ".mp4", ".avi", ".mkv", ".mov", ".wav", ".flac",
	".css", ".js", ".woff", ".woff2", ".ttf", ".eot",
	".exe", ".dll", ".so", ".dmg", ".apk",
}

// ==================== CTI KEYWORDS ====================

// HighValueKeywords CTI için yüksek değerli anahtar kelimeler
var HighValueKeywords = []string{
	"leak", "breach", "dump", "database", "credentials", "password",
	"hack", "exploit", "vulnerability", "0day", "zero-day",
	"ransomware", "malware", "trojan", "botnet", "rat",
	"carding", "fullz", "cvv", "credit card", "bank",
	"forum", "market", "vendor", "escrow",
	"email", "gmail", "outlook", "yahoo",
	"bitcoin", "btc", "monero", "xmr", "wallet",
	"vpn", "proxy", "tor", "anonymity",
	"government", "military", "classified",
	"source code", "api key", "private key", "ssh",
}

// SpamIndicators spam/düşük kaliteli içerik belirteçleri
var SpamIndicators = []string{
	"click here to download",
	"free download",
	"premium account generator",
	"100% working",
	"no survey",
	"skip to content skip to content",
	"cart / $ 0",
	"no products in the cart",
	"showing the single result",
	"add to wishlist",
	"return to shop",
}
