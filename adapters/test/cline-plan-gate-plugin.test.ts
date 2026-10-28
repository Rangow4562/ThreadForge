import assert from 'node:assert/strict';
import test from 'node:test';
import path from 'node:path';
import {
  createThreadforgeClinePlanGatePlugin,
  type ClineTaskPlanGuardInput,
} from '../src/cline-plan-gate-plugin.ts';

const options = {
  taskId: 'task-1',
  projectId: 'project-1',
  projectRoot: path.resolve('fixture-project'),
};

test('Cline plan gate sends only direct editor target metadata and returns no allow decision', async () => {
  let observedOptions: unknown;
  let observedInput: ClineTaskPlanGuardInput | undefined;
  const plugin = createThreadforgeClinePlanGatePlugin(options, async (configured, input) => {
    observedOptions = configured;
    observedInput = input;
    return { decision: 'allow' };
  });

  assert.equal(plugin.name, 'threadforge-task-plan-gate');
  assert.deepEqual(plugin.manifest.capabilities, ['hooks']);
  plugin.setup(undefined, { workspaceInfo: { rootPath: path.resolve('fixture-project') } });
  assert.equal(await plugin.hooks.beforeTool({
    toolCall: { toolName: 'editor' },
    input: { path: 'src/main.ts', new_text: 'private file contents' },
  }), undefined);
  assert.deepEqual(observedOptions, options);
  assert.deepEqual(observedInput, {
    hook_event_name: 'BeforeTool', tool_name: 'editor', cwd: path.resolve('fixture-project'),
    tool_input: { path: 'src/main.ts' },
  });
  assert.equal(JSON.stringify(observedInput).includes('private file contents'), false);
});

test('Cline plan gate checks canonical apply_patch input and skips denied operations without leaking paths', async () => {
  let observedInput: ClineTaskPlanGuardInput | undefined;
  const plugin = createThreadforgeClinePlanGatePlugin(options, async (_configured, input) => {
    observedInput = input;
    return { decision: 'deny' };
  });
  plugin.setup(undefined, { workspaceInfo: { rootPath: path.resolve('fixture-project') } });

  const result = await plugin.hooks.beforeTool({
    toolCall: { toolName: 'apply_patch' },
    input: { input: '*** Begin Patch\n*** Update File: docs/private.md\n*** End Patch', extra: 'not forwarded' },
  });
  assert.deepEqual(observedInput?.tool_input, { input: '*** Begin Patch\n*** Update File: docs/private.md\n*** End Patch' });
  assert.deepEqual(result, {
    skip: true,
    reason: 'Threadforge withheld this Cline file operation because the task plan or implementation scope did not authorize it.',
  });
  assert.equal(JSON.stringify(result).includes('docs/private.md'), false);
});

test('Cline plan gate accepts the SDK apply_patch string input variant without transforming it', async () => {
  let observedInput: ClineTaskPlanGuardInput | undefined;
  const plugin = createThreadforgeClinePlanGatePlugin(options, async (_configured, input) => {
    observedInput = input;
    return { decision: 'allow' };
  });
  plugin.setup(undefined, { workspaceInfo: { rootPath: path.resolve('fixture-project') } });
  const patch = '*** Begin Patch\n*** Update File: src/main.ts\n*** End Patch';
  assert.equal(await plugin.hooks.beforeTool({ toolCall: { toolName: 'apply_patch' }, input: patch }), undefined);
  assert.deepEqual(observedInput?.tool_input, { input: patch });
});

test('Cline plan gate fails closed for missing workspace, malformed decisions, and local guard errors', async () => {
  let missingWorkspaceChecks = 0;
  const missingWorkspace = createThreadforgeClinePlanGatePlugin(options, async (_configured, input) => {
    missingWorkspaceChecks += 1;
    assert.equal(input.cwd, '');
    return { decision: 'allow' };
  });
  missingWorkspace.setup(undefined, {});
  assert.equal((await missingWorkspace.hooks.beforeTool({ toolCall: { toolName: 'editor' }, input: { path: 'src/main.ts' } }))?.skip, true);
  assert.equal(missingWorkspaceChecks, 0);

  const invalidDecision = createThreadforgeClinePlanGatePlugin(options, async () => ({ decision: 'maybe' }));
  assert.equal((await invalidDecision.hooks.beforeTool({ toolCall: { toolName: 'editor' }, input: { path: 'src/main.ts' } }))?.skip, true);

  const unavailable = createThreadforgeClinePlanGatePlugin(options, async () => { throw new Error('private process output'); });
  const denied = await unavailable.hooks.beforeTool({ toolCall: { toolName: 'editor' }, input: { path: 'src/main.ts' } });
  assert.equal(denied?.skip, true);
  assert.equal(JSON.stringify(denied).includes('private process output'), false);
});

test('Cline plan gate leaves unrelated tools untouched and bounds its callback input', async () => {
  let checks = 0;
  const plugin = createThreadforgeClinePlanGatePlugin(options, async () => {
    checks += 1;
    return { decision: 'allow' };
  });
  assert.equal(await plugin.hooks.beforeTool({ toolCall: { toolName: 'run_commands' }, input: { command: 'echo not gated' } }), undefined);
  assert.equal(checks, 0);

  const oversized = createThreadforgeClinePlanGatePlugin(options, async () => {
    checks += 1;
    return { decision: 'allow' };
  });
  const blocked = await oversized.hooks.beforeTool({
    toolCall: { toolName: 'apply_patch' }, input: { input: 'x'.repeat((1 << 20) + 1) },
  });
  assert.equal(blocked?.skip, true);
  assert.equal(checks, 0);
  assert.throws(() => createThreadforgeClinePlanGatePlugin({ ...options, timeoutMs: 10_001 }), /between 100 and 10000/);
});
