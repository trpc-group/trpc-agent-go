//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// DurableStore is the default dependency-free durable review store.
//
// The issue allows SQLite or equivalent persistence. This implementation keeps
// the same task/report schema as records in a single JSON file at the configured
// .db path, avoiding CGO in examples. The SQL shape is documented in
// schema.sql so callers can swap this Store implementation for a strict SQL
// backend without changing orchestration code.
type DurableStore struct {
	mu   sync.Mutex
	path string
	data durableData
}

type durableData struct {
	Tasks               map[string]review.ReviewTask                 `json:"review_tasks"`
	Inputs              map[string]InputRecord                       `json:"review_inputs"`
	SandboxRuns         map[string][]review.SandboxRun               `json:"sandbox_runs"`
	PermissionDecisions map[string][]review.PermissionDecisionRecord `json:"permission_decisions"`
	Findings            map[string][]review.Finding                  `json:"findings"`
	Artifacts           map[string][]review.ArtifactRecord           `json:"review_artifacts"`
	Reports             map[string]ReportRecord                      `json:"review_reports"`
}

// NewDurable opens or initializes a dependency-free durable store.
func NewDurable(ctx context.Context, path string) (*DurableStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	store := &DurableStore{path: path, data: newDurableData()}
	if err := store.initialize(ctx); err != nil {
		return nil, err
	}
	return store, nil
}

// NewSQLite preserves the example's configured entrypoint name while using the
// dependency-free durable backend in this module.
func NewSQLite(ctx context.Context, path string) (*DurableStore, error) {
	return NewDurable(ctx, path)
}

func (s *DurableStore) Close() error {
	return nil
}

func (s *DurableStore) CreateTask(ctx context.Context, task review.ReviewTask) error {
	return s.mutate(ctx, func(data *durableData) error {
		task.Error = redact.Text(task.Error).Text
		data.Tasks[task.ID] = task
		return nil
	})
}

func (s *DurableStore) FinishTask(ctx context.Context, taskID string, status string, errText string, finishedAt time.Time) error {
	return s.mutate(ctx, func(data *durableData) error {
		task, ok := data.Tasks[taskID]
		if !ok {
			return fmt.Errorf("finish task: task %q not found", taskID)
		}
		task.Status = status
		if finishedAt.IsZero() {
			finishedAt = time.Now().UTC()
		} else {
			finishedAt = finishedAt.UTC()
		}
		task.FinishedAt = &finishedAt
		task.Error = redact.Text(errText).Text
		data.Tasks[taskID] = task
		return nil
	})
}

func (s *DurableStore) RecordInput(ctx context.Context, input InputRecord) error {
	return s.mutate(ctx, func(data *durableData) error {
		input.DiffSummary = redact.Text(input.DiffSummary).Text
		input.ChangedFilesJSON = redactChangedFilesJSON(input.ChangedFilesJSON)
		input.RedactedDiff = redact.Text(input.RedactedDiff).Text
		data.Inputs[input.TaskID] = input
		return nil
	})
}

func (s *DurableStore) RecordSandboxRun(ctx context.Context, run review.SandboxRun) error {
	return s.mutate(ctx, func(data *durableData) error {
		run.Command = redact.Text(run.Command).Text
		run.StdoutRedacted = redact.Text(run.StdoutRedacted).Text
		run.StderrRedacted = redact.Text(run.StderrRedacted).Text
		data.SandboxRuns[run.TaskID] = append(data.SandboxRuns[run.TaskID], run)
		return nil
	})
}

func (s *DurableStore) RecordPermissionDecision(ctx context.Context, decision review.PermissionDecisionRecord) error {
	return s.mutate(ctx, func(data *durableData) error {
		decision.Command = redact.Text(decision.Command).Text
		decision.Reason = redact.Text(decision.Reason).Text
		data.PermissionDecisions[decision.TaskID] = append(data.PermissionDecisions[decision.TaskID], decision)
		return nil
	})
}

func (s *DurableStore) SaveFindings(ctx context.Context, taskID string, findings []review.Finding) error {
	return s.mutate(ctx, func(data *durableData) error {
		existing := make(map[string]bool)
		for _, finding := range data.Findings[taskID] {
			existing[finding.Fingerprint] = true
		}
		for index, finding := range findings {
			if finding.Fingerprint == "" {
				finding.Fingerprint = review.Fingerprint(finding)
			}
			if finding.ID == "" {
				finding.ID = fmt.Sprintf("%s-finding-%03d", taskID, index+1)
			}
			finding.Title = redact.Text(finding.Title).Text
			finding.Evidence = redact.Text(finding.Evidence).Text
			finding.Recommendation = redact.Text(finding.Recommendation).Text
			if existing[finding.Fingerprint] {
				continue
			}
			existing[finding.Fingerprint] = true
			data.Findings[taskID] = append(data.Findings[taskID], finding)
		}
		return nil
	})
}

func (s *DurableStore) SaveArtifacts(ctx context.Context, artifacts []review.ArtifactRecord) error {
	return s.mutate(ctx, func(data *durableData) error {
		for _, artifact := range artifacts {
			data.Artifacts[artifact.TaskID] = append(data.Artifacts[artifact.TaskID], artifact)
		}
		return nil
	})
}

func (s *DurableStore) SaveReport(ctx context.Context, report ReportRecord) error {
	return s.mutate(ctx, func(data *durableData) error {
		report.Conclusion = redact.Text(report.Conclusion).Text
		report.MetricsJSON = redact.Text(report.MetricsJSON).Text
		data.Reports[report.TaskID] = report
		return nil
	})
}

func (s *DurableStore) LoadTaskReport(ctx context.Context, taskID string) (TaskReport, error) {
	if err := ctx.Err(); err != nil {
		return TaskReport{}, err
	}
	if err := s.refresh(ctx); err != nil {
		return TaskReport{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.data.Tasks[taskID]
	if !ok {
		return TaskReport{}, fmt.Errorf("load task report: task %q not found", taskID)
	}
	return TaskReport{
		Task:                task,
		Input:               s.data.Inputs[taskID],
		Findings:            sortedFindings(s.data.Findings[taskID]),
		SandboxRuns:         append([]review.SandboxRun(nil), s.data.SandboxRuns[taskID]...),
		PermissionDecisions: append([]review.PermissionDecisionRecord(nil), s.data.PermissionDecisions[taskID]...),
		Artifacts:           append([]review.ArtifactRecord(nil), s.data.Artifacts[taskID]...),
		Report:              s.data.Reports[taskID],
	}, nil
}

func (s *DurableStore) initialize(ctx context.Context) error {
	lock, err := acquireStoreFileLock(ctx, s.path)
	if err != nil {
		return err
	}
	defer lock.Close()
	s.data = newDurableData()
	if err := s.load(); err != nil {
		return err
	}
	return s.flush()
}

func (s *DurableStore) mutate(ctx context.Context, mutate func(*durableData) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := acquireStoreFileLock(ctx, s.path)
	if err != nil {
		return err
	}
	defer lock.Close()
	s.data = newDurableData()
	if err := s.load(); err != nil {
		return err
	}
	if err := mutate(&s.data); err != nil {
		return err
	}
	return s.flush()
}

func (s *DurableStore) refresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := acquireStoreFileLock(ctx, s.path)
	if err != nil {
		return err
	}
	defer lock.Close()
	s.data = newDurableData()
	return s.load()
}

func (s *DurableStore) load() error {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read store: %w", err)
	}
	if len(b) == 0 {
		return nil
	}
	if err := json.Unmarshal(b, &s.data); err != nil {
		return fmt.Errorf("decode store: %w", err)
	}
	s.data.ensure()
	return nil
}

func redactChangedFilesJSON(raw string) string {
	var files []review.DiffFile
	if err := json.Unmarshal([]byte(raw), &files); err != nil {
		return redact.Text(raw).Text
	}
	encoded, err := json.Marshal(redact.DiffFiles(files))
	if err != nil {
		return redact.Text(raw).Text
	}
	return string(encoded)
}

func (s *DurableStore) flush() error {
	s.data.ensure()
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode store: %w", err)
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".review-agent-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp store: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp store: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp store: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("replace store: %w", err)
	}
	return nil
}

func newDurableData() durableData {
	data := durableData{}
	data.ensure()
	return data
}

func (d *durableData) ensure() {
	if d.Tasks == nil {
		d.Tasks = map[string]review.ReviewTask{}
	}
	if d.Inputs == nil {
		d.Inputs = map[string]InputRecord{}
	}
	if d.SandboxRuns == nil {
		d.SandboxRuns = map[string][]review.SandboxRun{}
	}
	if d.PermissionDecisions == nil {
		d.PermissionDecisions = map[string][]review.PermissionDecisionRecord{}
	}
	if d.Findings == nil {
		d.Findings = map[string][]review.Finding{}
	}
	if d.Artifacts == nil {
		d.Artifacts = map[string][]review.ArtifactRecord{}
	}
	if d.Reports == nil {
		d.Reports = map[string]ReportRecord{}
	}
}

func sortedFindings(findings []review.Finding) []review.Finding {
	out := append([]review.Finding(nil), findings...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].RuleID < out[j].RuleID
	})
	return out
}

var _ Store = (*DurableStore)(nil)
