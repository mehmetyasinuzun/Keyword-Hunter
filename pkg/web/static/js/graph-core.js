/**
 * KeywordHunter — İlişki haritası: çekirdek (durum, tema paleti, kademeli veri yükleme).
 *
 * Modüller tek bir ad alanını paylaşır: window.KHGraph (G).
 *   graph-core.js   → durum, ayarlar, palet, veri yükleme, istatistik
 *   graph-render.js → D3 yerleşimleri, geçişli çizim, yakınlaştırma, vurgulama
 *   graph-ui.js     → ipucu, bağlam menüsü, ayar paneli, derinleştir / izle / ara
 *   pages/graph.js  → KH.act bağlamaları + açılış
 *
 * Dark web kaynaklı başlık/URL'ler yalnızca textContent / D3 .text() ile basılır.
 */
(function () {
    'use strict';

    const G = window.KHGraph = window.KHGraph || {};
    const T = (tr, en) => KH.tr(tr, en);
    const $ = (id) => document.getElementById(id);

    // ── Durum ───────────────────────────────────────────────────────────
    G.state = {
        data: null,            // ham ağaç (sunucu verisi; derinleştirme ile büyür)
        view: null,            // çizilen kök (odaklı sorguda sorgu düğümü)
        root: null,            // son çizilen d3.hierarchy
        layout: 'radial',
        query: '',
        queries: [],
        selected: null,        // bağlam menüsündeki düğüm (d3 düğümü)
        defaultDepth: 2,       // bu derinliğin altındaki düğümler varsayılan açık
        open: new Map(),       // kullanıcı aç/kapat geçersiz kılmaları: id → bool
        fresh: new Set(),      // yeni açılan bağlantılar (kesik çizgi animasyonu)
        userZoomed: false,     // kullanıcı elle yakınlaştırdıysa otomatik sığdırma yok
        activeLoadId: 0,
        controller: null,
        load: { inProgress: false, query: '', profile: null, counts: { queries: 0, engines: 0, results: 0 } }
    };

    const DEFAULT_SETTINGS = Object.freeze({ fontSize: 14, spacing: 1, truncateLength: 25, showAllLabels: true });
    G.DEFAULT_SETTINGS = DEFAULT_SETTINGS;
    G.settings = Object.assign({}, DEFAULT_SETTINGS);
    try {
        const saved = JSON.parse(localStorage.getItem('kh.graph.settings') || 'null');
        if (saved && typeof saved === 'object') {
            ['fontSize', 'spacing', 'truncateLength'].forEach(k => { if (isFinite(saved[k])) G.settings[k] = Number(saved[k]); });
            if (typeof saved.showAllLabels === 'boolean') G.settings.showAllLabels = saved.showAllLabels;
        }
        const layout = localStorage.getItem('kh.graph.layout');
        if (['radial', 'tree', 'force'].includes(layout)) G.state.layout = layout;
    } catch (_) { /* depolama kapalı olabilir */ }
    G.saveSettings = function () {
        try { localStorage.setItem('kh.graph.settings', JSON.stringify(G.settings)); localStorage.setItem('kh.graph.layout', G.state.layout); } catch (_) { }
    };

    const PROFILES = Object.freeze({
        overview: { maxQueries: 20, maxEnginesPerQuery: 8, maxResultsPerEngine: 160, resultPageSize: 60, maxEnginesTotal: 80, renderDebounceMs: 320 },
        focused: { maxQueries: 1, maxEnginesPerQuery: 28, maxResultsPerEngine: 700, resultPageSize: 140, maxEnginesTotal: 120, renderDebounceMs: 260 }
    });
    const MAX_PAGES_PER_ENGINE = 60;

    // ── Tema paleti: düğüm/bağlantı/etiket renkleri tek kaynaktan ───────
    /** Düğüm türü → renk. Açıklama (legend) ve çizim aynı haritayı kullanır. */
    G.palette = function () {
        const p = KH.palette();
        const v = (n) => KH.cssVar(n);
        G.colors = {
            root: p.violet || p.series[1],
            query: p.series[0],
            engine: p.series[2],
            result: v('--text-3') || p.fg2,
            multi: p.series[3],
            internal: p.series[4],
            external: p.series[5],
            text: p.fg,
            text2: p.fg2,
            halo: v('--bg'),
            surface: p.surface,
            primary: p.primary,
            font: p.font,
            mono: p.mono
        };
        return G.colors;
    };
    G.palette();

    /** Bir düğümün görsel türü: sonuç düğümü birden çok motorda bulunduysa "multi". */
    G.kind = function (data) {
        const t = data.type || 'result';
        if ((t === 'result' || t === 'internal' || t === 'external') && Number(data.count) > 1) return 'multi';
        if (t === 'link') return 'internal';
        if (t === 'internal-group') return 'internal';
        if (t === 'external-group') return 'external';
        return G.colors[t] ? t : 'result';
    };
    G.colorOf = function (data) { return G.colors[G.kind(data)] || G.colors.result; };

    // ── Sorgu listesi ───────────────────────────────────────────────────
    G.loadQueries = async function () {
        const select = $('query-select');
        try {
            const data = await KH.api('/api/queries');
            G.state.queries = Array.isArray(data && data.queries) ? data.queries : [];
        } catch (e) {
            G.state.queries = [];
            KH.toast(T('Sorgu listesi alınamadı: ', 'Could not load queries: ') + e.message, 'err');
        }
        select.textContent = '';
        const all = document.createElement('option');
        all.value = ''; all.textContent = T('Tüm sorgular', 'All queries');
        select.appendChild(all);
        G.state.queries.forEach(q => {
            const o = document.createElement('option');
            o.value = q.query;
            o.textContent = q.query + ' (' + KH.num(q.count) + ')';
            select.appendChild(o);
        });
        // URL'deki sorgu listede yoksa da seçilebilsin
        if (G.state.query && !G.state.queries.some(q => q.query === G.state.query)) {
            const o = document.createElement('option');
            o.value = G.state.query; o.textContent = G.state.query;
            select.appendChild(o);
        }
        select.value = G.state.query || '';
        return G.state.queries;
    };

    G.selectQuery = function (query, push) {
        G.state.query = (query || '').trim();
        if (push !== false) {
            const url = new URL(window.location.href);
            if (G.state.query) url.searchParams.set('q', G.state.query); else url.searchParams.delete('q');
            window.history.pushState({ q: G.state.query }, '', url);
        }
        const sel = $('query-select');
        if (sel && sel.value !== G.state.query) sel.value = G.state.query;
        G.state.open.clear();
        G.state.defaultDepth = 2;
        G.state.userZoomed = false;
        return G.initGraph(G.state.query);
    };

    // ── Kademeli yükleme: sorgular → motorlar → sonuçlar ───────────────
    G.initGraph = async function (queryParam) {
        const query = (queryParam || '').trim();
        const profile = query ? PROFILES.focused : PROFILES.overview;
        const session = beginLoad(query, profile);
        const root = { name: 'KeywordHunter', type: 'root', children: [] };
        G.state.data = root;

        try {
            const summaries = await resolveQueries(query, profile, session.signal);
            ensureCurrent(session.id);
            if (!summaries.length) {
                ['queries', 'engines', 'results'].forEach(s => markStep(s, 'done'));
                setMeta(T('Veri bulunamadı.', 'No data found.'));
                G.showEmpty(true);
                finishLoad(session.id);
                return;
            }
            root.children = summaries.map(s => ({ name: s.query, type: 'query', count: s.count, children: [] }));
            markStep('queries', 'done');
            markStep('engines', 'active');
            countLoaded();
            setMeta(T('Sorgular hazır: ', 'Queries ready: ') + G.state.load.counts.queries);
            scheduleRender(session.id, true);

            await hydrate(root, profile, session);
            ensureCurrent(session.id);

            markStep('engines', 'done');
            markStep('results', 'done');
            countLoaded();
            setMeta(summaryText());
            scheduleRender(session.id, true, true);
            finishLoad(session.id);
        } catch (error) {
            if (isAbort(error)) return;
            const partial = root.children && root.children.length > 0;
            if (partial) scheduleRender(session.id, true, true); else G.showEmpty(true);
            const step = activeStep();
            if (step) markStep(step, 'error');
            const msg = (error && error.message) || T('Yükleme hatası', 'Load error');
            setMeta(T('Yükleme kesildi: ', 'Loading stopped: ') + msg);
            finishLoad(session.id);
            KH.toast(T('Harita yüklenemedi: ', 'Map failed to load: ') + msg, 'err');
        }
    };

    function beginLoad(query, profile) {
        if (G.state.controller) G.state.controller.abort();
        clearTimeout(renderTimer); renderTimer = 0;
        const controller = new AbortController();
        const id = G.state.activeLoadId + 1;
        G.state.activeLoadId = id;
        G.state.controller = controller;
        G.state.load = { inProgress: true, query, profile, counts: { queries: 0, engines: 0, results: 0 } };
        const status = $('graph-load-status');
        if (status) { status.hidden = false; status.classList.add('is-loading'); }
        markStep('queries', 'active'); markStep('engines', 'pending'); markStep('results', 'pending');
        setMeta(T('Sorgu listesi alınıyor…', 'Fetching queries…'));
        G.showEmpty(false);
        return { id, signal: controller.signal };
    }
    function finishLoad(id) {
        if (id !== G.state.activeLoadId) return;
        G.state.load.inProgress = false;
        G.state.controller = null;
        const status = $('graph-load-status');
        if (status) status.classList.remove('is-loading');
    }
    function ensureCurrent(id) { if (id !== G.state.activeLoadId) throw new DOMException('stale graph request', 'AbortError'); }
    function isAbort(e) { return e && (e.name === 'AbortError' || e.message === 'stale graph request'); }

    async function resolveQueries(query, profile, signal) {
        const params = new URLSearchParams({ limit: String(profile.maxQueries) });
        if (query) params.set('q', query);
        const data = await KH.api('/api/graph/queries?' + params, { signal });
        const items = Array.isArray(data && data.queries) ? data.queries : [];
        if (query && !items.length) return [{ query, count: 0 }];
        return items
            .filter(i => i && typeof i.query === 'string' && i.query.trim())
            .map(i => ({ query: i.query.trim(), count: Number(i.count) || 0 }));
    }

    async function hydrate(root, profile, session) {
        let budget = profile.maxEnginesTotal;
        for (let qi = 0; qi < root.children.length; qi++) {
            const qNode = root.children[qi];
            if (budget <= 0) { setMeta(T('Motor bütçesi doldu, kalan sorgular atlandı. ', 'Engine budget reached, remaining queries skipped. ') + summaryText()); break; }
            ensureCurrent(session.id);
            setMeta(T('Motorlar: ', 'Engines: ') + (qi + 1) + '/' + root.children.length + ' · ' + qNode.name);
            const params = new URLSearchParams({ q: qNode.name, limit: String(Math.min(profile.maxEnginesPerQuery, budget)) });
            const payload = await KH.api('/api/graph/engines?' + params, { signal: session.signal });
            ensureCurrent(session.id);
            const engines = Array.isArray(payload && payload.engines) ? payload.engines : [];
            qNode.children = engines
                .filter(e => e && typeof e.engine === 'string' && e.engine.trim())
                .map(e => ({ name: e.engine.trim(), type: 'engine', count: Number(e.count) || 0, children: [] }));
            budget -= qNode.children.length;
            countLoaded();
            scheduleRender(session.id);
            if (!qNode.children.length) continue;
            markStep('results', 'active');
            for (let ei = 0; ei < qNode.children.length; ei++) {
                ensureCurrent(session.id);
                await hydrateEngine(qNode, qNode.children[ei], ei, qNode.children.length, profile, session);
            }
        }
    }

    async function hydrateEngine(qNode, eNode, index, total, profile, session) {
        let offset = 0, loaded = 0, pages = 0;
        while (loaded < profile.maxResultsPerEngine) {
            const limit = Math.min(profile.resultPageSize, profile.maxResultsPerEngine - loaded);
            const params = new URLSearchParams({ q: qNode.name, engine: eNode.name, limit: String(limit), offset: String(offset) });
            setMeta(T('Sonuçlar: ', 'Results: ') + (index + 1) + '/' + total + ' · ' + eNode.name + ' (' + loaded + ')');
            const payload = await KH.api('/api/graph/results?' + params, { signal: session.signal });
            ensureCurrent(session.id);
            const added = appendUnique(eNode, mapResults(payload && payload.results));
            loaded += added; pages++;
            if (added > 0) { countLoaded(); scheduleRender(session.id); }
            const n = Array.isArray(payload && payload.results) ? payload.results.length : 0;
            const next = Number(payload && payload.nextOffset) || 0;
            if (n === 0 || next <= offset || n < limit) break;
            offset = next;
            if (pages >= MAX_PAGES_PER_ENGINE) break;
        }
    }

    function mapResults(items) {
        if (!Array.isArray(items)) return [];
        return items.map(item => {
            const url = String(item.url || '').trim();
            if (!url) return null;
            const title = String(item.title || '').trim();
            return {
                name: title || url, url, type: 'result',
                count: Math.max(1, Number(item.sourceCount) || 1),
                nodeId: Number(item.id) || 0,
                isExpanded: Boolean(item.isExpanded),
                domain: String(item.domain || '').trim() || G.domainOf(url)
            };
        }).filter(Boolean);
    }
    function appendUnique(eNode, items) {
        eNode.children = eNode.children || [];
        const seen = new Set(eNode.children.map(i => i.url));
        let added = 0;
        items.forEach(i => { if (i.url && !seen.has(i.url)) { seen.add(i.url); eNode.children.push(i); added++; } });
        return added;
    }
    G.domainOf = function (url) {
        try { return new URL(url).hostname || ''; } catch (_) {
            const s = String(url).replace(/^https?:\/\//i, ''); const i = s.indexOf('/'); return i > -1 ? s.slice(0, i) : s;
        }
    };

    // ── Çizim zamanlaması (yükleme sırasında seyreltilmiş) ─────────────
    let renderTimer = 0, lastRender = 0;
    function scheduleRender(id, force, final) {
        if (id !== G.state.activeLoadId || !G.state.data) return;
        const wait = (G.state.load.profile && G.state.load.profile.renderDebounceMs) || 260;
        const now = Date.now();
        if (force || now - lastRender >= wait) {
            clearTimeout(renderTimer); renderTimer = 0;
            renderSnapshot(id, final);
            return;
        }
        if (renderTimer) return;
        renderTimer = setTimeout(() => { renderTimer = 0; renderSnapshot(id); }, Math.max(60, wait - (now - lastRender)));
    }
    function renderSnapshot(id, final) {
        if (id !== G.state.activeLoadId) return;
        const data = G.state.data;
        if (!data || !data.children || !data.children.length) { G.showEmpty(true); return; }
        // Odaklı sorguda kök, sorgu düğümünün kendisidir
        G.state.view = (G.state.load.query && data.children.length === 1) ? data.children[0] : data;
        G.updateStats(G.state.view);
        G.render({ fit: final ? 'final' : 'auto' });
        lastRender = Date.now();
    }

    // ── Yükleme durumu ──────────────────────────────────────────────────
    const STEP_IDS = { queries: 'graph-step-queries', engines: 'graph-step-engines', results: 'graph-step-results' };
    function markStep(step, status) {
        const el = $(STEP_IDS[step]);
        if (!el) return;
        el.classList.remove('is-active', 'is-done', 'is-error');
        if (status === 'active' || status === 'done' || status === 'error') el.classList.add('is-' + status);
    }
    function activeStep() { return Object.keys(STEP_IDS).find(s => { const el = $(STEP_IDS[s]); return el && el.classList.contains('is-active'); }) || ''; }
    function setMeta(text) { const el = $('graph-load-meta'); if (el) el.textContent = text || ''; }
    function countLoaded() {
        const c = { queries: 0, engines: 0, results: 0 };
        const root = G.state.data;
        if (root && root.children) {
            c.queries = root.children.length;
            root.children.forEach(q => (q.children || []).forEach(e => { c.engines++; c.results += (e.children || []).length; }));
        }
        G.state.load.counts = c;
    }
    function summaryText() {
        const c = G.state.load.counts;
        return T('Hazır: ', 'Ready: ') + KH.num(c.queries) + T(' sorgu · ', ' queries · ') + KH.num(c.engines) + T(' motor · ', ' engines · ') + KH.num(c.results) + T(' sonuç', ' results');
    }

    // ── İstatistik ──────────────────────────────────────────────────────
    G.updateStats = function (view) {
        let queries = 0, engines = 0, results = 0, multi = 0;
        const walkEngine = (e) => { engines++; (e.children || []).forEach(r => { results++; if (Number(r.count) > 1) multi++; }); };
        if (view && view.type === 'query') { queries = 1; (view.children || []).forEach(walkEngine); }
        else if (view) (view.children || []).forEach(q => { queries++; (q.children || []).forEach(walkEngine); });
        const set = (id, n) => { const el = $(id); if (el && el.textContent !== KH.num(n)) { el.textContent = KH.num(n); el.classList.remove('bump'); void el.offsetWidth; el.classList.add('bump'); } };
        set('stat-queries', queries); set('stat-engines', engines); set('stat-results', results); set('stat-multi', multi);
    };

    G.showEmpty = function (on) {
        const el = $('graph-empty');
        if (el) el.hidden = !on;
        if (on) { G.clearCanvas && G.clearCanvas(); G.updateStats(null); }
    };
})();
