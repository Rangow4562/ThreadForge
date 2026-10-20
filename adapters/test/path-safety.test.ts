import assert from 'node:assert/strict';
import { mkdtemp, realpath, rm, symlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { isSameRealPathWithoutLinks } from '../src/path-safety.ts';

test('accepts a real directory, including Windows short-name realpath aliases', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-path-safety-'));
  try {
    assert.equal(await isSameRealPathWithoutLinks(await realpath(root), root), true);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('rejects a path that traverses a symlink or junction', async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-path-safety-'));
  const link = `${root}-link`;
  try {
    try { await symlink(root, link, process.platform === 'win32' ? 'junction' : undefined); }
    catch { t.skip('symlink creation is unavailable in this environment'); return; }
    assert.equal(await isSameRealPathWithoutLinks(await realpath(root), link), false);
  } finally {
    await rm(link, { recursive: true, force: true }).catch(() => {});
    await rm(root, { recursive: true, force: true });
  }
});
