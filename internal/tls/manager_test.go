package tls

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateCA(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	certPEM, err := mgr.CACertPEM()
	if err != nil {
		t.Fatalf("CACertPEM failed: %v", err)
	}

	certPath := filepath.Join(dir, "ca-cert.pem")
	keyPath := filepath.Join(dir, "ca-key.pem")
	if _, err := os.Stat(certPath); err != nil {
		t.Fatalf("expected CA cert file: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected CA key file: %v", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		t.Fatalf("failed to decode CA cert PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	if !cert.IsCA {
		t.Fatalf("expected CA cert")
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Fatalf("expected self-signed CA cert: %v", err)
	}
}

func TestGetOrCreateCert(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir)

	leaf, err := mgr.GetOrCreateCert("example.test")
	if err != nil {
		t.Fatalf("GetOrCreateCert failed: %v", err)
	}

	if len(leaf.Certificate) == 0 {
		t.Fatalf("expected certificate chain")
	}
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}
	if cert.Subject.CommonName != "example.test" {
		t.Fatalf("unexpected CN: %s", cert.Subject.CommonName)
	}
	found := false
	for _, name := range cert.DNSNames {
		if name == "example.test" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected DNS name in cert")
	}
}
