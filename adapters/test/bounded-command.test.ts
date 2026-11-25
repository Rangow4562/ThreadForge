import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { runBoundedCommand } from '../src/bounded-command.ts';

test('captures successful stdout within the configured byte limit', async () => {
  const output = await runBoundedCommand(process.execPath, ['-e', 'process.stdout.write("ok")'], {
    label: 'fixture CLI', timeoutMs: 5_000, captureStdout: true, maxStdoutBytes: 8,
  });
  assert.equal(output, 'ok');
});

test('rejects commands that exceed the stdout byte limit', async () => {
  await assert.rejects(runBoundedCommand(process.execPath, ['-e', 'process.stdout.write("x".repeat(32))'], {
    label: 'fixture CLI', timeoutMs: 5_000, captureStdout: true, maxStdoutBytes: 8,
  }), /fixture CLI output exceeded the local limit/);
});

test('times out and terminates a non-responsive command', async () => {
  const started = Date.now();
  await assert.rejects(runBoundedCommand(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], {
    label: 'fixture CLI', timeoutMs: 100,
  }), /fixture CLI timed out after 100 ms/);
  assert.ok(Date.now() - started < 5_000, 'timed-out command should be reaped promptly');
});

test('Windows timeout terminates descendant processes as well as the CLI shim', {
  skip: process.platform !== 'win32',
}, async () => {
  const root = mkdtempSync(path.join(tmpdir(), 'threadforge-command-tree-'));
  const pidFile = path.join(root, 'child.pid');
  try {
    await assert.rejects(runBoundedCommand(process.execPath, ['-e', childTreeScript(pidFile)], {
      label: 'fixture CLI', timeoutMs: 1_000,
    }), /fixture CLI timed out after 1000 ms/);
    await assertDescendantStopped(Number(readFileSync(pidFile, 'utf8')));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('POSIX timeout terminates the spawned process group', {
  skip: process.platform === 'win32',
}, async () => {
  const root = mkdtempSync(path.join(tmpdir(), 'threadforge-command-group-'));
  const pidFile = path.join(root, 'child.pid');
  try {
    const script = childTreeScript(pidFile);
    await assert.rejects(runBoundedCommand(process.execPath, ['-e', script], {
      label: 'fixture CLI', timeoutMs: 1_000,
    }), /fixture CLI timed out after 1000 ms/);
    await assertDescendantStopped(Number(readFileSync(pidFile, 'utf8')));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

function processExists(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    return typeof error === 'object' && error !== null && 'code' in error && error.code === 'EPERM';
  }
}

function childTreeScript(pidFile: string): string {
  return [
    'const { spawn } = require("node:child_process")',
    'const { writeFileSync } = require("node:fs")',
    'const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore", windowsHide: true })',
    `writeFileSync(${JSON.stringify(pidFile)}, String(child.pid))`,
    'setInterval(() => {}, 1000)',
  ].join(';');
}

async function assertDescendantStopped(pid: number): Promise<void> {
  assert.ok(Number.isSafeInteger(pid) && pid > 0, 'fixture should record its child process id');
  for (let attempt = 0; attempt < 30 && processExists(pid); attempt++) {
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  assert.equal(processExists(pid), false, 'descendant process should not outlive the timed-out CLI');
}
