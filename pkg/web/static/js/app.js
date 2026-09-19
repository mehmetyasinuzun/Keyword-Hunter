/**
 * KeywordHunter — paylaşılan istemci yardımcıları (tüm sayfalar).
 * Global `KH` nesnesi: güvenli HTML kaçışlama, API çağrıları (CSRF + hata
 * yönetimi), toast bildirimleri, onay diyaloğu, panoya kopyalama, zaman
 * biçimleme, kritiklik rozeti, klavye kısayolları ve navbar sağlık çipi.
 */
(function () {
    'use strict';

    const KH = window.KH = window.KH || {};

    // ── Özgün ikon (JS ile üretilen içerik için) ─────────────────────────
    KH.icon = function (name, cls) {
        return '<svg class="ico' + (cls ? ' ' + cls : '') + '" aria-hidden="true"><use href="#i-' + String(name).replace(/[^a-z0-9-]/gi, '') + '"/></svg>';
    };

    // ── Güvenlik: metin → HTML ───────────────────────────────────────────
    KH.esc = function (v) {
        return String(v == null ? '' : v)
            .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    };

    // ── Toast ────────────────────────────────────────────────────────────
    function toastHost() {
        let host = document.getElementById('kh-toasts');
        if (!host) {
            host = document.createElement('div');
            host.id = 'kh-toasts';
            host.className = 'kh-toasts';
            document.body.appendChild(host);
        }
        return host;
    }
    KH.toast = function (message, type, ms) {
        type = type || 'info';
        const icons = { ok: 'check', err: 'offline', warn: 'warn', info: 'info' };
        const el = document.createElement('div');
        el.className = 'kh-toast ' + type;
        el.setAttribute('role', type === 'err' ? 'alert' : 'status');
        el.innerHTML = '<span>' + KH.icon(icons[type] || 'info') + '</span><span>' + KH.esc(message) + '</span><button class="close" aria-label="Kapat">' + KH.icon('close') + '</button>';
        el.querySelector('.close').onclick = () => el.remove();
        toastHost().appendChild(el);
        setTimeout(() => { el.style.opacity = '0'; el.style.transition = 'opacity .3s'; setTimeout(() => el.remove(), 320); }, ms || (type === 'err' ? 6000 : 3500));
        return el;
    };

    // ── Onay diyaloğu (native confirm yerine tutarlı modal) ─────────────
    KH.confirm = function (opts) {
        opts = typeof opts === 'string' ? { message: opts } : (opts || {});
        return new Promise((resolve) => {
            const overlay = document.createElement('div');
            overlay.className = 'kh-modal-overlay open';
            overlay.innerHTML =
                '<div class="kh-modal" role="dialog" aria-modal="true">' +
                '<h2>' + KH.esc(opts.title || 'Emin misiniz?') + '</h2>' +
                '<p style="color:var(--text-secondary);line-height:1.6">' + KH.esc(opts.message || '') + '</p>' +
                '<div class="kh-modal-actions">' +
                '<button class="kh-btn subtle" data-act="no">' + KH.esc(opts.cancelText || 'Vazgeç') + '</button>' +
                '<button class="kh-btn ' + (opts.danger ? 'danger' : 'primary') + '" data-act="yes">' + KH.esc(opts.okText || 'Onayla') + '</button>' +
                '</div></div>';
            const done = (v) => { overlay.remove(); document.removeEventListener('keydown', onKey); resolve(v); };
            const onKey = (e) => { if (e.key === 'Escape') done(false); if (e.key === 'Enter') done(true); };
            overlay.querySelector('[data-act="no"]').onclick = () => done(false);
            overlay.querySelector('[data-act="yes"]').onclick = () => done(true);
            overlay.addEventListener('click', (e) => { if (e.target === overlay) done(false); });
            document.addEventListener('keydown', onKey);
            document.body.appendChild(overlay);
            overlay.querySelector('[data-act="yes"]').focus();
        });
    };

    // ── Modal yardımcıları ──────────────────────────────────────────────
    KH.openModal = function (id) { const m = document.getElementById(id); if (m) { m.classList.add('open'); const f = m.querySelector('input,select,textarea,button'); if (f) setTimeout(() => f.focus(), 30); } };
    KH.closeModal = function (id) { const m = document.getElementById(id); if (m) m.classList.remove('open'); };
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape') document.querySelectorAll('.kh-modal-overlay.open').forEach(m => m.classList.remove('open')); });
    document.addEventListener('click', (e) => { if (e.target.classList && e.target.classList.contains('kh-modal-overlay')) e.target.classList.remove('open'); });

    // ── API ─────────────────────────────────────────────────────────────
    // fetch sarmalayıcı: JSON gövde, CSRF (head.html'deki global fetch override
    // ekler), 401 → login, 429 → bekleme mesajı, HTTP hatası → Error(message).
    KH.api = async function (url, opts) {
        opts = opts || {};
        const init = { method: opts.method || 'GET', headers: { 'Accept': 'application/json' } };
        if (opts.body !== undefined) {
            init.headers['Content-Type'] = 'application/json';
            init.body = JSON.stringify(opts.body);
        }
        if (opts.signal) init.signal = opts.signal;
        const res = await fetch(url, init);
        if (res.status === 401) {
            KH.toast('Oturum süresi doldu, giriş sayfasına yönlendiriliyorsunuz', 'warn');
            setTimeout(() => location.href = '/login', 800);
            throw new Error('Oturum süresi doldu');
        }
        let data = null;
        const ct = res.headers.get('content-type') || '';
        if (ct.includes('application/json')) { try { data = await res.json(); } catch (_) { data = null; } }
        if (!res.ok) {
            const msg = (data && data.error) || (res.status === 429 ? 'Çok fazla istek; birkaç saniye bekleyin' : ('HTTP ' + res.status));
            const err = new Error(msg); err.status = res.status; err.data = data; throw err;
        }
        return data;
    };

    // ── Pano ────────────────────────────────────────────────────────────
    KH.copy = async function (text, label) {
        try { await navigator.clipboard.writeText(text); KH.toast((label || 'Kopyalandı') + '', 'ok', 1800); }
        catch (_) {
            const ta = document.createElement('textarea'); ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
            document.body.appendChild(ta); ta.select();
            try { document.execCommand('copy'); KH.toast('Kopyalandı', 'ok', 1800); } catch (e) { KH.toast('Kopyalanamadı', 'err'); }
            ta.remove();
        }
    };

    // ── Biçimleme ───────────────────────────────────────────────────────
    KH.fmtTime = function (iso, withSeconds) {
        if (!iso) return '—';
        const d = new Date(iso);
        if (isNaN(d.getTime()) || d.getFullYear() < 1971) return '—';
        const p = n => String(n).padStart(2, '0');
        return p(d.getDate()) + '.' + p(d.getMonth() + 1) + '.' + d.getFullYear() + ' ' + p(d.getHours()) + ':' + p(d.getMinutes()) + (withSeconds ? ':' + p(d.getSeconds()) : '');
    };
    KH.ago = function (iso) {
        if (!iso) return '—';
        const d = new Date(iso); const s = Math.round((Date.now() - d.getTime()) / 1000);
        if (isNaN(s)) return '—';
        if (s < 60) return s + ' sn önce';
        if (s < 3600) return Math.round(s / 60) + ' dk önce';
        if (s < 86400) return Math.round(s / 3600) + ' sa önce';
        return Math.round(s / 86400) + ' gün önce';
    };
    KH.sev = function (c) { c = Number(c) || 1; return '<span class="kh-badge sev-' + c + '" title="Kritiklik seviyesi ' + c + '/5">SEV ' + c + '</span>'; };
    KH.tags = function (str) { if (!str) return '<span class="kh-muted" style="font-size:.75rem">—</span>'; return String(str).split(',').map(t => t.trim()).filter(Boolean).map(t => '<span class="kh-tag">' + KH.esc(t) + '</span>').join(''); };
    KH.short = function (s, n) { s = String(s || ''); return s.length > n ? s.slice(0, n - 1) + '…' : s; };
    KH.num = function (n) { return Number(n || 0).toLocaleString('tr-TR'); };

    // ── Buton meşgul durumu ─────────────────────────────────────────────
    KH.busy = function (btn, on, text) {
        if (!btn) return;
        if (on) { btn.dataset.orig = btn.innerHTML; btn.disabled = true; btn.innerHTML = '<span class="spinner"></span> ' + KH.esc(text || 'İşleniyor…'); }
        else { btn.disabled = false; if (btn.dataset.orig) btn.innerHTML = btn.dataset.orig; }
    };

    // ── Navbar: hamburger + sağlık çipi + kısayollar ─────────────────────
    document.addEventListener('DOMContentLoaded', () => {
        const burger = document.getElementById('nav-burger');
        const links = document.getElementById('nav-links');
        if (burger && links) burger.addEventListener('click', () => links.classList.toggle('open'));

        const chip = document.getElementById('nav-health');
        if (chip) {
            KH.api('/api/monitor/summary').then(d => {
                const up = d.enginesActiveUp || 0, total = d.enginesActive || 0;
                const dot = chip.querySelector('.kh-dot');
                const txt = chip.querySelector('.txt');
                if (dot) dot.className = 'kh-dot ' + (up === 0 ? 'down' : (up < total / 2 ? 'warn' : 'up'));
                if (txt) txt.textContent = up + '/' + total + ' motor';
                chip.title = 'Aktif motorlardan çevrimiçi: ' + up + '/' + total + ' · Planlı tarama: ' + (d.scheduledActive || 0) + '/' + (d.scheduledTotal || 0) + (d.schedulerBusy ? ' · şu an tarama sürüyor' : '');
            }).catch(() => { });
        }

        // Klavye: "/" → arama, "g" + harf → sayfa
        let g = false;
        document.addEventListener('keydown', (e) => {
            const tag = (e.target.tagName || '').toLowerCase();
            if (tag === 'input' || tag === 'textarea' || tag === 'select' || e.target.isContentEditable) return;
            if (e.key === '/') { e.preventDefault(); location.href = '/search'; return; }
            if (e.key === 'g') { g = true; setTimeout(() => g = false, 900); return; }
            if (g) {
                const map = { d: '/dashboard', r: '/results', h: '/results/graph', a: '/analytics', s: '/search', p: '/scheduled', i: '/watchlist', m: '/monitor', c: '/crawl', y: '/settings' };
                if (map[e.key]) { g = false; location.href = map[e.key]; }
            }
        });
    });
})();
