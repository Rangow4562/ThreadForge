# Project instruction bootstrap

Threadforge can append one fixed, advisory-only project-continuity block to the instruction file selected for 17 explicitly named harness targets. Goose has its own `.goosehints` project file, Cline has a project rule file, and Command Code and Codex in ChatGPT desktop, Codex CLI, and the Codex IDE extension each have distinct host IDs that share one managed `AGENTS.md` target. This is a reversible text-file operation; it does not install host-native skills, slash commands, MCP, hooks, telemetry, host trust, permissions, procedures, or execution authorization.

Start with the non-mutating plan:

```powershell
npm.cmd --prefix adapters run adapter -- bootstrap instructions plan `
  --project-root <absolute-project-root> `
  --hosts claude-code,command-code,cursor,antigravity,gemini-cli,codex-cli,chatgpt-desktop-codex,codex-ide,opencode,qwen-code,github-copilot,zcode,kimi-code,qoder,deepseek-harness,goose,cline
```

After reviewing target paths, install, update, or remove the selected guidance:

```powershell
npm.cmd --prefix adapters run adapter -- bootstrap instructions install `
  --project-root <absolute-project-root> `
  --hosts claude-code,command-code,cursor,antigravity,gemini-cli,codex-cli,chatgpt-desktop-codex,codex-ide,opencode,qwen-code,github-copilot,zcode,kimi-code,qoder,deepseek-harness,goose,cline

npm.cmd --prefix adapters run adapter -- bootstrap instructions update `
  --project-root <absolute-project-root> `
  --hosts claude-code,command-code,cursor,antigravity,gemini-cli,codex-cli,chatgpt-desktop-codex,codex-ide,opencode,qwen-code,github-copilot,zcode,kimi-code,qoder,deepseek-harness,goose,cline

npm.cmd --prefix adapters run adapter -- bootstrap instructions uninstall `
  --project-root <absolute-project-root> `
  --hosts claude-code,command-code,cursor,antigravity,gemini-cli,codex-cli,chatgpt-desktop-codex,codex-ide,opencode,qwen-code,github-copilot,zcode,kimi-code,qoder,deepseek-harness,goose,cline
```

| Host ID | Target file |
| --- | --- |
| `claude-code` | `CLAUDE.md` |
| `command-code`, `cursor`, `antigravity`, `codex-cli`, `chatgpt-desktop-codex`, `codex-ide`, `opencode`, `zcode`, `kimi-code`, `qoder`, `deepseek-harness` | `AGENTS.md` (one shared target) |
| `gemini-cli` | `GEMINI.md` |
| `qwen-code` | `QWEN.md` |
| `github-copilot` | `.github/copilot-instructions.md` |
| `goose` | `.goosehints` |
| `cline` | `.cline/rules/threadforge-memory.md` |

The Copilot path follows Mancode's preserved project-adapter contract; Copilot instruction loading is not verified here. Goose documents `.goosehints` as a project-context file; Threadforge's file-preservation behavior is tested, but live Goose discovery and prompt use are not. Installing to a fresh project may create `.github`; uninstall removes only the managed instruction file and leaves that directory in place. Qwen Code's project target uses its documented `QWEN.md` instruction file. This installer writes only the same advisory block; it does not change `.qwen/settings.json`, MCP, hooks, trust settings, native skills, or commands.

Cline's target is a separate `.cline/rules/threadforge-memory.md` file; the installer preserves existing rule text and manages only its marked block. It is guidance only: it does not configure Cline's user-global MCP server, bind a project ID, change trust/approval, or prove that the installed host loads or follows the rule. The guidance requires an explicit trusted `project_id` and freshness-aware v2 exact reads when those tools are available; otherwise it directs the agent to ask or use an approved portable project artifact.

The plan groups hosts that share a file, hashes the canonical project root, and does not read or write instruction files. Apply operations are locked locally and processed per target, not as one multi-file transaction. A failure is returned as a partial result so successful changes remain visible for review.

For an existing file, Threadforge appends a delimited block and preserves surrounding bytes. For a missing file, it creates the file. The block has an embedded SHA-256 ownership marker: install is idempotent, update requires a valid existing Threadforge block, and uninstall removes only that unchanged block. Edited/malformed blocks, symlink targets, non-regular files, invalid UTF-8, files over 1 MiB, and stale locks fail closed. Uninstall removes a file that Threadforge created only when it contains no other content. The marker detects accidental edits; it is not a signature or proof of approval.

The fixed content tells agents to consult relevant read-only Threadforge project/task context only when relevant and when project privacy policy permits the configured host/model to receive it; otherwise do not retrieve/inject and ask or follow policy. It treats the memory index as discovery only, prefers freshness-aware v2 exact reads, and prohibits guessing a project ID. It also requires checking scope, review, evidence, and freshness; surfacing stale, incomplete, conflicting, or privacy-blocked results; and never treating memory as execution authorization. It contains no project-specific facts or procedure instructions. Review the resulting file before committing it.

This confirms only that the selected Markdown file was managed safely. Host-specific file discovery, precedence, and actual prompt-time use are not verified for these targets; the installer does not configure host trust or MCP. Goose can separately materialize a digest-verified approved procedure export into `.goosehints`; see [Goose approved procedures](goose-memory.md). Mancode's documented mode prompts/skills/commands and session propagation are separate capabilities and remain outside this instruction-file manager. This feature is not host-support or cutover acceptance evidence.
