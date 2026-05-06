# connectors/webhook

Generic HTTP-in primitive. One instance = one upstream channel. The
runtime hosts it via `pgf serve`. Inbound requests turn into structured
events on stdout; the response is configurable per-instance.

This is the glue connector — anything that wants to POST/GET/PUT to you
(Stripe, GitHub, LemonSqueezy, IoT devices, scripts, other connectors)
points at a `webhook` instance and you get parsed events.

## Actions

None — receive-only.

## Triggers

| Name | Strategy | Description |
|---|---|---|
| `incoming` | webhook | Any method. Captures method, path, query, headers, body. Optional API-key + HMAC auth. Configurable response. |

## Auth — both optional, both can be combined

### API-key

| Field | Default | Notes |
|---|---|---|
| `secret_token` | — | Static value, `FieldSecret`. Empty disables this check. |
| `secret_header` | `X-Mw-Secret` | Header name to compare against. |
| `secret_query_param` | `secret` | Query param name to compare against. |

Either header or query param can match — convenient when the upstream
only supports one or the other.

### HMAC signature

| Field | Default | Notes |
|---|---|---|
| `signature_algo` | `none` | `none` / `hmac-sha256` / `hmac-sha256-prefix` / `hmac-sha1` |
| `signature_secret` | — | Shared HMAC key, `FieldSecret`. |
| `signature_header` | `X-Signature` | Header carrying the signature. |
| `signature_prefix` | — | e.g. `sha256=` for GitHub. When set, the prefix MUST be present on the incoming header (rejected otherwise). |

If both API-key and HMAC are configured, **both must pass** (defense in
depth). If neither is configured, anyone who knows the URL can POST —
acceptable inside a private network, not on the open internet.

### Provider compatibility

| Provider | algo | header | prefix |
|---|---|---|---|
| LemonSqueezy | `hmac-sha256` | `X-Signature` | (none) |
| GitHub | `hmac-sha256-prefix` | `X-Hub-Signature-256` | `sha256=` |
| Telegram setWebhook secret | API-key | `X-Telegram-Bot-Api-Secret-Token` | (use API-key mode) |
| Generic API key | API-key | any | — |
| Stripe / Slack | **not yet** — they sign `t=...,v1=...` composites; needs distinct algo entry | | |
| No auth | `none`, no token | — | — |

## Body parsing

| Content-Type | Emitted as |
|---|---|
| `application/json` (or `*+json`) | `payload.body` (JSON-decoded value) |
| `application/x-www-form-urlencoded` | `payload.body` (`map[string][]string`) |
| any other UTF-8 | `payload.body_text` (string) |
| binary / non-UTF-8 | `payload.body_base64` (base64 string) |

## Response config

| Field | Default | Notes |
|---|---|---|
| `response_body` | — | Static string returned verbatim. |
| `response_file` | — | Path read **at request time** (NOT preloaded). Wins over response_body. |
| `response_content_type` | `text/plain; charset=utf-8` | |
| `response_status` | `200` | |

`response_file` reads on every hit, so editing the file changes what the
next request sees — useful for IoT-style "device polls for current
config" patterns.

## Misc

| Field | Default | Notes |
|---|---|---|
| `allowed_methods` | empty (any) | Comma-separated allow-list. Empty = accept any method. |
| `strip_headers` | `Authorization,Cookie,Proxy-Authorization,X-Mw-Secret` | Removed from emitted events to avoid leaking creds via downstream logs. |

## Emitted Event

```json
{
  "id": "ff6b238a078243229b6c856d",
  "type": "request",
  "payload": {
    "method": "POST",
    "path": "/webhook/stripe/incoming",
    "url": "/webhook/stripe/incoming?source=cli",
    "query": {"source": ["cli"]},
    "headers": {"Content-Type": ["application/json"], "User-Agent": ["curl/8.x"]},
    "body": {"event": "order.created", "amount": 4200},
    "remote_addr": "127.0.0.1:54321",
    "received_at": "2026-05-06T07:14:33.123Z"
  },
  "timestamp": "2026-05-06T07:14:33.123Z"
}
```

`id` is a random hex token (no natural source-side dedup key for generic
HTTP). One of `body` / `body_text` / `body_base64` is populated, never
more than one.

## Usage

```bash
# Production: register with a real upstream
pgf connect webhook stripe                                  # wizard sets secret + response
pgf serve --addr :8080 --public-url https://my.host         # mounts every webhook instance
# point Stripe at https://my.host/webhook/stripe/incoming

# Local dev: poke it with curl
pgf serve --no-register --addr :18080
curl -X POST http://localhost:18080/webhook/stripe/incoming \
     -H "Content-Type: application/json" \
     -d '{"event":"order.created"}'
```

## Compose with other connectors

Telegram → webhook flow:

```bash
pgf connect webhook telegram-in
pgf run telegram/personal set_webhook \
       -p url=https://my.host/webhook/telegram-in/incoming \
       -p secret_token=secretvalue
# Configure telegram-in's secret_token to match.
pgf serve --addr :8080 --public-url https://my.host
```

GitHub:

```bash
pgf connect webhook github-repo
# wizard:
#   signature_algo: hmac-sha256-prefix
#   signature_secret: <GitHub webhook secret>
#   signature_header: X-Hub-Signature-256
#   signature_prefix: sha256=
pgf serve --addr :8080 --public-url https://my.host
# add webhook in GitHub repo settings → https://my.host/webhook/github-repo/incoming
```

## What to expect

- The trigger is a `WebhookTrigger` — it implements `OnEnable`, `OnDisable`,
  `Handle`. For this connector OnEnable / OnDisable are no-ops since there's
  no upstream to register with.
- `pgf serve --no-register` skips OnEnable/OnDisable for any mounted trigger,
  useful for local dev with curl.

## Known gaps

- No timestamp-prefixed signatures (Stripe, Slack) — they compute HMAC over
  `<timestamp>.<body>` rather than the body alone.
- No HTTP Basic auth — only token / signature. Add `Authorization: Bearer X`
  via the API-key mode by setting `secret_header=Authorization` and using
  the full literal value (`Bearer X`).
- Per-instance request log / replay store — could land in the per-instance
  state store, not yet implemented.
