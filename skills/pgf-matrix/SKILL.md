---
name: pgf-matrix
description: Send and read messages on a Matrix homeserver, list rooms, get room info, and stream new events via the pantograf `pgf` CLI. Use when the user asks to send a Matrix message, list rooms they're in, watch a room for new events, or post into a Matrix channel. Requires a configured `matrix` instance.
---

# pgf-matrix

Wraps the `matrix` connector — Matrix Client-Server API. One pantograf
instance = one user-token (no impersonation in Matrix; switch users by
switching instance).

## When to use

- "Send a Matrix message to room X"
- "What rooms am I in?"
- "Watch room X for new messages"
- "Read the last N messages in room X"
- "Post a notification to my Matrix channel"

## Prerequisites

`pgf instances` lists `matrix/<name>` entries. Setup requires either
an access token (pasted from Element → Settings → Help & About →
Advanced) or username+password (exchanged for a token at Validate;
password discarded).

## Actions

| Action | Required | Notes |
|---|---|---|
| `whoami` | — | Token's user_id + device_id. |
| `list-rooms` | — | Joined rooms; `enrich=true` (default) adds name + alias (1 extra HTTP/room). |
| `get-room` | `room` | Bundle of state events (name, topic, alias, encryption, members). |
| `send-message` | `room`, `body` | Plain text by default. Optional `html` for formatted body, `msgtype` (m.text / m.notice / m.emote). |
| `get-messages` | `room` | Recent timeline. `limit` (default 20), `dir` (b=newest-first), `from` pagination cursor. |

## Triggers

| Trigger | Strategy | Notes |
|---|---|---|
| `messages` | polling | Long-polls `/sync`. Persists `next_batch` across restart. `filter_types=m.room.message` by default — set empty to also emit member events, typing, etc. |

## Room references

Both forms work everywhere `room` is taken:

- `!opaqueRoomId:server.example.com` — internal room ID
- `#alias:server.example.com` — canonical alias (resolved via directory)

## Common patterns

### Send a message

```bash
pgf run matrix/<instance> send-message \
  -p room="!abc:matrix.example.com" \
  -p body="ready"

# With formatting
pgf run matrix/<instance> send-message \
  -p room="#general:matrix.example.com" \
  -p body="Build failed. See details." \
  -p html="Build <b>failed</b>. See <a href='https://...'>details</a>." \
  -p msgtype=m.notice
```

### List + filter rooms by name

```bash
pgf run matrix/<instance> list-rooms \
  | jq -r '.[] | select(.name|test("alerts";"i")) | "\(.room_id)\t\(.name)"'
```

### Stream new messages to a pipe / FIFO

```bash
mkfifo /tmp/matrix.in
pgf watch matrix/<instance> messages > /tmp/matrix.in &
# Consumer:
jq -c 'select(.payload.content.body|test("@bot "))' < /tmp/matrix.in
```

Each event is one NDJSON line:

```json
{"id":"$event_id","type":"m.room.message","payload":{"room_id":"!...","sender":"@user:...","content":{"body":"text","msgtype":"m.text"},"event_id":"$...","origin_server_ts":...},"timestamp":"..."}
```

### Recent history

```bash
pgf run matrix/<instance> get-messages -p room="!abc:matrix.example.com" -p limit=30 \
  | jq '.chunk | map(select(.type=="m.room.message")) | map({sender, body: .content.body})'
```

## Gotchas

- **End-to-end encrypted rooms** (E2EE) aren't supported. Bodies of
  encrypted events arrive as ciphertext placeholders. Send/read in
  unencrypted rooms only, or set up Olm/Megolm separately.
- **`list-rooms` with enrich=true** does N+1 HTTPs. For dozens of
  rooms, fine. For hundreds, pass `-p enrich=false` and look names up
  lazily.
- **First-run streaming** mints a watermark and emits nothing for the
  backlog; only new events arrive. To replay, you'd need to walk room
  histories via `get-messages` with pagination.
- **Aliases vs IDs.** Aliases (`#name:server`) are nicer for humans;
  IDs (`!opaque:server`) are stable. Either works as input.
