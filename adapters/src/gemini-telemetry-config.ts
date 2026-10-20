import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';

const maxSettingsBytes = 8 << 20;
const stateSchema = 'threadforge.gemini-telemetry-state.v1';
const managedSettings = {
  enabled: true,
  target: 'local',
  otlpEndpoint: 'http://127.0.0.1:4318',
  otlpProtocol: 'http',
  useCollector: true,
  logPrompts: false,
  traces: false,
} as const;
type JsonObject = Record<string, unknown>;
export type GeminiTelemetryChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';

interface OwnershipState {
  schema_version: typeof stateSchema;
  owned_keys: string[];
}

/** Installs only compatible local OTLP/HTTP settings, and refuses conflicting user telemetry config. */
export async function installGeminiTelemetry(options: { homeDir?: string } = {}): Promise<GeminiTelemetryChange> {
  const paths = await configPaths(options.homeDir ?? os.homedir(), true);
  return withLock(paths.lock, async () => {
    const settings = await readJson(paths.settings, 'Gemini settings');
    const settingsExisted = await pathExists(paths.settings);
    const telemetry = readTelemetry(settings);
    if (hasConfiguredOutfile(telemetry)) {
      throw new Error('Gemini telemetry.outfile overrides OTLP; remove or migrate it manually before installing Threadforge');
    }
    const state = await readOwnership(paths.state);
    const before = JSON.stringify(settings);
    const owned = new Set(state?.owned_keys ?? []);
    for (const [key, expected] of Object.entries(managedSettings)) {
      if (Object.hasOwn(telemetry, key)) {
        if (!deepEqual(telemetry[key], expected)) {
          throw new Error(`Gemini telemetry.${key} has a conflicting user value; no settings were changed`);
        }
        continue;
      }
      telemetry[key] = expected;
      owned.add(key);
    }
    settings.telemetry = telemetry;
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

export async function removeGeminiTelemetry(options: { homeDir?: string } = {}): Promise<GeminiTelemetryChange> {
  const paths = await configPaths(options.homeDir ?? os.homedir(), false);
  if (!paths) return 'absent';
  return withLock(paths.lock, async () => {
    const state = await readOwnership(paths.state);
    if (!state) return 'absent';
    const settings = await readJson(paths.settings, 'Gemini settings');
    const telemetry = readTelemetry(settings);
    let changed = false;
    for (const key of state.owned_keys) {
      if (Object.hasOwn(telemetry, key) && deepEqual(telemetry[key], managedSettings[key as keyof typeof managedSettings])) {
        delete telemetry[key];
        changed = true;
      }
    }
    if (changed) {
      if (Object.keys(telemetry).length === 0) delete settings.telemetry;
      else settings.telemetry = telemetry;
      await atomicWrite(paths.settings, settings);
    }
    await unlink(paths.state);
    return 'removed';
  });
}

async function configPaths(homeDir: string, create: true): Promise<{ settings: string; state: string; lock: string }>;
async function configPaths(homeDir: string, create: false): Promise<{ settings: string; state: string; lock: string } | undefined>;
async function configPaths(homeDir: string, create: boolean): Promise<{ settings: string; state: string; lock: string } | undefined> {
  if (!path.isAbsolute(homeDir)) throw new Error('Gemini home directory must be absolute');
  const home = await realpath(homeDir).catch(() => { throw new Error('Gemini home directory must exist and be accessible'); });
  const geminiDir = path.join(home, '.gemini');
  if (create) {
    try { await mkdir(geminiDir, { mode: 0o700 }); }
    catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Gemini settings directory'); }
  }
  const dirStat = await lstat(geminiDir).catch(() => null);
  if (!dirStat) return undefined;
  if (!dirStat.isDirectory() || dirStat.isSymbolicLink()) throw new Error('Gemini settings directory must be a real directory, not a symbolic link');
  return {
    settings: path.join(geminiDir, 'settings.json'),
    state: path.join(geminiDir, '.threadforge-telemetry.json'),
    lock: path.join(geminiDir, '.threadforge-telemetry.lock'),
  };
}

async function withLock<T>(lockPath: string, action: () => Promise<T>): Promise<T> {
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Gemini telemetry settings are locked by another writer or a stale lock');
    throw new Error('unable to lock Gemini telemetry settings');
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
    throw new Error('unable to inspect Threadforge Gemini ownership state');
  });
  if (!stat) return undefined;
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Threadforge Gemini ownership state must be a regular file');
  const value = await readJson(filePath, 'Threadforge Gemini ownership state');
  if (value.schema_version !== stateSchema || !Array.isArray(value.owned_keys) ||
      value.owned_keys.some((key) => typeof key !== 'string' || !Object.hasOwn(managedSettings, key)) ||
      new Set(value.owned_keys).size !== value.owned_keys.length) {
    throw new Error('Threadforge Gemini ownership state is invalid; inspect it before retrying');
  }
  return value as unknown as OwnershipState;
}

async function pathExists(filePath: string): Promise<boolean> {
  try { await lstat(filePath); return true; }
  catch (error) { if (isCode(error, 'ENOENT')) return false; throw error; }
}

function readTelemetry(settings: JsonObject): JsonObject {
  if (settings.telemetry === undefined || settings.telemetry === null) return {};
  if (!isRecord(settings.telemetry)) throw new Error('Gemini settings telemetry must be an object; no changes were made');
  return settings.telemetry;
}

function hasConfiguredOutfile(telemetry: JsonObject): boolean {
  return typeof telemetry.outfile === 'string' && telemetry.outfile.trim() !== '';
}

async function atomicWrite(filePath: string, value: unknown): Promise<void> {
  const tempPath = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    let mode = 0o600;
    try {
      const existing = await lstat(filePath);
      if (!existing.isFile() || existing.isSymbolicLink()) throw new Error('Gemini settings target must be a regular file');
      mode = existing.mode & 0o777;
    }
    catch (error) { if (!isCode(error, 'ENOENT')) throw error; }
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.writeFile(`${JSON.stringify(value, null, 2)}\n`, 'utf8');
    await handle.sync();
    await handle.close();
    handle = undefined;
    await rename(tempPath, filePath);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update Gemini telemetry config');
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
