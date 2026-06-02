package lexoffice

import (
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"
)

func TestComputeAmounts(t *testing.T) {
	tests := []struct {
		name                  string
		net, gross, tax, rate float64
		rc                    bool
		wantN, wantG, wantT   float64
		wantType              string
	}{
		{name: "net only @19", net: 1230, rate: 19, wantN: 1230, wantG: 1463.70, wantT: 233.70, wantType: "net"},
		{name: "gross only @19", gross: 119, rate: 19, wantN: 100, wantG: 119, wantT: 19, wantType: "net"},
		{name: "rate defaults to 19", net: 100, wantN: 100, wantG: 119, wantT: 19, wantType: "net"},
		{name: "explicit tax overrides", net: 100, tax: 5, rate: 19, wantN: 100, wantG: 105, wantT: 5, wantType: "net"},
		{name: "reverse charge from net", net: 500, rate: 19, rc: true, wantN: 500, wantG: 500, wantT: 0, wantType: "gross"},
		{name: "reverse charge from gross", gross: 500, rc: true, wantN: 500, wantG: 500, wantT: 0, wantType: "gross"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, g, ta, _, typ := computeAmounts(tc.net, tc.gross, tc.tax, tc.rate, tc.rc)
			if n != tc.wantN || g != tc.wantG || ta != tc.wantT || typ != tc.wantType {
				t.Errorf("got net=%.2f gross=%.2f tax=%.2f type=%s; want net=%.2f gross=%.2f tax=%.2f type=%s",
					n, g, ta, typ, tc.wantN, tc.wantG, tc.wantT, tc.wantType)
			}
		})
	}
}

func TestRound2(t *testing.T) {
	// round2 mirrors the lx CLI's cents rounder verbatim so vouchers book
	// identically; these are the representative (non-pathological) cases.
	for in, want := range map[float64]float64{233.7: 233.70, 100.0: 100.0, 119.0: 119.0, 1463.7: 1463.70} {
		if got := round2(in); got != want {
			t.Errorf("round2(%v) = %v, want %v", in, got, want)
		}
	}
}

// The lx CLI proves Lexware accepts a "file" part (octet-stream) plus a
// "type=voucher" form field. attachVoucherFile must reproduce that body
// shape — this checks the multipart it builds without hitting the network.
func TestAttachBodyShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invoice.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, ct, err := buildVoucherFileBody(path)
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(body, params["boundary"])
	var sawFile, sawType bool
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch part.FormName() {
		case "file":
			sawFile = true
			if part.FileName() != "invoice.pdf" {
				t.Errorf("filename = %q, want invoice.pdf", part.FileName())
			}
		case "type":
			sawType = true
			b, _ := io.ReadAll(part)
			if string(b) != "voucher" {
				t.Errorf("type = %q, want voucher", string(b))
			}
		}
	}
	if !sawFile || !sawType {
		t.Errorf("missing parts: file=%v type=%v", sawFile, sawType)
	}
}
