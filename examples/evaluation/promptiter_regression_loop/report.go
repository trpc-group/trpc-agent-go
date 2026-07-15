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
	"strings"
	"sync"
	"text/template"
	"time"

	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Report is the machine-readable optimization report
// (optimization_report.json); the markdown report renders from the same data.
type Report struct {
	// RunID, Mode, Seed, StartedAt/FinishedAt identify and bound the run.
	RunID      string    `json:"runId"`
	Mode       string    `json:"mode"`
	Seed       int64     `json:"seed"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Duration   string    `json:"duration"`
	// Models describes the model or fake-engine configuration per role.
	Models map[string]string `json:"models,omitempty"`
	// Config snapshots the pipeline configuration.
	Config *Config `json:"config"`
	// Baseline summarizes the pre-optimization state.
	Baseline ReportBaseline `json:"baseline"`
	// Candidate summarizes the selected (accepted) or best rejected candidate.
	Candidate *ReportCandidate `json:"candidate,omitempty"`
	// Attribution aggregates failure categories before and after.
	Attribution ReportAttribution `json:"attribution"`
	// Delta carries per-case movements of the reported candidate.
	Delta ReportDelta `json:"delta"`
	// Gate is the full two-stage gate decision.
	Gate *GateDecision `json:"gate"`
	// Rounds is the optimization timeline.
	Rounds []ReportRound `json:"rounds"`
	// Cost summarizes runner costs and stage durations.
	Cost ReportCost `json:"cost"`
	// NextSteps carries actionable follow-ups (canary, write-back command).
	NextSteps []string `json:"nextSteps"`
}

// ReportSetSummary aggregates one eval set.
type ReportSetSummary struct {
	Score     float64        `json:"score"`
	PassCount int            `json:"passCount"`
	CaseCount int            `json:"caseCount"`
	Cases     []CaseSnapshot `json:"cases"`
}

// ReportBaseline groups the baseline train and validation summaries.
type ReportBaseline struct {
	Train      ReportSetSummary `json:"train"`
	Validation ReportSetSummary `json:"validation"`
}

// ReportCandidate summarizes the reported candidate.
type ReportCandidate struct {
	// Round is the producing engine round.
	Round int `json:"round"`
	// Accepted mirrors the gate outcome for this candidate.
	Accepted bool `json:"accepted"`
	// Prompt is the candidate instruction text.
	Prompt string `json:"prompt,omitempty"`
	// ValidationScore and TrainScore are the aggregates.
	ValidationScore float64 `json:"validationScore"`
	TrainScore      float64 `json:"trainScore,omitempty"`
	TrainScoreKnown bool    `json:"trainScoreKnown"`
}

// ReportAttribution aggregates failure root causes before and after.
type ReportAttribution struct {
	// BaselineCounts and CandidateCounts count root causes by category.
	BaselineCounts  map[FailureCategory]int `json:"baselineCounts"`
	CandidateCounts map[FailureCategory]int `json:"candidateCounts"`
	// PerCase lists every attributed failure with its causal chain: baseline
	// failures first, then candidate-side failures.
	Baseline  []CaseAttribution `json:"baseline"`
	Candidate []CaseAttribution `json:"candidate"`
}

// ReportDelta carries the per-case movements of the reported candidate.
type ReportDelta struct {
	Validation []CaseDelta  `json:"validation"`
	Train      []CaseDelta  `json:"train,omitempty"`
	Summary    DeltaSummary `json:"summary"`
}

// ReportRound is one row of the optimization timeline.
type ReportRound struct {
	Round           int     `json:"round"`
	TrainScore      float64 `json:"trainScore"`
	ValidationScore float64 `json:"validationScore"`
	EngineAccepted  bool    `json:"engineAccepted"`
	EngineReason    string  `json:"engineReason"`
	ModelCalls      int64   `json:"modelCalls"`
	WallClock       string  `json:"wallClock"`
	StopReason      string  `json:"stopReason,omitempty"`
}

// ReportCost summarizes runner costs and per-stage wall clock.
type ReportCost struct {
	Scopes         map[string]ScopeCost `json:"scopes"`
	Total          ScopeCost            `json:"total"`
	StageDurations map[string]string    `json:"stageDurations"`
}

// BuildReport assembles the report from a finished pipeline result.
func BuildReport(opts Options, result *Result) *Report {
	report := &Report{
		RunID:      result.RunID,
		Mode:       string(opts.Mode),
		Seed:       opts.Config.Seed,
		StartedAt:  result.StartedAt,
		FinishedAt: result.FinishedAt,
		Duration:   result.FinishedAt.Sub(result.StartedAt).Round(time.Millisecond).String(),
		Models:     opts.Components.ModelInfo,
		Config:     opts.Config,
		Baseline: ReportBaseline{
			Train:      summarizeSet(result.BaselineTrain, result.BaselineTrainScore),
			Validation: summarizeSet(result.BaselineValidation, result.BaselineValidationScore),
		},
		Gate: result.Gate,
		Cost: ReportCost{
			Scopes:         result.Cost.Scopes,
			Total:          result.Cost.Total,
			StageDurations: formatStageDurations(result.StageDurations),
		},
	}
	reported := reportedCandidate(result)
	if reported != nil {
		report.Candidate = &ReportCandidate{
			Round:           reported.Round,
			Accepted:        result.Gate != nil && result.Gate.Accepted,
			Prompt:          candidatePromptText(opts.Config, reported),
			ValidationScore: reported.ValidationScore,
			TrainScore:      reported.TrainScore,
			TrainScoreKnown: reported.TrainScoreKnown,
		}
		report.Delta = ReportDelta{
			Validation: reported.Deltas,
			Train:      reported.TrainDeltas,
			Summary:    Summarize(reported.Deltas),
		}
	}
	report.Attribution = buildReportAttribution(result, reported)
	report.Rounds = buildReportRounds(result)
	report.NextSteps = buildNextSteps(opts, result)
	return report
}

// reportedCandidate picks the candidate the report focuses on: the selected
// one when accepted, otherwise the best-scoring rejected candidate.
func reportedCandidate(result *Result) *Candidate {
	if len(result.Candidates) == 0 {
		return nil
	}
	if result.Gate != nil && result.Gate.Accepted {
		for i := range result.Candidates {
			if result.Candidates[i].Round == result.Gate.SelectedRound {
				return &result.Candidates[i]
			}
		}
	}
	return &result.Candidates[bestScoringIndex(result.Candidates)]
}

// candidatePromptText extracts the instruction text from a candidate profile.
func candidatePromptText(config *Config, candidate *Candidate) string {
	if candidate.Profile == nil {
		return ""
	}
	surfaceID, err := instructionTargetSurfaceID(config)
	if err != nil {
		return ""
	}
	for _, override := range candidate.Profile.Overrides {
		if override.SurfaceID == surfaceID && override.Value.Text != nil {
			return *override.Value.Text
		}
	}
	return ""
}

func summarizeSet(cases []CaseSnapshot, score float64) ReportSetSummary {
	summary := ReportSetSummary{Score: score, CaseCount: len(cases), Cases: cases}
	for _, snapshot := range cases {
		if snapshot.Pass {
			summary.PassCount++
		}
	}
	return summary
}

func buildReportAttribution(result *Result, reported *Candidate) ReportAttribution {
	attribution := ReportAttribution{
		Baseline:       result.BaselineAttributions,
		BaselineCounts: AttributionStats(result.BaselineAttributions),
	}
	candidateAttributions := make([]CaseAttribution, 0)
	if reported != nil {
		for _, delta := range reported.Deltas {
			if delta.CandidateAttribution != nil {
				candidateAttributions = append(candidateAttributions, *delta.CandidateAttribution)
			}
		}
	}
	attribution.Candidate = candidateAttributions
	attribution.CandidateCounts = AttributionStats(candidateAttributions)
	return attribution
}

func buildReportRounds(result *Result) []ReportRound {
	if result.Run == nil {
		return nil
	}
	candidateByRound := make(map[int]*Candidate, len(result.Candidates))
	for i := range result.Candidates {
		candidateByRound[result.Candidates[i].Round] = &result.Candidates[i]
	}
	rounds := make([]ReportRound, 0, len(result.Run.Rounds))
	for _, round := range result.Run.Rounds {
		row := ReportRound{Round: round.Round}
		if round.Train != nil {
			row.TrainScore = round.Train.OverallScore
		}
		if round.Validation != nil {
			row.ValidationScore = round.Validation.OverallScore
		}
		if round.Acceptance != nil {
			row.EngineAccepted = round.Acceptance.Accepted
			row.EngineReason = round.Acceptance.Reason
		}
		if round.Stop != nil && round.Stop.ShouldStop {
			row.StopReason = round.Stop.Reason
		}
		if candidate, ok := candidateByRound[round.Round]; ok {
			row.ModelCalls = candidate.ModelCalls
			row.WallClock = candidate.WallClock.Round(time.Millisecond).String()
		}
		rounds = append(rounds, row)
	}
	return rounds
}

func buildNextSteps(opts Options, result *Result) []string {
	if result.Gate == nil || result.Gate.Recommendation != RecommendationAcceptPendingCanary {
		return []string{
			"候选被拒绝，baseline prompt 保持不变。",
			"参考失败归因与逐 case delta 修订评测集或调整优化目标后重跑。",
		}
	}
	return []string{
		"候选已通过离线门禁，但离线通过 ≠ 线上有效：建议以 shadow/canary 方式小流量上线候选 prompt。",
		"用 evaluation/evalset/recorder 录制采样流量为 canary evalset，替换 validation 输入重跑本 pipeline 做二次门禁。",
		writeBackStep(opts, result),
	}
}

func writeBackStep(opts Options, result *Result) string {
	if opts.WriteBack {
		return "已按 -write-back 将候选 prompt 回写到 " + opts.Config.PromptSourcePath() + "。"
	}
	return fmt.Sprintf(
		"确认后回写源 prompt：cp %s %s（或重跑时加 -write-back）。",
		result.CandidatePromptPath,
		opts.Config.PromptSourcePath(),
	)
}

func formatStageDurations(durations map[string]time.Duration) map[string]string {
	formatted := make(map[string]string, len(durations))
	for stage, duration := range durations {
		formatted[stage] = duration.Round(time.Millisecond).String()
	}
	return formatted
}

// WriteReports persists optimization_report.json and optimization_report.md
// under the output directory. The markdown template renders the gate verdict
// unconditionally, so the result must carry a gate decision.
func WriteReports(opts Options, result *Result) (jsonPath, markdownPath string, err error) {
	if result.Gate == nil {
		return "", "", errors.New("cannot write reports: result has no gate decision")
	}
	report := BuildReport(opts, result)
	jsonPath = filepath.Join(opts.OutputDir, "optimization_report.json")
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("marshal optimization report: %w", err)
	}
	if err := os.WriteFile(jsonPath, append(content, '\n'), 0o644); err != nil {
		return "", "", fmt.Errorf("write %q: %w", jsonPath, err)
	}
	markdownPath = filepath.Join(opts.OutputDir, "optimization_report.md")
	markdown, err := RenderMarkdown(report)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(markdownPath, []byte(markdown), 0o644); err != nil {
		return "", "", fmt.Errorf("write %q: %w", markdownPath, err)
	}
	return jsonPath, markdownPath, nil
}

// markdownTemplate renders the human-readable report. The first section must
// let a non-engineer answer "is this optimization worth accepting".
const markdownTemplate = `# Prompt 优化报告

## 结论

{{ if .Gate.Accepted -}}
**接受**（{{ .Gate.Recommendation }}）：{{ .Gate.Summary }}
{{- else -}}
**拒绝**：{{ .Gate.Summary }}
{{- end }}

- 运行 ID：` + "`{{ .RunID }}`" + `（mode={{ .Mode }}，seed={{ .Seed }}，耗时 {{ .Duration }}）
- 验证集总分：baseline {{ printf "%.4f" .Baseline.Validation.Score }} → 候选 {{ if .Candidate }}{{ printf "%.4f" .Candidate.ValidationScore }}{{ else }}-{{ end }}
- 训练集总分：baseline {{ printf "%.4f" .Baseline.Train.Score }}{{ if and .Candidate .Candidate.TrainScoreKnown }} → 候选 {{ printf "%.4f" .Candidate.TrainScore }}{{ end }}

## 分数对照

| 集合 | baseline 分数 | baseline 通过 | 候选分数 |
|---|---|---|---|
| train | {{ printf "%.4f" .Baseline.Train.Score }} | {{ .Baseline.Train.PassCount }}/{{ .Baseline.Train.CaseCount }} | {{ if and .Candidate .Candidate.TrainScoreKnown }}{{ printf "%.4f" .Candidate.TrainScore }}{{ else }}未重评{{ end }} |
| validation | {{ printf "%.4f" .Baseline.Validation.Score }} | {{ .Baseline.Validation.PassCount }}/{{ .Baseline.Validation.CaseCount }} | {{ if .Candidate }}{{ printf "%.4f" .Candidate.ValidationScore }}{{ else }}-{{ end }} |

## 逐 case delta（validation）

汇总：新增通过 {{ .Delta.Summary.NewPass }}，新增失败 {{ .Delta.Summary.NewFail }}，提升 {{ .Delta.Summary.Improved }}，退化 {{ .Delta.Summary.Regressed }}，不变 {{ .Delta.Summary.Unchanged }}

| case | 变化 | baseline | 候选 | Δ分数 | 候选侧根因 |
|---|---|---|---|---|---|
{{- range .Delta.Validation }}
| {{ escapeTable .EvalCaseID }} | {{ escapeTable .Kind }} | {{ passFail .BaselinePass }} {{ printf "%.2f" .BaselineScore }} | {{ passFail .CandidatePass }} {{ printf "%.2f" .CandidateScore }} | {{ printf "%+.2f" .ScoreDelta }} | {{ escapeTable (rootCauses .CandidateAttribution) }} |
{{- end }}
{{ if .Delta.Train }}
## 逐 case delta（train）

| case | 变化 | baseline | 候选 | Δ分数 |
|---|---|---|---|---|
{{- range .Delta.Train }}
| {{ escapeTable .EvalCaseID }} | {{ escapeTable .Kind }} | {{ passFail .BaselinePass }} {{ printf "%.2f" .BaselineScore }} | {{ passFail .CandidatePass }} {{ printf "%.2f" .CandidateScore }} | {{ printf "%+.2f" .ScoreDelta }} |
{{- end }}
{{ end }}
## 失败归因

baseline 失败 {{ len .Attribution.Baseline }} 例，候选失败 {{ len .Attribution.Candidate }} 例。

| 类别 | baseline | 候选 |
|---|---|---|
{{- range $category := attributionCategories .Attribution }}
| {{ $category }} | {{ index $.Attribution.BaselineCounts $category }} | {{ index $.Attribution.CandidateCounts $category }} |
{{- end }}

因果链明细：
{{ range .Attribution.Baseline }}
- [baseline] {{ .EvalSetID }}/{{ .EvalCaseID }}：{{ chainSummary . }}
  {{- range .Chain }}
  - {{ .Category }}{{ if .DerivedFrom }}（由 {{ .DerivedFrom }} 级联）{{ end }}：{{ oneline .Evidence }}
  {{- end }}
{{- end }}
{{- range .Attribution.Candidate }}
- [候选] {{ .EvalSetID }}/{{ .EvalCaseID }}：{{ chainSummary . }}
  {{- range .Chain }}
  - {{ .Category }}{{ if .DerivedFrom }}（由 {{ .DerivedFrom }} 级联）{{ end }}：{{ oneline .Evidence }}
  {{- end }}
{{- end }}

## 候选选择过程

| 轮次 | 验证集分数 | 模型调用 | 耗时 | 过安全门 | 选中 |
|---|---|---|---|---|---|
{{- range .Gate.Selection }}
| {{ .Round }} | {{ printf "%.4f" .ValidationScore }} | {{ .ModelCalls }} | {{ duration .WallClock }} | {{ yesNo .GatePassed }} | {{ yesNo .Selected }} |
{{- end }}

## 安全门规则明细

| 规则 | 实测 | 阈值 | 结果 | 说明 |
|---|---|---|---|---|
{{- range .Gate.Rules }}
| {{ escapeTable .Name }} | {{ escapeTable .Observed }} | {{ escapeTable .Threshold }} | {{ if .Passed }}通过{{ else }}**未通过**{{ end }} | {{ escapeTable .Reason }} |
{{- end }}

## 轮次时间线

| 轮次 | train | validation | 引擎内层判定 | 模型调用 | 耗时 |
|---|---|---|---|---|---|
{{- range .Rounds }}
| {{ .Round }} | {{ printf "%.4f" .TrainScore }} | {{ printf "%.4f" .ValidationScore }} | {{ if .EngineAccepted }}接受{{ else }}拒绝{{ end }}{{ if .StopReason }}（停止：{{ .StopReason }}）{{ end }} | {{ .ModelCalls }} | {{ .WallClock }} |
{{- end }}

## 成本摘要

| scope | 推理次数 | 模型调用 | prompt tokens | completion tokens |
|---|---|---|---|---|
{{- range $scope, $cost := .Cost.Scopes }}
| {{ $scope }} | {{ $cost.RunCalls }} | {{ $cost.ModelCalls }} | {{ $cost.PromptTokens }} | {{ $cost.CompletionTokens }} |
{{- end }}
| **合计** | {{ .Cost.Total.RunCalls }} | {{ .Cost.Total.ModelCalls }} | {{ .Cost.Total.PromptTokens }} | {{ .Cost.Total.CompletionTokens }} |

分阶段耗时：{{ range $stage, $duration := .Cost.StageDurations }}{{ $stage }}={{ $duration }} {{ end }}

## 下一步建议

{{ range .NextSteps }}- {{ . }}
{{ end }}
{{- if and .Candidate .Candidate.Prompt }}
## 候选 prompt 全文

{{ codeBlock "" .Candidate.Prompt }}
{{- end }}
`

// RenderMarkdown renders the human-readable report.
func RenderMarkdown(report *Report) (string, error) {
	tmpl, err := template.New("optimization_report").Funcs(template.FuncMap{
		"passFail": func(pass bool) string {
			if pass {
				return "pass"
			}
			return "fail"
		},
		"yesNo": func(value bool) string {
			if value {
				return "是"
			}
			return "否"
		},
		"duration": func(value time.Duration) string {
			return value.Round(time.Millisecond).String()
		},
		"chainSummary": func(attribution CaseAttribution) string {
			return attribution.ChainSummary()
		},
		"oneline": func(text string) string {
			return strings.Join(strings.Fields(text), " ")
		},
		"rootCauses": func(attribution *CaseAttribution) string {
			if attribution == nil {
				return "-"
			}
			causes := make([]string, 0, len(attribution.RootCauses))
			for _, cause := range attribution.RootCauses {
				causes = append(causes, string(cause.Category))
			}
			return strings.Join(causes, ", ")
		},
		"attributionCategories": func(attribution ReportAttribution) []FailureCategory {
			present := make([]FailureCategory, 0, len(knownFailureCategories))
			for _, category := range knownFailureCategories {
				if attribution.BaselineCounts[category] > 0 || attribution.CandidateCounts[category] > 0 {
					present = append(present, category)
				}
			}
			return present
		},
		// codeBlock renders a fenced code block whose fence is longer than any
		// backtick run inside the code, preventing model output from closing the
		// fence and injecting arbitrary Markdown into the audit report.
		"codeBlock": func(language, code string) string {
			maxRun := 0
			currentRun := 0
			for _, r := range code {
				if r == '`' {
					currentRun++
					if currentRun > maxRun {
						maxRun = currentRun
					}
				} else {
					currentRun = 0
				}
			}
			fenceLen := maxRun + 1
			if fenceLen < 3 {
				fenceLen = 3
			}
			fence := strings.Repeat("`", fenceLen)
			return fence + language + "\n" + code + "\n" + fence
		},
		// escapeTable escapes pipe characters in values rendered inside Markdown
		// table cells so that external eval data (case IDs, reasons, evidence)
		// cannot distort the table structure.
		"escapeTable": func(value any) string {
			text := fmt.Sprint(value)
			text = strings.ReplaceAll(text, "|", "\\|")
			return strings.ReplaceAll(text, "\n", " ")
		},
	}).Parse(markdownTemplate)
	if err != nil {
		return "", fmt.Errorf("parse report template: %w", err)
	}
	var builder strings.Builder
	if err := tmpl.Execute(&builder, report); err != nil {
		return "", fmt.Errorf("render report template: %w", err)
	}
	return builder.String(), nil
}

// RunMeta records the reproducibility context of one pipeline execution. It
// is written to audit/run_meta.json before any optimization starts so a
// crashed run still leaves its configuration on disk.
type RunMeta struct {
	// RunID uniquely identifies this execution.
	RunID string `json:"runId"`
	// StartedAt is the wall clock start time.
	StartedAt time.Time `json:"startedAt"`
	// Mode is the model sourcing mode (fake or real).
	Mode string `json:"mode"`
	// Seed is the configured random seed recorded for reproducibility.
	Seed int64 `json:"seed"`
	// AppName is the evaluation app under optimization.
	AppName string `json:"appName"`
	// TargetSurfaceIDs lists the compiled optimization surfaces.
	TargetSurfaceIDs []string `json:"targetSurfaceIds"`
	// Models describes the model or fake-engine configuration per role.
	Models map[string]string `json:"models,omitempty"`
	// Config snapshots the full pipeline configuration.
	Config *Config `json:"config"`
}

// auditWriter persists every engine event and per-round cost deltas under
// <outputDir>/audit. Events are flushed as they arrive so an interrupted run
// keeps a complete partial trail.
type auditWriter struct {
	dir     string
	tracker *CostTracker

	mu             sync.Mutex
	lastRoundCost  CostSummary
	roundCosts     map[int]CostSummary
	roundStarted   map[int]time.Time
	roundDurations map[int]time.Duration
}

func newAuditWriter(outputDir, runID string, tracker *CostTracker) (*auditWriter, error) {
	dir := filepath.Join(outputDir, "audit", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create audit dir %q: %w", dir, err)
	}
	writer := &auditWriter{
		dir:            dir,
		tracker:        tracker,
		roundCosts:     make(map[int]CostSummary),
		roundStarted:   make(map[int]time.Time),
		roundDurations: make(map[int]time.Duration),
	}
	if tracker != nil {
		writer.lastRoundCost = tracker.Snapshot()
	}
	return writer, nil
}

// RoundCost returns the cost delta attributed to one round.
func (w *auditWriter) RoundCost(round int) CostSummary {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.roundCosts[round]
}

// RoundDuration returns the wall clock duration of one round.
func (w *auditWriter) RoundDuration(round int) time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.roundDurations[round]
}

// WriteRunMeta persists the run metadata file.
func (w *auditWriter) WriteRunMeta(meta RunMeta) error {
	return w.writeJSON(filepath.Join(w.dir, "run_meta.json"), meta)
}

// WriteFile persists one named JSON artifact in the audit root.
func (w *auditWriter) WriteFile(name string, payload any) error {
	return w.writeJSON(filepath.Join(w.dir, name), payload)
}

// Observer returns the engine observer that persists every runtime event.
func (w *auditWriter) Observer() promptiterengine.Observer {
	return func(ctx context.Context, evt *promptiterengine.Event) error {
		_ = ctx
		if evt == nil {
			return nil
		}
		path := w.eventPath(evt)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create audit event dir for %q: %w", path, err)
		}
		if err := w.writeJSON(path, evt.Payload); err != nil {
			return err
		}
		switch evt.Kind {
		case promptiterengine.EventKindRoundStarted:
			w.mu.Lock()
			w.roundStarted[evt.Round] = time.Now()
			// Reset the cost baseline so the round delta covers exactly this
			// round, excluding S1 and the engine baseline validation.
			if w.tracker != nil {
				w.lastRoundCost = w.tracker.Snapshot()
			}
			w.mu.Unlock()
		case promptiterengine.EventKindRoundCompleted:
			w.mu.Lock()
			if started, ok := w.roundStarted[evt.Round]; ok {
				w.roundDurations[evt.Round] = time.Since(started)
			}
			w.mu.Unlock()
			if w.tracker != nil {
				return w.writeRoundCost(evt.Round)
			}
		}
		return nil
	}
}

func (w *auditWriter) eventPath(evt *promptiterengine.Event) string {
	if evt.Round <= 0 {
		return filepath.Join(w.dir, string(evt.Kind)+".json")
	}
	return filepath.Join(w.dir, fmt.Sprintf("round_%d", evt.Round), string(evt.Kind)+".json")
}

func (w *auditWriter) writeRoundCost(round int) error {
	w.mu.Lock()
	current := w.tracker.Snapshot()
	delta := current.Subtract(w.lastRoundCost)
	w.lastRoundCost = current
	w.roundCosts[round] = delta
	w.mu.Unlock()
	return w.writeJSON(
		filepath.Join(w.dir, fmt.Sprintf("round_%d", round), "cost.json"),
		delta,
	)
}

func (w *auditWriter) writeJSON(path string, payload any) error {
	content, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal audit artifact %q: %w", path, err)
	}
	if err := os.WriteFile(path, append(content, '\n'), 0o644); err != nil {
		return fmt.Errorf("write audit artifact %q: %w", path, err)
	}
	return nil
}
