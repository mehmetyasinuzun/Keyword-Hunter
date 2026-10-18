# Prometheus metrikleri (`GET /metrics`)

KeywordHunter, Prometheus metin biçiminde (`text/plain; version=0.0.4`; istenirse protobuf/OpenMetrics
müzakeresi `promhttp`'ye bırakılır) operasyonel metrikler yayımlar: HTTP trafiği, aramalar, bulgular, motor
sağlığı, izleme listesi, Tor, denetim zinciri, yedekler, saklama temizliği, bildirimler ve girişler.

> English summary: see [the end of this document](#english-summary).

- [Açma](#açma)
- [Kimlik doğrulama](#kimlik-doğrulama)
- [Metrik listesi](#metrik-listesi)
- [Kardinalite ve gizlilik](#kardinalite-ve-gizlilik)
- [Prometheus yapılandırması](#prometheus-yapılandırması)
- [Uyarı kuralları](#uyarı-kuralları)
- [Grafana panosu](#grafana-panosu)

## Açma

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `METRICS_ENABLED` | `false` | `true` (ya da `1`/`yes`/`on`) ise `GET /metrics` kaydedilir. Kapalıyken uç yoktur: **404** |

Değişiklik yeniden başlatmada etkinleşir. Ayarlar → **Prometheus metrikleri** (admin) etkin durumu gösterir.

## Kimlik doğrulama

Uç **asla anonim değildir**. Mevcut kişisel API token sistemi kullanılır, ama yalnız bu iş için ayrılmış
**`metrics` kapsamlı** token kabul edilir:

```
Authorization: Bearer kh_…
```

- **Kapsam `metrics`** (Hesabım → API token'ları → Yeni token → *Metrik*; yalnız admin rolü oluşturabilir). Bu
  token **yalnız `/metrics`'i** kazır: `/api/` altındaki her uçta `403 token_metrics_only`, `/taxii2/`'de `403`
  alır. Böylece Prometheus sunucusundan, yapılandırma yedeğinden ya da token dosyasından sızan kimlik bulgu,
  IOC, dışa aktarma ya da TAXII verisi **okuyamaz**; yalnız buradaki toplu sayaçları görür.
- `read` ve `write` token'ları (sahibi admin olsa bile) bu uçta **reddedilir** (`403 metrics_scope_required`):
  tüm bulgulara erişen genel amaçlı bir API kimliği kazıma yapılandırmasına konmasın.
- Token'ın **sahibi admin rolünde** olmalıdır. Rol her istekte `users` tablosundan okunur: admin'likten
  düşürülen, pasifleştirilen ya da silinen hesabın token'ı anında çalışmaz (`403 metrics_admin_only` / `401`).
  Ayrı bir kazıma hesabı gerekmez; token'ı mevcut bir admin hesabıyla oluşturup süreli verin ve Prometheus
  dışında kullanmayın. `REQUIRE_2FA` sahibi kapsıyorsa hesapta 2FA etkin olmalıdır (aksi halde
  `403 2fa_required`; `/api/` ve `/taxii2/` ile aynı kural).
- Oturum çerezi bu uçta **okunmaz** (tarayıcıdan açık bir oturumla erişilemez); token sorgu dizgisinden (`?token=`)
  okunmaz.

| Durum | Yanıt |
|---|---|
| `METRICS_ENABLED` kapalı | `404` |
| Token yok / Bearer dışı şema | `401` `metrics_token_required` + `WWW-Authenticate: Bearer realm="KeywordHunter metrics"` |
| Biçimsiz, bilinmeyen, iptal edilmiş ya da süresi dolmuş token | `401` `token_invalid` / `token_revoked` / `token_expired` (denetlenir: `auth.token.failure`, IP + neden başına dakikada bir) |
| `read` / `write` kapsamlı token | `403` `metrics_scope_required` (denetlenir: `access.denied`, `reason=token_scope`, token başına dakikada bir) |
| Sahibi (artık) admin değil | `403` `metrics_admin_only` (denetlenir: `access.denied`, `reason=metrics_role`, token başına dakikada bir) |
| `REQUIRE_2FA` kapsamında, 2FA'sız hesap | `403` `2fa_required` |
| Başarılı | `200`, metin biçimi; token'ın son kullanımı (dakikada bir) güncellenir |

**Neden ayrı bir `METRICS_TOKEN` değil?** API token'ları veritabanında yalnız SHA-256 özetiyle saklanır, süreli
olabilir, Hesabım/kullanıcılar ekranından tek tıkla iptal edilir, son kullanım zamanı/IP'si görünür, geçersiz
denemeleri denetlenir ve sahibi pasifleşince kendiliğinden geçersizleşir. `.env`'deki statik bir sır bunların
hiçbirini sağlamaz (düz metin, dönüşümsüz, kimin kullandığı belirsiz). **Neden ayrı bir kapsam?** `read`
kapsamı bu sistemde izleyici yetkisine eşittir: tüm bulguları `/api/export` ve `/taxii2/`
üzerinden okuyabilir. Kazıma kimliği genellikle izleme sunucusunda, daha geniş erişimli bir ortamda durur;
`metrics` kapsamı onu tek uca hapseder (şema 6 bu değeri `api_tokens` kısıtına ekler). **Neden admin?** Metrikler
arayüzde yalnız admin'e açık bilgiler içerir (denetim zinciri durumu, yedek durumu, başarısız giriş sayıları,
aktif oturumlar).

Başarılı kazımalar denetim kaydına **yazılmaz** (15–30 sn'de bir kazıma kaydı boğardı); token'ın son kullanım
zamanı Hesabım/kullanıcılar ekranında görünür.

## Metrik listesi

Önek: `keywordhunter_`. Sayaçlar (`_total`) süreç başladığında sıfırdan başlar; bilinen etiket birleşimleri
sıfırla önceden oluşturulur (ilk olaydan önce de seri vardır, `increase()` doğru çalışır).

### Olay metrikleri (anlık güncellenir)

| Metrik | Tür | Etiketler | Açıklama |
|---|---|---|---|
| `keywordhunter_build_info` | gauge | `version`, `goversion` | Her zaman 1; sürüm ve derleyen Go sürümü |
| `keywordhunter_http_requests_total` | counter | `method`, `route`, `status_class` | HTTP istekleri. `route` Gin rota **kalıbıdır** (`/api/results/:id/artifacts`), eşleşmeyen yol `unmatched`; `method` GET/HEAD/POST/PUT/PATCH/DELETE/OPTIONS ya da `OTHER`; `status_class` `1xx`…`5xx` |
| `keywordhunter_http_request_duration_seconds` | histogram | `method`, `route` | İstek süresi (kovalar 5 ms … 300 s). Uzun ömürlü SSE akışı `/events` histograma yazılmaz (yalnız sayılır) |
| `keywordhunter_search_runs_total` | counter | `trigger` | Tamamlanan anahtar kelime aramaları: `manual` (arayüz `POST /search`) · `scheduled` (planlı tarama, "şimdi çalıştır" dahil) |
| `keywordhunter_search_duration_seconds` | histogram | `trigger` | Arama süresi (tüm motorlar, Tor üzerinden; kovalar 1 s … 300 s) |
| `keywordhunter_new_findings_total` | counter | `severity` | İlk kez kaydedilen bulgular (arama, planlı tarama, örümcek); `severity` = kritiklik `1`…`5` |
| `keywordhunter_scheduled_scan_runs_total` | counter | `result` | Planlı tarama çalışmaları: `success` (en az bir motor yanıt verdi ve sonuçlar kaydedildi; 0 sonuç da başarıdır) · `engines_failed` (hiçbir motor yanıt vermedi: Tor kapalı, tüm motorlar hata verdi ya da etkin motor yok) · `timeout` (4 dk bütçe doldu; o ana dek gelenler kaydedildi) · `error` (sonuçlar kaydedilemedi) |
| `keywordhunter_watchlist_checks_total` | counter | `result` | İzleme listesi kontrolleri: `up` · `changed` (erişilebilir ve içerik değişti) · `blocked` (captcha/engel sayfası) · `down` |
| `keywordhunter_tor_circuit_renewals_total` | counter | `reason`, `result` | Tor devre yenileme (NEWNYM): `reason` = `manual` (Ayarlar) · `all_engines_down` (motor izleyici); `result` = `success` · `cooldown` (bekleme süresi) · `failure` |
| `keywordhunter_retention_purged_total` | counter | `kind` | Saklama temizliği ve denetim budamasıyla silinenler: `results` · `tags` · `artifacts` · `misp_pushed` · `graph_nodes` · `screenshots` · `screenshot_files` · `audit_events` |
| `keywordhunter_notification_sends_total` | counter | `channel`, `result` | Giden bildirimler (`channel`: şimdilik `webhook` — Slack/Discord/genel; `result`: `success` · `failure`). Ayarlardaki "test" gönderimi de sayılır |
| `keywordhunter_login_attempts_total` | counter | `method`, `result` | Etkileşimli girişler. `method`: `password` · `totp` · `recovery_code`; `result`: `success` (oturum açıldı) · `failure` (kod denetlenmeden reddedilen ikinci adım gönderimleri dahil: bekleyen giriş yok/süresi dolmuş, 2FA bu arada sıfırlanmış) · `locked` (IP/global kilit ya da 2FA deneme hakkı tükenmiş) · `challenge` (parola doğru, 2FA adımı bekleniyor) · `error` (iç hata) |

### Veritabanından türetilen göstergeler (önbellekli, ≤ 30 sn'de bir)

Kazıma başına SQLite'a gidilmez: bu göstergeler en fazla **30 saniyede bir** tek seferde okunur (eşzamanlı
kazımalar aynı okumayı paylaşır), sayımlar indekslidir (şema 5: `idx_search_results_criticality`,
`idx_watchlist_checks_latest`). Okuma başarısız olursa önceki değerler korunur ve hata sayılır.

| Metrik | Tür | Etiketler | Açıklama |
|---|---|---|---|
| `keywordhunter_findings` | gauge | `severity` | Kayıtlı bulgu sayısı, kritiklik `1`…`5` (aralık dışı eski değerler uç kovaya) |
| `keywordhunter_active_sessions` | gauge | — | Süresi dolmamış etkileşimli oturumlar |
| `keywordhunter_watchlist_sites` | gauge | — | Etkin izleme listesi siteleri |
| `keywordhunter_watchlist_sites_down` | gauge | — | Etkin sitelerden son kontrolü `down` olanlar (captcha/engel sayılmaz) |
| `keywordhunter_engine_up` | gauge | `engine` | Motorun son sağlık kontrolü başarılıysa 1, başarısızsa 0 (hiç kontrol edilmediyse seri yok) |
| `keywordhunter_engine_active` | gauge | `engine` | Motor /monitor'da aramalar için etkinse 1 |
| `keywordhunter_engine_response_seconds` | gauge | `engine` | Son sağlık kontrolünün yanıt süresi, **yalnız kontrol başarılıysa** (düşük motorda seri yok: başarısız kontrolün süresi yanıt süresi değildir, reddedilen bağlantı ≈ 0 sn motoru "en hızlı" gösterirdi) |
| `keywordhunter_engine_success_ratio` | gauge | `engine` | Motor eklendiğinden beri başarılı kontrollerin oranı (0–1; kontrol yoksa seri yok) |
| `keywordhunter_backup_enabled` | gauge | — | `BACKUP_DIR` tanımlıysa 1 |
| `keywordhunter_backup_last_status` | gauge | — | Son zamanlanmış yedek başarılıysa 1, başarısızsa 0 (hiç çalışmadıysa seri yok) |
| `keywordhunter_backup_last_run_timestamp_seconds` | gauge | — | Son zamanlanmış yedeğin bitiş zamanı (Unix) |
| `keywordhunter_backup_last_success_timestamp_seconds` | gauge | — | Son **başarılı** zamanlanmış yedeğin zamanı (Unix) |
| `keywordhunter_tor_reachable` | gauge | — | `TOR_PROXY`'deki SOCKS5 vekili kimliksiz SOCKS5 selamlamasına (`05 01 00` → `05 00`) yanıt verdiyse 1. Hiçbir hedefe bağlantı istenmez; deneme ≤ 2 sn, önbellekle birlikte yenilenir |
| `keywordhunter_audit_chain_ok` | gauge | — | Son doğrulama zinciri sağlam bulduysa 1; kırıksa **ya da doğrulama hata verdiyse** (taranamayan/türü bozulmuş satır, silinmiş zincir durumu) 0. Önceki başarılı değer korunmaz: kurcalama, satırı okunamaz kılan bir düzenlemeyle uyarıdan gizlenemez. Açılıştan sonraki ilk doğrulamaya dek seri yok; doğrulama açılışta ve 6 saatte bir, ayrıca /audit görüntülemelerinde artımlı |
| `keywordhunter_audit_chain_verify_error` | gauge | — | Son doğrulama hata verdiyse 1 (o durumda `audit_chain_ok` da 0), aksi halde 0 |
| `keywordhunter_audit_chain_last_verify_timestamp_seconds` | gauge | — | Son **başarılı tam** zincir doğrulamasının zamanı (Unix; hata veren doğrulama değiştirmez, hiç başarılı doğrulama yoksa seri yok) |
| `keywordhunter_metrics_snapshot_timestamp_seconds` | gauge | — | Veritabanı göstergelerinin son başarılı yenilenme zamanı (Unix) |
| `keywordhunter_metrics_snapshot_errors_total` | counter | — | Başarısız yenilemeler (önceki değerler korunur) |

### Çalışma zamanı

Ayrılmış kayıt defterinde (varsayılan küresel defter kullanılmaz) Go çalışma zamanı (`go_*`: goroutine, GC,
bellek) ve süreç (`process_*`: CPU, RSS, açık dosya, başlangıç zamanı; Linux) toplayıcıları da vardır.

## Kardinalite ve gizlilik

- Etiket değerleri yalnız sabit kümelerden gelir; bilinmeyen değer `other`/`OTHER` olur. Saldırganın seçtiği
  bir HTTP yöntemi ya da yol yeni seri **üretemez** (yöntem `OTHER`, eşleşmeyen yol `unmatched`).
- `route` her zaman rota kalıbıdır; ham yol, kimlik numarası, sorgu dizgisi asla etiket olmaz.
- **Hiçbir etikette** arama sorgusu, URL/onion adresi, kullanıcı adı, IP, token öneki ya da e-posta yoktur.
  `engine` yalnız koddaki sabit motor listesinden gelir (en çok 64 seri).
- Testler bunları zorlar: `pkg/metrics` (keyfi girdilerle seri sayısı sınırlı), `pkg/web/metrics_test.go`
  (tohumlanan sorgu/URL/kullanıcı adı hiçbir etikette görünmez).

## Prometheus yapılandırması

Örnek dosyalar: [`docs/prometheus/prometheus.yml`](prometheus/prometheus.yml) ve
[`docs/prometheus/keywordhunter-alerts.yml`](prometheus/keywordhunter-alerts.yml)
(`promtool check config` / `promtool check rules` ile doğrulandı).

```yaml
scrape_configs:
  - job_name: keywordhunter
    metrics_path: /metrics
    scheme: https            # TLS sonlandıran ters vekil arkasında; doğrudan :8080 için http
    authorization:
      type: Bearer
      credentials_file: /etc/prometheus/keywordhunter.token   # içinde yalnız kh_… token
    scrape_timeout: 15s
    static_configs:
      - targets: ['keywordhunter.example.internal']
```

- Token'ı YAML'a (`credentials:`) değil, yalnız Prometheus kullanıcısının okuyabildiği bir dosyaya yazın.
- `scrape_interval` için 30 sn yeterlidir (veritabanı göstergeleri zaten ≤ 30 sn'de bir yenilenir; daha sık
  kazıma yalnız olay sayaçlarını daha sık okur).
- Kazıma IP başına hız sınırına (`RATE_LIMIT_RPS`) tabidir; eşzamanlı en çok 4 kazıma işlenir, fazlası `503`.
- Docker Compose'da Prometheus'u aynı Compose ağına alıp hedefi **servis adıyla** `app:8080` (ya da konteyner
  adıyla `keywordhunter-app:8080`), `scheme: http` yapabilirsiniz (`docker-compose.yml`'deki servis `app`'tir).
  `/metrics`'i internete açmayın (ters vekilde yalnız izleme ağına izin verin).
- Token dosyasını kabuk geçmişine düşürmeden yazın (`read -s` komut satırına koymaz):

  ```sh
  read -rs T && printf '%s' "$T" | sudo install -m 0400 -o prometheus /dev/stdin /etc/prometheus/keywordhunter.token; unset T
  ```

## Uyarı kuralları

[`docs/prometheus/keywordhunter-alerts.yml`](prometheus/keywordhunter-alerts.yml) şunları içerir: kazıma
başarısız (`up == 0`), denetim zinciri kırık ya da doğrulanamıyor / 8 saattir doğrulanmadı / açılıştan beri hiç
başarıyla doğrulanmadı (`keywordhunter_build_info unless keywordhunter_audit_chain_last_verify_timestamp_seconds`,
1 saat), son yedek başarısız / 48 saattir başarılı yedek yok, Tor SOCKS erişilemez, tüm etkin motorlar düşük,
başarısız giriş artışı, planlı taramalar hep başarısız (`engines_failed` dahil), bildirimler gönderilemiyor, sürekli
5xx, yeni kritik bulgu.

## Grafana panosu

[`docs/grafana/keywordhunter.json`](grafana/keywordhunter.json) — Grafana → Dashboards → **New → Import** → dosyayı
yükleyin, Prometheus veri kaynağını seçin. `datasource` ve `instance` değişkenleri vardır. Bölümler: genel durum
(sürüm, Tor, denetim zinciri, yedek yaşı, oturumlar, etkin motorlar, düşük siteler, yüksek kritiklikli bulgular,
başarısız girişler), HTTP (durum sınıfı, rota başına p95, 5xx), aramalar ve bulgular, motorlar/izleme/Tor,
güvenlik ve bakım, süreç. Panodaki her sorgu yalnız bu belgedeki metrik adlarını kullanır
(`TestMetrics_GrafanaDashboardUsesRealMetrics` zorlar) ve `promtool` ile ayrıştırılarak doğrulanmıştır.

---

## English summary

- **Enable:** `METRICS_ENABLED=true` (default `false` → `GET /metrics` is not registered and returns **404**).
- **Auth (never anonymous):** `Authorization: Bearer kh_…` with a personal API token of the dedicated **`metrics`
  scope** whose **owner has the admin role** (only admins can create one). A `metrics` token can scrape `/metrics`
  only: it is rejected on every `/api/` endpoint (`403 token_metrics_only`) and on `/taxii2/`, so a credential
  leaked from the Prometheus host cannot read findings, IOCs, exports or TAXII. `read`/`write` tokens are rejected
  on `/metrics` (`403 metrics_scope_required`) because a `read` token is a viewer credential for all finding data.
  The owner's role is re-read on every request; disabled/deleted or demoted owners stop working immediately
  (`403 metrics_admin_only`); `REQUIRE_2FA` applies like on `/api/` and `/taxii2/`. Session cookies and `?token=`
  are not accepted. Missing token → `401 metrics_token_required` (+ `WWW-Authenticate: Bearer`), invalid →
  `401 token_invalid|token_revoked|token_expired`; denials are audited as `access.denied` (throttled). We reuse API
  tokens instead of a static `METRICS_TOKEN` because they are stored hashed, can expire, are revocable from the UI,
  show last use and are audited; admin-only because the metrics expose admin-level operational state (audit chain,
  backups, login failures, sessions). Schema migration 6 adds `metrics` to the `api_tokens` scope constraint.
- **Metrics:** see the tables above (names, types and labels are identical; descriptions are in Turkish). Event
  counters are process-wide and pre-initialised with their known label sets; database-derived gauges (findings by
  severity, active sessions, watchlist sites/down, per-engine up/active/response/success ratio, backup
  enabled/status/timestamps, Tor SOCKS reachability, audit chain status/verify error/last verification) are refreshed **at most
  every 30 s** by a cached collector backed by indexed queries (schema migration 5), so frequent scrapes do not
  hammer SQLite. A dedicated registry also carries the Go runtime and process collectors.
- **Cardinality/privacy:** label values come from fixed sets only (`other`/`OTHER` for anything else); `route` is
  the Gin route template (`unmatched` when nothing matched), never the raw path; no query, URL, username, IP or token
  data in any label.
- **Semantics worth knowing:** `audit_chain_ok` drops to 0 when the verification itself fails with an error
  (`audit_chain_verify_error` = 1), not only when the chain is broken; `scheduled_scan_runs_total{result="engines_failed"}`
  counts runs where no engine answered (Tor down, every engine failed, or none enabled) instead of calling them
  successes; `engine_response_seconds` is only exported while the latest check succeeded.
- **Files:** sample scrape config [`docs/prometheus/prometheus.yml`](prometheus/prometheus.yml) (bearer
  `credentials_file`; with Docker Compose the target is the service name `app:8080`), alert rules [`docs/prometheus/keywordhunter-alerts.yml`](prometheus/keywordhunter-alerts.yml)
  (both validated with `promtool`), importable Grafana dashboard
  [`docs/grafana/keywordhunter.json`](grafana/keywordhunter.json) (`datasource` + `instance` variables).
- **Dependency:** `github.com/prometheus/client_golang` — the reference implementation of the exposition format
  (text/protobuf/OpenMetrics content negotiation, histogram semantics, Go runtime and process collectors). Writing
  this by hand would duplicate a well-reviewed, widely deployed library and risk subtle format bugs.
