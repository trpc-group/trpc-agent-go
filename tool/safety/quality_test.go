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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCorpusQuality is the executable quality gate required by the plan.
// It loads the shared corpus fixture (testdata/tool_safety_corpus.json)
// and asserts:
//   - high-risk recall >= 0.90
//   - safe false-positive rate <= 0.10
//   - 100% detection for credential/key reads, dangerous deletion, and
//     non-whitelisted network targets.
func TestCorpusQuality(t *testing.T) {
	fixture := loadCorpusFixture(t)
	p := testPolicy(t)
	scanner := NewScanner(p)

	var highRiskTotal, highRiskDetected int
	var safeTotal, safeFP int
	mandatory := map[string]int{"dangerous_delete": 0, "credential_read": 0, "network": 0}
	mandatoryTotal := map[string]int{"dangerous_delete": 0, "credential_read": 0, "network": 0}

	for _, tc := range fixture.Cases {
		report, err := scanner.Scan(context.Background(), tc.toScanInput())
		require.NoError(t, err, "case %s", tc.Name)

		isHighRisk := tc.Expected == DecisionDeny
		if isHighRisk {
			highRiskTotal++
		}
		if tc.Expected == DecisionAllow {
			safeTotal++
			if report.Decision != DecisionAllow {
				safeFP++
				t.Logf("safe false positive: %s -> %s (rules=%v)",
					tc.Name, report.Decision, ruleIDsFromFindings(report.Findings))
			}
		}

		matched := report.Decision == tc.Expected
		if isHighRisk && matched {
			highRiskDetected++
		}
		if _, ok := mandatory[tc.Category]; ok {
			mandatoryTotal[tc.Category]++
			if matched {
				mandatory[tc.Category]++
			} else {
				t.Logf("mandatory miss: %s expected %s got %s (rules=%v)",
					tc.Name, tc.Expected, report.Decision, ruleIDsFromFindings(report.Findings))
			}
		}
		if !matched {
			t.Logf("case %s: expected %s got %s (rules=%v)",
				tc.Name, tc.Expected, report.Decision, ruleIDsFromFindings(report.Findings))
		}
	}

	require.NotZero(t, highRiskTotal)
	recall := float64(highRiskDetected) / float64(highRiskTotal)
	require.GreaterOrEqual(t, recall, 0.90, "high-risk recall too low: %.2f", recall)

	require.NotZero(t, safeTotal)
	fpRate := float64(safeFP) / float64(safeTotal)
	require.LessOrEqual(t, fpRate, 0.10, "safe false-positive rate too high: %.2f", fpRate)

	for cat, got := range mandatory {
		require.Equal(t, mandatoryTotal[cat], got,
			"mandatory category %s: expected 100%% detection (%d/%d)",
			cat, got, mandatoryTotal[cat])
	}
}

// TestCorpusPerformance_500UnderOneSecond asserts the 500-sample
// performance budget from the plan.
func TestCorpusPerformance_500UnderOneSecond(t *testing.T) {
	p := testPolicy(t)
	s := NewScanner(p)
	inputs := make([]ScanInput, 0, 500)
	for i := 0; i < 500; i++ {
		inputs = append(inputs, ScanInput{
			ToolName: "workspace_exec",
			Backend:  BackendWorkspaceExec,
			Command:  "echo " + itoa(i),
		})
	}
	start := time.Now()
	_, err := s.ScanBatch(context.Background(), inputs)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.Less(t, elapsed, time.Second, "500-command batch took %v", elapsed)
}
