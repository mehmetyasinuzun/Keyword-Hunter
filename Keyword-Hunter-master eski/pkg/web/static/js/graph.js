/**
 * KeywordHunter - Graph Visualization JavaScript
 * Modular, maintainable graph visualization with multiple layout options
 */

// =============================================================================
// GLOBAL STATE
// =============================================================================
const GraphState = {
    data: null,
    root: null,
    svg: null,
    g: null,
    zoom: null,
    currentLayout: 'radial',
    selectedNode: null,
    nodeId: 0,
    width: window.innerWidth,
    height: window.innerHeight - 94, // Adjusted for query panel
    duration: 600,
    queries: []
};

// Graf Ayarları (dinamik olarak değiştirilebilir)
const GraphSettings = {
    fontSize: 14,
    spacing: 1,
    truncateLength: 25,
    showAllLabels: true
};

// =============================================================================
// SETTINGS PANEL FUNCTIONS
// =============================================================================
function toggleSettingsPanel() {
    const panel = document.getElementById('settings-panel');
    panel.style.display = panel.style.display === 'none' ? 'block' : 'none';
}

function updateGraphSettings() {
    const fontSize = document.getElementById('font-size-slider').value;
    const spacing = document.getElementById('spacing-slider').value;
    const truncateLength = document.getElementById('truncate-slider').value;
    const showAllLabels = document.getElementById('show-all-labels').checked;
    
    document.getElementById('font-size-value').textContent = fontSize + 'px';
    document.getElementById('spacing-value').textContent = spacing + 'x';
    document.getElementById('truncate-value').textContent = truncateLength + ' kar.';
    
    GraphSettings.fontSize = parseInt(fontSize);
    GraphSettings.spacing = parseFloat(spacing);
    GraphSettings.truncateLength = parseInt(truncateLength);
    GraphSettings.showAllLabels = showAllLabels;
    
    // Değişiklikleri anında uygula
    renderLayout();
}

function applyGraphSettings() {
    updateGraphSettings();
    renderLayout();
    showToast('Ayarlar uygulandı', 'success');
}

function resetGraphSettings() {
    document.getElementById('font-size-slider').value = 14;
    document.getElementById('spacing-slider').value = 1;
    document.getElementById('truncate-slider').value = 25;
    document.getElementById('show-all-labels').checked = true;
    
    GraphSettings.fontSize = 14;
    GraphSettings.spacing = 1;
    GraphSettings.truncateLength = 25;
    GraphSettings.showAllLabels = true;
    
    updateGraphSettings();
    renderLayout();
    showToast('Ayarlar sıfırlandı', 'info');
}

// =============================================================================
// QUERY MANAGEMENT
// =============================================================================
async function loadQueries() {
    try {
        const response = await fetch('/api/queries');
        const data = await response.json();
        GraphState.queries = data.queries || [];
        
        const select = document.getElementById('query-select');
        select.innerHTML = '<option value="">Tüm Sorgular</option>';
        
        GraphState.queries.forEach(q => {
            const option = document.createElement('option');
            option.value = q.query;
            option.textContent = `${q.query} (${q.count} sonuç)`;
            select.appendChild(option);
        });
        
        return GraphState.queries;
    } catch (error) {
        console.error('Sorgular yüklenemedi:', error);
        return [];
    }
}

function loadSelectedQuery() {
    const select = document.getElementById('query-select');
    const query = select.value;
    window.currentQuery = query;
    
    // Update URL without reload
    const url = new URL(window.location);
    if (query) {
        url.searchParams.set('q', query);
    } else {
        url.searchParams.delete('q');
    }
    window.history.pushState({}, '', url);
    
    // Update label
    updateQueryLabel(query);
    
    // Reload graph
    initGraph(query);
}

function updateQueryLabel(query) {
    const label = document.getElementById('current-query-label');
    if (query) {
        label.textContent = `"${query}" için harita`;
    } else {
        label.textContent = 'Tüm sorgular';
    }
}

async function quickSearch() {
    const input = document.getElementById('quick-search');
    const query = input.value.trim();
    
    if (!query) {
        showToast('⚠️ Sorgu girin', 'warning');
        return;
    }
    
    showToast('🔍 Aranıyor...', 'info');
    
    try {
        const response = await fetch('/search', {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            body: `query=${encodeURIComponent(query)}`
        });
        
        if (response.ok) {
            // Reload queries and select the new one
            await loadQueries();
            document.getElementById('query-select').value = query;
            window.currentQuery = query;
            updateQueryLabel(query);
            initGraph(query);
            input.value = '';
            showToast('✅ Arama tamamlandı', 'success');
        } else {
            showToast('❌ Arama başarısız', 'error');
        }
    } catch (error) {
        showToast('❌ Bağlantı hatası', 'error');
    }
}

// =============================================================================
// INITIALIZATION
// =============================================================================
async function initGraph(queryParam = '') {
    try {
        const url = '/api/graph' + (queryParam ? `?q=${encodeURIComponent(queryParam)}` : '');
        const response = await fetch(url);
        GraphState.data = await response.json();
        
        if (!GraphState.data || !GraphState.data.children || GraphState.data.children.length === 0) {
            showEmptyState();
            return;
        }
        
        // If single query selected, use that query as root instead of "KeywordHunter"
        let graphData = GraphState.data;
        if (queryParam && GraphState.data.children && GraphState.data.children.length === 1) {
            // Single query - make it the root
            graphData = GraphState.data.children[0];
            graphData.type = 'query'; // Ensure type is set
        } else if (queryParam && GraphState.data.children) {
            // Find the matching query
            const matchingQuery = GraphState.data.children.find(c => c.name === queryParam);
            if (matchingQuery) {
                graphData = matchingQuery;
                graphData.type = 'query';
            }
        }
        
        updateStats(graphData);
        updateQueryLabel(queryParam);
        createGraph(graphData);
    } catch (error) {
        console.error('Graph verisi yüklenemedi:', error);
        showEmptyState();
    }
}

function showEmptyState() {
    document.getElementById('graph-container').innerHTML = `
        <div class="flex items-center justify-center h-full">
            <div class="text-center text-gray-400">
                <p class="text-6xl mb-4">📭</p>
                <p class="text-xl mb-2">Henüz sonuç bulunamadı</p>
                <p class="mb-4">Arama yaparak başlayın</p>
                <a href="/search" class="bg-blue-600 hover:bg-blue-700 text-white px-6 py-3 rounded-lg inline-block">
                    🔍 Arama Yap
                </a>
            </div>
        </div>
    `;
}

function updateStats(data) {
    let queries = 0, engines = 0, results = 0, multi = 0;
    
    // Check if root is query (single query view) or root (all queries view)
    if (data.type === 'query') {
        // Single query view - root is the query itself
        queries = 1;
        if (data.children) {
            engines = data.children.length;
            data.children.forEach(engine => {
                if (engine.children) {
                    results += engine.children.length;
                    engine.children.forEach(result => {
                        if (result.count > 1) multi++;
                    });
                }
            });
        }
    } else if (data.children) {
        // All queries view
        queries = data.children.length;
        data.children.forEach(query => {
            if (query.children) {
                engines += query.children.length;
                query.children.forEach(engine => {
                    if (engine.children) {
                        results += engine.children.length;
                        engine.children.forEach(result => {
                            if (result.count > 1) multi++;
                        });
                    }
                });
            }
        });
    }
    
    document.getElementById('stat-queries').textContent = queries;
    document.getElementById('stat-engines').textContent = engines;
    document.getElementById('stat-results').textContent = results;
    document.getElementById('stat-multi').textContent = multi;
}

// =============================================================================
// GRAPH CREATION
// =============================================================================
function createGraph(data) {
    d3.select('#graph-container svg').remove();
    
    GraphState.svg = d3.select('#graph-container')
        .append('svg')
        .attr('width', '100%')
        .attr('height', '100%');
    
    // Zoom behavior
    GraphState.zoom = d3.zoom()
        .scaleExtent([0.1, 4])
        .on('zoom', (event) => {
            GraphState.g.attr('transform', event.transform);
            updateSemanticZoom(event.transform.k);
        });
    
    GraphState.svg.call(GraphState.zoom)
        .on('contextmenu', (e) => e.preventDefault())
        .on('dblclick.zoom', null);
    
    GraphState.g = GraphState.svg.append('g');
    
    // Create hierarchy
    GraphState.root = d3.hierarchy(data);
    GraphState.root.x0 = GraphState.height / 2;
    GraphState.root.y0 = GraphState.width / 2;
    
    // Default collapsed: collapse all after depth 2
    if (GraphState.root.children) {
        GraphState.root.children.forEach(child => {
            if (child.children) {
                child.children.forEach(grandchild => {
                    collapse(grandchild);
                });
            }
        });
    }
    
    renderLayout();
    setTimeout(fitToScreen, 100);
}

// =============================================================================
// LAYOUT RENDERING
// =============================================================================
function renderLayout() {
    switch(GraphState.currentLayout) {
        case 'radial':
            renderRadialTree();
            break;
        case 'tree':
            renderHorizontalTree();
            break;
        case 'force':
            renderForceLayout();
            break;
    }
}

function renderRadialTree() {
    const nodes = GraphState.root.descendants();
    const nodeCount = nodes.length;
    
    // Dinamik radius - node sayısı ve spacing ayarına göre
    const baseRadius = Math.min(GraphState.width, GraphState.height) / 2 - 100;
    const radius = Math.max(baseRadius, nodeCount * GraphSettings.spacing * 2);
    
    const treeLayout = d3.tree()
        .size([2 * Math.PI, radius])
        .separation((a, b) => {
            const baseSep = a.parent === b.parent ? 1 : 2;
            return (baseSep * GraphSettings.spacing) / Math.max(a.depth, 0.5);
        });
    
    treeLayout(GraphState.root);
    
    GraphState.g.selectAll('*').remove();
    
    const gTransform = GraphState.g.append('g')
        .attr('transform', `translate(${GraphState.width/2}, ${GraphState.height/2})`);
    
    // Links
    gTransform.selectAll('.link')
        .data(GraphState.root.links())
        .enter()
        .append('path')
        .attr('class', d => `link link-${d.source.data.type}`)
        .attr('d', d3.linkRadial()
            .angle(d => d.x)
            .radius(d => d.y));
    
    // Nodes
    const node = gTransform.selectAll('.node')
        .data(nodes)
        .enter()
        .append('g')
        .attr('class', d => getNodeClass(d))
        .attr('transform', d => `rotate(${d.x * 180 / Math.PI - 90}) translate(${d.y}, 0)`)
        .on('click', handleNodeClick)
        .on('contextmenu', handleContextMenu)
        .on('mouseover', showTooltip)
        .on('mouseout', hideTooltip);
    
    node.append('circle')
        .attr('r', d => getNodeRadius(d));
    
    // Yazı görünürlüğü ve boyutu ayarlara göre
    const textNodes = GraphSettings.showAllLabels ? node : node.filter(d => d.depth <= 2);
    
    textNodes.append('text')
        .attr('dy', '0.31em')
        .attr('x', d => d.x < Math.PI === !d.children ? 10 : -10)
        .attr('text-anchor', d => d.x < Math.PI === !d.children ? 'start' : 'end')
        .attr('transform', d => d.x >= Math.PI ? 'rotate(180)' : null)
        .attr('font-size', d => getFontSize(d) + 'px')
        .attr('font-weight', d => d.depth <= 1 ? '600' : '400')
        .attr('fill', d => d.depth <= 2 ? '#fff' : '#e2e8f0')
        .text(d => truncate(d.data.name, getTruncateLength(d)))
        .clone(true).lower()
        .attr('stroke', '#0f0f1a')
        .attr('stroke-width', 3);
}

function renderHorizontalTree() {
    const nodes = GraphState.root.descendants();
    const nodeCount = nodes.length;
    
    // Dinamik yükseklik - node sayısı ve spacing ayarına göre
    const dynamicHeight = Math.max(GraphState.height - 100, nodeCount * 20 * GraphSettings.spacing);
    
    const treeLayout = d3.tree()
        .size([dynamicHeight, GraphState.width - 350])
        .separation((a, b) => (a.parent === b.parent ? 1 : 2) * GraphSettings.spacing);
    
    treeLayout(GraphState.root);
    
    GraphState.g.selectAll('*').remove();
    
    const gTransform = GraphState.g.append('g')
        .attr('transform', 'translate(180, 50)');
    
    // Links
    gTransform.selectAll('.link')
        .data(GraphState.root.links())
        .enter()
        .append('path')
        .attr('class', d => `link link-${d.source.data.type}`)
        .attr('d', d => `
            M ${d.source.y} ${d.source.x}
            C ${(d.source.y + d.target.y) / 2} ${d.source.x},
              ${(d.source.y + d.target.y) / 2} ${d.target.x},
              ${d.target.y} ${d.target.x}
        `);
    
    // Nodes
    const node = gTransform.selectAll('.node')
        .data(nodes)
        .enter()
        .append('g')
        .attr('class', d => getNodeClass(d))
        .attr('transform', d => `translate(${d.y}, ${d.x})`)
        .on('click', handleNodeClick)
        .on('contextmenu', handleContextMenu)
        .on('mouseover', showTooltip)
        .on('mouseout', hideTooltip);
    
    node.append('circle')
        .attr('r', d => getNodeRadius(d));
    
    // Yazı görünürlüğü ve boyutu ayarlara göre
    const textNodes = GraphSettings.showAllLabels ? node : node.filter(d => d.depth <= 2);
    
    textNodes.append('text')
        .attr('dy', '0.31em')
        .attr('x', d => d.children ? -12 : 12)
        .attr('text-anchor', d => d.children ? 'end' : 'start')
        .attr('font-size', d => getFontSize(d) + 'px')
        .attr('font-weight', d => d.depth <= 1 ? '600' : '400')
        .attr('fill', d => d.depth <= 2 ? '#fff' : '#e2e8f0')
        .text(d => truncate(d.data.name, getTruncateLength(d)))
        .clone(true).lower()
        .attr('stroke', '#0f0f1a')
        .attr('stroke-width', 3);
}

function renderForceLayout() {
    const nodes = GraphState.root.descendants();
    const links = GraphState.root.links();
    const nodeCount = nodes.length;
    
    GraphState.g.selectAll('*').remove();
    
    // Dinamik force değerleri - spacing ayarına göre
    const chargeStrength = Math.max(-500, -200 - (nodeCount * GraphSettings.spacing));
    const linkDistance = 60 * GraphSettings.spacing;
    const collisionRadius = 20 * GraphSettings.spacing;
    
    const simulation = d3.forceSimulation(nodes)
        .force('link', d3.forceLink(links).id(d => d.id).distance(linkDistance).strength(0.8))
        .force('charge', d3.forceManyBody().strength(chargeStrength))
        .force('center', d3.forceCenter(GraphState.width / 2, GraphState.height / 2))
        .force('collision', d3.forceCollide().radius(collisionRadius));
    
    const link = GraphState.g.selectAll('.link')
        .data(links)
        .enter()
        .append('line')
        .attr('class', d => `link link-${d.source.data.type}`);
    
    const node = GraphState.g.selectAll('.node')
        .data(nodes)
        .enter()
        .append('g')
        .attr('class', d => getNodeClass(d))
        .call(d3.drag()
            .on('start', (event, d) => {
                if (!event.active) simulation.alphaTarget(0.3).restart();
                d.fx = d.x; d.fy = d.y;
            })
            .on('drag', (event, d) => { d.fx = event.x; d.fy = event.y; })
            .on('end', (event, d) => {
                if (!event.active) simulation.alphaTarget(0);
                d.fx = null; d.fy = null;
            }))
        .on('click', handleNodeClick)
        .on('contextmenu', handleContextMenu)
        .on('mouseover', showTooltip)
        .on('mouseout', hideTooltip);
    
    node.append('circle').attr('r', d => getNodeRadius(d));
    
    // Yazı görünürlüğü ve boyutu ayarlara göre
    const textNodes = GraphSettings.showAllLabels ? node : node.filter(d => d.depth <= 2);
    
    textNodes.append('text')
        .attr('dy', -15)
        .attr('text-anchor', 'middle')
        .attr('font-size', d => getFontSize(d) + 'px')
        .attr('font-weight', d => d.depth <= 1 ? '600' : '400')
        .attr('fill', d => d.depth <= 2 ? '#fff' : '#e2e8f0')
        .text(d => truncate(d.data.name, getTruncateLength(d)))
        .clone(true).lower()
        .attr('stroke', '#0f0f1a')
        .attr('stroke-width', 3);
    
    simulation.on('tick', () => {
        link.attr('x1', d => d.source.x).attr('y1', d => d.source.y)
            .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
        node.attr('transform', d => `translate(${d.x}, ${d.y})`);
    });
}

// Dinamik font boyutu hesaplama
function getFontSize(d) {
    const baseSize = GraphSettings.fontSize;
    if (d.depth === 0) return baseSize + 4;
    if (d.depth === 1) return baseSize + 2;
    if (d.depth === 2) return baseSize;
    return Math.max(baseSize - 1, 10);
}

// Dinamik truncate uzunluğu hesaplama
function getTruncateLength(d) {
    const baseLength = GraphSettings.truncateLength;
    if (d.depth <= 2) return baseLength + 10;
    return baseLength;
}

// =============================================================================
// NODE HELPERS
// =============================================================================
function getNodeClass(d) {
    let cls = 'node';
    if (d.data.count > 1) cls += ' node-multi';
    else cls += ` node-${d.data.type}`;
    return cls;
}

function getNodeRadius(d) {
    const baseRadius = {
        'root': 18, 'query': 14, 'engine': 11, 'result': 8,
        'internal-group': 10, 'external-group': 10, 'link': 6
    };
    let r = baseRadius[d.data.type] || 6;
    if (d.data.count > 1) r += 2;
    return r;
}

function truncate(str, len) {
    if (!str) return '';
    return str.length > len ? str.substring(0, len) + '…' : str;
}

// =============================================================================
// COLLAPSE / EXPAND
// =============================================================================
function collapse(d) {
    if (d.children) {
        d._children = d.children;
        d._children.forEach(collapse);
        d.children = null;
    }
}

function expand(d) {
    if (d._children) {
        d.children = d._children;
        d._children = null;
        d.children.forEach(expand);
    }
}

function toggle(d) {
    if (d.children) {
        d._children = d.children;
        d.children = null;
    } else if (d._children) {
        d.children = d._children;
        d._children = null;
    }
}

function expandAll() {
    if (GraphState.root) {
        expand(GraphState.root);
        renderLayout();
        setTimeout(fitToScreen, 100);
    }
}

function collapseAll() {
    if (GraphState.root && GraphState.root.children) {
        GraphState.root.children.forEach(collapse);
        renderLayout();
        setTimeout(fitToScreen, 100);
    }
}

// Full Map - Tüm derinleştirmeleri ve node'ları açar
function showFullMap() {
    if (!GraphState.root) {
        showToast('⚠️ Graf verisi yok', 'warning');
        return;
    }
    
    // Tüm node'ları recursive olarak aç
    expandAllDeep(GraphState.root);
    
    showToast('🗺️ Tam harita görünümü', 'success');
    renderLayout();
    setTimeout(fitToScreen, 200);
}

// Tüm node'ları tamamen açar (collapse edilmiş olanlar dahil)
function expandAllDeep(node) {
    if (!node) return;
    
    // Eğer _children varsa (collapsed), aç
    if (node._children) {
        node.children = node._children;
        node._children = null;
    }
    
    // Children varsa onları da recursive aç
    if (node.children) {
        node.children.forEach(child => expandAllDeep(child));
    }
}

// =============================================================================
// EVENT HANDLERS
// =============================================================================
function handleNodeClick(event, d) {
    event.stopPropagation();
    if (d.children || d._children) {
        toggle(d);
        renderLayout();
    } else if (d.data.url) {
        GraphState.selectedNode = d;
        copyLink();
    }
}

function handleContextMenu(event, d) {
    event.preventDefault();
    event.stopPropagation();
    if (d.data.url || d.children || d._children) {
        GraphState.selectedNode = d;
        showContextMenu(event.pageX, event.pageY);
    }
}

// =============================================================================
// ZOOM & VIEW CONTROLS
// =============================================================================
function zoomIn() {
    GraphState.svg.transition().duration(300).call(GraphState.zoom.scaleBy, 1.5);
}

function zoomOut() {
    GraphState.svg.transition().duration(300).call(GraphState.zoom.scaleBy, 0.67);
}

function resetView() {
    GraphState.svg.transition().duration(500).call(GraphState.zoom.transform, d3.zoomIdentity);
}

function fitToScreen() {
    const bounds = GraphState.g.node().getBBox();
    const scale = 0.85 / Math.max(bounds.width / GraphState.width, bounds.height / GraphState.height);
    const tx = (GraphState.width - bounds.width * scale) / 2 - bounds.x * scale;
    const ty = (GraphState.height - bounds.height * scale) / 2 - bounds.y * scale;
    
    GraphState.svg.transition().duration(500).call(
        GraphState.zoom.transform,
        d3.zoomIdentity.translate(tx, ty).scale(scale)
    );
}

function updateSemanticZoom(scale) {
    const container = document.getElementById('graph-container');
    container.classList.remove('zoom-level-1', 'zoom-level-2', 'zoom-level-3');
    
    if (scale < 0.4) container.classList.add('zoom-level-1');
    else if (scale < 0.8) container.classList.add('zoom-level-2');
    else container.classList.add('zoom-level-3');
}

function focusNode() {
    closeContextMenu();
    if (!GraphState.selectedNode) return;
    
    GraphState.svg.transition().duration(500).call(
        GraphState.zoom.transform,
        d3.zoomIdentity
            .translate(GraphState.width/2 - (GraphState.selectedNode.y || 0) * 2, 
                      GraphState.height/2 - (GraphState.selectedNode.x || 0) * 2)
            .scale(2)
    );
}

// =============================================================================
// LAYOUT SWITCHING
// =============================================================================
function setLayout(layout) {
    GraphState.currentLayout = layout;
    
    document.querySelectorAll('.layout-btn').forEach(btn => btn.classList.remove('active'));
    document.getElementById(`layout-${layout}`).classList.add('active');
    
    renderLayout();
    setTimeout(fitToScreen, 100);
}

// =============================================================================
// TOOLTIP
// =============================================================================
function showTooltip(event, d) {
    const tooltip = document.getElementById('tooltip');
    let content = `<div class="title">${d.data.name}</div>`;
    
    if (d.data.url) {
        content += `<div class="url">${d.data.url}</div>`;
        if (d.data.url.includes('.onion')) {
            content += `<div class="onion-warning">🧅 Tor Browser gerektirir</div>`;
        }
    }
    
    if (d.data.count > 1) {
        content += `<div class="badge">🔥 ${d.data.count} motorda bulundu</div>`;
    }
    
    const childCount = (d._children || d.children || []).length;
    if (childCount > 0) {
        content += `<div style="margin-top: 8px; color: #a0aec0; font-size: 11px;">📂 ${childCount} alt öğe</div>`;
    }
    
    tooltip.innerHTML = content;
    tooltip.style.display = 'block';
    tooltip.style.left = (event.pageX + 15) + 'px';
    tooltip.style.top = (event.pageY - 10) + 'px';
}

function hideTooltip() {
    document.getElementById('tooltip').style.display = 'none';
}

// =============================================================================
// CONTEXT MENU
// =============================================================================
function showContextMenu(x, y) {
    const menu = document.getElementById('context-menu');
    const expandItem = document.getElementById('expand-menu-item');
    
    if (GraphState.selectedNode && GraphState.selectedNode.data.url) {
        const isOnion = GraphState.selectedNode.data.url.includes('.onion');
        if (GraphState.selectedNode.data.isExpanded) {
            expandItem.innerHTML = '✅ Derinleştirildi';
            expandItem.style.opacity = '0.5';
        } else if (!isOnion) {
            expandItem.innerHTML = '⚠️ Sadece .onion';
            expandItem.style.opacity = '0.5';
        } else {
            expandItem.innerHTML = '🔍 Derinleştir';
            expandItem.style.opacity = '1';
        }
        expandItem.style.display = 'flex';
    } else {
        expandItem.style.display = 'none';
    }
    
    menu.style.display = 'block';
    menu.style.left = x + 'px';
    menu.style.top = y + 'px';
}

function closeContextMenu() {
    document.getElementById('context-menu').style.display = 'none';
}

// =============================================================================
// COPY FUNCTIONS
// =============================================================================
function copyLink() {
    closeContextMenu();
    if (GraphState.selectedNode && GraphState.selectedNode.data.url) {
        navigator.clipboard.writeText(GraphState.selectedNode.data.url).then(() => {
            showToast('✅ Link kopyalandı!');
        }).catch(() => fallbackCopy(GraphState.selectedNode.data.url));
    }
}

function copyTitle() {
    closeContextMenu();
    if (GraphState.selectedNode && GraphState.selectedNode.data.name) {
        navigator.clipboard.writeText(GraphState.selectedNode.data.name).then(() => {
            showToast('✅ Başlık kopyalandı!');
        }).catch(() => fallbackCopy(GraphState.selectedNode.data.name));
    }
}

function fallbackCopy(text) {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.style.cssText = 'position:fixed;opacity:0';
    document.body.appendChild(ta);
    ta.select();
    try {
        document.execCommand('copy');
        showToast('✅ Kopyalandı!');
    } catch (e) {
        showToast('❌ Kopyalama başarısız', 'error');
    }
    document.body.removeChild(ta);
}

function copyLinkFromModal() {
    if (GraphState.selectedNode && GraphState.selectedNode.data.url) {
        navigator.clipboard.writeText(GraphState.selectedNode.data.url).then(() => {
            showToast('✅ Link kopyalandı!');
        }).catch(() => fallbackCopy(GraphState.selectedNode.data.url));
    }
}

// =============================================================================
// LINK INFO MODAL
// =============================================================================
function showLinkInfo() {
    closeContextMenu();
    if (!GraphState.selectedNode) return;
    
    const url = GraphState.selectedNode.data.url || '';
    const isOnion = url.includes('.onion');
    
    let content = `
        <div style="margin-bottom: 16px;">
            <label style="color: #a0aec0; font-size: 12px;">Başlık:</label>
            <p style="color: #e2e8f0; margin-top: 4px;">${GraphState.selectedNode.data.name}</p>
        </div>
    `;
    
    if (url) {
        content += `
            <div style="margin-bottom: 16px;">
                <label style="color: #a0aec0; font-size: 12px;">URL:</label>
                <p style="color: #63b3ed; margin-top: 4px; word-break: break-all; font-family: monospace; background: #1a202c; padding: 12px; border-radius: 8px; font-size: 12px;">${url}</p>
            </div>
        `;
    }
    
    if (isOnion) {
        content += `
            <div style="background: linear-gradient(135deg, #553c9a, #44337a); padding: 16px; border-radius: 12px; margin-bottom: 16px;">
                <p style="color: #e9d8fd; font-weight: 600;">🧅 Onion Linki</p>
                <p style="color: #d6bcfa; font-size: 12px; margin-top: 8px;">Dark Web adresi - Tor Browser gerektirir</p>
            </div>
        `;
    }
    
    if (GraphState.selectedNode.data.count > 1) {
        content += `
            <div style="background: linear-gradient(135deg, #744210, #5a3511); padding: 16px; border-radius: 12px;">
                <p style="color: #fbd38d; font-weight: 600;">🔥 ${GraphState.selectedNode.data.count} motorda bulundu</p>
                <p style="color: #f6e05e; font-size: 12px; margin-top: 4px;">Yüksek güvenilirlik</p>
            </div>
        `;
    }
    
    document.getElementById('link-modal-content').innerHTML = content;
    document.getElementById('link-modal').style.display = 'flex';
}

function closeLinkModal() {
    document.getElementById('link-modal').style.display = 'none';
}

// =============================================================================
// TOAST NOTIFICATIONS
// =============================================================================
function showToast(message, type = 'success') {
    const toast = document.getElementById('toast');
    toast.textContent = message;
    toast.className = 'toast show ' + type;
    setTimeout(() => { toast.className = 'toast'; }, 3000);
}

function showLoading(show) {
    document.getElementById('expand-loading').classList.toggle('show', show);
}

// =============================================================================
// EXPAND (DEEPEN) FUNCTIONALITY
// =============================================================================
async function expandNode() {
    closeContextMenu();
    
    if (!GraphState.selectedNode || !GraphState.selectedNode.data.url) {
        showToast('❌ Geçersiz seçim', 'error');
        return;
    }
    
    if (GraphState.selectedNode.data.isExpanded) {
        showToast('ℹ️ Zaten derinleştirilmiş', 'info');
        return;
    }
    
    const url = GraphState.selectedNode.data.url;
    if (!url.includes('.onion')) {
        showToast('⚠️ Sadece .onion adresleri', 'warning');
        return;
    }
    
    showToast('🧅 Taranıyor...', 'info');
    showLoading(true);
    
    try {
        const controller = new AbortController();
        const timeoutId = setTimeout(() => controller.abort(), 120000);
        
        const response = await fetch('/api/expand', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                url: url,
                parentId: GraphState.selectedNode.data.nodeId || 0,
                query: window.currentQuery || ''
            }),
            signal: controller.signal
        });
        
        clearTimeout(timeoutId);
        const data = await response.json();
        
        if (!data.success) {
            showLoading(false);
            showToast('❌ ' + (data.error || 'Başarısız'), 'error');
            return;
        }
        
        if (data.children && data.children.length > 0) {
            addChildrenToNode(GraphState.selectedNode, data.children);
            GraphState.selectedNode.data.isExpanded = true;
            showToast(`✅ ${data.totalLinks} link bulundu`, 'success');
        } else {
            showToast('ℹ️ Link bulunamadı', 'info');
        }
        
    } catch (error) {
        if (error.name === 'AbortError') {
            showToast('⏰ Zaman aşımı', 'error');
        } else {
            showToast('❌ Bağlantı hatası', 'error');
        }
    } finally {
        showLoading(false);
    }
}

function addChildrenToNode(parentNode, childrenData) {
    const newChildren = childrenData.map(child => {
        const childHierarchy = d3.hierarchy(child);
        childHierarchy.depth = parentNode.depth + 1;
        childHierarchy.parent = parentNode;
        processHierarchy(childHierarchy, parentNode.depth + 1);
        return childHierarchy;
    });
    
    if (!parentNode.children) parentNode.children = [];
    parentNode.children = parentNode.children.concat(newChildren);
    if (parentNode._children) parentNode._children = parentNode._children.concat(newChildren);
    
    renderLayout();
}

function processHierarchy(node, depth) {
    node.depth = depth;
    node.id = ++GraphState.nodeId;
    
    if (node.data.children) {
        node.children = node.data.children.map(childData => {
            const child = d3.hierarchy(childData);
            child.parent = node;
            processHierarchy(child, depth + 1);
            return child;
        });
        node._children = node.children;
        node.children = null;
    }
}

// =============================================================================
// EVENT LISTENERS SETUP
// =============================================================================
function setupEventListeners() {
    document.addEventListener('click', (e) => {
        if (!e.target.closest('.context-menu')) closeContextMenu();
    });
    
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') {
            closeLinkModal();
            closeContextMenu();
        }
    });
    
    window.addEventListener('resize', () => {
        GraphState.width = window.innerWidth;
        GraphState.height = window.innerHeight - 94; // Adjusted for query panel
        if (GraphState.data && GraphState.data.children && GraphState.data.children.length > 0) {
            createGraph(GraphState.data);
        }
    });
}

// Initialize event listeners
setupEventListeners();
