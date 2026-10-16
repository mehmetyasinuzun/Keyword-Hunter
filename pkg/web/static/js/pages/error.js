/** Hata sayfası: "Geri" geçmiş varsa geri gider, yoksa ana sayfaya bağlantı olarak çalışır. */
(function () {
    'use strict';
    KH.act({
        'history-back': (el, e) => { if (history.length > 1) { e.preventDefault(); history.back(); } else { location.href = el.getAttribute('href'); } }
    });
})();
