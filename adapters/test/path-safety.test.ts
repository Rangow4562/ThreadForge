import assert from 'node:assert/strict';
import { mkdtemp, realpath, rm, symlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { isSameRealPathWithoutLinks, isWithinRealPaths, resolveRealPathWithoutLinks } from '../src/path-safety.ts';

test('accepts a real directory, including Windows short-name realpath aliases', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-path-safety-'));
  try {
    const canonical = await realpath(root);
    assert.equal(await isSameRealPathWithoutLinks(canonical, root), true);
    assert.equal(await resolveRealPathWithoutLinks(path.join(root, 'state', 'cursor.json')), path.join(canonical, 'state', 'cursor.json'));
    assert.equal(await isWithinRealPaths(root, path.join(root, 'state', 'cursor.json')), true);
    assert.equal(await isWithinRealPaths(root, `${root}-sibling`), false);
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
    assert.equal(await resolveRealPathWithoutLinks(link), undefined);
  } finally {
    await rm(link, { recursive: true, force: true }).catch(() => {});
    await rm(root, { recursive: true, force: true });
  }
});
