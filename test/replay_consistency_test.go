//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

const replaySummaryFilterKey = "root/tools/weather"

type deterministicReplaySummarizer struct{}

func (*deterministicReplaySummarizer) ShouldSummarize(*session.Session) bool { return true }

func (*deterministicReplaySummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	if sess == nil {
		return "", session.ErrNilSession
	}
	parts := make([]string, 0, len(sess.Events))
	for i := range sess.Events {
		evt := &sess.Events[i]
		message := replayEventMessage(evt)
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%s|%s", evt.Author, message.Role, message.Content, evt.Branch, evt.FilterKey))
	}
	return strings.Join(parts, "\n"), nil
}

func (*deterministicReplaySummarizer) SetPrompt(string)     {}
func (*deterministicReplaySummarizer) SetModel(model.Model) {}
func (*deterministicReplaySummarizer) Metadata() map[string]any {
	return map[string]any{"name": "deterministic-replay"}
}

func replayEventMessage(evt *event.Event) model.Message {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return model.Message{}
	}
	return evt.Response.Choices[0].Message
}

func TestReplayConsistencyLightweight(t *testing.T) {
	started := time.Now()
	report, err := replaytest.RunSuite(
		context.Background(),
		replaytest.PublicCases(),
		lightweightReplayFactories(t.TempDir()),
	)
	require.NoError(t, err)
	require.Truef(t, report.Healthy(), "unexpected replay differences: %+v", report.Cases)
	require.Zero(t, report.FailedCases)
	require.Zero(t, report.BlockingDiffs)
	require.Less(t, time.Since(started), 30*time.Second)
}

func TestReplayConsistencyDetectsInjectedFaults(t *testing.T) {
	started := time.Now()
	cases := replaytest.PublicCases()
	require.GreaterOrEqual(t, len(cases), 10)
	caseByName := make(map[string]replaytest.ReplayCase, len(cases))
	for _, replayCase := range cases {
		caseByName[replayCase.Name] = replayCase
	}

	faultsByCase := make(map[string]int, len(cases))
	requiredSummaryFaults := map[string]bool{
		"summary_loss":              false,
		"summary_overwrite_failure": false,
		"summary_wrong_session":     false,
		"summary_wrong_filter_key":  false,
	}
	detected := 0
	for _, fault := range replaytest.PublicFaults() {
		fault := fault
		replayCase, ok := caseByName[fault.Case]
		require.Truef(t, ok, "fault %q references unknown case %q", fault.Name, fault.Case)
		faultsByCase[fault.Case]++
		if _, required := requiredSummaryFaults[fault.Name]; required {
			requiredSummaryFaults[fault.Name] = true
		}

		t.Run(fault.Name, func(t *testing.T) {
			result := runLightweightReplayCase(
				t,
				replayCase,
				lightweightReplayFactories(t.TempDir()),
			)
			require.Equal(t, replaytest.StatusPassed, result.Report.Status)

			baseline := result.Traces["inmemory"]
			source := result.Traces["sqlite"]
			faulty, err := fault.Inject(source)
			require.NoError(t, err)
			faulty.Backend = "sqlite-fault:" + fault.Name

			diffs, err := replaytest.DetectInjectedFault(
				replayCase,
				"inmemory",
				faulty.Backend,
				baseline,
				faulty,
			)
			require.NoError(t, err)
			require.Truef(t, replaytest.HasBlockingDiff(diffs),
				"fault %q was not detected", fault.Name)
			require.Conditionf(t, func() bool {
				for _, diff := range diffs {
					if fault.Expect.Matches(diff) {
						return true
					}
				}
				return false
			}, "fault %q produced no precisely located diff: %+v", fault.Name, diffs)
			detected++
		})
	}

	for _, replayCase := range cases {
		require.Positivef(t, faultsByCase[replayCase.Name],
			"public case %q has no injected fault", replayCase.Name)
	}
	for name, covered := range requiredSummaryFaults {
		require.Truef(t, covered, "required summary fault %q is missing", name)
	}
	require.Equal(t, len(replaytest.PublicFaults()), detected)
	require.Less(t, time.Since(started), 30*time.Second)
}

func TestReplayConsistencyServiceFaultWrappers(t *testing.T) {
	t.Run("duplicate_event", func(t *testing.T) {
		replayCase := publicReplayCase(t, "single_turn_dialogue")
		factories := lightweightReplayFactories(t.TempDir())
		sqliteFactory := factories[1]
		factories[1].Create = func(
			ctx context.Context,
			caseName string,
		) (replaytest.Backend, func() error, error) {
			backend, cleanup, err := sqliteFactory.Create(ctx, caseName)
			if err != nil {
				return replaytest.Backend{}, cleanup, err
			}
			backend.Session = &replaytest.FaultySessionService{
				Service: backend.Session,
				Mode:    replaytest.SessionFaultDuplicateEvent,
			}
			return backend, cleanup, nil
		}

		result := runLightweightReplayCase(t, replayCase, factories)
		require.Equal(t, replaytest.StatusFailed, result.Report.Status)
		require.True(t, replaytest.HasBlockingDiff(result.Report.Diffs))
		require.Condition(t, func() bool {
			for _, diff := range result.Report.Diffs {
				if diff.Section == "events" && diff.EventIndex != nil {
					return true
				}
			}
			return false
		})
	})

	t.Run("duplicate_memory", func(t *testing.T) {
		replayCase := publicReplayCase(t, "memory_write_read")
		factories := lightweightReplayFactories(t.TempDir())
		sqliteFactory := factories[1]
		factories[1].Create = func(
			ctx context.Context,
			caseName string,
		) (replaytest.Backend, func() error, error) {
			backend, cleanup, err := sqliteFactory.Create(ctx, caseName)
			if err != nil {
				return replaytest.Backend{}, cleanup, err
			}
			backend.Memory = &replaytest.FaultyMemoryService{
				Service: backend.Memory,
				Mode:    replaytest.MemoryFaultDuplicateWrite,
			}
			return backend, cleanup, nil
		}

		result := runLightweightReplayCase(t, replayCase, factories)
		require.Equal(t, replaytest.StatusFailed, result.Report.Status)
		require.Condition(t, func() bool {
			for _, diff := range result.Report.Diffs {
				if diff.Section == "memories" && diff.MemoryID != "" {
					return true
				}
			}
			return false
		})
	})

	t.Run("dirty_state", func(t *testing.T) {
		replayCase := publicReplayCase(t, "failure_retry_ack_loss")
		replayCase.Operations = []replaytest.Operation{
			replaytest.CreateSessionOperation{
				ID: "create",
				State: session.StateMap{
					"phase": []byte(`"started"`),
				},
			},
			replaytest.AppendEventOperation{
				ID: "dirty-state-event",
				Spec: replaytest.EventSpec{
					Author:  "assistant",
					Role:    model.RoleAssistant,
					Content: "commit state",
					StateDelta: session.StateMap{
						"phase": []byte(`"committed"`),
					},
				},
			},
		}
		factories := lightweightReplayFactories(t.TempDir())
		sqliteFactory := factories[1]
		factories[1].Create = func(
			ctx context.Context,
			caseName string,
		) (replaytest.Backend, func() error, error) {
			backend, cleanup, err := sqliteFactory.Create(ctx, caseName)
			if err != nil {
				return replaytest.Backend{}, cleanup, err
			}
			backend.Session = &replaytest.FaultySessionService{
				Service: backend.Session,
				Mode:    replaytest.SessionFaultDirtyState,
			}
			return backend, cleanup, nil
		}

		result := runLightweightReplayCase(t, replayCase, factories)
		require.Equal(t, replaytest.StatusFailed, result.Report.Status)
		require.Condition(t, func() bool {
			for _, diff := range result.Report.Diffs {
				if diff.Section == "state" &&
					strings.Contains(diff.Path, "phase") {
					return true
				}
			}
			return false
		})
	})

	t.Run("lost_summary_ack", func(t *testing.T) {
		replayCase := publicReplayCase(t, "summary_filter_and_overwrite")
		factories := lightweightReplayFactories(t.TempDir())
		sqliteFactory := factories[1]
		var observedLostAck bool
		factories[1].Create = func(
			ctx context.Context,
			caseName string,
		) (replaytest.Backend, func() error, error) {
			backend, cleanup, err := sqliteFactory.Create(ctx, caseName)
			if err != nil {
				return replaytest.Backend{}, cleanup, err
			}
			service := &lostAckRecoveringSessionService{
				Service: backend.Session,
			}
			backend.Session = service
			observedLostAck = false
			originalCleanup := cleanup
			cleanup = func() error {
				observedLostAck = service.ObservedLostAck()
				if originalCleanup == nil {
					return nil
				}
				return originalCleanup()
			}
			return backend, cleanup, nil
		}

		result := runLightweightReplayCase(t, replayCase, factories)
		require.Equal(t, replaytest.StatusPassed, result.Report.Status)
		require.True(t, observedLostAck)
	})
}

type lostAckRecoveringSessionService struct {
	session.Service

	observed bool
}

func (s *lostAckRecoveringSessionService) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	faulty := &replaytest.FaultySessionService{
		Service: s.Service,
		Mode:    replaytest.SessionFaultLostSummaryAck,
	}
	err := faulty.CreateSessionSummary(ctx, sess, filterKey, force)
	if err == nil {
		return errors.New("expected lost summary acknowledgement")
	}
	s.observed = true

	stored, loadErr := s.Service.GetSession(
		ctx,
		session.Key{
			AppName:   sess.AppName,
			UserID:    sess.UserID,
			SessionID: sess.ID,
		},
	)
	if loadErr != nil {
		return loadErr
	}
	stored.SummariesMu.RLock()
	value := stored.Summaries[filterKey]
	stored.SummariesMu.RUnlock()
	if value == nil || value.Summary == "" {
		return s.Service.CreateSessionSummary(
			ctx,
			stored,
			filterKey,
			force,
		)
	}
	return nil
}

func (s *lostAckRecoveringSessionService) ObservedLostAck() bool {
	return s.observed
}

func TestReplayConsistencySQLiteStorageFault(t *testing.T) {
	replayCase := publicReplayCase(t, "multi_turn_ordering")
	root := t.TempDir()
	factories := lightweightReplayFactories(root)
	sqliteFactory := factories[1]
	factories[1].Create = func(
		ctx context.Context,
		caseName string,
	) (replaytest.Backend, func() error, error) {
		backend, cleanup, err := sqliteFactory.Create(ctx, caseName)
		if err != nil {
			return replaytest.Backend{}, cleanup, err
		}
		originalLoad := backend.Load
		backend.Load = func(
			loadCtx context.Context,
			value replaytest.Backend,
		) (replaytest.CaptureInput, error) {
			databasePath := filepath.Join(
				root,
				replaySafeName(caseName),
				"session.db",
			)
			db, openErr := sql.Open(
				"sqlite3",
				databasePath+"?_busy_timeout=5000",
			)
			if openErr != nil {
				return replaytest.CaptureInput{}, openErr
			}
			_, corruptionErr := db.ExecContext(
				loadCtx,
				`DELETE FROM session_events WHERE id = (
					SELECT id FROM session_events
					WHERE app_name = ? AND user_id = ? AND session_id = ?
					ORDER BY id LIMIT 1
				)`,
				value.SessionKey.AppName,
				value.SessionKey.UserID,
				value.SessionKey.SessionID,
			)
			closeErr := db.Close()
			if corruptionErr != nil || closeErr != nil {
				return replaytest.CaptureInput{}, errors.Join(
					corruptionErr,
					closeErr,
				)
			}
			value.Load = originalLoad
			return loadReplayBackend(loadCtx, value)
		}
		return backend, cleanup, nil
	}

	result := runLightweightReplayCase(t, replayCase, factories)
	require.Equal(t, replaytest.StatusFailed, result.Report.Status)
	require.Condition(t, func() bool {
		for _, diff := range result.Report.Diffs {
			if diff.Section == "events" && diff.EventIndex != nil {
				return true
			}
		}
		return false
	})
}

func TestReplayConsistencyWritesExampleDiffReport(t *testing.T) {
	faultNames := []string{
		"tool_args_reference_corruption",
		"memory_loss",
		"summary_wrong_session",
		"track_name_corruption",
	}
	caseReports := make([]replaytest.CaseReport, 0, len(faultNames))
	for _, faultName := range faultNames {
		fault := publicReplayFault(t, faultName)
		replayCase := publicReplayCase(t, fault.Case)
		result := runLightweightReplayCase(
			t,
			replayCase,
			lightweightReplayFactories(t.TempDir()),
		)
		faulty, err := fault.Inject(result.Traces["sqlite"])
		require.NoError(t, err)
		faulty.Backend = "sqlite-fault:" + fault.Name
		diffs, err := replaytest.DetectInjectedFault(
			replayCase,
			"inmemory",
			faulty.Backend,
			result.Traces["inmemory"],
			faulty,
		)
		require.NoError(t, err)
		caseReports = append(caseReports, replaytest.CaseReport{
			Name:     replayCase.Name,
			Status:   replaytest.StatusFailed,
			Backends: []string{"inmemory", faulty.Backend},
			Capabilities: map[string]replaytest.CapabilitySet{
				"inmemory":     replayCapabilities(),
				faulty.Backend: replayCapabilities(),
			},
			Unsupported: []replaytest.Unsupported{
				{
					Backend:     "inmemory",
					Capability:  replaytest.CapabilityEventPaging,
					AllowedDiff: true,
					Reason:      "the example matrix does not exercise backend-specific paging tokens",
				},
				{
					Backend:     faulty.Backend,
					Capability:  replaytest.CapabilityTTL,
					AllowedDiff: true,
					Reason:      "TTL is disabled for deterministic replay",
				},
			},
			Diffs:    diffs,
			Duration: 50 * time.Millisecond,
		})
	}
	report := replaytest.BuildReport(
		"inmemory",
		[]string{"inmemory", "sqlite-fault"},
		caseReports,
	)
	report.GeneratedAt = time.Date(
		2026, 7, 19, 0, 0, 0, 0, time.UTC,
	)
	path := filepath.Join(
		"testdata",
		"session_memory_summary_track_diff_report.json",
	)
	require.NoError(t, replaytest.WriteReport(path, report))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"summary_id"`)
	require.Contains(t, string(raw), `"summary_filter_key"`)
	require.Contains(t, string(raw), `"event_index"`)
	require.Contains(t, string(raw), `"memory_id"`)
	require.Contains(t, string(raw), `"track_name"`)
	require.Contains(t, string(raw), `"allowed_diff": false`)
	require.Contains(t, string(raw), `"unsupported"`)
	require.Contains(t, string(raw), `"allowed_diff": true`)
}

func publicReplayCase(t *testing.T, name string) replaytest.ReplayCase {
	t.Helper()
	for _, replayCase := range replaytest.PublicCases() {
		if replayCase.Name == name {
			return replayCase
		}
	}
	t.Fatalf("public replay case %q not found", name)
	return replaytest.ReplayCase{}
}

func publicReplayFault(t *testing.T, name string) replaytest.FaultInjection {
	t.Helper()
	for _, fault := range replaytest.PublicFaults() {
		if fault.Name == name {
			return fault
		}
	}
	t.Fatalf("public replay fault %q not found", name)
	return replaytest.FaultInjection{}
}

func loadReplayBackend(
	ctx context.Context,
	backend replaytest.Backend,
) (replaytest.CaptureInput, error) {
	sess, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return replaytest.CaptureInput{}, err
	}
	appState, err := backend.Session.ListAppStates(
		ctx,
		backend.SessionKey.AppName,
	)
	if err != nil {
		return replaytest.CaptureInput{}, err
	}
	userState, err := backend.Session.ListUserStates(
		ctx,
		session.UserKey{
			AppName: backend.SessionKey.AppName,
			UserID:  backend.SessionKey.UserID,
		},
	)
	if err != nil {
		return replaytest.CaptureInput{}, err
	}
	memories, err := backend.Memory.ReadMemories(
		ctx,
		memory.UserKey{
			AppName: backend.SessionKey.AppName,
			UserID:  backend.SessionKey.UserID,
		},
		1000,
	)
	if err != nil {
		return replaytest.CaptureInput{}, err
	}
	return replaytest.CaptureInput{
		Session:   sess,
		AppState:  appState,
		UserState: userState,
		Memories:  memories,
		Unsupported: map[replaytest.CapabilityName]string{
			replaytest.CapabilityEventPaging: "the lightweight replay matrix does not exercise backend-specific paging tokens",
			replaytest.CapabilityTTL:         "TTL is disabled to keep deterministic replay snapshots",
		},
	}, nil
}

func runLightweightReplayCase(
	t *testing.T,
	replayCase replaytest.ReplayCase,
	factories []replaytest.BackendFactory,
) replaytest.RunResult {
	t.Helper()
	backends := make([]replaytest.Backend, 0, len(factories))
	cleanups := make([]func() error, 0, len(factories))
	for _, factory := range factories {
		backend, cleanup, err := factory.Create(
			context.Background(),
			replayCase.Name,
		)
		require.NoError(t, err)
		backends = append(backends, backend)
		cleanups = append(cleanups, cleanup)
	}
	defer func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			if cleanups[i] != nil {
				require.NoError(t, cleanups[i]())
			}
		}
	}()

	result, err := replaytest.RunCase(
		context.Background(),
		replayCase,
		backends,
	)
	require.NoError(t, err)
	return result
}

func lightweightReplayFactories(root string) []replaytest.BackendFactory {
	capabilities := replayCapabilities()
	return []replaytest.BackendFactory{
		{
			Name:         "inmemory",
			Capabilities: capabilities.Clone(),
			Create: func(_ context.Context, caseName string) (replaytest.Backend, func() error, error) {
				summarizer := &deterministicReplaySummarizer{}
				sessionService := sessioninmemory.NewSessionService(
					sessioninmemory.WithSessionTTL(0),
					sessioninmemory.WithSummarizer(summarizer),
					sessioninmemory.WithSummaryFilterAllowlist(replaySummaryFilterKey),
					sessioninmemory.WithCascadeFullSessionSummary(false),
				)
				memoryService := memoryinmemory.NewMemoryService(
					memoryinmemory.WithMinSearchScore(0),
					memoryinmemory.WithMaxResults(0),
				)
				key := replaySessionKey(caseName)
				return replaytest.Backend{
						Name: "inmemory", Session: sessionService, Memory: memoryService,
						Track: sessionService, SessionKey: key, Capabilities: capabilities.Clone(),
					}, func() error {
						return errors.Join(memoryService.Close(), sessionService.Close())
					}, nil
			},
		},
		{
			Name:         "sqlite",
			Capabilities: capabilities.Clone(),
			Create: func(_ context.Context, caseName string) (replaytest.Backend, func() error, error) {
				directory := filepath.Join(root, replaySafeName(caseName))
				if err := os.MkdirAll(directory, 0o750); err != nil {
					return replaytest.Backend{}, nil, err
				}
				sessionDB, err := sql.Open("sqlite3", filepath.Join(directory, "session.db")+"?_busy_timeout=5000&_journal_mode=WAL")
				if err != nil {
					return replaytest.Backend{}, nil, err
				}
				memoryDB, err := sql.Open("sqlite3", filepath.Join(directory, "memory.db")+"?_busy_timeout=5000&_journal_mode=WAL")
				if err != nil {
					sessionDB.Close()
					return replaytest.Backend{}, nil, err
				}
				summarizer := &deterministicReplaySummarizer{}
				sessionService, err := sessionsqlite.NewService(
					sessionDB,
					sessionsqlite.WithSessionTTL(0),
					sessionsqlite.WithSummarizer(summarizer),
					sessionsqlite.WithSummaryFilterAllowlist(replaySummaryFilterKey),
					sessionsqlite.WithCascadeFullSessionSummary(false),
				)
				if err != nil {
					sessionDB.Close()
					memoryDB.Close()
					return replaytest.Backend{}, nil, err
				}
				memoryService, err := memorysqlite.NewService(
					memoryDB,
					memorysqlite.WithMinSearchScore(0),
					memorysqlite.WithMaxResults(0),
				)
				if err != nil {
					sessionService.Close()
					memoryDB.Close()
					return replaytest.Backend{}, nil, err
				}
				key := replaySessionKey(caseName)
				return replaytest.Backend{
						Name: "sqlite", Session: sessionService, Memory: memoryService,
						Track: sessionService, SessionKey: key, Capabilities: capabilities.Clone(),
					}, func() error {
						return errors.Join(memoryService.Close(), sessionService.Close())
					}, nil
			},
		},
	}
}

func replayCapabilities() replaytest.CapabilitySet {
	supported := replaytest.Capability{Supported: true}
	unsupported := func(reason string) replaytest.Capability {
		return replaytest.Capability{AllowedDiff: true, Reason: reason}
	}
	return replaytest.CapabilitySet{
		replaytest.CapabilityEvents:              supported,
		replaytest.CapabilityState:               supported,
		replaytest.CapabilityAppState:            supported,
		replaytest.CapabilityUserState:           supported,
		replaytest.CapabilityMemory:              supported,
		replaytest.CapabilityMemorySearch:        supported,
		replaytest.CapabilitySummary:             supported,
		replaytest.CapabilityTracks:              supported,
		replaytest.CapabilityStateDelete:         supported,
		replaytest.CapabilityStateClear:          supported,
		replaytest.CapabilityEventPaging:         unsupported("the lightweight replay matrix does not exercise backend-specific paging tokens"),
		replaytest.CapabilityTTL:                 unsupported("TTL is disabled to keep deterministic replay snapshots"),
		replaytest.CapabilityEventStateDeltaNull: supported,
	}
}

func replaySessionKey(caseName string) session.Key {
	name := replaySafeName(caseName)
	return session.Key{AppName: "replay-" + name, UserID: "user-" + name, SessionID: "session-" + name}
}

func replaySafeName(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-").Replace(value)
}
