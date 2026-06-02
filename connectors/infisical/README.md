# connectors/infisical

Read and write secrets, and manage the surrounding project/environment/
membership structure, in an [Infisical](https://infisical.com) workspace.
One pgf instance = one Infisical **machine identity** (Universal Auth
clientId + clientSecret) on one deployment (cloud US/EU or self-hosted).

## Resource hierarchy

Mirrors Infisical's UI:

```
project (workspace)
  └── environment   (slug: dev / staging / prod / …)
        └── folder  (secretPath, default "/")
              └── secret (by name)
```

Most secret/folder actions therefore take `project_id` + `environment` +
`secret_path` + `name`. `project_id` and `environment` fall back to the
credential's `default_project_id` / `default_environment` when omitted, so
day-to-day calls stay short.

## Actions

| Group | Actions |
|---|---|
| **Projects** | `list-projects` · `create-project` (optionally pre-creates dev/staging/prod) · `get-project` · `update-project` (rename, delete-protection, …) · `delete-project` (refused while delete-protection is on) |
| **Environments** | `list-environments` · `create-environment` · `update-environment` (rename/reorder — slug changes break callers) · `delete-environment` (deletes its secrets; refused if it's the only one) |
| **Folders** | `list-folders` · `create-folder` · `delete-folder` (refuses non-empty on most versions) |
| **Secrets** | `list-secrets` (plaintext via `/raw`; optional `recursive`, `include_imports`) · `get-secret` · `create-secret` · `update-secret` (value/comment; no rename in the raw API) · `delete-secret` (hard delete, no undo) |
| **Org members** | `list-org-members` · `update-org-member-role` · `remove-org-member` |
| **Project members** | `list-project-members` · `add-project-member` (emails *or* usernames + role slugs) · `update-project-member-role` (roles stack) · `remove-project-member` |
| **Project identities** | `list-project-identities` · `add-project-identity` · `remove-project-identity` |

`value` on `create-secret` / `update-secret` is a `FieldSecret` — encrypted
at rest in pgf's own store, never echoed on the CLI.

## Credential

| Field | Required | Default | Notes |
|---|---|---|---|
| `api_base` | yes | `https://app.infisical.com` | Without `/api` suffix. Presets: Cloud US, Cloud EU (`eu.infisical.com`), Self-hosted. |
| `client_id` | yes | — | Organization → Identities → *identity* → Universal Auth → Client ID. |
| `client_secret` | yes | — | Same identity. Shown only at creation — regenerate if lost. `FieldSecret`. |
| `default_project_id` | no | — | Pre-fills `project_id` on every action. |
| `default_environment` | no | `dev` | Pre-fills `environment`. |
| `default_org_id` | no | — | Used by org-level actions. A Universal Auth identity is bound to one org. |

### Auth & Validate

Auth is **Universal Auth**: on `Open()` (and `Validate`) the connector
`POST`s `clientId`+`clientSecret` to
`/api/v1/auth/universal-auth/login` and receives a short-lived Bearer
token (~2 h TTL), held in memory for the session. `Validate` confirms the
base URL is reachable and the identity logs in — it does **not** probe any
specific project, so per-action calls surface scope errors when the
identity reaches for something it can't see.

> **Long-running daemons:** there is no on-401 token refresh yet (v1). For
> per-action `pgf run` calls the re-login on `Open` is negligible; a
> long-lived `pgf serve` instance may outlive the token.

## E2EE note

The connector uses the v3 **`/raw`** endpoints — the server-decrypted
variants — because the agent wants plaintext values, not E2EE-wrapped
blobs. **Workspaces with end-to-end encryption enabled will refuse `/raw`
access**; the action layer surfaces that error verbatim. Disable E2EE in
the workspace settings, or handle the encryption client-side outside this
connector.

## Usage

```bash
# Setup — pick a preset, paste clientId/clientSecret (secret, no echo)
pgf connect infisical main

# With default_project_id + default_environment set, calls stay short:
pgf run infisical/main list-secrets
pgf run infisical/main get-secret -p name=DATABASE_URL
pgf run infisical/main create-secret -p name=API_KEY -p value='…' -p comment='issued 2026-06'
pgf run infisical/main update-secret -p name=API_KEY -p value='…'
pgf run infisical/main delete-secret -p name=OLD_TOKEN

# Explicit project/environment/path
pgf run infisical/main list-secrets \
  -p project_id=6f3a… -p environment=prod -p secret_path=/services/api

# Structure
pgf run infisical/main list-projects
pgf run infisical/main create-project -p name='New Service' -p create_default_envs=true
pgf run infisical/main create-folder -p name=services -p secret_path=/

# Membership
pgf run infisical/main list-project-members -p project_id=6f3a…
pgf run infisical/main add-project-member -p project_id=6f3a… \
  -p emails=alice@example.com -p roles=member
pgf run infisical/main add-project-identity -p project_id=6f3a… \
  -p identity_id=… -p roles=admin
```

## Known gaps

- **No secret rename.** The raw API has no rename op — `update-secret`
  changes value/comment only. Recreate under the new name (or rename in the
  UI) to rename.
- **No token refresh** on the in-memory Bearer (see auth note) — re-login
  happens per `Open`, fine for `pgf run`, a gap for long-lived `serve`.
- **E2EE workspaces unsupported** via the plaintext `/raw` path (see above).
- **Members must pre-exist.** `add-project-member` / `add-project-identity`
  grant access to users/identities already created in the org; the initial
  org invite happens in the Infisical UI.
- **No secret-versioning / rollback, dynamic secrets, or secret-sharing
  links** — only the static-secret CRUD surface is wrapped.
