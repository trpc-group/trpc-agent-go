//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regression"
)

type inputs struct {
	train          evalset.EvalSet
	validation     evalset.EvalSet
	metrics        []*metric.EvalMetric
	config         runConfig
	baselinePrompt string
	fingerprint    string
}

type runConfig struct {
	Seed            int64                              `json:"seed"`
	NumRuns         int                                `json:"numRuns"`
	MaxRounds       int                                `json:"maxRounds"`
	TargetScore     *float64                           `json:"targetScore,omitempty"`
	CriticalCaseIDs []string                           `json:"criticalCaseIds,omitempty"`
	MetricPolicies  map[string]regression.MetricPolicy `json:"metricPolicies"`
	Gate            regression.GatePolicy              `json:"gate"`
	Budget          regression.BudgetPolicy            `json:"budget"`
	Audit           regression.AuditPolicy             `json:"audit,omitempty"`
}

func loadInputs(root string) (*inputs, error) {
	loaded := &inputs{}
	files := []struct {
		name   string
		target any
	}{
		{"train.evalset.json", &loaded.train},
		{"validation.evalset.json", &loaded.validation},
		{"metrics.json", &loaded.metrics},
		{"promptiter.json", &loaded.config},
	}
	raw := make(map[string][]byte, len(files)+1)
	for _, file := range files {
		content, err := os.ReadFile(filepath.Join(root, file.name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", file.name, err)
		}
		content = normalizeText(content)
		raw[file.name] = content
		if err := decodeStrict(file.name, content, file.target); err != nil {
			return nil, err
		}
	}
	prompt, err := os.ReadFile(filepath.Join(root, "baseline_prompt.txt"))
	if err != nil {
		return nil, fmt.Errorf("read baseline prompt: %w", err)
	}
	prompt = normalizeText(prompt)
	raw["baseline_prompt.txt"] = prompt
	loaded.baselinePrompt = strings.TrimSpace(string(prompt))
	if err := loaded.validate(); err != nil {
		return nil, err
	}
	loaded.fingerprint = fingerprint(raw)
	return loaded, nil
}

func normalizeText(content []byte) []byte {
	value := strings.ReplaceAll(string(content), "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return []byte(value)
}

func decodeStrict(name string, content []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode %s: trailing JSON value", name)
	}
	return nil
}

func (i *inputs) validate() error {
	if i.train.EvalSetID == "" || i.validation.EvalSetID == "" ||
		i.train.EvalSetID == i.validation.EvalSetID {
		return errors.New("train and validation eval set ids must be present and different")
	}
	if len(i.train.EvalCases) < 3 || len(i.validation.EvalCases) < 3 {
		return errors.New("train and validation eval sets must each contain at least three cases")
	}
	if len(i.metrics) == 0 || len(i.config.MetricPolicies) == 0 || i.baselinePrompt == "" {
		return errors.New("metrics, metric policies, and baseline prompt are required")
	}
	if i.config.NumRuns <= 0 || i.config.MaxRounds <= 0 {
		return errors.New("numRuns and maxRounds must be greater than zero")
	}
	if i.config.TargetScore != nil &&
		(!finiteScore(*i.config.TargetScore) || *i.config.TargetScore <= 0 || *i.config.TargetScore > 1) {
		return errors.New("targetScore must be greater than zero and at most one")
	}
	if err := i.validateEvalCases(); err != nil {
		return err
	}
	if err := i.validateMetrics(); err != nil {
		return err
	}
	if err := i.validateCriticalCases(); err != nil {
		return err
	}
	return (&regression.RunSpec{
		RunID:            "config-validation",
		TargetSurfaceID:  "configured-at-runtime",
		MetricPolicies:   i.config.MetricPolicies,
		CriticalCaseIDs:  i.config.CriticalCaseIDs,
		Gate:             i.config.Gate,
		Budget:           i.config.Budget,
		Runtime:          regression.RuntimePolicy{Seed: i.config.Seed, NumRuns: i.config.NumRuns, Deterministic: true},
		Audit:            i.config.Audit,
		InputFingerprint: "computed-after-validation",
	}).Validate()
}

func finiteScore(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (i *inputs) validateEvalCases() error {
	seen := make(map[string]struct{}, len(i.train.EvalCases)+len(i.validation.EvalCases))
	for _, set := range []*evalset.EvalSet{&i.train, &i.validation} {
		for _, evaluationCase := range set.EvalCases {
			if err := validateEvalCase(evaluationCase); err != nil {
				return err
			}
			if _, exists := seen[evaluationCase.EvalID]; exists {
				return fmt.Errorf("duplicate evaluation case id %q", evaluationCase.EvalID)
			}
			seen[evaluationCase.EvalID] = struct{}{}
		}
	}
	return nil
}

func validateEvalCase(evaluationCase *evalset.EvalCase) error {
	if evaluationCase == nil || strings.TrimSpace(evaluationCase.EvalID) == "" ||
		len(evaluationCase.Conversation) == 0 {
		return errors.New("every evaluation case requires evalId and conversation")
	}
	for _, invocation := range evaluationCase.Conversation {
		if invocation == nil || invocation.UserContent == nil || invocation.FinalResponse == nil {
			return fmt.Errorf("case %q requires userContent and finalResponse", evaluationCase.EvalID)
		}
	}
	return nil
}

func (i *inputs) validateMetrics() error {
	configuredNames := make(map[string]struct{}, len(i.metrics))
	for _, configuredMetric := range i.metrics {
		if configuredMetric == nil || configuredMetric.MetricName == "" || configuredMetric.EvaluatorName == "" {
			return errors.New("every metric requires metricName and evaluatorName")
		}
		if _, exists := i.config.MetricPolicies[configuredMetric.MetricName]; !exists {
			return fmt.Errorf("metric %q has no release policy", configuredMetric.MetricName)
		}
		if _, exists := configuredNames[configuredMetric.MetricName]; exists {
			return fmt.Errorf("duplicate metric %q", configuredMetric.MetricName)
		}
		configuredNames[configuredMetric.MetricName] = struct{}{}
	}
	for metricName := range i.config.MetricPolicies {
		if _, exists := configuredNames[metricName]; !exists {
			return fmt.Errorf("release policy references unknown metric %q", metricName)
		}
	}
	return nil
}

func (i *inputs) validateCriticalCases() error {
	validationCases := make(map[string]struct{}, len(i.validation.EvalCases))
	for _, evaluationCase := range i.validation.EvalCases {
		if evaluationCase != nil {
			validationCases[evaluationCase.EvalID] = struct{}{}
		}
	}
	for _, caseID := range i.config.CriticalCaseIDs {
		if _, exists := validationCases[caseID]; !exists {
			return fmt.Errorf("critical case %q is absent from validation eval set", caseID)
		}
	}
	return nil
}

func fingerprint(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write(files[name])
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func scenarioFingerprint(base, scenario string) string {
	digest := sha256.Sum256([]byte(base + "\x00" + scenario))
	return hex.EncodeToString(digest[:])
}
