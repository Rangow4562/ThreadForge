import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import path from 'node:path';

const fixtureAPIKey = 'threadforge-claude-local-fixture-only';

export async function runClaudePromptMemoryRead({
  projectRoot, settingsPath, claudeConfigDir, threadforgeHome, isolatedEnvironment, expectedGuidance,
  expectedTaskResumeMarkers = [], finalResponse = expectedGuidance, hostVersion = 'unknown',
}) {
  let messageRequests = 0;
  let localHealthChecks = 0;
  let requestsWithApprovedMemory = 0;
  let requestsWithTaskResume = 0;
  let toolsListedToProvider = false;
  let toolUseBlocksObserved = 0;
  let authorizationSafe = true;
  let fixtureError;
  const requestShapes = [];
  const server = createServer((request, response) => {
    const apiKey = request.headers['x-api-key'];
    const authorization = request.headers.authorization;
    if ((apiKey && apiKey !== fixtureAPIKey) || (authorization && authorization !== `Bearer ${fixtureAPIKey}`)) {
      authorizationSafe = false;
    }
    const chunks = [];
    let byteCount = 0;
    request.on('data', (chunk) => {
      byteCount += chunk.length;
      if (byteCount <= 8 << 20) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (byteCount > 8 << 20) throw new Error('Claude fixture request exceeded its local size bound');
        const route = new URL(request.url ?? '/', 'http://127.0.0.1').pathname;
        if (request.method === 'HEAD' && route === '/api/hello') {
          localHealthChecks++;
          response.writeHead(200, { 'content-length': '0', connection: 'close' }).end();
          return;
        }
        if (request.method === 'POST' && route.endsWith('/messages/count_tokens')) {
          writeJSON(response, { input_tokens: 32 });
          return;
        }
        if (request.method !== 'POST' || !route.endsWith('/messages')) {
          if (requestShapes.length < 8) requestShapes.push({ method: request.method, route, unexpected_endpoint: true });
          throw new Error(`Claude requested an unexpected local fixture-provider endpoint (${request.method} ${route})`);
        }
        const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const serializedBody = JSON.stringify(body);
        const observed = serializedBody.includes(expectedGuidance);
        const taskResumeObserved = expectedTaskResumeMarkers.every((marker) => serializedBody.includes(marker));
        toolsListedToProvider ||= Array.isArray(body.tools) && body.tools.length > 0;
        for (const message of Array.isArray(body.messages) ? body.messages : []) {
          for (const block of Array.isArray(message?.content) ? message.content : []) {
            if (typeof block?.type === 'string' && /(?:^|_)tool_(?:use|result)$/.test(block.type)) toolUseBlocksObserved++;
          }
        }
        messageRequests++;
        if (observed) requestsWithApprovedMemory++;
        if (expectedTaskResumeMarkers.length > 0 && taskResumeObserved) requestsWithTaskResume++;
        if (requestShapes.length < 8) {
          requestShapes.push({
            route, body_keys: Object.keys(body).sort(),
            message_count: Array.isArray(body.messages) ? body.messages.length : 0,
            stream: body.stream === true, approved_memory_present: observed,
            task_resume_context_present: expectedTaskResumeMarkers.length > 0 && taskResumeObserved,
            permission_tools_off: Array.isArray(body.tools) && body.tools.length === 0,
          });
        }
        if (!observed) fixtureError = 'Claude API request did not contain the approved project-memory hook result';
        if (expectedTaskResumeMarkers.length > 0 && !taskResumeObserved) {
          fixtureError = 'Claude API request did not contain the explicitly selected task-resume context';
        }
        sendMessage(response, body, finalResponse);
      } catch (error) {
        fixtureError ??= String(error?.message ?? 'Claude deterministic provider fixture failed').slice(0, 256);
        writeJSON(response, { type: 'error', error: { type: 'api_error', message: 'deterministic local Claude fixture failed' } }, 500);
      }
    });
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  assert(address && typeof address === 'object');
  mkdirSync(claudeConfigDir, { recursive: true });
  mkdirSync(threadforgeHome, { recursive: true });

  const environment = {
    ...isolatedEnvironment,
    CLAUDE_CONFIG_DIR: claudeConfigDir,
    THREADFORGE_HOME: threadforgeHome,
    ANTHROPIC_BASE_URL: `http://127.0.0.1:${address.port}`,
    ANTHROPIC_API_KEY: fixtureAPIKey,
  };
  try {
    const prompt = 'Use the approved project guidance relevant to credentials and give one concise verification reminder.';
    const run = await runClaude([
      '--print', '--output-format', 'json', '--no-session-persistence',
      '--permission-mode', 'default', '--tools', '', '--settings', settingsPath, prompt,
    ], { cwd: projectRoot, env: environment, timeout: 90_000 });
    if (fixtureError) throw new Error(fixtureError);
    assert.equal(authorizationSafe, true, 'Claude sent unexpected authorization data to the loopback fixture');
    assert.ok(messageRequests >= 1, 'Claude did not request a completion from the loopback fixture');
    assert.equal(requestsWithApprovedMemory, messageRequests,
      'every Claude model request must contain the result of the local approved-memory hook');
    if (expectedTaskResumeMarkers.length > 0) {
      assert.equal(requestsWithTaskResume, messageRequests,
        'every Claude model request must contain the explicitly selected task-resume context');
    }
    assert.equal(toolUseBlocksObserved, 0,
      'the deterministic no-action provider must not receive a host tool-use or tool-result block');
    assert.ok(run.stdout.includes(finalResponse), 'Claude final output did not contain the deterministic fixture response');
    return {
      status: 'verified', provider: 'deterministic-loopback-fixture',
      host_hook: 'UserPromptSubmit', approved_memory_observed: true,
      api_requests: messageRequests, local_health_checks: localHealthChecks, real_model_invocations: 0,
      tools_listed_to_provider: toolsListedToProvider, tool_use_blocks_observed: toolUseBlocksObserved,
      session_persistence: false,
      ...(expectedTaskResumeMarkers.length > 0 ? {
        task_resume: {
          schema_version: 'threadforge.claude-task-resume-smoke.v1',
          status: 'verified', host_cli_version: hostVersion,
          provider: 'deterministic-loopback-fixture', host_hook: 'UserPromptSubmit',
          task_context_observed: true, task_context_content_retained_in_report: false,
          task_objective_observed: true, requirements_not_finalized_warning_verified: true,
          advisory_safety_statement_verified: true,
          action_ready: false, guidance_only: true, execution_authorized: false,
          api_requests: messageRequests, real_model_invocations: 0,
        },
      } : {}),
    };
  } catch (error) {
    throw new Error(`${error?.message ?? 'Claude prompt-memory smoke failed'}; request_shapes=${JSON.stringify(requestShapes)}`);
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

function sendMessage(response, requestBody, guidance) {
  const content = [{ type: 'text', text: guidance }];
  const message = {
    id: 'msg_threadforge_claude_fixture', type: 'message', role: 'assistant',
    model: typeof requestBody.model === 'string' ? requestBody.model : 'claude-fixture',
    content, stop_reason: 'end_turn', stop_sequence: null,
    usage: { input_tokens: 32, output_tokens: Math.max(1, Math.ceil(guidance.length / 4)) },
  };
  if (requestBody.stream === true) {
    response.writeHead(200, {
      'content-type': 'text/event-stream; charset=utf-8',
      'cache-control': 'no-cache', connection: 'close',
    });
    writeEvent(response, 'message_start', { type: 'message_start', message: { ...message, content: [], stop_reason: null, usage: { input_tokens: 32, output_tokens: 0 } } });
    writeEvent(response, 'content_block_start', { type: 'content_block_start', index: 0, content_block: { type: 'text', text: '' } });
    writeEvent(response, 'content_block_delta', { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: guidance } });
    writeEvent(response, 'content_block_stop', { type: 'content_block_stop', index: 0 });
    writeEvent(response, 'message_delta', { type: 'message_delta', delta: { stop_reason: 'end_turn', stop_sequence: null }, usage: message.usage });
    writeEvent(response, 'message_stop', { type: 'message_stop' });
    response.end();
    return;
  }
  writeJSON(response, message);
}

function writeEvent(response, name, value) {
  response.write(`event: ${name}\ndata: ${JSON.stringify(value)}\n\n`);
}

function writeJSON(response, value, statusCode = 200) {
  const body = JSON.stringify(value);
  response.writeHead(statusCode, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': Buffer.byteLength(body), connection: 'close',
  }).end(body);
}

function runClaude(args, { cwd, env, timeout }) {
  return new Promise((resolve, reject) => {
    const child = spawn('claude', args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    const stdout = [];
    const stderr = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let settled = false;
    const fail = (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(error);
    };
    const timer = setTimeout(() => {
      child.kill();
      const detail = Buffer.concat(stderr).toString('utf8').slice(-1024).trim();
      fail(new Error(`Claude CLI exceeded the ${timeout} ms fixture timeout${detail ? `: ${detail}` : ''}`));
    }, timeout);
    child.once('error', fail);
    child.stdout.on('data', (chunk) => {
      stdoutBytes += chunk.length;
      if (stdoutBytes > 4 << 20) {
        child.kill();
        fail(new Error('Claude fixture stdout exceeded its local size bound'));
      } else stdout.push(chunk);
    });
    child.stderr.on('data', (chunk) => {
      if (stderrBytes < 2 << 20) { stderr.push(chunk); stderrBytes += chunk.length; }
    });
    child.once('close', (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      const out = Buffer.concat(stdout).toString('utf8');
      const err = Buffer.concat(stderr).toString('utf8');
      if (code !== 0) reject(new Error(`Claude CLI exited ${code}: ${(err || out).trim().slice(-2048)}`));
      else resolve({ stdout: out, stderr: err });
    });
  });
}
