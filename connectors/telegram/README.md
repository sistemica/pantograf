# connectors/telegram

Telegram Bot API connector. One credential = one bot. Send messages /
photos / documents, configure webhooks, and stream incoming updates via
long-poll.

## Actions

| Name | Description |
|---|---|
| `get-me` | Bot identity (id, username, first_name). Useful for sanity checks. |
| `get-updates` | One-shot `getUpdates` with offset / limit / timeout. For continuous streaming use the `messages` trigger. |
| `send-message` | Text. parse_mode (HTML / Markdown / MarkdownV2), disable_notification, reply_to_message_id. |
| `send-photo` | Local file path (multipart upload) **or** http(s) URL (Telegram fetches remotely). |
| `send-document` | Same dual-mode as send-photo. Any file type. |
| `set-webhook` | Tell Telegram to POST updates to a URL. Mutually exclusive with the messages trigger. |
| `delete-webhook` | Clear the webhook so the bot can be polled again or pointed elsewhere. |
| `get-webhook-info` | Current webhook URL + pending update count. |

## Triggers

| Name | Strategy | Description |
|---|---|---|
| `messages` | polling | Long-poll Telegram for incoming updates. Persists offset across restart. |

The webhook delivery path is **not** a Telegram-specific trigger. To
receive via webhook, compose the generic `webhook` connector with
`set-webhook` (see below).

## Credential

| Field | Required | Default | Notes |
|---|---|---|---|
| `bot_token` | yes | — | from @BotFather. `FieldSecret`, encrypted at rest. |
| `default_chat_id` | no | — | numeric ID or `@channelname`; lets `send_*` omit `chat_id`. |
| `api_base` | no | `https://api.telegram.org` | for self-hosted Bot API servers. |

`Validate` calls `getMe` and prints the bot username on success.

## Persistent offset

The `messages` trigger writes `trigger:messages:offset` to the per-instance
state store after each batch of updates. On restart the trigger loads it
and resumes — Telegram retains undelivered updates for 24h, so a brief
outage doesn't drop messages.

`start_offset` parameter:

| Value | Effect |
|---|---|
| `0` (default) | resume from persisted offset; on first run starts from 0 |
| `-1` | drain backlog (Telegram pattern); start fresh from the next new update |
| any positive int | use that as the initial offset |

## Polling vs webhook

Telegram only allows one consumer at a time. Either you poll OR a webhook
is set — not both.

| Need | Use |
|---|---|
| Laptop / dev / no public HTTPS | `pgf watch telegram/personal messages` |
| Production / public host | `set-webhook` → host the URL with a `webhook` connector + `pgf serve` |

## Usage

```bash
# One-time setup
pgf connect telegram personal       # wizard prompts for bot_token + default_chat_id
pgf run telegram/personal get-me

# Sending
pgf run telegram/personal send-message -p text="hello"
pgf run telegram/personal send-photo -p photo=/path/to/img.jpg -p caption="hi"
pgf run telegram/personal send-photo -p photo=https://example.com/x.jpg
pgf run telegram/personal send-document -p document=/tmp/report.pdf

# Receiving — long-poll mode
pgf watch telegram/personal messages -p start_offset=-1   # only on first run
pgf watch telegram/personal messages                       # default = resume

# Receiving — webhook mode (composes with the webhook connector)
pgf connect webhook telegram-in
pgf run telegram/personal set-webhook \
       -p url=https://my.host/webhook/telegram-in/incoming \
       -p secret_token=...                                # passed via X-Telegram-Bot-Api-Secret-Token
pgf serve --addr :8080 --public-url https://my.host        # hosts the receiver
pgf run telegram/personal delete-webhook                   # cleanup
```

## What to expect on the wire

- Long-poll uses a separate `httptr.Client` with a longer client-side
  timeout than the server-side long-poll timeout; ctx still drives shutdown.
- All actions go through `transport/http`, which auto-handles JSON marshaling
  and surfaces Telegram's `{ok: false, description, error_code}` errors as
  `telegram <method>: <description> (error_code=N)`.
- `send_*` auto-detects URL vs local path on the media field and switches
  between JSON body and multipart upload accordingly.

## Known gaps

- Stripe / Slack-style timestamped signature schemes not yet supported on
  the receiving side (use the generic `webhook` connector for those — but
  with caveats described in its README).
- No support for inline keyboards / answer-callback flows yet.
