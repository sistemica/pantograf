package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sistemica/pantograf/connector"
)

// IncomingRequest is the structured payload emitted for each request.
// One of Body / BodyText / BodyBase64 carries the body, depending on
// what the parser could decode:
//
//   - Body: JSON object/array/etc when Content-Type is application/json
//   - Body: map[string][]string when Content-Type is form-urlencoded
//   - BodyText: UTF-8 text when the bytes are valid UTF-8 (any other CT)
//   - BodyBase64: base64 of raw bytes (binary or invalid-UTF-8 bodies)
type IncomingRequest struct {
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	URL        string              `json:"url"`
	Query      map[string][]string `json:"query,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	Body       any                 `json:"body,omitempty"`
	BodyText   string              `json:"body_text,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
	RemoteAddr string              `json:"remote_addr,omitempty"`
	ReceivedAt time.Time           `json:"received_at"`
}

type incomingTrigger struct{}

func (incomingTrigger) Name() string         { return "incoming" }
func (incomingTrigger) DisplayName() string  { return "Incoming HTTP request" }
func (incomingTrigger) Description() string  { return "Receive any HTTP request. Emits a parsed Event; replies per credential config." }
func (incomingTrigger) Strategy() connector.TriggerStrategy {
	return connector.TriggerWebhook
}
func (incomingTrigger) Schema() connector.Schema { return connector.Schema{} }

// OnEnable / OnDisable are no-ops — there's no upstream to register with.
// The user points whatever upstream they have at the URL `pgf serve`
// announces on startup.
func (incomingTrigger) OnEnable(_ context.Context, _ connector.Session, _ connector.Values, _ string) error {
	return nil
}
func (incomingTrigger) OnDisable(_ context.Context, _ connector.Session, _ connector.Values) error {
	return nil
}

func (incomingTrigger) Handle(ctx context.Context, sess connector.Session, _ connector.Values, req *http.Request, emit connector.Sink) (*connector.WebhookResponse, error) {
	s, ok := sess.(*session)
	if !ok {
		return errResp(http.StatusInternalServerError, "wrong session type"), nil
	}
	v := s.cred.Values

	// Method allow-list.
	if allowed := v.StringList(fAllowedMethods); len(allowed) > 0 {
		ok := false
		for _, m := range allowed {
			if strings.EqualFold(strings.TrimSpace(m), req.Method) {
				ok = true
				break
			}
		}
		if !ok {
			return errResp(http.StatusMethodNotAllowed, "method not allowed: "+req.Method), nil
		}
	}

	// API-key style auth.
	if want := v.String(fSecret); want != "" {
		hdrName := v.String(fSecretHeader)
		qpName := v.String(fSecretQuery)
		if !secretMatches(req, hdrName, qpName, want) {
			return errResp(http.StatusUnauthorized, "secret mismatch"), nil
		}
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return errResp(http.StatusBadRequest, "read body: "+err.Error()), nil
	}
	defer req.Body.Close()

	// HMAC signature auth — needs the raw body, so it's checked here.
	if algo := v.String(fSigAlgo); algo != "" && algo != "none" {
		if err := verifySignature(req, body, algo, v.String(fSigSecret), v.String(fSigHeader), v.String(fSigPrefix)); err != nil {
			return errResp(http.StatusUnauthorized, err.Error()), nil
		}
	}

	payload := IncomingRequest{
		Method:     req.Method,
		Path:       req.URL.Path,
		URL:        req.URL.String(),
		Query:      req.URL.Query(),
		Headers:    filterHeaders(req.Header, v.StringList(fStripHeaders)),
		RemoteAddr: req.RemoteAddr,
		ReceivedAt: time.Now().UTC(),
	}
	parseBody(req.Header.Get("Content-Type"), body, &payload)

	ev := connector.Event{
		ID:        randomID(),
		Type:      "request",
		Payload:   payload,
		Timestamp: payload.ReceivedAt,
	}
	if err := emit(ctx, ev); err != nil {
		return errResp(http.StatusInternalServerError, "emit: "+err.Error()), nil
	}

	return buildResponse(v), nil
}

// ── helpers ───────────────────────────────────────────────────────────────

// secretMatches checks the incoming request against the configured secret
// in either the named header or named query param. Constant-time compare
// would be nicer; left as future hardening since this is not a high-value
// secret target.
// verifySignature computes the HMAC of body using secret and compares it
// (constant-time) against the value in headerName, optionally stripping
// `prefix` from the header value before compare. Returns nil on success.
func verifySignature(req *http.Request, body []byte, algo, secret, headerName, prefix string) error {
	if secret == "" {
		return errors.New("signature_secret missing")
	}
	if headerName == "" {
		return errors.New("signature_header missing")
	}
	got := req.Header.Get(headerName)
	if got == "" {
		return fmt.Errorf("missing header %s", headerName)
	}
	if prefix != "" {
		if !strings.HasPrefix(got, prefix) {
			return fmt.Errorf("signature header missing required prefix %q", prefix)
		}
		got = strings.TrimPrefix(got, prefix)
	}
	gotBytes, err := hex.DecodeString(strings.TrimSpace(got))
	if err != nil {
		return fmt.Errorf("signature header %s is not hex: %w", headerName, err)
	}

	var h hash.Hash
	switch algo {
	case "hmac-sha256", "hmac-sha256-prefix":
		h = hmac.New(sha256.New, []byte(secret))
	case "hmac-sha1":
		h = hmac.New(sha1.New, []byte(secret))
	default:
		return fmt.Errorf("unsupported signature_algo %q", algo)
	}
	h.Write(body)
	expected := h.Sum(nil)
	if !hmac.Equal(gotBytes, expected) {
		return errors.New("signature mismatch")
	}
	return nil
}

func secretMatches(req *http.Request, headerName, queryName, want string) bool {
	if headerName != "" {
		if got := req.Header.Get(headerName); got == want {
			return true
		}
	}
	if queryName != "" {
		if got := req.URL.Query().Get(queryName); got == want {
			return true
		}
	}
	return false
}

func filterHeaders(in http.Header, strip []string) map[string][]string {
	if len(in) == 0 {
		return nil
	}
	stripSet := map[string]struct{}{}
	for _, h := range strip {
		stripSet[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}
	out := make(map[string][]string, len(in))
	for k, vv := range in {
		if _, drop := stripSet[strings.ToLower(k)]; drop {
			continue
		}
		out[k] = vv
	}
	return out
}

// parseBody fills in payload.{Body,BodyText,BodyBase64} based on
// Content-Type. JSON and form-urlencoded get structured; UTF-8-clean
// bodies become text; anything else falls back to base64.
func parseBody(contentType string, body []byte, payload *IncomingRequest) {
	if len(body) == 0 {
		return
	}
	mediaType, _, _ := mime.ParseMediaType(contentType)

	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		var parsed any
		if err := json.Unmarshal(body, &parsed); err == nil {
			payload.Body = parsed
			return
		}
		// Fall through on parse error — body might be malformed but the
		// caller should still see something.
	case mediaType == "application/x-www-form-urlencoded":
		if vals, err := url.ParseQuery(string(body)); err == nil {
			payload.Body = map[string][]string(vals)
			return
		}
	}
	if utf8.Valid(body) {
		payload.BodyText = string(body)
		return
	}
	payload.BodyBase64 = base64.StdEncoding.EncodeToString(body)
}

// buildResponse turns the credential's response_* fields into a
// WebhookResponse. response_file wins over response_body and is read at
// request time so the file can change between requests.
func buildResponse(v connector.Values) *connector.WebhookResponse {
	resp := &connector.WebhookResponse{
		Status:      v.Int(fResponseStatus),
		ContentType: v.String(fResponseType),
	}
	if path := v.String(fResponseFile); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return errResp(http.StatusInternalServerError, fmt.Sprintf("read %s: %v", path, err))
		}
		resp.Body = data
		return resp
	}
	if body := v.String(fResponseBody); body != "" {
		resp.Body = []byte(body)
		return resp
	}
	return resp
}

func errResp(status int, msg string) *connector.WebhookResponse {
	return &connector.WebhookResponse{
		Status:      status,
		ContentType: "text/plain; charset=utf-8",
		Body:        []byte(msg),
	}
}

func randomID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// silence unused-import
var _ = errors.New
