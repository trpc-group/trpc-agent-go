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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/cases"
)

// TestMutationDetectionMatrix is the executable evidence for the detection
// acceptance criteria: for every public case and every applicable injected
// inconsistency, the differ must produce at least one non-allowed diff in
// the expected dimension.
func TestMutationDetectionMatrix(t *testing.T) {
	tgt := replaytest.NewInMemoryTarget("mutation-source")
	defer tgt.Close()
	runner := replaytest.NewRunner()
	mutations := replaytest.Mutations()

	detected := map[string]int{}
	skipped := map[string]int{}

	for _, c := range cases.All() {
		snap, err := runner.RunCase(context.Background(), c, tgt)
		require.NoError(t, err, "case %s", c.Name)
		require.Empty(t, snap.Unsupported, "in-memory target supports everything")
		base := replaytest.Normalize(snap)

		for _, m := range mutations {
			m := m
			if !m.Applies(base) {
				skipped[m.Name]++
				continue
			}
			t.Run(c.Name+"/"+m.Name, func(t *testing.T) {
				mutated := replaytest.CloneCanonical(base)
				m.Apply(mutated)
				diffs := replaytest.DiffCanonical(base, mutated, c.UnorderedEvents)
				var real []replaytest.Diff
				for _, d := range diffs {
					if !d.Allowed {
						real = append(real, d)
					}
				}
				require.NotEmpty(t, real,
					"mutation %s on case %s went undetected", m.Name, c.Name)
				found := false
				for _, d := range real {
					assert.NotEmpty(t, d.Path, "diff must carry a field path")
					assertDiffLocated(t, d)
					if d.Dimension == m.Dimension {
						found = true
					}
				}
				assert.True(t, found,
					"mutation %s: want a %s-dimension diff, got %v", m.Name, m.Dimension, real)
			})
			detected[m.Name]++
		}
	}

	// Every mutation must be applicable to at least one public case;
	// otherwise the matrix has a coverage hole.
	for _, m := range mutations {
		assert.Greater(t, detected[m.Name], 0,
			"mutation %s never applied (skipped %d times)", m.Name, skipped[m.Name])
		t.Logf("mutation %-24s applied to %d cases", m.Name, detected[m.Name])
	}
}

// assertDiffLocated checks that a diff carries the identity field of its
// dimension, so a failure report points at the offending entity, not just
// a field path. App/user state and error diffs are located by path alone.
func assertDiffLocated(t *testing.T, d replaytest.Diff) {
	t.Helper()
	switch d.Dimension {
	case replaytest.DimSession, replaytest.DimEvent, replaytest.DimSummary:
		assert.NotEmpty(t, d.SessionID, "diff at %s lacks session_id", d.Path)
	case replaytest.DimTrack:
		assert.NotEmpty(t, d.TrackName, "diff at %s lacks track_name", d.Path)
	case replaytest.DimMemory:
		assert.NotEmpty(t, d.MemoryID, "diff at %s lacks memory_id", d.Path)
	}
}

// TestSummaryMutationCategories pins the issue-mandated summary detection
// guarantees: loss, stale overwrite, wrong attribution and filter-key error
// must each be detected with filter-key localization.
func TestSummaryMutationCategories(t *testing.T) {
	tgt := replaytest.NewInMemoryTarget("summary-mutation-source")
	defer tgt.Close()
	runner := replaytest.NewRunner()

	c := cases.SummaryGenerateUpdate()
	snap, err := runner.RunCase(context.Background(), c, tgt)
	require.NoError(t, err)
	base := replaytest.Normalize(snap)
	require.NotNil(t, base.Sessions[0].Summaries[""])

	named := map[string]string{
		"drop_summary":          "loss",
		"stale_summary":         "overwrite",
		"wrong_session_summary": "attribution",
		"wrong_filterkey":       "filter-key",
	}
	for _, m := range replaytest.Mutations() {
		category, ok := named[m.Name]
		if !ok {
			continue
		}
		t.Run(category, func(t *testing.T) {
			mutated := replaytest.CloneCanonical(base)
			m.Apply(mutated)
			diffs := replaytest.DiffCanonical(base, mutated, false)
			var real []replaytest.Diff
			for _, d := range diffs {
				if !d.Allowed {
					real = append(real, d)
				}
			}
			require.NotEmpty(t, real, "%s summary problem undetected", category)
			for _, d := range real {
				assert.Equal(t, replaytest.DimSummary, d.Dimension)
			}
		})
	}
}
