/**
 * Hesabım: parola değişikliği, iki adımlı doğrulama (2FA) ve kişisel API
 * token'ları (listele, oluştur, tek seferlik göster, iptal et; admin için
 * "tüm kullanıcılar").
 *
 * Düz metin token yalnız oluşturma yanıtında gelir ve yalnız gösterim
 * modalında yaşar: modal kapanınca DOM'dan silinir, başka bir yere (bellek,
 * depolama, URL) yazılmaz. Aynı kural 2FA kurulum sırrı ve kurtarma kodları
 * için de geçerlidir: sihirbaz kapanınca QR, sır ve kodlar DOM'dan silinir.
 */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    const main = () => $('main');

    let tokens = [];
    let showAll = false;

    const validTime = (iso) => !!iso && new Date(iso).getFullYear() > 1971;
    const origin = () => location.origin;
    // Kabuk tırnaklaması: tek tırnak içinde tek tırnak '\'' ile kaçar.
    const sq = (s) => "'" + String(s).replace(/'/g, "'\\''") + "'";
    // Çok satırlı (\ ile devam eden) POSIX kabuk komutu: okunur ve olduğu gibi yapıştırılabilir.
    const curlFor = (token, path) => 'curl -sS \\\n  -H ' + sq('Authorization: Bearer ' + token) + ' \\\n  ' + sq(origin() + path);

    // ── Rozetler ────────────────────────────────────────────────────────
    function scopeBadge(scope) {
        if (scope === 'write') return KH.html`<span class="kh-badge warn">${KH.raw(KH.icon('lightning'))} ${KH.t('acct.m.write')}</span>`;
        if (scope === 'metrics') return KH.html`<span class="kh-badge ok">${KH.raw(KH.icon('analytics'))} ${KH.t('acct.m.metrics')}</span>`;
        return KH.html`<span class="kh-badge info">${KH.raw(KH.icon('eye'))} ${KH.t('acct.m.read')}</span>`;
    }
    function statusBadge(status) {
        if (status === 'revoked') return KH.html`<span class="kh-badge neutral">${T('iptal edildi', 'revoked')}</span>`;
        if (status === 'expired') return KH.html`<span class="kh-badge warn">${T('süresi doldu', 'expired')}</span>`;
        return KH.html`<span class="kh-badge ok"><span class="kh-dot up"></span> ${T('etkin', 'active')}</span>`;
    }
    function timeCell(iso) {
        return validTime(iso) ? KH.html`<span title="${KH.fmtTime(iso)}">${KH.fmtTime(iso).split(' ')[0]}</span>` : KH.html`<span class="kh-muted">—</span>`;
    }
    function lastUsedCell(t) {
        if (!validTime(t.lastUsedAt)) return KH.html`<span class="kh-muted">${T('hiç kullanılmadı', 'never used')}</span>`;
        return KH.html`<span title="${KH.fmtTime(t.lastUsedAt, true)}">${KH.ago(t.lastUsedAt)}</span>${t.lastUsedIp ? KH.html`<span class="acc-sub kh-mono">${t.lastUsedIp}</span>` : ''}`;
    }
    // "Yakında bitiyor" uyarısı: 3 günden az kaldıysa (taze bir 7 günlük token uyarı vermez).
    const SOON_MS = 3 * 86400e3;
    function expiryCell(t) {
        if (!validTime(t.expiresAt)) return KH.html`<span class="kh-muted">${T('süresiz', 'never')}</span>`;
        const date = KH.fmtTime(t.expiresAt).split(' ')[0];
        // İptal edilmiş token'ın gelecekteki bitişi anlamsızdır: soluk tarih, göreli süre yok.
        if (t.status === 'revoked') return KH.html`<span class="kh-muted" title="${KH.fmtTime(t.expiresAt)}">${date}</span>`;
        const soon = t.status === 'active' && new Date(t.expiresAt).getTime() - Date.now() < SOON_MS;
        return KH.html`<span class="${soon ? 'acc-exp-soon' : ''}" title="${KH.fmtTime(t.expiresAt)}">${date}</span><span class="acc-sub">${KH.ago(t.expiresAt)}</span>`;
    }

    // Orta/dar ekranda Bitiş sütunu gizlenir; kısa hâli ad hücresinde gösterilir.
    function expiryMeta(t) {
        if (!validTime(t.expiresAt)) return T('Bitiş: süresiz', 'Expires: never');
        const soon = t.status === 'active' && new Date(t.expiresAt).getTime() - Date.now() < SOON_MS;
        return KH.html`<span class="${soon ? 'acc-exp-soon' : ''}">${T('Bitiş', 'Expires')}: ${KH.fmtTime(t.expiresAt).split(' ')[0]}</span>`;
    }

    // ── Liste ───────────────────────────────────────────────────────────
    function row(t) {
        const active = t.status === 'active';
        const mine = !!t.mine;
        const owner = showAll ? KH.html`<span class="acc-owner${mine ? ' c-primary' : ''}">${KH.raw(KH.icon('user'))}${t.username}</span>` : '';
        const label = T('İptal et', 'Revoke') + ': ' + t.name;
        return KH.html`<tr data-id="${t.id}" class="${active ? '' : 'is-inactive'}">
            <td><div class="acc-tok-name">
                <b title="${t.name}">${t.name}</b>
                <span class="acc-tok-meta"><span class="kh-mono">${t.prefix}…</span>${owner}</span>
                <span class="acc-tok-meta acc-sm-only">${scopeBadge(t.scope)}${statusBadge(t.status)}</span>
                <span class="acc-tok-meta acc-md-only">${expiryMeta(t)}</span>
            </div></td>
            <td class="hide-sm">${scopeBadge(t.scope)}</td>
            <td class="muted nowrap text-sm acc-hide-md">${timeCell(t.createdAt)}</td>
            <td class="nowrap text-sm">${lastUsedCell(t)}</td>
            <td class="nowrap text-sm acc-hide-md">${expiryCell(t)}</td>
            <td class="hide-sm">${statusBadge(t.status)}</td>
            <td class="right nowrap">${active
                ? KH.html`<button type="button" class="kh-btn xs danger" data-action="tok-revoke" data-id="${t.id}" data-name="${t.name}" data-owner="${t.username}" data-mine="${mine ? '1' : '0'}" aria-label="${label}">${KH.raw(KH.icon('trash'))}<span class="acc-hide-md">${T('İptal et', 'Revoke')}</span></button>`
                : ''}</td>
        </tr>`;
    }
    function render() {
        const body = $('tok-body');
        const activeN = tokens.filter(t => t.status === 'active').length;
        $('tok-count').textContent = tokens.length ? activeN + '/' + tokens.length : '0';
        $('tok-count').title = T('etkin / toplam', 'active / total');
        if (!tokens.length) {
            const canCreate = main().dataset.managed === '1';
            KH.render(body, KH.html`<tr><td colspan="7"><div class="kh-empty">
                <div class="icon">${KH.raw(KH.icon('key'))}</div>
                <h3>${showAll ? T('Sistemde API token yok', 'No API tokens in the system') : T('Henüz API token yok', 'No API tokens yet')}</h3>
                <p>${T('SIEM, SOAR ya da betiklerinizin API’yi oturum açmadan kullanması için bir token oluşturun.', 'Create a token so your SIEM, SOAR or scripts can use the API without signing in.')}</p>
                ${canCreate ? KH.html`<button type="button" class="kh-btn primary sm mt-3" data-action="tok-new">${KH.raw(KH.icon('plus'))} ${KH.t('acct.tok.new')}</button>` : ''}
            </div></td></tr>`);
            return;
        }
        // Etkin olanlar önce, sonra en yeni
        const order = { active: 0, expired: 1, revoked: 2 };
        const list = tokens.slice().sort((a, b) => (order[a.status] - order[b.status]) || (b.id - a.id));
        KH.render(body, KH.html`${list.map(row)}`);
    }
    async function load() {
        // 2FA zorunlu ve eksik: API yalnız kayıt uçlarına açık; token listesi istenmez.
        if (main().dataset.need2fa === '1') {
            $('tok-count').textContent = '—';
            KH.render($('tok-body'), KH.html`<tr><td colspan="7"><div class="kh-empty"><div class="icon">${KH.raw(KH.icon('lock'))}</div><p>${KH.t('tfa.need.tokens')}</p></div></td></tr>`);
            return;
        }
        try {
            const d = await KH.api('/api/tokens' + (showAll ? '?all=1' : ''));
            tokens = d.tokens || [];
            render();
        } catch (e) {
            $('tok-count').textContent = '—';
            $('tok-count').title = '';
            KH.render($('tok-body'), KH.html`<tr><td colspan="7"><div class="kh-alert err acc-load-err">${KH.raw(KH.icon('warn'))}<span>${e.message}</span>
                <button type="button" class="kh-btn xs subtle" data-action="tok-reload">${KH.raw(KH.icon('refresh'))} ${T('Tekrar dene', 'Retry')}</button></div></td></tr>`);
        }
    }

    // ── Tek seferlik gösterim ───────────────────────────────────────────
    function reveal(token, returnFocus) {
        const curl = curlFor(token, '/api/results?minCriticality=4&limit=20');
        $('tok-value').value = token;
        $('tok-copy').dataset.copy = token;
        $('tok-curl').textContent = curl;
        $('tok-curl-copy').dataset.copy = curl;
        KH.openModal('token-reveal', { returnFocus });
        setTimeout(() => { $('tok-value').focus(); $('tok-value').select(); }, 80);
    }
    function wipeReveal() {
        $('tok-value').value = '';
        $('tok-copy').dataset.copy = '';
        $('tok-curl').textContent = '';
        $('tok-curl-copy').dataset.copy = '';
    }

    function nameValid(on) {
        const el = $('tok-name');
        el.classList.toggle('is-invalid', !on);
        if (on) el.removeAttribute('aria-invalid'); else el.setAttribute('aria-invalid', 'true');
        $('tok-name-h').classList.toggle('c-err', !on);
    }

    function openCreate() {
        if (main().dataset.managed !== '1' || main().dataset.need2fa === '1') return;
        $('token-form').reset();
        nameValid(true);
        KH.openModal('token-modal');
    }


    // ── İki adımlı doğrulama (2FA) ──────────────────────────────────────
    const tfaSec = () => $('security-2fa');
    let tfaCodes = [];      // yalnız sihirbaz açıkken bellekte
    let tfaAuthMode = '';   // 'disable' | 'regen'

    /** Kartın görünür durumunu günceller (sunucu ilk durumu basar). */
    function tfaApply(st) {
        const sec = tfaSec();
        if (!sec) return;
        if (st.enabled != null) sec.dataset.state = st.enabled ? 'on' : 'off';
        if (st.enabledAt) KH.render($('tfa-enabled-at'), KH.html`<time datetime="${st.enabledAt}">${KH.fmtTime(st.enabledAt)}</time>`);
        if (st.remaining != null) {
            $('tfa-remaining').textContent = String(st.remaining);
            $('tfa-low').hidden = !(sec.dataset.state === 'on' && st.remaining <= 3);
        }
    }

    function tfaStep(n) {
        const modal = $('tfa-modal');
        modal.querySelectorAll('.tfa-step').forEach(el => { el.hidden = el.dataset.step !== String(n); });
        modal.querySelectorAll('#tfa-stepper li').forEach(li => {
            const k = parseInt(li.dataset.step, 10);
            li.classList.toggle('on', k === n);
            li.classList.toggle('done', k < n);
            if (k === n) li.setAttribute('aria-current', 'step'); else li.removeAttribute('aria-current');
        });
        const focus = n === 1 ? modal.querySelector('[data-action="tfa-goto"][data-step="2"]') : n === 2 ? ($('tfa-pw').value ? $('tfa-code') : $('tfa-pw')) : $('tfa-codes-copy');
        if (focus) setTimeout(() => focus.focus(), 60);
    }

    /** Geçersiz alan durumu hem görsel (is-invalid) hem erişilebilir (aria-invalid). */
    function tfaInvalid(el, on) {
        el.classList.toggle('is-invalid', on);
        if (on) el.setAttribute('aria-invalid', 'true'); else el.removeAttribute('aria-invalid');
    }

    function tfaWipe() {
        $('tfa-qr').removeAttribute('src');
        $('tfa-secret').textContent = '';
        $('tfa-secret-copy').dataset.copy = '';
        $('tfa-code').value = '';
        tfaInvalid($('tfa-code'), false);
        $('tfa-pw').value = '';
        tfaInvalid($('tfa-pw'), false);
        $('tfa-codes').textContent = '';
        $('tfa-codes-copy').dataset.copy = '';
        $('tfa-saved').checked = false;
        $('tfa-done').disabled = true;
        tfaCodes = [];
    }

    /** Kurtarma kodlarını sihirbazın 3. adımında gösterir (etkinleştirme ya da yenileme). */
    function tfaShowCodes(codes, mode) {
        tfaCodes = codes.slice();
        const modal = $('tfa-modal');
        modal.querySelector('.tfa-modal').dataset.mode = mode;
        $('tfa-modal-title').textContent = mode === 'codes' ? KH.t('tfa.w.codesTitle') : KH.t('tfa.w.title');
        KH.render($('tfa-codes'), KH.html`${codes.map(c => KH.html`<li>${c}</li>`)}`);
        $('tfa-codes-copy').dataset.copy = codes.join('\n');
        $('tfa-saved').checked = false;
        $('tfa-done').disabled = true;
        if (!modal.classList.contains('open')) KH.openModal('tfa-modal', { returnFocus: $('tfa-regen-btn') });
        tfaStep(3);
    }

    async function tfaStart() {
        const sec = tfaSec();
        if (!sec || sec.dataset.managed !== '1' || sec.dataset.state === 'on') return;
        if (sec.dataset.available !== '1') { KH.toast(KH.t('tfa.unavailable'), 'warn'); return; }
        const btn = $('tfa-start-btn');
        KH.busy(btn, true, btn.dataset.busyText);
        try {
            const d = await KH.api('/api/me/2fa/setup', { method: 'POST', body: {} });
            tfaWipe();
            const modal = $('tfa-modal');
            modal.querySelector('.tfa-modal').dataset.mode = 'enroll';
            $('tfa-modal-title').textContent = KH.t('tfa.w.title');
            $('tfa-qr').src = d.qrDataURI;
            const grouped = String(d.secret).replace(/(.{4})(?=.)/g, '$1 ');
            $('tfa-secret').textContent = grouped;
            $('tfa-secret-copy').dataset.copy = d.secret;
            // Kapanınca odak yeni durumda görünür olan düğmeye döner (etkinleştirme
            // başarılıysa "Başlat" gizlenir; "Kodları yenile" görünür).
            KH.openModal('tfa-modal', { returnFocus: () => tfaSec().dataset.state === 'on' ? $('tfa-regen-btn') : btn });
            tfaStep(1);
        } catch (e) { KH.toast(tfaErr(e), 'err'); }
        finally { KH.busy(btn, false); }
    }

    /** Sunucu hata kodlarını arayüz diline çevirir (API metni iki dillidir). */
    function tfaErr(e) {
        const code = e && e.data && e.data.code;
        switch (code) {
            case 'bad_code': return T('Kod doğrulanamadı. Uygulamadaki güncel kodu girin; reddedilmeye devam ederse telefonunuzun saatini kontrol edin.', 'That code didn’t work. Enter the current code from your app; if it keeps failing, check your phone’s clock.');
            case 'setup_expired': return T('Kurulumun süresi doldu; lütfen yeniden başlatın.', 'The setup has expired; please start again.');
            case 'wrong_credentials': return T('Parola ya da doğrulama kodu hatalı.', 'Wrong password or code.');
            case 'wrong_password': return T('Mevcut parola hatalı.', 'Your current password is incorrect.');
            case 'rate_limited': return T('Çok fazla hatalı deneme. ' + (e.data.retryAfter || 60) + ' sn sonra tekrar deneyin.', 'Too many failed attempts. Try again in ' + (e.data.retryAfter || 60) + ' s.');
            case 'unavailable': return KH.t('tfa.unavailable');
            default: return e.message;
        }
    }

    function tfaAuthOpen(mode, returnFocus) {
        tfaAuthMode = mode;
        $('tfa-auth-form').reset();
        $('tfa-auth-title').textContent = mode === 'disable' ? KH.t('tfa.disable') : KH.t('tfa.regen');
        $('tfa-auth-lead').textContent = mode === 'disable'
            ? KH.t('tfa.a.disableLead') + (tfaSec().dataset.required === '1' ? ' ' + KH.t('tfa.requiredHint') : '')
            : KH.t('tfa.a.regenLead');
        const sub = $('tfa-auth-submit');
        sub.className = 'kh-btn ' + (mode === 'disable' ? 'danger' : 'primary');
        sub.textContent = mode === 'disable' ? KH.t('tfa.disable') : KH.t('tfa.a.regenBtn');
        KH.openModal('tfa-auth', { returnFocus });
    }

    function tfaDownload() {
        if (!tfaCodes.length) return;
        const user = tfaSec().dataset.user || '';
        const lines = [
            T('KeywordHunter — 2FA kurtarma kodları', 'KeywordHunter — 2FA recovery codes'),
            T('Hesap', 'Account') + ': ' + user,
            T('Oluşturma', 'Generated') + ': ' + KH.fmtTime(new Date().toISOString()),
            '',
            ...tfaCodes.map((c, i) => String(i + 1).padStart(2, '0') + '. ' + c),
            '',
            T('Her kod girişte yalnızca bir kez kullanılabilir. Yeni kodlar üretildiğinde bu liste geçersiz olur.', 'Each code can be used to sign in only once. Generating new codes invalidates this list.'),
            ''
        ];
        const blob = new Blob([lines.join('\r\n')], { type: 'text/plain;charset=utf-8' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = 'keywordhunter-recovery-codes-' + user.replace(/[^\w.-]+/g, '_') + '.txt';
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
    }

    KH.act({
        // 2FA
        'tfa-start': () => tfaStart(),
        'tfa-goto': (el) => tfaStep(parseInt(el.dataset.step, 10) || 1),
        'tfa-code-input': (el) => {
            const digits = el.value.replace(/\D+/g, '').slice(0, 6);
            if (el.value !== digits) el.value = digits;
            tfaInvalid(el, false);
            // Parola girilmişse 6. hanede otomatik doğrula.
            if (digits.length === 6 && $('tfa-pw').value && !$('tfa-verify-btn').disabled) $('tfa-verify-btn').click();
        },
        'tfa-pw-input': (el) => tfaInvalid(el, false),
        'tfa-verify': async () => {
            const btn = $('tfa-verify-btn');
            const code = $('tfa-code').value.trim();
            const password = $('tfa-pw').value;
            if (!password) { tfaInvalid($('tfa-pw'), true); $('tfa-pw').focus(); return; }
            if (code.length !== 6) { tfaInvalid($('tfa-code'), true); $('tfa-code').focus(); return; }
            if (btn.disabled) return;
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                const d = await KH.api('/api/me/2fa/enable', { method: 'POST', body: { password, code } });
                $('tfa-pw').value = '';
                tfaApply({ enabled: true, enabledAt: d.enabledAt, remaining: (d.recoveryCodes || []).length });
                // Kurulum sırrı artık gereksiz: kodlara geçmeden sil.
                $('tfa-qr').removeAttribute('src');
                $('tfa-secret').textContent = '';
                $('tfa-secret-copy').dataset.copy = '';
                tfaShowCodes(d.recoveryCodes || [], 'enroll');
                KH.toast(T('İki adımlı doğrulama etkinleştirildi', 'Two-factor authentication enabled'), 'ok');
            } catch (e) {
                const field = e.data && (e.data.code === 'wrong_password' || e.data.code === 'rate_limited') ? $('tfa-pw') : $('tfa-code');
                field.value = '';
                tfaInvalid(field, true);
                field.focus();
                if (e.data && e.data.code === 'setup_expired') tfaStep(1);
                KH.toast(tfaErr(e), 'err');
            } finally { KH.busy(btn, false); }
        },
        'tfa-saved': (el) => { $('tfa-done').disabled = !el.checked; },
        'tfa-download': () => tfaDownload(),
        'tfa-disable': (btn) => tfaAuthOpen('disable', btn),
        'tfa-regen': (btn) => tfaAuthOpen('regen', btn),
        'tfa-auth-submit': async () => {
            const btn = $('tfa-auth-submit');
            const password = $('tfa-auth-pw').value;
            const code = $('tfa-auth-code').value.trim();
            if (!password) { $('tfa-auth-pw').focus(); return; }
            if (!code) { $('tfa-auth-code').focus(); return; }
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                if (tfaAuthMode === 'disable') {
                    const d = await KH.api('/api/me/2fa/disable', { method: 'POST', body: { password, code } });
                    tfaApply({ enabled: false, remaining: 0 });
                    // "Kapat" düğmesi artık gizli: odak görünür "Etkinleştir"e döner.
                    KH.closeModal('tfa-auth', { returnFocus: $('tfa-start-btn') });
                    if (d && d.required) {
                        // Politika gereği zorunlu: konsolun geri kalanı yeniden kayıt bekler.
                        KH.toast(T('2FA kapatıldı. Politika gereği yeniden etkinleştirmeniz gerekiyor.', '2FA turned off. Policy requires you to enable it again.'), 'warn');
                        setTimeout(() => location.reload(), 1200);
                    } else {
                        KH.toast(T('İki adımlı doğrulama kapatıldı', 'Two-factor authentication turned off'), 'ok');
                    }
                } else {
                    const d = await KH.api('/api/me/2fa/recovery-codes', { method: 'POST', body: { password, code } });
                    KH.closeModal('tfa-auth');
                    tfaApply({ remaining: (d.recoveryCodes || []).length });
                    tfaShowCodes(d.recoveryCodes || [], 'codes');
                    KH.toast(T('Yeni kurtarma kodları üretildi; eskiler geçersiz', 'New recovery codes generated; the old ones no longer work'), 'ok');
                }
            } catch (e) {
                KH.toast(tfaErr(e), 'err');
                $('tfa-auth-code').value = '';
                (e.status === 403 ? $('tfa-auth-pw') : $('tfa-auth-code')).focus();
            } finally { KH.busy(btn, false); }
        },

        // Parola
        'acc-strength': (el) => {
            KH.pwMeter($('pw-meter'), $('pw-meter-l'), el.value);
            const c = $('pw-confirm');
            if (c.value) c.classList.toggle('is-invalid', c.value !== el.value);
        },
        'acc-confirm': (el) => el.classList.toggle('is-invalid', !!el.value && el.value !== $('pw-new').value),
        'acc-password': async () => {
            const btn = $('pw-submit');
            const current = $('pw-current').value;
            const next = $('pw-new').value;
            const revokeEl = $('pw-revoke-tokens');
            const revokeTokens = !!(revokeEl && revokeEl.checked);
            const confirm = $('pw-confirm').value;
            if (!current) { KH.toast(T('Mevcut parolanızı girin', 'Enter your current password'), 'warn'); $('pw-current').focus(); return; }
            if (next.length < 8) { KH.toast(T('Yeni parola en az 8 karakter olmalı', 'New password must be at least 8 characters'), 'warn'); $('pw-new').focus(); return; }
            if (next !== confirm) { KH.toast(T('Yeni parolalar eşleşmiyor', 'New passwords do not match'), 'warn'); $('pw-confirm').classList.add('is-invalid'); $('pw-confirm').focus(); return; }
            if (next === current) { KH.toast(T('Yeni parola mevcut paroladan farklı olmalı', 'New password must differ from the current one'), 'warn'); $('pw-new').focus(); return; }
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                const d = await KH.api('/api/me/password', { method: 'POST', body: { current, new: next, revokeTokens } });
                $('acc-pw-form').reset();
                $('pw-confirm').classList.remove('is-invalid');
                KH.pwMeter($('pw-meter'), $('pw-meter-l'), '');
                if (revokeTokens) {
                    const n = (d && d.tokensRevoked) || 0;
                    KH.toast(T('Parola güncellendi; diğer oturumlarınız kapatıldı ve ' + n + ' API token iptal edildi', 'Password updated; your other sessions were signed out and ' + n + ' API token(s) revoked'), 'ok');
                    load();
                } else {
                    KH.toast(T('Parola güncellendi; diğer oturumlarınız kapatıldı. API token’larınız geçerli kalır.', 'Password updated; your other sessions were signed out. Your API tokens remain valid.'), 'ok');
                }
            } catch (e) {
                KH.toast(e.message, 'err');
                if (e.status === 403) { $('pw-current').select(); $('pw-current').focus(); }
            } finally { KH.busy(btn, false); }
        },

        // Token'lar
        'acc-focus-password': () => { $('password').scrollIntoView({ block: 'start' }); setTimeout(() => $('pw-current').focus(), 60); },
        'tok-new': () => openCreate(),
        'tok-select': (el) => el.select(),
        'tok-reload': () => load(),
        'tok-name-input': (el) => { if (el.classList.contains('is-invalid') && el.value.trim()) nameValid(true); },
        'tok-all': (btn) => {
            showAll = btn.getAttribute('aria-checked') !== 'true';
            btn.setAttribute('aria-checked', String(showAll));
            btn.classList.toggle('on', showAll);
            load();
        },
        'tok-create': async () => {
            const btn = $('tok-submit');
            const name = $('tok-name').value.trim();
            const scopeEl = document.querySelector('input[name="tok-scope"]:checked');
            const scope = scopeEl ? scopeEl.value : 'read';
            const expiresInDays = parseInt($('tok-expiry').value, 10) || 0;
            // Satır içi hata (alan + ipucu); toast yok — sonraki gösterim modalının üstünde kalırdı.
            if (!name || [...name].length > 64) { nameValid(false); $('tok-name').focus(); return; }
            nameValid(true);
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                const d = await KH.api('/api/tokens', { method: 'POST', body: { name, scope, expiresInDays } });
                KH.closeModal('token-modal');
                $('token-form').reset();
                // Gösterim kapanınca odak gizli "oluştur" düğmesine değil karttaki "Yeni token"a döner.
                reveal(d.token, $('tok-new-btn'));
                load();
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        },
        'tok-revoke': async (btn) => {
            const name = btn.dataset.name || '';
            const other = btn.dataset.mine !== '1';
            const ok = await KH.confirm({
                title: T('Token’ı iptal et', 'Revoke token'),
                message: (other ? T('“' + btn.dataset.owner + '” kullanıcısına ait ', 'Owned by “' + btn.dataset.owner + '”. ') : '') +
                    T('“' + name + '” token’ı ile yapılan istekler anında reddedilir. Bu işlem geri alınamaz.', 'Requests using “' + name + '” will be rejected immediately. This cannot be undone.'),
                okText: T('İptal et', 'Revoke'), danger: true
            });
            if (!ok) return;
            KH.busy(btn, true);
            try {
                await KH.api('/api/tokens/' + encodeURIComponent(btn.dataset.id) + '/revoke', { method: 'POST', body: {} });
                KH.toast(T('Token iptal edildi', 'Token revoked'), 'ok', 2200);
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { load(); }
        }
    });

    document.addEventListener('DOMContentLoaded', () => {
        const how = curlFor('kh_…', '/api/whoami');
        $('how-curl').textContent = how;
        $('how-copy').dataset.copy = how;

        // Gösterim modalı (data-static: yalnız düğmeyle kapanır) kapandığı an token DOM'dan silinir.
        const rv = $('token-reveal');
        new MutationObserver(() => { if (!rv.classList.contains('open')) wipeReveal(); }).observe(rv, { attributes: true, attributeFilter: ['class'] });

        // 2FA sihirbazı kapandığı an QR, sır ve kurtarma kodları DOM'dan silinir;
        // etkinleştirme sonrası kapanırsa (veya need2fa ise) sayfa yeni durumu yükler.
        const tm = $('tfa-modal');
        if (tm) {
            new MutationObserver(() => {
                if (tm.classList.contains('open')) return;
                const enrolled = tm.querySelector('.tfa-modal').dataset.mode === 'enroll' && tfaCodes.length > 0;
                tfaWipe();
                if (enrolled && main().dataset.need2fa === '1') location.reload();
            }).observe(tm, { attributes: true, attributeFilter: ['class'] });
        }

        load();
        // Komut paleti / kısayol bağlantıları: /account#new-token, /account#password
        // (sayfa zaten açıkken paletten gelinirse yalnız hash değişir).
        const onHash = () => {
            if (location.hash === '#security-2fa') {
                const sec = tfaSec();
                if (sec) { sec.scrollIntoView({ block: 'start' }); if (main().dataset.need2fa === '1') $('tfa-start-btn') && $('tfa-start-btn').focus(); }
            } else if (location.hash === '#new-token') openCreate();
            else if (location.hash === '#password') KH.actions['acc-focus-password']();
        };
        window.addEventListener('hashchange', onHash);
        onHash();
    });
})();
