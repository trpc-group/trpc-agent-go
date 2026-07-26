//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConfigurationValidatorsRejectEveryInvalidFieldClass(t *testing.T) {
	validGate := func() GatePolicy {
		return GatePolicy{
			PrimaryMetric:         "quality",
			MetricDirections:      map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
			Epsilon:               DefaultEpsilon,
			MinValidationGain:     0,
			NoNewHardFailures:     true,
			NoCriticalRegressions: true,
		}
	}
	validPromptIter := func() PromptIterConfig {
		return PromptIterConfig{
			SchemaVersion: SchemaVersion,
			Policy: PromptIterPolicy{
				MaxOuterRounds:             1,
				SearchMinScoreGain:         0,
				InternalValidationStrategy: internalValidationTrainCaseIDs,
				InternalValidationCaseIDs:  []string{"train-a"},
				TargetSurfaceID:            "node#instruction",
			},
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*PromptIterConfig)
	}{
		{"schema", func(config *PromptIterConfig) { config.SchemaVersion = "2.0" }},
		{"rounds", func(config *PromptIterConfig) { config.Policy.MaxOuterRounds = 0 }},
		{"gain", func(config *PromptIterConfig) { config.Policy.SearchMinScoreGain = math.NaN() }},
		{"empty target", func(config *PromptIterConfig) { config.Policy.TargetSurfaceID = "" }},
		{"missing case ids", func(config *PromptIterConfig) {
			config.Policy.InternalValidationCaseIDs = nil
		}},
		{"train all with ids", func(config *PromptIterConfig) {
			config.Policy.InternalValidationStrategy = internalValidationTrainAll
		}},
		{"unsupported strategy", func(config *PromptIterConfig) {
			config.Policy.InternalValidationStrategy = "other"
		}},
		{"duplicate case", func(config *PromptIterConfig) {
			config.Policy.InternalValidationCaseIDs = []string{"train-a", "train-a"}
		}},
	} {
		t.Run("promptiter/"+test.name, func(t *testing.T) {
			config := validPromptIter()
			test.mutate(&config)
			require.Error(t, validatePromptIterConfig(config))
		})
	}

	for _, test := range []struct {
		name   string
		mutate func(*GatePolicy)
	}{
		{"primary metric", func(policy *GatePolicy) { policy.PrimaryMetric = "" }},
		{"directions", func(policy *GatePolicy) { policy.MetricDirections = nil }},
		{"epsilon", func(policy *GatePolicy) { policy.Epsilon = math.Inf(1) }},
		{"gain", func(policy *GatePolicy) { policy.MinValidationGain = -1 }},
		{"threshold", func(policy *GatePolicy) { policy.ModelCallStopThreshold = -1 }},
		{"empty metric", func(policy *GatePolicy) {
			policy.MetricDirections = map[string]ScoreDirection{"": ScoreHigherIsBetter}
		}},
		{"invalid direction", func(policy *GatePolicy) {
			policy.MetricDirections["quality"] = "sideways"
		}},
	} {
		t.Run("gate/"+test.name, func(t *testing.T) {
			policy := validGate()
			test.mutate(&policy)
			require.Error(t, validateGatePolicy(policy))
		})
	}

	validRegression := func() RegressionConfig {
		return RegressionConfig{
			SchemaVersion: SchemaVersion,
			ReportID:      "report",
			GeneratedAt:   time.Unix(0, 0).UTC(),
			Gate:          validGate(),
			EvidenceLimit: 10,
			Output: OutputConfig{
				JSON:     "optimization_report.json",
				Markdown: "optimization_report.md",
			},
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*RegressionConfig)
	}{
		{"schema", func(config *RegressionConfig) { config.SchemaVersion = "2.0" }},
		{"report", func(config *RegressionConfig) { config.ReportID = "" }},
		{"generated", func(config *RegressionConfig) { config.GeneratedAt = time.Time{} }},
		{"evidence zero", func(config *RegressionConfig) { config.EvidenceLimit = 0 }},
		{"evidence max", func(config *RegressionConfig) { config.EvidenceLimit = 101 }},
		{"gate", func(config *RegressionConfig) { config.Gate.PrimaryMetric = "" }},
		{"critical duplicate", func(config *RegressionConfig) {
			config.CriticalCaseIDs = []string{"a", "a"}
		}},
		{"hard empty", func(config *RegressionConfig) {
			config.HardFailureCaseIDs = []string{""}
		}},
		{"output", func(config *RegressionConfig) { config.Output.JSON = "" }},
	} {
		t.Run("regression/"+test.name, func(t *testing.T) {
			config := validRegression()
			test.mutate(&config)
			require.Error(t, validateRegressionConfig(config))
		})
	}

	for _, output := range []OutputConfig{
		{JSON: "", Markdown: "report.md"},
		{JSON: "report.json", Markdown: ""},
		{JSON: filepath.Join("nested", "report.json"), Markdown: "report.md"},
		{JSON: "report.json", Markdown: filepath.Join("nested", "report.md")},
		{JSON: "report.json", Markdown: "REPORT.JSON"},
	} {
		require.Error(t, validateOutputConfig(output))
	}
	require.NoError(t, validateOutputConfig(OutputConfig{
		JSON: "report.json", Markdown: "report.md",
	}))
}

func TestRunConfigValidationRejectsUnboundPublicFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RunConfig)
	}{
		{"nil profile", func(config *RunConfig) { config.InitialProfile = nil }},
		{"empty report", func(config *RunConfig) { config.ReportID = "" }},
		{"empty run", func(config *RunConfig) { config.RunID = "" }},
		{"same split", func(config *RunConfig) {
			config.Validation.EvalSetID = config.Train.EvalSetID
		}},
		{"empty evaluator hash", func(config *RunConfig) { config.EvaluatorConfigHash = "" }},
		{"empty policy hash", func(config *RunConfig) { config.MetricPolicyHash = "" }},
		{"empty engine", func(config *RunConfig) { config.Runtime.Engine = "" }},
		{"runtime seed", func(config *RunConfig) { config.Runtime.Seed++ }},
		{"evidence zero", func(config *RunConfig) { config.EvidenceLimit = 0 }},
		{"evidence max", func(config *RunConfig) { config.EvidenceLimit = 101 }},
		{"non UTC time", func(config *RunConfig) {
			config.GeneratedAt = config.GeneratedAt.In(time.FixedZone("offset", 3600))
		}},
		{"train provenance", func(config *RunConfig) { config.Train.EvalSetHash = "" }},
		{"train cases", func(config *RunConfig) { config.Train.CaseIDs = nil }},
		{"validation metrics", func(config *RunConfig) { config.Validation.MetricNames = nil }},
		{"empty case", func(config *RunConfig) { config.Train.CaseIDs[0] = "" }},
		{"empty metric", func(config *RunConfig) { config.Train.MetricNames[0] = "" }},
		{"unexpected normalized hash", func(config *RunConfig) {
			delete(config.Train.NormalizedInputHashes, "train-a")
			config.Train.NormalizedInputHashes["other"] = "hash"
		}},
		{"missing normalized hash", func(config *RunConfig) {
			config.Train.NormalizedInputHashes["train-a"] = ""
		}},
		{"metric inventory", func(config *RunConfig) {
			config.Validation.MetricNames = []string{"other"}
		}},
		{"metrics hash", func(config *RunConfig) {
			config.Validation.MetricsHash = "other"
		}},
		{"primary absent", func(config *RunConfig) {
			config.Gate.PrimaryMetric = "other"
		}},
		{"invalid direction", func(config *RunConfig) {
			config.Gate.MetricDirections["quality"] = "sideways"
		}},
		{"critical outside validation", func(config *RunConfig) {
			config.CriticalCaseIDs = []string{"train-a"}
		}},
		{"empty input hash", func(config *RunConfig) {
			config.InputHashes["baselinePrompt"] = ""
		}},
		{"validation input hash", func(config *RunConfig) {
			config.InputHashes["validationEvalSet"] = "other"
		}},
		{"metrics input hash", func(config *RunConfig) {
			config.InputHashes["metrics"] = "other"
		}},
		{"policy hash", func(config *RunConfig) { config.MetricPolicyHash = "other" }},
		{"evaluator binding", func(config *RunConfig) {
			config.EvaluatorConfigHash = "other"
		}},
		{"empty source binding", func(config *RunConfig) { config.sourceConfigHash = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := pipelineRunConfig()
			test.mutate(&config)
			require.Error(t, validateRunConfig(&config))
		})
	}
	require.Error(t, validateRunConfig(nil))
}

func TestSnapshotRequestValidationRejectsIncompleteProvenance(t *testing.T) {
	valid := func() (SnapshotRequest, string) {
		profile := pipelineProfile("prompt")
		hash, err := ProfileFingerprint(profile)
		require.NoError(t, err)
		return SnapshotRequest{
			EvaluationRunID:     "run/evaluation",
			Profile:             profile,
			ExpectedProfileHash: hash,
			Dataset: DatasetSpec{
				EvalSetID:             "train",
				EvalSetHash:           "train-hash",
				MetricsHash:           "metrics-hash",
				CaseIDs:               []string{"case-a"},
				MetricNames:           []string{"quality"},
				NormalizedInputHashes: map[string]string{"case-a": "input-hash"},
			},
			Split:               "train",
			EvaluatorConfigHash: "evaluator-hash",
			MetricPolicyHash:    "policy-hash",
			PrimaryMetric:       "quality",
			MetricDirections:    map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
			EvidenceLimit:       10,
		}, hash
	}
	for _, test := range []struct {
		name   string
		mutate func(*SnapshotRequest)
	}{
		{"run", func(request *SnapshotRequest) { request.EvaluationRunID = "" }},
		{"profile", func(request *SnapshotRequest) { request.Profile = nil }},
		{"profile hash", func(request *SnapshotRequest) { request.ExpectedProfileHash = "" }},
		{"eval set", func(request *SnapshotRequest) { request.Dataset.EvalSetID = "" }},
		{"eval hash", func(request *SnapshotRequest) { request.Dataset.EvalSetHash = "" }},
		{"metrics hash", func(request *SnapshotRequest) { request.Dataset.MetricsHash = "" }},
		{"cases", func(request *SnapshotRequest) { request.Dataset.CaseIDs = nil }},
		{"metrics", func(request *SnapshotRequest) { request.Dataset.MetricNames = nil }},
		{"split", func(request *SnapshotRequest) { request.Split = "" }},
		{"evaluator", func(request *SnapshotRequest) { request.EvaluatorConfigHash = "" }},
		{"policy", func(request *SnapshotRequest) { request.MetricPolicyHash = "" }},
		{"evidence zero", func(request *SnapshotRequest) { request.EvidenceLimit = 0 }},
		{"evidence max", func(request *SnapshotRequest) { request.EvidenceLimit = 101 }},
		{"hash mismatch", func(request *SnapshotRequest) { request.ExpectedProfileHash = "other" }},
		{"empty case", func(request *SnapshotRequest) { request.Dataset.CaseIDs[0] = "" }},
		{"duplicate case", func(request *SnapshotRequest) {
			request.Dataset.CaseIDs = []string{"case-a", "case-a"}
		}},
		{"empty metric", func(request *SnapshotRequest) { request.Dataset.MetricNames[0] = "" }},
		{"primary empty", func(request *SnapshotRequest) { request.PrimaryMetric = "" }},
		{"primary absent", func(request *SnapshotRequest) { request.PrimaryMetric = "other" }},
		{"direction", func(request *SnapshotRequest) {
			request.MetricDirections["quality"] = "sideways"
		}},
		{"input hash", func(request *SnapshotRequest) {
			request.Dataset.NormalizedInputHashes["case-a"] = ""
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, hash := valid()
			test.mutate(&request)
			require.Error(t, validateSnapshotRequest(request, hash))
		})
	}
	request, hash := valid()
	require.NoError(t, validateSnapshotRequest(request, hash))
}

func TestDeltaValidationRejectsMalformedSnapshotsAndPolicies(t *testing.T) {
	valid := func() (*EvaluationSnapshot, *EvaluationSnapshot, GatePolicy) {
		before := testSnapshot("before", []string{"case-a"}, []string{"quality"})
		after := testSnapshot("after", before.Inventory.CaseIDs, before.Inventory.MetricNames)
		setSnapshotCases(before, 0.5, testCase("case-a", false, 0.5))
		setSnapshotCases(after, 0.8, testCase("case-a", true, 0.8))
		return before, after, GatePolicy{
			PrimaryMetric:    "quality",
			MetricDirections: map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
			Epsilon:          DefaultEpsilon,
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*EvaluationSnapshot, *EvaluationSnapshot, *GatePolicy)
	}{
		{"comparison handled separately", func(_, _ *EvaluationSnapshot, _ *GatePolicy) {}},
		{"primary metric", func(_, _ *EvaluationSnapshot, policy *GatePolicy) {
			policy.PrimaryMetric = ""
		}},
		{"directions", func(_, _ *EvaluationSnapshot, policy *GatePolicy) {
			policy.MetricDirections = nil
		}},
		{"epsilon", func(_, _ *EvaluationSnapshot, policy *GatePolicy) {
			policy.Epsilon = math.NaN()
		}},
		{"before status", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Status = EvaluationRunFailed
		}},
		{"run id", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Provenance.RunID = ""
		}},
		{"profile hash", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Provenance.ProfileHash = ""
		}},
		{"eval id", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Provenance.EvalSetID = ""
		}},
		{"eval hash", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Provenance.EvalSetHash = ""
		}},
		{"metrics hash", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Provenance.MetricsHash = ""
		}},
		{"split", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Provenance.Split = ""
		}},
		{"evaluator hash", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Provenance.EvaluatorConfigHash = ""
		}},
		{"policy hash", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Provenance.MetricPolicyHash = ""
		}},
		{"case inventory", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Inventory.CaseIDs = nil
		}},
		{"metric inventory", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Inventory.MetricNames = nil
		}},
		{"duplicate case", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Inventory.CaseIDs = []string{"case-a", "case-a"}
		}},
		{"case count", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Cases = nil
		}},
		{"case id", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Cases[0].CaseID = ""
		}},
		{"primary absent", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Cases[0].PrimaryMetric = "other"
		}},
		{"metric direction", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Cases[0].Metrics[0].Direction = "sideways"
		}},
		{"case score", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Cases[0].Metrics[0].Score = math.Inf(1)
		}},
		{"overall score", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.OverallScore = math.NaN()
		}},
		{"aggregate counts", func(before, _ *EvaluationSnapshot, _ *GatePolicy) {
			before.Passed = 99
		}},
		{"case order", func(_, after *EvaluationSnapshot, _ *GatePolicy) {
			after.Inventory.CaseIDs = []string{"other"}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, after, policy := valid()
			test.mutate(before, after, &policy)
			comparison := "vs_initial"
			if test.name == "comparison handled separately" {
				comparison = ""
			}
			_, err := CalculateDelta(comparison, before, after, policy)
			require.Error(t, err)
		})
	}
	_, after, policy := valid()
	_, err := CalculateDelta("vs_initial", nil, after, policy)
	require.Error(t, err)
}

func TestDeltaValidatorsRejectEveryMalformedMetricAndBinding(t *testing.T) {
	validPolicy := func() GatePolicy {
		return GatePolicy{
			PrimaryMetric: "quality",
			MetricDirections: map[string]ScoreDirection{
				"quality": ScoreHigherIsBetter,
				"safety":  ScoreHigherIsBetter,
			},
			Epsilon: DefaultEpsilon,
		}
	}
	for _, mutate := range []func(*GatePolicy){
		func(policy *GatePolicy) { policy.PrimaryMetric = "missing" },
		func(policy *GatePolicy) { policy.MetricDirections["quality"] = "sideways" },
		func(policy *GatePolicy) {
			policy.MetricDirections = map[string]ScoreDirection{
				"quality": ScoreHigherIsBetter,
				"":        ScoreHigherIsBetter,
			}
		},
		func(policy *GatePolicy) { policy.MetricDirections["safety"] = "sideways" },
	} {
		policy := validPolicy()
		mutate(&policy)
		require.Error(t, validateDeltaPolicy(policy))
	}

	validCase := func() CaseResult {
		return testCase("case-a", true, 0.8, 0.9)
	}
	inventory := []string{"quality", "safety"}
	for _, test := range []struct {
		name   string
		mutate func(*CaseResult)
	}{
		{"metric count", func(item *CaseResult) { item.Metrics = item.Metrics[:1] }},
		{"empty metric", func(item *CaseResult) { item.Metrics[0].MetricName = "" }},
		{"duplicate metric", func(item *CaseResult) { item.Metrics[1].MetricName = "quality" }},
		{"unexpected metric", func(item *CaseResult) { item.Metrics[1].MetricName = "other" }},
		{"nonfinite threshold", func(item *CaseResult) { item.Metrics[0].Threshold = math.NaN() }},
		{"invalid status", func(item *CaseResult) { item.Metrics[0].Status = "unknown" }},
		{"status mismatch", func(item *CaseResult) { item.Metrics[0].Passed = false }},
		{"invalid direction", func(item *CaseResult) { item.Metrics[0].Direction = "sideways" }},
		{"policy direction mismatch", func(item *CaseResult) {
			item.Metrics[0].Direction = ScoreLowerIsBetter
		}},
		{"nonfinite rubric", func(item *CaseResult) {
			item.Metrics[0].RubricScores = []RubricScore{{ID: "r", Score: math.Inf(1)}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := validCase()
			test.mutate(&item)
			require.Error(t, validateCaseMetrics("snapshot", item, inventory, validPolicy()))
		})
	}
	require.NoError(t, validateCaseMetrics("snapshot", validCase(), inventory, validPolicy()))

	validSnapshot := func() *EvaluationSnapshot {
		snapshot := testSnapshot("profile", []string{"case-a"}, inventory)
		setSnapshotCases(snapshot, 0.8, validCase())
		return snapshot
	}
	for _, test := range []struct {
		name   string
		mutate func(*EvaluationSnapshot, *GatePolicy)
	}{
		{"primary missing from inventory", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Inventory.MetricNames = []string{"safety"}
			snapshot.Cases[0].Metrics = snapshot.Cases[0].Metrics[1:]
		}},
		{"metric missing policy direction", func(snapshot *EvaluationSnapshot, policy *GatePolicy) {
			delete(policy.MetricDirections, "safety")
		}},
		{"case eval set mismatch", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Cases[0].EvalSetID = "other"
		}},
		{"missing expected case", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Cases = nil
		}},
		{"duplicate expected case", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Cases = append(snapshot.Cases, snapshot.Cases[0])
		}},
		{"extra case", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			extra := validCase()
			extra.CaseID = "case-b"
			snapshot.Cases = append(snapshot.Cases, extra)
		}},
		{"case status", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Cases[0].Status = "unknown"
		}},
		{"case pass binding", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Cases[0].Passed = false
		}},
		{"case primary binding", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Cases[0].PrimaryMetric = "safety"
		}},
		{"case metrics", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Cases[0].Metrics = nil
		}},
		{"pass counts", func(snapshot *EvaluationSnapshot, _ *GatePolicy) {
			snapshot.Passed = 0
		}},
	} {
		t.Run("snapshot/"+test.name, func(t *testing.T) {
			snapshot := validSnapshot()
			policy := validPolicy()
			test.mutate(snapshot, &policy)
			_, err := validateComparisonSnapshot("snapshot", snapshot, policy)
			require.Error(t, err)
		})
	}
	_, err := validateComparisonSnapshot("snapshot", validSnapshot(), validPolicy())
	require.NoError(t, err)

	for _, test := range []struct {
		name   string
		mutate func(*EvaluationSnapshot)
	}{
		{"eval set id", func(after *EvaluationSnapshot) { after.Provenance.EvalSetID = "other" }},
		{"eval set hash", func(after *EvaluationSnapshot) { after.Provenance.EvalSetHash = "other" }},
		{"metrics hash", func(after *EvaluationSnapshot) { after.Provenance.MetricsHash = "other" }},
		{"split", func(after *EvaluationSnapshot) { after.Provenance.Split = "other" }},
		{"evaluator hash", func(after *EvaluationSnapshot) {
			after.Provenance.EvaluatorConfigHash = "other"
		}},
		{"policy hash", func(after *EvaluationSnapshot) { after.Provenance.MetricPolicyHash = "other" }},
		{"seed", func(after *EvaluationSnapshot) { after.Provenance.Seed++ }},
		{"case inventory", func(after *EvaluationSnapshot) {
			after.Inventory.CaseIDs = []string{"case-b"}
		}},
		{"metric inventory", func(after *EvaluationSnapshot) {
			after.Inventory.MetricNames = []string{"quality"}
		}},
	} {
		t.Run("compatibility/"+test.name, func(t *testing.T) {
			before := validSnapshot()
			after := validSnapshot()
			test.mutate(after)
			require.Error(t, validateCompatibleSnapshots(before, after))
		})
	}
	require.NoError(t, validateCompatibleSnapshots(validSnapshot(), validSnapshot()))
	require.False(t, sameStringSet([]string{"a"}, []string{"a", "b"}))
	require.False(t, sameStringSet([]string{"a"}, []string{"b"}))
	require.True(t, sameStringSet([]string{"a", "b"}, []string{"b", "a"}))

	for _, field := range []string{"critical", "hard", "expected no tools", "threshold"} {
		before := validSnapshot()
		after := validSnapshot()
		switch field {
		case "critical":
			after.Cases[0].Critical = true
		case "hard":
			after.Cases[0].HardFailure = true
		case "expected no tools":
			after.Cases[0].ExpectNoTools = true
		case "threshold":
			after.Cases[0].Metrics[0].Threshold++
		}
		_, err := CalculateDelta("comparison", before, after, validPolicy())
		require.Error(t, err)
	}
	beforeCase := validCase()
	afterCase := validCase()
	afterCase.Metrics[0].Direction = ScoreLowerIsBetter
	_, err = calculateCaseDelta(
		beforeCase,
		afterCase,
		inventory,
		validPolicy(),
		DefaultEpsilon,
	)
	require.Error(t, err)
}

func TestReleaseGateValidatorsRejectEveryMalformedDeltaAndResource(t *testing.T) {
	valid := func() (GatePolicy, DeltaSummary) {
		report := testReport(t)
		return report.ResolvedConfig.Gate, report.Candidates[0].Deltas.VsReleased
	}
	for _, test := range []struct {
		name   string
		mutate func(*GatePolicy, *DeltaSummary)
	}{
		{"case identity", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].CaseID = ""
		}},
		{"duplicate case", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases = append(delta.Cases, delta.Cases[0])
		}},
		{"case status", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].AfterStatus = "unknown"
		}},
		{"case pass binding", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].AfterPassed = false
		}},
		{"metric inventory count", func(policy *GatePolicy, _ *DeltaSummary) {
			policy.MetricDirections["safety"] = ScoreHigherIsBetter
		}},
		{"empty metric", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].Metrics[0].MetricName = ""
		}},
		{"duplicate metric", func(policy *GatePolicy, delta *DeltaSummary) {
			policy.MetricDirections["safety"] = ScoreHigherIsBetter
			delta.Cases[0].Metrics = append(delta.Cases[0].Metrics, delta.Cases[0].Metrics[0])
		}},
		{"metric outside policy", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].Metrics[0].MetricName = "other"
		}},
		{"metric nonfinite", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].Metrics[0].BeforeScore = math.NaN()
		}},
		{"metric invalid direction", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].Metrics[0].Direction = "sideways"
		}},
		{"metric invalid status", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].Metrics[0].BeforeStatus = "unknown"
		}},
		{"metric direction mismatch", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].Metrics[0].Direction = ScoreLowerIsBetter
		}},
		{"metric delta mismatch", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].Metrics[0].Delta++
		}},
		{"primary kind mismatch", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.Cases[0].PrimaryKind = ChangeUnchanged
		}},
		{"before mean mismatch", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.BeforeOverallScore++
		}},
		{"after mean mismatch", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.AfterOverallScore++
		}},
		{"aggregate counts", func(_ *GatePolicy, delta *DeltaSummary) {
			delta.NewlyPassing++
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy, delta := valid()
			test.mutate(&policy, &delta)
			require.Error(t, validateDeltaSummary(delta, policy, DefaultEpsilon))
		})
	}
	policy, delta := valid()
	require.NoError(t, validateDeltaSummary(delta, policy, DefaultEpsilon))

	invalidResources := []ResourceUsage{
		{ModelCalls: Count{Available: true, Value: -1}},
		{InputTokens: Count{Available: true, Value: -1}},
		{OutputTokens: Count{Available: true, Value: -1}},
		{LatencyMS: Count{Available: true, Value: -1}},
		{ModelCalls: Count{Value: 1}},
		{InputTokens: Count{Value: 1}},
		{OutputTokens: Count{Value: 1}},
		{LatencyMS: Count{Value: 1}},
		{MonetaryCost: Amount{Available: true, Value: math.NaN()}},
		{MonetaryCost: Amount{Available: true, Value: -1}},
		{MonetaryCost: Amount{Value: 1}},
	}
	for _, usage := range invalidResources {
		require.NotEmpty(t, validateResourceUsage(usage))
	}
	require.Empty(t, validateResourceUsage(ResourceUsage{
		ModelCalls:   Count{Available: true, Value: 1},
		InputTokens:  Count{Available: true, Value: 1},
		OutputTokens: Count{Available: true, Value: 1},
		LatencyMS:    Count{Available: true, Value: 1},
		MonetaryCost: Amount{Available: true, Value: 1, Unit: "USD"},
	}))

	require.True(t, caseHasRegression(CaseDelta{PrimaryKind: ChangeNewlyFailing}, DefaultEpsilon))
	require.True(t, caseHasRegression(CaseDelta{PrimaryKind: ChangeRegressed}, DefaultEpsilon))
	require.True(t, caseHasRegression(CaseDelta{
		PrimaryKind: ChangeUnchanged,
		Metrics: []MetricDelta{{
			Delta:     -1,
			Direction: ScoreHigherIsBetter,
		}},
	}, DefaultEpsilon))
	require.False(t, caseHasRegression(CaseDelta{
		PrimaryKind: ChangeImproved,
		Metrics: []MetricDelta{{
			Delta:     1,
			Direction: ScoreHigherIsBetter,
		}},
	}, DefaultEpsilon))

	policy, delta = valid()
	for _, mutate := range []func(*GatePolicy, *DeltaSummary, *ResourceUsage){
		func(policy *GatePolicy, _ *DeltaSummary, _ *ResourceUsage) {
			policy.PrimaryMetric = ""
		},
		func(policy *GatePolicy, _ *DeltaSummary, _ *ResourceUsage) {
			policy.MinValidationGain = math.NaN()
		},
		func(policy *GatePolicy, _ *DeltaSummary, _ *ResourceUsage) {
			policy.ModelCallStopThreshold = -1
		},
		func(_ *GatePolicy, delta *DeltaSummary, _ *ResourceUsage) {
			delta.Comparison = "vs_initial"
		},
		func(_ *GatePolicy, delta *DeltaSummary, _ *ResourceUsage) {
			delta.BeforeProfileHash = ""
		},
		func(_ *GatePolicy, delta *DeltaSummary, _ *ResourceUsage) {
			delta.ScoreDelta = math.Inf(1)
		},
		func(_ *GatePolicy, delta *DeltaSummary, _ *ResourceUsage) {
			delta.ScoreDelta++
		},
		func(_ *GatePolicy, delta *DeltaSummary, _ *ResourceUsage) {
			delta.Cases = nil
		},
		func(policy *GatePolicy, _ *DeltaSummary, usage *ResourceUsage) {
			policy.ModelCallStopThreshold = 1
			usage.ModelCalls = Count{}
		},
	} {
		policy, delta = valid()
		usage := ResourceUsage{}
		mutate(&policy, &delta, &usage)
		require.NotEmpty(t, validateReleaseInputs(policy, delta, usage, DefaultEpsilon))
	}
}

func TestAnalysisBoundaryHelpersCoverLimitsAndDeduplication(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), -1} {
		_, err := comparisonEpsilon(value)
		require.Error(t, err)
	}
	epsilon, err := comparisonEpsilon(0)
	require.NoError(t, err)
	require.Equal(t, DefaultEpsilon, epsilon)
	epsilon, err = comparisonEpsilon(0.25)
	require.NoError(t, err)
	require.Equal(t, 0.25, epsilon)

	full := make([]EvidenceReference, maxEvidenceReferences)
	require.Len(t, appendEvidence(full, makeEvidence("extra", "trace", "ignored")), maxEvidenceReferences)
	items := appendEvidence(nil,
		EvidenceReference{},
		makeEvidence("id", "trace", "summary"),
		makeEvidence("id", "trace", "summary"),
	)
	require.Len(t, items, 1)
	for index := 1; index < maxEvidenceReferences+2; index++ {
		items = appendEvidence(items, makeEvidence(
			fmt.Sprintf("id-%d", index),
			"trace",
			"summary",
		))
	}
	require.Len(t, items, maxEvidenceReferences)
}

func TestPipelineAndReportUtilityBranches(t *testing.T) {
	options := pipelineOptions{}
	var observed []ResourceEntry
	optionSet := []PipelineOption{
		WithTeacher(nil),
		WithJudge(nil),
		WithEngineEvaluationOptions(promptiterengine.EvaluationOptions{EvalCaseParallelism: 2}),
		WithEngineBackwardOptions(promptiterengine.BackwardOptions{}),
		WithEngineAggregationOptions(promptiterengine.AggregationOptions{}),
		WithEngineOptimizerOptions(promptiterengine.OptimizerOptions{}),
		WithEngineObserver(nil),
		WithResourceMeter(NewUsageMeter()),
		WithResourceObserver(func(entry ResourceEntry) { observed = append(observed, entry) }),
	}
	for _, option := range optionSet {
		option(&options)
	}
	require.Equal(t, 2, options.evaluationOptions.EvalCaseParallelism)
	require.NotNil(t, options.resourceMeter)
	require.NotNil(t, options.resourceObserver)

	structure := pipelineTestStructure()
	require.Error(t, validateProfileAndTarget(nil, pipelineProfile("prompt"), pipelineTestSurfaceID))
	wrongProfile := pipelineProfile("prompt")
	wrongProfile.StructureID = "other"
	require.Error(t, validateProfileAndTarget(structure, wrongProfile, pipelineTestSurfaceID))
	require.Error(t, validateProfileAndTarget(structure, pipelineProfile("prompt"), "missing"))
	require.NoError(t, validateProfileAndTarget(structure, pipelineProfile("prompt"), pipelineTestSurfaceID))

	require.Error(t, func() error {
		_, err := bindProfileToStructure(nil, structure)
		return err
	}())
	require.Error(t, func() error {
		_, err := bindProfileToStructure(pipelineProfile("prompt"), nil)
		return err
	}())
	bound, err := bindProfileToStructure(&promptiter.Profile{}, structure)
	require.NoError(t, err)
	require.Equal(t, structure.StructureID, bound.StructureID)

	train := pipelineRunConfig().Train
	all, err := resolveInternalValidation(train, PromptIterPolicy{
		InternalValidationStrategy: internalValidationTrainAll,
	})
	require.NoError(t, err)
	require.Equal(t, train.CaseIDs, all)
	for _, policy := range []PromptIterPolicy{
		{InternalValidationStrategy: internalValidationTrainCaseIDs},
		{InternalValidationStrategy: internalValidationTrainAll, InternalValidationCaseIDs: []string{"train-a"}},
		{InternalValidationStrategy: "other"},
		{InternalValidationStrategy: internalValidationTrainCaseIDs, InternalValidationCaseIDs: []string{"other"}},
		{InternalValidationStrategy: internalValidationTrainCaseIDs, InternalValidationCaseIDs: []string{"train-a", "train-a"}},
	} {
		_, err := resolveInternalValidation(train, policy)
		require.Error(t, err)
	}

	require.Nil(t, adaptPatches(nil))
	require.Empty(t, patchReasons(nil))
	require.Empty(t, patchReasons(&promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{Reason: " "}}}))
	require.Equal(t, "reason", patchReasons(&promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{Reason: "reason"}}}))
	require.Equal(t, "unavailable", countText(Count{}))
	require.Equal(t, "3", countText(Count{Available: true, Value: 3}))
	require.Equal(t, "unavailable", amountText(Amount{}))
	require.Equal(t, "1.500000", amountText(Amount{Available: true, Value: 1.5}))
	require.Equal(t, "1.500000 USD", amountText(Amount{Available: true, Value: 1.5, Unit: "USD"}))

	var out strings.Builder
	writeProfile(&out, "missing", nil)
	writeSnapshot(&out, "missing", nil)
	writeErrors(&out, []string{"error"})
	writeStringMap(&out, nil)
	writeResourceLedger(&out, ResourceLedger{})
	writeJSONBlock(&out, make(chan int))
	require.Contains(t, out.String(), "unable to render")
	require.Contains(t, compactJSON(make(chan int)), "unrenderable")

	for _, status := range []PipelineStatus{PipelineSucceeded, PipelineRunFailed, PipelineModelCallThresholdStopped} {
		require.True(t, validPipelineStatus(status))
	}
	require.False(t, validPipelineStatus("other"))
	for _, reason := range []StopReason{
		StopMaxRounds, StopModelCallThresholdReached, StopNoCandidate,
		StopNecessaryRunFailed, StopRepeatedFingerprint, StopTrainingFailuresFixed,
	} {
		require.True(t, validStopReason(reason))
	}
	require.False(t, validStopReason("other"))
	for _, status := range []DecisionStatus{DecisionAccepted, DecisionRejected, DecisionNotEvaluable} {
		require.True(t, validDecisionStatus(status))
	}
	require.False(t, validDecisionStatus("other"))
	for _, status := range []EvaluationStatus{
		EvaluationCompleted, EvaluationNotEvaluable, EvaluationRunFailed,
	} {
		require.True(t, validEvaluationStatus(status))
	}
	require.False(t, validEvaluationStatus("other"))

	report := testReport(t)
	dir := t.TempDir()
	require.NoError(t, Write(report, dir))
	require.FileExists(t, filepath.Join(dir, report.ResolvedConfig.Output.JSON))
	require.Error(t, validateRenderedJSON([]byte(`{}`), report))
	require.Error(t, validateRenderedMarkdown([]byte("missing"), report))
	require.Error(t, validateArtifactPair([]byte(`{}`), []byte("missing"), report))
}

func TestMarkdownWritersRetainOptionalAuditEvidence(t *testing.T) {
	report := testReport(t)
	snapshot := report.BaselineTrain
	snapshot.Error = "snapshot warning"
	snapshot.Resources = ResourceUsage{
		ModelCalls:   Count{Available: true, Value: 2},
		InputTokens:  Count{Available: true, Value: 10},
		OutputTokens: Count{Available: true, Value: 5},
		LatencyMS:    Count{Available: true, Value: 20},
		MonetaryCost: Amount{Available: true, Value: 0.25, Unit: "USD"},
	}
	result := &snapshot.Cases[0]
	result.ExpectedFacts = []string{"fact-a", "fact-b"}
	result.Error = "case warning"
	result.ExpectStructured = true
	result.StructuredOutput = `{"answer":"observed"}`
	result.ExpectedRoute = "lookup"
	result.ExpectedTools = []ToolCall{{
		Sequence:  1,
		Name:      "lookup",
		Arguments: map[string]any{"query": "weather"},
		Result:    map[string]any{"temperature": 25},
	}}
	result.Metrics[0].RubricScores = []RubricScore{
		{ID: "accuracy", Score: 0.4},
		{ID: "format", Score: 0.5, Reason: "wrong shape"},
	}
	snapshot.Attributions = []FailureAttribution{{
		EvalSetID:           snapshot.Provenance.EvalSetID,
		EvalCaseID:          result.CaseID,
		MetricName:          result.PrimaryMetric,
		PrimaryCategory:     FailureResponseMismatch,
		Reason:              "observed answer differs",
		Evidence:            []EvidenceReference{{ID: "response", Kind: "final_response", Summary: "mismatch"}},
		Severity:            FailureSeverityP2,
		Confidence:          0.9,
		EvidenceSufficiency: EvidenceSufficient,
		EvaluationRunID:     snapshot.Provenance.RunID,
		ProfileHash:         snapshot.Provenance.ProfileHash,
	}}

	var out strings.Builder
	writeSnapshot(&out, "snapshot", snapshot)
	writeCandidate(&out, &CandidateReport{
		Round:              2,
		ID:                 "incomplete",
		Status:             EvaluationNotEvaluable,
		SearchParentHash:   "search",
		ReleasedParentHash: "released",
		SearchDecision:     notEvaluableDecision("search unavailable"),
		ReleaseDecision:    notEvaluableDecision("release unavailable"),
		Errors:             []string{"candidate error"},
		Transition: StateTransition{
			SearchBefore:   "search",
			SearchAfter:    "search",
			ReleasedBefore: "released",
			ReleasedAfter:  "released",
			Explanation:    "unchanged",
		},
		Resources: ResourceLedger{
			Entries: []ResourceEntry{{
				Stage:       "evaluation",
				Round:       2,
				Split:       "train",
				ProfileHash: "candidate",
				Usage:       snapshot.Resources,
				Failed:      true,
			}},
			Cumulative: snapshot.Resources,
		},
	})
	text := out.String()
	for _, expected := range []string{
		"snapshot warning",
		"Expected facts",
		"case warning",
		"Structured output",
		"Expected tools",
		"accuracy=0.400000",
		"wrong shape",
		"Attribution:",
		"candidate error",
		"| evaluation | 2 | train | candidate |",
	} {
		require.Contains(t, text, expected)
	}
	require.Empty(t, compactJSON(nil))
	require.Empty(t, redactAndBoundText("value", 0))
}

func TestReportRenderingAndPublishingRejectsBoundaryInputs(t *testing.T) {
	report := testReport(t)
	report.Candidates = nil
	report.ReleasedProfile = withProfileRole(report.InitialProfile, ProfileReleased)
	report.FinalDecision = Decision{Status: DecisionRejected, Reasons: []string{"no candidate"}}
	markdown, err := RenderMarkdown(report)
	require.NoError(t, err)
	require.Contains(t, string(markdown), "_No candidate was evaluable._")

	require.Error(t, Write(report, ""))
	require.Error(t, WriteArtifacts(report, "", "report.md"))
	require.Error(t, WriteArtifacts(report, "report.json", ""))
	require.Error(t, WriteArtifacts(report, "same", "same"))

	unserializable := testReport(t)
	unserializable.Status = PipelineRunFailed
	unserializable.StopReason = StopNecessaryRunFailed
	unserializable.Runtime.Model = map[string]any{"unsupported": make(chan int)}
	_, err = sanitizedReport(unserializable)
	require.Error(t, err)
}

func TestResourceArithmeticAndReadOnlyManagers(t *testing.T) {
	var nilMeter *UsageMeter
	nilMeter.Record(ResourceUsage{})
	require.Equal(t, ResourceUsage{}, nilMeter.Snapshot())

	meter := NewUsageMeter()
	first := ResourceUsage{
		ModelCalls:   Count{Available: true, Value: 1},
		InputTokens:  Count{Available: true, Value: 2},
		OutputTokens: Count{Available: true, Value: 3},
		LatencyMS:    Count{Available: true, Value: 4},
		MonetaryCost: Amount{Available: true, Value: 1, Unit: "USD"},
	}
	meter.Record(first)
	meter.Record(first)
	require.Equal(t, int64(2), meter.Snapshot().ModelCalls.Value)

	require.Equal(t, Count{}, addCount(Count{}, Count{Available: true}))
	require.Equal(t, Amount{}, addAmount(Amount{}, Amount{Available: true}))
	require.Equal(t, Amount{}, addAmount(
		Amount{Available: true, Unit: "USD"},
		Amount{Available: true, Unit: "EUR"},
	))
	require.Equal(t, "USD", addAmount(
		Amount{Available: true, Value: 1},
		Amount{Available: true, Value: 2, Unit: "USD"},
	).Unit)
	require.Equal(t, Count{}, subtractCount(Count{Available: true, Value: 2}, Count{Available: true, Value: 1}))
	require.Equal(t, Amount{}, subtractAmount(
		Amount{Available: true, Value: 2, Unit: "USD"},
		Amount{Available: true, Value: 1, Unit: "USD"},
	))
	require.Equal(t, Amount{}, subtractAmount(
		Amount{Available: true, Unit: "USD"},
		Amount{Available: true, Unit: "EUR"},
	))
	require.Equal(t, "USD", subtractAmount(
		Amount{Available: true, Value: 1, Unit: "USD"},
		Amount{Available: true, Value: 2},
	).Unit)

	var observed []ResourceEntry
	var ledger ResourceLedger
	entry := ResourceEntry{Stage: "one", Usage: first}
	appendResourceEntry(nil, entry, nil)
	appendResourceEntry(&ledger, entry, func(value ResourceEntry) {
		observed = append(observed, value)
	})
	appendResourceEntry(&ledger, entry, nil)
	require.Len(t, observed, 1)
	require.Len(t, ledger.Entries, 2)

	evalManager := &frozenEvalSetManager{}
	_, err := evalManager.Create(context.Background(), "", "")
	require.ErrorIs(t, err, errFrozenInputsReadOnly)
	require.ErrorIs(t, evalManager.Delete(context.Background(), "", ""), errFrozenInputsReadOnly)
	require.ErrorIs(t, evalManager.AddCase(context.Background(), "", "", &evalset.EvalCase{}), errFrozenInputsReadOnly)
	require.ErrorIs(t, evalManager.UpdateCase(context.Background(), "", "", &evalset.EvalCase{}), errFrozenInputsReadOnly)
	require.ErrorIs(t, evalManager.DeleteCase(context.Background(), "", "", ""), errFrozenInputsReadOnly)

	metricManager := &frozenMetricManager{}
	require.ErrorIs(t, metricManager.Add(context.Background(), "", "", &metric.EvalMetric{}), errFrozenInputsReadOnly)
	require.ErrorIs(t, metricManager.Delete(context.Background(), "", "", ""), errFrozenInputsReadOnly)
	require.ErrorIs(t, metricManager.Update(context.Background(), "", "", &metric.EvalMetric{}), errFrozenInputsReadOnly)
}

func TestReportTopLevelValidationRejectsInvalidLifecycleFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Report)
	}{
		{"schema", func(report *Report) { report.SchemaVersion = "2.0" }},
		{"report id", func(report *Report) { report.ReportID = "" }},
		{"run id", func(report *Report) { report.RunID = "" }},
		{"generated", func(report *Report) { report.GeneratedAt = time.Time{} }},
		{"timezone", func(report *Report) {
			report.GeneratedAt = report.GeneratedAt.In(time.FixedZone("offset", 3600))
		}},
		{"status", func(report *Report) { report.Status = "other" }},
		{"stop reason", func(report *Report) { report.StopReason = "other" }},
		{"decision", func(report *Report) { report.FinalDecision.Status = "other" }},
		{"candidate status", func(report *Report) { report.Candidates[0].Status = "other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := testReport(t)
			test.mutate(report)
			_, err := RenderJSON(report)
			require.Error(t, err)
		})
	}
	_, err := RenderJSON(nil)
	require.Error(t, err)
}

func TestPromptIterResultValidationRejectsMalformedNativeLineage(t *testing.T) {
	valid := func() (*promptiterengine.RunResult, *promptiterengine.RunRequest, *astructure.Snapshot) {
		return pipelineCandidateRunResult(), &promptiterengine.RunRequest{
			InitialProfile:   pipelineProfile("initial prompt"),
			TargetSurfaceIDs: []string{pipelineTestSurfaceID},
		}, pipelineTestStructure()
	}
	for _, test := range []struct {
		name   string
		mutate func(*promptiterengine.RunResult, *promptiterengine.RunRequest, *astructure.Snapshot)
	}{
		{"status", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Status = "failed"
		}},
		{"round count", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.CurrentRound = 2
		}},
		{"baseline", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.BaselineValidation = nil
		}},
		{"rounds", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds = nil
		}},
		{"round number", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].Round = 2
		}},
		{"input", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].InputProfile = nil
		}},
		{"train", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].Train = nil
		}},
		{"validation", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].Validation = nil
		}},
		{"acceptance", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].Acceptance = nil
		}},
		{"acceptance score", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].Acceptance.ScoreDelta = math.NaN()
		}},
		{"acceptance reason", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].Acceptance.Reason = ""
		}},
		{"patches", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].Patches = nil
		}},
		{"output", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].OutputProfile = nil
		}},
		{"accepted", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.AcceptedProfile = nil
		}},
		{"input mismatch", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].InputProfile = pipelineProfile("other")
		}},
		{"patch outside target", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].Patches.Patches[0].SurfaceID = "other"
		}},
		{"output mismatch", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.Rounds[0].OutputProfile = pipelineProfile("other")
		}},
		{"accepted mismatch", func(result *promptiterengine.RunResult, _ *promptiterengine.RunRequest, _ *astructure.Snapshot) {
			result.AcceptedProfile = pipelineProfile("other")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, request, structure := valid()
			test.mutate(result, request, structure)
			require.Error(t, validatePromptIterResult(result, request, structure))
		})
	}
	result, request, structure := valid()
	require.NoError(t, validatePromptIterResult(result, request, structure))
	require.Error(t, validatePromptIterResult(nil, request, structure))
	require.Error(t, validatePromptIterResult(result, nil, structure))
	require.Error(t, validatePromptIterResult(result, request, nil))
}

func TestAttributionInputValidationRejectsUnboundEvidence(t *testing.T) {
	valid := func() AttributionInput {
		snapshot := testSnapshot(
			"profile",
			[]string{"case-a"},
			[]string{"quality"},
		)
		evalCase := testCase("case-a", false, 0.2)
		setSnapshotCases(snapshot, 0.2, evalCase)
		return AttributionInput{
			Snapshot: snapshot,
			Case:     snapshot.Cases[0],
			Metric:   snapshot.Cases[0].Metrics[0],
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*AttributionInput)
	}{
		{"snapshot status", func(input *AttributionInput) {
			input.Snapshot.Status = EvaluationRunFailed
		}},
		{"run id", func(input *AttributionInput) {
			input.Snapshot.Provenance.RunID = ""
		}},
		{"profile hash", func(input *AttributionInput) {
			input.Snapshot.Provenance.ProfileHash = ""
		}},
		{"eval set id", func(input *AttributionInput) {
			input.Snapshot.Provenance.EvalSetID = ""
		}},
		{"case eval set", func(input *AttributionInput) {
			input.Case.EvalSetID = "other"
		}},
		{"case id", func(input *AttributionInput) {
			input.Case.CaseID = ""
		}},
		{"metric name", func(input *AttributionInput) {
			input.Metric.MetricName = ""
		}},
		{"metric score", func(input *AttributionInput) {
			input.Metric.Score = math.NaN()
		}},
		{"case status", func(input *AttributionInput) {
			input.Case.Status = "not_evaluated"
		}},
		{"case pass mismatch", func(input *AttributionInput) {
			input.Case.Passed = true
		}},
		{"metric status", func(input *AttributionInput) {
			input.Metric.Status = "not_evaluated"
		}},
		{"metric pass mismatch", func(input *AttributionInput) {
			input.Metric.Passed = true
		}},
		{"case inventory", func(input *AttributionInput) {
			input.Snapshot.Inventory.CaseIDs = []string{"other"}
		}},
		{"metric inventory", func(input *AttributionInput) {
			input.Snapshot.Inventory.MetricNames = []string{"other"}
		}},
		{"duplicate case evidence", func(input *AttributionInput) {
			input.Snapshot.Cases = append(input.Snapshot.Cases, input.Case)
		}},
		{"missing case evidence", func(input *AttributionInput) {
			input.Snapshot.Cases = nil
		}},
		{"case evidence mismatch", func(input *AttributionInput) {
			input.Case.FinalResponse = "different"
		}},
		{"duplicate metric evidence", func(input *AttributionInput) {
			input.Case.Metrics = append(input.Case.Metrics, input.Metric)
			input.Snapshot.Cases[0] = input.Case
		}},
		{"missing metric evidence", func(input *AttributionInput) {
			input.Case.Metrics = nil
			input.Snapshot.Cases[0] = input.Case
		}},
		{"metric evidence mismatch", func(input *AttributionInput) {
			input.Metric.Reason = "different"
		}},
		{"rubric score", func(input *AttributionInput) {
			input.Metric.RubricScores = []RubricScore{{ID: "rubric", Score: math.Inf(1)}}
			input.Case.Metrics[0] = input.Metric
			input.Snapshot.Cases[0] = input.Case
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := valid()
			test.mutate(&input)
			result := AttributeFailure(input)
			require.NotEmpty(t, result.Reason)
			require.NotEmpty(t, result.Evidence)
		})
	}
	result := AttributeFailure(AttributionInput{})
	require.Equal(t, FailureInsufficient, result.PrimaryCategory)

	for _, evalCase := range []CaseResult{
		{HardFailure: true},
		{Critical: true},
		{},
	} {
		require.NotEmpty(t, failureSeverity(evalCase))
	}
	for _, confidence := range []float64{-1, 2} {
		finished := finishAttribution(FailureAttribution{Confidence: confidence})
		require.GreaterOrEqual(t, finished.Confidence, float64(0))
		require.LessOrEqual(t, finished.Confidence, float64(1))
		require.NotEmpty(t, finished.Reason)
		require.NotEmpty(t, finished.Evidence)
	}
}

func TestJSONLikeNormalizationCoversSupportedGoShapes(t *testing.T) {
	type embedded struct {
		Embedded float32 `json:"embedded"`
	}
	type payload struct {
		embedded
		Value   float32            `json:"value"`
		Ignored float64            `json:"-"`
		Map     map[int]float32    `json:"map"`
		Slice   []float64          `json:"slice"`
		Array   [1]float32         `json:"array"`
		Pointer *float64           `json:"pointer"`
		Nested  map[string]float64 `json:"nested"`
	}
	pointer := 1.5
	value := payload{
		embedded: embedded{Embedded: 1.25},
		Value:    1.5,
		Map:      map[int]float32{1: 2.5},
		Slice:    []float64{3.5},
		Array:    [1]float32{4.5},
		Pointer:  &pointer,
		Nested:   map[string]float64{"x": 5.5},
	}
	normalized, err := normalizeJSONLike(value)
	require.NoError(t, err)
	require.IsType(t, map[string]any{}, normalized)
	var nilFloatPointer *float64
	require.Equal(t, "normalized", restoreFloatingValues(
		reflect.ValueOf(nilFloatPointer),
		"normalized",
	))
	require.Equal(t, "normalized", restoreFloatingValues(reflect.Value{}, "normalized"))
	require.Equal(t, "normalized", restoreFloatingValues(
		reflect.ValueOf(map[string]float64{"a": 1}),
		"normalized",
	))
	require.Equal(t, "normalized", restoreFloatingValues(
		reflect.ValueOf([]float64{1}),
		"normalized",
	))
	require.Equal(t, "normalized", restoreFloatingValues(
		reflect.ValueOf(struct{ Value float64 }{Value: 1}),
		"normalized",
	))
	restored := restoreFloatingValues(
		reflect.ValueOf(map[bool]float64{true: 1}),
		map[string]any{"true": json.Number("1")},
	)
	require.Equal(t, json.Number("1"), restored.(map[string]any)["true"])
	nilKey := (*string)(nil)
	_, ok := reflectedJSONMapKey(reflect.ValueOf(nilKey))
	require.False(t, ok)
	_, ok = reflectedJSONMapKey(reflect.Value{})
	require.False(t, ok)
	_, ok = reflectedJSONMapKey(reflect.ValueOf(true))
	require.False(t, ok)

	for _, item := range []any{
		nil,
		json.RawMessage(`{"a":1}`),
		[]byte(`[1,2]`),
		`{"a":1}`,
		"plain",
		json.Number("1.25"),
		float64(1.25),
		float32(1.25),
		int(1),
		int8(1),
		int16(1),
		int32(1),
		int64(1),
		uint(1),
		uint8(1),
		uint16(1),
		uint32(1),
		uint64(1),
		map[string]any{"value": float32(1.25)},
		[]any{float32(1.25)},
	} {
		_, err := normalizeJSONLike(item)
		require.NoError(t, err)
	}
	for _, item := range []any{
		json.Number("nope"),
		math.NaN(),
		float32(math.Inf(1)),
		map[string]any{"bad": math.NaN()},
		[]any{math.Inf(1)},
		make(chan int),
	} {
		_, err := normalizeJSONLike(item)
		require.Error(t, err)
	}

	require.Equal(t, "1", func() string {
		value, ok := reflectedJSONMapKey(reflect.ValueOf(1))
		require.True(t, ok)
		return value
	}())
	require.Equal(t, "1", func() string {
		value, ok := reflectedJSONMapKey(reflect.ValueOf(uint(1)))
		require.True(t, ok)
		return value
	}())
	require.Equal(t, "key", func() string {
		value, ok := reflectedJSONMapKey(reflect.ValueOf("key"))
		require.True(t, ok)
		return value
	}())
	_, ok = reflectedJSONMapKey(reflect.ValueOf(1.5))
	require.False(t, ok)
	var nilPointer *string
	_, ok = reflectedJSONMapKey(reflect.ValueOf(nilPointer))
	require.False(t, ok)

	_, err = decodeJSONValue([]byte(`{} {}`))
	require.Error(t, err)
	_, err = decodeJSONValue([]byte(`{`))
	require.Error(t, err)

	for _, item := range []any{
		json.Number("1"),
		float64(1),
		float32(1),
		int(1),
		int8(1),
		int16(1),
		int32(1),
		int64(1),
		uint(1),
		uint8(1),
		uint16(1),
		uint32(1),
		uint64(1),
	} {
		number, ok := comparableNumberValue(item)
		require.True(t, ok)
		_, ok = number.rat()
		require.True(t, ok)
	}
	_, ok = comparableNumberValue("not-number")
	require.False(t, ok)
	_, ok = exactInteger("not-integer")
	require.False(t, ok)
	_, ok = (comparableNumber{}).rat()
	require.False(t, ok)

	require.True(t, equalNormalizedValue(
		map[string]any{"a": []any{json.Number("1")}},
		map[string]any{"a": []any{int64(1)}},
		DefaultEpsilon,
	))
	require.False(t, equalNormalizedValue(
		map[string]any{"a": 1},
		map[string]any{"b": 1},
		DefaultEpsilon,
	))
	require.False(t, equalNormalizedValue([]any{1}, []any{1, 2}, DefaultEpsilon))
	require.False(t, equalNormalizedValue(math.NaN(), float64(1), DefaultEpsilon))
}

func TestStructuredShapeAndTraceSummariesCoverAllKinds(t *testing.T) {
	require.Contains(t, structuredOutputIssue(`{}`, `{`), "not valid")
	require.Empty(t, structuredOutputIssue("", `{}`))
	require.Empty(t, structuredOutputIssue(`{`, `{}`))
	require.Contains(t, structuredOutputIssue(`{"a":1}`, `{"a":"x"}`), "kind")
	require.Contains(t, structuredOutputIssue(`{"a":1}`, `{}`), "missing")
	require.Empty(t, structuredOutputIssue(`[1]`, `[2]`))
	for _, item := range []struct {
		value any
		kind  string
	}{
		{nil, "null"},
		{map[string]any{}, "object"},
		{[]any{}, "array"},
		{"", "string"},
		{json.Number("1"), "number"},
		{true, "boolean"},
		{make(chan int), "unknown"},
	} {
		require.Equal(t, item.kind, jsonValueKind(item.value))
	}
	trace := make([]TraceStep, 7)
	for i := range trace {
		trace[i] = TraceStep{
			StepID:    "step",
			NodeID:    "node",
			AgentName: "agent",
			Branch:    "branch",
			Error:     "error",
		}
	}
	summary := summarizeTrace(trace)
	require.Contains(t, summary, "node=node")
	require.Contains(t, summary, "agent=agent")
	require.Contains(t, summary, "branch=branch")
	require.Contains(t, summary, "error=error")
	require.Contains(t, summary, "…")
	require.NotNil(t, ambiguousToolEvidence("actual", context.Canceled))
}

func TestAttributionAmbiguityOrderingAndNormalizationSafetyBranches(t *testing.T) {
	failed := CaseResult{
		EvalSetID:     "set",
		CaseID:        "case",
		Status:        "failed",
		PrimaryMetric: "quality",
		Metrics: []MetricResult{{
			MetricName: "quality",
			Status:     "failed",
			Direction:  ScoreHigherIsBetter,
		}},
		ExpectedTools: []ToolCall{
			{Sequence: 1, Name: "lookup"},
			{Name: "verify"},
		},
	}
	snapshot := testSnapshot("profile", []string{"case"}, []string{"quality"})
	snapshot.Provenance.EvalSetID = "set"
	snapshot.Cases = []CaseResult{failed}
	snapshot.Failed = 1
	metricResult := failed.Metrics[0]
	attribution := AttributeFailure(AttributionInput{
		Snapshot: snapshot,
		Case:     failed,
		Metric:   metricResult,
	})
	require.Equal(t, FailureAmbiguousEvidence, attribution.PrimaryCategory)

	failed.ExpectedRoute = "expected"
	failed.Route = "actual"
	failed.Trace = []TraceStep{{StepID: "route", Branch: "actual"}}
	snapshot.Cases[0] = failed
	attribution = AttributeFailure(AttributionInput{
		Snapshot: snapshot,
		Case:     failed,
		Metric:   metricResult,
	})
	require.Equal(t, FailureWrongRoute, attribution.PrimaryCategory)
	require.GreaterOrEqual(t, len(attribution.Evidence), 2)

	for _, test := range []struct {
		expected  []ToolCall
		actual    []ToolCall
		ambiguous bool
	}{
		{expected: []ToolCall{{Name: ""}}, ambiguous: true},
		{actual: []ToolCall{{Sequence: 1, Name: "a"}, {Sequence: 1, Name: "b"}}, ambiguous: true},
		{actual: []ToolCall{{Sequence: 1, Name: "a"}, {Name: "b"}}, ambiguous: true},
		{
			expected: []ToolCall{{Sequence: 1, Name: "a"}},
			actual:   []ToolCall{{Sequence: 1, Name: "b"}},
		},
		{
			expected: []ToolCall{{Sequence: 1, Name: "a"}},
			actual:   nil,
		},
		{
			expected:  []ToolCall{{Sequence: 1, Name: "a", Arguments: make(chan int)}},
			actual:    []ToolCall{{Sequence: 1, Name: "a", Arguments: map[string]any{}}},
			ambiguous: true,
		},
		{
			expected: []ToolCall{{Sequence: 1, Name: "a", Arguments: map[string]any{"id": 1}}},
			actual:   []ToolCall{{Sequence: 1, Name: "a", Arguments: map[string]any{"id": 2}}},
		},
	} {
		finding, ambiguous := compareTools(test.expected, test.actual)
		if test.ambiguous {
			require.Nil(t, finding)
			require.NotNil(t, ambiguous)
		} else {
			require.NotNil(t, finding)
			require.Nil(t, ambiguous)
		}
	}
	ordered, err := toolsInExecutionOrder([]ToolCall{
		{Sequence: 2, Name: "second"},
		{Sequence: 1, Name: "first"},
	})
	require.NoError(t, err)
	require.Equal(t, "first", ordered[0].Name)

	_, err = equalJSONLike(make(chan int), map[string]any{}, DefaultEpsilon)
	require.Error(t, err)
	_, err = equalJSONLike(map[string]any{}, make(chan int), DefaultEpsilon)
	require.Error(t, err)
	require.Contains(t, summarizeJSONLike(make(chan int)), "0x")
	_, err = decodeJSONValue([]byte(`{} {}`))
	require.Error(t, err)
	_, err = decodeJSONValue([]byte(`{} trailing`))
	require.Error(t, err)

	require.False(t, equalNormalizedValue(map[string]any{"a": 1}, []any{1}, DefaultEpsilon))
	require.False(t, equalNormalizedValue(
		map[string]any{"a": 1},
		map[string]any{"b": 1},
		DefaultEpsilon,
	))
	require.False(t, equalNormalizedValue([]any{1}, map[string]any{}, DefaultEpsilon))
	require.False(t, equalNormalizedValue([]any{1}, []any{2}, DefaultEpsilon))
	require.False(t, runesEqual([]rune("a"), []rune("ab")))
	require.False(t, factTokenBoundary([]rune("xa"), []rune("a"), 1, 2))
	require.False(t, factTokenBoundary([]rune("ax"), []rune("a"), 0, 1))
	require.Equal(t, 2, runeClauseStart([]rune("a,b"), 3))
	require.Equal(t, 1, runeClauseEnd([]rune("a,b"), 0))
	require.Equal(t, []FailureCategory{FailureWrongTool}, appendUniqueCategory(
		[]FailureCategory{FailureWrongTool},
		FailureWrongTool,
	))
}

func TestProfileAndSnapshotRecordValidatorsRejectEachMissingBinding(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ProfileRecord)
	}{
		{"role", func(profile *ProfileRecord) { profile.Role = ProfileCandidate }},
		{"hash", func(profile *ProfileRecord) { profile.Hash = "" }},
		{"structure", func(profile *ProfileRecord) { profile.StructureID = "" }},
		{"surface", func(profile *ProfileRecord) { profile.TargetSurfaceID = "" }},
		{"prompt", func(profile *ProfileRecord) { profile.Prompt = "" }},
		{"payload", func(profile *ProfileRecord) { profile.Profile = nil }},
		{"payload hash", func(profile *ProfileRecord) {
			profile.Profile = pipelineProfile("different")
		}},
	} {
		t.Run("profile/"+test.name, func(t *testing.T) {
			profile := testProfileRecord(t, ProfileInitial, "prompt")
			test.mutate(&profile)
			require.Error(t, validateProfileRecord("profile", ProfileInitial, &profile))
		})
	}
	require.Error(t, validateProfileRecord("profile", ProfileInitial, nil))

	for _, test := range []struct {
		name   string
		mutate func(*EvaluationSnapshot)
	}{
		{"status", func(snapshot *EvaluationSnapshot) { snapshot.Status = EvaluationRunFailed }},
		{"run", func(snapshot *EvaluationSnapshot) { snapshot.Provenance.RunID = "" }},
		{"profile", func(snapshot *EvaluationSnapshot) { snapshot.Provenance.ProfileHash = "" }},
		{"eval set", func(snapshot *EvaluationSnapshot) { snapshot.Provenance.EvalSetID = "" }},
		{"eval hash", func(snapshot *EvaluationSnapshot) { snapshot.Provenance.EvalSetHash = "" }},
		{"metrics hash", func(snapshot *EvaluationSnapshot) { snapshot.Provenance.MetricsHash = "" }},
		{"split", func(snapshot *EvaluationSnapshot) { snapshot.Provenance.Split = "" }},
		{"evaluator", func(snapshot *EvaluationSnapshot) {
			snapshot.Provenance.EvaluatorConfigHash = ""
		}},
		{"policy", func(snapshot *EvaluationSnapshot) {
			snapshot.Provenance.MetricPolicyHash = ""
		}},
		{"cases", func(snapshot *EvaluationSnapshot) { snapshot.Inventory.CaseIDs = nil }},
		{"metrics", func(snapshot *EvaluationSnapshot) { snapshot.Inventory.MetricNames = nil }},
		{"count", func(snapshot *EvaluationSnapshot) { snapshot.Cases = nil }},
	} {
		t.Run("snapshot/"+test.name, func(t *testing.T) {
			snapshot := testSnapshot("profile", []string{"case"}, []string{"quality"})
			setSnapshotCases(snapshot, 1, testCase("case", true, 1))
			test.mutate(snapshot)
			require.Error(t, validateCompletedSnapshot("snapshot", snapshot))
		})
	}
	require.Error(t, validateCompletedSnapshot("snapshot", nil))
}

func TestProfileSurfaceAndSmallPipelineHelpers(t *testing.T) {
	nonTextProfile := &promptiter.Profile{
		StructureID: "structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "surface",
			Value:     astructure.SurfaceValue{},
		}},
	}
	value, err := profileSurfaceText(nonTextProfile, "surface", nil)
	require.NoError(t, err)
	require.Contains(t, value, `"Text":null`)
	structure := &astructure.Snapshot{
		StructureID: "structure",
		Surfaces: []astructure.Surface{{
			SurfaceID: "surface",
			Value:     astructure.SurfaceValue{},
		}},
	}
	value, err = profileSurfaceText(&promptiter.Profile{}, "surface", structure)
	require.NoError(t, err)
	require.Contains(t, value, `"Text":null`)
	_, err = profileSurfaceText(&promptiter.Profile{}, "missing", structure)
	require.Error(t, err)

	patches := adaptPatches(&promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{
		SurfaceID: "surface",
		Value:     astructure.SurfaceValue{},
		Reason:    "reason",
	}}})
	require.Contains(t, patches[0].Value, `"Text":null`)

	_, found, err := profileOverrideValue(nil, "surface")
	require.Error(t, err)
	require.False(t, found)
	_, found, err = profileOverrideValue(&promptiter.Profile{}, "surface")
	require.NoError(t, err)
	require.False(t, found)
	value, found, err = profileOverrideValue(nonTextProfile, "surface")
	require.NoError(t, err)
	require.True(t, found)
	require.Contains(t, value, `"Text":null`)
	duplicate := *nonTextProfile
	duplicate.Overrides = append(duplicate.Overrides, duplicate.Overrides[0])
	_, _, err = profileOverrideValue(&duplicate, "surface")
	require.Error(t, err)

	require.Equal(t, 0, snapshotFailedCount(nil))
	require.Equal(t, 0, snapshotFailedCount(&EvaluationSnapshot{Status: EvaluationRunFailed}))
	require.Equal(t, 2, snapshotFailedCount(&EvaluationSnapshot{
		Status: EvaluationCompleted,
		Failed: 2,
	}))
	require.Equal(t, "", shortHash(""))
	require.Equal(t, "short", shortHash("short"))
	require.Len(t, shortHash(strings.Repeat("x", 32)), 12)
	require.Nil(t, cloneStringMap(nil))
	require.Equal(t, map[string]string{"a": "b"}, cloneStringMap(map[string]string{"a": "b"}))
}

func TestPromptPatchCompilationProfileRecordsAndFinalizationBranches(t *testing.T) {
	structure := pipelineTestStructure()
	input := pipelineProfile("initial prompt")
	targets := []string{pipelineTestSurfaceID}
	validPatch := func() promptiter.SurfacePatch {
		return promptiter.SurfacePatch{
			SurfaceID: pipelineTestSurfaceID,
			Value:     astructure.SurfaceValue{Text: stringPointer("candidate prompt")},
			Reason:    "improve",
		}
	}
	_, err := applyPromptIterPatches(structure, input, nil, targets)
	require.Error(t, err)
	for _, patches := range []promptiter.PatchSet{
		{Patches: []promptiter.SurfacePatch{{Reason: "reason"}}},
		{Patches: []promptiter.SurfacePatch{{
			SurfaceID: pipelineTestSurfaceID,
			Value:     astructure.SurfaceValue{Text: stringPointer("candidate")},
		}}},
		{Patches: []promptiter.SurfacePatch{validPatch(), validPatch()}},
		{Patches: []promptiter.SurfacePatch{{
			SurfaceID: "other",
			Value:     astructure.SurfaceValue{Text: stringPointer("candidate")},
			Reason:    "reason",
		}}},
	} {
		_, err := applyPromptIterPatches(structure, input, &patches, targets)
		require.Error(t, err)
	}
	missingSurface := promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{
		SurfaceID: "missing",
		Value:     astructure.SurfaceValue{Text: stringPointer("candidate")},
		Reason:    "reason",
	}}}
	_, err = applyPromptIterPatches(structure, input, &missingSurface, []string{"missing"})
	require.Error(t, err)
	patches := &promptiter.PatchSet{Patches: []promptiter.SurfacePatch{validPatch()}}
	output, err := applyPromptIterPatches(structure, input, patches, targets)
	require.NoError(t, err)
	require.Equal(t, "candidate prompt", pipelineProfileText(output))
	require.NoError(t, requireSameProfile("profile", output, output))
	require.Error(t, requireSameProfile("profile", output, input))

	_, err = buildProfileRecord(ProfileCandidate, nil, pipelineTestSurfaceID, structure)
	require.Error(t, err)
	_, err = buildProfileRecord(ProfileCandidate, input, "missing", structure)
	require.Error(t, err)
	withoutStructureID := pipelineProfile("prompt")
	withoutStructureID.StructureID = ""
	record, err := buildProfileRecord(
		ProfileCandidate,
		withoutStructureID,
		pipelineTestSurfaceID,
		structure,
	)
	require.NoError(t, err)
	require.Equal(t, structure.StructureID, record.StructureID)

	config := pipelineRunConfig()
	config.GeneratedAt = time.Time{}
	report := newReport(config, record)
	require.False(t, report.GeneratedAt.IsZero())
	state := ProfileState{
		Initial:  record,
		Search:   withProfileRole(record, ProfileSearch),
		Released: withProfileRole(record, ProfileReleased),
	}
	released := testProfileRecord(t, ProfileReleased, "released")
	state.Released = released
	finalizeReport(report, state)
	require.Equal(t, DecisionAccepted, report.FinalDecision.Status)

	report = newReport(config, record)
	report.Candidates = []CandidateReport{{
		ReleaseDecision: Decision{Status: DecisionRejected, Reasons: []string{"rejected"}},
	}}
	state.Released = withProfileRole(record, ProfileReleased)
	finalizeReport(report, state)
	require.Equal(t, DecisionRejected, report.FinalDecision.Status)
}

func TestConfigParsingDatasetAndLocatorHelpers(t *testing.T) {
	_, err := requireJSONObjectFields([]byte(`[]`), "object", "field")
	require.Error(t, err)
	_, err = requireJSONObjectFields([]byte(`null`), "object", "field")
	require.Error(t, err)
	_, err = requireJSONObjectFields([]byte(`{}`), "object", "field")
	require.Error(t, err)
	_, err = requireJSONObjectFields([]byte(`{"field":null}`), "object", "field")
	require.Error(t, err)
	object, err := requireJSONObjectFields([]byte(`{"field":1}`), "object", "field")
	require.NoError(t, err)
	require.Contains(t, object, "field")

	require.Error(t, validateSingleJSON([]byte(`{`)))
	require.Error(t, validateSingleJSON([]byte(`{} {}`)))
	require.NoError(t, validateSingleJSON([]byte(`{}`)))
	require.Error(t, validatePromptIterRequiredFields([]byte(`{}`)))
	require.Error(t, validatePromptIterRequiredFields([]byte(`{
		"schemaVersion":"1.0","seed":1,"policy":{}
	}`)))
	require.Error(t, validateRegressionRequiredFields([]byte(`{}`)))
	require.Error(t, validateRegressionRequiredFields([]byte(`{
		"schemaVersion":"1.0","reportId":"r","generatedAt":"1970-01-01T00:00:00Z",
		"evidenceLimit":1,"output":{},"gate":{}
	}`)))

	dir := t.TempDir()
	files := InputFiles{
		TrainEvalSet:      filepath.Join(dir, "train.json"),
		ValidationEvalSet: filepath.Join(dir, "validation.json"),
		Metrics:           filepath.Join(dir, "metrics.json"),
		BaselinePrompt:    filepath.Join(dir, "prompt.txt"),
		PromptIterConfig:  filepath.Join(dir, "promptiter.json"),
		RegressionConfig:  filepath.Join(dir, "regression.json"),
	}
	paths, err := validateInputFiles(files)
	require.NoError(t, err)
	require.Len(t, paths, 6)
	files.Metrics = ""
	_, err = validateInputFiles(files)
	require.Error(t, err)
	files.Metrics = files.TrainEvalSet
	_, err = validateInputFiles(files)
	require.Error(t, err)
	require.Error(t, validateOutputInputCollisions(
		OutputConfig{JSON: "train.json", Markdown: "report.md"},
		paths,
	))
	require.NoError(t, validateOutputInputCollisions(
		OutputConfig{JSON: "report.json", Markdown: "report.md"},
		paths,
	))
	_, _, err = readAndHashInputs(paths)
	require.Error(t, err)

	require.Error(t, func() error {
		_, err := loadNativeEvalSet(context.Background(), "app", []byte(`{`), "train")
		return err
	}())
	require.Error(t, func() error {
		_, err := loadNativeMetrics(context.Background(), "app", "train", []byte(`[`))
		return err
	}())

	validCase := func(id, content string) *evalset.EvalCase {
		return &evalset.EvalCase{
			EvalID: id,
			Conversation: []*evalset.Invocation{{
				UserContent: &model.Message{Content: content},
			}},
		}
	}
	metrics := []*metric.EvalMetric{{MetricName: "quality"}}
	for _, set := range []*evalset.EvalSet{
		nil,
		{},
		{EvalSetID: "set"},
		{EvalSetID: "set", EvalCases: []*evalset.EvalCase{nil}},
		{EvalSetID: "set", EvalCases: []*evalset.EvalCase{{}}},
		{EvalSetID: "set", EvalCases: []*evalset.EvalCase{
			validCase("a", "same"), validCase("a", "different"),
		}},
		{EvalSetID: "set", EvalCases: []*evalset.EvalCase{
			validCase("a", "same"), validCase("b", "same"),
		}},
	} {
		_, err := buildDatasetSpec(set, "set-hash", "metrics-hash", metrics)
		require.Error(t, err)
	}
	spec, err := buildDatasetSpec(
		&evalset.EvalSet{EvalSetID: "set", EvalCases: []*evalset.EvalCase{
			validCase("b", " Second  Input "),
			validCase("a", "First input"),
		}},
		"set-hash",
		"metrics-hash",
		metrics,
	)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, spec.CaseIDs)
	require.Equal(t, []string{"quality"}, spec.MetricNames)
	_, err = normalizedEvalCaseInput(&evalset.EvalCase{})
	require.Error(t, err)
	normalized, err := normalizedEvalCaseInput(&evalset.EvalCase{
		ConversationScenario: &evalset.ConversationScenario{StartingPrompt: " Scenario  Input "},
	})
	require.NoError(t, err)
	require.Contains(t, normalized, `"startingPrompt":"scenario input"`)
	partText := "part input"
	normalized, err = normalizedEvalCaseInput(&evalset.EvalCase{
		Conversation: []*evalset.Invocation{
			nil,
			{},
			{UserContent: &model.Message{ContentParts: []model.ContentPart{{
				Type: model.ContentTypeText,
				Text: &partText,
			}}}},
		},
	})
	require.NoError(t, err)
	require.Contains(t, normalized, "part input")

	require.Error(t, validateInternalValidation(
		PromptIterPolicy{
			InternalValidationStrategy: internalValidationTrainCaseIDs,
			InternalValidationCaseIDs:  []string{"heldout"},
		},
		DatasetSpec{CaseIDs: []string{"train"}},
		DatasetSpec{CaseIDs: []string{"heldout"}},
	))
	require.Error(t, validateInternalValidation(
		PromptIterPolicy{
			InternalValidationStrategy: internalValidationTrainCaseIDs,
			InternalValidationCaseIDs:  []string{"missing"},
		},
		DatasetSpec{CaseIDs: []string{"train"}},
		DatasetSpec{CaseIDs: []string{"heldout"}},
	))
	require.NoError(t, validateInternalValidation(
		PromptIterPolicy{InternalValidationStrategy: internalValidationTrainAll},
		DatasetSpec{},
		DatasetSpec{},
	))
	require.Error(t, validateConfiguredCases(
		RegressionConfig{CriticalCaseIDs: []string{"missing"}},
		DatasetSpec{CaseIDs: []string{"heldout"}},
	))
	require.Error(t, validateGateMetrics(
		GatePolicy{PrimaryMetric: "missing"},
		[]string{"quality"},
	))
	require.Error(t, validateGateMetrics(
		GatePolicy{
			PrimaryMetric:    "quality",
			MetricDirections: map[string]ScoreDirection{"other": ScoreHigherIsBetter},
		},
		[]string{"quality"},
	))
	require.Error(t, validateGateMetrics(
		GatePolicy{
			PrimaryMetric:    "quality",
			MetricDirections: map[string]ScoreDirection{},
		},
		[]string{"quality"},
	))

	profile := profileFromPrompt("prompt", "a")
	require.Len(t, profile.Overrides, 1)
	require.Equal(t, "a", profile.Overrides[0].SurfaceID)
	input := &inputEvalSetLocator{paths: map[string]string{"b": "B", "a": "A"}}
	require.Equal(t, "A", input.Build("", "", "a"))
	require.Contains(t, input.Build("", "", "missing"), "__unknown_eval_set__")
	list, err := input.List("", "")
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, list)
	require.Equal(t, "metrics", (&inputMetricLocator{path: "metrics"}).Build("", "", ""))
}

func TestRuntimeAndSourceBindingsFailClosedOnEveryMutableInput(t *testing.T) {
	runtime := RuntimeConfig{Engine: "engine", Seed: 2003}
	require.Error(t, BindRuntime(nil, runtime))

	for _, mutate := range []func(*RunConfig, *RuntimeConfig){
		func(config *RunConfig, _ *RuntimeConfig) { config.executionNonce = "" },
		func(_ *RunConfig, runtime *RuntimeConfig) { runtime.Engine = "" },
		func(_ *RunConfig, runtime *RuntimeConfig) { runtime.Seed++ },
		func(_ *RunConfig, runtime *RuntimeConfig) {
			runtime.Model = map[string]any{"unsupported": make(chan int)}
		},
		func(config *RunConfig, _ *RuntimeConfig) { config.InputHashes["trainEvalSet"] = "" },
		func(config *RunConfig, _ *RuntimeConfig) { config.InputHashes["validationEvalSet"] = "" },
		func(config *RunConfig, _ *RuntimeConfig) { config.InputHashes["metrics"] = "" },
		func(config *RunConfig, _ *RuntimeConfig) { config.InputHashes["baselinePrompt"] = "" },
		func(config *RunConfig, _ *RuntimeConfig) { config.InputHashes["promptIterConfig"] = "" },
		func(config *RunConfig, _ *RuntimeConfig) { config.InputHashes["regressionConfig"] = "" },
	} {
		config := pipelineRunConfig()
		currentRuntime := runtime
		mutate(&config, &currentRuntime)
		require.Error(t, BindRuntime(&config, currentRuntime))
	}
	_, err := RuntimeConfigFingerprint(RuntimeConfig{
		Model: map[string]any{"unsupported": make(chan int)},
	})
	require.Error(t, err)
	_, err = sourceConfigFingerprint(nil)
	require.Error(t, err)
	require.Error(t, sealSourceConfig(nil))

	config := pipelineRunConfig()
	config.executionNonce = "nonce"
	require.NoError(t, BindRuntime(&config, runtime))
	require.NotEmpty(t, config.RunID)
	require.NotEmpty(t, config.EvaluatorConfigHash)
	require.NotSame(t, &runtime, &config.Runtime)
}

func TestConfigurationLoadersAndFrozenManagersRejectPreflightFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := LoadRunConfig(canceled, "app", InputFiles{})
	require.ErrorIs(t, err, context.Canceled)
	_, err = LoadRunConfig(context.Background(), "", InputFiles{})
	require.ErrorContains(t, err, "app name")
	_, err = LoadRunConfig(context.Background(), "app", InputFiles{})
	require.ErrorContains(t, err, "path is empty")

	_, err = LoadPromptIterConfig(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
	_, err = LoadRegressionConfig(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
	_, _, err = NewInputManagers(nil)
	require.Error(t, err)
	invalid := pipelineRunConfig()
	invalid.RunID = ""
	_, _, err = NewInputManagers(&invalid)
	require.Error(t, err)
}

func TestNativeEvidenceAndMetricAdaptersRejectMalformedResults(t *testing.T) {
	require.Error(t, verifyInventory("item", []string{""}, []string{""}))
	require.Error(t, verifyInventory("item", []string{"a"}, []string{"a", "a"}))
	require.Error(t, verifyInventory("item", []string{"a"}, []string{"b"}))
	require.Error(t, verifyInventory("item", []string{"a", "b"}, []string{"a"}))
	require.NoError(t, verifyInventory("item", []string{"a"}, []string{"a"}))

	request := SnapshotRequest{
		Dataset: DatasetSpec{
			EvalSetID:   "set",
			CaseIDs:     []string{"case"},
			MetricNames: []string{"quality"},
		},
		PrimaryMetric:    "quality",
		MetricDirections: map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
		EvidenceLimit:    1,
	}
	source := &evalset.EvalCase{EvalID: "case"}
	_, err := adaptNativeCase(request, nil, source, nil, nil, nil)
	require.Error(t, err)
	_, err = adaptNativeCase(request, &evaluation.EvaluationCaseResult{}, nil, nil, nil, nil)
	require.Error(t, err)
	_, err = adaptNativeCase(
		request,
		&evaluation.EvaluationCaseResult{EvalCaseID: "case", OverallStatus: "unknown"},
		source,
		nil,
		nil,
		nil,
	)
	require.Error(t, err)

	validMetric := func() *evalresult.EvalMetricResult {
		return &evalresult.EvalMetricResult{
			MetricName: "quality",
			Score:      0.8,
			EvalStatus: status.EvalStatusPassed,
			Threshold:  0.5,
		}
	}
	for _, results := range [][]*evalresult.EvalMetricResult{
		{nil},
		{validMetric(), validMetric()},
		{{MetricName: "quality", EvalStatus: "unknown"}},
		{{MetricName: "quality", EvalStatus: status.EvalStatusPassed, Score: math.NaN()}},
	} {
		_, err := adaptNativeMetrics(
			request,
			&evaluation.EvaluationCaseResult{MetricResults: results},
			map[string]*metric.EvalMetric{"quality": {MetricName: "quality", Threshold: 0.5}},
		)
		require.Error(t, err)
	}
	_, err = adaptNativeMetrics(
		request,
		&evaluation.EvaluationCaseResult{MetricResults: []*evalresult.EvalMetricResult{validMetric()}},
		map[string]*metric.EvalMetric{"quality": {MetricName: "quality", Threshold: 0.7}},
	)
	require.Error(t, err)
	converted, err := adaptNativeMetrics(
		request,
		&evaluation.EvaluationCaseResult{MetricResults: []*evalresult.EvalMetricResult{validMetric()}},
		map[string]*metric.EvalMetric{"quality": {MetricName: "quality", Threshold: 0.5}},
	)
	require.NoError(t, err)
	require.Len(t, converted, 1)

	require.Empty(t, invocationResponse(nil))
	require.Empty(t, invocationResponse(&evalset.Invocation{}))
	require.Equal(t, "response", invocationResponse(&evalset.Invocation{
		FinalResponse: &model.Message{Content: "response"},
	}))
	require.Nil(t, lastInvocation([]*evalset.Invocation{nil}))
	last := &evalset.Invocation{InvocationID: "last"}
	require.Equal(t, last, lastInvocation([]*evalset.Invocation{nil, last, nil}))
	tools := invocationTools([]*evalset.Invocation{
		nil,
		{Tools: []*evalset.Tool{
			nil,
			{Name: "lookup", Arguments: map[string]any{"q": "x"}, Result: "ok"},
		}},
	})
	require.Len(t, tools, 1)
	require.Equal(t, 1, tools[0].Sequence)

	structured, route, facts := sourceExpectations(&evalset.EvalCase{
		Rubrics: []*evalset.EvalCaseRubric{
			nil,
			{Type: "expected_route", Description: "route-a"},
			{Type: "expected_fact", Content: &evalset.EvalCaseRubricContent{Text: "fact-a"}},
			{Type: "expected_fact", Description: ""},
			{Type: "structured_output"},
		},
	})
	require.True(t, structured)
	require.Equal(t, "route-a", route)
	require.Equal(t, []string{"fact-a"}, facts)
	require.True(t, sourceExpectsNoTools(&evalset.EvalCase{
		Conversation: []*evalset.Invocation{{
			Tools: make([]*evalset.Tool, 0),
		}},
	}))
	require.False(t, sourceExpectsNoTools(&evalset.EvalCase{
		Conversation: []*evalset.Invocation{{}},
	}))
	require.False(t, sourceExpectsNoTools(&evalset.EvalCase{
		Conversation: []*evalset.Invocation{{
			Tools: []*evalset.Tool{{Name: "lookup"}},
		}},
	}))
	require.False(t, sourceExpectsNoTools(nil))
	require.Equal(t, "leaf", routeLeaf("router -> branch:leaf"))
	require.Equal(t, "", routeLeaf(""))

	require.Equal(t, ResourceUsage{}, snapshotMeter(nil))
	require.Equal(t, int64(12), measuredUsage(nil, ResourceUsage{}, 12).LatencyMS.Value)
	meter := NewUsageMeter()
	before := meter.Snapshot()
	meter.Record(ResourceUsage{
		ModelCalls: Count{Available: true, Value: 1},
		LatencyMS:  Count{},
	})
	usage := measuredUsage(meter, before, 15)
	require.Equal(t, int64(1), usage.ModelCalls.Value)
	require.Equal(t, int64(15), usage.LatencyMS.Value)
}

func TestNativeEvaluatorConstructionAndInvocationEvidenceFallbacks(t *testing.T) {
	valid := ProfileEvaluatorConfig{
		AppName:        "app",
		AgentEvaluator: &pipelineNativeResultEvaluator{},
		EvalSetManager: &frozenEvalSetManager{},
		MetricManager:  &frozenMetricManager{},
		Structure:      pipelineTestStructure(),
	}
	for _, mutate := range []func(*ProfileEvaluatorConfig){
		func(config *ProfileEvaluatorConfig) { config.AppName = "" },
		func(config *ProfileEvaluatorConfig) { config.AgentEvaluator = nil },
		func(config *ProfileEvaluatorConfig) { config.EvalSetManager = nil },
		func(config *ProfileEvaluatorConfig) { config.MetricManager = nil },
		func(config *ProfileEvaluatorConfig) { config.Structure = nil },
	} {
		config := valid
		mutate(&config)
		_, err := NewProfileEvaluator(config)
		require.Error(t, err)
	}
	evaluator, err := NewProfileEvaluator(valid)
	require.NoError(t, err)
	require.NotNil(t, evaluator)

	perInvocationActual := &evalset.Invocation{InvocationID: "per-actual"}
	perInvocationExpected := &evalset.Invocation{InvocationID: "per-expected"}
	runActual := &evalset.Invocation{InvocationID: "run-actual"}
	sourceExpected := &evalset.Invocation{InvocationID: "source-expected"}
	sourceActual := &evalset.Invocation{InvocationID: "source-actual"}

	actual, expected := invocationEvidence(
		&evaluation.EvaluationCaseResult{
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{
					nil,
					{
						ActualInvocation:   perInvocationActual,
						ExpectedInvocation: perInvocationExpected,
					},
				},
			}},
			RunDetails: []*evaluation.EvaluationCaseRunDetails{
				nil,
				{},
				{Inference: &evaluation.EvaluationInferenceDetails{
					Inferences: []*evalset.Invocation{nil, runActual},
				}},
			},
		},
		&evalset.EvalCase{
			Conversation:       []*evalset.Invocation{sourceExpected},
			ActualConversation: []*evalset.Invocation{sourceActual},
		},
	)
	require.Equal(t, []*evalset.Invocation{runActual}, actual)
	require.Equal(t, []*evalset.Invocation{perInvocationExpected}, expected)

	actual, expected = invocationEvidence(
		&evaluation.EvaluationCaseResult{
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				EvalMetricResultPerInvocation: []*evalresult.EvalMetricResultPerInvocation{{
					ActualInvocation: perInvocationActual,
				}},
			}},
		},
		&evalset.EvalCase{Conversation: []*evalset.Invocation{nil, sourceExpected}},
	)
	require.Equal(t, []*evalset.Invocation{perInvocationActual}, actual)
	require.Equal(t, []*evalset.Invocation{sourceExpected}, expected)

	actual, expected = invocationEvidence(
		&evaluation.EvaluationCaseResult{},
		&evalset.EvalCase{ActualConversation: []*evalset.Invocation{nil, sourceActual}},
	)
	require.Equal(t, []*evalset.Invocation{sourceActual}, actual)
	require.Empty(t, expected)
}

func TestNativeResultAdapterRejectsEveryLineageAndInventoryViolation(t *testing.T) {
	request := SnapshotRequest{
		Dataset: DatasetSpec{
			EvalSetID:   "set",
			CaseIDs:     []string{"case"},
			MetricNames: []string{"quality"},
		},
		PrimaryMetric:    "quality",
		MetricDirections: map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
		EvidenceLimit:    10,
	}
	sourceSet := &evalset.EvalSet{
		EvalSetID: "set",
		EvalCases: []*evalset.EvalCase{{EvalID: "case"}},
	}
	metrics := map[string]*metric.EvalMetric{
		"quality": {MetricName: "quality", Threshold: 0.5},
	}
	validResult := func() *evaluation.EvaluationResult {
		return &evaluation.EvaluationResult{
			AppName:   "app",
			EvalSetID: "set",
			EvalCases: []*evaluation.EvaluationCaseResult{{
				EvalCaseID:    "case",
				OverallStatus: status.EvalStatusPassed,
				MetricResults: []*evalresult.EvalMetricResult{{
					MetricName: "quality",
					Score:      0.8,
					EvalStatus: status.EvalStatusPassed,
					Threshold:  0.5,
					Details: &evalresult.EvalMetricResultDetails{
						Reason: "correct",
						RubricScores: []*evalresult.RubricScore{
							nil,
							{ID: "accuracy", Score: 1, Reason: "correct"},
						},
					},
				}},
			}},
		}
	}
	evaluator := &ProfileEvaluator{appName: "app"}
	for _, test := range []struct {
		name   string
		mutate func(*SnapshotRequest, **evaluation.EvaluationResult)
	}{
		{"nil result", func(_ *SnapshotRequest, result **evaluation.EvaluationResult) {
			*result = nil
		}},
		{"app mismatch", func(_ *SnapshotRequest, result **evaluation.EvaluationResult) {
			(*result).AppName = "other"
		}},
		{"eval set mismatch", func(_ *SnapshotRequest, result **evaluation.EvaluationResult) {
			(*result).EvalSetID = "other"
		}},
		{"nil case", func(_ *SnapshotRequest, result **evaluation.EvaluationResult) {
			(*result).EvalCases[0] = nil
		}},
		{"duplicate case", func(_ *SnapshotRequest, result **evaluation.EvaluationResult) {
			(*result).EvalCases = append((*result).EvalCases, (*result).EvalCases[0])
		}},
		{"missing case", func(_ *SnapshotRequest, result **evaluation.EvaluationResult) {
			(*result).EvalCases = nil
		}},
		{"case adaptation", func(_ *SnapshotRequest, result **evaluation.EvaluationResult) {
			(*result).EvalCases[0].OverallStatus = "unknown"
		}},
		{"primary absent", func(request *SnapshotRequest, _ **evaluation.EvaluationResult) {
			request.PrimaryMetric = "other"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			currentRequest := request
			result := validResult()
			test.mutate(&currentRequest, &result)
			snapshot := newEvaluationSnapshot(currentRequest, "profile")
			require.Error(t, evaluator.adaptResult(
				snapshot,
				currentRequest,
				sourceSet,
				metrics,
				result,
			))
		})
	}
	snapshot := newEvaluationSnapshot(request, "profile")
	require.NoError(t, evaluator.adaptResult(snapshot, request, sourceSet, metrics, validResult()))
	require.Equal(t, 0.8, snapshot.OverallScore)
	require.Equal(t, 1, snapshot.Passed)
	require.Len(t, snapshot.Cases[0].Metrics[0].RubricScores, 1)

	require.Equal(t, "case error", nativeOperationalError(&evaluation.EvaluationCaseResult{
		EvalCaseResults: []*evalresult.EvalCaseResult{
			nil,
			{ErrorMessage: " case error "},
		},
	}))
	require.Equal(t, "inference error", nativeOperationalError(&evaluation.EvaluationCaseResult{
		RunDetails: []*evaluation.EvaluationCaseRunDetails{
			nil,
			{},
			{Inference: &evaluation.EvaluationInferenceDetails{ErrorMessage: " inference error "}},
		},
	}))
	require.Empty(t, nativeOperationalError(&evaluation.EvaluationCaseResult{}))
	steps, route := adaptTraceEvidence(&evaluation.EvaluationCaseResult{
		RunDetails: []*evaluation.EvaluationCaseRunDetails{
			nil,
			{},
			{Inference: &evaluation.EvaluationInferenceDetails{
				ExecutionTraces: []*atrace.Trace{
					nil,
					{Steps: []atrace.Step{{StepID: "step", Branch: "router -> leaf"}}},
				},
			}},
		},
	})
	require.Len(t, steps, 1)
	require.Equal(t, "leaf", route)

	fallbackCase := &evaluation.EvaluationCaseResult{
		EvalCaseResults: []*evalresult.EvalCaseResult{{
			OverallEvalMetricResults: []*evalresult.EvalMetricResult{
				nil,
				{
					MetricName: "quality",
					Score:      0.7,
					EvalStatus: status.EvalStatusPassed,
					Threshold:  0.5,
				},
			},
		}},
	}
	converted, err := adaptNativeMetrics(request, fallbackCase, metrics)
	require.NoError(t, err)
	require.Len(t, converted, 1)
}

func TestNativeProfileEvaluatorCoversRequestInputCompileAndExecutionFailures(t *testing.T) {
	ctx := context.Background()
	_, evalSetManager, metricManager := pipelineNativeEvaluator(
		t,
		ctx,
		&pipelineNativeService{surfaceID: pipelineTestSurfaceID},
	)
	t.Cleanup(func() {
		require.NoError(t, evalSetManager.Close())
		require.NoError(t, metricManager.Close())
	})
	config := pipelineRunConfig()
	profileHash, err := ProfileFingerprint(config.InitialProfile)
	require.NoError(t, err)
	request := SnapshotRequest{
		EvaluationRunID:     "run/train",
		Profile:             config.InitialProfile,
		ExpectedProfileHash: profileHash,
		Dataset:             config.Train,
		Split:               "train",
		Seed:                config.Seed,
		EvaluatorConfigHash: config.EvaluatorConfigHash,
		MetricPolicyHash:    config.MetricPolicyHash,
		PrimaryMetric:       config.Gate.PrimaryMetric,
		MetricDirections:    config.Gate.MetricDirections,
		EvidenceLimit:       config.EvidenceLimit,
	}
	evaluator, err := NewProfileEvaluator(ProfileEvaluatorConfig{
		AppName:        "pipeline-native",
		AgentEvaluator: failingAgentEvaluator{err: errors.New("native unavailable")},
		EvalSetManager: evalSetManager,
		MetricManager:  metricManager,
		Structure:      pipelineTestStructure(),
	})
	require.NoError(t, err)

	invalid := request
	invalid.EvaluationRunID = ""
	snapshot, err := evaluator.Evaluate(ctx, invalid)
	require.Error(t, err)
	require.NotNil(t, snapshot)
	require.NotEmpty(t, snapshot.Error)

	for _, dataset := range []DatasetSpec{
		{
			EvalSetID:   "missing",
			CaseIDs:     []string{"train-a"},
			MetricNames: []string{"quality"},
		},
		{
			EvalSetID:   config.Train.EvalSetID,
			CaseIDs:     []string{"missing"},
			MetricNames: []string{"quality"},
		},
		{
			EvalSetID:   config.Train.EvalSetID,
			CaseIDs:     []string{"train-a"},
			MetricNames: []string{"missing"},
		},
	} {
		_, _, err := evaluator.loadAndVerifyInputs(ctx, dataset)
		require.Error(t, err)
	}
	sourceSet, metricIndex, err := evaluator.loadAndVerifyInputs(ctx, config.Train)
	require.NoError(t, err)
	require.NotNil(t, sourceSet)
	require.Contains(t, metricIndex, "quality")

	invalidProfile := pipelineProfile("prompt")
	invalidProfile.Overrides[0].SurfaceID = "missing"
	invalidHash, err := ProfileFingerprint(invalidProfile)
	require.NoError(t, err)
	compileFailure := request
	compileFailure.Profile = invalidProfile
	compileFailure.ExpectedProfileHash = invalidHash
	snapshot, err = evaluator.Evaluate(ctx, compileFailure)
	require.Error(t, err)
	require.Contains(t, snapshot.Error, "surface")

	snapshot, err = evaluator.Evaluate(ctx, request)
	require.Error(t, err)
	require.Equal(t, EvaluationRunFailed, snapshot.Status)
	require.Contains(t, snapshot.Error, "native unavailable")
}

func TestReportComponentValidatorsCoverCandidateAndResourceFailures(t *testing.T) {
	validDataset := func() DatasetSpec {
		return DatasetSpec{
			EvalSetID:             "set",
			EvalSetHash:           "set-hash",
			MetricsHash:           "metrics-hash",
			CaseIDs:               []string{"case"},
			MetricNames:           []string{"quality"},
			NormalizedInputHashes: map[string]string{"case": "input-hash"},
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*DatasetSpec)
	}{
		{"id", func(dataset *DatasetSpec) { dataset.EvalSetID = "" }},
		{"hash", func(dataset *DatasetSpec) { dataset.EvalSetHash = "" }},
		{"metrics hash", func(dataset *DatasetSpec) { dataset.MetricsHash = "" }},
		{"cases", func(dataset *DatasetSpec) { dataset.CaseIDs = nil }},
		{"metrics", func(dataset *DatasetSpec) { dataset.MetricNames = nil }},
		{"duplicate case", func(dataset *DatasetSpec) {
			dataset.CaseIDs = []string{"case", "case"}
		}},
		{"duplicate metric", func(dataset *DatasetSpec) {
			dataset.MetricNames = []string{"quality", "quality"}
		}},
		{"hash count", func(dataset *DatasetSpec) {
			dataset.NormalizedInputHashes = nil
		}},
		{"empty input hash", func(dataset *DatasetSpec) {
			dataset.NormalizedInputHashes["case"] = ""
		}},
		{"unexpected input hash", func(dataset *DatasetSpec) {
			delete(dataset.NormalizedInputHashes, "case")
			dataset.NormalizedInputHashes["other"] = "input-hash"
		}},
	} {
		t.Run("dataset/"+test.name, func(t *testing.T) {
			dataset := validDataset()
			test.mutate(&dataset)
			require.Error(t, validateResolvedDataset("dataset", dataset))
		})
	}
	duplicateHashes := validDataset()
	duplicateHashes.CaseIDs = []string{"a", "b"}
	duplicateHashes.NormalizedInputHashes = map[string]string{"a": "same", "b": "same"}
	require.Error(t, validateResolvedDataset("dataset", duplicateHashes))
	require.NoError(t, validateResolvedDataset("dataset", validDataset()))

	target := "surface"
	validPatch := PatchRecord{SurfaceID: "surface", Value: "prompt", Reason: "reason"}
	for _, patches := range [][]PatchRecord{
		{{Value: "prompt", Reason: "reason"}},
		{{SurfaceID: "surface", Reason: "reason"}},
		{{SurfaceID: "surface", Value: "prompt"}},
		{{SurfaceID: "other", Value: "prompt", Reason: "reason"}},
		{validPatch, validPatch},
	} {
		require.Error(t, validateCandidatePatches("candidate", patches, target))
	}
	require.NoError(t, validateCandidatePatches("candidate", []PatchRecord{validPatch}, target))

	candidate := testReport(t).Candidates[0]
	for _, mutate := range []func(*CandidateReport){
		func(value *CandidateReport) { value.Transition.Explanation = "" },
		func(value *CandidateReport) { value.Transition.SearchBefore = "other" },
		func(value *CandidateReport) { value.Transition.ReleasedBefore = "other" },
		func(value *CandidateReport) { value.Transition.SearchUpdated = true },
		func(value *CandidateReport) { value.Transition.ReleaseUpdated = false },
	} {
		copy := candidate
		mutate(&copy)
		_, _, err := validateCandidateTransition("candidate", &copy)
		require.Error(t, err)
	}
	_, _, err := validateCandidateTransition("candidate", &candidate)
	require.NoError(t, err)

	require.Error(t, validateDecisionReasons("decision", Decision{}))
	require.Error(t, validateDecisionReasons(
		"decision",
		Decision{Reasons: []string{""}},
	))
	require.NoError(t, validateDecisionReasons(
		"decision",
		Decision{Reasons: []string{"reason"}},
	))

	for _, ledger := range []ResourceLedger{
		{Entries: []ResourceEntry{{}}},
		{Entries: []ResourceEntry{{Stage: "stage", Round: -1}}},
		{Entries: []ResourceEntry{{
			Stage: "stage",
			Usage: ResourceUsage{MonetaryCost: Amount{Unit: "USD"}},
		}}},
		{
			Entries: []ResourceEntry{{
				Stage: "stage",
				Usage: ResourceUsage{ModelCalls: Count{Available: true, Value: 1}},
			}},
			Cumulative: ResourceUsage{},
		},
	} {
		require.Error(t, validateResourceLedger("ledger", ledger))
	}
	validUsage := ResourceUsage{ModelCalls: Count{Available: true, Value: 1}}
	validLedger := ResourceLedger{
		Entries:    []ResourceEntry{{Stage: "stage", Usage: validUsage}},
		Cumulative: validUsage,
	}
	require.NoError(t, validateResourceLedger("ledger", validLedger))
}

func TestReportBindingsRejectEveryProfileSnapshotPatchAndDeltaDrift(t *testing.T) {
	t.Run("profile config", func(t *testing.T) {
		for _, mutate := range []func(*ProfileRecord){
			func(profile *ProfileRecord) { profile.StructureID = "other" },
			func(profile *ProfileRecord) { profile.TargetSurfaceID = "other" },
			func(profile *ProfileRecord) { profile.Profile = nil },
			func(profile *ProfileRecord) { profile.Profile.StructureID = "other" },
			func(profile *ProfileRecord) { profile.Profile.Overrides = nil },
			func(profile *ProfileRecord) { profile.Prompt = "other" },
		} {
			report := testReport(t)
			profile := report.InitialProfile
			mutate(&profile)
			require.Error(t, validateProfileConfigBinding(
				"profile",
				&profile,
				report.InitialProfile.StructureID,
				report.InitialProfile.TargetSurfaceID,
			))
		}
		report := testReport(t)
		require.NoError(t, validateProfileConfigBinding(
			"profile",
			&report.InitialProfile,
			report.InitialProfile.StructureID,
			report.InitialProfile.TargetSurfaceID,
		))
	})

	t.Run("snapshot binding", func(t *testing.T) {
		for _, mutate := range []func(*EvaluationSnapshot){
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.RunID = "other" },
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.ProfileHash = "other" },
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.EvalSetID = "other" },
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.EvalSetHash = "other" },
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.MetricsHash = "other" },
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.Split = "other" },
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.Seed++ },
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.EvaluatorConfigHash = "other" },
			func(snapshot *EvaluationSnapshot) { snapshot.Provenance.MetricPolicyHash = "other" },
			func(snapshot *EvaluationSnapshot) { snapshot.Inventory.CaseIDs = []string{"other"} },
			func(snapshot *EvaluationSnapshot) { snapshot.Inventory.MetricNames = []string{"other"} },
			func(snapshot *EvaluationSnapshot) { snapshot.Cases[0].Metrics = nil },
		} {
			report := testReport(t)
			snapshot := report.BaselineTrain
			binding := reportProvenanceBinding{
				evaluatorConfigHash: snapshot.Provenance.EvaluatorConfigHash,
				metricPolicyHash:    snapshot.Provenance.MetricPolicyHash,
			}
			mutate(snapshot)
			require.Error(t, validateSnapshotBinding(
				"snapshot",
				snapshot,
				report.InitialProfile.Hash,
				report.ResolvedConfig.Train,
				"train",
				report.RunID+"/baseline_train",
				report.ResolvedConfig.Seed,
				binding,
				report.ResolvedConfig.Gate,
			))
		}
	})

	t.Run("patch records", func(t *testing.T) {
		target := "agent#instruction"
		for _, patches := range [][]PatchRecord{
			{{SurfaceID: "", Value: "value", Reason: "reason"}},
			{{SurfaceID: target, Value: "", Reason: "reason"}},
			{{SurfaceID: target, Value: "value", Reason: ""}},
			{{SurfaceID: "other", Value: "value", Reason: "reason"}},
			{
				{SurfaceID: target, Value: "value", Reason: "reason"},
				{SurfaceID: target, Value: "value", Reason: "reason"},
			},
		} {
			require.Error(t, validateCandidatePatches("candidate", patches, target))
		}
		require.NoError(t, validateCandidatePatches("candidate", []PatchRecord{{
			SurfaceID: target,
			Value:     "value",
			Reason:    "reason",
		}}, target))
	})

	t.Run("patch binding", func(t *testing.T) {
		for _, mutate := range []func(*CandidateReport){
			func(candidate *CandidateReport) {
				candidate.Profile.Profile.Overrides = nil
			},
			func(candidate *CandidateReport) {
				candidate.Patches[0].Value = "other"
			},
			func(candidate *CandidateReport) {
				candidate.Profile.TargetSurfaceID = "other"
			},
			func(candidate *CandidateReport) {
				candidate.Profile.Prompt = "other"
			},
			func(candidate *CandidateReport) {
				candidate.OptimizationReason = "other"
			},
		} {
			report := testReport(t)
			candidate := report.Candidates[0]
			mutate(&candidate)
			require.Error(t, validateCandidatePatchBinding("candidate", &candidate))
		}
		report := testReport(t)
		require.NoError(t, validateCandidatePatchBinding("candidate", &report.Candidates[0]))
	})

	t.Run("delta binding", func(t *testing.T) {
		stateFor := func(report *Report) *reportValidationState {
			return &reportValidationState{
				searchHash:        report.InitialProfile.Hash,
				releasedHash:      report.InitialProfile.Hash,
				initialValidation: report.BaselineValidation,
				searchValidation:  report.BaselineValidation,
				releaseValidation: report.BaselineValidation,
			}
		}
		for _, mutate := range []func(*CandidateReport, *reportValidationState){
			func(candidate *CandidateReport, _ *reportValidationState) {
				candidate.Deltas.VsInitial.Comparison = "other"
			},
			func(_ *CandidateReport, state *reportValidationState) {
				state.searchValidation = nil
			},
			func(candidate *CandidateReport, _ *reportValidationState) {
				candidate.Deltas.VsSearchParent.BeforeProfileHash = "other"
			},
			func(candidate *CandidateReport, _ *reportValidationState) {
				candidate.Deltas.VsReleased.AfterProfileHash = "other"
			},
			func(candidate *CandidateReport, _ *reportValidationState) {
				candidate.Deltas.VsInitial.Cases[0].PrimaryKind = ChangeUnchanged
			},
			func(candidate *CandidateReport, _ *reportValidationState) {
				candidate.Deltas.VsInitial.ScoreDelta += DefaultEpsilon / 2
			},
			func(_ *CandidateReport, state *reportValidationState) {
				copied := *state.initialValidation
				copied.Provenance.EvalSetHash = "other"
				state.initialValidation = &copied
			},
		} {
			report := testReport(t)
			candidate := report.Candidates[0]
			state := stateFor(report)
			mutate(&candidate, state)
			require.Error(t, validateCandidateDeltas(
				"candidate",
				&candidate,
				state,
				report.ResolvedConfig.Gate,
			))
		}
		report := testReport(t)
		require.NoError(t, validateCandidateDeltas(
			"candidate",
			&report.Candidates[0],
			stateFor(report),
			report.ResolvedConfig.Gate,
		))
	})

	t.Run("transition binding", func(t *testing.T) {
		for _, mutate := range []func(*CandidateReport){
			func(candidate *CandidateReport) { candidate.Transition.Explanation = "" },
			func(candidate *CandidateReport) { candidate.Transition.SearchBefore = "other" },
			func(candidate *CandidateReport) { candidate.Transition.ReleasedBefore = "other" },
			func(candidate *CandidateReport) { candidate.Transition.SearchUpdated = true },
			func(candidate *CandidateReport) { candidate.Transition.ReleasedAfter = "other" },
		} {
			report := testReport(t)
			candidate := report.Candidates[0]
			mutate(&candidate)
			_, _, err := validateCandidateTransition("candidate", &candidate)
			require.Error(t, err)
		}
		report := testReport(t)
		searchUpdated, releaseUpdated, err := validateCandidateTransition(
			"candidate",
			&report.Candidates[0],
		)
		require.NoError(t, err)
		require.False(t, searchUpdated)
		require.True(t, releaseUpdated)
	})
}

func TestSuccessfulCandidateValidatorCoversLifecycleStatesAndIndependentUpdates(t *testing.T) {
	args := func(report *Report) (*CandidateReport, *reportValidationState, reportProvenanceBinding) {
		return &report.Candidates[0], &reportValidationState{
				searchHash:        report.InitialProfile.Hash,
				releasedHash:      report.InitialProfile.Hash,
				initialValidation: report.BaselineValidation,
				searchValidation:  report.BaselineValidation,
				releaseValidation: report.BaselineValidation,
			}, reportProvenanceBinding{
				evaluatorConfigHash: report.BaselineValidation.Provenance.EvaluatorConfigHash,
				metricPolicyHash:    report.BaselineValidation.Provenance.MetricPolicyHash,
			}
	}
	validate := func(
		report *Report,
		candidate *CandidateReport,
		state *reportValidationState,
		binding reportProvenanceBinding,
	) error {
		return validateSuccessfulCandidate(
			report,
			candidate,
			0,
			state,
			report.InitialProfile.StructureID,
			report.InitialProfile.TargetSurfaceID,
			binding,
		)
	}

	for _, mutate := range []func(*CandidateReport, *reportValidationState){
		func(candidate *CandidateReport, _ *reportValidationState) { candidate.Round = 2 },
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.SearchParentHash = "other"
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.ReleasedParentHash = "other"
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.PromptIterRunID = "other"
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.PromptIterStatus = "failed"
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.SearchDecision.Reasons = nil
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.ReleaseDecision.Reasons = []string{""}
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Patches = nil
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Patches[0].Reason = ""
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Profile = nil
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Status = EvaluationRunFailed
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Train.Provenance.RunID = "other"
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Validation.Provenance.RunID = "other"
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Profile.EvaluationRunID = "other"
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Deltas = nil
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.ReleaseDecision.ScoreDelta = nil
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Status = EvaluationNotEvaluable
		},
		func(candidate *CandidateReport, _ *reportValidationState) {
			candidate.Transition.Explanation = ""
		},
	} {
		report := testReport(t)
		candidate, state, binding := args(report)
		mutate(candidate, state)
		require.Error(t, validate(report, candidate, state, binding))
	}

	report := testReport(t)
	candidate, state, binding := args(report)
	candidate.SearchDecision = Decision{
		Status:     DecisionAccepted,
		Reasons:    []string{"search accepted"},
		ScoreDelta: float64Pointer(0.4),
	}
	candidate.Transition.SearchUpdated = true
	candidate.Transition.SearchAfter = candidate.Profile.Hash
	candidate.Transition.Explanation = "candidate advanced search and release"
	require.NoError(t, validate(report, candidate, state, binding))
	require.Equal(t, candidate.Profile.Hash, state.searchHash)
	require.Equal(t, candidate.Profile.Hash, state.releasedHash)

	report = testReport(t)
	candidate, state, binding = args(report)
	candidate.Status = EvaluationNotEvaluable
	candidate.Profile = nil
	candidate.Patches = nil
	candidate.OptimizationReason = ""
	candidate.Train = nil
	candidate.Validation = nil
	candidate.Deltas = nil
	candidate.SearchDecision = notEvaluableDecision("search unavailable")
	candidate.ReleaseDecision = notEvaluableDecision("release unavailable")
	candidate.Transition = unchangedTransition(
		candidate.SearchParentHash,
		candidate.ReleasedParentHash,
		"candidate unavailable",
	)
	require.NoError(t, validate(report, candidate, state, binding))
}

func TestSuccessfulReportConfigAndDatasetValidatorsCoverAuditInventoryEdges(t *testing.T) {
	for _, mutate := range []func(*Report){
		func(report *Report) { report.ResolvedConfig.EvidenceLimit = 0 },
		func(report *Report) { report.ResolvedConfig.Train.CaseIDs = []string{"train-case", "train-case"} },
		func(report *Report) { report.ResolvedConfig.Train.MetricNames = []string{"quality", "quality"} },
		func(report *Report) {
			report.ResolvedConfig.Validation.EvalSetID = report.ResolvedConfig.Train.EvalSetID
		},
		func(report *Report) { report.ResolvedConfig.Validation.MetricNames = []string{"quality", "safety"} },
		func(report *Report) { report.ResolvedConfig.Validation.MetricsHash = "other" },
		func(report *Report) {
			report.ResolvedConfig.Gate.MetricDirections = map[string]ScoreDirection{"other": ScoreHigherIsBetter}
		},
		func(report *Report) {
			report.ResolvedConfig.PromptIter.InternalValidationStrategy = internalValidationTrainCaseIDs
			report.ResolvedConfig.PromptIter.InternalValidationCaseIDs = []string{"validation-case"}
		},
		func(report *Report) {
			report.ResolvedConfig.CriticalCaseIDs = []string{"validation-case", "validation-case"}
		},
		func(report *Report) { report.ResolvedConfig.HardFailureCaseIDs = []string{"missing"} },
		func(report *Report) { delete(report.InputHashes, "baselinePrompt") },
		func(report *Report) { report.InputHashes["promptIterConfig"] = "" },
		func(report *Report) { report.InputHashes["trainEvalSet"] = "other" },
		func(report *Report) { report.InputHashes["validationEvalSet"] = "other" },
		func(report *Report) { report.InputHashes["metrics"] = "other" },
		func(report *Report) { report.Runtime.Engine = "" },
		func(report *Report) { report.Runtime.Seed++ },
		func(report *Report) {
			report.Runtime.Model = map[string]any{"unsupported": make(chan int)}
		},
	} {
		report := testReport(t)
		mutate(report)
		_, err := validateSuccessfulReportConfig(report)
		require.Error(t, err)
	}
	report := testReport(t)
	_, err := validateSuccessfulReportConfig(report)
	require.NoError(t, err)

	validDataset := func() DatasetSpec {
		return DatasetSpec{
			EvalSetID:   "set",
			EvalSetHash: "set-hash",
			MetricsHash: "metrics-hash",
			CaseIDs:     []string{"a", "b"},
			MetricNames: []string{"quality"},
			NormalizedInputHashes: map[string]string{
				"a": "hash-a",
				"b": "hash-b",
			},
		}
	}
	for _, mutate := range []func(*DatasetSpec){
		func(dataset *DatasetSpec) { dataset.EvalSetID = "" },
		func(dataset *DatasetSpec) { dataset.EvalSetHash = "" },
		func(dataset *DatasetSpec) { dataset.MetricsHash = "" },
		func(dataset *DatasetSpec) { dataset.CaseIDs = nil },
		func(dataset *DatasetSpec) { dataset.MetricNames = nil },
		func(dataset *DatasetSpec) { dataset.CaseIDs[1] = "a" },
		func(dataset *DatasetSpec) { dataset.MetricNames = []string{"quality", "quality"} },
		func(dataset *DatasetSpec) { delete(dataset.NormalizedInputHashes, "b") },
		func(dataset *DatasetSpec) { dataset.NormalizedInputHashes["a"] = "" },
		func(dataset *DatasetSpec) { dataset.NormalizedInputHashes["b"] = "hash-a" },
		func(dataset *DatasetSpec) {
			delete(dataset.NormalizedInputHashes, "b")
			dataset.NormalizedInputHashes["other"] = "hash-other"
		},
	} {
		dataset := validDataset()
		mutate(&dataset)
		require.Error(t, validateResolvedDataset("dataset", dataset))
	}
	require.NoError(t, validateResolvedDataset("dataset", validDataset()))

	for _, mutate := range []func(*Report){
		func(report *Report) { report.SearchProfile.Hash = "other" },
		func(report *Report) { report.ReleasedProfile.Hash = "other" },
		func(report *Report) { report.SearchProfile.EvaluationRunID = "other" },
		func(report *Report) { report.ReleasedProfile.EvaluationRunID = "other" },
		func(report *Report) { report.FinalDecision.Status = DecisionRejected },
	} {
		report := testReport(t)
		mutate(report)
		require.Error(t, validateSuccessfulReport(report))
	}
	report = testReport(t)
	report.Candidates = nil
	report.ReleasedProfile = withProfileRole(report.InitialProfile, ProfileReleased)
	report.FinalDecision = Decision{Status: DecisionAccepted, Reasons: []string{"accepted"}}
	require.Error(t, validateSuccessfulReport(report))
}

func TestLossHintsAndPipelineDecisionHelpers(t *testing.T) {
	require.Error(t, func() error {
		_, err := buildLossHints(nil)
		return err
	}())
	valid := func() *EvaluationSnapshot {
		snapshot := testSnapshot("profile", []string{"case"}, []string{"quality"})
		setSnapshotCases(snapshot, 0.2, testCase("case", false, 0.2))
		snapshot.Attributions = []FailureAttribution{{
			EvalSetID:           snapshot.Provenance.EvalSetID,
			EvalCaseID:          "case",
			MetricName:          "quality",
			PrimaryCategory:     FailureResponseMismatch,
			Reason:              "reason",
			Severity:            FailureSeverityP1,
			EvaluationRunID:     snapshot.Provenance.RunID,
			ProfileHash:         snapshot.Provenance.ProfileHash,
			EvidenceSufficiency: EvidenceSufficient,
			Evidence: []EvidenceReference{
				{ID: "one", Kind: "trace", Summary: "first"},
				{ID: "two", Kind: "", Summary: ""},
				{ID: "", Kind: "", Summary: strings.Repeat("x", 600)},
				{ID: "four", Kind: "tool", Summary: "fourth"},
				{ID: "five", Kind: "tool", Summary: "ignored"},
			},
		}}
		return snapshot
	}
	for _, mutate := range []func(*EvaluationSnapshot){
		func(snapshot *EvaluationSnapshot) {
			snapshot.Attributions[0].ProfileHash = "other"
		},
		func(snapshot *EvaluationSnapshot) {
			snapshot.Attributions = append(snapshot.Attributions, snapshot.Attributions[0])
		},
		func(snapshot *EvaluationSnapshot) {
			snapshot.Attributions = nil
		},
		func(snapshot *EvaluationSnapshot) {
			snapshot.Attributions[0].Reason = ""
		},
	} {
		snapshot := valid()
		mutate(snapshot)
		_, err := buildLossHints(snapshot)
		require.Error(t, err)
	}
	hints, err := buildLossHints(valid())
	require.NoError(t, err)
	require.Len(t, hints, 1)
	require.Contains(t, hints[0].Reason, "evidence:")
	require.LessOrEqual(t, len([]rune(hints[0].Reason)), 560)

	stopped, _ := modelCallThresholdStop(GatePolicy{}, ResourceUsage{})
	require.False(t, stopped)
	stopped, reason := modelCallThresholdStop(
		GatePolicy{ModelCallStopThreshold: 2},
		ResourceUsage{},
	)
	require.True(t, stopped)
	require.Contains(t, reason, "unavailable")
	stopped, reason = modelCallThresholdStop(
		GatePolicy{ModelCallStopThreshold: 2},
		ResourceUsage{ModelCalls: Count{Available: true, Value: 2}},
	)
	require.True(t, stopped)
	require.Contains(t, reason, "reached")
	stopped, _ = modelCallThresholdStop(
		GatePolicy{ModelCallStopThreshold: 2},
		ResourceUsage{ModelCalls: Count{Available: true, Value: 1}},
	)
	require.False(t, stopped)

	require.True(t, validEvaluableDecision(DecisionAccepted))
	require.True(t, validEvaluableDecision(DecisionRejected))
	require.False(t, validEvaluableDecision(DecisionNotEvaluable))
	require.Error(t, validateCandidateSnapshot("train", "profile", nil))
	require.Error(t, validateCandidateSnapshot(
		"train",
		"profile",
		&EvaluationSnapshot{
			Status: EvaluationCompleted,
			Provenance: EvaluationProvenance{
				ProfileHash: "other",
			},
		},
	))
	require.NoError(t, validateCandidateSnapshot(
		"train",
		"profile",
		&EvaluationSnapshot{
			Status: EvaluationCompleted,
			Provenance: EvaluationProvenance{
				ProfileHash: "profile",
			},
		},
	))
	require.Equal(t, ProfileReleased, withProfileRole(ProfileRecord{}, ProfileReleased).Role)
	transition := unchangedTransition("search", "released", "reason")
	require.Equal(t, transition.SearchBefore, transition.SearchAfter)
	require.Equal(t, DecisionNotEvaluable, notEvaluableDecision("reason").Status)
	require.Equal(t, []string{"context canceled", "context deadline exceeded"}, appendErrors(
		nil,
		context.Canceled,
		nil,
		context.DeadlineExceeded,
	))
	require.Equal(t, float64(1), *float64Pointer(1))
}

func TestIndependentProfileStateTransitionsCoverEveryDecisionCombination(t *testing.T) {
	candidateProfile := testProfileRecord(t, ProfileCandidate, "candidate")
	initialProfile := testProfileRecord(t, ProfileInitial, "initial")
	candidateTrain := &EvaluationSnapshot{
		Status:     EvaluationCompleted,
		Provenance: EvaluationProvenance{ProfileHash: candidateProfile.Hash},
	}
	candidateValidation := &EvaluationSnapshot{
		Status:     EvaluationCompleted,
		Provenance: EvaluationProvenance{ProfileHash: candidateProfile.Hash},
	}
	newState := func() *ProfileState {
		return &ProfileState{
			Initial:  initialProfile,
			Search:   withProfileRole(initialProfile, ProfileSearch),
			Released: withProfileRole(initialProfile, ProfileReleased),
		}
	}
	accepted := Decision{Status: DecisionAccepted, Reasons: []string{"accepted"}}
	rejected := Decision{Status: DecisionRejected, Reasons: []string{"rejected"}}

	_, err := ApplyStateTransition(
		nil,
		candidateProfile,
		candidateTrain,
		candidateValidation,
		accepted,
		accepted,
	)
	require.Error(t, err)
	for _, test := range []struct {
		name       string
		candidate  ProfileRecord
		train      *EvaluationSnapshot
		validation *EvaluationSnapshot
		search     Decision
		release    Decision
	}{
		{"invalid search", candidateProfile, candidateTrain, candidateValidation, Decision{Status: "bad"}, rejected},
		{"invalid release", candidateProfile, candidateTrain, candidateValidation, rejected, Decision{Status: "bad"}},
		{"empty candidate", ProfileRecord{}, candidateTrain, candidateValidation, accepted, rejected},
		{"search train missing", candidateProfile, nil, candidateValidation, accepted, rejected},
		{"search validation missing", candidateProfile, candidateTrain, nil, accepted, rejected},
		{"release validation missing", candidateProfile, candidateTrain, nil, rejected, accepted},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ApplyStateTransition(
				newState(),
				test.candidate,
				test.train,
				test.validation,
				test.search,
				test.release,
			)
			require.Error(t, err)
		})
	}

	for _, test := range []struct {
		name                    string
		search                  Decision
		release                 Decision
		wantSearch, wantRelease bool
	}{
		{"both accepted", accepted, accepted, true, true},
		{"search only", accepted, rejected, true, false},
		{"release only", rejected, accepted, false, true},
		{"neither", rejected, rejected, false, false},
		{
			"not evaluable",
			notEvaluableDecision("unknown"),
			accepted,
			false,
			false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := newState()
			transition, err := ApplyStateTransition(
				state,
				candidateProfile,
				candidateTrain,
				candidateValidation,
				test.search,
				test.release,
			)
			require.NoError(t, err)
			require.Equal(t, test.wantSearch, transition.SearchUpdated)
			require.Equal(t, test.wantRelease, transition.ReleaseUpdated)
			if test.wantSearch {
				require.Equal(t, candidateProfile.Hash, state.Search.Hash)
			}
			if test.wantRelease {
				require.Equal(t, candidateProfile.Hash, state.Released.Hash)
			}
		})
	}

	for _, acceptance := range []*promptiterengine.AcceptanceDecision{
		nil,
		{ScoreDelta: math.NaN()},
		{Accepted: true, ScoreDelta: 0.1},
		{Accepted: false, ScoreDelta: -0.1, Reason: "rejected"},
	} {
		decision := decideSearchFromNative(acceptance, DeltaSummary{ScoreDelta: 0.2})
		require.True(t, validDecisionStatus(decision.Status))
		require.NotEmpty(t, decision.Reasons)
	}
}

func TestSnapshotResponseValidationRejectsAllEvidenceContractViolations(t *testing.T) {
	valid := func() (SnapshotRequest, *EvaluationSnapshot) {
		config := pipelineRunConfig()
		hash, err := ProfileFingerprint(config.InitialProfile)
		require.NoError(t, err)
		request := SnapshotRequest{
			EvaluationRunID:     config.RunID + "/validation",
			Profile:             config.InitialProfile,
			ExpectedProfileHash: hash,
			Dataset: DatasetSpec{
				EvalSetID:   config.Validation.EvalSetID,
				EvalSetHash: config.Validation.EvalSetHash,
				MetricsHash: config.Validation.MetricsHash,
				CaseIDs:     []string{"heldout-a"},
				MetricNames: []string{"quality"},
			},
			Split:               "heldout_validation",
			Seed:                config.Seed,
			EvaluatorConfigHash: config.EvaluatorConfigHash,
			MetricPolicyHash:    config.MetricPolicyHash,
			PrimaryMetric:       "quality",
			MetricDirections:    map[string]ScoreDirection{"quality": ScoreHigherIsBetter},
			CriticalCaseIDs:     []string{"heldout-a"},
			HardFailureCaseIDs:  []string{"heldout-a"},
			EvidenceLimit:       4,
		}
		snapshot := pipelineSnapshot(request, 0.2, false, false)
		snapshot.Cases[0].Critical = true
		snapshot.Cases[0].HardFailure = true
		return request, snapshot
	}
	for _, test := range []struct {
		name   string
		mutate func(*SnapshotRequest, *EvaluationSnapshot)
	}{
		{"invalid status", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Status = "other"
		}},
		{"negative latency", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.LatencyMS = -1
		}},
		{"invalid resources", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Resources.LatencyMS.Value = -1
		}},
		{"invalid evidence limit", func(request *SnapshotRequest, _ *EvaluationSnapshot) {
			request.EvidenceLimit = 0
		}},
		{"unexpected case", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].CaseID = "other"
		}},
		{"duplicate case", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases = append(snapshot.Cases, snapshot.Cases[0])
		}},
		{"case eval set", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].EvalSetID = "other"
		}},
		{"case primary metric", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].PrimaryMetric = "other"
		}},
		{"critical flag", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].Critical = false
		}},
		{"hard flag", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].HardFailure = false
		}},
		{"case evidence limit", func(request *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].Trace = make([]TraceStep, request.EvidenceLimit+1)
		}},
		{"conflicting no-tool expectation", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].ExpectNoTools = true
			snapshot.Cases[0].ExpectedTools = []ToolCall{{Name: "lookup"}}
		}},
		{"unexpected metric", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].Metrics[0].MetricName = "other"
		}},
		{"duplicate metric", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].Metrics = append(
				snapshot.Cases[0].Metrics,
				snapshot.Cases[0].Metrics[0],
			)
		}},
		{"metric direction", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].Metrics[0].Direction = ScoreLowerIsBetter
		}},
		{"metric threshold", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].Metrics[0].Threshold = math.NaN()
		}},
		{"metric count", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].Metrics = nil
		}},
		{"case pass disagreement", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases[0].Passed = true
			snapshot.Cases[0].Status = "passed"
		}},
		{"duplicate attribution", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Attributions = append(snapshot.Attributions, snapshot.Attributions[0])
		}},
		{"attribution not failed", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Attributions[0].MetricName = "other"
		}},
		{"attribution reason", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Attributions[0].Reason = ""
		}},
		{"attribution evidence", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Attributions[0].Evidence = nil
		}},
		{"attribution evidence limit", func(request *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Attributions[0].Evidence = make(
				[]EvidenceReference,
				request.EvidenceLimit+1,
			)
		}},
		{"incomplete evidence", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Attributions[0].Evidence[0].Kind = ""
		}},
		{"snapshot error", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Error = "error"
		}},
		{"snapshot case count", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Cases = nil
			snapshot.Attributions = nil
		}},
		{"overall score", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.OverallScore = math.Inf(1)
		}},
		{"missing attribution", func(_ *SnapshotRequest, snapshot *EvaluationSnapshot) {
			snapshot.Attributions = nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, snapshot := valid()
			test.mutate(&request, snapshot)
			require.Error(t, validateSnapshotResponse(request, snapshot))
		})
	}

	request, snapshot := valid()
	snapshot.Status = EvaluationNotEvaluable
	snapshot.Cases = nil
	snapshot.Attributions = nil
	require.NoError(t, validateSnapshotResponse(request, snapshot))
}

type snapshotEvaluatorFunc func(
	context.Context,
	SnapshotRequest,
) (*EvaluationSnapshot, error)

type failingAgentEvaluator struct {
	err error
}

func (e failingAgentEvaluator) Evaluate(
	context.Context,
	string,
	...evaluation.Option,
) (*evaluation.EvaluationResult, error) {
	return nil, e.err
}

func (failingAgentEvaluator) Close() error {
	return nil
}

func (f snapshotEvaluatorFunc) Evaluate(
	ctx context.Context,
	request SnapshotRequest,
) (*EvaluationSnapshot, error) {
	return f(ctx, request)
}

func TestEvaluateSnapshotEnforcesEvaluatorErrorStatusContract(t *testing.T) {
	config := pipelineRunConfig()
	hash, err := ProfileFingerprint(config.InitialProfile)
	require.NoError(t, err)
	tests := []struct {
		name         string
		evaluator    SnapshotEvaluator
		wantSnapshot bool
	}{
		{
			name: "not evaluable status without error",
			evaluator: snapshotEvaluatorFunc(func(
				_ context.Context,
				request SnapshotRequest,
			) (*EvaluationSnapshot, error) {
				snapshot := pipelineSnapshot(request, 0.2, false, false)
				snapshot.Status = EvaluationNotEvaluable
				return snapshot, nil
			}),
			wantSnapshot: true,
		},
		{
			name: "snapshot with evaluator error",
			evaluator: snapshotEvaluatorFunc(func(
				_ context.Context,
				request SnapshotRequest,
			) (*EvaluationSnapshot, error) {
				return pipelineSnapshot(request, 0.2, false, false), context.Canceled
			}),
			wantSnapshot: true,
		},
		{
			name: "nil snapshot without error",
			evaluator: snapshotEvaluatorFunc(func(
				context.Context,
				SnapshotRequest,
			) (*EvaluationSnapshot, error) {
				return nil, nil
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pipeline := &Pipeline{evaluator: test.evaluator}
			var ledger ResourceLedger
			snapshot, err := pipeline.evaluateSnapshot(
				context.Background(),
				&config,
				config.InitialProfile,
				hash,
				config.Train,
				"train",
				"probe",
				1,
				&ledger,
				nil,
			)
			require.Error(t, err)
			require.Len(t, ledger.Entries, 1)
			if test.wantSnapshot {
				require.NotNil(t, snapshot)
				require.Equal(t, EvaluationNotEvaluable, snapshot.Status)
			} else {
				require.Nil(t, snapshot)
			}
		})
	}
}

func TestSuccessfulReportValidationRejectsEveryConfigurationBindingDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Report)
	}{
		{"generated timezone", func(report *Report) {
			report.GeneratedAt = report.GeneratedAt.In(time.FixedZone("offset", 3600))
		}},
		{"initial evaluation run", func(report *Report) {
			report.InitialProfile.EvaluationRunID = "other"
		}},
		{"promptiter policy", func(report *Report) {
			report.ResolvedConfig.PromptIter.MaxOuterRounds = 0
		}},
		{"gate", func(report *Report) {
			report.ResolvedConfig.Gate.Epsilon = 0
		}},
		{"output", func(report *Report) {
			report.ResolvedConfig.Output.JSON = ""
		}},
		{"evidence max", func(report *Report) {
			report.ResolvedConfig.EvidenceLimit = defaultEvidenceLimit + 1
		}},
		{"dataset id", func(report *Report) {
			report.ResolvedConfig.Train.EvalSetID = ""
		}},
		{"dataset hash", func(report *Report) {
			report.ResolvedConfig.Train.EvalSetHash = ""
		}},
		{"dataset metrics hash", func(report *Report) {
			report.ResolvedConfig.Train.MetricsHash = ""
		}},
		{"dataset cases", func(report *Report) {
			report.ResolvedConfig.Train.CaseIDs = nil
		}},
		{"dataset metrics", func(report *Report) {
			report.ResolvedConfig.Train.MetricNames = nil
		}},
		{"heldout leakage", func(report *Report) {
			report.ResolvedConfig.Validation.NormalizedInputHashes["validation-case"] =
				report.ResolvedConfig.Train.NormalizedInputHashes["train-case"]
		}},
		{"metric inventory", func(report *Report) {
			report.ResolvedConfig.Validation.MetricNames = []string{"other"}
		}},
		{"metric hashes", func(report *Report) {
			report.ResolvedConfig.Validation.MetricsHash = "other"
		}},
		{"primary metric", func(report *Report) {
			report.ResolvedConfig.Gate.PrimaryMetric = "other"
		}},
		{"direction count", func(report *Report) {
			report.ResolvedConfig.Gate.MetricDirections["other"] = ScoreHigherIsBetter
		}},
		{"internal validation", func(report *Report) {
			report.ResolvedConfig.PromptIter.InternalValidationStrategy =
				internalValidationTrainCaseIDs
			report.ResolvedConfig.PromptIter.InternalValidationCaseIDs =
				[]string{"validation-case"}
		}},
		{"critical absent", func(report *Report) {
			report.ResolvedConfig.CriticalCaseIDs = []string{"missing"}
		}},
		{"empty input hash", func(report *Report) {
			report.InputHashes["baselinePrompt"] = ""
		}},
		{"train input hash", func(report *Report) {
			report.InputHashes["trainEvalSet"] = "other"
		}},
		{"validation input hash", func(report *Report) {
			report.InputHashes["validationEvalSet"] = "other"
		}},
		{"metrics input hash", func(report *Report) {
			report.InputHashes["metrics"] = "other"
		}},
		{"candidate round", func(report *Report) {
			report.Candidates[0].Round = 2
		}},
		{"released parent", func(report *Report) {
			report.Candidates[0].ReleasedParentHash = "other"
		}},
		{"promptiter status", func(report *Report) {
			report.Candidates[0].PromptIterStatus = "failed"
		}},
		{"candidate profile evaluation run", func(report *Report) {
			report.Candidates[0].Profile.EvaluationRunID = "other"
		}},
		{"release score delta", func(report *Report) {
			value := 123.0
			report.Candidates[0].ReleaseDecision.ScoreDelta = &value
		}},
		{"profile structure", func(report *Report) {
			report.Candidates[0].Profile.StructureID = "other"
		}},
		{"profile target", func(report *Report) {
			report.Candidates[0].Profile.TargetSurfaceID = "other"
		}},
		{"payload structure", func(report *Report) {
			report.Candidates[0].Profile.Profile.StructureID = "other"
		}},
		{"target override absent", func(report *Report) {
			report.Candidates[0].Profile.Profile.Overrides = nil
		}},
		{"optimization reason", func(report *Report) {
			report.Candidates[0].OptimizationReason = "other"
		}},
		{"snapshot eval id", func(report *Report) {
			report.BaselineTrain.Provenance.EvalSetID = "other"
		}},
		{"snapshot case inventory", func(report *Report) {
			report.BaselineTrain.Inventory.CaseIDs = []string{"other"}
		}},
		{"snapshot metric inventory", func(report *Report) {
			report.BaselineTrain.Inventory.MetricNames = []string{"other"}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := testReport(t)
			test.mutate(report)
			_, err := RenderJSON(report)
			require.Error(t, err)
		})
	}
}

func TestPipelineCoversTrainingCompleteAndNativeFailureStops(t *testing.T) {
	t.Run("training already passes", func(t *testing.T) {
		engine := &pipelineStaticEngine{structure: pipelineTestStructure()}
		evaluator := snapshotEvaluatorFunc(func(
			_ context.Context,
			request SnapshotRequest,
		) (*EvaluationSnapshot, error) {
			return pipelineSnapshot(request, 1, true, false), nil
		})
		pipeline, err := New(engine, evaluator)
		require.NoError(t, err)
		config := pipelineRunConfig()
		report, err := pipeline.Run(context.Background(), &config)
		require.NoError(t, err)
		require.Equal(t, StopTrainingFailuresFixed, report.StopReason)
		require.Zero(t, engine.runCalls)
	})

	t.Run("native engine error", func(t *testing.T) {
		engine := &pipelineStaticEngine{
			structure: pipelineTestStructure(),
			err:       errors.New("native engine unavailable"),
		}
		pipeline, err := New(engine, &pipelineSnapshotEvaluator{})
		require.NoError(t, err)
		config := pipelineRunConfig()
		report, err := pipeline.Run(context.Background(), &config)
		require.NoError(t, err)
		require.Equal(t, PipelineRunFailed, report.Status)
		require.Equal(t, StopNecessaryRunFailed, report.StopReason)
		require.Len(t, report.Candidates, 1)
		require.Contains(t, strings.Join(report.Errors, " "), "native engine unavailable")
	})
}

func TestPipelineFailClosedStateMachineBranches(t *testing.T) {
	failedSnapshot := func(request SnapshotRequest, message string) (*EvaluationSnapshot, error) {
		snapshot := pipelineSnapshot(request, 0, false, false)
		snapshot.Status = EvaluationRunFailed
		snapshot.Error = message
		return snapshot, errors.New(message)
	}
	run := func(
		t *testing.T,
		result *promptiterengine.RunResult,
		evaluate snapshotEvaluatorFunc,
	) *Report {
		t.Helper()
		engine := &pipelineStaticEngine{
			structure: pipelineTestStructure(),
			result:    result,
		}
		pipeline, err := New(engine, evaluate)
		require.NoError(t, err)
		config := pipelineRunConfig()
		report, err := pipeline.Run(context.Background(), &config)
		require.NoError(t, err)
		return report
	}
	normalEvaluator := func(
		_ context.Context,
		request SnapshotRequest,
	) (*EvaluationSnapshot, error) {
		candidate := strings.Contains(pipelineProfileText(request.Profile), "candidate")
		score := 0.2
		passed := false
		if request.Split == "heldout_validation" {
			score = 0.4
		}
		if candidate {
			score = 0.9
			passed = true
		}
		return pipelineSnapshot(request, score, passed, false), nil
	}

	t.Run("baseline train failure", func(t *testing.T) {
		report := run(t, pipelineCandidateRunResult(), func(
			_ context.Context,
			request SnapshotRequest,
		) (*EvaluationSnapshot, error) {
			return failedSnapshot(request, "baseline train failed")
		})
		require.Equal(t, PipelineRunFailed, report.Status)
		require.Equal(t, StopNecessaryRunFailed, report.StopReason)
		require.Contains(t, report.Errors, "baseline train failed")
	})

	t.Run("baseline validation failure", func(t *testing.T) {
		report := run(t, pipelineCandidateRunResult(), func(
			ctx context.Context,
			request SnapshotRequest,
		) (*EvaluationSnapshot, error) {
			if request.Split == "heldout_validation" {
				return failedSnapshot(request, "baseline validation failed")
			}
			return normalEvaluator(ctx, request)
		})
		require.Equal(t, PipelineRunFailed, report.Status)
		require.Equal(t, StopNecessaryRunFailed, report.StopReason)
		require.Contains(t, report.Errors, "baseline validation failed")
	})

	t.Run("candidate validation failure", func(t *testing.T) {
		report := run(t, pipelineCandidateRunResult(), func(
			ctx context.Context,
			request SnapshotRequest,
		) (*EvaluationSnapshot, error) {
			if strings.Contains(pipelineProfileText(request.Profile), "candidate") &&
				request.Split == "heldout_validation" {
				return failedSnapshot(request, "candidate validation failed")
			}
			return normalEvaluator(ctx, request)
		})
		require.Equal(t, PipelineRunFailed, report.Status)
		require.Equal(t, StopNecessaryRunFailed, report.StopReason)
		require.Len(t, report.Candidates, 1)
		require.Equal(t, EvaluationNotEvaluable, report.Candidates[0].Status)
		require.Contains(t, report.Candidates[0].Errors, "candidate validation failed")
	})

	t.Run("no candidate patches", func(t *testing.T) {
		result := pipelineCandidateRunResult()
		initial := pipelineProfile("initial prompt")
		result.AcceptedProfile = initial
		result.Rounds[0].OutputProfile = initial
		result.Rounds[0].Patches = &promptiter.PatchSet{}
		result.Rounds[0].Acceptance.Accepted = false
		report := run(t, result, normalEvaluator)
		require.Equal(t, StopNoCandidate, report.StopReason)
		require.Len(t, report.Candidates, 1)
		require.Contains(t, report.Candidates[0].Errors, "native PromptIter produced no candidate patches")
	})

	t.Run("repeated initial fingerprint", func(t *testing.T) {
		result := pipelineCandidateRunResult()
		initial := pipelineProfile("initial prompt")
		result.AcceptedProfile = initial
		result.Rounds[0].OutputProfile = initial
		result.Rounds[0].Patches.Patches[0].Value.Text = stringPointer("initial prompt")
		report := run(t, result, normalEvaluator)
		require.Equal(t, StopRepeatedFingerprint, report.StopReason)
		require.Len(t, report.Candidates, 1)
		require.Contains(t, report.Candidates[0].Errors, "candidate profile fingerprint was already evaluated")
	})

	for _, field := range []string{"critical", "hard"} {
		t.Run("classification drift/"+field, func(t *testing.T) {
			report := run(t, pipelineCandidateRunResult(), func(
				ctx context.Context,
				request SnapshotRequest,
			) (*EvaluationSnapshot, error) {
				snapshot, err := normalEvaluator(ctx, request)
				if err == nil &&
					strings.Contains(pipelineProfileText(request.Profile), "candidate") &&
					request.Split == "heldout_validation" {
					if field == "critical" {
						snapshot.Cases[0].Critical = true
					} else {
						snapshot.Cases[0].HardFailure = true
					}
				}
				return snapshot, err
			})
			require.Equal(t, PipelineRunFailed, report.Status)
			require.Equal(t, StopNecessaryRunFailed, report.StopReason)
			require.Len(t, report.Candidates, 1)
			require.Contains(t, report.Errors[0], "candidate evaluation is not complete")
		})
	}
}

func TestPipelineModelCallThresholdStopsAtEveryOuterLoopBoundary(t *testing.T) {
	run := func(t *testing.T, threshold int64, meter ResourceMeter) *Report {
		t.Helper()
		engine := &pipelineStaticEngine{
			structure: pipelineTestStructure(),
			result:    pipelineCandidateRunResult(),
		}
		options := []PipelineOption{}
		if meter != nil {
			options = append(options, WithResourceMeter(meter))
		}
		pipeline, err := New(engine, &pipelineSnapshotEvaluator{}, options...)
		require.NoError(t, err)
		config := pipelineRunConfig()
		config.Gate.ModelCallStopThreshold = threshold
		bindPipelineRunConfig(&config)
		report, err := pipeline.Run(context.Background(), &config)
		require.NoError(t, err)
		require.Equal(t, PipelineModelCallThresholdStopped, report.Status)
		require.Equal(t, StopModelCallThresholdReached, report.StopReason)
		return report
	}

	report := run(t, 2, nil)
	require.Empty(t, report.Candidates)
	require.Equal(t, DecisionNotEvaluable, report.FinalDecision.Status)

	report = run(t, 3, nil)
	require.Len(t, report.Candidates, 1)
	require.Nil(t, report.Candidates[0].Train)
	require.Contains(t, strings.Join(report.Candidates[0].Errors, " "), "unavailable")

	for _, threshold := range []int64{3, 4} {
		meter := NewUsageMeter()
		meter.Record(ResourceUsage{
			ModelCalls:   Count{Available: true},
			InputTokens:  Count{Available: true},
			OutputTokens: Count{Available: true},
			LatencyMS:    Count{Available: true},
			MonetaryCost: Amount{Available: true, Unit: "USD"},
		})
		report = run(t, threshold, meter)
		require.Len(t, report.Candidates, 1)
		if threshold == 3 {
			require.NotNil(t, report.Candidates[0].Train)
			require.Nil(t, report.Candidates[0].Validation)
		} else {
			require.NotNil(t, report.Candidates[0].Validation)
		}
	}
}
