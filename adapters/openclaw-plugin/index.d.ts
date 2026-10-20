export type OpenClawHook =
  | 'session_start' | 'session_end' | 'message_received' | 'before_tool_call' | 'after_tool_call'
  | 'llm_output' | 'before_compaction' | 'after_compaction' | 'subagent_spawned' | 'subagent_ended';

export interface OpenClawEvent {
  timestamp: string;
  vendor: 'beacon';
  product: 'endpoint-agent';
  schema_version: '1.0';
  event: { kind: 'agent_runtime'; action: string; category?: string; fidelity: 'observed'; id?: string };
  severity: 'info' | 'low' | 'medium' | 'high' | 'critical';
  endpoint: { os: string };
  harness: { name: 'openclaw_gateway'; collection_method: 'plugin' };
  raw: Record<string, unknown>;
  [key: string]: unknown;
}

export interface OpenClawPluginAPI {
  on(hook: OpenClawHook, handler: (value: unknown, context?: unknown) => unknown): void;
}

export const openClawHooks: readonly OpenClawHook[];
export function mapOpenClawHook(
  hook: string,
  value: unknown,
  context?: unknown,
  capturedAt?: Date,
  pending?: Map<string, string[]>,
): OpenClawEvent[];
export function createThreadforgeOpenClawPlugin(
  send?: (event: OpenClawEvent) => unknown | Promise<unknown>,
  queueLimit?: number,
): {
  hooks: OpenClawHook[];
  register(api: OpenClawPluginAPI): void;
  flush(): Promise<void>;
  droppedEvents(): number;
};

declare const openClawPlugin: {
  id: string;
  name: string;
  description: string;
  register(api: OpenClawPluginAPI): void;
};

export default openClawPlugin;
