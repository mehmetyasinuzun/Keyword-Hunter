package storage

import "time"

// AlertConfig bildirim ayarlarını tutar
type AlertConfig struct {
	WebhookURL     string    `json:"webhookUrl"`
	MinCriticality int       `json:"minCriticality"`
	Enabled        bool      `json:"enabled"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// GetAlertConfig mevcut bildirim ayarlarını getirir
func (db *DB) GetAlertConfig() (*AlertConfig, error) {
	var cfg AlertConfig
	var enabled int

	err := db.conn.QueryRow(`
		SELECT webhook_url, min_criticality, enabled, updated_at
		FROM alert_config WHERE id = 1
	`).Scan(&cfg.WebhookURL, &cfg.MinCriticality, &enabled, &cfg.UpdatedAt)
	if err != nil {
		return &AlertConfig{MinCriticality: 3}, nil
	}

	cfg.Enabled = enabled == 1
	return &cfg, nil
}

// SaveAlertConfig bildirim ayarlarını kaydeder
func (db *DB) SaveAlertConfig(cfg AlertConfig) error {
	enabled := 0
	if cfg.Enabled {
		enabled = 1
	}
	_, err := db.conn.Exec(`
		UPDATE alert_config
		SET webhook_url = ?, min_criticality = ?, enabled = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = 1
	`, cfg.WebhookURL, cfg.MinCriticality, enabled)
	return err
}
