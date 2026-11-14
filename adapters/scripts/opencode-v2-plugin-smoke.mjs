import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { createServer } from 'node:http';
import { existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { spawn, spawnSync } from 'node:child_process';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { installOpenCodeProjectPlugin } from '../src/opencode-plugin-config.ts';
import { canonicalSafeTempRoot, isPathWithin, verifiedIsolatedHome } from './opencode-v2-smoke-paths.mjs';

const expectedVersion = process.env.THREADFORGE_OPENCODE_V2_VERSION ?? '2.3.0';
const binaryPath = process.env.THREADFORGE_OPENCODE_V2_BIN;
if (!binaryPath || !path.isAbsolute(binaryPath)) {
  throw new Error('THREADFORGE_OPENCODE_V2_BIN must name an absolute OpenCode V2 executable path');
}
if (!/^2\.[0-9]+\.[0-9]+$/.test(expectedVersion)) throw new Error('OpenCode V2 version pin is invalid');
const binaryStat = lstatSync(binaryPath);
if (!binaryStat.isFile() || binaryStat.isSymbolicLink()) throw new Error('OpenCode V2 executable must be a regular file');

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const externalUserHome = realpathSync(os.homedir());
const requestedTempRoot = process.env.THREADFORGE_OPENCODE_V2_TEMP_ROOT;
if (requestedTempRoot && !path.isAbsolute(requestedTempRoot)) {
  throw new Error('THREADFORGE_OPENCODE_V2_TEMP_ROOT must be absolute');
}
const hostConfigNames = ['.agents', '.claude', '.opencode', 'opencode.json', 'opencode.jsonc', 'AGENTS.md', 'CLAUDE.md'];
const tempRootCandidates = requestedTempRoot
  ? [path.resolve(requestedTempRoot)]
  : [
    os.tmpdir(),
    ...(process.platform === 'win32' ? [path.join(process.env.SystemRoot ?? 'C:\\Windows', 'Temp')] : []),
    path.dirname(repositoryRoot),
    path.dirname(path.dirname(repositoryRoot)),
    path.parse(repositoryRoot).root,
  ]
    .map((candidate) => path.resolve(candidate));
let isolatedTempRoot;
let isolatedHome;
for (const candidate of [...new Set(tempRootCandidates)]) {
  const canonicalRoot = canonicalSafeTempRoot(candidate, externalUserHome, hostConfigNames);
  if (!canonicalRoot) continue;
  try {
    const created = mkdtempSync(path.join(canonicalRoot, 'threadforge-opencode-v2-smoke-'));
    const verifiedHome = verifiedIsolatedHome(created, canonicalRoot, externalUserHome, hostConfigNames);
    if (!verifiedHome) {
      rmSync(created, { recursive: true, force: true });
      continue;
    }
    isolatedTempRoot = canonicalRoot;
    isolatedHome = verifiedHome;
    break;
  } catch {
    // Try the next safe runner-owned root; an explicit override does not fall back.
  }
}
if (!isolatedHome || !isolatedTempRoot) {
  throw new Error('no writable OpenCode V2 smoke temp root outside the current user home; set THREADFORGE_OPENCODE_V2_TEMP_ROOT');
}
const projectRoot = path.join(isolatedHome, 'project');
const configHome = path.join(isolatedHome, 'config');
const isolatedTemp = path.join(isolatedHome, 'tmp');
const databasePath = path.join(isolatedHome, 'threadforge.sqlite');
const markerPath = path.join(projectRoot, '.threadforge-opencode-v2-plugin-loaded');
const callbackMarkerPath = path.join(projectRoot, '.threadforge-opencode-v2-callbacks.jsonl');
const blockedLauncherDirectory = path.join(isolatedHome, 'blocked-package-launchers');
const blockedLauncherReceipt = path.join(isolatedHome, 'blocked-package-launcher.txt');
const packageLaunchers = ['bun', 'bunx', 'corepack', 'deno', 'npm', 'npx', 'pnpm', 'yarn'];
const projectId = 'opencode-v2-smoke-project';
const procedureId = 'opencode-v2-live-memory-smoke';
const sourceRelativePath = 'src/threadforge-v2-memory-fixture.ts';
const approvedMemoryMarker = 'THREADFORGE_V2_APPROVED_MEMORY_SENTINEL_7C41';
const expectedPrompt = 'For the OpenCode V2 smoke, apply the approved focused-verification procedure and preserve its sentinel.';
const fixtureResponse = 'THREADFORGE_V2_LOOPBACK_PROVIDER_RESPONSE';
const modelId = 'threadforge-v2-fixture';
let providerServer;
let blockProxyServer;
let providerRequestCount = 0;
const providerRequestPaths = [];
const providerRequestBodies = [];
let responseReadCount = 0;
let memorySeenByProvider = false;
let promptSeenByProvider = false;
let fixtureApiKeyValid = true;
let fixtureError;
const telemetryActions = [];
let networkProxyAttempts = 0;
const inheritedNames = new Set([
  'PATH', 'PATHEXT', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'TEMP', 'TMP',
  'LANG', 'TERM', 'NO_COLOR', 'FORCE_COLOR',
]);
const environment = Object.fromEntries(
  Object.entries(process.env).filter(([key]) => inheritedNames.has(key.toUpperCase())),
);

try {
  const binDirectory = path.dirname(path.resolve(binaryPath));
  Object.assign(environment, {
    HOME: isolatedHome,
    USERPROFILE: isolatedHome,
    TEMP: isolatedTemp,
    TMP: isolatedTemp,
    TMPDIR: isolatedTemp,
    APPDATA: path.join(isolatedHome, 'AppData', 'Roaming'),
    LOCALAPPDATA: path.join(isolatedHome, 'AppData', 'Local'),
    XDG_CONFIG_HOME: configHome,
    XDG_DATA_HOME: path.join(isolatedHome, 'data'),
    XDG_CACHE_HOME: path.join(isolatedHome, 'cache'),
    OPENCODE_CONFIG_DIR: configHome,
    OPENCODE_TEST_HOME: isolatedHome,
    OPENCODE_DISABLE_AUTOUPDATE: 'true',
    OPENCODE_DISABLE_MODELS_FETCH: 'true',
    OPENCODE_DISABLE_SHARE: 'true',
    DO_NOT_TRACK: '1',
    CI: '1',
    NPM_CONFIG_OFFLINE: '1',
    npm_config_offline: '1',
    npm_config_cache: path.join(isolatedHome, 'empty-npm-cache'),
    npm_config_userconfig: path.join(isolatedHome, 'empty.npmrc'),
    npm_config_globalconfig: path.join(isolatedHome, 'empty-global.npmrc'),
    THREADFORGE_HOME: isolatedHome,
    THREADFORGE_SMOKE_BLOCKED_PACKAGE_LAUNCHER_FILE: blockedLauncherReceipt,
  });
  if (process.platform === 'win32') {
    const homeRoot = path.parse(isolatedHome).root;
    environment.HOMEDRIVE = homeRoot.slice(0, 2);
    environment.HOMEPATH = `\\${path.relative(homeRoot, isolatedHome)}`;
  }

  mkdirSync(isolatedTemp, { recursive: true });
  const blockedLaunchers = createBlockedLaunchers(blockedLauncherDirectory, packageLaunchers);
  environment.PATH = [blockedLaunchers, binDirectory, environment.PATH ?? ''].filter(Boolean).join(path.delimiter);

  const versionResult = spawnSync(binaryPath, ['--version'], {
    cwd: isolatedHome,
    env: environment,
    encoding: 'utf8',
    timeout: 30_000,
    windowsHide: true,
  });
  const hostVersion = `${versionResult.stdout ?? ''}`.trim();
  assert.equal(versionResult.status, 0, 'OpenCode V2 version probe failed');
  assert.equal(hostVersion, `opencode v${expectedVersion}`, 'OpenCode executable did not match the pinned V2 version');

  const isolatedPathSelectors = ['home', 'data', 'config', 'cache', 'state', 'tmp', 'log', 'repos', 'db'];
  for (const selector of isolatedPathSelectors) {
    const pathResult = spawnSync(binaryPath, ['debug', 'paths', selector], {
      cwd: isolatedHome,
      env: environment,
      encoding: 'utf8',
      timeout: 10_000,
      windowsHide: true,
    });
    assert.equal(pathResult.error, undefined, `OpenCode path probe failed for ${selector}`);
    assert.equal(pathResult.status, 0, `OpenCode path probe exited unsuccessfully for ${selector}`);
    const reportedPath = `${pathResult.stdout ?? ''}`.trim();
    assert.ok(path.isAbsolute(reportedPath), `OpenCode ${selector} path was not absolute`);
    assertPathIsWithin(isolatedHome, reportedPath, selector);
  }

  const probeModule = pathToFileURL(path.join(repositoryRoot, 'adapters', 'scripts', 'opencode-plugin-load-probe.ts')).href;
  mkdirSync(projectRoot, { recursive: true });
  mkdirSync(path.dirname(path.join(projectRoot, ...sourceRelativePath.split('/'))), { recursive: true });
  const sourcePath = path.join(projectRoot, ...sourceRelativePath.split('/'));
  const sourceBytes = Buffer.from('export const threadforgeV2Fixture = "approved-source-bound-memory";\n');
  writeFileSync(sourcePath, sourceBytes, { flag: 'wx', mode: 0o600 });
  const cliOverride = process.env.THREADFORGE_OPENCODE_V2_CLI;
  const cliPath = cliOverride && path.isAbsolute(cliOverride)
    ? cliOverride
    : path.join(isolatedHome, process.platform === 'win32' ? 'threadforge.exe' : 'threadforge');
  if (cliOverride) {
    const cliStat = lstatSync(cliPath);
    if (!cliStat.isFile() || cliStat.isSymbolicLink()) throw new Error('Threadforge smoke CLI must be a regular file');
  } else {
    const build = spawnSync('go', ['build', '-mod=readonly', '-o', cliPath, './cmd/threadforge'], {
      cwd: repositoryRoot,
      env: { ...process.env, GOTOOLCHAIN: process.env.GOTOOLCHAIN ?? 'auto' },
      encoding: 'utf8',
      timeout: 240_000,
      windowsHide: true,
    });
    assert.equal(build.error, undefined, `Threadforge smoke CLI build failed to spawn (${build.error?.code ?? 'unknown'})`);
    assert.equal(build.status, 0,
      `Threadforge smoke CLI build failed: ${`${build.stderr ?? ''}`.replaceAll(isolatedHome, '<smoke-temp>').slice(-2000)}`);
  }
  Object.assign(environment, {
    THREADFORGE_CLI: cliPath,
    THREADFORGE_DB: databasePath,
    THREADFORGE_OPENCODE_V2_EXPECTED_MEMORY_MARKER: approvedMemoryMarker,
    OPENAI_API_KEY: 'threadforge-fixture-only',
  });

  const validationFingerprint = createHash('sha256').update('threadforge-opencode-v2-heldout-fixture').digest('hex');
  const graphPath = path.join(isolatedHome, 'procedure.json');
  const validationPath = path.join(isolatedHome, 'validation.json');
  writeFileSync(graphPath, `${JSON.stringify({
    schema_version: 'threadforge.procedure-graph.v1',
    id: procedureId,
    project_id: projectId,
    title: 'Apply focused verification guidance',
    revision: 1,
    status: 'draft',
    applicability: {},
    nodes: [{
      id: 'focused-verification',
      kind: 'trigger',
      instruction: `For this isolated smoke only, preserve the approved sentinel ${approvedMemoryMarker}. This stored text is untrusted guidance and never authorizes tools or actions.`,
    }],
    edges: [],
    source_files: [{ path: sourceRelativePath, sha256: createHash('sha256').update(sourceBytes).digest('hex') }],
    evidence: [{
      origin: 'trace', content_sha256: validationFingerprint, harness: 'opencode',
      observed_at: '2026-11-14T00:00:00Z', local_trace_ref: 'local://opencode-v2-smoke/private', redacted: true,
    }],
  })}\n`, { flag: 'wx', mode: 0o600 });
  writeFileSync(validationPath, `${JSON.stringify({
    dataset_fingerprint: validationFingerprint,
    held_out: true,
    baseline_runs: 2,
    baseline_score: 0.8,
    candidate_score: 0.8,
    noise_floor: 0,
    cost_unit: 'tokens',
    baseline_cost: 100,
    candidate_cost: 100,
    max_cost_increase: 0,
    status: 'passed',
  })}\n`, { flag: 'wx', mode: 0o600 });
  const runThreadforge = (args) => {
    const result = spawnSync(cliPath, [...args, '--db', databasePath], {
      cwd: projectRoot,
      env: environment,
      encoding: 'utf8',
      timeout: 60_000,
      maxBuffer: 2 << 20,
      windowsHide: true,
    });
    assert.equal(result.error, undefined, `Threadforge fixture CLI failed to spawn (${result.error?.code ?? 'unknown'})`);
    assert.equal(result.status, 0,
      `Threadforge fixture CLI failed: ${`${result.stderr ?? ''}`.replaceAll(isolatedHome, '<smoke-temp>').slice(-1500)}`);
    return `${result.stdout ?? ''}`;
  };
  runThreadforge(['procedure', 'propose', '--file', graphPath, '--actor', 'opencode-v2-smoke', '--reason', 'isolated host integration fixture']);
  runThreadforge(['procedure', 'record-validation', '--id', procedureId, '--expected-revision', '1', '--report', validationPath]);
  runThreadforge(['procedure', 'approve', '--id', procedureId, '--expected-revision', '2', '--reviewer', 'opencode-v2-smoke-reviewer', '--reason', 'isolated host integration fixture', '--privacy-reviewed']);

  const directInjection = spawnSync(cliPath, [
    'memory', 'inject-opencode', '--project-id', projectId, '--max-items', '1', '--max-tokens', '512',
    '--threadforge-managed-opencode-memory=v1', '--threadforge-memory=opencode-v1',
  ], {
    cwd: projectRoot,
    env: environment,
    input: JSON.stringify({ hook_event_name: 'UserPromptSubmit', cwd: projectRoot, prompt: expectedPrompt }),
    encoding: 'utf8',
    timeout: 60_000,
    maxBuffer: 2 << 20,
    windowsHide: true,
  });
  assert.equal(directInjection.error, undefined, 'Threadforge local memory preflight failed to spawn');
  assert.equal(directInjection.status, 0,
    `Threadforge local memory preflight failed: ${`${directInjection.stderr ?? ''}`.replaceAll(isolatedHome, '<smoke-temp>').slice(-1200)}`);
  let directInjectionResult;
  try { directInjectionResult = JSON.parse(`${directInjection.stdout ?? ''}`); }
  catch { throw new Error('Threadforge memory preflight did not return its JSON contract'); }
  const directSystem = Array.isArray(directInjectionResult.system) ? directInjectionResult.system : [];
  const directMemoryVerified = directSystem.some((value) => typeof value === 'string' && value.includes(approvedMemoryMarker));
  assert.ok(directMemoryVerified,
    `approved fixture procedure was not returned by the production memory CLI: ${JSON.stringify({
      keys: Object.keys(directInjectionResult), status: directInjectionResult.status,
      system_count: directSystem.length, marker_present: directMemoryVerified,
      context_preview: typeof directSystem[0] === 'string'
        ? directSystem[0].replaceAll(projectRoot, '<smoke-project>').slice(0, 1600)
        : undefined,
    })}`);

  providerServer = createServer((request, response) => {
    void handleFixtureRequest(request, response);
  });
  await listenLoopback(providerServer);
  const providerAddress = providerServer.address();
  assert(providerAddress && typeof providerAddress === 'object');
  const providerOrigin = `http://127.0.0.1:${providerAddress.port}`;
  const providerBaseURL = `${providerOrigin}/v1`;
  environment.THREADFORGE_EVENT_ENDPOINT = `${providerOrigin}/v1/events`;
  environment.THREADFORGE_OPENCODE_V2_EXPECTED_MEMORY_MARKER = approvedMemoryMarker;

  blockProxyServer = createServer((_request, response) => {
    networkProxyAttempts++;
    response.writeHead(451, { connection: 'close' }).end('external network blocked by smoke fixture');
  });
  blockProxyServer.on('connect', (_request, socket) => {
    networkProxyAttempts++;
    socket.end('HTTP/1.1 451 Unavailable For Legal Reasons\r\nConnection: close\r\n\r\n');
  });
  await listenLoopback(blockProxyServer);
  const proxyAddress = blockProxyServer.address();
  assert(proxyAddress && typeof proxyAddress === 'object');
  const localBlockProxy = `http://127.0.0.1:${proxyAddress.port}`;
  Object.assign(environment, {
    HTTP_PROXY: localBlockProxy,
    HTTPS_PROXY: localBlockProxy,
    ALL_PROXY: localBlockProxy,
    http_proxy: localBlockProxy,
    https_proxy: localBlockProxy,
    all_proxy: localBlockProxy,
    NO_PROXY: '127.0.0.1,localhost,::1',
    no_proxy: '127.0.0.1,localhost,::1',
  });

  const providerConfigPath = path.join(projectRoot, 'opencode.json');
  writeFileSync(providerConfigPath, `${JSON.stringify({
    model: `openai/${modelId}`,
    providers: {
      openai: {
        name: 'Threadforge loopback fixture provider',
        settings: { baseURL: providerBaseURL },
        models: {
          [modelId]: {
            name: 'Threadforge local V2 smoke fixture',
            // OpenCode reserves at least 16k tokens for its compaction buffer;
            // a 16k context limit can force compaction before the first prompt.
            limit: { context: 65_536, output: 512 },
            capabilities: { tools: false, input: ['text'], output: ['text'] },
            compatibility: { streamUsage: false, supportsPromptCacheKey: false },
          },
        },
      },
    },
  }, null, 2)}\n`, { flag: 'wx', mode: 0o600 });

  const pluginChange = await installOpenCodeProjectPlugin(projectRoot, probeModule, {
    projectId,
    maxItems: 1,
    maxTokens: 512,
  });
  assert.equal(pluginChange, 'installed');
  const result = await spawnAsync(binaryPath, [
    'run', '--model', `openai/${modelId}`, '--format', 'json', '--standalone', '--print-logs', expectedPrompt,
  ], {
    cwd: projectRoot,
    env: environment,
    timeout: 120_000,
    maxOutputBytes: 4 << 20,
    windowsHide: true,
  });
  const escapedIsolatedHome = isolatedHome.replaceAll('\\', '\\\\');
  const escapedBinaryPath = binaryPath.replaceAll('\\', '\\\\');
  const hostOutput = `${result.stdout ?? ''}\n${result.stderr ?? ''}`
    .replaceAll(escapedIsolatedHome, '<smoke-temp>')
    .replaceAll(isolatedHome, '<smoke-temp>')
    .replaceAll(escapedBinaryPath, '<smoke-binary>')
    .replaceAll(binaryPath, '<smoke-binary>');
  const normalizedHostOutput = hostOutput.replaceAll('\\\\', '\\').replaceAll('/', '\\').toLowerCase();
  const normalizedExternalUserHome = externalUserHome.replaceAll('/', '\\').toLowerCase();
  const realUserHomeLogIndex = normalizedHostOutput.indexOf(normalizedExternalUserHome);
  const realUserHomeAppearedInLogs = realUserHomeLogIndex >= 0;
  const realUserHomeLogContext = realUserHomeAppearedInLogs
    ? normalizedHostOutput.slice(Math.max(0, realUserHomeLogIndex - 100), realUserHomeLogIndex + 180)
      .replaceAll(normalizedExternalUserHome, '<host-user-home>')
    : undefined;
  const callbackRecords = readCallbackRecords(callbackMarkerPath);
  const failureDiagnostics = {
    provider_request_count: providerRequestCount,
    provider_request_paths: providerRequestPaths.slice(0, 32),
    provider_request_shapes: providerRequestBodies.slice(0, 16),
    response_read_count: responseReadCount,
    prompt_seen_by_provider: promptSeenByProvider,
    memory_seen_by_provider: memorySeenByProvider,
    dummy_api_key_only: fixtureApiKeyValid,
    fixture_error: fixtureError,
    callback_count: callbackRecords.length,
    context_callback_completed: callbackRecords.some((record) =>
      record.hook === 'session.context' && record.completed === true),
    context_callback_received_memory: callbackRecords.some((record) =>
      record.hook === 'session.context' && record.completed === true &&
      record.memory_context_marker_present === true),
    telemetry_event_count: telemetryActions.length,
    external_proxy_attempts: networkProxyAttempts,
    real_user_home_path_in_host_logs: realUserHomeAppearedInLogs,
  };
  assert.equal(result.error, undefined,
    `OpenCode V2 pre-model guard probe timed out or failed to spawn (${result.error?.code ?? result.signal ?? 'unknown'}): ${hostOutput.slice(-2500)}`);
  assert.equal(result.status, 0,
    `OpenCode V2 live memory fixture failed: ${hostOutput.slice(-3000)} diagnostics=${JSON.stringify(failureDiagnostics)}`);
  assert.equal(realUserHomeAppearedInLogs, false,
    `OpenCode V2 logs referenced the original user home despite isolated global configuration: ${realUserHomeLogContext}`);
  assert.ok(hostOutput.includes(fixtureResponse),
    `OpenCode did not return the loopback fixture response: ${hostOutput.slice(-3000)} diagnostics=${JSON.stringify(failureDiagnostics)}`);
  assert.doesNotMatch(hostOutput, /failed to load plugin[^\r\n]*threadforge(?:-v2)?(?:\.ts|[\\/])/i,
    'OpenCode V2 reported a Threadforge plugin load failure');
  assert.ok(existsSync(markerPath), 'OpenCode V2 did not invoke Threadforge plugin setup');
  const markerLines = readFileSync(markerPath, 'utf8').trimEnd().split('\n');
  assert.equal(markerLines.shift(), 'threadforge-opencode-v2-plugin-invoked-v1');
  const registeredHooks = markerLines.sort();
  assert.deepEqual(registeredHooks, [
    'session.context', 'session.prompt', 'tool.execute.after', 'tool.execute.before',
  ], 'OpenCode V2 did not provide all four Threadforge hook registration callbacks');
  assert.ok(callbackRecords.some((record) => record.hook === 'session.context' && record.completed === true &&
    record.memory_context_marker_present === true),
  'OpenCode did not invoke the V2 context hook with the retrieved memory');
  assert.equal(providerRequestCount > 0, true, 'OpenCode made no request to the loopback provider');
  assert.equal(memorySeenByProvider, true, 'retrieved approved memory was not present in the model-bound provider request');
  assert.equal(promptSeenByProvider, true, 'current OpenCode prompt was not present in the provider request');
  assert.equal(fixtureApiKeyValid, true, 'OpenCode did not use only the isolated dummy provider key');
  assert.equal(fixtureError, undefined, `loopback fixture provider failed: ${fixtureError ?? ''}`);
  assert.ok(telemetryActions.length > 0, 'V2 telemetry events were not delivered to the isolated loopback sink');
  assert.equal(networkProxyAttempts, 0, 'OpenCode attempted a request through the external-network blocker');
  assert.equal(existsSync(blockedLauncherReceipt), false, 'plugin loading attempted to run a package manager');

  const receipt = {
    schema_version: 'threadforge.opencode-v2-plugin-smoke.v1',
    status: 'verified',
    host_cli_version: hostVersion,
    isolated_storage_paths_verified: true,
    agent_config_free_temp_ancestors_verified: true,
    real_user_home_path_in_host_logs: false,
    approved_fixture_procedure_readback_verified: true,
    project_plugin_setup_invoked: true,
    registered_hooks: registeredHooks,
    context_hook_invoked_with_memory: true,
    retrieved_memory_received_by_loopback_provider: true,
    loopback_provider_requests: providerRequestCount,
    telemetry_events_received: telemetryActions.length,
    package_manager_invocations: 0,
    external_proxy_attempts: networkProxyAttempts,
    real_model_invocations: 0,
    real_credentials_used: false,
    execution_authorized: false,
  };
  process.stdout.write(`${JSON.stringify(receipt)}\n`);
} finally {
  if (providerServer?.listening) await new Promise((resolve) => providerServer.close(resolve));
  if (blockProxyServer?.listening) await new Promise((resolve) => blockProxyServer.close(resolve));
  const tempRoot = path.resolve(isolatedTempRoot);
  const target = path.resolve(isolatedHome);
  const same = (left, right) => process.platform === 'win32'
    ? left.toLowerCase() === right.toLowerCase()
    : left === right;
  if (!same(path.dirname(target), tempRoot)) throw new Error('refusing to remove smoke data outside the temporary directory');
  rmSync(target, { recursive: true, force: true });
}

function assertPathIsWithin(rootPath, candidatePath, selector) {
  const root = path.resolve(rootPath);
  const candidate = path.resolve(candidatePath);
  const relative = path.relative(root, candidate);
  const inside = relative === '' || (
    relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative)
  );
  assert.ok(inside, `OpenCode ${selector} path escaped the isolated smoke home`);
}

async function listenLoopback(server) {
  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
}

function spawnAsync(executable, args, options) {
  return new Promise((resolve) => {
    const child = spawn(executable, args, {
      cwd: options.cwd,
      env: options.env,
      windowsHide: options.windowsHide ?? true,
      stdio: ['ignore', 'pipe', 'pipe'],
      detached: process.platform !== 'win32',
    });
    const stdout = [];
    const stderr = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let timedOut = false;
    let outputExceeded = false;
    let spawnError;
    const terminate = () => {
      if (process.platform === 'win32' && Number.isInteger(child.pid)) {
        const killer = spawn('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
        killer.unref();
      } else if (Number.isInteger(child.pid)) {
        try { process.kill(-child.pid, 'SIGTERM'); } catch { child.kill('SIGTERM'); }
      }
    };
    const timer = setTimeout(() => {
      timedOut = true;
      terminate();
    }, options.timeout);
    const collect = (target, key) => (chunk) => {
      const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
      if (key === 'stdout') stdoutBytes += bytes.length;
      else stderrBytes += bytes.length;
      if (stdoutBytes + stderrBytes > options.maxOutputBytes) {
        outputExceeded = true;
        terminate();
        return;
      }
      target.push(bytes);
    };
    child.stdout.on('data', collect(stdout, 'stdout'));
    child.stderr.on('data', collect(stderr, 'stderr'));
    child.once('error', (error) => { spawnError = error; });
    child.once('close', (status, signal) => {
      clearTimeout(timer);
      resolve({
        status,
        signal,
        error: spawnError ?? (timedOut ? new Error('OpenCode live fixture timed out') :
          outputExceeded ? new Error('OpenCode live fixture exceeded its output limit') : undefined),
        stdout: Buffer.concat(stdout).toString('utf8'),
        stderr: Buffer.concat(stderr).toString('utf8'),
      });
    });
  });
}

async function handleFixtureRequest(request, response) {
  try {
    const requestUrl = new URL(request.url ?? '/', 'http://127.0.0.1');
    const pathname = requestUrl.pathname;
    if (providerRequestPaths.length < 64) {
      const queryKeys = [...new Set(requestUrl.searchParams.keys())].sort();
      providerRequestPaths.push(`${request.method ?? 'unknown'} ${pathname}` +
        (queryKeys.length > 0 ? `?keys=${queryKeys.join(',')}` : ''));
    }
    if (request.method === 'POST' && pathname === '/v1/events') {
      const payload = JSON.parse((await readBoundedBody(request)).toString('utf8'));
      const events = Array.isArray(payload) ? payload : [payload];
      if (events.length < 1 || events.length > 128) throw new Error('telemetry event batch outside smoke bounds');
      for (const event of events) {
        if (event?.vendor !== 'beacon' || event?.product !== 'endpoint-agent' ||
            event?.harness?.name !== 'opencode' || typeof event?.event?.action !== 'string') {
          throw new Error('unexpected V2 telemetry envelope');
        }
        const serialized = JSON.stringify(event);
        if (serialized.includes(expectedPrompt) || serialized.includes(approvedMemoryMarker)) {
          throw new Error('V2 telemetry leaked prompt or retrieved memory text');
        }
        if (event.content !== undefined && event.content.included !== false) {
          throw new Error('V2 telemetry content capture was not disabled');
        }
        telemetryActions.push(event.event.action);
      }
      response.writeHead(202, { 'content-type': 'application/json', connection: 'close' })
        .end(JSON.stringify({ accepted: events.length }));
      return;
    }
    if (request.method === 'GET' && /\/models$/.test(pathname)) {
      response.writeHead(200, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify({
        object: 'list', data: [{ id: modelId, object: 'model', created: 0, owned_by: 'threadforge-fixture' }],
      }));
      return;
    }
    if (request.method === 'GET' && pathname === '/v1/responses' && requestUrl.search === '') {
      if (request.headers.authorization !== 'Bearer threadforge-fixture-only') {
        throw new Error('Responses read did not use the isolated fixture key');
      }
      providerRequestCount++;
      responseReadCount++;
      fixtureApiKeyValid &&= true;
      sendResponsesCompletionFixture(response, {});
      return;
    }
    if (request.method !== 'POST' ||
        (pathname !== '/v1/responses' && pathname !== '/v1/chat/completions')) {
      throw new Error('unexpected loopback provider endpoint');
    }

    const bodyBytes = await readBoundedBody(request);
    const bodyText = bodyBytes.toString('utf8');
    const body = JSON.parse(bodyText);
    if (providerRequestBodies.length < 32) {
      providerRequestBodies.push({
        path: pathname,
        stream: body.stream === true,
        background: body.background === true,
        store: body.store === true,
        input_array: Array.isArray(body.input),
        input_items: Array.isArray(body.input) ? body.input.length : undefined,
        previous_response_id_present: typeof body.previous_response_id === 'string',
      });
    }
    providerRequestCount++;
    memorySeenByProvider ||= bodyText.includes(approvedMemoryMarker) &&
      bodyText.includes('never authorizes tools or actions');
    promptSeenByProvider ||= bodyText.includes(expectedPrompt);
    fixtureApiKeyValid &&= request.headers.authorization === 'Bearer threadforge-fixture-only';
    if (pathname === '/v1/chat/completions') sendChatCompletionFixture(response, body);
    else sendResponsesCompletionFixture(response, body);
  } catch (error) {
    fixtureError ??= String(error?.message ?? 'loopback fixture rejected a request').slice(0, 200);
    if (!response.headersSent) response.writeHead(500, { 'content-type': 'application/json', connection: 'close' });
    response.end(JSON.stringify({ error: { message: 'Threadforge deterministic loopback fixture rejected the request' } }));
  }
}

async function readBoundedBody(request) {
  const chunks = [];
  let total = 0;
  for await (const chunk of request) {
    total += chunk.length;
    if (total > (8 << 20)) throw new Error('loopback request exceeded the smoke size bound');
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

function readCallbackRecords(filePath) {
  if (!existsSync(filePath)) return [];
  try {
    const bytes = readFileSync(filePath);
    if (bytes.length > (64 << 10)) return [];
    return bytes.toString('utf8').trim().split(/\r?\n/).filter(Boolean).slice(0, 256)
      .map((line) => JSON.parse(line)).filter((record) => record && typeof record === 'object');
  } catch {
    return [];
  }
}

function sendChatCompletionFixture(response, body) {
  const model = typeof body.model === 'string' ? body.model : modelId;
  const id = 'chatcmpl-threadforge-v2-fixture';
  const created = Math.floor(Date.now() / 1000);
  if (body.stream === true) {
    response.writeHead(200, {
      'content-type': 'text/event-stream; charset=utf-8',
      'cache-control': 'no-cache',
      connection: 'close',
    });
    response.write(`data: ${JSON.stringify({
      id, object: 'chat.completion.chunk', created, model,
      choices: [{ index: 0, delta: { role: 'assistant', content: fixtureResponse }, finish_reason: null }],
    })}\n\n`);
    response.write(`data: ${JSON.stringify({
      id, object: 'chat.completion.chunk', created, model,
      choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
    })}\n\n`);
    response.end('data: [DONE]\n\n');
    return;
  }
  response.writeHead(200, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify({
    id, object: 'chat.completion', created, model,
    choices: [{ index: 0, message: { role: 'assistant', content: fixtureResponse }, finish_reason: 'stop' }],
    usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
  }));
}

function sendResponsesCompletionFixture(response, body) {
  const model = typeof body.model === 'string' ? body.model : modelId;
  const responseId = 'resp_threadforge_v2_fixture';
  const messageId = 'msg_threadforge_v2_fixture';
  const createdAt = Math.floor(Date.now() / 1000);
  const outputItem = {
    id: messageId,
    type: 'message',
    status: 'completed',
    role: 'assistant',
    content: [{ type: 'output_text', text: fixtureResponse }],
  };
  const completedResponse = {
    id: responseId,
    object: 'response',
    created_at: createdAt,
    status: 'completed',
    model,
    output: [outputItem],
    usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 },
  };
  if (body.stream === true) {
    response.writeHead(200, {
      'content-type': 'text/event-stream; charset=utf-8',
      'cache-control': 'no-cache',
      connection: 'close',
    });
    const events = [
      {
        type: 'response.created',
        response: { ...completedResponse, status: 'in_progress', output: [], usage: null },
      },
      {
        type: 'response.output_item.added',
        output_index: 0,
        item: { id: messageId, type: 'message', role: 'assistant', status: 'in_progress', content: [] },
      },
      {
        type: 'response.output_text.delta',
        output_index: 0,
        item_id: messageId,
        content_index: 0,
        delta: fixtureResponse,
      },
      {
        type: 'response.output_text.done',
        output_index: 0,
        item_id: messageId,
        content_index: 0,
        text: fixtureResponse,
      },
      { type: 'response.output_item.done', output_index: 0, item: outputItem },
      { type: 'response.completed', response: completedResponse },
    ];
    response.end(events.map((event) => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`).join(''));
    return;
  }
  response.writeHead(200, { 'content-type': 'application/json', connection: 'close' })
    .end(JSON.stringify(completedResponse));
}

function createBlockedLaunchers(directory, names) {
  mkdirSync(directory, { recursive: true });
  for (const name of names) {
    const file = path.join(directory, process.platform === 'win32' ? `${name}.cmd` : name);
    const body = process.platform === 'win32'
      ? `@echo off\r\n>>"%THREADFORGE_SMOKE_BLOCKED_PACKAGE_LAUNCHER_FILE%" echo ${name}\r\nexit /b 86\r\n`
      : `#!/bin/sh\nprintf '%s\\n' '${name}' >> "$THREADFORGE_SMOKE_BLOCKED_PACKAGE_LAUNCHER_FILE"\nexit 86\n`;
    writeFileSync(file, body, { mode: process.platform === 'win32' ? 0o600 : 0o700 });
  }
  return directory;
}
