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

type jsonHiddenSecretOutput struct {
	Password string `json:"-"`
	Safe     string `json:"safe"`
}

type camelCaseHiddenSecretOutput struct {
	APIToken string `json:"-"`
	Safe     string `json:"safe"`
}

type customMarshalSecretOutput struct {
	Token string
	Safe  string
}

func (value customMarshalSecretOutput) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Safe string `json:"safe"`
	}{Safe: value.Safe})
}

type ordinaryHiddenOutput struct {
	Cache string `json:"-"`
	Safe  string `json:"safe"`
}

type overlappingHiddenOutput struct {
	Visible []string `json:"visible"`
	Hidden  []string `json:"-"`
}

func TestRedactorFailsClosedForJSONHiddenSecrets(t *testing.T) {
	shared := []string{"ordinary", "token=overlapping-slice-secret"}
	tests := []struct {
		name  string
		value any
	}{
		{
			name: "json ignored exported field",
			value: jsonHiddenSecretOutput{
				Password: "hidden-password-value",
				Safe:     "visible",
			},
		},
		{
			name: "camel case sensitive field",
			value: camelCaseHiddenSecretOutput{
				APIToken: "camel-case-hidden-token",
				Safe:     "visible",
			},
		},
		{
			name: "custom marshaler omitted field",
			value: customMarshalSecretOutput{
				Token: "custom-marshaler-token",
				Safe:  "visible",
			},
		},
		{
			name: "overlapping hidden slice",
			value: overlappingHiddenOutput{
				Visible: shared[:1],
				Hidden:  shared[:2],
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clean, count := NewRedactor().RedactValue(test.value)
			require.Positive(t, count)
			require.Equal(t, RedactedValue, clean)
		})
	}
}

func TestRedactorPreservesOrdinaryConcreteValue(t *testing.T) {
	input := ordinaryHiddenOutput{Cache: "compiled-state", Safe: "visible"}

	clean, count := NewRedactor().RedactValue(input)

	require.Zero(t, count)
	require.IsType(t, ordinaryHiddenOutput{}, clean)
	require.Equal(t, input, clean)
}

func TestRedactorHandlesTruncatedPEMAndShortAuthorization(t *testing.T) {
	tests := []struct {
		name   string
		secret string
		input  string
	}{
		{
			name:   "truncated private key",
			secret: "truncated-private-key-material",
			input: "prefix\n-----BEGIN PRIVATE KEY-----\n" +
				"truncated-private-key-material",
		},
		{
			name:   "short bearer authorization",
			secret: "abc",
			input:  "Authorization: Bearer abc",
		},
		{
			name:   "short basic authorization",
			secret: "eA==",
			input:  "authorization=Basic eA==",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clean, count := NewRedactor().RedactString(test.input)
			require.Positive(t, count)
			require.NotContains(t, clean, test.secret)
			require.Contains(t, clean, RedactedValue)
		})
	}
}

type panickingMarshalOutput struct {
	Safe string
}

func (panickingMarshalOutput) MarshalJSON() ([]byte, error) {
	panic("marshal failure")
}

func TestRedactorFailsClosedWhenCustomMarshalerPanics(t *testing.T) {
	clean, count := NewRedactor().RedactValue(
		panickingMarshalOutput{Safe: "ordinary"},
	)

	require.Positive(t, count)
	require.Equal(t, RedactedValue, clean)
}

type namedSecretMap map[string]string

func TestRedactorPreservesNamedMapAfterRedaction(t *testing.T) {
	input := namedSecretMap{
		"token": "named-map-token",
		"safe":  "visible",
	}

	clean, count := NewRedactor().RedactValue(input)

	require.Positive(t, count)
	require.IsType(t, namedSecretMap{}, clean)
	result := clean.(namedSecretMap)
	require.Equal(t, RedactedValue, result["token"])
	require.Equal(t, "named-map-token", input["token"])
}

type panickingUnmarshalOutput struct {
	Password string `json:"password"`
}

func (*panickingUnmarshalOutput) UnmarshalJSON([]byte) error {
	panic("unmarshal failure")
}

func TestRedactorFailsClosedWhenCustomUnmarshalerPanics(t *testing.T) {
	const secret = "unmarshal-secret"
	clean, count := NewRedactor().RedactValue(
		panickingUnmarshalOutput{Password: secret},
	)

	require.Positive(t, count)
	encoded, err := json.Marshal(clean)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), secret)
}
