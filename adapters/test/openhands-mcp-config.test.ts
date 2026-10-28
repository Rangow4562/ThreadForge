import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  getOpenHandsUserMcpServerStatus,
  installOpenHandsUserMcpServer,
  openHandsMcpConfigPath,
  uninstallOpenHandsUserMcpServer,
  updateOpenHandsUserMcpServer,
} from '../src/openhands-mcp-config.ts';

async function withOpenHandsHome(run: (root: string) => Promise<void>): Promise<void> {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-openhands-mcp-'));
  try {
    await run(root);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
}

test('resolves the documented global OpenHands MCP config and validates explicit local paths', () => {
  const home = path.resolve('fixture-openhands-home');
  assert.equal(openHandsMcpConfigPath({ home }), path.join(home, '.openhands', 'mcp.json'));
  const override = path.join(home, 'alternate', 'mcp.json');
  assert.equal(openHandsMcpConfigPath({ configPath: override }), override);
  assert.throws(() => openHandsMcpConfigPath({ configPath: 'relative.json' }), /absolute local path/);
  assert.throws(() => openHandsMcpConfigPath({ configPath: `${home}${path.sep}..${path.sep}outside.json` }), /traversal/);
});

test('installs one global stdio entry, preserves other MCP servers, and never opts into approval', async () => {
  await withOpenHandsHome(async (root) => {
    const configPath = openHandsMcpConfigPath({ configPath: path.join(root, '.openhands', 'mcp.json') });
    await mkdir(path.dirname(configPath), { recursive: true });
    const original = {
      mcpServers: { analytics: { command: 'analytics', args: ['serve'] } },
      telemetry: { enabled: false },
    };
    await writeFile(configPath, JSON.stringify(original, null, 2));
    const command = path.join(root, 'bin', 'threadforge.exe');

    assert.equal(await installOpenHandsUserMcpServer(command, { configPath }), 'installed');
    const first = await readFile(configPath, 'utf8');
    assert.equal(await installOpenHandsUserMcpServer(command, { configPath }), 'unchanged');
    assert.equal(await readFile(configPath, 'utf8'), first);
    const installed = JSON.parse(first);
    assert.deepEqual(installed.telemetry, original.telemetry);
    assert.deepEqual(installed.mcpServers.analytics, original.mcpServers.analytics);
    assert.deepEqual(installed.mcpServers.threadforge, {
      command,
      args: ['mcp', 'serve', '--threadforge-managed-mcp=v1'],
    });
    assert.equal(Object.hasOwn(installed.mcpServers.threadforge, 'env'), false);
    assert.equal(Object.hasOwn(installed.mcpServers.threadforge, 'autoApprove'), false);
    assert.deepEqual(await getOpenHandsUserMcpServerStatus({ configPath }), {
      result: 'installed', server_name: 'threadforge', config_path: configPath, expected_command: command,
    });
  });
});

test('updates and uninstalls only the unchanged Threadforge entry', async () => {
  await withOpenHandsHome(async (root) => {
    const configPath = openHandsMcpConfigPath({ configPath: path.join(root, 'mcp.json') });
    const original = { mcpServers: { analytics: { command: 'analytics', args: ['serve'] } }, setting: true };
    await writeFile(configPath, JSON.stringify(original));
    await installOpenHandsUserMcpServer('threadforge-old', { configPath });

    await assert.rejects(updateOpenHandsUserMcpServer('wrong-current', 'threadforge-next', { configPath }), /refusing to replace/);
    assert.equal(await updateOpenHandsUserMcpServer('threadforge-old', 'threadforge-next', { configPath }), 'updated');
    assert.equal(await updateOpenHandsUserMcpServer('threadforge-next', 'threadforge-next', { configPath }), 'unchanged');
    await assert.rejects(uninstallOpenHandsUserMcpServer('threadforge-old', { configPath }), /refusing to remove/);
    assert.equal(await uninstallOpenHandsUserMcpServer('threadforge-next', { configPath }), 'removed');
    assert.equal(await uninstallOpenHandsUserMcpServer('threadforge-next', { configPath }), 'absent');
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), original);
  });
});

test('refuses collisions, embedded credentials, malformed JSON, and stale locks', async () => {
  await withOpenHandsHome(async (root) => {
    const configPath = openHandsMcpConfigPath({ configPath: path.join(root, 'mcp.json') });
    const collision = { mcpServers: { threadforge: { command: 'user-server', args: ['serve'] } } };
    await writeFile(configPath, JSON.stringify(collision));
    await assert.rejects(installOpenHandsUserMcpServer('threadforge', { configPath }), /already in use/);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), collision);
    await assert.rejects(installOpenHandsUserMcpServer('threadforge api_key=fixture-secret', { configPath }), /without credentials/);

    await writeFile(configPath, '{ invalid json');
    await assert.rejects(installOpenHandsUserMcpServer('threadforge', { configPath }), /strict JSON/);
    await writeFile(configPath, JSON.stringify({ mcpServers: {} }));
    await writeFile(`${configPath}.threadforge.lock`, 'stale');
    await assert.rejects(installOpenHandsUserMcpServer('threadforge', { configPath }), /locked or has a stale/);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), { mcpServers: {} });
  });
});
