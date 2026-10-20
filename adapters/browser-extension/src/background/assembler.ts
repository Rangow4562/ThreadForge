// Accumulates raw intercept events into completed ChatTurns, one parser per
// (tabId, reqId). No chrome.* — unit-testable. State is intentionally in-memory:
// a stream lasts seconds and the SW is kept awake while one is active; the
// DURABLE state (the delivery queue) lives in storage, not here.

import type { ChatTurn } from '../shared/types.js';
import type { InterceptEvent } from '../shared/types.js';
import { adapterForHost, type SiteAdapter, type TurnParser } from '../adapters/adapter.js';

interface Entry {
  adapter: SiteAdapter;
  parser: TurnParser;
  lastActivityAt: number;
}

const maxActiveStreams = 12;
const maxActivePerTab = 4;
const idleStreamMs = 120_000;

export class Assembler {
  private entries = new Map<string, Entry>();

  private key(tabId: number | undefined, reqId: number): string {
    return `${tabId ?? -1}:${reqId}`;
  }

  /** Feed one intercept event. Returns a finalized ChatTurn on 'done'/'error'. */
  handle(host: string, tabId: number | undefined, event: InterceptEvent): ChatTurn | null {
    const k = this.key(tabId, event.reqId);

    if (event.kind === 'request') {
      const adapter = adapterForHost(host);
      if (!adapter || !adapter.matchesRequest(event.url, event.method)) return null;
      if (!this.entries.has(k) && this.overCapacity(tabId)) return null;
      const parser = adapter.createParser(event.reqId);
      parser.onRequest(event.url, event.method, event.body);
      this.entries.set(k, { adapter, parser, lastActivityAt: Date.now() });
      return null;
    }

    const entry = this.entries.get(k);
    if (!entry) return null;
    entry.lastActivityAt = Date.now();

    switch (event.kind) {
      case 'chunk':
        entry.parser.onChunk(event.chunk);
        return null;
      case 'done':
      case 'error': {
        entry.parser.onDone();
        const turn = entry.parser.getTurn();
        this.entries.delete(k);
        return turn;
      }
    }
    return null;
  }

  /** Number of in-flight streams (used to decide SW keepalive). */
  get active(): number {
    return this.entries.size;
  }

  /** Finalize a stalled stream as partial and release its bounded parser state. */
  expireIdle(now: number, idleMs = idleStreamMs): ChatTurn[] {
    const expired: ChatTurn[] = [];
    for (const [key, entry] of this.entries) {
      if (now - entry.lastActivityAt < idleMs) continue;
      const turn = entry.parser.getTurn();
      this.entries.delete(key);
      if (turn) expired.push(turn);
    }
    return expired;
  }

  private overCapacity(tabId: number | undefined): boolean {
    if (this.entries.size >= maxActiveStreams) return true;
    const prefix = `${tabId ?? -1}:`;
    let activeForTab = 0;
    for (const key of this.entries.keys()) {
      if (key.startsWith(prefix) && ++activeForTab >= maxActivePerTab) return true;
    }
    return false;
  }
}
