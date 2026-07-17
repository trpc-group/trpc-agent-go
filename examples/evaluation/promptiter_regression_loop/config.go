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
)

const (
	modeFake = "fake"
	modeLive = "live"
)

type pipelineConfig struct {
	Seed              int64          `json:"seed"`
	PromptFile        string         `json:"promptFile"`
	TrainEvalSet      string         `json:"trainEvalSet"`
	ValidationEvalSet string         `json:"validationEvalSet"`
	MetricsFile       string         `json:"metricsFile"`
	PromptIterFile    string         `json:"promptIterFile"`
	OutputDir         string         `json:"outputDir"`
	Gate              gateFileConfig `json:"gate"`
	Live              liveConfig     `json:"live"`
}

type promptIterConfig struct {
	Target                  string  `json:"target"`
	MaxRounds               int     `json:"maxRounds"`
	MinScoreGain            float64 `json:"minScoreGain"`
	Optimizer               string  `json:"optimizer"`
	TrainOnlyOptimization   bool    `json:"trainOnlyOptimization"`
	CandidateValidationRuns int     `json:"candidateValidationRuns"`
}

type metricsConfig struct {
	Metrics []metricSpec `json:"metrics"`
}

type metricSpec struct {
	Name       string  `json:"name"`
	Threshold  float64 `json:"threshold,omitempty"`
	Kind       string  `json:"kind"`
	K          int     `json:"k,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type gateFileConfig struct {
	MinScoreGain    float64 `json:"minScoreGain"`
	PassK           int     `json:"passK"`
	BootstrapSeed   int64   `json:"bootstrapSeed"`
	BootstrapRounds int     `json:"bootstrapRounds"`
	MaxCalls        int     `json:"maxCalls"`
	MaxTokens       int     `json:"maxTokens"`
	MaxCostCNY      float64 `json:"maxCostCNY"`
}

type liveConfig struct {
	Model               string  `json:"model"`
	BaseURL             string  `json:"baseURL"`
	APIKeyEnv           string  `json:"apiKeyEnv"`
	TimeoutSeconds      int     `json:"timeoutSeconds"`
	MaxRetries          int     `json:"maxRetries"`
	InputCNYPerMillion  float64 `json:"inputCNYPerMillion"`
	OutputCNYPerMillion float64 `json:"outputCNYPerMillion"`
}

type evalSetFile struct {
	EvalSetID string     `json:"evalSetId"`
	Name      string     `json:"name"`
	EvalCases []caseSpec `json:"evalCases"`
}

type caseSpec struct {
	EvalID             string           `json:"evalId"`
	Critical           bool             `json:"critical,omitempty"`
	HardFailure        bool             `json:"hardFailure,omitempty"`
	Category           string           `json:"category"`
	RequiredDirective  string           `json:"requiredDirective"`
	ForbiddenDirective string           `json:"forbiddenDirective,omitempty"`
	ExpectedKeywords   []string         `json:"expectedKeywords"`
	Conversation       []invocationSpec `json:"conversation"`
}

type invocationSpec struct {
	InvocationID  string      `json:"invocationId"`
	UserContent   messageSpec `json:"userContent"`
	FinalResponse messageSpec `json:"finalResponse"`
}

type messageSpec struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type loadedConfig struct {
	pipelineConfig
	BaseDir    string
	Prompt     string
	PromptIter promptIterConfig
	Metrics    metricsConfig
	Train      evalSetFile
	Validation evalSetFile
}

func loadConfig(path string) (*loadedConfig, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg pipelineConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	baseDir := filepath.Dir(absPath)
	setDefaults(&cfg)
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	promptBytes, err := os.ReadFile(resolvePath(baseDir, cfg.PromptFile))
	if err != nil {
		return nil, fmt.Errorf("read baseline prompt: %w", err)
	}
	train, err := loadEvalSet(resolvePath(baseDir, cfg.TrainEvalSet))
	if err != nil {
		return nil, fmt.Errorf("load train eval set: %w", err)
	}
	validation, err := loadEvalSet(resolvePath(baseDir, cfg.ValidationEvalSet))
	if err != nil {
		return nil, fmt.Errorf("load validation eval set: %w", err)
	}
	var promptIter promptIterConfig
	if err := loadJSONFile(resolvePath(baseDir, cfg.PromptIterFile), &promptIter); err != nil {
		return nil, fmt.Errorf("load PromptIter config: %w", err)
	}
	var metrics metricsConfig
	if err := loadJSONFile(resolvePath(baseDir, cfg.MetricsFile), &metrics); err != nil {
		return nil, fmt.Errorf("load metrics config: %w", err)
	}
	setPromptIterDefaults(&promptIter)
	if err := validateLoadedInputs(cfg, promptIter, metrics, train, validation); err != nil {
		return nil, err
	}
	return &loadedConfig{
		pipelineConfig: cfg,
		BaseDir:        baseDir,
		Prompt:         strings.TrimSpace(string(promptBytes)),
		PromptIter:     promptIter,
		Metrics:        metrics,
		Train:          train,
		Validation:     validation,
	}, nil
}

func setDefaults(cfg *pipelineConfig) {
	if cfg.Seed == 0 {
		cfg.Seed = 20260717
	}
	if cfg.Gate.PassK == 0 {
		cfg.Gate.PassK = 3
	}
	if cfg.Gate.BootstrapRounds == 0 {
		cfg.Gate.BootstrapRounds = 5000
	}
	if cfg.Live.Model == "" {
		cfg.Live.Model = "deepseek-v4-flash"
	}
	if cfg.Live.BaseURL == "" {
		cfg.Live.BaseURL = "https://api.deepseek.com"
	}
	if cfg.Live.APIKeyEnv == "" {
		cfg.Live.APIKeyEnv = "DEEPSEEK_API_KEY"
	}
	if cfg.Live.TimeoutSeconds == 0 {
		cfg.Live.TimeoutSeconds = 45
	}
	if cfg.Live.MaxRetries == 0 {
		cfg.Live.MaxRetries = 2
	}
	if cfg.Live.InputCNYPerMillion == 0 {
		cfg.Live.InputCNYPerMillion = 1
	}
	if cfg.Live.OutputCNYPerMillion == 0 {
		cfg.Live.OutputCNYPerMillion = 2
	}
}

func setPromptIterDefaults(cfg *promptIterConfig) {
	if cfg.MaxRounds == 0 {
		cfg.MaxRounds = 1
	}
}

func validateConfig(cfg pipelineConfig) error {
	switch {
	case strings.TrimSpace(cfg.PromptFile) == "":
		return errors.New("promptFile is required")
	case strings.TrimSpace(cfg.TrainEvalSet) == "":
		return errors.New("trainEvalSet is required")
	case strings.TrimSpace(cfg.ValidationEvalSet) == "":
		return errors.New("validationEvalSet is required")
	case strings.TrimSpace(cfg.MetricsFile) == "":
		return errors.New("metricsFile is required")
	case strings.TrimSpace(cfg.PromptIterFile) == "":
		return errors.New("promptIterFile is required")
	case strings.TrimSpace(cfg.OutputDir) == "":
		return errors.New("outputDir is required")
	case cfg.Gate.PassK <= 0:
		return errors.New("gate.passK must be greater than zero")
	case cfg.Gate.MinScoreGain < 0:
		return errors.New("gate.minScoreGain must be non-negative")
	case cfg.Gate.MaxCostCNY < 0:
		return errors.New("gate.maxCostCNY must be non-negative")
	case cfg.Gate.MaxCalls < 0 || cfg.Gate.MaxTokens < 0:
		return errors.New("gate call and token budgets must be non-negative")
	case cfg.Live.TimeoutSeconds <= 0:
		return errors.New("live.timeoutSeconds must be greater than zero")
	case cfg.Live.MaxRetries < 0:
		return errors.New("live.maxRetries must be non-negative")
	case cfg.Live.InputCNYPerMillion <= 0 || cfg.Live.OutputCNYPerMillion <= 0:
		return errors.New("live token prices must be greater than zero")
	}
	return nil
}

func validateLoadedInputs(
	cfg pipelineConfig,
	promptIter promptIterConfig,
	metrics metricsConfig,
	train evalSetFile,
	validation evalSetFile,
) error {
	switch {
	case train.EvalSetID == validation.EvalSetID:
		return errors.New("train and validation eval set IDs must differ")
	case promptIter.MaxRounds <= 0:
		return errors.New("PromptIter maxRounds must be greater than zero")
	case promptIter.MinScoreGain < 0:
		return errors.New("PromptIter minScoreGain must be non-negative")
	case strings.TrimSpace(promptIter.Target) == "":
		return errors.New("PromptIter target is required")
	case strings.TrimSpace(promptIter.Optimizer) == "":
		return errors.New("PromptIter optimizer is required")
	case !promptIter.TrainOnlyOptimization:
		return errors.New("PromptIter must use train-only optimization")
	case promptIter.CandidateValidationRuns != cfg.Gate.PassK:
		return fmt.Errorf("PromptIter validation runs %d must equal gate PassK %d", promptIter.CandidateValidationRuns, cfg.Gate.PassK)
	}
	if err := validateMetrics(metrics, cfg.Gate.PassK); err != nil {
		return err
	}
	return validateDatasetIsolation(train, validation)
}

func validateMetrics(metrics metricsConfig, passK int) error {
	required := map[string]bool{
		"required_keywords": false,
		"hard_failure":      false,
		"pass_power_k":      false,
		"paired_bootstrap":  false,
	}
	for _, metric := range metrics.Metrics {
		if _, ok := required[metric.Name]; ok {
			required[metric.Name] = true
		}
		if metric.Name == "pass_power_k" && metric.K != passK {
			return fmt.Errorf("metric pass_power_k uses k=%d, want %d", metric.K, passK)
		}
	}
	for name, present := range required {
		if !present {
			return fmt.Errorf("required metric %q is missing", name)
		}
	}
	return nil
}

func validateDatasetIsolation(train, validation evalSetFile) error {
	trainIDs := make(map[string]struct{}, len(train.EvalCases))
	trainContent := make(map[string]string, len(train.EvalCases))
	for _, evalCase := range train.EvalCases {
		trainIDs[evalCase.EvalID] = struct{}{}
		trainContent[normalizedCaseContent(evalCase)] = evalCase.EvalID
	}
	for _, evalCase := range validation.EvalCases {
		if _, ok := trainIDs[evalCase.EvalID]; ok {
			return fmt.Errorf("train and validation share case ID %q", evalCase.EvalID)
		}
		if trainID, ok := trainContent[normalizedCaseContent(evalCase)]; ok {
			return fmt.Errorf("validation case %q duplicates train case %q content", evalCase.EvalID, trainID)
		}
	}
	return nil
}

func normalizedCaseContent(evalCase caseSpec) string {
	if len(evalCase.Conversation) == 0 {
		return ""
	}
	invocation := evalCase.Conversation[0]
	return strings.ToLower(strings.Join(strings.Fields(
		invocation.UserContent.Content+"\x00"+invocation.FinalResponse.Content,
	), " "))
}

func loadJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return err
	}
	return nil
}

func loadEvalSet(path string) (evalSetFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return evalSetFile{}, err
	}
	var set evalSetFile
	if err := json.Unmarshal(data, &set); err != nil {
		return evalSetFile{}, err
	}
	if strings.TrimSpace(set.EvalSetID) == "" || len(set.EvalCases) == 0 {
		return evalSetFile{}, errors.New("eval set must contain evalSetId and evalCases")
	}
	seen := make(map[string]struct{}, len(set.EvalCases))
	for _, c := range set.EvalCases {
		if strings.TrimSpace(c.EvalID) == "" || len(c.Conversation) != 1 {
			return evalSetFile{}, fmt.Errorf("eval case must have an ID and exactly one invocation")
		}
		if _, ok := seen[c.EvalID]; ok {
			return evalSetFile{}, fmt.Errorf("duplicate eval case ID %q", c.EvalID)
		}
		seen[c.EvalID] = struct{}{}
	}
	return set, nil
}

func resolvePath(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, filepath.FromSlash(path)))
}
