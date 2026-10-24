import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  copilotMemoryManagedMarker,
  copilotProjectMemoryHooksConfigPath,
  copilotProjectHooksConfigPath,
  copilotPlanGateManagedMarker,
  installCopilotProjectMemoryHook,
  installCopilotProjectPlanGateHook,
  removeCopilotProjectMemoryHook,
  removeCopilotProjectPlanGateHook,
} from '../src/copilot-hooks-config.ts';

const runnerMarker = '--threadforge-managed-copilot-plan-gate=v1';
const command = `threadforge task plan guard --host github-copilot ${runnerMarker} ${copilotPlanGateManagedMarker}`;
const memoryRunnerMarker = '--threadforge-managed-copilot-memory=v1';
const memoryBashCommand = `threadforge memory inject-copilot --project-id demo-project ${memoryRunnerMarker} ${copilotMemoryManagedMarker}`;
const memoryPowerShellCommand = `threadforge.exe memory inject-copilot --project-id demo-project ${memoryRunnerMarker} ${copilotMemoryManagedMarker}`;

test('Copilot CLI memory hook uses the prompt-transform event and preserves unrelated hook state', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-memory-'));
  try {
    const configPath = copilotProjectMemoryHooksConfigPath(root);
    await mkdir(path.dirname(configPath), { recursive: true });
    const planGatePath = copilotProjectHooksConfigPath(root);
    const planGateBytes = Buffer.from('{"version":1,"hooks":{"preToolUse":[]}}\n');
    await writeFile(planGatePath, planGateBytes);
    const original = {
      version: 1,
      userSetting: 'preserve-me',
      hooks: {
        userPromptTransformed: [
          { type: 'command', bash: 'user-memory-hook --keep' },
          { type: 'http', url: 'https://example.invalid/hook' },
          { type: 'command', bash: `old ${memoryRunnerMarker} ${copilotMemoryManagedMarker}` },
        ],
        sessionStart: [{ type: 'command', command: 'user-session-hook --keep' }],
      },
    };
    await writeFile(configPath, `${JSON.stringify(original, null, 2)}\n`);

    assert.equal(await installCopilotProjectMemoryHook({
      projectRoot: root, bashCommand: memoryBashCommand, powershellCommand: memoryPowerShellCommand,
    }), 'updated');
    let saved = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(saved.userSetting, 'preserve-me');
    assert.deepEqual(saved.hooks.userPromptTransformed.slice(0, 2), original.hooks.userPromptTransformed.slice(0, 2));
    assert.deepEqual(saved.hooks.sessionStart, original.hooks.sessionStart);
    const managed = saved.hooks.userPromptTransformed.filter((hook: { bash?: string; powershell?: string }) =>
      hook.bash?.includes(copilotMemoryManagedMarker) || hook.powershell?.includes(copilotMemoryManagedMarker));
    assert.deepEqual(managed, [{
      type: 'command', bash: memoryBashCommand, powershell: memoryPowerShellCommand, timeoutSec: 3,
    }]);
    assert.equal(await installCopilotProjectMemoryHook({
      projectRoot: root, bashCommand: memoryBashCommand, powershellCommand: memoryPowerShellCommand,
    }), 'unchanged');
    assert.deepEqual(await readFile(planGatePath), planGateBytes);

    assert.equal(await removeCopilotProjectMemoryHook(root), 'removed');
    saved = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(saved.hooks.userPromptTransformed, original.hooks.userPromptTransformed.slice(0, 2));
    assert.deepEqual(saved.hooks.sessionStart, original.hooks.sessionStart);
    assert.equal(await removeCopilotProjectMemoryHook(root), 'absent');
    assert.deepEqual(await readFile(planGatePath), planGateBytes);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('Copilot CLI memory installer rejects malformed files and secret-bearing shell commands', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-memory-invalid-'));
  try {
    const configPath = copilotProjectMemoryHooksConfigPath(root);
    await mkdir(path.dirname(configPath), { recursive: true });
    const malformed = Buffer.from('{ not-json');
    await writeFile(configPath, malformed);
    await assert.rejects(installCopilotProjectMemoryHook({
      projectRoot: root, bashCommand: memoryBashCommand, powershellCommand: memoryPowerShellCommand,
    }), /valid UTF-8 JSON/);
    assert.deepEqual(await readFile(configPath), malformed);
    await assert.rejects(installCopilotProjectMemoryHook({
      projectRoot: root,
      bashCommand: `${memoryBashCommand} --api-key=sk-${'x'.repeat(24)}`,
      powershellCommand: memoryPowerShellCommand,
    }), /credential-like values/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('Copilot project plan gate preserves unrelated hooks and neighboring hook files', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-plan-gate-'));
  try {
    const configPath = copilotProjectHooksConfigPath(root);
    await mkdir(path.dirname(configPath), { recursive: true });
    const neighboringPath = path.join(path.dirname(configPath), 'user-hooks.json');
    const neighboringBytes = Buffer.from('{"version":1,"hooks":{"sessionStart":[]}}\n');
    await writeFile(neighboringPath, neighboringBytes);
    const existing = {
      version: 1,
      userSetting: 'preserve-me',
      hooks: {
        preToolUse: [
          { type: 'command', command: 'user-pre-tool --keep', matcher: '*' },
          { type: 'command', command: `old ${runnerMarker} ${copilotPlanGateManagedMarker}`, matcher: '.*' },
        ],
        postToolUse: [],
        notification: [{ type: 'command', command: 'user-notification --keep' }],
      },
    };
    await writeFile(configPath, `${JSON.stringify(existing, null, 2)}\n`);

    assert.equal(await installCopilotProjectPlanGateHook({ command, projectRoot: root }), 'updated');
    let saved = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(saved.userSetting, 'preserve-me');
    assert.deepEqual(saved.hooks.preToolUse[0], existing.hooks.preToolUse[0]);
    assert.deepEqual(saved.hooks.postToolUse, []);
    assert.deepEqual(saved.hooks.notification, existing.hooks.notification);
    const managed = saved.hooks.preToolUse.filter((hook: { bash?: string; powershell?: string }) =>
      hook.bash?.includes(copilotPlanGateManagedMarker) || hook.powershell?.includes(copilotPlanGateManagedMarker));
    assert.equal(managed.length, 1);
    assert.equal(managed[0].matcher, '^(create|edit|apply_patch|str_replace_editor)$');
    assert.equal(managed[0].timeoutSec, 10);
    assert.equal(managed[0].bash, command);
    assert.equal(managed[0].powershell, command);
    assert.equal(await installCopilotProjectPlanGateHook({ command, projectRoot: root }), 'unchanged');
    assert.equal(await installCopilotProjectPlanGateHook({ command: `${command} --db D:/isolated/threadforge.sqlite`, projectRoot: root }), 'updated');
    saved = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(saved.hooks.preToolUse.filter((hook: { bash?: string; powershell?: string }) =>
      hook.bash?.includes(copilotPlanGateManagedMarker) || hook.powershell?.includes(copilotPlanGateManagedMarker)).length, 1);
    assert.deepEqual(await readFile(neighboringPath), neighboringBytes);

    assert.equal(await removeCopilotProjectPlanGateHook(root), 'removed');
    saved = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(saved.hooks.preToolUse, [existing.hooks.preToolUse[0]]);
    assert.deepEqual(saved.hooks.postToolUse, []);
    assert.deepEqual(saved.hooks.notification, existing.hooks.notification);
    assert.deepEqual(await readFile(neighboringPath), neighboringBytes);
    assert.equal(await removeCopilotProjectPlanGateHook(root), 'absent');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('Copilot plan gate refuses malformed configuration and credential-like commands', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-plan-gate-invalid-'));
  try {
    const configPath = copilotProjectHooksConfigPath(root);
    await mkdir(path.dirname(configPath), { recursive: true });
    const malformed = Buffer.from('{ not-json');
    await writeFile(configPath, malformed);
    await assert.rejects(
      installCopilotProjectPlanGateHook({ command, projectRoot: root }),
      /valid UTF-8 JSON/,
    );
    assert.deepEqual(await readFile(configPath), malformed);
    await assert.rejects(
      installCopilotProjectPlanGateHook({ command: `${command} --api-key=sk-${'x'.repeat(24)}`, projectRoot: root }),
      /credential-like values/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
