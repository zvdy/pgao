package db

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// writeSelfSignedCert generates an in-memory throwaway cert + key and
// writes them to disk. Used to exercise applyTLSConfig without baking
// fixtures into the repo. The pair is also wired together so it can be
// loaded as a client cert.
func writeSelfSignedCert(t *testing.T, dir string) (caPath, certPath, keyPath string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "pgao-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	caPath = filepath.Join(dir, "ca.pem")
	certPath = filepath.Join(dir, "client.pem")
	keyPath = filepath.Join(dir, "client.key")

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return caPath, certPath, keyPath
}

// freshPoolConfig returns a pgxpool.Config that hasn't been through
// applyTLSConfig yet. Uses a non-routable connection string so we never
// actually dial.
func freshPoolConfig(t *testing.T) *pgxpool.Config {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:5432/db?sslmode=require")
	if err != nil {
		t.Fatalf("parse base config: %v", err)
	}
	return cfg
}

func TestApplyTLSConfig_NoOpWhenAllFieldsEmpty(t *testing.T) {
	pc := freshPoolConfig(t)
	before := pc.ConnConfig.TLSConfig
	if err := applyTLSConfig(pc, ConnectionConfig{}); err != nil {
		t.Fatalf("expected nil error on empty config, got %v", err)
	}
	if pc.ConnConfig.TLSConfig != before {
		t.Errorf("TLSConfig was rewritten when no fields were set")
	}
}

func TestApplyTLSConfig_RequiresPairedCertAndKey(t *testing.T) {
	cases := []ConnectionConfig{
		{SSLCert: "/tmp/cert.pem"},
		{SSLKey: "/tmp/key.pem"},
	}
	for i, c := range cases {
		err := applyTLSConfig(freshPoolConfig(t), c)
		if err == nil {
			t.Errorf("case %d: expected error for unpaired cert/key, got nil", i)
			continue
		}
		if !strings.Contains(err.Error(), "ssl_cert and ssl_key must be set together") {
			t.Errorf("case %d: wrong error: %v", i, err)
		}
	}
}

func TestApplyTLSConfig_LoadsRootCAAndClientCert(t *testing.T) {
	dir := t.TempDir()
	caPath, certPath, keyPath := writeSelfSignedCert(t, dir)

	pc := freshPoolConfig(t)
	err := applyTLSConfig(pc, ConnectionConfig{
		SSLRootCert:   caPath,
		SSLCert:       certPath,
		SSLKey:        keyPath,
		SSLServerName: "db.example.internal",
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}

	tlsCfg := pc.ConnConfig.TLSConfig
	if tlsCfg == nil {
		t.Fatal("expected TLSConfig to be set")
	}
	if tlsCfg.ServerName != "db.example.internal" {
		t.Errorf("ServerName=%q, want override", tlsCfg.ServerName)
	}
	if tlsCfg.RootCAs == nil {
		t.Error("expected RootCAs to include the supplied PEM")
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("expected 1 client cert, got %d", len(tlsCfg.Certificates))
	}
}

func TestApplyTLSConfig_RejectsMalformedRootCA(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(bad, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := applyTLSConfig(freshPoolConfig(t), ConnectionConfig{SSLRootCert: bad})
	if err == nil || !strings.Contains(err.Error(), "no PEM certificates") {
		t.Errorf("expected no-PEM error, got %v", err)
	}
}

func TestApplyTLSConfig_ReportsMissingFile(t *testing.T) {
	err := applyTLSConfig(freshPoolConfig(t), ConnectionConfig{
		SSLRootCert: "/path/that/does/not/exist.pem",
	})
	if err == nil || !strings.Contains(err.Error(), "read ssl_root_cert") {
		t.Errorf("expected read-error, got %v", err)
	}
}

func TestApplyTLSConfig_PreservesParsedTLSWhenOnlyServerNameSet(t *testing.T) {
	pc := freshPoolConfig(t)
	// Pretend pgx already gave us a TLS config with non-default min version.
	if pc.ConnConfig.TLSConfig == nil {
		pc.ConnConfig.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	} else {
		pc.ConnConfig.TLSConfig.MinVersion = tls.VersionTLS13
	}
	if err := applyTLSConfig(pc, ConnectionConfig{SSLServerName: "alt.example"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if pc.ConnConfig.TLSConfig.MinVersion != tls.VersionTLS13 {
		t.Error("MinVersion was overwritten when overlaying ServerName")
	}
	if pc.ConnConfig.TLSConfig.ServerName != "alt.example" {
		t.Errorf("ServerName not applied: %q", pc.ConnConfig.TLSConfig.ServerName)
	}
}
