import assert from 'node:assert/strict';
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const smokeRoot = mkdtempSync(path.join(tmpdir(), 'threadforge-opencode-history-host-'));
const projectRoot = path.join(smokeRoot, 'project');
const statePath = path.join(smokeRoot, 'threadforge', 'opencode-history.json');
const providerID = 'threadforge-local-history';
const modelID = 'fixture-model';
const model = `${providerID}/${modelID}`;
const userMarker = 'threadforge synthetic history prompt 7d93f0';
const assistantMarker = 'threadforge synthetic history response 62a1bc';
const environmentNames = new Set([
  'PATH', 'PATHEXT', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'TEMP', 'TMP',
  'HOMEDRIVE', 'HOMEPATH', 'LANG', 'TERM', 'NO_COLOR', 'FORCE_COLOR',
]);
const isolatedEnvironment = Object.fromEntries(
  Object.entries(process.env).filter(([key]) => environmentNames.has(key.toUpperCase())),
);
Object.assign(isolatedEnvironment, {
  HOME: smokeRoot,
  USERPROFILE: smokeRoot,
  APPDATA: path.join(smokeRoot, 'AppData', 'Roaming'),
  LOCALAPPDATA: path.join(smokeRoot, 'AppData', 'Local'),
  XDG_CONFIG_HOME: path.join(smokeRoot, '.config'),
  XDG_DATA_HOME: path.join(smokeRoot, '.local', 'share'),
  XDG_CACHE_HOME: path.join(smokeRoot, '.cache'),
  OPENCODE_DISABLE_AUTOUPDATE: 'true',
  OPENCODE_DISABLE_LSP_DOWNLOAD: 'true',
});

function run(command, args, { cwd, timeout = 60_000 } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd, env: isolatedEnvironment, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
    const stdout = [];
    const stderr = [];
    let outputBytes = 0;
    let settled = false;
    const timer = setTimeout(() => {
      child.kill();
      finish(new Error(`${path.basename(command)} timed out during the isolated OpenCode history smoke`));
    }, timeout);
    const finish = (error, result) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (error) reject(error);
      else resolve(result);
    };
    for (const [stream, chunks] of [[child.stdout, stdout], [child.stderr, stderr]]) {
      stream.on('data', (chunk) => {
        outputBytes += chunk.length;
        if (outputBytes > 8 << 20) {
          child.kill();
          finish(new Error(`${path.basename(command)} exceeded the smoke output bound`));
          return;
        }
        chunks.push(chunk);
      });
    }
    child.once('error', (error) => finish(new Error(`${path.basename(command)} could not start: ${error.code ?? 'process error'}`)));
    child.once('close', (code) => {
      if (code !== 0) {
        finish(new Error(`${path.basename(command)} exited with status ${code ?? 'unknown'}`));
        return;
      }
      finish(undefined, {
        stdout: Buffer.concat(stdout).toString('utf8'),
        stderr: Buffer.concat(stderr).toString('utf8'),
      });
    });
  });
}

function sendCompletion(response, requestBody) {
  const created = Math.floor(Date.now() / 1000);
  const completionID = `chatcmpl-threadforge-${created}`;
  if (requestBody.stream === true) {
    response.writeHead(200, { 'content-type': 'text/event-stream; charset=utf-8', 'cache-control': 'no-cache', connection: 'close' });
    const chunks = [
      { id: completionID, object: 'chat.completion.chunk', created, model: modelID, choices: [{ index: 0, delta: { role: 'assistant', content: assistantMarker }, finish_reason: null }] },
      { id: completionID, object: 'chat.completion.chunk', created, model: modelID, choices: [{ index: 0, delta: {}, finish_reason: 'stop' }] },
    ];
    for (const chunk of chunks) response.write(`data: ${JSON.stringify(chunk)}\n\n`);
    response.end('data: [DONE]\n\n');
    return;
  }
  response.writeHead(200, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify({
    id: completionID, object: 'chat.completion', created, model: modelID,
    choices: [{ index: 0, message: { role: 'assistant', content: assistantMarker }, finish_reason: 'stop' }],
  }));
}

let providerRequests = 0;
let providerPromptObserved = false;
let providerError;
const provider = createServer((request, response) => {
  const url = new URL(request.url ?? '/', 'http://127.0.0.1');
  if (request.method === 'GET' && /\/models$/.test(url.pathname)) {
    response.writeHead(200, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify({
      object: 'list', data: [{ id: modelID, object: 'model', created: 0, owned_by: 'threadforge-local-fixture' }],
    }));
    return;
  }
  if (request.method !== 'POST' || !/\/chat\/completions$/.test(url.pathname)) {
    providerError ??= 'unexpected local provider route';
    response.writeHead(404, { 'content-type': 'application/json', connection: 'close' }).end('{"error":"not found"}');
    return;
  }
  const chunks = [];
  let requestBytes = 0;
  request.on('data', (chunk) => {
    requestBytes += chunk.length;
    if (requestBytes <= 1 << 20) chunks.push(chunk);
  });
  request.on('end', () => {
    try {
      if (requestBytes > 1 << 20) throw new Error('local fixture request exceeded its byte bound');
      const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
      if (body.model !== modelID) throw new Error('OpenCode selected an unexpected model');
      const messages = Array.isArray(body.messages) ? body.messages : [];
      providerPromptObserved ||= messages.some((message) => JSON.stringify(message).includes(userMarker));
      if (!providerPromptObserved) throw new Error('OpenCode did not send the synthetic user prompt to the loopback fixture');
      providerRequests++;
      sendCompletion(response, body);
    } catch (error) {
      providerError ??= String(error?.message ?? 'local provider fixture failed').slice(0, 160);
      response.writeHead(500, { 'content-type': 'application/json', connection: 'close' })
        .end('{"error":{"message":"local fixture rejected the request"}}');
    }
  });
});

let cleanupError;
try {
  mkdirSync(projectRoot, { recursive: true });
  await new Promise((resolve, reject) => {
    provider.once('error', reject);
    provider.listen(0, '127.0.0.1', resolve);
  });
  const address = provider.address();
  assert(address && typeof address === 'object');
  const endpoint = `http://127.0.0.1:${address.port}/v1`;
  writeFileSync(path.join(projectRoot, 'opencode.json'), `${JSON.stringify({
    '$schema': 'https://opencode.ai/config.json',
    autoupdate: false,
    model,
    plugin: [],
    provider: {
      [providerID]: {
        npm: '@ai-sdk/openai-compatible',
        name: 'Threadforge isolated history smoke provider',
        options: { baseURL: endpoint, apiKey: 'local-fixture-only' },
        models: { [modelID]: { name: 'Fixture only', limit: { context: 4096, output: 256 } } },
      },
    },
  }, null, 2)}\n`, { flag: 'wx', mode: 0o600 });

  const versionResult = await run('opencode', ['--version'], { cwd: projectRoot, timeout: 20_000 });
  const hostVersion = versionResult.stdout.trim();
  assert.match(hostVersion, /^\d+\.\d+\.\d+/, 'OpenCode did not report a semantic version');
  const promptRun = await run('opencode', [
    'run', '--model', model, '--dir', projectRoot, '--format', 'default', '--pure', userMarker,
  ], { cwd: projectRoot, timeout: 60_000 });
  assert.equal(providerError, undefined, `local provider boundary failed: ${providerError ?? ''}`);
  assert.ok(providerRequests > 0, 'OpenCode made no request to the loopback-only provider');
  assert.equal(providerPromptObserved, true);
  assert.ok(promptRun.stdout.includes(assistantMarker), 'OpenCode did not receive the fixture completion');

  const cliPath = path.join(repositoryRoot, 'adapters', 'src', 'cli.ts');
  const historyRun = await run(process.execPath, [
    '--experimental-strip-types', cliPath, 'opencode', 'sessions', 'sync',
    '--project-root', projectRoot, '--state-path', statePath, '--opencode-cli', 'opencode', '--print',
  ], { cwd: projectRoot, timeout: 90_000 });
  const result = JSON.parse(historyRun.stdout.trim());
  assert.equal(result.schema_version, 'threadforge.opencode-history-sync.v1');
  assert.equal(result.complete, true, 'OpenCode list/export preview was incomplete');
  assert.ok(result.sessions > 0, 'OpenCode did not expose the synthetic session');
  assert.ok(result.events_previewed > 0, 'Threadforge did not map any events from the real OpenCode export');
  assert.equal(result.content_mode, 'metadata');
  const serializedEvents = JSON.stringify(result.events ?? []);
  for (const privateValue of [userMarker, assistantMarker, 'local-fixture-only', path.resolve(projectRoot)]) {
    assert.equal(serializedEvents.includes(privateValue), false, `OpenCode history preview retained ${privateValue}`);
  }
  assert.equal(existsSync(statePath), false, '--print unexpectedly wrote an OpenCode history cursor');
  assert.equal(providerError, undefined, `local provider boundary failed after history export: ${providerError ?? ''}`);

  process.stdout.write(`${JSON.stringify({
    schema_version: 'threadforge.opencode-history-host-smoke.v1',
    status: 'verified', host_version: hostVersion,
    provider: 'loopback-only-fixture', provider_requests: providerRequests,
    real_model_invocations: 0, synthetic_session_count: result.sessions,
    events_previewed: result.events_previewed, preview_complete: result.complete,
    content_mode: result.content_mode, prompt_text_retained: false,
    raw_session_ids_retained: false, cursor_written: false,
  })}\n`);
} catch (error) {
  cleanupError = error;
} finally {
  if (provider.listening) await new Promise((resolve) => provider.close(resolve));
  const target = path.resolve(smokeRoot);
  const allowedRoot = path.resolve(tmpdir()) + path.sep;
  if (!target.startsWith(allowedRoot) || !path.basename(target).startsWith('threadforge-opencode-history-host-')) {
    cleanupError ??= new Error('refusing to remove an OpenCode smoke path outside its dedicated temporary directory');
  } else {
    try { rmSync(target, { recursive: true, force: true }); }
    catch (error) { cleanupError ??= error; }
  }
}
if (cleanupError) {
  process.stderr.write(`${cleanupError?.message ?? 'OpenCode history host smoke failed'}\n`);
  process.exitCode = 1;
}
