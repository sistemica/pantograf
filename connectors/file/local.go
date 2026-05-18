package file

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// localDriver serves files from a directory tree on the host. Keys are
// `/`-separated paths relative to `root`. Absolute / traversal-escape
// keys are rejected — the root is the boundary.
type localDriver struct {
	root string
}

func newLocalDriver(root string) (*localDriver, error) {
	if root == "" {
		return nil, errors.New("local: root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("local: resolve root: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("local: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local: root %q is not a directory", abs)
	}
	return &localDriver{root: abs}, nil
}

// resolveKey joins root + key, then verifies the resulting path stays
// under root. Returns an error on traversal (`../../etc/passwd`).
func (d *localDriver) resolveKey(key string) (string, error) {
	clean := strings.TrimPrefix(key, "/")
	if clean == "" {
		return d.root, nil
	}
	full := filepath.Join(d.root, clean)
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(d.root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("key %q escapes root", key)
	}
	return abs, nil
}

// relKey converts an absolute path back into a backend key (slash-form,
// root-relative). Returns "" for the root itself.
func (d *localDriver) relKey(abs string) string {
	rel, err := filepath.Rel(d.root, abs)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

func (d *localDriver) List(ctx context.Context, prefix string, recursive bool, limit int) ([]Entry, error) {
	base, err := d.resolveKey(prefix)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // empty prefix → empty list, not an error
		}
		return nil, err
	}
	var out []Entry
	if !info.IsDir() {
		// Prefix points at a single file — return the one entry.
		return []Entry{{
			Key:          d.relKey(base),
			Size:         info.Size(),
			LastModified: info.ModTime().UTC(),
			ContentType:  guessMime(base),
		}}, nil
	}

	if recursive {
		walkErr := filepath.WalkDir(base, func(p string, dirent os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if p == base {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			info, err := dirent.Info()
			if err != nil {
				return nil // skip transient stat errors during walk
			}
			out = append(out, Entry{
				Key:          d.relKey(p),
				Size:         info.Size(),
				LastModified: info.ModTime().UTC(),
				IsDir:        dirent.IsDir(),
				ContentType:  guessMime(p),
			})
			if limit > 0 && len(out) >= limit {
				return filepath.SkipAll
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
			return out, walkErr
		}
	} else {
		entries, err := os.ReadDir(base)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, Entry{
				Key:          d.relKey(filepath.Join(base, e.Name())),
				Size:         info.Size(),
				LastModified: info.ModTime().UTC(),
				IsDir:        e.IsDir(),
				ContentType:  guessMime(e.Name()),
			})
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (d *localDriver) Stat(ctx context.Context, key string) (Entry, error) {
	p, err := d.resolveKey(key)
	if err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	return Entry{
		Key:          d.relKey(p),
		Size:         info.Size(),
		LastModified: info.ModTime().UTC(),
		IsDir:        info.IsDir(),
		ContentType:  guessMime(p),
	}, nil
}

func (d *localDriver) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	p, err := d.resolveKey(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

func (d *localDriver) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (Entry, error) {
	p, err := d.resolveKey(key)
	if err != nil {
		return Entry{}, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return Entry{}, err
	}
	tmp := p + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return Entry{}, err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return Entry{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return Entry{}, err
	}
	if err := os.Rename(tmp, p); err != nil {
		return Entry{}, err
	}
	return d.Stat(ctx, d.relKey(p))
}

func (d *localDriver) Delete(ctx context.Context, key string) error {
	p, err := d.resolveKey(key)
	if err != nil {
		return err
	}
	if p == d.root {
		return errors.New("refusing to delete the backend root")
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil // idempotent
	}
	return err
}

// Search implements grep-style content search across the local tree.
// Honours include/exclude globs (basename), max_size (skip-large), and
// streams matches with optional context lines.
func (d *localDriver) Search(ctx context.Context, opts SearchOpts) (SearchResult, error) {
	if opts.Pattern == "" {
		return SearchResult{}, errors.New("search: pattern is required")
	}
	expr := opts.Pattern
	if opts.CaseInsensitive {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return SearchResult{}, fmt.Errorf("compile pattern: %w", err)
	}

	base, err := d.resolveKey(opts.Prefix)
	if err != nil {
		return SearchResult{}, err
	}
	info, err := os.Stat(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SearchResult{}, nil
		}
		return SearchResult{}, err
	}

	var result SearchResult
	files := []string{}
	if info.IsDir() {
		_ = filepath.WalkDir(base, func(p string, dirent os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if dirent.IsDir() {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			name := dirent.Name()
			if opts.Include != "" {
				if ok, _ := filepath.Match(opts.Include, name); !ok {
					result.Skipped++
					return nil
				}
			}
			if opts.Exclude != "" {
				if ok, _ := filepath.Match(opts.Exclude, name); ok {
					result.Skipped++
					return nil
				}
			}
			info, err := dirent.Info()
			if err != nil {
				result.Skipped++
				return nil
			}
			if opts.MaxSize > 0 && info.Size() > opts.MaxSize {
				result.Skipped++
				return nil
			}
			files = append(files, p)
			return nil
		})
	} else {
		files = append(files, base)
	}

	for _, fp := range files {
		if ctx.Err() != nil {
			break
		}
		if opts.MaxMatches > 0 && result.Count >= opts.MaxMatches {
			break
		}
		matches, scanned, err := scanFile(fp, d.relKey(fp), re, opts)
		if err != nil {
			result.Skipped++
			continue
		}
		if !scanned {
			result.Skipped++
			continue
		}
		result.Scanned++
		for _, m := range matches {
			if opts.MaxMatches > 0 && result.Count >= opts.MaxMatches {
				break
			}
			result.Matches = append(result.Matches, m)
			result.Count++
		}
	}
	return result, nil
}

// scanFile reads one file line-by-line, finds matches, and assembles
// context windows. Returns (matches, scanned, err). `scanned=false`
// means the file was skipped for a content reason (e.g. binary).
func scanFile(absPath, key string, re *regexp.Regexp, opts SearchOpts) ([]Match, bool, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()

	// Cheap binary detection: peek the first 512 bytes and skip if
	// they look non-text. Avoids spending CPU scanning images and tarballs.
	peek := make([]byte, 512)
	n, _ := f.Read(peek)
	if n > 0 && isLikelyBinary(peek[:n]) {
		return nil, false, nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var (
		matches []Match
		buf     []string // rolling window of recent lines (for Before context)
		queued  []*Match // matches still gathering After context
		offset  int64
		lineNo  int
	)
	if opts.Before > 0 {
		buf = make([]string, 0, opts.Before)
	}

	flushQueued := func() {
		for _, m := range queued {
			matches = append(matches, *m)
		}
		queued = queued[:0]
	}

	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		// Drain After-context for any queued matches.
		for _, q := range queued {
			q.After = append(q.After, line)
		}
		complete := queued[:0]
		for _, q := range queued {
			if len(q.After) >= opts.After {
				matches = append(matches, *q)
			} else {
				complete = append(complete, q)
			}
		}
		queued = complete

		if re.MatchString(line) {
			m := Match{
				Key:        key,
				Line:       lineNo,
				Text:       line,
				ByteOffset: offset,
			}
			if opts.Before > 0 {
				m.Before = append(m.Before, buf...)
			}
			if opts.After > 0 {
				cp := m
				queued = append(queued, &cp)
			} else {
				matches = append(matches, m)
			}
			if opts.MaxMatches > 0 && len(matches)+len(queued) >= opts.MaxMatches {
				break
			}
		}

		if opts.Before > 0 {
			if len(buf) == opts.Before {
				buf = buf[1:]
			}
			buf = append(buf, line)
		}
		offset += int64(len(line)) + 1
	}
	flushQueued()
	if err := scanner.Err(); err != nil {
		// Long-line or read error — return what we have plus the error
		// so the caller decides to skip-count or surface.
		return matches, true, err
	}
	return matches, true, nil
}

// isLikelyBinary returns true when a byte sample contains NULs or a high
// ratio of non-printable bytes. Same heuristic git uses.
func isLikelyBinary(b []byte) bool {
	if bytes.IndexByte(b, 0) >= 0 {
		return true
	}
	nonPrintable := 0
	for _, c := range b {
		if c < 9 || (c > 13 && c < 32) {
			nonPrintable++
		}
	}
	return nonPrintable*100/len(b) > 30
}

// guessMime returns a Content-Type for a filename using mime.TypeByExtension
// with a couple of common-extension fallbacks.
func guessMime(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return ""
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	// mime stdlib doesn't ship a few we care about:
	switch ext {
	case ".md", ".markdown":
		return "text/markdown"
	case ".yaml", ".yml":
		return "application/yaml"
	case ".log":
		return "text/plain"
	}
	return ""
}

