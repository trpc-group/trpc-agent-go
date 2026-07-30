package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// NarrativeInput carries the structured facts the Narrator turns into a natural
// language summary: the optimization outcome, the gate decision, and the
// attribution insights.
type NarrativeInput struct {
	BaselineScore  float64
	CandidateScore float64
	ScoreDelta     float64
	Accepted       bool
	GateReason     string
	GateRejectedBy string
	Insights       *AttributionInsights
	Failures       []CaseFailure
}

// Narrator writes a human-readable natural-language summary of the whole
// optimization+attribution outcome. The deterministic ruleNarrator is always
// available (and is the fallback when the LLM-enhanced reporter fails), so the
// report is always populated even offline.
type Narrator interface {
	Narrate(ctx context.Context, in NarrativeInput) (string, error)
	Method() string
}

// ruleNarrator produces a deterministic, template-based summary. Always available.
type ruleNarrator struct{}

func (ruleNarrator) Method() string { return "rule" }

func (ruleNarrator) Narrate(_ context.Context, in NarrativeInput) (string, error) {
	var b strings.Builder
	verdict := "已接受"
	if !in.Accepted {
		verdict = "已拒绝"
	}
	fmt.Fprintf(&b, "本次优化候选在验证集上得分为 %.4f（基线 %.4f，变化 %+.4f），gate 决策为%s。",
		in.CandidateScore, in.BaselineScore, in.ScoreDelta, verdict)
	if !in.Accepted {
		fmt.Fprintf(&b, "拒绝理由：%s", in.GateReason)
		if in.GateRejectedBy != "" {
			fmt.Fprintf(&b, "（%s）", in.GateRejectedBy)
		}
		fmt.Fprintf(&b, "。")
	} else {
		b.WriteString("候选满足接受条件。")
	}
	if ins := in.Insights; ins != nil && ins.Summary != "" {
		fmt.Fprintf(&b, " %s", ins.Summary)
	}
	return b.String(), nil
}

// llmCallStats records how many LLM attribution/report calls were attempted and
// how many failed. It is the observability surface for the "LLM enhancement is
// optional" guarantee: even when every LLM call fails, the loop still produces a
// complete, rule-based report, and these counters tell the operator what happened.
type llmCallStats struct {
	Calls  int
	Errors int
}

// EnhancedReporter collapses the cross-case insight summary + suggested fix +
// natural-language narrative into a SINGLE LLM call (the main cost/ latency win of
// the "rule-primary, LLM-enhanced" design). It receives the already-computed
// deterministic failure patterns (so counts are never wrong) plus the gate facts,
// and returns a structured report. Any failure returns an error so the caller
// falls back to the deterministic rule narrative.
type EnhancedReporter interface {
	Report(ctx context.Context, in EnhancedInput) (*EnhancedReport, error)
	Method() string
}

// EnhancedInput is the structured context handed to the EnhancedReporter.
// Patterns must already be aggregated deterministically (so the LLM only writes
// prose, never counts).
type EnhancedInput struct {
	BaselineScore  float64
	CandidateScore float64
	ScoreDelta     float64
	Accepted       bool
	GateReason     string
	GateRejectedBy string
	Insights       *AttributionInsights // deterministic patterns + rule summary
	Failures       []CaseFailure
}

// EnhancedReport is the structured output of the EnhancedReporter.
type EnhancedReport struct {
	Summary      string
	SuggestedFix string
	Narrative    string
}

type llmEnhancedReporter struct {
	model   model.Model
	timeout time.Duration
	stats   *llmCallStats
}

func (a *llmEnhancedReporter) Method() string { return "llm" }

func (a *llmEnhancedReporter) Report(ctx context.Context, in EnhancedInput) (*EnhancedReport, error) {
	reqCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: enhancedSystemPrompt},
			{Role: model.RoleUser, Content: buildEnhancedPrompt(in)},
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
		if a.stats != nil {
			a.stats.Errors++
		}
		return nil, err
	}
	out, ok := parseEnhancedReport(content)
	if !ok {
		if a.stats != nil {
			a.stats.Errors++
		}
		return nil, errors.New("enhanced reporter: could not parse a valid {summary,suggestedFix,narrative} JSON from the response")
	}
	return out, nil
}

const enhancedSystemPrompt = `你是一名评测优化报告的撰写者。基于以下结构化信息（分数变化、gate 决策、失败模式统计、失败样例），一次性产出三部分内容，并严格只输出一个 JSON 对象（不要输出任何额外文字或代码块标记）：
- "summary": 一句话结论，覆盖主要失败模式（简体中文）
- "suggestedFix": 可操作的 prompt 修复建议（简体中文，2-4 条，可用换行分隔）
- "narrative": 一段简体中文自然语言摘要（Markdown 段落，1-3 段，控制在 250 字以内），覆盖本次优化结论、分数变化、关键失败模式、以及修复建议
输出格式示例：{"summary":"...","suggestedFix":"...","narrative":"..."}`

// buildEnhancedPrompt renders the aggregated facts for the single merged LLM call.
// Only the deterministic pattern counts + a few concrete failure examples are sent,
// so the model writes prose instead of re-counting (counts stay exact and cheap).
func buildEnhancedPrompt(in EnhancedInput) string {
	var b strings.Builder
	verdict := "已接受"
	if !in.Accepted {
		verdict = "已拒绝"
	}
	fmt.Fprintf(&b, "优化结论：候选在验证集得分 %.4f，基线 %.4f，变化 %+.4f，gate 决策%s。\n",
		in.CandidateScore, in.BaselineScore, in.ScoreDelta, verdict)
	if !in.Accepted {
		fmt.Fprintf(&b, "拒绝理由：%s", in.GateReason)
		if in.GateRejectedBy != "" {
			fmt.Fprintf(&b, "（%s）", in.GateRejectedBy)
		}
		b.WriteString("。\n")
	}
	if ins := in.Insights; ins != nil {
		fmt.Fprintf(&b, "失败模式统计（精确计数，勿改写）：%s\n", ins.Summary)
		for i, p := range ins.Patterns {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  - %s: %d 个 (%.0f%%)", p.Category, p.Count, p.Ratio*100)
			if p.Example != "" {
				fmt.Fprintf(&b, " 例如：%s", oneLine(p.Example))
			}
			b.WriteString("\n")
		}
	}
	// Up to 3 concrete failure examples for grounding.
	shown := 0
	for _, f := range in.Failures {
		if strings.TrimSpace(f.Reason) == "" {
			continue
		}
		fmt.Fprintf(&b, "失败样例(%s/%s/%s): %s\n", f.EvalSetID, f.EvalCaseID, f.MetricName, oneLine(f.Reason))
		shown++
		if shown >= 3 {
			break
		}
	}
	return b.String()
}

// parseEnhancedReport extracts a {summary, suggestedFix, narrative} JSON object
// from free-form LLM text, tolerating prose and ```json fences. narrative is
// required (so a silent empty prose is rejected and the caller falls back to rule).
func parseEnhancedReport(text string) (*EnhancedReport, bool) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return nil, false
	}
	raw := text[start : end+1]
	var out struct {
		Summary      string `json:"summary"`
		SuggestedFix string `json:"suggestedFix"`
		Narrative    string `json:"narrative"`
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil, false
	}
	narrative := stripFences(out.Narrative)
	if strings.TrimSpace(narrative) == "" {
		return nil, false
	}
	return &EnhancedReport{
		Summary:      strings.TrimSpace(out.Summary),
		SuggestedFix: strings.TrimSpace(out.SuggestedFix),
		Narrative:    narrative,
	}, true
}

// stripFences removes optional ```markdown / ``` fences from an LLM-paragraph.
func stripFences(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "```") {
		if i := strings.Index(t, "\n"); i >= 0 {
			t = strings.TrimSpace(t[i+1:])
		}
	}
	if strings.HasSuffix(t, "```") {
		t = strings.TrimSpace(strings.TrimSuffix(t, "```"))
	}
	return t
}

// buildEnhancedReporter builds the merged aggregation+narrative reporter. It is
// only used in LLM mode (attribution=llm, real LLM available); the caller keeps the
// deterministic rule narrative as the fallback on any error.
func buildEnhancedReporter(cfg regressionConfig, stats ...*llmCallStats) (EnhancedReporter, error) {
	if selectAttributionMode(cfg) != "llm" {
		return nil, fmt.Errorf("enhanced reporter requires attribution=llm")
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
	return &llmEnhancedReporter{model: m, timeout: defaultAttributionTimeout, stats: s}, nil
}
