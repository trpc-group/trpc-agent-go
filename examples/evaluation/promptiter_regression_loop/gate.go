//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// Gate recommendations.
const (
	// RecommendationReject means no candidate survived the safety gate.
	RecommendationReject = "reject"
	// RecommendationAcceptPendingCanary means the candidate passed offline
	// gating and should proceed through canary validation before production.
	RecommendationAcceptPendingCanary = "accept_pending_canary"
)

// Candidate is one gate-evaluable optimization round output.
type Candidate struct {
	// Round is the engine round that produced this candidate.
	Round int `json:"round"`
	// ValidationScore is the aggregate validation score of the candidate.
	ValidationScore float64 `json:"validationScore"`
	// TrainScore is the candidate's train score when known (measured by the
	// next round's input evaluation); TrainScoreKnown gates its use.
	TrainScore      float64 `json:"trainScore,omitempty"`
	TrainScoreKnown bool    `json:"trainScoreKnown"`
	// ModelCalls and WallClock are the per-round costs of producing and
	// validating this candidate.
	ModelCalls int64         `json:"modelCalls"`
	WallClock  time.Duration `json:"wallClock"`
	// Deltas are the per-case validation deltas versus baseline.
	Deltas []CaseDelta `json:"deltas"`
	// TrainDeltas are the per-case train deltas versus baseline, available
	// only when the engine re-measured the train set with this candidate.
	TrainDeltas []CaseDelta `json:"trainDeltas,omitempty"`
	// Profile is the candidate profile; excluded from JSON (audited separately).
	Profile *promptiter.Profile `json:"-"`
}

// RuleOutcome is one safety-gate rule verdict on one candidate.
type RuleOutcome struct {
	// Name identifies the rule.
	Name string `json:"name"`
	// Passed is the verdict.
	Passed bool `json:"passed"`
	// Observed and Threshold render the measured value and the limit.
	Observed  string `json:"observed"`
	Threshold string `json:"threshold"`
	// Reason is the human-readable explanation.
	Reason string `json:"reason"`
}

// CandidateOutcome records how one candidate fared through gate and selection.
type CandidateOutcome struct {
	// Round identifies the candidate.
	Round int `json:"round"`
	// Objectives snapshot.
	ValidationScore float64       `json:"validationScore"`
	ModelCalls      int64         `json:"modelCalls"`
	WallClock       time.Duration `json:"wallClock"`
	// GatePassed reports whether all safety rules passed.
	GatePassed bool `json:"gatePassed"`
	// Selected marks the final pick.
	Selected bool `json:"selected"`
	// Rules stores the per-rule verdicts.
	Rules []RuleOutcome `json:"rules"`
}

// GateDecision is the final accept/reject outcome of S5.
type GateDecision struct {
	// Accepted reports whether any candidate passed the safety gate.
	Accepted bool `json:"accepted"`
	// SelectedRound is the chosen candidate round; zero when rejected.
	SelectedRound int `json:"selectedRound,omitempty"`
	// Rules are the rule verdicts of the selected candidate when accepted, or
	// of the best-scoring rejected candidate otherwise.
	Rules []RuleOutcome `json:"rules"`
	// Selection records every candidate's gate and selection outcome.
	Selection []CandidateOutcome `json:"selection"`
	// Recommendation is reject or accept_pending_canary.
	Recommendation string `json:"recommendation"`
	// Summary is the one-line human-readable conclusion.
	Summary string `json:"summary"`
}

// GateInput carries everything the pure gate evaluation needs.
type GateInput struct {
	// Gate is the configured safety policy.
	Gate GateConfig
	// BaselineValidationScore and BaselineTrainScore anchor the deltas.
	BaselineValidationScore float64
	BaselineTrainScore      float64
	// Candidates are the gate-evaluable round outputs.
	Candidates []Candidate
	// TotalModelCalls and TotalWallClock are run-level budget observations.
	TotalModelCalls int64
	TotalWallClock  time.Duration
}

// EvaluateGate applies every hard safety rule to every candidate and picks
// the best-scoring survivor. It
// is a pure function with no IO.
func EvaluateGate(input GateInput) (*GateDecision, error) {
	if len(input.Candidates) == 0 {
		return nil, errors.New("gate input has no candidates")
	}
	maxWallClock, err := input.Gate.MaxWallClockDuration()
	if err != nil {
		return nil, err
	}
	outcomes := make([]CandidateOutcome, 0, len(input.Candidates))
	passing := make([]int, 0, len(input.Candidates))
	for index, candidate := range input.Candidates {
		rules := evaluateSafetyRules(input, candidate, maxWallClock)
		outcome := CandidateOutcome{
			Round:           candidate.Round,
			ValidationScore: candidate.ValidationScore,
			ModelCalls:      candidate.ModelCalls,
			WallClock:       candidate.WallClock,
			GatePassed:      allRulesPassed(rules),
			Rules:           rules,
		}
		outcomes = append(outcomes, outcome)
		if outcome.GatePassed {
			passing = append(passing, index)
		}
	}
	decision := &GateDecision{Selection: outcomes}
	if len(passing) == 0 {
		decision.Accepted = false
		decision.Recommendation = RecommendationReject
		// The best-scoring rejected candidate is the most informative one to
		// explain in the decision report.
		bestIndex := bestScoringIndex(input.Candidates)
		decision.Rules = outcomes[bestIndex].Rules
		decision.Summary = rejectSummary(input, input.Candidates[bestIndex], outcomes[bestIndex])
		return decision, nil
	}
	selectedIndex := selectCandidate(input.Candidates, passing)
	decision.Accepted = true
	decision.SelectedRound = input.Candidates[selectedIndex].Round
	decision.Rules = outcomes[selectedIndex].Rules
	for i := range outcomes {
		if i == selectedIndex {
			outcomes[i].Selected = true
		}
	}
	decision.Selection = outcomes
	decision.Recommendation = RecommendationAcceptPendingCanary
	decision.Summary = fmt.Sprintf(
		"接受第 %d 轮候选：验证集 %.4f（baseline %.4f，Δ%+.4f），全部 %d 条安全规则通过",
		decision.SelectedRound,
		input.Candidates[selectedIndex].ValidationScore,
		input.BaselineValidationScore,
		input.Candidates[selectedIndex].ValidationScore-input.BaselineValidationScore,
		len(decision.Rules),
	)
	return decision, nil
}

// evaluateSafetyRules applies every configured hard rule to one candidate.
// Quality red lines never participate in trade-offs.
func evaluateSafetyRules(input GateInput, candidate Candidate, maxWallClock time.Duration) []RuleOutcome {
	gate := input.Gate
	rules := make([]RuleOutcome, 0, 7)

	scoreDelta := candidate.ValidationScore - input.BaselineValidationScore
	rules = append(rules, RuleOutcome{
		Name:      "min_validation_score_gain",
		Passed:    scoreDelta >= gate.MinValidationScoreGain,
		Observed:  fmt.Sprintf("%+.4f", scoreDelta),
		Threshold: fmt.Sprintf(">= %.4f", gate.MinValidationScoreGain),
		Reason: fmt.Sprintf("验证集总分 %.4f 对比 baseline %.4f",
			candidate.ValidationScore, input.BaselineValidationScore),
	})

	hardFails := hardFailCases(candidate.Deltas, gate.HardFailCategories)
	rules = append(rules, RuleOutcome{
		Name:      "max_new_hard_fails",
		Passed:    len(hardFails) <= gate.MaxNewHardFails,
		Observed:  fmt.Sprintf("%d", len(hardFails)),
		Threshold: fmt.Sprintf("<= %d", gate.MaxNewHardFails),
		Reason:    describeCases("新增 hard fail", hardFails),
	})

	regressed := make([]string, 0)
	for _, delta := range candidate.Deltas {
		if delta.Regressed() {
			regressed = append(regressed, delta.EvalCaseID)
		}
	}
	rules = append(rules, RuleOutcome{
		Name:      "max_regressed_cases",
		Passed:    len(regressed) <= gate.MaxRegressedCases,
		Observed:  fmt.Sprintf("%d", len(regressed)),
		Threshold: fmt.Sprintf("<= %d", gate.MaxRegressedCases),
		Reason:    describeCases("退化 case", regressed),
	})

	protectedRegressed := make([]string, 0)
	protected := make(map[string]struct{}, len(gate.ProtectedCases))
	for _, caseID := range gate.ProtectedCases {
		protected[caseID] = struct{}{}
	}
	for _, delta := range candidate.Deltas {
		if _, ok := protected[delta.EvalCaseID]; ok && delta.Regressed() {
			protectedRegressed = append(protectedRegressed, delta.EvalCaseID)
		}
	}
	rules = append(rules, RuleOutcome{
		Name:      "protected_cases",
		Passed:    len(protectedRegressed) == 0,
		Observed:  fmt.Sprintf("%d", len(protectedRegressed)),
		Threshold: "== 0",
		Reason:    describeCases("关键 case 退化", protectedRegressed),
	})

	if gate.RequireTrainNotWorse {
		outcome := RuleOutcome{
			Name:      "train_not_worse",
			Threshold: fmt.Sprintf(">= %.4f", input.BaselineTrainScore),
		}
		if !candidate.TrainScoreKnown {
			outcome.Passed = false
			outcome.Observed = "unknown"
			outcome.Reason = "候选训练集分数不可得（引擎未用该候选重评训练集），按失败处理"
		} else {
			outcome.Passed = candidate.TrainScore >= input.BaselineTrainScore-gate.Epsilon()
			outcome.Observed = fmt.Sprintf("%.4f", candidate.TrainScore)
			outcome.Reason = fmt.Sprintf("候选训练集 %.4f 对比 baseline %.4f",
				candidate.TrainScore, input.BaselineTrainScore)
		}
		rules = append(rules, outcome)
	}

	if gate.MaxModelCalls > 0 {
		rules = append(rules, RuleOutcome{
			Name:      "max_model_calls",
			Passed:    input.TotalModelCalls <= int64(gate.MaxModelCalls),
			Observed:  fmt.Sprintf("%d", input.TotalModelCalls),
			Threshold: fmt.Sprintf("<= %d", gate.MaxModelCalls),
			Reason:    "整个 pipeline 运行的模型调用预算",
		})
	}
	if maxWallClock > 0 {
		rules = append(rules, RuleOutcome{
			Name:      "max_wall_clock",
			Passed:    input.TotalWallClock <= maxWallClock,
			Observed:  input.TotalWallClock.Round(time.Millisecond).String(),
			Threshold: "<= " + maxWallClock.String(),
			Reason:    "整个 pipeline 运行的墙钟预算",
		})
	}
	return rules
}

// hardFailCases returns new-fail cases whose candidate-side root cause is in
// the configured hard-fail category set.
func hardFailCases(deltas []CaseDelta, categories []string) []string {
	hard := make(map[FailureCategory]struct{}, len(categories))
	for _, category := range categories {
		hard[FailureCategory(category)] = struct{}{}
	}
	cases := make([]string, 0)
	for _, delta := range deltas {
		if delta.Kind != DeltaNewFail {
			continue
		}
		if delta.CandidateAttribution == nil {
			// A new failure without attribution is conservatively hard.
			cases = append(cases, delta.EvalCaseID)
			continue
		}
		for _, cause := range delta.CandidateAttribution.RootCauses {
			if _, ok := hard[cause.Category]; ok {
				cases = append(cases, delta.EvalCaseID)
				break
			}
		}
	}
	return cases
}

func describeCases(label string, cases []string) string {
	if len(cases) == 0 {
		return "无" + label
	}
	return label + ": " + strings.Join(cases, ", ")
}

func allRulesPassed(rules []RuleOutcome) bool {
	for _, rule := range rules {
		if !rule.Passed {
			return false
		}
	}
	return true
}

// bestScoringIndex returns the index of the candidate with the highest
// validation score.
func bestScoringIndex(candidates []Candidate) int {
	best := 0
	for i := 1; i < len(candidates); i++ {
		if candidates[i].ValidationScore > candidates[best].ValidationScore {
			best = i
		}
	}
	return best
}

// rejectSummary explains the rejection; the overfitting pattern (train up,
// protected or per-case validation regression) is called out explicitly.
func rejectSummary(input GateInput, candidate Candidate, outcome CandidateOutcome) string {
	failed := make([]string, 0)
	for _, rule := range outcome.Rules {
		if !rule.Passed {
			failed = append(failed, rule.Name)
		}
	}
	summary := fmt.Sprintf("拒绝全部候选；最优候选（第 %d 轮）未通过规则: %s", candidate.Round, strings.Join(failed, ", "))
	newFails := make([]string, 0)
	for _, delta := range candidate.Deltas {
		if delta.Kind == DeltaNewFail {
			newFails = append(newFails, delta.EvalCaseID)
		}
	}
	trainImproved := candidate.TrainScoreKnown && candidate.TrainScore > input.BaselineTrainScore+input.Gate.Epsilon()
	if trainImproved && len(newFails) > 0 {
		summary += fmt.Sprintf(
			"。训练集 %+.4f 但验证集 case %s 由 pass 转 fail，判定为过拟合",
			candidate.TrainScore-input.BaselineTrainScore,
			strings.Join(newFails, ", "),
		)
	}
	return summary
}

// selectCandidate picks the gate-passing candidate with the highest
// validation score.
func selectCandidate(candidates []Candidate, passing []int) int {
	best := passing[0]
	for _, index := range passing[1:] {
		if candidates[index].ValidationScore > candidates[best].ValidationScore {
			best = index
		}
	}
	return best
}

// DeltaKind classifies how one case moved between baseline and candidate.
type DeltaKind string

const (
	// DeltaNewPass marks a case that flipped from fail to pass.
	DeltaNewPass DeltaKind = "new_pass"
	// DeltaNewFail marks a case that flipped from pass to fail.
	DeltaNewFail DeltaKind = "new_fail"
	// DeltaImproved marks a case whose score rose without a pass flip.
	DeltaImproved DeltaKind = "improved"
	// DeltaRegressed marks a case whose score dropped without a pass flip.
	DeltaRegressed DeltaKind = "regressed"
	// DeltaUnchanged marks a case with no meaningful movement.
	DeltaUnchanged DeltaKind = "unchanged"
)

// defaultScoreEpsilon suppresses floating point noise in score comparisons.
const defaultScoreEpsilon = 1e-6

// CaseDelta is the per-case comparison between baseline and candidate.
type CaseDelta struct {
	// EvalSetID and EvalCaseID identify the case.
	EvalSetID  string `json:"evalSetId"`
	EvalCaseID string `json:"evalCaseId"`
	// Kind classifies the movement.
	Kind DeltaKind `json:"kind"`
	// BaselinePass/Score and CandidatePass/Score are the raw endpoints.
	BaselinePass   bool    `json:"baselinePass"`
	BaselineScore  float64 `json:"baselineScore"`
	CandidatePass  bool    `json:"candidatePass"`
	CandidateScore float64 `json:"candidateScore"`
	// ScoreDelta is candidate minus baseline.
	ScoreDelta float64 `json:"scoreDelta"`
	// CandidateAttribution explains the candidate-side failure when the case
	// fails under the candidate.
	CandidateAttribution *CaseAttribution `json:"candidateAttribution,omitempty"`
}

// Regressed reports whether the case moved backwards (new fail or score drop).
func (d CaseDelta) Regressed() bool {
	return d.Kind == DeltaNewFail || d.Kind == DeltaRegressed
}

// DeltaSummary counts case movements by kind.
type DeltaSummary struct {
	NewPass   int `json:"newPass"`
	NewFail   int `json:"newFail"`
	Improved  int `json:"improved"`
	Regressed int `json:"regressed"`
	Unchanged int `json:"unchanged"`
}

// Summarize aggregates deltas by kind.
func Summarize(deltas []CaseDelta) DeltaSummary {
	summary := DeltaSummary{}
	for _, delta := range deltas {
		switch delta.Kind {
		case DeltaNewPass:
			summary.NewPass++
		case DeltaNewFail:
			summary.NewFail++
		case DeltaImproved:
			summary.Improved++
		case DeltaRegressed:
			summary.Regressed++
		case DeltaUnchanged:
			summary.Unchanged++
		}
	}
	return summary
}

// ComputeDeltas aligns baseline and candidate snapshots by case identity and
// classifies each case's movement. Pass flips take precedence over score
// comparison; score comparison uses the given epsilon (<=0 uses the default).
// Both sides must cover exactly the same cases.
func ComputeDeltas(baseline, candidate []CaseSnapshot, epsilon float64) ([]CaseDelta, error) {
	if epsilon <= 0 {
		epsilon = defaultScoreEpsilon
	}
	candidateByCase := make(map[string]CaseSnapshot, len(candidate))
	for _, snapshot := range candidate {
		candidateByCase[snapshotKey(snapshot)] = snapshot
	}
	if len(candidateByCase) != len(candidate) {
		return nil, fmt.Errorf("candidate snapshots contain duplicate cases")
	}
	deltas := make([]CaseDelta, 0, len(baseline))
	seen := make(map[string]struct{}, len(baseline))
	for _, baseSnapshot := range baseline {
		key := snapshotKey(baseSnapshot)
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("baseline snapshots contain duplicate case %s", key)
		}
		seen[key] = struct{}{}
		candSnapshot, ok := candidateByCase[key]
		if !ok {
			return nil, fmt.Errorf("candidate result is missing case %s", key)
		}
		delete(candidateByCase, key)
		deltas = append(deltas, classifyDelta(baseSnapshot, candSnapshot, epsilon))
	}
	if len(candidateByCase) > 0 {
		extra := make([]string, 0, len(candidateByCase))
		for key := range candidateByCase {
			extra = append(extra, key)
		}
		sort.Strings(extra)
		return nil, fmt.Errorf("candidate result contains unknown case(s): %v", extra)
	}
	return deltas, nil
}

func snapshotKey(snapshot CaseSnapshot) string {
	return snapshot.EvalSetID + "/" + snapshot.EvalCaseID
}

func classifyDelta(baseline, candidate CaseSnapshot, epsilon float64) CaseDelta {
	delta := CaseDelta{
		EvalSetID:      baseline.EvalSetID,
		EvalCaseID:     baseline.EvalCaseID,
		BaselinePass:   baseline.Pass,
		BaselineScore:  baseline.Score,
		CandidatePass:  candidate.Pass,
		CandidateScore: candidate.Score,
		ScoreDelta:     candidate.Score - baseline.Score,
	}
	switch {
	case !baseline.Pass && candidate.Pass:
		delta.Kind = DeltaNewPass
	case baseline.Pass && !candidate.Pass:
		delta.Kind = DeltaNewFail
	case math.Abs(delta.ScoreDelta) <= epsilon:
		delta.Kind = DeltaUnchanged
	case delta.ScoreDelta > 0:
		delta.Kind = DeltaImproved
	default:
		delta.Kind = DeltaRegressed
	}
	return delta
}
