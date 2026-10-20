// Service-worker entry. Wires message handling, assembles turns, and hands
// completed turns to the durable delivery queue.

import '../adapters/claude.js'; // registers the Claude adapter (side effect)
import '../adapters/chatgpt.js'; // registers the ChatGPT adapter (side effect)
import { ext } from '../shared/browser.js';
import type { ChatTurn, RelayMessage, Settings, SettingsPatch } from '../shared/types.js';
import { getSettings, saveSettings, siteEnabled } from './settings.js';
import { Assembler } from './assembler.js';
import { clearQueue, enqueueTurn, flush, installFlushAlarm } from './delivery.js';
import { detectBrowser } from '../shared/browser-identity.js';
import { isRelayMessage, siteForHost } from '../shared/messages.js';

const assembler = new Assembler();

// Which Chromium fork (or other browser) this is. Resolved once: the brand
// list and UA string are fixed for the life of the worker. Stamped on every
// emitted turn as user_agent.* / browser.brands.
const browserIdentity = detectBrowser(globalThis.navigator);

// Keep the SW awake while any stream is active, so mid-stream suspension is
// rare. Correctness never depends on this — durable state covers the rest.
let keepAliveTimer: ReturnType<typeof setInterval> | undefined;
function updateKeepAlive(): void {
  if (assembler.active > 0 && keepAliveTimer == null) {
    keepAliveTimer = setInterval(() => {
      void ext.runtime.getPlatformInfo();
      for (const partial of assembler.expireIdle(Date.now())) void finalizeTurn(partial).catch(() => undefined);
      updateKeepAlive();
    }, 20_000);
  } else if (assembler.active === 0 && keepAliveTimer != null) {
    clearInterval(keepAliveTimer);
    keepAliveTimer = undefined;
  }
}

ext.runtime.onInstalled.addListener(() => {
  void saveSettings({}); // materialize defaults
});

installFlushAlarm();

ext.runtime.onMessage.addListener((msg: RelayMessage | ControlMessage, sender, sendResponse) => {
  if (isControl(msg)) {
    handleControl(msg).then(sendResponse);
    return true; // async response
  }
  const senderPageURL = sender.url ?? sender.tab?.url;
  if (isRelayMessage(msg, senderPageURL ?? '')) {
    const pageURL = safePageURL(senderPageURL);
    const site = pageURL ? siteForHost(pageURL.hostname) : undefined;
    if (!site) return false;
    // Feed the assembler SYNCHRONOUSLY, in message-arrival order. Stream events
    // arrive ordered (request → chunk → … → done); an `await` before this call
    // could let a chunk be processed before its `request` registered a parser,
    // dropping early SSE (e.g. message_start). Async work (settings, delivery)
    // happens only after a turn is finalized.
    const turn = assembler.handle(pageURL!.hostname, sender.tab?.id, msg.event);
    updateKeepAlive();
    if (turn) {
      // Keep the MV3 worker alive across the durable queue write and bounded
      // local delivery attempt. The response is metadata-only and is not used
      // by the page; without this channel Chrome may suspend the worker before
      // the asynchronous finalize path reaches storage or fetch.
      void finalizeTurn(turn).then(
        () => sendResponse({ accepted: true }),
        () => sendResponse({ accepted: false }),
      );
      return true;
    }
    return false;
  }
  return false;
});

async function finalizeTurn(turn: ChatTurn): Promise<void> {
  const settings = await getSettings();
  // siteEnabled covers both the global toggle and the per-site toggle.
  if (!siteEnabled(settings, turn.site)) return;
  await enqueueTurn(turn, settings, browserIdentity);
}

// ---- Control messages from popup/options ----

type ControlMessage =
  | { type: 'GET_STATUS' }
  | { type: 'SET_SETTINGS'; patch: SettingsPatch }
  | { type: 'FLUSH_NOW' }
  | { type: 'CLEAR_QUEUE' };

function isControl(msg: any): msg is ControlMessage {
  return msg?.type === 'GET_STATUS' || msg?.type === 'SET_SETTINGS' || msg?.type === 'FLUSH_NOW' || msg?.type === 'CLEAR_QUEUE';
}

async function handleControl(msg: ControlMessage): Promise<unknown> {
  switch (msg.type) {
    case 'GET_STATUS': {
      const settings = await getSettings();
      const q = await ext.storage.local.get(['delivery_queue', 'queue_dropped_count']);
      return {
        settings,
        queueDepth: Array.isArray(q.delivery_queue) ? q.delivery_queue.length : 0,
        droppedCount: typeof q.queue_dropped_count === 'number' ? q.queue_dropped_count : 0,
        lastDeliveryIssue: Array.isArray(q.delivery_queue)
          ? (q.delivery_queue.find((item: any) => typeof item?.lastError === 'string')?.lastError ?? null)
          : null,
        active: assembler.active,
      };
    }
    case 'SET_SETTINGS':
      return { settings: await saveSettings(msg.patch) };
    case 'FLUSH_NOW':
      await flush();
      return { ok: true };
    case 'CLEAR_QUEUE':
      await clearQueue();
      return { ok: true };
  }
}

function safePageURL(value: string | undefined): URL | undefined {
  if (!value) return undefined;
  try {
    const url = new URL(value);
    if (url.protocol !== 'https:' || !siteForHost(url.hostname)) return undefined;
    return url;
  } catch {
    return undefined;
  }
}
