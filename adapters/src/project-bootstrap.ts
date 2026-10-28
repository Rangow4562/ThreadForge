import { createHash } from 'node:crypto';
import { realpath } from 'node:fs/promises';
import path from 'node:path';
import { isSameRealPathWithoutLinks } from './path-safety.ts';
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
import { looksCredentialLikeMcpValue } from './mcp-contract.ts';

export const projectBootstrapMcpTargets = {
  'claude-code': '.mcp.json',
  'command-code': '.mcp.json',
  'github-copilot': '.mcp.json',
  qoder: '.mcp.json',
  'kimi-code': path.join('.kimi-code', 'mcp.json'),
  cursor: path.join('.cursor', 'mcp.json'),
  antigravity: path.join('.agents', 'mcp_config.json'),
  'gemini-cli': path.join('.gemini', 'settings.json'),
  'qwen-code': path.join('.qwen', 'settings.json'),
  'codex-cli': path.join('.codex', 'config.toml'),
  'chatgpt-desktop-codex': path.join('.codex', 'config.toml'),
  'codex-ide': path.join('.codex', 'config.toml'),
  opencode: 'opencode.json',
} as const;

export type ProjectBootstrapMcpHost = keyof typeof projectBootstrapMcpTargets;
export type ProjectBootstrapAction = 'install' | 'update' | 'uninstall';

const hostNames = Object.keys(projectBootstrapMcpTargets) as ProjectBootstrapMcpHost[];
const maxHosts = hostNames.length;

type PlanHost = { host: ProjectBootstrapMcpHost; config_path: string; action: 'install_or_match' };
type HostResult = {
  host: ProjectBootstrapMcpHost;
  config_path: string;
  result: string;
  failure_code?: 'configuration_conflict_or_unavailable';
};

export async function runProjectBootstrapMcp(
  action: 'plan' | ProjectBootstrapAction,
  projectRoot: string,
  hosts: string[],
  command?: string,
  args?: string[],
): Promise<Record<string, unknown>> {
  const selectedHosts = validateHosts(hosts);
  const canonicalRoot = await canonicalProjectRoot(projectRoot);
  let normalizedCommand: string | undefined;
  let normalizedArgs: string[] | undefined;
  if (action !== 'uninstall') {
    normalizedCommand = validateCommand(command);
    normalizedArgs = await normalizeMcpArgs(canonicalRoot, args ?? []);
  }

  if (action === 'plan') {
    const planHosts: PlanHost[] = selectedHosts.map((host) => ({
      host,
      config_path: projectBootstrapMcpTargets[host],
      action: 'install_or_match',
    }));
    return {
      schema_version: 'threadforge.project-bootstrap-plan.v1',
      status: 'planned',
      project_root_sha256: sha256(canonicalRoot),
      hosts: planHosts,
      mutates_files: false,
      configuration_preflight: 'not_read',
      host_trust_changed: false,
      permissions_changed: false,
      telemetry_enabled: false,
      limitations: [
        'The plan does not inspect host files; install will refuse unowned or conflicting entries.',
        'Selected MCP configuration files may contain machine-specific executable paths; review them before committing.',
        'Hosts without a listed managed MCP adapter use the documented manual MCP or portable-skill fallback.',
      ],
    };
  }

  const results: HostResult[] = [];
  for (const host of selectedHosts) {
    try {
      const outcome = action === 'uninstall'
        ? await removeHost(host, canonicalRoot)
        : action === 'install'
          ? await installHost(host, canonicalRoot, normalizedCommand!, normalizedArgs!)
          : await updateHost(host, canonicalRoot, normalizedCommand!, normalizedArgs!);
      results.push({ host, config_path: projectBootstrapMcpTargets[host], result: outcome });
    } catch {
      results.push({
        host,
        config_path: projectBootstrapMcpTargets[host],
        result: 'failed',
        failure_code: 'configuration_conflict_or_unavailable',
      });
    }
  }
  const failed = results.some((entry) => entry.result === 'failed');
  return {
    schema_version: 'threadforge.project-bootstrap-result.v1',
    action,
    status: failed ? 'partial' : 'complete',
    hosts: results,
    atomic: false,
    partial_changes_possible: failed,
    host_trust_changed: false,
    permissions_changed: false,
    telemetry_enabled: false,
    guidance_only: true,
    execution_authorized: false,
  };
}

export function parseProjectBootstrapHosts(value: string): string[] {
  const hosts = value.split(',').map((host) => host.trim()).filter(Boolean);
  return validateHosts(hosts);
}

function validateHosts(hosts: string[]): ProjectBootstrapMcpHost[] {
  if (!Array.isArray(hosts) || hosts.length < 1 || hosts.length > maxHosts) {
    throw new Error(`select 1-${maxHosts} project MCP hosts explicitly`);
  }
  if (hosts.some((host) => typeof host !== 'string')) {
    throw new Error('project MCP host IDs must be strings');
  }
  const normalized = hosts.map((host) => host.trim().toLowerCase());
  if (normalized.some((host) => !hostNames.includes(host as ProjectBootstrapMcpHost))) {
    throw new Error(`supported project MCP hosts are: ${hostNames.join(', ')}`);
  }
  if (new Set(normalized).size !== normalized.length) throw new Error('project MCP host selection contains duplicates');
  return normalized as ProjectBootstrapMcpHost[];
}

async function canonicalProjectRoot(value: string): Promise<string> {
  if (!path.isAbsolute(value)) throw new Error('project root must be absolute');
  return realpath(value).catch(() => { throw new Error('project root must exist and be accessible'); });
}

function validateCommand(value: string | undefined): string {
  if (typeof value !== 'string' || !value.trim() || value !== value.trim() || value.length > 2048 || /[\r\n\0]/.test(value) || looksCredentialLikeMcpValue(value)) {
    throw new Error('MCP executable must be bounded and credential-free');
  }
  return value;
}

async function normalizeMcpArgs(projectRoot: string, args: string[]): Promise<string[]> {
  if (!Array.isArray(args) || args.length < 4 || args.length > 5 ||
      args.some((arg) => typeof arg !== 'string' || arg.length > 4096 || /[\r\n\0]/.test(arg) || looksCredentialLikeMcpValue(arg))) {
    throw new Error('bootstrap MCP arguments must be bounded, credential-free strings');
  }
  if (args[0] !== 'mcp' || args[1] !== 'serve' || args[2] !== '--project-root' ||
      (args.length === 5 && args[4] !== managedMcpArgument)) {
    throw new Error('bootstrap only supports the standard mcp serve --project-root command');
  }
  const configuredRoot = await canonicalProjectRoot(args[3]!);
  if (!await isSameRealPathWithoutLinks(projectRoot, configuredRoot)) throw new Error('MCP --project-root must match the selected project root');
  return ['mcp', 'serve', '--project-root', projectRoot, managedMcpArgument];
}

function sha256(value: string): string {
  return createHash('sha256').update(value).digest('hex');
}

async function installHost(host: ProjectBootstrapMcpHost, root: string, command: string, args: string[]): Promise<string> {
  if (isCodexProjectMcpHost(host)) return installCodexProjectMcpServer(root, command, args);
  if (host === 'opencode') return installOpenCodeProjectMcpServer(root, 'threadforge', command, args);
  return installProjectMcpServer(root, host as ProjectMcpHost, 'threadforge', {
    command, args, ...(host === 'gemini-cli' ? { trust: false as const } : {}),
  });
}

async function updateHost(host: ProjectBootstrapMcpHost, root: string, command: string, args: string[]): Promise<string> {
  if (isCodexProjectMcpHost(host)) return updateCodexProjectMcpServer(root, command, args);
  if (host === 'opencode') return updateManagedOpenCodeProjectMcpServer(root, 'threadforge', command, args);
  return updateManagedProjectMcpServer(root, host as ProjectMcpHost, 'threadforge', {
    command, args, ...(host === 'gemini-cli' ? { trust: false as const } : {}),
  });
}

async function removeHost(host: ProjectBootstrapMcpHost, root: string): Promise<string> {
  if (isCodexProjectMcpHost(host)) return removeCodexProjectMcpServer(root);
  if (host === 'opencode') return removeManagedOpenCodeProjectMcpServer(root, 'threadforge');
  return removeManagedProjectMcpServer(root, host as ProjectMcpHost, 'threadforge');
}

function isCodexProjectMcpHost(host: ProjectBootstrapMcpHost): boolean {
  return host === 'codex-cli' || host === 'chatgpt-desktop-codex' || host === 'codex-ide';
}
