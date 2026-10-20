import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  installKimiCodeHooks,
  kimiCodeConfigPath,
  removeKimiCodeHooks,
  updateKimiCodeHooks,
} from '../src/kimi-code-hooks-config.ts';
import { kimiCodeManagedMarker } from '../src/kimi-code-hook.ts';

const command = `node kimi-code-hook.ts ${kimiCodeManagedMarker}`;
const events = [
  'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'PostToolUseFailure',
  'PermissionRequest', 'PermissionResult', 'Stop', 'SessionEnd', 'SubagentStart',
  'SubagentStop', 'PreCompact', 'PostCompact', 'StopFailure', 'Interrupt', 'TaskStarted', 'Notification',
];
const temp = () => mkdtemp(path.join(os.tmpdir(), 'threadforge-kimi-code-'));
const count = (text: string, value: string) => text.split(value).length - 1;

test('appends Kimi hook tables while preserving API keys, comments, and user hooks byte-for-byte', async () => {
  const home = await temp();
  const config = kimiCodeConfigPath(home);
  const original = '\uFEFF# keep provider bytes\r\n[providers."moonshot"]\r\napi_key = "local-secret-value"\r\ndefault_model = "kimi-current"\r\n\r\n[[hooks]]\r\nevent = \'SessionStart\'\r\ncommand = \'echo user-hook\'\r\ntimeout = 1_0\r\n# user note after their hook\r\n';
  try {
    await writeFile(config, original, 'utf8');
    assert.equal(await installKimiCodeHooks({ command, kimiCodeHome: home }), 'installed');
    const installed = await readFile(config, 'utf8');
    assert.ok(installed.startsWith(original));
    assert.equal(count(installed, kimiCodeManagedMarker), events.length);
    assert.match(installed, /event = "SessionStart"\r\ncommand = "node kimi-code-hook\.ts --threadforge-hook=kimi"\r\ntimeout = 10/);
    assert.match(installed, /event = "UserPromptSubmit"\r\ncommand = "node kimi-code-hook\.ts --threadforge-hook=kimi"\r\ntimeout = 30/);
    assert.match(installed, /event = "Stop"\r\ncommand = "node kimi-code-hook\.ts --threadforge-hook=kimi"\r\ntimeout = 45/);
    assert.equal(installed.includes('event = "SessionHeartbeat"'), false);
    assert.equal(installed.includes('local-secret-value'), true);
    assert.equal(installed.includes('# user note after their hook'), true);
    assert.equal(await installKimiCodeHooks({ command, kimiCodeHome: home }), 'unchanged');
    assert.equal(await readFile(config, 'utf8'), installed);

    const suffix = '[profiles.fast]\r\nmodel = "other-model"\r\n# retain this trailing config\r\n';
    await writeFile(config, `${installed}${suffix}`, 'utf8');
    const updatedCommand = `node dist/kimi-code-hook.js ${kimiCodeManagedMarker}`;
    assert.equal(await updateKimiCodeHooks({ command: updatedCommand, kimiCodeHome: home }), 'updated');
    let updated = await readFile(config, 'utf8');
    assert.equal(count(updated, kimiCodeManagedMarker), events.length);
    assert.equal(updated.includes('node kimi-code-hook.ts'), false);
    assert.equal(updated.endsWith(suffix) || updated.includes(suffix), true);
    assert.equal(await updateKimiCodeHooks({ command: updatedCommand, kimiCodeHome: home }), 'unchanged');
    assert.equal(await removeKimiCodeHooks({ kimiCodeHome: home }), 'removed');
    updated = await readFile(config, 'utf8');
    assert.equal(updated, `${original}${suffix}`);
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('creates KIMI_CODE_HOME config when absent and leaves an empty config after removal', async () => {
  const home = await temp();
  const config = kimiCodeConfigPath(home);
  try {
    assert.equal(await installKimiCodeHooks({ command, kimiCodeHome: home }), 'installed');
    assert.equal(await removeKimiCodeHooks({ kimiCodeHome: home }), 'removed');
    assert.equal(await readFile(config, 'utf8'), '');
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('refuses inline hooks, malformed TOML, invalid hook fields, and unowned collisions without mutation', async () => {
  const home = await temp();
  const config = kimiCodeConfigPath(home);
  try {
    const invalidInputs = [
      'hooks = [{ event = "SessionStart", command = "echo user" }]\n',
      '[[hooks]]\nevent = "SessionStart"\ncommand = "echo user"\ntimeout = 0\n',
      '[[hooks]]\nevent = "SessionStart"\ncommand = "echo user"\nunknown = "not supported"\n',
      '[providers.openai\napi_key = "value"\n',
      'note = "unterminated\n',
    ];
    for (const original of invalidInputs) {
      await writeFile(config, original, 'utf8');
      await assert.rejects(installKimiCodeHooks({ command, kimiCodeHome: home }));
      assert.equal(await readFile(config, 'utf8'), original);
    }
    await writeFile(config, 'model = "keep"\n', 'utf8');
    await assert.rejects(updateKimiCodeHooks({ command, kimiCodeHome: home }), /absent/);
    assert.equal(await readFile(config, 'utf8'), 'model = "keep"\n');
    await assert.rejects(installKimiCodeHooks({ command: 'node hook.ts', kimiCodeHome: home }), /threadforge-hook/);
    await assert.rejects(installKimiCodeHooks({ command: `API_TOKEN=secret ${command}`, kimiCodeHome: home }), /credential/);
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('refuses stale locks, symlinked KIMI_CODE_HOME, and oversized config safely', async (t) => {
  const home = await temp();
  const outside = await temp();
  try {
    await writeFile(path.join(home, '.threadforge-kimi-code-hooks.lock'), 'stale', 'utf8');
    await assert.rejects(installKimiCodeHooks({ command, kimiCodeHome: home }), /locked/);
    await rm(path.join(home, '.threadforge-kimi-code-hooks.lock'), { force: true });

    const linked = path.join(home, 'linked');
    try { await symlink(outside, linked, 'junction'); }
    catch (error) {
      if (error && typeof error === 'object' && 'code' in error && ['EPERM', 'EACCES', 'ERROR_ACCESS_DENIED'].includes(String(error.code))) {
        t.skip('the current Windows environment does not permit creating directory symlinks');
        return;
      }
      throw error;
    }
    await assert.rejects(installKimiCodeHooks({ command, kimiCodeHome: linked }), /real directory/);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});

test('rejects oversized Kimi Code config before parsing it', async () => {
  const home = await temp();
  const config = kimiCodeConfigPath(home);
  try {
    const oversized = `# ${'x'.repeat((8 << 20) + 1)}\n`;
    await writeFile(config, oversized, 'utf8');
    await assert.rejects(installKimiCodeHooks({ command, kimiCodeHome: home }), /size limit/);
    assert.equal(await readFile(config, 'utf8'), oversized);
  } finally { await rm(home, { recursive: true, force: true }); }
});
