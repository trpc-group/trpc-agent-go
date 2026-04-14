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
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestRun_UnknownFlagReturnsUsageCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, 2, run([]string{"-unknown-flag"}))
}

func TestRun_Version(t *testing.T) {
	var stdout bytes.Buffer
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() {
		os.Stdout = oldStdout
	})

	code := run([]string{"version"})
	require.Equal(t, 0, code)
	require.NoError(t, w.Close())
	_, err = stdout.ReadFrom(r)
	require.NoError(t, err)
	require.Equal(t, currentVersion()+"\n", stdout.String())
}

func TestRun_Upgrade(t *testing.T) {
	old := runUpgradeCommandFunc
	runUpgradeCommandFunc = func(args []string) int {
		require.Equal(t, []string{"--state-dir", "/tmp/state"}, args)
		return 7
	}
	t.Cleanup(func() {
		runUpgradeCommandFunc = old
	})

	require.Equal(
		t,
		7,
		run([]string{"upgrade", "--state-dir", "/tmp/state"}),
	)
}

func TestBundledChannels(t *testing.T) {
	t.Helper()

	_, ok := registry.LookupChannel("telegram")
	require.True(t, ok)

	_, ok = registry.LookupChannel("stdin")
	require.True(t, ok)
}
