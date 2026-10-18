package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"modernc.org/sqlite" // Pure Go SQLite driver
)

// DB veritabanı bağlantısı
type DB struct {
	conn *sql.DB
	// dsn yedekleme gibi ayrı, kısa ömürlü bağlantılar için (bkz. backup.go).
	dsn string
	// path veritabanı dosyası (New'e verildiği gibi).
	path string

	// auditMu denetim zincirine eklemeyi ve budamayı sıralar: her satırın
	// prev_hash'i bir öncekinin hash'ine bağlı olduğundan iki ekleme yarışamaz.
	auditMu sync.Mutex
}

// New yeni veritabanı bağlantısı oluşturur
func New(dbPath string) (*DB, error) {
	// PRAGMA'ları DSN üzerinden ayarla - her bağlantıda tutarlı uygulanır.
	// _time_format=sqlite: time.Time değerleri SQLite'ın datetime() fonksiyonunun
	// parse edebildiği "YYYY-MM-DD HH:MM:SS.SSS+HH:MM" biçiminde yazılır. Sürücünün
	// varsayılan biçimi (Go time.String, monotonic saat dahil) SQL tarafında
	// karşılaştırılamıyordu.
	dsn := dsnFor(dbPath)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("veritabanı açılamadı: %w", err)
	}

	// SQLite'da tek writer modeli ve dosya kilitlenmelerini azaltmak için
	// bağlantı havuzunu tek bağlantı ile sınırla.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)

	// Bağlantıyı test et
	if err := pingWithRetry(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("veritabanı bağlantısı başarısız: %w", err)
	}

	db := &DB{conn: conn, dsn: dsn, path: dbPath}

	// Şemayı sürümlü migrasyonlarla güncelle (bkz. migrate.go). Sürümü bu
	// ikiliden yeni bir veritabanı ErrSchemaTooNew ile reddedilir.
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}

	// Yarıda kalmış bir budama bayrağı (işlem dışı yazılmışsa) silmeyi açık bırakmasın.
	if _, err := conn.Exec(`UPDATE audit_chain_state SET pruning = 0 WHERE id = 1 AND pruning <> 0`); err != nil {
		conn.Close()
		return nil, fmt.Errorf("audit_chain_state: %w", err)
	}

	// İsteğe bağlı izleme listesi tohumu (WATCHLIST_SEED; varsayılan kapalı)
	if err := db.SeedDefaultWatchlist(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("watchlist seed başarısız: %w", err)
	}

	return db, nil
}

// openRetryFor bağlantı açılışında SQLITE_BUSY için en uzun yeniden deneme.
const openRetryFor = 10 * time.Second

// pingWithRetry ilk bağlantıyı açar. Aynı (yeni) dosyayı aynı anda açan iki
// süreç WAL'a geçişte (DSN'deki journal_mode) kilit yükseltmesinde yarışabilir;
// SQLite bu kilitlenme durumunda meşgul işleyiciyi beklemeden SQLITE_BUSY
// döndürür. Yalnız bu hata kısa aralıklarla yeniden denenir.
func pingWithRetry(conn *sql.DB) error {
	deadline := time.Now().Add(openRetryFor)
	for {
		err := conn.Ping()
		if err == nil || !isBusy(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// isBusy SQLite'ın SQLITE_BUSY (genişletilmiş kodlar dahil) hatası mı.
func isBusy(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && se.Code()&0xff == 5 // SQLITE_BUSY
}

// dsnFor uygulamanın standart bağlantı dizgesi. secure_delete(FAST): silinen
// ya da güncellenen satırın eski içeriği, ek G/Ç gerektirmediği sürece sayfada
// sıfırlanır (saklama temizliği serbest sayfaları da sıfırlamak için ayrıca
// tam secure_delete kullanır; bkz. retention.go). Silinen sızıntı verisi
// veritabanı dosyasında adli araçlarla okunur kalmasın diye.
func dsnFor(dbPath string) string {
	return dbPath + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=secure_delete(FAST)&_time_format=sqlite"
}

// Path veritabanı dosyasının yolu (New'e verildiği gibi).
func (db *DB) Path() string { return db.path }

// Close veritabanı bağlantısını kapatır
func (db *DB) Close() error {
	return db.conn.Close()
}

// GetDBConn veritabanı bağlantısını döndürür
func (db *DB) GetDBConn() *sql.DB {
	return db.conn
}
