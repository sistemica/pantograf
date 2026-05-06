package smtp

import "crypto/tls"

func insecureTLS(host string) *tls.Config {
	return &tls.Config{ServerName: host, InsecureSkipVerify: true} //nolint:gosec
}
