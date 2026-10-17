# Güvenlik Politikası

## Desteklenen sürümler
En son `master` ve en güncel etiketli sürüm desteklenir.

## Zafiyet bildirimi
Güvenlik açıklarını **herkese açık issue açmadan** özel olarak bildirin:
GitHub → Security → **Report a vulnerability** (private advisory) veya depo
sahibine doğrudan ulaşın. Makul bir sürede yanıt verilecektir.

Lütfen ekleyin: etkilenen sürüm/commit, yeniden üretim adımları, etki değerlendirmesi.

## Kapsam
Bu araç dark web içeriği çeker; **izole ortamda** ve yasal yetki dahilinde çalıştırın.

Tasarım gereği güvenlik önlemleri (v0.15):
- bcrypt kimlik + IP/global login kilitleme, rol tabanlı erişim (admin/analyst/viewer)
- Oturum: HttpOnly + SameSite=Lax, kayan + mutlak ömür, parola değişiminde toplu düşürme
- CSRF (arama formu dahil tüm yazma uçları) + `GET /logout` için Fetch-Metadata koruması
- **Sıkı CSP** `script-src 'self'; style-src 'self'` — hiçbir yönergede `'unsafe-inline'`,
  `'unsafe-eval'`, nonce ya da hash yok. Satır içi betik/olay işleyicisi, `<style>` bloğu ve
  `style=""` özniteliği yok (şablonlarda ve JS'nin ürettiği HTML'de; her şablon ve JS
  dosyası için `templates_csp_test.go` ile, ayrıca tüm sayfaların sunucu çıktısı üzerinde
  zorlanır). Böylece HTML'e sızan bir metin ne betik çalıştırabilir ne de CSS ile veri
  sızdırabilir/arayüzü taklit edebilir. `object-src 'none'`, `frame-ancestors 'none'`,
  X-Frame-Options DENY, nosniff, no-store, HSTS (HTTPS)
- SSRF: derinleştirme/analiz/izleme yalnız `.onion`; webhook/ekran görüntüsü dahili
  adreslere çözümlenemez (DNS sonrası kontrol), yönlendirme takip edilmez
- Dark web metni arayüzde kaçışlanır (XSS); CSV formül-enjeksiyonu koruması
- Tüm dış trafik Tor üzerinden; veri yalnızca yerel SQLite'ta
- CI'da her push'ta `govulncheck`; haftalık Dependabot güncellemeleri

## Yapılandırma önerileri
- `ADMIN_PASS`'ı güçlü bir değerle değiştirin (Docker ilk açılışta rastgele üretir).
- İnternete açıksa HTTPS ters vekil + `WEB_SECURE_COOKIES=true`.
- `WEBHOOK_ALLOW_PRIVATE=false` (kurum içi alıcı gerekmedikçe).
