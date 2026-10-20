// Popup UI: toggle capture, choose retention, show live status.
import { ext } from '../shared/browser.js';
import { mountPermissionBanner } from '../shared/permissions.js';
import type { Settings } from '../shared/types.js';

interface Status {
  settings: Settings;
  queueDepth: number;
  droppedCount: number;
  lastDeliveryIssue: string | null;
  active: number;
}

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

async function refresh(): Promise<void> {
  const status = (await ext.runtime.sendMessage({ type: 'GET_STATUS' })) as Status;
  ($('enabled') as HTMLInputElement).checked = status.settings.enabled;
  ($('retention') as HTMLSelectElement).value = status.settings.retention;
  $('endpoint').textContent = status.settings.endpoint;
  $('queue').textContent = String(status.queueDepth);
  $('dropped').textContent = String(status.droppedCount);
  $('delivery-issue').textContent = status.lastDeliveryIssue ?? 'none';
  $('active').textContent = String(status.active);
}

$('enabled').addEventListener('change', async (e) => {
  await ext.runtime.sendMessage({
    type: 'SET_SETTINGS',
    patch: { enabled: (e.target as HTMLInputElement).checked },
  });
  await refresh();
});

$('retention').addEventListener('change', async (e) => {
  await ext.runtime.sendMessage({
    type: 'SET_SETTINGS',
    patch: { retention: (e.target as HTMLSelectElement).value as Settings['retention'] },
  });
  await refresh();
});

void refresh();
mountPermissionBanner(ext, document);

const clearQueue = document.getElementById('clear-queue') as HTMLButtonElement | null;
clearQueue?.addEventListener('click', async () => {
  if (!window.confirm('Permanently clear locally queued telemetry?')) return;
  await ext.runtime.sendMessage({ type: 'CLEAR_QUEUE' });
  await refresh();
});
