import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  installQwenHooks,
  installQwenProjectMemoryHook,
  installQwenProjectPlanGateHook,
  qwenMemoryManagedMarker,
  qwenPlanGateManagedMarker,
  qwenSettingsPath,
  removeQwenHooks,
  removeQwenProjectMemoryHook,
  removeQwenProjectPlanGateHook,
} from '../src/qwen-hooks-config.ts';
import { qwenManagedMarker } from '../src/qwen-hook.ts';

const command = `node qwen-hook.ts ${qwenManagedMarker}`;
const memoryCommand = `threadforge memory inject-qwen --project-id demo ${qwenMemoryManagedMarker} --threadforge-managed-qwen-memory=v1`;
const planGateCommand = `threadforge task plan guard --host qwen-code --task-id task-a --project-id demo --project-root C:/workspace ${qwenPlanGateManagedMarker} --threadforge-managed-qwen-plan-gate=v1`;
const events = [
  'SessionStart', 'UserPromptSubmit', 'PreToolUse', 'PostToolUse', 'PostToolUseFailure',
  'PermissionRequest', 'Stop', 'SubagentStart', 'SubagentStop', 'SessionEnd',
];
const temp = () => mkdtemp(path.join(os.tmpdir(), 'threadforge-qwen-'));

test('installs Qwen user hooks with Bash and millisecond timeouts while preserving existing settings', async () => {
  const home = await temp();
  const file = qwenSettingsPath('user', home);
  try {
    await mkdir(path.dirname(file));
    const initial = {
      model: { name: 'keep-model' }, disableAllHooks: false,
      mcpServers: { local: { command: 'keep-server' } },
      hooks: { UserPromptSubmit: [{ hooks: [{ type: 'command', command: 'echo user-hook', shell: 'bash' }] }] },
    };
    await writeFile(file, JSON.stringify(initial, null, 2), 'utf8');
    assert.equal(await installQwenHooks({ command, scope: 'user', homeDir: home }), 'installed');
    const first = await readFile(file, 'utf8');
    let settings = JSON.parse(first);
    assert.deepEqual(settings.model, initial.model);
    assert.deepEqual(settings.mcpServers, initial.mcpServers);
    assert.equal(settings.disableAllHooks, false);
    for (const event of events) {
      const refs = settings.hooks[event].flatMap((group: { hooks: Array<Record<string, unknown>> }) => group.hooks);
      const managed = refs.find((hook: Record<string, unknown>) => hook.command === command);
      assert.ok(managed, `${event} hook missing`);
      assert.equal(managed.shell, 'bash');
      if (['PreToolUse', 'PostToolUse', 'PostToolUseFailure', 'PermissionRequest'].includes(event)) {
        assert.equal(settings.hooks[event].some((group: { matcher?: string }) => group.matcher === '*'), true);
        assert.equal(managed.timeout, 10_000);
      }
      if (event === 'UserPromptSubmit') assert.equal(managed.timeout, 30_000);
      if (event === 'Stop') assert.equal(managed.timeout, 45_000);
      if (['SessionStart', 'SubagentStart', 'SubagentStop', 'SessionEnd'].includes(event)) assert.equal(Object.hasOwn(managed, 'timeout'), false);
    }
    assert.equal(settings.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-hook');
    assert.equal(await installQwenHooks({ command, scope: 'user', homeDir: home }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    const updatedCommand = `node dist/qwen-hook.js ${qwenManagedMarker}`;
    assert.equal(await installQwenHooks({ command: updatedCommand, scope: 'user', homeDir: home }), 'updated');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(JSON.stringify(settings).includes('node qwen-hook.ts'), false);
    assert.equal(settings.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-hook');
    assert.equal(await removeQwenHooks({ scope: 'user', homeDir: home }), 'removed');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(settings, {
      model: initial.model, disableAllHooks: false, mcpServers: initial.mcpServers,
      hooks: { UserPromptSubmit: [{ hooks: [{ type: 'command', command: 'echo user-hook', shell: 'bash' }] }] },
    });
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('project hooks do not change trust settings', async () => {
  const project = await temp();
  const file = qwenSettingsPath('project', project);
  try {
    assert.equal(await installQwenHooks({ command, scope: 'project', projectRoot: project }), 'installed');
    const settings = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(Object.hasOwn(settings, 'trustedFolders'), false);
    assert.equal(Object.hasOwn(settings, 'trust'), false);
    assert.equal(await removeQwenHooks({ scope: 'project', projectRoot: project }), 'removed');
  } finally { await rm(project, { recursive: true, force: true }); }
});

test('Qwen project memory hook is task-aware, independent, and reversible', async () => {
  const project = await temp();
  const file = qwenSettingsPath('project', project);
  try {
    const initial = {
      disableAllHooks: false,
      trust: { allowed: false },
      mcpServers: { threadforge: { command: 'threadforge', enabled: true } },
      hooks: {
        UserPromptSubmit: [{ hooks: [{ type: 'command', shell: 'bash', command: 'user-submit-hook' }] }],
        UserPromptExpansion: [{ matcher: '^init$', hooks: [{ type: 'command', shell: 'bash', command: 'user-expansion-hook' }] }],
      },
    };
    await mkdir(path.dirname(file));
    await writeFile(file, JSON.stringify(initial, null, 2), 'utf8');
    assert.equal(await installQwenHooks({ command, scope: 'project', projectRoot: project }), 'installed');
    assert.equal(await installQwenProjectMemoryHook({ command: memoryCommand, projectRoot: project }), 'installed');
    const first = await readFile(file, 'utf8');
    let settings = JSON.parse(first);
    assert.deepEqual(settings.trust, initial.trust);
    assert.deepEqual(settings.mcpServers, initial.mcpServers);
    assert.equal(settings.disableAllHooks, false);
    const groups = settings.hooks.UserPromptExpansion;
    assert.equal(groups[0].matcher, '^init$');
    assert.equal(groups[0].hooks[0].command, 'user-expansion-hook');
    const memoryRef = groups.flatMap((group: { hooks: Array<Record<string, unknown>> }) => group.hooks)
      .find((hook: Record<string, unknown>) => hook.command === memoryCommand);
    assert.ok(memoryRef);
    assert.equal(memoryRef.shell, 'bash');
    assert.equal(memoryRef.timeout, 30_000);
    assert.equal(settings.hooks.UserPromptSubmit.some((group: { hooks: Array<{ command: string }> }) =>
      group.hooks.some((hook) => hook.command === command)), true);
    assert.equal(await installQwenProjectMemoryHook({ command: memoryCommand, projectRoot: project }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    assert.equal(await removeQwenHooks({ scope: 'project', projectRoot: project }), 'removed');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(settings.hooks.UserPromptExpansion.flatMap((group: { hooks: Array<{ command: string }> }) => group.hooks)
      .some((hook: { command: string }) => hook.command === memoryCommand), true);
    assert.equal(settings.hooks.UserPromptSubmit[0].hooks[0].command, 'user-submit-hook');

    assert.equal(await removeQwenProjectMemoryHook(project), 'removed');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(settings, initial);
    assert.equal(await removeQwenProjectMemoryHook(project), 'absent');
  } finally { await rm(project, { recursive: true, force: true }); }
});

test('Qwen memory hook respects disabled hooks and requires both Threadforge markers', async () => {
  const project = await temp();
  const file = qwenSettingsPath('project', project);
  try {
    await assert.rejects(installQwenProjectMemoryHook({ command: 'threadforge inject-qwen', projectRoot: project }), /include/);
    await mkdir(path.dirname(file));
    const disabled = '{"disableAllHooks":true}';
    await writeFile(file, disabled, 'utf8');
    await assert.rejects(installQwenProjectMemoryHook({ command: memoryCommand, projectRoot: project }), /disableAllHooks/);
    assert.equal(await readFile(file, 'utf8'), disabled);
  } finally { await rm(project, { recursive: true, force: true }); }
});

test('Qwen deny-only plan gate is independently managed and preserves telemetry, memory, trust, and user hooks', async () => {
  const project = await temp();
  const file = qwenSettingsPath('project', project);
  try {
    const initial = {
      disableAllHooks: false,
      trust: { allowed: false },
      hooks: {
        PreToolUse: [{ matcher: '*', hooks: [{ type: 'command', shell: 'bash', command }] }],
        UserPromptExpansion: [{ hooks: [{ type: 'command', shell: 'bash', command: memoryCommand }] }],
      },
    };
    await mkdir(path.dirname(file));
    await writeFile(file, JSON.stringify(initial, null, 2), 'utf8');
    assert.equal(await installQwenProjectPlanGateHook({ command: planGateCommand, projectRoot: project }), 'installed');
    const first = await readFile(file, 'utf8');
    let settings = JSON.parse(first);
    assert.deepEqual(settings.trust, initial.trust);
    assert.equal(settings.disableAllHooks, false);
    const groups = settings.hooks.PreToolUse;
    assert.equal(groups[0].matcher, '*');
    assert.equal(groups[0].hooks[0].command, command);
    assert.equal(groups[1].matcher, '^(write_file|edit)$');
    assert.deepEqual(groups[1].hooks, [{ type: 'command', shell: 'bash', command: planGateCommand, timeout: 10 }]);
    assert.equal(settings.hooks.UserPromptExpansion[0].hooks[0].command, memoryCommand);
    assert.equal(await installQwenProjectPlanGateHook({ command: planGateCommand, projectRoot: project }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    assert.equal(await installQwenProjectPlanGateHook({ command: `${planGateCommand} --new`, projectRoot: project }), 'updated');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(settings.hooks.PreToolUse.length, 2);
    assert.equal(settings.hooks.PreToolUse[1].hooks[0].command, `${planGateCommand} --new`);
    assert.equal(await removeQwenProjectPlanGateHook(project), 'removed');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(settings.hooks.PreToolUse, initial.hooks.PreToolUse);
    assert.deepEqual(settings.hooks.UserPromptExpansion, initial.hooks.UserPromptExpansion);
    assert.equal(await removeQwenProjectPlanGateHook(project), 'absent');
  } finally { await rm(project, { recursive: true, force: true }); }
});

test('Qwen plan gate refuses disabled hooks and commands without both ownership markers', async () => {
  const project = await temp();
  const file = qwenSettingsPath('project', project);
  try {
    await assert.rejects(installQwenProjectPlanGateHook({ command: 'threadforge task plan guard', projectRoot: project }), /include/);
    await mkdir(path.dirname(file));
    const disabled = '{"disableAllHooks":true}';
    await writeFile(file, disabled, 'utf8');
    await assert.rejects(installQwenProjectPlanGateHook({ command: planGateCommand, projectRoot: project }), /disableAllHooks/);
    assert.equal(await readFile(file, 'utf8'), disabled);
  } finally { await rm(project, { recursive: true, force: true }); }
});

test('refuses disabled hooks, credentials, malformed JSON, bad event groups, and stale locks', async () => {
  const home = await temp();
  const file = qwenSettingsPath('user', home);
  try {
    await mkdir(path.dirname(file));
    const disabled = '{"disableAllHooks":true}';
    await writeFile(file, disabled, 'utf8');
    await assert.rejects(installQwenHooks({ command, scope: 'user', homeDir: home }), /disableAllHooks/);
    assert.equal(await readFile(file, 'utf8'), disabled);
    await assert.rejects(installQwenHooks({ command: `API_TOKEN=secret ${command}`, scope: 'user', homeDir: home }), /credential/);

    const malformed = '{not-json';
    await writeFile(file, malformed, 'utf8');
    await assert.rejects(installQwenHooks({ command, scope: 'user', homeDir: home }), /strict JSON/);
    assert.equal(await readFile(file, 'utf8'), malformed);
    const badGroup = '{"hooks":{"PreToolUse":"not-an-array"}}';
    await writeFile(file, badGroup, 'utf8');
    await assert.rejects(installQwenHooks({ command, scope: 'user', homeDir: home }), /must be an array/);
    assert.equal(await readFile(file, 'utf8'), badGroup);

    await writeFile(path.join(path.dirname(file), '.threadforge-qwen-hooks.lock'), 'stale', 'utf8');
    await assert.rejects(installQwenHooks({ command, scope: 'user', homeDir: home }), /locked/);
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('absent uninstall creates no directories and settings cannot traverse symlink paths', async (t) => {
  const home = await temp();
  const root = await temp();
  const outside = await temp();
  try {
    assert.equal(await removeQwenHooks({ scope: 'user', homeDir: home }), 'absent');
    await assert.rejects(readFile(qwenSettingsPath('user', home)), /ENOENT/);
    try { await symlink(outside, path.join(root, '.qwen'), 'junction'); }
    catch (error) {
      if (error && typeof error === 'object' && 'code' in error && ['EPERM', 'EACCES', 'ERROR_ACCESS_DENIED'].includes(String(error.code))) {
        t.skip('the current Windows environment does not permit creating directory symlinks');
        return;
      }
      throw error;
    }
    await assert.rejects(installQwenHooks({ command, scope: 'project', projectRoot: root }), /symbolic link/);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(root, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});

test('rejects oversized Qwen settings without rewriting them', async () => {
  const root = await temp();
  const file = qwenSettingsPath('project', root);
  try {
    await mkdir(path.dirname(file));
    const large = `{"keep":"${'x'.repeat((8 << 20) + 1)}"}`;
    await writeFile(file, large, 'utf8');
    await assert.rejects(installQwenHooks({ command, scope: 'project', projectRoot: root }), /size limit/);
    assert.equal(await readFile(file, 'utf8'), large);
  } finally { await rm(root, { recursive: true, force: true }); }
});
