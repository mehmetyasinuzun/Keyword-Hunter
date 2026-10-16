/**
 * KeywordHunter — İlişki haritası: D3 yerleşimleri ve geçişli çizim.
 *
 * Her çizim ham veriden yeni bir d3.hierarchy kurar; düğümler kararlı bir
 * yol-kimliği (id) ile eşleştirilir. Böylece aç/kapat, yerleşim değişimi ve
 * kademeli yükleme sırasında düğümler önceki konumlarından yeni konumlarına
 * akar (enter: en yakın atanın eski konumundan, exit: en yakın kalan ataya).
 * Tüm yerleşimler kartezyen (x, y) üretir; bağlantılar her yerleşimde aynı
 * "M … C …" biçiminde olduğundan yol enterpolasyonu pürüzsüzdür.
 */
(function () {
    'use strict';

    const G = window.KHGraph = window.KHGraph || {};
    const S = () => G.state;
    const reduced = window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    const TAU = Math.PI * 2;
    const R1 = (v) => Math.round(v * 10) / 10;

    let svg = null, viewport = null, gLinks = null, gNodes = null, zoom = null;
    let prevPos = new Map();     // id → {x, y} (son çizimdeki hedef konum)
    let semantic = '';

    // ── SVG iskeleti + yakınlaştırma ────────────────────────────────────
    function ensureSvg() {
        if (svg) return;
        const host = document.getElementById('graph-canvas');
        svg = d3.select(host).append('svg')
            .attr('class', 'gx-svg')
            .attr('width', '100%').attr('height', '100%')
            .on('contextmenu', (e) => e.preventDefault());
        viewport = svg.append('g').attr('class', 'gx-viewport');
        gLinks = viewport.append('g').attr('class', 'gx-links');
        gNodes = viewport.append('g').attr('class', 'gx-nodes');
        zoom = d3.zoom()
            .scaleExtent([0.04, 4])
            .on('zoom', (event) => {
                viewport.attr('transform', event.transform);
                if (event.sourceEvent) S().userZoomed = true;
                updateSemantic(event.transform.k);
                if (G.onZoom) G.onZoom(event);
            });
        svg.call(zoom).on('dblclick.zoom', null);
    }
    G.clearCanvas = function () {
        if (!svg) return;
        if (S().sim) { S().sim.stop(); S().sim = null; }
        gLinks.selectAll('*').remove();
        gNodes.selectAll('*').remove();
        prevPos = new Map();
        S().root = null;
    };

    function updateSemantic(k) {
        const px = k * G.settings.fontSize;
        const cls = px < 3.2 ? 'gx-z-far' : px < 6 ? 'gx-z-mid' : '';
        if (cls === semantic) return;
        svg.classed('gx-z-far', cls === 'gx-z-far').classed('gx-z-mid', cls === 'gx-z-mid');
        semantic = cls;
    }

    // ── Hiyerarşi + aç/kapat durumu ─────────────────────────────────────
    G.isOpen = function (n) {
        const o = S().open;
        return o.has(n.id) ? o.get(n.id) : n.depth < S().defaultDepth;
    };
    function build() {
        const root = d3.hierarchy(S().view, d => (d.children && d.children.length ? d.children : null));
        const seen = new Set();
        root.eachBefore(n => {
            let id = (n.parent ? n.parent.id + '>' : '') + (n.data.type || '') + ':' + (n.data.url || n.data.name || '');
            if (seen.has(id)) { let i = 2; while (seen.has(id + '#' + i)) i++; id += '#' + i; }
            seen.add(id);
            n.id = id;
            n.kind = G.kind(n.data);
        });
        root.eachBefore(n => {
            if (n.children && !G.isOpen(n)) { n._children = n.children; n.children = null; }
        });
        return root;
    }

    // ── Ölçüler ─────────────────────────────────────────────────────────
    G.radius = function (n) {
        const base = { root: 18, query: 13, engine: 10, result: 6.5, multi: 8.5, internal: 6, external: 6 };
        return base[n.depth === 0 ? (n.data.type === 'root' ? 'root' : n.kind) : n.kind] || 6;
    };
    G.fontSize = function (n) {
        const b = G.settings.fontSize;
        if (n.depth === 0) return b + 4;
        if (n.depth === 1) return b + 2;
        if (n.depth === 2) return b;
        return Math.max(b - 1, 10);
    };
    function labelLen(n) {
        const base = G.settings.truncateLength + (n.depth <= 1 || n.children ? 10 : 0);
        // Dairesel yerleşimde içe dönük iç düğüm etiketi halka bandına sığacak kadar kısalır
        if (S().layout === 'radial' && n.children && n.depth >= 2 && n.band) {
            return Math.max(6, Math.min(base, Math.floor((n.band - G.radius(n) - 24) / (G.fontSize(n) * 0.56))));
        }
        return base;
    }
    G.truncate = function (s, len) { s = String(s || ''); return s.length > len ? s.slice(0, len) + '…' : s; };
    function hasLabel(n) { return G.settings.showAllLabels || n.depth <= 2; }

    // ── Yerleşimler: hepsi n.x / n.y kartezyen üretir ────────────────────
    function layoutRadial(root) {
        const fs = G.settings.fontSize, sp = G.settings.spacing;
        const leaves = root.leaves().length;
        let maxDepth = 0; root.each(n => { if (n.depth > maxDepth) maxDepth = n.depth; });
        const ring = Math.max(125, fs * 9) * sp;
        const outer = Math.max(ring * Math.max(1, maxDepth), leaves * fs * 1.3 * sp / TAU);
        // Dış halka büyüdükçe iç halkalar da açılır (iç düğüm etiketleri sıkışmaz)
        const step = Math.max(ring, outer / Math.max(1, maxDepth) * 0.85);
        d3.tree().size([TAU, 1]).separation((a, b) => (a.parent === b.parent ? 1 : 2) / Math.max(1, a.depth))(root);
        root.each(n => {
            const a = n.x;
            const r = n.depth === 0 ? 0 : (n.depth >= maxDepth ? outer : n.depth * step);
            n.angle = a;
            n.band = step;
            n.x = R1(r * Math.sin(a));
            n.y = R1(-r * Math.cos(a));
        });
        placeRootLabel(root);
    }
    function layoutTree(root) {
        const fs = G.settings.fontSize, sp = G.settings.spacing;
        const rowH = fs * 1.6 * sp;
        const colW = Math.max(170, (G.settings.truncateLength + 10) * fs * 0.56 + 56) * Math.sqrt(sp);
        d3.tree().nodeSize([rowH, colW]).separation((a, b) => (a.parent === b.parent ? 1 : 1.5))(root);
        root.each(n => { const vx = n.x; n.x = R1(n.y); n.y = R1(vx); });
    }
    function layoutForce(root) {
        const sp = G.settings.spacing;
        const nodes = root.descendants(), links = root.links();
        nodes.forEach((n, i) => {
            const p = prevPos.get(n.id) || (n.parent && prevPos.get(n.parent.id));
            if (p) { n.x = p.x + (prevPos.has(n.id) ? 0 : (Math.random() - 0.5) * 30); n.y = p.y + (prevPos.has(n.id) ? 0 : (Math.random() - 0.5) * 30); }
            else { const a = i * 2.39996, r = 12 * Math.sqrt(i); n.x = r * Math.cos(a); n.y = r * Math.sin(a); }
        });
        if (S().sim) S().sim.stop();
        const sim = d3.forceSimulation(nodes)
            .force('link', d3.forceLink(links).distance(l => (l.target.depth <= 1 ? 150 : l.target.children || l.target._children ? 110 : 64) * sp).strength(0.9))
            .force('charge', d3.forceManyBody().strength(n => (n.depth === 0 ? -900 : (n.children || n._children) ? -420 : -150) * sp).distanceMax(1200))
            .force('collide', d3.forceCollide(n => G.radius(n) + (n.children ? 30 : 9) * sp).strength(0.9))
            .force('x', d3.forceX(0).strength(0.035))
            .force('y', d3.forceY(0).strength(0.035))
            .stop();
        const ticks = nodes.length > 1500 ? 90 : nodes.length > 600 ? 160 : 260;
        for (let i = 0; i < ticks; i++) sim.tick();
        nodes.forEach(n => { n.x = R1(n.x); n.y = R1(n.y); });
        S().sim = sim;
    }

    // ── Bağlantı yolu: üç yerleşimde de M…C… biçimi ─────────────────────
    function polar(p) { let a = Math.atan2(p.x, -p.y); if (a < 0) a += TAU; return { a, r: Math.hypot(p.x, p.y) }; }
    function cart(a, r) { return { x: r * Math.sin(a), y: -r * Math.cos(a) }; }
    function linkPath(s, t) {
        const L = S().layout;
        let c1, c2;
        if (L === 'radial') {
            const ps = polar(s), pt = polar(t), rm = (ps.r + pt.r) / 2;
            c1 = cart(ps.r < 1 ? pt.a : ps.a, rm); c2 = cart(pt.a, rm);
        } else if (L === 'tree') {
            const mx = (s.x + t.x) / 2; c1 = { x: mx, y: s.y }; c2 = { x: mx, y: t.y };
        } else {
            c1 = { x: s.x + (t.x - s.x) / 3, y: s.y + (t.y - s.y) / 3 }; c2 = { x: s.x + 2 * (t.x - s.x) / 3, y: s.y + 2 * (t.y - s.y) / 3 };
        }
        return 'M' + R1(s.x) + ',' + R1(s.y) + 'C' + R1(c1.x) + ',' + R1(c1.y) + ' ' + R1(c2.x) + ',' + R1(c2.y) + ' ' + R1(t.x) + ',' + R1(t.y);
    }

    // ── Etiket yerleşimi (okunur: dik/yatay; kök her zaman yatay) ───────
    function labelGeom(n) {
        const r = G.radius(n), fs = G.fontSize(n), L = S().layout, gap = r + 6;
        if (n.depth === 0) {
            if (L === 'tree') return { x: -gap, y: 0, rot: 0, anchor: 'end', dy: '0.32em' };
            if (L === 'radial' && n.labelAngle != null) {
                const sx = Math.sin(n.labelAngle), sy = -Math.cos(n.labelAngle), d = r + 8;
                if (sx > 0.38) return { x: R1(sx * d), y: R1(sy * d), rot: 0, anchor: 'start', dy: '0.32em' };
                if (sx < -0.38) return { x: R1(sx * d), y: R1(sy * d), rot: 0, anchor: 'end', dy: '0.32em' };
                return sy < 0 ? { x: 0, y: -(r + 8), rot: 0, anchor: 'middle', dy: '0' } : { x: 0, y: r + fs + 4, rot: 0, anchor: 'middle', dy: '0' };
            }
            return { x: 0, y: r + fs + 4, rot: 0, anchor: 'middle', dy: '0' };
        }
        if (L === 'radial') {
            const deg = n.angle * 180 / Math.PI - 90;
            const flip = n.angle > Math.PI;
            if (n.children) {
                // İlk halka az kalabalık: yatay, düğümün üstünde. Daha derin iç düğümler: içe doğru, dik okunur.
                if (n.depth === 1) return { x: 0, y: -(r + 7), rot: 0, anchor: 'middle', dy: '0' };
                return { x: flip ? gap : -gap, y: 0, rot: R1(flip ? deg + 180 : deg), anchor: flip ? 'start' : 'end', dy: '-0.45em' };
            }
            return { x: flip ? -gap : gap, y: 0, rot: R1(flip ? deg + 180 : deg), anchor: flip ? 'end' : 'start', dy: '0.32em' };
        }
        if (L === 'tree') {
            return n.children ? { x: -gap, y: 0, rot: 0, anchor: 'end', dy: '0.32em' } : { x: gap, y: 0, rot: 0, anchor: 'start', dy: '0.32em' };
        }
        // Kuvvet: iç düğüm üstte; yaprak, ebeveynden dışa doğru (yelpaze) ve dik okunur
        if (n.children) return { x: 0, y: -(r + 7), rot: 0, anchor: 'middle', dy: '0' };
        if (n.parent) {
            const th = Math.atan2(n.y - n.parent.y, n.x - n.parent.x) * 180 / Math.PI;
            const flip = Math.abs(th) > 90;
            return { x: flip ? -gap : gap, y: 0, rot: R1(flip ? th + 180 : th), anchor: flip ? 'end' : 'start', dy: '0.32em' };
        }
        return { x: gap, y: 0, rot: 0, anchor: 'start', dy: '0.32em' };
    }

    /** Etiketin kaba sınır kutusu (mutlak koordinat). */
    function labelBox(n) {
        const g = labelGeom(n), fs = G.fontSize(n);
        const w = Math.min(String(n.data.name || '').length, labelLen(n) + 1) * fs * 0.56;
        if (g.rot) {
            const th = g.rot * Math.PI / 180, dir = g.anchor === 'start' ? 1 : -1;
            const ax = n.x + Math.cos(th) * g.x, ay = n.y + Math.sin(th) * g.x;
            const ex = n.x + Math.cos(th) * (g.x + dir * w), ey = n.y + Math.sin(th) * (g.x + dir * w);
            return { x0: Math.min(ax, ex) - fs / 2, y0: Math.min(ay, ey) - fs / 2, x1: Math.max(ax, ex) + fs / 2, y1: Math.max(ay, ey) + fs / 2 };
        }
        const lx = n.x + g.x, ly = n.y + g.y;
        if (g.anchor === 'middle') return { x0: lx - w / 2, y0: ly - fs, x1: lx + w / 2, y1: ly + fs * 0.3 };
        if (g.anchor === 'start') return { x0: lx, y0: ly - fs * 0.6, x1: lx + w, y1: ly + fs * 0.6 };
        return { x0: lx - w, y0: ly - fs * 0.6, x1: lx, y1: ly + fs * 0.6 };
    }
    /**
     * Kök etiketi (dairesel): 24 yön denenir; ilk halkadaki düğüm/etiket kutuları
     * ve kökten çıkan bağlantılarla en az çakışan yön seçilir.
     */
    function placeRootLabel(root) {
        const kids = root.children || [];
        if (!kids.length) { root.labelAngle = Math.PI; return; }
        const boxes = kids.map(c => {
            const r = G.radius(c) + 3;
            const b = hasLabel(c) ? labelBox(c) : null;
            return [{ x0: c.x - r, y0: c.y - r, x1: c.x + r, y1: c.y + r }].concat(b ? [b] : []);
        }).flat();
        const overlap = (a, b) => Math.max(0, Math.min(a.x1, b.x1) - Math.max(a.x0, b.x0)) * Math.max(0, Math.min(a.y1, b.y1) - Math.max(a.y0, b.y0));
        const crosses = (b) => kids.reduce((acc, c) => {
            for (let t = 0.15; t <= 0.85; t += 0.1) { const x = c.x * t, y = c.y * t; if (x > b.x0 && x < b.x1 && y > b.y0 && y < b.y1) return acc + 1; }
            return acc;
        }, 0);
        let best = Math.PI, bestScore = Infinity;
        for (let i = 0; i < 24; i++) {
            const a = (Math.PI + i * TAU / 24) % TAU; // aşağıdan başla (eşitlikte alt tercih)
            root.labelAngle = a;
            const box = labelBox(root);
            const score = boxes.reduce((acc, b) => acc + overlap(box, b), 0) + crosses(box) * 260;
            if (score < bestScore - 0.5) { bestScore = score; best = a; }
        }
        root.labelAngle = best;
    }

    /** Hedef yerleşimin sınır kutusu (etiketler dahil, tahmini) — geçişle eşzamanlı sığdırma için. */
    function targetBounds(nodes) {
        let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
        const add = (x, y) => { if (x < x0) x0 = x; if (x > x1) x1 = x; if (y < y0) y0 = y; if (y > y1) y1 = y; };
        nodes.forEach(n => {
            const r = G.radius(n) + 2;
            add(n.x - r, n.y - r); add(n.x + r, n.y + r);
            if (!hasLabel(n)) return;
            const b = labelBox(n);
            add(b.x0, b.y0); add(b.x1, b.y1);
        });
        if (!isFinite(x0)) return null;
        return { x: x0, y: y0, width: Math.max(1, x1 - x0), height: Math.max(1, y1 - y0) };
    }

    // ── Ana çizim ───────────────────────────────────────────────────────
    /**
     * opts.fit: 'auto' (kullanıcı yakınlaştırmadıysa sığdır) | 'final' | 'force' | 'none'
     * opts.anchor: id — bu düğüm ekranda yerinde kalır (aç/kapat sırasında)
     * opts.instant: geçişsiz
     */
    G.render = function (opts) {
        opts = opts || {};
        if (!S().view) return;
        ensureSvg();
        G.palette();
        const root = build();
        const L = S().layout;
        if (L === 'tree') layoutTree(root); else if (L === 'force') layoutForce(root); else layoutRadial(root);
        if (L !== 'force' && S().sim) { S().sim.stop(); S().sim = null; }
        S().root = root;

        const nodes = root.descendants();
        const links = root.links();
        const byId = new Map(nodes.map(n => [n.id, n]));
        const dur = (opts.instant || reduced || nodes.length > 1800) ? 0 : 560;
        const ease = d3.easeCubicOut;
        const origin = (n) => {
            for (let p = n.parent; p; p = p.parent) { const o = prevPos.get(p.id); if (o) return o; }
            return prevPos.size ? { x: n.x, y: n.y } : { x: 0, y: 0 };
        };
        const sink = (n) => {
            for (let p = n.parent; p; p = p.parent) { const t = byId.get(p.id); if (t) return t; }
            return { x: n.x, y: n.y };
        };
        const T = (sel) => (dur ? sel.transition().duration(dur).ease(ease) : sel);
        const fresh = S().fresh;

        // Bağlantılar
        const linkSel = gLinks.selectAll('path.gx-link').data(links, d => d.target.id);
        const linkEnter = linkSel.enter().append('path')
            .attr('class', 'gx-link')
            .attr('d', d => { const o = origin(d.target); return linkPath(o, o); });
        const linkAll = linkEnter.merge(linkSel);
        linkAll.classed('is-fresh', d => fresh.has(d.target.id));
        T(linkAll).attr('d', d => linkPath(d.source, d.target));
        T(linkSel.exit()).attr('d', d => { const t = sink(d.target); return linkPath(t, t); }).style('opacity', 0).remove();

        // Düğümler
        const nodeSel = gNodes.selectAll('g.gx-node').data(nodes, d => d.id);
        const nodeEnter = nodeSel.enter().append('g')
            .attr('class', 'gx-node')
            .attr('transform', d => { const o = origin(d); return 'translate(' + o.x + ',' + o.y + ')'; })
            .style('opacity', 0);
        nodeEnter.append('circle').attr('class', 'halo');
        nodeEnter.append('circle').attr('class', 'ring');
        nodeEnter.append('circle').attr('class', 'dot').attr('r', 0);
        nodeEnter.append('text').attr('class', 'lbl');
        nodeEnter
            .on('click', (e, d) => G.onNodeClick && G.onNodeClick(e, d))
            .on('contextmenu', (e, d) => G.onNodeMenu && G.onNodeMenu(e, d))
            .on('mouseenter', (e, d) => G.onNodeEnter && G.onNodeEnter(e, d))
            .on('mousemove', (e, d) => G.onNodeMove && G.onNodeMove(e, d))
            .on('mouseleave', (e, d) => G.onNodeLeave && G.onNodeLeave(e, d))
            .on('keydown', (e, d) => G.onNodeKey && G.onNodeKey(e, d));

        const nodeAll = nodeEnter.merge(nodeSel);
        nodeAll
            .attr('data-kind', d => d.kind)
            .classed('is-root', d => d.depth === 0)
            .classed('is-collapsed', d => !!d._children)
            .classed('is-open', d => !!d.children)
            .classed('is-multi', d => d.kind === 'multi')
            .classed('is-expanded', d => !!d.data.isExpanded)
            .classed('no-label', d => !hasLabel(d))
            .attr('tabindex', d => (d.depth <= 2 || d.children || d._children) ? 0 : null)
            .attr('role', d => (d.depth <= 2 || d.children || d._children) ? 'button' : null)
            .attr('aria-label', d => (d.depth <= 2 || d.children || d._children) ? String(d.data.name || '') : null);

        nodeAll.select('circle.halo').attr('r', d => (d.kind === 'multi' ? G.radius(d) + 7 : 0));
        nodeAll.select('circle.ring').attr('r', d => (d._children ? G.radius(d) + 4.5 : 0));
        T(nodeAll.select('circle.dot')).attr('r', d => G.radius(d));

        const lbl = nodeAll.select('text.lbl')
            .text(d => G.truncate(d.data.name, labelLen(d)))
            .attr('font-size', d => G.fontSize(d))
            .attr('font-weight', d => (d.depth <= 1 ? 700 : d.children ? 650 : 500))
            .attr('dy', d => labelGeom(d).dy)
            .attr('text-anchor', d => labelGeom(d).anchor);
        T(lbl)
            .attr('x', d => labelGeom(d).x)
            .attr('y', d => labelGeom(d).y)
            .attr('transform', d => 'rotate(' + labelGeom(d).rot + ')');

        T(nodeAll).attr('transform', d => 'translate(' + d.x + ',' + d.y + ')').style('opacity', 1);
        T(nodeSel.exit()).attr('transform', d => { const t = sink(d); return 'translate(' + t.x + ',' + t.y + ')'; }).style('opacity', 0).remove();

        paint(nodeAll, linkAll);
        nodeAll.filter(d => d.depth === 0).raise(); // kök etiketi diğerlerinin üstünde (halesi örter)

        // Kuvvet yerleşiminde sürükleme (yalnız bu yerleşimde)
        if (L === 'force') nodeAll.call(dragBehaviour(nodeAll, linkAll));
        else nodeAll.on('.drag', null);

        // Yeni bağlantı vurgusu kısa süre sonra kalkar
        if (fresh.size) {
            const ids = new Set(fresh); fresh.clear();
            setTimeout(() => { gLinks.selectAll('path.gx-link.is-fresh').filter(d => ids.has(d.target.id)).classed('is-fresh', false); }, 2600);
        }

        // Görünüm: düğümü yerinde tut ya da sığdır
        const tr = d3.zoomTransform(svg.node());
        const prevAnchor = opts.anchor && prevPos.get(opts.anchor);
        const nextAnchor = opts.anchor && byId.get(opts.anchor);
        const mode = opts.fit || 'auto';
        if (mode === 'force' || ((mode === 'auto' || mode === 'final') && !S().userZoomed)) {
            G.fit(dur || 0, targetBounds(nodes));
        } else if (prevAnchor && nextAnchor) {
            const sx = tr.applyX(prevAnchor.x), sy = tr.applyY(prevAnchor.y);
            const nt = d3.zoomIdentity.translate(sx - tr.k * nextAnchor.x, sy - tr.k * nextAnchor.y).scale(tr.k);
            (dur ? svg.transition().duration(dur).ease(ease) : svg).call(zoom.transform, nt);
        }
        updateSemantic(tr.k);

        prevPos = new Map(nodes.map(n => [n.id, { x: n.x, y: n.y }]));
        if (G.onRendered) G.onRendered(root);
    };

    // ── Renkler (tema değişiminde yeniden boyanır) ─────────────────────
    function paint(nodeSel, linkSel) {
        const C = G.colors;
        nodeSel.each(function (d) {
            const g = d3.select(this), c = C[d.kind] || C.result;
            const hollow = !!d.children && d.depth > 0;
            const isRoot = d.depth === 0;
            g.select('circle.dot')
                .attr('fill', isRoot ? C.surface : hollow ? C.surface : c)
                .attr('stroke', isRoot || hollow ? (isRoot ? C[d.data.type === 'root' ? 'root' : d.kind] : c) : C.halo)
                .attr('stroke-width', isRoot ? 3.5 : hollow ? 2.5 : 1.5);
            g.select('circle.ring').attr('fill', 'none').attr('stroke', c);
            g.select('circle.halo').attr('fill', c);
            g.select('text.lbl')
                .attr('fill', (d.depth <= 1 || d.children) ? C.text : C.text2)
                .attr('stroke', C.halo)
                .attr('font-family', C.font);
        });
        linkSel
            .attr('stroke', d => C[d.target.kind] || C.result)
            .attr('stroke-width', d => (d.target.depth <= 1 ? 2.2 : d.target.children || d.target._children ? 1.7 : 1.1));
        svg.style('--gx-hl', C.primary);
    }
    G.recolor = function () {
        G.palette();
        if (G.paintLegend) G.paintLegend();
        if (!svg) return;
        paint(gNodes.selectAll('g.gx-node'), gLinks.selectAll('path.gx-link'));
    };

    // ── Sürükleme (kuvvet) ──────────────────────────────────────────────
    function dragBehaviour(nodeSel, linkSel) {
        const tick = () => {
            nodeSel.attr('transform', d => 'translate(' + d.x + ',' + d.y + ')');
            linkSel.attr('d', d => linkPath(d.source, d.target));
        };
        return d3.drag()
            .on('start', (e, d) => {
                const sim = S().sim; if (!sim) return;
                sim.on('tick', tick);
                if (!e.active) sim.alphaTarget(0.25).restart();
                d.fx = d.x; d.fy = d.y;
            })
            .on('drag', (e, d) => { d.fx = e.x; d.fy = e.y; })
            .on('end', (e, d) => {
                const sim = S().sim; if (!sim) return;
                if (!e.active) sim.alphaTarget(0);
                d.fx = null; d.fy = null;
                sim.on('end', () => { prevPos = new Map(nodeSel.data().map(n => [n.id, { x: n.x, y: n.y }])); });
            });
    }

    // ── Vurgulama: düğümden köke giden yol ──────────────────────────────
    G.highlight = function (d) {
        if (!svg) return;
        if (!d) {
            svg.classed('is-hovering', false);
            gNodes.selectAll('g.gx-node.is-hl').classed('is-hl', false);
            gLinks.selectAll('path.gx-link.is-hl').classed('is-hl', false);
            return;
        }
        const ids = new Set();
        for (let p = d; p; p = p.parent) ids.add(p.id);
        (d.children || []).forEach(c => ids.add(c.id));
        svg.classed('is-hovering', true);
        gNodes.selectAll('g.gx-node').classed('is-hl', n => ids.has(n.id));
        gLinks.selectAll('path.gx-link').classed('is-hl', l => ids.has(l.target.id) && (ids.has(l.source.id)));
    };

    // ── Görünür alan: yüzen panellerin kapattığı bölgeler dışarıda ─────
    G.safeRect = function () {
        const host = document.getElementById('graph-canvas');
        const cv = host.getBoundingClientRect();
        let r = { l: 0, t: 0, r: cv.width, b: cv.height };
        const panels = document.querySelectorAll('#gx .gx-top > .gx-panel, #gx .gx-side, #gx .gx-zoom, #gx .gx-help');
        panels.forEach(p => {
            if (p.hidden || p.offsetParent === null) return;
            const b = p.getBoundingClientRect();
            const pl = b.left - cv.left, pt = b.top - cv.top, pr = b.right - cv.left, pb = b.bottom - cv.top;
            if (pr <= r.l || pl >= r.r || pb <= r.t || pt >= r.b) return;
            const opts = [
                { l: r.l, t: Math.max(r.t, pb + 8), r: r.r, b: r.b },
                { l: r.l, t: r.t, r: r.r, b: Math.min(r.b, pt - 8) },
                { l: Math.max(r.l, pr + 8), t: r.t, r: r.r, b: r.b },
                { l: r.l, t: r.t, r: Math.min(r.r, pl - 8), b: r.b }
            ].filter(o => o.r - o.l > 80 && o.b - o.t > 80);
            if (!opts.length) return;
            opts.sort((a, b2) => (b2.r - b2.l) * (b2.b - b2.t) - (a.r - a.l) * (a.b - a.t));
            r = opts[0];
        });
        const pad = 12;
        return { x: r.l + pad, y: r.t + pad, w: Math.max(40, r.r - r.l - pad * 2), h: Math.max(40, r.b - r.t - pad * 2) };
    };

    G.fit = function (duration, bounds) {
        if (!svg || !S().root) return;
        const b = bounds || targetBounds(S().root.descendants()) || viewport.node().getBBox();
        if (!b || !b.width || !b.height) return;
        const s = G.safeRect();
        const k = Math.max(0.04, Math.min(1.25, Math.min(s.w / b.width, s.h / b.height)));
        const tx = s.x + s.w / 2 - (b.x + b.width / 2) * k;
        const ty = s.y + s.h / 2 - (b.y + b.height / 2) * k;
        const t = d3.zoomIdentity.translate(tx, ty).scale(k);
        const d = duration == null ? 520 : duration;
        (d && !reduced ? svg.transition().duration(d).ease(d3.easeCubicOut) : svg).call(zoom.transform, t);
    };
    G.zoomBy = function (factor) {
        if (!svg) return;
        const s = G.safeRect();
        svg.transition().duration(reduced ? 0 : 280).call(zoom.scaleBy, factor, [s.x + s.w / 2, s.y + s.h / 2]);
        S().userZoomed = true;
    };
    G.focusOn = function (d) {
        if (!svg || !d) return;
        const s = G.safeRect();
        const k = Math.max(1.4, d3.zoomTransform(svg.node()).k);
        const t = d3.zoomIdentity.translate(s.x + s.w / 2 - d.x * k, s.y + s.h / 2 - d.y * k).scale(k);
        svg.transition().duration(reduced ? 0 : 600).ease(d3.easeCubicInOut).call(zoom.transform, t);
        S().userZoomed = true;
        const el = gNodes.selectAll('g.gx-node').filter(n => n.id === d.id);
        el.classed('is-pinged', false); void (el.node() && el.node().getBBox());
        el.classed('is-pinged', true);
        setTimeout(() => el.classed('is-pinged', false), 1600);
    };
    /** Düğümün ekran (viewport) koordinatı — klavye ile menü açarken. */
    G.screenPos = function (d) {
        const t = d3.zoomTransform(svg.node());
        const r = svg.node().getBoundingClientRect();
        return { x: r.left + t.applyX(d.x), y: r.top + t.applyY(d.y) };
    };
})();
