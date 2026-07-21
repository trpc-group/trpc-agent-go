//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	storedEvaluationTextMaxBytes = 16 * 1024
	storedTextTruncationMarker   = "\n...[truncated]...\n"
)

type experimentRecorder interface {
	start(Request, options) error
	recordCandidate(*candidate) error
	recordEvaluation(string, *candidate, evaluationBatch, int64) error
	recordEvent(string, map[string]any) error
	finish(*Result) error
}

type noopRecorder struct{}

func (noopRecorder) start(Request, options) error     { return nil }
func (noopRecorder) recordCandidate(*candidate) error { return nil }
func (noopRecorder) recordEvaluation(string, *candidate, evaluationBatch, int64) error {
	return nil
}
func (noopRecorder) recordEvent(string, map[string]any) error { return nil }
func (noopRecorder) finish(*Result) error                     { return nil }

type fileRecorder struct {
	dir string
	mu  sync.Mutex
	seq int
}

type experimentRecord struct {
	ID                        string           `json:"id"`
	Dataset                   Dataset          `json:"dataset"`
	Scope                     skill.SkillScope `json:"scope,omitempty"`
	ParentRevisionID          string           `json:"parent_revision_id,omitempty"`
	Submit                    bool             `json:"submit"`
	RandomSeed                int64            `json:"random_seed"`
	MaxIterations             int              `json:"max_iterations"`
	MaxMetricCalls            int              `json:"max_metric_calls"`
	ReflectionBatchSize       int              `json:"reflection_batch_size"`
	MinimumHoldoutImprovement float64          `json:"minimum_holdout_improvement"`
	TimeLimit                 time.Duration    `json:"time_limit"`
	StartedAt                 time.Time        `json:"started_at"`
}

type candidateRecord struct {
	ID        string               `json:"id"`
	ParentID  string               `json:"parent_id,omitempty"`
	Component string               `json:"component,omitempty"`
	Rationale string               `json:"rationale,omitempty"`
	Spec      *evolution.SkillSpec `json:"spec"`
}

type evaluationRecord struct {
	Phase       string       `json:"phase"`
	CandidateID string       `json:"candidate_id"`
	Seed        int64        `json:"seed"`
	Cases       []Case       `json:"cases"`
	Results     []Evaluation `json:"results"`
	Summary     Summary      `json:"summary"`
	RecordedAt  time.Time    `json:"recorded_at"`
}

type eventRecord struct {
	At   time.Time      `json:"at"`
	Kind string         `json:"kind"`
	Data map[string]any `json:"data,omitempty"`
}

func newExperimentRecorder(root, experimentID string) (experimentRecorder, error) {
	if root == "" {
		return noopRecorder{}, nil
	}
	dir := filepath.Join(root, experimentID)
	for _, child := range []string{"", "candidates", "evaluations"} {
		path := filepath.Join(dir, child)
		if err := os.MkdirAll(path, 0o700); err != nil {
			return nil, fmt.Errorf("create experiment store: %w", err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return nil, fmt.Errorf("secure experiment store: %w", err)
		}
	}
	return &fileRecorder{dir: dir}, nil
}

func (r *fileRecorder) start(req Request, opts options) error {
	record := experimentRecord{
		ID:                        filepath.Base(r.dir),
		Dataset:                   cloneDataset(req.Dataset),
		Scope:                     req.Scope,
		ParentRevisionID:          req.ParentRevisionID,
		Submit:                    req.Submit,
		RandomSeed:                opts.randomSeed,
		MaxIterations:             opts.maxIterations,
		MaxMetricCalls:            opts.maxMetricCalls,
		ReflectionBatchSize:       opts.reflectionBatchSize,
		MinimumHoldoutImprovement: opts.minimumHoldoutImprovement,
		TimeLimit:                 opts.timeLimit,
		StartedAt:                 time.Now().UTC(),
	}
	return writeJSONAtomically(filepath.Join(r.dir, "experiment.json"), record)
}

func (r *fileRecorder) recordCandidate(value *candidate) error {
	if value == nil {
		return nil
	}
	record := candidateRecord{
		ID:        value.id,
		ParentID:  value.parentID,
		Rationale: value.rationale,
		Spec:      cloneSpec(value.spec),
	}
	if value.parentID != "" {
		record.Component = value.component.String()
	}
	return writeJSONAtomically(
		filepath.Join(r.dir, "candidates", value.id+".json"), record,
	)
}

func (r *fileRecorder) recordEvaluation(
	phase string,
	value *candidate,
	batch evaluationBatch,
	seed int64,
) error {
	if value == nil {
		return nil
	}
	r.mu.Lock()
	sequence := r.seq
	r.seq++
	r.mu.Unlock()
	record := evaluationRecord{
		Phase:       phase,
		CandidateID: value.id,
		Seed:        seed,
		Cases:       cloneCases(batch.cases),
		Results:     evaluationsForStorage(batch.ordered),
		Summary:     batch.summary(),
		RecordedAt:  time.Now().UTC(),
	}
	name := fmt.Sprintf("%04d-%s-%s.json", sequence, phase, value.id)
	return writeJSONAtomically(filepath.Join(r.dir, "evaluations", name), record)
}

func (r *fileRecorder) recordEvent(kind string, data map[string]any) error {
	record := eventRecord{At: time.Now().UTC(), Kind: kind, Data: data}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal experiment event: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	file, err := os.OpenFile(
		filepath.Join(r.dir, "events.jsonl"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("open experiment event log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure experiment event log: %w", err)
	}
	if _, err := file.Write(append(payload, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write experiment event: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close experiment event log: %w", err)
	}
	return nil
}

func (r *fileRecorder) finish(result *Result) error {
	return writeJSONAtomically(filepath.Join(r.dir, "result.json"), result)
}

func writeJSONAtomically(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %q: %w", path, err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".optimization-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for %q: %w", path, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file for %q: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file for %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file for %q: %w", path, err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("chmod temp file for %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %q: %w", path, err)
	}
	return nil
}

func evaluationsForStorage(evaluations []Evaluation) []Evaluation {
	stored := make([]Evaluation, len(evaluations))
	for index, evaluation := range evaluations {
		stored[index] = cloneEvaluation(evaluation)
		stored[index].Output = truncateStoredText(evaluation.Output)
		stored[index].Feedback = truncateStoredText(evaluation.Feedback)
		stored[index].Trace = truncateStoredText(evaluation.Trace)
	}
	return stored
}

func truncateStoredText(value string) string {
	if len(value) <= storedEvaluationTextMaxBytes {
		return value
	}
	budget := storedEvaluationTextMaxBytes - len(storedTextTruncationMarker)
	headBudget := budget * 3 / 4
	tailBudget := budget - headBudget
	headEnd := headBudget
	for headEnd > 0 && !utf8.RuneStart(value[headEnd]) {
		headEnd--
	}
	tailStart := len(value) - tailBudget
	for tailStart < len(value) && !utf8.RuneStart(value[tailStart]) {
		tailStart++
	}
	return value[:headEnd] + storedTextTruncationMarker + value[tailStart:]
}

func cloneDataset(dataset Dataset) Dataset {
	dataset.Feedback = cloneCases(dataset.Feedback)
	dataset.Validation = cloneCases(dataset.Validation)
	dataset.Holdout = cloneCases(dataset.Holdout)
	return dataset
}
