/**
 * KeywordHunter - Graph Visualization JavaScript (Core)
 * Global State, Initialization, Data Loading
 */

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
    height: window.innerHeight - 106, // Navbar + query panel
    duration: 600,
    queries: [],
    activeLoadId: 0,
    activeLoadController: null,
    load: {
        inProgress: false,
        query: '',
        profile: null,
        counts: {
            queries: 0,
            engines: 0,
            results: 0
        }
    }
};

const GRAPH_STAGE_PROFILES = Object.freeze({
    overview: {
        maxQueries: 20,
        maxEnginesPerQuery: 8,
        maxResultsPerEngine: 160,
        resultPageSize: 60,
        maxEnginesTotal: 80,
        renderDebounceMs: 260
    },
    focused: {
        maxQueries: 1,
        maxEnginesPerQuery: 28,
        maxResultsPerEngine: 700,
        resultPageSize: 140,
        maxEnginesTotal: 120,
        renderDebounceMs: 200
    }
});

const GRAPH_FETCH_GUARDS = Object.freeze({
    maxPagesPerEngine: 60
});

let resizeDebounceHandle = null;
let renderDebounceHandle = null;
let lastRenderAt = 0;

const GRAPH_STEP_IDS = Object.freeze({
    queries: 'graph-step-queries',
    engines: 'graph-step-engines',
    results: 'graph-step-results'
});

// Graph Settings
const GraphSettings = {
    fontSize: 14,
    spacing: 1,
    truncateLength: 25,
    showAllLabels: true
};

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
        console.error('Failed to load queries:', error);
        return [];
    }
}

function loadSelectedQuery() {
    const select = document.getElementById('query-select');
    const query = select.value;
    window.currentQuery = query;

    const url = new URL(window.location);
    if (query) {
        url.searchParams.set('q', query);
    } else {
        url.searchParams.delete('q');
    }
    window.history.pushState({}, '', url);

    updateQueryLabel(query);
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
    const normalizedQuery = (queryParam || '').trim();
    const profile = normalizedQuery ? GRAPH_STAGE_PROFILES.focused : GRAPH_STAGE_PROFILES.overview;
    const session = beginLoadSession(normalizedQuery, profile);

    try {
        const root = createGraphRoot();
        GraphState.data = root;

        const querySummaries = await resolveGraphQueries(normalizedQuery, profile, session.signal);
        ensureCurrentLoad(session.requestId);

        if (querySummaries.length === 0) {
            markLoadStep('queries', 'done');
            markLoadStep('engines', 'done');
            markLoadStep('results', 'done');
            setLoadMeta('Veri bulunamadi.');
            showEmptyState();
            finalizeLoadSession(session.requestId);
            return;
        }

        root.children = querySummaries.map(summary => ({
            name: summary.query,
            type: 'query',
            count: Number(summary.count) || 0,
            children: []
        }));

        GraphState.data = root;
        markLoadStep('queries', 'done');
        markLoadStep('engines', 'active');
        updateLoadCounters();
        setLoadMeta(`Sorgular hazir: ${GraphState.load.counts.queries}`);
        scheduleGraphRender({ requestId: session.requestId, force: true });

        await hydrateGraphIncrementally(root, normalizedQuery, profile, session);
        ensureCurrentLoad(session.requestId);

        markLoadStep('engines', 'done');
        markLoadStep('results', 'done');
        updateLoadCounters();
        setLoadMeta(buildLoadSummaryText());
        scheduleGraphRender({ requestId: session.requestId, force: true });
        finalizeLoadSession(session.requestId);
    } catch (error) {
        if (isAbortLikeError(error)) {
            return;
        }

        console.error('Failed to load graph data:', error);

        const hasPartialData = GraphState.data && Array.isArray(GraphState.data.children) && GraphState.data.children.length > 0;
        if (hasPartialData) {
            scheduleGraphRender({ requestId: session.requestId, force: true });
        } else {
            showEmptyState();
        }

        const activeStep = findActiveStep();
        if (activeStep) {
            markLoadStep(activeStep, 'error');
        }

        const message = error && error.message ? error.message : 'Yukleme hatasi';
        setLoadMeta(`Yukleme kesildi: ${message}`);
        finalizeLoadSession(session.requestId);

        if (typeof showToast === 'function') {
            showToast(`❌ Grafik yukleme hatasi: ${message}`, 'error');
        }
    }
}

function beginLoadSession(query, profile) {
    if (GraphState.activeLoadController) {
        GraphState.activeLoadController.abort();
    }

    if (renderDebounceHandle) {
        clearTimeout(renderDebounceHandle);
        renderDebounceHandle = null;
    }

    const controller = new AbortController();
    const requestId = GraphState.activeLoadId + 1;

    GraphState.activeLoadId = requestId;
    GraphState.activeLoadController = controller;
    GraphState.load = {
        inProgress: true,
        query,
        profile,
        counts: {
            queries: 0,
            engines: 0,
            results: 0
        }
    };

    setLoadStatusVisible(true);
    markLoadStep('queries', 'active');
    markLoadStep('engines', 'pending');
    markLoadStep('results', 'pending');
    setLoadMeta('Sorgu listesi aliniyor...');
    updateQueryLabel(query);

    return {
        requestId,
        signal: controller.signal
    };
}

function finalizeLoadSession(requestId) {
    if (requestId !== GraphState.activeLoadId) {
        return;
    }

    GraphState.load.inProgress = false;
    GraphState.activeLoadController = null;
}

function createGraphRoot() {
    return {
        name: '🕵️ KeywordHunter',
        type: 'root',
        children: []
    };
}

async function resolveGraphQueries(queryParam, profile, signal) {
    const params = new URLSearchParams();
    params.set('limit', String(profile.maxQueries));

    if (queryParam) {
        params.set('q', queryParam);
    }

    const data = await fetchJSON('/api/graph/queries?' + params.toString(), signal);
    const items = Array.isArray(data.queries) ? data.queries : [];

    if (queryParam && items.length === 0) {
        return [{ query: queryParam, count: 0 }];
    }

    return items
        .filter(item => item && typeof item.query === 'string' && item.query.trim() !== '')
        .map(item => ({
            query: item.query.trim(),
            count: Number(item.count) || 0
        }));
}

async function hydrateGraphIncrementally(root, queryParam, profile, session) {
    let remainingEngineBudget = profile.maxEnginesTotal;

    for (let queryIndex = 0; queryIndex < root.children.length; queryIndex += 1) {
        const queryNode = root.children[queryIndex];

        if (remainingEngineBudget <= 0) {
            setLoadMeta(`Motor butcesi doldu, kalan sorgular atlandi. ${buildLoadSummaryText()}`);
            break;
        }

        ensureCurrentLoad(session.requestId);
        setLoadMeta(`Motorlar: ${queryIndex + 1}/${root.children.length} - ${queryNode.name}`);

        const engineParams = new URLSearchParams();
        engineParams.set('q', queryNode.name);
        engineParams.set('limit', String(Math.min(profile.maxEnginesPerQuery, remainingEngineBudget)));

        const enginePayload = await fetchJSON('/api/graph/engines?' + engineParams.toString(), session.signal);
        ensureCurrentLoad(session.requestId);

        const engines = Array.isArray(enginePayload.engines) ? enginePayload.engines : [];
        queryNode.children = engines
            .filter(item => item && typeof item.engine === 'string' && item.engine.trim() !== '')
            .map(item => ({
                name: item.engine.trim(),
                type: 'engine',
                count: Number(item.count) || 0,
                children: []
            }));

        remainingEngineBudget -= queryNode.children.length;
        updateLoadCounters();
        scheduleGraphRender({ requestId: session.requestId });

        if (queryNode.children.length === 0) {
            continue;
        }

        markLoadStep('results', 'active');

        for (let engineIndex = 0; engineIndex < queryNode.children.length; engineIndex += 1) {
            const engineNode = queryNode.children[engineIndex];
            ensureCurrentLoad(session.requestId);

            await hydrateEngineResults(queryNode, engineNode, engineIndex, queryNode.children.length, profile, session);
        }

        markLoadStep('engines', 'active');
    }

    if (queryParam) {
        updateQueryLabel(queryParam);
    }
}

async function hydrateEngineResults(queryNode, engineNode, engineIndex, totalEngines, profile, session) {
    let nextOffset = 0;
    let totalLoadedForEngine = 0;
    let pageCount = 0;

    while (totalLoadedForEngine < profile.maxResultsPerEngine) {
        const remaining = profile.maxResultsPerEngine - totalLoadedForEngine;
        const pageLimit = Math.min(profile.resultPageSize, remaining);

        const params = new URLSearchParams();
        params.set('q', queryNode.name);
        params.set('engine', engineNode.name);
        params.set('limit', String(pageLimit));
        params.set('offset', String(nextOffset));

        setLoadMeta(`Sonuclar: ${engineIndex + 1}/${totalEngines} - ${engineNode.name} (${totalLoadedForEngine})`);

        const resultPayload = await fetchJSON('/api/graph/results?' + params.toString(), session.signal);
        ensureCurrentLoad(session.requestId);

        const mappedResults = mapResultItems(resultPayload.results);
        const addedCount = appendUniqueResults(engineNode, mappedResults);

        totalLoadedForEngine += addedCount;
        pageCount += 1;

        if (addedCount > 0) {
            updateLoadCounters();
            scheduleGraphRender({ requestId: session.requestId });
        }

        const resultCount = Array.isArray(resultPayload.results) ? resultPayload.results.length : 0;
        const payloadNextOffset = Number(resultPayload.nextOffset) || 0;

        if (resultCount === 0 || payloadNextOffset <= nextOffset || resultCount < pageLimit) {
            break;
        }

        nextOffset = payloadNextOffset;
        if (pageCount >= GRAPH_FETCH_GUARDS.maxPagesPerEngine) {
            break;
        }
    }
}

function mapResultItems(items) {
    if (!Array.isArray(items)) {
        return [];
    }

    return items
        .map(item => {
            const url = String(item.url || '').trim();
            if (!url) {
                return null;
            }

            const title = String(item.title || '').trim();
            return {
                name: title || url,
                url,
                type: 'result',
                count: Math.max(1, Number(item.sourceCount) || 1),
                nodeId: Number(item.id) || 0,
                isExpanded: Boolean(item.isExpanded),
                domain: String(item.domain || '').trim() || extractDomain(url)
            };
        })
        .filter(Boolean);
}

function appendUniqueResults(engineNode, incomingItems) {
    if (!engineNode.children) {
        engineNode.children = [];
    }

    const seenURLs = new Set(engineNode.children.map(item => item.url));
    let added = 0;

    incomingItems.forEach(item => {
        if (!item.url || seenURLs.has(item.url)) {
            return;
        }

        seenURLs.add(item.url);
        engineNode.children.push(item);
        added += 1;
    });

    return added;
}

function extractDomain(url) {
    try {
        return new URL(url).hostname || '';
    } catch (_error) {
        const withoutProtocol = url.replace(/^https?:\/\//i, '');
        const slashIndex = withoutProtocol.indexOf('/');
        if (slashIndex > -1) {
            return withoutProtocol.slice(0, slashIndex);
        }
        return withoutProtocol;
    }
}

function scheduleGraphRender(options = {}) {
    const requestId = Number(options.requestId || GraphState.activeLoadId);
    const force = Boolean(options.force);

    if (requestId !== GraphState.activeLoadId) {
        return;
    }

    if (!GraphState.data || !Array.isArray(GraphState.data.children)) {
        return;
    }

    const debounceMs = (GraphState.load.profile && GraphState.load.profile.renderDebounceMs) || 220;
    const now = Date.now();

    if (force || now-lastRenderAt >= debounceMs) {
        if (renderDebounceHandle) {
            clearTimeout(renderDebounceHandle);
            renderDebounceHandle = null;
        }
        renderGraphDataSnapshot(requestId);
        return;
    }

    if (renderDebounceHandle) {
        return;
    }

    const wait = Math.max(60, debounceMs - (now - lastRenderAt));
    renderDebounceHandle = setTimeout(() => {
        renderDebounceHandle = null;
        renderGraphDataSnapshot(requestId);
    }, wait);
}

function renderGraphDataSnapshot(requestId) {
    if (requestId !== GraphState.activeLoadId) {
        return;
    }

    if (!GraphState.data || !Array.isArray(GraphState.data.children) || GraphState.data.children.length === 0) {
        showEmptyState();
        return;
    }

    let graphData = GraphState.data;
    const query = (GraphState.load.query || '').trim();

    if (query && GraphState.data.children.length === 1) {
        graphData = GraphState.data.children[0];
        graphData.type = 'query';
    }

    updateStats(graphData);
    updateQueryLabel(query);
    createGraph(graphData);

    lastRenderAt = Date.now();
}

function ensureCurrentLoad(requestId) {
    if (requestId !== GraphState.activeLoadId) {
        throw new DOMException('stale graph request', 'AbortError');
    }
}

function isAbortLikeError(error) {
    return error && (error.name === 'AbortError' || error.message === 'stale graph request');
}

async function fetchJSON(url, signal) {
    const response = await fetch(url, {
        method: 'GET',
        headers: {
            Accept: 'application/json'
        },
        signal
    });

    if (!response.ok) {
        let reason = `HTTP ${response.status}`;
        try {
            const payload = await response.json();
            if (payload && payload.error) {
                reason = payload.error;
            }
        } catch (_parseError) {
            // ignore parse failure
        }
        throw new Error(reason);
    }

    return response.json();
}

function setLoadStatusVisible(visible) {
    const panel = document.getElementById('graph-load-status');
    if (!panel) {
        return;
    }
    panel.style.display = visible ? 'flex' : 'none';
}

function markLoadStep(step, status) {
    const elementId = GRAPH_STEP_IDS[step];
    if (!elementId) {
        return;
    }

    const el = document.getElementById(elementId);
    if (!el) {
        return;
    }

    el.classList.remove('is-active', 'is-done', 'is-error');

    if (status === 'active') {
        el.classList.add('is-active');
    } else if (status === 'done') {
        el.classList.add('is-done');
    } else if (status === 'error') {
        el.classList.add('is-error');
    }
}

function findActiveStep() {
    const stepNames = Object.keys(GRAPH_STEP_IDS);
    for (let i = 0; i < stepNames.length; i += 1) {
        const step = stepNames[i];
        const elementId = GRAPH_STEP_IDS[step];
        const el = document.getElementById(elementId);
        if (el && el.classList.contains('is-active')) {
            return step;
        }
    }
    return '';
}

function setLoadMeta(text) {
    const el = document.getElementById('graph-load-meta');
    if (!el) {
        return;
    }
    el.textContent = text || '';
}

function updateLoadCounters() {
    const root = GraphState.data;
    let queryCount = 0;
    let engineCount = 0;
    let resultCount = 0;

    if (root && Array.isArray(root.children)) {
        queryCount = root.children.length;

        root.children.forEach(queryNode => {
            if (!queryNode || !Array.isArray(queryNode.children)) {
                return;
            }

            engineCount += queryNode.children.length;
            queryNode.children.forEach(engineNode => {
                if (engineNode && Array.isArray(engineNode.children)) {
                    resultCount += engineNode.children.length;
                }
            });
        });
    }

    GraphState.load.counts.queries = queryCount;
    GraphState.load.counts.engines = engineCount;
    GraphState.load.counts.results = resultCount;
}

function buildLoadSummaryText() {
    return `Hazir: ${GraphState.load.counts.queries} sorgu, ${GraphState.load.counts.engines} motor, ${GraphState.load.counts.results} sonuc`;
}

function showEmptyState() {
    const container = document.getElementById('graph-container');

    document.querySelectorAll('#graph-container .graph-empty-state').forEach(node => node.remove());
    d3.select('#graph-container svg').remove();

    const emptyDiv = document.createElement('div');
    emptyDiv.className = 'graph-empty-state flex items-center justify-center h-full absolute inset-0 pointer-events-none';
    emptyDiv.innerHTML = `
        <div class="text-center text-gray-400 pointer-events-auto">
            <p class="text-6xl mb-4">📭</p>
            <p class="text-xl mb-2">Henüz sonuç bulunamadı</p>
            <p class="mb-4">Arama yaparak başlayın</p>
            <a href="/search" class="bg-blue-600 hover:bg-blue-700 text-white px-6 py-3 rounded-lg inline-block">
                🔍 Arama Yap
            </a>
        </div>
    `;
    container.appendChild(emptyDiv);
}

function handleGraphResize() {
    if (resizeDebounceHandle) {
        clearTimeout(resizeDebounceHandle);
    }

    resizeDebounceHandle = setTimeout(() => {
        GraphState.width = window.innerWidth;
        GraphState.height = window.innerHeight - 106;

        if (!GraphState.root || !GraphState.svg) {
            return;
        }

        renderLayout();
        setTimeout(fitToScreen, 120);
    }, 140);
}

window.addEventListener('resize', handleGraphResize);

function updateStats(data) {
    let queries = 0, engines = 0, results = 0, multi = 0;

    if (data.type === 'query') {
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
