/* ── Disk Space Report — App Logic ── */

const API_META = '/api/meta';
const API_SUMMARY = '/api/summary?top=20';
const API_TREE = '/api/tree';
const API_STATUS = '/api/status';

// ── State ──
let rootPath = '/';
let isScanning = false;
let statusInterval = null;

// ── API helpers ──

async function api(url, opts) {
  const res = await fetch(url, opts);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.json();
}

function showError(msg) {
  const banner = document.getElementById('error-banner');
  banner.textContent = msg;
  banner.style.display = 'block';
  setTimeout(() => { banner.style.display = 'none'; }, 6000);
}

function hideError() {
  document.getElementById('error-banner').style.display = 'none';
}

// ── HTML escaping ──

function escHtml(s) {
  const d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

// ── Size visualization ──

function sizeColor(bytes, maxBytes) {
  const ratio = maxBytes > 0 ? bytes / maxBytes : 0;
  if (ratio > 0.7) return 'var(--red)';
  if (ratio > 0.4) return 'var(--orange)';
  if (ratio > 0.15) return 'var(--yellow)';
  return 'var(--green)';
}

function sizeBar(bytes, maxBytes) {
  const ratio = maxBytes > 0 ? Math.max(bytes / maxBytes, 0.015) : 0;
  const color = sizeColor(bytes, maxBytes);
  return `<span class="size-bar" style="width:${Math.round(ratio * 56)}px;background:${color}"></span>`;
}

// ── Path display ──

function displayPath(path) {
  if (rootPath !== '/' && path.startsWith(rootPath)) {
    return path.slice(rootPath.length) || '/';
  }
  return path;
}

// ── Scan status polling ──

function startStatusPolling() {
  pollStatus();
  statusInterval = setInterval(pollStatus, 3000);
}

async function pollStatus() {
  try {
    const status = await api(API_STATUS);
    const prev = isScanning;
    isScanning = status.scanning;
    updateScanBanner(status);
    // Scan just finished — reload data
    if (prev && !isScanning) {
      loadSummary();
      navigateTo(rootPath);
    }
  } catch (_) {
    // Status endpoint not critical
  }
}

function updateScanBanner(status) {
  const banner = document.getElementById('scan-banner');
  if (status.scanning) {
    banner.style.display = 'flex';
    const count = status.scanned_dirs || 0;
    banner.querySelector('.scan-count').textContent = count.toLocaleString();
  } else {
    banner.style.display = 'none';
  }
}

// ── Summary ──

async function loadSummary() {
  const list = document.getElementById('summary-list');
  try {
    const dirs = await api(API_SUMMARY);
    if (!dirs || dirs.length === 0) {
      list.innerHTML = '<div class="empty-state">No scan data available</div>';
      return;
    }
    const maxSize = dirs[0].size;
    list.innerHTML = dirs.map((d, i) => `
      <li class="summary-item" data-path="${escHtml(d.path)}">
        <span class="summary-rank">${String(i + 1).padStart(2, ' ')}</span>
        <span class="summary-path">${sizeBar(d.size, maxSize)}${escHtml(displayPath(d.path))}</span>
        <span class="summary-size" style="color:${sizeColor(d.size, maxSize)}">${escHtml(d.size_formatted)}</span>
      </li>
    `).join('');
    list.querySelectorAll('.summary-item').forEach(el => {
      el.addEventListener('click', () => navigateTo(el.dataset.path));
    });
  } catch (e) {
    list.innerHTML = '<div class="empty-state">Failed to load summary data</div>';
  }
}

// ── Tree ──

async function loadChildren(path) {
  return api(`${API_TREE}?path=${encodeURIComponent(path)}&limit=500`);
}

function renderBreadcrumb(path) {
  const parts = path.split('/').filter(Boolean);
  let html = '<span class="crumb" data-path="/">/</span>';
  let accumulated = '';
  for (const part of parts) {
    accumulated += '/' + part;
    html += `<span class="sep">/</span><span class="crumb" data-path="${escHtml(accumulated)}">${escHtml(part)}</span>`;
  }
  const bar = document.getElementById('tree-breadcrumb');
  bar.innerHTML = html;
  bar.querySelectorAll('.crumb').forEach(el => {
    el.addEventListener('click', () => navigateTo(el.dataset.path));
  });
}

function renderTreeNodes(entries, container, parentMaxSize) {
  const maxSize = entries.length > 0 ? entries[0].size : 0;
  container.innerHTML = '';
  for (const entry of entries) {
    const node = document.createElement('div');
    node.className = 'tree-node';

    const isShallow = entry.shallow;
    const row = document.createElement('div');
    row.className = 'tree-row';
    row.innerHTML = `
      <span class="tree-toggle ${isShallow ? 'leaf' : ''}">&#9654;</span>
      <span class="tree-name">${escHtml(entry.name)}${isShallow ? '<span class="shallow-tag">shallow</span>' : ''}</span>
      <span class="tree-size" style="color:${sizeColor(entry.size, parentMaxSize || maxSize)}">${escHtml(entry.size_formatted)}</span>
    `;

    const childrenDiv = document.createElement('div');
    childrenDiv.className = 'tree-children hidden';

    if (!isShallow) {
      let loaded = false;
      row.addEventListener('click', async () => {
        const toggle = row.querySelector('.tree-toggle');
        if (!loaded) {
          toggle.classList.add('open');
          childrenDiv.classList.remove('hidden');
          childrenDiv.innerHTML = '<div class="tree-loading loading-spinner">Loading</div>';
          try {
            const children = await loadChildren(entry.path);
            if (children.length === 0) {
              childrenDiv.innerHTML = '<div class="tree-loading">Empty</div>';
              toggle.classList.add('leaf');
            } else {
              renderTreeNodes(children, childrenDiv, maxSize);
            }
          } catch (e) {
            childrenDiv.innerHTML = `<div class="tree-error">Error: ${escHtml(e.message)}</div>`;
          }
          loaded = true;
        } else {
          const open = toggle.classList.toggle('open');
          childrenDiv.classList.toggle('hidden', !open);
        }
      });
    }

    node.appendChild(row);
    node.appendChild(childrenDiv);
    container.appendChild(node);
  }
}

async function navigateTo(path) {
  renderBreadcrumb(path);
  const container = document.getElementById('tree-root');
  container.innerHTML = '<div class="tree-loading loading-spinner">Loading</div>';
  try {
    const entries = await loadChildren(path);
    if (entries.length === 0) {
      container.innerHTML = '<div class="empty-state">No subdirectories found</div>';
    } else {
      renderTreeNodes(entries, container, entries[0].size);
    }
  } catch (e) {
    container.innerHTML = `<div class="tree-error">Failed to load directory tree: ${escHtml(e.message)}</div>`;
  }
}

// ── Init ──

document.addEventListener('DOMContentLoaded', async () => {
  // Fetch meta to get root path
  try {
    const meta = await api(API_META);
    if (meta && meta.root) {
      rootPath = meta.root;
    }
    document.getElementById('scan-info').textContent = rootPath;
  } catch (e) {
    // Fall back to "/" — UI still works
    rootPath = '/';
    showError('Could not load scan metadata');
  }

  // Load summary and initial tree
  loadSummary();
  navigateTo(rootPath);

  // Start polling for scan status
  startStatusPolling();
});
