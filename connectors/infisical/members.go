package infisical

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// orgIDOr resolves the org_id param or falls back to default_org_id on
// the credential. Returns "" if neither is set so the caller can give a
// clear error.
func orgIDOr(s *session, params connector.Values) string {
	if v := params.String("org_id"); v != "" {
		return v
	}
	return s.cred.Values.String(fDefaultOrgID)
}

// ── list-org-members ──────────────────────────────────────────────────────

type listOrgMembersAction struct{}

func (listOrgMembersAction) Name() string         { return "list-org-members" }
func (listOrgMembersAction) DisplayName() string  { return "List org members (users)" }
func (listOrgMembersAction) Description() string  { return "User accounts in the organization. Use this to find membership ids needed by update-org-member-role / remove-org-member." }
func (listOrgMembersAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "org_id", Kind: connector.FieldString,
			Description: "Organization id. Falls back to default_org_id."},
	}}
}

func (a listOrgMembersAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	orgID := orgIDOr(s, params)
	if orgID == "" {
		return nil, errors.New("org_id is required (or set default_org_id on the credential)")
	}
	var resp struct {
		Memberships []map[string]any `json:"memberships"`
	}
	if err := s.http.GetJSON(ctx, "/api/v2/organizations/"+url.PathEscape(orgID)+"/memberships", nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"org_id":      orgID,
		"count":       len(resp.Memberships),
		"memberships": resp.Memberships,
	}, nil
}

// ── update-org-member-role ────────────────────────────────────────────────

type updateOrgMemberRoleAction struct{}

func (updateOrgMemberRoleAction) Name() string         { return "update-org-member-role" }
func (updateOrgMemberRoleAction) DisplayName() string  { return "Update org member role" }
func (updateOrgMemberRoleAction) Description() string  { return "Change a user's org role (admin / member / no-access / custom-role slug). Use the membership id from list-org-members." }
func (updateOrgMemberRoleAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "org_id", Kind: connector.FieldString,
			Description: "Organization id. Falls back to default_org_id."},
		{Name: "membership_id", Kind: connector.FieldString, Required: true,
			Description: "Membership id (NOT the user id). Get it from list-org-members → memberships[].id."},
		{Name: "role", Kind: connector.FieldString,
			Description: "New role slug. Built-ins: admin, member, no-access. Custom roles are also supported by slug."},
		{Name: "is_active", Kind: connector.FieldBool,
			Description: "Toggle the membership active state. Deactivated users can't sign in but their data is preserved."},
	}}
}

func (a updateOrgMemberRoleAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	orgID := orgIDOr(s, params)
	mid := params.String("membership_id")
	if orgID == "" || mid == "" {
		return nil, errors.New("org_id and membership_id are required")
	}
	body := map[string]any{}
	if r := params.String("role"); r != "" {
		body["role"] = r
	}
	if params.Has("is_active") {
		body["isActive"] = params.Bool("is_active")
	}
	if len(body) == 0 {
		return nil, errors.New("at least one of role or is_active is required")
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "PATCH",
		fmt.Sprintf("/api/v2/organizations/%s/memberships/%s", url.PathEscape(orgID), url.PathEscape(mid)),
		body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── remove-org-member ─────────────────────────────────────────────────────

type removeOrgMemberAction struct{}

func (removeOrgMemberAction) Name() string         { return "remove-org-member" }
func (removeOrgMemberAction) DisplayName() string  { return "Remove user from org" }
func (removeOrgMemberAction) Description() string  { return "Detach a user from the organization. The user's account on the homeserver stays — they just lose access to this org's resources." }
func (removeOrgMemberAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "org_id", Kind: connector.FieldString, Required: true,
			Description: "Organization id. Explicit — no fallback for destructive ops."},
		{Name: "membership_id", Kind: connector.FieldString, Required: true,
			Description: "Membership id from list-org-members."},
	}}
}

func (a removeOrgMemberAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	orgID := params.String("org_id")
	mid := params.String("membership_id")
	if orgID == "" || mid == "" {
		return nil, errors.New("org_id and membership_id are required")
	}
	if err := s.http.SendJSON(ctx, "DELETE",
		fmt.Sprintf("/api/v2/organizations/%s/memberships/%s", url.PathEscape(orgID), url.PathEscape(mid)),
		nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"org_id":        orgID,
		"membership_id": mid,
		"removed":       true,
	}, nil
}

// ── list-project-members ──────────────────────────────────────────────────

type listProjectMembersAction struct{}

func (listProjectMembersAction) Name() string         { return "list-project-members" }
func (listProjectMembersAction) DisplayName() string  { return "List project members (users)" }
func (listProjectMembersAction) Description() string  { return "Users with access to a specific project. Use the membership id from this list with update-project-member-role / remove-project-member." }
func (listProjectMembersAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id."},
	}}
}

func (a listProjectMembersAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	var resp struct {
		Memberships []map[string]any `json:"memberships"`
	}
	if err := s.http.GetJSON(ctx, "/api/v1/workspace/"+url.PathEscape(pid)+"/memberships", nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id":  pid,
		"count":       len(resp.Memberships),
		"memberships": resp.Memberships,
	}, nil
}

// ── add-project-member ────────────────────────────────────────────────────
//
// Add EXISTING org users to a project. Infisical doesn't have a single
// "invite to project" endpoint that creates accounts — users must
// already exist in the org (via web-UI invite/signup), then this action
// grants them access to a specific project.

type addProjectMemberAction struct{}

func (addProjectMemberAction) Name() string         { return "add-project-member" }
func (addProjectMemberAction) DisplayName() string  { return "Add existing org users to a project" }
func (addProjectMemberAction) Description() string  { return "Grant project access to one or more existing organization members. Users must already exist in the org (created via web-UI invite). Pass either emails OR usernames, plus the role slugs." }
func (addProjectMemberAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id."},
		{Name: "emails", Kind: connector.FieldStringList,
			Description: "Comma-separated emails. e.g. alice@example.com,bob@example.com. Mutually exclusive with usernames."},
		{Name: "usernames", Kind: connector.FieldStringList,
			Description: "Comma-separated usernames. Mutually exclusive with emails. Use whichever your org's user identifier is."},
		{Name: "roles", Kind: connector.FieldStringList, Default: "member",
			Description: "Role slugs to assign on the new project membership. Default 'member'. Other built-ins: admin, viewer, no-access. Custom roles also work by slug."},
	}}
}

func (a addProjectMemberAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	emails := params.StringList("emails")
	usernames := params.StringList("usernames")
	if len(emails) == 0 && len(usernames) == 0 {
		return nil, errors.New("either emails or usernames is required")
	}
	if len(emails) > 0 && len(usernames) > 0 {
		return nil, errors.New("emails and usernames are mutually exclusive — pick one")
	}
	roles := params.StringList("roles")
	if len(roles) == 0 {
		roles = []string{"member"}
	}
	body := map[string]any{"roleSlugs": roles}
	if len(emails) > 0 {
		body["emails"] = emails
	} else {
		body["usernames"] = usernames
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST",
		"/api/v2/workspace/"+url.PathEscape(pid)+"/memberships",
		body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── update-project-member-role ────────────────────────────────────────────

type updateProjectMemberRoleAction struct{}

func (updateProjectMemberRoleAction) Name() string         { return "update-project-member-role" }
func (updateProjectMemberRoleAction) DisplayName() string  { return "Update project member's role(s)" }
func (updateProjectMemberRoleAction) Description() string  { return "Replace the role set on a project membership. Roles is a list — Infisical supports stacking multiple roles per user." }
func (updateProjectMemberRoleAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id."},
		{Name: "membership_id", Kind: connector.FieldString, Required: true,
			Description: "Membership id from list-project-members (NOT the user id)."},
		{Name: "roles", Kind: connector.FieldStringList, Required: true,
			Description: "Comma-separated role slugs. e.g. admin or admin,viewer. Replaces the current role set entirely."},
	}}
}

func (a updateProjectMemberRoleAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	mid := params.String("membership_id")
	roles := params.StringList("roles")
	if pid == "" || mid == "" || len(roles) == 0 {
		return nil, errors.New("project_id, membership_id, and at least one role are required")
	}
	// Roles is an array of objects shaped like {role: <slug>}.
	roleObjs := make([]map[string]any, 0, len(roles))
	for _, r := range roles {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		roleObjs = append(roleObjs, map[string]any{"role": r})
	}
	body := map[string]any{"roles": roleObjs}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "PATCH",
		fmt.Sprintf("/api/v1/workspace/%s/memberships/%s", url.PathEscape(pid), url.PathEscape(mid)),
		body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── remove-project-member ─────────────────────────────────────────────────

type removeProjectMemberAction struct{}

func (removeProjectMemberAction) Name() string         { return "remove-project-member" }
func (removeProjectMemberAction) DisplayName() string  { return "Remove user(s) from project" }
func (removeProjectMemberAction) Description() string  { return "Detach users from a project. Pass either emails OR usernames. Users keep their org membership and other projects." }
func (removeProjectMemberAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString, Required: true,
			Description: "Project id. Explicit — no fallback for destructive ops."},
		{Name: "emails", Kind: connector.FieldStringList,
			Description: "Comma-separated emails to remove. Mutually exclusive with usernames."},
		{Name: "usernames", Kind: connector.FieldStringList,
			Description: "Comma-separated usernames to remove. Mutually exclusive with emails."},
	}}
}

func (a removeProjectMemberAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := params.String("project_id")
	emails := params.StringList("emails")
	usernames := params.StringList("usernames")
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	if len(emails) == 0 && len(usernames) == 0 {
		return nil, errors.New("either emails or usernames is required")
	}
	if len(emails) > 0 && len(usernames) > 0 {
		return nil, errors.New("emails and usernames are mutually exclusive")
	}
	body := map[string]any{}
	if len(emails) > 0 {
		body["emails"] = emails
	} else {
		body["usernames"] = usernames
	}
	if err := s.http.SendJSON(ctx, "DELETE",
		"/api/v2/workspace/"+url.PathEscape(pid)+"/memberships",
		body, nil); err != nil {
		return nil, err
	}
	out := map[string]any{
		"project_id": pid,
		"removed":    true,
	}
	if len(emails) > 0 {
		out["emails"] = emails
	} else {
		out["usernames"] = usernames
	}
	return out, nil
}

// ── list-project-identities ───────────────────────────────────────────────
//
// Machine identities with access to the project (the "I" side of users
// + identities). This is how you'd see every CI/CD service, agent, or
// other pgf instance currently authorized on the project.

type listProjectIdentitiesAction struct{}

func (listProjectIdentitiesAction) Name() string         { return "list-project-identities" }
func (listProjectIdentitiesAction) DisplayName() string  { return "List machine identities with project access" }
func (listProjectIdentitiesAction) Description() string  { return "Every machine identity (Universal Auth, AWS, K8s, ...) granted access to the project. Distinct from list-project-members which lists user accounts." }
func (listProjectIdentitiesAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id."},
	}}
}

func (a listProjectIdentitiesAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	var resp map[string]any
	if err := s.http.GetJSON(ctx, "/api/v2/workspace/"+url.PathEscape(pid)+"/identity-memberships", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── add-project-identity ──────────────────────────────────────────────────

type addProjectIdentityAction struct{}

func (addProjectIdentityAction) Name() string         { return "add-project-identity" }
func (addProjectIdentityAction) DisplayName() string  { return "Grant machine identity access to a project" }
func (addProjectIdentityAction) Description() string  { return "Authorize an existing machine identity on a project with one or more role slugs. The identity itself must already exist (created in the Infisical UI's Identities settings)." }
func (addProjectIdentityAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id."},
		{Name: "identity_id", Kind: connector.FieldString, Required: true,
			Description: "Identity id (from the Infisical UI, or a future list-identities action)."},
		{Name: "roles", Kind: connector.FieldStringList, Default: "member",
			Description: "Role slugs to assign. Default 'member'. e.g. admin, viewer."},
	}}
}

func (a addProjectIdentityAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	iid := params.String("identity_id")
	if pid == "" || iid == "" {
		return nil, errors.New("project_id and identity_id are required")
	}
	roles := params.StringList("roles")
	if len(roles) == 0 {
		roles = []string{"member"}
	}
	roleObjs := make([]map[string]any, 0, len(roles))
	for _, r := range roles {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		roleObjs = append(roleObjs, map[string]any{"role": r})
	}
	body := map[string]any{"roles": roleObjs}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST",
		fmt.Sprintf("/api/v2/workspace/%s/identity-memberships/%s", url.PathEscape(pid), url.PathEscape(iid)),
		body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── remove-project-identity ───────────────────────────────────────────────

type removeProjectIdentityAction struct{}

func (removeProjectIdentityAction) Name() string         { return "remove-project-identity" }
func (removeProjectIdentityAction) DisplayName() string  { return "Revoke machine identity from project" }
func (removeProjectIdentityAction) Description() string  { return "Remove a machine identity's access to a project. The identity itself stays (use the Infisical UI to delete the identity entirely)." }
func (removeProjectIdentityAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString, Required: true,
			Description: "Project id. Explicit — no fallback for destructive ops."},
		{Name: "identity_id", Kind: connector.FieldString, Required: true,
			Description: "Identity id whose project access should be revoked."},
	}}
}

func (a removeProjectIdentityAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := params.String("project_id")
	iid := params.String("identity_id")
	if pid == "" || iid == "" {
		return nil, errors.New("project_id and identity_id are required")
	}
	if err := s.http.SendJSON(ctx, "DELETE",
		fmt.Sprintf("/api/v2/workspace/%s/identity-memberships/%s", url.PathEscape(pid), url.PathEscape(iid)),
		nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id":  pid,
		"identity_id": iid,
		"removed":     true,
	}, nil
}
