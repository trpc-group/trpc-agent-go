//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/harness"
)

func TestRunAllCleanCaseNoInconsistency(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "01.json"), []byte(`{
		"name":"01_single_turn",
		"key":{"appName":"a","userID":"u","sessionID":"s"},
		"operations":[
			{"type":"append_event","event":{"author":"user","role":"user","content":"hi"}},
			{"type":"append_event","event":{"author":"assistant","role":"assistant","content":"hello"}}
		]
	}`), 0o644))

	bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
	require.NoError(t, err)
	defer func() {
		for _, b := range bs {
			_ = b.Close()
		}
	}()

	rep, err := harness.RunAll(context.Background(), dir, "light", bs)
	require.NoError(t, err)
	require.Equal(t, 0, rep.Summary.RealDiffs)
	require.Equal(t, "inmemory", rep.BaselineBackend)
	require.Equal(t, []string{"inmemory", "sqlite"}, rep.Backends)
}

func TestLoadCasesSortedByName(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "02.json"), []byte(`{"name":"b","key":{"appName":"a","userID":"u","sessionID":"s2"},"operations":[]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "01.json"), []byte(`{"name":"a","key":{"appName":"a","userID":"u","sessionID":"s1"},"operations":[]}`), 0o644))
	cases, err := harness.LoadCases(dir)
	require.NoError(t, err)
	require.Len(t, cases, 2)
	require.Equal(t, "a", cases[0].Name)
	require.Equal(t, "b", cases[1].Name)
}
