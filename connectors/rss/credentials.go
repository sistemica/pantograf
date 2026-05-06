package rss

import (
	"context"
	"errors"
	"fmt"

	"github.com/sistemica/pantograf/connector"
)

const (
	fFeedURL = "feed_url"
	fUA      = "user_agent"
	fTimeout = "timeout_seconds"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthNone }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name:        fFeedURL,
			Label:       "Feed URL",
			Kind:        connector.FieldString,
			Required:    true,
			Description: "RSS 2.0, RSS 1.0, Atom, or JSON Feed. e.g. https://news.ycombinator.com/rss",
		},
		{
			Name:    fUA,
			Label:   "User-Agent header",
			Kind:    connector.FieldString,
			Default: "pantograf/0.1 (+https://github.com/sistemica/pantograf)",
		},
		{
			Name:    fTimeout,
			Label:   "HTTP timeout (seconds)",
			Kind:    connector.FieldInt,
			Default: 30,
		},
	}}
}

func (credSpec) Presets() []connector.Preset { return nil }

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// Validate fetches and parses the feed once. Confirms it's reachable, the
// content type is parseable, and the URL is what the user thinks.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	if c.Values.String(fFeedURL) == "" {
		return errors.New("feed_url is required")
	}
	feed, err := fetchFeed(ctx, c.Values)
	if err != nil {
		return err
	}
	if feed != nil && feed.Title != "" {
		fmt.Printf("(%s) ", feed.Title)
	}
	return nil
}
