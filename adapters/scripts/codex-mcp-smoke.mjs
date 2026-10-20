import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { installCodexTelemetry } from '../src/codex-telemetry-config.ts';
import { installCodexProjectPlanGateHook, removeCodexProjectPlanGateHook } from '../src/codex-hooks-config.ts';

const fixtureKeyName = 'THREADFORGE_CODEX_FIXTURE_KEY';
const fixtureKey = 'threadforge-codex-local-fixture-only';
const providerID = 'threadforge-fixture';
const modelID = 'threadforge-codex-fixture-model';

export async function runCodexAgentMcpRead({
  projectRoot, codexHome, cliPath, databasePath, isolatedEnvironment, procedureID, expectedGuidance,
  toolName = 'threadforge_procedure_get', toolArguments, prompt, expectedFinalResponse,
  requiredResultMarkers = [], hostVersion = 'unknown',
}) {
  const mcpArguments = toolArguments ?? { procedure_id: procedureID };
  const promptText = prompt ?? `Read approved procedure ${procedureID} using Threadforge MCP, then repeat its first guidance sentence exactly. Do not use shell or modify files.`;
  const finalResponse = expectedFinalResponse ?? expectedGuidance;
  let completionRequests = 0;
  let auxiliaryRequests = 0;
  let fixtureToolCalls = 0;
  let toolResultObserved = false;
  let authorizationSafe = true;
  let fixtureError;
  const providerShapes = [];
  const telemetry = { logs: 0, traces: 0, log_records: 0, spans: 0, local_turn_cost_posts: 0 };
  const telemetrySensitiveFields = new Set();
  const telemetryMarkers = [
    ['user_prompt_text', promptText],
    ['assistant_or_memory_text', expectedGuidance],
    ['fixture_key', fixtureKey],
  ];
  const inspectTelemetryBody = (signal, body) => {
    const serialized = JSON.stringify(body);
    for (const [marker, value] of telemetryMarkers) {
      if (value && serialized.includes(value)) telemetrySensitiveFields.add(`${signal}:${marker}`);
    }
  };
  const server = createServer((request, response) => {
    if (request.headers.authorization && request.headers.authorization !== `Bearer ${fixtureKey}`) authorizationSafe = false;
    const chunks = [];
    let requestBytes = 0;
    request.on('data', (chunk) => {
      requestBytes += chunk.length;
      if (requestBytes <= 16 << 20) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (requestBytes > 16 << 20) throw new Error('Codex fixture request exceeded its local size bound');
        const pathname = new URL(request.url ?? '/', 'http://127.0.0.1').pathname;
        if (request.method === 'POST' && pathname === '/v1/analytics/codex/turn-costs') {
          const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
          inspectTelemetryBody('local_turn_costs', body);
          telemetry.local_turn_cost_posts++;
          writeJSON(response, { ok: true });
          return;
        }
        if (request.method === 'POST' && (pathname === '/v1/logs' || pathname === '/v1/traces')) {
          const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
          const signal = pathname === '/v1/logs' ? 'logs' : 'traces';
          const resources = signal === 'logs' ? body.resourceLogs : body.resourceSpans;
          if (!Array.isArray(resources)) throw new Error(`Codex OTLP ${signal} request did not use the JSON resource envelope`);
          inspectTelemetryBody(signal, body);
          telemetry[signal]++;
          if (signal === 'logs') {
            telemetry.log_records += resources.reduce((total, resource) => total +
              (Array.isArray(resource?.scopeLogs) ? resource.scopeLogs.reduce((sum, scope) =>
                sum + (Array.isArray(scope?.logRecords) ? scope.logRecords.length : 0), 0) : 0), 0);
          } else {
            telemetry.spans += resources.reduce((total, resource) => total +
              (Array.isArray(resource?.scopeSpans) ? resource.scopeSpans.reduce((sum, scope) =>
                sum + (Array.isArray(scope?.spans) ? scope.spans.length : 0), 0) : 0), 0);
          }
          if (telemetry.logs + telemetry.traces + telemetry.local_turn_cost_posts > 256) {
            throw new Error('Codex telemetry fixture exceeded its request-count bound');
          }
          writeJSON(response, { partialSuccess: {} });
          return;
        }
        if (request.method === 'GET' && /\/models$/.test(request.url ?? '')) {
          writeJSON(response, { object: 'list', data: [{ id: modelID, object: 'model', created: 0, owned_by: 'threadforge-fixture' }] });
          return;
        }
        if (request.method !== 'POST' || !/\/responses(?:\?.*)?$/.test(request.url ?? '')) {
          fixtureError = `Codex requested an unexpected local fixture-provider endpoint (${request.method} ${pathname})`;
          throw new Error(fixtureError);
        }

        const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const input = Array.isArray(body.input) ? body.input : [];
        const tools = Array.isArray(body.tools) ? body.tools : [];
        const functionOutputs = input.filter((item) => item?.type === 'function_call_output');
        completionRequests++;
        if (providerShapes.length < 12) {
          providerShapes.push({
            body_keys: Object.keys(body).sort().slice(0, 16),
            input_types: input.map((item) => typeof item?.type === 'string' ? item.type : 'unknown').slice(-16),
            tool_count: tools.length,
            target_tool_available: tools.some((tool) => {
              if ((tool?.name ?? tool?.function?.name) === toolName) return true;
              return tool?.type === 'namespace' && tool?.name === 'mcp__threadforge' &&
                Array.isArray(tool?.tools) && tool.tools.some((nested) => nested?.name === toolName);
            }),
            function_call_output_count: functionOutputs.length,
            function_calls: input.filter((item) => item?.type === 'function_call').map((item) => ({
              expected_tool: item.name === toolName,
              namespaced: typeof item.namespace === 'string',
              call_id_present: typeof item.call_id === 'string',
              argument_keys: parseArgumentKeys(item.arguments),
            })).slice(-8),
            function_outputs: functionOutputs.map((item) => summarizeFunctionOutput(item, expectedGuidance)).slice(-8),
            previous_response_id_present: typeof body.previous_response_id === 'string',
            stream: body.stream === true,
          });
        }

        if (functionOutputs.length > 0) {
          const resultText = functionOutputs.flatMap((item) => collectOutputText(item.output)).join('\n');
          const compactResultText = resultText.replace(/\s+/g, '');
          const containsGuidance = resultText.includes(expectedGuidance) ||
            resultText.includes(expectedGuidance.replace(/[.!?]+$/u, ''));
          const markersPresent = requiredResultMarkers.every((marker) => compactResultText.includes(marker.replace(/\s+/g, '')));
          if (!containsGuidance || !markersPresent) {
            fixtureError = toolName === 'threadforge_task_resume_context'
              ? 'Codex task-resume result omitted expected advisory fields'
              : 'Codex MCP tool result did not contain the expected approved procedure guidance';
            sendResponse(response, body, { content: 'The Threadforge MCP read did not return the expected content and safety fields.' });
            return;
          }
          toolResultObserved = true;
          sendResponse(response, body, { content: finalResponse });
          return;
        }

        const tool = tools.find((candidate) => {
          const name = candidate?.name ?? candidate?.function?.name;
          return typeof name === 'string' && name === toolName;
        });
        let functionToolName = tool?.name ?? tool?.function?.name;
        let toolNamespace;
        if (typeof functionToolName !== 'string') {
          const namespace = tools.find((candidate) => candidate?.type === 'namespace' && candidate?.name === 'mcp__threadforge');
          const nested = namespace?.tools?.find((candidate) => candidate?.name === toolName);
          if (nested) {
            functionToolName = nested.name;
            toolNamespace = namespace.name;
          }
        }
        if (typeof functionToolName !== 'string') {
          if (tools.length === 0 && auxiliaryRequests < 2 && fixtureToolCalls === 0) {
            auxiliaryRequests++;
            sendResponse(response, body, { content: 'Threadforge procedure verification' });
            return;
          }
          fixtureError = `Codex did not expose ${toolName} as a function tool`;
          throw new Error(fixtureError);
        }
        if (fixtureToolCalls >= 1) throw new Error('Codex attempted to repeat the Threadforge procedure tool call');
        fixtureToolCalls++;
        sendResponse(response, body, {
          functionCall: {
            callID: `call_${toolName}`, name: functionToolName,
            namespace: toolNamespace,
            arguments: JSON.stringify(mcpArguments),
          },
        });
      } catch (error) {
        fixtureError ??= String(error?.message ?? 'Codex deterministic provider fixture failed').slice(0, 256);
        writeJSON(response, { error: { message: 'deterministic local Codex provider fixture failed' } }, 500);
      }
    });
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  try {
    const address = server.address();
    assert(address && typeof address === 'object');
    const endpoint = `http://127.0.0.1:${address.port}/v1`;

    mkdirSync(codexHome, { recursive: true });
    const configPath = path.join(codexHome, 'config.toml');
    const config = [
      `model = ${tomlString(modelID)}`,
      `model_provider = ${tomlString(providerID)}`,
      'sandbox_mode = "read-only"',
      '',
      `[model_providers.${providerID}]`,
      'name = "Threadforge deterministic local smoke provider"',
      `base_url = ${tomlString(endpoint)}`,
      `env_key = ${tomlString(fixtureKeyName)}`,
      'wire_api = "responses"',
      'requires_openai_auth = false',
      'request_max_retries = 0',
      'stream_max_retries = 0',
      'stream_idle_timeout_ms = 120000',
      '',
      `[projects.${tomlString(projectRoot)}]`,
      'trust_level = "trusted"',
      '',
    ].join('\n');
    writeFileSync(configPath, config, { mode: 0o600 });
    const telemetryEndpoint = new URL(endpoint).origin;
    assert.equal(await installCodexTelemetry({ codexHome, endpoint: telemetryEndpoint }), 'installed');

    const environment = { ...isolatedEnvironment, CODEX_HOME: codexHome, [fixtureKeyName]: fixtureKey };
    const listing = await runCodex(['mcp', 'list', '--json'], { cwd: projectRoot, env: environment, timeout: 45_000 });
    assert.match(listing.stdout, /threadforge/i, 'Codex MCP inventory did not include the project Threadforge server');

    const run = await runCodex([
      'exec', '--json', '--ephemeral', '--sandbox', 'read-only', '--cd', projectRoot,
      '--skip-git-repo-check', '--model', modelID, promptText,
    ], { cwd: projectRoot, env: environment, timeout: 120_000 });
    if (fixtureError) throw new Error(fixtureError);
    assert.equal(authorizationSafe, true, 'Codex sent unexpected authorization data to the loopback fixture');
    assert.equal(fixtureToolCalls, 1, `expected exactly one scripted ${toolName} tool call`);
    assert.equal(toolResultObserved, true, 'fixture provider did not observe the MCP procedure result');
    assert.equal(completionRequests, auxiliaryRequests + 2,
      'expected only tool-less auxiliary requests, one MCP tool call, and one final response');
    assert.ok(run.stdout.includes(expectedFinalResponse ?? expectedGuidance), `Codex final output did not contain the expected ${toolName} summary`);
    assert.ok(telemetry.logs > 0 && telemetry.log_records > 0,
      `Codex did not deliver OTLP logs to the loopback collector: ${JSON.stringify(telemetry)}`);
    assert.ok(telemetry.traces > 0 && telemetry.spans > 0,
      `Codex did not deliver OTLP traces to the loopback collector: ${JSON.stringify(telemetry)}`);
    const sensitiveTelemetryFields = [...telemetrySensitiveFields].sort();
    assert.equal(sensitiveTelemetryFields.some((field) => field.endsWith(':fixture_key')), false,
      `Codex telemetry included the local fixture credential marker: ${JSON.stringify(sensitiveTelemetryFields)}`);
    assert.equal(sensitiveTelemetryFields.some((field) => field.endsWith(':user_prompt_text')), false,
      `Codex exported prompt text despite log_user_prompt=false: ${JSON.stringify(sensitiveTelemetryFields)}`);
    return {
      ...(toolName === 'threadforge_task_resume_context' ? {
        schema_version: 'threadforge.codex-task-resume-smoke.v1',
        host_cli_version: hostVersion,
        task_context_content_retained_in_report: false,
        advisory_result_markers_verified: requiredResultMarkers.length > 0 && toolResultObserved,
        requirements_not_finalized_warning_verified: requiredResultMarkers.includes('requirements_not_finalized') && toolResultObserved,
        action_ready: false,
        guidance_only: true,
        execution_authorized: false,
      } : {}),
      status: 'verified', provider: 'deterministic-loopback-fixture',
      codex_mcp_configured: true, tool: toolName,
      tool_call_count: fixtureToolCalls, tool_result_observed: toolResultObserved,
      completion_requests: completionRequests, auxiliary_completion_requests: auxiliaryRequests,
      telemetry: {
        status: 'verified', endpoint: 'loopback-only', log_batches: telemetry.logs,
        trace_batches: telemetry.traces, log_records: telemetry.log_records, spans: telemetry.spans,
        local_turn_cost_posts: telemetry.local_turn_cost_posts,
        user_prompt_text_detected: sensitiveTelemetryFields.some((field) => field.endsWith(':user_prompt_text')),
        approved_guidance_text_detected: sensitiveTelemetryFields.some((field) => field.endsWith(':assistant_or_memory_text')),
        fixture_key_detected: sensitiveTelemetryFields.some((field) => field.endsWith(':fixture_key')),
      },
      real_model_invocations: 0,
    };
  } catch (error) {
    throw new Error(`${error?.message ?? 'Codex MCP read failed'}; provider_shapes=${JSON.stringify(providerShapes)}`);
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

export async function runCodexAgentPlanGateAttempt({
  projectRoot, codexHome, cliPath, databasePath, isolatedEnvironment, taskID, projectID,
  targetRelativePath = 'src/__threadforge_codex_plan_gate_denied__.txt', hostVersion = 'unknown',
}) {
  const targetPath = path.join(projectRoot, ...targetRelativePath.split('/'));
  const taskArgs = [
    'task', 'plan', 'guard', '--host', 'codex-cli', '--task-id', taskID, '--project-id', projectID,
    '--project-root', projectRoot, '--db', databasePath,
    '--threadforge-managed-codex-plan-gate=v1', '--threadforge-plan-gate=codex-v1',
  ];
  const command = process.platform === 'win32'
    ? 'powershell.exe -NoProfile -NonInteractive -Command "' +
      '& ' + [cliPath, ...taskArgs].map(quotePowerShell).join(' ') + '"'
    : [cliPath, ...taskArgs].map(quotePosix).join(' ');
  const installResult = await installCodexProjectPlanGateHook(projectRoot, command);
  let completionRequests = 0;
  let auxiliaryRequests = 0;
  let execCommandRequests = 0;
  let execCommandToolAvailable;
  let denialFeedbackObserved = false;
  let fixtureError;
  const providerShapes = [];
  const server = createServer((request, response) => {
    const chunks = [];
    let requestBytes = 0;
    request.on('data', (chunk) => {
      requestBytes += chunk.length;
      if (requestBytes <= 8 << 20) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (requestBytes > 8 << 20) throw new Error('Codex plan-gate fixture request exceeded its local size bound');
        const pathname = new URL(request.url ?? '/', 'http://127.0.0.1').pathname;
        if (request.method === 'GET' && /\/models$/.test(pathname)) {
          writeJSON(response, { object: 'list', data: [{ id: modelID, object: 'model', created: 0, owned_by: 'threadforge-fixture' }] });
          return;
        }
        if (request.method !== 'POST' || !/\/responses(?:\?.*)?$/.test(request.url ?? '')) {
          throw new Error('Codex plan-gate fixture received an unexpected provider endpoint');
        }
        const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const input = Array.isArray(body.input) ? body.input : [];
        const toolOutputs = input.filter((item) =>
          item?.type === 'custom_tool_call_output' || item?.type === 'function_call_output');
        const outputText = toolOutputs.flatMap((item) => collectOutputText(item.output)).join('\n');
        if (toolOutputs.length > 0 && providerShapes.length < 8) {
          const loweredOutput = outputText.toLowerCase();
          providerShapes.push({
            tool_output_count: toolOutputs.length,
            output_bytes: Buffer.byteLength(outputText),
            output_item_types: toolOutputs.map((item) => item.type),
            is_error: toolOutputs.some((item) => item.is_error === true),
            threadforge_word_present: loweredOutput.includes('threadforge'),
            hook_word_present: loweredOutput.includes('hook'),
            threadforge_denial_coupled: /threadforge.{0,120}(denied|withheld|blocked)|(?:denied|withheld|blocked).{0,120}threadforge/i.test(outputText),
            threadforge_denial_reason_present: loweredOutput.includes('threadforge plan gate withheld this file edit'),
            pre_tool_use_denial_present: loweredOutput.includes('denied by pretooluse hook'),
            permission_denial_present: loweredOutput.includes('permission denied') || loweredOutput.includes('permissiondecision'),
            approval_word_present: loweredOutput.includes('approval'),
            sandbox_word_present: loweredOutput.includes('sandbox'),
            access_denied_present: /access.{0,24}denied|unauthorizedaccess/i.test(outputText),
            denied_word_present: loweredOutput.includes('denied') || loweredOutput.includes('withheld'),
            target_file_created: existsSync(targetPath),
          });
        }
        const threadforgeDenial = /threadforge.{0,120}(denied|withheld|blocked)|(?:denied|withheld|blocked).{0,120}threadforge/i.test(outputText);
        const denied = /Threadforge plan gate withheld this file edit/i.test(outputText) ||
          (threadforgeDenial && !/access.{0,24}denied|unauthorizedaccess/i.test(outputText)) ||
          ((/denied|withheld/i.test(outputText)) && /hook|pre.?tool.?use/i.test(outputText));
        if (toolOutputs.length > 0) {
          if (!denied) {
            fixtureError = 'Codex did not return the Threadforge PreToolUse denial to the loopback provider';
            sendResponse(response, body, { content: 'The task-plan gate denial was not observed.' });
            return;
          }
          denialFeedbackObserved = true;
          completionRequests++;
          sendResponse(response, body, { content: 'The shell command was denied by the Threadforge task-plan gate.' });
          return;
        }
        const tools = Array.isArray(body.tools) ? body.tools : [];
        if (providerShapes.length < 8) {
          providerShapes.push({
            input_types: input.map((item) => typeof item?.type === 'string' ? item.type : 'unknown').slice(-12),
            tool_shapes: tools.map((tool) => ({
              type: typeof tool?.type === 'string' ? tool.type : 'unknown',
              name: typeof tool?.name === 'string' ? tool.name : '',
              parameter_keys: Object.keys(tool?.function?.parameters?.properties ?? tool?.parameters?.properties ?? {}).slice(0, 24),
              required_keys: Array.isArray(tool?.function?.parameters?.required)
                ? tool.function.parameters.required.slice(0, 16)
                : Array.isArray(tool?.parameters?.required) ? tool.parameters.required.slice(0, 16) : [],
            })).slice(0, 24),
            custom_tool_outputs: toolOutputs.length,
          });
        }
        if (tools.length === 0 && auxiliaryRequests < 2 && execCommandRequests === 0) {
          auxiliaryRequests++;
          completionRequests++;
          sendResponse(response, body, { content: 'Synthetic Codex task-plan gate verification.' });
          return;
        }
        const execCommand = tools.find((tool) => tool?.name === 'exec_command' || tool?.function?.name === 'exec_command');
        if (!execCommand) {
          execCommandToolAvailable = false;
          completionRequests++;
          sendResponse(response, body, {
            content: 'This Codex CLI run did not expose its exec_command tool; no shell tool was invoked.',
          });
          return;
        }
        execCommandToolAvailable = true;
        if (execCommandRequests > 0) throw new Error('Codex requested exec_command more than once in the synthetic fixture');
        execCommandRequests++;
        completionRequests++;
        const argumentsValue = buildCodexExecCommandArguments(execCommand, projectRoot, targetPath);
        sendResponse(response, body, {
          functionCall: {
            callID: 'call_threadforge_plan_gate_exec_command', name: 'exec_command',
            arguments: JSON.stringify(argumentsValue),
          },
        });
      } catch (error) {
        fixtureError ??= String(error?.message ?? 'Codex deterministic plan-gate fixture failed').slice(0, 256);
        writeJSON(response, { error: { message: 'deterministic local Codex plan-gate fixture failed' } }, 500);
      }
    });
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  try {
    const address = server.address();
    assert(address && typeof address === 'object');
    const endpoint = 'http://127.0.0.1:' + address.port + '/v1';
    mkdirSync(codexHome, { recursive: true });
    const config = [
      'model = ' + tomlString(modelID),
      'model_provider = ' + tomlString(providerID),
      'sandbox_mode = "workspace-write"',
      '',
      '[features]',
      'hooks = true',
      '',
      '[model_providers.' + providerID + ']',
      'name = "Threadforge deterministic local plan-gate smoke provider"',
      'base_url = ' + tomlString(endpoint),
      'env_key = ' + tomlString(fixtureKeyName),
      'wire_api = "responses"',
      'requires_openai_auth = false',
      'request_max_retries = 0',
      'stream_max_retries = 0',
      'stream_idle_timeout_ms = 120000',
      '',
      '[projects.' + tomlString(projectRoot) + ']',
      'trust_level = "trusted"',
      '',
    ].join('\n');
    writeFileSync(path.join(codexHome, 'config.toml'), config, { mode: 0o600 });
    const environment = { ...isolatedEnvironment, CODEX_HOME: codexHome, [fixtureKeyName]: fixtureKey };
    const prompt = 'Use exec_command exactly once to create a file at ' + targetRelativePath +
      ' containing the single line synthetic plan-only edit. Do not use any other tool.';
    const run = await runCodex([
      '--dangerously-bypass-hook-trust', '--ask-for-approval', 'never', 'exec',
      '--json', '--ephemeral', '--sandbox', 'workspace-write',
      '--cd', projectRoot, '--skip-git-repo-check', '--model', modelID, prompt,
    ], { cwd: projectRoot, env: environment, timeout: 120_000 });
    if (fixtureError) throw new Error(fixtureError);
    if (execCommandToolAvailable !== true) {
      assert.equal(existsSync(targetPath), false, 'Codex created the target despite no exec_command tool being exposed');
      return {
        schema_version: 'threadforge.codex-cli-plan-gate-smoke.v1',
        status: 'unsupported', host_cli_version: hostVersion,
        install_action: installResult, hook_event: 'PreToolUse', tool: 'exec_command',
        tool_calls: 0, target_tool_available: false,
        denial_feedback_observed: false, denied_file_created: false,
        unsupported_reason: 'exec-command-not-exposed',
        provider: 'deterministic-loopback-fixture', real_model_invocations: 0,
      };
    }
    assert.equal(execCommandRequests, 1, 'Codex did not make exactly one synthetic exec_command request');
    assert.equal(denialFeedbackObserved, true, 'Codex did not return the plan-gate denial to the provider');
    assert.equal(existsSync(targetPath), false, 'Codex created the file despite the plan-only denial');
    assert.match(run.stdout.toLowerCase(), /denied|withheld/,
      'Codex final output did not reflect the synthetic plan-gate denial');
    return {
      schema_version: 'threadforge.codex-cli-plan-gate-smoke.v1',
      status: 'verified', host_cli_version: hostVersion,
      install_action: installResult, hook_event: 'PreToolUse', tool: 'exec_command',
      tool_calls: execCommandRequests, target_tool_available: true,
      denial_feedback_observed: denialFeedbackObserved,
      denied_file_created: false, provider: 'deterministic-loopback-fixture',
      real_model_invocations: 0,
    };
  } catch (error) {
    throw new Error((error?.message ?? 'Codex plan-gate smoke failed') +
      '; provider_shapes=' + JSON.stringify(providerShapes));
  } finally {
    await removeCodexProjectPlanGateHook(projectRoot).catch(() => {});
    await new Promise((resolve) => server.close(resolve));
  }
}

function buildCodexExecCommandArguments(tool, projectRoot, targetPath) {
  const parameters = tool?.function?.parameters ?? tool?.parameters ?? {};
  const properties = parameters.properties ?? {};
  const commandKey = ['cmd', 'command'].find((key) => Object.hasOwn(properties, key));
  if (!commandKey) {
    throw new Error('Codex exec_command schema did not expose a supported command field; keys=' +
      Object.keys(properties).sort().slice(0, 32).join(','));
  }
  const escapedTarget = process.platform === 'win32'
    ? targetPath.replaceAll("'", "''")
    : targetPath;
  const powershellScript = 'Set-Content -LiteralPath ' + quotePowerShell(escapedTarget) +
    ' -Value ' + quotePowerShell('synthetic plan-only edit');
  const posixScript = 'printf %s ' + quotePosix('synthetic plan-only edit') +
    ' > ' + quotePosix(escapedTarget);
  const commandString = process.platform === 'win32'
    ? 'powershell.exe -NoProfile -NonInteractive -Command "' + powershellScript + '"'
    : 'sh -c ' + quotePosix(posixScript);
  const commandSchema = properties[commandKey];
  let commandValue;
  if (commandSchema?.type === 'string') {
    commandValue = commandString;
  } else if (commandSchema?.type === 'array' && commandSchema.items?.type === 'string') {
    commandValue = process.platform === 'win32'
      ? ['powershell.exe', '-NoProfile', '-NonInteractive', '-Command', powershellScript]
      : ['sh', '-c', posixScript];
  } else {
    throw new Error('Codex exec_command command field has an unsupported schema type');
  }
  const result = { [commandKey]: commandValue };
  const required = Array.isArray(parameters.required) ? parameters.required : [];
  for (const key of required) {
    if (key === commandKey) continue;
    if (key === 'workdir' || key === 'cwd') result[key] = projectRoot;
    else if (key === 'max_output_tokens') result[key] = 256;
    else if (key === 'yield_time_ms') result[key] = 1000;
    else if (key === 'timeout_ms') result[key] = 5000;
    else if (key === 'login' || key === 'tty') result[key] = false;
    else if (key === 'shell') result[key] = process.platform === 'win32' ? 'powershell.exe' : 'sh';
    else throw new Error('Codex exec_command has an unsupported required argument: ' + key);
  }
  if (Object.hasOwn(properties, 'workdir')) result.workdir = projectRoot;
  if (Object.hasOwn(properties, 'cwd')) result.cwd = projectRoot;
  return result;
}

function sendResponse(response, requestBody, result) {
  const responseID = `resp_threadforge_${completionResponseCounter++}`;
  const createdAt = Math.floor(Date.now() / 1000);
  const output = result.customToolCall
    ? [{
      id: 'ctc_' + responseID, type: 'custom_tool_call', status: 'completed',
      call_id: result.customToolCall.callID, name: result.customToolCall.name, input: result.customToolCall.input,
    }]
    : result.functionCall
    ? [{
      id: `fc_${responseID}`, type: 'function_call', status: 'completed',
      call_id: result.functionCall.callID, name: result.functionCall.name,
      ...(typeof result.functionCall.namespace === 'string' ? { namespace: result.functionCall.namespace } : {}),
      arguments: result.functionCall.arguments,
    }]
    : [{
      id: `msg_${responseID}`, type: 'message', status: 'completed', role: 'assistant',
      content: [{ type: 'output_text', text: result.content, annotations: [] }],
    }];
  const completed = {
    id: responseID, object: 'response', created_at: createdAt, status: 'completed',
    model: typeof requestBody.model === 'string' ? requestBody.model : modelID,
    output, parallel_tool_calls: false, usage: {
      input_tokens: 0, input_tokens_details: { cached_tokens: 0 },
      output_tokens: 0, output_tokens_details: { reasoning_tokens: 0 }, total_tokens: 0,
    },
  };
  if (requestBody.stream === true) {
    sendResponseStream(response, completed, result);
    return;
  }
  writeJSON(response, completed);
}

let completionResponseCounter = 1;

function sendResponseStream(response, completed, result) {
  response.writeHead(200, {
    'content-type': 'text/event-stream; charset=utf-8',
    'cache-control': 'no-cache',
    connection: 'close',
  });
  let sequence = 0;
  const emit = (type, fields = {}) => response.write(
    `event: ${type}\ndata: ${JSON.stringify({ type, sequence_number: sequence++, ...fields })}\n\n`,
  );
  emit('response.created', { response: { ...completed, status: 'in_progress', output: [] } });
  emit('response.in_progress', { response: { ...completed, status: 'in_progress', output: [] } });
  if (result.customToolCall) {
    const item = completed.output[0];
    emit('response.output_item.added', {
      output_index: 0,
      item: { ...item, status: 'in_progress', input: '' },
    });
    emit('response.custom_tool_call_input.delta', {
      item_id: item.id, output_index: 0, delta: item.input,
    });
    emit('response.custom_tool_call_input.done', {
      item_id: item.id, output_index: 0, input: item.input,
    });
    emit('response.output_item.done', { output_index: 0, item });
  } else if (result.functionCall) {
    const item = completed.output[0];
    emit('response.output_item.added', {
      output_index: 0,
      item: { ...item, status: 'in_progress', arguments: '' },
    });
    emit('response.function_call_arguments.delta', {
      item_id: item.id, output_index: 0, delta: item.arguments,
    });
    emit('response.function_call_arguments.done', {
      item_id: item.id, output_index: 0, arguments: item.arguments,
    });
    emit('response.output_item.done', { output_index: 0, item });
  } else {
    const item = completed.output[0];
    const part = item.content[0];
    emit('response.output_item.added', {
      output_index: 0, item: { ...item, status: 'in_progress', content: [] },
    });
    emit('response.content_part.added', {
      item_id: item.id, output_index: 0, content_index: 0,
      part: { type: 'output_text', text: '', annotations: [] },
    });
    emit('response.output_text.delta', {
      item_id: item.id, output_index: 0, content_index: 0, delta: part.text,
    });
    emit('response.output_text.done', {
      item_id: item.id, output_index: 0, content_index: 0, text: part.text,
    });
    emit('response.content_part.done', {
      item_id: item.id, output_index: 0, content_index: 0, part,
    });
    emit('response.output_item.done', { output_index: 0, item });
  }
  emit('response.completed', { response: completed });
  response.end('data: [DONE]\n\n');
}

function writeJSON(response, value, statusCode = 200) {
  const body = JSON.stringify(value);
  response.writeHead(statusCode, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': Buffer.byteLength(body),
    connection: 'close',
  }).end(body);
}

function tomlString(value) {
  return `"${String(value).replaceAll('\\', '\\\\').replaceAll('"', '\\"').replaceAll('\n', '\\n').replaceAll('\r', '\\r')}"`;
}

function quotePowerShell(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function quotePosix(value) {
  return "'" + String(value).replaceAll("'", "'\\''") + "'";
}

function runCodex(args, { cwd, env, timeout }) {
  const windows = process.platform === 'win32';
  const command = windows ? 'pwsh.exe' : 'codex';
  const commandArgs = windows
    ? ['-NoProfile', '-NonInteractive', '-Command', `& 'codex' ${args.map(quotePowerShell).join(' ')}`]
    : args;
  return new Promise((resolve, reject) => {
    const child = spawn(command, commandArgs, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'] });
    const stdout = [];
    const stderr = [];
    let stdoutBytes = 0;
    let stderrBytes = 0;
    let settled = false;
    const finishError = (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(error);
    };
    const timer = setTimeout(() => {
      child.kill();
      const detail = Buffer.concat(stderr).toString('utf8').slice(-1024).trim();
      finishError(new Error(`Codex exceeded the ${timeout} ms fixture timeout${detail ? `: ${detail}` : ''}`));
    }, timeout);
    child.once('error', finishError);
    child.stdout.on('data', (chunk) => {
      stdoutBytes += chunk.length;
      if (stdoutBytes > 4 << 20) {
        child.kill();
        finishError(new Error('Codex fixture stdout exceeded its local size bound'));
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
      if (code !== 0) reject(new Error(
        `Codex exited ${code}: ${err.trim().slice(-1200)}; stdout_events=${JSON.stringify(summarizeEvents(out))}`,
      ));
      else resolve({ stdout: out, stderr: err });
    });
  });
}

function summarizeEvents(output) {
  const summaries = [];
  for (const line of output.split(/\r?\n/)) {
    try {
      const event = JSON.parse(line);
      summaries.push({
        type: typeof event?.type === 'string' ? event.type : undefined,
        item_type: typeof event?.item?.type === 'string' ? event.item.type : undefined,
        name: typeof event?.item?.name === 'string' ? event.item.name : undefined,
        status: typeof event?.status === 'string' ? event.status : undefined,
        error: typeof event?.message === 'string' ? event.message.slice(0, 160) : undefined,
      });
      if (summaries.length >= 24) break;
    } catch { /* Ignore non-JSON progress output; never include raw agent text in diagnostics. */ }
  }
  return summaries;
}

function parseArgumentKeys(value) {
  if (typeof value !== 'string') return [];
  try { return Object.keys(JSON.parse(value)).slice(0, 32); } catch { return []; }
}

function summarizeFunctionOutput(item, expectedGuidance) {
  const output = item?.output;
  let parsed = output;
  if (typeof output === 'string') {
    try { parsed = JSON.parse(output); } catch { parsed = undefined; }
  }
  const textValues = collectOutputText(output);
  return {
    item_keys: Object.keys(item ?? {}).sort(),
    output_type: Array.isArray(output) ? 'array' : output === null ? 'null' : typeof output,
    output_bytes: typeof output === 'string' ? Buffer.byteLength(output) : undefined,
    output_keys: parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? Object.keys(parsed).slice(0, 40) : [],
    is_error: item?.isError === true || parsed?.isError === true || typeof parsed?.error === 'string' || parsed?.error != null,
    output_terms: [...new Set(textValues.join(' ').toLowerCase().match(/[a-z][a-z_-]{2,}/gu) ?? [])].slice(0, 20),
    expected_guidance_present: textValues.some((value) => value.includes(expectedGuidance)),
    expected_guidance_without_terminal_punctuation_present: textValues.some((value) =>
      value.includes(expectedGuidance.replace(/[.!?]+$/u, ''))),
    text_piece_bytes: textValues.map((value) => Buffer.byteLength(value)).slice(0, 12),
    text_piece_count: textValues.length,
  };
}

function collectOutputText(value, depth = 0) {
  if (depth > 6 || value == null) return [];
  if (typeof value === 'string') return [value];
  if (Array.isArray(value)) return value.flatMap((item) => collectOutputText(item, depth + 1)).slice(0, 32);
  if (typeof value !== 'object') return [];
  const contentKeys = ['output', 'content', 'text', 'structuredContent', 'structured_content', 'result', 'data'];
  return contentKeys.flatMap((key) => Object.hasOwn(value, key) ? collectOutputText(value[key], depth + 1) : []).slice(0, 32);
}
