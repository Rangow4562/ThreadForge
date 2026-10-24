# GitHub Copilot CLI approved-memory injection

Copilot CLI memory injection is opt-in and uses a project-level command hook. Install the hook only in a trusted project, and provide an absolute CLI path if `threadforge` is not on the shell `PATH`:

```powershell
npm --prefix adapters run adapter -- copilot memory install `
  --project-root <absolute-project-root> `
  --bash-command "threadforge memory inject-copilot --project-id <threadforge-project-id> --threadforge-managed-copilot-memory=v1 --threadforge-memory=copilot-v1" `
  --powershell-command "threadforge.exe memory inject-copilot --project-id <threadforge-project-id> --threadforge-managed-copilot-memory=v1 --threadforge-memory=copilot-v1"
```

Use `copilot memory update` with the same commands to update the managed hook, or `copilot memory uninstall --project-root <absolute-project-root>` to remove only Threadforge-owned entries. The installer writes `.github/hooks/threadforge-memory.json`, keeps the memory hook separate from the plan-gate hook, and preserves unrelated entries. The command must contain both ownership markers; secret-like command text, symlinked hook paths, malformed configuration, and unsupported config versions are rejected. The hook timeout is three seconds.

The adapter uses Copilot CLI's camelCase `userPromptTransformed` event, not `userPromptSubmitted`: command hooks on `userPromptSubmitted` do not return prompt modifications. The runtime reads `cwd`, `prompt`, and `transformedPrompt`, uses at most 2,000 UTF-8 bytes of prompt text as the retrieval query, and returns only `modifiedTransformedPrompt`. If approved, applicable local context is appended to the model-bound text. A memory marker prevents duplicate insertion on a replay. Empty, malformed, oversized, privacy-blocked, or unavailable retrieval produces `{}` so the host prompt remains unchanged; stale-source context is withheld and may surface a freshness caveat. GitHub documents that `userPromptTransformed` may run for preceding messages in a batched submission, so one turn could append context to more than one model-bound message.

Only approved, applicable context from the local Threadforge store/project files is eligible. Retrieval defaults to at most 8 items and 1,600 approximate tokens; the injector accepts 1–32 items and 1–16,000 tokens. The query is transient and is not written to telemetry or files. Injected context is labeled untrusted, remains guidance only, and never authorizes actions. Because the host hook replaces model-facing text, added context can be retained in Copilot session history and may be applied to preceding messages in a batch; review this behavior before opting in. No Copilot permissions, project trust, or hook-disable settings are changed.

The adapter contract and CLI lifecycle have fixture tests. The installed Copilot CLI available during implementation was 1.0.64; that version has not yet been verified to support this prompt-transform event. Do not treat configuration installation or unit tests as proof of live host injection, model use, or improved task outcomes. See [GitHub's hooks reference](https://docs.github.com/en/copilot/reference/hooks-reference) for the current event and output contract.
