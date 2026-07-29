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
	"crypto/rand"
	"encoding/hex"
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
	runID, err := newReplayRunID()
	if err != nil {
		report := BuildReport(started, nil)
		report.Error = err.Error()
		report.Status = "failed"
		writeErr := WriteReport(cfg.ReportPath, report)
		result := &RunnerResult{Report: report, ReportPath: cfg.ReportPath}
		return result, errors.Join(err, writeErr)
	}
	caseReports := make([]CaseReport, 0, len(cases))
	var caseErrors []error
	for _, tc := range cases {
		caseReport, caseErr := runReplayCase(ctx, cfg, tempDir, runID, tc)
		if caseErr != nil {
			caseReport.Error = caseErr.Error()
			caseErrors = append(caseErrors, fmt.Errorf(
				"case %s: %w", tc.ID, caseErr,
			))
		}
		caseReports = append(caseReports, caseReport)
		if isContextTermination(caseErr) {
			break
		}
	}
	runErr := errors.Join(caseErrors...)
	report := BuildReport(started, caseReports)
	if runErr != nil {
		report.Error = runErr.Error()
		report.Status = "failed"
	} else if report.Status != "passed" {
		runErr = errors.New("replay consistency report contains failures")
		report.Error = runErr.Error()
	}
	writeErr := WriteReport(cfg.ReportPath, report)
	result := &RunnerResult{Report: report, ReportPath: cfg.ReportPath}
	return result, errors.Join(runErr, writeErr)
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
	runID string,
	tc ReplayCase,
) (CaseReport, error) {
	var err error
	caseReport := CaseReport{
		CaseID: tc.ID, Description: tc.Description,
		Runs: []ReplayResult{}, Comparisons: []ComparisonResult{},
	}
	// Fixture timestamps express ordering, not wall clock time. Rebase them
	// after service creation time because persistent summary stores reject a
	// summary whose cutoff predates the session's CreatedAt.
	tc, err = rebaseReplayCaseTimes(tc, time.Now().UTC().Add(24*time.Hour))
	if err != nil {
		return caseReport, fmt.Errorf("rebase case %s timestamps: %w", tc.ID, err)
	}
	canonical := make([]CanonicalSnapshot, 0, len(cfg.BackendFactories))
	backendNames := make([]string, 0, len(cfg.BackendFactories))
	var backendErrors []error
	userID := cfg.UserIDPrefix + "-" + tc.ID
	for _, factory := range cfg.BackendFactories {
		backendName := factory.Name()
		backendDir := filepath.Join(tempDir, tc.ID, factory.Name())
		if err := os.MkdirAll(backendDir, 0o755); err != nil {
			backendErr := fmt.Errorf(
				"backend %s create temp dir: %w", backendName, err,
			)
			caseReport.Runs = append(caseReport.Runs, failedReplayResult(
				tc.ID, backendName, backendErr,
			))
			backendErrors = append(backendErrors, backendErr)
			continue
		}
		backend, err := factory.Create(ctx, BackendConfig{
			CaseID: tc.ID, AppName: cfg.AppName, UserID: userID,
			TempDir: backendDir, RedisURL: cfg.RedisURL,
			//将redis的命名空间改为 —— replay:<run-id>:<case-id>
			KeyPrefix: fmt.Sprintf("replay:%s:%s", runID, tc.ID),
		})
		if err != nil {
			backendErr := fmt.Errorf("create %s backend: %w", backendName, err)
			caseReport.Runs = append(caseReport.Runs, failedReplayResult(
				tc.ID, backendName, backendErr,
			))
			backendErrors = append(backendErrors, backendErr)
			if isContextTermination(err) {
				return caseReport, errors.Join(backendErrors...)
			}
			continue
		}
		run, runErr := Replay(ctx, backend, tc, cfg.AppName, userID)
		closeErr := backend.Close()
		if run == nil {
			run = &ReplayResult{CaseID: tc.ID, Backend: backendName}
		}
		if runErr != nil {
			if run.Error == "" {
				run.Error = runErr.Error()
			}
			backendErrors = append(backendErrors, runErr)
		}
		if closeErr != nil {
			closeBackendErr := fmt.Errorf("close %s backend: %w", backendName, closeErr)
			run.Error = joinErrorText(run.Error, closeBackendErr)
			backendErrors = append(backendErrors, closeBackendErr)
		}
		if runErr != nil {
			caseReport.Runs = append(caseReport.Runs, *run)
			if isContextTermination(runErr) {
				return caseReport, errors.Join(backendErrors...)
			}
			continue
		}
		normalized, err := NormalizeSnapshot(run.FinalSnapshot, cfg.NormalizeOptions)
		if err != nil {
			normalizeErr := fmt.Errorf("normalize %s: %w", backendName, err)
			run.Error = joinErrorText(run.Error, normalizeErr)
			backendErrors = append(backendErrors, normalizeErr)
			caseReport.Runs = append(caseReport.Runs, *run)
			continue
		}
		caseReport.Runs = append(caseReport.Runs, *run)
		canonical = append(canonical, normalized)
		backendNames = append(backendNames, backendName)
	}
	if len(canonical) == 0 {
		backendErrors = append(backendErrors,
			fmt.Errorf("case %s has no successful backend results", tc.ID))
	} else if len(cfg.BackendFactories) > 1 && len(canonical) < 2 {
		backendErrors = append(backendErrors, fmt.Errorf(
			"case %s has insufficient successful backends: got %d, need at least 2",
			tc.ID, len(canonical),
		))
	}
	for i := 1; i < len(canonical); i++ {
		comparison := CompareSnapshots(
			canonical[0], canonical[i], backendNames[0], backendNames[i], tc.AllowedDiff,
		)
		caseReport.Comparisons = append(caseReport.Comparisons, comparison)
	}
	if cfg.RunMutations && len(canonical) > 0 {
		mutated, err := cloneSnapshot(canonical[0].Snapshot)
		if err != nil {
			backendErrors = append(backendErrors,
				fmt.Errorf("clone mutation snapshot for case %s: %w", tc.ID, err))
		} else {
			mutation, mutationErr := applyCaseMutation(&mutated, tc.ID)
			if mutationErr != nil {
				backendErrors = append(backendErrors,
					fmt.Errorf("mutate case %s: %w", tc.ID, mutationErr))
			} else {
				normalized, normalizeErr := NormalizeSnapshot(
					mutated, cfg.NormalizeOptions,
				)
				if normalizeErr != nil {
					backendErrors = append(backendErrors, fmt.Errorf(
						"normalize mutation for case %s: %w", tc.ID, normalizeErr,
					))
				} else {
					comparison := CompareSnapshots(
						canonical[0], normalized, backendNames[0], "mutated", nil,
					)
					caseReport.Mutations = append(caseReport.Mutations, MutationResult{
						Name: mutation.Name, Path: mutation.Path,
						Detected: !comparison.Equal, Differences: comparison.Differences,
					})
				}
			}
		}
	}
	return caseReport, errors.Join(backendErrors...)
}

func failedReplayResult(caseID, backend string, err error) ReplayResult {
	return ReplayResult{CaseID: caseID, Backend: backend, Error: err.Error()}
}

func joinErrorText(existing string, err error) string {
	if err == nil {
		return existing
	}
	if existing == "" {
		return err.Error()
	}
	return existing + "; " + err.Error()
}

func isContextTermination(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func newReplayRunID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate replay run ID: %w", err)
	}
	return hex.EncodeToString(id[:]), nil
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
		if action.Event != nil {
			timestamp, err := time.Parse(time.RFC3339Nano, action.Event.Timestamp)
			if err != nil {
				return tc, err
			}
			if earliest.IsZero() || timestamp.Before(earliest) {
				earliest = timestamp
			}
		}
		if action.Track != nil {
			timestamp, err := time.Parse(time.RFC3339Nano, action.Track.Timestamp)
			if err != nil {
				return tc, err
			}
			if earliest.IsZero() || timestamp.Before(earliest) {
				earliest = timestamp
			}
		}
	}
	if earliest.IsZero() {
		return tc, nil
	}
	//统一计算offset
	offset := base.Sub(earliest)
	for i := range tc.Actions {
		action := tc.Actions[i]
		if action.Event != nil {
			rebased, err := rebaseReplayTimestamp(action.Event.Timestamp, offset)
			if err != nil {
				return tc, err
			}
			copy := *action.Event
			copy.Timestamp = rebased
			action.Event = &copy
		}
		if action.Track != nil {
			rebased, err := rebaseReplayTimestamp(action.Track.Timestamp, offset)
			if err != nil {
				return tc, err
			}
			copy := *action.Track
			copy.Timestamp = rebased
			action.Track = &copy
		}
		if action.Expected != nil && len(action.Expected.Tracks) > 0 {
			expected := *action.Expected
			expected.Tracks = make(
				map[string][]TrackEventExpectation,
				len(action.Expected.Tracks),
			)
			for trackName, events := range action.Expected.Tracks {
				copiedEvents := append([]TrackEventExpectation(nil), events...)
				for eventIndex := range copiedEvents {
					if copiedEvents[eventIndex].Timestamp == "" {
						continue
					}
					rebased, err := rebaseReplayTimestamp(
						copiedEvents[eventIndex].Timestamp,
						offset,
					)
					if err != nil {
						return tc, fmt.Errorf(
							"rebase expected track %q event %d timestamp: %w",
							trackName,
							eventIndex,
							err,
						)
					}
					copiedEvents[eventIndex].Timestamp = rebased
				}
				expected.Tracks[trackName] = copiedEvents
			}
			action.Expected = &expected
		}
		tc.Actions[i] = action
	}
	return tc, nil
}

func rebaseReplayTimestamp(raw string, offset time.Duration) (string, error) {
	timestamp, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return "", err
	}
	return timestamp.Add(offset).UTC().Format(time.RFC3339Nano), nil
}
