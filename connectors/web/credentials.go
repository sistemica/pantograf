package web

import (
	"context"

	"github.com/sistemica/pantograf/connector"
)

const (
	fCDPEndpoint       = "cdp_endpoint"
	fDefaultUserAgent  = "default_user_agent"
	fProxyURL          = "proxy_url"
)

const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) pantograf/0.1 (+https://github.com/sistemica/pantograf)"

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthNone }

func (credSpec) Schema() connector.Schema {
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fCDPEndpoint, Label: "Chrome DevTools Protocol endpoint (optional)",
			Kind: connector.FieldString,
			Description: "Connect-only — pgf never auto-spawns Chrome. Run: chromium --headless --remote-debugging-port=9222 --no-sandbox. Then set this to ws://localhost:9222. When empty, js=true actions return an error.",
		},
		{
			Name: fDefaultUserAgent, Label: "Default User-Agent (optional)",
			Kind: connector.FieldString,
			Description: "Sent on every HTTP fetch. Override per-call with -p user_agent=...",
		},
		{
			Name: fProxyURL, Label: "HTTP/SOCKS proxy URL (optional)",
			Kind: connector.FieldString,
			Description: "e.g. http://192.168.1.50:8118 or socks5://127.0.0.1:1080. Used for net/http; passed to Chrome via --proxy-server only if the CDP instance honours it.",
		},
	}}
}

func (credSpec) Presets() []connector.Preset { return nil }

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	return out
}

// Validate is a no-op for the web connector — there are no credentials to
// probe. CDP reachability is checked lazily on the first js=true call so
// that an unreachable browser doesn't block instance creation.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	return nil
}
