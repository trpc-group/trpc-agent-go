//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeinterpreter

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// newMockSandbox creates a *Sandbox whose jupyterURL() points at the given
// httptest.Server, so tests can exercise RunCode / context handlers without a
// real e2b backend.
func newMockSandbox(t *testing.T, server *httptest.Server) *Sandbox {
	t.Helper()

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse mock server url: %v", err)
	}

	// Use the httptest server's own client (which trusts the test TLS root,
	// if any). We inject a RoundTripper that rewrites the host to the test
	// server's address regardless of what the SDK builds.
	client := server.Client()
	origTransport := client.Transport
	if origTransport == nil {
		origTransport = http.DefaultTransport
	}
	client.Transport = &rewriteTransport{
		base:       origTransport,
		targetHost: u.Host,
		scheme:     u.Scheme,
	}

	return &Sandbox{
		id:       "sbx-test",
		clientID: "client-test",
		template: DefaultTemplate,
		connection: &ConnectionConfig{
			APIKey:         "test-api-key",
			Domain:         "e2b.test",
			Debug:          true,
			RequestTimeout: 5 * time.Second,
			HTTPClient:     client,
		},
	}
}

type rewriteTransport struct {
	base       http.RoundTripper
	targetHost string
	scheme     string
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = r.scheme
	req.URL.Host = r.targetHost
	req.Host = r.targetHost
	return r.base.RoundTrip(req)
}

func ndjson(lines ...string) string {
	return strings.Join(lines, "\n")
}

// generateTestCAPEM returns a PEM-encoded self-signed certificate suitable
// for exercising code paths that load CAs from SSL_CERT_FILE.
func generateTestCAPEM(t *testing.T) []byte {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "e2b-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
