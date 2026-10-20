import { lstat, open } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { resolveThreadforgeTracePath } from './openclaw-otel.ts';
import { scanLocalOtelEventLog } from './local-otel-status.ts';

const defaultEndpoint = 'http://127.0.0.1:4318';
const maxSettingsBytes = 2 << 20;
const maxValidationWindowMs = 30 * 24 * 60 * 60_000;

export interface VSCodeOtelStatusOptions {
  settingsPath?: string;
  logPath?: string;
  env?: NodeJS.ProcessEnv;
  homeDir?: string;
  platform?: NodeJS.Platform;
  now?: Date;
}

export interface VSCodeOtelStatus {
  schema_version: 'threadforge.vscode-otel-status.v1';
  inspection_complete: boolean;
  telemetry_enabled: 'enabled' | 'disabled' | 'unset' | 'invalid';
  exporter_type: 'otlp_http' | 'file' | 'other' | 'unset';
  configured_endpoint: 'missing' | 'default_loopback_http' | 'loopback_http' | 'remote_http' | 'invalid';
  content_capture: 'enabled' | 'disabled' | 'unset' | 'invalid';
  warnings: string[];
  issues: string[];
  telemetry: {
    present: boolean;
    complete: boolean;
    older_events_omitted: boolean;
    malformed_lines: number;
    observed_events: number;
    last_event_observed: boolean;
    last_event_observed_at?: string;
  };
}

/** Print a VS Code Copilot Chat OTLP/HTTP settings fragment without changing VS Code settings. */
export function renderVSCodeOtelConfig(endpoint = defaultEndpoint): string {
  const normalized = validateLoopbackEndpoint(endpoint);
  return [
    'Merge these keys into existing VS Code User or Workspace settings; preserve unrelated JSONC settings:',
    '{',
    '  "github.copilot.chat.otel.enabled": true,',
    '  "github.copilot.chat.otel.exporterType": "otlp-http",',
    `  "github.copilot.chat.otel.otlpEndpoint": ${JSON.stringify(normalized)},`,
    '  "github.copilot.chat.otel.captureContent": false,',
    '  "github.copilot.chat.otel.maxAttributeSizeChars": 8192',
    '}',
    '',
    'The endpoint must be local. This fragment does not install hooks, edit settings, or capture message content.',
    'Reload VS Code after a manual settings change; check status and local event validation afterward.',
  ].join('\n');
}

export function resolveVSCodeSettingsPath(
  env: NodeJS.ProcessEnv = process.env,
  homeDir = os.homedir(),
  platform: NodeJS.Platform = process.platform,
): string {
  const pathApi = platform === 'win32' ? path.win32 : path.posix;
  if (platform === 'win32') {
    const appData = env.APPDATA?.trim() || pathApi.join(homeDir, 'AppData', 'Roaming');
    return pathApi.join(appData, 'Code', 'User', 'settings.json');
  }
  if (platform === 'darwin') return pathApi.join(homeDir, 'Library', 'Application Support', 'Code', 'User', 'settings.json');
  const configHome = env.XDG_CONFIG_HOME?.trim() || pathApi.join(homeDir, '.config');
  return pathApi.join(configHome, 'Code', 'User', 'settings.json');
}

/** Read VS Code JSONC settings and a bounded local event log without exposing either file's data. */
export async function getVSCodeOtelStatus(options: VSCodeOtelStatusOptions = {}): Promise<VSCodeOtelStatus> {
  const env = options.env ?? process.env;
  const homeDir = options.homeDir ?? os.homedir();
  const platform = options.platform ?? process.platform;
  const settingsPath = options.settingsPath ?? resolveVSCodeSettingsPath(env, homeDir, platform);
  const logPath = options.logPath ?? resolveThreadforgeTracePath(env, homeDir, platform);
  const issues = new Set<string>();
  const warnings = new Set<string>();
  const settingsResult = await readVSCodeSettings(settingsPath, issues);
  const settings = settingsResult.settings;
  const telemetryEnabled = parseBooleanSetting(settings, 'github.copilot.chat.otel.enabled');
  const exporterSetting = settings['github.copilot.chat.otel.exporterType'];
  const rawExporter = typeof exporterSetting === 'string' ? exporterSetting.trim() : '';
  const outfileSetting = settings['github.copilot.chat.otel.outfile'];
  const outfile = typeof outfileSetting === 'string' ? outfileSetting.trim() : '';
  const endpointSetting = settings['github.copilot.chat.otel.otlpEndpoint'];
  const endpointValue = typeof endpointSetting === 'string' ? endpointSetting.trim() : '';
  const exporterType: VSCodeOtelStatus['exporter_type'] =
    (exporterSetting !== undefined && typeof exporterSetting !== 'string') || (outfileSetting !== undefined && typeof outfileSetting !== 'string')
      ? 'other'
      : rawExporter === 'otlp-http' || !rawExporter && !outfile
        ? rawExporter ? 'otlp_http' : 'unset'
        : rawExporter === 'file' || outfile ? 'file' : 'other';
  const configuredEndpoint = endpointSetting !== undefined && typeof endpointSetting !== 'string'
    ? 'invalid'
    : endpointValue ? classifyEndpoint(endpointValue)
      : telemetryEnabled === 'enabled' ? 'default_loopback_http' : 'missing';
  const contentCapture = parseBooleanSetting(settings, 'github.copilot.chat.otel.captureContent');

  if (!settingsResult.inspected) issues.add(settingsResult.missing ? 'settings_missing' : 'settings_unavailable');
  if (telemetryEnabled === 'disabled' || telemetryEnabled === 'unset') issues.add('otel_not_enabled');
  if (telemetryEnabled === 'invalid') issues.add('otel_enabled_value_invalid');
  if (exporterType === 'file') issues.add('file_exporter_bypasses_local_otlp');
  else if (exporterType === 'other') issues.add('exporter_type_unsupported');
  if (outfileSetting !== undefined && typeof outfileSetting !== 'string') issues.add('file_exporter_setting_invalid');
  if (endpointSetting !== undefined && typeof endpointSetting !== 'string') issues.add('endpoint_value_invalid');
  if (configuredEndpoint === 'remote_http') issues.add('remote_endpoint_not_allowed_by_local_default');
  if (configuredEndpoint === 'invalid') issues.add('endpoint_invalid');
  if (configuredEndpoint === 'missing') issues.add('endpoint_missing');
  if (contentCapture === 'enabled') warnings.add('message_content_capture_enabled');
  if (contentCapture === 'invalid') issues.add('content_capture_value_invalid');

  const telemetry = await scanLocalOtelEventLog(logPath, 'vscode_copilot', options.now ?? new Date());
  if (!telemetry.present) issues.add('event_log_missing');
  if (!telemetry.complete) issues.add('event_log_incomplete');
  if (!telemetry.last_event_observed) issues.add('no_vscode_copilot_otlp_events');
  return {
    schema_version: 'threadforge.vscode-otel-status.v1',
    inspection_complete: settingsResult.inspected && telemetry.complete,
    telemetry_enabled: telemetryEnabled,
    exporter_type: exporterType,
    configured_endpoint: configuredEndpoint,
    content_capture: contentCapture,
    warnings: [...warnings],
    issues: [...issues],
    telemetry,
  };
}

export function validateVSCodeOtelStatus(status: VSCodeOtelStatus, sinceMs = 10 * 60_000, now = new Date()): {
  schema_version: 'threadforge.vscode-otel-validation.v1';
  valid: boolean;
  since_ms: number;
  issues: string[];
  warnings: string[];
  status: VSCodeOtelStatus;
} {
  if (!Number.isSafeInteger(sinceMs) || sinceMs < 1 || sinceMs > maxValidationWindowMs) {
    throw new Error('VS Code OTLP validation window must be between 1 ms and 30 days');
  }
  const issues = new Set(status.issues);
  const timestamp = status.telemetry.last_event_observed_at ? Date.parse(status.telemetry.last_event_observed_at) : Number.NaN;
  if (!Number.isFinite(timestamp) || timestamp < now.getTime() - sinceMs || timestamp > now.getTime() + 60_000) {
    issues.add('no_recent_vscode_copilot_otlp_event');
  }
  const valid = status.inspection_complete && status.telemetry_enabled === 'enabled' &&
    ['default_loopback_http', 'loopback_http'].includes(status.configured_endpoint) &&
    ['otlp_http', 'unset'].includes(status.exporter_type) && issues.size === 0;
  return { schema_version: 'threadforge.vscode-otel-validation.v1', valid, since_ms: sinceMs, issues: [...issues], warnings: status.warnings, status };
}

export function parseVSCodeOtelDuration(value: string): number {
  const match = /^(\d+)(ms|s|m|h|d)$/.exec(value.trim());
  if (!match) throw new Error('duration must use ms, s, m, h, or d units');
  const multiplier: Record<string, number> = { ms: 1, s: 1000, m: 60_000, h: 3_600_000, d: 86_400_000 };
  const result = Number(match[1]) * multiplier[match[2]!];
  if (!Number.isSafeInteger(result) || result < 1 || result > maxValidationWindowMs) {
    throw new Error('duration must be between 1 ms and 30 days');
  }
  return result;
}

async function readVSCodeSettings(filePath: string, issues: Set<string>): Promise<{ inspected: boolean; missing: boolean; settings: Record<string, unknown> }> {
  let handle;
  try {
    const pathInfo = await lstat(filePath);
    if (pathInfo.isSymbolicLink() || !pathInfo.isFile()) {
      issues.add(pathInfo.isSymbolicLink() ? 'settings_symlink_refused' : 'settings_not_regular_file');
      return { inspected: false, missing: false, settings: {} };
    }
    if (pathInfo.size > maxSettingsBytes) {
      issues.add('settings_oversized');
      return { inspected: false, missing: false, settings: {} };
    }
    handle = await open(filePath, 'r');
    const info = await handle.stat();
    const afterOpen = await lstat(filePath);
    if (afterOpen.isSymbolicLink() || !afterOpen.isFile() || info.size > maxSettingsBytes) {
      issues.add('settings_changed_or_symlinked');
      return { inspected: false, missing: false, settings: {} };
    }
    const bytes = Buffer.alloc(info.size);
    const { bytesRead } = await handle.read(bytes, 0, bytes.length, 0);
    let text: string;
    try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes.subarray(0, bytesRead)); }
    catch {
      issues.add('settings_invalid_utf8');
      return { inspected: false, missing: false, settings: {} };
    }
    try {
      const value: unknown = JSON.parse(stripJsonc(text));
      if (!isRecord(value)) throw new Error('settings root is not an object');
      return { inspected: true, missing: false, settings: value };
    } catch {
      issues.add('settings_invalid_jsonc');
      return { inspected: false, missing: false, settings: {} };
    }
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { inspected: false, missing: true, settings: {} };
    issues.add('settings_unreadable');
    return { inspected: false, missing: false, settings: {} };
  } finally {
    await handle?.close();
  }
}

function stripJsonc(input: string): string {
  let output = '';
  let inString = false;
  let escaped = false;
  let lineComment = false;
  let blockComment = false;
  for (let index = 0; index < input.length; index++) {
    const current = input[index]!;
    const next = input[index + 1];
    if (lineComment) {
      if (current === '\n' || current === '\r') { lineComment = false; output += current; }
      else output += ' ';
      continue;
    }
    if (blockComment) {
      if (current === '*' && next === '/') { output += '  '; index++; blockComment = false; }
      else output += current === '\n' || current === '\r' ? current : ' ';
      continue;
    }
    if (inString) {
      output += current;
      if (escaped) escaped = false;
      else if (current === '\\') escaped = true;
      else if (current === '"') inString = false;
      continue;
    }
    if (current === '"') { inString = true; output += current; continue; }
    if (current === '/' && next === '/') { output += '  '; index++; lineComment = true; continue; }
    if (current === '/' && next === '*') { output += '  '; index++; blockComment = true; continue; }
    output += current;
  }
  if (blockComment) throw new Error('VS Code settings contain an unterminated JSONC comment');
  let clean = '';
  inString = false;
  escaped = false;
  for (let index = 0; index < output.length; index++) {
    const current = output[index]!;
    if (inString) {
      clean += current;
      if (escaped) escaped = false;
      else if (current === '\\') escaped = true;
      else if (current === '"') inString = false;
      continue;
    }
    if (current === '"') { inString = true; clean += current; continue; }
    if (current === ',') {
      let next = index + 1;
      while (next < output.length && /\s/.test(output[next]!)) next++;
      if (output[next] === '}' || output[next] === ']') continue;
    }
    clean += current;
  }
  return clean;
}

function parseBooleanSetting(settings: Record<string, unknown>, key: string): VSCodeOtelStatus['telemetry_enabled'] | VSCodeOtelStatus['content_capture'] {
  const value = settings[key];
  if (value === undefined) return 'unset';
  if (value === true || value === 'true' || value === 1 || value === '1') return 'enabled';
  if (value === false || value === 'false' || value === 0 || value === '0') return 'disabled';
  return 'invalid';
}

function classifyEndpoint(value: string): VSCodeOtelStatus['configured_endpoint'] {
  try {
    const url = new URL(value);
    const host = url.hostname.toLowerCase().replace(/^\[|\]$/g, '');
    const ipv4Loopback = /^127(?:\.\d{1,3}){3}$/.test(host) && host.split('.').every((part) => Number(part) <= 255);
    const loopback = host === 'localhost' || host.endsWith('.localhost') || host === '::1' || ipv4Loopback;
    if (url.protocol !== 'http:' || url.username || url.password || url.search || url.hash || url.pathname !== '/') return 'invalid';
    return loopback ? 'loopback_http' : 'remote_http';
  } catch { return 'invalid'; }
}

function validateLoopbackEndpoint(value: string): string {
  if (classifyEndpoint(value) !== 'loopback_http') {
    throw new Error('VS Code OTLP endpoint must be a loopback HTTP base URL without credentials, path, query, or fragment');
  }
  return new URL(value).origin;
}
function isRecord(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
