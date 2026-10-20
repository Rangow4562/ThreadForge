import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  claudeManagedMarker,
  claudePlanGateManagedMarker,
  installClaudeProjectHooks,
  installClaudeProjectMemoryHook,
  installClaudeProjectPlanGateHook,
  removeClaudeProjectHooks,
  removeClaudeProjectMemoryHook,
  removeClaudeProjectPlanGateHook,
} from '../src/claude-hooks-config.ts';

const command = `node --experimental-strip-types adapters/src/claude-hook.ts ${claudeManagedMarker}`;

async function withProject(run: (root: string) => Promise<void>): Promise<void> {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-hooks-'));
  try { await run(root); }
  finally { await rm(root, { recursive: true, force: true }); }
}

function settingsPath(root: string): string {
  return path.join(root, '.claude', 'settings.json');
}

test('installs all documented hook event groups while preserving unrelated settings and hook types', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    await mkdir(path.dirname(file));
    const existing = {
      env: { KEEP: 'yes' },
      permissions: { defaultMode: 'default' },
      hooks: {
        SessionStart: [{ matcher: 'startup', hooks: [{ type: 'command', command: 'echo keep' }] }],
        Stop: [{ hooks: [{ type: 'http', url: 'http://127.0.0.1/hook', headers: { keep: 'value' } }] }],
      },
      plugins: ['unrelated-plugin'],
    };
    await writeFile(file, JSON.stringify(existing, null, 2));

    assert.equal(await installClaudeProjectHooks(root, { command }), 'installed');
    const first = await readFile(file, 'utf8');
    const installed = JSON.parse(first);
    assert.deepEqual(installed.env, existing.env);
    assert.deepEqual(installed.permissions, existing.permissions);
    assert.deepEqual(installed.plugins, existing.plugins);
    assert.deepEqual(installed.hooks.SessionStart[0], existing.hooks.SessionStart[0]);
    assert.deepEqual(installed.hooks.Stop[0], existing.hooks.Stop[0]);
    for (const event of [
      'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'PostToolUseFailure',
      'Stop', 'SubagentStart', 'SubagentStop', 'PermissionRequest', 'SessionEnd',
    ]) {
      assert.equal(installed.hooks[event].some((group: { hooks?: Array<{ command?: string }> }) =>
        group.hooks?.some((hook) => hook.command === command)), true, `${event} hook missing`);
    }
    assert.equal(await installClaudeProjectHooks(root, { command }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);
  });
});

test('updates only Threadforge entries and uninstall retains mixed user-owned groups', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    await mkdir(path.dirname(file));
    const oldCommand = command;
    const updatedCommand = `node ./dist/claude-hook.js ${claudeManagedMarker}`;
    await writeFile(file, JSON.stringify({
      hooks: {
        PostToolUse: [{ matcher: '*', hooks: [
          { type: 'command', command: 'echo user-hook', timeout: 3 },
          { type: 'command', command: oldCommand },
        ], customField: 'preserve' }],
      },
      other: true,
    }));

    assert.equal(await installClaudeProjectHooks(root, { command: updatedCommand }), 'updated');
    let current = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(JSON.stringify(current).includes(oldCommand), false);
    assert.equal(JSON.stringify(current).includes(command), false);
    assert.equal(JSON.stringify(current).includes(updatedCommand), true);
    assert.equal(current.hooks.PostToolUse[0].hooks[0].command, 'echo user-hook');
    assert.equal(current.hooks.PostToolUse[0].customField, 'preserve');
    assert.equal(await removeClaudeProjectHooks(root), 'removed');
    current = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(current, {
      hooks: { PostToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: 'echo user-hook', timeout: 3 }], customField: 'preserve' }] },
      other: true,
    });
    assert.equal(await removeClaudeProjectHooks(root), 'absent');
  });
});

test('installs and removes the approved-memory prompt hook independently from telemetry and user hooks', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    const telemetryCommand = command;
    const memoryCommand = `threadforge memory inject --project-id demo-project --threadforge-managed-claude-memory=v1 --threadforge-memory=claude-v1`;
    await mkdir(path.dirname(file));
    await writeFile(file, JSON.stringify({ hooks: {
      UserPromptSubmit: [{ hooks: [{ type: 'command', command: 'echo user-hook' }] }],
      SessionStart: [{ hooks: [{ type: 'command', command: telemetryCommand }] }],
    } }));

    assert.equal(await installClaudeProjectMemoryHook(root, memoryCommand), 'installed');
    let current = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(current.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-hook');
    assert.equal(current.hooks.UserPromptSubmit[1].hooks[0].command, memoryCommand);
    assert.equal(current.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(await installClaudeProjectMemoryHook(root, memoryCommand), 'unchanged');

    assert.equal(await removeClaudeProjectMemoryHook(root), 'removed');
    current = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(JSON.stringify(current).includes(memoryCommand), false);
    assert.equal(current.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-hook');
    assert.equal(current.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(await removeClaudeProjectMemoryHook(root), 'absent');
  });
});

test('installs the deny-only plan gate separately and preserves telemetry, memory, and user hooks', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    const telemetryCommand = command;
    const memoryCommand = 'threadforge memory inject --project-id demo --threadforge-managed-claude-memory=v1 --threadforge-memory=claude-v1';
    const planCommand = 'threadforge task plan guard --task-id task-a --project-id demo --project-root C:/workspace --threadforge-managed-claude-plan-gate=v1 --threadforge-plan-gate=claude-v1';
    const updatedPlanCommand = 'threadforge task plan guard --task-id task-b --project-id demo --project-root C:/workspace --threadforge-managed-claude-plan-gate=v1 --threadforge-plan-gate=claude-v1';
    await mkdir(path.dirname(file));
    await writeFile(file, JSON.stringify({ hooks: {
      PreToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: 'echo user-hook' }] }],
      UserPromptSubmit: [{ hooks: [{ type: 'command', command: memoryCommand }] }],
      SessionStart: [{ hooks: [{ type: 'command', command: telemetryCommand }] }],
    } }));

    assert.equal(await installClaudeProjectPlanGateHook(root, planCommand), 'installed');
    let current = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(current.hooks.PreToolUse[0].hooks[0].command, 'echo user-hook');
    assert.equal(current.hooks.PreToolUse[1].matcher, 'Write|Edit');
    assert.equal(current.hooks.PreToolUse[1].hooks[0].command, planCommand);
    assert.equal(current.hooks.UserPromptSubmit[0].hooks[0].command, memoryCommand);
    assert.equal(current.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(await installClaudeProjectPlanGateHook(root, planCommand), 'unchanged');

    assert.equal(await installClaudeProjectPlanGateHook(root, updatedPlanCommand), 'updated');
    current = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(JSON.stringify(current).includes(planCommand), false);
    assert.equal(current.hooks.PreToolUse[0].hooks[0].command, 'echo user-hook');
    assert.equal(current.hooks.PreToolUse[1].hooks[0].command, updatedPlanCommand);

    assert.equal(await removeClaudeProjectPlanGateHook(root), 'removed');
    current = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(current.hooks.PreToolUse, [
      { matcher: '*', hooks: [{ type: 'command', command: 'echo user-hook' }] },
    ]);
    assert.equal(current.hooks.UserPromptSubmit[0].hooks[0].command, memoryCommand);
    assert.equal(current.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(await removeClaudeProjectPlanGateHook(root), 'absent');
    assert.equal(claudePlanGateManagedMarker, '--threadforge-plan-gate=claude-v1');
  });
});

test('plan gate requires both explicit ownership and runtime markers', async () => {
  await withProject(async (root) => {
    await assert.rejects(
      installClaudeProjectPlanGateHook(root, 'threadforge task plan guard --threadforge-managed-claude-plan-gate=v1'),
      /ownership marker/,
    );
    await assert.rejects(
      installClaudeProjectPlanGateHook(root, 'threadforge task plan guard --threadforge-plan-gate=claude-v1'),
      /runtime marker/,
    );
  });
});

test('rejects malformed settings without modifying the source file', async () => {
  await withProject(async (root) => {
    const file = settingsPath(root);
    await mkdir(path.dirname(file));
    const contents = '{ "hooks": { "SessionStart": "not-an-array" } }';
    await writeFile(file, contents);
    await assert.rejects(installClaudeProjectHooks(root, { command }), /must be an array/);
    assert.equal(await readFile(file, 'utf8'), contents);
  });
});

test('requires the ownership marker before creating settings and refuses a symlinked settings directory', async () => {
  await withProject(async (root) => {
    await assert.rejects(installClaudeProjectHooks(root, { command: 'node hook.js' }), /ownership marker/);
    const outside = await mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-outside-'));
    try {
      await rm(path.join(root, '.claude'), { recursive: true, force: true });
      await symlink(outside, path.join(root, '.claude'), 'junction');
      await assert.rejects(installClaudeProjectHooks(root, { command }), /symbolic link/);
    } finally { await rm(outside, { recursive: true, force: true }); }
  });
});

test('requires both the installer ownership marker and the retrieval runtime marker', async () => {
  await withProject(async (root) => {
    await assert.rejects(
      installClaudeProjectMemoryHook(root, 'threadforge memory inject --project-id demo --threadforge-memory=claude-v1'),
      /runtime marker/,
    );
  });
});
