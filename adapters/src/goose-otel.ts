import { lstat, open } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { resolveThreadforgeTracePath } from './openclaw-otel.ts';
import { scanLocalOtelEventLog } from './local-otel-status.ts';

const defaultEndpoint = 'http://127.0.0.1:4318';
const maxConfigBytes = 2 << 20;
const maxValidationWindowMs = 30 * 24 * 60 * 60_000;

export interface GooseOtelStatusOptions {
  configPath?: string;
  logPath?: string;
  env?: NodeJS.ProcessEnv;
  homeDir?: string;
  platform?: NodeJS.Platform;
  now?: Date;
}

export interface GooseOtelStatus {
  schema_version: 'threadforge.goose-otel-status.v1';
  inspection_complete: boolean;
  configured_endpoint: 'missing' | 'loopback_http' | 'remote_http' | 'invalid';
  timeout_ms?: number;
  environment: {
    endpoint_override: 'unset' | 'matches_config' | 'differs' | 'invalid';
    sdk_disabled: 'unset' | 'true' | 'false' | 'invalid';
    protocol: 'unset' | 'http' | 'grpc' | 'other';
  };
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

/** Print-only local OTLP guidance. Threadforge never edits Goose's user-owned config. */
export function renderGooseOtelConfig(endpoint = defaultEndpoint): string {
  const normalized = validateLoopbackEndpoint(endpoint);
  return [
    '# Merge these keys into the existing Goose config.yaml; preserve all other settings.',
    `otel_exporter_otlp_endpoint: ${JSON.stringify(normalized)}`,
    'otel_exporter_otlp_timeout: 10000',
    '',
    '# This targets the local OTLP/HTTP receiver on port 4318 by default.',
    '# Threadforge does not enable message-content capture or write Goose configuration.',
    '# Inspect OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_SDK_DISABLED, and OTEL_EXPORTER_OTLP_PROTOCOL',
    '# in the environment that launches Goose; they can override or disable this config.',
  ].join('\n');
}

/** Read config and local JSONL evidence without exposing config values, paths, or event bodies. */
export async function getGooseOtelStatus(options: GooseOtelStatusOptions = {}): Promise<GooseOtelStatus> {
  const env = options.env ?? process.env;
  const homeDir = options.homeDir ?? os.homedir();
  const platform = options.platform ?? process.platform;
  const configPath = options.configPath ?? path.join(homeDir, '.config', 'goose', 'config.yaml');
  const logPath = options.logPath ?? resolveThreadforgeTracePath(env, homeDir, platform);
  const issues = new Set<string>();
  const config = await inspectGooseConfig(configPath, issues);
  const environment = inspectGooseEnvironment(env, config.endpoint);
  for (const issue of environment.issues) issues.add(issue);
  const telemetry = await scanLocalOtelEventLog(logPath, 'goose', options.now ?? new Date());
  if (!telemetry.present) issues.add('event_log_missing');
  if (!telemetry.complete) issues.add('event_log_incomplete');
  if (!telemetry.last_event_observed) issues.add('no_goose_otlp_events');

  return {
    schema_version: 'threadforge.goose-otel-status.v1',
    inspection_complete: config.inspected && telemetry.complete,
    configured_endpoint: config.endpointClass,
    ...(config.timeoutMs !== undefined ? { timeout_ms: config.timeoutMs } : {}),
    environment: environment.summary,
    issues: [...issues],
    telemetry,
  };
}

export function validateGooseOtelStatus(status: GooseOtelStatus, sinceMs = 10 * 60_000, now = new Date()): {
  schema_version: 'threadforge.goose-otel-validation.v1';
  valid: boolean;
  since_ms: number;
  issues: string[];
  status: GooseOtelStatus;
} {
  if (!Number.isSafeInteger(sinceMs) || sinceMs < 1 || sinceMs > maxValidationWindowMs) {
    throw new Error('Goose OTLP validation window must be between 1 ms and 30 days');
  }
  const issues = new Set(status.issues);
  const timestamp = status.telemetry.last_event_observed_at
    ? Date.parse(status.telemetry.last_event_observed_at)
    : Number.NaN;
  if (!Number.isFinite(timestamp) || timestamp < now.getTime() - sinceMs || timestamp > now.getTime() + 60_000) {
    issues.add('no_recent_goose_otlp_event');
  }
  const valid = status.inspection_complete && status.configured_endpoint === 'loopback_http' && issues.size === 0;
  return { schema_version: 'threadforge.goose-otel-validation.v1', valid, since_ms: sinceMs, issues: [...issues], status };
}

export function parseGooseOtelDuration(value: string): number {
  const match = /^(\d+)(ms|s|m|h|d)$/.exec(value.trim());
  if (!match) throw new Error('duration must use ms, s, m, h, or d units');
  const multiplier: Record<string, number> = { ms: 1, s: 1000, m: 60_000, h: 3_600_000, d: 86_400_000 };
  const result = Number(match[1]) * multiplier[match[2]!];
  if (!Number.isSafeInteger(result) || result < 1 || result > maxValidationWindowMs) {
    throw new Error('duration must be between 1 ms and 30 days');
  }
  return result;
}

interface ConfigInspection {
  inspected: boolean;
  endpoint?: string;
  endpointClass: GooseOtelStatus['configured_endpoint'];
  timeoutMs?: number;
}

async function inspectGooseConfig(filePath: string, issues: Set<string>): Promise<ConfigInspection> {
  let handle;
  try {
    const pathInfo = await lstat(filePath);
    if (pathInfo.isSymbolicLink() || !pathInfo.isFile()) {
      issues.add(pathInfo.isSymbolicLink() ? 'config_symlink_refused' : 'config_not_regular_file');
      return { inspected: false, endpointClass: 'invalid' };
    }
    if (pathInfo.size > maxConfigBytes) {
      issues.add('config_oversized');
      return { inspected: false, endpointClass: 'invalid' };
    }
    handle = await open(filePath, 'r');
    const openedInfo = await handle.stat();
    const latestPathInfo = await lstat(filePath);
    if (latestPathInfo.isSymbolicLink() || !latestPathInfo.isFile() || openedInfo.size > maxConfigBytes) {
      issues.add('config_changed_or_symlinked');
      return { inspected: false, endpointClass: 'invalid' };
    }
    const bytes = Buffer.alloc(openedInfo.size);
    const { bytesRead } = await handle.read(bytes, 0, bytes.length, 0);
    let content: string;
    try { content = new TextDecoder('utf-8', { fatal: true }).decode(bytes.subarray(0, bytesRead)); }
    catch {
      issues.add('config_invalid_utf8');
      return { inspected: false, endpointClass: 'invalid' };
    }
    const values = readKnownYamlScalars(content);
    if (values.issues.length) {
      for (const issue of values.issues) issues.add(issue);
      return { inspected: false, endpointClass: 'invalid' };
    }
    const endpoint = values.values.get('otel_exporter_otlp_endpoint');
    const rawTimeout = values.values.get('otel_exporter_otlp_timeout');
    if (!endpoint) issues.add('endpoint_missing');
    let endpointClass: GooseOtelStatus['configured_endpoint'] = endpoint ? classifyEndpoint(endpoint) : 'missing';
    if (endpointClass === 'remote_http') issues.add('remote_endpoint_not_allowed_by_local_default');
    if (endpointClass === 'invalid') issues.add('endpoint_invalid');
    let timeoutMs: number | undefined;
    if (rawTimeout !== undefined) {
      if (/^\d+$/.test(rawTimeout) && Number.isSafeInteger(Number(rawTimeout)) && Number(rawTimeout) > 0 && Number(rawTimeout) <= 120_000) {
        timeoutMs = Number(rawTimeout);
      } else issues.add('timeout_invalid');
    }
    return { inspected: true, ...(endpoint ? { endpoint } : {}), endpointClass, ...(timeoutMs !== undefined ? { timeoutMs } : {}) };
  } catch (error) {
    if (isCode(error, 'ENOENT')) {
      issues.add('config_missing');
      return { inspected: false, endpointClass: 'missing' };
    }
    issues.add('config_unreadable');
    return { inspected: false, endpointClass: 'invalid' };
  } finally {
    await handle?.close();
  }
}

function readKnownYamlScalars(content: string): { values: Map<string, string>; issues: string[] } {
  const wanted = new Set(['otel_exporter_otlp_endpoint', 'otel_exporter_otlp_timeout']);
  const values = new Map<string, string>();
  const issues = new Set<string>();
  for (const line of content.replace(/^\uFEFF/, '').split(/\r?\n/)) {
    if (!line.trim() || line.trimStart().startsWith('#') || /^\s/.test(line)) continue;
    const match = /^([A-Za-z_][A-Za-z0-9_-]*)\s*:\s*(.*?)\s*$/.exec(line);
    if (!match || !wanted.has(match[1]!)) continue;
    const key = match[1]!;
    if (values.has(key)) {
      issues.add('config_duplicate_otel_key');
      continue;
    }
    const scalar = parseYamlScalar(match[2]!);
    if (scalar === undefined) issues.add('config_unsupported_otel_value');
    else values.set(key, scalar);
  }
  return { values, issues: [...issues] };
}

function parseYamlScalar(value: string): string | undefined {
  const uncommented = stripYamlComment(value).trim();
  if (!uncommented || ['|', '>', 'null', '~'].includes(uncommented) || uncommented.startsWith('[') || uncommented.startsWith('{')) return undefined;
  if (uncommented.startsWith('"')) {
    try {
      const parsed: unknown = JSON.parse(uncommented);
      return typeof parsed === 'string' ? parsed : undefined;
    } catch { return undefined; }
  }
  if (uncommented.startsWith("'")) {
    if (!uncommented.endsWith("'") || uncommented.length < 2) return undefined;
    return uncommented.slice(1, -1).replaceAll("''", "'");
  }
  return uncommented;
}

function stripYamlComment(value: string): string {
  let quote: 'single' | 'double' | undefined;
  for (let index = 0; index < value.length; index++) {
    const current = value[index];
    if (quote === 'double' && current === '\\') { index++; continue; }
    if (quote === 'single' && current === "'" && value[index + 1] === "'") { index++; continue; }
    if (quote === 'double' && current === '"' || quote === 'single' && current === "'") { quote = undefined; continue; }
    if (!quote && current === '"') quote = 'double';
    else if (!quote && current === "'") quote = 'single';
    else if (!quote && current === '#' && (index === 0 || /\s/.test(value[index - 1]!))) return value.slice(0, index);
  }
  return value;
}

function classifyEndpoint(value: string): GooseOtelStatus['configured_endpoint'] {
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
    throw new Error('Goose OTLP endpoint must be a loopback HTTP base URL without credentials, path, query, or fragment');
  }
  return new URL(value).origin;
}

function inspectGooseEnvironment(env: NodeJS.ProcessEnv, configEndpoint?: string): {
  summary: GooseOtelStatus['environment'];
  issues: string[];
} {
  const issues = new Set<string>();
  const override = env.OTEL_EXPORTER_OTLP_ENDPOINT?.trim();
  let endpointOverride: GooseOtelStatus['environment']['endpoint_override'] = 'unset';
  if (override) {
    const overrideClass = classifyEndpoint(override);
    if (overrideClass === 'invalid') endpointOverride = 'invalid';
    else if (configEndpoint && normalizeEndpoint(override) === normalizeEndpoint(configEndpoint)) endpointOverride = 'matches_config';
    else endpointOverride = 'differs';
    if (endpointOverride !== 'matches_config') issues.add('environment_endpoint_override');
  }

  const disabledValue = env.OTEL_SDK_DISABLED?.trim().toLowerCase();
  let sdkDisabled: GooseOtelStatus['environment']['sdk_disabled'] = 'unset';
  if (disabledValue === 'true' || disabledValue === '1') {
    sdkDisabled = 'true';
    issues.add('otel_sdk_disabled');
  } else if (disabledValue === 'false' || disabledValue === '0') sdkDisabled = 'false';
  else if (disabledValue) {
    sdkDisabled = 'invalid';
    issues.add('otel_sdk_disabled_value_invalid');
  }

  const rawProtocol = env.OTEL_EXPORTER_OTLP_PROTOCOL?.trim().toLowerCase();
  let protocol: GooseOtelStatus['environment']['protocol'] = 'unset';
  if (rawProtocol) {
    if (rawProtocol === 'grpc' || rawProtocol.startsWith('grpc/')) {
      protocol = 'grpc';
      issues.add('grpc_protocol_override');
    } else if (rawProtocol === 'http/protobuf' || rawProtocol === 'http/json') protocol = 'http';
    else {
      protocol = 'other';
      issues.add('protocol_override_unknown');
    }
  }
  return { summary: { endpoint_override: endpointOverride, sdk_disabled: sdkDisabled, protocol }, issues: [...issues] };
}

function normalizeEndpoint(value: string): string | undefined {
  try { return new URL(value).origin; } catch { return undefined; }
}

function isRecord(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
