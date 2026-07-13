//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
)

const maxSpecChars = 64 * 1024

type component int

const (
	componentDescription component = iota
	componentWhenToUse
	componentSteps
	componentPitfalls
	componentCount
)

func (c component) String() string {
	switch c {
	case componentDescription:
		return "description"
	case componentWhenToUse:
		return "when_to_use"
	case componentSteps:
		return "steps"
	case componentPitfalls:
		return "pitfalls"
	default:
		return "unknown"
	}
}

type candidate struct {
	id            string
	parentID      string
	spec          *evolution.SkillSpec
	component     component
	rationale     string
	nextComponent component
	validation    evaluationBatch
}

type evaluationBatch struct {
	cases   []Case
	ordered []Evaluation
	byID    map[string]Evaluation
}

func newEvaluationBatch(cases []Case, evaluations []Evaluation) (evaluationBatch, error) {
	if len(evaluations) != len(cases) {
		return evaluationBatch{}, fmt.Errorf(
			"expected %d evaluation results, got %d",
			len(cases), len(evaluations),
		)
	}
	wanted := make(map[string]struct{}, len(cases))
	for _, item := range cases {
		wanted[item.ID] = struct{}{}
	}
	byID := make(map[string]Evaluation, len(evaluations))
	for _, evaluation := range evaluations {
		if _, ok := wanted[evaluation.CaseID]; !ok {
			return evaluationBatch{}, fmt.Errorf("unexpected evaluation case %q", evaluation.CaseID)
		}
		if _, duplicate := byID[evaluation.CaseID]; duplicate {
			return evaluationBatch{}, fmt.Errorf("duplicate evaluation case %q", evaluation.CaseID)
		}
		if math.IsNaN(evaluation.Score) || math.IsInf(evaluation.Score, 0) ||
			evaluation.Score < 0 || evaluation.Score > 1 {
			return evaluationBatch{}, fmt.Errorf(
				"case %q score must be finite and within [0, 1]", evaluation.CaseID,
			)
		}
		for name, value := range evaluation.Objectives {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return evaluationBatch{}, fmt.Errorf(
					"case %q objective %q must be finite", evaluation.CaseID, name,
				)
			}
		}
		byID[evaluation.CaseID] = cloneEvaluation(evaluation)
	}
	ordered := make([]Evaluation, 0, len(cases))
	for _, item := range cases {
		evaluation, ok := byID[item.ID]
		if !ok {
			return evaluationBatch{}, fmt.Errorf("missing evaluation case %q", item.ID)
		}
		ordered = append(ordered, evaluation)
	}
	return evaluationBatch{
		cases:   cloneCases(cases),
		ordered: ordered,
		byID:    byID,
	}, nil
}

func (b evaluationBatch) sum() float64 {
	var total float64
	for _, evaluation := range b.ordered {
		total += evaluation.Score
	}
	return total
}

func (b evaluationBatch) summary() Summary {
	summary := Summary{Cases: len(b.ordered)}
	if len(b.ordered) == 0 {
		return summary
	}
	objectiveTotals := make(map[string]float64)
	objectiveCounts := make(map[string]int)
	for _, evaluation := range b.ordered {
		summary.Score += evaluation.Score
		for name, value := range evaluation.Objectives {
			objectiveTotals[name] += value
			objectiveCounts[name]++
		}
	}
	summary.Score /= float64(len(b.ordered))
	if len(objectiveTotals) > 0 {
		summary.Objectives = make(map[string]float64, len(objectiveTotals))
		for name, total := range objectiveTotals {
			summary.Objectives[name] = total / float64(objectiveCounts[name])
		}
	}
	return summary
}

func validateRequest(req Request) error {
	if req.Seed == nil {
		return errors.New("seed skill spec is required")
	}
	if err := validateSpec(req.Seed); err != nil {
		return fmt.Errorf("invalid seed skill spec: %w", err)
	}
	if strings.TrimSpace(req.Dataset.ID) == "" {
		return errors.New("dataset id is required")
	}
	if strings.TrimSpace(req.Dataset.Version) == "" {
		return errors.New("dataset version is required")
	}
	if len(req.Dataset.Feedback) == 0 {
		return errors.New("feedback split must not be empty")
	}
	if len(req.Dataset.Validation) == 0 {
		return errors.New("validation split must not be empty")
	}
	if req.Submit {
		for split, count := range map[string]int{
			"feedback":   len(req.Dataset.Feedback),
			"validation": len(req.Dataset.Validation),
			"holdout":    len(req.Dataset.Holdout),
		} {
			if count < minimumPromotionCases {
				return fmt.Errorf(
					"submission requires at least %d %s cases",
					minimumPromotionCases, split,
				)
			}
		}
	}
	seen := make(map[string]string)
	for split, cases := range map[string][]Case{
		"feedback":   req.Dataset.Feedback,
		"validation": req.Dataset.Validation,
		"holdout":    req.Dataset.Holdout,
	} {
		for _, item := range cases {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				return fmt.Errorf("%s split contains an empty case id", split)
			}
			if previous, duplicate := seen[id]; duplicate {
				return fmt.Errorf(
					"case id %q appears in both %s and %s splits", id, previous, split,
				)
			}
			seen[id] = split
		}
	}
	return nil
}

func validateSpec(spec *evolution.SkillSpec) error {
	if spec == nil {
		return errors.New("nil spec")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(spec.Description) == "" {
		return errors.New("description is required")
	}
	if strings.TrimSpace(spec.WhenToUse) == "" {
		return errors.New("when_to_use is required")
	}
	if len(spec.Steps) == 0 {
		return errors.New("at least one step is required")
	}
	if specSize(spec) > maxSpecChars {
		return fmt.Errorf("spec exceeds %d characters", maxSpecChars)
	}
	for index, step := range spec.Steps {
		if strings.TrimSpace(step) == "" {
			return fmt.Errorf("step %d is empty", index)
		}
	}
	return nil
}

func specSize(spec *evolution.SkillSpec) int {
	if spec == nil {
		return 0
	}
	size := utf8.RuneCountInString(spec.Name) +
		utf8.RuneCountInString(spec.Description) +
		utf8.RuneCountInString(spec.WhenToUse)
	for _, value := range spec.Steps {
		size += utf8.RuneCountInString(value)
	}
	for _, value := range spec.Pitfalls {
		size += utf8.RuneCountInString(value)
	}
	return size
}

func specHash(spec *evolution.SkillSpec) (string, error) {
	payload, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func cloneSpec(spec *evolution.SkillSpec) *evolution.SkillSpec {
	if spec == nil {
		return nil
	}
	cloned := *spec
	cloned.Steps = append([]string(nil), spec.Steps...)
	cloned.Pitfalls = append([]string(nil), spec.Pitfalls...)
	return &cloned
}

func cloneCases(cases []Case) []Case {
	if cases == nil {
		return nil
	}
	cloned := make([]Case, len(cases))
	for index, item := range cases {
		cloned[index] = item
		cloned[index].Metadata = cloneStringMap(item.Metadata)
	}
	return cloned
}

func cloneEvaluation(evaluation Evaluation) Evaluation {
	evaluation.Objectives = cloneFloatMap(evaluation.Objectives)
	return evaluation
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneFloatMap(values map[string]float64) map[string]float64 {
	if values == nil {
		return nil
	}
	cloned := make(map[string]float64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
