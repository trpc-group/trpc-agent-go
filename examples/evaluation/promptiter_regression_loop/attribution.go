package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// caseIDPattern strips eval/case identifiers (e.g. "case-123", "v1", "nba-42")
// from a failure reason so two occurrences of the same root-cause text with
// different ids still collapse into one cluster. It only matches when an id
// prefix is followed by at least one digit, so ordinary words are never touched.
var caseIDPattern = regexp.MustCompile(`\b(case|v|eval|set|c)[-_/]?\d+[0-9a-z]*\b`)

// numberPattern strips bare numbers so "score 0.2" and "score 0.3" match.
var numberPattern = regexp.MustCompile(`\d+(\.\d+)?`)

// FailureCategory enumerates the coarse reason buckets for an eval failure.
type FailureCategory string

const (
	FailureResponseMismatch FailureCategory = "response_mismatch"
	FailureToolCallError    FailureCategory = "tool_call_error"
	FailureToolParamError   FailureCategory = "tool_param_error"
	FailureRouteError       FailureCategory = "route_error"
	FailureFormatError      FailureCategory = "format_error"
	FailureKnowledgeGap     FailureCategory = "knowledge_gap"
	FailureUnknown          FailureCategory = "unknown"
)

// CaseFailure describes one failed metric on one eval case, with attribution.
type CaseFailure struct {
	EvalSetID  string          `json:"evalSetId"`
	EvalCaseID string          `json:"evalCaseId"`
	MetricName string          `json:"metricName"`
	Score      float64         `json:"score"`
	Reason     string          `json:"reason"`
	Category   FailureCategory `json:"category"`
}

// AttributionResult aggregates failure attribution across an evaluation result.
type AttributionResult struct {
	TotalCases  int            `json:"totalCases"`
	FailedCases int            `json:"failedCases"`
	Failures    []CaseFailure  `json:"failures"`
	ByCategory  map[string]int `json:"byCategory"`
	Method      string         `json:"method"`
	// Clusters groups the individual failures into de-duplicated buckets so a
	// long failure list collapses into a few actionable clusters (pattern + count
	// + representative reason + sample case ids). Always computed deterministically.
	Clusters []FailureCluster `json:"clusters,omitempty"`
	// Insights is the higher-order summary produced after per-case attribution:
	// dominant failure patterns, a one-line conclusion, and an optional overall
	// fix direction. It is nil when no failures were attributed.
	Insights *AttributionInsights `json:"insights,omitempty"`
}

// FailureCluster is one de-duplicated group of similar failures.
type FailureCluster struct {
	Category FailureCategory `json:"category"`
	Count    int             `json:"count"`
	Reason   string          `json:"reason"` // representative reason for the cluster
	CaseIDs  []string        `json:"caseIds,omitempty"`
}

// FailurePattern is one aggregated failure bucket in the insight summary.
type FailurePattern struct {
	Category FailureCategory `json:"category"`
	Count    int             `json:"count"`
	Ratio    float64         `json:"ratio"`
	Example  string          `json:"example,omitempty"`
}

// AttributionInsights is the cross-case aggregation produced after per-case
// attribution. Pattern counts are always computed deterministically from the real
// failures (so the numbers are never wrong); the LLM enhancement only contributes
// the natural-language Summary and SuggestedFix.
type AttributionInsights struct {
	Method       string           `json:"method"`
	Summary      string           `json:"summary"`
	SuggestedFix string           `json:"suggestedFix,omitempty"`
	Patterns     []FailurePattern `json:"patterns"`
}

// Attributor attributes a single failed metric to a coarse failure category and
// an interpretable reason. The deterministic ruleAttributor guarantees
// reproducibility for the accept/reject gate; llmAttributor is an optional
// enhancement layer that produces richer, semantic reasons. Any LLM failure is
// surfaced as an error so the caller can fall back to rules.
type Attributor interface {
	// Attribute returns (category, reason, error). A non-nil error signals the
	// caller to fall back to the deterministic rule attribution.
	Attribute(ctx context.Context, in FailedMetricInput) (FailureCategory, string, error)
	// Method names the attribution strategy ("rule" or "llm") for the audit.
	Method() string
}

// FailedMetricInput is the context handed to an Attributor for one failed metric.
type FailedMetricInput struct {
	EvalSetID  string
	EvalCaseID string
	MetricName string
	Score      float64
	Reason     string // judge's original textual reason, possibly empty
	Trace      *atrace.Trace
}

// ruleAttributor is the deterministic baseline attribution. It never errors, so
// it is the safe fallback when the LLM enhancement is unavailable or fails.
type ruleAttributor struct{}

func (ruleAttributor) Method() string { return "rule" }

func (ruleAttributor) Attribute(_ context.Context, in FailedMetricInput) (FailureCategory, string, error) {
	cat := classifyMetric(promptiterengine.MetricResult{MetricName: in.MetricName, Reason: in.Reason}, in.Trace)
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = explainCategory(cat, promptiterengine.MetricResult{MetricName: in.MetricName, Score: in.Score})
	}
	return cat, reason, nil
}

// BatchAttribution is one result of a batched attribution call, aligned by index
// with the submitted FailedMetricInput slice.
type BatchAttribution struct {
	Category FailureCategory
	Reason   string
}

// BatchAttributor is an optional Attributor capability: it attributes many failed
// metrics in a single model call, which is far cheaper and faster than N sequential
// calls for LLM-based attribution. Attributors that do not implement it (e.g. the
// deterministic rule) fall back to per-case Attribute via classifyFailures.
type BatchAttributor interface {
	AttributeBatch(ctx context.Context, in []FailedMetricInput) ([]BatchAttribution, error)
}

// classifyFailures walks every failed metric in result and attributes it to a
// coarse failure category using the provided Attributor (rule or llm). result may
// be nil (returns an empty attribution). The attribution method is recorded for
// the audit report.
//
// When the Attributor also implements BatchAttributor, all failures are attributed
// in a single call (the LLM path collapses N sequential calls into one); otherwise
// it falls back to per-case calls. Any batch error degrades to per-case, and any
// per-case LLM error degrades to the deterministic rule, so the gate stays
// reproducible and the report is always populated.
func classifyFailures(ctx context.Context, result *promptiterengine.EvaluationResult, attr Attributor) *AttributionResult {
	ar := &AttributionResult{ByCategory: map[string]int{}, Method: attr.Method()}
	if result == nil {
		return ar
	}
	type pending struct {
		evalSetID  string
		evalCaseID string
		metric     promptiterengine.MetricResult
		in         FailedMetricInput
	}
	var pendings []pending
	for _, es := range result.EvalSets {
		for _, c := range es.Cases {
			ar.TotalCases++
			caseFailed := false
			for _, m := range c.Metrics {
				if !isFailedMetric(m) {
					continue
				}
				caseFailed = true
				pendings = append(pendings, pending{
					evalSetID: es.EvalSetID,
					evalCaseID: c.EvalCaseID,
					metric:     m,
					in: FailedMetricInput{
						EvalSetID:  es.EvalSetID,
						EvalCaseID: c.EvalCaseID,
						MetricName: m.MetricName,
						Score:      m.Score,
						Reason:     m.Reason,
						Trace:      c.Trace,
					},
				})
			}
			if caseFailed {
				ar.FailedCases++
			}
		}
	}

	cats := make([]FailureCategory, len(pendings))
	reasons := make([]string, len(pendings))
	if ba, ok := attr.(BatchAttributor); ok && len(pendings) > 0 {
		inputs := make([]FailedMetricInput, len(pendings))
		for i, p := range pendings {
			inputs[i] = p.in
		}
		if batch, err := ba.AttributeBatch(ctx, inputs); err == nil && len(batch) == len(pendings) {
			for i, b := range batch {
				cats[i], reasons[i] = b.Category, b.Reason
			}
		} else {
			// Batch failed (parse error, timeout, unparseable): degrade to the
			// deterministic rule per-case so we never pay N extra LLM calls and
			// the report is still populated.
			for i := range pendings {
				cats[i], reasons[i] = attributeOne(ctx, ruleAttributor{}, pendings[i])
			}
		}
	} else {
		for i := range pendings {
			cats[i], reasons[i] = attributeOne(ctx, attr, pendings[i])
		}
	}

	for i, p := range pendings {
		cat, reason := cats[i], reasons[i]
		ar.Failures = append(ar.Failures, CaseFailure{
			EvalSetID:  p.evalSetID,
			EvalCaseID: p.evalCaseID,
			MetricName: p.metric.MetricName,
			Score:      p.metric.Score,
			Reason:     reason,
			Category:   cat,
		})
		ar.ByCategory[string(cat)]++
	}
	ar.Clusters = buildClusters(ar.Failures)
	return ar
}

// buildClusters de-duplicates individual failures into a few actionable buckets
// keyed by (category, normalized reason). This is what turns a 200-line failure
// dump into "3 clusters: format_error x120, tool_call_error x60, ..." so a human
// (or a downstream optimizer) can act on patterns, not noise. Representative reason
// is taken from the first failure in the cluster; up to 5 sample case ids are kept.
func buildClusters(failures []CaseFailure) []FailureCluster {
	if len(failures) == 0 {
		return nil
	}
	type agg struct {
		category FailureCategory
		reason   string
		count    int
		caseIDs  []string
	}
	groups := map[string]*agg{}
	var order []string
	for _, f := range failures {
		key := string(f.Category) + "::" + normalizedReason(f.Reason)
		g, ok := groups[key]
		if !ok {
			g = &agg{category: f.Category, reason: f.Reason, count: 0}
			groups[key] = g
			order = append(order, key)
		}
		g.count++
		if len(g.caseIDs) < 5 {
			g.caseIDs = append(g.caseIDs, f.EvalSetID+"/"+f.EvalCaseID)
		}
	}
	clusters := make([]FailureCluster, 0, len(order))
	for _, key := range order {
		g := groups[key]
		clusters = append(clusters, FailureCluster{
			Category: g.category,
			Count:    g.count,
			Reason:   collapseRepeats(g.reason),
			CaseIDs:  g.caseIDs,
		})
	}
	// Sort by count desc, then category for stable output.
	for i := 0; i < len(clusters); i++ {
		for j := i + 1; j < len(clusters); j++ {
			if clusters[j].Count > clusters[i].Count ||
				(clusters[j].Count == clusters[i].Count && clusters[j].Category < clusters[i].Category) {
				clusters[i], clusters[j] = clusters[j], clusters[i]
			}
		}
	}
	return clusters
}

// normalizedReason reduces a failure reason to a dedup key: lowercased, with
// case ids / numbers stripped and whitespace collapsed, then truncated. Two
// failures with the same root cause but different case ids still collapse.
func normalizedReason(reason string) string {
	r := strings.ToLower(reason)
	r = caseIDPattern.ReplaceAllString(r, "")
	r = numberPattern.ReplaceAllString(r, "")
	fields := strings.Fields(r)
	r = strings.Join(fields, " ")
	if len(r) > 80 {
		r = r[:80]
	}
	return r
}

// collapseRepeats de-duplicates consecutive identical sentences in a reason so a
// framework that concatenates the same rubric reason N times (common with judges
// that emit one reason per rubric) shows only once in a cluster's representative
// reason. It splits on common CJK/Latin terminators and keeps the first of any
// run of identical sentences.
func collapseRepeats(reason string) string {
	hasTerm := strings.ContainsAny(reason, "。！？!?;；\n\r")
	parts := strings.FieldsFunc(reason, func(r rune) bool {
		return r == '。' || r == '！' || r == '？' || r == '!' || r == '?' || r == ';' || r == '；' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	prev := ""
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == prev {
			continue
		}
		out = append(out, p)
		prev = p
	}
	if len(out) == 0 {
		return strings.TrimSpace(reason)
	}
	if !hasTerm {
		return strings.Join(out, " ")
	}
	return strings.Join(out, "。") + "。"
}

// attributeOne attributes a single failed metric with the deterministic-rule
// fallback applied on any LLM error.
func attributeOne(ctx context.Context, attr Attributor, p struct {
	evalSetID  string
	evalCaseID string
	metric     promptiterengine.MetricResult
	in         FailedMetricInput
}) (FailureCategory, string) {
	cat, reason, err := attr.Attribute(ctx, p.in)
	if err != nil {
		cat = classifyMetric(p.metric, p.in.Trace)
		reason = strings.TrimSpace(p.metric.Reason)
		if reason == "" {
			reason = explainCategory(cat, p.metric)
		}
	}
	return cat, reason
}

// isFailedMetric reports whether a metric result is a failure. A metric is
// considered failed when it is explicitly marked failed, or its score is below
// the threshold (scores are expected to be high, close to 1.0).
func isFailedMetric(m promptiterengine.MetricResult) bool {
	return m.Status == status.EvalStatusFailed || m.Score < 1.0
}

// classifyMetric maps a failed metric to a coarse failure category. When the
// case carries an execution Trace, tool/route errors are detected from the
// ground-truth Step nodes (a tool step that errored, or any step error during an
// agent transfer) instead of relying on the often-generic judge reason text.
// This grounds attribution in what actually happened. When no trace is available
// (e.g. unit tests, or runners that do not emit traces), the cheap deterministic
// keyword heuristic on metric name + reason is used as a fallback.
//
// The signals used are exactly the ones in the spec: the metric name (the rubric
// id), the judge reason (which references the final response / structured output),
// and the execution trace (tool trajectory + final response output + step errors).
// When no keyword signal matches, it returns FailureUnknown rather than guessing
// response_mismatch: a silent mislabel would be more misleading than an explicit
// "uncategorized" bucket that the LLM (or a human) can later resolve.
func classifyMetric(m promptiterengine.MetricResult, trace *atrace.Trace) FailureCategory {
	if traceHasToolError(trace) {
		return FailureToolCallError
	}
	if traceHasStepError(trace) {
		return FailureRouteError
	}
	text := strings.ToLower(m.MetricName + " " + m.Reason)
	switch {
	// Tool *parameter* errors are detected before generic tool-call errors so the
	// two distinct spec buckets stay separate: wrong type / missing required param /
	// illegal value vs. the call itself failing.
	case strings.Contains(text, "argument") || strings.Contains(text, "param") ||
		strings.Contains(text, "参数") || strings.Contains(text, "参数错误") ||
		strings.Contains(text, "非法参数") || strings.Contains(text, "参数类型") ||
		strings.Contains(text, "取值非法") || strings.Contains(text, "缺少参数"):
		return FailureToolParamError
	case strings.Contains(text, "tool") || strings.Contains(text, "function") ||
		strings.Contains(text, "call") || strings.Contains(text, "工具") ||
		strings.Contains(text, "func_call"):
		return FailureToolCallError
	case strings.Contains(text, "route") || strings.Contains(text, "transfer") ||
		strings.Contains(text, "handoff") || strings.Contains(text, "转移") ||
		strings.Contains(text, "delegate") || strings.Contains(text, "subagent"):
		return FailureRouteError
	case strings.Contains(text, "format") || strings.Contains(text, "json") ||
		strings.Contains(text, "schema") || strings.Contains(text, "格式") ||
		strings.Contains(text, "valid") || strings.Contains(text, "结构") ||
		strings.Contains(text, "markdown") || strings.Contains(text, "xml") ||
		strings.Contains(text, "括号") || strings.Contains(text, "bracket"):
		return FailureFormatError
	case strings.Contains(text, "knowledge") || strings.Contains(text, "知识") ||
		strings.Contains(text, "recall") || strings.Contains(text, "事实") ||
		strings.Contains(text, "hallucin") || strings.Contains(text, "编造") ||
		strings.Contains(text, "缺失关键") || strings.Contains(text, "缺失"):
		return FailureKnowledgeGap
	case strings.Contains(text, "mismatch") || strings.Contains(text, "contradict") ||
		strings.Contains(text, "不满足") || strings.Contains(text, "不符") ||
		strings.Contains(text, "偏题") || strings.Contains(text, "rubric") ||
		strings.Contains(text, "答非所问") || strings.Contains(text, "off-topic") ||
		strings.Contains(text, "错误") || strings.Contains(text, "wrong"):
		return FailureResponseMismatch
	default:
		return FailureUnknown
	}
}

// explainCategory synthesizes a human-readable failure explanation from the
// attributed category when the evaluator did not provide a reason, so every
// failed case always has at least one interpretable reason.
func explainCategory(cat FailureCategory, m promptiterengine.MetricResult) string {
	switch cat {
	case FailureToolCallError:
		return fmt.Sprintf("metric %q scored %.2f: a tool call in the execution trace errored or used unexpected arguments", m.MetricName, m.Score)
	case FailureToolParamError:
		return fmt.Sprintf("metric %q scored %.2f: a tool was called with unexpected or illegal arguments (wrong type, missing required param, or out-of-range value)", m.MetricName, m.Score)
	case FailureRouteError:
		return fmt.Sprintf("metric %q scored %.2f: an agent transfer / routing step errored in the execution trace", m.MetricName, m.Score)
	case FailureFormatError:
		return fmt.Sprintf("metric %q scored %.2f: the response did not match the required output format or schema", m.MetricName, m.Score)
	case FailureKnowledgeGap:
		return fmt.Sprintf("metric %q scored %.2f: the response missed required knowledge or factual recall", m.MetricName, m.Score)
	case FailureResponseMismatch:
		return fmt.Sprintf("metric %q scored %.2f: the response content did not satisfy the rubric expectation", m.MetricName, m.Score)
	case FailureUnknown:
		return fmt.Sprintf("metric %q scored %.2f: the failure could not be confidently categorized by rules (no clear tool/route/format/knowledge signal); an LLM or manual review is needed", m.MetricName, m.Score)
	default:
		return fmt.Sprintf("metric %q scored %.2f below the pass threshold", m.MetricName, m.Score)
	}
}

// traceHasToolError reports whether the trace contains a tool step that errored.
func traceHasToolError(t *atrace.Trace) bool {
	if t == nil {
		return false
	}
	for _, s := range t.Steps {
		if s.NodeType == "tool" && s.Error != "" {
			return true
		}
	}
	return false
}

// traceHasStepError reports whether any step errored (e.g. an agent transfer /
// routing failure), used as a fallback when no tool error is present.
func traceHasStepError(t *atrace.Trace) bool {
	if t == nil {
		return false
	}
	for _, s := range t.Steps {
		if s.Error != "" {
			return true
		}
	}
	return false
}
