package rss

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/sistemica/pantograf/connector"
)

// fetchFeed pulls and parses one feed. The User-Agent and timeout come
// from the credential — some feeds reject default Go UA, others rate-limit.
func fetchFeed(ctx context.Context, v connector.Values) (*gofeed.Feed, error) {
	url := v.String(fFeedURL)
	if url == "" {
		return nil, fmt.Errorf("rss: feed_url is empty")
	}
	timeout := time.Duration(v.Int(fTimeout)) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ua := v.String(fUA)
	if ua == "" {
		ua = "pantograf/0.1"
	}

	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("rss: build request: %w", err)
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/json, application/xml;q=0.9, */*;q=0.8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rss: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("rss: GET %s: http %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("rss: read body: %w", err)
	}
	parser := gofeed.NewParser()
	feed, err := parser.ParseString(string(body))
	if err != nil {
		return nil, fmt.Errorf("rss: parse: %w", err)
	}
	return feed, nil
}

// itemID returns a stable id for a feed item. Priority: GUID > Link >
// SHA-256 of (Title + PubDate). Falls back to empty string if nothing
// works, which the watermark logic treats as "skip — no stable id".
func itemID(it *gofeed.Item) string {
	if it == nil {
		return ""
	}
	if it.GUID != "" {
		return it.GUID
	}
	if it.Link != "" {
		return it.Link
	}
	if it.Title != "" || it.Published != "" {
		h := sha256.Sum256([]byte(it.Title + "|" + it.Published))
		return "h:" + hex.EncodeToString(h[:8]) // 8 bytes is plenty for dedup
	}
	return ""
}

// itemTime returns the best-effort timestamp for ordering, falling back
// to "now" when nothing is parseable.
func itemTime(it *gofeed.Item) time.Time {
	if it == nil {
		return time.Now().UTC()
	}
	if it.PublishedParsed != nil {
		return *it.PublishedParsed
	}
	if it.UpdatedParsed != nil {
		return *it.UpdatedParsed
	}
	return time.Now().UTC()
}

// flattenItem turns a *gofeed.Item into a JSON-friendly map. We do this
// rather than passing the gofeed type through so the API stays library-
// version-stable and consumers see a predictable shape.
func flattenItem(it *gofeed.Item) map[string]any {
	if it == nil {
		return nil
	}
	out := map[string]any{
		"id":        itemID(it),
		"title":     it.Title,
		"link":      it.Link,
		"published": "",
	}
	if it.PublishedParsed != nil {
		out["published"] = it.PublishedParsed.UTC().Format(time.RFC3339)
	} else if it.UpdatedParsed != nil {
		out["published"] = it.UpdatedParsed.UTC().Format(time.RFC3339)
	}
	if it.Description != "" {
		out["description"] = it.Description
	}
	if it.Content != "" {
		out["content"] = it.Content
	}
	if len(it.Authors) > 0 && it.Authors[0] != nil {
		out["author"] = it.Authors[0].Name
	}
	if len(it.Categories) > 0 {
		out["categories"] = it.Categories
	}
	return out
}

// flattenFeed extracts feed-level metadata.
func flattenFeed(feed *gofeed.Feed) map[string]any {
	if feed == nil {
		return nil
	}
	return map[string]any{
		"title":       feed.Title,
		"description": feed.Description,
		"link":        feed.Link,
		"language":    feed.Language,
		"feed_type":   feed.FeedType,
	}
}
