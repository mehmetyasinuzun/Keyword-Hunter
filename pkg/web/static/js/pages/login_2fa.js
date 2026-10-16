/**
 * Girişin ikinci adımı (2FA): 6 haneli kod girilince otomatik gönderim,
 * kilit geri sayımı ve bekleyen girişin kalan süresi. Sayfa JS olmadan da
 * çalışır (form + bağlantılar); bu dosya yalnız kolaylık ekler.
 */
(function () {
    'use strict';
    let sent = false;
    function submit(form) {
        if (sent) return;
        sent = true;
        const btn = document.getElementById('tfa-btn');
        KH.busy(btn, true, btn.dataset.busyText);
        form.submit();
    }

    KH.act({
        // Yalnız rakam (yapıştırılan "123 456" dahil); 6 hane tamamlanınca gönder.
        'otp-input': (el) => {
            const digits = el.value.replace(/\D+/g, '').slice(0, 6);
            if (el.value !== digits) el.value = digits;
            const btn = document.getElementById('tfa-btn');
            if (digits.length === 6 && !btn.disabled) submit(el.form);
        }
    });

    document.addEventListener('DOMContentLoaded', () => {
        const form = document.getElementById('tfa-form');
        const btn = document.getElementById('tfa-btn');
        form.addEventListener('submit', (e) => {
            const code = document.getElementById('code');
            if (!code.value.trim()) { e.preventDefault(); code.focus(); return; }
            if (sent) { e.preventDefault(); return; }
            sent = true;
            KH.busy(btn, true, btn.dataset.busyText);
        });

        // IP kilidi: süre dolunca aynı adıma dön (bekleyen giriş hâlâ geçerliyse).
        const w = document.getElementById('lock-wait');
        if (w) {
            let n = parseInt(w.dataset.wait, 10) || 0;
            const t = setInterval(() => {
                n--;
                if (n <= 0) {
                    clearInterval(t);
                    const recovery = new URLSearchParams(location.search).get('mode') === 'recovery';
                    location.replace('/login/2fa' + (recovery ? '?mode=recovery' : ''));
                }
                else w.textContent = n;
            }, 1000);
        }

        // Bekleyen girişin kalan süresi (m:ss); dolunca sunucu yeniden girişe yönlendirir.
        const ex = document.getElementById('tfa-expiry');
        if (!ex) return;
        let left = parseInt(ex.dataset.expires, 10) || 0;
        const fmt = (s) => Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0');
        ex.textContent = fmt(Math.max(0, left));
        const timer = setInterval(() => {
            left--;
            if (left <= 0) { clearInterval(timer); location.replace('/login/2fa'); return; }
            ex.textContent = fmt(left);
            ex.classList.toggle('c-warn', left <= 60);
        }, 1000);
    });
})();
