//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redact

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStringRedactsCredentialShapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "openai style key",
			input: "key sk-proj-abcdefghijklmnopqrstuvwxyz012345",
			want:  "key [REDACTED:openai-api-key]",
		},
		{
			name:  "long bearer token",
			input: "Authorization: Bearer abcdefghijklmnopqrstuvwxyz.0123456789",
			want:  "Authorization: Bearer [REDACTED:bearer-token]",
		},
		{
			name:  "short bearer token",
			input: "Authorization: Bearer abc123",
			want:  "Authorization: Bearer [REDACTED:bearer-token]",
		},
		{
			name:  "password assignment",
			input: `DB_PASSWORD = "correct horse battery staple"`,
			want:  `DB_PASSWORD = "[REDACTED:password]"`,
		},
		{
			name:  "mixed case token assignment",
			input: `Access_Token: 'abcdefghijklmnopqrstuvwxyz'`,
			want:  `Access_Token: '[REDACTED:token]'`,
		},
		{
			name: "private key",
			input: "before\n-----BEGIN PRIVATE KEY-----\n" +
				"c3VwZXItc2VjcmV0LWtleS1tYXRlcmlhbA==\n" +
				"-----END PRIVATE KEY-----\nafter",
			want: "before\n[REDACTED:private-key]\nafter",
		},
		{
			name:  "postgres dsn",
			input: "postgres://reviewer:hunter2@db.example.test/reviews?sslmode=require",
			want:  "postgres://reviewer:[REDACTED:dsn]@db.example.test/reviews?sslmode=require",
		},
		{
			name:  "mysql dsn",
			input: "reviewer:hunter2@tcp(db.example.test:3306)/reviews",
			want:  "reviewer:[REDACTED:dsn]@tcp(db.example.test:3306)/reviews",
		},
		{
			name:  "credential url",
			input: "https://reviewer:hunter2@example.test/private",
			want:  "https://reviewer:[REDACTED:url-password]@example.test/private",
		},
		{
			name:  "multiple secrets",
			input: "token=abcdefghijklmnopqrstuvwxyz password=hunter2 Bearer zyxwvutsrqponmlkjihgfedcba",
			want:  "token=[REDACTED:token] password=[REDACTED:password] Bearer [REDACTED:bearer-token]",
		},
		{
			name:  "benign lookalikes",
			input: "sk-short Bearer illustration abc123 tokenize=value compassion=hunter2 https://example.test/path",
			want:  "sk-short Bearer illustration abc123 tokenize=value compassion=hunter2 https://example.test/path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, String(test.input))
		})
	}
}

func TestStringIsIdempotent(t *testing.T) {
	one := String(`token="sk-test-secret-value-0123456789"`)
	require.Equal(t, one, String(one))
	require.NotContains(t, one, "sk-test-secret-value-0123456789")
}

func TestStringBoundsInputAndOutput(t *testing.T) {
	secret := "sk-proj-" + strings.Repeat("s", 96)
	input := strings.Repeat("a", maxOutputBytes-len(" ")+1) + " " + secret

	got := String(input)
	require.LessOrEqual(t, len(got), maxOutputBytes)
	require.Contains(t, got, "[REDACTED:openai-api-key]")
	require.NotContains(t, got, secret)

	tooLarge := secret + strings.Repeat("x", maxInputBytes)
	require.Equal(t, "[REDACTED:input-too-large]", String(tooLarge))
}

func TestValueRedactsJSONCompatibleRecursiveValues(t *testing.T) {
	input := map[string]any{
		"message": "Bearer abcdefghijklmnopqrstuvwxyz",
		"nested": []any{
			`password="hunter2"`,
			map[string]any{"dsn": "redis://worker:secret@example.test/0"},
			float64(42),
			true,
			nil,
		},
	}

	got, err := Value(input)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"message": "Bearer [REDACTED:bearer-token]",
		"nested": []any{
			`password="[REDACTED:password]"`,
			map[string]any{"dsn": "redis://worker:[REDACTED:dsn]@example.test/0"},
			float64(42),
			true,
			nil,
		},
	}, got)
	require.Equal(t, `password="hunter2"`, input["nested"].([]any)[0], "Value must not mutate its input")
}

func TestValueRejectsUnsupportedValues(t *testing.T) {
	_, err := Value(make(chan int))
	require.EqualError(t, err, "redact value: unsupported type chan int")
}

func TestError(t *testing.T) {
	require.NoError(t, Error(nil))

	err := Error(errors.New("request failed: password=hunter2"))
	require.EqualError(t, err, "request failed: password=[REDACTED:password]")
}
