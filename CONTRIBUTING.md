# Katkı Rehberi

KeywordHunter'a katkı vermek istediğiniz için teşekkürler! Bu proje SOC/CTI
ekiplerinin günlük kullandığı bir araçtır; bu yüzden **güvenlik**, **kararlılık**
ve **tutarlı kullanıcı deneyimi** önceliklidir.

## Geliştirme ortamı

```bash
git clone https://github.com/mehmetyasinuzun/Keyword-Hunter.git
cd Keyword-Hunter
cp .env.example .env          # ADMIN_PASS'ı değiştirin
./run.sh                      # ya da: make run
```

Tor (Tor Browser 9150 ya da tor servisi 9050) olmadan da arayüz açılır; arama ve
izleme istekleri zarifçe hata verir.

## Kalite kapısı (CI ile aynı)

```bash
gofmt -l cmd pkg              # çıktı boş olmalı
go vet ./...
go test -race -count=1 ./...
go mod tidy -diff
for f in $(find pkg/web/static/js -name '*.js' -not -path '*/vendor/*'); do node --check "$f"; done
```

## Mimari kısa yol

| Katman | Yer |
|---|---|
| Giriş noktası | `cmd/main.go` |
| HTTP, şablonlar, statik | `pkg/web` (Gin, `html/template`, gömülü `static/`) |
| Ön yüz sözleşmesi | [`docs/FRONTEND.md`](docs/FRONTEND.md) |
| Depolama | `pkg/storage` (SQLite, saf Go) |
| Arama / kazıma / sınıflandırma | `pkg/search`, `pkg/scraper`, `pkg/cti`, `pkg/artifact`, `pkg/tagging` |
| Arka plan işçileri | `pkg/scheduler`, `pkg/monitor`, `pkg/crawler`, `pkg/capture` |
| i18n | `pkg/i18n/catalog_*.go` (TR birincil, EN tam) |

## Arayüz kuralları (özet)

- Şablonlarda **satır içi betik ve `on*=` işleyici yok** — CSP `script-src 'self'`
  ve `pkg/web/templates_csp_test.go` bunu zorlar. Davranış `data-action` ile
  `static/js/pages/<sayfa>.js` içinden bağlanır.
- Renkler daima CSS token'ıdır (`var(--primary)`), böylece açık/koyu tema çalışır.
- Kullanıcıya görünen her metin TR + EN katalogda olmalıdır.
- Dark web'den gelen her metin güvenilmezdir: şablonda otomatik kaçış, JS'de
  `KH.esc` / `textContent`.

## Commit ve PR

- Küçük, odaklı PR'lar; açıklayıcı commit mesajları (`ui:`, `security:`, `fix:`, `feat:`, `docs:`).
- PR şablonundaki kontrol listesini doldurun; arayüz değişikliklerinde açık ve koyu
  tema ekran görüntüsü ekleyin.
- Güvenlik açıklarını issue olarak değil, [SECURITY.md](SECURITY.md)'deki kanaldan bildirin.

## Etik

Bu yazılım yalnızca yasal ve yetkili tehdit istihbaratı çalışmaları içindir.
Yetkisiz erişimi kolaylaştıran katkılar kabul edilmez.
