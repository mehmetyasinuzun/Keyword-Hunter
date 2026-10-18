-- KeywordHunter v0.16 (git b1209e9; sürümlü migrasyonlardan önceki son kod) boş bir veritabanında
-- storage.New'in ürettiği şema: sqlite_master oluşturma sırasıyla dökülmüştür (ALTER ile eklenen
-- sütunlar dahil). Eski kurulumları benimseme testinin fikstürüdür; DEĞİŞTİRMEYİN.
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
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP, note TEXT DEFAULT '', case_status TEXT DEFAULT '', feed_at DATETIME,
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
CREATE UNIQUE INDEX idx_graph_nodes_url_parentkey
		ON graph_nodes(url, COALESCE(parent_id, 0));
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
		, default_active INTEGER DEFAULT 1);
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
CREATE TABLE result_artifacts (
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
CREATE INDEX idx_result_artifacts_result ON result_artifacts(result_id);
CREATE INDEX idx_result_artifacts_type ON result_artifacts(type);
CREATE INDEX idx_result_artifacts_value ON result_artifacts(value);
CREATE TABLE site_profiles (
			host TEXT PRIMARY KEY,
			cookies TEXT DEFAULT '',
			user_agent TEXT DEFAULT '',
			render_js INTEGER DEFAULT 0,
			notes TEXT DEFAULT '',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE TABLE crawl_jobs (
			id TEXT PRIMARY KEY,
			seed_url TEXT NOT NULL,
			query TEXT DEFAULT '',
			depth INTEGER DEFAULT 1,
			max_pages INTEGER DEFAULT 50,
			same_domain INTEGER DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'pending',
			pages_done INTEGER DEFAULT 0,
			found_count INTEGER DEFAULT 0,
			saved_count INTEGER DEFAULT 0,
			message TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at DATETIME,
			finished_at DATETIME
		);
CREATE INDEX idx_crawl_jobs_status ON crawl_jobs(status);
CREATE INDEX idx_crawl_jobs_created ON crawl_jobs(created_at);
CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE COLLATE NOCASE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'analyst',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_login_at DATETIME
		);
CREATE INDEX idx_users_role ON users(role);
CREATE TABLE user_2fa (
			user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			enabled INTEGER NOT NULL DEFAULT 0,
			secret_enc TEXT NOT NULL DEFAULT '',
			pending_enc TEXT NOT NULL DEFAULT '',
			pending_at DATETIME,
			last_step INTEGER NOT NULL DEFAULT 0,
			enabled_at DATETIME,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
CREATE TABLE user_recovery_codes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			code_hash TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			used_at DATETIME,
			UNIQUE(user_id, code_hash)
		);
CREATE INDEX idx_recovery_codes_user ON user_recovery_codes(user_id);
CREATE TABLE api_tokens (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL DEFAULT 0,
			username TEXT NOT NULL COLLATE NOCASE,
			name TEXT NOT NULL,
			prefix TEXT NOT NULL,
			token_hash TEXT NOT NULL UNIQUE,
			scope TEXT NOT NULL DEFAULT 'read' CHECK (scope IN ('read', 'write')),
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_used_at DATETIME,
			last_used_ip TEXT NOT NULL DEFAULT '',
			expires_at DATETIME,
			revoked_at DATETIME
		);
CREATE INDEX idx_api_tokens_username ON api_tokens(username);
CREATE INDEX idx_search_results_case ON search_results(case_status);
CREATE TABLE audit_events (
			id INTEGER PRIMARY KEY,
			ts TEXT NOT NULL,
			actor TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			auth_method TEXT NOT NULL DEFAULT '',
			ip TEXT NOT NULL DEFAULT '',
			action TEXT NOT NULL,
			target TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT '',
			success INTEGER NOT NULL DEFAULT 1,
			prev_hash TEXT NOT NULL DEFAULT '',
			hash TEXT NOT NULL
		);
CREATE INDEX idx_audit_events_ts ON audit_events(ts);
CREATE INDEX idx_audit_events_actor ON audit_events(actor COLLATE NOCASE);
CREATE INDEX idx_audit_events_action ON audit_events(action);
CREATE TABLE audit_chain_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			anchor_id INTEGER NOT NULL DEFAULT 0,
			anchor_hash TEXT NOT NULL DEFAULT '',
			head_id INTEGER NOT NULL DEFAULT 0,
			head_hash TEXT NOT NULL DEFAULT '',
			pruned_total INTEGER NOT NULL DEFAULT 0,
			pruned_at TEXT NOT NULL DEFAULT '',
			pruning INTEGER NOT NULL DEFAULT 0
		);
CREATE TRIGGER audit_events_no_update BEFORE UPDATE ON audit_events
		BEGIN
			SELECT RAISE(ABORT, 'audit_events yalnız eklenebilir');
		END;
CREATE TRIGGER audit_events_no_delete BEFORE DELETE ON audit_events
		WHEN COALESCE((SELECT pruning FROM audit_chain_state WHERE id = 1), 0) = 0
		BEGIN
			SELECT RAISE(ABORT, 'audit_events yalnız saklama budamasıyla silinebilir');
		END;
CREATE TRIGGER audit_events_append_only BEFORE INSERT ON audit_events
		WHEN NEW.id IS NOT (SELECT head_id + 1 FROM audit_chain_state WHERE id = 1)
		  OR NEW.prev_hash IS NOT (SELECT head_hash FROM audit_chain_state WHERE id = 1)
		BEGIN
			SELECT RAISE(ABORT, 'audit_events yalnız zincirin sonuna eklenebilir');
		END;
CREATE TRIGGER audit_chain_state_no_delete BEFORE DELETE ON audit_chain_state
		BEGIN
			SELECT RAISE(ABORT, 'audit_chain_state silinemez');
		END;
CREATE TRIGGER audit_chain_state_guard BEFORE UPDATE ON audit_chain_state
		WHEN ((NEW.head_id IS NOT OLD.head_id OR NEW.head_hash IS NOT OLD.head_hash)
		      AND (NEW.head_id IS NOT OLD.head_id + 1
		           OR NEW.head_hash IS NOT (SELECT hash FROM audit_events WHERE id = NEW.head_id)))
		  OR ((NEW.anchor_id IS NOT OLD.anchor_id OR NEW.anchor_hash IS NOT OLD.anchor_hash)
		      AND (OLD.pruning = 0 OR NEW.anchor_id < OLD.anchor_id OR NEW.anchor_id > NEW.head_id))
		  OR NEW.pruned_total < OLD.pruned_total
		BEGIN
			SELECT RAISE(ABORT, 'audit_chain_state yalnız zincirle birlikte ilerleyebilir');
		END;
CREATE TABLE misp_config (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			base_url TEXT NOT NULL DEFAULT '',
			api_key_enc TEXT NOT NULL DEFAULT '',
			verify_tls INTEGER NOT NULL DEFAULT 1,
			distribution INTEGER NOT NULL DEFAULT 0,
			tags TEXT NOT NULL DEFAULT 'tlp:amber,source:keywordhunter',
			allow_private INTEGER NOT NULL DEFAULT 0,
			auto_push INTEGER NOT NULL DEFAULT 0,
			auto_min_criticality INTEGER NOT NULL DEFAULT 4,
			auto_since DATETIME,
			updated_at DATETIME,
			updated_by TEXT NOT NULL DEFAULT ''
		);
CREATE TABLE misp_pushed (
			result_id INTEGER PRIMARY KEY REFERENCES search_results(id) ON DELETE CASCADE,
			event_id TEXT NOT NULL DEFAULT '',
			pushed_at DATETIME NOT NULL
		);
CREATE TRIGGER trg_search_results_feed_at AFTER INSERT ON search_results
		FOR EACH ROW WHEN NEW.feed_at IS NULL
		BEGIN
			UPDATE search_results SET feed_at = COALESCE(NEW.created_at, CURRENT_TIMESTAMP) WHERE id = NEW.id;
		END;
CREATE INDEX idx_search_results_feed_at ON search_results(julianday(feed_at), id);
INSERT INTO alert_config (id) VALUES (1);
INSERT INTO audit_chain_state (id) VALUES (1);
