import { appendFileSync, writeFileSync } from 'node:fs';
import { ThreadforgeOpenCodePlugin as createThreadforgeOpenCodePlugin, type OpenCodePluginContext } from '../src/opencode-plugin.ts';

export async function ThreadforgeOpenCodePlugin(context: OpenCodePluginContext): Promise<Record<string, (...args: unknown[]) => unknown>> {
  const hooks = await createThreadforgeOpenCodePlugin(context);
  const marker = process.env.THREADFORGE_OPENCODE_PLUGIN_SMOKE_MARKER;
  if (typeof marker === 'string' && marker.length > 0) {
    writeFileSync(marker, 'threadforge-opencode-plugin-invoked-v1\n', { mode: 0o600 });
  }
  const callbacksMarker = process.env.THREADFORGE_OPENCODE_PLUGIN_CALLBACKS_MARKER;
  if (typeof callbacksMarker !== 'string' || callbacksMarker.length === 0) return hooks;
  writeFileSync(callbacksMarker, '', { mode: 0o600 });
  let callbackCount = 0;
  return Object.fromEntries(Object.entries(hooks).map(([name, callback]) => [name, (...args: unknown[]) => {
    if (callbackCount < 256) {
      appendFileSync(callbacksMarker, `${JSON.stringify(summarizeCallback(name, args))}\n`, { mode: 0o600 });
    }
    callbackCount++;
    return callback(...args);
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
  }
  return summary;
}

function safeToken(value: unknown): string | undefined {
  return typeof value === 'string' && /^[A-Za-z][A-Za-z0-9_.-]{0,95}$/.test(value) ? value : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export default ThreadforgeOpenCodePlugin;
