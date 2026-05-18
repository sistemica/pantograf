package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// cacheEntry is what gets serialised into the state store. Keep it small —
// no headers besides Content-Type, no chain history. The body is the raw
// response (HTML, usually) returned by either the HTTP path or the CDP path.
type cacheEntry struct {
	FetchedAt   time.Time `json:"fetched_at"`
	Status      int       `json:"status"`
	ContentType string    `json:"content_type"`
	FinalURL    string    `json:"final_url"`
	Body        string    `json:"body"`
	JS          bool      `json:"js"`
}

// cacheKey is the disk-key for a given (url, js, user_agent) combination.
// Different js modes or different UAs can produce different content, so
// they get separate entries.
func cacheKey(url string, js bool, userAgent string) string {
	h := sha256.New()
	h.Write([]byte(url))
	h.Write([]byte{0})
	if js {
		h.Write([]byte("js"))
	} else {
		h.Write([]byte("http"))
	}
	h.Write([]byte{0})
	h.Write([]byte(userAgent))
	return "page:" + hex.EncodeToString(h.Sum(nil))[:32]
}

// readCache returns (entry, ok) if a fresh entry exists in the state store.
// "Fresh" = fetched_at within ttl. ttl == 0 disables the cache (always
// miss). A negative ttl is treated as 0.
func readCache(ctx context.Context, s *session, key string, ttl time.Duration) (*cacheEntry, bool) {
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
	return &e, true
}

func writeCache(ctx context.Context, s *session, key string, e *cacheEntry) {
	if s.state == nil {
		return
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	_ = s.state.Put(ctx, key, raw)
}

// parseTTL accepts Go duration strings ("5m", "30s", "1h", "0") and a
// blank string (default 5m). Negative or invalid → 0 (disabled).
func parseTTL(s string) time.Duration {
	if s == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0
	}
	return d
}
