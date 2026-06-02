# connectors/paperless

Search, read, upload, download, and organise documents in a
[Paperless-ngx](https://docs.paperless-ngx.com) instance. One pgf instance =
one Paperless server + one user's token.

## Resource model

Mirrors the Paperless UI:

```
document  — the OCR'd file + its metadata
  ├── correspondent   (who sent it)
  ├── document_type   (what it is)
  └── tags[]          (free-form labels)
```

Documents reference correspondents / document-types / tags by **integer id**.
Use the `list-*` taxonomy actions to resolve ids, then pass them to
`list-documents` (filter) or `update-document` (set).

## Actions

| Name | What it does |
|---|---|
| `list-documents` | List + filter. `query` runs Paperless's full-text index (OCR content + metadata); `title`/`correspondent`/`document_type`/`tags` narrow the set; `ordering`, `page`, `page_size` page through. |
| `get-document` | One document by id. `metadata=true` also pulls file metadata (original filename, mime, checksums). |
| `download-document` | Stream a document's file to `out`. Archived PDF by default; `original=true` for the source file. |
| `upload-document` | Multipart upload for OCR + consumption. **Async** — returns a task UUID; poll `task-status`. |
| `update-document` | PATCH metadata. Only passed fields change; `tags` **replaces** the full set. |
| `delete-document` | Permanently delete (trash → auto-purge). |
| `list-tags` / `list-correspondents` / `list-document-types` | Taxonomy, name + id, sorted; optional `name` substring filter. |
| `create-tag` / `create-correspondent` / `create-document-type` | Create taxonomy entries. `create-tag` takes optional `color` (hex) + `is_inbox_tag`. |
| `task-status` | Look up a consume task by UUID — `SUCCESS`/`FAILURE`/`PENDING` and, on success, `related_document` (the new doc id). |
| `statistics` | Instance-wide counts (total documents, characters, file-type breakdown). |

## Credential

| Field | Required | Notes |
|---|---|---|
| `url` | yes | Root, e.g. `https://paperless.example.org` — no trailing slash, no `/api`. |
| `token` | one of | API token (Settings → My Profile → API Token). Sent as `Authorization: Token <token>`. |
| `username` + `password` | one of | If no token, exchanged for one at connect via `POST /api/token/`. The resolved token is written back into the (encrypted) credential, so later sessions skip the exchange. |

`Validate` resolves a token and probes `GET /api/documents/?page_size=1`,
reporting the document count.

## Upload is asynchronous

`upload-document` hands the file to Paperless's consumer and returns
immediately with a task UUID. OCR + indexing happen in the background. To
learn the resulting document id, poll:

```bash
TASK=$(pgf run paperless/home upload-document -p file=/tmp/scan.pdf -p title=Invoice \
        | jq -r .task_id)
pgf run paperless/home task-status -p task_id=$TASK     # → status + related_document
```

A re-upload of identical bytes is **rejected as a duplicate** (task status
`FAILURE` with a "duplicate of …" message) — Paperless dedupes on content
hash, not filename.

> **Tags on upload:** not settable in the multipart post. Upload first, then
> `update-document -p tags=<ids>` once consumption succeeds.

## Usage

```bash
# Setup (token, or username+password to derive one)
pgf connect paperless home

# Search + read
pgf run paperless/home list-documents -p query="invoice 2026" -p page_size=10
pgf run paperless/home get-document -p id=128 -p metadata=true
pgf run paperless/home download-document -p id=128 -p out=/tmp/128.pdf

# Resolve taxonomy ids, then filter / organise
pgf run paperless/home list-tags
pgf run paperless/home list-documents -p correspondent=7 -p tags=11,42
pgf run paperless/home update-document -p id=128 -p title="Acme Invoice" -p tags=11,42

# Housekeeping
pgf run paperless/home statistics
pgf run paperless/home delete-document -p id=128
```

## Live verification

Verified end-to-end (June 2026) against two real Paperless-ngx instances:

- **Read** on both: `statistics`, `list-documents` (full-text query + filters),
  `get-document` (+metadata), all taxonomy lists.
- **Write** cycle on one instance: `upload-document` → `task-status` (polled to
  `SUCCESS`) → `update-document` (title + tag) → `download-document` (archived
  PDF) → `delete-document`, leaving zero residue.

## Implementation note

`upload-document` builds its multipart body in-connector (via the transport's
`Do` escape hatch) rather than the streaming `PostMultipart` helper, because
it must set a real per-part `Content-Type`: Paperless's parser rejects an
`application/octet-stream` file part with *"no file was submitted"*. The type
is sniffed with `http.DetectContentType`.

## Known gaps

- **No `delete-tag` / `delete-correspondent` / `delete-document-type`** — only
  create + list. Manage deletions in the UI for now.
- **No bulk operations** (`/api/documents/bulk_edit/`) — update one doc at a time.
- **No custom-fields, saved-views, workflows, or share-links.**
- **No notes / comments** on documents.
- **Tags can't be set on upload** (multipart limitation) — set via
  `update-document` after consumption.
- **No new-document trigger.** Polling `list-documents` by `-added` is the
  workaround until an IDLE/poll trigger is added.
