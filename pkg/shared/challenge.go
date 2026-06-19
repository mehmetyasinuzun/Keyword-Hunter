package shared

import "strings"

// challengeMarkers bir sayfanın doğrulama/captcha/engel sayfası olduğunu ele veren
// işaretler → engel türü.
var challengeMarkers = []struct {
	marker string
	kind   string
}{
	{"just a moment", "Cloudflare"},
	{"checking your browser", "Cloudflare"},
	{"cf-browser-verification", "Cloudflare"},
	{"cf_chl_", "Cloudflare"},
	{"cf-challenge", "Cloudflare"},
	{"attention required", "Cloudflare"},
	{"enable javascript and cookies to continue", "Cloudflare"},
	{"ddos-guard", "DDoS-Guard"},
	{"ddosguard", "DDoS-Guard"},
	{"g-recaptcha", "reCAPTCHA"},
	{"recaptcha", "reCAPTCHA"},
	{"hcaptcha", "hCaptcha"},
	{"h-captcha", "hCaptcha"},
	{"i'm not a robot", "captcha"},
	{"i am not a robot", "captcha"},
	{"robot değil", "captcha"},
	{"ben robot değilim", "captcha"},
	{"verify you are human", "captcha"},
	{"verify you're human", "captcha"},
	{"are you human", "captcha"},
	{"insan olduğ", "doğrulama"},   // "insan olduğunuzu doğrulayın"
	{"doğrulama kodu", "doğrulama"}, // captcha/2FA kodu
	{"güvenlik doğrulama", "doğrulama"},
	{"lütfen doğrulayın", "doğrulama"},
}

// DetectChallenge sayfa başlığı + gövdesinden bir engel/doğrulama sayfası olup
// olmadığını sezer. (found, kind) döndürür; kind örn. "Cloudflare", "captcha".
//
// Yalın metin eşleşmesi — yanlış pozitifi azaltmak için yalnız güçlü, ayırt edici
// işaretler kullanılır.
func DetectChallenge(title, body string) (bool, string) {
	hay := strings.ToLower(title + " \n " + body)
	for _, m := range challengeMarkers {
		if strings.Contains(hay, m.marker) {
			return true, m.kind
		}
	}
	return false, ""
}
