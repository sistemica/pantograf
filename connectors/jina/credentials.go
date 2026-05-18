package jina

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/sistemica/pantograf/connector"
	httptr "github.com/sistemica/pantograf/transport/http"
)

const (
	fAPIKey       = "api_key"
	fReaderBase   = "reader_base"
	fSearchBase   = "search_base"
	fGroundBase   = "ground_base"
	fDefaultEngine = "default_engine"
)

const (
	defaultReaderBase = "https://r.jina.ai"
	defaultSearchBase = "https://s.jina.ai"
	defaultGroundBase = "https://g.jina.ai"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthAPIKey }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fAPIKey, Label: "Jina API key (optional for read, REQUIRED for search/ground)", Kind: connector.FieldSecret,
			Description: "Sent as `Authorization: Bearer <key>`. Empty = anonymous tier — `read` works at 20 RPM, but `search` and `ground` return 401 with no key. Free key from jina.ai unlocks 500 RPM and the gated endpoints.",
		},
		{
			Name: fDefaultEngine, Label: "Default engine (optional)", Kind: connector.FieldEnum,
			Options: []connector.EnumOption{
				{Value: "", Label: "Jina default (direct fetch)"},
				{Value: "browser", Label: "Browser — render JS-heavy pages"},
				{Value: "direct", Label: "Direct — fast, no browser"},
				{Value: "cf-browser-rendering", Label: "Cloudflare rendering"},
			},
			Description: "Default for the `engine` param on read/search. Per-call override is always possible.",
		},
		{
			Name: fReaderBase, Label: "Reader endpoint", Kind: connector.FieldString,
			Default: defaultReaderBase,
			Description: "Override only for a self-hosted Reader mirror. Default https://r.jina.ai.",
		},
		{
			Name: fSearchBase, Label: "Search endpoint", Kind: connector.FieldString,
			Default: defaultSearchBase,
			Description: "Override only for self-hosted. Default https://s.jina.ai.",
		},
		{
			Name: fGroundBase, Label: "Grounding endpoint", Kind: connector.FieldString,
			Default: defaultGroundBase,
			Description: "Override only for self-hosted. Default https://g.jina.ai.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset {
	return []connector.Preset{
		{
			Name: "Jina hosted (free or keyed)",
			Description: "Default — point at https://{r,s,g}.jina.ai. Works without key on the free tier.",
			Values: connector.Values{
				fReaderBase: defaultReaderBase,
				fSearchBase: defaultSearchBase,
				fGroundBase: defaultGroundBase,
			},
		},
		{
			Name: "Self-hosted",
			Description: "Enter your own Reader mirror URLs.",
			Values: connector.Values{},
		},
	}
}

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	if u, ok := out[fReaderBase].(string); ok {
		out[fReaderBase] = strings.TrimRight(u, "/")
	}
	if u, ok := out[fSearchBase].(string); ok {
		out[fSearchBase] = strings.TrimRight(u, "/")
	}
	if u, ok := out[fGroundBase].(string); ok {
		out[fGroundBase] = strings.TrimRight(u, "/")
	}
	return out
}

// Validate hits the reader endpoint with a known-good URL (example.com)
// and checks for a 200. Cheap, works without a key, confirms the key is
// at least *accepted* when one is given.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	cli, err := readerClient(c)
	if err != nil {
		return err
	}
	tCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp readerResponse
	if err := cli.GetJSON(tCtx, "/https://example.com", nil, &resp); err != nil {
		return fmt.Errorf("reader probe: %w", err)
	}
	if resp.Data.Title == "" {
		return errors.New("reader probe: unexpected empty title")
	}
	fmt.Printf("(reader ok, key %s) ", keyStatus(c))
	return nil
}

func keyStatus(c connector.Credential) string {
	if c.Values.String(fAPIKey) == "" {
		return "anonymous"
	}
	return "present"
}

// readerClient builds the *httptr.Client used for the Reader endpoint.
// Search and Ground get their own clients built on demand (different base
// URLs, same auth).
func readerClient(c connector.Credential) (*httptr.Client, error) {
	return jinaClient(c, c.Values.String(fReaderBase), defaultReaderBase)
}

func searchClient(c connector.Credential) (*httptr.Client, error) {
	return jinaClient(c, c.Values.String(fSearchBase), defaultSearchBase)
}

func groundClient(c connector.Credential) (*httptr.Client, error) {
	return jinaClient(c, c.Values.String(fGroundBase), defaultGroundBase)
}

func jinaClient(c connector.Credential, base, fallback string) (*httptr.Client, error) {
	if base == "" {
		base = fallback
	}
	base = strings.TrimRight(base, "/")
	hdr := stdhttp.Header{}
	if key := c.Values.String(fAPIKey); key != "" {
		hdr.Set("Authorization", "Bearer "+key)
	}
	return httptr.New(httptr.Config{
		BaseURL: base,
		Headers: hdr,
		// Jina's hosted browser path can take 30s on heavy pages.
		Timeout: 90 * time.Second,
	})
}
