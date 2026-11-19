package config

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCertificate(t *testing.T) {
	rec := &recorder{}
	f := folderWith(t, "")
	f.log = slog.New(rec)
	c, created, err := f.Certificate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("the first start made no certificate")
	}
	for _, name := range []string{certFile, keyFile} {
		if m := mode(t, filepath.Join(f.Path, name)); m != FileMode {
			t.Errorf("%s mode %v", name, m)
		}
	}
	x, err := x509.ParseCertificate(c.TLS.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	key, ok := x.PublicKey.(*rsa.PublicKey)
	if !ok || key.N.BitLen() != 2048 {
		t.Errorf("key %T of %d bits, want RSA 2048", x.PublicKey, key.N.BitLen())
	}
	if x.NotAfter.Before(time.Now().Add(19*365*24*time.Hour)) || x.NotBefore.After(time.Now()) {
		t.Errorf("valid from %v to %v", x.NotBefore, x.NotAfter)
	}
	if err := x.CheckSignature(x.SignatureAlgorithm, x.RawTBSCertificate, x.Signature); err != nil || x.Issuer.String() != x.Subject.String() {
		t.Errorf("not self-signed: %v", err)
	}
	sum := sha256.Sum256(c.TLS.Certificate[0])
	if strings.ReplaceAll(c.Fingerprint, ":", "") != hex.EncodeToString(sum[:]) || len(c.Fingerprint) != 95 {
		t.Errorf("fingerprint %s", c.Fingerprint)
	}
	if !strings.Contains(rec.all(), c.Fingerprint) || strings.Contains(rec.all(), "PRIVATE") {
		t.Error("the log has not the fingerprint, or has the key")
	}

	// The next start reads the same one.
	again, created, err := f.Certificate(context.Background())
	if err != nil || created || again.Fingerprint != c.Fingerprint {
		t.Errorf("second start: %s, created %v, %v", again.Fingerprint, created, err)
	}

	// A new certificate replaces it.
	fresh, err := f.NewCertificate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := f.Certificate(context.Background())
	if err != nil || fresh.Fingerprint == c.Fingerprint || loaded.Fingerprint != fresh.Fingerprint {
		t.Errorf("new certificate %s, loaded %s, old %s, %v", fresh.Fingerprint, loaded.Fingerprint, c.Fingerprint, err)
	}

	// It works for TLS 1.2 and newer.
	srv, cli := net.Pipe()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Error(err)
		}
		if err := cli.Close(); err != nil {
			t.Error(err)
		}
	})
	pool := x509.NewCertPool()
	pool.AddCert(x)
	go func() {
		s := tls.Server(srv, &tls.Config{Certificates: []tls.Certificate{c.TLS}, MinVersion: tls.VersionTLS12})
		_ = s.Handshake()
	}()
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if sha256.Sum256(raw[0]) != sum {
				t.Error("another certificate in the handshake")
			}
			return nil
		}}
	if err := tls.Client(cli, cfg).Handshake(); err != nil {
		t.Fatal(err)
	}
}

func TestCertificateInvalid(t *testing.T) {
	good := folderWith(t, "")
	if _, err := good.NewCertificate(context.Background()); err != nil {
		t.Fatal(err)
	}
	certPEM, err := os.ReadFile(filepath.Join(good.Path, certFile))
	if err != nil {
		t.Fatal(err)
	}
	other := folderWith(t, "")
	if _, err := other.NewCertificate(context.Background()); err != nil {
		t.Fatal(err)
	}
	otherKey, err := os.ReadFile(filepath.Join(other.Path, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		files map[string]string
		route string
	}{
		{"key missing", map[string]string{certFile: string(certPEM)}, "Folder.Certificate." + TagCertificateInvalid},
		{"cert missing", map[string]string{keyFile: string(otherKey)}, "Folder.Certificate." + TagCertificateInvalid},
		{"garbage", map[string]string{certFile: "x", keyFile: "y"}, "Folder.Certificate." + TagCertificateInvalid},
		{"another key", map[string]string{certFile: string(certPEM), keyFile: string(otherKey)}, "Folder.Certificate." + TagCertificateInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := folderWith(t, "")
			for name, data := range tt.files {
				if err := os.WriteFile(filepath.Join(f.Path, name), []byte(data), FileMode); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := f.Certificate(context.Background())
			assertRoute(t, err, tt.route)
			// It is left as it was: only the user replaces it.
			for name, data := range tt.files {
				got, rerr := os.ReadFile(filepath.Join(f.Path, name))
				if rerr != nil || string(got) != data {
					t.Errorf("%s was changed", name)
				}
			}
		})
	}

	unreadable := folderWith(t, "")
	if err := os.Mkdir(filepath.Join(unreadable.Path, certFile), 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, err = unreadable.Certificate(context.Background())
	assertRoute(t, err, "Folder.Certificate->Folder.readFile."+TagReadFailed)
}

func TestNewCertificateFails(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := folderWith(t, "").NewCertificate(canceled)
	assertRoute(t, err, "Folder.newCertificate."+TagCertificateFailed)

	gone := folderWith(t, "")
	if err := os.Remove(gone.Path); err != nil {
		t.Fatal(err)
	}
	_, err = gone.NewCertificate(context.Background())
	assertRoute(t, err, "Folder.newCertificate->Folder.write."+TagWriteFailed)
}
