package jina

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// readerResponse / searchResponse / groundResponse match Jina's documented
// JSON envelopes. They surface only the fields we actually use; ignore the
// rest (Jina adds metadata over time, e.g. usage, model, requestId).

type readerResponse struct {
	Code int `json:"code"`
	Data struct {
		Title         string `json:"title"`
		URL           string `json:"url"`
		Description   string `json:"description,omitempty"`
		Content       string `json:"content"`
		PublishedTime string `json:"publishedTime,omitempty"`
		Warning       string `json:"warning,omitempty"`
		Usage         struct {
			Tokens int `json:"tokens"`
		} `json:"usage,omitempty"`
	} `json:"data"`
}

type searchResponse struct {
	Code int `json:"code"`
	Data []struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description,omitempty"`
		Content     string `json:"content,omitempty"`
		Usage       struct {
			Tokens int `json:"tokens"`
		} `json:"usage,omitempty"`
	} `json:"data"`
}

type groundResponse struct {
	Code int `json:"code"`
	Data struct {
		Factuality float64  `json:"factuality"`
		Result     bool     `json:"result"`
		Reason     string   `json:"reason"`
		References []struct {
			URL         string `json:"url"`
			KeyQuote    string `json:"keyQuote"`
			IsSupportive bool  `json:"isSupportive"`
		} `json:"references"`
		Usage struct {
			Tokens int `json:"tokens"`
		} `json:"usage,omitempty"`
	} `json:"data"`
}

// requestHeaders builds the optional X-* request headers from params.
// Default-engine on the credential is applied here when the per-call
// engine param is empty.
func requestHeaders(s *session, params connector.Values) stdhttp.Header {
	h := stdhttp.Header{}
	h.Set("Accept", "application/json")

	engine := params.String("engine")
	if engine == "" {
		engine = s.cred.Values.String(fDefaultEngine)
	}
	if engine != "" {
		h.Set("X-Engine", engine)
	}
	if loc := params.String("locale"); loc != "" {
		h.Set("X-Locale", loc)
	}
	if params.Bool("no_cache") {
		h.Set("X-No-Cache", "true")
	}
	if params.Bool("with_links_summary") {
		h.Set("X-With-Links-Summary", "true")
	}
	if params.Bool("with_images_summary") {
		h.Set("X-With-Images-Summary", "true")
	}
	if params.Bool("with_generated_alt") {
		h.Set("X-With-Generated-Alt", "true")
	}
	if t := params.String("respond_with"); t != "" {
		h.Set("X-Respond-With", t)
	}
	if schema := params.String("json_schema"); schema != "" {
		h.Set("X-Json-Schema", schema)
	}
	return h
}

// doWithHeaders sends a GET with the per-call headers merged onto the
// client's default headers. The transport helper only forwards the
// client's static headers; for jina we need per-request X-* headers, so
// we drop down to Do().
func doWithHeaders(ctx context.Context, cli *httptr.Client, path string, extra stdhttp.Header, out any) error {
	resp, err := cli.Do(ctx, stdhttp.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// transport/http.Client.Do already merged Authorization etc. from the
	// client config. We need to apply extra headers BEFORE the request —
	// but Do() builds the request itself. Workaround: build a sibling
	// client per-call that has the extra headers baked into Config.Headers.
	return errors.New("doWithHeaders: should not reach here — use doGet")
}

// doGet builds a one-shot http request with the combined headers, sends
// it, and JSON-unmarshals into out.
func doGet(ctx context.Context, cli *httptr.Client, path string, extra stdhttp.Header, out any) error {
	// transport/http exposes Headers() so we can compose without losing
	// the credential's Authorization.
	hdr := stdhttp.Header{}
	for k, vv := range cli.Headers() {
		for _, v := range vv {
			hdr.Add(k, v)
		}
	}
	for k, vv := range extra {
		for _, v := range vv {
			hdr.Set(k, v)
		}
	}

	full := cli.BaseURL() + path
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, full, nil)
	if err != nil {
		return err
	}
	for k, vv := range hdr {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	// transport/http.Client doesn't expose its inner *http.Client; build a
	// minimal one here. Reasonable default timeouts; ctx still drives
	// cancellation.
	httpCli := &stdhttp.Client{Timeout: 0}
	resp, err := httpCli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := readAllLimited(resp.Body, 16*1024*1024)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("jina: http %d: %s", resp.StatusCode, truncate(string(body), 240))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("jina: parse response: %w (body=%s)", err, truncate(string(body), 240))
	}
	return nil
}

// ── cache (shared shape with the web connector, separate disk namespace) ──

type cacheEntry struct {
	FetchedAt time.Time       `json:"fetched_at"`
	Action    string          `json:"action"`
	Body      json.RawMessage `json:"body"`
}

func cacheKey(action, payload string) string {
	h := sha256.New()
	h.Write([]byte(action))
	h.Write([]byte{0})
	h.Write([]byte(payload))
	return "jina:" + hex.EncodeToString(h.Sum(nil))[:32]
}

func cacheRead(ctx context.Context, s *session, key string, ttl time.Duration) (json.RawMessage, bool) {
	if ttl <= 0 || s.state == nil {
		return nil, false
	}
	raw, ok, err := s.state.Get(ctx, key)
	if err != nil || !ok {
		return nil, false
	}
	var e cacheEntry
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, false
	}
	if time.Since(e.FetchedAt) > ttl {
		return nil, false
	}
	return e.Body, true
}

func cacheWrite(ctx context.Context, s *session, key, action string, body json.RawMessage) {
	if s.state == nil {
		return
	}
	raw, err := json.Marshal(cacheEntry{
		FetchedAt: time.Now().UTC(),
		Action:    action,
		Body:      body,
	})
	if err != nil {
		return
	}
	_ = s.state.Put(ctx, key, raw)
}

func parseTTL(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0
	}
	return d
}

// ── read ──────────────────────────────────────────────────────────────────

type readAction struct{}

func (readAction) Name() string         { return "read" }
func (readAction) DisplayName() string  { return "Read URL → markdown" }
func (readAction) Description() string  { return "Hand a URL to Jina Reader; get back clean LLM-friendly markdown plus title + metadata. Bot-detection resilient via Jina's hosted browser fleet." }
func (readAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "url", Kind: connector.FieldString, Required: true,
			Description: "Absolute http(s) URL to fetch."},
		{Name: "engine", Kind: connector.FieldString,
			Description: "Override default_engine: '' (Jina default) | direct | browser | cf-browser-rendering."},
		{Name: "locale", Kind: connector.FieldString,
			Description: "Browser locale hint, e.g. en-US, de-DE."},
		{Name: "no_cache", Kind: connector.FieldBool, Default: false,
			Description: "Bypass Jina's own cache. Local pgf cache is separate (cache_ttl)."},
		{Name: "with_links_summary", Kind: connector.FieldBool, Default: false,
			Description: "Append a deduplicated link list at the end of the markdown."},
		{Name: "with_images_summary", Kind: connector.FieldBool, Default: false,
			Description: "Append an images list at the end."},
		{Name: "with_generated_alt", Kind: connector.FieldBool, Default: false,
			Description: "Generate alt text for images that lack one."},
		{Name: "respond_with", Kind: connector.FieldString,
			Description: "Set to 'readerlm-v2' to use Jina's specialised reader model (slower, higher quality)."},
		{Name: "json_schema", Kind: connector.FieldLongText,
			Description: "JSON schema for structured extraction. When set, Jina returns content shaped to match."},
		{Name: "cache_ttl", Kind: connector.FieldString,
			Description: "Local pgf cache TTL. Go duration; 0 to disable. Default 5m."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return Jina's full envelope instead of the simplified {title, url, content, ...}."},
	}}
}

func (a readAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	target := strings.TrimSpace(params.String("url"))
	if target == "" {
		return nil, errors.New("url is required")
	}
	if _, err := url.Parse(target); err != nil {
		return nil, fmt.Errorf("url: %w", err)
	}

	headers := requestHeaders(s, params)
	cacheTTL := parseTTL(params.String("cache_ttl"), 5*time.Minute)
	keyPayload := target + "|" + serializeHeaders(headers)
	key := cacheKey("read", keyPayload)

	if body, ok := cacheRead(ctx, s, key, cacheTTL); ok {
		return decodeReader(body, params.Bool("raw"), true), nil
	}

	path := "/" + target // r.jina.ai mounts the URL on the path
	var resp readerResponse
	if err := doGet(ctx, s.http, path, headers, &resp); err != nil {
		return nil, err
	}
	// Re-serialise to JSON for cache so we don't pay re-encoding cost
	// for the cached-hit path.
	rawJSON, _ := json.Marshal(resp)
	cacheWrite(ctx, s, key, "read", rawJSON)
	return decodeReader(rawJSON, params.Bool("raw"), false), nil
}

func decodeReader(body json.RawMessage, raw bool, fromCache bool) map[string]any {
	if raw {
		var anyOut any
		_ = json.Unmarshal(body, &anyOut)
		if m, ok := anyOut.(map[string]any); ok {
			m["from_cache"] = fromCache
			return m
		}
		return map[string]any{"raw": anyOut, "from_cache": fromCache}
	}
	var r readerResponse
	_ = json.Unmarshal(body, &r)
	return map[string]any{
		"title":          r.Data.Title,
		"url":            r.Data.URL,
		"description":    r.Data.Description,
		"content":        r.Data.Content,
		"published_time": r.Data.PublishedTime,
		"warning":        r.Data.Warning,
		"tokens":         r.Data.Usage.Tokens,
		"from_cache":     fromCache,
	}
}

// ── search ────────────────────────────────────────────────────────────────

type searchAction struct{}

func (searchAction) Name() string         { return "search" }
func (searchAction) DisplayName() string  { return "Web search (Jina)" }
func (searchAction) Description() string  { return "Jina's hosted web search. Returns top N results with title + URL + cleaned content snippet. Each result also costs tokens." }
func (searchAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "query", Kind: connector.FieldString, Required: true,
			Description: "Search query string. Free-form; supports site: and other Google-like operators."},
		{Name: "engine", Kind: connector.FieldString,
			Description: "Override default_engine for the underlying page fetches."},
		{Name: "locale", Kind: connector.FieldString,
			Description: "Locale hint."},
		{Name: "no_cache", Kind: connector.FieldBool, Default: false,
			Description: "Bypass Jina's cache."},
		{Name: "cache_ttl", Kind: connector.FieldString,
			Description: "Local cache TTL. Go duration; default 10m."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return Jina's full response."},
	}}
}

func (a searchAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := strings.TrimSpace(params.String("query"))
	if q == "" {
		return nil, errors.New("query is required")
	}
	cli, err := searchClient(s.cred)
	if err != nil {
		return nil, err
	}

	headers := requestHeaders(s, params)
	cacheTTL := parseTTL(params.String("cache_ttl"), 10*time.Minute)
	keyPayload := q + "|" + serializeHeaders(headers)
	key := cacheKey("search", keyPayload)

	if body, ok := cacheRead(ctx, s, key, cacheTTL); ok {
		return decodeSearch(body, params.Bool("raw"), true), nil
	}

	path := "/?q=" + url.QueryEscape(q)
	var resp searchResponse
	if err := doGet(ctx, cli, path, headers, &resp); err != nil {
		return nil, err
	}
	rawJSON, _ := json.Marshal(resp)
	cacheWrite(ctx, s, key, "search", rawJSON)
	return decodeSearch(rawJSON, params.Bool("raw"), false), nil
}

func decodeSearch(body json.RawMessage, raw bool, fromCache bool) map[string]any {
	if raw {
		var anyOut any
		_ = json.Unmarshal(body, &anyOut)
		if m, ok := anyOut.(map[string]any); ok {
			m["from_cache"] = fromCache
			return m
		}
	}
	var r searchResponse
	_ = json.Unmarshal(body, &r)
	results := make([]map[string]any, 0, len(r.Data))
	for _, d := range r.Data {
		results = append(results, map[string]any{
			"title":       d.Title,
			"url":         d.URL,
			"description": d.Description,
			"content":     d.Content,
			"tokens":      d.Usage.Tokens,
		})
	}
	return map[string]any{
		"engine":     "jina",
		"count":      len(results),
		"results":    results,
		"from_cache": fromCache,
	}
}

// ── ground ────────────────────────────────────────────────────────────────

type groundAction struct{}

func (groundAction) Name() string         { return "ground" }
func (groundAction) DisplayName() string  { return "Ground / fact-check a statement" }
func (groundAction) Description() string  { return "Submit a factual statement; Jina searches the web for evidence and returns a factuality score + supportive/refuting references." }
func (groundAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "statement", Kind: connector.FieldLongText, Required: true,
			Description: "The claim to fact-check, in plain English. e.g. 'Go 1.22 added range-over-func.'"},
		{Name: "no_cache", Kind: connector.FieldBool, Default: false,
			Description: "Bypass Jina's cache."},
		{Name: "cache_ttl", Kind: connector.FieldString,
			Description: "Local cache TTL. Go duration; default 1h."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return Jina's full response."},
	}}
}

func (a groundAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	stmt := strings.TrimSpace(params.String("statement"))
	if stmt == "" {
		return nil, errors.New("statement is required")
	}
	cli, err := groundClient(s.cred)
	if err != nil {
		return nil, err
	}

	headers := requestHeaders(s, params)
	cacheTTL := parseTTL(params.String("cache_ttl"), 1*time.Hour)
	key := cacheKey("ground", stmt)

	if body, ok := cacheRead(ctx, s, key, cacheTTL); ok {
		return decodeGround(body, params.Bool("raw"), true), nil
	}

	path := "/" + url.PathEscape(stmt)
	var resp groundResponse
	if err := doGet(ctx, cli, path, headers, &resp); err != nil {
		return nil, err
	}
	rawJSON, _ := json.Marshal(resp)
	cacheWrite(ctx, s, key, "ground", rawJSON)
	return decodeGround(rawJSON, params.Bool("raw"), false), nil
}

func decodeGround(body json.RawMessage, raw bool, fromCache bool) map[string]any {
	if raw {
		var anyOut any
		_ = json.Unmarshal(body, &anyOut)
		if m, ok := anyOut.(map[string]any); ok {
			m["from_cache"] = fromCache
			return m
		}
	}
	var r groundResponse
	_ = json.Unmarshal(body, &r)
	refs := make([]map[string]any, 0, len(r.Data.References))
	for _, ref := range r.Data.References {
		refs = append(refs, map[string]any{
			"url":           ref.URL,
			"key_quote":     ref.KeyQuote,
			"is_supportive": ref.IsSupportive,
		})
	}
	return map[string]any{
		"factuality": r.Data.Factuality,
		"result":     r.Data.Result,
		"reason":     r.Data.Reason,
		"references": refs,
		"from_cache": fromCache,
	}
}
