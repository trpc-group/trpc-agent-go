//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildReportAndWriters(t *testing.T) {
	baselineTrain, err := Summarize(pipelineResult("train", 0.4, false))
	if err != nil {
		t.Fatal(err)
	}
	baselineValidation, err := Summarize(pipelineResult("validation", 0.4, false))
	if err != nil {
		t.Fatal(err)
	}
	candidateTrain, err := Summarize(pipelineResult("train", 0.8, true))
	if err != nil {
		t.Fatal(err)
	}
	candidateValidation, err := Summarize(pipelineResult("validation", 0.8, true))
	if err != nil {
		t.Fatal(err)
	}
	trainDelta, _ := Compare(baselineTrain, candidateTrain)
	validationDelta, _ := Compare(baselineValidation, candidateValidation)
	attribution, _ := Attribute(candidateValidation)
	run := &OptimizationRun{
		InitialPrompt: "baseline prompt", AcceptedPrompt: "candidate prompt",
		BaselineTrain: baselineTrain, BaselineValidation: baselineValidation,
		AcceptedTrain: candidateTrain, AcceptedValidation: candidateValidation,
		Rounds: []Round{{
			Number: 1, InputPrompt: "baseline prompt", CandidatePrompt: "candidate prompt",
			Train: candidateTrain, Validation: candidateValidation,
			TrainDelta: trainDelta, ValidationDelta: validationDelta, Attribution: attribution,
			Gate: &GateDecision{Accepted: true}, ServingCost: Cost{ModelCalls: 2, Tokens: 20},
		}},
		TotalCost:            Cost{ModelCalls: 4, Tokens: 40, LatencyMS: 10},
		WriteBackRecommended: true, StopReason: "candidate accepted by regression gate", Seed: 2003,
	}
	started := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	report, err := BuildReport(run, AuditMetadata{
		RunID: "run-2003", StartedAt: started, FinishedAt: started.Add(2 * time.Second),
		Inputs: []AuditInput{
			{Name: "validation", Path: "/private/data/validation.evalset.json", SHA256: "def"},
			{Name: "train", Path: "/private/data/train.evalset.json", SHA256: "abc"},
		},
		Runtime: RuntimeAudit{
			Mode: "fake", Model: "deterministic", Config: map[string]string{
				"openai_api_key": "must-not-leak", "scenario": "regression",
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildReport() error = %v", err)
	}
	if report.Status != "accepted" || report.Audit.DurationMS != 2000 || report.Audit.Seed != 2003 {
		t.Fatalf("report = %+v", report)
	}
	if report.Audit.Inputs[0].Name != "train" || report.Audit.Inputs[0].Path != "train.evalset.json" {
		t.Fatalf("inputs = %+v", report.Audit.Inputs)
	}
	if report.Audit.Runtime.Config["openai_api_key"] != "<redacted>" || report.InitialPrompt.SHA256 == "" {
		t.Fatalf("audit = %+v, prompt = %+v", report.Audit, report.InitialPrompt)
	}

	var jsonOutput bytes.Buffer
	if err := WriteJSON(&jsonOutput, report); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	var decoded OptimizationReport
	if err := json.Unmarshal(jsonOutput.Bytes(), &decoded); err != nil {
		t.Fatalf("JSON is invalid: %v", err)
	}
	if decoded.Decision.StopReason != run.StopReason || strings.Contains(jsonOutput.String(), "must-not-leak") || strings.Contains(jsonOutput.String(), "/private/") {
		t.Fatalf("JSON output leaked or lost data: %s", jsonOutput.String())
	}
	metric := decoded.BaselineTrain.Cases[0].Metrics[0]
	if metric.Passed || !metric.Evaluated || metric.Threshold != 0.5 || metric.Reason == "" {
		t.Fatalf("report metric lost evaluation facts: %+v", metric)
	}

	var markdown bytes.Buffer
	if err := WriteMarkdown(&markdown, report); err != nil {
		t.Fatalf("WriteMarkdown() error = %v", err)
	}
	for _, text := range []string{
		"Status: **accepted**", "Train failures", "Validation failures", "Round 1",
		"Validation score", "Evaluation cost: 2 calls, 20 tokens", "Changed metrics", "newly_passed", "Model calls: 4",
	} {
		if !strings.Contains(markdown.String(), text) {
			t.Fatalf("Markdown does not contain %q:\n%s", text, markdown.String())
		}
	}
}

func TestBuildReportWithoutCandidate(t *testing.T) {
	baseline, err := Summarize(pipelineResult("train", 1, true))
	if err != nil {
		t.Fatal(err)
	}
	report, err := BuildReport(&OptimizationRun{
		InitialPrompt: "baseline", AcceptedPrompt: "baseline",
		BaselineTrain: baseline, BaselineValidation: baseline,
		StopReason: "no optimizable training failures",
	}, validAudit())
	if err != nil {
		t.Fatalf("BuildReport() error = %v", err)
	}
	if report.Status != "not_optimized" || report.Decision.Accepted {
		t.Fatalf("report = %+v", report)
	}
	var markdown bytes.Buffer
	if err := WriteMarkdown(&markdown, report); err != nil || !strings.Contains(markdown.String(), "No candidate was generated") {
		t.Fatalf("WriteMarkdown() = %v\n%s", err, markdown.String())
	}
}

func TestBuildReportRejectsIncompleteInputs(t *testing.T) {
	if _, err := BuildReport(nil, validAudit()); err == nil {
		t.Fatal("BuildReport(nil) error = nil")
	}
	baseline, _ := Summarize(pipelineResult("train", 1, true))
	if _, err := BuildReport(&OptimizationRun{
		InitialPrompt: "prompt", AcceptedPrompt: "prompt", BaselineTrain: baseline,
		BaselineValidation: baseline, StopReason: "done",
	}, AuditMetadata{}); err == nil {
		t.Fatal("BuildReport() accepted incomplete audit")
	}
	if _, err := BuildReport(&OptimizationRun{
		InitialPrompt: "prompt", AcceptedPrompt: "prompt", BaselineTrain: baseline,
		BaselineValidation: baseline, StopReason: "done", Rounds: []Round{{Number: 1}},
	}, validAudit()); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("BuildReport() incomplete round error = %v", err)
	}
	for _, run := range []*OptimizationRun{
		{BaselineTrain: baseline, BaselineValidation: baseline, StopReason: "done"},
		{InitialPrompt: "prompt", AcceptedPrompt: "prompt", BaselineTrain: baseline, BaselineValidation: baseline},
		{InitialPrompt: "prompt", AcceptedPrompt: "prompt", BaselineTrain: baseline, BaselineValidation: baseline,
			StopReason: "done", Rounds: []Round{{Number: 1, Train: baseline, Validation: baseline,
				TrainDelta: &DatasetDelta{}, ValidationDelta: &DatasetDelta{}, Gate: &GateDecision{}, ServingCost: Cost{Tokens: -1}}}},
	} {
		if _, err := BuildReport(run, validAudit()); err == nil {
			t.Fatalf("BuildReport(%+v) error = nil", run)
		}
	}
}

func TestReportRejectsInvalidWritersAndAudit(t *testing.T) {
	baseline, _ := Summarize(pipelineResult("train", 1, true))
	run := &OptimizationRun{InitialPrompt: "prompt", AcceptedPrompt: "prompt", BaselineTrain: baseline,
		BaselineValidation: baseline, StopReason: "done"}
	if err := WriteJSON(nil, &OptimizationReport{}); err == nil {
		t.Fatal("WriteJSON(nil) error = nil")
	}
	if err := WriteJSON(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("WriteJSON(nil report) error = nil")
	}
	if err := WriteMarkdown(nil, &OptimizationReport{}); err == nil {
		t.Fatal("WriteMarkdown(nil) error = nil")
	}
	if err := WriteMarkdown(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("WriteMarkdown(nil report) error = nil")
	}
	started := time.Now().UTC()
	audits := []AuditMetadata{
		{RunID: "run", StartedAt: started, FinishedAt: started.Add(-time.Second), Runtime: RuntimeAudit{Mode: "fake"}},
		{RunID: "run", StartedAt: started, FinishedAt: started, Runtime: RuntimeAudit{}},
		{RunID: "run", StartedAt: started, FinishedAt: started, Runtime: RuntimeAudit{Mode: "fake"}, Inputs: []AuditInput{{SHA256: "hash"}}},
		{RunID: "run", StartedAt: started, FinishedAt: started, Runtime: RuntimeAudit{Mode: "fake"}, Inputs: []AuditInput{{Name: "input"}}},
	}
	for _, audit := range audits {
		if _, err := BuildReport(run, audit); err == nil {
			t.Fatalf("BuildReport(%+v) error = nil", audit)
		}
	}
}

func validAudit() AuditMetadata {
	started := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	return AuditMetadata{
		RunID: "run", StartedAt: started, FinishedAt: started.Add(time.Second),
		Runtime: RuntimeAudit{Mode: "fake"},
	}
}
