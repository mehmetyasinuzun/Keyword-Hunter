/**
 * KeywordHunter - Graph Visualization JavaScript (UI)
 * UI Interactions, Event Handlers, Controls
 */

// Settings Panel
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

    if (GraphState.root) {
        renderLayout();
    }
}

function applyGraphSettings() {
    updateGraphSettings();
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
    showToast('Ayarlar sıfırlandı', 'info');
}

// Event Handlers
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

// Zoom Controls
function zoomIn() {
    if (!GraphState.svg || !GraphState.zoom) return;
    GraphState.svg.transition().duration(300).call(GraphState.zoom.scaleBy, 1.5);
}

function zoomOut() {
    if (!GraphState.svg || !GraphState.zoom) return;
    GraphState.svg.transition().duration(300).call(GraphState.zoom.scaleBy, 0.67);
}

function resetView() {
    if (!GraphState.svg || !GraphState.zoom) return;
    GraphState.svg.transition().duration(500).call(GraphState.zoom.transform, d3.zoomIdentity);
}

function fitToScreen() {
    if (!GraphState.g || !GraphState.svg || !GraphState.zoom) return;

    const bounds = GraphState.g.node().getBBox();
    if (!bounds || bounds.width <= 0 || bounds.height <= 0) return;

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
    if (!GraphState.selectedNode || !GraphState.svg || !GraphState.zoom) return;

    GraphState.svg.transition().duration(500).call(
        GraphState.zoom.transform,
        d3.zoomIdentity
            .translate(GraphState.width / 2 - (GraphState.selectedNode.y || 0) * 2,
                GraphState.height / 2 - (GraphState.selectedNode.x || 0) * 2)
            .scale(2)
    );
}

function setLayout(layout) {
    GraphState.currentLayout = layout;

    document.querySelectorAll('.layout-btn').forEach(btn => btn.classList.remove('active'));
    document.getElementById(`layout-${layout}`).classList.add('active');

    if (GraphState.root) {
        renderLayout();
        setTimeout(fitToScreen, 100);
    }
}

// Tooltip
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

// Context Menu
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

// Copy
function copyLink() {
    closeContextMenu();
    if (GraphState.selectedNode && GraphState.selectedNode.data.url) {
        navigator.clipboard.writeText(GraphState.selectedNode.data.url).then(() => {
            showToast('✅ Link kopyalandı!');
        }).catch(() => fallbackCopy(GraphState.selectedNode.data.url));
    }
}

// Toast
function showToast(message, type = 'info') {
    const toast = document.getElementById('toast');
    toast.textContent = message;
    toast.style.borderColor = '';
    toast.className = `toast show ${type}`;

    setTimeout(() => {
        toast.className = 'toast';
    }, 3000);
}

function fallbackCopy(text) {
    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();

    try {
        document.execCommand('copy');
        showToast('✅ Kopyalandı', 'success');
    } catch (e) {
        showToast('❌ Kopyalanamadı', 'error');
    }

    document.body.removeChild(textarea);
}

function copyTitle() {
    closeContextMenu();
    if (!GraphState.selectedNode || !GraphState.selectedNode.data) return;

    const title = GraphState.selectedNode.data.name || '';
    if (!title) return;

    navigator.clipboard.writeText(title)
        .then(() => showToast('✅ Başlık kopyalandı', 'success'))
        .catch(() => fallbackCopy(title));
}

function expandNode() {
    closeContextMenu();
    const node = GraphState.selectedNode;
    if (!node || !node.data || !node.data.url) {
        showToast('⚠️ Derinleştirilecek node seçilmedi', 'warning');
        return;
    }

    if (!node.data.url.includes('.onion')) {
        showToast('⚠️ Derinleştirme yalnızca .onion adreslerinde çalışır', 'warning');
        return;
    }

    if (node.data.isExpanded) {
        showToast('ℹ️ Bu node zaten derinleştirilmiş', 'info');
        return;
    }

    const loading = document.getElementById('expand-loading');
    if (loading) loading.style.display = 'flex';

    fetch('/api/expand', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            url: node.data.url,
            parentId: node.data.nodeId || 0,
            query: window.currentQuery || ''
        })
    })
        .then(r => r.json())
        .then(data => {
            if (!data.success) {
                throw new Error(data.error || 'Derinleştirme başarısız');
            }
            showToast(`✅ Derinleştirildi: ${data.savedLinks || 0} kayıt`, 'success');
            node.data.isExpanded = true;
            initGraph(window.currentQuery || '');
        })
        .catch(err => {
            showToast('❌ ' + err.message, 'error');
        })
        .finally(() => {
            if (loading) loading.style.display = 'none';
        });
}

function showLinkInfo() {
    closeContextMenu();
    const node = GraphState.selectedNode;
    if (!node || !node.data) return;

    const modal = document.getElementById('link-modal');
    const content = document.getElementById('link-modal-content');
    if (!modal || !content) return;

    content.innerHTML = `
        <div style="display: grid; gap: 10px; font-size: 14px;">
            <div><strong>Başlık:</strong> ${node.data.name || '-'}</div>
            <div><strong>Tür:</strong> ${node.data.type || '-'}</div>
            <div><strong>URL:</strong><br><span style="color:#63b3ed; word-break: break-all;">${node.data.url || '-'}</span></div>
            <div><strong>Domain:</strong> ${node.data.domain || '-'}</div>
            <div><strong>Node ID:</strong> ${node.data.nodeId || '-'}</div>
            <div><strong>Çoklu Kaynak Sayısı:</strong> ${node.data.count || 1}</div>
        </div>
    `;

    modal.style.display = 'flex';
}

function closeLinkModal() {
    const modal = document.getElementById('link-modal');
    if (modal) {
        modal.style.display = 'none';
    }
}

function copyLinkFromModal() {
    const node = GraphState.selectedNode;
    if (!node || !node.data || !node.data.url) {
        showToast('⚠️ Kopyalanacak link yok', 'warning');
        return;
    }

    navigator.clipboard.writeText(node.data.url)
        .then(() => showToast('✅ Link kopyalandı', 'success'))
        .catch(() => fallbackCopy(node.data.url));
}

document.addEventListener('click', (event) => {
    const menu = document.getElementById('context-menu');
    if (!menu) return;

    if (menu.style.display === 'block' && !menu.contains(event.target)) {
        closeContextMenu();
    }
});

document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape') {
        closeContextMenu();
        closeLinkModal();
    }
});
