/**
 * KeywordHunter - Graph Visualization JavaScript (Render)
 * D3.js Layout & Rendering Logic
 */

function createGraph(data) {
    d3.select('#graph-container svg').remove();
    // Remove any previous empty state
    const emptyState = document.querySelector('#graph-container > div.absolute.inset-0');
    if (emptyState) emptyState.remove();

    GraphState.svg = d3.select('#graph-container')
        .append('svg')
        .attr('width', '100%')
        .attr('height', '100%');

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

    GraphState.root = d3.hierarchy(data);
    GraphState.root.x0 = GraphState.height / 2;
    GraphState.root.y0 = GraphState.width / 2;

    // Collapse deeper levels by default
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

function renderLayout() {
    switch (GraphState.currentLayout) {
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
        .attr('transform', `translate(${GraphState.width / 2}, ${GraphState.height / 2})`);

    gTransform.selectAll('.link')
        .data(GraphState.root.links())
        .enter()
        .append('path')
        .attr('class', d => `link link-${d.source.data.type}`)
        .attr('d', d3.linkRadial()
            .angle(d => d.x)
            .radius(d => d.y));

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

    appendNodeElements(node);
}

function renderHorizontalTree() {
    const nodes = GraphState.root.descendants();
    const nodeCount = nodes.length;

    const dynamicHeight = Math.max(GraphState.height - 100, nodeCount * 20 * GraphSettings.spacing);

    const treeLayout = d3.tree()
        .size([dynamicHeight, GraphState.width - 350])
        .separation((a, b) => (a.parent === b.parent ? 1 : 2) * GraphSettings.spacing);

    treeLayout(GraphState.root);

    GraphState.g.selectAll('*').remove();

    const gTransform = GraphState.g.append('g')
        .attr('transform', 'translate(180, 50)');

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

    appendNodeElements(node, true);
}

function renderForceLayout() {
    const nodes = GraphState.root.descendants();
    const links = GraphState.root.links();
    const nodeCount = nodes.length;

    GraphState.g.selectAll('*').remove();

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

    appendNodeElements(node, false, true);

    simulation.on('tick', () => {
        link.attr('x1', d => d.source.x).attr('y1', d => d.source.y)
            .attr('x2', d => d.target.x).attr('y2', d => d.target.y);
        node.attr('transform', d => `translate(${d.x}, ${d.y})`);
    });
}

function appendNodeElements(nodeSelection, horizontal = false, isForce = false) {
    nodeSelection.append('circle')
        .attr('r', d => getNodeRadius(d));

    const textNodes = GraphSettings.showAllLabels ? nodeSelection : nodeSelection.filter(d => d.depth <= 2);

    textNodes.append('text')
        .attr('dy', isForce ? -15 : '0.31em')
        .attr('x', d => {
            if (isForce) return 0;
            if (horizontal) return d.children ? -12 : 12;
            return d.x < Math.PI === !d.children ? 10 : -10;
        })
        .attr('text-anchor', d => {
            if (isForce) return 'middle';
            if (horizontal) return d.children ? 'end' : 'start';
            return d.x < Math.PI === !d.children ? 'start' : 'end';
        })
        .attr('transform', d => {
            if (isForce || horizontal) return null;
            return d.x >= Math.PI ? 'rotate(180)' : null;
        })
        .attr('font-size', d => getFontSize(d) + 'px')
        .attr('font-weight', d => d.depth <= 1 ? '600' : '400')
        .attr('fill', d => d.depth <= 2 ? '#fff' : '#e2e8f0')
        .text(d => truncate(d.data.name, getTruncateLength(d)))
        .clone(true).lower()
        .attr('stroke', '#0f0f1a')
        .attr('stroke-width', 3);
}

// Helpers
function getFontSize(d) {
    const baseSize = GraphSettings.fontSize;
    if (d.depth === 0) return baseSize + 4;
    if (d.depth === 1) return baseSize + 2;
    if (d.depth === 2) return baseSize;
    return Math.max(baseSize - 1, 10);
}

function getTruncateLength(d) {
    const baseLength = GraphSettings.truncateLength;
    if (d.depth <= 2) return baseLength + 10;
    return baseLength;
}

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

function showFullMap() {
    if (!GraphState.root) {
        showToast('⚠️ Graf verisi yok', 'warning');
        return;
    }
    expandAllDeep(GraphState.root);
    showToast('🗺️ Tam harita görünümü', 'success');
    renderLayout();
    setTimeout(fitToScreen, 200);
}

function expandAllDeep(node) {
    if (!node) return;
    if (node._children) {
        node.children = node._children;
        node._children = null;
    }
    if (node.children) {
        node.children.forEach(child => expandAllDeep(child));
    }
}
