package youtrack

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// YouTrack's REST returns minimal fields by default — only `id`. To get
// usable output you MUST pass ?fields=... on every read. These are the
// shapes we ask for; consumers can override via -p fields=... when they
// need more.
const (
	userFields    = "id,login,fullName,email,banned,guest,online,ringId"
	projectFields = "id,name,shortName,description,leader(id,login,fullName),archived,ringId"
	issueFields   = "id,idReadable,summary,description,project(id,shortName,name),reporter(login,fullName),created,updated,resolved,customFields(name,value(login,name,fullName,id))"
)

// resolveProjectID turns either a YouTrack ID (e.g. "0-1") or shortName
// (e.g. "MW") into the canonical id required by issue creation. Empty
// input → empty output, no error.
func resolveProjectID(ctx context.Context, cli *httptr.Client, idOrKey string) (string, error) {
	if idOrKey == "" {
		return "", nil
	}
	// Heuristic: YouTrack resource IDs are dash-separated numerics like
	// "0-1" / "26-1234". Anything else is treated as a shortName.
	if looksLikeID(idOrKey) {
		return idOrKey, nil
	}
	q := url.Values{}
	q.Set("fields", "id,shortName")
	q.Set("query", idOrKey)
	var projects []struct {
		ID        string `json:"id"`
		ShortName string `json:"shortName"`
	}
	if err := cli.GetJSON(ctx, "/api/admin/projects", q, &projects); err != nil {
		return "", fmt.Errorf("lookup project %q: %w", idOrKey, err)
	}
	for _, p := range projects {
		if strings.EqualFold(p.ShortName, idOrKey) {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("no project with shortName %q", idOrKey)
}

// resolveUserID turns either a YouTrack id ("1-2"), a login, or empty
// into the user's id. Used by create-project (lead) and create-issue
// (assignee). Returns "" if input is "" — caller decides whether to
// require it.
func resolveUserID(ctx context.Context, cli *httptr.Client, loginOrID string) (string, error) {
	if loginOrID == "" {
		return "", nil
	}
	if looksLikeID(loginOrID) {
		return loginOrID, nil
	}
	q := url.Values{}
	q.Set("fields", "id,login")
	q.Set("query", loginOrID)
	var users []struct {
		ID    string `json:"id"`
		Login string `json:"login"`
	}
	if err := cli.GetJSON(ctx, "/api/users", q, &users); err != nil {
		return "", fmt.Errorf("lookup user %q: %w", loginOrID, err)
	}
	for _, u := range users {
		if strings.EqualFold(u.Login, loginOrID) {
			return u.ID, nil
		}
	}
	return "", fmt.Errorf("no user with login %q", loginOrID)
}

func looksLikeID(s string) bool {
	parts := strings.Split(s, "-")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// ── me ────────────────────────────────────────────────────────────────────

type meAction struct{}

func (meAction) Name() string         { return "me" }
func (meAction) DisplayName() string  { return "Get my user" }
func (meAction) Description() string  { return "Return the user the bound token belongs to. Cheapest validate." }
func (meAction) Schema() connector.Schema { return connector.Schema{} }

func (meAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	q := url.Values{}
	q.Set("fields", userFields)
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/api/users/me", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── list-users ────────────────────────────────────────────────────────────

type listUsersAction struct{}

func (listUsersAction) Name() string         { return "list-users" }
func (listUsersAction) DisplayName() string  { return "List users" }
func (listUsersAction) Description() string  { return "List users. Optional query filter (YouTrack syntax e.g. 'banned: false')." }
func (listUsersAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "query", Kind: connector.FieldString, Description: "YouTrack user query (login, fullName, banned, etc.)"},
		{Name: "top", Kind: connector.FieldInt, Default: 100, Description: "Max results."},
		{Name: "skip", Kind: connector.FieldInt, Default: 0},
		{Name: "fields", Kind: connector.FieldString, Default: userFields},
	}}
}

func (a listUsersAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := url.Values{}
	q.Set("fields", params.String("fields"))
	if v := params.String("query"); v != "" {
		q.Set("query", v)
	}
	q.Set("$top", fmt.Sprintf("%d", params.Int("top")))
	if skip := params.Int("skip"); skip > 0 {
		q.Set("$skip", fmt.Sprintf("%d", skip))
	}
	var out []map[string]any
	if err := s.http.GetJSON(ctx, "/api/users", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── get-user ──────────────────────────────────────────────────────────────

type getUserAction struct{}

func (getUserAction) Name() string         { return "get-user" }
func (getUserAction) DisplayName() string  { return "Get user" }
func (getUserAction) Description() string  { return "Fetch one user by login or id." }
func (getUserAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "user", Kind: connector.FieldString, Required: true, Description: "Login or YouTrack id (e.g. '1-2')."},
		{Name: "fields", Kind: connector.FieldString, Default: userFields},
	}}
}

func (a getUserAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	user := params.String("user")
	if user == "" {
		return nil, errors.New("user is required")
	}
	id, err := resolveUserID(ctx, s.http, user)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("fields", params.String("fields"))
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/api/users/"+url.PathEscape(id), q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── create-user ───────────────────────────────────────────────────────────

type createUserAction struct{}

func (createUserAction) Name() string         { return "create-user" }
func (createUserAction) DisplayName() string  { return "Create user" }
func (createUserAction) Description() string  { return "Admin: create a YouTrack user. Password optional — without it the user gets a reset email (if SMTP is configured)." }
func (createUserAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "login", Kind: connector.FieldString, Required: true},
		{Name: "email", Kind: connector.FieldString, Required: true},
		{Name: "full_name", Kind: connector.FieldString, Description: "Display name."},
		{Name: "password", Kind: connector.FieldSecret, Required: true, Description: "Initial password. Required on most self-hosted instances; some cloud installs allow omitting if SMTP reset is configured."},
		{Name: "force_change_password", Kind: connector.FieldBool, Default: false},
	}}
}

func (a createUserAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	login := params.String("login")
	email := params.String("email")
	if login == "" || email == "" {
		return nil, errors.New("login and email are required")
	}
	body := map[string]any{
		"login":               login,
		"email":               email,
		"forceChangePassword": params.Bool("force_change_password"),
	}
	if v := params.String("full_name"); v != "" {
		body["fullName"] = v
	}
	if v := params.String("password"); v != "" {
		body["password"] = v
	}
	q := url.Values{}
	q.Set("fields", userFields)
	var out map[string]any
	if err := s.http.PostJSON(ctx, "/api/users?"+q.Encode(), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── list-projects ─────────────────────────────────────────────────────────

type listProjectsAction struct{}

func (listProjectsAction) Name() string         { return "list-projects" }
func (listProjectsAction) DisplayName() string  { return "List projects" }
func (listProjectsAction) Description() string  { return "List all projects visible to this token." }
func (listProjectsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "query", Kind: connector.FieldString, Description: "YouTrack project search (matches name + shortName)."},
		{Name: "top", Kind: connector.FieldInt, Default: 100},
		{Name: "skip", Kind: connector.FieldInt, Default: 0},
		{Name: "fields", Kind: connector.FieldString, Default: projectFields},
	}}
}

func (a listProjectsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := url.Values{}
	q.Set("fields", params.String("fields"))
	if v := params.String("query"); v != "" {
		q.Set("query", v)
	}
	q.Set("$top", fmt.Sprintf("%d", params.Int("top")))
	if skip := params.Int("skip"); skip > 0 {
		q.Set("$skip", fmt.Sprintf("%d", skip))
	}
	var out []map[string]any
	if err := s.http.GetJSON(ctx, "/api/admin/projects", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── get-project ───────────────────────────────────────────────────────────

type getProjectAction struct{}

func (getProjectAction) Name() string         { return "get-project" }
func (getProjectAction) DisplayName() string  { return "Get project" }
func (getProjectAction) Description() string  { return "Fetch one project by shortName (e.g. MW) or id." }
func (getProjectAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project", Kind: connector.FieldString, Required: true, Description: "shortName or id"},
		{Name: "fields", Kind: connector.FieldString, Default: projectFields},
	}}
}

func (a getProjectAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id, err := resolveProjectID(ctx, s.http, params.String("project"))
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("fields", params.String("fields"))
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/api/admin/projects/"+url.PathEscape(id), q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── create-project ────────────────────────────────────────────────────────

type createProjectAction struct{}

func (createProjectAction) Name() string         { return "create-project" }
func (createProjectAction) DisplayName() string  { return "Create project" }
func (createProjectAction) Description() string  { return "Admin: create a project. lead is required by YouTrack — defaults to the bound user if omitted." }
func (createProjectAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "key", Kind: connector.FieldString, Required: true, Description: "Project shortName (e.g. MW)."},
		{Name: "name", Kind: connector.FieldString, Required: true, Description: "Display name."},
		{Name: "lead", Kind: connector.FieldString, Description: "Project lead login or id. Defaults to /me."},
		{Name: "description", Kind: connector.FieldLongText},
	}}
}

func (a createProjectAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	key := params.String("key")
	name := params.String("name")
	if key == "" || name == "" {
		return nil, errors.New("key and name are required")
	}

	leadID, err := resolveUserID(ctx, s.http, params.String("lead"))
	if err != nil {
		return nil, err
	}
	if leadID == "" {
		// Default to the bound user.
		var me struct {
			ID string `json:"id"`
		}
		if err := s.http.GetJSON(ctx, "/api/users/me?fields=id", nil, &me); err != nil {
			return nil, fmt.Errorf("resolve default lead: %w", err)
		}
		leadID = me.ID
	}

	body := map[string]any{
		"name":      name,
		"shortName": key,
		"leader":    map[string]any{"id": leadID},
	}
	if d := params.String("description"); d != "" {
		body["description"] = d
	}
	q := url.Values{}
	q.Set("fields", projectFields)
	var out map[string]any
	if err := s.http.PostJSON(ctx, "/api/admin/projects?"+q.Encode(), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── list-issues ───────────────────────────────────────────────────────────

type listIssuesAction struct{}

func (listIssuesAction) Name() string         { return "list-issues" }
func (listIssuesAction) DisplayName() string  { return "List issues" }
func (listIssuesAction) Description() string  { return "Search issues. Optional project filter (shortName/id) plus YouTrack query language." }
func (listIssuesAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_key", Kind: connector.FieldString, Description: "Filter to a single project shortName."},
		{Name: "query", Kind: connector.FieldString, Description: "YouTrack query (e.g. 'for: me #Open'). Combined with project filter if both present."},
		{Name: "top", Kind: connector.FieldInt, Default: 100},
		{Name: "skip", Kind: connector.FieldInt, Default: 0},
		{Name: "fields", Kind: connector.FieldString, Default: issueFields},
	}}
}

func (a listIssuesAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())

	// Combine project + query into one YouTrack query string.
	var bits []string
	if pk := params.String("project_key"); pk != "" {
		bits = append(bits, "project: "+pk)
	}
	if q := params.String("query"); q != "" {
		bits = append(bits, q)
	}

	q := url.Values{}
	q.Set("fields", params.String("fields"))
	if len(bits) > 0 {
		q.Set("query", strings.Join(bits, " "))
	}
	q.Set("$top", fmt.Sprintf("%d", params.Int("top")))
	if skip := params.Int("skip"); skip > 0 {
		q.Set("$skip", fmt.Sprintf("%d", skip))
	}
	var out []map[string]any
	if err := s.http.GetJSON(ctx, "/api/issues", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── get-issue ─────────────────────────────────────────────────────────────

type getIssueAction struct{}

func (getIssueAction) Name() string         { return "get-issue" }
func (getIssueAction) DisplayName() string  { return "Get issue" }
func (getIssueAction) Description() string  { return "Fetch one issue. Accepts the human-readable id (e.g. 'MW-123')." }
func (getIssueAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldString, Required: true, Description: "MW-123 or the internal id."},
		{Name: "fields", Kind: connector.FieldString, Default: issueFields},
	}}
}

func (a getIssueAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.String("id")
	if id == "" {
		return nil, errors.New("id is required")
	}
	q := url.Values{}
	q.Set("fields", params.String("fields"))
	var out map[string]any
	if err := s.http.GetJSON(ctx, "/api/issues/"+url.PathEscape(id), q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── create-token ──────────────────────────────────────────────────────────
//
// Permanent tokens live in the Hub (YouTrack's auth service) at
// /hub/api/rest/users/{id}/permanenttokens. With Update-User permission,
// an admin token can mint tokens for other users — letting us save a
// pantograf instance that acts as the target user.
//
// The Hub `scope` is required; default 0-0-0-0-0 is the standard YouTrack
// service id on self-hosted installs. Pass scope_id explicitly if your
// install uses a different service id (rare).

type createTokenAction struct{}

func (createTokenAction) Name() string         { return "create-token" }
func (createTokenAction) DisplayName() string  { return "Create permanent token" }
func (createTokenAction) Description() string  { return "Admin: mint a permanent token for a user (Hub API). Token is returned only once — save it immediately." }
func (createTokenAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "user", Kind: connector.FieldString, Required: true, Description: "Login or id of the user to mint a token for."},
		{Name: "name", Kind: connector.FieldString, Default: "pantograf", Description: "Display name of the token in the user's settings."},
		{Name: "scope_id", Kind: connector.FieldString,
			Description: "Hub service id. Empty = auto-resolve YouTrack app service. Override only for non-default scopes (e.g. YouTrack Mobile)."},
	}}
}

func (a createTokenAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	user := params.String("user")
	if user == "" {
		return nil, errors.New("user is required")
	}

	// Hub uses ringId, not YouTrack's internal user id. Resolve to ringId.
	ringID, err := resolveRingID(ctx, s.http, user)
	if err != nil {
		return nil, err
	}

	// Auto-resolve scope: find the Hub service whose applicationName is
	// "YouTrack". Falls back to the user-supplied scope_id if given.
	scopeID := params.String("scope_id")
	if scopeID == "" {
		scopeID, err = resolveYouTrackServiceID(ctx, s.http)
		if err != nil {
			return nil, fmt.Errorf("auto-resolve scope: %w", err)
		}
	}

	body := map[string]any{
		"name":  params.String("name"),
		"scope": []map[string]any{{"id": scopeID}},
	}
	q := url.Values{}
	q.Set("fields", "id,name,token,scope(id,name)")
	var out map[string]any
	path := "/hub/api/rest/users/" + url.PathEscape(ringID) + "/permanenttokens?" + q.Encode()
	if err := s.http.PostJSON(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// resolveYouTrackServiceID returns the Hub id of the YouTrack app service.
// Lists all Hub services and picks the one whose applicationName is
// "YouTrack" — different installs use different UUIDs (the "0-0-0-0-0"
// default is Hub-admin, which causes 401s for non-admin tokens).
func resolveYouTrackServiceID(ctx context.Context, cli *httptr.Client) (string, error) {
	q := url.Values{}
	q.Set("fields", "id,name,applicationName")
	q.Set("$top", "100")
	var resp struct {
		Services []struct {
			ID              string `json:"id"`
			Name            string `json:"name"`
			ApplicationName string `json:"applicationName"`
		} `json:"services"`
	}
	if err := cli.GetJSON(ctx, "/hub/api/rest/services", q, &resp); err != nil {
		return "", err
	}
	for _, s := range resp.Services {
		if s.ApplicationName == "YouTrack" {
			return s.ID, nil
		}
	}
	return "", errors.New("no Hub service with applicationName=YouTrack")
}

// resolveRingID looks up a user by login or YouTrack id and returns the
// Hub ringId — required for Hub-path API calls (permanent tokens etc.).
func resolveRingID(ctx context.Context, cli *httptr.Client, loginOrID string) (string, error) {
	if loginOrID == "" {
		return "", errors.New("user is required")
	}
	q := url.Values{}
	q.Set("fields", "id,login,ringId")
	if looksLikeID(loginOrID) {
		var u struct {
			RingID string `json:"ringId"`
			Login  string `json:"login"`
		}
		if err := cli.GetJSON(ctx, "/api/users/"+url.PathEscape(loginOrID), q, &u); err != nil {
			return "", fmt.Errorf("lookup user: %w", err)
		}
		if u.RingID == "" {
			return "", fmt.Errorf("user %q has no ringId", loginOrID)
		}
		return u.RingID, nil
	}
	q.Set("query", loginOrID)
	var users []struct {
		Login  string `json:"login"`
		RingID string `json:"ringId"`
	}
	if err := cli.GetJSON(ctx, "/api/users", q, &users); err != nil {
		return "", fmt.Errorf("lookup user: %w", err)
	}
	for _, u := range users {
		if strings.EqualFold(u.Login, loginOrID) && u.RingID != "" {
			return u.RingID, nil
		}
	}
	return "", fmt.Errorf("no user with login %q (or no ringId)", loginOrID)
}

// ── list-project-team ─────────────────────────────────────────────────────

type listProjectTeamAction struct{}

func (listProjectTeamAction) Name() string         { return "list-project-team" }
func (listProjectTeamAction) DisplayName() string  { return "List project team" }
func (listProjectTeamAction) Description() string  { return "List users who belong to a project's team (have any role in the project)." }
func (listProjectTeamAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project", Kind: connector.FieldString, Required: true, Description: "shortName or id."},
	}}
}

func (a listProjectTeamAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	pid, err := resolveProjectID(ctx, s.http, params.String("project"))
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("fields", "users(id,login,fullName,email,banned)")
	var team struct {
		Users []map[string]any `json:"users"`
	}
	if err := s.http.GetJSON(ctx, "/api/admin/projects/"+url.PathEscape(pid)+"/team", q, &team); err != nil {
		return nil, err
	}
	return team.Users, nil
}

// ── apply-command ─────────────────────────────────────────────────────────
//
// Note on team membership: modern YouTrack (2024+) has no public REST
// endpoint for modifying a ProjectTeam. /api/admin/projects/{id}/team/users
// only accepts GET; the Hub /usergroups path returns 404 for project
// teams (they're no longer Hub-backed). list-project-team works for
// reading, but to add/remove users you must use the YouTrack UI:
//   https://<host>/projects/<id>/settings/access
//
// YouTrack's /api/commands endpoint takes a free-text command string and
// applies it to a list of issues. Same syntax the in-app command bar uses.
// Examples (any combo can be one command):
//
//   "Assignee katharina"
//   "Priority Critical Type Bug"
//   "State In Progress"
//   "Due 2026-12-31"
//   "Estimation 4h"
//   "tag urgent"
//
// This single action covers every custom-field change the user could want
// without us hard-coding per-field $type ceremony.
//
// YouTrack's /api/commands endpoint takes a free-text command string and
// applies it to a list of issues. Same syntax the in-app command bar uses.
// Examples (any combo can be one command):
//
//   "Assignee katharina"
//   "Priority Critical Type Bug"
//   "State In Progress"
//   "Due 2026-12-31"
//   "Estimation 4h"
//   "tag urgent"
//
// This single action covers every custom-field change the user could want
// without us hard-coding per-field $type ceremony.

type applyCommandAction struct{}

func (applyCommandAction) Name() string         { return "apply-command" }
func (applyCommandAction) DisplayName() string  { return "Apply YouTrack command" }
func (applyCommandAction) Description() string  { return "Run a YouTrack command (Assignee/Priority/Type/State/Due/...) against one or more issues." }
func (applyCommandAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "command", Kind: connector.FieldString, Required: true,
			Description: "YouTrack command-bar syntax. e.g. 'Priority Critical' or 'Assignee katharina Due 2026-12-31'."},
		{Name: "issues", Kind: connector.FieldStringList, Required: true,
			Description: "One or more issue ids (readable form like PGFT-1, or internal). Comma-separate or repeat -p."},
		{Name: "comment", Kind: connector.FieldLongText, Description: "Optional comment to add alongside the command."},
		{Name: "silent", Kind: connector.FieldBool, Default: false, Description: "Suppress notification emails."},
	}}
}

func (a applyCommandAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	cmd := params.String("command")
	issues := params.StringList("issues")
	if cmd == "" || len(issues) == 0 {
		return nil, errors.New("command and issues are required")
	}
	issueRefs := make([]map[string]any, 0, len(issues))
	for _, id := range issues {
		issueRefs = append(issueRefs, map[string]any{"idReadable": id})
	}
	body := map[string]any{
		"query":  cmd,
		"issues": issueRefs,
		"silent": params.Bool("silent"),
	}
	if c := params.String("comment"); c != "" {
		body["comment"] = c
	}
	var out map[string]any
	if err := s.http.PostJSON(ctx, "/api/commands", body, &out); err != nil {
		return nil, err
	}
	return map[string]any{"applied": cmd, "issues": issues, "result": out}, nil
}

// ── set-assignee (convenience over apply-command) ─────────────────────────

type setAssigneeAction struct{}

func (setAssigneeAction) Name() string         { return "set-assignee" }
func (setAssigneeAction) DisplayName() string  { return "Set assignee" }
func (setAssigneeAction) Description() string  { return "Assign one or more issues to a user. Convenience over apply-command." }
func (setAssigneeAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "issue", Kind: connector.FieldString, Required: true, Description: "Issue id (PGFT-1) or comma-list."},
		{Name: "login", Kind: connector.FieldString, Required: true, Description: "User login. Use 'Unassigned' to clear."},
		{Name: "silent", Kind: connector.FieldBool, Default: false},
	}}
}

func (a setAssigneeAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	issue := params.String("issue")
	login := params.String("login")
	if issue == "" || login == "" {
		return nil, errors.New("issue and login are required")
	}
	body := map[string]any{
		"query":  "Assignee " + login,
		"issues": []map[string]any{{"idReadable": issue}},
		"silent": params.Bool("silent"),
	}
	var out map[string]any
	if err := s.http.PostJSON(ctx, "/api/commands", body, &out); err != nil {
		return nil, err
	}
	return map[string]any{"issue": issue, "assignee": login}, nil
}

// ── list-attachments ──────────────────────────────────────────────────────

type listAttachmentsAction struct{}

func (listAttachmentsAction) Name() string         { return "list-attachments" }
func (listAttachmentsAction) DisplayName() string  { return "List attachments" }
func (listAttachmentsAction) Description() string  { return "List files attached to an issue." }
func (listAttachmentsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "issue", Kind: connector.FieldString, Required: true},
	}}
}

func (a listAttachmentsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	issue := params.String("issue")
	if issue == "" {
		return nil, errors.New("issue is required")
	}
	q := url.Values{}
	q.Set("fields", "id,name,size,mimeType,url,author(login,fullName),created")
	var out []map[string]any
	if err := s.http.GetJSON(ctx, "/api/issues/"+url.PathEscape(issue)+"/attachments", q, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── attach-file ───────────────────────────────────────────────────────────

type attachFileAction struct{}

func (attachFileAction) Name() string         { return "attach-file" }
func (attachFileAction) DisplayName() string  { return "Attach file" }
func (attachFileAction) Description() string  { return "Upload a local file as an attachment on an issue." }
func (attachFileAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "issue", Kind: connector.FieldString, Required: true},
		{Name: "file", Kind: connector.FieldString, Required: true, Description: "Local file path."},
	}}
}

func (a attachFileAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	issue := params.String("issue")
	file := params.String("file")
	if issue == "" || file == "" {
		return nil, errors.New("issue and file are required")
	}
	if _, err := os.Stat(file); err != nil {
		return nil, fmt.Errorf("attach: %w", err)
	}
	q := url.Values{}
	q.Set("fields", "id,name,size,mimeType,url")
	path := "/api/issues/" + url.PathEscape(issue) + "/attachments?" + q.Encode()
	var out any
	if err := s.http.PostMultipart(ctx, path, nil, []httptr.FileField{{FieldName: "file", Path: file}}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── download-attachment ───────────────────────────────────────────────────
//
// The attachment object has a relative `url` like "/_persistent/foo.pdf?...".
// We fetch base+url with the same auth and stream bytes to disk.

type downloadAttachmentAction struct{}

func (downloadAttachmentAction) Name() string         { return "download-attachment" }
func (downloadAttachmentAction) DisplayName() string  { return "Download attachment" }
func (downloadAttachmentAction) Description() string  { return "Stream an issue attachment to a local file." }
func (downloadAttachmentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "issue", Kind: connector.FieldString, Required: true},
		{Name: "attachment_id", Kind: connector.FieldString, Required: true, Description: "From list-attachments."},
		{Name: "out", Kind: connector.FieldString, Required: true, Description: "Output file path."},
	}}
}

func (a downloadAttachmentAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	issue := params.String("issue")
	aid := params.String("attachment_id")
	out := params.String("out")
	if issue == "" || aid == "" || out == "" {
		return nil, errors.New("issue, attachment_id and out are required")
	}

	// 1. Resolve the attachment metadata to get its url.
	q := url.Values{}
	q.Set("fields", "id,name,url,size,mimeType")
	var meta struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		URL  string `json:"url"`
		Size int64  `json:"size"`
	}
	apath := "/api/issues/" + url.PathEscape(issue) + "/attachments/" + url.PathEscape(aid)
	if err := s.http.GetJSON(ctx, apath, q, &meta); err != nil {
		return nil, err
	}
	if meta.URL == "" {
		return nil, errors.New("attachment has no url")
	}

	// 2. Stream the binary. The attachment's URL is a host-relative path
	//    like /_persistent/...  Do() handles relative paths against the
	//    same base, so we pass it through.
	resp, err := s.http.Do(ctx, "GET", meta.URL, nil, "")
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("download: http %d: %s", resp.StatusCode, string(body))
	}
	f, err := os.Create(out)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	n, err := io.Copy(f, resp.Body)
	if err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	return map[string]any{"saved": true, "path": out, "size": n, "name": meta.Name}, nil
}

// ── add-comment ───────────────────────────────────────────────────────────

type addCommentAction struct{}

func (addCommentAction) Name() string         { return "add-comment" }
func (addCommentAction) DisplayName() string  { return "Add comment" }
func (addCommentAction) Description() string  { return "Add a comment to an issue. Issue id can be the readable form (e.g. PGFT-1)." }
func (addCommentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "issue", Kind: connector.FieldString, Required: true, Description: "Readable id (PGFT-1) or internal id."},
		{Name: "text", Kind: connector.FieldLongText, Required: true},
		{Name: "uses_markdown", Kind: connector.FieldBool, Default: true},
	}}
}

func (a addCommentAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	issue := params.String("issue")
	text := params.String("text")
	if issue == "" || text == "" {
		return nil, errors.New("issue and text are required")
	}
	body := map[string]any{
		"text":         text,
		"usesMarkdown": params.Bool("uses_markdown"),
	}
	q := url.Values{}
	q.Set("fields", "id,text,usesMarkdown,created,updated,author(login,fullName)")
	var out map[string]any
	path := "/api/issues/" + url.PathEscape(issue) + "/comments?" + q.Encode()
	if err := s.http.PostJSON(ctx, path, body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ── create-issue ──────────────────────────────────────────────────────────

type createIssueAction struct{}

func (createIssueAction) Name() string         { return "create-issue" }
func (createIssueAction) DisplayName() string  { return "Create issue" }
func (createIssueAction) Description() string  { return "Create an issue. Pass project_key (resolved internally) plus summary; description and assignee_login optional." }
func (createIssueAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_key", Kind: connector.FieldString, Required: true, Description: "Project shortName (e.g. MW) or id."},
		{Name: "summary", Kind: connector.FieldString, Required: true},
		{Name: "description", Kind: connector.FieldLongText},
		{Name: "assignee_login", Kind: connector.FieldString, Description: "Set the Assignee custom field by user login."},
	}}
}

func (a createIssueAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pk := params.String("project_key")
	summary := params.String("summary")
	if pk == "" || summary == "" {
		return nil, errors.New("project_key and summary are required")
	}
	projectID, err := resolveProjectID(ctx, s.http, pk)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"project": map[string]any{"id": projectID},
		"summary": summary,
	}
	if d := params.String("description"); d != "" {
		body["description"] = d
	}
	if assignee := params.String("assignee_login"); assignee != "" {
		body["customFields"] = []map[string]any{{
			"name":  "Assignee",
			"$type": "SingleUserIssueCustomField",
			"value": map[string]any{"login": assignee},
		}}
	}
	q := url.Values{}
	q.Set("fields", issueFields)
	var out map[string]any
	if err := s.http.PostJSON(ctx, "/api/issues?"+q.Encode(), body, &out); err != nil {
		return nil, err
	}
	return out, nil
}
