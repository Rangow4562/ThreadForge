import assert from 'node:assert/strict';
import test from 'node:test';
import { validateAdapterManifest } from '../src/protocol.ts';

test('accepts a non-injecting MCP adapter', () => {
  const manifest = validateAdapterManifest({
    schema_version: 'threadforge.adapter.v1',
    harness: 'example',
    adapter_version: '0.1.0',
    capabilities: ['capture', 'mcp'],
    injection_contract_verified: false,
  });
  assert.equal(manifest.harness, 'example');
});

test('refuses unverified context injection', () => {
  assert.throws(() => validateAdapterManifest({
    schema_version: 'threadforge.adapter.v1',
    harness: 'example',
    adapter_version: '0.1.0',
    capabilities: ['context-injection'],
    injection_contract_verified: false,
  }), /verified host contract/);
});
