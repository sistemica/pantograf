// Package imap is a thin wrapper around emersion/go-imap/v2 that handles
// dial + auth based on a flat config struct. Connector code uses the
// returned *imapclient.Client directly for fetch/search/append.
package imap

import (
	"crypto/tls"
	"errors"
	"fmt"

	"github.com/emersion/go-imap/v2/imapclient"
)

// Security selects the transport mode.
type Security string

const (
	SecurityTLS      Security = "tls"      // implicit TLS, typical port 993
	SecuritySTARTTLS Security = "starttls" // upgrade after greeting, port 143
	SecurityNone     Security = "none"     // plaintext (test only)
)

// Config carries everything Dial needs. Pull these out of a connector
// Credential's Values map — the connector owns the field names.
type Config struct {
	Host     string
	Port     int
	Security Security
	Username string
	Password string
	// InsecureSkipVerify disables TLS certificate checks. Off by default.
	InsecureSkipVerify bool
}

// Dial opens an authenticated IMAP connection. Caller must Close().
func Dial(cfg Config) (*imapclient.Client, error) {
	if cfg.Host == "" {
		return nil, errors.New("imap: host required")
	}
	if cfg.Port == 0 {
		switch cfg.Security {
		case SecuritySTARTTLS, SecurityNone:
			cfg.Port = 143
		default:
			cfg.Port = 993
		}
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	tlsCfg := &tls.Config{
		ServerName:         cfg.Host,
		InsecureSkipVerify: cfg.InsecureSkipVerify, //nolint:gosec // opt-in only
	}

	var (
		c   *imapclient.Client
		err error
	)
	switch cfg.Security {
	case SecuritySTARTTLS:
		c, err = imapclient.DialStartTLS(addr, &imapclient.Options{TLSConfig: tlsCfg})
	case SecurityNone:
		c, err = imapclient.DialInsecure(addr, nil)
	default: // TLS
		c, err = imapclient.DialTLS(addr, &imapclient.Options{TLSConfig: tlsCfg})
	}
	if err != nil {
		return nil, fmt.Errorf("imap: dial %s: %w", addr, err)
	}
	if err := c.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("imap: login %s: %w", cfg.Username, err)
	}
	return c, nil
}
