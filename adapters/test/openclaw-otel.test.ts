import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  getOpenClawOtelStatus, parseOpenClawDuration, renderOpenClawOtelConfig,
  resolveThreadforgeTracePath, validateOpenClawOtelStatus,
} from '../src/openclaw-otel.ts';

test('prints current OpenClaw OTLP protobuf config without capturing content or overwriting allowlists', () => {
  const output = renderOpenClawOtelConfig();
  assert.match(output, /openclaw plugins install @openclaw\/diagnostics-otel/);
  assert.match(output, /protocol: "http\/protobuf"/);
  assert.match(output, /tracesEndpoint: "http:\/\/127\.0\.0\.1:4318\/v1\/traces"/);
  assert.match(output, /metricsEndpoint: "http:\/\/127\.0\.0\.1:4318\/v1\/metrics"/);
  assert.match(output, /logsEndpoint: "http:\/\/127\.0\.0\.1:4318\/v1\/logs"/);
  assert.match(output, /logs: true/);
  assert.match(output, /logsExporter: "otlp"/);
  assert.match(output, /captureContent: false/);
  assert.doesNotMatch(output, /plugins: \{\s*allow:/);
  assert.match(output, /do not replace other diagnostics, plugin, or allowlist entries/);
});

test('requires explicit consent for content capture and HTTPS remote OTLP destinations', () => {
  assert.throws(() => renderOpenClawOtelConfig({ endpoint: 'https://telemetry.example/v1' }), /--allow-remote/);
  assert.throws(() => renderOpenClawOtelConfig({ endpoint: 'http://telemetry.example', allowRemote: true }), /HTTPS/);
  assert.throws(() => renderOpenClawOtelConfig({ endpoint: 'https://user:secret@telemetry.example', allowRemote: true }), /endpoint/);
  const captured = renderOpenClawOtelConfig({ captureContent: true });
  assert.match(captured, /captureContent: true/);
  assert.match(captured, /WARNING: OpenClaw may export bounded/);
  const remote = renderOpenClawOtelConfig({ endpoint: 'https://collector.example', allowRemote: true });
  assert.match(remote, /WARNING: remote TLS export was explicitly enabled/);
});

test('resolves trace store overrides and platform cache defaults without exposing paths', () => {
  assert.equal(resolveThreadforgeTracePath({ THREADFORGE_TRACE_FILE: 'custom/events.jsonl' }, 'C:\\Users\\tester', 'win32'), 'custom/events.jsonl');
  assert.equal(resolveThreadforgeTracePath({ THREADFORGE_HOME: 'D:\\local\\tf' }, 'C:\\Users\\tester', 'win32'), path.win32.join('D:\\local\\tf', 'traces', 'events.jsonl'));
  assert.equal(resolveThreadforgeTracePath({ LOCALAPPDATA: 'C:\\Users\\tester\\AppData\\Local' }, 'C:\\Users\\tester', 'win32'),
    path.join('C:\\Users\\tester\\AppData\\Local', 'threadforge', 'traces', 'events.jsonl'));
  assert.equal(resolveThreadforgeTracePath({ XDG_CACHE_HOME: '/tmp/cache' }, '/home/tester', 'linux'), '/tmp/cache/threadforge/traces/events.jsonl');
  assert.equal(resolveThreadforgeTracePath({}, '/Users/tester', 'darwin'), '/Users/tester/Library/Caches/threadforge/traces/events.jsonl');
});

test('reports only aggregate local OpenClaw OTLP evidence and ignores plugins and other harnesses', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-openclaw-otel-'));
  const log = path.join(root, 'events.jsonl');
  const now = new Date();
  const lines = [
    JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'openclaw_gateway', collection_method: 'otlp' }, message: 'private body' }),
    JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'openclaw_gateway', collection_method: 'plugin' } }),
    JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'cursor', collection_method: 'otlp' } }),
  ];
  try {
    await writeFile(log, `${lines.join('\n')}\n`, 'utf8');
    const status = await getOpenClawOtelStatus({ logPath: log, now });
    assert.equal(status.present, true);
    assert.equal(status.complete, true);
    assert.equal(status.observed_events, 1);
    assert.equal(status.last_event_observed, true);
    assert.equal(status.last_event_observed_at, now.toISOString());
    assert.equal(JSON.stringify(status).includes('private body'), false);
    assert.equal(JSON.stringify(status).includes(log), false);
    assert.equal(validateOpenClawOtelStatus(status, 10_000, now).valid, true);
    assert.equal(validateOpenClawOtelStatus(status, 1, new Date(now.getTime() + 5_000)).valid, false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('surfaces missing and malformed trace logs instead of reporting a clean status', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-openclaw-otel-'));
  const missing = await getOpenClawOtelStatus({ logPath: path.join(root, 'missing.jsonl') });
  assert.equal(missing.present, false);
  assert.equal(validateOpenClawOtelStatus(missing).valid, false);
  const log = path.join(root, 'events.jsonl');
  try {
    await writeFile(log, '{broken json}\n', 'utf8');
    const malformed = await getOpenClawOtelStatus({ logPath: log });
    assert.equal(malformed.complete, false);
    assert.equal(malformed.malformed_lines, 1);
    assert.equal(malformed.last_event_observed, false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('parses bounded validation windows', () => {
  assert.equal(parseOpenClawDuration('10m'), 600_000);
  assert.equal(parseOpenClawDuration('250ms'), 250);
  assert.throws(() => parseOpenClawDuration('0s'), /between 1 ms/);
  assert.throws(() => parseOpenClawDuration('10 weeks'), /must use/);
  assert.throws(() => parseOpenClawDuration('31d'), /between 1 ms/);
});
