import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
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

console.log(JSON.stringify({
  schema_version: 'threadforge.adapter-package-smoke.v1',
  package_name: packed[0].name,
  file_count: files.length,
  export_targets_verified: Object.keys(manifest.exports).length,
  unpacked_bytes: packed[0].unpackedSize,
  private: manifest.private === true,
}));
