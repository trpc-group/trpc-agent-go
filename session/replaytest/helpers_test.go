//go:build cgo

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// runOpsOnBackend executes a sequence of Ops on a single Backend,
// returning the final session.
func runOpsOnBackend(t *testing.T, ctx context.Context, backend Backend, ops []Op) *session.Session {
	t.Helper()
	var lastSess *session.Session
	for _, op := range ops {
		sess := executeOp(ctx, t, backend, op, lastSess)
		if sess != nil {
			lastSess = sess
		}
	}
	return lastSess
}

// executeOp runs a single Op on a backend and returns the session (if any).
// t is required for fatal error reporting when a step uses an unsupported backend service.
func executeOp(ctx context.Context, t *testing.T, backend Backend, op Op, lastSess *session.Session) *session.Session {
	t.Helper()
	switch op.Type {
	case OpCreateSession:
		s, err := backend.Sess.CreateSession(ctx, op.Key, op.State)
		if err != nil {
			return nil
		}
		return s

	case OpGetSession:
		return nil

	case OpDeleteSession:
		backend.Sess.DeleteSession(ctx, op.Key)
		return nil

	case OpAppendEvent:
		if lastSess == nil || op.Event == nil {
			return nil
		}
		backend.Sess.AppendEvent(ctx, lastSess, op.Event)
		return nil

	case OpUpdateSessionState:
		backend.Sess.UpdateSessionState(ctx, op.Key, op.State)
		return nil

	case OpCreateSummary:
		if lastSess == nil {
			return nil
		}
		backend.Sess.CreateSessionSummary(ctx, lastSess, op.FilterKey, op.Force)
		return nil

	case OpGetSummaryText:
		return nil

	case OpAppendTrackEvent:
		if backend.Track == nil {
			if lastSess == nil || op.TrackEvent == nil {
				return nil
			}
			t.Fatalf("append_track_event step requires session.TrackService support on backend %s", backend.Name)
		}
		if lastSess == nil || op.TrackEvent == nil {
			return nil
		}
		backend.Track.AppendTrackEvent(ctx, lastSess, op.TrackEvent)
		return nil

	case OpGetTrackEvents:
		return nil

	case OpAddMemory:
		if backend.Mem == nil {
			t.Fatalf("add_memory step requires memory.Service support on backend %s", backend.Name)
		}
		var opts []memory.AddOption
		if op.MemoryMeta != nil {
			opts = append(opts, memory.WithMetadata(op.MemoryMeta))
		}
		backend.Mem.AddMemory(ctx, op.UserKey, op.MemoryStr, op.Topics, opts...)
		return nil

	case OpReadMemories:
		if backend.Mem == nil {
			t.Fatalf("read_memories step requires memory.Service support on backend %s", backend.Name)
		}
		// ReadMemories is a capture step — results are captured during the Harness.Run flow.
		return nil

	case OpSearchMemories:
		if backend.Mem == nil {
			t.Fatalf("search_memories step requires memory.Service support on backend %s", backend.Name)
		}
		// SearchMemories is a capture step — results are captured during the Harness.Run flow.
		return nil

	case OpUpdateMemory:
		if backend.Mem == nil {
			t.Fatalf("update_memory step requires memory.Service support on backend %s", backend.Name)
		}
		var opts []memory.UpdateOption
		if op.MemoryMeta != nil {
			opts = append(opts, memory.WithUpdateMetadata(op.MemoryMeta))
		}
		backend.Mem.UpdateMemory(ctx, op.MemoryKey, op.MemoryStr, op.Topics, opts...)
		return nil

	case OpDeleteMemory:
		if backend.Mem == nil {
			t.Fatalf("delete_memory step requires memory.Service support on backend %s", backend.Name)
		}
		backend.Mem.DeleteMemory(ctx, op.MemoryKey)
		return nil

	case OpClearMemories:
		if backend.Mem == nil {
			t.Fatalf("clear_memories step requires memory.Service support on backend %s", backend.Name)
		}
		backend.Mem.ClearMemories(ctx, op.UserKey)
		return nil

	case OpGetSummary:
		return nil

	case OpUpdateAppState:
		if err := backend.Sess.UpdateAppState(ctx, op.AppName, op.State); err != nil {
			t.Fatalf("update_app_state on %s: %v", backend.Name, err)
		}
		return nil

	case OpDeleteAppState:
		if err := backend.Sess.DeleteAppState(ctx, op.AppName, op.DeleteKey); err != nil {
			t.Fatalf("delete_app_state on %s: %v", backend.Name, err)
		}
		return nil

	case OpListAppStates:
		// ListAppStates is a capture step.
		return nil

	case OpUpdateUserState:
		if err := backend.Sess.UpdateUserState(ctx, op.SessUserKey, op.State); err != nil {
			t.Fatalf("update_user_state on %s: %v", backend.Name, err)
		}
		return nil

	case OpDeleteUserState:
		if err := backend.Sess.DeleteUserState(ctx, op.SessUserKey, op.DeleteKey); err != nil {
			t.Fatalf("delete_user_state on %s: %v", backend.Name, err)
		}
		return nil

	case OpListUserStates:
		// ListUserStates is a capture step.
		return nil

	default:
		return nil
	}
}

// executeParallelGroups runs parallel operation groups on a backend.
func executeParallelGroups(
	t *testing.T,
	ctx context.Context,
	backend Backend,
	groups [][]Op,
	lastSess *session.Session,
) []*session.Session {
	t.Helper()
	results := make([]session.Session, len(groups))
	var g errgroup.Group
	for i, ops := range groups {
		i, ops := i, ops
		g.Go(func() error {
			groupLastSess := lastSess
			for _, op := range ops {
				sess := executeOp(ctx, t, backend, op, groupLastSess)
				if sess != nil {
					groupLastSess = sess
				}
			}
			if groupLastSess != nil {
				results[i] = *groupLastSess
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("parallel group execution failed: %v", err)
	}
	sessions := make([]*session.Session, len(results))
	for i := range results {
		s := results[i]
		sessions[i] = &s
	}
	return sessions
}

// makeBackends creates InMemory and SQLite Backend instances for a given session key.
func makeBackends(t *testing.T, key session.Key) []Backend {
	t.Helper()
	inMemFactory := inMemoryFactory{}
	sqliteFact := sqliteFactory{}

	inMemBackend := inMemFactory.Create(context.Background(), t)
	sqliteBackend := sqliteFact.Create(context.Background(), t)

	// Override session keys.
	inMemBackend.SessKey = func() session.Key { return key }
	sqliteBackend.SessKey = func() session.Key { return key }

	return []Backend{*inMemBackend, *sqliteBackend}
}

// runCaseOnBackends executes a Case (Op-based) on the given backends and returns results.
func runCaseOnBackends(t *testing.T, ctx context.Context, c Case, backends []Backend) []CaseResult {
	t.Helper()
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	harness := Harness{
		Backends:   backends,
		Normalizer: normalizer,
		Allowed:    c.AllowedDiffs,
	}
	result, err := harness.Run(ctx, c)
	require.NoError(t, err, "Harness.Run failed for case %s", c.Name)
	return []CaseResult{result}
}

// requireNoUnexpectedDiff asserts that a CaseResult has no unexpected diffs.
func requireNoUnexpectedDiff(t *testing.T, result CaseResult) {
	t.Helper()
	require.False(t, HasUnexpectedDiff(result),
		"unexpected diffs in %s: %+v", result.Name, result.Diffs)
}

// uniqueSessionKey returns a unique session key using the test name.
func uniqueSessionKey(t *testing.T, suffix string) session.Key {
	t.Helper()
	return session.Key{
		AppName:   "replay-test",
		UserID:    "user",
		SessionID: fmt.Sprintf("%s-%s", t.Name(), suffix),
	}
}

// deterministicSummarizer produces deterministic summaries from event count.
type deterministicSummarizer struct{}

func (d *deterministicSummarizer) ShouldSummarize(*session.Session) bool { return true }
func (d *deterministicSummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	if sess == nil {
		return "", nil
	}
	return fmt.Sprintf("summary-of-%d-events", len(sess.Events)), nil
}
func (d *deterministicSummarizer) SetPrompt(string)         {}
func (d *deterministicSummarizer) SetModel(any)             {}
func (d *deterministicSummarizer) Metadata() map[string]any { return nil }

// injectDrift mutates a snapshot by modifying a value at the given path.
func injectDrift(snap *Snapshot, section string, mutate func(any) any) {
	switch section {
	case "events":
		if len(snap.Events) > 0 {
			for k, v := range snap.Events[0] {
				snap.Events[0][k] = mutate(v)
				return
			}
		}
	case "state":
		for k, v := range snap.State {
			snap.State[k] = mutate(v)
			return
		}
	case "memories":
		if len(snap.Memories) > 0 {
			snap.Memories[0].Content = fmt.Sprintf("%s-drifted", snap.Memories[0].Content)
		}
	case "summaries":
		for k, s := range snap.Summaries {
			s.Text = fmt.Sprintf("%s-drifted", s.Text)
			snap.Summaries[k] = s
			return
		}
	case "tracks":
		for k, events := range snap.Tracks {
			if len(events) > 0 {
				events[0].Track = fmt.Sprintf("%s-drifted", events[0].Track)
				snap.Tracks[k] = events
				return
			}
		}
	}
}

// driftMutators returns per-section mutation functions for drift injection tests.
func driftMutators() map[string]func(any) any {
	return map[string]func(any) any{
		"events": func(v any) any { return fmt.Sprintf("%v-drifted", v) },
		"state":  func(v any) any { return fmt.Sprintf("%v-drifted", v) },
	}
}

// countOps returns the total number of operations in a Case.
func countOps(c Case) int {
	n := len(c.Ops)
	for _, group := range c.ParallelGroups {
		n += len(group)
	}
	return n
}

// newEventWithStateDeltaNull creates an event where StateDelta has nil values.
func newEventWithStateDeltaNull(content string, keys ...string) *event.Event {
	delta := make(map[string][]byte, len(keys))
	for _, k := range keys {
		delta[k] = nil
	}
	return newAssistantEventWithStateDelta(content, delta)
}

// newToolCallEventWithInvocation creates a tool call event with a specific invocation ID.
func newToolCallEventWithInvocation(invID, toolName, argsJSON, toolCallID string) *event.Event {
	e := newToolCallEvent(toolName, argsJSON, toolCallID)
	e.InvocationID = invID
	return e
}

// newToolResponseEventWithInvocation creates a tool response with a specific invocation ID.
func newToolResponseEventWithInvocation(invID, toolCallID, toolName, resultJSON string) *event.Event {
	e := newToolResponseEvent(toolCallID, toolName, resultJSON)
	e.InvocationID = invID
	return e
}

// newTrackEventWithVolatile creates a track event with volatile payload keys.
func newTrackEventWithVolatile(track string, payload map[string]any) *session.TrackEvent {
	b, _ := json.Marshal(payload)
	return &session.TrackEvent{
		Track:     session.Track(track),
		Payload:   json.RawMessage(b),
		Timestamp: time.Now(),
	}
}
