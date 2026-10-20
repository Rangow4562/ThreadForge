import { appendFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const [auditPath, executable, ...args] = process.argv.slice(2);
const maxInputBytes = 1 << 20;

async function readInput() {
  const chunks = [];
  let length = 0;
  for await (const chunk of process.stdin) {
    length += chunk.length;
    if (length > maxInputBytes) throw new Error('input_too_large');
    chunks.push(chunk);
  }
  return Buffer.concat(chunks);
}

function writeAudit(record) {
  if (!auditPath) throw new Error('audit_path_missing');
  appendFileSync(auditPath, `${JSON.stringify(record)}\n`, { encoding: 'utf8', mode: 0o600 });
}

try {
  if (!executable) throw new Error('guard_executable_missing');
  const input = await readInput();
  const child = spawnSync(executable, args, {
    input,
    windowsHide: true,
    maxBuffer: 4 << 20,
  });
  const stdout = Buffer.isBuffer(child.stdout) ? child.stdout : Buffer.alloc(0);
  const stderr = Buffer.isBuffer(child.stderr) ? child.stderr : Buffer.alloc(0);
  let decision = 'invalid';
  try {
    const result = JSON.parse(stdout.toString('utf8'));
    if (result?.decision === 'allow' || result?.decision === 'deny') decision = result.decision;
  } catch { /* Record only that the guard output was not a recognized decision. */ }
  writeAudit({
    decision,
    exit_code: Number.isInteger(child.status) ? child.status : null,
    request_bytes: input.length,
    response_bytes: stdout.length,
  });
  if (stdout.length > 0) process.stdout.write(stdout);
  if (stderr.length > 0) process.stderr.write(stderr);
  if (child.error) {
    process.stderr.write('Threadforge Antigravity plan-gate smoke runner failed.\n');
    process.exitCode = 1;
  } else process.exitCode = Number.isInteger(child.status) ? child.status : 1;
} catch {
  try { writeAudit({ decision: 'invalid', exit_code: 1, request_bytes: 0, response_bytes: 0 }); } catch { /* Keep failure content-free. */ }
  process.stderr.write('Threadforge Antigravity plan-gate smoke runner failed.\n');
  process.exitCode = 1;
}
