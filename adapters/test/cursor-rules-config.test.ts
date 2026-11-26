import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { Writable } from 'node:stream';
import test from 'node:test';

import {
  installCursorProjectMemoryRule,
  planCursorProjectMemoryRule,
  uninstallCursorProjectMemoryRule,
} from '../src/cursor-rules-config.ts';
import { runAdapterCLI } from '../src/cli.ts';

function fixture(): string {
  return mkdtempSync(path.join(os.tmpdir(), 'threadforge-cursor-rules-'));
}

function writableText(): { stream: Writable; text: () => string } {
  let value = '';
  const stream = new Writable({ write(chunk, _encoding, callback) { value += chunk.toString(); callback(); } });
  return { stream, text: () => value };
}

test('plan is read-only and does not create Cursor configuration directories', async () => {
  const root = fixture();
  try {
    const planned = await planCursorProjectMemoryRule(root);
    assert.equal(planned.schema_version, 'threadforge.cursor-project-memory-rule.v1');
    assert.equal(planned.action, 'planned');
    assert.equal(planned.path, '.cursor/rules/threadforge-memory.mdc');
    assert.equal(existsSync(path.join(root, '.cursor')), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('installs only the Threadforge project rule, is idempotent, and uninstalls unchanged content', async () => {
  const root = fixture();
  const cursorRoot = path.join(root, '.cursor');
  const rulesRoot = path.join(cursorRoot, 'rules');
  const settings = path.join(cursorRoot, 'settings.json');
  const hooks = path.join(cursorRoot, 'hooks.json');
  const neighbor = path.join(rulesRoot, 'user-rule.mdc');
  mkdirSync(rulesRoot, { recursive: true });
  writeFileSync(settings, '{"trust":"unchanged"}\n');
  writeFileSync(hooks, '{"hooks":{"sessionStart":[]}}\n');
  writeFileSync(neighbor, 'User-owned rule.\n');
  try {
    const installed = await installCursorProjectMemoryRule(root);
    assert.equal(installed.action, 'installed');
    assert.equal(installed.host, 'cursor');
    assert.equal(installed.path, '.cursor/rules/threadforge-memory.mdc');
    const target = path.join(root, ...installed.path.split('/'));
    const bytes = readFileSync(target);
    const content = bytes.toString('utf8');
    assert.equal(createHash('sha256').update(bytes).digest('hex'), installed.sha256);
    assert.match(content, /^---\ndescription: .*\nalwaysApply: false\n---\n/);
    assert.match(content, /advisory context/);
    assert.match(content, /never permission or execution authorization/);
    assert.match(content, /explicitly asks/);
    assert.doesNotMatch(content, /SECRET_PROMPT|api[_-]?key|sk-[A-Za-z0-9]/i);

    const repeated = await installCursorProjectMemoryRule(root);
    assert.equal(repeated.action, 'unchanged');
    assert.equal(readFileSync(settings, 'utf8'), '{"trust":"unchanged"}\n');
    assert.equal(readFileSync(hooks, 'utf8'), '{"hooks":{"sessionStart":[]}}\n');
    assert.equal(readFileSync(neighbor, 'utf8'), 'User-owned rule.\n');

    const removed = await uninstallCursorProjectMemoryRule(root);
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(settings, 'utf8'), '{"trust":"unchanged"}\n');
    assert.equal(readFileSync(hooks, 'utf8'), '{"hooks":{"sessionStart":[]}}\n');
    assert.equal(readFileSync(neighbor, 'utf8'), 'User-owned rule.\n');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('refuses an unowned collision and never overwrites user rules', async () => {
  const root = fixture();
  const target = path.join(root, '.cursor', 'rules', 'threadforge-memory.mdc');
  mkdirSync(path.dirname(target), { recursive: true });
  writeFileSync(target, 'User-owned rule.\n');
  try {
    await assert.rejects(planCursorProjectMemoryRule(root), /different content; refusing to overwrite/);
    await assert.rejects(installCursorProjectMemoryRule(root), /different content; refusing to overwrite/);
    await assert.rejects(uninstallCursorProjectMemoryRule(root), /edited or is user-owned; refusing to remove/);
    assert.equal(readFileSync(target, 'utf8'), 'User-owned rule.\n');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('refuses to remove a user-edited Threadforge rule', async () => {
  const root = fixture();
  const target = path.join(root, '.cursor', 'rules', 'threadforge-memory.mdc');
  try {
    await installCursorProjectMemoryRule(root);
    writeFileSync(target, readFileSync(target, 'utf8') + '\nUser edit.\n');
    await assert.rejects(uninstallCursorProjectMemoryRule(root), /edited or is user-owned; refusing to remove/);
    assert.ok(readFileSync(target, 'utf8').endsWith('User edit.\n'));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('fails closed on a linked Cursor rules directory', async (t) => {
  const root = fixture();
  const outside = fixture();
  try {
    try { symlinkSync(outside, path.join(root, '.cursor'), process.platform === 'win32' ? 'junction' : undefined); }
    catch { t.skip('link creation is unavailable in this environment'); return; }
    await assert.rejects(installCursorProjectMemoryRule(root), /symbolic link/);
    assert.deepEqual(readdirSync(outside), []);
  } finally {
    rmSync(root, { recursive: true, force: true });
    rmSync(outside, { recursive: true, force: true });
  }
});

test('refuses an existing installer lock without creating the rule', async () => {
  const root = fixture();
  const rulesRoot = path.join(root, '.cursor', 'rules');
  mkdirSync(rulesRoot, { recursive: true });
  writeFileSync(path.join(rulesRoot, '.threadforge-memory-rule.lock'), 'other writer\n');
  try {
    await assert.rejects(installCursorProjectMemoryRule(root), /locked by another installer or a stale lock/);
    assert.equal(existsSync(path.join(rulesRoot, 'threadforge-memory.mdc')), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('Cursor rules CLI supports read-only plan and explicit install/uninstall', async () => {
  const root = fixture();
  const stdout = writableText();
  const stderr = writableText();
  try {
    assert.equal(await runAdapterCLI(['cursor', 'rules', 'plan', '--project-root', root], stdout.stream, stderr.stream), 0, stderr.text());
    assert.equal(JSON.parse(stdout.text()).result.action, 'planned');
    assert.equal(existsSync(path.join(root, '.cursor')), false);

    const installOut = writableText();
    assert.equal(await runAdapterCLI(['cursor', 'rules', 'install', '--project-root', root], installOut.stream, stderr.stream), 0, stderr.text());
    assert.equal(JSON.parse(installOut.text()).result.action, 'installed');

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['cursor', 'rules', 'uninstall', '--project-root', root], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal(JSON.parse(removeOut.text()).result.action, 'removed');
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
