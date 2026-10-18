package storage

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ─────────────────────────── MISP entegrasyonu ───────────────────────────
//
// misp_config tek satırlık (id = 1) ayar kaydıdır. API anahtarı yalnız şifreli
// (AES-256-GCM, APP_SECRET'tan türetilen ayrı bir alt anahtar) saklanır;
// şifreleme/çözme web katmanındadır, depo yalnız opak metin görür.
// misp_pushed hangi bulgunun hangi MISP olayına gönderildiğini tutar: otomatik
// gönderim aynı bulguyu ikinci kez göndermez.

// MISP ayar varsayılanları.
const (
	DefaultMISPTags        = "tlp:amber,source:keywordhunter"
	DefaultMISPAutoMinCrit = 4
)

// MISPConfig MISP entegrasyon ayarları.
type MISPConfig struct {
	BaseURL            string
	APIKeyEnc          string // şifreli API anahtarı ("" = yok)
	VerifyTLS          bool
	Distribution       int // 0 kurum, 1 topluluk, 2 bağlı topluluklar, 3 herkes
	Tags               string
	AllowPrivate       bool
	AutoPush           bool
	AutoMinCriticality int
	AutoSince          *time.Time // otomatik gönderimin açıldığı an (öncesi gönderilmez)
	UpdatedAt          *time.Time
	UpdatedBy          string
}

// DefaultMISPConfig hiç kaydedilmemiş yapılandırma.
func DefaultMISPConfig() MISPConfig {
	return MISPConfig{VerifyTLS: true, Tags: DefaultMISPTags, AutoMinCriticality: DefaultMISPAutoMinCrit}
}

// GetMISPConfig kayıtlı ayarları döndürür; kayıt yoksa varsayılanlar (hata değil).
func (db *DB) GetMISPConfig() (MISPConfig, error) {
	cfg := DefaultMISPConfig()
	var verify, allowPriv, auto int
	var since, updated sql.NullTime
	err := db.conn.QueryRow(`
		SELECT base_url, api_key_enc, verify_tls, distribution, tags, allow_private, auto_push,
		       auto_min_criticality, auto_since, updated_at, updated_by
		FROM misp_config WHERE id = 1`).
		Scan(&cfg.BaseURL, &cfg.APIKeyEnc, &verify, &cfg.Distribution, &cfg.Tags, &allowPriv, &auto,
			&cfg.AutoMinCriticality, &since, &updated, &cfg.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultMISPConfig(), nil
	}
	if err != nil {
		return DefaultMISPConfig(), err
	}
	cfg.VerifyTLS, cfg.AllowPrivate, cfg.AutoPush = verify == 1, allowPriv == 1, auto == 1
	if since.Valid {
		cfg.AutoSince = &since.Time
	}
	if updated.Valid {
		cfg.UpdatedAt = &updated.Time
	}
	return cfg, nil
}

// SaveMISPConfig ayarları yazar (tek satır, upsert).
func (db *DB) SaveMISPConfig(cfg MISPConfig, by string, now time.Time) error {
	var since any
	if cfg.AutoSince != nil {
		since = cfg.AutoSince.UTC()
	}
	_, err := db.conn.Exec(`
		INSERT INTO misp_config (id, base_url, api_key_enc, verify_tls, distribution, tags, allow_private,
		                         auto_push, auto_min_criticality, auto_since, updated_at, updated_by)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			base_url = excluded.base_url, api_key_enc = excluded.api_key_enc, verify_tls = excluded.verify_tls,
			distribution = excluded.distribution, tags = excluded.tags, allow_private = excluded.allow_private,
			auto_push = excluded.auto_push, auto_min_criticality = excluded.auto_min_criticality,
			auto_since = excluded.auto_since, updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		cfg.BaseURL, cfg.APIKeyEnc, boolInt(cfg.VerifyTLS), cfg.Distribution, cfg.Tags, boolInt(cfg.AllowPrivate),
		boolInt(cfg.AutoPush), cfg.AutoMinCriticality, since, now.UTC(), by)
	return err
}

// RecordMISPPush bulguların gönderildiği MISP olayını kaydeder (yeniden
// gönderimde olay kimliği ve zaman güncellenir).
func (db *DB) RecordMISPPush(resultIDs []int64, eventID string, at time.Time) error {
	if len(resultIDs) == 0 {
		return nil
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
		INSERT INTO misp_pushed (result_id, event_id, pushed_at)
		SELECT id, ?, ? FROM search_results WHERE id = ?
		ON CONFLICT(result_id) DO UPDATE SET event_id = excluded.event_id, pushed_at = excluded.pushed_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, id := range resultIDs {
		if _, err := stmt.Exec(eventID, at.UTC(), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MISPAutoPushCandidates otomatik gönderime aday bulgular: eşik ve üzeri,
// otomatik gönderim açıldıktan sonra eklenmiş ve daha önce gönderilmemiş.
func (db *DB) MISPAutoPushCandidates(minCriticality int, since time.Time, limit int) ([]SearchResult, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := db.conn.Query(`
		SELECT id, title, url, source, query, criticality, COALESCE(category, ''), keyword_count, COALESCE(auto_tags, ''), created_at
		FROM search_results
		WHERE criticality >= ? AND julianday(created_at) >= julianday(?)
		  AND id NOT IN (SELECT result_id FROM misp_pushed)
		ORDER BY created_at, id
		LIMIT ?`, minCriticality, feedTime(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.AutoTags, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetResultsByIDs verilen kimliklerdeki bulguları kimlik sırasıyla döndürür
// (olmayanlar atlanır).
func (db *DB) GetResultsByIDs(ids []int64) ([]SearchResult, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	//nolint:gosec // G202: yalnız "?" yer tutucuları birleştirilir; değerler parametre
	rows, err := db.conn.Query(`
		SELECT id, title, url, source, query, criticality, COALESCE(category, ''), keyword_count, COALESCE(auto_tags, ''), created_at
		FROM search_results
		WHERE id IN (`+strings.TrimRight(strings.Repeat("?,", len(ids)), ",")+`)
		ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ID, &r.Title, &r.URL, &r.Source, &r.Query, &r.Criticality, &r.Category, &r.KeywordCount, &r.AutoTags, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
