import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  devinHooksConfigPath,
  installDevinHooks,
  removeDevinHooks,
} from '../src/devin-hooks-config.ts';
import { devinHookEvents, devinManagedMarker } from '../src/devin-hook.ts';

async function temporaryDirectory(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-devin-'));
}

test('user install/update/uninstall edits only marked hooks and preserves Devin settings and unowned groups', async () => {
  const home = await temporaryDirectory();
  const file = devinHooksConfigPath('user', home);
  const commandTemplate = `node hook.ts devin hook {event} ${devinManagedMarker}`;
  try {
    await mkdir(path.dirname(file), { recursive: true });
    const original = {
      model: 'preserve-model',
      hooks: { Stop: [{ matcher: 'custom', hooks: [{ type: 'command', command: 'echo user-owned' }] }] },
      unrelated: { keep: true },
    };
    await writeFile(file, JSON.stringify(original, null, 2), 'utf8');

    assert.equal(await installDevinHooks({ scope: 'user', homeDir: home, commandTemplate }), 'installed');
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.model, 'preserve-model');
    assert.deepEqual(config.unrelated, original.unrelated);
    assert.equal(config.hooks.Stop.some((group: { hooks: { command: string }[] }) => group.hooks.some((hook) => hook.command === 'echo user-owned')), true);
    for (const event of devinHookEvents) {
      const refs = config.hooks[event].flatMap((group: { hooks: unknown[] }) => group.hooks) as { command: string; timeout?: number }[];
      const installed = refs.filter((hook) => hook.command.includes(devinManagedMarker));
      assert.equal(installed.length, 1);
      assert.equal(installed[0]?.command, commandTemplate.replace('{event}', event));
      if (event === 'UserPromptSubmit') assert.equal(installed[0]?.timeout, 30);
      if (event === 'Stop') assert.equal(installed[0]?.timeout, 45);
    }
    const first = await readFile(file, 'utf8');
    assert.equal(await installDevinHooks({ scope: 'user', homeDir: home, commandTemplate }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    const updatedTemplate = `node dist/cli.js devin hook {event} ${devinManagedMarker}`;
    assert.equal(await installDevinHooks({ scope: 'user', homeDir: home, commandTemplate: updatedTemplate }), 'updated');
    config = JSON.parse(await readFile(file, 'utf8'));
    const stopRefs = config.hooks.Stop.flatMap((group: { hooks: unknown[] }) => group.hooks) as { command: string }[];
    assert.equal(stopRefs.some((hook) => hook.command === 'echo user-owned'), true);
    assert.equal(stopRefs.filter((hook) => hook.command.includes(devinManagedMarker)).length, 1);
    assert.equal(stopRefs.find((hook) => hook.command.includes(devinManagedMarker))?.command, updatedTemplate.replace('{event}', 'Stop'));

    assert.equal(await removeDevinHooks({ scope: 'user', homeDir: home }), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.model, 'preserve-model');
    assert.deepEqual(config.unrelated, original.unrelated);
    assert.deepEqual(config.hooks, original.hooks);
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('project install uses the standalone hook map and leaves unrelated project fields in the file intact', async () => {
  const root = await temporaryDirectory();
  const file = devinHooksConfigPath('project', root);
  try {
    const existing = { SessionStart: [{ matcher: 'manual', hooks: [{ type: 'command', command: 'echo keep' }] }], custom: { key: 'keep' } };
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify(existing, null, 2), 'utf8');
    assert.equal(await installDevinHooks({ scope: 'project', projectRoot: root }), 'installed');
    const config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.custom, existing.custom);
    assert.equal(config.SessionStart.some((group: { hooks: { command: string }[] }) => group.hooks.some((hook) => hook.command === 'echo keep')), true);
    assert.equal(config.UserPromptSubmit[0].hooks[0].command.includes('devin hook UserPromptSubmit'), true);
    assert.equal(await removeDevinHooks({ scope: 'project', projectRoot: root }), 'removed');
    const restored = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(restored, existing);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('invalid commands, malformed files, unowned configs, and missing project scope fail without mutation', async () => {
  const home = await temporaryDirectory();
  const root = await temporaryDirectory();
  const file = devinHooksConfigPath('user', home);
  const validTemplate = `node hook.ts {event} ${devinManagedMarker}`;
  try {
    await assert.rejects(installDevinHooks({ homeDir: home, commandTemplate: 'node hook.ts {event}' }), /exactly one/);
    await assert.rejects(installDevinHooks({ homeDir: home, commandTemplate: `TOKEN=private-secret node hook.ts {event} ${devinManagedMarker}` }), /exactly one|credential/);
    await assert.rejects(installDevinHooks({ scope: 'project', commandTemplate: validTemplate }), /absolute project root/);

    await mkdir(path.dirname(file), { recursive: true });
    const malformed = '{not-json';
    await writeFile(file, malformed, 'utf8');
    await assert.rejects(installDevinHooks({ scope: 'user', homeDir: home, commandTemplate: validTemplate }), /strict JSON/);
    assert.equal(await readFile(file, 'utf8'), malformed);

    const unowned = { hooks: { Stop: [{ hooks: [{ type: 'command', command: 'echo existing' }] }] } };
    await writeFile(file, JSON.stringify(unowned), 'utf8');
    assert.equal(await installDevinHooks({ scope: 'user', homeDir: home, commandTemplate: validTemplate }), 'installed');
    const config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.hooks.Stop.flatMap((group: { hooks: unknown[] }) => group.hooks).some((hook: { command: string }) => hook.command === 'echo existing'), true);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(root, { recursive: true, force: true });
  }
});
