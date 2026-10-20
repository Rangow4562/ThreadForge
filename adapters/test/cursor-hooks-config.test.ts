import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  cursorMemoryManagedMarker,
  cursorManagedMarker,
  cursorPlanGateManagedMarker,
  installCursorProjectPlanGateHook,
  installCursorProjectMemoryHook,
  installCursorProjectHooks,
  removeCursorProjectPlanGateHook,
  removeCursorProjectMemoryHook,
  removeCursorProjectHooks,
} from '../src/cursor-hooks-config.ts';

const command = `node adapters/src/cursor-hook.ts ${cursorManagedMarker}`;
const memoryCommand = `threadforge memory inject-cursor --project-id demo ${cursorMemoryManagedMarker} --threadforge-managed-cursor-memory=v1`;
const planGateCommand = `threadforge task plan guard --host cursor ${cursorPlanGateManagedMarker} --threadforge-plan-gate=cursor-v1`;
const settingsPath = (root: string) => path.join(root, '.cursor', 'hooks.json');

async function withProject(run: (root: string) => Promise<void>): Promise<void> {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cursor-hooks-'));
  try { await run(root); }
  finally { await rm(root, { recursive: true, force: true }); }
}

test('installs only observational hook groups and preserves unrelated Cursor hooks', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    await mkdir(path.dirname(file));
    const existing = {
      version: 1,
      userSetting: { retain: true },
      hooks: {
        postToolUse: [{ command: 'echo user-hook', timeout: 9, custom: { keep: 'yes' } }],
        preCompact: [{ command: 'user-compaction-hook', timeout: 5 }],
        beforeSubmitPrompt: [{ command: 'do-not-change' }],
      },
    };
    await writeFile(file, JSON.stringify(existing, null, 2));
    assert.equal(await installCursorProjectHooks(root, command), 'installed');
    const first = await readFile(file, 'utf8');
    const config = JSON.parse(first);
    assert.deepEqual(config.userSetting, existing.userSetting);
    assert.deepEqual(config.hooks.postToolUse[0], existing.hooks.postToolUse[0]);
    assert.deepEqual(config.hooks.preCompact[0], existing.hooks.preCompact[0]);
    assert.deepEqual(config.hooks.beforeSubmitPrompt, existing.hooks.beforeSubmitPrompt);
    for (const event of ['sessionStart', 'postToolUse', 'postToolUseFailure', 'afterFileEdit', 'afterAgentResponse', 'afterAgentThought', 'preCompact', 'sessionEnd']) {
      assert.equal(config.hooks[event].some((ref: { command: string }) => ref.command === command), true, `${event} hook missing`);
    }
    for (const blockingEvent of ['preToolUse', 'beforeShellExecution', 'beforeSubmitPrompt']) {
      assert.equal((config.hooks[blockingEvent] ?? []).some((ref: { command: string }) => ref.command.includes(cursorManagedMarker)), false);
    }
    assert.equal(await installCursorProjectHooks(root, command), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);
  });
});

test('installs an isolated fail-closed Cursor Write plan gate and preserves other hooks', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    await mkdir(path.dirname(file));
    const original = {
      version: 1,
      hooks: {
        preToolUse: [{ command: 'user-policy-hook', matcher: 'Shell', timeout: 4 }],
        sessionStart: [{ command: 'user-start-hook' }],
        beforeSubmitPrompt: [{ command: 'user-prompt-hook' }],
      },
      userOption: { keep: true },
    };
    await writeFile(file, JSON.stringify(original, null, 2));

    assert.equal(await installCursorProjectPlanGateHook(root, planGateCommand), 'installed');
    const first = await readFile(file, 'utf8');
    let config = JSON.parse(first);
    assert.deepEqual(config.userOption, original.userOption);
    assert.deepEqual(config.hooks.preToolUse[0], original.hooks.preToolUse[0]);
    assert.deepEqual(config.hooks.sessionStart, original.hooks.sessionStart);
    assert.deepEqual(config.hooks.beforeSubmitPrompt, original.hooks.beforeSubmitPrompt);
    assert.deepEqual(config.hooks.preToolUse[1], {
      command: planGateCommand, matcher: 'Write', timeout: 10, failClosed: true,
    });
    assert.equal(await installCursorProjectPlanGateHook(root, planGateCommand), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    const updated = `threadforge task plan guard --host cursor --task-id next ${cursorPlanGateManagedMarker} --threadforge-plan-gate=cursor-v1`;
    assert.equal(await installCursorProjectPlanGateHook(root, updated), 'updated');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.hooks.preToolUse.length, 2);
    assert.equal(config.hooks.preToolUse[0].command, 'user-policy-hook');
    assert.equal(config.hooks.preToolUse[1].command, updated);

    assert.equal(await removeCursorProjectPlanGateHook(root), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.hooks.preToolUse, original.hooks.preToolUse);
    assert.deepEqual(config.hooks.sessionStart, original.hooks.sessionStart);
    assert.deepEqual(config.hooks.beforeSubmitPrompt, original.hooks.beforeSubmitPrompt);
    assert.equal(await removeCursorProjectPlanGateHook(root), 'absent');
  });
});

test('Cursor plan-gate installation requires both ownership markers', async () => {
  await withProject(async (root) => {
    await assert.rejects(installCursorProjectPlanGateHook(root, 'threadforge task plan guard --host cursor'), /include/);
    await assert.rejects(installCursorProjectPlanGateHook(root,
      `threadforge task plan guard --host cursor ${cursorPlanGateManagedMarker}`), /include/);
  });
});

test('updates and uninstalls only marked Threadforge command refs', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    await mkdir(path.dirname(file));
    const updated = `node ./dist/cursor-hook.js ${cursorManagedMarker}`;
    await writeFile(file, JSON.stringify({ version: 1, hooks: {
      postToolUse: [
        { command: 'user-tool-hook', timeout: 12 },
        { command, matcher: 'Write|Edit' },
      ],
    } }));
    assert.equal(await installCursorProjectHooks(root, updated), 'updated');
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.hooks.postToolUse[0], { command: 'user-tool-hook', timeout: 12 });
    assert.equal(config.hooks.postToolUse[1].command, updated);
    assert.equal(await removeCursorProjectHooks(root), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.hooks, { postToolUse: [{ command: 'user-tool-hook', timeout: 12 }] });
    assert.equal(await removeCursorProjectHooks(root), 'absent');
  });
});

test('installs Cursor session-start memory separately and removes only its owned hook', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    await mkdir(path.dirname(file));
    const original = {
      version: 1,
      editorSetting: { preserve: true },
      hooks: {
        sessionStart: [{ command: 'user-start-hook', timeout: 7, custom: 'keep' }],
        beforeSubmitPrompt: [{ command: 'user-prompt-hook' }],
      },
    };
    await writeFile(file, JSON.stringify(original, null, 2));
    assert.equal(await installCursorProjectHooks(root, command), 'installed');
    assert.equal(await installCursorProjectMemoryHook(root, memoryCommand), 'installed');
    const first = await readFile(file, 'utf8');
    let config = JSON.parse(first);
    assert.deepEqual(config.editorSetting, original.editorSetting);
    assert.deepEqual(config.hooks.sessionStart[0], original.hooks.sessionStart[0]);
    assert.equal(config.hooks.sessionStart.some((hook: { command: string }) => hook.command === command), true);
    const memoryHook = config.hooks.sessionStart.find((hook: { command: string }) => hook.command === memoryCommand);
    assert.ok(memoryHook, 'Cursor sessionStart memory hook missing');
    assert.equal(memoryHook.timeout, 10);
    assert.deepEqual(config.hooks.beforeSubmitPrompt, original.hooks.beforeSubmitPrompt);
    assert.equal(await installCursorProjectMemoryHook(root, memoryCommand), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    assert.equal(await removeCursorProjectMemoryHook(root), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.hooks.sessionStart.some((hook: { command: string }) => hook.command === memoryCommand), false);
    assert.equal(config.hooks.sessionStart.some((hook: { command: string }) => hook.command === command), true);
    assert.deepEqual(config.hooks.sessionStart[0], original.hooks.sessionStart[0]);
    assert.deepEqual(config.hooks.beforeSubmitPrompt, original.hooks.beforeSubmitPrompt);
    assert.equal(await removeCursorProjectMemoryHook(root), 'absent');

    assert.equal(await removeCursorProjectHooks(root), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.hooks.sessionStart, original.hooks.sessionStart);
  });
});

test('Cursor memory installer requires both runner and ownership markers', async () => {
  await withProject(async (root) => {
    await assert.rejects(installCursorProjectMemoryHook(root, 'threadforge memory inject-cursor'), /include/);
    await assert.rejects(installCursorProjectMemoryHook(root, `threadforge ${cursorMemoryManagedMarker}`), /include/);
  });
});

test('rejects malformed config, unsupported versions, and symbolic-link paths safely', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    await mkdir(path.dirname(file));
    const malformed = '{"version":2,"hooks":{}}';
    await writeFile(file, malformed);
    await assert.rejects(installCursorProjectHooks(root, command), /unsupported Cursor hooks config version/);
    assert.equal(await readFile(file, 'utf8'), malformed);
    const outside = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cursor-outside-'));
    try {
      await rm(path.join(root, '.cursor'), { recursive: true, force: true });
      await symlink(outside, path.join(root, '.cursor'), 'junction');
      await assert.rejects(installCursorProjectHooks(root, command), /symbolic link/);
    } finally { await rm(outside, { recursive: true, force: true }); }
  });
});
