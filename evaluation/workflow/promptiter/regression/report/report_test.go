//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package report

import (
	"errors"
	"strings"
	"testing"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type failingReportPayload struct {
	secret string
}

func (failingReportPayload) MarshalJSON() ([]byte, error) {
	return nil, errors.New("payload cannot be encoded as JSON")
}

func (p failingReportPayload) String() string {
	return p.secret
}

func TestMarkdownIncludesPromptDeltaCasesAndGateReasons(t *testing.T) {
	baselineText := "baseline prompt"
	candidateText := "candidate prompt"
	baseline := &promptiter.Profile{
		StructureID: "structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value:     astructure.SurfaceValue{Text: &baselineText},
		}},
	}
	candidateProfile := &promptiter.Profile{
		StructureID: "structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value:     astructure.SurfaceValue{Text: &candidateText},
		}},
	}
	result := &regression.RunResult{
		RunID: "run-1", Status: regression.RunStatusSucceeded,
		Decision:  regression.DecisionRejected,
		StartedAt: time.Unix(1, 0).UTC(), EndedAt: time.Unix(2, 0).UTC(),
		Spec: &regression.RunSpec{
			TargetSurfaceID:  "agent#instruction",
			InputFingerprint: "fingerprint",
			Runtime:          regression.RuntimePolicy{Seed: 7, SeedApplied: true, NumRuns: 2},
		},
		PromptIter: &regression.PromptIterConfiguration{
			NumRuns: 2, MaxRounds: 4, MinScoreGain: .01,
			MaxRoundsWithoutAcceptance: 2,
			TargetSurfaceIDs:           []string{"agent#instruction"},
		},
		BaselineProfile:    baseline,
		BaselineTrain:      &regression.EvaluationSnapshot{OverallScore: .2, Complete: true, Cases: []regression.CaseResult{{CaseID: "train"}}},
		BaselineValidation: &regression.EvaluationSnapshot{OverallScore: .3, Complete: true, Cases: []regression.CaseResult{{CaseID: "validation"}}},
		Attributions: []regression.AttributionResult{{
			CaseID: "train", Category: regression.FailureFormat, Reason: "invalid JSON",
		}},
		AttributionCounts: map[regression.FailureCategory]int{regression.FailureFormat: 1},
		Candidates: []regression.CandidateResult{{
			Candidate:            regression.Candidate{ID: "candidate", Profile: candidateProfile},
			PromptIterAccepted:   true,
			ProfileChanged:       true,
			PromptIterShouldStop: true,
			PromptIterStopReason: "target score reached",
			RoundUsage: regression.UsageSummary{
				Calls: 2, TotalTokens: 30,
				CostEstimate:      regression.CostEstimate{EstimatedCost: .1, CostKnown: true},
				PromptIterLatency: 100 * time.Millisecond, Complete: true,
			},
			CumulativeUsage: regression.UsageSummary{
				Calls: 5, TotalTokens: 70,
				CostEstimate:      regression.CostEstimate{EstimatedCost: .25, CostKnown: true},
				PromptIterLatency: 250 * time.Millisecond, Complete: true,
			},
			TrainDelta: &regression.DeltaReport{BaselineScore: .2, CandidateScore: .8, WeightedScoreDelta: .6},
			ValidationDelta: &regression.DeltaReport{
				BaselineScore: .3, CandidateScore: .2, WeightedScoreDelta: -.1,
				NewFailures: 1,
				Cases: []regression.CaseDelta{{
					CaseID: "critical", Kind: regression.ChangeNewFail,
					BaselinePassed: true, CandidatePassed: false, Critical: true,
				}},
			},
			Gate: &regression.GateDecision{
				Decision: regression.DecisionRejected,
				Warnings: []string{"PromptIter accepted an audit-rejected round"},
				Rules: []regression.GateRuleResult{
					{
						Rule: "new_failures", Passed: false, Observed: 1, Threshold: 0,
						Reason: "validation introduced new failures",
					},
					{Rule: "validation_gain", Passed: true, Observed: .25, Threshold: .2},
				},
			},
		}},
	}
	encoded, err := JSON(result)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := Markdown(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"baseline prompt", "candidate prompt", "critical", "new_failures",
		"validation introduced new failures", "invalid JSON", "fingerprint", "| format_error | 1 |",
		"PromptIter accepted an audit-rejected round", "Random seed: `7` (applied)",
		"Started: `1970-01-01 00:00:01.000 UTC`", "Duration: `1.000 s`",
		"Hard round limit: `4`", "Acceptance minimum score gain: `0.01`",
		"Stop after consecutive unaccepted rounds: `2`", "Early-stop target score: `disabled`",
		"Effective profile change: `true`", "PromptIter stop: `target score reached`",
		"## Optimization progress", "| 0 | 0.3 | 0 | n/a | baseline | n/a |",
		"| 0.25 | 0.2 |",
		"Target surfaces: `agent#instruction`",
		"### Resources", "| round | 2 | 30 | 0.100000 | true | 100ms | true |",
		"| cumulative | 5 | 70 | 0.250000 | true | 250ms | true |",
	} {
		if !strings.Contains(string(markdown), expected) {
			t.Fatalf("markdown omitted %q:\n%s", expected, markdown)
		}
	}
	if !strings.Contains(string(encoded), "candidate prompt") || !strings.Contains(string(encoded), "attributionCounts") {
		t.Fatalf("JSON omitted candidate prompt or attribution counts: %s", encoded)
	}
}

func TestScenarioPromptAndMetadataCannotBreakMarkdownStructure(t *testing.T) {
	promptText := "Explain this fenced example:\n```json\n{\"ok\":true}\n```"
	result := &regression.RunResult{
		RunID: "markdown-safe",
		Spec: &regression.RunSpec{
			TargetSurfaceID:  "agent#instruction",
			InputFingerprint: "fingerprint",
			Runtime:          regression.RuntimePolicy{NumRuns: 1},
			Metadata: map[string]string{
				"model`name": "line one\nline `two`",
			},
		},
		BaselineProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value:     astructure.SurfaceValue{Text: &promptText},
		}}},
	}
	markdown, err := Markdown(result)
	if err != nil {
		t.Fatal(err)
	}
	value := string(markdown)
	if !strings.Contains(value, "````text\n"+promptText+"\n````") {
		t.Fatalf("prompt fence was not expanded safely:\n%s", value)
	}
	if !strings.Contains(value, "model\\`name") ||
		!strings.Contains(value, "line one<br>line \\`two\\`") {
		t.Fatalf("metadata was not escaped safely:\n%s", value)
	}
}

func TestScenarioRenderersDefensivelySanitizeDirectRunResults(t *testing.T) {
	promptText := "api_key=profile-secret"
	result := &regression.RunResult{
		RunID: "direct-render",
		Spec: &regression.RunSpec{
			TargetSurfaceID:  "agent#instruction",
			InputFingerprint: "fingerprint",
			Runtime:          regression.RuntimePolicy{NumRuns: 1},
			Metadata:         map[string]string{"authorization": "metadata-secret"},
		},
		BaselineProfile: &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: "agent#instruction",
			Value: astructure.SurfaceValue{
				Text:  &promptText,
				Model: &astructure.ModelRef{APIKey: "model-secret"},
				Tools: []astructure.ToolRef{{
					ID: "custom", InputSchema: &tool.Schema{
						Default: failingReportPayload{secret: "schema-payload-secret"},
					},
				}},
			},
		}}},
		BaselineTrain: &regression.EvaluationSnapshot{Cases: []regression.CaseResult{{
			CaseID: "case", Input: "password=input-secret",
			Runs: []regression.Observation{{
				FinalResponse: "Bearer response-secret",
				Tools: []regression.ToolObservation{{
					Name: "lookup", Arguments: `{"token":"argument-secret"}`,
				}},
			}},
		}}},
		Candidates: []regression.CandidateResult{{
			Candidate: regression.Candidate{ID: "candidate"},
			Gate: &regression.GateDecision{
				Decision: regression.DecisionAccepted,
				Rules: []regression.GateRuleResult{{
					Rule: "custom", Passed: true,
					Observed: failingReportPayload{secret: "gate-payload-secret"},
				}},
			},
		}},
	}
	jsonReport, err := JSON(result)
	if err != nil {
		t.Fatal(err)
	}
	markdown, err := Markdown(result)
	if err != nil {
		t.Fatal(err)
	}
	combined := string(jsonReport) + string(markdown)
	for _, secret := range []string{
		"profile-secret", "metadata-secret", "model-secret", "input-secret",
		"response-secret", "argument-secret", "schema-payload-secret",
		"gate-payload-secret",
	} {
		if strings.Contains(combined, secret) {
			t.Fatalf("renderer leaked %q: %s", secret, combined)
		}
	}
	if !strings.Contains(combined, "[UNSERIALIZABLE:") {
		t.Fatalf("renderer did not safely represent unsupported extension values: %s", combined)
	}
	if result.BaselineTrain.Cases[0].Input == "" ||
		result.BaselineTrain.Cases[0].Runs[0].FinalResponse == "" {
		t.Fatal("renderer mutated the caller's run result")
	}
}

func TestScenarioReportRendersNonTextOptimizationSurface(t *testing.T) {
	profile := &promptiter.Profile{Overrides: []promptiter.SurfaceOverride{{
		SurfaceID: "agent#model",
		Value: astructure.SurfaceValue{Model: &astructure.ModelRef{
			Provider: "test", Name: "deterministic-model",
		}},
	}}}
	markdown, err := Markdown(&regression.RunResult{
		RunID: "model-surface",
		Spec: &regression.RunSpec{
			TargetSurfaceID: "agent#model",
			Runtime:         regression.RuntimePolicy{NumRuns: 1},
		},
		BaselineProfile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	value := string(markdown)
	if !strings.Contains(value, "deterministic-model") || !strings.Contains(value, "Model") {
		t.Fatalf("model surface was not rendered in the report: %q", value)
	}
}
