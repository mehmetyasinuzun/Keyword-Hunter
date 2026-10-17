package storage

import (
	"context"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/logger"
)

// ─────────────────────────── TAXII akışı (salt okunur) ───────────────────────────
//
// TAXII koleksiyonları bulguları (created_at, id) sırasıyla, kesintisiz ve
// kararlı biçimde sunar. Aynı STIX nesnesi (aynı URL'nin göstergesi, aynı IOC,
// aynı IOC→URL ilişkisi) birden fazla bulguda geçebilir; koleksiyonda yalnız
// İLK geçtiği bulguyla birlikte bir kez yayımlanır. "İlk" kararı SQL'de,
// koleksiyonun kendi süzgeci (kritiklik eşiği) içinde verilir; böylece sayfa
// nereden başlarsa başlasın aynı nesne iki kez dönmez ve hiçbiri kaybolmaz.
// Sıralama ve date_added, bulgunun created_at'i değil feed_at'idir: feed_at
// bulgu eklenince created_at'e eşitlenir; bulguya SONRADAN yeni bir IOC
// çıkarıldığında (otomatik etiketleme ekleme anından sonra çalışır) ya da
// kritiklik yükseldiğinde (ör. findings-high eşiğini geçince) o ana ilerler.
// Böylece added_after ile artımlı çeken istemciler (OpenCTI, Sentinel) sonradan
// eklenen IOC'leri ve eşiği yeni geçen bulguları da alır. Sıralama
// julianday(feed_at) üzerindendir: farklı biçimde yazılmış zaman damgaları da
// doğru sıralanır (ifade indeksi bunu hızlandırır).

// FeedFilter TAXII akışı için bulgu seçimi.
type FeedFilter struct {
	MinCriticality int       // koleksiyon eşiği (1 = tümü)
	AddedAfter     time.Time // sıfır değilse yalnız bu andan SONRA eklenenler
	AfterKey       string    // imleç: created_at ham değeri ("" = baştan)
	AfterID        int64     // imleç: bu bulgu DAHİL sonrası
	Limit          int
}

// FeedFinding akıştaki bir bulgu ve nesnelerinin ilk-geçiş bayrakları.
type FeedFinding struct {
	SearchResult
	Key       string    // feed_at ham metni (imleç için; biçimden bağımsız)
	FeedAt    time.Time // akışa (yeniden) eklenme anı = TAXII date_added
	FirstURL  bool      // URL göstergesi koleksiyonda ilk kez bu bulguyla mı geçiyor
	Artifacts []FeedArtifact
}

// FeedArtifact bulgunun bir IOC'si ve ilk-geçiş bayrakları.
type FeedArtifact struct {
	ResultArtifact
	FirstIndicator bool // IOC göstergesi koleksiyonda ilk kez burada
	FirstRelation  bool // IOC → URL ilişkisi koleksiyonda ilk kez burada
}

// feedBefore "p, r'den önce gelir" koşulu (akış sırası: zaman, sonra id).
const feedBefore = `(julianday(p.feed_at) < julianday(r.feed_at) OR (julianday(p.feed_at) = julianday(r.feed_at) AND p.id < r.id))`

// feedNow feed_at'e yazılan "şimdi" (UTC, milisaniye; julianday ile ayrıştırılır).
const feedNow = `strftime('%Y-%m-%d %H:%M:%f', 'now')`

// feedBumpOnCriticalityRaise UPDATE … SET içinde kullanılır: yeni kritiklik
// (ilk "?") eskisinden yüksekse feed_at şimdiye ilerler. SET ifadeleri satırın
// ESKİ değerlerini görür; bu yüzden "criticality" güncelleme öncesidir.
const feedBumpOnCriticalityRaise = `feed_at = CASE WHEN ? > criticality THEN ` + feedNow + ` ELSE feed_at END`

// EnsureFeedSchema TAXII akışının feed_at sütununu, ekleme tetikleyicisini ve
// sıralama indeksini kurar (idempotent). Eski kayıtlar created_at ile doldurulur.
func (db *DB) EnsureFeedSchema() error {
	if !db.columnExists("search_results", "feed_at") {
		if _, err := db.conn.Exec(`ALTER TABLE search_results ADD COLUMN feed_at DATETIME`); err != nil {
			return err
		}
	}
	if _, err := db.conn.Exec(`UPDATE search_results SET feed_at = COALESCE(created_at, CURRENT_TIMESTAMP) WHERE feed_at IS NULL`); err != nil {
		return err
	}
	// Yeni bulgu: feed_at = created_at (INSERT'ler bu sütunu bilmek zorunda değil).
	if _, err := db.conn.Exec(`
		CREATE TRIGGER IF NOT EXISTS trg_search_results_feed_at AFTER INSERT ON search_results
		FOR EACH ROW WHEN NEW.feed_at IS NULL
		BEGIN
			UPDATE search_results SET feed_at = COALESCE(NEW.created_at, CURRENT_TIMESTAMP) WHERE id = NEW.id;
		END`); err != nil {
		return err
	}
	_, _ = db.conn.Exec(`DROP INDEX IF EXISTS idx_search_results_feed`)
	if _, err := db.conn.Exec(`CREATE INDEX IF NOT EXISTS idx_search_results_feed_at ON search_results(julianday(feed_at), id)`); err != nil {
		logger.Warn("TAXII akış indeksi oluşturulamadı (akış indekssiz çalışır): %v", err)
	}
	return nil
}

// columnExists tabloda sütunun olup olmadığı (PRAGMA table_info).
func (db *DB) columnExists(table, column string) bool {
	rows, err := db.conn.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && strings.EqualFold(name, column) {
			return true
		}
	}
	return false
}

// UpdateCriticality analistin elle verdiği kritiklik ve kategoriyi yazar;
// kritiklik yükseldiyse bulgu TAXII akışına yeniden girer (feed_at). Kayıt
// yoksa false döner.
func (db *DB) UpdateCriticality(id int64, criticality int, category string) (bool, error) {
	res, err := db.conn.Exec(`UPDATE search_results SET `+feedBumpOnCriticalityRaise+`, criticality = ?, category = ? WHERE id = ?`,
		criticality, criticality, category, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// FeedFindings imleçten (dahil) itibaren bulguları akış sırasıyla döndürür.
// withArtifacts true ise her bulgunun IOC'leri ilk-geçiş bayraklarıyla eklenir.
// ctx iptal edilirse (istemci bağlantıyı kesti) sorgu yarıda bırakılır.
func (db *DB) FeedFindings(ctx context.Context, f FeedFilter, withArtifacts bool) ([]FeedFinding, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	if f.MinCriticality < 1 {
		f.MinCriticality = 1
	}
	// Parametre sırası sorgudaki "?" sırasını izler: SELECT'teki ilk-geçiş
	// alt sorgusunun eşiği, WHERE koşulları, LIMIT.
	args := []any{f.MinCriticality, f.MinCriticality}
	where := []string{"r.criticality >= ?", "r.feed_at IS NOT NULL"}
	if !f.AddedAfter.IsZero() {
		where = append(where, "julianday(r.feed_at) > julianday(?)")
		args = append(args, feedTime(f.AddedAfter))
	}
	if f.AfterKey != "" {
		where = append(where, "(julianday(r.feed_at) > julianday(?) OR (julianday(r.feed_at) = julianday(?) AND r.id >= ?))")
		args = append(args, f.AfterKey, f.AfterKey, f.AfterID)
	}
	args = append(args, f.Limit)
	rows, err := db.conn.QueryContext(ctx, `
		SELECT r.id, r.title, r.url, r.source, r.query, r.criticality, COALESCE(r.category, ''), r.keyword_count,
		       COALESCE(r.auto_tags, ''), r.created_at, CAST(r.feed_at AS TEXT),
		       COALESCE(strftime('%Y-%m-%dT%H:%M:%fZ', r.feed_at), ''),
		       NOT EXISTS (SELECT 1 FROM search_results p WHERE p.url = r.url AND p.criticality >= ? AND `+feedBefore+`)
		FROM search_results r
		WHERE `+strings.Join(where, " AND ")+`
		ORDER BY julianday(r.feed_at), r.id
		LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]FeedFinding, 0, f.Limit)
	index := make(map[int64]int, f.Limit)
	for rows.Next() {
		var ff FeedFinding
		var feedAt string
		if err := rows.Scan(&ff.ID, &ff.Title, &ff.URL, &ff.Source, &ff.Query, &ff.Criticality, &ff.Category,
			&ff.KeywordCount, &ff.AutoTags, &ff.CreatedAt, &ff.Key, &feedAt, &ff.FirstURL); err != nil {
			return nil, err
		}
		ff.FeedAt = ff.CreatedAt
		if t, err := time.Parse("2006-01-02T15:04:05.000Z", feedAt); err == nil && t.After(ff.CreatedAt) {
			ff.FeedAt = t
		}
		index[ff.ID] = len(out)
		out = append(out, ff)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if !withArtifacts || len(out) == 0 {
		return out, nil
	}

	ids := make([]any, 0, len(out)+2)
	ids = append(ids, f.MinCriticality, f.MinCriticality)
	for _, ff := range out {
		ids = append(ids, ff.ID)
	}
	arows, err := db.conn.QueryContext(ctx, `
		SELECT a.id, a.result_id, a.type, a.value, COALESCE(a.context, ''), a.confidence, a.created_at,
		       NOT EXISTS (SELECT 1 FROM result_artifacts a2 JOIN search_results p ON p.id = a2.result_id
		                   WHERE a2.type = a.type AND a2.value = a.value AND p.criticality >= ? AND `+feedBefore+`),
		       NOT EXISTS (SELECT 1 FROM result_artifacts a2 JOIN search_results p ON p.id = a2.result_id
		                   WHERE a2.type = a.type AND a2.value = a.value AND p.url = r.url AND p.criticality >= ? AND `+feedBefore+`)
		FROM result_artifacts a JOIN search_results r ON r.id = a.result_id
		WHERE a.result_id IN (`+strings.TrimRight(strings.Repeat("?,", len(out)), ",")+`)
		ORDER BY a.result_id, a.type, a.value`, ids...)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var fa FeedArtifact
		if err := arows.Scan(&fa.ID, &fa.ResultID, &fa.Type, &fa.Value, &fa.Context, &fa.Confidence, &fa.CreatedAt,
			&fa.FirstIndicator, &fa.FirstRelation); err != nil {
			return nil, err
		}
		if i, ok := index[fa.ResultID]; ok {
			out[i].Artifacts = append(out[i].Artifacts, fa)
		}
	}
	return out, arows.Err()
}

// feedTime Go zamanını SQLite'ın julianday() ile ayrıştırabildiği UTC metne çevirir.
func feedTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000")
}
