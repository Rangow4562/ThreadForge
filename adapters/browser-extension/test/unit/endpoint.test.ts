import { describe, expect, it } from 'vitest';
import { DEFAULT_OTLP_LOGS_ENDPOINT, isLocalThreadforgeLogsEndpoint } from '../../src/shared/endpoint.js';

describe('local Threadforge OTLP endpoint', () => {
  it('uses loopback HTTP and accepts only the logs route', () => {
    expect(DEFAULT_OTLP_LOGS_ENDPOINT).toBe('http://127.0.0.1:4318/v1/logs');
    expect(isLocalThreadforgeLogsEndpoint(DEFAULT_OTLP_LOGS_ENDPOINT)).toBe(true);
    expect(isLocalThreadforgeLogsEndpoint('http://localhost:4318/v1/logs')).toBe(true);
    expect(isLocalThreadforgeLogsEndpoint('http://127.0.0.1:39871/v1/logs')).toBe(true);
  });

  it('rejects remote, non-HTTP, credentialed, ambiguous, or non-log endpoints', () => {
    for (const endpoint of [
      'https://collector.example.com/v1/logs',
      'http://192.168.1.12:4318/v1/logs',
      'http://[::1]:4318/v1/logs',
      'http://127.0.0.1/v1/logs',
      'http://user:secret@127.0.0.1:4318/v1/logs',
      'http://127.0.0.1:4318/v1/logs?token=secret',
      'http://127.0.0.1:4318/v1/logs#fragment',
      'http://127.0.0.1:4318/v1/traces',
      'http://127.0.0.1:4318/v1/logs/',
    ]) expect(isLocalThreadforgeLogsEndpoint(endpoint)).toBe(false);
    expect(isLocalThreadforgeLogsEndpoint(' http://127.0.0.1:4318/v1/logs')).toBe(false);
  });
});
