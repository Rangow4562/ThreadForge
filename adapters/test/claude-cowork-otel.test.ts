import assert from 'node:assert/strict';
import test from 'node:test';
import { renderClaudeCoworkOtelGuidance } from '../src/claude-cowork-otel.ts';

test('Cowork guidance requires an explicit public-DNS HTTPS collector and stays credential-free', () => {
  const text = renderClaudeCoworkOtelGuidance('https://otel.example.com/otlp');
  assert.match(text, /OTLP endpoint: https:\/\/otel\.example\.com\/otlp/);
  assert.match(text, /OTLP protocol: HTTP\/protobuf/);
  assert.match(text, /deployment\.environment=prod,service\.name=claude-cowork/);
  assert.match(text, /Threadforge local intake is loopback-only/);
  assert.match(text, /does not relay, receive, validate, or forward/);
  assert.match(text, /makes no network requests/);
  assert.doesNotMatch(text, /Authorization|Bearer|token=/i);
});

test('Cowork guidance rejects local, IP, non-HTTPS, credentialed, and malformed destinations', () => {
  for (const endpoint of [
    'http://otel.example.com',
    'https://localhost:4318',
    'https://collector.local',
    'https://collector.internal',
    'https://127.0.0.1:4318',
    'https://[::1]:4318',
    'https://user:secret@otel.example.com',
    'https://otel.example.com?token=secret',
    'https://otel.example.com#fragment',
    'https://collector',
    'not a url',
  ]) {
    assert.throws(() => renderClaudeCoworkOtelGuidance(endpoint), /Claude Cowork requires|must be an absolute/);
  }
});
