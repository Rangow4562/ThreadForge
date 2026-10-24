import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  installOpenCodeProjectPlugin,
  opencodeManagedMarker,
  removeOpenCodeProjectPlugin,
  renderOpenCodePlugin,
} from '../src/opencode-plugin-config.ts';

const packageSpecifier = '@threadforge/adapter-contracts/opencode-plugin';
const pluginPath = (root: string) => path.join(root, '.opencode', 'plugins', 'threadforge.ts');

async function withProject(run: (root: string) => Promise<void>): Promise<void> {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-config-'));
  try { await run(root); }
  finally { await rm(root, { recursive: true, force: true }); }
}

test('installs and idempotently updates an isolated OpenCode plugin file', async () => {
  await withProject(async (root) => {
    assert.equal(await installOpenCodeProjectPlugin(root, packageSpecifier), 'installed');
    const file = pluginPath(root);
    const first = await readFile(file, 'utf8');
    assert.equal(first, renderOpenCodePlugin(packageSpecifier));
    assert.equal(first.includes(opencodeManagedMarker), true);
    assert.equal(await installOpenCodeProjectPlugin(root, packageSpecifier), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);
  });
});

test('renders explicitly configured bounded project-memory retrieval without enabling it by default', () => {
  const defaultPlugin = renderOpenCodePlugin(packageSpecifier);
  const memoryPlugin = renderOpenCodePlugin(packageSpecifier, { projectId: 'parser-project', maxItems: 4, maxTokens: 900 });
  assert.doesNotMatch(defaultPlugin, /experimental\.chat\.system\.transform|memory:/);
  assert.match(memoryPlugin, /projectId":"parser-project"/);
  assert.match(memoryPlugin, /maxItems":4/);
  assert.match(memoryPlugin, /maxTokens":900/);
  assert.match(memoryPlugin, /ThreadforgeOpenCodePlugin\(context/);
  assert.throws(() => renderOpenCodePlugin(packageSpecifier, { projectId: 'API_KEY=secret' }), /project ID/);
  assert.throws(() => renderOpenCodePlugin(packageSpecifier, { projectId: 'parser-project', maxItems: 33 }), /limits/);
  assert.throws(() => renderOpenCodePlugin(packageSpecifier, { projectId: 'parser-project', maxTokens: 0 }), /limits/);
});

test('refuses to overwrite an unmanaged plugin and leaves its bytes unchanged', async () => {
  await withProject(async (root) => {
    const file = pluginPath(root);
    await mkdir(path.dirname(file), { recursive: true });
    const original = 'export const UserPlugin = async () => ({ event() {} });\n';
    await writeFile(file, original);
    await assert.rejects(installOpenCodeProjectPlugin(root, packageSpecifier), /unmanaged/);
    assert.equal(await readFile(file, 'utf8'), original);
  });
});

test('updates and removes only the marked Threadforge plugin file', async () => {
  await withProject(async (root) => {
    assert.equal(await installOpenCodeProjectPlugin(root, packageSpecifier), 'installed');
    assert.equal(await installOpenCodeProjectPlugin(root, 'file:///opt/threadforge/opencode-plugin.ts'), 'updated');
    const sibling = path.join(root, '.opencode', 'plugins', 'user-plugin.ts');
    await writeFile(sibling, 'export const UserPlugin = async () => ({});');
    assert.equal(await removeOpenCodeProjectPlugin(root), 'removed');
    await assert.rejects(readFile(pluginPath(root), 'utf8'), { code: 'ENOENT' });
    assert.equal(await readFile(sibling, 'utf8'), 'export const UserPlugin = async () => ({});');
    assert.equal(await removeOpenCodeProjectPlugin(root), 'absent');
  });
});

test('refuses unsafe module specifiers and symlinked plugin directories', async () => {
  await withProject(async (root) => {
    for (const invalid of ['./relative.ts', 'https://example.test/plugin.ts', 'file:///tmp/plugin.ts?token=x']) {
      await assert.rejects(installOpenCodeProjectPlugin(root, invalid), /specifier/);
    }
    const outside = await mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-outside-'));
    try {
      await symlink(outside, path.join(root, '.opencode'), 'junction');
      await assert.rejects(installOpenCodeProjectPlugin(root, packageSpecifier), /symbolic link/);
    } finally { await rm(outside, { recursive: true, force: true }); }
  });
});
