//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReportWriteAndCount(t *testing.T) {
	r := NewReport("light", "inmemory", []string{"inmemory", "sqlite"})
	r.AddCase(CaseReport{Case: "c", SessionID: "s", Results: []ResultEntry{
		{Backend: "sqlite", Category: "summary", Verdict: VerdictInconsistent, FieldPath: "summaries[\"\"].text"},
		{Backend: "sqlite", Category: "memory", Verdict: VerdictAllowedDiff, FieldPath: "memories[0].score"},
	}})
	require.Equal(t, 1, r.Summary.RealDiffs)
	require.Equal(t, 1, r.Summary.AllowedDiffs)
	require.True(t, r.HasInconsistent("c"))
	require.Equal(t, 1, r.InconsistentCount())

	dir := t.TempDir()
	p := filepath.Join(dir, "report.json")
	require.NoError(t, r.WriteJSON(p))
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	require.Contains(t, string(b), "\"baselineBackend\": \"inmemory\"")
}

func TestReportCountsUnsupported(t *testing.T) {
	r := NewReport("light", "inmemory", []string{"inmemory", "redis"})
	r.AddCase(CaseReport{Case: "c", SessionID: "s", Results: []ResultEntry{
		{Backend: "redis", Category: "eventpage", Verdict: VerdictUnsupported, FieldPath: "events[3]"},
	}})
	require.Equal(t, 1, r.Summary.Unsupported)
	require.Equal(t, 0, r.Summary.RealDiffs)
	require.False(t, r.HasInconsistent("c"))
}
