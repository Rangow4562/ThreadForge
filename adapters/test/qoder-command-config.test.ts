import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { Writable } from 'node:stream';
import test from 'node:test';
import { runAdapterCLI } from '../src/cli.ts';
import { installQoderProjectCommand, uninstallQoderProjectCommand } from '../src/qoder-command-config.ts';

const approvalNotice = '> This portable procedure is approved guidance, not authorization to execute actions.';
const omissionNotice = 'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.';

function fixture(): { root: string; file: string; sha256: string; commandName: string } {
  const root = mkdtempSync(path.join(tmpdir(), 'threadforge-qoder-command-'));
  const file = path.join(root, '.threadforge', 'skills', 'approved-procedure', 'rev-000002', 'SKILL.md');
  mkdirSync(path.dirname(file), { recursive: true });
  writeFileSync(file, [
    '---',
    'name: "threadforge-approved-procedure-a1b2c3d4-r2"',
    'description: "Approved Threadforge procedure: review the change."',
    '---',
    '',
    '# Threadforge procedure',
    '',
    '> Source procedure: approved-procedure revision 2',
    '',
    approvalNotice,
    '',
    '## Procedure',
    '',
    '### check: inspect',
    '',
    '> Inspect current project state; this is guidance only.',
    '',
    omissionNotice,
    '',
  ].join('\n'));
  const bytes = readFileSync(file);
  return {
    root,
    file,
    sha256: createHash('sha256').update(bytes).digest('hex'),
    commandName: 'threadforge-approved-procedure-a1b2c3d4-r2',
  };
}

function writableText(): { stream: Writable; text: () => string } {
  let value = '';
  const stream = new Writable({ write(chunk, _encoding, callback) { value += chunk.toString(); callback(); } });
  return { stream, text: () => value };
}

test('installs an explicit Qoder project command from an exact approved procedure export and preserves neighbors', async () => {
  const f = fixture();
  const instructions = path.join(f.root, 'AGENTS.md');
  const qoderSettings = path.join(f.root, '.qoder', 'settings.json');
  const neighbor = path.join(f.root, '.qoder', 'commands', 'user-review.md');
  const qoderSkill = path.join(f.root, '.qoder', 'skills', 'team-handbook', 'SKILL.md');
  mkdirSync(path.dirname(qoderSettings), { recursive: true });
  mkdirSync(path.dirname(neighbor), { recursive: true });
  mkdirSync(path.dirname(qoderSkill), { recursive: true });
  writeFileSync(instructions, 'User-owned instructions.\n');
  writeFileSync(qoderSettings, '{"trust":"unchanged"}\n');
  writeFileSync(neighbor, 'User-owned Qoder prompt.\n');
  writeFileSync(qoderSkill, 'User-owned Qoder Skill.\n');
  try {
    const first = await installQoderProjectCommand({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(first.action, 'installed');
    assert.equal(first.host, 'qoder-cli');
    assert.equal(first.command_name, f.commandName);
    assert.equal(first.path, `.qoder/commands/${f.commandName}.md`);
    assert.equal(first.source_sha256, f.sha256);
    const commandPath = path.join(f.root, ...first.path.split('/'));
    const command = readFileSync(commandPath, 'utf8');
    assert.match(command, /^---\nname: "threadforge-approved-procedure-a1b2c3d4-r2"\ndescription: "Approved Threadforge procedure: review the change\."\n---\n/);
    assert.ok(command.includes(approvalNotice));
    assert.ok(command.includes(omissionNotice));
    assert.equal(createHash('sha256').update(command).digest('hex'), first.command_sha256);

    const repeated = await installQoderProjectCommand({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    assert.equal(readFileSync(instructions, 'utf8'), 'User-owned instructions.\n');
    assert.equal(readFileSync(qoderSettings, 'utf8'), '{"trust":"unchanged"}\n');
    assert.equal(readFileSync(neighbor, 'utf8'), 'User-owned Qoder prompt.\n');
    assert.equal(readFileSync(qoderSkill, 'utf8'), 'User-owned Qoder Skill.\n');

    const removed = await uninstallQoderProjectCommand({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(instructions, 'utf8'), 'User-owned instructions.\n');
    assert.equal(readFileSync(qoderSettings, 'utf8'), '{"trust":"unchanged"}\n');
    assert.equal(readFileSync(neighbor, 'utf8'), 'User-owned Qoder prompt.\n');
    assert.equal(readFileSync(qoderSkill, 'utf8'), 'User-owned Qoder Skill.\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Qoder command CLI installs and removes the project command with digest-bound output', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const args = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['qoder', 'commands', 'install', ...args], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; result: { action: string; path: string } };
    assert.equal(installed.family, 'qoder');
    assert.equal(installed.result.action, 'installed');
    assert.equal(installed.result.path, `.qoder/commands/${f.commandName}.md`);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['qoder', 'commands', 'uninstall', ...args], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Qoder command installer refuses unowned collisions and preserves existing bytes', async () => {
  const f = fixture();
  const target = path.join(f.root, '.qoder', 'commands', `${f.commandName}.md`);
  mkdirSync(path.dirname(target), { recursive: true });
  writeFileSync(target, 'User-owned command.\n');
  try {
    await assert.rejects(
      installQoderProjectCommand({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }),
      /without a complete Threadforge ownership record/,
    );
    assert.equal(readFileSync(target, 'utf8'), 'User-owned command.\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Qoder command uninstall refuses edited command content', async () => {
  const f = fixture();
  try {
    const installed = await installQoderProjectCommand({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const target = path.join(f.root, ...installed.path.split('/'));
    writeFileSync(target, `${readFileSync(target, 'utf8')}\nUser edit.\n`);
    await assert.rejects(
      uninstallQoderProjectCommand({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }),
      /was modified; refusing to remove user-edited content/,
    );
    assert.ok(readFileSync(target, 'utf8').endsWith('User edit.\n'));
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Qoder command installer refuses a commands directory configured as a single Skill', async () => {
  const f = fixture();
  const skillMode = path.join(f.root, '.qoder', 'commands', 'SKILL.md');
  mkdirSync(path.dirname(skillMode), { recursive: true });
  writeFileSync(skillMode, 'User-owned Skill.\n');
  try {
    await assert.rejects(
      installQoderProjectCommand({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }),
      /containing SKILL.md as a single Skill/,
    );
    assert.equal(readFileSync(skillMode, 'utf8'), 'User-owned Skill.\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Qoder command installer rejects a stale source digest before writing host files', async () => {
  const f = fixture();
  try {
    await assert.rejects(
      installQoderProjectCommand({ projectRoot: f.root, skillFile: f.file, sha256: '0'.repeat(64) }),
      /SHA-256 does not match/,
    );
    assert.equal(readFileSync(path.join(f.root, '.threadforge', 'skills', 'approved-procedure', 'rev-000002', 'SKILL.md')).length > 0, true);
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});
