import assert from 'node:assert/strict';
import { access, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installCodexTelemetry, removeCodexTelemetry } from '../src/codex-telemetry-config.ts';

async function createHome(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-otel-'));
}

test('installs local Codex OTLP config idempotently and restores original bytes', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    await mkdir(home);
    const configPath = path.join(home, 'config.toml');
    const original = '# user comment\r\nmodel = "keep"';
    await writeFile(configPath, original, 'utf8');

    assert.equal(await installCodexTelemetry({ codexHome: home }), 'installed');
    const installed = await readFile(configPath, 'utf8');
    assert.ok(installed.startsWith(`${original}\r\n`));
    assert.match(installed, /\[otel\]\r\nenvironment = "local"\r\nmetrics_exporter = "none"\r\nlog_user_prompt = false/);
    assert.match(installed, /\[otel\.exporter\."otlp-http"\]\r\nendpoint = "http:\/\/127\.0\.0\.1:4318\/v1\/logs"\r\nprotocol = "json"/);
    assert.match(installed, /\[otel\.trace_exporter\."otlp-http"\]\r\nendpoint = "http:\/\/127\.0\.0\.1:4318\/v1\/traces"\r\nprotocol = "json"/);
    assert.equal(await installCodexTelemetry({ codexHome: home }), 'unchanged');

    assert.equal(await removeCodexTelemetry({ codexHome: home }), 'removed');
    assert.equal(await readFile(configPath, 'utf8'), original);
    assert.equal(await removeCodexTelemetry({ codexHome: home }), 'absent');
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('supports a selected loopback OTLP origin and updates only the owned block', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    await mkdir(home);
    const configPath = path.join(home, 'config.toml');
    const original = '# preserve this comment\nmodel = "keep"\n';
    await writeFile(configPath, original, 'utf8');

    assert.equal(await installCodexTelemetry({ codexHome: home, endpoint: 'http://127.0.0.1:43191/' }), 'installed');
    const installed = await readFile(configPath, 'utf8');
    assert.ok(installed.startsWith(original));
    assert.match(installed, /endpoint = "http:\/\/127\.0\.0\.1:43191\/v1\/logs"/);
    assert.match(installed, /endpoint = "http:\/\/127\.0\.0\.1:43191\/v1\/traces"/);
    assert.equal(await installCodexTelemetry({ codexHome: home, endpoint: 'http://127.0.0.1:43191' }), 'unchanged');

    assert.equal(await installCodexTelemetry({ codexHome: home, endpoint: 'http://localhost:4318' }), 'updated');
    const updated = await readFile(configPath, 'utf8');
    assert.ok(updated.startsWith(original));
    assert.match(updated, /endpoint = "http:\/\/localhost:4318\/v1\/logs"/);
    assert.match(updated, /endpoint = "http:\/\/localhost:4318\/v1\/traces"/);
    assert.equal(await removeCodexTelemetry({ codexHome: home }), 'removed');
    assert.equal(await readFile(configPath, 'utf8'), original);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('rejects non-loopback or non-origin OTLP endpoints before creating CODEX_HOME', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    for (const endpoint of [
      'https://127.0.0.1:4318',
      'http://192.0.2.1:4318',
      'http://127.0.0.1:4318/v1',
      'http://user:secret@127.0.0.1:4318',
      'http://127.0.0.1:4318?remote=true',
      'http://127.0.0.1:4318/#fragment',
    ]) {
      await assert.rejects(installCodexTelemetry({ codexHome: home, endpoint }), /Codex OTLP endpoint must be a loopback HTTP origin/);
      await assert.rejects(access(home), { code: 'ENOENT' });
    }
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('creates and removes a minimal config when CODEX_HOME has no config.toml', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    assert.equal(await installCodexTelemetry({ codexHome: home }), 'installed');
    assert.match(await readFile(path.join(home, 'config.toml'), 'utf8'), /127\.0\.0\.1:4318/);
    assert.equal(await removeCodexTelemetry({ codexHome: home }), 'removed');
    await assert.rejects(readFile(path.join(home, 'config.toml')), /ENOENT/);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('refuses unmanaged Codex OTel settings, including remote destinations, without editing them', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    await mkdir(home);
    const configPath = path.join(home, 'config.toml');
    for (const original of [
      '[otel]\nlog_user_prompt = true\n',
      '[otel.exporter."otlp-http"]\nendpoint = "https://collector.example/v1/logs"\n',
      'otel.log_user_prompt = true\n',
      'otel = { log_user_prompt = true }\n',
    ]) {
      await writeFile(configPath, original, 'utf8');
      await assert.rejects(installCodexTelemetry({ codexHome: home }), /unmanaged otel/);
      assert.equal(await readFile(configPath, 'utf8'), original);
    }
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('ignores OTel-looking comments and string values while preserving unrelated TOML', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    await mkdir(home);
    const configPath = path.join(home, 'config.toml');
    const original = '# [otel]\nnotes = "[otel.exporter.otlp-http]"\n[model]\nname = "keep"\n';
    await writeFile(configPath, original, 'utf8');
    assert.equal(await installCodexTelemetry({ codexHome: home }), 'installed');
    assert.ok((await readFile(configPath, 'utf8')).startsWith(original));
    assert.equal(await removeCodexTelemetry({ codexHome: home }), 'removed');
    assert.equal(await readFile(configPath, 'utf8'), original);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('preserves TOML appended after the managed block when uninstalling', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    await mkdir(home);
    const configPath = path.join(home, 'config.toml');
    const original = 'model = "keep"\n';
    await writeFile(configPath, original, 'utf8');
    await installCodexTelemetry({ codexHome: home });
    const userAddition = '[profiles.fast]\nmodel = "fast-model"\n';
    await writeFile(configPath, `${await readFile(configPath, 'utf8')}${userAddition}`, 'utf8');
    assert.equal(await removeCodexTelemetry({ codexHome: home }), 'removed');
    assert.equal(await readFile(configPath, 'utf8'), `${original}${userAddition}`);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('refuses modified or malformed managed blocks instead of overwriting user edits', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    await mkdir(home);
    const configPath = path.join(home, 'config.toml');
    await writeFile(configPath, 'model = "keep"\n', 'utf8');
    await installCodexTelemetry({ codexHome: home });
    const changed = (await readFile(configPath, 'utf8')).replace('metrics_exporter = "none"', 'metrics_exporter = "otlp"');
    await writeFile(configPath, changed, 'utf8');
    await assert.rejects(installCodexTelemetry({ codexHome: home }), /was edited/);
    await assert.rejects(removeCodexTelemetry({ codexHome: home }), /was edited/);
    assert.equal(await readFile(configPath, 'utf8'), changed);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('rejects symlinked Codex config files without touching their targets', async (t) => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  const outside = path.join(parent, 'outside.toml');
  try {
    await mkdir(home);
    await writeFile(outside, 'model = "protected"\n', 'utf8');
    try { await symlink(outside, path.join(home, 'config.toml')); }
    catch (error) {
      if (typeof error === 'object' && error !== null && 'code' in error && error.code === 'EPERM') {
        t.skip('the current Windows environment does not permit creating file symlinks');
        return;
      }
      throw error;
    }
    await assert.rejects(installCodexTelemetry({ codexHome: home }), /regular, non-symlink/);
    assert.equal(await readFile(outside, 'utf8'), 'model = "protected"\n');
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('rejects stale locks rather than racing another Codex config writer', async () => {
  const parent = await createHome();
  const home = path.join(parent, '.codex');
  try {
    await mkdir(home);
    await writeFile(path.join(home, '.threadforge-codex-telemetry.lock'), 'stale', 'utf8');
    await assert.rejects(installCodexTelemetry({ codexHome: home }), /locked/);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('rejects a non-absolute CODEX_HOME', async () => {
  await assert.rejects(installCodexTelemetry({ codexHome: '.codex' }), /absolute path/);
});
