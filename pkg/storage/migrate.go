package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"keywordhunter-mvp/pkg/logger"
)

// migration sıralı, yalnız-ekleme bir şema adımı. Sürüm numarası listedeki
// sırasıdır (1'den başlar) ve PRAGMA user_version'a yazılır.
type migration struct {
	name string
	up   func(q runner) error
	// foreignKeysOff adımı yabancı anahtar denetimi KAPALIYKEN çalıştırır.
	// Başka tabloların başvurduğu bir tabloyu yeniden kuran adım için
	// zorunludur: anahtarlar açıkken DROP TABLE örtük bir DELETE yapar ve
	// ON DELETE CASCADE çocuk satırları (etiket, IOC, 2FA…) sessizce siler.
	// PRAGMA foreign_keys işlem içinde değiştirilemediğinden çerçeve onu
	// BEGIN'den önce kapatır, COMMIT'ten önce foreign_key_check ile yeni ihlal
	// olmadığını doğrular (varsa adım geri alınır) ve sonra yeniden açar.
	foreignKeysOff bool
}

// migrations şema geçmişi. KURAL: mevcut adımlar asla değiştirilmez, silinmez
// ya da yeniden sıralanmaz; her şema değişikliği sona yeni bir adım olarak
// eklenir (bkz. docs/MIGRATIONS.md). Her adım tek bir işlemde çalışır ve
// user_version aynı işlemde artırılır: yarım kalan adım hiç uygulanmamış sayılır.
var migrations = []migration{
	// Taban, v0.3/v0.4'ten kalma veritabanlarında search_results'u sütun
	// sırasını düzeltmek için yeniden kurabilir (bkz. canonicalizeSearchResults).
	{name: "baseline", up: migrateBaseline, foreignKeysOff: true},
	{name: "maintenance_runs", up: migrateMaintenanceRuns},
	{name: "search_results_last_seen", up: migrateResultsLastSeen},
	{name: "screenshots_file_index", up: migrateScreenshotsFileIndex},
}

// LatestSchemaVersion bu ikilinin bildiği en yüksek şema sürümü.
func LatestSchemaVersion() int { return len(migrations) }

// ErrSchemaTooNew veritabanı bu ikiliden daha yeni bir sürümle oluşturulmuş
// (sürüm düşürme koruması). Eski ikili yeni şemayı bilmediği için yazmaya
// başlamadan açılış reddedilir.
var ErrSchemaTooNew = errors.New("veritabanı şeması bu sürümden yeni")

// SchemaVersion veritabanının şema sürümü (PRAGMA user_version).
func (db *DB) SchemaVersion() (int, error) {
	var v int
	err := db.conn.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

// migrate bekleyen migrasyonları sırayla uygular. Sürümü bilinenden yeni bir
// veritabanında hiçbir şeye dokunmadan ErrSchemaTooNew döner.
func (db *DB) migrate() error {
	ctx := context.Background()
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	q := connRunner{ctx: ctx, c: c}

	cur, err := userVersion(q)
	if err != nil {
		return fmt.Errorf("şema sürümü okunamadı: %w", err)
	}
	latest := LatestSchemaVersion()
	if cur > latest {
		return fmt.Errorf("%w: veritabanı şema sürümü %d, bu KeywordHunter ikilisi en çok %d biliyor. "+
			"Veritabanı daha yeni bir sürümle açılmış; eski ikiliyle açmak verileri bozabilir. "+
			"Güncel sürümü çalıştırın ya da bu sürümle alınmış bir yedeği geri yükleyin",
			ErrSchemaTooNew, cur, latest)
	}
	if cur == latest {
		return nil
	}
	if cur == 0 {
		legacy, err := hasUserTables(q)
		if err != nil {
			return err
		}
		if legacy {
			logger.Info("Şema: sürümsüz (v0.17 öncesi) veritabanı benimseniyor; eksik sütun/indeks/tetikleyiciler tamamlanacak")
		}
	}
	for v := cur + 1; v <= latest; v++ {
		applied, err := applyMigration(q, v)
		if err != nil {
			return fmt.Errorf("şema migrasyonu %d (%s) başarısız: %w", v, migrations[v-1].name, err)
		}
		if applied {
			logger.Info("Şema migrasyonu %d (%s) uygulandı", v, migrations[v-1].name)
		}
	}
	return nil
}

// applyMigration v sürümüne geçişi tek bir yazma işleminde uygular. BEGIN
// IMMEDIATE yazma kilidini baştan alır: aynı dosyayı aynı anda açan ikinci bir
// süreç bekler, kilit alındıktan sonra sürüm yeniden okunur ve adım zaten
// uygulanmışsa atlanır.
func applyMigration(q connRunner, v int) (applied bool, err error) {
	m := migrations[v-1]
	if m.foreignKeysOff {
		if err := setForeignKeys(q, false); err != nil {
			return false, err
		}
		// Bağlantı havuza (uygulamaya) döner: anahtarlar her durumda yeniden açılır.
		defer func() {
			if fkErr := setForeignKeys(q, true); fkErr != nil && err == nil {
				err = fkErr
			}
		}()
	}
	if _, err := q.Exec(`BEGIN IMMEDIATE`); err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			if _, rbErr := q.Exec(`ROLLBACK`); rbErr != nil && err == nil {
				err = rbErr
			}
		}
	}()
	cur, err := userVersion(q)
	if err != nil {
		return false, err
	}
	if cur >= v {
		return false, nil // başka bir süreç uyguladı (ROLLBACK yalnız kilidi bırakır)
	}
	if cur != v-1 {
		return false, fmt.Errorf("beklenmeyen şema sürümü %d (beklenen %d)", cur, v-1)
	}
	var fkBefore int
	if m.foreignKeysOff {
		if fkBefore, err = foreignKeyViolations(q); err != nil {
			return false, err
		}
	}
	if err := m.up(q); err != nil {
		return false, err
	}
	if m.foreignKeysOff {
		// Eski veritabanında önceden kalmış ihlaller (anahtarların kapalı
		// olduğu sürümlerden) adımı engellemez; adımın yarattığı yeni ihlal engeller.
		fkAfter, err := foreignKeyViolations(q)
		if err != nil {
			return false, err
		}
		if fkAfter > fkBefore {
			return false, fmt.Errorf("adım %d yeni yabancı anahtar ihlali bıraktı (%d → %d); geri alındı", v, fkBefore, fkAfter)
		}
	}
	if _, err := q.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
		return false, err
	}
	if _, err := q.Exec(`COMMIT`); err != nil {
		return false, err
	}
	committed = true
	return true, nil
}

// setForeignKeys bağlantının yabancı anahtar denetimini açar/kapatır ve
// değişikliğin gerçekten uygulandığını doğrular (işlem içinde PRAGMA
// sessizce yok sayılır; bu bir programlama hatasıdır).
func setForeignKeys(q runner, on bool) error {
	want := 0
	if on {
		want = 1
	}
	if _, err := q.Exec(fmt.Sprintf(`PRAGMA foreign_keys = %d`, want)); err != nil {
		return err
	}
	var got int
	if err := q.QueryRow(`PRAGMA foreign_keys`).Scan(&got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("PRAGMA foreign_keys = %d uygulanamadı (açık bir işlem var mı?)", want)
	}
	return nil
}

// foreignKeyViolations PRAGMA foreign_key_check'in bulduğu ihlal sayısı.
func foreignKeyViolations(q runner) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&n)
	return n, err
}

func userVersion(q runner) (int, error) {
	var v int
	err := q.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

// hasUserTables veritabanında uygulama tablosu var mı (sürümsüz eski kurulum).
func hasUserTables(q runner) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&n)
	return n > 0, err
}

// connRunner tek bir *sql.Conn üzerinde runner: migrasyon işlemi BEGIN
// IMMEDIATE ile açıldığından database/sql'in Tx'i (ertelenmiş BEGIN) kullanılmaz.
type connRunner struct {
	ctx context.Context
	c   *sql.Conn
}

func (r connRunner) Exec(query string, args ...any) (sql.Result, error) {
	return r.c.ExecContext(r.ctx, query, args...)
}

func (r connRunner) Query(query string, args ...any) (*sql.Rows, error) {
	return r.c.QueryContext(r.ctx, query, args...)
}

func (r connRunner) QueryRow(query string, args ...any) *sql.Row {
	return r.c.QueryRowContext(r.ctx, query, args...)
}
