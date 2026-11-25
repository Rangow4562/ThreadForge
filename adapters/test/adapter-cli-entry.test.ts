import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';
import { pathToFileURL } from 'node:url';
import { resolveAdapterCLIEntry, resolveAdapterSiblingModule } from '../src/adapter-cli-entry.ts';

test('source adapter modules resolve the TypeScript CLI with strip-types enabled', () => {
  const moduleUrl = pathToFileURL(path.resolve('src', 'devin-hooks-config.ts')).href;
  const entry = resolveAdapterCLIEntry(moduleUrl);
  assert.equal(entry.scriptPath, path.resolve('src', 'cli.ts'));
  assert.deepEqual(entry.nodeArguments, ['--experimental-strip-types']);
  assert.equal(resolveAdapterSiblingModule(moduleUrl, 'cline-plugin'), path.resolve('src', 'cline-plugin.ts'));
});

test('compiled package modules resolve the shipped JavaScript bin and sibling module', () => {
  const moduleUrl = pathToFileURL(path.resolve('dist', 'devin-hooks-config.js')).href;
  const entry = resolveAdapterCLIEntry(moduleUrl);
  assert.equal(entry.scriptPath, path.resolve('dist', 'bin.js'));
  assert.deepEqual(entry.nodeArguments, []);
  assert.equal(resolveAdapterSiblingModule(moduleUrl, 'cline-plugin'), path.resolve('dist', 'cline-plugin.js'));
});

test('rejects non-file module URLs and unsafe sibling names', () => {
  assert.throws(() => resolveAdapterCLIEntry('https://example.invalid/module.js'), /local file URL/);
  assert.throws(() => resolveAdapterSiblingModule('file:///tmp/module.ts', '../escape'), /module name is invalid/);
});
