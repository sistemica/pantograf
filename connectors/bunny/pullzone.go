package bunny

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// pullZone is the shape we project from Bunny's PullZoneModel — only the
// fields useful for our actions. Bunny's full model has dozens of fields
// (cache rules, edge scripts, WAF, analytics); expose those via raw=true.
type pullZone struct {
	ID          int64      `json:"Id"`
	Name        string     `json:"Name"`
	OriginURL   string     `json:"OriginUrl"`
	Type        int        `json:"Type"`
	CnameDomain string     `json:"CnameDomain"`
	Enabled     bool       `json:"Enabled"`
	Suspended   bool       `json:"Suspended"`
	Hostnames   []hostname `json:"Hostnames"`
}

type hostname struct {
	ID                int64  `json:"Id"`
	Value             string `json:"Value"`
	ForceSSL          bool   `json:"ForceSSL"`
	IsSystemHostname  bool   `json:"IsSystemHostname"`
	HasCertificate    bool   `json:"HasCertificate"`
}

func pullZoneTypeName(t int) string {
	switch t {
	case 0:
		return "Premium"
	case 1:
		return "Volume"
	default:
		return fmt.Sprintf("Type%d", t)
	}
}

func hostnameView(h hostname) map[string]any {
	out := map[string]any{
		"id":              h.ID,
		"value":           h.Value,
		"force_ssl":       h.ForceSSL,
		"system":          h.IsSystemHostname,
		"has_certificate": h.HasCertificate,
	}
	return out
}

func pullZoneView(p pullZone) map[string]any {
	hosts := make([]map[string]any, 0, len(p.Hostnames))
	for _, h := range p.Hostnames {
		hosts = append(hosts, hostnameView(h))
	}
	return map[string]any{
		"id":           p.ID,
		"name":         p.Name,
		"origin_url":   p.OriginURL,
		"type":         pullZoneTypeName(p.Type),
		"type_code":    p.Type,
		"cname_domain": p.CnameDomain,
		"enabled":      p.Enabled,
		"suspended":    p.Suspended,
		"hostnames":    hosts,
		"host_count":   len(hosts),
	}
}

// ── list-pullzones ────────────────────────────────────────────────────────

type listPullZonesAction struct{}

func (listPullZonesAction) Name() string         { return "list-pullzones" }
func (listPullZonesAction) DisplayName() string  { return "List Pull Zones" }
func (listPullZonesAction) Description() string  { return "Paginated list of CDN Pull Zones on the account. Pull Zones front a single origin (your sistemica.de) and serve it through Bunny's edge under your chosen hostnames." }
func (listPullZonesAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "search", Kind: connector.FieldString,
			Description: "Filter by name substring."},
		{Name: "page", Kind: connector.FieldInt, Default: 1,
			Description: "1-indexed page."},
		{Name: "per_page", Kind: connector.FieldInt, Default: 100,
			Description: "Items per page. Max 1000."},
	}}
}

type pullZonePage struct {
	Items        []pullZone `json:"Items"`
	CurrentPage  int        `json:"CurrentPage"`
	TotalItems   int        `json:"TotalItems"`
	HasMoreItems bool       `json:"HasMoreItems"`
}

func (a listPullZonesAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := url.Values{}
	q.Set("page", fmt.Sprintf("%d", params.Int("page")))
	q.Set("perPage", fmt.Sprintf("%d", params.Int("per_page")))
	if search := params.String("search"); search != "" {
		q.Set("search", search)
	}
	var resp pullZonePage
	if err := s.http.GetJSON(ctx, "/pullzone?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	zones := make([]map[string]any, 0, len(resp.Items))
	for _, p := range resp.Items {
		zones = append(zones, map[string]any{
			"id":           p.ID,
			"name":         p.Name,
			"origin_url":   p.OriginURL,
			"cname_domain": p.CnameDomain,
			"type":         pullZoneTypeName(p.Type),
			"hostnames":    len(p.Hostnames),
		})
	}
	return map[string]any{
		"page":      resp.CurrentPage,
		"total":     resp.TotalItems,
		"has_more":  resp.HasMoreItems,
		"count":     len(zones),
		"pullzones": zones,
	}, nil
}

// ── get-pullzone ──────────────────────────────────────────────────────────

type getPullZoneAction struct{}

func (getPullZoneAction) Name() string         { return "get-pullzone" }
func (getPullZoneAction) DisplayName() string  { return "Get Pull Zone details" }
func (getPullZoneAction) Description() string  { return "Full Pull Zone shape including the hostnames attached to it." }
func (getPullZoneAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldInt, Required: true,
			Description: "Pull Zone id."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return Bunny's full PullZoneModel (cache rules, WAF, edge scripts, ...) instead of the simplified view."},
	}}
}

func (a getPullZoneAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("id")
	if id == 0 {
		return nil, errors.New("id is required")
	}
	if params.Bool("raw") {
		var raw map[string]any
		if err := s.http.GetJSON(ctx, fmt.Sprintf("/pullzone/%d", id), nil, &raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
	var p pullZone
	if err := s.http.GetJSON(ctx, fmt.Sprintf("/pullzone/%d", id), nil, &p); err != nil {
		return nil, err
	}
	return pullZoneView(p), nil
}

// ── create-pullzone ───────────────────────────────────────────────────────

type createPullZoneAction struct{}

func (createPullZoneAction) Name() string         { return "create-pullzone" }
func (createPullZoneAction) DisplayName() string  { return "Create Pull Zone" }
func (createPullZoneAction) Description() string  { return "Create a CDN Pull Zone with a single origin. Bunny issues a *.b-cdn.net hostname; add custom hostnames after creation via add-hostname." }
func (createPullZoneAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Globally-unique pull zone name. Becomes <name>.b-cdn.net. Lowercase, hyphens ok, no dots."},
		{Name: "origin_url", Kind: connector.FieldString, Required: true,
			Description: "Origin URL the zone fetches from. e.g. https://sistemica.de. Scheme + host."},
		{Name: "type", Kind: connector.FieldEnum, Default: "Premium",
			Options: []connector.EnumOption{
				{Value: "Premium", Label: "Premium (code 0) — global, more PoPs"},
				{Value: "Volume", Label: "Volume (code 1) — cheaper, fewer PoPs"},
			},
			Description: "Pull Zone tier. Volume is ~1/3 the price for higher-egress sites with lower latency sensitivity."},
	}}
}

func (a createPullZoneAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	name := strings.TrimSpace(params.String("name"))
	origin := strings.TrimSpace(params.String("origin_url"))
	if name == "" || origin == "" {
		return nil, errors.New("name and origin_url are required")
	}
	typeCode := 0
	if strings.EqualFold(params.String("type"), "Volume") {
		typeCode = 1
	}
	body := map[string]any{
		"Name":      name,
		"OriginUrl": origin,
		"Type":      typeCode,
	}
	var p pullZone
	if err := s.http.SendJSON(ctx, "POST", "/pullzone", body, &p); err != nil {
		return nil, err
	}
	return pullZoneView(p), nil
}

// ── update-pullzone ───────────────────────────────────────────────────────

type updatePullZoneAction struct{}

func (updatePullZoneAction) Name() string         { return "update-pullzone" }
func (updatePullZoneAction) DisplayName() string  { return "Update Pull Zone" }
func (updatePullZoneAction) Description() string  { return "Partial update — change origin URL, type, or enable/disable. For deeper config (cache rules, WAF, edge scripts) use raw JSON via --input." }
func (updatePullZoneAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldInt, Required: true,
			Description: "Pull Zone id."},
		{Name: "origin_url", Kind: connector.FieldString,
			Description: "New origin URL. Omit to leave unchanged."},
		{Name: "enabled", Kind: connector.FieldBool,
			Description: "Enable / disable the zone."},
		{Name: "type", Kind: connector.FieldEnum,
			Options: []connector.EnumOption{
				{Value: "", Label: "(unchanged)"},
				{Value: "Premium", Label: "Premium"},
				{Value: "Volume", Label: "Volume"},
			},
			Description: "Switch tier."},
	}}
}

func (a updatePullZoneAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("id")
	if id == 0 {
		return nil, errors.New("id is required")
	}
	body := map[string]any{}
	if params.Has("origin_url") {
		body["OriginUrl"] = params.String("origin_url")
	}
	if params.Has("enabled") {
		body["Enabled"] = params.Bool("enabled")
	}
	if t := params.String("type"); t != "" {
		switch strings.ToLower(t) {
		case "premium":
			body["Type"] = 0
		case "volume":
			body["Type"] = 1
		}
	}
	if len(body) == 0 {
		return nil, errors.New("at least one field to update is required")
	}
	if err := s.http.SendJSON(ctx, "POST", fmt.Sprintf("/pullzone/%d", id), body, nil); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "updated": true, "fields": body}, nil
}

// ── delete-pullzone ───────────────────────────────────────────────────────

type deletePullZoneAction struct{}

func (deletePullZoneAction) Name() string         { return "delete-pullzone" }
func (deletePullZoneAction) DisplayName() string  { return "Delete Pull Zone" }
func (deletePullZoneAction) Description() string  { return "Permanently remove a Pull Zone and detach all its hostnames." }
func (deletePullZoneAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldInt, Required: true,
			Description: "Pull Zone id."},
	}}
}

func (a deletePullZoneAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("id")
	if id == 0 {
		return nil, errors.New("id is required")
	}
	if err := s.http.SendJSON(ctx, "DELETE", fmt.Sprintf("/pullzone/%d", id), nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "deleted": true}, nil
}

// ── add-hostname ──────────────────────────────────────────────────────────

type addHostnameAction struct{}

func (addHostnameAction) Name() string         { return "add-hostname" }
func (addHostnameAction) DisplayName() string  { return "Attach custom hostname to Pull Zone" }
func (addHostnameAction) Description() string  { return "Attach a custom domain (e.g. sistemica.io) to a Pull Zone. After attaching, point that domain's A records at Bunny's edge IPs or CNAME to <pullzone>.b-cdn.net." }
func (addHostnameAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "pullzone_id", Kind: connector.FieldInt, Required: true,
			Description: "Pull Zone id."},
		{Name: "hostname", Kind: connector.FieldString, Required: true,
			Description: "Custom hostname to attach. e.g. sistemica.io, www.example.com."},
	}}
}

func (a addHostnameAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("pullzone_id")
	host := strings.TrimSpace(params.String("hostname"))
	if id == 0 || host == "" {
		return nil, errors.New("pullzone_id and hostname are required")
	}
	body := map[string]any{"Hostname": host}
	if err := s.http.SendJSON(ctx, "POST", fmt.Sprintf("/pullzone/%d/addHostname", id), body, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"pullzone_id": id,
		"hostname":    host,
		"attached":    true,
	}, nil
}

// ── remove-hostname ───────────────────────────────────────────────────────

type removeHostnameAction struct{}

func (removeHostnameAction) Name() string         { return "remove-hostname" }
func (removeHostnameAction) DisplayName() string  { return "Detach custom hostname from Pull Zone" }
func (removeHostnameAction) Description() string  { return "Detach a hostname. The zone's *.b-cdn.net system hostname stays; only custom hostnames can be removed." }
func (removeHostnameAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "pullzone_id", Kind: connector.FieldInt, Required: true,
			Description: "Pull Zone id."},
		{Name: "hostname", Kind: connector.FieldString, Required: true,
			Description: "Hostname to detach."},
	}}
}

func (a removeHostnameAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("pullzone_id")
	host := strings.TrimSpace(params.String("hostname"))
	if id == 0 || host == "" {
		return nil, errors.New("pullzone_id and hostname are required")
	}
	body := map[string]any{"Hostname": host}
	if err := s.http.SendJSON(ctx, "DELETE", fmt.Sprintf("/pullzone/%d/removeHostname", id), body, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"pullzone_id": id,
		"hostname":    host,
		"detached":    true,
	}, nil
}

// ── load-free-certificate ─────────────────────────────────────────────────
//
// Bunny provisions a free Let's Encrypt certificate for the hostname.
// Requires the hostname to resolve to Bunny's edge first — the issuance
// uses HTTP-01 challenge over the CDN. If DNS isn't pointed at Bunny yet
// the call will fail with an ACME error.

type loadFreeCertificateAction struct{}

func (loadFreeCertificateAction) Name() string         { return "load-free-certificate" }
func (loadFreeCertificateAction) DisplayName() string  { return "Provision free Let's Encrypt cert" }
func (loadFreeCertificateAction) Description() string  { return "Bunny obtains a free Let's Encrypt certificate for the hostname via HTTP-01 challenge. Requires the hostname's DNS to already resolve to Bunny's edge (so the ACME challenge can land)." }
func (loadFreeCertificateAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "hostname", Kind: connector.FieldString, Required: true,
			Description: "Custom hostname (must already be attached to a Pull Zone)."},
	}}
}

func (a loadFreeCertificateAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	host := strings.TrimSpace(params.String("hostname"))
	if host == "" {
		return nil, errors.New("hostname is required")
	}
	q := url.Values{}
	q.Set("hostname", host)
	// GET — the endpoint is documented as GET with query param.
	if err := s.http.GetJSON(ctx, "/pullzone/loadFreeCertificate?"+q.Encode(), nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"hostname":    host,
		"provisioned": true,
	}, nil
}

// ── set-force-ssl ─────────────────────────────────────────────────────────

type setForceSSLAction struct{}

func (setForceSSLAction) Name() string         { return "set-force-ssl" }
func (setForceSSLAction) DisplayName() string  { return "Set Force-SSL on a Pull Zone hostname" }
func (setForceSSLAction) Description() string  { return "Toggle mandatory HTTPS redirect for one hostname on a Pull Zone." }
func (setForceSSLAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "pullzone_id", Kind: connector.FieldInt, Required: true,
			Description: "Pull Zone id."},
		{Name: "hostname", Kind: connector.FieldString, Required: true,
			Description: "Hostname to update."},
		{Name: "force_ssl", Kind: connector.FieldBool, Required: true,
			Description: "true = redirect HTTP→HTTPS; false = serve both."},
	}}
}

func (a setForceSSLAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("pullzone_id")
	host := strings.TrimSpace(params.String("hostname"))
	if id == 0 || host == "" {
		return nil, errors.New("pullzone_id and hostname are required")
	}
	body := map[string]any{
		"Hostname": host,
		"ForceSSL": params.Bool("force_ssl"),
	}
	if err := s.http.SendJSON(ctx, "POST", fmt.Sprintf("/pullzone/%d/setForceSSL", id), body, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"pullzone_id": id,
		"hostname":    host,
		"force_ssl":   params.Bool("force_ssl"),
		"updated":     true,
	}, nil
}
