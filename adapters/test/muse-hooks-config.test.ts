import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installMuseHooks, museHooksConfigPath, removeMuseHooks, updateMuseHooks } from '../src/muse-hooks-config.ts';
import { museManagedMarker } from '../src/muse-hook.ts';

const command = `node /opt/threadforge/muse-hook.ts ${museManagedMarker}`;
const events = [
  'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PermissionRequest', 'PostToolUse', 'PostToolUseFailure',
  'PreCompact', 'PostCompact', 'SubagentStart', 'SubagentStop', 'Notification', 'Stop', 'SessionEnd',
];
const temp = () => mkdtemp(path.join(os.tmpdir(), 'threadforge-muse-'));

test('installs project hooks in .muse/hooks.json, preserving other hook groups', async () => {
  const project = await temp();
  const file = museHooksConfigPath('project', project);
  const userHook = { matcher: '*', hooks: [{ type: 'command', command: 'npm run lint' }] };
  try {
    await mkdir(path.dirname(file));
    await writeFile(file, JSON.stringify({ hooks: { Stop: [userHook] }, unrelated: { keep: true } }), 'utf8');
    assert.equal(await installMuseHooks({ command, scope: 'project', projectRoot: project }), 'installed');
    const first = await readFile(file, 'utf8');
    const config = JSON.parse(first);
    assert.deepEqual(config.unrelated, { keep: true });
    assert.deepEqual(config.hooks.Stop[0], userHook);
    for (const event of events) {
      const managed = config.hooks[event].flatMap((group: { hooks: Array<Record<string, unknown>> }) => group.hooks)
        .find((hook: Record<string, unknown>) => typeof hook.command === 'string' && hook.command.includes(museManagedMarker));
      assert.ok(managed, `missing ${event}`);
      assert.equal(managed.type, 'command');
      assert.equal(managed.command, `${command} --hook-event=${event}`);
      assert.ok(typeof managed.timeout === 'number' && managed.timeout > 0);
      assert.equal(Object.hasOwn(managed, 'shell'), false);
      assert.equal(Object.hasOwn(managed, 'env'), false);
    }
    assert.equal(config.hooks.PreToolUse.at(-1).matcher, '*');
    assert.equal(await installMuseHooks({ command, scope: 'project', projectRoot: project }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);
    const updated = `node /opt/threadforge/muse-hook-v2.ts ${museManagedMarker}`;
    assert.equal(await updateMuseHooks({ command: updated, scope: 'project', projectRoot: project }), 'updated');
    assert.equal(JSON.parse(await readFile(file, 'utf8')).hooks.Stop[0].hooks[0].command, 'npm run lint');
    assert.equal(await removeMuseHooks({ scope: 'project', projectRoot: project }), 'removed');
    const after = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(after.unrelated, { keep: true });
    assert.deepEqual(after.hooks, { Stop: [userHook] });
  } finally { await rm(project, { recursive: true, force: true }); }
});

test('updates user settings under XDG_CONFIG_HOME without taking over managed hooks', async () => {
  const xdg = await temp();
  const file = museHooksConfigPath('user', xdg);
  const originalManagedPath = '/opt/admin/managed-hooks.json';
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify({
      schema_version: 1, model: { name: 'preserve' }, managed_hooks_path: originalManagedPath,
      hooks: { UserPromptSubmit: [{ matcher: '*', hooks: [{ type: 'command', command: 'echo user-hook' }] }] },
    }), 'utf8');
    assert.equal(await installMuseHooks({ command, scope: 'user', xdgConfigHome: xdg }), 'installed');
    const first = await readFile(file, 'utf8');
    const config = JSON.parse(first);
    assert.deepEqual(config.model, { name: 'preserve' });
    assert.equal(config.managed_hooks_path, originalManagedPath);
    assert.equal(config.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-hook');
    assert.equal(config.hooks.UserPromptSubmit[1].matcher, '*');
    assert.equal(await installMuseHooks({ command, scope: 'user', xdgConfigHome: xdg }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);
    assert.equal(await removeMuseHooks({ scope: 'user', xdgConfigHome: xdg }), 'removed');
    const after = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(after.schema_version, 1);
    assert.equal(after.managed_hooks_path, originalManagedPath);
    assert.deepEqual(after.hooks, { UserPromptSubmit: [{ matcher: '*', hooks: [{ type: 'command', command: 'echo user-hook' }] }] });
  } finally { await rm(xdg, { recursive: true, force: true }); }
});

test('requires user schema v1 and refuses malformed hooks, credentials, edited options, and stale locks', async () => {
  const xdg = await temp();
  const file = museHooksConfigPath('user', xdg);
  try {
    await mkdir(path.dirname(file), { recursive: true });
    for (const invalid of ['{}', '{"schema_version":2}', '{not-json']) {
      await writeFile(file, invalid, 'utf8');
      await assert.rejects(installMuseHooks({ command, scope: 'user', xdgConfigHome: xdg }));
      assert.equal(await readFile(file, 'utf8'), invalid);
    }
    await assert.rejects(installMuseHooks({ command: `API_TOKEN=private ${command}`, scope: 'user', xdgConfigHome: xdg }), /credential/);
    await writeFile(file, JSON.stringify({ schema_version: 1, hooks: { Stop: false } }), 'utf8');
    await assert.rejects(installMuseHooks({ command, scope: 'user', xdgConfigHome: xdg }), /array/);
    await writeFile(file, JSON.stringify({ schema_version: 1 }), 'utf8');
    assert.equal(await installMuseHooks({ command, scope: 'user', xdgConfigHome: xdg }), 'installed');
    const config = JSON.parse(await readFile(file, 'utf8'));
    const managed = config.hooks.Stop.at(-1).hooks[0];
    managed.timeout = 0;
    await writeFile(file, JSON.stringify(config), 'utf8');
    const changed = await readFile(file, 'utf8');
    await assert.rejects(updateMuseHooks({ command, scope: 'user', xdgConfigHome: xdg }), /modified/);
    assert.equal(await readFile(file, 'utf8'), changed);
    await rm(file);
    await writeFile(path.join(path.dirname(file), '.threadforge-muse-hooks.lock'), 'locked');
    await assert.rejects(installMuseHooks({ command, scope: 'user', xdgConfigHome: xdg }), /locked/);
  } finally { await rm(xdg, { recursive: true, force: true }); }
});

test('refuses symlinked Muse config paths and oversized files', async (t) => {
  const xdg = await temp();
  const outside = await temp();
  try {
    try { await symlink(outside, path.join(xdg, 'muse'), 'junction'); }
    catch { t.skip('the current Windows environment does not permit creating directory symlinks'); return; }
    await assert.rejects(installMuseHooks({ command, scope: 'user', xdgConfigHome: xdg }), /symbolic link/);
  } finally {
    await rm(xdg, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
  const largeRoot = await temp();
  const file = museHooksConfigPath('user', largeRoot);
  try {
    await mkdir(path.dirname(file), { recursive: true });
    const large = ' '.repeat((8 << 20) + 1);
    await writeFile(file, large, 'utf8');
    await assert.rejects(installMuseHooks({ command, scope: 'user', xdgConfigHome: largeRoot }), /size limit/);
    assert.equal((await readFile(file)).byteLength, Buffer.byteLength(large));
  } finally { await rm(largeRoot, { recursive: true, force: true }); }
});
