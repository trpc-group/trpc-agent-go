//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llm 负责模型审查 Provider 和结果合并策略。
package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

const (
	SourceFake = "fake_model"
	SourceReal = "model"

	ProviderAuditFake   = "fake"
	ProviderAuditCustom = "custom"
	ProviderAuditHTTP   = "http"
	BackendOfficial     = "trpc-agent-go/model.Model"
	BackendHTTP         = "http"
	BackendOpenAI       = "trpc-agent-go/model/openai"
)

// Provider 是语义审查 Provider 边界。
type Provider interface {
	Review(context.Context, Input) (Output, error)
}

// Input 是发送给语义审查 Provider 的脱敏载荷。
type Input struct {
	DiffSummary       string                   `json:"diff_summary"`
	InputMetadata     review.InputMetadata     `json:"input_metadata"`
	ExistingFindings  []review.Finding         `json:"existing_findings"`
	SandboxSummary    review.SandboxSummary    `json:"sandbox_summary"`
	GovernanceSummary review.GovernanceSummary `json:"governance_summary"`
}

// Output 是 Provider 返回的增量审查结果。
type Output struct {
	Findings []review.Finding `json:"findings"`
}

// ProviderFunc 把函数适配为 Provider。
type ProviderFunc func(context.Context, Input) (Output, error)

func (f ProviderFunc) Review(ctx context.Context, input Input) (Output, error) {
	return f(ctx, input)
}

// RunSummary 记录模型审查审计指标。
type RunSummary struct {
	CallCount      int
	FindingCount   int
	DurationMS     int64
	ExceptionCount int
	Provider       string
	Name           string
	Backend        string
}

// Audit 记录不含敏感信息的 Provider 身份。
type Audit struct {
	Provider string
	Name     string
	Backend  string
}

// ProviderSelectionConfig 为启用的模型评审选择 Provider。
type ProviderSelectionConfig struct {
	Enabled bool
	Custom  Provider
	HTTP    HTTPConfig
	OpenAI  OpenAIConfig
}

// ConfiguredProvider 为当前模式选择语义审查 Provider。
func ConfiguredProvider(cfg ProviderSelectionConfig) (Provider, Audit) {
	if !cfg.Enabled {
		return nil, Audit{}
	}
	if cfg.OpenAI.Enabled {
		audit := OpenAIModelAudit(cfg.OpenAI)
		provider, err := NewOpenAIReviewProvider(cfg.OpenAI)
		if err != nil {
			return ProviderFunc(func(ctx context.Context, input Input) (Output, error) {
				_ = ctx
				_ = input
				return Output{}, err
			}), audit
		}
		return provider, audit
	}
	if cfg.HTTP.Enabled {
		audit := Audit{
			Provider: ProviderAuditHTTP,
			Name:     ProviderName(cfg.HTTP.Model),
			Backend:  BackendHTTP,
		}
		provider, err := NewHTTPProvider(cfg.HTTP)
		if err != nil {
			return ProviderFunc(func(ctx context.Context, input Input) (Output, error) {
				_ = ctx
				_ = input
				return Output{}, err
			}), audit
		}
		return ProviderThroughOfficialModel(ProviderName(cfg.HTTP.Model), provider), audit
	}
	if cfg.Custom != nil {
		return ProviderThroughOfficialModel(DefaultModelAdapterName, cfg.Custom), Audit{
			Provider: ProviderAuditCustom,
			Name:     DefaultModelAdapterName,
			Backend:  BackendOfficial,
		}
	}
	return ProviderThroughOfficialModel(SourceFake, FakeProvider{}), Audit{
		Provider: ProviderAuditFake,
		Name:     SourceFake,
		Backend:  BackendOfficial,
	}
}

func ProviderName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return DefaultModelAdapterName
	}
	return name
}

// RunReview 调用语义 Provider，并合并它返回的增量 findings。
func RunReview(ctx context.Context, taskID string, provider Provider, audit Audit, result review.Result, diff []byte, inputMeta review.InputMetadata) (review.Result, RunSummary) {
	summary := RunSummary{
		CallCount: 1,
		Provider:  audit.Provider,
		Name:      audit.Name,
		Backend:   audit.Backend,
	}
	input := Input{
		DiffSummary:       review.RedactSecrets(string(diff)),
		InputMetadata:     inputMeta,
		ExistingFindings:  SanitizedFindingSnapshot(result.Findings, result.Warnings),
		SandboxSummary:    result.SandboxSummary,
		GovernanceSummary: result.GovernanceSummary,
	}
	start := time.Now()
	output, err := provider.Review(ctx, input)
	summary.DurationMS = time.Since(start).Milliseconds()
	if summary.DurationMS == 0 {
		summary.DurationMS = 1
	}
	if err != nil {
		summary.ExceptionCount = 1
		return ResultWithModelError(result, taskID, err), summary
	}
	result = MergeFindings(result, output.Findings)
	summary.FindingCount = CountModelSourceFindings(result.Findings) + CountModelSourceFindings(result.Warnings)
	return result, summary
}

// SanitizedFindingSnapshot 返回已脱敏、去重的现有 findings 快照。
func SanitizedFindingSnapshot(findings, warnings []review.Finding) []review.Finding {
	out := make([]review.Finding, 0, len(findings)+len(warnings))
	for _, finding := range append(append([]review.Finding(nil), findings...), warnings...) {
		out = append(out, SanitizeFinding(finding))
	}
	return review.DedupeFindings(out)
}

// MergeFindings 合并 Provider findings，并避免重复规则 findings。
func MergeFindings(result review.Result, modelFindings []review.Finding) review.Result {
	existing := map[string]struct{}{}
	for _, finding := range append(append([]review.Finding(nil), result.Findings...), result.Warnings...) {
		existing[finding.DedupeKey()] = struct{}{}
	}
	for _, finding := range modelFindings {
		finding = NormalizeFinding(finding)
		if _, ok := existing[finding.DedupeKey()]; ok {
			continue
		}
		existing[finding.DedupeKey()] = struct{}{}
		if strings.EqualFold(strings.TrimSpace(finding.Confidence), "high") {
			finding.Status = "finding"
			result.Findings = append(result.Findings, finding)
			continue
		}
		finding.Status = "needs_human_review"
		result.Warnings = append(result.Warnings, finding)
	}
	result.Findings = review.DedupeFindings(result.Findings)
	result.Warnings = review.DedupeFindings(result.Warnings)
	return result
}

// NormalizeFinding 填充默认值并归一化 source。
func NormalizeFinding(f review.Finding) review.Finding {
	f = SanitizeFinding(f)
	f.Source = NormalizeSource(f.Source)
	if f.Confidence == "" {
		f.Confidence = "low"
	}
	if f.RuleID == "" {
		f.RuleID = "model-review"
	}
	if f.Category == "" {
		f.Category = "model"
	}
	if f.Severity == "" {
		f.Severity = "low"
	}
	if f.Title == "" {
		f.Title = "Model review signal"
	}
	return f
}

// SanitizeFinding 在 Provider 输出进入报告和存储前脱敏 evidence。
func SanitizeFinding(f review.Finding) review.Finding {
	f.Evidence = review.RedactSecrets(f.Evidence)
	if f.Status == "" {
		f.Status = "finding"
	}
	return f
}

func NormalizeSource(source string) string {
	source = strings.TrimSpace(source)
	switch source {
	case SourceFake:
		return SourceFake
	default:
		return SourceReal
	}
}

// ResultWithModelError 把 Provider 失败转换成人工复核证据。
func ResultWithModelError(result review.Result, taskID string, err error) review.Result {
	if result.Metrics.ExceptionCounts == nil {
		result.Metrics.ExceptionCounts = map[string]int{}
	}
	result.Metrics.ExceptionCounts["model_provider"]++
	result.Warnings = append(result.Warnings, review.Finding{
		Severity:       "low",
		Category:       "model",
		Title:          "Model review provider failed",
		Evidence:       review.RedactSecrets(fmt.Sprintf("%s: %v", taskID, err)),
		Recommendation: "Ask a human reviewer to inspect semantic and cross-file risks.",
		Confidence:     "high",
		Source:         SourceReal,
		RuleID:         "model-provider-failed",
		Status:         "needs_human_review",
	})
	return result
}

// CountModelSourceFindings 统计 fake 和真实模型来源的 findings。
func CountModelSourceFindings(findings []review.Finding) int {
	count := 0
	for _, finding := range findings {
		if finding.Source == SourceReal || finding.Source == SourceFake {
			count++
		}
	}
	return count
}

// FakeProvider 为 fake-model 模式提供确定性的无网络 Provider。
type FakeProvider struct{}

func (FakeProvider) Review(ctx context.Context, input Input) (Output, error) {
	_ = ctx
	if !strings.Contains(input.DiffSummary, "CR_AGENT_FAKE_MODEL_") {
		return Output{}, nil
	}
	parsed, err := review.ParseUnifiedDiff(input.DiffSummary)
	if err != nil {
		return Output{}, err
	}
	var findings []review.Finding
	for _, file := range parsed.Files {
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind != "add" {
					continue
				}
				confidence := ""
				switch {
				case strings.Contains(line.Text, "CR_AGENT_FAKE_MODEL_HIGH"):
					confidence = "high"
				case strings.Contains(line.Text, "CR_AGENT_FAKE_MODEL_LOW"):
					confidence = "low"
				default:
					continue
				}
				signal := fakeSignalForLine(line.Text)
				findings = append(findings, review.Finding{
					Severity:       signal.Severity,
					Category:       signal.Category,
					File:           file.Path,
					Line:           line.NewLine,
					Title:          signal.Title,
					Evidence:       strings.TrimSpace(line.Text),
					Recommendation: signal.Recommendation,
					Confidence:     confidence,
					Source:         SourceFake,
					RuleID:         signal.RuleID,
				})
			}
		}
	}
	return Output{Findings: findings}, nil
}

type fakeSignal struct {
	RuleID         string
	Severity       string
	Category       string
	Title          string
	Recommendation string
}

func fakeSignalForLine(text string) fakeSignal {
	for marker, signal := range map[string]fakeSignal{
		"CR_AGENT_FAKE_MODEL_AUTHZ_BYPASS": {
			RuleID:         "fake-model-authz-bypass",
			Severity:       "high",
			Category:       "authorization",
			Title:          "Semantic authorization bypass risk",
			Recommendation: "Require explicit authorization checks for the newly allowed branch.",
		},
		"CR_AGENT_FAKE_MODEL_NIL_BOUNDARY": {
			RuleID:         "fake-model-nil-boundary",
			Severity:       "medium",
			Category:       "boundary",
			Title:          "Nil or zero-value boundary changes behavior",
			Recommendation: "Add explicit handling and tests for nil or zero-value input.",
		},
		"CR_AGENT_FAKE_MODEL_STATE_INCONSISTENCY": {
			RuleID:         "fake-model-state-inconsistency",
			Severity:       "medium",
			Category:       "state",
			Title:          "Cross-function state transition is inconsistent",
			Recommendation: "Keep state transitions and persisted status values aligned.",
		},
		"CR_AGENT_FAKE_MODEL_TRANSACTION_SEMANTIC": {
			RuleID:         "fake-model-transaction-semantic",
			Severity:       "high",
			Category:       "database",
			Title:          "Transaction semantics can commit a failed operation",
			Recommendation: "Rollback on semantic failure paths before returning success or committing.",
		},
		"CR_AGENT_FAKE_MODEL_ERROR_SWALLOW": {
			RuleID:         "fake-model-error-swallow",
			Severity:       "high",
			Category:       "error_handling",
			Title:          "Error is swallowed and reported as success",
			Recommendation: "Propagate or handle the error instead of returning a successful result.",
		},
	} {
		if strings.Contains(text, marker) {
			return signal
		}
	}
	return fakeSignal{
		RuleID:         "fake-model-semantic-risk",
		Severity:       "medium",
		Category:       "logic",
		Title:          "Fake model semantic review signal",
		Recommendation: "Inspect the semantic risk before merging.",
	}
}
