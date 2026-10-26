# Aider approved procedure access

Threadforge provides a configuration-free, opt-in route for loading one approved procedure into an Aider session. Aider's [`--read` option](https://aider.chat/docs/usage/conventions.html) adds a file as read-only chat context; this does not make the workspace or Aider's tools read-only.

Export a procedure only after its Threadforge review and privacy approval:

```powershell
threadforge procedure export-skill --project-root . --id <approved-procedure-id> --privacy-reviewed
```

Pass the export path and exact SHA-256 reported by the export command to the adapter:

```powershell
npm.cmd --prefix adapters run adapter -- aider skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installer writes the immutable export under `.agents/skills/<generated-name>/SKILL.md`, using the shared ownership marker and lock. It rejects digest mismatches, unowned conflicts, symlinked paths, and edited installed content; neighboring skills are preserved. It does not read or modify `.aider.conf.yml`, user-global settings, credentials, project trust, or permissions.

The install result includes the exact explicit load command. From the project root, start Aider with that command, for example:

```powershell
aider --read ".agents/skills/<generated-name>/SKILL.md"
```

Threadforge does not register this path in Aider configuration, so the user must opt in with `--read` for each invocation (or configure Aider independently). The file is read-only context to Aider, but its content remains untrusted guidance and never grants Threadforge or Aider execution authorization.

Remove the staged copy with the same export and digest:

```powershell
npm.cmd --prefix adapters run adapter -- aider skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The local CLI lifecycle test checks the installed path, generated load command, exact-byte removal, and preservation of an existing `.aider.conf.yml`. This is transport/configuration-preservation evidence only. Aider version discovery, live loading, model use, task outcome, and cross-harness retrieval remain unverified.
