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
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// faultSessionService wraps a session.Service to inject transient
// faults for error-recovery testing.  The driver sets nextFault before
// each step; the wrapper consumes it on the next method call and
// then clears it so subsequent calls proceed normally.
//
// This wrapper is NOT safe for concurrent use.  It is only used for
// sequential step execution in RunReplayCase, not inside runConcurrentSteps.
type faultSessionService struct {
	session.Service
	nextFault *FaultConfig
}

func (s *faultSessionService) CreateSession(
	ctx context.Context, key session.Key, state session.StateMap, opts ...session.Option,
) (*session.Session, error) {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return nil, fmt.Errorf("fault injected: fail before CreateSession")
	}
	sess, err := s.Service.CreateSession(ctx, key, state, opts...)
	if err != nil {
		return sess, err
	}
	if cfg != nil && cfg.FailAfter {
		return sess, fmt.Errorf("fault injected: fail after CreateSession")
	}
	return sess, nil
}

func (s *faultSessionService) AppendEvent(
	ctx context.Context, sess *session.Session, evt *event.Event, opts ...session.Option,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before AppendEvent")
	}
	err := s.Service.AppendEvent(ctx, sess, evt, opts...)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after AppendEvent")
	}
	return nil
}

func (s *faultSessionService) UpdateAppState(
	ctx context.Context, appName string, state session.StateMap,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before UpdateAppState")
	}
	err := s.Service.UpdateAppState(ctx, appName, state)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after UpdateAppState")
	}
	return nil
}

func (s *faultSessionService) UpdateUserState(
	ctx context.Context, userKey session.UserKey, state session.StateMap,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before UpdateUserState")
	}
	err := s.Service.UpdateUserState(ctx, userKey, state)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after UpdateUserState")
	}
	return nil
}

func (s *faultSessionService) UpdateSessionState(
	ctx context.Context, key session.Key, state session.StateMap,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before UpdateSessionState")
	}
	err := s.Service.UpdateSessionState(ctx, key, state)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after UpdateSessionState")
	}
	return nil
}

func (s *faultSessionService) CreateSessionSummary(
	ctx context.Context, sess *session.Session, filterKey string, force bool,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before CreateSessionSummary")
	}
	err := s.Service.CreateSessionSummary(ctx, sess, filterKey, force)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after CreateSessionSummary")
	}
	return nil
}

// faultTrackService wraps a session.TrackService to inject transient
// faults for error-recovery testing.  It follows the same nextFault
// pattern as faultSessionService.
type faultTrackService struct {
	session.TrackService
	nextFault *FaultConfig
}

func (s *faultTrackService) AppendTrackEvent(
	ctx context.Context, sess *session.Session, event *session.TrackEvent, opts ...session.Option,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before AppendTrackEvent")
	}
	err := s.TrackService.AppendTrackEvent(ctx, sess, event, opts...)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after AppendTrackEvent")
	}
	return nil
}

// faultMemoryService wraps a memory.Service to inject transient
// faults for error-recovery testing.  It follows the same nextFault
// pattern as faultSessionService.
type faultMemoryService struct {
	memory.Service
	nextFault *FaultConfig
}

func (s *faultMemoryService) AddMemory(
	ctx context.Context, userKey memory.UserKey, mem string,
	topics []string, opts ...memory.AddOption,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before AddMemory")
	}
	err := s.Service.AddMemory(ctx, userKey, mem, topics, opts...)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after AddMemory")
	}
	return nil
}

func (s *faultMemoryService) UpdateMemory(
	ctx context.Context, memoryKey memory.Key, memory string,
	topics []string, opts ...memory.UpdateOption,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before UpdateMemory")
	}
	err := s.Service.UpdateMemory(ctx, memoryKey, memory, topics, opts...)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after UpdateMemory")
	}
	return nil
}

func (s *faultMemoryService) DeleteMemory(
	ctx context.Context, memoryKey memory.Key,
) error {
	cfg := s.nextFault
	s.nextFault = nil
	if cfg != nil && cfg.FailBefore {
		return fmt.Errorf("fault injected: fail before DeleteMemory")
	}
	err := s.Service.DeleteMemory(ctx, memoryKey)
	if err != nil {
		return err
	}
	if cfg != nil && cfg.FailAfter {
		return fmt.Errorf("fault injected: fail after DeleteMemory")
	}
	return nil
}

// ReplayResult holds the output of running a ReplayCase against one backend.
type ReplayResult struct {
	Backend  string
	Key      session.Key
	Snapshot *ReplaySnapshot
}

// RunReplayCase executes a ReplayCase against a single backend.
func RunReplayCase(
	t testing.TB,
	ctx context.Context,
	backend *ReplayBackend,
	rc *ReplayCase,
) *ReplayResult {
	// Capture the reference time now so event timestamps post-date
	// session creation (which uses wall-clock time.Now() inside the
	// first step).  This prevents SQLite's getSummariesList from
	// discarding summaries.
	baseTime := rc.BaseTime
	if baseTime.IsZero() {
		baseTime = time.Now().UTC().Truncate(time.Second)
	}

	// Scan steps for fault-injection configs. If any step uses faults,
	// wrap the backend services so faults fire transparently during the
	// targeted operation.  All three wrappers share independent
	// nextFault pointers; the driver sets the relevant one before each
	// step.
	if hasFaults(rc.Steps) {
		backend.SessionService = &faultSessionService{
			Service: backend.SessionService,
		}
		backend.TrackService = &faultTrackService{
			TrackService: backend.TrackService,
		}
		backend.MemoryService = &faultMemoryService{
			Service: backend.MemoryService,
		}
	}

	key := session.Key{
		AppName:   rc.AppName,
		UserID:    rc.UserID,
		SessionID: rc.SessionID,
	}
	memKey := memory.UserKey{AppName: rc.AppName, UserID: rc.UserID}
	aliases := make(map[string]string)
	stepIdx := 0

	for _, step := range rc.Steps {
		switch step.Type {
		case StepCreateSession:
			if step.Fault != nil {
				if fw, ok := backend.SessionService.(*faultSessionService); ok {
					fw.nextFault = step.Fault
				}
			}
			sess, err := backend.SessionService.CreateSession(
				ctx, key, stateMapFromJSON(step.State),
			)
			if err != nil {
				if step.Fault != nil {
					t.Logf("step %d: expected fault on create session: %v", stepIdx, err)
					stepIdx++
					continue
				}
				t.Fatalf("create session: %v", err)
			}
			if sess == nil {
				t.Fatal("created session is nil")
			}

		case StepAppendEvent:
			if step.Fault != nil {
				if fw, ok := backend.SessionService.(*faultSessionService); ok {
					fw.nextFault = step.Fault
				}
			}
			sess := mustGetSession(t, ctx, backend, key)
			evt := buildEvent(step.Event, stepIdx, baseTime)
			if err := backend.SessionService.AppendEvent(ctx, sess, evt); err != nil {
				if step.Fault != nil {
					t.Logf("step %d: expected fault: %v", stepIdx, err)
					stepIdx++
					continue
				}
				t.Fatalf("append event step %d: %v", stepIdx, err)
			}

		case StepUpdateAppState:
			if step.Fault != nil {
				if fw, ok := backend.SessionService.(*faultSessionService); ok {
					fw.nextFault = step.Fault
				}
			}
			if err := backend.SessionService.UpdateAppState(
				ctx, key.AppName, stateMapFromJSON(step.State),
			); err != nil {
				if step.Fault != nil {
					t.Logf("step %d: expected fault: %v", stepIdx, err)
					stepIdx++
					continue
				}
				t.Fatalf("update app state: %v", err)
			}

		case StepUpdateUserState:
			if step.Fault != nil {
				if fw, ok := backend.SessionService.(*faultSessionService); ok {
					fw.nextFault = step.Fault
				}
			}
			uk := session.UserKey{AppName: key.AppName, UserID: key.UserID}
			if err := backend.SessionService.UpdateUserState(
				ctx, uk, stateMapFromJSON(step.State),
			); err != nil {
				if step.Fault != nil {
					t.Logf("step %d: expected fault: %v", stepIdx, err)
					stepIdx++
					continue
				}
				t.Fatalf("update user state: %v", err)
			}

		case StepUpdateSessionState:
			if step.Fault != nil {
				if fw, ok := backend.SessionService.(*faultSessionService); ok {
					fw.nextFault = step.Fault
				}
			}
			if err := backend.SessionService.UpdateSessionState(
				ctx, key, stateMapFromJSON(step.State),
			); err != nil {
				if step.Fault != nil {
					t.Logf("step %d: expected fault: %v", stepIdx, err)
					stepIdx++
					continue
				}
				t.Fatalf("update session state: %v", err)
			}

		case StepDeleteAppState:
			for k := range step.State {
				if err := backend.SessionService.DeleteAppState(
					ctx, key.AppName, k,
				); err != nil {
					t.Fatalf("delete app state key %q: %v", k, err)
				}
			}

		case StepDeleteUserState:
			uk := session.UserKey{AppName: key.AppName, UserID: key.UserID}
			for k := range step.State {
				if err := backend.SessionService.DeleteUserState(
					ctx, uk, k,
				); err != nil {
					t.Fatalf("delete user state key %q: %v", k, err)
				}
			}

		case StepAddMemory, StepUpdateMemory, StepDeleteMemory:
			if step.Fault != nil {
				if fw, ok := backend.MemoryService.(*faultMemoryService); ok {
					fw.nextFault = step.Fault
				}
			}
			if err := applyMemoryOp(
				ctx, backend.MemoryService, memKey, aliases, step.Memory,
			); err != nil {
				if step.Fault != nil {
					t.Logf("step %d: expected fault: %v", stepIdx, err)
					stepIdx++
					continue
				}
				t.Fatalf("memory op step %d: %v", stepIdx, err)
			}

		case StepCreateSummary:
			if step.Fault != nil {
				if fw, ok := backend.SessionService.(*faultSessionService); ok {
					fw.nextFault = step.Fault
				}
			}
			sess := mustGetSession(t, ctx, backend, key)
			backend.Summarizer.SetText(step.Summary.Text)
			if err := backend.SessionService.CreateSessionSummary(
				ctx, sess, step.Summary.FilterKey, step.Summary.Force,
			); err != nil {
				if step.Fault != nil {
					t.Logf("step %d: expected fault: %v", stepIdx, err)
					stepIdx++
					continue
				}
				t.Fatalf("create session summary: %v", err)
			}

		case StepAppendTrack:
			if step.Fault != nil {
				if fw, ok := backend.TrackService.(*faultTrackService); ok {
					fw.nextFault = step.Fault
				}
			}
			sess := mustGetSession(t, ctx, backend, key)
			payload, err := json.Marshal(step.Track.Payload)
			if err != nil {
				t.Fatalf("marshal track payload: %v", err)
			}
			if err := backend.TrackService.AppendTrackEvent(ctx, sess,
				&session.TrackEvent{
					Track:     session.Track(step.Track.Name),
					Payload:   payload,
					Timestamp: baseTime.Add(time.Duration(stepIdx) * time.Second),
				},
			); err != nil {
				if step.Fault != nil {
					t.Logf("step %d: expected fault: %v", stepIdx, err)
					stepIdx++
					continue
				}
				t.Fatalf("append track event: %v", err)
			}

		case StepConcurrentEvents:
			runConcurrentSteps(t, ctx, backend, key, memKey, aliases, step.Concurrent, baseTime)

		case StepGetSession:
			// snapshot point — captured at end of scenario.
		}
		stepIdx++
	}

	sess := mustGetSession(t, ctx, backend, key)
	memories, err := backend.MemoryService.ReadMemories(ctx, memKey, 0)
	if err != nil {
		t.Fatalf("read memories: %v", err)
	}
	snap := CaptureSnapshot(backend.Name, sess, memories)

	// When events order is intentionally non-deterministic (e.g.
	// concurrent writes), sort the normalised events so that
	// comparison is based on content identity rather than insertion
	// order.  This replaces broad allowed_diff wildcards with
	// precise per-field detection.
	if rc.Verify != nil && rc.Verify.EventsOrderIndependent {
		sort.Slice(snap.Events, func(i, j int) bool {
			a, _ := json.Marshal(snap.Events[i])
			b, _ := json.Marshal(snap.Events[j])
			return string(a) < string(b)
		})
	}

	return &ReplayResult{
		Backend:  backend.Name,
		Key:      key,
		Snapshot: snap,
	}
}

func mustGetSession(
	t testing.TB, ctx context.Context, backend *ReplayBackend, key session.Key,
) *session.Session {
	sess, err := backend.SessionService.GetSession(ctx, key)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if sess == nil {
		t.Fatal("session not found")
	}
	return sess
}

func buildEvent(ev *actionEvent, stepIndex int, baseTime time.Time) *event.Event {
	msg := model.Message{
		Role:    model.Role(ev.Role),
		Content: ev.Content,
	}
	for _, tc := range ev.ToolCalls {
		args, _ := json.Marshal(tc.Arguments)
		msg.ToolCalls = append(msg.ToolCalls, model.ToolCall{
			Type: "function",
			ID:   tc.ID,
			Function: model.FunctionDefinitionParam{
				Name: tc.Name, Arguments: args,
			},
		})
	}
	if ev.ToolID != "" {
		msg.ToolID = ev.ToolID
		msg.ToolName = ev.ToolName
	}

	obj := model.ObjectTypeChatCompletion
	if ev.Role == string(model.RoleTool) {
		obj = model.ObjectTypeToolResponse
	}

	ts := baseTime.Add(time.Duration(stepIndex) * time.Second)
	resp := &model.Response{
		Object:    obj,
		Done:      true,
		Timestamp: ts,
		Choices:   []model.Choice{{Index: 0, Message: msg}},
	}
	e := &event.Event{
		Response:  resp,
		Timestamp: ts,
		Author:    ev.Author,
		Branch:    ev.Branch,
		FilterKey: ev.FilterKey,
		Tag:       ev.Tag,
		Version:   event.CurrentVersion,
	}
	if ev.ID != "" {
		e.ID = ev.ID
		resp.ID = ev.ID
	}
	if e.FilterKey == "" {
		e.FilterKey = e.Branch
	}
	if len(ev.StateDelta) > 0 {
		e.StateDelta = stateMapFromJSON(ev.StateDelta)
	}
	for k, v := range ev.Extensions {
		if err := event.SetExtension(e, k, v); err != nil {
			panic(fmt.Sprintf("set extension %s: %v", k, err))
		}
	}
	if ev.Actions != nil {
		e.Actions = &event.EventActions{
			SkipSummarization: ev.Actions.SkipSummarization,
		}
	}
	return e
}

func stateMapFromJSON(raw map[string]any) session.StateMap {
	if raw == nil {
		return nil
	}
	out := make(session.StateMap, len(raw))
	for k, v := range raw {
		encoded, err := json.Marshal(v)
		if err != nil {
			panic(fmt.Sprintf("marshal state key %s: %v", k, err))
		}
		out[k] = encoded
	}
	return out
}

// applyMemoryOp executes a single memory operation (add / update / delete).
// It returns an error on failure so the caller can handle fault tolerance.
func applyMemoryOp(
	ctx context.Context, svc memory.Service,
	userKey memory.UserKey, aliases map[string]string, a *actionMemory,
) error {
	switch a.Op {
	case "add":
		var opts []memory.AddOption
		if a.Meta != nil {
			opts = append(opts, memory.WithMetadata(buildMemoryMeta(a.Meta)))
		}
		if err := svc.AddMemory(ctx, userKey, a.Content, copyStrings(a.Topics), opts...); err != nil {
			return fmt.Errorf("add memory %q: %w", a.Content, err)
		}
		if a.ResultAlias != "" {
			entries, _ := svc.ReadMemories(ctx, userKey, 0)
			for _, e := range entries {
				if e != nil && e.Memory != nil && e.Memory.Memory == a.Content {
					aliases[a.ResultAlias] = e.ID
					break
				}
			}
		}
	case "update":
		memID, ok := aliases[a.Ref]
		if !ok {
			return fmt.Errorf("memory alias %q not found", a.Ref)
		}
		var opts []memory.UpdateOption
		if a.Meta != nil {
			opts = append(opts, memory.WithUpdateMetadata(buildMemoryMeta(a.Meta)))
		}
		result := &memory.UpdateResult{}
		opts = append(opts, memory.WithUpdateResult(result))
		if err := svc.UpdateMemory(ctx, memory.Key{
			AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: memID,
		}, a.Content, copyStrings(a.Topics), opts...); err != nil {
			return fmt.Errorf("update memory %q: %w", a.Content, err)
		}
		if a.ResultAlias != "" {
			aliases[a.ResultAlias] = result.MemoryID
		}
	case "delete":
		memID, ok := aliases[a.Ref]
		if !ok {
			return fmt.Errorf("memory alias %q not found", a.Ref)
		}
		if err := svc.DeleteMemory(ctx, memory.Key{
			AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: memID,
		}); err != nil {
			return fmt.Errorf("delete memory %q: %w", a.Content, err)
		}
	default:
		return fmt.Errorf("unknown memory op %q", a.Op)
	}
	return nil
}

func buildMemoryMeta(m *memoryMeta) *memory.Metadata {
	md := &memory.Metadata{
		Kind:         memory.Kind(m.Kind),
		Participants: copyStrings(m.Participants),
		Location:     m.Location,
	}
	if m.EventTime != "" {
		tm, err := time.Parse(time.RFC3339, m.EventTime)
		if err == nil {
			md.EventTime = &tm
		}
	}
	return md
}

// hasFaults reports whether any step (including nested concurrent steps)
// has a fault-injection configuration.
func hasFaults(steps []ReplayStep) bool {
	for _, step := range steps {
		if step.Fault != nil {
			return true
		}
		for _, nested := range step.Concurrent {
			if nested.Fault != nil {
				return true
			}
		}
	}
	return false
}

func copyStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
}

func runConcurrentSteps(
	t testing.TB, ctx context.Context, backend *ReplayBackend,
	key session.Key, memKey memory.UserKey, aliases map[string]string,
	steps []ReplayStep, baseTime time.Time,
) {
	// Pre-build all events outside the critical section so goroutines
	// can interleave during construction.  All concurrent events share
	// the same base timestamp — they represent logically simultaneous
	// operations.
	type prebuilt struct {
		step ReplayStep
		evt  *event.Event // non-nil only for StepAppendEvent
	}
	pre := make([]prebuilt, len(steps))
	for i, step := range steps {
		pre[i].step = step
		if step.Type == StepAppendEvent {
			pre[i].evt = buildEvent(step.Event, 0, baseTime)
		}
	}

	// Two-phase barrier: all goroutines report ready, then all are
	// released simultaneously so AppendEvent calls genuinely race.
	var ready sync.WaitGroup
	var start sync.WaitGroup
	ready.Add(len(steps))
	start.Add(1)

	// Narrow mutex only for aliases map access (Go maps are not
	// safe for concurrent read+write).  Backend calls are intentionally
	// outside this lock — they run truly concurrently.
	var aliasesMu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, len(steps))

	for i := range pre {
		wg.Add(1)
		go func(p prebuilt) {
			defer wg.Done()

			// Phase 1: signal ready, then block until release.
			ready.Done()
			start.Wait()

			switch p.step.Type {
			case StepAppendEvent:
				// No external lock — backend must handle
				// concurrent GetSession + AppendEvent safely.
				sess, err := backend.SessionService.GetSession(ctx, key)
				if err != nil {
					errCh <- fmt.Errorf("concurrent get session: %w", err)
					return
				}
				if sess == nil {
					errCh <- fmt.Errorf("concurrent: session not found")
					return
				}
				err = backend.SessionService.AppendEvent(ctx, sess, p.evt)
				if err != nil {
					errCh <- fmt.Errorf("concurrent append event: %w", err)
				}

			case StepAddMemory, StepUpdateMemory, StepDeleteMemory:
				err := applyMemoryOpSafe(
					ctx, backend.MemoryService, memKey,
					&aliasesMu, aliases, p.step.Memory,
				)
				if err != nil {
					errCh <- err
				}

			default:
				errCh <- fmt.Errorf(
					"unsupported concurrent step type: %s", p.step.Type,
				)
			}
		}(pre[i])
	}

	// Wait until every goroutine is at start.Wait(), then release.
	ready.Wait()
	start.Done()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Errorf("concurrent step error: %v", err)
		}
	}
}

// applyMemoryOpSafe is like applyMemoryOp but returns an error instead of
// calling t.Fatalf, making it safe to use from goroutines.
// aliasesMu guards only the aliases map access; backend calls run outside
// the lock so concurrent memory operations genuinely race.
func applyMemoryOpSafe(
	ctx context.Context, svc memory.Service,
	userKey memory.UserKey, aliasesMu *sync.Mutex, aliases map[string]string,
	a *actionMemory,
) error {
	switch a.Op {
	case "add":
		var opts []memory.AddOption
		if a.Meta != nil {
			opts = append(opts, memory.WithMetadata(buildMemoryMeta(a.Meta)))
		}
		if err := svc.AddMemory(
			ctx, userKey, a.Content, copyStrings(a.Topics), opts...,
		); err != nil {
			return fmt.Errorf("add memory %q: %w", a.Content, err)
		}
		if a.ResultAlias != "" {
			entries, _ := svc.ReadMemories(ctx, userKey, 0)
			aliasesMu.Lock()
			for _, e := range entries {
				if e != nil && e.Memory != nil &&
					e.Memory.Memory == a.Content {
					aliases[a.ResultAlias] = e.ID
					break
				}
			}
			aliasesMu.Unlock()
		}
	case "update":
		aliasesMu.Lock()
		memID, ok := aliases[a.Ref]
		aliasesMu.Unlock()
		if !ok {
			return fmt.Errorf("memory alias %q not found", a.Ref)
		}
		var opts []memory.UpdateOption
		if a.Meta != nil {
			opts = append(
				opts,
				memory.WithUpdateMetadata(buildMemoryMeta(a.Meta)),
			)
		}
		result := &memory.UpdateResult{}
		opts = append(opts, memory.WithUpdateResult(result))
		if err := svc.UpdateMemory(
			ctx,
			memory.Key{
				AppName: userKey.AppName, UserID: userKey.UserID,
				MemoryID: memID,
			},
			a.Content, copyStrings(a.Topics), opts...,
		); err != nil {
			return fmt.Errorf("update memory %q: %w", a.Content, err)
		}
		if a.ResultAlias != "" {
			aliasesMu.Lock()
			aliases[a.ResultAlias] = result.MemoryID
			aliasesMu.Unlock()
		}
	case "delete":
		aliasesMu.Lock()
		memID, ok := aliases[a.Ref]
		aliasesMu.Unlock()
		if !ok {
			return fmt.Errorf("memory alias %q not found", a.Ref)
		}
		if err := svc.DeleteMemory(
			ctx,
			memory.Key{
				AppName: userKey.AppName, UserID: userKey.UserID,
				MemoryID: memID,
			},
		); err != nil {
			return fmt.Errorf("delete memory %q: %w", a.Content, err)
		}
	default:
		return fmt.Errorf("unknown memory op %q", a.Op)
	}
	return nil
}
