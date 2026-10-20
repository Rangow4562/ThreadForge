import assert from 'node:assert/strict';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { mkdirSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { gunzipSync } from 'node:zlib';
import { installGeminiTelemetry } from '../src/gemini-telemetry-config.ts';
import { installGeminiProjectMemoryHook, installGeminiProjectPlanGateHook } from '../src/gemini-memory-config.ts';

const fixtureModel = 'gemini-2.5-flash';
const fixtureKey = `AIzaSy${'0'.repeat(33)}`;
const outputMarker = 'THREADFORGE_GEMINI_TELEMETRY_SMOKE_OK';
const maxRequestBytes = 16 << 20;

export async function runGeminiCliTelemetrySmoke({ smokeRoot, isolatedEnvironment, runGemini, version, cliPath, taskFixture }) {
  const home = path.join(smokeRoot, 'gemini-telemetry-home');
  const project = path.join(smokeRoot, 'gemini-telemetry-project');
  const planGateProject = path.join(smokeRoot, 'gemini-plan-gate-project');
  const prompt = `Return exactly this marker and nothing else: ${outputMarker}`;
  const telemetry = { logs: 0, traces: 0, metrics: 0, payload_bytes: 0, content_types: new Set() };
  const routeCounts = {};
  const sensitiveFields = new Set();
  const sensitiveFieldPaths = new Set();
  let fixtureError;

  const server = createServer((request, response) => {
    const chunks = [];
    let requestBytes = 0;
    request.on('data', (chunk) => {
      requestBytes += chunk.length;
      if (requestBytes <= maxRequestBytes) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (requestBytes > maxRequestBytes) throw new Error('Gemini fixture request exceeded its local size bound');
        const url = new URL(request.url ?? '/', 'http://127.0.0.1');
        const pathname = url.pathname;
        const signal = ({ '/v1/logs': 'logs', '/v1/traces': 'traces', '/v1/metrics': 'metrics' })[pathname];
        if (request.method === 'POST' && signal) {
          routeCounts[`otlp.${signal}`] = (routeCounts[`otlp.${signal}`] ?? 0) + 1;
          const raw = Buffer.concat(chunks);
          if (raw.length === 0) throw new Error('Gemini OTLP fixture received an empty signal payload');
          const encoding = String(request.headers['content-encoding'] ?? '').toLowerCase();
          const payload = encoding === 'gzip' ? gunzipSync(raw) : raw;
          const markers = [
            ['user_prompt_text', Buffer.from(prompt)],
            ['model_output_text', Buffer.from(outputMarker)],
          ];
          for (const [marker, value] of markers) {
            if (payload.includes(value)) sensitiveFields.add(`${signal}:${marker}`);
          }
          if (String(request.headers['content-type'] ?? '').toLowerCase().includes('json')) {
            try {
              collectSensitiveFieldPaths(JSON.parse(payload.toString('utf8')), signal, '', prompt, outputMarker, sensitiveFieldPaths);
            } catch {
              fixtureError ??= `Gemini OTLP ${signal} JSON payload was not valid JSON`;
            }
          }
          telemetry[signal]++;
          telemetry.payload_bytes += raw.length;
          telemetry.content_types.add(String(request.headers['content-type'] ?? 'unknown').split(';', 1)[0].toLowerCase());
          if (signal === 'logs') writeJson(response, { partialSuccess: {} });
          else response.writeHead(200, { connection: 'close' }).end();
          return;
        }

        routeCounts.unexpected = (routeCounts.unexpected ?? 0) + 1;
        fixtureError ??= `unexpected local Gemini fixture route (${request.method} ${pathname})`;
        writeJson(response, { error: { code: 404, message: 'unexpected local Gemini fixture route' } }, 404);
      } catch (error) {
        fixtureError ??= String(error?.message ?? 'Gemini local fixture failed').slice(0, 256);
        writeJson(response, { error: { code: 500, message: 'deterministic local Gemini fixture failed' } }, 500);
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
    const origin = `http://127.0.0.1:${address.port}`;
    mkdirSync(home, { recursive: true, mode: 0o700 });
    mkdirSync(project, { recursive: true, mode: 0o700 });
    mkdirSync(planGateProject, { recursive: true, mode: 0o700 });
    const fakeResponsesPath = path.join(smokeRoot, 'gemini-fake-responses.jsonl');
    const fakeResponse = {
      method: 'generateContentStream',
      response: [{
        candidates: [{
          content: { role: 'model', parts: [{ text: outputMarker }] },
          finishReason: 'STOP',
        }],
        usageMetadata: { promptTokenCount: 1, candidatesTokenCount: 1, totalTokenCount: 2 },
        modelVersion: fixtureModel,
      }],
    };
    writeFileSync(fakeResponsesPath, `${JSON.stringify(fakeResponse)}\n`, { mode: 0o600, flag: 'wx' });

    const hookObserverPath = path.join(smokeRoot, 'gemini-task-resume-hook-observer.mjs');
    const hookObserverResultPath = path.join(smokeRoot, 'gemini-task-resume-hook-observer.json');
    const hookObserverSource = `import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
const [cliPath, resultPath, projectID, taskID] = process.argv.slice(2);
const markers = [
  ['task_objective_observed', 'Synthetic objective marker: preserve project guidance across harnesses.'],
  ['requirements_not_finalized_warning_verified', 'requirements are not finalized'],
  ['advisory_only_marker_verified', 'This is advisory context only'],
  ['non_authorization_marker_verified', 'does not authorize commands, tools, or execution'],
  ['current_instructions_marker_verified', 'does not override current user instructions'],
];
const child = spawnSync(cliPath, [
  'memory', 'inject-gemini', '--project-id', projectID,
  '--threadforge-managed-gemini-memory=v1', '--threadforge-memory=gemini-v1', '--task-id', taskID,
], { input: readFileSync(0), encoding: 'utf8', env: process.env, timeout: 10000, maxBuffer: 1 << 20, windowsHide: true });
let invocationCount = 0;
if (existsSync(resultPath)) {
  try { invocationCount = JSON.parse(readFileSync(resultPath, 'utf8')).hook_invocations ?? 0; }
  catch { process.stderr.write('Threadforge Gemini hook observer state was invalid.\\n'); process.exit(1); }
}
const baseObservation = { hook_invocations: invocationCount + 1, threadforge_cli_succeeded: false };
writeFileSync(resultPath, JSON.stringify(baseObservation), { mode: 0o600 });
if (child.error || child.status !== 0) {
  writeFileSync(resultPath, JSON.stringify({ ...baseObservation, threadforge_cli_exit_code: child.status ?? null, threadforge_cli_error_code: child.error?.code ?? null }), { mode: 0o600 });
  process.stderr.write('Threadforge Gemini task-resume hook fixture failed.\\n');
  process.exit(1);
}
let hookOutput;
try { hookOutput = JSON.parse(child.stdout); }
catch { process.stderr.write('Threadforge Gemini task-resume hook returned invalid JSON.\\n'); process.exit(1); }
const context = hookOutput?.hookSpecificOutput?.additionalContext;
const checks = Object.fromEntries(markers.map(([name, marker]) => [name, typeof context === 'string' && context.includes(marker)]));
writeFileSync(resultPath, JSON.stringify({ ...baseObservation, threadforge_cli_succeeded: true, ...checks }), { mode: 0o600 });
process.stdout.write(child.stdout);
if (Object.values(checks).some((value) => !value)) process.exitCode = 1;
`;
    writeFileSync(hookObserverPath, hookObserverSource, { mode: 0o600, flag: 'wx' });
    const hookCommandArguments = [
      process.execPath, hookObserverPath, cliPath, hookObserverResultPath, taskFixture.projectID, taskFixture.id,
      '--threadforge-managed-gemini-memory=v1', '--threadforge-memory=gemini-v1',
    ];
    const hookCommand = process.platform === 'win32'
      ? `& ${hookCommandArguments.map(quotePowerShellArgument).join(' ')}`
      : hookCommandArguments.map(quotePosixArgument).join(' ');
    assert.equal(await installGeminiProjectMemoryHook(project, hookCommand), 'installed');
    const projectSettingsPath = path.join(project, '.gemini', 'settings.json');
    const projectSettings = JSON.parse(readFileSync(projectSettingsPath, 'utf8'));
    const installedHook = projectSettings.hooks?.BeforeAgent?.flatMap((group) => group.hooks ?? [])
      .find((hook) => hook.name === 'threadforge-approved-memory');
    assert.equal(installedHook?.command, hookCommand, 'Gemini project BeforeAgent hook did not preserve the task-resume observer command');
    // Gemini sanitizes hook environments. Give only this disposable hook the
    // isolated Threadforge store root needed to read the synthetic task.
    installedHook.env = { THREADFORGE_HOME: smokeRoot };
    writeFileSync(projectSettingsPath, `${JSON.stringify(projectSettings, null, 2)}\n`, { mode: 0o600 });

    assert.equal(await installGeminiTelemetry({ homeDir: home }), 'installed');
    const settingsPath = path.join(home, '.gemini', 'settings.json');
    const settings = JSON.parse(readFileSync(settingsPath, 'utf8'));
    settings.telemetry = {
      ...settings.telemetry,
      enabled: true,
      target: 'local',
      otlpEndpoint: origin,
      otlpProtocol: 'http',
      useCollector: true,
      logPrompts: false,
    };
    settings.security = { ...(settings.security ?? {}), auth: {
      ...(settings.security?.auth ?? {}), selectedType: 'gemini-api-key',
    } };
    writeFileSync(settingsPath, `${JSON.stringify(settings, null, 2)}\n`, { mode: 0o600 });

    const geminiEnvironment = {
      ...isolatedEnvironment,
      HOME: home,
      USERPROFILE: home,
      APPDATA: path.join(home, 'AppData', 'Roaming'),
      LOCALAPPDATA: path.join(home, 'AppData', 'Local'),
      XDG_CONFIG_HOME: path.join(home, '.config'),
      XDG_DATA_HOME: path.join(home, '.local', 'share'),
      XDG_CACHE_HOME: path.join(home, '.cache'),
      // Gemini CLI treats this as its home root and appends `.gemini` itself.
      GEMINI_CLI_HOME: home,
      GEMINI_CLI_TRUST_WORKSPACE: 'true',
      GEMINI_CLI_NO_RELAUNCH: 'true',
      THREADFORGE_HOME: smokeRoot,
      GEMINI_API_KEY: fixtureKey,
      // If this host version ignores --fake-responses, force inference back to
      // the same loopback server so the request is observed and rejected.
      GOOGLE_GEMINI_BASE_URL: origin,
      GEMINI_TELEMETRY_ENABLED: 'true',
      GEMINI_TELEMETRY_TARGET: 'local',
      GEMINI_TELEMETRY_OTLP_ENDPOINT: origin,
      GEMINI_TELEMETRY_OTLP_PROTOCOL: 'http',
      GEMINI_TELEMETRY_USE_COLLECTOR: 'true',
      GEMINI_TELEMETRY_LOG_PROMPTS: 'false',
      HTTP_PROXY: origin,
      HTTPS_PROXY: origin,
      ALL_PROXY: origin,
      NO_PROXY: '127.0.0.1,localhost',
      http_proxy: origin,
      https_proxy: origin,
      all_proxy: origin,
      no_proxy: '127.0.0.1,localhost',
    };
    let output;
    try {
      output = await runGemini([
        '--output-format', 'json',
        '--model', fixtureModel,
        '--approval-mode', 'plan',
        '--skip-trust',
        '--debug',
        '--fake-responses', fakeResponsesPath,
      ], { cwd: project, env: geminiEnvironment, timeout: 30_000, includeStderr: true, stdin: prompt });
    } catch (error) {
      throw new Error(`Gemini host fake-response telemetry invocation failed; routes=${JSON.stringify(routeCounts)}; fixture_error=${fixtureError ?? 'none'}; cause=${String(error?.message ?? error).slice(0, 800)}`);
    }

    if (fixtureError) throw new Error(fixtureError);
    assert.ok(output.includes(outputMarker), 'Gemini CLI output omitted the deterministic fixture response');
    let hookObservation;
    try { hookObservation = JSON.parse(readFileSync(hookObserverResultPath, 'utf8')); }
    catch {
      const hookDiagnostics = output.split(/\r?\n/).filter((line) => /hook|trust|setting|BeforeAgent/i.test(line))
        .map((line) => line.replaceAll(smokeRoot, '<smoke-root>').replaceAll(prompt, '<fixture-prompt>').slice(0, 240)).slice(-12);
      throw new Error(`Gemini CLI did not execute its configured Threadforge BeforeAgent task-resume hook; diagnostics=${JSON.stringify(hookDiagnostics)}`);
    }
    assert.equal(hookObservation.threadforge_cli_succeeded, true,
      `Gemini BeforeAgent ran but Threadforge hook retrieval failed; exit=${hookObservation.threadforge_cli_exit_code ?? 'unknown'} error=${hookObservation.threadforge_cli_error_code ?? 'none'}`);
    assert.equal(hookObservation.hook_invocations, 1, 'Gemini CLI did not invoke the task-resume hook exactly once');
    assert.equal(hookObservation.task_objective_observed, true, 'Gemini BeforeAgent did not return the selected task objective');
    assert.equal(hookObservation.requirements_not_finalized_warning_verified, true, 'Gemini BeforeAgent omitted the unfinished-requirements warning');
    assert.equal(hookObservation.advisory_only_marker_verified, true, 'Gemini BeforeAgent omitted the advisory-only marker');
    assert.equal(hookObservation.non_authorization_marker_verified, true, 'Gemini BeforeAgent implied execution authorization');
    assert.equal(hookObservation.current_instructions_marker_verified, true, 'Gemini BeforeAgent omitted current-instruction precedence');
    const taskResume = {
      schema_version: 'threadforge.gemini-task-resume-smoke.v1',
      status: 'verified',
      host_cli_version: version,
      host_hook: 'BeforeAgent',
      task_context_observed: true,
      task_context_content_retained_in_report: false,
      task_objective_observed: hookObservation.task_objective_observed,
      requirements_not_finalized_warning_verified: hookObservation.requirements_not_finalized_warning_verified,
      advisory_safety_statement_verified: hookObservation.advisory_only_marker_verified &&
        hookObservation.non_authorization_marker_verified && hookObservation.current_instructions_marker_verified,
      action_ready: false,
      guidance_only: true,
      execution_authorized: false,
      hook_invocations: hookObservation.hook_invocations,
      response_source: 'gemini-cli-fake-responses-file',
      real_model_invocations: 0,
    };

    const planGateSentinelPath = path.join(planGateProject, 'threadforge-plan-gate-sentinel.txt');
    const planGateReplaceTargetPath = path.join(planGateProject, 'threadforge-plan-gate-replace-target.txt');
    const planGateReplaceTargetContent = 'preserve this approved fixture content';
    writeFileSync(planGateReplaceTargetPath, planGateReplaceTargetContent, { mode: 0o600, flag: 'wx' });
    const planGateFakeResponsesPath = path.join(smokeRoot, 'gemini-plan-gate-fake-responses.jsonl');
    const planGateFakeResponses = [
      {
        method: 'generateContentStream',
        response: [{
          candidates: [{
            content: { role: 'model', parts: [{ functionCall: {
              name: 'write_file',
              args: { file_path: planGateSentinelPath, content: 'THREADFORGE_GEMINI_DENIED_SENTINEL' },
            } }] },
            finishReason: 'STOP',
          }],
          usageMetadata: { promptTokenCount: 1, candidatesTokenCount: 1, totalTokenCount: 2 },
          modelVersion: fixtureModel,
        }],
      },
      {
        method: 'generateContentStream',
        response: [{
          candidates: [{
            content: { role: 'model', parts: [{ functionCall: {
              name: 'replace',
              args: {
                file_path: planGateReplaceTargetPath,
                instruction: 'Change the fixture text',
                old_string: planGateReplaceTargetContent,
                new_string: 'THREADFORGE_GEMINI_DENIED_REPLACEMENT',
              },
            } }] },
            finishReason: 'STOP',
          }],
          usageMetadata: { promptTokenCount: 1, candidatesTokenCount: 1, totalTokenCount: 2 },
          modelVersion: fixtureModel,
        }],
      },
      {
        method: 'generateContentStream',
        response: [{
          candidates: [{
            content: { role: 'model', parts: [{ text: 'Both direct file operations were denied by the task plan gate.' }] },
            finishReason: 'STOP',
          }],
          usageMetadata: { promptTokenCount: 1, candidatesTokenCount: 1, totalTokenCount: 2 },
          modelVersion: fixtureModel,
        }],
      },
    ];
    writeFileSync(planGateFakeResponsesPath, `${planGateFakeResponses.map((item) => JSON.stringify(item)).join('\n')}\n`,
      { mode: 0o600, flag: 'wx' });
    const planGateObserverPath = path.join(smokeRoot, 'gemini-plan-gate-observer.mjs');
    const planGateObserverResultPath = path.join(smokeRoot, 'gemini-plan-gate-observer.json');
    const planGateObserverSource = `import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
const [cliPath, resultPath, projectID, taskID, projectRoot, writeTargetPath, replaceTargetPath] = process.argv.slice(2);
const samePath = (left, right) => process.platform === 'win32'
  ? path.resolve(left).toLowerCase() === path.resolve(right).toLowerCase()
  : path.resolve(left) === path.resolve(right);
const rawInput = readFileSync(0);
let input;
try { input = JSON.parse(rawInput.toString('utf8')); }
catch { process.stderr.write('Gemini plan-gate smoke hook input was invalid.\\n'); process.exit(1); }
let prior = {};
if (existsSync(resultPath)) {
  try { prior = JSON.parse(readFileSync(resultPath, 'utf8')); }
  catch { process.stderr.write('Gemini plan-gate smoke observation was invalid.\\n'); process.exit(1); }
}
const isWriteFile = input?.tool_name === 'write_file';
const isReplace = input?.tool_name === 'replace';
const expectedPath = isWriteFile ? writeTargetPath : isReplace ? replaceTargetPath : '';
const inputVerified = input?.hook_event_name === 'BeforeTool' && Boolean(expectedPath) &&
  typeof input?.tool_input?.file_path === 'string' && samePath(input.tool_input.file_path, expectedPath);
const child = spawnSync(cliPath, [
  'task', 'plan', 'guard', '--host', 'gemini-cli', '--task-id', taskID, '--project-id', projectID,
  '--project-root', projectRoot,
  '--threadforge-managed-gemini-plan-gate=v1', '--threadforge-plan-gate=gemini-v1',
], { input: rawInput, encoding: 'utf8', env: process.env, timeout: 10000, maxBuffer: 1 << 20, windowsHide: true });
let guardOutput;
try { guardOutput = JSON.parse(child.stdout); } catch { guardOutput = {}; }
const guardSucceeded = !child.error && child.status === 0;
const denialObserved = guardOutput?.decision === 'deny' && typeof guardOutput?.reason === 'string' && guardOutput.reason.length > 0;
const noAllowDecision = guardOutput?.decision !== 'allow' && guardOutput?.permissionDecision !== 'allow';
const observation = {
  hook_invocations: (prior.hook_invocations ?? 0) + 1,
  write_file_invocations: (prior.write_file_invocations ?? 0) + (isWriteFile ? 1 : 0),
  replace_invocations: (prior.replace_invocations ?? 0) + (isReplace ? 1 : 0),
  hook_inputs_verified: (prior.hook_inputs_verified ?? true) && inputVerified,
  threadforge_guards_succeeded: (prior.threadforge_guards_succeeded ?? true) && guardSucceeded,
  write_file_denial_observed: (prior.write_file_denial_observed ?? false) || (isWriteFile && denialObserved),
  replace_denial_observed: (prior.replace_denial_observed ?? false) || (isReplace && denialObserved),
  no_allow_decisions_returned: (prior.no_allow_decisions_returned ?? true) && noAllowDecision,
  threadforge_cli_error_code: child.error?.code ?? null,
};
writeFileSync(resultPath, JSON.stringify(observation), { mode: 0o600 });
process.stdout.write(child.stdout ?? '{}');
if (!inputVerified || !guardSucceeded || !denialObserved || !noAllowDecision) process.exitCode = 1;
`;
    writeFileSync(planGateObserverPath, planGateObserverSource, { mode: 0o600, flag: 'wx' });
    const planGateCommandArguments = [
      process.execPath, planGateObserverPath, cliPath, planGateObserverResultPath,
      taskFixture.projectID, taskFixture.id, planGateProject, planGateSentinelPath, planGateReplaceTargetPath,
      '--threadforge-managed-gemini-plan-gate=v1', '--threadforge-plan-gate=gemini-v1',
    ];
    const planGateCommand = process.platform === 'win32'
      ? `& ${planGateCommandArguments.map(quotePowerShellArgument).join(' ')}`
      : planGateCommandArguments.map(quotePosixArgument).join(' ');
    assert.equal(await installGeminiProjectPlanGateHook(planGateProject, planGateCommand), 'installed');
    const planGateSettingsPath = path.join(planGateProject, '.gemini', 'settings.json');
    const planGateSettings = JSON.parse(readFileSync(planGateSettingsPath, 'utf8'));
    const installedPlanGate = planGateSettings.hooks?.BeforeTool?.flatMap((group) => group.hooks ?? [])
      .find((hook) => hook.name === 'threadforge-task-plan-gate');
    assert.equal(installedPlanGate?.command, planGateCommand,
      'Gemini project BeforeTool plan-gate hook did not preserve the owned observer command');
    assert.equal(planGateSettings.hooks.BeforeTool.find((group) => group.hooks?.includes(installedPlanGate))?.matcher,
      '^(write_file|replace)$', 'Gemini plan gate was not limited to direct file tools');
    installedPlanGate.env = { THREADFORGE_HOME: smokeRoot };
    writeFileSync(planGateSettingsPath, `${JSON.stringify(planGateSettings, null, 2)}\n`, { mode: 0o600 });

    let planGateOutput;
    try {
      planGateOutput = await runGemini([
        '--output-format', 'json',
        '--model', fixtureModel,
        '--approval-mode', 'yolo',
        '--skip-trust',
        '--debug',
        '--fake-responses', planGateFakeResponsesPath,
      ], {
        cwd: planGateProject,
        env: { ...geminiEnvironment, GEMINI_TELEMETRY_ENABLED: 'false' },
        timeout: 30_000,
        includeStderr: true,
        stdin: 'Use write_file to create the requested fixture file and replace to change the existing fixture text, then report both results.',
      });
    } catch (error) {
      throw new Error(`Gemini installed plan-gate denial invocation failed; unexpected_local_routes=${routeCounts.unexpected ?? 0}; cause=${String(error?.message ?? error).slice(0, 500)}`);
    }
    assert.ok(planGateOutput.includes('Both direct file operations were denied'),
      'Gemini CLI did not complete the deterministic response after both deny hooks');
    assert.equal(existsSync(planGateSentinelPath), false, 'Gemini created the file despite the plan-gate denial');
    assert.equal(readFileSync(planGateReplaceTargetPath, 'utf8'), planGateReplaceTargetContent,
      'Gemini changed the existing file despite the plan-gate denial');
    assert.equal(routeCounts.unexpected ?? 0, 0, 'Gemini plan-gate smoke attempted a model request outside the fake-response fixture');
    let planGateObservation;
    try { planGateObservation = JSON.parse(readFileSync(planGateObserverResultPath, 'utf8')); }
    catch { throw new Error('Gemini CLI did not execute the installed Threadforge BeforeTool plan-gate observer'); }
    assert.equal(planGateObservation.hook_invocations, 2, 'Gemini did not invoke the plan-gate hook for both direct file tools');
    assert.equal(planGateObservation.write_file_invocations, 1, 'Gemini did not invoke exactly one write_file hook');
    assert.equal(planGateObservation.replace_invocations, 1, 'Gemini did not invoke exactly one replace hook');
    assert.equal(planGateObservation.hook_inputs_verified, true, 'Gemini plan-gate hook received the wrong event or file target');
    assert.equal(planGateObservation.threadforge_guards_succeeded, true, 'Threadforge task-plan guard failed inside Gemini hook');
    assert.equal(planGateObservation.write_file_denial_observed, true, 'Threadforge did not deny Gemini write_file');
    assert.equal(planGateObservation.replace_denial_observed, true, 'Threadforge did not deny Gemini replace');
    assert.equal(planGateObservation.no_allow_decisions_returned, true, 'Threadforge plan gate returned an allow decision');
    const taskPlanGate = {
      schema_version: 'threadforge.gemini-cli-plan-gate-smoke.v1',
      status: 'verified',
      host_cli_version: version,
      install_action: 'installed',
      hook_event: 'BeforeTool',
      tools: ['write_file', 'replace'],
      hook_invocations: planGateObservation.hook_invocations,
      hook_inputs_verified: planGateObservation.hook_inputs_verified,
      write_file_denial_observed: planGateObservation.write_file_denial_observed,
      replace_denial_observed: planGateObservation.replace_denial_observed,
      write_file_created: existsSync(planGateSentinelPath),
      replace_target_unchanged: readFileSync(planGateReplaceTargetPath, 'utf8') === planGateReplaceTargetContent,
      response_source: 'gemini-cli-fake-responses-file',
      provider_requests: routeCounts.unexpected ?? 0,
      real_model_invocations: 0,
    };
    const debugTelemetryLines = output.split(/\r?\n/)
      .filter((line) => /telemetry|OpenTelemetry|OTLP/i.test(line))
      .map((line) => line.replaceAll(origin, '<loopback>').slice(0, 240))
      .slice(-20);
    const telemetrySdkStarted = /OpenTelemetry SDK started successfully/i.test(output);
    const telemetrySdkShutdownLogged = /OpenTelemetry SDK shut down successfully/i.test(output);
    const deliveryObserved = telemetry.logs > 0 && telemetry.payload_bytes > 0;
    if (!deliveryObserved) {
      assert.equal(telemetrySdkStarted, true,
        `Gemini CLI telemetry SDK did not start; diagnostics=${JSON.stringify(debugTelemetryLines)}`);
      return {
        task_resume: taskResume,
        task_plan_gate: taskPlanGate,
        telemetry: {
        schema_version: 'threadforge.gemini-cli-telemetry-smoke.v1',
        status: 'not_observed',
        version,
        response_source: 'gemini-cli-fake-responses-file',
        model_requests: 0,
        real_model_invocations: 0,
        telemetry_sdk_started: telemetrySdkStarted,
        telemetry_sdk_shutdown_logged: telemetrySdkShutdownLogged,
        unexpected_local_routes: routeCounts.unexpected ?? 0,
        telemetry: {
          status: 'not_observed',
          endpoint: 'loopback-only',
          ...summaryTelemetry(telemetry),
          payload_content_scanned: false,
        },
        },
      };
    }
    const observedSensitiveFields = [...sensitiveFields].sort();
    assert.equal(observedSensitiveFields.some((item) => item.endsWith(':user_prompt_text')), false,
      `Gemini telemetry included user prompt text despite logPrompts=false: fields=${JSON.stringify(observedSensitiveFields)}; paths=${JSON.stringify([...sensitiveFieldPaths].filter((item) => item.includes(':user_prompt_text')).sort())}; config=${JSON.stringify({ settings_log_prompts: settings.telemetry.logPrompts, env_log_prompts: geminiEnvironment.GEMINI_TELEMETRY_LOG_PROMPTS })}`);
    assert.equal(observedSensitiveFields.some((item) => item.endsWith(':model_output_text')), false,
      `Gemini telemetry included model output text despite detailed tracing being disabled: fields=${JSON.stringify(observedSensitiveFields)}; paths=${JSON.stringify([...sensitiveFieldPaths].filter((item) => item.includes(':model_output_text')).sort())}`);
    return {
      task_resume: taskResume,
      task_plan_gate: taskPlanGate,
      telemetry: {
      schema_version: 'threadforge.gemini-cli-telemetry-smoke.v1',
      status: 'verified',
      version,
      response_source: 'gemini-cli-fake-responses-file',
      model_requests: 0,
      real_model_invocations: 0,
      telemetry_sdk_started: telemetrySdkStarted,
      telemetry_sdk_shutdown_logged: telemetrySdkShutdownLogged,
      unexpected_local_routes: routeCounts.unexpected ?? 0,
      telemetry: {
        status: 'verified', endpoint: 'loopback-only', ...summaryTelemetry(telemetry),
        payload_content_scanned: true,
        user_prompt_text_detected: observedSensitiveFields.some((item) => item.endsWith(':user_prompt_text')),
        model_output_text_detected: observedSensitiveFields.some((item) => item.endsWith(':model_output_text')),
      },
      },
    };
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

function summaryTelemetry(telemetry) {
  return {
    log_batches: telemetry.logs,
    trace_batches: telemetry.traces,
    metric_batches: telemetry.metrics,
    payload_bytes: telemetry.payload_bytes,
    content_types: [...telemetry.content_types].sort(),
  };
}

function quotePowerShellArgument(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function quotePosixArgument(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

function collectSensitiveFieldPaths(value, signal, prefix, prompt, outputMarker, paths) {
  if (typeof value === 'string') {
    if (value.includes(prompt)) paths.add(`${signal}:user_prompt_text:${prefix || '$'}`);
    if (value.includes(outputMarker)) paths.add(`${signal}:model_output_text:${prefix || '$'}`);
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((item, index) => collectSensitiveFieldPaths(item, signal, `${prefix}[${index}]`, prompt, outputMarker, paths));
    return;
  }
  if (value && typeof value === 'object') {
    for (const [key, item] of Object.entries(value)) {
      const next = prefix ? `${prefix}.${key}` : key;
      collectSensitiveFieldPaths(item, signal, next, prompt, outputMarker, paths);
    }
  }
}

function writeJson(response, value, statusCode = 200) {
  const body = JSON.stringify(value);
  response.writeHead(statusCode, {
    'content-type': 'application/json; charset=utf-8',
    'content-length': Buffer.byteLength(body),
    connection: 'close',
  }).end(body);
}
