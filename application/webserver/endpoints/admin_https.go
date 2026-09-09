package gatesentryWebserverEndpoints

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"

	gatesentry2storage "bitbucket.org/abdullah_irfan/gatesentryf/storage"
)

// ApplyAdminHTTPS restarts the HTTPS admin listener after settings change.
// Set from the webserver package to avoid an import cycle.
var ApplyAdminHTTPS func(settings *gatesentry2storage.MapStore)

// SplitCertKeyPEM pulls CERTIFICATE and PRIVATE KEY blocks from one PEM blob.
func SplitCertKeyPEM(blob string) (certPEM, keyPEM string) {
	rest := []byte(blob)
	var certs []byte
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		encoded := pem.EncodeToMemory(block)
		switch {
		case block.Type == "CERTIFICATE":
			certs = append(certs, encoded...)
		case strings.Contains(block.Type, "PRIVATE KEY"):
			keyPEM += string(encoded)
		}
	}
	return string(certs), keyPEM
}

// ValidateServerTLS checks that certPEM and keyPEM form a usable TLS pair.
func ValidateServerTLS(certPEM, keyPEM string) error {
	if strings.TrimSpace(certPEM) == "" || strings.TrimSpace(keyPEM) == "" {
		return errors.New("server certificate and private key are required")
	}
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return err
	}
	return nil
}

// ParseCertificatePEM returns CN and expiry for the first certificate in pemData.
func ParseCertificatePEM(pemData string) (name, expiry string, err error) {
	return getCertInfo(pemData)
}

// ValidateCAPEM checks that pemData contains at least one X.509 certificate.
func ValidateCAPEM(pemData string) error {
	rest := []byte(pemData)
	found := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return errors.New("no certificate found in PEM")
	}
	return nil
}
