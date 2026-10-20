import { describe, expect, it } from 'vitest';
import { isInterceptEvent, isRelayMessage, siteForHost } from '../../src/shared/messages.js';

describe('browser bridge message validation', () => {
  it('maps only the explicitly supported HTTPS sites', () => {
    expect(siteForHost('claude.ai')).toBe('claude_web');
    expect(siteForHost('chatgpt.com')).toBe('chatgpt_web');
    expect(siteForHost('chat.openai.com')).toBe('chatgpt_web');
    expect(siteForHost('evil.example')).toBeUndefined();
  });

  it('accepts bounded request/chunk events only from the current chat origin and known routes', () => {
    expect(isInterceptEvent({
      kind: 'request', reqId: 1, method: 'POST',
      url: '/backend-api/f/conversation', body: '{"message":"prompt"}',
    }, 'https://chatgpt.com/')).toBe(true);
    expect(isInterceptEvent({ kind: 'chunk', reqId: 1, chunk: 'data: {}\n\n' }, 'https://chatgpt.com/')).toBe(true);
    for (const request of [
      { kind: 'request', reqId: 1, method: 'POST', url: 'https://evil.example/backend-api/f/conversation', body: null },
      { kind: 'request', reqId: 1, method: 'POST', url: 'http://chatgpt.com/backend-api/f/conversation', body: null },
      { kind: 'request', reqId: 1, method: 'GET', url: '/backend-api/f/conversation', body: null },
      { kind: 'request', reqId: 1, method: 'POST', url: '/upload/private-file', body: null },
      { kind: 'request', reqId: 1, method: 'POST', url: '/backend-api/f/conversation', body: 'x'.repeat((256 << 10) + 1) },
      { kind: 'chunk', reqId: 1, chunk: 'x'.repeat(33 << 10) },
      { kind: 'chunk', reqId: 0, chunk: 'x' },
    ]) expect(isInterceptEvent(request, 'https://chatgpt.com/')).toBe(false);
  });

  it('rejects arbitrary extension relay messages', () => {
    const valid = { type: 'THREADFORGE_RAW', event: { kind: 'done', reqId: 5 } };
    expect(isRelayMessage(valid, 'https://claude.ai/')).toBe(true);
    expect(isRelayMessage({ type: 'THREADFORGE_RAW', extra: 'x', event: { kind: 'done', reqId: 5 } }, 'https://claude.ai/')).toBe(false);
    expect(isRelayMessage({ type: 'BEACON_RAW', event: { kind: 'done', reqId: 5 } }, 'https://claude.ai/')).toBe(false);
  });
});
