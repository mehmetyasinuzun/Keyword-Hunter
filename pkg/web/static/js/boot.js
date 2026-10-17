/**
 * KeywordHunter — erken önyükleme (tüm sayfalar, <head> içinde senkron).
 *
 * app.js "defer" ile yüklenir; bu dosya ise ayrıştırma sırasında, sayfa
 * gösterilmeden önce çalışması gereken küçük işleri yapar. Kısa ve bağımlılıksız
 * kalmalıdır: sayfa ilk boyamayı bu betik için bekler.
 *
 * Sayfalar arası görünüm geçişi (kh.css: @view-transition { navigation: auto })
 * gelen sayfa gösterilmeden önce kullanıcı girdisiyle (ör. hızlı Ctrl+K)
 * kesilirse Chromium geçişi atlar ve kendi iç promise'ini "InvalidStateError:
 * Transition was aborted because of invalid state" ile reddeder. Bu promise
 * pagereveal'dan önce reddedildiği için hiçbir betik ona işleyici bağlayamaz;
 * yalnız animasyon atlanır, sayfa sorunsuz açılır. Bu tek durumu sessize alır,
 * diğer tüm hatalar olduğu gibi raporlanır.
 */
(function () {
    'use strict';
    window.addEventListener('unhandledrejection', function (e) {
        const r = e.reason;
        if (r && r.name === 'InvalidStateError' && typeof r.message === 'string' &&
            r.message.indexOf('Transition was aborted') === 0) {
            e.preventDefault();
        }
    });
})();
