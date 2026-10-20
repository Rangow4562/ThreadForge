import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';
import {
  clinePluginPath,
  getClinePluginStatus,
  installClinePlugin,
  uninstallClinePlugin,
  updateClinePlugin,
} from '../src/cline-plugin-config.ts';

const digest = (value: string) => createHash('sha256').update(value).digest('hex');

test('Cline plugin install is idempotent, scope-isolated, and removes only its owned wrapper', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-plugin-'));
  const userRoot = path.join(root, 'cline-home');
  const projectRoot = path.join(root, 'project');
  await mkdir(projectRoot);
  try {
    const userOptions = { scope: 'user' as const, clineDir: userRoot };
    const first = await installClinePlugin(userOptions);
    assert.equal(first.result, 'installed');
    assert.equal(first.plugin_path, clinePluginPath('user', userRoot));
    const wrapper = await readFile(first.plugin_path!, 'utf8');
    assert.match(wrapper, /^\/\/ threadforge-managed-cline-plugin:v1 sha256=[a-f0-9]{64}\n/);
    assert.match(wrapper, /import plugin from "file:/);
    assert.match(wrapper, /export default plugin;/);
    assert.equal((await installClinePlugin(userOptions)).result, 'unchanged');
    assert.equal((await getClinePluginStatus(userOptions)).result, 'installed');

    const projectOptions = { scope: 'project' as const, projectRoot };
    const project = await installClinePlugin(projectOptions);
    assert.equal(project.result, 'installed');
    assert.equal(project.plugin_path, clinePluginPath('project', projectRoot));
    assert.equal((await getClinePluginStatus(userOptions)).result, 'installed');
    assert.equal((await uninstallClinePlugin(projectOptions)).result, 'removed');
    assert.equal((await getClinePluginStatus(projectOptions)).result, 'absent');
    assert.equal((await uninstallClinePlugin(userOptions)).result, 'removed');
    assert.equal((await getClinePluginStatus(userOptions)).result, 'absent');
    await assert.rejects(readFile(path.join(projectRoot, '.cline', 'settings.json')), /ENOENT/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Cline plugin install upgrades a valid older Threadforge wrapper atomically', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-plugin-update-'));
  const clineDir = path.join(root, '.cline');
  const pluginPath = clinePluginPath('user', clineDir);
  try {
    await mkdir(path.dirname(pluginPath), { recursive: true });
    const source = pathToFileURL(fileURLToPath(new URL('../src/cline-plugin.ts', import.meta.url))).href;
    const body = `// Threadforge observational Cline plugin wrapper; callbacks are non-authorizing.\n// threadforge-module-sha256:${'0'.repeat(64)}\nimport plugin from ${JSON.stringify(source)};\nexport default plugin;\n`;
    await writeFile(pluginPath, `// threadforge-managed-cline-plugin:v1 sha256=${digest(body)}\n${body}`);

    const result = await updateClinePlugin({ scope: 'user', clineDir });
    assert.equal(result.result, 'updated');
    assert.notEqual(result.module_sha256, '0'.repeat(64));
    assert.equal((await getClinePluginStatus({ scope: 'user', clineDir })).result, 'installed');
  } finally { await rm(root, { recursive: true, force: true }); }
});

test('Cline plugin manager refuses unmanaged collisions and edited managed files', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-plugin-conflict-'));
  const clineDir = path.join(root, '.cline');
  const pluginPath = clinePluginPath('user', clineDir);
  try {
    await mkdir(path.dirname(pluginPath), { recursive: true });
    await writeFile(pluginPath, '// another plugin\nexport default {};\n');
    assert.equal((await getClinePluginStatus({ scope: 'user', clineDir })).result, 'unmanaged');
    await assert.rejects(installClinePlugin({ scope: 'user', clineDir }), /unmanaged/);
    assert.equal(await readFile(pluginPath, 'utf8'), '// another plugin\nexport default {};\n');

    await rm(pluginPath);
    await installClinePlugin({ scope: 'user', clineDir });
    const installed = await readFile(pluginPath, 'utf8');
    await writeFile(pluginPath, `${installed}// local edit\n`);
    assert.equal((await getClinePluginStatus({ scope: 'user', clineDir })).result, 'modified');
    await assert.rejects(uninstallClinePlugin({ scope: 'user', clineDir }), /edited/);
    assert.match(await readFile(pluginPath, 'utf8'), /local edit/);
  } finally { await rm(root, { recursive: true, force: true }); }
});
