---
name: pgf-telegram
description: Send messages, photos, and documents via a Telegram bot using the pantograf `pgf` CLI; also stream incoming updates. Use when the user asks to send a Telegram message, post a photo/document to a chat, configure a Telegram webhook, or watch for new bot messages. Requires a configured `telegram` instance — run `pgf instances` to discover names.
---

# pgf-telegram

Wraps the `telegram` connector — Telegram Bot API. One pantograf
instance = one bot token. Multi-bot setups just register multiple
instances.

## When to use

- "Send me a Telegram message saying X"
- "Send a photo / document to Telegram"
- "Set up a Telegram webhook"
- "Watch for new Telegram messages" (continuous streaming)
- "What recent messages did the bot get?"

## Prerequisites

`pgf instances` should list one or more `telegram/<name>` instances.
Bot token comes from @BotFather.

## Actions

```
pgf actions telegram
```

| Action | Required | Notes |
|---|---|---|
| `get-me` | — | Bot identity. Cheapest health-check. |
| `send-message` | `text` | Optional `chat_id` (defaults to credential's `default_chat_id`), `parse_mode` (HTML/Markdown/MarkdownV2), `disable_notification`, `reply_to_message_id`. |
| `send-photo` | `photo` | Local file path or `https://...` URL. Optional `caption`. |
| `send-document` | `document` | Same dual-mode as send-photo. |
| `get-updates` | — | One-shot fetch with `offset`, `limit`, `timeout=0` (short-poll). For continuous, use the **trigger**. |
| `set-webhook` / `delete-webhook` / `get-webhook-info` | varies | Webhook lifecycle on the bot side. |

## Triggers

```
pgf triggers telegram
```

| Trigger | Strategy | Notes |
|---|---|---|
| `messages` | polling | Long-polls `getUpdates`. **Persists offset** across restart — survives outages up to Telegram's 24h retention. |

## Common patterns

### Quick one-line send

```bash
pgf run telegram/<instance> send-message -p text="hello from pgf"
```

(Assumes `default_chat_id` is set in the credential. Otherwise add `-p chat_id=<id>`.)

### Send an image from disk

```bash
pgf run telegram/<instance> send-photo \
  -p photo=/path/to/img.jpg \
  -p caption="here it is"
```

### Stream incoming messages to a file/pipe

```bash
# First run: drain backlog
pgf watch telegram/<instance> messages -p start_offset=-1 > /tmp/tg.log &

# Subsequent runs: resume from persisted offset (default behavior)
pgf watch telegram/<instance> messages > /tmp/tg.log
```

Each line is one NDJSON `Event`:

```json
{"id":"123","type":"message","payload":{"message_id":42,"chat":{...},"from":{...},"text":"hi"},"timestamp":"..."}
```

### One-shot poll (e.g. from cron)

```bash
pgf run telegram/<instance> get-updates -p timeout=0 -p limit=20 \
  | jq '.[] | {id: .update_id, text: .message.text}'
```

## Gotchas

- **Only ONE consumer per bot token at a time.** Either polling OR a
  webhook, never both. `set-webhook` returns an error if polling is
  active elsewhere.
- **`messages` trigger persists `trigger:messages:offset`** in
  `~/.local/state/pgf/state/telegram/<name>/`. Restart resumes; want a
  fresh start? Set `-p start_offset=-1` once.
- **`send-photo` URL mode**: Telegram fetches the URL server-side; the
  URL must be reachable from Telegram's network, and must serve a
  proper image MIME type (a `image/svg+xml` URL gets rejected as
  "wrong type of the web page content").
- **Telegram's free tier is generous** but has bursting limits
  (~30 msg/sec per bot to different chats). Don't paste-flood.
