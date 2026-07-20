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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// corpusFixture is the JSON schema of testdata/tool_safety_corpus.json.
type corpusFixture struct {
	SchemaVersion string         `json:"schema_version"`
	Description   string         `json:"description"`
	Cases         []corpusCaseV2 `json:"cases"`
}

type corpusCaseV2 struct {
	Name     string          `json:"name"`
	Category string          `json:"category"`
	Expected Decision        `json:"expected"`
	Input    jsonScanInputV2 `json:"input"`
}

type jsonScanInputV2 struct {
	ToolName       string            `json:"tool_name"`
	Backend        Backend           `json:"backend"`
	Command        string            `json:"command"`
	CodeBlocks     []CodeBlock       `json:"code_blocks"`
	Cwd            string            `json:"cwd"`
	Env            map[string]string `json:"env"`
	PTY            bool              `json:"pty"`
	Background     bool              `json:"background"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

// loadCorpusFixture loads the shared corpus JSON. Both quality_test.go
// and the example program can use this fixture so there is one source
// of truth for the sample set.
func loadCorpusFixture(t *testing.T) corpusFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/tool_safety_corpus.json")
	require.NoError(t, err)
	var fixture corpusFixture
	require.NoError(t, json.Unmarshal(data, &fixture))
	require.NotEmpty(t, fixture.Cases)
	return fixture
}

// toScanInput converts a JSON corpus case to a ScanInput.
func (c *corpusCaseV2) toScanInput() ScanInput {
	in := ScanInput{
		ToolName:   c.Input.ToolName,
		Backend:    c.Input.Backend,
		Command:    c.Input.Command,
		CodeBlocks: c.Input.CodeBlocks,
		Cwd:        c.Input.Cwd,
		Env:        c.Input.Env,
		PTY:        c.Input.PTY,
		Background: c.Input.Background,
	}
	if c.Input.TimeoutSeconds > 0 {
		in.Timeout = time.Duration(c.Input.TimeoutSeconds) * time.Second
	}
	return in
}

func TestCorpusFixture_LoadsAndScans(t *testing.T) {
	fixture := loadCorpusFixture(t)
	p := testPolicy(t)
	scanner := NewScanner(p)
	for _, tc := range fixture.Cases {
		report, err := scanner.Scan(context.Background(), tc.toScanInput())
		require.NoError(t, err, "case %s", tc.Name)
		// Every case must produce a non-empty scan id and schema.
		require.NotEmpty(t, report.ScanID, "case %s", tc.Name)
		require.Equal(t, "1", report.SchemaVersion, "case %s", tc.Name)
		// No raw secrets in the report.
		raw, _ := json.Marshal(report)
		require.NotContains(t, string(raw), "sk_live_1234567890", "case %s", tc.Name)
	}
}

func TestCorpusFixture_QualityGateFromFixture(t *testing.T) {
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
				t.Logf("safe false positive: %s -> %s (rules=%v)", tc.Name, report.Decision, ruleIDsFromFindings(report.Findings))
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
			t.Logf("case %s: expected %s got %s (rules=%v)", tc.Name, tc.Expected, report.Decision, ruleIDsFromFindings(report.Findings))
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
