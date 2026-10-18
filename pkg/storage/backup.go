package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ─────────────────────────── Yedekleme ───────────────────────────
//
// Yedek, SQLite'ın VACUUM INTO komutuyla alınır: tek bir okuma işlemi içinde
// tutarlı bir anlık görüntü yeni bir dosyaya yazılır (WAL içeriği dahil);
// uygulama çalışırken alınabilir ve yazarları bekletmez (WAL). Ayrı, kısa
// ömürlü bir bağlantı kullanılır: uygulamanın tek bağlantılı havuzu kopyalama
// süresince kilitlenmez.
//
// Yedekten oturum tablosu (sessions) silinir: oturum kimlikleri taşıyıcı
// kimlik bilgisidir ve yedeği ele geçiren biri etkin oturumları devralamamalı.
// secure_delete ile silinen sayfalar sıfırlanır; silme son dosyada yeniden
// sayılarak doğrulanır (doğrulanamayan yedek başarısız sayılır).
//
// Diğer sırların bir kısmı korumalıdır (parola bcrypt, API token'ı SHA-256
// özeti, 2FA/MISP sırları APP_SECRET ile şifreli; APP_SECRET yedekte YOKTUR).
// AÇIK METİN kalanlar: site profili çerezleri (site_profiles.cookies — izlenen
// forumlardaki oturumlar) ve webhook adresleri (alert_config.webhook_url,
// scheduled_searches.webhook_url — Slack/Discord/Teams yol belirteçleri).
// Geri yüklemede çalışmaları gerektiği için yedekten silinmezler; yedek dosyası
// bu yüzden erişimi kısıtlı ve tercihen şifreli saklanmalıdır (README).

// BackupTo açık veritabanının tutarlı yedeğini dest'e yazar. dest yoksa
// oluşturulur; varsa boş olmalıdır (VACUUM INTO dolu dosyanın üzerine yazmaz).
func (db *DB) BackupTo(ctx context.Context, dest string) error {
	return vacuumInto(ctx, db.dsn, dest)
}

// BackupFile src'deki veritabanının yedeğini migrasyon çalıştırmadan alır
// (komut satırı: çalışan uygulamanın şemasına dokunulmaz). Kaynak SALT OKUNUR
// açılır: günlük kipi (journal_mode) değiştirilmez, -wal/-shm yaratılmaz ve
// arşivlenmiş bir yedeğin kopyası alınırken dosyanın kendisi değişmez.
func BackupFile(ctx context.Context, src, dest string) error {
	st, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("kaynak veritabanı: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("kaynak veritabanı düzenli bir dosya değil: %s", src)
	}
	dsn, err := fileURI(src, "mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	return vacuumInto(ctx, dsn, dest)
}

func vacuumInto(ctx context.Context, dsn, dest string) error {
	if strings.TrimSpace(dest) == "" {
		return errors.New("yedek hedefi boş")
	}
	// Mutlak yol: "file:" ile başlayan göreli bir ad URI olarak yorumlanmasın.
	abs, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	c, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	c.SetMaxOpenConns(1)
	if _, err := c.ExecContext(ctx, `VACUUM INTO ?`, abs); err != nil {
		return fmt.Errorf("VACUUM INTO: %w", err)
	}
	return scrubBackup(ctx, abs)
}

// fileURI yolu SQLite "file:" URI'sine çevirir. Yol olduğu gibi DSN'e
// eklenseydi sürücü onu ilk '?'ta bölerdi: adında '?' geçen bir dizindeki
// dosya yerine başka bir dosya açılır (ve oluşturulurdu).
func fileURI(path, query string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: query}
	return u.String(), nil
}

// scrubBackup yedekten oturumları güvenli siler (bkz. paket açıklaması) ve
// sonucu doğrular. Kapalı başarısız olur: dosya yoksa oluşturulmaz (mode=rw),
// sürümlü bir yedekte sessions tablosunun yokluğu ya da silmeden sonra kalan
// satır hatadır — oturumu taşıyan bir yedek asla "başarılı" sayılmaz.
func scrubBackup(ctx context.Context, path string) error {
	dsn, err := fileURI(path, "mode=rw&_pragma=secure_delete(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	c, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	c.SetMaxOpenConns(1)
	var n, version int
	if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&n); err != nil {
		return fmt.Errorf("yedek açılamadı: %w", err)
	}
	if err := c.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("yedek açılamadı: %w", err)
	}
	if n == 0 {
		if version >= 1 {
			return errors.New("yedekte sessions tablosu yok: oturum temizliği doğrulanamadı")
		}
		return nil // oturum tablosundan önceki sürümsüz bir veritabanı
	}
	if _, err := c.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return fmt.Errorf("yedekten oturumlar silinemedi: %w", err)
	}
	var left int
	if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&left); err != nil {
		return fmt.Errorf("yedek oturum denetimi: %w", err)
	}
	if left != 0 {
		return fmt.Errorf("yedekte %d oturum kaldı", left)
	}
	return nil
}

// ─────────────────────────── Doğrulama ───────────────────────────

// VerifyReport bir veritabanı dosyasının doğrulama raporu (verify-db).
type VerifyReport struct {
	Path                string            `json:"path"`
	SizeBytes           int64             `json:"sizeBytes"`
	IntegrityOK         bool              `json:"integrityOk"`
	Integrity           []string          `json:"integrity"` // "ok" ya da SQLite'ın bulduğu sorunlar
	SchemaVersion       int               `json:"schemaVersion"`
	LatestSchemaVersion int               `json:"latestSchemaVersion"`
	AuditChain          *AuditChainStatus `json:"auditChain,omitempty"` // denetim tablosu yoksa nil
	Counts              map[string]int    `json:"counts"`
}

// OK bütünlük ve (varsa) denetim zinciri temiz, şema bu ikilinin açabileceği sürümde.
func (r *VerifyReport) OK() bool {
	return r.IntegrityOK && (r.AuditChain == nil || r.AuditChain.OK) && r.SchemaVersion <= r.LatestSchemaVersion
}

// maxIntegrityErrors integrity_check'in döndüreceği en fazla sorun satırı.
const maxIntegrityErrors = 20

// openReadOnly dosyayı salt okunur açar (yoksa oluşturmaz, migrasyon yok).
func openReadOnly(path string) (*sql.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("düzenli bir dosya değil: %s", path)
	}
	dsn, err := fileURI(abs, "mode=ro&_pragma=busy_timeout(5000)&_time_format=sqlite")
	if err != nil {
		return nil, err
	}
	c, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	c.SetMaxOpenConns(1)
	return c, nil
}

// IntegrityCheck dosyada PRAGMA integrity_check çalıştırır; sorun varsa hata.
func IntegrityCheck(ctx context.Context, path string) error {
	c, err := openReadOnly(path)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	ok, msgs, err := integrityCheck(ctx, c)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("integrity_check başarısız: %s", strings.Join(msgs, "; "))
	}
	return nil
}

func integrityCheck(ctx context.Context, c *sql.DB) (bool, []string, error) {
	rows, err := c.QueryContext(ctx, fmt.Sprintf(`PRAGMA integrity_check(%d)`, maxIntegrityErrors))
	if err != nil {
		return false, nil, fmt.Errorf("integrity_check: %w", err)
	}
	defer rows.Close()
	var msgs []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return false, nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return false, nil, fmt.Errorf("integrity_check: %w", err)
	}
	return len(msgs) == 1 && msgs[0] == "ok", msgs, nil
}

// VerifyFile bir veritabanı dosyasını (ör. yedek) salt okunur doğrular:
// integrity_check, şema sürümü ve denetim kaydı hash zinciri. Dosyaya
// yazılmaz; bütünlüğü bozuk dosyada zincir doğrulaması atlanır.
func VerifyFile(ctx context.Context, path string) (*VerifyReport, error) {
	c, err := openReadOnly(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	rep := &VerifyReport{Path: path, LatestSchemaVersion: LatestSchemaVersion(), Counts: map[string]int{}}
	if st, err := os.Stat(path); err == nil {
		rep.SizeBytes = st.Size()
	}
	if rep.IntegrityOK, rep.Integrity, err = integrityCheck(ctx, c); err != nil {
		return nil, err
	}
	if err := c.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&rep.SchemaVersion); err != nil {
		return nil, err
	}
	if !rep.IntegrityOK {
		return rep, nil
	}
	tables := map[string]bool{}
	rows, err := c.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, err
		}
		tables[n] = true
	}
	rows.Close()
	for _, t := range []string{"search_results", "result_artifacts", "screenshots", "users", "audit_events"} {
		if !tables[t] {
			continue
		}
		var n int
		if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t).Scan(&n); err != nil {
			return nil, err
		}
		rep.Counts[t] = n
	}
	if tables["audit_events"] && tables["audit_chain_state"] {
		st, err := (&DB{conn: c}).AuditChainReport()
		if err != nil {
			return nil, fmt.Errorf("denetim zinciri: %w", err)
		}
		rep.AuditChain = &st
	}
	return rep, nil
}
