package email

import (
	"context"
	"fmt"
	"strings"

	"github.com/sistemica/pantograf/connector"
	imaptr "github.com/sistemica/pantograf/transport/imap"
	smtptr "github.com/sistemica/pantograf/transport/smtp"
)

const (
	fEmail        = "email"
	fPassword     = "password"
	fImapHost     = "imap_host"
	fImapPort     = "imap_port"
	fImapSecurity = "imap_security"
	fSmtpHost     = "smtp_host"
	fSmtpPort     = "smtp_port"
	fSmtpSecurity = "smtp_security"
)

type credSpec struct{}

func (credSpec) Kind() connector.AuthKind { return connector.AuthBasic }

func (credSpec) Schema() connector.Schema {
	securityOptions := []connector.EnumOption{
		{Value: "tls", Label: "TLS (implicit)"},
		{Value: "starttls", Label: "STARTTLS"},
		{Value: "none", Label: "None (plaintext)"},
	}
	return connector.Schema{Fields: []connector.FieldSpec{
		{Name: fEmail, Label: "Email address", Kind: connector.FieldString, Required: true},
		{Name: fPassword, Label: "Password / app password", Kind: connector.FieldSecret, Required: true},
		{Name: fImapHost, Label: "IMAP host", Kind: connector.FieldString, Required: true},
		{Name: fImapPort, Label: "IMAP port", Kind: connector.FieldInt, Default: 993},
		{Name: fImapSecurity, Label: "IMAP security", Kind: connector.FieldEnum, Options: securityOptions, Default: "tls"},
		{Name: fSmtpHost, Label: "SMTP host", Kind: connector.FieldString, Required: true},
		{Name: fSmtpPort, Label: "SMTP port", Kind: connector.FieldInt, Default: 465},
		{Name: fSmtpSecurity, Label: "SMTP security", Kind: connector.FieldEnum, Options: securityOptions, Default: "tls"},
	}}
}

func (credSpec) Presets() []connector.Preset {
	return []connector.Preset{
		{
			Name:        "Fastmail",
			Description: "imap.fastmail.com:993 / smtp.fastmail.com:465 — requires app password",
			Values: connector.Values{
				fImapHost: "imap.fastmail.com", fImapPort: 993, fImapSecurity: "tls",
				fSmtpHost: "smtp.fastmail.com", fSmtpPort: 465, fSmtpSecurity: "tls",
			},
		},
		{
			Name:        "GMX",
			Description: "imap.gmx.net:993 / mail.gmx.net:587",
			Values: connector.Values{
				fImapHost: "imap.gmx.net", fImapPort: 993, fImapSecurity: "tls",
				fSmtpHost: "mail.gmx.net", fSmtpPort: 587, fSmtpSecurity: "starttls",
			},
		},
		{
			Name:        "Gmail",
			Description: "imap.gmail.com / smtp.gmail.com — requires app password (2FA on)",
			Values: connector.Values{
				fImapHost: "imap.gmail.com", fImapPort: 993, fImapSecurity: "tls",
				fSmtpHost: "smtp.gmail.com", fSmtpPort: 465, fSmtpSecurity: "tls",
			},
		},
		{
			Name:        "Custom",
			Description: "Enter all values manually",
			Values:      connector.Values{},
		},
	}
}

// Defaults derives smtp_host from imap_host when only one is set, and
// auto-fills the email's domain into both hosts when neither is set.
// Cheap heuristics; the user can always override.
func (credSpec) Defaults(p connector.Values) connector.Values {
	out := connector.Values{}
	for k, v := range p {
		out[k] = v
	}
	imap, smtp := strings.TrimSpace(out.String(fImapHost)), strings.TrimSpace(out.String(fSmtpHost))
	switch {
	case imap != "" && smtp == "":
		out[fSmtpHost] = strings.Replace(imap, "imap.", "smtp.", 1)
	case smtp != "" && imap == "":
		out[fImapHost] = strings.Replace(smtp, "smtp.", "imap.", 1)
	}
	return out
}

// Validate runs a real IMAP login and an SMTP dial+auth+close probe. Both
// matter because providers can grant divergent permissions (e.g. Gmail
// app-pass scoped only to SMTP).
func (credSpec) Validate(ctx context.Context, c connector.Credential) error {
	cli, err := imaptr.Dial(imapConfigFromCred(c))
	if err != nil {
		return fmt.Errorf("IMAP login failed: %w", err)
	}
	_ = cli.Logout().Wait()
	_ = cli.Close()

	if err := smtptr.Probe(ctx, smtpConfigFromCred(c)); err != nil {
		return fmt.Errorf("SMTP login failed: %w", err)
	}
	return nil
}

func imapConfigFromCred(c connector.Credential) imaptr.Config {
	v := c.Values
	return imaptr.Config{
		Host:     v.String(fImapHost),
		Port:     v.Int(fImapPort),
		Security: imaptr.Security(v.String(fImapSecurity)),
		Username: v.String(fEmail),
		Password: v.String(fPassword),
	}
}

func smtpConfigFromCred(c connector.Credential) smtptr.Config {
	v := c.Values
	return smtptr.Config{
		Host:     v.String(fSmtpHost),
		Port:     v.Int(fSmtpPort),
		Security: smtptr.Security(v.String(fSmtpSecurity)),
		Username: v.String(fEmail),
		Password: v.String(fPassword),
	}
}
