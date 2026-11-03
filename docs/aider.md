# Aider approved procedure access

Threadforge provides two opt-in routes for loading one approved procedure into Aider: an explicit per-run `--read` command, or a separately managed project `.aider.conf.yml` `read` entry. Aider receives the file as read-only chat context; this does not make the workspace or Aider's tools read-only.

Export a procedure only after its Threadforge review and privacy approval:

```powershell
threadforge procedure export-skill --project-root . --id <approved-procedure-id> --privacy-reviewed
```

Pass the export path and exact SHA-256 reported by the export command to the adapter:

```powershell
npm.cmd --prefix adapters run adapter -- aider skills install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The installer writes the immutable export under `.agents/skills/<generated-name>/SKILL.md`, using the shared ownership marker and lock. It rejects digest mismatches, unowned conflicts, symlinked paths, and edited installed content; neighboring skills are preserved. It does not read or modify `.aider.conf.yml`, user-global settings, credentials, project trust, or permissions.

The skill installer does not edit `.aider.conf.yml`. Its result includes the exact explicit load command. From the project root, start Aider with that command, for example:

```powershell
aider --read ".agents/skills/<generated-name>/SKILL.md"
```

The file is read-only context to Aider, but its content remains untrusted guidance and never grants Threadforge or Aider execution authorization.

## Optional project auto-read

To persistently add the exact installed export to this project's Aider read-only context list, opt in with a separate command:

```powershell
npm.cmd --prefix adapters run adapter -- aider skills auto-read install --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

The command requires the matching Threadforge-owned skill to already be installed. It manages only the top-level `read` list in `.aider.conf.yml`; unrelated YAML values, comments, line-ending style, and other read paths are preserved. Invalid/duplicate YAML keys, unsupported `read` shapes, symlinks, edited skill bytes, and config races fail without overwriting the file. If the exact path is already present but is not Threadforge-owned, Threadforge reports it and leaves it unclaimed.

Remove only Threadforge's owned entry with the same verified export:

```powershell
npm.cmd --prefix adapters run adapter -- aider skills auto-read uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

Uninstall leaves user-owned read entries and settings intact; if the user changes or removes the managed entry, Threadforge will not overwrite or remove their replacement. Uninstalling the Threadforge skill also removes its managed auto-read entry first. The ownership record is content-free apart from the relative skill path and export SHA-256, stored under `.threadforge/aider-auto-read/`; no absolute project path or credential is recorded. Auto-read is explicit project context loading, not execution authorization, project trust, tool restriction, or permission enforcement.

Remove the staged copy with the same export and digest:

```powershell
npm.cmd --prefix adapters run adapter -- aider skills uninstall --project-root <absolute-project-path> --skill-file <exported-SKILL.md> --sha256 <export-digest>
```

Local CLI tests cover the explicit install/uninstall path and the opt-in project-config lifecycle, including idempotence, user-owned entries, YAML preservation, edited paths, and authority boundaries. This is fixture-level transport/configuration evidence only. Aider version discovery, live loading, model use, task outcome, and cross-harness retrieval remain unverified.
