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
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// LoadEvalSet reads and validates an evaluation-set JSON file.
func LoadEvalSet(path string) (*EvalSet, error) {
	var set EvalSet
	if err := readJSON(path, &set, true); err != nil {
		return nil, fmt.Errorf("load eval set: %w", err)
	}
	if err := validateEvalSet(&set); err != nil {
		return nil, fmt.Errorf("validate eval set %q: %w", path, err)
	}
	return &set, nil
}

// LoadMetrics reads and validates a metrics JSON file. The top-level array is
// compatible with the Evaluation Service's local metric files.
func LoadMetrics(path string) ([]MetricConfig, error) {
	var metrics []MetricConfig
	if err := readJSON(path, &metrics, true); err != nil {
		return nil, fmt.Errorf("load metrics: %w", err)
	}
	if err := validateMetrics(metrics); err != nil {
		return nil, err
	}
	return metrics, nil
}

func validateMetrics(metrics []MetricConfig) error {
	if len(metrics) == 0 {
		return errors.New("metrics are empty")
	}
	seen := make(map[string]struct{}, len(metrics))
	totalWeight := 0.0
	for i, metric := range metrics {
		if metric.MetricName == "" {
			return fmt.Errorf("metric %d name is empty", i)
		}
		if _, ok := seen[metric.MetricName]; ok {
			return fmt.Errorf("duplicate metric %q", metric.MetricName)
		}
		seen[metric.MetricName] = struct{}{}
		if !supportedMetric(metric.MetricName) {
			return fmt.Errorf("unsupported metric %q", metric.MetricName)
		}
		if !finiteScore(metric.Threshold) || metric.Threshold < 0 || metric.Threshold > 1 {
			return fmt.Errorf("metric %q threshold must be in [0,1]", metric.MetricName)
		}
		if !finiteScore(metric.Weight) || metric.Weight <= 0 {
			return fmt.Errorf("metric %q weight must be greater than 0", metric.MetricName)
		}
		totalWeight += metric.Weight
	}
	if !finiteScore(totalWeight) || totalWeight <= 0 {
		return errors.New("total metric weight must be finite and greater than 0")
	}
	return nil
}

// LoadConfig reads and validates promptiter.json.
func LoadConfig(path string) (*Config, error) {
	var cfg Config
	if err := readJSON(path, &cfg, true); err != nil {
		return nil, fmt.Errorf("load promptiter config: %w", err)
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validate promptiter config: %w", err)
	}
	return &cfg, nil
}

// LoadPrompt reads a baseline prompt source file.
func LoadPrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt: %w", err)
	}
	if !utf8.Valid(data) {
		return "", errors.New("prompt is not valid UTF-8")
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", errors.New("prompt is empty")
	}
	return prompt, nil
}

func readJSON(path string, target any, strict bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !utf8.Valid(data) {
		return errors.New("JSON input is not valid UTF-8")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder, "$"); err != nil {
		return fmt.Errorf("validate JSON keys: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate key %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object at %s is not closed", path)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := scanJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array at %s is not closed", path)
		}
	default:
		return fmt.Errorf("unexpected delimiter %q at %s", delimiter, path)
	}
	return nil
}

func validateEvalSet(set *EvalSet) error {
	if set == nil {
		return errors.New("eval set is nil")
	}
	if strings.TrimSpace(set.EvalSetID) == "" {
		return errors.New("evalSetId is empty")
	}
	if len(set.EvalCases) == 0 {
		return errors.New("evalCases are empty")
	}
	if set.PassThreshold == nil {
		defaultThreshold := 0.8
		set.PassThreshold = &defaultThreshold
	}
	if !finiteScore(*set.PassThreshold) || *set.PassThreshold < 0 || *set.PassThreshold > 1 {
		return errors.New("passThreshold must be in [0,1]")
	}
	seen := make(map[string]struct{}, len(set.EvalCases))
	for i := range set.EvalCases {
		evalCase := &set.EvalCases[i]
		if strings.TrimSpace(evalCase.EvalID) == "" {
			return fmt.Errorf("case %d evalId is empty", i)
		}
		if _, ok := seen[evalCase.EvalID]; ok {
			return fmt.Errorf("duplicate evalId %q", evalCase.EvalID)
		}
		seen[evalCase.EvalID] = struct{}{}
		if len(evalCase.Conversation) != 1 {
			return fmt.Errorf("case %q must contain exactly one conversation invocation", evalCase.EvalID)
		}
		expected := expectedInvocation(evalCase)
		if expected == nil {
			return fmt.Errorf("case %q has no expected invocation", evalCase.EvalID)
		}
		for toolIndex, expectedTool := range expected.Tools {
			if expectedTool == nil || strings.TrimSpace(expectedTool.Name) == "" {
				return fmt.Errorf("case %q expected tool %d is nil or unnamed", evalCase.EvalID, toolIndex)
			}
		}
		if len(evalCase.FakeResponses) == 0 {
			return fmt.Errorf("case %q fakeResponses are empty", evalCase.EvalID)
		}
		if evalCase.Expectations.MinRetrievedDocuments < 0 {
			return fmt.Errorf("case %q minRetrievedDocuments cannot be negative", evalCase.EvalID)
		}
		switch strings.ToLower(evalCase.Expectations.ResponseFormat) {
		case "", "json", "xml", "yaml", "yml":
		default:
			return fmt.Errorf("case %q responseFormat %q is unsupported", evalCase.EvalID, evalCase.Expectations.ResponseFormat)
		}
		facts := make(map[string]struct{}, len(evalCase.Expectations.RequiredFacts))
		for _, fact := range evalCase.Expectations.RequiredFacts {
			normalized := normalizeText(fact)
			if normalized == "" {
				return fmt.Errorf("case %q required fact is empty", evalCase.EvalID)
			}
			if _, ok := facts[normalized]; ok {
				return fmt.Errorf("case %q has duplicate required fact %q", evalCase.EvalID, fact)
			}
			facts[normalized] = struct{}{}
		}
		for variantID, output := range evalCase.FakeResponses {
			if !validCandidateID(variantID) {
				return fmt.Errorf("case %q fake response variant id %q is invalid", evalCase.EvalID, variantID)
			}
			if output.RetrievedDocuments < 0 {
				return fmt.Errorf("case %q variant %q retrievedDocuments cannot be negative", evalCase.EvalID, variantID)
			}
			if output.PromptSemanticSHA256 != "" && !validSHA256(output.PromptSemanticSHA256) {
				return fmt.Errorf(
					"case %q variant %q promptSemanticSha256 must be 64 lowercase hexadecimal characters",
					evalCase.EvalID,
					variantID,
				)
			}
			if output.RubricScore != nil && (!finiteScore(*output.RubricScore) || *output.RubricScore < 0 || *output.RubricScore > 1) {
				return fmt.Errorf("case %q variant %q rubricScore must be in [0,1]", evalCase.EvalID, variantID)
			}
			if err := validateUsage(output.Usage); err != nil {
				return fmt.Errorf("case %q variant %q usage: %w", evalCase.EvalID, variantID, err)
			}
			for toolIndex, actualTool := range output.Tools {
				if actualTool == nil || strings.TrimSpace(actualTool.Name) == "" {
					return fmt.Errorf("case %q variant %q tool %d is nil or unnamed", evalCase.EvalID, variantID, toolIndex)
				}
			}
			traceIDs := make(map[string]struct{}, len(output.Trace))
			for traceIndex, step := range output.Trace {
				if strings.TrimSpace(step.StepID) == "" {
					return fmt.Errorf("case %q variant %q trace step %d id is empty", evalCase.EvalID, variantID, traceIndex)
				}
				if _, ok := traceIDs[step.StepID]; ok {
					return fmt.Errorf("case %q variant %q has duplicate trace step id %q", evalCase.EvalID, variantID, step.StepID)
				}
				traceIDs[step.StepID] = struct{}{}
			}
			if len(output.Trace) > 0 {
				if _, err := normalizeTraceReplayOutput(output, false); err != nil {
					return fmt.Errorf("case %q variant %q trace: %w", evalCase.EvalID, variantID, err)
				}
			}
		}
	}
	return nil
}

func validateConfig(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	if strings.TrimSpace(cfg.SchemaVersion) == "" {
		return errors.New("schemaVersion is empty")
	}
	if cfg.Mode != "fake" && cfg.Mode != "trace" {
		return fmt.Errorf("mode %q is unsupported; use fake or trace", cfg.Mode)
	}
	if cfg.MaxRounds <= 0 {
		return errors.New("maxRounds must be greater than 0")
	}
	if cfg.MaxRounds > len(cfg.Candidates) {
		return errors.New("maxRounds exceeds configured candidate count")
	}
	if strings.TrimSpace(cfg.Surface.StructureID) == "" || strings.TrimSpace(cfg.Surface.NodeID) == "" || strings.TrimSpace(cfg.Surface.Type) == "" {
		return errors.New("surface structureId, nodeId, and type are required")
	}
	if cfg.Surface.Type != "instruction" && cfg.Surface.Type != "global_instruction" {
		return fmt.Errorf("surface type %q is not a supported text surface", cfg.Surface.Type)
	}
	seen := make(map[string]struct{}, len(cfg.Candidates))
	for i, candidate := range cfg.Candidates {
		if candidate.ID == "" {
			return fmt.Errorf("candidate %d id is empty", i)
		}
		if !validCandidateID(candidate.ID) {
			return fmt.Errorf("candidate id %q may contain only letters, digits, dot, underscore, and hyphen", candidate.ID)
		}
		if candidate.ID == "baseline" {
			return errors.New("candidate id \"baseline\" is reserved")
		}
		if _, ok := seen[candidate.ID]; ok {
			return fmt.Errorf("duplicate candidate id %q", candidate.ID)
		}
		seen[candidate.ID] = struct{}{}
		if strings.TrimSpace(candidate.AppendPrompt) == "" {
			return fmt.Errorf("candidate %q appendPrompt is empty", candidate.ID)
		}
		if strings.Contains(candidate.AppendPrompt, promptVariantMarkerPrefix) {
			return fmt.Errorf("candidate %q appendPrompt contains a reserved variant marker", candidate.ID)
		}
		if strings.TrimSpace(candidate.Reason) == "" {
			return fmt.Errorf("candidate %q reason is empty", candidate.ID)
		}
		categorySet := make(map[FailureCategory]struct{}, len(candidate.AddressCategories))
		for _, category := range candidate.AddressCategories {
			if !knownFailureCategory(category) {
				return fmt.Errorf("candidate %q has unknown failure category %q", candidate.ID, category)
			}
			if _, ok := categorySet[category]; ok {
				return fmt.Errorf("candidate %q has duplicate failure category %q", candidate.ID, category)
			}
			categorySet[category] = struct{}{}
		}
	}
	if strings.TrimSpace(cfg.FakeEngine.Name) == "" || strings.TrimSpace(cfg.FakeEngine.Version) == "" {
		return errors.New("fakeEngine name and version are required")
	}
	if cfg.FakeEngine.FallbackVariant == "" {
		cfg.FakeEngine.FallbackVariant = "baseline"
	}
	if !validCandidateID(cfg.FakeEngine.FallbackVariant) {
		return errors.New("fakeEngine fallbackVariant is invalid")
	}
	if cfg.FakeEngine.FallbackVariant != "baseline" {
		return errors.New("fakeEngine fallbackVariant must be \"baseline\"")
	}
	if cfg.Gate.MinValidationScoreGain < 0 {
		return errors.New("minValidationScoreGain cannot be negative")
	}
	criticalIDs := make(map[string]struct{}, len(cfg.Gate.CriticalCaseIDs))
	for _, id := range cfg.Gate.CriticalCaseIDs {
		if strings.TrimSpace(id) == "" {
			return errors.New("criticalCaseIds cannot contain an empty id")
		}
		if _, ok := criticalIDs[id]; ok {
			return fmt.Errorf("duplicate critical case id %q", id)
		}
		criticalIDs[id] = struct{}{}
	}
	return validateGatePolicy(cfg.Gate)
}

func validCandidateID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, current := range value {
		if (current < '0' || current > '9') && (current < 'a' || current > 'f') {
			return false
		}
	}
	return true
}

func knownFailureCategory(category FailureCategory) bool {
	switch category {
	case FailureFinalResponseMismatch, FailureToolCallError, FailureToolParameterError,
		FailureRouteError, FailureFormatError, FailureKnowledgeRetrievalInsufficient:
		return true
	default:
		return false
	}
}

func expectedInvocation(evalCase *EvalCase) *evalset.Invocation {
	if evalCase == nil {
		return nil
	}
	for i := len(evalCase.Conversation) - 1; i >= 0; i-- {
		if evalCase.Conversation[i] != nil {
			return evalCase.Conversation[i]
		}
	}
	return nil
}
