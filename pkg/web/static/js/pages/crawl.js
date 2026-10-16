/**
 * Örümcek: yeni tarama modali (istemci doğrulamalı), iş geçmişi, çalışan işler
 * için ilerleme yoklaması ve iptal. Tohum URL / sorgu / mesaj gibi dış kaynaklı
 * her metin yalnızca KH.html ile (kaçışlanarak) basılır.
 */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    const POLL_MS = 3000;
    const ONION_RE = /^https?:\/\/(?:[a-z0-9-]+\.)*(?:[a-z2-7]{56}|[a-z2-7]{16})\.onion(?::\d{1,5})?(?:[/?#].*)?$/i;
    const state = { jobs: [], prev: new Map(), timer: 0, tried: false };

    const badge = { pending: 'neutral', running: 'info', completed: 'ok', failed: 'err', cancelled: 'warn' };
    const icons = { pending: 'clock', completed: 'check', failed: 'warn', cancelled: 'close' };
    const label = () => ({ pending: T('kuyrukta', 'queued'), running: T('çalışıyor', 'running'), completed: T('tamamlandı', 'completed'), failed: T('hata', 'failed'), cancelled: T('iptal edildi', 'cancelled') });
    const isLive = (j) => j.status === 'running' || j.status === 'pending';

    function duration(a, b) {
        if (!a) return '';
        const s = Math.max(0, Math.round(((b ? new Date(b) : new Date()) - new Date(a)) / 1000));
        if (!isFinite(s)) return '';
        if (s < 60) return s + T(' sn', ' s');
        const m = Math.floor(s / 60);
        if (m < 60) return m + T(' dk ', ' min ') + (s % 60) + T(' sn', ' s');
        return Math.floor(m / 60) + T(' sa ', ' h ') + (m % 60) + T(' dk', ' min');
    }

    // ── Tablo ───────────────────────────────────────────────────────────
    function row(j) {
        const L = label();
        const pct = j.maxPages ? Math.min(100, Math.round((j.pagesDone || 0) / j.maxPages * 100)) : 0;
        const from = state.prev.has(j.id) ? state.prev.get(j.id) : 0;
        const seed = String(j.seedUrl || '');
        const safeHref = /^https?:\/\//i.test(seed) ? seed : '';
        const shown = KH.short(seed.replace(/^https?:\/\//i, ''), 34);
        const live = isLive(j);
        const dur = duration(j.startedAt, j.finishedAt);
        const st = badge[j.status] ? j.status : 'pending';
        return KH.html`<tr data-id="${j.id}" class="${live ? 'is-live' : ''}">
            <td data-label="${KH.t('crawl.colStatus')}"><span class="kh-badge ${badge[st]}">${j.status === 'running' ? KH.html`<span class="kh-dot pulse crawl-dot"></span>` : KH.raw(KH.icon(icons[st] || 'info', 'sm'))} ${L[j.status] || j.status}</span></td>
            <td class="crawl-c-seed" data-label="${KH.t('crawl.colSeed')}"><div class="crawl-seed">${safeHref ? KH.html`<a class="link" href="${safeHref}" target="_blank" rel="noopener noreferrer" title="${seed}">${shown}</a>` : KH.html`<span class="kh-mono" title="${seed}">${shown}</span>`}<button type="button" class="kh-copy" data-action="copy" data-copy="${seed}" data-copy-label="${T('Adres kopyalandı', 'Address copied')}" aria-label="${T('Adresi kopyala', 'Copy address')}">${KH.raw(KH.icon('copy', 'sm'))}</button></div></td>
            <td data-label="${KH.t('crawl.colQuery')}"><span class="kh-badge neutral mono crawl-q" title="${j.query}">${KH.short(j.query, 28)}</span></td>
            <td class="nowrap" data-label="${KH.t('crawl.colDepth')}"><span class="crawl-depth" title="${T('Derinlik ', 'Depth ') + j.depth}">${[1, 2, 3].map(n => KH.html`<i class="${n <= j.depth ? 'on' : ''}"></i>`)}</span><span class="crawl-scope kh-muted text-xs">${j.sameDomain ? T('aynı site', 'same site') : T('dış onion', 'external')}</span></td>
            <td class="crawl-c-prog" data-label="${KH.t('crawl.colProg')}">
                <div class="crawl-prog">
                    <div class="kh-progress${j.status === 'running' ? ' active' : ''}${j.status === 'failed' ? ' is-err' : j.status === 'cancelled' ? ' is-warn' : ''}"><div data-from="${from}" data-to="${pct}" style="width:${from}%"></div></div>
                    <span class="kh-mono text-xs"><b>${KH.num(j.pagesDone || 0)}</b>/${KH.num(j.maxPages || 0)} · ${pct}%</span>
                </div>
            </td>
            <td class="num" data-label="${KH.t('crawl.colFound')}"><b class="${j.savedCount ? 'c-ok' : 'kh-muted'}">${KH.num(j.savedCount || 0)}</b><div class="kh-hint">${KH.num(j.foundCount || 0)} ${T('sayfa', 'pages')}</div></td>
            <td class="crawl-c-time" data-label="${KH.t('crawl.colTime')}"><span class="nowrap" title="${KH.fmtTime(j.createdAt, true)}">${KH.ago(j.createdAt)}</span>${dur ? KH.html`<div class="kh-hint nowrap">${KH.raw(KH.icon('clock', 'sm'))} ${dur}</div>` : ''}${j.message ? KH.html`<div class="kh-hint crawl-msg ${j.status === 'failed' ? 'c-err' : ''}" title="${j.message}">${KH.short(j.message, 48)}</div>` : ''}</td>
            <td class="right nowrap crawl-c-act">${live
                ? KH.html`<button type="button" class="kh-btn xs danger" data-action="crawl-cancel" data-id="${j.id}" data-seed="${shown}" data-write>${KH.raw(KH.icon('close', 'sm'))} ${T('İptal', 'Cancel')}</button>`
                : j.savedCount ? KH.html`<span class="kh-rowactions"><a class="kh-btn xs subtle" href="${'/results?q=' + encodeURIComponent(j.query)}">${KH.raw(KH.icon('results', 'sm'))} ${T('Bulgular', 'Findings')}</a><a class="kh-btn xs subtle icon" href="${'/results/graph?q=' + encodeURIComponent(j.query)}" aria-label="${T('Haritada aç', 'Open in map')}" title="${T('Haritada aç', 'Open in map')}">${KH.raw(KH.icon('graph', 'sm'))}</a></span>` : ''}</td>
        </tr>`;
    }

    function render() {
        const body = $('crawl-body');
        const jobs = state.jobs;
        $('crawl-count').textContent = String(jobs.length);
        const live = jobs.filter(isLive).length;
        const rb = $('crawl-running');
        rb.hidden = !live;
        rb.textContent = live + ' ' + T('aktif', 'active');
        if (!jobs.length) {
            KH.render(body, KH.html`<tr><td colspan="8"><div class="kh-empty"><div class="icon">${KH.raw(KH.icon('crawl'))}</div><h3>${T('Henüz tarama yok', 'No crawls yet')}</h3><p>${T('Bir tohum .onion adresi verin; örümcek bağlantıları izleyerek siteyi haritalasın.', 'Give it a seed .onion address and the spider maps the site by following links.')}</p><div class="kh-inline"><button type="button" class="kh-btn primary" data-action="crawl-new" data-write>${KH.raw(KH.icon('plus'))} ${KH.t('crawl.new')}</button></div></div></td></tr>`);
            return;
        }
        KH.render(body, KH.html`${jobs.map(row)}`);
        requestAnimationFrame(() => requestAnimationFrame(() => body.querySelectorAll('[data-to]').forEach(b => { b.style.width = b.dataset.to + '%'; })));
        jobs.forEach(j => state.prev.set(j.id, j.maxPages ? Math.min(100, Math.round((j.pagesDone || 0) / j.maxPages * 100)) : 0));
    }

    async function loadJobs() {
        clearTimeout(state.timer);
        const btn = $('crawl-refresh');
        btn.classList.add('is-busy');
        try {
            const d = await KH.api('/api/crawl');
            const before = new Map(state.jobs.map(j => [j.id, j.status]));
            state.jobs = d.jobs || [];
            state.jobs.forEach(j => {
                const was = before.get(j.id);
                if (was && isLive({ status: was }) && !isLive(j)) {
                    if (j.status === 'completed') KH.toast(T('Tarama tamamlandı: ', 'Crawl finished: ') + KH.num(j.savedCount || 0) + T(' bulgu kaydedildi', ' findings saved'), 'ok');
                    else if (j.status === 'failed') KH.toast(T('Tarama hatayla bitti', 'Crawl failed') + (j.message ? ': ' + j.message : ''), 'err');
                }
            });
            render();
        } catch (e) {
            if (!state.jobs.length) KH.render($('crawl-body'), KH.html`<tr><td colspan="8"><div class="kh-alert err">${KH.raw(KH.icon('warn'))}<span>${e.message}</span></div></td></tr>`);
            else KH.toast(e.message, 'err');
        } finally {
            btn.classList.remove('is-busy');
            schedule();
        }
    }
    function schedule() {
        clearTimeout(state.timer);
        if (state.jobs.some(isLive)) state.timer = setTimeout(() => { if (document.hidden) schedule(); else loadJobs(); }, POLL_MS);
    }

    // ── Yeni tarama formu ───────────────────────────────────────────────
    function fieldError(input, errEl, msg) {
        input.setAttribute('aria-invalid', msg ? 'true' : 'false');
        errEl.hidden = !msg;
        errEl.textContent = msg || '';
        return !msg;
    }
    function normalizeSeed(v) {
        v = v.trim();
        if (v && !/^[a-z]+:\/\//i.test(v) && /\.onion(?:[:/?#]|$)/i.test(v)) v = 'http://' + v;
        return v;
    }
    function validate(focus) {
        const seed = $('c-seed'), pages = $('c-pages');
        const sv = normalizeSeed(seed.value);
        let seedMsg = '';
        if (!sv) seedMsg = T('Tohum adresi gerekli.', 'A seed address is required.');
        else if (!/^https?:\/\//i.test(sv)) seedMsg = T('Adres http:// ya da https:// ile başlamalı.', 'The address must start with http:// or https://.');
        else if (!ONION_RE.test(sv)) seedMsg = T('Geçerli bir .onion adresi değil (v3: 56 karakter, a–z ve 2–7).', 'Not a valid .onion address (v3: 56 characters, a–z and 2–7).');
        const pv = Number(pages.value);
        const pagesMsg = !Number.isInteger(pv) || pv < 1 || pv > 300 ? T('Sayfa sayısı 1 ile 300 arasında bir tam sayı olmalı.', 'Max pages must be a whole number between 1 and 300.') : '';
        const ok1 = fieldError(seed, $('c-seed-err'), seedMsg);
        const ok2 = fieldError(pages, $('c-pages-err'), pagesMsg);
        if (focus) { if (!ok1) seed.focus(); else if (!ok2) pages.focus(); }
        return ok1 && ok2 ? { seedUrl: sv, maxPages: pv } : null;
    }
    function openNew() {
        const f = $('crawl-form');
        f.reset();
        state.tried = false;
        ['c-seed', 'c-pages'].forEach(id => $(id).removeAttribute('aria-invalid'));
        ['c-seed-err', 'c-pages-err'].forEach(id => { $(id).hidden = true; });
        KH.openModal('new-crawl');
    }
    async function submit() {
        state.tried = true;
        const v = validate(true);
        if (!v) return;
        const btn = $('c-submit');
        KH.busy(btn, true, T('Başlatılıyor…', 'Starting…'));
        try {
            await KH.api('/api/crawl', {
                method: 'POST', body: {
                    seedUrl: v.seedUrl,
                    depth: parseInt($('c-depth').value, 10),
                    maxPages: v.maxPages,
                    sameDomain: $('c-domain').value === 'true',
                    query: $('c-query').value.trim()
                }
            });
            KH.toast(T('Tarama başlatıldı', 'Crawl started'), 'ok');
            KH.closeModal('new-crawl');
            loadJobs();
        } catch (e) {
            if (e.status === 400 && /onion|URL/i.test(e.message)) fieldError($('c-seed'), $('c-seed-err'), e.message);
            KH.toast(e.message, e.status === 503 ? 'warn' : 'err');
        } finally { KH.busy(btn, false); }
    }

    async function cancel(el) {
        const ok = await KH.confirm({ title: T('Tarama iptal edilsin mi?', 'Cancel this crawl?'), message: el.dataset.seed + T(' — o ana kadar bulunan sayfalar korunur.', ' — pages found so far are kept.'), okText: T('İptal et', 'Cancel crawl'), cancelText: T('Vazgeç', 'Keep running'), danger: true });
        if (!ok) return;
        KH.busy(el, true);
        try {
            await KH.api('/api/crawl/' + encodeURIComponent(el.dataset.id) + '/cancel', { method: 'POST', body: {} });
            KH.toast(T('Tarama iptal edildi', 'Crawl cancelled'), 'warn');
        } catch (e) { KH.toast(e.message, 'err'); KH.busy(el, false); }
        loadJobs();
    }

    KH.act({
        'crawl-new': openNew,
        'crawl-reload': () => loadJobs(),
        'crawl-submit': submit,
        'crawl-cancel': (el) => cancel(el),
        'crawl-validate': () => { if (state.tried) validate(false); }
    });

    document.addEventListener('DOMContentLoaded', () => {
        loadJobs();
        document.addEventListener('visibilitychange', () => { if (!document.hidden && state.jobs.some(isLive)) loadJobs(); });
        if (location.hash === '#new' && document.body.dataset.role !== 'viewer') openNew();
    });
})();
