import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const npmCli = process.env.npm_execpath;
assert.ok(npmCli, 'run the package smoke through npm so npm_execpath is available');

const pack = spawnSync(process.execPath, [npmCli, 'pack', '--dry-run', '--json'], {
  cwd: packageRoot,
  encoding: 'utf8',
  timeout: 120_000,
  maxBuffer: 8 << 20,
  env: { ...process.env, NPM_CONFIG_UPDATE_NOTIFIER: 'false' },
});
if (pack.error) throw new Error('npm pack dry run could not be started');
if (pack.status !== 0) throw new Error(`npm pack dry run failed: ${(pack.stderr || pack.stdout).trim().slice(0, 2000)}`);

let packed;
try {
  packed = JSON.parse(pack.stdout);
} catch {
  throw new Error('npm pack dry run returned malformed JSON');
}
assert.ok(Array.isArray(packed) && packed.length === 1, 'expected one adapter package dry-run result');

const manifest = JSON.parse(readFileSync(path.join(packageRoot, 'package.json'), 'utf8'));
const files = packed[0].files;
assert.ok(Array.isArray(files), 'npm did not return package file inventory');
const paths = new Set(files.map((file) => file.path));
for (const target of Object.values(manifest.exports)) {
  assert.ok(paths.has(target.replace(/^\.\//, '')), `package export target is missing: ${target}`);
}
for (const filePath of paths) {
  assert.equal(/(^|\/)(node_modules|browser-extension|test|scripts)(\/|$)/.test(filePath), false,
    `development or build output leaked into adapter package: ${filePath}`);
}
assert.ok(files.length <= 300, `adapter package unexpectedly contains ${files.length} files`);

assert.deepEqual(manifest.bin, { 'threadforge-adapter': './dist/cli.js' },
  'the portable adapter command must have one explicit package entry point');
assert.ok(paths.has('dist/cli.js'), 'compiled CLI entry point is missing from the package');
const tempRoot = mkdtempSync(path.join(os.tmpdir(), 'threadforge-adapter-package-smoke-'));
let launcherVerified = false;
try {
  const archive = spawnSync(process.execPath, [npmCli, 'pack', '--json', '--pack-destination', tempRoot], {
    cwd: packageRoot,
    encoding: 'utf8',
    timeout: 120_000,
    maxBuffer: 8 << 20,
    env: { ...process.env, NPM_CONFIG_UPDATE_NOTIFIER: 'false' },
  });
  if (archive.error || archive.status !== 0) {
    throw new Error(`npm pack failed: ${(archive.stderr || archive.stdout).trim().slice(0, 2000)}`);
  }
  const archiveInfo = JSON.parse(archive.stdout);
  assert.ok(Array.isArray(archiveInfo) && archiveInfo.length === 1, 'expected one packed adapter archive');
  const archivePath = path.join(tempRoot, archiveInfo[0].filename);
  const installRoot = path.join(tempRoot, 'install');
  mkdirSync(installRoot);
  const install = spawnSync(process.execPath, [
    npmCli, 'install', '--offline', '--ignore-scripts', '--no-audit', '--no-fund', '--no-save',
    '--package-lock=false', '--prefix', installRoot, archivePath,
  ], {
    cwd: tempRoot,
    encoding: 'utf8',
    timeout: 120_000,
    maxBuffer: 8 << 20,
    env: { ...process.env, NPM_CONFIG_UPDATE_NOTIFIER: 'false' },
  });
  if (install.error || install.status !== 0) {
    throw new Error(`offline package install failed: ${(install.stderr || install.stdout).trim().slice(0, 2000)}`);
  }

  const installedRoot = path.join(installRoot, 'node_modules', '@threadforge', 'adapter-contracts');
  const installedManifest = JSON.parse(readFileSync(path.join(installedRoot, 'package.json'), 'utf8'));
  assert.deepEqual(installedManifest.bin, manifest.bin, 'installed package lost its command mapping');
  const launch = spawnSync(process.execPath, [npmCli, 'exec', '--offline', '--prefix', installRoot, '--', 'threadforge-adapter'], {
    cwd: tempRoot,
    encoding: 'utf8',
    timeout: 30_000,
    maxBuffer: 2 << 20,
    env: { ...process.env, NPM_CONFIG_UPDATE_NOTIFIER: 'false' },
  });
  assert.equal(launch.status, 2, `packaged CLI should print usage without args, got ${launch.status}: ${launch.stderr}`);
  assert.match(launch.stderr, /Usage:/, 'installed threadforge-adapter did not reach the CLI');
  assert.match(launch.stderr, /openhands hook/, 'installed CLI usage omitted the documented OpenHands command');
  launcherVerified = true;
} finally {
  rmSync(tempRoot, { recursive: true, force: true });
}

console.log(JSON.stringify({
  schema_version: 'threadforge.adapter-package-smoke.v1',
  package_name: packed[0].name,
  file_count: files.length,
  export_targets_verified: Object.keys(manifest.exports).length,
  unpacked_bytes: packed[0].unpackedSize,
  private: manifest.private === true,
  portable_bin_verified: launcherVerified,
}));
