package infisical

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// scope is the resolved (projectId, environment, secretPath) triple
// every secret/folder action needs. The agent passes them as params,
// or falls back to the credential's defaults.
type scope struct {
	ProjectID   string
	Environment string
	SecretPath  string
}

func (s *session) resolveScope(params connector.Values) (scope, error) {
	sc := scope{
		ProjectID:   params.String("project_id"),
		Environment: params.String("environment"),
		SecretPath:  params.String("secret_path"),
	}
	if sc.ProjectID == "" {
		sc.ProjectID = s.cred.Values.String(fDefaultProjectID)
	}
	if sc.Environment == "" {
		sc.Environment = s.cred.Values.String(fDefaultEnvironment)
	}
	if sc.SecretPath == "" {
		sc.SecretPath = "/"
	}
	if sc.ProjectID == "" {
		return sc, errors.New("project_id is required (or set default_project_id on the credential)")
	}
	if sc.Environment == "" {
		return sc, errors.New("environment is required (or set default_environment on the credential)")
	}
	return sc, nil
}

// scopeQuery turns a scope into URL query values — Infisical's read
// endpoints take these as query params.
func (sc scope) query() url.Values {
	q := url.Values{}
	q.Set("workspaceId", sc.ProjectID)
	q.Set("environment", sc.Environment)
	q.Set("secretPath", sc.SecretPath)
	return q
}

// ── list-projects ─────────────────────────────────────────────────────────

type listProjectsAction struct{}

func (listProjectsAction) Name() string         { return "list-projects" }
func (listProjectsAction) DisplayName() string  { return "List projects (workspaces)" }
func (listProjectsAction) Description() string  { return "Every project the bound identity can see. Same noun as Infisical's UI; the API still uses 'workspace' in places." }
func (listProjectsAction) Schema() connector.Schema { return connector.Schema{} }

func (listProjectsAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	var resp struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := s.http.GetJSON(ctx, "/api/v1/workspace", nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"count":    len(resp.Workspaces),
		"projects": resp.Workspaces,
	}, nil
}

// ── list-environments ─────────────────────────────────────────────────────

type listEnvironmentsAction struct{}

func (listEnvironmentsAction) Name() string         { return "list-environments" }
func (listEnvironmentsAction) DisplayName() string  { return "List environments in a project" }
func (listEnvironmentsAction) Description() string  { return "Returns the environment slugs (dev/staging/prod/...) configured on the project." }
func (listEnvironmentsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project / workspace id. Falls back to default_project_id on the credential."},
	}}
}

func (a listEnvironmentsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	pid := params.String("project_id")
	if pid == "" {
		pid = s.cred.Values.String(fDefaultProjectID)
	}
	if pid == "" {
		return nil, errors.New("project_id is required")
	}
	// /api/v1/workspace/{id} returns the workspace details including
	// the environments array.
	var resp struct {
		Workspace struct {
			Name         string `json:"name"`
			Environments []map[string]any `json:"environments"`
		} `json:"workspace"`
	}
	if err := s.http.GetJSON(ctx, "/api/v1/workspace/"+url.PathEscape(pid), nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id":   pid,
		"project_name": resp.Workspace.Name,
		"count":        len(resp.Workspace.Environments),
		"environments": resp.Workspace.Environments,
	}, nil
}

// ── list-folders ──────────────────────────────────────────────────────────

type listFoldersAction struct{}

func (listFoldersAction) Name() string         { return "list-folders" }
func (listFoldersAction) DisplayName() string  { return "List folders at a path" }
func (listFoldersAction) Description() string  { return "Direct subfolders of secret_path in the given environment. Folders are organisational containers; they don't carry secrets themselves." }
func (listFoldersAction) Schema() connector.Schema {
	return connector.Schema{Fields: scopeFields()}
}

func (a listFoldersAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	sc, err := s.resolveScope(params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Folders []map[string]any `json:"folders"`
	}
	if err := s.http.GetJSON(ctx, "/api/v1/folders?"+sc.query().Encode(), nil, &resp); err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id":  sc.ProjectID,
		"environment": sc.Environment,
		"secret_path": sc.SecretPath,
		"count":       len(resp.Folders),
		"folders":     resp.Folders,
	}, nil
}

// ── create-folder ─────────────────────────────────────────────────────────

type createFolderAction struct{}

func (createFolderAction) Name() string         { return "create-folder" }
func (createFolderAction) DisplayName() string  { return "Create folder" }
func (createFolderAction) Description() string  { return "Create a folder under secret_path with the given name. The new folder lives at <secret_path>/<name>." }
func (createFolderAction) Schema() connector.Schema {
	return connector.Schema{Fields: append(scopeFields(),
		connector.FieldSpec{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Folder name (the leaf, not the full path)."})}
}

func (a createFolderAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	sc, err := s.resolveScope(params)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(params.String("name"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{
		"workspaceId": sc.ProjectID,
		"environment": sc.Environment,
		"path":        sc.SecretPath,
		"name":        name,
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/api/v1/folders", body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── delete-folder ─────────────────────────────────────────────────────────

type deleteFolderAction struct{}

func (deleteFolderAction) Name() string         { return "delete-folder" }
func (deleteFolderAction) DisplayName() string  { return "Delete folder" }
func (deleteFolderAction) Description() string  { return "Delete a folder. Refuses if the folder contains secrets unless the API is force-deleted in the UI (the API itself will refuse non-empty folders on most versions)." }
func (deleteFolderAction) Schema() connector.Schema {
	return connector.Schema{Fields: append(scopeFields(),
		connector.FieldSpec{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Folder name to delete (leaf, not full path)."})}
}

func (a deleteFolderAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	sc, err := s.resolveScope(params)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(params.String("name"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{
		"workspaceId": sc.ProjectID,
		"environment": sc.Environment,
		"path":        sc.SecretPath,
	}
	if err := s.http.SendJSON(ctx, "DELETE", "/api/v1/folders/"+url.PathEscape(name), body, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id":  sc.ProjectID,
		"environment": sc.Environment,
		"secret_path": sc.SecretPath,
		"name":        name,
		"deleted":     true,
	}, nil
}

// ── list-secrets ──────────────────────────────────────────────────────────

type listSecretsAction struct{}

func (listSecretsAction) Name() string         { return "list-secrets" }
func (listSecretsAction) DisplayName() string  { return "List secrets at a path" }
func (listSecretsAction) Description() string  { return "Every secret at <secret_path> in <environment>. Uses the /raw endpoint so values are returned in plaintext (server-side decryption). Requires the workspace to have E2EE disabled." }
func (listSecretsAction) Schema() connector.Schema {
	return connector.Schema{Fields: append(scopeFields(),
		connector.FieldSpec{Name: "include_imports", Kind: connector.FieldBool, Default: false,
			Description: "Include secrets imported from referenced environments / folders."},
		connector.FieldSpec{Name: "recursive", Kind: connector.FieldBool, Default: false,
			Description: "Walk subfolders. Default false (only secrets directly at secret_path)."})}
}

func (a listSecretsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	sc, err := s.resolveScope(params)
	if err != nil {
		return nil, err
	}
	q := sc.query()
	if params.Bool("include_imports") {
		q.Set("include_imports", "true")
	}
	if params.Bool("recursive") {
		q.Set("recursive", "true")
	}
	var resp struct {
		Secrets []map[string]any `json:"secrets"`
		Imports []map[string]any `json:"imports,omitempty"`
	}
	if err := s.http.GetJSON(ctx, "/api/v3/secrets/raw?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	out := map[string]any{
		"project_id":  sc.ProjectID,
		"environment": sc.Environment,
		"secret_path": sc.SecretPath,
		"count":       len(resp.Secrets),
		"secrets":     resp.Secrets,
	}
	if len(resp.Imports) > 0 {
		out["imports"] = resp.Imports
	}
	return out, nil
}

// ── get-secret ────────────────────────────────────────────────────────────

type getSecretAction struct{}

func (getSecretAction) Name() string         { return "get-secret" }
func (getSecretAction) DisplayName() string  { return "Get one secret" }
func (getSecretAction) Description() string  { return "Single secret by name. The plaintext value is in `secret.secretValue` of the response." }
func (getSecretAction) Schema() connector.Schema {
	return connector.Schema{Fields: append(scopeFields(),
		connector.FieldSpec{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Secret key name. e.g. STRIPE_API_KEY."},
		connector.FieldSpec{Name: "type", Kind: connector.FieldEnum, Default: "shared",
			Options: []connector.EnumOption{
				{Value: "shared", Label: "Shared (default — visible to whole project)"},
				{Value: "personal", Label: "Personal (overrides shared, only your identity sees)"},
			},
			Description: "Secret type. Personal secrets shadow the shared value for the calling identity."})}
}

func (a getSecretAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	sc, err := s.resolveScope(params)
	if err != nil {
		return nil, err
	}
	name := params.String("name")
	if name == "" {
		return nil, errors.New("name is required")
	}
	q := sc.query()
	q.Set("type", params.String("type"))
	var resp map[string]any
	if err := s.http.GetJSON(ctx, "/api/v3/secrets/raw/"+url.PathEscape(name)+"?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── create-secret ─────────────────────────────────────────────────────────

type createSecretAction struct{}

func (createSecretAction) Name() string         { return "create-secret" }
func (createSecretAction) DisplayName() string  { return "Create a secret" }
func (createSecretAction) Description() string  { return "Add a new secret at <secret_path>. Fails if a secret of that name already exists; use update-secret to overwrite." }
func (createSecretAction) Schema() connector.Schema {
	return connector.Schema{Fields: append(scopeFields(),
		connector.FieldSpec{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Secret key (uppercase + underscores conventionally)."},
		connector.FieldSpec{Name: "value", Kind: connector.FieldSecret, Required: true,
			Description: "Plaintext value. The connector sends it over TLS; Infisical encrypts at rest."},
		connector.FieldSpec{Name: "comment", Kind: connector.FieldString,
			Description: "Optional description shown in the Infisical UI."},
		connector.FieldSpec{Name: "type", Kind: connector.FieldEnum, Default: "shared",
			Options: []connector.EnumOption{
				{Value: "shared", Label: "Shared"},
				{Value: "personal", Label: "Personal (overrides shared for this identity)"},
			}})}
}

func (a createSecretAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	sc, err := s.resolveScope(params)
	if err != nil {
		return nil, err
	}
	name := params.String("name")
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{
		"workspaceId": sc.ProjectID,
		"environment": sc.Environment,
		"secretPath":  sc.SecretPath,
		"secretValue": params.String("value"),
		"type":        params.String("type"),
	}
	if c := params.String("comment"); c != "" {
		body["secretComment"] = c
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/api/v3/secrets/raw/"+url.PathEscape(name), body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── update-secret ─────────────────────────────────────────────────────────

type updateSecretAction struct{}

func (updateSecretAction) Name() string         { return "update-secret" }
func (updateSecretAction) DisplayName() string  { return "Update a secret" }
func (updateSecretAction) Description() string  { return "Change value / comment of an existing secret. Use rename via the UI — there's no rename op in the raw API." }
func (updateSecretAction) Schema() connector.Schema {
	return connector.Schema{Fields: append(scopeFields(),
		connector.FieldSpec{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Existing secret key."},
		connector.FieldSpec{Name: "value", Kind: connector.FieldSecret,
			Description: "New value. Omit to leave unchanged."},
		connector.FieldSpec{Name: "comment", Kind: connector.FieldString,
			Description: "Update the comment."},
		connector.FieldSpec{Name: "type", Kind: connector.FieldEnum, Default: "shared",
			Options: []connector.EnumOption{
				{Value: "shared", Label: "Shared"},
				{Value: "personal", Label: "Personal"},
			}})}
}

func (a updateSecretAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	sc, err := s.resolveScope(params)
	if err != nil {
		return nil, err
	}
	name := params.String("name")
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{
		"workspaceId": sc.ProjectID,
		"environment": sc.Environment,
		"secretPath":  sc.SecretPath,
		"type":        params.String("type"),
	}
	if params.Has("value") {
		body["secretValue"] = params.String("value")
	}
	if params.Has("comment") {
		body["secretComment"] = params.String("comment")
	}
	if len(body) <= 4 { // only the scope+type — nothing to update
		return nil, errors.New("at least one of value or comment must be supplied")
	}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "PATCH", "/api/v3/secrets/raw/"+url.PathEscape(name), body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── delete-secret ─────────────────────────────────────────────────────────

type deleteSecretAction struct{}

func (deleteSecretAction) Name() string         { return "delete-secret" }
func (deleteSecretAction) DisplayName() string  { return "Delete a secret" }
func (deleteSecretAction) Description() string  { return "Remove a secret. Irreversible (no soft delete)." }
func (deleteSecretAction) Schema() connector.Schema {
	return connector.Schema{Fields: append(scopeFields(),
		connector.FieldSpec{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Secret key to delete."},
		connector.FieldSpec{Name: "type", Kind: connector.FieldEnum, Default: "shared",
			Options: []connector.EnumOption{
				{Value: "shared", Label: "Shared"},
				{Value: "personal", Label: "Personal"},
			}})}
}

func (a deleteSecretAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	sc, err := s.resolveScope(params)
	if err != nil {
		return nil, err
	}
	name := params.String("name")
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{
		"workspaceId": sc.ProjectID,
		"environment": sc.Environment,
		"secretPath":  sc.SecretPath,
		"type":        params.String("type"),
	}
	if err := s.http.SendJSON(ctx, "DELETE", "/api/v3/secrets/raw/"+url.PathEscape(name), body, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"project_id":  sc.ProjectID,
		"environment": sc.Environment,
		"secret_path": sc.SecretPath,
		"name":        name,
		"deleted":     true,
	}, nil
}

// scopeFields is the (project_id, environment, secret_path) trio shared
// by every action that operates on a secret or folder. Centralised so
// adding e.g. a `tag_ids` filter to scope happens in one place.
func scopeFields() []connector.FieldSpec {
	return []connector.FieldSpec{
		{Name: "project_id", Kind: connector.FieldString,
			Description: "Project id. Falls back to default_project_id on the credential."},
		{Name: "environment", Kind: connector.FieldString,
			Description: "Environment slug (dev/staging/prod). Falls back to default_environment."},
		{Name: "secret_path", Kind: connector.FieldString, Default: "/",
			Description: "Folder path. e.g. /, /backend, /backend/db."},
	}
}

// Quiet a Go-vet warning: fmt is used by future error variants, keep
// the import live to avoid churn when extending the connector.
var _ = fmt.Sprintf
