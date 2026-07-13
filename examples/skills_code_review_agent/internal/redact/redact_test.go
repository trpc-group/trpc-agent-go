//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import (
	"strings"
	"testing"
)

func TestRedactString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "openai key",
			in:   "apiKey := \"sk-abcdefghijklmnopqrstuvwxyz123456\"",
			want: "apiKey := \"<redacted>\"",
		},
		{
			name: "password assignment",
			in:   "password = \"s3cret-value\"",
			want: "<redacted>\"",
		},
		{
			name: "api key colon",
			in:   "api_key: my-secret-token-123",
			want: "<redacted>",
		},
		{
			name: "token assignment",
			in:   "token = bearer-token-abc",
			want: "<redacted>",
		},
		{
			name: "bearer header",
			in:   "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload",
			want: "Authorization: Bearer <redacted>",
		},
		{
			name: "secret env",
			in:   "secret=super-secret",
			want: "<redacted>",
		},
		{
			name: "normal code",
			in:   "return fmt.Errorf(\"invalid user id\")",
			want: "return fmt.Errorf(\"invalid user id\")",
		},
		{
			name: "normal variable",
			in:   "var timeout = 30 * time.Second",
			want: "var timeout = 30 * time.Second",
		},
		{
			name: "log message",
			in:   "log.Printf(\"user login failed\")",
			want: "log.Printf(\"user login failed\")",
		},
		{
			name: "mixed",
			in:   "cfg := Config{api_key: sk-abcdefghijklmnopqrstuvwxyz123456}",
			want: "cfg := Config{api_key: <redacted>}",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactString(tc.in)
			if got != tc.want {
				t.Fatalf("RedactString() = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, "sk-") || strings.Contains(got, "s3cret") ||
				strings.Contains(got, "super-secret") || strings.Contains(got, "bearer-token") {
				t.Fatalf("sensitive value leaked: %q", got)
			}
		})
	}
}
