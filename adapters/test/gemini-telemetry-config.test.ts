import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installGeminiTelemetry, removeGeminiTelemetry } from '../src/gemini-telemetry-config.ts';

async function createHome(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-'));
}

test('installs local HTTP telemetry idempotently and removes only Threadforge-owned settings', async () => {
  const home = await createHome();
  try {
    const dir = path.join(home, '.gemini');
    await mkdir(dir);
    const settingsPath = path.join(dir, 'settings.json');
    const initial = {
      model: { name: 'user-selected-model' },
      telemetry: { customField: 'preserve', logPrompts: false },
      mcpServers: { local: { command: 'keep-me' } },
    };
    await writeFile(settingsPath, JSON.stringify(initial), 'utf8');

    assert.equal(await installGeminiTelemetry({ homeDir: home }), 'installed');
    const installed = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.deepEqual(installed.model, initial.model);
    assert.deepEqual(installed.mcpServers, initial.mcpServers);
    assert.equal(installed.telemetry.customField, 'preserve');
    assert.equal(installed.telemetry.enabled, true);
    assert.equal(installed.telemetry.target, 'local');
    assert.equal(installed.telemetry.otlpEndpoint, 'http://127.0.0.1:4318');
    assert.equal(installed.telemetry.otlpProtocol, 'http');
    assert.equal(installed.telemetry.useCollector, true);
    assert.equal(installed.telemetry.logPrompts, false);
    assert.equal(installed.telemetry.traces, false);
    assert.equal(await installGeminiTelemetry({ homeDir: home }), 'unchanged');

    assert.equal(await removeGeminiTelemetry({ homeDir: home }), 'removed');
    const removed = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.deepEqual(removed, initial);
    assert.equal(await removeGeminiTelemetry({ homeDir: home }), 'absent');
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('refuses conflicting settings and outfile without touching user files', async () => {
  const home = await createHome();
  try {
    const dir = path.join(home, '.gemini');
    await mkdir(dir);
    const settingsPath = path.join(dir, 'settings.json');
    const conflict = { telemetry: { enabled: false, customField: 'keep' } };
    await writeFile(settingsPath, JSON.stringify(conflict), 'utf8');
    await assert.rejects(installGeminiTelemetry({ homeDir: home }), /conflicting user value/);
    assert.deepEqual(JSON.parse(await readFile(settingsPath, 'utf8')), conflict);

    const outfile = { telemetry: { outfile: '.gemini/telemetry.log' } };
    await writeFile(settingsPath, JSON.stringify(outfile), 'utf8');
    await assert.rejects(installGeminiTelemetry({ homeDir: home }), /outfile overrides OTLP/);
    assert.deepEqual(JSON.parse(await readFile(settingsPath, 'utf8')), outfile);
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('uninstall keeps a Gemini setting the user changed after installation', async () => {
  const home = await createHome();
  try {
    await installGeminiTelemetry({ homeDir: home });
    const settingsPath = path.join(home, '.gemini', 'settings.json');
    const settings = JSON.parse(await readFile(settingsPath, 'utf8'));
    settings.telemetry.logPrompts = true;
    settings.telemetry.customField = 'new-user-value';
    await writeFile(settingsPath, JSON.stringify(settings), 'utf8');

    assert.equal(await removeGeminiTelemetry({ homeDir: home }), 'removed');
    const removed = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.deepEqual(removed.telemetry, { logPrompts: true, customField: 'new-user-value' });
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});
