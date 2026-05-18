package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/sistemica/pantograf/connector"
)

// ── list ──────────────────────────────────────────────────────────────────

type listAction struct{}

func (listAction) Name() string         { return "list" }
func (listAction) DisplayName() string  { return "List entries" }
func (listAction) Description() string  { return "List entries under a prefix. recursive=false (default) returns one level — like `ls`. recursive=true walks the whole subtree — like `find` or `tree`. Works the same on local and s3." }
func (listAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "prefix", Kind: connector.FieldString,
			Description: "Path prefix (local) or key prefix (s3). Empty = root."},
		{Name: "recursive", Kind: connector.FieldBool, Default: false,
			Description: "true = deep walk (`tree` / `find`); false = one level only (`ls`). Both backends honour this."},
		{Name: "limit", Kind: connector.FieldInt, Default: 0,
			Description: "Cap result count. 0 = no limit."},
	}}
}

func (a listAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	entries, err := s.drv.List(ctx, params.String("prefix"), params.Bool("recursive"), params.Int("limit"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"prefix":  params.String("prefix"),
		"count":   len(entries),
		"entries": entries,
	}, nil
}

// ── stat ──────────────────────────────────────────────────────────────────

type statAction struct{}

func (statAction) Name() string         { return "stat" }
func (statAction) DisplayName() string  { return "Stat one entry" }
func (statAction) Description() string  { return "Single-entry metadata: size, last_modified, content_type, etag. Errors if the key doesn't exist." }
func (statAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "key", Kind: connector.FieldString, Required: true,
			Description: "Path (local) or key (s3) relative to the backend root."},
	}}
}

func (a statAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	key := params.String("key")
	if key == "" {
		return nil, errors.New("key is required")
	}
	return s.drv.Stat(ctx, key)
}

// ── get ───────────────────────────────────────────────────────────────────

type getAction struct{}

func (getAction) Name() string         { return "get" }
func (getAction) DisplayName() string  { return "Get one entry" }
func (getAction) Description() string  { return "Download a key. With out=PATH writes to disk; otherwise returns the body inline (use only for known-small text)." }
func (getAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "key", Kind: connector.FieldString, Required: true,
			Description: "Path/key on the backend to fetch."},
		{Name: "out", Kind: connector.FieldString, IsPath: true,
			Description: "Local file path to write the body to. Subject to PGF_ALLOWED_PATHS. When empty, the body is returned inline in the JSON (capped at 1 MiB)."},
	}}
}

func (a getAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	key := params.String("key")
	if key == "" {
		return nil, errors.New("key is required")
	}
	rc, err := s.drv.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	if out := params.String("out"); out != "" {
		f, err := os.Create(out)
		if err != nil {
			return nil, fmt.Errorf("create %s: %w", out, err)
		}
		defer f.Close()
		n, err := io.Copy(f, rc)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"key":   key,
			"path":  out,
			"bytes": n,
		}, nil
	}

	const inlineCap = 1024 * 1024
	var buf bytes.Buffer
	n, err := io.Copy(&buf, io.LimitReader(rc, inlineCap+1))
	if err != nil {
		return nil, err
	}
	if n > inlineCap {
		return nil, fmt.Errorf("body exceeds %d bytes for inline get; pass -p out=<path>", inlineCap)
	}
	return map[string]any{
		"key":   key,
		"bytes": n,
		"body":  buf.String(),
	}, nil
}

// ── put ───────────────────────────────────────────────────────────────────

type putAction struct{}

func (putAction) Name() string         { return "put" }
func (putAction) DisplayName() string  { return "Put one entry" }
func (putAction) Description() string  { return "Upload to a key. Source is either a local file (src=PATH) or inline content (content=...)." }
func (putAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "key", Kind: connector.FieldString, Required: true,
			Description: "Destination path/key on the backend."},
		{Name: "src", Kind: connector.FieldString, IsPath: true,
			Description: "Local file to upload. Subject to PGF_ALLOWED_PATHS. Mutually exclusive with `content`."},
		{Name: "content", Kind: connector.FieldLongText,
			Description: "Inline content. Mutually exclusive with `src`."},
		{Name: "content_type", Kind: connector.FieldString,
			Description: "Override Content-Type. When empty, sniffed from extension (local + s3) or left blank."},
	}}
}

func (a putAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	key := params.String("key")
	if key == "" {
		return nil, errors.New("key is required")
	}
	src := params.String("src")
	content := params.String("content")
	if src == "" && content == "" {
		return nil, errors.New("either src or content is required")
	}
	if src != "" && content != "" {
		return nil, errors.New("src and content are mutually exclusive")
	}

	contentType := params.String("content_type")
	if contentType == "" {
		// Sniff from the destination key's extension as a fallback.
		contentType = guessMime(key)
	}

	var (
		reader io.Reader
		size   int64
	)
	if src != "" {
		f, err := os.Open(src)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", src, err)
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		reader = f
		size = info.Size()
	} else {
		b := []byte(content)
		reader = bytes.NewReader(b)
		size = int64(len(b))
	}

	entry, err := s.drv.Put(ctx, key, reader, size, contentType)
	if err != nil {
		return nil, err
	}
	return entry, nil
}

// ── delete ────────────────────────────────────────────────────────────────

type deleteAction struct{}

func (deleteAction) Name() string         { return "delete" }
func (deleteAction) DisplayName() string  { return "Delete one entry" }
func (deleteAction) Description() string  { return "Remove a key. Idempotent — deleting a missing key returns no error." }
func (deleteAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "key", Kind: connector.FieldString, Required: true,
			Description: "Path/key to remove."},
	}}
}

func (a deleteAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	key := params.String("key")
	if key == "" {
		return nil, errors.New("key is required")
	}
	if err := s.drv.Delete(ctx, key); err != nil {
		return nil, err
	}
	return map[string]any{"key": key, "deleted": true}, nil
}

// ── search ────────────────────────────────────────────────────────────────

type searchAction struct{}

func (searchAction) Name() string         { return "search" }
func (searchAction) DisplayName() string  { return "Grep-like content search" }
func (searchAction) Description() string  { return "Regex search across file contents. Local backend only — S3 would require downloading every object. Filename glob + max-size cutoff keep scans bounded." }
func (searchAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "pattern", Kind: connector.FieldString, Required: true,
			Description: "Go regexp. e.g. 'TODO\\(.*\\)', 'func \\w+', 'ERROR.*timeout'."},
		{Name: "prefix", Kind: connector.FieldString,
			Description: "Restrict to a subtree. Empty = whole root."},
		{Name: "include", Kind: connector.FieldString,
			Description: "Filename glob to include. e.g. '*.go', '*.md'."},
		{Name: "exclude", Kind: connector.FieldString,
			Description: "Filename glob to skip. e.g. '*.test.js'."},
		{Name: "max_size", Kind: connector.FieldInt, Default: 1048576,
			Description: "Skip files larger than this many bytes. Default 1 MiB; 0 = no limit."},
		{Name: "max_matches", Kind: connector.FieldInt, Default: 200,
			Description: "Cap total matches. 0 = no cap. Default 200."},
		{Name: "before", Kind: connector.FieldInt, Default: 0,
			Description: "Context lines before each match."},
		{Name: "after", Kind: connector.FieldInt, Default: 0,
			Description: "Context lines after each match."},
		{Name: "case_insensitive", Kind: connector.FieldBool, Default: false,
			Description: "Prefix the pattern with (?i)."},
	}}
}

func (a searchAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	searcher, ok := s.drv.(Searcher)
	if !ok {
		return nil, fmt.Errorf("search: not supported by the %q backend (download with `get` and grep locally, or wire up a search service)", s.cred.Values.String(fBackend))
	}
	opts := SearchOpts{
		Prefix:          params.String("prefix"),
		Pattern:         params.String("pattern"),
		Include:         params.String("include"),
		Exclude:         params.String("exclude"),
		MaxSize:         int64(params.Int("max_size")),
		MaxMatches:      params.Int("max_matches"),
		Before:          params.Int("before"),
		After:           params.Int("after"),
		CaseInsensitive: params.Bool("case_insensitive"),
	}
	return searcher.Search(ctx, opts)
}

// ── presign ───────────────────────────────────────────────────────────────

type presignAction struct{}

func (presignAction) Name() string         { return "presign" }
func (presignAction) DisplayName() string  { return "Presign URL" }
func (presignAction) Description() string  { return "Generate a time-limited URL to GET/PUT/HEAD an object. S3 only; local returns 'not supported'." }
func (presignAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "key", Kind: connector.FieldString, Required: true,
			Description: "Object key."},
		{Name: "method", Kind: connector.FieldEnum, Default: "GET",
			Options: []connector.EnumOption{
				{Value: "GET", Label: "GET (download)"},
				{Value: "PUT", Label: "PUT (upload)"},
				{Value: "HEAD", Label: "HEAD (metadata)"},
			},
			Description: "HTTP method the URL will be valid for."},
		{Name: "expiry", Kind: connector.FieldString, Default: "1h",
			Description: "Go duration. Clamped to 7 days max (S3 limit)."},
	}}
}

func (a presignAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	presigner, ok := s.drv.(Presigner)
	if !ok {
		return nil, fmt.Errorf("presign: not supported by the %q backend", s.cred.Values.String(fBackend))
	}
	key := params.String("key")
	if key == "" {
		return nil, errors.New("key is required")
	}
	expiry, err := time.ParseDuration(params.String("expiry"))
	if err != nil {
		return nil, fmt.Errorf("expiry: %w", err)
	}
	u, err := presigner.Presign(ctx, key, expiry, params.String("method"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"key":    key,
		"method": params.String("method"),
		"expiry": expiry.String(),
		"url":    u,
	}, nil
}
