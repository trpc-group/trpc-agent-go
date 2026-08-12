//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ---------------------------------------------------------------------------
// Failing mocks for error-path coverage
// ---------------------------------------------------------------------------

// failingSession returns errors from every write and read operation.
type failingSession struct {
	session.Service
}

var errSimulated = errors.New("simulated backend failure")

func (f *failingSession) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	opts ...session.Option,
) error {
	return errSimulated
}

func (f *failingSession) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	return &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}, nil
}

func (f *failingSession) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	return errSimulated
}

func (f *failingSession) CreateSessionSummary(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	return errSimulated
}

func (f *failingSession) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	return nil, errSimulated
}

func (f *failingSession) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	te *session.TrackEvent,
	opts ...session.Option,
) error {
	return errSimulated
}

func (f *failingSession) Close() error { return nil }

// nilGetSession returns a nil session without an error.
type nilGetSession struct {
	session.Service
}

func (n *nilGetSession) GetSession(
	ctx context.Context,
	key session.Key,
	opts ...session.Option,
) (*session.Session, error) {
	return nil, nil
}

func (n *nilGetSession) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	opts ...session.Option,
) (*session.Session, error) {
	return &session.Session{ID: key.SessionID, AppName: key.AppName, UserID: key.UserID}, nil
}

func (n *nilGetSession) Close() error { return nil }

// failingMemory returns errors from memory mutation operations.
type failingMemory struct {
	memory.Service
}

func (f *failingMemory) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	return errSimulated
}

func (f *failingMemory) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	content string,
	topics []string,
	opts ...memory.UpdateOption,
) error {
	return errSimulated
}

func (f *failingMemory) DeleteMemory(
	ctx context.Context, memoryKey memory.Key,
) error {
	return errSimulated
}

func (f *failingMemory) ClearMemories(
	ctx context.Context, userKey memory.UserKey,
) error {
	return errSimulated
}

func (f *failingMemory) ReadMemories(
	ctx context.Context, userKey memory.UserKey, limit int,
) ([]*memory.Entry, error) {
	return nil, errSimulated
}

func (f *failingMemory) Close() error { return nil }

// ---------------------------------------------------------------------------
// Harness error-path tests
// ---------------------------------------------------------------------------

func failingSessionFactory() BackendFactory {
	return BackendFactory{
		Name: "failing",
		CreateSession: func() (session.Service, error) {
			return &failingSession{}, nil
		},
		CreateMemory: func() (memory.Service, error) {
			return &failingMemory{}, nil
		},
	}
}

func TestHarnessSetupBackendErrors(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	userKey := session.UserKey{AppName: "app", UserID: "u"}
	h := NewHarness()

	// CreateSession factory nil.
	_, err := h.executeOnBackend(ctx, BackendFactory{Name: "x"}, key, userKey, ReplayCase{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CreateSession is nil")

	// CreateSession factory error.
	_, err = h.executeOnBackend(ctx, BackendFactory{
		Name: "x",
		CreateSession: func() (session.Service, error) {
			return nil, errSimulated
		},
	}, key, userKey, ReplayCase{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create session service")

	// Dedicated track factory error (session service does not implement TrackService).
	_, err = h.executeOnBackend(ctx, BackendFactory{
		Name: "x",
		CreateSession: func() (session.Service, error) {
			return &nilGetSession{}, nil
		},
		CreateTrack: func() (session.TrackService, error) {
			return nil, errSimulated
		},
	}, key, userKey, ReplayCase{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create track service")

	// Memory factory error.
	_, err = h.executeOnBackend(ctx, BackendFactory{
		Name: "x",
		CreateSession: func() (session.Service, error) {
			return &nilGetSession{}, nil
		},
		CreateMemory: func() (memory.Service, error) {
			return nil, errSimulated
		},
	}, key, userKey, ReplayCase{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create memory service")
}

func TestHarnessOperationErrors(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	userKey := session.UserKey{AppName: "app", UserID: "u"}
	h := NewHarness()
	base := NewInMemoryBackend()

	cases := []struct {
		name    string
		factory BackendFactory
		tc      ReplayCase
		errMsg  string
	}{
		{
			name:    "append_event_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				SkipMemories: true,
				Operations: []ReplayOperation{
					{Type: OpAppendEvent, Event: mkAssistantEvent("hi")},
				},
			},
			errMsg: "append_event",
		},
		{
			name:    "append_event_retry_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				SkipMemories: true,
				Operations: []ReplayOperation{
					{SimulateWriteError: true},
					{Type: OpAppendEvent, Event: mkAssistantEvent("hi")},
				},
			},
			errMsg: "append_event (retry)",
		},
		{
			name:    "append_event_nil",
			factory: base,
			tc: ReplayCase{
				SkipMemories: true,
				Operations:   []ReplayOperation{{Type: OpAppendEvent}},
			},
			errMsg: "event is nil",
		},
		{
			name:    "update_state_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				SkipMemories: true,
				Operations: []ReplayOperation{
					{Type: OpUpdateSessionState, StateMap: session.StateMap{"k": []byte("v")}},
				},
			},
			errMsg: "update_session_state",
		},
		{
			name:    "delete_state_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				SkipMemories: true,
				Operations: []ReplayOperation{
					{Type: OpDeleteSessionState, StateKey: "k"},
				},
			},
			errMsg: "delete_session_state",
		},
		{
			name: "track_event_nil",
			factory: BackendFactory{
				Name: "x",
				CreateSession: func() (session.Service, error) {
					return base.CreateSession()
				},
			},
			tc: ReplayCase{
				SkipMemories: true,
				Operations:   []ReplayOperation{{Type: OpAppendTrackEvent}},
			},
			errMsg: "append_track_event: event is nil",
		},
		{
			name:    "track_event_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				SkipMemories: true,
				Operations: []ReplayOperation{
					{Type: OpAppendTrackEvent, TrackEvent: mkTrackEvent("t", `{}`)},
				},
			},
			errMsg: "append_track_event",
		},
		{
			name:    "summary_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				SkipMemories: true,
				Operations: []ReplayOperation{
					{Type: OpCreateSummary, SummaryForce: true},
				},
			},
			errMsg: "create_summary",
		},
		{
			name:    "add_memory_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				Operations: []ReplayOperation{
					{Type: OpAddMemory, MemoryContent: "m"},
				},
			},
			errMsg: "add_memory",
		},
		{
			name:    "update_memory_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				Operations: []ReplayOperation{
					{Type: OpUpdateMemory, MemoryID: "id-1", MemoryContent: "m"},
				},
			},
			errMsg: "update_memory",
		},
		{
			name:    "delete_memory_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				Operations: []ReplayOperation{
					{Type: OpDeleteMemory, MemoryID: "id-1"},
				},
			},
			errMsg: "delete_memory",
		},
		{
			name:    "clear_memories_error",
			factory: failingSessionFactory(),
			tc: ReplayCase{
				Operations: []ReplayOperation{
					{Type: OpClearMemories},
				},
			},
			errMsg: "clear_memories",
		},
		{
			name: "get_session_error",
			factory: BackendFactory{
				Name: "x",
				CreateSession: func() (session.Service, error) {
					return &failingSession{}, nil
				},
			},
			tc:     ReplayCase{SkipMemories: true},
			errMsg: "get_session",
		},
		{
			name: "get_session_nil",
			factory: BackendFactory{
				Name: "x",
				CreateSession: func() (session.Service, error) {
					return &nilGetSession{}, nil
				},
			},
			tc:     ReplayCase{SkipMemories: true},
			errMsg: "get_session returned nil",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.executeOnBackend(ctx, tc.factory, key, userKey, tc.tc)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errMsg)
		})
	}
}

func TestHarnessReadMemoriesError(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	userKey := session.UserKey{AppName: "app", UserID: "u"}
	h := NewHarness()
	base := NewInMemoryBackend()

	factory := BackendFactory{
		Name: "x",
		CreateSession: func() (session.Service, error) {
			return base.CreateSession()
		},
		CreateMemory: func() (memory.Service, error) {
			return &readOnlyFailingMemory{}, nil
		},
	}
	tc := ReplayCase{Operations: []ReplayOperation{{Type: OpAddMemory, MemoryContent: "m"}}}
	_, err := h.executeOnBackend(ctx, factory, key, userKey, tc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read_memories")
}

// readOnlyFailingMemory allows writes but fails ReadMemories.
type readOnlyFailingMemory struct {
	memory.Service
}

func (r *readOnlyFailingMemory) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	return nil
}

func (r *readOnlyFailingMemory) ReadMemories(
	ctx context.Context, userKey memory.UserKey, limit int,
) ([]*memory.Entry, error) {
	return nil, errSimulated
}

func (r *readOnlyFailingMemory) Close() error { return nil }

// ---------------------------------------------------------------------------
// Backend factory tests
// ---------------------------------------------------------------------------

func TestOptionalBackendFactories(t *testing.T) {
	// Unset environment yields nil optional backends.
	for _, env := range []string{"REPLAY_REDIS_ADDR", "REPLAY_POSTGRES_DSN", "REPLAY_MYSQL_DSN", "REPLAY_CLICKHOUSE_DSN"} {
		os.Unsetenv(env)
	}
	assert.Nil(t, NewRedisBackend())
	assert.Nil(t, NewPostgresBackend())
	assert.Nil(t, NewMySQLBackend())
	assert.Nil(t, NewClickHouseBackend())

	defaults := DefaultBackends()
	require.Len(t, defaults, 1)
	assert.Equal(t, "inmemory", defaults[0].Name)

	all := AllBackends()
	assert.Equal(t, len(defaults), len(all))

	// Setting the environment variables exercises the enabled branches;
	// the factories still return nil placeholders until real integrations
	// are wired in, but the code paths are covered.
	t.Setenv("REPLAY_REDIS_ADDR", "localhost:6379")
	t.Setenv("REPLAY_POSTGRES_DSN", "postgres://localhost")
	t.Setenv("REPLAY_MYSQL_DSN", "mysql://localhost")
	t.Setenv("REPLAY_CLICKHOUSE_DSN", "clickhouse://localhost")
	assert.Nil(t, NewRedisBackend())
	assert.Nil(t, NewPostgresBackend())
	assert.Nil(t, NewMySQLBackend())
	assert.Nil(t, NewClickHouseBackend())
	assert.Equal(t, len(defaults), len(AllBackends()))
}

func TestNewSQLiteBackendPlaceholder(t *testing.T) {
	bf := NewSQLiteBackend()
	assert.Equal(t, "sqlite", bf.Name)
	assert.False(t, bf.Supports(OpCreateSession))
	assert.NotEmpty(t, bf.UnsupportedReason(OpCreateSession))
	assert.True(t, bf.Supports(OpAppendEvent))
}

func TestInMemoryBackendFactory(t *testing.T) {
	bf := NewInMemoryBackend()
	svc, err := bf.CreateSession()
	require.NoError(t, err)
	require.NotNil(t, svc)
	defer svc.Close()

	track, err := bf.CreateTrack()
	require.NoError(t, err)
	require.NotNil(t, track)

	mem, err := bf.CreateMemory()
	require.NoError(t, err)
	require.NotNil(t, mem)
	defer mem.Close()
}

func TestWithSessionKey(t *testing.T) {
	h := NewHarness(WithSessionKey("a", "b", "c"))
	assert.Equal(t, "a", h.cfg.appName)
	assert.Equal(t, "b", h.cfg.userID)
	assert.Equal(t, "c", h.cfg.sessionID)
}

// ---------------------------------------------------------------------------
// Comparator extra-branch tests
// ---------------------------------------------------------------------------

func TestCompareEventsCountMismatch(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "s",
		Events: []event.Event{
			{Author: "user"},
		},
	}
	other := &BackendSnapshot{BackendName: "other", SessionID: "s"}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	var found bool
	for _, d := range diffs {
		if d.FieldPath == "events.length" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestCompareTracksPresenceMismatch(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "s",
		Tracks: map[session.Track]*session.TrackEvents{
			"t1": {Track: "t1", Events: []session.TrackEvent{{Track: "t1", Payload: []byte(`{"a":1}`)}}},
		},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "s",
		Tracks: map[session.Track]*session.TrackEvents{
			"t2": {Track: "t2"},
		},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	var missing, extra bool
	for _, d := range diffs {
		if d.TrackName == "t1" && d.CompareValue == "missing" {
			missing = true
		}
		if d.TrackName == "t2" && d.CompareValue == "present" {
			extra = true
		}
	}
	assert.True(t, missing, "expected t1 missing diff")
	assert.True(t, extra, "expected t2 extra diff")
}

func TestCompareMemoriesDifferences(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "s",
		Memories: []*memory.Entry{
			{ID: "1", AppName: "a", UserID: "u", Memory: &memory.Memory{Memory: "same", Topics: []string{"t"}, Location: "loc1"}},
			{ID: "2", AppName: "a", UserID: "u", Memory: &memory.Memory{Memory: "only-in-base"}},
		},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "s",
		Memories: []*memory.Entry{
			{ID: "1", AppName: "a", UserID: "u", Memory: &memory.Memory{Memory: "same", Topics: []string{"different"}, Location: "loc2"}},
		},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	var countDiff, topicsDiff, locationDiff bool
	for _, d := range diffs {
		switch d.FieldPath {
		case "memories.length":
			countDiff = true
		case "memories[0].topics":
			topicsDiff = true
		case "memories[0].location":
			locationDiff = true
		}
	}
	assert.True(t, countDiff)
	assert.True(t, topicsDiff)
	assert.True(t, locationDiff)
}

func TestCompareStateMissingKeyInOther(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "s",
		State:       session.StateMap{"a": []byte("1")},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "s",
		State:       session.StateMap{"b": []byte("2")},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	var aMissing, bExtra bool
	for _, d := range diffs {
		if d.FieldPath == "state[a]" && d.CompareValue == nil {
			aMissing = true
		}
		if d.FieldPath == "state[b]" && d.BaseValue == nil {
			bExtra = true
		}
	}
	assert.True(t, aMissing)
	assert.True(t, bExtra)
}

func TestCompareSummariesTopicsMismatch(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "s",
		Summaries: map[string]*session.Summary{
			"": {Summary: "text", Topics: []string{"a"}},
		},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "s",
		Summaries: map[string]*session.Summary{
			"": {Summary: "text", Topics: []string{"b"}},
		},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	var found bool
	for _, d := range diffs {
		if d.FieldPath == "summaries[].topics" {
			found = true
		}
	}
	assert.True(t, found)
}

// ---------------------------------------------------------------------------
// Normalizer tolerance helpers
// ---------------------------------------------------------------------------

func TestTimeNear(t *testing.T) {
	now := time.Now()
	assert.True(t, timeNear(time.Time{}, time.Time{}))
	assert.True(t, timeNear(now, now.Add(500*time.Millisecond)))
	assert.False(t, timeNear(now, now.Add(10*time.Second)))
	assert.False(t, timeNear(now, time.Time{}))
}

func TestFloatNear(t *testing.T) {
	assert.True(t, floatNear(1.0, 1.0))
	assert.True(t, floatNear(1.0, 1.005))
	assert.True(t, floatNear(0, 0))
	assert.True(t, floatNear(0.5, 0.502))
	assert.False(t, floatNear(0, 0.001))
	assert.False(t, floatNear(1.0, 1.5))
}

func TestNormalizeJSON(t *testing.T) {
	// Valid JSON is deterministically re-marshalled.
	assert.Contains(t, normalizeJSON([]byte(`{"b":1,"a":2}`)), `"a":2`)
	// Empty input yields empty string.
	assert.Equal(t, "", normalizeJSON(nil))
	// Invalid JSON falls back to the trimmed raw text.
	assert.Equal(t, "not-json{", normalizeJSON([]byte(" not-json{ ")))
}

// ---------------------------------------------------------------------------
// Memory update/delete success paths and extra branches
// ---------------------------------------------------------------------------

func TestHarnessMemoryUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	userKey := session.UserKey{AppName: "app", UserID: "u"}
	h := NewHarness()
	base := NewInMemoryBackend()

	// Use one shared memory service instance: each NewMemoryService call
	// has an independent store, so the seeding and the harness must share.
	memSvc, err := base.CreateMemory()
	require.NoError(t, err)
	memUK := memory.UserKey{AppName: "app", UserID: "u"}
	require.NoError(t, memSvc.AddMemory(ctx, memUK, "seed-one", nil))
	require.NoError(t, memSvc.AddMemory(ctx, memUK, "seed-two", nil))
	entries, err := memSvc.ReadMemories(ctx, memUK, 10)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var idOne, idTwo string
	for _, e := range entries {
		switch e.Memory.Memory {
		case "seed-one":
			idOne = e.ID
		case "seed-two":
			idTwo = e.ID
		}
	}
	require.NotEmpty(t, idOne)
	require.NotEmpty(t, idTwo)

	factory := BackendFactory{
		Name: "inmemory",
		CreateSession: func() (session.Service, error) {
			return base.CreateSession()
		},
		CreateMemory: func() (memory.Service, error) {
			return memSvc, nil
		},
	}

	tc := ReplayCase{
		Operations: []ReplayOperation{
			{Type: OpUpdateMemory, MemoryID: idOne, MemoryContent: "updated-one"},
			{Type: OpDeleteMemory, MemoryID: idTwo},
		},
	}
	snap, err := h.executeOnBackend(ctx, factory, key, userKey, tc)
	require.NoError(t, err)
	require.Len(t, snap.Memories, 1)
	assert.Equal(t, "updated-one", snap.Memories[0].Memory.Memory)
}

func TestHarnessCloseBackendOwnsTrack(t *testing.T) {
	// A session service that does not implement TrackService forces the
	// dedicated track creation path, exercising closeBackend ownsTrack.
	base := NewInMemoryBackend()
	factory := BackendFactory{
		Name: "x",
		CreateSession: func() (session.Service, error) {
			return &nilGetSession{}, nil
		},
		CreateTrack: func() (session.TrackService, error) {
			return base.CreateTrack()
		},
	}
	h := NewHarness()
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	userKey := session.UserKey{AppName: "app", UserID: "u"}
	_, err := h.executeOnBackend(context.Background(), factory, key, userKey, ReplayCase{SkipMemories: true})
	// nilGetSession returns a nil session, so snapshot reading fails; the
	// point here is that service creation and closing succeed.
	require.Error(t, err)
}

func TestWriteReportError(t *testing.T) {
	// Writing to a directory path fails after successful marshalling.
	err := WriteReport(t.TempDir(), &ReplayReport{})
	assert.Error(t, err)
}

func TestHarnessRunBaseBackendFailure(t *testing.T) {
	failing := failingSessionFactory()
	h := NewHarness(WithBackends(NewInMemoryBackend(), failing))
	tc := ReplayCase{
		Name:         "base-ok-other-fails",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkAssistantEvent("hi")},
		},
	}
	report, err := h.Run(context.Background(), []ReplayCase{tc})
	require.NoError(t, err)
	// The failing second backend must produce a backend_error diff.
	require.NotEmpty(t, report.CaseResults)
	cr := report.CaseResults[0]
	assert.True(t, cr.HasDiff)
	var backendErr bool
	for _, d := range cr.Differences {
		if d.FieldPath == "backend_error" {
			backendErr = true
		}
	}
	assert.True(t, backendErr)
}

func TestHarnessRunBaseFailsEntirely(t *testing.T) {
	h := NewHarness()
	// Override the backend list directly: WithBackends appends to the
	// default backends, but here the failing backend must be the base.
	h.cfg.backends = []BackendFactory{failingSessionFactory(), NewInMemoryBackend()}
	tc := ReplayCase{
		Name:         "base-fails",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkAssistantEvent("hi")},
		},
	}
	_, err := h.Run(context.Background(), []ReplayCase{tc})
	require.Error(t, err)
}
