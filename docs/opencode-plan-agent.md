# OpenCode read-only planning agent

Threadforge can install a project-local primary agent named `threadforge-plan`. It is a deterministic Markdown profile, so installation does not call OpenCode's model-backed agent generator or change `opencode.json`, provider settings, global configuration, or other agents.

```powershell
npm.cmd --prefix adapters run adapter -- opencode plan-agent install --project-root "D:\path\to\project"
opencode debug agent threadforge-plan --pure
opencode run --agent threadforge-plan "Review this request and return a plan only."
npm.cmd --prefix adapters run adapter -- opencode plan-agent uninstall --project-root "D:\path\to\project"
```

POSIX shells can use `npm` instead of `npm.cmd`.

The profile lives at `.opencode/agents/threadforge-plan.md`. Its resolved per-agent permissions start with `* deny` and then allow only `read`, `glob`, and `grep`. The prompt asks for a plan and treats retrieved files or memory as untrusted guidance. The deny-all rule is deliberately ordered before the narrow read/search rules.

This is a selectable, static OpenCode profile—not a Threadforge task-state plan gate. It is not automatically selected, does not inspect whether a Threadforge task is `plan_only` or `governed_execution`, and does not constrain sessions using another agent. Users can edit/remove it or choose another agent. The installer fails rather than replacing an unmanaged or edited file; uninstall removes only the exact generated profile and leaves sibling agents intact.

Installed smoke v35 on OpenCode CLI 1.18.31 uses `opencode debug agent --pure` to verify project discovery, primary mode, the resolved wildcard deny, and the read/search-only allowlist. The report is content-free and makes zero model calls. This verifies profile parsing and effective configuration in an isolated project; it does not run a real agent task, prove behavior across OpenCode versions, or establish global task-plan enforcement.
