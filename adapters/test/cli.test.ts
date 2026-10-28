import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, realpath, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const cli = fileURLToPath(new URL('../src/cli.ts', import.meta.url));

async function invoke(args: string[], env: NodeJS.ProcessEnv = {}, stdinInput = ''): Promise<{ code: number | null; stdout: string; stderr: string }> {
  const child = spawn(process.execPath, ['--experimental-strip-types', cli, ...args], {
    env: { ...process.env, ...env },
    stdio: ['pipe', 'pipe', 'pipe'],
  });
  child.stdin.end(stdinInput);
  let stdout = '';
  let stderr = '';
  child.stdout.setEncoding('utf8');
  child.stderr.setEncoding('utf8');
  child.stdout.on('data', (chunk) => { stdout += chunk; });
  child.stderr.on('data', (chunk) => { stderr += chunk; });
  const code = await new Promise<number | null>((resolve, reject) => {
    child.once('error', reject);
    child.once('close', resolve);
  });
  return { code, stdout, stderr };
}

test('adapter CLI installs, updates, and removes Claude hooks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-adapter-cli-'));
  try {
    const command = 'node hook.js --threadforge-hook=claude';
    let result = await invoke(['claude', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');

    result = await invoke(['claude', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['claude', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(path.join(root, '.claude', 'settings.json'), 'utf8')), {});
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI manages Claude approved-memory injection without disturbing other hook owners', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-memory-cli-'));
  try {
    const command = 'threadforge memory inject --project-id demo-project --threadforge-managed-claude-memory=v1 --threadforge-memory=claude-v1';
    let result = await invoke(['claude-code', 'memory', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    let settings = JSON.parse(await readFile(path.join(root, '.claude', 'settings.json'), 'utf8'));
    assert.equal(settings.hooks.UserPromptSubmit[0].hooks[0].command, command);

    result = await invoke(['claude-code', 'memory', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke([
      'claude-code', 'memory', 'update', '--project-root', root, '--command', command,
      '--task-id', 'claude-task', '--session-id', 'claude-session', '--max-task-entries', '2',
    ]);
    assert.equal(result.code, 0, result.stderr);
    const explicitTaskCommand = command.replace(
      '--threadforge-memory=claude-v1',
      '--task-id claude-task --session-id claude-session --max-task-entries 2 --threadforge-memory=claude-v1',
    );
    settings = JSON.parse(await readFile(path.join(root, '.claude', 'settings.json'), 'utf8'));
    assert.equal(settings.hooks.UserPromptSubmit[0].hooks[0].command, explicitTaskCommand);

    result = await invoke([
      'claude-code', 'memory', 'update', '--project-root', root, '--command', command, '--discover-active-task',
    ]);
    assert.equal(result.code, 0, result.stderr);
    const discoveryCommand = command.replace(
      '--threadforge-memory=claude-v1',
      '--discover-active-task --max-task-entries 6 --threadforge-memory=claude-v1',
    );
    settings = JSON.parse(await readFile(path.join(root, '.claude', 'settings.json'), 'utf8'));
    assert.equal(settings.hooks.UserPromptSubmit[0].hooks[0].command, discoveryCommand);

    const settingsPath = path.join(root, '.claude', 'settings.json');
    const beforeInvalidOptions = await readFile(settingsPath, 'utf8');
    for (const [invalidOptions, expectedError] of [
      [['--task-id', 'claude-task', '--discover-active-task'], /mutually exclusive/],
      [['--session-id', 'claude-session'], /requires --task-id/],
      [['--task-id', 'Claude/Task'], /--task-id must match/],
      [['--discover-active-task', '--max-task-entries', '21'], /between 1 and 20/],
      [['--max-task-entries', '2'], /requires --task-id or --discover-active-task/],
    ] as const) {
      result = await invoke([
        'claude-code', 'memory', 'update', '--project-root', root, '--command', command, ...invalidOptions,
      ]);
      assert.equal(result.code, 1);
      assert.match(result.stderr, expectedError);
    }
    assert.equal(await readFile(settingsPath, 'utf8'), beforeInvalidOptions);
    result = await invoke([
      'claude-code', 'memory', 'update', '--project-root', root,
      '--command', `${command} --task-id claude-manual-task`, '--discover-active-task',
    ]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /already contains task-resume options/);
    assert.equal(await readFile(settingsPath, 'utf8'), beforeInvalidOptions);

    result = await invoke(['claude-code', 'memory', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    settings = JSON.parse(await readFile(path.join(root, '.claude', 'settings.json'), 'utf8'));
    assert.deepEqual(settings, {});
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs only the explicit Claude deny-only plan-gate hook', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-plan-gate-cli-'));
  try {
    const command = 'threadforge task plan guard --task-id task-a --project-id demo --project-root C:/workspace --threadforge-managed-claude-plan-gate=v1 --threadforge-plan-gate=claude-v1';
    let result = await invoke(['claude-code', 'plan-gate', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /deny-only/);
    let settings = JSON.parse(await readFile(path.join(root, '.claude', 'settings.json'), 'utf8'));
    assert.deepEqual(settings.hooks.PreToolUse, [{
      matcher: 'Write|Edit',
      hooks: [{ type: 'command', command }],
    }]);

    result = await invoke(['claude-code', 'plan-gate', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['claude-code', 'plan-gate', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    settings = JSON.parse(await readFile(path.join(root, '.claude', 'settings.json'), 'utf8'));
    assert.deepEqual(settings, {});

    result = await invoke(['claude-code', 'plan-gate', 'install', '--project-root', root, '--command',
      'threadforge task plan guard --threadforge-plan-gate=claude-v1']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /runtime marker/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs only the explicit Qwen project deny-only plan-gate hook', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-qwen-plan-gate-cli-'));
  try {
    const command = 'threadforge task plan guard --host qwen-code --task-id task-a --project-id demo --project-root C:/workspace --threadforge-managed-qwen-plan-gate=v1 --threadforge-plan-gate=qwen-v1';
    let result = await invoke(['qwen', 'plan-gate', 'install', '--scope', 'project', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /deny-only/);
    assert.match(JSON.parse(result.stdout).warning, /not a fail-closed security boundary/);
    assert.match(JSON.parse(result.stdout).warning, /does not change trust/);
    let settings = JSON.parse(await readFile(path.join(root, '.qwen', 'settings.json'), 'utf8'));
    assert.deepEqual(settings.hooks.PreToolUse, [{
      matcher: '^(write_file|edit)$',
      hooks: [{ type: 'command', shell: 'bash', command, timeout: 10 }],
    }]);

    result = await invoke(['qwen', 'plan-gate', 'update', '--scope', 'project', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['qwen', 'plan-gate', 'uninstall', '--scope', 'project', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    settings = JSON.parse(await readFile(path.join(root, '.qwen', 'settings.json'), 'utf8'));
    assert.deepEqual(settings, {});

    result = await invoke(['qwen', 'plan-gate', 'install', '--scope', 'project', '--project-root', root, '--command',
      'threadforge task plan guard --threadforge-plan-gate=qwen-v1']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /include.*managed-qwen-plan-gate/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs only the explicit GitHub Copilot CLI deny-only plan-gate hook', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-plan-gate-cli-'));
  try {
    const common = 'threadforge task plan guard --host github-copilot --task-id task-a --project-id demo';
    const bashCommand = `${common} --project-root /workspace --threadforge-managed-copilot-plan-gate=v1 --threadforge-plan-gate=copilot-v1`;
    const powershellCommand = `${common} --project-root C:/workspace --threadforge-managed-copilot-plan-gate=v1 --threadforge-plan-gate=copilot-v1`;
    let result = await invoke(['copilot', 'plan-gate', 'install', '--project-root', root,
      '--bash-command', bashCommand, '--powershell-command', powershellCommand]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /deny-only/);
    assert.match(JSON.parse(result.stdout).warning, /timeout/);
    const configPath = path.join(root, '.github', 'hooks', 'threadforge-plan-gate.json');
    let config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(config.version, 1);
    assert.deepEqual(config.hooks.preToolUse, [{
      type: 'command', bash: bashCommand, powershell: powershellCommand,
      matcher: '^(create|edit|apply_patch|str_replace_editor)$', timeoutSec: 10,
    }]);

    result = await invoke(['copilot', 'plan-gate', 'update', '--project-root', root,
      '--bash-command', bashCommand, '--powershell-command', powershellCommand]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['copilot', 'plan-gate', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config, { version: 1 });

    result = await invoke(['copilot', 'plan-gate', 'install', '--project-root', root, '--command',
      'threadforge task plan guard --threadforge-plan-gate=copilot-v1']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /managed-copilot-plan-gate/);

    result = await invoke(['copilot', 'plan-gate', 'install', '--project-root', root, '--bash-command', bashCommand]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /both --bash-command and --powershell-command/);

    result = await invoke(['copilot', 'plan-gate', 'install', '--project-root', root,
      '--command', bashCommand, '--powershell-command', powershellCommand]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /either --command or both/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI explicitly installs and removes GitHub Copilot CLI approved-memory hooks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-memory-cli-'));
  try {
    const runner = '--threadforge-managed-copilot-memory=v1';
    const marker = '--threadforge-memory=copilot-v1';
    const bashCommand = `threadforge memory inject-copilot --project-id demo-project ${runner} ${marker}`;
    const powershellCommand = `threadforge.exe memory inject-copilot --project-id demo-project ${runner} ${marker}`;
    let result = await invoke(['copilot', 'memory', 'install', '--project-root', root,
      '--bash-command', bashCommand, '--powershell-command', powershellCommand]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /userPromptTransformed/);
    assert.match(JSON.parse(result.stdout).warning, /session history/);
    const configPath = path.join(root, '.github', 'hooks', 'threadforge-memory.json');
    let config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(config.version, 1);
    assert.deepEqual(config.hooks.userPromptTransformed, [{
      type: 'command', bash: bashCommand, powershell: powershellCommand, timeoutSec: 3,
    }]);

    result = await invoke(['copilot', 'memory', 'update', '--project-root', root,
      '--bash-command', bashCommand, '--powershell-command', powershellCommand]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['copilot', 'memory', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config, { version: 1 });

    result = await invoke(['copilot', 'memory', 'install', '--project-root', root, '--command',
      `threadforge memory inject-copilot --project-id demo ${marker}`]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /managed-copilot-memory/);

    result = await invoke(['copilot', 'memory', 'install', '--project-root', root, '--bash-command', bashCommand]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /both --bash-command and --powershell-command/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('Claude transcript CLI exposes metadata-only status and non-mutating preview', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-history-cli-'));
  const projects = path.join(root, 'projects', 'project');
  const statePath = path.join(root, 'threadforge', 'claude-history.json');
  try {
    await mkdir(projects, { recursive: true });
    await writeFile(path.join(projects, 'session.jsonl'), `${JSON.stringify({
      type: 'user', uuid: 'private-id', timestamp: '2026-09-24T10:00:00.000Z', sessionId: 'private-session',
      message: { role: 'user', content: 'private prompt' },
    })}\n`, 'utf8');

    let result = await invoke(['claude', 'sessions', 'status', '--projects-dir', path.dirname(projects), '--state-path', statePath]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      schema_version: 'threadforge.claude-history-status.v1', sessions: 1, checkpointed_sessions: 0,
      pending_sessions: 1, symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false,
    });

    result = await invoke(['claude', 'sessions', 'sync', '--projects-dir', path.dirname(projects), '--state-path', statePath, '--print']);
    assert.equal(result.code, 0, result.stderr);
    const preview = JSON.parse(result.stdout) as { events_previewed: number; complete: boolean; events: unknown[] };
    assert.equal(preview.complete, true);
    assert.equal(preview.events_previewed, 2);
    assert.equal(JSON.stringify(preview.events).includes('private prompt'), false);
    await assert.rejects(readFile(statePath), /ENOENT/);

    result = await invoke(['claude', 'sessions', 'sync', '--projects-dir', path.dirname(projects), '--max-files', '5001', '--print']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /between 1 and 5000/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Cline history CLI reports stores and provides a non-mutating bounded preview', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-history-cli-'));
  const clineDir = path.join(root, '.cline');
  const taskDir = path.join(clineDir, 'data', 'tasks', 'private-task');
  const historyPath = path.join(taskDir, 'api_conversation_history.json');
  const statePath = path.join(root, '.threadforge', 'cline-state.json');
  try {
    await mkdir(taskDir, { recursive: true });
    await writeFile(historyPath, JSON.stringify([{ role: 'user', content: 'private Cline prompt' }]));
    let result = await invoke(['cline', 'sessions', 'status', '--cline-dir', clineDir,
      '--vscode-storage-dir', path.join(root, 'vscode-storage'), '--state-path', statePath]);
    assert.equal(result.code, 0, result.stderr);
    const status = JSON.parse(result.stdout) as { sessions: number; pending_sessions: number };
    assert.equal(status.sessions, 1);
    assert.equal(status.pending_sessions, 1);

    result = await invoke(['cline', 'sessions', 'sync', '--cline-dir', clineDir,
      '--vscode-storage-dir', path.join(root, 'vscode-storage'), '--state-path', statePath, '--print']);
    assert.equal(result.code, 0, result.stderr);
    const preview = JSON.parse(result.stdout) as { complete: boolean; events_previewed: number; events: unknown[] };
    assert.equal(preview.complete, true);
    assert.equal(preview.events_previewed, 2);
    assert.equal(JSON.stringify(preview.events).includes('private Cline prompt'), false);
    await assert.rejects(readFile(statePath), /ENOENT/);

    result = await invoke(['cline', 'sessions', 'sync', '--cline-dir', clineDir, '--max-sessions', '5001', '--print']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /between 1 and 5000/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Copilot transcript CLI exposes bounded status and non-mutating preview', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-history-cli-'));
  const copilotDir = path.join(root, '.copilot');
  const sessionDir = path.join(copilotDir, 'session-state', 'private-session');
  const statePath = path.join(root, 'threadforge', 'copilot-state.json');
  try {
    await mkdir(sessionDir, { recursive: true });
    await writeFile(path.join(sessionDir, 'events.jsonl'), `${JSON.stringify({
      id: 'private-record', timestamp: 1_777_000_000_000, type: 'user.message', data: { content: 'private prompt' },
    })}\n`, 'utf8');

    let result = await invoke(['copilot', 'sessions', 'status', '--copilot-dir', copilotDir, '--state-path', statePath]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      schema_version: 'threadforge.copilot-history-status.v1', sessions: 1, checkpointed_sessions: 0,
      pending_sessions: 1, symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
    });

    result = await invoke(['copilot', 'sessions', 'sync', '--copilot-dir', copilotDir, '--state-path', statePath, '--print']);
    assert.equal(result.code, 0, result.stderr);
    const preview = JSON.parse(result.stdout) as { complete: boolean; events_previewed: number; events: unknown[]; global_db_present: boolean };
    assert.equal(preview.complete, true);
    assert.equal(preview.events_previewed, 1);
    assert.equal(JSON.stringify(preview.events).includes('private prompt'), false);
    await assert.rejects(readFile(statePath), /ENOENT/);

    result = await invoke(['copilot', 'sessions', 'sync', '--copilot-dir', copilotDir, '--max-sessions', '5001', '--print']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /between 1 and 5000/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Cursor transcript CLI exposes bounded status and non-mutating preview', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cursor-history-cli-'));
  const projectsDir = path.join(root, 'projects');
  const transcriptDir = path.join(projectsDir, '-private-repo', 'agent-transcripts');
  const statePath = path.join(root, 'threadforge', 'cursor-state.json');
  try {
    await mkdir(transcriptDir, { recursive: true });
    await writeFile(path.join(transcriptDir, 'private-session.jsonl'), `${JSON.stringify({
      type: 'user_message', id: 'private-record', text: 'private prompt', timestamp: '2026-09-24T10:00:00Z',
    })}\n`, 'utf8');

    let result = await invoke(['cursor', 'sessions', 'status', '--projects-dir', projectsDir, '--state-path', statePath]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      schema_version: 'threadforge.cursor-history-status.v1', sessions: 1, checkpointed_sessions: 0,
      pending_sessions: 1, symlinks_skipped: 0, oversized_files_skipped: 0, files_truncated: false,
      global_db_present: false, composer_sessions: 0, composer_checkpointed_sessions: 0,
      composer_pending_sessions: 0, composer_conflicts: 0, composer_missing_sessions: 0, composers_truncated: false,
    });

    result = await invoke(['cursor', 'sessions', 'sync', '--projects-dir', projectsDir, '--state-path', statePath, '--print']);
    assert.equal(result.code, 0, result.stderr);
    const preview = JSON.parse(result.stdout) as { complete: boolean; events_previewed: number; events: unknown[]; global_db_present: boolean };
    assert.equal(preview.complete, true);
    assert.equal(preview.events_previewed, 1);
    assert.equal(JSON.stringify(preview.events).includes('private prompt'), false);
    assert.equal(preview.global_db_present, false);
    await assert.rejects(readFile(statePath), /ENOENT/);

    result = await invoke(['cursor', 'sessions', 'sync', '--projects-dir', projectsDir, '--global-db-path', path.join(root, 'missing.vscdb'), '--max-composers', '1', '--print']);
    assert.equal(result.code, 0, result.stderr);
    assert.equal((JSON.parse(result.stdout) as { global_db_present: boolean }).global_db_present, false);

    result = await invoke(['cursor', 'sessions', 'sync', '--projects-dir', projectsDir, '--max-files', '5001', '--print']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /between 1 and 5000/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Factory session CLI exposes bounded status and non-mutating preview', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-factory-history-cli-'));
  const sessionsDir = path.join(root, 'sessions');
  const projectDir = path.join(sessionsDir, '-private-project');
  const statePath = path.join(root, 'threadforge', 'factory-state.json');
  try {
    await mkdir(projectDir, { recursive: true });
    await writeFile(path.join(projectDir, 'private-session.jsonl'), `${JSON.stringify({
      type: 'session_start', id: 'private-record-id', sessionId: 'private-session', timestamp: '2026-09-25T10:00:00Z',
    })}\n`, 'utf8');

    let result = await invoke(['factory', 'sessions', 'status', '--sessions-dir', sessionsDir, '--state-path', statePath]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      schema_version: 'threadforge.factory-history-status.v1', sessions: 1, checkpointed_sessions: 0,
      pending_sessions: 1, symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
    });

    result = await invoke(['factory', 'sessions', 'sync', '--sessions-dir', sessionsDir, '--state-path', statePath, '--print']);
    assert.equal(result.code, 0, result.stderr);
    const preview = JSON.parse(result.stdout) as { complete: boolean; events_previewed: number; events: unknown[] };
    assert.equal(preview.complete, true);
    assert.equal(preview.events_previewed, 1);
    assert.equal(JSON.stringify(preview.events).includes('private-session'), false);
    assert.equal(JSON.stringify(preview.events).includes('private-record-id'), false);
    await assert.rejects(readFile(statePath), /ENOENT/);

    result = await invoke(['factory', 'sessions', 'sync', '--sessions-dir', sessionsDir, '--max-sessions', '5001', '--print']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /between 1 and 5000/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('fx session CLI exposes manifest status and non-mutating committed-log preview', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-fx-history-cli-'));
  const sessionsDir = path.join(root, 'sessions');
  const sessionDir = path.join(sessionsDir, '1788051418424-session-cli');
  const statePath = path.join(root, 'threadforge', 'fx-state.json');
  try {
    await mkdir(sessionDir, { recursive: true });
    const record = { schema_version: 1, log_generation: '3b4c72027563ff60190f0299461ee01c', seq: 1,
      event_id: 'private-event-id', timestamp_ms: 1_780_000_000_000, kind: 'session_started',
      payload: { id: 'private-session-id', created_at_ms: 1_780_000_000_000, workspace_root: 'C:/private/repo', origin_workspace_root: 'C:/private/repo' } };
    const log = `${JSON.stringify(record)}\n`;
    await writeFile(path.join(sessionDir, 'events.jsonl'), log, 'utf8');
    await writeFile(path.join(sessionDir, 'session.json'), JSON.stringify({
      schema_version: 3, storage_format: 'event_log_v1', id: '1788051418424-session-cli',
      log_generation: '3b4c72027563ff60190f0299461ee01c', last_event_seq: 1, event_log_bytes: Buffer.byteLength(log),
      updated_at_ms: 1_780_000_000_000,
    }), 'utf8');

    let result = await invoke(['fx', 'sessions', 'status', '--sessions-dir', sessionsDir, '--state-path', statePath]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      schema_version: 'threadforge.fx-history-status.v1', sessions: 1, checkpointed_sessions: 0,
      pending_sessions: 1, unreadable_manifests: 0, symlinks_skipped: 0, oversized_files_skipped: 0, sessions_truncated: false,
    });

    result = await invoke(['fx', 'sessions', 'sync', '--sessions-dir', sessionsDir, '--state-path', statePath, '--print']);
    assert.equal(result.code, 0, result.stderr);
    const preview = JSON.parse(result.stdout) as { complete: boolean; events_previewed: number; events: unknown[] };
    assert.equal(preview.complete, true);
    assert.equal(preview.events_previewed, 1);
    assert.equal(JSON.stringify(preview.events).includes('private-session-id'), false);
    assert.equal(JSON.stringify(preview.events).includes('C:/private/repo'), false);
    await assert.rejects(readFile(statePath), /ENOENT/);

    result = await invoke(['fx', 'sessions', 'sync', '--sessions-dir', sessionsDir, '--max-sessions', '5001', '--print']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /between 1 and 5000/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('adapter CLI passes flag-shaped MCP arguments through without shell expansion', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-mcp-cli-'));
  const otherRoot = await mkdtemp(path.join(os.tmpdir(), 'threadforge-mcp-cli-other-'));
  try {
    let result = await invoke([
      'mcp', 'install', '--host', 'cursor', '--project-root', root,
      '--command', 'threadforge', '--arg', 'mcp', '--arg', 'serve',
      '--arg', '--project-root', '--arg', otherRoot,
    ]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /must match the selected project root/);
    result = await invoke([
      'mcp', 'install', '--host', 'cursor', '--project-root', root,
      '--command', 'threadforge', '--arg', 'mcp', '--arg', 'serve',
      '--arg', '--project-root', '--arg', root,
    ]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const config = JSON.parse(await readFile(path.join(root, '.cursor', 'mcp.json'), 'utf8'));
    assert.deepEqual(config.mcpServers.threadforge.args, ['mcp', 'serve', '--project-root', root, '--threadforge-managed-mcp=v1']);
    result = await invoke([
      'mcp', 'update', '--host', 'cursor', '--project-root', root,
      '--command', 'threadforge.exe', '--arg', 'mcp', '--arg', 'serve',
      '--arg', '--project-root', '--arg', root,
    ]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'updated');
    result = await invoke(['mcp', 'uninstall', '--host', 'cursor', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(path.join(root, '.cursor', 'mcp.json'), 'utf8')), {});
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(otherRoot, { recursive: true, force: true });
  }
});

test('adapter CLI configures Gemini project MCP without granting trust', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-mcp-cli-'));
  try {
    let result = await invoke([
      'mcp', 'install', '--host', 'gemini-cli', '--project-root', root,
      '--command', 'threadforge', '--arg', 'mcp', '--arg', 'serve',
      '--arg', '--project-root', '--arg', root,
    ]);
    assert.equal(result.code, 0, result.stderr);
    const configPath = path.join(root, '.gemini', 'settings.json');
    const config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(config.mcpServers.threadforge.trust, false);
    assert.deepEqual(config.mcpServers.threadforge.args, ['mcp', 'serve', '--project-root', root, '--threadforge-managed-mcp=v1']);
    result = await invoke(['mcp', 'uninstall', '--host', 'gemini-cli', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), {});
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI configures GitHub Copilot project MCP in the shared workspace config', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-mcp-cli-'));
  try {
    const result = await invoke([
      'mcp', 'install', '--host', 'github-copilot', '--project-root', root,
      '--command', 'threadforge', '--arg', 'mcp', '--arg', 'serve',
      '--arg', '--project-root', '--arg', root,
    ]);
    assert.equal(result.code, 0, result.stderr);
    const config = JSON.parse(await readFile(path.join(root, '.mcp.json'), 'utf8'));
    assert.deepEqual(config.mcpServers.threadforge, {
      command: 'threadforge',
      args: ['mcp', 'serve', '--project-root', root, '--threadforge-managed-mcp=v1'],
    });
    const removed = await invoke(['mcp', 'uninstall', '--host', 'github-copilot', '--project-root', root]);
    assert.equal(removed.code, 0, removed.stderr);
    assert.deepEqual(JSON.parse(await readFile(path.join(root, '.mcp.json'), 'utf8')), {});
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('adapter CLI configures Qoder project MCP in the shared workspace config', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-qoder-mcp-cli-'));
  const configPath = path.join(root, '.mcp.json');
  try {
    await writeFile(configPath, JSON.stringify({ mcpServers: { analytics: { command: 'analytics', args: ['serve'] } } }));
    const result = await invoke([
      'mcp', 'install', '--host', 'qoder', '--project-root', root,
      '--command', 'threadforge', '--arg', 'mcp', '--arg', 'serve',
      '--arg', '--project-root', '--arg', root,
    ]);
    assert.equal(result.code, 0, result.stderr);
    const config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config.mcpServers.threadforge, {
      command: 'threadforge',
      args: ['mcp', 'serve', '--project-root', root, '--threadforge-managed-mcp=v1'],
    });
    const removed = await invoke(['mcp', 'uninstall', '--host', 'qoder', '--project-root', root]);
    assert.equal(removed.code, 0, removed.stderr);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), {
      mcpServers: { analytics: { command: 'analytics', args: ['serve'] } },
    });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI configures Kimi Code project MCP in its native project config', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-kimi-mcp-cli-'));
  const configPath = path.join(root, '.kimi-code', 'mcp.json');
  try {
    const result = await invoke([
      'mcp', 'install', '--host', 'kimi-code', '--project-root', root,
      '--command', 'threadforge', '--arg', 'mcp', '--arg', 'serve',
      '--arg', '--project-root', '--arg', root,
    ]);
    assert.equal(result.code, 0, result.stderr);
    const config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config.mcpServers.threadforge, {
      command: 'threadforge',
      args: ['mcp', 'serve', '--project-root', root, '--threadforge-managed-mcp=v1'],
    });
    const removed = await invoke(['mcp', 'uninstall', '--host', 'kimi-code', '--project-root', root]);
    assert.equal(removed.code, 0, removed.stderr);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), {});
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs and removes only the reviewed GitHub Copilot IDE prompt file', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-prompt-cli-'));
  try {
    const project = path.join(root, 'project');
    const skillFile = path.join(project, '.threadforge', 'skills', 'approved-procedure-a1b2c3d4', 'rev-000002', 'SKILL.md');
    const skillText = [
      '---',
      'name: "threadforge-approved-procedure-a1b2c3d4-r2"',
      'description: "Approved Threadforge procedure: review the change."',
      '---',
      '> Source procedure: approved-procedure-a1b2c3d4 revision 2',
      '',
      '> This portable procedure is approved guidance, not authorization to execute actions.',
      '',
      'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.',
      '',
      '## Steps',
      '1. Review current source state.',
      '',
    ].join('\n');
    await mkdir(path.dirname(skillFile), { recursive: true });
    await writeFile(skillFile, skillText, 'utf8');
    const sha256 = createHash('sha256').update(skillText).digest('hex');

    let result = await invoke(['copilot', 'prompts', 'install', '--project-root', project, '--skill-file', skillFile, '--sha256', sha256]);
    assert.equal(result.code, 0, result.stderr);
    assert.match(JSON.parse(result.stdout).warning, /manually invoked IDE customizations/);
    const promptPath = path.join(project, '.github', 'prompts', 'threadforge-approved-procedure-a1b2c3d4-r2.prompt.md');
    assert.match(await readFile(promptPath, 'utf8'), /Never infer approval to modify files/);

    result = await invoke(['copilot', 'prompts', 'uninstall', '--project-root', project, '--skill-file', skillFile, '--sha256', sha256]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result.action, 'removed');
    await assert.rejects(readFile(promptPath), /ENOENT/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('Oh My Pi CLI requires explicit scope and manages user or project extension files', async () => {
  const userAgent = await mkdtemp(path.join(os.tmpdir(), 'threadforge-omp-user-'));
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-omp-project-'));
  try {
    let result = await invoke(['omp', 'install', '--omp-agent-dir', userAgent]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /requires an explicit --scope user\|project choice/);

    result = await invoke(['omp', 'install', '--scope', 'user', '--omp-agent-dir', userAgent]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    result = await invoke(['omp', 'uninstall', '--scope', 'user', '--omp-agent-dir', userAgent]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');

    result = await invoke(['omp', 'install', '--scope', 'project', '--project-root', project]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    result = await invoke(['omp', 'uninstall', '--scope', 'project', '--project-root', project]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
  } finally {
    await rm(userAgent, { recursive: true, force: true });
    await rm(project, { recursive: true, force: true });
  }
});

test('OpenClaw CLI requires active-gateway scope and offers a non-mutating command preview', async () => {
  let result = await invoke(['openclaw', 'install']);
  assert.equal(result.code, 1);
  assert.match(result.stderr, /explicit --scope active-gateway/);

  result = await invoke(['openclaw', 'install', '--scope', 'active-gateway', '--print', '--acknowledge-local-source']);
  assert.equal(result.code, 0, result.stderr);
  const plan = JSON.parse(result.stdout) as { result: string; commands: Array<{ executable: string; args: string[] }> };
  assert.equal(plan.result, 'planned');
  assert.equal(plan.commands.length, 2);
  assert.ok(plan.commands[0]?.args.includes('--force'));
  assert.ok(plan.commands[0]?.args.some((arg) => arg.replace(/[\\/]+$/, '').endsWith(`adapters${path.sep}openclaw-plugin`)));
});

test('OpenClaw OTLP CLI prints local-safe guidance and checks only local log metadata', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-openclaw-otel-cli-'));
  const log = path.join(root, 'events.jsonl');
  try {
    let result = await invoke(['openclaw', 'otel', 'print-config']);
    assert.equal(result.code, 0, result.stderr);
    assert.match(result.stdout, /protocol: "http\/protobuf"/);
    assert.match(result.stdout, /captureContent: false/);
    assert.doesNotMatch(result.stdout, /plugins: \{\s*allow:/);

    const timestamp = new Date().toISOString();
    await writeFile(log, `${JSON.stringify({ timestamp, harness: { name: 'openclaw_gateway', collection_method: 'otlp' }, message: 'private' })}\n`, 'utf8');
    result = await invoke(['openclaw', 'otel', 'status', '--log-path', log, '--json']);
    assert.equal(result.code, 0, result.stderr);
    const status = JSON.parse(result.stdout) as { observed_events: number; last_event_observed: boolean };
    assert.equal(status.observed_events, 1);
    assert.equal(status.last_event_observed, true);
    assert.equal(result.stdout.includes('private'), false);

    result = await invoke(['openclaw', 'otel', 'validate', '--log-path', log, '--since', '10m', '--json']);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).valid, true);
    result = await invoke(['openclaw', 'otel', 'validate', '--log-path', path.join(root, 'missing.jsonl'), '--json']);
    assert.equal(result.code, 1);
    assert.equal(JSON.parse(result.stdout).valid, false);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('OpenClaw session-history status reads only the Threadforge cursor', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-openclaw-history-cli-'));
  try {
    const statePath = path.join(root, 'state', 'openclaw-history.json');
    const result = await invoke(['openclaw', 'sessions', 'status', '--state-path', statePath]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      schema_version: 'threadforge.openclaw-history-status.v1', tracked_sessions: 0, cursor_file_present: false,
    });
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('OpenClaw session-history CLI rejects unbounded message requests before invoking the host', async () => {
  const result = await invoke(['openclaw', 'sessions', 'sync', '--max-messages', '2001', '--print']);
  assert.equal(result.code, 1);
  assert.match(result.stderr, /--max-messages must be an integer between 1 and 2000/);
});

test('OpenCode session-history status reads the private Threadforge cursor without requiring the host', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-history-cli-'));
  try {
    const statePath = path.join(root, 'state', 'opencode-history.json');
    const result = await invoke(['opencode', 'sessions', 'status', '--state-path', statePath]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      schema_version: 'threadforge.opencode-history-status.v1', tracked_sessions: 0, cursor_file_present: false,
    });
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('OpenCode session-history CLI requires a bounded session count before invoking the host', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-history-limit-'));
  try {
    const result = await invoke(['opencode', 'sessions', 'sync', '--project-root', root, '--max-sessions', '201', '--print']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /--max-sessions must be an integer between 1 and 200/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('adapter CLI configures OpenCode project MCP using its local command-array schema', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-mcp-cli-'));
  try {
    let result = await invoke([
      'mcp', 'install', '--host', 'opencode', '--project-root', root,
      '--command', 'threadforge.exe', '--arg', 'mcp', '--arg', 'serve',
      '--arg', '--project-root', '--arg', root,
    ]);
    assert.equal(result.code, 0, result.stderr);
    const configPath = path.join(root, 'opencode.json');
    const config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config.mcp.threadforge, {
      type: 'local',
      command: ['threadforge.exe', 'mcp', 'serve', '--project-root', root, '--threadforge-managed-mcp=v1'],
      enabled: true,
    });
    result = await invoke(['mcp', 'uninstall', '--host', 'opencode', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), {});
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs and uninstalls the isolated OpenCode project plugin', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-cli-'));
  try {
    const moduleSpecifier = '@threadforge/adapter-contracts/opencode-plugin';
    let result = await invoke(['opencode', 'install', '--project-root', root, '--module', moduleSpecifier]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const pluginPath = path.join(root, '.opencode', 'plugins', 'threadforge.ts');
    const telemetryOnly = await readFile(pluginPath, 'utf8');
    assert.doesNotMatch(telemetryOnly, /memory:/);
    result = await invoke([
      'opencode', 'update', '--project-root', root, '--module', moduleSpecifier,
      '--memory-project-id', 'parser-project', '--memory-max-items', '4', '--memory-max-tokens', '900',
      '--memory-task-id', 'task-parser', '--memory-session-id', 'session-parser', '--memory-max-task-entries', '3',
    ]);
    assert.equal(result.code, 0, result.stderr);
    assert.match(result.stdout, /experimental messages-transform hook/);
    const configuredMemory = await readFile(pluginPath, 'utf8');
    assert.match(configuredMemory, /projectId":"parser-project"/);
    assert.match(configuredMemory, /maxItems":4/);
    assert.match(configuredMemory, /maxTokens":900/);
    assert.match(configuredMemory, /taskId":"task-parser"/);
    assert.match(configuredMemory, /sessionId":"session-parser"/);
    assert.match(configuredMemory, /maxTaskEntries":3/);
    result = await invoke([
      'opencode', 'update', '--project-root', root, '--module', moduleSpecifier,
      '--memory-project-id', 'parser-project', '--memory-discover-active-task', '--memory-max-task-entries', '2',
    ]);
    assert.equal(result.code, 0, result.stderr);
    const discoveredMemory = await readFile(pluginPath, 'utf8');
    assert.match(discoveredMemory, /discoverActiveTask":true/);
    assert.match(discoveredMemory, /maxTaskEntries":2/);
    result = await invoke([
      'opencode', 'update', '--project-root', root, '--module', moduleSpecifier,
      '--memory-project-id', 'parser-project', '--memory-task-id', 'task-parser', '--memory-discover-active-task',
    ]);
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /mutually exclusive/);
    result = await invoke(['opencode', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs and uninstalls only the OpenCode read-only planning agent', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-plan-agent-cli-'));
  try {
    let result = await invoke(['opencode', 'plan-agent', 'install', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(result.stdout, /static read-only planning profile/);
    const profilePath = path.join(root, '.opencode', 'agents', 'threadforge-plan.md');
    const profile = await readFile(profilePath, 'utf8');
    assert.match(profile, /"\*": deny/);
    assert.match(profile, /read: allow/);
    assert.doesNotMatch(profile, /\n  (?:bash|edit|write|patch|task): allow/);
    result = await invoke(['opencode', 'plan-agent', 'update', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['opencode', 'plan-agent', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('OpenCode plan-agent installation requires an explicit project root', async () => {
  const result = await invoke(['opencode', 'plan-agent', 'install']);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /--project-root is required/);
});

test('adapter CLI manages the Cursor observational hook config', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cursor-cli-'));
  try {
    const command = 'node cursor-hook.ts --threadforge-hook=cursor';
    let result = await invoke(['cursor', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    result = await invoke(['cursor', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['cursor', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI manages only the project Codex observational hooks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-cli-'));
  try {
    const command = 'node codex-hook.ts --threadforge-hook=codex';
    let result = await invoke(['codex', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const configPath = path.join(root, '.codex', 'hooks.json');
    const config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(Object.keys(config.hooks).sort(), ['PostToolUse', 'SessionStart']);
    assert.equal(JSON.stringify(config.hooks).includes('PreToolUse'), false);
    result = await invoke(['codex', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['codex', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI manages Codex approved-memory injection independently and preserves trust settings', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-memory-cli-'));
  try {
    const configPath = path.join(root, '.codex', 'hooks.json');
    await mkdir(path.dirname(configPath), { recursive: true });
    await writeFile(configPath, JSON.stringify({ features: { hooks: false }, trusted: false,
      hooks: { UserPromptSubmit: [{ hooks: [{ type: 'command', command: 'echo user-hook' }] }] } }));
    const telemetryCommand = 'node observer.js --threadforge-hook=codex';
    const memoryCommand = 'threadforge memory inject-codex --project-id demo-project --threadforge-managed-codex-memory=v1 --threadforge-memory=codex-v1';
    let result = await invoke(['codex', 'install', '--project-root', root, '--command', telemetryCommand]);
    assert.equal(result.code, 0, result.stderr);
    result = await invoke(['codex', 'memory', 'install', '--project-root', root, '--command', memoryCommand]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    let config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(config.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-hook');
    assert.equal(config.hooks.UserPromptSubmit[1].hooks[0].command, memoryCommand);
    assert.equal(config.hooks.UserPromptSubmit[1].hooks[0].timeout, 5);
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);

    result = await invoke(['codex', 'memory', 'update', '--project-root', root, '--command', memoryCommand]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke([
      'codex', 'memory', 'update', '--project-root', root, '--command', memoryCommand,
      '--task-id', 'codex-task', '--session-id', 'codex-session', '--max-task-entries', '4',
    ]);
    assert.equal(result.code, 0, result.stderr);
    const explicitTaskCommand = memoryCommand.replace(
      '--threadforge-memory=codex-v1',
      '--task-id codex-task --session-id codex-session --max-task-entries 4 --threadforge-memory=codex-v1',
    );
    config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(config.hooks.UserPromptSubmit[1].hooks[0].command, explicitTaskCommand);
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);

    result = await invoke([
      'codex', 'memory', 'update', '--project-root', root, '--command', memoryCommand, '--discover-active-task',
    ]);
    assert.equal(result.code, 0, result.stderr);
    const discoveryCommand = memoryCommand.replace(
      '--threadforge-memory=codex-v1',
      '--discover-active-task --max-task-entries 6 --threadforge-memory=codex-v1',
    );
    config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(config.hooks.UserPromptSubmit[1].hooks[0].command, discoveryCommand);

    const beforeInvalidOptions = await readFile(configPath, 'utf8');
    for (const [invalidOptions, expectedError] of [
      [['--task-id', 'codex-task', '--discover-active-task'], /mutually exclusive/],
      [['--session-id', 'codex-session'], /requires --task-id/],
      [['--task-id', 'Codex/Task'], /--task-id must match/],
      [['--discover-active-task', '--max-task-entries', '21'], /between 1 and 20/],
      [['--max-task-entries', '4'], /requires --task-id or --discover-active-task/],
    ] as const) {
      result = await invoke([
        'codex', 'memory', 'update', '--project-root', root, '--command', memoryCommand, ...invalidOptions,
      ]);
      assert.equal(result.code, 1);
      assert.match(result.stderr, expectedError);
    }
    assert.equal(await readFile(configPath, 'utf8'), beforeInvalidOptions);
    result = await invoke([
      'codex', 'memory', 'update', '--project-root', root,
      '--command', `${memoryCommand} --task-id codex-manual-task`, '--discover-active-task',
    ]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /already contains task-resume options/);
    assert.equal(await readFile(configPath, 'utf8'), beforeInvalidOptions);

    result = await invoke(['codex', 'memory', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(JSON.stringify(config).includes(memoryCommand), false);
    assert.equal(config.hooks.SessionStart[0].hooks[0].command, telemetryCommand);
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs only the explicit Codex deny-only task-plan gate without enabling hooks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-plan-gate-cli-'));
  try {
    const configPath = path.join(root, '.codex', 'hooks.json');
    await mkdir(path.dirname(configPath), { recursive: true });
    await writeFile(configPath, JSON.stringify({
      features: { hooks: false }, trusted: false,
      hooks: {
        PreToolUse: [{ matcher: 'Bash', hooks: [{ type: 'command', command: 'echo user-hook' }] }],
        UserPromptSubmit: [{ hooks: [{ type: 'command', command: 'echo user-prompt' }] }],
      },
    }));
    const command = 'threadforge task plan guard --host codex-cli --task-id task-a --project-id demo --project-root C:/workspace --threadforge-managed-codex-plan-gate=v1 --threadforge-plan-gate=codex-v1';
    let result = await invoke(['codex', 'plan-gate', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /deny-only.*fail open/);
    let config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config.hooks.PreToolUse[0], { matcher: 'Bash', hooks: [{ type: 'command', command: 'echo user-hook' }] });
    assert.deepEqual(config.hooks.PreToolUse[1], {
      matcher: '^(apply_patch|exec_command)$', hooks: [{ type: 'command', command, timeout: 5 }],
    });
    assert.equal(config.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-prompt');
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);

    result = await invoke(['codex', 'plan-gate', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['codex', 'plan-gate', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config.hooks.PreToolUse, [{ matcher: 'Bash', hooks: [{ type: 'command', command: 'echo user-hook' }] }]);
    assert.equal(config.hooks.UserPromptSubmit[0].hooks[0].command, 'echo user-prompt');
    assert.equal(config.features.hooks, false);
    assert.equal(config.trusted, false);

    result = await invoke(['codex', 'plan-gate', 'install', '--project-root', root, '--command',
      'threadforge task plan guard --threadforge-managed-codex-plan-gate=v1']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /runtime marker/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs Gemini project memory independently and preserves MCP, telemetry, and user hooks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-memory-cli-'));
  try {
    const settingsPath = path.join(root, '.gemini', 'settings.json');
    await mkdir(path.dirname(settingsPath), { recursive: true });
    const settings = {
      mcpServers: { threadforge: { command: 'threadforge', trust: false } },
      telemetry: { enabled: true, logPrompts: false },
      hooks: { BeforeAgent: [{ hooks: [{ name: 'user-hook', type: 'command', command: 'node user.js' }] }] },
    };
    await writeFile(settingsPath, JSON.stringify(settings));
    const command = 'threadforge memory inject-gemini --project-id demo-project --threadforge-managed-gemini-memory=v1 --threadforge-memory=gemini-v1';
    let result = await invoke(['gemini', 'memory', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /does not change trust or telemetry/);
    let next = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.deepEqual(next.mcpServers, settings.mcpServers);
    assert.deepEqual(next.telemetry, settings.telemetry);
    assert.equal(next.hooks.BeforeAgent[0].hooks[0].command, 'node user.js');
    assert.equal(next.hooks.BeforeAgent[1].hooks[0].command, command);
    assert.equal(next.hooks.BeforeAgent[1].hooks[0].timeout, 10000);

    result = await invoke(['gemini', 'memory', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    const explicitTaskResult = await invoke([
      'gemini', 'memory', 'update', '--project-root', root, '--command', command,
      '--task-id', 'gemini-task', '--session-id', 'gemini-session', '--max-task-entries', '3',
    ]);
    assert.equal(explicitTaskResult.code, 0, explicitTaskResult.stderr);
    const explicitTaskCommand = command.replace(
      '--threadforge-memory=gemini-v1',
      '--task-id gemini-task --session-id gemini-session --max-task-entries 3 --threadforge-memory=gemini-v1',
    );
    next = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.equal(next.hooks.BeforeAgent[1].hooks[0].command, explicitTaskCommand);

    result = await invoke([
      'gemini', 'memory', 'update', '--project-root', root, '--command', command, '--discover-active-task',
    ]);
    assert.equal(result.code, 0, result.stderr);
    const discoveryCommand = command.replace(
      '--threadforge-memory=gemini-v1',
      '--discover-active-task --max-task-entries 6 --threadforge-memory=gemini-v1',
    );
    next = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.equal(next.hooks.BeforeAgent[1].hooks[0].command, discoveryCommand);

    const quotedMarkerDecoy = `echo '--threadforge-memory=gemini-v1' && ${command}`;
    result = await invoke([
      'gemini', 'memory', 'update', '--project-root', root, '--command', quotedMarkerDecoy,
      '--task-id', 'gemini-task',
    ]);
    assert.equal(result.code, 0, result.stderr);
    const actualMarkerIndex = quotedMarkerDecoy.lastIndexOf('--threadforge-memory=gemini-v1');
    const expectedAfterQuotedDecoy = `${quotedMarkerDecoy.slice(0, actualMarkerIndex)}--task-id gemini-task --max-task-entries 6 ${quotedMarkerDecoy.slice(actualMarkerIndex)}`;
    next = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.equal(next.hooks.BeforeAgent[1].hooks[0].command, expectedAfterQuotedDecoy);

    const beforeInvalidOptions = await readFile(settingsPath, 'utf8');
    for (const [invalidOptions, expectedError] of [
      [['--task-id', 'gemini-task', '--discover-active-task'], /mutually exclusive/],
      [['--session-id', 'gemini-session'], /requires --task-id/],
      [['--task-id', 'Gemini/Task'], /--task-id must match/],
      [['--discover-active-task', '--max-task-entries', '21'], /between 1 and 20/],
      [['--max-task-entries', '3'], /requires --task-id or --discover-active-task/],
    ] as const) {
      result = await invoke([
        'gemini', 'memory', 'update', '--project-root', root, '--command', command, ...invalidOptions,
      ]);
      assert.equal(result.code, 1);
      assert.match(result.stderr, expectedError);
    }
    assert.equal(await readFile(settingsPath, 'utf8'), beforeInvalidOptions);
    result = await invoke([
      'gemini', 'memory', 'update', '--project-root', root,
      '--command', `${command} --task-id gemini-manual-task`, '--discover-active-task',
    ]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /already contains task-resume options/);
    assert.equal(await readFile(settingsPath, 'utf8'), beforeInvalidOptions);

    result = await invoke(['gemini', 'memory', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    next = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.deepEqual(next, settings);

    result = await invoke(['gemini', 'memory', 'install', '--command', command]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /--project-root/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs only the Gemini direct-file deny-only plan gate and preserves other project settings', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-plan-gate-cli-'));
  try {
    const settingsPath = path.join(root, '.gemini', 'settings.json');
    await mkdir(path.dirname(settingsPath), { recursive: true });
    const settings = {
      mcpServers: { threadforge: { command: 'threadforge', trust: false } },
      telemetry: { enabled: true, logPrompts: false },
      hooks: {
        BeforeAgent: [{ hooks: [{ name: 'user-prompt', type: 'command', command: 'node prompt.js' }] }],
        BeforeTool: [{ matcher: 'read_file', hooks: [{ name: 'user-reader', type: 'command', command: 'node reader.js' }] }],
      },
    };
    await writeFile(settingsPath, JSON.stringify(settings));
    const command = 'threadforge task plan guard --host gemini-cli --task-id task-a --project-id demo --project-root C:/workspace --threadforge-managed-gemini-plan-gate=v1 --threadforge-plan-gate=gemini-v1';
    let result = await invoke(['gemini', 'plan-gate', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const warning = JSON.parse(result.stdout).warning;
    assert.match(warning, /direct write_file\/replace/);
    assert.match(warning, /shell, MCP/);
    assert.match(warning, /does not change project trust/);
    let next = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.deepEqual(next.mcpServers, settings.mcpServers);
    assert.deepEqual(next.telemetry, settings.telemetry);
    assert.deepEqual(next.hooks.BeforeAgent, settings.hooks.BeforeAgent);
    assert.deepEqual(next.hooks.BeforeTool[0], settings.hooks.BeforeTool[0]);
    assert.deepEqual(next.hooks.BeforeTool[1], {
      matcher: '^(write_file|replace)$',
      hooks: [{ name: 'threadforge-task-plan-gate', type: 'command', command, timeout: 10000 }],
    });

    result = await invoke(['gemini', 'plan-gate', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['gemini', 'plan-gate', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    next = JSON.parse(await readFile(settingsPath, 'utf8'));
    assert.deepEqual(next, settings);

    result = await invoke(['gemini', 'plan-gate', 'install', '--command', command]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /--project-root/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI manages Cursor session-start memory without changing telemetry or prompt hooks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cursor-memory-cli-'));
  try {
    const hooksPath = path.join(root, '.cursor', 'hooks.json');
    await mkdir(path.dirname(hooksPath), { recursive: true });
    const original = {
      version: 1,
      hooks: {
        sessionStart: [{ command: 'user-start-hook' }],
        beforeSubmitPrompt: [{ command: 'user-prompt-hook' }],
      },
      userOption: { keep: true },
    };
    await writeFile(hooksPath, JSON.stringify(original));
    const telemetryCommand = 'node observer.js --threadforge-hook=cursor';
    const memoryCommand = 'threadforge memory inject-cursor --project-id demo-project --threadforge-managed-cursor-memory=v1 --threadforge-memory=cursor-v1';
    let activeMemoryCommand = memoryCommand;
    let result = await invoke(['cursor', 'install', '--project-root', root, '--command', telemetryCommand]);
    assert.equal(result.code, 0, result.stderr);
    result = await invoke(['cursor', 'memory', 'install', '--project-root', root, '--command', memoryCommand]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /not per-prompt retrieval/);
    let config = JSON.parse(await readFile(hooksPath, 'utf8'));
    assert.deepEqual(config.userOption, original.userOption);
    assert.deepEqual(config.hooks.beforeSubmitPrompt, original.hooks.beforeSubmitPrompt);
    assert.equal(config.hooks.sessionStart[0].command, 'user-start-hook');
    assert.equal(config.hooks.sessionStart.some((hook: { command: string }) => hook.command === telemetryCommand), true);
    const memoryHook = config.hooks.sessionStart.find((hook: { command: string }) => hook.command === memoryCommand);
    assert.ok(memoryHook);
    assert.equal(memoryHook.timeout, 10);

    result = await invoke(['cursor', 'memory', 'update', '--project-root', root, '--command', memoryCommand]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke([
      'cursor', 'memory', 'update', '--project-root', root, '--command', memoryCommand,
      '--task-id', 'cursor-task', '--session-id', 'cursor-session', '--max-task-entries', '4',
    ]);
    assert.equal(result.code, 0, result.stderr);
    activeMemoryCommand = memoryCommand.replace(
      '--threadforge-memory=cursor-v1',
      '--task-id cursor-task --session-id cursor-session --max-task-entries 4 --threadforge-memory=cursor-v1',
    );
    config = JSON.parse(await readFile(hooksPath, 'utf8'));
    assert.equal(config.hooks.sessionStart.some((hook: { command: string }) => hook.command === activeMemoryCommand), true);

    result = await invoke([
      'cursor', 'memory', 'update', '--project-root', root, '--command', memoryCommand, '--discover-active-task',
    ]);
    assert.equal(result.code, 0, result.stderr);
    activeMemoryCommand = memoryCommand.replace(
      '--threadforge-memory=cursor-v1',
      '--discover-active-task --max-task-entries 6 --threadforge-memory=cursor-v1',
    );
    config = JSON.parse(await readFile(hooksPath, 'utf8'));
    assert.equal(config.hooks.sessionStart.some((hook: { command: string }) => hook.command === activeMemoryCommand), true);

    const beforeInvalidOptions = await readFile(hooksPath, 'utf8');
    for (const [invalidOptions, expectedError] of [
      [['--task-id', 'cursor-task', '--discover-active-task'], /mutually exclusive/],
      [['--session-id', 'cursor-session'], /requires --task-id/],
      [['--task-id', 'Cursor/Task'], /--task-id must match/],
      [['--discover-active-task', '--max-task-entries', '21'], /between 1 and 20/],
      [['--max-task-entries', '4'], /requires --task-id or --discover-active-task/],
    ] as const) {
      result = await invoke([
        'cursor', 'memory', 'update', '--project-root', root, '--command', memoryCommand, ...invalidOptions,
      ]);
      assert.equal(result.code, 1);
      assert.match(result.stderr, expectedError);
    }
    assert.equal(await readFile(hooksPath, 'utf8'), beforeInvalidOptions);

    result = await invoke(['cursor', 'memory', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    config = JSON.parse(await readFile(hooksPath, 'utf8'));
    assert.equal(config.hooks.sessionStart.some((hook: { command: string }) => hook.command === activeMemoryCommand), false);
    assert.equal(config.hooks.sessionStart.some((hook: { command: string }) => hook.command === telemetryCommand), true);
    assert.deepEqual(config.hooks.beforeSubmitPrompt, original.hooks.beforeSubmitPrompt);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI installs and removes Cursor plan gate without replacing user hooks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cursor-plan-gate-cli-'));
  try {
    const hooksPath = path.join(root, '.cursor', 'hooks.json');
    await mkdir(path.dirname(hooksPath), { recursive: true });
    const original = {
      version: 1,
      hooks: {
        preToolUse: [{ command: 'user-shell-policy', matcher: 'Shell' }],
        sessionStart: [{ command: 'user-start-hook' }],
      },
      userOption: { keep: true },
    };
    await writeFile(hooksPath, JSON.stringify(original));
    const command = 'threadforge task plan guard --host cursor --task-id task-1 --project-id demo --project-root . --threadforge-managed-cursor-plan-gate=v1 --threadforge-plan-gate=cursor-v1';

    let result = await invoke(['cursor', 'plan-gate', 'install', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /direct Write tools only/);
    let config = JSON.parse(await readFile(hooksPath, 'utf8'));
    assert.deepEqual(config.userOption, original.userOption);
    assert.deepEqual(config.hooks.preToolUse[0], original.hooks.preToolUse[0]);
    assert.deepEqual(config.hooks.sessionStart, original.hooks.sessionStart);
    assert.deepEqual(config.hooks.preToolUse[1], {
      command, matcher: 'Write', timeout: 10, failClosed: true,
    });

    result = await invoke(['cursor', 'plan-gate', 'update', '--project-root', root, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['cursor', 'plan-gate', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    config = JSON.parse(await readFile(hooksPath, 'utf8'));
    assert.deepEqual(config, original);

    result = await invoke(['cursor', 'plan-gate', 'install', '--command', command]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /--project-root/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI manages Antigravity user and project hook scopes without enabling decisions', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-antigravity-user-cli-'));
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-antigravity-project-cli-'));
  const env = { HOME: home, USERPROFILE: home };
  const command = 'node antigravity-hook.ts --threadforge-hook=antigravity';
  try {
    let result = await invoke(['antigravity', 'install', '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const userConfig = path.join(home, '.gemini', 'config', 'hooks.json');
    let config = JSON.parse(await readFile(userConfig, 'utf8'));
    assert.equal(config['threadforge-endpoint'].PreInvocation[0].command, `${command} --hook-event=PreInvocation`);
    assert.equal(config['threadforge-endpoint'].PostToolUse[0].matcher, '*');
    assert.equal(config['threadforge-endpoint'].PostToolUse[0].hooks[0].command, `${command} --hook-event=PostToolUse`);
    assert.equal(config['threadforge-endpoint'].PreToolUse, undefined);
    assert.equal(config['threadforge-endpoint'].UserPromptSubmit, undefined);
    assert.equal(JSON.stringify(config).includes('decision'), false);

    result = await invoke(['antigravity', 'uninstall'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');

    result = await invoke(['antigravity', 'install', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const projectConfig = path.join(project, '.agents', 'hooks.json');
    config = JSON.parse(await readFile(projectConfig, 'utf8'));
    assert.equal(Object.keys(config['threadforge-endpoint']).sort().join(','), [
      'PostInvocation', 'PostToolUse', 'PreInvocation', 'Stop',
    ].sort().join(','));
    result = await invoke(['antigravity', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI installs project-only Antigravity approved-memory PreInvocation hooks', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-antigravity-memory-home-'));
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-antigravity-memory-project-'));
  const env = { HOME: home, USERPROFILE: home };
  const command = 'threadforge memory inject-antigravity --project-id demo-project --threadforge-managed-antigravity-memory=v1 --threadforge-memory=antigravity-v1';
  const file = path.join(project, '.agents', 'hooks.json');
  try {
    let result = await invoke(['antigravity', 'memory', 'install', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /requires explicit --scope project/);

    result = await invoke(['antigravity', 'memory', 'install', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /transient guidance once per execution/);
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config['threadforge-approved-memory'].PreInvocation, [{ type: 'command', command, timeout: 10 }]);
    assert.equal(JSON.stringify(config).includes('trust'), false);

    result = await invoke(['antigravity', 'memory', 'update', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke([
      'antigravity', 'memory', 'update', '--scope', 'project', '--project-root', project,
      '--command', command, '--task-id', 'antigravity-task', '--session-id', 'antigravity-session', '--max-task-entries', '5',
    ], env);
    assert.equal(result.code, 0, result.stderr);
    const explicitTaskCommand = command.replace(
      '--threadforge-memory=antigravity-v1',
      '--task-id antigravity-task --session-id antigravity-session --max-task-entries 5 --threadforge-memory=antigravity-v1',
    );
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config['threadforge-approved-memory'].PreInvocation, [{ type: 'command', command: explicitTaskCommand, timeout: 10 }]);

    result = await invoke([
      'antigravity', 'memory', 'update', '--scope', 'project', '--project-root', project,
      '--command', command, '--discover-active-task',
    ], env);
    assert.equal(result.code, 0, result.stderr);
    const discoveryCommand = command.replace(
      '--threadforge-memory=antigravity-v1',
      '--discover-active-task --max-task-entries 6 --threadforge-memory=antigravity-v1',
    );
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config['threadforge-approved-memory'].PreInvocation, [{ type: 'command', command: discoveryCommand, timeout: 10 }]);

    const beforeInvalidOptions = await readFile(file, 'utf8');
    for (const [invalidOptions, expectedError] of [
      [['--task-id', 'antigravity-task', '--discover-active-task'], /mutually exclusive/],
      [['--session-id', 'antigravity-session'], /requires --task-id/],
      [['--task-id', 'Antigravity/Task'], /--task-id must match/],
      [['--discover-active-task', '--max-task-entries', '21'], /between 1 and 20/],
      [['--max-task-entries', '5'], /requires --task-id or --discover-active-task/],
    ] as const) {
      result = await invoke([
        'antigravity', 'memory', 'update', '--scope', 'project', '--project-root', project,
        '--command', command, ...invalidOptions,
      ], env);
      assert.equal(result.code, 1);
      assert.match(result.stderr, expectedError);
    }
    assert.equal(await readFile(file, 'utf8'), beforeInvalidOptions);

    result = await invoke(['antigravity', 'memory', 'uninstall', '--project-root', project], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /requires explicit --scope project/);
    result = await invoke(['antigravity', 'memory', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config, {});
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI manages the project-only Antigravity deny/pass-through task-plan hook', async () => {
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-antigravity-plan-gate-cli-'));
  const env = { HOME: project, USERPROFILE: project };
  const file = path.join(project, '.agents', 'hooks.json');
  const command = `threadforge task plan guard --host antigravity-cli --task-id plan-task --project-id demo-project --project-root ${project} --threadforge-managed-antigravity-plan-gate=v1 --threadforge-plan-gate=antigravity-v1`;
  const original = {
    permissions: { ask: ['run_command'] },
    'threadforge-endpoint': { PostToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: 'observer' }] }] },
    'threadforge-approved-memory': { PreInvocation: [{ type: 'command', command: 'memory' }] },
    user: { PreToolUse: [{ matcher: 'run_command', hooks: [{ type: 'command', command: 'user-policy' }] }] },
  };
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify(original), 'utf8');
    let result = await invoke(['antigravity', 'plan-gate', 'install', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /requires explicit --scope project/);

    result = await invoke(['antigravity', 'plan-gate', 'install', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /allow only delegates/);
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.permissions, original.permissions);
    assert.deepEqual(config['threadforge-endpoint'], original['threadforge-endpoint']);
    assert.deepEqual(config['threadforge-approved-memory'], original['threadforge-approved-memory']);
    assert.deepEqual(config.user, original.user);
    assert.deepEqual(config['threadforge-task-plan-gate'], {
      enabled: true,
      PreToolUse: [{
        matcher: '^(write_to_file|replace_file_content|multi_replace_file_content)$',
        hooks: [{ type: 'command', command, timeout: 10 }],
      }],
    });

    result = await invoke(['antigravity', 'plan-gate', 'update', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['antigravity', 'plan-gate', 'uninstall', '--project-root', project], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /requires explicit --scope project/);
    result = await invoke(['antigravity', 'plan-gate', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), original);
  } finally {
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI requires project scope and manages Antigravity plan gates separately', async () => {
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-antigravity-plan-gate-cli-'));
  const env = { HOME: project, USERPROFILE: project };
  const file = path.join(project, '.agents', 'hooks.json');
  const command = `threadforge task plan guard --host antigravity-cli --task-id plan-task --project-id demo-project --project-root ${project} --threadforge-managed-antigravity-plan-gate=v1 --threadforge-plan-gate=antigravity-v1`;
  const original = {
    permissions: { ask: ['run_command'] },
    'threadforge-endpoint': { PostToolUse: [{ matcher: '*', hooks: [{ type: 'command', command: 'observer' }] }] },
    'threadforge-approved-memory': { PreInvocation: [{ type: 'command', command: 'memory' }] },
    user: { PreToolUse: [{ matcher: 'run_command', hooks: [{ type: 'command', command: 'user-policy' }] }] },
  };
  try {
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify(original), 'utf8');
    let result = await invoke(['antigravity', 'plan-gate', 'install', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /requires explicit --scope project/);

    result = await invoke(['antigravity', 'plan-gate', 'install', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /allow only delegates/);
    let config = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(config.permissions, original.permissions);
    assert.deepEqual(config['threadforge-endpoint'], original['threadforge-endpoint']);
    assert.deepEqual(config['threadforge-approved-memory'], original['threadforge-approved-memory']);
    assert.deepEqual(config.user, original.user);
    assert.deepEqual(config['threadforge-task-plan-gate'], {
      enabled: true,
      PreToolUse: [{
        matcher: '^(write_to_file|replace_file_content|multi_replace_file_content)$',
        hooks: [{ type: 'command', command, timeout: 10 }],
      }],
    });

    result = await invoke(['antigravity', 'plan-gate', 'update', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['antigravity', 'plan-gate', 'uninstall', '--project-root', project], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /requires explicit --scope project/);
    result = await invoke(['antigravity', 'plan-gate', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), original);
  } finally {
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI manages Devin Desktop Cascade hooks and requires the ownership marker for dispatch', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-devin-desktop-user-cli-'));
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-devin-desktop-project-cli-'));
  const env = { HOME: home, USERPROFILE: home, THREADFORGE_EVENT_ENDPOINT: 'http://127.0.0.1:4318/v1/events' };
  const marker = '--threadforge-devin-desktop-hook=v1';
  const template = `node hook.ts devin-desktop hook {event} ${marker}`;
  try {
    let result = await invoke(['devin-desktop', 'install', '--command-template', template], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /Restart Devin Desktop/);
    const userFile = path.join(home, '.codeium', 'windsurf', 'hooks.json');
    const userConfig = JSON.parse(await readFile(userFile, 'utf8'));
    assert.equal(userConfig.hooks.pre_user_prompt[0].show_output, false);

    result = await invoke(['devin-desktop', 'update', '--command-template', template], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['devin-desktop', 'install', '--scope', 'project', '--project-root', project, '--command-template', template], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.equal(JSON.parse(await readFile(path.join(project, '.windsurf', 'hooks.json'), 'utf8')).hooks.post_run_command[0].command,
      template.replace('{event}', 'post_run_command'));

    result = await invoke(['devin-desktop', 'hook', 'pre_user_prompt', marker], env, JSON.stringify({ agent_action_name: 'pre_user_prompt', user_prompt: 'private' }));
    assert.equal(result.code, 0, result.stderr);
    assert.equal(result.stdout, '');
    assert.doesNotMatch(result.stderr, /private/);
    result = await invoke(['devin-desktop', 'hook', 'pre_user_prompt'], env, '{}');
    assert.equal(result.code, 2);

    result = await invoke(['devin-desktop', 'uninstall'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    result = await invoke(['devin-desktop', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI installs, reports, updates, and removes the standalone Senpi extension by scope', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-senpi-user-cli-'));
  const agentDir = path.join(home, 'senpi-agent');
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-senpi-project-cli-'));
  const env = { HOME: home, USERPROFILE: home, OMO_CODING_AGENT_DIR: '', SENPI_CODING_AGENT_DIR: '', PI_CODING_AGENT_DIR: '' };
  try {
    let result = await invoke(['senpi', 'install', '--senpi-agent-dir', agentDir], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /Restart Senpi/);

    result = await invoke(['omo-senpi', 'status', '--senpi-agent-dir', agentDir], env);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout).result, {
      schema_version: 'threadforge.senpi-status.v1', extension: 'installed',
    });
    result = await invoke(['senpi', 'update', '--senpi-agent-dir', agentDir], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['senpi', 'install', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.match(JSON.parse(result.stdout).warning, /trusted projects; Threadforge did not change project trust/);
    assert.match(await readFile(path.join(project, '.senpi', 'extensions', 'threadforge.ts'), 'utf8'), /threadforge-managed-senpi-extension:v1/);

    result = await invoke(['senpi', 'status', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result.extension, 'installed');
    result = await invoke(['senpi', 'uninstall', '--senpi-agent-dir', agentDir], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.match(JSON.parse(result.stdout).warning, /Restart Senpi to unload/);
    result = await invoke(['senpi', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');

    result = await invoke(['senpi', 'install', '--scope', 'user', '--project-root', project], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /only valid with --scope project/);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI manages Qwen hooks and surfaces the trusted-project caveat', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-qwen-user-cli-'));
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-qwen-project-cli-'));
  const env = { HOME: home, USERPROFILE: home };
  const command = 'node qwen-hook.ts --threadforge-hook=qwen';
  try {
    let result = await invoke(['qwen', 'install', '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const userSettings = JSON.parse(await readFile(path.join(home, '.qwen', 'settings.json'), 'utf8'));
    assert.equal(userSettings.hooks.PermissionRequest[0].hooks[0].shell, 'bash');
    assert.equal(userSettings.hooks.PreToolUse[0].hooks[0].timeout, 10_000);
    result = await invoke(['qwen', 'update', '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['qwen', 'uninstall'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');

    result = await invoke(['qwen', 'install', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.match(JSON.parse(result.stdout).warning, /trusted folder/);
    const projectSettings = JSON.parse(await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8'));
    assert.equal(Object.hasOwn(projectSettings, 'trustedFolders'), false);
    const baseMemoryCommand = 'threadforge memory inject-qwen --project-id demo-project --threadforge-managed-qwen-memory=v1 --threadforge-memory=qwen-v1';
    let memoryCommand = baseMemoryCommand;
    result = await invoke(['qwen', 'memory', 'install', '--scope', 'project', '--project-root', project, '--command', memoryCommand], env);
    assert.equal(result.code, 0, result.stderr);
    assert.match(JSON.parse(result.stdout).warning, /trusted-folder approval/);
    let installed = JSON.parse(await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8'));
    const memoryRef = installed.hooks.UserPromptExpansion.flatMap((group: { hooks: Array<Record<string, unknown>> }) => group.hooks)
      .find((hook: Record<string, unknown>) => hook.command === memoryCommand);
    assert.ok(memoryRef);
    assert.equal(memoryRef.timeout, 30_000);
    result = await invoke(['qwen', 'memory', 'update', '--scope', 'project', '--project-root', project, '--command', memoryCommand], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke([
      'qwen', 'memory', 'update', '--scope', 'project', '--project-root', project, '--command', baseMemoryCommand,
      '--task-id', 'qwen-parser-task', '--session-id', 'qwen-parser-session', '--max-task-entries', '3',
    ], env);
    assert.equal(result.code, 0, result.stderr);
    memoryCommand = baseMemoryCommand.replace(
      '--threadforge-memory=qwen-v1',
      '--task-id qwen-parser-task --session-id qwen-parser-session --max-task-entries 3 --threadforge-memory=qwen-v1',
    );
    installed = JSON.parse(await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8'));
    const explicitTaskRef = installed.hooks.UserPromptExpansion.flatMap((group: { hooks: Array<Record<string, unknown>> }) => group.hooks)
      .find((hook: Record<string, unknown>) => hook.command === memoryCommand);
    assert.ok(explicitTaskRef);
    result = await invoke([
      'qwen', 'memory', 'update', '--scope', 'project', '--project-root', project, '--command', baseMemoryCommand,
      '--discover-active-task',
    ], env);
    assert.equal(result.code, 0, result.stderr);
    memoryCommand = baseMemoryCommand.replace(
      '--threadforge-memory=qwen-v1',
      '--discover-active-task --max-task-entries 6 --threadforge-memory=qwen-v1',
    );
    installed = JSON.parse(await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8'));
    const discoveryRef = installed.hooks.UserPromptExpansion.flatMap((group: { hooks: Array<Record<string, unknown>> }) => group.hooks)
      .find((hook: Record<string, unknown>) => hook.command === memoryCommand);
    assert.ok(discoveryRef);
    const beforeInvalidMemoryOptions = await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8');
    for (const [invalidOptions, expectedError] of [
      [['--task-id', 'qwen-parser-task', '--discover-active-task'], /mutually exclusive/],
      [['--session-id', 'qwen-parser-session'], /requires --task-id/],
      [['--task-id', 'Qwen/Task'], /--task-id must match/],
      [['--discover-active-task', '--max-task-entries', '21'], /between 1 and 20/],
      [['--max-task-entries', '3'], /requires --task-id or --discover-active-task/],
    ] as const) {
      result = await invoke([
        'qwen', 'memory', 'update', '--scope', 'project', '--project-root', project, '--command', baseMemoryCommand,
        ...invalidOptions,
      ], env);
      assert.equal(result.code, 1);
      assert.match(result.stderr, expectedError);
    }
    assert.equal(await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8'), beforeInvalidMemoryOptions);
    result = await invoke([
      'qwen', 'memory', 'update', '--scope', 'project', '--project-root', project,
      '--command', `${baseMemoryCommand} --task-id qwen-manual-task`, '--discover-active-task',
    ], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /already contains task-resume options/);
    assert.equal(await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8'), beforeInvalidMemoryOptions);
    result = await invoke(['qwen', 'memory', 'install', '--project-root', project, '--command', memoryCommand], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /explicit --scope project/);

    result = await invoke(['qwen', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    installed = JSON.parse(await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8'));
    assert.equal(installed.hooks.UserPromptExpansion.some((group: { hooks: Array<{ command: string }> }) =>
      group.hooks.some((hook) => hook.command === memoryCommand)), true);
    result = await invoke(['qwen', 'memory', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(path.join(project, '.qwen', 'settings.json'), 'utf8')), {});
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI installs and removes only user-level Kimi Code hook tables', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-kimi-code-cli-'));
  const env = { KIMI_CODE_HOME: home };
  const command = 'node kimi-code-hook.ts --threadforge-hook=kimi';
  const configPath = path.join(home, 'config.toml');
  try {
    let result = await invoke(['kimi', 'install', '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const installed = await readFile(configPath, 'utf8');
    assert.match(installed, /\[\[hooks\]\]/);
    assert.match(installed, /event = "PermissionResult"/);
    result = await invoke(['kimi', 'update', '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['kimi', 'install', '--project-root', home, '--command', command], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /unsupported adapter option --project-root/);
    result = await invoke(['kimi', 'uninstall'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.equal(await readFile(configPath, 'utf8'), '');
  } finally { await rm(home, { recursive: true, force: true }); }
});

test('adapter CLI manages Kiro user and project hook files independently', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-kiro-cli-home-'));
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-kiro-cli-project-'));
  const env = { KIRO_HOME: home };
  const command = 'node kiro-hook.ts --threadforge-hook=kiro';
  const userConfig = path.join(home, 'hooks', 'threadforge-observational.json');
  const projectConfig = path.join(project, '.kiro', 'hooks', 'threadforge-observational.json');
  try {
    let result = await invoke(['kiro', 'install', '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.equal(JSON.parse(await readFile(userConfig, 'utf8')).hooks.length, 6);
    result = await invoke(['kiro', 'update', '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['kiro', 'install', '--scope', 'project', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(await readFile(projectConfig, 'utf8')).hooks.length, 6);
    result = await invoke(['kiro', 'uninstall', '--scope', 'project', '--project-root', project], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    result = await invoke(['kiro', 'uninstall'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    result = await invoke(['kiro', 'install', '--project-root', project, '--command', command], env);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /only valid with --scope project/);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI requires an explicit Muse scope and manages project/user settings separately', async () => {
  const xdg = await mkdtemp(path.join(os.tmpdir(), 'threadforge-muse-cli-xdg-'));
  const project = await mkdtemp(path.join(os.tmpdir(), 'threadforge-muse-cli-project-'));
  const command = 'node muse-hook.ts --threadforge-hook=muse';
  const userConfig = path.join(xdg, 'muse', 'settings.json');
  const projectConfig = path.join(project, '.muse', 'hooks.json');
  try {
    let result = await invoke(['muse', 'install', '--command', command], { XDG_CONFIG_HOME: xdg });
    assert.equal(result.code, 1);
    assert.match(result.stderr, /explicit --scope/);
    result = await invoke(['muse', 'install', '--scope', 'user', '--command', command], { XDG_CONFIG_HOME: xdg });
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    assert.equal(JSON.parse(await readFile(userConfig, 'utf8')).schema_version, 1);
    result = await invoke(['muse', 'update', '--scope', 'user', '--command', command], { XDG_CONFIG_HOME: xdg });
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['muse', 'install', '--scope', 'project', '--project-root', project, '--command', command], { XDG_CONFIG_HOME: xdg });
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(await readFile(projectConfig, 'utf8')).hooks.Stop.length, 1);
    result = await invoke(['muse', 'uninstall', '--scope', 'project', '--project-root', project], { XDG_CONFIG_HOME: xdg });
    assert.equal(result.code, 0, result.stderr);
    result = await invoke(['muse', 'uninstall', '--scope', 'user'], { XDG_CONFIG_HOME: xdg });
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
  } finally {
    await rm(xdg, { recursive: true, force: true });
    await rm(project, { recursive: true, force: true });
  }
});

test('adapter CLI manages Gemini telemetry in the selected user home', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-gemini-cli-'));
  const env = { HOME: home, USERPROFILE: home };
  try {
    let result = await invoke(['gemini', 'install'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const configPath = path.join(home, '.gemini', 'settings.json');
    assert.equal(JSON.parse(await readFile(configPath, 'utf8')).telemetry.otlpProtocol, 'http');
    result = await invoke(['gemini', 'update'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['gemini', 'uninstall'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), {});
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('adapter CLI installs and removes opt-in Claude Code telemetry in user settings', async () => {
  const home = await mkdtemp(path.join(os.tmpdir(), 'threadforge-claude-otel-cli-'));
  const env = { HOME: home, USERPROFILE: home };
  try {
    let result = await invoke(['telemetry', 'install', '--host', 'claude-code'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const configPath = path.join(home, '.claude', 'settings.json');
    const installed = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(installed.env.OTEL_EXPORTER_OTLP_ENDPOINT, 'http://127.0.0.1:4318');
    assert.equal(installed.env.OTEL_LOG_RAW_API_BODIES, '0');
    result = await invoke(['telemetry', 'update', '--host', 'claude-code'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['telemetry', 'uninstall', '--host', 'claude-code'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), {});
  } finally {
    await rm(home, { recursive: true, force: true });
  }
});

test('adapter CLI installs and removes opt-in Codex CLI telemetry under CODEX_HOME', async () => {
  const parent = await mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-otel-cli-'));
  const codexHome = path.join(parent, '.codex');
  const env = { CODEX_HOME: codexHome };
  try {
    let result = await invoke(['telemetry', 'install', '--host', 'codex-cli'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const configPath = path.join(codexHome, 'config.toml');
    const installed = await readFile(configPath, 'utf8');
    assert.match(installed, /log_user_prompt = false/);
    assert.match(installed, /127\.0\.0\.1:4318\/v1\/logs/);
    assert.match(installed, /127\.0\.0\.1:4318\/v1\/traces/);

    result = await invoke(['telemetry', 'update', '--host', 'codex-cli'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');
    result = await invoke(['telemetry', 'uninstall', '--host', 'codex-cli'], env);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    await assert.rejects(readFile(configPath), /ENOENT/);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('adapter CLI previews Codex rollout backfill without a project root or cursor mutation', async () => {
  const parent = await mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-rollout-cli-'));
  const codexHome = path.join(parent, '.codex');
  const rolloutPath = path.join(codexHome, 'sessions', '2026', '09', '25', 'rollout-session.jsonl');
  const statePath = path.join(parent, 'threadforge', 'state', 'codex-rollouts.v1.json');
  try {
    await mkdir(path.dirname(rolloutPath), { recursive: true });
    const lines = [
      { type: 'session_meta', timestamp: '2026-09-25T10:00:00Z', payload: { session_id: 'local-session' } },
      { type: 'response_item', timestamp: '2026-09-25T10:00:01Z', payload: { type: 'message', role: 'user', content: 'private prompt text' } },
    ];
    await writeFile(rolloutPath, `${lines.map((line) => JSON.stringify(line)).join('\n')}\n`, 'utf8');
    const result = await invoke(['codex', 'sync', '--codex-home', codexHome, '--state-path', statePath, '--print']);
    assert.equal(result.code, 0, result.stderr);
    const output = JSON.parse(result.stdout) as { schema_version: string; events: Array<Record<string, unknown>>; events_previewed: number };
    assert.equal(output.schema_version, 'threadforge.codex-rollout-sync.v1');
    assert.equal(output.events_previewed, 2);
    assert.equal(output.events.length, 2);
    assert.ok(!result.stdout.includes('private prompt text'));
    await assert.rejects(readFile(statePath), /ENOENT/);

    const status = await invoke(['codex', 'status', '--codex-home', codexHome, '--state-path', statePath]);
    assert.equal(status.code, 0, status.stderr);
    assert.equal(JSON.parse(status.stdout).schema_version, 'threadforge.codex-rollout-status.v1');
    assert.equal(JSON.parse(status.stdout).pending_sessions, 1);

    const conflict = await invoke(['codex', 'sync', '--codex-home', codexHome, '--state-path', statePath, '--print', '--include-content']);
    assert.equal(conflict.code, 1);
    assert.match(conflict.stderr, /cannot be combined/);
    await assert.rejects(readFile(statePath), /ENOENT/);
  } finally {
    await rm(parent, { recursive: true, force: true });
  }
});

test('adapter CLI manages the project Codex MCP table without changing trust settings', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-mcp-cli-'));
  const configPath = path.join(root, '.codex', 'config.toml');
  const original = 'model = "keep"\n[mcp_servers.analytics]\ncommand = "analytics"\nargs = ["serve"]\n';
  try {
    await mkdir(path.dirname(configPath), { recursive: true });
    await writeFile(configPath, original, 'utf8');
    const base = ['mcp', 'install', '--host', 'codex-cli', '--project-root', root, '--command', 'threadforge', '--arg', 'mcp', '--arg', 'serve', '--arg', '--project-root', '--arg', root];
    let result = await invoke(base);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const installed = await readFile(configPath, 'utf8');
    assert.match(installed, /\[mcp_servers\.threadforge\]/);
    assert.match(installed, /\[mcp_servers\.analytics\]/);
    assert.match(installed, /--threadforge-managed-mcp=v1/);
    assert.equal(await invoke(base).then((value) => JSON.parse(value.stdout).result), 'unchanged');

    for (const host of ['chatgpt-desktop-codex', 'codex-ide']) {
      const aliasInstall = await invoke(['mcp', 'install', '--host', host, ...base.slice(4)]);
      assert.equal(aliasInstall.code, 0, aliasInstall.stderr);
      assert.equal(JSON.parse(aliasInstall.stdout).result, 'unchanged');
    }

    result = await invoke(['mcp', 'update', '--host', 'chatgpt-desktop-codex', '--project-root', root, '--command', 'threadforge.exe', '--arg', 'mcp', '--arg', 'serve', '--arg', '--project-root', '--arg', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'updated');
    result = await invoke(['mcp', 'uninstall', '--host', 'codex-ide', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.equal(await readFile(configPath, 'utf8'), original);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('adapter CLI previews Pi local history without writing a cursor', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-pi-cli-'));
  const sessionsDir = path.join(root, 'pi-agent', 'sessions');
  const statePath = path.join(root, 'threadforge', 'pi-state.json');
  const transcript = path.join(sessionsDir, 'nested', 'session.jsonl');
  try {
    await mkdir(path.dirname(transcript), { recursive: true });
    await writeFile(transcript, [
      JSON.stringify({ type: 'session', id: 'private-session', timestamp: '2026-09-24T10:00:00Z' }),
      JSON.stringify({ type: 'message', message: { role: 'user', content: 'private prompt' } }),
      '',
    ].join('\n'), 'utf8');
    const preview = await invoke(['pi', 'sessions', 'sync', '--sessions-dir', sessionsDir, '--state-path', statePath, '--print']);
    assert.equal(preview.code, 0, preview.stderr);
    const output = JSON.parse(preview.stdout) as { schema_version: string; events_previewed: number; complete: boolean };
    assert.equal(output.schema_version, 'threadforge.pi-history-sync.v1');
    assert.equal(output.events_previewed, 2);
    assert.equal(output.complete, true);
    assert.equal(preview.stdout.includes('private prompt'), false);
    await assert.rejects(readFile(statePath), /ENOENT/);

    const status = await invoke(['pi', 'sessions', 'status', '--sessions-dir', sessionsDir, '--state-path', statePath]);
    assert.equal(status.code, 0, status.stderr);
    assert.equal(JSON.parse(status.stdout).pending_files, 1);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('adapter CLI previews Prime Agent session and artifact history through the alias', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-prime-cli-'));
  const sessionsDir = path.join(root, 'prime-agent', 'sessions');
  const artifactsDir = path.join(root, 'prime-agent', 'session-artifacts');
  const statePath = path.join(root, 'threadforge', 'prime-state.json');
  try {
    await mkdir(sessionsDir, { recursive: true });
    await mkdir(artifactsDir, { recursive: true });
    await writeFile(path.join(sessionsDir, 'session.jsonl'), `${JSON.stringify({ type: 'session', id: 'private-session' })}\n`, 'utf8');
    await writeFile(path.join(artifactsDir, 'artifact.jsonl'), `${JSON.stringify({ type: 'session', id: 'private-artifact' })}\n`, 'utf8');
    const preview = await invoke(['prime-agent', 'sessions', 'sync', '--sessions-dir', sessionsDir, '--artifacts-dir', artifactsDir,
      '--state-path', statePath, '--max-files', '50', '--print']);
    assert.equal(preview.code, 0, preview.stderr);
    const output = JSON.parse(preview.stdout) as { schema_version: string; session_files: number; artifact_files: number; events_previewed: number; complete: boolean };
    assert.equal(output.schema_version, 'threadforge.prime-history-sync.v1');
    assert.equal(output.session_files, 1);
    assert.equal(output.artifact_files, 1);
    assert.equal(output.events_previewed, 2);
    assert.equal(output.complete, true);
    assert.equal(preview.stdout.includes('private-session'), false);
    await assert.rejects(readFile(statePath), /ENOENT/);
    const status = await invoke(['prime', 'sessions', 'status', '--sessions-dir', sessionsDir, '--artifacts-dir', artifactsDir, '--state-path', statePath]);
    assert.equal(status.code, 0, status.stderr);
    assert.equal(JSON.parse(status.stdout).pending_files, 2);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('adapter CLI installs async OpenHands hooks and handles telemetry fail-open without host output', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-openhands-cli-'));
  const configPath = path.join(root, '.openhands', 'hooks.json');
  try {
    let result = await invoke(['open-hands', 'install', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const hooks = JSON.parse(await readFile(configPath, 'utf8')) as Record<string, Array<{ hooks: Array<Record<string, unknown>> }>>;
    assert.equal(Object.keys(hooks).length, 6);
    assert.equal(Object.values(hooks).every((groups) => groups.every((group) => group.hooks.every((hook) => hook.async === true))), true);
    assert.equal(Object.values(hooks).flatMap((groups) => groups.flatMap((group) => group.hooks))
      .every((hook) => typeof hook.command === 'string' && hook.command.includes('--threadforge-openhands-hook=v1')), true);

    result = await invoke(['openhands', 'update', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['openhands', 'hook', 'pre_tool_use', '--threadforge-openhands-hook=v1'],
      { THREADFORGE_EVENT_ENDPOINT: 'http://127.0.0.1:1/v1/events' },
      JSON.stringify({ session_id: 'private-session', tool_name: 'terminal', tool_input: { command: 'private command' } }));
    assert.equal(result.code, 0, result.stderr);
    assert.equal(result.stdout, '');

    result = await invoke(['openhands', 'uninstall', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    await assert.rejects(readFile(configPath), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('adapter CLI installs OpenHands MCP globally without project binding or auto-approval', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-openhands-mcp-cli-'));
  const configPath = path.join(root, '.openhands', 'mcp.json');
  try {
    await mkdir(path.dirname(configPath), { recursive: true });
    const original = { mcpServers: { analytics: { command: 'analytics', args: ['serve'] } }, otherSetting: true };
    await writeFile(configPath, JSON.stringify(original));
    const command = path.join(root, 'bin', 'threadforge.exe');
    let result = await invoke(['openhands', 'mcp', 'install', '--config-path', configPath, '--command', command]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    let config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config.mcpServers.threadforge, {
      command, args: ['mcp', 'serve', '--threadforge-managed-mcp=v1'],
    });
    assert.deepEqual(config.mcpServers.analytics, original.mcpServers.analytics);
    assert.equal(Object.hasOwn(config.mcpServers.threadforge, 'autoApprove'), false);

    result = await invoke(['openhands', 'mcp', 'status', '--config-path', configPath]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');

    result = await invoke(['openhands', 'mcp', 'update', '--config-path', configPath,
      '--expected-command', command, '--command', `${command}-next`]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'updated');

    result = await invoke(['openhands', 'mcp', 'uninstall', '--config-path', configPath, '--expected-command', `${command}-next`]);
    assert.equal(result.code, 0, result.stderr);
    config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config, original);

    const projectScoped = await invoke(['openhands', 'mcp', 'install', '--project-root', root, '--command', command]);
    assert.notEqual(projectScoped.code, 0);
    assert.match(projectScoped.stderr, /unsupported adapter option/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Hermes CLI prints a manual hook fragment and invokes only marked telemetry hooks', async () => {
  const config = await invoke(['hermes', 'hooks', 'print-config']);
  assert.equal(config.code, 0, config.stderr);
  assert.match(config.stdout, /^hooks:\n/m);
  assert.match(config.stdout, /on_session_finalize:/);
  assert.equal(config.stdout.includes('hooks_auto_accept'), false);

  const custom = await invoke(['hermes', 'hooks', 'print-config', '--command-template', `app {event} --threadforge-hermes-hook=v1`]);
  assert.equal(custom.code, 0, custom.stderr);
  assert.match(custom.stdout, /app on_session_start/);

  const invalid = await invoke(['hermes', 'hook', 'on_session_start', '--wrong-marker'], {}, '{}');
  assert.equal(invalid.code, 2);
  assert.match(invalid.stderr, /invalid or unowned/);

  const result = await invoke(['hermes', 'hook', 'on_session_start', '--threadforge-hermes-hook=v1'],
    { THREADFORGE_EVENT_ENDPOINT: 'http://127.0.0.1:1/v1/events' }, JSON.stringify({ session_id: 'private-session' }));
  assert.equal(result.code, 0, result.stderr);
  assert.equal(result.stdout, '');
});

test('Hermes session-history status is available without a running Hermes host', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-hermes-history-cli-'));
  try {
    const result = await invoke([
      'hermes', 'sessions', 'status', '--db-path', path.join(root, 'missing.db'),
      '--state-path', path.join(root, 'threadforge', 'state.json'),
    ]);
    assert.equal(result.code, 0, result.stderr);
    assert.deepEqual(JSON.parse(result.stdout), {
      schema_version: 'threadforge.hermes-history-status.v1', database_present: false,
      sessions: 0, sessions_truncated: false, tracked_sessions: 0,
      cursor_file_present: false, errors: [],
    });
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('DeepSeek Harness CLI accepts only marked events and preserves the empty no-opinion response', async () => {
  const invalid = await invoke(['deepseek-harness', 'hook', 'PermissionRequest', '--wrong-marker'], {}, '{}');
  assert.equal(invalid.code, 2);
  assert.match(invalid.stderr, /invalid or unowned/);

  const result = await invoke(['dsh', 'hook', 'PreToolUse', '--threadforge-dsh-hook=v1'],
    { THREADFORGE_EVENT_ENDPOINT: 'http://127.0.0.1:1/v1/events' },
    JSON.stringify({
      hook_event_name: 'PreToolUse', session_id: 'private-session', tool_name: 'bash',
      tool_input: { command: 'private command' },
    }));
  assert.equal(result.code, 0, result.stderr);
  assert.equal(result.stdout, '{}\n');
  assert.equal(result.stdout.includes('allow'), false);
  assert.equal(result.stderr.includes('private command'), false);
});

test('DeepSeek Harness MCP CLI prints a manual project Cordis patch without changing files or permissions', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-dsh-mcp-'));
  const args = ['--project-root', root, '--command', 'threadforge.exe',
    '--arg', 'mcp', '--arg', 'serve', '--arg', '--project-root', '--arg', root];
  try {
    const result = await invoke(['dsh', 'mcp', 'print-patch', ...args]);
    assert.equal(result.code, 0, result.stderr);
    assert.match(result.stdout, /^- insert:\n/);
    assert.match(result.stdout, /name: '@deepseek-ai\/dsh-mcp-client'/);
    assert.match(result.stdout, /serverName: threadforge\n        transport: stdio/);
    assert.match(result.stdout, /--threadforge-managed-mcp=v1/);
    assert.ok(result.stdout.includes(root));
    assert.doesNotMatch(result.stdout, /\benv:|\btrust:|autoApprove|permissionOverride/i);
    await assert.rejects(readFile(path.join(root, '.dsh', 'threadforge-mcp.patch.yml')));

    const rejected = await invoke(['deepseek-harness', 'mcp', 'print-patch', ...args.map((value, index) =>
      index === args.indexOf('threadforge.exe') ? 'threadforge token=fixture-secret' : value)]);
    assert.equal(rejected.code, 1);
    assert.match(rejected.stderr, /credential-free/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('DeepSeek session CLI exposes read-only status and bounded sync commands', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-dsh-cli-'));
  const stateDirectory = path.join(path.dirname(root), `${path.basename(root)}-threadforge-state`);
  try {
    const statePath = path.join(stateDirectory, 'cursor.json');
    const status = await invoke(['dsh', 'sessions', 'status', '--dsh-home', root, '--state-path', statePath]);
    assert.equal(status.code, 0, status.stderr);
    assert.equal(JSON.parse(status.stdout).schema_version, 'threadforge.dsh-history-status.v1');
    assert.equal(JSON.parse(status.stdout).sessions, 0);
    const preview = await invoke(['deepseek-harness', 'sessions', 'sync', '--dsh-home', root, '--state-path', statePath, '--print']);
    assert.equal(preview.code, 0, preview.stderr);
    assert.equal(JSON.parse(preview.stdout).schema_version, 'threadforge.dsh-history-sync.v1');
    assert.equal(JSON.parse(preview.stdout).complete, true);
    await assert.rejects(readFile(statePath));
    const invalidWatch = await invoke(['dsh', 'sessions', 'sync', '--dsh-home', root, '--state-path', statePath, '--watch', '--interval', '2s']);
    assert.equal(invalidWatch.code, 1);
    assert.match(invalidWatch.stderr, /between 5s and 168h/);
    const conflictingRoots = await invoke(['dsh', 'sessions', 'status', '--dsh-home', root, '--sessions-dir', root]);
    assert.equal(conflictingRoots.code, 1);
    assert.match(conflictingRoots.stderr, /either --dsh-home or --sessions-dir/);
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(stateDirectory, { recursive: true, force: true });
  }
});

test('Grok Build CLI installs project hooks without changing trust and runs fail-open observation', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-cli-'));
  const template = 'threadforge adapter grok hook {event} --threadforge-grok-hook=v1';
  try {
    const installed = await invoke([
      'grok', 'hooks', 'install', '--scope', 'project', '--project-root', root, '--command-template', template,
    ]);
    assert.equal(installed.code, 0, installed.stderr);
    assert.equal(JSON.parse(installed.stdout).result, 'installed');
    assert.match(JSON.parse(installed.stdout).warning, /\/hooks-trust/);
    const hookPath = path.join(root, '.grok', 'hooks', 'threadforge.json');
    const config = JSON.parse(await readFile(hookPath, 'utf8')) as { hooks: Record<string, Array<{ hooks: Array<{ command: string }> }>> };
    assert.match(config.hooks.PreToolUse[0].hooks[0].command, /grok hook pre-tool --threadforge-grok-hook=v1/);

    const observed = await invoke(['grok', 'hook', 'pre-tool', '--threadforge-grok-hook=v1'], {
      THREADFORGE_EVENT_ENDPOINT: 'http://127.0.0.1:1/v1/events',
    }, JSON.stringify({ hookEventName: 'pre_tool_use', sessionId: 'private-session', toolName: 'run_terminal_command', toolInput: { command: 'private command' } }));
    assert.equal(observed.code, 0, observed.stderr);
    assert.equal(observed.stdout, '');
    assert.match(observed.stderr, /no policy decision was made/);
    assert.equal(observed.stderr.includes('private command'), false);

    const rejected = await invoke(['grok', 'hook', 'pre-tool', '--wrong-marker'], {}, '{}');
    assert.equal(rejected.code, 2);
    assert.match(rejected.stderr, /invalid or unowned/);

    const removed = await invoke(['grok', 'hooks', 'uninstall', '--scope', 'project', '--project-root', root]);
    assert.equal(removed.code, 0, removed.stderr);
    assert.equal(JSON.parse(removed.stdout).result, 'removed');
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Grok local-history CLI exposes metadata-only status and a non-mutating preview', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-history-cli-'));
  const sessionsDir = path.join(root, 'sessions');
  const statePath = path.join(root, 'threadforge-state', 'grok.json');
  try {
    await mkdir(sessionsDir, { recursive: true });
    const statusResult = await invoke(['grok', 'sessions', 'status', '--sessions-dir', sessionsDir, '--state-path', statePath]);
    assert.equal(statusResult.code, 0, statusResult.stderr);
    const status = JSON.parse(statusResult.stdout);
    assert.equal(status.sessions, 0);
    assert.equal(status.pending_sessions, 0);
    assert.equal(status.incomplete_sessions, 0);

    const preview = await invoke(['grok', 'sessions', 'sync', '--sessions-dir', sessionsDir, '--state-path', statePath, '--print']);
    assert.equal(preview.code, 0, preview.stderr);
    assert.equal(JSON.parse(preview.stdout).complete, true);
    await assert.rejects(readFile(statePath), { code: 'ENOENT' });

    const insideSource = await invoke(['grok', 'sessions', 'status', '--sessions-dir', sessionsDir, '--state-path', path.join(sessionsDir, 'cursor.json')]);
    assert.notEqual(insideSource.code, 0);
    assert.match(insideSource.stderr, /stored outside Grok sessions/);
    const invalidBounds = await invoke(['grok', 'sessions', 'status', '--sessions-dir', sessionsDir, '--max-sessions', '5001']);
    assert.notEqual(invalidBounds.code, 0);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Claude Cowork CLI prints public HTTPS guidance only and refuses setup or credential options', async () => {
  const printed = await invoke(['claude-cowork', 'otel', 'print-config', '--endpoint', 'https://otel.example.com/otlp']);
  assert.equal(printed.code, 0, printed.stderr);
  assert.match(printed.stdout, /OTLP protocol: HTTP\/protobuf/);
  assert.match(printed.stdout, /remote export is opt-in/);
  assert.match(printed.stdout, /does not relay, receive, validate, or forward/);
  assert.doesNotMatch(printed.stdout, /Authorization|Bearer|token=/i);

  const missing = await invoke(['cowork', 'otel', 'print-config']);
  assert.notEqual(missing.code, 0);
  const credentialFlag = await invoke(['cowork', 'otel', 'print-config', '--endpoint', 'https://otel.example.com', '--headers', 'Authorization=Bearer secret']);
  assert.notEqual(credentialFlag.code, 0);
  assert.doesNotMatch(credentialFlag.stderr, /secret/);
  for (const action of ['setup', 'status', 'validate']) {
    const unsupported = await invoke(['cowork', 'otel', action]);
    assert.notEqual(unsupported.code, 0);
  }
  const local = await invoke(['cowork', 'otel', 'print-config', '--endpoint', 'http://127.0.0.1:4318']);
  assert.notEqual(local.code, 0);
  assert.doesNotMatch(local.stderr, /127\.0\.0\.1/);
});

test('Goose OTLP CLI prints local guidance and reports only sanitized config and telemetry status', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-otel-cli-'));
  const configPath = path.join(root, 'config.yaml');
  const logPath = path.join(root, 'events.jsonl');
  const config = 'provider: private-provider\notel_exporter_otlp_endpoint: http://127.0.0.1:4318\notel_exporter_otlp_timeout: 10000\n';
  try {
    const printed = await invoke(['goose', 'otel', 'print-config']);
    assert.equal(printed.code, 0, printed.stderr);
    assert.match(printed.stdout, /otel_exporter_otlp_endpoint/);
    assert.match(printed.stdout, /127\.0\.0\.1:4318/);
    assert.equal(printed.stdout.includes('OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true'), false);

    await writeFile(configPath, config, 'utf8');
    await writeFile(logPath, `${JSON.stringify({
      timestamp: new Date().toISOString(),
      harness: { name: 'goose', collection_method: 'otlp' },
      message: 'private reasoning',
    })}\n`, 'utf8');
    const cleanEnv = { OTEL_EXPORTER_OTLP_ENDPOINT: '', OTEL_SDK_DISABLED: '', OTEL_EXPORTER_OTLP_PROTOCOL: '' };
    const statusResult = await invoke(['goose', 'otel', 'status', '--config-path', configPath, '--log-path', logPath, '--json'], cleanEnv);
    assert.equal(statusResult.code, 0, statusResult.stderr);
    const status = JSON.parse(statusResult.stdout) as { configured_endpoint: string; telemetry: { observed_events: number }; issues: string[] };
    assert.equal(status.configured_endpoint, 'loopback_http');
    assert.equal(status.telemetry.observed_events, 1);
    assert.deepEqual(status.issues, []);

    const validated = await invoke(['goose', 'otel', 'validate', '--config-path', configPath, '--log-path', logPath, '--since', '10m', '--json'], cleanEnv);
    assert.equal(validated.code, 0, validated.stderr);
    assert.equal(JSON.parse(validated.stdout).valid, true);

    const overridden = await invoke(['goose', 'otel', 'status', '--config-path', configPath, '--log-path', logPath, '--json'], {
      OTEL_EXPORTER_OTLP_ENDPOINT: 'https://private-collector.example',
      OTEL_SDK_DISABLED: 'true',
      OTEL_EXPORTER_OTLP_PROTOCOL: 'grpc',
    });
    assert.equal(overridden.code, 0, overridden.stderr);
    assert.equal(overridden.stdout.includes('private-collector'), false);
    assert.equal(overridden.stdout.includes(configPath), false);
    assert.equal(overridden.stdout.includes('private reasoning'), false);
    assert.deepEqual(await readFile(configPath, 'utf8'), config);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Goose history CLI exposes status without requiring Goose to be running', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-goose-history-cli-'));
  try {
    const databasePath = path.join(root, 'missing-sessions.db');
    const statePath = path.join(root, 'threadforge', 'state.json');
    const status = await invoke([
      'goose', 'sessions', 'status', '--db-path', databasePath, '--state-path', statePath,
    ]);
    assert.equal(status.code, 0, status.stderr);
    const parsed = JSON.parse(status.stdout) as { schema_version: string; database_present: boolean; errors: unknown[] };
    assert.equal(parsed.schema_version, 'threadforge.goose-history-status.v1');
    assert.equal(parsed.database_present, false);
    assert.deepEqual(parsed.errors, []);
    assert.equal(status.stdout.includes(root), false);

    const invalid = await invoke(['goose', 'sessions', 'sync', '--db-path', 'relative.db']);
    assert.notEqual(invalid.code, 0);
    assert.doesNotMatch(invalid.stdout, /private-goose-session/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('VS Code OTLP CLI prints local settings guidance and validates JSONC-backed Copilot events', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-vscode-otel-cli-'));
  const settingsPath = path.join(root, 'settings.json');
  const logPath = path.join(root, 'events.jsonl');
  const settings = `{
  // Existing VS Code settings are retained; Threadforge only inspects this file.
  "github.copilot.chat.otel.enabled": true,
  "github.copilot.chat.otel.exporterType": "otlp-http",
  "github.copilot.chat.otel.otlpEndpoint": "http://127.0.0.1:4318",
  "github.copilot.chat.otel.captureContent": false,
}`;
  try {
    const printed = await invoke(['vscode', 'otel', 'print-config']);
    assert.equal(printed.code, 0, printed.stderr);
    assert.match(printed.stdout, /github\.copilot\.chat\.otel\.enabled/);
    assert.match(printed.stdout, /"github\.copilot\.chat\.otel\.captureContent": false/);

    await writeFile(settingsPath, settings, 'utf8');
    await writeFile(logPath, `${JSON.stringify({
      timestamp: new Date().toISOString(),
      harness: { name: 'vscode_copilot', collection_method: 'otlp' },
      message: 'private Copilot prompt',
    })}\n`, 'utf8');
    const status = await invoke(['vscode', 'otel', 'status', '--settings-path', settingsPath, '--log-path', logPath, '--json']);
    assert.equal(status.code, 0, status.stderr);
    const value = JSON.parse(status.stdout) as { configured_endpoint: string; telemetry: { observed_events: number }; issues: string[] };
    assert.equal(value.configured_endpoint, 'loopback_http');
    assert.equal(value.telemetry.observed_events, 1);
    assert.deepEqual(value.issues, []);

    const valid = await invoke(['vscode', 'otel', 'validate', '--settings-path', settingsPath, '--log-path', logPath, '--since', '10m', '--json']);
    assert.equal(valid.code, 0, valid.stderr);
    assert.equal(JSON.parse(valid.stdout).valid, true);
    assert.equal(status.stdout.includes(settingsPath), false);
    assert.equal(status.stdout.includes('private Copilot prompt'), false);
    assert.equal(await readFile(settingsPath, 'utf8'), settings);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('project MCP bootstrap plans without writes, installs fourteen host IDs across host config shapes, and uninstalls only owned entries', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-bootstrap-mcp-'));
  const canonicalRoot = await realpath(root);
  const hostList = 'claude-code,command-code,github-copilot,qoder,zcode,kimi-code,cursor,antigravity,gemini-cli,qwen-code,codex-cli,chatgpt-desktop-codex,codex-ide,opencode';
  const args = ['--project-root', root, '--hosts', hostList, '--command', 'threadforge.exe',
    '--arg', 'mcp', '--arg', 'serve', '--arg', '--project-root', '--arg', root];
  const configPaths = [
    '.mcp.json', '.mcp.json', '.mcp.json', '.mcp.json', path.join('.zcode', 'config.json'), path.join('.kimi-code', 'mcp.json'),
    path.join('.cursor', 'mcp.json'), path.join('.agents', 'mcp_config.json'),
    path.join('.gemini', 'settings.json'), path.join('.qwen', 'settings.json'), path.join('.codex', 'config.toml'),
    path.join('.codex', 'config.toml'), path.join('.codex', 'config.toml'), 'opencode.json',
  ];
  try {
    const plan = await invoke(['bootstrap', 'mcp', 'plan', ...args]);
    assert.equal(plan.code, 0, plan.stderr);
    const planValue = JSON.parse(plan.stdout) as { status: string; mutates_files: boolean; hosts: { config_path: string }[] };
    assert.equal(planValue.status, 'planned');
    assert.equal(planValue.mutates_files, false);
    assert.deepEqual(planValue.hosts.map((entry) => entry.config_path), configPaths);
    for (const configPath of configPaths) await assert.rejects(readFile(path.join(root, configPath)));

    const qwenSettingsPath = path.join(root, '.qwen', 'settings.json');
    await mkdir(path.dirname(qwenSettingsPath), { recursive: true });
    const qwenSettings = {
      hooks: { SessionStart: [{ hooks: [{ type: 'command', command: 'user-owned-hook' }] }] },
      mcpServers: { analytics: { command: 'analytics', args: ['serve'] } },
    };
    await writeFile(qwenSettingsPath, JSON.stringify(qwenSettings, null, 2));

    const zcodeConfigPath = path.join(root, '.zcode', 'config.json');
    await mkdir(path.dirname(zcodeConfigPath), { recursive: true });
    const zcodeConfig = {
      features: { mcp: true },
      mcp: { servers: { analytics: { type: 'stdio', command: 'analytics', args: ['serve'] } } },
      ui: { locale: 'en-US' },
    };
    await writeFile(zcodeConfigPath, JSON.stringify(zcodeConfig, null, 2));

    const installed = await invoke(['bootstrap', 'mcp', 'install', ...args]);
    assert.equal(installed.code, 0, installed.stderr);
    const installValue = JSON.parse(installed.stdout) as {
      schema_version: string; status: string; host_trust_changed: boolean; hosts: { result: string }[];
    };
    assert.equal(installValue.schema_version, 'threadforge.project-bootstrap-result.v1');
    assert.equal(installValue.status, 'complete');
    assert.equal(installValue.host_trust_changed, false);
    assert.deepEqual(installValue.hosts.map((entry) => entry.result), ['installed', 'unchanged', 'unchanged', 'unchanged', 'installed', 'installed', 'installed', 'installed', 'installed', 'installed', 'installed', 'unchanged', 'unchanged', 'installed']);

    const gemini = JSON.parse(await readFile(path.join(root, '.gemini', 'settings.json'), 'utf8'));
    assert.equal(gemini.mcpServers.threadforge.trust, false);
    const cursor = JSON.parse(await readFile(path.join(root, '.cursor', 'mcp.json'), 'utf8'));
    assert.deepEqual(cursor.mcpServers.threadforge.args, ['mcp', 'serve', '--project-root', canonicalRoot, '--threadforge-managed-mcp=v1']);
    const qwen = JSON.parse(await readFile(qwenSettingsPath, 'utf8'));
    assert.deepEqual(qwen.hooks, qwenSettings.hooks);
    assert.deepEqual(qwen.mcpServers.analytics, qwenSettings.mcpServers.analytics);
    assert.deepEqual(qwen.mcpServers.threadforge, {
      command: 'threadforge.exe', args: ['mcp', 'serve', '--project-root', canonicalRoot, '--threadforge-managed-mcp=v1'],
    });
    assert.equal(Object.hasOwn(qwen.mcpServers.threadforge, 'trust'), false);
    const claudeAndCommandCode = JSON.parse(await readFile(path.join(root, '.mcp.json'), 'utf8'));
    assert.deepEqual(claudeAndCommandCode.mcpServers.threadforge.args,
      ['mcp', 'serve', '--project-root', canonicalRoot, '--threadforge-managed-mcp=v1']);
    const kimiProject = JSON.parse(await readFile(path.join(root, '.kimi-code', 'mcp.json'), 'utf8'));
    assert.deepEqual(kimiProject.mcpServers.threadforge.args,
      ['mcp', 'serve', '--project-root', canonicalRoot, '--threadforge-managed-mcp=v1']);

    const zcode = JSON.parse(await readFile(zcodeConfigPath, 'utf8'));
    assert.deepEqual(zcode.features, zcodeConfig.features);
    assert.deepEqual(zcode.ui, zcodeConfig.ui);
    assert.deepEqual(zcode.mcp.servers.analytics, zcodeConfig.mcp.servers.analytics);
    assert.deepEqual(zcode.mcp.servers.threadforge, {
      type: 'stdio', command: 'threadforge.exe',
      args: ['mcp', 'serve', '--project-root', canonicalRoot, '--threadforge-managed-mcp=v1'],
    });
    assert.equal(Object.hasOwn(zcode.mcp.servers.threadforge, 'trust'), false);

    const repeated = await invoke(['bootstrap', 'mcp', 'install', ...args]);
    assert.equal(repeated.code, 0, repeated.stderr);
    const repeatedValue = JSON.parse(repeated.stdout) as { hosts: { result: string }[] };
    assert.deepEqual(repeatedValue.hosts.map((entry) => entry.result), Array(14).fill('unchanged'));

    const updated = await invoke(['bootstrap', 'mcp', 'update', ...args.map((value, index) => index === args.indexOf('threadforge.exe') ? 'threadforge-next.exe' : value)]);
    assert.equal(updated.code, 0, updated.stderr);
    const updateValue = JSON.parse(updated.stdout) as { hosts: { result: string }[] };
    assert.deepEqual(updateValue.hosts.map((entry) => entry.result), ['updated', 'unchanged', 'unchanged', 'unchanged', 'updated', 'updated', 'updated', 'updated', 'updated', 'updated', 'updated', 'unchanged', 'unchanged', 'updated']);
    const updatedCodex = await readFile(path.join(root, '.codex', 'config.toml'), 'utf8');
    assert.equal(updatedCodex.includes('threadforge-next.exe'), true);
    const updatedQwen = JSON.parse(await readFile(qwenSettingsPath, 'utf8'));
    assert.equal(updatedQwen.mcpServers.threadforge.command, 'threadforge-next.exe');
    assert.deepEqual(updatedQwen.hooks, qwenSettings.hooks);
    const updatedZcode = JSON.parse(await readFile(zcodeConfigPath, 'utf8'));
    assert.equal(updatedZcode.mcp.servers.threadforge.command, 'threadforge-next.exe');
    assert.equal(updatedZcode.mcp.servers.threadforge.type, 'stdio');
    assert.deepEqual(updatedZcode.features, zcodeConfig.features);

    const removed = await invoke(['bootstrap', 'mcp', 'uninstall', '--project-root', root, '--hosts', hostList]);
    assert.equal(removed.code, 0, removed.stderr);
    const removedValue = JSON.parse(removed.stdout) as { status: string; hosts: { result: string }[] };
    assert.equal(removedValue.status, 'complete');
    assert.deepEqual(removedValue.hosts.map((entry) => entry.result), ['removed', 'absent', 'absent', 'absent', 'removed', 'removed', 'removed', 'removed', 'removed', 'removed', 'removed', 'absent', 'absent', 'removed']);
    assert.deepEqual(JSON.parse(await readFile(qwenSettingsPath, 'utf8')), qwenSettings);
    assert.deepEqual(JSON.parse(await readFile(zcodeConfigPath, 'utf8')), zcodeConfig);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('direct ZCode MCP CLI manages its project config without changing trust settings', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-zcode-mcp-'));
  const configPath = path.join(root, '.zcode', 'config.json');
  const original = {
    features: { mcp: true },
    mcp: { servers: { analytics: { type: 'stdio', command: 'analytics', args: ['serve'] } } },
  };
  await mkdir(path.dirname(configPath), { recursive: true });
  await writeFile(configPath, JSON.stringify(original, null, 2));
  const args = ['--host', 'zcode', '--project-root', root, '--command', 'threadforge.exe',
    '--arg', 'mcp', '--arg', 'serve', '--arg', '--project-root', '--arg', root];
  try {
    const installed = await invoke(['mcp', 'install', ...args]);
    assert.equal(installed.code, 0, installed.stderr);
    let config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(config.mcp.servers.threadforge, {
      type: 'stdio', command: 'threadforge.exe',
      args: ['mcp', 'serve', '--project-root', root, '--threadforge-managed-mcp=v1'],
    });
    assert.deepEqual(config.features, original.features);
    assert.deepEqual(config.mcp.servers.analytics, original.mcp.servers.analytics);
    assert.equal(Object.hasOwn(config.mcp.servers.threadforge, 'trust'), false);

    const updated = await invoke(['mcp', 'update', ...args.map((value, index) => index === args.indexOf('threadforge.exe') ? 'threadforge-next.exe' : value)]);
    assert.equal(updated.code, 0, updated.stderr);
    config = JSON.parse(await readFile(configPath, 'utf8'));
    assert.equal(config.mcp.servers.threadforge.command, 'threadforge-next.exe');

    const removed = await invoke(['mcp', 'uninstall', '--host', 'zcode', '--project-root', root]);
    assert.equal(removed.code, 0, removed.stderr);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), original);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('project instruction bootstrap plans, installs shared advisory files, and removes managed blocks', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-bootstrap-instructions-'));
  const hosts = 'claude-code,command-code,cursor,antigravity,gemini-cli,codex-cli,chatgpt-desktop-codex,codex-ide,opencode,qwen-code,github-copilot,zcode,kimi-code,qoder,deepseek-harness,goose,cline';
  const args = ['--project-root', root, '--hosts', hosts];
  try {
    const plan = await invoke(['bootstrap', 'instructions', 'plan', ...args]);
    assert.equal(plan.code, 0, plan.stderr);
    const planValue = JSON.parse(plan.stdout) as { mutates_files: boolean; targets: { path: string }[] };
    assert.equal(planValue.mutates_files, false);
    assert.deepEqual(planValue.targets.map((target) => target.path), [
      'CLAUDE.md', 'AGENTS.md', 'GEMINI.md', 'QWEN.md', '.github/copilot-instructions.md',
      '.goosehints', '.cline/rules/threadforge-memory.md',
    ]);

    const install = await invoke(['bootstrap', 'instructions', 'install', ...args]);
    assert.equal(install.code, 0, install.stderr);
    const result = JSON.parse(install.stdout) as {
      status: string; host_trust_changed: boolean; execution_authorized: boolean;
      targets: { path: string; result: string; hosts: string[] }[];
    };
    assert.equal(result.status, 'complete');
    assert.equal(result.host_trust_changed, false);
    assert.equal(result.execution_authorized, false);
    assert.deepEqual(result.targets.map((target) => target.result), ['installed', 'installed', 'installed', 'installed', 'installed', 'installed', 'installed']);
    assert.equal(result.targets.find((target) => target.path === 'AGENTS.md')?.hosts.length, 11);
    assert.match(await readFile(path.join(root, 'GEMINI.md'), 'utf8'), /guidance, not permission or authorization/);
    assert.match(await readFile(path.join(root, 'GEMINI.md'), 'utf8'), /If that permission is unclear, do not retrieve or inject the context/);
    assert.match(await readFile(path.join(root, 'QWEN.md'), 'utf8'), /guidance, not permission or authorization/);
    assert.match(await readFile(path.join(root, '.github', 'copilot-instructions.md'), 'utf8'), /guidance, not permission or authorization/);
    assert.match(await readFile(path.join(root, '.goosehints'), 'utf8'), /guidance, not permission or authorization/);
    assert.match(await readFile(path.join(root, '.cline', 'rules', 'threadforge-memory.md'), 'utf8'), /Never guess a project_id/);

    const removed = await invoke(['bootstrap', 'instructions', 'uninstall', ...args]);
    assert.equal(removed.code, 0, removed.stderr);
    assert.deepEqual((JSON.parse(removed.stdout) as { targets: { result: string }[] }).targets.map((target) => target.result), Array(7).fill('removed'));
    for (const filename of ['CLAUDE.md', 'AGENTS.md', 'GEMINI.md', 'QWEN.md', '.github/copilot-instructions.md', '.goosehints', '.cline/rules/threadforge-memory.md']) {
      await assert.rejects(readFile(path.join(root, filename)));
    }
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('project MCP bootstrap reports partial failures, preserves conflicts, and rejects secrets before mutation', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-bootstrap-mcp-partial-'));
  const cursorPath = path.join(root, '.cursor', 'mcp.json');
  try {
    await mkdir(path.dirname(cursorPath), { recursive: true });
    const original = JSON.stringify({ mcpServers: { threadforge: { command: 'user-owned', args: ['leave-alone'] } } }, null, 2);
    await writeFile(cursorPath, original, 'utf8');
    const args = ['--project-root', root, '--hosts', 'claude-code,cursor,gemini-cli', '--command', 'threadforge.exe',
      '--arg', 'mcp', '--arg', 'serve', '--arg', '--project-root', '--arg', root];
    const partial = await invoke(['bootstrap', 'mcp', 'install', ...args]);
    assert.equal(partial.code, 1);
    const partialValue = JSON.parse(partial.stdout) as {
      status: string; atomic: boolean; partial_changes_possible: boolean;
      hosts: { host: string; result: string; failure_code?: string }[];
    };
    assert.equal(partialValue.status, 'partial');
    assert.equal(partialValue.atomic, false);
    assert.equal(partialValue.partial_changes_possible, true);
    assert.deepEqual(partialValue.hosts.map((entry) => entry.result), ['installed', 'failed', 'installed']);
    assert.equal(partialValue.hosts[1]?.failure_code, 'configuration_conflict_or_unavailable');
    assert.equal(await readFile(cursorPath, 'utf8'), original);
    assert.equal((await readFile(path.join(root, '.mcp.json'), 'utf8')).includes('threadforge-managed-mcp=v1'), true);

    const unsafeRoot = await mkdtemp(path.join(os.tmpdir(), 'threadforge-bootstrap-mcp-secret-'));
    try {
      const secret = 'api_key=fixture-secret-value';
      const unsafe = await invoke(['bootstrap', 'mcp', 'install', '--project-root', unsafeRoot, '--hosts', 'claude-code',
        '--command', `threadforge ${secret}`, '--arg', 'mcp', '--arg', 'serve', '--arg', '--project-root', '--arg', unsafeRoot]);
      assert.equal(unsafe.code, 1);
      assert.equal(unsafe.stdout.includes(secret), false);
      assert.equal(unsafe.stderr.includes(secret), false);
      await assert.rejects(readFile(path.join(unsafeRoot, '.mcp.json')));
    } finally { await rm(unsafeRoot, { recursive: true, force: true }); }
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Cline plugin CLI installs and removes explicit user and project registrations', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-plugin-cli-'));
  const project = path.join(root, 'project');
  const clineHome = path.join(root, 'cline-home');
  await mkdir(project);
  try {
    let result = await invoke(['cline', 'plugin', 'install', '--scope', 'project', '--project-root', project]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    result = await invoke(['cline', 'plugin', 'status', '--scope', 'project', '--project-root', project]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    result = await invoke(['cline', 'plugin', 'uninstall', '--scope', 'project', '--project-root', project]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');

    result = await invoke(['cline', 'plugin', 'install', '--cline-dir', clineHome]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).scope, 'user');
    result = await invoke(['cline', 'plugin', 'uninstall', '--cline-dir', clineHome]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    await assert.rejects(readFile(path.join(project, '.cline', 'settings.json')), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Cline MCP CLI manages only the user-global settings file', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-mcp-cli-'));
  const settings = path.join(root, 'data', 'settings', 'cline_mcp_settings.json');
  try {
    let result = await invoke(['cline', 'mcp', 'install', '--cline-dir', root, '--command', 'threadforge.exe']);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    let config = JSON.parse(await readFile(settings, 'utf8'));
    assert.deepEqual(config.mcpServers.threadforge.args, ['mcp', 'serve', '--threadforge-managed-mcp=v1']);
    assert.deepEqual(config.mcpServers.threadforge.autoApprove, []);

    result = await invoke(['cline', 'mcp', 'status', '--cline-dir', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    result = await invoke(['cline', 'mcp', 'update', '--cline-dir', root, '--expected-command', 'threadforge.exe', '--command', 'threadforge-next.exe']);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'updated');
    result = await invoke(['cline', 'mcp', 'uninstall', '--cline-dir', root, '--expected-command', 'threadforge-next.exe']);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(settings, 'utf8')), {});

    result = await invoke(['cline', 'mcp', 'install', '--cline-dir', root, '--project-root', root, '--command', 'threadforge.exe']);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /unexpected|unsupported/);
    result = await invoke(['cline', 'mcp', 'install', '--cline-dir', root, '--command', 'threadforge.exe', '--config-path', settings]);
    assert.equal(result.code, 1);
    assert.match(result.stderr, /either --cline-dir or --config-path/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
