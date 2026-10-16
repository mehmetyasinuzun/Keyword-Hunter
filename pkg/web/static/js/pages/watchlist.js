/** İzleme listesi: KPI'lar, site ekleme (v3 onion doğrulama), süzülebilir tablo, kontrol / görüntü / geçmiş / duraklat / sil. */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    const ONION_RE = /^https?:\/\/(?:[a-z0-9-]+\.)*(?:[a-z2-7]{56}|[a-z2-7]{16})\.onion(?::\d{1,5})?(?:[/?#].*)?$/i;

    let items = [];
    let statusFilter = '';
    let shotTarget = null; // geçmiş modalındaki hedef { id, url }

    const pct = (n) => (KH.lang === 'en' ? n + '%' : '%' + n);
    const checked = (i) => !!i.lastChecked && new Date(i.lastChecked).getFullYear() > 1971;
    const isHttp = (u) => /^https?:\/\//i.test(String(u || ''));

    // ── Hücreler ────────────────────────────────────────────────────────
    function statusPill(i) {
        let pill;
        if (!checked(i)) pill = KH.html`<span class="kh-badge neutral">${KH.raw(KH.icon('clock'))} ${T('bekliyor', 'pending')}</span>`;
        else if (i.status === 'up') pill = KH.html`<span class="kh-badge ok"><span class="kh-dot up pulse"></span> ${T('ayakta', 'up')}</span>`;
        else if (i.status === 'engel') pill = KH.html`<span class="kh-badge warn" title="${T('Doğrulama / captcha ekranı', 'Verification / captcha page')}">${KH.raw(KH.icon('shield'))} ${T('engel', 'blocked')}</span>`;
        else pill = KH.html`<span class="kh-badge err"><span class="kh-dot down"></span> ${T('kapalı', 'down')}</span>`;
        const extra = [];
        if (i.changed) extra.push(KH.html`<span class="kh-badge info" title="${T('Son kontrolde içerik değişti', 'Content changed on last check')}">${KH.raw(KH.icon('activity'))} ${T('değişti', 'changed')}</span>`);
        if (!i.enabled) extra.push(KH.html`<span class="kh-badge neutral">${KH.raw(KH.icon('pause'))} ${T('duraklatıldı', 'paused')}</span>`);
        return KH.html`<div class="wl-status">${pill}${extra}</div>`;
    }
    function latency(i) {
        if (!checked(i) || !i.responseMs) return KH.html`<span class="kh-muted">—</span>`;
        const s = i.responseMs / 1000;
        const tone = s < 3 ? 'c-ok' : s < 8 ? 'c-warn' : 'c-err';
        return KH.html`<span class="wl-lat ${tone}">${s.toFixed(1)}s</span>${i.httpCode ? KH.html`<span class="wl-code">HTTP ${i.httpCode}</span>` : ''}`;
    }
    function uptime(i) {
        if (!checked(i)) return KH.html`<span class="kh-muted">—</span>`;
        const up = Math.max(0, Math.min(100, Number(i.uptimePct) || 0));
        const tone = up < 50 ? 'bad' : up < 85 ? 'mid' : 'good';
        return KH.html`<div class="wl-up ${tone}" title="${T('Son kontrollerde erişilebilirlik', 'Availability over recent checks')}"><div class="kh-progress thin"><div data-bar="${up}"></div></div><span>${pct(up)}</span></div>`;
    }
    function actions(i) {
        const tip = (tr, en) => T(tr, en);
        return KH.html`<div class="kh-rowactions wl-actions">
            <button type="button" class="kh-btn xs icon ghost" data-action="wl-check" data-id="${i.id}" aria-label="${tip('Şimdi kontrol et', 'Check now')}" data-tip="${tip('Şimdi kontrol et', 'Check now')}" data-write>${KH.raw(KH.icon('pulse'))}</button>
            <button type="button" class="kh-btn xs icon subtle" data-action="wl-shot-now" data-id="${i.id}" data-url="${i.url}" aria-label="${tip('Ekran görüntüsü al', 'Capture screenshot')}" data-tip="${tip('Görüntü al', 'Capture')}" data-write>${KH.raw(KH.icon('camera'))}</button>
            <button type="button" class="kh-btn xs icon subtle" data-action="wl-history" data-id="${i.id}" data-url="${i.url}" aria-label="${tip('Görüntü geçmişi', 'Screenshot history')}" data-tip="${tip('Geçmiş', 'History')}">${KH.raw(KH.icon('clock'))}</button>
            <button type="button" class="kh-btn xs icon subtle" data-action="wl-toggle" data-id="${i.id}" aria-label="${i.enabled ? tip('Duraklat', 'Pause') : tip('Sürdür', 'Resume')}" data-tip="${i.enabled ? tip('Duraklat', 'Pause') : tip('Sürdür', 'Resume')}" data-write>${KH.raw(KH.icon(i.enabled ? 'pause' : 'play'))}</button>
            <button type="button" class="kh-btn xs icon danger" data-action="wl-delete" data-id="${i.id}" data-name="${i.name}" aria-label="${tip('Sil', 'Delete')}" data-tip="${tip('Sil', 'Delete')}" data-write>${KH.raw(KH.icon('trash'))}</button>
        </div>`;
    }
    function row(i) {
        const host = String(i.url || '').replace(/^https?:\/\//i, '');
        const link = isHttp(i.url)
            ? KH.html`<a class="link" href="${i.url}" target="_blank" rel="noopener noreferrer" title="${i.url}">${KH.short(host, 24)}</a>`
            : KH.html`<span class="kh-mono text-xs">${KH.short(host, 24)}</span>`;
        const last = checked(i) ? KH.html`<span title="${KH.fmtTime(i.lastChecked, true)}">${KH.ago(i.lastChecked)}</span>` : KH.html`<span class="kh-muted">—</span>`;
        return KH.html`<tr data-id="${i.id}" class="${i.enabled ? '' : 'is-paused'}">
            <td>${statusPill(i)}</td>
            <td class="wl-site"><span class="title" title="${i.name}">${i.name}</span>
                <div class="meta">${i.category ? KH.html`<span class="kh-badge neutral">${i.category}</span>` : ''}${i.title ? KH.html`<span class="kh-truncate" title="${i.title}">${KH.short(i.title, 48)}</span>` : ''}${i.notes ? KH.html`<span class="wl-note" title="${i.notes}">${KH.raw(KH.icon('note', 'sm'))} ${KH.short(i.notes, 32)}</span>` : ''}</div></td>
            <td class="nowrap">${link} <button type="button" class="kh-copy" data-action="copy" data-copy="${i.url}" data-copy-label="${T('URL kopyalandı', 'URL copied')}" aria-label="${T('URL’yi kopyala', 'Copy URL')}">${KH.raw(KH.icon('copy', 'sm'))}</button></td>
            <td class="muted nowrap kh-mono text-xs">${last}</td>
            <td class="num nowrap">${latency(i)}</td>
            <td>${uptime(i)}</td>
            <td class="right nowrap">${actions(i)}</td>
        </tr>`;
    }

    // ── Liste ───────────────────────────────────────────────────────────
    function matches(i) {
        if (statusFilter === 'changed' && !i.changed) return false;
        if (statusFilter && statusFilter !== 'changed' && (!checked(i) || i.status !== statusFilter)) return false;
        const f = $('wl-filter').value.trim().toLocaleLowerCase(KH.lang);
        if (!f) return true;
        return [i.name, i.url, i.category, i.title, i.notes].join(' ').toLocaleLowerCase(KH.lang).includes(f);
    }
    function renderKPIs() {
        const n = (f) => items.filter(f).length;
        const up = n(i => checked(i) && i.status === 'up');
        const set = (id, v) => { const el = $(id); const before = el.textContent; el.textContent = String(v); if (before === '—') KH.countUp(el); };
        set('k-up', up);
        set('k-down', n(i => checked(i) && i.status === 'down'));
        set('k-blocked', n(i => checked(i) && i.status === 'engel'));
        set('k-changed', n(i => i.changed));
        const total = items.length;
        $('k-up-sub').textContent = total ? pct(KH.pct(up, total)) + T(' erişilebilirlik · ', ' availability · ') + total + T(' site', total === 1 ? ' site' : ' sites') : T('henüz site yok', 'no sites yet');
        document.querySelectorAll('.wl-kpi').forEach(b => b.setAttribute('aria-pressed', String(!!statusFilter && b.dataset.filter === statusFilter)));
        document.querySelectorAll('#wl-seg [data-filter]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.filter === statusFilter)));
    }
    function renderList() {
        const body = $('wl-body');
        const list = items.filter(matches);
        $('wl-count').textContent = list.length === items.length ? String(items.length) : list.length + '/' + items.length;
        if (!items.length) {
            KH.render(body, KH.html`<tr><td colspan="7"><div class="kh-empty"><div class="icon">${KH.raw(KH.icon('radar'))}</div><h3>${T('İzlenen site yok', 'No sites watched yet')}</h3><p>${T('Yukarıdaki formdan bir onion sitesi ekleyin ya da gömülü Türk tohum listesini yükleyin.', 'Add an onion site with the form above or load the built-in Turkish seed list.')}</p></div></td></tr>`);
        } else if (!list.length) {
            KH.render(body, KH.html`<tr><td colspan="7"><div class="kh-empty"><div class="icon">${KH.raw(KH.icon('filter'))}</div><h3>${T('Filtreyle eşleşen site yok', 'No sites match the filter')}</h3><div class="kh-inline"><button type="button" class="kh-btn sm subtle" data-action="wl-clear">${T('Filtreyi temizle', 'Clear filter')}</button></div></div></td></tr>`);
        } else {
            KH.render(body, KH.html`${list.map(row)}`);
            KH.enhance(body);
        }
        renderKPIs();
    }
    async function loadList(quiet) {
        try {
            const d = await KH.api('/api/watchlist');
            items = d.items || [];
            renderList();
        } catch (e) {
            if (!quiet) KH.render($('wl-body'), KH.html`<tr><td colspan="7"><div class="kh-alert err">${KH.raw(KH.icon('warn'))}<span>${e.message}</span></div></td></tr>`);
        }
    }

    // ── Ekleme formu doğrulaması ────────────────────────────────────────
    function normalizeURL(v) {
        v = v.trim();
        if (v && !/^[a-z][a-z0-9+.-]*:\/\//i.test(v) && /\.onion(?:[:/?#]|$)/i.test(v)) v = 'http://' + v;
        return v;
    }
    function urlState(show) {
        const input = $('wl-url'), msg = $('wl-url-msg');
        const v = normalizeURL(input.value);
        let state = '', text = T('Yalnızca v3 .onion adresleri (56 karakter) kabul edilir.', 'Only v3 .onion addresses (56 chars) are accepted.');
        if (v) {
            if (ONION_RE.test(v)) { state = 'ok'; text = T('Geçerli onion adresi', 'Valid onion address'); }
            else if (show || v.length > 20) {
                state = 'err';
                const host = v.replace(/^https?:\/\//i, '').split(/[/:?#]/)[0];
                text = !/\.onion$/i.test(host) ? T('Adres .onion ile bitmeli', 'Address must end with .onion')
                    : T('Onion adı 56 karakter olmalı (şu an ' + host.replace(/\.onion$/i, '').split('.').pop().length + ')', 'Onion name must be 56 characters (now ' + host.replace(/\.onion$/i, '').split('.').pop().length + ')');
            }
        }
        input.classList.toggle('is-valid', state === 'ok');
        input.classList.toggle('is-invalid', state === 'err');
        input.setAttribute('aria-invalid', String(state === 'err'));
        msg.className = 'kh-hint wl-url-msg' + (state ? ' is-' + state : '');
        msg.textContent = text;
        return state === 'ok' ? v : null;
    }
    function markName(show) {
        const el = $('wl-name');
        const bad = show && !el.value.trim();
        el.classList.toggle('is-invalid', bad);
        el.setAttribute('aria-invalid', String(bad));
        return !bad;
    }

    // ── Görüntü geçmişi ─────────────────────────────────────────────────
    async function showHistory(id, url) {
        shotTarget = { id, url };
        $('shot-url').textContent = url;
        $('shot-capture').dataset.id = id;
        $('shot-capture').dataset.url = url;
        const body = $('shot-body');
        KH.render(body, KH.html`<div class="wl-timeline"><div class="kh-skel block"></div><div class="kh-skel block"></div><div class="kh-skel block"></div></div>`);
        KH.openModal('shot-modal');
        try {
            const d = await KH.api('/api/screenshots?target=' + encodeURIComponent(url) + '&limit=30');
            const s = d.screenshots || [];
            $('shot-capture').hidden = !d.available;
            if (!s.length) {
                KH.render(body, KH.html`<div class="kh-empty"><div class="icon">${KH.raw(KH.icon('camera'))}</div><h3>${T('Bu hedef için görüntü yok', 'No screenshots for this target')}</h3><p>${d.available
                    ? T('“Görüntü al” ile Tor üzerinden ilk görüntüyü alın; içerik değiştiğinde otomatik görüntü de alınır.', 'Use “Capture” to take the first one over Tor; screenshots are also taken automatically when content changes.')
                    : T('Ekran görüntüsü bu ortamda kapalı — yalnızca Chromium içeren Docker imajında çalışır.', 'Screenshots are disabled here — they only work in the Docker image with Chromium.')}</p></div>`);
                return;
            }
            KH.render(body, KH.html`<ol class="wl-timeline">${s.map((x, n) => KH.html`<li class="wl-shot${n === 0 ? ' latest' : ''}">
                <a href="/screenshot/file/${encodeURIComponent(x.id)}" target="_blank" rel="noopener" aria-label="${T('Görüntüyü aç', 'Open screenshot')} · ${KH.fmtTime(x.takenAt)}">
                    <img loading="lazy" src="/screenshot/file/${encodeURIComponent(x.id)}" alt="">
                    ${n === 0 ? KH.html`<span class="kh-badge info wl-latest">${T('en yeni', 'latest')}</span>` : ''}
                    ${x.challenge ? KH.html`<span class="kh-badge warn wl-chal" title="${x.challengeKind || ''}">${KH.raw(KH.icon('warn'))} ${x.challengeKind || T('engel', 'blocked')}</span>` : ''}
                </a>
                <div class="cap"><span title="${KH.fmtTime(x.takenAt, true)}">${KH.ago(x.takenAt)}</span><span class="kh-mono">${Math.max(1, Math.round((x.bytes || 0) / 1024))} KB</span></div>
            </li>`)}</ol>`);
        } catch (e) {
            KH.render(body, KH.html`<div class="kh-alert err">${KH.raw(KH.icon('warn'))}<span>${e.message}</span></div>`);
        }
    }
    async function capture(btn, id, url) {
        KH.busy(btn, true, btn.classList.contains('icon') ? '' : T('Alınıyor…', 'Capturing…'));
        KH.toast(T('Ekran görüntüsü Tor üzerinden alınıyor (30–70 sn)…', 'Capturing over Tor (30–70 s)…'), 'info');
        try {
            const d = await KH.api('/api/screenshot', { method: 'POST', body: { targetUrl: url, source: 'manual', refId: Number(id) || 0 } });
            KH.toast(d.challenge
                ? T('Engel ekranı (' + (d.challengeKind || 'doğrulama') + ') — elle müdahale gerekebilir', 'Challenge page (' + (d.challengeKind || 'verification') + ') — manual action may be needed')
                : T('Görüntü alındı (', 'Captured (') + Math.round((d.bytes || 0) / 1024) + ' KB)', d.challenge ? 'warn' : 'ok');
            showHistory(id, url);
        } catch (e) { KH.toast(e.message, 'err'); }
        finally { KH.busy(btn, false); }
    }

    // ── Eylemler ────────────────────────────────────────────────────────
    KH.act({
        'wl-reload': async (btn) => { KH.busy(btn, true); await loadList(); KH.busy(btn, false); },
        'wl-filter': KH.debounce(renderList, 120),
        'wl-status': (el) => { statusFilter = el.dataset.filter || ''; renderList(); },
        'wl-quick': (el) => { statusFilter = statusFilter === el.dataset.filter ? '' : el.dataset.filter; renderList(); $('wl-list-t').scrollIntoView({ behavior: 'smooth', block: 'start' }); },
        'wl-clear': () => { statusFilter = ''; $('wl-filter').value = ''; renderList(); },
        'wl-url-check': () => urlState(false),

        'wl-add': async () => {
            const okName = markName(true);
            const url = urlState(true);
            if (!okName) { $('wl-name').focus(); return; }
            if (!url) { $('wl-url').focus(); return; }
            const btn = $('wl-submit');
            const payload = { name: $('wl-name').value.trim(), url, category: $('wl-category').value.trim(), notes: $('wl-notes').value.trim() };
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                await KH.api('/api/watchlist', { method: 'POST', body: payload });
                KH.toast(T('Site eklendi: ', 'Site added: ') + payload.name, 'ok');
                $('wl-form').reset();
                urlState(false); markName(false);
                await loadList();
                const tr = $('wl-body').querySelector('tr:last-child');
                if (tr) tr.classList.add('kh-flash');
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        },

        'wl-seed': async () => {
            const ok = await KH.confirm({
                title: T('Tohum listesini yükle', 'Load seed list'),
                message: T('Gömülü Türk onion listesindeki hedefler (forum, market, sorgu paneli, chat) izleme listesine eklenir ve Tor üzerinden düzenli olarak kontrol edilmeye başlanır.', 'Targets from the built-in Turkish onion list (forums, markets, lookup panels, chats) are added and checked regularly over Tor.'),
                okText: T('Yükle', 'Load')
            });
            if (!ok) return;
            try { const d = await KH.api('/api/watchlist/seed', { method: 'POST', body: {} }); KH.toast(KH.num(d.added) + T(' hedef eklendi', ' targets added'), 'ok'); loadList(); }
            catch (e) { KH.toast(e.message, 'err'); }
        },

        'wl-check': async (btn) => {
            KH.busy(btn, true);
            try {
                const d = await KH.api('/api/watchlist/' + encodeURIComponent(btn.dataset.id) + '/check', { method: 'POST', body: {} });
                const msg = d.status === 'up' ? T('Ayakta', 'Up') + ' (' + ((d.responseMs || 0) / 1000).toFixed(1) + 's)' + (d.changed ? T(' — içerik değişti', ' — content changed') : '')
                    : d.status === 'engel' ? T('Engel / captcha ekranı', 'Blocked / captcha page') : T('Çevrimdışı', 'Offline');
                KH.toast(msg, d.status === 'up' ? 'ok' : 'warn');
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); loadList(true); }
        },
        'wl-shot-now': (btn) => capture(btn, btn.dataset.id, btn.dataset.url),
        'wl-capture': (btn) => { if (shotTarget) capture(btn, shotTarget.id, shotTarget.url); },
        'wl-history': (btn) => showHistory(btn.dataset.id, btn.dataset.url),

        'wl-toggle': async (btn) => {
            KH.busy(btn, true);
            try {
                const d = await KH.api('/api/watchlist/' + encodeURIComponent(btn.dataset.id) + '/toggle', { method: 'POST', body: {} });
                KH.toast(d.enabled ? T('İzleme sürdürüldü', 'Monitoring resumed') : T('İzleme duraklatıldı', 'Monitoring paused'), 'ok', 2200);
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); loadList(true); }
        },
        'wl-delete': async (btn) => {
            const name = btn.dataset.name || '';
            const ok = await KH.confirm({ title: T('Siteyi sil', 'Delete site'), message: T('“' + name + '” ve tüm kontrol geçmişi silinecek.', '“' + name + '” and its entire check history will be deleted.'), okText: T('Sil', 'Delete'), danger: true });
            if (!ok) return;
            try { await KH.api('/api/watchlist/' + encodeURIComponent(btn.dataset.id) + '/delete', { method: 'POST', body: {} }); KH.toast(T('Silindi', 'Deleted'), 'ok', 2200); loadList(); }
            catch (e) { KH.toast(e.message, 'err'); }
        }
    });

    document.addEventListener('DOMContentLoaded', () => {
        loadList();
        $('wl-name').addEventListener('input', () => markName(false));
        $('wl-url').addEventListener('blur', () => { if ($('wl-url').value.trim()) urlState(true); });
        setInterval(() => { if (!document.hidden && !document.querySelector('.kh-modal-overlay.open')) loadList(true); }, 60000);
    });
})();
