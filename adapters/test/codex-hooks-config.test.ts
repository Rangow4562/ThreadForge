import assert from 'node:assert/strict';
import { lstat, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  codexManagedMarker,
  codexMemoryManagedMarker,
  codexPlanGateManagedMarker,
  installCodexProjectHooks,
  installCodexProjectMemoryHook,
  installCodexProjectPlanGateHook,
  removeCodexProjectHooks,
  removeCodexProjectMemoryHook,
  removeCodexProjectPlanGateHook,
} from '../src/codex-hooks-config.ts';

async function createRoot(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-hooks-'));
}

test('installs observational Codex hooks while preserving unrelated and blocking hooks', async () => {
  const root = await createRoot();
  try {
    const dir = path.join(root, '.codex');
    await mkdir(dir);
    const file = path.join(dir, 'hooks.json');
    const original = {
      other_setting: { retained: true },
      hooks: {
        PostToolUse: [{ matcher: 'shell', hooks: [{ type: 'command', command: 'echo user-hook', timeout: 9 }] }],
        PreToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: 'echo never-change' }] }],
      },
    };
    await writeFile(file, JSON.stringify(original), 'utf8');
    const command = `node hook.js ${codexManagedMarker}`;
    assert.equal(await installCodexProjectHooks(root, command), 'installed');
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.other_setting, original.other_setting);
    assert.deepEqual(config.hooks.PreToolUse, original.hooks.PreToolUse);
    assert.deepEqual(config.hooks.PostToolUse[0], original.hooks.PostToolUse[0]);
    assert.deepEqual(Object.keys(config.hooks).sort(), ['PostToolUse', 'PreToolUse', 'SessionStart'].sort());
    assert.equal(config.hooks.SessionStart[0].hooks[0].command, command);
    assert.equal(config.hooks.SessionStart[0].hooks[0].timeout, 5);
    assert.equal(await installCodexProjectHooks(root, command), 'unchanged');

    const replacement = `node new-hook.js ${codexManagedMarker}`;
    assert.equal(await installCodexProjectHooks(root, replacement), 'updated');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(JSON.stringify(config).includes('node hook.js'), false);
    assert.equal(JSON.stringify(config).includes('node new-hook.js'), true);
    assert.equal(await removeCodexProjectHooks(root), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.other_setting, original.other_setting);
    assert.deepEqual(config.hooks, {
      PostToolUse: original.hooks.PostToolUse,
      PreToolUse: original.hooks.PreToolUse,
    });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('installs approved-memory injection separately and preserves telemetry, user hooks, and trust settings', async () => {
  const root = await createRoot();
  try {
    const dir = path.join(root, '.codex');
    await mkdir(dir);
    const file = path.join(dir, 'hooks.json');
    const telemetryCommand = `node observer.js ${codexManagedMarker}`;
    const memoryCommand = `threadforge memory inject-codex --project-id demo-project --threadforge-managed-codex-memory=v1 ${codexMemoryManagedMarker}`;
    await writeFile(file, JSON.stringify({
      trusted: false,
      features: { hooks: false },
      hooks: { UserPromptSubmit: [{ hooks: [{ type: 'command', command: 'echo user-hook' }] }] },
    }), 'utf8');

    assert.equal(await installCodexProjectHooks(root, telemetryCommand), 'installed');
    assert.equal(await installCodexProjectMemoryHook(root, memoryCommand), 'installed');
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-hook');
    assert.equal(config.hooks.UserPromptSubmit[1].hooks[0].command, memoryCommand);
    assert.equal(config.hooks.UserPromptSubmit[1].hooks[0].timeout, 5);
    assert.equal(config.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);
    assert.equal(await installCodexProjectMemoryHook(root, memoryCommand), 'unchanged');

    assert.equal(await removeCodexProjectMemoryHook(root), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(JSON.stringify(config).includes(memoryCommand), false);
    assert.equal(config.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-hook');
    assert.equal(config.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('installs Codex patch and shell plan gate separately and preserves hooks, trust, and feature settings', async () => {
  const root = await createRoot();
  try {
    const dir = path.join(root, '.codex');
    await mkdir(dir);
    const file = path.join(dir, 'hooks.json');
    const userHook = { matcher: 'Bash', hooks: [{ type: 'command', command: 'echo user-hook', timeout: 9 }] };
    const telemetryCommand = `node observer.js ${codexManagedMarker}`;
    const memoryCommand = `threadforge memory inject-codex --project-id demo-project --threadforge-managed-codex-memory=v1 ${codexMemoryManagedMarker}`;
    const planGateCommand = `threadforge task plan guard --host codex-cli --task-id task-a --project-id demo-project --project-root C:/workspace --threadforge-managed-codex-plan-gate=v1 --threadforge-plan-gate=codex-v1 ${codexPlanGateManagedMarker}`;
    await writeFile(file, JSON.stringify({
      trusted: false,
      features: { hooks: false },
      hooks: {
        PreToolUse: [userHook],
        UserPromptSubmit: [{ hooks: [{ type: 'command', command: 'echo user-prompt' }] }],
      },
    }), 'utf8');

    assert.equal(await installCodexProjectHooks(root, telemetryCommand), 'installed');
    assert.equal(await installCodexProjectMemoryHook(root, memoryCommand), 'installed');
    assert.equal(await installCodexProjectPlanGateHook(root, planGateCommand), 'installed');
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.hooks.PreToolUse[0], userHook);
    assert.deepEqual(config.hooks.PreToolUse[1], {
      matcher: '^(apply_patch|exec_command)$',
      hooks: [{ type: 'command', command: planGateCommand, timeout: 5 }],
    });
    assert.equal(config.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-prompt');
    assert.equal(config.hooks.UserPromptSubmit[1].hooks[0].command, memoryCommand);
    assert.equal(config.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);
    assert.equal(await installCodexProjectPlanGateHook(root, planGateCommand), 'unchanged');

    const replacement = planGateCommand.replace('--task-id task-a', '--task-id task-b');
    assert.equal(await installCodexProjectPlanGateHook(root, replacement), 'updated');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.hooks.PreToolUse[1].hooks[0].command, replacement);
    assert.equal(JSON.stringify(config).includes(planGateCommand), false);
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);

    assert.equal(await removeCodexProjectPlanGateHook(root), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.hooks.PreToolUse, [userHook]);
    assert.equal(config.hooks.UserPromptSubmit[1].hooks[0].command, memoryCommand);
    assert.equal(config.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('refuses malformed, oversized, symlinked, and unmarked Codex config updates', async () => {
  const root = await createRoot();
  try {
    const dir = path.join(root, '.codex');
    await mkdir(dir);
    const file = path.join(dir, 'hooks.json');
    await writeFile(file, '{broken', 'utf8');
    await assert.rejects(installCodexProjectHooks(root, `node hook.js ${codexManagedMarker}`), /strict JSON/);
    assert.equal(await readFile(file, 'utf8'), '{broken');

    await writeFile(file, JSON.stringify({ hooks: {} }), 'utf8');
    await assert.rejects(installCodexProjectHooks(root, 'node hook.js'), /must include/);
    await assert.rejects(installCodexProjectMemoryHook(root,
      `threadforge memory inject-codex --project-id demo ${codexMemoryManagedMarker}`), /runtime marker/);
    await assert.rejects(installCodexProjectPlanGateHook(root,
      `threadforge task plan guard --host codex-cli --task-id task-a --project-id demo ${codexPlanGateManagedMarker}`), /runtime marker/);

    await writeFile(file, ' '.repeat((8 << 20) + 1), 'utf8');
    await assert.rejects(installCodexProjectHooks(root, `node hook.js ${codexManagedMarker}`), /size limit/);
    assert.ok((await lstat(file)).isFile());
  } finally {
    await rm(root, { recursive: true, force: true });
  }

  const symlinkRoot = await createRoot();
  const target = await createRoot();
  try {
    await mkdir(path.join(target, '.codex'));
    await (await import('node:fs/promises')).symlink(path.join(target, '.codex'), path.join(symlinkRoot, '.codex'), 'junction');
    await assert.rejects(installCodexProjectHooks(symlinkRoot, `node hook.js ${codexManagedMarker}`), /symbolic link/);
  } finally {
    await rm(symlinkRoot, { recursive: true, force: true });
    await rm(target, { recursive: true, force: true });
  }
});
