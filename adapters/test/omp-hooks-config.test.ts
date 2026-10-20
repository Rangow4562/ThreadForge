import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installOmpExtension, ompExtensionPath, removeOmpExtension, updateOmpExtension } from '../src/omp-hooks-config.ts';
import { ompManagedMarker } from '../src/omp-extension.ts';

const temp = () => mkdtemp(path.join(os.tmpdir(), 'threadforge-omp-'));

test('installs, verifies, updates, and removes only the managed user extension file', async () => {
  const agent = await temp();
  const file = ompExtensionPath('user', agent);
  const sibling = path.join(agent, 'extensions', 'my-extension.ts');
  try {
    await mkdir(path.dirname(file));
    const siblingContent = 'export default function myExtension() {}\n';
    await writeFile(sibling, siblingContent, 'utf8');
    assert.equal(await installOmpExtension({ scope: 'user', ompAgentDir: agent }), 'installed');
    const first = await readFile(file, 'utf8');
    assert.match(first, new RegExp(`^${ompManagedMarker.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} sha256=[a-f0-9]{64}`));
    assert.match(first, /tool_approval_resolved/);
    assert.equal(await installOmpExtension({ scope: 'user', ompAgentDir: agent }), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);
    assert.equal(await readFile(sibling, 'utf8'), siblingContent);
    assert.equal(await updateOmpExtension({ scope: 'user', ompAgentDir: agent }), 'unchanged');
    assert.equal(await removeOmpExtension({ scope: 'user', ompAgentDir: agent }), 'removed');
    assert.equal(await readFile(sibling, 'utf8'), siblingContent);
  } finally { await rm(agent, { recursive: true, force: true }); }
});

test('installs into the selected project extension root and refuses unowned or edited targets', async () => {
  const project = await temp();
  const file = ompExtensionPath('project', project);
  try {
    assert.equal(await installOmpExtension({ scope: 'project', projectRoot: project }), 'installed');
    assert.ok((await readFile(file, 'utf8')).includes(ompManagedMarker));
    const altered = (await readFile(file, 'utf8')).replace('Threadforge', 'User-edited');
    await writeFile(file, altered, 'utf8');
    await assert.rejects(updateOmpExtension({ scope: 'project', projectRoot: project }), /modified/);
    await assert.rejects(removeOmpExtension({ scope: 'project', projectRoot: project }), /modified/);
    assert.equal(await readFile(file, 'utf8'), altered);
  } finally { await rm(project, { recursive: true, force: true }); }

  const collisionRoot = await temp();
  const collision = ompExtensionPath('user', collisionRoot);
  try {
    await mkdir(path.dirname(collision), { recursive: true });
    await writeFile(collision, 'export default function userExtension() {}', 'utf8');
    await assert.rejects(installOmpExtension({ scope: 'user', ompAgentDir: collisionRoot }), /unowned or was modified/);
    assert.equal(await readFile(collision, 'utf8'), 'export default function userExtension() {}');
  } finally { await rm(collisionRoot, { recursive: true, force: true }); }
});

test('resolves OMP profiles and config root from current upstream environment variables', async (t) => {
  const home = await temp();
  const previous = {
    OMP_PROFILE: process.env.OMP_PROFILE,
    PI_PROFILE: process.env.PI_PROFILE,
    PI_CONFIG_DIR: process.env.PI_CONFIG_DIR,
    PI_CODING_AGENT_DIR: process.env.PI_CODING_AGENT_DIR,
  };
  try {
    process.env.OMP_PROFILE = 'qa-profile';
    process.env.PI_PROFILE = 'ignored-legacy-profile';
    process.env.PI_CONFIG_DIR = 'omp-config';
    process.env.PI_CODING_AGENT_DIR = path.join(home, 'ignored-agent-override');
    const file = ompExtensionPath('user', path.join(home, 'omp-config', 'profiles', 'qa-profile', 'agent'));
    assert.equal(await installOmpExtension({ scope: 'user', homeDir: home }), 'installed');
    assert.ok((await readFile(file, 'utf8')).includes(ompManagedMarker));
    await removeOmpExtension({ scope: 'user', homeDir: home });
  } finally {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    await rm(home, { recursive: true, force: true });
  }
  assert.ok(t);
});

test('refuses symlinked extension paths and stale locks', async (t) => {
  const home = await temp();
  const outside = await temp();
  try {
    try { await symlink(outside, path.join(home, 'extensions'), 'junction'); }
    catch { t.skip('the current Windows environment does not permit creating directory symlinks'); return; }
    await assert.rejects(installOmpExtension({ scope: 'user', ompAgentDir: home }), /symbolic link/);
  } finally {
    await rm(home, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
  const agent = await temp();
  try {
    await mkdir(path.join(agent, 'extensions'));
    await writeFile(path.join(agent, 'extensions', '.threadforge-omp-extension.lock'), 'locked');
    await assert.rejects(installOmpExtension({ scope: 'user', ompAgentDir: agent }), /locked/);
  } finally { await rm(agent, { recursive: true, force: true }); }
});
