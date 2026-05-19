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

// ── create-room ───────────────────────────────────────────────────────────
//
// POST /createRoom is the universal entry point for new rooms AND spaces.
// Same shape; spaces just set creation_content.type = "m.space".
// `create-space` below is a thin wrapper that enforces that.

type createRoomAction struct{}

func (createRoomAction) Name() string         { return "create-room" }
func (createRoomAction) DisplayName() string  { return "Create a room" }
func (createRoomAction) Description() string  { return "Create a new Matrix room. Optionally invite users at creation time. Preset shorthand for the common visibility/join-rule combinations." }
func (createRoomAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Kind: connector.FieldString,
			Description: "Display name for the room. Optional but recommended."},
		{Name: "topic", Kind: connector.FieldString,
			Description: "Room topic / description shown in clients."},
		{Name: "alias", Kind: connector.FieldString,
			Description: "Local part of the room alias (just the bit before the colon). Becomes #<alias>:<homeserver>."},
		{Name: "preset", Kind: connector.FieldEnum, Default: "private_chat",
			Options: []connector.EnumOption{
				{Value: "private_chat", Label: "Private chat — invite-only, history off"},
				{Value: "trusted_private_chat", Label: "Trusted private chat — invite-only, all invitees get power level 100"},
				{Value: "public_chat", Label: "Public chat — anyone can join, history on"},
			},
			Description: "Convenience for visibility + join-rule + history-visibility combos."},
		{Name: "invite", Kind: connector.FieldStringList,
			Description: "Matrix user IDs to invite at creation. e.g. @alice:matrix.example.com,@bob:matrix.example.com."},
		{Name: "encrypted", Kind: connector.FieldBool, Default: false,
			Description: "Enable E2EE on the room at creation. Once enabled it cannot be turned off."},
		{Name: "topic_html", Kind: connector.FieldString,
			Description: "Optional HTML-formatted topic. Ignored by older clients."},
	}}
}

func (a createRoomAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	body := buildCreateRoomBody(params, false)
	var resp struct {
		RoomID    string `json:"room_id"`
		RoomAlias string `json:"room_alias,omitempty"`
	}
	if err := s.http.SendJSON(ctx, "POST", "/_matrix/client/v3/createRoom", body, &resp); err != nil {
		return nil, err
	}
	out := map[string]any{"room_id": resp.RoomID}
	if resp.RoomAlias != "" {
		out["room_alias"] = resp.RoomAlias
	}
	return out, nil
}

// ── create-space ──────────────────────────────────────────────────────────
//
// A Space is a room with creation_content.type == "m.space". Clients
// render it as a folder/grouping for child rooms. After creating the
// space, use add-room-to-space to attach existing rooms.

type createSpaceAction struct{}

func (createSpaceAction) Name() string         { return "create-space" }
func (createSpaceAction) DisplayName() string  { return "Create a space" }
func (createSpaceAction) Description() string  { return "Create a Matrix space (a room with type=m.space). Use add-room-to-space afterwards to attach existing rooms as children." }
func (createSpaceAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Kind: connector.FieldString,
			Description: "Display name for the space."},
		{Name: "topic", Kind: connector.FieldString,
			Description: "Description shown in clients."},
		{Name: "alias", Kind: connector.FieldString,
			Description: "Local part of the alias. Becomes #<alias>:<homeserver>."},
		{Name: "preset", Kind: connector.FieldEnum, Default: "private_chat",
			Options: []connector.EnumOption{
				{Value: "private_chat", Label: "Private — invite-only"},
				{Value: "public_chat", Label: "Public — joinable by anyone on the homeserver"},
			},
			Description: "Same as create-room but limited to the two meaningful options for spaces."},
		{Name: "invite", Kind: connector.FieldStringList,
			Description: "User IDs to invite at creation."},
	}}
}

func (a createSpaceAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	body := buildCreateRoomBody(params, true)
	var resp struct {
		RoomID    string `json:"room_id"`
		RoomAlias string `json:"room_alias,omitempty"`
	}
	if err := s.http.SendJSON(ctx, "POST", "/_matrix/client/v3/createRoom", body, &resp); err != nil {
		return nil, err
	}
	out := map[string]any{"space_id": resp.RoomID, "type": "m.space"}
	if resp.RoomAlias != "" {
		out["space_alias"] = resp.RoomAlias
	}
	return out, nil
}

// buildCreateRoomBody is the shared body builder for createRoom (both
// regular rooms and spaces). isSpace=true sets creation_content.type
// to m.space and drops the "trusted_private_chat" preset which doesn't
// make sense for a space.
func buildCreateRoomBody(params connector.Values, isSpace bool) map[string]any {
	body := map[string]any{
		"preset": params.String("preset"),
	}
	if name := params.String("name"); name != "" {
		body["name"] = name
	}
	if topic := params.String("topic"); topic != "" {
		body["topic"] = topic
	}
	if alias := params.String("alias"); alias != "" {
		body["room_alias_name"] = alias
	}
	if invites := params.StringList("invite"); len(invites) > 0 {
		body["invite"] = invites
	}
	if isSpace {
		body["creation_content"] = map[string]any{"type": "m.space"}
	}
	// E2EE — only meaningful for rooms, not spaces.
	if !isSpace && params.Bool("encrypted") {
		body["initial_state"] = []map[string]any{
			{
				"type":      "m.room.encryption",
				"state_key": "",
				"content":   map[string]any{"algorithm": "m.megolm.v1.aes-sha2"},
			},
		}
	}
	if html := params.String("topic_html"); html != "" {
		extra, _ := body["initial_state"].([]map[string]any)
		extra = append(extra, map[string]any{
			"type":      "m.room.topic",
			"state_key": "",
			"content": map[string]any{
				"topic":          params.String("topic"),
				"format":         "org.matrix.custom.html",
				"formatted_body": html,
			},
		})
		body["initial_state"] = extra
	}
	return body
}

// ── invite-user ───────────────────────────────────────────────────────────

type inviteUserAction struct{}

func (inviteUserAction) Name() string         { return "invite-user" }
func (inviteUserAction) DisplayName() string  { return "Invite user to room" }
func (inviteUserAction) Description() string  { return "Send a Matrix invite to a user. Works for both regular rooms and spaces. Requires the inviting account to have the right power level (default: any member can invite)." }
func (inviteUserAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "room", Kind: connector.FieldString, Required: true,
			Description: "Room id (!...) or alias (#...). Same accepting shape as send-message."},
		{Name: "user", Kind: connector.FieldString, Required: true,
			Description: "Full Matrix user id. e.g. @alice:matrix.example.com."},
		{Name: "reason", Kind: connector.FieldLongText,
			Description: "Optional reason shown to the invitee in supporting clients."},
	}}
}

func (a inviteUserAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	roomID, err := resolveRoomID(ctx, s.http, params.String("room"))
	if err != nil {
		return nil, err
	}
	user := params.String("user")
	if user == "" {
		return nil, errors.New("user is required")
	}
	body := map[string]any{"user_id": user}
	if reason := params.String("reason"); reason != "" {
		body["reason"] = reason
	}
	path := "/_matrix/client/v3/rooms/" + url.PathEscape(roomID) + "/invite"
	if err := s.http.SendJSON(ctx, "POST", path, body, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"room_id": roomID,
		"user":    user,
		"invited": true,
	}, nil
}

// ── add-room-to-space ─────────────────────────────────────────────────────
//
// Spaces are linked to their child rooms via a state event
// `m.space.child` on the space, where the state_key is the room id and
// the content has at least one `via` server. This is a one-way link —
// the child room can independently advertise its parent space via
// `m.space.parent` (not done here; clients can navigate without it).

type addRoomToSpaceAction struct{}

func (addRoomToSpaceAction) Name() string         { return "add-room-to-space" }
func (addRoomToSpaceAction) DisplayName() string  { return "Add room as space child" }
func (addRoomToSpaceAction) Description() string  { return "Link an existing room as a child of a space. Writes an m.space.child state event on the space; clients then list the room under the space." }
func (addRoomToSpaceAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "space", Kind: connector.FieldString, Required: true,
			Description: "Space id or alias."},
		{Name: "room", Kind: connector.FieldString, Required: true,
			Description: "Child room id or alias."},
		{Name: "via", Kind: connector.FieldStringList,
			Description: "Comma-separated homeserver hostnames that can route the child room. Default: derived from the room's id suffix."},
		{Name: "suggested", Kind: connector.FieldBool, Default: false,
			Description: "Mark as suggested — clients may highlight it in the space."},
		{Name: "order", Kind: connector.FieldString,
			Description: "Sort key (string). Lower lex sorts first. Optional."},
	}}
}

func (a addRoomToSpaceAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	spaceID, err := resolveRoomID(ctx, s.http, params.String("space"))
	if err != nil {
		return nil, fmt.Errorf("space: %w", err)
	}
	roomID, err := resolveRoomID(ctx, s.http, params.String("room"))
	if err != nil {
		return nil, fmt.Errorf("room: %w", err)
	}

	via := params.StringList("via")
	if len(via) == 0 {
		// Derive from the room id — the part after the colon is the
		// canonical via server for the room. (`!abc:matrix.example.com`
		// → `matrix.example.com`.)
		if i := strings.LastIndex(roomID, ":"); i > 0 {
			via = []string{roomID[i+1:]}
		}
	}
	if len(via) == 0 {
		return nil, errors.New("via is required (couldn't derive from room id)")
	}

	content := map[string]any{"via": via}
	if params.Bool("suggested") {
		content["suggested"] = true
	}
	if order := params.String("order"); order != "" {
		content["order"] = order
	}

	// PUT /rooms/{spaceId}/state/m.space.child/{roomId}
	path := "/_matrix/client/v3/rooms/" + url.PathEscape(spaceID) +
		"/state/m.space.child/" + url.PathEscape(roomID)
	var resp struct {
		EventID string `json:"event_id"`
	}
	if err := s.http.SendJSON(ctx, "PUT", path, content, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"space_id": spaceID,
		"room_id":  roomID,
		"event_id": resp.EventID,
		"via":      via,
		"linked":   true,
	}, nil
}
