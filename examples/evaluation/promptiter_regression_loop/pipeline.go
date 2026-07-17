//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type optimizationReport struct {
	SchemaVersion            string           `json:"schemaVersion"`
	Mode                     string           `json:"mode"`
	Seed                     int64            `json:"seed"`
	DurationMillis           int64            `json:"durationMillis"`
	DeterministicFingerprint string           `json:"deterministicFingerprint"`
	Model                    modelAudit       `json:"model"`
	PromptIter               promptIterAudit  `json:"promptIter"`
	Train                    evaluationPair   `json:"train"`
	Validation               evaluationPair   `json:"validation"`
	Comparison               Comparison       `json:"comparison"`
	Gate                     GateResult       `json:"gate"`
	AttributionSummary       attributionAudit `json:"attributionSummary"`
	SelectedPrompt           string           `json:"selectedPrompt"`
}

type modelAudit struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
	BaseURL  string `json:"baseURL,omitempty"`
}

type evaluationPair struct {
	Baseline  []CaseEvaluation `json:"baseline"`
	Candidate []CaseEvaluation `json:"candidate"`
}

type attributionAudit struct {
	TrainBaseline       map[FailureCategory]int `json:"trainBaseline"`
	TrainCandidate      map[FailureCategory]int `json:"trainCandidate"`
	ValidationBaseline  map[FailureCategory]int `json:"validationBaseline"`
	ValidationCandidate map[FailureCategory]int `json:"validationCandidate"`
}

func runPipeline(ctx context.Context, configPath, mode string) error {
	started := time.Now()
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != modeFake && mode != modeLive {
		return fmt.Errorf("unsupported mode %q: use fake or live", mode)
	}
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	candidate, promptIterAudit, err := runDeterministicPromptIter(ctx, cfg)
	if err != nil {
		return err
	}

	var generator textGenerator = fakeGenerator{}
	modelInfo := modelAudit{Provider: "deterministic", Name: "fake-trace-runner"}
	if mode == modeLive {
		apiKey := strings.TrimSpace(os.Getenv(cfg.Live.APIKeyEnv))
		live, liveErr := newLiveGenerator(cfg.Live, cfg.Gate, apiKey)
		if liveErr != nil {
			return fmt.Errorf("create live generator: %w", liveErr)
		}
		generator = live
		modelInfo = modelAudit{Provider: "deepseek", Name: cfg.Live.Model, BaseURL: cfg.Live.BaseURL}
	}

	trainBaseline, err := evaluatePrompt(ctx, cfg.Train, cfg.Prompt, 1, generator)
	if err != nil {
		return fmt.Errorf("evaluate train baseline: %w", err)
	}
	trainCandidate, err := evaluatePrompt(ctx, cfg.Train, candidate, 1, generator)
	if err != nil {
		return fmt.Errorf("evaluate train candidate: %w", err)
	}
	validationBaseline, err := evaluatePrompt(ctx, cfg.Validation, cfg.Prompt, cfg.Gate.PassK, generator)
	if err != nil {
		return fmt.Errorf("evaluate validation baseline: %w", err)
	}
	validationCandidate, err := evaluatePrompt(ctx, cfg.Validation, candidate, cfg.Gate.PassK, generator)
	if err != nil {
		return fmt.Errorf("evaluate validation candidate: %w", err)
	}
	comparison, err := CompareCases(validationBaseline, validationCandidate, cfg.Gate.PassK)
	if err != nil {
		return fmt.Errorf("compare validation cases: %w", err)
	}
	comparison.Usage = comparison.Usage.Add(evaluationsUsage(trainBaseline)).Add(evaluationsUsage(trainCandidate))
	gate, err := EvaluateGate(comparison, GateConfig{
		MinScoreGain:       cfg.Gate.MinScoreGain,
		PassK:              cfg.Gate.PassK,
		BootstrapSeed:      cfg.Gate.BootstrapSeed,
		BootstrapResamples: cfg.Gate.BootstrapRounds,
		MaxCalls:           cfg.Gate.MaxCalls,
		MaxTokens:          cfg.Gate.MaxTokens,
		MaxCostCNY:         cfg.Gate.MaxCostCNY,
	})
	if err != nil {
		return fmt.Errorf("evaluate gate: %w", err)
	}
	selectedPrompt := cfg.Prompt
	if gate.Accepted {
		selectedPrompt = candidate
	}
	report := &optimizationReport{
		SchemaVersion:  "1.0",
		Mode:           mode,
		Seed:           cfg.Seed,
		DurationMillis: time.Since(started).Milliseconds(),
		Model:          modelInfo,
		PromptIter:     promptIterAudit,
		Train:          evaluationPair{Baseline: trainBaseline, Candidate: trainCandidate},
		Validation:     evaluationPair{Baseline: validationBaseline, Candidate: validationCandidate},
		Comparison:     comparison,
		Gate:           gate,
		AttributionSummary: attributionAudit{
			TrainBaseline:       summarizeAttributions(trainBaseline),
			TrainCandidate:      summarizeAttributions(trainCandidate),
			ValidationBaseline:  summarizeAttributions(validationBaseline),
			ValidationCandidate: summarizeAttributions(validationCandidate),
		},
		SelectedPrompt: selectedPrompt,
	}
	fingerprint, err := reportFingerprint(report)
	if err != nil {
		return err
	}
	report.DeterministicFingerprint = fingerprint
	outputDir := resolvePath(cfg.BaseDir, cfg.OutputDir)
	if err := writeReports(outputDir, report); err != nil {
		return err
	}
	return nil
}

func evaluationsUsage(evaluations []CaseEvaluation) Usage {
	var usage Usage
	for _, evaluation := range evaluations {
		for _, run := range evaluation.Runs {
			usage = usage.Add(run.Usage)
		}
	}
	return usage
}

func summarizeAttributions(group []CaseEvaluation) map[FailureCategory]int {
	summary := make(map[FailureCategory]int)
	for _, evaluation := range group {
		for _, run := range evaluation.Runs {
			if run.Passed {
				continue
			}
			summary[run.Attribution.Category]++
			break
		}
	}
	return summary
}

func reportFingerprint(report *optimizationReport) (string, error) {
	if report == nil {
		return "", errors.New("report is nil")
	}
	stable := *report
	stable.DurationMillis = 0
	stable.DeterministicFingerprint = ""
	data, err := json.Marshal(stable)
	if err != nil {
		return "", fmt.Errorf("marshal report fingerprint: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}
