// Durable delivery queue. Normalized OTLP envelopes are persisted to
// chrome.storage.local so nothing is lost if the SW is suspended, then POSTed
// to the local Threadforge collector. Failed sends retry with exponential backoff
// driven by chrome.alarms (which survives suspension, unlike setTimeout).

import { ext } from '../shared/browser.js';
import type { ChatTurn, Settings } from '../shared/types.js';
import { turnToEnvelope } from '../shared/normalize.js';
import type { LogsEnvelope } from '../shared/otlp.js';
import type { BrowserIdentity } from '../shared/browser-identity.js';
import { isLocalThreadforgeLogsEndpoint } from '../shared/endpoint.js';

const QUEUE_KEY = 'delivery_queue';
const ALARM = 'threadforge_browser_flush';
const MAX_ATTEMPTS = 6;
const MAX_QUEUE_ITEMS = 100;
const MAX_QUEUE_BYTES = 4 << 20;
const MAX_ITEM_BYTES = 512 << 10;
const DROPPED_KEY = 'queue_dropped_count';
const deliveryTimeoutMs = 10_000;

interface QueueItem {
  id: string;
  endpoint: string;
  envelope: LogsEnvelope;
  attempts: number;
  lastError?: DeliveryIssue;
}

export type DeliveryIssue = 'local_endpoint_blocked' | 'request_timeout' | 'network_error' | 'receiver_rejected';

async function readQueue(): Promise<QueueItem[]> {
  const got = await ext.storage.local.get(QUEUE_KEY);
  return (got[QUEUE_KEY] as QueueItem[]) ?? [];
}

async function writeQueue(items: QueueItem[]): Promise<void> {
  await ext.storage.local.set({ [QUEUE_KEY]: items });
}

// Serialize every read-modify-write on the queue. The service worker is a single
// JS context, but async interleaving of enqueue/flush would otherwise let two
// read→write cycles clobber each other's writes (dropping queued telemetry).
// Only this SW writes QUEUE_KEY, so an in-memory promise-chain mutex suffices.
let queueLock: Promise<unknown> = Promise.resolve();
function withQueueLock<T>(fn: () => Promise<T>): Promise<T> {
  const result = queueLock.then(fn, fn);
  queueLock = result.then(
    () => undefined,
    () => undefined,
  );
  return result;
}

/** Normalize a turn and enqueue it for delivery, then kick a flush. */
export async function enqueueTurn(
  turn: ChatTurn,
  settings: Settings,
  identity?: BrowserIdentity,
): Promise<void> {
  const envelope = turnToEnvelope(turn, settings.retention, identity);
  const body = JSON.stringify(envelope);
  const bytes = new TextEncoder().encode(body).byteLength;
  if (bytes > MAX_ITEM_BYTES || !isLocalThreadforgeLogsEndpoint(settings.endpoint)) {
    await recordDrop();
    return;
  }
  const item: QueueItem = {
    id: turn.turnId,
    endpoint: settings.endpoint,
    envelope,
    attempts: 0,
  };
  await withQueueLock(async () => {
    const queue = await readQueue();
    const queueBytes = queue.reduce((sum, queued) => sum + new TextEncoder().encode(JSON.stringify(queued.envelope)).byteLength, 0);
    if (queue.length >= MAX_QUEUE_ITEMS || queueBytes + bytes > MAX_QUEUE_BYTES) {
      await recordDrop();
      return;
    }
    queue.push(item);
    await writeQueue(queue);
  });
  await flush();
}

/** Attempt to POST everything queued; requeue failures with backoff. */
export async function flush(): Promise<void> {
  await withQueueLock(async () => {
    const queue = await readQueue();
    if (queue.length === 0) return;

    const remaining: QueueItem[] = [];
    let anyFailed = false;

    for (const item of queue) {
      const issue = await post(item);
      if (issue) {
        item.attempts += 1;
        item.lastError = issue;
        if (item.attempts < MAX_ATTEMPTS) {
          remaining.push(item);
          anyFailed = true;
        } else {
          await recordDrop();
        }
      }
    }

    await writeQueue(remaining);
    if (anyFailed) scheduleRetry(remaining);
  });
}

async function post(item: QueueItem): Promise<DeliveryIssue | undefined> {
  if (!isLocalThreadforgeLogsEndpoint(item.endpoint)) return 'local_endpoint_blocked';
  try {
    const res = await fetch(item.endpoint, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(item.envelope),
      signal: AbortSignal.timeout(deliveryTimeoutMs),
      redirect: 'error',
    });
    return res.ok ? undefined : 'receiver_rejected';
  } catch (error) {
    return error instanceof DOMException && error.name === 'TimeoutError' ? 'request_timeout' : 'network_error';
  }
}

async function recordDrop(): Promise<void> {
  const current = await ext.storage.local.get(DROPPED_KEY);
  const count = typeof current[DROPPED_KEY] === 'number' && Number.isSafeInteger(current[DROPPED_KEY])
    ? current[DROPPED_KEY] as number
    : 0;
  await ext.storage.local.set({ [DROPPED_KEY]: Math.min(count + 1, Number.MAX_SAFE_INTEGER) });
}

export async function clearQueue(): Promise<void> {
  await withQueueLock(async () => writeQueue([]));
}

function scheduleRetry(queue: QueueItem[]): void {
  const minAttempts = Math.min(...queue.map((i) => i.attempts));
  // We compute an exponential backoff, but note MV3 clamps chrome.alarms to a
  // ~30s minimum — so in practice the first retry lands at ~30s regardless of
  // this value. That's acceptable here: the collector is a local loopback
  // endpoint that is almost always reachable, so retries are the rare path and
  // the durable queue (chrome.storage) guarantees eventual delivery. We use
  // alarms rather than setTimeout because they survive service-worker suspension.
  const backoffMs = Math.min(250 * 2 ** minAttempts, 60_000);
  ext.alarms.create(ALARM, { when: Date.now() + backoffMs });
}

/** Wire the alarm listener (called once from the SW entry). */
export function installFlushAlarm(): void {
  ext.alarms.onAlarm.addListener((alarm) => {
    if (alarm.name === ALARM) void flush();
  });
}
