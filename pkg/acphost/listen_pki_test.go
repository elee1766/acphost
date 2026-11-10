package acphost

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeTestTLS writes a self-signed CA that is also the server certificate,
// enough for the files to load, and returns the spec block for it.
func writeTestTLS(t *testing.T, dir string) ListenTLS {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	crt := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	l := ListenTLS{Cert: filepath.Join(dir, "srv.crt"), Key: filepath.Join(dir, "srv.key"), ClientCA: filepath.Join(dir, "ca.crt")}
	require.NoError(t, os.WriteFile(l.Cert, crt, 0o600))
	require.NoError(t, os.WriteFile(l.Key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb}), 0o600))
	require.NoError(t, os.WriteFile(l.ClientCA, crt, 0o600))
	return l
}
