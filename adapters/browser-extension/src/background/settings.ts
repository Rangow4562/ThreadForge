// Storage-backed settings with defaults. Lives in chrome.storage.local so it
// survives service-worker suspension.

import { ext } from '../shared/browser.js';
import { type Settings, type SettingsPatch, type SiteName } from '../shared/types.js';
import { mergeSettings, normalizeSettings } from '../shared/settings-schema.js';

const KEY = 'settings';

export async function getSettings(): Promise<Settings> {
  const got = await ext.storage.local.get(KEY);
  return normalizeSettings(got[KEY]);
}

export async function saveSettings(patch: SettingsPatch): Promise<Settings> {
  const current = await getSettings();
  const next = mergeSettings(current, patch);
  await ext.storage.local.set({ [KEY]: next });
  return next;
}

export function siteEnabled(settings: Settings, site: SiteName): boolean {
  return settings.enabled && settings.sites[site] !== false;
}
