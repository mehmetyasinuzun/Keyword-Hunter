/** Ayarlar: kimlik, web/oturum, altyapı (.env), site profilleri, Tor devresi. Yalnız admin. */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    let snapshot = '';

    // ── Parola gücü (0–4) ───────────────────────────────────────────────
    function strength(v) {
        if (!v) return 0;
        if (v.length < 8) return 1;
        let s = 0;
        if (v.length >= 12) s++;
        if (v.length >= 16) s++;
        if (/[a-zçğıöşü]/.test(v) && /[A-ZÇĞİÖŞÜ]/.test(v)) s++;
        if (/\d/.test(v)) s++;
        if (/[^\p{L}\p{N}]/u.test(v)) s++;
        return Math.max(1, Math.min(4, s));
    }
    function paintMeter() {
        const v = $('admin-pass').value;
        const lv = strength(v);
        $('pw-meter').dataset.level = String(lv);
        const hint = $('pw-hint');
        const names = ['', T('Çok kısa — en az 8 karakter', 'Too short — at least 8 characters'), T('Orta — uzatın ya da sembol ekleyin', 'Fair — make it longer or add symbols'), T('Güçlü', 'Strong'), T('Çok güçlü', 'Very strong')];
        hint.textContent = lv ? names[lv] : T('Boş bırakırsanız mevcut parola korunur. Parola yalnızca bcrypt hash olarak saklanır; değişince diğer oturumlar kapanır.', 'Leave empty to keep the current password. It is stored only as a bcrypt hash; other sessions are signed out when it changes.');
        hint.className = 'kh-hint pw-label lv-' + lv;
    }

    // ── Form ↔ veri ─────────────────────────────────────────────────────
    function payload() {
        return {
            adminUser: $('admin-user').value.trim(),
            adminPass: $('admin-pass').value,
            torProxy: $('tor-proxy').value.trim(),
            dbPath: $('db-path').value.trim(),
            webAddr: $('web-addr').value.trim(),
            logDir: $('log-dir').value.trim(),
            sessionTtlHours: Number($('session-ttl').value),
            rateLimitRps: Number($('rate-rps').value),
            rateLimitBurst: Number($('rate-burst').value),
            secureCookies: $('secure-cookies').getAttribute('aria-checked') === 'true'
        };
    }
    function paintDirty() {
        const dirty = !!snapshot && JSON.stringify(payload()) !== snapshot;
        $('st-savebar').classList.toggle('is-dirty', dirty);
        $('st-dirty-dot').className = 'kh-dot ' + (dirty ? 'warn pulse' : 'unknown');
        $('st-dirty-t').textContent = dirty ? T('Kaydedilmemiş değişiklikler var', 'You have unsaved changes') : T('Tüm değişiklikler kaydedildi', 'All changes saved');
    }
    function fill(s) {
        $('admin-user').value = s.adminUser || '';
        $('admin-pass').value = '';
        $('tor-proxy').value = s.torProxy || '';
        $('db-path').value = s.dbPath || '';
        $('web-addr').value = s.webAddr || '';
        $('log-dir').value = s.logDir || '';
        $('session-ttl').value = s.sessionTtlHours || 24;
        $('rate-rps').value = s.rateLimitRps || 25;
        $('rate-burst').value = s.rateLimitBurst || 80;
        $('secure-cookies').setAttribute('aria-checked', String(!!s.secureCookies));
        $('env-path').textContent = s.envFilePath || '.env';
        $('env-path').title = s.envFilePath || '.env';
        $('env-copy').dataset.copy = s.envFilePath || '.env';
        if (s.version) $('ver').textContent = 'v' + s.version;

        const bcrypt = s.passwordStorage === 'bcrypt';
        const ps = $('pw-storage');
        ps.hidden = false;
        ps.className = 'kh-badge ' + (bcrypt ? 'ok' : 'warn');
        KH.render(ps, KH.html`${KH.raw(KH.icon(bcrypt ? 'shield' : 'warn'))} ${bcrypt ? T('parola: bcrypt', 'password: bcrypt') : T('parola: düz metin', 'password: plaintext')}`);
        ps.title = bcrypt ? T('Yönetici parolası hash olarak saklanıyor', 'Admin password is stored as a hash') : T('Parola .env içinde düz metin; yeni parola belirlerseniz hash’e dönüşür', 'Password is plaintext in .env; setting a new one converts it to a hash');
        $('pw-warn').hidden = bcrypt;

        paintMeter();
        snapshot = JSON.stringify(payload());
        paintDirty();
    }
    async function load() {
        const d = await KH.api('/api/settings/env');
        fill(d.settings || {});
    }
    function showAlert(type, title, lines) {
        const a = $('settings-alert');
        a.hidden = false;
        KH.render(a, KH.html`<div class="kh-alert ${type}">${KH.raw(KH.icon(type === 'err' ? 'warn' : type === 'ok' ? 'check' : 'info'))}<div><strong>${title}</strong>${lines.map(l => KH.html`<div class="st-alert-line">${l}</div>`)}</div></div>`);
    }
    function validate(p) {
        const bad = (id, tr, en) => { $(id).focus(); KH.toast(T(tr, en), 'warn'); return false; };
        if (!p.adminUser || /\s/.test(p.adminUser)) return bad('admin-user', 'Kullanıcı adı boş olamaz ve boşluk içeremez', 'Username cannot be empty or contain spaces');
        if (p.adminPass && p.adminPass.length < 8) return bad('admin-pass', 'Yeni parola en az 8 karakter olmalı', 'New password must be at least 8 characters');
        if (!(p.sessionTtlHours >= 1 && p.sessionTtlHours <= 720)) return bad('session-ttl', 'Oturum süresi 1–720 saat olmalı', 'Session TTL must be 1–720 hours');
        if (!(p.rateLimitRps >= 1 && p.rateLimitRps <= 200)) return bad('rate-rps', 'Hız limiti 1–200 olmalı', 'Rate limit must be 1–200');
        if (!(p.rateLimitBurst >= 1 && p.rateLimitBurst <= 500)) return bad('rate-burst', 'Burst 1–500 olmalı', 'Burst must be 1–500');
        if (!/^[^\s:]+:\d{1,5}$/.test(p.torProxy) && !/^\[[^\]]+\]:\d{1,5}$/.test(p.torProxy)) return bad('tor-proxy', 'Tor proxy host:port biçiminde olmalı (örn. 127.0.0.1:9150)', 'Tor proxy must be host:port (e.g. 127.0.0.1:9150)');
        if (!p.webAddr || !p.dbPath || !p.logDir) return bad(!p.webAddr ? 'web-addr' : !p.dbPath ? 'db-path' : 'log-dir', 'Altyapı alanları boş bırakılamaz', 'Infrastructure fields cannot be empty');
        return true;
    }

    // ── Site profilleri ─────────────────────────────────────────────────
    function profileRow(p) {
        return KH.html`<tr>
            <td class="kh-mono text-xs break-all">${p.host}</td>
            <td>${p.hasCookies ? KH.html`<span class="kh-badge ok mono" title="${p.cookiePeek}">${KH.raw(KH.icon('check'))} ${KH.short(p.cookiePeek, 28)}</span>` : KH.html`<span class="kh-muted">—</span>`}</td>
            <td class="center">${p.renderJs ? KH.html`<span class="kh-badge info">JS</span>` : KH.html`<span class="kh-muted">—</span>`}</td>
            <td class="kh-muted text-xs hide-sm" title="${p.userAgent || ''}">${KH.short(p.userAgent || '—', 28)}</td>
            <td class="muted nowrap text-xs" title="${KH.fmtTime(p.updatedAt)}">${KH.ago(p.updatedAt)}</td>
            <td class="right nowrap"><div class="kh-rowactions">
                <button type="button" class="kh-btn xs icon subtle" data-action="sp-edit" data-host="${p.host}" data-render="${p.renderJs ? '1' : '0'}" aria-label="${T('Düzenle', 'Edit')}: ${p.host}" data-tip="${T('Düzenle', 'Edit')}">${KH.raw(KH.icon('edit'))}</button>
                <button type="button" class="kh-btn xs icon danger" data-action="sp-delete" data-host="${p.host}" aria-label="${T('Sil', 'Delete')}: ${p.host}" data-tip="${T('Sil', 'Delete')}">${KH.raw(KH.icon('trash'))}</button>
            </div></td>
        </tr>`;
    }
    async function loadProfiles() {
        const body = $('sp-body');
        try {
            const d = await KH.api('/api/site-profiles');
            const rh = $('render-hint');
            rh.className = 'kh-badge st-apply ' + (d.renderAvailable ? 'ok' : 'neutral');
            rh.textContent = d.renderAvailable ? T('JS render etkin', 'JS render available') : T('JS render için Chromium gerekli', 'JS render needs Chromium');
            rh.title = d.renderAvailable ? '' : T('Docker imajında hazır gelir', 'Bundled in the Docker image');
            const ps = d.profiles || [];
            KH.render(body, ps.length ? KH.html`${ps.map(profileRow)}` : KH.html`<tr><td colspan="6"><div class="kh-empty p-4"><div class="icon">${KH.raw(KH.icon('cookie'))}</div><p>${T('Henüz site profili yok.', 'No site profiles yet.')}</p></div></td></tr>`);
        } catch (e) {
            KH.render(body, KH.html`<tr><td colspan="6"><div class="kh-alert err">${KH.raw(KH.icon('warn'))}<span>${e.message}</span></div></td></tr>`);
        }
    }
    function clearProfileForm() {
        $('sp-form').reset();
        $('sp-render').setAttribute('aria-checked', 'false');
    }

    // ── Tor ─────────────────────────────────────────────────────────────
    let torTimer = 0;
    async function loadTor() {
        clearTimeout(torTimer);
        const st = $('tor-status'), btn = $('newnym-btn'), dot = $('tor-dot'), addr = $('tor-addr');
        try {
            const d = await KH.api('/api/tor/status');
            if (!d.configured) {
                st.textContent = T('Tor kontrol portu yapılandırılmamış (TOR_CONTROL)', 'Tor control port not configured (TOR_CONTROL)');
                dot.className = 'kh-dot unknown'; btn.disabled = true; addr.hidden = true;
                return;
            }
            addr.hidden = false; addr.textContent = d.addr || '';
            if (d.canRenew) {
                st.textContent = d.lastRenew && new Date(d.lastRenew).getFullYear() > 1971 ? T('Hazır · son yenileme ', 'Ready · last renewed ') + KH.ago(d.lastRenew) : T('Hazır', 'Ready');
                dot.className = 'kh-dot up'; btn.disabled = false;
            } else {
                st.textContent = T(d.waitSeconds + ' sn sonra tekrar istenebilir', 'Can renew again in ' + d.waitSeconds + ' s');
                dot.className = 'kh-dot warn'; btn.disabled = true;
                torTimer = setTimeout(loadTor, Math.max(1, Math.min(d.waitSeconds, 30)) * 1000);
            }
            if (d.lastError) { st.textContent += T(' · hata: ', ' · error: ') + d.lastError; dot.className = 'kh-dot down'; }
        } catch (_) {
            st.textContent = T('Durum alınamadı', 'Status unavailable'); dot.className = 'kh-dot down'; btn.disabled = true;
        }
    }

    KH.act({
        'st-dirty': () => setTimeout(paintDirty, 0), // toggle-switch önce çalışsın
        'st-strength': paintMeter,
        'st-eye': (btn) => {
            const input = $('admin-pass');
            const show = input.type === 'password';
            input.type = show ? 'text' : 'password';
            btn.setAttribute('aria-pressed', String(show));
            input.focus();
        },
        'st-reload': async () => {
            try { await load(); $('settings-alert').hidden = true; KH.toast(T('Ayarlar yeniden yüklendi', 'Settings reloaded'), 'ok', 2000); }
            catch (e) { showAlert('err', T('Yüklenemedi', 'Could not load'), [e.message]); }
        },
        'st-save': async () => {
            const p = payload();
            if (!validate(p)) return;
            const btn = $('save-btn');
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                const d = await KH.api('/api/settings/env', { method: 'POST', body: p });
                showAlert('ok', T('Ayarlar kaydedildi.', 'Settings saved.'), [
                    T('Anında uygulanan: ', 'Applied now: ') + (d.appliedRuntime || []).join(', '),
                    T('Yeniden başlatma gerektiren: ', 'Requires restart: ') + (d.requiresRestart || []).join(', ')
                ]);
                KH.toast(T('Ayarlar kaydedildi', 'Settings saved'), 'ok');
                await load();
            } catch (e) {
                showAlert('err', T('Kaydedilemedi', 'Could not save'), [e.message]);
                KH.toast(e.message, 'err');
            } finally { KH.busy(btn, false); paintDirty(); }
        },

        'sp-save': async () => {
            const host = $('sp-host').value.trim();
            if (!host || host.indexOf('.') < 0) { KH.toast(T('Geçerli bir host girin (örn. abcd…xyz.onion)', 'Enter a valid host (e.g. abcd…xyz.onion)'), 'warn'); $('sp-host').focus(); return; }
            const btn = $('sp-save');
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                await KH.api('/api/site-profiles', { method: 'POST', body: { host, cookies: $('sp-cookies').value.trim(), userAgent: $('sp-ua').value.trim(), renderJs: $('sp-render').getAttribute('aria-checked') === 'true' } });
                KH.toast(T('Profil kaydedildi', 'Profile saved'), 'ok');
                clearProfileForm();
                loadProfiles();
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        },
        'sp-clear': clearProfileForm,
        'sp-edit': (btn) => {
            clearProfileForm();
            $('sp-host').value = btn.dataset.host || '';
            $('sp-render').setAttribute('aria-checked', String(btn.dataset.render === '1'));
            $('sec-profiles').scrollIntoView({ behavior: 'smooth', block: 'start' });
            setTimeout(() => $('sp-cookies').focus(), 300);
            KH.toast(T('Çerez ve User-Agent gizli tutulur; değiştirmek için yeniden girip kaydedin', 'Cookie and User-Agent are kept secret; re-enter them and save to change'), 'info');
        },
        'sp-delete': async (btn) => {
            const host = btn.dataset.host || '';
            const ok = await KH.confirm({ title: T('Profili sil', 'Delete profile'), message: T(host + ' profili ve kayıtlı çerezleri silinecek.', 'The profile and stored cookies for ' + host + ' will be deleted.'), okText: T('Sil', 'Delete'), danger: true });
            if (!ok) return;
            try { await KH.api('/api/site-profiles/delete', { method: 'POST', body: { host } }); KH.toast(T('Silindi', 'Deleted'), 'ok', 2000); loadProfiles(); }
            catch (e) { KH.toast(e.message, 'err'); }
        },

        'tor-newnym': async (btn) => {
            KH.busy(btn, true, btn.dataset.busyText);
            try { await KH.api('/api/tor/newnym', { method: 'POST', body: { force: false } }); KH.toast(T('Yeni Tor devresi istendi', 'New Tor circuit requested'), 'ok'); }
            catch (e) { KH.toast(e.message, 'warn'); }
            finally { KH.busy(btn, false); loadTor(); }
        }
    });

    document.addEventListener('DOMContentLoaded', () => {
        load().catch(e => showAlert('err', T('Ayarlar yüklenemedi', 'Could not load settings'), [e.message]));
        loadProfiles();
        loadTor();
        window.addEventListener('beforeunload', (e) => {
            if ($('st-savebar').classList.contains('is-dirty')) { e.preventDefault(); e.returnValue = ''; }
        });
    });
})();
