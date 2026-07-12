//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Config carries the reproducible regression-loop inputs.
type Config struct {
	AppName             string            `json:"appName"`
	PromptSource        string            `json:"promptSource"`
	MetricsPath         string            `json:"metricsPath"`
	TrainEvalSetID      string            `json:"trainEvalSetId"`
	ValidationEvalSetID string            `json:"validationEvalSetId"`
	Scenario            string            `json:"scenario,omitempty"`
	OutputJSON          string            `json:"outputJson"`
	OutputMarkdown      string            `json:"outputMarkdown"`
	Seed                int64             `json:"seed"`
	TargetSurfaceIDs    []string          `json:"targetSurfaceIds"`
	Gate                GateConfig        `json:"gate"`
	PromptIter          PromptIterConfig  `json:"promptiter"`
	ModelConfig         map[string]string `json:"modelConfig,omitempty"`
	FakeConfig          map[string]string `json:"fakeConfig,omitempty"`
	Attribution         AttributionConfig `json:"attribution,omitempty"`
}

// PromptIterConfig stores the outer-loop defaults used to build a RunRequest.
type PromptIterConfig struct {
	MaxRounds                  int      `json:"maxRounds"`
	MinScoreGain               float64  `json:"minScoreGain"`
	MaxRoundsWithoutAcceptance int      `json:"maxRoundsWithoutAcceptance"`
	TargetScore                *float64 `json:"targetScore,omitempty"`
	EvalCaseParallelism        int      `json:"evalCaseParallelism,omitempty"`
	ParallelInferenceEnabled   bool     `json:"parallelInferenceEnabled,omitempty"`
	ParallelEvaluationEnabled  bool     `json:"parallelEvaluationEnabled,omitempty"`
	BackwardParallelismEnabled bool     `json:"backwardParallelismEnabled,omitempty"`
	BackwardParallelism        int      `json:"backwardParallelism,omitempty"`
	SurfaceParallelismEnabled  bool     `json:"surfaceParallelismEnabled,omitempty"`
	SurfaceParallelism         int      `json:"surfaceParallelism,omitempty"`
}

// Validate checks required regression-loop inputs.
func (c Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.AppName) == "" {
		errs = append(errs, errors.New("app name is empty"))
	}
	if strings.TrimSpace(c.PromptSource) == "" {
		errs = append(errs, errors.New("prompt source is empty"))
	}
	if strings.TrimSpace(c.MetricsPath) == "" {
		errs = append(errs, errors.New("metrics path is empty"))
	}
	if strings.TrimSpace(c.TrainEvalSetID) == "" {
		errs = append(errs, errors.New("train eval set id is empty"))
	}
	if strings.TrimSpace(c.ValidationEvalSetID) == "" {
		errs = append(errs, errors.New("validation eval set id is empty"))
	}
	if strings.TrimSpace(c.OutputJSON) == "" {
		errs = append(errs, errors.New("output JSON path is empty"))
	}
	if strings.TrimSpace(c.OutputMarkdown) == "" {
		errs = append(errs, errors.New("output markdown path is empty"))
	}
	if len(c.TargetSurfaceIDs) == 0 {
		errs = append(errs, errors.New("target surface ids are empty"))
	}
	for _, surfaceID := range c.TargetSurfaceIDs {
		if strings.TrimSpace(surfaceID) == "" {
			errs = append(errs, errors.New("target surface id is empty"))
		}
	}
	if c.PromptIter.MaxRounds <= 0 {
		errs = append(errs, errors.New("promptiter max rounds must be greater than 0"))
	}
	if c.Gate.RequireEngineAccepted && c.PromptIter.MaxRounds <= 0 {
		errs = append(errs, errors.New("engine acceptance requires promptiter rounds"))
	}
	for metricName, category := range c.Attribution.MetricCategoryHints {
		category = normalizeFailureCategory(category)
		if strings.TrimSpace(metricName) == "" {
			errs = append(errs, errors.New("attribution metric category hint has empty metric name"))
			continue
		}
		if category == "" || !knownFailureCategory(category) {
			errs = append(errs, fmt.Errorf(
				"attribution metric %q has unknown failure category %q",
				metricName,
				c.Attribution.MetricCategoryHints[metricName],
			))
		}
	}
	for _, metricName := range c.Gate.HardFailMetricNames {
		if strings.TrimSpace(metricName) == "" {
			errs = append(errs, errors.New("gate hard fail metric name is empty"))
		}
	}
	return errors.Join(errs...)
}

// BuildRunRequest builds a PromptIter RunRequest from the config and train loss hints.
func (c Config) BuildRunRequest(lossHints []promptiterengine.LossHint) *promptiterengine.RunRequest {
	targetScore := c.PromptIter.TargetScore
	request := &promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{
				EvalSetID: c.TrainEvalSetID,
				LossHints: lossHints,
			},
		},
		Validation: []promptiterengine.EvalSetInput{
			{
				EvalSetID: c.ValidationEvalSetID,
			},
		},
		EvaluationOptions: promptiterengine.EvaluationOptions{
			EvalCaseParallelism:               c.PromptIter.EvalCaseParallelism,
			EvalCaseParallelInferenceEnabled:  c.PromptIter.ParallelInferenceEnabled,
			EvalCaseParallelEvaluationEnabled: c.PromptIter.ParallelEvaluationEnabled,
		},
		BackwardOptions: promptiterengine.BackwardOptions{
			CaseParallelismEnabled: c.PromptIter.BackwardParallelismEnabled,
			CaseParallelism:        c.PromptIter.BackwardParallelism,
		},
		AggregationOptions: promptiterengine.AggregationOptions{
			SurfaceParallelismEnabled: c.PromptIter.SurfaceParallelismEnabled,
			SurfaceParallelism:        c.PromptIter.SurfaceParallelism,
		},
		OptimizerOptions: promptiterengine.OptimizerOptions{
			SurfaceParallelismEnabled: c.PromptIter.SurfaceParallelismEnabled,
			SurfaceParallelism:        c.PromptIter.SurfaceParallelism,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: c.PromptIter.MinScoreGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: c.PromptIter.MaxRoundsWithoutAcceptance,
			TargetScore:                targetScore,
		},
		MaxRounds:        c.PromptIter.MaxRounds,
		TargetSurfaceIDs: append([]string(nil), c.TargetSurfaceIDs...),
	}
	return request
}

// LoadMetricDefinitions reads metrics.json in either array form or
// {"metrics":[...]} form. Only stable audit fields are retained.
func LoadMetricDefinitions(path string) ([]MetricDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read metrics path: %w", err)
	}
	var defs []MetricDefinition
	if err := json.Unmarshal(data, &defs); err == nil {
		return normalizeMetricDefinitions(defs)
	}
	var wrapped struct {
		Metrics []MetricDefinition `json:"metrics"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, fmt.Errorf("decode metrics path: %w", err)
	}
	return normalizeMetricDefinitions(wrapped.Metrics)
}

func normalizeMetricDefinitions(defs []MetricDefinition) ([]MetricDefinition, error) {
	out := make([]MetricDefinition, 0, len(defs))
	seen := map[string]struct{}{}
	for _, def := range defs {
		def.MetricName = strings.TrimSpace(def.MetricName)
		def.EvaluatorName = strings.TrimSpace(def.EvaluatorName)
		def.FailureCategory = normalizeFailureCategory(def.FailureCategory)
		if def.MetricName == "" {
			return nil, errors.New("metrics path contains metric with empty metricName")
		}
		if def.FailureCategory != "" && !knownFailureCategory(def.FailureCategory) {
			return nil, fmt.Errorf(
				"metrics path contains unknown failureCategory %q for metric %q",
				def.FailureCategory,
				def.MetricName,
			)
		}
		if _, ok := seen[def.MetricName]; ok {
			return nil, fmt.Errorf("metrics path contains duplicate metric %q", def.MetricName)
		}
		seen[def.MetricName] = struct{}{}
		out = append(out, def)
	}
	return out, nil
}

func metricNames(defs []MetricDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		if strings.TrimSpace(def.MetricName) != "" {
			names = append(names, def.MetricName)
		}
	}
	return names
}

// MarshalJSON writes a duration as Go's readable duration string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// UnmarshalJSON reads a duration from either a string or a numeric nanosecond value.
func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		parsed, err := time.ParseDuration(text)
		if err != nil {
			return fmt.Errorf("parse duration %q: %w", text, err)
		}
		d.Duration = parsed
		return nil
	}
	var nanos int64
	if err := json.Unmarshal(data, &nanos); err != nil {
		return err
	}
	d.Duration = time.Duration(nanos)
	return nil
}
