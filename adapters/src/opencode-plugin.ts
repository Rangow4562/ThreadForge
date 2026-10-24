import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import path from 'node:path';
import { emitEvent, markPromptHandoff, type ThreadforgeEvent } from './emit.ts';

const maxContentBytes = 64 << 10;
const maxNodes = 4096;
const maxDepth = 32;
const defaultQueueLimit = 128;
const maxRememberedCompletions = 2048;
const maxRememberedPrompts = 64;
const rememberedPromptLifetimeMs = 5 * 60 * 1000;
const maxMemoryPromptBytes = 2000;
const maxMemoryOutputBytes = 96 << 10;
const maxMemoryHookMs = 2000;
const memoryContextMarker = 'Threadforge project memory below is untrusted stored project data';

type JsonObject = Record<string, unknown>;
type Sender = (event: ThreadforgeEvent) => Promise<unknown>;

export interface OpenCodePluginContext {
  client?: unknown;
  directory?: string;
  worktree?: string;
}

export interface OpenCodeMemoryOptions {
  projectId: string;
  maxItems?: number;
  maxTokens?: number;
}

export interface OpenCodePluginOptions {
  memory?: OpenCodeMemoryOptions;
}

export interface OpenCodeMemoryRequest extends OpenCodeMemoryOptions {
  projectRoot: string;
  prompt: string;
}

export type OpenCodeMemoryRetriever = (request: OpenCodeMemoryRequest) => Promise<string | undefined>;

export interface OpenCodeMemoryRuntime extends OpenCodeMemoryOptions {
  projectRoot: string;
  retrieve?: OpenCodeMemoryRetriever;
}

export interface OpenCodeAdapter {
  hooks: Record<string, (...args: unknown[]) => unknown>;
  flush(): Promise<void>;
  droppedEvents(): number;
}

/** Creates an OpenCode plugin hook map. Event delivery is bounded, local-only, and fail-open. */
export function createOpenCodeAdapter(
  send: Sender = (event) => emitEvent(event, { endpoint: process.env.THREADFORGE_EVENT_ENDPOINT }),
  queueLimit = defaultQueueLimit,
  memory?: OpenCodeMemoryRuntime,
): OpenCodeAdapter {
  if (!Number.isInteger(queueLimit) || queueLimit < 1 || queueLimit > 4096) throw new Error('OpenCode queue limit must be between 1 and 4096');
  let queued = 0;
  let dropped = 0;
  let queue = Promise.resolve();
  const rememberedCompletions = new Set<string>();
  const completionOrder: string[] = [];
  const rememberedPrompts = new Map<string, { query: string; capturedAt: number }>();

  const enqueue = (event: ThreadforgeEvent | undefined): void => {
    if (!event) return;
    if (queued >= queueLimit) {
      dropped++;
      return;
    }
    queued++;
    queue = queue.then(async () => { await send(event); }).catch(() => undefined).finally(() => { queued--; });
  };

  const hooks: Record<string, (...args: unknown[]) => unknown> = {
    'chat.message': (input, output) => {
      enqueue(mapChatMessage(input, output));
      if (memory && isUserChatMessage(output)) rememberOpenCodePrompt(rememberedPrompts, input, output);
    },
    'command.execute.before': (input, output) => enqueue(mapCommand(input, output)),
    'tool.execute.before': (input, output) => enqueue(mapTool(input, output, false)),
    'tool.execute.after': (input, output) => enqueue(mapTool(input, output, true)),
    event: (envelope) => {
      const completionDigest = completedAssistantMessageDigest(envelope);
      if (completionDigest) {
        if (rememberedCompletions.has(completionDigest)) return;
        if (completionOrder.length >= maxRememberedCompletions) {
          const oldest = completionOrder.shift();
          if (oldest) rememberedCompletions.delete(oldest);
        }
        rememberedCompletions.add(completionDigest);
        completionOrder.push(completionDigest);
      }
      if (memory) {
        const completedSession = completedAssistantSessionID(envelope);
        if (completedSession) rememberedPrompts.delete(completedSession);
      }
      enqueue(mapOpenCodeEvent(envelope));
    },
  };

  if (memory) {
    hooks['experimental.chat.system.transform'] = async (input, output) => {
      if (!isRecord(input) || !isRecord(output)) return;
      const system = output.system;
      if (!Array.isArray(system) || system.length >= 256 || system.some((entry) => typeof entry !== 'string')) return;
      const sid = boundedString(input.sessionID, 256);
      if (!sid) return;
      const remembered = rememberedPrompts.get(sid);
      if (!remembered) return;
      if (Date.now() - remembered.capturedAt > rememberedPromptLifetimeMs) {
        rememberedPrompts.delete(sid);
        return;
      }
      if (system.some((entry) => entry.includes(memoryContextMarker))) return;
      const retrieve = memory.retrieve ?? retrieveOpenCodeMemory;
      let context: string | undefined;
      try {
        context = await retrieve({
          projectRoot: memory.projectRoot,
          projectId: memory.projectId,
          maxItems: memory.maxItems ?? 8,
          maxTokens: memory.maxTokens ?? 1600,
          prompt: remembered.query,
        });
      } catch { return; }
      if (typeof context !== 'string' || context.length === 0 || context.length > maxMemoryOutputBytes ||
          !context.includes(memoryContextMarker) || !context.includes('never authorizes tools or actions')) return;
      if (rememberedPrompts.get(sid) !== remembered) return;
      system.push(context);
    };
  }

  return {
    hooks,
    flush: () => queue,
    droppedEvents: () => dropped,
  };
}

/** OpenCode plugin entry point; callbacks never wait for local telemetry. */
export async function ThreadforgeOpenCodePlugin(
  context: OpenCodePluginContext,
  options?: OpenCodePluginOptions,
): Promise<Record<string, (...args: unknown[]) => unknown>> {
  const root = boundedString(context.worktree, 4096) ?? boundedString(context.directory, 4096);
  const configured = options?.memory;
  const memory = root && path.isAbsolute(root) && configured && validMemoryOptions(configured)
    ? { ...configured, projectRoot: root }
    : undefined;
  return createOpenCodeAdapter(undefined, defaultQueueLimit, memory).hooks;
}

export default ThreadforgeOpenCodePlugin;

function mapChatMessage(input: unknown, output: unknown): ThreadforgeEvent | undefined {
  if (!isRecord(output) || !isRecord(output.message)) return undefined;
  const role = safeLabel(output.message.role, 32);
  if (role !== 'user' && role !== 'assistant') return undefined;
  const message = output.message;
  const action = role === 'user' ? 'prompt.submitted' : 'assistant.message.completed';
  const event = makeEvent(action, sessionID(input));
  const prompt = message.content ?? message.parts ?? message.text;
  const digest = contentDigest(prompt);
  if (digest) event.content = digest;
  if (role === 'user') markPromptHandoff(event, prompt);
  const model = modelLabel(input);
  if (model) event.model = model;
  return event;
}

function isUserChatMessage(output: unknown): boolean {
  return isRecord(output) && isRecord(output.message) && safeLabel(output.message.role, 32) === 'user';
}

function rememberOpenCodePrompt(
  remembered: Map<string, { query: string; capturedAt: number }>, input: unknown, output: unknown,
): void {
  const sid = sessionID(input);
  const query = chatMessageText(output);
  if (!sid || !query) return;
  const now = Date.now();
  for (const [key, value] of remembered) {
    if (now - value.capturedAt > rememberedPromptLifetimeMs) remembered.delete(key);
  }
  if (remembered.has(sid)) remembered.delete(sid);
  while (remembered.size >= maxRememberedPrompts) {
    const oldest = remembered.keys().next().value as string | undefined;
    if (!oldest) break;
    remembered.delete(oldest);
  }
  remembered.set(sid, { query, capturedAt: now });
}

function chatMessageText(output: unknown): string | undefined {
  if (!isRecord(output) || !isRecord(output.message)) return undefined;
  const message = output.message;
  for (const candidate of [message.content, message.parts, message.text]) {
    const text = textFromChatContent(candidate);
    if (text) return truncateUTF8(text.trim(), maxMemoryPromptBytes);
  }
  return undefined;
}

function textFromChatContent(value: unknown): string | undefined {
  if (typeof value === 'string') return value;
  if (!Array.isArray(value)) return undefined;
  const parts = value.filter((part): part is JsonObject => isRecord(part) && part.type === 'text' && typeof part.text === 'string');
  return parts.length > 0 ? parts.map((part) => part.text as string).join('\n') : undefined;
}

function truncateUTF8(value: string, maxBytes: number): string {
  let output = '';
  let bytes = 0;
  for (const character of value) {
    const size = Buffer.byteLength(character, 'utf8');
    if (bytes + size > maxBytes) break;
    output += character;
    bytes += size;
  }
  return output;
}

function completedAssistantSessionID(envelope: unknown): string | undefined {
  if (!isRecord(envelope) || !isRecord(envelope.event) || envelope.event.type !== 'message.updated') return undefined;
  const properties = isRecord(envelope.event.properties) ? envelope.event.properties :
    isRecord(envelope.event.data) ? envelope.event.data : undefined;
  const info = properties && isRecord(properties.info) ? properties.info : undefined;
  const time = info && isRecord(info.time) ? info.time : undefined;
  if (safeLabel(info?.role, 32) !== 'assistant' || typeof time?.completed !== 'number' || !Number.isFinite(time.completed) || time.completed <= 0) {
    return undefined;
  }
  return sessionID(properties) ?? sessionID(info);
}

function validMemoryOptions(options: OpenCodeMemoryOptions): boolean {
  const maxItems = options.maxItems ?? 8;
  const maxTokens = options.maxTokens ?? 1600;
  return /^[a-z][a-z0-9-]{0,63}$/.test(options.projectId) &&
    Number.isInteger(maxItems) && maxItems >= 1 && maxItems <= 32 &&
    Number.isInteger(maxTokens) && maxTokens >= 1 && maxTokens <= 16000;
}

async function retrieveOpenCodeMemory(request: OpenCodeMemoryRequest): Promise<string | undefined> {
  const command = process.env.THREADFORGE_CLI?.trim() || 'threadforge';
  if (command.length > 4096 || /[\r\n\0]/.test(command)) return undefined;
  const input = JSON.stringify({
    hook_event_name: 'UserPromptSubmit',
    cwd: request.projectRoot,
    prompt: request.prompt,
  });
  if (Buffer.byteLength(input, 'utf8') > (8 << 10)) return undefined;
  const args = [
    'memory', 'inject-opencode', '--project-id', request.projectId,
    '--max-items', String(request.maxItems ?? 8), '--max-tokens', String(request.maxTokens ?? 1600),
    '--threadforge-managed-opencode-memory=v1', '--threadforge-memory=opencode-v1',
  ];
  return new Promise((resolve) => {
    let child;
    try {
      child = spawn(command, args, {
        cwd: request.projectRoot,
        env: localThreadforgeEnvironment(),
        stdio: ['pipe', 'pipe', 'ignore'],
        windowsHide: true,
      });
    } catch { resolve(undefined); return; }
    const chunks: Buffer[] = [];
    let received = 0;
    let settled = false;
    const finish = (value?: string) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(value);
    };
    const timer = setTimeout(() => {
      child.kill();
      finish();
    }, maxMemoryHookMs);
    child.stdout.on('data', (chunk: Buffer | string) => {
      const data = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      received += data.length;
      if (received > maxMemoryOutputBytes) {
        child.kill();
        finish();
        return;
      }
      chunks.push(data);
    });
    child.once('error', () => finish());
    child.once('close', (code) => {
      if (code !== 0) return finish();
      try {
        const decoded = JSON.parse(Buffer.concat(chunks).toString('utf8')) as unknown;
        if (!isRecord(decoded) || Object.keys(decoded).length !== 1 || !Array.isArray(decoded.system) || decoded.system.length > 1) {
          return finish();
        }
        if (decoded.system.length === 0) return finish('');
        const context = decoded.system[0];
        finish(typeof context === 'string' ? context : undefined);
      } catch { finish(); }
    });
    child.stdin.on('error', () => {});
    child.stdin.end(input, 'utf8');
  });
}

function localThreadforgeEnvironment(): NodeJS.ProcessEnv {
  const allowed = [
    'PATH', 'PATHEXT', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'TEMP', 'TMP', 'HOME', 'USERPROFILE',
    'APPDATA', 'LOCALAPPDATA', 'XDG_CACHE_HOME', 'THREADFORGE_HOME', 'THREADFORGE_DB', 'THREADFORGE_TRACE_FILE',
  ];
  return Object.fromEntries(allowed.flatMap((key) => process.env[key] === undefined ? [] : [[key, process.env[key]]])) as NodeJS.ProcessEnv;
}

function mapCommand(input: unknown, output: unknown): ThreadforgeEvent | undefined {
  if (!isRecord(input)) return undefined;
  const event = makeEvent('command.invoked', sessionID(input));
  const commandName = safeLabel(input.command, 128);
  if (commandName) event.raw = { command_name: commandName };
  if (isRecord(output) && Array.isArray(output.parts)) event.raw = { ...(event.raw as JsonObject ?? {}), command_part_count: output.parts.length };
  return event;
}

function mapTool(input: unknown, output: unknown, after: boolean): ThreadforgeEvent | undefined {
  if (!isRecord(input)) return undefined;
  const metadata = after && isRecord(output) && isRecord(output.metadata) ? output.metadata : undefined;
  const error = after && isRecord(output) && (output.error !== undefined || Boolean(metadata && numericFailure(metadata)));
  const event = makeEvent(after ? (error ? 'tool.failed' : 'tool.completed') : 'tool.invoked', sessionID(input));
  const toolName = safeLabel(input.tool, 128);
  if (toolName) event.tool = { name: toolName };
  const callID = boundedString(input.callID, 256);
  if (callID) event.raw = { tool_call_id_sha256: hash(callID) };
  const content = after ? output : isRecord(output) ? output.args : undefined;
  const digest = contentDigest(content);
  if (digest) event.content = digest;
  if (error) event.severity = 'medium';
  return event;
}

function mapOpenCodeEvent(envelope: unknown): ThreadforgeEvent | undefined {
  if (!isRecord(envelope) || !isRecord(envelope.event)) return undefined;
  const type = boundedString(envelope.event.type, 96);
  if (!type) return undefined;
  const properties = isRecord(envelope.event.properties) ? envelope.event.properties :
    isRecord(envelope.event.data) ? envelope.event.data : {};
  const sid = sessionID(properties);
  if (type === 'message.updated') {
    const info = isRecord(properties.info) ? properties.info : undefined;
    const time = info && isRecord(info.time) ? info.time : undefined;
    if (safeLabel(info?.role, 32) !== 'assistant' || typeof time?.completed !== 'number' || !Number.isFinite(time.completed) || time.completed <= 0) {
      return undefined;
    }
    return makeEvent('assistant.message.completed', sid ?? sessionID(info));
  }
  const actions: Record<string, string> = {
    'session.created': 'session.started',
    'session.deleted': 'session.ended',
    'session.idle': 'session.idle',
    'session.status': 'session.status.changed',
    'session.error': 'session.error',
    'permission.asked': 'permission.requested',
    'permission.v2.asked': 'permission.requested',
    'command.executed': 'command.executed',
    'session.diff': 'file.diff_observed',
  };
  if (type === 'permission.replied' || type === 'permission.v2.replied') {
    const decision = permissionDecision(properties.response ?? properties.reply);
    if (!decision) return undefined;
    const event = makeEvent(decision === 'denied' ? 'permission.denied' : 'permission.allowed', sid);
    event.approval = { required: true, decision };
    return event;
  }
  const action = actions[type];
  if (!action) return undefined;
  const event = makeEvent(action, sid);
  if (action === 'permission.requested') event.approval = { required: true };
  if (type === 'session.status') {
    const status = isRecord(properties.status) ? safeLabel(properties.status.type, 64) : undefined;
    if (status) event.raw = { session_status: status };
  }
  if (type === 'session.diff') {
    const diffs = Array.isArray(properties.diff) ? properties.diff : [];
    event.raw = { diff_count: Math.min(diffs.length, 10000) };
  }
  if (type === 'command.executed') {
    const commandName = safeLabel(properties.name ?? properties.command, 128);
    if (commandName) event.raw = { command_name: commandName };
  }
  if (type === 'session.error') event.severity = 'medium';
  return event;
}

function completedAssistantMessageDigest(envelope: unknown): string | undefined {
  if (!isRecord(envelope) || !isRecord(envelope.event) || envelope.event.type !== 'message.updated') return undefined;
  const properties = isRecord(envelope.event.properties) ? envelope.event.properties :
    isRecord(envelope.event.data) ? envelope.event.data : undefined;
  const info = properties && isRecord(properties.info) ? properties.info : undefined;
  const time = info && isRecord(info.time) ? info.time : undefined;
  if (safeLabel(info?.role, 32) !== 'assistant' || typeof time?.completed !== 'number' || !Number.isFinite(time.completed) || time.completed <= 0) {
    return undefined;
  }
  const id = boundedString(info?.id, 256);
  return id ? hash(id) : undefined;
}

function makeEvent(action: string, session: string | undefined): ThreadforgeEvent {
  const event: ThreadforgeEvent = {
    timestamp: new Date().toISOString(),
    vendor: 'beacon',
    product: 'endpoint-agent',
    schema_version: '1.0',
    event: { kind: 'agent_runtime', action, fidelity: 'observed' },
    severity: 'info',
    endpoint: { os: normalizedOS(process.platform) },
    harness: { name: 'opencode', collection_method: 'plugin' },
  };
  if (session) event.session = { id: `opencode:${hash(session)}` };
  return event;
}

function contentDigest(value: unknown): JsonObject | undefined {
  if (value === undefined || value === null) return undefined;
  const state = { nodes: 0, bytes: 0 };
  if (!withinContentLimits(value, state, 0, new WeakSet())) return undefined;
  let serialized: string;
  try { serialized = typeof value === 'string' ? value : JSON.stringify(canonicalize(value)); }
  catch { return undefined; }
  if (typeof serialized !== 'string') return undefined;
  const bytes = Buffer.byteLength(serialized, 'utf8');
  if (bytes > maxContentBytes) return undefined;
  return { retention: 'metadata', included: false, hash: hash(serialized), bytes };
}

function withinContentLimits(value: unknown, state: { nodes: number; bytes: number }, depth: number, seen: WeakSet<object>): boolean {
  state.nodes++;
  if (state.nodes > maxNodes || depth > maxDepth) return false;
  if (typeof value === 'string') {
    state.bytes += Buffer.byteLength(JSON.stringify(value), 'utf8');
    return state.bytes <= maxContentBytes;
  }
  if (value === null || typeof value === 'boolean' || typeof value === 'number') {
    state.bytes += Buffer.byteLength(JSON.stringify(value), 'utf8');
    return state.bytes <= maxContentBytes;
  }
  if (Array.isArray(value)) {
    if (seen.has(value)) return false;
    seen.add(value);
    for (const item of value) if (!withinContentLimits(item, state, depth + 1, seen)) return false;
    seen.delete(value);
    return state.bytes + Math.max(0, value.length - 1) <= maxContentBytes;
  }
  if (isRecord(value)) {
    if (seen.has(value)) return false;
    seen.add(value);
    const keys = Object.keys(value).sort();
    for (const key of keys) {
      state.bytes += Buffer.byteLength(JSON.stringify(key), 'utf8') + 1;
      if (state.bytes > maxContentBytes || !withinContentLimits(value[key], state, depth + 1, seen)) return false;
    }
    seen.delete(value);
    return state.bytes + Math.max(0, keys.length - 1) <= maxContentBytes;
  }
  return false;
}

function canonicalize(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (isRecord(value)) return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}

function sessionID(value: unknown): string | undefined {
  if (!isRecord(value)) return undefined;
  const direct = boundedString(value.sessionID ?? value.session_id, 256);
  if (direct) return direct;
  if (isRecord(value.session)) {
    const nested = boundedString(value.session.id, 256);
    if (nested) return nested;
  }
  if (isRecord(value.info)) {
    const nested = boundedString(value.info.sessionID, 256);
    if (nested) return nested;
  }
  return undefined;
}

function modelLabel(value: unknown): string | undefined {
  if (!isRecord(value)) return undefined;
  const provider = safeLabel(value.providerID, 64);
  const model = safeLabel(value.modelID, 96);
  return provider && model ? `${provider}/${model}` : undefined;
}

function permissionDecision(value: unknown): 'allowed' | 'denied' | undefined {
  const normalized = typeof value === 'string' ? value.toLowerCase() : '';
  if (normalized === 'once' || normalized === 'always' || normalized === 'allow') return 'allowed';
  if (normalized === 'reject' || normalized === 'deny') return 'denied';
  return undefined;
}

function numericFailure(value: JsonObject): boolean {
  const exit = value.exit ?? value.exitCode ?? value.exit_code;
  return typeof exit === 'number' && exit !== 0;
}

function boundedString(value: unknown, max: number): string | undefined {
  if (typeof value !== 'string') return undefined;
  const text = value.trim();
  return text.length > 0 && text.length <= max ? text : undefined;
}

function safeLabel(value: unknown, max: number): string | undefined {
  const text = boundedString(value, max);
  if (!text || !/^[A-Za-z0-9][A-Za-z0-9_.:/-]*$/.test(text) ||
      /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(text) ||
      /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(text)) return undefined;
  return text;
}

function hash(value: string): string {
  return createHash('sha256').update(value, 'utf8').digest('hex');
}

function normalizedOS(platform: string): string {
  if (platform === 'win32') return 'windows';
  if (platform === 'darwin') return 'macos';
  if (platform === 'linux') return 'linux';
  return 'other';
}

function isRecord(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
