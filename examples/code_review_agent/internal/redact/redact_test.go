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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

type stringerValue string

func (value stringerValue) String() string { return string(value) }

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
			name:  "bearer tail",
			input: "Authorization: Bearer abc123,tail;still-secret",
			want:  "Authorization: Bearer [REDACTED:bearer-token]",
		},
		{
			name:  "basic authorization",
			input: "Authorization: Basic dXNlcjpwYXNzd29yZA==",
			want:  "Authorization: [REDACTED:authorization]",
		},
		{
			name:  "other authorization scheme",
			input: "Authorization: Digest username=reviewer,response=secret",
			want:  "Authorization: [REDACTED:authorization]",
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
			name:  "marker prefix cannot hide a secret",
			input: "password=[REDACTED:decoy]hunter2",
			want:  "password=[REDACTED:password]",
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
	tests := []string{
		`token="sk-test-secret-value-0123456789"`,
		"password=hunter2",
		"clientSecret: 'abcdefghijklmnopqrstuvwxyz'",
		"postgres://reviewer:hunter2@db.example.test/reviews",
		"reviewer:hunter2@tcp(db.example.test:3306)/reviews",
		"https://reviewer:hunter2@example.test/private",
		"Authorization: Bearer abc123",
		"Bearer abcdefghijklmnopqrstuvwxyz",
		"sk-proj-abcdefghijklmnopqrstuvwxyz012345",
		"-----BEGIN PRIVATE KEY-----\nsecret\n-----END PRIVATE KEY-----",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			one := String(input)
			require.Equal(t, one, String(one))
		})
	}
}

func TestStringUnquotedAssignmentsFailClosedAtPunctuation(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "comma", input: "password=alpha,beta"},
		{name: "semicolon", input: "token=alpha;beta"},
		{name: "closing brace", input: "clientSecret=alpha}beta"},
		{name: "closing bracket", input: "credentials=alpha]beta"},
		{name: "adjacent property", input: "apiKey=alpha,next=value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := String(test.input)
			require.Contains(t, got, "[REDACTED:")
			require.NotContains(t, got, "alpha")
			require.NotContains(t, got, "beta")
			require.NotContains(t, got, "next=value")
			require.Equal(t, got, String(got))
		})
	}
}

func TestStringRejectsForgedQuotedMarkersAndMultiwordSecrets(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "forged quoted marker suffix",
			input: `password="[REDACTED:password]"hunter2`,
			want:  `password=[REDACTED:password]`,
		},
		{
			name:  "forged quoted marker with word tail",
			input: `password="[REDACTED:password]"hunter2 more-secret`,
			want:  `password=[REDACTED:password]`,
		},
		{
			name:  "multiword value",
			input: "password=correct horse battery staple",
			want:  "password=[REDACTED:password]",
		},
		{
			name:  "assignment shaped tail remains secret",
			input: "password=correct horse user=reviewer",
			want:  "password=[REDACTED:password]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := String(test.input)
			require.Equal(t, test.want, got)
			require.Equal(t, got, String(got))
		})
	}
}

func TestStringQuotedMarkersFailClosedOnUnknownTails(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "whitespace tail",
			input: `password="[REDACTED:password]" hunter2`,
			want:  `password="[REDACTED:password]"`,
		},
		{
			name:  "comma tail",
			input: `password="[REDACTED:password]",hunter2`,
			want:  `password="[REDACTED:password]"`,
		},
		{
			name:  "json property boundary",
			input: `{"password":"[REDACTED:password]","safe":"visible"}`,
			want:  `{"password":"[REDACTED:password]","safe":"visible"}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := String(test.input)
			require.Equal(t, test.want, got)
			require.Equal(t, got, String(got))
		})
	}
}

func TestStringRedactsLogicalAssignmentContinuations(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "multiline double quote",
			input: "password=\"first line\nsecond secret\"\n" +
				"after=visible",
			want: "password=\"[REDACTED:password]\"\n" +
				"after=visible",
		},
		{
			name: "multiline single quote",
			input: "clientSecret='first line\nsecond secret'\n" +
				"after=visible",
			want: "clientSecret='[REDACTED:secret]'\n" +
				"after=visible",
		},
		{
			name: "backslash continuation",
			input: "apiKey=first line \\\nsecond secret \\\nthird secret\n" +
				"after=visible",
			want: "apiKey=[REDACTED:api-key]\n" +
				"after=visible",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := String(test.input)
			require.Equal(t, test.want, got)
			require.NotContains(t, got, "second secret")
			require.Equal(t, got, String(got))
		})
	}
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

func TestStringTruncatesAtUTF8Boundaries(t *testing.T) {
	input := strings.Repeat("界", maxOutputBytes) +
		" password=hunter2 " + strings.Repeat("🙂", maxOutputBytes)

	got := String(input)
	require.LessOrEqual(t, len(got), maxOutputBytes)
	require.True(t, utf8.ValidString(got))
	require.Contains(t, got, truncatedMarker)
	require.NotContains(t, got, "hunter2")
}

func TestStringRepairsInvalidUTF8(t *testing.T) {
	input := "before " + string([]byte{0xff, 0xfe}) + " password=hunter2"

	got := String(input)
	require.True(t, utf8.ValidString(got))
	require.NotContains(t, got, "hunter2")

	value, err := Value(map[string]string{input: input})
	require.NoError(t, err)
	for key, item := range value.(map[string]string) {
		require.True(t, utf8.ValidString(key))
		require.True(t, utf8.ValidString(item))
	}
}

func TestStringHandlesPrivateKeyPEMByMatchingLabel(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "matching label",
			input: "before\n-----BEGIN RSA PRIVATE KEY-----\nsecret\n" +
				"-----END RSA PRIVATE KEY-----\nafter",
			want: "before\n[REDACTED:private-key]\nafter",
		},
		{
			name:  "unterminated redacts to EOF",
			input: "before\n-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\nmore secret",
			want:  "before\n[REDACTED:private-key]",
		},
		{
			name: "different footer does not terminate",
			input: "before\n-----BEGIN RSA PRIVATE KEY-----\nsecret\n" +
				"-----END EC PRIVATE KEY-----\nstill secret\n" +
				"-----END RSA PRIVATE KEY-----\nafter",
			want: "before\n[REDACTED:private-key]\nafter",
		},
		{
			name: "mismatched footer redacts to EOF",
			input: "before\n-----BEGIN PRIVATE KEY-----\nsecret\n" +
				"-----END PUBLIC KEY-----\nafter",
			want: "before\n[REDACTED:private-key]",
		},
		{
			name: "indented CRLF block",
			input: "before\r\n  -----BEGIN ENCRYPTED PRIVATE KEY-----\r\nsecret\r\n" +
				"  -----END ENCRYPTED PRIVATE KEY-----\r\nafter",
			want: "before\r\n[REDACTED:private-key]\r\nafter",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, String(test.input))
		})
	}
}

func TestStringRedactsPrivateKeyBoundariesAfterLogPrefixes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "matching footer",
			input: "2026-07-29 INFO key=-----BEGIN PRIVATE KEY-----\nsecret\n" +
				"2026-07-29 INFO key=-----END PRIVATE KEY-----\nafter",
			want: "2026-07-29 INFO key=[REDACTED:private-key]\nafter",
		},
		{
			name:  "truncated block",
			input: "prefix cert=-----BEGIN RSA PRIVATE KEY-----\nsecret\ntail",
			want:  "prefix cert=[REDACTED:private-key]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, String(test.input))
		})
	}
}

func TestStringRejectsPEMFooterWithNonWhitespaceSuffix(t *testing.T) {
	input := "before\n-----BEGIN PRIVATE KEY-----\nsecret\n" +
		"-----END PRIVATE KEY-----garbage\nstill-secret\n" +
		"-----END PRIVATE KEY-----   \nafter"
	want := "before\n[REDACTED:private-key]   \nafter"

	require.Equal(t, want, String(input))

	truncated := "before\n-----BEGIN PRIVATE KEY-----\nsecret\n" +
		"-----END PRIVATE KEY-----garbage\nstill-secret"
	require.Equal(t, "before\n[REDACTED:private-key]", String(truncated))
}

func TestStringClassifiesIdentifierComponents(t *testing.T) {
	input := strings.Join([]string{
		"apiKey=one",
		"accessToken=two",
		"clientSecret=three",
		"privateKey=four",
		"private.key=four-dot",
		"credentials=five",
		"token_type=Bearer",
		"tokenCount=12",
		"password_policy=strict",
	}, " ")
	want := strings.Join([]string{
		"apiKey=[REDACTED:api-key]",
		"accessToken=[REDACTED:token]",
		"clientSecret=[REDACTED:secret]",
		"privateKey=[REDACTED:private-key]",
		"private.key=[REDACTED:private-key]",
		"credentials=[REDACTED:credential]",
		"token_type=Bearer",
		"tokenCount=12",
		"password_policy=strict",
	}, " ")

	require.Equal(t, want, String(input))
}

func TestStringPreservesMetadataIdentifiers(t *testing.T) {
	input := strings.Join([]string{
		"token_endpoint=https://issuer.example.test/token",
		"token_count_total=42",
		"tokenExpirySeconds=3600",
		"accessTokenEndpoint=https://issuer.example.test/token",
		"password_policy_version=v2",
		"api_key_count=3",
		"secret_count=4",
		"credential_type=oauth",
		"private_key_path=/run/secrets/key.pem",
		"token_value=must-redact",
		"password_hash=must-redact",
		"token_endpoint_password=must-redact",
	}, " ")
	want := strings.Join([]string{
		"token_endpoint=https://issuer.example.test/token",
		"token_count_total=42",
		"tokenExpirySeconds=3600",
		"accessTokenEndpoint=https://issuer.example.test/token",
		"password_policy_version=v2",
		"api_key_count=3",
		"secret_count=4",
		"credential_type=oauth",
		"private_key_path=/run/secrets/key.pem",
		"token_value=[REDACTED:token]",
		"password_hash=[REDACTED:password]",
		"token_endpoint_password=[REDACTED:password]",
	}, " ")

	require.Equal(t, want, String(input))

	value, err := Value(map[string]string{
		"token_endpoint":          "https://issuer.example.test/token",
		"token_count_total":       "42",
		"tokenExpirySeconds":      "3600",
		"accessTokenEndpoint":     "https://issuer.example.test/token",
		"password_policy_version": "v2",
		"api_key_count":           "3",
		"secret_count":            "4",
		"credential_type":         "oauth",
		"private_key_path":        "/run/secrets/key.pem",
		"token_value":             "must-redact",
		"token_endpoint_password": "must-redact",
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"token_endpoint":          "https://issuer.example.test/token",
		"token_count_total":       "42",
		"tokenExpirySeconds":      "3600",
		"accessTokenEndpoint":     "https://issuer.example.test/token",
		"password_policy_version": "v2",
		"api_key_count":           "3",
		"secret_count":            "4",
		"credential_type":         "oauth",
		"private_key_path":        "/run/secrets/key.pem",
		"token_value":             "[REDACTED:token]",
		"token_endpoint_password": "[REDACTED:password]",
	}, value)
}

func TestValueHandlesNoncanonicalSensitiveMapKeys(t *testing.T) {
	input := map[string]string{
		"password ":                     "secret-one",
		"password[]":                    "secret-two",
		"password:description":          "secret-colon",
		"password=description":          "secret-equals",
		"api_key!":                      "secret-three",
		"token_count[]":                 "7",
		"api_key_count!":                "3",
		"secret_count ":                 "4",
		"credential_type[]":             "oauth",
		"private_key_path!":             "/run/secrets/key.pem",
		"api_key_count_password[]":      "compound-secret",
		"private_key_path_clientSecret": "compound-secret-two",
	}

	value, err := Value(input)
	require.NoError(t, err)
	got := value.(map[string]string)
	require.Equal(t, "[REDACTED:password]", got["password "])
	require.Equal(t, "[REDACTED:password]", got["password[]"])
	require.Equal(t,
		"[REDACTED:password]",
		got["password:[REDACTED:password]"],
	)
	require.Equal(t,
		"[REDACTED:password]",
		got["password=[REDACTED:password]"],
	)
	require.Equal(t, "[REDACTED:api-key]", got["api_key!"])
	require.Equal(t, "7", got["token_count[]"])
	require.Equal(t, "3", got["api_key_count!"])
	require.Equal(t, "4", got["secret_count "])
	require.Equal(t, "oauth", got["credential_type[]"])
	require.Equal(t, "/run/secrets/key.pem", got["private_key_path!"])
	require.Equal(t, "[REDACTED:password]", got["api_key_count_password[]"])
	require.Equal(t, "[REDACTED:secret]", got["private_key_path_clientSecret"])
	require.Equal(t, "secret-one", input["password "])
}

func TestStringRedactsQuotedProperties(t *testing.T) {
	input := `{"apiKey":"secret","password_policy":"strict"}`
	want := `{"apiKey":"[REDACTED:api-key]","password_policy":"strict"}`
	require.Equal(t, want, String(input))
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

func TestValueUsesSensitiveMapKeySemantics(t *testing.T) {
	input := map[string]any{
		"Password":     "hunter2",
		"apiKey":       "short-key",
		"accessToken":  12345,
		"clientSecret": true,
		"privateKey":   []string{"secret-key-material"},
		"credentials": map[string]any{
			"username": "reviewer",
			"opaque":   "secret",
		},
		"nested": map[string]any{
			"AcCeSsToKeN": "nested-secret",
		},
		"token_type":      "Bearer",
		"token_count":     2,
		"password_policy": "strict",
	}

	got, err := Value(input)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"Password":        "[REDACTED:password]",
		"apiKey":          "[REDACTED:api-key]",
		"accessToken":     "[REDACTED:token]",
		"clientSecret":    "[REDACTED:secret]",
		"privateKey":      "[REDACTED:private-key]",
		"credentials":     "[REDACTED:credential]",
		"nested":          map[string]any{"AcCeSsToKeN": "[REDACTED:token]"},
		"token_type":      "Bearer",
		"token_count":     2,
		"password_policy": "strict",
	}, got)
	require.Equal(t, "hunter2", input["Password"])
	require.Equal(t, []string{"secret-key-material"}, input["privateKey"])
}

func TestValueSupportsTypedMapsAndNamedStringKeys(t *testing.T) {
	type fieldName string
	type fieldValue string

	input := map[fieldName]fieldValue{
		"APIKey":      "typed-secret",
		"tokenCount":  "7",
		"displayName": "reviewer",
	}

	got, err := Value(input)
	require.NoError(t, err)
	require.Equal(t, map[fieldName]fieldValue{
		"APIKey":      "[REDACTED:api-key]",
		"tokenCount":  "7",
		"displayName": "reviewer",
	}, got)

	numbers, err := Value(map[fieldName]int{
		"password":   12345,
		"tokenCount": 7,
	})
	require.NoError(t, err)
	require.Equal(t, map[fieldName]int{
		"password":   0,
		"tokenCount": 7,
	}, numbers)
}

func TestValueKeepsSensitiveJSONNumbersMarshalable(t *testing.T) {
	input := map[string]json.Number{
		"accessToken": json.Number("123456789"),
		"token_count": json.Number("7"),
	}

	value, err := Value(input)
	require.NoError(t, err)
	got := value.(map[string]json.Number)
	require.Equal(t, json.Number("0"), got["accessToken"])
	require.Equal(t, json.Number("7"), got["token_count"])
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{"accessToken":0,"token_count":7}`, string(encoded))
	require.Equal(t, json.Number("123456789"), input["accessToken"])
}

func TestValueSanitizesBytePayloads(t *testing.T) {
	raw := json.RawMessage(`{"password":"raw-secret"}`)
	bytesValue := []byte("password=byte-secret")
	input := map[string]any{
		"raw":   raw,
		"bytes": bytesValue,
	}

	value, err := Value(input)
	require.NoError(t, err)
	got := value.(map[string]any)
	require.Nil(t, got["raw"].(json.RawMessage))
	require.Nil(t, got["bytes"].([]byte))
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "raw-secret")
	require.NotContains(t, string(encoded), "byte-secret")
	require.JSONEq(t, `{"bytes":null,"raw":null}`, string(encoded))
	require.Equal(t, json.RawMessage(`{"password":"raw-secret"}`), raw)
	require.Equal(t, []byte("password=byte-secret"), bytesValue)
}

func TestValueSanitizesFixedByteArrays(t *testing.T) {
	input := [16]byte{'s', 'e', 'c', 'r', 'e', 't'}

	value, err := Value(input)
	require.NoError(t, err)
	require.Equal(t, [16]byte{}, value)
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "115,101,99,114,101,116")
	require.Equal(t, byte('s'), input[0])
}

func TestValueSanitizesInvalidJSONNumbers(t *testing.T) {
	secret := json.Number("invalid-secret-number")

	value, err := Value(secret)
	require.NoError(t, err)
	require.Equal(t, json.Number("0"), value)
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	require.Equal(t, "0", string(encoded))

	nested, err := Value(map[string]json.Number{"token_count": secret})
	require.NoError(t, err)
	require.Equal(t, json.Number("0"), nested.(map[string]json.Number)["token_count"])
	require.Equal(t, json.Number("invalid-secret-number"), secret)
}

func TestValueReplacesSensitiveTypedContainers(t *testing.T) {
	input := map[string]map[string]string{
		"credentials": {"username": "reviewer", "opaque": "secret"},
		"metadata":    {"region": "test"},
	}

	got, err := Value(input)
	require.NoError(t, err)
	require.Equal(t, map[string]map[string]string{
		"credentials": nil,
		"metadata":    {"region": "test"},
	}, got)
	require.Equal(t, "secret", input["credentials"]["opaque"])
}

func TestValueFailsClosedForTypedInterfaces(t *testing.T) {
	input := map[string]fmt.Stringer{
		"password": stringerValue("typed-secret"),
	}

	got, err := Value(input)
	require.NoError(t, err)
	require.Equal(t, map[string]fmt.Stringer{"password": nil}, got)
	require.Equal(t, "typed-secret", input["password"].String())
}

func TestValueCopiesPointersWithoutMutatingInput(t *testing.T) {
	message := "Bearer abcdefghijklmnopqrstuvwxyz"
	input := map[string]*string{"message": &message}

	value, err := Value(input)
	require.NoError(t, err)
	got := value.(map[string]*string)
	require.NotSame(t, input["message"], got["message"])
	require.Equal(t, "Bearer [REDACTED:bearer-token]", *got["message"])
	require.Equal(t, "Bearer abcdefghijklmnopqrstuvwxyz", message)
}

func TestValueSanitizesMapKeysAndPreservesCollisions(t *testing.T) {
	input := map[string]int{
		"password=alpha": 1,
		"password=beta":  2,
		"ordinary":       3,
	}

	got, err := Value(input)
	require.NoError(t, err)
	require.Equal(t, map[string]int{
		"password=[REDACTED:password]":               0,
		"password=[REDACTED:password] [COLLISION:2]": 0,
		"ordinary": 3,
	}, got)
	require.Equal(t, map[string]int{
		"password=alpha": 1,
		"password=beta":  2,
		"ordinary":       3,
	}, input)
}

func TestValueMapKeyCollisionWithExistingSuffixIsDeterministic(t *testing.T) {
	input := map[string]int{
		"password=[REDACTED:password] [COLLISION:2]": 1,
		"password=alpha": 2,
		"password=beta":  3,
	}

	first, err := Value(input)
	require.NoError(t, err)
	second, err := Value(input)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first.(map[string]int), len(input))
}

func TestValuePreservesAliasesWithoutMutatingInput(t *testing.T) {
	shared := map[string]any{"password": "hunter2"}
	input := map[string]any{"left": shared, "right": shared}

	got, err := Value(input)
	require.NoError(t, err)
	out := got.(map[string]any)
	left := out["left"].(map[string]any)
	right := out["right"].(map[string]any)
	require.Equal(t, "[REDACTED:password]", left["password"])
	left["alias-check"] = true
	require.Equal(t, true, right["alias-check"])
	require.Equal(t, "hunter2", shared["password"])
	require.NotContains(t, shared, "alias-check")
}

func TestValueRejectsCyclesAndLimitsWork(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		input := map[string]any{}
		input["self"] = input

		_, err := Value(input)
		require.EqualError(t, err, "redact value: cycle detected")
	})

	t.Run("slice cycle", func(t *testing.T) {
		input := make([]any, 1)
		input[0] = input

		_, err := Value(input)
		require.EqualError(t, err, "redact value: cycle detected")
	})

	t.Run("depth", func(t *testing.T) {
		var input any = "leaf"
		for range maxValueDepth + 1 {
			input = []any{input}
		}

		_, err := Value(input)
		require.ErrorContains(t, err, "maximum depth exceeded")
	})

	t.Run("items", func(t *testing.T) {
		input := make([]string, maxValueItems)

		_, err := Value(input)
		require.EqualError(t, err, "redact value: maximum item count exceeded")
	})

	t.Run("aliases", func(t *testing.T) {
		shared := map[string]string{"safe": "value"}
		input := make([]any, maxValueAliases+2)
		for i := range input {
			input[i] = shared
		}

		_, err := Value(input)
		require.EqualError(t, err, "redact value: maximum alias count exceeded")
	})

	t.Run("distinct empty slices are not aliases", func(t *testing.T) {
		input := make([][]string, maxValueAliases+2)
		for i := range input {
			input[i] = []string{}
		}

		got, err := Value(input)
		require.NoError(t, err)
		require.Len(t, got.([][]string), len(input))
	})
}

func TestValueChecksItemBudgetBeforeContainerWork(t *testing.T) {
	stateForTest := func() valueState {
		return valueState{
			active: make(map[valueVisit]bool),
			memo:   make(map[valueVisit]reflect.Value),
		}
	}
	tests := []struct {
		name  string
		value any
	}{
		{name: "map", value: map[string]string{"one": "1", "two": "2"}},
		{name: "slice", value: []string{"one", "two"}},
		{name: "array", value: [2]string{"one", "two"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			items := maxValueItems - 2
			state := stateForTest()
			_, err := redactValue(reflect.ValueOf(test.value), 0, &items, &state)
			require.EqualError(t, err, "maximum item count exceeded")
			require.Equal(t, maxValueItems-1, items)
		})
	}
}

func TestValueReusesExactAliasesBeforeContainerBudget(t *testing.T) {
	sharedMap := map[string]string{"one": "1", "two": "2"}
	state := valueState{
		active: make(map[valueVisit]bool),
		memo:   make(map[valueVisit]reflect.Value),
	}
	items := maxValueItems - 4
	first, err := redactValue(reflect.ValueOf(sharedMap), 0, &items, &state)
	require.NoError(t, err)
	require.Equal(t, maxValueItems-1, items)

	second, err := redactValue(reflect.ValueOf(sharedMap), 0, &items, &state)
	require.NoError(t, err)
	require.Equal(t, maxValueItems, items)
	require.Equal(t, first.Pointer(), second.Pointer())
}

func TestValueEnforcesAggregateByteBudgets(t *testing.T) {
	t.Run("processed bytes", func(t *testing.T) {
		chunk := "repeated-large-string"
		state := valueState{
			processedBytes: maxValueProcessedBytes - 2*len(chunk),
			active:         make(map[valueVisit]bool),
			memo:           make(map[valueVisit]reflect.Value),
		}
		items := 0
		_, err := redactValue(
			reflect.ValueOf([]string{chunk, chunk, chunk}),
			0,
			&items,
			&state,
		)
		require.EqualError(t, err, "maximum processed byte count exceeded")
	})

	t.Run("output bytes", func(t *testing.T) {
		piece := "safe"
		state := valueState{
			outputBytes: maxValueOutputBytes -
				maxRedactedStringBytes(len(piece)) - len(piece),
			active: make(map[valueVisit]bool),
			memo:   make(map[valueVisit]reflect.Value),
		}
		items := 0
		_, err := redactValue(
			reflect.ValueOf([]string{piece, piece, piece}),
			0,
			&items,
			&state,
		)
		require.EqualError(t, err, "maximum output byte count exceeded")
	})

	t.Run("container aliases reuse byte work", func(t *testing.T) {
		payload := strings.Repeat("a", 1024)
		shared := map[string]any{"payload": payload}
		input := make([]any, 8)
		for i := range input {
			input[i] = shared
		}

		state := valueState{
			processedBytes: maxValueProcessedBytes - len("payload") - len(payload),
			active:         make(map[valueVisit]bool),
			memo:           make(map[valueVisit]reflect.Value),
		}
		items := 0
		redacted, err := redactValue(
			reflect.ValueOf(input),
			0,
			&items,
			&state,
		)
		require.NoError(t, err)
		got := redacted.Interface().([]any)
		first := got[0].(map[string]any)
		require.Equal(t, payload, first["payload"])
		for _, item := range got[1:] {
			require.Equal(t,
				reflect.ValueOf(first).Pointer(),
				reflect.ValueOf(item.(map[string]any)).Pointer(),
			)
		}
		require.Equal(t, payload, shared["payload"])
	})
}

func TestValueCountsSensitiveMapFields(t *testing.T) {
	input := make(map[string]string, maxValueItems)
	for i := range maxValueItems {
		input[fmt.Sprintf("password_%05d", i)] = "secret"
	}

	_, err := Value(input)
	require.EqualError(t, err, "redact value: maximum item count exceeded")

	items := 0
	state := valueState{
		active: make(map[valueVisit]bool),
		memo:   make(map[valueVisit]reflect.Value),
	}
	_, err = redactValue(
		reflect.ValueOf(map[string]string{
			"password": "secret",
			"safe":     "value",
		}),
		0,
		&items,
		&state,
	)
	require.NoError(t, err)
	require.Equal(t, 3, items, "root and both fields must count")
}

func TestValueRejectsUnsupportedValues(t *testing.T) {
	_, err := Value(make(chan int))
	require.EqualError(t, err, "redact value: unsupported type chan int")
}

func TestError(t *testing.T) {
	require.NoError(t, Error(nil))

	raw := errors.New("request failed: password=hunter2")
	err := Error(fmt.Errorf("review: %w", raw))
	require.EqualError(t, err, "review: request failed: password=[REDACTED:password]")
	require.Nil(t, errors.Unwrap(err))
	require.False(t, errors.Is(err, raw))
	var target interface{ Unwrap() error }
	require.False(t, errors.As(err, &target))
}
