import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  geminiMemoryManagedMarker,
  geminiPlanGateManagedMarker,
  installGeminiProjectMemoryHook,
  installGeminiProjectPlanGateHook,
  removeGeminiProjectMemoryHook,
  removeGeminiProjectPlanGateHook,
} from '../src/gemini-memory-config.ts';

const command = `threadforge memory inject-gemini --project-id demo --threadforge-managed-gemini-memory=v1 ${geminiMemoryManagedMarker}`;
const planGateCommand = `threadforge task plan guard --host gemini-cli --task-id task-a --project-id demo --project-root C:/workspace --threadforge-managed-gemini-plan-gate=v1 ${geminiPlanGateManagedMarker}`;

test('installs approved Gemini BeforeAgent retrieval without changing trust, MCP, telemetry, or user hooks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-memory-'));
  const directory = path.join(root, '.gemini');
  await mkdir(directory);
  const file = path.join(directory, 'settings.json');
  const userHook = { name: 'user-before-agent', type: 'command', command: 'node user-hook.js', timeout: 30000 };
  const otherEvent = { name: 'user-after-agent', type: 'command', command: 'node after-hook.js', timeout: 30000 };
  const initial = {
    mcpServers: { threadforge: { command: 'threadforge', trust: false } },
    telemetry: { enabled: true, logPrompts: false, target: 'local' },
    hooks: {
      BeforeAgent: [{ matcher: '*', hooks: [userHook] }],
      AfterAgent: [{ hooks: [otherEvent] }],
    },
  };
  await writeFile(file, `${JSON.stringify(initial, null, 2)}\n`, { mode: 0o600 });
  try {
    assert.equal(await installGeminiProjectMemoryHook(root, command), 'installed');
    const installed = JSON.parse(await readFile(file, 'utf8')) as Record<string, any>;
    assert.deepEqual(installed.mcpServers, initial.mcpServers);
    assert.deepEqual(installed.telemetry, initial.telemetry);
    assert.deepEqual(installed.hooks.AfterAgent, initial.hooks.AfterAgent);
    assert.deepEqual(installed.hooks.BeforeAgent[0], initial.hooks.BeforeAgent[0]);
    assert.deepEqual(installed.hooks.BeforeAgent[1], {
      hooks: [{
        name: 'threadforge-approved-memory',
        type: 'command',
        command,
        timeout: 10000,
      }],
    });
    assert.equal(await installGeminiProjectMemoryHook(root, command), 'unchanged');

    const updatedCommand = `${command} --max-items 4`;
    assert.equal(await installGeminiProjectMemoryHook(root, updatedCommand), 'updated');
    const updated = JSON.parse(await readFile(file, 'utf8')) as Record<string, any>;
    assert.equal(updated.hooks.BeforeAgent[0].hooks[0].command, 'node user-hook.js');
    assert.equal(updated.hooks.BeforeAgent[1].hooks[0].command, updatedCommand);

    assert.equal(await removeGeminiProjectMemoryHook(root), 'removed');
    const removed = JSON.parse(await readFile(file, 'utf8')) as Record<string, any>;
    assert.deepEqual(removed.hooks.BeforeAgent, initial.hooks.BeforeAgent);
    assert.deepEqual(removed.hooks.AfterAgent, initial.hooks.AfterAgent);
    assert.deepEqual(removed.mcpServers, initial.mcpServers);
    assert.deepEqual(removed.telemetry, initial.telemetry);
    assert.equal(await removeGeminiProjectMemoryHook(root), 'absent');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('requires both ownership markers and leaves malformed settings unchanged', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-memory-invalid-'));
  try {
    await assert.rejects(installGeminiProjectMemoryHook(root, 'threadforge memory inject-gemini'), /include/);
    const directory = path.join(root, '.gemini');
    await mkdir(directory);
    const file = path.join(directory, 'settings.json');
    await writeFile(file, '{not json', { mode: 0o600 });
    await assert.rejects(installGeminiProjectMemoryHook(root, command), /invalid strict JSON/);
    assert.equal(await readFile(file, 'utf8'), '{not json');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('removing an absent Gemini memory hook does not create settings directories', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-memory-absent-'));
  try {
    assert.equal(await removeGeminiProjectMemoryHook(root), 'absent');
    await assert.rejects(readFile(path.join(root, '.gemini', 'settings.json')));
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('refuses a conflicting Gemini config lock before changing project settings', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-memory-lock-'));
  const directory = path.join(root, '.gemini');
  await mkdir(directory);
  const file = path.join(directory, 'settings.json');
  const source = JSON.stringify({ telemetry: { logPrompts: false } });
  await writeFile(file, source, { mode: 0o600 });
  await writeFile(path.join(directory, '.threadforge-mcp-config.lock'), 'locked', { mode: 0o600 });
  try {
    await assert.rejects(installGeminiProjectMemoryHook(root, command), /locked/);
    assert.equal(await readFile(file, 'utf8'), source);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('installs Gemini BeforeTool plan gate independently and preserves all unrelated settings and mixed hook groups', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-plan-gate-'));
  const directory = path.join(root, '.gemini');
  await mkdir(directory);
  const file = path.join(directory, 'settings.json');
  const userHook = { name: 'user-before-tool', type: 'command', command: 'node user-tool.js', timeout: 30000 };
  const mixedUserHook = { name: 'user-mixed-hook', type: 'command', command: 'node mixed.js', timeout: 30000 };
  const staleManagedHook = {
    name: 'old-threadforge-gate', type: 'command',
    command: `threadforge task plan guard --threadforge-managed-gemini-plan-gate=v1 ${geminiPlanGateManagedMarker}`,
    timeout: 1000,
  };
  const initial = {
    mcpServers: { threadforge: { command: 'threadforge', trust: false } },
    telemetry: { enabled: true, logPrompts: false },
    trustedFolders: { '/workspace': true },
    hooks: {
      BeforeAgent: [{ matcher: '*', hooks: [{ name: 'user-prompt', type: 'command', command: 'node prompt.js' }] }],
      BeforeTool: [
        { matcher: 'user-tool', hooks: [userHook] },
        { matcher: 'mixed-tools', hooks: [mixedUserHook, staleManagedHook] },
      ],
    },
  };
  await writeFile(file, `${JSON.stringify(initial, null, 2)}\n`, { mode: 0o600 });
  try {
    assert.equal(await installGeminiProjectPlanGateHook(root, planGateCommand), 'updated');
    let settings = JSON.parse(await readFile(file, 'utf8')) as Record<string, any>;
    assert.deepEqual(settings.mcpServers, initial.mcpServers);
    assert.deepEqual(settings.telemetry, initial.telemetry);
    assert.deepEqual(settings.trustedFolders, initial.trustedFolders);
    assert.deepEqual(settings.hooks.BeforeAgent, initial.hooks.BeforeAgent);
    assert.deepEqual(settings.hooks.BeforeTool[0], initial.hooks.BeforeTool[0]);
    assert.deepEqual(settings.hooks.BeforeTool[1], { matcher: 'mixed-tools', hooks: [mixedUserHook] });
    assert.deepEqual(settings.hooks.BeforeTool[2], {
      matcher: '^(write_file|replace)$',
      hooks: [{ name: 'threadforge-task-plan-gate', type: 'command', command: planGateCommand, timeout: 10000 }],
    });
    assert.equal(await installGeminiProjectPlanGateHook(root, planGateCommand), 'unchanged');

    const updatedCommand = `${planGateCommand} --max-items 4`;
    assert.equal(await installGeminiProjectPlanGateHook(root, updatedCommand), 'updated');
    settings = JSON.parse(await readFile(file, 'utf8')) as Record<string, any>;
    assert.equal(settings.hooks.BeforeTool.length, 3);
    assert.equal(settings.hooks.BeforeTool[2].hooks[0].command, updatedCommand);

    assert.equal(await removeGeminiProjectPlanGateHook(root), 'removed');
    settings = JSON.parse(await readFile(file, 'utf8')) as Record<string, any>;
    assert.deepEqual(settings.hooks.BeforeTool, [
      initial.hooks.BeforeTool[0],
      { matcher: 'mixed-tools', hooks: [mixedUserHook] },
    ]);
    assert.deepEqual(settings.hooks.BeforeAgent, initial.hooks.BeforeAgent);
    assert.deepEqual(settings.mcpServers, initial.mcpServers);
    assert.deepEqual(settings.telemetry, initial.telemetry);
    assert.deepEqual(settings.trustedFolders, initial.trustedFolders);
    assert.equal(await removeGeminiProjectPlanGateHook(root), 'absent');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('Gemini plan-gate manager rejects missing ownership markers and malformed BeforeTool config', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-plan-gate-invalid-'));
  try {
    await assert.rejects(installGeminiProjectPlanGateHook(root, 'threadforge task plan guard'), /include/);
    const directory = path.join(root, '.gemini');
    await mkdir(directory);
    const file = path.join(directory, 'settings.json');
    const malformed = JSON.stringify({ hooks: { BeforeTool: [{ matcher: '*', hooks: 'invalid' }] } });
    await writeFile(file, malformed, { mode: 0o600 });
    await assert.rejects(installGeminiProjectPlanGateHook(root, planGateCommand), /unsupported hook group/);
    assert.equal(await readFile(file, 'utf8'), malformed);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
