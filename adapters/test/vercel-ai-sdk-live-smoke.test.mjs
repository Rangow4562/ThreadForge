import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

const smokeScript = fileURLToPath(new URL('../scripts/vercel-ai-sdk-live-smoke.mjs', import.meta.url));

test('optional Vercel AI SDK live-smoke entrypoint has valid Node syntax', () => {
  const result = spawnSync(process.execPath, ['--check', smokeScript], { encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr || result.stdout);
});
