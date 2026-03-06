//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator"
)

type agentInvocationTrace struct {
	EvalSetID   string                `json:"evalSetId,omitempty"`
	EvalCaseID  string                `json:"evalCaseId,omitempty"`
	RunID       int                   `json:"runId,omitempty"`
	Invocations []*evalset.Invocation `json:"invocations,omitempty"`
}

type judgeMetricTrace struct {
	MetricName   string                    `json:"metricName,omitempty"`
	Score        float64                   `json:"score,omitempty"`
	Threshold    float64                   `json:"threshold,omitempty"`
	EvalStatus   string                    `json:"evalStatus,omitempty"`
	Output       json.RawMessage           `json:"output,omitempty"`
	RubricScores []*evalresult.RubricScore `json:"rubricScores,omitempty"`
}

type judgeInvocationTrace struct {
	InvocationIndex int                `json:"invocationIndex,omitempty"`
	Metrics         []judgeMetricTrace `json:"metrics,omitempty"`
}

type judgeCaseTrace struct {
	EvalSetID   string                 `json:"evalSetId,omitempty"`
	EvalCaseID  string                 `json:"evalCaseId,omitempty"`
	RunID       int                    `json:"runId,omitempty"`
	Invocations []judgeInvocationTrace `json:"invocations,omitempty"`
}

func writeRoundArtifactsForRound(outputDir string, round *promptiterator.Round) error {
	if strings.TrimSpace(outputDir) == "" {
		return errors.New("output dir is empty")
	}
	if round == nil {
		return errors.New("round is nil")
	}
	roundDir := filepath.Join(outputDir, fmt.Sprintf("round_%02d", round.Index))
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		return fmt.Errorf("create round dir: %w", err)
	}
	if err := writeTextFile(filepath.Join(roundDir, "prompt.md"), round.Prompt); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(roundDir, "issues.json"), round.Issues); err != nil {
		return err
	}
	if round.Gradient != nil {
		if err := writeJSONFile(filepath.Join(roundDir, "aggregator.json"), round.Gradient); err != nil {
			return err
		}
	}
	if strings.TrimSpace(round.OptimizedPrompt) != "" {
		if err := writeTextFile(filepath.Join(roundDir, "optimizer.md"), round.OptimizedPrompt); err != nil {
			return err
		}
	}
	if err := writeEvaluationArtifacts(roundDir, round.EvalResults); err != nil {
		return err
	}
	return nil
}

func writeEvaluationArtifacts(roundDir string, evalResults map[string]*evaluation.EvaluationResult) error {
	if len(evalResults) == 0 {
		return errors.New("eval results are empty")
	}
	evalDir := filepath.Join(roundDir, "evaluation")
	if err := os.MkdirAll(evalDir, 0o755); err != nil {
		return fmt.Errorf("create evaluation dir: %w", err)
	}
	usedCaseDirs := make(map[string]string)
	for evalSetID, res := range evalResults {
		if strings.TrimSpace(evalSetID) == "" {
			return errors.New("eval set id is empty")
		}
		if res == nil || res.EvalResult == nil {
			return fmt.Errorf("evaluation result for %s is nil", evalSetID)
		}
		safeEvalSetID, err := sanitizePathSegment(evalSetID)
		if err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(evalDir, safeEvalSetID+".json"), res.EvalResult); err != nil {
			return err
		}
		if err := writeAgentOutputs(roundDir, evalSetID, res.EvalResult, usedCaseDirs); err != nil {
			return err
		}
	}
	return nil
}

func writeAgentOutputs(roundDir string, evalSetID string, evalSetResult *evalresult.EvalSetResult, usedCaseDirs map[string]string) error {
	if strings.TrimSpace(evalSetID) == "" {
		return errors.New("eval set id is empty")
	}
	if evalSetResult == nil {
		return errors.New("eval set result is nil")
	}
	if usedCaseDirs == nil {
		return errors.New("used case dirs map is nil")
	}
	for _, caseResult := range evalSetResult.EvalCaseResults {
		if caseResult == nil {
			return errors.New("eval case result is nil")
		}
		safeCaseID, err := sanitizePathSegment(caseResult.EvalID)
		if err != nil {
			return err
		}
		origin := fmt.Sprintf("%s/%s/run_%d", evalSetID, caseResult.EvalID, caseResult.RunID)
		if prev, ok := usedCaseDirs[safeCaseID]; ok {
			return fmt.Errorf("eval case dir collision for %q: %s and %s", safeCaseID, prev, origin)
		}
		usedCaseDirs[safeCaseID] = origin
		caseDir := filepath.Join(roundDir, safeCaseID)
		if err := os.MkdirAll(caseDir, 0o755); err != nil {
			return fmt.Errorf("create case dir: %w", err)
		}
		actualInvocations := make([]*evalset.Invocation, 0)
		expectedInvocations := make([]*evalset.Invocation, 0)
		judgeTrace := judgeCaseTrace{
			EvalSetID:   evalSetID,
			EvalCaseID:  caseResult.EvalID,
			RunID:       caseResult.RunID,
			Invocations: make([]judgeInvocationTrace, 0),
		}
		for invIdx, perInvocation := range caseResult.EvalMetricResultPerInvocation {
			if perInvocation == nil {
				continue
			}
			if perInvocation.ActualInvocation != nil {
				actualInvocations = append(actualInvocations, perInvocation.ActualInvocation)
			}
			if perInvocation.ExpectedInvocation != nil {
				expectedInvocations = append(expectedInvocations, perInvocation.ExpectedInvocation)
			}
			metrics := make([]judgeMetricTrace, 0)
			for _, metricResult := range perInvocation.EvalMetricResults {
				if metricResult == nil || metricResult.Criterion == nil || metricResult.Criterion.LLMJudge == nil {
					continue
				}
				var output json.RawMessage
				var rubricScores []*evalresult.RubricScore
				if metricResult.Details != nil {
					raw := strings.TrimSpace(metricResult.Details.Reason)
					if raw != "" {
						if !json.Valid([]byte(raw)) {
							return fmt.Errorf("judge output is not valid json (evalSet=%s, evalCase=%s, metric=%s)", evalSetID, caseResult.EvalID, metricResult.MetricName)
						}
						output = json.RawMessage(raw)
					}
					rubricScores = metricResult.Details.RubricScores
				}
				metrics = append(metrics, judgeMetricTrace{
					MetricName:   metricResult.MetricName,
					Score:        metricResult.Score,
					Threshold:    metricResult.Threshold,
					EvalStatus:   string(metricResult.EvalStatus),
					Output:       output,
					RubricScores: rubricScores,
				})
			}
			if len(metrics) != 0 {
				judgeTrace.Invocations = append(judgeTrace.Invocations, judgeInvocationTrace{
					InvocationIndex: invIdx,
					Metrics:         metrics,
				})
			}
		}
		candidateTrace := agentInvocationTrace{
			EvalSetID:   evalSetID,
			EvalCaseID:  caseResult.EvalID,
			RunID:       caseResult.RunID,
			Invocations: actualInvocations,
		}
		if err := writeJSONFile(filepath.Join(caseDir, "candidate.json"), candidateTrace); err != nil {
			return err
		}
		teacherTrace := agentInvocationTrace{
			EvalSetID:   evalSetID,
			EvalCaseID:  caseResult.EvalID,
			RunID:       caseResult.RunID,
			Invocations: expectedInvocations,
		}
		if err := writeJSONFile(filepath.Join(caseDir, "teacher.json"), teacherTrace); err != nil {
			return err
		}
		if err := writeJSONFile(filepath.Join(caseDir, "judge.json"), judgeTrace); err != nil {
			return err
		}
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is empty")
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func writeTextFile(path string, content string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is empty")
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func sanitizePathSegment(segment string) (string, error) {
	segment = strings.TrimSpace(segment)
	if segment == "" {
		return "", errors.New("path segment is empty")
	}
	var b strings.Builder
	for _, r := range segment {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "", fmt.Errorf("path segment %q is not usable", segment)
	}
	return out, nil
}
