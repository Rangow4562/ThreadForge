import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const providerID = 'threadforge-local-smoke';
const modelID = 'threadforge-command-code-fixture';
const maxRequestBytes = 8 << 20;

export async function runCommandCodeAgentMcpRead({
  projectRoot, home, procedureID, expectedGuidance, isolatedEnvironment, runCLI,
}) {
  let providerRequests = 0;
  let auxiliaryRequests = 0;
  let toolCalls = 0;
  let toolResultObserved = false;
  let externalConnectionsBlocked = 0;
  let authorizationSafe = true;
  let fixtureError;
  const exposedTools = new Set();
  const server = createServer((request, response) => {
    const chunks = [];
    let requestBytes = 0;
    request.on('data', (chunk) => {
      requestBytes += chunk.length;
      if (requestBytes <= maxRequestBytes) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (requestBytes > maxRequestBytes) throw new Error('local_fixture_request_too_large');
        const requestURL = new URL(request.url ?? '/', 'http://127.0.0.1');
        if (request.method === 'GET' && /^\/v1\/models\/?$/.test(requestURL.pathname)) {
          writeJSON(response, { object: 'list', data: [{ id: modelID, object: 'model', owned_by: 'threadforge-fixture' }] });
          return;
        }
        if (request.method !== 'POST' || !/^\/v1\/chat\/completions\/?$/.test(requestURL.pathname)) {
          fixtureError ??= 'unexpected_loopback_provider_route';
          writeJSON(response, { error: { message: 'unexpected local fixture route' } }, 404);
          return;
        }
        if (request.headers.authorization) {
          authorizationSafe = false;
          fixtureError ??= 'unexpected_authorization_header';
          writeJSON(response, { error: { message: 'local fixture does not accept authorization' } }, 401);
          return;
        }

        const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        providerRequests++;
        const messages = Array.isArray(body.messages) ? body.messages : [];
        const toolResults = messages.filter(isToolResultMessage);
        toolResultObserved = toolResults.some((message) => JSON.stringify(message).includes(expectedGuidance));
        if (toolResults.length > 0 && !toolResultObserved) {
          fixtureError ??= 'threadforge_tool_result_missing_expected_guidance';
          sendChatResponse(response, body, {
            content: 'The Threadforge procedure result did not contain the expected approved guidance.',
          });
          return;
        }
        if (toolResultObserved) {
          sendChatResponse(response, body, { content: expectedGuidance });
          return;
        }

        const tools = collectTools(body.tools);
        for (const tool of tools) exposedTools.add(tool.name);
        const call = makeProcedureCall(tools, procedureID);
        if (call) {
          if (toolCalls !== 0) {
            fixtureError ??= 'repeated_threadforge_procedure_tool_call';
            sendChatResponse(response, body, { content: 'The procedure lookup was already attempted.' });
            return;
          }
          toolCalls++;
          sendChatResponse(response, body, call);
          return;
        }

        if (tools.length === 0 && auxiliaryRequests < 2 && toolCalls === 0) {
          auxiliaryRequests++;
          sendChatResponse(response, body, { content: 'Use the available read-only Threadforge procedure MCP tool.' });
          return;
        }
        fixtureError ??= 'threadforge_procedure_get_not_exposed_to_model';
        sendChatResponse(response, body, { content: 'The Threadforge procedure lookup tool was not exposed.' });
      } catch {
        fixtureError ??= 'local_command_code_provider_fixture_error';
        writeJSON(response, { error: { message: 'deterministic local Command Code fixture failed' } }, 500);
      }
    });
  });
  server.on('connect', (_request, socket) => {
    externalConnectionsBlocked++;
    socket.end('HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n');
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  assert(address && typeof address === 'object');
  const endpoint = `http://127.0.0.1:${address.port}/v1`;
  const providerDirectory = path.join(home, '.commandcode');
  mkdirSync(providerDirectory, { recursive: true, mode: 0o700 });
  writeFileSync(path.join(providerDirectory, 'providers.json'), `${JSON.stringify({
    provider: {
      [providerID]: {
        name: 'Threadforge loopback smoke fixture',
        baseURL: endpoint,
        apiKey: false,
        models: { [modelID]: { contextWindow: 8192, maxOutput: 512, cost: { input: 0, output: 0 } } },
      },
    },
  }, null, 2)}\n`, { flag: 'w', mode: 0o600 });

  const proxy = `http://127.0.0.1:${address.port}`;
  const environment = {
    ...isolatedEnvironment,
    HOME: home,
    USERPROFILE: home,
    CMD_LOCAL_ONLY: '1',
    HTTP_PROXY: proxy,
    HTTPS_PROXY: proxy,
    ALL_PROXY: proxy,
    NO_PROXY: '127.0.0.1,localhost',
    http_proxy: proxy,
    https_proxy: proxy,
    all_proxy: proxy,
    no_proxy: '127.0.0.1,localhost',
  };

  try {
    let availableModels;
    try {
      availableModels = runCLI([
        '--no-auto-update', '--local-only', '--list-models',
      ], { cwd: projectRoot, env: environment, timeout: 15_000 });
    } catch (error) {
      if (/not authenticated/i.test(String(error?.message ?? error))) {
        return {
          status: 'skipped',
          reason: 'isolated_command_code_cli_requires_login',
          project_mcp_configured: true,
          local_provider_configured: true,
          real_model_invocations: 0,
        };
      }
      throw error;
    }
    if (!availableModels.includes(`${providerID}/${modelID}`)) {
      throw new Error('Command Code did not load the isolated keyless loopback model configuration');
    }

    const prompt = `Read approved procedure ${procedureID} using the Threadforge project MCP tool, then repeat its first guidance sentence exactly. Do not use or request any other tool.`;
    let output;
    try {
      output = runCLI([
        '--no-auto-update', '--local-only', '--no-session', '--trust',
        '--model', `${providerID}/${modelID}`,
        '--print', prompt, '--output-format', 'json', '--max-turns', '4',
      ], { cwd: projectRoot, env: environment, timeout: 90_000 });
    } catch (error) {
      if (/not authenticated/i.test(String(error?.message ?? error))) {
        return {
          status: 'skipped',
          reason: 'isolated_command_code_cli_requires_login_for_agent_run',
          project_mcp_configured: true,
          local_provider_configured: true,
          local_model_listed: true,
          provider_requests: providerRequests,
          real_model_invocations: 0,
        };
      }
      throw error;
    }
    if (fixtureError) throw new Error(fixtureError);
    const result = output.split(/\r?\n/).map(parseJSONLine).filter(Boolean)
      .reverse().find((frame) => frame.type === 'result');
    assert(result, 'Command Code did not emit a final JSON result frame');
    assert.equal(result.subtype, 'success', `Command Code headless run ended as ${result.subtype}`);
    assert.equal(authorizationSafe, true, 'Command Code sent an unexpected authorization header to the local fixture');
    assert.equal(toolCalls, 1, 'expected exactly one scripted Threadforge procedure read');
    assert.equal(toolResultObserved, true, 'the local provider did not receive the approved procedure result');
    assert.ok(result.finalText?.includes(expectedGuidance), 'Command Code final output omitted the approved procedure guidance');
    return {
      status: 'verified',
      provider: 'loopback-openai-compatible-fixture',
      project_mcp_configured: true,
      tool: 'threadforge_procedure_get',
      tool_call_count: toolCalls,
      tool_result_observed: toolResultObserved,
      provider_requests: providerRequests,
      auxiliary_provider_requests: auxiliaryRequests,
      external_connections_blocked: externalConnectionsBlocked,
      exposed_tool_names: [...exposedTools].filter((name) => /threadforge|mcp/i.test(name)).slice(0, 12),
      real_model_invocations: 0,
    };
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

function collectTools(value, depth = 0, result = [], seen = new Set()) {
  if (depth > 8 || value == null || typeof value !== 'object' || seen.has(value)) return result;
  seen.add(value);
  if (Array.isArray(value)) {
    for (const item of value.slice(0, 256)) collectTools(item, depth + 1, result, seen);
    return result;
  }
  const name = value.function?.name ?? value.name;
  if (typeof name === 'string' && (value.function || value.parameters || value.inputSchema || value.tools)) {
    const schema = value.function?.parameters ?? value.parameters ?? value.inputSchema ?? {};
    result.push({ name, schema });
  }
  for (const [key, child] of Object.entries(value)) {
    if (key === 'description' || key === 'input_examples') continue;
    collectTools(child, depth + 1, result, seen);
  }
  return result;
}

function makeProcedureCall(tools, procedureID) {
  const direct = tools.find((tool) => /threadforge_procedure_get$/i.test(tool.name));
  if (direct) {
    const properties = direct.schema?.properties ?? {};
    const idField = ['procedure_id', 'procedureId', 'id'].find((field) => Object.hasOwn(properties, field)) ?? 'procedure_id';
    return { functionName: direct.name, arguments: { [idField]: procedureID } };
  }

  const generic = tools.find((tool) => /(?:call_mcp_tool|mcp_tool_call)$/i.test(tool.name));
  if (!generic) return undefined;
  const properties = generic.schema?.properties ?? {};
  const args = {};
  for (const field of Object.keys(properties)) {
    if (/^(server|server_name)$/i.test(field)) args[field] = 'threadforge';
    else if (/^(tool|tool_name)$/i.test(field)) args[field] = 'threadforge_procedure_get';
    else if (/^(procedure_id|procedureId|id)$/i.test(field)) args[field] = procedureID;
    else if (/^(arguments|params|parameters)$/i.test(field)) args[field] = { procedure_id: procedureID };
  }
  if (!Object.values(args).some((value) => value === procedureID)) return undefined;
  return { functionName: generic.name, arguments: args };
}

function isToolResultMessage(message) {
  return message?.role === 'tool' || message?.type === 'tool_result' ||
    (typeof message?.tool_call_id === 'string' && message?.role !== 'assistant');
}

function sendChatResponse(response, requestBody, result) {
  const id = `chatcmpl-threadforge-${Date.now()}-${Math.random().toString(16).slice(2, 8)}`;
  const model = typeof requestBody.model === 'string' ? requestBody.model : modelID;
  const created = Math.floor(Date.now() / 1000);
  const message = result.functionName
    ? { role: 'assistant', content: null, tool_calls: [{
      id: `call-threadforge-${id}`, type: 'function',
      function: { name: result.functionName, arguments: JSON.stringify(result.arguments) },
    }] }
    : { role: 'assistant', content: result.content };
  const finishReason = result.functionName ? 'tool_calls' : 'stop';
  if (requestBody.stream === true) {
    response.writeHead(200, {
      'content-type': 'text/event-stream; charset=utf-8',
      'cache-control': 'no-cache',
      connection: 'close',
    });
    const emit = (delta, finish_reason = null) => response.write(`data: ${JSON.stringify({
      id, object: 'chat.completion.chunk', created, model,
      choices: [{ index: 0, delta, finish_reason }],
    })}\n\n`);
    if (result.functionName) {
      emit({ tool_calls: [{ index: 0, ...message.tool_calls[0] }] });
      emit({}, finishReason);
    } else {
      emit({ content: result.content });
      emit({}, finishReason);
    }
    response.end('data: [DONE]\n\n');
    return;
  }
  writeJSON(response, {
    id, object: 'chat.completion', created, model,
    choices: [{ index: 0, message, finish_reason: finishReason }],
    usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 },
  });
}

function writeJSON(response, value, statusCode = 200) {
  response.writeHead(statusCode, { 'content-type': 'application/json' });
  response.end(JSON.stringify(value));
}

function parseJSONLine(line) {
  try { return JSON.parse(line); } catch { return undefined; }
}
