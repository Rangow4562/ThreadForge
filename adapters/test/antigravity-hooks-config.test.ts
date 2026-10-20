import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  antigravityMemoryManagedMarker,
  antigravityPlanGateManagedMarker,
  antigravityHooksConfigPath,
  installAntigravityHooks,
  installAntigravityProjectMemoryHook,
  installAntigravityProjectPlanGateHook,
  removeAntigravityHooks,
  removeAntigravityProjectMemoryHook,
  removeAntigravityProjectPlanGateHook,
} from '../src/antigravity-hooks-config.ts';
import { antigravityManagedMarker } from '../src/antigravity-hook.ts';

const command = `node antigravity-hook.ts ${antigravityManagedMarker}`;
const memoryCommand = `threadforge memory inject-antigravity --project-id demo-project --threadforge-managed-antigravity-memory=v1 ${antigravityMemoryManagedMarker}`;
const planGateCommand = `threadforge task plan guard --host antigravity-cli ${antigravityPlanGateManagedMarker} --threadforge-plan-gate=antigravity-v1`;
const events = ['PreInvocation', 'PostToolUse', 'PostInvocation', 'Stop'];

async function temporaryDirectory(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-antigravity-'));
}

test('installs and removes user hooks while preserving MCP config and unrelated hook blocks', async () => {
  const home = await temporaryDirectory();
  const file = antigravityHooksConfigPath('user', home);
  try {
    await mkdir(path.dirname(file), { recursive: true });
    const original = {
      other: { Stop: [{ hooks: [{ type: 'command', command: 'echo preserve' }] }] },
      mcpServers: { local: { command: 'keep-this-server' } },
    };
    await writeFile(file, JSON.stringify(original, null, 2), 'utf8');
    assert.equal(await installAntigravityHooks({ command, scope: 'user', homeDir: home }), 'installed');
    const installed = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(installed.other, original.other);
    assert.deepEqual(installed.mcpServers, original.mcpServers);
    for (const event of events) {
      const handlers = installed['threadforge-endpoint'][event];
      if (event === 'PostToolUse') {
        assert.equal(handlers.length, 1);
        assert.equal(handlers[0].matcher, '*');
        assert.equal(handlers[0].hooks[0].command, `${command} --hook-event=${event}`);
      } else {
        assert.equal(handlers.length, 1);
        assert.equal(handlers[0].command, `${command} --hook-event=${event}`);
        assert.equal(handlers[0].timeout, 30);
      }
    }
    assert.equal(installed['threadforge-endpoint'].UserPromptSubmit, undefined);
    assert.equal(JSON.stringify(installed).includes('trust'), false);
    const first = await readFile(file, 'utf8');
    assert.equal(await installAntigravityHooks({ command, scope: 'user', homeDir: home }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    assert.equal(await removeAntigravityHooks({ scope: 'user', homeDir: home }), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), original);
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('project scope uses .agents/hooks.json and preserves user edits mixed into managed groups', async () => {
  const root = await temporaryDirectory();
  const file = antigravityHooksConfigPath('project', root);
  try {
    assert.equal(await installAntigravityHooks({ command, scope: 'project', projectRoot: root }), 'installed');
    let config = JSON.parse(await readFile(file, 'utf8'));
    config['threadforge-endpoint'].PostToolUse[0].hooks.push({ type: 'command', command: 'echo user-added', timeout: 2 });
    config['threadforge-endpoint'].customNote = 'preserve edits';
    const userPreToolRef = { type: 'command', command: 'echo user-policy', timeout: 4 };
    const userLegacyPromptRef = { type: 'command', command: 'echo user-prompt-hook', timeout: 4 };
    config['threadforge-endpoint'].PreToolUse = [{ matcher: 'run_command', hooks: [userPreToolRef] }];
    config['threadforge-endpoint'].UserPromptSubmit = [userLegacyPromptRef];
    await writeFile(file, JSON.stringify(config, null, 2), 'utf8');

    const updatedCommand = `node dist/antigravity-hook.js ${antigravityManagedMarker}`;
    assert.equal(await installAntigravityHooks({ command: updatedCommand, scope: 'project', projectRoot: root }), 'updated');
    config = JSON.parse(await readFile(file, 'utf8'));
    const postHooks = config['threadforge-endpoint'].PostToolUse.flatMap((group: { hooks: unknown[] }) => group.hooks);
    assert.equal(postHooks.some((hook: { command: string }) => hook.command === 'echo user-added'), true);
    assert.equal(postHooks.some((hook: { command: string }) => hook.command.includes('node antigravity-hook.ts')), false);
    assert.equal(postHooks.some((hook: { command: string }) => hook.command === `${updatedCommand} --hook-event=PostToolUse`), true);
    assert.deepEqual(config['threadforge-endpoint'].PreToolUse, [{ matcher: 'run_command', hooks: [userPreToolRef] }]);
    assert.deepEqual(config['threadforge-endpoint'].UserPromptSubmit, [userLegacyPromptRef]);
    assert.equal(config['threadforge-endpoint'].customNote, 'preserve edits');

    assert.equal(await removeAntigravityHooks({ scope: 'project', projectRoot: root }), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config['threadforge-endpoint'].PostToolUse, [{ matcher: '*', hooks: [{ type: 'command', command: 'echo user-added', timeout: 2 }] }]);
    assert.deepEqual(config['threadforge-endpoint'].PreToolUse, [{ matcher: 'run_command', hooks: [userPreToolRef] }]);
    assert.deepEqual(config['threadforge-endpoint'].UserPromptSubmit, [userLegacyPromptRef]);
    assert.equal(config['threadforge-endpoint'].customNote, 'preserve edits');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('updates legacy nested hooks without reinstalling decision-bearing or unsupported events', async () => {
  const root = await temporaryDirectory();
  const file = antigravityHooksConfigPath('project', root);
  const legacyManaged = { type: 'command', command: `${command} --hook-event=PostToolUse`, timeout: 10 };
  const legacyUser = { type: 'command', command: 'echo legacy-user-ref', timeout: 3 };
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify({
      'threadforge-endpoint': {
        hooks: {
          PreInvocation: [{ hooks: [{ type: 'command', command: `${command} --hook-event=PreInvocation`, timeout: 30 }] }],
          PreToolUse: [{ hooks: [{ type: 'command', command: `${command} --hook-event=PreToolUse`, timeout: 10 }] }],
          PostToolUse: [{ hooks: [legacyManaged, legacyUser] }],
          UserPromptSubmit: [{ hooks: [{ type: 'command', command: `${command} --hook-event=UserPromptSubmit`, timeout: 30 }] }],
        },
        note: 'preserve legacy metadata',
      },
    }), 'utf8');

    const updatedCommand = `node dist/antigravity-hook.js ${antigravityManagedMarker}`;
    assert.equal(await installAntigravityHooks({ command: updatedCommand, scope: 'project', projectRoot: root }), 'updated');
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config['threadforge-endpoint'].PreInvocation[0].command, `${updatedCommand} --hook-event=PreInvocation`);
    assert.equal(config['threadforge-endpoint'].PreToolUse, undefined);
    assert.equal(config['threadforge-endpoint'].UserPromptSubmit, undefined);
    assert.deepEqual(config['threadforge-endpoint'].hooks.PostToolUse, [{ hooks: [legacyUser] }]);
    assert.equal(config['threadforge-endpoint'].note, 'preserve legacy metadata');

    assert.equal(await removeAntigravityHooks({ scope: 'project', projectRoot: root }), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config, { 'threadforge-endpoint': {
      hooks: { PostToolUse: [{ hooks: [legacyUser] }] }, note: 'preserve legacy metadata',
    } });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('project memory hook uses direct PreInvocation handlers and is independently removable', async () => {
  const root = await temporaryDirectory();
  const file = antigravityHooksConfigPath('project', root);
  const userHandler = { type: 'command', command: 'echo user-reminder', timeout: 5 };
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify({ reminder: { PreInvocation: [userHandler] }, other: { enabled: true } }), 'utf8');
    assert.equal(await installAntigravityHooks({ command, scope: 'project', projectRoot: root }), 'installed');
    assert.equal(await installAntigravityProjectMemoryHook({ command: memoryCommand, projectRoot: root }), 'installed');

    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.reminder.PreInvocation, [userHandler]);
    assert.deepEqual(config.other, { enabled: true });
    const handlers = config['threadforge-approved-memory'].PreInvocation;
    assert.equal(handlers.length, 1);
    assert.deepEqual(handlers[0], { type: 'command', command: memoryCommand, timeout: 10 });
    assert.equal(JSON.stringify(handlers).includes('threadforge-hook=antigravity'), false);
    assert.equal(await installAntigravityProjectMemoryHook({ command: memoryCommand, projectRoot: root }), 'unchanged');

    assert.equal(await removeAntigravityHooks({ scope: 'project', projectRoot: root }), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.ok(config['threadforge-approved-memory']);
    assert.deepEqual(config.reminder.PreInvocation, [userHandler]);

    assert.equal(await removeAntigravityProjectMemoryHook(root), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config, { reminder: { PreInvocation: [userHandler] }, other: { enabled: true } });
    assert.equal(await removeAntigravityProjectMemoryHook(root), 'absent');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('project memory hook preserves non-command PreInvocation refs and refuses unowned or unsafe blocks', async () => {
  const root = await temporaryDirectory();
  const file = antigravityHooksConfigPath('project', root);
  const userRef = { type: 'command', command: 'echo user-owned' };
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify({ 'threadforge-approved-memory': { PreInvocation: [userRef] } }), 'utf8');
    await assert.rejects(installAntigravityProjectMemoryHook({ command: memoryCommand, projectRoot: root }), /without Threadforge ownership/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8'))['threadforge-approved-memory'].PreInvocation, [userRef]);

    await writeFile(file, JSON.stringify({ reminder: { PreInvocation: [{ message: 'preserve this host ref' }] } }), 'utf8');
    assert.equal(await installAntigravityProjectMemoryHook({ command: memoryCommand, projectRoot: root }), 'installed');
    const config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.reminder.PreInvocation, [{ message: 'preserve this host ref' }]);
    await assert.rejects(installAntigravityProjectMemoryHook({ command: 'threadforge memory inject-antigravity', projectRoot: root }), /must include/);
    await assert.rejects(installAntigravityProjectMemoryHook({ command: `API_TOKEN=secret ${memoryCommand}`, projectRoot: root }), /credential/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('project plan gate installs only direct file PreToolUse handlers and preserves other owners', async () => {
  const project = await temporaryDirectory();
  const file = antigravityHooksConfigPath('project', project);
  try {
    await mkdir(path.dirname(file), { recursive: true });
    const original = {
      'threadforge-endpoint': { PostInvocation: [{ type: 'command', command: 'telemetry-hook' }] },
      'threadforge-approved-memory': { PreInvocation: [{ type: 'command', command: 'memory-hook' }] },
      user: { PreToolUse: [{ matcher: 'run_command', hooks: [{ type: 'command', command: 'user-policy-hook' }] }] },
      mcpServers: { preserve: { command: 'mcp-server' } },
    };
    await writeFile(file, JSON.stringify(original, null, 2), 'utf8');

    assert.equal(await installAntigravityProjectPlanGateHook({ command: planGateCommand, projectRoot: project }), 'installed');
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config['threadforge-endpoint'], original['threadforge-endpoint']);
    assert.deepEqual(config['threadforge-approved-memory'], original['threadforge-approved-memory']);
    assert.deepEqual(config.user, original.user);
    assert.deepEqual(config.mcpServers, original.mcpServers);
    assert.equal(config['threadforge-task-plan-gate'].enabled, true);
    assert.deepEqual(config['threadforge-task-plan-gate'].PreToolUse, [
      {
        matcher: '^(write_to_file|replace_file_content|multi_replace_file_content)$',
        hooks: [{ type: 'command', command: planGateCommand, timeout: 10 }],
      },
    ]);
    const first = await readFile(file, 'utf8');
    assert.equal(await installAntigravityProjectPlanGateHook({ command: planGateCommand, projectRoot: project }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);

    const updated = `threadforge task plan guard --host antigravity-cli --task-id next --threadforge-managed-antigravity-plan-gate=v1 --threadforge-plan-gate=antigravity-v1 ${antigravityPlanGateManagedMarker}`;
    assert.equal(await installAntigravityProjectPlanGateHook({ command: updated, projectRoot: project }), 'updated');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config['threadforge-task-plan-gate'].PreToolUse.length, 1);
    assert.equal(config['threadforge-task-plan-gate'].PreToolUse[0].hooks[0].command, updated);

    assert.equal(await removeAntigravityProjectPlanGateHook(project), 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config['threadforge-task-plan-gate'], undefined);
    assert.deepEqual(config['threadforge-endpoint'], original['threadforge-endpoint']);
    assert.deepEqual(config['threadforge-approved-memory'], original['threadforge-approved-memory']);
    assert.deepEqual(config.user, original.user);
    assert.deepEqual(config.mcpServers, original.mcpServers);
    assert.equal(await removeAntigravityProjectPlanGateHook(project), 'absent');
  } finally {
    await rm(project, { recursive: true, force: true });
  }
});

test('project plan gate refuses unowned blocks and commands without both markers', async () => {
  const project = await temporaryDirectory();
  const file = antigravityHooksConfigPath('project', project);
  try {
    await assert.rejects(installAntigravityProjectPlanGateHook({ command: 'threadforge task plan guard', projectRoot: project }), /include/);
    await assert.rejects(installAntigravityProjectPlanGateHook({
      command: `threadforge task plan guard --host antigravity-cli ${antigravityPlanGateManagedMarker}`,
      projectRoot: project,
    }), /include/);
    await mkdir(path.dirname(file), { recursive: true });
    const original = { 'threadforge-task-plan-gate': { enabled: false, custom: true } };
    await writeFile(file, JSON.stringify(original), 'utf8');
    await assert.rejects(installAntigravityProjectPlanGateHook({ command: planGateCommand, projectRoot: project }), /without Threadforge ownership/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), original);
  } finally {
    await rm(project, { recursive: true, force: true });
  }
});

test('fails closed on unowned blocks, malformed JSON, invalid commands, and stale locks', async () => {
  const home = await temporaryDirectory();
  const file = antigravityHooksConfigPath('user', home);
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await assert.rejects(installAntigravityHooks({ command: 'node hook.js', scope: 'user', homeDir: home }), /threadforge-hook/);
    await assert.rejects(installAntigravityHooks({ command: `API_TOKEN=secret ${command}`, scope: 'user', homeDir: home }), /credential/);

    const unowned = JSON.stringify({ 'threadforge-endpoint': { Stop: [{ type: 'command', command: 'echo user-owned' }] } });
    await writeFile(file, unowned, 'utf8');
    await assert.rejects(installAntigravityHooks({ command, scope: 'user', homeDir: home }), /without Threadforge ownership/);
    assert.equal(await readFile(file, 'utf8'), unowned);

    const malformed = '{not-json';
    await writeFile(file, malformed, 'utf8');
    await assert.rejects(installAntigravityHooks({ command, scope: 'user', homeDir: home }), /strict JSON/);
    assert.equal(await readFile(file, 'utf8'), malformed);

    await writeFile(path.join(path.dirname(file), '.threadforge-antigravity-hooks.lock'), 'stale', 'utf8');
    await assert.rejects(installAntigravityHooks({ command, scope: 'user', homeDir: home }), /locked/);
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('uninstall with absent user hooks does not create configuration directories', async () => {
  const home = await temporaryDirectory();
  try {
    assert.equal(await removeAntigravityHooks({ scope: 'user', homeDir: home }), 'absent');
    await assert.rejects(readFile(path.join(home, '.gemini', 'config', 'hooks.json')), /ENOENT/);
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('rejects project symlinks and oversized hook settings without modifying files', async (t) => {
  const root = await temporaryDirectory();
  const outside = await temporaryDirectory();
  try {
    await assert.rejects(installAntigravityHooks({ command, scope: 'project' }), /absolute project root/);
    try { await symlink(outside, path.join(root, '.agents'), 'junction'); }
    catch (error) {
      if (error && typeof error === 'object' && 'code' in error && ['EPERM', 'EACCES', 'ERROR_ACCESS_DENIED'].includes(String(error.code))) {
        t.skip('the current Windows environment does not permit creating directory symlinks');
        return;
      }
      throw error;
    }
    await assert.rejects(installAntigravityHooks({ command, scope: 'project', projectRoot: root }), /symbolic link/);

    await rm(path.join(root, '.agents'), { recursive: true, force: true });
    const file = antigravityHooksConfigPath('project', root);
    await mkdir(path.dirname(file));
    const large = `{"keep":"${'x'.repeat((8 << 20) + 1)}"}`;
    await writeFile(file, large, 'utf8');
    await assert.rejects(installAntigravityHooks({ command, scope: 'project', projectRoot: root }), /size limit/);
    assert.equal(await readFile(file, 'utf8'), large);
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});
