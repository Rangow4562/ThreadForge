import assert from 'node:assert/strict';
import test from 'node:test';
import { lstat, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { grokHooksPath, installGrokHooks, removeGrokHooks } from '../src/grok-hooks-config.ts';

const template = 'threadforge adapter grok hook {event} --threadforge-grok-hook=v1';

test('Grok user install owns only its dedicated hook file and is idempotent/reversible', async () => {
  const homeDir = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-user-'));
  try {
    const hooksDir = path.join(homeDir, '.grok', 'hooks');
    await mkdir(hooksDir, { recursive: true });
    const neighboring = path.join(hooksDir, 'personal.json');
    const neighborBytes = '{"description":"user hook","hooks":{}}\n';
    await writeFile(neighboring, neighborBytes);

    assert.equal(await installGrokHooks({ homeDir, commandTemplate: template }), 'installed');
    assert.equal(await installGrokHooks({ homeDir, commandTemplate: template }), 'unchanged');
    const file = grokHooksPath('user', homeDir);
    const installed = JSON.parse(await readFile(file, 'utf8')) as { description: string; hooks: Record<string, Array<{ hooks: Array<{ command: string; timeout: number }> }>> };
    assert.match(installed.description, /threadforge-owner-v1:[a-f0-9]{64}$/);
    assert.deepEqual(Object.keys(installed.hooks).sort(), [
      'PostToolUse', 'PostToolUseFailure', 'PreToolUse', 'SessionEnd', 'SessionStart', 'Stop', 'UserPromptSubmit',
    ]);
    for (const groups of Object.values(installed.hooks)) {
      assert.equal(groups.length, 1);
      assert.equal(groups[0].hooks.length, 1);
      assert.equal(groups[0].hooks[0].timeout, 5);
      assert.match(groups[0].hooks[0].command, /--threadforge-grok-hook=v1/);
    }
    assert.equal(await readFile(neighboring, 'utf8'), neighborBytes);

    assert.equal(await installGrokHooks({ homeDir, commandTemplate: 'threadforge adapter grok hook {event} --threadforge-grok-hook=v1 --quiet' }), 'updated');
    assert.equal(await removeGrokHooks({ homeDir }), 'removed');
    assert.equal(await removeGrokHooks({ homeDir }), 'absent');
    assert.equal(await readFile(neighboring, 'utf8'), neighborBytes);
  } finally { await rm(homeDir, { recursive: true, force: true }); }
});

test('Grok project scope requires a root and leaves edited or unowned hook files intact', async () => {
  await assert.rejects(installGrokHooks({ scope: 'project', commandTemplate: template }), /absolute Grok root/);
  const projectRoot = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-project-'));
  try {
    const trustedHooks = path.join(projectRoot, '.grok', 'hooks');
    await mkdir(trustedHooks, { recursive: true });
    const reservedPath = grokHooksPath('project', projectRoot);
    const unownedBytes = '{"description":"user-owned hooks","hooks":{}}\n';
    await writeFile(reservedPath, unownedBytes);
    await assert.rejects(installGrokHooks({ scope: 'project', projectRoot, commandTemplate: template }), /unowned or edited/);
    assert.equal(await readFile(reservedPath, 'utf8'), unownedBytes);
    await rm(reservedPath);

    const userFile = path.join(trustedHooks, 'user.json');
    await writeFile(userFile, '{"description":"unrelated","hooks":{}}\n');
    const scope = { scope: 'project' as const, projectRoot };
    assert.equal(await installGrokHooks({ ...scope, commandTemplate: template }), 'installed');
    const managed = grokHooksPath('project', projectRoot);
    const original = await readFile(managed, 'utf8');
    await writeFile(managed, original.replace('"timeout": 5', '"timeout": 6'));
    await assert.rejects(removeGrokHooks(scope), /edited; refusing removal/);
    assert.equal(await readFile(userFile, 'utf8'), '{"description":"unrelated","hooks":{}}\n');
    assert.equal(await lstat(managed).then((value) => value.isFile()), true);
  } finally { await rm(projectRoot, { recursive: true, force: true }); }
});

test('Grok hook manager refuses credential-bearing commands and symlinked managed paths', async (t) => {
  const homeDir = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-safe-'));
  try {
    await assert.rejects(installGrokHooks({ homeDir, commandTemplate: `${template} --api-key=secret-value` }), /no credentials/);
    const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-link-'));
    try {
      const outside = await mkdtemp(path.join(os.tmpdir(), 'threadforge-grok-outside-'));
      try {
        await symlink(outside, path.join(root, '.grok'), 'junction');
        await assert.rejects(installGrokHooks({ scope: 'project', projectRoot: root, commandTemplate: template }), /symbolic link/);
      } catch (error) {
        if (process.platform === 'win32') { t.skip(`symlink creation is not permitted: ${String(error)}`); return; }
        throw error;
      } finally { await rm(outside, { recursive: true, force: true }); }
    } finally { await rm(root, { recursive: true, force: true }); }
  } finally { await rm(homeDir, { recursive: true, force: true }); }
});
