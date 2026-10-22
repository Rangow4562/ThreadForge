import assert from 'node:assert/strict';
import path from 'node:path';
import test from 'node:test';

import {
  antigravityExecutionModeArgs,
  classifyAntigravityPlanGateProbe,
  scopedAntigravityWritePermissionRule,
} from '../scripts/antigravity-mcp-smoke.mjs';

const validFixture = {
  toolCallCount: 1,
  hookCallbackInvocations: 1,
  guardDecision: 'deny',
  hostToolErrorObserved: true,
  fixtureIntegrityVerified: true,
};

test('classifies an observed Antigravity deny only when the host returns a tool error', () => {
  assert.deepEqual(classifyAntigravityPlanGateProbe(validFixture), { status: 'verified', reason: undefined });
  assert.deepEqual(classifyAntigravityPlanGateProbe({
    ...validFixture,
    hostToolErrorObserved: false,
  }), { status: 'not_verified', reason: 'host_tool_error_after_guard_denial_not_observed' });
});

test('records an absent hook callback after an actual host tool call without claiming denial', () => {
  for (const hostToolErrorObserved of [false, true]) {
    assert.deepEqual(classifyAntigravityPlanGateProbe({
      ...validFixture,
      hookCallbackInvocations: 0,
      guardDecision: 'unknown',
      hostToolErrorObserved,
    }), { status: 'not_observed', reason: 'workspace_pretool_hook_not_invoked' });
  }
});

test('distinguishes a provider turn that never exercised the host tool', () => {
  assert.deepEqual(classifyAntigravityPlanGateProbe({
    ...validFixture,
    toolCallCount: 0,
    hookCallbackInvocations: 0,
    guardDecision: 'unknown',
    hostToolErrorObserved: false,
  }), { status: 'probe_not_exercised', reason: 'host_tool_call_not_observed' });
});

test('fails closed for an unexpected allow, invalid fixture, or repeated invocation', () => {
  for (const invalid of [
    { ...validFixture, guardDecision: 'allow' },
    { ...validFixture, fixtureIntegrityVerified: false },
    { ...validFixture, hookCallbackInvocations: 2 },
  ]) {
    assert.equal(classifyAntigravityPlanGateProbe(invalid).status, 'not_verified');
  }
});

test('scopes the plan-gate fixture permission to one project-relative write target', () => {
  const root = path.resolve('disposable', 'project');
  assert.equal(
    scopedAntigravityWritePermissionRule(root, path.join(root, 'src', 'denied.txt')),
    'write_file (src/denied.txt)',
  );
  assert.throws(
    () => scopedAntigravityWritePermissionRule(root, path.resolve(root, '..', 'outside', 'denied.txt')),
    /inside the disposable project/,
  );
});

test('Antigravity execution mode flags are explicit and constrained', () => {
  assert.deepEqual(antigravityExecutionModeArgs(undefined), []);
  assert.deepEqual(antigravityExecutionModeArgs('accept-edits'), ['--mode', 'accept-edits']);
  assert.throws(() => antigravityExecutionModeArgs('--dangerously-skip-permissions'), /execution mode is invalid/);
});
