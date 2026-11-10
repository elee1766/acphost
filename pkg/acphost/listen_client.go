package acphost

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// ClientTLSFiles names the files a TCP client presents and trusts. They
// are read again on every dial, so a client certificate that cert-manager
// or SPIRE replaced is used on the next connection.
type ClientTLSFiles struct {
	Cert string // client certificate (PEM)
	Key  string // client key (PEM)
	CA   string // CA bundle the host's server certificate must chain to (PEM)
	// ServerName is the name checked against the server certificate. Empty
	// uses the host part of the address given to ConnectTCP.
	ServerName string
}

// WithClientTLSFiles makes ConnectTCP use mutual TLS with the files in f,
// read on each dial. Connect ignores it.
func WithClientTLSFiles(f ClientTLSFiles) ConnectOption {
	return func(o *connectOptions) { o.tlsFiles = &f }
}

// WithClientTLS makes ConnectTCP use cfg, for a caller with its own
// certificate source (tls.Config.GetClientCertificate, for one).
// Connect ignores it. It wins over WithClientTLSFiles.
func WithClientTLS(cfg *tls.Config) ConnectOption {
	return func(o *connectOptions) { o.tlsConfig = cfg }
}

// clientTLS returns the config for a dial, or nil for plaintext.
func (o connectOptions) clientTLS() (*tls.Config, error) {
	if o.tlsConfig != nil {
		return o.tlsConfig.Clone(), nil
	}
	f := o.tlsFiles
	if f == nil {
		return nil, nil
	}
	cert, err := tls.LoadX509KeyPair(f.Cert, f.Key)
	if err != nil {
		return nil, fmt.Errorf("client certificate: %w", err)
	}
	pem, err := os.ReadFile(f.CA)
	if err != nil {
		return nil, fmt.Errorf("client tls ca: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("client tls ca " + f.CA + ": no PEM certificate")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   f.ServerName,
	}, nil
}
