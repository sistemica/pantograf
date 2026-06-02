# connectors/synapse

Server-side administration of a [Synapse](https://element-hq.github.io/synapse/)
Matrix homeserver via the `/_synapse/admin/v{1,2}/...` API. One pgf
instance = one homeserver + one admin-flagged access token.

This is **distinct from the `matrix` connector**. `matrix` speaks the
standard Client-Server API (`/_matrix/client/v3/...`) that every
homeserver implements — sending messages, listing the rooms *you've
joined*. `synapse` speaks the implementation-specific admin surface:
every user the server knows about, every room on the server, password
resets, deactivations, history purges. Dendrite and Conduit expose
their own admin APIs at different paths, so this connector is
Synapse-only on purpose.

Pair a `synapse/<name>` instance with a `matrix/<name>` instance
pointing at the same homeserver to get both ordinary and admin
operations from one account — the two stay independent, so a non-admin
token can never reach the admin actions.

## Actions

| Name | What it does |
|---|---|
| `server-version` | Synapse + Python versions. Cheapest probe; also used by `Validate`. |
| `list-users` | Every locally-registered user (admin scope). Filter by name; include/exclude guests, deactivated, admins; paginated + orderable. |
| `get-user` | Full user record: displayname, admin flag, deactivated, creation/last-seen timestamps, threepids, device + room counts. |
| `create-user` | Idempotent `PUT` — creates if missing, updates if present. Set `password` for a usable account, `admin=true` to grant homeserver-admin, `email` to attach a threepid. |
| `set-password` | Set a new password. `logout_devices=true` (default) invalidates every session so the user must re-login everywhere. |
| `deactivate-user` | Mark an account deactivated. `erase=true` does a GDPR-style erase (clears displayname + avatar, future federated lookups return empty). |
| `list-rooms` | Every room the server knows about — not just joined rooms (which is what `matrix list-rooms` returns). Filter by name + member count, paginated. |
| `delete-room` | Forcibly remove a room. `block=true` prevents re-creation by non-admins. `purge` runs async — Synapse returns a `delete_id` to poll. |
| `purge-history` | Drop events before a timestamp. `delete_local_events=true` also removes the local copy. Reclaims disk; irreversible. |

## Credential

| Field | Required | Notes |
|---|---|---|
| `homeserver_url` | yes | C-S endpoint root, e.g. `https://chat.example.com`. Same value as the matching `matrix` instance. |
| `access_token` | yes | Token of an **admin-flagged** user. Element → Settings → Help & About → Advanced → Access Token. `FieldSecret`, encrypted at rest. |
| `admin_user_id` | auto | Filled by `Validate` from `whoami`. Leave blank at connect. |

### Validate — three layered probes

`Validate` fails loudly at the first layer that breaks, each with a remedy:

1. **whoami** (`/_matrix/client/v3/account/whoami`) — confirms the token +
   URL are usable, discovers and stores the user id.
2. **server-version** (`/_synapse/admin/v1/server_version`) — confirms the
   homeserver actually *is* Synapse (Dendrite/Conduit 404 here).
3. **admin probe** (`/_synapse/admin/v1/users/{id}/admin`) — confirms the
   user carries the admin flag. A non-admin token is refused with the exact
   SQL to fix it:
   ```sql
   UPDATE users SET admin=1 WHERE name='@you:example.com';
   ```

On success it prints `(synapse <version>, admin <user>)`.

## Usage

```bash
# Setup — paste the admin token with no echo
pgf connect synapse home

# Who/what does the server hold?
pgf run synapse/home server-version
pgf run synapse/home list-users -p limit=50
pgf run synapse/home get-user -p user=@alice:example.com

# Provision an account (idempotent)
pgf run synapse/home create-user \
  -p user=@bot:example.com -p password='…' -p displayname='Service Bot'

# Force a password reset and kick all sessions
pgf run synapse/home set-password -p user=@alice:example.com -p password='…'

# Deactivate + erase (GDPR)
pgf run synapse/home deactivate-user -p user=@spam:example.com -p erase=true

# Rooms (server-wide)
pgf run synapse/home list-rooms -p search=offtopic -p limit=20
pgf run synapse/home delete-room -p room='!abc:example.com' -p block=true

# Reclaim disk: drop history older than a timestamp (ms epoch)
pgf run synapse/home purge-history \
  -p room='!abc:example.com' -p purge_up_to_ts=1700000000000
```

## Known gaps

- **No media / quota admin.** `/_synapse/admin/v1/media`, per-user upload
  limits, and the media-retention endpoints aren't wrapped.
- **No registration-token management** (`/_synapse/admin/v1/registration_tokens`).
- **No room-membership admin** (`make_room_admin`, force-join/leave).
- **`delete-room` is fire-and-poll.** The connector returns Synapse's
  `delete_id`; it does not poll the async status endpoint for you.
- **Synapse-only.** Dendrite/Conduit admin APIs live at different paths and
  are out of scope.
