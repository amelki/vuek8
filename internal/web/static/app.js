const nsSelect = document.getElementById('namespace-select');
const wlSelect = document.getElementById('workload-select');
const groupSelect = document.getElementById('group-select');
const podSearch = document.getElementById('pod-search');
const podCount = document.getElementById('pod-count');
const tree = document.getElementById('tree');
const topoEl = document.getElementById('topology');
const tooltipEl = document.getElementById('tooltip');
const detailPanel = document.getElementById('detail-panel');
const detailTitle = document.getElementById('detail-title');
const detailBody = document.getElementById('detail-body');

const sidebar = document.getElementById('sidebar');
const clusterListEl = document.getElementById('cluster-list');

let apiBase = ''; // empty = same origin (browser mode), set to http://... in native mode
let activeTab = 'topology';
let allNodes = [];
let allPods = [];
let allMetrics = {}; // key: "namespace/podName" -> { cpuMilli, memBytes }
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
  if (activeTab !== 'topology') return;
  // Generate fake topology cards
  const pools = [
    { name: '', nodes: 4, dotsPerNode: 6 },
    { name: '', nodes: 3, dotsPerNode: 0 },
    { name: '', nodes: 5, dotsPerNode: [45, 30, 35, 25, 40] },
    { name: '', nodes: 4, dotsPerNode: [20, 15, 25, 18] },
  ];
  let html = '';
  for (const pool of pools) {
    html += `<div class="topo-pool skeleton-pool">`;
    html += `<div class="topo-pool-header"><span class="skeleton-text skeleton-w120"></span> <span class="skeleton-text skeleton-w80"></span></div>`;
    html += `<div class="topo-machines">`;
    for (let i = 0; i < pool.nodes; i++) {
      const dotCount = Array.isArray(pool.dotsPerNode) ? pool.dotsPerNode[i] : pool.dotsPerNode;
      html += `<div class="topo-machine skeleton-card">`;
      html += `<div class="topo-machine-header"><span class="skeleton-text skeleton-w100"></span><span class="skeleton-text skeleton-w30"></span></div>`;
      html += `<div class="topo-machine-resources"><span class="skeleton-text skeleton-w80"></span></div>`;
      if (dotCount > 0) {
        html += `<div class="topo-pods">`;
        for (let d = 0; d < dotCount; d++) {
          html += `<div class="topo-dot skeleton-dot"></div>`;
        }
        html += `</div>`;
      }
      html += `</div>`;
    }
    html += `</div></div>`;
  }
  topoEl.innerHTML = html;
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

// Wait for cache to be ready, showing progress
async function waitForCache() {
  showProgress(5, 'Connecting to cluster...');
  let shownCachedData = false;
  let fakeProgress = 5;
  showSkeleton();
  while (true) {
    try {
      const p = await fetchJSON('/api/progress');

      // If ready (even from disk cache), load and show data immediately
      if (p.ready && !shownCachedData) {
        shownCachedData = true;
        // Load cached data right away
        await loadDataQuiet();
      }

      if (p.error) {
        hideProgress();
        showErrorBanner(p.error);
        return;
      }

      // If live data is loaded (not just disk cache), we're done
      if (p.ready && !p.loading) {
        hideProgress();
        hideErrorBanner();
        return;
      }

      // Still loading live data — show progress
      // Fake progress that slowly approaches 90% but never reaches it
      fakeProgress += (90 - fakeProgress) * 0.08;
      if (p.total > 0) {
        const realPct = Math.round((p.current / p.total) * 100);
        const pct = Math.max(fakeProgress, realPct);
        showProgress(pct, `Refreshing... (${p.current}/${p.total})`);
      } else if (shownCachedData) {
        showProgress(fakeProgress, 'Refreshing live data...');
      } else {
        showProgress(fakeProgress, 'Loading cluster data...');
      }
    } catch (e) {
      // server not ready yet
    }
    await new Promise(r => setTimeout(r, 300));
  }
}

// All API calls are instant (served from server-side cache)
async function loadDataQuiet() {
  const [nodesResult, podsResult, metricsResult] = await Promise.allSettled([
    fetchJSON('/api/nodes'),
    fetchJSON('/api/pods'),
    fetchJSON('/api/metrics'),
  ]);
  if (nodesResult.status === 'fulfilled') allNodes = nodesResult.value;
  if (podsResult.status === 'fulfilled') allPods = podsResult.value;
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
  populateWorkloads();
  render();
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
  return p.workloadKind + '/' + (p.workloadName || p.name);
}

// --- Rendering ---

function getFilteredPods() {
  const ns = nsSelect.value;
  const wl = wlSelect.value;
  const filter = podSearch.value.toLowerCase();

  let filtered = allPods;
  if (ns) filtered = filtered.filter(p => p.namespace === ns);
  if (wl) filtered = filtered.filter(p => (p.workloadKind + '/' + (p.workloadName || p.name)) === wl);
  if (filter) filtered = filtered.filter(p => p.name.toLowerCase().includes(filter));

  podCount.textContent = `${filtered.length} pods`;
  return filtered;
}

function populateWorkloads() {
  const ns = nsSelect.value;
  let pods = allPods;
  if (ns) pods = pods.filter(p => p.namespace === ns);

  const workloads = new Map();
  for (const p of pods) {
    const key = p.workloadKind + '/' + (p.workloadName || p.name);
    if (!workloads.has(key)) workloads.set(key, { kind: p.workloadKind, name: p.workloadName || p.name });
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
  let html = podTableHeader();
  html += `<div class="flat-list">`;
  for (const p of pods) {
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
    const workloadNames = new Set(kindPods.map(p => p.workloadName || p.name));

    html += `<div class="tree-node">`;
    html += `<div class="group-header" data-group="${esc(kindKey)}">`;
    html += `<span class="node-toggle ${isOpen ? 'open' : ''}">&#9654;</span>`;
    html += `<span class="workload-kind">${esc(kind)}</span>`;
    html += `<span class="pod-count-badge">${workloadNames.size} workloads</span>`;
    html += `<span class="pod-count-badge">${kindPods.length} pods</span>`;
    html += `</div>`;
    html += `<div class="group-children ${isOpen ? '' : 'collapsed'}">`;

    // Second level: individual workloads
    const byWorkload = groupBy(kindPods, p => p.workloadName || p.name);
    const sorted = [...byWorkload.entries()].sort((a, b) => a[0].localeCompare(b[0]));
    for (const [name, wPods] of sorted) {
      if (podSearch.value && wPods.length === 0) continue;
      html += renderGroup(kindKey + '/' + name, wPods, workloadHeaderContent(kind, name, wPods.length), false, true);
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

    const workloadNames = new Set(kindPods.map(p => p.workloadName || p.name));

    html += `<div class="tree-node">`;
    html += `<div class="group-header" data-group="${esc(kindKey)}">`;
    html += `<span class="node-toggle ${isKindOpen ? 'open' : ''}">&#9654;</span>`;
    html += `<span class="workload-kind">${esc(kind)}</span>`;
    html += `<span class="pod-count-badge">${workloadNames.size} workloads</span>`;
    html += `<span class="pod-count-badge">${kindPods.length} pods</span>`;
    html += `</div>`;
    html += `<div class="group-children ${isKindOpen ? '' : 'collapsed'}">`;

    // Second level: individual workloads
    const byWorkload = groupBy(kindPods, p => p.workloadName || p.name);
    const sortedWorkloads = [...byWorkload.entries()].sort((a, b) => a[0].localeCompare(b[0]));

    for (const [wName, wPods] of sortedWorkloads) {
      if (podSearch.value && wPods.length === 0) continue;

      const wKey = kindKey + '/' + wName;
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
  { name: 'Namespace', min: 120 },
  { name: 'Name', min: 100, flex: true },
  { name: 'Containers', min: 105, align: 'right' },
  { name: 'Status', min: 120, align: 'right' },
  { name: 'Ready', min: 85, align: 'right' },
  { name: 'Restarts', min: 95, align: 'right' },
  { name: 'Age', min: 85, align: 'right' },
  { name: 'Tag', min: 85, align: 'right' },
];

function gridTemplateFromColumns() {
  return columns.map(c => c.flex ? '1fr' : (c.width || c.min) + 'px').join(' ');
}

function podTableHeader() {
  const tpl = gridTemplateFromColumns();
  let h = `<div class="pod-table-header" style="grid-template-columns: ${tpl}">`;
  columns.forEach((col, i) => {
    const align = col.align === 'right' ? ' style="text-align:right"' : '';
    h += `<span class="col-header" data-col="${i}"${align}>${esc(col.name)}`;
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

  if (isOpen && p.containers) {
    r += `<div class="container-list">`;
    for (const c of p.containers) {
      r += `<div class="container-row">`;
      r += `<span class="container-name">${esc(c.name)}</span>`;
      r += `<span class="container-image">${esc(c.image)}</span>`;
      r += `<span class="container-tag" title="${esc(c.tag)}">${esc(c.tag)}</span>`;
      r += `</div>`;
    }
    r += `</div>`;
  }
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
  html += detailField('Status', p.status);
  html += detailField('Ready', p.ready);
  if (p.restarts > 0) html += detailField('Restarts', p.restarts);
  html += detailField('Age', p.age);
  html += detailField('Node', p.node);
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
    html += detailField('CPU Usage', cpuDetail);
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
    html += detailField('Memory Usage', memDetail);
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
    html += detailField('Name', p.workloadName);
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
    logsEl.textContent = await res.text() || '(no logs)';
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
    logsEl.textContent = tailLines.join('\n');
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
      logsEl.textContent = tailLines.join('\n');
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

  const items = [
    { label: 'Open shell in terminal', action: () => openTerminalExec() },
    { label: 'Copy shell command', action: () => copyTerminalCmd('exec') },
    { sep: true },
    { label: 'Tail logs in terminal', action: () => openTerminalLogs() },
    { label: 'Copy tail command', action: () => copyTerminalCmd('logs') },
  ];

  for (const item of items) {
    if (item.sep) {
      const sep = document.createElement('div');
      sep.className = 'pod-actions-sep';
      menu.appendChild(sep);
      continue;
    }
    const el = document.createElement('div');
    el.className = 'pod-actions-item';
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
    btn.innerHTML = '&#x2716;';
    btn.title = 'Collapse logs';
  } else {
    btn.innerHTML = '&#x26F6;';
    btn.title = 'Expand logs';
  }
}

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && detailPanel.classList.contains('logs-fullscreen')) {
    toggleLogsFullscreen();
  }
});

function detailField(label, value) {
  return `<div class="detail-field"><span class="detail-field-label">${esc(label)}</span><span class="detail-field-value" title="${esc(String(value))}">${esc(String(value))}</span></div>`;
}

// --- Topology view ---

function nodePool(name) {
  // Strip trailing instance identifiers:
  //   "other-5" → "other"               (numeric index)
  //   "shards-dc3-10" → "shards-dc3"    (numeric index)
  //   "scw-mee6-staging-other-01491037da69411fb40b727" → "scw-mee6-staging-other" (hash)
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

function dotClass(status) {
  const s = status.toLowerCase().replace(/[^a-z]/g, '');
  const known = ['running','succeeded','completed','pending','containercreating','failed','error','crashloopbackoff','imagepullbackoff','errimagepull','terminating'];
  return 'topo-dot topo-dot-' + (known.includes(s) ? s : 'unknown');
}

function getColorMode() {
  return document.getElementById('color-mode').value;
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

function dotStyle(p) {
  const mode = getColorMode();
  if (mode === 'status') return '';
  const key = p.namespace + '/' + p.name;
  const m = allMetrics[key];
  if (!m) return 'background: #21262d;';

  if (mode === 'cpu') {
    const limit = p.cpuLimitMilli || 0;
    if (limit > 0) return resourceColor((m.cpuMilli / limit) * 100);
    const nodeCPU = getNodeCPUMilli(p.node);
    if (nodeCPU > 0) return resourceColor((m.cpuMilli / nodeCPU) * 100);
    return 'background: #21262d;';
  }

  if (mode === 'mem') {
    const limit = p.memLimitBytes || 0;
    if (limit > 0) return resourceColor((m.memBytes / limit) * 100);
    const nodeMem = getNodeMemBytes(p.node);
    if (nodeMem > 0) return resourceColor((m.memBytes / nodeMem) * 100);
    return 'background: #21262d;';
  }

  return '';
}

function renderTopology() {
  const filtered = getFilteredPods();
  const filter = podSearch.value.toLowerCase();

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
    const pool = nodePool(n.name);
    if (!pools.has(pool)) pools.set(pool, []);
    pools.get(pool).push(n);
  }

  // Sort pools by name
  const sortedPools = [...pools.entries()].sort((a, b) => a[0].localeCompare(b[0]));

  let html = '';
  for (const [pool, nodes] of sortedPools) {
    const totalPods = nodes.reduce((sum, n) => sum + (podsByNode.get(n.name) || []).length, 0);
    if (filter && totalPods === 0) continue;

    html += `<div class="topo-pool">`;
    html += `<div class="topo-pool-header">${esc(pool)} <span class="pool-count">${nodes.length} nodes / ${totalPods} pods</span></div>`;
    html += `<div class="topo-machines">`;

    for (const n of nodes) {
      const pods = podsByNode.get(n.name) || [];
      if (filter && pods.length === 0) continue;
      html += `<div class="topo-machine ${n.status !== 'Ready' ? 'not-ready' : ''}">`;
      html += `<div class="topo-machine-header">`;
      html += `<span class="topo-machine-name">${esc(n.name)}</span>`;
      html += `<span class="topo-machine-stats">${pods.length}</span>`;
      html += `</div>`;
      html += `<div class="topo-machine-resources">${esc(n.cpuCapacity)} CPU &middot; ${esc(n.memoryCapacity)}</div>`;
      html += `<div class="topo-pods">`;
      for (const p of pods) {
        html += `<div class="${dotClass(p.status)}" style="${dotStyle(p)}" data-pod-b64="${btoa(JSON.stringify(p))}"></div>`;
      }
      html += `</div>`;
      html += `</div>`;
    }

    html += `</div></div>`;
  }

  topoEl.innerHTML = html;

  // Attach hover tooltips
  topoEl.querySelectorAll('.topo-dot').forEach(el => {
    el.addEventListener('mouseenter', (e) => {
      const p = JSON.parse(atob(el.dataset.podB64));
      const tag = (p.containers && p.containers.length > 0) ? p.containers[0].tag : '';
      tooltipEl.innerHTML =
        `<div class="tooltip-row"><span class="tooltip-label">Pod</span> <span class="tooltip-value">${esc(p.name)}</span></div>` +
        `<div class="tooltip-row"><span class="tooltip-label">Namespace</span> <span class="tooltip-value">${esc(p.namespace)}</span></div>` +
        `<div class="tooltip-row"><span class="tooltip-label">Status</span> <span class="tooltip-value">${esc(p.status)}</span></div>` +
        `<div class="tooltip-row"><span class="tooltip-label">Ready</span> <span class="tooltip-value">${esc(p.ready)}</span></div>` +
        (p.restarts > 0 ? `<div class="tooltip-row"><span class="tooltip-label">Restarts</span> <span class="tooltip-value">${p.restarts}</span></div>` : '') +
        `<div class="tooltip-row"><span class="tooltip-label">Age</span> <span class="tooltip-value">${esc(p.age)}</span></div>` +
        (tag ? `<div class="tooltip-row"><span class="tooltip-label">Image</span> <span class="tooltip-value">${esc(tag)}</span></div>` : '') +
        (p.workloadName ? `<div class="tooltip-row"><span class="tooltip-label">${esc(p.workloadKind)}</span> <span class="tooltip-value">${esc(p.workloadName)}</span></div>` : '');
      // Add CPU/Memory info if available
      const metricKey = p.namespace + '/' + p.name;
      const metric = allMetrics[metricKey];
      if (metric) {
        const cpuLimit = p.cpuLimitMilli || 0;
        const memLimit = p.memLimitBytes || 0;
        const cpuPct = cpuLimit > 0 ? Math.round((metric.cpuMilli / cpuLimit) * 100) : null;
        const memMi = Math.round(metric.memBytes / 1024 / 1024);
        const memLimitMi = memLimit > 0 ? Math.round(memLimit / 1024 / 1024) : 0;

        let cpuText = fmtCPU(metric.cpuMilli);
        if (cpuLimit > 0) {
          cpuText += ` / ${fmtCPU(cpuLimit)} (${cpuPct}%)`;
        } else {
          const nodeCPU = getNodeCPUMilli(p.node);
          if (nodeCPU > 0) {
            const nodePct = Math.round((metric.cpuMilli / nodeCPU) * 100);
            cpuText += ` / ${fmtCPU(nodeCPU)} node (${nodePct}%)`;
          }
        }

        let memText = fmtMem(metric.memBytes);
        if (memLimit > 0) {
          const memPct = Math.round((metric.memBytes / memLimit) * 100);
          memText += ` / ${fmtMem(memLimit)} limit (${memPct}%)`;
        } else {
          const nodeMem = getNodeMemBytes(p.node);
          if (nodeMem > 0) {
            const nodePct = Math.round((metric.memBytes / nodeMem) * 100);
            memText += ` / ${fmtMem(nodeMem)} node (${nodePct}%)`;
          }
        }

        tooltipEl.innerHTML += `<div class="tooltip-row"><span class="tooltip-label">CPU</span> <span class="tooltip-value">${cpuText}</span></div>`;
        tooltipEl.innerHTML += `<div class="tooltip-row"><span class="tooltip-label">Memory</span> <span class="tooltip-value">${memText}</span></div>`;
      }
      tooltipEl.classList.remove('hidden');
      positionTooltip(e);
    });
    el.addEventListener('mousemove', positionTooltip);
    el.addEventListener('mouseleave', () => {
      tooltipEl.classList.add('hidden');
    });
    el.addEventListener('click', () => {
      const p = JSON.parse(atob(el.dataset.podB64));
      tooltipEl.classList.add('hidden');
      openDetail(p);
    });
  });
}

function positionTooltip(e) {
  const x = e.clientX + 12;
  const y = e.clientY + 12;
  tooltipEl.style.left = x + 'px';
  tooltipEl.style.top = y + 'px';
}

// --- Tab switching ---

function switchTab(tab) {
  activeTab = tab;
  document.querySelectorAll('.tab').forEach(t => t.classList.toggle('active', t.dataset.tab === tab));
  topoEl.classList.toggle('hidden', tab !== 'topology');
  tree.classList.toggle('hidden', tab !== 'list');
  // Show/hide list-only controls
  document.querySelectorAll('.list-only').forEach(el => el.classList.toggle('hidden-ctrl', tab !== 'list'));
  render();
}

function render() {
  if (activeTab === 'topology') renderTopology();
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
      const id = el.dataset.pod;
      if (expandedPods.has(id)) expandedPods.delete(id);
      else expandedPods.add(id);
      renderTree();
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
  initColumnResize();
}

document.querySelectorAll('.tab').forEach(t => {
  t.addEventListener('click', () => switchTab(t.dataset.tab));
});
document.getElementById('detail-close').addEventListener('click', closeDetail);
document.getElementById('detail-actions-btn').addEventListener('click', (e) => {
  e.stopPropagation();
  showPodActionsMenu(e);
});
document.getElementById('expand-all').addEventListener('click', () => {
  for (const key of expanded.keys()) expanded.set(key, true);
  renderTree();
});
document.getElementById('collapse-all').addEventListener('click', () => {
  for (const key of expanded.keys()) expanded.set(key, false);
  renderTree();
});
nsSelect.addEventListener('change', () => { populateWorkloads(); render(); });
wlSelect.addEventListener('change', render);
groupSelect.addEventListener('change', render);
document.getElementById('color-mode').addEventListener('change', render);
podSearch.addEventListener('input', render);

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
document.querySelectorAll('.list-only').forEach(el => el.classList.add('hidden-ctrl'));

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

function renderSidebar() {
  const showHidden = appSettings.showHidden || false;
  const showAllContexts = appSettings.showAllContexts || false;
  let visible = clusters;
  if (!showAllContexts) visible = visible.filter(c => c.isDefault || c.active);
  if (!showHidden) visible = visible.filter(c => !c.hidden || c.active);

  let html = '';
  for (const c of visible) {
    const cls = (c.active ? ' active' : '') + (c.hidden ? ' hidden-cluster' : '') + (c.error ? ' error-cluster' : '');
    const fileName = c.filePath.split('/').pop();
    html += `<div class="cluster-item${cls}" data-cluster-id="${esc(c.id)}">`;
    html += `<div class="cluster-item-row">`;
    html += `<span class="cluster-name">${esc(c.displayName)}</span>`;
    html += `<span class="cluster-menu-btn" data-menu-id="${esc(c.id)}">&#8942;</span>`;
    html += `</div>`;
    html += `<span class="cluster-file">${esc(fileName)}</span>`;
    html += `<span class="cluster-server">${esc(c.server)}</span>`;
    if (c.error) {
      html += `<span class="cluster-error" title="${esc(c.error)}">unreachable</span>`;
    }
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
}

async function switchCluster(id) {
  try {
    hideProgress();
    hideErrorBanner();
    showProgress(0, 'Switching cluster...');
    allNodes = [];
    allPods = [];
    render();

    await fetch(apiURL('/api/clusters/switch'), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    });

    await loadClusters();
    await waitForCache();
    await loadClusters(); // reload to pick up error state
    await loadNamespaces();
    await refresh();
  } catch (e) {
    console.error('switch failed:', e);
    hideProgress();
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
  renameInput.value = currentName;
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
document.getElementById('rename-cancel').addEventListener('click', () => renameModal.classList.add('hidden'));
document.getElementById('rename-close').addEventListener('click', () => renameModal.classList.add('hidden'));
renameModal.addEventListener('click', (e) => { if (e.target === renameModal) renameModal.classList.add('hidden'); });
renameInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') confirmRename(); if (e.key === 'Escape') renameModal.classList.add('hidden'); });

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

  menu.innerHTML = `
    <div class="options-menu-item" data-option="showHidden">
      <span class="options-check">${showHidden ? '&#10003;' : ''}</span>
      Show hidden clusters
    </div>
    <div class="options-menu-item" data-option="showAllContexts">
      <span class="options-check">${showAllContexts ? '&#10003;' : ''}</span>
      Show all contexts per kubeconfig
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
async function checkForUpdate() {
  try {
    const info = await fetchJSON('/api/version');
    if (info.hasUpdate) {
      const banner = document.getElementById('update-banner');
      document.getElementById('update-text').textContent = `New version available: v${info.latest} (current: v${info.current})`;
      document.getElementById('update-link').href = info.updateUrl;
      banner.classList.remove('hidden');
    }
  } catch (e) {
    // silently ignore
  }
}

document.getElementById('update-dismiss').addEventListener('click', () => {
  document.getElementById('update-banner').classList.add('hidden');
});

// Startup
(async () => {
  // In native app, the API URL is injected into the HTML
  if (window.__KGLANCE_API_BASE__) {
    apiBase = window.__KGLANCE_API_BASE__;
  }
  await loadSettings();
  applySidebarState();
  checkForUpdate();
  await loadClusters();
  await waitForCache();
  await loadNamespaces();
  await refresh();
  setInterval(refresh, 3000);
  setInterval(loadNamespaces, 15000);
  setInterval(loadClusters, 5000);
})();
