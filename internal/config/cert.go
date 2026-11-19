package config

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"strings"
	"time"

	"github.com/rm4n0s/errors"
)

// Tags of the errors of the certificate.
const (
	// TagCertificateFailed: a new certificate could not be made.
	TagCertificateFailed = "CertificateFailed"
	// TagCertificateInvalid: cert.pem and key.pem are not a certificate
	// and its key; "New certificate" replaces them.
	TagCertificateInvalid = "CertificateInvalid"
)

// The certificate's files and what a new one is (SPEC §8.1).
const (
	certFile = "cert.pem"
	keyFile  = "key.pem"
	rsaBits  = 2048
	// certLifetime is long: clients remember the certificate, and a new
	// one makes each of them ask again.
	certLifetime = 20 * 365 * 24 * time.Hour
)

// Certificate is the server's TLS certificate.
type Certificate struct {
	// TLS is the certificate and its key, for rdp.Config.
	TLS tls.Certificate
	// Fingerprint is the SHA-256 of the certificate, as hex pairs joined
	// by colons, as FreeRDP shows it.
	Fingerprint string
}

// fingerprint formats the SHA-256 of der.
func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = hex.EncodeToString([]byte{b})
	}
	return strings.Join(pairs, ":")
}

// Certificate returns the certificate of the folder, making one on the
// first start; created says it was made now. A certificate that cannot be
// read is an error, not replaced: clients remember it, so only the user
// replaces it (NewCertificate).
func (f *Folder) Certificate(ctx context.Context) (c Certificate, created bool, err error) {
	certPEM, haveCert, err := f.readFile(certFile)
	if err != nil {
		return Certificate{}, false, err
	}
	keyPEM, haveKey, err := f.readFile(keyFile)
	if err != nil {
		return Certificate{}, false, err
	}
	if !haveCert && !haveKey {
		c, err := f.NewCertificate(ctx)
		return c, err == nil, err
	}
	if !haveCert || !haveKey {
		return Certificate{}, false, errors.New(TagCertificateInvalid, "only one of cert.pem and key.pem is in mardp's folder",
			"cert", haveCert, "key", haveKey)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Certificate{}, false, errors.NewErr(TagCertificateInvalid, err).SetMetadata("path", f.Path)
	}
	return Certificate{TLS: pair, Fingerprint: fingerprint(pair.Certificate[0])}, false, nil
}

// NewCertificate makes a new self-signed RSA 2048 certificate and replaces
// the folder's.
func (f *Folder) NewCertificate(ctx context.Context) (Certificate, error) {
	return f.newCertificate(ctx, time.Now())
}

// newCertificate makes a certificate valid from now.
func (f *Folder) newCertificate(ctx context.Context, now time.Time) (Certificate, error) {
	if err := ctx.Err(); err != nil {
		return Certificate{}, errors.NewErr(TagCertificateFailed, err)
	}
	key, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return Certificate{}, errors.NewErr(TagCertificateFailed, err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return Certificate{}, errors.NewErr(TagCertificateFailed, err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "mardp"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(certLifetime),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return Certificate{}, errors.NewErr(TagCertificateFailed, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Certificate{}, errors.NewErr(TagCertificateFailed, err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	// The key first: a cert.pem next to the old key.pem would not load.
	if err := f.write(ctx, keyFile, keyPEM); err != nil {
		return Certificate{}, err
	}
	if err := f.write(ctx, certFile, certPEM); err != nil {
		return Certificate{}, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return Certificate{}, errors.NewErr(TagCertificateFailed, err)
	}
	f.log.Info("a new certificate was made", "fingerprint", fingerprint(der))
	return Certificate{TLS: pair, Fingerprint: fingerprint(der)}, nil
}
