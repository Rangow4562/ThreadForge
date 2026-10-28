import assert from 'node:assert/strict';
import { lstat, mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { installQwenProjectMemoryHook, removeQwenProjectMemoryHook } from '../src/qwen-hooks-config.ts';
import {
  installProjectMcpServer,
  managedMcpArgument,
  projectMcpConfigPath,
  removeProjectMcpServer,
  removeManagedProjectMcpServer,
  updateManagedProjectMcpServer,
  updateProjectMcpServer,
  type McpServerConfig,
  type ProjectMcpHost,
} from '../src/mcp-config.ts';

const initial: McpServerConfig = { command: 'threadforge', args: ['mcp', 'serve', '--project-root', '.', managedMcpArgument] };

async function withProject(run: (root: string) => Promise<void>): Promise<void> {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-mcp-'));
  try {
    await run(root);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
}

test('uses project-local config paths for supported MCP hosts', () => {
  const root = path.resolve('fixture-project');
  assert.equal(projectMcpConfigPath(root, 'claude-code'), path.join(root, '.mcp.json'));
  assert.equal(projectMcpConfigPath(root, 'command-code'), path.join(root, '.mcp.json'));
  assert.equal(projectMcpConfigPath(root, 'github-copilot'), path.join(root, '.mcp.json'));
  assert.equal(projectMcpConfigPath(root, 'qoder'), path.join(root, '.mcp.json'));
  assert.equal(projectMcpConfigPath(root, 'kimi-code'), path.join(root, '.kimi-code', 'mcp.json'));
  assert.equal(projectMcpConfigPath(root, 'cursor'), path.join(root, '.cursor', 'mcp.json'));
  assert.equal(projectMcpConfigPath(root, 'antigravity'), path.join(root, '.agents', 'mcp_config.json'));
  assert.equal(projectMcpConfigPath(root, 'gemini-cli'), path.join(root, '.gemini', 'settings.json'));
  assert.equal(projectMcpConfigPath(root, 'qwen-code'), path.join(root, '.qwen', 'settings.json'));
  assert.throws(() => projectMcpConfigPath('.', 'unknown' as ProjectMcpHost), /unsupported/);
});

test('Command Code shares the project .mcp.json contract and preserves adjacent server entries', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'command-code');
    await writeFile(file, JSON.stringify({ mcpServers: { analytics: { command: 'analytics', args: ['serve'] } } }));
    assert.equal(await installProjectMcpServer(root, 'command-code', 'threadforge', initial), 'installed');
    assert.equal(await installProjectMcpServer(root, 'command-code', 'threadforge', initial), 'unchanged');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), {
      mcpServers: {
        analytics: { command: 'analytics', args: ['serve'] },
        threadforge: initial,
      },
    });
    assert.equal(await removeManagedProjectMcpServer(root, 'command-code', 'threadforge'), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), {
      mcpServers: { analytics: { command: 'analytics', args: ['serve'] } },
    });
  });
});

test('GitHub Copilot CLI shares the project .mcp.json contract and preserves adjacent server entries', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'github-copilot');
    await writeFile(file, JSON.stringify({ mcpServers: { analytics: { command: 'analytics', args: ['serve'] } } }));
    assert.equal(await installProjectMcpServer(root, 'github-copilot', 'threadforge', initial), 'installed');
    assert.equal(await installProjectMcpServer(root, 'github-copilot', 'threadforge', initial), 'unchanged');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), {
      mcpServers: {
        analytics: { command: 'analytics', args: ['serve'] },
        threadforge: initial,
      },
    });
    assert.equal(await removeManagedProjectMcpServer(root, 'github-copilot', 'threadforge'), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), {
      mcpServers: { analytics: { command: 'analytics', args: ['serve'] } },
    });
  });
});

test('Qoder project MCP shares .mcp.json and preserves neighboring server entries', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'qoder');
    const original = { mcpServers: { analytics: { command: 'analytics', args: ['serve'] } }, projectSetting: true };
    await writeFile(file, JSON.stringify(original, null, 2));
    const qoderServer: McpServerConfig = {
      command: 'threadforge', args: ['mcp', 'serve', '--project-root', root, managedMcpArgument],
    };

    assert.equal(await installProjectMcpServer(root, 'qoder', 'threadforge', qoderServer), 'installed');
    assert.equal(await installProjectMcpServer(root, 'qoder', 'threadforge', qoderServer), 'unchanged');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), {
      ...original,
      mcpServers: { ...original.mcpServers, threadforge: qoderServer },
    });
    await assert.rejects(installProjectMcpServer(root, 'qoder', 'other', { ...qoderServer, cwd: root }), /only accepts command and args/);
    await assert.rejects(installProjectMcpServer(root, 'qoder', 'other', {
      ...qoderServer, command: 'threadforge api_key=fixture-secret',
    }), /credential-like/);
    assert.equal(await updateManagedProjectMcpServer(root, 'qoder', 'threadforge', {
      ...qoderServer, command: 'threadforge-next',
    }), 'updated');
    assert.equal(await removeManagedProjectMcpServer(root, 'qoder', 'threadforge'), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), original);
  });
});

test('Kimi Code project MCP uses its native project file and rejects credential-like config', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'kimi-code');
    const original = { mcpServers: { analytics: { command: 'analytics', args: ['serve'] } }, projectSetting: true };
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify(original, null, 2));
    const kimiServer: McpServerConfig = {
      command: 'threadforge', args: ['mcp', 'serve', '--project-root', root, managedMcpArgument],
    };

    assert.equal(await installProjectMcpServer(root, 'kimi-code', 'threadforge', kimiServer), 'installed');
    assert.equal(await installProjectMcpServer(root, 'kimi-code', 'threadforge', kimiServer), 'unchanged');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), {
      ...original,
      mcpServers: { ...original.mcpServers, threadforge: kimiServer },
    });
    await assert.rejects(installProjectMcpServer(root, 'kimi-code', 'other', { ...kimiServer, cwd: root }), /only accepts command and args/);
    await assert.rejects(installProjectMcpServer(root, 'kimi-code', 'other', {
      ...kimiServer, args: [...kimiServer.args, 'api_key=fixture-secret'],
    }), /credential-like/);
    assert.equal(await updateManagedProjectMcpServer(root, 'kimi-code', 'threadforge', {
      ...kimiServer, command: 'threadforge-next',
    }), 'updated');
    assert.equal(await removeManagedProjectMcpServer(root, 'kimi-code', 'threadforge'), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), original);
  });
});

test('installs idempotently and preserves unrelated server and settings keys', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'cursor');
    await mkdir(path.dirname(file), { recursive: true });
    const before = {
      mcpServers: { analytics: { command: 'analytics', args: ['serve'] } },
      editor: { theme: 'dark', enabled: true },
    };
    await writeFile(file, JSON.stringify(before, null, 2));

    assert.equal(await installProjectMcpServer(root, 'cursor', 'threadforge', initial), 'installed');
    const first = await readFile(file, 'utf8');
    assert.equal(await installProjectMcpServer(root, 'cursor', 'threadforge', initial), 'unchanged');
    assert.equal(await readFile(file, 'utf8'), first);
    assert.deepEqual(JSON.parse(first), {
      ...before,
      mcpServers: { ...before.mcpServers, threadforge: initial },
    });
  });
});

test('installs Gemini MCP in project settings without enabling host trust', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'gemini-cli');
    await mkdir(path.dirname(file), { recursive: true });
    const before = {
      model: { name: 'user-model' },
      telemetry: { enabled: false },
      mcpServers: { analytics: { command: 'analytics', args: ['serve'] } },
    };
    await writeFile(file, JSON.stringify(before));
    const threadforge: McpServerConfig = { ...initial, trust: false };

    assert.equal(await installProjectMcpServer(root, 'gemini-cli', 'threadforge', threadforge), 'installed');
    assert.equal(await installProjectMcpServer(root, 'gemini-cli', 'threadforge', threadforge), 'unchanged');
    const installed = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(installed.model, before.model);
    assert.deepEqual(installed.telemetry, before.telemetry);
    assert.deepEqual(installed.mcpServers.analytics, before.mcpServers.analytics);
    assert.deepEqual(installed.mcpServers.threadforge, threadforge);
    assert.equal(installed.mcpServers.threadforge.trust, false);
    assert.equal(await removeManagedProjectMcpServer(root, 'gemini-cli', 'threadforge'), 'removed');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), before);
  });
});

test('Qwen project MCP shares the settings lock with hooks and preserves all unrelated settings', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'qwen-code');
    await mkdir(path.dirname(file), { recursive: true });
    const original = {
      model: { name: 'user-model' },
      hooks: { SessionStart: [{ hooks: [{ type: 'command', command: 'user-hook' }] }] },
      mcpServers: { analytics: { command: 'analytics', args: ['serve'] } },
    };
    await writeFile(file, JSON.stringify(original, null, 2));
    const qwenServer: McpServerConfig = {
      command: 'threadforge', args: ['mcp', 'serve', '--project-root', root, managedMcpArgument],
    };

    assert.equal(await installProjectMcpServer(root, 'qwen-code', 'threadforge', qwenServer), 'installed');
    assert.equal(await installProjectMcpServer(root, 'qwen-code', 'threadforge', qwenServer), 'unchanged');
    let settings = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(settings.model, original.model);
    assert.deepEqual(settings.hooks, original.hooks);
    assert.deepEqual(settings.mcpServers.analytics, original.mcpServers.analytics);
    assert.deepEqual(settings.mcpServers.threadforge, qwenServer);
    assert.equal(Object.hasOwn(settings.mcpServers.threadforge, 'trust'), false);

    assert.equal(await installQwenProjectMemoryHook({
      projectRoot: root,
      command: 'threadforge memory inject-qwen --project-id demo --threadforge-managed-qwen-memory=v1 --threadforge-memory=qwen-v1',
    }), 'installed');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(settings.mcpServers.threadforge, qwenServer);
    assert.equal(settings.hooks.UserPromptExpansion.length, 1);

    const updated: McpServerConfig = {
      command: 'threadforge-next', args: ['mcp', 'serve', '--project-root', root, managedMcpArgument],
    };
    assert.equal(await updateManagedProjectMcpServer(root, 'qwen-code', 'threadforge', updated), 'updated');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(settings.mcpServers.threadforge, updated);
    assert.equal(settings.hooks.UserPromptExpansion.length, 1);

    assert.equal(await removeManagedProjectMcpServer(root, 'qwen-code', 'threadforge'), 'removed');
    assert.equal(await removeQwenProjectMemoryHook(root), 'removed');
    settings = JSON.parse(await readFile(file, 'utf8'));
    assert.deepEqual(settings, original);
  });
});

test('Qwen MCP install fails closed for unowned collisions, removed markers, unsafe values, and shared-lock contention', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'qwen-code');
    await mkdir(path.dirname(file), { recursive: true });
    const userOwned = { mcpServers: { threadforge: { command: 'user-server', args: ['serve'] } } };
    await writeFile(file, JSON.stringify(userOwned));
    const server: McpServerConfig = {
      command: 'threadforge', args: ['mcp', 'serve', '--project-root', root, managedMcpArgument],
    };

    await assert.rejects(installProjectMcpServer(root, 'qwen-code', 'threadforge', server), /refusing to overwrite/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), userOwned);

    const withoutMarker = { mcpServers: { threadforge: { command: 'threadforge', args: ['mcp', 'serve'] } } };
    await writeFile(file, JSON.stringify(withoutMarker));
    await assert.rejects(removeManagedProjectMcpServer(root, 'qwen-code', 'threadforge'), /not marked as Threadforge-managed/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), withoutMarker);

    await assert.rejects(installProjectMcpServer(root, 'qwen-code', 'other', {
      command: 'threadforge api_key=fixture-secret', args: server.args,
    }), /credential-like/);
    await assert.rejects(installProjectMcpServer(root, 'qwen-code', 'other', { ...server, cwd: root }), /only accepts command and args/);

    await writeFile(path.join(path.dirname(file), '.threadforge-qwen-hooks.lock'), 'locked');
    await assert.rejects(installProjectMcpServer(root, 'qwen-code', 'other', server), /Qwen settings are locked/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), withoutMarker);
  });
});

test('Qwen absent removal is read-only, updates never create settings, and invalid install creates no directory', async () => {
  await withProject(async (root) => {
    const qwenDirectory = path.join(root, '.qwen');
    assert.equal(await removeManagedProjectMcpServer(root, 'qwen-code', 'threadforge'), 'absent');
    await assert.rejects(lstat(qwenDirectory));
    await assert.rejects(updateManagedProjectMcpServer(root, 'qwen-code', 'threadforge', {
      command: 'threadforge', args: ['mcp', 'serve', '--project-root', root, managedMcpArgument],
    }), /settings are absent/);
    await assert.rejects(installProjectMcpServer(root, 'qwen-code', 'threadforge', {
      command: 'threadforge api_key=fixture-secret',
      args: ['mcp', 'serve', '--project-root', root, managedMcpArgument],
    }), /credential-like/);
    await assert.rejects(lstat(qwenDirectory));
  });
});

test('refuses overwrite conflicts, but supports compare-and-swap update and removal', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'claude-code');
    await installProjectMcpServer(root, 'claude-code', 'threadforge', initial);
    const current = JSON.parse(await readFile(file, 'utf8')).mcpServers.threadforge as McpServerConfig;
    const next: McpServerConfig = { command: 'threadforge', args: ['mcp', 'serve', '--project-root', '.', '--read-only', managedMcpArgument] };

    await assert.rejects(installProjectMcpServer(root, 'claude-code', 'threadforge', next), /refusing to overwrite/);
    await assert.rejects(updateProjectMcpServer(root, 'claude-code', 'threadforge', { ...current, args: [] }, next), /changed since it was read/);
    assert.equal(await updateProjectMcpServer(root, 'claude-code', 'threadforge', { args: current.args, command: current.command }, next), 'updated');
    assert.equal(await removeProjectMcpServer(root, 'claude-code', 'threadforge', next), 'removed');
    assert.equal(await removeProjectMcpServer(root, 'claude-code', 'threadforge', next), 'absent');
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), {});
  });
});

test('refuses removal if the entry changed after the caller read it', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'antigravity');
    await installProjectMcpServer(root, 'antigravity', 'threadforge', initial);
    const changed = { command: 'custom-threadforge-wrapper', args: ['mcp', 'serve'] };
    await writeFile(file, JSON.stringify({ mcpServers: { threadforge: changed }, custom: true }));
    await assert.rejects(removeProjectMcpServer(root, 'antigravity', 'threadforge', initial), /refusing to remove/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), { mcpServers: { threadforge: changed }, custom: true });
  });
});

test('leaves malformed or comment-bearing JSON untouched', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'cursor');
    await mkdir(path.dirname(file), { recursive: true });
    const content = '{\n  // user comment\n  "mcpServers": {}\n}\n';
    await writeFile(file, content);
    await assert.rejects(installProjectMcpServer(root, 'cursor', 'threadforge', initial), /strict JSON/);
    assert.equal(await readFile(file, 'utf8'), content);
  });
});

test('refuses oversized configuration without reading it into the merge path', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'claude-code');
    await writeFile(file, ' '.repeat((8 << 20) + 1));
    await assert.rejects(installProjectMcpServer(root, 'claude-code', 'threadforge', initial), /size limit/);
    assert.equal((await readFile(file, 'utf8')).length, (8 << 20) + 1);
  });
});

test('refuses symlinked host config directories', async () => {
  await withProject(async (root) => {
    const outside = await mkdtemp(path.join(os.tmpdir(), 'threadforge-outside-'));
    try {
      await symlink(outside, path.join(root, '.cursor'), 'junction');
      await assert.rejects(installProjectMcpServer(root, 'cursor', 'threadforge', initial), /symbolic link/);
    } finally {
      await rm(outside, { recursive: true, force: true });
    }
  });
});

test('fails closed when another writer holds the config lock', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'cursor');
    await mkdir(path.dirname(file), { recursive: true });
    const lock = path.join(path.dirname(file), '.threadforge-mcp-config.lock');
    await writeFile(lock, '');
    await assert.rejects(installProjectMcpServer(root, 'cursor', 'threadforge', initial), /locked by another installer/);
    assert.equal(await readFile(lock, 'utf8'), '');
    await assert.rejects(readFile(file, 'utf8'), { code: 'ENOENT' });
  });
});

test('does not permit environment fields that could carry credentials', async () => {
  await withProject(async (root) => {
    await assert.rejects(installProjectMcpServer(root, 'claude-code', 'threadforge', {
      ...initial,
      env: { API_TOKEN: 'secret' },
    } as McpServerConfig), /environment secrets are not accepted/);
    await assert.rejects(readFile(projectMcpConfigPath(root, 'claude-code'), 'utf8'), { code: 'ENOENT' });
  });
});

test('rejects Gemini MCP trust bypass without modifying config', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'gemini-cli');
    const userConfig = { telemetry: { enabled: false }, mcpServers: { analytics: { command: 'a', args: [] } } };
    await mkdir(path.dirname(file), { recursive: true });
    await writeFile(file, JSON.stringify(userConfig));
    await assert.rejects(installProjectMcpServer(root, 'gemini-cli', 'threadforge', { ...initial, trust: true } as unknown as McpServerConfig), /trust must remain false/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')), userConfig);

    const managed: McpServerConfig = { ...initial, trust: false };
    await installProjectMcpServer(root, 'gemini-cli', 'threadforge', managed);
    const userChangedTrust = { ...managed, trust: true };
    await writeFile(file, JSON.stringify({ ...userConfig, mcpServers: { ...userConfig.mcpServers, threadforge: userChangedTrust } }));
    const changed = await readFile(file, 'utf8');
    await assert.rejects(updateManagedProjectMcpServer(root, 'gemini-cli', 'threadforge', managed), /trust changed/);
    await assert.rejects(removeManagedProjectMcpServer(root, 'gemini-cli', 'threadforge'), /trust changed/);
    assert.equal(await readFile(file, 'utf8'), changed);
  });
});

test('update and uninstall require the explicit Threadforge ownership marker', async () => {
  await withProject(async (root) => {
    const file = projectMcpConfigPath(root, 'cursor');
    await mkdir(path.dirname(file), { recursive: true });
    const userEntry = { command: 'threadforge', args: ['mcp', 'serve'] };
    await writeFile(file, JSON.stringify({ mcpServers: { threadforge: userEntry, other: { command: 'other', args: [] } } }));
    const next = { ...initial, args: [...initial.args] };
    await assert.rejects(updateManagedProjectMcpServer(root, 'cursor', 'threadforge', next), /not marked as Threadforge-managed/);
    await assert.rejects(removeManagedProjectMcpServer(root, 'cursor', 'threadforge'), /not marked as Threadforge-managed/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')).mcpServers.threadforge, userEntry);
    await assert.rejects(removeManagedProjectMcpServer(root, 'cursor', 'other'), /not marked as Threadforge-managed/);
    assert.deepEqual(JSON.parse(await readFile(file, 'utf8')).mcpServers.other, { command: 'other', args: [] });
  });
});
