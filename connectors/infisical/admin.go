package infisical

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// ── create-project ────────────────────────────────────────────────────────
//
// POST /api/v2/workspace — body: { projectName, slug?, projectDescription?,
// shouldCreateDefaultEnvs?, type?, template? }. Returns the new project
// with its id, slug, and (if shouldCreateDefaultEnvs=true) the auto-
// created dev/staging/prod envs.

type createProjectAction struct{}

func (createProjectAction) Name() string         { return "create-project" }
func (createProjectAction) DisplayName() string  { return "Create project (workspace)" }
func (createProjectAction) Description() string  { return "Create a new Infisical project. Optionally pre-creates default dev/staging/prod environments — recommended for new projects so secrets have somewhere to land immediately." }
func (createProjectAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Project display name. e.g. 'noesis-backend'."},
		{Name: "slug", Kind: connector.FieldString,
			Description: "Optional slug. Inferred from name if omitted."},
		{Name: "description", Kind: connector.FieldString,
			Description: "Optional description shown in the UI."},
		{Name: "create_default_envs", Kind: connector.FieldBool, Default: true,
			Description: "Pre-create dev/staging/prod environments. Disable if you want to define environments yourself via create-environment."},
		{Name: "type", Kind: connector.FieldEnum, Default: "secret-manager",
			Options: []connector.EnumOption{
				{Value: "secret-manager", Label: "Secret manager (default)"},
				{Value: "cert-manager", Label: "Certificate manager"},
				{Value: "kms", Label: "KMS"},
				{Value: "ssh", Label: "SSH"},
			},
			Description: "Project type. Most projects are secret-manager; cert-manager / kms / ssh are specialised."},
	}}
}

func (a createProjectAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	name := strings.TrimSpace(params.String("name"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{
		"projectName":             name,
		"shouldCreateDefaultEnvs": params.Bool("create_default_envs"),
		"type":                    params.String("type"),
	}
	if slug := params.String("slug"); slug != "" {
		body["slug"] = slug
	}
	if desc := params.String("description"); desc != "" {
		body["projectDescription"] = desc
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/api/v2/workspace", body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── get-project ───────────────────────────────────────────────────────────

type getProjectAction struct{}

func (getProjectAction) Name() string         { return "get-project" }
func (getProjectAction) DisplayName() string  { return "Get project details" }
func (getProjectAction) Description() string  { return "Full project shape: name, slug, description, environments, settings (auto-capitalisation, delete-protection, ...)." }
func (getProjectAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id on the credential."},
	}}
}

func (a getProjectAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	var resp map[string]any
	if err := s.http.GetJSON(ctx, "/api/v1/workspace/"+url.PathEscape(pid), nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── update-project ────────────────────────────────────────────────────────

type updateProjectAction struct{}

func (updateProjectAction) Name() string         { return "update-project" }
func (updateProjectAction) DisplayName() string  { return "Update project metadata" }
func (updateProjectAction) Description() string  { return "Partial update — only the fields passed are changed. Useful for renaming, description, enabling delete-protection." }
func (updateProjectAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id."},
		{Name: "name", Kind: connector.FieldString,
			Description: "New display name."},
		{Name: "slug", Kind: connector.FieldString,
			Description: "New slug. Must be unique within the org."},
		{Name: "description", Kind: connector.FieldString,
			Description: "Update description."},
		{Name: "auto_capitalization", Kind: connector.FieldBool,
			Description: "Toggle auto-capitalisation of secret keys on input."},
		{Name: "delete_protection", Kind: connector.FieldBool,
			Description: "Toggle delete-protection. With protection on, delete-project will be refused until you turn it off."},
		{Name: "secret_sharing", Kind: connector.FieldBool,
			Description: "Toggle secret-sharing feature."},
	}}
}

func (a updateProjectAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	body := map[string]any{}
	if v := params.String("name"); v != "" {
		body["name"] = v
	}
	if v := params.String("slug"); v != "" {
		body["slug"] = v
	}
	if v := params.String("description"); v != "" {
		body["description"] = v
	}
	if params.Has("auto_capitalization") {
		body["autoCapitalization"] = params.Bool("auto_capitalization")
	}
	if params.Has("delete_protection") {
		body["hasDeleteProtection"] = params.Bool("delete_protection")
	}
	if params.Has("secret_sharing") {
		body["secretSharing"] = params.Bool("secret_sharing")
	}
	if len(body) == 0 {
		return nil, errors.New("at least one field to update is required")
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "PATCH", "/api/v1/workspace/"+url.PathEscape(pid), body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── delete-project ────────────────────────────────────────────────────────

type deleteProjectAction struct{}

func (deleteProjectAction) Name() string         { return "delete-project" }
func (deleteProjectAction) DisplayName() string  { return "Delete project" }
func (deleteProjectAction) Description() string  { return "Permanently remove a project and all its secrets, environments, folders. Refused when delete-protection is enabled — turn it off via update-project first." }
func (deleteProjectAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString, Required: true,
			Description: "Project id. NOT pulled from default_project_id — destructive operations require an explicit id so the agent can't accidentally drop your default project."},
	}}
}

func (a deleteProjectAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := params.String("project_id")
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "DELETE", "/api/v1/workspace/"+url.PathEscape(pid), nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id": pid,
		"deleted":    true,
		"workspace":  resp,
	}, nil
}

// ── create-environment ────────────────────────────────────────────────────

type createEnvironmentAction struct{}

func (createEnvironmentAction) Name() string         { return "create-environment" }
func (createEnvironmentAction) DisplayName() string  { return "Add environment to a project" }
func (createEnvironmentAction) Description() string  { return "Create a new environment (slug = url-safe name used in secret-list calls). Lowest 'position' value is the project's default environment in the UI." }
func (createEnvironmentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id."},
		{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Display name. e.g. 'Production'."},
		{Name: "slug", Kind: connector.FieldString, Required: true,
			Description: "Url-safe slug. e.g. 'prod'. This is what secret-list / get-secret use to identify the environment."},
		{Name: "position", Kind: connector.FieldInt,
			Description: "Sort position. Lower = earlier in the UI. Default = end of list."},
	}}
}

func (a createEnvironmentAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	name := strings.TrimSpace(params.String("name"))
	slug := strings.TrimSpace(params.String("slug"))
	if name == "" || slug == "" {
		return nil, errors.New("name and slug are required")
	}
	body := map[string]any{
		"name": name,
		"slug": slug,
	}
	if params.Has("position") {
		body["position"] = params.Int("position")
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/api/v1/workspace/"+url.PathEscape(pid)+"/environments", body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── update-environment ────────────────────────────────────────────────────

type updateEnvironmentAction struct{}

func (updateEnvironmentAction) Name() string         { return "update-environment" }
func (updateEnvironmentAction) DisplayName() string  { return "Update environment" }
func (updateEnvironmentAction) Description() string  { return "Rename or reorder an environment. Changing the slug requires all callers (CI/CD, agent scripts) to use the new slug — handle with care." }
func (updateEnvironmentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id."},
		{Name: "environment_id", Kind: connector.FieldString, Required: true,
			Description: "Internal id of the environment (NOT the slug). Get it from get-project → environments[].id."},
		{Name: "name", Kind: connector.FieldString,
			Description: "New display name."},
		{Name: "slug", Kind: connector.FieldString,
			Description: "New slug. Use sparingly — breaks callers."},
		{Name: "position", Kind: connector.FieldInt,
			Description: "New sort position."},
	}}
}

func (a updateEnvironmentAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := projectIDOr(s, params)
	envID := params.String("environment_id")
	if pid == "" || envID == "" {
		return nil, errors.New("project_id and environment_id are required")
	}
	body := map[string]any{}
	if v := params.String("name"); v != "" {
		body["name"] = v
	}
	if v := params.String("slug"); v != "" {
		body["slug"] = v
	}
	if params.Has("position") {
		body["position"] = params.Int("position")
	}
	if len(body) == 0 {
		return nil, errors.New("at least one of name / slug / position is required")
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "PATCH",
		fmt.Sprintf("/api/v1/workspace/%s/environments/%s", url.PathEscape(pid), url.PathEscape(envID)),
		body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── delete-environment ────────────────────────────────────────────────────

type deleteEnvironmentAction struct{}

func (deleteEnvironmentAction) Name() string         { return "delete-environment" }
func (deleteEnvironmentAction) DisplayName() string  { return "Delete environment" }
func (deleteEnvironmentAction) Description() string  { return "Remove an environment from a project. All secrets in that environment are deleted permanently. Refused if the environment is the project's only one." }
func (deleteEnvironmentAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString, Required: true,
			Description: "Project id. Explicit — no fallback for destructive ops."},
		{Name: "environment_id", Kind: connector.FieldString, Required: true,
			Description: "Internal id of the environment to delete."},
	}}
}

func (a deleteEnvironmentAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := params.String("project_id")
	envID := params.String("environment_id")
	if pid == "" || envID == "" {
		return nil, errors.New("project_id and environment_id are required")
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "DELETE",
		fmt.Sprintf("/api/v1/workspace/%s/environments/%s", url.PathEscape(pid), url.PathEscape(envID)),
		nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id":     pid,
		"environment_id": envID,
		"deleted":        true,
	}, nil
}

// projectIDOr returns the explicit param if set, else the credential's
// default. Used by non-destructive actions; destructive ones (delete-*)
// deliberately don't fall back so an agent can't drop a default
// resource by omission.
func projectIDOr(s *session, params connector.Values) string {
	if pid := params.String("project_id"); pid != "" {
		return pid
	}
	return s.cred.Values.String(fDefaultProjectID)
}
