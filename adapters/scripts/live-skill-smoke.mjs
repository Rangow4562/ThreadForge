import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { spawn, spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { createOpenCodeAdapter } from '../src/opencode-plugin.ts';
import { installAntigravityProjectPlanGateHook } from '../src/antigravity-hooks-config.ts';
import { installAntigravityProjectSkill } from '../src/antigravity-skill-config.ts';
import { installClaudeCodeProjectSkill } from '../src/claude-skill-config.ts';
import { installClaudeProjectMemoryHook } from '../src/claude-hooks-config.ts';
import { installCommandCodeProjectSkill } from '../src/command-code-skill-config.ts';
import { installClineProjectSkill } from '../src/cline-skill-config.ts';
import { installCopilotProjectPlanGateHook } from '../src/copilot-hooks-config.ts';
import { installCopilotProjectSkill } from '../src/copilot-skill-config.ts';
import { installCodexProjectSkill } from '../src/codex-skill-config.ts';
import { installCursorProjectSkill } from '../src/cursor-skill-config.ts';
import { installGeminiProjectSkill } from '../src/gemini-skill-config.ts';
import { installOpenCodeProjectSkill } from '../src/opencode-skill-config.ts';
import { installOpenCodeProjectMcpServer } from '../src/opencode-mcp-config.ts';
import { installOpenCodeProjectPlugin, opencodeManagedMarker } from '../src/opencode-plugin-config.ts';
import { installOpenCodeProjectPlanAgent, opencodePlanAgentName } from '../src/opencode-plan-agent-config.ts';
import { installCodexProjectMcpServer } from '../src/codex-mcp-config.ts';
import { installProjectMcpServer } from '../src/mcp-config.ts';
import { installZCodeProjectSkill } from '../src/zcode-skill-config.ts';
import { installDeepSeekHarnessProjectSkill } from '../src/deepseek-harness-skill-config.ts';
import { installQoderProjectSkill } from '../src/qoder-skill-config.ts';
import { installQoderProjectCommand } from '../src/qoder-command-config.ts';
import { runCodexAgentMcpRead, runCodexAgentPlanGateAttempt } from './codex-mcp-smoke.mjs';
import { runGeminiCliTelemetrySmoke } from './gemini-telemetry-smoke.mjs';
import { runClaudePromptMemoryRead } from './claude-memory-smoke.mjs';
import { runAntigravityAgentMcpRead, runAntigravityPlanGateAttempt } from './antigravity-mcp-smoke.mjs';
import { runCommandCodeAgentMcpRead } from './command-code-mcp-smoke.mjs';

const approvalNotice = '> This portable procedure is approved guidance, not authorization to execute actions.';
const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const isolatedHome = mkdtempSync(path.join(tmpdir(), 'threadforge-installed-skill-smoke-'));
const projectRoot = path.join(isolatedHome, 'project');
const commandCodeProjectRoot = path.join(isolatedHome, 'command-code-project');
const databasePath = path.join(isolatedHome, 'threadforge.sqlite');
const codexHome = path.join(isolatedHome, 'codex-home');
const claudeConfigDir = path.join(isolatedHome, 'claude-config');
// Share the disposable default store with the explicit MCP database so the
// installed Claude prompt hook can read the same synthetic continuity task.
const threadforgeHome = isolatedHome;
const cliPath = path.join(isolatedHome, process.platform === 'win32' ? 'threadforge.exe' : 'threadforge');
const inheritedEnvironmentNames = new Set([
  'PATH', 'PATHEXT', 'SYSTEMROOT', 'WINDIR', 'COMSPEC', 'TEMP', 'TMP',
  'LANG', 'TERM', 'NO_COLOR', 'FORCE_COLOR',
]);
const isolatedEnvironment = Object.fromEntries(
  Object.entries(process.env).filter(([key]) => inheritedEnvironmentNames.has(key.toUpperCase())),
);
Object.assign(isolatedEnvironment, {
  HOME: isolatedHome,
  USERPROFILE: isolatedHome,
  APPDATA: path.join(isolatedHome, 'AppData', 'Roaming'),
  LOCALAPPDATA: path.join(isolatedHome, 'AppData', 'Local'),
  XDG_CONFIG_HOME: path.join(isolatedHome, '.config'),
  XDG_DATA_HOME: path.join(isolatedHome, '.local', 'share'),
  XDG_CACHE_HOME: path.join(isolatedHome, '.cache'),
  // Gemini CLI treats this as its home root and appends `.gemini` itself.
  GEMINI_CLI_HOME: isolatedHome,
  CODEX_HOME: codexHome,
  CLAUDE_CONFIG_DIR: claudeConfigDir,
  THREADFORGE_HOME: threadforgeHome,
  CMD_LOCAL_ONLY: '1',
  // Keep installed-host smoke checks from changing the globally installed CLI.
  AGY_CLI_DISABLE_AUTO_UPDATE: 'true',
  // Trust only the disposable workspace; never edit the user's Gemini trust state.
  GEMINI_CLI_TRUST_WORKSPACE: 'true',
});

function run(command, args, { cwd = repositoryRoot, env = process.env, timeout = 120_000, includeStderr = false } = {}) {
  const result = spawnSync(command, args, {
    cwd, env, encoding: 'utf8', timeout, maxBuffer: 4 << 20,
  });
  if (result.error) {
    const detail = [result.error.code, result.error.message].filter(Boolean).join(': ');
    const partial = summarizePartialCommandOutput(result.stdout, result.stderr);
    throw new Error(`${path.basename(command)} could not run the local smoke command${detail ? ` (${detail})` : ''}${partial ? `; partial_output=${partial}` : ''}`);
  }
  if (result.status !== 0) {
    const detail = (result.stderr || result.stdout || '').trim().slice(0, 2000);
    throw new Error(`${path.basename(command)} exited ${result.status}: ${detail}`);
  }
  return includeStderr ? `${result.stdout}${result.stderr}` : result.stdout;
}

function runThreadforge(args) {
  return run(cliPath, args, { cwd: projectRoot, env: isolatedEnvironment }).trim();
}

function runCommandCode(args, { cwd = commandCodeProjectRoot, env = isolatedEnvironment, timeout = 60_000, includeStderr = false } = {}) {
  // The Windows PowerShell shim can outlive spawnSync's timeout and leave its
  // Node child holding the disposable project as its working directory. Run
  // the same installed CLI entrypoint directly so timeout/cleanup stays scoped.
  const shimPath = run('where.exe', ['cmdc.ps1'], { cwd, env, timeout: 10_000 })
    .split(/\r?\n/).map((entry) => entry.trim()).find(Boolean);
  if (!shimPath) throw new Error('Could not resolve the installed Command Code cmdc.ps1 shim');
  const entrypoint = path.join(path.dirname(shimPath), 'node_modules', 'command-code', 'dist', 'index.mjs');
  if (!existsSync(entrypoint)) throw new Error('Could not resolve the installed Command Code CLI entrypoint next to cmdc.ps1');
  return run(process.execPath, [entrypoint, ...args], { cwd, env, timeout, includeStderr });
}

function runHost(command, args, { cwd = projectRoot, env = isolatedEnvironment, timeout = 30_000, includeStderr = false } = {}) {
  if (process.platform === 'win32' && command === 'cmdc') {
    return runCommandCode(args, { cwd, env, timeout, includeStderr });
  }
  const windowsPowerShellShim = process.platform === 'win32' && ['gemini', 'codex', 'command-code', 'copilot'].includes(command);
  const executable = windowsPowerShellShim ? 'pwsh.exe' : command;
  const executableArgs = windowsPowerShellShim
    ? ['-NoProfile', '-NonInteractive', '-Command', `& ${[command, ...args].map(quotePowerShellArgument).join(' ')}`]
    : args;
  return run(executable, executableArgs, { cwd, env, timeout, includeStderr });
}

function runGeminiCliAsync(args, { cwd = projectRoot, env = isolatedEnvironment, timeout = 90_000, includeStderr = false, stdin } = {}) {
  if (process.platform !== 'win32') return runAsync('gemini', args, { cwd, env, timeout, includeStderr, stdin });
  const shimPath = run('where.exe', ['gemini.ps1'], { cwd, env, timeout: 10_000 })
    .split(/\r?\n/).map((entry) => entry.trim()).find(Boolean);
  if (!shimPath) throw new Error('Could not resolve the installed Gemini CLI PowerShell shim');
  const entrypoint = path.join(path.dirname(shimPath), 'node_modules', '@google', 'gemini-cli', 'bundle', 'gemini.js');
  if (!existsSync(entrypoint)) throw new Error('Could not resolve the installed Gemini CLI Node entrypoint next to gemini.ps1');
  return runAsync(process.execPath, [entrypoint, ...args], { cwd, env, timeout, includeStderr, stdin });
}

function quotePowerShellArgument(value) {
  return `'${String(value).replaceAll("'", "''")}'`;
}

function summarizePartialCommandOutput(stdout, stderr) {
  const value = [stderr, stdout].map((part) => String(part ?? '').trim()).filter(Boolean).join(' | ');
  if (!value) return '';
  return value
    .replaceAll(isolatedHome, '<smoke-home>')
    .replaceAll(projectRoot, '<smoke-project>')
    .replaceAll(commandCodeProjectRoot, '<command-code-project>')
    .replaceAll(databasePath, '<smoke-db>')
    .replaceAll(cliPath, '<threadforge-cli>')
    .replace(/(?:Bearer\s+|key=)[^\s&]+/gi, '<credential>')
    .replace(/[\r\n\t]+/g, ' ')
    .replace(/\s{2,}/g, ' ')
    .slice(-600);
}

async function removeIsolatedHomeWithRetry() {
  for (let attempt = 0; ; attempt += 1) {
    try {
      rmSync(isolatedHome, { recursive: true, force: true });
      return;
    } catch (error) {
      if (!['EBUSY', 'EPERM', 'ENOTEMPTY'].includes(error?.code) || attempt >= 11) throw error;
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
  }
}

function tryGeminiSkillDiscovery() {
  try {
    return { discovered: true, output: runHost('gemini', ['skills', 'list']) };
  } catch (error) {
    // Newer Gemini CLI builds require an auth method even for local skill
    // inventory. Do not forward user credentials or invent a fake provider;
    // preserve the installer check and report discovery as unavailable.
    if (/Please set an Auth method|GEMINI_API_KEY|GOOGLE_GENAI_USE_(VERTEXAI|GCA)/i.test(String(error?.message ?? error))) {
      return { discovered: false, skipped: 'auth-not-configured-in-isolated-home' };
    }
    throw error;
  }
}

function hasNamedMcpServer(value, expectedName) {
  if (Array.isArray(value)) return value.some((item) => hasNamedMcpServer(item, expectedName));
  if (value === null || typeof value !== 'object') return false;
  if (value.name === expectedName || value.server_name === expectedName || value.serverName === expectedName) return true;
  return Object.entries(value).some(([key, item]) => key === expectedName || hasNamedMcpServer(item, expectedName));
}

function tryCopilotProjectMcpDiscovery(cwd) {
  const copilotHome = path.join(isolatedHome, 'copilot-home');
  const env = {
    ...isolatedEnvironment,
    COPILOT_HOME: copilotHome,
    COPILOT_AUTO_UPDATE: 'false',
    COPILOT_OFFLINE: 'true',
    // Offline mode requires a configured local provider, but listing config
    // must not invoke it. The endpoint is intentionally not backed by a server.
    COPILOT_PROVIDER_BASE_URL: 'http://127.0.0.1:9/v1',
    COPILOT_MODEL: 'threadforge-fixture-only',
  };
  let version;
  try {
    version = runHost('copilot', ['--version'], { cwd, env, timeout: 20_000 }).trim().split(/\r?\n/, 1)[0];
  } catch (error) {
    if (/could not run the local smoke command \(ENOENT\)/i.test(String(error?.message ?? error))) {
      return { status: 'not-installed', model_invocations: 0 };
    }
    throw error;
  }
  const output = runHost('copilot', ['mcp', 'list', '--json'], { cwd, env, timeout: 30_000 });
  let listing;
  try {
    listing = JSON.parse(output);
  } catch {
    throw new Error('Copilot CLI MCP listing did not return valid JSON');
  }
  const serverListed = hasNamedMcpServer(listing, 'threadforge');
  assert.equal(serverListed, true, 'Copilot CLI did not list the isolated workspace Threadforge MCP server');
  return { status: 'verified', version, config: '.mcp.json', workspace_server_listed: true, offline: true, model_invocations: 0 };
}

async function runCopilotPlanGateHostSmoke(version) {
  const resultBase = {
    schema_version: 'threadforge.copilot-cli-plan-gate-smoke.v1',
    host_cli_version: version ?? null,
    hook_event: 'preToolUse',
    guard_fixture: 'missing-task',
    provider_routes_loopback_only: true,
    real_model_invocations: 0,
  };
  if (!version) {
    return {
      ...resultBase,
      status: 'not-installed',
      provider: null,
      tool_attempts: [],
      hook_denial_count: 0,
      write_effects_observed: 0,
      provider_requests: 0,
    };
  }
  const project = path.join(isolatedHome, 'copilot-plan-gate-project');
  const database = path.join(isolatedHome, 'copilot-plan-gate.sqlite');
  mkdirSync(project, { recursive: true });
  run('git', ['init', '--quiet'], {
    cwd: project,
    env: { ...isolatedEnvironment, GIT_CONFIG_NOSYSTEM: '1' },
    timeout: 15_000,
  });
  const taskID = 'copilot-plan-gate-smoke-missing-task';
  const projectID = 'copilot-plan-gate-smoke-project';
  const markers = '--threadforge-managed-copilot-plan-gate=v1 --threadforge-plan-gate=copilot-v1';
  const commonArgs = `task plan guard --host github-copilot --task-id ${taskID} --project-id ${projectID}`;
  const bashProject = process.platform === 'win32' ? windowsPathToBash(project) : project;
  const bashDatabase = process.platform === 'win32' ? windowsPathToBash(database) : database;
  const bashCli = process.platform === 'win32' ? windowsPathToBash(cliPath) : cliPath;
  const bashCommand = `${quoteBashArgument(bashCli)} ${commonArgs} --project-root ${quoteBashArgument(bashProject)} --db ${quoteBashArgument(bashDatabase)} ${markers}`;
  const powershellCommand = `& ${quotePowerShellArgument(cliPath)} ${commonArgs} --project-root ${quotePowerShellArgument(project)} --db ${quotePowerShellArgument(database)} ${markers}`;
  assert.equal(await installCopilotProjectPlanGateHook({
    projectRoot: project, bashCommand, powershellCommand,
  }), 'installed');

  const toolNames = ['create', 'edit', 'apply_patch', 'str_replace_editor'];
  const attempts = toolNames.map((tool) => {
    const safeName = tool.replaceAll(/[^a-z0-9]+/gi, '-').toLowerCase();
    const initialContent = tool === 'edit' || tool === 'str_replace_editor'
      ? `THREADFORGE-${safeName}-original-content`
      : null;
    const targetPath = `threadforge-plan-gate-${safeName}-must-deny.txt`;
    if (initialContent !== null) {
      writeFileSync(path.join(project, targetPath), `${initialContent}\n`, { flag: 'wx', mode: 0o600 });
    }
    return {
      tool,
      targetPath,
      initialContent,
      replacementContent: `THREADFORGE-${safeName}-replacement-content`,
      callID: `call_copilot_${safeName}_plan_gate_smoke`,
      toolAvailable: null,
      availabilityReason: null,
      denialFeedbackObserved: false,
      targetStateUnchanged: false,
    };
  });
  let completionRequests = 0;
  let nextAttemptIndex = 0;
  let completedAttemptCount = 0;
  let pendingAttempt;
  let fixtureError;
  const requestPaths = [];
  const requestDiagnostics = [];
  const server = createServer((request, response) => {
    const requestPath = new URL(request.url ?? '/', 'http://127.0.0.1').pathname;
    requestPaths.push(requestPath);
    const chunks = [];
    let requestBytes = 0;
    request.on('data', (chunk) => {
      requestBytes += chunk.length;
      if (requestBytes <= 8 << 20) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (requestBytes > 8 << 20) throw new Error('Copilot provider request exceeded the local size bound');
        if (request.method === 'GET' && /\/models$/.test(requestPath)) {
          response.writeHead(200, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify({
            object: 'list', data: [{ id: 'threadforge-copilot-fixture', object: 'model', created: 0, owned_by: 'threadforge-local' }],
          }));
          return;
        }
        if (request.method !== 'POST' || !/\/chat\/completions$/.test(requestPath)) {
          throw new Error('Copilot requested an unexpected local provider route');
        }
        const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const messages = Array.isArray(body.messages) ? body.messages : [];
        const toolResults = messages.filter((message) =>
          message?.role === 'tool' || message?.role === 'function' || message?.type === 'function_call_output');
        const latestToolResult = toolResults[completedAttemptCount];
        const toolResultText = latestToolResult ? toolMessageText(latestToolResult.content) : '';
        const denialSignals = {
          pre_tool_use: /Denied by preToolUse hook/i.test(toolResultText),
          threadforge: /Threadforge withheld this Copilot file operation/i.test(toolResultText),
          permission: /permission.*denied|denied.*permission/i.test(toolResultText),
          hook: /hook.*denied|denied.*hook/i.test(toolResultText),
        };
        requestDiagnostics.push({
          message_roles: messages.map((message) => typeof message?.role === 'string' ? message.role : message?.type ?? 'unknown').slice(-8),
          tool_result_count: toolResults.length,
          denial_signals: denialSignals,
        });
        completionRequests++;

        if (pendingAttempt) {
          if (!latestToolResult || toolResults.length !== completedAttemptCount + 1) {
            throw new Error(`Copilot did not return exactly one result for the ${pendingAttempt.tool} attempt`);
          }
          if (latestToolResult.tool_call_id && latestToolResult.tool_call_id !== pendingAttempt.callID) {
            throw new Error(`Copilot returned a mismatched tool result for the ${pendingAttempt.tool} attempt`);
          }
          pendingAttempt.denialFeedbackObserved = Object.values(denialSignals).some(Boolean);
          const target = path.join(project, pendingAttempt.targetPath);
          pendingAttempt.targetStateUnchanged = pendingAttempt.initialContent === null
            ? !existsSync(target)
            : existsSync(target) && readFileSync(target, 'utf8') === `${pendingAttempt.initialContent}\n`;
          if (!pendingAttempt.denialFeedbackObserved) {
            fixtureError = `Copilot did not return the preToolUse denial for ${pendingAttempt.tool}; diagnostics=${JSON.stringify(requestDiagnostics)}`;
          } else if (!pendingAttempt.targetStateUnchanged) {
            fixtureError = `Copilot changed a disposable target after the ${pendingAttempt.tool} denial`;
          }
          completedAttemptCount++;
          pendingAttempt = undefined;
        } else if (toolResults.length !== completedAttemptCount) {
          throw new Error('Copilot returned an unexpected or repeated tool result');
        }

        if (fixtureError) {
          sendFixtureCompletion(request, response, body, { content: 'A Copilot direct-file operation did not satisfy the denial fixture.' });
          return;
        }

        while (nextAttemptIndex < attempts.length) {
          const attempt = attempts[nextAttemptIndex];
          assert(attempt, 'expected a Copilot direct-file smoke attempt');
          const tools = Array.isArray(body.tools) ? body.tools : [];
          const selectedTool = tools.find((candidate) => {
            const name = candidate?.function?.name ?? candidate?.name;
            return name === attempt.tool;
          });
          nextAttemptIndex++;
          if (!selectedTool) {
            attempt.toolAvailable = false;
            attempt.availabilityReason = 'not-exposed';
            const target = path.join(project, attempt.targetPath);
            attempt.targetStateUnchanged = attempt.initialContent === null
              ? !existsSync(target)
              : existsSync(target) && readFileSync(target, 'utf8') === `${attempt.initialContent}\n`;
            continue;
          }
          const parameters = selectedTool.function?.parameters ?? selectedTool.parameters ?? {};
          const argumentsValue = buildCopilotPlanGateArguments(
            attempt.tool, parameters, attempt.targetPath, attempt.initialContent, attempt.replacementContent,
          );
          if (argumentsValue === null) {
            attempt.toolAvailable = false;
            attempt.availabilityReason = 'unsupported-arguments';
            const target = path.join(project, attempt.targetPath);
            attempt.targetStateUnchanged = attempt.initialContent === null
              ? !existsSync(target)
              : existsSync(target) && readFileSync(target, 'utf8') === `${attempt.initialContent}\n`;
            continue;
          }
          attempt.toolAvailable = true;
          attempt.availabilityReason = 'exposed';
          pendingAttempt = attempt;
          sendFixtureCompletion(request, response, body, {
            toolCall: {
              id: attempt.callID,
              name: selectedTool.function?.name ?? selectedTool.name,
              arguments: JSON.stringify(argumentsValue),
            },
          });
          return;
        }
        if (completedAttemptCount !== attempts.filter((attempt) => attempt.toolAvailable).length || pendingAttempt) {
          throw new Error('Copilot did not complete every exposed direct-file tool attempt');
        }
        const availableCount = attempts.filter((attempt) => attempt.toolAvailable).length;
        sendFixtureCompletion(request, response, body, {
          content: availableCount > 0
            ? `All ${availableCount} exposed direct-file operations were denied by the Threadforge task-plan gate.`
            : 'Copilot exposed none of the selected direct-file tools in this fixture.',
        });
      } catch (error) {
        fixtureError ??= String(error?.message ?? 'Copilot local hook fixture failed').slice(0, 256);
        response.writeHead(500, { 'content-type': 'application/json', connection: 'close' })
          .end(JSON.stringify({ error: { message: 'deterministic local Copilot fixture failed' } }));
      }
    });
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  assert(address && typeof address === 'object');
  const baseURL = `http://127.0.0.1:${address.port}/v1`;
  const copilotHome = path.join(isolatedHome, 'copilot-home');
  mkdirSync(copilotHome, { recursive: true });
  const copilotSettingsPath = path.join(copilotHome, 'config.json');
  const copilotSettings = existsSync(copilotSettingsPath) ? JSON.parse(readFileSync(copilotSettingsPath, 'utf8')) : {};
  const trustedFolders = Array.isArray(copilotSettings.trustedFolders) ? copilotSettings.trustedFolders : [];
  copilotSettings.trustedFolders = [...new Set([...trustedFolders, project])];
  copilotSettings.disableAllHooks = false;
  copilotSettings.autoUpdate = false;
  writeFileSync(copilotSettingsPath, `${JSON.stringify(copilotSettings, null, 2)}\n`, { mode: 0o600 });
  const copilotEnvironment = {
    ...isolatedEnvironment,
    COPILOT_HOME: copilotHome,
    COPILOT_AUTO_UPDATE: 'false',
    COPILOT_OFFLINE: 'true',
    COPILOT_PROVIDER_TYPE: 'openai',
    COPILOT_PROVIDER_BASE_URL: baseURL,
    COPILOT_PROVIDER_API_KEY: 'threadforge-fixture-only',
    COPILOT_PROVIDER_WIRE_API: 'completions',
    COPILOT_MODEL: 'gpt-4.1',
    COPILOT_PROVIDER_MODEL_ID: 'gpt-4.1',
    COPILOT_PROVIDER_WIRE_MODEL: 'threadforge-copilot-fixture',
  };

  try {
    const output = await runCopilotAgent(['--prompt',
      'Perform the four requested direct file operations exactly once each. Do not use any other tool.',
      '--allow-all-tools', '--silent'], {
      cwd: project, env: copilotEnvironment, timeout: 90_000,
    });
    if (fixtureError) throw new Error(`Copilot plan-gate hook fixture failed: ${fixtureError}; routes=${requestPaths.join(',')}`);
    assert.equal(nextAttemptIndex, attempts.length, 'expected a result for every selected Copilot direct file tool');
    assert.equal(completedAttemptCount, attempts.filter((attempt) => attempt.toolAvailable).length, 'expected one result for every exposed Copilot direct file tool');
    if (!attempts.every((attempt) => attempt.targetStateUnchanged
      && (attempt.toolAvailable === false || attempt.denialFeedbackObserved))) {
      throw new Error(`Copilot hook result mismatch: attempts=${JSON.stringify(attempts.map(({ tool, toolAvailable, denialFeedbackObserved, targetStateUnchanged }) => ({ tool, toolAvailable, denialFeedbackObserved, targetStateUnchanged })))}; request_diagnostics=${JSON.stringify(requestDiagnostics)}`);
    }
    const exposedToolCount = attempts.filter((attempt) => attempt.toolAvailable).length;
    if (exposedToolCount > 0) assert.ok(output.includes('denied'), 'Copilot final output did not reflect the synthetic denials');
    return {
      ...resultBase,
      status: exposedToolCount === attempts.length ? 'verified' : 'partial',
      provider: 'deterministic-loopback-fixture',
      tool_attempts: attempts.map(({ tool, toolAvailable, availabilityReason, denialFeedbackObserved, targetStateUnchanged }) => ({
        tool, tool_available: toolAvailable, availability_reason: availabilityReason,
        attempt_count: toolAvailable ? 1 : 0,
        denial_feedback_observed: denialFeedbackObserved, target_state_unchanged: targetStateUnchanged,
      })),
      hook_denial_count: attempts.filter((attempt) => attempt.denialFeedbackObserved).length,
      write_effects_observed: attempts.filter((attempt) => !attempt.targetStateUnchanged).length,
      provider_requests: completionRequests, provider_routes_loopback_only: true,
    };
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

async function runCopilotTaskResumeHostSmoke(version, taskFixture) {
  if (!version) return { status: 'not-installed', real_model_invocations: 0 };
  const copilotHome = path.join(isolatedHome, 'copilot-task-resume-home');
  mkdirSync(copilotHome, { recursive: true });
  const settingsPath = path.join(copilotHome, 'config.json');
  writeFileSync(settingsPath, `${JSON.stringify({
    trustedFolders: [projectRoot], disableAllHooks: true, autoUpdate: false,
  }, null, 2)}\n`, { mode: 0o600 });

  let providerRequests = 0;
  let auxiliaryProviderRequests = 0;
  let toolCallCount = 0;
  let toolResultObserved = false;
  let availableToolsRestricted = false;
  let fixtureError;
  const providerDiagnostics = [];
  const taskID = taskFixture.id;
  const objective = taskFixture.objective;
  const requiredMarkers = [
    'threadforge.task-resume-context.v1', 'requirements_not_finalized',
    '"action_ready":false', '"guidance_only":true', '"execution_authorized":false',
  ];
  const server = createServer((request, response) => {
    const requestPath = new URL(request.url ?? '/', 'http://127.0.0.1').pathname;
    const chunks = [];
    let requestBytes = 0;
    request.on('data', (chunk) => {
      requestBytes += chunk.length;
      if (requestBytes <= 8 << 20) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (requestBytes > 8 << 20) throw new Error('Copilot task-resume request exceeded the local size bound');
        if (request.method === 'GET' && /\/models$/.test(requestPath)) {
          response.writeHead(200, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify({
            object: 'list', data: [{ id: 'threadforge-copilot-task-resume-fixture', object: 'model', created: 0, owned_by: 'threadforge-local' }],
          }));
          return;
        }
        if (request.method !== 'POST' || !/\/chat\/completions$/.test(requestPath)) {
          throw new Error('Copilot requested an unexpected local task-resume provider route');
        }
        const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const messages = Array.isArray(body.messages) ? body.messages : [];
        const toolResults = messages.filter((message) => message?.role === 'tool' || message?.role === 'function');
        providerRequests++;

        if (toolResults.length > 0) {
          const resultText = toolResults.map((message) => toolMessageText(message.content)).join('\n');
          const compactResultText = resultText.replace(/\s+/g, '');
          const markersPresent = requiredMarkers.every((marker) => compactResultText.includes(marker.replace(/\s+/g, '')));
          if (toolResults.length !== 1 || !resultText.includes(objective) || !markersPresent || toolCallCount !== 1) {
            fixtureError = 'Copilot Threadforge MCP result omitted the selected task objective or advisory safety fields';
            sendFixtureCompletion(request, response, body, { content: 'The task-resume result omitted required advisory safety fields.' });
            return;
          }
          toolResultObserved = true;
          sendFixtureCompletion(request, response, body, {
            content: 'The task objective was read. Requirements are not finalized, context is guidance only, and execution is not authorized.',
          });
          return;
        }

        const tools = Array.isArray(body.tools) ? body.tools : [];
        if (providerDiagnostics.length < 8) {
          providerDiagnostics.push({
            message_roles: messages.map((message) => typeof message?.role === 'string' ? message.role : 'unknown').slice(-8),
            tool_names: tools.map((candidate) => candidate?.function?.name ?? candidate?.name ?? 'unknown').slice(0, 32),
            tool_result_count: toolResults.length,
          });
        }
        if (tools.length === 0 && toolCallCount === 0 && auxiliaryProviderRequests < 2) {
          auxiliaryProviderRequests++;
          sendFixtureCompletion(request, response, body, { content: 'Threadforge task-resume verification' });
          return;
        }
        const tool = tools.find((candidate) => {
          const name = candidate?.function?.name ?? candidate?.name;
          return typeof name === 'string' && name.endsWith('threadforge_task_resume_context');
        });
        if (!tool) throw new Error(`Copilot did not expose the Threadforge task-resume MCP tool; tools=${JSON.stringify(tools.map((candidate) => candidate?.function?.name ?? candidate?.name ?? 'unknown').slice(0, 32))}`);
        availableToolsRestricted = tools.length === 1;
        const toolName = tool.function?.name ?? tool.name;
        const schema = tool.function?.parameters ?? tool.parameters ?? {};
        const properties = schema.properties && typeof schema.properties === 'object' ? schema.properties : {};
        const required = Array.isArray(schema.required) ? schema.required : ['task_id'];
        const argumentsValue = {};
        for (const key of required) {
          if (key === 'task_id') argumentsValue[key] = taskID;
          else throw new Error('Copilot task-resume MCP tool exposed an unsupported required argument');
        }
        if (argumentsValue.task_id !== taskID || !Object.hasOwn(properties, 'task_id')) {
          throw new Error('Copilot task-resume MCP tool schema did not require task_id');
        }
        if (toolCallCount !== 0) throw new Error('Copilot attempted more than one Threadforge task-resume MCP call');
        toolCallCount++;
        sendFixtureCompletion(request, response, body, {
          toolCall: { id: 'call_copilot_threadforge_task_resume', name: toolName, arguments: JSON.stringify(argumentsValue) },
        });
      } catch (error) {
        fixtureError ??= String(error?.message ?? 'Copilot task-resume fixture failed').slice(0, 256);
        response.writeHead(500, { 'content-type': 'application/json', connection: 'close' })
          .end(JSON.stringify({ error: { message: 'deterministic local Copilot task-resume fixture failed' } }));
      }
    });
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  assert(address && typeof address === 'object');
  const baseURL = `http://127.0.0.1:${address.port}/v1`;
  const environment = {
    ...isolatedEnvironment,
    COPILOT_HOME: copilotHome,
    COPILOT_AUTO_UPDATE: 'false',
    COPILOT_OFFLINE: 'true',
    COPILOT_PROVIDER_TYPE: 'openai',
    COPILOT_PROVIDER_BASE_URL: baseURL,
    COPILOT_PROVIDER_API_KEY: 'threadforge-fixture-only',
    COPILOT_PROVIDER_WIRE_API: 'completions',
    COPILOT_MODEL: 'gpt-4.1',
    COPILOT_PROVIDER_MODEL_ID: 'gpt-4.1',
    COPILOT_PROVIDER_WIRE_MODEL: 'threadforge-copilot-task-resume-fixture',
  };
  try {
    const mcpListing = runHost('copilot', ['mcp', 'list', '--json'], {
      cwd: projectRoot, env: environment, timeout: 30_000,
    });
    assert.equal(hasNamedMcpServer(JSON.parse(mcpListing), 'threadforge'), true,
      'Copilot task-resume smoke home did not discover the project Threadforge MCP server');
    const output = await runCopilotAgent([
      '--prompt', 'Read the explicitly selected task through the Threadforge task-resume MCP tool and report its objective and advisory status.',
      '--available-tools=threadforge-threadforge_task_resume_context', '--allow-all-tools', '--no-auto-update', '--silent',
    ], { cwd: projectRoot, env: environment, timeout: 90_000 });
    if (fixtureError) throw new Error(`Copilot task-resume fixture failed: ${fixtureError}; provider_requests=${providerRequests}`);
    assert.equal(toolCallCount, 1,
      `Copilot did not make exactly one Threadforge task-resume MCP call; provider_requests=${providerRequests}; provider_diagnostics=${JSON.stringify(providerDiagnostics)}`);
    assert.equal(availableToolsRestricted, true,
      `Copilot exposed tools beyond the selected read-only task-resume MCP tool; provider_diagnostics=${JSON.stringify(providerDiagnostics)}`);
    assert.equal(toolResultObserved, true, 'Copilot did not receive the Threadforge task-resume MCP result');
    assert.ok(output.includes('Requirements are not finalized'), 'Copilot final response omitted the scripted advisory status');
    return {
      schema_version: 'threadforge.copilot-task-resume-smoke.v1',
      status: 'verified', host_cli_version: version,
      provider: 'deterministic-loopback-fixture', tool: 'threadforge_task_resume_context',
      tool_call_count: toolCallCount, tool_result_observed: toolResultObserved,
      task_context_content_retained_in_report: false,
      advisory_result_markers_verified: true, requirements_not_finalized_warning_verified: true,
      action_ready: false, guidance_only: true, execution_authorized: false,
      available_tools_restricted_to_task_resume: availableToolsRestricted,
      provider_requests: providerRequests, auxiliary_provider_requests: auxiliaryProviderRequests,
      real_model_invocations: 0,
    };
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

function buildCopilotCreateArguments(schema, targetPath) {
  const properties = schema?.properties && typeof schema.properties === 'object' ? schema.properties : {};
  const required = Array.isArray(schema?.required) ? schema.required : Object.keys(properties);
  const result = {};
  let pathAssigned = false;
  for (const key of required) {
    const property = properties[key] ?? {};
    const normalized = String(key).toLowerCase();
    if (/(?:file_?path|filepath|target_?path|filename)$/.test(normalized) || normalized === 'path') {
      result[key] = targetPath;
      pathAssigned = true;
    } else if (/(?:content|contents|text|code|body|description|summary)/.test(normalized)) {
      result[key] = 'synthetic Threadforge plan-gate smoke content';
    } else if (Array.isArray(property.enum) && property.enum.length > 0) {
      result[key] = property.enum[0];
    } else if (property.type === 'string' || property.type === undefined) {
      result[key] = `synthetic ${key}`;
    } else if (property.type === 'boolean') {
      result[key] = false;
    } else if (property.type === 'integer' || property.type === 'number') {
      result[key] = property.minimum ?? 1;
    } else if (property.type === 'array') {
      result[key] = [];
    } else if (property.type === 'object') {
      result[key] = {};
    } else {
      return null;
    }
  }
  for (const [key, property] of Object.entries(properties)) {
    if (result[key] !== undefined) continue;
    if (/(?:file_?path|filepath|target_?path|filename)$/.test(key.toLowerCase()) || key.toLowerCase() === 'path') {
      result[key] = targetPath;
      pathAssigned = true;
    }
  }
  return pathAssigned ? result : null;
}

function buildCopilotPlanGateArguments(toolName, schema, targetPath, originalContent, replacementContent) {
  const properties = schema?.properties && typeof schema.properties === 'object' ? schema.properties : {};
  if (toolName === 'apply_patch') {
    const patchKey = Object.keys(properties).find((key) => key.toLowerCase() === 'patch');
    if (!patchKey || (properties[patchKey]?.type && properties[patchKey].type !== 'string')) return null;
    return {
      [patchKey]: [
        '*** Begin Patch',
        `*** Add File: ${targetPath}`,
        `+${replacementContent}`,
        '*** End Patch',
      ].join('\n'),
    };
  }

  const result = buildCopilotCreateArguments(schema, targetPath);
  if (!result) return null;
  for (const [key, property] of Object.entries(properties)) {
    const normalized = key.toLowerCase();
    if (/(?:file_?path|filepath|target_?path|filename)$/.test(normalized) || normalized === 'path') {
      result[key] = targetPath;
    } else if (/^(?:old|original|search|find|needle|match)/.test(normalized)) {
      result[key] = originalContent ?? `synthetic ${key}`;
    } else if (/^(?:new|replace|replacement|substitute|insert)/.test(normalized)) {
      result[key] = replacementContent;
    } else if (toolName === 'str_replace_editor' && normalized === 'command') {
      if (Array.isArray(property.enum) && !property.enum.includes('str_replace')) return null;
      result[key] = 'str_replace';
    }
  }
  return result;
}

function windowsPathToBash(value) {
  const match = /^([A-Za-z]):[\\/](.*)$/.exec(value);
  return match ? `/${match[1].toLowerCase()}/${match[2].replaceAll('\\', '/')}` : value.replaceAll('\\', '/');
}

function quoteBashArgument(value) {
  return `'${String(value).replaceAll("'", "'\\''")}'`;
}

async function runCopilotAgent(args, options) {
  if (process.platform !== 'win32') return runAsync('copilot', args, options);
  const shimPath = run('where.exe', ['copilot.ps1'], { cwd: options.cwd, env: options.env, timeout: 10_000 })
    .split(/\r?\n/).map((entry) => entry.trim()).find(Boolean);
  if (!shimPath) throw new Error('Could not resolve the installed Copilot CLI PowerShell shim');
  const entrypoint = path.join(path.dirname(shimPath), 'node_modules', '@github', 'copilot', 'npm-loader.js');
  if (!existsSync(entrypoint)) throw new Error('Could not resolve the Copilot CLI Node entrypoint next to copilot.ps1');
  return runAsync(process.execPath, [entrypoint, ...args], options);
}

let smokeFailure;
async function main() {
try {
  mkdirSync(projectRoot, { recursive: true });
  mkdirSync(commandCodeProjectRoot, { recursive: true });
  const openCodePlanAgentInstallAction = await installOpenCodeProjectPlanAgent(projectRoot);
  assert.equal(openCodePlanAgentInstallAction, 'installed');

  // Exercise the OpenCode callback mapper with a payload-shaped fixture. Its
  // output is digest-only; no actual user transcript or model call is involved.
  const capturedEvents = [];
  const adapter = createOpenCodeAdapter(async (event) => { capturedEvents.push(event); });
  adapter.hooks['chat.message'](
    { sessionID: 'smoke-session-private', providerID: 'local', modelID: 'fixture' },
    { message: { role: 'user', content: 'private-smoke-prompt-do-not-store' } },
  );
  adapter.hooks['tool.execute.before'](
    { session_id: 'smoke-session-private', callID: 'smoke-call-private', tool: 'Read' },
    { args: { file_path: 'src/private-smoke-file.ts' } },
  );
  await adapter.flush();
  assert.equal(capturedEvents.length, 2);
  assert.equal(capturedEvents.every((event) => event.harness.name === 'opencode'), true);
  const capturedJSON = JSON.stringify(capturedEvents);
  for (const privateValue of ['private-smoke-prompt-do-not-store', 'smoke-session-private', 'smoke-call-private', 'src/private-smoke-file.ts']) {
    assert.equal(capturedJSON.includes(privateValue), false, `captured event retained ${privateValue}`);
  }
  const captureDigest = createHash('sha256').update(capturedJSON).digest('hex');

  const freshnessSourceRelativePath = 'src/opencode-smoke.ts';
  const freshnessSourcePath = path.join(projectRoot, ...freshnessSourceRelativePath.split('/'));
  const freshnessSourceBytes = Buffer.from('export const smokeSource = "initial";\n');
  mkdirSync(path.dirname(freshnessSourcePath), { recursive: true });
  writeFileSync(freshnessSourcePath, freshnessSourceBytes, { flag: 'wx', mode: 0o600 });
  const freshnessSourceSHA256 = createHash('sha256').update(freshnessSourceBytes).digest('hex');

  // This deterministic draft and validation report test lifecycle plumbing
  // only. They are not local-model output or held-out acceptance evidence.
  const procedureID = 'cross-harness-opencode-smoke';
  const graph = {
    schema_version: 'threadforge.procedure-graph.v1',
    id: procedureID,
    project_id: 'cross-harness-smoke',
    title: 'Review a change with focused verification',
    revision: 1,
    status: 'draft',
    applicability: { paths: ['src/**'] },
    nodes: [{ id: 'inspect', kind: 'trigger', instruction: 'Inspect the current change and identify its affected behavior.' }],
    edges: [],
    source_files: [{ path: freshnessSourceRelativePath, sha256: freshnessSourceSHA256 }],
    evidence: [{
      origin: 'trace', content_sha256: captureDigest, harness: 'opencode',
      observed_at: capturedEvents[0].timestamp, local_trace_ref: 'local://smoke/private-trace', redacted: true,
    }],
  };
  const graphPath = path.join(isolatedHome, 'draft.json');
  const validationPath = path.join(isolatedHome, 'validation.json');
  writeFileSync(graphPath, `${JSON.stringify(graph)}\n`, { flag: 'wx', mode: 0o600 });
  writeFileSync(validationPath, `${JSON.stringify({
    dataset_fingerprint: captureDigest,
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

  const goEnvironment = { ...process.env, GOPROXY: 'off', GOTOOLCHAIN: 'local' };
  run('go', ['build', '-mod=readonly', '-o', cliPath, './cmd/threadforge'], {
    cwd: repositoryRoot, env: goEnvironment, timeout: 180_000,
  });
  runThreadforge(['procedure', 'propose', '--file', graphPath, '--actor', 'smoke-capture', '--reason', 'isolated OpenCode adapter fixture', '--db', databasePath]);
  runThreadforge(['procedure', 'record-validation', '--id', procedureID, '--expected-revision', '1', '--report', validationPath, '--db', databasePath]);
  runThreadforge(['procedure', 'approve', '--id', procedureID, '--expected-revision', '2', '--reviewer', 'smoke-reviewer', '--reason', 'isolated lifecycle fixture only', '--privacy-reviewed', '--db', databasePath]);
  const exportResult = JSON.parse(runThreadforge([
    'procedure', 'export-skill', '--project-root', projectRoot, '--id', procedureID,
    '--privacy-reviewed', '--db', databasePath,
  ]));
  assert.equal(exportResult.graph_id, procedureID);
  assert.equal(exportResult.graph_revision, 3);
  const skillFile = path.join(projectRoot, ...exportResult.path.split('/'));
  const skillBytes = readFileSync(skillFile);
  const skillBody = new TextDecoder('utf-8', { fatal: true }).decode(skillBytes);
  assert.equal(createHash('sha256').update(skillBytes).digest('hex'), exportResult.sha256);
  assert.equal(skillBody.includes('local://smoke/private-trace'), false, 'portable export leaked its local trace reference');
  const skillName = /^---\r?\nname: "([^"]+)"/m.exec(skillBody)?.[1];
  assert.ok(skillName, 'Threadforge export did not contain a generated skill name');

  const neighborSkillPath = path.join(projectRoot, '.agents', 'skills', 'user-owned-neighbor', 'SKILL.md');
  const neighborSkillBytes = Buffer.from('---\nname: user-owned-neighbor\ndescription: Preserve this skill.\n---\nUser-owned content.\n');
  mkdirSync(path.dirname(neighborSkillPath), { recursive: true });
  writeFileSync(neighborSkillPath, neighborSkillBytes, { flag: 'wx', mode: 0o600 });
  const qoderCommandNeighbor = path.join(projectRoot, '.qoder', 'commands', 'user-owned-review.md');
  const qoderCommandNeighborBytes = Buffer.from('User-owned Qoder command; preserve it.\n');
  mkdirSync(path.dirname(qoderCommandNeighbor), { recursive: true });
  writeFileSync(qoderCommandNeighbor, qoderCommandNeighborBytes, { flag: 'wx', mode: 0o600 });
  const qoderInstructions = path.join(projectRoot, 'AGENTS.md');
  const qoderInstructionsBytes = Buffer.from('User-owned Qoder project instructions.\n');
  writeFileSync(qoderInstructions, qoderInstructionsBytes, { flag: 'wx', mode: 0o600 });
  const copilotInstructionsPath = path.join(projectRoot, '.github', 'copilot-instructions.md');
  const copilotInstructionsBytes = Buffer.from('User-owned Copilot repository instructions.\n');
  mkdirSync(path.dirname(copilotInstructionsPath), { recursive: true });
  writeFileSync(copilotInstructionsPath, copilotInstructionsBytes, { flag: 'wx', mode: 0o600 });

  const installationCases = [
    ['claude_code', installClaudeCodeProjectSkill, '.claude'],
    ['cline', installClineProjectSkill, '.cline'],
    ['github_copilot', installCopilotProjectSkill, '.github'],
    ['codex_cli', installCodexProjectSkill, '.agents'],
    ['cursor', installCursorProjectSkill, '.agents'],
    ['gemini_cli', installGeminiProjectSkill, '.agents'],
    ['antigravity', installAntigravityProjectSkill, '.agents'],
    ['opencode', installOpenCodeProjectSkill, '.agents'],
    ['zcode', installZCodeProjectSkill, '.agents'],
    ['deepseek_harness', installDeepSeekHarnessProjectSkill, '.dsh'],
    ['qoder', installQoderProjectSkill, '.qoder'],
  ];
  const installations = {};
  for (const [host, install, rootSegment] of installationCases) {
    const result = await install({ projectRoot, skillFile, sha256: exportResult.sha256 });
    const expectedPath = `${rootSegment}/skills/${skillName}/SKILL.md`;
    assert.equal(result.path, expectedPath, `${host} installed to an unexpected project path`);
    assert.equal(result.sha256, exportResult.sha256, `${host} changed the reviewed digest`);
    const installedBytes = readFileSync(path.join(projectRoot, ...result.path.split('/')));
    assert.deepEqual(installedBytes, skillBytes, `${host} changed the reviewed skill bytes`);
    assert.equal(createHash('sha256').update(installedBytes).digest('hex'), exportResult.sha256);
    installations[host] = { path: result.path, action: result.action, sha256_verified: true };
  }
  const commandCodeExport = JSON.parse(runThreadforge([
    'procedure', 'export-skill', '--project-root', commandCodeProjectRoot, '--id', procedureID,
    '--privacy-reviewed', '--db', databasePath,
  ]));
  assert.equal(commandCodeExport.graph_id, procedureID);
  assert.equal(commandCodeExport.sha256, exportResult.sha256);
  const commandCodeSkillFile = path.join(commandCodeProjectRoot, ...commandCodeExport.path.split('/'));
  const commandCodeSkillBytes = readFileSync(commandCodeSkillFile);
  assert.deepEqual(commandCodeSkillBytes, skillBytes);
  const commandCodeSkill = await installCommandCodeProjectSkill({
    projectRoot: commandCodeProjectRoot, skillFile: commandCodeSkillFile, sha256: commandCodeExport.sha256,
  });
  assert.equal(commandCodeSkill.path, `.commandcode/skills/${skillName}/SKILL.md`);
  assert.deepEqual(readFileSync(path.join(commandCodeProjectRoot, ...commandCodeSkill.path.split('/'))), commandCodeSkillBytes);
  installations.command_code = {
    path: commandCodeSkill.path, action: commandCodeSkill.action, sha256_verified: commandCodeSkill.sha256 === exportResult.sha256,
  };
  assert.deepEqual(readFileSync(neighborSkillPath), neighborSkillBytes,
    'project skill installers changed a neighboring user-owned skill');
  assert.deepEqual(readFileSync(qoderCommandNeighbor), qoderCommandNeighborBytes,
    'Qoder command installer changed a neighboring user-owned command');
  assert.deepEqual(readFileSync(qoderInstructions), qoderInstructionsBytes,
    'Qoder command installer changed user-owned project instructions');
  assert.deepEqual(readFileSync(copilotInstructionsPath), copilotInstructionsBytes,
    'Copilot skill installation changed user-owned repository instructions');
  assert.deepEqual(readFileSync(skillFile), skillBytes);

  const qoderCommand = await installQoderProjectCommand({ projectRoot, skillFile, sha256: exportResult.sha256 });
  assert.equal(qoderCommand.path, `.qoder/commands/${skillName}.md`);
  assert.equal(qoderCommand.source_sha256, exportResult.sha256);
  const qoderCommandBytes = readFileSync(path.join(projectRoot, ...qoderCommand.path.split('/')));
  assert.equal(createHash('sha256').update(qoderCommandBytes).digest('hex'), qoderCommand.command_sha256);
  assert.equal(new TextDecoder('utf-8', { fatal: true }).decode(qoderCommandBytes).includes(approvalNotice), true);

  const opencodeProbeModule = pathToFileURL(path.join(repositoryRoot, 'adapters', 'scripts', 'opencode-plugin-load-probe.ts')).href;
  const opencodePluginMarker = path.join(isolatedHome, 'opencode-plugin-loaded.marker');
  const opencodePluginCallbacksMarker = path.join(isolatedHome, 'opencode-plugin-callbacks.jsonl');
  isolatedEnvironment.THREADFORGE_OPENCODE_PLUGIN_SMOKE_MARKER = opencodePluginMarker;
  isolatedEnvironment.THREADFORGE_OPENCODE_PLUGIN_CALLBACKS_MARKER = opencodePluginCallbacksMarker;
  const opencodePluginAction = await installOpenCodeProjectPlugin(projectRoot, opencodeProbeModule, {
    projectId: 'cross-harness-smoke', maxItems: 4, maxTokens: 1200,
  });
  assert.equal(opencodePluginAction, 'installed');
  const opencodePluginRuntime = runOpenCodePluginLoadCheck(opencodePluginMarker);

  const openCodeVersion = runHost('opencode', ['--version']).trim();
  const openCodeReadOnlyPlanAgent = runOpenCodePlanAgentCheck(openCodeVersion);
  const openCodeSkills = runHost('opencode', ['debug', 'skill', '--pure']);
  assert.equal(await installOpenCodeProjectMcpServer(projectRoot, 'threadforge', cliPath, [
    'mcp', 'serve', '--db', databasePath, '--project-root', projectRoot, '--threadforge-managed-mcp=v1',
  ]), 'installed');
  const openCodeMcpOutput = runHost('opencode', ['mcp', 'list', '--pure']);
  const openCodeMcpStatus = openCodeMcpOutput.replace(/\u001b\[[0-9;]*m/g, '')
    .split(/\r?\n/).find((line) => /threadforge/i.test(line));
  assert.ok(openCodeMcpStatus, 'OpenCode MCP listing did not include the project Threadforge server');
  assert.match(openCodeMcpStatus, /connected/i, 'OpenCode did not establish the Threadforge MCP connection');
  const openCodeAgentMcpRead = await runOpenCodeAgentMcpRead(procedureID, graph.nodes[0].instruction, {
    hostVersion: openCodeVersion,
    requiredResultMarkers: [
      '"status":"complete"', '"freshness":"verified"',
      '"guidance_only":true', '"action_ready":false',
    ],
    forbiddenResultMarkers: ['local://smoke/private-trace'],
  });
  writeFileSync(freshnessSourcePath, 'export const smokeSource = "changed-after-approval";\n', { mode: 0o600 });
  const openCodeAgentStaleMcpRead = await runOpenCodeAgentMcpRead(
    procedureID,
    '"freshness":"stale"',
    {
      hostVersion: openCodeVersion,
      prompt: 'Read the approved procedure after its source has changed. Report only its freshness status; do not repeat its guidance.',
      finalResponse: 'The approved procedure is stale, so its instructions were withheld.',
      expectedFinalText: 'instructions were withheld',
      requiredResultMarkers: [
        '"status":"stale"', '"freshness":"stale"',
        '"guidance_only":true', '"action_ready":false',
      ],
      forbiddenResultMarkers: [graph.title, graph.nodes[0].instruction, 'local://smoke/private-trace'],
    },
  );
  const openCodeProcedureFreshness = {
    schema_version: 'threadforge.opencode-procedure-freshness-smoke.v1',
    status: 'verified',
    host_cli_version: openCodeVersion,
    provider: 'deterministic-loopback-fixture',
    fresh_read_status: 'complete',
    fresh_read_freshness: 'verified',
    fresh_guidance_returned: openCodeAgentMcpRead.required_result_markers_verified,
    local_trace_reference_withheld: openCodeAgentMcpRead.forbidden_result_markers_absent &&
      openCodeAgentStaleMcpRead.forbidden_result_markers_absent,
    stale_read_status: 'stale',
    stale_read_freshness: 'stale',
    stale_guidance_withheld: openCodeAgentStaleMcpRead.forbidden_result_markers_absent,
    guidance_only: true,
    action_ready: false,
    execution_authorized: false,
    real_model_invocations: 0,
  };
  writeFileSync(freshnessSourcePath, freshnessSourceBytes, { mode: 0o600 });
  const openCodeTaskFixture = {
    id: 'opencode-mcp-resume-smoke-task',
    projectID: 'cross-harness-smoke',
    title: 'Synthetic OpenCode task-resume fixture',
    objective: 'Synthetic objective marker: preserve project guidance across harnesses.',
  };
  const createdOpenCodeTask = JSON.parse(runThreadforge([
    'task', 'create', '--id', openCodeTaskFixture.id, '--project-id', openCodeTaskFixture.projectID,
    '--title', openCodeTaskFixture.title, '--objective', openCodeTaskFixture.objective,
    '--criterion', 'Read the task snapshot and report its advisory status.', '--db', databasePath,
  ]));
  assert.equal(createdOpenCodeTask.id, openCodeTaskFixture.id, 'OpenCode task-resume fixture was not created in the isolated store');
  const openCodeTaskMemoryPluginAction = await installOpenCodeProjectPlugin(projectRoot, opencodeProbeModule, {
    projectId: openCodeTaskFixture.projectID, maxItems: 4, maxTokens: 1200,
    taskId: openCodeTaskFixture.id, maxTaskEntries: 4,
  });
  assert.equal(openCodeTaskMemoryPluginAction, 'updated', 'OpenCode plugin did not opt into the explicit synthetic task snapshot');
  const openCodeAgentTaskResume = await runOpenCodeAgentMcpRead(
    openCodeTaskFixture.id,
    openCodeTaskFixture.objective,
    {
      hostVersion: openCodeVersion,
      toolSuffix: 'threadforge_task_resume_context',
      toolArguments: { task_id: openCodeTaskFixture.id },
      prompt: 'Use the Threadforge MCP task-resume tool to read this task and summarize its objective and safety status.',
      finalResponse: 'The task objective was read. Its requirements are not finalized, and the snapshot is guidance only, not execution authorization.',
      expectedFinalText: 'requirements are not finalized',
      requiredResultMarkers: [
        'threadforge.task-resume-context.v1',
        'requirements_not_finalized',
        '"action_ready":false',
        '"guidance_only":true',
        '"execution_authorized":false',
      ],
    },
  );
  const opencodePluginCallbacks = readFileSync(opencodePluginCallbacksMarker, 'utf8').trim().split(/\r?\n/)
    .filter(Boolean).slice(0, 256).map((line) => JSON.parse(line));
  assert.ok(opencodePluginCallbacks.some((callback) => callback.hook === 'chat.message' && callback.role === 'user'),
    'OpenCode did not invoke chat.message with a user message');
  const opencodePluginCallbackSummary = {
    callback_count: opencodePluginCallbacks.length,
    hook_names: [...new Set(opencodePluginCallbacks.map((callback) => callback.hook))].sort(),
    event_types: [...new Set(opencodePluginCallbacks.map((callback) => callback.event_type).filter(Boolean))].sort(),
    chat_message_roles: [...new Set(opencodePluginCallbacks.filter((callback) => callback.hook === 'chat.message').map((callback) => callback.role).filter(Boolean))].sort(),
    assistant_message_update_completed: opencodePluginCallbacks.some((callback) =>
      callback.hook === 'event' && callback.event_type === 'message.updated' && callback.role === 'assistant' && callback.completed_time_present === true),
  };

  assert.equal(await installCodexProjectMcpServer(projectRoot, cliPath, [
    'mcp', 'serve', '--db', databasePath, '--project-root', projectRoot, '--threadforge-managed-mcp=v1',
  ]), 'installed');
  const codexVersion = runHost('codex', ['--version']).trim();
  const codexAgentMcpRead = await runCodexAgentMcpRead({
    projectRoot, codexHome, cliPath, databasePath, isolatedEnvironment, procedureID,
    expectedGuidance: graph.nodes[0].instruction,
    hostVersion: codexVersion,
  });
  const codexTaskResumeResult = await runCodexAgentMcpRead({
    projectRoot, codexHome, cliPath, databasePath, isolatedEnvironment,
    procedureID: openCodeTaskFixture.id,
    expectedGuidance: openCodeTaskFixture.objective,
    hostVersion: codexVersion,
    toolName: 'threadforge_task_resume_context',
    toolArguments: { task_id: openCodeTaskFixture.id },
    prompt: 'Read this explicitly selected Threadforge task context and report its objective and advisory status. Do not use shell or modify files.',
    expectedFinalResponse: 'The task objective was read. Requirements are not finalized, and the context is advisory only; execution is not authorized.',
    requiredResultMarkers: [
      'threadforge.task-resume-context.v1',
      'requirements_not_finalized',
      '"action_ready":false',
      '"guidance_only":true',
      '"execution_authorized":false',
    ],
  });
  assert.equal(codexTaskResumeResult.status, 'verified', 'Codex did not complete the isolated read-only task-resume MCP call');
  const handoffTaskFixture = {
    id: 'opencode-codex-handoff-smoke-task',
    projectID: openCodeTaskFixture.projectID,
  };
  const createdHandoffTask = JSON.parse(runThreadforge([
    'task', 'create', '--id', handoffTaskFixture.id, '--project-id', handoffTaskFixture.projectID,
    '--title', 'Synthetic OpenCode/Codex offered-handoff fixture',
    '--objective', 'Inspect the synthetic checkpoint from a separate harness.',
    '--criterion', 'Read the offered state without changing ownership.', '--db', databasePath,
  ]));
  assert.equal(createdHandoffTask.id, handoffTaskFixture.id, 'isolated handoff task was not created');
  const handoffFixture = {
    id: 'opencode-codex-offered-handoff-smoke',
    claimID: 'opencode-codex-handoff-claim-smoke',
    checkpointID: 'opencode-codex-handoff-checkpoint-smoke',
    receiver: 'codex-smoke-receiver',
    baselineSHA256: 'b'.repeat(64),
    checkpointSummary: 'Synthetic checkpoint marker: inspect the fixture from the receiving harness.',
  };
  const taskOwner = createdHandoffTask.owner;
  assert.equal(typeof taskOwner, 'string', 'task fixture did not record its local owner');
  assert.ok(taskOwner.length > 0, 'task fixture owner was empty');
  const ownerBeforeHandoffRead = JSON.parse(runThreadforge([
    'task', 'show', '--id', handoffTaskFixture.id, '--db', databasePath,
  ]));
  runThreadforge([
    'task', 'claim', 'acquire', '--task-id', handoffTaskFixture.id, '--id', handoffFixture.claimID,
    '--expected-task-revision', String(ownerBeforeHandoffRead.revision),
    '--baseline-sha256', handoffFixture.baselineSHA256, '--scope', 'path:src/**', '--actor', taskOwner,
    '--db', databasePath,
  ]);
  const draftedHandoff = JSON.parse(runThreadforge([
    'task', 'handoff', 'draft', '--id', handoffFixture.id, '--task-id', handoffTaskFixture.id,
    '--expected-task-revision', String(ownerBeforeHandoffRead.revision), '--claim-id', handoffFixture.claimID,
    '--expected-claim-revision', '1', '--baseline-sha256', handoffFixture.baselineSHA256,
    '--to-owner', handoffFixture.receiver, '--checkpoint-entry-id', handoffFixture.checkpointID,
    '--checkpoint-summary', handoffFixture.checkpointSummary,
    '--checkpoint-details', 'Synthetic detail remains in the isolated local store.', '--actor', taskOwner,
    '--db', databasePath,
  ]));
  assert.equal(draftedHandoff.handoffs?.[0]?.status, 'draft', 'handoff fixture did not enter the draft state');
  const offeredHandoff = JSON.parse(runThreadforge([
    'task', 'handoff', 'offer', '--id', handoffFixture.id, '--task-id', handoffTaskFixture.id,
    '--expected-revision', '1', '--actor', taskOwner, '--db', databasePath,
  ]));
  assert.equal(offeredHandoff.handoffs?.[0]?.status, 'offered', 'handoff fixture was not explicitly offered');
  assert.equal(offeredHandoff.execution_authorized, false, 'offered handoff unexpectedly authorized execution');
  const handoffResultMarkers = [
    'threadforge.task-handoff-page.v1', '"total_count":1', '"offset":0', '"limit":1',
    '"has_more":false', '"status":"offered"', '"action_ready":false',
    '"guidance_only":true', '"execution_authorized":false',
  ];
  const openCodeAgentTaskHandoffRead = await runOpenCodeAgentMcpRead(
    handoffTaskFixture.id,
    handoffFixture.checkpointSummary,
    {
      hostVersion: openCodeVersion,
      toolSuffix: 'threadforge_task_handoffs',
      toolArguments: { task_id: handoffTaskFixture.id, limit: 1, offset: 0 },
      prompt: 'Read the explicitly offered handoff page from Threadforge MCP. Report only its status and advisory safety boundary; do not accept or change it.',
      finalResponse: 'The offered handoff is visible as advisory guidance only; ownership and execution authority did not change.',
      expectedFinalText: 'ownership and execution authority did not change',
      requiredResultMarkers: handoffResultMarkers,
      privateTelemetryMarkers: [handoffFixture.id, handoffFixture.receiver],
    },
  );
  const codexAgentTaskHandoffRead = await runCodexAgentMcpRead({
    projectRoot, codexHome, cliPath, databasePath, isolatedEnvironment,
    procedureID: handoffTaskFixture.id,
    expectedGuidance: handoffFixture.checkpointSummary,
    hostVersion: codexVersion,
    toolName: 'threadforge_task_handoffs',
    toolArguments: { task_id: handoffTaskFixture.id, limit: 1, offset: 0 },
    prompt: 'Read the same explicitly offered Threadforge handoff page. Report only its status and advisory safety boundary; do not accept, use shell, or modify files.',
    expectedFinalResponse: 'The same offered handoff is visible as advisory guidance only; ownership and execution authority did not change.',
    requiredResultMarkers: handoffResultMarkers,
    privateTelemetryValues: [handoffTaskFixture.id, handoffFixture.id, handoffFixture.receiver],
  });
  const ownerAfterHandoffReads = JSON.parse(runThreadforge([
    'task', 'show', '--id', handoffTaskFixture.id, '--db', databasePath,
  ]));
  assert.equal(ownerAfterHandoffReads.owner, ownerBeforeHandoffRead.owner,
    'read-only cross-harness handoff calls changed the task owner');
  assert.equal(ownerAfterHandoffReads.revision, ownerBeforeHandoffRead.revision,
    'read-only cross-harness handoff calls changed the task revision');
  assert.equal(openCodeAgentTaskHandoffRead.tool, 'threadforge_task_handoffs');
  assert.equal(openCodeAgentTaskHandoffRead.tool_call_count, 1);
  assert.equal(openCodeAgentTaskHandoffRead.tool_result_observed, true);
  assert.equal(openCodeAgentTaskHandoffRead.handoff_page_contract_markers_verified, true);
  assert.equal(codexAgentTaskHandoffRead.tool, 'threadforge_task_handoffs');
  assert.equal(codexAgentTaskHandoffRead.tool_call_count, 1);
  assert.equal(codexAgentTaskHandoffRead.tool_result_observed, true);
  assert.equal(codexAgentTaskHandoffRead.handoff_page_contract_markers_verified, true);
  assert.equal(typeof codexAgentTaskHandoffRead.private_identifiers_detected_in_telemetry, 'boolean',
    'Codex handoff smoke did not classify local telemetry identifier presence');
  const taskHandoffCrossHarnessSmoke = {
    schema_version: 'threadforge.task-handoff-cross-harness-smoke.v1',
    status: 'verified',
    provider: 'deterministic-loopback-fixture',
    opencode: {
      host_cli_version: openCodeVersion,
      tool_call_count: openCodeAgentTaskHandoffRead.tool_call_count,
      tool_result_observed: openCodeAgentTaskHandoffRead.tool_result_observed,
      page_contract_markers_verified: openCodeAgentTaskHandoffRead.handoff_page_contract_markers_verified,
      private_identifiers_absent_from_telemetry: openCodeAgentTaskHandoffRead.private_identifiers_absent_from_telemetry,
    },
    codex_cli: {
      host_cli_version: codexVersion,
      tool_call_count: codexAgentTaskHandoffRead.tool_call_count,
      tool_result_observed: codexAgentTaskHandoffRead.tool_result_observed,
      page_contract_markers_verified: codexAgentTaskHandoffRead.handoff_page_contract_markers_verified,
      local_telemetry_contains_task_or_handoff_identifiers: codexAgentTaskHandoffRead.private_identifiers_detected_in_telemetry,
    },
    same_offered_handoff_read_by_both: true,
    bounded_page_verified: true,
    advisory_only_verified: true,
    task_owner_and_revision_unchanged: true,
    handoff_content_retained_in_report: false,
    task_or_handoff_identifiers_retained_in_report: false,
    action_ready: false,
    guidance_only: true,
    execution_authorized: false,
    ownership_transferred: false,
    real_model_invocations: 0,
  };
  const codexPlanGateTask = {
    id: 'codex-plan-gate-smoke-task',
    projectID: 'codex-plan-gate-smoke-project',
  };
  runThreadforge([
    'task', 'create', '--id', codexPlanGateTask.id, '--project-id', codexPlanGateTask.projectID,
    '--title', 'Synthetic Codex plan-gate task', '--objective', 'Keep this task in planning-only status.',
    '--criterion', 'Do not create or modify files.', '--db', databasePath,
  ]);
  runThreadforge([
    'task', 'requirements', 'finalize', '--id', codexPlanGateTask.id, '--expected-revision', '1',
    '--actor', 'installed-smoke', '--reason', 'synthetic plan-gate fixture requirements are final',
    '--db', databasePath,
  ]);
  const codexPlanFile = path.join(isolatedHome, 'codex-plan-gate-plan.md');
  const codexPlanScopeFile = path.join(isolatedHome, 'codex-plan-gate-scope.json');
  writeFileSync(codexPlanFile, 'Keep this task as an approved plan only; do not execute edits.\n', { flag: 'wx', mode: 0o600 });
  writeFileSync(codexPlanScopeFile, '{"include":["src/**"],"exclude":[]}', { flag: 'wx', mode: 0o600 });
  runThreadforge([
    'task', 'plan', 'revise', '--id', codexPlanGateTask.id, '--expected-revision', '2',
    '--file', codexPlanFile, '--scope-file', codexPlanScopeFile, '--db', databasePath,
  ]);
  runThreadforge([
    'task', 'plan', 'confirm', '--id', codexPlanGateTask.id, '--expected-revision', '3',
    '--decision', 'plan_only', '--actor', 'installed-smoke', '--reason', 'synthetic fixture must remain plan only',
    '--db', databasePath,
  ]);
  const codexPlanGateSmoke = await runCodexAgentPlanGateAttempt({
    projectRoot, codexHome, cliPath, databasePath, isolatedEnvironment,
    taskID: codexPlanGateTask.id, projectID: codexPlanGateTask.projectID, hostVersion: codexVersion,
  });
  const codexAgentTaskResume = {
    schema_version: 'threadforge.codex-task-resume-smoke.v1',
    status: codexTaskResumeResult.status,
    host_cli_version: codexVersion,
    provider: codexTaskResumeResult.provider,
    tool: codexTaskResumeResult.tool,
    tool_call_count: codexTaskResumeResult.tool_call_count,
    tool_result_observed: codexTaskResumeResult.tool_result_observed,
    task_context_content_retained_in_report: false,
    advisory_result_markers_verified: codexTaskResumeResult.advisory_result_markers_verified,
    requirements_not_finalized_warning_verified: codexTaskResumeResult.requirements_not_finalized_warning_verified,
    action_ready: codexTaskResumeResult.action_ready,
    guidance_only: codexTaskResumeResult.guidance_only,
    execution_authorized: codexTaskResumeResult.execution_authorized,
    completion_requests: codexTaskResumeResult.completion_requests,
    auxiliary_completion_requests: codexTaskResumeResult.auxiliary_completion_requests,
    telemetry: codexTaskResumeResult.telemetry,
    real_model_invocations: 0,
  };
  const geminiVersion = runHost('gemini', ['--version']).trim();
  const geminiCliSmokeResults = await runGeminiCliTelemetrySmoke({
    smokeRoot: isolatedHome, isolatedEnvironment, runGemini: runGeminiCliAsync,
    version: geminiVersion, cliPath, taskFixture: openCodeTaskFixture,
  });
  const geminiCliTelemetrySmoke = geminiCliSmokeResults.telemetry;
  const geminiAgentTaskResume = geminiCliSmokeResults.task_resume;
  const geminiCliPlanGate = geminiCliSmokeResults.task_plan_gate;

  const antigravityMcpConfig = {
    command: cliPath,
    args: ['mcp', 'serve', '--db', databasePath, '--project-root', projectRoot, '--threadforge-managed-mcp=v1'],
  };
  assert.equal(await installProjectMcpServer(projectRoot, 'antigravity', 'threadforge', antigravityMcpConfig), 'installed');
  const antigravityMcpFile = JSON.parse(readFileSync(path.join(projectRoot, '.agents', 'mcp_config.json'), 'utf8'));
  assert.deepEqual(antigravityMcpFile.mcpServers?.threadforge, antigravityMcpConfig,
    'Threadforge did not write the expected isolated Antigravity project MCP entry');
  const antigravityMcpListHelp = runHost('agy', ['mcp', 'list', '--help'], { includeStderr: true });
  const antigravityMcpListHelpFlags = [...new Set(
    [...antigravityMcpListHelp.matchAll(/(?:^|\s)(--[a-z][a-z0-9-]*)/gi)].map((match) => match[1]),
  )].sort();
  const antigravityMcpOutput = runHost('agy', ['mcp', 'list']);
  const antigravityMcpListed = antigravityMcpOutput.includes('threadforge');
  const antigravityVersion = runHost('agy', ['--version']).trim();
  const antigravityAgentMcpRead = await runAntigravityAgentMcpRead({
    projectRoot, home: isolatedHome, cliPath, procedureID,
    expectedGuidance: graph.nodes[0].instruction,
    isolatedEnvironment,
    hostVersion: antigravityVersion,
  });
  const antigravityTaskResumeResult = await runAntigravityAgentMcpRead({
    projectRoot, home: isolatedHome, cliPath, procedureID: openCodeTaskFixture.id,
    expectedGuidance: openCodeTaskFixture.objective,
    isolatedEnvironment,
    hostVersion: antigravityVersion,
    toolName: 'threadforge_task_resume_context',
    toolArguments: { task_id: openCodeTaskFixture.id },
    prompt: 'Read this explicitly selected Threadforge task snapshot. Report its objective and advisory safety status; do not use any other tool.',
    expectedFinalResponse: 'The task objective was read. Requirements are not finalized, and the context is advisory only; execution is not authorized.',
    requiredResultMarkers: [
      'threadforge.task-resume-context.v1',
      'requirements_not_finalized',
      '"action_ready":false',
      '"guidance_only":true',
      '"execution_authorized":false',
    ],
  });
  assert.equal(antigravityTaskResumeResult.status, 'verified',
    `Antigravity task-resume smoke failed safely: ${JSON.stringify({
      reason: antigravityTaskResumeResult.reason,
      host_failure_category: antigravityTaskResumeResult.host_failure_category,
      provider_requests: antigravityTaskResumeResult.provider_requests,
      tool_call_count: antigravityTaskResumeResult.tool_call_count,
      tool_result_observed: antigravityTaskResumeResult.tool_result_observed,
      result_check_diagnostics: antigravityTaskResumeResult.result_check_diagnostics,
      declared_tool_schemas: antigravityTaskResumeResult.provider_trace?.flatMap((trace) => trace.declared_tool_schemas ?? []).slice(-4),
      function_calls: antigravityTaskResumeResult.provider_trace?.flatMap((trace) => trace.function_calls ?? []).slice(-2),
      function_responses: antigravityTaskResumeResult.provider_trace?.flatMap((trace) => trace.function_responses ?? []).slice(-2),
      external_network_attempts_blocked: antigravityTaskResumeResult.external_network_attempts_blocked,
    })}`);
  const antigravityDeniedFile = path.join(projectRoot, 'src', 'threadforge-antigravity-plan-gate-denied.txt');
  const antigravityPlanGateAuditPath = path.join(isolatedHome, 'antigravity-plan-gate-audit.jsonl');
  const antigravityPlanGateObserver = path.join(repositoryRoot, 'adapters', 'scripts', 'antigravity-plan-gate-observer.mjs');
  mkdirSync(path.dirname(antigravityDeniedFile), { recursive: true });
  assert.equal(existsSync(antigravityDeniedFile), false, 'Antigravity plan-gate sentinel unexpectedly exists before smoke');
  const antigravityPlanGateCommand = `"${process.execPath}" "${antigravityPlanGateObserver}" "${antigravityPlanGateAuditPath}" "${cliPath}" task plan guard --host antigravity-cli --task-id ${openCodeTaskFixture.id} --project-id ${openCodeTaskFixture.projectID} --project-root "${projectRoot}" --db "${databasePath}" --threadforge-managed-antigravity-plan-gate=v1 --threadforge-plan-gate=antigravity-v1`;
  const antigravityPlanGateInstallAction = await installAntigravityProjectPlanGateHook({
    command: antigravityPlanGateCommand,
    projectRoot,
  });
  const antigravityPlanGateResult = await runAntigravityPlanGateAttempt({
    projectRoot, home: isolatedHome, cliPath, isolatedEnvironment,
    deniedFilePath: antigravityDeniedFile, auditPath: antigravityPlanGateAuditPath, hostVersion: antigravityVersion,
  });
  const antigravityDeniedFileCreated = existsSync(antigravityDeniedFile);
  const antigravityPlanGateProbeValid = (
    antigravityPlanGateResult.status === 'verified' &&
    antigravityPlanGateResult.tool_call_count === 1 &&
    antigravityPlanGateResult.hook_callback_invocations === 1 &&
    antigravityPlanGateResult.guard_decision === 'deny' &&
    antigravityPlanGateResult.host_tool_error_observed
  ) || (
    antigravityPlanGateResult.status === 'not_observed' &&
    antigravityPlanGateResult.tool_call_count === 1 &&
    antigravityPlanGateResult.hook_callback_invocations === 0 &&
    antigravityPlanGateResult.guard_decision === 'unknown'
  ) || (
    antigravityPlanGateResult.status === 'probe_not_exercised' &&
    antigravityPlanGateResult.tool_call_count === 0 &&
    antigravityPlanGateResult.hook_callback_invocations === 0 &&
    antigravityPlanGateResult.guard_decision === 'unknown' &&
    !antigravityPlanGateResult.host_tool_error_observed
  ) || (
    antigravityPlanGateResult.status === 'not_verified' &&
    antigravityPlanGateResult.tool_call_count === 1 &&
    antigravityPlanGateResult.hook_callback_invocations === 1 &&
    antigravityPlanGateResult.guard_decision === 'deny' &&
    !antigravityPlanGateResult.host_tool_error_observed
  );
  assert.ok(antigravityPlanGateProbeValid && antigravityPlanGateResult.provider_requests > 0 &&
    antigravityPlanGateResult.local_fixture_authentication_validated && antigravityPlanGateResult.real_model_invocations === 0,
    `Antigravity task-plan gate smoke failed safely: ${JSON.stringify({
      status: antigravityPlanGateResult.status,
      reason: antigravityPlanGateResult.reason,
      host_failure_category: antigravityPlanGateResult.host_failure_category,
      host_error_summary: antigravityPlanGateResult.host_error_summary,
      provider_requests: antigravityPlanGateResult.provider_requests,
      tool_call_count: antigravityPlanGateResult.tool_call_count,
      hook_callback_invocations: antigravityPlanGateResult.hook_callback_invocations,
      guard_decision: antigravityPlanGateResult.guard_decision,
      host_tool_error_observed: antigravityPlanGateResult.host_tool_error_observed,
      declared_tool_schemas: antigravityPlanGateResult.provider_trace?.flatMap((trace) => trace.declared_tool_schemas ?? []).slice(-4),
      function_calls: antigravityPlanGateResult.provider_trace?.flatMap((trace) => trace.function_calls ?? []).slice(-2),
      function_responses: antigravityPlanGateResult.provider_trace?.flatMap((trace) => trace.function_responses ?? []).slice(-2),
      external_network_attempts_blocked: antigravityPlanGateResult.external_network_attempts_blocked,
      denied_file_created: antigravityDeniedFileCreated,
    })}`);
  assert.equal(antigravityDeniedFileCreated, false, 'Antigravity plan gate did not prevent the denied file write');
  const antigravityPlanGateSmoke = {
    schema_version: 'threadforge.antigravity-plan-gate-smoke.v1',
    status: antigravityPlanGateResult.status,
    host_cli_version: antigravityVersion,
    install_action: antigravityPlanGateInstallAction,
    hook_event: 'PreToolUse',
    tool: antigravityPlanGateResult.tool,
    tool_call_count: antigravityPlanGateResult.tool_call_count,
    direct_file_tools_declared: antigravityPlanGateResult.direct_file_tools_declared,
    hook_callback_invocations: antigravityPlanGateResult.hook_callback_invocations,
    guard_decision: antigravityPlanGateResult.guard_decision,
    host_tool_error_observed: antigravityPlanGateResult.host_tool_error_observed,
    sandbox_enabled: false,
    sentinel_created: antigravityDeniedFileCreated,
    provider: 'loopback-gemini-api-fixture',
    provider_requests: antigravityPlanGateResult.provider_requests,
    real_model_invocations: 0,
  };
  const antigravityAgentTaskResume = {
    schema_version: 'threadforge.antigravity-task-resume-smoke.v1',
    status: antigravityTaskResumeResult.status,
    host_cli_version: antigravityVersion,
    provider: antigravityTaskResumeResult.provider,
    tool: antigravityTaskResumeResult.tool,
    tool_call_count: antigravityTaskResumeResult.tool_call_count,
    tool_result_observed: antigravityTaskResumeResult.tool_result_observed,
    task_context_content_retained_in_report: false,
    advisory_result_markers_verified: antigravityTaskResumeResult.advisory_result_markers_verified,
    requirements_not_finalized_warning_verified: antigravityTaskResumeResult.requirements_not_finalized_warning_verified,
    action_ready: antigravityTaskResumeResult.action_ready,
    guidance_only: antigravityTaskResumeResult.guidance_only,
    execution_authorized: antigravityTaskResumeResult.execution_authorized,
    provider_requests: antigravityTaskResumeResult.provider_requests,
    auxiliary_provider_requests: antigravityTaskResumeResult.auxiliary_provider_requests,
    local_fixture_authentication_validated: antigravityTaskResumeResult.local_fixture_authentication_validated,
    external_network_attempts_blocked: antigravityTaskResumeResult.external_network_attempts_blocked,
    real_model_invocations: antigravityTaskResumeResult.real_model_invocations,
  };

  let commandCodeSkillDiscovered;
  let commandCodeAgentMcpRead;
  let copilotProjectMcpDiscovery;
  let copilotTaskResumeSmoke;
  let copilotPlanGateSmoke;
  if (process.env.THREADFORGE_OPENCODE_MEMORY_SMOKE_ONLY !== '1') {
  const commandCodeMcpConfig = {
    command: cliPath,
    args: ['mcp', 'serve', '--db', databasePath, '--project-root', commandCodeProjectRoot, '--threadforge-managed-mcp=v1'],
  };
  assert.equal(await installProjectMcpServer(commandCodeProjectRoot, 'command-code', 'threadforge', commandCodeMcpConfig), 'installed');
  const commandCodeMcpOutput = runHost('cmdc', ['--no-auto-update', 'mcp', 'list'], {
    cwd: commandCodeProjectRoot, timeout: 60_000,
  });
  assert.ok(commandCodeMcpOutput.toLowerCase().includes('threadforge'),
    'Command Code did not list the isolated project Threadforge MCP server');
  const commandCodeSkillOutput = runHost('cmdc', ['--no-auto-update', 'skills', 'list'], { cwd: commandCodeProjectRoot });
  commandCodeSkillDiscovered = commandCodeSkillOutput.includes(skillName);
  commandCodeAgentMcpRead = await runCommandCodeAgentMcpRead({
    projectRoot: commandCodeProjectRoot,
    home: isolatedHome,
    procedureID,
    expectedGuidance: graph.nodes[0].instruction,
    isolatedEnvironment,
    runCLI: (args, options) => runHost('cmdc', args, options),
  });

  const copilotMcpConfig = {
    command: cliPath,
    args: ['mcp', 'serve', '--db', databasePath, '--project-root', projectRoot, '--threadforge-managed-mcp=v1'],
  };
  assert.equal(await installProjectMcpServer(projectRoot, 'github-copilot', 'threadforge', copilotMcpConfig), 'installed');
  copilotProjectMcpDiscovery = tryCopilotProjectMcpDiscovery(projectRoot);
  copilotTaskResumeSmoke = await runCopilotTaskResumeHostSmoke(
    copilotProjectMcpDiscovery.status === 'verified' ? copilotProjectMcpDiscovery.version : undefined,
    openCodeTaskFixture,
  );
  copilotPlanGateSmoke = await runCopilotPlanGateHostSmoke(
    copilotProjectMcpDiscovery.status === 'verified' ? copilotProjectMcpDiscovery.version : undefined,
  );
  }

  const claudeMemorySource = 'docs/claude-memory-smoke-fixture.md';
  const claudeGuidance = 'For local verification, use only the isolated dummy API key and never print or persist credential values.';
  const claudeSourceBytes = Buffer.from(`# Disposable Claude memory smoke fixture\n\n${claudeGuidance}\n`);
  const claudeSourcePath = path.join(projectRoot, ...claudeMemorySource.split('/'));
  mkdirSync(path.dirname(claudeSourcePath), { recursive: true });
  writeFileSync(claudeSourcePath, claudeSourceBytes, { flag: 'wx', mode: 0o600 });
  const sourceReceipt = JSON.parse(runThreadforge([
    'memory', 'fingerprint', '--project-root', projectRoot, '--path', claudeMemorySource,
  ]));
  assert.equal(sourceReceipt.status, 'complete');
  assert.equal(sourceReceipt.sources.length, 1);
  const candidatePath = path.join(isolatedHome, 'claude-memory-candidate.json');
  writeFileSync(candidatePath, `${JSON.stringify({
    schema_version: 'threadforge.project-memory.v2',
    id: 'claude-local-smoke-guidance',
    project_id: 'cross-harness-smoke',
    kind: 'fact',
    title: 'Local credential verification',
    content: { guidance: claudeGuidance },
    evidence: [{
      origin: 'document',
      content_sha256: createHash('sha256').update(claudeSourceBytes).digest('hex'),
      redacted: true,
      source_files: sourceReceipt.sources,
    }],
  })}\n`, { flag: 'wx', mode: 0o600 });
  const publishedMemory = JSON.parse(runThreadforge([
    'memory', 'publish', '--project-root', projectRoot, '--file', candidatePath,
    '--expected-revision', '0', '--reviewer', 'smoke-reviewer',
    '--reason', 'isolated deterministic host smoke fixture', '--privacy-reviewed',
  ]));
  assert.equal(publishedMemory.id, 'claude-local-smoke-guidance');
  assert.equal(publishedMemory.status, 'approved');
  const verifiedMemory = JSON.parse(runThreadforge([
    'memory', 'read', '--project-root', projectRoot, '--project-id', 'cross-harness-smoke',
    '--kind', 'fact', '--id', 'claude-local-smoke-guidance',
  ]));
  assert.equal(verifiedMemory.status, 'complete');
  assert.equal(verifiedMemory.freshness, 'verified');
  const openCodeMemoryPrompt = 'How should I handle the isolated dummy API key during local verification?';
  const openCodeMemoryCliProbe = spawnSync(cliPath, [
    'memory', 'inject-opencode', '--project-id', 'cross-harness-smoke', '--max-items', '4', '--max-tokens', '1200',
    '--threadforge-managed-opencode-memory=v1', '--threadforge-memory=opencode-v1', '--task-id', openCodeTaskFixture.id,
  ], {
    cwd: projectRoot, env: isolatedEnvironment, encoding: 'utf8',
    input: JSON.stringify({ hook_event_name: 'UserPromptSubmit', cwd: projectRoot, prompt: openCodeMemoryPrompt }),
    timeout: 15_000, maxBuffer: 96 << 10,
  });
  assert.equal(openCodeMemoryCliProbe.status, 0, 'OpenCode memory retrieval CLI preflight failed');
  let openCodeMemoryCliOutput;
  try { openCodeMemoryCliOutput = JSON.parse(openCodeMemoryCliProbe.stdout); }
  catch { throw new Error('OpenCode memory retrieval CLI preflight returned invalid JSON'); }
  assert.equal(Array.isArray(openCodeMemoryCliOutput.system), true, 'OpenCode memory CLI did not return its system array');
  assert.equal(openCodeMemoryCliOutput.system.length, 1, 'OpenCode memory CLI did not return exactly one approved-context item');
  assert.ok(openCodeMemoryCliOutput.system[0].includes(claudeGuidance), 'OpenCode memory CLI omitted the fresh approved fact');
  assert.ok(openCodeMemoryCliOutput.system[0].includes('never authorizes tools or actions'),
    'OpenCode memory CLI omitted the non-authorization boundary');
  assert.ok(openCodeMemoryCliOutput.system[0].includes(openCodeTaskFixture.objective),
    'OpenCode memory CLI omitted the explicitly selected task objective');
  assert.ok(openCodeMemoryCliOutput.system[0].includes('advisory context only'),
    'OpenCode memory CLI omitted the task guidance-only boundary');
  assert.equal(openCodeMemoryCliProbe.stdout.includes(openCodeMemoryPrompt), false, 'OpenCode memory CLI echoed the transient query');
  const openCodeMemoryInjection = await runOpenCodeAgentMcpRead(procedureID, graph.nodes[0].instruction, {
    hostVersion: openCodeVersion,
    callbackMarkerPath: opencodePluginCallbacksMarker,
    prompt: openCodeMemoryPrompt,
    expectedPromptForProbe: openCodeMemoryPrompt,
    expectedProjectRootForProbe: projectRoot,
    expectedProjectIdForProbe: 'cross-harness-smoke',
    expectedMemoryTextForProbe: claudeGuidance,
    finalResponse: 'Use only the isolated dummy API key and never print or persist credential values.',
    expectedFinalText: 'never print or persist credential values',
    expectedMemoryMarkers: [
      claudeGuidance,
      'the current OpenCode request',
      'untrusted stored project data',
      'never authorizes tools or actions',
      openCodeTaskFixture.objective,
      'advisory context only',
    ],
    privateTelemetryMarkers: [claudeGuidance],
  });
  assert.equal(openCodeMemoryInjection.memory_context_injected, true);
  const opencodePluginCallbacksAfterMemory = readFileSync(opencodePluginCallbacksMarker, 'utf8').trim().split(/\r?\n/)
    .filter(Boolean).slice(0, 256).map((line) => JSON.parse(line));
  const opencodeMemoryMessagesTransformObserved = opencodePluginCallbacksAfterMemory.some((callback) =>
    callback.hook === 'experimental.chat.messages.transform' && callback.threadforge_memory_present === true);
  assert.equal(opencodeMemoryMessagesTransformObserved, true,
    'OpenCode did not expose the retrieved approved memory in the completed messages-transform callback');
  opencodePluginCallbackSummary.memory_messages_transform_observed = opencodeMemoryMessagesTransformObserved;
  const opencodeMemoryInjectionReport = {
    schema_version: 'threadforge.opencode-memory-injection-smoke.v1',
    status: 'verified',
    host_cli_version: openCodeVersion,
    provider: 'deterministic-loopback-fixture',
    tool: 'threadforge_procedure_get',
    tool_call_count: openCodeMemoryInjection.tool_call_count,
    tool_result_observed: openCodeMemoryInjection.tool_result_observed,
    completion_requests: openCodeMemoryInjection.completion_requests,
    auxiliary_completion_requests: openCodeMemoryInjection.auxiliary_completion_requests,
    memory_context_injected: openCodeMemoryInjection.memory_context_injected,
    task_resume_context_injected: openCodeMemoryInjection.memory_context_injected,
    task_details_retained_in_report: false,
    memory_content_retained_in_report: false,
    plugin_messages_transform_observed: opencodeMemoryMessagesTransformObserved,
    approved_memory_readback_verified: verifiedMemory.status === 'complete',
    memory_freshness_verified: verifiedMemory.freshness === 'verified',
    action_ready: false,
    guidance_only: true,
    execution_authorized: false,
    real_model_invocations: 0,
  };
  if (process.env.THREADFORGE_OPENCODE_MEMORY_SMOKE_ONLY === '1') {
    console.log(JSON.stringify(opencodeMemoryInjectionReport));
    return;
  }
  const claudeTaskResumeMarkers = [
    openCodeTaskFixture.objective,
    'requirements are not finalized',
    'This is advisory context only',
    'does not authorize commands, tools, or execution',
    'does not override current user instructions',
  ];
  const claudeVersion = runHost('claude', ['--version']).trim();
  const claudeMemoryCommand = `${JSON.stringify(cliPath)} memory inject --project-id cross-harness-smoke --threadforge-managed-claude-memory=v1 --threadforge-memory=claude-v1 --task-id ${openCodeTaskFixture.id}`;
  assert.equal(await installClaudeProjectMemoryHook(projectRoot, claudeMemoryCommand), 'installed');
  const claudeHookProbe = spawnSync(cliPath, [
    'memory', 'inject', '--project-id', 'cross-harness-smoke',
    '--threadforge-managed-claude-memory=v1', '--threadforge-memory=claude-v1',
    '--task-id', openCodeTaskFixture.id,
  ], {
    cwd: projectRoot, env: isolatedEnvironment, encoding: 'utf8',
    input: JSON.stringify({ hook_event_name: 'UserPromptSubmit', cwd: projectRoot, prompt: 'Read the selected task context.' }),
    timeout: 15_000, maxBuffer: 4 << 20,
  });
  assert.equal(claudeHookProbe.status, 0, 'Claude task-resume hook CLI preflight failed');
  let claudeHookProbeOutput;
  try { claudeHookProbeOutput = JSON.parse(claudeHookProbe.stdout); }
  catch { throw new Error('Claude task-resume hook CLI preflight returned invalid JSON'); }
  const claudeHookContext = claudeHookProbeOutput?.hookSpecificOutput?.additionalContext;
  assert.equal(typeof claudeHookContext, 'string', 'Claude task-resume hook did not return additionalContext');
  const missingClaudeTaskMarkers = claudeTaskResumeMarkers.filter((marker) => !claudeHookContext.includes(marker));
  assert.deepEqual(missingClaudeTaskMarkers, [],
    `Claude task-resume hook CLI preflight omitted expected context markers; skip_code=${/task resume context skipped: ([a-z_]+)/i.exec(claudeHookProbe.stderr)?.[1] ?? 'none'}`);
  const {
    task_resume: claudeAgentTaskResume,
    ...claudeAgentMemoryRead
  } = await runClaudePromptMemoryRead({
    projectRoot,
    settingsPath: path.join(projectRoot, '.claude', 'settings.json'),
    claudeConfigDir,
    threadforgeHome,
    isolatedEnvironment,
    expectedGuidance: claudeGuidance,
    hostVersion: claudeVersion,
    expectedTaskResumeMarkers: claudeTaskResumeMarkers,
    finalResponse: 'The selected task is available as advisory context; requirements are not finalized and execution is not authorized.',
  });

  const codexPromptInputs = runHost('codex', ['debug', 'prompt-input']);
  const geminiDiscovery = tryGeminiSkillDiscovery();
  const commandCodeVersion = runHost('cmdc', ['--no-auto-update', '--version']).trim();
  runHost('claude', ['doctor']);
  assert.ok(openCodeSkills.includes(skillName), 'OpenCode did not discover the exported approved procedure');
  assert.ok(codexPromptInputs.includes(skillName), 'Codex CLI prompt inputs did not include the exported approved procedure');
  if (geminiDiscovery.discovered) {
    assert.ok(geminiDiscovery.output.includes(skillName), 'Gemini CLI did not discover the exported approved procedure');
  }

  console.log(JSON.stringify({
    schema_version: 'threadforge.installed-skill-smoke.v36',
    source_event_fixture: 'opencode-plugin-payload',
    captured_events: capturedEvents.length,
    capture_content_retained: false,
    draft_and_validation_fixture: true,
    validation_is_acceptance_evidence: false,
    real_model_invocations: 0,
    procedure_id: procedureID,
    procedure_revision: exportResult.graph_revision,
    project_skill_path: installations.opencode.path,
    sha256_verified: true,
    installations,
    qoder_command: {
      action: qoderCommand.action,
      path: qoderCommand.path,
      source_sha256_verified: qoderCommand.source_sha256 === exportResult.sha256,
      command_sha256_verified: createHash('sha256').update(qoderCommandBytes).digest('hex') === qoderCommand.command_sha256,
      host_discovery_verified: false,
    },
    opencode_plugin: {
      action: opencodePluginAction,
      path: '.opencode/plugins/threadforge.ts',
      session_list_completed: opencodePluginRuntime.session_list_completed,
      host_loaded_and_invoked_plugin: opencodePluginRuntime.plugin_invoked,
      callback_contract: opencodePluginCallbackSummary,
    },
    opencode_read_only_plan_agent: openCodeReadOnlyPlanAgent,
    opencode_agent_mcp_read: openCodeAgentMcpRead,
    opencode_agent_stale_mcp_read: openCodeAgentStaleMcpRead,
    opencode_procedure_freshness: openCodeProcedureFreshness,
    opencode_agent_task_resume: openCodeAgentTaskResume,
    opencode_memory_injection: opencodeMemoryInjectionReport,
    opencode_agent_task_handoff_read: openCodeAgentTaskHandoffRead,
    codex_agent_mcp_read: codexAgentMcpRead,
    codex_agent_task_resume: codexAgentTaskResume,
    codex_agent_task_handoff_read: codexAgentTaskHandoffRead,
    task_handoff_cross_harness: taskHandoffCrossHarnessSmoke,
    codex_agent_task_plan_gate: codexPlanGateSmoke,
    claude_agent_task_resume: claudeAgentTaskResume,
    gemini_cli_telemetry_smoke: geminiCliTelemetrySmoke,
    gemini_agent_task_resume: geminiAgentTaskResume,
    gemini_cli_plan_gate: geminiCliPlanGate,
    claude_agent_memory_read: claudeAgentMemoryRead,
    antigravity_project_mcp_list_probe: {
      status: antigravityMcpListed ? 'listed' : 'not-listed',
      version: antigravityVersion, project_config: '.agents/mcp_config.json',
      project_config_written_and_read_back: true,
      mcp_list_help_flags: antigravityMcpListHelpFlags,
      cli_list_detected_project_server: antigravityMcpListed,
      agent_run: antigravityAgentMcpRead,
      task_resume: antigravityAgentTaskResume,
      plan_gate: antigravityPlanGateSmoke,
      real_model_invocations: 0,
    },
    command_code_project_mcp_readiness: {
      status: 'verified', version: commandCodeVersion, config: '.mcp.json',
      mcp_list_detected_project_server: true,
      project_skill_list_detected_installed_skill: commandCodeSkillDiscovered,
      agent_run: commandCodeAgentMcpRead,
      real_model_invocations: 0,
    },
    github_copilot_project_mcp: copilotProjectMcpDiscovery,
    github_copilot_task_resume: copilotTaskResumeSmoke,
    github_copilot_plan_gate: copilotPlanGateSmoke,
    diagnostics: {
      claude_code: { version: claudeVersion, doctor_completed: true },
    },
    discovery: {
      opencode: { version: openCodeVersion, skill_discovered: true, threadforge_mcp_connected: true },
      gemini_cli: { version: geminiVersion, ...geminiDiscovery, output: undefined },
      codex_cli: { version: codexVersion, discovered: true, method: 'debug prompt-input' },
    },
  }));
} catch (error) {
  smokeFailure = error;
  throw error;
} finally {
  try {
    await removeIsolatedHomeWithRetry();
  } catch (cleanupError) {
    if (smokeFailure) {
      throw new AggregateError([smokeFailure, cleanupError], 'Installed-host smoke failed and its isolated home could not be fully removed');
    }
    throw cleanupError;
  }
}
}

await main();

function runOpenCodePluginLoadCheck(markerPath) {
  // The first run can include OpenCode's TypeScript plugin compilation on a cold cache.
  const output = run('opencode', ['session', 'list', '--format', 'json', '--print-logs'], {
    cwd: projectRoot, env: isolatedEnvironment, timeout: 240_000,
  });
  if (!existsSync(markerPath)) {
    throw new Error(`OpenCode session inventory did not load and invoke the project Threadforge plugin: ${output.slice(-2048)}`);
  }
  assert.equal(readFileSync(markerPath, 'utf8'), 'threadforge-opencode-plugin-invoked-v1\n');
  return { session_list_completed: true, plugin_invoked: true };
}

function runOpenCodePlanAgentCheck(hostVersion) {
  const output = runHost('opencode', ['debug', 'agent', opencodePlanAgentName, '--pure'], {
    cwd: projectRoot, env: isolatedEnvironment,
  });
  let agent;
  try { agent = JSON.parse(output); }
  catch { throw new Error('OpenCode debug agent did not return its resolved profile as JSON'); }
  assert.equal(agent.name, opencodePlanAgentName, 'OpenCode did not discover the project Threadforge planning agent');
  assert.equal(agent.mode, 'primary', 'Threadforge planning agent is not a primary agent');
  assert.ok(Array.isArray(agent.permission), 'OpenCode debug output omitted resolved agent permissions');

  const permissions = agent.permission;
  let denyAllIndex = -1;
  for (let index = 0; index < permissions.length; index++) {
    const rule = permissions[index];
    if (rule?.permission === '*' && rule?.pattern === '*' && rule?.action === 'deny') denyAllIndex = index;
  }
  assert.notEqual(denyAllIndex, -1, 'OpenCode did not resolve the explicit deny-all agent rule');
  const allWildcardIndexes = permissions.flatMap((rule, index) =>
    rule?.permission === '*' && rule?.pattern === '*' ? [index] : []);
  assert.equal(denyAllIndex, allWildcardIndexes.at(-1), 'a later wildcard permission overrides the agent deny-all rule');

  const postDenyAllows = permissions.slice(denyAllIndex + 1)
    .filter((rule) => rule?.action === 'allow' && rule?.permission !== 'external_directory')
    .map((rule) => rule.permission);
  const allowedPermissions = [...new Set(postDenyAllows)].sort();
  assert.deepEqual(allowedPermissions, ['glob', 'grep', 'read'],
    'OpenCode resolved permissions beyond read/search after the deny-all rule');
  const mutatingOrExternalAllows = new Set(['bash', 'edit', 'write', 'patch', 'task', 'webfetch', 'websearch', 'skill', 'todowrite']);
  assert.equal(permissions.slice(denyAllIndex + 1).some((rule) =>
    rule?.action === 'allow' && mutatingOrExternalAllows.has(rule.permission)), false,
  'OpenCode resolved an execution or mutation permission after the deny-all rule');
  return {
    schema_version: 'threadforge.opencode-read-only-plan-agent-smoke.v1',
    status: 'verified',
    host_cli_version: hostVersion,
    agent_name: opencodePlanAgentName,
    agent_mode: 'primary',
    project_agent_discovered: true,
    deny_all_permission_verified: true,
    read_search_allowlist_verified: true,
    execution_or_mutation_permissions_allowlisted: false,
    threadforge_task_state_bound: false,
    action_ready: false,
    execution_authorized: false,
    real_model_invocations: 0,
  };
}

async function runOpenCodeAgentMcpRead(procedureID, expectedGuidance, options = {}) {
  const {
    hostVersion = 'unknown',
    toolSuffix = 'threadforge_procedure_get',
    toolArguments = { procedure_id: procedureID },
    prompt = `Use the Threadforge MCP tool to read approved procedure ${procedureID}, then repeat its first guidance sentence exactly.`,
    finalResponse = expectedGuidance,
    expectedFinalText = expectedGuidance,
    requiredResultMarkers = [],
    forbiddenResultMarkers = [],
    privateTelemetryMarkers = [],
    expectedMemoryMarkers = [],
    callbackMarkerPath,
    expectedPromptForProbe,
    expectedProjectRootForProbe,
    expectedProjectIdForProbe,
    expectedMemoryTextForProbe,
  } = options;
  const providerID = 'threadforge-fixture';
  const modelID = 'fixture-model';
  const model = `${providerID}/${modelID}`;
  const configPath = path.join(projectRoot, 'opencode.json');
  const config = JSON.parse(readFileSync(configPath, 'utf8'));
  let completionRequests = 0;
  let auxiliaryRequests = 0;
  let fixtureToolCalls = 0;
  let toolResultObserved = false;
  let memoryContextInjected = false;
  let observedToolResultText = '';
  let fixtureError;
  const pluginEvents = [];
  const providerRequestPaths = [];
  const providerShapes = [];
  const server = createServer((request, response) => {
    providerRequestPaths.push(new URL(request.url ?? '/', 'http://127.0.0.1').pathname);
    const chunks = [];
    let requestBytes = 0;
    request.on('data', (chunk) => {
      requestBytes += chunk.length;
      if (requestBytes <= 8 << 20) chunks.push(chunk);
    });
    request.on('end', () => {
      try {
        if (requestBytes > 8 << 20) throw new Error('fixture request exceeded its local size bound');
        if (request.method === 'POST' && new URL(request.url ?? '/', 'http://127.0.0.1').pathname === '/v1/events') {
          const payload = JSON.parse(Buffer.concat(chunks).toString('utf8'));
          const events = Array.isArray(payload) ? payload : [payload];
          if (events.length < 1 || events.length > 200) throw new Error('OpenCode plugin event batch exceeded its local bound');
          for (const event of events) {
            if (event?.vendor !== 'beacon' || event?.product !== 'endpoint-agent' || event?.harness?.name !== 'opencode' ||
                typeof event?.event?.action !== 'string' || (event?.content !== undefined && event.content.included !== false)) {
              throw new Error('OpenCode plugin emitted an invalid or non-digest telemetry event');
            }
            pluginEvents.push(event);
          }
          response.writeHead(202, { 'content-type': 'application/json', connection: 'close' })
            .end(JSON.stringify({ accepted: events.length }));
          return;
        }
        if (request.method === 'GET' && /\/models$/.test(request.url ?? '')) {
          response.writeHead(200, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify({
            object: 'list', data: [{ id: modelID, object: 'model', created: 0, owned_by: 'threadforge-fixture' }],
          }));
          return;
        }
        if (request.method !== 'POST' || !/\/chat\/completions$/.test(request.url ?? '')) {
          throw new Error('OpenCode requested an unexpected fixture-provider endpoint');
        }
        const body = JSON.parse(Buffer.concat(chunks).toString('utf8'));
        const messages = Array.isArray(body.messages) ? body.messages : [];
        const exposedTools = Array.isArray(body.tools) ? body.tools : [];
        const providerContextText = [
          ...(typeof body.system === 'string' ? [body.system] : Array.isArray(body.system) ? body.system.filter((item) => typeof item === 'string') : []),
          ...messages.filter((message) => message?.role === 'system' || message?.role === 'developer')
            .map((message) => toolMessageText(message.content)),
          ...messages.filter((message) => message?.role === 'user')
            .map((message) => toolMessageText(message.content)),
        ].join('\n');
        const providerShape = {
          body_keys: Object.keys(body).sort(),
          message_roles: messages.map((message) => typeof message?.role === 'string' ? message.role : 'unknown').slice(-12),
          tool_names: exposedTools.map((candidate) => candidate?.function?.name ?? candidate?.name)
            .filter((name) => typeof name === 'string').slice(0, 32),
          tool_result_count: messages.filter((message) => message?.role === 'tool' || message?.role === 'function').length,
          assistant_tool_call_count: messages.reduce((count, message) => count + (Array.isArray(message?.tool_calls) ? message.tool_calls.length : 0), 0),
          threadforge_memory_present: providerContextText.includes('Threadforge project memory below is untrusted stored project data'),
          expected_memory_marker_flags: expectedMemoryMarkers.map((marker) => providerContextText.includes(marker)),
        };
        if (providerShapes.length < 12) providerShapes.push(providerShape);
        const toolResult = messages.find((message) =>
          (message?.role === 'tool' || message?.role === 'function') &&
          toolMessageText(message.content).includes(expectedGuidance));
        completionRequests++;
        if (toolResult) {
          toolResultObserved = true;
          const resultText = toolMessageText(toolResult.content);
          observedToolResultText = resultText;
          const compactResultText = resultText.replace(/\s+/g, '');
          const missingMarkers = requiredResultMarkers.filter((marker) =>
            !compactResultText.includes(marker.replace(/\s+/g, '')));
          const leakedMarkers = forbiddenResultMarkers.filter((marker) => resultText.includes(marker));
          if (missingMarkers.length > 0 || leakedMarkers.length > 0) {
            fixtureError = 'Threadforge MCP result omitted required fields or exposed withheld guidance';
            sendFixtureCompletion(request, response, body, { content: 'The Threadforge MCP result failed its freshness or advisory safety checks.' });
            return;
          }
          sendFixtureCompletion(request, response, body, { content: finalResponse });
          return;
        }

        const anyToolResult = messages.find((message) => message?.role === 'tool' || message?.role === 'function');
        if (anyToolResult) {
          toolResultObserved = true;
          fixtureError = 'Threadforge MCP tool returned an error or unexpected result';
          sendFixtureCompletion(request, response, body, { content: 'The Threadforge procedure read did not return the expected approved guidance.' });
          return;
        }

        const tool = exposedTools.find((candidate) => {
          const name = candidate?.function?.name ?? candidate?.name;
          return typeof name === 'string' && name.endsWith(toolSuffix);
        });
        const toolName = tool?.function?.name ?? tool?.name;
        if (typeof toolName !== 'string') {
          if (exposedTools.length === 0 && fixtureToolCalls === 0) {
            auxiliaryRequests++;
            sendFixtureCompletion(request, response, body, { content: 'Threadforge procedure verification' });
            return;
          }
          throw new Error(`OpenCode did not expose ${toolSuffix}`);
        }
        if (expectedMemoryMarkers.length > 0 && !memoryContextInjected) {
          const missingMemoryMarkers = expectedMemoryMarkers.filter((marker) => !providerContextText.includes(marker));
          memoryContextInjected = missingMemoryMarkers.length === 0;
        }
        if (fixtureToolCalls >= 1) {
          fixtureError = `OpenCode attempted to repeat ${toolSuffix}`;
          sendFixtureCompletion(request, response, body, { content: 'The Threadforge MCP read was already attempted once.' });
          return;
        }
        fixtureToolCalls++;
        sendFixtureCompletion(request, response, body, {
          toolCall: {
            id: `call_${toolSuffix}`, name: toolName,
            arguments: JSON.stringify(toolArguments),
          },
        });
      } catch (error) {
        fixtureError ??= String(error?.message ?? 'local deterministic provider failed').slice(0, 256);
        response.writeHead(500, { 'content-type': 'application/json', connection: 'close' })
          .end(JSON.stringify({ error: { message: 'deterministic local provider fixture failed' } }));
      }
    });
  });

  await new Promise((resolve, reject) => {
    server.once('error', reject);
    server.listen(0, '127.0.0.1', resolve);
  });
  const address = server.address();
  assert(address && typeof address === 'object');
  const endpoint = `http://127.0.0.1:${address.port}/v1`;
  config.model = model;
  config.provider = {
    [providerID]: {
      npm: '@ai-sdk/openai-compatible',
      name: 'Threadforge deterministic local smoke provider',
      options: { baseURL: endpoint, apiKey: 'threadforge-fixture-only' },
      models: {
        [modelID]: {
          name: 'Deterministic tool-call fixture',
          limit: { context: 16_384, output: 1024 },
        },
      },
    },
  };
  writeFileSync(configPath, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });

  try {
    const openCodeEnvironment = {
      ...isolatedEnvironment,
      THREADFORGE_CLI: cliPath,
      THREADFORGE_EVENT_ENDPOINT: `${endpoint}/events`,
      ...(typeof expectedPromptForProbe === 'string' ? {
        THREADFORGE_OPENCODE_EXPECTED_PROMPT_TEXT: expectedPromptForProbe,
      } : {}),
      ...(typeof expectedProjectRootForProbe === 'string' ? {
        THREADFORGE_OPENCODE_EXPECTED_PROJECT_ROOT: expectedProjectRootForProbe,
      } : {}),
      ...(typeof expectedProjectIdForProbe === 'string' ? {
        THREADFORGE_OPENCODE_EXPECTED_PROJECT_ID: expectedProjectIdForProbe,
      } : {}),
      ...(typeof expectedMemoryTextForProbe === 'string' ? {
        THREADFORGE_OPENCODE_EXPECTED_MEMORY_TEXT: expectedMemoryTextForProbe,
      } : {}),
    };
    let output;
    try {
      output = await runAsync('opencode', [
        'run', '--model', model, '--dir', projectRoot, '--auto', '--format', 'default', prompt,
      ], { cwd: projectRoot, env: openCodeEnvironment, timeout: 45_000 });
    } catch (error) {
      throw new Error(`${error?.message ?? 'OpenCode run failed'}; fixture_paths=${providerRequestPaths.join(',') || 'none'}; completion_requests=${completionRequests}; tool_calls=${fixtureToolCalls}; fixture_error=${fixtureError ?? 'none'}`);
    }
    if (fixtureError) throw new Error(`OpenCode MCP-read fixture failed: ${fixtureError}; provider_shapes=${JSON.stringify(providerShapes)}`);
    assert.equal(completionRequests, auxiliaryRequests + 2, 'expected only auxiliary local preflight requests plus one tool request and one final response');
    assert.equal(fixtureToolCalls, 1, 'expected exactly one Threadforge procedure tool call');
    assert.equal(toolResultObserved, true, 'provider did not receive the MCP procedure result before final response');
    if (expectedMemoryMarkers.length > 0 && !memoryContextInjected) {
      const callbacks = readFileSync(callbackMarkerPath, 'utf8').trim().split(/\r?\n/)
        .filter(Boolean).slice(0, 256).map((line) => JSON.parse(line));
      const transforms = callbacks.filter((callback) => callback.hook === 'experimental.chat.messages.transform');
      const userMessages = callbacks.filter((callback) => callback.hook === 'chat.message' && callback.role === 'user');
      const diagnostic = {
        messages_transform_calls: transforms.length,
        transform_memory_flags: transforms.map((callback) => callback.threadforge_memory_present === true),
        transformed_user_text_bytes: transforms.map((callback) => callback.user_text_bytes),
        expected_prompt_in_transformed_user_text: transforms.map((callback) => callback.expected_prompt_in_text === true),
        expected_prompt_within_retrieval_window: transforms.map((callback) => callback.matches_expected_prompt === true),
        worktree_matches_expected: transforms.map((callback) => callback.worktree_matches_expected === true),
        directory_matches_expected: transforms.map((callback) => callback.directory_matches_expected === true),
        effective_root_matches_expected: transforms.map((callback) => callback.effective_root_matches_expected === true),
        project_id_matches_expected: transforms.map((callback) => callback.project_id_matches === true),
        expected_approved_memory_text_present: transforms.map((callback) => callback.expected_memory_text_present === true),
        user_message_callbacks: userMessages.length,
        user_message_session_present: userMessages.filter((callback) => callback.session_present === true).length,
        transformed_user_message_matches_expected_prompt: transforms.some((callback) => callback.matches_expected_prompt === true),
        callback_order: callbacks.map((callback) => ({
          sequence: callback.callback_index, hook: callback.hook, role: callback.role,
          threadforge_memory_present: callback.threadforge_memory_present,
        })),
        provider_memory_flags: providerShapes.map((shape) => shape.threadforge_memory_present),
        provider_expected_memory_marker_flags: providerShapes.map((shape) => shape.expected_memory_marker_flags),
      };
      throw new Error(`OpenCode installed prompt memory injection was not observed; diagnostics=${JSON.stringify(diagnostic)}`);
    }
    assert.ok(output.includes(expectedFinalText), `OpenCode final output did not contain the expected ${toolSuffix} summary`);
    const actions = pluginEvents.map((event) => event.event.action);
    assert.ok(actions.includes('prompt.submitted'), 'OpenCode did not send the installed plugin user-prompt event to the local intake');
    assert.ok(actions.includes('tool.invoked') && actions.includes('tool.completed'),
      `OpenCode did not send tool lifecycle events to the local intake; received actions=${actions.join(',') || 'none'}`);
    assert.ok(actions.includes('assistant.message.completed'),
      `OpenCode did not send the completed assistant-message event to the local intake; received actions=${actions.join(',') || 'none'}`);
    const serializedPluginEvents = JSON.stringify(pluginEvents);
    for (const privateValue of [prompt, expectedGuidance, procedureID, 'threadforge-fixture-only', ...forbiddenResultMarkers, ...privateTelemetryMarkers]) {
      assert.equal(serializedPluginEvents.includes(privateValue), false, `OpenCode plugin telemetry retained ${privateValue}`);
    }
    if (toolSuffix === 'threadforge_task_resume_context' || toolSuffix === 'threadforge_task_handoffs') {
      assert.equal(serializedPluginEvents.includes(toolArguments.task_id), false,
        'OpenCode plugin telemetry retained the raw continuity task identifier');
    }
    if (requiredResultMarkers.length > 0) {
      const compactResultText = observedToolResultText.replace(/\s+/g, '');
      assert.equal(requiredResultMarkers.every((marker) => compactResultText.includes(marker.replace(/\s+/g, ''))), true,
        'OpenCode task-resume MCP result omitted advisory fields');
    }
    return {
      ...(toolSuffix === 'threadforge_task_resume_context' ? {
        schema_version: 'threadforge.opencode-task-resume-smoke.v1',
        task_context_content_retained_in_report: false,
        result_advisory_markers_verified: requiredResultMarkers.length > 0,
        requirements_not_finalized_warning_verified: requiredResultMarkers.includes('requirements_not_finalized'),
        action_ready: false, guidance_only: true, execution_authorized: false,
      } : {}),
      ...(toolSuffix === 'threadforge_task_handoffs' ? {
        handoff_page_contract_markers_verified: requiredResultMarkers.length > 0,
        private_identifiers_absent_from_telemetry: true,
      } : {}),
      status: 'verified', host_cli_version: hostVersion,
      provider: 'deterministic-loopback-fixture',
      tool: toolSuffix, tool_call_count: fixtureToolCalls,
      tool_result_observed: toolResultObserved, completion_requests: completionRequests,
      auxiliary_completion_requests: auxiliaryRequests,
      ...(toolSuffix === 'threadforge_procedure_get' ? {
        required_result_markers_verified: requiredResultMarkers.length > 0,
        forbidden_result_markers_absent: forbiddenResultMarkers.length > 0,
      } : {}),
      ...(expectedMemoryMarkers.length > 0 ? {
        memory_context_injected: memoryContextInjected,
        system_context_content_retained_in_report: false,
      } : {}),
      plugin_event_delivery: {
        status: 'verified', event_count: pluginEvents.length, actions: [...new Set(actions)].sort(),
        provider: 'loopback-only', prompt_text_retained: false, execution_authorized: false,
      },
      real_model_invocations: 0,
    };
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
}

function toolMessageText(content) {
  if (typeof content === 'string') return content;
  if (Array.isArray(content)) {
    return content.map((part) => typeof part?.text === 'string' ? part.text : JSON.stringify(part)).join('\n');
  }
  try { return JSON.stringify(content ?? ''); }
  catch { return ''; }
}

function sendFixtureCompletion(request, response, body, result) {
  const model = typeof body.model === 'string' ? body.model : 'fixture-model';
  const completionID = `chatcmpl-threadforge-${Date.now()}`;
  if (body.stream === true) {
    response.writeHead(200, {
      'content-type': 'text/event-stream; charset=utf-8',
      'cache-control': 'no-cache',
      connection: 'close',
    });
    const writeChunk = (delta, finishReason = null) => response.write(`data: ${JSON.stringify({
      id: completionID, object: 'chat.completion.chunk', created: Math.floor(Date.now() / 1000), model,
      choices: [{ index: 0, delta, finish_reason: finishReason }],
    })}\n\n`);
    if (result.toolCall) {
      writeChunk({ role: 'assistant' });
      writeChunk({ tool_calls: [{
        index: 0, id: result.toolCall.id, type: 'function',
        function: { name: result.toolCall.name, arguments: result.toolCall.arguments },
      }] });
      writeChunk({}, 'tool_calls');
    } else {
      writeChunk({ role: 'assistant', content: result.content });
      writeChunk({}, 'stop');
    }
    response.end('data: [DONE]\n\n');
    return;
  }
  response.writeHead(200, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify({
    id: completionID, object: 'chat.completion', created: Math.floor(Date.now() / 1000), model,
    choices: [{
      index: 0,
      message: result.toolCall
        ? { role: 'assistant', content: null, tool_calls: [{
            id: result.toolCall.id, type: 'function',
            function: { name: result.toolCall.name, arguments: result.toolCall.arguments },
          }] }
        : { role: 'assistant', content: result.content },
      finish_reason: result.toolCall ? 'tool_calls' : 'stop',
    }],
    usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
  }));
}

function runAsync(command, args, { cwd = repositoryRoot, env = process.env, timeout = 120_000, includeStderr = false, stdin } = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, { cwd, env, stdio: [stdin === undefined ? 'ignore' : 'pipe', 'pipe', 'pipe'] });
    const stdout = [];
    const stderr = [];
    let outputBytes = 0;
    let errorBytes = 0;
    let settled = false;
    const fail = (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(error);
    };
    if (stdin !== undefined) child.stdin.end(stdin);
    const timer = setTimeout(() => {
      child.kill();
      const detail = Buffer.concat(stderr).toString('utf8').slice(-1024).trim();
      fail(new Error(`${path.basename(command)} exceeded the ${timeout} ms fixture timeout${detail ? `: ${detail}` : ''}`));
    }, timeout);
    child.once('error', fail);
    child.stdout.on('data', (chunk) => {
      outputBytes += chunk.length;
      if (outputBytes > 4 << 20) {
        child.kill();
        fail(new Error(`${path.basename(command)} fixture output exceeded its local bound`));
      } else stdout.push(chunk);
    });
    child.stderr.on('data', (chunk) => {
      if (errorBytes < 2 << 20) { stderr.push(chunk); errorBytes += chunk.length; }
    });
    child.once('close', (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      const out = Buffer.concat(stdout).toString('utf8');
      const err = Buffer.concat(stderr).toString('utf8');
      if (code !== 0) reject(new Error(`${path.basename(command)} exited ${code}: ${(err || out).trim().slice(-2048)}`));
      else resolve(includeStderr ? `${out}${err}` : out);
    });
  });
}


