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
	"sort"
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
	replayApp  = "replay-consistency"
	replayUser = "fixture-user"
	seqKey     = "replay.seq"
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
	name        string
	sessions    frameworksession.Service
	tracks      frameworksession.TrackService
	memories    frameworkmemory.Service
	summarizer  *deterministicSummarizer
	key         frameworksession.Key
	unsupported map[string]string
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

func (b *serviceBackend) Save(input Snapshot) error {
	ctx := context.Background()
	b.key = frameworksession.Key{AppName: replayApp, UserID: replayUser, SessionID: input.SessionID}
	b.unsupported = clone(input).Unsupported
	sess, err := b.sessions.CreateSession(ctx, b.key, frameworksession.StateMap{})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	for _, item := range input.Events {
		if err := b.sessions.AppendEvent(ctx, sess, toFrameworkEvent(item)); err != nil {
			return fmt.Errorf("append event: %w", err)
		}
	}
	keys := make([]string, 0, len(input.State))
	for key := range input.State {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		// Exercise overwrite semantics before persisting the final value.
		if err := b.sessions.UpdateSessionState(ctx, b.key, frameworksession.StateMap{key: []byte(`"stale"`)}); err != nil {
			return fmt.Errorf("prime session state %q: %w", key, err)
		}
		value, err := json.Marshal(input.State[key])
		if err != nil {
			return fmt.Errorf("encode state %q: %w", key, err)
		}
		if input.State[key] == nil {
			value = nil
		}
		if err := b.sessions.UpdateSessionState(ctx, b.key, frameworksession.StateMap{key: value}); err != nil {
			return fmt.Errorf("update session state %q: %w", key, err)
		}
	}

	userKey := frameworkmemory.UserKey{AppName: replayApp, UserID: replayUser}
	for _, item := range input.Memories {
		topics := stringSlice(item.Metadata["topics"])
		if err := b.memories.AddMemory(ctx, userKey, item.Content, topics); err != nil {
			return fmt.Errorf("add memory: %w", err)
		}
	}
	for _, item := range input.Tracks {
		payload, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("encode track: %w", err)
		}
		if err := b.tracks.AppendTrackEvent(ctx, sess, &frameworksession.TrackEvent{
			Track: frameworksession.Track(item.Name), Payload: payload, Timestamp: time.Now().UTC(),
		}); err != nil {
			return fmt.Errorf("append track: %w", err)
		}
	}
	for _, item := range input.Summaries {
		fresh, err := b.sessions.GetSession(ctx, b.key)
		if err != nil {
			return fmt.Errorf("load session for summary: %w", err)
		}
		b.summarizer.set(item.Text)
		if err := b.sessions.CreateSessionSummary(ctx, fresh, item.FilterKey, true); err != nil {
			return fmt.Errorf("create summary %q: %w", item.FilterKey, err)
		}
		stored, err := b.sessions.GetSession(ctx, b.key)
		if err != nil {
			return fmt.Errorf("reload summary %q: %w", item.FilterKey, err)
		}
		if stored.Summaries[item.FilterKey] == nil {
			return fmt.Errorf("summary %q was not persisted from %d events", item.FilterKey, len(fresh.Events))
		}
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
	out := Snapshot{
		SessionID: b.key.SessionID, State: decodeState(sess.SnapshotState()),
		Unsupported: cloneStringMap(b.unsupported),
	}
	for index := range sess.Events {
		out.Events = append(out.Events, fromFrameworkEvent(&sess.Events[index], index+1))
	}
	entries, err := b.memories.ReadMemories(ctx, frameworkmemory.UserKey{AppName: replayApp, UserID: replayUser}, 100)
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

func toFrameworkEvent(input Event) *frameworkevent.Event {
	message := model.Message{Role: model.Role(input.Role), Content: input.Content, ToolName: input.Tool}
	if input.Tool != "" && input.Role != string(model.RoleTool) {
		arguments, _ := json.Marshal(input.Args)
		message.ToolCalls = []model.ToolCall{{
			Type: "function", ID: fmt.Sprintf("call-%d", input.Seq),
			Function: model.FunctionDefinitionParam{Name: input.Tool, Arguments: arguments},
		}}
	}
	if input.Response != nil {
		encoded, _ := json.Marshal(input.Response)
		message.Content = string(encoded)
		message.ToolID = fmt.Sprintf("call-%d", input.Seq-1)
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
	e.StateDelta = encodeState(input.StateDelta)
	e.Extensions = make(map[string]json.RawMessage, len(input.Extensions)+1)
	for key, value := range input.Extensions {
		e.Extensions[key], _ = json.Marshal(value)
	}
	e.Extensions[seqKey], _ = json.Marshal(input.Seq)
	return e
}

func fromFrameworkEvent(input *frameworkevent.Event, fallbackSeq int) Event {
	out := Event{ID: input.ID, Seq: fallbackSeq, Author: input.Author, Branch: input.Branch, Tag: input.Tag, FilterKey: input.FilterKey, Timestamp: input.Timestamp.UTC().Format(time.RFC3339Nano)}
	if raw, ok := input.Extensions[seqKey]; ok {
		_ = json.Unmarshal(raw, &out.Seq)
	}
	if input.Response != nil && len(input.Response.Choices) > 0 {
		message := input.Response.Choices[0].Message
		out.Role, out.Content, out.Tool = string(message.Role), message.Content, message.ToolName
		if len(message.ToolCalls) > 0 {
			out.Tool = message.ToolCalls[0].Function.Name
			_ = json.Unmarshal(message.ToolCalls[0].Function.Arguments, &out.Args)
		}
		if message.Role == model.RoleTool && message.Content != "" {
			_ = json.Unmarshal([]byte(message.Content), &out.Response)
		}
	}
	out.StateDelta = decodeState(input.StateDelta)
	out.Extensions = make(map[string]any, len(input.Extensions))
	for key, raw := range input.Extensions {
		if key == seqKey {
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			out.Extensions[key] = value
		}
	}
	if len(out.Extensions) == 0 {
		out.Extensions = nil
	}
	return out
}

func encodeState(input map[string]any) frameworksession.StateMap {
	if input == nil {
		return nil
	}
	out := make(frameworksession.StateMap, len(input))
	for key, value := range input {
		if value == nil {
			out[key] = nil
			continue
		}
		out[key], _ = json.Marshal(value)
	}
	return out
}

func decodeState(input frameworksession.StateMap) map[string]any {
	out := make(map[string]any, len(input))
	for key, raw := range input {
		if raw == nil {
			out[key] = nil
			continue
		}
		var value any
		if json.Unmarshal(raw, &value) == nil {
			out[key] = value
		} else {
			out[key] = string(raw)
		}
	}
	return out
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
