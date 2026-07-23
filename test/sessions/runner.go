//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sessions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// RunnerConfig configures a complete backend consistency run.
type RunnerConfig struct {
	CaseDir          string
	ReportPath       string
	TempDir          string
	AppName          string
	UserIDPrefix     string
	RedisURL         string
	BackendFactories []BackendFactory
	NormalizeOptions NormalizeOptions
	RunMutations     bool
}

// RunnerResult contains the report and its path.
type RunnerResult struct {
	Report     ReplayReport
	ReportPath string
}

// RunReplayConsistency runs every case on every configured backend.
func RunReplayConsistency(ctx context.Context, cfg RunnerConfig) (*RunnerResult, error) {
	started := time.Now()
	if cfg.CaseDir == "" {
		cfg.CaseDir = filepath.Join("testdata", "replay_cases")
	}
	if cfg.AppName == "" {
		cfg.AppName = "replay-consistency"
	}
	if cfg.UserIDPrefix == "" {
		cfg.UserIDPrefix = "replay-user"
	}
	if len(cfg.BackendFactories) == 0 {
		var err error
		cfg.BackendFactories, err = BackendFactoriesFromEnv()
		if err != nil {
			return nil, err
		}
	}
	if cfg.RedisURL == "" {
		cfg.RedisURL = os.Getenv("REPLAY_REDIS_URL")
	}
	tempDir := cfg.TempDir
	removeTemp := false
	if tempDir == "" {
		sqliteRoot := strings.TrimSpace(os.Getenv("REPLAY_SQLITE_DIR"))
		if sqliteRoot != "" {
			if err := os.MkdirAll(sqliteRoot, 0o755); err != nil {
				return nil, fmt.Errorf("create replay sqlite root: %w", err)
			}
		}
		var err error
		tempDir, err = os.MkdirTemp(sqliteRoot, "trpc-replay-*")
		if err != nil {
			return nil, fmt.Errorf("create replay temp dir: %w", err)
		}
		removeTemp = sqliteRoot == ""
	}
	if removeTemp {
		defer os.RemoveAll(tempDir)
	}
	cases, err := LoadReplayCases(cfg.CaseDir)
	if err != nil {
		report := BuildReport(started, nil)
		report.Error = err.Error()
		report.Status = "failed"
		writeErr := WriteReport(cfg.ReportPath, report)
		result := &RunnerResult{Report: report, ReportPath: cfg.ReportPath}
		return result, errors.Join(err, writeErr)
	}
	caseReports := make([]CaseReport, 0, len(cases))
	for _, tc := range cases {
		caseReport, err := runReplayCase(ctx, cfg, tempDir, tc)
		if err != nil {
			caseReport.Error = err.Error()
			caseReports = append(caseReports, caseReport)
			report := BuildReport(started, caseReports)
			report.Error = err.Error()
			report.Status = "failed"
			writeErr := WriteReport(cfg.ReportPath, report)
			result := &RunnerResult{Report: report, ReportPath: cfg.ReportPath}
			return result, errors.Join(err, writeErr)
		}
		caseReports = append(caseReports, caseReport)
	}
	report := BuildReport(started, caseReports)
	if err := WriteReport(cfg.ReportPath, report); err != nil {
		return nil, err
	}
	return &RunnerResult{Report: report, ReportPath: cfg.ReportPath}, nil
}

// BackendFactoriesFromEnv selects the backend matrix for lightweight or
// integration runs. REPLAY_BACKENDS is a comma-separated allowlist; skip
// variables are applied afterward and accept strconv.ParseBool values.
func BackendFactoriesFromEnv() ([]BackendFactory, error) {
	available := map[string]BackendFactory{
		"inmemory": InMemoryBackendFactory{},
		"sqlite":   SQLiteBackendFactory{},
		"redis":    RedisBackendFactory{},
	}
	names := []string{"inmemory", "sqlite", "redis"}
	if raw := strings.TrimSpace(os.Getenv("REPLAY_BACKENDS")); raw != "" {
		names = nil
		seen := make(map[string]struct{})
		for _, value := range strings.Split(raw, ",") {
			name := strings.ToLower(strings.TrimSpace(value))
			if _, ok := available[name]; !ok {
				return nil, fmt.Errorf("unknown REPLAY_BACKENDS entry %q", name)
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	skipped := make(map[string]bool)
	for _, item := range []struct {
		backend string
		envs    []string
	}{
		{backend: "inmemory", envs: []string{"REPLAY_SKIP_INMEMORY"}},
		{backend: "sqlite", envs: []string{"REPLAY_SKIP_SQL", "REPLAY_SKIP_SQLITE"}},
		{backend: "redis", envs: []string{"REPLAY_SKIP_REDIS"}},
	} {
		for _, envName := range item.envs {
			raw, ok := os.LookupEnv(envName)
			if !ok || strings.TrimSpace(raw) == "" {
				continue
			}
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", envName, err)
			}
			skipped[item.backend] = skipped[item.backend] || value
		}
	}
	factories := make([]BackendFactory, 0, len(names))
	for _, name := range names {
		if !skipped[name] {
			factories = append(factories, available[name])
		}
	}
	if len(factories) == 0 {
		return nil, errors.New("replay backend selection is empty")
	}
	return factories, nil
}

func runReplayCase(
	ctx context.Context,
	cfg RunnerConfig,
	tempDir string,
	tc ReplayCase,
) (CaseReport, error) {
	var err error
	// Fixture timestamps express ordering, not wall clock time. Rebase them
	// after service creation time because persistent summary stores reject a
	// summary whose cutoff predates the session's CreatedAt.
	tc, err = rebaseReplayCaseTimes(tc, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		return CaseReport{}, fmt.Errorf("rebase case %s timestamps: %w", tc.ID, err)
	}
	caseReport := CaseReport{
		CaseID: tc.ID, Description: tc.Description,
		Runs: []ReplayResult{}, Comparisons: []ComparisonResult{},
	}
	canonical := make([]CanonicalSnapshot, 0, len(cfg.BackendFactories))
	backendNames := make([]string, 0, len(cfg.BackendFactories))
	userID := cfg.UserIDPrefix + "-" + tc.ID
	for _, factory := range cfg.BackendFactories {
		backendDir := filepath.Join(tempDir, tc.ID, factory.Name())
		if err := os.MkdirAll(backendDir, 0o755); err != nil {
			return caseReport, fmt.Errorf("create backend temp dir: %w", err)
		}
		backend, err := factory.Create(ctx, BackendConfig{
			CaseID: tc.ID, AppName: cfg.AppName, UserID: userID,
			TempDir: backendDir, RedisURL: cfg.RedisURL,
			KeyPrefix: "replay:" + tc.ID,
		})
		if err != nil {
			return caseReport, fmt.Errorf("create %s backend: %w", factory.Name(), err)
		}
		run, runErr := Replay(ctx, backend, tc, cfg.AppName, userID)
		closeErr := backend.Close()
		if run != nil {
			caseReport.Runs = append(caseReport.Runs, *run)
		}
		if runErr != nil {
			return caseReport, runErr
		}
		if closeErr != nil {
			return caseReport, fmt.Errorf("close %s backend: %w", factory.Name(), closeErr)
		}
		normalized, err := NormalizeSnapshot(run.FinalSnapshot, cfg.NormalizeOptions)
		if err != nil {
			return caseReport, fmt.Errorf("normalize %s: %w", factory.Name(), err)
		}
		canonical = append(canonical, normalized)
		backendNames = append(backendNames, factory.Name())
	}
	if len(canonical) == 0 {
		return caseReport, fmt.Errorf("case %s has no backend results", tc.ID)
	}
	for i := 1; i < len(canonical); i++ {
		comparison := CompareSnapshots(
			canonical[0], canonical[i], backendNames[0], backendNames[i], tc.AllowedDiff,
		)
		caseReport.Comparisons = append(caseReport.Comparisons, comparison)
	}
	if cfg.RunMutations {
		mutated, err := cloneSnapshot(canonical[0].Snapshot)
		if err != nil {
			return caseReport, err
		}
		mutation, err := applyCaseMutation(&mutated, tc.ID)
		if err != nil {
			return caseReport, fmt.Errorf("mutate case %s: %w", tc.ID, err)
		}
		normalized, err := NormalizeSnapshot(mutated, cfg.NormalizeOptions)
		if err != nil {
			return caseReport, err
		}
		comparison := CompareSnapshots(
			canonical[0], normalized, backendNames[0], "mutated", nil,
		)
		caseReport.Mutations = append(caseReport.Mutations, MutationResult{
			Name: mutation.Name, Path: mutation.Path,
			Detected: !comparison.Equal, Differences: comparison.Differences,
		})
	}
	return caseReport, nil
}

func applyCaseMutation(snapshot *Snapshot, caseID string) (Mutation, error) {
	switch caseID {
	case "10_summary_missing":
		return ApplySummaryMutation(snapshot, MutationSummaryMissing)
	case "11_summary_overwrite":
		return ApplySummaryMutation(snapshot, MutationSummaryOverwrite)
	case "12_summary_wrong_session":
		return ApplySummaryMutation(snapshot, MutationSummaryWrongSession)
	case "15_event_postwrite_retry":
		return ApplyEventDuplicateMutation(snapshot)
	case "16_state_summary_failure":
		return ApplyStateDirtyMutation(snapshot)
	case "18_track_observability":
		return ApplyTrackMutation(snapshot)
	default:
		return ApplyMutation(snapshot)
	}
}

func rebaseReplayCaseTimes(tc ReplayCase, base time.Time) (ReplayCase, error) {
	var earliest time.Time
	for _, action := range tc.Actions {
		if action.Event == nil && action.Track == nil {
			continue
		}
		raw := ""
		if action.Event != nil {
			raw = action.Event.Timestamp
		} else {
			raw = action.Track.Timestamp
		}
		timestamp, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return tc, err
		}
		if earliest.IsZero() || timestamp.Before(earliest) {
			earliest = timestamp
		}
	}
	if earliest.IsZero() {
		return tc, nil
	}
	for i := range tc.Actions {
		if tc.Actions[i].Event == nil && tc.Actions[i].Track == nil {
			continue
		}
		raw := ""
		if tc.Actions[i].Event != nil {
			raw = tc.Actions[i].Event.Timestamp
		} else {
			raw = tc.Actions[i].Track.Timestamp
		}
		timestamp, err := time.Parse(
			time.RFC3339Nano,
			raw,
		)
		if err != nil {
			return tc, err
		}
		rebased := base.Add(timestamp.Sub(earliest)).Format(time.RFC3339Nano)
		if tc.Actions[i].Event != nil {
			copy := *tc.Actions[i].Event
			copy.Timestamp = rebased
			tc.Actions[i].Event = &copy
		} else {
			copy := *tc.Actions[i].Track
			copy.Timestamp = rebased
			tc.Actions[i].Track = &copy
		}
	}
	return tc, nil
}
