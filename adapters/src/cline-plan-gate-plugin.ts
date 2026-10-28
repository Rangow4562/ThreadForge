import { spawn } from 'node:child_process';
import { isAbsolute } from 'node:path';

const maxInputBytes = 1 << 20;
const maxOutputBytes = 64 << 10;
const maxReason = 'Threadforge could not verify this Cline file operation against the configured task plan; the operation was skipped.';
const denyReason = 'Threadforge withheld this Cline file operation because the task plan or implementation scope did not authorize it.';

export interface ThreadforgeClinePlanGateOptions {
  taskId: string;
  projectId: string;
  projectRoot: string;
  databasePath?: string;
  threadforgeCommand?: string;
  timeoutMs?: number;
}

export interface ClineTaskPlanGuardInput {
  hook_event_name: 'BeforeTool';
  tool_name: 'editor' | 'apply_patch';
  cwd: string;
  tool_input: Record<string, unknown>;
}

export interface ClineTaskPlanGuardResult {
  decision: 'allow' | 'deny';
}

export type ThreadforgeClinePlanGateInvoker = (
  options: Readonly<ThreadforgeClinePlanGateOptions>,
  input: ClineTaskPlanGuardInput,
) => Promise<unknown>;

/** Create an opt-in, deny-only Cline CLI beforeTool task-plan gate. */
export function createThreadforgeClinePlanGatePlugin(
  options: ThreadforgeClinePlanGateOptions,
  invokeGuard: ThreadforgeClinePlanGateInvoker = runTaskPlanGuard,
) {
  const configured = validateOptions(options);
  let workspaceRoot = '';

  return {
    name: 'threadforge-task-plan-gate',
    manifest: { capabilities: ['hooks'] },
    setup(_api: unknown, context: unknown): void {
      const workspaceInfo = asRecord(asRecord(context).workspaceInfo);
      workspaceRoot = typeof workspaceInfo.rootPath === 'string' && isAbsolute(workspaceInfo.rootPath)
        ? workspaceInfo.rootPath
        : '';
    },
    hooks: {
      async beforeTool(context: unknown): Promise<{ skip: true; reason: string } | undefined> {
        try {
          const call = asRecord(asRecord(context).toolCall);
          const toolName = call.toolName;
          if (toolName !== 'editor' && toolName !== 'apply_patch') return undefined;
          if (!workspaceRoot) return { skip: true, reason: maxReason };

          const callbackInput = asRecord(context).input;
          const rawInput = asRecord(callbackInput);
          const toolInput = toolName === 'editor'
            ? { ...(typeof rawInput.path === 'string' ? { path: rawInput.path } : {}) }
            : { ...(typeof callbackInput === 'string' ? { input: callbackInput } :
              typeof rawInput.input === 'string' ? { input: rawInput.input } : {}) };
          const input: ClineTaskPlanGuardInput = {
            hook_event_name: 'BeforeTool',
            tool_name: toolName,
            cwd: workspaceRoot,
            tool_input: toolInput,
          };
          if (Buffer.byteLength(JSON.stringify(input), 'utf8') > maxInputBytes) {
            return { skip: true, reason: maxReason };
          }

          const result = await invokeGuard(configured, input);
          if (isGuardResult(result) && result.decision === 'allow') return undefined;
          return { skip: true, reason: isGuardResult(result) && result.decision === 'deny' ? denyReason : maxReason };
        } catch {
          return { skip: true, reason: maxReason };
        }
      },
    },
  };
}

export function validateThreadforgeClinePlanGateOptions(
  options: ThreadforgeClinePlanGateOptions,
): ThreadforgeClinePlanGateOptions {
  return validateOptions(options);
}

async function runTaskPlanGuard(
  options: Readonly<ThreadforgeClinePlanGateOptions>,
  input: ClineTaskPlanGuardInput,
): Promise<ClineTaskPlanGuardResult> {
  const args = [
    'task', 'plan', 'guard', '--host', 'cline-cli',
    '--task-id', options.taskId,
    '--project-id', options.projectId,
    '--project-root', options.projectRoot,
    '--threadforge-managed-cline-plan-gate=v1',
    '--threadforge-plan-gate=cline-v1',
  ];
  if (options.databasePath) args.push('--db', options.databasePath);

  const encodedInput = Buffer.from(JSON.stringify(input), 'utf8');
  if (encodedInput.byteLength > maxInputBytes) throw new Error('task-plan guard input exceeds the local size limit');

  const output = await new Promise<Buffer>((resolve, reject) => {
    const child = spawn(options.threadforgeCommand ?? 'threadforge', args, {
      cwd: options.projectRoot,
      windowsHide: true,
      stdio: ['pipe', 'pipe', 'ignore'],
    });
    const chunks: Buffer[] = [];
    let outputBytes = 0;
    let settled = false;
    const finish = (error?: Error, result?: Buffer): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (error) reject(error);
      else resolve(result ?? Buffer.alloc(0));
    };
    const timer = setTimeout(() => {
      child.kill();
      finish(new Error('task-plan guard timed out'));
    }, options.timeoutMs ?? 5000);
    child.stdout.on('data', (chunk: Buffer) => {
      outputBytes += chunk.byteLength;
      if (outputBytes > maxOutputBytes) {
        child.kill();
        finish(new Error('task-plan guard response exceeds the local size limit'));
        return;
      }
      chunks.push(Buffer.from(chunk));
    });
    child.on('error', () => finish(new Error('task-plan guard could not start')));
    child.on('close', (code) => {
      if (code !== 0) {
        finish(new Error('task-plan guard returned an unsuccessful status'));
        return;
      }
      finish(undefined, Buffer.concat(chunks));
    });
    child.stdin.on('error', () => {
      child.kill();
      finish(new Error('task-plan guard input could not be sent'));
    });
    child.stdin.end(encodedInput);
  });

  const parsed: unknown = JSON.parse(output.toString('utf8'));
  if (!isGuardResult(parsed)) throw new Error('task-plan guard returned an invalid decision');
  return parsed;
}

function validateOptions(options: ThreadforgeClinePlanGateOptions): ThreadforgeClinePlanGateOptions {
  if (!isRecord(options)) throw new Error('Cline task-plan gate configuration is invalid');
  for (const [name, value] of [['taskId', options.taskId], ['projectId', options.projectId]] as const) {
    if (typeof value !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value)) {
      throw new Error(`Cline task-plan gate ${name} is invalid`);
    }
  }
  if (typeof options.projectRoot !== 'string' || !isAbsolute(options.projectRoot) || isRemotePath(options.projectRoot) || hasTraversal(options.projectRoot)) {
    throw new Error('Cline task-plan gate projectRoot must be an absolute local path');
  }
  if (options.databasePath !== undefined &&
      (typeof options.databasePath !== 'string' || !isAbsolute(options.databasePath) || isRemotePath(options.databasePath) || hasTraversal(options.databasePath))) {
    throw new Error('Cline task-plan gate databasePath must be an absolute local path');
  }
  if (options.threadforgeCommand !== undefined &&
      (typeof options.threadforgeCommand !== 'string' || options.threadforgeCommand.trim() === '' ||
       options.threadforgeCommand.length > 4096 || /[\u0000-\u001f\u007f]/.test(options.threadforgeCommand))) {
    throw new Error('Cline task-plan gate threadforgeCommand is invalid');
  }
  if (options.timeoutMs !== undefined && (!Number.isInteger(options.timeoutMs) || options.timeoutMs < 100 || options.timeoutMs > 10_000)) {
    throw new Error('Cline task-plan gate timeoutMs must be between 100 and 10000');
  }
  return Object.freeze({ ...options });
}

function isGuardResult(value: unknown): value is ClineTaskPlanGuardResult {
  return isRecord(value) && (value.decision === 'allow' || value.decision === 'deny');
}

function asRecord(value: unknown): Record<string, unknown> {
  return isRecord(value) ? value : {};
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isRemotePath(value: string): boolean {
  return /^(?:\\\\|\/\/)/.test(value);
}

function hasTraversal(value: string): boolean {
  return value.split(/[\\/]/).some((segment) => segment === '.' || segment === '..');
}
