//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

//

package safety

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Acceptance corpus types whose detection rate must reach 100% (issue #2002
// acceptance criterion 3: reading secrets, dangerous deletion, non-allowlisted
// network egress).
var mustCatchTypes = map[string]bool{
	"secret":  true,
	"delete":  true,
	"network": true,
}

// corpusSample is one entry of testdata/samples.json.
type corpusSample struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Group        string `json:"group"` // high_risk | safe | benchmark
	Type         string `json:"type"`
	Command      string `json:"command"`
	Backend      string `json:"backend"`
	Expected     string `json:"expected"`
	ExpectedRisk string `json:"expected_risk"`
	ExpectRule   string `json:"expect_rule,omitempty"`
	Repeat       int    `json:"repeat,omitempty"`
}

// corpusFile is the root of testdata/samples.json.
type corpusFile struct {
	Policy  string         `json:"policy"`
	Samples []corpusSample `json:"samples"`
}

func loadCorpus(t *testing.T) (*Scanner, []corpusSample) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "samples.json"))
	require.NoError(t, err)
	var cf corpusFile
	require.NoError(t, json.Unmarshal(data, &cf))
	require.NotEmpty(t, cf.Samples, "corpus must be non-empty")

	policy, err := LoadPolicy(filepath.Join("testdata", cf.Policy))
	require.NoError(t, err, "corpus policy must load (proves criterion 6: policy-driven config)")
	s := NewScanner(policy)
	return s, cf.Samples
}

// reportKeys are the fields criterion 5 requires in every structured report.
func assertStructuredReport(t *testing.T, s corpusSample, r ScanReport) {
	t.Helper()
	raw, err := json.Marshal(r)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	for _, key := range []string{"decision", "risk_level", "rule_id", "evidence", "recommendation"} {
		_, ok := m[key]
		assert.True(t, ok, "sample %s report missing field %q", s.ID, key)
	}
	if r.Decision != DecisionAllow {
		assert.NotEmpty(t, r.Recommendation, "sample %s must carry a recommendation for %s", s.ID, r.Decision)
		assert.NotEmpty(t, r.Evidence, "sample %s must carry evidence for %s", s.ID, r.Decision)
	}
}

// TestAcceptanceCorpus runs every corpus sample and enforces the hard-coded
// acceptance metrics from issue #2002:
//   - every sample scans and produces a structured report (criterion 1, 5)
//   - high-risk samples: ≥ 90% intercepted, and the three must-catch types
//     (secrets / dangerous deletion / non-allowlisted egress) reach 100%
//     denial (criterion 2, 3)
//   - safe samples: ≤ 10% false positives (criterion 2)
func TestAcceptanceCorpus(t *testing.T) {
	s, samples := loadCorpus(t)

	total, intercepted, safeTotal, safeFlagged := 0, 0, 0, 0
	mustCatchHit := make(map[string]int)

	for _, smp := range samples {
		cmd := smp.Command
		if smp.Repeat > 1 {
			cmd = strings.Repeat(cmd, smp.Repeat)
		}
		r := s.Scan(context.Background(), ScanRequest{
			ToolName: "workspace_exec",
			Command:  cmd,
			Backend:  smp.Backend,
		})

		assertStructuredReport(t, smp, r)

		switch smp.Group {
		case "high_risk":
			total++
			if r.Decision == DecisionDeny || r.Decision == DecisionAsk {
				intercepted++
			}
			assert.NotEqual(t, DecisionAllow, r.Decision,
				"high-risk sample %s (%s) must be intercepted, got %s",
				smp.ID, smp.Name, r.Decision)
			assert.Equal(t, RiskLevel(smp.ExpectedRisk), r.RiskLevel,
				"sample %s risk level mismatch", smp.ID)
			if smp.ExpectRule != "" {
				assert.Equal(t, smp.ExpectRule, r.RuleID,
					"sample %s rule mismatch", smp.ID)
			}
			if mustCatchTypes[smp.Type] {
				mustCatchHit[smp.Type]++
				assert.Equal(t, DecisionDeny, r.Decision,
					"must-catch sample %s (%s) must deny, got %s",
					smp.ID, smp.Type, r.Decision)
			}
		case "safe":
			safeTotal++
			if r.Decision != DecisionAllow {
				safeFlagged++
			}
			assert.Equal(t, DecisionAllow, r.Decision,
				"safe sample %s (%s) must allow, got %s (rule=%s)",
				smp.ID, smp.Name, r.Decision, r.RuleID)
		case "benchmark":
			start := time.Now()
			r := s.Scan(context.Background(), ScanRequest{
				ToolName: "workspace_exec",
				Command:  cmd,
				Backend:  smp.Backend,
			})
			require.Less(t, time.Since(start), time.Second,
				"500-segment scan took %v, expected <1s (criterion 4)", time.Since(start))
			assert.Equal(t, DecisionAllow, r.Decision, "benchmark sample must stay allow")
		}
	}

	// Criterion 2: high-risk detection rate ≥ 90%.
	detectRate := float64(intercepted) / float64(total) * 100
	assert.GreaterOrEqual(t, detectRate, 90.0,
		"high-risk detection rate %.1f%% must be ≥ 90%% (%d/%d)",
		detectRate, intercepted, total)

	// Criterion 2: safe false-positive rate ≤ 10%.
	fpRate := float64(safeFlagged) / float64(safeTotal) * 100
	assert.LessOrEqual(t, fpRate, 10.0,
		"safe false-positive rate %.1f%% must be ≤ 10%% (%d/%d)",
		fpRate, safeFlagged, safeTotal)

	// Criterion 3: must-catch types (secrets/delete/egress) 100%.
	for _, typ := range []string{"secret", "delete", "network"} {
		assert.NotZero(t, mustCatchHit[typ], "corpus must contain %q must-catch samples", typ)
	}
	require.NotZero(t, total, "corpus must contain high-risk samples")
	require.NotZero(t, safeTotal, "corpus must contain safe samples")
}

// TestAcceptanceCorpusMinSamples guarantees the corpus satisfies the "at least
// 12 samples" deliverable and that every required scenario type from the issue
// is present.
func TestAcceptanceCorpusMinSamples(t *testing.T) {
	_, samples := loadCorpus(t)
	assert.GreaterOrEqual(t, len(samples), 12, "corpus must have ≥ 12 samples")

	types := map[string]bool{}
	for _, s := range samples {
		types[s.Type] = true
	}
	for _, required := range []string{
		"delete", "secret", "network", "whitelist_network", "shell_bypass",
		"pipeline", "dependency", "long_run", "output_flood", "hostexec",
		"ask", "safe",
	} {
		assert.True(t, types[required], "corpus missing required scenario type %q", required)
	}
}
