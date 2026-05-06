// Package smtp wraps wneessen/go-mail for SMTP send. The connector builds
// a Message and hands it to Send which dials per call.
package smtp

import (
	"context"
	"errors"
	"fmt"

	"github.com/wneessen/go-mail"
)

type Security string

const (
	SecurityTLS      Security = "tls"      // implicit TLS, typical port 465
	SecuritySTARTTLS Security = "starttls" // upgrade, port 587
	SecurityNone     Security = "none"
)

type Config struct {
	Host               string
	Port               int
	Security           Security
	Username           string
	Password           string
	InsecureSkipVerify bool
}

// Send connects, authenticates, transmits all messages, and tears down.
// Each call is its own short-lived connection — fine for CLI-scale use;
// a future connector can hold a *mail.Client open if rate matters.
func Send(ctx context.Context, cfg Config, msgs ...*mail.Msg) error {
	if cfg.Host == "" {
		return errors.New("smtp: host required")
	}
	client, err := newClient(cfg)
	if err != nil {
		return err
	}
	if err := client.DialAndSendWithContext(ctx, msgs...); err != nil {
		return fmt.Errorf("smtp: send: %w", err)
	}
	return nil
}

// Probe dials and authenticates against the SMTP server, then closes — no
// message is sent. Used by the credential wizard's Validate.
func Probe(ctx context.Context, cfg Config) error {
	if cfg.Host == "" {
		return errors.New("smtp: host required")
	}
	client, err := newClient(cfg)
	if err != nil {
		return err
	}
	if err := client.DialWithContext(ctx); err != nil {
		return fmt.Errorf("smtp: probe: %w", err)
	}
	return client.Close()
}

func newClient(cfg Config) (*mail.Client, error) {
	if cfg.Port == 0 {
		switch cfg.Security {
		case SecuritySTARTTLS:
			cfg.Port = 587
		case SecurityNone:
			cfg.Port = 25
		default:
			cfg.Port = 465
		}
	}
	opts := []mail.Option{
		mail.WithPort(cfg.Port),
		mail.WithUsername(cfg.Username),
		mail.WithPassword(cfg.Password),
		mail.WithSMTPAuth(mail.SMTPAuthPlain),
	}
	switch cfg.Security {
	case SecuritySTARTTLS:
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	case SecurityNone:
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	default: // TLS
		opts = append(opts, mail.WithSSL())
	}
	if cfg.InsecureSkipVerify {
		opts = append(opts, mail.WithTLSConfig(insecureTLS(cfg.Host)))
	}
	c, err := mail.NewClient(cfg.Host, opts...)
	if err != nil {
		return nil, fmt.Errorf("smtp: client: %w", err)
	}
	return c, nil
}
