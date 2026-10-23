import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  clineMcpSettingsPath,
  getClineUserMcpServerStatus,
  installClineUserMcpServer,
  uninstallClineUserMcpServer,
  updateClineUserMcpServer,
} from '../src/cline-mcp-config.ts';

async function withClineHome(run: (root: string) => Promise<void>): Promise<void> {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-mcp-'));
  try {
    await run(root);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
}

test('resolves the documented Cline MCP file and honors explicit local path overrides', () => {
  const home = path.resolve('fixture-home');
  assert.equal(
    clineMcpSettingsPath({ home, env: {} }),
    path.join(home, '.cline', 'data', 'settings', 'cline_mcp_settings.json'),
  );
  assert.equal(
    clineMcpSettingsPath({ clineDir: path.join(home, 'alternate-cline-home'), env: {} }),
    path.join(home, 'alternate-cline-home', 'data', 'settings', 'cline_mcp_settings.json'),
  );
  const override = path.join(home, 'custom', 'cline_mcp_settings.json');
  assert.equal(clineMcpSettingsPath({ home, env: { CLINE_MCP_SETTINGS_PATH: override } }), override);
  assert.equal(clineMcpSettingsPath({ home, configPath: override, env: {} }), override);
  assert.equal(
    clineMcpSettingsPath({ clineDir: path.join(home, 'explicit-cline'), env: { CLINE_MCP_SETTINGS_PATH: override } }),
    path.join(home, 'explicit-cline', 'data', 'settings', 'cline_mcp_settings.json'),
  );
  assert.throws(() => clineMcpSettingsPath({ configPath: 'relative.json', env: {} }), /absolute local path/);
  assert.throws(() => clineMcpSettingsPath({ configPath: `${home}${path.sep}..${path.sep}outside.json`, env: {} }), /traversal/);
});

test('installs a user-global MCP entry without changing auto-approval or unrelated Cline settings', async () => {
  await withClineHome(async (root) => {
    const configPath = clineMcpSettingsPath({ clineDir: root, env: {} });
    await assert.rejects(installClineUserMcpServer('API_TOKEN=never-store-this', { clineDir: root, env: {} }), /bounded command/);
    await mkdir(path.dirname(configPath), { recursive: true });
    const original = {
      mcpServers: { analytics: { command: 'analytics', args: ['serve'], disabled: false } },
      telemetry: { enabled: false },
      welcomeViewCompleted: true,
    };
    await writeFile(configPath, JSON.stringify(original, null, 2));
    const command = path.join(root, 'bin', 'threadforge.exe');

    assert.equal(await installClineUserMcpServer(command, { clineDir: root, env: {} }), 'installed');
    const first = await readFile(configPath, 'utf8');
    assert.equal(await installClineUserMcpServer(command, { clineDir: root, env: {} }), 'unchanged');
    assert.equal(await readFile(configPath, 'utf8'), first);
    const installed = JSON.parse(first);
    assert.deepEqual(installed.telemetry, original.telemetry);
    assert.equal(installed.welcomeViewCompleted, true);
    assert.deepEqual(installed.mcpServers.analytics, original.mcpServers.analytics);
    assert.deepEqual(installed.mcpServers.threadforge, {
      command,
      args: ['mcp', 'serve', '--threadforge-managed-mcp=v1'],
      disabled: false,
      autoApprove: [],
    });

    assert.deepEqual(await getClineUserMcpServerStatus({ clineDir: root, env: {} }), {
      result: 'installed', server_name: 'threadforge', config_path: configPath,
      expected_command: command,
    });
  });
});

test('updates only the owned entry and uninstall preserves other Cline servers', async () => {
  await withClineHome(async (root) => {
    const configPath = clineMcpSettingsPath({ clineDir: root, env: {} });
    const before = { mcpServers: { analytics: { command: 'analytics', args: ['serve'] } }, other: 'preserved' };
    await mkdir(path.dirname(configPath), { recursive: true });
    await writeFile(configPath, JSON.stringify(before));
    await installClineUserMcpServer('threadforge-old', { clineDir: root, env: {} });
    const withThreadforge = JSON.parse(await readFile(configPath, 'utf8'));
    assert.deepEqual(withThreadforge.mcpServers.analytics, before.mcpServers.analytics);

    await assert.rejects(updateClineUserMcpServer('wrong-current-command', 'threadforge-next', { clineDir: root, env: {} }), /refusing to replace/);
    assert.equal(await updateClineUserMcpServer('threadforge-old', 'threadforge-new', { clineDir: root, env: {} }), 'updated');
    assert.equal(await updateClineUserMcpServer('threadforge-new', 'threadforge-new', { clineDir: root, env: {} }), 'unchanged');
    await assert.rejects(uninstallClineUserMcpServer('threadforge-old', { clineDir: root, env: {} }), /refusing to remove/);
    assert.equal(await uninstallClineUserMcpServer('threadforge-new', { clineDir: root, env: {} }), 'removed');
    assert.equal(await uninstallClineUserMcpServer('threadforge-new', { clineDir: root, env: {} }), 'absent');
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), before);
  });
});

test('refuses collisions, modified managed entries, malformed files, and stale locks', async () => {
  await withClineHome(async (root) => {
    const configPath = clineMcpSettingsPath({ clineDir: root, env: {} });
    await mkdir(path.dirname(configPath), { recursive: true });
    const conflict = { mcpServers: { threadforge: { command: 'user-server', args: ['serve'] } } };
    await writeFile(configPath, JSON.stringify(conflict));
    await assert.rejects(installClineUserMcpServer('threadforge', { clineDir: root, env: {} }), /already in use/);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), conflict);

    await writeFile(configPath, JSON.stringify({ mcpServers: { threadforge: {
      command: 'threadforge', args: ['mcp', 'serve', '--threadforge-managed-mcp=v1'],
      disabled: true, autoApprove: [],
    } } }));
    assert.equal((await getClineUserMcpServerStatus({ clineDir: root, env: {} })).result, 'modified');
    await assert.rejects(uninstallClineUserMcpServer('threadforge', { clineDir: root, env: {} }), /refusing to remove/);

    await writeFile(configPath, '{ invalid json');
    await assert.rejects(installClineUserMcpServer('threadforge', { clineDir: root, env: {} }), /strict JSON/);
    await writeFile(configPath, JSON.stringify({ mcpServers: {} }));
    await writeFile(`${configPath}.threadforge.lock`, 'stale');
    await assert.rejects(installClineUserMcpServer('threadforge', { clineDir: root, env: {} }), /locked or have a stale/);
    assert.deepEqual(JSON.parse(await readFile(configPath, 'utf8')), { mcpServers: {} });
  });
});
