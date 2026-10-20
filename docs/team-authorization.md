# Authenticated team procedure publication

Shared procedure files are no longer trusted because they merely contain an `approved_by` name or a `privacy_reviewed` Boolean. Every new project-shared revision is a single `threadforge.shared-procedure-record.v1` envelope containing the sanitized procedure and a detached Ed25519 attestation. The signature binds the SHA-256 of the compact JSON serialization of the sanitized graph, action, project, procedure ID, revision, and privacy-review claim; it is verified on every project-file read.

## Provision identities and trust

Generate an Ed25519 identity locally. Keep both the private key and its backups outside project repositories:

```powershell
threadforge team identity generate --private-key <secure-user-directory>\reviewer-private.json --public-key <temporary-public-directory>\reviewer-public.json
```

Keep the private file private and protected by the current OS account's filesystem permissions. Threadforge writes it with mode `0600` and checks Unix permission bits; on Windows, store it only in a directory whose ACL grants access to the current account and administrators, because Go's portable file-mode API cannot verify the full Windows ACL. Only the public identity file may be sent to a team administrator. The public `key_id` is SHA-256 of the raw Ed25519 public key. The private key is never printed by Threadforge.

A team administrator creates a policy from public identities, binding each key to a stable `subject`, exact `projects`, and explicit roles. Policy v1 remains procedure-only; policy v2 adds `continuity.write`, `continuity.review`, `continuity.requirements.finalize`, and `continuity.plan.confirm` to the procedure roles `procedure.approve`, `procedure.publish`, `procedure.privacy-review`, and `procedure.supersede`. Do not accept policy files from a project checkout as trust roots. Distribute the policy and its SHA-256 through separate trusted channels, then validate and install it:

```powershell
threadforge team trust validate --file .\trusted-reviewers.json
threadforge team trust install --file .\trusted-reviewers.json --expected-sha256 <digest-from-trusted-channel>
```

The installed policy is outside the project, at `THREADFORGE_HOME/team-trust/trusted-reviewers-v1.json` (or the default local Threadforge data directory). Installation requires an exact out-of-band digest pin and stores that digest in the adjacent `.sha256` file. Every installed-policy read compares the policy bytes to this pin; a missing, malformed, or changed pin fails closed. Removing the policy but leaving its pin is also treated as an invalid installation, not as a return to local-only mode. Each project grant is registered append-only in the local SQLite store, so removing the policy cannot silently convert an already team-scoped project to local-only. Revocation or role changes require installing a newly pinned policy and restarting long-running Threadforge services so they reload it.

## Authorize local task and session writes

If a project ID appears in any installed member's `projects` grants, Threadforge's CLI requires `--identity-key <private-key.json>` for every task, requirements, plan, continuity-entry, and session mutation in that project. Projects absent from the policy retain local-only actor labels. The selected identity must be explicitly granted `continuity.write`; `task plan confirm` additionally requires `continuity.plan.confirm`; requirements finalization or reopening additionally requires `continuity.requirements.finalize`; and review/verification entries additionally require `continuity.review`. The trusted policy subject becomes the stored actor. A caller-supplied `--actor` must be that subject (or the default `local-user` label).

Example, after installing a policy that grants the key `continuity.write` for `my-project`:

```powershell
threadforge task update --id <task-id> --expected-revision <n> --summary "Reviewed progress" --identity-key <secure-user-directory>\member-private.json
```

The CLI signs each mutation with Ed25519. The detached `threadforge.continuity-attestation.v1` binds the trusted subject/key, canonical policy digest, action and audit action, project/resource, expected revision, exact entity JSON, and audit reason. SQLite verifies it against the currently installed pinned policy inside the mutation transaction, then stores it atomically beside task/session audit rows or inside the append-only continuity entry. Direct `Store` writes are also rejected for registered team projects unless they carry a valid proof. The local loopback HTTP and MCP continuity endpoints remain read-only.

This proves possession of an administrator-provisioned key, not a remote login or an unforgeable human identity. Keep private keys behind OS ACLs and do not make a human approval key available to an agent that should not exercise that role. The SQLite store retains one canonical policy snapshot and its first-seen installed source-file digest for each policy version; every mutation separately rechecks the current file against its adjacent out-of-band pin. Signed authorization binds to the canonical policy, so harmless JSON formatting changes do not create a conflicting policy version. `Store.VerifyContinuityAuthorization` can verify old evidence after policy rotation. Use `threadforge team audit verify` to check the local team continuity ledger against retained policy snapshots; the command is a consistency check, not tamper-proof storage against a SQLite administrator.

Verify the local team continuity ledger and all retained signed task, session, and entry mutations with:

```powershell
threadforge team audit verify --db .\threadforge.sqlite
```

The versioned JSON report checks each team-scoped revision against its matching append-only audit record, reconstructs the signed entity/reason digest, and verifies the Ed25519 signature against the retained canonical policy snapshot. Missing or invalid evidence produces `failed` and a nonzero exit. Unsigned rows dated before the project was first registered as team-scoped produce `review_required` and a nonzero exit; they are never counted as verified. `no_team_projects` is also nonzero so an empty audit cannot be mistaken for a pass. Findings are capped at 200, while aggregate invalid counts continue increasing. This is a local consistency check, not tamper-proof storage against an administrator who can rewrite the SQLite file and its schema.

## Local task claims

Task claims provide advisory, single-local-store coordination over declared path, module, API, or schema scopes. Acquire one with a caller-computed code-baseline digest and one or more repeatable scopes:

```powershell
threadforge task claim acquire --task-id <task-id> --id <claim-id> --expected-task-revision <n> --baseline-sha256 <digest> --scope 'path:src/api/**' --lease-seconds 3600
threadforge task claim list --task-id <task-id> --baseline-sha256 <current-digest>
threadforge task claim renew --task-id <task-id> --id <claim-id> --expected-claim-revision <n> --expected-task-revision <task-revision> --baseline-sha256 <same-digest> --lease-seconds 3600
threadforge task claim revalidate --task-id <task-id> --id <claim-id> --expected-claim-revision <n> --expected-task-revision <current-task-revision> --baseline-sha256 <new-digest>
threadforge task claim release --task-id <task-id> --id <claim-id> --expected-claim-revision <n>
```

Lease duration defaults to 3600 seconds and is bounded to 60–86400 seconds. A task can have only one active claim. Active claims with an overlapping scope in the same scope kind block another acquisition; path conflicts recognize exact paths and trailing `/**` subtrees, while module/API/schema labels conflict on exact equality. An expired lease is reported as expired but remains active and blocks takeover until the owner explicitly releases it. Renewal is rejected when the task revision or supplied baseline differs; revalidation must extend the lease and refresh at least one of those bindings. Scope changes require release and a new claim. Claim mutations must use the recorded owner; that identity is cryptographically enforced for registered team projects, while local-only `--actor` values are labels, not authenticated identities.

The baseline digest is caller-supplied and compared as a commitment; Threadforge does not inspect the working tree or prove that the digest represents the claimed code. `baseline_status=unverified` is returned unless the caller supplies a current digest to `show`/`list`. Claims are append-only, credential-screened continuity events in the private local SQLite store. A registered team project requires a signed `continuity.write` authorization for each event, which `team audit verify` replays and checks. Claims are not synchronized by `team sync`, are not a filesystem lock or execution permission, and do not authorize tool calls.

## Local task handoffs

The local `threadforge task handoff` commands implement `draft → offered → accepted|rejected|cancelled`. Acceptance atomically updates the task owner/head, transfers the active claim without extending its lease, inserts the linked checkpoint, and appends the handoff event. Each changed team record receives a separate signed `continuity.write` authorization; `team audit verify` checks transition order and cross-record links. Stale or expired claims cannot be accepted, and rejected/cancelled handoffs leave owner and claim unchanged. See the [handoff command contract](task-handoffs.md).

Task, claim, handoff, and checkpoint data remain local SQLite state and are not carried by Git-ref procedure sync. Handoff context is advisory and never authorizes execution. Historical Mancode handoff migration is still unverified.

## Team project scope lifecycle

Team project scopes are retained permanently. An explicitly authorized decommission freezes task/session/entry mutations and shared procedure publication/supersession without deleting continuity history, published files, or the scope registration. Publication and continuity mutations serialize through a per-project SQLite write gate, so a concurrent decommission cannot slip between the active-state check and the write. The trust policy must grant `team.scope.manage`; the operator must also provide the private identity, an audit reason, and the exact project ID twice as a confirmation:

```powershell
threadforge team scope status --project-id <project-id> --db .\threadforge.sqlite
threadforge team scope decommission --project-id <project-id> --confirm-project-id <same-id> --identity-key <admin-private.json> --reason "Project is leaving this team" --db .\threadforge.sqlite
```

Decommission and restoration events are append-only, signed against their exact scope revision, and included in `threadforge team audit verify` (`threadforge.team-continuity-audit.v2`). Continuity mutations and shared procedure publication/supersession fail while decommissioned. To restore those writes, the organization administrator must install and independently pin a new policy for the same organization with a different canonical policy digest, grant `team.scope.manage`, and use a signing key different from the decommissioning signer. The verifier checks both policy rotation and signer-key rotation:

```powershell
threadforge team scope restore --project-id <project-id> --confirm-project-id <same-id> --identity-key <new-admin-private.json> --reason "Reauthorize project under rotated team trust" --db .\threadforge.sqlite
```

This signed freeze/reactivation lifecycle does not erase local records or credentials, and it does not make SQLite tamper-proof against a local administrator.

## Git-ref synchronization of published procedures

Threadforge's Git-ref transport is intentionally narrower than Mancode's team-authority transport: it exchanges **only** authenticated records already published under `.threadforge/procedures/<procedure-id>/`. It never sends SQLite data, tasks, sessions, decisions, raw traces, private keys, credentials, branches, or worktrees. Every record is revalidated against the receiving clone's independently installed and pinned trust policy before materialization. The active team-project scope gate is held for local imports and the remote push CAS, so a decommissioned scope cannot publish or accept shared procedures.

Configure the same repository as a normal Git remote in each clone and register the same project ID in each clone's local team scope. After a reviewer has published one or more procedures in clone A, the first push starts from revision zero:

```powershell
threadforge team sync push --project-root . --project-id <project-id> --remote origin --expected-revision 0
```

Other clones pull the published records locally:

```powershell
threadforge team sync pull --project-root . --project-id <project-id> --remote origin
```

Each successful push receipt returns the new `remote_revision`; use that exact revision for the next push. Push uses one Git-ref compare-and-swap and fails if the remote revision advanced. It also refuses to remove a remote record or overwrite a divergent same-revision record: pull and review the remote snapshot before retrying. Pull is additive and idempotent; if a write is interrupted, repeating pull reconciles identical immutable files. The remote ref is `refs/threadforge/team/<project-hash>` and contains a versioned manifest plus digest-committed, signed procedure records. Local appdata stores only immutable high-water pins (project/remote digests, revision, manifest digest, and Git commit) so a clone that has synced before rejects a rewound or rewritten ref. A brand-new clone has no prior remote pin; its first sync therefore relies on the configured remote plus the independently pinned procedure-signing policy. Remote identity is bound by a digest over the canonical repository identity; URL userinfo and query strings are omitted and never written to the manifest or receipt. Fetch and push URLs must resolve to the same repository. Git's interactive credential prompts are disabled; configure credentials through the existing Git credential mechanism before invoking sync.

The manifest schema is [team-procedure-manifest-v1](../schema/team-procedure-manifest-v1.schema.json), and command output uses [team-sync-receipt-v1](../schema/team-sync-receipt-v1.schema.json). This is not a backup/restore path for the local database, nor is it Mancode-compatible synchronization of claims, handoffs, ownership fences, task state, or transport migration/recovery. Git remains responsible for business-code branches and worktrees.

## Local SQLite recovery

Create and verify a local snapshot, then restore it only to a new staged ID:

```powershell
threadforge team recovery snapshot --id before-upgrade
threadforge team recovery verify --id before-upgrade
threadforge team recovery restore --snapshot-id before-upgrade --restore-id after-disk-repair --confirm-restore-id after-disk-repair --reason "Recover local store after disk repair"
threadforge team recovery verify --id after-disk-repair --kind restore
threadforge team recovery activate --restore-id after-disk-repair --activation-id usable-copy --confirm-activation-id usable-copy --reason "Confirm local recovery"
# For team-managed scopes, add: --identity-key <team-admin-private.json>
```

Bundles are written only below `<THREADFORGE_HOME>/team-recovery/{snapshots,restored}/`; the default home is Threadforge's local data directory. IDs are lowercase letters, digits, and hyphens (1–64 characters). Snapshot creation uses SQLite's online backup API so an open store is copied consistently. Each private bundle contains only `store.sqlite` and a strict `manifest.json`, with schema version, byte size, SHA-256, creation time, source snapshot binding (for restores), and a local reason. Verification rejects symlinks, extra files/SQLite sidecars, unsupported schemas, integrity or foreign-key failures, and digest/size mismatches. Snapshots and restore IDs are immutable; an existing ID is never overwritten. Raw database contents remain local and are not uploaded or team-shared.

A restore is intentionally **staged, not activated**. The restored SQLite file carries an internal recovery-state marker; ordinary `Store.Open` refuses it, preventing a pre-decommission snapshot from silently re-enabling team writes. Its receipt and manifest set `activation_allowed=false`. `activate` copies the staged file again to a different ID, registers the currently pinned policy's project scopes, signs a decommission event for every active scope using the current trusted `team.scope.manage` identity, verifies the complete team audit, and only then sets the new copy's internal state to active. Existing decommissioned scopes remain frozen. If any active scope lacks a current trusted signer, or the audit needs review, activation fails and publishes no new active copy. A local-only store still requires the exact confirmation ID and an activation reason. The activated copy is never made the default store automatically; callers must explicitly select its `store.sqlite`. Its manifest records the activation method, frozen-scope count, audit status, and activation-time database digest. After normal use changes the DB, `verify --kind restore` reports `modified_since_activation` alongside fresh SQLite integrity checks rather than treating the activation-time digest as current. This local SQLite facility does not implement Mancode's Git-ref team transport migration/recovery, cross-device synchronization, or full backup acceptance.

## Publish and verify

The reviewer named in the graph's existing approval record must map to the signing key's trusted subject. The key must be authorized for that project and hold `procedure.approve`, `procedure.publish`, and `procedure.privacy-review`. Supersession also requires `procedure.supersede`. Privacy review remains an explicit `--privacy-reviewed` action and the graph itself must already have `privacy_reviewed: true`.

```powershell
threadforge procedure publish --project-root . --id <procedure-id> --expected-revision <n> --identity-key <secure-user-directory>\reviewer-private.json --privacy-reviewed
threadforge procedure publish-supersession --project-root . --id <procedure-id> --expected-revision <n> --identity-key <secure-user-directory>\reviewer-private.json --privacy-reviewed
```

The immutable revision file contains both the graph and its signed attestation, avoiding a graph/signature sidecar race. Readers check the trust policy, role and project grants, signature, graph digest, revision, privacy claim, and—on ordinary publication—that the authenticated subject matches `approved_by`. Supersession remains a signed, privacy-reviewed tombstone; old IDs cannot be revived. Unsigned legacy shared files and tampered envelopes fail closed and are not guidance.

The policy administrator must verify the human-to-key binding out of band. Key loss or compromise requires a newly pinned policy; decommissioned scopes remain registered and frozen until restoration is authorized under a different canonical policy digest. Shared procedures and continuity records remain advisory, never execution authorization. See the [procedure attestation schema](../schema/team-attestation-v1.schema.json), [continuity attestation schema](../schema/continuity-attestation-v1.schema.json), [trust policy v1 schema](../schema/team-trust-policy-v1.schema.json), [trust policy v2 schema](../schema/team-trust-policy-v2.schema.json), and [shared record schema](../schema/shared-procedure-record-v1.schema.json).
