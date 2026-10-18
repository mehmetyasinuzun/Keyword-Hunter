-- KeywordHunter v0.4 (git tag v0.4; v0.3 ile aynı) boş bir veritabanında storage.New'in ürettiği şema:
-- yalnız search_results (auto_tags YOK; v0.5 onu ALTER ile created_at'in ARKASINA ekledi), search_history ve
-- graph_nodes. Sütun sırası düzeltmesini (canonicalizeSearchResults) sınayan benimseme fikstürüdür; DEĞİŞTİRMEYİN.
CREATE TABLE search_results (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			url TEXT NOT NULL,
			source TEXT NOT NULL,
			query TEXT NOT NULL,
			criticality INTEGER DEFAULT 1,
			category TEXT DEFAULT 'Genel',
			keyword_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(url, source, query)
		);
CREATE INDEX idx_search_results_query ON search_results(query);
CREATE INDEX idx_search_results_source ON search_results(source);
CREATE INDEX idx_search_results_created ON search_results(created_at);
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
