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
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func parseSinglePEMCert(t *testing.T, pemBytes []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("test PEM did not contain a CERTIFICATE block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert
}

func TestBuildTLSConfigFromEnv_NoEnv(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", "")

	cfg := buildTLSConfigFromEnv()
	if cfg != nil {
		t.Fatalf("expected nil tls.Config when no env vars are set, got %+v", cfg)
	}
}

func TestBuildTLSConfigFromEnv_OnlyCertFile(t *testing.T) {
	pemBytes := generateTestCAPEM(t)
	cert := parseSinglePEMCert(t, pemBytes)

	dir := t.TempDir()
	path := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}

	t.Setenv("SSL_CERT_FILE", path)
	t.Setenv("SSL_CERT_DIR", "")

	cfg := buildTLSConfigFromEnv()
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config when SSL_CERT_FILE is set")
	}
	if cfg.RootCAs == nil {
		t.Fatal("expected non-nil RootCAs")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion: got %d, want TLS1.2 (%d)", cfg.MinVersion, tls.VersionTLS12)
	}
	if !poolTrusts(cfg.RootCAs, cert) {
		t.Error("RootCAs does not trust the SSL_CERT_FILE certificate")
	}
}

func TestBuildTLSConfigFromEnv_OnlyCertDir(t *testing.T) {
	pemBytes := generateTestCAPEM(t)
	cert := parseSinglePEMCert(t, pemBytes)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "trusted.crt"), pemBytes, 0o600); err != nil {
		t.Fatalf("write crt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("skip me"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subdir", "ignored.pem"), pemBytes, 0o600); err != nil {
		t.Fatalf("write nested cert: %v", err)
	}

	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", dir)

	cfg := buildTLSConfigFromEnv()
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatal("expected non-nil tls.Config with RootCAs when SSL_CERT_DIR is set")
	}
	if !poolTrusts(cfg.RootCAs, cert) {
		t.Error("RootCAs does not trust the SSL_CERT_DIR certificate")
	}
}

func TestBuildTLSConfigFromEnv_BothCertFileAndDir(t *testing.T) {
	pemA := generateTestCAPEM(t)
	pemB := generateTestCAPEM(t)
	certA := parseSinglePEMCert(t, pemA)
	certB := parseSinglePEMCert(t, pemB)

	fileDir := t.TempDir()
	filePath := filepath.Join(fileDir, "a.pem")
	if err := os.WriteFile(filePath, pemA, 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}

	certDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(certDir, "b.pem"), pemB, 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}

	t.Setenv("SSL_CERT_FILE", filePath)
	t.Setenv("SSL_CERT_DIR", certDir)

	cfg := buildTLSConfigFromEnv()
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatal("expected non-nil tls.Config with RootCAs")
	}
	if !poolTrusts(cfg.RootCAs, certA) {
		t.Error("RootCAs missing SSL_CERT_FILE certificate")
	}
	if !poolTrusts(cfg.RootCAs, certB) {
		t.Error("RootCAs missing SSL_CERT_DIR certificate")
	}
}

func TestBuildTLSConfigFromEnv_MissingCertFile(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", filepath.Join(t.TempDir(), "does-not-exist.pem"))
	t.Setenv("SSL_CERT_DIR", "")

	cfg := buildTLSConfigFromEnv()
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config even when SSL_CERT_FILE is missing")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion: got %d, want TLS1.2", cfg.MinVersion)
	}
	if cfg.RootCAs == nil {
		t.Error("RootCAs should fall back to the system pool, not be nil")
	}
}

func TestBuildTLSConfigFromEnv_MissingCertDir(t *testing.T) {
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", filepath.Join(t.TempDir(), "no-such-dir"))

	cfg := buildTLSConfigFromEnv()
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config even when SSL_CERT_DIR is missing")
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion: got %d, want TLS1.2", cfg.MinVersion)
	}
}

func TestBuildTLSConfigFromEnv_InvalidPEMContent(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "not-a-cert.pem")
	if err := os.WriteFile(bad, []byte("this is not a certificate\n"), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	t.Setenv("SSL_CERT_FILE", bad)
	t.Setenv("SSL_CERT_DIR", "")

	cfg := buildTLSConfigFromEnv()
	if cfg == nil {
		t.Fatal("expected non-nil tls.Config when only invalid PEM is provided")
	}
	if cfg.RootCAs == nil {
		t.Error("RootCAs should still be non-nil (system pool fallback)")
	}
}

func TestBuildTLSConfigFromEnv_IgnoresUnknownExtensions(t *testing.T) {
	pemBytes := generateTestCAPEM(t)
	cert := parseSinglePEMCert(t, pemBytes)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "trust.txt"), pemBytes, 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("SSL_CERT_DIR", dir)

	cfg := buildTLSConfigFromEnv()
	if cfg == nil || cfg.RootCAs == nil {
		t.Fatal("expected non-nil tls.Config")
	}
	if poolTrusts(cfg.RootCAs, cert) {
		t.Error("RootCAs unexpectedly trusts a cert from a non-.pem/.crt file")
	}
}

func poolTrusts(pool *x509.CertPool, cert *x509.Certificate) bool {
	_, err := cert.Verify(x509.VerifyOptions{
		Roots:       pool,
		CurrentTime: cert.NotBefore.Add(1),
	})
	return err == nil
}
