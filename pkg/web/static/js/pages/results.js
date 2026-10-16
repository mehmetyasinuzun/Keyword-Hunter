/**
 * Bulgular: etiket bulutu, seçim + toplu işlemler, satır içi kritiklik/kategori
 * düzenleme, vaka durumu + analist notu, IOC modalı, tekil ve toplu etiketleme.
 * Satır kimliği/URL'si <tr data-id data-url data-title> özniteliklerinden okunur.
 */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    const rowOf = (el) => el.closest('tr[data-id]');
    const rows = () => Array.from(document.querySelectorAll('tr[data-id]'));
    const pageIds = () => rows().map(r => parseInt(r.dataset.id, 10));
    const pageData = () => $('main').dataset;

    function flash(el) { if (!el) return; el.classList.remove('kh-flash'); void el.offsetWidth; el.classList.add('kh-flash'); }
    /** UTF-8 bayt sınırına göre kırp (sunucu sınırları bayt cinsinden). */
    function clipBytes(s, max) {
        const enc = new TextEncoder();
        s = String(s || '');
        if (enc.encode(s).length <= max) return s;
        let out = '';
        for (const ch of s) { if (enc.encode(out + ch + '…').length > max) break; out += ch; }
        return out + '…';
    }

    // ── Etiket bulutu + sorgu önerileri ─────────────────────────────────
    function loadTags() {
        const cloud = $('tag-cloud');
        if (!cloud) return;
        const active = pageData().activeTag || '';
        KH.api('/api/tags').then(d => {
            const tags = (d.tags || []).slice(0, 60);
            if (!tags.length) { KH.render(cloud, KH.html`<span class="kh-hint">${T('Henüz etiket yok — kayıtları etiketleyerek oluşturun.', 'No tags yet — run tagging on records to create them.')}</span>`); fitTags(); return; }
            const max = Math.max.apply(null, tags.map(t => t.count));
            KH.render(cloud, KH.html`${tags.map(t => KH.html`<a class="kh-chip ${t.tag === active ? 'active' : ''} ${t.count >= max * 0.6 ? 'w3' : t.count >= max * 0.3 ? 'w2' : 'w1'}" href="${'/results?tag=' + encodeURIComponent(t.tag)}">${t.tag} <span class="count">${KH.num(t.count)}</span></a>`)}`);
            fitTags();
        }).catch(() => KH.render(cloud, KH.html`<span class="kh-hint">${T('Etiketler yüklenemedi', 'Tags failed to load')}</span>`));
    }
    function fitTags() {
        const cloud = $('tag-cloud'), more = $('tag-more');
        if (!cloud || !more) return;
        const clamped = cloud.classList.contains('clamped');
        more.hidden = clamped && cloud.scrollHeight <= cloud.clientHeight + 2;
        more.setAttribute('aria-expanded', String(!clamped));
        more.setAttribute('aria-label', clamped ? T('Tüm etiketleri göster', 'Show all tags') : T('Daha az göster', 'Show less'));
        more.title = more.getAttribute('aria-label');
    }
    function loadQueryList() {
        const dl = $('query-list');
        if (!dl) return;
        KH.api('/api/queries').then(d => {
            KH.render(dl, KH.html`${(d.queries || []).slice(0, 200).map(q => KH.html`<option value="${q.query}">`)}`);
        }).catch(() => { });
    }

    // ── IOC rozetleri + modal ───────────────────────────────────────────
    function setIocBadge(id, n) {
        const el = $('art-' + id);
        if (!el) return;
        if (n > 0) KH.render(el, KH.html`<button type="button" class="kh-badge info art-badge" data-action="show-ioc" title="${T('IOC listesini aç', 'Open IOC list')}">${KH.raw(KH.icon('dna'))} ${KH.num(n)} IOC</button>`);
        else el.textContent = '';
    }
    function loadIocCounts() {
        const ids = pageIds();
        if (!ids.length) return;
        KH.api('/api/artifacts/counts?ids=' + ids.join(',')).then(d => {
            Object.entries(d.counts || {}).forEach(([id, n]) => setIocBadge(id, n));
        }).catch(() => { });
    }
    const iocNames = {
        email: ['mail', 'E-posta', 'Email'], bitcoin: ['coin', 'Bitcoin', 'Bitcoin'], btc: ['coin', 'Bitcoin', 'Bitcoin'], monero: ['coin', 'Monero', 'Monero'], xmr: ['coin', 'Monero', 'Monero'],
        ip: ['globe', 'IP', 'IP'], onion: ['onion', 'Onion', 'Onion'], credit_card: ['card', 'Kart', 'Card'], phone: ['phone', 'Telefon', 'Phone'],
        ssh_key: ['key', 'SSH anahtarı', 'SSH key'], api_key: ['key', 'API anahtarı', 'API key'], username: ['user', 'Kullanıcı', 'Username'], hash: ['hash', 'Hash', 'Hash']
    };
    const iocName = (t) => { const n = iocNames[t] || ['info', t, t]; return { icon: n[0], label: KH.lang === 'en' ? n[2] : n[1] }; };
    function csvCell(v) { v = String(v == null ? '' : v); return /[",\n\r]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v; }
    function toCSV(list) {
        return ['type,value,confidence'].concat(list.map(a => [a.type, a.value, Number(a.confidence || 0).toFixed(2)].map(csvCell).join(','))).join('\n');
    }
    async function showIoc(el) {
        const row = rowOf(el);
        if (!row) return;
        const body = $('artifact-body'), copyAll = $('ioc-copy-all');
        $('ioc-sub').textContent = row.dataset.title || row.dataset.url;
        copyAll.hidden = true;
        KH.render(body, KH.html`<div class="kh-skel"></div><div class="kh-skel w-80"></div><div class="kh-skel w-60"></div>`);
        KH.openModal('artifact-modal');
        try {
            const d = await KH.api('/api/results/' + encodeURIComponent(row.dataset.id) + '/artifacts');
            const list = d.artifacts || [];
            if (!list.length) {
                KH.render(body, KH.html`<div class="kh-empty p-4"><div class="icon">${KH.raw(KH.icon('dna'))}</div><h3>${T('Bu kayıt için IOC yok', 'No IOCs for this record')}</h3>
                    <p>${T('Önce “Etiketle” çalıştırın; sayfa içeriği Tor üzerinden çekilir ve e-posta, cüzdan, IP, hash gibi göstergeler çıkarılır.', 'Run “Tag” first; the page is fetched over Tor and indicators such as emails, wallets, IPs and hashes are extracted.')}</p></div>`);
                return;
            }
            const byType = {};
            list.forEach(a => { byType[a.type] = (byType[a.type] || 0) + 1; });
            KH.render(body, KH.html`
                <div class="ioc-summary">${Object.keys(byType).map(t => { const n = iocName(t); return KH.html`<span class="kh-chip">${KH.raw(KH.icon(n.icon, 'sm'))} ${n.label} <span class="count">${byType[t]}</span></span>`; })}
                    <span class="kh-hint ml-auto">${T('güven skoruna göre sıralı', 'sorted by confidence')}</span></div>
                <div class="ioc-list">${list.map(a => {
                    const n = iocName(a.type), conf = Math.round((a.confidence || 0) * 100);
                    return KH.html`<div class="ioc-row">
                        <span class="kh-badge neutral ioc-type">${KH.raw(KH.icon(n.icon))} ${n.label}</span>
                        <div class="ioc-val"><div class="v">${a.value}</div>${a.context ? KH.html`<div class="ctx">${a.context}</div>` : ''}</div>
                        <span class="kh-badge mono ${conf >= 80 ? 'ok' : conf >= 60 ? 'info' : 'neutral'}" title="${T('Güven', 'Confidence')}">${conf}%</span>
                        <button type="button" class="kh-btn xs subtle icon" data-action="copy" data-copy="${a.value}" aria-label="${T('Kopyala', 'Copy')}" title="${T('Kopyala', 'Copy')}">${KH.raw(KH.icon('copy'))}</button>
                    </div>`;
                })}</div>`);
            copyAll.dataset.copy = toCSV(list);
            copyAll.dataset.copyLabel = T('Tüm IOC\'ler CSV olarak kopyalandı', 'All IOCs copied as CSV');
            $('ioc-copy-lbl').textContent = T('Tümünü CSV olarak kopyala', 'Copy all as CSV') + ' (' + list.length + ')';
            copyAll.hidden = false;
        } catch (e) {
            KH.render(body, KH.html`<div class="kh-alert err">${KH.raw(KH.icon('warn'))}<span>${e.message}</span></div>`);
        }
    }

    // ── Seçim ───────────────────────────────────────────────────────────
    const selected = () => Array.from(document.querySelectorAll('.row-check:checked')).map(c => parseInt(c.value, 10));
    function updateSelection() {
        const n = selected().length, total = rows().length;
        $('bulk-n').textContent = String(n);
        $('bulkwrap').classList.toggle('show', n > 0);
        rows().forEach(r => r.classList.toggle('selected', r.querySelector('.row-check').checked));
        const all = $('select-all');
        if (all) { all.checked = n > 0 && n === total; all.indeterminate = n > 0 && n < total; }
    }
    function setAll(on) { document.querySelectorAll('.row-check').forEach(c => { c.checked = on; }); updateSelection(); }
    let lastCheck = null;
    document.addEventListener('click', (e) => {
        const cb = e.target.closest && e.target.closest('.row-check');
        if (!cb) return;
        if (e.shiftKey && lastCheck && lastCheck !== cb) {
            const list = Array.from(document.querySelectorAll('.row-check'));
            const a = list.indexOf(lastCheck), b = list.indexOf(cb);
            if (a >= 0 && b >= 0) list.slice(Math.min(a, b), Math.max(a, b) + 1).forEach(c => { c.checked = cb.checked; });
        }
        lastCheck = cb;
    });
    const rowById = (id) => document.querySelector('tr[data-id="' + id + '"]');

    // ── Satır alanları ──────────────────────────────────────────────────
    function setCrit(row, crit) {
        const sel = row.querySelector('select[data-field="criticality"]');
        sel.value = String(crit);
        sel.className = 'kh-select sm crit-select s' + crit;
    }
    function setHits(id, n) {
        const el = $('count-' + id);
        if (!el) return;
        el.textContent = String(n);
        el.classList.toggle('pos', n > 0);
        flash(el);
    }
    function renderTags(el, str) {
        const list = String(str || '').split(',').map(t => t.trim()).filter(Boolean);
        if (!list.length) { KH.render(el, KH.html`<span class="kh-hint">${T('etiketlenmedi', 'not tagged')}</span>`); return; }
        KH.render(el, KH.html`${list.map(t => KH.html`<a class="kh-tag" href="${'/results?tag=' + encodeURIComponent(t)}">${t}</a>`)}`);
    }
    async function saveCase(row, status, note) {
        return KH.api('/api/results/' + encodeURIComponent(row.dataset.id) + '/case', { method: 'POST', body: { status, note } });
    }

    // ── Toplu etiketleme ────────────────────────────────────────────────
    let jobId = '', pollTimer = null, tagES = null;
    const jobStatus = (s) => ({ pending: T('kuyrukta', 'queued'), running: T('çalışıyor', 'running'), completed: T('tamamlandı', 'completed'), failed: T('başarısız', 'failed'), cancelled: T('iptal edildi', 'cancelled'), cancelling: T('iptal ediliyor', 'cancelling') })[s] || s;
    function setTagProgress(pct, text) {
        if (pct != null) { $('tag-bar').style.width = pct + '%'; $('tag-pct').textContent = pct + '%'; }
        if (text) { $('tag-status').textContent = text; $('tag-status').title = text; }
    }
    function startTagStream(total) {
        if (tagES) tagES.close();
        let done = 0;
        try {
            tagES = new EventSource('/events');
            tagES.onmessage = (ev) => {
                try {
                    const d = JSON.parse(ev.data);
                    if (d.type === 'auto_tag' || d.type === 'auto_tag_error') { done++; setTagProgress(Math.min(99, Math.round(done / Math.max(total, 1) * 100)), d.message); }
                } catch (_) { }
            };
            tagES.onerror = () => { if (tagES) tagES.close(); };
        } catch (_) { tagES = null; }
    }
    function stopPolling() { if (pollTimer) { clearInterval(pollTimer); pollTimer = null; } }
    function startPolling(id, total) {
        stopPolling();
        pollTimer = setInterval(() => {
            KH.api('/api/batch-auto-tag/' + encodeURIComponent(id)).then(d => {
                const tot = d.total || total;
                const pct = Math.min(100, Math.round((d.processed || 0) / Math.max(tot, 1) * 100));
                setTagProgress(pct, T('Durum: ', 'Status: ') + jobStatus(d.status) + ' · ' + (d.processed || 0) + '/' + tot + ' · ✓' + (d.successes || 0) + ' ✗' + (d.failures || 0));
                if (d.status === 'completed') {
                    stopPolling(); setTagProgress(100);
                    KH.toast(T('Etiketleme tamamlandı, sayfa yenileniyor', 'Tagging completed, reloading'), 'ok');
                    setTimeout(() => location.reload(), 1200);
                } else if (d.status === 'failed' || d.status === 'cancelled') {
                    cleanupBatch();
                    KH.toast(d.error || (T('İş ', 'Job ') + jobStatus(d.status)), d.status === 'failed' ? 'err' : 'warn');
                }
            }).catch(() => { });
        }, 2000);
    }
    function cleanupBatch() {
        jobId = ''; stopPolling();
        if (tagES) { tagES.close(); tagES = null; }
        const btn = $('batch-tag-btn');
        if (btn) KH.busy(btn, false);
        if ($('cancel-batch-btn')) $('cancel-batch-btn').hidden = true;
        if ($('tag-progress')) $('tag-progress').hidden = true;
        const tb = document.querySelector('.results-toolbar');
        if (tb) tb.classList.remove('busy');
    }
    async function startBatch(ids) {
        const btn = $('batch-tag-btn');
        KH.busy(btn, true, T('Kuyruğa alınıyor…', 'Queueing…'));
        $('cancel-batch-btn').hidden = false;
        $('tag-progress').hidden = false;
        document.querySelector('.results-toolbar').classList.add('busy');
        setTagProgress(0, T('İş kuyruğa alınıyor…', 'Queueing job…'));
        startTagStream(ids.length);
        try {
            const d = await KH.api('/api/batch-auto-tag', { method: 'POST', body: { resultIds: ids, query: pageData().query || '' } });
            jobId = d.jobId;
            KH.busy(btn, false); KH.busy(btn, true, T('İşleniyor…', 'Processing…'));
            setTagProgress(0, T('Durum: ', 'Status: ') + jobStatus(d.status || 'pending') + ' · 0/' + (d.total || ids.length));
            startPolling(d.jobId, d.total || ids.length);
        } catch (e) { cleanupBatch(); KH.toast(e.message, 'err'); }
    }

    // ── Eylemler ────────────────────────────────────────────────────────
    KH.act({
        'filter-auto': (el) => { const f = el.form; if (f.requestSubmit) f.requestSubmit(); else f.submit(); },
        'tags-toggle': () => { $('tag-cloud').classList.toggle('clamped'); fitTags(); },

        'select-all': (el) => setAll(el.checked),
        'row-check': () => updateSelection(),
        'clear-selection': () => setAll(false),

        'bulk-copy': () => {
            const urls = selected().map(id => (rowById(id) || {}).dataset).filter(Boolean).map(d => d.url);
            if (!urls.length) return;
            KH.copy(urls.join('\n'), urls.length + T(' URL kopyalandı', ' URLs copied'));
        },
        'bulk-delete': async () => {
            const ids = selected();
            if (!ids.length) return;
            const ok = await KH.confirm({
                title: T('Kayıtları sil', 'Delete records'),
                message: ids.length + T(' bulgu ve bunlara bağlı etiket/IOC kayıtları kalıcı olarak silinecek.', ' findings and their tags/IOCs will be permanently deleted.'),
                okText: T('Sil', 'Delete'), danger: true
            });
            if (!ok) return;
            try {
                const d = await KH.api('/api/results/delete', { method: 'POST', body: { ids } });
                KH.toast((d.deleted != null ? d.deleted : ids.length) + T(' kayıt silindi', ' records deleted'), 'ok');
                ids.forEach(id => { const r = rowById(id); if (r) { r.classList.add('leaving'); setTimeout(() => r.remove(), 260); } });
                setTimeout(() => { updateSelection(); if (!rows().length) location.reload(); }, 300);
            } catch (e) { KH.toast(e.message, 'err'); }
        },
        'bulk-sev': async () => {
            const ids = selected();
            if (!ids.length) return;
            const v = await KH.prompt({
                title: T('Kritiklik ata', 'Set severity'), icon: 'fire', type: 'number', min: 1, max: 5, value: '4',
                label: T('Seçili ', 'Severity (1-5) for ') + ids.length + T(' kayıt için kritiklik (1-5)', ' selected records'),
                okText: T('Uygula', 'Apply')
            });
            if (v === null) return;
            const crit = parseInt(v, 10);
            if (!(crit >= 1 && crit <= 5)) { KH.toast(T('Kritiklik 1 ile 5 arasında olmalı', 'Severity must be between 1 and 5'), 'warn'); return; }
            let ok = 0, fail = 0;
            for (const id of ids) {
                const row = rowById(id);
                if (!row) continue;
                const cat = row.querySelector('input[data-field="category"]').value.trim();
                try {
                    await KH.api('/api/update-criticality', { method: 'POST', body: { id, type: 'result', criticality: crit, category: cat } });
                    setCrit(row, crit); flash(row); ok++;
                } catch (_) { fail++; }
            }
            KH.toast(ok + T(' kayıt güncellendi', ' records updated') + (fail ? ' · ' + fail + T(' başarısız', ' failed') : ''), fail ? 'warn' : 'ok');
        },

        'update-row': async (el) => {
            const row = rowOf(el);
            const sel = row.querySelector('select[data-field="criticality"]');
            const catEl = row.querySelector('input[data-field="category"]');
            const crit = parseInt(sel.value, 10);
            const cat = catEl.value.trim();
            sel.className = 'kh-select sm crit-select s' + crit;
            try {
                await KH.api('/api/update-criticality', { method: 'POST', body: { id: parseInt(row.dataset.id, 10), type: 'result', criticality: crit, category: cat } });
                catEl.value = cat || 'Genel'; catEl.title = catEl.value;
                KH.toast(T('Güncellendi', 'Updated'), 'ok', 1500); flash(row);
            } catch (e) { KH.toast(e.message, 'err'); }
        },
        'update-case': async (sel) => {
            const row = rowOf(sel);
            sel.className = 'kh-select sm case-sel cs-' + sel.value;
            try {
                await saveCase(row, sel.value, row.querySelector('.note-btn').dataset.note || '');
                KH.toast(T('Vaka güncellendi', 'Case updated'), 'ok', 1500); flash(row);
            } catch (e) { KH.toast(e.message, 'err'); }
        },
        'edit-note': async (btn) => {
            const row = rowOf(btn);
            const note = await KH.prompt({
                title: T('Analist notu', 'Analyst note'), icon: 'note', multiline: true, maxLength: 2000,
                label: KH.short(row.dataset.title || row.dataset.url, 90), value: btn.dataset.note || '',
                placeholder: T('Bu bulguyla ilgili gözlemleriniz, bağlantılı vakalar…', 'Your observations, related cases…')
            });
            if (note === null) return;
            const v = note.trim();
            try {
                await saveCase(row, row.querySelector('select[data-field="case"]').value, v);
                btn.dataset.note = v;
                btn.classList.toggle('has-note', !!v);
                btn.title = v ? T('Not: ', 'Note: ') + v : T('Not ekle', 'Add note');
                btn.setAttribute('aria-label', v ? T('Not', 'Note') : T('Not ekle', 'Add note'));
                const pv = row.querySelector('.note-preview');
                pv.textContent = v; pv.title = v; pv.hidden = !v;
                KH.toast(v ? T('Not kaydedildi', 'Note saved') : T('Not silindi', 'Note removed'), 'ok', 1500);
            } catch (e) { KH.toast(e.message, 'err'); }
        },

        'auto-tag': async (btn) => {
            const row = rowOf(btn), id = parseInt(row.dataset.id, 10);
            const tagsEl = $('tag-list-' + id);
            const orig = tagsEl.innerHTML;
            KH.busy(btn, true);
            KH.render(tagsEl, KH.html`<span class="kh-tag pending"><span class="kh-spinner"></span> ${T('Tor üzerinden çekiliyor…', 'Fetching over Tor…')}</span>`);
            try {
                const d = await KH.api('/api/auto-tag', { method: 'POST', body: { id } });
                renderTags(tagsEl, d.tagsStr);
                if (d.artifacts > 0) setIocBadge(id, d.artifacts);
                if (d.keywordHits > 0) setHits(id, d.keywordHits);
                if (typeof d.criticality === 'number' && d.criticality >= 1 && d.criticality <= 5) setCrit(row, d.criticality);
                if (d.category) { const c = row.querySelector('input[data-field="category"]'); c.value = d.category; c.title = d.category; }
                flash(row);
                KH.toast(T('Etiketlendi: ', 'Tagged: ') + (d.tagsStr || '—') + (d.artifacts ? ' · ' + d.artifacts + ' IOC' : ''), 'ok');
            } catch (e) { tagsEl.innerHTML = orig; KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        },
        'recount': async (btn) => {
            const id = parseInt(rowOf(btn).dataset.id, 10);
            KH.busy(btn, true);
            try {
                const d = await KH.api('/api/analyze-result', { method: 'POST', body: { id } });
                setHits(id, d.count || 0);
                KH.toast(T('Sıklık: ', 'Frequency: ') + (d.count || 0), 'ok');
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        },
        'show-ioc': showIoc,
        'watch': async (btn) => {
            const row = rowOf(btn);
            let host;
            try { host = new URL(row.dataset.url).hostname; } catch (_) { KH.toast(T('Geçersiz URL', 'Invalid URL'), 'err'); return; }
            if (!host) { KH.toast(T('Geçersiz URL', 'Invalid URL'), 'err'); return; }
            const name = clipBytes((row.dataset.title || '').trim() || host, 120);
            KH.busy(btn, true);
            try {
                await KH.api('/api/watchlist', { method: 'POST', body: { name, url: 'http://' + host, category: T('Bulgu', 'Finding'), notes: T('Bulgulardan eklendi', 'Added from findings') } });
                KH.toast(T('İzleme listesine eklendi: ', 'Added to watchlist: ') + KH.short(host, 32), 'ok');
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        },

        'batch-tag': (btn) => {
            const ids = btn.dataset.scope === 'selected' ? selected() : pageIds();
            if (!ids.length) { KH.toast(T('Etiketlenecek kayıt yok', 'Nothing to tag'), 'warn'); return; }
            if (jobId) { KH.toast(T('Zaten çalışan bir etiketleme işi var', 'A tagging job is already running'), 'warn'); return; }
            if (!$('batch-tag-btn')) return;
            startBatch(ids);
        },
        'batch-cancel': async (btn) => {
            if (!jobId) { cleanupBatch(); return; }
            KH.busy(btn, true);
            try {
                const d = await KH.api('/api/batch-auto-tag/' + encodeURIComponent(jobId) + '/cancel', { method: 'POST', body: {} });
                if (d.status === 'cancelling') {
                    setTagProgress(null, T('İptal ediliyor; mevcut kayıt bitince durur…', 'Cancelling; stops after the current record…'));
                } else { cleanupBatch(); KH.toast(T('İptal edildi', 'Cancelled'), 'warn'); }
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        }
    });

    document.addEventListener('keydown', (e) => {
        if (e.key !== 'Escape' || !selected().length) return;
        const tag = (e.target.tagName || '').toLowerCase();
        if (tag === 'input' || tag === 'textarea' || tag === 'select' || document.querySelector('.kh-modal-overlay.open')) return;
        setAll(false);
    });

    document.addEventListener('DOMContentLoaded', () => {
        loadTags(); loadQueryList(); loadIocCounts();
        const clr = $('tag-clear');
        if (clr) { const u = new URL(location.href); u.searchParams.delete('tag'); u.searchParams.delete('page'); clr.href = u.pathname + u.search; }
        document.querySelectorAll('.when[data-ago] > span').forEach(s => { const iso = s.parentElement.dataset.ago; if (iso) s.textContent = KH.ago(iso); });
        window.addEventListener('resize', KH.debounce(fitTags, 150));
        window.addEventListener('pagehide', () => { stopPolling(); if (tagES) tagES.close(); });
        updateSelection();
    });
})();
