import { createHash, randomUUID } from 'node:crypto';
import { constants } from 'node:fs';
import { lstat, mkdir, open, rename, unlink } from 'node:fs/promises';
import path from 'node:path';
import { isMap, isScalar, parseDocument } from 'yaml';
import { getGooseConfigPath, normalizeGooseOtelEndpoint } from './goose-otel.ts';
import { resolveRealPathWithoutLinks } from './path-safety.ts';

const defaultTimeoutMs = 10_000;
const maxTimeoutMs = 120_000;
const maxGooseConfigBytes = 2 << 20;
const beginMarkerPrefix = '# THREADFORGE-GOOSE-OTEL-BEGIN v1';
const endMarker = '# THREADFORGE-GOOSE-OTEL-END v1';

export type GooseOtelConfigAction = 'install' | 'update' | 'uninstall';

export interface GooseOtelConfigOptions {
  configPath?: string;
  endpoint?: string;
  timeoutMs?: number;
  homeDir?: string;
  platform?: NodeJS.Platform;
  env?: NodeJS.ProcessEnv;
  allowContentCapture?: boolean;
}

export interface GooseOtelConfigResult {
  schema_version: 'threadforge.goose-otel-config.v1';
  action: GooseOtelConfigAction;
  result: 'installed' | 'updated' | 'unchanged' | 'removed' | 'absent';
  content_capture_possible: boolean;
  content_capture_scope: 'loopback_configured' | 'none';
  consent_confirmed: boolean;
  config_created_by_threadforge: boolean;
  configured_endpoint_class?: 'loopback_http';
  timeout_ms?: number;
  runtime_environment_may_override: boolean;
}

interface Snapshot {
  exists: boolean;
  bytes?: Buffer;
  mode: number;
}

interface DecodedConfig {
  text: string;
  bom: boolean;
  eol: string;
}

interface ManagedBlock {
  start: number;
  contentEnd: number;
  endpoint: string;
  timeoutMs: number;
  createdFile: boolean;
  separatorAdded: boolean;
  originalPrefixSha256: string;
  eol: string;
  bom: boolean;
}

/** Install or manage the explicitly opted-in, loopback-only Goose OTLP YAML block. */
export async function runGooseOtelConfigAction(
  action: GooseOtelConfigAction,
  options: GooseOtelConfigOptions = {},
): Promise<GooseOtelConfigResult> {
  if (action !== 'uninstall' && options.allowContentCapture !== true) {
    throw new Error('Goose OTLP may capture prompts, outputs, reasoning, and tool metadata; pass --allow-content-capture to confirm local capture');
  }
  if (action === 'uninstall' && (options.endpoint !== undefined || options.timeoutMs !== undefined)) {
    throw new Error('Goose OTLP uninstall does not accept endpoint or timeout options');
  }

  const requestedPath = options.configPath ?? getGooseConfigPath({
    platform: options.platform,
    homeDir: options.homeDir,
    appData: (options.env ?? process.env).APPDATA,
  });
  const absolutePath = await safeConfigPath(requestedPath);
  if (action === 'install') await ensureParentDirectory(absolutePath);
  const configPath = await safeConfigPath(absolutePath);
  const initial = await readSnapshot(configPath);
  if (action === 'uninstall' && !initial.exists) return resultFor(action, 'absent', false, false);

  return withConfigLock(configPath, async () => {
    const snapshot = await readSnapshot(configPath);
    if (action === 'update' && !snapshot.exists) {
      throw new Error('Goose OTLP has no Threadforge-managed config to update');
    }
    if (action === 'uninstall' && !snapshot.exists) return resultFor(action, 'absent', false, false);

    const decoded = decodeConfig(snapshot.bytes ?? Buffer.alloc(0));
    const parsed = parseGooseConfig(decoded.text);
    const block = findManagedBlock(decoded.text, decoded.eol, decoded.bom);
    const existingKeys = new Set(parsed.keys);
    const hasEndpoint = existingKeys.has('otel_exporter_otlp_endpoint');
    const hasTimeout = existingKeys.has('otel_exporter_otlp_timeout');

    if (!block) {
      if (action === 'uninstall') return resultFor(action, 'absent', false, false);
      if (action === 'update') throw new Error('Goose OTLP has no Threadforge-managed config to update');
      if (hasEndpoint || hasTimeout) {
        throw new Error('Goose OTLP keys already exist outside Threadforge ownership; preserve them and use print-config for manual review');
      }
      const endpoint = normalizeGooseOtelEndpoint(options.endpoint ?? 'http://127.0.0.1:4318');
      const timeoutMs = validateTimeout(options.timeoutMs ?? defaultTimeoutMs);
      const separatorAdded = decoded.text.length > 0 && !decoded.text.endsWith(decoded.eol);
      const prefix = separatorAdded ? `${decoded.text}${decoded.eol}` : decoded.text;
      const marker = renderManagedBlock({
        endpoint,
        timeoutMs,
        eol: decoded.eol,
        createdFile: !snapshot.exists,
        separatorAdded,
        originalPrefixSha256: sha256(decoded.text),
      });
      const nextText = `${prefix}${marker}${decoded.eol}`;
      validateGooseConfig(nextText);
      await atomicReplace(configPath, snapshot, encodeConfig(nextText, decoded.bom));
      return resultFor(action, 'installed', true, !snapshot.exists, timeoutMs);
    }

    if (!blockMatchesConfig(block, parsed)) {
      throw new Error('Threadforge-managed Goose OTLP block conflicts with the parsed YAML settings; no changes were made');
    }

    if (action === 'uninstall') {
      let prefix = decoded.text.slice(0, block.start);
      let suffix = decoded.text.slice(block.contentEnd);
      if (suffix.startsWith(block.eol)) suffix = suffix.slice(block.eol.length);
      if (block.separatorAdded && !suffix && prefix.endsWith(block.eol)) {
        const withoutInsertedSeparator = prefix.slice(0, -block.eol.length);
        if (sha256(withoutInsertedSeparator) === block.originalPrefixSha256) prefix = withoutInsertedSeparator;
      }
      const restoredText = `${prefix}${suffix}`;
      const restoredBytes = encodeConfig(restoredText, block.bom);
      if (block.createdFile && restoredBytes.length === 0) {
        await atomicRemove(configPath, snapshot);
      } else {
        validateGooseConfig(restoredText);
        await atomicReplace(configPath, snapshot, restoredBytes);
      }
      return resultFor(action, 'removed', false, false);
    }

    const endpoint = normalizeGooseOtelEndpoint(options.endpoint ?? block.endpoint);
    const timeoutMs = validateTimeout(options.timeoutMs ?? block.timeoutMs);
    if (action === 'install') {
      if (endpoint !== block.endpoint || timeoutMs !== block.timeoutMs) {
        throw new Error('Threadforge-managed Goose OTLP settings differ; use update to change them');
      }
      return resultFor(action, 'unchanged', true, block.createdFile, timeoutMs);
    }
    if (endpoint === block.endpoint && timeoutMs === block.timeoutMs) {
      return resultFor(action, 'unchanged', true, block.createdFile, timeoutMs);
    }

    const replacement = renderManagedBlock({
      endpoint,
      timeoutMs,
      eol: block.eol,
      createdFile: block.createdFile,
      separatorAdded: block.separatorAdded,
      originalPrefixSha256: block.originalPrefixSha256,
    });
    const nextText = `${decoded.text.slice(0, block.start)}${replacement}${decoded.text.slice(block.contentEnd)}`;
    validateGooseConfig(nextText);
    await atomicReplace(configPath, snapshot, encodeConfig(nextText, block.bom));
    return resultFor(action, 'updated', true, block.createdFile, timeoutMs);
  });
}

function resultFor(
  action: GooseOtelConfigAction,
  result: GooseOtelConfigResult['result'],
  capturePossible: boolean,
  created: boolean,
  timeoutMs?: number,
): GooseOtelConfigResult {
  return {
    schema_version: 'threadforge.goose-otel-config.v1',
    action,
    result,
    content_capture_possible: capturePossible,
    content_capture_scope: capturePossible ? 'loopback_configured' : 'none',
    consent_confirmed: action !== 'uninstall' && capturePossible,
    config_created_by_threadforge: created,
    ...(capturePossible ? { configured_endpoint_class: 'loopback_http' as const } : {}),
    ...(timeoutMs !== undefined ? { timeout_ms: timeoutMs } : {}),
    runtime_environment_may_override: capturePossible,
  };
}

function validateTimeout(value: number): number {
  if (!Number.isSafeInteger(value) || value < 1 || value > maxTimeoutMs) {
    throw new Error('Goose OTLP timeout must be an integer between 1 and 120000 milliseconds');
  }
  return value;
}

function renderManagedBlock(options: {
  endpoint: string;
  timeoutMs: number;
  eol: string;
  createdFile: boolean;
  separatorAdded: boolean;
  originalPrefixSha256: string;
}): string {
  const body = [
    `otel_exporter_otlp_endpoint: ${JSON.stringify(options.endpoint)}`,
    `otel_exporter_otlp_timeout: ${options.timeoutMs}`,
  ];
  const bodyText = body.join(options.eol);
  const marker = `${beginMarkerPrefix} sha256=${sha256(bodyText)} created=${options.createdFile ? 1 : 0} separator=${options.separatorAdded ? 1 : 0} prefix_sha256=${options.originalPrefixSha256}`;
  return [marker, ...body, endMarker].join(options.eol);
}

function findManagedBlock(text: string, eol: string, bom: boolean): ManagedBlock | undefined {
  const lines = text.split(eol);
  if (text.endsWith(eol)) lines.pop();
  const starts = lines.map((line, index) => line.startsWith(beginMarkerPrefix) ? index : -1).filter((index) => index >= 0);
  const ends = lines.map((line, index) => line === endMarker ? index : -1).filter((index) => index >= 0);
  if (starts.length === 0 && ends.length === 0) {
    if (text.includes('THREADFORGE-GOOSE-OTEL-BEGIN') || text.includes('THREADFORGE-GOOSE-OTEL-END')) {
      throw new Error('Goose OTLP ownership marker is incomplete or unsupported');
    }
    return undefined;
  }
  if (starts.length !== 1 || ends.length !== 1) throw new Error('Goose OTLP ownership markers are duplicated or malformed');
  const startLine = starts[0]!;
  const endLine = ends[0]!;
  if (endLine !== startLine + 3) throw new Error('Threadforge-managed Goose OTLP block has an unsupported shape');
  const markerMatch = /^# THREADFORGE-GOOSE-OTEL-BEGIN v1 sha256=([a-f0-9]{64}) created=([01]) separator=([01]) prefix_sha256=([a-f0-9]{64})$/.exec(lines[startLine]!);
  if (!markerMatch) throw new Error('Threadforge-managed Goose OTLP marker is malformed');
  const body = lines.slice(startLine + 1, endLine);
  if (sha256(body.join(eol)) !== markerMatch[1]) throw new Error('Threadforge-managed Goose OTLP block was edited; no changes were made');
  const bodyRoot = parseGooseConfig(body.join(eol));
  const endpointNode = bodyRoot.values.get('otel_exporter_otlp_endpoint');
  const timeoutNode = bodyRoot.values.get('otel_exporter_otlp_timeout');
  const endpointValue = scalarValue(endpointNode);
  const timeoutValue = scalarValue(timeoutNode);
  if (typeof endpointValue !== 'string' || typeof timeoutValue !== 'number') {
    throw new Error('Threadforge-managed Goose OTLP values are malformed');
  }
  const endpoint = normalizeGooseOtelEndpoint(endpointValue);
  const timeoutMs = validateTimeout(timeoutValue);
  const start = lineStartOffset(lines, startLine, eol);
  const contentEnd = lineStartOffset(lines, endLine, eol) + lines[endLine]!.length;
  return {
    start,
    contentEnd,
    endpoint,
    timeoutMs,
    createdFile: markerMatch[2] === '1',
    separatorAdded: markerMatch[3] === '1',
    originalPrefixSha256: markerMatch[4]!,
    eol,
    bom,
  };
}

function lineStartOffset(lines: string[], lineIndex: number, eol: string): number {
  let offset = 0;
  for (let index = 0; index < lineIndex; index++) offset += lines[index]!.length + eol.length;
  return offset;
}

function parseGooseConfig(text: string): { keys: string[]; values: Map<string, unknown> } {
  const document = parseDocument(text, { version: '1.2', uniqueKeys: true });
  if (document.errors.length > 0 || document.warnings.length > 0) {
    throw new Error('Goose config is ambiguous or invalid YAML; no changes were made');
  }
  if (document.contents === null) return { keys: [], values: new Map() };
  if (!isMap(document.contents)) throw new Error('Goose config root must be a YAML mapping; no changes were made');
  const values = new Map<string, unknown>();
  for (const pair of document.contents.items) {
    if (!isScalar(pair.key) || typeof pair.key.value !== 'string') {
      throw new Error('Goose config contains a non-string root key; no changes were made');
    }
    values.set(pair.key.value, pair.value);
  }
  return { keys: [...values.keys()], values };
}

function scalarValue(value: unknown): unknown {
  return isScalar(value) ? value.value : undefined;
}

function blockMatchesConfig(block: ManagedBlock, parsed: { keys: string[]; values: Map<string, unknown> }): boolean {
  if (parsed.keys.filter((key) => key === 'otel_exporter_otlp_endpoint').length !== 1 ||
      parsed.keys.filter((key) => key === 'otel_exporter_otlp_timeout').length !== 1) return false;
  return scalarValue(parsed.values.get('otel_exporter_otlp_endpoint')) === block.endpoint &&
    scalarValue(parsed.values.get('otel_exporter_otlp_timeout')) === block.timeoutMs;
}

function decodeConfig(bytes: Buffer): DecodedConfig {
  let text: string;
  try { text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes); }
  catch { throw new Error('Goose config is not valid UTF-8; no changes were made'); }
  const bom = text.startsWith('\ufeff');
  if (bom) text = text.slice(1);
  if (text.includes('\r') && text.replaceAll('\r\n', '').includes('\r')) {
    throw new Error('Goose config has unsupported carriage-return line endings; no changes were made');
  }
  const eol = text.includes('\r\n') ? '\r\n' : '\n';
  if (eol === '\r\n' && text.replaceAll('\r\n', '').includes('\n')) {
    throw new Error('Goose config has mixed line endings; no changes were made');
  }
  return { text, bom, eol };
}

function encodeConfig(text: string, bom: boolean): Buffer {
  const bytes = Buffer.from(`${bom ? '\ufeff' : ''}${text}`, 'utf8');
  if (bytes.length > maxGooseConfigBytes) throw new Error('updated Goose config exceeds the 2 MiB local limit');
  return bytes;
}

function validateGooseConfig(text: string): void {
  parseGooseConfig(text);
}

function sha256(value: string): string {
  return createHash('sha256').update(value, 'utf8').digest('hex');
}

async function safeConfigPath(input: string): Promise<string> {
  if (!path.isAbsolute(input)) throw new Error('Goose config path must be absolute');
  const resolved = path.resolve(input);
  const safe = await resolveRealPathWithoutLinks(resolved);
  if (!safe) throw new Error('Goose config path is inaccessible or traverses a symbolic link');
  return safe;
}

async function ensureParentDirectory(filePath: string): Promise<void> {
  const parent = path.dirname(filePath);
  if (!await resolveRealPathWithoutLinks(parent)) throw new Error('Goose config parent path is inaccessible or traverses a symbolic link');
  try { await mkdir(parent, { recursive: true, mode: 0o700 }); }
  catch { throw new Error('unable to create Goose config directory'); }
  if (!await resolveRealPathWithoutLinks(parent)) throw new Error('Goose config parent path changed or traverses a symbolic link');
}

async function readSnapshot(filePath: string): Promise<Snapshot> {
  const before = await lstat(filePath).catch((error: unknown) => isCode(error, 'ENOENT') ? null : Promise.reject(error));
  if (!before) return { exists: false, mode: 0o600 };
  if (before.isSymbolicLink() || !before.isFile()) throw new Error('Goose config must be a regular file, not a symbolic link');
  if (before.size > maxGooseConfigBytes) throw new Error('Goose config exceeds the 2 MiB local limit');
  const handle = await open(filePath, 'r').catch(() => { throw new Error('unable to read Goose config'); });
  try {
    const opened = await handle.stat();
    if (!opened.isFile() || opened.isSymbolicLink() || opened.size > maxGooseConfigBytes) {
      throw new Error('Goose config changed or exceeds the 2 MiB local limit');
    }
    const bytes = Buffer.alloc(opened.size);
    let offset = 0;
    while (offset < bytes.length) {
      const read = await handle.read(bytes, offset, bytes.length - offset, offset);
      if (read.bytesRead === 0) break;
      offset += read.bytesRead;
    }
    if (offset !== opened.size) throw new Error('Goose config changed while it was being read');
    const after = await lstat(filePath).catch(() => null);
    if (!after?.isFile() || after.isSymbolicLink() || after.size !== before.size || after.mtimeMs !== before.mtimeMs ||
        after.dev !== before.dev || after.ino !== before.ino) {
      throw new Error('Goose config changed while it was being read');
    }
    return { exists: true, bytes, mode: before.mode & 0o777 };
  } finally {
    await handle.close().catch(() => {});
  }
}

async function withConfigLock<T>(filePath: string, action: () => Promise<T>): Promise<T> {
  const lockPath = `${filePath}.threadforge.lock`;
  let lock;
  try { lock = await open(lockPath, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, 0o600); }
  catch (error) {
    if (isCode(error, 'EEXIST')) throw new Error('Goose config is locked by another writer or a stale Threadforge lock');
    throw new Error('unable to lock Goose config');
  }
  const lockInfo = await lock.stat().catch(() => undefined);
  try { return await action(); }
  finally {
    await lock.close().catch(() => {});
    if (lockInfo) {
      const current = await lstat(lockPath).catch(() => null);
      if (current?.isFile() && !current.isSymbolicLink() && current.dev === lockInfo.dev && current.ino === lockInfo.ino) {
        await unlink(lockPath).catch(() => {});
      }
    }
  }
}

async function atomicReplace(filePath: string, expected: Snapshot, bytes: Buffer): Promise<void> {
  const current = await readSnapshot(filePath);
  if (!sameSnapshot(expected, current)) throw new Error('Goose config changed before update; no changes were made');
  const temporary = `${filePath}.threadforge-${randomUUID()}.tmp`;
  let handle;
  try {
    handle = await open(temporary, constants.O_CREAT | constants.O_EXCL | constants.O_WRONLY, expected.mode);
    await handle.writeFile(bytes);
    await handle.sync();
    await handle.close();
    handle = undefined;
    const beforeRename = await readSnapshot(filePath);
    if (!sameSnapshot(expected, beforeRename)) throw new Error('Goose config changed during update; no changes were made');
    if (!await resolveRealPathWithoutLinks(filePath)) throw new Error('Goose config path became unsafe; no changes were made');
    await rename(temporary, filePath);
  } catch (error) {
    await handle?.close().catch(() => {});
    await unlink(temporary).catch(() => {});
    if (error instanceof Error && error.message.startsWith('Goose config changed')) throw error;
    if (error instanceof Error && error.message.startsWith('Goose config path')) throw error;
    throw new Error('unable to atomically update Goose config');
  }
}

async function atomicRemove(filePath: string, expected: Snapshot): Promise<void> {
  const current = await readSnapshot(filePath);
  if (!sameSnapshot(expected, current)) throw new Error('Goose config changed before removal; no changes were made');
  if (!await resolveRealPathWithoutLinks(filePath)) throw new Error('Goose config path became unsafe; no changes were made');
  try { await unlink(filePath); }
  catch { throw new Error('unable to remove the Threadforge-created Goose config'); }
}

function sameSnapshot(left: Snapshot, right: Snapshot): boolean {
  if (left.exists !== right.exists) return false;
  if (!left.exists || !right.exists) return left.exists === right.exists;
  return left.mode === right.mode && left.bytes?.equals(right.bytes ?? Buffer.alloc(0)) === true;
}

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error &&
    (error as NodeJS.ErrnoException).code === code;
}
