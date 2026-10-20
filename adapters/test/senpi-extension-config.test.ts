import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  getSenpiExtensionState,
  installSenpiExtension,
  removeSenpiExtension,
  senpiExtensionPath,
  updateSenpiExtension,
} from '../src/senpi-extension-config.ts';
import { senpiManagedMarker } from '../src/senpi-extension.ts';

const environmentKeys = ['OMO_CODING_AGENT_DIR', 'SENPI_CODING_AGENT_DIR', 'PI_CODING_AGENT_DIR'] as const;

async function temp(): Promise<string> { return mkdtemp(path.join(os.tmpdir(), 'threadforge-senpi-')); }

async function withSenpiEnvironment(action: () => Promise<void>): Promise<void> {
  const previous = new Map(environmentKeys.map((key) => [key, process.env[key]]));
  try {
    for (const key of environmentKeys) delete process.env[key];
    await action();
  } finally {
    for (const key of environmentKeys) {
      const value = previous.get(key);
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

test('user install is idempotent, owns only threadforge.ts, and removes only unchanged managed content', async () => {
  await withSenpiEnvironment(async () => {
    const home = await temp();
    try {
      const directory = path.join(home, '.senpi', 'agent', 'extensions');
      await mkdir(directory, { recursive: true });
      await writeFile(path.join(directory, 'user-extension.ts'), 'export default () => {};', 'utf8');
      const file = senpiExtensionPath('user', path.join(home, '.senpi', 'agent'));

      assert.equal(await installSenpiExtension({ homeDir: home }), 'installed');
      const original = await readFile(file, 'utf8');
      assert.match(original.split('\n')[0]!, new RegExp(`^${senpiManagedMarker} sha256=[a-f0-9]{64}$`));
      assert.match(original, /session_start/);
      assert.equal(await readFile(path.join(directory, 'user-extension.ts'), 'utf8'), 'export default () => {};');
      assert.equal(await getSenpiExtensionState({ homeDir: home }), 'installed');
      if (process.platform !== 'win32') assert.equal((await stat(directory)).mode & 0o777, 0o700);
      assert.equal(await installSenpiExtension({ homeDir: home }), 'unchanged');
      assert.equal(await updateSenpiExtension({ homeDir: home }), 'unchanged');

      await writeFile(file, `${original}// local edit\n`, 'utf8');
      assert.equal(await getSenpiExtensionState({ homeDir: home }), 'modified_or_unowned');
      await assert.rejects(updateSenpiExtension({ homeDir: home }), /unowned or was modified/);
      await assert.rejects(removeSenpiExtension({ homeDir: home }), /unowned or was modified/);
      await writeFile(file, original, 'utf8');
      assert.equal(await removeSenpiExtension({ homeDir: home }), 'removed');
      assert.equal(await getSenpiExtensionState({ homeDir: home }), 'missing');
      assert.equal(await removeSenpiExtension({ homeDir: home }), 'absent');
    } finally { await rm(home, { recursive: true, force: true }); }
  });
});

test('project extension uses .senpi/extensions and has a distinct path from user scope', async () => {
  await withSenpiEnvironment(async () => {
    const root = await temp();
    const home = await temp();
    try {
      assert.equal(await installSenpiExtension({ scope: 'project', projectRoot: root, homeDir: home }), 'installed');
      const projectFile = senpiExtensionPath('project', root);
      const userFile = senpiExtensionPath('user', path.join(home, '.senpi', 'agent'));
      assert.notEqual(projectFile, userFile);
      assert.equal((await readFile(projectFile, 'utf8')).includes(senpiManagedMarker), true);
      if (process.platform !== 'win32') assert.equal((await stat(path.dirname(projectFile))).mode & 0o777, 0o755);
      assert.equal(await getSenpiExtensionState({ scope: 'project', projectRoot: root, homeDir: home }), 'installed');
      assert.equal(await removeSenpiExtension({ scope: 'project', projectRoot: root, homeDir: home }), 'removed');
    } finally {
      await rm(root, { recursive: true, force: true });
      await rm(home, { recursive: true, force: true });
    }
  });
});

test('Senpi directory overrides honor SENPI then legacy PI fallback, and expand home prefixes', async () => {
  await withSenpiEnvironment(async () => {
    const home = await temp();
    const piAgent = path.join(home, 'selected-pi', 'agent');
    try {
      process.env.SENPI_CODING_AGENT_DIR = '~/selected-senpi/agent';
      process.env.PI_CODING_AGENT_DIR = piAgent;
      assert.equal(await installSenpiExtension({ homeDir: home }), 'installed');
      assert.equal(await readFile(senpiExtensionPath('user', path.join(home, 'selected-senpi', 'agent')), 'utf8').then(() => true), true);
      await assert.rejects(readFile(senpiExtensionPath('user', piAgent)), /ENOENT/);
      await removeSenpiExtension({ homeDir: home });

      delete process.env.SENPI_CODING_AGENT_DIR;
      assert.equal(await installSenpiExtension({ homeDir: home }), 'installed');
      assert.equal(await readFile(senpiExtensionPath('user', piAgent), 'utf8').then(() => true), true);
      await removeSenpiExtension({ homeDir: home });
    } finally { await rm(home, { recursive: true, force: true }); }
  });
});

test('an explicitly empty SENPI override selects the default directory instead of legacy overrides', async () => {
  await withSenpiEnvironment(async () => {
    const home = await temp();
    const legacy = path.join(home, 'legacy-agent');
    try {
      process.env.SENPI_CODING_AGENT_DIR = '';
      process.env.PI_CODING_AGENT_DIR = legacy;
      assert.equal(await installSenpiExtension({ homeDir: home }), 'installed');
      assert.equal(await readFile(senpiExtensionPath('user', path.join(home, '.senpi', 'agent')), 'utf8').then(() => true), true);
      await assert.rejects(readFile(senpiExtensionPath('user', legacy)), /ENOENT/);
    } finally { await rm(home, { recursive: true, force: true }); }
  });
});

test('relative Senpi environment directories resolve from the invoking working directory', async () => {
  await withSenpiEnvironment(async () => {
    const home = await temp();
    const agent = path.join(home, 'relative-agent');
    try {
      process.env.SENPI_CODING_AGENT_DIR = path.relative(process.cwd(), agent);
      assert.equal(await installSenpiExtension({ homeDir: home }), 'installed');
      assert.equal(await readFile(senpiExtensionPath('user', agent), 'utf8').then(() => true), true);
      await removeSenpiExtension({ homeDir: home });
    } finally { await rm(home, { recursive: true, force: true }); }
  });
});

test('refuses relative roots, unowned filename collisions, and oversized extensions without overwriting', async () => {
  await withSenpiEnvironment(async () => {
    const home = await temp();
    const root = await temp();
    try {
      await assert.rejects(installSenpiExtension({ homeDir: 'relative-home' }), /absolute/);
      await assert.rejects(installSenpiExtension({ homeDir: home, senpiAgentDir: 'relative-agent' }), /absolute/);
      await assert.rejects(installSenpiExtension({ scope: 'project', projectRoot: 'relative-project' }), /absolute project root/);

      const file = senpiExtensionPath('user', path.join(home, '.senpi', 'agent'));
      await mkdir(path.dirname(file), { recursive: true });
      const unowned = 'export default function userExtension() {}\n';
      await writeFile(file, unowned, 'utf8');
      await assert.rejects(installSenpiExtension({ homeDir: home }), /unowned or was modified/);
      assert.equal(await readFile(file, 'utf8'), unowned);
      await rm(file);

      await writeFile(file, 'x'.repeat((512 << 10) + 1), 'utf8');
      await assert.rejects(installSenpiExtension({ homeDir: home }), /size limit/);
    } finally {
      await rm(home, { recursive: true, force: true });
      await rm(root, { recursive: true, force: true });
    }
  });
});
