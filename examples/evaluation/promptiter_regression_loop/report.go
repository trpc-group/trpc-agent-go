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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

type optimizationReport struct {
	SchemaVersion string            `json:"schemaVersion"`
	Pipeline      pipelineMetadata  `json:"pipeline"`
	InputManifest inputManifest     `json:"inputManifest"`
	Runtime       runtimeMetadata   `json:"runtime"`
	Baseline      baselineReport    `json:"baseline"`
	Rounds        []roundReport     `json:"rounds"`
	FinalDecision gateDecision      `json:"finalDecision"`
	Accounting    accountingSummary `json:"accounting"`
	Artifacts     artifactManifest  `json:"artifacts"`
}

type pipelineMetadata struct {
	RunID      string `json:"runId"`
	Status     string `json:"status"`
	StartedAt  string `json:"startedAt"`
	EndedAt    string `json:"endedAt"`
	DurationMS int64  `json:"durationMs"`
	Mode       string `json:"mode"`
	Scenario   string `json:"scenario"`
	Seed       int64  `json:"seed"`
}

type inputManifest struct {
	TrainDatasetHash      string   `json:"trainDatasetHash"`
	ValidationDatasetHash string   `json:"validationDatasetHash"`
	MetricsHash           string   `json:"metricsHash"`
	BaselineProfileHash   string   `json:"baselineProfileHash"`
	DatasetOverlap        []string `json:"datasetOverlap"`
}

type runtimeMetadata struct {
	CandidateModel        string `json:"candidateModel"`
	WorkerModel           string `json:"workerModel"`
	APIKeyRequired        bool   `json:"apiKeyRequired"`
	SecretValuesPersisted bool   `json:"secretValuesPersisted"`
}

type baselineReport struct {
	Train              *evaluationSnapshot `json:"train"`
	Validation         *evaluationSnapshot `json:"validation"`
	Attributions       []attribution       `json:"attributions"`
	AttributionSummary map[string]int      `json:"attributionSummary"`
}

type roundReport struct {
	Round                int                 `json:"round"`
	ParentProfileHash    string              `json:"parentProfileHash"`
	CandidateProfileHash string              `json:"candidateProfileHash"`
	CandidatePrompt      string              `json:"candidatePrompt"`
	PromptIterDecision   promptIterDecision  `json:"promptiterDecision"`
	CandidateTrain       *evaluationSnapshot `json:"candidateTrain"`
	CandidateValidation  *evaluationSnapshot `json:"candidateValidation"`
	TrainDelta           *comparison         `json:"trainDelta"`
	ValidationDelta      *comparison         `json:"validationDelta"`
	GateDecision         gateDecision        `json:"gateDecision"`
}

type promptIterDecision struct {
	Accepted   bool    `json:"accepted"`
	ScoreDelta float64 `json:"scoreDelta"`
	Reason     string  `json:"reason"`
}

type artifactManifest struct {
	JSONReport            string   `json:"jsonReport"`
	MarkdownReport        string   `json:"markdownReport"`
	CandidatePrompt       string   `json:"candidatePrompt"`
	RoundCandidatePrompts []string `json:"roundCandidatePrompts"`
	AcceptedPrompt        *string  `json:"acceptedPrompt"`
}

func runPipeline(parent context.Context, cfg *config) (*optimizationReport, error) {
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	ctx, cancel := context.WithTimeout(parent, cfg.timeout())
	defer cancel()
	startedAt := time.Now()
	datasetOverlap, err := validateDatasetIsolation(cfg.Inputs.TrainEvalset, cfg.Inputs.ValidationEvalset, cfg.DatasetGuard)
	if err != nil {
		return nil, fmt.Errorf("dataset isolation: %w", err)
	}
	baselineBytes, err := os.ReadFile(cfg.Inputs.BaselinePrompt)
	if err != nil {
		return nil, fmt.Errorf("read baseline prompt: %w", err)
	}
	baselineInstruction := strings.TrimSpace(string(baselineBytes))
	trainHash, err := hashFile(cfg.Inputs.TrainEvalset)
	if err != nil {
		return nil, err
	}
	validationHash, err := hashFile(cfg.Inputs.ValidationEvalset)
	if err != nil {
		return nil, err
	}
	metricsHash, err := hashFile(cfg.Inputs.Metrics)
	if err != nil {
		return nil, err
	}
	baselineProfileHash := hashText(baselineInstruction)
	recorder := &accountingRecorder{}
	runtime, err := buildRuntime(ctx, cfg, baselineInstruction, recorder)
	if err != nil {
		return nil, err
	}
	defer runtime.close()

	baselineTrainResult, err := runtime.evaluate(ctx, trainEvalSetID, baselineInstruction, "baseline.train")
	if err != nil {
		return nil, err
	}
	baselineValidationResult, err := runtime.evaluate(ctx, validationEvalSetID, baselineInstruction, "baseline.validation")
	if err != nil {
		return nil, err
	}
	baselineTrain, err := buildSnapshot(baselineTrainResult, "train", trainHash, metricsHash, baselineProfileHash, cfg.Seed)
	if err != nil {
		return nil, fmt.Errorf("build train baseline snapshot: %w", err)
	}
	baselineValidation, err := buildSnapshot(baselineValidationResult, "validation", validationHash, metricsHash, baselineProfileHash, cfg.Seed)
	if err != nil {
		return nil, fmt.Errorf("build validation baseline snapshot: %w", err)
	}
	baselineAttributions, err := attributeFailures(baselineTrain)
	if err != nil {
		return nil, err
	}
	validationAttributions, err := attributeFailures(baselineValidation)
	if err != nil {
		return nil, err
	}
	allBaselineAttributions := append(append([]attribution(nil), baselineAttributions...), validationAttributions...)

	report := &optimizationReport{
		SchemaVersion: reportSchemaVersion,
		Pipeline: pipelineMetadata{
			RunID:     fmt.Sprintf("run-%d-%s", cfg.Seed, cfg.Scenario),
			Status:    "running",
			StartedAt: startedAt.UTC().Format(time.RFC3339Nano),
			Mode:      "deterministic_fake",
			Scenario:  cfg.Scenario,
			Seed:      cfg.Seed,
		},
		InputManifest: inputManifest{
			TrainDatasetHash:      trainHash,
			ValidationDatasetHash: validationHash,
			MetricsHash:           metricsHash,
			BaselineProfileHash:   baselineProfileHash,
			DatasetOverlap:        datasetOverlap,
		},
		Runtime: runtimeMetadata{
			CandidateModel:        "deterministic-candidate",
			WorkerModel:           "deterministic-worker",
			APIKeyRequired:        false,
			SecretValuesPersisted: false,
		},
		Baseline: baselineReport{
			Train:              baselineTrain,
			Validation:         baselineValidation,
			Attributions:       allBaselineAttributions,
			AttributionSummary: summarizeAttributions(allBaselineAttributions),
		},
		Rounds: make([]roundReport, 0, cfg.MaxRounds),
	}

	parentProfileHash := baselineProfileHash
	parentTrain := baselineTrain
	parentValidation := baselineValidation
	var parentProfile *promptiter.Profile
	for roundNumber := 1; roundNumber <= cfg.MaxRounds; roundNumber++ {
		attributions, err := attributeFailures(parentTrain)
		if err != nil {
			return nil, err
		}
		if len(attributions) == 0 {
			break
		}
		lossHints, err := lossHintsFromAttributions(attributions)
		if err != nil {
			return nil, err
		}
		promptIterResult, err := runtime.optimize(ctx, parentProfile, lossHints, cfg.Gate.MinValidationGain)
		if err != nil {
			return nil, err
		}
		promptIterRound := promptIterResult.Rounds[0]
		targetSurfaceID := astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction)
		candidateInstruction, err := instructionFromProfile(promptIterRound.OutputProfile, targetSurfaceID)
		if err != nil {
			return nil, err
		}
		candidateProfileHash := hashText(candidateInstruction)
		candidateTrainResult, err := runtime.evaluate(ctx, trainEvalSetID, candidateInstruction, fmt.Sprintf("candidate.%d.train", roundNumber))
		if err != nil {
			return nil, err
		}
		candidateValidationResult, err := runtime.evaluate(ctx, validationEvalSetID, candidateInstruction, fmt.Sprintf("candidate.%d.validation", roundNumber))
		if err != nil {
			return nil, err
		}
		candidateTrain, err := buildSnapshot(candidateTrainResult, "train", trainHash, metricsHash, candidateProfileHash, cfg.Seed)
		if err != nil {
			return nil, err
		}
		candidateValidation, err := buildSnapshot(candidateValidationResult, "validation", validationHash, metricsHash, candidateProfileHash, cfg.Seed)
		if err != nil {
			return nil, err
		}
		trainDelta, err := compareSnapshots(parentTrain, candidateTrain)
		if err != nil {
			return nil, err
		}
		validationDelta, err := compareSnapshots(parentValidation, candidateValidation)
		if err != nil {
			return nil, err
		}
		accounting := recorder.summary(time.Since(startedAt))
		decision := evaluateGate(
			cfg.Gate,
			cfg.Budget,
			parentTrain,
			candidateTrain,
			parentValidation,
			candidateValidation,
			trainDelta,
			validationDelta,
			accounting,
		)
		internalDecision := promptIterDecision{}
		if promptIterRound.Acceptance != nil {
			internalDecision.Accepted = promptIterRound.Acceptance.Accepted
			internalDecision.ScoreDelta = promptIterRound.Acceptance.ScoreDelta
			internalDecision.Reason = promptIterRound.Acceptance.Reason
		}
		report.Rounds = append(report.Rounds, roundReport{
			Round:                roundNumber,
			ParentProfileHash:    parentProfileHash,
			CandidateProfileHash: candidateProfileHash,
			CandidatePrompt:      candidateInstruction,
			PromptIterDecision:   internalDecision,
			CandidateTrain:       candidateTrain,
			CandidateValidation:  candidateValidation,
			TrainDelta:           trainDelta,
			ValidationDelta:      validationDelta,
			GateDecision:         decision,
		})
		report.FinalDecision = decision
		if decision.Accepted {
			parentProfileHash = candidateProfileHash
			parentTrain = candidateTrain
			parentValidation = candidateValidation
			parentProfile = promptIterRound.OutputProfile
			// A Release Gate acceptance is the terminal success condition. Stopping
			// here prevents a later experimental candidate from replacing a known
			// deployable prompt with a rejected one.
			break
		}
	}
	if len(report.Rounds) == 0 {
		report.FinalDecision = gateDecision{
			Accepted:    false,
			Deployable:  false,
			ReasonCodes: []string{"NO_OPTIMIZATION_NEEDED"},
			Summary:     "训练集没有失败项，未生成候选。",
		}
	}
	report.Accounting = recorder.summary(time.Since(startedAt))
	endedAt := time.Now()
	report.Pipeline.Status = "succeeded"
	report.Pipeline.EndedAt = endedAt.UTC().Format(time.RFC3339Nano)
	report.Pipeline.DurationMS = endedAt.Sub(startedAt).Milliseconds()
	roundPromptArtifacts := make([]string, 0, len(report.Rounds))
	for _, round := range report.Rounds {
		roundPromptArtifacts = append(roundPromptArtifacts, fmt.Sprintf("round-%03d-candidate_prompt.txt", round.Round))
	}
	report.Artifacts = artifactManifest{
		JSONReport:            "optimization_report.json",
		MarkdownReport:        "optimization_report.md",
		CandidatePrompt:       "candidate_prompt.txt",
		RoundCandidatePrompts: roundPromptArtifacts,
	}
	if report.FinalDecision.Accepted {
		acceptedPath := "accepted_prompt.txt"
		report.Artifacts.AcceptedPrompt = &acceptedPath
	}
	if err := writeReportArtifacts(cfg, report); err != nil {
		return nil, err
	}
	return report, nil
}

func summarizeAttributions(items []attribution) map[string]int {
	result := make(map[string]int)
	for _, item := range items {
		result[item.PrimaryCategory]++
	}
	return result
}

func writeReportArtifacts(cfg *config, report *optimizationReport) error {
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	candidatePrompts := make([][]byte, 0, len(report.Rounds))
	for _, round := range report.Rounds {
		promptBytes := []byte(round.CandidatePrompt + "\n")
		if containsPromptSecret(promptBytes) || string(redactReport(promptBytes)) != string(promptBytes) {
			return fmt.Errorf("round %d candidate prompt contains secret-like material", round.Round)
		}
		candidatePrompts = append(candidatePrompts, promptBytes)
	}
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}
	jsonData = redactReport(jsonData)
	markdown := redactReport([]byte(renderMarkdown(report)))
	if containsSecretCanary(jsonData) || containsSecretCanary(markdown) {
		return errors.New("report contains a secret canary")
	}
	if err := writeAtomic(cfg.reportJSONPath(), append(jsonData, '\n')); err != nil {
		return err
	}
	if err := writeAtomic(cfg.reportMarkdownPath(), markdown); err != nil {
		return err
	}
	candidatePrompt := ""
	if len(report.Rounds) > 0 {
		candidatePrompt = report.Rounds[len(report.Rounds)-1].CandidatePrompt
	}
	for index, promptBytes := range candidatePrompts {
		if index >= len(report.Artifacts.RoundCandidatePrompts) {
			return errors.New("round prompt artifact manifest is incomplete")
		}
		if err := writeAtomic(filepath.Join(cfg.OutputDir, report.Artifacts.RoundCandidatePrompts[index]), promptBytes); err != nil {
			return err
		}
	}
	if err := writeAtomic(filepath.Join(cfg.OutputDir, "candidate_prompt.txt"), []byte(candidatePrompt+"\n")); err != nil {
		return err
	}
	acceptedPath := filepath.Join(cfg.OutputDir, "accepted_prompt.txt")
	if !report.FinalDecision.Accepted {
		if err := os.Remove(acceptedPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale accepted prompt: %w", err)
		}
		return nil
	}
	return writeAtomic(acceptedPath, []byte(candidatePrompt+"\n"))
}

func renderMarkdown(report *optimizationReport) string {
	var builder strings.Builder
	builder.WriteString("# PromptIter Regression Loop Report\n\n")
	if report.FinalDecision.Accepted {
		builder.WriteString("结论：**接受候选 Prompt**。\n\n")
	} else {
		builder.WriteString("结论：**拒绝候选 Prompt**。\n\n")
	}
	builder.WriteString("- 场景：`" + report.Pipeline.Scenario + "`\n")
	builder.WriteString(fmt.Sprintf("- Baseline Train：`%.3f`\n", report.Baseline.Train.OverallScore))
	builder.WriteString(fmt.Sprintf("- Baseline Validation：`%.3f`\n", report.Baseline.Validation.OverallScore))
	builder.WriteString(fmt.Sprintf("- 模型调用：`%d`\n", report.Accounting.ModelCalls))
	builder.WriteString(fmt.Sprintf("- 总 Token：`%d`\n", report.Accounting.TotalTokens))
	builder.WriteString(fmt.Sprintf("- 耗时：`%dms`\n", report.Pipeline.DurationMS))
	builder.WriteString(fmt.Sprintf("- 成本：`%s`（`%s`）\n", formatCost(report.Accounting.Cost), report.Accounting.CostStatus))
	builder.WriteString("- Gate 理由：`" + strings.Join(report.FinalDecision.ReasonCodes, "`, `") + "`\n\n")
	for _, round := range report.Rounds {
		builder.WriteString(fmt.Sprintf("## Round %d\n\n", round.Round))
		builder.WriteString(fmt.Sprintf("- Candidate Train：`%.3f`，Delta `%.3f`\n", round.CandidateTrain.OverallScore, round.TrainDelta.OverallDelta))
		builder.WriteString(fmt.Sprintf("- Candidate Validation：`%.3f`，Delta `%.3f`\n", round.CandidateValidation.OverallScore, round.ValidationDelta.OverallDelta))
		builder.WriteString(fmt.Sprintf("- 独立 Gate：`%t`\n", round.GateDecision.Accepted))
		builder.WriteString(fmt.Sprintf("- PromptIter 内部 acceptance（仅审计）：`%t`\n\n", round.PromptIterDecision.Accepted))
		builder.WriteString(fmt.Sprintf("- 候选 Prompt 产物：`round-%03d-candidate_prompt.txt`\n\n", round.Round))
		builder.WriteString("### Validation 逐 Case/Metric Delta\n\n")
		builder.WriteString("| Case | Metric | Baseline | Candidate | Delta | Transition |\n")
		builder.WriteString("|---|---|---:|---:|---:|---|\n")
		for _, delta := range round.ValidationDelta.Metrics {
			builder.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s |\n",
				delta.CaseID,
				delta.MetricName,
				formatOptionalFloat(delta.BaselineScore),
				formatOptionalFloat(delta.CandidateScore),
				formatOptionalFloat(delta.ScoreDelta),
				delta.Transition,
			))
		}
		builder.WriteString("\n")
	}
	builder.WriteString("## 失败归因统计\n\n")
	keys := make([]string, 0, len(report.Baseline.AttributionSummary))
	for key := range report.Baseline.AttributionSummary {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString(fmt.Sprintf("- `%s`: %d\n", key, report.Baseline.AttributionSummary[key]))
	}
	builder.WriteString("\n## 发布产物\n\n")
	if report.FinalDecision.Accepted {
		builder.WriteString("已生成 `accepted_prompt.txt`。\n")
	} else {
		builder.WriteString("未生成可部署的 accepted prompt；候选仅用于审计。\n")
	}
	return builder.String()
}

func formatOptionalFloat(value *float64) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%.3f", *value)
}

func formatCost(value *float64) string {
	if value == nil {
		return "null"
	}
	return fmt.Sprintf("%.8f", *value)
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".promptiter-regression-*")
	if err != nil {
		return fmt.Errorf("create temporary artifact: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("commit artifact %s: %w", path, err)
	}
	return nil
}

var (
	jsonSecretPattern   = regexp.MustCompile(`(?i)("(?:authorization|api[_-]?key|x-api-key|access[_-]?token|secret|cookie)"\s*:\s*")[^"]*(")`)
	bearerPattern       = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/-]+=*`)
	promptSecretPattern = regexp.MustCompile(`(?i)(?:api[_-]?key|x-api-key|access[_-]?token|authorization|cookie|secret)\s*[:=]\s*[^\s,;]+`)
)

func redactReport(data []byte) []byte {
	redacted := jsonSecretPattern.ReplaceAll(data, []byte(`${1}[REDACTED]${2}`))
	return bearerPattern.ReplaceAll(redacted, []byte("Bearer [REDACTED]"))
}

func containsSecretCanary(data []byte) bool {
	return strings.Contains(string(data), "SECRET_CANARY_DO_NOT_PERSIST")
}

func containsPromptSecret(data []byte) bool {
	return containsSecretCanary(data) || bearerPattern.Match(data) || promptSecretPattern.Match(data)
}
