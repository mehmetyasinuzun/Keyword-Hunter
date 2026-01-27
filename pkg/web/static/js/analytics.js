/**
 * ═══════════════════════════════════════════════════════════════════════
 * KeywordHunter - Analytics Page JavaScript
 * Modular CTI Analytics Dashboard Logic
 * ═══════════════════════════════════════════════════════════════════════
 */

// ═══════════════════════════════════════════════════════════════
// STATE MANAGEMENT
// ═══════════════════════════════════════════════════════════════

let analyticsData = null;
let queryList = [];
let selectedQueries = new Set();
let currentInterval = 'day';
let queryChartInterval = 'day';
let charts = {};

// Color Palettes
const COLORS = {
    sources: {
        'Ahmia': '#3b82f6',
        'Torch': '#f97316',
        'Haystack': '#10b981',
        'OnionLand': '#06b6d4',
        'DeepSearch': '#8b5cf6',
        'FindTor': '#ec4899',
        'Tor66': '#ef4444',
        'Excavator': '#84cc16',
        'Torgle': '#14b8a6',
        'Onionway': '#6366f1',
        'OSS': '#f59e0b',
        'Amnesia': '#a855f7',
        'Torland': '#22c55e'
    },
    queries: [
        '#63b3ed', '#805ad5', '#38b2ac', '#ed8936', '#48bb78',
        '#f56565', '#667eea', '#ed64a6', '#4fd1c5', '#fc8181',
        '#9f7aea', '#68d391', '#f6ad55', '#76e4f7', '#b794f4'
    ],
    criticality: {
        1: '#6b7280',
        2: '#3b82f6',
        3: '#f59e0b',
        4: '#f97316',
        5: '#ef4444'
    }
};

// ═══════════════════════════════════════════════════════════════
// INITIALIZATION
// ═══════════════════════════════════════════════════════════════

document.addEventListener('DOMContentLoaded', async () => {
    Chart.defaults.color = '#5a6a8a';
    Chart.defaults.borderColor = 'rgba(99, 179, 237, 0.08)';
    Chart.defaults.font.family = "'Plus Jakarta Sans', sans-serif";

    await loadQueries();
    await loadAnalyticsData();
});

// ═══════════════════════════════════════════════════════════════
// DATA FETCHING
// ═══════════════════════════════════════════════════════════════

async function loadQueries() {
    try {
        const response = await fetch('/api/queries');
        const data = await response.json();
        queryList = (data.queries || []).filter(q => q && q.query);
        renderFilterChips();
    } catch (error) {
        console.error('Failed to load queries:', error);
        queryList = [];
        renderFilterChips();
    }
}

async function loadAnalyticsData() {
    try {
        const hasSelection = selectedQueries.size > 0;
        const intervalToUse = hasSelection ? queryChartInterval : currentInterval;
        const queryParam = hasSelection
            ? '&query=' + encodeURIComponent([...selectedQueries][0])
            : '';

        const response = await fetch(`/api/analytics?interval=${intervalToUse}${queryParam}`);
        analyticsData = await response.json();

        // Null-safe defaults
        analyticsData.sources = analyticsData.sources || [];
        analyticsData.timeline = analyticsData.timeline || [];
        analyticsData.domains = analyticsData.domains || [];
        analyticsData.criticality = analyticsData.criticality || [];
        analyticsData.queries = analyticsData.queries || [];
        analyticsData.keywordStats = analyticsData.keywordStats || {};

        updateStats();
        renderAllCharts();
        renderDomains();
    } catch (error) {
        console.error('Failed to load analytics:', error);
    }
}

async function loadAnalyticsDataForQueryChart() {
    if (selectedQueries.size === 0) return;

    const selectedQuery = [...selectedQueries][0];

    try {
        const response = await fetch(`/api/analytics?interval=${queryChartInterval}&query=${encodeURIComponent(selectedQuery)}`);
        const data = await response.json();

        analyticsData.timeline = data.timeline || [];
        renderQueryBarChart();
    } catch (error) {
        console.error('Failed to load query chart data:', error);
    }
}

// ═══════════════════════════════════════════════════════════════
// FILTER MANAGEMENT
// ═══════════════════════════════════════════════════════════════

function renderFilterChips() {
    const container = document.getElementById('filter-chips');

    if (queryList.length === 0) {
        container.innerHTML = '<span class="filter-empty">Henüz sorgu verisi bulunamadı</span>';
        return;
    }

    container.innerHTML = queryList.map((q, index) => `
        <button 
            class="filter-chip ${selectedQueries.has(q.query) ? 'active' : ''}"
            onclick="toggleQuery('${encodeURIComponent(q.query)}')"
            data-query="${encodeURIComponent(q.query)}"
        >
            ${escapeHtml(q.query)}
            <span class="filter-chip-count">${q.count || 0}</span>
        </button>
    `).join('');
}

function toggleQuery(encodedQuery) {
    const query = decodeURIComponent(encodedQuery);
    if (selectedQueries.has(query)) {
        selectedQueries.delete(query);
    } else {
        selectedQueries.clear();
        selectedQueries.add(query);
    }
    renderFilterChips();
    loadAnalyticsData();
}

function clearFilters() {
    selectedQueries.clear();
    renderFilterChips();
    loadAnalyticsData();
}

function changeInterval(interval) {
    currentInterval = interval;

    document.querySelectorAll('.chart-control-btn').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.interval === interval);
    });

    const titles = { hour: 'Saatlik', day: 'Günlük', week: 'Haftalık' };
    document.getElementById('timeline-title').innerHTML = `<span></span> Zaman Tüneli (${titles[interval]})`;

    loadAnalyticsData();
}

function changeQueryChartInterval(interval) {
    queryChartInterval = interval;

    const controlsEl = document.getElementById('query-chart-controls');
    controlsEl.querySelectorAll('.chart-control-btn').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.interval === interval);
    });

    loadAnalyticsDataForQueryChart();
}

// ═══════════════════════════════════════════════════════════════
// STATS UPDATE
// ═══════════════════════════════════════════════════════════════

function updateStats() {
    const total = analyticsData.totalSites || 0;
    const freq = analyticsData.keywordStats?.totalHits || 0;
    const withHits = analyticsData.keywordStats?.withHits || 0;
    const rate = total > 0 ? Math.round((withHits / total) * 100) : 0;

    animateNumber('stat-total', total);
    animateNumber('stat-freq', freq);
    document.getElementById('stat-rate').textContent = `%${rate}`;
    animateNumber('stat-queries', queryList.length);
}

function animateNumber(elementId, target) {
    const el = document.getElementById(elementId);
    const duration = 600;
    const start = parseInt(el.textContent) || 0;
    const startTime = performance.now();

    function update(currentTime) {
        const elapsed = currentTime - startTime;
        const progress = Math.min(elapsed / duration, 1);
        const eased = 1 - Math.pow(1 - progress, 3);
        const current = Math.round(start + (target - start) * eased);
        el.textContent = current.toLocaleString('tr-TR');

        if (progress < 1) {
            requestAnimationFrame(update);
        }
    }

    requestAnimationFrame(update);
}

// ═══════════════════════════════════════════════════════════════
// CHART RENDERING - Main
// ═══════════════════════════════════════════════════════════════

function renderAllCharts() {
    renderQueryBarChart();
    renderTimelineChart();
    renderQueryPieChart();
    renderSourcePieChart();
    renderCriticalityChart();
}

function getChartContext(id) {
    if (charts[id]) charts[id].destroy();
    return document.getElementById(id).getContext('2d');
}

function renderEmptyChart(ctx, message) {
    const canvas = ctx.canvas;
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.fillStyle = '#5a6a8a';
    ctx.font = '14px "Plus Jakarta Sans", sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText(message, canvas.width / 2, canvas.height / 2);
}

// ═══════════════════════════════════════════════════════════════
// CHART RENDERING - Query Bar Chart (Dynamic)
// ═══════════════════════════════════════════════════════════════

function renderQueryBarChart() {
    const ctx = getChartContext('queryBarChart');
    const titleEl = document.getElementById('query-chart-title');
    const controlsEl = document.getElementById('query-chart-controls');

    const hasSelection = selectedQueries.size > 0;
    const selectedQuery = hasSelection ? [...selectedQueries][0] : null;

    if (hasSelection) {
        // DUAL METRIC COMBO CHART MODE
        const intervalLabels = { hour: 'Saatlik', day: 'Günlük', week: 'Haftalık' };
        titleEl.innerHTML = `<span></span> "${escapeHtml(selectedQuery)}" - Süreç Analizi (${intervalLabels[queryChartInterval] || 'Günlük'})`;
        controlsEl.style.display = 'flex';

        controlsEl.querySelectorAll('.chart-control-btn').forEach(btn => {
            btn.classList.toggle('active', btn.dataset.interval === queryChartInterval);
        });

        const timeline = analyticsData.timeline || [];

        if (timeline.length === 0) {
            renderEmptyChart(ctx, 'Bu sorgu için zaman verisi bulunamadı');
            return;
        }

        const dates = [...new Set(timeline.map(t => t.date))].sort();
        const periodicCounts = dates.map(date => {
            return timeline
                .filter(t => t.date === date)
                .reduce((sum, t) => sum + (t.count || 0), 0);
        });

        let runningTotal = 0;
        const cumulativeTotals = periodicCounts.map(count => {
            runningTotal += count;
            return runningTotal;
        });

        if (dates.length === 1) {
            dates.unshift('Başlangıç');
            periodicCounts.unshift(0);
            cumulativeTotals.unshift(0);
        }

        const barGradient = ctx.createLinearGradient(0, 0, 0, 400);
        barGradient.addColorStop(0, 'rgba(139, 92, 246, 0.9)');
        barGradient.addColorStop(1, 'rgba(139, 92, 246, 0.3)');

        const lineGradient = ctx.createLinearGradient(0, 0, 0, 400);
        lineGradient.addColorStop(0, 'rgba(6, 182, 212, 0.4)');
        lineGradient.addColorStop(0.5, 'rgba(6, 182, 212, 0.15)');
        lineGradient.addColorStop(1, 'rgba(6, 182, 212, 0.02)');

        charts.queryBarChart = new Chart(ctx, {
            type: 'bar',
            data: {
                labels: dates,
                datasets: [
                    {
                        type: 'line',
                        label: 'Kümülatif Toplam',
                        data: cumulativeTotals,
                        borderColor: '#06b6d4',
                        backgroundColor: lineGradient,
                        fill: true,
                        tension: 0.4,
                        borderWidth: 3,
                        pointRadius: 6,
                        pointBackgroundColor: '#06b6d4',
                        pointBorderColor: '#0d0f16',
                        pointBorderWidth: 3,
                        pointHoverRadius: 10,
                        pointHoverBackgroundColor: '#22d3ee',
                        yAxisID: 'y1',
                        order: 0
                    },
                    {
                        type: 'bar',
                        label: 'Dönemsel Yeni Sonuç',
                        data: periodicCounts,
                        backgroundColor: barGradient,
                        borderColor: 'rgba(139, 92, 246, 1)',
                        borderWidth: 1,
                        borderRadius: 6,
                        borderSkipped: false,
                        barPercentage: 0.7,
                        categoryPercentage: 0.8,
                        yAxisID: 'y',
                        order: 1
                    }
                ]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                interaction: { mode: 'index', intersect: false },
                plugins: {
                    legend: {
                        position: 'top',
                        align: 'end',
                        labels: {
                            boxWidth: 14,
                            usePointStyle: true,
                            padding: 20,
                            color: '#8b9dc3',
                            font: { size: 11, weight: '600' }
                        }
                    },
                    tooltip: {
                        backgroundColor: 'rgba(13, 15, 22, 0.97)',
                        titleColor: '#e8edf5',
                        titleFont: { size: 13, weight: '700' },
                        bodyColor: '#8b9dc3',
                        borderColor: 'rgba(6, 182, 212, 0.4)',
                        borderWidth: 1,
                        padding: 16,
                        cornerRadius: 12,
                        displayColors: true,
                        callbacks: {
                            title: (items) => ` ${items[0].label}`,
                            label: (context) => {
                                const value = context.parsed.y;
                                if (context.dataset.label === 'Kümülatif Toplam') {
                                    return `  Toplam Birikim: ${value.toLocaleString('tr-TR')}`;
                                }
                                return `  Bu Dönem: +${value.toLocaleString('tr-TR')}`;
                            },
                            afterBody: (items) => {
                                const idx = items[0].dataIndex;
                                const periodic = periodicCounts[idx];
                                const cumulative = cumulativeTotals[idx];
                                const percent = cumulative > 0 ? Math.round((periodic / cumulative) * 100) : 0;
                                return ['', ` Bu dönem toplam birikimin %${percent}'i`];
                            }
                        }
                    }
                },
                scales: {
                    x: {
                        grid: { display: false },
                        ticks: { color: '#5a6a8a', maxRotation: 45 }
                    },
                    y: {
                        type: 'linear',
                        position: 'left',
                        beginAtZero: true,
                        title: { display: true, text: 'Dönemsel Sayı', color: '#8b5cf6', font: { size: 11, weight: '600' } },
                        grid: { color: 'rgba(139, 92, 246, 0.08)' },
                        ticks: { color: '#8b5cf6' }
                    },
                    y1: {
                        type: 'linear',
                        position: 'right',
                        beginAtZero: true,
                        title: { display: true, text: 'Kümülatif Toplam', color: '#06b6d4', font: { size: 11, weight: '600' } },
                        grid: { drawOnChartArea: false },
                        ticks: { color: '#06b6d4' }
                    }
                },
                animation: { duration: 800, easing: 'easeOutQuart' }
            }
        });

    } else {
        // BAR CHART MODE - Show general query distribution
        titleEl.innerHTML = '<span></span> Sorgu Bazlı Sonuç Dağılımı';
        controlsEl.style.display = 'none';

        const data = queryList.slice(0, 12);

        if (data.length === 0) {
            renderEmptyChart(ctx, 'Sorgu verisi bulunamadı');
            return;
        }

        charts.queryBarChart = new Chart(ctx, {
            type: 'bar',
            data: {
                labels: data.map(q => truncateText(q.query, 15)),
                datasets: [{
                    label: 'Sonuç Sayısı',
                    data: data.map(q => q.count),
                    backgroundColor: data.map((_, i) => COLORS.queries[i % COLORS.queries.length]),
                    borderRadius: 8,
                    borderSkipped: false,
                    barThickness: 45,
                    maxBarThickness: 60
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                plugins: {
                    legend: { display: false },
                    tooltip: {
                        backgroundColor: 'rgba(13, 15, 22, 0.95)',
                        titleColor: '#e8edf5',
                        bodyColor: '#8b9dc3',
                        borderColor: 'rgba(99, 179, 237, 0.2)',
                        borderWidth: 1,
                        padding: 12,
                        cornerRadius: 8,
                        callbacks: {
                            title: (items) => data[items[0].dataIndex].query
                        }
                    }
                },
                scales: {
                    x: {
                        grid: { display: false },
                        ticks: { maxRotation: 45, minRotation: 0, color: '#5a6a8a' }
                    },
                    y: {
                        beginAtZero: true,
                        grid: { color: 'rgba(99, 179, 237, 0.06)' },
                        ticks: { color: '#5a6a8a' }
                    }
                }
            }
        });
    }
}

// ═══════════════════════════════════════════════════════════════
// CHART RENDERING - Timeline Chart
// ═══════════════════════════════════════════════════════════════

function renderTimelineChart() {
    const ctx = getChartContext('timelineChart');
    const timeline = analyticsData.timeline || [];

    if (timeline.length === 0) {
        renderEmptyChart(ctx, 'Zaman serisi verisi bulunamadı');
        return;
    }

    const sources = [...new Set(timeline.map(t => t.source))];
    const dates = [...new Set(timeline.map(t => t.date))].sort();

    const datasets = sources.map((source, i) => {
        const color = COLORS.sources[source] || COLORS.queries[i % COLORS.queries.length];
        return {
            label: source,
            data: dates.map(date => {
                const entry = timeline.find(t => t.date === date && t.source === source);
                return entry ? entry.count : 0;
            }),
            borderColor: color,
            backgroundColor: color + '20',
            fill: false,
            tension: 0.35,
            borderWidth: 2,
            pointRadius: 4,
            pointBackgroundColor: color,
            pointBorderColor: '#0d0f16',
            pointBorderWidth: 2,
            pointHoverRadius: 7
        };
    });

    charts.timelineChart = new Chart(ctx, {
        type: 'line',
        data: { labels: dates, datasets },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            interaction: { mode: 'index', intersect: false },
            plugins: {
                legend: {
                    position: 'top',
                    labels: {
                        boxWidth: 12,
                        usePointStyle: true,
                        padding: 20
                    }
                },
                tooltip: {
                    backgroundColor: 'rgba(13, 15, 22, 0.95)',
                    titleColor: '#e8edf5',
                    bodyColor: '#8b9dc3',
                    borderColor: 'rgba(99, 179, 237, 0.2)',
                    borderWidth: 1,
                    padding: 12,
                    cornerRadius: 8
                }
            },
            scales: {
                x: { grid: { display: false } },
                y: {
                    beginAtZero: true,
                    grid: { color: 'rgba(99, 179, 237, 0.06)' }
                }
            }
        }
    });
}

// ═══════════════════════════════════════════════════════════════
// CHART RENDERING - Pie Charts
// ═══════════════════════════════════════════════════════════════

function renderQueryPieChart() {
    const ctx = getChartContext('queryPieChart');
    const data = queryList.slice(0, 8);

    if (data.length === 0) {
        renderEmptyChart(ctx, 'Veri yok');
        return;
    }

    charts.queryPieChart = new Chart(ctx, {
        type: 'doughnut',
        data: {
            labels: data.map(q => truncateText(q.query, 12)),
            datasets: [{
                data: data.map(q => q.count),
                backgroundColor: data.map((_, i) => COLORS.queries[i]),
                borderWidth: 0,
                hoverOffset: 8
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            cutout: '60%',
            plugins: {
                legend: {
                    position: 'right',
                    labels: { boxWidth: 10, padding: 8, font: { size: 10 } }
                }
            }
        }
    });
}

function renderSourcePieChart() {
    const ctx = getChartContext('sourcePieChart');
    const sources = analyticsData.sources || [];

    if (sources.length === 0) {
        renderEmptyChart(ctx, 'Kaynak verisi yok');
        return;
    }

    charts.sourcePieChart = new Chart(ctx, {
        type: 'doughnut',
        data: {
            labels: sources.map(s => s.source),
            datasets: [{
                data: sources.map(s => s.count),
                backgroundColor: sources.map(s => COLORS.sources[s.source] || '#6b7280'),
                borderWidth: 0,
                hoverOffset: 8
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            cutout: '60%',
            plugins: {
                legend: {
                    position: 'right',
                    labels: { boxWidth: 10, padding: 8, font: { size: 10 } }
                }
            }
        }
    });
}

function renderCriticalityChart() {
    const ctx = getChartContext('criticalityChart');
    const crit = analyticsData.criticality || [];

    if (crit.length === 0) {
        renderEmptyChart(ctx, 'Kritiklik verisi yok');
        return;
    }

    charts.criticalityChart = new Chart(ctx, {
        type: 'doughnut',
        data: {
            labels: crit.map(c => `Seviye ${c.level}`),
            datasets: [{
                data: crit.map(c => c.count),
                backgroundColor: crit.map(c => COLORS.criticality[c.level] || '#6b7280'),
                borderWidth: 0,
                hoverOffset: 8
            }]
        },
        options: {
            responsive: true,
            maintainAspectRatio: false,
            cutout: '60%',
            plugins: {
                legend: {
                    position: 'right',
                    labels: { boxWidth: 10, padding: 8, font: { size: 10 } }
                }
            }
        }
    });
}

// ═══════════════════════════════════════════════════════════════
// DOMAIN LIST RENDERING
// ═══════════════════════════════════════════════════════════════

function renderDomains() {
    const container = document.getElementById('domain-list');
    const countEl = document.getElementById('domain-count');
    const domains = analyticsData.domains || [];

    if (domains.length === 0) {
        container.innerHTML = `
            <div class="empty-state">
                <div class="empty-icon">🌐</div>
                <p>Henüz domain verisi bulunamadı</p>
            </div>
        `;
        countEl.textContent = '0 domain';
        return;
    }

    const sorted = [...domains].sort((a, b) => b.count - a.count);
    const maxCount = sorted[0].count;

    countEl.textContent = `${sorted.length} domain`;

    container.innerHTML = sorted.slice(0, 50).map((d, i) => {
        const barWidth = Math.max(5, (d.count / maxCount) * 100);
        return `
            <div class="domain-item">
                <span class="domain-rank">${i + 1}</span>
                <div class="domain-info">
                    <div class="domain-name" title="${escapeHtml(d.domain)}">${escapeHtml(d.domain)}</div>
                    <div class="domain-bar-container">
                        <div class="domain-bar" style="width: ${barWidth}%"></div>
                    </div>
                </div>
                <span class="domain-value">${d.count}</span>
            </div>
        `;
    }).join('');
}

// ═══════════════════════════════════════════════════════════════
// UTILITIES
// ═══════════════════════════════════════════════════════════════

function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

function truncateText(text, maxLength) {
    if (!text) return '';
    return text.length > maxLength ? text.substring(0, maxLength) + '...' : text;
}
