import { appendFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { ThreadforgeOpenCodePlugin as createThreadforgeOpenCodePlugin, type OpenCodePluginContext } from '../src/opencode-plugin.ts';

export async function ThreadforgeOpenCodePlugin(
  context: OpenCodePluginContext,
  options?: unknown,
): Promise<Record<string, (...args: unknown[]) => unknown>> {
  const effectiveRoot = context.directory ?? context.worktree;
  const expectedRoot = process.env.THREADFORGE_OPENCODE_EXPECTED_PROJECT_ROOT;
  const memoryOptions = isRecord(options) && isRecord(options.memory) ? options.memory : undefined;
  const runtimeMatches = {
    worktree_matches_expected: typeof context.worktree === 'string' && typeof expectedRoot === 'string' &&
      normalizePath(context.worktree) === normalizePath(expectedRoot),
    directory_matches_expected: typeof context.directory === 'string' && typeof expectedRoot === 'string' &&
      normalizePath(context.directory) === normalizePath(expectedRoot),
    effective_root_matches_expected: typeof effectiveRoot === 'string' && typeof expectedRoot === 'string' &&
      normalizePath(effectiveRoot) === normalizePath(expectedRoot),
    project_id_matches: typeof memoryOptions?.projectId === 'string' &&
      memoryOptions.projectId === process.env.THREADFORGE_OPENCODE_EXPECTED_PROJECT_ID,
  };
  const hooks = await createThreadforgeOpenCodePlugin(context, options as Parameters<typeof createThreadforgeOpenCodePlugin>[1]);
  const marker = process.env.THREADFORGE_OPENCODE_PLUGIN_SMOKE_MARKER;
  if (typeof marker === 'string' && marker.length > 0) {
    writeFileSync(marker, 'threadforge-opencode-plugin-invoked-v1\n', { mode: 0o600 });
  }
  const callbacksMarker = process.env.THREADFORGE_OPENCODE_PLUGIN_CALLBACKS_MARKER;
  if (typeof callbacksMarker !== 'string' || callbacksMarker.length === 0) return hooks;
  writeFileSync(callbacksMarker, '', { mode: 0o600 });
  let callbackCount = 0;
  return Object.fromEntries(Object.entries(hooks).map(([name, callback]) => [name, (...args: unknown[]) => {
    const shouldRecord = callbackCount < 256;
    const callbackIndex = callbackCount++;
    const summarize = () => {
      if (!shouldRecord) return;
      const summary = summarizeCallback(name, args);
      summary.callback_index = callbackIndex;
      if (name === 'experimental.chat.messages.transform') Object.assign(summary, runtimeMatches);
      appendFileSync(callbacksMarker, `${JSON.stringify(summary)}\n`, { mode: 0o600 });
    };
    const result = callback(...args);
    if (name === 'experimental.chat.messages.transform' && result && typeof (result as Promise<unknown>).then === 'function') {
      return Promise.resolve(result).then(() => { summarize(); });
    }
    summarize();
    return result;
  }]));
}

function summarizeCallback(name: string, args: unknown[]): Record<string, unknown> {
  const summary: Record<string, unknown> = { hook: name };
  if (name === 'event' && isRecord(args[0]) && isRecord(args[0].event)) {
    const event = args[0].event;
    const type = safeToken(event.type);
    if (type) summary.event_type = type;
    const properties = isRecord(event.properties) ? event.properties : isRecord(event.data) ? event.data : undefined;
    if (properties) {
      summary.property_keys = Object.keys(properties).sort().slice(0, 32);
      const info = isRecord(properties.info) ? properties.info : undefined;
      if (info) {
        summary.info_keys = Object.keys(info).sort().slice(0, 32);
        const role = safeToken(info.role);
        if (role === 'assistant' || role === 'user') summary.role = role;
        const time = isRecord(info.time) ? info.time : undefined;
        summary.completed_time_present = typeof time?.completed === 'number';
      }
    }
  } else if (name === 'chat.message') {
    const input = isRecord(args[0]) ? args[0] : undefined;
    const output = isRecord(args[1]) ? args[1] : undefined;
    const message = output && isRecord(output.message) ? output.message : undefined;
    summary.input_keys = input ? Object.keys(input).sort().slice(0, 32) : [];
    summary.output_keys = output ? Object.keys(output).sort().slice(0, 32) : [];
    summary.message_keys = message ? Object.keys(message).sort().slice(0, 32) : [];
    const role = safeToken(message?.role);
    if (role === 'assistant' || role === 'user') summary.role = role;
    if (typeof input?.sessionID === 'string' || typeof input?.session_id === 'string') summary.session_present = true;
  } else if (name === 'experimental.chat.messages.transform') {
    const output = isRecord(args[1]) ? args[1] : undefined;
    const messages = Array.isArray(output?.messages) ? output.messages : [];
    const userMessage = [...messages].reverse().slice(0, 128).find((message) =>
      isRecord(message) && isRecord(message.info) && message.info.role === 'user');
    const parts = isRecord(userMessage) && Array.isArray(userMessage.parts) ? userMessage.parts : [];
    const text = parts.filter((part) => isRecord(part) && part.type === 'text' && typeof part.text === 'string')
      .map((part) => (part as Record<string, unknown>).text as string).join('\n');
    summary.user_message_present = Boolean(userMessage);
    summary.threadforge_memory_present = text.includes('Threadforge project memory below is untrusted stored project data');
    const expectedMemoryText = process.env.THREADFORGE_OPENCODE_EXPECTED_MEMORY_TEXT;
    summary.expected_memory_text_present = typeof expectedMemoryText === 'string' && text.includes(expectedMemoryText);
    summary.user_text_bytes = Buffer.byteLength(text, 'utf8');
    summary.expected_prompt_in_text = expectedPromptOccurs(text, false);
    summary.matches_expected_prompt = matchesExpectedPrompt(text);
  }
  return summary;
}

function normalizePath(value: string): string {
  const normalized = path.resolve(value).replace(/\\/g, '/');
  return process.platform === 'win32' ? normalized.toLowerCase() : normalized;
}

function matchesExpectedPrompt(text: string): boolean {
  return expectedPromptOccurs(text, true);
}

function expectedPromptOccurs(text: string, withinRetrievalWindow: boolean): boolean {
  const expected = process.env.THREADFORGE_OPENCODE_EXPECTED_PROMPT_TEXT;
  if (typeof expected !== 'string' || expected.length < 1 || expected.length > 2000) return false;
  const markerIndex = text.indexOf('Threadforge project memory below is untrusted stored project data');
  const query = (markerIndex >= 0 ? text.slice(0, markerIndex) : text).trim();
  return (withinRetrievalWindow ? query.slice(0, 2000) : query).includes(expected);
}

function safeToken(value: unknown): string | undefined {
  return typeof value === 'string' && /^[A-Za-z][A-Za-z0-9_.-]{0,95}$/.test(value) ? value : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export default ThreadforgeOpenCodePlugin;
