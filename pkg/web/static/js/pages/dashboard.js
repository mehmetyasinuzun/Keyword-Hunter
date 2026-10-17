/** Panel: motor sağlığı, yeni bulgu akışı, IOC özeti, izleme özeti, ekran görüntüleri. */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;

    function loadEngineHealth() {
        KH.api('/api/monitor/summary').then(d => {
            const up = d.enginesActiveUp || 0, act = d.enginesActive || 0;
            $('engine-up').textContent = up + '/' + act;
            $('engine-sub').textContent = T('aktif motorlardan çevrimiçi · ', 'active engines online · ') + (d.scheduledActive || 0) + T(' planlı tarama', ' scheduled');
            $('engine-stat').className = 'kh-stat ' + (act === 0 ? 'blue' : up === 0 ? 'red' : (up < act / 2 ? 'amber' : 'green'));
        }).catch(() => { $('engine-sub').textContent = T('alınamadı', 'unavailable'); });
    }

    const artNames = { email: ['mail', 'E-posta', 'Email'], bitcoin: ['coin', 'Bitcoin', 'Bitcoin'], monero: ['coin', 'Monero', 'Monero'], ip: ['globe', 'IP', 'IP'], onion: ['onion', 'Onion', 'Onion'], credit_card: ['card', 'Kart', 'Card'], phone: ['phone', 'Telefon', 'Phone'], ssh_key: ['key', 'SSH', 'SSH'], api_key: ['key', 'API Key', 'API key'], username: ['user', 'Kullanıcı', 'Username'], hash: ['hash', 'Hash', 'Hash'] };
    function loadArtifactStats() {
        const box = $('artifact-stats');
        KH.api('/api/artifacts/stats').then(d => {
            if (!d.types || !d.types.length) {
                box.innerHTML = '<p class="kh-hint">' + KH.esc(T('Henüz IOC çıkarılmadı. Bulgular sayfasında “Etiketle” çalıştırdığınızda e-posta, cüzdan, IP, hash ve onion adresleri otomatik toplanır.', 'No IOCs yet. Run “Tag” on Findings to auto-collect emails, wallets, IPs, hashes and onion addresses.')) + '</p>';
                return;
            }
            const chips = d.types.map(t => {
                const n = artNames[t.type] || ['info', t.type, t.type];
                return '<a class="kh-chip" href="/results" title="' + KH.esc(t.type) + '">' + KH.icon(n[0], 'sm') + ' ' + KH.esc(KH.lang === 'en' ? n[2] : n[1]) + ' <span class="count">' + KH.num(t.count) + '</span></a>';
            }).join('');
            box.innerHTML = '<div class="kh-inline kh-stagger">' + chips + '</div><p class="kh-hint mt-3">' + KH.esc(T('Toplam ', 'Total ')) + '<b>' + KH.num(d.total) + '</b> artifact</p>';
        }).catch(() => { box.innerHTML = '<p class="kh-hint">' + KH.esc(T('Alınamadı.', 'Unavailable.')) + '</p>'; });
    }

    function loadWatchSummary() {
        const box = $('watch-summary');
        KH.api('/api/watchlist').then(d => {
            const items = d.items || [];
            if (!items.length) { box.innerHTML = '<p class="kh-hint">' + KH.esc(T('İzlenen site yok.', 'No sites watched.')) + ' <a href="/watchlist">' + KH.esc(T('Ekle', 'Add')) + ' →</a></p>'; return; }
            const count = (f) => items.filter(f).length;
            const up = count(i => i.status === 'up'), down = count(i => i.status === 'down'), blocked = count(i => i.status === 'engel'), changed = count(i => i.changed);
            const pct = KH.pct(up, items.length);
            box.innerHTML =
                '<div class="kh-inline mb-3"><span class="kh-badge ok">' + up + ' ' + KH.esc(T('ayakta', 'up')) + '</span><span class="kh-badge err">' + down + ' ' + KH.esc(T('kapalı', 'down')) + '</span><span class="kh-badge warn">' + blocked + ' ' + KH.esc(T('engel', 'blocked')) + '</span>' +
                (changed ? '<span class="kh-badge info">' + changed + ' ' + KH.esc(T('değişti', 'changed')) + '</span>' : '') + '</div>' +
                '<div class="kh-progress"><div data-bar="' + pct + '"></div></div>' +
                '<div class="flex justify-between mt-2 text-xs kh-muted"><span>' + KH.esc(T('Erişilebilirlik', 'Availability')) + ' ' + KH.pctText(pct) + '</span><a href="/watchlist">' + KH.esc(T('Detay', 'Details')) + ' →</a></div>';
            KH.enhance(box);
        }).catch(() => { box.innerHTML = '<p class="kh-hint">' + KH.esc(T('Alınamadı.', 'Unavailable.')) + '</p>'; });
    }

    function loadShots() {
        const box = $('shots'), hint = $('shots-hint');
        KH.api('/api/screenshots?limit=8').then(d => {
            const s = d.screenshots || [];
            if (!d.available) hint.textContent = T('yalnızca Docker imajında (Chromium) etkin', 'enabled only in the Docker image (Chromium)');
            if (!s.length) { box.innerHTML = '<div class="kh-empty p-4"><div class="icon">' + KH.icon('camera') + '</div><p>' + KH.esc(T('Henüz görüntü yok.', 'No screenshots yet.')) + '</p></div>'; box.classList.remove('shots'); return; }
            box.innerHTML = s.map(x => '<a href="/screenshot/file/' + encodeURIComponent(x.id) + '" target="_blank" rel="noopener" title="' + KH.esc(x.targetUrl) + '"><img loading="lazy" src="/screenshot/file/' + encodeURIComponent(x.id) + '" alt=""><span class="cap">' + (x.challenge ? KH.icon('warn', 'sm') + ' ' : '') + KH.esc(KH.short(String(x.targetUrl).replace(/^https?:\/\//, ''), 32)) + '</span></a>').join('');
        }).catch(() => { box.innerHTML = '<p class="kh-hint">' + KH.esc(T('Alınamadı.', 'Unavailable.')) + '</p>'; });
    }

    function loadNewResults() {
        const hours = $('new-results-hours').value;
        const feed = $('new-results-feed'), badge = $('new-results-badge');
        KH.api('/api/new-results?hours=' + encodeURIComponent(hours) + '&limit=12').then(d => {
            const r = d.results || [];
            badge.hidden = !r.length;
            badge.textContent = r.length + ' ' + T('yeni', 'new');
            if (!r.length) { feed.innerHTML = '<div class="kh-empty p-4"><div class="icon">' + KH.icon('check') + '</div><p>' + KH.esc(T('Bu aralıkta yeni bulgu yok.', 'No new findings in this window.')) + '</p></div>'; return; }
            feed.innerHTML = r.map(x =>
                '<a class="kh-feed-item fresh" href="/results?text=' + encodeURIComponent(x.URL) + '">' + KH.sev(x.Criticality) +
                '<div class="body"><div class="t">' + KH.esc(KH.short(x.Title, 90)) + '</div><div class="u">' + KH.esc(KH.short(x.URL, 80)) + '</div></div>' +
                '<span class="kh-badge neutral mono src">' + KH.esc(x.Source) + '</span></a>').join('');
            // Kademeli giriş: gecikme CSSOM ile verilir (satır içi style özniteliği CSP'de engellenir)
            feed.querySelectorAll('.kh-feed-item').forEach((el, i) => { el.style.animationDelay = (i * 35) + 'ms'; });
        }).catch(() => { feed.innerHTML = '<p class="kh-hint">' + KH.esc(T('Akış yüklenemedi.', 'Feed failed to load.')) + '</p>'; });
    }

    function relTimes() { document.querySelectorAll('[data-ago]').forEach(el => { if (el.dataset.ago) { el.title = el.textContent; el.textContent = KH.ago(el.dataset.ago); } }); }

    KH.act({ 'load-new': loadNewResults });
    document.addEventListener('DOMContentLoaded', () => {
        loadEngineHealth(); loadNewResults(); loadArtifactStats(); loadWatchSummary(); loadShots(); relTimes();
        setInterval(() => { if (!document.hidden) loadNewResults(); }, 60000);
    });
})();
