import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parse as parseYaml } from 'yaml';

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
function collectCommands(value, output = []) {
  if (Array.isArray(value)) {
    for (const item of value) collectCommands(item, output);
  } else if (value && typeof value === 'object') {
    for (const [key, item] of Object.entries(value)) {
      if (key === 'command' && typeof item === 'string') output.push(item);
      else collectCommands(item, output);
    }
  }
  return output;
}
function quoteShellArgument(value) {
  return process.platform === 'win32' ? `"${value}"` : `'${value.replaceAll("'", "'\\''")}'`;
}
function assertPackagedCLICommand(command, label, cliPath) {
  assert.ok(command.includes(cliPath), `${label} command does not use the packaged CLI entry point`);
  assert.ok(command.includes(quoteShellArgument(process.execPath)), `${label} command does not quote the Node executable`);
  assert.ok(command.includes(quoteShellArgument(cliPath)), `${label} command does not quote the packaged CLI path`);
  assert.equal(command.includes('cli.ts'), false, `${label} command refers to an omitted TypeScript file`);
  assert.equal(command.includes('--experimental-strip-types'), false, `${label} command keeps the source-only Node flag`);
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
for (const runtimeTemplate of ['omp-extension.ts', 'senpi-extension.ts']) {
  assert.ok(paths.has(`dist/${runtimeTemplate}`), `runtime extension template is missing from the package: ${runtimeTemplate}`);
  assert.equal(
    normalizedText(path.join(packageRoot, 'dist', runtimeTemplate)),
    normalizedText(path.join(packageRoot, 'src', runtimeTemplate)),
    `packaged runtime extension template differs from maintained source: ${runtimeTemplate}`,
  );
}
const tempRoot = mkdtempSync(path.join(os.tmpdir(), 'threadforge adapter package smoke-'));
let launcherVerified = false;
let senpiInstalledTemplateVerified = false;
let ompInstalledTemplateVerified = false;
let clineInstalledPluginTargetVerified = false;
let hostHookCommandsResolvePackagedCLI = false;
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

  const packagedCliPath = path.join(installedRoot, 'dist', 'bin.js');
  assert.ok(existsSync(packagedCliPath), 'installed package is missing its CLI bin');
  const cliEntrypoint = await import(pathToFileURL(path.join(installedRoot, 'dist', 'adapter-cli-entry.js')).href);
  for (const moduleFile of ['devin-hooks-config.js', 'devin-desktop-hooks-config.js', 'hermes-hook.js', 'openhands-hooks-config.js']) {
    const entry = cliEntrypoint.resolveAdapterCLIEntry(pathToFileURL(path.join(installedRoot, 'dist', moduleFile)).href);
    assert.equal(path.resolve(entry.scriptPath), path.resolve(packagedCliPath), `${moduleFile} did not resolve the installed package bin`);
    assert.deepEqual(entry.nodeArguments, [], `${moduleFile} retained source-only Node arguments`);
  }

  const clineConfig = await import(pathToFileURL(path.join(installedRoot, 'dist', 'cline-plugin-config.js')).href);
  const clineProject = path.join(tempRoot, 'cline-project');
  mkdirSync(clineProject);
  const clineResult = await clineConfig.installClinePlugin({ scope: 'project', projectRoot: clineProject });
  assert.equal(clineResult.result, 'installed', 'installed package could not create its Cline wrapper');
  const clineWrapperPath = clineConfig.clinePluginPath('project', clineProject);
  const clineWrapper = readFileSync(clineWrapperPath, 'utf8');
  const clineImport = clineWrapper.split(/\r?\n/).find((line) => line.startsWith('import plugin from '));
  assert.ok(clineImport, 'installed Cline wrapper has no plugin module import');
  const clineModuleUrl = JSON.parse(clineImport.slice('import plugin from '.length, -1));
  const clineModulePath = fileURLToPath(new URL(clineModuleUrl));
  assert.ok(existsSync(clineModulePath), 'installed Cline wrapper points to a missing plugin module');
  const expectedClineModulePath = path.join(installedRoot, 'dist', 'cline-plugin.js');
  const clinePathsMatch = process.platform === 'win32'
    ? await (await import(pathToFileURL(path.join(installedRoot, 'dist', 'path-safety.js')).href))
      .isSameRealPathWithoutLinks(clineModulePath, expectedClineModulePath)
    : path.resolve(clineModulePath) === path.resolve(expectedClineModulePath);
  assert.equal(clinePathsMatch, true,
    'installed Cline wrapper does not point to the compiled packaged plugin');
  assert.equal((await clineConfig.uninstallClinePlugin({ scope: 'project', projectRoot: clineProject })).result, 'removed');
  clineInstalledPluginTargetVerified = true;

  const ompConfig = await import(pathToFileURL(path.join(installedRoot, 'dist', 'omp-hooks-config.js')).href);
  const ompProject = path.join(tempRoot, 'omp-project');
  mkdirSync(ompProject);
  const ompOptions = { scope: 'project', projectRoot: ompProject };
  assert.equal(await ompConfig.installOmpExtension(ompOptions), 'installed', 'installed package could not install the OMP template');
  const ompExtensionPath = ompConfig.ompExtensionPath('project', ompProject);
  const ompExtension = readFileSync(ompExtensionPath, 'utf8');
  assert.match(ompExtension, /^\/\/ threadforge-managed-omp-extension:v1 sha256=[a-f0-9]{64}\n/,
    'installed OMP extension omitted its ownership digest');
  assert.equal(await ompConfig.installOmpExtension(ompOptions), 'unchanged', 'installed OMP install was not idempotent');
  assert.equal(await ompConfig.removeOmpExtension(ompOptions), 'removed', 'installed OMP extension could not be removed');
  ompInstalledTemplateVerified = true;

  const devinHooks = await import(pathToFileURL(path.join(installedRoot, 'dist', 'devin-hooks-config.js')).href);
  const devinProject = path.join(tempRoot, 'devin-project');
  mkdirSync(devinProject);
  assert.equal(await devinHooks.installDevinHooks({ scope: 'project', projectRoot: devinProject }), 'installed');
  const devinConfig = JSON.parse(readFileSync(devinHooks.devinHooksConfigPath('project', devinProject), 'utf8'));
  const devinCommands = collectCommands(devinConfig);
  assert.ok(devinCommands.length > 0, 'installed Devin config contains no generated hook commands');
  for (const command of devinCommands) assertPackagedCLICommand(command, 'Devin', packagedCliPath);
  await devinHooks.removeDevinHooks({ scope: 'project', projectRoot: devinProject });

  const desktopHooks = await import(pathToFileURL(path.join(installedRoot, 'dist', 'devin-desktop-hooks-config.js')).href);
  const desktopProject = path.join(tempRoot, 'devin-desktop-project');
  mkdirSync(desktopProject);
  assert.equal(await desktopHooks.installDevinDesktopHooks({ scope: 'project', projectRoot: desktopProject }), 'installed');
  const desktopConfig = JSON.parse(readFileSync(desktopHooks.devinDesktopHooksConfigPath('project', desktopProject), 'utf8'));
  const desktopCommands = collectCommands(desktopConfig);
  assert.ok(desktopCommands.length > 0, 'installed Devin Desktop config contains no generated hook commands');
  for (const command of desktopCommands) assertPackagedCLICommand(command, 'Devin Desktop', packagedCliPath);
  await desktopHooks.removeDevinDesktopHooks({ scope: 'project', projectRoot: desktopProject });

  const openHandsHooks = await import(pathToFileURL(path.join(installedRoot, 'dist', 'openhands-hooks-config.js')).href);
  const openHandsProject = path.join(tempRoot, 'openhands-project');
  mkdirSync(openHandsProject);
  assert.equal(await openHandsHooks.installOpenHandsProjectHooks(openHandsProject), 'installed');
  const openHandsConfig = JSON.parse(readFileSync(path.join(openHandsProject, '.openhands', 'hooks.json'), 'utf8'));
  const openHandsCommands = collectCommands(openHandsConfig);
  assert.ok(openHandsCommands.length > 0, 'installed OpenHands config contains no generated hook commands');
  for (const command of openHandsCommands) assertPackagedCLICommand(command, 'OpenHands', packagedCliPath);
  await openHandsHooks.removeOpenHandsProjectHooks(openHandsProject);

  const hermesHook = await import(pathToFileURL(path.join(installedRoot, 'dist', 'hermes-hook.js')).href);
  const hermesCommands = collectCommands(parseYaml(hermesHook.renderHermesHookConfig()));
  assert.ok(hermesCommands.length > 0, 'installed Hermes config contains no generated hook commands');
  for (const command of hermesCommands) assertPackagedCLICommand(command, 'Hermes', packagedCliPath);
  hostHookCommandsResolvePackagedCLI = true;

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
  omp_installed_template_verified: ompInstalledTemplateVerified,
  cline_installed_plugin_target_verified: clineInstalledPluginTargetVerified,
  host_hook_commands_resolve_packaged_cli: hostHookCommandsResolvePackagedCLI,
  importable_exports_verified: Object.keys(manifest.exports).length,
}));
