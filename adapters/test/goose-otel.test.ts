import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  getGooseOtelStatus,
  parseGooseOtelDuration,
  renderGooseOtelConfig,
  validateGooseOtelStatus,
} from '../src/goose-otel.ts';

test('prints loopback-only OTLP/HTTP YAML without enabling content capture or editing config', () => {
  const output = renderGooseOtelConfig();
  assert.match(output, /otel_exporter_otlp_endpoint: "http:\/\/127\.0\.0\.1:4318"/);
  assert.match(output, /otel_exporter_otlp_timeout: 10000/);
  assert.match(output, /does not enable message-content capture/);
  assert.throws(() => renderGooseOtelConfig('https://collector.example'), /loopback HTTP/);
  assert.throws(() => renderGooseOtelConfig('http://user:secret@127.0.0.1:4318'), /loopback HTTP/);
  assert.throws(() => renderGooseOtelConfig('http://127.0.0.1:4318/v1/traces'), /loopback HTTP/);
});

test('reports sanitized Goose config, environment, and recent local OTLP event evidence', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-'));
  const configPath = path.join(root, 'config.yaml');
  const logPath = path.join(root, 'events.jsonl');
  const now = new Date('2026-09-25T11:00:00.000Z');
  try {
    await writeFile(configPath, [
      'provider: anthropic',
      'otel_exporter_otlp_endpoint: http://127.0.0.1:4318 # local receiver',
      'otel_exporter_otlp_timeout: 10000',
      '',
    ].join('\n'), 'utf8');
    await writeFile(logPath, [
      JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'goose', collection_method: 'otlp' }, message: 'private reasoning' }),
      JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'goose', collection_method: 'hook' } }),
      JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'cursor', collection_method: 'otlp' } }),
    ].join('\n') + '\n', 'utf8');
    const status = await getGooseOtelStatus({
      configPath, logPath, now, homeDir: root,
      env: { OTEL_EXPORTER_OTLP_ENDPOINT: '', OTEL_SDK_DISABLED: '', OTEL_EXPORTER_OTLP_PROTOCOL: '' },
    });
    assert.equal(status.inspection_complete, true);
    assert.equal(status.configured_endpoint, 'loopback_http');
    assert.equal(status.timeout_ms, 10000);
    assert.deepEqual(status.environment, { endpoint_override: 'unset', sdk_disabled: 'unset', protocol: 'unset' });
    assert.equal(status.telemetry.observed_events, 1);
    assert.equal(status.telemetry.last_event_observed_at, now.toISOString());
    assert.deepEqual(status.issues, []);
    assert.equal(validateGooseOtelStatus(status, 60_000, now).valid, true);
    assert.equal(validateGooseOtelStatus(status, 1, new Date(now.getTime() + 5_000)).valid, false);
    for (const secret of [root, configPath, logPath, '127.0.0.1:4318', 'private reasoning', 'anthropic']) {
      assert.equal(JSON.stringify(status).includes(secret), false);
    }
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('surfaces environment endpoint, SDK-disabled, and gRPC overrides without echoing values', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-env-'));
  const configPath = path.join(root, 'config.yaml');
  try {
    await writeFile(configPath, 'otel_exporter_otlp_endpoint: http://127.0.0.1:4318\n', 'utf8');
    const status = await getGooseOtelStatus({
      configPath, logPath: path.join(root, 'missing.jsonl'), homeDir: root,
      env: {
        OTEL_EXPORTER_OTLP_ENDPOINT: 'https://private-collector.example/v1',
        OTEL_SDK_DISABLED: 'true',
        OTEL_EXPORTER_OTLP_PROTOCOL: 'grpc',
      },
    });
    assert.equal(status.environment.endpoint_override, 'invalid');
    assert.equal(status.environment.sdk_disabled, 'true');
    assert.equal(status.environment.protocol, 'grpc');
    assert.deepEqual(status.issues, [
      'environment_endpoint_override', 'otel_sdk_disabled', 'grpc_protocol_override',
      'event_log_missing', 'event_log_incomplete', 'no_goose_otlp_events',
    ]);
    const serialized = JSON.stringify(status);
    for (const secret of ['private-collector', 'https://', configPath, 'missing.jsonl']) assert.equal(serialized.includes(secret), false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('fails visibly for duplicate, unsupported, remote, malformed, or symlinked configuration', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-config-'));
  const configPath = path.join(root, 'config.yaml');
  const missingLog = path.join(root, 'missing.jsonl');
  try {
    await writeFile(configPath, [
      'otel_exporter_otlp_endpoint: http://127.0.0.1:4318',
      'otel_exporter_otlp_endpoint: http://127.0.0.1:4318',
    ].join('\n'), 'utf8');
    let status = await getGooseOtelStatus({ configPath, logPath: missingLog, homeDir: root, env: {} });
    assert.equal(status.configured_endpoint, 'invalid');
    assert.ok(status.issues.includes('config_duplicate_otel_key'));

    await writeFile(configPath, 'otel_exporter_otlp_endpoint: http://collector.example:4318\n', 'utf8');
    status = await getGooseOtelStatus({ configPath, logPath: missingLog, homeDir: root, env: {} });
    assert.equal(status.configured_endpoint, 'remote_http');
    assert.ok(status.issues.includes('remote_endpoint_not_allowed_by_local_default'));
    assert.equal(JSON.stringify(status).includes('collector.example'), false);

    await writeFile(configPath, 'otel_exporter_otlp_timeout: [unsupported]\n', 'utf8');
    status = await getGooseOtelStatus({ configPath, logPath: missingLog, homeDir: root, env: {} });
    assert.equal(status.configured_endpoint, 'invalid');
    assert.ok(status.issues.includes('config_unsupported_otel_value'));

    const target = path.join(root, 'target.yaml');
    await writeFile(target, 'otel_exporter_otlp_endpoint: http://127.0.0.1:4318\n', 'utf8');
    await rm(configPath);
    try { await symlink(target, configPath); }
    catch (error) {
      if (process.platform === 'win32' && isCode(error, 'EPERM')) { t.skip('Windows symlink permission is unavailable'); return; }
      throw error;
    }
    status = await getGooseOtelStatus({ configPath, logPath: missingLog, homeDir: root, env: {} });
    assert.ok(status.issues.includes('config_symlink_refused'));
    assert.equal(status.configured_endpoint, 'invalid');
    assert.equal((await readFile(target, 'utf8')).includes('127.0.0.1'), true);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('reports malformed local event logs and bounds the validation window', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-log-'));
  const configPath = path.join(root, 'config.yaml');
  const logPath = path.join(root, 'events.jsonl');
  try {
    await writeFile(configPath, 'otel_exporter_otlp_endpoint: http://127.0.0.1:4318\n', 'utf8');
    await writeFile(logPath, '{broken json}\n', 'utf8');
    const status = await getGooseOtelStatus({ configPath, logPath, homeDir: root, env: {} });
    assert.equal(status.telemetry.complete, false);
    assert.equal(status.telemetry.malformed_lines, 1);
    assert.equal(status.telemetry.last_event_observed, false);
    assert.throws(() => parseGooseOtelDuration('31d'), /between 1 ms and 30 days/);
    assert.throws(() => parseGooseOtelDuration('3 weeks'), /must use/);
    assert.equal(parseGooseOtelDuration('10m'), 600_000);
  } finally { await rm(root, { recursive: true, force: true }); }
});

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
