/**
 * KeywordHunter — İlişki haritası: etkileşimler (ipucu, bağlam menüsü, ayarlar,
 * hızlı arama, derinleştirme, izleme listesi, detay modalı).
 * Tüm güvenilmez metinler textContent ile basılır; HTML dizgisi yalnızca
 * KH.html (otomatik kaçışlı) ile kurulur.
 */
(function () {
    'use strict';

    const G = window.KHGraph = window.KHGraph || {};
    const T = (tr, en) => KH.tr(tr, en);
    const $ = (id) => document.getElementById(id);
    const S = () => G.state;

    const KIND_LABEL = {
        root: ['Kök', 'Root'], query: ['Sorgu', 'Query'], engine: ['Motor', 'Engine'], result: ['Sonuç', 'Result'],
        multi: ['Çoklu kaynak', 'Multi-source'], internal: ['İç link', 'Internal link'], external: ['Dış link', 'External link']
    };
    G.kindLabel = (k) => { const l = KIND_LABEL[k] || KIND_LABEL.result; return T(l[0], l[1]); };
    const isOnion = (url) => { try { return /\.onion$/i.test(new URL(url).hostname); } catch (_) { return /\.onion(\/|$|:)/i.test(String(url || '')); } };

    // ── Açıklama (legend) + istatistik noktaları: çizimle aynı palet ────
    G.paintLegend = function () {
        document.querySelectorAll('#gx [data-type] .sw').forEach(sw => {
            const type = sw.closest('[data-type]').dataset.type;
            sw.style.setProperty('--c', G.colors[type] || G.colors.result);
        });
    };

    // ── İpucu ───────────────────────────────────────────────────────────
    const tip = () => $('graph-tooltip');
    function tipContent(d) {
        const kids = (d.children || d._children || []).length;
        const url = d.data.url || '';
        return KH.html`
            <div class="gx-tip-kind"><i class="sw"></i>${G.kindLabel(d.depth === 0 && d.data.type === 'root' ? 'root' : d.kind)}${d.data.isExpanded ? KH.html` · <span class="ok">${T('derinleştirildi', 'expanded')}</span>` : ''}</div>
            <div class="gx-tip-title">${d.data.name || '—'}</div>
            ${url ? KH.html`<div class="gx-tip-url">${url}</div>` : ''}
            <div class="gx-tip-meta">
              ${url && isOnion(url) ? KH.html`<span>${KH.raw(KH.icon('onion', 'sm'))} ${T('Tor Browser gerektirir', 'Requires Tor Browser')}</span>` : ''}
              ${Number(d.data.count) > 1 && (d.kind === 'multi') ? KH.html`<span class="multi">${KH.raw(KH.icon('fire', 'sm'))} ${d.data.count} ${T('motorda bulundu', 'engines matched')}</span>` : ''}
              ${(d.kind === 'query' || d.kind === 'engine') && d.data.count ? KH.html`<span>${KH.raw(KH.icon('results', 'sm'))} ${KH.num(d.data.count)} ${T('sonuç', 'results')}</span>` : ''}
              ${kids ? KH.html`<span>${KH.raw(KH.icon('folder', 'sm'))} ${kids} ${T('alt öğe', 'children')} · ${d.children ? T('kapatmak için tıkla', 'click to collapse') : T('açmak için tıkla', 'click to expand')}</span>` : ''}
            </div>`;
    }
    function placeTip(e) {
        const el = tip(); if (!el || el.hidden) return;
        const pad = 14, w = el.offsetWidth, h = el.offsetHeight;
        let x = e.clientX + pad, y = e.clientY + pad;
        if (x + w > window.innerWidth - 8) x = e.clientX - w - pad;
        if (y + h > window.innerHeight - 8) y = e.clientY - h - pad;
        el.style.transform = 'translate(' + Math.max(8, x) + 'px,' + Math.max(8, y) + 'px)';
    }
    G.onNodeEnter = function (e, d) {
        const m = $('graph-ctx');
        if (m && m.classList.contains('open')) return; // menü açıkken ipucu/vurgu yok
        G.highlight(d);
        if (e.pointerType === 'touch' || (e.sourceCapabilities && e.sourceCapabilities.firesTouchEvents)) return;
        const el = tip(); if (!el) return;
        KH.render(el, tipContent(d));
        el.querySelector('.sw').style.setProperty('--c', G.colors[d.kind] || G.colors.result);
        el.hidden = false;
        placeTip(e);
    };
    G.onNodeMove = function (e) { placeTip(e); };
    G.onNodeLeave = function () { G.highlight(null); const el = tip(); if (el) el.hidden = true; };
    G.hideTip = G.onNodeLeave;

    // ── Tıklama: aç/kapat; yaprakta (fare) linki kopyala, dokunmada menü ─
    G.toggle = function (d) {
        if (!d.children && !d._children) return false;
        const opening = !!d._children;
        S().open.set(d.id, opening);
        if (opening) (d._children || []).forEach(c => S().fresh.add(c.id));
        G.render({ anchor: d.id });
        return true;
    };
    G.onNodeClick = function (e, d) {
        if (e.defaultPrevented) return; // sürükleme
        G.hideTip();
        if (G.toggle(d)) return;
        // Dokunmatikte menü: aynı tıklamanın belge düzeyindeki "menüleri kapat" dinleyicisinden sonra aç
        if (e.pointerType === 'touch' || e.pointerType === 'pen') { const x = e.clientX, y = e.clientY; setTimeout(() => G.openMenu(d, x, y), 0); return; }
        if (d.data.url) KH.copy(d.data.url, T('Link kopyalandı', 'Link copied'));
    };
    G.onNodeMenu = function (e, d) {
        e.preventDefault();
        G.hideTip();
        G.openMenu(d, e.clientX, e.clientY);
    };
    G.onNodeKey = function (e, d) {
        if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); if (!G.toggle(d)) { const p = G.screenPos(d); G.openMenu(d, p.x, p.y); } }
        else if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) { e.preventDefault(); const p = G.screenPos(d); G.openMenu(d, p.x, p.y); }
    };

    // ── Bağlam menüsü ───────────────────────────────────────────────────
    const menu = () => $('graph-ctx');
    G.openMenu = function (d, x, y) {
        const m = menu(); if (!m) return;
        S().selected = d;
        const url = d.data.url || '';
        const onion = url && isOnion(url);
        $('graph-ctx-head').textContent = G.truncate(d.data.name || url || '—', 48);
        m.querySelectorAll('[data-need="url"]').forEach(el => { el.hidden = !url; });
        m.querySelectorAll('[data-need="onion"]').forEach(el => { el.hidden = !onion; });
        const ex = $('expand-menu-item');
        if (ex && url) {
            const label = ex.querySelector('span');
            ex.classList.remove('is-muted');
            if (d.data.isExpanded) { label.textContent = T('Derinleştirildi', 'Already expanded'); ex.classList.add('is-muted'); }
            else if (!onion) { label.textContent = T('Derinleştir (yalnız .onion)', 'Expand (.onion only)'); ex.classList.add('is-muted'); }
            else label.textContent = T('Derinleştir', 'Expand');
        }
        // Önce görünür yap, sonra ölç ve ekrana sığdır
        m.style.left = '0px'; m.style.top = '0px';
        m.classList.add('open');
        const w = m.offsetWidth, h = m.offsetHeight;
        const left = Math.min(Math.max(8, x + 2), window.innerWidth - w - 8);
        const top = Math.min(Math.max(8, y + 2), window.innerHeight - h - 8);
        m.style.left = left + 'px'; m.style.top = top + 'px';
        m.style.transformOrigin = (x > left + 1 ? 'right' : 'left') + ' ' + (y > top + 1 ? 'bottom' : 'top');
        setTimeout(() => { const first = m.querySelector('.kh-menu-item:not([hidden])'); if (first) first.focus({ preventScroll: true }); }, 20);
    };
    G.closeMenu = function () { const m = menu(); if (m) m.classList.remove('open'); };

    function selected(needUrl) {
        const d = S().selected;
        if (!d || !d.data) return null;
        if (needUrl && !d.data.url) { KH.toast(T('Bu düğümün linki yok', 'This node has no link'), 'warn'); return null; }
        return d;
    }
    G.copyLink = function () { G.closeMenu(); const d = selected(true); if (d) KH.copy(d.data.url, T('Link kopyalandı', 'Link copied')); };
    G.copyTitle = function () { G.closeMenu(); const d = selected(false); if (d && d.data.name) KH.copy(d.data.name, T('Başlık kopyalandı', 'Title copied')); };
    G.focusSelected = function () { G.closeMenu(); const d = selected(false); if (d) G.focusOn(d); };

    // ── Derinleştir (/api/expand) ───────────────────────────────────────
    let expanding = false;
    G.expandSelected = async function () {
        G.closeMenu();
        const d = selected(true); if (!d) return;
        if (expanding) { KH.toast(T('Derinleştirme sürüyor…', 'Expansion in progress…'), 'info'); return; }
        if (!isOnion(d.data.url)) { KH.toast(T('Derinleştirme yalnızca .onion adreslerinde çalışır', 'Expansion only works on .onion addresses'), 'warn'); return; }
        if (d.data.isExpanded && d.data.children && d.data.children.length) { KH.toast(T('Bu düğüm zaten derinleştirilmiş', 'This node is already expanded'), 'info'); return; }
        expanding = true;
        const busy = $('expand-loading'); if (busy) busy.hidden = false;
        const data = d.data;
        try {
            const res = await KH.api('/api/expand', { method: 'POST', body: { url: data.url, parentId: data.nodeId || 0, query: S().query || '' } });
            if (!res || !res.success) throw new Error((res && res.error) || T('Derinleştirme başarısız', 'Expansion failed'));
            data.isExpanded = true;
            const gid = Number(res.graphNodeId) || 0;
            let kids = [];
            if (gid > 0) {
                const cd = await KH.api('/api/graph/children/' + encodeURIComponent(gid));
                kids = (cd && cd.success && Array.isArray(cd.children)) ? cd.children : [];
            }
            if (kids.length) {
                data.children = kids;
                // d.id yeni çizimde değişmez (yol kimliği) → açık işaretle + yeni bağlantıları canlandır
                S().open.set(d.id, true);
                const prefix = d.id + '>';
                kids.forEach(k => S().fresh.add(prefix + (k.type || '') + ':' + (k.url || k.name || '')));
                G.updateStats(S().view);
                G.render({ anchor: d.id });
                KH.toast(T('Derinleştirildi: ', 'Expanded: ') + KH.num(res.savedLinks || kids.length) + T(' bağlantı', ' links'), 'ok');
            } else {
                G.render({ anchor: d.id, fit: 'none' });
                KH.toast(T('Sayfa tarandı ancak yeni bağlantı bulunamadı', 'Page crawled but no new links were found'), 'info');
            }
        } catch (e) {
            KH.toast(T('Derinleştirilemedi: ', 'Could not expand: ') + e.message, 'err');
        } finally {
            expanding = false;
            if (busy) busy.hidden = true;
        }
    };

    // ── İzleme listesine ekle ───────────────────────────────────────────
    G.watchSelected = async function () {
        G.closeMenu();
        const d = selected(true); if (!d) return;
        let host = '';
        try { host = new URL(d.data.url).hostname; } catch (_) { KH.toast(T('Geçersiz URL', 'Invalid URL'), 'warn'); return; }
        if (!/\.onion$/i.test(host)) { KH.toast(T('Yalnızca .onion siteleri izlenebilir', 'Only .onion sites can be watched'), 'warn'); return; }
        try {
            const r = await KH.api('/api/watchlist', { method: 'POST', body: { name: String(d.data.name || host).slice(0, 100), url: 'http://' + host, category: 'Harita', notes: T('Haritadan eklendi', 'Added from the map') } });
            if (r && r.success === false) throw new Error(r.error || T('Eklenemedi', 'Could not add'));
            KH.toast(T('İzleme listesine eklendi: ', 'Added to watchlist: ') + host, 'ok');
        } catch (e) { KH.toast(e.message, 'err'); }
    };

    // ── Detay modalı ────────────────────────────────────────────────────
    G.showDetails = function () {
        G.closeMenu();
        const d = selected(false); if (!d) return;
        const x = d.data;
        const kids = (d.children || d._children || []).length;
        const rows = [
            [T('Başlık', 'Title'), x.name || '—', ''],
            [T('Tür', 'Type'), G.kindLabel(d.depth === 0 && x.type === 'root' ? 'root' : d.kind), ''],
            [T('URL', 'URL'), x.url || '—', 'mono break-all c-primary'],
            [T('Alan adı', 'Domain'), x.domain || (x.url ? G.domainOf(x.url) : '—'), 'mono break-all'],
            [T('Düğüm ID', 'Node ID'), x.nodeId ? String(x.nodeId) : '—', 'mono'],
            [T('Kaynak sayısı', 'Source count'), String(x.count || 1), 'mono'],
            [T('Alt öğe', 'Children'), String(kids), 'mono']
        ];
        KH.render($('link-modal-content'), KH.html`${rows.map(r => KH.html`<div><dt>${r[0]}</dt><dd class="${r[2]}">${r[1]}</dd></div>`)}`);
        const findings = $('link-modal-findings');
        findings.hidden = !x.url;
        if (x.url) findings.href = '/results?text=' + encodeURIComponent(x.url);
        $('link-modal-copy').hidden = !x.url;
        KH.openModal('link-modal');
    };
    G.copyFromModal = function () { const d = selected(true); if (d) KH.copy(d.data.url, T('Link kopyalandı', 'Link copied')); };

    // ── Hızlı arama (POST /search, HTML döner; yalnız durum kodu kullanılır) ─
    let searching = false;
    G.quickSearch = async function () {
        const input = $('quick-search'), btn = $('quick-search-btn');
        const q = input.value.replace(/\s+/g, ' ').trim();
        if (!q) { KH.toast(T('Sorgu girin', 'Enter a query'), 'warn'); input.focus(); return; }
        if (q.length > 200) { KH.toast(T('Sorgu 200 karakteri aşamaz', 'Query cannot exceed 200 characters'), 'warn'); return; }
        if (searching) return;
        searching = true;
        KH.busy(btn, true);
        input.disabled = true;
        KH.toast(T('Tor üzerinden aranıyor… (10-90 sn)', 'Searching over Tor… (10-90 s)'), 'info');
        try {
            const res = await fetch('/search', {
                method: 'POST', credentials: 'same-origin',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded', 'Accept': 'text/html' },
                body: 'query=' + encodeURIComponent(q)
            });
            if (res.status === 401 || res.redirected && /\/login/.test(res.url)) { KH.toast(T('Oturum süresi doldu', 'Session expired'), 'warn'); setTimeout(() => { location.href = '/login'; }, 900); return; }
            if (res.status === 403) throw new Error(T('Bu işlem için yetkiniz yok', 'You are not allowed to do this'));
            if (res.status === 429) throw new Error(T('Çok fazla istek; birkaç saniye bekleyin', 'Too many requests; wait a few seconds'));
            if (!res.ok) throw new Error('HTTP ' + res.status);
            input.value = '';
            G.state.query = q;
            await G.loadQueries();
            const known = G.state.queries.find(x => x.query === q);
            KH.toast(known ? T('Arama tamamlandı: ', 'Search finished: ') + KH.num(known.count) + T(' sonuç', ' results') : T('Arama tamamlandı, sonuç bulunamadı', 'Search finished with no results'), known ? 'ok' : 'warn');
            G.selectQuery(q);
        } catch (e) {
            KH.toast(T('Arama başarısız: ', 'Search failed: ') + e.message, 'err');
        } finally {
            searching = false;
            KH.busy(btn, false);
            input.disabled = false;
        }
    };

    // ── Yerleşim ────────────────────────────────────────────────────────
    G.setLayout = function (layout) {
        if (!['radial', 'tree', 'force'].includes(layout)) return;
        S().layout = layout;
        document.querySelectorAll('[data-action="graph-layout"]').forEach(b => {
            const on = b.dataset.layout === layout;
            b.setAttribute('aria-pressed', String(on)); b.classList.toggle('active', on);
        });
        G.saveSettings();
        if (S().view) { S().userZoomed = false; G.render({ fit: 'force' }); }
    };

    // ── Ayarlar ─────────────────────────────────────────────────────────
    G.syncSettingsUI = function () {
        const s = G.settings;
        $('font-size-slider').value = s.fontSize;
        $('spacing-slider').value = s.spacing;
        $('truncate-slider').value = s.truncateLength;
        $('show-all-labels').checked = s.showAllLabels;
        $('font-size-value').textContent = s.fontSize + 'px';
        $('spacing-value').textContent = s.spacing + '×';
        $('truncate-value').textContent = s.truncateLength + T(' kar.', ' ch');
    };
    const rerender = KH.debounce(() => { if (S().view) G.render({ fit: S().userZoomed ? 'none' : 'auto', instant: false }); }, 120);
    G.readSettings = function () {
        G.settings.fontSize = parseInt($('font-size-slider').value, 10) || 14;
        G.settings.spacing = parseFloat($('spacing-slider').value) || 1;
        G.settings.truncateLength = parseInt($('truncate-slider').value, 10) || 25;
        G.settings.showAllLabels = $('show-all-labels').checked;
        G.syncSettingsUI();
        G.saveSettings();
        rerender();
    };
    G.resetSettings = function () {
        Object.assign(G.settings, G.DEFAULT_SETTINGS);
        G.syncSettingsUI();
        G.saveSettings();
        if (S().view) G.render({ fit: 'force' });
        KH.toast(T('Ayarlar sıfırlandı', 'Settings reset'), 'info', 1800);
    };
    G.toggleSettings = function (force) {
        const p = $('settings-panel'), btn = $('settings-toggle');
        const open = typeof force === 'boolean' ? force : p.hidden;
        p.hidden = !open;
        btn.setAttribute('aria-expanded', String(open));
        btn.classList.toggle('is-on', open);
        if (open) setTimeout(() => $('font-size-slider').focus({ preventScroll: true }), 30);
    };
})();
