# connectors/matrix

Matrix (Client-Server API) connector. Bearer-token auth with a login
fallback that exchanges username + password for a token at Validate
time and then drops the password from the persisted credential.

One pantograf instance = one Matrix user. Multi-user works the same way
as YouTrack: register multiple instances side-by-side.

## Actions

| Name | Description |
|---|---|
| `whoami` | Identity of the bound token (user_id + device_id). Cheapest validate. |
| `list-rooms` | Joined rooms; optionally enriched with name + canonical alias (1 extra HTTP per room). |
| `get-room` | Room state bundle: name, topic, alias, encryption, joined-members list. |
| `send-message` | Text message via idempotent PUT. Supports plain + HTML formatted body, parameterised msgtype. |
| `get-messages` | Recent timeline events with pagination cursor. |

Trigger (planned): `new-messages` — long-poll `/sync` with the `next_batch` watermark persisted to the state store.

## Credential

| Field | Required | Notes |
|---|---|---|
| `homeserver_url` | yes | Root of the Matrix C-S API, e.g. `https://matrix.nextmind.team`. Trailing slash trimmed. |
| `access_token` | one of two paths | Paste from Element → Settings → Help & About → Advanced → Access Token. **OR** leave empty and use `login_user` + `login_password`. |
| `login_user` | login fallback | Bare local part (e.g. `claudia`, not `@claudia:server`). |
| `login_password` | login fallback | `FieldSecret`. **Discarded** after Validate exchanges it for a token — never persisted. |
| `user_id` | auto | Filled by Validate (`@user:server`). |

### Validate behaviour

1. If `access_token` is given, just probe `/account/whoami`.
2. Otherwise POST `/login` with user + password → mints a token →
   stores in `access_token` → deletes `login_password` from the cred
   values → probes `/whoami` with the new token.

The password-drop happens because `Values` is a map; the wizard saves
whatever's in `Credential.Values` after Validate returns.

## Room references

Both forms are accepted everywhere a room is taken:

- `!FXaXcEgQgpeEoMOWyG:matrix.nextmind.team` — opaque room ID
- `#general:matrix.nextmind.team` — canonical alias (resolved via `/directory/room/{alias}`)

## Usage

```bash
# Login-via-password (token will be minted + sealed; password discarded)
pgf connect --input '{
  "homeserver_url": "https://matrix.nextmind.team",
  "login_user": "claudia",
  "login_password": "..."
}' matrix claudia

# Or with an existing token
pgf connect --input '{
  "homeserver_url": "https://matrix.nextmind.team",
  "access_token": "syt_..."
}' matrix claudia

pgf run matrix/claudia whoami
pgf run matrix/claudia list-rooms
pgf run matrix/claudia send-message \
  -p room="!abc:matrix.nextmind.team" \
  -p body="Hello"

# HTML formatted
pgf run matrix/claudia send-message \
  -p room="#general:matrix.nextmind.team" \
  -p body="See <details>." \
  -p html="See <b>details</b>."

# Pagination
pgf run matrix/claudia get-messages -p room="..." -p limit=50
```

## What to expect on the wire

- `Authorization: Bearer <token>` on every request.
- Sends are **PUT** to `/rooms/{id}/send/m.room.message/{txnId}`. The
  txnId is `pgf-<unix-nanos>-<counter>` per process — retrying with the
  same txnId is a no-op, which is what idempotency means here.
- list-rooms with `enrich=true` does N+1 HTTP calls (the joined-rooms
  endpoint returns IDs only; one `/state/m.room.name/` + one
  `/state/m.room.canonical_alias/` per room follow). For tens of rooms
  this is fine; for hundreds, pass `enrich=false`.
- Most `/state/...` lookups can 404 (e.g. a room with no canonical
  alias). The enrichment helper swallows those errors and returns "".

## Known gaps

- No long-poll `/sync` trigger yet — planned. Will persist `next_batch`
  in the state store, same pattern as Telegram's `messages` and RSS's
  `new-items`.
- No encryption support — E2EE rooms would need Olm/Megolm crypto,
  significant work. For now you can send/read in unencrypted rooms only.
- No media uploads (`mxc://...`). Comes later.
- No room admin (invite, kick, ban, create) — read + send only for v1.
