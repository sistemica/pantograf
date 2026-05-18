package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sistemica/pantograf/connector"
)

const (
	maxResponseSize    = 8 * 1024 * 1024 // 8 MiB — pages larger than this are rare and probably bot traps
	defaultHTTPTimeout = 30 * time.Second
)

// fetchInputs is the shared parameter set every action accepts. Each
// extract-* action calls fetchOrCache to obtain a page and then operates
// on the resulting HTML.
type fetchInputs struct {
	URL       string
	JS        bool
	WaitFor   string // CSS selector to wait for (js mode only)
	Selector  string // CSS selector to scope extraction (extract-* only)
	UserAgent string
	Timeout   time.Duration
	CacheTTL  time.Duration
}

// readFetchInputs pulls the shared params off the schema. Each action's
// Schema() repeats the shared set; this helper avoids re-implementing the
// extraction six times.
func readFetchInputs(params connector.Values) (fetchInputs, error) {
	rawURL := strings.TrimSpace(params.String("url"))
	if rawURL == "" {
		return fetchInputs{}, errors.New("url is required")
	}
	if _, err := url.Parse(rawURL); err != nil {
		return fetchInputs{}, fmt.Errorf("url: %w", err)
	}
	timeout := defaultHTTPTimeout
	if t := params.String("timeout"); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return fetchInputs{}, fmt.Errorf("timeout: %w", err)
		}
		timeout = d
	}
	return fetchInputs{
		URL:       rawURL,
		JS:        params.Bool("js"),
		WaitFor:   params.String("wait_for"),
		Selector:  params.String("selector"),
		UserAgent: params.String("user_agent"),
		Timeout:   timeout,
		CacheTTL:  parseTTL(params.String("cache_ttl")),
	}, nil
}

// fetchOrCache returns the (possibly cached) page body. Sets fromCache so
// the action can surface it in the response. When the cache misses or is
// disabled, fetches via either HTTP or CDP.
func fetchOrCache(ctx context.Context, s *session, in fetchInputs) (entry *cacheEntry, fromCache bool, err error) {
	ua := in.UserAgent
	if ua == "" {
		ua = s.cred.Values.String(fDefaultUserAgent)
	}
	if ua == "" {
		ua = defaultUserAgent
	}
	key := cacheKey(in.URL, in.JS, ua)

	if cached, ok := readCache(ctx, s, key, in.CacheTTL); ok {
		return cached, true, nil
	}

	if in.JS {
		entry, err = fetchViaCDP(ctx, s, in.URL, in.WaitFor, in.Timeout, ua)
	} else {
		entry, err = fetchViaHTTP(ctx, s, in.URL, in.Timeout, ua)
	}
	if err != nil {
		return nil, false, err
	}
	writeCache(ctx, s, key, entry)
	return entry, false, nil
}

// fetchViaHTTP is the pure-Go path. Honours an optional proxy URL on the
// credential. Reads up to maxResponseSize, refuses to buffer more (bot
// traps and accidental huge downloads).
func fetchViaHTTP(ctx context.Context, s *session, target string, timeout time.Duration, userAgent string) (*cacheEntry, error) {
	transport := &stdhttp.Transport{
		Proxy: stdhttp.ProxyFromEnvironment,
	}
	if p := s.cred.Values.String(fProxyURL); p != "" {
		u, err := url.Parse(p)
		if err != nil {
			return nil, fmt.Errorf("proxy_url: %w", err)
		}
		transport.Proxy = stdhttp.ProxyURL(u)
	}
	client := &stdhttp.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *stdhttp.Request, via []*stdhttp.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}

	req, err := stdhttp.NewRequestWithContext(ctx, "GET", target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxResponseSize {
		return nil, fmt.Errorf("response exceeds %d bytes", maxResponseSize)
	}

	return &cacheEntry{
		FetchedAt:   time.Now().UTC(),
		Status:      resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		FinalURL:    resp.Request.URL.String(),
		Body:        string(body),
		JS:          false,
	}, nil
}
