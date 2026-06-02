package lexoffice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	stdhttp "net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

// round2 rounds to 2 decimal places (cents).
//
// QUIRK: this is the lx CLI's exact rounder, kept verbatim so vouchers
// created here book to identical cent values as ones created with the tool.
// It is a naive (v*100+0.5) truncation, NOT math.Round — so a value sitting
// on a float-representation boundary can round "wrong" (e.g. round2(1.005)
// yields 1.00, because 1.005*100 is 100.4999… in float64). Do not swap in
// math.Round: matching lx bit-for-bit matters more here than textbook
// rounding, and real net/gross/tax figures don't land on these boundaries.
func round2(v float64) float64 { return float64(int((v*100)+0.5)) / 100 }

// computeAmounts derives the (net, gross, tax) triple + taxType from whatever
// the caller supplied, exactly as the lx CLI does:
//   - reverse charge (§13b): gross == net, tax 0, taxType "gross"
//   - otherwise: fill net from gross, tax from net*rate, gross from net+tax
//
// rate of 0 defaults to 19%. All money values are rounded to cents.
func computeAmounts(net, gross, tax, rate float64, reverseCharge bool) (n, g, t, r float64, taxType string) {
	if rate == 0 {
		rate = 19
	}
	if reverseCharge {
		amt := net
		if amt == 0 {
			amt = gross
		}
		return round2(amt), round2(amt), 0, rate, "gross"
	}
	if net == 0 && gross > 0 {
		net = gross / (1 + rate/100)
	}
	if tax == 0 {
		tax = net * rate / 100
	}
	if gross == 0 {
		gross = net + tax
	}
	return round2(net), round2(gross), round2(tax), rate, "net"
}

// amount parses a decimal-string param into a float64; empty → 0.
func amount(p connector.Values, key string) (float64, error) {
	s := strings.TrimSpace(p.String(key))
	if s == "" {
		return 0, nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a number", key, s)
	}
	return f, nil
}

// ── list-categories ─────────────────────────────────────────────────────────

type listCategoriesAction struct{}

func (listCategoriesAction) Name() string        { return "list-categories" }
func (listCategoriesAction) DisplayName() string { return "List posting categories" }
func (listCategoriesAction) Description() string {
	return "List posting categories (the categoryId values voucher items reference). Optional name substring + type filter (income / outgo / receivable / payable)."
}
func (listCategoriesAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "search", Label: "Name contains", Kind: connector.FieldString},
		{Name: "type", Label: "Type filter", Kind: connector.FieldEnum, Options: []connector.EnumOption{
			{Value: "income", Label: "income"},
			{Value: "outgo", Label: "outgo"},
			{Value: "receivable", Label: "receivable"},
			{Value: "payable", Label: "payable"},
		}},
	}}
}
func (a listCategoriesAction) Run(ctx context.Context, sess connector.Session, p connector.Values) (any, error) {
	s := sess.(*session)
	p = p.WithDefaults(a.Schema())
	var cats []map[string]any
	if err := retryOn429(ctx, func() error {
		return s.http.GetJSON(ctx, "/v1/posting-categories", nil, &cats)
	}); err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(p.String("search")))
	kind := strings.ToLower(strings.TrimSpace(p.String("type")))
	if search == "" && kind == "" {
		return cats, nil
	}
	out := make([]map[string]any, 0, len(cats))
	for _, c := range cats {
		if search != "" && !strings.Contains(strings.ToLower(fmt.Sprint(c["name"])), search) {
			continue
		}
		if kind != "" && !strings.EqualFold(fmt.Sprint(c["type"]), kind) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// ── create-purchase-voucher ───────────────────────────────────────────────────

type createPurchaseVoucherAction struct{}

func (createPurchaseVoucherAction) Name() string        { return "create-purchase-voucher" }
func (createPurchaseVoucherAction) DisplayName() string { return "Create purchase voucher" }
func (createPurchaseVoucherAction) Description() string {
	return "Create a purchaseinvoice voucher (Eingangsrechnung) and optionally attach a PDF. Net/tax math: give net (tax=net*rate, gross=net+tax) OR gross (net derived); tax overrides. reverse_charge=true books §13b (gross=net, tax 0, taxType gross) — pass the §13b category."
}
func (createPurchaseVoucherAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "contact_id", Label: "Vendor contact id", Kind: connector.FieldString, Required: true},
		{Name: "category_id", Label: "Posting category id", Kind: connector.FieldString, Required: true, Description: "From list-categories."},
		{Name: "number", Label: "Vendor invoice number", Kind: connector.FieldString, Required: true},
		{Name: "date", Label: "Invoice date (YYYY-MM-DD)", Kind: connector.FieldString, Required: true},
		{Name: "due", Label: "Due date (YYYY-MM-DD)", Kind: connector.FieldString},
		{Name: "net", Label: "Net amount", Kind: connector.FieldString, Description: "Decimal, e.g. 1230.00"},
		{Name: "gross", Label: "Gross amount", Kind: connector.FieldString, Description: "Decimal; derived from net if omitted."},
		{Name: "tax", Label: "Tax amount", Kind: connector.FieldString, Description: "Decimal; derived from net*rate if omitted."},
		{Name: "rate", Label: "Tax rate (percent)", Kind: connector.FieldString, Default: "19"},
		{Name: "remark", Label: "Remark", Kind: connector.FieldString},
		{Name: "reverse_charge", Label: "§13b reverse charge", Kind: connector.FieldBool, Default: false},
		{Name: "pdf", Label: "PDF to attach after creation", Kind: connector.FieldString, IsPath: true},
	}}
}
func (a createPurchaseVoucherAction) Run(ctx context.Context, sess connector.Session, p connector.Values) (any, error) {
	s := sess.(*session)
	p = p.WithDefaults(a.Schema())

	contactID := strings.TrimSpace(p.String("contact_id"))
	category := strings.TrimSpace(p.String("category_id"))
	number := strings.TrimSpace(p.String("number"))
	date := strings.TrimSpace(p.String("date"))
	if contactID == "" || category == "" || number == "" || date == "" {
		return nil, errors.New("contact_id, category_id, number, and date are required")
	}
	net, err := amount(p, "net")
	if err != nil {
		return nil, err
	}
	gross, err := amount(p, "gross")
	if err != nil {
		return nil, err
	}
	tax, err := amount(p, "tax")
	if err != nil {
		return nil, err
	}
	rate, err := amount(p, "rate")
	if err != nil {
		return nil, err
	}
	net, gross, tax, rate, taxType := computeAmounts(net, gross, tax, rate, p.Bool("reverse_charge"))

	body := map[string]any{
		"type":                 "purchaseinvoice",
		"voucherNumber":        number,
		"voucherDate":          date,
		"totalGrossAmount":     gross,
		"totalTaxAmount":       tax,
		"taxType":              taxType,
		"useCollectiveContact": false,
		"contactId":            contactID,
		"remark":               p.String("remark"),
		"voucherItems": []map[string]any{{
			"amount":         net,
			"taxAmount":      tax,
			"taxRatePercent": rate,
			"categoryId":     category,
		}},
	}
	if due := strings.TrimSpace(p.String("due")); due != "" {
		body["dueDate"] = due
	}

	var resp map[string]any
	if err := retryOn429(ctx, func() error {
		return s.http.SendJSON(ctx, stdhttp.MethodPost, "/v1/vouchers", body, &resp)
	}); err != nil {
		return nil, err
	}
	vid, _ := resp["id"].(string)

	result := map[string]any{
		"id":            vid,
		"voucherNumber": number,
		"net":           net,
		"tax":           tax,
		"gross":         gross,
		"taxType":       taxType,
	}
	if pdf := strings.TrimSpace(p.String("pdf")); pdf != "" {
		if vid == "" {
			return result, errors.New("voucher created but response had no id — cannot attach PDF")
		}
		fr, err := attachVoucherFile(ctx, s, vid, pdf)
		if err != nil {
			result["attach_error"] = err.Error()
			return result, nil
		}
		result["attached_file_id"] = fr["id"]
	}
	return result, nil
}

// ── attach-voucher-file ───────────────────────────────────────────────────────

type attachVoucherFileAction struct{}

func (attachVoucherFileAction) Name() string        { return "attach-voucher-file" }
func (attachVoucherFileAction) DisplayName() string { return "Attach file to voucher" }
func (attachVoucherFileAction) Description() string {
	return "Attach a PDF (or other file) to an existing voucher via POST /v1/vouchers/{id}/files. Returns the new file id."
}
func (attachVoucherFileAction) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: "voucher_id", Label: "Voucher id", Kind: connector.FieldString, Required: true},
		{Name: "file", Label: "File to attach", Kind: connector.FieldString, Required: true, IsPath: true},
	}}
}
func (a attachVoucherFileAction) Run(ctx context.Context, sess connector.Session, p connector.Values) (any, error) {
	s := sess.(*session)
	p = p.WithDefaults(a.Schema())
	vid := strings.TrimSpace(p.String("voucher_id"))
	file := strings.TrimSpace(p.String("file"))
	if vid == "" || file == "" {
		return nil, errors.New("voucher_id and file are required")
	}
	return attachVoucherFile(ctx, s, vid, file)
}

// attachVoucherFile uploads a file to a voucher's /files endpoint. The body
// is built in memory (Content-Length set) with the file under field "file"
// and a "type=voucher" form field — byte-for-byte the shape the lx CLI uses,
// which the live Lexware API accepts.
func attachVoucherFile(ctx context.Context, s *session, voucherID, filePath string) (map[string]any, error) {
	body, contentType, err := buildVoucherFileBody(filePath)
	if err != nil {
		return nil, err
	}
	path := "/v1/vouchers/" + url.PathEscape(voucherID) + "/files"
	resp, err := s.http.Do(ctx, stdhttp.MethodPost, path, body, contentType)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("attach: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &out); err != nil {
		return nil, fmt.Errorf("attach: decode response: %w (body: %s)", err, strings.TrimSpace(string(raw)))
	}
	return out, nil
}

// buildVoucherFileBody assembles the /files multipart body: the file under
// field "file" plus a "type=voucher" form field, buffered so Content-Length
// is set. This is byte-for-byte the shape the lx CLI uses, which the live
// Lexware API accepts.
//
// QUIRK: CreateFormFile emits the file part as application/octet-stream, and
// Lexware is fine with that — unlike Paperless-ngx, whose parser rejects an
// octet-stream part as "no file submitted" and needs a sniffed Content-Type
// (see connectors/paperless). Same operation, opposite server expectation;
// don't try to unify the two upload paths.
func buildVoucherFileBody(filePath string) (io.Reader, string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", fmt.Errorf("open %s: %w", filePath, err)
	}
	defer f.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("type", "voucher"); err != nil {
		return nil, "", err
	}
	fw, err := mw.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return nil, "", err
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return &buf, mw.FormDataContentType(), nil
}
