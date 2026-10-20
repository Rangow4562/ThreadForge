import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { lstatSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { Writable } from 'node:stream';
import { test } from 'node:test';
import { runAdapterCLI } from '../src/cli.ts';
import { installClaudeCodeProjectSkill, uninstallClaudeCodeProjectSkill } from '../src/claude-skill-config.ts';
import { installCommandCodeProjectSkill, uninstallCommandCodeProjectSkill } from '../src/command-code-skill-config.ts';
import { installAntigravityProjectSkill, uninstallAntigravityProjectSkill } from '../src/antigravity-skill-config.ts';
import { installClineProjectSkill, uninstallClineProjectSkill } from '../src/cline-skill-config.ts';
import { installCopilotProjectSkill, uninstallCopilotProjectSkill } from '../src/copilot-skill-config.ts';
import { installCodexProjectSkill, uninstallCodexProjectSkill } from '../src/codex-skill-config.ts';
import { installCursorProjectSkill, uninstallCursorProjectSkill } from '../src/cursor-skill-config.ts';
import { installGeminiProjectSkill, uninstallGeminiProjectSkill } from '../src/gemini-skill-config.ts';
import { installOpenCodeProjectSkill, uninstallOpenCodeProjectSkill } from '../src/opencode-skill-config.ts';
import { installOpenHandsProjectSkill, uninstallOpenHandsProjectSkill } from '../src/openhands-skill-config.ts';
import { installKimiCodeProjectSkill, uninstallKimiCodeProjectSkill } from '../src/kimi-code-skill-config.ts';
import { installZCodeProjectSkill, uninstallZCodeProjectSkill } from '../src/zcode-skill-config.ts';
import { installDeepSeekHarnessProjectSkill, uninstallDeepSeekHarnessProjectSkill } from '../src/deepseek-harness-skill-config.ts';
import { installQoderProjectSkill, uninstallQoderProjectSkill } from '../src/qoder-skill-config.ts';

const approvalNotice = '> This portable procedure is approved guidance, not authorization to execute actions.';
const omissionNotice = 'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.';

function fixture(): { root: string; file: string; sha256: string; bytes: Buffer } {
  const root = mkdtempSync(path.join(tmpdir(), 'threadforge-cline-skill-'));
  const graphID = 'approved-procedure';
  const revision = 2;
  const file = path.join(root, '.threadforge', 'skills', graphID, 'rev-000002', 'SKILL.md');
  mkdirSync(path.dirname(file), { recursive: true });
  const body = [
    '---',
    'name: "threadforge-approved-procedure-a1b2c3d4-r2"',
    'description: "Approved Threadforge procedure: review the change."',
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
    '> Inspect the current project state and report uncertainty.',
    '',
    omissionNotice,
    '',
  ].join('\n');
  const bytes = Buffer.from(body, 'utf8');
  writeFileSync(file, bytes);
  return { root, file, bytes, sha256: createHash('sha256').update(bytes).digest('hex') };
}

function writableText(): { stream: Writable; text: () => string } {
  let value = '';
  const stream = new Writable({ write(chunk, _encoding, callback) { value += chunk.toString(); callback(); } });
  return { stream, text: () => value };
}

test('installs only a digest-matched Threadforge export and preserves neighboring Cline skills', async () => {
  const f = fixture();
  try {
    const neighborDir = path.join(f.root, '.cline', 'skills', 'user-skill');
    mkdirSync(neighborDir, { recursive: true });
    writeFileSync(path.join(neighborDir, 'SKILL.md'), 'user content\n');

    const first = await installClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(first.action, 'installed');
    assert.equal(first.path, '.cline/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, first.path)), f.bytes);

    const repeated = await installClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');

    const removed = await uninstallClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(path.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs a reviewed procedure into Cursor portable project skills and preserves neighboring content', async () => {
  const f = fixture();
  const neighborDir = path.join(f.root, '.agents', 'skills', 'user-skill');
  mkdirSync(neighborDir, { recursive: true });
  writeFileSync(path.join(neighborDir, 'SKILL.md'), 'user content\n');
  try {
    const installed = await installCursorProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'cursor');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.path)), f.bytes);

    const repeated = await installCursorProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallCursorProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(path.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs and removes an approved procedure in local Claude Code project skills', async () => {
  const f = fixture();
  const neighborDir = path.join(f.root, '.claude', 'skills', 'user-skill');
  mkdirSync(neighborDir, { recursive: true });
  writeFileSync(path.join(neighborDir, 'SKILL.md'), 'user content\n');
  try {
    const installed = await installClaudeCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'claude-code');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.claude/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.path)), f.bytes);

    const repeated = await installClaudeCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallClaudeCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(path.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs and removes a reviewed procedure in OpenCode project skills', async () => {
  const f = fixture();
  const neighborDir = path.join(f.root, '.agents', 'skills', 'user-skill');
  mkdirSync(neighborDir, { recursive: true });
  writeFileSync(path.join(neighborDir, 'SKILL.md'), 'user content\n');
  try {
    const installed = await installOpenCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'opencode');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.path)), f.bytes);

    const repeated = await installOpenCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallOpenCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(path.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs and removes a reviewed procedure in Codex CLI project skills', async () => {
  const f = fixture();
  const neighborDir = path.join(f.root, '.agents', 'skills', 'user-skill');
  mkdirSync(neighborDir, { recursive: true });
  writeFileSync(path.join(neighborDir, 'SKILL.md'), 'user content\n');
  try {
    const installed = await installCodexProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'codex-cli');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.path)), f.bytes);

    const repeated = await installCodexProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallCodexProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(path.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs and removes a reviewed procedure in Gemini CLI project skills', async () => {
  const f = fixture();
  try {
    const installed = await installGeminiProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'gemini-cli');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    const removed = await uninstallGeminiProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs and removes a reviewed procedure in Antigravity project skills', async () => {
  const f = fixture();
  try {
    const installed = await installAntigravityProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'antigravity');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    const removed = await uninstallAntigravityProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs and removes an approved procedure in OpenHands project Agent Skills', async () => {
  const f = fixture();
  const neighborDir = path.join(f.root, '.agents', 'skills', 'user-skill');
  mkdirSync(neighborDir, { recursive: true });
  writeFileSync(path.join(neighborDir, 'SKILL.md'), 'user content\n');
  try {
    const installed = await installOpenHandsProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'openhands');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.path)), f.bytes);

    const repeated = await installOpenHandsProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const owner = JSON.parse(readFileSync(path.join(path.dirname(path.join(f.root, installed.path)), '.threadforge-owner.json'), 'utf8')) as { host: string };
    assert.equal(owner.host, 'agents-skills');

    const removed = await uninstallOpenHandsProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(path.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs and removes an approved procedure in Kimi Code native project skills', async () => {
  const f = fixture();
  const neighborDir = path.join(f.root, '.kimi-code', 'skills', 'user-skill');
  mkdirSync(neighborDir, { recursive: true });
  writeFileSync(path.join(neighborDir, 'SKILL.md'), 'user content\n');
  try {
    const installed = await installKimiCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'kimi-code');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.kimi-code/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.path)), f.bytes);

    const repeated = await installKimiCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const owner = JSON.parse(readFileSync(path.join(path.dirname(path.join(f.root, installed.path)), '.threadforge-owner.json'), 'utf8')) as { host: string };
    assert.equal(owner.host, 'kimi-code');

    const removed = await uninstallKimiCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(path.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs and removes an approved procedure in ZCode shared project Agent Skills', async () => {
  const f = fixture();
  const neighborDir = path.join(f.root, '.agents', 'skills', 'user-skill');
  mkdirSync(neighborDir, { recursive: true });
  writeFileSync(path.join(neighborDir, 'SKILL.md'), 'user content\n');
  try {
    const installed = await installZCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'zcode');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    const ownerPath = path.join(f.root, path.dirname(installed.path), '.threadforge-owner.json');
    assert.equal((JSON.parse(readFileSync(ownerPath, 'utf8')) as { host: string }).host, 'agents-skills');

    const repeated = await installZCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallZCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(path.join(neighborDir, 'SKILL.md'), 'utf8'), 'user content\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs DeepSeek Harness skills under .dsh/skills without touching shared .agents skills', async () => {
  const f = fixture();
  const agentsNeighbor = path.join(f.root, '.agents', 'skills', 'user-skill', 'SKILL.md');
  mkdirSync(path.dirname(agentsNeighbor), { recursive: true });
  writeFileSync(agentsNeighbor, 'user-owned shared skill\n');
  try {
    const installed = await installDeepSeekHarnessProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'deepseek-harness');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.dsh/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    const ownerPath = path.join(f.root, path.dirname(installed.path), '.threadforge-owner.json');
    assert.equal((JSON.parse(readFileSync(ownerPath, 'utf8')) as { host: string }).host, 'deepseek-harness');
    assert.equal(readFileSync(agentsNeighbor, 'utf8'), 'user-owned shared skill\n');

    const repeated = await installDeepSeekHarnessProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallDeepSeekHarnessProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(agentsNeighbor, 'utf8'), 'user-owned shared skill\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('installs Qoder CLI skills without changing Qoder commands or shared instructions', async () => {
  const f = fixture();
  const qoderCommands = path.join(f.root, '.qoder', 'commands', 'user-command.md');
  const agentsFile = path.join(f.root, 'AGENTS.md');
  mkdirSync(path.dirname(qoderCommands), { recursive: true });
  writeFileSync(qoderCommands, 'User-owned Qoder command.\n');
  writeFileSync(agentsFile, 'User-owned shared project instructions.\n');
  try {
    const installed = await installQoderProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'qoder');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.qoder/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    const ownerPath = path.join(f.root, path.dirname(installed.path), '.threadforge-owner.json');
    assert.equal((JSON.parse(readFileSync(ownerPath, 'utf8')) as { host: string }).host, 'qoder');

    const repeated = await installQoderProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallQoderProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.equal(readFileSync(qoderCommands, 'utf8'), 'User-owned Qoder command.\n');
    assert.equal(readFileSync(agentsFile, 'utf8'), 'User-owned shared project instructions.\n');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('reuses one .agents/skills export across hosts with shared ownership and uninstall', async () => {
  const f = fixture();
  try {
    const codexInstall = await installCodexProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(codexInstall.action, 'installed');

    const geminiInstall = await installGeminiProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(geminiInstall.action, 'unchanged');
    const ownerPath = path.join(f.root, path.dirname(geminiInstall.path), '.threadforge-owner.json');
    const owner = JSON.parse(readFileSync(ownerPath, 'utf8')) as { host: string };
    assert.equal(owner.host, 'agents-skills');

    // Older installers wrote the concrete host name into this shared directory.
    writeFileSync(ownerPath, `${JSON.stringify({ ...owner, host: 'codex-cli' })}\n`);
    const compatibleInstall = await installGeminiProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(compatibleInstall.action, 'unchanged');

    const removed = await uninstallAntigravityProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('distributes one approved portable export across Claude, Cline, shared .agents, and Kimi Code skill roots', async () => {
  const f = fixture();
  const claudeNeighbor = path.join(f.root, '.claude', 'skills', 'user-skill');
  const clineNeighbor = path.join(f.root, '.cline', 'skills', 'user-skill');
  const codexNeighbor = path.join(f.root, '.agents', 'skills', 'user-skill');
  mkdirSync(claudeNeighbor, { recursive: true });
  mkdirSync(clineNeighbor, { recursive: true });
  mkdirSync(codexNeighbor, { recursive: true });
  writeFileSync(path.join(claudeNeighbor, 'SKILL.md'), 'claude user content\n');
  writeFileSync(path.join(clineNeighbor, 'SKILL.md'), 'cline user content\n');
  writeFileSync(path.join(codexNeighbor, 'SKILL.md'), 'codex user content\n');
  try {
    const claude = await installClaudeCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const cline = await installClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const codex = await installCodexProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const cursor = await installCursorProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const gemini = await installGeminiProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const opencode = await installOpenCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const antigravity = await installAntigravityProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const openhands = await installOpenHandsProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const kimi = await installKimiCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });

    assert.equal(claude.action, 'installed');
    assert.equal(claude.path, '.claude/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.equal(cline.action, 'installed');
    assert.equal(cline.path, '.cline/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.equal(codex.action, 'installed');
    assert.equal(codex.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual([cursor.action, gemini.action, opencode.action, antigravity.action, openhands.action], [
      'unchanged', 'unchanged', 'unchanged', 'unchanged', 'unchanged',
    ]);
    assert.equal(kimi.action, 'installed');
    assert.equal(kimi.path, '.kimi-code/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.equal(new Set([claude.path, cline.path, codex.path, cursor.path, gemini.path, opencode.path, antigravity.path, openhands.path, kimi.path]).size, 4);
    for (const installedPath of new Set([claude.path, cline.path, codex.path, kimi.path])) {
      const installedBytes = readFileSync(path.join(f.root, installedPath));
      assert.deepEqual(installedBytes, f.bytes);
      assert.equal(createHash('sha256').update(installedBytes).digest('hex'), f.sha256);
    }

    const removedClaude = await uninstallClaudeCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    const removedCline = await uninstallClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removedClaude.action, 'removed');
    assert.equal(removedCline.action, 'removed');
    assert.equal(readFileSync(path.join(claudeNeighbor, 'SKILL.md'), 'utf8'), 'claude user content\n');
    assert.equal(readFileSync(path.join(clineNeighbor, 'SKILL.md'), 'utf8'), 'cline user content\n');

    const removedAgentsSkill = await uninstallCodexProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removedAgentsSkill.action, 'removed');
    const removedOpenHands = await uninstallOpenHandsProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removedOpenHands.action, 'absent');
    const removedKimi = await uninstallKimiCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removedKimi.action, 'removed');
    assert.equal(readFileSync(path.join(codexNeighbor, 'SKILL.md'), 'utf8'), 'codex user content\n');
    assert.deepEqual(readFileSync(f.file), f.bytes);
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('refuses digest mismatch, conflicting skills, edited installs, and non-export paths', async () => {
  const f = fixture();
  try {
    await assert.rejects(
      installClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: '0'.repeat(64) }),
      /SHA-256 does not match/,
    );

    const destination = path.join(f.root, '.cline', 'skills', 'threadforge-approved-procedure-a1b2c3d4-r2');
    mkdirSync(destination, { recursive: true });
    writeFileSync(path.join(destination, 'SKILL.md'), 'user-owned skill\n');
    await assert.rejects(
      installClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }),
      /without a complete Threadforge ownership record/,
    );

    await assert.rejects(
      uninstallClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }),
      /no Threadforge ownership record/,
    );

    rmSync(destination, { recursive: true, force: true });
    await installClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    writeFileSync(path.join(destination, 'SKILL.md'), Buffer.concat([f.bytes, Buffer.from('\nuser edit\n')]));
    await assert.rejects(
      uninstallClineProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }),
      /modified; refusing to remove/,
    );

    const outside = path.join(f.root, 'SKILL.md');
    writeFileSync(outside, f.bytes);
    await assert.rejects(
      installClineProjectSkill({ projectRoot: f.root, skillFile: outside, sha256: f.sha256 }),
      /must be an exported/,
    );
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Cline CLI installs and removes a reviewed exported procedure using its printed digest', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['cline', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { action: string; sha256: string } };
    assert.equal(installed.family, 'cline');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.action, 'installed');
    assert.equal(installed.result.sha256, f.sha256);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['cline', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    const removed = JSON.parse(removeOut.text()) as { result: { action: string } };
    assert.equal(removed.result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Cursor CLI installs and removes a reviewed procedure in .agents/skills', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['cursor', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'cursor');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.host, 'cursor');
    assert.equal(installed.result.action, 'installed');
    assert.match(installed.result.path, /^\.agents\/skills\//);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['cursor', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    const removed = JSON.parse(removeOut.text()) as { result: { action: string } };
    assert.equal(removed.result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Claude Code CLI installs and removes a reviewed procedure in .claude/skills', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['claude-code', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'claude-code');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.host, 'claude-code');
    assert.equal(installed.result.action, 'installed');
    assert.match(installed.result.path, /^\.claude\/skills\//);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['claude-code', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    const removed = JSON.parse(removeOut.text()) as { result: { action: string } };
    assert.equal(removed.result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('OpenCode CLI installs and removes a reviewed procedure in .agents/skills', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['opencode', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'opencode');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.host, 'opencode');
    assert.equal(installed.result.action, 'installed');
    assert.match(installed.result.path, /^\.agents\/skills\//);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['opencode', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    const removed = JSON.parse(removeOut.text()) as { result: { action: string } };
    assert.equal(removed.result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('OpenHands CLI installs only reviewed project skills and preserves its MCP and hook settings', async () => {
  const f = fixture();
  const configDirectory = path.join(f.root, '.openhands');
  mkdirSync(configDirectory, { recursive: true });
  const mcpConfigPath = path.join(configDirectory, 'mcp.json');
  const hooksConfigPath = path.join(configDirectory, 'hooks.json');
  const mcpConfig = '{"mcpServers":{"user-server":{"command":"custom-mcp"}}}\n';
  const hooksConfig = '{"hooks":{"SessionStart":[{"matcher":"*","hooks":[{"type":"command","command":"user-hook"}]}]}}\n';
  writeFileSync(mcpConfigPath, mcpConfig);
  writeFileSync(hooksConfigPath, hooksConfig);
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['openhands', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'openhands');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.host, 'openhands');
    assert.equal(installed.result.action, 'installed');
    assert.equal(installed.result.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.equal(readFileSync(mcpConfigPath, 'utf8'), mcpConfig);
    assert.equal(readFileSync(hooksConfigPath, 'utf8'), hooksConfig);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['open-hands', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
    assert.equal(readFileSync(mcpConfigPath, 'utf8'), mcpConfig);
    assert.equal(readFileSync(hooksConfigPath, 'utf8'), hooksConfig);
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Kimi Code CLI installs reviewed project skills without changing project TOML hooks or settings', async () => {
  const f = fixture();
  const configDirectory = path.join(f.root, '.kimi-code');
  mkdirSync(configDirectory, { recursive: true });
  const configPath = path.join(configDirectory, 'config.toml');
  const config = '# user-owned Kimi settings\n[model]\nname = "local-model"\n\n[[hooks]]\nevent = "SessionStart"\ncommand = "user-hook"\ntimeout = 12\n';
  writeFileSync(configPath, config);
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['kimi', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'kimi');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.host, 'kimi-code');
    assert.equal(installed.result.action, 'installed');
    assert.equal(installed.result.path, '.kimi-code/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.equal(readFileSync(configPath, 'utf8'), config);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['kimi', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
    assert.equal(readFileSync(configPath, 'utf8'), config);
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('ZCode CLI installs and removes approved skills under the shared Agent Skills path', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['zcode', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; result: { host: string; path: string } };
    assert.equal(installed.family, 'zcode');
    assert.equal(installed.result.host, 'zcode');
    assert.equal(installed.result.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['zcode', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Command Code installs approved procedures into its native project skill path', async () => {
  const f = fixture();
  try {
    const installed = await installCommandCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(installed.host, 'command-code');
    assert.equal(installed.action, 'installed');
    assert.equal(installed.path, '.commandcode/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.path)), f.bytes);

    const repeated = await installCommandCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(repeated.action, 'unchanged');
    const removed = await uninstallCommandCodeProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(removed.action, 'removed');
    assert.throws(() => readFileSync(path.join(f.root, installed.path)), /ENOENT/);
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Command Code skills CLI routes install and uninstall through the native project manager', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['command-code', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; result: { host: string; path: string; action: string } };
    assert.equal(installed.family, 'command-code');
    assert.equal(installed.result.host, 'command-code');
    assert.equal(installed.result.action, 'installed');
    assert.equal(installed.result.path, '.commandcode/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    assert.deepEqual(readFileSync(path.join(f.root, installed.result.path)), f.bytes);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['command-code', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
    assert.throws(() => readFileSync(path.join(f.root, installed.result.path)), /ENOENT/);
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('DeepSeek Harness CLI aliases install only to its isolated .dsh/skills directory', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['deepseek-harness', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; result: { host: string; path: string } };
    assert.equal(installed.family, 'dsh');
    assert.equal(installed.result.host, 'deepseek-harness');
    assert.equal(installed.result.path, '.dsh/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['dsh', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Qoder CLI installs and removes approved procedures under .qoder/skills', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['qoder', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; result: { host: string; path: string } };
    assert.equal(installed.family, 'qoder');
    assert.equal(installed.result.host, 'qoder');
    assert.equal(installed.result.path, '.qoder/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');
    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['qoder', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('OpenHands CLI installs and removes a reviewed procedure in shared .agents/skills', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['openhands', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'openhands');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.host, 'openhands');
    assert.equal(installed.result.action, 'installed');
    assert.equal(installed.result.path, '.agents/skills/threadforge-approved-procedure-a1b2c3d4-r2/SKILL.md');

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['open-hands', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('GitHub Copilot installs approved project skills under .github/skills and preserves neighboring guidance', async () => {
  const f = fixture();
  const instructions = path.join(f.root, '.github', 'copilot-instructions.md');
  const neighboringSkill = path.join(f.root, '.github', 'skills', 'team-handbook', 'SKILL.md');
  mkdirSync(path.dirname(instructions), { recursive: true });
  mkdirSync(path.dirname(neighboringSkill), { recursive: true });
  writeFileSync(instructions, 'User-owned repository instructions.\n');
  writeFileSync(neighboringSkill, 'User-owned neighboring skill.\n');
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['copilot', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'copilot');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.host, 'github-copilot');
    assert.equal(installed.result.action, 'installed');
    assert.match(installed.result.path, /^\.github\/skills\//);

    const skillDirectory = path.join(f.root, '.github', 'skills', 'threadforge-approved-procedure-a1b2c3d4-r2');
    const owner = JSON.parse(readFileSync(path.join(skillDirectory, '.threadforge-owner.json'), 'utf8')) as { host: string };
    assert.equal(owner.host, 'github-copilot');
    assert.equal(readFileSync(instructions, 'utf8'), 'User-owned repository instructions.\n');
    assert.equal(readFileSync(neighboringSkill, 'utf8'), 'User-owned neighboring skill.\n');

    const again = await installCopilotProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(again.action, 'unchanged');
    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['copilot', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
    assert.equal(readFileSync(instructions, 'utf8'), 'User-owned repository instructions.\n');
    assert.equal(readFileSync(neighboringSkill, 'utf8'), 'User-owned neighboring skill.\n');
    const absent = await uninstallCopilotProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 });
    assert.equal(absent.action, 'absent');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Codex CLI installs and removes a reviewed procedure in .agents/skills', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['codex', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'codex');
    assert.equal(installed.action, 'install');
    assert.equal(installed.result.host, 'codex-cli');
    assert.equal(installed.result.action, 'installed');
    assert.match(installed.result.path, /^\.agents\/skills\//);

    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['codex', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    const removed = JSON.parse(removeOut.text()) as { result: { action: string } };
    assert.equal(removed.result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Gemini CLI installs and removes a reviewed procedure in .agents/skills', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['gemini', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; action: string; result: { host: string; action: string; path: string } };
    assert.equal(installed.family, 'gemini');
    assert.equal(installed.result.host, 'gemini-cli');
    assert.match(installed.result.path, /^\.agents\/skills\//);
    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['gemini', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('Antigravity skills route is project-scoped despite user-hook default scope', async () => {
  const f = fixture();
  const stdout = writableText();
  const stderr = writableText();
  const base = ['--project-root', f.root, '--skill-file', f.file, '--sha256', f.sha256];
  try {
    assert.equal(await runAdapterCLI(['antigravity', 'skills', 'install', ...base], stdout.stream, stderr.stream), 0, stderr.text());
    const installed = JSON.parse(stdout.text()) as { family: string; result: { host: string; path: string } };
    assert.equal(installed.family, 'antigravity');
    assert.equal(installed.result.host, 'antigravity');
    assert.match(installed.result.path, /^\.agents\/skills\//);
    const removeOut = writableText();
    assert.equal(await runAdapterCLI(['antigravity', 'skills', 'uninstall', ...base], removeOut.stream, stderr.stream), 0, stderr.text());
    assert.equal((JSON.parse(removeOut.text()) as { result: { action: string } }).result.action, 'removed');
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('rejects symlinked portable-skill inputs without following them', async (t) => {
  const f = fixture();
  const link = path.join(f.root, '.threadforge', 'skills', 'linked');
  try {
    try {
      symlinkSync(path.join(f.root, '.threadforge', 'skills', 'approved-procedure'), link, 'dir');
    } catch {
      t.skip('directory symlink creation is unavailable');
      return;
    }
    assert.equal(lstatSync(link).isSymbolicLink(), true);
    await assert.rejects(
      installClineProjectSkill({
        projectRoot: f.root,
        skillFile: path.join(link, 'rev-000002', 'SKILL.md'),
        sha256: f.sha256,
      }),
      /symbolic-link component/,
    );
  } finally {
    rmSync(f.root, { recursive: true, force: true });
  }
});

test('refuses symlinked host skill directories during install and uninstall', async (t) => {
  const f = fixture();
  const outside = mkdtempSync(path.join(tmpdir(), 'threadforge-cursor-skills-outside-'));
  const link = path.join(f.root, '.agents');
  mkdirSync(path.join(outside, 'skills'), { recursive: true });
  try {
    try {
      symlinkSync(outside, link, 'dir');
    } catch {
      t.skip('directory symlink creation is unavailable');
      return;
    }
    await assert.rejects(
      installCursorProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }),
      /symbolic link/,
    );
    await assert.rejects(
      uninstallCursorProjectSkill({ projectRoot: f.root, skillFile: f.file, sha256: f.sha256 }),
      /symbolic link/,
    );
  } finally {
    rmSync(f.root, { recursive: true, force: true });
    rmSync(outside, { recursive: true, force: true });
  }
});
