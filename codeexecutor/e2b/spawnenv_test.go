//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package e2b

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvTokenCleanUsesEnvIAndInjectsMinimalPATH(t *testing.T) {
	got := envToken(
		map[string]string{"WORKSPACE_DIR": "/ws"},
		map[string]string{"FOO": "bar"},
		true,
	)
	require.True(t, strings.HasPrefix(got, "env -i "),
		"clean token must start the target command from an empty env")
	require.Contains(t, got, minimalCleanPATH)
	require.Contains(t, got, "WORKSPACE_DIR=")
	require.Contains(t, got, "FOO=")
}

func TestEnvTokenCleanKeepsCallerPATH(t *testing.T) {
	got := envToken(nil, map[string]string{"PATH": "/caller/bin"}, true)
	require.Contains(t, got, "/caller/bin")
	require.NotContains(t, got, minimalCleanPATH,
		"caller PATH must suppress the minimal PATH fallback")
}

func TestCleanWrapperCommandStartsWrapperInEmptyEnv(t *testing.T) {
	got := cleanWrapperCommand("printf %s ok")
	require.True(t, strings.HasPrefix(got, "/usr/bin/env -i "),
		"clean wrapper must clear the bootstrap environment before bash starts")
	require.Contains(t, got, "PATH="+shellQuote(minimalCleanPATH))
	require.Contains(t, got, "/bin/bash --noprofile --norc -c")
	require.Contains(t, got, shellQuote("printf %s ok"))
}
