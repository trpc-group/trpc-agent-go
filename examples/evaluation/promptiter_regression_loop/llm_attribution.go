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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// defaultAttributionTimeout bounds a single LLM attribution call so a slow or
// hung model cannot block the whole audit. On timeout the caller falls back to
// the deterministic rule attribution.
const defaultAttributionTimeout = 60 * time.Second

// attributionSystemPrompt instructs the LLM to act as an evaluation-failure
// attribution expert. The output is constrained to a single JSON object so it can
// be parsed deterministically and never silently breaks the downstream report.
const attributionSystemPrompt = `你是一名评估失败归因专家。给定一个评测失败 case 的信息（评测集、case id、指标名、分数、judge 原始理由、执行 trace 摘要），判断失败的根本原因类别，并给出一段简洁、可解释、面向开发者修复 prompt 的中文说明（可以包含具体的修改方向）。

只能从以下类别中选择其一：
- response_mismatch：回复内容不满足评分标准或用户期望
- tool_call_error：执行过程中工具调用本身报错、未按要求发起或 func_call 失败
- tool_param_error：工具被调用但参数错误（类型不符、缺失必填参数、取值非法）
- route_error：agent 路由/转移（handoff）过程报错
- format_error：回复格式、结构或 JSON schema 不符合要求
- knowledge_gap：回复缺失关键知识或事实性召回不足
- unknown：依据现有信息无法判断

严格只输出一个 JSON 对象，不要输出任何额外文字或代码块标记：
{"category": "<上述类别之一>", "reason": "<中文解释>"}`

// llmAttributor is the optional LLM enhancement layer over the deterministic rule.
// It does not participate in the accept/reject gate: any failure is reported as an
// error so classifyFailures can fall back to ruleAttributor, keeping the gate
// reproducible and cost-free even when the LLM is unavailable.
type llmAttributor struct {
	model   model.Model
	timeout time.Duration
	stats   *llmCallStats
}

func (a *llmAttributor) Method() string { return "llm" }

func (a *llmAttributor) Attribute(ctx context.Context, in FailedMetricInput) (FailureCategory, string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: attributionSystemPrompt},
			{Role: model.RoleUser, Content: buildAttributionPrompt(in)},
		},
	}
	if a.stats != nil {
		a.stats.Calls++
	}
	ch, err := a.model.GenerateContent(reqCtx, req)
	if err != nil {
		if a.stats != nil {
			a.stats.Errors++
		}
		return "", "", err
	}
	content, err := readModelChannel(reqCtx, ch)
	if err != nil {
		return "", "", err
	}
	cat, reason, ok := parseLLMAttribution(content)
	if !ok {
		return "", "", errors.New("llm attribution: could not parse a valid category/reason JSON from the response")
	}
	return cat, reason, nil
}

// AttributeBatch attributes many failed metrics in a single model call, returning a
// slice aligned by index with the input. This collapses what would otherwise be N
// sequential per-case calls into one, which is the main cost/ latency win for LLM
// attribution on large evaluation sets.
func (a *llmAttributor) AttributeBatch(ctx context.Context, inputs []FailedMetricInput) ([]BatchAttribution, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	reqCtx, cancel := context.WithTimeout(ctx, a.timeout*time.Duration(len(inputs)+1))
	defer cancel()

	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: attributionSystemPrompt},
			{Role: model.RoleUser, Content: buildBatchAttributionPrompt(inputs)},
		},
	}
	if a.stats != nil {
		a.stats.Calls++
	}
	ch, err := a.model.GenerateContent(reqCtx, req)
	if err != nil {
		if a.stats != nil {
			a.stats.Errors++
		}
		return nil, err
	}
	content, err := readModelChannel(reqCtx, ch)
	if err != nil {
		return nil, err
	}
	out, ok := parseLLMBatchAttribution(content, len(inputs))
	if !ok {
		return nil, errors.New("llm batch attribution: could not parse a valid category/reason array from the response")
	}
	return out, nil
}

const batchAttributionSystemPromptSuffix = "\n当存在多条失败时，请一次性返回一个 JSON 数组，元素顺序与输入失败项一一对应，每项格式为 {\"category\": \"...\", \"reason\": \"...\"}。"

// buildBatchAttributionPrompt renders all failed metrics for a single batched call.
func buildBatchAttributionPrompt(inputs []FailedMetricInput) string {
	var b strings.Builder
	b.WriteString("以下是多个失败 case（编号 i 从 0 开始，请按相同顺序返回结果）：\n\n")
	for i, in := range inputs {
		fmt.Fprintf(&b, "[%d] evalSet=%s case=%s metric=%s score=%.3f\n", i, in.EvalSetID, in.EvalCaseID, in.MetricName, in.Score)
		fmt.Fprintf(&b, "    judge 理由: %s\n", oneLine(in.Reason))
		if in.Trace != nil {
			if in.Trace.Input != nil {
				fmt.Fprintf(&b, "    query: %s\n", clipText(in.Trace.Input.Text, 300))
			}
			if in.Trace.Output != nil {
				fmt.Fprintf(&b, "    response: %s\n", clipText(in.Trace.Output.Text, 300))
			}
			for j, s := range in.Trace.Steps {
				if s.Error != "" {
					fmt.Fprintf(&b, "    step[%d] %s error: %s\n", j, s.NodeType, s.Error)
				}
			}
		}
	}
	b.WriteString("\n" + batchAttributionSystemPromptSuffix)
	return b.String()
}

// parseLLMBatchAttribution extracts a JSON array of {category, reason} from
// free-form LLM text, tolerating prose and ```json fences. It returns ok=false if
// the array is missing, unparsable, or has the wrong length (so the caller can
// fall back to per-case attribution).
func parseLLMBatchAttribution(text string, n int) ([]BatchAttribution, bool) {
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")
	if start < 0 || end <= start {
		// Also accept a single object when n==1.
		if cat, reason, ok := parseLLMAttribution(text); ok && n == 1 {
			return []BatchAttribution{{Category: cat, Reason: reason}}, true
		}
		return nil, false
	}
	raw := text[start : end+1]
	var arr []struct {
		Category string `json:"category"`
		Reason   string `json:"reason"`
	}
	if json.Unmarshal([]byte(raw), &arr) != nil {
		return nil, false
	}
	if len(arr) != n {
		return nil, false
	}
	out := make([]BatchAttribution, n)
	for i, item := range arr {
		if strings.TrimSpace(item.Reason) == "" {
			return nil, false
		}
		out[i] = BatchAttribution{Category: normalizeCategory(item.Category), Reason: item.Reason}
	}
	return out, true
}

// readModelChannel drains a model response channel and returns the concatenated
// assistant text. It respects context cancellation so a stalled model cannot hang
// the audit loop.
func readModelChannel(ctx context.Context, ch <-chan *model.Response) (string, error) {
	var sb strings.Builder
	done := make(chan error, 1)
	go func() {
		for r := range ch {
			if r.Error != nil {
				done <- fmt.Errorf("llm attribution model error: %v", r.Error)
				return
			}
			for _, c := range r.Choices {
				sb.WriteString(c.Message.Content)
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			return "", err
		}
		return sb.String(), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// parseLLMAttribution extracts a {"category","reason"} JSON object from free-form
// LLM text (tolerating surrounding prose or ```json fences). A missing/empty
// reason is rejected so the caller can fall back to rules.
func parseLLMAttribution(text string) (FailureCategory, string, bool) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return "", "", false
	}
	raw := text[start : end+1]
	var out struct {
		Category string `json:"category"`
		Reason   string `json:"reason"`
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		return "", "", false
	}
	reason := strings.TrimSpace(out.Reason)
	if reason == "" {
		return "", "", false
	}
	return normalizeCategory(out.Category), reason, true
}

// normalizeCategory maps an LLM-provided category string to a known bucket,
// defaulting to unknown so an off-spec label never crashes downstream logic.
func normalizeCategory(s string) FailureCategory {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "response_mismatch":
		return FailureResponseMismatch
	case "tool_call_error":
		return FailureToolCallError
	case "tool_param_error":
		return FailureToolParamError
	case "route_error":
		return FailureRouteError
	case "format_error":
		return FailureFormatError
	case "knowledge_gap":
		return FailureKnowledgeGap
	default:
		return FailureUnknown
	}
}

// buildAttributionPrompt renders the failed-metric context for the LLM, including
// the user query, final response, and per-step trace (node type, error, I/O).
// Long texts are clipped to keep the prompt bounded and cheap.
func buildAttributionPrompt(in FailedMetricInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "评测集: %s\n", in.EvalSetID)
	fmt.Fprintf(&b, "Case ID: %s\n", in.EvalCaseID)
	fmt.Fprintf(&b, "指标: %s\n", in.MetricName)
	fmt.Fprintf(&b, "分数: %.3f\n", in.Score)
	fmt.Fprintf(&b, "Judge 原始理由: %s\n", oneLine(in.Reason))
	b.WriteString("执行 Trace 摘要:\n")
	if in.Trace != nil {
		if in.Trace.Input != nil {
			fmt.Fprintf(&b, "  - 输入(query): %s\n", clipText(in.Trace.Input.Text, 1200))
		}
		if in.Trace.Output != nil {
			fmt.Fprintf(&b, "  - 输出(response): %s\n", clipText(in.Trace.Output.Text, 1200))
		}
		for i, s := range in.Trace.Steps {
			fmt.Fprintf(&b, "  - step[%d] type=%s error=%s\n", i, s.NodeType, s.Error)
			if s.Input != nil {
				fmt.Fprintf(&b, "      in: %s\n", clipText(s.Input.Text, 400))
			}
			if s.Output != nil {
				fmt.Fprintf(&b, "      out: %s\n", clipText(s.Output.Text, 400))
			}
		}
	} else {
		b.WriteString("  (无 trace)\n")
	}
	return b.String()
}

func oneLine(s string) string {
	return clipText(strings.ReplaceAll(strings.TrimSpace(s), "\n", " "), 600)
}

func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// realLLMAvailable reports whether a real (non-fake) OpenAI-compatible LLM is
// configured: fake mode is always offline, and a real key must be present.
func realLLMAvailable(cfg regressionConfig) bool {
	return !cfg.Fake && strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
}

// selectAttributionMode resolves the configured attribution mode into a concrete
// "rule" or "llm" choice, expanding "auto" based on real-LLM availability.
func selectAttributionMode(cfg regressionConfig) string {
	mode := strings.ToLower(strings.TrimSpace(cfg.Attribution))
	if mode == "" {
		mode = "rule"
	}
	if mode == "auto" {
		if realLLMAvailable(cfg) {
			return "llm"
		}
		return "rule"
	}
	return mode
}

// loadAttributionModel builds the model client used by LLM-based attribution,
// defaulting to the judge model unless AttributionModelName is set.
func loadAttributionModel(cfg regressionConfig) (model.Model, error) {
	name := cfg.AttributionModelName
	if name == "" {
		name = cfg.JudgeModelName
	}
	return loadOpenAIModel(name, fakeRoleJudge, cfg.Fake, cfg.FakeScenario)
}

// buildAttributor selects the attribution strategy from config:
//   - "rule"  -> always deterministic ruleAttributor (default; reproducible, free)
//   - "llm"   -> llmAttributor over a real OpenAI model; errors if no real LLM is
//     available (fake mode or missing OPENAI_API_KEY)
//   - "auto"  -> llm when a real LLM is available, otherwise rule
//
// The LLM model defaults to the judge model unless AttributionModelName is set.
func buildAttributor(cfg regressionConfig, stats ...*llmCallStats) (Attributor, error) {
	if selectAttributionMode(cfg) != "llm" {
		return ruleAttributor{}, nil
	}
	if !realLLMAvailable(cfg) {
		return nil, fmt.Errorf("attribution=llm requires a real LLM (set OPENAI_API_KEY and do not use -fake)")
	}
	m, err := loadAttributionModel(cfg)
	if err != nil {
		return nil, err
	}
	var s *llmCallStats
	if len(stats) > 0 {
		s = stats[0]
	}
	return &llmAttributor{model: m, timeout: defaultAttributionTimeout, stats: s}, nil
}
