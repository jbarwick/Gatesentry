package gatesentryWebserverEndpoints

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func testPEMs(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "admin.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"admin.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}))
	return certPEM, keyPEM
}

func TestValidateServerTLS(t *testing.T) {
	cert, key := testPEMs(t)
	if err := ValidateServerTLS(cert, key); err != nil {
		t.Fatal(err)
	}
	if err := ValidateServerTLS("", key); err == nil {
		t.Fatal("expected error for empty cert")
	}
	if err := ValidateServerTLS(cert, cert); err == nil {
		t.Fatal("expected error for cert used as key")
	}
}

func TestSplitCertKeyPEM(t *testing.T) {
	cert, key := testPEMs(t)
	c, k := SplitCertKeyPEM(cert + key)
	if c == "" || k == "" {
		t.Fatalf("cert or key empty after split")
	}
	if err := ValidateServerTLS(c, k); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCAPEM(t *testing.T) {
	cert, _ := testPEMs(t)
	if err := ValidateCAPEM(cert); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCAPEM("not a cert"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseCertificatePEM(t *testing.T) {
	cert, _ := testPEMs(t)
	name, expiry, err := ParseCertificatePEM(cert)
	if err != nil {
		t.Fatal(err)
	}
	if name != "admin.test" {
		t.Fatalf("name=%q", name)
	}
	if expiry == "" {
		t.Fatal("empty expiry")
	}
}
