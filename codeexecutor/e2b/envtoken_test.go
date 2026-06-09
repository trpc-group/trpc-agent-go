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

func TestEnvToken_NoCleanKeepsLegacyEnv(t *testing.T) {
	got := envToken(
		map[string]string{"WORKSPACE_DIR": "/ws"},
		map[string]string{"FOO": "bar"},
		false,
	)
	require.True(t, strings.HasPrefix(got, "env "),
		"non-clean token must use legacy `env `")
	require.NotContains(t, got, "-i")
	require.Contains(t, got, "WORKSPACE_DIR=")
	require.Contains(t, got, "FOO=")
	require.NotContains(t, got, minimalCleanPATH,
		"non-clean token must not inject the minimal PATH")
	require.True(t, strings.HasSuffix(got, " "),
		"token must end with a separating space")
}

func TestEnvToken_NoCleanEmptyMapsYieldEmpty(t *testing.T) {
	require.Equal(t, "", envToken(nil, nil, false))
}

func TestEnvToken_CleanUsesEnvIAndInjectsMinimalPATH(t *testing.T) {
	got := envToken(
		map[string]string{"WORKSPACE_DIR": "/ws"},
		map[string]string{"FOO": "bar"},
		true,
	)
	require.True(t, strings.HasPrefix(got, "env -i "),
		"clean token must start the command from an empty env")
	require.Contains(t, got, minimalCleanPATH)
	require.Contains(t, got, "WORKSPACE_DIR=")
	require.Contains(t, got, "FOO=")
}

func TestEnvToken_CleanKeepsCallerPATH(t *testing.T) {
	got := envToken(nil, map[string]string{"PATH": "/caller/bin"}, true)
	require.Contains(t, got, "/caller/bin")
	require.NotContains(t, got, minimalCleanPATH,
		"caller PATH must suppress the minimal PATH fallback")
}

// TestEnvToken_CleanWithEmptyPATH documents that an explicit empty
// PATH in spec is honored as the caller's deliberate choice: like
// codeexecutor/local's key-presence check, hasPathKey keys off the
// presence of "PATH", not its value, so the minimalCleanPATH
// fallback is suppressed and the token carries an empty PATH.
func TestEnvToken_CleanWithEmptyPATH(t *testing.T) {
	got := envToken(nil, map[string]string{"PATH": ""}, true)
	require.Contains(t, got, "env -i ")
	require.Contains(t, got, "PATH=''")
	require.NotContains(t, got, minimalCleanPATH,
		"explicit empty PATH should suppress the fallback")
}

// TestEnvToken_CleanCaseSensitivePATH guards the Linux-only target:
// a lowercase "Path" is a distinct variable and must not suppress
// the minimal PATH injection, otherwise `env -i` would leave the
// command without a usable PATH.
func TestEnvToken_CleanCaseSensitivePATH(t *testing.T) {
	got := envToken(nil, map[string]string{"Path": "/caller/bin"}, true)
	require.Contains(t, got, minimalCleanPATH)
	require.Contains(t, got, "Path=")
}

func TestEnvToken_SpecOverridesBaseKey(t *testing.T) {
	got := envToken(
		map[string]string{"WORKSPACE_DIR": "/base"},
		map[string]string{"WORKSPACE_DIR": "/override"},
		true,
	)
	require.Contains(t, got, "/override")
	require.NotContains(t, got, "/base")
	require.Equal(t, 1, strings.Count(got, "WORKSPACE_DIR="),
		"override must not duplicate the key")
}

func TestEnvToken_DeterministicOrdering(t *testing.T) {
	base := map[string]string{"B": "2", "A": "1"}
	spec := map[string]string{"D": "4", "C": "3"}
	first := envToken(base, spec, true)
	for i := 0; i < 16; i++ {
		require.Equal(t, first, envToken(base, spec, true),
			"token must be deterministic across calls")
	}
}

func TestEnvToken_CleanEmptyMapsYieldPathOnly(t *testing.T) {
	require.Equal(t,
		"env -i PATH="+shellQuote(minimalCleanPATH)+" ",
		envToken(nil, nil, true),
	)
}
