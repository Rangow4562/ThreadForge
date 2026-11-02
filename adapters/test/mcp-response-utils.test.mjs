import assert from 'node:assert/strict';
import test from 'node:test';
import { findJsonObjectWithSchema } from '../scripts/mcp-response-utils.mjs';

test('finds the requested versioned object inside a wrapped MCP provider response', () => {
  const expected = {
    schema_version: 'threadforge.task-resume-context-with-receipt.v1',
    context: { task: { id: 'synthetic-task', revision: 3 } },
    retrieval_receipt: { id: 'synthetic-receipt', task_revision: 3 },
  };
  const response = [{ response: { output: `MCP tool response:\n${JSON.stringify(expected)}\nCall completed.` } }];

  assert.deepEqual(
    findJsonObjectWithSchema(response, 'threadforge.task-resume-context-with-receipt.v1'),
    expected,
  );
});

test('ignores malformed or oversized JSON-shaped provider text', () => {
  const malformed = 'tool output {"schema_version":"threadforge.test.v1"';
  const oversized = `${'x'.repeat(1 << 20)}{"schema_version":"threadforge.test.v1"}`;

  assert.equal(findJsonObjectWithSchema(malformed, 'threadforge.test.v1'), undefined);
  assert.equal(findJsonObjectWithSchema(oversized, 'threadforge.test.v1'), undefined);
});

test('bounds traversal of cyclic provider objects', () => {
  const cyclic = {};
  cyclic.self = cyclic;

  assert.equal(findJsonObjectWithSchema(cyclic, 'threadforge.test.v1'), undefined);
});
