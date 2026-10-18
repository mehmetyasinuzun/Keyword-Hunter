# Şema migrasyonları

SQLite şeması `PRAGMA user_version` ile sürümlenir. Sıralı adımlar
`pkg/storage/migrate.go` içindeki `migrations` listesindedir; sürüm numarası adımın
listedeki sırasıdır (1'den başlar).

| Sürüm | Ad | İçerik |
|---|---|---|
| 1 | `baseline` | v0.16'nın boş veritabanında ürettiği şemanın tamamı (`pkg/storage/schema.go`) + sürümsüz eski veritabanlarını benimseme (`foreignKeysOff`) |
| 2 | `maintenance_runs` | Yedekleme / saklama işlerinin son çalışma durumu |
| 3 | `search_results_last_seen` | `search_results.last_seen_at`: saklama süresi bulgunun son görüldüğü andan sayılır |
| 4 | `screenshots_file_index` | `screenshots(file_path)` indeksi (saklama temizliğinde dosya başına tablo taraması yok) |

## Açılışta ne olur

1. `user_version` okunur.
2. Bu ikilinin bildiğinden **büyükse** açılış `storage.ErrSchemaTooNew` ile reddedilir ve dosyaya
   dokunulmaz (sürüm düşürme koruması). Güncel sürümü çalıştırın ya da eski sürümle alınmış bir yedeği
   geri yükleyin.
3. Eksik her adım sırayla, **kendi işleminde** (`BEGIN IMMEDIATE`) uygulanır; `user_version` aynı
   işlemde artırılır. Hata olursa adım tümüyle geri alınır ve sürüm değişmez.
4. `user_version = 0` ama tablolar varsa (v0.17 öncesi kurulum) veritabanı **benimsenir**: adım 1
   idempotenttir — eksik tablo, sütun (`ALTER TABLE … ADD COLUMN`, yalnız eksikse), indeks ve
   tetikleyicileri ekler, eski veri tutarsızlıklarını giderir (graf kök düğümü yinelenenleri,
   `feed_at` geri doldurma, eski zaman damgası biçimi). v0.3/v0.4'te oluşturulmuş veritabanlarında
   `search_results` taban sütun sırasıyla yeniden kurulur (orada `auto_tags` sonradan eklendiği için
   `created_at`'in arkasındaydı); böylece her benimsenen veritabanı yeni veritabanıyla birebir aynı şemaya
   ulaşır. Graf kök düğümü yinelenenleri dışında satır silinmez (yinelenenler tek satırda birleştirilir,
   çocukları korunan satıra bağlanır); `audit_events`'e dokunulmaz: denetim zinciri geçerli kalır.

## Yeni bir migrasyon eklemek

1. `pkg/storage/migrate.go` → `migrations` listesinin **sonuna** bir adım ekleyin:

   ```go
   var migrations = []migration{
   	{name: "baseline", up: migrateBaseline, foreignKeysOff: true},
   	// … mevcut adımlar (2–4) …
   	{name: "screenshots_file_index", up: migrateScreenshotsFileIndex},
   	{name: "watchlist_tags", up: func(q runner) error {
   		_, err := q.Exec(`ALTER TABLE watchlist ADD COLUMN tags TEXT NOT NULL DEFAULT ''`)
   		return err
   	}},
   }
   ```

2. Kurallar:
   - Mevcut adımları **asla** değiştirmeyin, silmeyin, yeniden sıralamayın; `schema.go`'daki taban
     metinleri donmuştur. Yanlış bir adımı yeni bir adımla düzeltin.
   - Adım küçük ve tek amaçlı olsun; adı (`name`) ne yaptığını söylesin.
   - Adım işlem içinde çalışır: `q` dışında bağlantı kullanmayın, `BEGIN`/`COMMIT` yazmayın.
   - ⚠️ **Başka tabloların başvurduğu bir tabloyu yeniden kurmak** (`search_results`, `users`,
     `watchlist`, `graph_nodes`…) yabancı anahtarlar açıkken **veri siler**: `DROP TABLE` örtük bir
     `DELETE` yapar ve `ON DELETE CASCADE` çocuk satırları (etiketler, IOC'ler, MISP kayıtları, 2FA ve
     kurtarma kodları, izleme kontrolleri) sessizce gider; işlem yine de başarıyla biter. Uygulama
     bağlantısı `foreign_keys(1)` ile açılır ve `PRAGMA foreign_keys` işlem içinde değiştirilemez (sessizce
     yok sayılır). Böyle bir adımı `foreignKeysOff: true` ile işaretleyin:

     ```go
     {name: "watchlist_rebuild", foreignKeysOff: true, up: func(q runner) error { … }},
     ```

     Çerçeve yabancı anahtarları `BEGIN`'den önce kapatır, `COMMIT`'ten önce `PRAGMA foreign_key_check`
     ile adımın yeni ihlal bırakmadığını doğrular (bırakırsa adım geri alınır) ve sonra yeniden açar.
     Adımın içinde SQLite'ın [12 adımlı yöntemini](https://www.sqlite.org/lang_altertable.html#otheralter)
     izleyin (yeni tablo → kopya → eski tabloyu düşür → yeniden adlandır → indeks/tetikleyicileri yeniden
     kur). Testte çocuk tabloların satır sayılarının korunduğunu da doğrulayın
     (`TestMigrate_ForeignKeysOffStep` örnektir).
   - Hataları yutmayın (`_, _ = q.Exec(...)` yok). Adım yalnız bir kez çalışır; "duplicate column"
     yalnız benimseme (adım 1) için beklenir ve orada sütun önce `columnExists` ile denetlenir.
   - `audit_events` / `audit_chain_state` üzerindeki tetikleyiciler UPDATE/DELETE'i reddeder; bu
     tablolara dokunan bir adım zinciri bozmamalıdır (yeni sütun eklemek hash girdisini değiştirmez).
   - Uzun sürecek veri dönüşümlerinden kaçının: adım uygulama açılışını bekletir.
3. Test ekleyin (`pkg/storage/migrate_test.go`): yeni adımdan önceki şemayla açılmış bir veritabanının
   yükseltildiğini ve satırların korunduğunu doğrulayın. `TestMigrate_*` testleri yeni ve benimsenmiş
   veritabanlarının şemasının (`sqlite_master`, normalize) aynı kaldığını zaten denetler.
4. Gerekirse `CHANGELOG.md`'de belirtin; geri dönüş (downgrade) yoktur, kullanıcıya yükseltmeden önce
   yedek almasını hatırlatın (`keywordhunter backup -out …`).

## English summary

The schema is versioned with `PRAGMA user_version`; `pkg/storage/migrate.go` holds an ordered,
append-only list. Each step runs in its own `BEGIN IMMEDIATE` transaction that also bumps
`user_version`. A database newer than the binary is refused (`ErrSchemaTooNew`). Pre-v0.17 databases
(`user_version = 0` with tables) are adopted by the idempotent baseline step, which also rebuilds
`search_results` of v0.3/v0.4-born databases into the canonical column order; apart from merging duplicate
graph root nodes it deletes no rows. To add a migration, append a small named step to `migrations` —
never edit or reorder existing ones — and cover it in `migrate_test.go`. A step that rebuilds a table
other tables reference **must** set `foreignKeysOff: true`: with foreign keys on, `DROP TABLE` cascades
and silently deletes child rows; the framework turns them off before `BEGIN`, checks
`PRAGMA foreign_key_check` for new violations before `COMMIT` and turns them back on.
