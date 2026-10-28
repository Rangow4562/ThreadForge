# DeepSeek Harness project skills

Threadforge installs approved portable procedures into DeepSeek Harness' project-local skill directory:

```text
.dsh/skills/<generated-skill-name>/SKILL.md
```

The dedicated `.dsh/skills/` target follows the preserved Mancode platform contract and keeps DeepSeek Harness files separate from the shared `.agents/skills/` directory. Ownership markers and installer locks are host-specific.

Export and review a procedure first, then install the exported artifact using its printed digest. Both `dsh` and `deepseek-harness` are accepted command families:

```powershell
threadforge procedure export-skill --project-root <absolute-project-path> --id <procedure-id> --privacy-reviewed
npm.cmd --prefix adapters run adapter -- dsh skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
npm.cmd --prefix adapters run adapter -- deepseek-harness skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

Only a generated export beneath `.threadforge/skills/<id>/rev-NNNNNN/SKILL.md` is accepted. The installer validates the expected SHA-256, generated identity, and advisory/privacy notices; it writes exclusively, refuses collisions or edited content, and preserves neighboring files. It does not edit Cordis configuration, use the shared `.agents/skills/` target, change sandbox/trust settings, configure MCP or telemetry, or authorize execution.

The file-discovery path is documented, but GUI discovery, refresh behavior, double-window session propagation, subagent inheritance, and actual procedure use remain unverified. Treat the installed procedure as guidance only. For explicit MCP access, Threadforge can print a project-bound Cordis overlay; DeepSeek Harness must be launched with `--patch` each time because it does not auto-discover project MCP configuration. See [DeepSeek Harness MCP access](deepseek-harness-mcp.md).
