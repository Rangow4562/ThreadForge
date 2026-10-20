# Cursor Cloud Agents: approved procedure access

Threadforge supports a knowledge-only path for making an already approved and privacy-reviewed procedure available in a Cursor project. It does not capture Cursor Cloud Agent telemetry, upload transcripts, provision credentials, or configure remote storage.

First export the approved procedure from the local Threadforge store and inspect the generated artifact:

```sh
threadforge procedure export-skill --project-root <project> --id <approved-procedure-id> --privacy-reviewed
```

The JSON result includes the immutable `path` and `sha256`. After reviewing those exact bytes, run the adapter command from `adapters/`:

```sh
npm run adapter -- cursor skills install --project-root <project> --skill-file <exported-path> --sha256 <exported-sha256>
```

The installer places the skill at `.agents/skills/<generated-skill-name>/SKILL.md`, the portable project Agent Skills location documented by [Cursor](https://cursor.com/docs/skills). Include that reviewed file in the repository if it should be present in a Cloud Agent workspace. Installing locally does not commit, push, or upload the file. Cursor's project-skill loader and its behavior in a live Cloud Agent have not been tested by Threadforge.

To remove it, provide the same exported file and digest:

```sh
npm run adapter -- cursor skills uninstall --project-root <project> --skill-file <exported-path> --sha256 <exported-sha256>
```

The installer accepts only a Threadforge-generated export under `.threadforge/skills/<id>/rev-NNNNNN/SKILL.md`, validates its graph/revision identity and advisory/privacy notices, and requires the SHA-256 printed by the exporter. It copies exact bytes, records ownership in the shared `.agents/skills` family, rejects symlink paths and conflicts, and refuses to overwrite or remove user-edited content. Other Threadforge adapters targeting this directory can reuse the same export and owner record. Each procedure revision has a distinct skill name; remove old revisions explicitly using their original export and digest.

The procedure is sanitized guidance, not an execution permission or policy override. Skills do not authorize actions. No raw traces, reviewer identity, approval rationale, credentials, or telemetry are sent to Cursor. This improves project continuity but does not satisfy the Cursor Cloud Agents telemetry/source parity row or the all-or-nothing cutover gate.
