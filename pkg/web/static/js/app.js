/**
 * KeywordHunter — çekirdek istemci (tüm sayfalar).
 *
 * Sıkı CSP (script-src 'self') altında çalışır: şablonlarda satır içi betik ya
 * da on*= işleyicisi yoktur. Davranış, bildirimsel öznitelikler üzerinden tek
 * noktadan delege edilir:
 *
 *   data-action="ad"   → tıklamada   KH.actions.ad(el, event)
 *   data-change="ad"   → change'de   KH.actions.ad(el, event)
 *   data-input="ad"    → input'ta    KH.actions.ad(el, event)
 *   data-submit="ad"   → form submit (preventDefault otomatik)
 *   data-enter="ad"    → Enter tuşunda
 *
 * Sayfa betikleri işleyicilerini KH.act({ ad: fn, ... }) ile kaydeder; sayfa
 * verisi <script type="application/json" id="page-data"> içinden KH.data() ile
 * okunur. Dark web kaynaklı her metin KH.esc ile kaçışlanır; DOM'a değer
 * gömerken innerHTML yerine data-* öznitelikleri ve textContent tercih edilir.
 */
(function () {
    'use strict';

    const KH = window.KH = window.KH || {};
    const doc = document;
    const root = doc.documentElement;
    const reducedMotion = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;

    // ── Çerez / i18n ─────────────────────────────────────────────────────
    function readCookie(name) {
        const m = doc.cookie.match('(?:^|; )' + name.replace(/[.$?*|{}()[\]\\/+^]/g, '\\$&') + '=([^;]*)');
        return m ? decodeURIComponent(m[1]) : '';
    }
    function writeCookie(name, value, days) {
        const secure = location.protocol === 'https:' ? ';secure' : '';
        doc.cookie = name + '=' + encodeURIComponent(value) + ';path=/;max-age=' + (days * 86400) + ';samesite=lax' + secure;
    }
    KH.cookie = readCookie;

    function readJSON(id) {
        const el = doc.getElementById(id);
        if (!el) return null;
        try { return JSON.parse(el.textContent || 'null'); } catch (_) { return null; }
    }
    KH.i18n = { lang: root.lang || 'tr', msgs: readJSON('kh-i18n') || {} };
    KH.lang = KH.i18n.lang;
    KH.t = function (key, fallback) { return KH.i18n.msgs[key] || fallback || key; };
    KH.tr = function (tr, en) { return KH.lang === 'en' ? en : tr; };
    KH.setLang = function (lang) {
        fetch('/api/lang', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ lang }) })
            .catch(() => writeCookie('lang', lang, 365))
            .finally(() => location.reload());
    };
    /** Sayfa verisi: <script type="application/json" id="page-data"> */
    KH.data = function (id) { return readJSON(id || 'page-data') || {}; };
    KH.csrf = function () { return readCookie('csrf_token'); };

    // ── CSRF: /api/ ve form POST'larına başlık (tüm fetch çağrıları) ────
    const nativeFetch = window.fetch.bind(window);
    window.fetch = function (input, init) {
        const options = init ? Object.assign({}, init) : {};
        const method = (options.method || (input && input.method) || 'GET').toUpperCase();
        const url = typeof input === 'string' ? input : (input && input.url) || '';
        let path = url;
        try { const u = new URL(url, location.href); if (u.origin !== location.origin) path = ''; else path = u.pathname; } catch (_) { }
        if (path && !['GET', 'HEAD', 'OPTIONS'].includes(method)) {
            const headers = new Headers(options.headers || {});
            if (!headers.has('X-CSRF-Token')) { const tok = KH.csrf(); if (tok) headers.set('X-CSRF-Token', tok); }
            options.headers = headers;
        }
        return nativeFetch(input, options);
    };
    window.getCSRFToken = KH.csrf; // eski modüller için

    // ── Güvenli HTML ─────────────────────────────────────────────────────
    KH.esc = function (v) {
        return String(v == null ? '' : v)
            .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    };
    KH.icon = function (name, cls) {
        return '<svg class="ico' + (cls ? ' ' + KH.esc(cls) : '') + '" aria-hidden="true"><use href="#i-' + String(name).replace(/[^a-z0-9-]/gi, '') + '"/></svg>';
    };
    /** Etiketli şablon: değerler otomatik kaçışlanır; KH.raw() ile işaretlenenler hariç. */
    function Raw(s) { this.s = String(s); }
    KH.raw = function (s) { return new Raw(s); };
    KH.html = function (strings) {
        let out = strings[0];
        for (let i = 1; i < arguments.length; i++) {
            const v = arguments[i];
            if (Array.isArray(v)) out += v.map(x => x instanceof Raw ? x.s : KH.esc(x)).join('');
            else out += v instanceof Raw ? v.s : KH.esc(v);
            out += strings[i];
        }
        return new Raw(out);
    };
    KH.render = function (el, raw) { if (el) el.innerHTML = raw instanceof Raw ? raw.s : String(raw); return el; };

    // ── Toast ────────────────────────────────────────────────────────────
    KH.toast = function (message, type, ms) {
        type = type || 'info';
        ms = ms || (type === 'err' ? 6000 : 3500);
        const host = doc.getElementById('kh-toasts') || (function () {
            const h = doc.createElement('div'); h.id = 'kh-toasts'; h.className = 'kh-toasts'; h.setAttribute('aria-live', 'polite'); doc.body.appendChild(h); return h;
        })();
        const icons = { ok: 'check', err: 'offline', warn: 'warn', info: 'info' };
        const el = doc.createElement('div');
        el.className = 'kh-toast ' + type;
        el.style.setProperty('--ttl', ms + 'ms');
        el.setAttribute('role', type === 'err' ? 'alert' : 'status');
        el.innerHTML = '<span class="ic">' + KH.icon(icons[type] || 'info', 'sm') + '</span><span class="msg"></span><button class="close" type="button" aria-label="' + KH.esc(KH.t('common.close', 'Kapat')) + '">' + KH.icon('close', 'sm') + '</button>';
        el.querySelector('.msg').textContent = String(message);
        let timer = null, left = ms, started = Date.now();
        const leave = () => { el.classList.add('leaving'); setTimeout(() => el.remove(), 320); };
        const arm = () => { started = Date.now(); timer = setTimeout(leave, left); };
        el.addEventListener('mouseenter', () => { clearTimeout(timer); left -= Date.now() - started; });
        el.addEventListener('mouseleave', arm);
        el.querySelector('.close').addEventListener('click', leave);
        host.appendChild(el);
        while (host.children.length > 5) host.firstElementChild.remove();
        arm();
        return el;
    };

    // ── Modal (erişilebilir: odak tuzağı, ESC, odak iadesi) ──────────────
    const focusables = 'a[href],button:not([disabled]),input:not([disabled]):not([type=hidden]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])';
    const modalStack = [];
    function trapKeys(e) {
        const top = modalStack[modalStack.length - 1];
        if (!top) return;
        if (e.key === 'Escape') { e.preventDefault(); top.close(); return; }
        if (e.key === 'Tab') {
            const f = Array.from(top.el.querySelectorAll(focusables)).filter(x => x.offsetParent !== null);
            if (!f.length) return;
            const first = f[0], last = f[f.length - 1];
            if (e.shiftKey && doc.activeElement === first) { e.preventDefault(); last.focus(); }
            else if (!e.shiftKey && doc.activeElement === last) { e.preventDefault(); first.focus(); }
        }
    }
    doc.addEventListener('keydown', trapKeys, true);
    function openOverlay(overlay, onClose) {
        const prev = doc.activeElement;
        const entry = {
            el: overlay,
            close: function () {
                overlay.classList.remove('open');
                const i = modalStack.indexOf(entry); if (i >= 0) modalStack.splice(i, 1);
                if (onClose) onClose();
                if (prev && prev.focus) setTimeout(() => prev.focus(), 0);
            }
        };
        modalStack.push(entry);
        overlay.classList.add('open');
        const dialog = overlay.querySelector('[role=dialog]') || overlay.firstElementChild;
        if (dialog) { dialog.setAttribute('role', 'dialog'); dialog.setAttribute('aria-modal', 'true'); }
        const auto = overlay.querySelector('[autofocus]') || overlay.querySelector('input:not([type=hidden]):not([type=checkbox]),select,textarea') || overlay.querySelector(focusables);
        if (auto) setTimeout(() => auto.focus(), 60);
        return entry;
    }
    KH.openModal = function (id) {
        const m = typeof id === 'string' ? doc.getElementById(id) : id;
        if (!m || m.classList.contains('open')) return;
        openOverlay(m);
    };
    KH.closeModal = function (id) {
        const m = typeof id === 'string' ? doc.getElementById(id) : id;
        if (!m) return;
        const entry = modalStack.find(x => x.el === m);
        if (entry) entry.close(); else m.classList.remove('open');
    };
    doc.addEventListener('mousedown', (e) => {
        if (e.target.classList && e.target.classList.contains('kh-modal-overlay')) KH.closeModal(e.target);
    });

    function dialog(opts, bodyHTML, resolveValue) {
        return new Promise((resolve) => {
            const overlay = doc.createElement('div');
            overlay.className = 'kh-modal-overlay';
            overlay.innerHTML = '<div class="kh-modal sm" role="dialog" aria-modal="true" aria-labelledby="kh-dlg-t">' +
                '<h2 id="kh-dlg-t">' + (opts.icon ? KH.icon(opts.icon) : '') + '<span></span></h2>' +
                (opts.message ? '<p class="lead"></p>' : '') + (bodyHTML || '') +
                '<div class="kh-modal-actions"><button type="button" class="kh-btn subtle" data-k="no"></button>' +
                '<button type="button" class="kh-btn ' + (opts.danger ? 'danger' : 'primary') + '" data-k="yes"></button></div></div>';
            overlay.querySelector('h2 span').textContent = opts.title || KH.t('dlg.sure', KH.tr('Emin misiniz?', 'Are you sure?'));
            if (opts.message) overlay.querySelector('.lead').textContent = opts.message;
            overlay.querySelector('[data-k=no]').textContent = opts.cancelText || KH.t('common.cancel', 'Vazgeç');
            overlay.querySelector('[data-k=yes]').textContent = opts.okText || KH.tr('Onayla', 'Confirm');
            doc.body.appendChild(overlay);
            let settled = false;
            const finish = (ok) => {
                if (settled) return; settled = true;
                const v = ok ? resolveValue(overlay) : null;
                entry.close();
                setTimeout(() => overlay.remove(), 400);
                resolve(v);
            };
            const entry = openOverlay(overlay, () => finish(false));
            overlay.querySelector('[data-k=no]').addEventListener('click', () => finish(false));
            overlay.querySelector('[data-k=yes]').addEventListener('click', () => finish(true));
            overlay.addEventListener('keydown', (e) => { if (e.key === 'Enter' && e.target.tagName !== 'TEXTAREA' && e.target.tagName !== 'BUTTON') { e.preventDefault(); finish(true); } });
            if (!bodyHTML) setTimeout(() => overlay.querySelector('[data-k=yes]').focus(), 60);
        });
    }
    /** Onay diyaloğu → Promise<boolean> */
    KH.confirm = function (opts) {
        opts = typeof opts === 'string' ? { message: opts } : (opts || {});
        return dialog(Object.assign({ icon: opts.danger ? 'warn' : 'info' }, opts), '', () => true).then(v => v === true);
    };
    /** Metin girişi diyaloğu (window.prompt yerine) → Promise<string|null> */
    KH.prompt = function (opts) {
        opts = typeof opts === 'string' ? { title: opts } : (opts || {});
        const field = opts.multiline
            ? '<textarea class="kh-textarea" maxlength="' + (opts.maxLength || 2000) + '"></textarea>'
            : '<input class="kh-input' + (opts.mono ? ' mono' : '') + '" type="' + (opts.type || 'text') + '" maxlength="' + (opts.maxLength || 500) + '"' + (opts.min != null ? ' min="' + opts.min + '"' : '') + (opts.max != null ? ' max="' + opts.max + '"' : '') + '>';
        const body = '<div class="kh-modal-body"><label class="kh-field"><span class="kh-label"></span>' + field + (opts.hint ? '<span class="kh-hint"></span>' : '') + '</label></div>';
        const p = dialog(Object.assign({ icon: opts.icon || 'edit', okText: opts.okText || KH.t('common.save', 'Kaydet') }, opts), body, (ov) => ov.querySelector('input,textarea').value);
        const ov = doc.body.lastElementChild;
        ov.querySelector('.kh-label').textContent = opts.label || '';
        if (opts.hint) ov.querySelector('.kh-hint').textContent = opts.hint;
        const inp = ov.querySelector('input,textarea');
        inp.value = opts.value == null ? '' : String(opts.value);
        if (opts.placeholder) inp.placeholder = opts.placeholder;
        setTimeout(() => { inp.focus(); inp.select && inp.select(); }, 80);
        return p;
    };

    // ── API ─────────────────────────────────────────────────────────────
    /** fetch sarmalayıcı: JSON gövde, CSRF, 401 → giriş, 429 → bekleme, hata → Error(message). */
    KH.api = async function (url, opts) {
        opts = opts || {};
        const init = { method: opts.method || (opts.body !== undefined ? 'POST' : 'GET'), headers: { 'Accept': 'application/json' }, credentials: 'same-origin' };
        if (opts.body !== undefined) { init.headers['Content-Type'] = 'application/json'; init.body = JSON.stringify(opts.body); }
        if (opts.signal) init.signal = opts.signal;
        const res = await fetch(url, init);
        if (res.status === 401) {
            if (!KH._expired) {
                KH._expired = true;
                KH.toast(KH.tr('Oturum süresi doldu, giriş sayfasına yönlendiriliyorsunuz', 'Session expired, redirecting to sign in'), 'warn');
                setTimeout(() => { location.href = '/login'; }, 900);
            }
            throw new Error(KH.tr('Oturum süresi doldu', 'Session expired'));
        }
        let data = null;
        if ((res.headers.get('content-type') || '').includes('application/json')) { try { data = await res.json(); } catch (_) { data = null; } }
        if (!res.ok) {
            const msg = (data && data.error) || (res.status === 429 ? KH.tr('Çok fazla istek; birkaç saniye bekleyin', 'Too many requests; wait a few seconds') : res.status === 403 ? KH.tr('Bu işlem için yetkiniz yok', 'You are not allowed to do this') : 'HTTP ' + res.status);
            const err = new Error(msg); err.status = res.status; err.data = data; throw err;
        }
        return data;
    };

    // ── Pano ────────────────────────────────────────────────────────────
    KH.copy = async function (text, label) {
        const ok = () => KH.toast(label || KH.tr('Kopyalandı', 'Copied'), 'ok', 1800);
        try { await navigator.clipboard.writeText(String(text)); ok(); }
        catch (_) {
            const ta = doc.createElement('textarea'); ta.value = String(text); ta.setAttribute('readonly', ''); ta.style.position = 'fixed'; ta.style.opacity = '0';
            doc.body.appendChild(ta); ta.select();
            try { doc.execCommand('copy'); ok(); } catch (e) { KH.toast(KH.tr('Kopyalanamadı', 'Copy failed'), 'err'); }
            ta.remove();
        }
    };

    // ── Biçimleme ───────────────────────────────────────────────────────
    const locale = () => (KH.lang === 'en' ? 'en-GB' : 'tr-TR');
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
        if (isNaN(s) || d.getFullYear() < 1971) return '—';
        try {
            const rtf = new Intl.RelativeTimeFormat(locale(), { numeric: 'auto', style: 'short' });
            if (Math.abs(s) < 60) return rtf.format(-s, 'second');
            if (Math.abs(s) < 3600) return rtf.format(-Math.round(s / 60), 'minute');
            if (Math.abs(s) < 86400) return rtf.format(-Math.round(s / 3600), 'hour');
            return rtf.format(-Math.round(s / 86400), 'day');
        } catch (_) {
            if (s < 60) return s + ' sn önce';
            if (s < 3600) return Math.round(s / 60) + ' dk önce';
            if (s < 86400) return Math.round(s / 3600) + ' sa önce';
            return Math.round(s / 86400) + ' gün önce';
        }
    };
    KH.sev = function (c) { c = Math.min(5, Math.max(1, Number(c) || 1)); return '<span class="kh-badge sev-' + c + '" title="' + KH.esc(KH.tr('Kritiklik', 'Severity')) + ' ' + c + '/5">SEV ' + c + '</span>'; };
    KH.tags = function (str) {
        const list = String(str || '').split(',').map(t => t.trim()).filter(Boolean);
        if (!list.length) return '<span class="kh-muted text-xs">—</span>';
        return list.map(t => '<span class="kh-tag">' + KH.esc(t) + '</span>').join('');
    };
    KH.short = function (s, n) { s = String(s || ''); return s.length > n ? s.slice(0, n - 1) + '…' : s; };
    KH.num = function (n) { return Number(n || 0).toLocaleString(locale()); };
    KH.pct = function (a, b) { return b > 0 ? Math.round(a / b * 100) : 0; };
    KH.debounce = function (fn, ms) { let t; return function () { const a = arguments, c = this; clearTimeout(t); t = setTimeout(() => fn.apply(c, a), ms || 200); }; };

    // ── Buton meşgul durumu ─────────────────────────────────────────────
    KH.busy = function (btn, on, text) {
        if (!btn) return;
        if (on) {
            if (btn.dataset.busy === '1') return;
            btn.dataset.busy = '1'; btn.dataset.orig = btn.innerHTML; btn.disabled = true; btn.setAttribute('aria-busy', 'true');
            btn.innerHTML = '<span class="spinner" aria-hidden="true"></span>' + (text ? ' <span>' + KH.esc(text) + '</span>' : '');
        } else {
            btn.dataset.busy = ''; btn.disabled = false; btn.removeAttribute('aria-busy');
            if (btn.dataset.orig) btn.innerHTML = btn.dataset.orig;
        }
    };

    // ── Tema ────────────────────────────────────────────────────────────
    KH.theme = function () { return root.dataset.theme || 'auto'; };
    /** Etkin (çözümlenmiş) tema: 'light' | 'dark' */
    KH.resolvedTheme = function () {
        const t = KH.theme();
        if (t === 'auto') return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
        return t;
    };
    KH.cssVar = function (name, el) { return getComputedStyle(el || root).getPropertyValue(name).trim(); };
    /** Grafik kütüphaneleri için güncel tema paleti */
    KH.palette = function () {
        const v = KH.cssVar;
        return {
            series: [1, 2, 3, 4, 5, 6, 7, 8].map(i => v('--chart-' + i)),
            sev: [1, 2, 3, 4, 5].map(i => v('--sev-' + i)),
            grid: v('--chart-grid'), text: v('--chart-text'), fg: v('--text'), fg2: v('--text-2'),
            surface: v('--surface-solid'), border: v('--border-strong'), primary: v('--primary'),
            ok: v('--ok'), err: v('--err'), warn: v('--warn'), violet: v('--violet'),
            font: v('--font-sans'), mono: v('--font-mono')
        };
    };
    function applyTheme(t, animate) {
        const go = () => {
            root.dataset.theme = t;
            writeCookie('theme', t, 365);
            doc.querySelectorAll('[data-action="theme-set"]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.value === t)));
            window.dispatchEvent(new CustomEvent('kh:theme', { detail: { theme: t, resolved: KH.resolvedTheme() } }));
        };
        if (animate && doc.startViewTransition && !reducedMotion) doc.startViewTransition(go); else go();
    }
    KH.setTheme = function (t) { applyTheme(['light', 'dark', 'auto'].includes(t) ? t : 'auto', true); };
    if (window.matchMedia) {
        window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => {
            if (KH.theme() === 'auto') window.dispatchEvent(new CustomEvent('kh:theme', { detail: { theme: 'auto', resolved: KH.resolvedTheme() } }));
        });
    }

    // ── Olay delegasyonu ────────────────────────────────────────────────
    KH.actions = KH.actions || {};
    /** İşleyici kaydı: KH.act({ ad: (el, ev) => … }) */
    KH.act = function (map) { Object.assign(KH.actions, map); };
    function dispatch(attr, e) {
        const el = e.target.closest && e.target.closest('[' + attr + ']');
        if (!el) return;
        if (el.disabled || el.getAttribute('aria-disabled') === 'true') return;
        const names = el.getAttribute(attr).split(/\s+/).filter(Boolean);
        names.forEach((name) => {
            const fn = KH.actions[name];
            if (typeof fn !== 'function') { console.warn('[KH] tanımsız eylem:', name); return; }
            try { fn(el, e); } catch (err) { console.error('[KH] eylem hatası:', name, err); KH.toast(err.message || String(err), 'err'); }
        });
    }
    doc.addEventListener('click', (e) => {
        const el = e.target.closest && e.target.closest('[data-action]');
        if (el && (el.tagName === 'A' && !el.getAttribute('href') || el.tagName === 'BUTTON' && el.type !== 'submit')) e.preventDefault();
        dispatch('data-action', e);
    });
    doc.addEventListener('change', (e) => dispatch('data-change', e));
    doc.addEventListener('input', (e) => dispatch('data-input', e));
    doc.addEventListener('submit', (e) => {
        if (e.target.closest('[data-submit]')) { e.preventDefault(); dispatch('data-submit', e); }
    });
    doc.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && !e.isComposing && e.target.closest && e.target.closest('[data-enter]')) { e.preventDefault(); dispatch('data-enter', e); }
    });

    // Yerleşik eylemler
    KH.act({
        'modal-open': (el) => KH.openModal(el.dataset.target),
        'modal-close': (el) => KH.closeModal(el.dataset.target || el.closest('.kh-modal-overlay')),
        'copy': (el) => KH.copy(el.dataset.copy != null ? el.dataset.copy : (el.closest('[data-url]') || {}).dataset?.url || '', el.dataset.copyLabel),
        'toggle-switch': (el) => { const on = !(el.classList.contains('on') || el.getAttribute('aria-checked') === 'true'); el.classList.toggle('on', on); el.setAttribute('aria-checked', String(on)); },
        'theme-cycle': () => { const order = ['auto', 'light', 'dark']; KH.setTheme(order[(order.indexOf(KH.theme()) + 1) % 3]); KH.toast(KH.tr('Tema: ', 'Theme: ') + ({ auto: KH.tr('Sistem', 'System'), light: KH.tr('Açık', 'Light'), dark: KH.tr('Koyu', 'Dark') })[KH.theme()], 'info', 1400); },
        'theme-set': (el) => KH.setTheme(el.dataset.value),
        'lang-toggle': () => KH.setLang(KH.lang === 'en' ? 'tr' : 'en'),
        'nav-toggle': () => root.classList.toggle('nav-open'),
        'nav-close': () => root.classList.remove('nav-open'),
        'sb-collapse': () => {
            const on = !root.classList.contains('sb-collapsed');
            root.classList.toggle('sb-collapsed', on);
            if (on) writeCookie('sb', '1', 365); else doc.cookie = 'sb=;path=/;max-age=0';
        },
        'menu-toggle': (el, e) => {
            e.stopPropagation();
            const m = doc.getElementById(el.dataset.target); if (!m) return;
            const open = !m.classList.contains('open');
            closeMenus(); m.classList.toggle('open', open); el.setAttribute('aria-expanded', String(open));
            if (open) { const first = m.querySelector('button,a'); if (first) setTimeout(() => first.focus(), 30); }
        },
        'palette': () => Palette.open(),
        'shortcuts': () => showShortcuts(),
        'my-password': () => changeMyPassword(),
        'reload': () => location.reload(),
        'back-top': () => window.scrollTo({ top: 0, behavior: reducedMotion ? 'auto' : 'smooth' })
    });
    function closeMenus() {
        doc.querySelectorAll('.kh-menu.open').forEach(m => m.classList.remove('open'));
        doc.querySelectorAll('[data-action="menu-toggle"]').forEach(b => b.setAttribute('aria-expanded', 'false'));
    }
    doc.addEventListener('click', (e) => { if (!e.target.closest('.kh-menu')) closeMenus(); });
    doc.addEventListener('keydown', (e) => { if (e.key === 'Escape') { closeMenus(); root.classList.remove('nav-open'); } });

    async function changeMyPassword() {
        closeMenus();
        const current = await KH.prompt({ title: KH.t('shell.myPassword'), label: KH.tr('Mevcut parola', 'Current password'), type: 'password', icon: 'lock', okText: KH.tr('Devam', 'Next') });
        if (!current) return;
        const next = await KH.prompt({ title: KH.t('shell.myPassword'), label: KH.tr('Yeni parola (en az 8 karakter)', 'New password (min 8 chars)'), type: 'password', icon: 'lock', hint: KH.tr('Diğer oturumlarınız kapanır.', 'Your other sessions will be signed out.') });
        if (!next) return;
        if (next.length < 8) { KH.toast(KH.tr('Parola en az 8 karakter olmalı', 'Password must be at least 8 characters'), 'warn'); return; }
        try { await KH.api('/api/me/password', { method: 'POST', body: { current, new: next } }); KH.toast(KH.tr('Parola güncellendi', 'Password updated'), 'ok'); }
        catch (e) { KH.toast(e.message, 'err'); }
    }

    // ── Komut paleti (Ctrl/⌘+K) ─────────────────────────────────────────
    const Palette = {
        el: null, input: null, list: null, items: [], sel: 0,
        build() {
            if (this.el) return;
            const o = doc.createElement('div');
            o.className = 'kh-palette-overlay';
            o.innerHTML = '<div class="kh-palette" role="dialog" aria-modal="true" aria-label="' + KH.esc(KH.t('shell.palette')) + '">' +
                '<div class="kh-palette-input">' + KH.icon('search') + '<input type="text" autocomplete="off" spellcheck="false" aria-controls="kh-pal-list" aria-autocomplete="list"><kbd>Esc</kbd></div>' +
                '<div class="kh-palette-list" id="kh-pal-list" role="listbox"></div>' +
                '<div class="kh-palette-foot"><span><kbd>↑</kbd> <kbd>↓</kbd> ' + KH.esc(KH.tr('gezin', 'navigate')) + '</span><span><kbd>↵</kbd> ' + KH.esc(KH.tr('seç', 'select')) + '</span><span class="ml-auto">KeywordHunter</span></div></div>';
            doc.body.appendChild(o);
            this.el = o; this.input = o.querySelector('input'); this.list = o.querySelector('.kh-palette-list');
            this.input.placeholder = KH.t('shell.paletteHint');
            this.input.addEventListener('input', () => { this.sel = 0; this.render(); });
            this.input.addEventListener('keydown', (e) => {
                if (e.key === 'ArrowDown') { e.preventDefault(); this.move(1); }
                else if (e.key === 'ArrowUp') { e.preventDefault(); this.move(-1); }
                else if (e.key === 'Enter') { e.preventDefault(); this.run(this.items[this.sel]); }
            });
            this.list.addEventListener('click', (e) => { const b = e.target.closest('[data-i]'); if (b) this.run(this.items[+b.dataset.i]); });
            this.list.addEventListener('mousemove', (e) => { const b = e.target.closest('[data-i]'); if (b && +b.dataset.i !== this.sel) { this.sel = +b.dataset.i; this.paintSel(); } });
            o.addEventListener('mousedown', (e) => { if (e.target === o) this.close(); });
        },
        commands() {
            const nav = Array.from(doc.querySelectorAll('.kh-nav-link')).map(a => ({
                sec: KH.t('pal.goto'), icon: a.dataset.icon || 'arrow-right', label: a.querySelector('.kh-nav-text').textContent.trim(),
                hint: (a.querySelector('.kbd') || {}).textContent || '', run: () => { location.href = a.getAttribute('href'); }
            }));
            const acts = [
                { sec: KH.t('pal.action'), icon: 'siren', label: KH.t('pal.criticalFindings'), run: () => { location.href = '/results?minCriticality=4'; } },
                { sec: KH.t('pal.action'), icon: 'scheduled', label: KH.t('pal.newSchedule'), run: () => { location.href = '/scheduled#new'; } },
                { sec: KH.t('pal.action'), icon: 'contrast', label: KH.tr('Tema: Açık / Koyu / Sistem', 'Theme: Light / Dark / System'), run: () => KH.actions['theme-cycle']() },
                { sec: KH.t('pal.action'), icon: 'language', label: KH.t('pal.toggleLang'), run: () => KH.actions['lang-toggle']() },
                { sec: KH.t('pal.action'), icon: 'keyboard', label: KH.t('shell.shortcuts'), hint: '?', run: () => showShortcuts() },
                { sec: KH.t('pal.action'), icon: 'lock', label: KH.t('shell.myPassword'), run: () => changeMyPassword() },
                { sec: KH.t('pal.action'), icon: 'logout', label: KH.t('pal.logout'), run: () => { const f = doc.querySelector('form[action="/logout"]'); if (f) f.submit(); } }
            ];
            return nav.concat(acts);
        },
        render() {
            const q = this.input.value.trim();
            const ql = q.toLocaleLowerCase(locale());
            let items = this.commands().filter(c => !ql || c.label.toLocaleLowerCase(locale()).includes(ql));
            if (q) {
                items.unshift(
                    { sec: KH.tr('Ara', 'Search'), icon: 'search', label: KH.t('pal.searchDark') + ' “' + q + '”', run: () => { location.href = '/search?q=' + encodeURIComponent(q); } },
                    { sec: KH.tr('Ara', 'Search'), icon: 'filter', label: KH.t('pal.filterFindings') + ' “' + q + '”', run: () => { location.href = '/results?text=' + encodeURIComponent(q); } }
                );
            }
            this.items = items;
            if (!items.length) { this.list.innerHTML = '<div class="kh-palette-empty">' + KH.esc(KH.t('pal.noMatch')) + '</div>'; return; }
            let html = '', last = '';
            items.forEach((c, i) => {
                if (c.sec !== last) { html += '<div class="kh-palette-sec">' + KH.esc(c.sec) + '</div>'; last = c.sec; }
                let label = KH.esc(c.label);
                if (ql && !c.label.includes('“')) {
                    const idx = c.label.toLocaleLowerCase(locale()).indexOf(ql);
                    if (idx >= 0) label = KH.esc(c.label.slice(0, idx)) + '<mark>' + KH.esc(c.label.slice(idx, idx + q.length)) + '</mark>' + KH.esc(c.label.slice(idx + q.length));
                }
                html += '<button type="button" class="kh-palette-item" role="option" data-i="' + i + '">' + KH.icon(c.icon) + '<span>' + label + '</span>' + (c.hint ? '<span class="hint">' + KH.esc(c.hint) + '</span>' : '') + '</button>';
            });
            this.list.innerHTML = html;
            this.paintSel();
        },
        paintSel() {
            this.list.querySelectorAll('.kh-palette-item').forEach(b => { const on = +b.dataset.i === this.sel; b.classList.toggle('sel', on); b.setAttribute('aria-selected', String(on)); if (on) b.scrollIntoView({ block: 'nearest' }); });
        },
        move(d) { if (!this.items.length) return; this.sel = (this.sel + d + this.items.length) % this.items.length; this.paintSel(); },
        run(c) { if (!c) return; this.close(); setTimeout(() => c.run(), 10); },
        open(prefill) {
            if (!doc.querySelector('.kh-nav')) return; // giriş sayfasında palet yok
            this.build(); this.input.value = prefill || ''; this.sel = 0; this.render();
            this.entry = openOverlay(this.el); setTimeout(() => this.input.focus(), 30);
        },
        close() { if (this.entry) this.entry.close(); this.entry = null; }
    };
    KH.openPalette = (q) => Palette.open(q);

    function showShortcuts() {
        closeMenus();
        let o = doc.getElementById('kh-kbd-help');
        if (!o) {
            o = doc.createElement('div'); o.id = 'kh-kbd-help'; o.className = 'kh-modal-overlay';
            const rows = [
                ['Ctrl / ⌘ + K', KH.t('kbd.palette')], ['/', KH.t('kbd.search')], ['G → D · R · H · A · P · I · C · M · U · Y', KH.t('kbd.goto')],
                ['?', KH.t('kbd.help')], ['Esc', KH.t('kbd.close')]
            ];
            o.innerHTML = '<div class="kh-modal sm" role="dialog" aria-modal="true"><h2>' + KH.icon('keyboard') + KH.esc(KH.t('kbd.title')) + '</h2>' +
                '<div class="kh-modal-body">' + rows.map(r => '<div class="flex items-center justify-between gap-4 text-sm"><span class="kh-dim">' + KH.esc(r[1]) + '</span><span class="kh-mono text-xs">' + r[0].split(' ').map(k => /^[+→·/]$/.test(k) && k !== '/' ? '<span class="kh-muted">' + KH.esc(k) + '</span>' : '<kbd>' + KH.esc(k) + '</kbd>').join(' ') + '</span></div>').join('') + '</div>' +
                '<div class="kh-modal-actions"><button type="button" class="kh-btn subtle" data-action="modal-close">' + KH.esc(KH.t('common.close', 'Kapat')) + '</button></div></div>';
            doc.body.appendChild(o);
        }
        KH.openModal(o);
    }

    // ── Görsel cila: spotlight, sayaç, halka, görünürlük ────────────────
    let spotRaf = 0, spotEv = null;
    doc.addEventListener('pointermove', (e) => {
        if (e.pointerType !== 'mouse' || reducedMotion) return;
        spotEv = e;
        if (spotRaf) return;
        spotRaf = requestAnimationFrame(() => {
            spotRaf = 0;
            const el = spotEv.target.closest && spotEv.target.closest('.kh-card, .kh-stat');
            if (!el) return;
            const r = el.getBoundingClientRect();
            el.style.setProperty('--mx', (spotEv.clientX - r.left) + 'px');
            el.style.setProperty('--my', (spotEv.clientY - r.top) + 'px');
        });
    }, { passive: true });

    /** Sayısal metni 0'dan hedefe akıcı sayar (biçim korunur: "1.234", "%60", "3/10"). */
    KH.countUp = function (el, to, opts) {
        opts = opts || {};
        const text = el.textContent.trim();
        const m = to != null ? null : text.match(/-?[\d.,]+/);
        const target = to != null ? Number(to) : (m ? Number(m[0].replace(/[.,](?=\d{3}\b)/g, '').replace(',', '.')) : NaN);
        if (!isFinite(target) || reducedMotion || target === 0) { if (to != null) el.textContent = (opts.prefix || '') + KH.num(target) + (opts.suffix || ''); return; }
        const prefix = opts.prefix != null ? opts.prefix : (m ? text.slice(0, m.index) : '');
        const suffix = opts.suffix != null ? opts.suffix : (m ? text.slice(m.index + m[0].length) : '');
        const dur = opts.duration || Math.min(1400, 500 + Math.log10(Math.abs(target) + 1) * 300);
        const t0 = performance.now();
        const step = (now) => {
            const k = Math.min(1, (now - t0) / dur);
            const eased = 1 - Math.pow(1 - k, 4);
            el.textContent = prefix + KH.num(Math.round(target * eased)) + suffix;
            if (k < 1) requestAnimationFrame(step);
        };
        requestAnimationFrame(step);
    };
    KH.enhance = function (scope) {
        scope = scope || doc;
        const io = 'IntersectionObserver' in window ? new IntersectionObserver((entries, obs) => {
            entries.forEach(en => {
                if (!en.isIntersecting) return;
                const el = en.target; obs.unobserve(el);
                if (el.hasAttribute('data-countup')) KH.countUp(el);
                if (el.hasAttribute('data-ring')) { const p = Number(el.dataset.ring) || 0; el.style.setProperty('--p', '0'); requestAnimationFrame(() => animateRing(el, p)); }
                if (el.hasAttribute('data-bar')) requestAnimationFrame(() => { el.style.width = el.dataset.bar + '%'; });
                el.classList.add('in');
            });
        }, { threshold: 0.15 }) : null;
        scope.querySelectorAll('[data-countup],[data-ring],[data-bar],.kh-reveal').forEach(el => {
            if (el.dataset.enhanced) return; el.dataset.enhanced = '1';
            if (io) io.observe(el); else { el.classList.add('in'); if (el.dataset.bar) el.style.width = el.dataset.bar + '%'; }
        });
    };
    function animateRing(el, target) {
        if (reducedMotion) { el.style.setProperty('--p', target); return; }
        const t0 = performance.now(), dur = 1200;
        const step = (now) => { const k = Math.min(1, (now - t0) / dur); el.style.setProperty('--p', (target * (1 - Math.pow(1 - k, 3))).toFixed(1)); if (k < 1) requestAnimationFrame(step); };
        requestAnimationFrame(step);
    }

    // ── Sistem durumu (üst bar + yan menü) ──────────────────────────────
    function refreshHealth() {
        const chip = doc.getElementById('nav-health');
        if (!chip) return;
        KH.api('/api/monitor/summary').then(d => {
            const up = d.enginesActiveUp || 0, total = d.enginesActive || 0;
            const cls = total === 0 ? 'unknown' : up === 0 ? 'down' : (up < total / 2 ? 'warn' : 'up');
            chip.querySelector('.kh-dot').className = 'kh-dot pulse ' + cls;
            chip.querySelector('.txt').textContent = up + '/' + total + ' ' + KH.t('shell.engines', 'motor');
            chip.title = KH.tr('Aktif motorlardan çevrimiçi: ', 'Active engines online: ') + up + '/' + total + ' · ' + KH.tr('Planlı tarama: ', 'Scheduled: ') + (d.scheduledActive || 0) + '/' + (d.scheduledTotal || 0) + (d.schedulerBusy ? KH.tr(' · tarama sürüyor', ' · scan running') : '');
            const sd = doc.getElementById('kh-sys-dot'), st = doc.getElementById('kh-sys-txt');
            if (sd) sd.className = 'kh-dot ' + cls;
            if (st) st.textContent = 'Tor · ' + up + '/' + total;
        }).catch(() => { });
    }

    // ── Klavye ──────────────────────────────────────────────────────────
    let gPending = false, gTimer = null;
    const gMap = { d: '/dashboard', r: '/results', h: '/results/graph', a: '/analytics', s: '/search', p: '/scheduled', i: '/watchlist', m: '/monitor', c: '/crawl', u: '/users', y: '/settings' };
    doc.addEventListener('keydown', (e) => {
        if ((e.ctrlKey || e.metaKey) && (e.key === 'k' || e.key === 'K')) { e.preventDefault(); Palette.open(); return; }
        const tag = (e.target.tagName || '').toLowerCase();
        if (tag === 'input' || tag === 'textarea' || tag === 'select' || e.target.isContentEditable || e.ctrlKey || e.metaKey || e.altKey) return;
        if (modalStack.length) return;
        if (e.key === '/') { e.preventDefault(); const q = doc.getElementById('query'); if (q) q.focus(); else location.href = '/search'; return; }
        if (e.key === '?') { e.preventDefault(); showShortcuts(); return; }
        if (e.key === 'g') { gPending = true; clearTimeout(gTimer); gTimer = setTimeout(() => { gPending = false; }, 900); return; }
        if (gPending && gMap[e.key]) { gPending = false; location.href = gMap[e.key]; }
    });

    // ── Başlat ──────────────────────────────────────────────────────────
    doc.addEventListener('DOMContentLoaded', () => {
        doc.querySelectorAll('[data-action="theme-set"]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.value === KH.theme())));
        if (doc.body.dataset.role === 'viewer') doc.body.classList.add('role-viewer');
        KH.enhance();
        if (doc.querySelector('.kh-nav')) { refreshHealth(); setInterval(() => { if (!doc.hidden) refreshHealth(); }, 60000); }
        // Mobil çekmece: bağlantıya tıklayınca kapan
        doc.querySelectorAll('.kh-nav-link').forEach(a => a.addEventListener('click', () => root.classList.remove('nav-open')));
    });
})();
