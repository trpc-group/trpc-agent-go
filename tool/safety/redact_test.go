//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactString_CoversCommonSecretShapes(t *testing.T) {
	input := `api_key="secret value with spaces" token=abc123 Authorization: Bearer eyJhbGciOi.fake.payload AKIA1234567890ABCDEF sk-abcdefghijklmnop -----BEGIN PRIVATE KEY-----x-----END PRIVATE KEY-----`
	out, redacted := redactString(input)
	require.True(t, redacted)
	require.NotContains(t, out, "secret value with spaces")
	require.NotContains(t, out, "Bearer eyJ")
	require.NotContains(t, out, "AKIA1234567890ABCDEF")
	require.NotContains(t, out, "sk-abcdefghijklmnop")
	require.NotContains(t, out, "PRIVATE KEY")
}

func TestContainsSecret_CoversJSONSecretShapes(t *testing.T) {
	require.True(t, containsSecret(`{"token":"abc123"}`))
	require.True(t, containsSecret(`{"nested":{"password":"abc123"}}`))
	require.True(t, containsSecret(`{"items":[{"client_secret":"abc123"}]}`))
	require.False(t, containsSecret(`{"message":"plain"}`))
	require.False(t, containsSecret(`{"max_tokens":128}`))
	require.False(t, containsSecret(`{"token_count":42}`))
	require.False(t, containsSecret(`{"authorization_required":false}`))
}

func TestRedactString_RedactsURLUserinfo(t *testing.T) {
	input := `curl https://alice:s3cr3t@allowed.example/path`
	out, redacted := redactString(input)
	require.True(t, redacted)
	require.NotContains(t, out, "alice")
	require.NotContains(t, out, "s3cr3t")
	require.Contains(t, out, "https://allowed.example/path")
}

func TestRedactString_RedactsGenericURLUserinfo(t *testing.T) {
	input := `postgres://alice:s3cr3t@db.example/app redis://bob:redis-secret@cache.example mongodb://carol:mongo-secret@db.example/app`
	out, redacted := redactString(input)
	require.True(t, redacted)
	require.NotContains(t, out, "alice")
	require.NotContains(t, out, "s3cr3t")
	require.NotContains(t, out, "bob")
	require.NotContains(t, out, "redis-secret")
	require.NotContains(t, out, "carol")
	require.NotContains(t, out, "mongo-secret")
	require.Contains(t, out, "postgres://db.example/app")
	require.Contains(t, out, "redis://cache.example")
	require.Contains(t, out, "mongodb://db.example/app")
}

func TestRedactString_RedactsURLUsernameOnly(t *testing.T) {
	input := `curl https://ghp_value@allowed.example/path`
	out, redacted := redactString(input)
	require.True(t, redacted)
	require.NotContains(t, out, "ghp_value")
	require.Contains(t, out, "https://allowed.example/path")
}

func TestRedactString_RedactsSpaceSeparatedCredentialFlags(t *testing.T) {
	input := `cli --token abc123 --password "hunter 2"`
	out, redacted := redactString(input)
	require.True(t, redacted)
	require.Contains(t, out, "--token <redacted>")
	require.Contains(t, out, "--password <redacted>")
	require.NotContains(t, out, "abc123")
	require.NotContains(t, out, "hunter 2")
}

func TestRedactString_RedactsCurlCredentialFlags(t *testing.T) {
	for _, input := range []string{
		`curl -u alice:password https://allowed.example`,
		`curl -ualice:password https://allowed.example`,
		`curl -Uproxy:password https://allowed.example`,
		`curl -U proxy:password https://allowed.example`,
		`curl --user alice:password https://allowed.example`,
		`curl --user=alice:password https://allowed.example`,
		`curl --proxy-user proxy:password https://allowed.example`,
		`curl --oauth2-bearer bearer-token https://allowed.example`,
	} {
		out, redacted := redactString(input)
		require.True(t, redacted, input)
		require.NotContains(t, out, "alice:password", input)
		require.NotContains(t, out, "proxy:password", input)
		require.NotContains(t, out, "bearer-token", input)
	}
}

func TestRedactString_RedactsRemainingNetworkCredentialFlags(t *testing.T) {
	cases := []struct {
		input  string
		secret string
	}{
		{`curl --pass curl-passphrase https://allowed.example`, "curl-passphrase"},
		{`curl --pass=curl-passphrase https://allowed.example`, "curl-passphrase"},
		{`curl --proxy-pass proxy-passphrase https://allowed.example`, "proxy-passphrase"},
		{`curl --proxy-pass=proxy-passphrase https://allowed.example`, "proxy-passphrase"},
		{`wget --ftp-user ftp-user-value https://allowed.example`, "ftp-user-value"},
		{`wget --ftp-password=ftp-password-value https://allowed.example`, "ftp-password-value"},
		{`wget --http-user http-user-value https://allowed.example`, "http-user-value"},
		{`wget --http-password=http-password-value https://allowed.example`, "http-password-value"},
		{`wget --proxy-password proxy-password-value https://allowed.example`, "proxy-password-value"},
		{`wget --password=wget-password-value https://allowed.example`, "wget-password-value"},
	}
	for _, tc := range cases {
		out, redacted := redactString(tc.input)
		require.True(t, redacted, tc.input)
		require.NotContains(t, out, tc.secret, tc.input)
	}
}

func TestRedactString_RedactsNetworkCredentialsWhenShellParsingFails(t *testing.T) {
	input := `curl -u alice:s3cr3t https://allowed.example > out.txt`
	out, redacted := redactString(input)
	require.True(t, redacted)
	require.NotContains(t, out, "alice:s3cr3t")
	require.NotContains(t, out, "s3cr3t")
}

func TestRedactString_RedactsNetworkCredentialsAfterLeadingAssignments(t *testing.T) {
	input := `FOO=1 curl -u alice:s3cr3t https://allowed.example > out.txt`
	out, redacted := redactString(input)
	require.True(t, redacted)
	require.NotContains(t, out, "alice:s3cr3t")
	require.NotContains(t, out, "s3cr3t")
}

func TestRedactString_DoesNotTreatWgetUserAgentAsCredential(t *testing.T) {
	for _, input := range []string{
		`wget -U Mozilla https://allowed.example`,
		`wget -UMozilla https://allowed.example`,
	} {
		out, redacted := redactString(input)
		require.False(t, redacted, input)
		require.Equal(t, input, out)
	}
}

func TestRedactString_ScopesNetworkCredentialFlagsToCommandSegment(t *testing.T) {
	input := `python -u script.py | curl -u alice:password https://allowed.example && docker --user 1000 alpine`
	out, redacted := redactString(input)
	require.True(t, redacted)
	require.Contains(t, out, `python -u script.py`)
	require.Contains(t, out, `docker --user 1000 alpine`)
	require.NotContains(t, out, "alice:password")
}

func TestRedactString_DoesNotRedactAmbiguousFlagsOutsideNetworkClients(t *testing.T) {
	for _, input := range []string{
		`python -u script.py`,
		`docker run --user 1000 alpine`,
	} {
		out, redacted := redactString(input)
		require.False(t, redacted, input)
		require.Equal(t, input, out)
	}
}

func TestRedactString_NoSecretLeavesInput(t *testing.T) {
	out, redacted := redactString("plain output")
	require.False(t, redacted)
	require.Equal(t, "plain output", out)
}

func TestRedactEnv_RedactsSecretNamesAndValues(t *testing.T) {
	redacted, changed := redactEnv(map[string]string{
		"OPENAI_API_KEY": "plain",
		"HEADER":         "Authorization: Bearer abc.def.ghi",
		"SAFE":           "ok",
	})
	require.True(t, changed)
	require.Equal(t, "<redacted>", redacted["OPENAI_API_KEY"])
	require.Equal(t, "<redacted>", redacted["HEADER"])
	require.Equal(t, "ok", redacted["SAFE"])

	empty, changed := redactEnv(nil)
	require.False(t, changed)
	require.Nil(t, empty)
}

func TestLooksSecretName_CoversAliases(t *testing.T) {
	for _, name := range []string{
		"TOKEN",
		"db_password",
		"client_secret",
		"apiKey",
		"private_key",
		"authorization_header",
		"bearer_value",
		"aws_access_key_id",
	} {
		require.True(t, looksSecretName(name), name)
	}
	require.False(t, looksSecretName("PATH"))
}
