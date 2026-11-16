import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const modRoot = path.join(packageRoot, 'command-code-mod');
const repositoryRoot = path.resolve(packageRoot, '..');
const packageManifest = JSON.parse(readFileSync(path.join(packageRoot, 'package.json'), 'utf8'));
const modManifest = JSON.parse(readFileSync(path.join(modRoot, 'package.json'), 'utf8'));
assert.equal(modManifest.license, 'Apache-2.0', 'Command Code mod must declare the project license');
assert.equal(modManifest.version, packageManifest.version, 'Command Code mod version must match the adapter package');
for (const filePath of ['LICENSE', 'NOTICE', 'THIRD_PARTY.md']) {
  const modText = readFileSync(path.join(modRoot, filePath), 'utf8').replace(/\r\n/g, '\n').replace(/\n+$/, '');
  const rootText = readFileSync(path.join(repositoryRoot, filePath), 'utf8').replace(/\r\n/g, '\n').replace(/\n+$/, '');
  assert.equal(modText, rootText, `Command Code mod must retain synchronized legal file ${filePath}`);
}
const tempRoot = mkdtempSync(path.join(os.tmpdir(), 'threadforge-command-code-mod-smoke-'));
const projectRoot = path.join(tempRoot, 'project');
const homeRoot = path.join(tempRoot, 'home');
const tempRootReal = realpathSync(tempRoot);
const allowedTempRoot = realpathSync(os.tmpdir());
const relativeTemp = path.relative(allowedTempRoot, tempRootReal);
assert.ok(relativeTemp && !relativeTemp.startsWith('..') && !path.isAbsolute(relativeTemp),
  'isolated smoke directory must be contained by the OS temp directory');
assert.ok(path.basename(tempRootReal).startsWith('threadforge-command-code-mod-smoke-'),
  'isolated smoke directory must have the expected generated prefix');

function cleanupSmokeRoot() {
  const resolved = realpathSync(tempRoot);
  const relative = path.relative(allowedTempRoot, resolved);
  if (resolved !== tempRootReal || !relative || relative.startsWith('..') || path.isAbsolute(relative) ||
      !path.basename(resolved).startsWith('threadforge-command-code-mod-smoke-')) {
    throw new Error('Refusing cleanup because the generated smoke directory failed path verification');
  }
  rmSync(resolved, { recursive: true, force: true });
}

const inheritedNames = new Set(['PATH', 'PATHEXT', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'TEMP', 'TMP', 'LANG', 'TERM', 'NO_COLOR']);
const env = Object.fromEntries(Object.entries(process.env)
  .filter(([key]) => inheritedNames.has(key.toUpperCase())));
Object.assign(env, {
  HOME: homeRoot,
  USERPROFILE: homeRoot,
  APPDATA: path.join(homeRoot, 'AppData', 'Roaming'),
  LOCALAPPDATA: path.join(homeRoot, 'AppData', 'Local'),
  XDG_CONFIG_HOME: path.join(homeRoot, '.config'),
  XDG_DATA_HOME: path.join(homeRoot, '.local', 'share'),
  XDG_CACHE_HOME: path.join(homeRoot, '.cache'),
  CMD_LOCAL_ONLY: '1',
  npm_config_offline: 'true',
  NPM_CONFIG_OFFLINE: 'true',
  COREPACK_ENABLE_NETWORK: '0',
  PIP_NO_INDEX: '1',
  UV_OFFLINE: '1',
});

mkdirSync(projectRoot, { recursive: true });
mkdirSync(homeRoot, { recursive: true });
writeFileSync(path.join(homeRoot, '.npmrc'), 'offline=true\n', { mode: 0o600 });
writeFileSync(path.join(homeRoot, '.gitconfig'), '[init]\n  defaultBranch = main\n', { mode: 0o600 });

function run(command, args, { cwd = projectRoot, timeout = 30_000 } = {}) {
  const result = spawnSync(command, args, { cwd, env, encoding: 'utf8', timeout, maxBuffer: 2 << 20 });
  if (result.error || result.status !== 0) {
    const detail = (result.stderr || result.stdout || result.error?.message || '').trim().slice(0, 1200);
    throw new Error(`${path.basename(command)} exited ${result.status ?? 'unavailable'}: ${detail}`);
  }
  return result.stdout.trim();
}

function resolveCommandCode() {
  if (process.platform === 'win32') {
    const located = spawnSync('where.exe', ['cmdc.ps1'], { env, encoding: 'utf8', timeout: 10_000, maxBuffer: 64 << 10 });
    const shim = located.status === 0 ? located.stdout.split(/\r?\n/).map((entry) => entry.trim()).find(Boolean) : undefined;
    if (!shim) return undefined;
    const entrypoint = path.join(path.dirname(shim), 'node_modules', 'command-code', 'dist', 'index.mjs');
    return existsSync(entrypoint) ? { executable: process.execPath, prefix: [entrypoint] } : undefined;
  }
  const located = spawnSync('which', ['cmdc'], { env, encoding: 'utf8', timeout: 10_000, maxBuffer: 64 << 10 });
  const executable = located.status === 0 ? located.stdout.trim().split(/\r?\n/)[0] : undefined;
  return executable ? { executable, prefix: [] } : undefined;
}

const commandCode = resolveCommandCode();
if (!commandCode) {
  cleanupSmokeRoot();
  console.log(JSON.stringify({
    schema_version: 'threadforge.command-code-mod-host-smoke.v1',
    status: 'skipped',
    reason: 'installed_cmdc_cli_not_found',
    agent_run_attempted: false,
    real_model_invocations: 0,
  }));
} else {
  const invoke = (args) => run(commandCode.executable, [...commandCode.prefix, '--no-auto-update', ...args]);
  try {
    run('git', ['init', '--quiet'], { cwd: projectRoot });

    const version = invoke(['--version']).replace(/^v/, '');
    const addOutput = invoke(['mods', 'add', modRoot]);
    assert.match(addOutput, /Installed local:/i, 'Command Code did not accept the local mod source');

    const settingsPath = path.join(projectRoot, '.commandcode', 'settings.json');
    const settingsAfterAdd = JSON.parse(readFileSync(settingsPath, 'utf8'));
    assert.ok(settingsAfterAdd.mods?.sources?.includes(modRoot), 'project settings did not record the Threadforge mod source');

    const inspectOutput = invoke(['mods', 'inspect', modRoot]);
    assert.match(inspectOutput, /registers:\s+event subscription/i,
      'installed CLI static inspection did not detect the observer event registration');
    assert.match(inspectOutput, /hooks:\s+none/i,
      'installed CLI inspection found a behavior-changing hook in the telemetry mod');

    const removeOutput = invoke(['mods', 'remove', modRoot]);
    assert.match(removeOutput, /Removed local:/i, 'Command Code did not remove the project-scoped mod source');
    const settingsAfterRemove = JSON.parse(readFileSync(settingsPath, 'utf8'));
    assert.deepEqual(settingsAfterRemove, {}, 'mod removal did not clear only the now-empty Threadforge mod settings');

    console.log(JSON.stringify({
      schema_version: 'threadforge.command-code-mod-host-smoke.v1',
      status: 'verified',
      host: 'command-code',
      version,
      project_scope: true,
      isolated_home: true,
      local_mod_source_recorded: true,
      inspector_detected_event_subscription: true,
      inspector_detected_behavior_hooks: false,
      source_removed_from_project_settings: true,
      agent_run_attempted: false,
      provider_requests: 0,
      real_model_invocations: 0,
    }));
  } finally {
    cleanupSmokeRoot();
  }
}
