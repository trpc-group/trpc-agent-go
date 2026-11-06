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
	"reflect"
	"strings"
	"time"
)

const (
	pipelineStatusSucceeded = "succeeded"
	pipelineStatusFailed    = "failed"
)

type optimizationReport struct {
	SchemaVersion            string           `json:"schemaVersion"`
	Status                   string           `json:"status"`
	Error                    string           `json:"error,omitempty"`
	Mode                     string           `json:"mode"`
	CandidateSource          string           `json:"candidateSource"`
	Seed                     int64            `json:"seed"`
	DurationMillis           int64            `json:"durationMillis"`
	DeterministicFingerprint string           `json:"deterministicFingerprint"`
	EvaluationModel          modelAudit       `json:"evaluationModel"`
	OptimizerModel           modelAudit       `json:"optimizerModel"`
	Resources                resourceAudit    `json:"resources"`
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

type stageResourceAudit struct {
	Usage         Usage  `json:"usage"`
	LatencyMillis int64  `json:"latencyMillis"`
	Error         string `json:"error,omitempty"`
}

type resourceAudit struct {
	BaselineEvaluation  stageResourceAudit `json:"baselineEvaluation"`
	Optimizer           stageResourceAudit `json:"optimizer"`
	CandidateEvaluation stageResourceAudit `json:"candidateEvaluation"`
	Total               stageResourceAudit `json:"total"`
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

	var generator textGenerator = fakeGenerator{}
	var budget *liveBudget
	evaluationModel := modelAudit{
		Provider: "deterministic",
		Name:     "fake-trace-runner",
	}
	optimizerModel := modelAudit{
		Provider: "deterministic",
		Name:     "fake-promptiter-optimizer",
	}
	evaluationAPIKey := ""
	optimizerAPIKey := ""
	if mode == modeLive {
		evaluationAPIKey, optimizerAPIKey, err = loadLiveAPIKeys(cfg.Live)
		if err != nil {
			return err
		}
		budget = newLiveBudget(cfg.Gate, cfg.Live.Optimizer.Budget)
		live, liveErr := newLiveGeneratorWithBudget(
			cfg.Live,
			budget,
			evaluationAPIKey,
		)
		if liveErr != nil {
			return fmt.Errorf("create live generator: %w", liveErr)
		}
		generator = live
		evaluationModel = modelAudit{
			Provider: "deepseek",
			Name:     cfg.Live.Model,
			BaseURL:  cfg.Live.BaseURL,
		}
		optimizerModel = modelAudit{
			Provider: "deepseek",
			Name:     cfg.Live.Optimizer.Model,
			BaseURL:  cfg.Live.Optimizer.BaseURL,
		}
	}

	baselineStarted := time.Now()
	baselineUsageBefore := budgetUsage(budget)
	trainBaseline, err := evaluatePrompt(ctx, cfg.Train, cfg.Prompt, 1, generator)
	if err != nil {
		return fmt.Errorf("evaluate train baseline: %w", err)
	}
	validationBaseline, err := evaluatePrompt(
		ctx,
		cfg.Validation,
		cfg.Prompt,
		cfg.Gate.PassK,
		generator,
	)
	if err != nil {
		return fmt.Errorf("evaluate validation baseline: %w", err)
	}
	baselineUsage := budgetUsage(budget).subtract(baselineUsageBefore).reportUsage()
	if mode == modeFake {
		baselineUsage = evaluationsUsage(trainBaseline).Add(
			evaluationsUsage(validationBaseline),
		)
	}
	baselineResources := stageResourceAudit{
		Usage:         baselineUsage,
		LatencyMillis: time.Since(baselineStarted).Milliseconds(),
	}

	var candidate string
	var promptIterAudit promptIterAudit
	var promptIterErr error
	if mode == modeFake {
		candidate, promptIterAudit, promptIterErr = runDeterministicPromptIter(ctx, cfg)
	} else {
		reservation := candidateEvaluationReservation(cfg)
		budget.setEvaluationReserve(reservation)
		runtime, runtimeErr := newLivePromptOptimizer(
			ctx,
			cfg,
			budget,
			optimizerAPIKey,
		)
		if runtimeErr != nil {
			promptIterErr = runtimeErr
			promptIterAudit = failedPromptIterAudit(
				cfg,
				candidateSourceLiveLLM,
				runtimeErr,
			)
		} else {
			optimizerModel = runtime.model
			defer runtime.close()
			optimizerUsageBefore := budget.snapshot(budgetStageOptimizer)
			candidate, promptIterAudit, promptIterErr = runPromptIter(
				ctx,
				cfg,
				runtime.optimizer,
				candidateSourceLiveLLM,
				func() Usage {
					return budget.snapshot(budgetStageOptimizer).
						subtract(optimizerUsageBefore).
						reportUsage()
				},
			)
		}
		budget.clearEvaluationReserve()
	}

	if promptIterErr != nil {
		report := failedOptimizationReport(
			cfg,
			mode,
			started,
			evaluationModel,
			optimizerModel,
			baselineResources,
			promptIterAudit,
			trainBaseline,
			validationBaseline,
			promptIterErr,
			budgetUsage(budget).reportUsage(),
		)
		if writeErr := finalizeAndWriteReport(
			cfg,
			report,
			evaluationAPIKey,
			optimizerAPIKey,
		); writeErr != nil {
			safePromptIterErr := sanitizeReportError(
				promptIterErr,
				evaluationAPIKey,
				optimizerAPIKey,
			)
			return errors.Join(
				fmt.Errorf("run PromptIter: %w", safePromptIterErr),
				writeErr,
			)
		}
		return fmt.Errorf(
			"run PromptIter: %w",
			sanitizeReportError(
				promptIterErr,
				evaluationAPIKey,
				optimizerAPIKey,
			),
		)
	}

	candidateStarted := time.Now()
	candidateUsageBefore := budgetUsage(budget)
	trainCandidate, err := evaluatePrompt(ctx, cfg.Train, candidate, 1, generator)
	if err != nil {
		return fmt.Errorf("evaluate train candidate: %w", err)
	}
	validationCandidate, err := evaluatePrompt(
		ctx,
		cfg.Validation,
		candidate,
		cfg.Gate.PassK,
		generator,
	)
	if err != nil {
		return fmt.Errorf("evaluate validation candidate: %w", err)
	}
	candidateUsage := budgetUsage(budget).subtract(candidateUsageBefore).reportUsage()
	if mode == modeFake {
		candidateUsage = evaluationsUsage(trainCandidate).Add(
			evaluationsUsage(validationCandidate),
		)
	}
	candidateResources := stageResourceAudit{
		Usage:         candidateUsage,
		LatencyMillis: time.Since(candidateStarted).Milliseconds(),
	}

	comparison, err := CompareCases(
		validationBaseline,
		validationCandidate,
		cfg.Gate.PassK,
	)
	if err != nil {
		return fmt.Errorf("compare validation cases: %w", err)
	}
	totalUsage := baselineUsage.Add(promptIterAudit.Usage).Add(candidateUsage)
	if mode == modeLive {
		totalUsage = budgetUsage(budget).reportUsage()
	}
	comparison.Usage = totalUsage
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
		SchemaVersion:   "1.1",
		Status:          pipelineStatusSucceeded,
		Mode:            mode,
		CandidateSource: promptIterAudit.Source,
		Seed:            cfg.Seed,
		DurationMillis:  time.Since(started).Milliseconds(),
		EvaluationModel: evaluationModel,
		OptimizerModel:  optimizerModel,
		Resources: resourceAudit{
			BaselineEvaluation: baselineResources,
			Optimizer: stageResourceAudit{
				Usage:         promptIterAudit.Usage,
				LatencyMillis: promptIterAudit.LatencyMillis,
				Error:         promptIterAudit.Error,
			},
			CandidateEvaluation: candidateResources,
			Total: stageResourceAudit{
				Usage:         totalUsage,
				LatencyMillis: time.Since(started).Milliseconds(),
			},
		},
		PromptIter: promptIterAudit,
		Train: evaluationPair{
			Baseline:  trainBaseline,
			Candidate: trainCandidate,
		},
		Validation: evaluationPair{
			Baseline:  validationBaseline,
			Candidate: validationCandidate,
		},
		Comparison: comparison,
		Gate:       gate,
		AttributionSummary: attributionAudit{
			TrainBaseline:       summarizeAttributions(trainBaseline),
			TrainCandidate:      summarizeAttributions(trainCandidate),
			ValidationBaseline:  summarizeAttributions(validationBaseline),
			ValidationCandidate: summarizeAttributions(validationCandidate),
		},
		SelectedPrompt: selectedPrompt,
	}
	return finalizeAndWriteReport(
		cfg,
		report,
		evaluationAPIKey,
		optimizerAPIKey,
	)
}

func failedPromptIterAudit(
	cfg *loadedConfig,
	source string,
	err error,
) promptIterAudit {
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	return promptIterAudit{
		SurfaceID:      cfg.PromptIter.Target,
		Source:         source,
		Completed:      false,
		Error:          errorMessage,
		BaselinePrompt: strings.TrimSpace(cfg.Prompt),
	}
}

func failedOptimizationReport(
	cfg *loadedConfig,
	mode string,
	started time.Time,
	evaluationModel modelAudit,
	optimizerModel modelAudit,
	baselineResources stageResourceAudit,
	promptIter promptIterAudit,
	trainBaseline []CaseEvaluation,
	validationBaseline []CaseEvaluation,
	runErr error,
	totalUsage Usage,
) *optimizationReport {
	errorMessage := runErr.Error()
	gate := rejectedOptimizerGate(totalUsage, cfg.Gate, errorMessage)
	return &optimizationReport{
		SchemaVersion:   "1.1",
		Status:          pipelineStatusFailed,
		Error:           errorMessage,
		Mode:            mode,
		CandidateSource: promptIter.Source,
		Seed:            cfg.Seed,
		DurationMillis:  time.Since(started).Milliseconds(),
		EvaluationModel: evaluationModel,
		OptimizerModel:  optimizerModel,
		Resources: resourceAudit{
			BaselineEvaluation: baselineResources,
			Optimizer: stageResourceAudit{
				Usage:         promptIter.Usage,
				LatencyMillis: promptIter.LatencyMillis,
				Error:         promptIter.Error,
			},
			Total: stageResourceAudit{
				Usage:         totalUsage,
				LatencyMillis: time.Since(started).Milliseconds(),
				Error:         errorMessage,
			},
		},
		PromptIter: promptIter,
		Train: evaluationPair{
			Baseline: trainBaseline,
		},
		Validation: evaluationPair{
			Baseline: validationBaseline,
		},
		Comparison: Comparison{
			PassK: cfg.Gate.PassK,
			Usage: totalUsage,
		},
		Gate: gate,
		AttributionSummary: attributionAudit{
			TrainBaseline:      summarizeAttributions(trainBaseline),
			ValidationBaseline: summarizeAttributions(validationBaseline),
		},
		SelectedPrompt: strings.TrimSpace(cfg.Prompt),
	}
}

func rejectedOptimizerGate(
	usage Usage,
	cfg gateFileConfig,
	detail string,
) GateResult {
	checks := []GateCheck{
		newGateCheck(
			"optimizer_completed",
			false,
			0,
			1,
			"==",
			detail,
		),
		budgetCheck("calls_budget", usage.Calls, cfg.MaxCalls),
		budgetCheck("tokens_budget", usage.Tokens(), cfg.MaxTokens),
		costBudgetCheck(usage.CostCNY, cfg.MaxCostCNY),
	}
	result := GateResult{
		Accepted:     false,
		Checks:       checks,
		FailedChecks: []string{"optimizer_completed"},
	}
	for _, check := range checks[1:] {
		if !check.Passed {
			result.FailedChecks = append(result.FailedChecks, check.Name)
		}
	}
	return result
}

func candidateEvaluationReservation(cfg *loadedConfig) resourceReservation {
	if cfg == nil {
		return resourceReservation{}
	}
	maxCandidateBytes := len([]byte(cfg.Prompt)) +
		cfg.Live.Optimizer.MaxOutputTokens*4
	attempts := cfg.Live.MaxRetries + 1
	var reservation resourceReservation
	addSet := func(set evalSetFile, runs int) {
		for _, evalCase := range set.EvalCases {
			input := ""
			if len(evalCase.Conversation) > 0 {
				input = evalCase.Conversation[0].UserContent.Content
			}
			estimate := estimateTextRequest(
				strings.Repeat("x", maxCandidateBytes),
				input,
				512,
				cfg.Live.InputCNYPerMillion,
				cfg.Live.OutputCNYPerMillion,
			)
			reservation.Calls += runs * attempts
			reservation.Tokens += estimate.tokens() * runs * attempts
			reservation.CostCNY += estimate.CostCNY * float64(runs*attempts)
		}
	}
	addSet(cfg.Train, 1)
	addSet(cfg.Validation, cfg.Gate.PassK)
	return reservation
}

func budgetUsage(budget *liveBudget) generationUsage {
	if budget == nil {
		return generationUsage{}
	}
	return budget.snapshot("")
}

func loadLiveAPIKeys(cfg liveConfig) (string, string, error) {
	evaluationAPIKey := strings.TrimSpace(os.Getenv(cfg.APIKeyEnv))
	optimizerAPIKey := strings.TrimSpace(os.Getenv(cfg.Optimizer.APIKeyEnv))
	switch {
	case evaluationAPIKey == "":
		return "", "", fmt.Errorf("%s is empty", cfg.APIKeyEnv)
	case optimizerAPIKey == "":
		return "", "", fmt.Errorf("%s is empty", cfg.Optimizer.APIKeyEnv)
	default:
		return evaluationAPIKey, optimizerAPIKey, nil
	}
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

func finalizeAndWriteReport(
	cfg *loadedConfig,
	report *optimizationReport,
	secrets ...string,
) error {
	if cfg == nil {
		return errors.New("loaded config is nil")
	}
	if report == nil {
		return errors.New("optimization report is nil")
	}
	sanitizeReportErrors(report, secrets...)
	fingerprint, err := reportFingerprint(report)
	if err != nil {
		return err
	}
	report.DeterministicFingerprint = fingerprint
	outputDir := resolvePath(cfg.BaseDir, cfg.OutputDir)
	return writeReports(outputDir, report)
}

func sanitizeReportErrors(report *optimizationReport, secrets ...string) {
	if report == nil {
		return
	}
	sanitizeReportValue(reflect.ValueOf(report), secrets...)
}

// sanitizeReportValue visits every free-text field so secrets echoed by a
// provider or generated by the optimizer cannot survive in nested audit data.
// Keeping the traversal schema-independent also protects fields added to the
// report later.
func sanitizeReportValue(value reflect.Value, secrets ...string) {
	if !value.IsValid() {
		return
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			sanitizeReportValue(value.Elem(), secrets...)
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			sanitizeReportValue(value.Field(i), secrets...)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			sanitizeReportValue(value.Index(i), secrets...)
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			mapValue := iterator.Value()
			writable := reflect.New(mapValue.Type()).Elem()
			writable.Set(mapValue)
			sanitizeReportValue(writable, secrets...)
			value.SetMapIndex(iterator.Key(), writable)
		}
	case reflect.String:
		if value.CanSet() {
			value.SetString(sanitizeReportText(value.String(), secrets...))
		}
	}
}

func sanitizeReportError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	return errors.New(sanitizeReportText(err.Error(), secrets...))
}

func sanitizeReportText(text string, secrets ...string) string {
	sanitized := redactSensitiveDisclosures(text)
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		sanitized = strings.ReplaceAll(sanitized, secret, sensitiveRedaction)
	}
	return sanitized
}

func reportFingerprint(report *optimizationReport) (string, error) {
	if report == nil {
		return "", errors.New("report is nil")
	}
	reportData, err := json.Marshal(report)
	if err != nil {
		return "", fmt.Errorf("marshal report for fingerprint: %w", err)
	}
	var stable optimizationReport
	if err := json.Unmarshal(reportData, &stable); err != nil {
		return "", fmt.Errorf("copy report for fingerprint: %w", err)
	}
	stable.DurationMillis = 0
	stable.DeterministicFingerprint = ""
	stable.Resources.BaselineEvaluation.LatencyMillis = 0
	stable.Resources.Optimizer.LatencyMillis = 0
	stable.Resources.CandidateEvaluation.LatencyMillis = 0
	stable.Resources.Total.LatencyMillis = 0
	stable.PromptIter.LatencyMillis = 0
	normalizeEvaluationLatencies(stable.Train.Baseline)
	normalizeEvaluationLatencies(stable.Train.Candidate)
	normalizeEvaluationLatencies(stable.Validation.Baseline)
	normalizeEvaluationLatencies(stable.Validation.Candidate)
	data, err := json.Marshal(stable)
	if err != nil {
		return "", fmt.Errorf("marshal report fingerprint: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func normalizeEvaluationLatencies(evaluations []CaseEvaluation) {
	for caseIndex := range evaluations {
		for runIndex := range evaluations[caseIndex].Runs {
			evaluations[caseIndex].Runs[runIndex].LatencyMillis = 0
		}
	}
}
