const nsSelect = document.getElementById('namespace-select');
const wlSelect = document.getElementById('workload-select');
const groupSelect = document.getElementById('group-select');
const podSearch = document.getElementById('pod-search');
const podCount = document.getElementById('pod-count');
const tree = document.getElementById('pods-view');
const topoEl = document.getElementById('nodes-view');
const workloadsEl = document.getElementById('workloads-view');
const tooltipEl = document.getElementById('tooltip');
const detailPanel = document.getElementById('detail-panel');
const detailTitle = document.getElementById('detail-title');
const detailBody = document.getElementById('detail-body');

const sidebar = document.getElementById('sidebar');
const clusterListEl = document.getElementById('cluster-list');

let apiBase = ''; // empty = same origin (browser mode), set to http://... in native mode
let activeTab = 'nodes';
let allNodes = [];
let allPods = [];
let allMetrics = {}; // key: "namespace/podName" -> { cpuMilli, memBytes }
let prevPodState = new Map(); // key: "namespace/podName" -> status (for animation tracking)
let searchCaseSensitive = false;
let searchRegex = false;
let allWorkloadStatuses = {}; // key: "kind/namespace/name" -> WorkloadStatus
let workloadsNeedFullLayout = true; // set to true when tab switches or view changes
const collapsedPools = new Set(); // track collapsed pool/category headers
let clusters = [];
// Track expand state for group headers (keyed by group path)
const expanded = new Map();
// Track which pods are expanded to show container details
const expandedPods = new Set();
// Track which pods have their container dots fully expanded
const expandedContainers = new Set();
const MAX_DOTS = 5;

function apiURL(path) {
  return apiBase + path;
}

async function fetchJSON(path) {
  const res = await fetch(apiURL(path));
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json();
}

function statusClass(status) {
  return 'status status-' + status.toLowerCase().replace(/[^a-z]/g, '');
}

function restartsClass(n) {
  if (n >= 10) return 'restarts-bad';
  if (n >= 3) return 'restarts-warn';
  return '';
}

function esc(s) {
  if (!s) return '';
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// Convert ANSI escape codes to HTML spans
const ansiColors = {
  30: '#666', 31: '#f85149', 32: '#3fb950', 33: '#d29922',
  34: '#58a6ff', 35: '#bc8cff', 36: '#76e3ea', 37: '#c9d1d9',
  90: '#8b949e', 91: '#ff7b72', 92: '#56d364', 93: '#e3b341',
  94: '#79c0ff', 95: '#d2a8ff', 96: '#a5d6ff', 97: '#f0f6fc',
};

function ansiToHtml(text) {
  let html = '';
  let open = false;
  let i = 0;
  while (i < text.length) {
    // Match ESC[ ... m
    if (text.charCodeAt(i) === 0x1b && text[i + 1] === '[') {
      const end = text.indexOf('m', i + 2);
      if (end !== -1) {
        const codes = text.slice(i + 2, end).split(';').map(Number);
        if (open) { html += '</span>'; open = false; }
        for (const code of codes) {
          if (code === 0 || code === 39) { /* reset */ }
          else if (code === 1) { html += '<span style="font-weight:bold">'; open = true; }
          else if (ansiColors[code]) { html += `<span style="color:${ansiColors[code]}">`; open = true; }
        }
        i = end + 1;
        continue;
      }
    }
    // Escape HTML characters
    const ch = text[i];
    if (ch === '<') html += '&lt;';
    else if (ch === '>') html += '&gt;';
    else if (ch === '&') html += '&amp;';
    else html += ch;
    i++;
  }
  if (open) html += '</span>';
  return html;
}

// Build a map of node name -> NodeInfo for quick lookup
function nodeMap() {
  const m = new Map();
  for (const n of allNodes) m.set(n.name, n);
  return m;
}

async function loadNamespaces() {
  const namespaces = await fetchJSON('/api/namespaces');
  const current = nsSelect.value;
  nsSelect.innerHTML = '<option value="">All namespaces</option>';
  for (const ns of namespaces) {
    const opt = document.createElement('option');
    opt.value = ns;
    opt.textContent = ns;
    if (ns === current) opt.selected = true;
    nsSelect.appendChild(opt);
  }
}

const progressBar = document.getElementById('progress-bar');
const progressFill = document.getElementById('progress-fill');
const progressText = document.getElementById('progress-text');

function showProgress(pct, text) {
  progressBar.classList.remove('hidden');
  progressFill.style.width = pct + '%';
  progressText.textContent = text;
}

function hideProgress() {
  progressBar.classList.add('hidden');
  hideSkeleton();
}

function showSkeleton() {
  // Generate skeleton cards for whichever tab is visible
  function skeletonCards(count, dotsPerCard) {
    let html = '';
    for (let i = 0; i < count; i++) {
      const dots = Array.isArray(dotsPerCard) ? dotsPerCard[i] : dotsPerCard;
      html += `<div class="topo-machine skeleton-card">`;
      html += `<div class="topo-machine-header"><span class="skeleton-text skeleton-w100"></span><span class="skeleton-text skeleton-w30"></span></div>`;
      html += `<div class="topo-machine-resources"><span class="skeleton-text skeleton-w80"></span></div>`;
      if (dots > 0) {
        html += `<div class="topo-pods">`;
        for (let d = 0; d < dots; d++) {
          html += `<div class="topo-dot skeleton-dot"></div>`;
        }
        html += `</div>`;
      }
      html += `</div>`;
    }
    return html;
  }

  // Nodes tab: skeleton with pool groups
  const pools = [
    { nodes: 4, dots: 6 },
    { nodes: 3, dots: 0 },
    { nodes: 5, dots: [45, 30, 35, 25, 40] },
    { nodes: 4, dots: [20, 15, 25, 18] },
  ];
  let nodesHtml = '';
  for (const pool of pools) {
    nodesHtml += `<div class="topo-pool skeleton-pool">`;
    nodesHtml += `<div class="topo-pool-header"><span class="skeleton-text skeleton-w120"></span> <span class="skeleton-text skeleton-w80"></span></div>`;
    nodesHtml += `<div class="topo-machines">${skeletonCards(pool.nodes, pool.dots)}</div></div>`;
  }
  topoEl.innerHTML = nodesHtml;

  // Workloads tab: skeleton with flat cards
  let wlHtml = `<div class="topo-pool skeleton-pool">`;
  wlHtml += `<div class="topo-pool-header"><span class="skeleton-text skeleton-w120"></span> <span class="skeleton-text skeleton-w80"></span></div>`;
  wlHtml += `<div class="topo-machines">${skeletonCards(12, [40, 30, 25, 20, 15, 12, 10, 8, 8, 6, 5, 4])}</div></div>`;
  workloadsEl.innerHTML = wlHtml;
}

function hideSkeleton() {
  // skeleton gets replaced by real content on next render
}

const errorBanner = document.getElementById('error-banner');
const errorBannerText = document.getElementById('error-banner-text');

function showErrorBanner(msg) {
  errorBannerText.textContent = msg;
  errorBanner.classList.remove('hidden');
}

function hideErrorBanner() {
  errorBanner.classList.add('hidden');
}

const syncSpinner = document.getElementById('sync-spinner');

function showSyncing() {
  syncSpinner.classList.remove('hidden');
}

function hideSyncing() {
  syncSpinner.classList.add('hidden');
}

// Wait for cache to be ready, showing progress
async function waitForCache() {
  let shownCachedData = false;
  let fakeProgress = 5;
  let hasDiskCache = false;
  while (true) {
    try {
      const p = await fetchJSON('/api/progress');

      // If ready (even from disk cache), load and show data immediately
      if (p.ready && !shownCachedData) {
        shownCachedData = true;
        hasDiskCache = true;
        await loadDataQuiet();
        // Show subtle spinner instead of progress bar
        hideProgress();
        showSyncing();
      }

      if (!shownCachedData && !hasDiskCache) {
        // No cache — show full progress bar and skeleton
        if (fakeProgress === 5) {
          showProgress(5, 'Connecting to cluster...');
          showSkeleton();
        }
      }

      if (p.error) {
        hideProgress();
        hideSyncing();
        showErrorBanner(p.error);
        return;
      }

      // If live data is loaded (not just disk cache), we're done
      if (p.ready && !p.loading) {
        hideProgress();
        hideSyncing();
        hideErrorBanner();
        return;
      }

      // Still loading — update progress bar only if no disk cache
      fakeProgress += (90 - fakeProgress) * 0.08;
      if (!hasDiskCache) {
        if (p.total > 0) {
          const realPct = Math.round((p.current / p.total) * 100);
          const pct = Math.max(fakeProgress, realPct);
          showProgress(pct, `Loading... (${p.current}/${p.total})`);
        } else {
          showProgress(fakeProgress, 'Loading cluster data...');
        }
      }
    } catch (e) {
      // server not ready yet
      if (!hasDiskCache && fakeProgress === 5) {
        showProgress(5, 'Connecting to cluster...');
        showSkeleton();
      }
    }
    await new Promise(r => setTimeout(r, 300));
  }
}

// All API calls are instant (served from server-side cache)
async function loadDataQuiet() {
  const [nodesResult, podsResult, metricsResult, workloadsResult] = await Promise.allSettled([
    fetchJSON('/api/nodes'),
    fetchJSON('/api/pods'),
    fetchJSON('/api/metrics'),
    fetchJSON('/api/workloads'),
  ]);
  if (nodesResult.status === 'fulfilled') allNodes = nodesResult.value;
  if (podsResult.status === 'fulfilled') {
    if (allPods.length > 0) {
      snapshotPodState();
      animEnabled = true;
    }
    allPods = podsResult.value;
  }
  if (metricsResult.status === 'fulfilled' && metricsResult.value) {
    allMetrics = {};
    for (const pm of metricsResult.value) {
      let totalCPU = 0, totalMem = 0;
      for (const cm of (pm.containers || [])) {
        totalCPU += cm.cpuMilli;
        totalMem += cm.memBytes;
      }
      allMetrics[pm.namespace + '/' + pm.name] = { cpuMilli: totalCPU, memBytes: totalMem };
    }
  }
  if (workloadsResult.status === 'fulfilled' && workloadsResult.value) {
    allWorkloadStatuses = {};
    for (const ws of workloadsResult.value) {
      allWorkloadStatuses[ws.kind + '/' + ws.namespace + '/' + ws.name] = ws;
    }
  }
  populateWorkloads();
  render();
  checkPendingDeepLink();
}

async function loadData() {
  await loadDataQuiet();
}

// --- Grouping helpers ---

function groupBy(pods, keyFn) {
  const groups = new Map();
  for (const p of pods) {
    const key = keyFn(p) || '__none__';
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(p);
  }
  return groups;
}

function workloadKey(p) {
  return p.workloadKind + '/' + p.namespace + '/' + (p.workloadName || p.name);
}

// --- Rendering ---

function matchesSearch(name, filter) {
  if (!filter) return true;
  if (searchRegex) {
    try {
      const flags = searchCaseSensitive ? '' : 'i';
      return new RegExp(filter, flags).test(name);
    } catch (e) {
      return false; // invalid regex, show nothing
    }
  }
  if (searchCaseSensitive) return name.includes(filter);
  return name.toLowerCase().includes(filter.toLowerCase());
}

function getFilteredPods() {
  const ns = nsSelect.value;
  const wl = wlSelect.value;
  const filter = podSearch.value;

  let filtered = allPods;
  if (ns) filtered = filtered.filter(p => p.namespace === ns);
  if (wl) filtered = filtered.filter(p => workloadKey(p) === wl);
  if (filter) filtered = filtered.filter(p => matchesSearch(p.name, filter));

  podCount.textContent = `${filtered.length} pods`;
  return filtered;
}

function populateWorkloads() {
  const ns = nsSelect.value;
  let pods = allPods;
  if (ns) pods = pods.filter(p => p.namespace === ns);

  const workloads = new Map();
  for (const p of pods) {
    const key = workloadKey(p);
    if (!workloads.has(key)) workloads.set(key, { kind: p.workloadKind, name: p.workloadName || p.name, namespace: p.namespace });
  }

  const current = wlSelect.value;
  wlSelect.innerHTML = '<option value="">All workloads</option>';
  const sorted = [...workloads.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  for (const [key, w] of sorted) {
    const opt = document.createElement('option');
    opt.value = key;
    opt.textContent = `${w.name} (${w.kind})`;
    if (key === current) opt.selected = true;
    wlSelect.appendChild(opt);
  }
}

function renderTree() {
  const filtered = getFilteredPods();

  const mode = groupSelect.value;
  let html = '';

  switch (mode) {
    case 'flat':
      html = renderFlat(filtered);
      break;
    case 'node':
      html = renderByNode(filtered);
      break;
    case 'workload':
      html = renderByWorkload(filtered);
      break;
    case 'node-workload':
      html = renderByNodeWorkload(filtered);
      break;
    case 'workload-node':
      html = renderByWorkloadNode(filtered);
      break;
  }

  tree.innerHTML = html;
  attachListeners();
}

function renderFlat(pods) {
  const sorted = sortPods(pods);
  let html = podTableHeader();
  html += `<div class="flat-list">`;
  for (const p of sorted) {
    html += podRow(p, true);
  }
  html += `</div>`;
  return html;
}

function renderByNode(pods) {
  const nodes = nodeMap();
  const byNode = groupBy(pods, p => p.node);
  let html = '';

  for (const n of allNodes) {
    const nodePods = byNode.get(n.name) || [];
    if (podSearch.value && nodePods.length === 0) continue;
    html += renderGroup(n.name, nodePods, nodeHeaderContent(n, nodePods.length));
  }
  const unassigned = byNode.get('__none__') || byNode.get('') || [];
  if (unassigned.length > 0) {
    html += renderGroup('__unassigned__', unassigned, `<span class="group-label">Unassigned</span> <span class="pod-count-badge">${unassigned.length} pods</span>`, true);
  }
  return html;
}

function renderByWorkload(pods) {
  // First level: workload kind (Deployment, StatefulSet, etc.)
  const byKind = groupBy(pods, p => p.workloadKind || 'Pod');
  const kindOrder = [...byKind.keys()].sort();
  let html = '';

  for (const kind of kindOrder) {
    const kindPods = byKind.get(kind);
    const kindKey = 'k:' + kind;
    const isOpen = isExpanded(kindKey);

    // Count unique workloads in this kind
    const workloadNames = new Set(kindPods.map(p => p.namespace + '/' + (p.workloadName || p.name)));

    html += `<div class="tree-node">`;
    html += `<div class="group-header" data-group="${esc(kindKey)}">`;
    html += `<span class="node-toggle ${isOpen ? 'open' : ''}">&#9654;</span>`;
    html += `<span class="workload-kind">${esc(kind)}</span>`;
    html += `<span class="pod-count-badge">${workloadNames.size} workloads</span>`;
    html += `<span class="pod-count-badge">${kindPods.length} pods</span>`;
    html += `</div>`;
    html += `<div class="group-children ${isOpen ? '' : 'collapsed'}">`;

    // Second level: individual workloads
    const byWorkload = groupBy(kindPods, p => p.namespace + '/' + (p.workloadName || p.name));
    const sorted = [...byWorkload.entries()].sort((a, b) => a[0].localeCompare(b[0]));
    for (const [wKey, wPods] of sorted) {
      const name = wPods[0].workloadName || wPods[0].name;
      if (podSearch.value && wPods.length === 0) continue;
      html += renderGroup(kindKey + '/' + wKey, wPods, workloadHeaderContent(kind, name, wPods.length), false, true);
    }

    html += `</div></div>`;
  }
  return html;
}

function renderByNodeWorkload(pods) {
  const nodes = nodeMap();
  const byNode = groupBy(pods, p => p.node);
  let html = '';

  for (const n of allNodes) {
    const nodePods = byNode.get(n.name) || [];
    if (podSearch.value && nodePods.length === 0) continue;

    const key = 'n:' + n.name;
    const isOpen = isExpanded(key);
    html += `<div class="tree-node">`;
    html += `<div class="group-header" data-group="${esc(key)}">`;
    html += `<span class="node-toggle ${isOpen ? 'open' : ''}">&#9654;</span>`;
    html += nodeHeaderContent(n, nodePods.length);
    html += `</div>`;
    html += `<div class="group-children ${isOpen ? '' : 'collapsed'}">`;

    // Sub-group by workload
    const byWorkload = groupBy(nodePods, workloadKey);
    const sorted = [...byWorkload.entries()].sort((a, b) => a[0].localeCompare(b[0]));
    for (const [wKey, wPods] of sorted) {
      const [kind, name] = splitOnce(wKey, '/');
      html += renderGroup(key + '/' + wKey, wPods, workloadHeaderContent(kind, name, wPods.length), false, true);
    }

    html += `</div></div>`;
  }
  return html;
}

function renderByWorkloadNode(pods) {
  // First level: workload kind
  const byKind = groupBy(pods, p => p.workloadKind || 'Pod');
  const kindOrder = [...byKind.keys()].sort();
  let html = '';

  for (const kind of kindOrder) {
    const kindPods = byKind.get(kind);
    const kindKey = 'wn-k:' + kind;
    const isKindOpen = isExpanded(kindKey);

    const workloadNames = new Set(kindPods.map(p => p.namespace + '/' + (p.workloadName || p.name)));

    html += `<div class="tree-node">`;
    html += `<div class="group-header" data-group="${esc(kindKey)}">`;
    html += `<span class="node-toggle ${isKindOpen ? 'open' : ''}">&#9654;</span>`;
    html += `<span class="workload-kind">${esc(kind)}</span>`;
    html += `<span class="pod-count-badge">${workloadNames.size} workloads</span>`;
    html += `<span class="pod-count-badge">${kindPods.length} pods</span>`;
    html += `</div>`;
    html += `<div class="group-children ${isKindOpen ? '' : 'collapsed'}">`;

    // Second level: individual workloads
    const byWorkload = groupBy(kindPods, p => p.namespace + '/' + (p.workloadName || p.name));
    const sortedWorkloads = [...byWorkload.entries()].sort((a, b) => a[0].localeCompare(b[0]));

    for (const [wGroupKey, wPods] of sortedWorkloads) {
      const wName = wPods[0].workloadName || wPods[0].name;
      if (podSearch.value && wPods.length === 0) continue;

      const wKey = kindKey + '/' + wGroupKey;
      const isWOpen = isExpanded(wKey);
      html += `<div class="tree-node nested">`;
      html += `<div class="group-header" data-group="${esc(wKey)}">`;
      html += `<span class="node-toggle ${isWOpen ? 'open' : ''}">&#9654;</span>`;
      html += `<span class="group-label">${esc(wName)}</span>`;
      html += `<span class="pod-count-badge">${wPods.length} pods</span>`;
      html += `</div>`;
      html += `<div class="group-children ${isWOpen ? '' : 'collapsed'}">`;

      // Third level: nodes
      const byNode = groupBy(wPods, p => p.node);
      for (const n of allNodes) {
        const nPods = byNode.get(n.name);
        if (!nPods) continue;
        html += renderGroup(wKey + '/' + n.name, nPods, nodeHeaderContent(n, nPods.length), false, true);
      }
      const unassigned = byNode.get('__none__') || byNode.get('') || [];
      if (unassigned.length > 0) {
        html += renderGroup(wKey + '/__unassigned__', unassigned, `<span class="group-label">Unassigned</span> <span class="pod-count-badge">${unassigned.length}</span>`, true, true);
      }

      html += `</div></div>`;
    }

    html += `</div></div>`;
  }
  return html;
}

// --- Shared rendering blocks ---

function nodeHeaderContent(n, count) {
  return `<span class="${statusClass(n.status)}">${esc(n.status)}</span>` +
    `<span class="group-label">${esc(n.name)}</span>` +
    `<span class="pod-count-badge">${count} pods</span>` +
    `<span class="node-meta">` +
    `<span>${esc(n.roles)}</span>` +
    `<span>${esc(n.kubeletVersion)}</span>` +
    `<span>${esc(n.cpuCapacity)} CPU</span>` +
    `<span>${esc(n.memoryCapacity)}</span>` +
    `</span>`;
}

function workloadHeaderContent(kind, name, count) {
  return `<span class="workload-kind">${esc(kind)}</span>` +
    `<span class="group-label">${esc(name)}</span>` +
    `<span class="pod-count-badge">${count} pods</span>`;
}

function renderGroup(key, pods, headerContent, dashed, nested) {
  const isOpen = isExpanded(key);
  let html = `<div class="tree-node ${nested ? 'nested' : ''}">`;
  html += `<div class="group-header ${dashed ? 'dashed' : ''}" data-group="${esc(key)}">`;
  html += `<span class="node-toggle ${isOpen ? 'open' : ''}">&#9654;</span>`;
  html += headerContent;
  html += `</div>`;
  html += `<div class="pod-list ${isOpen ? '' : 'collapsed'}">`;
  for (const p of pods) {
    html += podRow(p);
  }
  html += `</div></div>`;
  return html;
}

function isExpanded(key) {
  if (!expanded.has(key)) expanded.set(key, true);
  return expanded.get(key);
}

// Column definitions: name, default width
// Default widths:
// Containers: 9.5 squares = 9.5 * (8px dot + 2px gap) = 95px
// Status: ~"ContainerCreating" = ~120px
// Ready, Restarts, Age, Tag: ~10 chars monospace = 85px each
const columns = [
  { name: 'Namespace', min: 120, key: 'namespace' },
  { name: 'Name', min: 100, flex: true, key: 'name' },
  { name: 'Containers', min: 105, align: 'right', key: 'containers' },
  { name: 'Status', min: 120, align: 'right', key: 'status' },
  { name: 'Ready', min: 85, align: 'right', key: 'ready' },
  { name: 'Restarts', min: 95, align: 'right', key: 'restarts' },
  { name: 'Age', min: 85, align: 'right', key: 'age' },
  { name: 'Tag', min: 85, align: 'right', key: 'tag' },
];

let sortCol = null; // column key
let sortAsc = true;

function parseAge(age) {
  if (!age) return 0;
  let total = 0;
  const parts = age.match(/(\d+)(y|d|h|m|s)/g);
  if (!parts) return 0;
  for (const p of parts) {
    const n = parseInt(p);
    const unit = p.slice(-1);
    switch (unit) {
      case 'y': total += n * 365 * 86400; break;
      case 'd': total += n * 86400; break;
      case 'h': total += n * 3600; break;
      case 'm': total += n * 60; break;
      case 's': total += n; break;
    }
  }
  return total;
}

function sortPods(pods) {
  if (!sortCol) return pods;
  const sorted = [...pods];
  sorted.sort((a, b) => {
    let va, vb;
    switch (sortCol) {
      case 'namespace': va = a.namespace; vb = b.namespace; break;
      case 'name': va = a.name; vb = b.name; break;
      case 'containers': va = (a.containers || []).length; vb = (b.containers || []).length; break;
      case 'status': va = a.status; vb = b.status; break;
      case 'ready': va = a.ready; vb = b.ready; break;
      case 'restarts': va = a.restarts; vb = b.restarts; break;
      case 'age': va = parseAge(a.age); vb = parseAge(b.age); break;
      case 'tag':
        va = (a.containers && a.containers.length > 0) ? a.containers[0].tag : '';
        vb = (b.containers && b.containers.length > 0) ? b.containers[0].tag : '';
        break;
      default: return 0;
    }
    if (typeof va === 'string') {
      const cmp = va.localeCompare(vb);
      return sortAsc ? cmp : -cmp;
    }
    return sortAsc ? va - vb : vb - va;
  });
  return sorted;
}

function gridTemplateFromColumns() {
  return columns.map(c => c.flex ? '1fr' : (c.width || c.min) + 'px').join(' ');
}

function podTableHeader() {
  const tpl = gridTemplateFromColumns();
  let h = `<div class="pod-table-header" style="grid-template-columns: ${tpl}">`;
  columns.forEach((col, i) => {
    const align = col.align === 'right' ? ' style="text-align:right"' : '';
    const isSorted = sortCol === col.key;
    const arrow = isSorted ? (sortAsc ? ' &#9650;' : ' &#9660;') : '';
    const sortClass = isSorted ? ' sorted' : '';
    h += `<span class="col-header${sortClass}" data-col="${i}" data-sort-key="${col.key}"${align}>${esc(col.name)}${arrow}`;
    h += `<span class="col-resize" data-col="${i}"></span>`;
    h += `</span>`;
  });
  h += `</div>`;
  return h;
}

function applyGridTemplate() {
  const tpl = gridTemplateFromColumns();
  document.querySelectorAll('.pod-table-header, .pod-row').forEach(el => {
    el.style.gridTemplateColumns = tpl;
  });
}

// Drag-to-resize columns
function initColumnResize() {
  document.querySelectorAll('.col-resize').forEach(handle => {
    handle.addEventListener('mousedown', (e) => {
      e.preventDefault();
      e.stopPropagation();
      const colIdx = parseInt(handle.dataset.col);
      const startX = e.clientX;
      const header = handle.closest('.pod-table-header');
      const cell = header.children[colIdx];

      // Snapshot the actual rendered width
      const startWidth = cell.offsetWidth;

      // If it was flex, lock it to a fixed width from now on
      if (columns[colIdx].flex) {
        columns[colIdx].flex = false;
      }

      handle.classList.add('active');

      const onMove = (e) => {
        const delta = e.clientX - startX;
        columns[colIdx].width = Math.max(30, startWidth + delta);
        applyGridTemplate();
      };

      const onUp = () => {
        handle.classList.remove('active');
        document.removeEventListener('mousemove', onMove);
        document.removeEventListener('mouseup', onUp);
      };

      document.addEventListener('mousemove', onMove);
      document.addEventListener('mouseup', onUp);
    });
  });
}

function podRow(p, flat) {
  const podId = esc(p.namespace + '/' + p.name);
  const isOpen = expandedPods.has(podId);
  const dotsExpanded = expandedContainers.has(podId);
  let r = `<div class="pod-row-wrap">`;
  r += `<div class="pod-row" style="grid-template-columns: ${gridTemplateFromColumns()}" data-pod="${podId}" data-pod-b64="${btoa(JSON.stringify(p))}">`;
  r += `<span class="pod-ns">${esc(p.namespace)}</span>`;
  r += `<span class="pod-name">${esc(p.name)}</span>`;

  r += `<span class="pod-info-containers pod-col-right">`;
  if (p.containers) {
    for (const c of p.containers) {
      r += `<span class="container-dot ${dotClass(c.status)}" title="${esc(c.name)}: ${esc(c.status)}"></span>`;
    }
  }
  r += `</span>`;

  r += `<span class="pod-col pod-col-right"><span class="${statusClass(p.status)}">${esc(p.status)}</span></span>`;
  r += `<span class="pod-col pod-col-right">${esc(p.ready)}</span>`;
  r += `<span class="pod-col pod-col-right ${restartsClass(p.restarts)}">${p.restarts === 0 ? '' : p.restarts}</span>`;
  r += `<span class="pod-col pod-col-right">${esc(p.age)}</span>`;
  const tag = (p.containers && p.containers.length > 0) ? p.containers[0].tag : '';
  r += `<span class="pod-col pod-col-right pod-info-tag" title="${esc(tag)}">${esc(tag)}</span>`;
  r += `</div>`;
  r += `</div>`;
  return r;
}

function splitOnce(s, sep) {
  const i = s.indexOf(sep);
  if (i === -1) return [s, ''];
  return [s.substring(0, i), s.substring(i + 1)];
}

// --- Detail panel ---

function openDetail(p) {
  stopTail();
  detailTitle.textContent = p.name;
  currentDetailPod = p;

  let html = '';

  // Pod info
  html += `<div class="detail-section">`;
  html += `<div class="detail-section-title">Pod</div>`;
  html += detailField('Name', p.name);
  html += detailField('Namespace', p.namespace);
  html += detailField('Status', p.status, statusFontColor(p.status, true));
  html += detailField('Ready', p.ready);
  if (p.restarts > 0) html += detailField('Restarts', p.restarts);
  html += detailField('Age', p.age);
  html += `<div class="detail-field"><span class="detail-field-label">Node</span><a class="detail-field-value detail-node-link" href="#" data-node="${esc(p.node)}">${esc(p.node)}</a></div>`;
  html += `</div>`;

  // Resources
  const mKey = p.namespace + '/' + p.name;
  const m = allMetrics[mKey];
  html += `<div class="detail-section">`;
  html += `<div class="detail-section-title">Resources</div>`;
  if (m) {
    const cpuLim = p.cpuLimitMilli || 0;
    const memLim = p.memLimitBytes || 0;
    const memMi = Math.round(m.memBytes / 1024 / 1024);
    const memLimMi = memLim > 0 ? Math.round(memLim / 1024 / 1024) : 0;

    let cpuDetail = fmtCPU(m.cpuMilli);
    if (cpuLim > 0) {
      cpuDetail += ' / ' + fmtCPU(cpuLim) + ' limit (' + Math.round((m.cpuMilli / cpuLim) * 100) + '%)';
    } else {
      const nodeCPU = getNodeCPUMilli(p.node);
      if (nodeCPU > 0) {
        cpuDetail += ' / ' + fmtCPU(nodeCPU) + ' node (' + Math.round((m.cpuMilli / nodeCPU) * 100) + '%)';
      } else {
        cpuDetail += ' (no limit)';
      }
    }
    const cpuPct = cpuLim > 0 ? (m.cpuMilli / cpuLim) * 100 : (getNodeCPUMilli(p.node) > 0 ? (m.cpuMilli / getNodeCPUMilli(p.node)) * 100 : 0);
    html += detailField('CPU Usage', cpuDetail, cpuPct > 0 ? resourceFontColor(cpuPct, true) : null);
    let memDetail = fmtMem(m.memBytes);
    if (memLim > 0) {
      memDetail += ' / ' + fmtMem(memLim) + ' limit (' + Math.round((m.memBytes / memLim) * 100) + '%)';
    } else {
      const nodeMem = getNodeMemBytes(p.node);
      if (nodeMem > 0) {
        memDetail += ' / ' + fmtMem(nodeMem) + ' node (' + Math.round((m.memBytes / nodeMem) * 100) + '%)';
      } else {
        memDetail += ' (no limit)';
      }
    }
    const memPct = memLim > 0 ? (m.memBytes / memLim) * 100 : (getNodeMemBytes(p.node) > 0 ? (m.memBytes / getNodeMemBytes(p.node)) * 100 : 0);
    html += detailField('Memory Usage', memDetail, memPct > 0 ? resourceFontColor(memPct, true) : null);
  } else {
    html += detailField('CPU Request', p.cpuRequestMilli > 0 ? fmtCPU(p.cpuRequestMilli) : '-');
    html += detailField('CPU Limit', p.cpuLimitMilli > 0 ? fmtCPU(p.cpuLimitMilli) : '-');
    html += detailField('Mem Request', p.memRequestBytes > 0 ? fmtMem(p.memRequestBytes) : '-');
    html += detailField('Mem Limit', p.memLimitBytes > 0 ? fmtMem(p.memLimitBytes) : '-');
  }
  html += `</div>`;

  // Workload
  if (p.workloadName) {
    html += `<div class="detail-section">`;
    html += `<div class="detail-section-title">Workload</div>`;
    html += detailField('Kind', p.workloadKind);
    html += `<div class="detail-field"><span class="detail-field-label">Name</span><a class="detail-field-value detail-workload-link" href="#" data-workload-ns="${esc(p.namespace)}" data-workload-name="${esc(p.workloadName)}">${esc(p.workloadName)}</a></div>`;
    html += `</div>`;
  }

  // Containers
  if (p.containers && p.containers.length > 0) {
    html += `<div class="detail-section">`;
    html += `<div class="detail-section-title">Containers (${p.containers.length})</div>`;
    for (const c of p.containers) {
      html += `<div class="detail-container-card">`;
      html += `<div class="detail-container-header">`;
      html += `<span class="detail-container-name">${esc(c.name)}</span>`;
      html += `<span class="container-dot ${dotClass(c.status)}" title="${esc(c.status)}"></span>`;
      html += `</div>`;
      html += `<div class="detail-image-full">${esc(c.image)}:<span class="detail-image-tag">${esc(c.tag)}</span></div>`;
      html += `</div>`;
    }
    html += `</div>`;
  }

  // Logs
  html += `<div class="detail-section">`;
  html += `<div class="detail-section-title">Logs</div>`;
  html += `<div class="detail-logs-controls">`;
  if (p.containers && p.containers.length > 1) {
    html += `<select id="log-container-select" class="log-container-select">`;
    for (const c of p.containers) {
      html += `<option value="${esc(c.name)}">${esc(c.name)}</option>`;
    }
    html += `</select>`;
  }
  html += `<button class="btn btn-ghost btn-sm" onclick="loadLogs()">Refresh</button>`;
  html += `<button class="btn btn-ghost btn-sm" onclick="loadLogs(500)">More</button>`;
  html += `<button class="btn btn-ghost btn-sm" id="tail-btn" onclick="toggleTail()">Tail</button>`;
  html += `<button class="btn btn-ghost btn-sm" id="pause-btn" onclick="togglePause()" style="display:none">Pause</button>`;
  html += `<span style="flex:1"></span>`;
  html += `<button class="btn btn-ghost btn-sm" onclick="downloadLogs()">Download</button>`;
  html += `<button class="btn btn-ghost btn-sm logs-expand-btn" id="logs-fullscreen-btn" onclick="toggleLogsFullscreen()" title="Expand logs">&#x26F6;</button>`;
  html += `</div>`;
  html += `<pre id="detail-logs" class="detail-logs">Loading...</pre>`;
  html += `</div>`;

  detailBody.innerHTML = html;
  detailPanel.classList.remove('hidden');

  // Wire up node link
  const nodeLink = detailBody.querySelector('.detail-node-link');
  if (nodeLink) {
    nodeLink.addEventListener('click', (e) => {
      e.preventDefault();
      navigateToNode(nodeLink.dataset.node);
    });
  }

  // Wire up workload link
  const wlLink = detailBody.querySelector('.detail-workload-link');
  if (wlLink) {
    wlLink.addEventListener('click', (e) => {
      e.preventDefault();
      navigateToWorkload(wlLink.dataset.workloadNs, wlLink.dataset.workloadName);
    });
  }

  // Load logs
  loadLogs();
}

let currentDetailPod = null;

async function loadLogs(tail) {
  if (!currentDetailPod) return;
  const p = currentDetailPod;
  const logsEl = document.getElementById('detail-logs');
  if (!logsEl) return;

  const containerSelect = document.getElementById('log-container-select');
  const container = containerSelect ? containerSelect.value : (p.containers && p.containers.length > 0 ? p.containers[0].name : '');

  const tailLines = tail || 100;
  try {
    const url = apiURL(`/api/logs?namespace=${encodeURIComponent(p.namespace)}&pod=${encodeURIComponent(p.name)}&container=${encodeURIComponent(container)}&tail=${tailLines}`);
    const res = await fetch(url);
    if (!res.ok) {
      logsEl.textContent = `Error: ${await res.text()}`;
      return;
    }
    const raw = await res.text();
    logsEl.innerHTML = raw ? ansiToHtml(raw) : '(no logs)';
    logsEl.scrollTop = logsEl.scrollHeight;
  } catch (e) {
    logsEl.textContent = `Error: ${e.message}`;
  }
}

let tailEventSource = null;
let tailPaused = false;
let tailBuffer = [];

function getSelectedContainer() {
  const containerSelect = document.getElementById('log-container-select');
  const p = currentDetailPod;
  return containerSelect ? containerSelect.value : (p && p.containers && p.containers.length > 0 ? p.containers[0].name : '');
}

function toggleTail() {
  if (tailEventSource) {
    stopTail();
  } else {
    startTail();
  }
}

let tailLines = [];
let tailFlushTimer = null;

function startTail() {
  if (!currentDetailPod) return;
  const p = currentDetailPod;
  const container = getSelectedContainer();

  stopTail();
  tailPaused = false;
  tailBuffer = [];
  tailLines = [];

  const url = apiURL(`/api/logs/stream?namespace=${encodeURIComponent(p.namespace)}&pod=${encodeURIComponent(p.name)}&container=${encodeURIComponent(container)}`);
  tailEventSource = new EventSource(url);

  const logsEl = document.getElementById('detail-logs');
  const tailBtn = document.getElementById('tail-btn');
  const pauseBtn = document.getElementById('pause-btn');
  if (tailBtn) { tailBtn.textContent = 'Stop'; tailBtn.classList.add('btn-active'); }
  if (pauseBtn) pauseBtn.style.display = '';

  // Batch incoming lines, flush to DOM every 200ms
  let pendingLines = [];

  function flushToDOM() {
    if (!logsEl || pendingLines.length === 0) return;
    tailLines.push(...pendingLines);
    // Keep last 2000 lines
    if (tailLines.length > 2000) {
      tailLines = tailLines.slice(-2000);
    }
    pendingLines = [];
    logsEl.innerHTML = ansiToHtml(tailLines.join('\n'));
    logsEl.scrollTop = logsEl.scrollHeight;
  }

  tailFlushTimer = setInterval(flushToDOM, 200);

  tailEventSource.addEventListener('message', (e) => {
    if (tailPaused) {
      tailBuffer.push(e.data);
      // Cap buffer to avoid memory issues
      if (tailBuffer.length > 5000) tailBuffer = tailBuffer.slice(-5000);
      if (pauseBtn) pauseBtn.textContent = `Resume (${tailBuffer.length})`;
      return;
    }
    pendingLines.push(e.data);
  });

  tailEventSource.addEventListener('error', () => {
    flushToDOM();
    stopTail();
  });
}

function stopTail() {
  if (tailEventSource) {
    tailEventSource.close();
    tailEventSource = null;
  }
  if (tailFlushTimer) {
    clearInterval(tailFlushTimer);
    tailFlushTimer = null;
  }
  tailPaused = false;
  tailBuffer = [];
  const tailBtn = document.getElementById('tail-btn');
  const pauseBtn = document.getElementById('pause-btn');
  if (tailBtn) { tailBtn.textContent = 'Tail'; tailBtn.classList.remove('btn-active'); }
  if (pauseBtn) { pauseBtn.style.display = 'none'; pauseBtn.textContent = 'Pause'; }
}

function togglePause() {
  const logsEl = document.getElementById('detail-logs');
  const pauseBtn = document.getElementById('pause-btn');
  if (tailPaused) {
    // Resume — flush buffer into tailLines
    tailPaused = false;
    tailLines.push(...tailBuffer);
    if (tailLines.length > 2000) {
      tailLines = tailLines.slice(-2000);
    }
    tailBuffer = [];
    if (logsEl) {
      logsEl.innerHTML = ansiToHtml(tailLines.join('\n'));
      logsEl.scrollTop = logsEl.scrollHeight;
    }
    if (pauseBtn) pauseBtn.textContent = 'Pause';
  } else {
    tailPaused = true;
    if (pauseBtn) pauseBtn.textContent = 'Resume (0)';
  }
}

function downloadLogs() {
  if (!currentDetailPod) return;
  const p = currentDetailPod;
  const container = getSelectedContainer();
  const url = apiURL(`/api/logs/download?namespace=${encodeURIComponent(p.namespace)}&pod=${encodeURIComponent(p.name)}&container=${encodeURIComponent(container)}`);
  window.open(url, '_blank');
}

let podActionsMenu = null;

function showPodActionsMenu(e) {
  closePodActionsMenu();
  const btn = e.currentTarget || e.target;
  const rect = btn.getBoundingClientRect();

  const menu = document.createElement('div');
  menu.className = 'pod-actions-menu';
  menu.style.left = rect.left + 'px';
  menu.style.top = (rect.bottom + 4) + 'px';

  const p = currentDetailPod;
  const items = [
    { label: 'Copy pod link', action: () => copyPodLink(p) },
    { sep: true },
    { label: 'Open shell in terminal', action: () => openTerminalExec() },
    { label: 'Copy shell command', action: () => copyTerminalCmd('exec') },
    { sep: true },
    { label: 'Tail logs in terminal', action: () => openTerminalLogs() },
    { label: 'Copy tail command', action: () => copyTerminalCmd('logs') },
  ];
  if (appSettings.allowActions) {
    items.push({ sep: true });
    items.push({ label: 'Delete pod', action: () => confirmDeletePod(p), danger: true });
    items.push({ sep: true });
    items.push({ label: 'Restart ' + (p.workloadKind || 'workload'), action: () => confirmRestartWorkload(p) });
  }

  for (const item of items) {
    if (item.sep) {
      const sep = document.createElement('div');
      sep.className = 'pod-actions-sep';
      menu.appendChild(sep);
      continue;
    }
    const el = document.createElement('div');
    el.className = 'pod-actions-item' + (item.danger ? ' danger' : '');
    el.textContent = item.label;
    el.addEventListener('click', () => {
      closePodActionsMenu();
      item.action();
    });
    menu.appendChild(el);
  }

  document.body.appendChild(menu);
  podActionsMenu = menu;

  setTimeout(() => {
    document.addEventListener('click', closePodActionsMenu, { once: true });
  }, 0);
}

function closePodActionsMenu() {
  if (podActionsMenu) {
    podActionsMenu.remove();
    podActionsMenu = null;
  }
}

function podLink(p) {
  const active = clusters.find(c => c.active);
  const server = active ? active.server : '';
  const path = 'pod/' + encodeURIComponent(p.namespace) + '/' + encodeURIComponent(p.name) + '@' + encodeURIComponent(server);
  return 'https://vuek8.app/open#' + path;
}

function copyPodLink(p) {
  navigator.clipboard.writeText(podLink(p)).then(() => showToast('Pod link copied'));
}

function showPodContextMenu(p, e) {
  e.preventDefault();
  closePodActionsMenu();

  const menu = document.createElement('div');
  menu.className = 'pod-actions-menu';
  menu.style.left = e.clientX + 'px';
  menu.style.top = e.clientY + 'px';

  const items = [
    { label: 'View details', action: () => openDetail(p) },
    { label: 'Copy pod link', action: () => copyPodLink(p) },
    { sep: true },
    { label: 'Copy shell command', action: () => { currentDetailPod = p; copyTerminalCmd('exec'); } },
    { label: 'Copy tail command', action: () => { currentDetailPod = p; copyTerminalCmd('logs'); } },
  ];
  if (appSettings.allowActions) {
    items.push({ sep: true });
    items.push({ label: 'Delete pod', action: () => confirmDeletePod(p), danger: true });
  }

  for (const item of items) {
    if (item.sep) {
      const sep = document.createElement('div');
      sep.className = 'pod-actions-sep';
      menu.appendChild(sep);
      continue;
    }
    const el = document.createElement('div');
    el.className = 'pod-actions-item' + (item.danger ? ' danger' : '');
    el.textContent = item.label;
    el.addEventListener('click', () => {
      closePodActionsMenu();
      item.action();
    });
    menu.appendChild(el);
  }

  document.body.appendChild(menu);
  podActionsMenu = menu;

  // Flip menu if it overflows viewport
  requestAnimationFrame(() => {
    const rect = menu.getBoundingClientRect();
    if (rect.right > window.innerWidth) menu.style.left = (e.clientX - rect.width) + 'px';
    if (rect.bottom > window.innerHeight) menu.style.top = (e.clientY - rect.height) + 'px';
  });

  setTimeout(() => {
    document.addEventListener('click', closePodActionsMenu, { once: true });
  }, 0);
}

async function confirmDeletePod(p) {
  if (!confirm(`Delete pod ${p.name} in namespace ${p.namespace}?`)) return;
  try {
    const res = await fetch(apiURL('/api/actions/delete-pod'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ namespace: p.namespace, pod: p.name }),
    });
    const data = await res.json();
    if (data.error) {
      showToast('Error: ' + data.error);
    } else {
      showToast('Pod ' + p.name + ' deleted');
      closeDetail();
    }
  } catch (e) {
    showToast('Error: ' + e.message);
  }
}

async function confirmRestartWorkload(p) {
  const kind = p.workloadKind || p.kind || 'Deployment';
  const name = p.workloadName || p.name;
  const ns = p.namespace;
  if (!confirm(`Restart ${kind} ${name}?`)) return;
  try {
    const res = await fetch(apiURL('/api/actions/restart-workload'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ namespace: ns, workloadName: name, workloadKind: kind }),
    });
    const data = await res.json();
    if (data.error) {
      showToast('Error: ' + data.error);
    } else {
      showToast(kind + ' ' + name + ' restarting');
    }
  } catch (e) {
    showToast('Error: ' + e.message);
  }
}

function getActiveClusterInfo() {
  const active = clusters.find(c => c.active);
  return active ? { kubeconfigPath: active.filePath, contextName: active.contextName } : {};
}

async function openTerminalLogs() {
  if (!currentDetailPod) return;
  const p = currentDetailPod;
  const cluster = getActiveClusterInfo();
  const containerSelect = document.getElementById('log-container-select');
  const container = containerSelect ? containerSelect.value : (p.containers && p.containers.length > 0 ? p.containers[0].name : '');

  const res = await fetch(apiURL('/api/terminal/logs'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      namespace: p.namespace,
      pod: p.name,
      container,
      kubeconfigPath: cluster.kubeconfigPath,
      contextName: cluster.contextName,
    }),
  });
  const data = await res.json();
  if (data.error) {
    alert(`Could not open terminal. Run manually:\n\n${data.command}`);
  }
}

async function openTerminalExec() {
  if (!currentDetailPod) return;
  const p = currentDetailPod;
  const cluster = getActiveClusterInfo();
  const containerSelect = document.getElementById('log-container-select');
  const container = containerSelect ? containerSelect.value : (p.containers && p.containers.length > 0 ? p.containers[0].name : '');

  const res = await fetch(apiURL('/api/terminal/exec'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      namespace: p.namespace,
      pod: p.name,
      container,
      kubeconfigPath: cluster.kubeconfigPath,
      contextName: cluster.contextName,
    }),
  });
  const data = await res.json();
  if (data.error) {
    alert(`Could not open terminal. Run manually:\n\n${data.command}`);
  }
}

async function copyTerminalCmd(action) {
  if (!currentDetailPod) return;
  const p = currentDetailPod;
  const cluster = getActiveClusterInfo();
  const containerSelect = document.getElementById('log-container-select');
  const container = containerSelect ? containerSelect.value : (p.containers && p.containers.length > 0 ? p.containers[0].name : '');

  const res = await fetch(`/api/terminal/${action}?dryRun=true`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      namespace: p.namespace,
      pod: p.name,
      container,
      kubeconfigPath: cluster.kubeconfigPath,
      contextName: cluster.contextName,
    }),
  });
  const data = await res.json();
  if (data.command) {
    await navigator.clipboard.writeText(data.command);
    // Brief visual feedback
    const btn = event.target;
    const orig = btn.innerHTML;
    btn.innerHTML = '&#10003;';
    setTimeout(() => { btn.innerHTML = orig; }, 1000);
  }
}

function closeDetail() {
  stopTail();
  detailPanel.classList.add('hidden');
  detailPanel.classList.remove('logs-fullscreen');
  currentDetailPod = null;
}

function toggleLogsFullscreen() {
  detailPanel.classList.toggle('logs-fullscreen');
  const btn = document.getElementById('logs-fullscreen-btn');
  if (detailPanel.classList.contains('logs-fullscreen')) {
    detailPanel.dataset.prevWidth = detailPanel.style.width || '';
    detailPanel.style.width = '';
    btn.innerHTML = '&#x2716;';
    btn.title = 'Collapse logs';
  } else {
    detailPanel.style.width = detailPanel.dataset.prevWidth || '';
    btn.innerHTML = '&#x26F6;';
    btn.title = 'Expand logs';
  }
}

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && detailPanel.classList.contains('logs-fullscreen')) {
    toggleLogsFullscreen();
  }
});

function detailField(label, value, colorStyle) {
  const style = colorStyle ? ` style="color:${colorStyle}"` : '';
  return `<div class="detail-field"><span class="detail-field-label">${esc(label)}</span><span class="detail-field-value"${style} title="${esc(String(value))}">${esc(String(value))}</span></div>`;
}

// --- Topology view ---

function nodePool(name) {
  // Strip trailing instance identifiers:
  //   "other-5" → "other"               (numeric index)
  //   "shards-dc3-10" → "shards-dc3"    (numeric index)
  //   "app-pool-a7f8d2e19b" → "app-pool"   (hash suffix)
  const parts = name.split('-');
  let i = parts.length - 1;
  while (i > 0) {
    const seg = parts[i];
    if (/^\d+$/.test(seg)) {
      i--;
    } else if (seg.length > 5 && /^[0-9a-f]+$/i.test(seg)) {
      i--;
    } else {
      break;
    }
  }
  return parts.slice(0, i + 1).join('-');
}

function nodeSubnet(node) {
  if (!node.ip) return 'unknown';
  const parts = node.ip.split('.');
  return parts.slice(0, 3).join('.') + '.x';
}

function getTopoGroupKey(node) {
  const mode = document.getElementById('topo-group').value;
  switch (mode) {
    case 'pool': return nodePool(node.name);
    case 'role': return node.roles || 'unknown';
    case 'subnet': return nodeSubnet(node);
    case 'flat': return 'All nodes';
    case 'label': {
      const labelKey = document.getElementById('topo-label-select').value;
      if (!labelKey) return 'unlabeled';
      return (node.labels && node.labels[labelKey]) || 'unlabeled';
    }
    default: return nodePool(node.name);
  }
}

function dotClass(status, ready) {
  const s = status.toLowerCase().replace(/[^a-z]/g, '');
  if (s === 'running' && ready) {
    const parts = ready.split('/');
    if (parts.length === 2 && parts[0] !== parts[1]) return 'topo-dot topo-dot-notready';
  }
  const known = ['running','succeeded','completed','pending','containercreating','failed','error','crashloopbackoff','imagepullbackoff','errimagepull','terminating'];
  return 'topo-dot topo-dot-' + (known.includes(s) ? s : 'unknown');
}

let animEnabled = false; // skip animations on first load / cluster switch

function dotAnim(p) {
  if (!animEnabled) return '';
  const key = p.namespace + '/' + p.name;
  const prev = prevPodState.get(key);
  if (prev === undefined) return ' topo-dot-new';
  if (prev !== p.status) return ' topo-dot-changed';
  return '';
}

function snapshotPodState() {
  prevPodState = new Map();
  for (const p of allPods) {
    prevPodState.set(p.namespace + '/' + p.name, p.status);
  }
}

function getColorMode() {
  return document.getElementById('color-mode').value;
}

// Compute aggregated node utilization from pod metrics
function getNodeUtilization(nodeName) {
  let cpuMilli = 0, memBytes = 0;
  for (const p of allPods) {
    if (p.node !== nodeName) continue;
    const key = p.namespace + '/' + p.name;
    const m = allMetrics[key];
    if (m) {
      cpuMilli += m.cpuMilli || 0;
      memBytes += m.memBytes || 0;
    }
  }
  return { cpuMilli, memBytes };
}

function resourceBgColor(pct) {
  if (pct < 15) return 'background: rgba(63, 185, 80, 0.08);';
  if (pct < 40) return 'background: rgba(63, 185, 80, 0.18);';
  if (pct < 65) return 'background: rgba(210, 153, 34, 0.18);';
  if (pct < 85) return 'background: rgba(218, 109, 40, 0.22);';
  return 'background: rgba(248, 81, 73, 0.25);';
}

function nodeCardStyle(node) {
  const mode = getColorMode();
  if (!mode.startsWith('node-')) return '';

  if (mode === 'node-status') {
    if (node.status !== 'Ready') return 'background: rgba(248, 81, 73, 0.25);';
    return 'background: rgba(63, 185, 80, 0.18);';
  }

  const util = getNodeUtilization(node.name);
  const cpuCap = parseInt(node.cpuCapacity) * 1000;
  const memMatch = node.memoryCapacity.match(/([\d.]+)Gi/);
  const memCap = memMatch ? parseFloat(memMatch[1]) * 1024 * 1024 * 1024 : 0;

  let pct = 0;
  if (mode === 'node-cpu' && cpuCap > 0) pct = (util.cpuMilli / cpuCap) * 100;
  if (mode === 'node-mem' && memCap > 0) pct = (util.memBytes / memCap) * 100;

  return resourceBgColor(pct);
}

function getNodeCPUMilli(nodeName) {
  const node = allNodes.find(n => n.name === nodeName);
  if (!node) return 0;
  return parseInt(node.cpuCapacity) * 1000;
}

function getNodeMemBytes(nodeName) {
  const node = allNodes.find(n => n.name === nodeName);
  if (!node) return 0;
  // memoryCapacity is like "125.4Gi"
  const m = node.memoryCapacity.match(/([\d.]+)Gi/);
  if (m) return parseFloat(m[1]) * 1024 * 1024 * 1024;
  return 0;
}

function fmtCPU(milli) {
  const v = milli / 1000;
  if (v >= 10) return Math.round(v) + ' CPU';
  if (v >= 1) return v.toFixed(1) + ' CPU';
  return v.toFixed(2) + ' CPU';
}

function fmtMem(bytes) {
  if (bytes <= 0) return '0';
  const gb = bytes / (1024 * 1024 * 1024);
  if (gb >= 1) return gb % 1 === 0 ? gb + ' GB' : gb.toFixed(1) + ' GB';
  const mb = bytes / (1024 * 1024);
  if (mb >= 1) return Math.round(mb) + ' MB';
  return Math.round(bytes / 1024) + ' KB';
}

function resourceColor(pct) {
  if (pct < 10) return 'background: #238636;';
  if (pct < 30) return 'background: #3fb950;';
  if (pct < 60) return 'background: #d29922;';
  if (pct < 85) return 'background: #da6d28;';
  return 'background: #f85149;';
}

function statusFontColor(status, skipGreen) {
  const s = status.toLowerCase().replace(/[^a-z]/g, '');
  if (s === 'running' || s === 'ready' || s === 'succeeded') return skipGreen ? null : '#3fb950';
  if (s === 'pending' || s === 'containercreating' || s === 'terminating') return '#d29922';
  if (s === 'completed') return '#8b949e';
  if (s === 'failed' || s === 'error' || s === 'crashloopbackoff' || s === 'imagepullbackoff' || s === 'errimagepull') return '#f85149';
  return null;
}

function resourceFontColor(pct, skipGreen) {
  if (pct < 30) return skipGreen ? null : '#3fb950';
  if (pct < 60) return '#d29922';
  if (pct < 85) return '#da6d28';
  return '#f85149';
}

function dotStyle(p) {
  const mode = getColorMode();
  if (mode === 'pod-status') return '';
  // Node color modes → pods in grayscale
  if (mode.startsWith('node-')) return 'background: #484f58; opacity: 0.5;';
  const key = p.namespace + '/' + p.name;
  const m = allMetrics[key];
  if (!m) return 'background: #21262d;';

  if (mode === 'pod-cpu') {
    const limit = p.cpuLimitMilli || 0;
    if (limit > 0) return resourceColor((m.cpuMilli / limit) * 100);
    const nodeCPU = getNodeCPUMilli(p.node);
    if (nodeCPU > 0) return resourceColor((m.cpuMilli / nodeCPU) * 100);
    return 'background: #21262d;';
  }

  if (mode === 'pod-mem') {
    const limit = p.memLimitBytes || 0;
    if (limit > 0) return resourceColor((m.memBytes / limit) * 100);
    const nodeMem = getNodeMemBytes(p.node);
    if (nodeMem > 0) return resourceColor((m.memBytes / nodeMem) * 100);
    return 'background: #21262d;';
  }

  return '';
}

function renderTopologyByNodes(filtered, filter) {
  // Group pods by node
  const podsByNode = new Map();
  for (const p of filtered) {
    if (!p.node) continue;
    if (!podsByNode.has(p.node)) podsByNode.set(p.node, []);
    podsByNode.get(p.node).push(p);
  }

  // Group nodes by pool
  const pools = new Map();
  for (const n of allNodes) {
    const pool = getTopoGroupKey(n);
    if (!pools.has(pool)) pools.set(pool, []);
    pools.get(pool).push(n);
  }

  const sortedPools = [...pools.entries()].sort((a, b) => a[0].localeCompare(b[0]));

  let html = '';
  for (const [pool, nodes] of sortedPools) {
    const totalPods = nodes.reduce((sum, n) => sum + (podsByNode.get(n.name) || []).length, 0);
    if (filter && totalPods === 0) continue;

    const poolCollapsed = collapsedPools.has(pool);
    html += `<div class="topo-pool">`;
    html += `<div class="topo-pool-header" data-pool="${esc(pool)}"><span class="pool-toggle ${poolCollapsed ? '' : 'open'}">&#8250;</span>${esc(pool)} <span class="pool-count">${nodes.length} nodes / ${totalPods} pods</span></div>`;
    html += `<div class="topo-machines topo-pool-content ${poolCollapsed ? 'pool-collapsed' : ''}">`;

    for (const n of nodes) {
      const pods = podsByNode.get(n.name) || [];
      if (filter && pods.length === 0) continue;
      html += `<div class="topo-machine" data-node="${esc(n.name)}" style="${nodeCardStyle(n)}">`;
      html += `<div class="topo-machine-header">`;
      html += `<span class="topo-machine-name" title="${esc(n.name)}">${esc(n.name)}</span>`;
      html += `<span class="topo-machine-stats">${pods.length}</span>`;
      html += `</div>`;
      html += `<div class="topo-machine-resources">${esc(n.cpuCapacity)} CPU &middot; ${esc(n.memoryCapacity)}</div>`;
      html += `<div class="topo-pods">`;
      for (const p of pods) {
        html += `<div class="${dotClass(p.status, p.ready)}${dotAnim(p)}" style="${dotStyle(p)}" data-pod-b64="${btoa(JSON.stringify(p))}"></div>`;
      }
      html += `</div>`;
      html += `</div>`;
    }

    html += `</div></div>`;
  }
  return html;
}

function renderTopologyByPods(filtered, filter, mode) {
  // Group pods into cards
  const cards = new Map();
  for (const p of filtered) {
    let key;
    if (mode === 'workload-by-kind' || mode === 'workload-flat') {
      key = p.namespace + '/' + (p.workloadName || p.name);
    } else if (mode === 'kind') {
      key = p.workloadKind || 'Pod';
    } else {
      key = p.namespace || 'default';
    }
    if (!cards.has(key)) cards.set(key, []);
    cards.get(key).push(p);
  }

  // Sort by pod count descending
  const sorted = [...cards.entries()].sort((a, b) => b[1].length - a[1].length);

  // Detect workload names that appear in multiple namespaces
  const nameCount = new Map();
  for (const [, pods] of sorted) {
    const n = pods[0] ? (pods[0].workloadName || pods[0].name) : '';
    nameCount.set(n, (nameCount.get(n) || 0) + 1);
  }
  const duplicateNames = new Set([...nameCount.entries()].filter(([, c]) => c > 1).map(([n]) => n));

  // Shared layout helpers
  const cardWidth = 180;
  const cardPadding = 16;
  const innerWidth = cardWidth - cardPadding;
  const dotSize = 13;
  const dotsPerRow = Math.floor(innerWidth / dotSize);
  const headerHeight = 52;
  const gap = 10;

  function estimateHeight(podCount) {
    const rows = Math.ceil(podCount / dotsPerRow);
    return headerHeight + rows * dotSize + cardPadding;
  }

  function renderCard(cardKey, pods, showKind) {
    const name = pods[0] ? (pods[0].workloadName || pods[0].name) : cardKey;
    const running = pods.filter(p => p.status === 'Running').length;
    const notRunning = pods.length - running;
    const kindLabel = showKind ? (pods[0].workloadKind || '') : '';
    // Look up rollout status
    const ns = pods[0] ? pods[0].namespace : 'default';
    const kind = pods[0] ? (pods[0].workloadKind || 'Deployment') : 'Deployment';
    const wsKey = kind + '/' + ns + '/' + name;
    const ws = allWorkloadStatuses[wsKey];
    const isRolling = ws && ws.rolloutStatus === 'progressing';
    const allPodsReady = pods.length > 0 && pods.every(p => p.status === 'Running' && p.ready && p.ready.split('/')[0] === p.ready.split('/')[1]);
    const isDegraded = ws && ws.rolloutStatus === 'degraded' && !allPodsReady;
    const cardClass = isRolling ? ' rolling-out' : isDegraded ? ' degraded' : (notRunning > 0 ? ' has-errors' : '');
    const wlData = btoa(JSON.stringify({ name, namespace: ns, kind, podCount: pods.length }));
    let h = '';
    h += `<div class="topo-machine${cardClass}" data-workload="${wlData}">`;
    h += `<div class="topo-machine-header">`;
    h += `<div class="topo-machine-name-wrap"><span class="topo-machine-name" title="${esc(name)}">${esc(name)}</span>`;
    if (duplicateNames.has(name)) {
      h += `<span class="topo-machine-ns">${esc(ns)}</span>`;
    }
    h += `</div>`;
    if (isRolling) {
      h += `<span class="rollout-badge">${ws.updatedReplicas}/${ws.replicas}</span>`;
    }
    h += `<span class="topo-machine-stats">${pods.length}</span>`;
    h += `</div>`;
    if (kindLabel) {
      h += `<div class="topo-machine-resources">${esc(kindLabel)}</div>`;
    }
    h += `<div class="topo-pods">`;
    for (const p of pods) {
      h += `<div class="${dotClass(p.status, p.ready)}${dotAnim(p)}" style="${dotStyle(p)}" data-pod-b64="${btoa(JSON.stringify(p))}"></div>`;
    }
    h += `</div>`;
    if (isRolling && ws.replicas > 0) {
      const pct = Math.round((ws.updatedReplicas / ws.replicas) * 100);
      h += `<div class="rollout-info">Rolling out · ${pct}%</div>`;
      h += `<div class="rollout-progress"><div class="rollout-progress-fill" style="width:${pct}%"></div></div>`;
    }
    h += `</div>`;
    return h;
  }

  // Masonry layout: distributes cards into columns with ceiling-based packing
  function masonryLayout(items, showKind) {
    const containerWidth = topoEl.clientWidth || 1200;
    const colWidth = cardWidth + 10;
    const numCols = Math.max(1, Math.floor(containerWidth / colWidth));
    const columns = Array.from({length: numCols}, () => ({html: '', height: 0}));

    let cardIdx = 0;

    // First row: fill left to right
    for (let c = 0; c < numCols && cardIdx < items.length; c++, cardIdx++) {
      const [name, pods] = items[cardIdx];
      columns[c].html += renderCard(name, pods, showKind);
      columns[c].height += estimateHeight(pods.length) + gap;
    }

    // Remaining: sweep left→right, place where it fits under ceiling
    while (cardIdx < items.length) {
      const ceiling = Math.max(...columns.map(c => c.height));
      let placedAny = false;

      for (let c = 0; c < numCols && cardIdx < items.length; c++) {
        const [name, pods] = items[cardIdx];
        const cardH = estimateHeight(pods.length) + gap;
        if (columns[c].height + cardH <= ceiling + gap) {
          columns[c].html += renderCard(name, pods, showKind);
          columns[c].height += cardH;
          cardIdx++;
          placedAny = true;
        }
      }

      if (!placedAny) {
        const [name, pods] = items[cardIdx];
        let minIdx = 0;
        for (let c = 1; c < numCols; c++) {
          if (columns[c].height < columns[minIdx].height) minIdx = c;
        }
        columns[minIdx].html += renderCard(name, pods, showKind);
        columns[minIdx].height += estimateHeight(pods.length) + gap;
        cardIdx++;
      }
    }

    let h = `<div class="topo-masonry">`;
    for (const col of columns) {
      h += `<div class="topo-masonry-col">${col.html}</div>`;
    }
    h += `</div>`;
    return h;
  }

  let html = '';

  if (mode === 'workload-by-kind') {
    // Group cards by workload kind, each kind is a section
    const byKind = new Map();
    for (const [name, pods] of sorted) {
      const kind = pods[0].workloadKind || 'Pod';
      if (!byKind.has(kind)) byKind.set(kind, []);
      byKind.get(kind).push([name, pods]);
    }
    const kindOrder = ['Deployment', 'StatefulSet', 'DaemonSet', 'CronJob', 'Job', 'Pod'];
    const sortedKinds = [...byKind.entries()].sort((a, b) => {
      const ai = kindOrder.indexOf(a[0]), bi = kindOrder.indexOf(b[0]);
      return (ai === -1 ? 99 : ai) - (bi === -1 ? 99 : bi);
    });

    for (const [kind, workloads] of sortedKinds) {
      const totalPods = workloads.reduce((sum, w) => sum + w[1].length, 0);
      const kindKey = 'wl-' + kind;
      const kindCollapsed = collapsedPools.has(kindKey);
      html += `<div class="topo-pool">`;
      html += `<div class="topo-pool-header" data-pool="${esc(kindKey)}"><span class="pool-toggle ${kindCollapsed ? '' : 'open'}">&#8250;</span>${esc(kind)}s <span class="pool-count">${workloads.length} workloads / ${totalPods} pods</span></div>`;
      html += `<div class="topo-pool-content ${kindCollapsed ? 'pool-collapsed' : ''}">`;
      html += masonryLayout(workloads, false);
      html += `</div></div>`;
    }
  } else {
    const totalPods = filtered.length;
    const label = mode === 'workload-flat' ? 'workloads' : mode === 'kind' ? 'kinds' : 'namespaces';
    const flatKey = 'wl-flat';
    const flatCollapsed = collapsedPools.has(flatKey);
    html += `<div class="topo-pool">`;
    html += `<div class="topo-pool-header" data-pool="${esc(flatKey)}"><span class="pool-toggle ${flatCollapsed ? '' : 'open'}">&#8250;</span>${esc(label)} <span class="pool-count">${sorted.length} ${label} / ${totalPods} pods</span></div>`;
    html += `<div class="topo-pool-content ${flatCollapsed ? 'pool-collapsed' : ''}">`;
    html += masonryLayout(sorted, false);
    html += `</div>`;
  }
  html += `</div></div>`;

  return html;
}

function renderNodes() {
  const filtered = getFilteredPods();
  const filter = podSearch.value.toLowerCase();
  const html = renderTopologyByNodes(filtered, filter);
  topoEl.innerHTML = html;
  attachTopoDotListeners(topoEl);
  attachPoolToggleListeners(topoEl);
}

function renderWorkloads() {
  const filtered = getFilteredPods();
  const filter = podSearch.value.toLowerCase();
  const wlGroup = document.getElementById('workload-group').value;
  const modeMap = {'kind': 'workload-by-kind', 'flat': 'workload-flat', 'by-kind': 'kind'};

  const hasCards = workloadsEl.querySelector('.topo-machine:not(.skeleton-card)') !== null;
  if (workloadsNeedFullLayout || !hasCards) {
    // Full layout: recompute masonry from scratch
    const html = renderTopologyByPods(filtered, filter, modeMap[wlGroup] || 'workload-by-kind');
    workloadsEl.innerHTML = html;
    attachTopoDotListeners(workloadsEl);
    attachPoolToggleListeners(workloadsEl);
    attachWorkloadCardListeners(workloadsEl);
    workloadsNeedFullLayout = false;
  } else {
    // Incremental: update card contents in place
    updateWorkloadCardsInPlace(filtered, modeMap[wlGroup] || 'workload-by-kind');
  }
}

function updateWorkloadCardsInPlace(filtered, mode) {
  // Group pods by the same key used in renderTopologyByPods
  const cards = new Map();
  for (const p of filtered) {
    let key;
    if (mode === 'workload-by-kind' || mode === 'workload-flat') {
      key = p.namespace + '/' + (p.workloadName || p.name);
    } else if (mode === 'kind') {
      key = p.workloadKind || 'Pod';
    } else {
      key = p.namespace || 'default';
    }
    if (!cards.has(key)) cards.set(key, []);
    cards.get(key).push(p);
  }

  // Find all card elements and update their contents
  workloadsEl.querySelectorAll('.topo-machine').forEach(cardEl => {
    if (!cardEl.dataset.workload) return;
    const wlData = JSON.parse(atob(cardEl.dataset.workload));
    const cardKey = wlData.namespace + '/' + wlData.name;
    const pods = cards.get(cardKey);
    if (!pods) return;
    const name = wlData.name;

    // Update pod count
    const statsEl = cardEl.querySelector('.topo-machine-stats');
    if (statsEl) statsEl.textContent = pods.length;

    // Update resources line
    const running = pods.filter(p => p.status === 'Running').length;
    const notRunning = pods.length - running;
    const resEl = cardEl.querySelector('.topo-machine-resources');
    const kindLabel = pods[0] ? (pods[0].workloadKind || '') : '';

    // Check rollout status
    const ns = wlData.namespace;
    const kind = wlData.kind;
    const wsKey = kind + '/' + ns + '/' + name;
    const ws = allWorkloadStatuses[wsKey];
    const isRolling = ws && ws.rolloutStatus === 'progressing';
    const allPodsReady = pods.length > 0 && pods.every(p => p.status === 'Running' && p.ready && p.ready.split('/')[0] === p.ready.split('/')[1]);
    const isDegraded = ws && ws.rolloutStatus === 'degraded' && !allPodsReady;

    if (resEl) {
      resEl.textContent = kindLabel || '';
    }

    // Update/add rollout info text
    let infoEl = cardEl.querySelector('.rollout-info');
    if (isRolling && ws.replicas > 0) {
      if (!infoEl) {
        infoEl = document.createElement('div');
        infoEl.className = 'rollout-info';
        const dotsEl2 = cardEl.querySelector('.topo-pods');
        if (dotsEl2) dotsEl2.after(infoEl);
      }
      const pctText = Math.round((ws.updatedReplicas / ws.replicas) * 100);
      infoEl.textContent = 'Rolling out \u00b7 ' + pctText + '%';
    } else if (infoEl) {
      infoEl.remove();
    }

    // Update card class
    cardEl.className = 'topo-machine' + (isRolling ? ' rolling-out' : isDegraded ? ' degraded' : (notRunning > 0 ? ' has-errors' : ''));

    // Lock height during rollout so card doesn't bounce
    if (isRolling) {
      const currentHeight = cardEl.offsetHeight;
      const currentMin = parseInt(cardEl.style.minHeight) || 0;
      if (currentHeight > currentMin) {
        cardEl.style.minHeight = currentHeight + 'px';
      }
    } else {
      cardEl.style.minHeight = '';
    }

    // Update/add rollout badge
    let badge = cardEl.querySelector('.rollout-badge');
    if (isRolling) {
      if (!badge) {
        badge = document.createElement('span');
        badge.className = 'rollout-badge';
        const header = cardEl.querySelector('.topo-machine-header');
        if (header && statsEl) header.insertBefore(badge, statsEl);
      }
      badge.className = 'rollout-badge';
      badge.textContent = ws.updatedReplicas + '/' + ws.replicas;
    } else if (badge) {
      badge.remove();
    }

    // Update/add rollout progress bar
    let progressEl = cardEl.querySelector('.rollout-progress');
    if (isRolling && ws.replicas > 0) {
      const pct = Math.round((ws.updatedReplicas / ws.replicas) * 100);
      if (!progressEl) {
        progressEl = document.createElement('div');
        progressEl.className = 'rollout-progress';
        progressEl.innerHTML = `<div class="rollout-progress-fill" style="width:${pct}%"></div>`;
        cardEl.appendChild(progressEl);
      } else {
        progressEl.querySelector('.rollout-progress-fill').style.width = pct + '%';
      }
    } else if (progressEl) {
      progressEl.remove();
    }

    // Update pod dots
    const dotsEl = cardEl.querySelector('.topo-pods');
    if (dotsEl) {
      let dotsHtml = '';
      for (const p of pods) {
        dotsHtml += `<div class="${dotClass(p.status, p.ready)}${dotAnim(p)}" style="${dotStyle(p)}" data-pod-b64="${btoa(JSON.stringify(p))}"></div>`;
      }
      dotsEl.innerHTML = dotsHtml;
      // Re-attach listeners for new dots
      dotsEl.querySelectorAll('.topo-dot').forEach(el => {
        el.addEventListener('mouseenter', (e) => {
          const p = JSON.parse(atob(el.dataset.podB64));
          showDotTooltip(p, e);
        });
        el.addEventListener('mousemove', positionTooltip);
        el.addEventListener('mouseleave', () => tooltipEl.classList.add('hidden'));
        el.addEventListener('click', () => {
          const p = JSON.parse(atob(el.dataset.podB64));
          tooltipEl.classList.add('hidden');
          openDetail(p);
        });
      });
    }
  });
}

function showDotTooltip(p, e) {
  const tag = (p.containers && p.containers.length > 0) ? p.containers[0].tag : '';
  tooltipEl.innerHTML =
    `<div class="tooltip-row"><span class="tooltip-label">Pod</span> <span class="tooltip-value">${esc(p.name)}</span></div>` +
    `<div class="tooltip-row"><span class="tooltip-label">Namespace</span> <span class="tooltip-value">${esc(p.namespace)}</span></div>` +
    `<div class="tooltip-row"><span class="tooltip-label">Status</span> <span class="tooltip-value ${getColorMode() === 'pod-status' ? statusClass(p.status) : ''}">${esc(p.status)}</span></div>` +
    `<div class="tooltip-row"><span class="tooltip-label">Ready</span> <span class="tooltip-value">${esc(p.ready)}</span></div>` +
    (p.restarts > 0 ? `<div class="tooltip-row"><span class="tooltip-label">Restarts</span> <span class="tooltip-value">${p.restarts}</span></div>` : '') +
    `<div class="tooltip-row"><span class="tooltip-label">Age</span> <span class="tooltip-value">${esc(p.age)}</span></div>` +
    (tag ? `<div class="tooltip-row"><span class="tooltip-label">Image</span> <span class="tooltip-value">${esc(tag)}</span></div>` : '') +
    (p.workloadName ? `<div class="tooltip-row"><span class="tooltip-label">${esc(p.workloadKind)}</span> <span class="tooltip-value">${esc(p.workloadName)}</span></div>` : '');
  const metricKey = p.namespace + '/' + p.name;
  const metric = allMetrics[metricKey];
  if (metric) {
    const cpuLimit = p.cpuLimitMilli || 0;
    const memLimit = p.memLimitBytes || 0;
    let cpuText = fmtCPU(metric.cpuMilli);
    if (cpuLimit > 0) {
      cpuText += ` / ${fmtCPU(cpuLimit)} (${Math.round((metric.cpuMilli / cpuLimit) * 100)}%)`;
    } else {
      const nodeCPU = getNodeCPUMilli(p.node);
      if (nodeCPU > 0) cpuText += ` / ${fmtCPU(nodeCPU)} node (${Math.round((metric.cpuMilli / nodeCPU) * 100)}%)`;
    }
    let memText = fmtMem(metric.memBytes);
    if (memLimit > 0) {
      memText += ` / ${fmtMem(memLimit)} limit (${Math.round((metric.memBytes / memLimit) * 100)}%)`;
    } else {
      const nodeMem = getNodeMemBytes(p.node);
      if (nodeMem > 0) memText += ` / ${fmtMem(nodeMem)} node (${Math.round((metric.memBytes / nodeMem) * 100)}%)`;
    }
    const colorMode = getColorMode();
    const cpuPctVal = cpuLimit > 0 ? (metric.cpuMilli / cpuLimit) * 100 : (getNodeCPUMilli(p.node) > 0 ? (metric.cpuMilli / getNodeCPUMilli(p.node)) * 100 : 0);
    const memPctVal = memLimit > 0 ? (metric.memBytes / memLimit) * 100 : (getNodeMemBytes(p.node) > 0 ? (metric.memBytes / getNodeMemBytes(p.node)) * 100 : 0);
    const cpuColor = colorMode === 'pod-cpu' && cpuPctVal > 0 ? ` style="color:${resourceFontColor(cpuPctVal)}"` : '';
    const memColor = colorMode === 'pod-mem' && memPctVal > 0 ? ` style="color:${resourceFontColor(memPctVal)}"` : '';
    tooltipEl.innerHTML += `<div class="tooltip-row"><span class="tooltip-label">CPU</span> <span class="tooltip-value"${cpuColor}>${cpuText}</span></div>`;
    tooltipEl.innerHTML += `<div class="tooltip-row"><span class="tooltip-label">Memory</span> <span class="tooltip-value"${memColor}>${memText}</span></div>`;
  }
  tooltipEl.classList.remove('hidden');
  positionTooltip(e);
}

let tooltipTimer = null;

function attachPoolToggleListeners(container) {
  container.querySelectorAll('.topo-pool-header[data-pool]').forEach(el => {
    el.style.cursor = 'pointer';
    el.addEventListener('click', () => {
      const pool = el.dataset.pool;
      if (collapsedPools.has(pool)) {
        collapsedPools.delete(pool);
      } else {
        collapsedPools.add(pool);
      }
      // Toggle the content visibility
      const content = el.nextElementSibling;
      if (content) content.classList.toggle('pool-collapsed');
      const toggle = el.querySelector('.pool-toggle');
      if (toggle) toggle.classList.toggle('open');
    });
  });
}

function attachWorkloadCardListeners(container) {
  container.querySelectorAll('.topo-machine[data-workload]').forEach(card => {
    card.addEventListener('contextmenu', (e) => {
      if (!appSettings.allowActions) return;
      e.preventDefault();
      const wl = JSON.parse(atob(card.dataset.workload));
      showWorkloadContextMenu(wl, e.clientX, e.clientY);
    });
  });
}

let workloadContextMenu = null;

function showWorkloadContextMenu(wl, x, y) {
  closeWorkloadContextMenu();
  const menu = document.createElement('div');
  menu.className = 'pod-actions-menu';
  menu.style.left = x + 'px';
  menu.style.top = y + 'px';

  const items = [
    { label: `Restart ${wl.kind}`, action: () => confirmRestartWorkload({ namespace: wl.namespace, workloadName: wl.name, workloadKind: wl.kind }) },
    { label: `Scale ${wl.kind}`, action: () => promptScaleWorkload(wl) },
  ];

  for (const item of items) {
    const el = document.createElement('div');
    el.className = 'pod-actions-item' + (item.danger ? ' danger' : '');
    el.textContent = item.label;
    el.addEventListener('click', () => { closeWorkloadContextMenu(); item.action(); });
    menu.appendChild(el);
  }

  document.body.appendChild(menu);
  workloadContextMenu = menu;
  setTimeout(() => document.addEventListener('click', closeWorkloadContextMenu, { once: true }), 0);
}

function closeWorkloadContextMenu() {
  if (workloadContextMenu) { workloadContextMenu.remove(); workloadContextMenu = null; }
}

async function promptScaleWorkload(wl) {
  const input = prompt(`Scale ${wl.kind} ${wl.name} (currently ${wl.podCount} pods).\nNew replica count:`, wl.podCount);
  if (input === null) return;
  const replicas = parseInt(input);
  if (isNaN(replicas) || replicas < 0) { showToast('Invalid replica count'); return; }
  try {
    const res = await fetch(apiURL('/api/actions/scale-workload'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ namespace: wl.namespace, workloadName: wl.name, workloadKind: wl.kind, replicas }),
    });
    const data = await res.json();
    if (data.error) {
      showToast('Error: ' + data.error);
    } else {
      showToast(wl.kind + ' ' + wl.name + ' scaled to ' + replicas);
    }
  } catch (e) {
    showToast('Error: ' + e.message);
  }
}

function attachTopoDotListeners(container) {
  container.querySelectorAll('.topo-dot').forEach(el => {
    el.addEventListener('mouseenter', (e) => {
      clearTimeout(tooltipTimer);
      tooltipTimer = setTimeout(() => showDotTooltip(JSON.parse(atob(el.dataset.podB64)), e), 120);
    });
    el.addEventListener('mousemove', positionTooltip);
    el.addEventListener('mouseleave', () => { clearTimeout(tooltipTimer); tooltipEl.classList.add('hidden'); });
    el.addEventListener('click', () => { clearTimeout(tooltipTimer); tooltipEl.classList.add('hidden'); openDetail(JSON.parse(atob(el.dataset.podB64))); });
    el.addEventListener('contextmenu', (e) => { clearTimeout(tooltipTimer); tooltipEl.classList.add('hidden'); showPodContextMenu(JSON.parse(atob(el.dataset.podB64)), e); });
  });
}

function positionTooltip(e) {
  const gap = 12;
  const rect = tooltipEl.getBoundingClientRect();
  let x = e.clientX + gap;
  let y = e.clientY + gap;
  if (x + rect.width > window.innerWidth) x = e.clientX - gap - rect.width;
  if (y + rect.height > window.innerHeight) y = e.clientY - gap - rect.height;
  tooltipEl.style.left = x + 'px';
  tooltipEl.style.top = y + 'px';
}

// --- Tab switching ---

function updateExpandToggle() {
  const isGrouped = activeTab === 'pods' && groupSelect.value !== 'flat';
  document.getElementById('toggle-expand').classList.toggle('hidden-ctrl', !isGrouped);
}

function switchTab(tab) {
  activeTab = tab;
  workloadsNeedFullLayout = true;
  document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
  topoEl.classList.toggle('hidden', tab !== 'nodes');
  workloadsEl.classList.toggle('hidden', tab !== 'workloads');
  tree.classList.toggle('hidden', tab !== 'pods');
  // Show/hide tab-specific controls
  document.querySelectorAll('.nodes-only').forEach(el => el.classList.toggle('hidden-ctrl', tab !== 'nodes'));
  document.querySelectorAll('.workloads-only').forEach(el => el.classList.toggle('hidden-ctrl', tab !== 'workloads'));
  document.querySelectorAll('.pods-only').forEach(el => el.classList.toggle('hidden-ctrl', tab !== 'pods'));
  // Color mode is shared between nodes and workloads
  document.querySelectorAll('.topo-only').forEach(el => el.classList.toggle('hidden-ctrl', tab !== 'nodes' && tab !== 'workloads'));
  updateExpandToggle();
  render();
  saveSessionState();
}

function navigateToNode(nodeName) {
  switchTab('nodes');
  // Allow DOM to render, then find and scroll to the node card
  requestAnimationFrame(() => {
    const card = topoEl.querySelector(`.topo-machine[data-node="${CSS.escape(nodeName)}"]`);
    if (!card) return;
    // Expand the pool if collapsed
    const pool = card.closest('.topo-pool');
    if (pool) {
      const content = pool.querySelector('.topo-pool-content');
      if (content && content.classList.contains('pool-collapsed')) {
        const header = pool.querySelector('.topo-pool-header');
        if (header) header.click();
      }
    }
    card.scrollIntoView({ behavior: 'smooth', block: 'center' });
    card.classList.add('node-highlight');
    setTimeout(() => card.classList.remove('node-highlight'), 2500);
  });
}

function navigateToWorkload(ns, name) {
  switchTab('workloads');
  requestAnimationFrame(() => {
    // Find the card whose data-workload matches namespace + name
    const cards = workloadsEl.querySelectorAll('.topo-machine[data-workload]');
    for (const card of cards) {
      try {
        const d = JSON.parse(atob(card.dataset.workload));
        if (d.name === name && d.namespace === ns) {
          card.scrollIntoView({ behavior: 'smooth', block: 'center' });
          card.classList.add('node-highlight');
          setTimeout(() => card.classList.remove('node-highlight'), 2500);
          return;
        }
      } catch (e) {}
    }
  });
}

function render() {
  if (activeTab === 'nodes') renderNodes();
  else if (activeTab === 'workloads') renderWorkloads();
  else renderTree();
}

// --- Event listeners ---

function attachListeners() {
  tree.querySelectorAll('.group-header').forEach(el => {
    el.addEventListener('click', () => {
      const key = el.dataset.group;
      expanded.set(key, !expanded.get(key));
      renderTree();
    });
  });
  tree.querySelectorAll('.pod-row').forEach(el => {
    el.addEventListener('click', (e) => {
      e.stopPropagation();
      if (el.dataset.podB64) {
        openDetail(JSON.parse(atob(el.dataset.podB64)));
      }
    });
  });
  // "+N" container dots expander
  tree.querySelectorAll('.containers-more').forEach(el => {
    el.addEventListener('click', (e) => {
      e.stopPropagation();
      const id = el.dataset.expandDots;
      if (expandedContainers.has(id)) expandedContainers.delete(id);
      else expandedContainers.add(id);
      renderTree();
    });
  });
  // Column header sort
  tree.querySelectorAll('.col-header[data-sort-key]').forEach(el => {
    el.addEventListener('click', (e) => {
      if (e.target.classList.contains('col-resize')) return; // don't sort on resize drag
      const key = el.dataset.sortKey;
      if (sortCol === key) {
        sortAsc = !sortAsc;
      } else {
        sortCol = key;
        sortAsc = true;
      }
      renderTree();
    });
  });
  initColumnResize();
}

document.querySelectorAll('.tab').forEach(t => {
  t.addEventListener('click', () => switchTab(t.dataset.tab));
});
document.getElementById('detail-close').addEventListener('click', closeDetail);

// Detail panel resize
(function() {
  const handle = document.getElementById('detail-resize-handle');
  let dragging = false;
  let startX, startWidth;

  handle.addEventListener('mousedown', (e) => {
    e.preventDefault();
    dragging = true;
    startX = e.clientX;
    startWidth = detailPanel.offsetWidth;
    handle.classList.add('active');
    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
  });

  document.addEventListener('mousemove', (e) => {
    if (!dragging) return;
    const delta = startX - e.clientX;
    const newWidth = Math.max(300, Math.min(startWidth + delta, window.innerWidth * 0.8));
    detailPanel.style.width = newWidth + 'px';
  });

  document.addEventListener('mouseup', () => {
    if (!dragging) return;
    dragging = false;
    handle.classList.remove('active');
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  });
})();
document.getElementById('detail-actions-btn').addEventListener('click', (e) => {
  e.stopPropagation();
  showPodActionsMenu(e);
});
document.getElementById('toggle-expand').addEventListener('click', () => {
  const btn = document.getElementById('toggle-expand');
  const allExpanded = [...expanded.values()].every(v => v);
  for (const key of expanded.keys()) expanded.set(key, !allExpanded);
  btn.textContent = allExpanded ? 'Expand all' : 'Collapse all';
  renderTree();
});
// --- Custom picker menus ---
let activePickerMenu = null;

function closePickerMenu() {
  if (activePickerMenu) { activePickerMenu.remove(); activePickerMenu = null; }
}

function openPickerMenu(pickerEl) {
  closePickerMenu();
  const selectId = pickerEl.dataset.target;
  const select = document.getElementById(selectId);
  if (!select) return;

  const menu = document.createElement('div');
  menu.className = 'picker-menu';
  const rect = pickerEl.getBoundingClientRect();
  menu.style.left = rect.left + 'px';
  menu.style.top = (rect.bottom + 4) + 'px';

  for (const opt of select.options) {
    const item = document.createElement('div');
    const isLabelOpt = (selectId === 'topo-group' && opt.value === 'label');
    const isActiveLabel = select.value === 'label' && opt.value === 'label';
    item.className = 'picker-menu-item' + (opt.value === select.value || isActiveLabel ? ' active' : '');

    if (isLabelOpt) {
      // Label option with submenu
      item.innerHTML = opt.textContent + ' <span class="submenu-arrow">&#8250;</span>';
      item.style.position = 'relative';
      item.addEventListener('mouseenter', () => {
        // Remove any existing submenu
        const old = menu.querySelector('.picker-submenu');
        if (old) old.remove();

        const sub = document.createElement('div');
        sub.className = 'picker-menu picker-submenu';
        sub.style.position = 'absolute';
        sub.style.left = (item.offsetWidth - 4) + 'px';
        sub.style.top = '-4px';

        // Populate with label keys
        const keys = new Set();
        for (const n of allNodes) {
          if (n.labels) for (const k of Object.keys(n.labels)) keys.add(k);
        }
        const labelSelect = document.getElementById('topo-label-select');
        const currentLabel = labelSelect ? labelSelect.value : '';

        for (const k of [...keys].sort()) {
          const subItem = document.createElement('div');
          subItem.className = 'picker-menu-item' + (k === currentLabel && select.value === 'label' ? ' active' : '');
          subItem.textContent = k;
          subItem.addEventListener('click', (e) => {
            e.stopPropagation();
            select.value = 'label';
            // Populate and set the label select
            if (labelSelect) {
              labelSelect.innerHTML = '';
              for (const key of [...keys].sort()) {
                const o = document.createElement('option');
                o.value = key; o.textContent = key;
                if (key === k) o.selected = true;
                labelSelect.appendChild(o);
              }
              labelSelect.value = k;
            }
            select.dispatchEvent(new Event('change'));
            pickerEl.textContent = 'Label: ' + k;
            closePickerMenu();
          });
          sub.appendChild(subItem);
        }
        item.appendChild(sub);
      });
      item.addEventListener('mouseleave', (e) => {
        // Only remove if not moving into submenu
        setTimeout(() => {
          const sub = item.querySelector('.picker-submenu');
          if (sub && !sub.matches(':hover') && !item.matches(':hover')) sub.remove();
        }, 100);
      });
    } else {
      item.textContent = opt.textContent;
      item.addEventListener('click', () => {
        select.value = opt.value;
        select.dispatchEvent(new Event('change'));
        pickerEl.textContent = opt.textContent;
        closePickerMenu();
      });
    }
    menu.appendChild(item);
  }

  document.body.appendChild(menu);
  activePickerMenu = menu;
  setTimeout(() => document.addEventListener('click', closePickerMenu, { once: true }), 0);
}

document.querySelectorAll('.ctrl-picker').forEach(picker => {
  picker.addEventListener('click', (e) => {
    e.stopPropagation();
    openPickerMenu(picker);
  });
});

// Sync picker text when selects change programmatically
function syncPickerText(selectId, pickerId) {
  const select = document.getElementById(selectId);
  const picker = document.getElementById(pickerId);
  if (!select || !picker) return;
  const opt = select.options[select.selectedIndex];
  if (!opt) return;
  picker.textContent = opt.textContent;
}

nsSelect.addEventListener('change', () => { workloadsNeedFullLayout = true; populateWorkloads(); render(); saveSessionState(); });
wlSelect.addEventListener('change', () => { workloadsNeedFullLayout = true; render(); saveSessionState(); });
groupSelect.addEventListener('change', () => { updateExpandToggle(); render(); saveSessionState(); });
document.getElementById('color-mode').addEventListener('change', () => { render(); saveSessionState(); });
document.getElementById('topo-group').addEventListener('change', () => {
  render();
  saveSessionState();
});
document.getElementById('topo-label-select').addEventListener('change', () => {
  syncPickerText('topo-label-select', 'topo-label-picker');
  render();
  saveSessionState();
});
document.getElementById('workload-group').addEventListener('change', () => { workloadsNeedFullLayout = true; render(); saveSessionState(); });
podSearch.addEventListener('input', () => {
  document.getElementById('pod-search-clear').classList.toggle('hidden', !podSearch.value);
  workloadsNeedFullLayout = true;
  render();
  saveSessionState();
});
document.getElementById('pod-search-clear').addEventListener('click', () => {
  podSearch.value = '';
  document.getElementById('pod-search-clear').classList.add('hidden');
  workloadsNeedFullLayout = true;
  render();
  saveSessionState();
});
document.getElementById('search-case').addEventListener('click', () => {
  searchCaseSensitive = !searchCaseSensitive;
  document.getElementById('search-case').classList.toggle('active', searchCaseSensitive);
  render();
});
document.getElementById('search-regex').addEventListener('click', () => {
  searchRegex = !searchRegex;
  document.getElementById('search-regex').classList.toggle('active', searchRegex);
  render();
});

let loading = false;

async function refresh() {
  if (loading) return;
  loading = true;
  try {
    await loadData();
  } catch (e) {
    console.error('refresh failed:', e);
  } finally {
    loading = false;
  }
}

// Initial setup: hide list-only controls

// --- Sidebar / Cluster management ---

async function loadClusters() {
  try {
    clusters = await fetchJSON('/api/clusters');
  } catch (e) {
    clusters = [];
  }
  renderSidebar();
}

let appSettings = {};

async function loadSettings() {
  try {
    appSettings = await fetchJSON('/api/settings');
  } catch (e) {
    appSettings = {};
  }
}

async function saveSetting(key, value) {
  appSettings[key] = value;
  await fetch(apiURL('/api/settings/update'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(appSettings),
  });
}

function clusterInitials(name) {
  // "arctis-prod-eu" → "AP", "my-cluster" → "MC", "staging" → "ST"
  const parts = name.replace(/[^a-zA-Z0-9]+/g, ' ').trim().split(/\s+/);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  const w = parts[0] || '';
  return (w.length >= 2 ? w[0] + w[1] : w[0] || '?').toUpperCase();
}

function clusterColor(name) {
  // Deterministic color from name
  let hash = 0;
  for (let i = 0; i < name.length; i++) hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  const hue = Math.abs(hash) % 360;
  return `hsl(${hue}, 45%, 35%)`;
}

function renderSidebar() {
  const showHidden = appSettings.showHidden || false;
  const showAllContexts = appSettings.showAllContexts || false;
  let visible = clusters;
  if (!showAllContexts) visible = visible.filter(c => c.isDefault || c.active);
  if (!showHidden) visible = visible.filter(c => !c.hidden || c.active);

  // Sort by saved order
  const order = appSettings.clusterOrder || [];
  if (order.length > 0) {
    const orderMap = {};
    order.forEach((id, i) => orderMap[id] = i);
    visible.sort((a, b) => {
      const ai = orderMap[a.id] !== undefined ? orderMap[a.id] : 9999;
      const bi = orderMap[b.id] !== undefined ? orderMap[b.id] : 9999;
      return ai - bi;
    });
  }

  let html = '';
  for (const c of visible) {
    const cls = (c.active ? ' active' : '') + (c.hidden ? ' hidden-cluster' : '') + (c.error ? ' error-cluster' : '');
    const fileName = c.filePath.split('/').pop();
    const initials = clusterInitials(c.displayName);
    const color = clusterColor(c.displayName);
    html += `<div class="cluster-item${cls}" data-cluster-id="${esc(c.id)}" draggable="true">`;
    html += `<div class="cluster-item-row">`;
    if (c.icon) {
      html += `<img class="cluster-icon cluster-icon-img" src="${c.icon}" title="${esc(c.displayName)}">`;
    } else {
      html += `<div class="cluster-icon" style="background:${color}" title="${esc(c.displayName)}">${esc(initials)}</div>`;
    }
    html += `<div class="cluster-item-text">`;
    html += `<span class="cluster-name">${esc(c.displayName)}</span>`;
    html += `<span class="cluster-file">${esc(fileName)}</span>`;
    html += `<span class="cluster-server">${esc(c.server)}</span>`;
    if (c.error) {
      html += `<span class="cluster-error" title="${esc(c.error)}">unreachable</span>`;
    }
    html += `</div>`;
    html += `<span class="cluster-menu-btn" data-menu-id="${esc(c.id)}">&#8942;</span>`;
    html += `</div>`;
    html += `</div>`;
  }
  clusterListEl.innerHTML = html;

  // Click to switch
  clusterListEl.querySelectorAll('.cluster-item').forEach(el => {
    el.addEventListener('click', (e) => {
      if (e.target.closest('.cluster-menu-btn')) return;
      switchCluster(el.dataset.clusterId);
    });
  });

  // Menu button
  clusterListEl.querySelectorAll('.cluster-menu-btn').forEach(el => {
    el.addEventListener('click', (e) => {
      e.stopPropagation();
      showClusterMenu(el.dataset.menuId, e.clientX, e.clientY);
    });
  });

  // Right-click context menu on cluster items
  clusterListEl.querySelectorAll('.cluster-item').forEach(el => {
    el.addEventListener('contextmenu', (e) => {
      e.preventDefault();
      e.stopPropagation();
      showClusterMenu(el.dataset.clusterId, e.clientX, e.clientY);
    });
  });

  // Drag and drop reordering
  let dragEl = null;
  clusterListEl.querySelectorAll('.cluster-item').forEach(el => {
    el.addEventListener('dragstart', (e) => {
      dragEl = el;
      el.classList.add('dragging');
      e.dataTransfer.effectAllowed = 'move';
    });
    el.addEventListener('dragend', () => {
      el.classList.remove('dragging');
      dragEl = null;
      clusterListEl.querySelectorAll('.cluster-item').forEach(item => item.classList.remove('drag-over'));
    });
    el.addEventListener('dragover', (e) => {
      e.preventDefault();
      e.dataTransfer.dropEffect = 'move';
      if (el !== dragEl) {
        clusterListEl.querySelectorAll('.cluster-item').forEach(item => item.classList.remove('drag-over'));
        el.classList.add('drag-over');
      }
    });
    el.addEventListener('dragleave', () => {
      el.classList.remove('drag-over');
    });
    el.addEventListener('drop', (e) => {
      e.preventDefault();
      if (!dragEl || dragEl === el) return;
      // Reorder DOM
      const items = [...clusterListEl.querySelectorAll('.cluster-item')];
      const fromIdx = items.indexOf(dragEl);
      const toIdx = items.indexOf(el);
      if (fromIdx < toIdx) {
        el.after(dragEl);
      } else {
        el.before(dragEl);
      }
      el.classList.remove('drag-over');
      // Save new order
      const newOrder = [...clusterListEl.querySelectorAll('.cluster-item')].map(item => item.dataset.clusterId);
      appSettings.clusterOrder = newOrder;
      saveSessionState();
    });
  });
}

async function switchCluster(id) {
  try {
    hideProgress();
    hideErrorBanner();
    hideSyncing();
    allNodes = [];
    allPods = [];
    allWorkloadStatuses = {};
    allMetrics = {};
    animEnabled = false;
    prevPodState.clear();
    workloadsNeedFullLayout = true;
    render();

    await fetch(apiURL('/api/clusters/switch'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    });

    await loadClusters();
    await waitForCache();
    hideSyncing();
    await loadClusters(); // reload to pick up error state
    await loadNamespaces();
    await refresh();
  } catch (e) {
    console.error('switch failed:', e);
    hideProgress();
    hideSyncing();
    await loadClusters();
  }
}

let activeMenu = null;

function showClusterMenu(id, x, y) {
  closeClusterMenu();
  const c = clusters.find(c => c.id === id);
  if (!c) return;

  const menu = document.createElement('div');
  menu.className = 'cluster-menu';
  menu.style.left = x + 'px';
  menu.style.top = y + 'px';

  const renameItem = document.createElement('div');
  renameItem.className = 'cluster-menu-item';
  renameItem.textContent = 'Rename';
  renameItem.addEventListener('click', () => {
    closeClusterMenu();
    renameCluster(id, c.displayName);
  });
  menu.appendChild(renameItem);

  const hideItem = document.createElement('div');
  hideItem.className = 'cluster-menu-item';
  hideItem.textContent = c.hidden ? 'Show' : 'Hide';
  hideItem.addEventListener('click', () => {
    closeClusterMenu();
    toggleHideCluster(id, !c.hidden);
  });
  menu.appendChild(hideItem);

  const iconItem = document.createElement('div');
  iconItem.className = 'cluster-menu-item';
  iconItem.textContent = c.icon ? 'Remove icon' : 'Set icon';
  iconItem.addEventListener('click', () => {
    closeClusterMenu();
    if (c.icon) {
      setClusterIcon(id, '');
    } else {
      pickClusterIcon(id);
    }
  });
  menu.appendChild(iconItem);

  document.body.appendChild(menu);
  activeMenu = menu;

  // Close on outside click
  setTimeout(() => {
    document.addEventListener('click', closeClusterMenu, { once: true });
  }, 0);
}

function closeClusterMenu() {
  if (activeMenu) {
    activeMenu.remove();
    activeMenu = null;
  }
}

const renameModal = document.getElementById('rename-modal');
const renameInput = document.getElementById('rename-input');
let renameTargetId = null;

function renameCluster(id, currentName) {
  renameTargetId = id;
  const c = clusters.find(cl => cl.id === id);
  renameInput.value = currentName;
  renameInput.placeholder = c ? c.contextName : 'Display name';
  renameModal.classList.remove('hidden');
  setTimeout(() => { renameInput.focus(); renameInput.select(); }, 50);
}

async function confirmRename() {
  const newName = renameInput.value.trim();
  if (!newName || !renameTargetId) return;
  renameModal.classList.add('hidden');
  await fetch(apiURL('/api/clusters/rename'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: renameTargetId, displayName: newName }),
  });
  renameTargetId = null;
  await loadClusters();
}

document.getElementById('rename-confirm').addEventListener('click', confirmRename);
document.getElementById('rename-reset').addEventListener('click', async () => {
  if (!renameTargetId) return;
  renameModal.classList.add('hidden');
  await fetch(apiURL('/api/clusters/rename'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: renameTargetId, displayName: '' }),
  });
  renameTargetId = null;
  await loadClusters();
});
document.getElementById('rename-cancel').addEventListener('click', () => renameModal.classList.add('hidden'));
document.getElementById('rename-close').addEventListener('click', () => renameModal.classList.add('hidden'));
renameModal.addEventListener('click', (e) => { if (e.target === renameModal) renameModal.classList.add('hidden'); });
renameInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') confirmRename(); if (e.key === 'Escape') renameModal.classList.add('hidden'); });

let iconTargetId = null;
let iconOriginalImg = null; // original Image element for re-filtering
let iconActiveFilter = 'none';
const iconModal = document.getElementById('icon-modal');
const iconUrlInput = document.getElementById('icon-url-input');
const iconCanvas = document.getElementById('icon-preview-canvas');
const iconPreviewSection = document.getElementById('icon-preview-section');

function pickClusterIcon(id) {
  iconTargetId = id;
  iconOriginalImg = null;
  iconActiveFilter = 'none';
  iconUrlInput.value = '';
  iconPreviewSection.classList.add('hidden');
  document.querySelectorAll('.icon-swatch').forEach(s => s.classList.toggle('active', s.dataset.filter === 'none'));
  iconModal.classList.remove('hidden');
  setTimeout(() => iconUrlInput.focus(), 50);
}

function closeIconModal() {
  iconModal.classList.add('hidden');
  iconTargetId = null;
  iconOriginalImg = null;
}

function showIconPreview(img) {
  iconOriginalImg = img;
  iconActiveFilter = 'none';
  document.querySelectorAll('.icon-swatch').forEach(s => s.classList.toggle('active', s.dataset.filter === 'none'));
  applyIconFilter('none');
  iconPreviewSection.classList.remove('hidden');
}

function applyIconFilter(filter) {
  if (!iconOriginalImg) return;
  iconActiveFilter = filter;
  const ctx = iconCanvas.getContext('2d');
  ctx.clearRect(0, 0, 64, 64);
  ctx.drawImage(iconOriginalImg, 0, 0, 64, 64);

  if (filter === 'none') return;

  const imageData = ctx.getImageData(0, 0, 64, 64);
  const d = imageData.data;
  for (let i = 0; i < d.length; i += 4) {
    if (d[i + 3] === 0) continue; // skip transparent
    const gray = 0.299 * d[i] + 0.587 * d[i + 1] + 0.114 * d[i + 2];

    if (filter === 'grayscale') {
      d[i] = d[i + 1] = d[i + 2] = gray;
    } else {
      // Tint: blend grayscale with target color
      const tints = {
        red:    [220, 60, 60],
        orange: [220, 160, 40],
        green:  [60, 170, 90],
        blue:   [60, 120, 220],
        purple: [140, 80, 210],
      };
      const t = tints[filter] || [gray, gray, gray];
      const mix = 0.6; // 60% tint, 40% original luminance
      d[i]     = Math.round(t[0] * mix + gray * (1 - mix));
      d[i + 1] = Math.round(t[1] * mix + gray * (1 - mix));
      d[i + 2] = Math.round(t[2] * mix + gray * (1 - mix));
    }
  }
  ctx.putImageData(imageData, 0, 0);
}

async function fetchAndPreview(url) {
  url = url.trim();
  if (!url) return;
  // Fetch via server to avoid CORS
  const resp = await fetch(apiURL('/api/clusters/icon'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: '__preview__', url: url }),
  });
  // Server saved it to __preview__ — not ideal. Let's use a different approach:
  // Fetch the data URL from server via a GET-like endpoint
}

// Better approach: add a /api/clusters/fetch-icon endpoint that returns the data URL
async function fetchIconDataURL(url) {
  const resp = await fetch(apiURL('/api/clusters/fetch-icon'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url: url }),
  });
  if (!resp.ok) return null;
  const data = await resp.json();
  return data.icon || null;
}

async function fetchAndShowPreview(url) {
  url = url.trim();
  if (!url) return;
  const dataURL = await fetchIconDataURL(url);
  if (!dataURL) return;
  const img = new Image();
  img.onload = () => showIconPreview(img);
  img.src = dataURL;
}

function fileToPreview(file) {
  const reader = new FileReader();
  reader.onload = () => {
    const img = new Image();
    img.onload = () => showIconPreview(img);
    img.src = reader.result;
  };
  reader.readAsDataURL(file);
}

// Source tabs
document.querySelectorAll('.icon-src-tab').forEach(tab => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.icon-src-tab').forEach(t => t.classList.remove('active'));
    tab.classList.add('active');
    document.getElementById('icon-src-url').classList.toggle('hidden', tab.dataset.src !== 'url');
    document.getElementById('icon-src-file').classList.toggle('hidden', tab.dataset.src !== 'file');
  });
});

// Fetch URL
document.getElementById('icon-url-btn').addEventListener('click', () => fetchAndShowPreview(iconUrlInput.value));
iconUrlInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') fetchAndShowPreview(iconUrlInput.value); });

// File upload
document.getElementById('icon-file-btn').addEventListener('click', () => {
  const input = document.createElement('input');
  input.type = 'file';
  input.accept = 'image/*';
  input.addEventListener('change', () => { if (input.files[0]) fileToPreview(input.files[0]); });
  input.click();
});

// Filter swatches
document.querySelectorAll('.icon-swatch').forEach(swatch => {
  swatch.addEventListener('click', () => {
    document.querySelectorAll('.icon-swatch').forEach(s => s.classList.remove('active'));
    swatch.classList.add('active');
    applyIconFilter(swatch.dataset.filter);
  });
});

// Apply
document.getElementById('icon-apply-btn').addEventListener('click', async () => {
  if (!iconTargetId) return;
  const dataURL = iconCanvas.toDataURL('image/png');
  await setClusterIcon(iconTargetId, dataURL);
  closeIconModal();
});

// Cancel / close
document.getElementById('icon-cancel-btn').addEventListener('click', closeIconModal);
document.getElementById('icon-modal-close').addEventListener('click', closeIconModal);
iconModal.addEventListener('click', (e) => { if (e.target === iconModal) closeIconModal(); });

async function setClusterIcon(id, icon) {
  await fetch(apiURL('/api/clusters/icon'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id, icon }),
  });
  await loadClusters();
}

async function toggleHideCluster(id, hidden) {
  await fetch(apiURL('/api/clusters/hide'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id, hidden }),
  });
  await loadClusters();
}

// Sidebar toggle
function applySidebarState() {
  if (appSettings.sidebarCollapsed) sidebar.classList.add('collapsed');
  else sidebar.classList.remove('collapsed');
}

function restoreSessionState() {
  if (appSettings.activeTab) {
    activeTab = appSettings.activeTab;
    switchTab(activeTab);
  }
  if (appSettings.namespace) nsSelect.value = appSettings.namespace;
  if (appSettings.workload) wlSelect.value = appSettings.workload;
  if (appSettings.podSearch) podSearch.value = appSettings.podSearch;
  if (appSettings.colorMode) document.getElementById('color-mode').value = appSettings.colorMode;
  if (appSettings.topoGroup) document.getElementById('topo-group').value = appSettings.topoGroup;
  if (appSettings.listGroup) document.getElementById('group-select').value = appSettings.listGroup;
  // Sync all picker texts
  syncPickerText('topo-group', 'topo-group-picker');
  syncPickerText('workload-group', 'workload-group-picker');
  syncPickerText('color-mode', 'color-mode-picker');
  syncPickerText('group-select', 'group-select-picker');
  // If grouped by label, show "Label: xxx" in the picker
  if (document.getElementById('topo-group').value === 'label') {
    const labelVal = document.getElementById('topo-label-select').value;
    if (labelVal) {
      document.getElementById('topo-group-picker').textContent = 'Label: ' + labelVal;
    }
  }
}

function saveSessionState() {
  appSettings.activeTab = activeTab;
  appSettings.namespace = nsSelect.value;
  appSettings.workload = wlSelect.value;
  appSettings.podSearch = podSearch.value;
  appSettings.colorMode = document.getElementById('color-mode').value;
  appSettings.topoGroup = document.getElementById('topo-group').value;
  appSettings.topoLabel = document.getElementById('topo-label-select').value;
  appSettings.listGroup = document.getElementById('group-select').value;
  // Debounced save
  clearTimeout(saveSessionState._timer);
  saveSessionState._timer = setTimeout(() => {
    fetch(apiURL('/api/settings/update'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(appSettings),
    });
  }, 500);
}

document.getElementById('sidebar-toggle').addEventListener('click', () => {
  sidebar.classList.toggle('collapsed');
  saveSetting('sidebarCollapsed', sidebar.classList.contains('collapsed'));
});

// Options popup menu
let optionsMenu = null;

function showOptionsMenu() {
  closeOptionsMenu();
  const btn = document.getElementById('sidebar-options-btn');
  const rect = btn.getBoundingClientRect();

  const menu = document.createElement('div');
  menu.className = 'options-menu';
  menu.style.left = rect.left + 'px';
  menu.style.bottom = (window.innerHeight - rect.top + 4) + 'px';

  const showHidden = appSettings.showHidden || false;
  const showAllContexts = appSettings.showAllContexts || false;
  const allowActions = appSettings.allowActions || false;

  menu.innerHTML = `
    <div class="options-menu-item" data-option="showHidden">
      <span class="options-check">${showHidden ? '&#10003;' : ''}</span>
      Show hidden clusters
    </div>
    <div class="options-menu-item" data-option="showAllContexts">
      <span class="options-check">${showAllContexts ? '&#10003;' : ''}</span>
      Show all contexts per kubeconfig
    </div>
    <div class="options-menu-sep"></div>
    <div class="options-menu-item" data-option="allowActions">
      <span class="options-check">${allowActions ? '&#10003;' : ''}</span>
      Allow actions (delete, restart, scale)
    </div>
  `;

  menu.querySelectorAll('.options-menu-item').forEach(item => {
    item.addEventListener('click', async () => {
      const key = item.dataset.option;
      await saveSetting(key, !appSettings[key]);
      renderSidebar();
      showOptionsMenu(); // re-render menu with updated checkmarks
    });
  });

  document.body.appendChild(menu);
  optionsMenu = menu;

  setTimeout(() => {
    document.addEventListener('click', closeOptionsMenu, { once: true });
  }, 0);
}

function closeOptionsMenu() {
  if (optionsMenu) {
    optionsMenu.remove();
    optionsMenu = null;
  }
}

document.getElementById('sidebar-options-btn').addEventListener('click', (e) => {
  e.stopPropagation();
  if (optionsMenu) closeOptionsMenu();
  else showOptionsMenu();
});

// Update check
// --- Changelog ---

const changelog = [
  { version: '0.5.0', items: [
    'Deep links: share pod URLs with colleagues (via vuek8.app/open)',
    'Auto-switch to the right cluster when opening a shared link',
    'Right-click context menu on pod dots',
    'ANSI color codes rendered in log viewer',
    'Custom URL scheme (vuek8://) for desktop app deep linking',
  ]},
  { version: '0.4.7', items: [
    'Real-time updates via Kubernetes Watch API (replaces polling)',
    'Resizable detail panel',
    'Live cluster discovery (new kubeconfig files appear without restart)',
    'Smarter loading: spinner instead of progress bar when cached',
    'Skeleton cards on both Nodes and Workloads tabs',
    'CronJob pods show "Succeeded" in green',
    'Active cluster styling improved',
  ]},
  { version: '0.4.5', items: [
    'Namespace-aware workload grouping (same-name workloads separated)',
    'Namespace label shown when names collide across namespaces',
    'Tooltip on truncated node/workload names',
    'StatefulSet rollout detection fix',
    'Cross-check pod readiness vs Deployment status',
    'Clickable node and workload links in pod details',
  ]},
  { version: '0.4.2', items: [
    'Distinguish degraded from progressing workloads',
    'Not-ready pods shown as yellow dots',
    'Tooltip flips when near viewport edge',
  ]},
  { version: '0.4.0', items: [
    'Rollout status detection with live progress bars',
    'Workload indicators (progressing, degraded, stable)',
    'Collapsible workload categories',
  ]},
  { version: '0.4.1', items: [
    'Pod and workload actions (delete, restart, scale)',
    'Safety toggle for destructive actions',
  ]},
  { version: '0.3.0', items: [
    'Sortable pod columns',
    'Improved age format',
  ]},
  { version: '0.2.0', items: [
    '3-tab layout: Nodes, Workloads, Pods',
    'Cluster icons with custom upload and color tints',
    'Workload topology view',
  ]},
  { version: '0.2.2', items: [
    'In-app auto-update with restart button',
  ]},
  { version: '0.1.0', items: [
    'Initial release',
    'Topology view with node pools and pod dots',
    'Multi-cluster auto-discovery',
    'Real-time pod status',
    'Native macOS desktop app',
  ]},
];

async function checkChangelog() {
  try {
    const info = await fetchJSON('/api/version');
    const currentVersion = info.current;
    if (!currentVersion || currentVersion === 'dev') return;

    const lastSeen = localStorage.getItem('vuek8-last-seen-version');
    if (lastSeen === currentVersion) return;

    // Find what's new since last seen
    const toast = document.getElementById('changelog-toast');
    const title = document.getElementById('changelog-toast-title');
    const body = document.getElementById('changelog-toast-body');
    const showAllBtn = document.getElementById('changelog-show-all');

    // Find entries newer than lastSeen
    const lastSeenIdx = lastSeen ? changelog.findIndex(c => lastSeen.startsWith(c.version)) : -1;
    // Entries to highlight: everything newer than lastSeen (or all if first run)
    const newEntries = lastSeenIdx === -1 ? [changelog[0]] : changelog.slice(0, lastSeenIdx);
    if (newEntries.length === 0) {
      localStorage.setItem('vuek8-last-seen-version', currentVersion);
      return;
    }

    title.textContent = "What's new in v" + newEntries[0].version;

    let html = '';
    for (const entry of newEntries) {
      if (newEntries.length > 1) html += `<div class="changelog-version">v${esc(entry.version)}</div>`;
      html += '<ul>';
      for (const item of entry.items) html += `<li>${esc(item)}</li>`;
      html += '</ul>';
    }
    body.innerHTML = html;

    // Show all button
    let showingAll = false;
    showAllBtn.addEventListener('click', () => {
      if (showingAll) {
        body.innerHTML = html;
        showAllBtn.textContent = 'Show full changelog';
        showingAll = false;
        return;
      }
      let allHtml = '';
      for (const entry of changelog) {
        allHtml += `<div class="changelog-version">v${esc(entry.version)}</div>`;
        allHtml += '<ul>';
        for (const item of entry.items) allHtml += `<li>${esc(item)}</li>`;
        allHtml += '</ul>';
      }
      body.innerHTML = allHtml;
      showAllBtn.textContent = 'Show less';
      showingAll = true;
    });

    toast.classList.remove('hidden');

    document.getElementById('changelog-toast-close').addEventListener('click', () => {
      toast.classList.add('hidden');
      localStorage.setItem('vuek8-last-seen-version', currentVersion);
    });

    // Auto-dismiss after 30 seconds
    setTimeout(() => {
      if (!toast.classList.contains('hidden')) {
        toast.classList.add('hidden');
        localStorage.setItem('vuek8-last-seen-version', currentVersion);
      }
    }, 30000);
  } catch (e) {
    // silently ignore
  }
}

function showChangelog() {
  const toast = document.getElementById('changelog-toast');
  const title = document.getElementById('changelog-toast-title');
  const body = document.getElementById('changelog-toast-body');

  title.textContent = "What's new in v" + changelog[0].version;

  let html = '';
  for (const entry of changelog) {
    html += `<div class="changelog-version">v${esc(entry.version)}</div>`;
    html += '<ul>';
    for (const item of entry.items) html += `<li>${esc(item)}</li>`;
    html += '</ul>';
  }
  body.innerHTML = html;
  const showAllBtn = document.getElementById('changelog-show-all');
  showAllBtn.style.display = 'none';
  toast.classList.remove('hidden');
}

async function checkForUpdate() {
  try {
    const info = await fetchJSON('/api/version');
    if (info.hasUpdate) {
      document.getElementById('update-text').textContent = `New version available: v${info.latest}`;
      document.getElementById('update-banner').classList.remove('hidden');
    }
  } catch (e) {
    // silently ignore
  }
}

document.getElementById('update-btn').addEventListener('click', async () => {
  const btn = document.getElementById('update-btn');
  const text = document.getElementById('update-text');
  btn.disabled = true;
  btn.textContent = 'Updating...';
  text.textContent = 'Downloading and installing update...';
  try {
    const resp = await fetch(apiURL('/api/self-update'), { method: 'POST' });
    if (resp.ok) {
      text.textContent = 'Restarting...';
      btn.classList.add('hidden');
      fetch(apiURL('/api/restart'), { method: 'POST' });
    } else {
      const err = await resp.text();
      text.textContent = 'Update failed: ' + err;
      btn.textContent = 'Retry';
      btn.disabled = false;
    }
  } catch (e) {
    text.textContent = 'Update failed: ' + e.message;
    btn.textContent = 'Retry';
    btn.disabled = false;
  }
});

document.getElementById('update-dismiss').addEventListener('click', () => {
  document.getElementById('update-banner').classList.add('hidden');
});

// Demo rollout keyboard shortcut
document.addEventListener('keydown', (e) => {
  if (e.key === 'r' && !e.ctrlKey && !e.metaKey && !e.altKey && document.activeElement.tagName !== 'INPUT') {
    fetch(apiURL('/api/demo/rollout/toggle'), { method: 'POST' }).catch(() => {});
    showToast('Press R to start/stop rollout');
  }
});

function showToast(msg) {
  let toast = document.getElementById('demo-toast');
  if (!toast) {
    toast = document.createElement('div');
    toast.id = 'demo-toast';
    toast.className = 'demo-toast';
    document.body.appendChild(toast);
  }
  toast.textContent = msg;
  toast.classList.add('visible');
  clearTimeout(toast._timer);
  toast._timer = setTimeout(() => toast.classList.remove('visible'), 2000);
}

// Startup
(async () => {
  // In native app, the API URL is injected into the HTML
  if (window.__KGLANCE_API_BASE__) {
    apiBase = window.__KGLANCE_API_BASE__;
  }
  await loadSettings();
  applySidebarState();
  restoreSessionState();
  checkForUpdate();
  checkChangelog();
  await loadClusters();
  await waitForCache();
  hideSyncing();
  await loadNamespaces();
  await refresh();
  setInterval(refresh, 3000);
  setInterval(loadNamespaces, 15000);
  setInterval(loadClusters, 5000);

  // Show demo hint after a short delay (GET to check, won't trigger rollout)
  try {
    const demoCheck = await fetch(apiURL('/api/demo/rollout/toggle'));
    if (demoCheck.ok) {
      setTimeout(() => showToast('Press R to start/stop a rollout demo'), 2000);
    }
  } catch(e) {}

  // Deep link: #pod/<namespace>/<name> or vuek8://pod/<namespace>/<name>
  handleDeepLink();
  window.addEventListener('hashchange', handleDeepLink);

  // Listen for events from Wails (desktop app)
  if (window.runtime && window.runtime.EventsOn) {
    window.runtime.EventsOn('deep-link', (path) => {
      window.location.hash = '#' + path;
    });
    window.runtime.EventsOn('show-changelog', () => {
      showChangelog();
    });
  }
})();

let pendingDeepLink = null;

function handleDeepLink() {
  const hash = window.location.hash;
  if (!hash.startsWith('#pod/')) return;
  // Format: #pod/namespace/name or #pod/namespace/name@server
  const raw = hash.slice(5);
  const atIdx = raw.lastIndexOf('@');
  let path, server;
  if (atIdx !== -1) {
    path = raw.slice(0, atIdx);
    server = decodeURIComponent(raw.slice(atIdx + 1));
  } else {
    path = raw;
    server = '';
  }
  const parts = path.split('/');
  if (parts.length < 2) return;
  const ns = decodeURIComponent(parts[0]);
  const name = decodeURIComponent(parts[1]);
  // Clear hash immediately so it doesn't re-trigger
  history.replaceState(null, '', window.location.pathname + window.location.search);
  resolveDeepLink(ns, name, server);
}

async function resolveDeepLink(ns, name, server) {
  // If server is specified and we're on the wrong cluster, switch first
  if (server) {
    const active = clusters.find(c => c.active);
    if (!active || active.server !== server) {
      const target = clusters.find(c => c.server === server && !c.hidden);
      if (target) {
        pendingDeepLink = { ns, name, server };
        await switchCluster(target.id);
        return; // switchCluster will refresh data, checkPendingDeepLink will fire
      }
    }
  }

  const pod = allPods.find(p => p.namespace === ns && p.name === name);
  if (pod) {
    openDetail(pod);
    pendingDeepLink = null;
  } else if (allPods.length === 0) {
    // Data not loaded yet — retry after next refresh
    pendingDeepLink = { ns, name, server };
  } else {
    // Pod not found in current data — open with minimal info
    openDetail({
      name: name,
      namespace: ns,
      status: 'Unknown',
      ready: '-',
      restarts: 0,
      age: '-',
      node: '-',
      containers: [],
      workloadName: '',
      workloadKind: '',
      cpuRequestMilli: 0, cpuLimitMilli: 0,
      memRequestBytes: 0, memLimitBytes: 0,
    });
    pendingDeepLink = null;
  }
}

// Called after each data refresh to resolve pending deep links
function checkPendingDeepLink() {
  if (!pendingDeepLink) return;
  resolveDeepLink(pendingDeepLink.ns, pendingDeepLink.name, pendingDeepLink.server);
}
