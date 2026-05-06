# connectors/rss

Stateful RSS / Atom / JSON Feed reader. One credential = one feed URL.
Each instance has its own watermark in the state store, so `list-new`
and the `new-items` trigger only surface items the consumer hasn't seen.

No auth in v1 — most feeds are public. HTTP basic auth for gated feeds
is a future addition.

## Actions

| Name | Description |
|---|---|
| `fetch` | Fetch and parse the feed. Read-only — does NOT advance the watermark. Useful for one-off reads or backlog browsing. |
| `list-new` | Atomic read + advance: returns items above the watermark, then sets it to the newest. First call (no watermark) returns `[]` unless `include_backlog=true`. |
| `mark-seen` | Set the watermark to a specific id, or to the newest item in the current feed. |
| `info` | Show the persisted watermark and `last_fetched_at` without touching the feed. |
| `reset` | Clear all persisted state. Next `list-new` starts fresh. |

## Triggers

| Name | Strategy | Description |
|---|---|---|
| `new-items` | polling | Periodically fetches the feed and emits each new item as an Event. Persists watermark; restart resumes cleanly. |

## Credential

| Field | Required | Default | Notes |
|---|---|---|---|
| `feed_url` | yes | — | RSS 2.0, RSS 1.0, Atom, or JSON Feed |
| `user_agent` | no | `pantograf/0.1 (+...)` | Some feeds reject the default Go UA |
| `timeout_seconds` | no | 30 | HTTP request timeout |

`Validate` fetches the feed once. On success the wizard prints the feed
title for confirmation.

## State store keys (per instance)

| Key | Value |
|---|---|
| `rss:last_seen_id` | The GUID / link / hash of the most-recently-emitted item |
| `rss:last_fetched_at` | ISO-8601 timestamp of the last successful fetch |

## Item ID resolution

Stable per-item IDs are derived in priority order:

1. `<guid>` (RSS) / `<id>` (Atom) — preferred when present
2. `<link>` — fallback for feeds without GUID
3. `h:<sha256-prefix>` of `title|published` — last-resort hash

This handles the common "feed has GUIDs", "feed only has links", and
"poorly-formatted feed has neither" cases.

## Event payload (trigger)

```json
{
  "id": "https://example.com/post/42",
  "type": "item",
  "payload": {
    "id": "https://example.com/post/42",
    "title": "...",
    "link": "https://example.com/post/42",
    "published": "2026-05-06T10:00:00Z",
    "description": "...",
    "content": "...",
    "author": "Jane",
    "categories": ["tech"]
  },
  "timestamp": "2026-05-06T10:00:00Z"
}
```

`Event.Timestamp` reflects the item's published time; `Event.ID` is the
stable item id, suitable as a downstream dedup key.

## Usage

```bash
# Add a feed (non-interactive)
pgf connect --input '{"feed_url": "https://news.ycombinator.com/rss"}' rss hn
pgf connect --input '{"feed_url": "https://lobste.rs/rss"}' rss lobsters

# One-off read of latest items
pgf run rss/hn fetch -p limit=10

# What's new since last call (advances watermark)
pgf run rss/hn list-new

# Inspect state
pgf run rss/hn info

# Stream new items continuously (5-min poll by default)
pgf watch rss/hn new-items

# First run: emit existing backlog as well, then continue normally
pgf watch rss/hn new-items -p include_backlog=true

# Manual watermark control
pgf run rss/hn mark-seen -p id=<item-id>
pgf run rss/hn reset
```

## What to expect

- The trigger emits items **oldest-first** within a poll, so the consumer
  sees items in the order they were published.
- `Event.ID` matches the item's stable id, suitable for dedup.
- After each successful poll the watermark is persisted; restart resumes.
- On HTTP / parse errors the trigger backs off exponentially (1 s → 5 min,
  capped) without losing the watermark.
- Minimum `poll_interval` is 30 s — politeness floor.

## Known gaps

- No HTTP basic auth for gated feeds.
- No conditional GET (If-Modified-Since / ETag) — every poll fetches the
  full feed body. Not a problem for most feeds; matters at scale.
- No multi-feed merge — one instance per feed by design. Use `pgf serve`
  or a flow tool to fan many `new-items` triggers into one stream.
