//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	replayApp  = "replaytest"
	replayUser = "user"
)

// StandardCases returns the ten lightweight replay cases used as the backend
// compatibility baseline. They are deterministic and require no credentials or
// external infrastructure.
func StandardCases() []Case {
	return []Case{
		{Name: "single_turn", Replay: replaySingleTurn},
		{Name: "multi_turn", Replay: replayMultiTurn},
		{Name: "tool_call", Replay: replayToolCall},
		{Name: "state_updates", Replay: replayStateUpdates},
		{Name: "memory_read_write", Replay: replayMemoryReadWrite},
		{Name: "summary_update", Replay: replaySummaryUpdate},
		{Name: "summary_with_follow_up_events", Replay: replaySummaryWithFollowUpEvents},
		{Name: "track_events", Replay: replayTrackEvents},
		{Name: "interleaved_writes", Replay: replayInterleavedWrites},
		{Name: "retry_recovery", Replay: replayRetryRecovery},
	}
}

func replaySingleTurn(ctx context.Context, backend Backend) (Snapshot, error) {
	key, sess, err := createCaseSession(ctx, backend, "single_turn")
	if err != nil {
		return Snapshot{}, err
	}
	if err := appendEvents(ctx, backend, sess,
		newReplayEvent("single-user", model.RoleUser, "hello", ""),
		newReplayEvent("single-assistant", model.RoleAssistant, "hello back", ""),
	); err != nil {
		return Snapshot{}, err
	}
	return Capture(ctx, backend, key)
}

func replayMultiTurn(ctx context.Context, backend Backend) (Snapshot, error) {
	key, sess, err := createCaseSession(ctx, backend, "multi_turn")
	if err != nil {
		return Snapshot{}, err
	}
	if err := appendEvents(ctx, backend, sess,
		newReplayEvent("multi-user-1", model.RoleUser, "first question", ""),
		newReplayEvent("multi-assistant-1", model.RoleAssistant, "first answer", ""),
		newReplayEvent("multi-user-2", model.RoleUser, "second question", ""),
		newReplayEvent("multi-assistant-2", model.RoleAssistant, "second answer", ""),
	); err != nil {
		return Snapshot{}, err
	}
	return Capture(ctx, backend, key)
}

func replayToolCall(ctx context.Context, backend Backend) (Snapshot, error) {
	key, sess, err := createCaseSession(ctx, backend, "tool_call")
	if err != nil {
		return Snapshot{}, err
	}
	call := newReplayEvent("tool-call", model.RoleAssistant, "", "tools")
	call.Choices[0].Message.ToolCalls = []model.ToolCall{{
		ID:   "weather-1",
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "weather",
			Arguments: []byte(`{"city":"Shenzhen"}`),
		},
	}}
	result := newReplayEvent("tool-result", model.RoleTool, "sunny", "tools")
	result.Choices[0].Message.ToolID = "weather-1"
	result.Choices[0].Message.ToolName = "weather"
	if err := event.SetExtension(result, event.ToolCallArgsExtensionKey,
		map[string]any{"weather-1": map[string]any{"city": "Shenzhen"}}); err != nil {
		return Snapshot{}, err
	}
	if err := appendEvents(ctx, backend, sess, call, result); err != nil {
		return Snapshot{}, err
	}
	return Capture(ctx, backend, key)
}

func replayStateUpdates(ctx context.Context, backend Backend) (Snapshot, error) {
	key, _, err := createCaseSession(ctx, backend, "state_updates")
	if err != nil {
		return Snapshot{}, err
	}
	if err := backend.Session.UpdateSessionState(ctx, key, session.StateMap{
		"status": []byte("draft"),
		"remove": []byte("temporary"),
	}); err != nil {
		return Snapshot{}, fmt.Errorf("write initial state: %w", err)
	}
	if err := backend.Session.UpdateSessionState(ctx, key, session.StateMap{
		"status": []byte("final"),
		"remove": nil,
	}); err != nil {
		return Snapshot{}, fmt.Errorf("overwrite state: %w", err)
	}
	return Capture(ctx, backend, key)
}

func replayMemoryReadWrite(ctx context.Context, backend Backend) (Snapshot, error) {
	key, _, err := createCaseSession(ctx, backend, "memory_read_write")
	if err != nil {
		return Snapshot{}, err
	}
	userKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
	if err := backend.Memory.AddMemory(ctx, userKey, "prefers concise answers", []string{"preference"}); err != nil {
		return Snapshot{}, fmt.Errorf("write preference memory: %w", err)
	}
	eventTime := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	if err := backend.Memory.AddMemory(ctx, userKey, "completed deployment review", []string{"work"}, memory.WithMetadata(&memory.Metadata{
		Kind:      memory.KindEpisode,
		EventTime: &eventTime,
	})); err != nil {
		return Snapshot{}, fmt.Errorf("write episodic memory: %w", err)
	}
	return Capture(ctx, backend, key)
}

func replaySummaryUpdate(ctx context.Context, backend Backend) (Snapshot, error) {
	key, sess, err := createCaseSession(ctx, backend, "summary_update")
	if err != nil {
		return Snapshot{}, err
	}
	if err := appendEvents(ctx, backend, sess,
		newReplayEvent("summary-user", model.RoleUser, "summarize this", "branch-a"),
		newReplayEvent("summary-assistant", model.RoleAssistant, "summary source", "branch-a"),
	); err != nil {
		return Snapshot{}, err
	}
	fresh, err := backend.Session.GetSession(ctx, key)
	if err != nil {
		return Snapshot{}, err
	}
	if err := backend.Session.CreateSessionSummary(ctx, fresh, "branch-a", true); err != nil {
		return Snapshot{}, fmt.Errorf("create branch summary: %w", err)
	}
	if err := appendEvents(ctx, backend, sess,
		newReplayEvent("summary-user-follow-up", model.RoleUser, "add this too", "branch-a"),
	); err != nil {
		return Snapshot{}, err
	}
	fresh, err = backend.Session.GetSession(ctx, key)
	if err != nil {
		return Snapshot{}, err
	}
	if err := backend.Session.CreateSessionSummary(ctx, fresh, "branch-a", true); err != nil {
		return Snapshot{}, fmt.Errorf("update branch summary: %w", err)
	}
	return Capture(ctx, backend, key, "branch-a")
}

func replaySummaryWithFollowUpEvents(ctx context.Context, backend Backend) (Snapshot, error) {
	key, sess, err := createCaseSession(ctx, backend, "summary_with_follow_up_events")
	if err != nil {
		return Snapshot{}, err
	}
	if err := appendEvents(ctx, backend, sess,
		newReplayEvent("truncate-user", model.RoleUser, "old context", ""),
		newReplayEvent("truncate-assistant", model.RoleAssistant, "old answer", ""),
	); err != nil {
		return Snapshot{}, err
	}
	fresh, err := backend.Session.GetSession(ctx, key)
	if err != nil {
		return Snapshot{}, err
	}
	if err := backend.Session.CreateSessionSummary(ctx, fresh, "", true); err != nil {
		return Snapshot{}, fmt.Errorf("create full summary: %w", err)
	}
	if err := appendEvents(ctx, backend, sess,
		newReplayEvent("follow-up-user", model.RoleUser, "new context", ""),
		newReplayEvent("follow-up-assistant", model.RoleAssistant, "new answer", ""),
	); err != nil {
		return Snapshot{}, err
	}
	return Capture(ctx, backend, key, session.SummaryFilterKeyAllContents)
}

func replayTrackEvents(ctx context.Context, backend Backend) (Snapshot, error) {
	key, sess, err := createCaseSession(ctx, backend, "track_events")
	if err != nil {
		return Snapshot{}, err
	}
	trackService, ok := backend.Session.(session.TrackService)
	if !ok {
		return Snapshot{}, fmt.Errorf("backend %q does not support track events", backend.Name)
	}
	if err := appendTrackEvents(ctx, trackService, sess,
		newReplayTrackEvent("tool", map[string]any{"state": "started", "invocation": "call-1"}),
		newReplayTrackEvent("tool", map[string]any{"state": "failed", "error": "timeout", "duration_ms": 15}),
	); err != nil {
		return Snapshot{}, err
	}
	return Capture(ctx, backend, key)
}

func replayInterleavedWrites(ctx context.Context, backend Backend) (Snapshot, error) {
	key, sess, err := createCaseSession(ctx, backend, "interleaved_writes")
	if err != nil {
		return Snapshot{}, err
	}
	second := make(chan struct{})
	done := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		done <- backend.Session.AppendEvent(ctx, sess,
			newReplayEvent("interleaved-tool-a", model.RoleTool, "a", "parallel"))
		close(second)
	}()
	go func() {
		defer wait.Done()
		<-second
		done <- backend.Session.AppendEvent(ctx, sess,
			newReplayEvent("interleaved-tool-b", model.RoleTool, "b", "parallel"))
	}()
	wait.Wait()
	close(done)
	for appendErr := range done {
		if appendErr != nil {
			return Snapshot{}, fmt.Errorf("append interleaved event: %w", appendErr)
		}
	}
	return Capture(ctx, backend, key)
}

func replayRetryRecovery(ctx context.Context, backend Backend) (Snapshot, error) {
	key, sess, err := createCaseSession(ctx, backend, "retry_recovery")
	if err != nil {
		return Snapshot{}, err
	}
	userKey := memory.UserKey{AppName: key.AppName, UserID: key.UserID}
	for attempt := 0; attempt < 2; attempt++ {
		if err := backend.Memory.AddMemory(ctx, userKey, "retry-safe fact", []string{"recovery"}); err != nil {
			return Snapshot{}, fmt.Errorf("write retry memory: %w", err)
		}
	}
	if err := backend.Session.UpdateSessionState(ctx, key, session.StateMap{"attempt": []byte("1")}); err != nil {
		return Snapshot{}, err
	}
	if err := backend.Session.UpdateSessionState(ctx, key, session.StateMap{"attempt": []byte("2")}); err != nil {
		return Snapshot{}, err
	}
	if err := appendEvents(ctx, backend, sess,
		newReplayEvent("recovery-user", model.RoleUser, "retry complete", "")); err != nil {
		return Snapshot{}, err
	}
	return Capture(ctx, backend, key)
}

func createCaseSession(ctx context.Context, backend Backend, name string) (session.Key, *session.Session, error) {
	key := session.Key{AppName: replayApp, UserID: replayUser, SessionID: name}
	sess, err := backend.Session.CreateSession(ctx, key, nil)
	if err != nil {
		return session.Key{}, nil, fmt.Errorf("create session: %w", err)
	}
	return key, sess, nil
}

func appendEvents(ctx context.Context, backend Backend, sess *session.Session, events ...*event.Event) error {
	for _, replayEvent := range events {
		if err := backend.Session.AppendEvent(ctx, sess, replayEvent); err != nil {
			return fmt.Errorf("append event: %w", err)
		}
	}
	return nil
}

func appendTrackEvents(ctx context.Context, service session.TrackService, sess *session.Session, events ...*session.TrackEvent) error {
	for _, trackEvent := range events {
		if err := service.AppendTrackEvent(ctx, sess, trackEvent); err != nil {
			return fmt.Errorf("append track event: %w", err)
		}
	}
	return nil
}

func newReplayEvent(id string, role model.Role, content, filterKey string) *event.Event {
	response := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role:    role,
		Content: content,
	}}}}
	replayEvent := event.NewResponseEvent("replay-invocation", "replay-agent", response, event.WithBranch(filterKey))
	replayEvent.ID = id
	replayEvent.Timestamp = time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	replayEvent.FilterKey = filterKey
	return replayEvent
}

func newReplayTrackEvent(track session.Track, payload any) *session.TrackEvent {
	raw, _ := json.Marshal(payload)
	return &session.TrackEvent{
		Track:     track,
		Payload:   raw,
		Timestamp: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
	}
}
