import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  devinDesktopHooksConfigPath,
  installDevinDesktopHooks,
  removeDevinDesktopHooks,
} from '../src/devin-desktop-hooks-config.ts';
import { devinDesktopHookEvents, devinDesktopManagedMarker } from '../src/devin-desktop-hook.ts';

async function temporaryDirectory(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-devin-desktop-'));
}

test('user install/update/uninstall preserves unrelated Cascade settings and hooks', async () => {
  const home = await temporaryDirectory();
  const file = devinDesktopHooksConfigPath('user', home);
  const commandTemplate = `node hook.ts devin-desktop hook {event} ${devinDesktopManagedMarker}`;
  try {
    await mkdir(path.dirname(file), { recursive: true });
    const original = {
      hooks: { post_write_code: [{ command: 'echo user-owned', show_output: true }] },
      unrelated: { keep: true },
    };
    await writeFile(file, JSON.stringify(original, null, 2), 'utf8');

    assert.equal(await installDevinDesktopHooks({ homeDir: home, commandTemplate }), 'installed');
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.unrelated, original.unrelated);
    assert.equal(config.hooks.post_write_code.some((hook: { command: string }) => hook.command === 'echo user-owned'), true);
    for (const event of devinDesktopHookEvents) {
      const installed = config.hooks[event].filter((hook: { command: string }) => hook.command.includes(devinDesktopManagedMarker));
      assert.equal(installed.length, 1);
      assert.equal(installed[0].command, commandTemplate.replace('{event}', event));
      assert.equal(installed[0].show_output, false);
    }
    const first = await readFile(file, 'utf8');
    assert.equal(await installDevinDesktopHooks({ homeDir: home, commandTemplate }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    const updatedTemplate = `node dist/cli.js devin-desktop hook {event} ${devinDesktopManagedMarker}`;
    assert.equal(await installDevinDesktopHooks({ homeDir: home, commandTemplate: updatedTemplate }), 'updated');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.hooks.post_write_code.some((hook: { command: string }) => hook.command === 'echo user-owned'), true);
    assert.equal(config.hooks.post_write_code.filter((hook: { command: string }) => hook.command.includes(devinDesktopManagedMarker)).length, 1);

    assert.equal(await removeDevinDesktopHooks({ homeDir: home }), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config, original);
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('project scope uses .windsurf/hooks.json and validates an absolute real project root', async () => {
  const root = await temporaryDirectory();
  const file = devinDesktopHooksConfigPath('project', root);
  const commandTemplate = `node hook.ts devin-desktop hook {event} ${devinDesktopManagedMarker}`;
  try {
    await assert.rejects(installDevinDesktopHooks({ scope: 'project', commandTemplate }), /absolute project root/);
    await mkdir(path.dirname(file), { recursive: true });
    const initial = { hooks: { custom_event: [{ command: 'echo keep' }] }, workspace: 'preserve' };
    await writeFile(file, JSON.stringify(initial), 'utf8');
    assert.equal(await installDevinDesktopHooks({ scope: 'project', projectRoot: root, commandTemplate }), 'installed');
    const config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.workspace, 'preserve');
    assert.deepEqual(config.hooks.custom_event, initial.hooks.custom_event);
    assert.equal(await removeDevinDesktopHooks({ scope: 'project', projectRoot: root }), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), initial);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('invalid templates, relative homes, malformed JSON, and invalid hook shapes fail without mutation', async () => {
  const home = await temporaryDirectory();
  const file = devinDesktopHooksConfigPath('user', home);
  const validTemplate = `node hook.ts {event} ${devinDesktopManagedMarker}`;
  try {
    await assert.rejects(installDevinDesktopHooks({ homeDir: 'relative-home', commandTemplate: validTemplate }), /absolute/);
    await assert.rejects(installDevinDesktopHooks({ homeDir: home, commandTemplate: 'node hook.ts {event}' }), /exactly one/);
    await assert.rejects(installDevinDesktopHooks({ homeDir: home, commandTemplate: `TOKEN=private-secret {event} ${devinDesktopManagedMarker}` }), /credential/);
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, '{bad-json', 'utf8');
    await assert.rejects(installDevinDesktopHooks({ homeDir: home, commandTemplate: validTemplate }), /strict JSON/);
    assert.equal(await readFile(file, 'utf8'), '{bad-json');
    await writeFile(file, JSON.stringify({ hooks: { post_read_code: { command: 'not-an-array' } } }), 'utf8');
    await assert.rejects(installDevinDesktopHooks({ homeDir: home, commandTemplate: validTemplate }), /must be an array/);
  } finally { await rm(home, { recursive: true, force: true }); }
});
