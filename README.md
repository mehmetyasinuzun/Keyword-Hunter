# KeywordHunter — Dark Web Cyber Threat Intelligence Platformu

KeywordHunter, Tor ağındaki dark web arama motorlarını **eşzamanlı** tarayan, bulguları
otomatik sınıflandıran (kritiklik 1-5, kategori), sayfa içeriğinden **IOC/artifact**
(e-posta, kripto cüzdanı, IP, hash, onion adresi…) çıkaran, bilinen onion sitelerini
**izleyen** (uptime, içerik değişikliği, captcha/engel tespiti, ekran görüntüsü) ve yeni
bulguları **webhook** ile bildiren, tek ikili dosyadan oluşan bir CTI aracıdır.

Tüm dış trafik Tor üzerinden çıkar; arayüz ve varlıklar tamamen yerel (CDN yok), veri
yalnızca sizin makinenizde SQLite'ta tutulur.

**Sürüm:** v0.11 · Go 1.26 · SQLite (modernc, saf Go, CGO yok) · Gin · D3.js · Chart.js

---

## Neler yapar?

| Modül | Özellik |
|---|---|
| **Arama** | 10 canlı doğrulanmış onion arama motorunda paralel tarama, canlı motor durumu, otomatik kritiklik/kategori (İngilizce + Türkçe sinyaller), dedup, kayıt |
| **Bulgular** | Filtre (sorgu, metin, motor, kategori, min. kritiklik, etiket), sıralama, sayfalama, toplu seçim (etiketle / kritiklik ata / URL kopyala / sil), CSV & JSON export |
| **Etiketleme + IOC** | Sayfayı Tor'dan çeker, Unicode/Türkçe farkındalıklı anahtar kelime çıkarır, kategori/kritiklik günceller, e-posta / BTC / XMR / IP / hash / kart (Luhn) / SSH / API key / onion IOC'lerini saklar ve pivot aramaya açar |
| **Harita** | Sorgu → motor → sonuç ilişki grafiği (radial / tree / force), düğüm derinleştirme (sayfadaki linkleri çıkarıp graf'a ekler) |
| **Analiz** | Zaman serisi, kaynak/sorgu/kritiklik/kategori dağılımı, domain haritası, sıklık istatistikleri |
| **Planlı Tarama** | Dakika bazlı periyot, yalnızca *yeni* URL'leri diff'leyen çalışma, tarama-özel + genel webhook (Slack/Discord/Teams), test gönderimi |
| **İzleme Listesi** | Onion siteleri için periyodik erişilebilirlik, görünür-metin hash'i ile değişiklik tespiti, Cloudflare/captcha/doğrulama ekranı sezimi, uptime %, tetiklenen ve manuel ekran görüntüsü (Docker) + zaman çizelgesi |
| **Motorlar** | Canlı sağlık, yanıt süresi, başarı oranı, motoru aramaya dahil et / çıkar (gerçekten etkiler); tüm motorlar düşerse otomatik Tor devre yenileme |
| **Örümcek** | Bir tohum .onion'dan derinlik/sayfa sınırlı, nazik BFS site tarama; keşfedilen sayfalar bulgu olur |
| **Site Profilleri** | Host bazlı Cookie + User-Agent + JS render (giriş duvarı/captcha aşımı) |
| **Export** | CSV, JSON ve **STIX 2.1** (MISP/OpenCTI/Sentinel) — IOC'ler ve ilişkiler dahil |
| **Ayarlar** | Kimlik (bcrypt), oturum süresi, hız limiti anında uygulanır; altyapı ayarları .env'ye yazılır |

---

## Hızlı Başlangıç (Docker — önerilen)

Gereksinim: Docker 20.10+ ve Compose v2.

```bash
git clone https://github.com/mehmetyasinuzun/Keyword-Hunter.git
cd Keyword-Hunter
docker compose up -d --build
docker compose logs app | grep -A3 "yönetici parolası"   # ilk kurulumda üretilen parola
```

Windows: `temiz_baslat.bat` aynı adımları yapar.

* Arayüz: **http://localhost:8080** (varsayılan olarak yalnızca bu makineden erişilir)
* Sağlık: `http://localhost:8080/healthz`
* Tor konteyneri yalnızca compose ağına açıktır; host'a port yayınlanmaz.
* LAN/sunucudan erişim için: `KH_BIND=0.0.0.0 docker compose up -d` — bu durumda mutlaka
  güçlü parola ve HTTPS ters vekil (Caddy/nginx) kullanın, `WEB_SECURE_COOKIES=true` yapın.

Kalıcı veriler `./data/` altındadır: `keywordhunter.db`, `.env`, `logs/`, `screenshots/`.
`docker compose down -v && rm -rf data` tüm veriyi siler.

### Yerel çalıştırma — macOS / Linux / Windows (Go 1.26+)

Uygulama saf Go'dur (CGO yok, SQLite gömülü); altı hedefte derlenir ve çalışır:
`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, `windows/amd64`, `windows/arm64`.

| Platform | Başlat | Not |
|---|---|---|
| macOS / Linux | `./run.sh` (veya `make run`) | `.env` yoksa şablondan oluşturur, Tor portunu kontrol eder |
| Windows (PowerShell) | `.
un.ps1` | `winget install GoLang.Go` ile Go kurun |
| Windows (cmd) | `build_and_run.bat` | `run.ps1`'i çağıran ince sarmalayıcı |
| Her platform | `./run.sh release` | `dist/` altına 6 platform için ikili üretir |

Tor: **Tor Browser** açıkken 9150, **tor servisi** 9050 portunu dinler (`brew install tor`,
`apt install tor`, Windows'ta Tor Expert Bundle). `.env` içindeki `TOR_PROXY` bunu göstermeli.

Ekran görüntüsü: makinede Chrome/Chromium/Brave/Edge varsa otomatik bulunur
(macOS `/Applications`, Windows `Program Files`, Linux `/usr/bin`); bulunamazsa
`CHROME_BIN=/yol/chrome` verin. Docker imajında Chromium hazır gelir.

---

## Yapılandırma (`.env` / `data/.env`)

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `ADMIN_USER` | `admin` | Giriş kullanıcı adı |
| `ADMIN_PASS` | — | Düz metin parola. /settings'ten değiştirildiğinde silinir ve `ADMIN_PASS_HASH` yazılır |
| `ADMIN_PASS_HASH` | — | bcrypt hash; verilirse `ADMIN_PASS` gerekmez |
| `WEB_ADDR` | `:8080` | Dinleme adresi |
| `WEB_SECURE_COOKIES` | `false` | HTTPS arkasında `true`. Düz HTTP'de `true` ise tarayıcı çerezi reddeder |
| `SESSION_TTL_HOURS` | `24` | Kayan oturum süresi (1–720); mutlak üst sınır 30 gün |
| `RATE_LIMIT_RPS` / `RATE_LIMIT_BURST` | `25` / `80` | IP başına hız limiti (statik dosyalar muaf) |
| `TOR_PROXY` | `127.0.0.1:9150` | SOCKS5 adresi; Docker'da otomatik `tor:9050` |
| `DB_PATH` | `keywordhunter.db` | SQLite dosyası |
| `LOG_DIR` / `LOG_LEVEL` | `logs` / `info` | Günlük döndürmeli log |
| `WATCHLIST_INTERVAL_MIN` | `15` | İzleme kontrol aralığı |
| `WATCHLIST_SEED` | `none` | `turkey` = gömülü Türk onion listesini otomatik yükle; dosya yolu = JSON seed. UI'dan da yüklenebilir |
| `SCREENSHOT_DIR` | `/data/screenshots` (Docker) / `screenshots` | PNG çıktı dizini |
| `CHROME_BIN` | otomatik | Chrome/Chromium ikili yolu (ekran görüntüsü) |
| `WEBHOOK_ALLOW_PRIVATE` | `false` | `true` ise webhook'lar kurum içi (10.x, 192.168.x, localhost) SIEM/SOAR alıcılarına da gidebilir; internete açık kurulumda kapalı tutun |
| `TZ` (compose) | `Europe/Istanbul` | Arayüzde gösterilen saat dilimi |

Docker'da `ENV_FILE=/data/.env` kullanılır; kökteki `.env` Docker için okunmaz.

---

## Güvenlik modeli

* Tek yönetici hesabı, **bcrypt** parola, IP başına ve global **login kilitleme** (5 hata → üstel kilit).
* Oturum çerezi `HttpOnly + SameSite=Lax`, kayan + mutlak ömür; parola değişince diğer oturumlar düşer.
* API yazma uçlarında **CSRF token**; `GET /logout` siteler-arası tetiklemeye karşı Fetch Metadata ile korunur.
* **CSP** (`default-src 'self'`, harici script/iframe yok), `X-Frame-Options: DENY`, `nosniff`, `no-store`, HSTS (HTTPS'te).
* **SSRF koruması**: derinleştirme/analiz/izleme yalnızca `.onion`; webhook ve ekran görüntüsü hedefleri dahili/loopback/link-local adreslere çözümlenemez (DNS sonrası kontrol), yönlendirme takip edilmez. Kurum içi alıcı gerekiyorsa `WEBHOOK_ALLOW_PRIVATE=true` (yönlendirme yine kapalı).
* Dark web'den gelen tüm metinler arayüzde kaçışlanır (stored XSS yok); CSV export formül enjeksiyonuna karşı korumalı.
* Docker: root olmayan kullanıcı, `no-new-privileges`, Tor portu host'a kapalı, `govulncheck` temiz.

Bu araç saldırgan içerik barındıran siteleri ziyaret eder. Yalnızca izole bir ortamda
ve yasal yetkiniz dahilinde kullanın; kimlik bilgilerini paylaşmayın.

---

## Arama motorları

Liste 2026-09-18'de gerçek Tor devresi üzerinden, üç turda ve iki sorguyla doğrulandı
(`pkg/search/engines.go`). Varsayılan aktif: **Tordex, Amnesia, Tor66, Onionway, OnionLand,
Torland, Excavator, TorNet, Submarine, Danex**. Captcha isteyen (OSS, Torgle), JavaScript
gerektiren (Torgol), boş dizinli (DeepSearches) veya erişilemeyen (Torch, Ahmia onion,
FindTor) motorlar pasif gelir; `/monitor` ekranından açılabilir. Motor listesi
değiştiğinde kullanıcı özelleştirmeleri korunur.

---

## API (oturum + CSRF gerekir)

`GET /api/results?q=&text=&source=&category=&minCriticality=&tag=&sort=&limit=&page=` ·
`GET /api/export/results?format=csv|json` · `POST /api/results/delete` ·
`POST /api/auto-tag` · `POST /api/batch-auto-tag` · `GET /api/results/:id/artifacts` ·
`GET /api/artifacts?type=&value=` · `GET /api/artifacts/stats` · `GET /api/engines` ·
`POST /api/engines/:name/toggle` · `GET|POST /api/scheduled` · `POST /api/scheduled/:id/run-now` ·
`GET|POST /api/alert-config` · `POST /api/alert-config/test` · `GET|POST /api/watchlist` ·
`POST /api/watchlist/:id/check` · `POST /api/screenshot` · `GET /api/screenshots` ·
`GET /api/analytics` · `GET /api/graph/*` · `GET /events` (SSE) · `GET /healthz` (açık).

---

## Geliştirme

```bash
go build ./... && go vet ./... && go test -race ./...
```

Testler: yapılandırma/env deposu, kimlik + kilitleme, auth/CSRF/logout akışı, arama
parser'ı, CTI sınıflandırıcı (TR/EN), artifact çıkarımı, storage migrasyonları
(graph_nodes dedup, zaman damgası normalizasyonu, saat dilimi güvenli zamanlayıcı),
motor varsayılanı/kullanıcı seçimi.

Mimari: `cmd/main.go` → `pkg/config` (.env) → `pkg/storage` (SQLite) → `pkg/search`
(motorlar, parser) · `pkg/scraper` (Tor HTTP, metin, etiket) · `pkg/cti` (sınıflandırma) ·
`pkg/artifact` (IOC) · `pkg/tagging` (iş kuyruğu) · `pkg/scheduler` · `pkg/monitor` ·
`pkg/capture` (chromedp) · `pkg/notify` (webhook) · `pkg/web` (Gin, şablonlar, statik).

## Yasal uyarı

Bu yazılım güvenlik araştırmacıları ve SOC/CTI ekipleri içindir. Yetkisiz erişim veya
yasadışı faaliyet için kullanılamaz; sorumluluk kullanıcıya aittir.

**Geliştirici:** Mehmet Yasin Uzun
