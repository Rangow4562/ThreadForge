import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import { parseDocument } from 'yaml';

type Update = {
  'package-ecosystem': string;
  directory: string;
  schedule: { interval: string };
  'open-pull-requests-limit': number;
  groups?: Record<string, { patterns: string[]; 'update-types'?: string[] }>;
};

type DependabotConfig = {
  version: number;
  updates: Update[];
};

const repositoryRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const configPath = path.join(repositoryRoot, '.github', 'dependabot.yml');

test('Dependabot update policy covers the checked-in package directories without auto-merge', () => {
  const document = parseDocument(readFileSync(configPath, 'utf8'), { uniqueKeys: true });
  assert.deepEqual(document.errors, []);
  const config = document.toJS() as DependabotConfig;

  assert.equal(config.version, 2);
  const expected = new Set([
    'github-actions|/',
    'gomod|/',
    'npm|/adapters',
    'npm|/adapters/browser-extension',
    'npm|/adapters/openclaw-plugin',
    'docker|/containers/engineering',
    'uv|/python',
  ]);
  assert.equal(config.updates.length, expected.size);

  for (const update of config.updates) {
    const key = update['package-ecosystem'] + '|' + update.directory;
    assert.ok(expected.delete(key), 'unexpected or duplicate Dependabot target: ' + key);
    assert.equal(update.schedule.interval, 'weekly');
    assert.equal(update['open-pull-requests-limit'], 5);
    assert.equal(existsSync(path.join(repositoryRoot, update.directory)), true);
    for (const group of Object.values(update.groups ?? {})) {
      assert.deepEqual(group.patterns, ['*']);
      if (group['update-types']) {
        assert.deepEqual(group['update-types'], ['minor', 'patch']);
      }
    }
  }
  assert.deepEqual([...expected], []);
});
