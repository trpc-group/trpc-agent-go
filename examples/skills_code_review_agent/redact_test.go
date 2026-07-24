//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactSecrets(t *testing.T) {
	inputs := []string{
		`api_key = "sk-live-1234567890abcdef"`,
		`Authorization: Bearer abcdefghijklmnopqrstuvwxyz`,
		`password=hunter2`,
		`token: ghp_abcdefghijklmnopqrstuvwxyz123456`,
		`postgres://admin:supersecret@db.local/app`,
		`AKIAIOSFODNN7EXAMPLE`,
	}
	for _, input := range inputs {
		got := Redact(input)
		require.NotContains(t, got, secretValue(input), input)
		require.Contains(t, got, redactedValue, input)
	}
}

func TestContainsSecret(t *testing.T) {
	require.True(t, ContainsSecret(`clientSecret := "very-secret-value"`))
	require.False(t, ContainsSecret(`password := os.Getenv("PASSWORD")`))
	require.False(t, ContainsSecret(`token := "[REDACTED]"`))
}

func TestRedactionCoverage(t *testing.T) {
	secrets := []string{
		`dbPassword = "database-password"`,
		`service_token: abcdefghijklmnop`,
		`authorization = "Basic dXNlcjpwYXNz"`,
		`AKIAIOSFODNN7EXAMPLE`,
		`ASIAIOSFODNN7EXAMPLE`,
		`ghp_abcdefghijklmnopqrstuvwxyz123456`,
		`githubToken = "gho_abcdefghijklmnopqrstuvwxyz123456"`,
		`stripeKey = "sk_live_1234567890abcdef"`,
		`openaiAPIKey = "sk-proj-1234567890abcdef"`,
		`googleKey = "AIzaSyDUMMYDUMMYDUMMYDUMMYDUMMYDUMMY"`,
		`slackToken = "xox` + `b-1234567890-abcdefghijklmnop"`,
		`gitlabToken = "glpat-abcdefghijklmnopqrst"`,
		`npmToken = "npm_abcdefghijklmnopqrstuvwxyz"`,
		`Authorization: Bearer abc.def.ghiabcdefghijkl`,
		`redis://default:redis-password@cache.local/0`,
		`password=plain-password`,
		`client_secret: client-secret-value`,
		`api-key: abcdefghijklmnop`,
		`access_key = "access-key-value"`,
		"-----BEGIN PRIVATE KEY-----\nfixture\n-----END PRIVATE KEY-----",
	}
	redacted := 0
	for _, secret := range secrets {
		if Redact(secret) != secret && ContainsSecret(secret) {
			redacted++
		}
	}
	require.GreaterOrEqual(t, float64(redacted)/float64(len(secrets)), 0.95)
}

func secretValue(input string) string {
	candidates := []string{
		"sk-live-1234567890abcdef",
		"abcdefghijklmnopqrstuvwxyz",
		"hunter2",
		"ghp_abcdefghijklmnopqrstuvwxyz123456",
		"supersecret",
		"AKIAIOSFODNN7EXAMPLE",
	}
	for _, candidate := range candidates {
		if strings.Contains(input, candidate) {
			return candidate
		}
	}
	return input
}
