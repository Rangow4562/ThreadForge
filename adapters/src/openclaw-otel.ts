import { open, stat } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

const maxStatusTailBytes = 16 << 20;
const maxStatusLineBytes = 64 << 10;
const defaultEndpoint = 'http://127.0.0.1:4318';

export interface OpenClawOtelConfigOptions {
  endpoint?: string;
  captureContent?: boolean;
  allowRemote?: boolean;
}

export interface OpenClawOtelStatusOptions {
  logPath?: string;
  env?: NodeJS.ProcessEnv;
  homeDir?: string;
  platform?: NodeJS.Platform;
  now?: Date;
}

export interface OpenClawOtelStatus {
  schema_version: 'threadforge.openclaw-otel-status.v1';
  present: boolean;
  complete: boolean;
  older_events_omitted: boolean;
  malformed_lines: number;
  observed_events: number;
  last_event_observed: boolean;
  last_event_observed_at?: string;
}

export function renderOpenClawOtelConfig(options: OpenClawOtelConfigOptions = {}): string {
  const endpoint = validateEndpoint(options.endpoint ?? defaultEndpoint, options.allowRemote ?? false);
  const captureContent = options.captureContent === true;
  const rows = [
    'Install/enable OpenClaw diagnostics-otel using the OpenClaw CLI:',
    '  openclaw plugins install @openclaw/diagnostics-otel',
    '  openclaw plugins enable diagnostics-otel',
    '',
    'Merge this fragment into the existing OpenClaw JSON5 config; do not replace other diagnostics, plugin, or allowlist entries:',
    '{',
    '  diagnostics: {',
    '    enabled: true,',
    '    otel: {',
    '      enabled: true,',
    `      endpoint: ${JSON.stringify(endpoint)},`,
    `      tracesEndpoint: ${JSON.stringify(signalEndpoint(endpoint, 'traces'))},`,
    `      metricsEndpoint: ${JSON.stringify(signalEndpoint(endpoint, 'metrics'))},`,
    `      logsEndpoint: ${JSON.stringify(signalEndpoint(endpoint, 'logs'))},`,
    '      protocol: "http/protobuf",',
    '      serviceName: "openclaw-gateway",',
    '      traces: true,',
    '      metrics: true,',
    '      logs: true,',
    '      logsExporter: "otlp",',
    '      sampleRate: 1.0,',
    '      flushIntervalMs: 60000,',
    `      captureContent: ${captureContent},`,
    '    },',
    '  },',
    '}',
    '',
    'Capture content is off unless --capture-content is explicitly selected. Check existing per-signal OTLP endpoints and environment overrides before enabling export.',
  ];
  if (options.allowRemote) rows.push('WARNING: remote TLS export was explicitly enabled; Threadforge does not manage or retain the remote credentials.');
  if (captureContent) rows.push('WARNING: OpenClaw may export bounded, redacted prompts, responses, tool inputs/outputs, and tool definitions. System prompts remain excluded by OpenClaw.');
  return rows.join('\n');
}

export function resolveThreadforgeTracePath(
  env: NodeJS.ProcessEnv = process.env,
  homeDir = os.homedir(),
  platform: NodeJS.Platform = process.platform,
): string {
  if (env.THREADFORGE_TRACE_FILE?.trim()) return env.THREADFORGE_TRACE_FILE.trim();
  const pathApi = platform === 'win32' ? path.win32 : path.posix;
  let root = env.THREADFORGE_HOME?.trim();
  if (!root) {
    if (platform === 'win32') root = pathApi.join(env.LOCALAPPDATA?.trim() || pathApi.join(homeDir, 'AppData', 'Local'), 'threadforge');
    else if (platform === 'darwin') root = pathApi.join(homeDir, 'Library', 'Caches', 'threadforge');
    else root = pathApi.join(env.XDG_CACHE_HOME?.trim() || pathApi.join(homeDir, '.cache'), 'threadforge');
  }
  return pathApi.join(root, 'traces', 'events.jsonl');
}

/** Scans a bounded local JSONL tail and returns aggregate status only, never event bodies or paths. */
export async function getOpenClawOtelStatus(options: OpenClawOtelStatusOptions = {}): Promise<OpenClawOtelStatus> {
  const logPath = options.logPath ?? resolveThreadforgeTracePath(options.env, options.homeDir, options.platform);
  let file;
  try { file = await open(logPath, 'r'); }
  catch (error) {
    if (isCode(error, 'ENOENT')) return emptyStatus(false);
    throw new Error('Threadforge local event log is inaccessible');
  }
  try {
    const info = await stat(logPath);
    const size = Math.min(info.size, maxStatusTailBytes);
    const offset = Math.max(0, info.size - size);
    const bytes = Buffer.alloc(size);
    const { bytesRead } = await file.read(bytes, 0, bytes.byteLength, offset);
    let contents = bytes.subarray(0, bytesRead).toString('utf8');
    const olderOmitted = offset > 0;
    if (olderOmitted) {
      const firstBreak = contents.indexOf('\n');
      contents = firstBreak < 0 ? '' : contents.slice(firstBreak + 1);
    }
    const lines = contents.split(/\r?\n/);
    if (lines.at(-1) === '') lines.pop();
    const status = emptyStatus(true);
    status.older_events_omitted = olderOmitted;
    let latest = 0;
    for (const line of lines) {
      if (!line) continue;
      if (Buffer.byteLength(line, 'utf8') > maxStatusLineBytes) {
        status.malformed_lines++;
        continue;
      }
      let event: unknown;
      try { event = JSON.parse(line); }
      catch { status.malformed_lines++; continue; }
      if (!isRecord(event) || !isOpenClawOtlpEvent(event)) continue;
      const timestamp = typeof event.timestamp === 'string' ? Date.parse(event.timestamp) : Number.NaN;
      if (!Number.isFinite(timestamp)) {
        status.malformed_lines++;
        continue;
      }
      status.observed_events++;
      if (timestamp > latest) latest = timestamp;
    }
    status.complete = status.malformed_lines === 0 && !olderOmitted;
    if (status.observed_events) {
      status.last_event_observed = true;
      status.last_event_observed_at = new Date(latest).toISOString();
    }
    return status;
  } finally {
    await file.close();
  }
}

export function validateOpenClawOtelStatus(status: OpenClawOtelStatus, sinceMs = 10 * 60_000, now = new Date()): {
  schema_version: 'threadforge.openclaw-otel-validation.v1';
  valid: boolean;
  since_ms: number;
  status: OpenClawOtelStatus;
} {
  if (!Number.isSafeInteger(sinceMs) || sinceMs < 1 || sinceMs > 30 * 24 * 60 * 60_000) {
    throw new Error('OpenClaw OTLP validation window must be between 1 ms and 30 days');
  }
  const timestamp = status.last_event_observed_at ? Date.parse(status.last_event_observed_at) : Number.NaN;
  return {
    schema_version: 'threadforge.openclaw-otel-validation.v1',
    valid: Number.isFinite(timestamp) && timestamp >= now.getTime() - sinceMs && timestamp <= now.getTime() + 60_000,
    since_ms: sinceMs,
    status,
  };
}

export function parseOpenClawDuration(value: string): number {
  const match = /^(\d+)(ms|s|m|h|d)$/.exec(value.trim());
  if (!match) throw new Error('duration must use ms, s, m, h, or d units');
  const multiplier: Record<string, number> = { ms: 1, s: 1000, m: 60_000, h: 3_600_000, d: 86_400_000 };
  const amount = Number(match[1]);
  const result = amount * multiplier[match[2]!];
  if (!Number.isSafeInteger(result) || result < 1 || result > 30 * 24 * 60 * 60_000) {
    throw new Error('duration must be between 1 ms and 30 days');
  }
  return result;
}

function validateEndpoint(value: string, allowRemote: boolean): string {
  let url: URL;
  try { url = new URL(value); }
  catch { throw new Error('OpenClaw OTLP endpoint must be a valid HTTP URL'); }
  const hostname = url.hostname.toLowerCase().replace(/^\[|\]$/g, '');
  const ipv4Loopback = /^127(?:\.\d{1,3}){3}$/.test(hostname) && hostname.split('.').every((part) => Number(part) <= 255);
  const loopback = hostname === 'localhost' || hostname.endsWith('.localhost') || hostname === '::1' || ipv4Loopback;
  if ((!loopback && (!allowRemote || url.protocol !== 'https:')) || loopback && !['http:', 'https:'].includes(url.protocol) ||
      url.username || url.password || url.search || url.hash || url.pathname !== '/') {
    throw new Error('OpenClaw OTLP endpoint must be a loopback HTTP endpoint; remote endpoints require HTTPS and --allow-remote');
  }
  return url.origin;
}

function signalEndpoint(endpoint: string, signal: string): string {
  return `${endpoint}/v1/${signal}`;
}

function isOpenClawOtlpEvent(value: Record<string, unknown>): boolean {
  const harness = isRecord(value.harness) ? value.harness : {};
  return typeof harness.name === 'string' && /open[-_]?claw/i.test(harness.name) && harness.collection_method === 'otlp';
}

function emptyStatus(present: boolean): OpenClawOtelStatus {
  return {
    schema_version: 'threadforge.openclaw-otel-status.v1', present, complete: present,
    older_events_omitted: false, malformed_lines: 0, observed_events: 0, last_event_observed: false,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
