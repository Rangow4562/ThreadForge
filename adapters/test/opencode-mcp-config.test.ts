import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  installOpenCodeProjectMcpServer,
  openCodeMcpConfigPath,
  removeManagedOpenCodeProjectMcpServer,
  updateManagedOpenCodeProjectMcpServer,
} from '../src/opencode-mcp-config.ts';
import { managedMcpArgument } from '../src/mcp-config.ts';

async function withProject(run: (root: string) => Promise<void>): Promise<void> {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-opencode-mcp-'));
  try { await run(root); }
  finally { await rm(root, { recursive: true, force: true }); }
}

function args(root: string): string[] {
  return ['mcp', 'serve', '--project-root', root, managedMcpArgument];
}

test('manages the OpenCode local MCP schema while preserving unrelated project config', async () => {
  await withProject(async (root) => {
    const file = openCodeMcpConfigPath(root);
    const before = { model: 'user/model', permission: { bash: 'ask' }, mcp: { analytics: { type: 'local', command: ['analytics'], enabled: true } } };
    await writeFile(file, JSON.stringify(before), 'utf8');
    assert.equal(await installOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge.exe', args(root)), 'installed');
    const installedText = await readFile(file, 'utf8');
    assert.equal(await installOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge.exe', args(root)), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), installedText);
    const installed = JSON.parse(installedText);
    assert.deepEqual(installed.model, before.model);
    assert.deepEqual(installed.permission, before.permission);
    assert.deepEqual(installed.mcp.analytics, before.mcp.analytics);
    assert.deepEqual(installed.mcp.threadforge, {
      type: 'local', command: ['threadforge.exe', ...args(root)], enabled: true,
    });

    assert.equal(await updateManagedOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge-next.exe', args(root)), 'updated');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')).mcp.threadforge.command, ['threadforge-next.exe', ...args(root)]);
    assert.equal(await removeManagedOpenCodeProjectMcpServer(root, 'threadforge'), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), before);
  });
});

test('refuses unowned conflicts and preserves user edits to a marked server', async () => {
  await withProject(async (root) => {
    const file = openCodeMcpConfigPath(root);
    const userConfig = { mcp: { threadforge: { type: 'local', command: ['other'], enabled: true } } };
    await writeFile(file, JSON.stringify(userConfig), 'utf8');
    await assert.rejects(installOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge', args(root)), /refusing to overwrite/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), userConfig);

    await writeFile(file, '{}', 'utf8');
    await installOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge', args(root));
    const edited = JSON.parse(await readFile(file, 'utf8'));
    edited.mcp.threadforge.environment = { TOKEN: 'user-value' };
    await writeFile(file, JSON.stringify(edited), 'utf8');
    const bytes = await readFile(file, 'utf8');
    await assert.rejects(updateManagedOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge-new', args(root)), /unchanged Threadforge entry/);
    await assert.rejects(removeManagedOpenCodeProjectMcpServer(root, 'threadforge'), /unchanged Threadforge entry/);
    assert.equal(await readFile(file, 'utf8'), bytes);
  });
});

test('refuses JSONC, a mismatched project root, oversized input, and stale locks safely', async () => {
  await withProject(async (root) => {
    const file = openCodeMcpConfigPath(root);
    const jsonc = path.join(root, 'opencode.jsonc');
    const jsoncBytes = '{\n  // keep my comments\n  "model": "user/model"\n}\n';
    await writeFile(jsonc, jsoncBytes, 'utf8');
    await assert.rejects(installOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge', args(root)), /opencode.jsonc is present/);
    assert.equal(await readFile(jsonc, 'utf8'), jsoncBytes);
    await rm(jsonc);

    await assert.rejects(installOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge', ['mcp', 'serve', '--project-root', os.tmpdir(), managedMcpArgument]), /must match the selected project root/);
    await writeFile(file, ' '.repeat((8 << 20) + 1), 'utf8');
    await assert.rejects(installOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge', args(root)), /size limit/);
    await rm(file);

    const lock = path.join(root, '.threadforge-opencode-mcp.lock');
    await writeFile(lock, '');
    await assert.rejects(installOpenCodeProjectMcpServer(root, 'threadforge', 'threadforge', args(root)), /locked by another writer/);
  });
});
