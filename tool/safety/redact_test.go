//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactorCredentialForms(t *testing.T) {
	privateKey := "-----BEGIN PRIVATE KEY-----\nvery-secret-private-key-material\n-----END PRIVATE KEY-----"
	tests := []struct {
		name   string
		secret string
		input  string
	}{
		{name: "api key assignment", secret: "generic-api-secret", input: "API_KEY=generic-api-secret"},
		{name: "token json", secret: "tiny-token", input: `{"token":"tiny-token"}`},
		{name: "password flag", secret: "swordfish", input: `run --password "swordfish"`},
		{name: "bearer", secret: "eyOpaqueBearerToken123456", input: "Authorization: Bearer eyOpaqueBearerToken123456"},
		{name: "jwt", secret: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature123456", input: "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature123456"},
		{name: "aws", secret: "AKIAIOSFODNN7EXAMPLE", input: "aws key AKIAIOSFODNN7EXAMPLE"},
		{name: "github", secret: "ghp_1234567890abcdefghijklmnop", input: "git token ghp_1234567890abcdefghijklmnop"},
		{name: "openai", secret: "sk-proj-1234567890abcdefghijklmnop", input: "openai sk-proj-1234567890abcdefghijklmnop"},
		{name: "private key", secret: privateKey, input: "key:\n" + privateKey},
	}

	redactor := NewRedactor()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			redacted, count := redactor.RedactString(test.input)
			require.Positive(t, count)
			require.NotContains(t, redacted, test.secret)
			require.Contains(t, redacted, RedactedValue)
		})
	}
}

func TestRedactorBytesDoesNotMutateInput(t *testing.T) {
	redactor := NewRedactor()
	input := []byte("password=top-secret-password")
	original := append([]byte(nil), input...)

	redacted, count := redactor.RedactBytes(input)

	require.Positive(t, count)
	require.Equal(t, original, input)
	require.NotContains(t, string(redacted), "top-secret-password")
}

func TestRedactorJSONValueIsRecursiveAndNonMutating(t *testing.T) {
	const (
		password = "short"
		token    = "nested-token"
		bearer   = "long-bearer-value-12345"
	)
	input := map[string]any{
		"password": password,
		"nested": []any{
			map[string]any{"access_token": token},
			"Bearer " + bearer,
		},
		"safe": "hello",
	}

	redacted, count := NewRedactor().RedactValue(input)
	encoded, err := json.Marshal(redacted)
	require.NoError(t, err)

	require.GreaterOrEqual(t, count, 3)
	require.NotContains(t, string(encoded), password)
	require.NotContains(t, string(encoded), token)
	require.NotContains(t, string(encoded), bearer)
	require.Equal(t, password, input["password"])
	nested := input["nested"].([]any)
	require.Equal(t, token, nested[0].(map[string]any)["access_token"])
}

func TestRedactorRawJSONAndInvalidJSON(t *testing.T) {
	redactor := NewRedactor()
	valid := json.RawMessage(`{"client_secret":"json-secret","safe":true}`)
	invalid := json.RawMessage(`password=invalid-json-secret`)

	cleanValid, validCount := redactor.RedactValue(valid)
	cleanInvalid, invalidCount := redactor.RedactValue(invalid)

	require.Positive(t, validCount)
	require.Positive(t, invalidCount)
	require.NotContains(t, string(cleanValid.(json.RawMessage)), "json-secret")
	require.NotContains(t, string(cleanInvalid.(json.RawMessage)), "invalid-json-secret")
	require.True(t, json.Valid(cleanValid.(json.RawMessage)))
}

func TestRedactorLeavesOrdinaryTextUntouched(t *testing.T) {
	input := "go test ./tool/safety -count=1"
	redacted, count := NewRedactor().RedactString(input)
	require.Zero(t, count)
	require.Equal(t, input, redacted)
	require.False(t, strings.Contains(redacted, RedactedValue))
}
