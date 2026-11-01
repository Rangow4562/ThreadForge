import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { test } from 'node:test';
import { runClineSkillHostSmoke } from '../scripts/cline-skill-host-smoke.mjs';

test('Cline inventory smoke installs procedure and typed memory skills without invoking an agent', async () => {
  const version = '3.0.73';
  const runCommand = (_executable, args, options = {}) => {
    if (args.length === 1 && args[0] === '--version') return { status: 0, stdout: `${version}\n` };
    assert.deepEqual(args.slice(-2), ['config', '--json']);
    const skillsRoot = path.join(options.cwd, '.cline', 'skills');
    const skills = readdirSync(skillsRoot, { withFileTypes: true })
      .filter((entry) => entry.isDirectory())
      .map((entry) => {
        const skillPath = path.join('.cline', 'skills', entry.name, 'SKILL.md');
        const body = readFileSync(path.join(options.cwd, skillPath), 'utf8');
        const name = /^name: ("(?:\\.|[^"\\])*")$/m.exec(body)?.[1];
        return { name: name ? JSON.parse(name) : undefined, path: skillPath };
      });
    return { status: 0, stdout: JSON.stringify({ skills }) };
  };

  const receipt = await runClineSkillHostSmoke(process.execPath, version, { runCommand });
  assert.equal(receipt.schema_version, 'threadforge.cline-skill-inventory-smoke.v2');
  assert.equal(receipt.discovered_project_skill_count, 2);
  assert.equal(receipt.discovered_procedure_skill_count, 1);
  assert.equal(receipt.discovered_memory_skill_count, 1);
  assert.equal(receipt.procedure_name_retained_in_report, false);
  assert.equal(receipt.memory_skill_name_retained_in_report, false);
  assert.equal(receipt.procedure_body_retained_in_report, false);
  assert.equal(receipt.memory_skill_body_retained_in_report, false);
  assert.equal(receipt.agent_retrieval_status, 'not_exercised');
  assert.equal(receipt.model_invocations, 0);
  assert.equal(receipt.execution_authorized, false);
});
