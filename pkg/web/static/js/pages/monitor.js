/**
 * Arama Motorları: canlı sağlık tablosu, aç/kapat anahtarı, "şimdi kontrol et",
 * Tor NEWNYM (yalnız admin), filtre/arama ve görünürken 30 sn'de bir yenileme.
 * Motor adları/URL'leri ve notlar yalnızca KH.html ile (kaçışlanarak) basılır.
 */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    const REFRESH_MS = 30000;
    const state = { engines: [], notes: {}, filter: 'all', q: '', lastLoad: 0, checking: false, pending: new Set() };
    const canWrite = () => document.body.dataset.role !== 'viewer';
    const pctText = (p) => KH.lang === 'en' ? p + '%' : '%' + p;
    const secs = (ms) => ms < 1000 ? Math.round(ms) + ' ms' : (ms / 1000).toLocaleString(KH.lang === 'en' ? 'en-GB' : 'tr-TR', { minimumFractionDigits: 1, maximumFractionDigits: 1 }) + T(' sn', ' s');

    // ── Veri ────────────────────────────────────────────────────────────
    async function loadEngines(opts) {
        opts = opts || {};
        try {
            const d = await KH.api('/api/engines');
            state.engines = d.engines || [];
            state.notes = d.notes || {};
            state.lastLoad = Date.now();
            renderSummary(d, opts.animate);
            renderTable();
            stamp(true);
        } catch (e) {
            stamp(false);
            if (!state.engines.length) {
                KH.render($('engines-body'), KH.html`<tr><td colspan="8"><div class="kh-empty"><div class="icon">${KH.raw(KH.icon('offline'))}</div><h3>${T('Motor verisi yüklenemedi', 'Could not load engines')}</h3><p>${e.message}</p><div class="kh-inline"><button type="button" class="kh-btn subtle" data-action="mon-reload">${KH.raw(KH.icon('refresh'))} ${T('Tekrar dene', 'Retry')}</button></div></div></td></tr>`);
            }
        }
    }
    function stamp(ok) {
        const el = $('last-updated');
        const time = new Date().toLocaleTimeString(KH.lang === 'en' ? 'en-GB' : 'tr-TR', { hour: '2-digit', minute: '2-digit', second: '2-digit' });
        KH.render(el, KH.html`<span class="kh-dot ${ok ? 'up' : 'down'}"></span><span>${ok ? T('Güncellendi ', 'Updated ') + time : T('Güncellenemedi', 'Update failed')}</span>`);
        el.title = KH.t('mon.autoRefresh');
        $('live-dot').className = 'kh-dot pulse ' + (ok ? 'up' : 'down');
    }

    function renderSummary(d, animate) {
        const list = state.engines;
        const active = list.filter(e => e.isActive).length;
        const ups = list.filter(e => e.lastStatus === 'up' && e.lastResponseMs > 0);
        const set = (id, n) => { const el = $(id); if (animate) KH.countUp(el, n); else el.textContent = KH.num(n); };
        set('sum-up', d.upCount || 0);
        set('sum-down', d.downCount || 0);
        set('sum-active', active);
        $('sum-active-sub').textContent = T('toplam ', 'of ') + list.length + T(' motordan', ' engines');
        requestAnimationFrame(() => { $('sum-active-bar').style.width = KH.pct(active, list.length) + '%'; });
        $('sum-avg').textContent = ups.length ? secs(ups.reduce((a, e) => a + e.lastResponseMs, 0) / ups.length) : '—';
    }

    // ── Tablo ───────────────────────────────────────────────────────────
    const statusOrder = { up: 0, down: 1, unknown: 2 };
    function visible() {
        const q = state.q.toLowerCase();
        return state.engines
            .filter(e => state.filter === 'all' || (state.filter === 'off' ? !e.isActive : (e.lastStatus || 'unknown') === state.filter))
            .filter(e => !q || String(e.name).toLowerCase().includes(q) || String(e.url).toLowerCase().includes(q))
            .sort((a, b) => (b.isActive - a.isActive) || ((statusOrder[a.lastStatus] ?? 2) - (statusOrder[b.lastStatus] ?? 2)) || String(a.name).localeCompare(String(b.name)));
    }
    function latTone(ms) { return ms <= 0 ? 'none' : ms < 5000 ? 'ok' : ms < 15000 ? 'warn' : 'bad'; }
    function rateTone(r, total) { return !total ? 'none' : r >= 80 ? 'ok' : r >= 50 ? 'warn' : 'bad'; }

    function row(e) {
        const st = e.lastStatus === 'up' || e.lastStatus === 'down' ? e.lastStatus : 'unknown';
        const total = (e.successCount || 0) + (e.failCount || 0);
        const rate = KH.pct(e.successCount || 0, total);
        const ms = e.lastResponseMs || 0;
        const latW = ms > 0 ? Math.max(4, Math.min(100, Math.round(ms / 30000 * 100))) : 0;
        const url = String(e.url || '');
        const shownUrl = url.replace(/^https?:\/\//, '').replace('{query}', '…');
        const note = state.notes[e.name];
        const busy = state.pending.has(e.name);
        const stLabel = { up: T('çevrimiçi', 'online'), down: T('çevrimdışı', 'offline'), unknown: T('bilinmiyor', 'unknown') }[st];
        const stIcon = { up: 'online', down: 'offline', unknown: 'info' }[st];
        const sw = canWrite()
            ? KH.html`<button type="button" class="kh-switch${e.isActive ? ' on' : ''}${busy ? ' busy' : ''}" role="switch" aria-checked="${String(!!e.isActive)}" data-action="mon-toggle" data-name="${e.name}" aria-label="${e.name + ' · ' + KH.t('mon.colActive')}" title="${e.isActive ? T('Aramadan çıkar', 'Remove from search') : T('Aramaya dahil et', 'Include in search')}"></button>`
            : KH.html`<span class="kh-switch${e.isActive ? ' on' : ''}" role="switch" aria-checked="${String(!!e.isActive)}" aria-disabled="true" aria-label="${e.name}"></span>`;
        return KH.html`<tr class="${e.isActive ? '' : 'off'}" data-name="${e.name}">
            <td class="mon-c-engine">
                <div class="mon-engine">
                    <span class="kh-dot ${st}${st === 'up' && e.isActive ? ' pulse' : ''}"></span>
                    <div class="mon-id">
                        <div class="mon-name">${e.name}${e.isActive ? '' : KH.html` <span class="kh-badge neutral">${T('pasif', 'inactive')}</span>`}</div>
                        <div class="mon-url"><span class="u kh-mono" title="${url}">${shownUrl}</span><button type="button" class="kh-copy" data-action="copy" data-copy="${url}" data-copy-label="${T('URL kopyalandı', 'URL copied')}" aria-label="${T('URL’yi kopyala', 'Copy URL')}">${KH.raw(KH.icon('copy', 'sm'))}</button></div>
                        ${note ? KH.html`<div class="mon-note">${KH.raw(KH.icon('info', 'sm'))} ${note}</div>` : ''}
                    </div>
                </div>
            </td>
            <td data-label="${KH.t('mon.colStatus')}"><span class="kh-badge ${st === 'up' ? 'ok' : st === 'down' ? 'err' : 'neutral'}">${KH.raw(KH.icon(stIcon, 'sm'))} ${stLabel}</span></td>
            <td data-label="${KH.t('mon.colLatency')}"><div class="mon-lat t-${latTone(ms)}"><span class="v">${ms > 0 ? secs(ms) : '—'}</span><span class="bar"><i data-w="${latW}"></i></span></div></td>
            <td data-label="${KH.t('mon.colSuccess')}"><div class="mon-rate t-${rateTone(rate, total)}"><span class="v">${total ? pctText(rate) : '—'}</span><span class="bar"><i data-w="${rate}"></i></span><span class="s">${KH.num(e.successCount || 0)}/${KH.num(total)}</span></div></td>
            <td class="num ${e.failCount ? 'c-err' : 'muted'}" data-label="${KH.t('mon.colFails')}">${KH.num(e.failCount || 0)}</td>
            <td class="num" data-label="${KH.t('mon.colStreak')}">${e.consecutiveFails >= 3 ? KH.html`<span class="kh-badge err mono">${e.consecutiveFails}</span>` : e.consecutiveFails ? KH.html`<span class="kh-badge warn mono">${e.consecutiveFails}</span>` : KH.html`<span class="kh-muted">0</span>`}</td>
            <td class="muted nowrap" data-label="${KH.t('mon.colChecked')}" title="${KH.fmtTime(e.lastCheckedAt, true)}">${e.lastCheckedAt ? KH.ago(e.lastCheckedAt) : '—'}</td>
            <td class="center mon-c-switch" data-label="${KH.t('mon.colActive')}">${sw}</td>
        </tr>`;
    }
    function renderTable() {
        const body = $('engines-body');
        const list = visible();
        $('engine-count').textContent = list.length === state.engines.length ? String(state.engines.length) : list.length + ' / ' + state.engines.length;
        if (!list.length) {
            KH.render(body, KH.html`<tr><td colspan="8"><div class="kh-empty"><div class="icon">${KH.raw(KH.icon('monitor'))}</div><h3>${state.engines.length ? T('Eşleşen motor yok', 'No matching engines') : T('Henüz motor kaydı yok', 'No engines recorded yet')}</h3><p>${state.engines.length ? T('Filtreyi ya da aramayı değiştirin.', 'Change the filter or search.') : T('İlk sağlık kontrolü tamamlanınca motorlar burada listelenir.', 'Engines appear here after the first health check.')}</p></div></td></tr>`);
            return;
        }
        KH.render(body, KH.html`${list.map(row)}`);
        requestAnimationFrame(() => body.querySelectorAll('i[data-w]').forEach(i => { i.style.width = i.dataset.w + '%'; }));
    }

    // ── Eylemler ────────────────────────────────────────────────────────
    async function toggle(el) {
        const name = el.dataset.name;
        if (state.pending.has(name)) return;
        const next = el.getAttribute('aria-checked') !== 'true';
        state.pending.add(name);
        el.classList.toggle('on', next); el.setAttribute('aria-checked', String(next)); el.classList.add('busy');
        const eng = state.engines.find(e => e.name === name);
        try {
            await KH.api('/api/engines/' + encodeURIComponent(name) + '/toggle', { method: 'POST', body: { active: next } });
            if (eng) eng.isActive = next;
            KH.toast(name + (next ? T(' aramaya dahil edildi', ' included in searches') : T(' aramadan çıkarıldı', ' removed from searches')), 'ok');
        } catch (e) {
            el.classList.toggle('on', !next); el.setAttribute('aria-checked', String(!next));
            KH.toast(e.message, 'err');
        } finally {
            state.pending.delete(name);
            el.classList.remove('busy');
            loadEngines();
        }
    }

    async function checkNow(btn) {
        if (state.checking) return;
        state.checking = true;
        KH.busy(btn, true, T('Kontrol ediliyor…', 'Checking…'));
        const label = () => btn.querySelector('span:last-child');
        try {
            await KH.api('/api/engines/check-now', { method: 'POST', body: {} });
            KH.toast(T('Kontrol başlatıldı; sonuçlar yaklaşık 30 saniyede gelir', 'Check started; results arrive in about 30 seconds'), 'info');
            const until = Date.now() + 34000;
            const tick = async () => {
                const left = Math.max(0, Math.round((until - Date.now()) / 1000));
                const l = label(); if (l) l.textContent = T('Kontrol ediliyor · ', 'Checking · ') + left + T(' sn', ' s');
                await loadEngines();
                if (left > 0) setTimeout(tick, 4000);
                else { state.checking = false; KH.busy(btn, false); KH.toast(T('Sağlık kontrolü tamamlandı', 'Health check finished'), 'ok'); }
            };
            setTimeout(tick, 4000);
        } catch (e) {
            state.checking = false;
            KH.busy(btn, false);
            KH.toast(e.message, 'err');
        }
    }

    let torInfo = null;
    function loadTor() {
        const btn = $('newnym-btn');
        if (!btn) return;
        KH.api('/api/tor/status').then(d => {
            torInfo = d;
            // Yapılandırılmamış: düğme etkin kalır (aria-disabled DEĞİL — çekirdek
            // dağıtıcı aria-disabled öğelerdeki tıklamayı yutar) ve tıklanınca
            // nedenini uyarı olarak söyler; neden ayrıca aria-describedby ile
            // ekran okuyuculara, title ile fareye iletilir.
            if (!d.configured) {
                const hint = d.hint || T('Tor kontrol portu yapılandırılmamış', 'Tor control port is not configured');
                btn.classList.add('is-off');
                btn.title = hint;
                let sr = $('newnym-hint');
                if (!sr) {
                    sr = document.createElement('span');
                    sr.id = 'newnym-hint';
                    sr.className = 'kh-sr';
                    btn.after(sr);
                }
                sr.textContent = hint;
                btn.setAttribute('aria-describedby', 'newnym-hint');
            }
            else if (!d.canRenew) btn.title = T('Yeni devre için ', 'Next circuit in ') + d.waitSeconds + T(' sn bekleyin', ' s');
            else btn.title = KH.t('mon.newnymTip');
        }).catch(() => { });
    }
    async function newNym(btn, force) {
        if (torInfo && !torInfo.configured) { KH.toast(torInfo.hint || T('Tor kontrol portu yapılandırılmamış (TOR_CONTROL)', 'Tor control port is not configured (TOR_CONTROL)'), 'warn'); return; }
        KH.busy(btn, true, T('İsteniyor…', 'Requesting…'));
        try {
            await KH.api('/api/tor/newnym', { method: 'POST', body: { force: !!force } });
            KH.toast(T('Yeni Tor devresi istendi; çıkış düğümleri değişecek', 'New Tor circuit requested; exit nodes will change'), 'ok');
        } catch (e) {
            KH.busy(btn, false);
            if (e.status === 409 && !force) {
                const ok = await KH.confirm({ title: T('Yine de yenilensin mi?', 'Renew anyway?'), message: e.message, okText: T('Zorla yenile', 'Force renew') });
                if (ok) return newNym(btn, true);
            } else KH.toast(e.message, 'warn');
        } finally {
            KH.busy(btn, false);
            loadTor();
        }
    }

    KH.act({
        'mon-toggle': (el) => toggle(el),
        'mon-check': (el) => checkNow(el),
        'mon-newnym': (el) => newNym(el, false),
        'mon-reload': () => loadEngines({ animate: true }),
        'mon-filter': (el) => {
            state.filter = el.dataset.filter;
            document.querySelectorAll('[data-action="mon-filter"]').forEach(b => b.setAttribute('aria-pressed', String(b === el)));
            renderTable();
        },
        'mon-search': KH.debounce((el) => { state.q = el.value.trim(); renderTable(); }, 120)
    });

    document.addEventListener('DOMContentLoaded', () => {
        loadEngines({ animate: true });
        loadTor();
        setInterval(() => { if (!document.hidden && !state.checking) loadEngines(); }, REFRESH_MS);
        document.addEventListener('visibilitychange', () => {
            if (!document.hidden && Date.now() - state.lastLoad > REFRESH_MS) loadEngines();
        });
    });
})();
