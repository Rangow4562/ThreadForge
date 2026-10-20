// MAIN-world interceptor. Runs in the page's own JS context (injected as a file
// via a world:"MAIN" content script, which satisfies ChatGPT's CSP since it is
// not inline). It monkeypatches window.fetch, clones matched streaming
// responses so the page is completely unaffected, and posts raw SSE chunks to
// the ISOLATED content script over window.postMessage.
//
// This layer is deliberately site-agnostic: it does NOT understand SSE
// semantics. It only decides "is this a chat request worth teeing?" and relays
// bytes. All parsing happens in the service worker via the site adapter.

import { THREADFORGE_CAPTURE_CONTROL, THREADFORGE_MSG, type InterceptEvent } from '../shared/types.js';
import { looksLikeChatRequest } from '../adapters/adapter.js';
import { isCaptureControlMessage, MAX_INTERCEPT_CHUNK_CHARS, MAX_REQUEST_BODY_CHARS, siteForHost } from '../shared/messages.js';

const maxStreamBytes = 4 << 20;

declare global {
  interface Window {
  __threadforgeInterceptorInstalled?: boolean;
  __threadforgeCaptureControlSeen?: boolean;
  }
}

(function install() {
  if (window.__threadforgeInterceptorInstalled) return;
  window.__threadforgeInterceptorInstalled = true;

  let reqSeq = 0;
  let captureEnabled = false;
  window.__threadforgeCaptureControlSeen = false;
  window.addEventListener('message', (message: MessageEvent) => {
    if (message.source !== window || message.origin !== window.location.origin || !isCaptureControlMessage(message.data)) return;
    captureEnabled = message.data.enabled && siteForHost(window.location.hostname) !== undefined;
    window.__threadforgeCaptureControlSeen = true;
  });
  const originalFetch = window.fetch.bind(window);

  function post(event: InterceptEvent): void {
    window.postMessage({ source: THREADFORGE_MSG, event }, window.location.origin);
  }

  window.fetch = async function threadforgeFetch(
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> {
    const url =
      typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
    const method = (init?.method ?? (input instanceof Request ? input.method : 'GET')).toUpperCase();

    const matched = captureEnabled && siteForHost(window.location.hostname) !== undefined && looksLikeChatRequest(url);

    // Capture the request body BEFORE fetch consumes it. Prefer a string
    // init.body; otherwise read a clone of the Request (fetch(new Request(...))),
    // whose body would otherwise never be seen and the prompt would be lost.
    let body: string | null = null;
    if (matched) {
      if (typeof init?.body === 'string') {
        body = init.body.length <= MAX_REQUEST_BODY_CHARS ? init.body : null;
      } else if (input instanceof Request) {
        try {
          const candidate = await input.clone().text();
          body = candidate.length <= MAX_REQUEST_BODY_CHARS ? candidate : null;
        } catch {
          body = null;
        }
      }
    }

    const response = await originalFetch(input as RequestInfo, init);

    if (matched && response.body) {
      const reqId = ++reqSeq;
      post({ kind: 'request', reqId, url, method, body });

      // Tee the stream via a clone so the page's own reader is untouched.
      const clone = response.clone();
      teeStream(clone, reqId, post).catch(() => post({ kind: 'error', reqId }));
    }

    return response;
  } as typeof window.fetch;
})();

async function teeStream(
  response: Response,
  reqId: number,
  post: (e: InterceptEvent) => void,
): Promise<void> {
  const reader = response.body!.getReader();
  const decoder = new TextDecoder();
  let capturedBytes = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      capturedBytes += value.byteLength;
      if (capturedBytes > maxStreamBytes) {
        post({ kind: 'error', reqId });
        await reader.cancel().catch(() => undefined);
        return;
      }
      const chunk = decoder.decode(value, { stream: true });
      postText(chunk, reqId, post);
    }
    const tail = decoder.decode();
    postText(tail, reqId, post);
    post({ kind: 'done', reqId });
  } finally {
    reader.releaseLock();
  }
}

function postText(text: string, reqId: number, post: (event: InterceptEvent) => void): void {
  for (let offset = 0; offset < text.length; offset += MAX_INTERCEPT_CHUNK_CHARS) {
    const chunk = text.slice(offset, offset + MAX_INTERCEPT_CHUNK_CHARS);
    if (chunk) post({ kind: 'chunk', reqId, chunk });
  }
}
