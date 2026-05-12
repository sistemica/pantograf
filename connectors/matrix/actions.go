package matrix

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// resolveRoomID accepts either a room ID (`!abc:server`) or alias
// (`#room:server`) and returns the canonical ID. Aliases are resolved
// via the directory endpoint.
func resolveRoomID(ctx context.Context, cli *httptr.Client, idOrAlias string) (string, error) {
	if idOrAlias == "" {
		return "", errors.New("room id/alias required")
	}
	if strings.HasPrefix(idOrAlias, "!") {
		return idOrAlias, nil
	}
	if !strings.HasPrefix(idOrAlias, "#") {
		return "", fmt.Errorf("room reference must start with '!' or '#', got %q", idOrAlias)
	}
	var resp struct {
		RoomID string `json:"room_id"`
	}
	path := "/_matrix/client/v3/directory/room/" + url.PathEscape(idOrAlias)
	if err := cli.GetJSON(ctx, path, nil, &resp); err != nil {
		return "", fmt.Errorf("resolve alias %q: %w", idOrAlias, err)
	}
	return resp.RoomID, nil
}

// ── whoami ────────────────────────────────────────────────────────────────

type whoamiAction struct{}

func (whoamiAction) Name() string         { return "whoami" }
func (whoamiAction) DisplayName() string  { return "Who am I" }
func (whoamiAction) Description() string  { return "Identity of the bound access token: user_id + device_id." }
func (whoamiAction) Schema() connector.Schema { return connector.Schema{} }

func (whoamiAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/_matrix/client/v3/account/whoami", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── list-rooms ────────────────────────────────────────────────────────────
//
// /joined_rooms returns IDs only. We enrich each with the canonical alias
// + name from /rooms/{id}/state — at the cost of one HTTP per room, which
// is fine for typical user-scale (dozens of rooms, not thousands).

type listRoomsAction struct{}

func (listRoomsAction) Name() string         { return "list-rooms" }
func (listRoomsAction) DisplayName() string  { return "List joined rooms" }
func (listRoomsAction) Description() string  { return "Rooms this account has joined. Optionally enriched with name and alias." }
func (listRoomsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "enrich", Kind: connector.FieldBool, Default: true,
			Description: "Look up room name and canonical alias for each. Costs 1 HTTP/room. Disable for raw IDs only."},
	}}
}

func (a listRoomsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	var resp struct {
		JoinedRooms []string `json:"joined_rooms"`
	}
	if err := s.http.GetJSON(ctx, "/_matrix/client/v3/joined_rooms", nil, &resp); err != nil {
		return nil, err
	}
	if !params.Bool("enrich") {
		return resp.JoinedRooms, nil
	}
	out := make([]map[string]any, 0, len(resp.JoinedRooms))
	for _, id := range resp.JoinedRooms {
		entry := map[string]any{"room_id": id}
		if name, alias := lookupRoomNameAlias(ctx, s.http, id); name != "" || alias != "" {
			entry["name"] = name
			entry["alias"] = alias
		}
		out = append(out, entry)
	}
	return out, nil
}

// lookupRoomNameAlias returns (name, canonical_alias). Best-effort —
// errors swallowed; a missing state event yields "".
func lookupRoomNameAlias(ctx context.Context, cli *httptr.Client, roomID string) (string, string) {
	var nameEv struct {
		Name string `json:"name"`
	}
	_ = cli.GetJSON(ctx, "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/state/m.room.name/", nil, &nameEv)

	var aliasEv struct {
		Alias string `json:"alias"`
	}
	_ = cli.GetJSON(ctx, "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/state/m.room.canonical_alias/", nil, &aliasEv)

	return nameEv.Name, aliasEv.Alias
}

// ── get-room ──────────────────────────────────────────────────────────────

type getRoomAction struct{}

func (getRoomAction) Name() string         { return "get-room" }
func (getRoomAction) DisplayName() string  { return "Get room info" }
func (getRoomAction) Description() string  { return "Room state: name, topic, alias, members, encryption status." }
func (getRoomAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "room", Kind: connector.FieldString, Required: true, Description: "Room id (!...) or alias (#...)."},
	}}
}

func (a getRoomAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	roomID, err := resolveRoomID(ctx, s.http, params.String("room"))
	if err != nil {
		return nil, err
	}
	out := map[string]any{"room_id": roomID}

	// Bundle a few common state events.
	for _, ev := range []string{"m.room.name", "m.room.topic", "m.room.canonical_alias", "m.room.encryption", "m.room.create"} {
		var raw map[string]any
		if err := s.http.GetJSON(ctx, "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/state/"+url.PathEscape(ev)+"/", nil, &raw); err == nil {
			out[ev] = raw
		}
	}

	// Joined member count + sample.
	var members struct {
		Joined map[string]any `json:"joined"`
	}
	if err := s.http.GetJSON(ctx, "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/joined_members", nil, &members); err == nil {
		ids := make([]string, 0, len(members.Joined))
		for id := range members.Joined {
			ids = append(ids, id)
		}
		out["joined_members_count"] = len(ids)
		out["joined_members"] = ids
	}
	return out, nil
}

// ── send-message ──────────────────────────────────────────────────────────

type sendMessageAction struct{}

func (sendMessageAction) Name() string         { return "send-message" }
func (sendMessageAction) DisplayName() string  { return "Send message" }
func (sendMessageAction) Description() string  { return "Send a text message to a room. Supports plain or HTML (formatted) body." }
func (sendMessageAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "room", Kind: connector.FieldString, Required: true, Description: "Room id (!...) or alias (#...)."},
		{Name: "body", Kind: connector.FieldLongText, Required: true, Description: "Plain-text body (also used as fallback for HTML clients)."},
		{Name: "html", Kind: connector.FieldLongText, Description: "Optional HTML body. When set, msgtype becomes m.text with format org.matrix.custom.html."},
		{Name: "msgtype", Kind: connector.FieldString, Default: "m.text", Description: "m.text | m.notice | m.emote."},
	}}
}

func (a sendMessageAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	roomID, err := resolveRoomID(ctx, s.http, params.String("room"))
	if err != nil {
		return nil, err
	}
	body := params.String("body")
	if body == "" {
		return nil, errors.New("body is required")
	}
	content := map[string]any{
		"msgtype": params.String("msgtype"),
		"body":    body,
	}
	if html := params.String("html"); html != "" {
		content["format"] = "org.matrix.custom.html"
		content["formatted_body"] = html
	}

	// Idempotency key — Matrix uses txnId so retries don't double-send.
	txn := s.txn.Add(1)
	txnID := fmt.Sprintf("pgf-%d-%d", time.Now().UnixNano(), txn)

	path := "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) +
		"/send/m.room.message/" + url.PathEscape(txnID)

	var resp struct {
		EventID string `json:"event_id"`
	}
	// PUT, not POST — Matrix sends are idempotent PUTs keyed on txnId.
	if err := s.http.SendJSON(ctx, "PUT", path, content, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"event_id": resp.EventID,
		"room_id":  roomID,
	}, nil
}

// ── get-messages ──────────────────────────────────────────────────────────

type getMessagesAction struct{}

func (getMessagesAction) Name() string         { return "get-messages" }
func (getMessagesAction) DisplayName() string  { return "Get room messages" }
func (getMessagesAction) Description() string  { return "Recent messages in a room (timeline, default newest-first)." }
func (getMessagesAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "room", Kind: connector.FieldString, Required: true},
		{Name: "limit", Kind: connector.FieldInt, Default: 20, Description: "Max events to return."},
		{Name: "dir", Kind: connector.FieldEnum, Default: "b",
			Options: []connector.EnumOption{
				{Value: "b", Label: "Backward (newest first)"},
				{Value: "f", Label: "Forward (oldest first)"},
			}},
		{Name: "from", Kind: connector.FieldString, Description: "Pagination cursor; pass `end` from a previous response."},
	}}
}

func (a getMessagesAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	roomID, err := resolveRoomID(ctx, s.http, params.String("room"))
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("dir", params.String("dir"))
	q.Set("limit", fmt.Sprintf("%d", params.Int("limit")))
	if from := params.String("from"); from != "" {
		q.Set("from", from)
	}
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/_matrix/client/v3/rooms/"+url.PathEscape(roomID)+"/messages", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}
