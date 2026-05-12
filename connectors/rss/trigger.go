package rss

import (
	"context"
	"errors"
	"time"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

type newItemsTrigger struct{}

func (newItemsTrigger) Name() string         { return "new-items" }
func (newItemsTrigger) DisplayName() string  { return "New items" }
func (newItemsTrigger) Description() string  { return "Polling trigger. Periodically fetches the feed and emits each new item as an Event. Persists watermark." }
func (newItemsTrigger) Strategy() connector.TriggerStrategy {
	return connector.TriggerPolling
}
func (newItemsTrigger) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "poll_interval", Kind: connector.FieldInt, Default: 300,
			Description: "Seconds between polls. Be polite: 300s (5m) is a reasonable floor for most feeds."},
		{Name: "include_backlog", Kind: connector.FieldBool, Default: false,
			Description: "On first run (no watermark), emit ALL current items. Subsequent runs unaffected."},
	}}
}

func (t newItemsTrigger) Subscribe(ctx context.Context, sess connector.Session, params connector.Values, emit connector.Sink) error {
	s, ok := sess.(*session)
	if !ok {
		return errors.New("rss new-items: wrong session type")
	}
	params = params.WithDefaults(t.Schema())
	interval := time.Duration(params.Int("poll_interval")) * time.Second
	if interval < 30*time.Second {
		interval = 30 * time.Second
	}
	includeBacklog := params.Bool("include_backlog")

	// First poll runs immediately; subsequent polls on the ticker.
	first := true
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	backoff := httptr.NewBackoff(time.Second, 5*time.Minute)

	for {
		if !first {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
		first = false

		feed, err := fetchFeed(ctx, s.cred.Values)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d := backoff.Next()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
			continue
		}
		backoff.Reset()

		watermark, _ := loadWatermark(ctx, s.State())
		fresh, newWatermark := diffItems(feed, watermark, includeBacklog)

		// Emit oldest-first so consumers see chronological order.
		for i := len(fresh) - 1; i >= 0; i-- {
			it := fresh[i]
			id := itemID(it)
			ev := connector.Event{
				ID:        id,
				Type:      "item",
				Payload:   flattenItem(it),
				Timestamp: itemTime(it).UTC(),
			}
			if err := emit(ctx, ev); err != nil {
				return err
			}
		}
		if newWatermark != "" && newWatermark != watermark {
			_ = saveWatermark(ctx, s.State(), newWatermark)
		}
		// First-run "include_backlog" only applies to the very first poll.
		includeBacklog = false
	}
}

