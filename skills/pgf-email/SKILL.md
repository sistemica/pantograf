---
name: pgf-email
description: Read, search, send, draft, and attach files via email (IMAP+SMTP) using the pantograf `pgf` CLI. Use when the user asks to check their inbox, search past emails, send mail, save a draft, download attachments, or list mail folders. Requires a configured `email` instance — run `pgf instances` to discover names.
---

# pgf-email

Wraps the `email` connector from pantograf. Works against any IMAP+SMTP
provider (Fastmail, GMX, Gmail with app password, Protonmail Bridge,
self-hosted, …).

## When to use

- "Read my last N emails" / "what's in my inbox"
- "Search emails for X"
- "Send an email to X"
- "Save a draft of X"
- "Download attachment from email Y"
- "List mail folders"

## When NOT to use

- Reading **one specific cloud provider** with a richer API (Gmail
  labels, Outlook categories) — IMAP exposes the lowest common
  denominator. Use the provider's REST API instead.
- Bulk operations across thousands of messages — IMAP is slow; consider
  a dedicated search index.

## Prerequisites

Pick the instance name from `pgf instances` (look for `email/...`). If
none, ask the user to run `pgf connect email <name>` first.

## Actions

```
pgf actions email
```

| Action | Required params | Notes |
|---|---|---|
| `read-emails` | — | `folder` (default INBOX), `limit` (default 10), `include_body` (default true). Returns array; multipart-parsed body + attachment metadata. |
| `get-email` | `uid` | Single message by UID, full body + attachments. |
| `list-folders` | — | All IMAP mailboxes. |
| `search-emails` | `query` | Subject substring; `folder`, `limit`. |
| `send-email` | `to`, `subject`, `body` | Optional `cc`, `bcc`, `from`, `html`, `attachments`. |
| `save-draft` | `to`, `subject`, `body` | Saves to Drafts folder. Same param set as send. |
| `download-attachment` | `uid`, `part_id`, `out` | `part_id` comes from `read-emails` → `attachments[].part_id`. |

## Common patterns

### Read recent + show subjects

```bash
pgf run email/<instance> read-emails -p limit=5 -p include_body=false \
  | jq -r '.[] | "\(.uid)\t\(.from)\t\(.subject)"'
```

### Send a plain message

```bash
pgf run email/<instance> send-email \
  -p to=foo@example.com \
  -p subject="hello" \
  -p body="hi there"
```

### Send with attachments (list)

```bash
# Repeated -p
pgf run email/<instance> send-email \
  -p to=foo@example.com -p subject=files -p body=see-attached \
  -p attachments=/tmp/a.pdf -p attachments=/tmp/b.png

# Or comma-separated
pgf run email/<instance> send-email \
  -p to=foo@example.com -p subject=files -p body=see-attached \
  -p attachments=/tmp/a.pdf,/tmp/b.png
```

### Search + drill into one

```bash
UIDS=$(pgf run email/<instance> search-emails -p query="invoice" | jq -r '.[].uid')
for uid in $UIDS; do
  pgf run email/<instance> get-email -p uid=$uid | jq '{from, subject, body}'
done
```

### Download a PDF attachment

```bash
# 1. Find the message + attachment
MSG=$(pgf run email/<instance> read-emails -p limit=20 \
       | jq '.[] | select(.attachments != null) | select(.subject|test("invoice";"i")) | .' | head -1)
UID=$(echo "$MSG" | jq -r .uid)
PART=$(echo "$MSG" | jq -r '.attachments[] | select(.content_type=="application/pdf") | .part_id' | head -1)

# 2. Download
pgf run email/<instance> download-attachment \
  -p uid=$UID -p part_id=$PART -p out=/tmp/invoice.pdf
```

## Gotchas

- **`include_body=false`** is much faster for "just list subjects".
  Use it whenever the bodies aren't needed.
- **UIDs are per-folder.** After `pgf run ... read-emails -p folder=Sent`
  the UIDs are NOT comparable to INBOX UIDs.
- **Attachment downloads need the message's `part_id`**, not a filename.
  Get it from `read-emails` or `get-email` responses.
- **`from` field formatting** can be `"Name <email@host>"` or just
  `"email@host"`. Strip with regex if you need just the address.
