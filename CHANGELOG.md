# Değişiklik Günlüğü

## v0.17.0 — unreleased

### Test ve fuzz
- Veri toplama çekirdeğine davranışsal testler (Tor'suz: httptest sunucusu, `.onion` adlarını test
  sunucusuna bağlayan dialer, bellek içi site grafı). Kapsam (`go test -cover`, önce → sonra):
  scraper %3,9 → %94,3 · monitor %6,4 → %94,2 · scheduler %11,2 → %87,5 · tagging %31,2 → %88,7 ·
  crawler %35,0 → %93,2 · storage %46,8 → %86,4 · shared %48,1 → %90,0 · logger %46,0 → %92,0 ·
  search %56,8 → %93,9 · artifact %69,7 → %76,9.
- Kapsanan davranışlar: motor sonuç sayfası ayrıştırma (`pkg/search/testdata/engines/` altında Tordex,
  Ahmia, Torch ve düz metin listesi biçimini taklit eden HTML örnekleri), tekilleştirme ve sayfalama
  (yeni sonuç gelmeyen sayfada durma); planlı taramada due zamanı/sırası ve "yalnız yeni URL" farkı
  (webhook ve genel bildirim eşikleri); motor izleyicide up/down sınıflandırması, art arda düşüş
  sayaçları ve iki tur tam-düşükte Tor devre yenileme kancası; örümcekte BFS derinlik/sayfa sınırı,
  aynı-host ve yalnız-onion kuralları, iptal ve kuyruk taşması; toplu etiketlemede kaldığı yerden devam,
  iptal, kapanışta duraklatma; storage'da filtre/LIKE kaçışı/sıralama/sayfalama, vaka durumu, etiket ve
  IOC sorguları, izleme listesi uptime/değişiklik, graf ağacı, analitik toplamları.
- Küçük test dikişleri (davranış değişmez): scheduler arama arayüzü, örümcek bağlantı çekici + gecikme
  alanı, motor izleyicide motor listesi alanı.
- 11 yerel Go fuzz hedefi (düşmanca dark web girdisi): `FuzzExtract` (IOC), `FuzzHTMLToText`,
  `FuzzExtractLinks`, `FuzzNormalizeURL`, `FuzzExtractTopKeywordsFromHTML`, `FuzzParseResults`,
  `FuzzVisitKey`, `FuzzExtractDomain`, `FuzzTruncate`, `FuzzDetectChallenge`, `FuzzNormalizeTags`. Her
  biri gerçek değişmezler doğrular (sınırlı/geçerli UTF-8 çıktı, yalnız http(s) bağlantı, sabit nokta,
  determinizm); yerelde her biri 60 sn koştu. Bulunan girdiler `testdata/fuzz/` altında regresyon tohumu.
- CI (Test & Vet): `^func Fuzz` ile otomatik bulunan her hedef 10 sn koşar; çökme girdileri artefakt
  olarak yüklenir.
- Düzeltilen hatalar:
  - **Graf kilitlenmesi:** bir sonuç düğümü derinleştirildikten sonra `/graph` (`GetGraphData`), açık
    result-set varken çocuk sorgusu attığı için tek DB bağlantısında sonsuza dek bekliyor ve tüm
    veritabanı erişimini kilitliyordu. Satırlar önce belleğe alınıyor.
  - Toplu etiketleme: kapanış sırasında gelen `Submit`/kurtarma kapalı kuyruğa gönderip panik ediyordu;
    örümcekte aynı kontrol-sonra-gönder yarışı. Kanal artık kilit altında kapanıyor.
  - Motor izleyici: "Şimdi kontrol et" ile zamanlayıcı turu `downStreak` üzerinde yarışıyordu (veri yarışı).
  - Örümcek: son sayfa çekilirken iptal edilen iş "completed" yazılıyordu; parçadan önceki boşluk
    yüzünden aynı sayfa iki kez geziliyordu.
  - Bağlantı çıkarımı: `<script>` içindeki `<a href>` dizgileri hayalet bağlantı üretip gerçek bağlantıyı
    yutuyordu; `href`'teki `&amp;` çözülmüyordu (sorgu parametreleri bozuluyordu); başlıktaki varlıklar
    çözülmüyordu; `JavaScript:`/`data:`/`ftp:`/`file:`, host'suz `http:0` ve geri ayrıştırılamayan
    `//::` bağlantıları graf/örümceğe sızıyordu.
  - Etiketleme: CTI anahtar kelimeleri alt dizgi olarak aranıyordu ("administrator" → `rat`, `tor`);
    tam kelime eşleşmesine geçildi; arama sorgusu ayrıca dışlansa da CTI listesinden geri ekleniyordu.
  - IOC bağlamı (50 bayt pencere), artifact bağlamı (300 bayt) ve etiketleme metni (200 KB) çok baytlı
    karakteri ortadan bölüp geçersiz UTF-8 saklıyordu (Türkçe sayfalar); `shared.TruncateBytes` eklendi.
  - `ReplaceArtifacts` yok sayılan yinelenen IOC'leri de sayıyordu; analitikte ortalama hit tam sayı
    olmayan ortalamada her zaman 0 görünüyordu (`AVG` REAL → int taraması hatası yutuluyordu).
  - `ExtractDomain` `#` parçasını alan adına katıyordu (`x.onion#top`): iç bağlantı dış sayılıyordu.
  - Motor sonuçlarında URL'den türeyen başlık ve URL sınırsızdı (saldırgan motor sayfası): başlık 150
    rune, URL 2048 bayt. Etiket ayrıştırması (`splitTags`) karesel dize birleştirmeden kurtarıldı.

### Sürümlü şema migrasyonları
- Şema artık `PRAGMA user_version` ile sürümlenir: `pkg/storage/migrate.go`'da sıralı, yalnız-ekleme
  migrasyon listesi. Her adım kendi işleminde (`BEGIN IMMEDIATE`) çalışır ve sürüm aynı işlemde artar;
  başarısız adım tümüyle geri alınır. Aynı dosyayı aynı anda açan iki süreç adımı bir kez uygular.
- Migrasyon 1 (`baseline`): v0.16'nın boş veritabanında ürettiği şemanın birebir aynısı (tablolar,
  indeksler, denetim ve TAXII tetikleyicileri). Dağınık `CREATE … IF NOT EXISTS` + hatası yutulan
  `ALTER TABLE … ADD COLUMN` çağrıları (`db.go`, `engine_stats.go`, `results.go`, `screenshots.go`,
  `taxii.go`, kullanıcı/2FA/token/denetim/MISP şemaları) kaldırıldı.
- Sürümsüz eski veritabanları (v0.17 öncesi) açılışta **benimsenir**: eksik sütunlar yalnız yoksa
  eklenir, eksik indeks/tetikleyiciler kurulur, graf kök düğümü yinelenenleri birleştirilir (tek satırda
  toplanır, çocukları korunan satıra bağlanır — silinen tek satırlar bunlardır), `feed_at` geri
  doldurulur, eski zaman damgaları normalize edilir. v0.3/v0.4'te oluşturulmuş veritabanlarında
  `search_results` taban sütun sırasıyla yeniden kurulur (`auto_tags` orada `created_at`'in arkasındaydı);
  id'ler ve AUTOINCREMENT sayacı korunur, böylece her benimsenen veritabanı yeni veritabanıyla birebir aynı
  şemaya ulaşır. `audit_events`'e dokunulmaz, hash zinciri geçerli kalır.
- Başka tabloların başvurduğu bir tabloyu yeniden kuran adım için `foreignKeysOff` bayrağı: yabancı
  anahtarlar `BEGIN`'den önce kapatılır (açıkken `DROP TABLE` etiket, IOC, MISP, 2FA ve kurtarma kodu
  satırlarını `ON DELETE CASCADE` ile sessizce silerdi), `COMMIT`'ten önce `foreign_key_check` yeni ihlal
  ararsa adım geri alınır, sonra anahtarlar yeniden açılır. Taban adım bu bayrakla çalışır;
  `docs/MIGRATIONS.md` buna göre düzeltildi.
- Migrasyon 3 (`search_results.last_seen_at`) ve 4 (`screenshots(file_path)` indeksi) saklama süresi için.
- Aynı yeni dosyayı aynı anda ilk kez açan iki süreç WAL'a geçişte `SQLITE_BUSY` alabiliyordu (meşgul
  işleyici bu kilitlenmede beklemez); ilk bağlantı yalnız bu hata için kısa aralıklarla yeniden denenir.
- **Sürüm düşürme koruması:** bu ikilinin bildiğinden yeni bir şema sürümüne sahip veritabanı açılmaz
  (`storage.ErrSchemaTooNew`, açık hata iletisi); dosyaya dokunulmaz.
- Testler: taban şema ile v0.16 şemasının birebir eşitliği; v0.4, v0.9 ve v0.16 şemalarından (git
  geçmişinden dökülmüş `testdata/legacy_v*.sql` fikstürleri) ve v0.4'te doğup v0.16'ya yükseltilmiş bir
  veritabanından benimsenen veritabanının yeni veritabanıyla aynı şemaya (normalize `sqlite_master` +
  sütun listeleri) ulaşması, satırların (çocuk satırlar dahil) ve denetim zinciri doğrulamasının
  korunması; `foreignKeysOff` adımında üst tablo yeniden kurulurken çocukların korunması ve yeni ihlalin
  geri alınması; yeni sürümün reddi; başarısız adımın geri alınması; idempotent yeniden açılış; eşzamanlı
  açılış. Yeni migrasyon ekleme rehberi: `docs/MIGRATIONS.md`.
- **Yükseltme notu (v0.16 → v0.17):** ilk açılış veritabanını tek yönlü benimser (eski ikiliye dönüş
  yoktur). Yeni ikiliyi ilk kez başlatmadan önce onunla migrasyonsuz bir yedek alın:
  `keywordhunter backup -out v0.17-oncesi.db` (Docker: `docker compose run --rm --no-deps app
  /app/keywordhunter backup -out /data/v0.17-oncesi.db`).

### Yedekleme ve geri yükleme
- Çevrimiçi, tutarlı yedek: SQLite `VACUUM INTO` ayrı ve kısa ömürlü bir bağlantıyla (uygulamanın tek bağlantılı
  havuzu ve yazarlar beklemez); her yedek `PRAGMA integrity_check` ile doğrulanır. Yedekten oturumlar
  `secure_delete` ile silinir (oturum kimlikleri taşıyıcı kimlik bilgisidir); temizlik `file:` URI ve `mode=rw`
  ile tam olarak yedek dosyasına uygulanır (yolda `?` olsa da), son dosyada yeniden sayılarak doğrulanır ve
  doğrulanamazsa yedek başarısız sayılır. Site profili çerezleri ve webhook adresleri yedekte açık metindir;
  README bunu açıkça belirtir.
- Ayarlar → **Yedekleme** (yalnız admin): zamanlanmış yedeğin durumu (dizin, aralık, tutulan sayı, dizindeki yedek
  sayısı, son çalışma/son başarı ve hata) ve **Şimdi yedek indir** düğmesi (`data-action`, CSP uyumlu, TR/EN).
  `POST /api/admin/backup` veritabanının yanında (aynı birim) geçici bir kopya üretir, adını siler ve
  `keywordhunter-<sürüm>-<UTC zaman>.db` olarak akıtır; CSRF + Fetch Metadata korumalı, yalnız etkileşimli admin
  oturumu (`/api/admin` API token'a kapalı). Sunucunun 5 dk yazma süresi bu uç için üretimde 30 dk'ya, aktarımda
  boyuta göre uzatılır. `backup.download` aktarımdan sonra denetlenir (dosya adı, boyut, gönderilen bayt,
  tamamlandı mı; yarıda kesilen indirme başarısız). Aynı anda tek yedek üretilir (`409 backup_busy`).
- Zamanlanmış yedek: `BACKUP_DIR` (boş = kapalı), `BACKUP_INTERVAL_HOURS` (24), `BACKUP_KEEP` (7). Atomik yazım
  (0600 geçici dosya → doğrulama → fsync → yeniden adlandırma), en eskiden döndürme (yalnız bu adlandırmadaki
  dosyalar; az önce yazılan yedek saat geri alınmış olsa bile silinmez, ileri tarihli yedekler loglanır), son
  çalışma `maintenance_runs` tablosunda (şema migrasyonu 2), sonuç loglanır ve `backup.scheduled` olarak
  denetlenir; başarısızlıkta 1 saat sonra yeniden dener, yeniden başlatma aralığı sıfırlamaz (gelecekte görünen son
  başarı en çok bir aralık erteler). Kapanış süren yedeği iptal edip bekler (geçici dosya silinir); öldürülen
  süreçlerin `.keywordhunter-backup-*.tmp` artıkları açılışta, indirmede ve her zamanlanmış yedekte süpürülür.
  Ayarlar ve log mutlak dizini gösterir; Docker giriş betiği şablondaki göreli `BACKUP_DIR=backups`'ı kalıcı
  `/data/backups`'a taşır (aksi hâlde kapsayıcı yeniden oluşturulunca silinen katmana yazılırdı).
- Ayarlar durumu yanıltmaz: başarısız yedekte yazılmamış dosya adı gösterilmez; zamanlanmış yedek kapatılınca
  açma ipucu görünür, önceki çalışma geçmiş olarak etiketlenir.
- Komut satırı: `keywordhunter backup [-out f.db] [-db kaynak.db]` (migrasyonsuz, var olan dosyanın üzerine yazmaz,
  kaynağı salt okunur açar: günlük kipini değiştirmez, arşivlenmiş bir yedeğin kopyasını alırken dosyaya yazmaz)
  ve `keywordhunter verify-db [-json] f.db` (integrity_check + denetim zinciri + şema sürümü; sorun → çıkış kodu 1).
- README (TR/EN): Yedekleme ve geri yükleme adımları (Docker'da durmuş uygulama için `docker compose run`
  komutları, eski veritabanının WAL'ıyla birlikte kenara alınması, CLI çıktısının Türkçe olduğu), v0.16'dan
  yükseltme notu, `APP_SECRET`'ın yedekten ayrı saklanması ve farklı `APP_SECRET` ile geri yüklemede 2FA/MISP
  sırlarının durumu (kurtarma kodu / admin sıfırlaması).

### Saklama süresi: bulgular ve ekran görüntüleri
- `RESULT_RETENTION_DAYS` (varsayılan `0` = süresiz): günlük temizlik, bir aramanın **son döndürdüğü** andan bu
  yana süresi dolan bulguları siler (`last_seen_at`; aramalarda hâlâ çıkan bulgu silinip sonra "yeni" olarak geri
  gelmez, uyarı/MISP/TAXII yinelenmez); vaka durumu **İnceleniyor** (`investigating`) ya da **Doğrulandı**
  (`confirmed`) olanlar tutulur. Bağlı satırlar tutarlı gider: etiketler, IOC'ler ve MISP gönderim kayıtları
  (yabancı anahtar, `ON DELETE CASCADE`), URL'nin graf kökü ve tüm derinleştirme alt ağacı, o URL'nin ekran
  görüntüsü satırları ve PNG dosyaları — yalnız URL'yi kullanan başka bulgu kalmadıysa ve URL izleme listesinde
  değilse. 500'lük işlemlerle (tek yazar uzun kilitlenmez; dosya yolu araması indeksli, karesel tarama yok).
- Silme güvenli: temizlik `PRAGMA secure_delete` açık ayrı bir bağlantıda yapılır, uygulama bağlantısı
  `secure_delete=FAST` kullanır ve çalışma sonunda WAL kesilir; silinen bulgu metni veritabanı dosyasında okunur
  kalmaz. Önceki yedekler etkilenmez (README).
- `SCREENSHOT_RETENTION_DAYS` (varsayılan `0`): daha eski ekran görüntüsü satırları ve dosyaları silinir;
  İnceleniyor/Doğrulandı bir vakanın URL'sine ait görüntüler (kanıt) tutulur. Dosyalar yalnız ekran görüntüsü
  dizininde, yalnız dosya adıyla ve her parti işlendikten hemen sonra silinir (yarıda kesilen çalışma sahipsiz PNG
  bırakmaz); kapanış kalan partileri atlar.
- Her çalışma loglanır, `retention.purge` olarak denetlenir (yalnız sayımlar: bulgu, etiket, IOC, graf düğümü, ekran
  görüntüsü, dosya, tutulan vaka) ve son durumu `maintenance_runs`'a yazılır. Ayarlar → **Saklama süresi** (admin)
  etkin politikayı (`AUDIT_RETENTION_DAYS` dahil), her zaman tutulan vaka durumlarını ve son temizliği gösterir;
  silinemeyen dosyalar uyarı olarak, başarısızlıkta son başarılı temizlik görünür. Yeniden başlatma günlük aralığı
  sıfırlamaz.
- Testler sahte saatle: eski/yeni ayrımı, son görülmeye göre yaş (yeniden görülen bulgu kalır, bilinen URL ve MISP
  kaydı korunur), korunan vaka durumları, diskten silinen dosyalar (parti başına), iptal, silinen metnin dosyada
  kalmaması, indeksli sorgu planı, bağlı satırlar ve graf alt ağacı (yabancı anahtar ihlali yok), paylaşılan URL ve
  izleme listesi istisnaları, sayımlar, `0` ile kapalı; bakım işçilerinin kısa gecikmeyle kendiliğinden çalışması ve
  kapanışta durması.

### Tedarik zinciri
- **CodeQL** (`.github/workflows/codeql.yml`): Go (gerçek `go build ./...` ile, `build-mode: manual`),
  JavaScript/TypeScript ve GitHub Actions; `security-extended` sorguları; `master`'a push, PR ve haftalık koşu. İş
  yetkileri yalnız `security-events: write`, `contents: read`, `actions: read`.
- **golangci-lint v2** (`.golangci.yml`, CI'da `golangci-lint` işi, linter setup-go'nun Go 1.27 araç zinciriyle
  derlenir): errcheck, govet, staticcheck, ineffassign, unused, bodyclose, gosec, errorlint, misspell, nolintlint.
  Mevcut ~470 bulgunun tamamı kodda düzeltildi; istisnalar dar kapsamlı ve gerekçeli (errcheck için yalnız
  `sql.Rows/Stmt.Close`, `sql.Tx.Rollback` ve `fmt.Fprint*`; i18n kataloglarında ve testlerde belirli gosec
  kuralları; satır düzeyindeki her `//nolint` linter adı + gerekçe taşır). misspell Türkçe sözcükler ve kalıcı
  `cancelled` durum kodu için ayarlı.
- **Sürümler imzalı ve izlenebilir** (`release.yml`): ikililerin SPDX SBOM'u (`keywordhunter-<sürüm>.spdx.json`,
  `SHA256SUMS.txt`'ye dahil) ve imajın SPDX SBOM'u (`…-image.spdx.json`) release'e eklenir; `SHA256SUMS.txt`
  cosign ile anahtarsız imzalanır (`SHA256SUMS.txt.sigstore.json`, yayından önce workflow kimliğiyle doğrulanır);
  imaj etiketle değil özetle imzalanır (`cosign sign ghcr.io/…@sha256:…`); `dist/*` ve imaj için GitHub build
  provenance attestation'ı (imajınki registry'ye itilir); imaj SBOM'u ayrıca imaj özetine SBOM attestation'ı olarak
  bağlanır. İmzalar yayından önce README'deki kimlik kuralıyla (yalnız `v*` tag'i ya da `master`) doğrulanır.
  Doğrulama komutları README → **Sürümleri doğrulama** (`gh attestation verify` de aynı ref kuralını şart koşar).
- Elle sürüm yalnız `master`'dan (ya da aynı adlı tag'den) ve o ref'in ucundan çalışır: başka dal, incelenmemiş ya
  da geçmiş bir commit imzalı/attestation'lı sürüme dönüşemez ve provenance hep derlenen commit'i adlandırır.
  Geçmiş bir commit için tag push yolu kullanılır.
- Tüm workflow'lardaki action'lar commit SHA'sına sabitlendi (`# vX.Y.Z` yorumuyla; Dependabot günceller): taşınan
  bir etiket imza kimliğini kullanamaz.
- Release workflow yetkileri iş bazına indirildi (üst düzey yalnız `contents: read`; `id-token`/`attestations`
  yalnız imzalayan işlerde); Docker işi ikili işini bekler (tag/ref doğrulaması imaj itilmeden önce yapılır);
  sürüm derlemesi Go modül önbelleği kullanmaz; checkout kimlik bilgisini saklamaz.
- Release notlarındaki göreli Markdown bağlantıları tag'e sabit mutlak adrese çevrilir
  (`https://github.com/<repo>/blob/<tag>/<yol>`, görseller `raw/`, başlıklı bağlantılar dahil; kod blokları ve
  `kod` aralıkları, `../` bağlantılar olduğu gibi kalır).
- CONTRIBUTING: `master` için önerilen dal koruması (zorunlu kontroller iş adlarıyla, tek bakımcıya uygun onay
  sayısı, tag koruması ve elle sürüm yoluyla ilişkisi, Actions yetkileri) — bakımcıların GitHub ayarlarında
  etkinleştirmesi gerekir. Workflow'lar `actionlint` + shellcheck temiz.
- Lint'in bulduğu hatalar:
  - **Oturum rolü:** kullanıcı satırı okunurken veritabanı hatası (ör. meşgul) olursa oturum **admin** sayılıyordu;
    artık okuma hatasında en düşük rol (viewer). users tablosunda olmayan bir adın oturumu yalnız `.env`
    (bootstrap) hesabınınsa admin'dir; başka ad (ör. silinen bir kullanıcının, silmeyle yarışan girişinden kalan
    oturum) admin sayılmaz, oturum silinir ve 401 döner. Parola değiştirme de okuma hatasında `.env` hesabına
    düşmez (yalnız `.env` parolasının değişip users satırının eski kalması engellendi).
  - Günlük log rotasyonu başarısız olursa kapanmış dosyaya yazılmaya çalışılıyordu; log dosyaları artık `0600`,
    log / ekran görüntüsü / `.env` dizinleri `0750`, ekran görüntüsü PNG'leri `0600` ile oluşturulur (mevcut
    dosyaların izinleri değişmez). **Yükseltme notu:** Docker'da bu dosyaların sahibi uid 10001'dir; host'ta
    `tail data/logs/…` izin hatası verir — `docker compose logs app` ya da `sudo` kullanın (README → Docker).
  - Etiketli sonuç sayacı ve analitik kelime istatistikleri sorgu hatasını yutuyordu (etiket istatistikleri artık
    sahte 0/0 yerine hata döndürür ve loglar); örümcek ilerlemesi, motor kontrolü, planlı arama geçmişi ve graf
    "genişletildi" işaretinin yazma hataları sessizce kayboluyordu (artık loglanır).
  - Arama motoru hatası `%v` ile sarıldığı için `errors.Is` sınıflandırması kırılabiliyordu (`%w`); sentinel
    karşılaştırmaları `errors.Is`'e çevrildi.
  - Bellekteki içerik tekilleştirme özeti MD5 → SHA-256, STIX paket parmak izi SHA-1 → SHA-256 (ikisi de
    saklanmaz).

### Prometheus metrikleri
- `GET /metrics` (Prometheus metin biçimi), yalnız `METRICS_ENABLED=true` iken (varsayılan kapalı → 404). Ayrıntı,
  tüm metriklerin listesi ve örnekler: `docs/METRICS.md`; Ayarlar → **Prometheus metrikleri** durumu gösterir.
- **Asla anonim değil:** mevcut kişisel API token sisteminde yeni, yalnız-kazıma **`metrics` kapsamı**
  (`Authorization: Bearer kh_…`); token'ın **sahibi admin** olmalı (yalnız admin bu kapsamı oluşturabilir; rol her
  istekte tazelenir, `REQUIRE_2FA` uygulanır). `metrics` token'ı `/api/`'de `403 token_metrics_only`, `/taxii2/`'de
  `403` alır: Prometheus sunucusundan sızsa bile bulgu/IOC okuyamaz. `read`/`write` token'ları `/metrics`'te
  reddedilir (`403 metrics_scope_required`). Ayrı bir `METRICS_TOKEN` yerine API token'ları seçildi: özetle
  saklanır, süreli olabilir, arayüzden iptal edilir, son kullanımı görünür ve sahibi pasifleşince geçersizleşir.
  Token yok/geçersiz → `401` (`metrics_token_required` / `token_*`, `WWW-Authenticate: Bearer`), yanlış kapsam ya
  da admin olmayan sahip → `403` (`access.denied` olarak, token başına dakikada bir denetlenir); oturum çerezi kabul
  edilmez. Hesabım → Yeni token'da admin'e *Metrik* seçeneği; kapsam açıklaması ve README token kuralları
  güncellendi (`read` izleyiciye eşittir, `/api/` + `/taxii2/`; `metrics` yalnız `/metrics`).
- Şema migrasyonu 6 (`api_token_metrics_scope`): `api_tokens` kapsam kısıtına `metrics` eklenir (tablo yeniden
  kurulur; satırlar, id'ler ve AUTOINCREMENT sırası korunur).
- Bağımlılık: `github.com/prometheus/client_golang` v1.25.0 (biçimin referans uygulaması: içerik müzakeresi,
  histogram anlamı, Go çalışma zamanı ve süreç toplayıcıları). Varsayılan küresel kayıt defteri yerine sunucuya
  özel kayıt defteri (Go + süreç toplayıcılarıyla).
- Metrikler (`keywordhunter_`): `build_info{version,goversion}`; `http_requests_total{method,route,status_class}` ve
  `http_request_duration_seconds{method,route}` — `route` Gin rota kalıbı, eşleşmeyen `unmatched`, yöntem sabit
  kümeye (`OTHER`) indirgenir, ara katman en dışta (Recovery'nin 500'leri de sayılır), SSE akışı histograma
  yazılmaz; `search_runs_total` / `search_duration_seconds{trigger=manual|scheduled}`;
  `new_findings_total{severity}` (her kaynaktan ilk kez kaydedilen bulgu, işlem başarılıysa);
  `scheduled_scan_runs_total{result=success|engines_failed|timeout|error}` (`engines_failed`: hiçbir motor yanıt
  vermedi — Tor kapalı, tümü hata verdi ya da etkin motor yok; arama paketi motor özetini (`SearchStats`) döndürür); `watchlist_checks_total{result=up|changed|blocked|down}`;
  `tor_circuit_renewals_total{reason=manual|all_engines_down,result}`; `retention_purged_total{kind}` (saklama
  temizliği + denetim budaması); `notification_sends_total{channel,result}`;
  `login_attempts_total{method=password|totp|recovery_code,result=success|failure|locked|challenge|error}` (kod
  denetlenmeden reddedilen ikinci adım gönderimleri de: bekleyen giriş yok → `failure`, deneme hakkı tükenmiş →
  `locked`, 2FA bu arada sıfırlanmış → `failure`).
- Veritabanından türetilen göstergeler en fazla **30 sn'de bir** tek okumayla yenilenen önbellekli toplayıcıdan:
  `findings{severity}`, `active_sessions`, `watchlist_sites` / `watchlist_sites_down`, motor başına `engine_up`,
  `engine_active`, `engine_response_seconds` (yalnız son kontrol başarılıysa; başarısız kontrolün süresi yanıt
  süresi sayılmaz), `engine_success_ratio`, `backup_enabled`, `backup_last_status`,
  `backup_last_run_timestamp_seconds`, `backup_last_success_timestamp_seconds`, `tor_reachable` (TOR_PROXY'ye
  SOCKS5 selamlaması; hedefe bağlanılmaz), `audit_chain_ok`, `audit_chain_verify_error` ve
  `audit_chain_last_verify_timestamp_seconds` (kilitsiz özet; doğrulama öncesi yanıltıcı 0 yerine seri yok;
  doğrulama HATA verirse — taranamayan satır, silinmiş zincir durumu — `audit_chain_ok` 0 olur, önceki başarılı
  değer korunmaz), önbelleğin yaşı ve yenileme hataları.
- Şema migrasyonu 5 (`metrics_indexes`): `search_results(criticality)` ve
  `watchlist_checks(watchlist_id, checked_at, id)` indeksleri — sayımlar tablo taramasız.
- Kardinalite/gizlilik: tüm etiket değerleri sabit kümelerden (bilinmeyen → `other`); etiketlerde sorgu, URL,
  kullanıcı adı, IP yok. Bilinen etiket birleşimleri sıfırla önceden oluşturulur.
- Belgeler: README (TR/EN) bölümü ve yapılandırma tablosu, `docs/METRICS.md`, Bearer `credentials_file`'lı örnek
  `docs/prometheus/prometheus.yml` (token dosyası kabuk geçmişine düşmeden `read -s` ile yazılır; Compose hedefi
  servis adı `app:8080`), örnek uyarı kuralları (açılıştan beri hiç doğrulanmamış zincir için `unless` kuralı dahil) `docs/prometheus/keywordhunter-alerts.yml` (ikisi de
  `promtool check` ile doğrulandı) ve içe aktarılabilir Grafana panosu `docs/grafana/keywordhunter.json`
  (`datasource` + `instance` değişkenleri; sorgular `promtool` ile ayrıştırıldı).
- Testler: kapalıyken 404; anonim / Basic / biçimsiz / bilinmeyen token 401, read/write token'ları (admin'inki
  dahil) 403 `metrics_scope_required` (ve denetim kaydı), admin `metrics` token'ı 200 ama `/api/` ve `/taxii2/`'de
  403, admin dışı rol `metrics` token'ı oluşturamaz, yalnız çerez 401, rolü düşürülen admin 403, 2FA politikası 403;
  şema 6 yükseltmesi satırları/id'leri/sırayı korur; doğrulama hatasında `audit_chain_ok` 0;
  exposition `expfmt` ile ayrıştırılır; tohumlanan durum (bulgu, oturum, izleme, motor, yedek, Tor, denetim zinciri)
  değerlere birebir yansır; önbellek süresi içinde tek veritabanı okuması; HTTP ara katmanı etiketleri (rota
  kalıbı, `unmatched`, `OTHER`, ham yol/sorgu sızmaz, panik 500'ü sayılır); keyfi girdilerle sınırlı seri sayısı;
  hiçbir etikette sorgu/URL/kullanıcı adı yok; planlı tarama (hiçbir motor yanıt vermediğinde `engines_failed`),
  webhook ve yeni bulgu sayaçları; uygulamadaki çağrı yerleri (elle arama, Ayarlar'dan ve tüm-motorlar-düşük
  kancasından Tor yenileme — başarı/bekleme/hata, iki yoldan izleme kontrolü, saklama temizliğinin tür başına
  sayımları, denetim budaması); metrik ara katmanının sırası `New` ile aynı `useBaseMiddleware`'den sınanır; Grafana panosundaki
  ve `METRICS.md`/uyarı kurallarındaki her metrik adının gerçekten yayımlandığı ve her metriğin belgede/panoda yer
  aldığı.

## v0.16.0 — 2026-10-17 · "Sentinel"

Ekip ve entegrasyon sürümü: denetim kaydı, kişisel API token'ları, iki adımlı
doğrulama, TAXII 2.1 / MISP entegrasyonu, tamamen sıkı CSP, çok dilli
sınıflandırıcı ve CI'da uçtan uca test paketi. Her özellik üç bağımsız
incelemeden (güvenlik, doğruluk, UX/i18n) ve tam kalite kapısından geçti.

### Uçtan uca test paketi (Playwright + axe) CI'da
- `cmd/khseed`: boş bir veritabanını deterministik (sabit çekirdek), gerçekçi demo verisiyle kurar —
  sorgu/motor/kritiklik/kategori çeşitliliğinde etiketli bulgular, IOC'ler, vaka durumu ve notlar,
  arama geçmişi, planlı taramalar, kontrol geçmişli izleme listesi, sağlıklı motor istatistikleri,
  normal yoldan (hash zinciri) yazılmış denetim olayları ve bilinen parolalı `analyst` / `viewer`
  hesapları. `-reset` ile idempotent; `-db` zorunlu ve `.env`'deki `DB_PATH` ile aynı dosyaysa reddedilir
  (uygulama veritabanı asla silinmez); `make demo` / `make seed` (demo yalnız `127.0.0.1`'e bağlanır).
- `e2e/`: Playwright 1.64 + @axe-core/playwright. Giriş (hatalı/başarılı), tüm menü sayfaları +
  Hesabım + Denetim koyu/açık/İngilizce'de sıfır konsol hatası ve sıfır CSP ihlali
  (`securitypolicyviolation`), 390 px'te yatay taşma yok, Ctrl+K paleti, kalıcı tema, viewer → markalı
  403, API token oluştur → Bearer → iptal → 401, 2FA kaydı + testte hesaplanan RFC 6238 TOTP ile giriş,
  denetim kaydında giriş olayları ve axe taraması (`serious`/`critical` = 0).
- CI: yeni `e2e` işi (tohumla → Tor'suz sunucu → `/healthz` → Chromium → rapor + sunucu logu artefakt).
- Sayfalar arası görünüm geçişi, gelen sayfa gösterilmeden önce kullanıcı girdisiyle kesildiğinde (ör. gezinmenin
  hemen ardından Ctrl+K) Chromium 156+ yakalanmamış `InvalidStateError` raporluyordu; `<head>`'deki küçük senkron
  `static/js/boot.js` yalnız bu durumu sessize alır. Playwright 1.64'e geçişte E2E ile yakalandı.
- Erişilebilirlik düzeltmeleri (axe bulguları, bastırma yok): `--text-3` her iki temada ≥4.5:1
  kontrast; açık temada `--primary`/`--ok`/`--warn` koyulaştırıldı (beyaz yazılı birincil düğme
  4.1 → 5.9:1); kapalı komut paleti erişilebilirlik ağacından çıkarıldı (`visibility`); üst bar
  düğmelerinin adı görünen metni içerir (WCAG 2.5.3); panodaki ikon-tek harita bağlantısına ad;
  kritiklik çubuğu `role=img` içinde etkileşimli öğe barındırmaz; yapılandırılmamış Tor yenileme
  düğmesi etkin kalır, nedeni `aria-describedby` ile okunur ve tıklanınca uyarı verir; ipuçlu `.kh-btn`
  baloncukları artık kırpılmaz; ayarlardaki kayan tablo ve kod bloğu klavyeyle kaydırılabilir.

### Güvenlik: CSP `style-src 'self'` (`'unsafe-inline'` kaldırıldı)
- Politika artık hiçbir yönergede `'unsafe-inline'` içermiyor: `script-src 'self'; style-src 'self'`.
  HTML'e sızan güvenilmez bir metin satır içi CSS ile veri sızdıramaz (öznitelik seçicili arka plan
  isteği) ya da sahte bir katman/oturum kutusu bindiremez.
- Kalan satır içi stiller kaldırıldı: ikon sprite'ı (`.kh-sprite`), gösterge paneli / izleme / analiz
  ilerleme çubukları (`.w-0`, `.kh-progress > [data-bar]` boş başlar), panel akışının kademeli
  girişi ve örümcek ilerleme çubuğu (`data-from` → `data-to`) artık CSSOM ile (`el.style.x = …`)
  ayarlanır. Chart.js, chartjs-plugin-datalabels ve D3 tarayıcıda doğrulandı: yalnız CSSOM
  kullanıyorlar, ihlal yok.
- `templates_csp_test.go` genişletildi: şablonlarda `<style>` / `style=` özniteliği; JS'de üretilen
  HTML'de `style="…"`, `setAttribute('style', …)`, D3 `.attr('style', …)`, `createElement('style')`
  yakalanır (dedektörün kendi doğru/yanlış pozitif tablosuyla); politikada `'unsafe-*'`, nonce/hash
  ya da `style-src-attr`/`-elem` gevşetmesi olmamalı; ve tüm menü sayfaları + `/account`, `/login`,
  `/login/2fa`, 404/403 sayfalarının sunucu çıktısı satır içi stil içermemeli.

### API hata iletileri TR/EN + kararlı hata kodları
- Her `/api` hata yanıtı (ara katmanlar dahil: oturum 401, CSRF 403, rol/kapsam 403, hız sınırı 429,
  token, 2FA, denetim, entegrasyon) artık `{"error": <ileti>, "code": <kararlı kod>}` biçimindedir;
  mevcut `success:false` / `retryAfter` alanları ve HTTP durumları korunur. İletiler
  `pkg/i18n/catalog_apierr.go`'da (`apierr.<kod>`, TR+EN birebir). İngilizce arayüzde artık Türkçe
  bildirim çıkmaz.
- Dil: `lang` çerezi → `Accept-Language` (token/TAXII istemcileri; q değerli) → `tr`. Arayüz `/api`
  çağrılarına sayfa dilini `Accept-Language` olarak ekler; `KH.api` hatası `err.code` taşır.
- **Uyumluluk:** iletiye göre eşleşen istemciler `code`'a geçmeli — örn. REQUIRE_2FA reddi artık
  sabit `"2FA required"` metni değil, `code: "2fa_required"` ile tanınır; token ile çağrılamayan uçların
  reddi (`session_required`) gibi eski karma "TR (EN)" iletiler artık İngilizce parça içermez — istemci
  `Accept-Language: en` göndermedikçe yalnız Türkçedir. Başarı iletileri (`message`)
  de istek dilindedir. Tor devre yenilemede bekleme süresi tipli hatadan (`tor.CooldownError`) okunur.
- Sayfa çekme hataları (yeniden tarama, graf derinleştirme, tekil etiketleme) sınıflandırılır: Tor kapalı
  (`tor_unreachable`), site çevrimdışı (`site_unreachable`), yanıt yok (`site_not_responding`), zaman aşımı
  (`site_timeout`) — iletinin tamamı istek dilindedir; alt katmanın Türkçe metni yanıta girmez.
  Tanınmayan hatalar uç koduyla (`analyze_failed`, `expand_failed`, `tag_fetch_failed`) ham ayrıntı taşır.
- Toplu etiketleme iş durumu (`GET /api/batch-auto-tag/:id`) kayıtlı hata iletisini istek dilinde
  `error` + kararlı `errorCode` (`job_cancelled_by_user`, `tagging_queue_full`, `job_all_failed`…) olarak döndürür.
- Testler: koddaki her kodun iki katalogda da bulunduğu (kaynak taraması; ölü anahtar da yakalanır),
  TR/EN fmt fiili eşleşmesi, elle yazılmış hata gövdesi kalmadığı ve çerez / Accept-Language ile
  İngilizce yanıt.

### Entegrasyonlar: TAXII 2.1 sunucusu + MISP gönderimi
- **TAXII 2.1 (salt okunur, `/taxii2/`):** keşif, API kökü (`/taxii2/kh/`), koleksiyon listesi/tekil,
  `objects/` (zarf; `added_after`, `limit` 100/1000, opak `next`, `match[id|type|version|spec_version]`),
  `objects/{id}/` ve `manifest/`; `application/taxii+json;version=2.1` (nesne uçlarında
  `application/stix+json;version=2.1` da), `Accept` anlaşması + `406`, `X-TAXII-Date-Added-First/Last`,
  TAXII hata nesneleri (404/429 dahil). Sabit UUID'li koleksiyonlar: `findings-all`, `findings-high`
  (SEV ≥ 4), `indicators`. Nesneler `pkg/export` STIX kurucularından; veriye bağlı zaman damgalarıyla
  istekler arası kararlı, koleksiyon içinde tekilleştirilmiş (SQL'de ilk-geçiş), `date_added` = bulgunun
  akışa giriş anı (`feed_at`: eklenince `created_at`, sonradan yeni IOC çıkarılınca ya da kritiklik
  yükselince ilerler — artımlı çeken istemciler geç IOC'leri kaçırmaz); kart ve telefon numaraları
  yayımlanmaz. İstek başına en fazla 5000 bulgu taranır (tek nesne araması dahil), eşzamanlı taramalar
  sınırlıdır (4, hesap başına 2) ve istemci giderse tarama durur. Kimlik: API token (Bearer ya da Basic parolası; kullanıcı
  adı boş ya da sahibi), `read` kapsamı yeterli, çerez yok sayılır, iptal/süresi dolmuş token ve 2FA
  politikası uygulanır; `taxii.read` token + koleksiyon başına dakikada bir denetlenir.
- **MISP gönderimi:** Ayarlar → Entegrasyonlar (admin): adres, API anahtarı (`APP_SECRET`'tan ayrı amaçla
  türetilmiş AES-256-GCM; asla geri dönmez; adres başka sunucuya taşınırken yeniden istenir), TLS
  doğrulama, dağıtım 0–3, varsayılan etiketler, özel ağ izni (SSRF koruması; düz http yalnız kurum içi IP'lere), otomatik gönderim (eşik,
  varsayılan kapalı). Bulgular → toplu işlem "MISP'e gönder": tek olay (`url`, `email-src`, `btc`,
  `xmr`, `ip-dst`, `md5`/`sha1`/`sha256`, `domain`; kart ve sırlar gönderilmez; tehdit düzeyi en yüksek
  kritiklikten). Çıkış doğrudan clearnet/intranet (Tor değil), yönlendirme yok, 15 sn, 1 MB.
  `misp.config.update`, `misp.test`, `misp.push` denetlenir (anahtarsız).
- Belge: [docs/INTEGRATIONS.md](docs/INTEGRATIONS.md) (uçlar, curl, OpenCTI/Sentinel/MISP kurulumu, güvenlik notları).

### İki adımlı doğrulama (TOTP, RFC 6238)
- **Kayıt (Hesabım → İki adımlı doğrulama):** 3 adımlı sihirbaz — sunucuda üretilen
  QR (PNG data URI, `github.com/skip2/go-qrcode`: saf Go, bağımlılıksız, ISO 18004
  maske seçimi) + 4'erli gruplanmış base32 anahtar → 6 haneli kod doğrulama → bir kez
  gösterilen 10 kurtarma kodu (kopyala / `.txt` indir / "kaydettim" onayı). Sihirbaz
  kapanınca QR, sır ve kodlar DOM'dan silinir.
- **Kripto (`pkg/totp`):** HMAC-SHA1, 6 hane, 30 sn, ±1 adım, sabit-zamanlı karşılaştırma,
  RFC 4226/6238 test vektörleri; kalıcı son adımla yeniden oynatma koruması (atomik CAS).
  Sırlar `APP_SECRET`'tan HKDF-SHA256 ile türetilen anahtarla AES-256-GCM şifreli ve
  kullanıcıya bağlı (AAD); kurtarma kodlarından yalnız kullanıcıya bağlı SHA-256 özeti.
- **Giriş:** parola doğruysa oturum açılmaz; 5 dk'lık bekleyen giriş (`/login` kapsamlı,
  HttpOnly + SameSite=Strict çerez) → `/login/2fa` (otomatik gönderim, kurtarma kodu
  modu, kalan deneme, kilit geri sayımı, kalan süre, iptal). 5 hatalı kodda bekleyen giriş
  düşer; kod hataları IP kilidine sayılır. Deneme hakkı (bekleyen giriş + IP) doğrulamadan
  önce atomik olarak ayrılır: eşzamanlı istekler sınırı aşamaz; parola değişikliği bekleyen
  girişi düşürür. Oturum, normal girişle aynı `startSession`
  yolundan açılır. DB hatasında `.env` bootstrap kimliğine düşülmez (2FA atlatılamaz).
- **Yönetim:** etkinleştirme mevcut parolayı ister; parola + kodla kapatma ve kod yenileme; admin için `/users`'ta 2FA rozeti
  (açık / eksik / kapalı) ve "2FA'yı sıfırla" (kendi hesabına uygulanamaz).
- **Zorunluluk:** `REQUIRE_2FA=none|admins|all`; kayıtsız kullanıcı Hesabım'a yönlenir,
  API `403 {"code":"2fa_required"}`, token'ları reddedilir. Politika etkinken `users` dışı
  `.env` yönetici girişi reddedilir; kalıcı `APP_SECRET` yoksa uygulama başlamaz.
- **`APP_SECRET`:** yoksa üretilip etkin `.env`'in sonuna eklenir (yorumlar korunur, değer loglanmaz); yazılamazsa
  2FA kaydı o çalıştırmada kapalıdır.
- **Denetim:** `2fa.*`, `auth.2fa.challenge|success|failure`; giriş olayında `mfa`.
- **Giriş sayfası:** sorgu dizgisindeki serbest `message` metni artık yansıtılmaz
  (yalnız bilinen ileti kodları).

### Değiştirilemez denetim kaydı (`/audit`)
- **Hash zinciri:** her olay `sha256(prev_hash || kanonik alanlar)` ile bir öncekine
  bağlanır; `audit_chain_state` çapa (budanan son satır) ve baş (son satır) tutar.
  Satır değiştirme, ortadan/baştan silme ve sondan kırpma kırılma noktasıyla
  yakalanır; başın ötesine zincir dışı eklenen satırlar ya da geri sarılmış zincir
  durumu da (`extra`) kırık sayılır. SQLite tetikleyicileri UPDATE'i her zaman,
  DELETE'i budama dışında reddeder; ekleme yalnız zincirin sonuna yapılabilir ve
  zincir durumu yalnız ileri gider. Yazılamayan olaylar `/audit`'te uyarı olarak görünür.
- **Sızıntı önleme:** URL hedeflerinde (örümcek, izleme listesi, ekran görüntüsü,
  derinleştirme) kullanıcı bilgisi ve sorgu değerleri kayda girmez; başka siteden
  tetiklenen dışa aktarma (bulgu CSV/JSON/STIX, denetim) Fetch Metadata ile reddedilir.
  JSON dışa aktarma `ts` alanını hash'in kapsadığı birebir metinle verir.
- **Kapsam:** giriş başarılı/başarısız/kilitli (kilitli denemeler IP başına dakikada
  bir), çıkış, rol/CSRF reddi (`access.denied`), kullanıcı oluşturma/güncelleme/
  silme/parola sıfırlama, öz-parola, ayarlar (yalnız değişen anahtar adları), bildirim
  ayarı/testi (webhook yalnız ana makine), bulgu silme/vaka/kritiklik/analiz,
  planlı tarama, izleme listesi, motorlar, Tor NEWNYM (otomatik dahil), site profili
  (yalnız host), örümcek, CSV/JSON/STIX/denetim dışa aktarma, arama, etiketleme,
  derinleştirme, ekran görüntüsü, zincir doğrulama ve budama.
- **Arayüz:** yalnız admin; bütünlük kartı, filtre (aktör, kategori, sonuç, tarih,
  metin), genişletilebilir ayrıntı, sayfalama, filtreye duyarlı CSV/JSON dışa
  aktarma, mobil kart düzeni, TR/EN. Kısayol: <kbd>G</kbd> <kbd>L</kbd>.
- **API:** `GET /api/audit`, `GET /api/audit/export?format=csv|json`,
  `POST /api/audit/verify` (admin).
- **Yapılandırma:** `AUDIT_RETENTION_DAYS` (varsayılan 365, `0` = süresiz).

### Çok dilli CTI sınıflandırıcı (`pkg/cti`)
- **RU / DE / ES / PT yeraltı sinyalleri:** eski "PredictCTI v2" (PR #2) sözlüğündeki
  değerli terimler (пробив, стилер, обнал, кардинг, ботнет, дедики, Datenleck,
  Geldwäsche, contraseña, vazamento de dados, lavagem de dinheiro …) mevcut
  kategori/kritiklik şemasına eşlenerek taşındı; uyuşturucu, silah, sahte belge,
  kiralık suç ve DDoS hizmeti sinyalleri `Illegal Market`, phishing/aklama
  `Finansal Dolandırıcılık`, zararlı yazılım `Exploit / Vulnerability` altında
  toplandı (TR/EN tamamlayıcı terimlerle birlikte).
- **Unicode-doğru eşleşme:** Kiril dahil küçük harf katlama, aksan silme
  (contraseña = contrasena, Geldwäsche = geldwaesche), ß → ss, ё → е, Türkçe ı/İ;
  eşleşme kelime sınırlı, kökler (`обнал*`) yalnız kelime başında — kısa kökler
  (мет, крипт, скам, брут) alakasız kelimelerde tetiklenmez. URL yolundaki
  kelimeler de ayrıştırılır.
- **Sabit etiket adları:** her sinyal tek bir etiketle kaydedilir; çekimler,
  aksanlı/aksansız ve umlaut/ae yazımları ile transliterasyonlar aynı etikete
  düşer (ботнета / ботнету → `ботнет`, probiv → `пробив`, Geldwaesche →
  `geldwäsche`), böylece Bulgular'daki `?tag=` süzgeci hepsini bulur. Sorgu
  metninden yalnız tek kelimelik sinyaller, sabit adıyla etiket olur.
- **Yanlış pozitif sertleştirmesi:** çok anlamlı kelimeler (выкуп, слив, golpe,
  filtración, heroína, hitman, ddos) yalnız destekleyici sinyaldir — tekrar
  etse ya da sorguda geçse de tek başına sınıflandırma üretmez. DDoS koruma
  sayfaları artık `Illegal Market` sayılmaz (yalnız "ddos for hire", "ддос на
  заказ" gibi ifadeler); eşik altı sonuçlarda çok dilli sinyaller zayıf tehdit
  etiketi üretmez, metin `Genel` kalır.
- Mevcut TR/EN kuralları ve skorları birebir korundu; dil başına doğru pozitif,
  yanlış pozitif koruma, aksan/büyük-küçük harf eşdeğerliği ve çift sayım
  testleri eklendi.

### Ekran görüntüsü / JS render (`pkg/capture`)
- **chromedp v0.21.0 + cdproto v0.157.10**'a geçildi; yakalama kodu yeni API'ye taşındı.
- **Yalnız http sunan .onion siteler yeniden yükleniyor:** güncel Chromium http://
  gezinmesini önce https://'e çevirdiği için bu siteler `ERR_BLOCKED_BY_CLIENT` /
  `ERR_TOO_MANY_REDIRECTS` ile düşüyordu; HTTPS yükseltmeleri (`HttpsUpgrades`,
  `HttpsFirstBalancedModeAutoEnable`) kapatıldı.
- **Ekran görüntüleri tam 1280×900:** yeni headless kipte pencere çerçevesi yüzünden
  1280×761 çıkıyordu; görünür alan sabitlendi, boyutlar PNG başlığından okunuyor.
- **Güvenlik — Tor dışına sızıntılar kapatıldı:** düşman bir .onion sayfası
  (1) tarayıcıyı `127.0.0.1` / `localhost` / `[::1]` (ve 169.254/16) adreslerine
  yönlendirip sunucudaki iç servislerin içeriğini render/ekran görüntüsü çıktısına
  sızdırabiliyordu — Chromium loopback'i proxy'yi atlayarak bağlıyordu
  (`--proxy-bypass-list=<-loopback>` ile artık Tor'a gider; ayrıca iç adreste ya da
  hata sayfasında biten sonuç atılır); (2) WebRTC/STUN ile proxy'siz UDP gönderip
  platformun gerçek çıkış IP'sini öğrenebiliyordu
  (`--webrtc-ip-handling-policy=disable_non_proxied_udp`).
- Gerçek tarayıcı testleri (sahte SOCKS5 + sahte .onion) eklendi; `CHROME_BIN`
  verilmezse atlanır (bkz. CONTRIBUTING).

## v0.15.0 — 2026-10-16 · "Nightwatch"

Arayüzün baştan yeniden tasarımı, sıkı CSP'ye geçiş ve bu süreçte bulunan
güvenlik/işlev hatalarının düzeltilmesi.

### Arayüz ve deneyim
- **Nightwatch tasarım sistemi** (`static/css/kh.css`): tek kaynaklı token'lar,
  **açık / koyu / sistem teması** (sunucu tarafında `<html data-theme>` olarak
  basılır; ilk boyamada doğru tema, yanıp sönme yok), bileşen kütüphanesi ve hareket
  katmanı (sayfalar arası görünüm geçişleri, kademeli giriş, sayaç animasyonları,
  imleç takipli ışık, iskelet yükleyiciler; `prefers-reduced-motion` tam desteklenir).
- **Uygulama kabuğu:** 1440px'te iki satıra taşan 13 öğeli üst menü yerine gruplu,
  daraltılabilir yan menü (mobilde çekmece), konum göstergeli üst bar, motor sağlığı
  çipi, tema/dil düğmeleri ve kullanıcı menüsü (parola değiştirme, kısayollar, çıkış).
- **Komut paleti** (<kbd>Ctrl</kbd>/<kbd>⌘</kbd>+<kbd>K</kbd>) ve kısayol yardımı (<kbd>?</kbd>).
- **Yeni marka işareti "Onion Reticle"**: Tor soğan katmanları + nişangah; animasyonlu
  SVG (katmanlar çizilerek belirir, radar ping, tarama yayı). Favicon, giriş ekranı,
  yan menü ve README banner'ı güncellendi.
- **Tüm sayfalar yeniden yazıldı:** giriş (canlı radar, kilit geri sayımı, Caps Lock
  uyarısı), panel, arama (canlı motor çipleri, durdurma), bulgular (12 sütunlu filtre,
  toplu işlem çubuğu, mobilde kart görünümü), harita (tema duyarlı D3, cam paneller),
  analiz (tema duyarlı Chart.js), planlı taramalar, izleme listesi, motorlar, örümcek,
  kullanıcılar, ayarlar; markalı 404/403 sayfaları.
- **Sunucu tarafı i18n:** dil, rol ve menü ilk baytta doğru gelir; çeviriler ekran
  bazlı `pkg/i18n/catalog_*.go` dosyalarına ayrıldı (TR + EN eksiksiz).
- Kullanılmayan 165 KB Tailwind derlemesi ve eski `app.css`/`graph.css`/`analytics.css`
  kaldırıldı.

### Güvenlik
- **Go 1.27.2 + `golang.org/x/net` 0.60 + `golang.org/x/crypto` 0.58:** govulncheck'in
  koddan erişilebilir bulduğu 12 zafiyet kapatıldı (html/template GO-2026-6599/6600,
  net/http, net/textproto, crypto/tls ve x/net HTTP/2). Docker imajları
  golang:1.27-alpine / alpine:3.24, GitHub Actions Node 24 sürümlerine taşındı;
  Dependabot güncellemeleri ekosistem başına gruplandı.
- **CSP sıkılaştırıldı: `script-src 'self'`** (`'unsafe-inline'` kaldırıldı). Şablonlarda
  satır içi betik / `on*=` işleyici / `javascript:` URL'si yoktur; `templates_csp_test.go`
  bunu her şablon ve JS dosyası için zorlar. Davranış `data-action` delegasyonu ile
  bağlanır, sayfa verisi çalıştırılmayan JSON bloklarıyla taşınır.
- **Enjeksiyon kalıpları kaldırıldı:** dark web ya da kullanıcı kaynaklı değerler
  (sorgu, motor/iş adı, izleme sitesi adı/URL'si, kullanıcı adı, site profili host'u,
  IOC değeri) satır içi `onclick` dizgilerine gömülüyordu; öznitelik içinde HTML kaçışı
  çözüldüğü için tırnak içeren bir değer betik bağlamından çıkabiliyordu. Hepsi
  `data-*` öznitelikleriyle değiştirildi.
- **POST /search** artık CSRF token'ı ve en az analist rolü ister (önceden ikisi de
  yoktu; izleyici rolü arama başlatabiliyordu).
- **/settings sayfası yalnız admin** (rota + handler); önceden her rol URL ile açabiliyordu.
- Menü rol bazında sunucuda süzülür (izleyici admin bağlantılarını HTML'de hiç almaz).
- Tema çerezi doğrulanır (beyaz liste); webhook testi analist için kayıtlı URL'yi
  kullanır, sır tarayıcıya taşınmaz; tohum/izleme URL'leri yalnız http(s) ise bağlantı olur.
- `/api/expand`: geçersiz onion adresi Tor'a gitmeden 400 ile reddedilir.

### Hata düzeltmeleri
- Dışa aktarma (CSV/JSON/STIX) ve sayfalama bağlantıları filtreleri düşürüyordu
  (html/template sorgu dizesindeki `&`/`=` karakterlerini kaçışlıyordu).
- Etiketler `"a,b"` biçiminde saklandığında bölünmüyordu (`tags` şablon fonksiyonu).
- Aramada SSE bağlanmadan istek başladığı için ilk motor olayları kayboluyordu.
- Giriş sayfasında yanlış "Oturum süresi doldu" bildirimi çıkıyordu.
- Başlangıç logunda dinleme adresi `http://localhost127.0.0.1:8080` olarak basılıyordu.
- Haritada lejant renkleri düğümlerle uyuşmuyordu, kök etiketi dikey yazılıyordu,
  yazı boyutu ayarı etkisizdi, bazı düğümler kontrollerin altında kalıyordu.
- Motor aç/kapat anahtarı tek beyaz nokta olarak görünüyordu.
- Çok baytlı (Türkçe) başlıklarla "izlemeye ekle" reddediliyordu.
- Planlı tarama oluşturulduktan sonra modal yeniden açılıyordu; "şimdi çalıştır"
  sonucu körlemesine yeniden yükleme yerine izlenerek güncellenir.

### Proje
- README yeniden yazıldı (TR) + `README.en.md`; tema duyarlı animasyonlu banner,
  mimari diyagramı, ekran görüntüsü galerisi.
- `docs/FRONTEND.md` (ön yüz sözleşmesi), `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`,
  issue/PR şablonları; CI'a JS sözdizimi kontrolü.
- Yeni testler: CSP regresyonu, sunucu tarafı rol/tema/dil, hata sayfaları, arama
  CSRF/rol, admin sayfa erişimi, log URL biçimi.

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
- **Sessiz güvenlik hataları görünür kılındı (`handlers_users.go`):**
  - Parola sıfırlama/değişiminde **oturum iptali** (`DELETE FROM sessions`) başarısız
    olursa eski oturumlar canlı kalıyordu ve bu yutuluyordu — artık hata loglanıyor.
  - Bootstrap admin öz-parola değişiminde `creds.Update` hatası yutuluyordu; başarısız
    olsa **boş hash `.env`'e yazılıp admin kilitlenebilirdi** — artık 500 ile durur.
  - `.env`'e kalıcı yazma başarısız olursa yeni parola yalnız bellekte kalıyor, yeniden
    başlatmada **eski parola geri geliyordu** — artık kullanıcıya açıkça bildirilir.
- `BatchRunner.Submit` artık `ctx` iptalini onurlandırıyor (iptal edilmiş istek yetim
  iş üretmez). `graph_nodes` en-iyi-çaba eklemesi bilinçli olarak belgelendi.
- **SSRF derinlemesine savunma (`capture.Render` / `capture.Capture`):** headless
  Chromium `--host-resolver-rules` ile DNS'i Tor'a zorluyordu ama düz IP hedefleri
  (`http://127.0.0.1:…`, `10.0.0.5`, `169.254.169.254`) DNS gerektirmez; onları yalnız
  Tor daemon'unun *varsayılan* iç-adres reddi durduruyordu — koruma Tor
  yapılandırmasına bağımlıydı. `Capture` hiç URL doğrulamıyordu. Her iki fonksiyon
  artık chromium kontrolünden önce kendi kodumuzda yalnız `.onion` kabul ediyor.
  Test: `TestRenderAndCapture_RejectNonOnion` (7 iç/clearnet hedef reddi + onion geçişi).
- CSP `script-src 'unsafe-inline'` ödünleşimi sabitin üstünde belgelendi (satır içi
  onclick/script blokları gerektiriyor; kaldırma yolu: harici betik + istek başına nonce).
- **Webhook sırrı sızıntısı kapatıldı.** Slack/Discord webhook adresleri yolda
  taşıyıcı-benzeri bir belirteç taşır; üç yerde tam adres açığa çıkıyordu:
  - **Log:** `scheduler` başarıda tam adresi yazıyordu; başarısızlıkta `%v err`
    `*url.Error` üzerinden yine tam adresi yazıyordu. `SendWebhook` artık `*url.Error`'ı
    açıp yalnız alttaki ağ hatasını ve gizlenmiş adresi (`shared.RedactURL`: yalnız
    şema+ana makine) taşır.
  - **API:** `GET /api/alert-config` ve `GET /api/scheduled` açık `api` grubundaydı —
    salt-okunur **viewer** bile sırrı çekebiliyordu. Admin dışı roller artık gizlenmiş
    adres görür (yapılandırıldığını anlar, sırrı göremez).
  - **HTML:** `/scheduled` sayfası genel webhook'u `value="…"` olarak gömüyordu; istemci
    gizleme kaynak görünümünü kapatmaz. Sunucu tarafında admin dışı için gizlenir.
  - Yerleşik RBAC sözleşmesi korundu (analist alert-config yazabilir). Analist gizlenmiş
    maskeyi geri gönderip yalnız eşik/etkin değiştirirse gerçek sır **maskeyle ezilmez**
    (`handleAlertConfigSave` kayıtlı adresi korur); yeni adres normal kaydedilir.
  - Testler: `TestRedactURL`, `TestWebhookSecret_RedactedForNonAdmin`.
- Doğrulandı (düzeltme gerekmedi): giriş her seferinde yeni oturum kimliği üretir
  (`crypto/rand`, fixation kapalı); CSRF `subtle.ConstantTimeCompare`; site profili
  logu çerez *değerini* değil yalnız varlığını yazar.
- **Bağımlılık/hijyen:** `go.mod` düzenli değildi (`chromedp/cdproto` doğrudan
  içe aktarılıyor ama dolaylı listeleniyordu) — `go mod tidy` uygulandı ve CI'ye
  **`go mod tidy -diff` kapısı** eklendi (sapma yeniden oluşamaz). `go mod verify`
  temiz. `govulncheck`: kodun eriştiği **0** açık; tek modül-düzeyi bulgu
  GO-2026-5932 (`golang.org/x/crypto@v0.57.0`, yalnız bcrypt kullanılıyor, etkilenen
  simgeler çağrılmıyor, **henüz düzeltilmiş sürüm yok** — izlenecek).
- Bağlam değeri üzerindeki iki çıplak `sid.(string)` iddiası comma-ok'a çevrildi
  (gizli 500-panik). Öz-parola yolunda boş kimlikle devam etmek `id <> ""` üzerinden
  çağıranın **kendi** oturumunu da düşüreceğinden ek boş-olmayan koruması eklendi.
- Doğrulandı (düzeltme gerekmedi): dışa aktarımlar sınırlı (bulgular ≤50k, STIX ≤20k,
  500'lük sayfalarla); hız sınırlayıcı `ClientIP()` ile anahtarlanır ve
  `SetTrustedProxies(nil)` XFF sahteciliğini kapatır; kod yolunda `panic(` yok.
- **Test kapsamı:** testsiz iki pakete birim testleri eklendi (Tor gerektirmeden).
  `crawler`: `normalizeURL` (fragment/sondaki `/`/küçük harf, ziyaret-tekilleştirme),
  `titleFromURL`, `Submit` reddi (.onion olmayan tohum → iş satırı yok) ve **sınır
  kırpma** (depth 99→3, pages 99999→300, alt sınır 0→1/50, boş sorgu → `crawl:<host>`;
  durdurulmuş runner üzerinden gerçek kırpma kodu sınanır). `scraper`: `LooksJSOnly`
  dört kuralı (zengin metin, script yok, SPA/noscript izi, kısa metin eşiği).
  Test edilen paket 13 → 15.
- **Motor izleyicide gizli DNS sızıntısı kapatıldı:** `buildTorTransport("")`
  `http.DefaultTransport`'a (doğrudan clearnet) düşüyordu — boş `TOR_PROXY` ile
  `.onion` motor adresleri yerel DNS'e sızar ve `HTTP_PROXY` ortam değişkenleri
  onurlandırılırdı; kod tabanının "yalnız Tor, geri düşüş yok" değişmezini bozuyordu.
  Artık hata döner; başlangıç bunu `Warn` ile bildirip izleyiciyi devre dışı bırakır
  (yapılandırma varsayılanı `127.0.0.1:9150` olduğundan yalnız açık yanlış
  yapılandırmada tetiklenir). Test: `TestBuildTorTransport_NoDirectFallback`.
- **Kalan testsiz paketler de kapatıldı — kod içeren her paket artık test edilir:**
  `scheduler`: `DispatchGlobalAlert` davranışsal test (httptest alıcı; devre dışı → çağrı
  yok, yalnız eşik altı → çağrı yok, karışık → tam 1 çağrı ve gövde eşik üstü başlıkları
  içerir/eşik altını içermez, boş webhook → çağrı yok). `logger`: seviye filtresi + günlük
  tarih adlı dosya + dosyaya ANSI sızmaz. Test edilen paket 15 → 18.
- Güvenlik taramaları (düzeltme gerekmedi, doğrulandı): webhook istemcisi yönlendirme
  takip etmiyor (`ErrUseLastResponse`) ve **bağlantı anında** çözümlenmiş IP'yi
  `net.Dialer.Control` ile reddediyor (DNS rebinding/TOCTOU kapalı), ortam proxy'leri
  yok sayılıyor; Tor istemcisi yalnız SOCKS5 (hedef için doğrudan geri düşüş yok);
  izleme listesi + örümcek tohumu `.onion` zorunlu; tek `template.HTML` sitesi kaçışlı;
  tek dosya servisi (`/screenshots/:id`) yalnız ID→DB kaydı + `filepath.Base`.
- Tarama sonuçları (düzeltme gerekmedi): `context.With*` üç adayı yanlış pozitif
  (cancel farklı adla çağrılıyor); gerçek DB sorgularının tümü `rows.Close` ediyor;
  15 goroutine lansmanı sınırlı/iptal edilebilir; global 1 MB gövde sınırı
  22 `ShouldBindJSON` ucunu kapsıyor; 3 isteğe bağlı gövdeli uç güvenli varsayılana düşüyor.

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
