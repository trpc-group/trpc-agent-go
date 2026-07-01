//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
)

// Run replays a case against one backend and projects the read-back state into
// a Snapshot. Operations declared to fail (FailAfter) swallow the induced error
// so the read-back still runs, exercising recovery behavior.
func Run(ctx context.Context, b *backends.Backend, c *ReplayCase) (*Snapshot, error) {
	key := session.Key{AppName: c.Key.AppName, UserID: c.Key.UserID, SessionID: c.Key.SessionID}
	userKey := memory.UserKey{AppName: c.Key.AppName, UserID: c.Key.UserID}

	if _, err := b.Session.CreateSession(ctx, key, session.StateMap{}); err != nil {
		return nil, fmt.Errorf("%s: create session: %w", b.Name, err)
	}

	for i := range c.Operations {
		op := c.Operations[i]
		repeat := op.Repeat
		if repeat < 0 {
			repeat = 0
		}
		for r := 0; r <= repeat; r++ {
			if err := applyOperation(ctx, b, key, userKey, op); err != nil {
				// Failure cases intentionally induce errors; keep replaying so the
				// read-back reflects the recovered state.
				if op.FailAfter > 0 {
					continue
				}
				return nil, fmt.Errorf("%s: op %q #%d: %w", b.Name, op.Type, i, err)
			}
		}
	}

	return readBack(ctx, b, key, userKey, c.Key.SessionID)
}

func applyOperation(
	ctx context.Context,
	b *backends.Backend,
	key session.Key,
	userKey memory.UserKey,
	op Operation,
) error {
	switch op.Type {
	case "append_event":
		fresh, err := b.Session.GetSession(ctx, key)
		if err != nil {
			return err
		}
		return b.Session.AppendEvent(ctx, fresh, buildEvent(op.Event))
	case "set_state":
		return b.Session.UpdateSessionState(ctx, key, session.StateMap{op.Key: stateValue(op)})
	case "delete_state":
		// Apply a nil-valued state delta through an event to stay backend-agnostic.
		fresh, err := b.Session.GetSession(ctx, key)
		if err != nil {
			return err
		}
		e := event.New("", "system")
		e.StateDelta = map[string][]byte{op.Key: nil}
		return b.Session.AppendEvent(ctx, fresh, e)
	case "add_memory":
		return b.Memory.AddMemory(ctx, userKey, op.Value, op.Topics, memoryAddOptions(op)...)
	case "update_memory":
		var res memory.UpdateResult
		return b.Memory.UpdateMemory(
			ctx,
			memory.Key{AppName: userKey.AppName, UserID: userKey.UserID, MemoryID: op.MemoryID},
			op.Value,
			op.Topics,
			memory.WithUpdateResult(&res),
		)
	case "delete_memory":
		return b.Memory.DeleteMemory(ctx, memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: op.MemoryID,
		})
	case "create_summary":
		fresh, err := b.Session.GetSession(ctx, key)
		if err != nil {
			return err
		}
		return b.Session.CreateSessionSummary(ctx, fresh, op.FilterKey, op.Force)
	case "append_track":
		tracker, ok := b.Session.(session.TrackService)
		if !ok {
			return nil
		}
		fresh, err := b.Session.GetSession(ctx, key)
		if err != nil {
			return err
		}
		return tracker.AppendTrackEvent(ctx, fresh, &session.TrackEvent{
			Track:     session.Track(op.Track),
			Payload:   op.Payload,
			Timestamp: time.Now(),
		})
	default:
		return fmt.Errorf("unknown operation type %q", op.Type)
	}
}

func stateValue(op Operation) []byte {
	if op.IsNil {
		return nil
	}
	if op.Bytes != nil {
		return op.Bytes
	}
	return []byte(op.Value)
}

func memoryAddOptions(op Operation) []memory.AddOption {
	if op.Kind == "" {
		return nil
	}
	return []memory.AddOption{memory.WithMetadata(&memory.Metadata{Kind: memory.Kind(op.Kind)})}
}

func buildEvent(spec *EventSpec) *event.Event {
	msg := model.Message{
		Role:    model.Role(spec.Role),
		Content: spec.Content,
		ToolID:  spec.ToolID,
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
	e := &event.Event{
		Author:    spec.Author,
		Response:  &model.Response{Choices: []model.Choice{{Message: msg}}},
		Timestamp: time.Now(),
		Branch:    spec.Branch,
		Tag:       spec.Tag,
		FilterKey: spec.FilterKey,
	}
	if len(spec.StateDelta) > 0 {
		e.StateDelta = make(map[string][]byte, len(spec.StateDelta))
		for k, v := range spec.StateDelta {
			e.StateDelta[k] = []byte(v)
		}
	}
	if len(spec.Extensions) > 0 {
		e.Extensions = spec.Extensions
	}
	return e
}

func readBack(
	ctx context.Context,
	b *backends.Backend,
	key session.Key,
	userKey memory.UserKey,
	sessionID string,
) (*Snapshot, error) {
	sess, err := b.Session.GetSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%s: read session: %w", b.Name, err)
	}
	snap := &Snapshot{
		SessionID: sessionID,
		State:     map[string]string{},
	}
	if sess != nil {
		snap.Events = projectEvents(sess.GetEvents())
		for k, v := range sess.SnapshotState() {
			snap.State[k] = string(v)
		}
		snap.Summaries = projectSummaries(sess, sessionID)
		snap.Tracks = projectTracks(sess)
	}

	mems, err := b.Memory.ReadMemories(ctx, userKey, 100)
	if err != nil {
		return nil, fmt.Errorf("%s: read memories: %w", b.Name, err)
	}
	snap.Memories = projectMemories(mems)
	return snap, nil
}

func projectEvents(events []event.Event) []EventView {
	views := make([]EventView, 0, len(events))
	for i := range events {
		e := events[i]
		var msg model.Message
		if e.Response != nil && len(e.Response.Choices) > 0 {
			msg = e.Response.Choices[0].Message
		}
		var toolCalls []ToolCallSpec
		for _, tc := range msg.ToolCalls {
			toolCalls = append(toolCalls, ToolCallSpec{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: json.RawMessage(tc.Function.Arguments),
			})
		}
		var stateDelta map[string]string
		if len(e.StateDelta) > 0 {
			stateDelta = make(map[string]string, len(e.StateDelta))
			for k, v := range e.StateDelta {
				stateDelta[k] = string(v)
			}
		}
		var extensions map[string]any
		if len(e.Extensions) > 0 {
			extensions = make(map[string]any, len(e.Extensions))
			for k, v := range e.Extensions {
				var decoded any
				_ = json.Unmarshal(v, &decoded)
				extensions[k] = decoded
			}
		}
		views = append(views, EventView{
			Author:     e.Author,
			Role:       string(msg.Role),
			Content:    msg.Content,
			ToolCalls:  toolCalls,
			ToolID:     msg.ToolID,
			Branch:     e.Branch,
			Tag:        e.Tag,
			FilterKey:  e.FilterKey,
			StateDelta: stateDelta,
			Extensions: extensions,
		})
	}
	return views
}

func projectSummaries(sess *session.Session, sessionID string) []SummaryView {
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()
	views := make([]SummaryView, 0, len(sess.Summaries))
	for filterKey, sum := range sess.Summaries {
		if sum == nil {
			continue
		}
		view := SummaryView{
			FilterKey: filterKey,
			Text:      sum.Summary,
			Topics:    sum.Topics,
			SessionID: sessionID,
			UpdatedAt: sum.UpdatedAt,
		}
		if sum.Boundary != nil {
			view.Version = sum.Boundary.Version
			view.CutoffAt = sum.Boundary.CutoffAt
		}
		views = append(views, view)
	}
	return views
}

func projectTracks(sess *session.Session) []TrackView {
	sess.TracksMu.RLock()
	defer sess.TracksMu.RUnlock()
	var views []TrackView
	for name, history := range sess.Tracks {
		if history == nil {
			continue
		}
		for _, te := range history.Events {
			var payload any
			if len(te.Payload) > 0 {
				_ = json.Unmarshal(te.Payload, &payload)
			}
			views = append(views, TrackView{
				Name:      string(name),
				Payload:   payload,
				Timestamp: te.Timestamp,
			})
		}
	}
	return views
}

func projectMemories(entries []*memory.Entry) []MemoryView {
	views := make([]MemoryView, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Memory == nil {
			continue
		}
		views = append(views, MemoryView{
			ID:      entry.ID,
			Content: entry.Memory.Memory,
			Topics:  entry.Memory.Topics,
			Kind:    string(entry.Memory.Kind),
			Score:   entry.Score,
		})
	}
	return views
}
