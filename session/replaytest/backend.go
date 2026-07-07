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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// NewInMemoryBackend returns the required in-memory replay backend.
func NewInMemoryBackend() Backend {
	return &inMemoryBackend{}
}

// NewJSONFileBackend returns a lightweight local persistent backend. It is the
// default SQLite-equivalent backend used by root-module tests.
func NewJSONFileBackend(dir string) Backend {
	return &jsonFileBackend{dir: dir}
}

type inMemoryBackend struct{}

func (b *inMemoryBackend) Name() string { return "session/inmemory+memory/inmemory" }

func (b *inMemoryBackend) Supports(cap Capability) bool {
	switch cap {
	case CapabilityTrack, CapabilityTTL, CapabilityMemorySearch:
		return true
	default:
		return false
	}
}

func (b *inMemoryBackend) Unsupported(cap Capability) string {
	if b.Supports(cap) {
		return ""
	}
	if cap == CapabilityEventPage {
		return "inmemory GetSession does not support strict offset event pages"
	}
	return "capability is not exposed by the in-memory replay adapter"
}

func (b *inMemoryBackend) Apply(ctx context.Context, c ReplayCase) (*Snapshot, error) {
	sessions := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(deterministicSummarizer{}),
		sessinmemory.WithAsyncSummaryNum(0),
	)
	defer sessions.Close()
	memories := meminmemory.NewMemoryService()
	defer memories.Close()

	sess, err := sessions.CreateSession(ctx, c.Key, nil)
	if err != nil {
		return nil, err
	}
	stateOverlay := newStateOverlay()
	logicalMemoryIDs := map[string]string{}
	seenEvents := map[string]struct{}{}
	unsupported := make([]UnsupportedFeature, 0)

	for _, op := range c.Operations {
		switch op.Kind {
		case OpAppendEvent:
			overlayEventStateDelta(stateOverlay, op.Event, seenEvents)
			if err := appendEventOnce(ctx, sessions, sess, op.Event, seenEvents); err != nil {
				return nil, err
			}
		case OpRetryEvent:
			overlayEventStateDelta(stateOverlay, op.Event, seenEvents)
			if err := appendEventOnce(ctx, sessions, sess, op.Event, seenEvents); err != nil {
				return nil, err
			}
			if err := appendEventOnce(ctx, sessions, sess, op.Event, seenEvents); err != nil {
				return nil, err
			}
		case OpSetState:
			value := cloneRaw(op.State.Value)
			if err := sessions.UpdateSessionState(ctx, c.Key, session.StateMap{op.State.Key: value}); err != nil {
				return nil, err
			}
			sess.SetState(op.State.Key, value)
			stateOverlay.set(op.State.Key, value)
		case OpDeleteState:
			sess.DeleteState(op.State.Key)
			stateOverlay.delete(op.State.Key)
		case OpClearState:
			sess.State = make(session.StateMap)
			stateOverlay.clear()
		case OpAddMemory:
			if err := memories.AddMemory(ctx, userKey(c.Key), op.Memory.Content, op.Memory.Topics, memoryOptions(op.Memory)...); err != nil {
				return nil, err
			}
			if op.Memory.ID != "" {
				id, err := findMemoryID(ctx, memories, c.Key, op.Memory.Content)
				if err != nil {
					return nil, err
				}
				logicalMemoryIDs[op.Memory.ID] = id
			}
		case OpUpdateMemory:
			id := logicalMemoryIDs[op.Memory.ID]
			result := &memory.UpdateResult{}
			err := memories.UpdateMemory(
				ctx,
				memory.Key{AppName: c.Key.AppName, UserID: c.Key.UserID, MemoryID: id},
				op.Memory.Content,
				op.Memory.Topics,
				append(memoryUpdateOptions(op.Memory), memory.WithUpdateResult(result))...,
			)
			if err != nil {
				return nil, err
			}
			if result.MemoryID != "" {
				logicalMemoryIDs[op.Memory.ID] = result.MemoryID
			}
		case OpDeleteMemory:
			id := logicalMemoryIDs[op.Memory.ID]
			if err := memories.DeleteMemory(ctx, memory.Key{AppName: c.Key.AppName, UserID: c.Key.UserID, MemoryID: id}); err != nil {
				return nil, err
			}
		case OpClearMemory:
			if err := memories.ClearMemories(ctx, userKey(c.Key)); err != nil {
				return nil, err
			}
		case OpWriteSummary:
			if err := sessions.CreateSessionSummary(ctx, sess, op.Summary.FilterKey, op.Summary.Force); err != nil {
				return nil, err
			}
			fresh, err := sessions.GetSession(ctx, c.Key)
			if err != nil {
				return nil, err
			}
			sess = fresh
		case OpAppendTrack:
			raw, err := json.Marshal(op.Track.Payload)
			if err != nil {
				return nil, err
			}
			ts := op.Track.Timestamp
			if ts.IsZero() {
				ts = deterministicEventTime(string(op.Track.Name))
			}
			if err := sessions.AppendTrackEvent(ctx, sess, &session.TrackEvent{
				Track:     op.Track.Name,
				Payload:   raw,
				Timestamp: ts,
			}); err != nil {
				return nil, err
			}
		case OpUnsupportedProbe:
			if !b.Supports(op.Unsupported) {
				unsupported = append(unsupported, UnsupportedFeature{
					Capability:  op.Unsupported,
					AllowedDiff: true,
					Explanation: b.Unsupported(op.Unsupported),
				})
			}
		}
	}
	got, err := sessions.GetSession(ctx, c.Key)
	if err != nil {
		return nil, err
	}
	stateOverlay.apply(got)
	memoryEntries, err := memories.ReadMemories(ctx, userKey(c.Key), 100)
	if err != nil {
		return nil, err
	}
	return normalizeSession(c.Name, b.Name(), got, memoryEntries, unsupported), nil
}

func (b *inMemoryBackend) Close() error { return nil }

type jsonFileBackend struct {
	dir string
}

func (b *jsonFileBackend) Name() string { return "session/jsonfile+memory/jsonfile" }

func (b *jsonFileBackend) Supports(cap Capability) bool {
	switch cap {
	case CapabilityTrack, CapabilityMemorySearch:
		return true
	default:
		return false
	}
}

func (b *jsonFileBackend) Unsupported(cap Capability) string {
	if b.Supports(cap) {
		return ""
	}
	switch cap {
	case CapabilityEventPage:
		return "jsonfile replay backend stores full event logs and does not implement strict page reads"
	case CapabilityTTL:
		return "jsonfile replay backend does not expire records during lightweight consistency tests"
	default:
		return "capability is not exposed by the jsonfile replay adapter"
	}
}

func (b *jsonFileBackend) Apply(ctx context.Context, c ReplayCase) (*Snapshot, error) {
	dir := b.dir
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "trpc-agent-replay-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
	}
	store := &fileStore{
		Session:  session.NewSession(c.Key.AppName, c.Key.UserID, c.Key.SessionID),
		Memories: []*memory.Entry{},
	}
	path := filepath.Join(dir, safeFileName(c.Name)+".json")
	if err := writeStore(path, store); err != nil {
		return nil, err
	}
	logicalMemoryIDs := map[string]string{}
	seenEvents := map[string]struct{}{}
	unsupported := make([]UnsupportedFeature, 0)

	for _, op := range c.Operations {
		loaded, err := readStore(path)
		if err != nil {
			return nil, err
		}
		switch op.Kind {
		case OpAppendEvent:
			if err := appendFileEvent(loaded.Session, op.Event, seenEvents); err != nil {
				return nil, err
			}
		case OpRetryEvent:
			if err := appendFileEvent(loaded.Session, op.Event, seenEvents); err != nil {
				return nil, err
			}
			if err := appendFileEvent(loaded.Session, op.Event, seenEvents); err != nil {
				return nil, err
			}
		case OpSetState:
			loaded.Session.SetState(op.State.Key, cloneRaw(op.State.Value))
		case OpDeleteState:
			loaded.Session.DeleteState(op.State.Key)
		case OpClearState:
			loaded.Session.State = make(session.StateMap)
		case OpAddMemory:
			entry := fileMemoryEntry(c.Key, op.Memory)
			loaded.Memories = upsertMemory(loaded.Memories, entry)
			if op.Memory.ID != "" {
				logicalMemoryIDs[op.Memory.ID] = entry.ID
			}
		case OpUpdateMemory:
			id := logicalMemoryIDs[op.Memory.ID]
			entry := fileMemoryEntry(c.Key, op.Memory)
			entry.ID = stableID(c.Key.AppName, c.Key.UserID, op.Memory.Content, strings.Join(op.Memory.Topics, ","), "")
			loaded.Memories = deleteMemory(loaded.Memories, id)
			loaded.Memories = upsertMemory(loaded.Memories, entry)
			logicalMemoryIDs[op.Memory.ID] = entry.ID
		case OpDeleteMemory:
			loaded.Memories = deleteMemory(loaded.Memories, logicalMemoryIDs[op.Memory.ID])
		case OpClearMemory:
			loaded.Memories = nil
		case OpWriteSummary:
			writeFileSummary(loaded.Session, op.Summary.FilterKey, op.Summary.Force)
		case OpAppendTrack:
			raw, err := json.Marshal(op.Track.Payload)
			if err != nil {
				return nil, err
			}
			ts := op.Track.Timestamp
			if ts.IsZero() {
				ts = deterministicEventTime(string(op.Track.Name))
			}
			if err := loaded.Session.AppendTrackEvent(&session.TrackEvent{
				Track:     op.Track.Name,
				Payload:   raw,
				Timestamp: ts,
			}); err != nil {
				return nil, err
			}
		case OpUnsupportedProbe:
			if !b.Supports(op.Unsupported) {
				unsupported = append(unsupported, UnsupportedFeature{
					Capability:  op.Unsupported,
					AllowedDiff: true,
					Explanation: b.Unsupported(op.Unsupported),
				})
			}
		}
		if err := writeStore(path, loaded); err != nil {
			return nil, err
		}
		_ = ctx
	}
	finalStore, err := readStore(path)
	if err != nil {
		return nil, err
	}
	return normalizeSession(c.Name, b.Name(), finalStore.Session, finalStore.Memories, unsupported), nil
}

func (b *jsonFileBackend) Close() error { return nil }

type fileStore struct {
	Session  *session.Session `json:"session"`
	Memories []*memory.Entry  `json:"memories"`
}

type stateOverlay struct {
	cleared bool
	values  map[string][]byte
	deleted map[string]struct{}
}

func newStateOverlay() *stateOverlay {
	return &stateOverlay{
		values:  map[string][]byte{},
		deleted: map[string]struct{}{},
	}
}

func (o *stateOverlay) set(key string, value []byte) {
	delete(o.deleted, key)
	o.values[key] = cloneRaw(value)
}

func (o *stateOverlay) delete(key string) {
	delete(o.values, key)
	o.deleted[key] = struct{}{}
}

func (o *stateOverlay) clear() {
	o.cleared = true
	o.values = map[string][]byte{}
	o.deleted = map[string]struct{}{}
}

func (o *stateOverlay) apply(sess *session.Session) {
	if sess == nil {
		return
	}
	if o.cleared {
		sess.State = make(session.StateMap)
	}
	for k := range o.deleted {
		sess.DeleteState(k)
	}
	for k, v := range o.values {
		sess.SetState(k, v)
	}
}

func appendEventOnce(
	ctx context.Context,
	svc session.Service,
	sess *session.Session,
	spec *EventSpec,
	seen map[string]struct{},
) error {
	if spec == nil {
		return nil
	}
	if _, ok := seen[spec.LogicalID]; ok {
		return nil
	}
	evt, err := eventFromSpec(*spec)
	if err != nil {
		return err
	}
	seen[spec.LogicalID] = struct{}{}
	return svc.AppendEvent(ctx, sess, evt)
}

func appendFileEvent(sess *session.Session, spec *EventSpec, seen map[string]struct{}) error {
	if spec == nil {
		return nil
	}
	if _, ok := seen[spec.LogicalID]; ok {
		return nil
	}
	evt, err := eventFromSpec(*spec)
	if err != nil {
		return err
	}
	seen[spec.LogicalID] = struct{}{}
	sess.UpdateUserSession(evt)
	return nil
}

func writeFileSummary(sess *session.Session, filterKey string, force bool) {
	if sess.Summaries == nil {
		sess.Summaries = make(map[string]*session.Summary)
	}
	prev := sess.Summaries[filterKey]
	var boundary *session.SummaryBoundary
	if prev != nil {
		boundary = prev.CutoffBoundary()
	}
	delta := eventsAfterBoundary(sess.GetEvents(), boundary, filterKey)
	if !force && len(delta) == 0 {
		return
	}
	input := make([]event.Event, 0, len(delta)+1)
	if prev != nil && prev.Summary != "" {
		input = append(input, event.Event{
			Author: "system",
			Response: &model.Response{Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleSystem, Content: prev.Summary},
			}}},
			Timestamp: time.Now().UTC(),
		})
	}
	input = append(input, delta...)
	tmp := session.NewSession(sess.AppName, sess.UserID, sess.ID+":"+filterKey, session.WithSessionEvents(input))
	text, _ := deterministicSummarizer{}.Summarize(context.Background(), tmp)
	latest := latestBoundary(filterKey, delta)
	if latest == nil {
		latest = session.NewSummaryBoundary(filterKey, time.Now().UTC())
	}
	sess.Summaries[filterKey] = &session.Summary{
		Summary:   text,
		UpdatedAt: latest.CutoffTime(),
		Boundary:  latest,
	}
}

func eventsAfterBoundary(events []event.Event, boundary *session.SummaryBoundary, filterKey string) []event.Event {
	out := make([]event.Event, 0, len(events))
	start := -1
	if boundary != nil && boundary.LastEventID != "" {
		for i, evt := range events {
			if evt.ID == boundary.LastEventID {
				start = i + 1
				break
			}
		}
	}
	for i, evt := range events {
		if start >= 0 && i < start {
			continue
		}
		if start < 0 && boundary != nil && !boundary.CutoffTime().IsZero() && evt.Timestamp.Before(boundary.CutoffTime()) {
			continue
		}
		if filterKey != "" && !evt.Filter(filterKey) {
			continue
		}
		out = append(out, evt)
	}
	return out
}

func latestBoundary(filterKey string, events []event.Event) *session.SummaryBoundary {
	if len(events) == 0 {
		return nil
	}
	latest := events[0]
	for _, evt := range events[1:] {
		if evt.Timestamp.After(latest.Timestamp) {
			latest = evt
		}
	}
	return session.NewSummaryBoundaryWithEventID(filterKey, latest.Timestamp, latest.ID)
}

func fileMemoryEntry(key session.Key, spec *MemorySpec) *memory.Entry {
	now := time.Unix(1700000000, 0).UTC()
	topics := append([]string{}, spec.Topics...)
	sort.Strings(topics)
	m := &memory.Memory{
		Memory:      spec.Content,
		Topics:      topics,
		LastUpdated: &now,
	}
	if spec.Metadata != nil {
		m.Kind = spec.Metadata.Kind
		m.EventTime = spec.Metadata.EventTime
		m.Participants = append([]string{}, spec.Metadata.Participants...)
		m.Location = spec.Metadata.Location
	}
	if m.Kind == "" {
		m.Kind = memory.KindFact
	}
	return &memory.Entry{
		ID:        stableID(key.AppName, key.UserID, spec.Content, strings.Join(topics, ","), string(m.Kind)),
		AppName:   key.AppName,
		UserID:    key.UserID,
		Memory:    m,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func upsertMemory(entries []*memory.Entry, entry *memory.Entry) []*memory.Entry {
	entries = deleteMemory(entries, entry.ID)
	return append(entries, entry)
}

func deleteMemory(entries []*memory.Entry, id string) []*memory.Entry {
	out := entries[:0]
	for _, entry := range entries {
		if entry != nil && entry.ID == id {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func readStore(path string) (*fileStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var store fileStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	return &store, nil
}

func writeStore(path string, store *fileStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func safeFileName(name string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", " ", "_", ":", "_")
	return replacer.Replace(name)
}

func userKey(key session.Key) memory.UserKey {
	return memory.UserKey{AppName: key.AppName, UserID: key.UserID}
}

func memoryOptions(spec *MemorySpec) []memory.AddOption {
	if spec == nil || spec.Metadata == nil {
		return nil
	}
	return []memory.AddOption{memory.WithMetadata(spec.Metadata)}
}

func memoryUpdateOptions(spec *MemorySpec) []memory.UpdateOption {
	if spec == nil || spec.Metadata == nil {
		return nil
	}
	return []memory.UpdateOption{memory.WithUpdateMetadata(spec.Metadata)}
}

func findMemoryID(ctx context.Context, svc memory.Service, key session.Key, content string) (string, error) {
	entries, err := svc.ReadMemories(ctx, userKey(key), 100)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry != nil && entry.Memory != nil && entry.Memory.Memory == content {
			return entry.ID, nil
		}
	}
	return "", fmt.Errorf("memory not found for content %q", content)
}

func cloneRaw(v []byte) []byte {
	if v == nil {
		return nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out
}

func overlayEventStateDelta(overlay *stateOverlay, spec *EventSpec, seen map[string]struct{}) {
	if overlay == nil || spec == nil {
		return
	}
	if _, ok := seen[spec.LogicalID]; ok {
		return
	}
	for k, v := range spec.StateDelta {
		overlay.set(k, v)
	}
}
