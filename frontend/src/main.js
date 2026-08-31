import './style.css';
import './fixes.css';
import './immersive.css';
import uPlot from 'uplot';
import 'uplot/dist/uPlot.min.css';

if ('serviceWorker' in navigator) {
    window.addEventListener('load', () => navigator.serviceWorker.register('/service-worker.js').catch((error) => console.warn('service worker registration failed', error)));
}

const $ = (id) => document.getElementById(id);
const HISTORY_SECONDS = 5 * 60;
const api = async (path, method = 'GET', body = null) => {
    const opts = { method, headers: {} };
    if (body !== null) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
    const response = await fetch(path, opts);
    if (!response.ok) throw new Error((await response.text()) || response.statusText);
    return response.json();
};
const fmtBps = (value) => {
    const n = Math.abs(Number(value) || 0);
    if (n >= 1e9) return `${(n / 1e9).toFixed(2)} Gbps`;
    if (n >= 1e6) return `${(n / 1e6).toFixed(2)} Mbps`;
    if (n >= 1e3) return `${(n / 1e3).toFixed(1)} Kbps`;
    return `${n.toFixed(0)} bps`;
};
const ifaceKey = (deviceID, index) => `${deviceID}:${index}`;
const cardID = (type, key) => `${type}:${key}`;

let config = { devices: [], groups: [], dashboardCards: [], dashboardColumns: 1, groupMemberTraces: true, labels: {}, interval: 3, selected: [] };
let samples = [];
let charts = new Map();
let monitoring = false;
let ws;
let dragID = null;
let resizeFrame = 0;

function normalizeClientConfig() {
    config.devices ||= []; config.groups ||= []; config.selected ||= []; config.dashboardCards ||= []; config.labels ||= {};
    config.dashboardColumns = Math.min(4, Math.max(1, Number(config.dashboardColumns) || 1));
    if (typeof config.groupMemberTraces !== 'boolean') config.groupMemberTraces = true;
    // A visible interface card is an explicit request to monitor that source.
    // This repairs configs created by older versions that could retain a card
    // after dropping its matching selection.
    config.dashboardCards.forEach((card) => {
        if (card.sourceType === 'interface' && card.visible !== false && !config.selected.includes(card.sourceKey)) config.selected.push(card.sourceKey);
        if (!card.height || card.height < 140) card.height = 205;
    });
    const have = new Set(config.dashboardCards.map((c) => c.id));
    config.selected.forEach((key) => {
        const id = cardID('interface', key);
        if (!have.has(id)) { config.dashboardCards.push({ id, sourceType: 'interface', sourceKey: key, visible: true, scaleMode: 'auto', height: 205 }); have.add(id); }
    });
    config.groups.forEach((group) => {
        const id = cardID('group', group.name);
        if (!have.has(id)) { config.dashboardCards.push({ id, sourceType: 'group', sourceKey: group.name, visible: true, scaleMode: 'auto', height: 205 }); have.add(id); }
    });
}

async function saveConfig() {
    config = await api('/api/config', 'POST', config);
    normalizeClientConfig();
    updateControls(); renderSettings(); renderDashboard();
}

async function init() {
    try {
        [config, { running: monitoring }] = await Promise.all([api('/api/config'), api('/api/status')]);
    } catch (error) { setStatus('error', 'Configuration unavailable'); console.error(error); }
    normalizeClientConfig();
    $('interval').value = config.interval || 3;
    bindEvents(); updateControls(); renderSettings(); renderDashboard(); connectWS();
}

function bindEvents() {
    $('btn-settings').addEventListener('click', () => setDrawer(true));
    $('btn-quick-settings').addEventListener('click', () => setDrawer(true));
    $('btn-close-settings').addEventListener('click', () => setDrawer(false));
    $('drawer-scrim').addEventListener('click', () => setDrawer(false));
    $('btn-scan').addEventListener('click', scanDevice);
    $('btn-add-group').addEventListener('click', addGroup);
    $('grid-columns').addEventListener('change', async () => { config.dashboardColumns = Number($('grid-columns').value); await saveConfig(); });
    $('group-member-traces').addEventListener('change', async () => { config.groupMemberTraces = $('group-member-traces').checked; await saveConfig(); });
    $('btn-start').addEventListener('click', start);
    $('btn-stop').addEventListener('click', stop);
    window.addEventListener('keydown', (event) => {
        if (event.target.matches('input, select, textarea')) return;
        if (event.key === '?') { event.preventDefault(); setDrawer(true); }
        if (event.key.toLowerCase() === 'm') { event.preventDefault(); monitoring ? stop() : start(); }
    });
    const resizeCharts = () => {
        cancelAnimationFrame(resizeFrame);
        resizeFrame = requestAnimationFrame(() => charts.forEach((chart) => chart.setSize(chartSize(chart.root))));
    };
    window.addEventListener('resize', resizeCharts);
    new ResizeObserver(resizeCharts).observe($('dashboard'));
    window.setInterval(updateSampleAge, 1000);
}

function setDrawer(open) {
    $('settings-drawer').classList.toggle('open', open);
    $('drawer-scrim').classList.toggle('open', open);
    $('settings-drawer').setAttribute('aria-hidden', String(!open));
}

function setStatus(kind, text) {
    const el = $('status'); el.className = `connection ${kind}`; el.innerHTML = '<i></i>'; el.append(` ${text}`);
}

function updateControls() {
    const ready = config.selected.length > 0;
    $('btn-start').disabled = monitoring || !ready;
    $('btn-stop').disabled = !monitoring;
    $('btn-start').textContent = monitoring ? 'Monitoring active' : ready ? 'Start monitoring' : 'Select an interface';
    $('btn-stop').textContent = monitoring ? 'Stop monitoring' : 'Stopped';
}

function connectWS() {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(`${proto}//${location.host}/ws`);
    ws.onopen = () => setStatus(monitoring ? 'live' : '', monitoring ? 'Monitoring live' : 'Monitoring stopped');
    ws.onmessage = (event) => {
        const message = JSON.parse(event.data);
        if (message.type === 'config') { config = message.data; normalizeClientConfig(); $('interval').value = config.interval || 3; updateControls(); renderSettings(); renderDashboard(); }
        if (message.type === 'monitoring') { monitoring = message.running; updateControls(); setStatus(monitoring ? 'live' : '', monitoring ? 'Monitoring live' : 'Monitoring stopped'); }
        if (message.type === 'sample') {
            samples.push(message.data);
            const cutoff = Date.now() - HISTORY_SECONDS * 1000;
            samples = samples.filter((sample) => sample.ts >= cutoff);
            if (monitoring) setStatus('live', 'Monitoring live');
            updateDashboard();
        }
    };
    ws.onclose = () => { setStatus('error', 'Reconnecting…'); setTimeout(connectWS, 2000); };
    ws.onerror = () => setStatus('error', 'Connection error');
}

async function scanDevice() {
    const ip = $('dev-ip').value.trim(); if (!ip) return;
    $('scan-msg').textContent = `Scanning ${ip}…`;
    try {
        const result = await api('/api/scan', 'POST', { ip, community: $('dev-community').value.trim() || 'public', version: $('dev-version').value, port: Number($('dev-port').value) || 161 });
        const index = config.devices.findIndex((device) => device.id === result.device.id);
        if (index >= 0) config.devices[index] = result.device; else config.devices.push(result.device);
        await saveConfig(); $('scan-msg').textContent = `Found ${result.interfaces.length} interfaces.`;
    } catch (error) { $('scan-msg').textContent = `Scan failed: ${error.message}`; }
}

function allInterfaces() { return config.devices.flatMap((device) => device.interfaces || []); }
function findInterface(key) { return allInterfaces().find((item) => ifaceKey(item.deviceID, item.index) === key); }
function findDevice(id) { return config.devices.find((device) => device.id === id); }
function findGroup(name) { return config.groups.find((group) => group.name === name); }
function interfaceLabel(key) {
    const iface = findInterface(key);
    return config.labels[key] || iface?.alias || iface?.name || key;
}
function labelFor(card) {
    if (card.sourceType === 'group') return card.sourceKey;
    return interfaceLabel(card.sourceKey);
}
function sublabelFor(card) {
    if (card.sourceType === 'group') { const group = findGroup(card.sourceKey); return `${group?.members.length || 0} interfaces combined`; }
    const iface = findInterface(card.sourceKey); const device = iface && findDevice(iface.deviceID); return iface ? `${device?.ip || iface.deviceID} · ${iface.name}` : 'Saved interface';
}
function capacityFor(card) {
    if (card.sourceType === 'interface') return findInterface(card.sourceKey)?.speed || 0;
    return (findGroup(card.sourceKey)?.members || []).reduce((total, member) => total + (findInterface(ifaceKey(member.deviceID, member.index))?.speed || 0), 0);
}
function activeCards() {
    return config.dashboardCards.filter((card) => card.visible !== false && (card.sourceType === 'group' ? !!findGroup(card.sourceKey) : config.selected.includes(card.sourceKey)));
}

function renderSettings() {
    const list = $('iface-list'); list.innerHTML = '';
    config.devices.forEach((device) => {
        const label = document.createElement('div'); label.className = 'device-label'; label.textContent = `${device.ip} · ${device.useHC ? '64-bit counters' : '32-bit counters'}`; list.append(label);
        (device.interfaces || []).sort((a, b) => a.index - b.index).forEach((iface) => {
            const key = ifaceKey(iface.deviceID, iface.index); const row = document.createElement('label'); row.className = 'interface-row';
            const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.checked = config.selected.includes(key); checkbox.addEventListener('change', async () => {
                if (checkbox.checked) config.selected.push(key);
                else {
                    config.selected = config.selected.filter((item) => item !== key);
                    config.dashboardCards = config.dashboardCards.filter((card) => card.sourceType !== 'interface' || card.sourceKey !== key);
                }
                await saveConfig();
            });
            const name = document.createElement('span'); name.className = 'interface-name'; name.innerHTML = `<strong>${escapeHTML(interfaceLabel(key))}</strong><span>${escapeHTML(iface.name)} · ${fmtBps(iface.speed)} link</span>`;
            const state = document.createElement('span'); state.className = `interface-state ${iface.status === 1 ? 'up' : ''}`; state.textContent = iface.status === 1 ? 'UP' : 'DOWN';
            const rename = document.createElement('button'); rename.type = 'button'; rename.className = 'small-button'; rename.textContent = 'Rename'; rename.addEventListener('click', async (event) => { event.preventDefault(); await renameInterface(key); });
            row.append(checkbox, name, state, rename); list.append(row);
        });
    });
    if (!config.devices.length) list.innerHTML = '<p class="helper">Scan a device to choose interfaces.</p>';
    const groups = $('group-list'); groups.innerHTML = '';
    config.groups.forEach((group, index) => {
        const el = document.createElement('div'); el.className = 'group-item';
        const head = document.createElement('div'); head.className = 'group-item-head'; head.innerHTML = `<strong>${escapeHTML(group.name)}</strong>`;
        const remove = document.createElement('button'); remove.className = 'small-button'; remove.textContent = 'Delete'; remove.addEventListener('click', async () => { config.groups.splice(index, 1); config.dashboardCards = config.dashboardCards.filter((card) => card.sourceKey !== group.name || card.sourceType !== 'group'); await saveConfig(); });
        const members = document.createElement('div'); members.className = 'group-members'; members.textContent = group.members.map((member) => interfaceLabel(ifaceKey(member.deviceID, member.index))).join(', ') || 'No interfaces';
        head.append(remove); el.append(head, members); groups.append(el);
    });
    $('grid-columns').value = String(config.dashboardColumns);
    $('group-member-traces').checked = config.groupMemberTraces;
    const cards = $('card-list'); cards.innerHTML = '';
    config.dashboardCards.forEach((card) => {
        const row = document.createElement('label'); row.className = 'interface-row';
        const checkbox = document.createElement('input'); checkbox.type = 'checkbox'; checkbox.checked = card.visible !== false;
        checkbox.addEventListener('change', async () => { card.visible = checkbox.checked; await saveConfig(); });
        const label = document.createElement('span'); label.className = 'interface-name'; label.innerHTML = `<strong>${escapeHTML(labelFor(card))}</strong><span>${card.sourceType === 'group' ? 'Group' : 'Interface'} graph</span>`;
        row.append(checkbox, label); cards.append(row);
    });
}

async function renameInterface(key) {
    const current = config.labels[key] || '';
    const next = window.prompt('Name this port (leave blank to use the SNMP name):', current);
    if (next === null) return;
    const label = next.trim();
    if (label) config.labels[key] = label;
    else delete config.labels[key];
    await saveConfig();
}

async function addGroup() {
    const name = $('group-name').value.trim(); if (!name || findGroup(name)) return;
    const members = config.selected.map((key) => { const [deviceID, index] = key.split(':'); return { deviceID, index: Number(index) }; });
    if (!members.length) { $('scan-msg').textContent = 'Select one or more interfaces first.'; return; }
    config.groups.push({ name, members }); $('group-name').value = ''; await saveConfig();
}

const GROUP_COLORS = ['#50b9ff', '#b38cff', '#5ed39b', '#ffb15e', '#f47cac', '#59d7c7', '#e9d66b', '#ff806f'];

function groupMembers(card) {
    return (findGroup(card.sourceKey)?.members || []).map((member) => ifaceKey(member.deviceID, member.index));
}

function cardData(card) {
    const source = (sample) => card.sourceType === 'interface' ? sample.interfaces[card.sourceKey] : sample.groups[card.sourceKey];
    const series = samples.map((sample) => source(sample));
    const totals = series.map((item) => Number(item?.total) || 0);
    const recorded = totals.filter((_, index) => Boolean(series[index]));
    const members = card.sourceType === 'group' && config.groupMemberTraces ? groupMembers(card).map((key, index) => ({
        key, label: interfaceLabel(key), color: GROUP_COLORS[index % GROUP_COLORS.length],
        inbound: samples.map((sample) => Number(sample.interfaces?.[key]?.in) || 0),
        outbound: samples.map((sample) => -(Number(sample.interfaces?.[key]?.out) || 0)),
    })) : [];
    return { xs: samples.map((sample) => sample.ts), inbound: series.map((item) => Number(item?.in) || 0), outbound: series.map((item) => -(Number(item?.out) || 0)), members, latest: series.at(-1) || {}, present: series.some(Boolean), peak: Math.max(0, ...recorded), average: recorded.length ? recorded.reduce((sum, value) => sum + value, 0) / recorded.length : 0, error: samples.at(-1)?.errors?.[card.sourceType === 'interface' ? card.sourceKey.split(':')[0] : findGroup(card.sourceKey)?.members?.[0]?.deviceID] || '' };
}

function chartSize(root) { return { width: Math.max(100, root.clientWidth), height: Math.max(100, root.clientHeight) }; }
function chartValues(data) {
    return data.members.length
        ? [data.xs, ...data.members.flatMap((member) => [member.inbound, member.outbound]), data.inbound, data.outbound]
        : [data.xs, data.inbound, data.outbound];
}
function makeChart(card, root) {
    const data = cardData(card); const capacity = capacityFor(card);
    const range = (_, min, max) => {
        const magnitude = card.scaleMode === 'capacity' && capacity > 0 ? capacity : Math.max(Math.abs(min || 0), Math.abs(max || 0), 1);
        return [-magnitude * 1.08, magnitude * 1.08];
    };
    const isBreakdown = data.members.length > 0;
    const series = [{ label: 'Time' }];
    const values = [data.xs];
    if (isBreakdown) {
        data.members.forEach((member) => {
            series.push({ label: `${member.label} in`, scale: 'bps', stroke: member.color, width: 1.3, points: { show: false } }); values.push(member.inbound);
            series.push({ label: `${member.label} out`, scale: 'bps', stroke: member.color, width: 1.3, dash: [5, 4], points: { show: false } }); values.push(member.outbound);
        });
        series.push({ label: 'Combined in', scale: 'bps', stroke: '#f1f6fb', width: 2.2, points: { show: false } }); values.push(data.inbound);
        series.push({ label: 'Combined out', scale: 'bps', stroke: '#f1f6fb', width: 2.2, dash: [7, 4], points: { show: false } }); values.push(data.outbound);
    } else {
        series.push({ label: 'Inbound', scale: 'bps', stroke: '#36a8f5', fill: 'rgba(54,168,245,.38)', width: 1.5, points: { show: false } }); values.push(data.inbound);
        series.push({ label: 'Outbound', scale: 'bps', stroke: '#ff8a3d', fill: 'rgba(255,138,61,.34)', width: 1.5, points: { show: false } }); values.push(data.outbound);
    }
    const chart = new uPlot({ ...chartSize(root), ms: 1, series, scales: { x: { time: true, ms: 1 }, bps: { range } }, axes: [{ values: (_, values) => values.map((value) => new Date(value).toLocaleTimeString([], { minute: '2-digit', second: '2-digit' })), grid: { stroke: '#263445' }, ticks: { stroke: '#263445' }, stroke: '#758397' }, { side: 1, scale: 'bps', size: 86, values: (_, values) => values.map((value) => value === 0 ? '0' : fmtBps(value)), grid: { stroke: '#263445' }, ticks: { stroke: '#263445' }, stroke: '#92a2b5' }], cursor: { drag: { x: true, y: false }, points: { show: true, size: 6 } }, legend: { show: false } }, chartValues(data), root);
    chart.root = root; return chart;
}

function metric(label, value, kind = '') { return `<div class="metric ${kind}"><span class="metric-label">${label}</span><span class="metric-value">${fmtBps(value)}</span></div>`; }
function renderDashboard() {
    charts.forEach((chart) => chart.destroy()); charts.clear();
    const dashboard = $('dashboard'); dashboard.style.setProperty('--dashboard-columns', config.dashboardColumns); dashboard.innerHTML = ''; const cards = activeCards();
    if (!cards.length) { dashboard.innerHTML = '<div class="empty-dashboard"><h3>No traffic cards yet</h3><p>Open Settings, scan a device, and select the interfaces or groups you want to monitor.</p></div>'; return; }
    cards.forEach((card) => {
        const data = cardData(card); const article = document.createElement('article'); article.className = 'graph-card'; article.dataset.cardId = card.id;
        const capacity = capacityFor(card); const utilization = capacity ? (data.latest.total || 0) / capacity : 0;
        if (utilization >= .9) article.classList.add('critical'); else if (utilization >= .7) article.classList.add('warning');
        article.innerHTML = `<header class="card-header"><span class="drag-handle" draggable="true" title="Drag to reorder">⠿</span><div class="card-title"><h3>${escapeHTML(labelFor(card))}</h3><p>${escapeHTML(sublabelFor(card))}</p></div><div class="card-metrics">${metric('In', data.latest.in, 'in')}${metric('Out', data.latest.out, 'out')}${metric('Total', data.latest.total)}</div><div class="card-actions"><select class="scale-select" aria-label="Graph scale"><option value="auto">Auto scale</option><option value="capacity">Link capacity</option></select><button class="small-button card-up" title="Move graph up">↑</button><button class="small-button card-down" title="Move graph down">↓</button><button class="small-button card-hide" title="Hide card">◉</button></div></header><div class="chart-wrap" style="height:${card.height || 205}px"><div class="chart"></div><div class="chart-empty"><strong>Waiting for this interface</strong><span>The graph will appear after its first poll</span></div><button class="resize-handle" title="Drag to resize graph" aria-label="Resize graph">↘</button></div><footer class="card-footer"><span><i class="legend-dot in"></i>INBOUND</span><span><i class="legend-dot out"></i>OUTBOUND</span><span class="metric-average">AVG ${fmtBps(data.average)}</span><span class="metric-peak">PEAK ${fmtBps(data.peak)}</span><span class="card-error"></span></footer>`;
        if (data.members.length) {
            const footer = article.querySelector('.card-footer');
            footer.innerHTML = `<span class="group-key"><i class="legend-dot" style="background:#f1f6fb"></i>COMBINED</span>${data.members.map((member) => `<span class="group-key" title="${escapeHTML(member.label)}"><i class="legend-dot" style="background:${member.color}"></i>${escapeHTML(member.label)}</span>`).join('')}<span class="group-direction">solid IN · dashed OUT</span><span class="card-error"></span>`;
        }
        const select = article.querySelector('select'); select.value = card.scaleMode || 'auto'; select.addEventListener('change', async () => { card.scaleMode = select.value; await saveConfig(); });
        article.querySelector('.card-hide').addEventListener('click', async () => { card.visible = false; await saveConfig(); });
        article.querySelector('.card-up').addEventListener('click', () => moveCard(card.id, -1));
        article.querySelector('.card-down').addEventListener('click', () => moveCard(card.id, 1));
        attachDrag(article, card); dashboard.append(article);
        attachResize(article, card);
        article.querySelector('.chart-empty').hidden = data.present;
        article.querySelector('.card-error').textContent = data.error ? `Poll error: ${data.error}` : '';
        charts.set(card.id, makeChart(card, article.querySelector('.chart')));
    });
    updateDashboardTotal();
}

function updateCard(card) {
    const article = document.querySelector(`.graph-card[data-card-id="${CSS.escape(card.id)}"]`);
    const chart = charts.get(card.id);
    if (!article || !chart) return false;
    const data = cardData(card);
    chart.setData(chartValues(data));
    const capacity = capacityFor(card); const utilization = capacity ? (data.latest.total || 0) / capacity : 0;
    article.classList.remove('warning', 'critical');
    if (utilization >= .9) article.classList.add('critical'); else if (utilization >= .7) article.classList.add('warning');
    article.querySelector('.metric.in .metric-value').textContent = fmtBps(data.latest.in);
    article.querySelector('.metric.out .metric-value').textContent = fmtBps(data.latest.out);
    article.querySelector('.metric:not(.in):not(.out) .metric-value').textContent = fmtBps(data.latest.total);
    article.querySelector('.chart-empty').hidden = data.present;
    article.querySelector('.metric-average')?.replaceChildren(`AVG ${fmtBps(data.average)}`);
    article.querySelector('.metric-peak')?.replaceChildren(`PEAK ${fmtBps(data.peak)}`);
    article.querySelector('.card-error').textContent = data.error ? `Poll error: ${data.error}` : '';
    return true;
}

function updateDashboard() {
    const cards = activeCards();
    if (cards.length !== charts.size || cards.some((card) => !updateCard(card))) renderDashboard();
    updateDashboardTotal();
}
function updateDashboardTotal() {
    const latest = samples.at(-1); if (!latest) return;
    let total = 0; Object.values(latest.interfaces || {}).forEach((sample) => { total += Number(sample.total) || 0; });
    $('dashboard-total').textContent = `Live total ${fmtBps(total)} · poll ${latest.pollMS || 0}ms`;
    updateSampleAge();
}

function updateSampleAge() {
    const latest = samples.at(-1);
    $('sample-age').textContent = latest ? `Updated ${Math.max(0, (Date.now() - latest.ts) / 1000).toFixed(1)}s ago` : 'No sample yet';
}
function attachDrag(article, card) {
    const handle = article.querySelector('.drag-handle');
    handle.addEventListener('dragstart', (event) => { dragID = card.id; article.classList.add('dragging'); event.dataTransfer.effectAllowed = 'move'; });
    handle.addEventListener('dragend', () => { article.classList.remove('dragging'); dragID = null; });
    article.addEventListener('dragover', (event) => { event.preventDefault(); event.dataTransfer.dropEffect = 'move'; });
    article.addEventListener('drop', async (event) => {
        event.preventDefault(); if (!dragID || dragID === card.id) return;
        const from = config.dashboardCards.findIndex((item) => item.id === dragID); const to = config.dashboardCards.findIndex((item) => item.id === card.id);
        const [moved] = config.dashboardCards.splice(from, 1); config.dashboardCards.splice(to, 0, moved); await saveConfig();
    });
}

async function moveCard(id, direction) {
    const from = config.dashboardCards.findIndex((card) => card.id === id);
    const visible = activeCards();
    const current = visible.findIndex((card) => card.id === id);
    const target = visible[current + direction];
    if (from < 0 || !target) return;
    const to = config.dashboardCards.findIndex((card) => card.id === target.id);
    const [moved] = config.dashboardCards.splice(from, 1);
    config.dashboardCards.splice(to, 0, moved);
    await saveConfig();
}

function attachResize(article, card) {
    const handle = article.querySelector('.resize-handle');
    const wrap = article.querySelector('.chart-wrap');
    handle.addEventListener('pointerdown', (event) => {
        event.preventDefault();
        const startY = event.clientY;
        const startHeight = wrap.clientHeight;
        handle.setPointerCapture(event.pointerId);
        const resize = (move) => {
            const height = Math.min(640, Math.max(140, startHeight + move.clientY - startY));
            wrap.style.height = `${height}px`;
            charts.get(card.id)?.setSize(chartSize(wrap.querySelector('.chart')));
        };
        const finish = async (up) => {
            resize(up);
            card.height = wrap.clientHeight;
            handle.removeEventListener('pointermove', resize);
            handle.removeEventListener('pointerup', finish);
            await saveConfig();
        };
        handle.addEventListener('pointermove', resize);
        handle.addEventListener('pointerup', finish);
    });
}

async function start() { try { const interval = Number($('interval').value); if (interval > 0) config.interval = interval; await saveConfig(); await api('/api/start', 'POST'); monitoring = true; updateControls(); setStatus('live', 'Monitoring live'); } catch (error) { setStatus('error', `Start failed: ${error.message}`); } }
async function stop() { try { await api('/api/stop', 'POST'); monitoring = false; updateControls(); setStatus('', 'Monitoring stopped'); } catch (error) { setStatus('error', `Stop failed: ${error.message}`); } }
function escapeHTML(value) { const el = document.createElement('div'); el.textContent = value || ''; return el.innerHTML; }
init();
