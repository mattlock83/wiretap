package tls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultCertValidity = 365 * 24 * time.Hour
)

// Manager handles CA loading and on-demand leaf certificate generation.
type Manager struct {
	mu          sync.Mutex
	baseDir     string
	caCertPath  string
	caKeyPath   string
	ca          *x509.Certificate
	caKey       *rsa.PrivateKey
	caCertPEM   []byte
	certCache   map[string]*tls.Certificate
	now         func() time.Time
	serialMutex sync.Mutex
	serial      *big.Int
}

// NewManager creates a CA manager rooted at baseDir.
// Files are created as needed and cached in memory.
func NewManager(baseDir string) *Manager {
	if baseDir == "" {
		cacheDir, err := os.UserCacheDir()
		if err != nil || cacheDir == "" {
			cacheDir = os.TempDir()
		}
		baseDir = filepath.Join(cacheDir, "wiretap", "ca")
	}
	return &Manager{
		baseDir:    baseDir,
		caCertPath: filepath.Join(baseDir, "ca-cert.pem"),
		caKeyPath:  filepath.Join(baseDir, "ca-key.pem"),
		certCache:  make(map[string]*tls.Certificate),
		now:        time.Now,
		serial:     big.NewInt(1),
	}
}

// CACertPEM returns the PEM-encoded CA certificate, creating it if needed.
func (m *Manager) CACertPEM() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureCA(); err != nil {
		return nil, err
	}
	return append([]byte(nil), m.caCertPEM...), nil
}

// GetOrCreateCert returns a leaf certificate for host, signed by the CA.
func (m *Manager) GetOrCreateCert(host string) (*tls.Certificate, error) {
	if host == "" {
		return nil, errors.New("host is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureCA(); err != nil {
		return nil, err
	}
	if cert, ok := m.certCache[host]; ok {
		return cert, nil
	}

	leafKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}

	now := m.now()
	notBefore := now.Add(-5 * time.Minute)
	notAfter := now.Add(defaultCertValidity)

	serial, err := m.nextSerial()
	if err != nil {
		return nil, err
	}

	leafTmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: host,
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}
	if ip := net.ParseIP(host); ip != nil {
		leafTmpl.IPAddresses = []net.IP{ip}
	} else {
		leafTmpl.DNSNames = []string{host}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, leafTmpl, m.ca, &leafKey.PublicKey, m.caKey)
	if err != nil {
		return nil, fmt.Errorf("sign leaf cert: %w", err)
	}

	leafCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	leafKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(leafKey)})

	pair, err := tls.X509KeyPair(leafCert, leafKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse key pair: %w", err)
	}

	m.certCache[host] = &pair
	return &pair, nil
}

func (m *Manager) ensureCA() error {
	if m.ca != nil && m.caKey != nil {
		return nil
	}

	if err := os.MkdirAll(m.baseDir, 0o700); err != nil {
		return fmt.Errorf("create CA dir: %w", err)
	}

	certPEM, keyPEM, err := m.loadCAFromDisk()
	if err == nil {
		cert, key, err := parseCA(certPEM, keyPEM)
		if err != nil {
			return err
		}
		m.ca = cert
		m.caKey = key
		m.caCertPEM = certPEM
		return nil
	}

	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	cert, key, certPEM, keyPEM, err := generateCA(m.now())
	if err != nil {
		return err
	}

	if err := os.WriteFile(m.caCertPath, certPEM, 0o644); err != nil {
		return fmt.Errorf("write CA cert: %w", err)
	}
	if err := os.WriteFile(m.caKeyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write CA key: %w", err)
	}

	m.ca = cert
	m.caKey = key
	m.caCertPEM = certPEM
	return nil
}

func (m *Manager) loadCAFromDisk() ([]byte, []byte, error) {
	certPEM, err := os.ReadFile(m.caCertPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(m.caKeyPath)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

func parseCA(certPEM, keyPEM []byte) (*x509.Certificate, *rsa.PrivateKey, error) {
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, errors.New("failed to decode CA cert")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA cert: %w", err)
	}
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, errors.New("failed to decode CA key")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse CA key: %w", err)
	}
	return cert, key, nil
}

func generateCA(now time.Time) (*x509.Certificate, *rsa.PrivateKey, []byte, []byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "Wiretap Proxy CA",
			Organization: []string{"Wiretap"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(5 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create CA cert: %w", err)
	}

	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("parse generated CA: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	return cert, key, certPEM, keyPEM, nil
}

func (m *Manager) nextSerial() (*big.Int, error) {
	m.serialMutex.Lock()
	defer m.serialMutex.Unlock()

	serial := new(big.Int).Set(m.serial)
	m.serial.Add(m.serial, big.NewInt(1))
	return serial, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	return serial, nil
}
