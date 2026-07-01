//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/harness"
)

func TestPublicReplayCasesCatalog(t *testing.T) {
	cases, err := harness.LoadCases("testdata/cases")
	require.NoError(t, err)

	var clean, faulty []*harness.ReplayCase
	for _, c := range cases {
		if c.FaultInjection == "" {
			clean = append(clean, c)
			continue
		}
		faulty = append(faulty, c)
	}
	require.Len(t, clean, 20)
	require.Len(t, faulty, 20)

	cleanNames := map[string]bool{}
	cleanUsers := map[string]string{}
	var hasSummary, hasTrack, hasMemory bool
	for _, c := range clean {
		require.NotEmpty(t, c.Name)
		require.NotEmpty(t, c.Key.AppName)
		require.NotEmpty(t, c.Key.UserID)
		require.NotEmpty(t, c.Key.SessionID)
		require.NotEmpty(t, c.Operations)
		require.False(t, cleanNames[c.Name], "duplicate clean case name %q", c.Name)
		cleanNames[c.Name] = true
		if previous := cleanUsers[c.Key.UserID]; previous != "" {
			t.Fatalf("clean cases %q and %q share userID %q", previous, c.Name, c.Key.UserID)
		}
		cleanUsers[c.Key.UserID] = c.Name

		for _, op := range c.Operations {
			switch op.Type {
			case "create_summary":
				hasSummary = true
			case "append_track":
				hasTrack = true
			case "add_memory", "update_memory", "delete_memory":
				hasMemory = true
			}
		}
	}
	require.True(t, hasSummary, "public catalog must cover summary operations")
	require.True(t, hasTrack, "public catalog must cover track operations")
	require.True(t, hasMemory, "public catalog must cover memory operations")

	for _, c := range faulty {
		require.NotEmpty(t, c.Name)
		require.NotEmpty(t, c.FaultInjection)
		require.NotNil(t, c.ExpectedDefect, "faulty case %q must declare expectedDefect", c.Name)
		require.NotEmpty(t, c.ExpectedDefect.Category)
		require.NotEmpty(t, c.ExpectedDefect.FieldPath)
	}

	require.Equal(t, "integration", findCase(t, clean, "14_event_pagination").Mode)
	require.Equal(t, "integration", findCase(t, clean, "20_ttl_expiry").Mode)
}

func TestLightweightReplayCasesAreConsistent(t *testing.T) {
	bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
	require.NoError(t, err)
	defer closeBackends(bs)

	report, err := harness.RunAll(context.Background(), "testdata/cases", "light", bs)
	require.NoError(t, err)
	require.Equal(t, "inmemory", report.BaselineBackend)
	require.Equal(t, 18, report.Summary.Cases)
	if report.InconsistentCount() != 0 {
		t.Logf("report: %+v", report.Cases)
	}
	require.Zero(t, report.InconsistentCount())
}

func TestFaultyReplayCasesSurfaceExpectedDefects(t *testing.T) {
	cases, err := harness.LoadCases("testdata/cases")
	require.NoError(t, err)

	for _, c := range cases {
		if c.FaultInjection == "" || c.Mode == "integration" {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
			require.NoError(t, err)
			defer closeBackends(bs)

			base, err := harness.Run(context.Background(), bs[0], c)
			require.NoError(t, err)
			bad, err := harness.RunFaulty(context.Background(), bs[1], c)
			require.NoError(t, err)
			harness.Normalize(base)
			harness.Normalize(bad)

			diffs := harness.Compare(c.Name, bs[1].Name, base, bad)
			require.NotEmpty(t, diffs)
			require.True(t, hasExpectedDefect(diffs, c.ExpectedDefect), "diffs did not match expected defect: %+v", diffs)
		})
	}
}

func TestReplayReportCommandWritesJSON(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.json")
	cmd := exec.Command("go", "run", "./cmd/replayreport", "-cases", "testdata/cases", "-out", out)
	cmd.Dir = "."
	raw, err := cmd.CombinedOutput()
	require.NoError(t, err, string(raw))

	data, err := os.ReadFile(out)
	require.NoError(t, err)
	var report harness.Report
	require.NoError(t, json.Unmarshal(data, &report))
	require.Equal(t, "light", report.Mode)
	require.Equal(t, "inmemory", report.BaselineBackend)
	require.Contains(t, report.Backends, "sqlite")
	require.Equal(t, 18, report.Summary.Cases)
}

func findCase(t *testing.T, cases []*harness.ReplayCase, name string) *harness.ReplayCase {
	t.Helper()
	for _, c := range cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("case %q not found", name)
	return nil
}

func closeBackends(bs []*backends.Backend) {
	for _, b := range bs {
		_ = b.Close()
	}
}

func hasExpectedDefect(diffs []harness.Diff, expected *harness.ExpectedDefect) bool {
	if expected == nil {
		return false
	}
	for _, d := range diffs {
		if d.Category == expected.Category {
			return true
		}
	}
	return false
}
