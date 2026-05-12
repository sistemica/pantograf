package lexoffice

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"os"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

// retryOn429 is a thin alias over httptr.RetryOn pinned to status 429.
// Lexware rate-limits at 2 req/s and returns 429 with a blocking
// timeframe of "seconds to minutes"; the shared helper handles the
// exponential backoff (600 ms → 30 s capped, 5 attempts).
func retryOn429(ctx context.Context, fn func() error) error {
	return httptr.RetryOn(ctx, fn, stdhttp.StatusTooManyRequests)
}

// voucherEndpoint maps a voucherlist row's voucherType to the resource
// endpoint that owns that voucher's full record.
//
// Sales-side documents created via the API or the Rechnungen module live
// under /v1/{type}/{id}. Manually-entered bookkeeping vouchers (typical
// for purchaseinvoice, sometimes for sales rows imported from elsewhere)
// live under /v1/vouchers/{id}.
//
// Returns the path WITHOUT a trailing /{id}. Empty string means "general
// voucher endpoint" — caller should use /v1/vouchers/{id}.
func voucherEndpoint(voucherType string) string {
	switch voucherType {
	case "salesinvoice", "invoice":
		return "/v1/invoices"
	case "salescreditnote":
		return "/v1/credit-notes"
	case "downpaymentinvoice":
		return "/v1/down-payment-invoices"
	case "quotation":
		return "/v1/quotations"
	case "orderconfirmation":
		return "/v1/order-confirmations"
	case "deliverynote":
		return "/v1/delivery-notes"
	case "dunning":
		return "/v1/dunnings"
	case "purchaseinvoice", "purchasecreditnote":
		return "/v1/vouchers"
	default:
		return "/v1/vouchers"
	}
}

// supportsRenderedDocument reports whether /v1/{type}/{id}/document is a
// thing for this voucherType. General vouchers (/v1/vouchers/{id}) carry
// attached files via a `files[]` field instead.
func supportsRenderedDocument(voucherType string) bool {
	switch voucherType {
	case "salesinvoice", "invoice",
		"salescreditnote",
		"downpaymentinvoice",
		"quotation",
		"orderconfirmation",
		"deliverynote",
		"dunning":
		return true
	}
	return false
}

// ── get_profile ───────────────────────────────────────────────────────────

type getProfileAction struct{}

func (getProfileAction) Name() string         { return "get-profile" }
func (getProfileAction) DisplayName() string  { return "Get profile" }
func (getProfileAction) Description() string  { return "Return the bound organisation: companyName, organizationId, taxType, businessFeatures, etc." }
func (getProfileAction) Schema() connector.Schema { return connector.Schema{} }

func (getProfileAction) Run(ctx context.Context, sess connector.Session, _ connector.Values) (any, error) {
	s := sess.(*session)
	var out map[string]any
	err := retryOn429(ctx, func() error { return s.http.GetJSON(ctx, "/v1/profile", nil, &out) })
	return out, err
}

// ── list_contacts ─────────────────────────────────────────────────────────

type listContactsAction struct{}

func (listContactsAction) Name() string         { return "list-contacts" }
func (listContactsAction) DisplayName() string  { return "List contacts" }
func (listContactsAction) Description() string  { return "Search contacts. All filters optional." }
func (listContactsAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "email", Kind: connector.FieldString, Description: "Filter by email."},
		{Name: "name", Kind: connector.FieldString, Description: "Filter by company / contact name."},
		{Name: "number", Kind: connector.FieldString, Description: "Filter by customer/vendor number."},
		{Name: "customer", Kind: connector.FieldBool, Description: "Only customers."},
		{Name: "vendor", Kind: connector.FieldBool, Description: "Only vendors."},
		{Name: "size", Kind: connector.FieldInt, Default: 25, Description: "Page size, max 250."},
		{Name: "page", Kind: connector.FieldInt, Default: 0},
	}}
}

func (a listContactsAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	q := url.Values{}
	for _, k := range []string{"email", "name", "number"} {
		if v := params.String(k); v != "" {
			q.Set(k, v)
		}
	}
	if params.Bool("customer") {
		q.Set("customer", "true")
	}
	if params.Bool("vendor") {
		q.Set("vendor", "true")
	}
	q.Set("size", fmt.Sprintf("%d", params.Int("size")))
	if p := params.Int("page"); p > 0 {
		q.Set("page", fmt.Sprintf("%d", p))
	}
	var out map[string]any
	err := retryOn429(ctx, func() error { return s.http.GetJSON(ctx, "/v1/contacts", q, &out) })
	return out, err
}

// ── get_contact ───────────────────────────────────────────────────────────

type getContactAction struct{}

func (getContactAction) Name() string         { return "get-contact" }
func (getContactAction) DisplayName() string  { return "Get contact" }
func (getContactAction) Description() string  { return "Fetch one contact by ID." }
func (getContactAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldString, Required: true},
	}}
}

func (a getContactAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	id := params.String("id")
	if id == "" {
		return nil, errors.New("id is required")
	}
	var out map[string]any
	err := retryOn429(ctx, func() error {
		return s.http.GetJSON(ctx, "/v1/contacts/"+url.PathEscape(id), nil, &out)
	})
	return out, err
}

// ── list_vouchers ─────────────────────────────────────────────────────────

type listVouchersAction struct{}

func (listVouchersAction) Name() string         { return "list-vouchers" }
func (listVouchersAction) DisplayName() string  { return "List vouchers" }
func (listVouchersAction) Description() string  { return "Unified search across invoices / credit notes / vouchers / quotations / etc. via /v1/voucherlist." }
func (listVouchersAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "voucher_type", Kind: connector.FieldEnum, Required: true,
			Options: []connector.EnumOption{
				{Value: "salesinvoice", Label: "Outgoing invoice"},
				{Value: "salescreditnote", Label: "Outgoing credit note"},
				{Value: "purchaseinvoice", Label: "Incoming invoice"},
				{Value: "purchasecreditnote", Label: "Incoming credit note"},
				{Value: "invoice", Label: "Invoice (legacy alias)"},
				{Value: "downpaymentinvoice", Label: "Down-payment invoice"},
				{Value: "quotation", Label: "Quotation"},
				{Value: "orderconfirmation", Label: "Order confirmation"},
				{Value: "deliverynote", Label: "Delivery note"},
				{Value: "dunning", Label: "Dunning"},
			}},
		{Name: "voucher_status", Kind: connector.FieldString, Default: "any",
			Description: "any | open | paid | paidoff | voided | transferred | sepadebit | overdue. Required by API; default 'any' disables filter."},
		{Name: "contact_id", Kind: connector.FieldString},
		{Name: "voucher_date_from", Kind: connector.FieldString, Description: "ISO date e.g. 2026-01-01"},
		{Name: "voucher_date_to", Kind: connector.FieldString},
		{Name: "size", Kind: connector.FieldInt, Default: 25, Description: "Page size, max 250."},
		{Name: "page", Kind: connector.FieldInt, Default: 0},
		{Name: "sort", Kind: connector.FieldString, Default: "voucherDate,DESC"},
	}}
}

func (a listVouchersAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	params = params.WithDefaults(a.Schema())
	vt := params.String("voucher_type")
	if vt == "" {
		return nil, errors.New("voucher_type is required")
	}
	q := url.Values{}
	q.Set("voucherType", vt)
	if v := params.String("voucher_status"); v != "" {
		q.Set("voucherStatus", v)
	}
	if v := params.String("contact_id"); v != "" {
		q.Set("contactId", v)
	}
	if v := params.String("voucher_date_from"); v != "" {
		q.Set("voucherDateFrom", v)
	}
	if v := params.String("voucher_date_to"); v != "" {
		q.Set("voucherDateTo", v)
	}
	q.Set("size", fmt.Sprintf("%d", params.Int("size")))
	if p := params.Int("page"); p > 0 {
		q.Set("page", fmt.Sprintf("%d", p))
	}
	if v := params.String("sort"); v != "" {
		q.Set("sort", v)
	}
	var out map[string]any
	err := retryOn429(ctx, func() error { return s.http.GetJSON(ctx, "/v1/voucherlist", q, &out) })
	return out, err
}

// ── get_voucher ───────────────────────────────────────────────────────────
//
// Dispatcher that resolves a voucherlist row to its full record. Tries the
// type-specific endpoint first; on 404 falls back to /v1/vouchers/{id}
// because some salesinvoice rows are actually manually-entered bookkeeping
// vouchers and live there instead.

type getVoucherAction struct{}

func (getVoucherAction) Name() string         { return "get-voucher" }
func (getVoucherAction) DisplayName() string  { return "Get voucher (any kind)" }
func (getVoucherAction) Description() string  { return "Fetch one voucherlist row by id + type. Falls back to /v1/vouchers/{id} on 404." }
func (getVoucherAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldString, Required: true},
		{Name: "voucher_type", Kind: connector.FieldString, Required: true,
			Description: "From the voucherlist row's voucherType field. e.g. salesinvoice, purchaseinvoice."},
	}}
}

func (a getVoucherAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	id := params.String("id")
	vt := params.String("voucher_type")
	if id == "" || vt == "" {
		return nil, errors.New("id and voucher_type are required")
	}

	// First attempt: the type-specific endpoint.
	primary := voucherEndpoint(vt) + "/" + url.PathEscape(id)
	var out map[string]any
	err := retryOn429(ctx, func() error { return s.http.GetJSON(ctx, primary, nil, &out) })
	if err == nil {
		out["_source_endpoint"] = primary
		return out, nil
	}

	// Fall back to /v1/vouchers/{id} on 404.
	var apiErr *httptr.APIError
	if errors.As(err, &apiErr) && apiErr.Status == stdhttp.StatusNotFound && primary != "/v1/vouchers/"+url.PathEscape(id) {
		fallback := "/v1/vouchers/" + url.PathEscape(id)
		out = nil
		ferr := retryOn429(ctx, func() error { return s.http.GetJSON(ctx, fallback, nil, &out) })
		if ferr == nil {
			out["_source_endpoint"] = fallback
			out["_fell_back_from"] = primary
			return out, nil
		}
	}
	return nil, err
}

// ── download_voucher_pdf ──────────────────────────────────────────────────
//
// Resolves the file ID, then streams /v1/files/{id} to disk. Two paths:
//   - Renderable types (invoice, credit-note, ...) →
//       GET /v1/{type}/{id}/document → { documentFileId } → /v1/files/{id}
//   - General vouchers (purchaseinvoice etc.) → the voucher carries
//       a `files[]` array with attached PDFs. We grab the first.

type downloadVoucherPDFAction struct{}

func (downloadVoucherPDFAction) Name() string         { return "download-voucher-pdf" }
func (downloadVoucherPDFAction) DisplayName() string  { return "Download voucher PDF" }
func (downloadVoucherPDFAction) Description() string  { return "Resolve voucher → file id → download bytes to a local path." }
func (downloadVoucherPDFAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "id", Kind: connector.FieldString, Required: true},
		{Name: "voucher_type", Kind: connector.FieldString, Required: true,
			Description: "From the voucherlist row's voucherType field."},
		{Name: "out", Kind: connector.FieldString, Required: true, Description: "Output file path."},
	}}
}

func (a downloadVoucherPDFAction) Run(ctx context.Context, sess connector.Session, params connector.Values) (any, error) {
	s := sess.(*session)
	id := params.String("id")
	vt := params.String("voucher_type")
	out := params.String("out")
	if id == "" || vt == "" || out == "" {
		return nil, errors.New("id, voucher_type, and out are required")
	}

	fileID, source, err := resolveFileID(ctx, s, id, vt)
	if err != nil {
		return nil, err
	}

	// Stream /v1/files/{fileID} to disk.
	var n int64
	err = retryOn429(ctx, func() error {
		resp, derr := s.http.Do(ctx, stdhttp.MethodGet, "/v1/files/"+url.PathEscape(fileID), nil, "")
		if derr != nil {
			return derr
		}
		defer resp.Body.Close()
		if resp.StatusCode == stdhttp.StatusTooManyRequests {
			body, _ := io.ReadAll(resp.Body)
			return &httptr.APIError{Status: resp.StatusCode, URL: resp.Request.URL.String(), Body: body}
		}
		if resp.StatusCode/100 != 2 {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("file download: http %d: %s", resp.StatusCode, string(body))
		}
		f, ferr := os.Create(out)
		if ferr != nil {
			return ferr
		}
		defer f.Close()
		nn, cerr := io.Copy(f, resp.Body)
		n = nn
		return cerr
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"saved":   true,
		"path":    out,
		"size":    n,
		"file_id": fileID,
		"source":  source,
	}, nil
}

// resolveFileID returns the file id to download for a given voucher,
// dispatching by voucherType. `source` is a tag describing how the id was
// found, useful for debugging.
func resolveFileID(ctx context.Context, s *session, id, voucherType string) (fileID, source string, err error) {
	if supportsRenderedDocument(voucherType) {
		path := voucherEndpoint(voucherType) + "/" + url.PathEscape(id) + "/document"
		var doc struct {
			DocumentFileID string `json:"documentFileId"`
		}
		err = retryOn429(ctx, func() error { return s.http.GetJSON(ctx, path, nil, &doc) })
		if err == nil && doc.DocumentFileID != "" {
			return doc.DocumentFileID, path, nil
		}
		// On 404 or empty, fall through to general-voucher path.
		var apiErr *httptr.APIError
		if !(err == nil || (errors.As(err, &apiErr) && apiErr.Status == stdhttp.StatusNotFound)) {
			return "", "", err
		}
	}

	// General voucher path: GET /v1/vouchers/{id}, take the first attached file.
	var v map[string]any
	verr := retryOn429(ctx, func() error {
		return s.http.GetJSON(ctx, "/v1/vouchers/"+url.PathEscape(id), nil, &v)
	})
	if verr != nil {
		return "", "", fmt.Errorf("resolve voucher: %w", verr)
	}
	files, _ := v["files"].([]any)
	if len(files) == 0 {
		return "", "", errors.New("voucher has no attached files")
	}
	// Lexware vouchers carry files as plain string UUIDs, not objects.
	// Defensively support both — the API used to return objects.
	switch first := files[0].(type) {
	case string:
		if first != "" {
			return first, "/v1/vouchers/" + url.PathEscape(id) + "#files[0]", nil
		}
	case map[string]any:
		if id2, ok := first["id"].(string); ok && id2 != "" {
			return id2, "/v1/vouchers/" + url.PathEscape(id) + "#files[0]", nil
		}
	}
	return "", "", errors.New("voucher's first file has no id")
}
