import { constants } from 'node:fs';
import { lstat, mkdir, open, readFile, realpath, rename, unlink } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { kimiCodeManagedMarker } from './kimi-code-hook.ts';

export type KimiCodeHookChange = 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
export interface KimiCodeHookOptions {
  command: string;
  kimiCodeHome?: string;
}

const maxConfigBytes = 8 << 20;
const lockName = '.threadforge-kimi-code-hooks.lock';
const hookDefinitions: Array<{ event: string; timeout: number }> = [
  { event: 'SessionStart', timeout: 10 },
  { event: 'UserPromptSubmit', timeout: 30 },
  { event: 'PreToolUse', timeout: 10 },
  { event: 'PostToolUse', timeout: 10 },
  { event: 'PostToolUseFailure', timeout: 10 },
  { event: 'PermissionRequest', timeout: 10 },
  { event: 'PermissionResult', timeout: 10 },
  { event: 'Stop', timeout: 45 },
  { event: 'SessionEnd', timeout: 45 },
  { event: 'SubagentStart', timeout: 10 },
  { event: 'SubagentStop', timeout: 10 },
  { event: 'PreCompact', timeout: 10 },
  { event: 'PostCompact', timeout: 10 },
  { event: 'StopFailure', timeout: 10 },
  { event: 'Interrupt', timeout: 10 },
  { event: 'TaskStarted', timeout: 10 },
  { event: 'Notification', timeout: 10 },
];

type TomlValue = string | number;
interface HookTable {
  start: number;
  dataEnd: number;
  fields: Map<string, TomlValue>;
  invalid?: string;
  code: string;
  managed?: boolean;
}
interface ScanResult { hooks: HookTable[]; inlineHooks: boolean; }
interface FileSnapshot { identity?: { dev: number; ino: number; mode: number; size: number; mtimeMs: number }; }
interface TomlStringState { multiline?: string; }

/** Installs user-scope Kimi Code hooks by appending validated TOML array tables. */
export async function installKimiCodeHooks(options: KimiCodeHookOptions): Promise<KimiCodeHookChange> {
  validateCommand(options.command);
  const config = await resolveConfig(options.kimiCodeHome, true);
  if (!config) throw new Error('unable to resolve Kimi Code configuration path');
  return withLock(config, async () => {
    const read = await readConfig(config);
    const scan = scanKimiToml(read.text);
    if (scan.inlineHooks) throw new Error('Kimi Code uses [[hooks]] array tables; inline hooks configuration cannot be safely extended');
    const owned = scan.hooks.filter((entry) => entry.managed);
    if (owned.length > 0) {
      if (hasExpectedHooks(owned, options.command)) return 'unchanged';
      throw new Error('Threadforge Kimi Code hooks already exist; use update to replace the marked entries');
    }
    const next = appendHooks(read.text, options.command);
    await atomicWrite(config, read.snapshot, next);
    return 'installed';
  });
}

/** Updates only marked Kimi Code hook entries; it never creates an install during update. */
export async function updateKimiCodeHooks(options: KimiCodeHookOptions): Promise<KimiCodeHookChange> {
  validateCommand(options.command);
  const config = await resolveConfig(options.kimiCodeHome, false);
  if (!config) throw new Error('Kimi Code config.toml is absent; install Threadforge hooks before updating');
  return withLock(config, async () => {
    const read = await readConfig(config);
    const scan = scanKimiToml(read.text);
    if (scan.inlineHooks) throw new Error('Kimi Code inline hooks configuration cannot be safely updated');
    const owned = scan.hooks.filter((entry) => entry.managed);
    if (owned.length === 0) throw new Error('Threadforge Kimi Code hooks are absent; refusing to create them during update');
    if (hasExpectedHooks(owned, options.command)) return 'unchanged';
    const next = appendHooks(removeHookTables(read.text, owned), options.command);
    await atomicWrite(config, read.snapshot, next);
    return 'updated';
  });
}

export async function removeKimiCodeHooks(options: Omit<KimiCodeHookOptions, 'command'> = {}): Promise<KimiCodeHookChange> {
  const config = await resolveConfig(options.kimiCodeHome, false);
  if (!config) return 'absent';
  return withLock(config, async () => {
    const read = await readConfig(config);
    const scan = scanKimiToml(read.text);
    const owned = scan.hooks.filter((entry) => entry.managed);
    if (owned.length === 0) return 'absent';
    if (scan.inlineHooks) throw new Error('Kimi Code inline hooks configuration cannot be safely uninstalled');
    await atomicWrite(config, read.snapshot, removeHookTables(read.text, owned));
    return 'removed';
  });
}

export function kimiCodeConfigPath(kimiCodeHome: string): string {
  if (!path.isAbsolute(kimiCodeHome)) throw new Error('KIMI_CODE_HOME must be an absolute path');
  return path.join(kimiCodeHome, 'config.toml');
}

async function resolveConfig(kimiCodeHome: string | undefined, createDefaultHome: boolean): Promise<string | undefined> {
  const configuredHome = kimiCodeHome ?? process.env.KIMI_CODE_HOME;
  let root: string;
  if (configuredHome) {
    if (!path.isAbsolute(configuredHome)) throw new Error('KIMI_CODE_HOME must be an absolute path');
    const inputStat = await lstat(configuredHome).catch((error: unknown) => {
      if (!createDefaultHome && isCode(error, 'ENOENT')) return null;
      throw new Error('KIMI_CODE_HOME must exist and be accessible');
    });
    if (!inputStat) return undefined;
    if (!inputStat.isDirectory() || inputStat.isSymbolicLink()) throw new Error('KIMI_CODE_HOME must be a real directory');
    root = await realpath(configuredHome).catch(() => { throw new Error('KIMI_CODE_HOME must exist and be accessible'); });
  } else {
    const home = await realpath(os.homedir()).catch(() => { throw new Error('user home must exist and be accessible'); });
    const homeInfo = await lstat(home);
    if (!homeInfo.isDirectory() || homeInfo.isSymbolicLink()) throw new Error('user home must be a real directory');
    const defaultRoot = path.join(home, '.kimi-code');
    if (createDefaultHome) {
      try { await mkdir(defaultRoot, { mode: 0o700 }); }
      catch (error) { if (!isCode(error, 'EEXIST')) throw new Error('unable to create Kimi Code home'); }
    }
    const info = await lstat(defaultRoot).catch((error: unknown) => {
      if (!createDefaultHome && isCode(error, 'ENOENT')) return null;
      throw new Error('Kimi Code home is inaccessible');
    });
    if (!info) return undefined;
    if (!info.isDirectory() || info.isSymbolicLink()) throw new Error('Kimi Code home must be a real directory');
    root = await realpath(defaultRoot).catch(() => { throw new Error('Kimi Code home is inaccessible'); });
  }
  const rootStat = await lstat(root);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) throw new Error('Kimi Code home must be a real directory');
  const config = path.join(root, 'config.toml');
  const info = await lstat(config).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!info) return createDefaultHome ? config : undefined;
  if (!info.isFile() || info.isSymbolicLink()) throw new Error('Kimi Code config.toml must be a regular file');
  if (info.size > maxConfigBytes) throw new Error('Kimi Code config.toml exceeds the local size limit');
  return config;
}

async function withLock<T>(configPath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = path.join(path.dirname(configPath), lockName);
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Kimi Code hooks are locked by another writer or a stale lock');
    throw new Error('unable to lock Kimi Code config');
  }
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    await unlink(lockPath).catch(() => {});
  }
}

async function readConfig(configPath: string): Promise<{ text: string; snapshot: FileSnapshot }> {
  try {
    const info = await lstat(configPath);
    if (!info.isFile() || info.isSymbolicLink()) throw new Error('Kimi Code config.toml must be a regular file');
    if (info.size > maxConfigBytes) throw new Error('Kimi Code config.toml exceeds the local size limit');
    const bytes = await readFile(configPath);
    const text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes);
    return { text, snapshot: { identity: { dev: info.dev, ino: info.ino, mode: info.mode, size: info.size, mtimeMs: info.mtimeMs } } };
  } catch (error) {
    if (isCode(error, 'ENOENT')) return { text: '', snapshot: {} };
    if (error instanceof Error && error.message.startsWith('Kimi Code config.toml')) throw error;
    throw new Error('Kimi Code config.toml is not valid UTF-8; no changes were made');
  }
}

function scanKimiToml(text: string): ScanResult {
  const hooks: HookTable[] = [];
  let currentTable: string[] = [];
  let currentArrayTable = false;
  let currentHook: HookTable | undefined;
  let depth = 0;
  let inlineHooks = false;
  const strings: TomlStringState = {};
  for (const line of linesWithOffsets(text)) {
    const wasMultiline = Boolean(strings.multiline);
    const sourceLine = line.start === 0 ? line.text.replace(/^\uFEFF/, '') : line.text;
    const code = stripTomlComment(sourceLine, strings);
    if (wasMultiline || strings.multiline) {
      if (currentHook) currentHook.code += code;
      continue;
    }
    const trimmed = code.trim();
    if (!trimmed) continue;
    if (depth > 0) {
      if (currentHook) currentHook.code += code;
      depth = updateTomlValueDepth(code, depth);
      continue;
    }
    if (trimmed.startsWith('[')) {
      const header = parseTableHeader(trimmed);
      currentTable = header.path;
      currentArrayTable = header.array;
      currentHook = undefined;
      if (header.path[0] === 'hooks') {
        if (header.path.length === 1 && header.array) {
          currentHook = { start: line.start, dataEnd: line.end, fields: new Map(), code: '' };
          hooks.push(currentHook);
        } else {
          inlineHooks = true;
        }
      }
      continue;
    }
    const equals = findUnquotedEquals(trimmed);
    if (equals < 0) throw new Error('Kimi Code config contains unsupported TOML syntax; no changes were made');
    const keyText = trimmed.slice(0, equals).trim();
    const valueText = trimmed.slice(equals + 1).trim();
    if (currentTable.length === 0) {
      const keys = parseKeyPath(keyText);
      if (keys[0] === 'hooks') inlineHooks = true;
    }
    if (currentHook && currentTable.length === 1 && currentTable[0] === 'hooks' && currentArrayTable) {
      const keys = parseKeyPath(keyText);
      if (keys.length !== 1) currentHook.invalid = 'dotted hook keys are unsupported';
      const key = keys[0] ?? '';
      if (!['event', 'matcher', 'command', 'timeout'].includes(key)) currentHook.invalid = `unsupported hook field ${key}`;
      if (currentHook.fields.has(key)) currentHook.invalid = `duplicate hook field ${key}`;
      const value = parseHookFieldValue(key, valueText);
      if (value === undefined) currentHook.invalid = `unsupported value for hook field ${key}`;
      else currentHook.fields.set(key, value);
      currentHook.code += code;
      currentHook.dataEnd = line.end;
    }
    depth = updateTomlValueDepth(valueText, 0);
  }
  if (strings.multiline) throw new Error('Kimi Code config has an unterminated TOML multiline string; no changes were made');
  if (depth !== 0) throw new Error('Kimi Code config has an unterminated TOML array or inline table; no changes were made');
  for (const hook of hooks) {
    if (hook.invalid) throw new Error(`Kimi Code hooks array table ${hook.invalid}; no changes were made`);
    const event = hook.fields.get('event');
    const command = hook.fields.get('command');
    const matcher = hook.fields.get('matcher');
    const timeout = hook.fields.get('timeout');
    if (typeof event !== 'string' || !event || typeof command !== 'string' || !command ||
        matcher !== undefined && typeof matcher !== 'string' ||
        timeout !== undefined && (typeof timeout !== 'number' || !Number.isInteger(timeout) || timeout < 1 || timeout > 600)) {
      throw new Error('Kimi Code hooks array table is incomplete or malformed; no changes were made');
    }
    const markerAppears = hook.code.includes(kimiCodeManagedMarker);
    const managed = command.includes(kimiCodeManagedMarker);
    if (markerAppears && !managed) throw new Error('Kimi Code Threadforge marker appears in unsupported hook syntax; inspect it before retrying');
    hook.managed = managed;
    if (managed && !hookDefinitions.some((definition) => definition.event === event)) {
      throw new Error('Threadforge Kimi Code hook uses an unsupported event; inspect it before retrying');
    }
  }
  return { hooks, inlineHooks };
}

function parseHookFieldValue(key: string, raw: string): TomlValue | undefined {
  const value = raw.trim();
  if (key === 'timeout') {
    if (!/^[+-]?(?:0|[1-9](?:_?[0-9])*)$/.test(value)) return undefined;
    const number = Number(value.replaceAll('_', ''));
    return Number.isSafeInteger(number) ? number : undefined;
  }
  return parseTomlString(value);
}

function parseTomlString(value: string): string | undefined {
  if (value.length < 2) return undefined;
  if (value.startsWith('"') && value.endsWith('"')) {
    try { const parsed: unknown = JSON.parse(value); return typeof parsed === 'string' ? parsed : undefined; }
    catch { return undefined; }
  }
  if (value.startsWith("'") && value.endsWith("'") && !value.slice(1, -1).includes("'")) return value.slice(1, -1);
  return undefined;
}

function appendHooks(content: string, command: string): string {
  const eol = content.includes('\r\n') ? '\r\n' : '\n';
  let prefix = content;
  if (prefix && !prefix.endsWith('\n')) prefix += eol;
  const blocks = hookDefinitions.map(({ event, timeout }) => [
    '[[hooks]]',
    `event = ${JSON.stringify(event)}`,
    `command = ${JSON.stringify(command)}`,
    `timeout = ${timeout}`,
  ].join(eol));
  return `${prefix}${blocks.join(eol)}${eol}`;
}

function hasExpectedHooks(entries: HookTable[], command: string): boolean {
  if (entries.length !== hookDefinitions.length) return false;
  const actual = entries.map((entry) => ({
    event: entry.fields.get('event'),
    command: entry.fields.get('command'),
    matcher: entry.fields.get('matcher'),
    timeout: entry.fields.get('timeout'),
  })).sort((a, b) => String(a.event).localeCompare(String(b.event)));
  const expected = [...hookDefinitions].sort((a, b) => a.event.localeCompare(b.event)).map(({ event, timeout }) => ({ event, command, matcher: undefined, timeout }));
  return actual.every((entry, index) => entry.event === expected[index]?.event && entry.command === command &&
    entry.matcher === undefined && entry.timeout === expected[index]?.timeout);
}

function removeHookTables(content: string, entries: HookTable[]): string {
  const ranges = entries.map(({ start, dataEnd }) => ({ start, end: dataEnd })).sort((a, b) => a.start - b.start);
  let result = '';
  let offset = 0;
  for (const range of ranges) {
    if (range.start < offset || range.end < range.start) throw new Error('Kimi Code owned hook ranges overlap; no changes were made');
    result += content.slice(offset, range.start);
    offset = range.end;
  }
  return result + content.slice(offset);
}

function* linesWithOffsets(text: string): Generator<{ text: string; start: number; end: number }> {
  for (let start = 0; start < text.length;) {
    const newline = text.indexOf('\n', start);
    const end = newline < 0 ? text.length : newline + 1;
    let line = text.slice(start, newline < 0 ? text.length : newline);
    if (line.endsWith('\r')) line = line.slice(0, -1);
    yield { text: line, start, end };
    start = end;
  }
}

function parseTableHeader(value: string): { path: string[]; array: boolean } {
  const array = value.startsWith('[[');
  const opening = array ? 2 : 1;
  const closing = array ? ']]' : ']';
  if (!value.endsWith(closing)) throw new Error('Kimi Code config has an unsupported TOML table header; no changes were made');
  const pathParts = parseKeyPath(value.slice(opening, -closing.length).trim());
  if (pathParts.length === 0) throw new Error('Kimi Code config has an empty TOML table header; no changes were made');
  return { path: pathParts, array };
}

function parseKeyPath(value: string): string[] {
  const keys: string[] = [];
  let index = 0;
  while (index < value.length) {
    while (/\s/.test(value[index] ?? '')) index++;
    if (index >= value.length) break;
    const quote = value[index];
    let key: string;
    if (quote === '"' || quote === "'") {
      const start = index++;
      let escaped = false;
      for (; index < value.length; index++) {
        const character = value[index];
        if (quote === '"' && character === '\\' && !escaped) { escaped = true; continue; }
        if (character === quote && !escaped) break;
        escaped = false;
      }
      if (index >= value.length) throw new Error('Kimi Code config has an unterminated quoted TOML key; no changes were made');
      const raw = value.slice(start, index + 1);
      try { key = quote === '"' ? JSON.parse(raw) as string : raw.slice(1, -1); }
      catch { throw new Error('Kimi Code config has an unsupported quoted TOML key; no changes were made'); }
      index++;
    } else {
      const start = index;
      while (index < value.length && !/[.\s]/.test(value[index])) index++;
      key = value.slice(start, index);
      if (!/^[A-Za-z0-9_-]+$/.test(key)) throw new Error('Kimi Code config has an unsupported bare TOML key; no changes were made');
    }
    keys.push(key);
    while (/\s/.test(value[index] ?? '')) index++;
    if (index === value.length) break;
    if (value[index] !== '.') throw new Error('Kimi Code config has an unsupported dotted TOML key; no changes were made');
    index++;
  }
  return keys;
}

function findUnquotedEquals(value: string): number {
  let quote = '';
  let escaped = false;
  for (let index = 0; index < value.length; index++) {
    const character = value[index];
    if (quote) {
      if (quote === '"' && character === '\\' && !escaped) { escaped = true; continue; }
      if (character === quote && !escaped) quote = '';
      escaped = false;
      continue;
    }
    if (character === '"' || character === "'") { quote = character; continue; }
    if (character === '=') return index;
  }
  return -1;
}

function stripTomlComment(line: string, state: TomlStringState): string {
  let output = '';
  let index = 0;
  if (state.multiline) {
    const close = findMultilineEnd(line, state.multiline, 0);
    if (close < 0) return '';
    state.multiline = undefined;
    index = close + 3;
  }
  for (; index < line.length;) {
    const character = line[index];
    if (character === '#') break;
    if ((character === '"' || character === "'") && line.startsWith(character.repeat(3), index)) {
      const marker = character.repeat(3);
      const close = findMultilineEnd(line, marker, index + 3);
      if (close < 0) { state.multiline = marker; break; }
      output += '""';
      index = close + 3;
      continue;
    }
    if (character === '"' || character === "'") {
      const start = index++;
      let escaped = false;
      for (; index < line.length; index++) {
        const current = line[index];
        if (character === '"' && current === '\\' && !escaped) { escaped = true; continue; }
        if (current === character && !escaped) break;
        escaped = false;
      }
      if (index >= line.length) throw new Error('Kimi Code config has an unterminated TOML string; no changes were made');
      output += line.slice(start, index + 1);
      index++;
      continue;
    }
    output += character;
    index++;
  }
  return output;
}

function findMultilineEnd(line: string, marker: string, from: number): number {
  for (let index = line.indexOf(marker, from); index >= 0; index = line.indexOf(marker, index + marker.length)) {
    if (marker === "'''" || !isEscaped(line, index)) return index;
  }
  return -1;
}

function isEscaped(value: string, index: number): boolean {
  let slashes = 0;
  for (let cursor = index - 1; cursor >= 0 && value[cursor] === '\\'; cursor--) slashes++;
  return slashes % 2 === 1;
}

function updateTomlValueDepth(value: string, initial: number): number {
  let depth = initial;
  let quote = '';
  let escaped = false;
  for (let index = 0; index < value.length; index++) {
    const character = value[index];
    if (quote) {
      if (quote === '"' && character === '\\' && !escaped) { escaped = true; continue; }
      if (character === quote && !escaped) quote = '';
      escaped = false;
      continue;
    }
    if ((character === '"' || character === "'") && value.startsWith(character.repeat(3), index)) {
      const marker = character.repeat(3);
      const close = findMultilineEnd(value, marker, index + 3);
      if (close < 0) return depth;
      index = close + 2;
      continue;
    }
    if (character === '"' || character === "'") { quote = character; continue; }
    if (character === '[' || character === '{') depth++;
    else if (character === ']' || character === '}') depth--;
    if (depth < 0) throw new Error('Kimi Code config has an invalid TOML array or inline table; no changes were made');
  }
  return depth;
}

async function atomicWrite(config: string, snapshot: FileSnapshot, content: string): Promise<void> {
  const bytes = Buffer.from(content, 'utf8');
  if (bytes.byteLength > maxConfigBytes) throw new Error('Kimi Code config.toml would exceed the local size limit');
  const tempPath = `${config}.threadforge-${randomUUID()}.tmp`;
  const mode = snapshot.identity ? snapshot.identity.mode & 0o777 : 0o600;
  let handle;
  try {
    await assertUnchanged(config, snapshot);
    handle = await open(tempPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, mode);
    await handle.chmod(mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    await assertUnchanged(config, snapshot);
    await rename(tempPath, config);
  } catch {
    await handle?.close().catch(() => {});
    await unlink(tempPath).catch(() => {});
    throw new Error('unable to atomically update Kimi Code config.toml');
  }
}

async function assertUnchanged(config: string, snapshot: FileSnapshot): Promise<void> {
  const info = await lstat(config).catch((error: unknown) => {
    if (isCode(error, 'ENOENT')) return null;
    throw error;
  });
  if (!snapshot.identity) {
    if (info) throw new Error('Kimi Code config.toml appeared during update');
    return;
  }
  if (!info || !info.isFile() || info.isSymbolicLink() || info.dev !== snapshot.identity.dev || info.ino !== snapshot.identity.ino ||
      info.size !== snapshot.identity.size || info.mtimeMs !== snapshot.identity.mtimeMs) {
    throw new Error('Kimi Code config.toml changed during update');
  }
}

function validateCommand(command: string): void {
  if (typeof command !== 'string' || !command.trim() || command.length > 8192 || /[\r\n\0]/.test(command)) {
    throw new Error('Kimi Code hook command is invalid or too long');
  }
  if (!command.includes(kimiCodeManagedMarker)) throw new Error(`Kimi Code hook command must include ${kimiCodeManagedMarker}`);
  if (looksSecretLike(command)) throw new Error('Kimi Code hook command must not contain credential-like values');
}

function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?(?:key|token)|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) ||
    /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
