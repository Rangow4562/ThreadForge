# Devin CLI observational hooks

Threadforge supports a partial, local-first hook integration for Devin CLI. It does not configure Devin Cloud Agents or Devin Desktop; those are separate source-matrix entries.

## Install and remove

Run from the `adapters/` directory:

```powershell
npm.cmd run adapter -- devin install --scope user
npm.cmd run adapter -- devin install --scope project --project-root <absolute-project-path>
npm.cmd run adapter -- devin uninstall --scope user
npm.cmd run adapter -- devin uninstall --scope project --project-root <absolute-project-path>
```

`user` is the default scope. User hooks are merged under `hooks` in `~/.config/devin/config.json`; project hooks are written as a standalone event map in `<project>/.devin/hooks.v1.json`. The default command invokes the current Threadforge TypeScript CLI. Packaged deployments should pass `--command-template` with exactly one `{event}` placeholder and the `--threadforge-devin-hook=v1` marker.

Install/update preserves unrelated root settings, unowned hook references, and other hook groups. Uninstall removes only command references containing Threadforge's ownership marker. Existing malformed, oversized, symlinked, or concurrently changed config is rejected without mutation. Config reads are capped at 8 MiB; writes use a per-directory lock and compare-before-atomic-replace. Restart Devin CLI after changing hooks.

## Events and privacy

The adapter maps `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `Stop`, and `SessionEnd`. Session and tool-call identifiers are SHA-256 hashed; workspace roots are hashed; prompt/tool content is represented only by byte counts and bounded SHA-256 digests (64 KiB digest limit). Hook stdin is capped at 1 MiB and event delivery is restricted to the local `/v1/events` receiver with a 250 ms timeout. Invalid payloads and unavailable intake fail open, produce no stdout, and never allow or deny an action. A permission hook means only “request observed”; no approval outcome is inferred or retained.

## Compatibility boundary

The event list and config paths are based on the pinned Beacon runtime documentation/source snapshot recorded in `docs/legacy-baseline.md`. This is behavior-reference-only; Threadforge's adapter code is newly authored. Devin's live payload field requirements, timeout units, hook response semantics, current version compatibility, and packaged command loading have not been validated against an installed host. The source row therefore remains partial and is not cutover evidence.
