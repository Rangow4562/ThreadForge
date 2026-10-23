import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { lstat, mkdir, mkdtemp, readFile, realpath, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { buildClinePluginPackage } from './build-cline-plugin-package.mjs';

const tempPrefix = 'threadforge-cline-plugin-host-smoke-';

export async function runClinePluginHostSmoke(cliPath, expectedVersion) {
  const cli = await validateCliPath(cliPath);
  if (typeof expectedVersion !== 'string' || !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(expectedVersion)) {
    throw new Error('--version must be an exact Cline CLI semantic version');
  }
  const versionResult = run(cli, ['--version']);
  if (versionResult.status !== 0 || versionResult.stdout.trim() !== expectedVersion) {
    throw new Error('installed Cline CLI version does not match --version');
  }

  const root = await mkdtemp(path.join(os.tmpdir(), tempPrefix));
  const configDirectory = path.join(root, 'config');
  const dataDirectory = path.join(root, 'data');
  const projectDirectory = path.join(root, 'project');
  const packageDirectory = path.join(root, 'plugin-package');
  const previousFetch = globalThis.fetch;
  const captured = [];
  try {
    await Promise.all([mkdir(configDirectory), mkdir(dataDirectory), mkdir(projectDirectory)]);
    const built = await buildClinePluginPackage(packageDirectory);
    const install = run(cli, [
      '--config', configDirectory,
      '--data-dir', dataDirectory,
      'plugin', 'install', '--cwd', projectDirectory, '--json', packageDirectory,
    ]);
    if (install.status !== 0) throw new Error('Cline CLI rejected the generated local plugin package');
    let installed;
    try { installed = JSON.parse(install.stdout); }
    catch { throw new Error('Cline CLI plugin install did not return JSON'); }
    if (typeof installed.installPath !== 'string' || !Array.isArray(installed.entryPaths) ||
        !Array.isArray(installed.mcpSyncFailures) || installed.mcpSyncFailures.length !== 0) {
      throw new Error('Cline CLI reported an incomplete plugin install or module-loading failure');
    }
    const projectReal = await realpath(projectDirectory);
    const entryPaths = installed.entryPaths.filter((value) => typeof value === 'string');
    if (entryPaths.length !== 1 || !isInside(projectReal, path.resolve(entryPaths[0]))) {
      throw new Error('Cline CLI installed an unexpected plugin entry layout');
    }
    const entryPath = path.resolve(entryPaths[0]);
    const packageManifest = JSON.parse(await readFile(path.join(path.dirname(entryPath), 'package.json'), 'utf8'));
    if (packageManifest.threadforge?.schema_version !== 'threadforge.cline-plugin-package.v1' ||
        packageManifest.threadforge?.bundle_sha256 !== built.bundle_sha256) {
      throw new Error('installed Cline plugin package digest does not match the generated package');
    }
    await readFile(path.join(path.dirname(entryPath), 'emit.js'));

    globalThis.fetch = async (_input, init) => {
      const payload = JSON.parse(String(init?.body));
      captured.push(...(Array.isArray(payload) ? payload : [payload]));
      return new Response('{}', { status: 202 });
    };
    const loaded = await import(pathToFileURL(entryPath).href);
    const plugin = loaded.default;
    if (plugin?.name !== 'threadforge' || typeof plugin.setup !== 'function' || typeof plugin.hooks?.beforeRun !== 'function') {
      throw new Error('installed Cline plugin entry has an unsupported runtime contract');
    }
    plugin.setup(undefined, { workspaceInfo: { rootPath: 'C:\\private\\smoke-project' } });
    const callbackResult = await plugin.hooks.beforeRun({
      taskId: 'private-smoke-task',
      input: { text: 'private-smoke-prompt-must-not-be-retained' },
    });
    if (callbackResult !== undefined || captured.map((event) => event.event?.action).join(',') !== 'session.started,prompt.submitted') {
      throw new Error('installed Cline adapter callback did not preserve its observational contract');
    }
    const serialized = JSON.stringify(captured);
    for (const privateValue of ['private-smoke-prompt-must-not-be-retained', 'C:\\private\\smoke-project', 'private-smoke-task']) {
      if (serialized.includes(privateValue)) throw new Error('installed Cline adapter retained private fixture content');
    }
    return {
      schema_version: 'threadforge.cline-plugin-host-smoke.v1',
      host: 'cline-cli',
      host_version: expectedVersion,
      package_install_status: 'passed',
      package_entry_import_status: 'passed',
      installed_adapter_callback_status: 'passed',
      host_callback_dispatch_status: 'not_exercised',
      mcp_sync_failure_count: installed.mcpSyncFailures.length,
      model_invocations: 0,
      raw_prompt_retained: false,
      workspace_path_retained: false,
      task_id_retained: false,
    };
  } finally {
    globalThis.fetch = previousFetch;
    await removeOwnedTempRoot(root);
  }
}

function run(executable, args) {
  const result = spawnSync(executable, args, {
    encoding: 'utf8',
    timeout: 120_000,
    maxBuffer: 4 << 20,
    windowsHide: true,
  });
  if (result.error) throw new Error('unable to run the selected Cline CLI binary');
  return result;
}

async function validateCliPath(value) {
  if (typeof value !== 'string' || !path.isAbsolute(value) || /^(?:\\\\|\/\/)/.test(value) ||
      value.split(/[\\/]/).some((part) => part === '.' || part === '..')) {
    throw new Error('--cline-path must identify an absolute local executable path');
  }
  const absolute = path.resolve(value);
  const info = await lstat(absolute).catch(() => null);
  if (!info?.isFile() || isLinked(info)) throw new Error('--cline-path must identify a regular non-linked file');
  return absolute;
}

function isInside(root, candidate) {
  const relative = path.relative(root, candidate);
  return relative !== '' && relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

async function removeOwnedTempRoot(root) {
  const tempRoot = path.resolve(os.tmpdir());
  const absolute = path.resolve(root);
  const relative = path.relative(tempRoot, absolute);
  if (!path.basename(absolute).startsWith(tempPrefix) || !isInside(tempRoot, absolute) || relative.split(path.sep).length !== 1) {
    throw new Error('refusing to remove a Cline smoke directory outside the owned temporary root');
  }
  const info = await lstat(absolute).catch(() => null);
  if (info && (!info.isDirectory() || isLinked(info))) throw new Error('refusing to remove an unsafe Cline smoke directory');
  if (info) await rm(absolute, { recursive: true, force: false });
}

function isLinked(info) {
  return info.isSymbolicLink() || Boolean((info.st_file_attributes ?? 0) & 0x400);
}

function parseArguments(args) {
  const values = new Map();
  for (let i = 0; i < args.length; i += 2) {
    const flag = args[i];
    const value = args[i + 1];
    if (!['--cline-path', '--version'].includes(flag) || typeof value !== 'string' || value.startsWith('--') || values.has(flag)) {
      throw new Error('Usage: npm run test:cline-plugin-host -- --cline-path <absolute-cline-exe> --version <exact-version>');
    }
    values.set(flag, value);
  }
  if (values.size !== 2) throw new Error('Usage: npm run test:cline-plugin-host -- --cline-path <absolute-cline-exe> --version <exact-version>');
  return values;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const args = parseArguments(process.argv.slice(2));
    const result = await runClinePluginHostSmoke(args.get('--cline-path'), args.get('--version'));
    process.stdout.write(`${JSON.stringify(result)}\n`);
  } catch (error) {
    process.stderr.write(`${error instanceof Error ? error.message : 'Cline plugin host smoke failed'}\n`);
    process.exitCode = 1;
  }
}
