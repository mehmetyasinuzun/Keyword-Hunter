package shared

import "net/url"

// RedactURL bir URL'nin gizli kısımlarını (kullanıcı bilgisi, yol, sorgu) gizler;
// yalnız şema ve ana makineyi bırakır. Slack/Discord webhook adresleri yolda
// taşıyıcı-benzeri bir sır taşır — log ve düşük yetkili yanıtlarda bu kullanılır.
func RedactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<gizlendi>"
	}
	out := u.Scheme + "://" + u.Host
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		out += "/…"
	}
	return out
}
