# connectors/youtrack

YouTrack (JetBrains) connector. Permanent-token auth, REST + JSON.
Multi-user access by registering one pantograf instance per user-token —
YouTrack has no impersonation, so each call runs as the token's owner
with their permissions.

## Actions

### Read

| Name | Description |
|---|---|
| `me` | Identity of the bound token. Cheapest validate. |
| `list-users` | Paginated user search with optional YouTrack query. |
| `get-user` | Resolve by login or id. |
| `list-projects` | All projects visible to this token. |
| `get-project` | By shortName (e.g. MW) or id. |
| `list-issues` | Search by `project_key` + YouTrack query syntax. |
| `get-issue` | Accepts the human-readable id (`PGFT-1`). |
| `list-attachments` | Files on an issue. |
| `list-project-team` | Members of a project's team (read-only — see below). |

### Write

| Name | Description |
|---|---|
| `create-user` | Admin. Password is required on this instance (reset-via-email isn't configured). |
| `create-project` | Admin. Lead defaults to `/me` if omitted. |
| `create-issue` | `project_key` resolved internally; optional `assignee_login`. |
| `add-comment` | Plain or markdown comment on an issue. |
| `attach-file` | Multipart upload. |
| `download-attachment` | Streams the binary to a local path (auth-aware redirect to `/_persistent/...`). |

### State changes (universal)

| Name | Description |
|---|---|
| `apply-command` | Apply a YouTrack command-bar string to one or more issues. Covers **every** custom field change without per-field $type ceremony. |
| `set-assignee` | Convenience wrapper over `apply-command "Assignee X"`. |

### Auth/admin

| Name | Description |
|---|---|
| `create-token` | Admin mints a Hub permanent token for any user. Auto-resolves the YouTrack service scope (no need to know the UUID). Token shown ONCE — save immediately. |

## Credential

| Field | Required | Default | Notes |
|---|---|---|---|
| `token` | yes | — | `perm-...` from YouTrack Profile → Authentication. `FieldSecret`, encrypted at rest. |
| `base_url` | yes | — | e.g. `https://tasks.sistemica.cloud` (no trailing `/api`). |

`Validate` calls `/api/users/me` and prints the bound login + fullName.

## Multi-user setup

YouTrack auth is per-user permanent tokens — there's no impersonation.
The pantograf-native pattern is one instance per user-token:

```bash
# admin (your token, has admin permissions)
pgf connect --input '{"token": "perm-...admin", "base_url": "https://tasks.sistemica.cloud"}' \
            youtrack admin

# mint a token for a user (admin operation)
pgf run youtrack/admin create-user \
  -p login=katharina -p email=k@example.com -p full_name="Katharina" -p password=...
TOKEN=$(pgf run youtrack/admin create-token -p user=katharina | jq -r .token)

# save that user's token as a separate instance
pgf connect --input "{\"token\": \"$TOKEN\", \"base_url\": \"https://tasks.sistemica.cloud\"}" \
            youtrack katharina

# now the same connector acts as different users:
pgf run youtrack/admin list-users
pgf run youtrack/katharina create-issue -p project_key=MW -p summary="..."
```

A bash alias makes it ergonomic:

```bash
yt() { pgf run youtrack/${YT_PROFILE:-admin} "$@"; }

yt issues list                                  # as admin
YT_PROFILE=katharina yt create-issue ...        # as katharina
```

## YouTrack command syntax cheatsheet (apply-command)

```bash
pgf run youtrack/admin apply-command -p issues=PGFT-1 -p command='...'
```

| What | Command string |
|---|---|
| Assignee | `Assignee katharina` |
| Unassign | `Assignee Unassigned` |
| Priority | `Priority Critical` |
| Type | `Type Bug` |
| State | `State In Progress` |
| Due date | `Due 2026-12-31` |
| Estimation | `Estimation 4h` |
| Tag | `tag urgent` |
| Multi-update | `Priority Critical Type Bug Due 2026-12-31` (one call) |

## Known limitations on YouTrack 2024+

| Capability | Status |
|---|---|
| Read project teams | ✓ via `list-project-team` |
| Add/remove team members via REST | ✗ — `/api/admin/projects/{id}/team/users` is GET-only; Hub `/usergroups` paths return 404 because project teams aren't Hub-backed in modern installs. **Use the YouTrack UI**: `<host>/projects/{id}/settings/access`. |
| Create user without password | ✗ on most self-hosted (need SMTP reset configured). Action requires `password`. |
| Attachment binary download | ✓ via the host-relative `_persistent/...` URL with the same Bearer token. |

## Hub vs YouTrack auth

Permanent tokens are minted via the **Hub** API (`/hub/api/rest/...`),
not YouTrack proper. The connector handles this automatically: it
resolves the user's `ringId` (Hub-side identity) and the right service
scope (auto-fetched, since the standard `0-0-0-0-0` is **YouTrack
Administration** which non-admin users can't use — that was a real bug
hit during testing).

## Usage

```bash
pgf connect youtrack admin                                # wizard
pgf run youtrack/admin me

# Create + assign + tag an issue in one go
pgf run youtrack/admin create-issue \
  -p project_key=PGFT -p summary="bug" -p description="..."
pgf run youtrack/admin apply-command \
  -p issues=PGFT-1 -p command='Priority Critical Type Bug Assignee admin'

# Attachments
pgf run youtrack/admin attach-file -p issue=PGFT-1 -p file=/tmp/log.txt
pgf run youtrack/admin list-attachments -p issue=PGFT-1
pgf run youtrack/admin download-attachment \
  -p issue=PGFT-1 -p attachment_id=12-2 -p out=/tmp/log.txt
```

## What to expect on the wire

- All requests need `?fields=...` to return useful data — YouTrack's
  default response is just `id`. Defaults pre-set in the connector.
- The connector wraps the same `transport/http` library used by Telegram,
  Lexware, etc. Bearer auth, no Accept hardcoded (binary downloads via
  `Do()` need empty Accept).
- Lookups (project shortName → id, user login → id) are one-call helpers
  used by `create-issue` / `set-assignee` / `create-token` — keeps the
  param surface ergonomic at the cost of one extra GET.

## Known gaps

- No transitions/workflow API beyond `apply-command "State X"`.
- No time tracking / worklog actions yet.
- No tag CRUD (only via `apply-command "tag X"`).
- No issue update beyond `apply-command` + `add-comment`. Field changes
  via PUT /api/issues/{id} not exposed; use commands instead.
- Team-membership writes blocked by YouTrack 2024+ (UI only).
