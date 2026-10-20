// Shared, site-agnostic types. Everything downstream of `ChatTurn` is generic;
// only the per-site adapters know how ChatGPT vs Claude wire formats look.

export type SiteName = 'claude_web' | 'chatgpt_web';

export type Retention = 'metadata' | 'redacted' | 'full';

/** A single chat message (user or assistant), provider-neutral. */
export interface Msg {
  role: 'system' | 'user' | 'assistant' | 'tool';
  text: string;
}

export interface ToolCall {
  id: string;
  name: string;
  /** Raw arguments as sent by the model (JSON string or object). */
  arguments?: unknown;
  /** Tool result, if the turn captured one. */
  result?: unknown;
}

export interface Usage {
  /** Uncached prompt tokens. Disjoint from the two cache counts below. */
  inputTokens?: number;
  outputTokens?: number;
  /** Prompt tokens written to the cache on this turn. */
  cacheCreationInputTokens?: number;
  /** Prompt tokens served from the cache on this turn. */
  cacheReadInputTokens?: number;
}

/**
 * The canonical representation of one completed (or best-effort partial) chat
 * turn. Produced by a SiteAdapter, consumed by the pure `normalizeTurn`.
 */
export interface ChatTurn {
  site: SiteName;
  /** Conversation id — becomes both beacon.session.id and gen_ai.conversation.id. */
  sessionId: string;
  /** Stable id for this request/response pair within the conversation. */
  turnId: string;
  requestModel?: string;
  responseModel?: string;
  promptText: string;
  inputMessages: Msg[];
  outputMessages: Msg[];
  responseText: string;
  toolCalls: ToolCall[];
  usage?: Usage;
  /** epoch ms */
  startedAt: number;
  /** epoch ms; undefined => stream never completed (aborted/partial). */
  completedAt?: number;
  captureMode: 'sse' | 'dom';
  /** Trimmed raw payload for debugging; subject to the size cap. */
  raw?: unknown;
}

export interface Settings {
  enabled: boolean;
  retention: Retention;
  /** OTLP logs endpoint; only a local Threadforge receiver is accepted. */
  endpoint: string;
  /** Per-site enable toggles. */
  sites: Record<SiteName, boolean>;
}

export const DEFAULT_SETTINGS: Settings = {
  // Installation alone must not begin capturing browser conversations.
  enabled: false,
  retention: 'metadata',
  endpoint: 'http://127.0.0.1:4318/v1/logs',
  sites: { claude_web: false, chatgpt_web: false },
};

// ---- Messaging between MAIN interceptor → ISOLATED content → service worker ----

/** Emitted by the MAIN-world interceptor via window.postMessage. */
export type InterceptEvent =
  | { kind: 'request'; reqId: number; url: string; method: string; body: string | null }
  | { kind: 'chunk'; reqId: number; chunk: string }
  | { kind: 'done'; reqId: number }
  | { kind: 'error'; reqId: number };

export const THREADFORGE_MSG = '__threadforge_browser_intercept_v1__' as const;
export const THREADFORGE_CAPTURE_CONTROL = '__threadforge_browser_capture_control_v1__' as const;

/** Envelope posted on window by the interceptor. */
export interface ThreadforgeWindowMessage {
  source: typeof THREADFORGE_MSG;
  event: InterceptEvent;
}

/** Sent from the content script to the service worker. */
export interface RelayMessage {
  type: 'THREADFORGE_RAW';
  event: InterceptEvent;
}

export type SettingsPatch = Omit<Partial<Settings>, 'sites'> & {
  sites?: Partial<Record<SiteName, boolean>>;
};
