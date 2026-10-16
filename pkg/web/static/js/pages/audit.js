/**
 * Denetim kaydı: filtreli/sayfalı olay tablosu, genişletilebilir ayrıntı,
 * zincir bütünlüğü doğrulama ve filtreye duyarlı dışa aktarma.
 *
 * Olay alanları (hedef, ayrıntı, aktör) saldırgan kontrollü olabilir: satırlar
 * KH.html ile kaçışlanarak kurulur, ayrıntı JSON'u yalnız textContent ile basılır.
 */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    const LIMIT = 50;
    const CATS = ['auth', 'user', 'data', 'system', 'export'];
    const ROLE_BADGE = { admin: 'err', analyst: 'info', viewer: 'violet' };

    const state = { actor: '', category: '', result: '', from: '', to: '', q: '', page: 1 };
    let events = [];
    let seq = 0;

    // ── URL durumu (yenilemede filtreler korunur) ──────────────────────
    function readURL() {
        const p = new URLSearchParams(location.search);
        state.actor = (p.get('actor') || '').slice(0, 128);
        state.category = CATS.includes(p.get('category')) ? p.get('category') : '';
        state.result = ['success', 'failure'].includes(p.get('result')) ? p.get('result') : '';
        state.from = /^\d{4}-\d{2}-\d{2}$/.test(p.get('from') || '') ? p.get('from') : '';
        state.to = /^\d{4}-\d{2}-\d{2}$/.test(p.get('to') || '') ? p.get('to') : '';
        state.q = (p.get('q') || '').slice(0, 200);
        state.page = Math.max(1, parseInt(p.get('page') || '1', 10) || 1);
    }
    function writeURL() {
        const p = new URLSearchParams();
        Object.keys(state).forEach(k => { if (state[k] && !(k === 'page' && state.page === 1)) p.set(k, state[k]); });
        const qs = p.toString();
        history.replaceState(null, '', location.pathname + (qs ? '?' + qs : ''));
    }
    function syncForm() {
        $('a-actor').value = state.actor;
        $('a-category').value = state.category;
        $('a-from').value = state.from;
        $('a-to').value = state.to;
        $('a-q').value = state.q;
        document.querySelectorAll('[data-action="a-result"]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.value === state.result)));
    }
    function readForm() {
        state.actor = $('a-actor').value.trim();
        state.category = $('a-category').value;
        state.from = $('a-from').value;
        state.to = $('a-to').value;
        state.q = $('a-q').value.trim();
    }

    /** Yerel gün sınırlarını UTC RFC3339'a çevirir (bitiş günü dahil). */
    function dayISO(day, end) {
        const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(day || '');
        if (!m) return '';
        const d = new Date(+m[1], +m[2] - 1, +m[3] + (end ? 1 : 0));
        return isNaN(d.getTime()) ? '' : d.toISOString();
    }
    function apiParams() {
        const p = new URLSearchParams();
        if (state.actor) p.set('actor', state.actor);
        if (state.category) p.set('category', state.category);
        if (state.result) p.set('result', state.result);
        if (state.from) p.set('from', dayISO(state.from, false));
        if (state.to) p.set('to', dayISO(state.to, true));
        if (state.q) p.set('q', state.q);
        return p;
    }
    function syncExports() {
        const base = apiParams().toString();
        $('a-export-csv').href = '/api/audit/export?format=csv' + (base ? '&' + base : '');
        $('a-export-json').href = '/api/audit/export?format=json' + (base ? '&' + base : '');
    }
    const filtered = () => !!(state.actor || state.category || state.result || state.from || state.to || state.q);

    // ── Biçimleme ───────────────────────────────────────────────────────
    function initials(name) {
        const s = String(name || '').trim();
        return (s ? s.slice(0, 2) : '?').toLocaleUpperCase(KH.lang === 'en' ? 'en' : 'tr');
    }
    const actionLabel = (code) => KH.t('audit.act.' + code, code);
    const methodLabel = (m) => KH.t('audit.m.' + m, m || '—');
    const catLabel = (c) => KH.t('audit.cat.' + c, c);
    function prettyDetails(raw) {
        if (!raw) return '';
        try { return JSON.stringify(JSON.parse(raw), null, 2); } catch (_) { return String(raw); }
    }

    function actorCell(e) {
        if (e.authMethod === 'system') {
            return KH.html`<div class="a-who"><span class="a-av sys" aria-hidden="true">${KH.raw(KH.icon('settings'))}</span><div class="min-w-0"><div class="a-name">${KH.t('audit.m.system', 'Sistem')}</div></div></div>`;
        }
        if (!e.actor) {
            return KH.html`<div class="a-who"><span class="a-av anon" aria-hidden="true">?</span><div class="min-w-0"><div class="a-name kh-muted">${KH.t('audit.m.anonymous', 'Anonim')}</div></div></div>`;
        }
        const role = e.role || '';
        return KH.html`<div class="a-who">
            <span class="a-av r-${role}" aria-hidden="true">${initials(e.actor)}</span>
            <div class="min-w-0"><div class="a-name kh-truncate" title="${e.actor}">${e.actor}</div>
            ${role ? KH.html`<span class="kh-badge ${ROLE_BADGE[role] || 'neutral'} a-role">${KH.t('role.' + role, role)}</span>` : ''}</div>
        </div>`;
    }

    function row(e) {
        const fail = !e.success;
        const id = 'a-d-' + e.id;
        return KH.html`<tr class="a-row${fail ? ' is-fail' : ''}" data-action="a-toggle" data-id="${e.id}">
            <td class="a-c-exp"><button type="button" class="kh-btn xs icon subtle a-exp" data-action="a-toggle" data-id="${e.id}" aria-expanded="false" aria-controls="${id}" aria-label="${KH.t('audit.c.expand', 'Ayrıntı')}: #${e.id}">${KH.raw(KH.icon('chevron-down'))}</button></td>
            <td class="a-c-time"><span class="a-ago" title="${KH.fmtTime(e.ts, true)}">${KH.ago(e.ts)}</span><span class="a-abs kh-mono">${KH.fmtTime(e.ts)}</span></td>
            <td class="a-c-actor">${actorCell(e)}</td>
            <td class="a-c-action"><div class="a-act">
                <span class="kh-badge plain a-cat-${e.category}" title="${catLabel(e.category)}">${actionLabel(e.action)}</span>
                ${fail ? KH.html`<span class="kh-badge err a-fail">${KH.raw(KH.icon('close'))}${T('başarısız', 'failed')}</span>` : ''}
            </div><code class="a-code">${e.action}</code></td>
            <td class="a-c-target"><span class="a-target kh-truncate" title="${e.target}">${e.target || '—'}</span></td>
            <td class="a-c-ip"><span class="kh-mono a-ip">${e.ip || '—'}</span></td>
            <td class="a-c-method"><span class="a-method m-${e.authMethod}">${methodLabel(e.authMethod)}</span></td>
        </tr>
        <tr class="a-detail" id="${id}" hidden><td colspan="7"><div class="a-detail-in">
            <dl class="a-meta">
                <div><dt>${T('Olay', 'Event')}</dt><dd class="kh-mono">#${e.id}</dd></div>
                <div><dt>${T('Zaman damgası (UTC)', 'Timestamp (UTC)')}</dt><dd class="kh-mono">${e.ts}</dd></div>
                <div class="a-ip-dd"><dt>IP · ${KH.t('audit.c.method', 'Yöntem')}</dt><dd class="kh-mono">${e.ip || '—'} · ${methodLabel(e.authMethod)}</dd></div>
                <div class="a-hash"><dt>Hash</dt><dd class="kh-mono">${e.hash}</dd></div>
                <div class="a-hash"><dt>${T('Önceki hash', 'Previous hash')}</dt><dd class="kh-mono">${e.prevHash || T('(başlangıç)', '(genesis)')}</dd></div>
            </dl>
            <pre class="a-json" data-id="${e.id}"></pre>
        </div></td></tr>`;
    }

    function renderRows() {
        const body = $('a-body');
        if (!events.length) {
            const f = filtered();
            KH.render(body, KH.html`<tr><td colspan="7"><div class="kh-empty">
                <div class="icon">${KH.raw(KH.icon(f ? 'filter' : 'audit'))}</div>
                <h3>${f ? KH.t('audit.noMatch') : KH.t('audit.empty')}</h3>
                <p>${f ? KH.t('audit.noMatchS') : KH.t('audit.emptyS')}</p>
                ${f ? KH.html`<div class="kh-inline"><button type="button" class="kh-btn subtle" data-action="a-clear">${KH.raw(KH.icon('close'))} ${KH.t('audit.f.clear')}</button></div>` : ''}
            </div></td></tr>`);
            return;
        }
        KH.render(body, KH.html`${events.map(row)}`);
        // Ayrıntı JSON'u güvenilmez metindir: yalnız textContent ile yazılır.
        body.querySelectorAll('pre.a-json').forEach(pre => {
            const e = events.find(x => String(x.id) === pre.dataset.id);
            const txt = e ? prettyDetails(e.details) : '';
            if (txt) pre.textContent = txt;
            else { pre.textContent = T('Ek ayrıntı yok', 'No additional details'); pre.classList.add('empty'); }
        });
    }

    function renderPager(d) {
        const nav = $('a-pager');
        $('a-count').textContent = KH.num(d.total);
        if (!d.total) { nav.hidden = true; $('a-range').textContent = ''; return; }
        const from = (d.page - 1) * d.limit + 1, to = Math.min(d.page * d.limit, d.total);
        $('a-range').textContent = from + '–' + to + ' / ' + KH.num(d.total);
        if (d.pages <= 1) { nav.hidden = true; return; }
        nav.hidden = false;
        KH.render(nav, KH.html`
            <button type="button" class="kh-btn sm subtle" data-action="a-page" data-page="1"${KH.raw(d.page <= 1 ? ' disabled' : '')} aria-label="${T('İlk sayfa', 'First page')}">«</button>
            <button type="button" class="kh-btn sm subtle" data-action="a-page" data-page="${d.page - 1}"${KH.raw(d.page <= 1 ? ' disabled' : '')}>‹ ${T('Önceki', 'Previous')}</button>
            <span class="info">${d.page} / ${d.pages}</span>
            <button type="button" class="kh-btn sm subtle" data-action="a-page" data-page="${d.page + 1}"${KH.raw(d.page >= d.pages ? ' disabled' : '')}>${T('Sonraki', 'Next')} ›</button>
            <button type="button" class="kh-btn sm subtle" data-action="a-page" data-page="${d.pages}"${KH.raw(d.page >= d.pages ? ' disabled' : '')} aria-label="${T('Son sayfa', 'Last page')}">»</button>`);
    }

    function renderChain(ch) {
        const card = $('a-chain');
        if (!ch) return;
        card.classList.remove('green', 'red', 'amber');
        card.classList.add(ch.ok ? 'green' : 'red');
        card.title = KH.t('audit.headHash', 'Head hash') + ': ' + (ch.headHash || '—');
        if (ch.ok) {
            KH.render($('a-chain-v'), KH.html`${KH.raw(KH.icon('check', 'a-chain-ic'))}${KH.t('audit.chainOk')}`);
            KH.render($('a-chain-s'), KH.html`<span class="kh-mono">${KH.num(ch.checked)}</span> ${KH.t('audit.checked')}`);
        } else {
            KH.render($('a-chain-v'), KH.html`${KH.raw(KH.icon('warn', 'a-chain-ic'))}${KH.t('audit.chainBroken')} #${ch.brokenAt}`);
            $('a-chain-s').textContent = KH.t('audit.brokenAt') + ' #' + ch.brokenAt + ' · ' + KH.t('audit.reason.' + ch.reason, ch.reason || '');
        }
    }

    function renderStats(st) {
        if (!st) return;
        $('a-total').textContent = KH.num(st.total);
        $('a-fail').textContent = KH.num(st.failures24h);
        $('a-actors').textContent = KH.num(st.actors24h);
        const fc = $('a-fail-card');
        fc.classList.toggle('red', st.failures24h > 0);
        fc.classList.toggle('cyan', !st.failures24h);
    }

    /** Yazılamayan denetim olayları: sayaç sıfırdan büyükse uyarı görünür. */
    function renderWriteFailures(wf) {
        const box = $('a-wfail');
        if (!box || !wf) return;
        const n = Number(wf.count) || 0;
        box.hidden = n <= 0;
        $('a-wfail-n').textContent = KH.num(n);
        box.title = wf.lastAt ? T('Son hata: ', 'Last failure: ') + KH.fmtTime(wf.lastAt, true) : '';
    }

    function renderActors(list) {
        const dl = $('a-actor-list');
        dl.textContent = '';
        (list || []).forEach(a => { const o = document.createElement('option'); o.value = a; dl.appendChild(o); });
    }

    async function load() {
        const my = ++seq;
        writeURL();
        syncExports();
        const body = $('a-body');
        body.classList.add('is-loading');
        const p = apiParams();
        p.set('page', String(state.page));
        p.set('limit', String(LIMIT));
        try {
            const d = await KH.api('/api/audit?' + p.toString());
            if (my !== seq) return; // eski yanıt
            if (d.page !== state.page) { state.page = d.page; writeURL(); }
            events = d.events || [];
            renderRows();
            renderPager(d);
            renderChain(d.chain);
            renderStats(d.stats);
            renderActors(d.actors);
            renderWriteFailures(d.writeFailures);
        } catch (e) {
            if (my !== seq) return;
            KH.render(body, KH.html`<tr><td colspan="7"><div class="kh-alert err">${KH.raw(KH.icon('warn'))}<span>${e.message}</span>
                <button type="button" class="kh-btn sm subtle" data-action="a-retry">${KH.raw(KH.icon('refresh'))} ${T('Tekrar dene', 'Retry')}</button></div></td></tr>`);
            $('a-pager').hidden = true;
            $('a-count').textContent = '—';
            $('a-range').textContent = '';
        } finally {
            if (my === seq) body.classList.remove('is-loading');
        }
    }
    function refilter() { readForm(); state.page = 1; load(); }

    function toggle(id) {
        const detail = $('a-d-' + id);
        if (!detail) return;
        const open = detail.hidden;
        detail.hidden = !open;
        const tr = detail.previousElementSibling;
        if (tr) {
            tr.classList.toggle('open', open);
            const btn = tr.querySelector('.a-exp');
            if (btn) btn.setAttribute('aria-expanded', String(open));
        }
    }

    KH.act({
        'a-filter': refilter,
        'a-retry': () => load(),
        'a-filter-soon': KH.debounce(refilter, 300),
        'a-result': (btn) => { state.result = btn.dataset.value || ''; syncForm(); readForm(); state.page = 1; load(); },
        'a-clear': () => {
            Object.assign(state, { actor: '', category: '', result: '', from: '', to: '', q: '', page: 1 });
            syncForm();
            load();
        },
        'a-page': (btn) => {
            const p = parseInt(btn.dataset.page, 10);
            if (!p || p === state.page) return;
            state.page = p;
            load().then(() => { const card = document.querySelector('.a-card'); if (card) card.scrollIntoView({ block: 'start', behavior: 'smooth' }); });
        },
        'a-toggle': (el, ev) => {
            // Satırda metin seçiliyorsa (IP/hash kopyalama) açılıp kapanmasın
            if (el.tagName === 'TR' && String(window.getSelection ? window.getSelection() : '').trim()) return;
            if (el.tagName === 'TR' && ev && ev.target.closest('a,button,input,select')) return;
            toggle(el.dataset.id);
        },
        'a-verify': async (btn) => {
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                const d = await KH.api('/api/audit/verify', { method: 'POST', body: {} });
                renderChain(d.chain);
                if (d.chain.ok) KH.toast(T('Zincir doğrulandı: ', 'Chain verified: ') + KH.num(d.chain.checked) + T(' olay bütün', ' events intact'), 'ok');
                else KH.toast(T('Zincir #', 'Chain broken at #') + d.chain.brokenAt + T(' satırında kırık — kayıt kurcalanmış olabilir', ' — the log may have been tampered with'), 'err', 9000);
                load();
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        }
    });

    document.addEventListener('DOMContentLoaded', () => {
        readURL();
        syncForm();
        load();
    });
})();
