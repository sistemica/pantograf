package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/state"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// Update is the bot-API update envelope. We pass-through whatever
// Telegram sends; payload is shaped by `allowed_updates`.
type Update struct {
	UpdateID      int64           `json:"update_id"`
	Message       map[string]any  `json:"message,omitempty"`
	EditedMessage map[string]any  `json:"edited_message,omitempty"`
	ChannelPost   map[string]any  `json:"channel_post,omitempty"`
	CallbackQuery map[string]any  `json:"callback_query,omitempty"`
	// Open-ended: anything else Telegram includes goes to Extras.
	Extras map[string]any `json:"-"`
}

// updateKind picks the first non-empty payload sub-field. Telegram
// guarantees exactly one of message/edited_message/channel_post/etc per
// update.
func (u Update) kind() (string, any) {
	switch {
	case u.Message != nil:
		return "message", u.Message
	case u.EditedMessage != nil:
		return "edited_message", u.EditedMessage
	case u.ChannelPost != nil:
		return "channel_post", u.ChannelPost
	case u.CallbackQuery != nil:
		return "callback_query", u.CallbackQuery
	default:
		return "unknown", nil
	}
}

// callGetUpdates is shared between the action (one-shot) and the trigger
// (long-poll loop). httpClient lets the trigger pass a long-timeout
// client; pass nil to use the session's default.
func callGetUpdates(ctx context.Context, cli *httptr.Client, offset int64, limit int, timeout int, allowed []string) ([]Update, error) {
	body := map[string]any{}
	if offset > 0 {
		body["offset"] = offset
	}
	if limit > 0 {
		body["limit"] = limit
	}
	if timeout > 0 {
		body["timeout"] = timeout
	}
	if len(allowed) > 0 {
		body["allowed_updates"] = allowed
	}
	var ups []Update
	if err := post(ctx, cli, "getUpdates", body, &ups); err != nil {
		return nil, err
	}
	return ups, nil
}

// ── get_updates action ────────────────────────────────────────────────────

type getUpdatesAction struct{}

func (getUpdatesAction) Name() string         { return "get-updates" }
func (getUpdatesAction) DisplayName() string  { return "Get updates" }
func (getUpdatesAction) Description() string  { return "Fetch pending updates once. For continuous streaming use the messages trigger." }
func (getUpdatesAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "offset", Label: "First update_id to return", Kind: connector.FieldInt,
			Description: "Pass last_seen+1 to acknowledge prior updates and only get newer ones."},
		{Name: "limit", Label: "Max updates", Kind: connector.FieldInt, Default: 100},
		{Name: "timeout", Label: "Long-poll timeout (seconds)", Kind: connector.FieldInt, Default: 0,
			Description: "0 = short poll. Server returns immediately. Use trigger for long-poll."},
		{Name: "allowed_updates", Label: "Filter update kinds", Kind: connector.FieldStringList,
			Description: "e.g. message,edited_message,callback_query"},
	}}
}

func (a getUpdatesAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s, ok := sess.(*session)
	if !ok {
		return nil, errors.New("get_updates: wrong session type")
	}
	params = params.WithDefaults(a.Schema())
	offset := int64(params.Int("offset"))
	limit := params.Int("limit")
	timeout := params.Int("timeout")
	allowed := params.StringList("allowed_updates")

	return callGetUpdates(ctx, s.http, offset, limit, timeout, allowed)
}

// ── messages trigger ──────────────────────────────────────────────────────

type messagesTrigger struct{}

func (messagesTrigger) Name() string         { return "messages" }
func (messagesTrigger) DisplayName() string  { return "Incoming messages" }
func (messagesTrigger) Description() string  { return "Long-poll Telegram for new messages, channel posts, and callback queries." }
func (messagesTrigger) Strategy() connector.TriggerStrategy {
	return connector.TriggerPolling
}
func (messagesTrigger) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "allowed_updates", Label: "Filter update kinds", Kind: connector.FieldStringList,
			Default: "message,edited_message,channel_post,callback_query"},
		{Name: "long_poll_timeout", Label: "Long-poll seconds", Kind: connector.FieldInt, Default: 25,
			Description: "Server holds the connection up to this long. Lower = more requests; higher = less responsive shutdown."},
		{Name: "start_offset", Label: "Initial offset (0 = pick up where we left off, -1 = only future updates)",
			Kind: connector.FieldInt, Default: 0},
	}}
}

func (t messagesTrigger) Subscribe(ctx context.Context, sess connector.Session, params connector.Values, emit connector.Sink) error {
	s, ok := sess.(*session)
	if !ok {
		return errors.New("messages: wrong session type")
	}
	params = params.WithDefaults(t.Schema())

	allowed := splitCSV(params.StringList("allowed_updates"))
	pollTimeout := params.Int("long_poll_timeout")
	if pollTimeout <= 0 {
		pollTimeout = 25
	}

	// A long-poll request must NOT be aborted by an HTTP client timeout
	// shorter than the server-side wait. Build a dedicated client whose
	// deadline is well past pollTimeout; ctx still drives shutdown.
	pollClient, err := httptr.New(httptr.Config{
		BaseURL: s.http.BaseURL(),
		Timeout: time.Duration(pollTimeout+15) * time.Second,
	})
	if err != nil {
		return err
	}

	offset := int64(params.Int("start_offset"))
	switch {
	case offset > 0:
		// Caller-provided offset wins.
	case offset == -1:
		// Drain backlog: documented Telegram pattern for "skip everything
		// queued, start fresh from now."
		ups, err := callGetUpdates(ctx, s.http, 0, 1, 0, nil)
		if err != nil {
			return fmt.Errorf("drain backlog: %w", err)
		}
		if len(ups) > 0 {
			offset = ups[0].UpdateID + 1
		}
		_, _ = callGetUpdates(ctx, s.http, offset, 1, 0, nil)
	default: // offset == 0 → resume from persisted state
		if persisted, ok, err := loadOffset(ctx, s.State()); err == nil && ok {
			offset = persisted
		}
	}

	backoff := newBackoff(time.Second, 30*time.Second)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		ups, err := callGetUpdates(ctx, pollClient, offset, 100, pollTimeout, allowed)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			d := backoff.next()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
			continue
		}
		backoff.reset()

		for _, u := range ups {
			kind, payload := u.kind()
			ev := connector.Event{
				ID:        strconv.FormatInt(u.UpdateID, 10),
				Type:      kind,
				Payload:   payload,
				Timestamp: time.Now().UTC(),
			}
			if err := emit(ctx, ev); err != nil {
				return err
			}
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
		}
		// Persist after each batch (not each event) — small, infrequent IO.
		// On crash we may re-deliver the in-flight batch; events carry
		// stable IDs, so consumers dedupe.
		if len(ups) > 0 {
			_ = saveOffset(ctx, s.State(), offset)
		}
	}
}

// ── offset persistence ────────────────────────────────────────────────────

const offsetKey = "trigger:messages:offset"

func loadOffset(ctx context.Context, st state.Store) (int64, bool, error) {
	if st == nil {
		return 0, false, nil
	}
	data, ok, err := st.Get(ctx, offsetKey)
	if err != nil || !ok {
		return 0, false, err
	}
	var v int64
	if err := json.Unmarshal(data, &v); err != nil {
		return 0, false, err
	}
	return v, true, nil
}

func saveOffset(ctx context.Context, st state.Store, offset int64) error {
	if st == nil {
		return nil
	}
	data, _ := json.Marshal(offset)
	return st.Put(ctx, offsetKey, data)
}

// splitCSV flattens both repeated -p flag input ("a", "b") and
// comma-merged input ("a,b") into a clean list with whitespace trimmed.
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

// backoff is a tiny exponential helper for long-poll error recovery.
// 1s → 2s → 4s → 8s → 16s → 30s (capped).
type backoff struct {
	cur, init, max time.Duration
}

func newBackoff(initial, max time.Duration) *backoff {
	return &backoff{cur: initial, init: initial, max: max}
}

func (b *backoff) next() time.Duration {
	d := b.cur
	b.cur *= 2
	if b.cur > b.max {
		b.cur = b.max
	}
	return d
}

func (b *backoff) reset() { b.cur = b.init }
