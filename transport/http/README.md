# transport/http

Reusable HTTP client for connectors that talk to JSON REST APIs. Thin layer
over `net/http`: a `Client` carries a base URL, default headers, and a
timeout, and exposes JSON / form / multipart helpers so each new connector
doesn't reinvent boilerplate.

## Surface

| Method | Purpose |
|---|---|
| `New(Config) (*Client, error)` | Build a client. Validates BaseURL up-front. |
| `(*Client).BaseURL() string` | Read back the configured base URL. Useful when constructing a sibling client (e.g. with a longer timeout for long-poll). |
| `(*Client).GetJSON(ctx, path, query, out)` | GET; JSON-unmarshal response into `out`. `out` may be nil. |
| `(*Client).PostJSON(ctx, path, body, out)` | POST a JSON body; JSON-unmarshal response. |
| `(*Client).PostForm(ctx, path, form, out)` | POST `application/x-www-form-urlencoded`. |
| `(*Client).PostMultipart(ctx, path, fields, files, out)` | `multipart/form-data` with file streaming via `io.Pipe`. |
| `(*Client).Do(ctx, method, path, body, ct)` | Escape hatch: returns `*http.Response` for binary downloads etc. |

## Config

| Field | Type | Default | Notes |
|---|---|---|---|
| `BaseURL` | string | — | required, must include scheme |
| `Headers` | http.Header | empty | sent on every request; per-call merge on top |
| `UserAgent` | string | `mw/0.1` | sets `User-Agent` |
| `Timeout` | time.Duration | 30s | client-level deadline; ctx still drives shutdown |

## URL composition

Relative paths are concatenated to BaseURL with explicit slash handling.
This deliberately avoids `url.ResolveReference`, which silently clobbers a
non-trivial BaseURL path when the relative path begins with `/` (Telegram's
`/bot<token>` prefix is a real example that broke during testing).

| BaseURL | Path argument | Resulting URL |
|---|---|---|
| `https://api.example.com/v1` | `/users` | `https://api.example.com/v1/users` |
| `https://api.example.com/v1/` | `users` | `https://api.example.com/v1/users` |
| anything | `https://other.host/x` | `https://other.host/x` (absolute passthrough) |

## File uploads

`PostMultipart` streams files from disk via `io.Pipe`, so memory stays
bounded regardless of file size. `FileField`:

| Field | Notes |
|---|---|
| `FieldName` | the form field name (e.g. `"document"`) |
| `Path` | local file path |
| `MimeType` | optional; sniffed if empty |
| `Filename` | optional; defaults to `filepath.Base(Path)` |

## Errors

Non-2xx responses return `*APIError{Status, URL, Body}`. The body is
truncated to 200 chars in `Error()` for sane logs but kept in full on the
struct so callers can surface server-side error messages to users.

## Example

```go
import http "github.com/sistemica/mw/transport/http"

cli, _ := http.New(http.Config{
    BaseURL: "https://api.example.com/v1",
    Headers: stdhttp.Header{"Authorization": []string{"Bearer xxx"}},
})

var me struct {
    ID   int    `json:"id"`
    Name string `json:"name"`
}
if err := cli.GetJSON(ctx, "/me", nil, &me); err != nil {
    var apiErr *http.APIError
    if errors.As(err, &apiErr) {
        // server-side error, apiErr.Status / apiErr.Body
    }
    return err
}
```

## What this package does NOT do

- Pagination — every API does it differently; the connector knows its API.
- OAuth / token refresh flows — separate concern.
- Streaming (SSE, WebSocket) — different transports.
- Retry / backoff — caller's policy. The Telegram messages trigger
  implements its own exponential backoff for long-poll error recovery.
