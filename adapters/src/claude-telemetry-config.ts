import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';

const maxSettingsBytes = 8 << 20;
const stateSchema = 'threadforge.claude-telemetry-state.v1';
const managedEnv = {
  CLAUDE_CODE_ENABLE_TELEMETRY: '1',
  CLAUDE_CODE_ENHANCED_TELEMETRY_BETA: '1',
  OTEL_METRICS_EXPORTER: 'otlp',
  OTEL_LOGS_EXPORTER: 'otlp',
  OTEL_TRACES_EXPORTER: 'otlp',
  OTEL_EXPORTER_OTLP_PROTOCOL: 'http/json',
  OTEL_EXPORTER_OTLP_ENDPOINT: 'http://127.0.0.1:4318',
  BETA_TRACING_ENDPOINT: '',
  OTEL_EXPORTER_OTLP_LOGS_PROTOCOL: '',
  OTEL_EXPORTER_OTLP_METRICS_PROTOCOL: '',
  OTEL_EXPORTER_OTLP_TRACES_PROTOCOL: '',
  OTEL_EXPORTER_OTLP_LOGS_ENDPOINT: '',
  OTEL_EXPORTER_OTLP_METRICS_ENDPOINT: '',
  OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: '',
  OTEL_EXPORTER_OTLP_HEADERS: '',
  OTEL_EXPORTER_OTLP_LOGS_HEADERS: '',
  OTEL_EXPORTER_OTLP_METRICS_HEADERS: '',
  OTEL_EXPORTER_OTLP_TRACES_HEADERS: '',
  OTEL_LOG_USER_PROMPTS: '0',
  OTEL_LOG_ASSISTANT_RESPONSES: '0',
  OTEL_LOG_TOOL_DETAILS: '0',
  OTEL_LOG_TOOL_CONTENT: '0',
  OTEL_LOG_RAW_API_BODIES: '0',
} as const;
type JsonObject = Record<string, unknown>;
export type ClaudeTelemetryChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

interface OwnershipState {
  schema_version: typeof stateSchema;
  owned_keys: string[];
}

/** Adds local OTLP/HTTP telemetry to the Claude user settings without enabling content capture. */
export async function installClaudeTelemetry(options: { homeDir?: string } = {}): Promise<ClaudeTelemetryChange> {
  const paths = await configPaths(options.homeDir ?? os.homedir(), true);
  return withLock(paths.lock, async () => {
    const settings = await readJson(paths.settings, 'Claude settings');
    const settingsExisted = await pathExists(paths.settings);
    const env = readEnv(settings);
    const state = await readOwnership(paths.state);
    const before = JSON.stringify(settings);
    const owned = new Set(state?.owned_keys ?? []);
    for (const [key, expected] of Object.entries(managedEnv)) {
      if (Object.hasOwn(env, key)) {
        if (env[key] !== expected) throw new Error(`Claude env.${key} has a conflicting user value; no settings were changed`);
        continue;
      }
      env[key] = expected;
      owned.add(key);
    }
    settings.env = env;
    const changed = before !== JSON.stringify(settings);
    const nextState: OwnershipState = { schema_version: stateSchema, owned_keys: [...owned].sort() };
    if (!changed && state && deepEqual(nextState, state)) return 'unchanged';

    if (!state) {
      await atomicWrite(paths.settings, settings);
      try {
        await atomicWrite(paths.state, nextState);
      } catch (error) {
        if (settingsExisted) await atomicWrite(paths.settings, JSON.parse(before) as JsonObject).catch(() => {});
        else await unlink(paths.settings).catch(() => {});
        throw error;
      }
      return 'installed';
    }
    await atomicWrite(paths.settings, settings);
    if (!deepEqual(nextState, state)) await atomicWrite(paths.state, nextState);
    return 'updated';
  });
}

export async function removeClaudeTelemetry(options: { homeDir?: string } = {}): Promise<ClaudeTelemetryChange> {
  const paths = await configPaths(options.homeDir ?? os.homedir(), false);
  if (!paths) return 'absent';
  return withLock(paths.lock, async () => {
    const state = await readOwnership(paths.state);
    if (!state) return 'absent';
    const settings = await readJson(paths.settings, 'Claude settings');
    const env = readEnv(settings);
    let changed = false;
    for (const key of state.owned_keys) {
      if (Object.hasOwn(env, key) && env[key] === managedEnv[key as keyof typeof managedEnv]) {
        delete env[key];
        changed = true;
      }
    }
    if (changed) {
      if (Object.keys(env).length === 0) delete settings.env;
      else settings.env = env;
      await atomicWrite(paths.settings, settings);
    }
    await unlink(paths.state);
    return 'removed';
  });
}

async function configPaths(homeDir: string, create: true): Promise<{ settings: string; state: string; lock: string }>;
async function configPaths(homeDir: string, create: false): Promise<{ settings: string; state: string; lock: string } | undefined>;
async function configPaths(homeDir: string, create: boolean): Promise<{ settings: string; state: string; lock: string } | undefined> {
  if (!path.isAbsolute(homeDir)) throw new Error('Claude home directory must be absolute');
  const home = await realpath(homeDir).catch(() => { throw new Error('Claude home directory must exist and be accessible'); });
  const claudeDir = path.join(home, '.claude');
  if (create) {
    try { await mkdir(claudeDir, { mode: 0o700 }); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Claude settings directory'); }
  }
  const dirStat = await lstat(claudeDir).catch(() => null);
  if (!dirStat) return undefined;
  if (!dirStat.isDirectory() || dirStat.isSymbolicLink()) throw new Error('Claude settings directory must be a real directory, not a symbolic link');
  return {
    settings: path.join(claudeDir, 'settings.json'),
    state: path.join(claudeDir, '.threadforge-telemetry.json'),
    lock: path.join(claudeDir, '.threadforge-telemetry.lock'),
  };
}

async function withLock<T>(lockPath: string, action: () => Promise<T>): Promise<T> {
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Claude telemetry settings are locked by another writer or a stale lock');
    throw new Error('unable to lock Claude telemetry settings');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readJson(filePath: string, label: string): Promise<JsonObject> {
  try {
    const stat = await lstat(filePath);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error(`${label} must be a regular file`);
    if (stat.size > maxSettingsBytes) throw new Error(`${label} exceeds the local size limit`);
    const value: unknown = JSON.parse((await readFile(filePath, 'utf8')).replace(/^\uFEFF/, ''));
    if (!isRecord(value)) throw new Error(`${label} root must be an object`);
    return value;
  } catch (error) {
    if (isCode(error, 'ENOENT')) return {};
    if (error instanceof Error && error.message.startsWith(label)) throw error;
    throw new Error(`${label} is invalid strict JSON; no changes were made`);
  }
}

async function readOwnership(filePath: string): Promise<OwnershipState | undefined> {
  const stat = await lstat(filePath).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw new Error('unable to inspect Threadforge Claude ownership state');
  });
  if (!stat) return undefined;
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Threadforge Claude ownership state must be a regular file');
  const value = await readJson(filePath, 'Threadforge Claude ownership state');
  if (value.schema_version !== stateSchema || !Array.isArray(value.owned_keys) ||
      value.owned_keys.some((key) => typeof key !== 'string' || !Object.hasOwn(managedEnv, key)) ||
      new Set(value.owned_keys).size !== value.owned_keys.length) {
    throw new Error('Threadforge Claude ownership state is invalid; inspect it before retrying');
  }
  return value as unknown as OwnershipState;
}

async function pathExists(filePath: string): Promise<boolean> {
  try { await lstat(filePath); return true; }
  catch (error) { if (isCode(error, 'ENOENT')) return false; throw error; }
}

function readEnv(settings: JsonObject): JsonObject {
  if (settings.env === undefined || settings.env === null) return {};
  if (!isRecord(settings.env)) throw new Error('Claude settings env must be an object; no changes were made');
  return settings.env;
}

async function atomicWrite(filePath: string, value: unknown): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    let mode = 0o600;
    try {
      const existing = await lstat(filePath);
      if (!existing.isFile() || existing.isSymbolicLink()) throw new Error('Claude settings target must be a regular file');
      mode = existing.mode & 0o777;
    } catch (error) { if (!isCode(error, 'ENOENT')) throw error; }
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.writeFile(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update Claude telemetry config');
  }
}

function deepEqual(left: unknown, right: unknown): boolean {
  return JSON.stringify(canonicalize(left)) === JSON.stringify(canonicalize(right));
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isCode(error: unknown, code: string): boolean {
  return isRecord(error) && error.code === code;
}
