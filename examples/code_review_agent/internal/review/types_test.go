//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindingValidateAcceptsValidFinding(t *testing.T) {
	f := Finding{
		SchemaVersion:  SchemaVersion,
		Severity:       SeverityHigh,
		Category:       "security",
		File:           "internal/server/server.go",
		Line:           42,
		Title:          "unchecked authorization",
		Evidence:       "the handler uses the request before authorization",
		Recommendation: "authorize the request before using it",
		Confidence:     ConfidenceHigh,
		Source:         SourceRule,
		RuleID:         "security/authorization",
		Fingerprint:    "review/v1:example",
		Disposition:    DispositionFinding,
	}

	require.NoError(t, f.Validate())
}

func TestFindingValidateRejectsUnknownEnums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Finding)
	}{
		{name: "severity", mutate: func(f *Finding) { f.Severity = "urgent" }},
		{name: "confidence", mutate: func(f *Finding) { f.Confidence = "certain" }},
		{name: "source", mutate: func(f *Finding) { f.Source = "scanner" }},
		{name: "disposition", mutate: func(f *Finding) { f.Disposition = "accepted" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validFinding()
			tt.mutate(&f)
			require.Error(t, f.Validate())
		})
	}
}

func TestTaskValidateUsesClosedStatusAndPhaseEnums(t *testing.T) {
	task := Task{
		SchemaVersion: SchemaVersion,
		ID:            "task-1",
		Status:        StatusRunning,
		Phase:         PhaseRules,
		Mode:          "rule-only",
	}
	require.NoError(t, task.Validate())

	task.Status = "paused"
	require.Error(t, task.Validate())

	task.Status = StatusSkipped
	require.Error(t, task.Validate())

	task.Status = StatusRunning
	task.Phase = "publishing"
	require.Error(t, task.Validate())
}

func TestGovernanceDecisionValidateUsesClosedEnums(t *testing.T) {
	decision := GovernanceDecision{
		SchemaVersion: SchemaVersion,
		Kind:          DecisionKindPermission,
		Tool:          "go_test",
		Action:        DecisionActionAllow,
		Reason:        "fixed command is allowed",
		Rule:          "allow-go-test",
	}
	require.NoError(t, decision.Validate())

	decision.Kind = "approval"
	require.Error(t, decision.Validate())

	decision.Kind = DecisionKindPermission
	decision.Action = "prompt"
	require.Error(t, decision.Validate())
}

func TestSandboxRunValidateAcceptsRunOnlyStatus(t *testing.T) {
	run := SandboxRun{
		SchemaVersion: SchemaVersion,
		TaskID:        "task-1",
		Command:       "go test",
		Status:        StatusSkipped,
	}

	require.NoError(t, run.Validate())
}

func TestMetricsValidateSchemaVersion(t *testing.T) {
	metrics := Metrics{SchemaVersion: SchemaVersion}
	require.NoError(t, metrics.Validate())

	metrics.SchemaVersion = "review/v2"
	require.Error(t, metrics.Validate())
}

func validFinding() Finding {
	return Finding{
		SchemaVersion:  SchemaVersion,
		Severity:       SeverityHigh,
		Category:       "security",
		File:           "internal/server/server.go",
		Line:           42,
		Title:          "unchecked authorization",
		Evidence:       "the handler uses the request before authorization",
		Recommendation: "authorize the request before using it",
		Confidence:     ConfidenceHigh,
		Source:         SourceRule,
		RuleID:         "security/authorization",
		Fingerprint:    "review/v1:example",
		Disposition:    DispositionFinding,
	}
}
