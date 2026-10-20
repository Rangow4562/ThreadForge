export const DEFAULT_OTLP_LOGS_ENDPOINT = 'http://127.0.0.1:4318/v1/logs';

/** Only the local Threadforge OTLP receiver is eligible for browser content. */
export function isLocalThreadforgeLogsEndpoint(value: unknown): value is string {
  if (typeof value !== 'string' || value.trim() !== value || /[\u0000-\u0020\u007f]/.test(value)) return false;
  if (value.includes('?') || value.includes('#')) return false;
  let parsed: URL;
  try { parsed = new URL(value); }
  catch { return false; }
  return parsed.protocol === 'http:' &&
    (parsed.hostname === '127.0.0.1' || parsed.hostname.toLowerCase() === 'localhost') &&
    parsed.port !== '' && Number(parsed.port) >= 1 && Number(parsed.port) <= 65535 &&
    parsed.pathname === '/v1/logs' &&
    parsed.username === '' && parsed.password === '' && parsed.search === '' && parsed.hash === '';
}

export function assertLocalThreadforgeLogsEndpoint(value: unknown): asserts value is string {
  if (!isLocalThreadforgeLogsEndpoint(value)) {
    throw new Error('endpoint must be the local Threadforge OTLP/HTTP logs receiver at http://127.0.0.1:4318/v1/logs');
  }
}
