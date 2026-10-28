import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { pathToFileURL } from 'node:url';
import { buildClinePlanGatePackage } from '../scripts/build-cline-plan-gate-package.mjs';

test('builds a configured Cline task-plan gate package with bounded, local-only inputs and notices', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-plan-gate-'));
  const output = path.join(root, 'bundle');
  try {
    const result = await buildClinePlanGatePackage(output, {
      taskId: 'task-fixture', projectId: 'project-fixture', projectRoot: root,
      threadforgeCommand: 'threadforge-fixture', timeoutMs: 4500,
    });
    assert.equal(result.schema_version, 'threadforge.cline-task-plan-gate-package-build.v1');
    assert.equal(result.package_name, 'threadforge-cline-task-plan-gate');
    assert.equal(result.file_count, 7);
    assert.equal(result.bundle_sha256.length, 64);
    assert.equal(result.config_sha256.length, 64);
    assert.deepEqual((await readdir(output)).sort(), [
      'LICENSE', 'NOTICE', 'README.md', 'THIRD_PARTY.md', 'cline-plan-gate-plugin.js', 'index.js', 'package.json',
    ]);

    const manifest = JSON.parse(await readFile(path.join(output, 'package.json'), 'utf8'));
    assert.equal(manifest.private, true);
    assert.equal(manifest.type, 'module');
    assert.equal(manifest.main, './index.js');
    assert.equal(manifest.license, 'Apache-2.0');
    assert.equal(manifest.dependencies, undefined);
    assert.equal(manifest.threadforge.schema_version, 'threadforge.cline-task-plan-gate-package.v1');
    assert.equal(manifest.threadforge.bundle_sha256, result.bundle_sha256);

    const index = await readFile(path.join(output, 'index.js'), 'utf8');
    const implementation = await readFile(path.join(output, 'cline-plan-gate-plugin.js'), 'utf8');
    assert.match(index, /createThreadforgeClinePlanGatePlugin/);
    assert.match(index, /task-fixture/);
    assert.match(index, /project-fixture/);
    assert.doesNotMatch(`${index}\n${implementation}`, /\.ts["']/);
    assert.doesNotMatch(`${index}\n${implementation}`, /D:\\\\Personal\\\\Memory|adapters[\\/]src/);
    assert.match(await readFile(path.join(output, 'README.md'), 'utf8'), /not a security boundary/);

    const loaded = await import(pathToFileURL(path.join(output, 'index.js')).href);
    assert.equal(loaded.default.name, 'threadforge-task-plan-gate');
    assert.deepEqual(loaded.default.manifest.capabilities, ['hooks']);

    const pluginModule = await import(pathToFileURL(path.join(output, 'cline-plan-gate-plugin.js')).href);
    const plugin = pluginModule.createThreadforgeClinePlanGatePlugin({
      taskId: 'task-test', projectId: 'project-test', projectRoot: root,
    }, async () => ({ decision: 'deny' }));
    const denial = await plugin.hooks.beforeTool({ toolCall: { toolName: 'editor' }, input: { path: 'outside.txt' } });
    assert.equal(denial?.skip, true);
    assert.equal(JSON.stringify(denial).includes('outside.txt'), false);

    await assert.rejects(buildClinePlanGatePackage(output, {
      taskId: 'task-fixture', projectId: 'project-fixture', projectRoot: root,
    }), /already exists/);
    await assert.rejects(buildClinePlanGatePackage(path.join(root, 'bad'), {
      taskId: 'task-fixture', projectId: 'project-fixture', projectRoot: `${root}${path.sep}..`,
    }), /traversal components/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
