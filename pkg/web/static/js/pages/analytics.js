/**
 * Analiz Merkezi: sorgu odağı, KPI'lar, Chart.js grafikleri (tema duyarlı),
 * domain haritası, IOC özeti ve CSV bağlantısı.
 *
 * Tüm renkler KH.palette()'ten gelir; `kh:theme` olayında grafikler aynı
 * veriden yeniden kurulur. Dark web kaynaklı her metin (sorgu, domain, kaynak)
 * yalnızca KH.html/KH.esc ya da canvas metni olarak basılır.
 */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    const reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    const locale = KH.lang === 'en' ? 'en-GB' : 'tr-TR';
    const TOP = 7; // kategorik renk sayısı (8. renk "Diğer" için nötr kalır)

    const state = {
        data: null,      // /api/analytics (zaman tüneli aralığıyla)
        qdata: null,     // seçili sorgunun zaman serisi (sorgu grafiği aralığıyla)
        queries: [],     // /api/queries (sayıya göre azalan)
        selected: null,  // odaklanılan sorgu
        interval: 'day',
        qInterval: 'day',
        qMode: 'periodic',
        loaded: false,
        seq: 0
    };
    const charts = {};
    const rank = { source: new Map(), query: new Map() }; // varlık → sabit renk sırası

    // ── Yardımcılar ─────────────────────────────────────────────────────
    function alpha(color, a) {
        color = String(color || '').trim();
        let m = color.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/i);
        if (m) {
            let h = m[1];
            if (h.length === 3) h = h.split('').map(c => c + c).join('');
            const n = parseInt(h, 16);
            return 'rgba(' + (n >> 16 & 255) + ',' + (n >> 8 & 255) + ',' + (n & 255) + ',' + a + ')';
        }
        m = color.match(/^rgba?\(([^)]+)\)$/i);
        if (m) { const p = m[1].split(',').map(s => s.trim()); return 'rgba(' + p[0] + ',' + p[1] + ',' + p[2] + ',' + a + ')'; }
        return color;
    }
    const fmtNum = (n) => KH.num(n);
    const pctText = (p) => KH.lang === 'en' ? p + '%' : '%' + p;

    function rememberRanks(map, names) {
        names.forEach((n) => { if (!map.has(n)) map.set(n, map.size); });
    }
    function entityColor(kind, name, P) {
        const i = rank[kind].get(name);
        return i != null && i < TOP ? P.series[i] : alpha(P.fg2, .55);
    }

    /** Zaman kovası etiketi: kısa (eksen) ve uzun (ipucu) biçim. */
    function bucketLabel(raw, interval, long) {
        raw = String(raw || '');
        if (interval === 'week') {
            const m = raw.match(/^(\d{4})-(\d{1,2})$/);
            if (!m) return raw;
            return long ? (KH.lang === 'en' ? 'Week ' + (+m[2]) + ', ' + m[1] : m[1] + ' · ' + (+m[2]) + '. hafta') : (KH.lang === 'en' ? 'W' : 'H') + (+m[2]) + ' ’' + m[1].slice(2);
        }
        const m = raw.match(/^(\d{4})-(\d{2})-(\d{2})(?: (\d{2}):00)?$/);
        if (!m) return raw;
        const d = new Date(+m[1], +m[2] - 1, +m[3], m[4] ? +m[4] : 0);
        try {
            if (interval === 'hour') {
                return long ? d.toLocaleString(locale, { day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit' })
                    : d.toLocaleString(locale, { day: 'numeric', month: 'short' }) + ' ' + m[4] + ':00';
            }
            return long ? d.toLocaleDateString(locale, { weekday: 'short', day: 'numeric', month: 'long', year: 'numeric' })
                : d.toLocaleDateString(locale, { day: 'numeric', month: 'short' });
        } catch (_) { return raw; }
    }

    // ── Grafik durumu (iskelet / boş / hazır) ──────────────────────────
    function setChartState(id, mode, msg) {
        const canvas = document.getElementById(id);
        const box = canvas && canvas.closest('.an-chart');
        if (!box) return;
        const st = box.querySelector('.an-state');
        box.dataset.state = mode;
        if (mode === 'loading') KH.render(st, KH.html`<div class="kh-skel block"></div>`);
        else if (mode === 'empty') KH.render(st, KH.html`<div class="an-empty">${KH.raw(KH.icon('empty'))}<span>${msg || T('Henüz veri yok', 'No data yet')}</span></div>`);
        else KH.render(st, '');
        const legend = document.getElementById(id + '-legend');
        if (legend && mode !== 'ready') legend.innerHTML = '';
    }
    function destroy(id) { if (charts[id]) { charts[id].destroy(); delete charts[id]; } }

    // ── Chart.js ortak ayarları (paletten) ──────────────────────────────
    function applyDefaults(P) {
        Chart.defaults.font.family = P.font;
        Chart.defaults.font.size = 12;
        Chart.defaults.color = P.text;
        Chart.defaults.borderColor = P.grid;
    }
    function tooltip(P, extra) {
        return Object.assign({
            backgroundColor: alpha(P.surface, .96), borderColor: P.border, borderWidth: 1,
            titleColor: P.fg, bodyColor: P.fg2, footerColor: P.text,
            titleFont: { family: P.font, weight: '700', size: 12.5 }, bodyFont: { family: P.font, size: 12 },
            footerFont: { family: P.font, size: 11, weight: '500' },
            padding: 12, cornerRadius: 10, boxPadding: 5, usePointStyle: true, caretSize: 6
        }, extra || {});
    }
    function anim(animate, extra) {
        if (!animate || reduced) return false;
        return Object.assign({ duration: 750, easing: 'easeOutQuart' }, extra || {});
    }
    const stagger = (ctx) => (ctx.type === 'data' && ctx.mode === 'default' ? ctx.dataIndex * 35 : 0);
    function axis(P, extra) {
        return Object.assign({
            border: { display: false },
            grid: { color: P.grid, drawTicks: false },
            ticks: { color: P.text, padding: 8, font: { family: P.font, size: 11 } }
        }, extra || {});
    }
    /** Dikey gradyan: renk üstte yoğun, tabanda saydam (alan/çubuk dolguları için). */
    function vGrad(color, top, bottom) {
        return (c) => {
            const { ctx, chartArea } = c.chart;
            if (!chartArea) return alpha(color, top);
            const g = ctx.createLinearGradient(0, chartArea.top, 0, chartArea.bottom);
            g.addColorStop(0, alpha(color, top)); g.addColorStop(1, alpha(color, bottom));
            return g;
        };
    }
    function hGrad(color, left, right) {
        return (c) => {
            const { ctx, chartArea } = c.chart;
            if (!chartArea) return alpha(color, right);
            const g = ctx.createLinearGradient(chartArea.left, 0, chartArea.right, 0);
            g.addColorStop(0, alpha(color, left)); g.addColorStop(1, alpha(color, right));
            return g;
        };
    }
    const dl = () => (window.ChartDataLabels ? [window.ChartDataLabels] : []);

    /** Halka grafiğin ortasına görünür toplam. */
    const centerTotal = {
        id: 'khCenter',
        afterDraw(chart, _a, opts) {
            const meta = chart.getDatasetMeta(0);
            if (!meta || !meta.data || !meta.data.length) return;
            const ds = chart.data.datasets[0].data;
            let sum = 0;
            ds.forEach((v, i) => { if (chart.getDataVisibility(i)) sum += v || 0; });
            const { x, y } = meta.data[0];
            const ctx = chart.ctx;
            ctx.save();
            ctx.textAlign = 'center'; ctx.textBaseline = 'middle';
            ctx.fillStyle = opts.color; ctx.font = '800 22px ' + opts.mono;
            ctx.fillText(fmtNum(sum), x, y - 7);
            ctx.fillStyle = opts.sub; ctx.font = '700 10px ' + opts.font;
            ctx.fillText(opts.label.toLocaleUpperCase(locale), x, y + 14);
            ctx.restore();
        }
    };

    // ── Veri ────────────────────────────────────────────────────────────
    function norm(d) {
        d = d || {};
        ['sources', 'timeline', 'domains', 'criticality', 'queries', 'categories'].forEach(k => { d[k] = d[k] || []; });
        d.keywordStats = d.keywordStats || {};
        return d;
    }
    async function loadQueries() {
        try {
            const d = await KH.api('/api/queries');
            state.queries = (d.queries || []).filter(q => q && q.query);
        } catch (e) { state.queries = []; }
        rememberRanks(rank.query, state.queries.map(q => q.query));
    }
    function analyticsURL(interval, query) {
        return '/api/analytics?interval=' + encodeURIComponent(interval) + (query ? '&query=' + encodeURIComponent(query) : '');
    }
    async function loadData(opts) {
        opts = opts || {};
        const my = ++state.seq;
        markBusy(true);
        try {
            const tasks = [KH.api(analyticsURL(state.interval, state.selected))];
            if (opts.queries) tasks.push(loadQueries());
            const [d] = await Promise.all(tasks);
            if (my !== state.seq) return;
            state.data = norm(d);
            if (!state.selected) rememberRanks(rank.source, state.data.sources.map(s => s.source));
            if (state.selected) {
                state.qdata = state.qInterval === state.interval ? state.data : norm(await KH.api(analyticsURL(state.qInterval, state.selected)));
                if (my !== state.seq) return;
            } else state.qdata = null;
            state.loaded = true;
            renderChips();
            renderStats();
            renderCharts(true);
            renderDomains();
        } catch (e) {
            if (my !== state.seq) return;
            KH.toast(T('Analiz verisi alınamadı: ', 'Analytics failed to load: ') + e.message, 'err');
            ['queryBarChart', 'timelineChart', 'queryPieChart', 'sourcePieChart', 'criticalityChart', 'categoryChart'].forEach(id => { destroy(id); setChartState(id, 'empty', T('Veri alınamadı', 'Could not load data')); });
        } finally {
            if (my === state.seq) markBusy(false);
        }
    }
    async function loadQueryChart() {
        if (!state.selected) return;
        const my = ++state.seq;
        markBusy(true, ['queryBarChart']);
        try {
            state.qdata = state.qInterval === state.interval ? state.data : norm(await KH.api(analyticsURL(state.qInterval, state.selected)));
            if (my !== state.seq) return;
            renderQueryChart(true);
        } catch (e) { KH.toast(e.message, 'err'); }
        finally { if (my === state.seq) markBusy(false); }
    }
    function markBusy(on, ids) {
        const btn = $('refresh-btn');
        if (btn) btn.classList.toggle('is-busy', on);
        document.querySelectorAll('.an-chart').forEach(b => {
            const c = b.querySelector('canvas');
            if (!on || !ids || ids.includes(c.id)) b.classList.toggle('busy', on && state.loaded);
        });
    }

    // ── Sorgu çipleri ───────────────────────────────────────────────────
    function renderChips() {
        const box = $('filter-chips');
        const qs = state.queries;
        $('clear-filter').disabled = !state.selected;
        const exp = $('export-link');
        if (exp) exp.href = '/api/export/results?format=csv' + (state.selected ? '&q=' + encodeURIComponent(state.selected) : '');
        if (!qs.length) {
            KH.render(box, KH.html`<span class="kh-hint">${T('Henüz sorgu verisi yok.', 'No query data yet.')} <a href="/search" data-write>${T('Arama yapın', 'Run a search')} →</a></span>`);
            return;
        }
        $('chip-search-wrap').hidden = qs.length <= 12;
        KH.render(box, KH.html`${qs.map(q => KH.html`<button type="button" class="kh-chip${q.query === state.selected ? ' active' : ''}" data-action="an-select" data-query="${q.query}" aria-pressed="${String(q.query === state.selected)}" title="${q.query}"><span class="q">${q.query}</span><span class="count">${fmtNum(q.count)}</span></button>`)}`);
        filterChips();
    }
    function filterChips() {
        const inp = $('chip-search');
        const f = (inp && inp.value || '').trim().toLocaleLowerCase(locale);
        document.querySelectorAll('#filter-chips .kh-chip').forEach(ch => {
            ch.hidden = !!f && !ch.dataset.query.toLocaleLowerCase(locale).includes(f) && ch.dataset.query !== state.selected;
        });
    }
    function select(q) {
        state.selected = q && q !== state.selected ? q : null;
        renderChips();
        loadData();
    }

    // ── KPI ─────────────────────────────────────────────────────────────
    function renderStats() {
        const d = state.data;
        const total = d.totalSites || 0;
        const freq = d.keywordStats.totalHits || 0;
        const withHits = d.keywordStats.withHits || 0, withoutHits = d.keywordStats.withoutHits || 0;
        const records = withHits + withoutHits;
        const rate = KH.pct(withHits, records);
        const high = d.criticality.filter(c => c.level >= 3).reduce((a, c) => a + c.count, 0);
        const sevTotal = d.criticality.reduce((a, c) => a + c.count, 0);
        KH.countUp($('stat-total'), total);
        KH.countUp($('stat-freq'), freq);
        KH.countUp($('stat-rate'), rate, KH.lang === 'en' ? { prefix: '', suffix: '%' } : { prefix: '%', suffix: '' });
        KH.countUp($('stat-high'), high);
        requestAnimationFrame(() => { $('stat-rate-bar').style.width = rate + '%'; });
        $('stat-total-sub').textContent = state.selected ? '“' + KH.short(state.selected, 32) + '” ' + T('sorgusu', 'query') : fmtNum(state.queries.length) + ' ' + T('sorgu', 'queries');
        $('stat-high-sub').textContent = T('Sev 3+ · kayıtların ', 'Sev 3+ · ') + pctText(KH.pct(high, sevTotal)) + T('', ' of records');
    }

    // ── Grafikler ───────────────────────────────────────────────────────
    function renderCharts(animate) {
        if (!state.data || !window.Chart) return;
        applyDefaults(KH.palette());
        renderQueryChart(animate);
        renderTimeline(animate);
        renderCriticality(animate);
        renderSources(animate);
        renderQueryPie(animate);
        renderCategories(animate);
    }

    function renderQueryChart(animate) {
        const id = 'queryBarChart';
        const P = KH.palette();
        destroy(id);
        const ctrl = $('query-chart-controls'), stats = $('query-stats');
        const title = $('query-chart-title'), hint = $('query-chart-hint');
        if (state.selected) {
            title.textContent = '“' + KH.short(state.selected, 48) + '”';
            hint.textContent = T('zaman içinde bulgu', 'findings over time');
            ctrl.hidden = false;
            document.querySelectorAll('[data-action="an-qinterval"]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.interval === state.qInterval)));
            document.querySelectorAll('[data-action="an-qmode"]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.mode === state.qMode)));
            const tl = (state.qdata || {}).timeline || [];
            if (!tl.length) { stats.hidden = true; setChartState(id, 'empty', T('Bu sorgu için zaman verisi yok', 'No time data for this query')); return; }
            const buckets = [...new Set(tl.map(t => t.date))].sort();
            const per = buckets.map(b => tl.filter(t => t.date === b).reduce((a, t) => a + (t.count || 0), 0));
            let run = 0;
            const cum = per.map(v => (run += v));
            const peakI = per.indexOf(Math.max.apply(null, per));
            stats.hidden = false;
            KH.render(stats, KH.html`
                <div><span>${T('Toplam', 'Total')}</span><b>${fmtNum(run)}</b></div>
                <div><span>${T('Dönem', 'Periods')}</span><b>${fmtNum(buckets.length)}</b></div>
                <div><span>${T('Zirve', 'Peak')}</span><b>${fmtNum(per[peakI])}</b><small>${bucketLabel(buckets[peakI], state.qInterval)}</small></div>
                <div><span>${T('Son dönem', 'Latest')}</span><b>+${fmtNum(per[per.length - 1])}</b><small>${bucketLabel(buckets[buckets.length - 1], state.qInterval)}</small></div>`);
            const cumulative = state.qMode === 'cumulative';
            if (cumulative && buckets.length === 1) { buckets.unshift(''); per.unshift(0); cum.unshift(0); }
            const labels = buckets.map(b => b ? bucketLabel(b, state.qInterval) : T('Başlangıç', 'Start'));
            const color = cumulative ? P.series[2] : P.series[1];
            const few = buckets.length <= 2;
            charts[id] = new Chart($(id), {
                type: cumulative ? 'line' : 'bar',
                data: {
                    labels,
                    datasets: [cumulative ? {
                        label: T('Kümülatif toplam', 'Cumulative total'), data: cum,
                        borderColor: color, backgroundColor: vGrad(color, .34, .02), fill: 'origin', tension: .38, borderWidth: 2,
                        pointRadius: few ? 4 : 0, pointHoverRadius: 6, pointBackgroundColor: color, pointBorderColor: P.surface, pointBorderWidth: 2
                    } : {
                        label: T('Dönemsel yeni bulgu', 'New findings per period'), data: per,
                        backgroundColor: vGrad(color, .95, .45), hoverBackgroundColor: color,
                        borderRadius: 6, borderSkipped: 'start', maxBarThickness: 46, categoryPercentage: .78, barPercentage: .9
                    }]
                },
                options: {
                    responsive: true, maintainAspectRatio: false,
                    interaction: { mode: 'index', intersect: false },
                    animation: anim(animate, cumulative ? {} : { delay: stagger }),
                    plugins: {
                        legend: { display: false },
                        datalabels: { display: false },
                        tooltip: tooltip(P, {
                            callbacks: {
                                title: (it) => buckets[it[0].dataIndex] ? bucketLabel(buckets[it[0].dataIndex], state.qInterval, true) : T('Başlangıç', 'Start'),
                                label: (c) => ' ' + T('Bu dönem: +', 'This period: +') + fmtNum(per[c.dataIndex]),
                                afterLabel: (c) => ' ' + T('Birikim: ', 'Running total: ') + fmtNum(cum[c.dataIndex]),
                                footer: (it) => { const i = it[0].dataIndex; return cum[i] ? T('Birikimin ', 'Share of total: ') + pctText(KH.pct(per[i], cum[i])) + T('’i bu dönemde', '') : ''; }
                            }
                        })
                    },
                    scales: {
                        x: axis(P, { grid: { display: false }, ticks: { color: P.text, maxRotation: 0, autoSkipPadding: 14, font: { size: 11 } } }),
                        y: axis(P, { beginAtZero: true, ticks: { color: P.text, precision: 0, padding: 8, font: { size: 11 } } })
                    }
                }
            });
            setChartState(id, 'ready');
            return;
        }
        // Genel görünüm: sorgu başına sonuç (ilk 12)
        title.textContent = KH.t('an.queryChart', T('Sorgu Bazlı Sonuçlar', 'Results by Query'));
        hint.textContent = T('ilk 12 · odaklanmak için tıklayın', 'top 12 · click to focus');
        ctrl.hidden = true; stats.hidden = true;
        const data = state.queries.slice(0, 12);
        if (!data.length) { setChartState(id, 'empty', T('Sorgu verisi yok', 'No query data')); return; }
        const color = P.series[0];
        charts[id] = new Chart($(id), {
            type: 'bar',
            plugins: dl(),
            data: {
                labels: data.map(q => KH.short(q.query, 26)),
                datasets: [{
                    label: T('Sonuç', 'Results'), data: data.map(q => q.count),
                    backgroundColor: hGrad(color, .5, .95), hoverBackgroundColor: color,
                    borderRadius: 6, borderSkipped: 'start', maxBarThickness: 22, categoryPercentage: .82, barPercentage: .9
                }]
            },
            options: {
                indexAxis: 'y', responsive: true, maintainAspectRatio: false,
                layout: { padding: { right: 44 } },
                animation: anim(animate, { delay: stagger }),
                onClick: (_e, els) => { if (els.length) select(data[els[0].index].query); },
                onHover: (e, els) => { e.native.target.style.cursor = els.length ? 'pointer' : 'default'; },
                plugins: {
                    legend: { display: false },
                    tooltip: tooltip(P, { callbacks: { title: (it) => data[it[0].dataIndex].query, label: (c) => ' ' + fmtNum(c.parsed.x) + ' ' + T('sonuç', 'results'), footer: () => T('Tıkla → bu sorguya odaklan', 'Click → focus this query') } }),
                    datalabels: { anchor: 'end', align: 'end', offset: 6, clamp: true, color: P.fg2, font: { family: P.mono, weight: '700', size: 11 }, formatter: (v) => fmtNum(v) }
                },
                scales: {
                    x: axis(P, { beginAtZero: true, ticks: { color: P.text, precision: 0, padding: 6, font: { size: 11 } } }),
                    y: axis(P, { grid: { display: false }, ticks: { color: P.fg2, padding: 8, font: { size: 11.5, weight: '600' } } })
                }
            }
        });
        setChartState(id, 'ready');
    }

    function renderTimeline(animate) {
        const id = 'timelineChart';
        const P = KH.palette();
        destroy(id);
        const tl = state.data.timeline;
        if (!tl.length) { setChartState(id, 'empty', T('Zaman serisi verisi yok', 'No time series data')); return; }
        const buckets = [...new Set(tl.map(t => t.date))].sort();
        const totals = new Map();
        tl.forEach(t => totals.set(t.source, (totals.get(t.source) || 0) + t.count));
        const bySize = [...totals.keys()].sort((a, b) => totals.get(b) - totals.get(a));
        const shown = bySize.slice(0, TOP), rest = bySize.slice(TOP);
        const series = (names) => buckets.map(b => tl.filter(t => t.date === b && names.includes(t.source)).reduce((a, t) => a + t.count, 0));
        const few = buckets.length <= 2;
        // 1-2 dönem: alan grafiği anlamsızdır → yığılmış sütun; daha fazlası: yumuşak yığılmış alan
        const mk = (label, data, color, i) => (few ? {
            label, data, backgroundColor: alpha(color, .88), hoverBackgroundColor: color, borderColor: P.surface,
            borderWidth: { top: 2 }, borderSkipped: 'start', borderRadius: 3, maxBarThickness: 72
        } : {
            label, data, borderColor: color, backgroundColor: vGrad(color, .30, .03), fill: i === 0 ? 'origin' : '-1',
            tension: .38, borderWidth: 2, pointRadius: 0, pointHoverRadius: 5,
            pointBackgroundColor: color, pointBorderColor: P.surface, pointBorderWidth: 2
        });
        const datasets = shown.map((s, i) => mk(s, series([s]), entityColor('source', s, P), i));
        if (rest.length) datasets.push(mk(T('Diğer', 'Other') + ' (' + rest.length + ')', series(rest), alpha(P.fg2, .6), datasets.length));
        charts[id] = new Chart($(id), {
            type: few ? 'bar' : 'line',
            data: { labels: buckets.map(b => bucketLabel(b, state.interval)), datasets },
            options: {
                responsive: true, maintainAspectRatio: false,
                interaction: { mode: 'index', intersect: false },
                animation: anim(animate, { duration: 900 }),
                plugins: {
                    datalabels: { display: false },
                    legend: { position: 'top', align: 'end', labels: { color: P.fg2, usePointStyle: true, pointStyle: 'rectRounded', boxWidth: 10, boxHeight: 10, padding: 14, font: { family: P.font, size: 11.5, weight: '600' } } },
                    tooltip: tooltip(P, {
                        itemSort: (a, b) => b.parsed.y - a.parsed.y,
                        filter: (it) => it.parsed.y > 0,
                        callbacks: {
                            title: (it) => bucketLabel(buckets[it[0].dataIndex], state.interval, true),
                            label: (c) => ' ' + c.dataset.label + ': ' + fmtNum(c.parsed.y),
                            footer: (it) => T('Toplam: ', 'Total: ') + fmtNum(it.reduce((a, x) => a + x.parsed.y, 0))
                        }
                    })
                },
                scales: {
                    x: axis(P, { stacked: few, grid: { display: false }, ticks: { color: P.text, maxRotation: 0, autoSkipPadding: 16, font: { size: 11 } } }),
                    y: axis(P, { stacked: true, beginAtZero: true, ticks: { color: P.text, precision: 0, padding: 8, font: { size: 11 } } })
                }
            }
        });
        setChartState(id, 'ready');
    }

    /** Halka grafik + HTML lejant (lejant aynı zamanda erişilebilir tablo görünümüdür). */
    function donut(id, items, opts, animate) {
        const P = KH.palette();
        destroy(id);
        if (!items.length) { setChartState(id, 'empty', opts.empty); return; }
        const total = items.reduce((a, x) => a + x.value, 0);
        charts[id] = new Chart($(id), {
            type: 'doughnut',
            plugins: [centerTotal],
            data: {
                labels: items.map(x => x.label),
                datasets: [{ data: items.map(x => x.value), backgroundColor: items.map(x => x.color), hoverBackgroundColor: items.map(x => x.color), borderColor: P.surface, borderWidth: 2, hoverBorderColor: P.surface, borderRadius: 4, spacing: 1, hoverOffset: 6 }]
            },
            options: {
                responsive: true, maintainAspectRatio: false, cutout: '72%',
                layout: { padding: 6 },
                animation: anim(animate, { animateRotate: true, duration: 900 }),
                onClick: (_e, els) => { if (els.length && opts.onClick) opts.onClick(items[els[0].index]); },
                onHover: (e, els) => { e.native.target.style.cursor = els.length && opts.onClick ? 'pointer' : 'default'; },
                plugins: {
                    legend: { display: false },
                    datalabels: { display: false },
                    khCenter: { label: opts.center, color: P.fg, sub: P.text, mono: P.mono, font: P.font },
                    tooltip: tooltip(P, { callbacks: { title: (it) => items[it[0].dataIndex].full || items[it[0].dataIndex].label, label: (c) => ' ' + fmtNum(c.parsed) + ' · ' + pctText(KH.pct(c.parsed, total)) } })
                }
            }
        });
        setChartState(id, 'ready');
        const legend = $(id + '-legend');
        KH.render(legend, KH.html`${items.map((x, i) => KH.html`<li><button type="button" data-action="an-legend" data-chart="${id}" data-index="${i}" aria-pressed="true" title="${x.full || x.label}"><span class="sw" data-color="${x.color}"></span><span class="nm">${x.label}</span><span class="v">${fmtNum(x.value)}</span><span class="p">${pctText(KH.pct(x.value, total))}</span></button></li>`)}`);
        legend.querySelectorAll('.sw').forEach(s => { s.style.background = s.dataset.color; });
    }
    function foldTop(rows, kind, P) {
        const sorted = rows.slice().sort((a, b) => b.value - a.value);
        const head = sorted.slice(0, TOP).map(r => Object.assign(r, { color: entityColor(kind, r.key, P) }));
        const tail = sorted.slice(TOP);
        if (tail.length) head.push({ key: null, label: T('Diğer', 'Other') + ' (' + tail.length + ')', value: tail.reduce((a, r) => a + r.value, 0), color: alpha(P.fg2, .45) });
        return head;
    }
    function renderCriticality(animate) {
        const P = KH.palette();
        const names = { 1: T('Bilgi', 'Info'), 2: T('Düşük', 'Low'), 3: T('Orta', 'Medium'), 4: T('Yüksek', 'High'), 5: T('Acil', 'Critical') };
        const items = state.data.criticality.filter(c => c.count > 0).sort((a, b) => b.level - a.level).map(c => {
            const lv = Math.min(5, Math.max(1, c.level || 1));
            return { key: lv, label: 'SEV ' + lv + ' · ' + names[lv], value: c.count, color: P.sev[lv - 1] };
        });
        donut('criticalityChart', items, { center: T('kayıt', 'records'), empty: T('Kritiklik verisi yok', 'No severity data'), onClick: (x) => { location.href = '/results?minCriticality=' + x.key + (state.selected ? '&q=' + encodeURIComponent(state.selected) : ''); } }, animate);
    }
    function renderSources(animate) {
        const P = KH.palette();
        const rows = state.data.sources.map(s => ({ key: s.source, label: s.source, value: s.count }));
        donut('sourcePieChart', foldTop(rows, 'source', P), { center: T('sonuç', 'results'), empty: T('Kaynak verisi yok', 'No source data'), onClick: (x) => { if (x.key) location.href = '/results?source=' + encodeURIComponent(x.key) + (state.selected ? '&q=' + encodeURIComponent(state.selected) : ''); } }, animate);
    }
    function renderQueryPie(animate) {
        const P = KH.palette();
        const rows = state.queries.map(q => ({ key: q.query, label: KH.short(q.query, 22), full: q.query, value: q.count }));
        donut('queryPieChart', foldTop(rows, 'query', P), { center: T('sonuç', 'results'), empty: T('Sorgu verisi yok', 'No query data'), onClick: (x) => { if (x.key) select(x.key); } }, animate);
    }
    function renderCategories(animate) {
        const id = 'categoryChart';
        const P = KH.palette();
        destroy(id);
        const cats = state.data.categories.filter(c => c.category);
        if (!cats.length) { setChartState(id, 'empty', T('Kategori verisi yok', 'No category data')); return; }
        const color = P.violet;
        charts[id] = new Chart($(id), {
            type: 'bar',
            plugins: dl(),
            data: { labels: cats.map(c => KH.short(c.category, 18)), datasets: [{ label: T('Kayıt', 'Records'), data: cats.map(c => c.count), backgroundColor: hGrad(color, .45, .92), hoverBackgroundColor: color, borderRadius: 5, borderSkipped: 'start', maxBarThickness: 16, categoryPercentage: .8, barPercentage: .9 }] },
            options: {
                indexAxis: 'y', responsive: true, maintainAspectRatio: false,
                layout: { padding: { right: 36 } },
                animation: anim(animate, { delay: stagger }),
                onClick: (_e, els) => { if (els.length) location.href = '/results?category=' + encodeURIComponent(cats[els[0].index].category) + (state.selected ? '&q=' + encodeURIComponent(state.selected) : ''); },
                onHover: (e, els) => { e.native.target.style.cursor = els.length ? 'pointer' : 'default'; },
                plugins: {
                    legend: { display: false },
                    tooltip: tooltip(P, { callbacks: { title: (it) => cats[it[0].dataIndex].category, label: (c) => ' ' + fmtNum(c.parsed.x) + ' ' + T('kayıt', 'records') } }),
                    datalabels: { anchor: 'end', align: 'end', offset: 4, clamp: true, color: P.fg2, font: { family: P.mono, weight: '700', size: 10.5 }, formatter: (v) => fmtNum(v) }
                },
                scales: {
                    x: axis(P, { beginAtZero: true, ticks: { color: P.text, precision: 0, maxTicksLimit: 5, font: { size: 10.5 } } }),
                    y: axis(P, { grid: { display: false }, ticks: { color: P.fg2, padding: 6, font: { size: 11, weight: '600' } } })
                }
            }
        });
        setChartState(id, 'ready');
    }

    // ── Domain haritası ─────────────────────────────────────────────────
    function renderDomains() {
        const box = $('domain-list'), countEl = $('domain-count');
        const domains = (state.data && state.data.domains) || [];
        const f = ($('domain-filter').value || '').trim().toLowerCase();
        const list = domains.filter(d => !f || String(d.domain).toLowerCase().includes(f)).sort((a, b) => b.count - a.count);
        countEl.textContent = fmtNum(list.length) + (f ? ' / ' + fmtNum(domains.length) : '') + ' ' + T('domain', 'domains');
        if (!list.length) {
            KH.render(box, KH.html`<div class="kh-empty p-4"><div class="icon">${KH.raw(KH.icon('map'))}</div><p>${domains.length ? T('Filtreyle eşleşen domain yok.', 'No domains match the filter.') : T('Henüz domain verisi yok.', 'No domain data yet.')}</p></div>`);
            return;
        }
        const max = list[0].count || 1;
        KH.render(box, KH.html`<ol class="an-domain-list">${list.slice(0, 60).map((d, i) => KH.html`<li>
            <span class="rk">${i + 1}</span>
            <div class="body">
                <div class="top"><a class="nm" href="${'/results?text=' + encodeURIComponent(d.domain)}" title="${d.domain}">${d.domain}</a><button type="button" class="kh-copy" data-action="copy" data-copy="${d.domain}" aria-label="${T('Kopyala', 'Copy')}">${KH.raw(KH.icon('copy', 'sm'))}</button></div>
                <div class="bar"><div data-w="${Math.max(3, Math.round(d.count / max * 100))}"></div></div>
            </div>
            <span class="v">${fmtNum(d.count)}</span>
        </li>`)}</ol>${list.length > 60 ? KH.html`<p class="kh-hint mt-3">${T('İlk 60 gösteriliyor; daraltmak için filtreleyin.', 'Showing the top 60; filter to narrow down.')}</p>` : ''}`);
        requestAnimationFrame(() => box.querySelectorAll('[data-w]').forEach(b => { b.style.width = b.dataset.w + '%'; }));
    }

    // ── IOC özeti ───────────────────────────────────────────────────────
    const iocNames = { email: ['mail', 'E-posta', 'Email'], bitcoin: ['coin', 'Bitcoin', 'Bitcoin'], monero: ['coin', 'Monero', 'Monero'], ip: ['globe', 'IP', 'IP'], onion: ['onion', 'Onion', 'Onion'], credit_card: ['card', 'Kart', 'Card'], phone: ['phone', 'Telefon', 'Phone'], ssh_key: ['key', 'SSH', 'SSH'], api_key: ['key', 'API anahtarı', 'API key'], username: ['user', 'Kullanıcı', 'Username'], hash: ['hash', 'Hash', 'Hash'], btc: ['coin', 'Bitcoin', 'Bitcoin'], xmr: ['coin', 'Monero', 'Monero'], eth: ['coin', 'Ethereum', 'Ethereum'] };
    function loadIOC() {
        const box = $('ioc-grid');
        KH.api('/api/artifacts/stats').then(d => {
            const types = (d.types || []).slice().sort((a, b) => b.count - a.count);
            if (!types.length) {
                KH.render(box, KH.html`<div class="kh-empty p-4"><div class="icon">${KH.raw(KH.icon('dna'))}</div><h3>${T('Henüz IOC yok', 'No IOCs yet')}</h3><p>${T('Bulgular sayfasında etiketlemeyi çalıştırdığınızda e-posta, cüzdan, IP, hash ve onion adresleri burada toplanır.', 'Run tagging on Findings to collect emails, wallets, IPs, hashes and onion addresses here.')}</p></div>`);
                return;
            }
            const total = d.total || types.reduce((a, t) => a + t.count, 0);
            KH.render(box, KH.html`<div class="an-ioc-grid kh-stagger">${types.map(t => {
                const n = iocNames[t.type] || ['info', String(t.type).toUpperCase(), String(t.type).toUpperCase()];
                return KH.html`<a class="an-ioc-tile" href="/results" title="${t.type}"><span class="ic">${KH.raw(KH.icon(n[0]))}</span><span class="n" data-countup>${fmtNum(t.count)}</span><span class="l">${KH.lang === 'en' ? n[2] : n[1]}</span><div class="kh-progress thin"><div data-w="${KH.pct(t.count, total)}"></div></div></a>`;
            })}</div><p class="kh-hint mt-3">${T('Toplam', 'Total')} <b>${fmtNum(total)}</b> artifact</p>`);
            KH.enhance(box);
            requestAnimationFrame(() => box.querySelectorAll('[data-w]').forEach(b => { b.style.width = b.dataset.w + '%'; }));
        }).catch(() => KH.render(box, KH.html`<p class="kh-hint">${T('IOC özeti alınamadı.', 'Could not load the IOC summary.')}</p>`));
    }

    // ── Eylemler ────────────────────────────────────────────────────────
    KH.act({
        'an-select': (el) => select(el.dataset.query),
        'an-clear': () => { if (state.selected) select(null); },
        'an-chip-filter': () => filterChips(),
        'an-refresh': () => { loadData({ queries: true }); loadIOC(); },
        'an-interval': (el) => {
            if (el.dataset.interval === state.interval) return;
            state.interval = el.dataset.interval;
            document.querySelectorAll('[data-action="an-interval"]').forEach(b => b.setAttribute('aria-pressed', String(b === el)));
            loadData();
        },
        'an-qinterval': (el) => {
            if (el.dataset.interval === state.qInterval) return;
            state.qInterval = el.dataset.interval;
            document.querySelectorAll('[data-action="an-qinterval"]').forEach(b => b.setAttribute('aria-pressed', String(b === el)));
            loadQueryChart();
        },
        'an-qmode': (el) => {
            if (el.dataset.mode === state.qMode) return;
            state.qMode = el.dataset.mode;
            renderQueryChart(true);
        },
        'an-domains': KH.debounce(renderDomains, 120),
        'an-legend': (el) => {
            const ch = charts[el.dataset.chart];
            if (!ch) return;
            const i = Number(el.dataset.index);
            ch.toggleDataVisibility(i);
            ch.update();
            el.setAttribute('aria-pressed', String(ch.getDataVisibility(i)));
        }
    });

    window.addEventListener('kh:theme', () => { if (state.loaded) requestAnimationFrame(() => renderCharts(false)); });

    document.addEventListener('DOMContentLoaded', () => {
        if (!window.Chart) { KH.toast(T('Grafik kütüphanesi yüklenemedi', 'Chart library failed to load'), 'err'); return; }
        loadData({ queries: true });
        loadIOC();
    });
})();
