/** Kullanıcılar & roller: liste, rol değiştirme, etkin/pasif, parola sıfırlama, silme, yeni kullanıcı. */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;
    const ROLES = ['admin', 'analyst', 'viewer'];
    const roleLabel = (r) => KH.t('role.' + r, r);

    let users = [];
    let me = '';

    const isSelf = (u) => u.username.toLocaleLowerCase('en') === me.toLocaleLowerCase('en');
    const validTime = (iso) => !!iso && new Date(iso).getFullYear() > 1971;
    function initials(name) {
        const s = String(name || '').trim();
        return (s ? s.slice(0, 2) : '?').toLocaleUpperCase(KH.lang === 'en' ? 'en' : 'tr');
    }

    // ── Parola gücü (0–4) ───────────────────────────────────────────────
    function strength(v) {
        if (!v) return 0;
        let s = 0;
        if (v.length >= 8) s++;
        if (v.length >= 12) s++;
        if (/[a-zçğıöşü]/.test(v) && /[A-ZÇĞİÖŞÜ]/.test(v)) s++;
        if (/\d/.test(v)) s++;
        if (/[^\p{L}\p{N}]/u.test(v)) s++;
        if (v.length < 8) return 1;
        return Math.max(1, Math.min(4, s - 1));
    }
    function paintMeter(meter, label, v) {
        const lv = strength(v);
        meter.dataset.level = String(lv);
        const names = [T('En az 8 karakter; harf, rakam ve sembol karıştırın.', 'At least 8 characters; mix letters, digits and symbols.'),
            v.length < 8 ? T('Çok kısa — en az 8 karakter', 'Too short — at least 8 characters') : T('Zayıf', 'Weak'),
            T('Orta', 'Fair'), T('Güçlü', 'Strong'), T('Çok güçlü', 'Very strong')];
        label.textContent = names[lv];
        label.className = 'kh-hint pw-label lv-' + lv;
    }

    // ── Liste ───────────────────────────────────────────────────────────
    function row(u) {
        const self = isSelf(u);
        const lock = T('Kendi hesabınız', 'Your own account');
        const opts = ROLES.map(r => KH.html`<option value="${r}"${KH.raw(u.role === r ? ' selected' : '')}>${roleLabel(r)}</option>`);
        const last = validTime(u.lastLoginAt) ? KH.html`<span title="${KH.fmtTime(u.lastLoginAt)}">${KH.ago(u.lastLoginAt)}</span>` : KH.html`<span class="kh-muted">${T('hiç', 'never')}</span>`;
        return KH.html`<tr data-id="${u.id}" class="${(self ? 'is-self ' : '') + (u.enabled ? '' : 'is-disabled')}">
            <td><div class="u-cell">
                <span class="u-av r-${u.role}" aria-hidden="true">${initials(u.username)}</span>
                <div class="min-w-0"><div class="u-name"><span class="kh-truncate" title="${u.username}">${u.username}</span>${self ? KH.html`<span class="kh-badge info">${T('siz', 'you')}</span>` : ''}</div>
                <div class="u-sub kh-mono">#${u.id}</div></div>
            </div></td>
            <td><select class="kh-select sm u-role-sel r-${u.role}" data-change="u-role" data-id="${u.id}" data-name="${u.username}" data-prev="${u.role}" aria-label="${T('Rol', 'Role')}: ${u.username}"${KH.raw(self ? ' disabled title="' + KH.esc(T('Kendi rolünüzü değiştiremezsiniz', 'You cannot change your own role')) + '"' : '')}>${opts}</select></td>
            <td>${u.enabled ? KH.html`<span class="kh-badge ok"><span class="kh-dot up"></span> ${T('etkin', 'active')}</span>` : KH.html`<span class="kh-badge neutral">${KH.raw(KH.icon('pause'))} ${T('pasif', 'disabled')}</span>`}</td>
            <td class="muted nowrap text-sm">${last}</td>
            <td class="muted nowrap kh-mono text-xs hide-sm">${KH.fmtTime(u.createdAt)}</td>
            <td class="right nowrap"><div class="kh-rowactions u-actions">
                <button type="button" class="kh-btn xs icon subtle" data-action="u-pass" data-id="${u.id}" data-name="${u.username}" aria-label="${T('Parolayı sıfırla', 'Reset password')}: ${u.username}" data-tip="${T('Parola sıfırla', 'Reset password')}">${KH.raw(KH.icon('key'))}</button>
                <button type="button" class="kh-btn xs icon ${u.enabled ? 'warn' : 'success'}" data-action="u-toggle" data-id="${u.id}" data-name="${u.username}" data-enable="${u.enabled ? '0' : '1'}" aria-label="${u.enabled ? T('Pasifleştir', 'Disable') : T('Etkinleştir', 'Enable')}: ${u.username}" data-tip="${self ? lock : (u.enabled ? T('Pasifleştir', 'Disable') : T('Etkinleştir', 'Enable'))}"${KH.raw(self ? ' disabled' : '')}>${KH.raw(KH.icon(u.enabled ? 'pause' : 'play'))}</button>
                <button type="button" class="kh-btn xs icon danger" data-action="u-delete" data-id="${u.id}" data-name="${u.username}" aria-label="${T('Sil', 'Delete')}: ${u.username}" data-tip="${self ? lock : T('Sil', 'Delete')}"${KH.raw(self ? ' disabled' : '')}>${KH.raw(KH.icon('trash'))}</button>
            </div></td>
        </tr>`;
    }
    function render() {
        const body = $('users-body');
        const f = $('u-filter').value.trim().toLocaleLowerCase(KH.lang);
        const list = users.filter(u => !f || (u.username + ' ' + roleLabel(u.role)).toLocaleLowerCase(KH.lang).includes(f));
        $('u-count').textContent = list.length === users.length ? String(users.length) : list.length + '/' + users.length;
        ROLES.forEach(r => {
            const el = $('c-' + r);
            const first = el.textContent === '—';
            el.textContent = String(users.filter(u => u.role === r).length);
            if (first) KH.countUp(el);
        });
        if (!list.length) {
            KH.render(body, KH.html`<tr><td colspan="6"><div class="kh-empty"><div class="icon">${KH.raw(KH.icon('user'))}</div><h3>${users.length ? T('Eşleşen kullanıcı yok', 'No matching users') : T('Kullanıcı yok', 'No users')}</h3></div></td></tr>`);
            return;
        }
        // Kendi hesabınız en üstte, sonra ada göre
        list.sort((a, b) => (isSelf(b) - isSelf(a)) || a.username.localeCompare(b.username, KH.lang));
        KH.render(body, KH.html`${list.map(row)}`);
    }
    async function load() {
        try {
            const d = await KH.api('/api/users');
            me = d.me || '';
            users = d.users || [];
            render();
        } catch (e) {
            KH.render($('users-body'), KH.html`<tr><td colspan="6"><div class="kh-alert err">${KH.raw(KH.icon('warn'))}<span>${e.message}</span></div></td></tr>`);
        }
    }
    async function update(id, body) { return KH.api('/api/users/' + encodeURIComponent(id), { method: 'POST', body }); }

    KH.act({
        'u-new': () => {
            $('user-form').reset();
            paintMeter($('u-meter'), $('u-meter-l'), '');
            KH.openModal('user-modal');
        },
        'u-filter': KH.debounce(render, 120),
        'u-strength': (el) => paintMeter($('u-meter'), $('u-meter-l'), el.value),
        'u-eye': (btn) => {
            const input = $(btn.dataset.target);
            const show = input.type === 'password';
            input.type = show ? 'text' : 'password';
            btn.setAttribute('aria-pressed', String(show));
            input.focus();
        },

        'u-create': async () => {
            const btn = $('u-submit');
            const username = $('u-name').value.trim();
            const password = $('u-pass').value;
            const roleEl = document.querySelector('input[name="u-role"]:checked');
            if (username.length < 3 || /\s/.test(username)) { KH.toast(T('Kullanıcı adı en az 3 karakter olmalı ve boşluk içermemeli', 'Username must be at least 3 characters with no spaces'), 'warn'); $('u-name').focus(); return; }
            if (password.length < 8) { KH.toast(T('Parola en az 8 karakter olmalı', 'Password must be at least 8 characters'), 'warn'); $('u-pass').focus(); return; }
            KH.busy(btn, true, btn.dataset.busyText);
            try {
                await KH.api('/api/users', { method: 'POST', body: { username, password, role: roleEl ? roleEl.value : 'analyst' } });
                KH.toast(T('Kullanıcı oluşturuldu: ', 'User created: ') + username, 'ok');
                KH.closeModal('user-modal');
                $('user-form').reset();
                await load();
                const tr = Array.from($('users-body').querySelectorAll('tr')).find(r => r.querySelector('.u-name .kh-truncate') && r.querySelector('.u-name .kh-truncate').textContent === username);
                if (tr) tr.classList.add('kh-flash');
            } catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); }
        },

        'u-role': async (sel) => {
            const prev = sel.dataset.prev;
            const role = sel.value;
            if (role === prev) return;
            if (role === 'admin' || prev === 'admin') {
                const ok = await KH.confirm({
                    title: T('Rolü değiştir', 'Change role'),
                    message: T('“' + sel.dataset.name + '” kullanıcısının rolü ' + roleLabel(prev) + ' → ' + roleLabel(role) + ' olacak. Yeni yetkiler anında geçerli olur.', 'Role of “' + sel.dataset.name + '” will change ' + roleLabel(prev) + ' → ' + roleLabel(role) + '. New permissions apply immediately.'),
                    okText: T('Değiştir', 'Change'), danger: role === 'admin'
                });
                if (!ok) { sel.value = prev; return; }
            }
            sel.disabled = true;
            try { await update(sel.dataset.id, { role }); KH.toast(T('Rol güncellendi', 'Role updated'), 'ok', 2200); }
            catch (e) { KH.toast(e.message, 'err'); }
            finally { load(); }
        },

        'u-toggle': async (btn) => {
            const enabled = btn.dataset.enable === '1';
            if (!enabled) {
                const ok = await KH.confirm({ title: T('Kullanıcıyı pasifleştir', 'Disable user'), message: T('“' + btn.dataset.name + '” artık giriş yapamayacak.', '“' + btn.dataset.name + '” will no longer be able to sign in.'), okText: T('Pasifleştir', 'Disable'), danger: true });
                if (!ok) return;
            }
            KH.busy(btn, true);
            try { await update(btn.dataset.id, { enabled }); KH.toast(enabled ? T('Etkinleştirildi', 'Enabled') : T('Pasifleştirildi', 'Disabled'), 'ok', 2200); }
            catch (e) { KH.toast(e.message, 'err'); }
            finally { KH.busy(btn, false); load(); }
        },

        'u-pass': async (btn) => {
            const name = btn.dataset.name || '';
            const p = await KH.prompt({
                title: T('Parolayı sıfırla', 'Reset password'), icon: 'key', type: 'password', maxLength: 256,
                label: T('“' + name + '” için yeni parola', 'New password for “' + name + '”'),
                hint: T('En az 8 karakter. Kullanıcının açık oturumları kapatılır.', 'At least 8 characters. The user’s open sessions are signed out.'),
                okText: T('Parolayı güncelle', 'Update password')
            });
            if (p == null || p === '') return;
            if (p.length < 8) { KH.toast(T('Parola en az 8 karakter olmalı', 'Password must be at least 8 characters'), 'warn'); return; }
            try { await update(btn.dataset.id, { password: p }); KH.toast(T('Parola güncellendi; kullanıcının oturumları kapatıldı', 'Password updated; the user’s sessions were signed out'), 'ok'); }
            catch (e) { KH.toast(e.message, 'err'); }
        },

        'u-delete': async (btn) => {
            const name = btn.dataset.name || '';
            const ok = await KH.confirm({ title: T('Kullanıcıyı sil', 'Delete user'), message: T('“' + name + '” ve tüm oturumları kalıcı olarak silinecek.', '“' + name + '” and all of their sessions will be permanently deleted.'), okText: T('Sil', 'Delete'), danger: true });
            if (!ok) return;
            try { await KH.api('/api/users/' + encodeURIComponent(btn.dataset.id) + '/delete', { method: 'POST', body: {} }); KH.toast(T('Silindi', 'Deleted'), 'ok', 2200); load(); }
            catch (e) { KH.toast(e.message, 'err'); }
        }
    });

    document.addEventListener('DOMContentLoaded', load);
})();
