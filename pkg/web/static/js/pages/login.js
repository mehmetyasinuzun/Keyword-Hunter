/** Giriş: parola göster/gizle, Caps Lock uyarısı, kilit geri sayımı, gönderim durumu. */
(function () {
    'use strict';
    KH.act({
        'toggle-pw': (btn) => {
            const input = document.getElementById('password');
            const show = input.type === 'password';
            input.type = show ? 'text' : 'password';
            btn.setAttribute('aria-pressed', String(show));
            input.focus();
        }
    });
    document.addEventListener('DOMContentLoaded', () => {
        const pw = document.getElementById('password');
        const caps = document.getElementById('caps-warn');
        const onKey = (e) => { if (e.getModifierState) caps.hidden = !e.getModifierState('CapsLock'); };
        pw.addEventListener('keyup', onKey);
        pw.addEventListener('keydown', onKey);

        const form = document.getElementById('login-form');
        const btn = document.getElementById('login-btn');
        form.addEventListener('submit', () => { KH.busy(btn, true, btn.dataset.busyText); });

        const w = document.getElementById('lock-wait');
        if (w) {
            let n = parseInt(w.dataset.wait, 10) || 0;
            btn.disabled = true;
            const t = setInterval(() => {
                n--;
                if (n <= 0) { clearInterval(t); location.href = '/login'; } else { w.textContent = n; }
            }, 1000);
        }
    });
})();
