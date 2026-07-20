//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// TestPublicReplayCasesInRootModule keeps the coverage for the replay
// operation program in the same module as session/replaytest. The end-to-end
// matrix under test/ is intentionally a separate module and therefore cannot
// contribute to this package's coverage profile.
func TestPublicReplayCasesInRootModule(t *testing.T) {
	cases := PublicCases()
	require.Len(t, cases, 12)

	for _, replayCase := range cases {
		replayCase := replayCase
		t.Run(replayCase.Name, func(t *testing.T) {
			result, err := RunCase(
				context.Background(),
				replayCase,
				rootReplayBackends(t, replayCase.Name),
			)
			require.NoError(t, err)
			require.Equal(t, StatusPassed, result.Report.Status)
			require.Len(t, result.Traces, 2)
			require.Empty(t, result.Report.Diffs)
		})
	}
}

// TestPublicFaultsInRootModule executes every public mutation against a
// captured in-memory trace. Besides testing the mutation functions, this
// verifies that each injected fault remains locatable by the public contract.
func TestPublicFaultsInRootModule(t *testing.T) {
	cases := make(map[string]ReplayCase)
	for _, replayCase := range PublicCases() {
		cases[replayCase.Name] = replayCase
	}

	results := make(map[string]RunResult, len(cases))
	for name, replayCase := range cases {
		result, err := RunCase(
			context.Background(),
			replayCase,
			rootReplayBackends(t, replayCase.Name),
		)
		require.NoError(t, err)
		require.Equal(t, StatusPassed, result.Report.Status)
		results[name] = result
	}

	for _, fault := range PublicFaults() {
		fault := fault
		t.Run(fault.Name, func(t *testing.T) {
			replayCase, ok := cases[fault.Case]
			require.True(t, ok)
			result := results[fault.Case]
			source := result.Traces["inmemory-b"]

			faulty, err := fault.Inject(source)
			require.NoError(t, err)
			faulty.Backend = "inmemory-b-fault:" + fault.Name

			diffs, err := DetectInjectedFault(
				replayCase,
				"inmemory-a",
				faulty.Backend,
				result.Traces["inmemory-a"],
				faulty,
			)
			require.NoError(t, err)
			require.Truef(t, HasBlockingDiff(diffs), "fault %q was not detected", fault.Name)

			var matched bool
			for _, diff := range diffs {
				if fault.Expect.Matches(diff) {
					matched = true
					break
				}
			}
			require.Truef(t, matched, "fault %q was not precisely located: %+v", fault.Name, diffs)
		})
	}
}

func TestFaultExpectationsAndServiceWrappers(t *testing.T) {
	t.Run("expectation_matches_all_constraints", func(t *testing.T) {
		eventIndex := 2
		summaryKey := "root/tools/weather"
		diff := Diff{
			Section:          "events",
			Path:             "$.events[2].content",
			EventIndex:       &eventIndex,
			SummaryFilterKey: &summaryKey,
			TrackName:        "tool.weather",
		}
		require.True(t, FaultExpectation{
			Section:           "events",
			PathContains:      "content",
			RequireEventIndex: true,
			RequireSummaryKey: true,
			RequireTrackName:  true,
		}.Matches(diff))
		require.False(t, (FaultExpectation{RequireBlockingDiff: true}).Matches(
			Diff{AllowedDiff: true},
		))
		require.False(t, (FaultExpectation{Section: "state"}).Matches(diff))
		require.False(t, (FaultExpectation{PathContains: "missing"}).Matches(diff))
		require.False(t, (FaultExpectation{RequireEventIndex: true}).Matches(Diff{}))
		require.False(t, (FaultExpectation{RequireMemoryID: true}).Matches(Diff{}))
		require.False(t, (FaultExpectation{RequireSummaryKey: true}).Matches(Diff{}))
		require.False(t, (FaultExpectation{RequireTrackName: true}).Matches(Diff{}))
	})

	t.Run("fault_injection_validation_and_clone", func(t *testing.T) {
		source := Trace{Backend: "source", Final: Snapshot{
			SessionID: "session",
			Events:    []map[string]any{{"id": "event:one"}},
			Summaries: map[string]SummarySnapshot{},
			Tracks:    map[string][]TrackEventSnapshot{},
		}}
		_, err := FaultInjection{}.Inject(source)
		require.ErrorContains(t, err, "fault name and case are required")
		_, err = (FaultInjection{Name: "missing-apply", Case: "case"}).Inject(source)
		require.ErrorContains(t, err, "has no mutation")
		original := source.Final.Events[0]["id"]
		injected, err := (FaultInjection{
			Name: "copy", Case: "case",
			Apply: func(trace *Trace) error {
				trace.Final.Events[0]["id"] = "event:changed"
				return nil
			},
		}).Inject(source)
		require.NoError(t, err)
		require.Equal(t, "event:one", original)
		require.Equal(t, "event:changed", injected.Final.Events[0]["id"])
	})

	t.Run("session_fault_modes", func(t *testing.T) {
		ctx := context.Background()
		key := rootReplayKey("fault-session")
		base := sessioninmemory.NewSessionService()
		t.Cleanup(func() { require.NoError(t, base.Close()) })
		_, err := base.CreateSession(ctx, key, session.StateMap{})
		require.NoError(t, err)
		sess, err := base.GetSession(ctx, key)
		require.NoError(t, err)

		makeEvent := func(id string, delta session.StateMap) *event.Event {
			return &event.Event{
				ID: id, Author: "assistant", Version: event.CurrentVersion,
				Response: &model.Response{Choices: []model.Choice{{
					Message: model.Message{Role: model.RoleAssistant, Content: id},
				}}},
				StateDelta: delta,
			}
		}

		err = (&FaultySessionService{
			Service: base, Mode: SessionFaultPreCommitEventError,
		}).AppendEvent(ctx, sess, makeEvent("precommit", nil))
		require.Error(t, err)

		err = base.AppendEvent(ctx, sess, &event.Event{
			ID: "user-start", Author: "user", Version: event.CurrentVersion,
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "start"},
			}}},
		})
		require.NoError(t, err)

		err = (&FaultySessionService{
			Service: base, Mode: SessionFaultLostEventAck,
		}).AppendEvent(ctx, sess, makeEvent("lost-ack", nil))
		require.Error(t, err)

		err = (&FaultySessionService{
			Service: base, Mode: SessionFaultDuplicateEvent,
		}).AppendEvent(ctx, sess, makeEvent("duplicate", nil))
		require.NoError(t, err)

		err = (&FaultySessionService{
			Service: base, Mode: SessionFaultDirtyState,
		}).AppendEvent(ctx, sess, makeEvent(
			"dirty-state",
			session.StateMap{"phase": []byte(`"fresh"`)},
		))
		require.NoError(t, err)

		err = (&FaultySessionService{
			Service: base, Mode: "passthrough",
		}).AppendEvent(ctx, sess, makeEvent("passthrough", nil))
		require.NoError(t, err)

		stored, err := base.GetSession(ctx, key)
		require.NoError(t, err)
		require.GreaterOrEqual(t, len(stored.Events), 7)
		require.Equal(t, []byte(`"stale-after-retry"`), stored.State["phase"])
	})

	t.Run("summary_and_memory_fault_modes", func(t *testing.T) {
		ctx := context.Background()
		key := rootReplayKey("fault-summary")
		summaryService := sessioninmemory.NewSessionService(
			sessioninmemory.WithSummarizer(&rootReplaySummarizer{}),
		)
		t.Cleanup(func() { require.NoError(t, summaryService.Close()) })
		sess, err := summaryService.CreateSession(ctx, key, session.StateMap{})
		require.NoError(t, err)
		err = summaryService.AppendEvent(ctx, sess, rootReplayEvent("summary-event", "summary"))
		require.NoError(t, err)
		err = (&FaultySessionService{
			Service: summaryService, Mode: SessionFaultLostSummaryAck,
		}).CreateSessionSummary(ctx, sess, session.SummaryFilterKeyAllContents, true)
		require.Error(t, err)

		memoryService := memoryinmemory.NewMemoryService()
		t.Cleanup(func() { require.NoError(t, memoryService.Close()) })
		err = (&FaultyMemoryService{
			Service: memoryService, Mode: MemoryFaultDuplicateWrite,
		}).AddMemory(
			ctx,
			memory.UserKey{AppName: key.AppName, UserID: key.UserID},
			"duplicate memory",
			[]string{"test"},
		)
		require.NoError(t, err)
		entries, err := memoryService.ReadMemories(
			ctx,
			memory.UserKey{AppName: key.AppName, UserID: key.UserID},
			100,
		)
		require.NoError(t, err)
		require.Len(t, entries, 2)

		err = (&FaultyMemoryService{
			Service: memoryService, Mode: "passthrough",
		}).AddMemory(
			ctx,
			memory.UserKey{AppName: key.AppName, UserID: key.UserID},
			"passthrough memory",
			nil,
		)
		require.NoError(t, err)
	})
}

func TestOperationValidationBranches(t *testing.T) {
	ctx := context.Background()
	backend := rootReplayFactories()[0].Create
	value, cleanup, err := backend(ctx, "operation-validation")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cleanup()) })
	runtime := NewRuntime(value, NormalizeOptions{})
	require.NoError(t, (CreateSessionOperation{ID: "create"}).Execute(ctx, runtime))

	require.ErrorContains(t, (AppendEventOperation{}).Execute(ctx, runtime), "event logical id is required")
	require.ErrorContains(t, (AppendEventOperation{
		ID: "unknown-tool-response",
		Spec: EventSpec{
			Author: "tool", Role: model.RoleTool, ToolResponseID: "missing",
		},
	}).Execute(ctx, runtime), "unknown logical tool call")
	require.ErrorContains(t, (AppendEventOperation{
		ID: "unknown-parent-trigger",
		Spec: EventSpec{
			Author: "assistant", Role: model.RoleAssistant,
			ParentTriggerID: "missing",
		},
	}).Execute(ctx, runtime), "parent metadata references unknown")
	require.ErrorContains(t, (AppendEventOperation{
		ID: "unknown-tool-args",
		Spec: EventSpec{
			Author: "assistant", Role: model.RoleAssistant,
			ToolCallArgs: map[string]json.RawMessage{"missing": json.RawMessage(`{}`)},
		},
	}).Execute(ctx, runtime), "tool args reference unknown")

	require.ErrorContains(t, (DeleteStateOperation{
		ID: "invalid-delete", Scope: StateScopeSession, Keys: []string{"key"},
	}).Execute(ctx, runtime), "state delete is unsupported")
	require.ErrorContains(t, (ClearStateOperation{
		ID: "invalid-clear", Scope: StateScopeSession,
	}).Execute(ctx, runtime), "state clear is unsupported")
	require.ErrorContains(t, (SetStateOperation{
		ID: "invalid-set", Scope: "invalid", Values: session.StateMap{"key": []byte(`1`)},
	}).Execute(ctx, runtime), "unsupported state scope")

	require.NoError(t, (SetStateOperation{
		ID: "user-set", Scope: StateScopeUser,
		Values: session.StateMap{"user-key": []byte(`1`)},
	}).Execute(ctx, runtime))
	require.NoError(t, (DeleteStateOperation{
		ID: "user-delete", Scope: StateScopeUser, Keys: []string{"user-key"},
	}).Execute(ctx, runtime))
	require.NoError(t, (SetStateOperation{
		ID: "app-set", Scope: StateScopeApp,
		Values: session.StateMap{"app-key": []byte(`1`)},
	}).Execute(ctx, runtime))
	require.NoError(t, (ClearStateOperation{
		ID: "app-clear", Scope: StateScopeApp,
	}).Execute(ctx, runtime))

	require.Equal(t, "clear", (ClearMemoryOperation{ID: "clear"}).OperationID())
	require.NoError(t, (AddMemoryOperation{
		ID: "clearable", Content: "clearable memory", Topics: []string{"test"},
	}).Execute(ctx, runtime))
	require.NoError(t, (ClearMemoryOperation{ID: "clear"}).Execute(ctx, runtime))
	memories, err := value.Memory.ReadMemories(
		ctx,
		memory.UserKey{AppName: value.SessionKey.AppName, UserID: value.SessionKey.UserID},
		100,
	)
	require.NoError(t, err)
	require.Empty(t, memories)

	require.Equal(t, "recovery", (RecoveryAppendEventOperation{ID: "recovery"}).OperationID())
	require.NoError(t, (AppendTrackOperation{
		ID: "invalid-track-json", Track: "test",
		Payload: json.RawMessage(`{"not-json"`),
	}).Execute(ctx, runtime))
	require.NoError(t, (AppendTrackOperation{
		ID: "nested-track-identifiers", Track: "test",
		Payload: json.RawMessage(
			`{"invocation_id":"nested","items":[{"tool_call_id":"nested-tool"}]}`,
		),
	}).Execute(ctx, runtime))

	require.ErrorContains(t, (AddMemoryOperation{
		ID: "no-memory", Content: "value",
	}).Execute(ctx, &Runtime{
		Backend:    Backend{Name: "no-memory", Session: value.Session, SessionKey: value.SessionKey},
		Normalizer: NewNormalizer(NormalizeOptions{}),
		Ledger:     NewIdentityLedger(),
	}), "has no memory service")
	require.ErrorContains(t, (UpdateMemoryOperation{
		ID: "unknown-update", MemoryID: "missing", Content: "value",
	}).Execute(ctx, runtime), "unknown logical memory")
	require.ErrorContains(t, (DeleteMemoryOperation{
		ID: "unknown-delete", MemoryID: "missing",
	}).Execute(ctx, runtime), "unknown logical memory")
	require.ErrorContains(t, (AppendTrackOperation{
		ID: "no-track", Track: "track",
	}).Execute(ctx, &Runtime{
		Backend:    Backend{Name: "no-track", Session: &stubSessionService{}, SessionKey: value.SessionKey},
		Normalizer: NewNormalizer(NormalizeOptions{}),
		Ledger:     NewIdentityLedger(),
	}), "has no track service")
	require.ErrorContains(t, (ParallelOperation{
		ID:         "parallel-error",
		Operations: []Operation{errorOperation{id: "one", err: errors.New("one")}},
	}).Execute(ctx, runtime), "one")
	require.ErrorContains(t, (WaitSummaryOperation{
		ID: "wait-missing", FilterKey: "missing", Timeout: time.Millisecond,
	}).Execute(ctx, runtime), "was not persisted")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, (WaitSummaryOperation{
		ID: "wait-cancelled", FilterKey: "missing", Timeout: time.Second,
	}).Execute(cancelled, runtime), context.Canceled)

	require.NoError(t, (SessionWindowCheckpointOperation{
		ID: "window-default-name", EventNum: 1,
	}).Execute(ctx, runtime))
	trace, err := runtime.trace(ctx)
	require.NoError(t, err)
	require.Condition(t, func() bool {
		for _, checkpoint := range trace.Checkpoints {
			if checkpoint.Name == "window-default-name" &&
				checkpoint.AfterOp == "window-default-name" {
				return true
			}
		}
		return false
	})
}

func TestReportBuildAndWriteValidation(t *testing.T) {
	report := BuildReport("a", []string{"a", "b"}, []CaseReport{
		{Name: "passed", Status: StatusPassed, Diffs: []Diff{{AllowedDiff: true}}},
		{Name: "failed", Status: StatusFailed, Diffs: []Diff{{AllowedDiff: false}}},
		{Name: "skipped", Status: StatusSkipped},
		{Name: "mixed", Status: StatusMixed},
		{Name: "inconclusive", Status: StatusInconclusive},
		{Name: "unknown", Status: "unknown"},
	})
	require.False(t, report.Healthy())
	require.Equal(t, 6, report.TotalCases)
	require.Equal(t, 1, report.PassedCases)
	require.Equal(t, 2, report.FailedCases)
	require.Equal(t, 1, report.SkippedCases)
	require.Equal(t, 1, report.MixedCases)
	require.Equal(t, 1, report.Inconclusive)
	require.Equal(t, 1, report.AllowedDiffs)
	require.Equal(t, 1, report.BlockingDiffs)
	require.ErrorContains(t, WriteReport("", report), "report path is empty")

	path := filepath.Join(t.TempDir(), "nested", "report.json")
	report.Version = 0
	report.GeneratedAt = time.Time{}
	require.NoError(t, WriteReport(path, report))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, 1, decoded.Version)
	require.False(t, decoded.GeneratedAt.IsZero())
}

func TestIdentityLedgerReplaceValidation(t *testing.T) {
	var nilLedger *IdentityLedger
	require.ErrorContains(
		t,
		nilLedger.Replace(IdentityMemory, "old", "new", "logical"),
		"identity ledger is nil",
	)

	ledger := NewIdentityLedger()
	require.ErrorContains(
		t,
		ledger.Replace("", "old", "new", "logical"),
		"identity namespace",
	)
	require.NoError(t, ledger.Register(IdentityMemory, "raw-old", "logical"))
	require.ErrorContains(
		t,
		ledger.Replace(IdentityMemory, "wrong-old", "raw-new", "logical"),
		"not",
	)
	require.NoError(t, ledger.Register(IdentityMemory, "raw-other", "other"))
	require.ErrorContains(
		t,
		ledger.Replace(IdentityMemory, "raw-old", "raw-other", "logical"),
		"already maps",
	)
	require.NoError(
		t,
		ledger.Replace(IdentityMemory, "raw-old", "raw-new", "logical"),
	)
	_, oldExists := ledger.Logical(IdentityMemory, "raw-old")
	require.False(t, oldExists)
	raw, ok := ledger.Raw(IdentityMemory, "logical")
	require.True(t, ok)
	require.Equal(t, "raw-new", raw)
}

func TestFaultMutationErrorBranches(t *testing.T) {
	require.ErrorContains(
		t,
		mutateEvent(&Trace{}, "event:missing", func(map[string]any, map[string]any) {}),
		"not found",
	)
	require.ErrorContains(
		t,
		mutateEvent(
			&Trace{Final: Snapshot{
				Events: []map[string]any{{"id": "event:no-choices"}},
			}},
			"event:no-choices",
			func(map[string]any, map[string]any) {},
		),
		"has no choices",
	)
	require.ErrorContains(
		t,
		mutateEvent(
			&Trace{Final: Snapshot{
				Events: []map[string]any{{
					"id":      "event:no-message",
					"choices": []any{map[string]any{}},
				}},
			}},
			"event:no-message",
			func(map[string]any, map[string]any) {},
		),
		"has no message",
	)
	require.ErrorContains(
		t,
		mutateSummary(&Trace{Final: Snapshot{
			Summaries: map[string]SummarySnapshot{},
		}}, "missing", func(*Snapshot, *SummarySnapshot) {}),
		"not found",
	)
	_, err := checkpointSummary(&Trace{}, "missing", "summary")
	require.ErrorContains(t, err, `checkpoint "missing" not found`)
	_, err = checkpointSummary(&Trace{
		Checkpoints: []CheckpointSnapshot{{
			Name: "checkpoint",
			Snapshot: Snapshot{
				Summaries: map[string]SummarySnapshot{},
			},
		}},
	}, "checkpoint", "summary")
	require.ErrorContains(t, err, `summary "summary" not found`)
	require.Nil(t, cloneIntPointer(nil))
}

func TestMemoryHelpersAndJSONValidation(t *testing.T) {
	eventTime := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	metadata := &memory.Metadata{
		Kind: memory.KindEpisode, EventTime: &eventTime,
		Participants: []string{"Alice", "User"}, Location: "Shenzhen",
	}
	entry := &memory.Entry{
		ID: "memory-id",
		Memory: &memory.Memory{
			Memory: "content", Topics: []string{"b", "a"},
			Kind: memory.KindEpisode, EventTime: &eventTime,
			Participants: []string{"User", "Alice"}, Location: "Shenzhen",
		},
	}
	id, err := identifyMemoryID(
		[]*memory.Entry{{ID: "existing", Memory: entry.Memory}},
		[]*memory.Entry{entry},
		"content",
		[]string{"a", "b"},
		metadata,
	)
	require.NoError(t, err)
	require.Equal(t, "memory-id", id)
	_, err = identifyMemoryID(nil, nil, "missing", nil, nil)
	require.ErrorContains(t, err, "was not found after write")
	require.False(t, memoryMatches(nil, "content", nil, nil))
	require.False(t, memoryMatches(
		&memory.Entry{Memory: &memory.Memory{Memory: "other"}},
		"content", nil, nil,
	))
	require.False(t, memoryMatches(
		&memory.Entry{Memory: &memory.Memory{Memory: "content", Topics: []string{"other"}}},
		"content", []string{"expected"}, nil,
	))
	require.False(t, memoryMatches(
		&memory.Entry{Memory: &memory.Memory{
			Memory: "content", Topics: []string{"a", "b"},
			Kind: memory.KindFact,
		}},
		"content", []string{"a", "b"}, metadata,
	))
	cloned := cloneMetadata(metadata)
	require.NotSame(t, metadata, cloned)
	require.NotSame(t, metadata.EventTime, cloned.EventTime)
	require.Nil(t, cloneMetadata(nil))

	require.ErrorContains(
		t,
		decodeJSON([]byte(`{} {}`), &map[string]any{}),
		"unexpected trailing JSON value",
	)
	require.Error(t, decodeJSON([]byte(`{`), &map[string]any{}))
	require.NotEmpty(t, shortHash("value"))
}

func rootReplayFactories() []BackendFactory {
	capabilities := rootReplayCapabilities()
	return []BackendFactory{
		rootReplayFactory("inmemory-a", capabilities),
		rootReplayFactory("inmemory-b", capabilities),
	}
}

func rootReplayBackends(t *testing.T, caseName string) []Backend {
	t.Helper()
	backends := make([]Backend, 0, 2)
	for _, factory := range rootReplayFactories() {
		backend, cleanup, err := factory.Create(context.Background(), caseName)
		require.NoError(t, err)
		t.Cleanup(func() {
			require.NoError(t, cleanup())
		})
		backends = append(backends, backend)
	}
	return backends
}

func rootReplayFactory(name string, capabilities CapabilitySet) BackendFactory {
	return BackendFactory{
		Name:         name,
		Capabilities: capabilities.Clone(),
		Create: func(_ context.Context, caseName string) (Backend, func() error, error) {
			svc := sessioninmemory.NewSessionService(
				sessioninmemory.WithSessionTTL(0),
				sessioninmemory.WithSummarizer(&rootReplaySummarizer{}),
				sessioninmemory.WithSummaryFilterAllowlist("root/tools/weather"),
				sessioninmemory.WithCascadeFullSessionSummary(false),
			)
			mem := memoryinmemory.NewMemoryService(
				memoryinmemory.WithMinSearchScore(0),
				memoryinmemory.WithMaxResults(0),
			)
			key := rootReplayKey(caseName)
			return Backend{
					Name: name, Session: svc, Memory: mem, Track: svc,
					SessionKey: key, Capabilities: capabilities.Clone(),
				},
				func() error { return errors.Join(mem.Close(), svc.Close()) },
				nil
		},
	}
}

func rootReplayCapabilities() CapabilitySet {
	supported := Capability{Supported: true}
	unsupported := func(reason string) Capability {
		return Capability{AllowedDiff: true, Reason: reason}
	}
	return CapabilitySet{
		CapabilityEvents:              supported,
		CapabilityState:               supported,
		CapabilityAppState:            supported,
		CapabilityUserState:           supported,
		CapabilityMemory:              supported,
		CapabilityMemorySearch:        supported,
		CapabilitySummary:             supported,
		CapabilityTracks:              supported,
		CapabilityStateDelete:         supported,
		CapabilityStateClear:          supported,
		CapabilityEventPaging:         unsupported("paging is not exercised by root unit tests"),
		CapabilityTTL:                 unsupported("TTL is disabled for deterministic root unit tests"),
		CapabilityEventStateDeltaNull: supported,
	}
}

func rootReplayKey(name string) session.Key {
	safe := strings.NewReplacer("/", "-", "\\", "-", " ", "-", ":", "-").Replace(strings.ToLower(name))
	return session.Key{
		AppName:   "replay-" + safe,
		UserID:    "user-" + safe,
		SessionID: "session-" + safe,
	}
}

type rootReplaySummarizer struct{}

func (*rootReplaySummarizer) ShouldSummarize(*session.Session) bool { return true }

func (*rootReplaySummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	if sess == nil {
		return "", session.ErrNilSession
	}
	parts := make([]string, 0, len(sess.Events))
	for i := range sess.Events {
		evt := &sess.Events[i]
		message := rootReplayMessage(evt)
		parts = append(parts, fmt.Sprintf(
			"%s|%s|%s|%s|%s",
			evt.Author, message.Role, message.Content, evt.Branch, evt.FilterKey,
		))
	}
	return strings.Join(parts, "\n"), nil
}

func (*rootReplaySummarizer) SetPrompt(string)     {}
func (*rootReplaySummarizer) SetModel(model.Model) {}
func (*rootReplaySummarizer) Metadata() map[string]any {
	return map[string]any{"name": "root-replay-test"}
}

func rootReplayMessage(evt *event.Event) model.Message {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return model.Message{}
	}
	return evt.Response.Choices[0].Message
}

func rootReplayEvent(id, content string) *event.Event {
	return &event.Event{
		ID: id, Author: "assistant", Version: event.CurrentVersion,
		Response: &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: content},
		}}},
	}
}
