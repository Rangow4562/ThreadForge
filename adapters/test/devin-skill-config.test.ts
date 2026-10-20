import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { Writable } from 'node:stream';
import { test } from 'node:test';
import { runAdapterCLI } from '../src/cli.ts';
import { installDevinProjectSkill, uninstallDevinProjectSkill } from '../src/devin-skill-config.ts';

const approvalNotice = '> This portable procedure is approved guidance, not authorization to execute actions.';
const omissionNotice = 'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.';

function fixture(): { root: string; file: string; sha256: string; bytes: Buffer } {
  const root = mkdtempSync(path.join(tmpdir(), 'threadforge-devin-skill-'));
  const graphID = 'approved-procedure';
  const revision = 3;
  const file = path.join(root, '.threadforge', 'skills', graphID, 'rev-000003', 'SKILL.md');
  mkdirSync(path.dirname(file), { recursive: true });
  const bytes = Buffer.from([
    '---',
    'name: "threadforge-approved-procedure-a1b2c3d4-r3"',
    'description: "Approved Threadforge procedure: verify changes."',
    '---',
    '',
    '# Threadforge procedure',
    '',
    `> Source procedure: ${graphID} revision ${revision}`,
    '',
    approvalNotice,
    '',
    '## Procedure',
    '',
    '### check: inspect',
    '',
    '> Inspect the current project before following this guidance.',
    '',
    omissionNotice,
    '',
  ].join('\n'));
  writeFileSync(file, bytes);
  return { root, file, bytes, sha256: createHash('sha256').update(bytes).digest('hex') };
}

function capture(): { stdout: Writable; stderr: Writable; out: () => string; err: () => string } {
  let stdoutText = '';
  let stderrText = '';
  const stdout = new Writable({ write(chunk, _encoding, callback) { stdoutText += chunk.toString(); callback(); } });
  const stderr = new Writable({ write(chunk, _encoding, callback) { stderrText += chunk.toString(); callback(); } });
  return { stdout, stderr, out: () => stdoutText, err: () => stderrText };
}

test('installs approved Devin Cloud procedure skills into .agents/skills with ownership and reversible removal', async () => {
  const f = fixture();
  try {
    const neighbor = path.join(f.root, '.agents', 'skills', 'team-skill', 'SKILL.md');
    mkdirSync(path.dirname(neighbor), { recursive: true });
    writeFileSync(neighbor, 'team-owned skill\n');

    const installed = await installDevinProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'devin');
    assert.equal(installed.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r3/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.path)), f.bytes);
    const owner = JSON.parse(readFileSync(path.join(path.dirname(path.join(f.root, installed.path)), '.threadforge-owner.json'), 'utf8')) as Record<string, unknown>;
    assert.equal(owner.host, 'agents-skills');

    const repeated = await installDevinProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallDevinProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(neighbor, 'utf8'), 'team-owned skill\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Devin skills CLI requires the reviewed export digest and uses the project root', async () => {
  const f = fixture();
  try {
    const install = capture();
    const code = await runAdapterCLI([
      'devin', 'skills', 'install', '--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256,
    ], install.stdout, install.stderr);
    assert.equal(code, 0, install.err());
    assert.equal(JSON.parse(install.out()).result.action, 'installed');

    const invalid = capture();
    const invalidCode = await runAdapterCLI([
      'devin', 'skills', 'install', '--project-root', f.root, '--skill-file', f.file, '--sha256', '0'.repeat(64),
    ], invalid.stdout, invalid.stderr);
    assert.equal(invalidCode, 1);
    assert.match(invalid.err(), /SHA-256 does not match/);

    const uninstall = capture();
    const uninstallCode = await runAdapterCLI([
      'devin', 'skills', 'uninstall', '--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256,
    ], uninstall.stdout, uninstall.stderr);
    assert.equal(uninstallCode, 0, uninstall.err());
    assert.equal(JSON.parse(uninstall.out()).result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});
