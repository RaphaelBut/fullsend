---
title: GitLab Role-Credential Contract
---

# GitLab Role-Credential Contract

This is the internal contract for GitLab responsibility identities:
built-in **Poller**, **Analyst**, and **Coder**, plus optional
administrator-registered **custom roles**. It implements
[#7497](https://github.com/fullsend-ai/fullsend/issues/7497) under the
three-role decision in [#7424](https://github.com/fullsend-ai/fullsend/issues/7424)
and parent [#7496](https://github.com/fullsend-ai/fullsend/issues/7496).

The Go package is [`internal/gitlabroles`](../../internal/gitlabroles/).
Provisioning, routing, rotation, and shared-token retirement are
follow-up issues; this document is the contract those issues implement
against.

Built-in and custom roles are the same kind of registry entry. Job
credential selection walks that registry; it does not switch on a
three-role enum.

**Current runtime is unchanged.** When the migration gate is unset or
`disabled`, jobs continue to authenticate with the shared
`FULLSEND_FORGE_TOKEN` project access token described in
[ADR 0067](../ADRs/0067-gitlab-cron-polling-event-dispatch.md). Custom
roles are not required on existing installations.

## Registered roles

A **registered role** is an allowlisted GitLab responsibility identity.
It has:

- A stable name (`poller`, `analyst`, `coder`, or an administrator-
  chosen custom name).
- A kind: `builtin` or `custom`.
- Responsibility metadata (what the identity is for).
- A credential **reference** (own secret, or reuse of another
  registered role's secret). The registry never stores token values.
- Capability flags used for validation (not GitLab ACL grants).
- Agent / harness-role names that map onto it.

GitLab project-token scopes cannot express endpoint-level least
privilege; separate credentials give distinct audit identities, keep
Analyst eligible for native MR approval when Coder committed, and limit
the blast radius of a single compromise.

### Built-in roles

These three are always in the registry. Existing installations do not
need to declare them.

| Role | Responsibility | Must not |
| --- | --- | --- |
| **Poller** | Event/issue reads, pipeline dispatch, poll-state writes on `fullsend-poll-state-slash` and `fullsend-poll-state-events` | Modify application code or act as the Analyst approval identity |
| **Analyst** | Review, triage, prioritization, retrospectives, issue/reporting, notes, labels | Modify repository code or poll-state branches |
| **Coder** | Repository writes, code/fix work, merge-request creation and updates | Be used as the Analyst approval identity |

Stable Go names: `poller`, `analyst`, `coder`
(`gitlabroles.RolePoller` / `RoleAnalyst` / `RoleCoder`).

### Custom roles

An administrator may register additional roles. A custom role is a
first-class registry entry: the same `Resolve` path, the same
unconfigured / unregistered / auth-failed distinction, and the same
migration gate as the built-ins.

Custom roles are optional. An empty registry variable means built-ins
only.

## Trusted registry

The registry is installation state, not repository content.

| Source | Allowed? |
| --- | --- |
| Built-in table in `internal/gitlabroles` | Yes (always present) |
| Protected CI/CD variable `FULLSEND_GITLAB_ROLE_REGISTRY` | Yes (administrator-controlled JSON) |
| `.fullsend/config.yaml`, harness files, merge-request diffs, issue bodies | **No.** These may *reference* a registered name (for example a harness `role:` field). They must not create, rename, or elevate a role. |

`gitlabroles.LoadRegistry` / `ParseRegistry` are the only constructors
for custom roles. The JSON decoder rejects unknown fields, so a leaked
token cannot hide under a key such as `token`. `secret_name` must be a
CI/CD variable name (`FULLSEND_GITLAB_ROLE_SCANNER_TOKEN`); values that
look like GitLab PATs (`glpat-…`) are rejected.

A custom role cannot:

- Reuse a built-in name (`poller`, `analyst`, `coder`).
- Steal a built-in agent mapping (`review`, `code`, `fix`, …).
- Point `reuse` at an unregistered name or create a reuse cycle.
- Declare an unknown capability.

Harness `role:` and custom-agent names are validated with
`Registry.ValidateAgent`. An unregistered name returns
`ErrUnregistered`. Routing (#7499) is what enforces that at dispatch
time; this contract defines the check.

## Credential references

Each registration names how the role authenticates. The registry stores
**references and policy**, never raw secret values.

| `credential` | Meaning |
| --- | --- |
| `own` (default) | The role has its own masked CI/CD variable. Built-in names are listed below. Custom names derive `FULLSEND_GITLAB_ROLE_<NAME>_TOKEN` (hyphens become underscores). |
| `reuse` | The role shares another **registered** role's credential. `reuse` is the target role name. Presence and rotation follow the target. |

Reuse is how a custom agent can share Coder (or another role) without
minting a second PAT. It is not a silent fallback: the job still
selects that role's identity, and a runtime auth failure of the shared
secret still fails closed.

## Capabilities

Capabilities are contract metadata for validation. Every role token is
still GitLab Developer (30) with the `api` scope; do not document these
flags as least-privilege API grants.

| Capability | Typical holder |
| --- | --- |
| `read_issues` | Poller, Analyst, Coder |
| `write_issues`, `write_notes`, `write_labels` | Analyst |
| `approve_merge_request` | Analyst |
| `dispatch_pipeline`, `write_poll_state` | Poller |
| `write_repository`, `write_merge_request` | Coder |

`Registration.Has` is the check routing (#7499) will use so Analyst
cannot perform code writes through the normal role configuration, and a
custom role cannot exceed the capabilities the administrator declared.

## Identifiers

### CI/CD variables

Role tokens are **masked, protected** project CI/CD variables, same
storage as today's shared bot PAT. The migration gate and the registry
document are **protected and unmasked** so status and logs can print
mode and policy without exposing secrets.

| Name | Kind | Purpose |
| --- | --- | --- |
| `FULLSEND_FORGE_TOKEN` | masked secret | Shared bot PAT. Unchanged default path. |
| `FULLSEND_GITLAB_POLLER_TOKEN` | masked secret | Poller PAT. Optional until provisioning. |
| `FULLSEND_GITLAB_ANALYST_TOKEN` | masked secret | Analyst PAT. Optional until provisioning. |
| `FULLSEND_GITLAB_CODER_TOKEN` | masked secret | Coder PAT. Optional until provisioning. |
| `FULLSEND_GITLAB_ROLE_<NAME>_TOKEN` | masked secret | Custom role PAT when `credential` is `own`. Optional until provisioning. |
| `FULLSEND_GITLAB_ROLE_MIGRATION` | unmasked variable | Feature gate. Absent or empty = `disabled`. |
| `FULLSEND_GITLAB_ROLE_REGISTRY` | unmasked variable | Administrator registry JSON. Absent or empty = built-ins only. |

Canonical constants live in [`internal/forge/forge.go`](../../internal/forge/forge.go)
(`SecretForgeToken`, `SecretGitLabPollerToken`,
`SecretGitLabAnalystToken`, `SecretGitLabCoderToken`,
`VarGitLabRoleMigration`, `VarGitLabRoleRegistry`). Custom secret names
are derived by `gitlabroles.CustomSecretName`.

Role secrets and the registry **must not** be added to
`requiredSecretsForForge` while the gate is disabled. Existing
installations would otherwise fail health checks for secrets they do
not have.

### Project access token names

| Role | PAT name | Access | Scopes |
| --- | --- | --- | --- |
| Shared (today) | `fullsend-bot` | Developer (30) | `api` |
| Poller | `fullsend-poller` | Developer (30) | `api` |
| Analyst | `fullsend-analyst` | Developer (30) | `api` |
| Coder | `fullsend-coder` | Developer (30) | `api` |
| Custom `own` | `fullsend-role-<name>` | Developer (30) | `api` |
| Custom `reuse` | (none; uses the target role's PAT) | — | — |

Provisioning (#7498) creates these tokens. This contract only names
them. Access level and scopes match the current shared bot; do not
claim finer GitLab permissions than the implementation uses.

## Job → role mapping

| Job | Role |
| --- | --- |
| GitLab poller/controller (`fullsend poll`, `fullsend-poll.yml`) | Poller |
| Agents / harness roles `review`, `triage`, `prioritize`, `retro`, `scribe` | Analyst |
| Agents / harness roles `code`, `fix`, `coder` | Coder |
| Custom agent whose name or harness `role:` is listed on a registered custom role | That custom role |

`Registry.RoleFor` accepts either an agent name or a harness `role:`
value. Built-in aliases and custom agent names share this lookup.

Unmapped jobs (for example `e2e` or an unregistered custom agent) keep
working on the shared token when the gate is `disabled` or `rollback`.
In `migrating` and `enforced` they fail closed (`ErrUnregistered`)
rather than guessing an identity. `ValidateAgent` itself takes no mode
and always rejects an unmapped name, so routing (#7499) must only call
it as a pre-check ahead of `Resolve` when the migration gate is
`migrating` or `enforced`; calling it unconditionally ahead of the
legacy `disabled`/`rollback` path would break existing installations
that rely on unmapped jobs falling back to the shared token.

## Migration gate

`FULLSEND_GITLAB_ROLE_MIGRATION` is the only switch that changes
credential selection. Values are case-insensitive; unknown values fail
closed (`ErrInvalidMode`) so a typo cannot silently disable the gate.

| Mode | When | Shared token used | Missing role secret |
| --- | --- | --- | --- |
| `disabled` (default, unset) | Existing installations | Always | Ignored |
| `migrating` | Additive rollout after #7498 | Only as **explicit** fallback when that role is unconfigured | Use shared token; report pending |
| `rollback` | Operator-initiated rollback | Always | Ignored (role secrets unused) |
| `enforced` | After verification (#7501) | Never | Fail (`ErrUnconfigured`) |

The shared token is **not** selected after an arbitrary
role-credential failure. The only legitimate shared-token uses are:

1. `disabled` (legacy path)
2. `rollback` (explicit operator action)
3. `migrating` **and** the role secret is absent/empty (not yet provisioned)

An unregistered name is never a reason to use the shared token in a
role-aware mode.

## How a job selects its credential

Call `gitlabroles.Resolve` with:

- `Mode` from `gitlabroles.ModeFrom` (the gate variable)
- `Job` (`PollerJob()` or `AgentJob(name)`)
- `Registry` from `LoadRegistry` (zero value = built-ins only)
- `Present`: a boolean map of whether each secret *name* is non-empty
  (`PresenceFrom`). **Never put token values in this map.**
- `FailedSecret`: the secret *name* that already failed authentication
  in this job, or empty

The result is a `Source` whose `SecretName` is the CI/CD variable to
read. Callers then `os.Getenv(src.SecretName)`. Built-in and custom
roles return through this same function.

Routing (#7499) is what wires `Resolve` into `fullsend poll`,
`fullsend run`, and GitLab CI templates. Until then, those paths keep
reading `FULLSEND_FORGE_TOKEN` directly.

## Unconfigured vs unregistered vs failed

These are different errors. Do not collapse them.

| Situation | Sentinel | Meaning |
| --- | --- | --- |
| Role name is not in the registry | `ErrUnregistered` | Custom agent referenced an unknown identity |
| Role secret absent or empty | `ErrUnconfigured` | Registered, not provisioned yet |
| Shared secret absent in `disabled`/`rollback` | `ErrSharedUnconfigured` | Legacy path broken |
| Runtime 401/403 (or equivalent) from a selected credential | `ErrAuthFailed` | Credential is present but unusable |
| Job kind is empty or unrecognized | `ErrUnknownJob` | No identity to select |
| Gate value is not a known mode | `ErrInvalidMode` | Fail closed |
| Registry JSON is malformed or untrusted | `ErrInvalidRegistry` | Fail closed; do not load custom roles |

In `migrating`, a registered role whose secret is absent but whose
shared token is present is **not** `ErrUnconfigured` — `Resolve`
returns a successful `Source` with `Fallback` set (explicit migration
fallback). `ErrUnconfigured` in `migrating` means both the role secret
and the shared token are absent. `ErrUnregistered` and `ErrAuthFailed`
never fall back to the shared token in any mode.

## No silent fallback on authentication failure

If a selected credential fails authentication or authorization, the job
fails. It does **not** retry as another identity, including the shared
bot.

`Resolve` enforces this when `FailedSecret` is set: it returns
`ErrAuthFailed` in every mode and returns a zero `Source`. Callers that
observe an auth failure must either pass that secret name back into
`Resolve` or stop; they must not call `Resolve` again with a different
job or a cleared `FailedSecret` in order to pick a substitute.

## Status, drift, and diagnostics

`gitlabroles.Diagnose(mode, present, registry)` is the observable
report:

- Per-role state: `configured` or `unconfigured` (presence only),
  including custom roles and reuse targets
- `Partial`: some but not all registered role secrets exist
- `Ready`:
  - `disabled` / `rollback`: shared token present
  - `migrating` / `enforced`: every registered role's credential is present
- `Missing`: registered roles whose secrets are absent
- `Diagnostics`: human-readable lines with **names only**

Classification of a missing role secret:

- `disabled` / `rollback`: not required (not drift)
- `migrating`: pending (informational; expected during rollout)
- `enforced`: missing/required (drift / fail closed)

A role secret that is present while the gate is `disabled` or
`rollback` is reported as "configured but unused". That is not an
error; leftover secrets after rollback are expected until uninstall or
rotation (#7500) removes them.

**Never** put token values in logs, status output, issue comments, or
`Error` strings. Presence booleans and variable names are the only
safe signals.

`repos status` / converge health checks stay on the shared-token
required set until provisioning (#7498) starts writing role secrets
under an enabled gate.

## Registry JSON shape

`FULLSEND_GITLAB_ROLE_REGISTRY` (protected, unmasked):

```json
{
  "roles": [
    {
      "name": "scanner",
      "responsibility": "read-only scanning",
      "credential": "own",
      "capabilities": ["read_issues", "write_notes"],
      "agents": ["scanner"]
    },
    {
      "name": "deployer",
      "credential": "reuse",
      "reuse": "coder",
      "capabilities": ["write_repository", "write_merge_request"],
      "agents": ["deploy"]
    }
  ]
}
```

`name` must match `^[a-z][a-z0-9_-]*$` with no double hyphen, the same
rule as mint role names. `secret_name` is optional on `own` and must
equal the derived `FULLSEND_GITLAB_ROLE_<NAME>_TOKEN` when set.

Provisioning (#7498) is what writes this variable. Agents and
repository files do not.

## What this contract does not do

Leave these to the follow-up issues. Do not implement them under #7497.

| Issue | Work |
| --- | --- |
| [#7498](https://github.com/fullsend-ai/fullsend/issues/7498) | Create/enroll built-in and custom PATs, store them as protected masked CI variables, write the registry, set the gate, report partial provisioning, preserve the shared token, reinstall/drift/uninstall |
| [#7499](https://github.com/fullsend-ai/fullsend/issues/7499) | Wire `Resolve` and `ValidateAgent` into poll, agent, and forge operations so each job uses only its registered role |
| [#7500](https://github.com/fullsend-ai/fullsend/issues/7500) | Rotation, recovery, in-flight jobs, expiry/revocation diagnostics |
| [#7501](https://github.com/fullsend-ai/fullsend/issues/7501) | Verification, enable `enforced`, retire the shared token |
| [#7502](https://github.com/fullsend-ai/fullsend/issues/7502) | ADR 0067 status annotation and operator-facing lifecycle docs |

## Security notes

- Threat priority remains external injection > insider > drift >
  supply chain. Separate identities reduce insider/compromise blast
  radius; they do not replace protected-variable and protected-branch
  controls from ADR 0067.
- Role registration is administrator-controlled installation state.
  Arbitrary repository or pull-request content cannot create or elevate
  a role.
- All role secrets stay protected and masked. The gate and registry
  variables are protected so only protected-branch pipelines observe a
  mode or policy change.
- GitLab `Developer` + `api` is still coarse. Do not document these
  tokens as least-privilege API grants.
- `CI_DEBUG_TRACE` remains forbidden on jobs that hold any of these
  variables.
