package bunny

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// ── list-zones ────────────────────────────────────────────────────────────

type listZonesAction struct{}

func (listZonesAction) Name() string         { return "list-zones" }
func (listZonesAction) DisplayName() string  { return "List DNS zones" }
func (listZonesAction) Description() string  { return "Paginated list of DNS zones on the account. Optional search filters by domain substring." }
func (listZonesAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "search", Kind: connector.FieldString,
			Description: "Filter zones by substring of the domain."},
		{Name: "page", Kind: connector.FieldInt, Default: 1,
			Description: "1-indexed page."},
		{Name: "per_page", Kind: connector.FieldInt, Default: 100,
			Description: "Items per page. Max 1000."},
	}}
}

func (a listZonesAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := url.Values{}
	q.Set("page", fmt.Sprintf("%d", params.Int("page")))
	q.Set("perPage", fmt.Sprintf("%d", params.Int("per_page")))
	if search := params.String("search"); search != "" {
		q.Set("search", search)
	}
	var resp zonePage
	if err := s.http.GetJSON(ctx, "/dnszone?"+q.Encode(), nil, &resp); err != nil {
		return nil, err
	}
	zones := make([]map[string]any, 0, len(resp.Items))
	for _, z := range resp.Items {
		zones = append(zones, map[string]any{
			"id":     z.ID,
			"domain": z.Domain,
		})
	}
	return map[string]any{
		"page":      resp.CurrentPage,
		"total":     resp.TotalItems,
		"has_more":  resp.HasMoreItems,
		"count":     len(zones),
		"zones":     zones,
	}, nil
}

// ── get-zone ──────────────────────────────────────────────────────────────

type getZoneAction struct{}

func (getZoneAction) Name() string         { return "get-zone" }
func (getZoneAction) DisplayName() string  { return "Get DNS zone details" }
func (getZoneAction) Description() string  { return "Full zone shape including all records. Records are returned with friendly type names (A, CNAME, …) alongside Bunny's numeric type codes." }
func (getZoneAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldInt, Required: true,
			Description: "Numeric zone id from list-zones."},
		{Name: "raw", Kind: connector.FieldBool, Default: false,
			Description: "Return the full Bunny response instead of the simplified view."},
	}}
}

func (a getZoneAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("id")
	if id == 0 {
		return nil, errors.New("id is required")
	}
	if params.Bool("raw") {
		var raw map[string]any
		if err := s.http.GetJSON(ctx, fmt.Sprintf("/dnszone/%d", id), nil, &raw); err != nil {
			return nil, err
		}
		return raw, nil
	}
	var z zone
	if err := s.http.GetJSON(ctx, fmt.Sprintf("/dnszone/%d", id), nil, &z); err != nil {
		return nil, err
	}
	return zoneView(z), nil
}

// ── create-zone ───────────────────────────────────────────────────────────

type createZoneAction struct{}

func (createZoneAction) Name() string         { return "create-zone" }
func (createZoneAction) DisplayName() string  { return "Create DNS zone" }
func (createZoneAction) Description() string  { return "Register a new DNS zone for a domain. Bunny returns the new zone id; update your registrar to use Bunny nameservers afterwards." }
func (createZoneAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "domain", Kind: connector.FieldString, Required: true,
			Description: "Domain to host. e.g. example.com (no protocol)."},
	}}
}

func (a createZoneAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	domain := strings.TrimSpace(params.String("domain"))
	if domain == "" {
		return nil, errors.New("domain is required")
	}
	body := map[string]any{"Domain": domain}
	var z zone
	if err := s.http.SendJSON(ctx, "POST", "/dnszone", body, &z); err != nil {
		return nil, err
	}
	return zoneView(z), nil
}

// ── delete-zone ───────────────────────────────────────────────────────────

type deleteZoneAction struct{}

func (deleteZoneAction) Name() string         { return "delete-zone" }
func (deleteZoneAction) DisplayName() string  { return "Delete DNS zone" }
func (deleteZoneAction) Description() string  { return "Permanently removes the zone and all its records. Irreversible." }
func (deleteZoneAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldInt, Required: true,
			Description: "Numeric zone id."},
	}}
}

func (a deleteZoneAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("id")
	if id == 0 {
		return nil, errors.New("id is required")
	}
	if err := s.http.SendJSON(ctx, "DELETE", fmt.Sprintf("/dnszone/%d", id), nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "deleted": true}, nil
}

// ── check-zone-availability ───────────────────────────────────────────────

type checkAvailabilityAction struct{}

func (checkAvailabilityAction) Name() string         { return "check-zone-availability" }
func (checkAvailabilityAction) DisplayName() string  { return "Check if a domain is available to register as a Bunny zone" }
func (checkAvailabilityAction) Description() string  { return "Verify a domain can be added to this account (no conflict with an existing zone)." }
func (checkAvailabilityAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Domain to check."},
	}}
}

func (a checkAvailabilityAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	name := strings.TrimSpace(params.String("name"))
	if name == "" {
		return nil, errors.New("name is required")
	}
	body := map[string]any{"Name": name}
	var resp map[string]any
	if err := s.http.SendJSON(ctx, "POST", "/dnszone/checkavailability", body, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ── export-zone ───────────────────────────────────────────────────────────

type exportZoneAction struct{}

func (exportZoneAction) Name() string         { return "export-zone" }
func (exportZoneAction) DisplayName() string  { return "Export zone as BIND file" }
func (exportZoneAction) Description() string  { return "Dump the zone in standard BIND zone-file format. Writes to `out` (path on disk)." }
func (exportZoneAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldInt, Required: true,
			Description: "Numeric zone id."},
		{Name: "out", Kind: connector.FieldString, Required: true, IsPath: true,
			Description: "Destination file path. Subject to PGF_ALLOWED_PATHS."},
	}}
}

func (a exportZoneAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	id := params.Int("id")
	out := params.String("out")
	if id == 0 || out == "" {
		return nil, errors.New("id and out are required")
	}
	resp, err := s.http.Do(ctx, "GET", fmt.Sprintf("/dnszone/%d/export", id), nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("export: http %d", resp.StatusCode)
	}
	f, err := os.Create(out)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", out, err)
	}
	defer f.Close()
	n, err := f.ReadFrom(resp.Body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"zone_id": id,
		"path":    out,
		"bytes":   n,
	}, nil
}

// ── add-record ────────────────────────────────────────────────────────────
//
// ShowWhen-gated fields keep the schema clean: priority shows only for
// MX and SRV; weight + port show only for SRV; flags + tag show only
// for CAA. The agent sees only the fields relevant to the chosen type.

type addRecordAction struct{}

func (addRecordAction) Name() string         { return "add-record" }
func (addRecordAction) DisplayName() string  { return "Add DNS record" }
func (addRecordAction) Description() string  { return "Create a new DNS record in a zone. Pass type as string (A, AAAA, CNAME, MX, SRV, CAA, TXT, NS, ...); the connector translates to Bunny's numeric type codes." }
func (addRecordAction) Schema() connector.Schema {
	opts := make([]connector.EnumOption, 0, len(recordTypeNames))
	for i, name := range recordTypeNames {
		opts = append(opts, connector.EnumOption{Value: name, Label: fmt.Sprintf("%s (code %d)", name, i)})
	}
	isSRV := func(v connector.Values) bool { return strings.EqualFold(v.String("type"), "SRV") }
	isMXOrSRV := func(v connector.Values) bool {
		t := strings.ToUpper(v.String("type"))
		return t == "MX" || t == "SRV"
	}
	isCAA := func(v connector.Values) bool { return strings.EqualFold(v.String("type"), "CAA") }

	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "zone_id", Kind: connector.FieldInt, Required: true,
			Description: "Numeric zone id (from list-zones)."},
		{Name: "type", Kind: connector.FieldEnum, Required: true, Default: "A",
			Options: opts,
			Description: "Record type. Common: A, AAAA, CNAME, TXT, MX, NS, SRV, CAA."},
		{Name: "name", Kind: connector.FieldString, Required: true,
			Description: "Record name. `@` or empty = apex. `www` = www.example.com when zone is example.com."},
		{Name: "value", Kind: connector.FieldString, Required: true,
			Description: "Record target. e.g. IP for A/AAAA; hostname for CNAME/NS; text for TXT; '0 issue \"letsencrypt.org\"' for CAA."},
		{Name: "ttl", Kind: connector.FieldInt, Default: 3600,
			Description: "Time-to-live in seconds. Bunny minimum varies by record type."},
		{Name: "priority", Kind: connector.FieldInt,
			ShowWhen: isMXOrSRV,
			Description: "MX/SRV only. Lower = preferred."},
		{Name: "weight", Kind: connector.FieldInt,
			ShowWhen: isSRV,
			Description: "SRV only. Selection weight among same-priority targets."},
		{Name: "port", Kind: connector.FieldInt,
			ShowWhen: isSRV,
			Description: "SRV only. Service port number."},
		{Name: "flags", Kind: connector.FieldInt,
			ShowWhen: isCAA,
			Description: "CAA only. 0 (non-critical) or 128 (critical)."},
		{Name: "tag", Kind: connector.FieldString,
			ShowWhen: isCAA,
			Description: "CAA only. e.g. `issue`, `issuewild`, `iodef`."},
		{Name: "comment", Kind: connector.FieldString,
			Description: "Free-form note shown in the Bunny dashboard."},
		{Name: "disabled", Kind: connector.FieldBool, Default: false,
			Description: "Create the record in a disabled state (won't resolve)."},
	}}
}

func (a addRecordAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	zoneID := params.Int("zone_id")
	if zoneID == 0 {
		return nil, errors.New("zone_id is required")
	}
	typeCode, err := recordTypeFromName(params.String("type"))
	if err != nil {
		return nil, err
	}
	name := params.String("name")
	value := params.String("value")
	if value == "" {
		return nil, errors.New("value is required")
	}
	body := map[string]any{
		"Type":  typeCode,
		"Name":  name,
		"Value": value,
		"Ttl":   params.Int("ttl"),
	}
	if v := params.Int("priority"); v != 0 {
		body["Priority"] = v
	}
	if v := params.Int("weight"); v != 0 {
		body["Weight"] = v
	}
	if v := params.Int("port"); v != 0 {
		body["Port"] = v
	}
	if v := params.Int("flags"); v != 0 {
		body["Flags"] = v
	}
	if v := params.String("tag"); v != "" {
		body["Tag"] = v
	}
	if v := params.String("comment"); v != "" {
		body["Comment"] = v
	}
	if params.Bool("disabled") {
		body["Disabled"] = true
	}
	var r record
	if err := s.http.SendJSON(ctx, "PUT", fmt.Sprintf("/dnszone/%d/records", zoneID), body, &r); err != nil {
		return nil, err
	}
	return recordView(r), nil
}

// ── update-record ─────────────────────────────────────────────────────────

type updateRecordAction struct{}

func (updateRecordAction) Name() string         { return "update-record" }
func (updateRecordAction) DisplayName() string  { return "Update DNS record" }
func (updateRecordAction) Description() string  { return "Partial update — only the fields you pass are changed. Use get-zone first if you need the current record state." }
func (updateRecordAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "zone_id", Kind: connector.FieldInt, Required: true,
			Description: "Numeric zone id."},
		{Name: "record_id", Kind: connector.FieldInt, Required: true,
			Description: "Numeric record id (visible in get-zone's records list)."},
		{Name: "value", Kind: connector.FieldString,
			Description: "New value. Omit to keep current."},
		{Name: "ttl", Kind: connector.FieldInt,
			Description: "New TTL. Omit to keep current."},
		{Name: "priority", Kind: connector.FieldInt,
			Description: "MX/SRV priority. Omit to keep current."},
		{Name: "weight", Kind: connector.FieldInt,
			Description: "SRV weight."},
		{Name: "port", Kind: connector.FieldInt,
			Description: "SRV port."},
		{Name: "flags", Kind: connector.FieldInt,
			Description: "CAA flags."},
		{Name: "tag", Kind: connector.FieldString,
			Description: "CAA tag."},
		{Name: "comment", Kind: connector.FieldString,
			Description: "Update comment."},
		{Name: "disabled", Kind: connector.FieldBool,
			Description: "Enable / disable the record without deleting it."},
	}}
}

func (a updateRecordAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	zoneID := params.Int("zone_id")
	recordID := params.Int("record_id")
	if zoneID == 0 || recordID == 0 {
		return nil, errors.New("zone_id and record_id are required")
	}
	body := map[string]any{}
	if params.Has("value") {
		body["Value"] = params.String("value")
	}
	if params.Has("ttl") {
		body["Ttl"] = params.Int("ttl")
	}
	if params.Has("priority") {
		body["Priority"] = params.Int("priority")
	}
	if params.Has("weight") {
		body["Weight"] = params.Int("weight")
	}
	if params.Has("port") {
		body["Port"] = params.Int("port")
	}
	if params.Has("flags") {
		body["Flags"] = params.Int("flags")
	}
	if params.Has("tag") {
		body["Tag"] = params.String("tag")
	}
	if params.Has("comment") {
		body["Comment"] = params.String("comment")
	}
	if params.Has("disabled") {
		body["Disabled"] = params.Bool("disabled")
	}
	if len(body) == 0 {
		return nil, errors.New("at least one field to update is required")
	}
	if err := s.http.SendJSON(ctx, "POST", fmt.Sprintf("/dnszone/%d/records/%d", zoneID, recordID), body, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"zone_id":   zoneID,
		"record_id": recordID,
		"updated":   true,
		"fields":    body,
	}, nil
}

// ── delete-record ─────────────────────────────────────────────────────────

type deleteRecordAction struct{}

func (deleteRecordAction) Name() string         { return "delete-record" }
func (deleteRecordAction) DisplayName() string  { return "Delete DNS record" }
func (deleteRecordAction) Description() string  { return "Remove a single record from a zone." }
func (deleteRecordAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "zone_id", Kind: connector.FieldInt, Required: true,
			Description: "Numeric zone id."},
		{Name: "record_id", Kind: connector.FieldInt, Required: true,
			Description: "Numeric record id."},
	}}
}

func (a deleteRecordAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	zoneID := params.Int("zone_id")
	recordID := params.Int("record_id")
	if zoneID == 0 || recordID == 0 {
		return nil, errors.New("zone_id and record_id are required")
	}
	if err := s.http.SendJSON(ctx, "DELETE", fmt.Sprintf("/dnszone/%d/records/%d", zoneID, recordID), nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{
		"zone_id":   zoneID,
		"record_id": recordID,
		"deleted":   true,
	}, nil
}
