import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

export type OpenClawPluginAction = 'install' | 'update' | 'uninstall';
export interface OpenClawPluginOptions {
  scope: 'active-gateway';
  openclawCli?: string;
  acknowledgeLocalSource?: boolean;
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
  runner: (command: OpenClawCommand) => Promise<void> = runCommand,
): Promise<'installed' | 'reloaded' | 'uninstalled'> {
  const commands = buildOpenClawPluginCommands(action, options);
  for (let index = 0; index < commands.length; index++) {
    try {
      await runner(commands[index]!);
    } catch (error) {
      if (action === 'install' && index > 0) {
        throw new Error(`OpenClaw installed the Threadforge plugin but could not enable it; run "openclaw plugins enable ${pluginId}" after resolving the CLI error`);
      }
      const detail = error instanceof Error ? error.message : 'unknown OpenClaw CLI error';
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
  if (action !== 'install' && options.acknowledgeLocalSource) {
    throw new Error('--acknowledge-local-source is valid only with OpenClaw install');
  }
}

async function runCommand(command: OpenClawCommand): Promise<void> {
  if (process.platform === 'win32') {
    await runPowerShell(command);
    return;
  }
  await new Promise<void>((resolve, reject) => {
    const child = spawn(command.executable, command.args, { stdio: 'inherit', windowsHide: true });
    child.once('error', (error) => reject(new Error(`could not start ${command.executable}: ${error.message}`)));
    child.once('close', (code, signal) => {
      if (code === 0) resolve();
      else reject(new Error(`${command.executable} exited ${signal ? `from ${signal}` : `with status ${code}`}`));
    });
  });
}

async function runPowerShell(command: OpenClawCommand): Promise<void> {
  const quote = (value: string): string => {
    if (/[\0\r\n]/.test(value)) throw new Error('OpenClaw CLI argument contains invalid characters');
    return `'${value.replace(/'/g, "''")}'`;
  };
  const invocation = [command.executable, ...command.args].map(quote).join(' ');
  const script = `& ${invocation}\n$threadforgeExit = $LASTEXITCODE\nif ($null -eq $threadforgeExit) { $threadforgeExit = 0 }\nexit $threadforgeExit`;
  const encoded = Buffer.from(script, 'utf16le').toString('base64');
  await new Promise<void>((resolve, reject) => {
    const child = spawn('powershell.exe', ['-NoProfile', '-EncodedCommand', encoded], { stdio: 'inherit', windowsHide: true });
    child.once('error', (error) => reject(new Error(`could not start PowerShell for OpenClaw CLI: ${error.message}`)));
    child.once('close', (code, signal) => {
      if (code === 0) resolve();
      else reject(new Error(`OpenClaw CLI exited ${signal ? `from ${signal}` : `with status ${code}`}`));
    });
  });
}
