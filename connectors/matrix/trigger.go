package matrix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// messagesTrigger long-polls /sync, persists next_batch to state, and emits
// each new timeline event as a connector.Event. Same shape as Telegram's
// messages trigger — drop-in fanout into the same NDJSON consumer.
type messagesTrigger struct{}

func (messagesTrigger) Name() string         { return "messages" }
func (messagesTrigger) DisplayName() string  { return "Incoming messages" }
func (messagesTrigger) Description() string  { return "Long-poll Matrix /sync. Emits timeline events as they arrive. Filter by event type." }
func (messagesTrigger) Strategy() connector.TriggerStrategy {
	return connector.TriggerPolling
}
func (messagesTrigger) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "filter_types", Kind: connector.FieldStringList, Default: "m.room.message",
			Description: "Comma-separated event types to emit. Empty = all (member joins, typing, etc.)."},
		{Name: "long_poll_timeout", Kind: connector.FieldInt, Default: 30,
			Description: "Server-side wait, seconds. Higher = fewer requests; lower = more responsive shutdown."},
		{Name: "start_offset", Kind: connector.FieldInt, Default: 0,
			Description: "0 = resume from persisted next_batch (first run skips backlog). -1 = same as 0. Reserved for future modes."},
	}}
}

const nextBatchKey = "trigger:messages:next_batch"

func (t messagesTrigger) Subscribe(ctx context.Context, sess connector.Session, params connector.Values, emit connector.Sink) error {
	s, ok := sess.(*session)
	if !ok {
		return errors.New("matrix messages: wrong session type")
	}
	params = params.WithDefaults(t.Schema())

	filterTypes := splitCSV(params.StringList("filter_types"))
	pollTimeoutSec := params.Int("long_poll_timeout")
	if pollTimeoutSec <= 0 {
		pollTimeoutSec = 30
	}

	// Dedicated long-poll client. ctx still drives shutdown, but the
	// per-request deadline has to outlive the server-side wait.
	pollClient, err := httptr.New(httptr.Config{
		BaseURL: s.http.BaseURL(),
		Headers: s.http.Headers(),
		Timeout: time.Duration(pollTimeoutSec+15) * time.Second,
	})
	if err != nil {
		return err
	}

	// Resume from persisted next_batch.
	nextBatch, _ := loadNextBatch(ctx, s.State())

	// On a brand-new instance (no next_batch yet) we don't want to dump
	// the entire backlog. Do one short-poll sync with timeline.limit=0
	// to mint a starting batch and emit nothing.
	if nextBatch == "" {
		seed, err := callSync(ctx, pollClient, "", 0, true)
		if err != nil {
			return fmt.Errorf("seed sync: %w", err)
		}
		nextBatch = seed.NextBatch
		_ = saveNextBatch(ctx, s.State(), nextBatch)
	}

	backoff := httptr.NewBackoff(time.Second, 5*time.Minute)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := callSync(ctx, pollClient, nextBatch, pollTimeoutSec*1000, false)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff.Next()):
			}
			continue
		}
		backoff.Reset()

		for roomID, room := range resp.Rooms.Join {
			for _, ev := range room.Timeline.Events {
				if len(filterTypes) > 0 && !contains(filterTypes, ev.Type) {
					continue
				}
				payload := map[string]any{
					"room_id":          roomID,
					"event_id":         ev.EventID,
					"type":             ev.Type,
					"sender":           ev.Sender,
					"origin_server_ts": ev.OriginServerTS,
					"content":          ev.Content,
				}
				if err := emit(ctx, connector.Event{
					ID:        ev.EventID,
					Type:      ev.Type,
					Payload:   payload,
					Timestamp: time.UnixMilli(ev.OriginServerTS).UTC(),
				}); err != nil {
					return err
				}
			}
		}

		nextBatch = resp.NextBatch
		_ = saveNextBatch(ctx, s.State(), nextBatch)
	}
}

// ── /sync response (subset) ───────────────────────────────────────────────

type syncResp struct {
	NextBatch string `json:"next_batch"`
	Rooms     struct {
		Join map[string]struct {
			Timeline struct {
				Events []syncEvent `json:"events"`
				Prev   string      `json:"prev_batch"`
				Limited bool       `json:"limited"`
			} `json:"timeline"`
		} `json:"join"`
	} `json:"rooms"`
}

type syncEvent struct {
	EventID        string         `json:"event_id"`
	Type           string         `json:"type"`
	Sender         string         `json:"sender"`
	OriginServerTS int64          `json:"origin_server_ts"`
	Content        map[string]any `json:"content"`
}

// callSync calls /_matrix/client/v3/sync. When seed=true the request uses
// a tight filter (timeline.limit=0) and short timeout — used once at the
// start of a fresh subscription to mint a watermark without emitting
// historical events.
func callSync(ctx context.Context, cli *httptr.Client, since string, timeoutMs int, seed bool) (*syncResp, error) {
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if timeoutMs > 0 {
		q.Set("timeout", fmt.Sprintf("%d", timeoutMs))
	}
	if seed {
		// Limit timeline to zero events on the seed sync — we just want
		// the next_batch cursor.
		q.Set("filter", `{"room":{"timeline":{"limit":0}}}`)
	}
	var resp syncResp
	if err := cli.GetJSON(ctx, "/_matrix/client/v3/sync", q, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ── watermark persistence ─────────────────────────────────────────────────

func loadNextBatch(ctx context.Context, st state.Store) (string, error) {
	if st == nil {
		return "", nil
	}
	data, ok, err := st.Get(ctx, nextBatchKey)
	if err != nil || !ok {
		return "", err
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		// Older write was raw string; tolerate.
		return string(data), nil
	}
	return s, nil
}

func saveNextBatch(ctx context.Context, st state.Store, v string) error {
	if st == nil || v == "" {
		return nil
	}
	data, _ := json.Marshal(v)
	return st.Put(ctx, nextBatchKey, data)
}

// ── helpers ───────────────────────────────────────────────────────────────

func splitCSV(in []string) []string {
	var out []string
	for _, s := range in {
		for _, p := range strings.Split(s, ",") {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
