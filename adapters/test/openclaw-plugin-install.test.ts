import assert from 'node:assert/strict';
import test from 'node:test';
import { buildOpenClawPluginCommands, openClawPluginPackagePath, runOpenClawPluginAction } from '../src/openclaw-plugin-install.ts';

const options = { scope: 'active-gateway' as const };

test('builds explicit OpenClaw install, reload, and keep-files uninstall commands', () => {
  const install = buildOpenClawPluginCommands('install', options);
  assert.deepEqual(install, [
    { executable: 'openclaw', args: ['plugins', 'install', '--link', openClawPluginPackagePath()] },
    { executable: 'openclaw', args: ['plugins', 'enable', 'threadforge-observability'] },
  ]);
  assert.deepEqual(buildOpenClawPluginCommands('update', options), [
    { executable: 'openclaw', args: ['plugins', 'reload', 'threadforge-observability'] },
  ]);
  assert.deepEqual(buildOpenClawPluginCommands('uninstall', options), [
    { executable: 'openclaw', args: ['plugins', 'uninstall', 'threadforge-observability', '--keep-files'] },
  ]);
  assert.deepEqual(buildOpenClawPluginCommands('install', { ...options, acknowledgeLocalSource: true })[0]?.args, [
    'plugins', 'install', '--link', '--force', openClawPluginPackagePath(),
  ]);
});

test('delegates configuration changes to the selected OpenClaw CLI and stops after failure', async () => {
  const commands: string[][] = [];
  const result = await runOpenClawPluginAction('install', { ...options, openclawCli: 'openclaw-custom' }, async (command) => {
    commands.push([command.executable, ...command.args]);
  });
  assert.equal(result, 'installed');
  assert.equal(commands.length, 2);
  assert.equal(commands[0]?.[0], 'openclaw-custom');

  let calls = 0;
  await assert.rejects(runOpenClawPluginAction('install', options, async () => {
    calls++;
    if (calls === 2) throw new Error('enable failed');
  }), /installed the Threadforge plugin but could not enable it/);
  assert.equal(calls, 2);
});

test('requires explicit active-gateway scope and reserves force acknowledgement for install', () => {
  assert.throws(() => buildOpenClawPluginCommands('install', {} as typeof options), /explicit --scope/);
  assert.throws(() => buildOpenClawPluginCommands('update', { ...options, acknowledgeLocalSource: true }), /only with OpenClaw install/);
  assert.throws(() => buildOpenClawPluginCommands('install', { ...options, openclawCli: 'bad\ncommand' }), /invalid characters/);
});
