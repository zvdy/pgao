package db

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// applyTLSConfig extends the pgxpool config with operator-supplied TLS
// material: root CA, client cert/key, and a server-name override. All
// fields are optional; an empty ConnectionConfig leaves pgx's parsed
// TLS handling alone.
//
// Validation surface (returns a wrapped error):
//   - SSLCert and SSLKey are paired; supplying one without the other is
//     rejected at config time so we never silently drop client auth.
//   - File reads happen here (not at first connect) so misconfigured
//     paths fail fast; the supervisor records the error in cluster
//     status instead of looping silently on every probe.
func applyTLSConfig(poolConfig *pgxpool.Config, c ConnectionConfig) error {
	if c.SSLRootCert == "" && c.SSLCert == "" && c.SSLKey == "" && c.SSLServerName == "" {
		return nil
	}
	if (c.SSLCert == "") != (c.SSLKey == "") {
		return errors.New("ssl_cert and ssl_key must be set together")
	}

	// pgx's parsed config already has a TLSConfig when the URL hinted at
	// TLS (sslmode=require / verify-*). Preserve it so we layer on top
	// instead of clobbering insecure defaults.
	tlsCfg := poolConfig.ConnConfig.TLSConfig
	if tlsCfg == nil {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		tlsCfg = tlsCfg.Clone()
	}

	if c.SSLRootCert != "" {
		pem, err := os.ReadFile(c.SSLRootCert)
		if err != nil {
			return fmt.Errorf("read ssl_root_cert %q: %w", c.SSLRootCert, err)
		}
		pool := tlsCfg.RootCAs
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("ssl_root_cert %q contains no PEM certificates", c.SSLRootCert)
		}
		tlsCfg.RootCAs = pool
	}

	if c.SSLCert != "" && c.SSLKey != "" {
		cert, err := tls.LoadX509KeyPair(c.SSLCert, c.SSLKey)
		if err != nil {
			return fmt.Errorf("load ssl_cert %q + ssl_key %q: %w", c.SSLCert, c.SSLKey, err)
		}
		tlsCfg.Certificates = append(tlsCfg.Certificates, cert)
	}

	if c.SSLServerName != "" {
		tlsCfg.ServerName = c.SSLServerName
	}

	poolConfig.ConnConfig.TLSConfig = tlsCfg
	return nil
}
