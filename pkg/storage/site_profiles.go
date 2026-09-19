package storage

import (
	"database/sql"
	"strings"
	"time"
)

// SiteProfile bir onion/clearnet host için çekim ayarları: oturum çerezleri
// (giriş duvarını aşmak için analistin tarayıcısından kopyaladığı Cookie
// başlığı), özel User-Agent ve JS render zorunluluğu.
type SiteProfile struct {
	Host      string    `json:"host"`
	Cookies   string    `json:"cookies"` // "a=1; b=2"
	UserAgent string    `json:"userAgent"`
	RenderJS  bool      `json:"renderJs"`
	Notes     string    `json:"notes"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// EnsureSiteProfileSchema site_profiles tablosunu oluşturur.
func (db *DB) EnsureSiteProfileSchema() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS site_profiles (
			host TEXT PRIMARY KEY,
			cookies TEXT DEFAULT '',
			user_agent TEXT DEFAULT '',
			render_js INTEGER DEFAULT 0,
			notes TEXT DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

// NormalizeHost host adını küçük harfe çevirir, şema/yol/port kırpar.
func NormalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "http://"), "https://")
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	return h
}

// UpsertSiteProfile profil oluşturur/günceller.
func (db *DB) UpsertSiteProfile(p SiteProfile) error {
	p.Host = NormalizeHost(p.Host)
	render := 0
	if p.RenderJS {
		render = 1
	}
	_, err := db.conn.Exec(`
		INSERT INTO site_profiles (host, cookies, user_agent, render_js, notes, updated_at)
		VALUES (?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(host) DO UPDATE SET cookies=excluded.cookies, user_agent=excluded.user_agent,
			render_js=excluded.render_js, notes=excluded.notes, updated_at=CURRENT_TIMESTAMP
	`, p.Host, strings.TrimSpace(p.Cookies), strings.TrimSpace(p.UserAgent), render, strings.TrimSpace(p.Notes))
	return err
}

// GetSiteProfile host için profil döndürür; yoksa nil, nil.
func (db *DB) GetSiteProfile(host string) (*SiteProfile, error) {
	var p SiteProfile
	var render int
	err := db.conn.QueryRow(`SELECT host, cookies, user_agent, render_js, notes, updated_at FROM site_profiles WHERE host = ?`, NormalizeHost(host)).
		Scan(&p.Host, &p.Cookies, &p.UserAgent, &render, &p.Notes, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.RenderJS = render == 1
	return &p, nil
}

// ListSiteProfiles tüm profilleri döndürür (çerezler maskelenmez; yalnız yönetici erişir).
func (db *DB) ListSiteProfiles() ([]SiteProfile, error) {
	rows, err := db.conn.Query(`SELECT host, cookies, user_agent, render_js, notes, updated_at FROM site_profiles ORDER BY host`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]SiteProfile, 0)
	for rows.Next() {
		var p SiteProfile
		var render int
		if err := rows.Scan(&p.Host, &p.Cookies, &p.UserAgent, &render, &p.Notes, &p.UpdatedAt); err == nil {
			p.RenderJS = render == 1
			list = append(list, p)
		}
	}
	return list, rows.Err()
}

// DeleteSiteProfile profili siler.
func (db *DB) DeleteSiteProfile(host string) error {
	_, err := db.conn.Exec(`DELETE FROM site_profiles WHERE host = ?`, NormalizeHost(host))
	return err
}
