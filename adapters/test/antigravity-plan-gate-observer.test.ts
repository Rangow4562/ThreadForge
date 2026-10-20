import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const observerPath = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../scripts/antigravity-plan-gate-observer.mjs');

test('Antigravity plan-gate observer forwards the decision and audits only content-free metadata', () => {
  const root = mkdtempSync(path.join(tmpdir(), 'threadforge-antigravity-plan-gate-observer-'));
  const auditPath = path.join(root, 'observer.jsonl');
  const privateInput = JSON.stringify({ prompt: 'private prompt', TargetFile: 'private/path.txt' });
  try {
    const result = spawnSync(process.execPath, [
      observerPath,
      auditPath,
      process.execPath,
      '-e',
      "process.stdout.write(JSON.stringify({decision:'deny'}))",
    ], { input: privateInput, encoding: 'utf8', timeout: 5_000 });

    assert.equal(result.status, 0);
    assert.equal(result.stdout, '{"decision":"deny"}');
    assert.equal(result.stderr, '');
    const auditText = readFileSync(auditPath, 'utf8');
    const audit = JSON.parse(auditText);
    assert.deepEqual(Object.keys(audit).sort(), ['decision', 'exit_code', 'request_bytes', 'response_bytes']);
    assert.equal(audit.decision, 'deny');
    assert.equal(audit.exit_code, 0);
    assert.equal(audit.request_bytes, Buffer.byteLength(privateInput));
    assert.equal(audit.response_bytes, Buffer.byteLength(result.stdout));
    assert.doesNotMatch(auditText, /private prompt|private\/path\.txt/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
