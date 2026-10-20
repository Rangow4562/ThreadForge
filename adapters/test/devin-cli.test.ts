import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { devinManagedMarker } from '../src/devin-hook.ts';

const cli = fileURLToPath(new URL('../src/cli.ts', import.meta.url));

async function invoke(args: string[], stdinInput = '', env: NodeJS.ProcessEnv = {}): Promise<{ code: number | null; stdout: string; stderr: string }> {
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

test('devin-cli alias installs, idempotently updates, and removes project hooks through the adapter CLI', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-devin-cli-'));
  const file = path.join(root, '.devin', 'hooks.v1.json');
  const commandTemplate = `node hook {event} ${devinManagedMarker}`;
  try {
    let result = await invoke(['devin-cli', 'install', '--scope', 'project', '--project-root', root, '--command-template', commandTemplate]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'installed');
    const config = JSON.parse(await readFile(file, 'utf8'));
    assert.equal(config.SessionStart[0].hooks[0].command, commandTemplate.replace('{event}', 'SessionStart'));

    result = await invoke(['devin', 'update', '--scope', 'project', '--project-root', root, '--command-template', commandTemplate]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'unchanged');

    result = await invoke(['devin', 'uninstall', '--scope', 'project', '--project-root', root]);
    assert.equal(result.code, 0, result.stderr);
    assert.equal(JSON.parse(result.stdout).result, 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), {});
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('hook dispatch is marker-gated and fail-open with no stdout decision', async () => {
  const validPayload = JSON.stringify({ hook_event_name: 'SessionStart', session_id: 'private-session' });
  const valid = await invoke(['devin', 'hook', 'SessionStart', devinManagedMarker], validPayload, {
    THREADFORGE_EVENT_ENDPOINT: 'https://invalid.example/v1/events',
  });
  assert.equal(valid.code, 0);
  assert.equal(valid.stdout, '');
  assert.equal(valid.stderr.includes('private-session'), false);

  const invalid = await invoke(['devin', 'hook', 'PermissionRequest', '--threadforge-devin-hook=wrong'], validPayload);
  assert.equal(invalid.code, 2);
  assert.equal(invalid.stdout, '');
  assert.equal(invalid.stderr.includes('private-session'), false);
});
