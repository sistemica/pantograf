# connectors/email

Single connector covering both IMAP (read / list / search / draft) and SMTP
(send) for any email account. Vendor knowledge lives as data — Presets fill
`imap_*` and `smtp_*` for Fastmail / GMX / Gmail / Custom in one wizard step.

## Actions

| Name | Description |
|---|---|
| `read-emails` | Most recent N from a folder. Multipart-aware: separates `body` (text/plain), `html_body`, `attachments` metadata. Returns `message_id` / `in_reply_to`. |
| `get-email` | One message by UID, with body + attachment list. Returns `message_id` / `in_reply_to`. |
| `download-attachment` | One attachment part by UID + part_id, decoded, written to disk. |
| `list-folders` | All IMAP mailboxes (full label hierarchy). |
| `search-emails` | IMAP SEARCH over a folder. `field` selects what to match: `subject` (default), `from`, `to`, `body`, `text` (headers+body); configurable limit. |
| `save-draft` | Compose + APPEND to Drafts. cc/bcc/attachments + threading (`in_reply_to` / `references`). |
| `send-email` | Send via SMTP. cc/bcc/from-override/attachments + threading (`in_reply_to` / `references`). |

### Threading (in-thread replies)

`send-email` and `save-draft` accept two optional fields:

- `in_reply_to` — the `Message-ID` you are replying to (from `get-email` /
  `read-emails` output). Bare ids are normalised to `<id@host>`.
- `references` — the full References chain. When omitted it defaults to the
  `in_reply_to` value, which is correct for a direct reply.

These set the `In-Reply-To` and `References` headers so the recipient's client
threads the message. `message_id` is surfaced on every read action precisely so
it can be fed back here.

## Triggers

None yet. IMAP IDLE / new-message stream is planned.

## Credential

| Field | Required | Default | Notes |
|---|---|---|---|
| `email` | yes | — | full address; doubles as IMAP/SMTP username |
| `password` | yes | — | app-password preferred. `FieldSecret`, encrypted at rest. |
| `imap_host` | yes | preset-filled | e.g. `imap.fastmail.com` |
| `imap_port` | no | 993 | |
| `imap_security` | no | `tls` | `tls` / `starttls` / `none` |
| `smtp_host` | yes | preset-filled | e.g. `smtp.fastmail.com` |
| `smtp_port` | no | 465 | |
| `smtp_security` | no | `tls` | |

## Presets

| Name | IMAP host:port | SMTP host:port |
|---|---|---|
| Fastmail | imap.fastmail.com:993 (TLS) | smtp.fastmail.com:465 (TLS) |
| GMX | imap.gmx.net:993 (TLS) | mail.gmx.net:587 (STARTTLS) |
| Gmail | imap.gmail.com:993 (TLS) | smtp.gmail.com:465 (TLS) |
| Protonmail Bridge | 127.0.0.1:1143 (STARTTLS, self-signed) | 127.0.0.1:1025 (STARTTLS, self-signed) |
| Custom | manual | manual |

The wizard also fills missing host fields by simple substitution (e.g.
`imap.<domain>` ↔ `smtp.<domain>`) when one side is given but not the other.

## Validate (wizard)

The wizard probes both protocols at the end:

1. Real IMAP login (`Dial` + `LOGIN`)
2. SMTP `Probe` (dial + AUTH + close, no message sent)

This catches divergent permissions early — e.g. Gmail app-passwords scoped
to SMTP but not IMAP.

## Multipart parsing

`read-emails` / `get-email` decode the full RFC 822 message and surface:

- `body` — first text/plain part
- `html_body` — first text/html alternative
- `attachments[]` — entries with `filename`, `content_type`, `size`, `part_id`

Decoding handles `quoted-printable`, `base64` (whitespace-tolerant), nested
multipart structures, and RFC 2047 encoded filenames.

## Usage

```bash
pgf connect email sistemica
# wizard: pick "Fastmail" preset → email + app password → IMAP+SMTP probe

pgf run email/sistemica list-folders
pgf run email/sistemica read-emails -p folder=INBOX -p limit=5
pgf run email/sistemica search-emails -p query=invoice                 # subject (default)
pgf run email/sistemica search-emails -p query=alice@x.com -p field=from
pgf run email/sistemica search-emails -p query=reminder -p field=body
pgf run email/sistemica get-email -p uid=12345

# Reply in-thread: take message_id from get-email, feed it to in_reply_to
pgf run email/sistemica send-email -p to=alice@x.com -p subject="Re: hi" \
                                  -p body=thanks -p in_reply_to='<abc@host>'

pgf run email/sistemica download-attachment -p uid=12345 -p part_id=2 \
                                           -p out=/tmp/file.pdf

pgf run email/sistemica send-email -p to=foo@example.com \
                                  -p subject=hi -p body=hello \
                                  -p attachments=/tmp/foo.pdf

pgf run email/sistemica save-draft -p to=foo@example.com \
                                  -p subject=draft -p body=...
```

For multiple recipients, either repeat `-p` or comma-separate:

```bash
-p to=a@x.com -p to=b@y.com
-p to=a@x.com,b@y.com
```

## What to expect on the wire

- IMAP: full-message fetch (`BODY[]`) when bodies are requested, so the parser
  has the boundary param from the top-level `Content-Type`. Cheaper partial
  fetches are not used; messages are typically tens of KB so this is fine.
- SMTP: per-call short-lived dial. No connection pooling.

## Known gaps

- No `delete_message` / `move_message` actions yet
- No IDLE / new-message trigger
- OAuth2 path (Gmail with strict 2FA) not implemented
- No auto-categorization / filter-rule actions
- IMAP SEARCH is server-side substring, not semantic; no body-text ranking
