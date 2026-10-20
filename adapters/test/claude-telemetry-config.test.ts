import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installClaudeTelemetry, removeClaudeTelemetry } from '../src/claude-telemetry-config.ts';

async function createHome(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-otel-'));
}

test('installs local OTLP signals with content capture disabled and preserves unrelated user settings', async () => {
  const home = await createHome();
  try {
    const dir = path.join(home, '.claude');
    await mkdir(dir);
    const settingsPath = path.join(dir, 'settings.json');
    const initial = {
      model: 'user-model',
      env: { USER_CUSTOM: 'keep-me' },
      mcpServers: { local: { command: 'keep-server' } },
    };
    await writeFile(settingsPath, JSON.stringify(initial), 'utf8');

    assert.equal(await installClaudeTelemetry({ homeDir: home }), 'installed');
    const installed = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.equal(installed.model, initial.model);
    assert.deepEqual(installed.mcpServers, initial.mcpServers);
    assert.equal(installed.env.USER_CUSTOM, 'keep-me');
    assert.equal(installed.env.CLAUDE_CODE_ENABLE_TELEMETRY, '1');
    assert.equal(installed.env.CLAUDE_CODE_ENHANCED_TELEMETRY_BETA, '1');
    assert.equal(installed.env.OTEL_METRICS_EXPORTER, 'otlp');
    assert.equal(installed.env.OTEL_LOGS_EXPORTER, 'otlp');
    assert.equal(installed.env.OTEL_TRACES_EXPORTER, 'otlp');
    assert.equal(installed.env.OTEL_EXPORTER_OTLP_PROTOCOL, 'http/json');
    assert.equal(installed.env.OTEL_EXPORTER_OTLP_ENDPOINT, 'http://127.0.0.1:4318');
    for (const key of [
      'BETA_TRACING_ENDPOINT', 'OTEL_EXPORTER_OTLP_LOGS_ENDPOINT', 'OTEL_EXPORTER_OTLP_METRICS_ENDPOINT',
      'OTEL_EXPORTER_OTLP_TRACES_ENDPOINT', 'OTEL_EXPORTER_OTLP_HEADERS', 'OTEL_EXPORTER_OTLP_LOGS_HEADERS',
      'OTEL_EXPORTER_OTLP_METRICS_HEADERS', 'OTEL_EXPORTER_OTLP_TRACES_HEADERS',
    ]) assert.equal(installed.env[key], '', `${key} must not divert telemetry or add headers`);
    for (const key of ['OTEL_LOG_USER_PROMPTS', 'OTEL_LOG_ASSISTANT_RESPONSES', 'OTEL_LOG_TOOL_DETAILS', 'OTEL_LOG_TOOL_CONTENT', 'OTEL_LOG_RAW_API_BODIES']) {
      assert.equal(installed.env[key], '0', `${key} must remain disabled`);
    }
    assert.equal(await installClaudeTelemetry({ homeDir: home }), 'unchanged');

    assert.equal(await removeClaudeTelemetry({ homeDir: home }), 'removed');
    assert.deepEqual(JSON.parse(await readFile(settingsPath, 'utf8')), initial);
    assert.equal(await removeClaudeTelemetry({ homeDir: home }), 'absent');
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('refuses conflicting or non-object env settings without changing the file', async () => {
  const home = await createHome();
  try {
    const dir = path.join(home, '.claude');
    await mkdir(dir);
    const settingsPath = path.join(dir, 'settings.json');
    const conflict = { env: { OTEL_EXPORTER_OTLP_ENDPOINT: 'https://remote.example/otel', keep: 'yes' } };
    await writeFile(settingsPath, JSON.stringify(conflict), 'utf8');
    await assert.rejects(installClaudeTelemetry({ homeDir: home }), /conflicting user value/);
    assert.deepEqual(JSON.parse(await readFile(settingsPath, 'utf8')), conflict);

    const invalid = { env: 'not-an-object' };
    await writeFile(settingsPath, JSON.stringify(invalid), 'utf8');
    await assert.rejects(installClaudeTelemetry({ homeDir: home }), /env must be an object/);
    assert.deepEqual(JSON.parse(await readFile(settingsPath, 'utf8')), invalid);
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('uninstall retains managed env values the user changed after installation', async () => {
  const home = await createHome();
  try {
    await installClaudeTelemetry({ homeDir: home });
    const settingsPath = path.join(home, '.claude', 'settings.json');
    const settings = JSON.parse(await readFile(settingsPath, 'utf8'));
    settings.env.OTEL_LOG_USER_PROMPTS = '1';
    settings.env.USER_CUSTOM = 'new-value';
    await writeFile(settingsPath, JSON.stringify(settings), 'utf8');

    assert.equal(await removeClaudeTelemetry({ homeDir: home }), 'removed');
    assert.deepEqual(JSON.parse(await readFile(settingsPath, 'utf8')), {
      env: { OTEL_LOG_USER_PROMPTS: '1', USER_CUSTOM: 'new-value' },
    });
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('refuses a symlinked Claude settings directory without writing outside the user home', async () => {
  const home = await createHome();
  const outside = await mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-outside-'));
  try {
    const protectedFile = path.join(outside, 'settings.json');
    const original = JSON.stringify({ env: { USER_CUSTOM: 'outside-value' } });
    await writeFile(protectedFile, original, 'utf8');
    await symlink(outside, path.join(home, '.claude'), 'junction');
    await assert.rejects(installClaudeTelemetry({ homeDir: home }), /symbolic link/);
    assert.equal(await readFile(protectedFile, 'utf8'), original);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});
