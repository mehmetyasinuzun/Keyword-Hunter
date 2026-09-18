package storage

import (
	"fmt"
	"strings"
	"time"
)

// ResultArtifact bir bulgudan çıkarılmış IOC/artifact (e-posta, cüzdan, IP, hash…).
type ResultArtifact struct {
	ID         int64     `json:"id"`
	ResultID   int64     `json:"resultId"`
	Type       string    `json:"type"`
	Value      string    `json:"value"`
	Context    string    `json:"context"`
	Confidence float64   `json:"confidence"`
	CreatedAt  time.Time `json:"createdAt"`
}

// ArtifactStat artifact türü başına sayım.
type ArtifactStat struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// EnsureArtifactSchema result_artifacts tablosunu oluşturur.
func (db *DB) EnsureArtifactSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS result_artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			result_id INTEGER NOT NULL,
			type TEXT NOT NULL,
			value TEXT NOT NULL,
			context TEXT DEFAULT '',
			confidence REAL DEFAULT 0.5,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(result_id, type, value),
			FOREIGN KEY (result_id) REFERENCES search_results(id) ON DELETE CASCADE
		);
		CREATE INDEX IF NOT EXISTS idx_result_artifacts_result ON result_artifacts(result_id);
		CREATE INDEX IF NOT EXISTS idx_result_artifacts_type ON result_artifacts(type);
		CREATE INDEX IF NOT EXISTS idx_result_artifacts_value ON result_artifacts(value);
	`)
	if err != nil {
		return fmt.Errorf("result_artifacts tablosu oluşturulamadı: %w", err)
	}
	return nil
}

// ReplaceArtifacts bir bulgunun artifact'larını yeniden yazar.
func (db *DB) ReplaceArtifacts(resultID int64, items []ResultArtifact) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM result_artifacts WHERE result_id = ?`, resultID); err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO result_artifacts (result_id, type, value, context, confidence) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	n := 0
	for _, a := range items {
		v := strings.TrimSpace(a.Value)
		if v == "" || len(v) > 512 {
			continue
		}
		ctx := a.Context
		if len(ctx) > 300 {
			ctx = ctx[:300]
		}
		if _, err := stmt.Exec(resultID, a.Type, v, ctx, a.Confidence); err == nil {
			n++
		}
	}
	return n, tx.Commit()
}

// GetArtifacts bir bulgunun artifact'larını döndürür.
func (db *DB) GetArtifacts(resultID int64) ([]ResultArtifact, error) {
	rows, err := db.conn.Query(`
		SELECT id, result_id, type, value, context, confidence, created_at
		FROM result_artifacts WHERE result_id = ?
		ORDER BY confidence DESC, type, value
	`, resultID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]ResultArtifact, 0)
	for rows.Next() {
		var a ResultArtifact
		if err := rows.Scan(&a.ID, &a.ResultID, &a.Type, &a.Value, &a.Context, &a.Confidence, &a.CreatedAt); err == nil {
			list = append(list, a)
		}
	}
	return list, rows.Err()
}

// ArtifactCounts tüm bulgular için tür bazlı sayım (dashboard/analytics).
func (db *DB) ArtifactCounts() ([]ArtifactStat, error) {
	rows, err := db.conn.Query(`SELECT type, COUNT(*) FROM result_artifacts GROUP BY type ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]ArtifactStat, 0)
	for rows.Next() {
		var s ArtifactStat
		if err := rows.Scan(&s.Type, &s.Count); err == nil {
			list = append(list, s)
		}
	}
	return list, rows.Err()
}

// ArtifactCountByResult verilen bulgu ID'leri için artifact sayısını döndürür (liste rozeti).
func (db *DB) ArtifactCountByResult(ids []int64) (map[int64]int, error) {
	out := make(map[int64]int)
	if len(ids) == 0 {
		return out, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.conn.Query("SELECT result_id, COUNT(*) FROM result_artifacts WHERE result_id IN ("+placeholders+") GROUP BY result_id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if rows.Scan(&id, &n) == nil {
			out[id] = n
		}
	}
	return out, nil
}

// SearchArtifacts değer içinde arama (IOC pivotu): aynı e-posta/cüzdan hangi bulgularda geçiyor?
func (db *DB) SearchArtifacts(typ, value string, limit int) ([]ResultArtifact, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	where := "1=1"
	args := []interface{}{}
	if typ != "" {
		where += " AND type = ?"
		args = append(args, typ)
	}
	if v := strings.TrimSpace(value); v != "" {
		where += " AND value LIKE ? ESCAPE '\\'"
		args = append(args, "%"+escapeLike(v)+"%")
	}
	args = append(args, limit)
	rows, err := db.conn.Query(`
		SELECT id, result_id, type, value, context, confidence, created_at
		FROM result_artifacts WHERE `+where+`
		ORDER BY created_at DESC LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]ResultArtifact, 0)
	for rows.Next() {
		var a ResultArtifact
		if err := rows.Scan(&a.ID, &a.ResultID, &a.Type, &a.Value, &a.Context, &a.Confidence, &a.CreatedAt); err == nil {
			list = append(list, a)
		}
	}
	return list, rows.Err()
}
