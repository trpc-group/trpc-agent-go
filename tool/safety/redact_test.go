//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	secrets := DefaultPolicy().secrets
	cases := []string{
		"deploy --token=AKIAIOSFODNN7EXAMPLE1",
		"export API_KEY=abcdef0123456789abcdef",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
	}
	for _, in := range cases {
		out, found := redactSecrets(in, secrets)
		if !found {
			t.Errorf("expected secret in %q", in)
		}
		if !strings.Contains(out, redactionMask) {
			t.Errorf("expected mask in %q -> %q", in, out)
		}
	}
	// Redaction is idempotent and leaves clean text unchanged.
	if out, found := redactSecrets("go test ./...", secrets); found || out != "go test ./..." {
		t.Errorf("clean text altered: %q found=%v", out, found)
	}
}

func TestMatchedSecretNames(t *testing.T) {
	secrets := DefaultPolicy().secrets
	names := matchedSecretNames("token=AKIAIOSFODNN7EXAMPLE1", secrets)
	if len(names) == 0 {
		t.Fatalf("expected at least one matched secret name")
	}
}
