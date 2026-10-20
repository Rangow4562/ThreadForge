import { DEFAULT_SETTINGS, type Settings, type SettingsPatch, type SiteName } from './types.js';
import { assertLocalThreadforgeLogsEndpoint, isLocalThreadforgeLogsEndpoint } from './endpoint.js';

export function normalizeSettings(raw: unknown): Settings {
  const stored = isRecord(raw) ? raw : {};
  const sites = isRecord(stored.sites) ? stored.sites : {};
  const endpoint = isLocalThreadforgeLogsEndpoint(stored.endpoint) ? stored.endpoint : undefined;
  if (endpoint === undefined) return { ...DEFAULT_SETTINGS, sites: { ...DEFAULT_SETTINGS.sites } };
  return {
    enabled: stored.enabled === true,
    retention: stored.retention === 'redacted' || stored.retention === 'full' ? stored.retention : 'metadata',
    endpoint,
    sites: {
      claude_web: sites.claude_web === true,
      chatgpt_web: sites.chatgpt_web === true,
    },
  };
}

export function mergeSettings(current: Settings, patch: SettingsPatch): Settings {
  if (!isRecord(patch)) throw new Error('settings patch must be an object');
  const unexpected = Object.keys(patch).find((key) => !['enabled', 'retention', 'endpoint', 'sites'].includes(key));
  if (unexpected) throw new Error('settings patch contains an unsupported field');
  if (patch.enabled !== undefined && typeof patch.enabled !== 'boolean') throw new Error('enabled must be boolean');
  if (patch.retention !== undefined && patch.retention !== 'metadata' && patch.retention !== 'redacted' && patch.retention !== 'full') {
    throw new Error('retention must be metadata, redacted, or full');
  }
  if (patch.endpoint !== undefined) assertLocalThreadforgeLogsEndpoint(patch.endpoint);
  const patchSites = patch.sites as unknown;
  if (patchSites !== undefined && (!isRecord(patchSites) ||
      Object.entries(patchSites).some(([name, enabled]) =>
        (name !== 'claude_web' && name !== 'chatgpt_web') || typeof enabled !== 'boolean'))) {
    throw new Error('sites must contain only boolean claude_web and chatgpt_web values');
  }
  return {
    ...current,
    ...(patch.enabled !== undefined ? { enabled: patch.enabled } : {}),
    ...(patch.retention !== undefined ? { retention: patch.retention } : {}),
    ...(patch.endpoint !== undefined ? { endpoint: patch.endpoint } : {}),
    sites: { ...current.sites, ...(patchSites as Partial<Record<SiteName, boolean>> | undefined) },
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
