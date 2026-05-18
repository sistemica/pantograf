# connectors/web

Fetch URLs and extract structured content. Default path is pure-Go HTTP
(net/http + goquery + html-to-markdown/v2 + Mozilla Readability port).
JavaScript-heavy pages are rendered by connecting to an existing Chrome
instance over the DevTools Protocol — pgf never auto-spawns a browser.

## Actions

| Name | Description |
|---|---|
| `fetch` | Raw fetch. Returns `{url, status, content_type, body, from_cache, js}`. |
| `extract-markdown` | Readability extracts the article; html-to-markdown converts. Returns `{title, byline, excerpt, length, markdown, ...}`. |
| `extract-html` | CSS selector → list of `{text, html, attrs}`. |
| `extract-links` | Every `<a href>` (resolved to absolute) with `{href, text, rel}`. Optional `selector` scopes the search. |
| `extract-media` | `<img>`, `<audio>`, `<video>`, `<source>` URLs with `{kind, src, alt|type}`. Optional `selector` scopes. |
| `screenshot` | Full-page (entire scrollable area) PNG/JPEG via CDP. Requires `cdp_endpoint`. |
| `search` | DuckDuckGo HTML search. Returns `[{title, url, snippet}]`. No API key. |

All fetch-style actions share these inputs: `url` (required), `js` (bool,
CDP mode), `wait_for` (selector, js only), `user_agent`, `timeout`,
`cache_ttl` (default 5m).

## Credential

| Field | Required | Notes |
|---|---|---|
| `cdp_endpoint` | no | `ws://host:9222`. Start Chrome with `--remote-debugging-port=9222`. Connect-only; pgf never spawns. When empty, `js=true` and `screenshot` return a clear error. |
| `default_user_agent` | no | UA on every HTTP fetch. Per-call override via `-p user_agent=...`. |
| `proxy_url` | no | HTTP/SOCKS proxy. `http://host:port` or `socks5://host:port`. Used for net/http; passed to Chrome only if the CDP instance honours it. |

`Validate` is a no-op — no creds to probe. CDP reachability is checked
lazily on the first `js=true` call so an unreachable browser doesn't
block `pgf connect`.

## The cache

All fetch-style actions share a disk cache in the per-instance state
store (`~/.local/state/pgf/state/web/<name>/`). Subsequent extract-*
calls on the same `(url, js, user_agent)` reuse the body without a
second HTTP request:

```bash
pgf run web/default extract-markdown -p url=https://...   # cold: 300ms
pgf run web/default extract-links    -p url=https://...   # cache hit: 40ms
pgf run web/default extract-media    -p url=https://...   # cache hit: 30ms
pgf run web/default extract-html     -p url=https://... -p selector=h2  # cache hit
```

Each response includes `from_cache: bool`. Bypass with `-p cache_ttl=0`,
extend with `-p cache_ttl=1h`. Default is 5m.

## Browser mode (CDP)

Start a Chrome with the DevTools port open:

```bash
chromium --headless=new --remote-debugging-port=9222 --no-sandbox \
  --disable-dev-shm-usage --disable-gpu
```

…or in Docker (any image is fine; this is just one option):

```bash
docker run -d --rm -p 9222:9222 --name chrome \
  ghcr.io/browserless/chromium:latest \
  --remote-debugging-port=9222 --remote-debugging-address=0.0.0.0
```

Then set `cdp_endpoint=ws://host:9222` on the credential. Calls with
`-p js=true` render through the browser before extraction. The
`screenshot` action **always** uses CDP (you can't screenshot what you
didn't render).

`wait_for` accepts any CSS selector — e.g. `wait_for=".product-price"` —
to delay extraction until that element is visible. Useful for sites
that lazy-load content after the initial DOM event.

## Use your logged-in browser session

Each `js=true` call opens a **new tab inside the existing Chrome**
instance. Tabs share the browser's cookie jar and localStorage, so
sites you've logged into manually see pgf as the same authenticated
user. This is the cleanest path to scraping behind a login —
no headless cookie injection, no service account, no automation
fingerprint beyond the CDP attachment itself.

### Setup

1. Start Chrome **headed** (not `--headless`) with a **persistent
   profile** so logins survive restarts:

   ```bash
   chromium \
     --remote-debugging-port=9222 \
     --remote-debugging-address=127.0.0.1 \
     --user-data-dir=$HOME/.config/chromium-pgf \
     about:blank
   ```

2. In that window, log into whatever you need — GitHub, your CRM,
   a paywalled site, your bank's portal. Cookies + localStorage land
   in `~/.config/chromium-pgf` and stay there across Chrome restarts.

3. Point pgf at the same port:

   ```bash
   pgf connect --input '{"cdp_endpoint":"ws://localhost:9222"}' web me
   ```

4. Run with `js=true`. The new tab inherits your session.

   ```bash
   pgf run web/me extract-markdown \
     -p url=https://your-paywalled-site.com/private-article \
     -p js=true \
     -p wait_for="article"
   ```

The tab is cleaned up after each call (`chromedp.NewContext` ends → tab
closes), so your browser doesn't accumulate clutter even after many
runs.

### Security — read this part

CDP has **no authentication**. Anything that can reach port 9222 can
drive your browser as you: read your inbox, post on your behalf, dump
session cookies, take screenshots of every open tab.

- **Bind to localhost only** (the example above sets
  `--remote-debugging-address=127.0.0.1`, which is also the Chrome
  default). Never expose 9222 across the network.
- For a remote Chrome (a desktop you sometimes SSH into) use an
  SSH tunnel: `ssh -L 9222:127.0.0.1:9222 user@host`. Then set
  `cdp_endpoint=ws://localhost:9222` on the pgf side.
- If `pgf` runs as a different unix user than the one owning the
  Chrome profile, that user *also* gets full control of your browser
  via the CDP port. The agent-vs-pgf user split documented in
  pantograf's `SECURITY.md` does **not** protect you here — both
  sides see the same browser. Either run pgf as the same user as
  Chrome, or use a dedicated profile/Chrome instance for scraping.

### Caveats

- **Some sites detect DevTools attached** and degrade or refuse to load
  (Google sign-in flows are the most common case; some banks also
  block). They typically check `navigator.webdriver`, certain
  CDP-only events, or the existence of the debugging port. There are
  third-party "stealth" plugins for Puppeteer/Playwright that mask
  these signals; none are built into chromedp or this connector. If
  you hit it, the site is probably also against scraping by policy —
  worth checking before working around.
- **All tabs in the browser share the cookie jar.** If you want pgf
  to use a *different* identity from your daily-driver Chrome, run a
  separate Chrome instance with a different `--user-data-dir` and a
  different `--remote-debugging-port`. Two pgf credentials,
  `web/personal` and `web/work`, each pointing at their own browser,
  works fine.
- **No incognito support yet.** Every action runs in a regular tab,
  so it sees and writes cookies. A future `private=true` action flag
  could use `chromedp.NewBrowser` for an isolated context when needed.

## Path whitelist

`screenshot.out` is `IsPath: true` — when `PGF_ALLOWED_PATHS` is set,
the output file must resolve under one of the allowed roots. Same gate
as the email connector's `attachments` and whisper's `audio`. Verified:
attempting `screenshot -p out=/etc/shadow.png` is rejected before any
CDP work happens.

## Usage

```bash
# Setup (no creds needed for HTTP-only path)
pgf connect --input '{}' --no-validate web default

# Or with browser mode enabled
pgf connect --input '{"cdp_endpoint":"ws://localhost:9222"}' web default

# Plain HTTP — clean article body as markdown
pgf run web/default extract-markdown -p url=https://go.dev/blog/govulncheck

# Selector — every h2 on the page
pgf run web/default extract-html -p url=https://example.com -p selector=h2

# Every link on the page (resolved to absolute URLs)
pgf run web/default extract-links -p url=https://example.com

# Just the links inside the article
pgf run web/default extract-links \
  -p url=https://example.com -p selector="article"

# All images + audio + video
pgf run web/default extract-media -p url=https://example.com

# Browser mode for a JS-heavy SPA, waiting for content to render
pgf run web/default extract-markdown \
  -p url=https://news.ycombinator.com -p js=true -p wait_for=".athing"

# Full-page screenshot (scrolls entire page height)
pgf run web/default screenshot \
  -p url=https://en.wikipedia.org/wiki/Pantograph \
  -p out=/tmp/pantograph.png

# JPEG with quality
pgf run web/default screenshot \
  -p url=https://example.com -p out=/tmp/snap.jpg -p quality=75

# Web search
pgf run web/default search -p query="open-source LLM router" -p max_results=10
pgf run web/default search -p site=github.com -p query="chromedp examples"

# Compose: search → fetch top result as markdown
pgf run web/default search -p query="govulncheck release" \
  | jq -r '.results[0].url' \
  | xargs -I{} pgf run web/default extract-markdown -p url={}
```

## Known gaps

- **No streaming JS evaluation.** Can't run custom JS in the page yet
  (e.g. scroll to bottom, click a button). chromedp supports it; add a
  `evaluate` action when there's a concrete use case.
- **Single page per call.** No crawl / link-following loop — that's an
  agent-loop concern, not a connector one.
- **DDG only for search.** No Brave, Google, Searx, Tavily. The
  credential schema leaves room; adding `engine` as an enum is the
  next step.
- **No HAR / network capture.** Useful for paywall investigations
  (XHR endpoints behind a SPA) but out of scope here.
- **`screenshot` always re-renders.** The HTML cache and the pixel
  output are independent; calling screenshot twice within `cache_ttl`
  still re-screenshots. Easy to add a pixel cache when needed.
