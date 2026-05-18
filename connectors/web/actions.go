package web

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	htmltomd "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/PuerkitoBio/goquery"
	readability "github.com/go-shiori/go-readability"

	"github.com/sistemica/pantograf/connector"
)

// sharedFields lists the parameters that appear on every fetch-style
// action. Helper kept here so each action's Schema reads in one glance.
func sharedFields(extra ...connector.FieldSpec) []connector.FieldSpec {
	base := []connector.FieldSpec{
		{Name: "url", Kind: connector.FieldString, Required: true,
			Description: "Absolute http(s) URL."},
		{Name: "js", Kind: connector.FieldBool, Default: false,
			Description: "Render through Chrome over CDP instead of plain HTTP. Requires cdp_endpoint on the credential."},
		{Name: "wait_for", Kind: connector.FieldString,
			Description: "CSS selector to wait for before extracting (js=true only)."},
		{Name: "user_agent", Kind: connector.FieldString,
			Description: "Override the credential's default_user_agent for this call."},
		{Name: "timeout", Kind: connector.FieldString,
			Description: "Go duration: 30s, 2m, etc. Default 30s."},
		{Name: "cache_ttl", Kind: connector.FieldString,
			Description: "Reuse a recent fetch of the same URL+js+user_agent. Go duration; 0 to disable. Default 5m."},
	}
	return append(base, extra...)
}

// ── fetch ─────────────────────────────────────────────────────────────────

type fetchAction struct{}

func (fetchAction) Name() string         { return "fetch" }
func (fetchAction) DisplayName() string  { return "Fetch URL" }
func (fetchAction) Description() string  { return "Fetch a URL (HTTP or CDP). Returns status, content-type, final-URL, body. Honours the per-instance cache." }
func (fetchAction) Schema() connector.Schema {
	return connector.Schema{Fields: sharedFields()}
}

func (a fetchAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	in, err := readFetchInputs(params)
	if err != nil {
		return nil, err
	}
	entry, fromCache, err := fetchOrCache(ctx, s, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"url":          entry.FinalURL,
		"status":       entry.Status,
		"content_type": entry.ContentType,
		"body":         entry.Body,
		"from_cache":   fromCache,
		"js":           entry.JS,
	}, nil
}

// ── extract-markdown ──────────────────────────────────────────────────────

type extractMarkdownAction struct{}

func (extractMarkdownAction) Name() string         { return "extract-markdown" }
func (extractMarkdownAction) DisplayName() string  { return "Extract main content as Markdown" }
func (extractMarkdownAction) Description() string  { return "Mozilla Readability extracts the article body; html-to-markdown converts to clean CommonMark. Best for LLM consumption of articles, blog posts, documentation." }
func (extractMarkdownAction) Schema() connector.Schema {
	return connector.Schema{Fields: sharedFields()}
}

func (a extractMarkdownAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	in, err := readFetchInputs(params)
	if err != nil {
		return nil, err
	}
	entry, fromCache, err := fetchOrCache(ctx, s, in)
	if err != nil {
		return nil, err
	}
	parsedURL, _ := url.Parse(entry.FinalURL)
	article, err := readability.FromReader(strings.NewReader(entry.Body), parsedURL)
	if err != nil {
		return nil, fmt.Errorf("readability: %w", err)
	}
	md, err := htmltomd.ConvertString(article.Content)
	if err != nil {
		return nil, fmt.Errorf("html-to-markdown: %w", err)
	}
	return map[string]any{
		"url":        entry.FinalURL,
		"title":      article.Title,
		"byline":     article.Byline,
		"excerpt":    article.Excerpt,
		"length":     article.Length,
		"site_name":  article.SiteName,
		"markdown":   md,
		"from_cache": fromCache,
	}, nil
}

// ── extract-html ──────────────────────────────────────────────────────────

type extractHTMLAction struct{}

func (extractHTMLAction) Name() string         { return "extract-html" }
func (extractHTMLAction) DisplayName() string  { return "Extract HTML by selector" }
func (extractHTMLAction) Description() string  { return "Return every element matching a CSS selector with its text, outer HTML, and attributes." }
func (extractHTMLAction) Schema() connector.Schema {
	return connector.Schema{Fields: sharedFields(
		connector.FieldSpec{
			Name: "selector", Kind: connector.FieldString, Required: true,
			Description: "CSS selector. e.g. 'article h2', '.product-card', '#main > p'.",
		},
	)}
}

func (a extractHTMLAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	in, err := readFetchInputs(params)
	if err != nil {
		return nil, err
	}
	if in.Selector == "" {
		return nil, errors.New("selector is required")
	}
	entry, fromCache, err := fetchOrCache(ctx, s, in)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(entry.Body))
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	var matches []map[string]any
	doc.Find(in.Selector).Each(func(_ int, sel *goquery.Selection) {
		html, _ := goquery.OuterHtml(sel)
		attrs := map[string]string{}
		if node := sel.Get(0); node != nil {
			for _, a := range node.Attr {
				attrs[a.Key] = a.Val
			}
		}
		matches = append(matches, map[string]any{
			"text":  strings.TrimSpace(sel.Text()),
			"html":  html,
			"attrs": attrs,
		})
	})
	return map[string]any{
		"url":        entry.FinalURL,
		"selector":   in.Selector,
		"count":      len(matches),
		"matches":    matches,
		"from_cache": fromCache,
	}, nil
}

// ── extract-links ─────────────────────────────────────────────────────────

type extractLinksAction struct{}

func (extractLinksAction) Name() string         { return "extract-links" }
func (extractLinksAction) DisplayName() string  { return "Extract links" }
func (extractLinksAction) Description() string  { return "Every <a href>: href (resolved to absolute), text, rel. Optional selector scopes the search to a subtree." }
func (extractLinksAction) Schema() connector.Schema {
	return connector.Schema{Fields: sharedFields(
		connector.FieldSpec{
			Name: "selector", Kind: connector.FieldString,
			Description: "Optional CSS selector to scope. Default = whole document.",
		},
	)}
}

func (a extractLinksAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	in, err := readFetchInputs(params)
	if err != nil {
		return nil, err
	}
	entry, fromCache, err := fetchOrCache(ctx, s, in)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(entry.Body))
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	base, _ := url.Parse(entry.FinalURL)

	scope := doc.Selection
	if in.Selector != "" {
		scope = doc.Find(in.Selector)
	}

	var links []map[string]any
	scope.Find("a[href]").Each(func(_ int, sel *goquery.Selection) {
		href, _ := sel.Attr("href")
		href = strings.TrimSpace(href)
		if href == "" {
			return
		}
		abs := href
		if base != nil {
			if u, err := base.Parse(href); err == nil {
				abs = u.String()
			}
		}
		rel, _ := sel.Attr("rel")
		links = append(links, map[string]any{
			"href": abs,
			"text": strings.TrimSpace(sel.Text()),
			"rel":  rel,
		})
	})
	return map[string]any{
		"url":        entry.FinalURL,
		"count":      len(links),
		"links":      links,
		"from_cache": fromCache,
	}, nil
}

// ── extract-media ─────────────────────────────────────────────────────────

type extractMediaAction struct{}

func (extractMediaAction) Name() string         { return "extract-media" }
func (extractMediaAction) DisplayName() string  { return "Extract media URLs" }
func (extractMediaAction) Description() string  { return "Every <img>, <audio>, <video>, <source>. Resolves to absolute URLs. Kind tag identifies which element." }
func (extractMediaAction) Schema() connector.Schema {
	return connector.Schema{Fields: sharedFields(
		connector.FieldSpec{
			Name: "selector", Kind: connector.FieldString,
			Description: "Optional CSS selector to scope.",
		},
	)}
}

func (a extractMediaAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	in, err := readFetchInputs(params)
	if err != nil {
		return nil, err
	}
	entry, fromCache, err := fetchOrCache(ctx, s, in)
	if err != nil {
		return nil, err
	}
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(entry.Body))
	if err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	base, _ := url.Parse(entry.FinalURL)

	scope := doc.Selection
	if in.Selector != "" {
		scope = doc.Find(in.Selector)
	}

	abs := func(s string) string {
		s = strings.TrimSpace(s)
		if s == "" || base == nil {
			return s
		}
		if u, err := base.Parse(s); err == nil {
			return u.String()
		}
		return s
	}

	var media []map[string]any
	scope.Find("img[src]").Each(func(_ int, sel *goquery.Selection) {
		src, _ := sel.Attr("src")
		alt, _ := sel.Attr("alt")
		media = append(media, map[string]any{"kind": "img", "src": abs(src), "alt": alt})
	})
	scope.Find("audio[src], audio source[src]").Each(func(_ int, sel *goquery.Selection) {
		src, _ := sel.Attr("src")
		typ, _ := sel.Attr("type")
		media = append(media, map[string]any{"kind": "audio", "src": abs(src), "type": typ})
	})
	scope.Find("video[src], video source[src]").Each(func(_ int, sel *goquery.Selection) {
		src, _ := sel.Attr("src")
		typ, _ := sel.Attr("type")
		media = append(media, map[string]any{"kind": "video", "src": abs(src), "type": typ})
	})
	return map[string]any{
		"url":        entry.FinalURL,
		"count":      len(media),
		"media":      media,
		"from_cache": fromCache,
	}, nil
}

// ── screenshot ────────────────────────────────────────────────────────────

type screenshotAction struct{}

func (screenshotAction) Name() string         { return "screenshot" }
func (screenshotAction) DisplayName() string  { return "Full-page screenshot" }
func (screenshotAction) Description() string  { return "Render the page over CDP and capture the entire scrollable area (not just the viewport) to a PNG or JPEG file. Requires cdp_endpoint." }
func (screenshotAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "url", Kind: connector.FieldString, Required: true,
			Description: "Absolute http(s) URL."},
		{Name: "out", Kind: connector.FieldString, Required: true, IsPath: true,
			Description: "Output file path. Extension picks format: .png (default) or .jpg/.jpeg."},
		{Name: "wait_for", Kind: connector.FieldString,
			Description: "CSS selector to wait for before capturing."},
		{Name: "quality", Kind: connector.FieldInt, Default: 90,
			Description: "JPEG quality 1–100. Ignored for PNG."},
		{Name: "user_agent", Kind: connector.FieldString,
			Description: "Override the credential's default_user_agent."},
		{Name: "timeout", Kind: connector.FieldString, Default: "60s",
			Description: "Go duration; longer than fetch default because large pages take time to render."},
	}}
}

func (a screenshotAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	target := strings.TrimSpace(params.String("url"))
	out := strings.TrimSpace(params.String("out"))
	if target == "" || out == "" {
		return nil, errors.New("url and out are required")
	}
	timeout, err := durationOrDefault(params.String("timeout"), 60*time.Second)
	if err != nil {
		return nil, fmt.Errorf("timeout: %w", err)
	}
	ua := params.String("user_agent")
	if ua == "" {
		ua = s.cred.Values.String(fDefaultUserAgent)
	}
	if ua == "" {
		ua = defaultUserAgent
	}
	buf, err := screenshotViaCDP(ctx, s, target, params.String("wait_for"), timeout, ua, params.Int("quality"))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(out, buf, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", out, err)
	}
	return map[string]any{
		"url":   target,
		"path":  out,
		"bytes": len(buf),
	}, nil
}

// ── search ────────────────────────────────────────────────────────────────
//
// DuckDuckGo HTML search. No API key. Returns structured {title, url,
// snippet}. v1 is DDG-only because it works without setup; the credential
// schema leaves room for future engine swapping.

type searchAction struct{}

func (searchAction) Name() string         { return "search" }
func (searchAction) DisplayName() string  { return "Web search (DuckDuckGo)" }
func (searchAction) Description() string  { return "DuckDuckGo HTML search. Returns [{title, url, snippet}]. No API key needed; may rate-limit under heavy use." }
func (searchAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "query", Kind: connector.FieldString, Required: true,
			Description: "Search query string. Use site:example.com to scope."},
		{Name: "site", Kind: connector.FieldString,
			Description: "Convenience: prefixes query with site:<value>."},
		{Name: "max_results", Kind: connector.FieldInt, Default: 10,
			Description: "1–25. Default 10."},
		{Name: "user_agent", Kind: connector.FieldString,
			Description: "Override default UA."},
		{Name: "timeout", Kind: connector.FieldString,
			Description: "Go duration. Default 30s."},
	}}
}

func (a searchAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := strings.TrimSpace(params.String("query"))
	if q == "" {
		return nil, errors.New("query is required")
	}
	if site := strings.TrimSpace(params.String("site")); site != "" {
		q = "site:" + site + " " + q
	}
	max := params.Int("max_results")
	if max <= 0 {
		max = 10
	}
	if max > 25 {
		max = 25
	}
	timeout, err := durationOrDefault(params.String("timeout"), defaultHTTPTimeout)
	if err != nil {
		return nil, fmt.Errorf("timeout: %w", err)
	}
	ua := params.String("user_agent")
	if ua == "" {
		ua = s.cred.Values.String(fDefaultUserAgent)
	}
	if ua == "" {
		// DDG serves the HTML variant for desktop browsers; an obviously
		// non-browser UA gets a 202 challenge.
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	}

	results, err := ddgSearch(ctx, s, q, max, ua, timeout)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"query":   q,
		"engine":  "duckduckgo",
		"count":   len(results),
		"results": results,
	}, nil
}

// durationOrDefault parses a Go duration; returns the default on empty.
func durationOrDefault(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}
