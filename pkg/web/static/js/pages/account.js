/**
 * Hesabım: parola değişikliği + kişisel API token'ları (listele, oluştur,
 * tek seferlik göster, iptal et; admin için "tüm kullanıcılar").
 *
 * Düz metin token yalnız oluşturma yanıtında gelir ve yalnız gösterim
 * modalında yaşar: modal kapanınca DOM'dan silinir, başka bir yere (bellek,
 * depolama, URL) yazılmaz.
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
        return scope === 'write'
            ? KH.html`<span class="kh-badge warn">${KH.raw(KH.icon('lightning'))} ${KH.t('acct.m.write')}</span>`
            : KH.html`<span class="kh-badge info">${KH.raw(KH.icon('eye'))} ${KH.t('acct.m.read')}</span>`;
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
        if (main().dataset.managed !== '1') return;
        $('token-form').reset();
        nameValid(true);
        KH.openModal('token-modal');
    }

    KH.act({
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

        load();
        // Komut paleti / kısayol bağlantıları: /account#new-token, /account#password
        // (sayfa zaten açıkken paletten gelinirse yalnız hash değişir).
        const onHash = () => {
            if (location.hash === '#new-token') openCreate();
            else if (location.hash === '#password') KH.actions['acc-focus-password']();
        };
        window.addEventListener('hashchange', onHash);
        onHash();
    });
})();
