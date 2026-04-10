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
    queries: []
};

const GRAPH_API_LIMITS = Object.freeze({
    overview: {
        maxQueries: 25,
        maxResultsPerEngine: 40
    },
    focused: {
        maxQueries: 1,
        maxResultsPerEngine: 180
    }
});

let resizeDebounceHandle = null;

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
    try {
        const url = buildGraphApiURL(queryParam);
        const response = await fetch(url);
        GraphState.data = await response.json();

        if (!GraphState.data || !GraphState.data.children || GraphState.data.children.length === 0) {
            showEmptyState();
            return;
        }

        let graphData = GraphState.data;
        if (queryParam && GraphState.data.children && GraphState.data.children.length === 1) {
            graphData = GraphState.data.children[0];
            graphData.type = 'query';
        } else if (queryParam && GraphState.data.children) {
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
        console.error('Failed to load graph data:', error);
        showEmptyState();
    }
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

function buildGraphApiURL(queryParam = '') {
    const params = new URLSearchParams();
    const profile = queryParam ? GRAPH_API_LIMITS.focused : GRAPH_API_LIMITS.overview;

    if (queryParam) {
        params.set('q', queryParam);
    }

    params.set('maxQueries', String(profile.maxQueries));
    params.set('maxResultsPerEngine', String(profile.maxResultsPerEngine));

    return '/api/graph?' + params.toString();
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
