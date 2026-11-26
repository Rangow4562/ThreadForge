import assert from 'node:assert/strict';
import { lstat, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { parse } from 'yaml';
import { getGooseOtelStatus } from '../src/goose-otel.ts';
import { runGooseOtelConfigAction } from '../src/goose-otel-config.ts';

test('requires explicit prompt/reasoning capture consent and removes a created config idempotently', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-install-'));
  const configPath = path.join(root, '.config', 'goose', 'config.yaml');
  try {
    await assert.rejects(runGooseOtelConfigAction('install', { configPath }), /allow-content-capture/);
    await assert.rejects(lstat(path.dirname(configPath)));

    const installed = await runGooseOtelConfigAction('install', {
      configPath, homeDir: root, allowContentCapture: true,
    });
    assert.equal(installed.result, 'installed');
    assert.equal(installed.config_created_by_threadforge, true);
    assert.equal(installed.content_capture_possible, true);
    assert.equal(installed.content_capture_scope, 'loopback_configured');
    assert.equal(installed.consent_confirmed, true);
    assert.equal(installed.runtime_environment_may_override, true);
    assert.equal(installed.timeout_ms, 10_000);
    for (const secret of [configPath, '127.0.0.1:4318']) assert.equal(JSON.stringify(installed).includes(secret), false);

    const before = await readFile(configPath);
    const parsed = parse(before.toString('utf8')) as Record<string, unknown>;
    assert.equal(parsed.otel_exporter_otlp_endpoint, 'http://127.0.0.1:4318');
    assert.equal(parsed.otel_exporter_otlp_timeout, 10_000);
    assert.equal((await runGooseOtelConfigAction('install', { configPath, allowContentCapture: true })).result, 'unchanged');
    assert.deepEqual(await readFile(configPath), before);

    const removed = await runGooseOtelConfigAction('uninstall', { configPath });
    assert.equal(removed.result, 'removed');
    assert.equal(removed.content_capture_possible, false);
    assert.equal(removed.consent_confirmed, false);
    await assert.rejects(lstat(configPath));
    assert.equal((await runGooseOtelConfigAction('uninstall', { configPath })).result, 'absent');
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('preserves BOM, CRLF, comments, and user YAML through update and exact uninstall', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-preserve-'));
  const configPath = path.join(root, 'config.yaml');
  const original = Buffer.from('\ufeff# User-owned Goose settings\r\nprovider: anthropic\r\ncustom:\r\n  enabled: true\r\n', 'utf8');
  try {
    await writeFile(configPath, original);
    const installed = await runGooseOtelConfigAction('install', {
      configPath, endpoint: 'http://localhost:4318/', timeoutMs: 8_000, allowContentCapture: true,
    });
    assert.equal(installed.result, 'installed');
    let content = await readFile(configPath);
    assert.equal(content.subarray(0, original.length).equals(original), true);
    assert.equal(content.toString('utf8').replaceAll('\r\n', '').includes('\n'), false);
    assert.equal(content.toString('utf8').startsWith('\ufeff# User-owned Goose settings\r\n'), true);
    let parsed = parse(content.toString('utf8')) as Record<string, unknown>;
    assert.equal(parsed.provider, 'anthropic');
    assert.equal(parsed.otel_exporter_otlp_endpoint, 'http://localhost:4318');
    assert.equal(parsed.otel_exporter_otlp_timeout, 8_000);

    const updated = await runGooseOtelConfigAction('update', {
      configPath, endpoint: 'http://127.0.0.1:4319', timeoutMs: 20_000, allowContentCapture: true,
    });
    assert.equal(updated.result, 'updated');
    content = await readFile(configPath);
    assert.equal(content.subarray(0, original.length).equals(original), true);
    parsed = parse(content.toString('utf8')) as Record<string, unknown>;
    assert.equal(parsed.otel_exporter_otlp_endpoint, 'http://127.0.0.1:4319');
    assert.equal(parsed.otel_exporter_otlp_timeout, 20_000);

    assert.equal((await runGooseOtelConfigAction('uninstall', { configPath })).result, 'removed');
    assert.deepEqual(await readFile(configPath), original);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('preserves files without a final newline and retains user additions made after installation', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-eof-'));
  const configPath = path.join(root, 'config.yaml');
  const original = Buffer.from('provider: anthropic', 'utf8');
  try {
    await writeFile(configPath, original);
    await runGooseOtelConfigAction('install', { configPath, allowContentCapture: true });
    const installedText = (await readFile(configPath, 'utf8'));
    await writeFile(configPath, `${installedText}user_setting: keep-me\n`, 'utf8');
    const removed = await runGooseOtelConfigAction('uninstall', { configPath });
    assert.equal(removed.result, 'removed');
    const finalText = await readFile(configPath, 'utf8');
    assert.match(finalText, /^provider: anthropic\nuser_setting: keep-me\n$/);
    assert.equal(finalText.includes('THREADFORGE-GOOSE-OTEL'), false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('refuses unmanaged OTLP keys, malformed YAML, remote endpoints, and unsafe timeouts without edits', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-conflict-'));
  const configPath = path.join(root, 'config.yaml');
  try {
    const manual = Buffer.from('provider: anthropic\notel_exporter_otlp_endpoint: http://127.0.0.1:4318\n', 'utf8');
    await writeFile(configPath, manual);
    await assert.rejects(runGooseOtelConfigAction('install', { configPath, allowContentCapture: true }), /outside Threadforge ownership/);
    assert.deepEqual(await readFile(configPath), manual);

    await writeFile(configPath, 'provider: [unterminated\n', 'utf8');
    const malformed = await readFile(configPath);
    await assert.rejects(runGooseOtelConfigAction('install', { configPath, allowContentCapture: true }), /invalid YAML/);
    assert.deepEqual(await readFile(configPath), malformed);

    await writeFile(configPath, 'provider: anthropic\n', 'utf8');
    const valid = await readFile(configPath);
    await assert.rejects(runGooseOtelConfigAction('install', {
      configPath, endpoint: 'https://collector.example', allowContentCapture: true,
    }), /loopback HTTP/);
    await assert.rejects(runGooseOtelConfigAction('install', {
      configPath, timeoutMs: 120_001, allowContentCapture: true,
    }), /between 1 and 120000/);
    assert.deepEqual(await readFile(configPath), valid);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('requires an unmodified Threadforge block before update or uninstall', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-edited-'));
  const configPath = path.join(root, 'config.yaml');
  try {
    await runGooseOtelConfigAction('install', { configPath, allowContentCapture: true });
    const original = await readFile(configPath, 'utf8');
    await writeFile(configPath, original.replace('otel_exporter_otlp_timeout: 10000', 'otel_exporter_otlp_timeout: 9000'), 'utf8');
    const edited = await readFile(configPath);
    await assert.rejects(runGooseOtelConfigAction('update', { configPath, allowContentCapture: true }), /block was edited/);
    await assert.rejects(runGooseOtelConfigAction('uninstall', { configPath }), /block was edited/);
    assert.deepEqual(await readFile(configPath), edited);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('refuses symlinked config paths without changing the target', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-link-'));
  const configPath = path.join(root, 'config.yaml');
  const targetPath = path.join(root, 'target.yaml');
  try {
    const target = Buffer.from('provider: anthropic\n', 'utf8');
    await writeFile(targetPath, target);
    try { await symlink(targetPath, configPath); }
    catch (error) {
      if (process.platform === 'win32' && isCode(error, 'EPERM')) { t.skip('Windows symlink permission is unavailable'); return; }
      throw error;
    }
    await assert.rejects(runGooseOtelConfigAction('install', { configPath, allowContentCapture: true }), /symbolic link/);
    assert.deepEqual(await readFile(targetPath), target);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('managed OTLP settings read back through the existing sanitized status contract', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-readback-'));
  const configPath = path.join(root, 'config.yaml');
  try {
    await runGooseOtelConfigAction('install', { configPath, allowContentCapture: true });
    const status = await getGooseOtelStatus({
      configPath, homeDir: root, logPath: path.join(root, 'missing.jsonl'), env: {},
    });
    assert.equal(status.configured_endpoint, 'loopback_http');
    assert.equal(status.timeout_ms, 10_000);
    for (const secret of [root, configPath, '127.0.0.1:4318']) assert.equal(JSON.stringify(status).includes(secret), false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('uses Goose APPDATA config location on Windows', async (t) => {
  if (process.platform !== 'win32') { t.skip('Windows default config location is covered on Windows runners'); return; }
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-appdata-'));
  const appData = path.join(root, 'Roaming');
  const configPath = path.join(appData, 'Block', 'goose', 'config', 'config.yaml');
  try {
    await runGooseOtelConfigAction('install', {
      homeDir: root, platform: 'win32', env: { APPDATA: appData }, allowContentCapture: true,
    });
    const parsed = parse((await readFile(configPath, 'utf8'))) as Record<string, unknown>;
    assert.equal(parsed.otel_exporter_otlp_endpoint, 'http://127.0.0.1:4318');
  } finally { await rm(root, { recursive: true, force: true }); }
});

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
