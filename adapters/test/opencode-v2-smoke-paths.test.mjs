import assert from 'node:assert/strict';
import { mkdirSync, mkdtempSync, rmSync, symlinkSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  canonicalSafeTempRoot,
  isPathWithin,
  verifiedIsolatedHome,
} from '../scripts/opencode-v2-smoke-paths.mjs';

const configNames = ['.threadforge-test-host-config'];

test('canonical temp-root checks reject real-home and host-config ancestors', (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'threadforge-opencode-v2-path-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const userHome = path.join(root, 'user-home');
  const cleanRoot = path.join(root, 'clean-temp');
  const configuredBase = path.join(root, 'configured-base');
  const configuredRoot = path.join(configuredBase, 'configured-temp');
  mkdirSync(userHome);
  mkdirSync(cleanRoot);
  mkdirSync(configuredBase);
  mkdirSync(configuredRoot);
  mkdirSync(path.join(configuredBase, configNames[0]));

  assert.equal(canonicalSafeTempRoot(userHome, userHome, configNames), undefined);
  assert.equal(canonicalSafeTempRoot(configuredRoot, userHome, configNames), undefined);
  assert.equal(canonicalSafeTempRoot(cleanRoot, userHome, configNames), cleanRoot);
});

test('canonical temp-root checks follow symlink aliases before checking home and config ancestry', (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'threadforge-opencode-v2-alias-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const userHome = path.join(root, 'user-home');
  const externalTemp = path.join(root, 'external-temp');
  mkdirSync(userHome);
  mkdirSync(externalTemp);
  const userAlias = path.join(root, 'user-alias');
  const configAlias = path.join(root, 'config-alias');
  try {
    symlinkSync(userHome, userAlias, process.platform === 'win32' ? 'junction' : 'dir');
    symlinkSync(externalTemp, configAlias, process.platform === 'win32' ? 'junction' : 'dir');
  } catch (error) {
    t.skip(`directory symlinks are unavailable: ${String(error)}`);
    return;
  }
  mkdirSync(path.join(externalTemp, configNames[0]));
  assert.equal(canonicalSafeTempRoot(userAlias, userHome, configNames), undefined);
  assert.equal(canonicalSafeTempRoot(configAlias, userHome, configNames), undefined);
});

test('created smoke homes must resolve inside the canonical root and outside the real home', (t) => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'threadforge-opencode-v2-created-home-test-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const userHome = path.join(root, 'user-home');
  const tempRoot = path.join(root, 'temp-root');
  mkdirSync(userHome);
  mkdirSync(tempRoot);
  const created = mkdtempSync(path.join(tempRoot, 'smoke-'));
  const outsideHome = mkdtempSync(path.join(root, 'outside-'));
  assert.equal(verifiedIsolatedHome(created, tempRoot, userHome, configNames), created);
  assert.equal(verifiedIsolatedHome(outsideHome, tempRoot, userHome, configNames), undefined);
  assert.equal(isPathWithin(tempRoot, outsideHome), false);
});
