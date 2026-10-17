# Entegrasyonlar: TAXII 2.1 sunucusu ve MISP gönderimi

KeywordHunter bulgularını CTI platformlarına iki yoldan aktarır:

| Yol | Yön | Kimlik | Kim kullanır |
|---|---|---|---|
| **TAXII 2.1 sunucusu** (`/taxii2/`) | Platform **çeker** (pull), salt okunur | Kişisel API token'ı (Bearer ya da HTTP Basic) | OpenCTI, Microsoft Sentinel, MISP (taxii feed), her TAXII 2.1 istemcisi |
| **MISP gönderimi** | KeywordHunter **iter** (push), bulgu başına değil seçim başına tek olay | MISP API anahtarı (yalnız admin girer, şifreli saklanır) | MISP |

> English quick reference: see [the end of this document](#english-quick-reference).

---

## 1. TAXII 2.1 sunucusu

OASIS TAXII 2.1 uyumlu, **salt okunur** bir sunucudur. Koleksiyon nesneleri, dosya dışa aktarımındaki
(`GET /api/export/stix`) STIX 2.1 kurucularının aynısından üretilir: aynı deterministik kimlikler, aynı
desenler. Fark, zaman damgalarının istek anına değil veriye bağlı olmasıdır — aynı veri her istekte
birebir aynı nesneyi (aynı `id` + aynı `modified` = aynı sürüm) üretir, böylece tüketici her çekişte
"yeni sürüm" görmez.

### Uç noktalar

| Yöntem | Yol | Açıklama |
|---|---|---|
| `GET` | `/taxii2/` | Keşif (discovery): `default` ve `api_roots` |
| `GET` | `/taxii2/kh/` | API kökü bilgisi (`versions`, `max_content_length`) |
| `GET` | `/taxii2/kh/collections/` | Koleksiyon listesi |
| `GET` | `/taxii2/kh/collections/{id}/` | Tek koleksiyon |
| `GET` | `/taxii2/kh/collections/{id}/objects/` | Nesneler (zarf): `added_after`, `limit`, `next`, `match[id]`, `match[type]`, `match[version]`, `match[spec_version]` |
| `GET` | `/taxii2/kh/collections/{id}/objects/{object-id}/` | Tek nesne (zarf) |
| `GET` | `/taxii2/kh/collections/{id}/manifest/` | Nesne özetleri (`id`, `date_added`, `version`, `media_type`) |
| `POST` / `DELETE` | `…/objects/…` | `403` — koleksiyonlar salt okunurdur (`can_write: false`) |

`{id}` koleksiyon kimliği (UUID) ya da takma adıdır (`alias`). Yollar `/` ile biter; eğik çizgisiz istek
kalıcı olarak yönlendirilir.

### Koleksiyonlar

Kimlikler sabittir (UUIDv5); sürüm yükseltmesinde ya da yeniden kurulumda değişmez, istemci
yapılandırmaları kırılmaz. Ayarlar → Entegrasyonlar ekranı tam adresleri kopyalama düğmeleriyle gösterir.

| Takma ad | Kimlik | İçerik |
|---|---|---|
| `findings-all` | `4e8e1b28-53c1-5eee-a771-b92fbf71fb00` | Tüm bulgular: URL göstergesi + IOC göstergeleri + IOC → bulgu `related-to` ilişkileri |
| `findings-high` | `a967eef9-6fab-5e3f-a4b3-3e235c1dc779` | Yalnız kritikliği **4–5** olan bulgular ve onların IOC/ilişkileri |
| `indicators` | `8d76704e-d6e7-5396-bf6a-07de1854d968` | Yalnız IOC göstergeleri (e-posta, IP, onion, hash, BTC/XMR); URL göstergesi ve ilişki yok |

Her koleksiyonun ilk nesnesi, göstergelerin `created_by_ref` ile işaret ettiği sabit **KeywordHunter
`identity`** nesnesidir.

### Nesne modeli

- **`date_added` = bulgunun akışa (yeniden) giriş anı.** Bulgu eklenince `created_at`'idir; bulguya
  **sonradan yeni bir IOC çıkarılınca** (otomatik etiketleme eklemeden sonra çalışır) ya da **kritiklik
  yükselince** (ör. `findings-high` eşiğini geçince) o ana ilerler ve bulgunun nesneleri yeniden sunulur.
  Böylece `added_after` ile artımlı çeken istemciler sonradan çıkarılan IOC'leri ve eşiği sonradan geçen
  bulguları kaçırmaz. Bir bulgunun bütün nesneleri aynı `date_added`'ı taşır; sıralama `date_added`
  artandır.
- **Tekilleştirme:** aynı STIX nesnesi (aynı URL'nin göstergesi, aynı IOC, aynı IOC → URL ilişkisi)
  birden fazla bulguda geçebilir; koleksiyonda yalnız **ilk** geçtiği bulguyla bir kez yayımlanır. Karar
  koleksiyonun kendi süzgeci içinde verilir (ör. `findings-high`'da yalnız SEV 4–5 bulgular sayılır).
- **`modified`:** URL göstergesinde son analiz anı (IOC'lerin yazıldığı ya da kritikliğin yükseldiği an),
  IOC göstergesinde IOC'nin çıkarıldığı an. `created` değişmez. Yalnız etiket/kategori değişen ya da
  kritikliği düşen bir yeniden etiketleme `date_added`'ı ilerletmez; artımlı çeken istemci bu tür
  güncellemeleri görmez — tam eşitleme için ara sıra `added_after` olmadan çekin.
- **Paylaşılmayanlar:** kart numaraları (`credit_card`, PCI kapsamında kişisel veri) ve telefon
  numaraları (`phone`, gerçek kişilere ait olabilir; MISP gönderimi de atlar) TAXII ile **asla**
  yayımlanmaz. (Analistin bilinçli eylemi olan STIX dosya dışa aktarımı içermeye devam eder.)
- **Tek sürüm:** her nesnenin yalnız güncel sürümü sunulur; `match[version]=first|last|all` aynı sonucu
  verir, zaman damgası verilirse yalnız `modified` değeri ona eşit nesneler döner.

### Sayfalama ve süzgeçler

| Parametre | Kural |
|---|---|
| `limit` | Varsayılan **100**, üst sınır **1000** (daha büyüğü 1000'e indirilir); `< 1` ya da sayı değilse `400` |
| `added_after` | RFC 3339 zaman damgası; yalnız bu andan **sonra** eklenenler (kesin büyük) |
| `next` | Bir önceki yanıttaki opak imleç; aynı süzgeçlerle birlikte gönderin |
| `match[type]` | Virgüllü ya da tekrarlı: `indicator`, `relationship`, `identity` |
| `match[id]` | Virgüllü ya da tekrarlı STIX kimlikleri (en fazla 100) |
| `match[version]` | `first` · `last` · `all` · RFC 3339 zaman damgası |
| `match[spec_version]` | `2.1` (başka değer → boş sonuç) |

Zarf `{"more": bool, "next": "…", "objects": [...]}` biçimindedir (`next` yalnız `more: true` iken).
Bir istek (liste, manifest ya da tek nesne) en fazla 5000 bulgu tarar; seyrek eşleşen süzgeçlerde
sayfa limitten az (hatta sıfır) nesneyle `more: true` dönebilir — `next` ile devam edin. Bu,
`objects/{id}/` için de geçerlidir: büyük bir koleksiyonda nesne ilk 5000 bulguda yoksa yanıt `404`
değil boş bir zarf + `next` olur. Aynı anda en fazla 4 tarama (hesap başına 2) yürür; fazlası
`429` + `Retry-After` alır. İstemci bağlantıyı keserse tarama durur. Nesne ve manifest yanıtları
`X-TAXII-Date-Added-First` / `X-TAXII-Date-Added-Last` başlıklarını taşır (sayfadaki ilk/son
`date_added`); artımlı çekişte bir sonraki `added_after` olarak sonuncuyu kullanabilirsiniz.

### Medya türleri

- İstek: `Accept: application/taxii+json;version=2.1` (sürümsüz `application/taxii+json`, `*/*` ve boş
  `Accept` da kabul edilir). Nesne uçları (`objects/`, `objects/{id}/`) ayrıca
  `application/stix+json;version=2.1` kabul eder ve yanıtı bu türle döndürür.
- Desteklenmeyen tür (ör. `text/html`, `application/json`, `version=2.0`) → `406`.
- Yanıt: `Content-Type: application/taxii+json;version=2.1` (ya da istenen STIX türü).
- Hatalar TAXII hata nesnesidir: `{"title": "…", "description": "…", "http_status": "401"}`
  (`400` geçersiz parametre · `401` kimlik yok/geçersiz · `403` 2FA zorunluluğu ya da yazma denemesi ·
  `404` bilinmeyen koleksiyon/nesne · `406` · `429` hız sınırı ya da eşzamanlı tarama sınırı · `500`).

### Kimlik doğrulama

Yalnız **kişisel API token'ı** kabul edilir (Hesabım → API token'ları, `/account`). **`read` kapsamı
yeterlidir** ve önerilir.

- `Authorization: Bearer kh_…`, **ya da**
- **HTTP Basic**: parola = token; kullanıcı adı **boş** ya da token sahibinin kullanıcı adı olmalıdır
  (başka bir ad → `401`; yanlış hesaba yapıştırılmış bir token sessizce çalışmaz). OpenCTI ve Sentinel
  gibi istemciler çoğunlukla yalnız Basic destekler.

Kimliksiz/geçersiz istek `401` + `WWW-Authenticate: Basic realm="KeywordHunter TAXII"` alır. İptal
edilmiş ya da süresi dolmuş token, pasif/silinmiş hesap reddedilir. `REQUIRE_2FA` kapsamındaki ama 2FA'sı
olmayan hesabın token'ı `403` alır. Oturum çerezi bu uçlarda **hiç okunmaz**; uçlar yalnız GET olduğundan
CSRF yüzeyi yoktur. Global IP hız sınırı (`RATE_LIMIT_RPS` / `RATE_LIMIT_BURST`) geçerlidir.

### curl örnekleri

```bash
export KH='https://keywordhunter.example'
export KH_TOKEN='kh_…'                         # read kapsamlı token
H='Accept: application/taxii+json;version=2.1'

# Keşif ve koleksiyonlar (Basic: boş kullanıcı adı, parola = token)
curl -sS -H "$H" -u ":$KH_TOKEN" "$KH/taxii2/"
curl -sS -H "$H" -u ":$KH_TOKEN" "$KH/taxii2/kh/collections/"

# Yüksek/kritik bulgular, 500'lük sayfalar (Bearer)
curl -sS -H "$H" -H "Authorization: Bearer $KH_TOKEN" \
  "$KH/taxii2/kh/collections/findings-high/objects/?limit=500"

# Sonraki sayfa: önceki yanıttaki "next"
curl -sS -H "$H" -u ":$KH_TOKEN" \
  "$KH/taxii2/kh/collections/findings-high/objects/?limit=500&next=eyJr…"

# Artımlı: belirli bir andan sonra eklenen yalnız göstergeler
curl -sS -H "$H" -u ":$KH_TOKEN" -G "$KH/taxii2/kh/collections/indicators/objects/" \
  --data-urlencode 'added_after=2026-10-01T00:00:00.000Z' --data-urlencode 'match[type]=indicator'

# Manifest ve tek nesne
curl -sS -H "$H" -u ":$KH_TOKEN" "$KH/taxii2/kh/collections/findings-all/manifest/?limit=50"
curl -sS -H "$H" -u ":$KH_TOKEN" "$KH/taxii2/kh/collections/findings-all/objects/indicator--<uuid>/"
```

Python (`taxii2-client`):

```python
from taxii2client.v21 import Server, as_pages
srv = Server("https://keywordhunter.example/taxii2/", user="", password="kh_…")
col = next(c for c in srv.default.collections if c.alias == "findings-high")
for env in as_pages(col.get_objects, per_request=500):
    for obj in env.get("objects", []):
        print(obj["type"], obj["id"])
```

### OpenCTI

1. KeywordHunter'da **Hesabım → API token'ları → Yeni token**: kapsam `read`, makul bir süre (ör. 365 gün).
2. OpenCTI'de **Data → Ingestion → TAXII feeds → Add (+)**:
   - **TAXII server URL:** API kökü — `https://keywordhunter.example/taxii2/kh/`
   - **TAXII version:** `2.1`
   - **Collection:** listeden seçin ya da kimliği yapıştırın (ör. `findings-high` → `a967eef9-…`)
   - **Authentication type:** *Basic user / password* — kullanıcı adı boş (ya da KeywordHunter
     kullanıcı adınız), parola = token. (*Bearer token* seçeneği de çalışır.)
   - **Import from date:** ilk çekişte geçmişin ne kadarının alınacağı.
3. Kaydedip başlatın. OpenCTI `added_after` ile artımlı çeker; gösterge ve ilişkiler KeywordHunter
   `identity`'si altında oluşur.

### Microsoft Sentinel

1. Token'ı yukarıdaki gibi `read` kapsamıyla oluşturun.
2. Sentinel → **Content hub** → *Threat Intelligence* çözümünü kurun → **Data connectors →
   Threat Intelligence – TAXII → Open connector page**.
3. Alanlar:
   - **Friendly name:** `KeywordHunter – high`
   - **API root URL:** `https://keywordhunter.example/taxii2/kh/`
   - **Collection ID:** koleksiyon kimliği (ör. `a967eef9-6fab-5e3f-a4b3-3e235c1dc779`)
   - **Username:** boş ya da KeywordHunter kullanıcı adınız · **Password:** token
   - **Import indicators / Polling frequency:** ihtiyaca göre (ör. "At most one month old", "Once an hour")
4. Sentinel yalnız `indicator` nesnelerini içe alır; `indicators` ya da `findings-high` koleksiyonu
   önerilir. Sentinel'in KeywordHunter'a erişebilmesi için sunucunun Sentinel'den (Azure'dan) HTTPS ile
   erişilebilir olması gerekir.

---

## 2. MISP gönderimi

**Ayarlar → Entegrasyonlar → MISP** (yalnız admin) ekranından yapılandırılır. Bulgular sayfasında birkaç
kayıt seçip toplu işlem çubuğundaki **"MISP'e gönder"** ile **tek bir MISP olayı** oluşturulur; başarı
bildirimi olayın bağlantısını içerir. Düğme, MISP yapılandırılmadıkça hiç görünmez ve yalnız analist ve
admin rollerine açıktır.

> **Çıkış Tor üzerinden DEĞİLDİR.** KeywordHunter MISP'e doğrudan (clearnet ya da kurum içi ağ)
> bağlanır. Ortam proxy'leri (`HTTP(S)_PROXY`) dahil hiçbir proxy kullanılmaz.

### Ayarlar

| Alan | Varsayılan | Not |
|---|---|---|
| MISP adresi | — | `https://misp.example.org` (alt yol olabilir). Kullanıcı bilgisi, sorgu, parça ve `.onion` kabul edilmez |
| API anahtarı | — | MISP → *Global Actions → My Profile → Auth keys*. Şifreli saklanır, **bir daha gösterilmez**; boş bırakılan alan kayıtlı anahtarı korur |
| TLS doğrulama | **açık** | Yalnız kurum içi, öz-imzalı sertifikalı MISP için kapatın |
| Özel ağa izin ver (`allowPrivate`) | kapalı | Kapalıyken yalnız genel adresler: loopback, RFC 1918, link-local (bulut metadata `169.254.169.254` dahil), CGNAT, `*.internal`/`*.local`/`*.lan` reddedilir ve **DNS çözümünden sonra** gerçek IP yeniden denetlenir (DNS rebinding). Açıkken kurum içi adresler kabul edilir; düz `http` de kabul edilir ama **yalnız kurum içi IP'lere** (genel bir IP adresi reddedilir, bir ad bağlantı anında genel bir IP'ye çözülürse bağlantı kurulmaz — anahtar internette şifresiz gitmez) |
| Dağıtım | `0` | `0` kurum · `1` topluluk · `2` bağlı topluluklar · `3` tüm topluluklar |
| Varsayılan etiketler | `tlp:amber, source:keywordhunter` | Virgülle; en fazla 20, her biri en fazla 128 karakter |
| Otomatik gönderim | kapalı | Açıldıktan **sonra** eklenen ve eşiği (varsayılan SEV 4+) geçen bulgular 10 dakikada bir tek olay olarak; her bulgu bir kez |

**Bağlantıyı test et** `GET /servers/getVersion` çağırır ve MISP sürümünü gösterir; formdaki kaydedilmemiş
değerlerle de çalışır.

### Eşleme

| KeywordHunter | MISP özniteliği | Kategori |
|---|---|---|
| Bulgunun URL'si | `url` (yorum = bulgu başlığı) | Network activity |
| `email` | `email-src` | Payload delivery |
| `bitcoin` | `btc` | Financial fraud |
| `monero` | `xmr` | Financial fraud |
| `ip` | `ip-dst` | Network activity |
| `hash` (32 / 40 / 64 hex) | `md5` / `sha1` / `sha256` | Payload delivery |
| `onion` | `domain` (yalnız ana makine) ya da `url` (yol varsa) | Network activity |
| `credit_card` | **gönderilmez** (PCI kişisel veri) | — |
| `api_key`, `ssh_key`, `phone`, `username` | **gönderilmez** (sır / kişisel veri) | — |

- Olay: `info` = "KeywordHunter: N dark web finding(s) — query: …", `analysis` = `0` (Initial),
  `published` = `false`, `threat_level_id` en yüksek kritiklikten: **SEV 4–5 → 1 (High)**,
  **SEV 3 → 2 (Medium)**, **SEV 1–2 → 3 (Low)**. Etiketler olay düzeyindedir.
- Tüm öznitelikler `to_ids = false` ve `distribution = 5` (olaydan devral): IOC'ler dark web
  metninden otomatik çıkarılmıştır ve doğrulanmamıştır; IDS dışa aktarımına almak analistin MISP'teki
  bilinçli kararıdır.
- Aynı (tür, değer) olayda bir kez yer alır; tek gönderimde en fazla 250 bulgu (Bulgular sayfasının en büyük sayfa boyutu) ve 5000 öznitelik.

### API

```bash
# Seçili bulguları tek bir MISP olayı olarak gönder (analyst+; token için "write" kapsamı)
curl -sS -X POST -H "Authorization: Bearer $KH_TOKEN" -H 'Content-Type: application/json' \
  -d '{"resultIds":[42,43,57]}' "$KH/api/integrations/misp/push"
# → {"success":true,"eventId":"1337","eventUuid":"…","eventUrl":"https://misp…/events/view/1337",
#    "findings":3,"attributes":11,"skipped":{"credit_card":1}}
```

| Uç | Yetki | Not |
|---|---|---|
| `GET /api/integrations/misp/config` | admin oturumu | Anahtar asla dönmez; yalnız `hasApiKey` |
| `POST /api/integrations/misp/config` | admin oturumu | `{baseUrl, apiKey, clearApiKey, verifyTls, allowPrivate, distribution, tags, autoPush, autoMinCriticality}`; verilmeyen alan korunur |
| `POST /api/integrations/misp/test` | admin oturumu | Kayıtlı ayarlar ya da gövdede taslak |
| `POST /api/integrations/misp/push` | analyst+ (oturum + CSRF ya da `write` token) | `{resultIds: [...]}` (1–200) |

Ayar ve test uçları API token ile **çağrılamaz** (`403 {"code": "session_required", "error": …}`);
gönderim ucu SOAR otomasyonu için `write` kapsamlı token'a açıktır.

---

## 3. Güvenlik notları

- **MISP anahtarı** `APP_SECRET`'tan HKDF ile, TOTP anahtarından **ayrı bir amaç etiketiyle** türetilen
  AES-256-GCM anahtarıyla şifrelenir (AAD bağlamı `misp:api_key`). `APP_SECRET` yoksa ya da `.env`'e
  kalıcı yazılamadıysa anahtar kaydı reddedilir (`409`) — düz metin saklanmaz. `APP_SECRET` değişirse
  kayıtlı anahtar çözülemez; yeniden girin.
- Anahtar hiçbir API yanıtında, HTML'de, logda ya da denetim kaydında yer almaz; MISP'e yalnız
  `Authorization` başlığında gider.
- **Anahtar başka sunucuya taşınamaz:** adres başka bir origin'e (şema + ana makine + port)
  çevrilirken anahtar yeniden girilmezse kayıt ve test reddedilir. Ele geçirilmiş bir admin oturumu,
  kayıtlı anahtarı kendi sunucusuna "test" ettirerek sızdıramaz.
- MISP istemcisi: 15 sn zaman aşımı, **yönlendirme izlenmez** (3xx hata sayılır), yanıt en fazla
  1 MB, TLS ≥ 1.2, aynı anda en fazla 2 MISP çağrısı; MISP'in hata iletisi kontrol karakterlerinden
  arındırılıp kısaltılarak gösterilir.
- **TAXII** yalnız token kabul eder; çerez yok sayılır, yazma yok, sorgu dizgisinden token okunmaz.
  Koleksiyon kimlikleri tahmin edilebilir olsa da her istek kimlik doğrulama ister. TAXII yanıtları
  `Cache-Control: no-store` taşır. Keşif adresleri istek `Host`'undan (yalnız güvenli karakterler) ve
  şemadan (`SECURE_COOKIES` → `https`, loopback hariç) üretilir; ters vekil arkasında `Host`'u
  koruyun.
- **Denetim kaydı:** `taxii.read` (token + koleksiyon başına dakikada en fazla bir satır; uç, nesne
  sayısı, `more`), `misp.config.update`, `misp.test`, `misp.push` (manuel: aktör; otomatik: `system`,
  `auto: true`; bulgu kimlikleri, öznitelik sayısı, olay kimliği). Hepsi `/audit`'te "Dışa aktarma" ya da
  "Sistem" kategorisinde görünür.

---

## English quick reference

**TAXII 2.1 (read-only)** — discovery `GET /taxii2/`, API root `/taxii2/kh/`, collections
`findings-all` (`4e8e1b28-53c1-5eee-a771-b92fbf71fb00`), `findings-high` (criticality ≥ 4,
`a967eef9-6fab-5e3f-a4b3-3e235c1dc779`) and `indicators` (IOC indicators only,
`8d76704e-d6e7-5396-bf6a-07de1854d968`); the alias also works in place of the ID.
`objects/` supports `added_after`, `limit` (default 100, max 1000), opaque `next`, `match[id]`,
`match[type]`, `match[version]`, `match[spec_version]`; `objects/{id}/` and `manifest/` are available.
Responses use `application/taxii+json;version=2.1` (object endpoints also honour
`application/stix+json;version=2.1`), unsupported `Accept` → `406`, errors are TAXII error objects,
`X-TAXII-Date-Added-First/Last` headers are set. `date_added` is the finding's `created_at`, moved
forward when new IOCs are extracted later or its criticality rises, so clients polling with
`added_after` also receive late IOCs and findings that later enter `findings-high`. Objects are
deduplicated (published once, with the first finding that contains them) and stable across requests.
Each request scans at most 5000 findings (also `objects/{id}/`: an empty envelope with `next` instead
of `404` when the budget runs out); at most 4 concurrent scans (2 per account), otherwise `429`.
Payment card and phone numbers are never published.

**Auth** — personal API token (`read` scope is enough) as `Authorization: Bearer kh_…` or HTTP Basic
(password = token, username empty or the token owner's). No cookies, no CSRF; `401` carries
`WWW-Authenticate: Basic realm="KeywordHunter TAXII"`.

**OpenCTI** — Data → Ingestion → TAXII feeds → Add: version 2.1, server URL = API root, pick the
collection, Basic auth with the token as password. **Sentinel** — "Threat Intelligence – TAXII" data
connector: API root URL, Collection ID, empty username, password = token.

**MISP push** — Settings → Integrations (admin): URL, API key (encrypted at rest with a key derived from
`APP_SECRET`, never shown again, must be re-entered when the URL moves to another server), TLS
verification (on), distribution 0–3, default tags, private-network toggle (SSRF guard: public targets only
unless enabled; plain `http` only to internal IPs), optional auto-push. Findings → select → **Send to MISP** creates one event
(`url`, `email-src`, `btc`, `xmr`, `ip-dst`, `md5`/`sha1`/`sha256`, `domain`; card numbers and secrets
are never sent; `to_ids=false`; threat level from the highest severity). Egress is direct
clearnet/intranet, **not Tor**. API: `POST /api/integrations/misp/push {"resultIds":[…]}`
(analyst+, `write` token allowed). Every push/test/config change is audited without the key.
