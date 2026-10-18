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
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...   # 0 issues
go run github.com/rhysd/actionlint/cmd/actionlint@latest                         # workflow değiştirdiyseniz
```

`golangci-lint` yapılandırması `.golangci.yml`'dedir (errcheck, govet, staticcheck, ineffassign, unused,
bodyclose, gosec, errorlint, misspell, nolintlint). Bulguyu kodda düzeltin; gerçekten yanlış alarm olan bir
satırda `//nolint:<linter> // <gerekçe>` kullanın (linter adı ve gerekçe zorunludur, nolintlint denetler).
Bir linter'ı depo genelinde kapatmayın.

`pkg/capture`'ın gerçek tarayıcı testleri (sahte SOCKS5 proxy + sahte .onion;
Tor gerekmez) yalnız `CHROME_BIN` verildiğinde koşar, aksi halde sessizce atlanır.
Ekran görüntüsü / JS render koduna dokunduysanız çalıştırın:

```bash
CHROME_BIN=/path/to/chrome go test -race -count=1 -run '^TestBrowser_' ./pkg/capture
```

## Demo verisi

`cmd/khseed` boş bir veritabanını **deterministik**, gerçekçi bir demo veri
kümesiyle doldurur: farklı sorgu / motor / kritiklik / kategoride etiketli
bulgular, IOC'ler, vaka durumları ve analist notları, arama geçmişi, planlı
taramalar, kontrol geçmişli izleme listesi, sağlıklı motor istatistikleri ve
hash zincirli denetim olayları. Kurumlar ve kişiler kurgusaldır (e-postalar
`.example`, IP'ler RFC 5737 belgeleme bloğu). Ekran görüntüsü almak ve arayüzü
gerçekçi veriyle denemek için idealdir.

```bash
make demo                     # data/demo.db'yi baştan kurar ve uygulamayı onunla açar
make seed                     # yalnız tohumla (uygulamayı başlatmaz)
go run ./cmd/khseed -db /tmp/kh.db -reset -admin-pass 'Güçlü-Bir-Parola-1!'
```

- `-reset` mevcut dosyayı (WAL/SHM dahil) silip yeniden kurar; `-reset` olmadan
  dolu bir veritabanına yazılmaz. Aynı `-seed` her zaman aynı içeriği üretir
  (zaman damgaları "şimdi"ye görelidir ki pano dolu gelsin).
- Admin kimliği `-admin-user` / `-admin-pass`, yoksa `-env .env`
  (`ADMIN_USER`, `ADMIN_PASS` ya da `ADMIN_PASS_HASH`), yoksa `ADMIN_PASS`
  ortam değişkeninden gelir; hiçbiri yoksa güçlü bir parola üretilip bir kez
  yazdırılır. `make demo` admin kimliğini `.env`'den alır, `.env`'deki asıl
  `DB_PATH`'e dokunmaz.
- `-db` zorunludur: khseed `.env`'deki `DB_PATH`'e asla düşmez ve `-db`,
  `-env` ile verilen dosyanın `DB_PATH`'iyle aynı dosyaysa hiçbir şeye
  dokunmadan reddeder (uygulama veritabanını silmeye karşı koruma).
- `make demo`, `.env`'deki `WEB_ADDR` ne olursa olsun **yalnız
  `127.0.0.1:${DEMO_PORT:-8080}`** üzerinde dinler; parolası herkesçe bilinen
  demo hesapları ağa açılmaz.
- Demo hesapları — **herkesçe bilinir, yalnız geliştirme/test içindir:**

  | Kullanıcı | Rol | Parola |
  |---|---|---|
  | `analyst` | analyst | `Analyst-Demo-Pass-1!` |
  | `viewer`  | viewer  | `Viewer-Demo-Pass-1!` |

## Uçtan uca testler (Playwright + axe)

`e2e/` gerçek bir sunucuya karşı gerçek Chromium ile koşar ve CI'da (`e2e`
işi) her PR'da çalışır: giriş, her sayfanın koyu/açık tema ve İngilizce'de
**sıfır konsol hatası ve sıfır CSP ihlaliyle** açılması, 390 px'te yatay taşma
olmaması, komut paleti, kalıcı tema, rol kapıları (viewer → markalı 403), API
token yaşam döngüsü (oluştur → Bearer → iptal → 401), 2FA kaydı ve TOTP ile
giriş, denetim kaydı ve **axe-core** erişilebilirlik taraması (WCAG 2.1 A/AA;
`serious`/`critical` ihlal yok — ihlal bastırılmaz, uygulamada düzeltilir).

```bash
# 1) Tohumlanmış veritabanıyla sunucuyu başlatın (ayrı terminal)
go run ./cmd/khseed -db /tmp/kh-e2e.db -reset -admin-pass 'E2e-Admin-Pass-1!'
cat > /tmp/kh-e2e.env <<'ENV'
ADMIN_USER=admin
ADMIN_PASS=E2e-Admin-Pass-1!
WEB_ADDR=127.0.0.1:8080
WEB_SECURE_COOKIES=false
TOR_PROXY=127.0.0.1:9
DB_PATH=/tmp/kh-e2e.db
RATE_LIMIT_RPS=200
RATE_LIMIT_BURST=500
ENV
ENV_FILE=/tmp/kh-e2e.env go run ./cmd

# 2) Testler
cd e2e
npm ci
npx playwright install chromium          # ilk seferde
KH_BASE_URL=http://127.0.0.1:8080 KH_ADMIN_PASS='E2e-Admin-Pass-1!' npx playwright test
npx playwright show-report               # HTML rapor
```

Testler demo verisini değiştirir (yeni kullanıcı, token, 2FA); her koşudan önce
`-reset` ile yeniden tohumlayın. Yeni bir sayfa eklediğinizde
`e2e/tests/helpers.js` → `SIDEBAR_PAGES` listesine de ekleyin; konsol/CSP,
mobil taşma ve axe kontrolleri otomatik kapsar.

## Mimari kısa yol

| Katman | Yer |
|---|---|
| Giriş noktası | `cmd/main.go` |
| Demo verisi | `cmd/khseed` |
| Uçtan uca testler | `e2e/` (Playwright + axe-core) |
| HTTP, şablonlar, statik | `pkg/web` (Gin, `html/template`, gömülü `static/`) |
| Ön yüz sözleşmesi | [`docs/FRONTEND.md`](docs/FRONTEND.md) |
| Depolama | `pkg/storage` (SQLite, saf Go) |
| Şema migrasyonları | `pkg/storage/migrate.go` — yeni adım ekleme: [`docs/MIGRATIONS.md`](docs/MIGRATIONS.md) |
| Arama / kazıma / sınıflandırma | `pkg/search`, `pkg/scraper`, `pkg/cti`, `pkg/artifact`, `pkg/tagging` |
| Arka plan işçileri | `pkg/scheduler`, `pkg/monitor`, `pkg/crawler`, `pkg/capture` |
| i18n | `pkg/i18n/catalog_*.go` (TR birincil, EN tam) |

## Arayüz kuralları (özet)

- Şablonlarda **satır içi betik ve `on*=` işleyici yok** — CSP `script-src 'self'`
  ve `pkg/web/templates_csp_test.go` bunu zorlar. Davranış `data-action` ile
  `static/js/pages/<sayfa>.js` içinden bağlanır.
- **Satır içi stil de yok** — CSP `style-src 'self'`: `<style>`, `style=""` (JS'nin
  ürettiği HTML dahil) ve `setAttribute('style', …)` yasak; sınıf kullanın, dinamik
  değeri eklemeden sonra CSSOM ile atayın (`el.style.width = …`).
- Renkler daima CSS token'ıdır (`var(--primary)`), böylece açık/koyu tema çalışır.
- Kullanıcıya görünen her metin TR + EN katalogda olmalıdır.
- Dark web'den gelen her metin güvenilmezdir: şablonda otomatik kaçış, JS'de
  `KH.esc` / `textContent`.

## Dal koruması (bakımcılar için)

Aşağıdakiler depoda değil GitHub ayarlarında tutulur; **Settings → Rules → Rulesets** (ya da Settings →
Branches → Branch protection rules) altında `master` için elle etkinleştirilmelidir:

- **Doğrudan push yok:** değişiklikler PR ile gelir; tüm konuşmalar çözülmüş olmalı. **Onay sayısı:** tek bakımcı
  varken **0** (PR yazarı kendi PR'ını onaylayamaz; 1 zorunlu onay ve boş bypass listesiyle hiçbir PR birleşemez —
  zorunlu kontroller ve PR şartı korumayı sağlar). İkinci bakımcı geldiğinde **1 onay** ve "yeni commit gelince
  eski onaylar düşer" açın.
- **Zorunlu durum kontrolleri** (iş adlarıyla; "dal güncel olmalı" açık):

  | Workflow | Kontrol adı |
  |---|---|
  | CI | `Test & Vet` |
  | CI | `golangci-lint` |
  | CI | `govulncheck` |
  | CI | `E2E (Playwright + axe)` |
  | CI | `Cross-compile matrix (linux, amd64)` · `(linux, arm64)` · `(darwin, amd64)` · `(darwin, arm64)` · `(windows, amd64)` · `(windows, arm64)` |
  | CodeQL | `CodeQL (go)` · `CodeQL (javascript-typescript)` · `CodeQL (actions)` |

  Rulesets kullanıyorsanız ayrıca **Require code scanning results** → CodeQL, "High or higher" güvenlik
  uyarısında birleştirmeyi engeller.
- **Force push ve dal silme engelli**; bu kurallar ve zorunlu kontroller yöneticiler için de geçerli (bypass
  listesi boş).
- **Tag koruması** (`v*` için tag ruleset): **update** ve **deletion** kısıtlı (yayımlanmış bir sürümün tag'i başka
  commit'e taşınamaz/silinemez). **Creation**'ı da kısıtlarsanız (yalnız bakımcılar) elle çalıştırılan Release
  yolu yeni tag'i oluşturamaz (`GITHUB_TOKEN` bypass listesinde değildir): o durumda tag'i önce bir bakımcı push
  eder, tag push'u sürümü çıkarır. Release workflow'u her `v*` tag push'unda imzalı sürüm üretir; imza kimliği
  workflow'dur, tag'i kimin ittiği değil.
- **Elle sürüm** (Actions → Release → Run workflow) yalnız `master`'dan ya da aynı adlı `v*` tag'inden ve o ref'in
  ucundan çalışır; başka dal ya da geçmiş commit reddedilir (imza ve provenance çalıştırılan ref'i/commit'i
  adlandırır; README'deki doğrulama komutları yalnız `v*` tag'i ve `master` kimliğini kabul eder). Geçmiş bir commit
  için tag'i o commit'e push edin.
- **Actions:** Settings → Actions → General → Workflow permissions = **Read repository contents** (workflow'lar
  ihtiyaç duydukları yazma yetkisini iş bazında ister) ve "Allow GitHub Actions to create and approve pull
  requests" kapalı.

## Commit ve PR

- Küçük, odaklı PR'lar; açıklayıcı commit mesajları (`ui:`, `security:`, `fix:`, `feat:`, `docs:`).
- PR şablonundaki kontrol listesini doldurun; arayüz değişikliklerinde açık ve koyu
  tema ekran görüntüsü ekleyin.
- Güvenlik açıklarını issue olarak değil, [SECURITY.md](SECURITY.md)'deki kanaldan bildirin.

## Etik

Bu yazılım yalnızca yasal ve yetkili tehdit istihbaratı çalışmaları içindir.
Yetkisiz erişimi kolaylaştıran katkılar kabul edilmez.
