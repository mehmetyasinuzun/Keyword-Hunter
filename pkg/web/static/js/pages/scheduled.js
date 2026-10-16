/** Planlı taramalar: genel bildirim merkezi, tarama kartları (aç/kapat, şimdi çalıştır, sil), yeni tarama modalı. */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;

    // ── Biçimleme ───────────────────────────────────────────────────────
    function fmtInterval(m) {
        m = Number(m) || 0;
        const u = { min: T('dk', 'min'), h: T('sa', 'h'), d: T('gün', 'd') };
        if (m < 60) return m + ' ' + u.min;
        if (m % 1440 === 0) return (m / 1440) + ' ' + u.d;
        if (m % 60 === 0) return (m / 60) + ' ' + u.h;
        return Math.floor(m / 60) + ' ' + u.h + ' ' + (m % 60) + ' ' + u.min;
    }
    function paintTimes(scope) {
        scope.querySelectorAll('[data-ago]').forEach(el => {
            const iso = el.dataset.ago;
            if (!iso) return;
            el.title = KH.fmtTime(iso);
            el.textContent = KH.ago(iso);
        });
        scope.querySelectorAll('[data-interval]').forEach(el => { el.textContent = T('her ', 'every ') + fmtInterval(el.dataset.interval); });
    }

    // ── Özet şeridi ─────────────────────────────────────────────────────
    function cards() { return Array.from(document.querySelectorAll('.sc-card')); }
    function paintSummary() {
        const list = cards();
        const active = list.filter(c => !c.classList.contains('is-paused'));
        const count = $('sc-count'), act = $('sc-active'), next = $('sc-next');
        count.hidden = act.hidden = !list.length;
        count.textContent = list.length + ' ' + T('tarama', list.length === 1 ? 'scan' : 'scans');
        act.textContent = active.length + ' ' + T('aktif', 'active');
        act.className = 'kh-badge ' + (active.length ? 'ok' : 'neutral');
        // En yakın sonraki çalışma (yalnızca aktif kartlar)
        let soonest = null;
        active.forEach(c => {
            const iso = c.querySelector('[data-k="nextRun"]').dataset.ago;
            if (iso && (!soonest || Date.parse(iso) < Date.parse(soonest))) soonest = iso;
        });
        next.textContent = soonest ? T('Sıradaki çalışma ', 'Next run ') + KH.ago(soonest) : '';
        next.title = soonest ? KH.fmtTime(soonest) : '';
        $('sc-empty').hidden = list.length > 0;
    }

    function setEnabled(card, on) {
        card.classList.toggle('is-paused', !on);
        const sw = card.querySelector('.kh-switch');
        if (sw) sw.setAttribute('aria-checked', String(on));
    }

    // Sunucudaki güncel sayaçları kartlara işler (şimdi çalıştır sonrası ve periyodik).
    function setStat(card, key, value) {
        const el = card.querySelector('[data-k="' + key + '"]');
        if (!el) return;
        const prev = el.textContent.replace(/\D/g, '');
        if (String(value) === prev) return;
        el.textContent = KH.num(value);
        el.closest('.sc-stat').classList.remove('kh-flash');
        void el.offsetWidth; // animasyonu yeniden başlat
        el.closest('.sc-stat').classList.add('kh-flash');
    }
    function setTime(card, key, iso, emptyText) {
        const el = card.querySelector('[data-k="' + key + '"]');
        if (!el) return;
        if (iso) { el.dataset.ago = iso; el.title = KH.fmtTime(iso); el.textContent = KH.ago(iso); }
        else { delete el.dataset.ago; el.removeAttribute('title'); el.textContent = emptyText; }
    }
    async function refreshCards() {
        const d = await KH.api('/api/scheduled');
        const byId = {};
        (d.searches || []).forEach(s => { byId[s.ID] = s; });
        cards().forEach(card => {
            const s = byId[card.dataset.id];
            if (!s) return;
            setEnabled(card, !!s.Enabled);
            setStat(card, 'runs', s.TotalRuns);
            setStat(card, 'last', s.LastResultCount);
            setStat(card, 'new', s.LastNewCount);
            card.querySelector('[data-k="newbox"]').classList.toggle('tone-ok', s.LastNewCount > 0);
            setTime(card, 'lastRun', s.LastRunAt, T('henüz çalışmadı', 'never run'));
            setTime(card, 'nextRun', s.NextRunAt, '—');
        });
        paintSummary();
        return byId;
    }

    // ── Yeni tarama modalı ──────────────────────────────────────────────
    function syncPresets(v) {
        document.querySelectorAll('#interval-presets [data-min]').forEach(b => b.setAttribute('aria-pressed', String(Number(b.dataset.min) === Number(v))));
    }
    function openNew() {
        if (document.body.classList.contains('role-viewer')) return;
        KH.openModal('new-modal');
    }

    // ── Bildirim merkezi ────────────────────────────────────────────────
    function alertPayload() {
        return {
            webhookUrl: $('alert-url').value.trim(),
            minCriticality: parseInt($('alert-min').value, 10) || 3,
            enabled: $('alert-enabled').getAttribute('aria-checked') === 'true'
        };
    }
    function looksLikeURL(v) { return /^https?:\/\/[^\s/]+/i.test(v); }

    KH.act({
        'sc-new': openNew,
        'sc-preset': (el) => { $('new-interval').value = el.dataset.min; syncPresets(el.dataset.min); },
        'sc-interval': (el) => syncPresets(el.value),

        'sc-create': async () => {
            const btn = $('new-submit');
            const query = $('new-query').value.trim();
            const interval = parseInt($('new-interval').value, 10);
            const hook = $('new-webhook').value.trim();
            if (!query) { KH.toast(T('Arama sorgusu gerekli', 'A search query is required'), 'warn'); $('new-query').focus(); return; }
            if (!(interval >= 5 && interval <= 1440)) { KH.toast(T('Aralık 5–1440 dakika olmalı', 'Interval must be 5–1440 minutes'), 'warn'); $('new-interval').focus(); return; }
            if (hook && !looksLikeURL(hook)) { KH.toast(T('Webhook bir http(s) adresi olmalı', 'Webhook must be an http(s) URL'), 'warn'); $('new-webhook').focus(); return; }
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                await KH.api('/api/scheduled', { method: 'POST', body: { query, intervalMinutes: interval, alertThreshold: parseInt($('new-threshold').value, 10) || 3, webhookUrl: hook } });
                KH.toast(T('Tarama planlandı', 'Scan scheduled'), 'ok');
                // #new parçasını düşür; aksi halde yeniden yüklemede modal tekrar açılır.
                setTimeout(() => location.replace('/scheduled'), 450);
            } catch (e) { KH.toast(e.message, 'err'); KH.busy(btn, false); }
        },

        'sc-toggle': async (el) => {
            const card = el.closest('.sc-card');
            const was = el.getAttribute('aria-checked') === 'true';
            setEnabled(card, !was); // iyimser
            el.disabled = true;
            try {
                const d = await KH.api('/api/scheduled/' + encodeURIComponent(el.dataset.id) + '/toggle', { method: 'POST', body: {} });
                setEnabled(card, !!d.enabled);
                KH.toast(d.enabled ? T('Tarama aktif edildi', 'Scan enabled') : T('Tarama durduruldu', 'Scan paused'), 'ok', 2200);
                refreshCards().catch(() => { });
            } catch (e) { setEnabled(card, was); KH.toast(e.message, 'err'); }
            finally { el.disabled = false; paintSummary(); }
        },

        'sc-run': async (btn) => {
            const id = btn.dataset.id;
            const card = btn.closest('.sc-card');
            const runsBefore = Number(card.querySelector('[data-k="runs"]').textContent.replace(/\D/g, '')) || 0;
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                await KH.api('/api/scheduled/' + encodeURIComponent(id) + '/run-now', { method: 'POST', body: {} });
                KH.toast(T('Tarama başlatıldı — bitince sayaçlar burada güncellenir', 'Scan started — counters update here when it finishes'), 'ok');
                card.classList.add('is-running');
                watchRun(card, id, runsBefore);
            } catch (e) {
                KH.toast(e.status === 409 ? T('Başka bir tarama sürüyor; bitince tekrar deneyin', 'Another scan is running; try again when it finishes') : e.message, 'warn');
            } finally { KH.busy(btn, false); }
        },

        'sc-delete': async (el) => {
            const name = el.dataset.name || '';
            const ok = await KH.confirm({
                title: T('Planlı taramayı sil', 'Delete scheduled scan'),
                message: T('“' + name + '” taraması silinecek. Daha önce bulunan bulgular korunur.', '“' + name + '” will be deleted. Findings already collected are kept.'),
                okText: T('Sil', 'Delete'), danger: true
            });
            if (!ok) return;
            try {
                await KH.api('/api/scheduled/' + encodeURIComponent(el.dataset.id), { method: 'DELETE' });
                const card = el.closest('.sc-card');
                card.classList.add('is-leaving');
                setTimeout(() => { card.remove(); paintSummary(); }, 260);
                KH.toast(T('Silindi', 'Deleted'), 'ok', 2200);
            } catch (e) { KH.toast(e.message, 'err'); }
        },

        'alert-save': async () => {
            const btn = $('alert-save');
            const p = alertPayload();
            if (p.webhookUrl && !looksLikeURL(p.webhookUrl)) { KH.toast(T('Webhook bir http(s) adresi olmalı', 'Webhook must be an http(s) URL'), 'warn'); $('alert-url').focus(); return; }
            if (p.enabled && !p.webhookUrl) { KH.toast(T('Bildirimleri açmak için webhook adresi girin', 'Enter a webhook URL to enable notifications'), 'warn'); $('alert-url').focus(); return; }
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                await KH.api('/api/alert-config', { method: 'POST', body: p });
                $('alert-url').defaultValue = p.webhookUrl;
                $('alert-card').dataset.on = p.enabled ? '1' : '0';
                KH.toast(T('Bildirim ayarları kaydedildi', 'Notification settings saved'), 'ok');
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        },

        'alert-test': async (btn) => {
            const input = $('alert-url');
            const url = input.value.trim();
            if (!url) { KH.toast(T('Önce webhook adresi girin', 'Enter a webhook URL first'), 'warn'); input.focus(); return; }
            // Değiştirilmemiş adres (admin dışı rollerde gizlenmiş maske) gönderilmez;
            // sunucu kayıtlı adresi kullanır — sır istemciye hiç inmeden test edilebilir.
            const body = { webhookUrl: url === input.defaultValue.trim() ? '' : url };
            KH.busy(btn, true, btn.dataset.busyText);
            try { await KH.api('/api/alert-config/test', { method: 'POST', body }); KH.toast(T('Test bildirimi gönderildi — kanalı kontrol edin', 'Test notification sent — check your channel'), 'ok'); }
            catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        }
    });

    // “Şimdi çalıştır” sonrası: toplam çalışma sayısı artana kadar yokla (en fazla ~3 dk).
    function watchRun(card, id, runsBefore) {
        let tries = 0;
        const tick = async () => {
            tries++;
            try {
                const byId = await refreshCards();
                const s = byId[id];
                if (s && s.TotalRuns > runsBefore) {
                    card.classList.remove('is-running');
                    KH.toast(T('Tarama tamamlandı: ', 'Scan finished: ') + KH.num(s.LastResultCount) + T(' sonuç, ', ' results, ') + KH.num(s.LastNewCount) + T(' yeni', ' new'), s.LastNewCount > 0 ? 'ok' : 'info', 5000);
                    return;
                }
            } catch (_) { /* yoklamaya devam */ }
            if (tries < 45) setTimeout(tick, 4000); else card.classList.remove('is-running');
        };
        setTimeout(tick, 3000);
    }

    document.addEventListener('DOMContentLoaded', () => {
        paintTimes(document);
        paintSummary();
        if (location.hash === '#new') openNew();
        window.addEventListener('hashchange', () => { if (location.hash === '#new') openNew(); });
        // Sayaç ve göreli zamanları tazele (sekme görünürken)
        setInterval(() => { if (!document.hidden && cards().length) refreshCards().catch(() => { }); }, 60000);
    });
})();
