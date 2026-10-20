import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installOpenHandsProjectHooks, openHandsHookEvents, openHandsManagedMarker, removeOpenHandsProjectHooks } from '../src/openhands-hooks-config.ts';

async function project(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-openhands-hooks-')); }
function file(root: string): string { return path.join(root, '.openhands', 'hooks.json'); }

test('installs asynchronous hooks into the project config while preserving unrelated groups and event casing', async () => {
  const root = await project();
  const original = {
    session_start: [{ hooks: [{ command: 'user-session-start', timeout: 60 }] }],
    PreToolUse: [{ matcher: 'terminal', hooks: [{ command: 'user-pre-tool', timeout: 30 }] }],
    SessionEnd: [{ hooks: [{ command: 'user-session-end' }] }],
  };
  await mkdir(path.dirname(file(root)), { recursive: true });
  await writeFile(file(root), JSON.stringify(original, null, 2), 'utf8');
  const template = `threadforge openhands hook {event} ${openHandsManagedMarker}`;
  try {
    assert.equal(await installOpenHandsProjectHooks(root, { commandTemplate: template }), 'installed');
    const installed = JSON.parse(await readFile(file(root), 'utf8')) as Record<string, Array<{ hooks: Array<Record<string, unknown>> }>>;
    assert.equal(installed.PreToolUse.length, 2);
    assert.equal(installed.pre_tool_use, undefined);
    assert.equal(installed.session_start.length, 2);
    assert.equal(installed.SessionEnd.length, 2);
    const managed = Object.values(installed).flatMap((groups) => groups.flatMap((group) => group.hooks))
      .filter((hook) => typeof hook.command === 'string' && hook.command.includes(openHandsManagedMarker));
    assert.equal(managed.length, 6);
    assert.equal(managed.every((hook) => hook.type === 'command' && hook.async === true && hook.timeout === 10), true);
    assert.equal(managed.some((hook) => String(hook.command).includes('{event}')), false);
    assert.equal(await installOpenHandsProjectHooks(root, { commandTemplate: template }), 'unchanged');
    assert.deepEqual(await removeOpenHandsProjectHooks(root), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file(root), 'utf8')), original);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('supports the legacy hooks wrapper and default self-hosted Node command', async () => {
  const root = await project();
  const original = { hooks: { user_prompt_submit: [{ matcher: '*', hooks: [{ command: 'existing' }] }] } };
  await mkdir(path.dirname(file(root)), { recursive: true });
  await writeFile(file(root), JSON.stringify(original), 'utf8');
  try {
    assert.equal(await installOpenHandsProjectHooks(root), 'installed');
    const installed = JSON.parse(await readFile(file(root), 'utf8')) as { hooks: Record<string, Array<{ hooks: Array<Record<string, unknown>> }>> };
    const refs = installed.hooks.user_prompt_submit.flatMap((group) => group.hooks);
    assert.equal(refs.length, 2);
    const managed = refs.find((hook) => String(hook.command).includes(openHandsManagedMarker));
    assert.equal(managed?.async, true);
    assert.match(String(managed?.command), /--experimental-strip-types/);
    assert.equal(await removeOpenHandsProjectHooks(root), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file(root), 'utf8')), original);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('refuses duplicate event casings, unsupported top-level keys, invalid JSON, and bad command templates', async () => {
  const root = await project();
  const configPath = file(root);
  await mkdir(path.dirname(configPath), { recursive: true });
  try {
    const duplicate = JSON.stringify({ session_start: [], SessionStart: [] });
    await writeFile(configPath, duplicate, 'utf8');
    await assert.rejects(installOpenHandsProjectHooks(root), /more than once/);
    assert.equal(await readFile(configPath, 'utf8'), duplicate);

    const unknown = JSON.stringify({ hooks_version: 2 });
    await writeFile(configPath, unknown, 'utf8');
    await assert.rejects(installOpenHandsProjectHooks(root), /unsupported event/);
    assert.equal(await readFile(configPath, 'utf8'), unknown);

    await writeFile(configPath, '{not-json', 'utf8');
    await assert.rejects(installOpenHandsProjectHooks(root), /invalid strict JSON/);
    await assert.rejects(installOpenHandsProjectHooks(root, { commandTemplate: `threadforge ${openHandsManagedMarker}` }), /placeholder/);
    await assert.rejects(installOpenHandsProjectHooks(root, { commandTemplate: `threadforge {event} ${openHandsManagedMarker}\n` }), /placeholder/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('uninstall is absent when no managed references exist and removes an empty owned hooks file', async () => {
  const root = await project();
  try {
    assert.equal(await removeOpenHandsProjectHooks(root), 'absent');
    assert.equal(await installOpenHandsProjectHooks(root), 'installed');
    assert.equal(await removeOpenHandsProjectHooks(root), 'removed');
    await assert.rejects(readFile(file(root)), /ENOENT/);
    assert.equal(openHandsHookEvents.length, 6);
  } finally { await rm(root, { recursive: true, force: true }); }
});
