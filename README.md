# pantograf

> A pantograph is the hinged arm on top of an electric train that collects
> power from overhead lines. It's the connector that bridges a fixed
> infrastructure to moving consumers.

**pantograf** is a Go connector framework. One reusable library defines the
contract; many consumers — a CLI, an HTTP server, a webhook receiver, your
own program — plug into the same set of connectors and get the same
actions, triggers, credential wizard, encrypted-at-rest credential store,
and per-instance state store for free.

The design borrows from the convergent patterns of Apache Camel, Conduit,
n8n, Activepieces, and Airbyte's CDK, translated into idiomatic Go:
struct + slice + interface, no reflect, explicit registry, no magic.

## The idea in one diagram

```
    ┌──────────────────────────────────────────────────────────────────┐
    │ Consumers (any program importing the library)                    │
    │                                                                  │
    │   pgf CLI    │   HTTP server    │   MCP server    │   your code  │
    └──────────────┼──────────────────┼─────────────────┼──────────────┘
                   │                  │                 │
                   └──────────────────┴─────────────────┘
                                      │
    ┌─────────────────────────────────┴────────────────────────────────┐
    │ Runtime                                                          │
    │   • Registry              connector lookup by name               │
    │   • Credential store      yamlstore   (encrypted secrets)        │
    │   • State store           fsstore     (per-instance KV)          │
    │   • Wizard                schema-driven, validates live          │
    │   • pgf serve             webhook multiplexer                    │
    └──────────────────────────────────┬───────────────────────────────┘
                                       │
    ┌──────────────────────────────────┴───────────────────────────────┐
    │ Connectors (vendor abstractions)                                 │
    │                                                                  │
    │   email   │   telegram   │   matrix   │   llm   │ ...future...   │
    └───────┬───────────┬───────────┬───────────┬──────────────────────┘
            │           │           │           │
    ┌───────┴───────────┴───────────┴───────────┴──────────────────────┐
    │ Transports (wire-protocol clients, vendor-neutral)               │
    │                                                                  │
    │   imap        │        smtp        │        http                 │
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
| [email](connectors/email/README.md) | read-emails / get-email / list-folders / search-emails (subject/from/to/body/text) / save-draft / send-email / download-attachment | — | IMAP + SMTP, vendor presets (Fastmail / GMX / Gmail / Protonmail Bridge / Custom), multipart parsing, in-thread replies (`in_reply_to`), encrypted-at-rest creds |
| [telegram](connectors/telegram/README.md) | get-me / get-updates / send-message / send-photo / send-document / set-webhook / delete-webhook / get-webhook-info | messages (polling, persistent offset) | Bot API |
| [matrix](connectors/matrix/README.md) | whoami / list-rooms / get-room / send-message / set-typing / get-messages / create-room / create-space / invite-user / add-room-to-space | messages (polling /sync, persistent next_batch) | Matrix C-S API. Bearer auth, login fallback that exchanges password→token at Validate and discards the password. |
| [synapse](connectors/synapse/README.md) | server-version / users (list / get / create / set-password / deactivate) / rooms (list / delete / purge-history) | — | Synapse **admin** API — server-wide user/room management. Requires an admin-flagged token. Distinct from `matrix` (the standard C-S API any homeserver implements). |
| [llm](connectors/llm/README.md) | list-models / chat-completion / embed | — | OpenAI-compatible. Reasoning channel + tool calls pass-through. 10-min HTTP timeout for long thinking calls. |
| [whisper](connectors/whisper/README.md) | list-models / transcribe / translate | — | Speech-to-text against standalone Whisper servers (faster-whisper-server, whisper.cpp, vLLM-Whisper). OpenAI-shape `/audio/transcriptions`. |
| [web](connectors/web/README.md) | fetch / extract-markdown / extract-html / extract-links / extract-media / screenshot / search | — | Fetch + extract. Default HTTP, optional CDP browser mode for JS-heavy pages. Readability→markdown, CSS-selector extraction, DuckDuckGo search, per-instance cache. |
| [jina](connectors/jina/README.md) | read / search / ground | — | Jina AI hosted Reader + Search + Grounding. URL→markdown resilient to bot-blocking; web search; statement factuality scoring. Fallback when `web` is blocked. |
| [webhook](connectors/webhook/README.md) | — | incoming (any method, parsed body, optional API-key + HMAC auth, configurable response from string or file) | Generic HTTP receiver. Glue for any upstream that POSTs. |
| [rss](connectors/rss/README.md) | fetch / list-new / mark-seen / info / reset | new-items (polling, persistent watermark) | Stateful RSS / Atom / JSON Feed reader. Skips backlog by default. |
| [file](connectors/file/README.md) | list / stat / get / put / delete / search / presign | — | Pluggable backend: local filesystem **or** S3-compatible (AWS / MinIO / R2 / B2). Regex content search (local), time-limited presigned URLs (S3). |
| [youtrack](connectors/youtrack/README.md) | me / users / projects / issues (CRUD + comments + attachments + state) / articles (CRUD + tree) / `apply-command` (universal field setter) / create-token | — | JetBrains issue tracker. Multi-user via one instance per user-token. Hub permanent tokens. |
| [lexoffice](connectors/lexoffice/README.md) | get-profile / list-contacts / get-contact / list-vouchers / get-voucher / download-voucher-pdf | — | German accounting (Lexware Office). Bearer auth, type-aware voucher dispatch, exponential 429 backoff. |
| [paperless](connectors/paperless/README.md) | list-documents (full-text + filters) / get-document / download-document / upload-document / update-document / delete-document / list+create tags / correspondents / document-types / task-status / statistics | — | Paperless-ngx DMS. Token auth or username+password exchange. Async upload→consume with task polling; multipart sets a real per-part content-type. |
| [bunny](connectors/bunny/README.md) | zones (list / get / create / delete / check / export-BIND) / records (add / update / delete) / pull-zones (list / get / create / update / delete) / hostnames (add / remove) / load-free-certificate / set-force-ssl | — | Bunny.net DNS + CDN Pull Zones. String→numeric DNS-type mapping, Let's Encrypt via HTTP-01, host-header forwarding for vhost origins. |
| [infisical](connectors/infisical/README.md) | projects (CRUD) / environments (CRUD) / folders (list / create / delete) / secrets (CRUD) / org + project membership / identities | — | Infisical secrets management via Universal Auth. Self-hosted-friendly. Reads plaintext via the `/raw` endpoint (workspace E2EE disabled). |

### Transports

| Name | Purpose |
|---|---|
| [transport/imap](transport/imap/README.md) | Dial + LOGIN, returns raw `*imapclient.Client` |
| [transport/smtp](transport/smtp/README.md) | Send + Probe (auth-only check) |
| [transport/http](transport/http/README.md) | JSON / form / multipart helpers with explicit URL composition; Backoff + RetryOn helpers |

### Runtime support

| Package | Purpose |
|---|---|
| `connector` | the contract: Connector, Action, Trigger, Session, Schema, Values, Registry |
| `storage` / `storage/yamlstore` | credential persistence (one YAML per instance) |
| `state` / `state/fsstore` | per-instance KV state for triggers (offsets, cursors) |
| `secrets` | NaCl-secretbox at-rest encryption, master key on disk |
| `cmd/pgf` | the reference CLI consumer |

## Quick start

```bash
go install github.com/sistemica/pantograf/cmd/pgf@latest

# Browse what's available
pgf connectors
pgf actions email
pgf triggers telegram

# Add a credential (interactive wizard) — schema-driven prompts
pgf connect email inbox            # prompts for IMAP/SMTP host + creds

# Or non-interactively (good for CI / containers)
pgf connect --input '{
  "email": "you@example.com",
  "password": "...",
  "imap_host": "imap.example.com", "imap_port": 993, "imap_security": "tls",
  "smtp_host": "smtp.example.com", "smtp_port": 465, "smtp_security": "tls"
}' email inbox

# Use it
pgf run email/inbox list-folders
pgf run email/inbox send-email -p to=foo@bar.com -p subject=hi -p body=hello

# Stream a polling trigger to stdout (NDJSON) — pipe wherever
pgf run telegram/personal get-me
pgf watch telegram/personal messages > /tmp/events.ndjson

# Host all webhook triggers on a single port
pgf serve --addr :8080 --public-url https://your-host.example.com
```

Storage layout (XDG-compliant):

```
~/.config/pgf/master.key                          # 32-byte NaCl key, mode 0600
~/.config/pgf/instances/<type>/<name>.yaml        # one per instance, secret fields sealed
~/.local/state/pgf/state/<type>/<name>/...        # per-instance trigger state
```

Override paths via `PGF_KEY_DIR`, `PGF_STORE_DIR`, `PGF_STATE_DIR`.

## Examples

End-to-end workflows composed from the bundled connectors live in
[`examples/`](examples/):

| Example | What it does |
|---|---|
| [triage-emails](examples/triage-emails/) | bash + jq: read inbox → LLM classifies "bug report?" → files matching ones as YouTrack issues |

## Design decisions worth knowing

| Decision | Why |
|---|---|
| **No reflect.** Schemas are hand-written `[]FieldSpec` slices. | Predictable, debuggable, no struct-tag magic. Codegen could generate these later if boilerplate grows. |
| **Connector contract is small (~5 methods).** Triggers split into two sub-interfaces by strategy. | Adding a new connector is a directory + a `Register` call. No build-time scaffolding. |
| **Credentials are first-class with `Validate(ctx, cred)`.** | Wizard probes live service before saving — catches bad creds at setup, not at first action. |
| **Vendor knowledge lives as data (`Presets`).** | "Fastmail" isn't a connector — it's a 6-line `Preset` entry inside the email connector's credential spec. |
| **State store is per-instance, separate from credentials.** | Triggers can persist offsets/cursors; restart resumes cleanly. Telegram's `messages` trigger demonstrates this. |
| **Secrets encrypted at rest.** | NaCl secretbox with key on disk (mode 0600). Sealed values marked at runtime by the `sealed:` prefix; legacy plaintext still works during migration. |
| **`pgf serve` is a webhook multiplexer.** | One process, mounts every webhook trigger across every instance under `/<type>/<name>/<trigger>`. NDJSON to stdout. |
| **HTTP URL composition uses explicit slash handling, not `url.ResolveReference`.** | A non-trivial BaseURL path (e.g. Telegram's `/bot<token>`) gets clobbered when the relative path starts with `/`. We hit that; documented in `transport/http/README.md`. |
| **One CLI consumer, but the library is multi-consumer.** | `cmd/pgf` is the reference. An HTTP gateway, an MCP server, or your own Go program can all import the connector library and reuse the same instances/state/encryption. |

## Adding a connector

A new connector is a directory with three files plus a one-line registration:

```
connectors/yourthing/
├── credentials.go          # CredentialSpec — fields, presets, Validate probe
├── connector.go            # Descriptor + Open + session struct
└── actions.go              # one type per action implementing connector.Action
```

Then in `cmd/pgf/main.go`:

```go
import yourthingpkg "github.com/sistemica/pantograf/connectors/yourthing"
// ...
yourthingpkg.Register(connector.Default)
```

That's it. The new connector immediately appears in `pgf connectors`,
its action schemas are introspectable, the wizard works, credentials
are encrypted, instances live alongside the others. The per-connector
READMEs in `connectors/*/` document the contract more concretely.

## Layout

```
pantograf/
├── connector/                   # the contract (no reflect)
├── secrets/                     # at-rest encryption (NaCl secretbox)
├── state/  state/fsstore/       # per-instance KV
├── storage/  storage/yamlstore/ # credential persistence
├── transport/
│   ├── http/                    # generic HTTP client + retry helpers
│   ├── imap/                    # IMAP wrapper
│   └── smtp/                    # SMTP wrapper
├── connectors/
│   ├── email/                   # IMAP + SMTP
│   ├── telegram/                # Bot API
│   ├── matrix/                  # Matrix C-S API
│   ├── synapse/                 # Synapse admin API
│   ├── llm/                     # OpenAI-compatible
│   ├── whisper/                 # speech-to-text
│   ├── web/                     # fetch + extract (HTTP / CDP)
│   ├── jina/                    # Jina AI Reader / Search / Grounding
│   ├── webhook/                 # generic HTTP-in
│   ├── rss/                     # feed reader
│   ├── file/                    # local FS / S3-compatible
│   ├── youtrack/                # JetBrains
│   ├── lexoffice/               # Lexware Office
│   ├── paperless/               # Paperless-ngx DMS
│   ├── bunny/                   # Bunny.net DNS + CDN
│   └── infisical/               # secrets management
├── examples/                    # runnable workflows
└── cmd/pgf/                     # the reference CLI
```

## Roadmap

Confirmed-working today (real E2E tested in development):

- email: Fastmail / Protonmail Bridge — wizard, list/read/draft/send, multi-field search (subject/from/to/body/text), in-thread replies, attachments, multipart parsing, byte-perfect attachment round-trip
- telegram: full Bot API surface + persistent-offset `messages` trigger (verified resume across restart)
- matrix: send/read + long-poll `/sync` trigger with persistent `next_batch`; create-room/space, invite, typing indicator
- synapse: admin user/room management against a live Synapse homeserver
- llm: reasoning + tool calls round-trip against an OpenAI-compatible endpoint
- whisper: transcribe / translate against a faster-whisper-server endpoint
- web / jina: URL→markdown extraction and web search (HTTP, CDP browser, and Jina hosted fallback)
- file: local FS + S3-compatible (MinIO/R2/B2) list/get/put/delete/search
- webhook: GET / POST / PUT, HMAC-SHA256 (LemonSqueezy + GitHub-prefix), API-key auth, response-file read at request time
- youtrack: ~30 actions across users / projects / issues / articles / comments / attachments
- lexoffice: read path against the live Lexware API; byte-perfect PDF download
- paperless: read + full write cycle (upload→consume→update→download→delete) verified against two live Paperless-ngx instances
- bunny: DNS zones/records + CDN Pull Zones, custom hostnames, Let's Encrypt
- infisical: secrets / projects / environments / folders + org & project membership via Universal Auth

Open follow-ups:

- IMAP IDLE trigger for email
- `delete-message` / `move-message` email actions
- Stripe / Slack timestamp-prefixed signatures (`t=...,v1=...`)
- OAuth2 wizard path (Gmail, Google Calendar)
- More connectors as concrete use cases appear

## License

Apache 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE). Copyright
2026 sistemica GmbH.
