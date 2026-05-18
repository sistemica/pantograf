package file

import (
	"context"
	"fmt"
	"strings"

	"github.com/sistemica/pantograf/connector"
)

const (
	fBackend = "backend"

	// local
	fRoot = "root"

	// s3
	fEndpoint  = "endpoint"
	fRegion    = "region"
	fBucket    = "bucket"
	fAccessKey = "access_key"
	fSecretKey = "secret_key"
	fUseSSL    = "use_ssl"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthCustom }

func (credSpec) Schema() connector.Schema {
	isS3 := func(v connector.Values) bool { return v.String(fBackend) == "s3" }
	isLocal := func(v connector.Values) bool { return v.String(fBackend) == "local" }
	return connector.Schema{Fields: []connector.FieldSpec{
		{
			Name: fBackend, Label: "Backend", Kind: connector.FieldEnum, Required: true,
			Default: "local",
			Options: []connector.EnumOption{
				{Value: "local", Label: "Local filesystem"},
				{Value: "s3", Label: "S3 / S3-compatible (MinIO, R2, B2, ...)"},
			},
			Description: "Picks the storage driver. Determines which fields below are prompted.",
		},

		// local backend
		{
			Name: fRoot, Label: "Local root directory", Kind: connector.FieldString,
			Required: true, IsPath: true,
			ShowWhen: isLocal,
			Description: "Absolute path the connector treats as its root. Keys are interpreted relative to this. Traversal outside is rejected. Subject to PGF_ALLOWED_PATHS.",
		},

		// s3 backend
		{
			Name: fEndpoint, Label: "S3 endpoint", Kind: connector.FieldString,
			Required: true,
			ShowWhen: isS3,
			Description: "Host[:port] or full https:// URL. e.g. s3.amazonaws.com, https://r2.example.com, http://localhost:9000 (MinIO).",
		},
		{
			Name: fRegion, Label: "S3 region", Kind: connector.FieldString,
			Default: "us-east-1",
			ShowWhen: isS3,
			Description: "Region string. AWS expects the real region; MinIO and R2 accept us-east-1.",
		},
		{
			Name: fBucket, Label: "S3 bucket", Kind: connector.FieldString, Required: true,
			ShowWhen: isS3,
			Description: "Bucket name. One bucket per pgf instance — register a second instance for a second bucket.",
		},
		{
			Name: fAccessKey, Label: "S3 access key", Kind: connector.FieldString, Required: true,
			ShowWhen: isS3,
			Description: "S3 access key ID. Stored in cleartext; the secret_key alongside is sealed.",
		},
		{
			Name: fSecretKey, Label: "S3 secret key", Kind: connector.FieldSecret, Required: true,
			ShowWhen: isS3,
			Description: "S3 secret key. Sealed by pgf's vault — never visible to the agent.",
		},
		{
			Name: fUseSSL, Label: "Use HTTPS (when endpoint is bare host)", Kind: connector.FieldBool,
			Default: true,
			ShowWhen: isS3,
			Description: "Ignored when endpoint is a full http(s):// URL (the URL scheme wins).",
		},
	}}
}

func (credSpec) Presets() []connector.Preset {
	return []connector.Preset{
		{
			Name: "Local",
			Description: "Filesystem under a chosen root.",
			Values: connector.Values{fBackend: "local"},
		},
		{
			Name: "MinIO (local docker)",
			Description: "http://localhost:9000 with default minioadmin credentials.",
			Values: connector.Values{
				fBackend:   "s3",
				fEndpoint:  "http://localhost:9000",
				fRegion:    "us-east-1",
				fAccessKey: "minioadmin",
				fUseSSL:    false,
			},
		},
		{
			Name: "AWS S3",
			Description: "Public AWS S3 over HTTPS.",
			Values: connector.Values{
				fBackend:  "s3",
				fEndpoint: "s3.amazonaws.com",
				fUseSSL:   true,
			},
		},
		{
			Name: "Cloudflare R2",
			Description: "R2 endpoint (account-specific).",
			Values: connector.Values{
				fBackend:  "s3",
				fEndpoint: "https://<account>.r2.cloudflarestorage.com",
				fRegion:   "auto",
				fUseSSL:   true,
			},
		},
		{
			Name: "Custom",
			Description: "Fill all fields manually.",
			Values: connector.Values{},
		},
	}
}

func (credSpec) Defaults(p connector.Values) connector.Values {
	out := make(connector.Values, len(p))
	for k, v := range p {
		out[k] = v
	}
	if s, ok := out[fEndpoint].(string); ok {
		out[fEndpoint] = strings.TrimRight(s, "/")
	}
	if s, ok := out[fRoot].(string); ok {
		out[fRoot] = strings.TrimRight(s, "/")
	}
	return out
}

// Validate routes through the same Open path the runtime uses, then
// issues one List(prefix="") against the configured backend. That
// covers reachability + auth + (for s3) bucket existence in one probe.
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	drv, err := buildDriver(c)
	if err != nil {
		return err
	}
	entries, err := drv.List(ctx, "", false, 5)
	if err != nil {
		return fmt.Errorf("list probe: %w", err)
	}
	fmt.Printf("(backend=%s, %d entries) ", c.Values.String(fBackend), len(entries))
	return nil
}

// buildDriver picks the right driver impl based on the backend field.
// Single call site used by both Validate and Open.
func buildDriver(c connector.Credential) (driver, error) {
	switch backend := c.Values.String(fBackend); backend {
	case "local":
		return newLocalDriver(c.Values.String(fRoot))
	case "s3":
		return newS3Driver(
			c.Values.String(fEndpoint),
			c.Values.String(fRegion),
			c.Values.String(fAccessKey),
			c.Values.String(fSecretKey),
			c.Values.String(fBucket),
			boolDefault(c.Values, fUseSSL, true),
		)
	default:
		return nil, fmt.Errorf("unsupported backend %q (want local|s3)", backend)
	}
}

// boolDefault reads a Values bool with a true-default-when-missing
// semantic (use_ssl defaults true, but a credential created before this
// field existed has no entry).
func boolDefault(v connector.Values, key string, def bool) bool {
	if !v.Has(key) {
		return def
	}
	return v.Bool(key)
}
