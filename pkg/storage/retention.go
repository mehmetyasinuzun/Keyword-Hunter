package storage

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"keywordhunter-mvp/pkg/logger"
)

// ─────────────────────────── Saklama süresi (retention) ───────────────────────────
//
// RESULT_RETENTION_DAYS / SCREENSHOT_RETENTION_DAYS süresi dolan bulguları ve
// ekran görüntülerini siler. Vaka durumu "investigating" (İnceleniyor) ya da
// "confirmed" (Doğrulandı) olan bulgular ve bunların URL'sine ait ekran
// görüntüleri (kanıt) süresiz tutulur.
//
// Bulgunun yaşı SON GÖRÜLDÜĞÜ andan sayılır (last_seen_at; aramalar hâlâ
// döndürüyorsa her kayıtta yenilenir, hiç yeniden görülmediyse created_at).
// İlk görülme anından sayılsaydı hâlâ yayında olan bir bulgu her N günde bir
// silinip bir sonraki aramada "yeni" olarak geri gelir, uyarı ve MISP/TAXII
// paylaşımı yinelenirdi.
//
// Bulgu silinince bağlı satırlar tutarlı biçimde gider: result_tags,
// result_artifacts ve misp_pushed yabancı anahtarla (ON DELETE CASCADE);
// graph_nodes'ta URL'nin kök düğümü ve tüm derinleştirme alt ağacı ile o
// URL'nin ekran görüntüleri — yalnız URL'yi kullanan başka bulgu kalmadıysa ve
// URL izleme listesinde değilse. Ekran görüntüsü dosyaları her parti işlendikten
// hemen sonra çağıranın onFiles işleviyle silinir; veritabanı önce, dosya sonra.
//
// Silme PRAGMA secure_delete açık bir bağlantıda yapılır (silinen içerik
// sayfalarda sıfırlanır) ve çalışma sonunda WAL kesilir: süresi dolan sızıntı
// verisi veritabanı dosyasında adli araçlarla okunur kalmaz. Daha önce alınmış
// yedekler bundan etkilenmez (BACKUP_KEEP ile kendi süreleri dolar).

// RetentionKeepCaseStatuses saklama süresinden muaf vaka durumları.
var RetentionKeepCaseStatuses = []string{"investigating", "confirmed"}

// retentionBatch bir işlemde silinen en fazla bulgu/ekran görüntüsü (SQLite
// parametre sınırının ve tek yazarın uzun kilitlenmesinin altında).
const retentionBatch = 500

// PurgeStats bir saklama temizliğinin sayımları.
type PurgeStats struct {
	Results     int64    `json:"results"`     // silinen bulgu
	Tags        int64    `json:"tags"`        // silinen bulgu etiketi
	Artifacts   int64    `json:"artifacts"`   // silinen IOC
	MISPPushed  int64    `json:"mispPushed"`  // silinen MISP gönderim kaydı
	GraphNodes  int64    `json:"graphNodes"`  // silinen graf düğümü (alt ağaç dahil)
	Screenshots int64    `json:"screenshots"` // silinen ekran görüntüsü satırı
	KeptCases   int64    `json:"keptCases"`   // süresi dolduğu hâlde vaka durumu nedeniyle tutulan bulgu
	Files       []string `json:"-"`           // silinecek ekran görüntüsü dosyaları (dizine göreli ad)
}

// Add iki temizliğin sayımlarını birleştirir.
func (p *PurgeStats) Add(o PurgeStats) {
	p.Results += o.Results
	p.Tags += o.Tags
	p.Artifacts += o.Artifacts
	p.MISPPushed += o.MISPPushed
	p.GraphNodes += o.GraphNodes
	p.Screenshots += o.Screenshots
	p.KeptCases += o.KeptCases
	p.Files = append(p.Files, o.Files...)
}

// resultAgeSQL bulgunun saklama yaşı: son görülme, yoksa ilk görülme.
const resultAgeSQL = `julianday(COALESCE(last_seen_at, created_at))`

// migrateResultsLastSeen şema 3: search_results.last_seen_at. Yeni eklenen
// bulguda NULL'dur (yaş created_at'ten sayılır); aynı bulgu bir aramada yeniden
// görüldükçe güncellenir (bkz. SaveResultsWithIDs). Geri doldurma gerekmez.
func migrateResultsLastSeen(q runner) error {
	_, err := q.Exec(`ALTER TABLE search_results ADD COLUMN last_seen_at DATETIME`)
	return err
}

// migrateScreenshotsFileIndex şema 4: screenshots(file_path) indeksi. Saklama
// temizliği silinen her dosyanın başka bir satırca kullanılıp kullanılmadığını
// sorar; indekssiz her sorgu tüm tabloyu tarıyordu (parti başına karesel) ve
// tek bağlantılı uygulama bu sürede bekliyordu.
func migrateScreenshotsFileIndex(q runner) error {
	_, err := q.Exec(`CREATE INDEX idx_screenshots_file ON screenshots(file_path)`)
	return err
}

// keepCaseSQL "durumu korunan vaka" koşulu (sabit liste; kullanıcı girdisi değil).
func keepCaseSQL(col string) string {
	return "COALESCE(" + col + ", '') IN ('" + strings.Join(RetentionKeepCaseStatuses, "','") + "')"
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, v := range ids {
		out[i] = v
	}
	return out
}

func stringArgs(ss []string) []any {
	out := make([]any, len(ss))
	for i, v := range ss {
		out[i] = v
	}
	return out
}

// PurgeResultsBefore son görülmesi (yoksa ilk görülmesi) cutoff'tan eski,
// korunan vaka durumunda olmayan bulguları ve bağlı satırlarını siler. Her
// parti kendi işleminde silinir; onFiles (nil olabilir) partinin artık hiçbir
// satırın kullanmadığı ekran görüntüsü dosyalarıyla işlem sonrası çağrılır.
// ctx iptal edilirse kalan partiler atlanır (yapılan silmeler kalıcıdır).
func (db *DB) PurgeResultsBefore(ctx context.Context, cutoff time.Time, onFiles func([]string)) (PurgeStats, error) {
	var st PurgeStats
	cut := cutoff.UTC()
	if err := db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_results
		WHERE `+resultAgeSQL+` < julianday(?) AND `+keepCaseSQL("case_status"), cut).Scan(&st.KeptCases); err != nil {
		return st, err
	}
	defer db.afterPurge(&st)
	for {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		var batch PurgeStats
		n, err := db.purgeResultBatch(ctx, cut, &batch)
		if err != nil {
			return st, err
		}
		st.Add(batch)
		if onFiles != nil && len(batch.Files) > 0 {
			onFiles(batch.Files)
		}
		if n < retentionBatch {
			return st, nil
		}
	}
}

func (db *DB) purgeResultBatch(ctx context.Context, cut time.Time, st *PurgeStats) (int, error) {
	var n int
	err := db.secureDeleteTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = purgeResultBatchTx(tx, cut, st)
		return err
	})
	if err != nil {
		*st = PurgeStats{}
		return 0, err
	}
	return n, nil
}

func purgeResultBatchTx(tx *sql.Tx, cut time.Time, st *PurgeStats) (int, error) {
	//nolint:gosec // G202: sabit SQL parçaları; değerler parametre
	rows, err := tx.Query(`SELECT id, url FROM search_results
		WHERE `+resultAgeSQL+` < julianday(?) AND NOT `+keepCaseSQL("case_status")+`
		ORDER BY id LIMIT ?`, cut, retentionBatch)
	if err != nil {
		return 0, err
	}
	var ids []int64
	urlSet := map[string]bool{}
	for rows.Next() {
		var id int64
		var u string
		if err := rows.Scan(&id, &u); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
		urlSet[u] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	in, args := placeholders(len(ids)), int64Args(ids)

	var tags, arts, misp int64
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM result_tags WHERE result_id IN (`+in+`)),
		(SELECT COUNT(*) FROM result_artifacts WHERE result_id IN (`+in+`)),
		(SELECT COUNT(*) FROM misp_pushed WHERE result_id IN (`+in+`))`,
		append(append(append([]any{}, args...), args...), args...)...).Scan(&tags, &arts, &misp); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM search_results WHERE id IN (`+in+`)`, args...) //nolint:gosec // G202: yalnız "?" yer tutucuları birleştirilir; değerler parametre
	if err != nil {
		return 0, err
	}
	deleted, _ := res.RowsAffected()

	// Artık hiçbir bulgunun kullanmadığı URL'ler: graf ve ekran görüntüsü temizliği.
	var orphan []string
	for u := range urlSet {
		var still int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM search_results WHERE url = ?)`, u).Scan(&still); err != nil {
			return 0, err
		}
		if still == 0 {
			orphan = append(orphan, u)
		}
	}
	var graph, shots int64
	var files []string
	if len(orphan) > 0 {
		uin, uargs := placeholders(len(orphan)), stringArgs(orphan)
		// Kök düğüm + derinleştirme alt ağacı; tek ifadede silinir (yabancı
		// anahtar denetimi ifade sonunda yapılır, çocuklar ebeveynle birlikte gider).
		//nolint:gosec // G202: yalnız "?" yer tutucuları birleştirilir; değerler parametre
		g, err := tx.Exec(`WITH RECURSIVE sub(id) AS (
				SELECT id FROM graph_nodes WHERE parent_id IS NULL AND url IN (`+uin+`)
				UNION SELECT g.id FROM graph_nodes g JOIN sub ON g.parent_id = sub.id)
			DELETE FROM graph_nodes WHERE id IN (SELECT id FROM sub)`, uargs...)
		if err != nil {
			return 0, err
		}
		graph, _ = g.RowsAffected()
		// İzleme listesindeki URL'lerin görüntüleri izleme kanıtıdır: yalnız
		// SCREENSHOT_RETENTION_DAYS ile silinir.
		files, shots, err = deleteScreenshots(tx, `target_url IN (`+uin+`)
			AND NOT EXISTS (SELECT 1 FROM watchlist w WHERE w.url = screenshots.target_url OR w.url || '/' = screenshots.target_url)`, uargs...)
		if err != nil {
			return 0, err
		}
	}
	st.Results += deleted
	st.Tags += tags
	st.Artifacts += arts
	st.MISPPushed += misp
	st.GraphNodes += graph
	st.Screenshots += shots
	st.Files = append(st.Files, files...)
	return len(ids), nil
}

// deleteScreenshots koşula uyan ekran görüntüsü satırlarını siler ve
// dosya adlarını döndürür (başka bir satırın hâlâ kullandığı dosya hariç).
func deleteScreenshots(tx *sql.Tx, where string, args ...any) ([]string, int64, error) {
	rows, err := tx.Query(`SELECT id, file_path FROM screenshots WHERE `+where, args...) //nolint:gosec // G202: where çağıranların sabit koşulları; değerler parametre
	if err != nil {
		return nil, 0, err
	}
	var ids []int64
	paths := map[string]bool{}
	for rows.Next() {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return nil, 0, err
		}
		ids = append(ids, id)
		if p != "" {
			paths[p] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var n int64
	for start := 0; start < len(ids); start += retentionBatch {
		chunk := ids[start:min(start+retentionBatch, len(ids))]
		res, err := tx.Exec(`DELETE FROM screenshots WHERE id IN (`+placeholders(len(chunk))+`)`, int64Args(chunk)...) //nolint:gosec // G202: yalnız "?" yer tutucuları birleştirilir; değerler parametre
		if err != nil {
			return nil, 0, err
		}
		k, _ := res.RowsAffected()
		n += k
	}
	var files []string
	for p := range paths {
		var used int
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM screenshots WHERE file_path = ?)`, p).Scan(&used); err != nil {
			return nil, 0, err
		}
		if used == 0 {
			files = append(files, p)
		}
	}
	return files, n, nil
}

// PurgeScreenshotsBefore taken_at'i cutoff'tan eski ekran görüntülerini siler;
// korunan vakadaki (İnceleniyor/Doğrulandı) bir bulgunun URL'sine ait olanlar
// kalır. onFiles ve ctx PurgeResultsBefore'daki gibidir.
func (db *DB) PurgeScreenshotsBefore(ctx context.Context, cutoff time.Time, onFiles func([]string)) (PurgeStats, error) {
	var st PurgeStats
	cut := cutoff.UTC()
	defer db.afterPurge(&st)
	for {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		var files []string
		var n int64
		err := db.secureDeleteTx(ctx, func(tx *sql.Tx) error {
			var err error
			files, n, err = deleteScreenshots(tx, `id IN (SELECT s.id FROM screenshots s
				WHERE julianday(s.taken_at) < julianday(?)
				  AND NOT EXISTS (SELECT 1 FROM search_results r WHERE r.url = s.target_url AND `+keepCaseSQL("r.case_status")+`)
				ORDER BY s.id LIMIT ?)`, cut, retentionBatch)
			return err
		})
		if err != nil {
			return st, err
		}
		st.Screenshots += n
		st.Files = append(st.Files, files...)
		if onFiles != nil && len(files) > 0 {
			onFiles(files)
		}
		if n < retentionBatch {
			return st, nil
		}
	}
}

// secureDeleteTx fn'i, silinen içeriği sayfalarda sıfırlayan (PRAGMA
// secure_delete) ayrılmış bağlantıda tek bir işlemde çalıştırır. Uygulamanın
// geri kalanı bu ayara maruz kalmaz: bağlantı havuza dönmeden kapatılır.
func (db *DB) secureDeleteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	c, err := db.conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if _, err := c.ExecContext(ctx, `PRAGMA secure_delete = ON`); err != nil {
		return err
	}
	defer func() {
		if _, err := c.ExecContext(context.Background(), `PRAGMA secure_delete = OFF`); err != nil {
			logger.Warn("secure_delete kapatılamadı: %v", err)
		}
	}()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// afterPurge bir şey silindiyse WAL'ı denetim noktasından geçirip keser:
// silinen satırların eski sayfa kopyaları -wal dosyasında kalmasın. En iyi
// çaba (eşzamanlı bir okuyucu, ör. yedek, varsa kesme bir sonraki sefere kalır).
func (db *DB) afterPurge(st *PurgeStats) {
	if st.Results == 0 && st.Screenshots == 0 && st.GraphNodes == 0 {
		return
	}
	var busy, logFrames, done int
	if err := db.conn.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &done); err != nil {
		logger.Warn("Saklama sonrası WAL kesilemedi: %v", err)
	} else if busy != 0 {
		logger.Debug("Saklama sonrası WAL kesilemedi: eşzamanlı okuyucu var")
	}
}
