package file

import (
	"context"
	"io"
	"time"
)

// Entry is the normalised metadata for one stored object across all
// backends. Local files and S3 objects both produce this shape so action
// output is identical regardless of where the data lives.
type Entry struct {
	Key          string    `json:"key"`           // backend-relative identifier (path or s3 key)
	Size         int64     `json:"size"`          // bytes
	LastModified time.Time `json:"last_modified"` // UTC
	IsDir        bool      `json:"is_dir,omitempty"`
	ContentType  string    `json:"content_type,omitempty"` // when the backend knows
	ETag         string    `json:"etag,omitempty"`         // when the backend reports one
}

// Match is one grep-style hit inside a file. Only the local backend
// produces these today; S3 returns "not supported" — see Searcher.
type Match struct {
	Key       string   `json:"key"`
	Line      int      `json:"line"`            // 1-indexed
	Text      string   `json:"text"`            // the matching line, trimmed of trailing \r\n
	Before    []string `json:"before,omitempty"` // context lines preceding (newest first → oldest)
	After     []string `json:"after,omitempty"`  // context lines following
	ByteOffset int64   `json:"byte_offset"`     // start of the matching line in the file
}

// driver is the internal capability every backend implements. Actions
// only see this interface, so adding a new backend is "drop in a file
// implementing it and switch on the credential's backend field."
type driver interface {
	List(ctx context.Context, prefix string, recursive bool, limit int) ([]Entry, error)
	Stat(ctx context.Context, key string) (Entry, error)
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Entry, error)
	Delete(ctx context.Context, key string) error
}

// Searcher is the optional capability for backends that can grep their
// own storage cheaply. Local implements it; S3 doesn't (downloading
// every object to grep is expensive enough that we want the agent to
// pick that explicitly, not stumble into it).
type Searcher interface {
	Search(ctx context.Context, opts SearchOpts) (SearchResult, error)
}

// Presigner is the optional capability for backends that can issue
// time-limited shareable URLs to objects. S3 implements it; local
// doesn't.
type Presigner interface {
	Presign(ctx context.Context, key string, expiry time.Duration, method string) (string, error)
}

// SearchOpts mirrors the action schema for searchAction. Kept here so
// the driver interface doesn't leak the action layer's shape but stays
// in sync with it.
type SearchOpts struct {
	Prefix          string
	Pattern         string
	Include         string // glob applied to the basename
	Exclude         string // glob applied to the basename
	MaxSize         int64  // skip files larger than this; 0 = no limit
	MaxMatches      int    // cap on returned matches; 0 = no cap
	Before          int    // context lines before each match
	After           int    // context lines after each match
	CaseInsensitive bool
}

// SearchResult aggregates the matches plus a few statistics. The action
// layer surfaces these so an agent can see how thorough the scan was.
type SearchResult struct {
	Matches []Match `json:"matches"`
	Count   int     `json:"count"`
	Scanned int     `json:"scanned"` // files actually grep'd
	Skipped int     `json:"skipped"` // files skipped (too large, exclude glob, binary detect)
}
