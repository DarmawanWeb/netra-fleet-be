package tlsutil

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"testing"
)

func TestSelfSigned(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, err := SelfSigned(dir, []string{"netra.site", "10.1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"localhost", "netra.site", "10.1.2.3", "127.0.0.1"} {
		if err := cert.VerifyHostname(h); err != nil {
			t.Errorf("%s: %v", h, err)
		}
	}
	if info, _ := os.Stat(keyFile); info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", info.Mode().Perm())
	}
	before, _ := os.ReadFile(certFile)
	if _, _, err := SelfSigned(dir, nil); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(certFile); string(after) != string(before) {
		t.Fatal("an existing certificate was replaced")
	}
}
