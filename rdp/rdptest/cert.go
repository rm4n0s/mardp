package rdptest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"time"

	"github.com/rm4n0s/errors"
)

// TagCertificateFailed: making the test certificate failed; the cause is
// kept.
const TagCertificateFailed = "ClientCertificateFailed"

// Certificate makes a self-signed RSA 2048 certificate, the kind mardp makes
// (SPEC §8.1), for 127.0.0.1, ::1 and localhost. It returns it for the
// server and a client TLS configuration that trusts it and nothing else.
func Certificate() (tls.Certificate, *tls.Config, error) {
	return certificate(big.NewInt(time.Now().UnixNano()))
}

func certificate(serial *big.Int) (tls.Certificate, *tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, nil, errors.NewErr(TagCertificateFailed, err).SetMetadata("part", "key")
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "rdptest"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, errors.NewErr(TagCertificateFailed, err).SetMetadata("part", "certificate")
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, nil, errors.NewErr(TagCertificateFailed, err).SetMetadata("part", "parse")
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	return cert, &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}, nil
}
