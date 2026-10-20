import assert from 'node:assert/strict';
import { mkdtemp, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  getVSCodeOtelStatus,
  parseVSCodeOtelDuration,
  renderVSCodeOtelConfig,
  resolveVSCodeSettingsPath,
  validateVSCodeOtelStatus,
} from '../src/vscode-otel.ts';

test('prints a local-only VS Code Copilot OTLP/HTTP settings fragment with content capture off', () => {
  const output = renderVSCodeOtelConfig();
  assert.match(output, /github\.copilot\.chat\.otel\.enabled/);
  assert.match(output, /"github\.copilot\.chat\.otel\.exporterType": "otlp-http"/);
  assert.match(output, /"github\.copilot\.chat\.otel\.otlpEndpoint": "http:\/\/127\.0\.0\.1:4318"/);
  assert.match(output, /"github\.copilot\.chat\.otel\.captureContent": false/);
  assert.match(output, /maxAttributeSizeChars": 8192/);
  assert.throws(() => renderVSCodeOtelConfig('https://collector.example'), /loopback HTTP/);
  assert.throws(() => renderVSCodeOtelConfig('http://127.0.0.1:4318/v1/traces'), /loopback HTTP/);
});

test('resolves VS Code settings defaults without touching the filesystem', () => {
  assert.equal(resolveVSCodeSettingsPath({ APPDATA: 'C:\\Users\\tester\\AppData\\Roaming' }, 'C:\\Users\\tester', 'win32'),
    path.win32.join('C:\\Users\\tester\\AppData\\Roaming', 'Code', 'User', 'settings.json'));
  assert.equal(resolveVSCodeSettingsPath({}, '/Users/tester', 'darwin'),
    '/Users/tester/Library/Application Support/Code/User/settings.json');
  assert.equal(resolveVSCodeSettingsPath({ XDG_CONFIG_HOME: '/tmp/config' }, '/home/tester', 'linux'),
    '/tmp/config/Code/User/settings.json');
});

test('reads JSONC settings and recent Copilot OTLP events without returning values or paths', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-vscode-otel-'));
  const settingsPath = path.join(root, 'settings.json');
  const logPath = path.join(root, 'events.jsonl');
  const now = new Date('2026-09-25T12:00:00.000Z');
  try {
    const settings = [
      '{',
      '  // VS Code comments and trailing commas must remain readable.',
      '  "github.copilot.chat.otel.enabled": true,',
      '  "github.copilot.chat.otel.exporterType": "otlp-http",',
      '  "github.copilot.chat.otel.otlpEndpoint": "http://127.0.0.1:4318",',
      '  "github.copilot.chat.otel.captureContent": true,',
      '  "other.setting": "private-value",',
      '}',
    ].join('\n');
    await writeFile(settingsPath, settings, 'utf8');
    await writeFile(logPath, [
      JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'vscode_copilot', collection_method: 'otlp' }, message: 'private prompt' }),
      JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'vscode', collection_method: 'hook' } }),
      JSON.stringify({ timestamp: now.toISOString(), harness: { name: 'copilot_cli', collection_method: 'otlp' } }),
    ].join('\n') + '\n', 'utf8');
    const status = await getVSCodeOtelStatus({ settingsPath, logPath, homeDir: root, now, env: {} });
    assert.equal(status.inspection_complete, true);
    assert.equal(status.telemetry_enabled, 'enabled');
    assert.equal(status.exporter_type, 'otlp_http');
    assert.equal(status.configured_endpoint, 'loopback_http');
    assert.equal(status.content_capture, 'enabled');
    assert.deepEqual(status.warnings, ['message_content_capture_enabled']);
    assert.equal(status.telemetry.observed_events, 1);
    assert.deepEqual(status.issues, []);
    assert.equal(validateVSCodeOtelStatus(status, 60_000, now).valid, true);
    assert.equal(validateVSCodeOtelStatus(status, 1, new Date(now.getTime() + 5_000)).valid, false);
    const serialized = JSON.stringify(status);
    for (const secret of [root, settingsPath, logPath, '127.0.0.1:4318', 'private-value', 'private prompt']) assert.equal(serialized.includes(secret), false);
    assert.equal(await import('node:fs/promises').then(({ readFile }) => readFile(settingsPath, 'utf8')), settings);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('surfaces disabled telemetry, file export, remote endpoints, and malformed settings', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-vscode-issues-'));
  const settingsPath = path.join(root, 'settings.json');
  const logPath = path.join(root, 'missing.jsonl');
  try {
    await writeFile(settingsPath, '{"github.copilot.chat.otel.enabled":false,"github.copilot.chat.otel.exporterType":"file","github.copilot.chat.otel.otlpEndpoint":"http://collector.private:4318"}', 'utf8');
    const status = await getVSCodeOtelStatus({ settingsPath, logPath, homeDir: root, env: {} });
    assert.equal(status.telemetry_enabled, 'disabled');
    assert.equal(status.exporter_type, 'file');
    assert.equal(status.configured_endpoint, 'remote_http');
    for (const issue of ['otel_not_enabled', 'file_exporter_bypasses_local_otlp', 'remote_endpoint_not_allowed_by_local_default', 'event_log_missing']) {
      assert.ok(status.issues.includes(issue));
    }
    assert.equal(JSON.stringify(status).includes('collector.private'), false);

    await writeFile(settingsPath, '{ malformed jsonc', 'utf8');
    const malformed = await getVSCodeOtelStatus({ settingsPath, logPath, homeDir: root, env: {} });
    assert.ok(malformed.issues.includes('settings_invalid_jsonc'));
    assert.equal(malformed.inspection_complete, false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('refuses symlinked settings and bounds validation windows', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-vscode-link-'));
  const target = path.join(root, 'target.json');
  const settingsPath = path.join(root, 'settings.json');
  try {
    await writeFile(target, '{"github.copilot.chat.otel.enabled":true}', 'utf8');
    try { await symlink(target, settingsPath); }
    catch (error) {
      if (process.platform === 'win32' && isCode(error, 'EPERM')) { t.skip('Windows symlink permission is unavailable'); return; }
      throw error;
    }
    const status = await getVSCodeOtelStatus({ settingsPath, logPath: path.join(root, 'missing'), homeDir: root, env: {} });
    assert.ok(status.issues.includes('settings_symlink_refused'));
    assert.equal(status.telemetry_enabled, 'unset');
    assert.throws(() => parseVSCodeOtelDuration('31d'), /between 1 ms and 30 days/);
    assert.throws(() => parseVSCodeOtelDuration('1 week'), /must use/);
    assert.equal(parseVSCodeOtelDuration('10m'), 600_000);
  } finally { await rm(root, { recursive: true, force: true }); }
});

function isCode(error: unknown, code: string): boolean {
  return typeof error === 'object' && error !== null && 'code' in error && error.code === code;
}
