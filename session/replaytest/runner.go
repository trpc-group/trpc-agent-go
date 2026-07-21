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
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// CaseAppName is the fixed app name used by all cases.
	CaseAppName = "replay-app"
	// CaseUserID is the fixed user ID used by all cases.
	CaseUserID = "replay-user"

	// errClassInjected marks a runner-injected transient failure.
	errClassInjected = "injected_transient"
	// errClassBackend marks a backend-returned error.
	errClassBackend = "error"
	// errClassNil marks a successful operation.
	errClassNil = "nil"
)

// Runner replays cases against targets and captures snapshots.
type Runner struct {
	// PollTimeout bounds the read-back sync point per session. Backends
	// with asynchronous persistence must become visible within this budget;
	// a shortfall is surfaced as missing-event diffs, not as a runner error.
	PollTimeout time.Duration
	// PollInterval is the poll sleep between read-back attempts.
	PollInterval time.Duration
}

// NewRunner returns a Runner with sane defaults.
func NewRunner() *Runner {
	return &Runner{PollTimeout: 5 * time.Second, PollInterval: 10 * time.Millisecond}
}

// runState accumulates per-case execution state.
type runState struct {
	sessions map[string]*session.Session
	created  map[string]bool
	expected map[string]int
	errs     []ErrorRecord
	// memUsers tracks every user ID touched by memory steps so the snapshot
	// reads each scope back separately. It always contains CaseUserID.
	memUsers map[string]bool
	seq      int
	// baseTime anchors the deterministic event clock for this run. It is
	// re-anchored to the wall clock at the start of every run so that
	// derived artifacts (e.g. summary UpdatedAt) stay after the session
	// creation time; timestamp values themselves are normalized away, so
	// determinism of the comparison is unaffected.
	baseTime time.Time
}

// RunCase resets the target, replays the case and captures a snapshot.
// When the target lacks required capabilities, the returned snapshot only
// carries the Unsupported list.
func (r *Runner) RunCase(ctx context.Context, c Case, t Target) (*Snapshot, error) {
	if err := t.Reset(ctx); err != nil {
		return nil, fmt.Errorf("reset target %s: %w", t.Name(), err)
	}
	snap := &Snapshot{Backend: t.Name(), Case: c.Name, SearchQuery: c.SearchQuery}
	if missing := t.Caps().Missing(c.NeedCaps); missing != (Capability{}) {
		snap.Unsupported = missing.Names()
		return snap, nil
	}
	// A target that claims a capability must also expose the service for it;
	// check explicitly instead of relying on NeedCaps to avoid a nil panic.
	if c.NeedCaps.Session && t.SessionService() == nil {
		return nil, fmt.Errorf("target %s claims session capability but SessionService is nil", t.Name())
	}
	if (c.NeedCaps.Memory || c.NeedCaps.MemorySearch) && t.MemoryService() == nil {
		return nil, fmt.Errorf("target %s claims memory capability but MemoryService is nil", t.Name())
	}
	if c.FloatDelta < 0 || math.IsNaN(c.FloatDelta) || math.IsInf(c.FloatDelta, 0) {
		return nil, fmt.Errorf("case %s: float delta must be finite and non-negative", c.Name)
	}

	rs := &runState{
		sessions: make(map[string]*session.Session),
		created:  make(map[string]bool),
		expected: make(map[string]int),
		memUsers: map[string]bool{CaseUserID: true},
		baseTime: time.Now().UTC(),
	}
	for i, st := range c.Steps {
		if err := r.applyStep(ctx, i, c, t, rs, st); err != nil {
			return nil, fmt.Errorf("case %s step %d (%s): %w", c.Name, i, st.Op, err)
		}
	}
	r.syncPoint(ctx, t, rs)
	if c.NeedCaps.Summary {
		if err := verifySummaryIsolation(ctx, t, c.Name); err != nil {
			return nil, fmt.Errorf("case %s: %w", c.Name, err)
		}
	}
	if err := takeSnapshot(ctx, t, c, rs, snap); err != nil {
		return nil, fmt.Errorf("case %s snapshot: %w", c.Name, err)
	}
	return snap, nil
}

// probeSummarySuffix marks the session ID of the summary-isolation probe:
// the runner creates one fresh probe session per summary case, so a backend
// that leaks summaries across sessions is caught by the probe read-back.
const probeSummarySuffix = "-summary-isolation"

// verifySummaryIsolation creates a fresh session after all case steps and
// asserts it contains no summaries. A backend that stores summaries at the
// wrong scope (e.g. keyed by app/user instead of by session) leaks them
// into every session read afterwards; the probe catches that at the service
// boundary, where a snapshot diff could not tell leak from absent write.
func verifySummaryIsolation(ctx context.Context, t Target, caseName string) error {
	svc := t.SessionService()
	probeKey := session.Key{
		AppName:   CaseAppName,
		UserID:    CaseUserID,
		SessionID: strings.ReplaceAll(caseName, "/", "-") + probeSummarySuffix,
	}
	probe, err := svc.CreateSession(ctx, probeKey, nil)
	if err != nil {
		return fmt.Errorf("create summary-isolation probe session: %w", err)
	}
	if probe == nil {
		return fmt.Errorf("create summary-isolation probe session: backend returned nil session")
	}
	probe, err = svc.GetSession(ctx, probeKey)
	if err != nil {
		return fmt.Errorf("get summary-isolation probe session: %w", err)
	}
	if probe == nil {
		return fmt.Errorf("get summary-isolation probe session: backend returned nil session")
	}
	probe.SummariesMu.RLock()
	leaked := len(probe.Summaries)
	probe.SummariesMu.RUnlock()
	if leaked != 0 {
		return fmt.Errorf("fresh probe session contains %d summaries (cross-session summary leak)", leaked)
	}
	if err := svc.DeleteSession(ctx, probeKey); err != nil {
		return fmt.Errorf("delete summary-isolation probe session: %w", err)
	}
	return nil
}

// applyStep executes one step.
func (r *Runner) applyStep(
	ctx context.Context,
	idx int,
	c Case,
	t Target,
	rs *runState,
	st Step,
) error {
	switch st.Op {
	case OpCreateSession:
		return r.applyCreateSession(ctx, t, rs, st)
	case OpAppendEvent:
		return r.applyAppendEvent(ctx, idx, t, rs, st)
	case OpUpdateState, OpUpdateAppState, OpDeleteAppState,
		OpUpdateUserState, OpDeleteUserState:
		return applyStateOp(ctx, t, st)
	case OpAddMemory, OpUpdateMemory, OpDeleteMemory, OpClearMemories:
		return applyMemoryOp(ctx, t, rs, st)
	case OpSummary:
		return applySummary(ctx, t, rs, st)
	case OpAppendTrack:
		return r.applyTrack(ctx, t, rs, st)
	case OpConcurrentEvents:
		return r.applyConcurrent(ctx, t.SessionService(), rs, st)
	default:
		return fmt.Errorf("unknown op %q", st.Op)
	}
}

// applyCreateSession handles OpCreateSession.
func (r *Runner) applyCreateSession(
	ctx context.Context,
	t Target,
	rs *runState,
	st Step,
) error {
	skey := session.Key{AppName: CaseAppName, UserID: CaseUserID, SessionID: st.SessionID}
	sess, err := t.SessionService().CreateSession(ctx, skey, toStateMap(st.State))
	if err != nil {
		return err
	}
	rs.sessions[st.SessionID] = sess
	rs.created[st.SessionID] = true
	return nil
}

// applyAppendEvent handles OpAppendEvent, including injected transient
// failures and expected-error probes.
func (r *Runner) applyAppendEvent(
	ctx context.Context,
	idx int,
	t Target,
	rs *runState,
	st Step,
) error {
	evt := r.buildEvent(rs, st.SessionID, st.Event)
	sess := rs.sessions[st.SessionID]
	if sess == nil {
		// Deliberately append to a session this case never created.
		sess = &session.Session{AppName: CaseAppName, UserID: CaseUserID, ID: st.SessionID}
	}
	svc := t.SessionService()
	if st.Event.FailTimes > 0 {
		svc = &transientFailService{Service: svc, fail: st.Event.FailTimes}
	}
	for {
		err := svc.AppendEvent(ctx, sess, evt)
		if st.Event.ExpectError {
			rs.errs = append(rs.errs, ErrorRecord{Step: idx, Class: errorClass(err)})
			return nil
		}
		if st.Event.FailTimes > 0 {
			if err != nil {
				// Transient failure at the service boundary; retry like a
				// real client would. The event must land exactly once.
				rs.errs = append(rs.errs, ErrorRecord{Step: idx, Class: errClassInjected})
				continue
			}
			rs.errs = append(rs.errs, ErrorRecord{Step: idx, Class: errClassNil})
		}
		if err != nil {
			return fmt.Errorf("append event: %w", err)
		}
		break
	}
	rs.expected[st.SessionID]++
	return nil
}

// transientFailService fails the next fail AppendEvent calls with a
// transient error before delegating to the wrapped service, so a case
// exercises the write-fail/retry path through the real service interface.
type transientFailService struct {
	session.Service
	fail int
}

// AppendEvent implements session.Service.
func (s *transientFailService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	evt *event.Event,
	opts ...session.Option,
) error {
	if s.fail > 0 {
		s.fail--
		return fmt.Errorf("replaytest: injected transient failure (%d left)", s.fail)
	}
	return s.Service.AppendEvent(ctx, sess, evt, opts...)
}

// applyStateOp handles the session/app/user state operations.
func applyStateOp(ctx context.Context, t Target, st Step) error {
	svc := t.SessionService()
	skey := session.Key{AppName: CaseAppName, UserID: CaseUserID, SessionID: st.SessionID}
	ukey := session.UserKey{AppName: CaseAppName, UserID: CaseUserID}
	switch st.Op {
	case OpUpdateState:
		return svc.UpdateSessionState(ctx, skey, toStateMap(st.State))
	case OpUpdateAppState:
		return svc.UpdateAppState(ctx, CaseAppName, toStateMap(st.State))
	case OpDeleteAppState:
		for _, k := range st.StateKeys {
			if err := svc.DeleteAppState(ctx, CaseAppName, k); err != nil {
				return err
			}
		}
	case OpUpdateUserState:
		return svc.UpdateUserState(ctx, ukey, toStateMap(st.State))
	case OpDeleteUserState:
		for _, k := range st.StateKeys {
			if err := svc.DeleteUserState(ctx, ukey, k); err != nil {
				return err
			}
		}
	}
	return nil
}

// applyMemoryOp handles the memory operations under the step's user scope
// (CaseUserID unless the spec overrides it) and records the scope for the
// snapshot. A MatchContent miss is a case-authoring bug (all public cases
// match content they added), so it fails the run instead of being recorded
// as a comparable error class.
func applyMemoryOp(ctx context.Context, t Target, rs *runState, st Step) error {
	msvc := t.MemoryService()
	uid := memoryUser(st.Memory)
	rs.memUsers[uid] = true
	ukey := memory.UserKey{AppName: CaseAppName, UserID: uid}
	switch st.Op {
	case OpAddMemory:
		return msvc.AddMemory(ctx, ukey,
			st.Memory.Content, st.Memory.Topics, memoryAddOptions(st.Memory)...)
	case OpUpdateMemory:
		id, err := findMemoryID(ctx, msvc, ukey, st.Memory.MatchContent)
		if err != nil {
			return err
		}
		return msvc.UpdateMemory(ctx,
			memory.Key{AppName: CaseAppName, UserID: uid, MemoryID: id},
			st.Memory.Content, st.Memory.Topics, memoryUpdateOptions(st.Memory)...)
	case OpDeleteMemory:
		id, err := findMemoryID(ctx, msvc, ukey, st.Memory.MatchContent)
		if err != nil {
			return err
		}
		return msvc.DeleteMemory(ctx,
			memory.Key{AppName: CaseAppName, UserID: uid, MemoryID: id})
	case OpClearMemories:
		return msvc.ClearMemories(ctx, ukey)
	}
	return nil
}

// memoryUser resolves the user ID for a memory step: the runner default
// unless the spec overrides it (used by the scope-isolation case).
func memoryUser(spec *MemorySpec) string {
	if spec != nil && spec.UserID != "" {
		return spec.UserID
	}
	return CaseUserID
}

// applySummary handles OpSummary.
func applySummary(ctx context.Context, t Target, rs *runState, st Step) error {
	sess := rs.sessions[st.SessionID]
	if sess == nil {
		return fmt.Errorf("summary on unknown session %q", st.SessionID)
	}
	return t.SessionService().CreateSessionSummary(ctx, sess, st.Summary.FilterKey, true)
}

// applyTrack handles OpAppendTrack.
func (r *Runner) applyTrack(ctx context.Context, t Target, rs *runState, st Step) error {
	sess := rs.sessions[st.SessionID]
	if sess == nil {
		return fmt.Errorf("track on unknown session %q", st.SessionID)
	}
	rs.seq++
	te := &session.TrackEvent{
		Track:     session.Track(st.Track.Track),
		Payload:   json.RawMessage(st.Track.Payload),
		Timestamp: rs.baseTime.Add(time.Duration(rs.seq) * time.Second),
	}
	ts, ok := t.SessionService().(session.TrackService)
	if !ok {
		return fmt.Errorf("session service does not implement session.TrackService")
	}
	return ts.AppendTrackEvent(ctx, sess, te)
}

// applyConcurrent fans out writers behind a start barrier. Event IDs and
// timestamps are pre-assigned to keep the run deterministic. Each writer
// gets its own clone of the session: AppendEvent mutates the caller's
// session (UpdatedAt, in-place event/state merge), so sharing one pointer
// across goroutines would be a caller-side data race, not a backend one.
func (r *Runner) applyConcurrent(
	ctx context.Context,
	svc session.Service,
	rs *runState,
	st Step,
) error {
	sess := rs.sessions[st.SessionID]
	if sess == nil {
		return fmt.Errorf("concurrent append on unknown session %q", st.SessionID)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errCh := make(chan error, len(st.Concurrent))
	for _, w := range st.Concurrent {
		author := w.Author
		if author == "" {
			author = "replay"
		}
		specs := make([]*event.Event, 0, w.Count)
		for i := 0; i < w.Count; i++ {
			rs.seq++
			specs = append(specs, r.buildEvent(rs, st.SessionID, &EventSpec{
				Author:       author,
				Role:         "assistant",
				Content:      fmt.Sprintf("%s-%02d", w.Prefix, i+1),
				Branch:       w.Branch,
				InvocationID: "inv-" + w.Branch,
			}))
			rs.expected[st.SessionID]++
		}
		wg.Add(1)
		go func(events []*event.Event, wsess *session.Session) {
			defer wg.Done()
			<-start
			for _, evt := range events {
				if err := svc.AppendEvent(ctx, wsess, evt); err != nil {
					errCh <- err
					return
				}
			}
		}(specs, sess.Clone())
	}
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		return fmt.Errorf("concurrent append: %w", err)
	}
	return nil
}

// buildEvent constructs a deterministic event from a spec.
func (r *Runner) buildEvent(rs *runState, sessionID string, spec *EventSpec) *event.Event {
	rs.seq++
	id := fmt.Sprintf("evt-%s-%04d", sessionID, rs.seq)
	ts := rs.baseTime.Add(time.Duration(rs.seq) * time.Second)
	msg := model.Message{
		Role:     model.Role(spec.Role),
		Content:  spec.Content,
		ToolID:   spec.ToolID,
		ToolName: spec.ToolName,
	}
	for _, tc := range spec.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			Type: "function",
			ID:   tc.ID,
			Function: model.FunctionDefinitionParam{
				Name:      tc.Name,
				Arguments: []byte(tc.Args),
			},
		})
	}
	choice := model.Choice{Index: 0, Message: msg}
	if spec.FinishReason != "" {
		fr := spec.FinishReason
		choice.FinishReason = &fr
	}
	return &event.Event{
		RequestID:    spec.RequestID,
		InvocationID: spec.InvocationID,
		Author:       spec.Author,
		ID:           id,
		Timestamp:    ts,
		Branch:       spec.Branch,
		Tag:          spec.Tag,
		FilterKey:    spec.FilterKey,
		StateDelta:   toStateMap(spec.StateDelta),
		Extensions:   toExtensions(spec.Extensions),
		Response: &model.Response{
			ID:      "resp-" + id,
			Object:  "chat.completion",
			Created: rs.baseTime.Unix(),
			Model:   "replay-model",
			Choices: []model.Choice{choice},
		},
	}
}

// syncPoint waits until every created session exposes its events. It stops
// when the expected count is reached or the observed count stays stable
// across several polls: read-side filtering may legitimately hide events
// (e.g. leading non-user messages), so an unreached expectation is left
// for the differ to judge rather than treated as a runner failure.
func (r *Runner) syncPoint(ctx context.Context, t Target, rs *runState) {
	svc := t.SessionService()
	if svc == nil {
		return
	}
	const stablePolls = 3
	deadline := time.Now().Add(r.PollTimeout)
	for sid, want := range rs.expected {
		if !rs.created[sid] {
			continue
		}
		key := session.Key{AppName: CaseAppName, UserID: CaseUserID, SessionID: sid}
		last, stable := -1, 0
		for {
			sess, err := svc.GetSession(ctx, key)
			got := -1
			if err == nil && sess != nil {
				got = len(sess.Events)
			}
			if got >= want || stable >= stablePolls {
				break
			}
			if got == last {
				stable++
			} else {
				stable = 0
			}
			last = got
			if time.Now().After(deadline) {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(r.PollInterval):
			}
		}
	}
}

// findMemoryID resolves a memory ID by exact content match within one user
// scope.
func findMemoryID(
	ctx context.Context,
	msvc memory.Service,
	ukey memory.UserKey,
	content string,
) (string, error) {
	entries, err := msvc.ReadMemories(ctx, ukey, 0)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Memory != nil && e.Memory.Memory == content {
			return e.ID, nil
		}
	}
	return "", fmt.Errorf("memory %q not found", content)
}

// errorClass classifies an operation error for cross-backend comparison.
func errorClass(err error) string {
	if err == nil {
		return errClassNil
	}
	return errClassBackend
}

// toStateMap converts raw JSON spec values into a session.StateMap.
// Values that are not valid JSON are encoded as JSON strings.
func toStateMap(spec map[string]string) session.StateMap {
	if spec == nil {
		return nil
	}
	out := make(session.StateMap, len(spec))
	for k, v := range spec {
		out[k] = toRawJSON(v)
	}
	return out
}

// toExtensions converts raw JSON spec values into event extensions.
func toExtensions(spec map[string]string) map[string]json.RawMessage {
	if spec == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(spec))
	for k, v := range spec {
		out[k] = toRawJSON(v)
	}
	return out
}

// toRawJSON returns v as raw JSON, encoding plain strings as JSON strings.
func toRawJSON(v string) json.RawMessage {
	if json.Valid([]byte(v)) {
		return json.RawMessage(v)
	}
	b, _ := json.Marshal(v)
	return b
}

// memoryAddOptions builds AddOptions from a spec.
func memoryAddOptions(spec *MemorySpec) []memory.AddOption {
	if spec.Metadata == nil {
		return nil
	}
	return []memory.AddOption{memory.WithMetadata(toMemoryMetadata(spec.Metadata))}
}

// memoryUpdateOptions builds UpdateOptions from a spec.
func memoryUpdateOptions(spec *MemorySpec) []memory.UpdateOption {
	if spec.Metadata == nil {
		return nil
	}
	return []memory.UpdateOption{memory.WithUpdateMetadata(toMemoryMetadata(spec.Metadata))}
}

// toMemoryMetadata converts a MetadataSpec.
func toMemoryMetadata(spec *MetadataSpec) *memory.Metadata {
	return &memory.Metadata{
		Kind:         memory.Kind(spec.Kind),
		Participants: spec.Participants,
		Location:     spec.Location,
	}
}
