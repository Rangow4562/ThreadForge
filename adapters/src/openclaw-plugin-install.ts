import { fileURLToPath } from 'node:url';
import { runBoundedCommand } from './bounded-command.ts';

export const defaultOpenClawCommandTimeoutMs = 120_000;

export type OpenClawPluginAction = 'install' | 'update' | 'uninstall';
export interface OpenClawPluginOptions {
  scope: 'active-gateway';
  openclawCli?: string;
  acknowledgeLocalSource?: boolean;
  timeoutMs?: number;
}
export interface OpenClawCommand {
  executable: string;
  args: string[];
}

const pluginId = 'threadforge-observability';
const packageDirectory = fileURLToPath(new URL('../openclaw-plugin/', import.meta.url));

/** Build the exact OpenClaw CLI operations without editing OpenClaw's JSON5 configuration. */
export function buildOpenClawPluginCommands(action: OpenClawPluginAction, options: OpenClawPluginOptions): OpenClawCommand[] {
  validateOptions(action, options);
  const executable = options.openclawCli?.trim() || 'openclaw';
  if (/[\0\r\n]/.test(executable)) throw new Error('OpenClaw CLI path contains invalid characters');
  if (action === 'install') {
    return [
      {
        executable,
        args: ['plugins', 'install', '--link', ...(options.acknowledgeLocalSource ? ['--force'] : []), packageDirectory],
      },
      { executable, args: ['plugins', 'enable', pluginId] },
    ];
  }
  if (action === 'update') return [{ executable, args: ['plugins', 'reload', pluginId] }];
  return [{ executable, args: ['plugins', 'uninstall', pluginId, '--keep-files'] }];
}

/** Executes OpenClaw's own scoped plugin-management commands and preserves its confirmation flow. */
export async function runOpenClawPluginAction(
  action: OpenClawPluginAction,
  options: OpenClawPluginOptions,
  runner: (command: OpenClawCommand, timeoutMs: number) => Promise<void> = runCommand,
): Promise<'installed' | 'reloaded' | 'uninstalled'> {
  const commands = buildOpenClawPluginCommands(action, options);
  const timeoutMs = options.timeoutMs ?? defaultOpenClawCommandTimeoutMs;
  for (let index = 0; index < commands.length; index++) {
    try {
      await runner(commands[index]!, timeoutMs);
    } catch (error) {
      if (action === 'install' && index > 0) {
        throw new Error(`OpenClaw installed the Threadforge plugin but could not enable it; run "openclaw plugins enable ${pluginId}" after resolving the CLI error`);
      }
      const detail = error instanceof Error ? error.message : 'unknown OpenClaw CLI error';
      if (/timed out/i.test(detail)) {
        throw new Error(`OpenClaw plugin ${action} timed out; the CLI operation may have partially completed. Inspect "openclaw plugins list" before retrying. ${detail}`);
      }
      throw new Error(`OpenClaw plugin ${action} failed: ${detail}`);
    }
  }
  if (action === 'install') return 'installed';
  if (action === 'update') return 'reloaded';
  return 'uninstalled';
}

export function openClawPluginPackagePath(): string {
  return packageDirectory;
}

function validateOptions(action: string, options: OpenClawPluginOptions): void {
  if (!['install', 'update', 'uninstall'].includes(action)) throw new Error('unsupported OpenClaw plugin action');
  if (!options || options.scope !== 'active-gateway') throw new Error('OpenClaw requires the explicit --scope active-gateway choice');
  if (options.timeoutMs !== undefined && (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs < 1_000 || options.timeoutMs > 600_000)) {
    throw new Error('OpenClaw CLI timeout must be between 1000 and 600000 ms');
  }
  if (action !== 'install' && options.acknowledgeLocalSource) {
    throw new Error('--acknowledge-local-source is valid only with OpenClaw install');
  }
}

async function runCommand(command: OpenClawCommand, timeoutMs: number): Promise<void> {
  if (process.platform === 'win32') {
    await runPowerShell(command, timeoutMs);
    return;
  }
  await runBoundedCommand(command.executable, command.args, { label: 'OpenClaw CLI', timeoutMs });
}

async function runPowerShell(command: OpenClawCommand, timeoutMs: number): Promise<void> {
  const quote = (value: string): string => {
    if (/[\0\r\n]/.test(value)) throw new Error('OpenClaw CLI argument contains invalid characters');
    return `'${value.replace(/'/g, "''")}'`;
  };
  const invocation = [command.executable, ...command.args].map(quote).join(' ');
  const script = `& ${invocation}\n$threadforgeExit = $LASTEXITCODE\nif ($null -eq $threadforgeExit) { $threadforgeExit = 0 }\nexit $threadforgeExit`;
  const encoded = Buffer.from(script, 'utf16le').toString('base64');
  await runBoundedCommand('powershell.exe', ['-NoProfile', '-EncodedCommand', encoded], {
    label: 'OpenClaw CLI',
    timeoutMs,
  });
}
