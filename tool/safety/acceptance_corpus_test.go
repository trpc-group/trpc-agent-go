//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// corpusSample mirrors testdata/acceptance_corpus.json entries used as
// hard gates for issue #2002 acceptance criteria 1–5.
type corpusSample struct {
	ID                 string         `json:"id"`
	Class              string         `json:"class"` // "safe" | "high"
	Category           string         `json:"category,omitempty"`
	AcceptanceScenario string         `json:"acceptance_scenario,omitempty"`
	MustCatch          string         `json:"must_catch,omitempty"`
	Tool               string         `json:"tool"`
	Args               map[string]any `json:"args"`
	Expect             string         `json:"expect"` // allow|deny|ask
	AllowHosts         []string       `json:"allow_hosts,omitempty"`
	StdinPad           int            `json:"stdin_pad,omitempty"`
}

func loadAcceptanceCorpus(t *testing.T) []corpusSample {
	t.Helper()
	path := filepath.Join("testdata", "acceptance_corpus.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var samples []corpusSample
	require.NoError(t, json.Unmarshal(raw, &samples))
	require.GreaterOrEqual(t, len(samples), 35)
	return samples
}

func guardForSample(s corpusSample) *safety.Guard {
	policy := safety.DefaultPolicy()
	if len(s.AllowHosts) > 0 {
		policy.AllowedHosts = append(append([]string{}, policy.AllowedHosts...), s.AllowHosts...)
	}
	return safety.NewGuard(safety.WithPolicy(policy), safety.WithAuditor(safety.NewMemoryAuditor()))
}

func expandArgs(s corpusSample) map[string]any {
	args := map[string]any{}
	for k, v := range s.Args {
		args[k] = v
	}
	if s.StdinPad > 0 {
		args["stdin"] = strings.Repeat("y", s.StdinPad)
	}
	return args
}

func TestAcceptanceCorpus_QualityGates(t *testing.T) {
	samples := loadAcceptanceCorpus(t)
	ctx := context.Background()

	var (
		highTotal, highCaught int
		safeTotal, safeFP     int
		mustCatch             = map[string]int{"secret": 0, "delete": 0, "egress": 0}
		mustCatchOK           = map[string]int{"secret": 0, "delete": 0, "egress": 0}
		categories            = map[string]int{}
		scenarios             = map[string]int{}
	)

	for _, s := range samples {
		s := s
		if s.Category != "" {
			categories[s.Category]++
		}
		if s.AcceptanceScenario != "" {
			scenarios[s.AcceptanceScenario]++
		}
		g := guardForSample(s)
		raw, err := json.Marshal(expandArgs(s))
		require.NoError(t, err)
		dec, err := g.CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName:  s.Tool,
			Arguments: raw,
		})
		require.NoError(t, err, s.ID)
		got := string(dec.Action)
		require.Equal(t, s.Expect, got, "%s reason=%s", s.ID, dec.Reason)

		results := g.LastResults()
		require.NotEmpty(t, results, s.ID)
		rep := results[len(results)-1]
		require.NotEmpty(t, string(rep.Decision), "%s decision", s.ID)
		require.NotEmpty(t, string(rep.RiskLevel), "%s risk_level", s.ID)
		if rep.Decision != safety.DecisionAllow {
			require.NotEmpty(t, rep.RuleID, "%s rule_id", s.ID)
			require.NotEmpty(t, rep.Evidence, "%s evidence", s.ID)
			require.NotEmpty(t, rep.Advice, "%s recommendation", s.ID)
		}

		switch s.Class {
		case "high":
			highTotal++
			if got != "allow" {
				highCaught++
			}
		case "safe":
			safeTotal++
			if got != "allow" {
				safeFP++
			}
		}
		if s.MustCatch != "" {
			mustCatch[s.MustCatch]++
			if got == "deny" {
				mustCatchOK[s.MustCatch]++
			}
		}
	}

	require.Greater(t, highTotal, 0)
	require.Greater(t, safeTotal, 0)
	// Criterion 2: high-risk detection ≥ 90%, safe false-positive ≤ 10%.
	// Corpus expects are locked to DefaultPolicy; rates are measured against
	// those labels so a policy regression cannot silently pass.
	highRate := float64(highCaught) / float64(highTotal)
	fpRate := float64(safeFP) / float64(safeTotal)
	require.GreaterOrEqual(t, highRate, 0.90, "high detection %d/%d", highCaught, highTotal)
	require.LessOrEqual(t, fpRate, 0.10, "safe FP %d/%d", safeFP, safeTotal)

	// Criterion 3: secret / delete / egress must-catch are 100% deny.
	for kind, n := range mustCatch {
		require.Greater(t, n, 0, "corpus missing must_catch=%s", kind)
		require.Equal(t, n, mustCatchOK[kind], "must_catch %s deny rate", kind)
	}
	require.GreaterOrEqual(t, len(categories), 8, "category coverage")
	require.GreaterOrEqual(t, len(scenarios), 10, "acceptance_scenario coverage")
	require.GreaterOrEqual(t, safeTotal, 8)
	require.GreaterOrEqual(t, highTotal, 20)
}

func TestAcceptanceCorpus_500SegmentUnderOneSecond(t *testing.T) {
	policy := safety.DefaultPolicy()
	start := time.Now()
	for i := 0; i < 500; i++ {
		_ = safety.Scan(safety.Extracted{
			Backend:  safety.BackendWorkspace,
			ToolName: "workspace_exec",
			Command:  "go test ./pkg/...",
			RawText:  "go test ./pkg/...",
		}, policy)
	}
	require.Less(t, time.Since(start), time.Second)
}
