package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiter "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func newResult(score float64, cases []promptiterengine.CaseResult) *promptiterengine.EvaluationResult {
	return &promptiterengine.EvaluationResult{
		EvalSets: []promptiterengine.EvalSetResult{
			{EvalSetID: validationEvalSetID, OverallScore: score, Cases: cases},
		},
	}
}

func newRunResult(baselineScore, candidateScore float64) *promptiterengine.RunResult {
	return &promptiterengine.RunResult{
		BaselineValidation: newResult(baselineScore, failedCase("c1")),
		Rounds: []promptiterengine.RoundResult{
			{
				Train:      newResult(baselineScore, failedCase("t1")),
				Validation: newResult(candidateScore, failedCase("c1")),
			},
		},
	}
}

func TestDecideAcceptance_RejectsCostBudgetExceeded(t *testing.T) {
	result := newRunResult(0.80, 0.92)
	gateCfg := GateConfig{MinValidationGain: 0.01, CostBudget: 0.5, CostUsed: 0.75}
	decision := decideAcceptance(result.BaselineValidation, result.Rounds[0].Validation, gateCfg)
	if decision.Accepted {
		t.Fatalf("expected cost budget rejection, got accepted")
	}
	if decision.RejectedBy != "cost_budget_exceeded" {
		t.Fatalf("expected rejectedBy=cost_budget_exceeded, got %q", decision.RejectedBy)
	}
}

func TestDecideAcceptance_CostBudgetUnlimited(t *testing.T) {
	result := newRunResult(0.80, 0.92)
	gateCfg := GateConfig{MinValidationGain: 0.01, CostBudget: 0, CostUsed: 999}
	decision := decideAcceptance(result.BaselineValidation, result.Rounds[0].Validation, gateCfg)
	if !decision.Accepted {
		t.Fatalf("expected accept when budget unlimited, got %q", decision.RejectedBy)
	}
}

func TestEstimateCost(t *testing.T) {
	cfg := regressionConfig{CostPerEval: 0.01, CostPerWorker: 0.05}
	result := newRunResult(0.80, 0.92)
	cost := estimateCost(cfg, result)
	// baseline validation: 1 case; 1 round: train(1)+val(1)=2 eval units; workers: 1+2=3
	if cost.EvalUnits != 3 {
		t.Fatalf("EvalUnits=%d, want 3", cost.EvalUnits)
	}
	if cost.WorkerUnits != 3 {
		t.Fatalf("WorkerUnits=%d, want 3", cost.WorkerUnits)
	}
	want := 3*0.01 + 3*0.05
	if math.Abs(cost.Total-want) > 1e-9 {
		t.Fatalf("Total=%.4f, want %.4f", cost.Total, want)
	}
}

func failedCase(id string) []promptiterengine.CaseResult {
	return []promptiterengine.CaseResult{
		{
			EvalSetID:  validationEvalSetID,
			EvalCaseID: id,
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "factual_grounding", Score: 0.2, Status: status.EvalStatusFailed, Reason: "tool call wrong"},
			},
		},
	}
}

func passedCase(id string) []promptiterengine.CaseResult {
	return []promptiterengine.CaseResult{
		{
			EvalSetID:  validationEvalSetID,
			EvalCaseID: id,
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "factual_grounding", Score: 1.0, Status: status.EvalStatusPassed},
			},
		},
	}
}

func TestClassifyFailures(t *testing.T) {
	ar := classifyFailures(context.Background(), newResult(0.2, failedCase("case-1")), ruleAttributor{})
	if ar.TotalCases != 1 {
		t.Fatalf("expected 1 total case, got %d", ar.TotalCases)
	}
	if ar.FailedCases != 1 {
		t.Fatalf("expected 1 failed case, got %d", ar.FailedCases)
	}
	if len(ar.Failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(ar.Failures))
	}
	if ar.Failures[0].Category != FailureToolCallError {
		t.Fatalf("expected tool_call_error, got %s", ar.Failures[0].Category)
	}
	if ar.ByCategory[string(FailureToolCallError)] != 1 {
		t.Fatalf("expected tool_call_error count 1, got %d", ar.ByCategory[string(FailureToolCallError)])
	}
}

func TestClassifyFailures_UsesTraceForToolError(t *testing.T) {
	// A generic judge reason ("low score") would otherwise fall back to
	// response_mismatch, but the execution trace shows a tool step errored,
	// so attribution must report tool_call_error.
	cases := []promptiterengine.CaseResult{
		{
			EvalSetID:  validationEvalSetID,
			EvalCaseID: "case-1",
			Trace: &atrace.Trace{
				Steps: []atrace.Step{
					{NodeType: "llm"},
					{NodeType: "tool", Error: "invalid arguments for lookup_record"},
				},
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "final_response_avg_score", Score: 0.3, Status: status.EvalStatusFailed, Reason: "low score"},
			},
		},
	}
	ar := classifyFailures(context.Background(), newResult(0.3, cases), ruleAttributor{})
	if ar.ByCategory[string(FailureToolCallError)] != 1 {
		t.Fatalf("expected tool_call_error from trace, got %v", ar.ByCategory)
	}
}

func TestClassifyFailures_UsesTraceForRouteError(t *testing.T) {
	// No tool step, but an agent transfer step errored: trace-grounded
	// attribution must report route_error instead of the keyword fallback.
	cases := []promptiterengine.CaseResult{
		{
			EvalSetID:  validationEvalSetID,
			EvalCaseID: "case-1",
			Trace: &atrace.Trace{
				Steps: []atrace.Step{
					{NodeType: "agent", Error: "handoff to unknown agent"},
				},
			},
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "final_response_avg_score", Score: 0.3, Status: status.EvalStatusFailed, Reason: "low score"},
			},
		},
	}
	ar := classifyFailures(context.Background(), newResult(0.3, cases), ruleAttributor{})
	if ar.ByCategory[string(FailureRouteError)] != 1 {
		t.Fatalf("expected route_error from trace, got %v", ar.ByCategory)
	}
}

func TestDecideAcceptance_RejectsRegression(t *testing.T) {
	// Candidate validation score drops below baseline => must reject even if
	// the training set (not modeled here) improved.
	baseline := newResult(0.8, nil)
	candidate := newResult(0.6, nil)
	d := decideAcceptance(baseline, candidate, GateConfig{MinValidationGain: 0.01, AllowRegression: false})
	if d.Accepted {
		t.Fatalf("expected rejection on validation regression")
	}
	if d.RejectedBy != "validation_regression" {
		t.Fatalf("expected validation_regression, got %s", d.RejectedBy)
	}
}

func TestDecideAcceptance_RejectsNewHardFails(t *testing.T) {
	// Candidate improves overall score but introduces a new hard fail.
	baseline := newResult(0.8, passedCase("case-1"))
	candidate := newResult(0.85, failedCase("case-1"))
	d := decideAcceptance(baseline, candidate, GateConfig{MinValidationGain: 0.01, AllowRegression: false, MaxNewHardFails: 0})
	if d.Accepted {
		t.Fatalf("expected rejection on new hard fail")
	}
	if d.RejectedBy != "new_hard_fails" {
		t.Fatalf("expected new_hard_fails, got %s", d.RejectedBy)
	}
}

func TestDecideAcceptance_AcceptsGain(t *testing.T) {
	baseline := newResult(0.7, passedCase("case-1"))
	candidate := newResult(0.85, passedCase("case-1"))
	d := decideAcceptance(baseline, candidate, GateConfig{MinValidationGain: 0.01, AllowRegression: false, MaxNewHardFails: 0})
	if !d.Accepted {
		t.Fatalf("expected acceptance, got rejected: %s", d.Reason)
	}
}

func TestDecideAcceptance_RejectsInsufficientGain(t *testing.T) {
	// Candidate improves but by less than MinValidationGain (0.8 -> 0.805,
	// delta 0.005 < 0.01): must reject on insufficient_gain even though it
	// did not regress.
	baseline := newResult(0.80, nil)
	candidate := newResult(0.805, nil)
	d := decideAcceptance(baseline, candidate, GateConfig{MinValidationGain: 0.01, AllowRegression: false, MaxNewHardFails: 0})
	if d.Accepted {
		t.Fatalf("expected rejection for insufficient gain")
	}
	if d.RejectedBy != "insufficient_gain" {
		t.Fatalf("expected insufficient_gain, got %s", d.RejectedBy)
	}
}

func TestDecideAcceptance_RejectsKeyCaseRegression(t *testing.T) {
	baseline := newResult(0.8, passedCase("key-case"))
	candidate := newResult(0.9, failedCase("key-case"))
	d := decideAcceptance(baseline, candidate, GateConfig{
		MinValidationGain: 0.01, AllowRegression: false, MaxNewHardFails: 0, KeyCaseIDs: []string{"key-case"},
	})
	if d.Accepted {
		t.Fatalf("expected rejection on key case regression")
	}
	if d.RejectedBy != "key_case_regression" {
		t.Fatalf("expected key_case_regression, got %s", d.RejectedBy)
	}
}

func TestDecideAcceptance_RejectsOverfitting(t *testing.T) {
	// Overfitting scenario: the candidate improves the TRAIN set (0.5 -> 0.9)
	// but regresses on the VALIDATION set (0.8 -> 0.6). The gate compares only
	// baseline vs candidate validation, so the train gain must not rescue the
	// candidate: it must be rejected on validation_regression.
	result := &promptiterengine.RunResult{
		BaselineValidation: newResult(0.8, passedCase("v1")),
		Rounds: []promptiterengine.RoundResult{
			{
				Round:      1,
				Train:      newResult(0.9, passedCase("t1")), // train improved
				Validation: newResult(0.6, failedCase("v1")), // validation regressed
			},
		},
	}
	d := decideAcceptance(
		result.BaselineValidation,
		result.Rounds[0].Validation,
		GateConfig{MinValidationGain: 0.01, AllowRegression: false, MaxNewHardFails: 0},
	)
	if d.Accepted {
		t.Fatalf("overfitting candidate must be rejected")
	}
	if d.RejectedBy != "validation_regression" {
		t.Fatalf("expected validation_regression, got %s", d.RejectedBy)
	}
}

func TestClassifyFailures_AlwaysHasReason(t *testing.T) {
	// The evaluator returned no reason text: attribution must synthesize an
	// interpretable explanation so every failed case carries a reason.
	cases := []promptiterengine.CaseResult{
		{
			EvalSetID:  validationEvalSetID,
			EvalCaseID: "case-1",
			Metrics: []promptiterengine.MetricResult{
				{MetricName: "final_response_avg_score", Score: 0.3, Status: status.EvalStatusFailed, Reason: ""},
			},
		},
	}
	ar := classifyFailures(context.Background(), newResult(0.3, cases), ruleAttributor{})
	if len(ar.Failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(ar.Failures))
	}
	if ar.Failures[0].Reason == "" {
		t.Fatalf("failure reason must never be empty")
	}
}

// fakeLLMAttrModel is a test double for model.Model that returns a fixed assistant
// message (or errors), so the LLM attribution layer can be tested without a real
// API. It satisfies the model.Model interface used by llmAttributor.
type fakeLLMAttrModel struct {
	content string
	err     bool
}

func (m *fakeLLMAttrModel) GenerateContent(_ context.Context, _ *model.Request) (<-chan *model.Response, error) {
	if m.err {
		return nil, errors.New("simulated llm failure")
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		Done: true,
		Choices: []model.Choice{
			{Message: model.Message{Role: model.RoleAssistant, Content: m.content}},
		},
	}
	close(ch)
	return ch, nil
}

func (m *fakeLLMAttrModel) Info() model.Info { return model.Info{Name: "fake-llm-attr"} }

func newLLMAttr(content string) *llmAttributor {
	return &llmAttributor{model: &fakeLLMAttrModel{content: content}, timeout: 2 * time.Second}
}

func TestLLMAttributor_ParsesJSON(t *testing.T) {
	// LLM may wrap the JSON in prose and ```json fences; the parser must tolerate it.
	raw := "根据 trace 分析：\n```json\n{\"category\": \"format_error\", \"reason\": \"回复缺少闭合括号，不是合法 JSON\"}\n```"
	a := newLLMAttr(raw)
	cat, reason, err := a.Attribute(context.Background(), FailedMetricInput{
		EvalSetID: "v", EvalCaseID: "c1", MetricName: "m", Score: 0.2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cat != FailureFormatError {
		t.Fatalf("cat=%s, want format_error", cat)
	}
	if reason == "" {
		t.Fatalf("reason must not be empty")
	}
}

func TestLLMAttributor_ErrorSignalsFallback(t *testing.T) {
	// A failing LLM call must surface an error so classifyFailures can fall back
	// to the deterministic rule (gate stays reproducible).
	a := &llmAttributor{model: &fakeLLMAttrModel{err: true}, timeout: 2 * time.Second}
	if _, _, err := a.Attribute(context.Background(), FailedMetricInput{}); err == nil {
		t.Fatalf("expected error from failed LLM call")
	}
}

func TestClassifyFailures_LLMMethod(t *testing.T) {
	// With an LLM attributor, the report records method="llm" and uses the LLM's
	// natural-language reason.
	a := newLLMAttr(`{"category": "knowledge_gap", "reason": "回复缺失关键球员赛季数据"}`)
	ar := classifyFailures(context.Background(), newResult(0.3, failedCase("case-1")), a)
	if ar.Method != "llm" {
		t.Fatalf("method=%s, want llm", ar.Method)
	}
	if len(ar.Failures) != 1 {
		t.Fatalf("failures=%d, want 1", len(ar.Failures))
	}
	if ar.Failures[0].Category != FailureKnowledgeGap {
		t.Fatalf("cat=%s, want knowledge_gap", ar.Failures[0].Category)
	}
	if ar.Failures[0].Reason != "回复缺失关键球员赛季数据" {
		t.Fatalf("reason=%q", ar.Failures[0].Reason)
	}
}

func TestClassifyFailures_FallbackOnLLMError(t *testing.T) {
	// When the LLM errors, classification must still succeed via the rule fallback,
	// and every failure keeps a non-empty reason.
	a := &llmAttributor{model: &fakeLLMAttrModel{err: true}, timeout: 2 * time.Second}
	ar := classifyFailures(context.Background(), newResult(0.3, failedCase("case-1")), a)
	if len(ar.Failures) != 1 {
		t.Fatalf("failures=%d, want 1", len(ar.Failures))
	}
	if ar.Failures[0].Reason == "" {
		t.Fatalf("fallback reason must not be empty")
	}
}

func TestBuildAttributor_Selection(t *testing.T) {
	// "rule" -> ruleAttributor; "llm" without a real LLM -> error (caller falls
	// back); "auto" without a real LLM -> ruleAttributor.
	attr, _ := buildAttributor(regressionConfig{Attribution: "rule"})
	if _, ok := attr.(ruleAttributor); !ok {
		t.Fatalf("attribution=rule should yield ruleAttributor")
	}
	if _, err := buildAttributor(regressionConfig{Attribution: "llm"}); err == nil {
		t.Fatalf("attribution=llm without real LLM should error")
	}
	attr, _ = buildAttributor(regressionConfig{Attribution: "auto"})
	if _, ok := attr.(ruleAttributor); !ok {
		t.Fatalf("attribution=auto without real LLM should yield ruleAttributor")
	}
}

func TestAttributionAccuracy_LabeledSet(t *testing.T) {
	// A small labeled fixture set covering every category. The deterministic
	// classifier must reach >= 75% accuracy on it (acceptance criterion #4).
	type labeled struct {
		metric promptiterengine.MetricResult
		trace  *atrace.Trace
		want   FailureCategory
	}
	fixtures := []labeled{
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.2, Reason: "generic low score"},
			&atrace.Trace{Steps: []atrace.Step{{NodeType: "tool", Error: "bad args"}}}, FailureToolCallError},
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.2, Reason: "generic low score"},
			&atrace.Trace{Steps: []atrace.Step{{NodeType: "agent", Error: "handoff failed"}}}, FailureRouteError},
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.4, Reason: "tool call used wrong function"}, nil, FailureToolCallError},
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.4, Reason: "tool call missing required parameter id"}, nil, FailureToolParamError},
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.4, Reason: "transferred to the wrong route"}, nil, FailureRouteError},
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.4, Reason: "output is not valid json schema"}, nil, FailureFormatError},
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.4, Reason: "缺少关键知识点 recall"}, nil, FailureKnowledgeGap},
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.4, Reason: "answer contradicts the rubric expectation"}, nil, FailureResponseMismatch},
		{promptiterengine.MetricResult{MetricName: "m", Score: 0.4, Reason: "响应格式不符合要求"}, nil, FailureFormatError},
	}
	correct := 0
	for i, f := range fixtures {
		if got := classifyMetric(f.metric, f.trace); got == f.want {
			correct++
		} else {
			t.Logf("fixture %d: got %s, want %s", i, got, f.want)
		}
	}
	acc := float64(correct) / float64(len(fixtures))
	if acc < 0.75 {
		t.Fatalf("attribution accuracy %.2f below required 0.75 (%d/%d)", acc, correct, len(fixtures))
	}
}

func TestBuildReport_SurfacesRegressedCandidate(t *testing.T) {
	// When the final-round candidate regresses, the report must surface the
	// candidate's real score and negative delta (not silently fall back to the
	// baseline), together with the gate rejection reason.
	baseline := newResult(0.8, passedCase("v1"))
	candidate := newResult(0.5, failedCase("v1"))
	result := &promptiterengine.RunResult{
		BaselineValidation: baseline,
		Rounds: []promptiterengine.RoundResult{
			{
				Round:      1,
				Train:      newResult(0.9, passedCase("t1")),
				Validation: candidate,
				Acceptance: &promptiterengine.AcceptanceDecision{Accepted: false, Reason: "regressed"},
			},
		},
	}
	gate := decideAcceptance(baseline, candidate, GateConfig{MinValidationGain: 0.01, AllowRegression: false})
	report := buildReport(regressionConfig{CandidateModelName: "test-model"}, result, classifyFailures(context.Background(), baseline, ruleAttributor{}), gate, &CostReport{}, 0, "rule narrative", 0, 0)
	if report.Accepted {
		t.Fatalf("expected rejected report")
	}
	if report.GateRejectedBy != "validation_regression" {
		t.Fatalf("expected gateRejectedBy=validation_regression, got %q", report.GateRejectedBy)
	}
	if math.Abs(report.CandidateScore-0.5) > 1e-9 {
		t.Fatalf("CandidateScore=%.4f, want 0.5 (the regressed candidate)", report.CandidateScore)
	}
	if report.ScoreDelta > -0.29 {
		t.Fatalf("ScoreDelta=%.4f, want ~-0.3", report.ScoreDelta)
	}
}

func TestPerCaseDeltas(t *testing.T) {
	// Each case below exercises one of the four transition classes the report must
	// distinguish (新增通过 / 新增失败 / 分数提升 / 分数下降) plus flat.
	cases := []struct {
		id            string
		baseScore     float64
		candScore     float64
		wantBasePass  bool
		wantCandPass  bool
		wantTrend     string
		wantTransiton string
	}{
		{"new_pass", 0.5, 1.0, false, true, "up", "new_pass"},
		{"new_fail", 1.0, 0.5, true, false, "down", "new_fail"},
		{"score_up", 0.5, 0.8, false, false, "up", "score_up"},
		{"score_down", 0.8, 0.5, false, false, "down", "score_down"},
		{"flat", 0.5, 0.5, false, false, "flat", "flat"},
	}
	var baseCases, candCases []promptiterengine.CaseResult
	for _, c := range cases {
		baseCases = append(baseCases, promptiterengine.CaseResult{
			EvalSetID: validationEvalSetID, EvalCaseID: c.id,
			Metrics: []promptiterengine.MetricResult{{MetricName: "m", Score: c.baseScore}},
		})
		candCases = append(candCases, promptiterengine.CaseResult{
			EvalSetID: validationEvalSetID, EvalCaseID: c.id,
			Metrics: []promptiterengine.MetricResult{{MetricName: "m", Score: c.candScore}},
		})
	}
	baseline := newResult(0.5, baseCases)
	candidate := newResult(0.8, candCases)
	deltas := perCaseDeltas(baseline, candidate)
	if len(deltas) != len(cases) {
		t.Fatalf("expected %d deltas, got %d", len(cases), len(deltas))
	}
	byID := map[string]CaseDelta{}
	for _, d := range deltas {
		byID[d.EvalCaseID] = d
	}
	for _, c := range cases {
		d, ok := byID[c.id]
		if !ok {
			t.Fatalf("missing delta for case %s", c.id)
		}
		if d.BaselinePassed != c.wantBasePass {
			t.Errorf("%s: baselinePassed=%v want %v", c.id, d.BaselinePassed, c.wantBasePass)
		}
		if d.CandidatePassed != c.wantCandPass {
			t.Errorf("%s: candidatePassed=%v want %v", c.id, d.CandidatePassed, c.wantCandPass)
		}
		if d.Trend != c.wantTrend {
			t.Errorf("%s: trend=%s want %s", c.id, d.Trend, c.wantTrend)
		}
		if d.Transition != c.wantTransiton {
			t.Errorf("%s: transition=%s want %s", c.id, d.Transition, c.wantTransiton)
		}
	}
}

func TestBuildAndWriteReport(t *testing.T) {
	dir := t.TempDir()
	baseline := newResult(0.7, passedCase("case-1"))
	candidate := newResult(0.85, passedCase("case-1"))
	result := &promptiterengine.RunResult{
		BaselineValidation: baseline,
		Rounds: []promptiterengine.RoundResult{
			{
				Round:      1,
				Validation: candidate,
				Acceptance: &promptiterengine.AcceptanceDecision{Accepted: true, Reason: "ok"},
			},
		},
	}
	gate := decideAcceptance(baseline, candidate, GateConfig{MinValidationGain: 0.01})
	report := buildReport(regressionConfig{OutputDir: dir, CandidateModelName: "test-model"}, result, classifyFailures(context.Background(), baseline, ruleAttributor{}), gate, &CostReport{}, 1500*time.Millisecond, "rule narrative", 0, 0)
	if err := writeReport(dir, report, result); err != nil {
		t.Fatalf("write report: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "optimization_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var out RegressionReport
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("expected accepted report")
	}
	if len(out.PerCaseDelta) != 1 {
		t.Fatalf("expected 1 per-case delta, got %d", len(out.PerCaseDelta))
	}
	if out.DurationMS != 1500 {
		t.Fatalf("expected durationMs=1500, got %d", out.DurationMS)
	}
	if len(out.RoundRecords) != 1 || out.RoundRecords[0].Round != 1 {
		t.Fatalf("expected 1 round record, got %+v", out.RoundRecords)
	}
	if _, err := os.Stat(filepath.Join(dir, "optimization_report.md")); err != nil {
		t.Fatalf("md report missing: %v", err)
	}
}

func TestCandidateInstructionText_LastRound(t *testing.T) {
	// When the engine rejects every round (no accepted profile), the report must
	// still show the last round's candidate prompt — the exact prompt the gate
	// compared against — instead of an empty string.
	want := "用极简一句话概括比赛，不要展开任何细节"
	txt := want
	result := &promptiterengine.RunResult{
		Rounds: []promptiterengine.RoundResult{
			{
				Round: 1,
				OutputProfile: &promptiter.Profile{
					Overrides: []promptiter.SurfaceOverride{{Value: astructure.SurfaceValue{Text: &txt}}},
				},
			},
		},
	}
	if got := candidateInstructionText(result); got != want {
		t.Fatalf("candidate instruction = %q, want %q", got, want)
	}
	// No rounds -> empty, no panic.
	if got := candidateInstructionText(&promptiterengine.RunResult{}); got != "" {
		t.Fatalf("expected empty when no rounds, got %q", got)
	}
}

func sampleFailures() []CaseFailure {
	return []CaseFailure{
		{EvalSetID: "v", EvalCaseID: "c1", MetricName: "m", Score: 0.2, Reason: "回复不是合法 JSON", Category: FailureFormatError},
		{EvalSetID: "v", EvalCaseID: "c2", MetricName: "m", Score: 0.3, Reason: "缺少闭合括号", Category: FailureFormatError},
		{EvalSetID: "v", EvalCaseID: "c3", MetricName: "m", Score: 0.4, Reason: "tool 参数错误", Category: FailureToolCallError},
	}
}

func TestRuleInsightAggregator(t *testing.T) {
	ins := ruleInsightAggregator{}.Aggregate(context.Background(), sampleFailures())
	if ins.Method != "rule" {
		t.Fatalf("method=%s, want rule", ins.Method)
	}
	if len(ins.Patterns) != 2 {
		t.Fatalf("expected 2 patterns, got %d", len(ins.Patterns))
	}
	// Deterministic counts: format_error=2, tool_call_error=1; sorted by count desc.
	if ins.Patterns[0].Category != FailureFormatError || ins.Patterns[0].Count != 2 {
		t.Fatalf("top pattern wrong: %+v", ins.Patterns[0])
	}
	if ins.Patterns[0].Ratio < 0.66 || ins.Patterns[0].Ratio > 0.67 {
		t.Fatalf("ratio wrong: %v", ins.Patterns[0].Ratio)
	}
	if ins.Summary == "" {
		t.Fatalf("summary must not be empty")
	}
}

func TestEnhancedReporter_ParsesJSON(t *testing.T) {
	raw := "{\"summary\":\"失败集中在格式问题，建议强化 JSON 输出约束\",\"suggestedFix\":\"1. 在指令中明确要求只输出 JSON\\n2. 提供 schema 示例\",\"narrative\":\"本次优化被拒绝。失败主要为格式错误，建议在指令中强化 JSON 约束。\"}"
	ins := ruleInsightAggregator{}.Aggregate(context.Background(), sampleFailures())
	rep := &llmEnhancedReporter{model: &fakeLLMAttrModel{content: raw}, timeout: 2 * time.Second}
	out, err := rep.Report(context.Background(), EnhancedInput{
		BaselineScore: 0.8, CandidateScore: 0.5, ScoreDelta: -0.3,
		Accepted: false, GateReason: "regressed", GateRejectedBy: "validation_regression",
		Insights: ins, Failures: sampleFailures(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Summary == "" {
		t.Fatalf("summary must be parsed from LLM")
	}
	if out.SuggestedFix == "" {
		t.Fatalf("suggestedFix must be parsed from LLM")
	}
	if !strings.Contains(out.Narrative, "拒绝") {
		t.Fatalf("narrative must come from LLM: %q", out.Narrative)
	}
	// Pattern counts stay deterministic regardless of the LLM.
	if ins.Patterns[0].Category != FailureFormatError || ins.Patterns[0].Count != 2 {
		t.Fatalf("pattern counts must remain deterministic: %+v", ins.Patterns[0])
	}
}

func TestEnhancedReporter_FallbackOnError(t *testing.T) {
	rep := &llmEnhancedReporter{model: &fakeLLMAttrModel{err: true}, timeout: 2 * time.Second}
	ins := ruleInsightAggregator{}.Aggregate(context.Background(), sampleFailures())
	_, err := rep.Report(context.Background(), EnhancedInput{
		BaselineScore: 0.8, CandidateScore: 0.5, ScoreDelta: -0.3,
		Accepted: false, GateReason: "regressed", GateRejectedBy: "validation_regression",
		Insights: ins, Failures: sampleFailures(),
	})
	if err == nil {
		t.Fatalf("expected error to trigger rule fallback in caller")
	}
}

func TestBuildInsightAggregator_Selection(t *testing.T) {
	// Aggregation is always deterministic (the LLM enhancement is produced by the
	// EnhancedReporter in a single merged call), so buildInsightAggregator always
	// yields ruleInsightAggregator regardless of the attribution mode.
	for _, mode := range []string{"rule", "llm", "auto"} {
		agg, err := buildInsightAggregator(regressionConfig{Attribution: mode})
		if err != nil {
			t.Fatalf("attribution=%s should not error: %v", mode, err)
		}
		if _, ok := agg.(ruleInsightAggregator); !ok {
			t.Fatalf("attribution=%s should yield ruleInsightAggregator", mode)
		}
	}
}

func TestRuleNarrator(t *testing.T) {
	ins := ruleInsightAggregator{}.Aggregate(context.Background(), sampleFailures())
	out, err := ruleNarrator{}.Narrate(context.Background(), NarrativeInput{
		BaselineScore:  0.8,
		CandidateScore: 0.5,
		ScoreDelta:     -0.3,
		Accepted:       false,
		GateReason:     "candidate validation score regressed below baseline",
		GateRejectedBy: "validation_regression",
		Insights:       ins,
		Failures:       sampleFailures(),
	})
	if err != nil {
		t.Fatalf("rule narrator should not error: %v", err)
	}
	if !strings.Contains(out, "已拒绝") || !strings.Contains(out, "0.5000") {
		t.Fatalf("rule narrative missing key facts: %q", out)
	}
}

func TestEnhancedReporter_Selection(t *testing.T) {
	// attribution=rule -> error (reporter is LLM-only, caller uses ruleNarrator).
	if _, err := buildEnhancedReporter(regressionConfig{Attribution: "rule"}); err == nil {
		t.Fatalf("attribution=rule should not build an enhanced reporter")
	}
	// attribution=llm without a real LLM -> error (caller falls back to rule).
	if _, err := buildEnhancedReporter(regressionConfig{Attribution: "llm"}); err == nil {
		t.Fatalf("attribution=llm without real LLM should error")
	}
	// attribution=auto without a real LLM -> error (degrades to rule narrative).
	if _, err := buildEnhancedReporter(regressionConfig{Attribution: "auto"}); err == nil {
		t.Fatalf("attribution=auto without real LLM should error")
	}
}

func TestRuleNarrator_Deterministic(t *testing.T) {
	// The rule narrator must always produce a populated narrative (used as the
	// offline/ fallback path), so the report is never empty.
	out, err := ruleNarrator{}.Narrate(context.Background(), NarrativeInput{
		BaselineScore: 0.8, CandidateScore: 0.5, ScoreDelta: -0.3,
		Accepted: false, GateReason: "regressed", GateRejectedBy: "validation_regression",
		Failures: sampleFailures(),
	})
	if err != nil {
		t.Fatalf("rule narrator should not error: %v", err)
	}
	if !strings.Contains(out, "已拒绝") {
		t.Fatalf("rule narrative missing verdict: %q", out)
	}
}

func newLLMBatchAttr(content string) *llmAttributor {
	return &llmAttributor{model: &fakeLLMAttrModel{content: content}, timeout: 2 * time.Second}
}

func TestClassifyFailures_BatchAttribution(t *testing.T) {
	// When the attributor implements BatchAttributor, all failures are attributed
	// in ONE model call. The fake response is a JSON array aligned with the inputs.
	raw := `[
		{"category": "format_error", "reason": "回复缺少闭合括号，不是合法 JSON"},
		{"category": "tool_call_error", "reason": "工具参数类型错误"}
	]`
	ar := classifyFailures(context.Background(), newResult(0.3, []promptiterengine.CaseResult{
		{EvalSetID: validationEvalSetID, EvalCaseID: "c1", Metrics: []promptiterengine.MetricResult{{MetricName: "m", Score: 0.2, Status: status.EvalStatusFailed, Reason: "bad"}}},
		{EvalSetID: validationEvalSetID, EvalCaseID: "c2", Metrics: []promptiterengine.MetricResult{{MetricName: "m", Score: 0.3, Status: status.EvalStatusFailed, Reason: "bad"}}},
	}), newLLMBatchAttr(raw))
	if ar.Method != "llm" {
		t.Fatalf("method=%s, want llm", ar.Method)
	}
	if len(ar.Failures) != 2 {
		t.Fatalf("failures=%d, want 2", len(ar.Failures))
	}
	if ar.Failures[0].Category != FailureFormatError || ar.Failures[0].Reason == "" {
		t.Fatalf("batch[0] wrong: %+v", ar.Failures[0])
	}
	if ar.Failures[1].Category != FailureToolCallError || ar.Failures[1].Reason == "" {
		t.Fatalf("batch[1] wrong: %+v", ar.Failures[1])
	}
}

func TestClassifyFailures_ToolParamError(t *testing.T) {
	// "工具参数错误" must be its own category (distinct from a generic tool-call
	// failure), matching the spec's failure taxonomy.
	cases := []promptiterengine.CaseResult{
		{
			EvalSetID: validationEvalSetID, EvalCaseID: "c1",
			Metrics: []promptiterengine.MetricResult{{MetricName: "m", Score: 0.3, Status: status.EvalStatusFailed, Reason: "tool call missing required parameter"}},
		},
	}
	ar := classifyFailures(context.Background(), newResult(0.3, cases), ruleAttributor{})
	if ar.ByCategory[string(FailureToolParamError)] != 1 {
		t.Fatalf("expected tool_param_error, got %v", ar.ByCategory)
	}
	// A plain "tool call failed" (no parameter wording) stays tool_call_error.
	cases[0].Metrics[0].Reason = "tool call failed with internal error"
	ar2 := classifyFailures(context.Background(), newResult(0.3, cases), ruleAttributor{})
	if ar2.ByCategory[string(FailureToolCallError)] != 1 {
		t.Fatalf("expected tool_call_error, got %v", ar2.ByCategory)
	}
}

func TestClassifyFailures_Clusters(t *testing.T) {
	// Many individual failures must collapse into a few de-duplicated clusters.
	similar := func(id, reason string) promptiterengine.CaseResult {
		return promptiterengine.CaseResult{
			EvalSetID: validationEvalSetID, EvalCaseID: id,
			Metrics: []promptiterengine.MetricResult{{MetricName: "m", Score: 0.2, Status: status.EvalStatusFailed, Reason: reason}},
		}
	}
	ar := classifyFailures(context.Background(), newResult(0.3, []promptiterengine.CaseResult{
		similar("c1", "回复不是合法 JSON"),
		similar("c2", "回复不是合法 JSON"),
		similar("c3", "回复不是合法 JSON"),
		similar("c4", "tool 参数错误"),
	}), ruleAttributor{})
	if len(ar.Clusters) != 2 {
		t.Fatalf("expected 2 clusters, got %d: %+v", len(ar.Clusters), ar.Clusters)
	}
	// Sorted by count desc: format_error x3 first.
	if ar.Clusters[0].Category != FailureFormatError || ar.Clusters[0].Count != 3 {
		t.Fatalf("top cluster wrong: %+v", ar.Clusters[0])
	}
	if ar.Clusters[0].Reason == "" {
		t.Fatalf("cluster representative reason must not be empty")
	}
	if len(ar.Clusters[0].CaseIDs) < 1 {
		t.Fatalf("cluster should carry sample case ids")
	}
	if ar.Clusters[1].Category != FailureToolParamError || ar.Clusters[1].Count != 1 {
		t.Fatalf("second cluster wrong: %+v", ar.Clusters[1])
	}
}

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.json")
	content := `{"attribution":"auto","attributionModelName":"deepseek-v4-flash","minScoreGain":0.05,"maxRounds":6,"fake":true}`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg := regressionConfig{}
	if err := loadConfigFile(p, &cfg); err != nil {
		t.Fatalf("loadConfigFile: %v", err)
	}
	if cfg.Attribution != "auto" {
		t.Fatalf("attribution=%q, want auto", cfg.Attribution)
	}
	if cfg.AttributionModelName != "deepseek-v4-flash" {
		t.Fatalf("attributionModelName=%q", cfg.AttributionModelName)
	}
	if cfg.MinScoreGain != 0.05 {
		t.Fatalf("minScoreGain=%v, want 0.05", cfg.MinScoreGain)
	}
	if cfg.MaxRounds != 6 {
		t.Fatalf("maxRounds=%d, want 6", cfg.MaxRounds)
	}
	if !cfg.Fake {
		t.Fatalf("fake should be true")
	}
}

func TestLLMCallStats_Counted(t *testing.T) {
	// The EnhancedReporter (and llmAttributor) track how many LLM calls were made
	// and how many failed, so the loop can report observability counters even when
	// the optional LLM layer is entirely unavailable.
	stats := &llmCallStats{}
	rep := &llmEnhancedReporter{model: &fakeLLMAttrModel{content: "{\"summary\":\"s\",\"suggestedFix\":\"f\",\"narrative\":\"n\"}"}, timeout: 2 * time.Second, stats: stats}
	_, _ = rep.Report(context.Background(), EnhancedInput{
		BaselineScore: 0.8, CandidateScore: 0.5, Insights: &AttributionInsights{}, Failures: sampleFailures(),
	})
	if stats.Calls != 1 {
		t.Fatalf("expected 1 LLM call counted, got %d", stats.Calls)
	}
	if stats.Errors != 0 {
		t.Fatalf("expected 0 errors, got %d", stats.Errors)
	}

	statsFail := &llmCallStats{}
	repFail := &llmEnhancedReporter{model: &fakeLLMAttrModel{err: true}, timeout: 2 * time.Second, stats: statsFail}
	_, _ = repFail.Report(context.Background(), EnhancedInput{
		BaselineScore: 0.8, CandidateScore: 0.5, Insights: &AttributionInsights{}, Failures: sampleFailures(),
	})
	if statsFail.Calls != 1 || statsFail.Errors != 1 {
		t.Fatalf("expected 1 call and 1 error, got calls=%d errors=%d", statsFail.Calls, statsFail.Errors)
	}
}

func TestRunRegressionLoop_FakeHappy(t *testing.T) {
	// End-to-end smoke test: the entire loop runs offline with the fake model in
	// rule attribution mode, producing an accepted candidate, 0 LLM calls, and a
	// populated report (clusters + narrative + audit).
	dir := t.TempDir()
	cfg := regressionConfig{
		DataDir:              "data",
		OutputDir:            dir,
		CandidateModelName:   "deepseek-v3.2",
		CandidateInstruction: "你是一名体育评论员。",
		JudgeModelName:       "gpt-5.2",
		WorkerModelName:      "gpt-5.2",
		MaxRounds:            4,
		MinScoreGain:         0.01,
		Fake:                 true,
		FakeScenario:         scenarioHappy,
		KeyCaseIDs:           nil,
		Logger:               log.New(io.Discard, "", 0),
		TrainEvalSetID:       trainEvalSetID,
		ValidationEvalSetID:  validationEvalSetID,
		MetricFileID:         sharedMetricFileID,
		CostPerEval:          0.01,
		CostPerWorker:        0.05,
		Attribution:          "rule",
		AttributionModelName: "deepseek-v4-flash",
	}
	if err := runRegressionLoop(context.Background(), cfg); err != nil {
		t.Fatalf("runRegressionLoop: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "optimization_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep RegressionReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if !rep.Accepted {
		t.Fatalf("happy scenario should be accepted; reason=%s", rep.GateRejectedBy)
	}
	if rep.LLMCalls != 0 {
		t.Fatalf("rule mode should make 0 LLM calls, got %d", rep.LLMCalls)
	}
	if len(rep.Attribution.Clusters) == 0 {
		t.Fatalf("expected at least one failure cluster in the report")
	}
	if rep.Narrative == "" {
		t.Fatalf("expected a non-empty narrative")
	}
}

func TestCollapseRepeats(t *testing.T) {
	// A judge that concatenates one reason per rubric must collapse to a single
	// sentence in the cluster representative reason.
	got := collapseRepeats("回复基本完整但信息密度不足。回复基本完整但信息密度不足。回复基本完整但信息密度不足。")
	want := "回复基本完整但信息密度不足。"
	if got != want {
		t.Fatalf("collapseRepeats=%q, want %q", got, want)
	}
	// A single English reason without terminators must be preserved as-is.
	single := "final response mismatch: length mismatch: actual length 72 is less than min 350"
	if collapseRepeats(single) != single {
		t.Fatalf("single reason should be preserved")
	}
}

func TestRunRegressionLoop_FakeRegression(t *testing.T) {
	// Regression scenario: the optimizer drives the candidate into a degraded
	// branch, so the deterministic gate must reject it. Verifies the gate is
	// driven by validation score, not the (improved) training score.
	dir := t.TempDir()
	cfg := regressionConfig{
		DataDir:              "data",
		OutputDir:            dir,
		CandidateModelName:   "deepseek-v3.2",
		CandidateInstruction: "你是一名体育评论员。",
		JudgeModelName:       "gpt-5.2",
		WorkerModelName:      "gpt-5.2",
		MaxRounds:            4,
		MinScoreGain:         0.01,
		Fake:                 true,
		FakeScenario:         scenarioRegression,
		Logger:               log.New(io.Discard, "", 0),
		TrainEvalSetID:       trainEvalSetID,
		ValidationEvalSetID:  validationEvalSetID,
		MetricFileID:         sharedMetricFileID,
		CostPerEval:          0.01,
		CostPerWorker:        0.05,
		Attribution:          "rule",
		AttributionModelName: "deepseek-v4-flash",
	}
	if err := runRegressionLoop(context.Background(), cfg); err != nil {
		t.Fatalf("runRegressionLoop: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "optimization_report.json"))
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var rep RegressionReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if rep.Accepted {
		t.Fatalf("regression scenario must be rejected")
	}
	if rep.GateRejectedBy != "validation_regression" {
		t.Fatalf("expected validation_regression, got %s", rep.GateRejectedBy)
	}
}
