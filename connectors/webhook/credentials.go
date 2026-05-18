package webhook

import (
	"context"
	"strconv"

	"github.com/sistemica/pantograf/connector"
)

const (
	// API-key style: static token compared against a header or query param.
	fSecret       = "secret_token"
	fSecretHeader = "secret_header"
	fSecretQuery  = "secret_query_param"

	// HMAC signature: per-request hash of the raw body using a shared secret.
	fSigAlgo   = "signature_algo"
	fSigSecret = "signature_secret"
	fSigHeader = "signature_header"
	fSigPrefix = "signature_prefix"

	// Response config.
	fResponseBody   = "response_body"
	fResponseFile   = "response_file"
	fResponseType   = "response_content_type"
	fResponseStatus = "response_status"

	// Misc.
	fAllowedMethods = "allowed_methods"
	fStripHeaders   = "strip_headers"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthCustom }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		// API-key style.
		{
			Name: fSecret, Label: "API key / shared secret (optional)", Kind: connector.FieldSecret,
			Description: "Static value compared against secret_header or ?secret_query_param=...",
		},
		{
			Name: fSecretHeader, Label: "Header name carrying the secret", Kind: connector.FieldString,
			Default:     "X-Mw-Secret",
			Description: "e.g. X-Mw-Secret, X-Telegram-Bot-Api-Secret-Token, Authorization (full value).",
		},
		{
			Name: fSecretQuery, Label: "Query param carrying the secret", Kind: connector.FieldString,
			Default: "secret",
		},

		// HMAC signature style.
		{
			Name: fSigAlgo, Label: "Signature algorithm", Kind: connector.FieldEnum,
			Options: []connector.EnumOption{
				{Value: "none", Label: "(none)"},
				{Value: "hmac-sha256", Label: "HMAC-SHA256 — plain hex (LemonSqueezy)"},
				{Value: "hmac-sha256-prefix", Label: "HMAC-SHA256 — prefixed (GitHub: 'sha256=...')"},
				{Value: "hmac-sha1", Label: "HMAC-SHA1 — legacy"},
			},
			Default: "none",
		},
		{
			Name: fSigSecret, Label: "Signature secret", Kind: connector.FieldSecret,
			Description: "Shared HMAC key. Required when signature_algo != none.",
		},
		{
			Name: fSigHeader, Label: "Signature header name", Kind: connector.FieldString,
			Default: "X-Signature",
			Description: "LemonSqueezy: X-Signature. GitHub: X-Hub-Signature-256.",
		},
		{
			Name: fSigPrefix, Label: "Header prefix to strip before compare", Kind: connector.FieldString,
			Description: "e.g. 'sha256=' for GitHub. Empty for plain hex (LemonSqueezy).",
		},
		{
			Name: fResponseBody, Label: "Static response body", Kind: connector.FieldLongText,
			Description: "Returned verbatim. Ignored if response_file is set.",
		},
		{
			Name: fResponseFile, Label: "Response file path", Kind: connector.FieldString, IsPath: true,
			Description: "Read at request time (NOT preloaded). Wins over response_body. With PGF_ALLOWED_PATHS set, must be within an allowed root.",
		},
		{
			Name: fResponseType, Label: "Response Content-Type", Kind: connector.FieldString,
			Default: "text/plain; charset=utf-8",
		},
		{
			Name: fResponseStatus, Label: "Response status", Kind: connector.FieldInt, Default: 200,
		},
		{
			Name: fAllowedMethods, Label: "Allowed HTTP methods", Kind: connector.FieldStringList,
			Description: "Comma-separated. Empty = any method allowed.",
		},
		{
			Name: fStripHeaders, Label: "Strip these headers from emitted events", Kind: connector.FieldStringList,
			Default:     "Authorization,Cookie,Proxy-Authorization,X-Mw-Secret",
			Description: "Authorization headers stay out of stdout to avoid leaking via downstream logs.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset { return nil }

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	// Coerce response_status to int when wizard returned it as a string.
	if s, ok := out[fResponseStatus].(string); ok {
		if n, err := strconv.Atoi(s); err == nil {
			out[fResponseStatus] = n
		}
	}
	return out
}

// Validate is a no-op — there's no upstream service to probe.
func (credSpec) Validate(_ context.Context, _ connector.Credential) error { return nil }
