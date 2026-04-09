package cert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	CACertFileName     = "cert-ca.pem"
	CAKeyFileName      = "key-ca.pem"
	CertFileName       = "cert-tls.pem"
	KeyFileName        = "key-tls.pem"
	ClientCertFileName = "cert-client.pem"
	ClientKeyFileName  = "key-client.pem"
	SAKeyFileName      = "sa.key"
	SAPubFileName      = "sa.pub"

	DefaultDirPermission = 0o750
)

// Data contains the certificate and key data in PEM format.
type Data struct {
	Path       string
	CACert     []byte
	ServerCert []byte
	ServerKey  []byte
	ClientCert []byte
	ClientKey  []byte
	SAKey      []byte
	SAPub      []byte
}

// CABundle returns the CA certificate as a base64-encoded string.
func (d *Data) CABundle() []byte {
	return []byte(base64.StdEncoding.EncodeToString(d.CACert))
}

// New generates all TLS certificates and SA key pair in the specified path.
func New(
	path string,
	validity time.Duration,
	sans []string,
) (*Data, error) {
	if err := os.MkdirAll(path, DefaultDirPermission); err != nil {
		return nil, fmt.Errorf("failed to create cert directory: %w", err)
	}

	// Generate CA
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	caSerial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			CommonName: "envtest-ca",
		},
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caCertDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	caCertPEM := encodeCert(caCertDER)
	caKeyPEM, err := encodeECKey(caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to encode CA key: %w", err)
	}

	// Generate server cert
	serverCertPEM, serverKeyPEM, err := generateSignedCert(caCert, caKey, validity, pkix.Name{
		CommonName: "envtest-server",
	}, sans, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth})
	if err != nil {
		return nil, fmt.Errorf("failed to generate server cert: %w", err)
	}

	// Generate client cert (CN=system:admin, O=system:masters for RBAC admin access)
	clientCertPEM, clientKeyPEM, err := generateSignedCert(caCert, caKey, validity, pkix.Name{
		CommonName:   "system:admin",
		Organization: []string{"system:masters"},
	}, nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth})
	if err != nil {
		return nil, fmt.Errorf("failed to generate client cert: %w", err)
	}

	// Generate SA key pair
	saKeyPEM, saPubPEM, err := generateRSAKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate SA key pair: %w", err)
	}

	// Write all files
	files := map[string]struct {
		data []byte
		perm os.FileMode
	}{
		CACertFileName:     {caCertPEM, 0o644},
		CAKeyFileName:      {caKeyPEM, 0o600},
		CertFileName:       {serverCertPEM, 0o644},
		KeyFileName:        {serverKeyPEM, 0o600},
		ClientCertFileName: {clientCertPEM, 0o644},
		ClientKeyFileName:  {clientKeyPEM, 0o600},
		SAKeyFileName:      {saKeyPEM, 0o600},
		SAPubFileName:      {saPubPEM, 0o644},
	}

	for name, f := range files {
		if err := os.WriteFile(filepath.Join(path, name), f.data, f.perm); err != nil {
			return nil, fmt.Errorf("failed to write %s: %w", name, err)
		}
	}

	return &Data{
		Path:       path,
		CACert:     caCertPEM,
		ServerCert: serverCertPEM,
		ServerKey:  serverKeyPEM,
		ClientCert: clientCertPEM,
		ClientKey:  clientKeyPEM,
		SAKey:      saKeyPEM,
		SAPub:      saPubPEM,
	}, nil
}

func generateSignedCert(
	caCert *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	validity time.Duration,
	subject pkix.Name,
	sans []string,
	extKeyUsage []x509.ExtKeyUsage,
) (certPEM []byte, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, nil, err
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               subject,
		NotBefore:             now.Add(-1 * time.Minute),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           extKeyUsage,
		BasicConstraintsValid: true,
	}

	for _, san := range sans {
		if ip := net.ParseIP(san); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, san)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal key: %w", err)
	}

	return encodeCert(certDER), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func generateRSAKeyPair() (keyPEM []byte, pubPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}

	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal public key: %w", err)
	}
	pubPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})

	return keyPEM, pubPEM, nil
}

func encodeCert(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func encodeECKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}
	return serial, nil
}
