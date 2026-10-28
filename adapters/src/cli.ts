import path from 'node:path';
import { realpath } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';
import {
  installClaudeProjectHooks,
  installClaudeProjectMemoryHook,
  installClaudeProjectPlanGateHook,
  removeClaudeProjectHooks,
  removeClaudeProjectMemoryHook,
  removeClaudeProjectPlanGateHook,
} from './claude-hooks-config.ts';
import { installOpenCodeProjectPlugin, removeOpenCodeProjectPlugin } from './opencode-plugin-config.ts';
import { installOpenCodeProjectPlanAgent, removeOpenCodeProjectPlanAgent } from './opencode-plan-agent-config.ts';
import {
  installCursorProjectHooks,
  installCursorProjectMemoryHook,
  installCursorProjectPlanGateHook,
  removeCursorProjectHooks,
  removeCursorProjectMemoryHook,
  removeCursorProjectPlanGateHook,
} from './cursor-hooks-config.ts';
import {
  installCodexProjectHooks,
  installCodexProjectMemoryHook,
  installCodexProjectPlanGateHook,
  removeCodexProjectHooks,
  removeCodexProjectMemoryHook,
  removeCodexProjectPlanGateHook,
} from './codex-hooks-config.ts';
import {
  installAntigravityHooks,
  installAntigravityProjectMemoryHook,
  installAntigravityProjectPlanGateHook,
  removeAntigravityHooks,
  removeAntigravityProjectMemoryHook,
  removeAntigravityProjectPlanGateHook,
  type AntigravityHookScope,
} from './antigravity-hooks-config.ts';
import {
  installQwenHooks,
  installQwenProjectMemoryHook,
  installQwenProjectPlanGateHook,
  removeQwenHooks,
  removeQwenProjectMemoryHook,
  removeQwenProjectPlanGateHook,
  type QwenHookScope,
} from './qwen-hooks-config.ts';
import { installKimiCodeHooks, removeKimiCodeHooks, updateKimiCodeHooks } from './kimi-code-hooks-config.ts';
import { installKiroHooks, removeKiroHooks, updateKiroHooks, type KiroHookScope } from './kiro-hooks-config.ts';
import { installMuseHooks, removeMuseHooks, updateMuseHooks, type MuseHookScope } from './muse-hooks-config.ts';
import { installOmpExtension, removeOmpExtension, updateOmpExtension, type OmpHookScope } from './omp-hooks-config.ts';
import { buildOpenClawPluginCommands, runOpenClawPluginAction, type OpenClawPluginAction } from './openclaw-plugin-install.ts';
import { getOpenClawOtelStatus, parseOpenClawDuration, renderOpenClawOtelConfig, validateOpenClawOtelStatus } from './openclaw-otel.ts';
import { installGeminiTelemetry, removeGeminiTelemetry } from './gemini-telemetry-config.ts';
import {
  installGeminiProjectMemoryHook,
  installGeminiProjectPlanGateHook,
  removeGeminiProjectMemoryHook,
  removeGeminiProjectPlanGateHook,
} from './gemini-memory-config.ts';
import { installClaudeTelemetry, removeClaudeTelemetry } from './claude-telemetry-config.ts';
import { installCodexTelemetry, removeCodexTelemetry } from './codex-telemetry-config.ts';
import { getCodexRolloutStatus, syncCodexRollouts } from './codex-rollouts.ts';
import { getOpenClawHistoryStatus, syncOpenClawHistory } from './openclaw-history.ts';
import { getOpenCodeHistoryStatus, syncOpenCodeHistory } from './opencode-history.ts';
import { getClaudeHistoryStatus, syncClaudeHistory } from './claude-history.ts';
import { getCopilotHistoryStatus, syncCopilotHistory } from './copilot-history.ts';
import {
  installCopilotProjectMemoryHook,
  installCopilotProjectPlanGateHook,
  removeCopilotProjectMemoryHook,
  removeCopilotProjectPlanGateHook,
} from './copilot-hooks-config.ts';
import { getCursorHistoryStatus, syncCursorHistory } from './cursor-history.ts';
import { getFactoryHistoryStatus, syncFactoryHistory } from './factory-history.ts';
import { getFxHistoryStatus, syncFxHistory } from './fx-history.ts';
import { getPiHistoryStatus, syncPiHistory } from './pi-history.ts';
import { getPrimeHistoryStatus, syncPrimeHistory } from './prime-history.ts';
import { installOpenHandsProjectHooks, openHandsManagedMarker, removeOpenHandsProjectHooks } from './openhands-hooks-config.ts';
import { runOpenHandsHook } from './openhands-hook.ts';
import {
  getOpenHandsUserMcpServerStatus,
  installOpenHandsUserMcpServer,
  uninstallOpenHandsUserMcpServer,
  updateOpenHandsUserMcpServer,
} from './openhands-mcp-config.ts';
import { devinHookEvents, devinManagedMarker, runDevinHook } from './devin-hook.ts';
import { installDevinHooks, removeDevinHooks, type DevinHookScope } from './devin-hooks-config.ts';
import { devinDesktopHookEvents, devinDesktopManagedMarker, runDevinDesktopHook } from './devin-desktop-hook.ts';
import { installDevinDesktopHooks, removeDevinDesktopHooks, type DevinDesktopHookScope } from './devin-desktop-hooks-config.ts';
import { getSenpiExtensionState, installSenpiExtension, removeSenpiExtension, updateSenpiExtension } from './senpi-extension-config.ts';
import { grokHookActions, grokManagedMarker, runGrokHook } from './grok-hook.ts';
import { installGrokHooks, removeGrokHooks, type GrokHookScope } from './grok-hooks-config.ts';
import { getGrokHistoryStatus, syncGrokHistory } from './grok-history.ts';
import { hermesHookEvents, hermesManagedMarker, renderHermesHookConfig, runHermesHook } from './hermes-hook.ts';
import { getHermesHistoryStatus, syncHermesHistory } from './hermes-history.ts';
import { deepseekHookEvents, deepseekManagedMarker, runDeepSeekHook } from './deepseek-hook.ts';
import { getDeepSeekHistoryStatus, syncDeepSeekHistory, watchDeepSeekHistory } from './deepseek-history.ts';
import { renderDeepSeekHarnessMcpPatch } from './dsh-mcp-patch.ts';
import { getClineHistoryStatus, syncClineHistory, watchClineHistory } from './cline-history.ts';
import { getClinePluginStatus, installClinePlugin, uninstallClinePlugin, updateClinePlugin } from './cline-plugin-config.ts';
import {
  getClineUserMcpServerStatus,
  installClineUserMcpServer,
  uninstallClineUserMcpServer,
  updateClineUserMcpServer,
} from './cline-mcp-config.ts';
import { getGooseOtelStatus, parseGooseOtelDuration, renderGooseOtelConfig, validateGooseOtelStatus } from './goose-otel.ts';
import { getGooseHistoryStatus, syncGooseHistory } from './goose-history.ts';
import { getVSCodeOtelStatus, parseVSCodeOtelDuration, renderVSCodeOtelConfig, validateVSCodeOtelStatus } from './vscode-otel.ts';
import { renderClaudeCoworkOtelGuidance } from './claude-cowork-otel.ts';
import { installProjectSkill, uninstallProjectSkill, type ProjectSkillHost } from './project-skill-config.ts';
import { installAiderProjectSkill, uninstallAiderProjectSkill } from './aider-skill-config.ts';
import { installCopilotProjectPrompt, uninstallCopilotProjectPrompt } from './copilot-prompt-config.ts';
import { installQoderProjectCommand, uninstallQoderProjectCommand } from './qoder-command-config.ts';
import {
  installCodexProjectMcpServer,
  removeCodexProjectMcpServer,
  updateCodexProjectMcpServer,
} from './codex-mcp-config.ts';
import {
  installOpenCodeProjectMcpServer,
  removeManagedOpenCodeProjectMcpServer,
  updateManagedOpenCodeProjectMcpServer,
} from './opencode-mcp-config.ts';
import {
  installProjectMcpServer,
  managedMcpArgument,
  removeManagedProjectMcpServer,
  updateManagedProjectMcpServer,
  type ProjectMcpHost,
} from './mcp-config.ts';
import { parseProjectBootstrapHosts, runProjectBootstrapMcp } from './project-bootstrap.ts';
import { parseProjectInstructionHosts, runProjectInstructionBootstrap } from './project-instructions.ts';

interface ParsedOptions {
  values: Map<string, string>;
  repeated: Map<string, string[]>;
  switches: Set<string>;
}

export async function runAdapterCLI(args: string[], stdout: NodeJS.WritableStream, stderr: NodeJS.WritableStream): Promise<number> {
  let family = args[0];
  if (family === 'prime-agent') family = 'prime';
  if (family === 'devin-cli') family = 'devin';
  if (family === 'omo-senpi' || family === 'omo_senpi') family = 'senpi';
  if (family === 'open-hands') family = 'openhands';
  if (family === 'deepseek-harness' || family === 'deepseek_harness') family = 'dsh';
  if (family === 'claude-cowork') family = 'cowork';
  let action = args[1];
  let rest = args.slice(2);
  let hermesSubsystem: 'hooks' | 'sessions' | undefined;
  let grokSubsystem: 'hooks' | 'sessions' | undefined;
  let openClawSubsystem: 'otel' | 'sessions' | undefined;
  let gooseSubsystem: 'otel' | 'sessions' | undefined;
  let vscodeSubsystem: 'otel' | undefined;
  let coworkSubsystem: 'otel' | undefined;
  let openCodeSubsystem: 'sessions' | 'plan-agent' | undefined;
  let claudeSubsystem: 'sessions' | undefined;
  let claudeCodeSubsystem: 'memory' | 'plan-gate' | undefined;
  let codexSubsystem: 'memory' | 'plan-gate' | undefined;
  let geminiSubsystem: 'memory' | 'plan-gate' | undefined;
  let qwenSubsystem: 'memory' | 'plan-gate' | undefined;
  let antigravitySubsystem: 'memory' | 'plan-gate' | undefined;
  let copilotSubsystem: 'sessions' | 'memory' | 'plan-gate' | 'prompts' | undefined;
  let cursorSubsystem: 'sessions' | 'memory' | 'plan-gate' | undefined;
  let factorySubsystem: 'sessions' | undefined;
  let fxSubsystem: 'sessions' | undefined;
  let piSubsystem: 'sessions' | undefined;
  let primeSubsystem: 'sessions' | undefined;
  let dshSubsystem: 'sessions' | 'mcp' | undefined;
  let clineSubsystem: 'sessions' | 'plugin' | 'mcp' | undefined;
  let openHandsSubsystem: 'mcp' | undefined;
  let projectSkillSubsystem: 'skills' | undefined;
  let qoderCommandSubsystem: 'commands' | undefined;
  let bootstrapSubsystem: 'mcp' | 'instructions' | undefined;
  if (!family || !action) {
    stderr.write(`${usage()}\n`);
    return 2;
  }
  if (family === 'bootstrap' && (action === 'mcp' || action === 'instructions')) {
    bootstrapSubsystem = action;
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if ((family === 'aider' || family === 'claude-code' || family === 'command-code' || family === 'cline' || family === 'cursor' || family === 'devin' || family === 'opencode' || family === 'codex' || family === 'gemini' || family === 'antigravity' || family === 'copilot' || family === 'openhands' || family === 'kimi' || family === 'zcode' || family === 'dsh' || family === 'qoder' || family === 'windsurf' || family === 'devin-desktop') && action === 'skills') {
    projectSkillSubsystem = 'skills';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'qoder' && action === 'commands') {
    qoderCommandSubsystem = 'commands';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'claude-code' && action === 'memory') {
    claudeCodeSubsystem = 'memory';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'claude-code' && action === 'plan-gate') {
    claudeCodeSubsystem = 'plan-gate';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(usage() + '\n');
      return 2;
    }
  }
  if (family === 'codex' && action === 'memory') {
    codexSubsystem = 'memory';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'codex' && action === 'plan-gate') {
    codexSubsystem = 'plan-gate';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'gemini' && action === 'memory') {
    geminiSubsystem = 'memory';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'gemini' && action === 'plan-gate') {
    geminiSubsystem = 'plan-gate';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'cursor' && action === 'memory') {
    cursorSubsystem = 'memory';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'cursor' && action === 'plan-gate') {
    cursorSubsystem = 'plan-gate';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'qwen' && action === 'memory') {
    qwenSubsystem = 'memory';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'qwen' && action === 'plan-gate') {
    qwenSubsystem = 'plan-gate';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'copilot' && action === 'plan-gate') {
    copilotSubsystem = 'plan-gate';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'copilot' && action === 'memory') {
    copilotSubsystem = 'memory';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'copilot' && action === 'prompts') {
    copilotSubsystem = 'prompts';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'antigravity' && action === 'memory') {
    antigravitySubsystem = 'memory';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'antigravity' && action === 'plan-gate') {
    antigravitySubsystem = 'plan-gate';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'cowork' && action === 'otel') {
    coworkSubsystem = 'otel';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'vscode' && action === 'otel') {
    vscodeSubsystem = 'otel';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'goose' && action === 'otel') {
    gooseSubsystem = 'otel';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'goose' && action === 'sessions') {
    gooseSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'dsh' && action === 'sessions') {
    dshSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'dsh' && action === 'mcp') {
    dshSubsystem = 'mcp';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'cline' && action === 'sessions') {
    clineSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'cline' && action === 'plugin') {
    clineSubsystem = 'plugin';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'cline' && action === 'mcp') {
    clineSubsystem = 'mcp';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'openhands' && action === 'mcp') {
    openHandsSubsystem = 'mcp';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'dsh' && action === 'hook') {
    const [eventName, marker, ...unexpected] = rest;
    if (!deepseekHookEvents.includes(eventName as typeof deepseekHookEvents[number]) ||
        marker !== deepseekManagedMarker || unexpected.length > 0) {
      stderr.write('DeepSeek Harness hook invocation is invalid or unowned\n');
      return 2;
    }
    await runDeepSeekHook(eventName!, process.stdin, stdout, stderr, {
      endpoint: process.env.THREADFORGE_EVENT_ENDPOINT,
      timeoutMs: 250,
    });
    return 0;
  }
  if (family === 'devin' && action === 'hook') {
    const [eventName, marker, ...unexpected] = rest;
    if (!devinHookEvents.includes(eventName as typeof devinHookEvents[number]) || marker !== devinManagedMarker || unexpected.length > 0) {
      stderr.write('Devin hook invocation is invalid or unowned\n');
      return 2;
    }
    await runDevinHook(eventName!, process.stdin, stdout, stderr, {
      endpoint: process.env.THREADFORGE_EVENT_ENDPOINT,
      timeoutMs: 250,
    });
    return 0;
  }
  if (family === 'devin-desktop' && action === 'hook') {
    const [eventName, marker, ...unexpected] = rest;
    if (!devinDesktopHookEvents.includes(eventName as typeof devinDesktopHookEvents[number]) ||
        marker !== devinDesktopManagedMarker || unexpected.length > 0) {
      stderr.write('Devin Desktop hook invocation is invalid or unowned\n');
      return 2;
    }
    await runDevinDesktopHook(eventName!, process.stdin, stdout, stderr, {
      endpoint: process.env.THREADFORGE_EVENT_ENDPOINT,
      timeoutMs: 250,
    });
    return 0;
  }
  if (family === 'hermes' && action === 'hook') {
    const [eventName, marker, ...unexpected] = rest;
    if (!hermesHookEvents.includes(eventName as typeof hermesHookEvents[number]) || marker !== hermesManagedMarker || unexpected.length > 0) {
      stderr.write('Hermes hook invocation is invalid or unowned\n');
      return 2;
    }
    await runHermesHook(eventName!, process.stdin, process.env.THREADFORGE_EVENT_ENDPOINT);
    return 0;
  }
  if (family === 'grok' && action === 'hook') {
    const [hookAction, marker, ...unexpected] = rest;
    if (!grokHookActions.includes(hookAction as typeof grokHookActions[number]) || marker !== grokManagedMarker || unexpected.length > 0) {
      stderr.write('Grok Build hook invocation is invalid or unowned\n');
      return 2;
    }
    await runGrokHook(hookAction!, process.stdin, stdout, stderr, {
      endpoint: process.env.THREADFORGE_EVENT_ENDPOINT,
      timeoutMs: 250,
    });
    return 0;
  }
  if (family === 'grok' && action === 'hooks') {
    grokSubsystem = 'hooks';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'grok' && action === 'sessions') {
    grokSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'hermes' && action === 'hooks') {
    hermesSubsystem = 'hooks';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'hermes' && action === 'sessions') {
    hermesSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'openhands' && action === 'hook') {
    const [eventType, marker, ...unexpected] = rest;
    if (!['session_start', 'user_prompt_submit', 'pre_tool_use', 'post_tool_use', 'stop', 'session_end'].includes(eventType ?? '') ||
        marker !== openHandsManagedMarker || unexpected.length > 0) {
      stderr.write('OpenHands hook invocation is invalid or unowned\n');
      return 2;
    }
    await runOpenHandsHook(eventType!, process.stdin, process.env.THREADFORGE_EVENT_ENDPOINT);
    return 0;
  }
  if (family === 'openclaw' && (action === 'otel' || action === 'sessions')) {
    openClawSubsystem = action;
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'opencode' && action === 'sessions') {
    openCodeSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'opencode' && action === 'plan-agent') {
    openCodeSubsystem = 'plan-agent';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'claude' && action === 'sessions') {
    claudeSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'copilot' && action === 'sessions') {
    copilotSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'cursor' && action === 'sessions') {
    cursorSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'factory' && action === 'sessions') {
    factorySubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'fx' && action === 'sessions') {
    fxSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'pi' && action === 'sessions') {
    piSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  if (family === 'prime' && action === 'sessions') {
    primeSubsystem = 'sessions';
    action = rest[0];
    rest = rest.slice(1);
    if (!action) {
      stderr.write(`${usage()}\n`);
      return 2;
    }
  }
  try {
    const options = parseOptions(rest);
    if (family === 'hermes' && hermesSubsystem === 'hooks' && action === 'print-config') {
      rejectUnexpected(options, ['command-template']);
      stdout.write(renderHermesHookConfig(options.values.get('command-template')));
      return 0;
    }
    if (family === 'cowork' && coworkSubsystem === 'otel' && action === 'print-config') {
      rejectUnexpected(options, ['endpoint']);
      stdout.write(`${renderClaudeCoworkOtelGuidance(required(options, 'endpoint'))}\n`);
      return 0;
    }
    const providedMuseScope = options.values.get('scope');
    if (family === 'muse' && !providedMuseScope) throw new Error('Muse requires an explicit --scope user|project choice');
    const providedOmpScope = options.values.get('scope');
    if (family === 'omp' && !providedOmpScope) throw new Error('Oh My Pi requires an explicit --scope user|project choice');
    const providedOpenClawScope = options.values.get('scope');
    const isOpenClawPluginAction = family === 'openclaw' && ['install', 'update', 'uninstall'].includes(action);
    if (isOpenClawPluginAction && !providedOpenClawScope) throw new Error('OpenClaw requires an explicit --scope active-gateway choice');
    const antigravityScope = options.values.get('scope') ?? 'user';
    const devinScope = options.values.get('scope') ?? 'user';
    const devinDesktopScope = options.values.get('scope') ?? 'user';
    const senpiScope = options.values.get('scope') ?? 'user';
    const qwenScope = options.values.get('scope') ?? 'user';
    const kiroScope = options.values.get('scope') ?? 'user';
    const grokScope = options.values.get('scope') ?? 'user';
    const museScope = providedMuseScope ?? 'user';
    const ompScope = providedOmpScope ?? 'user';
    const requestedClinePluginScope = options.values.get('scope') ?? 'user';
    if (family === 'cline' && clineSubsystem === 'plugin' && requestedClinePluginScope !== 'user' && requestedClinePluginScope !== 'project') {
      throw new Error('Cline plugin scope must be user or project');
    }
    const clinePluginScope = requestedClinePluginScope === 'project' ? 'project' as const : 'user' as const;
    const projectRoot = (family === 'gemini' && projectSkillSubsystem !== 'skills' && geminiSubsystem === undefined) || family === 'telemetry' || family === 'openclaw' || family === 'cowork' ||
      (family === 'grok' && grokSubsystem === 'sessions') || (family === 'dsh' && dshSubsystem === 'sessions') ||
      (family === 'hermes' && hermesSubsystem === 'sessions') ||
      (family === 'goose' && gooseSubsystem === 'sessions') ||
      (family === 'cline' && clineSubsystem === 'sessions') ||
      (family === 'cline' && clineSubsystem === 'plugin' && clinePluginScope === 'user') ||
      (family === 'cline' && clineSubsystem === 'mcp') ||
      (family === 'openhands' && openHandsSubsystem === 'mcp') ||
      (family === 'goose' && gooseSubsystem === 'otel') ||
      (family === 'vscode' && vscodeSubsystem === 'otel') ||
      (family === 'claude' && claudeSubsystem === 'sessions') ||
      (family === 'copilot' && copilotSubsystem === 'sessions') ||
      (family === 'cursor' && cursorSubsystem === 'sessions') ||
      (family === 'factory' && factorySubsystem === 'sessions') ||
      (family === 'fx' && fxSubsystem === 'sessions') ||
      (family === 'pi' && piSubsystem === 'sessions') ||
      (family === 'prime' && primeSubsystem === 'sessions') ||
      (family === 'opencode' && openCodeSubsystem === 'sessions' && action === 'status') ||
      (family === 'codex' && (action === 'sync' || action === 'status')) ||
      ((family === 'antigravity' && projectSkillSubsystem !== 'skills' && antigravityScope === 'user') || (family === 'devin' && projectSkillSubsystem !== 'skills' && devinScope === 'user') ||
        (family === 'devin-desktop' && projectSkillSubsystem !== 'skills' && devinDesktopScope === 'user') || (family === 'senpi' && senpiScope === 'user') ||
        (family === 'qwen' && qwenScope === 'user') ||
        (family === 'kiro' && kiroScope === 'user') || (family === 'grok' && grokScope === 'user') || (family === 'muse' && museScope === 'user') ||
        (family === 'omp' && ompScope === 'user') || (family === 'kimi' && projectSkillSubsystem !== 'skills'))
      ? ''
      : required(options, 'project-root');
    if (family === 'dsh' && dshSubsystem === 'mcp' && action === 'print-patch') {
      rejectUnexpected(options, ['project-root', 'command'], ['arg']);
      if (!path.isAbsolute(projectRoot)) throw new Error('--project-root must be an absolute path');
      const command = required(options, 'command');
      const args = await buildManagedMcpArgs(projectRoot, options.repeated.get('arg') ?? []);
      stdout.write(renderDeepSeekHarnessMcpPatch(command, args));
      return 0;
    }
    if (family === 'dsh' && dshSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['dsh-home', 'sessions-dir', 'state-path', 'max-sessions', 'workspace', 'session-id']);
      const status = await getDeepSeekHistoryStatus({
        dshHome: options.values.get('dsh-home'), sessionsDir: options.values.get('sessions-dir'),
        statePath: options.values.get('state-path'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        workspace: options.values.get('workspace'), sessionId: options.values.get('session-id'),
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return status.errors.length === 0 && status.incomplete_sessions === 0 && !status.sessions_truncated &&
        status.symlinks_skipped === 0 && status.oversized_files_skipped === 0 ? 0 : 1;
    }
    if (family === 'cline' && clineSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['cline-dir', 'vscode-storage-dir', 'state-path', 'max-sessions']);
      const status = await getClineHistoryStatus({
        clineDir: options.values.get('cline-dir'), vscodeStorageDir: options.values.get('vscode-storage-dir'),
        statePath: options.values.get('state-path'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return status.errors.length === 0 && status.incomplete_sessions === 0 && !status.sessions_truncated &&
        status.symlinks_skipped === 0 && status.oversized_files_skipped === 0 ? 0 : 1;
    }
    if (family === 'cline' && clineSubsystem === 'plugin' && ['install', 'update', 'uninstall', 'status'].includes(action)) {
      rejectUnexpected(options, ['scope', 'project-root', 'cline-dir']);
      const pluginOptions = {
        scope: clinePluginScope,
        ...(clinePluginScope === 'user' && options.values.has('cline-dir') ? { clineDir: required(options, 'cline-dir') } : {}),
        ...(clinePluginScope === 'project' ? { projectRoot } : {}),
      } as const;
      if (clinePluginScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      if (clinePluginScope === 'project' && options.values.has('cline-dir')) throw new Error('--cline-dir is only valid with --scope user');
      const result = action === 'install' ? await installClinePlugin(pluginOptions)
        : action === 'update' ? await updateClinePlugin(pluginOptions)
          : action === 'uninstall' ? await uninstallClinePlugin(pluginOptions)
            : await getClinePluginStatus(pluginOptions);
      stdout.write(`${JSON.stringify(result)}\n`);
      return action === 'status' && ['unmanaged', 'modified', 'source-unavailable', 'source-changed'].includes(result.result) ? 1 : 0;
    }
    if (family === 'cline' && clineSubsystem === 'mcp' && ['install', 'update', 'uninstall', 'status'].includes(action)) {
      rejectUnexpected(options, ['cline-dir', 'config-path', 'command', 'expected-command']);
      if (options.values.has('cline-dir') && options.values.has('config-path')) {
        throw new Error('use either --cline-dir or --config-path, not both');
      }
      if ((action === 'install' || action === 'status') && options.values.has('expected-command')) {
        throw new Error(`--expected-command is not valid for Cline MCP ${action}`);
      }
      if ((action === 'status' || action === 'uninstall') && options.values.has('command')) {
        throw new Error(`--command is not valid for Cline MCP ${action}`);
      }
      const pathOptions = {
        ...(options.values.has('cline-dir') ? { clineDir: required(options, 'cline-dir') } : {}),
        ...(options.values.has('config-path') ? { configPath: required(options, 'config-path') } : {}),
      };
      if (action === 'status') {
        const status = await getClineUserMcpServerStatus(pathOptions);
        stdout.write(`${JSON.stringify(status)}\n`);
        return status.result === 'installed' ? 0 : 1;
      }
      const result = action === 'install' ? await installClineUserMcpServer(required(options, 'command'), pathOptions)
        : action === 'update' ? await updateClineUserMcpServer(required(options, 'expected-command'), required(options, 'command'), pathOptions)
          : await uninstallClineUserMcpServer(required(options, 'expected-command'), pathOptions);
      stdout.write(`${JSON.stringify({ result })}\n`);
      return 0;
    }
    if (family === 'openhands' && openHandsSubsystem === 'mcp' && ['install', 'update', 'uninstall', 'status'].includes(action)) {
      rejectUnexpected(options, ['config-path', 'command', 'expected-command']);
      if ((action === 'install' || action === 'status') && options.values.has('expected-command')) {
        throw new Error(`--expected-command is not valid for OpenHands MCP ${action}`);
      }
      if ((action === 'status' || action === 'uninstall') && options.values.has('command')) {
        throw new Error(`--command is not valid for OpenHands MCP ${action}`);
      }
      const pathOptions = options.values.has('config-path') ? { configPath: required(options, 'config-path') } : {};
      if (action === 'status') {
        const status = await getOpenHandsUserMcpServerStatus(pathOptions);
        stdout.write(`${JSON.stringify(status)}\n`);
        return status.result === 'installed' ? 0 : 1;
      }
      const result = action === 'install'
        ? await installOpenHandsUserMcpServer(required(options, 'command'), pathOptions)
        : action === 'update'
          ? await updateOpenHandsUserMcpServer(required(options, 'expected-command'), required(options, 'command'), pathOptions)
          : await uninstallOpenHandsUserMcpServer(required(options, 'expected-command'), pathOptions);
      stdout.write(`${JSON.stringify({ result })}\n`);
      return 0;
    }
    if (family === 'cline' && clineSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['cline-dir', 'vscode-storage-dir', 'state-path', 'endpoint', 'max-sessions', 'interval'], [], ['print', 'include-content', 'watch']);
      const syncOptions = {
        clineDir: options.values.get('cline-dir'), vscodeStorageDir: options.values.get('vscode-storage-dir'),
        statePath: options.values.get('state-path'), endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        preview: options.switches.has('print'), includeContent: options.switches.has('include-content'),
      };
      if (options.switches.has('watch')) {
        const intervalMs = parseDeepSeekDuration(options.values.get('interval') ?? '1m');
        const controller = new AbortController();
        const stop = () => controller.abort();
        process.once('SIGINT', stop);
        try {
          const watchResult = await watchClineHistory(syncOptions, intervalMs, (sweep) => stdout.write(`${JSON.stringify(sweep)}\n`), controller.signal);
          return watchResult.complete ? 0 : 1;
        } finally { process.removeListener('SIGINT', stop); }
      }
      if (options.values.has('interval')) throw new Error('--interval requires --watch');
      const syncResult = await syncClineHistory(syncOptions);
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    }
    if (family === 'dsh' && dshSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['dsh-home', 'sessions-dir', 'state-path', 'endpoint', 'max-sessions', 'workspace', 'session-id', 'interval'], [], ['print', 'include-content', 'watch']);
      const syncOptions = {
        dshHome: options.values.get('dsh-home'), sessionsDir: options.values.get('sessions-dir'),
        statePath: options.values.get('state-path'), endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        workspace: options.values.get('workspace'), sessionId: options.values.get('session-id'),
        preview: options.switches.has('print'), includeContent: options.switches.has('include-content'),
      };
      if (options.switches.has('watch')) {
        const intervalMs = parseDeepSeekDuration(options.values.get('interval') ?? '1m');
        const controller = new AbortController();
        const stop = () => controller.abort();
        process.once('SIGINT', stop);
        try {
          const watchResult = await watchDeepSeekHistory(syncOptions, intervalMs, (sweep) => stdout.write(`${JSON.stringify(sweep)}\n`), controller.signal);
          return watchResult.complete ? 0 : 1;
        } finally { process.removeListener('SIGINT', stop); }
      }
      if (options.values.has('interval')) throw new Error('--interval requires --watch');
      const syncResult = await syncDeepSeekHistory(syncOptions);
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    }
    if (family === 'bootstrap' && bootstrapSubsystem === 'instructions') {
      const hosts = parseProjectInstructionHosts(required(options, 'hosts'));
      if (action !== 'plan' && action !== 'install' && action !== 'update' && action !== 'uninstall') {
        throw new Error('bootstrap instructions action must be plan, install, update, or uninstall');
      }
      rejectUnexpected(options, ['project-root', 'hosts']);
      const result = await runProjectInstructionBootstrap(action, projectRoot, hosts);
      stdout.write(`${JSON.stringify(result)}\n`);
      return result.status === 'partial' ? 1 : 0;
    }
    if (family === 'bootstrap' && bootstrapSubsystem === 'mcp') {
      const hosts = parseProjectBootstrapHosts(required(options, 'hosts'));
      if (action === 'uninstall') {
        rejectUnexpected(options, ['project-root', 'hosts']);
        const result = await runProjectBootstrapMcp('uninstall', projectRoot, hosts);
        stdout.write(`${JSON.stringify(result)}\n`);
        return result.status === 'partial' ? 1 : 0;
      }
      if (action !== 'plan' && action !== 'install' && action !== 'update') {
        throw new Error('bootstrap mcp action must be plan, install, update, or uninstall');
      }
      rejectUnexpected(options, ['project-root', 'hosts', 'command'], ['arg']);
      const command = required(options, 'command');
      const mcpArgs = await buildManagedMcpArgs(projectRoot, options.repeated.get('arg') ?? []);
      const result = await runProjectBootstrapMcp(action, projectRoot, hosts, command, mcpArgs);
      stdout.write(`${JSON.stringify(result)}\n`);
      return result.status === 'partial' ? 1 : 0;
    }
    let result: unknown;
    let warning: string | undefined;
    if (family === 'devin' && projectSkillSubsystem !== 'skills' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command-template']);
      if (devinScope !== 'user' && devinScope !== 'project') throw new Error('Devin hook scope must be user or project');
      if (devinScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      result = await installDevinHooks({
        scope: devinScope as DevinHookScope,
        ...(devinScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('command-template') ? { commandTemplate: required(options, 'command-template') } : {}),
      });
      warning = 'Restart Devin CLI to reload hook configuration; live host payload and response behavior remain unverified';
    } else if (family === 'devin' && projectSkillSubsystem !== 'skills' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (devinScope !== 'user' && devinScope !== 'project') throw new Error('Devin hook scope must be user or project');
      if (devinScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      result = await removeDevinHooks({
        scope: devinScope as DevinHookScope,
        ...(devinScope === 'project' ? { projectRoot } : {}),
      });
      warning = 'Restart Devin CLI to reload hook configuration';
    } else if (family === 'devin-desktop' && projectSkillSubsystem !== 'skills' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command-template']);
      if (devinDesktopScope !== 'user' && devinDesktopScope !== 'project') throw new Error('Devin Desktop hook scope must be user or project');
      if (devinDesktopScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      result = await installDevinDesktopHooks({
        scope: devinDesktopScope as DevinDesktopHookScope,
        ...(devinDesktopScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('command-template') ? { commandTemplate: required(options, 'command-template') } : {}),
      });
      warning = 'Restart Devin Desktop to reload Cascade/Windsurf hooks; installed-host payload and response behavior remain unverified';
    } else if (family === 'devin-desktop' && projectSkillSubsystem !== 'skills' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (devinDesktopScope !== 'user' && devinDesktopScope !== 'project') throw new Error('Devin Desktop hook scope must be user or project');
      if (devinDesktopScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      result = await removeDevinDesktopHooks({
        scope: devinDesktopScope as DevinDesktopHookScope,
        ...(devinDesktopScope === 'project' ? { projectRoot } : {}),
      });
      warning = 'Restart Devin Desktop to reload Cascade/Windsurf hooks';
    } else if (family === 'senpi' && (action === 'install' || action === 'update' || action === 'uninstall' || action === 'status')) {
      rejectUnexpected(options, ['scope', 'project-root', 'senpi-agent-dir']);
      if (senpiScope !== 'user' && senpiScope !== 'project') throw new Error('Senpi --scope must be user or project');
      if (senpiScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      if (senpiScope === 'project' && options.values.has('senpi-agent-dir')) throw new Error('--senpi-agent-dir is only valid with --scope user');
      const senpiOptions = {
        scope: senpiScope as 'user' | 'project',
        ...(senpiScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('senpi-agent-dir') ? { senpiAgentDir: required(options, 'senpi-agent-dir') } : {}),
      };
      if (action === 'status') {
        result = { schema_version: 'threadforge.senpi-status.v1', extension: await getSenpiExtensionState(senpiOptions) };
      } else {
        result = action === 'uninstall'
          ? await removeSenpiExtension(senpiOptions)
          : action === 'update'
            ? await updateSenpiExtension(senpiOptions)
            : await installSenpiExtension(senpiOptions);
        warning = senpiScope === 'project'
          ? 'Senpi project extensions load only in trusted projects; Threadforge did not change project trust. Restart Senpi to load the extension.'
          : action === 'uninstall'
            ? 'Restart Senpi to unload the extension.'
            : 'Restart Senpi to load the extension; installed-host API/version behavior remains unverified';
      }
    } else if (family === 'copilot' && copilotSubsystem === 'prompts' && (action === 'install' || action === 'uninstall')) {
      rejectUnexpected(options, ['project-root', 'skill-file', 'sha256']);
      const promptOptions = {
        projectRoot,
        skillFile: required(options, 'skill-file'),
        sha256: required(options, 'sha256'),
      };
      result = action === 'install'
        ? await installCopilotProjectPrompt(promptOptions)
        : await uninstallCopilotProjectPrompt(promptOptions);
      if (action === 'install') {
        warning = 'Copilot prompt files are manually invoked IDE customizations; discovery and use depend on the selected IDE/version. This command does not configure prompt-file settings, change trust, or enable Copilot CLI prompt-file support.';
      }
    } else if (family === 'aider' && projectSkillSubsystem === 'skills' && (action === 'install' || action === 'uninstall')) {
      rejectUnexpected(options, ['project-root', 'skill-file', 'sha256']);
      const skillOptions = {
        projectRoot,
        skillFile: required(options, 'skill-file'),
        sha256: required(options, 'sha256'),
      };
      result = action === 'install'
        ? await installAiderProjectSkill(skillOptions)
        : await uninstallAiderProjectSkill(skillOptions);
      if (action === 'install') {
        const skillPath = (result as { path: string }).path;
        warning = `Threadforge does not configure Aider to auto-read this skill. From the project root, explicitly load the read-only guidance with: aider --read "${skillPath}". Aider configuration was not edited.`;
      }
    } else if ((family === 'claude-code' || family === 'command-code' || family === 'cline' || family === 'cursor' || family === 'devin' || family === 'opencode' || family === 'codex' || family === 'gemini' || family === 'antigravity' || family === 'copilot' || family === 'openhands' || family === 'kimi' || family === 'zcode' || family === 'dsh' || family === 'qoder' || family === 'windsurf' || family === 'devin-desktop') && projectSkillSubsystem === 'skills' && (action === 'install' || action === 'uninstall')) {
      rejectUnexpected(options, ['project-root', 'skill-file', 'sha256']);
      const skillOptions = {
        host: family === 'windsurf' || family === 'devin-desktop' ? 'windsurf' : family === 'codex' ? 'codex-cli' : family === 'gemini' ? 'gemini-cli' : family === 'copilot' ? 'github-copilot' : family === 'kimi' ? 'kimi-code' : family === 'dsh' ? 'deepseek-harness' : family as ProjectSkillHost,
        projectRoot,
        skillFile: required(options, 'skill-file'),
        sha256: required(options, 'sha256'),
      };
      result = action === 'install'
        ? await installProjectSkill(skillOptions)
        : await uninstallProjectSkill(skillOptions);
    } else if (family === 'qoder' && qoderCommandSubsystem === 'commands' && (action === 'install' || action === 'uninstall')) {
      rejectUnexpected(options, ['project-root', 'skill-file', 'sha256']);
      const commandOptions = {
        projectRoot,
        skillFile: required(options, 'skill-file'),
        sha256: required(options, 'sha256'),
      };
      result = action === 'install'
        ? await installQoderProjectCommand(commandOptions)
        : await uninstallQoderProjectCommand(commandOptions);
    } else if (family === 'grok' && grokSubsystem === 'hooks' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command-template']);
      if (grokScope !== 'user' && grokScope !== 'project') throw new Error('Grok hook scope must be user or project');
      if (grokScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      result = await installGrokHooks({
        scope: grokScope as GrokHookScope,
        ...(grokScope === 'project' ? { projectRoot } : {}),
        commandTemplate: required(options, 'command-template'),
      });
      if (grokScope === 'project') warning = 'Grok Build project hooks require /hooks-trust; Threadforge did not change project trust';
    } else if (family === 'grok' && grokSubsystem === 'hooks' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (grokScope !== 'user' && grokScope !== 'project') throw new Error('Grok hook scope must be user or project');
      if (grokScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      result = await removeGrokHooks({ scope: grokScope as GrokHookScope, ...(grokScope === 'project' ? { projectRoot } : {}) });
      if (grokScope === 'project') warning = 'Grok Build project hooks require /hooks-trust; Threadforge did not change project trust';
    } else if (family === 'hermes' && hermesSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['db-path', 'state-path', 'max-sessions']);
      const status = await getHermesHistoryStatus({
        dbPath: options.values.get('db-path'), statePath: options.values.get('state-path'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return status.errors.length === 0 && !status.sessions_truncated ? 0 : 1;
    } else if (family === 'hermes' && hermesSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['db-path', 'state-path', 'endpoint', 'max-sessions', 'max-messages'], [], ['print', 'include-content']);
      const syncResult = await syncHermesHistory({
        dbPath: options.values.get('db-path'), statePath: options.values.get('state-path'), endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        maxMessages: options.values.has('max-messages') ? Number(required(options, 'max-messages')) : undefined,
        preview: options.switches.has('print'), includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'goose' && gooseSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['db-path', 'state-path', 'max-sessions']);
      const status = await getGooseHistoryStatus({
        dbPath: options.values.get('db-path'), statePath: options.values.get('state-path'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return status.errors.length === 0 && !status.sessions_truncated ? 0 : 1;
    } else if (family === 'goose' && gooseSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['db-path', 'state-path', 'endpoint', 'max-sessions', 'max-messages'], [], ['print', 'include-content']);
      const syncResult = await syncGooseHistory({
        dbPath: options.values.get('db-path'), statePath: options.values.get('state-path'), endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        maxMessages: options.values.has('max-messages') ? Number(required(options, 'max-messages')) : undefined,
        preview: options.switches.has('print'), includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'grok' && grokSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['sessions-dir', 'state-path', 'max-sessions']);
      const status = await getGrokHistoryStatus({
        sessionsDir: options.values.get('sessions-dir'), statePath: options.values.get('state-path'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'grok' && grokSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['sessions-dir', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
      const syncResult = await syncGrokHistory({
        sessionsDir: options.values.get('sessions-dir'), statePath: options.values.get('state-path'), endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        preview: options.switches.has('print'), includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'vscode' && vscodeSubsystem === 'otel' && action === 'print-config') {
      rejectUnexpected(options, ['endpoint']);
      stdout.write(`${renderVSCodeOtelConfig(options.values.has('endpoint') ? required(options, 'endpoint') : undefined)}\n`);
      return 0;
    } else if (family === 'vscode' && vscodeSubsystem === 'otel' && (action === 'status' || action === 'validate')) {
      rejectUnexpected(options, ['settings-path', 'log-path', 'since'], [], ['json']);
      const status = await getVSCodeOtelStatus({
        ...(options.values.has('settings-path') ? { settingsPath: required(options, 'settings-path') } : {}),
        ...(options.values.has('log-path') ? { logPath: required(options, 'log-path') } : {}),
      });
      if (action === 'status') {
        stdout.write(options.switches.has('json')
          ? `${JSON.stringify(status)}\n`
          : `VS Code Copilot OTLP status: enabled=${status.telemetry_enabled}, endpoint=${status.configured_endpoint}, events=${status.telemetry.observed_events}, issues=${status.issues.length}\n`);
        return 0;
      }
      const sinceMs = options.values.has('since') ? parseVSCodeOtelDuration(required(options, 'since')) : parseVSCodeOtelDuration('10m');
      const validation = validateVSCodeOtelStatus(status, sinceMs);
      stdout.write(options.switches.has('json')
        ? `${JSON.stringify(validation)}\n`
        : `VS Code Copilot OTLP validation ${validation.valid ? 'passed' : 'failed'}: ${validation.issues.length} issue(s)\n`);
      return validation.valid ? 0 : 1;
    } else if (family === 'goose' && gooseSubsystem === 'otel' && action === 'print-config') {
      rejectUnexpected(options, ['endpoint']);
      stdout.write(`${renderGooseOtelConfig(options.values.has('endpoint') ? required(options, 'endpoint') : undefined)}\n`);
      return 0;
    } else if (family === 'goose' && gooseSubsystem === 'otel' && (action === 'status' || action === 'validate')) {
      rejectUnexpected(options, ['config-path', 'log-path', 'since'], [], ['json']);
      const status = await getGooseOtelStatus({
        ...(options.values.has('config-path') ? { configPath: required(options, 'config-path') } : {}),
        ...(options.values.has('log-path') ? { logPath: required(options, 'log-path') } : {}),
      });
      if (action === 'status') {
        stdout.write(options.switches.has('json')
          ? `${JSON.stringify(status)}\n`
          : `Goose OTLP status: endpoint=${status.configured_endpoint}, observed_events=${status.telemetry.observed_events}, issues=${status.issues.length}\n`);
        return 0;
      }
      const sinceMs = options.values.has('since') ? parseGooseOtelDuration(required(options, 'since')) : parseGooseOtelDuration('10m');
      const validation = validateGooseOtelStatus(status, sinceMs);
      stdout.write(options.switches.has('json')
        ? `${JSON.stringify(validation)}\n`
        : `Goose OTLP validation ${validation.valid ? 'passed' : 'failed'}: ${validation.issues.length} issue(s)\n`);
      return validation.valid ? 0 : 1;
    } else if (family === 'openclaw' && action === 'print-config') {
      rejectUnexpected(options, ['endpoint'], [], ['capture-content', 'allow-remote']);
      stdout.write(`${renderOpenClawOtelConfig({
        ...(options.values.has('endpoint') ? { endpoint: required(options, 'endpoint') } : {}),
        ...(options.switches.has('capture-content') ? { captureContent: true } : {}),
        ...(options.switches.has('allow-remote') ? { allowRemote: true } : {}),
      })}\n`);
      return 0;
    } else if (family === 'openclaw' && openClawSubsystem === 'otel' && (action === 'status' || action === 'validate')) {
      rejectUnexpected(options, ['log-path', 'since'], [], ['json']);
      const status = await getOpenClawOtelStatus(options.values.has('log-path') ? { logPath: required(options, 'log-path') } : {});
      if (action === 'status') {
        if (options.switches.has('json')) stdout.write(`${JSON.stringify(status)}\n`);
        else stdout.write(status.last_event_observed
          ? `OpenClaw OTLP events observed: ${status.observed_events}; last at ${status.last_event_observed_at}${status.complete ? '' : ' (local log scan incomplete)'}\n`
          : `No OpenClaw OTLP events observed in the local Threadforge event log${status.present ? '' : ' (log not present)'}\n`);
        return 0;
      }
      const sinceMs = options.values.has('since') ? parseOpenClawDuration(required(options, 'since')) : parseOpenClawDuration('10m');
      const validation = validateOpenClawOtelStatus(status, sinceMs);
      if (options.switches.has('json')) stdout.write(`${JSON.stringify(validation)}\n`);
      else stdout.write(validation.valid
        ? `OpenClaw OTLP validation passed: recent event observed within ${options.values.get('since') ?? '10m'}\n`
        : `OpenClaw OTLP validation failed: no recent OpenClaw event observed within ${options.values.get('since') ?? '10m'}\n`);
      return validation.valid ? 0 : 1;
    } else if (family === 'openclaw' && openClawSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['state-path']);
      stdout.write(`${JSON.stringify(await getOpenClawHistoryStatus({ statePath: options.values.get('state-path') }))}\n`);
      return 0;
    } else if (family === 'openclaw' && openClawSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['openclaw-cli', 'state-path', 'endpoint', 'max-sessions', 'max-messages'], [], ['print', 'include-content']);
      const syncResult = await syncOpenClawHistory({
        openclawCli: options.values.get('openclaw-cli'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        maxMessages: options.values.has('max-messages') ? Number(required(options, 'max-messages')) : undefined,
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'opencode' && openCodeSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['state-path']);
      stdout.write(`${JSON.stringify(await getOpenCodeHistoryStatus({ statePath: options.values.get('state-path') }))}\n`);
      return 0;
    } else if (family === 'opencode' && openCodeSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['project-root', 'opencode-cli', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
      const syncResult = await syncOpenCodeHistory({
        projectRoot,
        opencodeCli: options.values.get('opencode-cli'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'claude' && claudeSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['projects-dir', 'state-path', 'max-files']);
      const status = await getClaudeHistoryStatus({
        projectsDir: options.values.get('projects-dir'),
        statePath: options.values.get('state-path'),
        maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'claude' && claudeSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['projects-dir', 'state-path', 'endpoint', 'max-files'], [], ['print', 'include-content']);
      const syncResult = await syncClaudeHistory({
        projectsDir: options.values.get('projects-dir'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'copilot' && copilotSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['copilot-dir', 'state-path', 'max-sessions']);
      const status = await getCopilotHistoryStatus({
        copilotDir: options.values.get('copilot-dir'),
        statePath: options.values.get('state-path'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'copilot' && copilotSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['copilot-dir', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
      const syncResult = await syncCopilotHistory({
        copilotDir: options.values.get('copilot-dir'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'cursor' && cursorSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['projects-dir', 'global-db-path', 'state-path', 'max-files', 'max-composers']);
      const status = await getCursorHistoryStatus({
        projectsDir: options.values.get('projects-dir'),
        globalDbPath: options.values.get('global-db-path'),
        statePath: options.values.get('state-path'),
        maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
        maxComposers: options.values.has('max-composers') ? Number(required(options, 'max-composers')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'cursor' && cursorSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['projects-dir', 'global-db-path', 'state-path', 'endpoint', 'max-files', 'max-composers'], [], ['print', 'include-content']);
      const syncResult = await syncCursorHistory({
        projectsDir: options.values.get('projects-dir'),
        globalDbPath: options.values.get('global-db-path'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
        maxComposers: options.values.has('max-composers') ? Number(required(options, 'max-composers')) : undefined,
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'factory' && factorySubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['sessions-dir', 'state-path', 'max-sessions']);
      const status = await getFactoryHistoryStatus({
        sessionsDir: options.values.get('sessions-dir'),
        statePath: options.values.get('state-path'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'factory' && factorySubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['sessions-dir', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
      const syncResult = await syncFactoryHistory({
        sessionsDir: options.values.get('sessions-dir'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'fx' && fxSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['sessions-dir', 'state-path', 'max-sessions']);
      const status = await getFxHistoryStatus({
        sessionsDir: options.values.get('sessions-dir'),
        statePath: options.values.get('state-path'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'fx' && fxSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['sessions-dir', 'state-path', 'endpoint', 'max-sessions'], [], ['print', 'include-content']);
      const syncResult = await syncFxHistory({
        sessionsDir: options.values.get('sessions-dir'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        maxSessions: options.values.has('max-sessions') ? Number(required(options, 'max-sessions')) : undefined,
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'pi' && piSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['sessions-dir', 'state-path', 'max-files']);
      const status = await getPiHistoryStatus({
        sessionsDir: options.values.get('sessions-dir'),
        statePath: options.values.get('state-path'),
        maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'pi' && piSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['sessions-dir', 'state-path', 'endpoint', 'max-files'], [], ['print', 'include-content']);
      const syncResult = await syncPiHistory({
        sessionsDir: options.values.get('sessions-dir'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'prime' && primeSubsystem === 'sessions' && action === 'status') {
      rejectUnexpected(options, ['sessions-dir', 'artifacts-dir', 'state-path', 'max-files']);
      const status = await getPrimeHistoryStatus({
        sessionsDir: options.values.get('sessions-dir'), artifactsDir: options.values.get('artifacts-dir'),
        statePath: options.values.get('state-path'),
        maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'prime' && primeSubsystem === 'sessions' && action === 'sync') {
      rejectUnexpected(options, ['sessions-dir', 'artifacts-dir', 'state-path', 'endpoint', 'max-files'], [], ['print', 'include-content']);
      const syncResult = await syncPrimeHistory({
        sessionsDir: options.values.get('sessions-dir'), artifactsDir: options.values.get('artifacts-dir'),
        statePath: options.values.get('state-path'), endpoint: options.values.get('endpoint'),
        maxFiles: options.values.has('max-files') ? Number(required(options, 'max-files')) : undefined,
        preview: options.switches.has('print'), includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify(syncResult)}\n`);
      return syncResult.complete ? 0 : 1;
    } else if (family === 'kimi' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['command', 'kimi-code-home']);
      const configOptions = {
        command: required(options, 'command'),
        ...(options.values.has('kimi-code-home') ? { kimiCodeHome: required(options, 'kimi-code-home') } : {}),
      };
      result = action === 'install'
        ? await installKimiCodeHooks(configOptions)
        : await updateKimiCodeHooks(configOptions);
    } else if (family === 'kimi' && action === 'uninstall') {
      rejectUnexpected(options, ['kimi-code-home']);
      result = await removeKimiCodeHooks(options.values.has('kimi-code-home')
        ? { kimiCodeHome: required(options, 'kimi-code-home') }
        : {});
    } else if (family === 'kiro' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['command', 'scope', 'project-root', 'kiro-home']);
      if (kiroScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      if (kiroScope === 'project' && options.values.has('kiro-home')) throw new Error('--kiro-home is only valid with --scope user');
      const configOptions = {
        command: required(options, 'command'),
        scope: kiroScope as KiroHookScope,
        ...(kiroScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('kiro-home') ? { kiroHome: required(options, 'kiro-home') } : {}),
      };
      result = action === 'install' ? await installKiroHooks(configOptions) : await updateKiroHooks(configOptions);
    } else if (family === 'kiro' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root', 'kiro-home']);
      if (kiroScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      if (kiroScope === 'project' && options.values.has('kiro-home')) throw new Error('--kiro-home is only valid with --scope user');
      result = await removeKiroHooks({
        scope: kiroScope as KiroHookScope,
        ...(kiroScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('kiro-home') ? { kiroHome: required(options, 'kiro-home') } : {}),
      });
    } else if (family === 'muse' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['command', 'scope', 'project-root', 'xdg-config-home']);
      if (museScope !== 'user' && museScope !== 'project') throw new Error('Muse scope must be user or project');
      if (museScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      if (museScope === 'project' && options.values.has('xdg-config-home')) throw new Error('--xdg-config-home is only valid with --scope user');
      const configOptions = {
        command: required(options, 'command'),
        scope: museScope as MuseHookScope,
        ...(museScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('xdg-config-home') ? { xdgConfigHome: required(options, 'xdg-config-home') } : {}),
      };
      result = action === 'install' ? await installMuseHooks(configOptions) : await updateMuseHooks(configOptions);
    } else if (family === 'muse' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root', 'xdg-config-home']);
      if (museScope !== 'user' && museScope !== 'project') throw new Error('Muse scope must be user or project');
      if (museScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      if (museScope === 'project' && options.values.has('xdg-config-home')) throw new Error('--xdg-config-home is only valid with --scope user');
      result = await removeMuseHooks({
        scope: museScope as MuseHookScope,
        ...(museScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('xdg-config-home') ? { xdgConfigHome: required(options, 'xdg-config-home') } : {}),
      });
    } else if (family === 'omp' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'omp-agent-dir']);
      if (ompScope !== 'user' && ompScope !== 'project') throw new Error('Oh My Pi scope must be user or project');
      if (ompScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      if (ompScope === 'project' && options.values.has('omp-agent-dir')) throw new Error('--omp-agent-dir is only valid with --scope user');
      const configOptions = {
        scope: ompScope as OmpHookScope,
        ...(ompScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('omp-agent-dir') ? { ompAgentDir: required(options, 'omp-agent-dir') } : {}),
      };
      result = action === 'install' ? await installOmpExtension(configOptions) : await updateOmpExtension(configOptions);
    } else if (family === 'omp' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root', 'omp-agent-dir']);
      if (ompScope !== 'user' && ompScope !== 'project') throw new Error('Oh My Pi scope must be user or project');
      if (ompScope === 'user' && options.values.has('project-root')) throw new Error('--project-root is only valid with --scope project');
      if (ompScope === 'project' && options.values.has('omp-agent-dir')) throw new Error('--omp-agent-dir is only valid with --scope user');
      result = await removeOmpExtension({
        scope: ompScope as OmpHookScope,
        ...(ompScope === 'project' ? { projectRoot } : {}),
        ...(options.values.has('omp-agent-dir') ? { ompAgentDir: required(options, 'omp-agent-dir') } : {}),
      });
    } else if (family === 'openhands' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command-template']);
      result = await installOpenHandsProjectHooks(projectRoot, {
        ...(options.values.has('command-template') ? { commandTemplate: required(options, 'command-template') } : {}),
      });
    } else if (family === 'openhands' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeOpenHandsProjectHooks(projectRoot);
    } else if (family === 'openclaw' && (action === 'install' || action === 'update' || action === 'uninstall')) {
      rejectUnexpected(options, ['scope', 'openclaw-cli'], [], ['print', 'acknowledge-local-source']);
      if (providedOpenClawScope !== 'active-gateway') throw new Error('OpenClaw scope must be active-gateway; run the command under the profile used by the Gateway');
      if (action !== 'install' && options.switches.has('acknowledge-local-source')) {
        throw new Error('--acknowledge-local-source is valid only with OpenClaw install');
      }
      const pluginAction = action as OpenClawPluginAction;
      const pluginOptions = {
        scope: 'active-gateway' as const,
        ...(options.values.has('openclaw-cli') ? { openclawCli: required(options, 'openclaw-cli') } : {}),
        ...(options.switches.has('acknowledge-local-source') ? { acknowledgeLocalSource: true } : {}),
      };
      if (options.switches.has('print')) {
        const commands = buildOpenClawPluginCommands(pluginAction, pluginOptions);
        stdout.write(`${JSON.stringify({ schema_version: 'threadforge.adapter-install.v1', family, action, result: 'planned', commands })}\n`);
        return 0;
      }
      result = await runOpenClawPluginAction(pluginAction, pluginOptions);
    } else if (family === 'codex' && action === 'status') {
      rejectUnexpected(options, ['codex-home', 'state-path']);
      const status = await getCodexRolloutStatus({
        codexHome: options.values.get('codex-home'),
        statePath: options.values.get('state-path'),
      });
      stdout.write(`${JSON.stringify(status)}\n`);
      return 0;
    } else if (family === 'codex' && action === 'sync') {
      rejectUnexpected(options, ['codex-home', 'state-path', 'endpoint'], [], ['print', 'include-content']);
      const syncResult = await syncCodexRollouts({
        codexHome: options.values.get('codex-home'),
        statePath: options.values.get('state-path'),
        endpoint: options.values.get('endpoint'),
        preview: options.switches.has('print'),
        includeContent: options.switches.has('include-content'),
      });
      stdout.write(`${JSON.stringify({ schema_version: 'threadforge.codex-rollout-sync.v1', ...syncResult })}\n`);
      return syncResult.errors.length > 0 ? 1 : 0;
    } else if (family === 'telemetry' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['host']);
      const host = required(options, 'host');
      if (host === 'claude-code') result = await installClaudeTelemetry();
      else if (host === 'codex-cli') result = await installCodexTelemetry();
      else throw new Error('unsupported telemetry host');
    } else if (family === 'telemetry' && action === 'uninstall') {
      rejectUnexpected(options, ['host']);
      const host = required(options, 'host');
      if (host === 'claude-code') result = await removeClaudeTelemetry();
      else if (host === 'codex-cli') result = await removeCodexTelemetry();
      else throw new Error('unsupported telemetry host');
    } else if (family === 'gemini' && geminiSubsystem === 'memory' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command', 'task-id', 'session-id', 'max-task-entries'], [], ['discover-active-task']);
      result = await installGeminiProjectMemoryHook(projectRoot,
        taskAwareMemoryHookCommand(options, 'Gemini', '--threadforge-memory=gemini-v1'));
      warning = 'Gemini BeforeAgent adds approved project memory as guidance only; review Gemini project trust, and Threadforge does not change trust or telemetry settings.';
    } else if (family === 'gemini' && geminiSubsystem === 'memory' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeGeminiProjectMemoryHook(projectRoot);
    } else if (family === 'gemini' && geminiSubsystem === 'plan-gate' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command']);
      result = await installGeminiProjectPlanGateHook(projectRoot, required(options, 'command'));
      warning = 'Gemini BeforeTool plan gate only returns deny for direct write_file/replace operations that fail the configured task/scope checks; hooks may fail open, and shell, MCP, races after path checking, and other tools are not covered. Threadforge does not change project trust or permissions.';
    } else if (family === 'gemini' && geminiSubsystem === 'plan-gate' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeGeminiProjectPlanGateHook(projectRoot);
    } else if (family === 'cursor' && cursorSubsystem === 'memory' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command']);
      result = await installCursorProjectMemoryHook(projectRoot, required(options, 'command'));
      warning = 'Cursor sessionStart adds bounded approved project memory as startup guidance, not per-prompt retrieval or authorization; use the Threadforge MCP tool for task-specific context.';
    } else if (family === 'cursor' && cursorSubsystem === 'memory' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeCursorProjectMemoryHook(projectRoot);
    } else if (family === 'cursor' && cursorSubsystem === 'plan-gate' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command']);
      result = await installCursorProjectPlanGateHook(projectRoot, required(options, 'command'));
      warning = 'Cursor plan gate is opt-in and fail-closed for direct Write tools only; it emits deny decisions but no allows. It does not gate shell/MCP tools, close path-check races, sandbox execution, or change Cursor permissions.';
    } else if (family === 'cursor' && cursorSubsystem === 'plan-gate' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeCursorProjectPlanGateHook(projectRoot);
    } else if (family === 'gemini' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, []);
      result = await installGeminiTelemetry();
    } else if (family === 'gemini' && action === 'uninstall') {
      rejectUnexpected(options, []);
      result = await removeGeminiTelemetry();
    } else if (family === 'claude-code' && claudeCodeSubsystem === 'memory' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command', 'task-id', 'session-id', 'max-task-entries'], [], ['discover-active-task']);
      result = await installClaudeProjectMemoryHook(projectRoot,
        taskAwareMemoryHookCommand(options, 'Claude Code', '--threadforge-memory=claude-v1'));
      warning = 'Claude UserPromptSubmit adds approved project memory as guidance only; retrieval is local, and the hook does not authorize actions';
    } else if (family === 'claude-code' && claudeCodeSubsystem === 'memory' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeClaudeProjectMemoryHook(projectRoot);
    } else if (family === 'claude-code' && claudeCodeSubsystem === 'plan-gate' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command']);
      result = await installClaudeProjectPlanGateHook(projectRoot, required(options, 'command'));
      warning = 'Claude plan gate is deny-only for direct Write/Edit file tools; it emits no allow decision, does not sandbox shell/MCP or close TOCTOU races, and leaves Claude permissions authoritative.';
    } else if (family === 'claude-code' && claudeCodeSubsystem === 'plan-gate' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeClaudeProjectPlanGateHook(projectRoot);
    } else if (family === 'claude' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command']);
      const command = required(options, 'command');
      result = await installClaudeProjectHooks(projectRoot, { command });
    } else if (family === 'claude' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeClaudeProjectHooks(projectRoot);
    } else if (family === 'opencode' && openCodeSubsystem === 'plan-agent' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root']);
      result = await installOpenCodeProjectPlanAgent(required(options, 'project-root'));
      warning = 'OpenCode threadforge-plan is a static read-only planning profile with no Threadforge task-state binding; it does not force its selection or constrain other agents. Recheck resolved permissions after changing the profile or OpenCode configuration.';
    } else if (family === 'opencode' && openCodeSubsystem === 'plan-agent' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeOpenCodeProjectPlanAgent(required(options, 'project-root'));
    } else if (family === 'opencode' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options,
        ['project-root', 'module', 'memory-project-id', 'memory-max-items', 'memory-max-tokens', 'memory-task-id', 'memory-session-id', 'memory-max-task-entries'],
        [], ['memory-discover-active-task']);
      const memoryProjectId = options.values.has('memory-project-id') ? required(options, 'memory-project-id') : undefined;
      const taskId = options.values.get('memory-task-id');
      const sessionId = options.values.get('memory-session-id');
      const discoverActiveTask = options.switches.has('memory-discover-active-task');
      const hasMemorySettings = options.values.has('memory-max-items') || options.values.has('memory-max-tokens') ||
        taskId !== undefined || sessionId !== undefined || discoverActiveTask || options.values.has('memory-max-task-entries');
      if (!memoryProjectId && hasMemorySettings) throw new Error('OpenCode memory options require --memory-project-id');
      if (sessionId !== undefined && taskId === undefined) throw new Error('--memory-session-id requires --memory-task-id');
      if (taskId !== undefined && discoverActiveTask) throw new Error('--memory-task-id and --memory-discover-active-task are mutually exclusive');
      const hasTaskSelection = taskId !== undefined || discoverActiveTask;
      if (options.values.has('memory-max-task-entries') && !hasTaskSelection) {
        throw new Error('--memory-max-task-entries requires --memory-task-id or --memory-discover-active-task');
      }
      const memory = memoryProjectId ? {
        projectId: memoryProjectId,
        maxItems: Number(options.values.get('memory-max-items') ?? 8),
        maxTokens: Number(options.values.get('memory-max-tokens') ?? 1600),
        ...(taskId !== undefined ? { taskId } : {}),
        ...(sessionId !== undefined ? { sessionId } : {}),
        ...(discoverActiveTask ? { discoverActiveTask: true } : {}),
        ...(hasTaskSelection ? { maxTaskEntries: Number(options.values.get('memory-max-task-entries') ?? 6) } : {}),
      } : undefined;
      result = await installOpenCodeProjectPlugin(projectRoot, required(options, 'module'), memory);
      if (memory) warning = 'OpenCode approved-memory retrieval is opt-in and uses the experimental messages-transform hook. Bounded user text is sent to the local Threadforge CLI for ranking; optional task snapshots require explicit task selection or unique-task discovery, remain advisory, and do not authorize actions.';
    } else if (family === 'opencode' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeOpenCodeProjectPlugin(projectRoot);
    } else if (family === 'cursor' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command']);
      result = await installCursorProjectHooks(projectRoot, required(options, 'command'));
    } else if (family === 'cursor' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeCursorProjectHooks(projectRoot);
    } else if (family === 'codex' && codexSubsystem === 'memory' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command', 'task-id', 'session-id', 'max-task-entries'], [], ['discover-active-task']);
      result = await installCodexProjectMemoryHook(projectRoot,
        taskAwareMemoryHookCommand(options, 'Codex', '--threadforge-memory=codex-v1'));
      warning = 'Codex UserPromptSubmit adds approved project memory as guidance only; review and trust the hook in Codex. Threadforge does not change hook trust or feature settings.';
    } else if (family === 'codex' && codexSubsystem === 'memory' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeCodexProjectMemoryHook(projectRoot);
    } else if (family === 'codex' && codexSubsystem === 'plan-gate' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command']);
      result = await installCodexProjectPlanGateHook(projectRoot, required(options, 'command'));
      warning = 'Codex PreToolUse plan gate is deny-only: it path-checks direct apply_patch and denies exec_command unless a valid governed_execution plan is confirmed; governed shell effects are not path-scoped, hook errors/timeouts can fail open, MCP and TOCTOU are not covered, and trust/feature settings remain unchanged.';
    } else if (family === 'codex' && codexSubsystem === 'plan-gate' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeCodexProjectPlanGateHook(projectRoot);
    } else if (family === 'codex' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command']);
      result = await installCodexProjectHooks(projectRoot, required(options, 'command'));
    } else if (family === 'codex' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeCodexProjectHooks(projectRoot);
    } else if (family === 'antigravity' && antigravitySubsystem === 'memory' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command']);
      if (options.values.get('scope') !== 'project') throw new Error('Antigravity memory requires explicit --scope project');
      result = await installAntigravityProjectMemoryHook({ command: required(options, 'command'), projectRoot });
      warning = 'Antigravity PreInvocation adds approved project memory as transient guidance once per execution; project hooks require user trust, and Threadforge does not change trust or tool authorization.';
    } else if (family === 'antigravity' && antigravitySubsystem === 'memory' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (options.values.get('scope') !== 'project') throw new Error('Antigravity memory requires explicit --scope project');
      result = await removeAntigravityProjectMemoryHook(projectRoot);
    } else if (family === 'antigravity' && antigravitySubsystem === 'plan-gate' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command']);
      if (options.values.get('scope') !== 'project') throw new Error('Antigravity plan-gate requires explicit --scope project');
      result = await installAntigravityProjectPlanGateHook({ command: required(options, 'command'), projectRoot });
      warning = 'Antigravity plan gate matches direct write_to_file/replace_file_content/multi_replace_file_content operations only. A deny blocks; an in-scope allow only delegates to the next hook/default policy and sets no permissionOverrides or auto_approve. It does not cover shell/MCP, sandbox execution, or path races. Installed agy CLI 1.4.2 smoke observed zero project-hook callbacks; do not rely on this gate in that CLI version. Antigravity IDE hook behavior remains unverified.';
    } else if (family === 'antigravity' && antigravitySubsystem === 'plan-gate' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (options.values.get('scope') !== 'project') throw new Error('Antigravity plan-gate requires explicit --scope project');
      result = await removeAntigravityProjectPlanGateHook(projectRoot);
    } else if (family === 'antigravity' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command']);
      if (antigravityScope !== 'user' && antigravityScope !== 'project') throw new Error('Antigravity --scope must be user or project');
      result = await installAntigravityHooks({
        scope: antigravityScope as AntigravityHookScope,
        ...(antigravityScope === 'project' ? { projectRoot } : {}),
        command: required(options, 'command'),
      });
    } else if (family === 'antigravity' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (antigravityScope !== 'user' && antigravityScope !== 'project') throw new Error('Antigravity --scope must be user or project');
      result = await removeAntigravityHooks({
        scope: antigravityScope as AntigravityHookScope,
        ...(antigravityScope === 'project' ? { projectRoot } : {}),
      });
    } else if (family === 'qwen' && qwenSubsystem === 'memory' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command', 'task-id', 'session-id', 'max-task-entries'], [], ['discover-active-task']);
      if (options.values.get('scope') !== 'project') throw new Error('Qwen memory requires explicit --scope project');
      result = await installQwenProjectMemoryHook({
        command: taskAwareMemoryHookCommand(options, 'Qwen', '--threadforge-memory=qwen-v1'), projectRoot,
      });
      warning = 'Qwen UserPromptExpansion adds approved memory and optionally selected task context as advisory guidance; project hooks require trusted-folder approval, and Threadforge does not change trust.';
    } else if (family === 'qwen' && qwenSubsystem === 'memory' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (options.values.get('scope') !== 'project') throw new Error('Qwen memory requires explicit --scope project');
      result = await removeQwenProjectMemoryHook(projectRoot);
    } else if (family === 'qwen' && qwenSubsystem === 'plan-gate' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command']);
      if (options.values.get('scope') !== 'project') throw new Error('Qwen plan-gate requires explicit --scope project');
      result = await installQwenProjectPlanGateHook({ command: required(options, 'command'), projectRoot });
      warning = 'Qwen plan gate is best-effort deny-only for direct write_file/edit tools, not a fail-closed security boundary: Qwen may continue after hook errors. It does not sandbox shell/MCP or close TOCTOU races, and requires Qwen trusted-folder approval. Threadforge does not change trust.';
    } else if (family === 'qwen' && qwenSubsystem === 'plan-gate' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (options.values.get('scope') !== 'project') throw new Error('Qwen plan-gate requires explicit --scope project');
      result = await removeQwenProjectPlanGateHook(projectRoot);
    } else if (family === 'copilot' && copilotSubsystem === 'plan-gate' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command', 'bash-command', 'powershell-command']);
      const commands = copilotPlanGateCommands(options);
      result = await installCopilotProjectPlanGateHook({ ...commands, projectRoot });
      warning = 'Copilot plan gate is deny-only for direct create/edit/apply_patch/str_replace_editor tools; it emits no allow decision, and GitHub documents command-hook timeouts as fail-open; it does not sandbox shell/MCP or close TOCTOU races, and leaves Copilot permissions and hook-disable settings unchanged.';
    } else if (family === 'copilot' && copilotSubsystem === 'plan-gate' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeCopilotProjectPlanGateHook(projectRoot);
    } else if (family === 'copilot' && copilotSubsystem === 'memory' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['project-root', 'command', 'bash-command', 'powershell-command']);
      const commands = copilotMemoryHookCommands(options);
      result = await installCopilotProjectMemoryHook({ ...commands, projectRoot });
      warning = 'Copilot CLI userPromptTransformed rewrites model-bound text and may persist in session history; the host may call it for preceding messages in a batched submission. Retrieval is advisory, and this installer does not change Copilot trust or permissions.';
    } else if (family === 'copilot' && copilotSubsystem === 'memory' && action === 'uninstall') {
      rejectUnexpected(options, ['project-root']);
      result = await removeCopilotProjectMemoryHook(projectRoot);
    } else if (family === 'qwen' && (action === 'install' || action === 'update')) {
      rejectUnexpected(options, ['scope', 'project-root', 'command']);
      if (qwenScope !== 'user' && qwenScope !== 'project') throw new Error('Qwen --scope must be user or project');
      result = await installQwenHooks({
        scope: qwenScope as QwenHookScope,
        ...(qwenScope === 'project' ? { projectRoot } : {}),
        command: required(options, 'command'),
      });
      if (qwenScope === 'project') warning = 'Qwen Code runs project hooks only in a trusted folder; Threadforge did not change project trust';
    } else if (family === 'qwen' && action === 'uninstall') {
      rejectUnexpected(options, ['scope', 'project-root']);
      if (qwenScope !== 'user' && qwenScope !== 'project') throw new Error('Qwen --scope must be user or project');
      result = await removeQwenHooks({
        scope: qwenScope as QwenHookScope,
        ...(qwenScope === 'project' ? { projectRoot } : {}),
      });
      if (qwenScope === 'project') warning = 'Qwen Code runs project hooks only in a trusted folder; Threadforge did not change project trust';
    } else if (family === 'mcp' && action === 'uninstall') {
      const hostName = required(options, 'host');
      const name = options.values.get('name') ?? 'threadforge';
      rejectUnexpected(options, ['project-root', 'host', 'name']);
      if (hostName === 'codex-cli' || hostName === 'chatgpt-desktop-codex' || hostName === 'codex-ide') {
        if (name !== 'threadforge') throw new Error('Codex project MCP currently manages the fixed server name threadforge');
        result = await removeCodexProjectMcpServer(projectRoot);
      } else {
        result = hostName === 'opencode'
          ? await removeManagedOpenCodeProjectMcpServer(projectRoot, name)
          : await removeManagedProjectMcpServer(projectRoot, hostName as ProjectMcpHost, name);
      }
    } else if (family === 'mcp' && (action === 'install' || action === 'update')) {
      const hostName = required(options, 'host');
      const command = required(options, 'command');
      const args = await buildManagedMcpArgs(projectRoot, options.repeated.get('arg') ?? []);
      const cwd = options.values.get('cwd');
      rejectUnexpected(options, ['project-root', 'host', 'command', 'cwd', 'name'], ['arg']);
      const name = options.values.get('name') ?? 'threadforge';
      if (hostName === 'codex-cli' || hostName === 'chatgpt-desktop-codex' || hostName === 'codex-ide') {
        if (cwd !== undefined) throw new Error('Codex project MCP config does not accept --cwd; use the project root as its working directory');
        if (name !== 'threadforge') throw new Error('Codex project MCP currently manages the fixed server name threadforge');
        result = action === 'install'
          ? await installCodexProjectMcpServer(projectRoot, command, args)
          : await updateCodexProjectMcpServer(projectRoot, command, args);
      } else if (hostName === 'opencode') {
        if (cwd !== undefined) throw new Error('OpenCode project MCP config does not accept --cwd; use the project root as its working directory');
        result = action === 'install'
          ? await installOpenCodeProjectMcpServer(projectRoot, name, command, args)
          : await updateManagedOpenCodeProjectMcpServer(projectRoot, name, command, args);
      } else if (hostName === 'zcode') {
        if (cwd !== undefined) throw new Error('ZCode project MCP config does not accept --cwd; use the project root as its working directory');
        if (name !== 'threadforge') throw new Error('ZCode project MCP currently manages the fixed server name threadforge');
        const server = { type: 'stdio' as const, command, args };
        result = action === 'install'
          ? await installProjectMcpServer(projectRoot, 'zcode', name, server)
          : await updateManagedProjectMcpServer(projectRoot, 'zcode', name, server);
      } else {
        const host = hostName as ProjectMcpHost;
      const server = {
        command, args, ...(cwd === undefined ? {} : { cwd }),
        ...(host === 'gemini-cli' ? { trust: false as const } : {}),
      };
      result = action === 'install'
        ? await installProjectMcpServer(projectRoot, host, name, server)
        : await updateManagedProjectMcpServer(projectRoot, host, name, server);
      }
    } else {
      throw new Error('unsupported adapter command');
    }
    stdout.write(`${JSON.stringify({ schema_version: 'threadforge.adapter-install.v1', family, action, result, ...(warning ? { warning } : {}) })}\n`);
    return 0;
  } catch (error) {
    const message = error instanceof Error ? error.message : 'adapter command failed';
    stderr.write(`${message}\n`);
    return 1;
  }
}

function parseOptions(args: string[]): ParsedOptions {
  const values = new Map<string, string>();
  const repeated = new Map<string, string[]>();
  const switches = new Set<string>();
  for (let index = 0; index < args.length; index++) {
    const key = args[index];
    if (!key?.startsWith('--') || key.length < 3) throw new Error('adapter options must use --name value form');
    const name = key.slice(2);
    if (name === 'print' || name === 'include-content' || name === 'watch' || name === 'acknowledge-local-source' ||
        name === 'memory-discover-active-task' || name === 'discover-active-task' ||
        name === 'json' || name === 'capture-content' || name === 'allow-remote') {
      if (switches.has(name)) throw new Error(`adapter option --${name} was repeated`);
      switches.add(name);
      continue;
    }
    const value = args[++index];
    if (name === 'arg') {
      if (value === undefined) throw new Error(`adapter option --${name} requires a value`);
      repeated.set(name, [...(repeated.get(name) ?? []), value]);
    } else {
      if (value === undefined || value.startsWith('--')) throw new Error(`adapter option --${name} requires a value`);
      if (values.has(name)) throw new Error(`adapter option --${name} was repeated`);
      values.set(name, value);
    }
  }
  return { values, repeated, switches };
}

function required(options: ParsedOptions, key: string): string {
  const value = options.values.get(key)?.trim();
  if (!value) throw new Error(`adapter option --${key} is required`);
  return value;
}

function taskAwareMemoryHookCommand(options: ParsedOptions, host: 'Claude Code' | 'Codex' | 'Gemini' | 'Qwen', runtimeMarker: string): string {
  const command = required(options, 'command');
  const taskId = options.values.get('task-id');
  const sessionId = options.values.get('session-id');
  const discoverActiveTask = options.switches.has('discover-active-task');
  const hasTaskSelection = taskId !== undefined || discoverActiveTask;
  if (sessionId !== undefined && taskId === undefined) throw new Error('--session-id requires --task-id');
  if (taskId !== undefined && discoverActiveTask) throw new Error('--task-id and --discover-active-task are mutually exclusive');
  if (options.values.has('max-task-entries') && !hasTaskSelection) {
    throw new Error('--max-task-entries requires --task-id or --discover-active-task');
  }
  if (taskId !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(taskId)) {
    throw new Error('--task-id must match [a-z][a-z0-9-]{0,63}');
  }
  if (sessionId !== undefined && !/^[a-z][a-z0-9-]{0,63}$/.test(sessionId)) {
    throw new Error('--session-id must match [a-z][a-z0-9-]{0,63}');
  }
  if (!hasTaskSelection) return command;
  if (/(?:^|\s|["'])--(?:task-id|session-id|discover-active-task|max-task-entries)(?=$|\s|["'=])/.test(command)) {
    throw new Error(`${host} memory command already contains task-resume options; choose either command arguments or installer options`);
  }
  const maxTaskEntries = Number(options.values.get('max-task-entries') ?? 6);
  if (!Number.isInteger(maxTaskEntries) || maxTaskEntries < 1 || maxTaskEntries > 20) {
    throw new Error('--max-task-entries must be between 1 and 20');
  }
  const selected = taskId === undefined ? '--discover-active-task' : `--task-id ${taskId}`;
  const session = sessionId === undefined ? '' : ` --session-id ${sessionId}`;
  const runtimeMarkers = [...command.matchAll(new RegExp(`(?:^|\\s)${runtimeMarker}(?=$|\\s)`, 'g'))];
  const runtimeMarkerMatch = runtimeMarkers[0];
  if (runtimeMarkers.length !== 1 || !runtimeMarkerMatch) {
    throw new Error(`${host} task selection requires exactly one unquoted Threadforge memory runtime marker`);
  }
  const markerIndex = (runtimeMarkerMatch.index ?? 0) + runtimeMarkerMatch[0].lastIndexOf(runtimeMarker);
  const selection = `${selected}${session} --max-task-entries ${maxTaskEntries} `;
  return `${command.slice(0, markerIndex)}${selection}${command.slice(markerIndex)}`;
}

function copilotPlanGateCommands(options: ParsedOptions): { bashCommand: string; powershellCommand: string } {
  const shared = options.values.get('command');
  const bashCommand = options.values.get('bash-command');
  const powershellCommand = options.values.get('powershell-command');
  if (shared && (bashCommand || powershellCommand)) {
    throw new Error('use either --command or both --bash-command and --powershell-command');
  }
  if (shared) return { bashCommand: shared, powershellCommand: shared };
  if (bashCommand && powershellCommand) return { bashCommand, powershellCommand };
  throw new Error('Copilot plan-gate requires --command or both --bash-command and --powershell-command');
}

function copilotMemoryHookCommands(options: ParsedOptions): { bashCommand: string; powershellCommand: string } {
  const shared = options.values.get('command');
  const bashCommand = options.values.get('bash-command');
  const powershellCommand = options.values.get('powershell-command');
  if (shared && (bashCommand || powershellCommand)) {
    throw new Error('use either --command or both --bash-command and --powershell-command');
  }
  if (shared) return { bashCommand: shared, powershellCommand: shared };
  if (bashCommand && powershellCommand) return { bashCommand, powershellCommand };
  throw new Error('Copilot memory requires --command or both --bash-command and --powershell-command');
}

function parseDeepSeekDuration(value: string): number {
  const match = /^(\d+)(ms|s|m|h)$/.exec(value.trim());
  if (!match) throw new Error('--interval must use a positive duration such as 30s, 5m, or 1h');
  const amount = Number(match[1]);
  const multiplier = match[2] === 'ms' ? 1 : match[2] === 's' ? 1_000 : match[2] === 'm' ? 60_000 : 3_600_000;
  const interval = amount * multiplier;
  if (!Number.isSafeInteger(interval) || interval < 5_000 || interval > 7 * 24 * 60 * 60 * 1_000) {
    throw new Error('--interval must be between 5s and 168h');
  }
  return interval;
}

function rejectUnexpected(options: ParsedOptions, allowed: string[], repeatedAllowed: string[] = [], switchesAllowed: string[] = []): void {
  const unexpected = [...options.values.keys()].find((key) => !allowed.includes(key)) ??
    [...options.repeated.keys()].find((key) => !repeatedAllowed.includes(key)) ??
    [...options.switches].find((key) => !switchesAllowed.includes(key));
  if (unexpected) throw new Error(`unsupported adapter option --${unexpected}`);
}

async function buildManagedMcpArgs(projectRoot: string, args: string[]): Promise<string[]> {
  if (args.length < 4 || args[0] !== 'mcp' || args[1] !== 'serve') {
    throw new Error('MCP args must begin with mcp serve and include --project-root <same-project-root>');
  }
  const rootIndexes = args.flatMap((value, index) => value === '--project-root' ? [index] : []);
  if (rootIndexes.length !== 1 || args.some((value) => value.startsWith('--project-root='))) {
    throw new Error('MCP args must contain exactly one --project-root setting');
  }
  const rootIndex = rootIndexes[0];
  if (!args[rootIndex + 1] || args[rootIndex + 1].startsWith('--')) {
    throw new Error('MCP args must include --project-root <same-project-root>');
  }
  const canonicalRoot = await realpath(projectRoot).catch(() => { throw new Error('project root must exist and be accessible'); });
  const configuredRoot = await realpath(args[rootIndex + 1]).catch(() => { throw new Error('MCP --project-root must exist and match the selected project root'); });
  const normalize = (value: string) => path.resolve(value).replace(/[\\/]+$/, '');
  const same = process.platform === 'win32'
    ? normalize(canonicalRoot).toLowerCase() === normalize(configuredRoot).toLowerCase()
    : normalize(canonicalRoot) === normalize(configuredRoot);
  if (!same) throw new Error('MCP --project-root must match the selected project root');
  return [...args.filter((value) => value !== managedMcpArgument), managedMcpArgument];
}

function usage(): string {
  return [
    'Usage:',
    '  npm run adapter -- devin install|update [--scope user|project] [--project-root <absolute-path>] [--command-template <command-with-{event}-and-marker>]',
    '  npm run adapter -- devin uninstall [--scope user|project] [--project-root <absolute-path>]',
    '  npm run adapter -- devin hook <SessionStart|UserPromptSubmit|PreToolUse|PermissionRequest|PostToolUse|Stop|SessionEnd> --threadforge-devin-hook=v1',
    '  npm run adapter -- devin-desktop install|update [--scope user|project] [--project-root <absolute-path>] [--command-template <command-with-{event}-and-marker>]',
    '  npm run adapter -- devin-desktop uninstall [--scope user|project] [--project-root <absolute-path>]',
    '  npm run adapter -- devin-desktop hook <pre_user_prompt|post_write_code|post_run_command|post_mcp_tool_use|post_read_code> --threadforge-devin-desktop-hook=v1',
    '  npm run adapter -- senpi install|update|uninstall|status [--scope user|project] [--project-root <absolute-path>] [--senpi-agent-dir <absolute-path>]',
    '  npm run adapter -- claude install|update --project-root <absolute-path> --command <shell-command-with-marker>',
    '  npm run adapter -- claude uninstall --project-root <absolute-path>',
    '  npm run adapter -- cursor install|update --project-root <absolute-path> --command <shell-command-with-marker>',
    '  npm run adapter -- cursor uninstall --project-root <absolute-path>',
    '  npm run adapter -- codex install|update --project-root <absolute-path> --command <shell-command-with-marker>',
    '  npm run adapter -- codex uninstall --project-root <absolute-path>',
    '  npm run adapter -- antigravity install|update [--scope user|project] [--project-root <absolute-path>] --command <shell-command-with-marker>',
    '  npm run adapter -- antigravity uninstall [--scope user|project] [--project-root <absolute-path>]',
    '  npm run adapter -- antigravity memory install|update --scope project --project-root <absolute-path> --command <threadforge-memory-hook-command-with-markers>',
    '  npm run adapter -- antigravity memory uninstall --scope project --project-root <absolute-path>',
    '  npm run adapter -- antigravity plan-gate install|update --scope project --project-root <absolute-path> --command <threadforge-task-plan-guard-command-with-markers>',
    '  npm run adapter -- antigravity plan-gate uninstall --scope project --project-root <absolute-path>',
    '  npm run adapter -- qwen install|update [--scope user|project] [--project-root <absolute-path>] --command <bash-command-with-marker>',
    '  npm run adapter -- qwen uninstall [--scope user|project] [--project-root <absolute-path>]',
    '  npm run adapter -- qwen memory install|update --scope project --project-root <absolute-path> --command <threadforge-memory-hook-command-with-markers> [--task-id <id> [--session-id <id>] | --discover-active-task] [--max-task-entries 1-20]',
    '  npm run adapter -- qwen memory uninstall --scope project --project-root <absolute-path>',
    '  npm run adapter -- qwen plan-gate install|update --scope project --project-root <absolute-path> --command <threadforge-deny-only-plan-gate-command-with-markers>',
    '  npm run adapter -- qwen plan-gate uninstall --scope project --project-root <absolute-path>',
    '  npm run adapter -- copilot plan-gate install|update --project-root <absolute-path> (--command <shared-shell-command> | --bash-command <command> --powershell-command <command>)',
    '  npm run adapter -- copilot plan-gate uninstall --project-root <absolute-path>',
    '  npm run adapter -- copilot memory install|update --project-root <absolute-path> (--command <shared-shell-command> | --bash-command <command> --powershell-command <command>)',
    '  npm run adapter -- copilot memory uninstall --project-root <absolute-path>',
    '  npm run adapter -- kimi install|update --command <shell-command-with-marker> [--kimi-code-home <absolute-path>]',
    '  npm run adapter -- kimi uninstall [--kimi-code-home <absolute-path>]',
    '  npm run adapter -- kiro install|update --scope user|project --command <shell-command-with-marker> [--project-root <absolute-path>] [--kiro-home <absolute-path>]',
    '  npm run adapter -- kiro uninstall [--scope user|project] [--project-root <absolute-path>] [--kiro-home <absolute-path>]',
    '  npm run adapter -- muse install|update --scope user|project --command <shell-command-with-marker> [--project-root <absolute-path>] [--xdg-config-home <absolute-path>]',
    '  npm run adapter -- muse uninstall --scope user|project [--project-root <absolute-path>] [--xdg-config-home <absolute-path>]',
    '  npm run adapter -- omp install|update --scope user|project [--project-root <absolute-path>] [--omp-agent-dir <absolute-path>]',
    '  npm run adapter -- omp uninstall --scope user|project [--project-root <absolute-path>] [--omp-agent-dir <absolute-path>]',
    '  npm run adapter -- openclaw install|update|uninstall --scope active-gateway [--openclaw-cli <command>] [--print] [--acknowledge-local-source]',
    '  npm run adapter -- openhands install|update --project-root <absolute-path> [--command-template <command-with-{event}-and-marker>]',
    '  npm run adapter -- openhands uninstall --project-root <absolute-path>',
    '  npm run adapter -- openhands mcp install|update|uninstall|status [--config-path <absolute-user-config-path>] [--command <threadforge-executable>] [--expected-command <current-executable>]',
    '  npm run adapter -- openhands hook <session_start|user_prompt_submit|pre_tool_use|post_tool_use|stop|session_end> --threadforge-openhands-hook=v1',
    '  npm run adapter -- hermes hooks print-config [--command-template <command-with-{event}-and-marker>]',
    '  npm run adapter -- hermes hook <event> --threadforge-hermes-hook=v1',
    '  npm run adapter -- hermes sessions status [--db-path <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-500>]',
    '  npm run adapter -- hermes sessions sync [--db-path <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-500>] [--max-messages <1-2000>] [--print] [--include-content]',
    '  npm run adapter -- grok hooks install|update --scope user|project --command-template <command-with-{event}-and-marker> [--project-root <absolute-path>]',
    '  npm run adapter -- grok hooks uninstall --scope user|project [--project-root <absolute-path>]',
    '  npm run adapter -- grok hook <session-start|prompt-submit|pre-tool|post-tool|post-tool-failure|stop|session-end> --threadforge-grok-hook=v1',
    '  npm run adapter -- grok sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]',
    '  npm run adapter -- grok sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]',
    '  npm run adapter -- dsh hook <SessionStart|UserPromptSubmit|PreToolUse|PostToolUse|Stop|SubagentStart|SubagentStop> --threadforge-dsh-hook=v1',
    '  npm run adapter -- dsh mcp print-patch --project-root <absolute-path> --command <threadforge-executable> --arg mcp --arg serve --arg --project-root --arg <same-project-root>',
    '  npm run adapter -- dsh sessions status [--dsh-home <absolute-path>|--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>] [--workspace <absolute-path>] [--session-id <id>]',
    '  npm run adapter -- dsh sessions sync [--dsh-home <absolute-path>|--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--workspace <absolute-path>] [--session-id <id>] [--print] [--include-content] [--watch --interval <5s-168h>]',
    '  npm run adapter -- claude-cowork otel print-config --endpoint <public-https-collector-url>',
    '  npm run adapter -- goose otel print-config [--endpoint <loopback-http-base-url>]',
    '  npm run adapter -- goose otel status [--config-path <config.yaml>] [--log-path <events.jsonl>] [--json]',
    '  npm run adapter -- goose otel validate [--since <duration>] [--config-path <config.yaml>] [--log-path <events.jsonl>] [--json]',
    '  npm run adapter -- goose sessions status [--db-path <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-500>]',
    '  npm run adapter -- goose sessions sync [--db-path <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-500>] [--max-messages <1-2000>] [--print] [--include-content]',
    '  npm run adapter -- vscode otel print-config [--endpoint <loopback-http-base-url>]',
    '  npm run adapter -- vscode otel status [--settings-path <settings.json>] [--log-path <events.jsonl>] [--json]',
    '  npm run adapter -- vscode otel validate [--since <duration>] [--settings-path <settings.json>] [--log-path <events.jsonl>] [--json]',
    '  npm run adapter -- openclaw otel print-config [--endpoint <url>] [--capture-content] [--allow-remote]',
    '  npm run adapter -- openclaw otel status [--log-path <events.jsonl>] [--json]',
    '  npm run adapter -- openclaw otel validate [--since <duration>] [--log-path <events.jsonl>] [--json]',
    '  npm run adapter -- openclaw sessions status [--state-path <absolute-path>]',
    '  npm run adapter -- openclaw sessions sync [--openclaw-cli <command>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-200>] [--max-messages <1-2000>] [--print] [--include-content]',
    '  npm run adapter -- claude sessions status [--projects-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-5000>]',
    '  npm run adapter -- claude sessions sync [--projects-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-5000>] [--print] [--include-content]',
    '  npm run adapter -- copilot sessions status [--copilot-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]',
    '  npm run adapter -- copilot sessions sync [--copilot-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]',
    '  npm run adapter -- cursor sessions status [--projects-dir <absolute-path>] [--global-db-path <absolute-path>] [--state-path <absolute-path>] [--max-files <1-5000>] [--max-composers <1-5000>]',
    '  npm run adapter -- cursor sessions sync [--projects-dir <absolute-path>] [--global-db-path <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-5000>] [--max-composers <1-5000>] [--print] [--include-content]',
    '  npm run adapter -- factory sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]',
    '  npm run adapter -- factory sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]',
    '  npm run adapter -- fx sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]',
    '  npm run adapter -- fx sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content]',
    '  npm run adapter -- pi sessions status [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-15000>]',
    '  npm run adapter -- pi sessions sync [--sessions-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-15000>] [--print] [--include-content]',
    '  npm run adapter -- prime-agent sessions status [--sessions-dir <absolute-path>] [--artifacts-dir <absolute-path>] [--state-path <absolute-path>] [--max-files <1-15000>]',
    '  npm run adapter -- prime-agent sessions sync [--sessions-dir <absolute-path>] [--artifacts-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-files <1-15000>] [--print] [--include-content]',
    '  npm run adapter -- opencode sessions status [--state-path <absolute-path>]',
    '  npm run adapter -- opencode sessions sync --project-root <absolute-path> [--opencode-cli <command>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-200>] [--print] [--include-content]',
    '  npm run adapter -- opencode plan-agent install|update|uninstall --project-root <absolute-path>',
    '  npm run adapter -- codex status [--codex-home <absolute-path>] [--state-path <absolute-path>]',
    '  npm run adapter -- codex sync [--codex-home <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--print] [--include-content]',
    '  npm run adapter -- gemini install|update|uninstall',
    '  npm run adapter -- telemetry install|update|uninstall --host <claude-code|codex-cli>',
    '  npm run adapter -- opencode install|update --project-root <absolute-path> --module <Threadforge-package-export-or-file-url> [--memory-project-id <id> [--memory-task-id <id> [--memory-session-id <id>] | --memory-discover-active-task] [--memory-max-items 1-32] [--memory-max-tokens 1-16000] [--memory-max-task-entries 1-20]]',
    '  npm run adapter -- opencode uninstall --project-root <absolute-path>',
    '  npm run adapter -- mcp install --host <claude-code|command-code|github-copilot|qoder|zcode|kimi-code|cursor|antigravity|gemini-cli|qwen-code|opencode|codex-cli|chatgpt-desktop-codex|codex-ide> --project-root <absolute-path> --command <executable> [--arg <value> ...] [--cwd <path>] [--name threadforge]',
    '  npm run adapter -- mcp update --host <claude-code|command-code|github-copilot|qoder|zcode|kimi-code|cursor|antigravity|gemini-cli|qwen-code|opencode|codex-cli|chatgpt-desktop-codex|codex-ide> --project-root <absolute-path> --command <executable> [--arg <value> ...] [--cwd <path>] [--name threadforge]',
    '  npm run adapter -- mcp uninstall --host <claude-code|command-code|github-copilot|qoder|zcode|kimi-code|cursor|antigravity|gemini-cli|qwen-code|opencode|codex-cli|chatgpt-desktop-codex|codex-ide> --project-root <absolute-path> [--name threadforge]',
    '  npm run adapter -- bootstrap mcp plan|install|update --project-root <absolute-path> --hosts <claude-code|command-code|github-copilot|qoder|zcode|kimi-code|cursor|antigravity|gemini-cli|qwen-code|codex-cli|chatgpt-desktop-codex|codex-ide|opencode,...> --command <executable> --arg mcp --arg serve --arg --project-root --arg <same-project-root>',
    '  npm run adapter -- bootstrap mcp uninstall --project-root <absolute-path> --hosts <comma-separated-supported-hosts>',
    '  npm run adapter -- bootstrap instructions plan|install|update|uninstall --project-root <absolute-path> --hosts <claude-code|command-code|cursor|antigravity|gemini-cli|codex-cli|chatgpt-desktop-codex|codex-ide|opencode|qwen-code|github-copilot|zcode|kimi-code|qoder|deepseek-harness|goose|cline,...>',
    '  npm run adapter -- cline skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- cline plugin install|update|uninstall|status [--scope user --cline-dir <absolute-path> | --scope project --project-root <absolute-path>]',
    '  npm run adapter -- cline mcp install --command <threadforge-executable> [--cline-dir <absolute-path> | --config-path <absolute-path>]',
    '  npm run adapter -- cline mcp update --expected-command <current-executable> --command <new-executable> [--cline-dir <absolute-path> | --config-path <absolute-path>]',
    '  npm run adapter -- cline mcp uninstall --expected-command <current-executable> [--cline-dir <absolute-path> | --config-path <absolute-path>]',
    '  npm run adapter -- cline mcp status [--cline-dir <absolute-path> | --config-path <absolute-path>]',
    '  npm run adapter -- cline sessions status [--cline-dir <absolute-path>] [--vscode-storage-dir <absolute-path>] [--state-path <absolute-path>] [--max-sessions <1-5000>]',
    '  npm run adapter -- cline sessions sync [--cline-dir <absolute-path>] [--vscode-storage-dir <absolute-path>] [--state-path <absolute-path>] [--endpoint <loopback-v1-events-url>] [--max-sessions <1-5000>] [--print] [--include-content] [--watch --interval <5s-168h>]',
    '  npm run adapter -- claude-code skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- aider skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- command-code skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- claude-code memory install|update --project-root <absolute-path> --command <threadforge-memory-hook-command-with-markers> [--task-id <id> [--session-id <id>] | --discover-active-task] [--max-task-entries 1-20]',
    '  npm run adapter -- claude-code memory uninstall --project-root <absolute-path>',
    '  npm run adapter -- claude-code plan-gate install|update --project-root <absolute-path> --command <threadforge-deny-only-plan-gate-command-with-markers>',
    '  npm run adapter -- claude-code plan-gate uninstall --project-root <absolute-path>',
    '  npm run adapter -- codex memory install|update --project-root <absolute-path> --command <threadforge-memory-hook-command-with-markers> [--task-id <id> [--session-id <id>] | --discover-active-task] [--max-task-entries 1-20]',
    '  npm run adapter -- codex memory uninstall --project-root <absolute-path>',
    '  npm run adapter -- codex plan-gate install|update --project-root <absolute-path> --command <threadforge-deny-only-plan-gate-command-with-markers>',
    '  npm run adapter -- codex plan-gate uninstall --project-root <absolute-path>',
    '  npm run adapter -- gemini memory install|update --project-root <absolute-path> --command <threadforge-memory-hook-command-with-markers> [--task-id <id> [--session-id <id>] | --discover-active-task] [--max-task-entries 1-20]',
    '  npm run adapter -- gemini memory uninstall --project-root <absolute-path>',
    '  npm run adapter -- gemini plan-gate install|update --project-root <absolute-path> --command <threadforge-deny-only-plan-gate-command-with-markers>',
    '  npm run adapter -- gemini plan-gate uninstall --project-root <absolute-path>',
    '  npm run adapter -- cursor memory install|update --project-root <absolute-path> --command <threadforge-memory-hook-command-with-markers>',
    '  npm run adapter -- cursor memory uninstall --project-root <absolute-path>',
    '  npm run adapter -- cursor plan-gate install|update --project-root <absolute-path> --command <threadforge-deny-only-plan-gate-command-with-markers>',
    '  npm run adapter -- cursor plan-gate uninstall --project-root <absolute-path>',
    '  npm run adapter -- cursor skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- devin skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- windsurf skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- devin-desktop skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- opencode skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- codex skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- gemini skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- antigravity skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- openhands skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- kimi skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- zcode skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- dsh skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- qoder skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- qoder commands install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- copilot skills install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
    '  npm run adapter -- copilot prompts install|uninstall --project-root <absolute-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>',
  ].join('\n');
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  process.exitCode = await runAdapterCLI(process.argv.slice(2), process.stdout, process.stderr);
}
