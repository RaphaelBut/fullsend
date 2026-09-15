---
title: "114. GitHub Packages access through a host-bound provider fed by the workflow token"
status: Accepted
relates_to:
  - security-threat-model
  - agent-infrastructure
topics:
  - security
  - sandbox
  - credentials
---

# 114. GitHub Packages access through a host-bound provider fed by the workflow token

Date: 2026-09-15

## Status

Accepted

## Context

Code and fix agents install dependencies inside the sandbox. A package on
GitHub Packages that another organization owns cannot be read with the minted
App token, even after the mint grants `packages: read`: GitHub scopes
installation tokens to the packages of the org the App is installed in and
answers `403 Permission installation not allowed to Read organization package`
(measured 2026-09-09, fullsend#6649). Anonymous requests get 401 even for public
packages. The job's own Actions token with `packages: read` succeeds, and the
tarball is then served from `pkg-npm.githubusercontent.com` through a
pre-signed URL that expires in minutes and carries no bearer credential.

After the mint the only credential in the runner process was the App token, and
the reusable workflows forward no user secret to the code job, so a repo-level
provider had nothing usable to bind. Sandboxed agents receive credentials only
through OpenShell providers and L7 egress policy
([ADR 0017](0017-credential-isolation-for-sandboxed-agents.md),
[ADR 0025](0025-provider-credential-delivery-for-sandboxed-agents.md),
[ADR 0065](0065-provider-backed-policy-composition.md)); inference credentials
already follow the run-scoped provider pattern
([ADR 0092](0092-openai-wif-credential-delivery.md)).

## Options

- **Rely on the minted App token.** Insufficient by construction for packages
  owned by another org; works only same-org, where `${GH_TOKEN}` already serves.
- **Export the Actions token into the sandbox environment.** The real value
  would land in transcripts and artifacts and could be replayed at any
  allowlisted host. Rejected.
- **A user-supplied PAT.** No passthrough exists for repo secrets, and it would
  add a long-lived personal credential where the job already holds a
  short-lived one. Rejected.

## Decision

**The Actions workflow token reaches the sandbox only as an OpenShell provider
credential bound to the GitHub Packages hosts; forge identity stays the minted
App token.**

1. On GitHub Actions, `mintAgentToken` copies the pre-mint `GH_TOKEN` to
   `FULLSEND_WORKFLOW_TOKEN` before replacing `GH_TOKEN` with the minted token,
   masks it, and unsets it at cleanup. Outside Actions nothing is copied: a local
   PAT never becomes a workflow token, and a caller-set value is left alone.
2. The variable is a new credential class, provider-only: every harness `${}`
   site (`runner_env`, `env.runner`, `env.sandbox`, `host_files`,
   `validation_loop.schema`) refuses it, pre/post/validation child environments
   strip it, `env.sandbox` cannot name it, and redaction knows its value. Only
   provider credential expansion reads it, so the sandbox holds a placeholder.
3. A repo opts in with a provider (`type: fullsend-github-packages`) whose
   profile binds the placeholder to `npm.pkg.github.com:443` and allowlists
   `pkg-npm.githubusercontent.com:443` without a credential, both read-only and
   enforced. The proxy rejects the placeholder at any other host, so
   `api.github.com` and `github.com` keep resolving the App token, and the
   post-script keeps pushing with `PUSH_TOKEN`.
4. Nothing ships by default. The provider, profile and `~/.npmrc` line live in
   the repository's `.fullsend`, read from the trusted ref.

## Consequences

- Cross-org GitHub Packages installs work with the repository's own token and no
  new identity or secret.
- The token's write permissions are unreachable from the sandbox: only the two
  registry hosts resolve the placeholder, both read-only, and forge writes still
  go through the post-script with the App token.
- GitLab and local runs are unchanged, because nothing is preserved outside
  Actions.
- Provider definitions read from the trusted ref may now reference one more
  runner credential; repos that ship other providers should review their `${}`
  uses.
- Shipping the provider by default and other registries such as `ghcr.io` are
  follow-on decisions.
