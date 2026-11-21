import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

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
const lock = JSON.parse(readFileSync(path.join(packageRoot, 'package-lock.json'), 'utf8'));
assert.equal(lock.name, manifest.name, 'adapter package lock name is stale');
assert.equal(lock.version, manifest.version, 'adapter package lock version is stale');
assert.equal(lock.packages?.['']?.name, manifest.name, 'adapter package lock root name is stale');
assert.equal(lock.packages?.['']?.version, manifest.version, 'adapter package lock root version is stale');
assert.equal(lock.packages?.['']?.license, manifest.license, 'adapter package lock license is stale');
const files = packed[0].files;
assert.ok(Array.isArray(files), 'npm did not return package file inventory');
const paths = new Set(files.map((file) => file.path));
const repositoryRoot = path.resolve(packageRoot, '..');
const legalNoticeFiles = ['LICENSE', 'NOTICE', 'THIRD_PARTY.md'];
const nestedPackages = [
  { directory: 'openclaw-plugin', name: '@threadforge/openclaw-observer' },
  { directory: 'command-code-mod', name: 'threadforge-command-code-telemetry' },
];
function normalizedText(filePath) {
  return readFileSync(filePath, 'utf8').replace(/\r\n/g, '\n').replace(/\n+$/, '') + '\n';
}
assert.equal(manifest.license, 'Apache-2.0', 'adapter package license metadata must match the project license');
for (const filePath of legalNoticeFiles) {
  assert.ok(paths.has(filePath), `project license/notice file is missing from the adapter package: ${filePath}`);
  assert.equal(
    normalizedText(path.join(packageRoot, filePath)),
    normalizedText(path.join(repositoryRoot, filePath)),
    `adapter package legal notice drifted from the repository root: ${filePath}`,
  );
}
for (const nested of nestedPackages) {
  const nestedManifest = JSON.parse(readFileSync(path.join(packageRoot, nested.directory, 'package.json'), 'utf8'));
  assert.equal(nestedManifest.name, nested.name, `nested package identity changed: ${nested.directory}`);
  assert.equal(nestedManifest.version, manifest.version, `nested package version drifted: ${nested.directory}`);
  assert.equal(nestedManifest.license, 'Apache-2.0', `nested package license is missing: ${nested.directory}`);
  for (const filePath of legalNoticeFiles) {
    const packagePath = `${nested.directory}/${filePath}`;
    assert.ok(paths.has(packagePath), `nested package legal file is missing: ${packagePath}`);
    assert.equal(
      normalizedText(path.join(packageRoot, nested.directory, filePath)),
      normalizedText(path.join(repositoryRoot, filePath)),
      `nested package legal file drifted: ${packagePath}`,
    );
  }
}
for (const modFile of [
  'command-code-mod/package.json',
  'command-code-mod/index.ts',
  'command-code-mod/telemetry.ts',
]) {
  assert.ok(paths.has(modFile), `packaged Command Code mod file is missing: ${modFile}`);
}
const bundledDependencies = new Set(manifest.bundleDependencies ?? []);
for (const dependency of bundledDependencies) {
  assert.ok(manifest.dependencies?.[dependency], `bundled dependency is not declared as a runtime dependency: ${dependency}`);
  assert.ok(paths.has(`node_modules/${dependency}/package.json`), `bundled dependency manifest is missing: ${dependency}`);
  assert.ok(paths.has(`node_modules/${dependency}/LICENSE`), `bundled dependency license is missing: ${dependency}`);
}
for (const target of Object.values(manifest.exports)) {
  const packagePath = target.replace(/^\.\//, '');
  assert.ok(paths.has(packagePath), `package export target is missing: ${target}`);
  if (packagePath.startsWith('dist/') && packagePath.endsWith('.js')) {
    assert.ok(paths.has(packagePath.slice(0, -3) + '.d.ts'), `package export declaration is missing: ${target}`);
  }
}
for (const filePath of paths) {
  const isBundledDependency = [...bundledDependencies].some((dependency) => filePath.startsWith(`node_modules/${dependency}/`));
  assert.equal(/(^|\/)(browser-extension|test|scripts)(\/|$)/.test(filePath) ||
      (/(^|\/)node_modules(\/|$)/.test(filePath) && !isBundledDependency), false,
    `development or build output leaked into adapter package: ${filePath}`);
}
assert.ok(files.length <= 600, `adapter package unexpectedly contains ${files.length} files`);

assert.deepEqual(manifest.bin, { 'threadforge-adapter': './dist/bin.js' },
  'the portable adapter command must have one explicit package entry point');
assert.ok(paths.has('dist/bin.js'), 'compiled bin entry point is missing from the package');
assert.ok(paths.has('dist/senpi-extension.ts'), 'Senpi extension template is missing from the package');
assert.equal(
  normalizedText(path.join(packageRoot, 'dist', 'senpi-extension.ts')),
  normalizedText(path.join(packageRoot, 'src', 'senpi-extension.ts')),
  'packaged Senpi extension template differs from its maintained source',
);
const tempRoot = mkdtempSync(path.join(os.tmpdir(), 'threadforge-adapter-package-smoke-'));
let launcherVerified = false;
let senpiInstalledTemplateVerified = false;
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
  assert.equal(installedManifest.license, 'Apache-2.0', 'installed adapter lost its Apache-2.0 license metadata');
  for (const filePath of legalNoticeFiles) {
    assert.equal(
      normalizedText(path.join(installedRoot, filePath)),
      normalizedText(path.join(repositoryRoot, filePath)),
      `installed adapter package lost or changed its legal notice: ${filePath}`,
    );
  }
  for (const nested of nestedPackages) {
    const installedNested = path.join(installedRoot, nested.directory);
    const nestedManifest = JSON.parse(readFileSync(path.join(installedNested, 'package.json'), 'utf8'));
    assert.equal(nestedManifest.version, manifest.version, `installed nested package version drifted: ${nested.directory}`);
    assert.equal(nestedManifest.license, 'Apache-2.0', `installed nested package license is missing: ${nested.directory}`);
    for (const filePath of legalNoticeFiles) {
      assert.equal(
        normalizedText(path.join(installedNested, filePath)),
        normalizedText(path.join(repositoryRoot, filePath)),
        `installed nested package legal file drifted: ${nested.directory}/${filePath}`,
      );
    }
  }
  for (const modFile of [
    'command-code-mod/package.json',
    'command-code-mod/index.ts',
    'command-code-mod/telemetry.ts',
  ]) {
    assert.ok(readFileSync(path.join(installedRoot, modFile), 'utf8').length > 0,
      `installed Command Code mod file is empty: ${modFile}`);
  }
  const exportedSpecifiers = Object.keys(manifest.exports).map((subpath) => `${manifest.name}/${subpath.slice(2)}`);
  const importAllExports = spawnSync(process.execPath, [
    '--input-type=module', '--eval',
    `await Promise.all(${JSON.stringify(exportedSpecifiers)}.map((specifier) => import(specifier)));`,
  ], {
    cwd: installRoot,
    encoding: 'utf8',
    timeout: 30_000,
    maxBuffer: 2 << 20,
  });
  assert.equal(importAllExports.status, 0,
    `installed package exports failed to import: ${(importAllExports.stderr || importAllExports.stdout).trim().slice(0, 2000)}`);

  const senpiConfig = await import(pathToFileURL(path.join(installedRoot, 'dist', 'senpi-extension-config.js')).href);
  const senpiProject = path.join(tempRoot, 'senpi-project');
  mkdirSync(senpiProject);
  const senpiOptions = { scope: 'project', projectRoot: senpiProject };
  assert.equal(await senpiConfig.installSenpiExtension(senpiOptions), 'installed',
    'installed adapter package could not install the Senpi extension template');
  const senpiExtensionPath = path.join(senpiProject, '.senpi', 'extensions', 'threadforge.ts');
  const senpiExtension = readFileSync(senpiExtensionPath, 'utf8');
  assert.match(senpiExtension, /^\/\/ threadforge-managed-senpi-extension:v1 sha256=[a-f0-9]{64}\n/,
    'installed Senpi extension omitted its ownership digest');
  assert.equal(await senpiConfig.installSenpiExtension(senpiOptions), 'unchanged',
    'installed Senpi extension install was not idempotent');
  assert.equal(await senpiConfig.removeSenpiExtension(senpiOptions), 'removed',
    'installed Senpi extension could not be removed from the disposable project');
  assert.equal(await senpiConfig.getSenpiExtensionState(senpiOptions), 'missing',
    'Senpi extension remained after removal from the disposable project');
  senpiInstalledTemplateVerified = true;

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
  senpi_installed_template_verified: senpiInstalledTemplateVerified,
  importable_exports_verified: Object.keys(manifest.exports).length,
}));
