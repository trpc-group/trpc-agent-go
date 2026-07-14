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
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// stepBaseTime is the reference time for event timestamps.
var stepBaseTime = time.Now().UTC().Truncate(time.Second)

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
			sess, err := backend.SessionService.CreateSession(
				ctx, key, stateMapFromJSON(step.State),
			)
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			if sess == nil {
				t.Fatal("created session is nil")
			}

		case StepAppendEvent:
			sess := mustGetSession(t, ctx, backend, key)
			evt := buildEvent(step.Event, stepIdx)
			if err := backend.SessionService.AppendEvent(ctx, sess, evt); err != nil {
				t.Fatalf("append event step %d: %v", stepIdx, err)
			}

		case StepUpdateAppState:
			if err := backend.SessionService.UpdateAppState(
				ctx, key.AppName, stateMapFromJSON(step.State),
			); err != nil {
				t.Fatalf("update app state: %v", err)
			}

		case StepUpdateUserState:
			uk := session.UserKey{AppName: key.AppName, UserID: key.UserID}
			if err := backend.SessionService.UpdateUserState(
				ctx, uk, stateMapFromJSON(step.State),
			); err != nil {
				t.Fatalf("update user state: %v", err)
			}

		case StepUpdateSessionState:
			if err := backend.SessionService.UpdateSessionState(
				ctx, key, stateMapFromJSON(step.State),
			); err != nil {
				t.Fatalf("update session state: %v", err)
			}

		case StepAddMemory, StepUpdateMemory, StepDeleteMemory:
			applyMemoryOp(t, ctx, backend.MemoryService, memKey, aliases, step.Memory)

		case StepCreateSummary:
			sess := mustGetSession(t, ctx, backend, key)
			backend.Summarizer.SetText(step.Summary.Text)
			if err := backend.SessionService.CreateSessionSummary(
				ctx, sess, step.Summary.FilterKey, step.Summary.Force,
			); err != nil {
				t.Fatalf("create session summary: %v", err)
			}

		case StepAppendTrack:
			sess := mustGetSession(t, ctx, backend, key)
			payload, err := json.Marshal(step.Track.Payload)
			if err != nil {
				t.Fatalf("marshal track payload: %v", err)
			}
			if err := backend.TrackService.AppendTrackEvent(ctx, sess,
				&session.TrackEvent{
					Track:     session.Track(step.Track.Name),
					Payload:   payload,
					Timestamp: stepBaseTime.Add(time.Duration(stepIdx) * time.Second),
				},
			); err != nil {
				t.Fatalf("append track event: %v", err)
			}

		case StepConcurrentEvents:
			runConcurrentSteps(t, ctx, backend, key, memKey, aliases, step.Concurrent)

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
	return &ReplayResult{
		Backend:  backend.Name,
		Key:      key,
		Snapshot: CaptureSnapshot(backend.Name, sess, memories),
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

func buildEvent(ev *actionEvent, stepIndex int) *event.Event {
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

	e := &event.Event{
		Response: &model.Response{
			Object:    obj,
			Done:      true,
			Timestamp: stepBaseTime.Add(time.Duration(stepIndex) * time.Second),
			Choices:   []model.Choice{{Index: 0, Message: msg}},
		},
		Author:    ev.Author,
		Branch:    ev.Branch,
		FilterKey: ev.FilterKey,
		Tag:       ev.Tag,
		Version:   event.CurrentVersion,
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

func applyMemoryOp(
	t testing.TB, ctx context.Context, svc memory.Service,
	userKey memory.UserKey, aliases map[string]string, a *actionMemory,
) {
	switch a.Op {
	case "add":
		var opts []memory.AddOption
		if a.Meta != nil {
			opts = append(opts, memory.WithMetadata(buildMemoryMeta(a.Meta)))
		}
		if err := svc.AddMemory(ctx, userKey, a.Content, copyStrings(a.Topics), opts...); err != nil {
			t.Fatalf("add memory %q: %v", a.Content, err)
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
			t.Fatalf("memory alias %q not found", a.Ref)
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
			t.Fatalf("update memory %q: %v", a.Content, err)
		}
		if a.ResultAlias != "" {
			aliases[a.ResultAlias] = result.MemoryID
		}
	case "delete":
		memID, ok := aliases[a.Ref]
		if !ok {
			t.Fatalf("memory alias %q not found", a.Ref)
		}
		if err := svc.DeleteMemory(ctx, memory.Key{
			AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: memID,
		}); err != nil {
			t.Fatalf("delete memory %q: %v", a.Content, err)
		}
	default:
		t.Fatalf("unknown memory op %q", a.Op)
	}
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

func copyStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s...)
}

func runConcurrentSteps(
	t testing.TB, ctx context.Context, backend *ReplayBackend,
	key session.Key, memKey memory.UserKey, aliases map[string]string,
	steps []ReplayStep,
) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	errCh := make(chan error, len(steps))

	for i := range steps {
		wg.Add(1)
		go func(step ReplayStep, idx int) {
			defer wg.Done()
			mu.Lock()
			_ = aliases // shared alias map
			mu.Unlock()

			if step.Type == StepAppendEvent {
				sess := mustGetSession(t, ctx, backend, key)
				evt := buildEvent(step.Event, idx)
				if err := backend.SessionService.AppendEvent(ctx, sess, evt); err != nil {
					errCh <- fmt.Errorf("concurrent append event: %w", err)
				}
			}
		}(steps[i], i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Errorf("concurrent step error: %v", err)
		}
	}
}
