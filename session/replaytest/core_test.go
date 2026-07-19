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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestIdentityLedgerPreservesOneToOneRelations(t *testing.T) {
	ledger := NewIdentityLedger()
	require.NoError(t, ledger.Register(IdentityEvent, "raw-a", "turn-1"))
	logical, ok := ledger.Logical(IdentityEvent, "raw-a")
	require.True(t, ok)
	require.Equal(t, "turn-1", logical)
	raw, ok := ledger.Raw(IdentityEvent, "turn-1")
	require.True(t, ok)
	require.Equal(t, "raw-a", raw)
	require.Error(t, ledger.Register(IdentityEvent, "raw-b", "turn-1"))
	require.Error(t, ledger.Register(IdentityEvent, "raw-a", "turn-2"))

	clone := ledger.Clone()
	require.NoError(t, clone.Register(IdentityEvent, "raw-b", "turn-2"))
	_, originalHasSecond := ledger.Logical(IdentityEvent, "raw-b")
	require.False(t, originalHasSecond)
}

func TestNormalizeBytesPreservesRepresentation(t *testing.T) {
	require.Equal(t, TaggedBytes{Kind: "nil"}, normalizeBytes(nil))
	require.Equal(t, TaggedBytes{Kind: "json", Value: json.Number("9007199254740993")},
		normalizeBytes([]byte("9007199254740993")))
	require.Equal(t, TaggedBytes{Kind: "utf8", Value: "hello"}, normalizeBytes([]byte("hello")))
	require.Equal(t, TaggedBytes{Kind: "base64", Value: "/wA="}, normalizeBytes([]byte{0xff, 0x00}))
}

func TestNormalizerUsesLogicalIdentifiersAndKeepsReferences(t *testing.T) {
	ledger := NewIdentityLedger()
	require.NoError(t, ledger.Register(IdentityEvent, "event-raw", "assistant-tool"))
	require.NoError(t, ledger.Register(IdentityInvocation, "invocation-raw", "root-invocation"))
	require.NoError(t, ledger.Register(IdentityToolCall, "call-raw", "weather-call"))
	timestamp := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	evt := event.Event{
		Response: &model.Response{
			ID: "response-noise", Created: 123, Timestamp: timestamp,
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{
							{
								Type: "function", ID: "call-raw",
								Function: model.FunctionDefinitionParam{
									Name: "weather", Arguments: []byte(`{"city":"Shenzhen","large":9007199254740993}`),
								},
							},
						},
					},
				},
			},
		},
		ID: "event-raw", InvocationID: "invocation-raw", Author: "assistant",
		Timestamp: timestamp,
		Extensions: map[string]json.RawMessage{
			event.ToolCallArgsExtensionKey: json.RawMessage(`{"call-raw":{"city":"Shenzhen"}}`),
		},
	}
	sess := &session.Session{
		ID: "session-1", AppName: "app", UserID: "user",
		Events: []event.Event{evt}, State: session.StateMap{},
	}
	snapshot, err := NewNormalizer(NormalizeOptions{}).Normalize(CaptureInput{Session: sess}, ledger)
	require.NoError(t, err)
	require.Len(t, snapshot.Events, 1)
	got := snapshot.Events[0]
	require.Equal(t, "event:assistant-tool", got["id"])
	require.Equal(t, "invocation:root-invocation", got["invocationId"])
	require.NotContains(t, got, "timestamp")
	require.NotContains(t, got, "created")
	require.NotContains(t, got, "response")

	choices := got["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	call := message["tool_calls"].([]any)[0].(map[string]any)
	require.Equal(t, "tool-call:weather-call", call["id"])
	arguments := call["function"].(map[string]any)["arguments"].(map[string]any)
	require.Equal(t, json.Number("9007199254740993"), arguments["large"])

	extensions := got["extensions"].(map[string]any)
	args := extensions[event.ToolCallArgsExtensionKey].(map[string]any)
	require.Contains(t, args, "tool-call:weather-call")
}

func TestNormalizerPreservesDuplicateMemoryMultiplicity(t *testing.T) {
	entries := []*memory.Entry{
		{ID: "raw-a", AppName: "app", UserID: "user", Memory: &memory.Memory{Memory: "same", Topics: []string{"b", "a"}}},
		{ID: "raw-b", AppName: "app", UserID: "user", Memory: &memory.Memory{Memory: "same", Topics: []string{"a", "b"}}},
	}
	sess := &session.Session{ID: "session", AppName: "app", UserID: "user", State: session.StateMap{}}
	snapshot, err := NewNormalizer(NormalizeOptions{MemoryOrder: MemoryOrderUnordered}).Normalize(
		CaptureInput{Session: sess, Memories: entries}, NewIdentityLedger(),
	)
	require.NoError(t, err)
	require.Len(t, snapshot.Memories, 2)
	require.NotEqual(t, snapshot.Memories[0].ID, snapshot.Memories[1].ID)
	require.Equal(t, -1, snapshot.Memories[0].Rank)
	require.Equal(t, []string{"a", "b"}, snapshot.Memories[0].Topics)
}

func TestNormalizerMapsSummaryBoundaryToLogicalEvent(t *testing.T) {
	ledger := NewIdentityLedger()
	require.NoError(t, ledger.Register(IdentityEvent, "raw-1", "turn-1"))
	require.NoError(t, ledger.Register(IdentityEvent, "raw-2", "turn-2"))
	first := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)
	sess := &session.Session{
		ID: "session", AppName: "app", UserID: "user", State: session.StateMap{},
		Events: []event.Event{
			{Response: &model.Response{}, ID: "raw-1", Timestamp: first, Author: "user"},
			{Response: &model.Response{}, ID: "raw-2", Timestamp: second, Author: "assistant"},
		},
		Summaries: map[string]*session.Summary{
			"root/tools": {
				Summary: "summary", UpdatedAt: second,
				Boundary: session.NewSummaryBoundaryWithEventID("root/tools", second, "raw-2"),
			},
		},
	}
	snapshot, err := NewNormalizer(NormalizeOptions{}).Normalize(CaptureInput{Session: sess}, ledger)
	require.NoError(t, err)
	summary := snapshot.Summaries["root/tools"]
	require.Equal(t, "event:turn-2", summary.LastEventLogicalID)
	require.Equal(t, 1, *summary.LastEventIndex)
	require.Equal(t, 1, *summary.CutoffAtEventIndex)
}

func TestSummaryReachedExpectedEventRejectsStaleSummary(t *testing.T) {
	ledger := NewIdentityLedger()
	require.NoError(t, ledger.Register(
		IdentityEvent,
		"raw-old",
		"old",
	))
	require.NoError(t, ledger.Register(
		IdentityEvent,
		"raw-new",
		"new",
	))
	value := &session.Summary{
		Summary: "old summary",
		Boundary: session.NewSummaryBoundaryWithEventID(
			"root",
			time.Now(),
			"raw-old",
		),
	}
	require.False(t, summaryReachedExpectedEvent(value, "new", ledger))
	value.Boundary.LastEventID = "raw-new"
	require.True(t, summaryReachedExpectedEvent(value, "new", ledger))
	require.True(t, summaryReachedExpectedEvent(value, "", ledger))
}

func TestCompareSnapshotsDistinguishesMissingAndNullAndLocatesDiff(t *testing.T) {
	left := Snapshot{
		SessionID: "session", State: map[string]any{"key": TaggedBytes{Kind: "nil"}},
		Summaries: map[string]SummarySnapshot{}, Tracks: map[string][]TrackEventSnapshot{},
	}
	right := left
	right.State = map[string]any{}
	diffs, err := CompareSnapshots("state", "inmemory", "sqlite", left, right, nil, "final")
	require.NoError(t, err)
	require.Len(t, diffs, 1)
	require.Equal(t, "$.state.key", diffs[0].Path)
	require.True(t, diffs[0].BaselinePresent)
	require.False(t, diffs[0].ComparedPresent)
	require.False(t, diffs[0].AllowedDiff)

	allowed := []AllowedDiff{{
		Section: "state", Path: "$.state.key", BackendA: "inmemory", BackendB: "sqlite",
		Reason: "documented state representation gap",
	}}
	diffs, err = CompareSnapshots("state", "inmemory", "sqlite", left, right, allowed, "final")
	require.NoError(t, err)
	require.True(t, diffs[0].AllowedDiff)
	require.Equal(t, "documented state representation gap", diffs[0].Explanation)
}

func TestCompareSnapshotsAddsSummaryAndTrackContext(t *testing.T) {
	left := Snapshot{
		SessionID: "session",
		Summaries: map[string]SummarySnapshot{"root/tools": {FilterKey: "root/tools", Text: "left"}},
		Tracks:    map[string][]TrackEventSnapshot{"tool": {{Track: "tool", Payload: map[string]any{"status": "ok"}}}},
	}
	right := left
	right.Summaries = map[string]SummarySnapshot{"root/tools": {FilterKey: "root/tools", Text: "right"}}
	right.Tracks = map[string][]TrackEventSnapshot{"tool": {{Track: "tool", Payload: map[string]any{"status": "failed"}}}}
	diffs, err := CompareSnapshots("context", "a", "b", left, right, nil, "final")
	require.NoError(t, err)
	require.Len(t, diffs, 2)
	for _, diff := range diffs {
		switch diff.Section {
		case "summaries":
			require.NotEmpty(t, diff.SummaryID)
			require.NotNil(t, diff.SummaryFilterKey)
			require.Equal(t, "root/tools", *diff.SummaryFilterKey)
		case "tracks":
			require.Equal(t, "tool", diff.TrackName)
		default:
			t.Fatalf("unexpected diff section %q", diff.Section)
		}
	}
}

func TestCompareSnapshotsUsesUnsupportedOnlyAsCapabilityMetadata(t *testing.T) {
	left := Snapshot{
		SessionID: "session",
		Events:    []map[string]any{{"id": "event:one"}},
		Tracks:    map[string][]TrackEventSnapshot{},
		Unsupported: map[CapabilityName]string{
			CapabilityTTL: "TTL disabled",
		},
	}
	right := left
	right.Unsupported = map[CapabilityName]string{
		CapabilityEventPaging: "paging not exercised",
	}
	diffs, err := CompareSnapshots(
		"capabilities",
		"inmemory",
		"sqlite",
		left,
		right,
		nil,
		"final",
	)
	require.NoError(t, err)
	require.Empty(t, diffs)
}

func TestCompareTracesRejectsDuplicateCheckpointNames(t *testing.T) {
	snapshot := Snapshot{
		SessionID: "session",
		Summaries: map[string]SummarySnapshot{},
		Tracks:    map[string][]TrackEventSnapshot{},
	}
	trace := Trace{
		Final: snapshot,
		Checkpoints: []CheckpointSnapshot{
			{Name: "same", Snapshot: snapshot},
			{Name: "same", Snapshot: snapshot},
		},
	}
	_, err := CompareTraces("case", "a", "b", trace, trace, nil)
	require.ErrorContains(t, err, `duplicate checkpoint name "same"`)
}

func TestCompareTracesDetectsCheckpointOrderAndAfterOperation(t *testing.T) {
	snapshot := Snapshot{
		SessionID: "session",
		Summaries: map[string]SummarySnapshot{},
		Tracks:    map[string][]TrackEventSnapshot{},
	}
	baseline := Trace{
		Final: snapshot,
		Checkpoints: []CheckpointSnapshot{
			{Name: "first", AfterOp: "op-1", Snapshot: snapshot},
			{Name: "second", AfterOp: "op-2", Snapshot: snapshot},
		},
	}
	compared := Trace{
		Final: snapshot,
		Checkpoints: []CheckpointSnapshot{
			{Name: "second", AfterOp: "wrong-op", Snapshot: snapshot},
			{Name: "first", AfterOp: "op-1", Snapshot: snapshot},
		},
	}
	diffs, err := CompareTraces(
		"checkpoints",
		"inmemory",
		"sqlite",
		baseline,
		compared,
		nil,
	)
	require.NoError(t, err)
	require.NotEmpty(t, diffs)
	require.Condition(t, func() bool {
		for _, diff := range diffs {
			if diff.Section == "checkpoints" &&
				strings.Contains(diff.Path, "after_op") {
				return true
			}
		}
		return false
	})
}

func TestValidateEventOrderRejectsUnexpectedLogicalEvent(t *testing.T) {
	snapshot := Snapshot{
		SessionID: "session",
		Events: []map[string]any{
			{"id": "event:expected"},
			{"id": "event:unexpected"},
		},
	}
	diffs := ValidateEventOrder(
		"causal",
		"sqlite",
		snapshot,
		EventOrderContract{
			ExactLogicalIDs: []string{"expected"},
		},
	)
	require.Len(t, diffs, 1)
	require.Equal(t, "$.events[1]", diffs[0].Path)
	require.Equal(t, 1, *diffs[0].EventIndex)
	require.False(t, diffs[0].BaselinePresent)
	require.True(t, diffs[0].ComparedPresent)
}

func TestValidateTraceEventOrderChecksCompleteCheckpoint(t *testing.T) {
	valid := Snapshot{
		SessionID: "session",
		Events: []map[string]any{
			{"id": "event:parent"},
			{"id": "event:child"},
		},
	}
	invalid := Snapshot{
		SessionID: "session",
		Events: []map[string]any{
			{"id": "event:child"},
			{"id": "event:parent"},
		},
	}
	diffs := validateTraceEventOrder(
		"causal",
		"sqlite",
		Trace{
			Final: valid,
			Checkpoints: []CheckpointSnapshot{{
				Name:     "after_parallel",
				Snapshot: invalid,
			}},
		},
		EventOrderContract{
			ExactLogicalIDs: []string{"parent", "child"},
			HappensBefore:   [][2]string{{"parent", "child"}},
		},
	)
	require.Len(t, diffs, 1)
	require.Equal(t, "after_parallel", diffs[0].Checkpoint)
	require.Equal(t, "causal event order violated", diffs[0].Explanation)
}

func TestRunCaseReportsDeclaredUnsupportedWithoutChangingStatus(t *testing.T) {
	replayCase := ReplayCase{
		Name:       "capability_metadata",
		Required:   []CapabilityName{CapabilityEvents},
		Operations: []Operation{noOpOperation{id: "noop"}},
	}
	backends := []Backend{
		captureOnlyBackend("inmemory", CapabilitySet{
			CapabilityEvents: {Supported: true},
			CapabilityTTL: {
				AllowedDiff: true,
				Reason:      "TTL is disabled for deterministic replay",
			},
		}),
		captureOnlyBackend("sqlite", CapabilitySet{
			CapabilityEvents: {Supported: true},
			CapabilityTTL: {
				AllowedDiff: true,
				Reason:      "TTL is disabled for deterministic replay",
			},
		}),
	}
	result, err := RunCase(context.Background(), replayCase, backends)
	require.NoError(t, err)
	require.Equal(t, StatusPassed, result.Report.Status)
	require.Len(t, result.Report.Unsupported, 2)
	for _, unsupported := range result.Report.Unsupported {
		require.Equal(t, CapabilityTTL, unsupported.Capability)
		require.True(t, unsupported.AllowedDiff)
		require.NotEmpty(t, unsupported.Reason)
	}
}

func TestRuntimeCaptureUsesBackendCapabilityContract(t *testing.T) {
	backend := captureOnlyBackend("backend", CapabilitySet{
		CapabilityEvents: {Supported: true},
		CapabilityTTL: {
			AllowedDiff: true,
			Reason:      "TTL is disabled for deterministic replay",
		},
	})
	backend.Load = func(
		context.Context,
		Backend,
	) (CaptureInput, error) {
		return CaptureInput{
			Session: &session.Session{
				ID:        backend.SessionKey.SessionID,
				AppName:   backend.SessionKey.AppName,
				UserID:    backend.SessionKey.UserID,
				State:     session.StateMap{},
				Summaries: map[string]*session.Summary{},
				Tracks:    map[session.Track]*session.TrackEvents{},
			},
			Unsupported: map[CapabilityName]string{
				CapabilityEvents: "loader attempted to hide event differences",
			},
		}, nil
	}
	snapshot, err := NewRuntime(
		backend,
		NormalizeOptions{},
	).Capture(context.Background())
	require.NoError(t, err)
	require.NotContains(t, snapshot.Unsupported, CapabilityEvents)
	require.Equal(
		t,
		"TTL is disabled for deterministic replay",
		snapshot.Unsupported[CapabilityTTL],
	)
}

func TestBackendValidateRequiresUnsupportedReason(t *testing.T) {
	backend := captureOnlyBackend("backend", CapabilitySet{
		CapabilityEvents: {Supported: true},
		CapabilityTTL:    {},
	})
	require.ErrorContains(
		t,
		backend.Validate(),
		`unsupported capability "ttl" requires a reason`,
	)
}

func TestRunCaseFailsWhenRequiredCapabilityIsNotAllowed(t *testing.T) {
	replayCase := ReplayCase{
		Name:       "required_capability",
		Required:   []CapabilityName{CapabilityTracks},
		Operations: []Operation{noOpOperation{id: "noop"}},
	}
	baseline := captureOnlyBackend("inmemory", CapabilitySet{
		CapabilityTracks: {Supported: true},
	})
	compared := captureOnlyBackend("other", CapabilitySet{
		CapabilityTracks: {
			AllowedDiff: false,
			Reason:      "track persistence is unavailable",
		},
	})
	result, err := RunCase(
		context.Background(),
		replayCase,
		[]Backend{baseline, compared},
	)
	require.NoError(t, err)
	require.Equal(t, StatusFailed, result.Report.Status)
	require.True(t, HasBlockingDiff(result.Report.Diffs))
	require.Equal(t, "capabilities", result.Report.Diffs[0].Section)
	require.Equal(t, "$.capabilities.tracks", result.Report.Diffs[0].Path)
}

func TestWriteReportIsConcurrentSafeAndReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			report := BuildReport("inmemory", []string{"inmemory", "sqlite"}, []CaseReport{{
				Name: "case", Status: StatusPassed,
			}})
			report.Version = index + 1
			require.NoError(t, WriteReport(path, report))
		}(i)
	}
	group.Wait()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var report Report
	require.NoError(t, json.Unmarshal(raw, &report))
	require.Equal(t, 1, report.TotalCases)
	require.Equal(t, 1, report.PassedCases)
}

func TestRunSuiteCleansAlreadyCreatedBackendsWhenFactoryFails(t *testing.T) {
	closed := 0
	factories := []BackendFactory{
		{
			Name: "created",
			Capabilities: CapabilitySet{
				CapabilityEvents: {Supported: true},
			},
			Create: func(context.Context, string) (Backend, func() error, error) {
				return Backend{
						Name:    "created",
						Session: &stubSessionService{},
						SessionKey: session.Key{
							AppName: "app", UserID: "user", SessionID: "session",
						},
						Capabilities: CapabilitySet{
							CapabilityEvents: {Supported: true},
						},
					}, func() error {
						closed++
						return nil
					}, nil
			},
		},
		{
			Name: "failed",
			Capabilities: CapabilitySet{
				CapabilityEvents: {Supported: true},
			},
			Create: func(context.Context, string) (Backend, func() error, error) {
				return Backend{}, nil, errors.New("factory failed")
			},
		},
	}
	_, err := RunSuite(context.Background(), []ReplayCase{{
		Name:       "cleanup",
		Required:   []CapabilityName{CapabilityEvents},
		Operations: []Operation{CreateSessionOperation{ID: "create"}},
	}}, factories)
	require.Error(t, err)
	require.Equal(t, 1, closed)
}

func TestRunSuiteCleansFailingFactoryResource(t *testing.T) {
	closed := 0
	factories := []BackendFactory{
		{
			Name: "created",
			Capabilities: CapabilitySet{
				CapabilityEvents: {Supported: true},
			},
			Create: func(context.Context, string) (Backend, func() error, error) {
				return captureOnlyBackend("created", CapabilitySet{
						CapabilityEvents: {Supported: true},
					}), func() error {
						closed++
						return nil
					}, nil
			},
		},
		{
			Name: "failed",
			Capabilities: CapabilitySet{
				CapabilityEvents: {Supported: true},
			},
			Create: func(context.Context, string) (Backend, func() error, error) {
				return Backend{}, func() error {
					closed++
					return nil
				}, errors.New("factory failed after allocating a resource")
			},
		},
	}
	_, err := RunSuite(context.Background(), []ReplayCase{{
		Name:       "cleanup",
		Required:   []CapabilityName{CapabilityEvents},
		Operations: []Operation{noOpOperation{id: "noop"}},
	}}, factories)
	require.Error(t, err)
	require.Equal(t, 2, closed)
}

func TestRunSuiteJoinsRunAndCleanupErrors(t *testing.T) {
	runErr := errors.New("operation failed")
	closeErr := errors.New("cleanup failed")
	factories := []BackendFactory{
		{
			Name: "baseline",
			Capabilities: CapabilitySet{
				CapabilityEvents: {Supported: true},
			},
			Create: func(context.Context, string) (Backend, func() error, error) {
				return captureOnlyBackend("baseline", CapabilitySet{
						CapabilityEvents: {Supported: true},
					}), func() error {
						return closeErr
					}, nil
			},
		},
		{
			Name: "compared",
			Capabilities: CapabilitySet{
				CapabilityEvents: {Supported: true},
			},
			Create: func(context.Context, string) (Backend, func() error, error) {
				return captureOnlyBackend("compared", CapabilitySet{
					CapabilityEvents: {Supported: true},
				}), nil, nil
			},
		},
	}
	_, err := RunSuite(context.Background(), []ReplayCase{{
		Name:       "cleanup",
		Required:   []CapabilityName{CapabilityEvents},
		Operations: []Operation{errorOperation{id: "fail", err: runErr}},
	}}, factories)
	require.ErrorIs(t, err, runErr)
	require.ErrorIs(t, err, closeErr)
}

func TestRunSuiteJoinsMissingFactoryAndCleanupErrors(t *testing.T) {
	closeErr := errors.New("cleanup failed")
	factories := []BackendFactory{
		{
			Name: "created",
			Capabilities: CapabilitySet{
				CapabilityEvents: {Supported: true},
			},
			Create: func(context.Context, string) (Backend, func() error, error) {
				return captureOnlyBackend("created", CapabilitySet{
						CapabilityEvents: {Supported: true},
					}), func() error {
						return closeErr
					}, nil
			},
		},
		{
			Name: "missing",
			Capabilities: CapabilitySet{
				CapabilityEvents: {Supported: true},
			},
		},
	}
	_, err := RunSuite(context.Background(), []ReplayCase{{
		Name:       "cleanup",
		Required:   []CapabilityName{CapabilityEvents},
		Operations: []Operation{noOpOperation{id: "noop"}},
	}}, factories)
	require.ErrorContains(t, err, `backend factory "missing" has no create function`)
	require.ErrorIs(t, err, closeErr)
}

type stubSessionService struct {
	session.Service
}

type noOpOperation struct {
	id string
}

func (o noOpOperation) OperationID() string {
	return o.id
}

func (noOpOperation) Execute(context.Context, *Runtime) error {
	return nil
}

type errorOperation struct {
	id  string
	err error
}

func (o errorOperation) OperationID() string {
	return o.id
}

func (o errorOperation) Execute(context.Context, *Runtime) error {
	return o.err
}

func captureOnlyBackend(
	name string,
	capabilities CapabilitySet,
) Backend {
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "session",
	}
	return Backend{
		Name:         name,
		Session:      &stubSessionService{},
		SessionKey:   key,
		Capabilities: capabilities,
		Load: func(
			context.Context,
			Backend,
		) (CaptureInput, error) {
			return CaptureInput{
				Session: &session.Session{
					ID:        key.SessionID,
					AppName:   key.AppName,
					UserID:    key.UserID,
					State:     session.StateMap{},
					Summaries: map[string]*session.Summary{},
					Tracks:    map[session.Track]*session.TrackEvents{},
				},
			}, nil
		},
	}
}
