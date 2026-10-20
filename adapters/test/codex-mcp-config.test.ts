import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  installCodexProjectMcpServer,
  removeCodexProjectMcpServer,
  updateCodexProjectMcpServer,
} from '../src/codex-mcp-config.ts';
import { managedMcpArgument } from '../src/mcp-config.ts';

async function createProject(): Promise<string> {
  return mkdtemp(path.join(os.tmpdir(), 'threadforge-codex-mcp-'));
}

function args(root: string): string[] {
  return ['mcp', 'serve', '--project-root', root, managedMcpArgument];
}

test('installs a managed project Codex MCP table and restores original TOML bytes', async () => {
  const root = await createProject();
  const directory = path.join(root, '.codex');
  const configPath = path.join(directory, 'config.toml');
  try {
    await mkdir(directory);
    const original = '# user comment\r\nmodel = "keep"\r\n\r\n[mcp_servers.analytics]\r\ncommand = "analytics"\r\nargs = ["serve"]';
    await writeFile(configPath, original, 'utf8');

    assert.equal(await installCodexProjectMcpServer(root, 'C:\\Tools\\threadforge.exe', args(root)), 'installed');
    const installed = await readFile(configPath, 'utf8');
    assert.ok(installed.startsWith(original + '\r\n'));
    assert.match(installed, /\[mcp_servers\.threadforge\]\r\ncommand = "C:\\\\Tools\\\\threadforge\.exe"\r\nargs = \["mcp", "serve", "--project-root",/);
    assert.match(installed, /--threadforge-managed-mcp=v1/);
    assert.match(installed, /\[mcp_servers\.analytics\]/);
    assert.equal(await installCodexProjectMcpServer(root, 'C:\\Tools\\threadforge.exe', args(root)), 'unchanged');

    assert.equal(await updateCodexProjectMcpServer(root, 'threadforge', args(root)), 'updated');
    assert.match(await readFile(configPath, 'utf8'), /command = "threadforge"/);
    assert.equal(await removeCodexProjectMcpServer(root), 'removed');
    assert.equal(await readFile(configPath, 'utf8'), original);
    assert.equal(await removeCodexProjectMcpServer(root), 'absent');
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('preserves multiline values containing MCP-looking strings and appends to existing server tables', async () => {
  const root = await createProject();
  const directory = path.join(root, '.codex');
  const configPath = path.join(directory, 'config.toml');
  try {
    await mkdir(directory);
    const original = 'model = [\n  "gpt-5",\n  "[mcp_servers.threadforge]"\n]\nnote = """\n[mcp_servers.threadforge]\n"""\n[mcp_servers]\nanalytics = { command = "analytics", args = ["serve"] }\n';
    await writeFile(configPath, original, 'utf8');
    assert.equal(await installCodexProjectMcpServer(root, 'threadforge', args(root)), 'installed');
    assert.equal(await removeCodexProjectMcpServer(root), 'removed');
    assert.equal(await readFile(configPath, 'utf8'), original);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('refuses unowned threadforge tables, inline mcp_servers values, and edits to the managed block', async () => {
  const root = await createProject();
  const directory = path.join(root, '.codex');
  const configPath = path.join(directory, 'config.toml');
  try {
    await mkdir(directory);
    for (const original of [
      '[mcp_servers.threadforge]\ncommand = "user-server"\nargs = []\n',
      '[mcp_servers."threadforge"]\ncommand = "user-server"\nargs = []\n',
      '[[mcp_servers.threadforge]]\ncommand = "user-server"\nargs = []\n',
      'mcp_servers.threadforge = { command = "user-server", args = [] }\n',
      '[mcp_servers]\nthreadforge = { command = "user-server", args = [] }\n',
      'mcp_servers = { threadforge = { command = "user-server" } }\n',
    ]) {
      await writeFile(configPath, original, 'utf8');
      await assert.rejects(installCodexProjectMcpServer(root, 'threadforge', args(root)), /already exists|inline mcp_servers/);
      assert.equal(await readFile(configPath, 'utf8'), original);
    }

    await writeFile(configPath, 'model = "keep"\n', 'utf8');
    await installCodexProjectMcpServer(root, 'threadforge', args(root));
    const changed = (await readFile(configPath, 'utf8')).replace('command = "threadforge"', 'command = "changed"');
    await writeFile(configPath, changed, 'utf8');
    await assert.rejects(updateCodexProjectMcpServer(root, 'threadforge', args(root)), /was edited/);
    await assert.rejects(removeCodexProjectMcpServer(root), /was edited/);
    assert.equal(await readFile(configPath, 'utf8'), changed);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('uninstall preserves unrelated TOML appended after the owned table', async () => {
  const root = await createProject();
  const directory = path.join(root, '.codex');
  const configPath = path.join(directory, 'config.toml');
  try {
    await mkdir(directory);
    const original = 'model = "keep"\n';
    await writeFile(configPath, original, 'utf8');
    await installCodexProjectMcpServer(root, 'threadforge', args(root));
    const appended = '[profiles.fast]\nmodel = "fast-model"\n';
    await writeFile(configPath, `${await readFile(configPath, 'utf8')}${appended}`, 'utf8');
    assert.equal(await removeCodexProjectMcpServer(root), 'removed');
    assert.equal(await readFile(configPath, 'utf8'), `${original}${appended}`);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('rejects unsafe paths, invalid arguments, stale locks, and credential-like command values', async () => {
  const root = await createProject();
  const directory = path.join(root, '.codex');
  try {
    await mkdir(directory);
    await assert.rejects(installCodexProjectMcpServer(root, 'API_TOKEN=secret-value', args(root)), /credential/);
    await assert.rejects(installCodexProjectMcpServer(root, 'threadforge', ['mcp', 'serve', '--project-root', path.join(root, 'other'), managedMcpArgument]), /project-root/);
    await writeFile(path.join(directory, '.threadforge-mcp-config.lock'), 'stale', 'utf8');
    await assert.rejects(installCodexProjectMcpServer(root, 'threadforge', args(root)), /locked/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test('refuses a symlinked project .codex directory', async () => {
  const root = await createProject();
  const outside = await createProject();
  try {
    await symlink(outside, path.join(root, '.codex'), 'junction');
    const protectedFile = path.join(outside, 'config.toml');
    const original = 'model = "protected"\n';
    await writeFile(protectedFile, original, 'utf8');
    await assert.rejects(installCodexProjectMcpServer(root, 'threadforge', args(root)), /symbolic link/);
    assert.equal(await readFile(protectedFile, 'utf8'), original);
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});
