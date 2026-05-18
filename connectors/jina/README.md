# connectors/jina

Wrapper around Jina AI's hosted Reader / Search / Grounding endpoints.
Treat this as the fallback when the local `web` connector hits bot
detection, paywall walls, or JS-heavy pages it can't render — Jina
runs a real browser fleet on residential-ish IPs and returns
LLM-clean markdown.

Composes well with `web`:

```bash
pgf run web/x extract-markdown -p url=https://... || \
pgf run jina/x read -p url=https://...
```

## Actions

| Name | Anonymous tier | Description |
|---|---|---|
| `read` | ✅ (20 RPM) | URL → markdown via `r.jina.ai`. Title + content + metadata. |
| `search` | ❌ requires key | Web search via `s.jina.ai`. Top results with content snippets. |
| `ground` | ❌ requires key | Fact-check a statement via `g.jina.ai`. Returns factuality + references. |

`search` and `ground` return **401 Authentication required** without
an API key — Jina's anonymous tier covers only `read`. Free key from
[jina.ai](https://jina.ai) unlocks the rest.

## Credential

| Field | Required | Notes |
|---|---|---|
| `api_key` | optional for read, **required for search/ground** | Bearer token. Free tier: 500 RPM. |
| `default_engine` | no | Default for the `engine` param: `""` (Jina default), `direct`, `browser`, `cf-browser-rendering`. Override per-call. |
| `reader_base` | no | Default `https://r.jina.ai`. Change only for a self-hosted Reader. |
| `search_base` | no | Default `https://s.jina.ai`. |
| `ground_base` | no | Default `https://g.jina.ai`. |

`Validate` calls Reader against `example.com` to confirm reachability
and that the API key is at least accepted.

## The cache

All three actions share a disk cache in the per-instance state store
(`~/.local/state/pgf/state/jina/<name>/`), keyed by (action, URL/query/
statement, request headers). TTLs:

- `read`: 5 minutes (configurable via `-p cache_ttl=10m` / `cache_ttl=0`)
- `search`: 10 minutes
- `ground`: 1 hour (facts change slowly)

Each response includes `from_cache: bool`. Verified: cold `read` of
`example.com` was 315ms, cache hit was 11ms.

## Engine modes (Reader, Search)

| `engine` | What it does |
|---|---|
| `""` (default) | Jina chooses — direct for static pages, browser when it detects JS-heavy. |
| `direct` | Fast HTTP fetch. Cheapest. |
| `browser` | Render in a real Chromium. Use for SPA-style sites or when direct misses content. |
| `cf-browser-rendering` | Cloudflare's hosted rendering. Sometimes evades stricter bot detection. |

Set a default on the credential (`default_engine`) or override per-call
with `-p engine=browser`.

## Useful Reader options

| Param | Effect |
|---|---|
| `locale=en-US` | Hints the rendering locale; some sites localise content. |
| `with_links_summary=true` | Appends a deduplicated link list at the end of the markdown. Cheap way to feed an agent every outbound link without a second call. |
| `with_images_summary=true` | Same for images. |
| `with_generated_alt=true` | Generates alt text for images that lack one. Useful when downstream LLM consumes the markdown. |
| `respond_with=readerlm-v2` | Uses Jina's specialised reader model — slower, higher fidelity. |
| `json_schema=<JSON>` | Structured extraction. Jina returns content shaped to the schema instead of free-form markdown. Powerful but token-expensive. |
| `no_cache=true` | Skip Jina's own cache. (Separate from the local pgf cache, which uses `cache_ttl`.) |

## Usage

```bash
# Anonymous tier (no key needed for read)
pgf connect --input '{}' jina anon

# With a key for search/ground
pgf connect --input '{"api_key":"jina_xxx"}' jina mykey

# Read a URL
pgf run jina/anon read -p url=https://news.ycombinator.com

# Read with browser rendering for a JS-heavy SPA
pgf run jina/anon read \
  -p url=https://www.aitoolsdirectory.com \
  -p engine=browser

# Read + structured extraction
pgf run jina/mykey read \
  -p url=https://aiagent.app/tools/aider \
  -p json_schema='{"type":"object","properties":{"name":{"type":"string"},"pricing":{"type":"string"},"key_features":{"type":"array","items":{"type":"string"}}}}'

# Search (needs key)
pgf run jina/mykey search -p query="open-source code review agents"

# Ground a statement (needs key)
pgf run jina/mykey ground -p statement="Go 1.22 added range-over-func"
```

## When to use Jina vs the local web connector

| Situation | Use |
|---|---|
| Article on a static site or simple blog | `web extract-markdown` — local, fast, no service fees |
| Cloudflare / hCaptcha / heavy bot detection | `jina read` — Jina's fleet generally gets through |
| Heavy JS SPA, *and* you have a logged-in Chrome via CDP | `web extract-markdown -p js=true` — uses YOUR session |
| Heavy JS SPA, no logged-in browser available | `jina read -p engine=browser` |
| Need just title + plain text | Either; jina is more resilient |
| Need CSS-selector-based extraction (`extract-html`, `extract-links`) | `web` — jina returns markdown, not HTML |
| Full-page screenshot | `web screenshot` (needs CDP) — jina doesn't do screenshots from this API |
| Web search | Pick by what's reachable: `web search` (DDG, no key) or `jina search` (better quality, needs key) |

## Known gaps

- **No structured extraction helpers in this connector.** The
  `json_schema` param is plumbed through but parsing the result back
  into a typed shape is on the agent. Cheap workaround: pipe to `jq`.
- **No streaming.** Jina supports SSE on Reader; the connector calls
  the buffered JSON variant for simplicity. Streaming would be a
  separate action.
- **Single URL per `read` call.** Multi-URL batch is on Jina's
  roadmap; not exposed here.
- **No HAR / network capture** like Playwright would give. Out of
  scope.
