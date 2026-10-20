import { lstat, open } from 'node:fs/promises';

const maxStatusTailBytes = 16 << 20;
const maxStatusLineBytes = 64 << 10;

export interface LocalOtelEventStatus {
  present: boolean;
  complete: boolean;
  older_events_omitted: boolean;
  malformed_lines: number;
  observed_events: number;
  last_event_observed: boolean;
  last_event_observed_at?: string;
}

/** Scan a bounded JSONL tail for one OTLP harness, returning aggregate evidence only. */
export async function scanLocalOtelEventLog(logPath: string, harnessName: string, now = new Date()): Promise<LocalOtelEventStatus> {
  let handle;
  try {
    const pathInfo = await lstat(logPath);
    if (pathInfo.isSymbolicLink() || !pathInfo.isFile()) throw new Error('Threadforge local event log is not a regular file');
    handle = await open(logPath, 'r');
    const openInfo = await handle.stat();
    const latestPathInfo = await lstat(logPath);
    if (latestPathInfo.isSymbolicLink() || !latestPathInfo.isFile()) throw new Error('Threadforge local event log changed to a symlink');
    const size = Math.min(openInfo.size, maxStatusTailBytes);
    const offset = Math.max(0, openInfo.size - size);
    const bytes = Buffer.alloc(size);
    const { bytesRead } = await handle.read(bytes, 0, size, offset);
    let content = bytes.subarray(0, bytesRead).toString('utf8');
    const olderEventsOmitted = offset > 0;
    if (olderEventsOmitted) {
      const firstBreak = content.indexOf('\n');
      content = firstBreak < 0 ? '' : content.slice(firstBreak + 1);
    }
    const status: LocalOtelEventStatus = {
      present: true, complete: !olderEventsOmitted, older_events_omitted: olderEventsOmitted,
      malformed_lines: 0, observed_events: 0, last_event_observed: false,
    };
    let latest = 0;
    const lines = content.split(/\r?\n/);
    if (lines.at(-1) === '') lines.pop();
    for (const line of lines) {
      if (!line) continue;
      if (Buffer.byteLength(line, 'utf8') > maxStatusLineBytes) {
        status.malformed_lines++;
        continue;
      }
      let event: unknown;
      try { event = JSON.parse(line); }
      catch { status.malformed_lines++; continue; }
      if (!isRecord(event) || !isOtelEvent(event, harnessName)) continue;
      const timestamp = typeof event.timestamp === 'string' ? Date.parse(event.timestamp) : Number.NaN;
      if (!Number.isFinite(timestamp) || timestamp > now.getTime() + 60_000) {
        status.malformed_lines++;
        continue;
      }
      status.observed_events++;
      if (timestamp > latest) latest = timestamp;
    }
    status.complete = status.complete && status.malformed_lines === 0;
    if (status.observed_events) {
      status.last_event_observed = true;
      status.last_event_observed_at = new Date(latest).toISOString();
    }
    return status;
  } catch (error) {
    if (isCode(error, 'ENOENT')) {
      return { present: false, complete: false, older_events_omitted: false, malformed_lines: 0, observed_events: 0, last_event_observed: false };
    }
    throw new Error('Threadforge local event log is inaccessible');
  } finally {
    await handle?.close();
  }
}

function isOtelEvent(value: Record<string, unknown>, name: string): boolean {
  const harness = isRecord(value.harness) ? value.harness : {};
  return harness.name === name && harness.collection_method === 'otlp';
}
function isRecord(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function isCode(error: unknown, code: string): boolean { return typeof error === 'object' && error !== null && 'code' in error && error.code === code; }
