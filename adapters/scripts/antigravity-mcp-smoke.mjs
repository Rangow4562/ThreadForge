import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const fixtureKey = 'threadforge-antigravity-local-fixture-only';
const fixtureModel = 'Gemini 3.8 Flash (Low)';
const maxRequestBytes = 8 << 20;

export async function runAntigravityAgentMcpRead({
  projectRoot, home, cliPath, procedureID, expectedGuidance, isolatedEnvironment,
  toolName = 'threadforge_procedure_get', toolArguments, prompt, expectedFinalResponse,
  requiredResultMarkers = [], hostVersion = 'unknown', scriptedFunctionCall, expectedToolResponseMarker,
  sandbox = true,
}) {
  const requestedArguments = toolArguments ?? { procedure_id: procedureID };
  const finalResponse = expectedFinalResponse ?? expectedGuidance;
  const promptText = prompt ??
    `Read the approved procedure ${procedureID} using the Threadforge project MCP tool. Then repeat its first guidance sentence exactly. Do not use or request any other tool.`;
  let providerRequests = 0;
  let auxiliaryRequests = 0;
  let toolCalls = 0;
  let toolResultObserved = false;
  let fixtureReason;
  let authorizationSafe = true;
  let hostFailureCategory;
  let hostErrorSummary;
  let blockedExternalRequests = 0;
  let expectedContentObserved = false;
  let resultMarkerFlags = {};
  const observedTools = new Set();
  const providerTrace = [];

  const server = createServer((request, response) => {
    const chunks = [];
    let requestBytes = 0;
    request.on('data', (chunk) => {
      requestBytes += chunk.length;
      if (requestBytes <= maxRequestBytes) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (requestBytes > maxRequestBytes) throw new Error('request_too_large');
        const url = new URL(request.url ?? '/', 'http://127.0.0.1');
        const suppliedKey = url.searchParams.get('key') ?? request.headers['x-goog-api-key'] ?? '';
        const authorization = request.headers.authorization;
        if ((suppliedKey && suppliedKey !== fixtureKey) ||
            (authorization && authorization !== `Bearer ${fixtureKey}`)) {
          authorizationSafe = false;
          writeJson(response, { error: { code: 401, message: 'local fixture key rejected' } }, 401);
          return;
        }

        if (request.method === 'GET' && /\/models(?:\?.*)?$/.test(request.url ?? '')) {
          writeJson(response, { models: [{
            name: `models/${fixtureModel}`, displayName: 'Threadforge local fixture',
            supportedGenerationMethods: ['generateContent', 'countTokens'],
          }] });
          return;
        }
        if (request.method !== 'POST' || !/\/models\/[^/]+:(?:generateContent|streamGenerateContent|countTokens)(?:\?.*)?$/.test(request.url ?? '')) {
          fixtureReason ??= 'unexpected_local_provider_route';
          writeJson(response, { error: { code: 404, message: 'unexpected local fixture route' } }, 404);
          return;
        }

        const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const serialized = JSON.stringify(body);
        if (url.pathname.endsWith(':countTokens')) {
          writeJson(response, { totalTokens: 1 });
          return;
        }
        providerRequests++;
        const declarations = collectFunctionDeclarations(body.tools);
        for (const declaration of declarations) observedTools.add(declaration.name);

        const functionResponses = collectFunctionResponses(body.contents);
        const contents = Array.isArray(body.contents) ? body.contents : [];
        providerTrace.push({
          request_index: providerRequests,
          content_roles: contents.map((item) => item?.role).filter((value) => typeof value === 'string').slice(-8),
          part_types: contents.flatMap((item) => (Array.isArray(item?.parts) ? item.parts : []).map((part) =>
            Object.keys(part ?? {}).filter((key) => ['text', 'functionCall', 'functionResponse', 'inlineData', 'fileData'].includes(key)).join('+') || 'other'))
            .slice(-12),
          declared_threadforge_tools: declarations.map((item) => item.name).filter((name) => /threadforge|mcp/i.test(name)).slice(0, 16),
          declared_tool_schemas: declarations.filter((item) => item.name.includes(toolName) || /call_mcp_tool/i.test(item.name))
            .map((item) => ({
              name: item.name,
              parameter_keys: Object.keys(item.schema?.properties ?? {}).slice(0, 24),
              property_shapes: Object.entries(item.schema?.properties ?? {}).slice(0, 24).map(([name, property]) => ({
                name, type: typeof property?.type === 'string' ? property.type : undefined,
                enum: Array.isArray(property?.enum) ? property.enum.slice(0, 16) : undefined,
                required: Array.isArray(item.schema?.required) && item.schema.required.includes(name),
              })),
              required: Array.isArray(item.schema?.required) ? item.schema.required.slice(0, 24) : [],
            })).slice(0, 8),
          function_calls: collectFunctionCalls(contents).map((item) => ({
            name: typeof item.name === 'string' ? item.name : undefined,
            argument_keys: item.args && typeof item.args === 'object' ? Object.keys(item.args).slice(0, 24) : [],
            procedure_id_matches: requestedArguments.procedure_id === undefined ? undefined :
              item.args?.procedure_id === requestedArguments.procedure_id ||
              item.args?.Arguments?.procedure_id === requestedArguments.procedure_id,
            task_id_matches: requestedArguments.task_id === undefined ? undefined :
              item.args?.task_id === requestedArguments.task_id ||
              item.args?.Arguments?.task_id === requestedArguments.task_id,
          })).slice(-8),
          function_responses: functionResponses.map((item) => summarizeFunctionResponse(item, expectedGuidance)).slice(-8),
        });

        if (functionResponses.length > 0) {
          const resultText = functionResponseText(functionResponses);
          if (scriptedFunctionCall) {
            expectedContentObserved = normalizedText(resultText).includes(normalizedText(expectedToolResponseMarker ?? ''));
            resultMarkerFlags = { threadforge_plan_gate_denial: expectedContentObserved };
            toolResultObserved = expectedContentObserved;
            if (!toolResultObserved) fixtureReason ??= 'plan_gate_denial_not_returned_to_provider';
            else fixtureReason = undefined;
            sendGemini(response, request.url, {
              text: toolResultObserved ? finalResponse : 'The expected Threadforge plan-gate denial was not observed.',
            });
            return;
          }
          const compactResultText = resultText.replace(/\s+/g, '');
          expectedContentObserved = normalizedText(resultText).includes(normalizedText(expectedGuidance));
          resultMarkerFlags = Object.fromEntries(requiredResultMarkers.map((marker) => [
            marker.replace(/[^A-Za-z0-9]+/g, '_').replace(/^_|_$/g, ''),
            compactResultText.includes(marker.replace(/\s+/g, '')),
          ]));
          toolResultObserved = expectedContentObserved && Object.values(resultMarkerFlags).every(Boolean);
          if (!toolResultObserved) fixtureReason ??= toolName === 'threadforge_task_resume_context'
            ? 'task_resume_result_missing_advisory_fields'
            : 'procedure_result_missing_expected_guidance';
          sendGemini(response, request.url, {
            text: toolResultObserved ? finalResponse : 'The Threadforge MCP result did not contain the expected content and safety fields.',
          });
          return;
        }

        if (serialized.includes(expectedGuidance)) {
          const compactSerialized = serialized.replace(/\s+/g, '');
          expectedContentObserved = true;
          resultMarkerFlags = Object.fromEntries(requiredResultMarkers.map((marker) => [
            marker.replace(/[^A-Za-z0-9]+/g, '_').replace(/^_|_$/g, ''),
            compactSerialized.includes(marker.replace(/\s+/g, '')),
          ]));
          toolResultObserved = Object.values(resultMarkerFlags).every(Boolean);
          if (!toolResultObserved) fixtureReason ??= 'task_resume_result_missing_advisory_fields';
          sendGemini(response, request.url, {
            text: toolResultObserved ? finalResponse : 'The Threadforge MCP result omitted required advisory safety fields.',
          });
          return;
        }

        if (toolCalls > 0) {
          fixtureReason ??= 'threadforge_mcp_tool_result_not_returned_to_model';
          sendGemini(response, request.url, { text: 'The Threadforge MCP read has not returned a result.' });
          return;
        }

        if (scriptedFunctionCall) {
          const target = declarations.find((item) => item.name === scriptedFunctionCall.name);
          if (!target) {
            sendGemini(response, request.url, { text: 'The scripted Antigravity file tool was not exposed.' });
            return;
          }
        fixtureReason = undefined;
          toolCalls++;
          sendGemini(response, request.url, { functionCall: scriptedFunctionCall });
          return;
        }

        const target = declarations.find((item) => item.name.endsWith(toolName));
        if (target) {
          if (toolCalls !== 0) {
            fixtureReason ??= 'repeated_threadforge_mcp_tool_call';
            sendGemini(response, request.url, { text: 'The Threadforge MCP lookup was already attempted.' });
            return;
          }
          toolCalls++;
          const args = { ...requestedArguments };
          if (Object.hasOwn(target.schema?.properties ?? {}, 'toolSummary')) {
            args.toolSummary = 'Read the explicitly selected Threadforge task or procedure as advisory context.';
          }
          sendGemini(response, request.url, {
            functionCall: { name: target.name, args },
          });
          return;
        }

        const genericMcpTool = declarations.find((item) => /call_mcp_tool|mcp_tool_call/i.test(item.name));
        if (genericMcpTool) {
          const args = makeGenericMcpArguments(genericMcpTool, toolName, requestedArguments);
          if (args) {
            if (toolCalls !== 0) {
              fixtureReason ??= 'repeated_threadforge_mcp_tool_call';
              sendGemini(response, request.url, { text: 'The Threadforge MCP lookup was already attempted.' });
              return;
            }
            toolCalls++;
            sendGemini(response, request.url, { functionCall: { name: genericMcpTool.name, args } });
            return;
          }
        }

        if (declarations.length === 0 && auxiliaryRequests < 2 && toolCalls === 0) {
          auxiliaryRequests++;
          sendGemini(response, request.url, { text: 'Threadforge approved procedure lookup is available for this request.' });
          return;
        }
        fixtureReason ??= 'project_procedure_tool_not_exposed';
        sendGemini(response, request.url, { text: 'The project procedure lookup tool was not exposed.' });
      } catch {
        fixtureReason ??= 'local_provider_fixture_error';
        writeJson(response, { error: { code: 500, message: 'local deterministic fixture failed' } }, 500);
      }
    });
  });
  server.on('connect', (_request, socket) => {
    blockedExternalRequests++;
    socket.end('HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\n\r\n');
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  if (!address || typeof address !== 'object') {
    await new Promise((resolve) => server.close(resolve));
    throw new Error('Antigravity loopback fixture did not bind');
  }

  const settingsPath = path.join(home, '.gemini', 'antigravity-cli', 'settings.json');
  mkdirSync(path.dirname(settingsPath), { recursive: true, mode: 0o700 });
  writeFileSync(settingsPath, `${JSON.stringify({
    modelProvider: 'gemini',
    trustedWorkspaces: [projectRoot],
    permissions: { allow: [`mcp(threadforge/${toolName})`] },
  }, null, 2)}\n`, { flag: 'w', mode: 0o600 });

  const endpoint = `http://127.0.0.1:${address.port}/v1beta`;
  const proxy = `http://127.0.0.1:${address.port}`;
  const environment = {
    ...isolatedEnvironment,
    HOME: home,
    USERPROFILE: home,
    GEMINI_API_KEY: fixtureKey,
    GOOGLE_GEMINI_BASE_URL: endpoint,
    HTTP_PROXY: proxy,
    HTTPS_PROXY: proxy,
    ALL_PROXY: proxy,
    NO_PROXY: '127.0.0.1,localhost',
    http_proxy: proxy,
    https_proxy: proxy,
    all_proxy: proxy,
    no_proxy: '127.0.0.1,localhost',
  };
  let output = '';
  let invocationFailed = false;
  let hostOutput = '';
  try {
    const cliArgs = [
      `--print=${promptText}`,
      '--output-format', 'json',
      '--print-timeout', '8s',
      '--model', fixtureModel,
    ];
    if (sandbox) cliArgs.push('--sandbox');
    const run = await runAntigravityCli(cliArgs, { cwd: projectRoot, env: environment, timeout: 12_000 });
    output = run.stdout;
    hostOutput = `${run.stderr}\n${run.stdout}`;
    invocationFailed = run.error !== undefined || run.exitCode !== 0 || run.timedOut;
    if (invocationFailed) {
      hostFailureCategory = classifyHostFailure(hostOutput);
      hostErrorSummary = summarizeHostError(hostOutput, {
        fixtureKey, cliPath, projectRoot, home, procedureID, expectedGuidance,
        deniedFilePath: scriptedFunctionCall?.args?.TargetFile,
        deniedFileContent: scriptedFunctionCall?.args?.CodeContent,
      });
    }

    if (scriptedFunctionCall && expectedToolResponseMarker &&
        normalizedText(hostOutput).includes(normalizedText(expectedToolResponseMarker))) {
      expectedContentObserved = true;
      toolResultObserved = true;
      resultMarkerFlags = { threadforge_plan_gate_denial: true };
      fixtureReason = undefined;
    }

    const expectedInOutput = output.includes(finalResponse);
    const observedPlanGateDenial = scriptedFunctionCall !== undefined && toolResultObserved;
    const verified = (!invocationFailed || observedPlanGateDenial) && authorizationSafe && toolCalls === 1 &&
      toolResultObserved && (expectedInOutput || observedPlanGateDenial) && !fixtureReason;
    const reason = verified ? undefined : fixtureReason ?? (
      !authorizationSafe ? 'fixture_authentication_mismatch' :
        providerRequests === 0 ? 'local_provider_not_reached' :
          toolCalls === 0 ? 'project_procedure_tool_not_dispatched' :
            !toolResultObserved ? 'procedure_result_not_observed_by_provider' :
              !expectedInOutput ? 'procedure_guidance_not_returned' : 'host_invocation_incomplete'
    );
    return {
      status: verified ? 'verified' : 'not-verified',
      host_cli_version: hostVersion,
      reason,
      host_failure_category: hostFailureCategory,
      host_error_summary: hostErrorSummary,
      provider: 'loopback-gemini-api-fixture',
      project_mcp_configured: true,
      project_read_tool_always_loaded: observedTools.has(toolName) ||
        [...observedTools].some((name) => name.endsWith(toolName)),
      tool: toolCalls === 1 ? toolName : undefined,
      tool_call_count: toolCalls,
      tool_result_observed: toolResultObserved,
      ...(toolName === 'threadforge_task_resume_context' ? {
        schema_version: 'threadforge.antigravity-task-resume-smoke.v1',
        task_context_content_retained_in_report: false,
        advisory_result_markers_verified: requiredResultMarkers.length > 0 && toolResultObserved,
        requirements_not_finalized_warning_verified: requiredResultMarkers.includes('requirements_not_finalized') && toolResultObserved,
        result_check_diagnostics: { expected_content_observed: expectedContentObserved, marker_flags: resultMarkerFlags },
        action_ready: false,
        guidance_only: true,
        execution_authorized: false,
      } : {}),
      provider_requests: providerRequests,
      auxiliary_provider_requests: auxiliaryRequests,
      local_fixture_authentication_validated: providerRequests > 0 && authorizationSafe,
      external_network_attempts_blocked: blockedExternalRequests,
      provider_trace: providerTrace,
      real_model_invocations: 0,
    };
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

export async function runAntigravityPlanGateAttempt({
  projectRoot, home, cliPath, isolatedEnvironment, deniedFilePath, auditPath, hostVersion = 'unknown',
}) {
  const marker = 'THREADFORGE_ANTIGRAVITY_DENIED_FILE_SENTINEL';
  const denialReason = 'Threadforge withheld this Antigravity file operation';
  const result = await runAntigravityAgentMcpRead({
    projectRoot,
    home,
    cliPath,
    procedureID: 'antigravity-plan-gate-smoke',
    expectedGuidance: denialReason,
    isolatedEnvironment,
    hostVersion,
    sandbox: false,
    toolName: 'write_to_file',
    prompt: 'Create one file in the project and wait for the tool result.',
    expectedFinalResponse: 'The requested file write was denied by the Threadforge task plan gate.',
    scriptedFunctionCall: {
      name: 'write_to_file',
      args: {
        TargetFile: deniedFilePath,
        Overwrite: false,
        CodeContent: marker,
        Description: 'Create the isolated plan-gate smoke sentinel.',
        toolAction: 'write',
        toolSummary: 'Create the isolated plan-gate smoke sentinel.',
      },
    },
    expectedToolResponseMarker: denialReason,
  });
  let audit = [];
  if (typeof auditPath === 'string' && auditPath !== '') {
    try {
      audit = readFileSync(auditPath, 'utf8').split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
    } catch { audit = []; }
  }
  const hostToolResponse = result.provider_trace?.flatMap((trace) => trace.function_responses ?? [])
    .find((entry) => entry.name === 'write_to_file');
  const hostToolErrorObserved = hostToolResponse?.result_signal === 'tool_error' &&
    hostToolResponse?.result_markers?.numeric_status === 1;
  const guardDecisionObserved = audit.length === 1 && audit[0]?.decision === 'deny' && audit[0]?.exit_code === 0;
  const probeComplete = hostToolErrorObserved && result.tool_call_count === 1 && result.provider_requests > 0 &&
    result.local_fixture_authentication_validated && result.real_model_invocations === 0;
  const verified = guardDecisionObserved && probeComplete;
  const hookNotObserved = audit.length === 0 && probeComplete;
  return {
    ...result,
    status: verified ? 'verified' : hookNotObserved ? 'not_observed' : 'not-verified',
    reason: verified ? undefined : hookNotObserved ? 'workspace_pretool_hook_not_invoked' : !guardDecisionObserved ? 'guard_denial_not_observed' :
      !hostToolErrorObserved ? 'host_tool_error_after_guard_denial_not_observed' : result.reason,
    hook_callback_invocations: audit.length,
    guard_decision: audit.length === 1 ? audit[0]?.decision : 'unknown',
    host_tool_error_observed: hostToolErrorObserved,
    tool_result_observed: hostToolErrorObserved,
  };
}

function runAntigravityCli(args, { cwd, env, timeout }) {
  return new Promise((resolve) => {
    const child = spawn('agy', args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    const stdout = [];
    const stderr = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let timedOut = false;
    let settled = false;
    const settle = (value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(value);
    };
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill();
    }, timeout);
    child.stdout.on('data', (chunk) => {
      stdoutBytes += chunk.length;
      if (stdoutBytes <= 1 << 20) stdout.push(chunk);
      else child.kill();
    });
    child.stderr.on('data', (chunk) => {
      stderrBytes += chunk.length;
      if (stderrBytes <= 1 << 20) stderr.push(chunk);
      else child.kill();
    });
    child.once('error', (error) => settle({ error, exitCode: null, timedOut,
      stdout: Buffer.concat(stdout).toString('utf8'), stderr: Buffer.concat(stderr).toString('utf8') }));
    child.once('close', (exitCode) => settle({ exitCode, timedOut,
      stdout: Buffer.concat(stdout).toString('utf8'), stderr: Buffer.concat(stderr).toString('utf8') }));
  });
}

function summarizeHostError(message, replacements) {
  let summary = String(message);
  for (const [value, replacement] of [
    [replacements.fixtureKey, '<fixture-key>'],
    [replacements.cliPath, '<threadforge-cli>'],
    [replacements.projectRoot, '<isolated-project>'],
    [replacements.home, '<isolated-home>'],
    [replacements.procedureID, '<procedure-id>'],
    [replacements.expectedGuidance, '<guidance>'],
    [replacements.deniedFilePath, '<target-file>'],
    [replacements.deniedFileContent, '<file-content>'],
  ]) {
    if (value) summary = summary.replaceAll(value, replacement);
  }
  return summary
    .replace(/(?:Bearer\s+|key=)[^\s&]+/gi, '<credential>')
    .replace(/[\r\n\t]+/g, ' ')
    .trim().slice(-600);
}

function classifyHostFailure(message) {
  if (/--print.*prompt|prompt.*--print/i.test(message)) return 'cli_argument_parsing';
  if (/invalid model selection|model .*not recognized/i.test(message)) return 'model_selection';
  if (/api.?key|authentication|unauthorized|401/i.test(message)) return 'provider_authentication';
  if (/model.?provider|provider.*(unsupported|unknown|missing)/i.test(message)) return 'provider_configuration';
  if (/trust|workspace/i.test(message)) return 'project_trust';
  if (/permission|approval|denied|not allowed/i.test(message)) return 'tool_permission';
  if (/mcp|server/i.test(message)) return 'mcp_startup';
  if (/timeout|timed out/i.test(message)) return 'host_timeout';
  if (/network|connect|econn|fetch/i.test(message)) return 'network_or_endpoint';
  return 'host_cli_error';
}

function collectFunctionDeclarations(value, depth = 0, declarations = [], seen = new Set()) {
  if (depth > 10 || value == null || typeof value !== 'object' || seen.has(value)) return declarations;
  seen.add(value);
  if (Array.isArray(value)) {
    for (const item of value) collectFunctionDeclarations(item, depth + 1, declarations, seen);
    return declarations;
  }
  const name = typeof value.name === 'string' ? value.name : undefined;
  if (name && (value.parameters || value.parametersJsonSchema || value.parameters_json_schema)) {
    declarations.push({ name, schema: value.parameters ?? value.parametersJsonSchema ?? value.parameters_json_schema });
  }
  for (const key of ['functionDeclarations', 'function_declarations', 'tools', 'function', 'declarations']) {
    if (Object.hasOwn(value, key)) collectFunctionDeclarations(value[key], depth + 1, declarations, seen);
  }
  return declarations;
}

function collectFunctionResponses(contents) {
  if (!Array.isArray(contents)) return [];
  return contents.flatMap((content) => Array.isArray(content?.parts)
    ? content.parts.map((part) => part?.functionResponse).filter((value) => value && typeof value === 'object')
    : []).slice(-16);
}

function collectFunctionCalls(contents) {
  if (!Array.isArray(contents)) return [];
  return contents.flatMap((content) => Array.isArray(content?.parts)
    ? content.parts.map((part) => part?.functionCall).filter((value) => value && typeof value === 'object')
    : []).slice(-16);
}

function functionResponseText(value, parts = [], depth = 0) {
  if (depth > 8 || parts.join('').length > 1 << 20) return parts.join('\n');
  if (typeof value === 'string') {
    parts.push(value);
    if (depth < 6) {
      try { functionResponseText(JSON.parse(value), parts, depth + 1); } catch { /* plain text */ }
    }
    return parts.join('\n');
  }
  if (typeof value === 'boolean' || typeof value === 'number') {
    parts.push(String(value));
    return parts.join('\n');
  }
  if (Array.isArray(value)) {
    for (const item of value.slice(0, 32)) functionResponseText(item, parts, depth + 1);
    return parts.join('\n');
  }
  if (value && typeof value === 'object') {
    parts.push(JSON.stringify(value));
    for (const item of Object.values(value).slice(0, 48)) functionResponseText(item, parts, depth + 1);
  }
  return parts.join('\n');
}

function summarizeFunctionResponse(item, expectedGuidance) {
  const output = item.response?.output;
  let parsed = output;
  let outputIsJson = false;
  if (typeof output === 'string') {
    try {
      parsed = JSON.parse(output);
      outputIsJson = true;
    } catch { /* Keep the response opaque; never copy it into the report. */ }
  }
  const shapes = [];
  const textValues = [];
  collectSafeShape(parsed, expectedGuidance, shapes, textValues);
  return {
    name: typeof item.name === 'string' ? item.name : undefined,
    response_keys: item.response && typeof item.response === 'object' ? Object.keys(item.response).slice(0, 24) : [],
    output_type: output === null ? 'null' : Array.isArray(output) ? 'array' : typeof output,
    output_bytes: typeof output === 'string' ? Buffer.byteLength(output) : undefined,
    output_json_parsed: outputIsJson,
    result_signal: classifyProcedureOutput(output),
    result_markers: procedureOutputMarkers(output),
    result_shapes: shapes.slice(0, 12),
    is_error: item.response?.isError === true || item.response?.is_error === true || item.isError === true,
    expected_guidance_present: textValues.some((value) => normalizedText(value).includes(normalizedText(expectedGuidance))),
  };
}

function classifyProcedureOutput(output) {
  const value = typeof output === 'string' ? output.toLowerCase() : '';
  if ((value.includes('missing') || value.includes('unknown') || value.includes('required') || value.includes('invalid')) &&
      (value.includes('argument') || value.includes('parameter') || value.includes('procedure_id'))) return 'tool_argument_error';
  for (const [signal, terms] of [
    ['stale', ['stale']],
    ['incomplete', ['incomplete']],
    ['unverified', ['unverified']],
    ['not_found', ['not found', 'unknown procedure']],
    ['conflict', ['conflict']],
    ['privacy_blocked', ['privacy blocked', 'privacy_blocked']],
    ['permission_blocked', ['permission denied', 'not authorized', 'permission blocked']],
    ['mcp_transport_error', ['mcp', 'server', 'connection', 'stdio', 'exited']],
    ['tool_error', ['error', 'failed', 'denied', 'timed out', 'unavailable']],
  ]) {
    if (signal === 'mcp_transport_error'
      ? terms.slice(1).some((term) => value.includes(term)) && value.includes(terms[0])
      : terms.some((term) => value.includes(term))) return signal;
  }
  return value ? 'opaque_text' : 'empty';
}

function procedureOutputMarkers(output) {
  const value = typeof output === 'string' ? output.toLowerCase() : '';
  const codeMatch = value.match(/(?:status|code|exit)[\s:=]+(\d{1,3})/i);
  return {
    mentions_mcp: value.includes('mcp'),
    mentions_server: value.includes('server'),
    mentions_connection: value.includes('connection'),
    mentions_permission: value.includes('permission') || value.includes('authorized'),
    mentions_argument: value.includes('argument') || value.includes('procedure_id'),
    mentions_parameter: value.includes('parameter'),
    mentions_required: value.includes('required'),
    mentions_invalid: value.includes('invalid'),
    mentions_procedure: value.includes('procedure'),
    mentions_function: value.includes('function'),
    mentions_unknown_or_missing: value.includes('unknown') || value.includes('not found') || value.includes('missing'),
    numeric_status: codeMatch ? Number(codeMatch[1]) : undefined,
  };
}

function collectSafeShape(value, expectedGuidance, shapes, textValues, depth = 0, seen = new Set()) {
  if (depth > 7 || value == null) return;
  if (typeof value === 'string') {
    textValues.push(value);
    if (textValues.length > 64) textValues.length = 64;
    if (depth < 5) {
      try { collectSafeShape(JSON.parse(value), expectedGuidance, shapes, textValues, depth + 1, seen); } catch { /* plain text */ }
    }
    return;
  }
  if (typeof value !== 'object' || seen.has(value)) return;
  seen.add(value);
  if (Array.isArray(value)) {
    for (const item of value.slice(0, 32)) collectSafeShape(item, expectedGuidance, shapes, textValues, depth + 1, seen);
    return;
  }
  const keys = Object.keys(value);
  shapes.push({
    keys: keys.slice(0, 32),
    status: typeof value.status === 'string' ? value.status : undefined,
    freshness: typeof value.freshness === 'string' ? value.freshness : undefined,
    output_status: typeof value.output_status === 'string' ? value.output_status : undefined,
    error_present: value.error != null,
  });
  if (shapes.length > 24) shapes.length = 24;
  for (const key of keys.slice(0, 48)) {
    collectSafeShape(value[key], expectedGuidance, shapes, textValues, depth + 1, seen);
  }
}

function normalizedText(value) {
  return String(value).replace(/\s+/gu, ' ').trim();
}

function makeGenericMcpArguments(declaration, toolName, toolArguments) {
  const properties = declaration.schema?.properties;
  if (!properties || typeof properties !== 'object') return undefined;
  const args = {};
  for (const key of Object.keys(properties)) {
    const normalized = key.replace(/([a-z0-9])([A-Z])/g, '$1_$2').toLowerCase().replaceAll('-', '_');
    if (Object.hasOwn(toolArguments, normalized)) args[key] = toolArguments[normalized];
    else if (normalized === 'id' && toolArguments.task_id) args[key] = toolArguments.task_id;
    else if (normalized === 'id' && toolArguments.procedure_id) args[key] = toolArguments.procedure_id;
    else if (/^(server|server_name|mcp_server|mcp_server_name)$/.test(normalized)) args[key] = 'threadforge';
    else if (/^(tool|tool_name|mcp_tool|mcp_tool_name)$/.test(normalized)) args[key] = toolName;
    else if (/^(arguments|args|input|params)$/.test(normalized)) args[key] = { ...toolArguments };
    else if (/^(tool_action|action)$/.test(normalized)) args[key] = 'read';
    else if (/^(tool_summary|summary)$/.test(normalized)) args[key] = `Read ${toolName} as bounded, read-only advisory context.`;
  }
  return Object.keys(args).some((key) => /task_id|procedure_id|arguments|args|input|params/i.test(key)) ? args : undefined;
}

function sendGemini(response, requestURL, result) {
  const body = {
    candidates: [{
      index: 0,
      content: {
        role: 'model',
        parts: result.functionCall
          ? [{ functionCall: result.functionCall }]
          : [{ text: result.text }],
      },
      finishReason: 'STOP',
    }],
    usageMetadata: { promptTokenCount: 1, candidatesTokenCount: 1, totalTokenCount: 2 },
    modelVersion: fixtureModel,
  };
  if (requestURL.includes(':streamGenerateContent') || requestURL.includes('alt=sse')) {
    response.writeHead(200, { 'content-type': 'text/event-stream; charset=utf-8', connection: 'close' });
    response.end(`data: ${JSON.stringify(body)}\n\n`);
  } else writeJson(response, body);
}

function writeJson(response, value, statusCode = 200) {
  const body = JSON.stringify(value);
  response.writeHead(statusCode, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': Buffer.byteLength(body),
    connection: 'close',
  }).end(body);
}
