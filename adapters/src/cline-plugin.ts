import { createHash } from 'node:crypto';
import { emitEvents, markPromptHandoff, type EmitOptions, type ThreadforgeEvent } from './emit.ts';

const maxDigestBytes = 64 << 10;
const maxContextDepth = 8;
const maxContextNodes = 2_000;
const maxPluginSendMs = 250;
type JsonObject = Record<string, unknown>;
type ClineStage = 'run_start' | 'prompt' | 'tool_before' | 'tool_after' | 'run_end';

export interface ThreadforgeClinePluginOptions extends Pick<EmitOptions, 'endpoint' | 'fetchImpl'> {
  timeoutMs?: number;
}

/** Map Cline SDK plugin callbacks into digest-first observed events. */
export function mapClinePluginPayload(type: string, value: unknown, capturedAt = new Date(), setupContext?: unknown): ThreadforgeEvent[] {
  const context = { ...identity(asRecord(setupContext)), ...asRecord(value) };
  const stage = normalizeStage(type || firstString(context, ['type', 'hookName', 'hook_name', 'stage', 'event']));
  if (!stage) return [];
  const timestamp = capturedAt.toISOString();
  const taskId = safeOpaque(firstString(context, ['taskId', 'taskID', 'task_id']) || firstNestedString(context, ['task', 'run'], ['id', 'taskId', 'task_id']), 512);
  const roots = firstWorkspaceRoot(context);
  const call = firstRecord(context.toolCall, context.tool_call, context.call);
  const callId = safeOpaque(firstString(call, ['id', 'toolCallId', 'tool_call_id']) || firstString(context, ['toolCallId', 'tool_call_id', 'callId', 'call_id']), 512);
  const model = safeLabel(firstString(context, ['model', 'modelId', 'model_id']) || firstNestedString(context, ['modelInfo', 'model_info'], ['modelId', 'model_id', 'model', 'id', 'name']), 160);
  const provider = safeLabel(firstString(context, ['apiProvider', 'api_provider', 'provider', 'providerId', 'provider_id']), 96);
  const base = (action: string, category: string, suffix: string, message: string, severity: ThreadforgeEvent['severity'] = 'info'): ThreadforgeEvent => ({
    timestamp, vendor: 'beacon', product: 'endpoint-agent', schema_version: '1.0',
    event: { id: sha256(`${stage}:${taskId ?? ''}:${callId ?? ''}:${suffix}:${timestamp}`), kind: 'agent_runtime', action, category, fidelity: 'observed' },
    severity, endpoint: { os: normalizedOS(process.platform) }, harness: { name: 'cline', collection_method: 'plugin' },
    hook: { name: stage },
    ...(taskId ? { session: { id: `cline:${sha256(taskId)}` } } : {}),
    ...(model ? { model } : {}),
    ...(provider ? { gen_ai: { provider: { name: provider } } } : {}),
    raw: { source: 'cline_plugin', stage, message, ...(roots[0] ? { workspace_sha256: sha256(roots[0]) } : {}), ...(roots.length > 1 ? { workspace_count: roots.length } : {}) },
  });

  if (stage === 'run_start') {
    const events = [base('session.started', 'session', 'start', 'Cline task started')];
    const prompt = promptText(context);
    if (prompt) events.push(promptEvent(base, prompt));
    return events;
  }
  if (stage === 'prompt') {
    const prompt = promptText(context);
    return prompt ? [promptEvent(base, prompt)] : [];
  }
  if (stage === 'run_end') {
    const outcome = taskOutcome(context);
    const action = outcome === 'failed' ? 'session.error' : 'session.ended';
    const message = outcome === 'failed' ? 'Cline task failed' : outcome === 'cancelled' ? 'Cline task cancelled' : 'Cline task ended';
    const event = base(action, outcome === 'failed' ? 'error' : 'session', 'end', message, outcome === 'failed' ? 'high' : 'info');
    if (outcome === 'cancelled') event.session = { ...(asRecord(event.session)), cancel_reason: cancelReason(context) };
    if (outcome === 'failed') event.error = { type: safeLabel(firstString(asRecord(context.error), ['name', 'type', 'code']), 96) ?? 'task_error' };
    const usage = usageFields(context);
    if (Object.keys(usage).length) event.gen_ai = { ...(asRecord(event.gen_ai)), usage };
    return [event];
  }

  const toolName = safeLabel(firstString(call, ['name', 'toolName', 'tool_name', 'tool']) || firstString(context, ['toolName', 'tool_name', 'tool']), 128) ?? 'unknown';
  const toolInput = firstRecord(call.input, call.arguments, call.args, context.toolInput, context.tool_input, context.arguments, context.params);
  const response = firstRecord(call.result, call.output, context.toolResult, context.tool_result, context.response, context.result, context.output);
  if (stage === 'tool_before') {
    const event = base('tool.invoked', 'tool', `before:${toolName}`, 'Cline tool invocation observed');
    event.tool = { name: toolName };
    if (callId) event.gen_ai = mergeObject(asRecord(event.gen_ai), { tool: { call: { id: `cline-call:${sha256(callId)}` } } });
    const digest = contentDigest(toolInput);
    if (digest) event.content = digest;
    addCommandFields(event, toolName, toolInput, {});
    return [event];
  }

  const failed = explicitToolFailure(context, call, response);
  const kind = classifyTool(toolName);
  const action = failed ? 'tool.failed' : kind.action;
  const category = failed ? 'tool' : kind.category;
  const event = base(action, category, `after:${toolName}`, failed ? 'Cline tool failed' : 'Cline tool result observed', failed ? 'high' : 'info');
  event.tool = { name: toolName };
  if (callId) event.gen_ai = mergeObject(asRecord(event.gen_ai), { tool: { call: { id: `cline-call:${sha256(callId)}` } } });
  if (failed) event.error = { type: safeLabel(firstString(asRecord(context.error), ['name', 'type', 'code']) || firstString(response, ['errorType', 'error_type']), 96) ?? 'tool_error' };
  const inputDigest = contentDigest(toolInput);
  if (inputDigest) event.content = inputDigest;
  addCommandFields(event, toolName, toolInput, response);
  addFileFields(event, toolName, toolInput, response, roots[0], failed);
  addMcpFields(event, toolName, toolInput);
  const output = firstPresent(response, ['output', 'stdout', 'text', 'content', 'result']) ??
    firstPresent(call, ['result', 'output']) ?? firstPresent(context, ['result', 'output']);
  const outputDigest = contentDigest(output);
  if (outputDigest) event.content = outputDigest;
  const duration = nonNegativeInteger(firstValue(context, ['durationMs', 'duration_ms']) ?? firstValue(response, ['durationMs', 'duration_ms']));
  if (duration !== undefined) event.tool = { ...asRecord(event.tool), duration_ms: duration };
  return [event];
}

/** Create a Cline hooks plugin. Every callback resolves undefined and never writes a host decision. */
export function createThreadforgeClinePlugin(options: ThreadforgeClinePluginOptions = {}) {
  if (options.timeoutMs !== undefined && (!Number.isInteger(options.timeoutMs) || options.timeoutMs < 100 || options.timeoutMs > 10_000)) {
    throw new Error('Cline plugin timeoutMs must be between 100 and 10000');
  }
  let setup: JsonObject = {};
  const forward = async (type: string, context: unknown): Promise<void> => {
    try {
      const events = mapClinePluginPayload(type, context, new Date(), setup);
      if (events.length) await emitEvents(events, { endpoint: options.endpoint, fetchImpl: options.fetchImpl, timeoutMs: options.timeoutMs ?? maxPluginSendMs });
    } catch {
      // Cline remains authoritative: telemetry errors never throw, cancel a tool, or rewrite output.
    }
  };
  return {
    name: 'threadforge',
    manifest: { capabilities: ['hooks'] },
    setup(_api: unknown, context: unknown): void { setup = identity(asRecord(context)); },
    hooks: {
      beforeRun: (context: unknown) => forward('beforeRun', context),
      beforeTool: (context: unknown) => forward('beforeTool', context),
      afterTool: (context: unknown) => forward('afterTool', context),
      afterRun: (context: unknown) => forward('afterRun', context),
    },
  };
}

export const ThreadforgeClinePlugin = createThreadforgeClinePlugin();
export default ThreadforgeClinePlugin;

function promptEvent(
  base: (action: string, category: string, suffix: string, message: string, severity?: ThreadforgeEvent['severity']) => ThreadforgeEvent,
  prompt: string,
): ThreadforgeEvent {
  const event = base('prompt.submitted', 'prompt', 'prompt', 'Cline prompt submitted');
  event.content = contentDigest(prompt);
  markPromptHandoff(event, prompt);
  return event;
}

function normalizeStage(type: string): ClineStage | undefined {
  const normalized = type.toLowerCase().replace(/[^a-z0-9]/g, '');
  if (['beforerun', 'runstart', 'sessionstart', 'taskstart', 'taskresume'].includes(normalized)) return 'run_start';
  if (['userpromptsubmit', 'promptsubmit', 'input'].includes(normalized)) return 'prompt';
  if (['beforetool', 'toolcallbefore', 'pretooluse'].includes(normalized)) return 'tool_before';
  if (['aftertool', 'toolcallafter', 'posttooluse'].includes(normalized)) return 'tool_after';
  if (['afterrun', 'runend', 'sessionshutdown', 'taskcomplete'].includes(normalized)) return 'run_end';
  return undefined;
}

function promptText(context: JsonObject): string | undefined {
  const direct = firstPresent(context, ['prompt', 'userMessage', 'user_message', 'message', 'text']);
  const input = firstPresent(context, ['input', 'run', 'context']);
  return extractText(direct) ?? extractText(input);
}

function extractText(value: unknown): string | undefined {
  if (typeof value === 'string') return value || undefined;
  if (Array.isArray(value)) {
    const parts = value.flatMap((part) => isRecord(part) && typeof part.text === 'string' ? [part.text] : []);
    return parts.length ? parts.join('\n') : undefined;
  }
  if (!isRecord(value)) return undefined;
  const text = firstPresent(value, ['text', 'content', 'message', 'prompt']);
  return typeof text === 'string' ? text : undefined;
}

function taskOutcome(context: JsonObject): 'completed' | 'cancelled' | 'failed' {
  const result = firstRecord(context.result, context.run, context.task, context.metadata);
  const status = (firstString(context, ['status', 'outcome', 'runStatus', 'run_status', 'completionStatus', 'completion_status']) ||
    firstString(result, ['status', 'outcome', 'runStatus', 'run_status', 'completionStatus', 'completion_status'])).toLowerCase();
  if (['aborted', 'cancelled', 'canceled'].includes(status)) return 'cancelled';
  if (['failed', 'failure', 'error'].includes(status)) return 'failed';
  return 'completed';
}

function cancelReason(context: JsonObject): string {
  const result = firstRecord(context.result, context.run, context.task, context.metadata);
  const status = firstString(context, ['status', 'outcome', 'completionStatus', 'completion_status']) ||
    firstString(result, ['status', 'outcome', 'completionStatus', 'completion_status']);
  return ['aborted', 'cancelled', 'canceled'].includes(status.toLowerCase()) ? status.toLowerCase() : 'cancelled';
}

function explicitToolFailure(context: JsonObject, call: JsonObject, response: JsonObject): boolean {
  const candidates = [context.error, call.error, response.error];
  return context.isError === true || context.is_error === true || call.isError === true || call.is_error === true ||
    response.isError === true || response.is_error === true || candidates.some((value) =>
      typeof value === 'string' ? value.length > 0 : isRecord(value));
}

function classifyTool(name: string): { action: string; category: string } {
  const lower = name.toLowerCase();
  if (['list_files', 'search_files', 'list_code_definition_names', 'read_file'].includes(lower)) return { action: 'file.read', category: 'file' };
  if (['use_mcp_tool', 'access_mcp_resource'].includes(lower)) return { action: 'mcp.tool_invoked', category: 'mcp' };
  if (lower.includes('command') || lower.includes('shell') || lower.includes('terminal') || lower.includes('bash')) return { action: 'command.executed', category: 'command' };
  if (lower.includes('read')) return { action: 'file.read', category: 'file' };
  if (lower.includes('write') || lower.includes('edit') || lower.includes('replace') || lower.includes('patch') || lower.includes('create')) return { action: 'file.modified', category: 'file' };
  return { action: 'tool.completed', category: 'tool' };
}

function addCommandFields(event: ThreadforgeEvent, toolName: string, input: JsonObject, response: JsonObject): void {
  if (classifyTool(toolName).category !== 'command') return;
  const command = firstString(input, ['command', 'cmd', 'commandLine', 'command_line']);
  const output = firstPresent(response, ['output', 'stdout', 'text', 'result']);
  const outputText = extractText(output);
  const exitCode = firstValue(response, ['exit_code', 'exitCode']) ?? firstValue(input, ['exit_code', 'exitCode']);
  event.command = {
    ...(command ? { command_sha256: sha256(command), command_bytes: Buffer.byteLength(command, 'utf8') } : {}),
    ...(Number.isSafeInteger(exitCode) ? { exit_code: exitCode } : {}),
    ...(outputText ? { output_sha256: sha256(outputText), output_bytes: Buffer.byteLength(outputText, 'utf8') } : {}),
  };
}

function addFileFields(event: ThreadforgeEvent, toolName: string, input: JsonObject, response: JsonObject, root: string | undefined, failed: boolean): void {
  const classification = classifyTool(toolName);
  if (classification.category !== 'file') return;
  const sourcePath = firstString(input, ['path', 'file_path', 'filePath', 'target_file', 'targetFile']) ||
    firstString(response, ['path', 'file_path', 'filePath']);
  if (!sourcePath) return;
  const lower = toolName.toLowerCase();
  const operation = classification.action === 'file.read' ? 'read' : lower.includes('write') || lower.includes('create') ? 'create' : 'modify';
  const pathIdentity = root && !isAbsoluteLike(sourcePath) ? `${root.replace(/[\\/]+$/, '')}/${sourcePath.replace(/^[\\/]+/, '')}` : sourcePath;
  const file: JsonObject = { path_sha256: sha256(pathIdentity), operation };
  if (!failed && operation !== 'read') {
    const before = firstPresent(input, ['before', 'oldContent', 'old_content', 'originalContent', 'original_content']);
    const after = firstPresent(input, ['after', 'newContent', 'new_content', 'content', 'diff']);
    if (before !== undefined) file.before_content = contentDigest(before);
    if (after !== undefined) file.after_content = contentDigest(after);
    if (before !== undefined || after !== undefined) event.content = contentDigest({ before, after });
  }
  if (!failed) event.file = file;
}

function addMcpFields(event: ThreadforgeEvent, toolName: string, input: JsonObject): void {
  if (!['use_mcp_tool', 'access_mcp_resource'].includes(toolName.toLowerCase())) return;
  const server = safeLabel(firstString(input, ['serverName', 'server_name', 'server']), 96);
  const tool = safeLabel(firstString(input, ['toolName', 'tool_name', 'tool', 'resource']), 128);
  event.mcp = { ...(server ? { server } : {}), ...(tool ? { tool } : {}) };
}

function usageFields(context: JsonObject): JsonObject {
  const usage = firstRecord(context.usage, context.tokens, firstRecord(context.result, context.run, context.output, context.metrics).usage);
  const out: JsonObject = {};
  const numeric = (names: string[]) => nonNegativeInteger(firstValue(usage, names));
  const input = numeric(['inputTokens', 'input_tokens', 'tokensIn', 'tokens_in', 'promptTokens', 'prompt_tokens']);
  const output = numeric(['outputTokens', 'output_tokens', 'tokensOut', 'tokens_out', 'completionTokens', 'completion_tokens']);
  const cacheRead = numeric(['cacheReadTokens', 'cache_read_tokens', 'cachedTokens', 'cached_tokens']);
  const cacheWrite = numeric(['cacheWriteTokens', 'cache_write_tokens', 'cacheCreationTokens', 'cache_creation_tokens']);
  if (input !== undefined) out.input_tokens = input;
  if (output !== undefined) out.output_tokens = output;
  if (cacheRead !== undefined) out.cache_read = { input_tokens: cacheRead };
  if (cacheWrite !== undefined) out.cache_creation = { input_tokens: cacheWrite };
  const cost = firstValue(usage, ['totalCost', 'total_cost', 'costUsd', 'cost_usd', 'cost']);
  if (typeof cost === 'number' && Number.isFinite(cost) && cost >= 0 && cost <= 1_000_000_000) out.cost_usd = cost;
  return out;
}

function identity(context: JsonObject): JsonObject {
  const task = firstRecord(context.task, context.run);
  const workspace = firstRecord(context.workspaceInfo, context.workspace_info, context.workspace);
  const taskId = firstString(context, ['taskId', 'taskID', 'task_id']) || firstString(task, ['id', 'taskId', 'task_id']);
  const roots = firstWorkspaceRoot(context);
  const cwd = firstString(context, ['cwd', 'workingDirectory', 'working_directory']) ||
    firstString(workspace, ['rootPath', 'root_path', 'cwd', 'path']);
  const identity: JsonObject = {};
  if (taskId) identity.taskId = taskId;
  if (roots.length) identity.workspaceRoots = roots;
  else if (cwd) identity.cwd = cwd;
  for (const key of ['model', 'modelId', 'model_id', 'apiProvider', 'api_provider', 'provider', 'providerId', 'provider_id', 'clineVersion', 'cline_version', 'version']) {
    if (context[key] !== undefined) identity[key] = context[key];
  }
  return identity;
}

function firstWorkspaceRoot(context: JsonObject): string[] {
  const workspace = firstRecord(context.workspaceInfo, context.workspace_info, context.workspace);
  const roots = firstPresent(context, ['workspaceRoots', 'workspace_roots', 'workspaceFolders']) ?? workspace.workspaceRoots ?? workspace.roots;
  const values = Array.isArray(roots) ? roots : roots === undefined ? [] : [roots];
  return values.flatMap((value) => {
    if (typeof value === 'string' && value.length <= 4_096) return [value];
    if (isRecord(value)) {
      const candidate = firstString(value, ['path', 'uri', 'fsPath', 'fs_path', 'rootPath', 'root_path']);
      return candidate && candidate.length <= 4_096 ? [candidate] : [];
    }
    return [];
  }).slice(0, 16);
}

function contentDigest(value: unknown): JsonObject | undefined {
  if (value === undefined || value === null) return undefined;
  const serialized = stableSerialize(value);
  if (serialized === undefined) return undefined;
  const bytes = Buffer.byteLength(serialized, 'utf8');
  return { retention: 'metadata', included: false, ...(bytes <= maxDigestBytes ? { hash: sha256(serialized) } : { omitted: true }), bytes };
}

function stableSerialize(value: unknown): string | undefined {
  let remaining = maxContextNodes;
  const clone = (input: unknown, depth: number, active: WeakSet<object>): unknown => {
    remaining--;
    if (remaining < 0 || depth >= maxContextDepth) return '[bounded]';
    if (input === null || typeof input === 'string' || typeof input === 'number' || typeof input === 'boolean') return input;
    if (typeof input === 'bigint') return String(input);
    if (typeof input !== 'object') return undefined;
    if (active.has(input)) return '[cycle]';
    if (input instanceof Date) return input.toISOString();
    if (input instanceof Error) return { name: input.name, message: input.message };
    active.add(input);
    try {
      if (Array.isArray(input)) return input.slice(0, 256).map((entry) => clone(entry, depth + 1, active));
      const object = input as JsonObject;
      const out: JsonObject = {};
      for (const key of Object.keys(object).sort().slice(0, 256)) {
        const item = clone(object[key], depth + 1, active);
        if (item !== undefined) out[key] = item;
      }
      return out;
    } finally { active.delete(input); }
  };
  try { return JSON.stringify(clone(value, 0, new WeakSet<object>())); }
  catch { return undefined; }
}

function mergeObject(left: JsonObject, right: JsonObject): JsonObject {
  const out: JsonObject = { ...left };
  for (const [key, value] of Object.entries(right)) {
    if (isRecord(out[key]) && isRecord(value)) out[key] = mergeObject(out[key] as JsonObject, value);
    else out[key] = value;
  }
  return out;
}
function firstNestedString(source: JsonObject, parents: string[], keys: string[]): string {
  for (const parent of parents) {
    const value = firstRecord(source[parent]);
    const found = firstString(value, keys);
    if (found) return found;
  }
  return '';
}
function firstString(source: JsonObject, keys: string[]): string {
  for (const key of keys) if (typeof source[key] === 'string' && source[key]) return source[key] as string;
  return '';
}
function firstPresent(source: JsonObject, keys: string[]): unknown {
  for (const key of keys) if (source[key] !== undefined && source[key] !== null) return source[key];
  return undefined;
}
function firstValue(source: JsonObject, keys: string[]): unknown {
  for (const key of keys) if (source[key] !== undefined && source[key] !== null) return source[key];
  return undefined;
}
function firstRecord(...values: unknown[]): JsonObject {
  for (const value of values) if (isRecord(value)) return value;
  return {};
}
function asRecord(value: unknown): JsonObject { return isRecord(value) ? value : {}; }
function safeOpaque(value: string, max: number): string | undefined { return value.length <= max && /^[A-Za-z0-9_.:-]+$/.test(value) ? value : undefined; }
function safeLabel(value: string, max: number): string | undefined {
  return value.length <= max && /^[A-Za-z0-9][A-Za-z0-9_.:/+-]*$/.test(value) && !looksSecretLike(value) ? value : undefined;
}
function looksSecretLike(value: string): boolean {
  return /\b(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|password|secret|authorization)\b\s*[:=]\s*\S+/i.test(value) ||
    /\bBearer\s+[A-Za-z0-9._~+/-]+=*/i.test(value) || /\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16})\b/.test(value);
}
function isAbsoluteLike(value: string): boolean { return value.startsWith('/') || value.startsWith('\\') || /^[A-Za-z]:[\\/]/.test(value); }
function nonNegativeInteger(value: unknown): number | undefined { return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : undefined; }
function isRecord(value: unknown): value is JsonObject { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function sha256(value: string): string { return createHash('sha256').update(value, 'utf8').digest('hex'); }
function normalizedOS(platform: string): string { return platform === 'win32' ? 'windows' : platform === 'darwin' ? 'macos' : platform === 'linux' ? 'linux' : 'other'; }
