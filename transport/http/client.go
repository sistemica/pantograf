// Package http is a reusable HTTP client wrapper for connectors that talk
// to JSON REST APIs. It's a thin layer over net/http: a Client carries a
// base URL, default headers, and a timeout, and exposes JSON / form /
// multipart helpers so connectors don't reinvent boilerplate per API.
//
// What it does NOT do (intentional, keep it small):
//   - Pagination (each connector knows its API's pagination shape)
//   - OAuth flows (separate concern)
//   - Streaming (SSE/WebSocket are different transports)
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	stdhttp "net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config configures a Client. BaseURL is mandatory and must include scheme.
// Headers are sent on every request; per-call headers are merged on top.
type Config struct {
	BaseURL   string
	Headers   stdhttp.Header
	UserAgent string
	Timeout   time.Duration
}

// Client wraps an *http.Client with default base URL + headers.
type Client struct {
	cfg   Config
	inner *stdhttp.Client
}

// BaseURL returns the configured base URL — useful when a caller wants to
// build a sibling client with a different timeout or header set.
func (c *Client) BaseURL() string { return c.cfg.BaseURL }

// New returns a Client. Config.BaseURL is validated up-front.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("http: BaseURL is required")
	}
	if _, err := url.Parse(cfg.BaseURL); err != nil {
		return nil, fmt.Errorf("http: BaseURL: %w", err)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Headers == nil {
		cfg.Headers = stdhttp.Header{}
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "mw/0.1"
	}
	return &Client{
		cfg:   cfg,
		inner: &stdhttp.Client{Timeout: cfg.Timeout},
	}, nil
}

// APIError is returned for any non-2xx response. The body is captured so
// callers can surface server-side error messages to the user.
type APIError struct {
	Status int
	URL    string
	Body   []byte
}

func (e *APIError) Error() string {
	body := strings.TrimSpace(string(e.Body))
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	return fmt.Sprintf("http %d %s: %s", e.Status, e.URL, body)
}

// FileField describes one file part for multipart uploads.
type FileField struct {
	FieldName string // form field name (e.g. "document")
	Path      string // local file path
	MimeType  string // optional; sniffed if empty
	Filename  string // optional; basename of Path if empty
}

// GetJSON does GET <path>?<query> and JSON-unmarshals the response into out.
// out may be nil to discard the body.
func (c *Client) GetJSON(ctx context.Context, path string, query url.Values, out any) error {
	req, err := c.newRequest(ctx, stdhttp.MethodGet, path, query, nil, "")
	if err != nil {
		return err
	}
	return c.doJSON(req, out)
}

// SendJSON marshals body as JSON, sends with the given method, and
// unmarshals the response. Use for POST / PUT / PATCH / DELETE-with-body.
// For GET use GetJSON; for forms / multipart see PostForm / PostMultipart.
//
//   SendJSON(ctx, "POST",  path, reqBody, &out)
//   SendJSON(ctx, "PUT",   path, reqBody, &out)
//   SendJSON(ctx, "PATCH", path, reqBody, &out)
func (c *Client) SendJSON(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("http: marshal request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := c.newRequest(ctx, method, path, nil, rdr, "application/json")
	if err != nil {
		return err
	}
	return c.doJSON(req, out)
}

// PostForm sends form-urlencoded values via POST.
func (c *Client) PostForm(ctx context.Context, path string, form url.Values, out any) error {
	body := strings.NewReader(form.Encode())
	req, err := c.newRequest(ctx, stdhttp.MethodPost, path, nil, body, "application/x-www-form-urlencoded")
	if err != nil {
		return err
	}
	return c.doJSON(req, out)
}

// PostMultipart sends a multipart/form-data POST. fields are simple form
// values; files are streamed from disk. Use for file uploads (Telegram
// sendDocument, Slack files.upload, etc.).
func (c *Client) PostMultipart(ctx context.Context, path string, fields map[string]string, files []FileField, out any) error {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		defer mw.Close()
		for k, v := range fields {
			if err := mw.WriteField(k, v); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}
		for _, f := range files {
			file, err := os.Open(f.Path)
			if err != nil {
				_ = pw.CloseWithError(fmt.Errorf("open %s: %w", f.Path, err))
				return
			}
			fname := f.Filename
			if fname == "" {
				fname = filepath.Base(f.Path)
			}
			part, err := mw.CreateFormFile(f.FieldName, fname)
			if err != nil {
				file.Close()
				_ = pw.CloseWithError(err)
				return
			}
			if _, err := io.Copy(part, file); err != nil {
				file.Close()
				_ = pw.CloseWithError(err)
				return
			}
			file.Close()
		}
	}()

	req, err := c.newRequest(ctx, stdhttp.MethodPost, path, nil, pr, mw.FormDataContentType())
	if err != nil {
		return err
	}
	return c.doJSON(req, out)
}

// Do is the escape hatch for callers that need full *http.Response control
// (e.g. binary downloads). Path may be absolute or relative to BaseURL.
func (c *Client) Do(ctx context.Context, method, path string, body io.Reader, contentType string) (*stdhttp.Response, error) {
	req, err := c.newRequest(ctx, method, path, nil, body, contentType)
	if err != nil {
		return nil, err
	}
	return c.inner.Do(req)
}

// newRequest builds a request with merged headers.
func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*stdhttp.Request, error) {
	full, err := c.resolveURL(path, query)
	if err != nil {
		return nil, err
	}
	req, err := stdhttp.NewRequestWithContext(ctx, method, full, body)
	if err != nil {
		return nil, err
	}
	for k, vv := range c.cfg.Headers {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// Accept is set by the JSON helpers (doJSON). Do() leaves it empty so
	// the caller can negotiate (Lexware /v1/files/{id} returns base64-in-JSON
	// when Accept: application/json is set, but binary bytes otherwise).
	return req, nil
}

// resolveURL joins BaseURL + path. It does NOT use url.ResolveReference,
// which treats "/foo" as host-absolute and clobbers a non-trivial BaseURL
// path (the `/bot<token>` segment in Telegram's URL is a real example).
// Instead we concat with explicit slash handling so both leading-slash and
// no-slash relative paths land in the right place.
func (c *Client) resolveURL(path string, query url.Values) (string, error) {
	full := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		base := strings.TrimRight(c.cfg.BaseURL, "/")
		full = base + "/" + strings.TrimLeft(path, "/")
	}
	if len(query) > 0 {
		u, err := url.Parse(full)
		if err != nil {
			return "", err
		}
		q := u.Query()
		for k, vv := range query {
			for _, v := range vv {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
		full = u.String()
	}
	return full, nil
}

// doJSON sends req, asserts 2xx, and JSON-unmarshals into out (if non-nil).
func (c *Client) doJSON(req *stdhttp.Request, out any) error {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := c.inner.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return &APIError{Status: resp.StatusCode, URL: req.URL.String(), Body: body}
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("http: unmarshal: %w (body=%q)", err, truncate(body, 200))
	}
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
