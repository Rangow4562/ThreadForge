import { THREADFORGE_CAPTURE_CONTROL, THREADFORGE_MSG, type InterceptEvent, type RelayMessage } from './types.js';

// 256K UTF-16 code units bound UTF-8 payloads to about 1 MiB.
export const MAX_REQUEST_BODY_CHARS = 256 << 10;
export const MAX_INTERCEPT_CHUNK_CHARS = 32 << 10;

export function siteForHost(hostname: string): 'claude_web' | 'chatgpt_web' | undefined {
  const host = hostname.toLowerCase().replace(/\.$/, '');
  if (host === 'claude.ai') return 'claude_web';
  if (host === 'chatgpt.com' || host === 'chat.openai.com') return 'chatgpt_web';
  return undefined;
}

export function isInterceptEvent(value: unknown, pageOrigin: string): value is InterceptEvent {
  if (!isRecord(value) || !Number.isSafeInteger(value.reqId) || (value.reqId as number) < 1) return false;
  switch (value.kind) {
    case 'request': {
      if (!exactKeys(value, ['kind', 'reqId', 'url', 'method', 'body']) ||
          typeof value.url !== 'string' || value.url.length > 4096 || value.method !== 'POST' ||
          (value.body !== null && (typeof value.body !== 'string' || value.body.length > MAX_REQUEST_BODY_CHARS))) return false;
      let url: URL;
      try { url = new URL(value.url, pageOrigin); }
      catch { return false; }
      let origin: URL;
      try { origin = new URL(pageOrigin); }
      catch { return false; }
      if (url.protocol !== 'https:' || url.hostname !== origin.hostname || siteForHost(url.hostname) === undefined) return false;
      return /(?:\/backend-(?:api|anon)\/(?:f\/)?conversation|\/chat_conversations\/.+\/(?:completion|retry_completion)|\/completion\b)/.test(url.pathname);
    }
    case 'chunk':
      return exactKeys(value, ['kind', 'reqId', 'chunk']) && typeof value.chunk === 'string' &&
        value.chunk.length > 0 && value.chunk.length <= MAX_INTERCEPT_CHUNK_CHARS;
    case 'done':
    case 'error':
      return exactKeys(value, ['kind', 'reqId']);
    default:
      return false;
  }
}

export function isRelayMessage(value: unknown, pageOrigin: string): value is RelayMessage {
  return isRecord(value) && exactKeys(value, ['type', 'event']) &&
    value.type === 'THREADFORGE_RAW' && isInterceptEvent(value.event, pageOrigin);
}

export function isThreadforgePageMessage(value: unknown): value is { source: typeof THREADFORGE_MSG; event: unknown } {
  return isRecord(value) && exactKeys(value, ['source', 'event']) && value.source === THREADFORGE_MSG;
}

export function isCaptureControlMessage(value: unknown): value is { source: typeof THREADFORGE_CAPTURE_CONTROL; enabled: boolean } {
  return isRecord(value) && exactKeys(value, ['source', 'enabled']) &&
    value.source === THREADFORGE_CAPTURE_CONTROL && typeof value.enabled === 'boolean';
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function exactKeys(value: Record<string, unknown>, expected: string[]): boolean {
  const keys = Object.keys(value);
  return keys.length === expected.length && expected.every((key) => Object.hasOwn(value, key));
}
