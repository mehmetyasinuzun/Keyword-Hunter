# Değişiklik Günlüğü

## v0.14.1 — 2026-09-19

### İç kalite / paranoya turu (yeni özellik yok)
- **Güvenlik/RBAC düzeltmesi:** viewer rolü kendi parolasını değiştiremiyordu —
  `/api/me/password` grup-geneli "yazma = analyst+" korumasına takılıp 403 dönüyordu,
  yani ele geçirilmiş bir viewer parolası asla döndürülemezdi. Öz-hizmet ucu dar bir
  istisnayla korumadan muaf tutuldu; gerçek yazma uçları viewer için 403 kalmaya devam
  ediyor. Regresyon testi: `TestRBAC_ViewerCanChangeOwnPasswordButNotWrite`.
- **`rows.Err()` denetimi eklendi (23 fonksiyon):** `rows.Next()` döngülerinde yineleme
  ortasında oluşan DB hatası sessizce yutuluyor, kısmi liste "başarılı" gibi
  dönüyordu. Depo fonksiyonları artık hatayı yayıyor; graf HTTP uçları
  (`/api/graph/*`) kısmi 200 yerine 500 dönüyor. İki en-iyi-çaba yardımcısı
  (`getExpandedNodeIDsByURL`, `watchlistUptime`) bilinçli olarak imza değiştirmeden
  belgelendi.
- Ölü kod: crawler'daki hiç okunmayan `frame.parent` alanı kaldırıldı.
- Vaka notu kesimi bayt yerine rune sınırında (Türkçe çok baytlı karakter bölünmez).
- `update-criticality` içindeki gereksiz `Sprintf` ile kurulmuş SQL düz sabite indirgendi
  (tablo zaten sabitti; enjeksiyon görünümlü kalıp temizlendi).

`go test -race ./...` 13 paket yeşil, gofmt/vet temiz.

## v0.14.0 — 2026-09-19

### Vaka yönetimi (analist iş akışı)
- Bulgulara **vaka durumu** (yeni · inceleniyor · doğrulandı · reddedildi) ve serbest
  **not** eklenebilir. `search_results` tablosuna `case_status` + `note` sütunları
  idempotent göçle eklendi (`EnsureCaseColumns`), `idx_search_results_case` indeksi.
- Bulgular sayfasında **Vaka** filtresi (durum bazlı + "atanmış herhangi") ve satır içi
  renkli durum seçici + not düzenleme düğmesi. `POST /api/results/:id/case`,
  `GET /api/results/case-counts`.
- CSV/JSON dışa aktarımı artık `case_status` ve `note` alanlarını da içeriyor.
- RBAC: durum/not yazma analyst+; viewer salt-okunur.

Testler: `TestCaseManagement` (durum doğrulama, filtre, sayım, güncelleme) yeşil.
`go test -race ./...` 13 paket yeşil, gofmt/vet temiz.


## v0.13.0 — 2026-09-19

### İki dilli arayüz (TR / EN)
- `pkg/i18n` mesaj kataloğu (tr tam, en kapsar; eksik anahtar otomatik tr'ye düşer).
- Dil çerezi + navbar/giriş ekranında **TR/EN** anahtarı; `data-i18n` işaretli
  öğeler istemci tarafında çevrilir (işaretlenmemiş metin Türkçe kalır → FOUC/bozulma yok).
- Navbar ve giriş ekranı tam çevrildi; panel/arama/bulgular başlıkları işaretlendi.
  `GET /api/i18n`, `POST /api/lang`.

### Kararlılık doğrulaması
- ~18.500 istek (5000'i 10 paralel, ~875 req/s) altında bellek 36→38 MB'de sabit
  kaldı (sızıntı yok), 16 OS thread sabit, CPU boşta %0. Örümcek + JS render + çok
  sayfalı arama gerçek Tor ile uçtan uca doğrulandı.

Testler: i18n (fallback zinciri, normalize, katalog). `go test -race ./...` 13 paket yeşil.

## v0.12.0 — 2026-09-19

### Çoklu kullanıcı + roller
- **users** tablosu, üç rol: **admin** (tam yetki + kullanıcı/ayar yönetimi),
  **analyst** (arama/etiketleme/örümcek/izleme/planlı — yazma), **viewer** (salt-okunur).
- .env admin'i açılışta users tablosuna bootstrap admin olarak taşınır; giriş artık
  bcrypt ile DB üzerinden doğrulanır, son giriş zamanı tutulur.
- Rol her istekte tazelenir (rol/pasif değişikliği anında etkin); parola değişince
  ilgili kullanıcının diğer oturumları kapanır; son etkin admin düşürülemez/silinemez.
- Kullanıcı yönetimi sayfası (yalnız admin), kendi parolanı değiştirme (her rol),
  rol tabanlı navbar gizleme ve viewer için salt-okunur arayüz.
- API rol kapıları: yazma işlemleri analyst+, yönetim/ayar/tor/site-profil admin.

### Örümcek iyileştirmesi
- Keşfedilen sayfalar başlık+URL üzerinden CTI ile sınıflandırılarak kaydedilir
  (artık kritiklik 1'de kalmıyor).

Testler: RBAC uçtan uca (analyst/viewer sınırları, son-admin koruması, pasif giriş).
`go test -race ./...` 12 paket yeşil.

## v0.11.0 — 2026-09-19

Kapsam genişletme + özgün tasarım turu.

### Scraper "her senaryoya" yaklaştı
- **JS render**: headless Chromium ile JavaScript çalıştıran sayfalar da çekilebilir.
  JS-only sayfa sezildiğinde veya 403/503 alındığında otomatik tarayıcıya düşülür.
- **Site profilleri**: host bazlı Cookie başlığı + User-Agent + "JS render" bayrağı
  (giriş duvarı/captcha olan siteler için). Ayarlar ekranından yönetilir; çerezler
  yalnızca sunucuda saklanır, listede maskelenir.
- **Motor sayfalama**: Tordex/Tor66/Onionway/OnionLand/Excavator/Danex için 2-5.
  sayfa (canlı doğrulandı: tek sayfa ~299 → 3 sayfa 505 sonuç). Aramada sayfa seçimi.
- **Örümcek (crawler)**: bir tohum .onion'dan BFS ile derinlik (≤3) ve sayfa (≤300)
  sınırlı, nazik (istekler arası gecikme), iptal edilebilir site tarama; keşfedilen
  sayfalar bulgu olarak kaydedilir. Yeni "Örümcek" sayfası.

### Tor devre yönetimi
- **NEWNYM**: Tor kontrol portu üzerinden yeni devre isteği (bekleme süreli).
  Tüm aktif motorlar iki tur üst üste düşerse otomatik tetiklenir. Ayarlar'dan elle
  de istenebilir. `TOR_CONTROL` / `TOR_CONTROL_PASSWORD` ile açılır.

### STIX 2.1 export
- Bulgular ve IOC'ler STIX 2.1 paketi olarak indirilebilir (MISP/OpenCTI/Sentinel/
  Splunk uyumlu): url/email/ip/domain/hash/cryptocurrency/card indicator'ları,
  deterministik kimlikler (yeniden export'ta çift kayıt yok), related-to ilişkileri.
  Bulgular ekranında STIX butonu; `GET /api/export/stix`.

### Özgün tasarım
- **61 parçalık el çizimi SVG ikon seti** (stroke tabanlı, tema renkli, currentColor);
  tüm arayüzdeki ~177 emoji kaldırıldı. Yeni marka logosu (hedef halkası + tarama +
  düğüm izi) ve eşleşen SVG favicon (/favicon.ico 404'ü giderildi).
- Navbar, KPI kartları, butonlar, IOC rozetleri, tablo aksiyonları, toast/onay,
  grafik tooltip'leri ve boş durumlar yeni ikonlarla yeniden çizildi.

### Testler
Yeni: tor (NEWNYM/cooldown/auth), export (STIX şekli/determinizm), site profil ve
crawl storage. `go test -race ./...` 12 paket yeşil, `govulncheck` 0.

## v0.10.1 — 2026-09-19

- Analiz ve Harita sayfaları ortak bileşen setine taşındı (kategori grafiği, IOC özeti,
  domain filtresi/bulgu bağlantısı; harita: tema uyumu, kullanım ipucu, "izleme listesine ekle").
- Çapraz platform: `run.sh` (macOS/Linux), `run.ps1` (Windows), ince `.bat` sarmalayıcılar,
  `Makefile`, `./run.sh release` ile 6 hedef için ikili; Chrome/Chromium otomatik tespiti
  macOS/Windows/Linux için; platform bilgisi açılış logunda.
- `WEBHOOK_ALLOW_PRIVATE`: kurum içi SIEM/SOAR alıcılarına izin (yönlendirme yine kapalı);
  webhook teslimi gerçek HTTP alıcıyla (unit + canlı) doğrulandı, zamanlayıcı gerçek zamanda
  doğrulandı (tick → 154 sonuç → 35 eşik üstü bulgu → genel webhook).
- Yeni testler: notify (teslim, yönlendirme reddi, özel-ağ anahtarı), capture (Chrome tespiti).

## v0.10.0 — 2026-09-18

Tam denetim + sertleştirme + yeniden tasarım sürümü. Tüm değişiklikler gerçek Tor
devresi üzerinden canlı doğrulandı (10/10 motor, 299 sonuç / 11 sn).

### Düzeltilen kritik hatalar
- **graph_nodes sonsuz duplikasyon**: `UNIQUE(url, parent_id)` NULL parent için çalışmıyordu; her arama aynı kök düğümü yeniden ekliyordu. Açılışta birleştirme migrasyonu + `COALESCE(parent_id,0)` ifade indeksi.
- **Planlı taramalar saat dilimi kadar geç çalışıyordu** (`next_run_at` yerel ofsetli, `CURRENT_TIMESTAMP` UTC; ham metin karşılaştırması). `datetime()` normalizasyonu, `_time_format=sqlite`, eski damgalar için migrasyon.
- **Motorlar sayfası tamamen bozuktu** (JSON alanları camelCase, şablon PascalCase okuyordu → `TypeError`).
- **Bulgular satır düzenleme**: kategori değişince kritiklik eski değere dönüyordu (şablona gömülü değerler); artık DOM'daki güncel değer gönderilir.
- **Motor aç/kapat aramayı etkilemiyordu**; artık gerçekten filtreler.
- `scheduled.html` `window.setInterval`'ı gölgeliyordu; aralık sınırları backend ile hizalandı.
- Tamamlanmış etiketleme işi "iptal" ile geçersiz kılınıyordu.
- Kuyruk doluyken iş sessizce askıda kalıyordu → açık hata.
- `.env` tırnaklama asimetrisi (`"` / `\` içeren parolalar /settings sonrası bozuluyordu).
- Rune-güvenli kısaltma (Türkçe başlıklarda `�`).
- SSE akışı 120 sn'de kopuyordu (`WriteTimeout`); `ResponseController` ile kaldırıldı.
- Statik dosyalar hız limitine dahildi → grafik yükleyici 429 alıyordu.
- Ölü .onion'a etiketleme 3×60 sn bekletiyordu → 100 sn üst sınır, 2 deneme.
- Bağlantı kurulumu bağlam iptalini dinlemiyordu (`Dial` → `DialContext`).

### Güvenlik
- bcrypt parola (düz metin `ADMIN_PASS` ilk değişiklikte hash'e dönüşür), IP + global login kilitleme.
- Oturum mutlak ömrü (30 gün), parola değişince diğer oturumlar düşer, `no-store`.
- CSP, Permissions-Policy, COOP/CORP; CDN bağımlılığı kaldırıldı (Chart.js, D3 yerel).
- SSRF: webhook/ekran görüntüsü hedeflerinde dahili adres reddi (DNS sonrası kontrol), yönlendirme yok; izleme listesi yalnızca v3 .onion.
- Dark web başlıkları grafik tooltip/modal'ında kaçışlanıyor (stored XSS kapatıldı).
- `GET /logout` Fetch Metadata ile CSRF'e karşı korundu.
- Docker: Tor portu host'a kapatıldı, uygulama varsayılan 127.0.0.1'e bağlanır, root olmayan kullanıcı, `no-new-privileges`, rastgele ilk parola (admin123 kaldırıldı).
- Bağımlılıklar güncellendi; `govulncheck` 0 zafiyet (quic-go GO-2026-5676 / GO-2025-4233 kapatıldı).
- CSV formül enjeksiyonu koruması; `.env` `.gitignore`'a eklendi.

### Yeni özellikler
- Canlı doğrulanmış motor listesi (10 aktif + 7 pasif/not'lu), kullanıcı seçimlerini koruyan varsayılan güncelleme.
- IOC/artifact çıkarımı (e-posta, BTC, XMR, IP, hash, kart-Luhn, telefon, SSH, API key, onion) + pivot arama + istatistik.
- Bulgular: filtre/sıralama/sayfalama, toplu seçim (etiketle, kritiklik ata, URL kopyala, sil), CSV/JSON export, izleme listesine ekle.
- Genel bildirim merkezi (alert_config artık gerçekten kullanılıyor) + test gönderimi; manuel aramalar da bildirir.
- Türkçe-farkındalıklı sınıflandırma ve etiketleme (Unicode tokenizasyon, TR sinyaller/durak kelimeler).
- İzleme listesi: görünür-metin hash'i ile değişiklik tespiti, ekran görüntüsü bekleme süresi, görüntü zaman çizelgesi, tohum listesi artık opt-in.
- `/healthz`, sürüm bilgisi, klavye kısayolları, responsive navbar, toast/modal/onay bileşenleri, mobil uyum.
- Yeniden tasarlanan Panel / Arama (canlı motor çipleri) / Bulgular / Motorlar / Planlı / İzleme / Ayarlar / Giriş.

### Testler
config, env store, kimlik + kilitleme, auth/CSRF/logout akışı, healthz, search parser, CTI (TR/EN), artifact, storage migrasyonları, motor varsayılanları — `go test -race ./...` yeşil.
