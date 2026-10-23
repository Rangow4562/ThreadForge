import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { pathToFileURL } from 'node:url';
import { buildClinePluginPackage } from '../scripts/build-cline-plugin-package.mjs';

test('builds a source-independent Cline CLI plugin package and invokes its installed module', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'threadforge-cline-plugin-package-'));
  const output = path.join(root, 'bundle');
  const originalFetch = globalThis.fetch;
  const observed = [];
  try {
    const result = await buildClinePluginPackage(output);
    assert.equal(result.schema_version, 'threadforge.cline-plugin-package-build.v1');
    assert.equal(result.package_name, 'threadforge-observational-cline-plugin');
    assert.equal(result.file_count, 7);
    assert.equal(result.bundle_sha256.length, 64);
    assert.deepEqual((await readdir(output)).sort(), [
      'LICENSE', 'NOTICE', 'README.md', 'THIRD_PARTY.md', 'emit.js', 'index.js', 'package.json',
    ]);

    const manifest = JSON.parse(await readFile(path.join(output, 'package.json'), 'utf8'));
    assert.equal(manifest.private, true);
    assert.equal(manifest.type, 'module');
    assert.equal(manifest.main, './index.js');
    assert.equal(manifest.license, 'Apache-2.0');
    assert.deepEqual(manifest.dependencies, undefined);
    assert.equal(manifest.threadforge.schema_version, 'threadforge.cline-plugin-package.v1');
    assert.equal(manifest.threadforge.bundle_sha256, result.bundle_sha256);

    const index = await readFile(path.join(output, 'index.js'), 'utf8');
    const emit = await readFile(path.join(output, 'emit.js'), 'utf8');
    assert.match(index, /from ['"]\.\/emit\.js['"]/);
    assert.doesNotMatch(index, /\.ts["']/);
    assert.doesNotMatch(emit, /\.ts["']/);
    assert.doesNotMatch(`${index}\n${emit}`, /D:\\\\Personal\\\\Memory|adapters[\\/]src/);

    globalThis.fetch = async (_input, init) => {
      const payload = JSON.parse(String(init?.body));
      observed.push(...(Array.isArray(payload) ? payload : [payload]));
      return new Response('{}', { status: 202 });
    };
    const loaded = await import(pathToFileURL(path.join(output, 'index.js')).href);
    const plugin = loaded.default;
    assert.equal(plugin.name, 'threadforge');
    plugin.setup(undefined, { workspaceInfo: { rootPath: 'C:\\private\\project' } });
    assert.equal(await plugin.hooks.beforeRun({ taskId: 'private-task', input: { text: 'private prompt' } }), undefined);
    assert.deepEqual(observed.map((event) => event.event.action), ['session.started', 'prompt.submitted']);
    const serialized = JSON.stringify(observed);
    assert.equal(serialized.includes('private prompt'), false);
    assert.equal(serialized.includes('C:\\private\\project'), false);
    assert.equal(serialized.includes('private-task'), false);

    await assert.rejects(buildClinePluginPackage(output), /already exists/);
    await assert.rejects(buildClinePluginPackage('relative-output'), /absolute local directory/);
  } finally {
    globalThis.fetch = originalFetch;
    await rm(root, { recursive: true, force: true });
  }
});
