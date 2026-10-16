/** İlişki haritası — eylem bağlamaları ve açılış (CSP: satır içi betik yok). */
(function () {
    'use strict';
    const G = window.KHGraph;
    const T = KH.tr;
    const $ = (id) => document.getElementById(id);

    KH.act({
        'graph-query': (el) => G.selectQuery(el.value),
        'graph-search': () => G.quickSearch(),
        'graph-layout': (el) => G.setLayout(el.dataset.layout),
        'graph-full': () => {
            if (!G.state.view) { KH.toast(T('Graf verisi yok', 'No graph data'), 'warn'); return; }
            G.state.open.clear(); G.state.defaultDepth = Infinity; G.state.userZoomed = false;
            G.render({ fit: 'force' });
            KH.toast(T('Tam harita görünümü', 'Full map view'), 'ok', 1800);
        },
        'graph-collapse': () => {
            if (!G.state.view) return;
            G.state.open.clear(); G.state.defaultDepth = 1; G.state.userZoomed = false;
            G.render({ fit: 'force' });
        },
        'graph-reset': () => {
            if (!G.state.view) return;
            G.state.open.clear(); G.state.defaultDepth = 2; G.state.userZoomed = false;
            G.render({ fit: 'force' });
        },
        'graph-fit': () => { G.state.userZoomed = false; G.fit(); },
        'graph-zoom-in': () => G.zoomBy(1.5),
        'graph-zoom-out': () => G.zoomBy(1 / 1.5),
        'graph-settings': () => G.toggleSettings(),
        'graph-setting': () => G.readSettings(),
        'graph-settings-reset': () => G.resetSettings(),
        'graph-settings-apply': () => { G.readSettings(); G.toggleSettings(false); KH.toast(T('Ayarlar uygulandı', 'Settings applied'), 'ok', 1600); },
        'graph-legend': (el) => {
            const side = $('graph-side');
            const open = side.classList.toggle('legend-closed') === false;
            el.setAttribute('aria-expanded', String(open));
            try { localStorage.setItem('kh.graph.legend', open ? '1' : '0'); } catch (_) { }
        },
        'graph-help-hide': () => {
            $('graph-help').hidden = true;
            try { localStorage.setItem('kh.graph.help', '0'); } catch (_) { }
            if (!G.state.userZoomed) G.fit();
        },
        'graph-copy-link': () => G.copyLink(),
        'graph-copy-title': () => G.copyTitle(),
        'graph-expand': () => G.expandSelected(),
        'graph-watch': () => G.watchSelected(),
        'graph-focus': () => G.focusSelected(),
        'graph-details': () => G.showDetails(),
        'graph-ctx-close': () => G.closeMenu(),
        'graph-copy-modal': () => G.copyFromModal()
    });

    document.addEventListener('DOMContentLoaded', async () => {
        if (!G || typeof d3 === 'undefined') { KH.toast(T('Harita kütüphanesi yüklenemedi', 'Map library failed to load'), 'err'); return; }
        const root = $('gx');
        G.state.query = (root.dataset.query || '').trim();

        // Kişisel görünüm tercihleri (yalnız bu tarayıcı)
        try {
            if (localStorage.getItem('kh.graph.help') === '0') $('graph-help').hidden = true;
            const leg = localStorage.getItem('kh.graph.legend');
            if (leg === '0' || (leg === null && window.innerWidth < 720)) {
                $('graph-side').classList.add('legend-closed');
                document.querySelector('[data-action="graph-legend"]').setAttribute('aria-expanded', 'false');
            }
        } catch (_) { }

        G.syncSettingsUI();
        G.paintLegend();
        document.querySelectorAll('[data-action="graph-layout"]').forEach(b => {
            const on = b.dataset.layout === G.state.layout;
            b.setAttribute('aria-pressed', String(on)); b.classList.toggle('active', on);
        });

        await G.loadQueries();
        G.initGraph(G.state.query);
    });

    // Tema değişince: palet yeniden okunur, düğüm/bağlantı/etiket yeniden boyanır
    window.addEventListener('kh:theme', () => requestAnimationFrame(() => G.recolor()));

    // Tarayıcı geri/ileri: URL'deki sorguyu yükle
    window.addEventListener('popstate', () => {
        const q = new URL(window.location.href).searchParams.get('q') || '';
        G.selectQuery(q, false);
    });

    // Boyut değişince görünümü yeniden sığdır (kullanıcı yakınlaştırmadıysa)
    window.addEventListener('resize', KH.debounce(() => { if (G.state.root && !G.state.userZoomed) G.fit(300); }, 160));

    // Kaydırma/yakınlaştırmada açık menü ve ipucu kapanır
    G.onZoom = function (e) { if (e.sourceEvent) { G.closeMenu(); G.hideTip(); } };

    // Klavye: + / - yakınlaştır, F sığdır, 1-2-3 yerleşim, Esc menü/ayar kapat
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') { G.closeMenu(); if (!$('settings-panel').hidden) G.toggleSettings(false); return; }
        const tag = (e.target.tagName || '').toLowerCase();
        if (tag === 'input' || tag === 'textarea' || tag === 'select' || e.ctrlKey || e.metaKey || e.altKey) return;
        if (document.querySelector('.kh-modal-overlay.open, .kh-palette-overlay.open')) return;
        const map = { '+': () => G.zoomBy(1.5), '=': () => G.zoomBy(1.5), '-': () => G.zoomBy(1 / 1.5), 'f': () => { G.state.userZoomed = false; G.fit(); }, '1': () => G.setLayout('radial'), '2': () => G.setLayout('tree'), '3': () => G.setLayout('force') };
        const fn = map[e.key];
        if (fn) { e.preventDefault(); fn(); }
    });
})();
