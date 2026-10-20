const form = document.querySelector('#project-form');
const projectInput = document.querySelector('#project-id');
const status = document.querySelector('#status');
const scanButton = document.querySelector('#scan-security');
const inventoryButton = document.querySelector('#scan-inventory');
const metricCatalogButton = document.querySelector('#load-metrics');
const metricQueryButton = document.querySelector('#query-metrics');
const metricSelect = document.querySelector('#metric-select');
const metricWindow = document.querySelector('#metric-window');
const metricMeasure = document.querySelector('#metric-measure');
const metricStatus = document.querySelector('#metric-status');
const metricChart = document.querySelector('#metric-chart');
const findingDetailsToggle = document.querySelector('#include-security-findings');
const securityMinSeverity = document.querySelector('#security-min-severity');
const securitySessionFilter = document.querySelector('#security-session-filter');
const securityReviewButton = document.querySelector('#show-security-reviews');
const securityReviewPrevious = document.querySelector('#security-reviews-previous');
const securityReviewNext = document.querySelector('#security-reviews-next');
const securityReviewPageSize = 100;
let securityReviewOffset = 0;
let securityReviewHasNextPage = false;

function renderCounts(target, values) {
  target.replaceChildren();
  const entries = Object.entries(values || {}).sort(([left], [right]) => left.localeCompare(right));
  if (entries.length === 0) {
    const item = document.createElement('li');
    item.textContent = 'No records';
    target.append(item);
    return;
  }
  for (const [name, count] of entries) {
    const item = document.createElement('li');
    item.textContent = `${name}: ${count}`;
    target.append(item);
  }
}

function renderRuntimes(counts, lastSeenByHarness) {
  const target = document.querySelector('#runtimes');
  target.replaceChildren();
  const names = Object.keys(counts || {}).sort((left, right) => left.localeCompare(right));
  if (names.length === 0) {
    const item = document.createElement('li');
    item.textContent = 'No observed runtime events.';
    target.append(item);
    return;
  }
  for (const name of names) {
    const timestamp = lastSeenByHarness?.[name];
    const date = timestamp ? new Date(timestamp) : null;
    const lastSeen = date && !Number.isNaN(date.getTime()) ? date.toLocaleString() : 'not available';
    const item = document.createElement('li');
    item.textContent = `${name}: ${counts[name]} events · last seen ${lastSeen}`;
    target.append(item);
  }
}

function renderProcedures(value) {
  const target = document.querySelector('#procedures');
  target.replaceChildren();
  if (!value) {
    const item = document.createElement('li');
    item.textContent = 'Enter a project ID to view.';
    target.append(item);
    return;
  }
  renderCounts(target, value);
}

function renderTasks(value, recent) {
  const target = document.querySelector('#tasks');
  target.replaceChildren();
  if (!value) {
    const item = document.createElement('li');
    item.textContent = 'Enter a project ID to view.';
    target.append(item);
    return;
  }
 renderCounts(target, value);
  const recentTarget = document.querySelector("#recent-tasks");
  recentTarget.replaceChildren();
  if (!Array.isArray(recent) || recent.length === 0) {
    const item = document.createElement("li");
    item.textContent = "No recent tasks.";
    recentTarget.append(item);
    return;
  }
  for (const task of recent.slice(0, 8)) {
    const item = document.createElement("li");
    const nextAction = task.next_action ? " — " + task.next_action : "";
    item.textContent = (task.title || task.id) + " (" + task.status + ", " + task.requirements_state + ")" + nextAction;
    recentTarget.append(item);
  }
}

function renderRecent(events) {
  const target = document.querySelector('#recent');
  target.replaceChildren();
  for (const event of events || []) {
    const row = document.createElement('tr');
    for (const value of [event.timestamp, event.harness, event.action, event.severity]) {
      const cell = document.createElement('td');
      cell.textContent = value || '—';
      row.append(cell);
    }
    target.append(row);
  }
  if (!target.childElementCount) {
    const row = document.createElement('tr');
    const cell = document.createElement('td');
    cell.colSpan = 4;
    cell.textContent = 'No captured events.';
    row.append(cell);
    target.append(row);
  }
}

function renderSecurityFindingDetails(data) {
  const target = document.querySelector('#security-finding-details');
  target.replaceChildren();
  if (!data.finding_details_included) return;
  if (!Array.isArray(data.findings) || data.findings.length === 0) {
    const empty = document.createElement('p');
    empty.className = 'muted';
    empty.textContent = 'No findings at the selected severity threshold.';
    target.append(empty);
    return;
  }
  for (const finding of data.findings) {
    const card = document.createElement('article');
    card.className = 'security-finding';
    const heading = document.createElement('h3');
    heading.textContent = `[${finding.severity}] ${finding.title} (${finding.rule_id})`;
    card.append(heading);
    const posture = document.createElement('p');
    posture.className = 'muted';
    posture.textContent = `Posture: ${finding.posture}`;
    card.append(posture);
    const reason = document.createElement('p');
    reason.textContent = finding.reason;
    card.append(reason);
    const reviewStatus = document.createElement('p');
    reviewStatus.className = 'muted';
    reviewStatus.textContent = `Review: ${finding.review_status || 'open'} (revision ${finding.review_revision || 0})`;
    card.append(reviewStatus);
    card.append(createSecurityReviewControls(finding, (updated) => {
      finding.review_status = updated.status;
      finding.review_revision = updated.revision;
      finding.review_active = updated.active;
      reviewStatus.textContent = `Review: ${updated.status} (revision ${updated.revision})`;
    }));
    const evidence = document.createElement('ul');
    for (const event of finding.events || []) {
      const item = document.createElement('li');
      item.textContent = `${event.timestamp} · ${event.harness} · ${event.action}`;
      evidence.append(item);
    }
    if (finding.events_truncated) {
      const item = document.createElement('li');
      item.className = 'muted';
      item.textContent = 'Additional matched event metadata omitted by the display limit.';
      evidence.append(item);
    }
    card.append(evidence);
    target.append(card);
  }
  if (data.findings_truncated) {
    const notice = document.createElement('p');
    notice.className = 'muted';
    notice.textContent = 'Only the first 100 finding summaries are shown. Use the local CLI JSON output for the complete result.';
    target.append(notice);
  }
}

function createSecurityReviewControls(review, onUpdated) {
  const controls = document.createElement('div');
  controls.className = 'security-review-controls';
  const select = document.createElement('select');
  select.setAttribute('aria-label', 'Security finding review status');
  for (const value of ['open', 'acknowledged', 'resolved', 'false_positive']) {
    const option = document.createElement('option');
    option.value = value;
    option.textContent = value.replace('_', ' ');
    select.append(option);
  }
  select.value = review.status || review.review_status || 'open';
  const save = document.createElement('button');
  save.type = 'button';
  save.textContent = 'Save review';
  save.disabled = select.value === (review.status || review.review_status || 'open');
  const message = document.createElement('span');
  message.className = 'muted';
  select.addEventListener('change', () => {
    save.disabled = select.value === (review.status || review.review_status || 'open');
  });
  save.addEventListener('click', async () => {
    save.disabled = true;
    message.textContent = 'Saving…';
    try {
      const response = await fetch(`/v1/security/finding-reviews/${encodeURIComponent(review.fingerprint)}`, {
        method: 'POST',
        headers: { accept: 'application/json', 'content-type': 'application/json' },
        body: JSON.stringify({
          schema_version: 'threadforge.security-review-request.v1',
          expected_revision: review.revision ?? review.review_revision ?? 0,
          status: select.value,
          actor: 'local-user',
        }),
      });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || `Review update failed (${response.status}).`);
      review.status = data.review.status;
      review.revision = data.review.revision;
      review.active = data.review.active;
      select.value = data.review.status;
      message.textContent = 'Saved.';
      save.disabled = true;
      onUpdated?.(data.review);
    } catch (error) {
      message.textContent = error instanceof Error ? error.message : 'Review update failed.';
      save.disabled = false;
    }
  });
  controls.append(select, save, message);
  return controls;
}

async function loadSecurityReviewHistory(requestedOffset = securityReviewOffset) {
  const button = securityReviewButton;
  const statusText = document.querySelector('#security-review-status');
  const target = document.querySelector('#security-review-history');
  button.disabled = true;
  securityReviewPrevious.disabled = true;
  securityReviewNext.disabled = true;
  statusText.textContent = 'Loading local review state…';
  target.replaceChildren();
  try {
    const offset = Math.max(0, Math.floor(requestedOffset));
    const response = await fetch(`/v1/security/finding-reviews?limit=${securityReviewPageSize}&offset=${offset}`, { headers: { accept: 'application/json' } });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || `Review history failed (${response.status}).`);
    securityReviewOffset = data.offset;
    securityReviewHasNextPage = data.offset + data.reviews.length < data.total;
    for (const review of data.reviews || []) {
      const card = document.createElement('article');
      card.className = 'security-finding';
      const heading = document.createElement('h3');
      heading.textContent = `[${review.severity}] ${review.rule_id}`;
      card.append(heading);
      const detail = document.createElement('p');
      detail.className = 'muted';
      detail.textContent = `${review.active ? 'Active in last complete scan' : 'Not present in last complete scan'} · First seen ${review.first_seen} · Last seen ${review.last_seen} · ${review.fingerprint.slice(0, 12)}`;
      card.append(detail);
      card.append(createSecurityReviewControls(review, (updated) => {
        review.status = updated.status;
        review.revision = updated.revision;
      }));
      target.append(card);
    }
    if (!target.childElementCount) {
      const empty = document.createElement('p');
      empty.className = 'muted';
      empty.textContent = 'No complete persisted security scans yet.';
      target.append(empty);
    }
    const first = data.reviews.length ? data.offset + 1 : 0;
    const last = data.offset + data.reviews.length;
    statusText.textContent = `Showing ${first}–${last} of ${data.total} local finding review(s).`;
  } catch (error) {
    statusText.textContent = error instanceof Error ? error.message : 'Review history unavailable.';
  } finally {
    button.disabled = false;
    securityReviewPrevious.disabled = securityReviewOffset <= 0;
    securityReviewNext.disabled = !securityReviewHasNextPage;
  }
}

async function refresh() {
  status.textContent = 'Loading local status…';
  const project = projectInput.value.trim();
  const query = project ? `?project_id=${encodeURIComponent(project)}` : '';
  try {
    const response = await fetch(`/v1/dashboard${query}`, { headers: { accept: 'application/json' } });
    if (!response.ok) throw new Error(`Dashboard request failed (${response.status}).`);
    const data = await response.json();
    document.querySelector('#event-count').textContent = String(data.traces.sampled_events);
    document.querySelector('#event-window').textContent = data.traces.older_events_omitted
      ? 'Recent 16 MiB window; older events omitted.'
      : 'All locally stored events.';
    renderRuntimes(data.traces.by_harness, data.traces.last_seen_by_harness);
    renderCounts(document.querySelector('#actions'), data.traces.by_action);
    renderProcedures(data.procedures);
    renderTasks(data.tasks, data.recent_tasks);
    renderCounts(document.querySelector('#migrations'), data.migrations_by_state);
    renderRecent(data.traces.recent);
    status.textContent = data.traces.malformed_lines
      ? `Loaded with ${data.traces.malformed_lines} malformed trace line(s) reported.`
      : 'Local status loaded.';
  } catch (error) {
    status.textContent = error instanceof Error ? error.message : 'Dashboard unavailable.';
  }
}

async function runSecurityScan() {
  const scanStatus = document.querySelector('#security-status');
  const findings = document.querySelector('#security-findings');
  scanButton.disabled = true;
  findingDetailsToggle.disabled = true;
  securityMinSeverity.disabled = true;
  securitySessionFilter.disabled = true;
  scanStatus.textContent = 'Scanning local telemetry…';
  findings.replaceChildren();
  document.querySelector('#security-finding-details').replaceChildren();
  try {
    const minimumSeverity = securityMinSeverity.value;
    const sessionFilter = securitySessionFilter.value.trim();
    const includeDetails = findingDetailsToggle.checked;
    const params = new URLSearchParams({ min_severity: minimumSeverity });
    if (includeDetails) params.set('include_findings', 'true');
    if (sessionFilter) params.set('session', sessionFilter);
    const response = await fetch(`/v1/security/scan?${params.toString()}`, {
      method: 'POST',
      headers: { accept: 'application/json' },
    });
    if (!response.ok) throw new Error(`Security scan failed (${response.status}).`);
    const data = await response.json();
    renderCounts(findings, data.findings_by_severity);
    renderSecurityFindingDetails(data);
    scanStatus.textContent = data.complete
      ? `Scanned ${data.event_count} event(s)${sessionFilter ? ' in the selected session' : ''} against ${data.rule_count} rules; ${data.finding_count} finding(s). ${data.review_state_saved ? 'Review state recorded locally.' : (sessionFilter || minimumSeverity !== 'info') ? 'Filtered scan; persistent review state was not changed.' : 'Review state was not changed.'}`
      : `Scan incomplete: ${data.malformed_lines} malformed event(s); review state was not changed.`;
    if (data.finding_details_included && data.findings_truncated) {
      scanStatus.textContent += ' Finding details are truncated to the first 100 summaries.';
    }
  } catch (error) {
    scanStatus.textContent = error instanceof Error ? error.message : 'Security scan unavailable.';
  } finally {
    scanButton.disabled = false;
    findingDetailsToggle.disabled = false;
    securityMinSeverity.disabled = false;
    securitySessionFilter.disabled = false;
  }
}

async function runInventoryScan() {
  const scanStatus = document.querySelector('#inventory-status');
  const summary = document.querySelector('#inventory-summary');
  inventoryButton.disabled = true;
  scanStatus.textContent = 'Scanning local configuration metadata…';
  summary.replaceChildren();
  try {
    const response = await fetch('/v2/inventory/scan', {
      method: 'POST',
      headers: { accept: 'application/json' },
    });
    if (!response.ok) throw new Error(`Inventory scan failed (${response.status}).`);
    const data = await response.json();
    for (const [label, count] of [
      ['Configured runtimes', data.configured_runtime_count],
      ['Configuration files', data.config_file_count],
      ['MCP servers', data.mcp_server_count],
      ['Skill manifests', data.skill_manifest_count],
    ]) {
      const item = document.createElement('li');
      item.textContent = `${label}: ${count}`;
      summary.append(item);
    }
    for (const runtime of data.runtimes ?? []) {
      const item = document.createElement('li');
      const configScopes = formatInventoryScopes(runtime.config_files_by_scope);
      const skillScopes = formatInventoryScopes(runtime.skill_manifests_by_scope);
      item.textContent = `${runtime.runtime}: ${runtime.config_file_count} config file(s)${configScopes ? ` (${configScopes})` : ''}, ${runtime.mcp_server_count} MCP server(s), ${runtime.skill_manifest_count} skill manifest(s)${skillScopes ? ` (${skillScopes})` : ''}, ${runtime.incomplete_source_count} incomplete source(s)`;
      summary.append(item);
    }
    scanStatus.textContent = data.complete
      ? 'Scan complete. No paths or configuration values are shown.'
      : `Scan incomplete: ${data.incomplete_source_count} source(s) were unreadable, unsupported, or invalid.`;
  } catch (error) {
    scanStatus.textContent = error instanceof Error ? error.message : 'Inventory scan unavailable.';
  } finally {
    inventoryButton.disabled = false;
  }
}

function formatInventoryScopes(items) {
  return (items ?? []).map(({ scope, count }) => `${scope}: ${count}`).join(', ');
}

let metricCatalog = [];

function metricSvgElement(name, attributes = {}) {
  const element = document.createElementNS('http://www.w3.org/2000/svg', name);
  for (const [key, value] of Object.entries(attributes)) element.setAttribute(key, String(value));
  return element;
}

async function loadMetricCatalog() {
  metricCatalogButton.disabled = true;
  metricSelect.disabled = true;
  metricQueryButton.disabled = true;
  metricStatus.textContent = 'Reading the bounded local metric log window…';
  metricSelect.replaceChildren();
  try {
    const response = await fetch('/v1/metrics/catalog?limit=250', { headers: { accept: 'application/json' } });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || `Metric catalog failed (${response.status}).`);
    metricCatalog = Array.isArray(data.metrics) ? data.metrics : [];
    metricCatalog.forEach((metric, index) => {
      const option = document.createElement('option');
      option.value = String(index);
      option.textContent = `${metric.name} · ${metric.harness} · ${metric.aggregation}${metric.unit ? ` · ${metric.unit}` : ''}`;
      metricSelect.append(option);
    });
    metricSelect.disabled = metricCatalog.length === 0;
    metricQueryButton.disabled = metricCatalog.length === 0;
    metricStatus.textContent = metricCatalog.length
      ? `${metricCatalog.length} local metric series available${data.metrics_truncated ? '; catalog limit reached' : ''}${data.older_events_omitted ? '; older events omitted by the scan bound' : ''}${data.malformed_lines ? `; ${data.malformed_lines} malformed line(s)` : ''}.`
      : 'No safe, queryable metric series found in the bounded local log window.';
  } catch (error) {
    metricStatus.textContent = error instanceof Error ? error.message : 'Metric catalog unavailable.';
  } finally {
    metricCatalogButton.disabled = false;
  }
}

function renderMetricSeries(data) {
  const width = 720;
  const height = 260;
  const left = 58;
  const right = 16;
  const top = 14;
  const bottom = 34;
  const plotWidth = width - left - right;
  const plotHeight = height - top - bottom;
  const points = Array.isArray(data.points) ? data.points : [];
  metricChart.replaceChildren();
  metricChart.setAttribute('aria-label', `${data.name} metric time series, ${points.length} points`);
  for (let index = 0; index <= 4; index++) {
    const y = top + (plotHeight * index) / 4;
    metricChart.append(metricSvgElement('line', { x1: left, x2: width - right, y1: y, y2: y, class: 'metric-grid-line' }));
  }
  if (!points.length) {
    const empty = metricSvgElement('text', { x: left, y: top + 30, class: 'metric-axis-label' });
    empty.textContent = 'No numeric samples in this time range for the selected measure.';
    metricChart.append(empty);
    return;
  }
  const values = points.map((point) => point.value);
  const minimum = Math.min(...values);
  const maximum = Math.max(...values);
  const spread = maximum - minimum || Math.max(Math.abs(maximum) * .1, 1);
  const low = minimum - spread * .05;
  const high = maximum + spread * .05;
  const from = Date.parse(data.from);
  const to = Date.parse(data.to);
  const timeSpan = Math.max(1, to - from);
  for (const point of points) {
    const timestamp = Date.parse(point.timestamp);
    if (!Number.isFinite(timestamp) || !Number.isFinite(point.value)) continue;
    const x = left + ((timestamp - from) / timeSpan) * plotWidth;
    const y = top + ((high - point.value) / (high - low)) * plotHeight;
    const circle = metricSvgElement('circle', { cx: x, cy: y, r: 3.5, class: 'metric-point' });
    const title = metricSvgElement('title');
    title.textContent = `${point.timestamp} · ${point.value} ${point.unit || ''} · ${point.harness}`;
    circle.append(title);
    metricChart.append(circle);
  }
  const highLabel = metricSvgElement('text', { x: 2, y: top + 5, class: 'metric-axis-label' });
  highLabel.textContent = high.toPrecision(4);
  const lowLabel = metricSvgElement('text', { x: 2, y: height - bottom, class: 'metric-axis-label' });
  lowLabel.textContent = low.toPrecision(4);
  const firstTime = metricSvgElement('text', { x: left, y: height - 8, class: 'metric-axis-label' });
  firstTime.textContent = new Date(from).toLocaleString();
  const lastTime = metricSvgElement('text', { x: width - right, y: height - 8, 'text-anchor': 'end', class: 'metric-axis-label' });
  lastTime.textContent = new Date(to).toLocaleString();
  metricChart.append(highLabel, lowLabel, firstTime, lastTime);
}

async function queryMetricSeries() {
  const metric = metricCatalog[Number(metricSelect.value)];
  if (!metric) return;
  metricQueryButton.disabled = true;
  metricStatus.textContent = 'Querying local metric samples…';
  metricChart.replaceChildren();
  const to = new Date();
  const from = new Date(to.getTime() - Number(metricWindow.value) * 60 * 60 * 1000);
  const params = new URLSearchParams({
    name: metric.name,
    harness: metric.harness,
    unit: metric.unit,
    aggregation: metric.aggregation,
    measure: metricMeasure.value,
    from: from.toISOString(),
    to: to.toISOString(),
    limit: '1000',
  });
  try {
    const response = await fetch(`/v1/metrics/timeseries?${params.toString()}`, { headers: { accept: 'application/json' } });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || `Metric query failed (${response.status}).`);
    renderMetricSeries(data);
    metricStatus.textContent = `${data.points.length} of ${data.matched_points} point(s) shown${data.points_truncated ? '; point limit reached' : ''}${data.older_events_omitted ? '; older events omitted by the scan bound' : ''}${data.malformed_lines ? `; ${data.malformed_lines} malformed line(s)` : ''}. Metric attributes are not returned.`;
  } catch (error) {
    metricStatus.textContent = error instanceof Error ? error.message : 'Metric series unavailable.';
  } finally {
    metricQueryButton.disabled = false;
  }
}

form.addEventListener('submit', (event) => {
  event.preventDefault();
  refresh();
});

scanButton.addEventListener('click', runSecurityScan);
securityReviewButton.addEventListener('click', loadSecurityReviewHistory);
securityReviewPrevious.addEventListener('click', () => loadSecurityReviewHistory(securityReviewOffset - securityReviewPageSize));
securityReviewNext.addEventListener('click', () => loadSecurityReviewHistory(securityReviewOffset + securityReviewPageSize));
findingDetailsToggle.addEventListener('change', () => {
  if (!findingDetailsToggle.checked) {
    document.querySelector('#security-finding-details').replaceChildren();
  }
});
inventoryButton.addEventListener('click', runInventoryScan);
metricCatalogButton.addEventListener('click', loadMetricCatalog);
metricQueryButton.addEventListener('click', queryMetricSeries);

refresh();
