-- KeywordHunter v0.9 (git tag v0.9) boş bir veritabanında storage.New'in ürettiği şema:
-- vaka/feed_at/default_active sütunları, graf tekillik indeksi, kullanıcı/2FA/token/denetim/MISP
-- tabloları yok. Eski kurulumları benimseme testinin fikstürüdür; DEĞİŞTİRMEYİN.
CREATE TABLE search_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			source TEXT NOT NULL,
			query TEXT NOT NULL,
			criticality INTEGER DEFAULT 1,
			category TEXT DEFAULT 'Genel',
			keyword_count INTEGER DEFAULT 0,
			auto_tags TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(url, source, query)
		);
CREATE INDEX idx_search_results_query ON search_results(query);
CREATE INDEX idx_search_results_source ON search_results(source);
CREATE INDEX idx_search_results_created ON search_results(created_at);
CREATE TABLE result_tags (
			result_id INTEGER NOT NULL,
			tag TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (result_id, tag),
			FOREIGN KEY (result_id) REFERENCES search_results(id) ON DELETE CASCADE
		);
CREATE INDEX idx_result_tags_tag ON result_tags(tag);
CREATE INDEX idx_result_tags_result ON result_tags(result_id);
CREATE TABLE search_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			query TEXT NOT NULL,
			result_count INTEGER DEFAULT 0,
			searched_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE TABLE graph_nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			title TEXT NOT NULL,
			domain TEXT NOT NULL,
			parent_id INTEGER,
			depth INTEGER DEFAULT 0,
			link_type TEXT DEFAULT 'search',
			source_query TEXT,
			discovered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_expanded INTEGER DEFAULT 0,
			FOREIGN KEY (parent_id) REFERENCES graph_nodes(id),
			UNIQUE(url, parent_id)
		);
CREATE INDEX idx_graph_nodes_parent ON graph_nodes(parent_id);
CREATE INDEX idx_graph_nodes_domain ON graph_nodes(domain);
CREATE INDEX idx_graph_nodes_depth ON graph_nodes(depth);
CREATE INDEX idx_graph_nodes_query ON graph_nodes(source_query);
CREATE TABLE tagging_jobs (
			id TEXT PRIMARY KEY,
			query TEXT,
			total_count INTEGER NOT NULL,
			processed_count INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			failure_count INTEGER DEFAULT 0,
			status TEXT NOT NULL,
			error_message TEXT DEFAULT '',
			result_ids_json TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME
		);
CREATE INDEX idx_tagging_jobs_status ON tagging_jobs(status);
CREATE INDEX idx_tagging_jobs_created ON tagging_jobs(created_at);
CREATE TABLE alert_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			webhook_url TEXT DEFAULT '',
			min_criticality INTEGER DEFAULT 3,
			enabled INTEGER DEFAULT 0,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL,
			csrf_token TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_seen_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_sessions_username ON sessions(username);
CREATE TABLE watchlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			url TEXT NOT NULL UNIQUE,
			category TEXT DEFAULT 'Genel',
			notes TEXT DEFAULT '',
			enabled INTEGER DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE INDEX idx_watchlist_enabled ON watchlist(enabled);
CREATE TABLE watchlist_checks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			watchlist_id INTEGER NOT NULL,
			checked_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			status TEXT NOT NULL,
			http_code INTEGER DEFAULT 0,
			response_ms INTEGER DEFAULT 0,
			content_hash TEXT DEFAULT '',
			title TEXT DEFAULT '',
			changed INTEGER DEFAULT 0,
			FOREIGN KEY (watchlist_id) REFERENCES watchlist(id) ON DELETE CASCADE
		);
CREATE INDEX idx_watchlist_checks_item ON watchlist_checks(watchlist_id);
CREATE INDEX idx_watchlist_checks_time ON watchlist_checks(checked_at);
CREATE TABLE scheduled_searches (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			query TEXT NOT NULL,
			interval_minutes INTEGER NOT NULL DEFAULT 60,
			enabled INTEGER NOT NULL DEFAULT 1,
			webhook_url TEXT DEFAULT '',
			alert_threshold INTEGER DEFAULT 3,
			last_run_at DATETIME,
			next_run_at DATETIME,
			last_result_count INTEGER DEFAULT 0,
			last_new_count INTEGER DEFAULT 0,
			total_runs INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE INDEX idx_scheduled_due ON scheduled_searches(enabled, next_run_at);
CREATE TABLE engine_stats (
			name TEXT PRIMARY KEY,
			url TEXT NOT NULL,
			is_active INTEGER DEFAULT 1,
			last_checked_at DATETIME,
			last_status TEXT DEFAULT 'unknown',
			last_response_ms INTEGER DEFAULT 0,
			success_count INTEGER DEFAULT 0,
			fail_count INTEGER DEFAULT 0,
			total_results INTEGER DEFAULT 0,
			consecutive_fails INTEGER DEFAULT 0,
			added_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE TABLE screenshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			target_url TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'manual',
			ref_id INTEGER NOT NULL DEFAULT 0,
			file_path TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			width INTEGER DEFAULT 0,
			height INTEGER DEFAULT 0,
			bytes INTEGER DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'ok',
			error_msg TEXT DEFAULT '',
			title TEXT DEFAULT '',
			challenge INTEGER DEFAULT 0,
			challenge_kind TEXT DEFAULT '',
			taken_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE INDEX idx_screenshots_target ON screenshots(target_url, taken_at);
CREATE INDEX idx_screenshots_taken ON screenshots(taken_at);
INSERT INTO alert_config (id) VALUES (1);
