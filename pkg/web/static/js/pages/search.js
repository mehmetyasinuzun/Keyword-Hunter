/** Arama: hazır sorgular, canlı motor takibi (SSE /events), sonuç parçasını yerinde yenileme. */
(function () {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const T = KH.tr;

    let engines = [];       // aktif motor adları (/api/engines)
    let state = {};         // motor → { cls, count, msg }
    let es = null;          // EventSource
    let ctrl = null;        // AbortController (çalışan arama)
    let t0 = 0, tick = null, logN = 0;

    // ── Motor özeti ─────────────────────────────────────────────────────
    function loadEngines() {
        return KH.api('/api/engines').then(d => {
            const act = (d.engines || []).filter(e => e.isActive);
            engines = act.map(e => e.name);
            const up = act.filter(e => e.lastStatus === 'up').length;
            $('engine-sum-txt').textContent = act.length + T(' aktif motor · ', ' active engines · ') + up + T(' çevrimiçi', ' online');
            $('engine-dot').className = 'kh-dot ' + (act.length === 0 ? 'unknown' : up === 0 ? 'down' : (up < act.length / 2 ? 'warn' : 'up'));
        }).catch(() => { $('engine-sum-txt').textContent = T('motor durumu alınamadı', 'engine status unavailable'); });
    }

    // ── Son sorgular ────────────────────────────────────────────────────
    function loadRecent() {
        KH.api('/api/queries').then(d => {
            const qs = (d.queries || []).filter(q => q && q.query).slice(0, 8);
            $('recent-row').hidden = !qs.length;
            KH.render($('recent-chips'), KH.html`${qs.map(q => KH.html`<button type="button" class="kh-chip" data-action="preset" data-q="${q.query}">${q.query}${q.count ? KH.html` <span class="count">${KH.num(q.count)}</span>` : ''}</button>`)}`);
        }).catch(() => { });
    }

    // ── Canlı panel ─────────────────────────────────────────────────────
    const chipIcon = { running: 'refresh', done: 'check', fail: 'close', zero: 'empty' };
    function renderChips() {
        const names = engines.slice();
        Object.keys(state).forEach(n => { if (!names.includes(n)) names.push(n); });
        KH.render($('engine-chips'), KH.html`${names.map(n => {
            const s = state[n] || {};
            return KH.html`<span class="kh-engine-chip ${s.cls || 'wait'}" title="${s.msg || T('bekliyor', 'waiting')}">${KH.raw(KH.icon(chipIcon[s.cls] || 'clock'))} ${n}${s.count != null ? KH.html` <b>${KH.num(s.count)}</b>` : ''}</span>`;
        })}`);
    }

    const logMap = {
        engine_start: ['SCAN', 'scan'], engine_end: ['DONE', 'done'], engine_page: ['PAGE', 'page'], info: ['INFO', 'info'],
        error: ['ERR', 'err'], success: ['OK', 'ok'], auto_tag: ['TAG', 'tag'], auto_tag_error: ['TAG!', 'warn']
    };
    function addLog(d) {
        let [st, cls] = logMap[d.type] || ['LOG', 'info'];
        const msg = String(d.message || '');
        if (d.type === 'engine_end' && /ERROR|FAILED/.test(msg)) { st = 'FAIL'; cls = 'err'; }
        const ln = document.createElement('div');
        ln.className = 'ln';
        const ts = document.createElement('span'); ts.className = 'ts'; ts.textContent = new Date().toLocaleTimeString(KH.lang === 'en' ? 'en-GB' : 'tr-TR', { hour12: false });
        const sp = document.createElement('span'); sp.className = 'st t-' + cls; sp.textContent = '[' + st + ']';
        const body = document.createElement('span'); body.className = 'msg';
        if (d.engine) { const b = document.createElement('b'); b.textContent = d.engine + ': '; body.appendChild(b); }
        body.appendChild(document.createTextNode(msg.replace(/^SEARCH ENGINE (SUCCESS|ERROR): ?/, '')));
        ln.append(ts, sp, body);
        const log = $('log');
        const stick = log.scrollTop + log.clientHeight >= log.scrollHeight - 24;
        log.appendChild(ln);
        while (log.children.length > 400) log.firstElementChild.remove();
        if (stick) log.scrollTop = log.scrollHeight;
        $('log-count').textContent = String(++logN);
    }

    function setProgress(pct) {
        pct = Math.max(2, Math.min(100, Math.round(pct)));
        $('live-bar').style.width = pct + '%';
        $('live-pct').textContent = pct + '%';
    }
    function updateLive(d) {
        const msg = String(d.message || '');
        if (d.type === 'engine_start' && d.engine) state[d.engine] = { cls: 'running', msg: T('sorgulanıyor', 'querying') };
        if (d.type === 'engine_page' && d.engine && state[d.engine]) state[d.engine].msg = msg;
        if (d.type === 'engine_end' && d.engine) {
            const m = msg.match(/(\d+) sonuç/); const n = m ? parseInt(m[1], 10) : 0;
            state[d.engine] = /ERROR|FAILED/.test(msg) ? { cls: 'fail', msg } : { cls: n > 0 ? 'done' : 'zero', count: n, msg };
        }
        renderChips();
        const vals = Object.values(state);
        const finished = vals.filter(s => s.cls !== 'running').length;
        const total = Math.max(engines.length, vals.length, 1);
        const found = vals.reduce((a, s) => a + (s.count || 0), 0);
        if (d.type === 'success') setProgress(100); else setProgress(finished / total * 100);
        $('live-status').textContent = finished + '/' + total + T(' motor tamamlandı', ' engines finished') + (found ? ' · ' + KH.num(found) + T(' ham sonuç', ' raw results') : '');
    }

    function fmtElapsed() {
        const s = Math.floor((Date.now() - t0) / 1000);
        return String(Math.floor(s / 60)).padStart(2, '0') + ':' + String(s % 60).padStart(2, '0');
    }
    function startLive(q) {
        state = {}; logN = 0;
        $('log').textContent = ''; $('log-count').textContent = '0';
        $('live-query').textContent = q;
        $('live-status').textContent = T('Motorlar Tor üzerinden sorgulanıyor…', 'Querying engines over Tor…');
        $('live').hidden = false;
        $('live').classList.remove('is-done', 'is-fail', 'is-stopped', 'is-warn');
        $('live').classList.add('is-running');
        $('live-progress').classList.add('active');
        $('live-dot').className = 'kh-dot up pulse';
        $('stop-btn').hidden = false;
        setProgress(2); renderChips();
        const r = $('live').getBoundingClientRect();
        if (r.top > window.innerHeight * 0.7) $('live').scrollIntoView({ behavior: 'smooth', block: 'start' });
        t0 = Date.now(); $('live-timer').textContent = '00:00';
        clearInterval(tick); tick = setInterval(() => { $('live-timer').textContent = fmtElapsed(); }, 1000);
        if (es) es.close();
        // Akış bağlanmadan arama başlarsa ilk motor olayları kaçar: açılmasını
        // (en fazla 1,5 sn) bekleyen bir söz döndür.
        return new Promise((resolve) => {
            const done = () => { clearTimeout(guard); resolve(); };
            const guard = setTimeout(resolve, 1500);
            try {
                es = new EventSource('/events');
                es.onopen = done;
                es.onmessage = (ev) => { try { const d = JSON.parse(ev.data); addLog(d); updateLive(d); } catch (_) { } };
                es.onerror = () => { if (es) es.close(); done(); };
            } catch (_) { es = null; done(); }
        });
    }
    function finishLive(kind) {
        clearInterval(tick); tick = null;
        if (es) { es.close(); es = null; }
        const live = $('live');
        live.classList.remove('is-running');
        live.classList.add('is-' + kind);
        $('live-progress').classList.remove('active');
        $('stop-btn').hidden = true;
        $('live-dot').className = 'kh-dot ' + (kind === 'done' ? 'up' : kind === 'fail' ? 'down' : 'warn');
        Object.keys(state).forEach(n => { if (state[n].cls === 'running') state[n] = { cls: kind === 'done' ? 'zero' : 'fail', msg: T('yanıt yok', 'no response') }; });
        renderChips();
        const vals = Object.values(state);
        const ok = vals.filter(s => s.cls === 'done' || s.cls === 'zero').length;
        if (kind === 'done') {
            setProgress(100);
            const none = vals.length > 0 && ok === 0;
            live.classList.toggle('is-warn', none);
            if (none) $('live-dot').className = 'kh-dot down';
            $('live-status').textContent = T('Tamamlandı', 'Completed') + ' · ' + fmtElapsed() + (vals.length ? ' · ' + (none ? T('hiçbir motor yanıt vermedi', 'no engine responded') : ok + '/' + vals.length + T(' motor yanıt verdi', ' engines responded')) : '');
        } else {
            $('live-status').textContent = kind === 'stopped' ? T('Durduruldu', 'Stopped') : T('Başarısız', 'Failed');
        }
    }

    // ── Sonuç alanı durumları ───────────────────────────────────────────
    function area() { return $('results-area'); }
    function showScanning() {
        KH.render(area(), KH.html`<section class="kh-card scanning" aria-busy="true">
            <div class="scan-rows">${[0, 1, 2, 3].map(() => KH.html`<div class="scan-row"><div class="kh-skel w-40"></div><div class="kh-skel"></div><div class="kh-skel w-60"></div></div>`)}</div>
            <p class="kh-hint text-center mt-4">${T('Motorlar Tor üzerinden sorgulanıyor; bu işlem 30-90 saniye sürebilir…', 'Querying engines over Tor; this can take 30-90 seconds…')}</p></section>`);
    }
    function showError(msg, stopped) {
        KH.render(area(), KH.html`<section class="kh-card"><div class="kh-empty ${stopped ? '' : 'is-err'}">
            <div class="icon">${KH.raw(KH.icon(stopped ? 'pause' : 'warn'))}</div>
            <h3>${stopped ? T('Arama durduruldu', 'Search stopped') : T('Arama tamamlanamadı', 'Search could not be completed')}</h3>
            <p>${msg}</p>
            <div class="kh-inline">
                <button type="button" class="kh-btn primary" data-action="retry" data-write>${KH.raw(KH.icon('refresh'))} ${T('Tekrar dene', 'Try again')}</button>
                <a class="kh-btn subtle" href="/monitor">${KH.raw(KH.icon('monitor'))} ${T('Motor sağlığı', 'Engine health')}</a>
            </div></div></section>`);
    }

    // ── Arama ───────────────────────────────────────────────────────────
    async function runSearch() {
        const form = $('search-form');
        const input = $('query');
        const q = input.value.trim().replace(/\s+/g, ' ');
        if (!q) { KH.toast(T('Bir arama sorgusu yazın', 'Type a search query'), 'warn'); input.focus(); return; }
        if (ctrl) return;
        input.value = q;
        const btn = $('search-btn');
        KH.busy(btn, true, btn.dataset.busyText);
        ctrl = new AbortController();
        showScanning();
        try {
            await startLive(q);
            if (ctrl.signal.aborted) throw new DOMException('aborted', 'AbortError');
            const res = await fetch(form.getAttribute('action'), { method: 'POST', body: new FormData(form), signal: ctrl.signal, credentials: 'same-origin' });
            if (res.redirected && new URL(res.url).pathname === '/login') { location.href = '/login'; return; }
            const doc = new DOMParser().parseFromString(await res.text(), 'text/html');
            if (!res.ok) {
                const alert = doc.querySelector('.search-hero .kh-alert.err');
                throw new Error((alert && alert.textContent.trim()) ||
                    (res.status === 429 ? T('Çok fazla istek; birkaç saniye bekleyin', 'Too many requests; wait a few seconds')
                        : res.status === 403 ? T('Bu işlem için yetkiniz yok', 'You are not allowed to do this')
                            : T('Arama isteği başarısız', 'Search request failed') + ' (HTTP ' + res.status + ')'));
            }
            const fresh = doc.getElementById('results-area');
            if (!fresh) throw new Error(T('Beklenmeyen yanıt', 'Unexpected response'));
            area().replaceWith(fresh);
            KH.enhance(fresh);
            finishLive('done');
            try { history.replaceState(null, '', '/search?q=' + encodeURIComponent(q)); } catch (_) { }
            const total = fresh.querySelector('#stat-total');
            if (total) KH.toast(KH.num(total.dataset.n) + T(' sonuç bulundu', ' results found'), 'ok');
            else KH.toast(T('Arama tamamlandı; sonuç yok', 'Search finished with no results'), 'warn');
            loadRecent();
        } catch (err) {
            if (err && err.name === 'AbortError') {
                finishLive('stopped');
                showError(T('Arama kullanıcı tarafından durduruldu; motorlardaki istekler iptal edildi.', 'The search was stopped; pending engine requests were cancelled.'), true);
            } else {
                finishLive('fail');
                showError(err.message || String(err));
                KH.toast(err.message || String(err), 'err');
            }
        } finally {
            ctrl = null;
            KH.busy(btn, false);
        }
    }

    KH.act({
        'search': runSearch,
        'retry': runSearch,
        'stop': () => { if (ctrl) ctrl.abort(); },
        'preset': (el) => {
            const input = $('query');
            input.value = el.dataset.q || '';
            input.focus();
            input.setSelectionRange(input.value.length, input.value.length);
            input.classList.remove('kh-flash'); void input.offsetWidth; input.classList.add('kh-flash');
        },
        'refocus': () => {
            window.scrollTo({ top: 0, behavior: 'smooth' });
            const input = $('query'); input.focus(); input.select();
        }
    });

    document.addEventListener('DOMContentLoaded', () => {
        loadEngines(); loadRecent();
        const input = $('query');
        if (input.value) input.setSelectionRange(input.value.length, input.value.length);
        window.addEventListener('pagehide', () => { if (es) es.close(); });
    });
})();
