package jina

import (
	"io"
	stdhttp "net/http"
	"sort"
	"strings"
)

// readAllLimited is a small helper that drains up to limit bytes from r.
// Returns an error if the body exceeds limit (rather than silently
// truncating, which would silently corrupt the JSON parse).
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, &errTooLarge{Limit: limit}
	}
	return body, nil
}

type errTooLarge struct{ Limit int64 }

func (e *errTooLarge) Error() string {
	return "response body exceeds " + itoa(e.Limit) + " bytes"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// serializeHeaders builds a stable cache-key fragment from the per-call
// X-* headers. Sorted by name so a stable order is guaranteed regardless
// of map iteration.
func serializeHeaders(h stdhttp.Header) string {
	if len(h) == 0 {
		return ""
	}
	keys := make([]string, 0, len(h))
	for k := range h {
		if k == "Accept" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString(":")
		sb.WriteString(strings.Join(h.Values(k), ","))
		sb.WriteString(";")
	}
	return sb.String()
}
