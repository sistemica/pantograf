# connectors/file

One connector, pluggable backend. The credential picks `local` or `s3`
at `connect` time; the action set (`list`, `stat`, `get`, `put`,
`delete`, `search`, `presign`) is identical regardless of where the
bytes live. Same design memo uses for its `history` / `history-pg`
split: shape-oriented actions on top of swappable storage.

Adding a backend is a single file implementing the internal `driver`
interface (List/Stat/Get/Put/Delete) plus a handful of
`ShowWhen`-gated fields on the credential schema. The action layer
doesn't change.

## Actions

| Name | Notes |
|---|---|
| `list` | Entries under a prefix. Recursive on s3; one-level on local unless `recursive=true`. |
| `stat` | One entry's metadata (size, last_modified, content_type, etag). |
| `get` | Download. Use `out=PATH` to stream to disk; omit `out` to return the body inline (1 MiB cap). |
| `put` | Upload from a local file (`src=PATH`) or inline content (`content=...`). |
| `delete` | Idempotent removal. |
| `search` | Regex over file contents. **Local only** — s3 returns `"not supported"`. |
| `presign` | Time-limited GET/PUT/HEAD URL. **S3 only** — local returns `"not supported"`. |

`search` and `presign` are gated by optional driver capabilities
(`Searcher` and `Presigner` interfaces). The runtime returns a clear
`"not supported"` error when the chosen backend doesn't implement them.

## Credential

| Field | When | Notes |
|---|---|---|
| `backend` | always | Enum: `local`, `s3`. Picks the driver and which fields below the wizard prompts for. |
| `root` | local only | Absolute path treated as the backend root. Keys are interpreted relative to it; traversal outside is rejected. `IsPath: true` — subject to `PGF_ALLOWED_PATHS`. |
| `endpoint` | s3 only | Host[:port] or full http(s):// URL. e.g. `s3.amazonaws.com`, `http://localhost:9000`, `https://<account>.r2.cloudflarestorage.com`. |
| `region` | s3 only | AWS region. MinIO and R2 accept `us-east-1` / `auto`. Default `us-east-1`. |
| `bucket` | s3 only | One bucket per instance. Register a second `file/<name>` for a second bucket. |
| `access_key` | s3 only | Access key ID (not sealed — it's not the credential). |
| `secret_key` | s3 only | `FieldSecret`. Sealed by pgf's vault. |
| `use_ssl` | s3 only | Ignored when `endpoint` is a full URL (the scheme wins). |

The `ShowWhen` predicates mean the interactive wizard only asks for
fields relevant to the chosen backend, AND the path-whitelist gate
skips inactive fields (a `root=/etc` value left on an s3 credential
won't trigger a false-positive rejection because the s3 driver doesn't
read `root`).

### Presets

| Name | Backend | Endpoint |
|---|---|---|
| Local | local | — |
| MinIO (local docker) | s3 | http://localhost:9000, default `minioadmin/minioadmin` |
| AWS S3 | s3 | s3.amazonaws.com |
| Cloudflare R2 | s3 | https://<account>.r2.cloudflarestorage.com |
| Custom | s3 | manual |

`Validate` runs `list("",limit=5)` against the configured backend —
one probe that confirms reachability + auth + (s3) bucket existence.

## Search — local backend

Grep-style content search via Go's `regexp`. Skips files that fail a
cheap binary-detection sniff (NUL byte or >30% non-printable in the
first 512B). Honours `include`/`exclude` filename globs and
`max_size`. Default `max_size=1MiB` keeps scans from spending time on
images/tarballs/binary logs by accident — set to `0` to disable.

```bash
# Find every TODO in markdown files, with one line of context each side
pgf run file/local search \
  -p pattern='TODO\(.*\)' \
  -p include='*.md' \
  -p before=1 -p after=1

# Case-insensitive error-trace search across logs
pgf run file/local search \
  -p prefix=logs/ \
  -p pattern='error|exception|traceback' \
  -p case_insensitive=true \
  -p max_matches=50
```

Output shape:

```json
{
  "matches": [
    {"key": "a/two.md", "line": 2, "text": "TODO(claude): write something",
     "before": ["line 1"], "after": ["line 3"], "byte_offset": 7}
  ],
  "count": 1, "scanned": 2, "skipped": 2
}
```

`scanned` = files actually grep'd. `skipped` = filtered out by
include/exclude/size/binary-sniff.

## ls and tree

`list` is both `ls` and `tree` depending on `recursive`:

```bash
pgf run file/x list                       # ls — top level, one row per entry
pgf run file/x list -p recursive=true     # tree / find — whole subtree
pgf run file/x list -p prefix=docs/2026/  # ls a subtree
```

Output is JSON. Pretty-rendering for humans is a `jq` one-liner — no
new action needed:

```bash
# ls -l style
pgf run file/x list | jq -r '
  .entries[] | "\(if .is_dir then "d" else "-" end) \(.size) \(.key)"'

# tree style
pgf run file/x list -p recursive=true | jq -r '
  .entries | sort_by(.key) | .[] | .key
  | split("/") | (length - 1) as $depth | (.[-1])
  | "  " * $depth + (.)'
```

## Path whitelist

- `root` (local credential): `IsPath: true`. Set `PGF_ALLOWED_PATHS` so the local backend's root must sit under an allowed directory.
- `get.out` and `put.src` (per-action local paths): also `IsPath: true`. Verified live — `out=/etc/shadow.md` and `src=/etc/passwd` are both rejected before the I/O happens.
- `ShowWhen=false` fields are skipped by the path validator, so an irrelevant `root` value on an s3 credential is fine.

## Testing against MinIO locally

```bash
docker run -d --rm \
  --name pgf-minio \
  -p 9000:9000 -p 9001:9001 \
  -e MINIO_ROOT_USER=minioadmin \
  -e MINIO_ROOT_PASSWORD=minioadmin \
  minio/minio server /data --console-address :9001

# Create a bucket
docker exec pgf-minio sh -c \
  'mc alias set local http://localhost:9000 minioadmin minioadmin && mc mb local/pgftest'

pgf connect --input '{
  "backend":"s3",
  "endpoint":"http://localhost:9000",
  "region":"us-east-1",
  "bucket":"pgftest",
  "access_key":"minioadmin",
  "secret_key":"minioadmin",
  "use_ssl":false
}' file s3local
```

All s3 actions then work against this instance — verified during
development: put (file + inline), list, stat, get (inline + to-disk),
presign (GET/PUT/HEAD), delete.

## Usage examples

```bash
# Local
pgf connect file work       # wizard; picks local, asks for root
pgf run file/work list -p recursive=true
pgf run file/work stat -p key=docs/spec.md
pgf run file/work get -p key=docs/spec.md         # inline (small text)
pgf run file/work get -p key=videos/intro.mp4 -p out=/tmp/intro.mp4
pgf run file/work put -p key=outbox/note.md -p content='# Hello'
pgf run file/work search -p pattern='deadline' -p include='*.md'

# S3 / MinIO / R2 / B2 (same actions, different credential)
pgf run file/s3 list -p prefix=docs/
pgf run file/s3 put -p key=images/logo.png -p src=/tmp/logo.png
pgf run file/s3 presign -p key=images/logo.png -p method=GET -p expiry=1h
# → public-share URL good for one hour, no credentials exposed
```

## Known gaps

- **No search on s3.** By design: scanning would download every
  object. If you need it, narrow with `list` + `get` + grep locally,
  or wire up a dedicated index (wissen, OpenSearch).
- **No copy/move across backends.** `put(get())` works but isn't a
  single action yet. Add when there's a concrete cross-backend
  workflow.
- **No s3 versioning.** GetObject and PutObject don't take a
  version-id parameter today; can add when needed.
- **No s3 select / object metadata queries.** Out of scope — that's a
  search-engine concern.
- **No smb / sshfs / webdav backends.** Same architecture handles
  them — adding one is one driver file + a few ShowWhen fields. Defer
  until there's a real use case.
- **No streaming through stdout** for `get` without `out`. Inline
  returns text in JSON; binary objects must use `out=PATH`.
