# pantograf

> A pantograph is the hinged arm on top of an electric train that collects
> power from overhead lines. It's the connector that bridges a fixed
> infrastructure to moving consumers.

**pantograf** is a Go connector framework. One reusable library defines the
contract; many consumers — a CLI, an HTTP server, a Telegram bot, your own
program — plug into the same set of connectors and get the same actions,
triggers, credential wizard, encrypted-at-rest credential store, and
per-instance state store for free.

The design is borrowed from the convergent patterns of Apache Camel,
Conduit, n8n, Activepieces, and Airbyte's CDK, then translated into
idiomatic Go: struct + slice + interface, no reflect, explicit registry,
no magic.

## The idea in one diagram

```
    ┌──────────────────────────────────────────────────────────────────┐
    │ Consumers (any program importing the library)                    │
    │                                                                  │
    │   pgf CLI    │    HTTP server   │   MCP server    │   your code  │
    └──────────────┼──────────────────┼─────────────────┼──────────────┘
                   │                  │                 │
                   └──────────────────┴─────────────────┘
                                      │
    ┌─────────────────────────────────┴────────────────────────────────┐
    │ Runtime                                                          │
    │   • Registry              connector lookup by name               │
    │   • Credential store      yamlstore  (encrypted secrets)         │
    │   • State store           fsstore    (per-instance KV)           │
    │   • Wizard                schema-driven, validates live          │
    │   • pgf serve              webhook multiplexer                    │
    └──────────────────────────────────┬───────────────────────────────┘
                                       │
    ┌──────────────────────────────────┴───────────────────────────────┐
    │ Connectors (vendor abstractions)                                 │
    │                                                                  │
    │   email       │   telegram    │   webhook     │   ...future...   │
    └───────┬───────┴───────┬───────┴───────┬───────┴──────────────────┘
            │               │               │
    ┌───────┴───────────────┴───────────────┴──────────────────────────┐
    │ Transports (wire-protocol clients, vendor-neutral)               │
    │                                                                  │
    │   imap        │   smtp        │   http        │                  │
    └──────────────────────────────────────────────────────────────────┘
```

## The contract

A connector implements one interface; everything else falls out:

```go
type Connector interface {
    Descriptor() Descriptor                  // name, version, categories
    Credential() CredentialSpec              // schema + presets + Validate
    Actions()  []Action                      // one-shot RPCs
    Triggers() []Trigger                     // event sources

    Open(ctx, cred Credential, opts OpenOptions) (Session, error)
}
```

Triggers split by strategy. `pgf watch` calls `Subscribe` on streaming
triggers; `pgf serve` hosts an HTTP receiver and dispatches to webhook
triggers:

```go
type StreamingTrigger interface {  // Polling / Push
    Trigger
    Subscribe(ctx, sess, params, emit Sink) error
}

type WebhookTrigger interface {    // Webhook
    Trigger
    OnEnable(ctx, sess, params, publicURL string) error
    OnDisable(ctx, sess, params) error
    Handle(ctx, sess, params, req *http.Request, emit Sink) (*WebhookResponse, error)
}
```

## What's built

### Connectors

| Name | Actions | Triggers | Notes |
|---|---|---|---|
| [email](connectors/email/README.md) | read-emails / get-email / list-folders / search-emails / save-draft / send-email / download-attachment | — | IMAP + SMTP, vendor presets (Fastmail/GMX/Gmail/Custom), multipart parsing, encrypted-at-rest creds |
| [telegram](connectors/telegram/README.md) | get-me / get-updates / send-message / send-photo / send-document / set-webhook / delete-webhook / get-webhook-info | messages (polling, persistent offset) | Bot API |
| [lexoffice](connectors/lexoffice/README.md) | get-profile / list-contacts / get-contact / list-vouchers / get-voucher / download-voucher-pdf | — | German accounting (Lexware Office). Bearer auth, type-aware voucher dispatch, exponential 429 backoff |
| [rss](connectors/rss/README.md) | fetch / list-new / mark-seen / info / reset | new-items (polling, persistent watermark) | Stateful RSS/Atom/JSON Feed reader. Skips backlog by default; `include_backlog=true` for first-run flush. |
| [webhook](connectors/webhook/README.md) | — | incoming (any method, parsed body, optional API-key + HMAC auth, configurable response from string or file) | Generic HTTP receiver. Glue for any upstream that POSTs |

### Transports

| Name | Purpose |
|---|---|
| [transport/imap](transport/imap/README.md) | Dial + LOGIN, returns raw `*imapclient.Client` |
| [transport/smtp](transport/smtp/README.md) | Send + Probe (auth-only check) |
| [transport/http](transport/http/README.md) | JSON / form / multipart helpers, with explicit URL composition |

### Runtime support

| Package | Purpose |
|---|---|
| `connector` | the contract: Connector, Action, Trigger, Session, Schema, Values, Registry |
| `storage`, `storage/yamlstore` | credential persistence (one YAML per instance) |
| `state`, `state/fsstore` | per-instance KV state for triggers (offsets, cursors) |
| `secrets` | NaCl-secretbox at-rest encryption, master key on disk |
| `cmd/pgf` | the reference CLI consumer |

## Quick start

```bash
go build -o ~/.local/bin/pgf ./cmd/pgf

# 1. Connect Fastmail (wizard runs IMAP+SMTP probe; password sealed at rest)
pgf connect email sistemica
pgf run email/sistemica list-folders
pgf run email/sistemica send-email -p to=foo@bar.com -p subject=hi -p body=hello

# 2. Connect a Telegram bot, stream incoming messages
pgf connect telegram personal
pgf watch telegram/personal messages   # blocks; emits NDJSON to stdout

# 3. Generic webhook receiver — Stripe, GitHub, IoT, anything
pgf connect webhook github-repo        # wizard sets HMAC config
pgf serve --addr :8080 --public-url https://my.host
# event NDJSON streams as inbound POSTs arrive
```

## Design decisions worth knowing

| Decision | Why |
|---|---|
| **No reflect.** Schemas are hand-written `[]FieldSpec` slices. | Predictable, debuggable, no struct-tag magic. Codegen could generate these later if needed; for now hand-written is fine. |
| **Connector contract is small (~5 methods).** Triggers split into two sub-interfaces by strategy. | Adding a new connector is a directory + a `Register` call. No build-time scaffolding. |
| **Credentials are first-class with `Validate(ctx, cred)`.** | Wizard probes live service before saving — catches bad creds at setup, not at first action. |
| **Vendor knowledge lives as data (`Presets`).** | "Fastmail" isn't a connector — it's a 6-line `Preset` entry inside the email connector's credential spec. |
| **State store is per-instance, separate from credentials.** | Triggers can persist offsets/cursors; restart resumes cleanly. Telegram's `messages` trigger demonstrates this. |
| **Secrets encrypted at rest.** | NaCl secretbox with key on disk (mode 0600). Marked at runtime by the `sealed:` prefix; legacy plaintext still works during migration. |
| **`pgf serve` is a webhook multiplexer.** | One process, mounts every webhook trigger across every instance under `/<type>/<name>/<trigger>`. NDJSON to stdout. |
| **HTTP URL composition uses explicit slash handling, not `url.ResolveReference`.** | A non-trivial BaseURL path (e.g. Telegram's `/bot<token>`) gets clobbered when the relative path starts with `/`. We hit that bug; documented in `transport/http/README.md`. |

## Roadmap

Confirmed-working today (real E2E tested in development):

- Email: Fastmail (Sistemica account) — wizard, list/read/search/draft/send/attachments, byte-perfect attachment round-trip
- Telegram: getMe, send-message, send-photo, send-document
- Telegram messages trigger: long-poll + persistent offset (verified resume across restart)
- Generic webhook: GET / POST JSON / POST form / PUT, HMAC-SHA256 (LemonSqueezy + GitHub-prefix), API-key auth, response_file read at request time

Open follow-ups:

- IMAP IDLE trigger for email
- `delete_message` / `move_message` email actions
- Stripe / Slack timestamp-prefixed signatures (`t=...,v1=...`)
- Cross-instance credential sharing (avoid re-typing the same password for two instances)
- OAuth2 wizard (Gmail, Calendar)
- More connectors (HubSpot, GitHub, IBKR, ...)

## Layout

```
pantograf/
├── connector/                 # the contract (no reflect)
├── secrets/                   # at-rest encryption
├── state/  state/fsstore/     # per-instance KV
├── storage/  storage/yamlstore/  # credential persistence
├── transport/
│   ├── http/                  # generic HTTP client
│   ├── imap/                  # IMAP wrapper
│   └── smtp/                  # SMTP wrapper
├── connectors/
│   ├── email/                 # IMAP + SMTP
│   ├── telegram/              # Bot API
│   └── webhook/               # generic HTTP-in
└── cmd/pgf/                   # the reference CLI
```

## License

— (TBD)
