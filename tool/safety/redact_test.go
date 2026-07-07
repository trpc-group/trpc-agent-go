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
