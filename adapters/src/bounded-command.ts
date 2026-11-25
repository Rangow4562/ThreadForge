import { spawn, type ChildProcess } from 'node:child_process';

export type BoundedCommandOptions = {
  label: string;
  timeoutMs: number;
  captureStdout?: boolean;
  maxStdoutBytes?: number;
  cwd?: string;
};

/** Run a host CLI with a wall-clock bound and terminate its process tree on Windows. */
export function runBoundedCommand(
  executable: string,
  args: string[],
  options: BoundedCommandOptions,
): Promise<string> {
  const { label, timeoutMs, captureStdout = false, maxStdoutBytes } = options;
  if (!label.trim()) throw new Error('command label is required');
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1) throw new Error('command timeout must be a positive integer');
  if (captureStdout && (!Number.isSafeInteger(maxStdoutBytes) || (maxStdoutBytes ?? 0) < 1)) {
    throw new Error('captured command output requires a positive byte limit');
  }

  return new Promise<string>((resolve, reject) => {
    let child;
    try {
      child = spawn(executable, args, {
        cwd: options.cwd,
        stdio: captureStdout ? ['ignore', 'pipe', 'ignore'] : 'inherit',
        detached: process.platform !== 'win32',
        windowsHide: true,
      });
    } catch {
      reject(new Error(`${label} could not be started`));
      return;
    }

    const chunks: Buffer[] = [];
    let outputBytes = 0;
    let settled = false;
    let terminating = false;
    let timeoutHandle: ReturnType<typeof setTimeout> | undefined;
    const onInterrupt = () => forwardParentSignal('SIGINT');
    const onTerminate = () => forwardParentSignal('SIGTERM');

    const removeSignalHandlers = () => {
      process.removeListener('SIGINT', onInterrupt);
      process.removeListener('SIGTERM', onTerminate);
    };

    const finish = (error?: Error, output = '') => {
      if (settled) return;
      settled = true;
      if (timeoutHandle) clearTimeout(timeoutHandle);
      removeSignalHandlers();
      if (error) reject(error);
      else resolve(output);
    };

    const terminate = (message: string) => {
      if (terminating || settled) return;
      terminating = true;
      void terminateProcessTree(child).finally(() => finish(new Error(message)));
    };

    const forwardParentSignal = (signal: 'SIGINT' | 'SIGTERM') => {
      if (terminating || settled) return;
      terminating = true;
      void terminateProcessTree(child).finally(() => {
        removeSignalHandlers();
        try { process.kill(process.pid, signal); } catch { finish(new Error(`${label} interrupted`)); }
      });
    };

    process.once('SIGINT', onInterrupt);
    process.once('SIGTERM', onTerminate);

    timeoutHandle = setTimeout(() => {
      terminate(`${label} timed out after ${timeoutMs} ms`);
    }, timeoutMs);

    if (captureStdout && child.stdout) {
      child.stdout.on('data', (chunk: Buffer) => {
        if (settled || terminating) return;
        outputBytes += chunk.byteLength;
        if (outputBytes > (maxStdoutBytes ?? 0)) {
          terminate(`${label} output exceeded the local limit`);
          return;
        }
        chunks.push(Buffer.from(chunk));
      });
    }

    child.once('error', () => finish(new Error(`${label} could not be started`)));
    child.once('close', (code, signal) => {
      if (terminating) return;
      if (code === 0) finish(undefined, Buffer.concat(chunks).toString('utf8'));
      else finish(new Error(`${label} exited ${signal ? `from ${signal}` : `with status ${code}`}`));
    });
  });
}

async function terminateProcessTree(child: ChildProcess): Promise<void> {
  if (!child.pid) {
    child.kill();
    return;
  }

  if (process.platform !== 'win32') {
    try { process.kill(-child.pid, 'SIGTERM'); } catch { child.kill('SIGTERM'); }
    await new Promise((resolve) => setTimeout(resolve, 1_000));
    try { process.kill(-child.pid, 'SIGKILL'); } catch { child.kill('SIGKILL'); }
    return;
  }

  await new Promise<void>((resolve) => {
    let completed = false;
    const finish = () => {
      if (completed) return;
      completed = true;
      clearTimeout(fallback);
      resolve();
    };
    const fallback = setTimeout(() => {
      try { child.kill(); } catch { /* The OS may have already reaped the process. */ }
      finish();
    }, 3_000);
    const killer = spawn('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], {
      stdio: 'ignore',
      windowsHide: true,
    });
    killer.once('error', () => {
      try { child.kill(); } catch { /* Fall back to the direct child if taskkill is unavailable. */ }
      finish();
    });
    killer.once('close', () => {
      if (child.exitCode === null && child.signalCode === null) {
        try { child.kill(); } catch { /* The process tree may already be gone. */ }
      }
      finish();
    });
  });
}
