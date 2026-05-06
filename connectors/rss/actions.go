package rss

import (
	"context"
	"encoding/json"
	"time"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
)

// State store keys (per instance).
const (
	keyLastSeenID = "rss:last_seen_id"
	keyLastFetch  = "rss:last_fetched_at"
)

// loadWatermark returns the persisted last-seen item id, or "" if none.
func loadWatermark(ctx context.Context, st state.Store) (string, error) {
	if st == nil {
		return "", nil
	}
	data, ok, err := st.Get(ctx, keyLastSeenID)
	if err != nil || !ok {
		return "", err
	}
	return string(data), nil
}

func saveWatermark(ctx context.Context, st state.Store, id string) error {
	if st == nil || id == "" {
		return nil
	}
	if err := st.Put(ctx, keyLastSeenID, []byte(id)); err != nil {
		return err
	}
	stamp, _ := json.Marshal(time.Now().UTC().Format(time.RFC3339))
	return st.Put(ctx, keyLastFetch, stamp)
}

// ── fetch ─────────────────────────────────────────────────────────────────

type fetchAction struct{}

func (fetchAction) Name() string         { return "fetch" }
func (fetchAction) DisplayName() string  { return "Fetch feed" }
func (fetchAction) Description() string  { return "Fetch and parse the feed. Read-only — does NOT advance the watermark." }
func (fetchAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "limit", Kind: connector.FieldInt, Default: 0, Description: "Max items to return. 0 = all."},
	}}
}

func (a fetchAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	feed, err := fetchFeed(ctx, s.cred.Values)
	if err != nil {
		return nil, err
	}
	limit := params.Int("limit")
	items := feed.Items
	if limit > 0 && limit < len(items) {
		items = items[:limit]
	}
	flat := make([]map[string]any, 0, len(items))
	for _, it := range items {
		flat = append(flat, flattenItem(it))
	}
	return map[string]any{
		"feed":  flattenFeed(feed),
		"items": flat,
		"count": len(flat),
	}, nil
}

// ── list-new ──────────────────────────────────────────────────────────────

type listNewAction struct{}

func (listNewAction) Name() string         { return "list-new" }
func (listNewAction) DisplayName() string  { return "List new items" }
func (listNewAction) Description() string  { return "Atomic read + advance: returns items above the watermark, then sets the watermark to the newest. First call (no watermark) returns []." }
func (listNewAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "include_backlog", Kind: connector.FieldBool, Default: false,
			Description: "On first run (no watermark), return ALL feed items instead of skipping backlog. Subsequent runs unaffected."},
	}}
}

func (a listNewAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	feed, err := fetchFeed(ctx, s.cred.Values)
	if err != nil {
		return nil, err
	}
	watermark, err := loadWatermark(ctx, s.State())
	if err != nil {
		return nil, err
	}
	newItems, newWatermark := diffItems(feed, watermark, params.Bool("include_backlog"))
	if newWatermark != "" && newWatermark != watermark {
		if err := saveWatermark(ctx, s.State(), newWatermark); err != nil {
			return nil, err
		}
	}
	out := []map[string]any{}
	for _, it := range newItems {
		out = append(out, flattenItem(it))
	}
	return map[string]any{
		"new_count": len(out),
		"items":     out,
		"watermark": newWatermark,
	}, nil
}

// ── mark-seen ─────────────────────────────────────────────────────────────

type markSeenAction struct{}

func (markSeenAction) Name() string         { return "mark-seen" }
func (markSeenAction) DisplayName() string  { return "Mark seen" }
func (markSeenAction) Description() string  { return "Set the watermark to a specific id, or to the newest item in the current feed." }
func (markSeenAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldString, Description: "Specific item id to set as watermark. Omit to use newest item from current feed."},
	}}
}

func (a markSeenAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	id := params.String("id")
	if id == "" {
		feed, err := fetchFeed(ctx, s.cred.Values)
		if err != nil {
			return nil, err
		}
		if len(feed.Items) == 0 {
			return map[string]any{"watermark": "", "note": "feed has no items; watermark unchanged"}, nil
		}
		id = itemID(feed.Items[0])
	}
	if err := saveWatermark(ctx, s.State(), id); err != nil {
		return nil, err
	}
	return map[string]any{"watermark": id}, nil
}

// ── info ──────────────────────────────────────────────────────────────────

type infoAction struct{}

func (infoAction) Name() string         { return "info" }
func (infoAction) DisplayName() string  { return "Connector state info" }
func (infoAction) Description() string  { return "Report the persisted watermark and last-fetched timestamp without touching the feed." }
func (infoAction) Schema() connector.Schema { return connector.Schema{} }

func (infoAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	wm, _ := loadWatermark(ctx, s.State())
	out := map[string]any{
		"feed_url":  s.cred.Values.String(fFeedURL),
		"watermark": wm,
	}
	if s.State() != nil {
		if data, ok, _ := s.State().Get(ctx, keyLastFetch); ok {
			var t string
			_ = json.Unmarshal(data, &t)
			out["last_fetched_at"] = t
		}
	}
	return out, nil
}

// ── reset ─────────────────────────────────────────────────────────────────

type resetAction struct{}

func (resetAction) Name() string         { return "reset" }
func (resetAction) DisplayName() string  { return "Reset watermark" }
func (resetAction) Description() string  { return "Clear all persisted state. Next list-new starts fresh." }
func (resetAction) Schema() connector.Schema { return connector.Schema{} }

func (resetAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	if s.State() != nil {
		_ = s.State().Delete(ctx, keyLastSeenID)
		_ = s.State().Delete(ctx, keyLastFetch)
	}
	return map[string]any{"reset": true}, nil
}

