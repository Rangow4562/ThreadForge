// Options UI: per-site enable controls; the receiver remains loopback-only.
import { ext } from '../shared/browser.js';
import { mountPermissionBanner } from '../shared/permissions.js';
import type { Settings } from '../shared/types.js';

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;

async function load(): Promise<void> {
  const { settings } = (await ext.runtime.sendMessage({ type: 'GET_STATUS' })) as {
    settings: Settings;
  };
  $('endpoint').textContent = settings.endpoint;
  ($('site_claude_web') as HTMLInputElement).checked = settings.sites.claude_web !== false;
  ($('site_chatgpt_web') as HTMLInputElement).checked = settings.sites.chatgpt_web !== false;
}

$('save').addEventListener('click', async () => {
  const patch: Partial<Settings> = {
    sites: {
      claude_web: ($('site_claude_web') as HTMLInputElement).checked,
      chatgpt_web: ($('site_chatgpt_web') as HTMLInputElement).checked,
    },
  };
  const error = $('error');
  try {
    await ext.runtime.sendMessage({ type: 'SET_SETTINGS', patch });
    error.hidden = true;
    const saved = $('saved');
    saved.hidden = false;
    setTimeout(() => (saved.hidden = true), 1500);
  } catch {
    error.textContent = 'Settings were not saved. Check the selected sites and try again.';
    error.hidden = false;
  }
});

void load();
mountPermissionBanner(ext, document);
