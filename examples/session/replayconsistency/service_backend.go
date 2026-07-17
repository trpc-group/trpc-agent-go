// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
	frameworkevent "trpc.group/trpc-go/trpc-agent-go/event"
	frameworkmemory "trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memorysqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	frameworksession "trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	sessionsqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

const (
	replayApp                = "replay-consistency"
	replayUser               = "fixture-user"
	seqKey                   = "replay.seq"
	generatedEventIDKey      = "replay.generated_event_id"
	generatedToolCallIDKey   = "replay.generated_tool_call_id"
	generatedToolResultIDKey = "replay.generated_tool_result_id"
)

type deterministicSummarizer struct {
	mu   sync.RWMutex
	text string
}

func (s *deterministicSummarizer) set(text string) {
	s.mu.Lock()
	s.text = text
	s.mu.Unlock()
}
func (s *deterministicSummarizer) ShouldSummarize(*frameworksession.Session) bool { return true }
func (s *deterministicSummarizer) Summarize(context.Context, *frameworksession.Session) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.text, nil
}
func (*deterministicSummarizer) SetPrompt(string)     {}
func (*deterministicSummarizer) SetModel(model.Model) {}
func (*deterministicSummarizer) Metadata() map[string]any {
	return map[string]any{"kind": "deterministic-replay"}
}

type serviceBackend struct {
	name         string
	sessions     frameworksession.Service
	tracks       frameworksession.TrackService
	memories     frameworkmemory.Service
	summarizer   *deterministicSummarizer
	key          frameworksession.Key
	unsupported  map[string]string
	modeledState map[string]bool
}

func NewInMemoryBackend() Backend {
	summarizer := &deterministicSummarizer{}
	sessions := sessioninmemory.NewSessionService(
		sessioninmemory.WithSummarizer(summarizer),
		sessioninmemory.WithSummaryFilterAllowlist("all", "conversation"),
	)
	return &serviceBackend{
		name: "inmemory-services", sessions: sessions, tracks: sessions,
		memories: memoryinmemory.NewMemoryService(), summarizer: summarizer,
	}
}

func NewSQLiteBackend(path string) (Backend, error) {
	sessionDB, err := sql.Open("sqlite", path+".session.db")
	if err != nil {
		return nil, err
	}
	summarizer := &deterministicSummarizer{}
	sessions, err := sessionsqlite.NewService(
		sessionDB,
		sessionsqlite.WithSummarizer(summarizer),
		sessionsqlite.WithSummaryFilterAllowlist("all", "conversation"),
	)
	if err != nil {
		_ = sessionDB.Close()
		return nil, err
	}
	memoryDB, err := sql.Open("sqlite", path+".memory.db")
	if err != nil {
		_ = sessions.Close()
		return nil, err
	}
	memories, err := memorysqlite.NewService(memoryDB)
	if err != nil {
		_ = memoryDB.Close()
		_ = sessions.Close()
		return nil, err
	}
	return &serviceBackend{
		name: "sqlite-services", sessions: sessions, tracks: sessions,
		memories: memories, summarizer: summarizer,
	}, nil
}

func (b *serviceBackend) Name() string { return b.name }

func (b *serviceBackend) Begin(input Snapshot) error {
	ctx := context.Background()
	b.key = frameworksession.Key{AppName: replayApp, UserID: replayUser, SessionID: input.SessionID}
	b.unsupported = cloneStringMap(input.Unsupported)
	b.modeledState = make(map[string]bool, len(input.State))
	for key := range input.State {
		b.modeledState[key] = true
	}
	if b.unsupported == nil {
		b.unsupported = map[string]string{}
	}
	for index, item := range input.Memories {
		prefix := fmt.Sprintf("/memories/%d", index)
		b.unsupported[prefix+"/id"] = "memory services generate backend-specific IDs"
		if item.Scope != "user" {
			b.unsupported[prefix+"/scope"] = "memory services persist user-scoped memories"
		}
		if item.Similarity != 0 {
			b.unsupported[prefix+"/similarity"] = "similarity is query-derived rather than persisted"
		}
		for key := range item.Metadata {
			if key != "topics" {
				b.unsupported[prefix+"/metadata"] = "memory services persist topics but not arbitrary metadata"
				break
			}
		}
	}
	for index, item := range input.Summaries {
		prefix := fmt.Sprintf("/summaries/%d", index)
		if item.ID != "summary:"+item.FilterKey {
			b.unsupported[prefix+"/id"] = "summary IDs are derived from the filter key"
		}
		b.unsupported[prefix+"/version"] = "summary boundary versions are assigned by the session service"
	}
	if _, err := b.sessions.CreateSession(ctx, b.key, frameworksession.StateMap{}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (b *serviceBackend) currentSession(ctx context.Context) (*frameworksession.Session, error) {
	sess, err := b.sessions.GetSession(ctx, b.key)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, errors.New("session not found")
	}
	return sess, nil
}

func (b *serviceBackend) AppendEvent(item Event) error {
	ctx := context.Background()
	sess, err := b.currentSession(ctx)
	if err != nil {
		return fmt.Errorf("load session for event: %w", err)
	}
	event, err := toFrameworkEvent(item)
	if err != nil {
		return fmt.Errorf("encode event %d: %w", item.Seq, err)
	}
	if err := b.sessions.AppendEvent(ctx, sess, event); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

func (b *serviceBackend) AppendEventIdempotent(item Event) error {
	if item.ID == "" {
		return errors.New("idempotent append requires a stable event ID")
	}
	ctx := context.Background()
	sess, err := b.currentSession(ctx)
	if err != nil {
		return fmt.Errorf("load session for idempotent append: %w", err)
	}
	for i := range sess.Events {
		if sess.Events[i].ID == item.ID {
			return nil
		}
	}
	return b.AppendEvent(item)
}

func (b *serviceBackend) UpdateState(key string, input any) error {
	ctx := context.Background()
	// Exercise overwrite semantics before persisting the final value.
	if err := b.sessions.UpdateSessionState(ctx, b.key, frameworksession.StateMap{key: []byte(`"stale"`)}); err != nil {
		return fmt.Errorf("prime session state %q: %w", key, err)
	}
	value, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode state %q: %w", key, err)
	}
	if input == nil {
		value = nil
	}
	if err := b.sessions.UpdateSessionState(ctx, b.key, frameworksession.StateMap{key: value}); err != nil {
		return fmt.Errorf("update session state %q: %w", key, err)
	}
	return nil
}

func (b *serviceBackend) AddMemory(item Memory) error {
	ctx := context.Background()
	userKey := frameworkmemory.UserKey{AppName: replayApp, UserID: replayUser}
	if err := b.memories.AddMemory(ctx, userKey, item.Content, stringSlice(item.Metadata["topics"])); err != nil {
		return fmt.Errorf("add memory: %w", err)
	}
	return nil
}

func (b *serviceBackend) AppendTrack(item TrackEvent) error {
	ctx := context.Background()
	sess, err := b.currentSession(ctx)
	if err != nil {
		return fmt.Errorf("load session for track: %w", err)
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("encode track: %w", err)
	}
	if err := b.tracks.AppendTrackEvent(ctx, sess, &frameworksession.TrackEvent{
		Track: frameworksession.Track(item.Name), Payload: payload, Timestamp: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("append track: %w", err)
	}
	return nil
}

func (b *serviceBackend) CreateSummary(item Summary) error {
	ctx := context.Background()
	fresh, err := b.currentSession(ctx)
	if err != nil {
		return fmt.Errorf("load session for summary: %w", err)
	}
	b.summarizer.set(item.Text)
	if err := b.sessions.CreateSessionSummary(ctx, fresh, item.FilterKey, true); err != nil {
		return fmt.Errorf("create summary %q: %w", item.FilterKey, err)
	}
	stored, err := b.currentSession(ctx)
	if err != nil {
		return fmt.Errorf("reload summary %q: %w", item.FilterKey, err)
	}
	if stored.Summaries[item.FilterKey] == nil {
		return fmt.Errorf("summary %q was not persisted from %d events", item.FilterKey, len(fresh.Events))
	}
	return nil
}

func (b *serviceBackend) Load() (Snapshot, error) {
	ctx := context.Background()
	sess, err := b.sessions.GetSession(ctx, b.key)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get session: %w", err)
	}
	if sess == nil {
		return Snapshot{}, errors.New("session not found after replay")
	}
	state, err := decodeState(sess.SnapshotState())
	if err != nil {
		return Snapshot{}, fmt.Errorf("decode session state: %w", err)
	}
	for key := range state {
		if !b.modeledState[key] {
			delete(state, key)
		}
	}
	out := Snapshot{
		SessionID: b.key.SessionID, State: state,
		Unsupported: cloneStringMap(b.unsupported),
	}
	for index := range sess.Events {
		event, err := fromFrameworkEvent(&sess.Events[index], index+1)
		if err != nil {
			return Snapshot{}, fmt.Errorf("decode event %d: %w", index, err)
		}
		out.Events = append(out.Events, event)
	}
	entries, err := b.memories.ReadMemories(ctx, frameworkmemory.UserKey{AppName: replayApp, UserID: replayUser}, 0)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read memories: %w", err)
	}
	for _, entry := range entries {
		metadata := map[string]any{}
		if len(entry.Memory.Topics) > 0 {
			metadata["topics"] = entry.Memory.Topics
		}
		out.Memories = append(out.Memories, Memory{
			ID: entry.ID, Content: entry.Memory.Memory, Metadata: metadata,
			Scope: "user", Similarity: entry.Score,
		})
	}
	for filterKey, item := range sess.Summaries {
		version := frameworksession.SummaryBoundaryVersion
		if item.Boundary != nil && item.Boundary.Version != 0 {
			version = item.Boundary.Version
		}
		out.Summaries = append(out.Summaries, Summary{
			ID: "summary:" + filterKey, SessionID: b.key.SessionID, FilterKey: filterKey,
			Text: item.Summary, Version: version, UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	for _, history := range sess.Tracks {
		for _, item := range history.Events {
			var track TrackEvent
			if err := json.Unmarshal(item.Payload, &track); err != nil {
				return Snapshot{}, fmt.Errorf("decode track payload: %w", err)
			}
			track.Name = string(item.Track)
			track.Timestamp = item.Timestamp.UTC().Format(time.RFC3339Nano)
			out.Tracks = append(out.Tracks, track)
		}
	}
	return out, nil
}

func (b *serviceBackend) Close() error {
	return errors.Join(b.memories.Close(), b.sessions.Close())
}

func toFrameworkEvent(input Event) (*frameworkevent.Event, error) {
	message := model.Message{Role: model.Role(input.Role), Content: input.Content, ToolName: input.Tool}
	generatedToolCallID := false
	if input.Tool != "" && input.Role != string(model.RoleTool) {
		arguments, err := json.Marshal(input.Args)
		if err != nil {
			return nil, fmt.Errorf("encode tool arguments: %w", err)
		}
		toolCallID := input.ToolCallID
		if toolCallID == "" {
			toolCallID = fmt.Sprintf("generated-call-%d", input.Seq)
			generatedToolCallID = true
		}
		message.ToolCalls = []model.ToolCall{{
			Type: "function", ID: toolCallID,
			Function: model.FunctionDefinitionParam{Name: input.Tool, Arguments: arguments},
		}}
	}
	generatedToolResultID := false
	if input.Response != nil {
		encoded, err := json.Marshal(input.Response)
		if err != nil {
			return nil, fmt.Errorf("encode tool response: %w", err)
		}
		message.Content = string(encoded)
		message.ToolID = input.ToolResultID
		if message.ToolID == "" {
			message.ToolID = fmt.Sprintf("generated-result-%d", input.Seq)
			generatedToolResultID = true
		}
	}
	response := &model.Response{
		Choices: []model.Choice{{Message: message}}, Done: true,
	}
	if message.Role == model.RoleTool {
		response.Object = model.ObjectTypeToolResponse
	}
	invocationID := fmt.Sprintf("invocation-%d", input.Seq)
	if input.Tool != "" {
		invocationID = "invocation-" + input.Tool
	}
	e := frameworkevent.NewResponseEvent(invocationID, input.Author, response)
	if input.ID != "" {
		e.ID = input.ID
	}
	e.Branch, e.Tag, e.FilterKey = input.Branch, input.Tag, input.FilterKey
	stateDelta, err := encodeState(input.StateDelta)
	if err != nil {
		return nil, fmt.Errorf("encode state delta: %w", err)
	}
	e.StateDelta = stateDelta
	e.Extensions = make(map[string]json.RawMessage, len(input.Extensions)+4)
	for key, value := range input.Extensions {
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode extension %q: %w", key, err)
		}
		e.Extensions[key] = raw
	}
	sequence, err := json.Marshal(input.Seq)
	if err != nil {
		return nil, fmt.Errorf("encode sequence: %w", err)
	}
	e.Extensions[seqKey] = sequence
	if input.ID == "" {
		e.Extensions[generatedEventIDKey] = json.RawMessage("true")
	}
	if generatedToolCallID {
		e.Extensions[generatedToolCallIDKey] = json.RawMessage("true")
	}
	if generatedToolResultID {
		e.Extensions[generatedToolResultIDKey] = json.RawMessage("true")
	}
	return e, nil
}

func fromFrameworkEvent(input *frameworkevent.Event, fallbackSeq int) (Event, error) {
	out := Event{ID: input.ID, Seq: fallbackSeq, Author: input.Author, Branch: input.Branch, Tag: input.Tag, FilterKey: input.FilterKey, Timestamp: input.Timestamp.UTC().Format(time.RFC3339Nano)}
	if _, generated := input.Extensions[generatedEventIDKey]; generated {
		out.ID = ""
	}
	if raw, ok := input.Extensions[seqKey]; ok {
		if err := json.Unmarshal(raw, &out.Seq); err != nil {
			return Event{}, fmt.Errorf("decode sequence: %w", err)
		}
	}
	if input.Response != nil && len(input.Response.Choices) > 0 {
		message := input.Response.Choices[0].Message
		out.Role, out.Content, out.Tool = string(message.Role), message.Content, message.ToolName
		if len(message.ToolCalls) > 0 {
			out.Tool = message.ToolCalls[0].Function.Name
			out.ToolCallID = message.ToolCalls[0].ID
			if _, generated := input.Extensions[generatedToolCallIDKey]; generated {
				out.ToolCallID = ""
			}
			if err := json.Unmarshal(message.ToolCalls[0].Function.Arguments, &out.Args); err != nil {
				return Event{}, fmt.Errorf("decode tool arguments: %w", err)
			}
		}
		if message.Role == model.RoleTool && message.Content != "" {
			out.ToolResultID = message.ToolID
			if _, generated := input.Extensions[generatedToolResultIDKey]; generated {
				out.ToolResultID = ""
			}
			if err := json.Unmarshal([]byte(message.Content), &out.Response); err != nil {
				return Event{}, fmt.Errorf("decode tool response: %w", err)
			}
			out.Content = ""
		}
	}
	stateDelta, err := decodeState(input.StateDelta)
	if err != nil {
		return Event{}, fmt.Errorf("decode state delta: %w", err)
	}
	out.StateDelta = stateDelta
	out.Extensions = make(map[string]any, len(input.Extensions))
	for key, raw := range input.Extensions {
		if key == seqKey || key == generatedEventIDKey || key == generatedToolCallIDKey || key == generatedToolResultIDKey {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return Event{}, fmt.Errorf("decode extension %q: %w", key, err)
		}
		out.Extensions[key] = value
	}
	if len(out.Extensions) == 0 {
		out.Extensions = nil
	}
	return out, nil
}

func encodeState(input map[string]any) (frameworksession.StateMap, error) {
	if input == nil {
		return nil, nil
	}
	out := make(frameworksession.StateMap, len(input))
	for key, value := range input {
		if value == nil {
			out[key] = nil
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode key %q: %w", key, err)
		}
		out[key] = raw
	}
	return out, nil
}

func decodeState(input frameworksession.StateMap) (map[string]any, error) {
	out := make(map[string]any, len(input))
	for key, raw := range input {
		if raw == nil {
			out[key] = nil
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode key %q: %w", key, err)
		}
		out[key] = value
	}
	return out, nil
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	if direct, ok := value.([]string); ok {
		return direct
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func cloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
