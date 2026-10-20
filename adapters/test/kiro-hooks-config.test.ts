import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installKiroHooks, kiroHooksConfigPath, removeKiroHooks, updateKiroHooks } from '../src/kiro-hooks-config.ts';
import { kiroManagedMarker } from '../src/kiro-hook.ts';

const command = `node /opt/threadforge/kiro-hook.ts ${kiroManagedMarker}`;
const events = ['SessionStart', 'AgentSpawn', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'Stop'];
const timeouts: Record<string, number> = { SessionStart: 10, AgentSpawn: 10, UserPromptSubmit: 30, PreToolUse: 10, PostToolUse: 10, Stop: 45 };
const temp = () => mkdtemp(path.join(os.tmpdir(), 'threadforge-kiro-'));

test('installs Kiro user hooks in a dedicated file and preserves sibling and in-file user hooks', async () => {
  const home = await temp();
  const file = kiroHooksConfigPath('user', home);
  const sibling = path.join(home, 'hooks', 'my-user-hook.json');
  try {
    await mkdir(path.dirname(sibling));
    const siblingBytes = '{"version":"v1","hooks":[]}\r\n';
    await writeFile(sibling, siblingBytes, 'utf8');
    const userHook = { name: 'user-check', trigger: 'PostFileSave', action: { type: 'command', command: 'npm run lint' }, enabled: true };
    await writeFile(file, JSON.stringify({ version: 'v1', hooks: [userHook] }), 'utf8');

    assert.equal(await installKiroHooks({ command, scope: 'user', homeDir: home }), 'installed');
    const first = await readFile(file, 'utf8');
    const config = JSON.parse(first);
    assert.equal(config.version, 'v1');
    assert.deepEqual(config.hooks[0], userHook);
    assert.deepEqual(config.hooks.slice(1).map((hook: { trigger: string }) => hook.trigger), events);
    for (const hook of config.hooks.slice(1)) {
      assert.equal(hook.action.type, 'command');
      assert.equal(hook.action.command, `${command} --hook-event=${hook.trigger}`);
      assert.equal(hook.enabled, true);
      assert.equal(hook.timeout, timeouts[hook.trigger]);
      assert.equal(Object.hasOwn(hook, 'matcher'), false);
      assert.equal(hook.action.command.includes(kiroManagedMarker), true);
    }
    assert.equal(await installKiroHooks({ command, scope: 'user', homeDir: home }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);
    assert.equal(await readFile(sibling, 'utf8'), siblingBytes);

    const updated = `node /opt/threadforge/kiro-hook-v2.ts ${kiroManagedMarker}`;
    assert.equal(await updateKiroHooks({ command: updated, scope: 'user', homeDir: home }), 'updated');
    const updatedFile = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(updatedFile.hooks[0].action.command, 'npm run lint');
    assert.equal(updatedFile.hooks[1].action.command, `${updated} --hook-event=SessionStart`);
    assert.equal(await removeKiroHooks({ scope: 'user', homeDir: home }), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), { version: 'v1', hooks: [userHook] });
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('installs project hooks at .kiro/hooks and supports simultaneous separate scopes', async () => {
  const project = await temp();
  const home = await temp();
  try {
    assert.equal(await installKiroHooks({ command, scope: 'project', projectRoot: project }), 'installed');
    assert.equal(await installKiroHooks({ command, scope: 'user', homeDir: home }), 'installed');
    const projectConfig = JSON.parse(await readFile(kiroHooksConfigPath('project', project), 'utf8'));
    const userConfig = JSON.parse(await readFile(kiroHooksConfigPath('user', home), 'utf8'));
    assert.equal(projectConfig.hooks.length, events.length);
    assert.equal(userConfig.hooks.length, events.length);
    assert.equal(await removeKiroHooks({ scope: 'project', projectRoot: project }), 'removed');
    assert.equal(await removeKiroHooks({ scope: 'user', homeDir: home }), 'removed');
  } finally {
    await rm(project, { recursive: true, force: true });
    await rm(home, { recursive: true, force: true });
  }
});

test('refuses malformed files, modified managed hooks, credential commands, and stale locks', async () => {
  const home = await temp();
  const file = kiroHooksConfigPath('user', home);
  try {
    await mkdir(path.dirname(file));
    for (const invalid of ['{not-json', '{"version":"v2","hooks":[]}', '{"version":"v1","hooks":[],"extra":true}']) {
      await writeFile(file, invalid, 'utf8');
      await assert.rejects(installKiroHooks({ command, scope: 'user', homeDir: home }));
      assert.equal(await readFile(file, 'utf8'), invalid);
    }
    await assert.rejects(installKiroHooks({ command: `API_TOKEN=private ${command}`, scope: 'user', homeDir: home }), /credential/);
    await assert.rejects(installKiroHooks({ command: `node hook.ts ${kiroManagedMarker} --hook-event=Stop`, scope: 'user', homeDir: home }), /event selector/);

    await writeFile(file, JSON.stringify({ version: 'v1', hooks: [{ name: 'user', trigger: 'Stop', action: { type: 'command', command: 'npm run lint' } }] }), 'utf8');
    assert.equal(await installKiroHooks({ command, scope: 'user', homeDir: home }), 'installed');
    const installed = JSON.parse(await readFile(file, 'utf8'));
    installed.hooks.find((hook: { trigger: string; action: { command: string } }) => hook.trigger === 'Stop' && hook.action.command.includes(kiroManagedMarker)).timeout = 3;
    await writeFile(file, JSON.stringify(installed), 'utf8');
    const altered = await readFile(file, 'utf8');
    await assert.rejects(updateKiroHooks({ command, scope: 'user', homeDir: home }), /modified/);
    await assert.rejects(removeKiroHooks({ scope: 'user', homeDir: home }), /modified/);
    assert.equal(await readFile(file, 'utf8'), altered);
    await rm(file);
    await writeFile(path.join(path.dirname(file), '.threadforge-kiro-hooks.lock'), 'locked');
    await assert.rejects(installKiroHooks({ command, scope: 'user', homeDir: home }), /locked/);
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('refuses symbolic-link paths and oversized Kiro hook files without changing targets', async (t) => {
  const home = await temp();
  const outside = await temp();
  try {
    const link = path.join(home, 'hooks');
    try { await symlink(outside, link, 'junction'); }
    catch { t.skip('the current Windows environment does not permit creating directory symlinks'); return; }
    await assert.rejects(installKiroHooks({ command, scope: 'user', homeDir: home }), /symbolic link/);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }

  const largeHome = await temp();
  const file = kiroHooksConfigPath('user', largeHome);
  try {
    await mkdir(path.dirname(file));
    const large = ' '.repeat((1 << 20) + 1);
    await writeFile(file, large, 'utf8');
    await assert.rejects(installKiroHooks({ command, scope: 'user', homeDir: largeHome }), /size limit/);
    assert.equal((await readFile(file)).byteLength, Buffer.byteLength(large));
  } finally { await rm(largeHome, { recursive: true, force: true }); }
});
