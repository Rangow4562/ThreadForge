# GitHub Copilot CLI project memory

Threadforge does not install automatic prompt-memory injection for Copilot CLI. The CLI's documented command-hook events do not provide a supported way for a command hook to modify the prompt; `userPromptTransformed` belongs to the Copilot SDK callback contract. Accordingly, `copilot memory install|update` now fails before reading or changing project configuration. `copilot memory uninstall --project-root <absolute-project-root>` remains available to remove only Threadforge-marked entries written by the earlier unsupported installer, preserving unrelated hooks.

For a portable advisory fallback, optionally install the managed project instruction block:

```powershell
npm.cmd --prefix adapters run adapter -- bootstrap instructions install `
  --project-root <absolute-project-root> `
  --hosts github-copilot
```

This appends the fixed Threadforge continuity guidance to `.github/copilot-instructions.md` while preserving existing text. It tells the host to use only privacy-permitted, freshness-aware project MCP reads, never guess `project_id`, and treat retrieved content as non-authorizing guidance. It does not configure MCP, trust, permissions, or hooks, and it cannot guarantee that a model follows the instructions.

Use the project MCP server for explicit, read-only memory retrieval instead. Install it through the project `.mcp.json` manager:

```powershell
npm.cmd --prefix adapters run adapter -- mcp install `
  --host github-copilot `
  --project-root <absolute-project-root> `
  --command <absolute-threadforge-executable> `
  --arg mcp --arg serve --arg --project-root --arg <absolute-project-root>
```

After installing, Copilot can explicitly call `threadforge_project_memory_read_v2` with `project_id`, `kind`, and `id`. Results are freshness-checked when the MCP server has the project root, and remain untrusted guidance with `action_ready=false`, `guidance_only=true`, and `execution_authorized=false`. This is an explicit tool read, not automatic prompt injection. See [project MCP setup](mcp.md#github-copilot-cli-project-mcp).

## Copilot SDK prompt memory

The adapter package exports `addThreadforgeCopilotSDKMemory` from `@threadforge/adapter-contracts/copilot-sdk-memory`. It composes the SDK's `onUserPromptTransformed` callback with any existing callback and preserves unrelated hook fields. The adapter invokes the local `threadforge memory inject-copilot-sdk` command with bounded camelCase `cwd`, `prompt`, and `transformedPrompt` input; prompt text is sent on child-process stdin, never in command arguments. The local retrieval query uses at most 2,000 UTF-8 bytes of prompt text. Defaults are 8 items, 1,600 approximate tokens, a 2-second timeout, and no task context. Task context is opt-in through `taskId` with optional `sessionId`, or `discoverActiveTask`; `maxTaskEntries` defaults to 6 (range 1-20) when selected. Retrieval failures leave the existing transformed prompt and callback result unchanged. The helper does not install or modify Copilot CLI configuration.

```ts
import { addThreadforgeCopilotSDKMemory } from '@threadforge/adapter-contracts/copilot-sdk-memory';

const hooks = addThreadforgeCopilotSDKMemory(existingHooks, {
  projectId: 'my-project',
  // taskId: 'task-123', // opt in only when a task snapshot is wanted
});
const session = await client.createSession({ hooks });
```

This callback changes the transformed prompt sent to the model and may persist in SDK session history. Threadforge retrieves only locally approved, freshness-checked project guidance and formats it as untrusted, non-authorizing context; prompt content itself is not stored by this integration. This is adapter contract/unit-test evidence, not a live Copilot SDK version test, real-model adherence, efficacy, or CLI command-hook support. See the [Copilot SDK hook contract](https://github.com/github/copilot-sdk/blob/main/docs/hooks/user-prompt-transformed.md).

The installed Copilot CLI 1.0.64 smoke verifies exactly one explicitly selected, read-only project-memory MCP call through a deterministic loopback provider. Smoke v41 also checks that the installed CLI includes the managed project-instruction block in its provider request. The tool call and final answer are scripted by the fixture; no model follows the instruction, and `real_model_invocations=0`. The report contains no memory text or record IDs. This is selected-version instruction-loading and MCP-transport evidence only—not real-model adherence, fewer repeated failures, or cutover evidence. See [GitHub's Copilot CLI hooks guide](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-hooks), [hooks reference](https://docs.github.com/en/copilot/reference/hooks-reference), and [Copilot SDK documentation](https://docs.github.com/en/copilot/concepts/agents/about-copilot-sdk) for their distinct event contracts.
