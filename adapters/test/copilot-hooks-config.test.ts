import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  copilotProjectHooksConfigPath,
  copilotPlanGateManagedMarker,
  installCopilotProjectPlanGateHook,
  removeCopilotProjectPlanGateHook,
} from '../src/copilot-hooks-config.ts';

const runnerMarker = '--threadforge-managed-copilot-plan-gate=v1';
const command = `threadforge task plan guard --host github-copilot ${runnerMarker} ${copilotPlanGateManagedMarker}`;

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
