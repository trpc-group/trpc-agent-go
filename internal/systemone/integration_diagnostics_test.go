//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package systemone_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntegrationErrorBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		key  string
		want string
	}{
		{
			name: "validation details without a key",
			body: `{"detail":[{"loc":["body","model"],"type":"missing","msg":"Field required"}]}`,
			want: `{"detail":[{"loc":["body","model"],"type":"missing","msg":"Field required"}]}`,
		},
		{
			name: "credential in non-json body",
			body: `<html>Bearer secret-key; secret-key</html>`,
			key:  "secret-key",
			want: `<html>Bearer [REDACTED]; [REDACTED]</html>`,
		},
		{
			name: "json-escaped credential",
			body: `{"detail":"bad key secret-\"key\""}`,
			key:  `secret-"key"`,
			want: `{"detail":"bad key [REDACTED]"}`,
		},
		{
			name: "bounded output",
			body: strings.Repeat("x", 5000),
			want: strings.Repeat("x", 4096) + "... (truncated)",
		},
		{
			name: "redaction across log boundary",
			body: strings.Repeat("x", 4090) + "secret-key" + strings.Repeat("y", 20),
			key:  "secret-key",
			want: strings.Repeat("x", 4090) + "[REDAC... (truncated)",
		},
		{name: "empty body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.body)
			original := bytes.Clone(body)
			require.Equal(t, tc.want, integrationErrorBody(body, tc.key))
			require.Equal(t, original, body, "diagnostics must not modify HTTPError.Body")
		})
	}
}
