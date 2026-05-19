package synapse

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// qualifyUserID returns the param as-is if it's already a full
// @local:server, otherwise prepends @ and appends the homeserver's
// server-name. The server-name is derived from the admin's whoami
// response (saved by Validate) — NOT from the homeserver URL, because
// they can differ (delegated server-name: chat.sistemica.cloud hosts
// the API for the sistemica.cloud server-name).
func (s *session) qualifyUserID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "@") && strings.Contains(raw, ":") {
		return raw
	}
	serverName := s.serverName()
	if serverName == "" {
		// Last-resort fallback. Almost always wrong on a delegated
		// homeserver — return uncertain form so the API errors clearly
		// rather than silently produce something nonsensical.
		return "@" + strings.TrimPrefix(raw, "@")
	}
	return "@" + strings.TrimPrefix(raw, "@") + ":" + serverName
}

// serverName returns the homeserver's server-name (the part after `:`
// in any user id on this homeserver). Sourced from admin_user_id which
// Validate populated via whoami.
func (s *session) serverName() string {
	uid := s.cred.Values.String(fAdminUserID)
	if i := strings.LastIndex(uid, ":"); i > 0 {
		return uid[i+1:]
	}
	return ""
}

// ── server-version ────────────────────────────────────────────────────────

type serverVersionAction struct{}

func (serverVersionAction) Name() string             { return "server-version" }
func (serverVersionAction) DisplayName() string      { return "Synapse server version" }
func (serverVersionAction) Description() string      { return "Returns the Synapse + Python versions. Cheapest probe; used by Validate." }
func (serverVersionAction) Schema() connector.Schema { return connector.Schema{} }

func (serverVersionAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/_synapse/admin/v1/server_version", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── list-users ────────────────────────────────────────────────────────────

type listUsersAction struct{}

func (listUsersAction) Name() string         { return "list-users" }
func (listUsersAction) DisplayName() string  { return "List users on the homeserver" }
func (listUsersAction) Description() string  { return "All users registered locally on this Synapse. Different from `matrix list-rooms` etc. — this is the admin-scope view, every user the server knows about." }
func (listUsersAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Kind: connector.FieldString,
			Description: "Filter by local part substring."},
		{Name: "guests", Kind: connector.FieldBool, Default: true,
			Description: "Include guest accounts."},
		{Name: "deactivated", Kind: connector.FieldBool, Default: false,
			Description: "Include deactivated accounts."},
		{Name: "admins", Kind: connector.FieldString, Default: "",
			Description: "'true' = admins only, 'false' = non-admins only, '' = both."},
		{Name: "from", Kind: connector.FieldInt, Default: 0,
			Description: "Offset for pagination."},
		{Name: "limit", Kind: connector.FieldInt, Default: 100,
			Description: "Page size. Max 100 (Synapse cap)."},
		{Name: "order_by", Kind: connector.FieldEnum, Default: "name",
			Options: []connector.EnumOption{
				{Value: "name", Label: "Local part"},
				{Value: "is_guest", Label: "Guest flag"},
				{Value: "admin", Label: "Admin flag"},
				{Value: "user_type", Label: "Type"},
				{Value: "deactivated", Label: "Deactivated flag"},
				{Value: "shadow_banned", Label: "Shadow ban"},
				{Value: "creation_ts", Label: "Creation time"},
				{Value: "last_seen_ts", Label: "Last seen"},
			},
			Description: "Sort key."},
		{Name: "direction", Kind: connector.FieldEnum, Default: "f",
			Options: []connector.EnumOption{
				{Value: "f", Label: "Forward (ascending)"},
				{Value: "b", Label: "Backward (descending)"},
			},
			Description: "Sort direction."},
	}}
}

func (a listUsersAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := url.Values{}
	if n := params.String("name"); n != "" {
		q.Set("name", n)
	}
	q.Set("guests", fmt.Sprintf("%t", params.Bool("guests")))
	q.Set("deactivated", fmt.Sprintf("%t", params.Bool("deactivated")))
	if v := params.String("admins"); v != "" {
		q.Set("admins", v)
	}
	q.Set("from", fmt.Sprintf("%d", params.Int("from")))
	q.Set("limit", fmt.Sprintf("%d", params.Int("limit")))
	q.Set("order_by", params.String("order_by"))
	q.Set("dir", params.String("direction"))

	var resp struct {
		Users     []map[string]any `json:"users"`
		Total     int              `json:"total"`
		NextToken string           `json:"next_token,omitempty"`
	}
	if err := s.http.GetJSON(ctx, "/_synapse/admin/v2/users?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"total":      resp.Total,
		"count":      len(resp.Users),
		"users":      resp.Users,
		"next_token": resp.NextToken,
	}, nil
}

// ── get-user ──────────────────────────────────────────────────────────────

type getUserAction struct{}

func (getUserAction) Name() string         { return "get-user" }
func (getUserAction) DisplayName() string  { return "Get user details" }
func (getUserAction) Description() string  { return "Full user record: name, displayname, admin flag, deactivated, creation_ts, last_seen_ts, threepids, devices count, room count." }
func (getUserAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "user", Kind: connector.FieldString, Required: true,
			Description: "Local part (alice) or full Matrix id (@alice:host)."},
	}}
}

func (a getUserAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	uid := s.qualifyUserID(params.String("user"))
	if uid == "" {
		return nil, errors.New("user is required")
	}
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/_synapse/admin/v2/users/"+url.PathEscape(uid), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── create-user ───────────────────────────────────────────────────────────

type createUserAction struct{}

func (createUserAction) Name() string         { return "create-user" }
func (createUserAction) DisplayName() string  { return "Create or update a user" }
func (createUserAction) Description() string  { return "Idempotent PUT — creates if missing, updates if present. Set password to register a usable account; set admin=true to grant homeserver-admin privileges; threepids attaches email/msisdn identifiers." }
func (createUserAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "user", Kind: connector.FieldString, Required: true,
			Description: "Local part or full @user:host. Local part will be qualified with the homeserver domain automatically."},
		{Name: "password", Kind: connector.FieldSecret,
			Description: "Initial password. Omit to create a passwordless account (e.g. for SSO-only users) or to leave unchanged on update."},
		{Name: "displayname", Kind: connector.FieldString,
			Description: "Display name shown in clients."},
		{Name: "admin", Kind: connector.FieldBool, Default: false,
			Description: "Grant homeserver-admin role. Use sparingly."},
		{Name: "deactivated", Kind: connector.FieldBool, Default: false,
			Description: "Create as deactivated. Rare — usually you create active then deactivate later."},
		{Name: "email", Kind: connector.FieldString,
			Description: "Attach an email threepid. Used for password reset + identity discovery."},
		{Name: "logout_devices", Kind: connector.FieldBool, Default: false,
			Description: "On update with a new password: log out the user from every existing device. No effect on create."},
	}}
}

func (a createUserAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	uid := s.qualifyUserID(params.String("user"))
	if uid == "" {
		return nil, errors.New("user is required")
	}
	body := map[string]any{
		"admin":       params.Bool("admin"),
		"deactivated": params.Bool("deactivated"),
	}
	if p := params.String("password"); p != "" {
		body["password"] = p
	}
	if d := params.String("displayname"); d != "" {
		body["displayname"] = d
	}
	if e := params.String("email"); e != "" {
		body["threepids"] = []map[string]any{{
			"medium":  "email",
			"address": e,
		}}
	}
	if params.Bool("logout_devices") {
		body["logout_devices"] = true
	}
	var out map[string]any
	if err := s.http.SendJSON(ctx, "PUT", "/_synapse/admin/v2/users/"+url.PathEscape(uid), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── set-password ──────────────────────────────────────────────────────────

type setPasswordAction struct{}

func (setPasswordAction) Name() string         { return "set-password" }
func (setPasswordAction) DisplayName() string  { return "Force-reset user password" }
func (setPasswordAction) Description() string  { return "Set a new password. Optionally invalidates every device session so the user must re-login everywhere." }
func (setPasswordAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "user", Kind: connector.FieldString, Required: true,
			Description: "Target user (local part or full id)."},
		{Name: "password", Kind: connector.FieldSecret, Required: true,
			Description: "New password."},
		{Name: "logout_devices", Kind: connector.FieldBool, Default: true,
			Description: "Default true — best practice for password reset to force re-login on every device."},
	}}
}

func (a setPasswordAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	uid := s.qualifyUserID(params.String("user"))
	pw := params.String("password")
	if uid == "" || pw == "" {
		return nil, errors.New("user and password are required")
	}
	body := map[string]any{
		"new_password":   pw,
		"logout_devices": params.Bool("logout_devices"),
	}
	if err := s.http.SendJSON(ctx, "POST", "/_synapse/admin/v1/reset_password/"+url.PathEscape(uid), body, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"user":           uid,
		"password_reset": true,
		"logout_devices": params.Bool("logout_devices"),
	}, nil
}

// ── deactivate-user ───────────────────────────────────────────────────────

type deactivateUserAction struct{}

func (deactivateUserAction) Name() string         { return "deactivate-user" }
func (deactivateUserAction) DisplayName() string  { return "Deactivate user account" }
func (deactivateUserAction) Description() string  { return "Mark account deactivated. With erase=true the user is GDPR-style erased — display name and avatar are removed, future federated lookups return empty. Profile rooms remain but membership leaves." }
func (deactivateUserAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "user", Kind: connector.FieldString, Required: true,
			Description: "Target user."},
		{Name: "erase", Kind: connector.FieldBool, Default: false,
			Description: "GDPR erase: clear display name + avatar, return empty for federated requests."},
	}}
}

func (a deactivateUserAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	uid := s.qualifyUserID(params.String("user"))
	if uid == "" {
		return nil, errors.New("user is required")
	}
	body := map[string]any{"erase": params.Bool("erase")}
	var out map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/_synapse/admin/v1/deactivate/"+url.PathEscape(uid), body, &out); err != nil {
		return nil, err
	}
	return map[string]any{
		"user":        uid,
		"deactivated": true,
		"erased":      params.Bool("erase"),
		"id_server_unbind_result": out["id_server_unbind_result"],
	}, nil
}

// ── list-rooms (admin scope — all rooms on server) ────────────────────────

type listRoomsAction struct{}

func (listRoomsAction) Name() string         { return "list-rooms" }
func (listRoomsAction) DisplayName() string  { return "List ALL rooms on the homeserver" }
func (listRoomsAction) Description() string  { return "Admin scope — every room the server knows about, not just rooms the admin has joined (which is what `matrix list-rooms` returns). Filterable by name + member count." }
func (listRoomsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "search", Kind: connector.FieldString,
			Description: "Filter by name / alias / canonical alias substring."},
		{Name: "from", Kind: connector.FieldInt, Default: 0,
			Description: "Pagination offset."},
		{Name: "limit", Kind: connector.FieldInt, Default: 100,
			Description: "Page size. Max 100."},
		{Name: "order_by", Kind: connector.FieldEnum, Default: "name",
			Options: []connector.EnumOption{
				{Value: "name", Label: "Name"},
				{Value: "canonical_alias", Label: "Canonical alias"},
				{Value: "joined_members", Label: "Joined members"},
				{Value: "joined_local_members", Label: "Local joined members"},
				{Value: "version", Label: "Room version"},
				{Value: "creator", Label: "Creator"},
				{Value: "encryption", Label: "Encryption algo"},
				{Value: "federatable", Label: "Federatable"},
				{Value: "public", Label: "Publicness"},
				{Value: "join_rules", Label: "Join rules"},
				{Value: "guest_access", Label: "Guest access"},
				{Value: "history_visibility", Label: "History visibility"},
				{Value: "state_events", Label: "State event count"},
			}},
		{Name: "direction", Kind: connector.FieldEnum, Default: "f",
			Options: []connector.EnumOption{
				{Value: "f", Label: "Ascending"},
				{Value: "b", Label: "Descending"},
			}},
	}}
}

func (a listRoomsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := url.Values{}
	if v := params.String("search"); v != "" {
		q.Set("search_term", v)
	}
	q.Set("from", fmt.Sprintf("%d", params.Int("from")))
	q.Set("limit", fmt.Sprintf("%d", params.Int("limit")))
	q.Set("order_by", params.String("order_by"))
	q.Set("dir", params.String("direction"))

	var resp struct {
		Rooms      []map[string]any `json:"rooms"`
		Offset     int              `json:"offset"`
		TotalRooms int              `json:"total_rooms"`
		NextBatch  int              `json:"next_batch,omitempty"`
		PrevBatch  int              `json:"prev_batch,omitempty"`
	}
	if err := s.http.GetJSON(ctx, "/_synapse/admin/v1/rooms?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"total":      resp.TotalRooms,
		"offset":     resp.Offset,
		"count":      len(resp.Rooms),
		"rooms":      resp.Rooms,
		"next_batch": resp.NextBatch,
	}, nil
}

// ── delete-room ───────────────────────────────────────────────────────────

type deleteRoomAction struct{}

func (deleteRoomAction) Name() string         { return "delete-room" }
func (deleteRoomAction) DisplayName() string  { return "Delete a room (admin purge)" }
func (deleteRoomAction) Description() string  { return "Forcibly remove a room from the homeserver. Block=true also prevents re-creation by anyone other than admin. PurgeRoom is async — Synapse returns a delete_id you can poll for status." }
func (deleteRoomAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "room", Kind: connector.FieldString, Required: true,
			Description: "Full room id (!abc:host) — admin v2 takes the id directly."},
		{Name: "block", Kind: connector.FieldBool, Default: false,
			Description: "Block the room from being recreated. Use for abusive rooms."},
		{Name: "purge", Kind: connector.FieldBool, Default: true,
			Description: "Also delete every event from the database (default). false leaves the room in place but kicks all local users."},
		{Name: "message", Kind: connector.FieldLongText,
			Description: "Message sent to room members before kicking. Optional."},
		{Name: "room_name", Kind: connector.FieldString,
			Description: "Replacement room name shown to kicked members. Optional."},
	}}
}

func (a deleteRoomAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	room := params.String("room")
	if room == "" {
		return nil, errors.New("room is required")
	}
	body := map[string]any{
		"block": params.Bool("block"),
		"purge": params.Bool("purge"),
	}
	if m := params.String("message"); m != "" {
		body["message"] = m
	}
	if n := params.String("room_name"); n != "" {
		body["room_name"] = n
	}
	var out map[string]any
	if err := s.http.SendJSON(ctx, "DELETE", "/_synapse/admin/v2/rooms/"+url.PathEscape(room), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── purge-history ─────────────────────────────────────────────────────────

type purgeHistoryAction struct{}

func (purgeHistoryAction) Name() string         { return "purge-history" }
func (purgeHistoryAction) DisplayName() string  { return "Purge old message history" }
func (purgeHistoryAction) Description() string  { return "Drop events before a timestamp (delete_local_events=true also removes local copy). Use to reclaim disk space; events are gone forever — no undo." }
func (purgeHistoryAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "room", Kind: connector.FieldString, Required: true,
			Description: "Room id to purge from."},
		{Name: "purge_up_to_ts", Kind: connector.FieldInt, Required: true,
			Description: "Unix ms timestamp; events older than this go. e.g. (epoch ms 30 days ago)."},
		{Name: "delete_local_events", Kind: connector.FieldBool, Default: false,
			Description: "Also drop events from local copy. Default false (kept for federation re-fetch)."},
	}}
}

func (a purgeHistoryAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	room := params.String("room")
	ts := params.Int("purge_up_to_ts")
	if room == "" || ts == 0 {
		return nil, errors.New("room and purge_up_to_ts are required")
	}
	body := map[string]any{
		"purge_up_to_ts":      ts,
		"delete_local_events": params.Bool("delete_local_events"),
	}
	var out map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/_synapse/admin/v1/purge_history/"+url.PathEscape(room), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}
