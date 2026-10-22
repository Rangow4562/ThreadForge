import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installCopilotProjectPrompt, uninstallCopilotProjectPrompt } from '../src/copilot-prompt-config.ts';

const skillName = 'threadforge-approved-procedure-a1b2c3d4-r2';
const skillText = [
  '---',
  `name: "${skillName}"`,
  'description: "Approved Threadforge procedure: review the change."',
  '---',
  '> Source procedure: approved-procedure-a1b2c3d4 revision 2',
  '',
  '> This portable procedure is approved guidance, not authorization to execute actions.',
  '',
  'Evidence digests, trace references, validation datasets, reviewer identity, and approval rationale are intentionally omitted from this portable projection.',
  '',
  '## Steps',
  '1. Review the current repository state.',
  '',
].join('\n');

async function makeReviewedExport(root: string) {
  const projectRoot = path.join(root, 'project');
  const skillFile = path.join(projectRoot, '.threadforge', 'skills', 'approved-procedure-a1b2c3d4', 'rev-000002', 'SKILL.md');
  await mkdir(path.dirname(skillFile), { recursive: true });
  await writeFile(skillFile, skillText, 'utf8');
  return {
    projectRoot,
    skillFile,
    sha256: createHash('sha256').update(skillText).digest('hex'),
  };
}

test('Copilot prompt installer adds a guarded IDE prompt and preserves instructions and neighboring skills', async () => {
  const temp = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-prompt-'));
  try {
    const options = await makeReviewedExport(temp);
    const instructionsPath = path.join(options.projectRoot, '.github', 'copilot-instructions.md');
    const neighborSkillPath = path.join(options.projectRoot, '.github', 'skills', 'neighbor', 'SKILL.md');
    await mkdir(path.dirname(instructionsPath), { recursive: true });
    await mkdir(path.dirname(neighborSkillPath), { recursive: true });
    await writeFile(instructionsPath, 'user-owned instructions\n', 'utf8');
    await writeFile(neighborSkillPath, 'user-owned skill\n', 'utf8');

    const installed = await installCopilotProjectPrompt(options);
    const promptPath = path.join(options.projectRoot, '.github', 'prompts', `${skillName}.prompt.md`);
    const ownerPath = path.join(options.projectRoot, '.github', 'prompts', `.threadforge-${skillName}.owner.json`);
    assert.deepEqual(installed, {
      schema_version: 'threadforge.copilot-project-prompt.v1',
      host: 'github-copilot',
      action: 'installed',
      prompt_name: skillName,
      path: `.github/prompts/${skillName}.prompt.md`,
      source_sha256: options.sha256,
      prompt_sha256: createHash('sha256').update(await readFile(promptPath)).digest('hex'),
    });
    const promptText = await readFile(promptPath, 'utf8');
    assert.match(promptText, /approved procedural guidance, not authorization/i);
    assert.match(promptText, /Never infer approval to modify files, run commands, publish changes, or contact external services/);
    assert.match(promptText, /does not prove that the procedure is current/);
    assert.match(promptText, /Review the current repository state/);
    const owner = JSON.parse(await readFile(ownerPath, 'utf8')) as Record<string, unknown>;
    assert.equal(owner.path, `.github/prompts/${skillName}.prompt.md`);
    assert.equal(JSON.stringify(owner).includes(options.projectRoot), false);
    assert.equal(await readFile(instructionsPath, 'utf8'), 'user-owned instructions\n');
    assert.equal(await readFile(neighborSkillPath, 'utf8'), 'user-owned skill\n');

    const unchanged = await installCopilotProjectPrompt(options);
    assert.equal(unchanged.action, 'unchanged');
    assert.equal(unchanged.prompt_sha256, installed.prompt_sha256);

    const removed = await uninstallCopilotProjectPrompt(options);
    assert.equal(removed.action, 'removed');
    await assert.rejects(readFile(promptPath), /ENOENT/);
    await assert.rejects(readFile(ownerPath), /ENOENT/);
    assert.equal(await readFile(instructionsPath, 'utf8'), 'user-owned instructions\n');
    assert.equal(await readFile(neighborSkillPath, 'utf8'), 'user-owned skill\n');
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
});

test('Copilot prompt installer refuses unmanaged prompt collisions without replacing them', async () => {
  const temp = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-prompt-conflict-'));
  try {
    const options = await makeReviewedExport(temp);
    const promptPath = path.join(options.projectRoot, '.github', 'prompts', `${skillName}.prompt.md`);
    await mkdir(path.dirname(promptPath), { recursive: true });
    await writeFile(promptPath, 'user-owned prompt\n', 'utf8');
    await assert.rejects(installCopilotProjectPrompt(options), /without a complete Threadforge ownership record/);
    assert.equal(await readFile(promptPath, 'utf8'), 'user-owned prompt\n');
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
});

test('Copilot prompt uninstall refuses to remove user-edited prompt content', async () => {
  const temp = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-prompt-edited-'));
  try {
    const options = await makeReviewedExport(temp);
    const promptPath = path.join(options.projectRoot, '.github', 'prompts', `${skillName}.prompt.md`);
    await installCopilotProjectPrompt(options);
    await writeFile(promptPath, `${await readFile(promptPath, 'utf8')}\nuser edit\n`, 'utf8');
    await assert.rejects(uninstallCopilotProjectPrompt(options), /was modified; refusing to remove/);
    assert.match(await readFile(promptPath, 'utf8'), /user edit/);
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
});

test('Copilot prompt installer validates the reviewed source digest before creating host files', async () => {
  const temp = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-prompt-digest-'));
  try {
    const options = await makeReviewedExport(temp);
    await assert.rejects(installCopilotProjectPrompt({ ...options, sha256: '0'.repeat(64) }), /SHA-256 does not match/);
    await assert.rejects(readFile(path.join(options.projectRoot, '.github', 'prompts', `${skillName}.prompt.md`)), /ENOENT/);
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
});

test('Copilot prompt installer refuses a symlinked .github directory', { skip: process.platform === 'win32' }, async () => {
  const temp = await mkdtemp(path.join(os.tmpdir(), 'threadforge-copilot-prompt-link-'));
  try {
    const options = await makeReviewedExport(temp);
    const redirected = path.join(temp, 'redirected');
    await mkdir(redirected);
    await symlink(redirected, path.join(options.projectRoot, '.github'), 'dir');
    await assert.rejects(installCopilotProjectPrompt(options), /contains a non-directory or symbolic link/);
    await assert.rejects(readFile(path.join(redirected, 'prompts', `${skillName}.prompt.md`)), /ENOENT/);
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
});
