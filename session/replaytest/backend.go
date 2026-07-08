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
	run := &inMemoryRun{
		backend:          b,
		ctx:              ctx,
		caseDef:          c,
		sessions:         sessions,
		memories:         memories,
		sess:             sess,
		stateOverlay:     newStateOverlay(),
		logicalMemoryIDs: map[string]string{},
		seenEvents:       map[string]struct{}{},
	}

	if err := run.applyOperations(); err != nil {
		return nil, err
	}
	got, err := sessions.GetSession(ctx, c.Key)
	if err != nil {
		return nil, err
	}
	run.stateOverlay.apply(got)
	memoryEntries, err := memories.ReadMemories(ctx, userKey(c.Key), 100)
	if err != nil {
		return nil, err
	}
	return normalizeSession(c.Name, b.Name(), got, memoryEntries, run.unsupported), nil
}

func (b *inMemoryBackend) Close() error { return nil }

type inMemoryRun struct {
	backend          *inMemoryBackend
	ctx              context.Context
	caseDef          ReplayCase
	sessions         *sessinmemory.SessionService
	memories         memory.Service
	sess             *session.Session
	stateOverlay     *stateOverlay
	logicalMemoryIDs map[string]string
	seenEvents       map[string]struct{}
	unsupported      []UnsupportedFeature
}

func (r *inMemoryRun) applyOperations() error {
	for _, op := range r.caseDef.Operations {
		if err := r.applyOperation(op); err != nil {
			return err
		}
	}
	return nil
}

func (r *inMemoryRun) applyOperation(op Operation) error {
	switch op.Kind {
	case OpAppendEvent, OpRetryEvent:
		return r.applyEventOperation(op)
	case OpSetState, OpDeleteState, OpClearState:
		return r.applyStateOperation(op)
	case OpAddMemory, OpUpdateMemory, OpDeleteMemory, OpClearMemory:
		return r.applyMemoryOperation(op)
	case OpWriteSummary:
		return r.applySummaryOperation(op)
	case OpAppendTrack:
		return r.applyTrackOperation(op)
	case OpUnsupportedProbe:
		r.recordUnsupported(op.Unsupported)
	}
	return nil
}

func (r *inMemoryRun) applyEventOperation(op Operation) error {
	overlayEventStateDelta(r.stateOverlay, op.Event, r.seenEvents)
	if err := appendEventOnce(r.ctx, r.sessions, r.sess, op.Event, r.seenEvents); err != nil {
		return err
	}
	if op.Kind == OpRetryEvent {
		return appendEventOnce(r.ctx, r.sessions, r.sess, op.Event, r.seenEvents)
	}
	return nil
}

func (r *inMemoryRun) applyStateOperation(op Operation) error {
	switch op.Kind {
	case OpSetState:
		if op.State == nil {
			return nil
		}
		value := cloneRaw(op.State.Value)
		if err := r.sessions.UpdateSessionState(
			r.ctx,
			r.caseDef.Key,
			session.StateMap{op.State.Key: value},
		); err != nil {
			return err
		}
		r.sess.SetState(op.State.Key, value)
		r.stateOverlay.set(op.State.Key, value)
	case OpDeleteState:
		if op.State != nil {
			r.sess.DeleteState(op.State.Key)
			r.stateOverlay.delete(op.State.Key)
		}
	case OpClearState:
		r.sess.State = make(session.StateMap)
		r.stateOverlay.clear()
	}
	return nil
}

func (r *inMemoryRun) applyMemoryOperation(op Operation) error {
	switch op.Kind {
	case OpAddMemory:
		return r.addMemory(op.Memory)
	case OpUpdateMemory:
		return r.updateMemory(op.Memory)
	case OpDeleteMemory:
		return r.deleteMemory(op.Memory)
	case OpClearMemory:
		return r.memories.ClearMemories(r.ctx, userKey(r.caseDef.Key))
	default:
		return nil
	}
}

func (r *inMemoryRun) addMemory(spec *MemorySpec) error {
	if spec == nil {
		return nil
	}
	if err := r.memories.AddMemory(
		r.ctx,
		userKey(r.caseDef.Key),
		spec.Content,
		spec.Topics,
		memoryOptions(spec)...,
	); err != nil {
		return err
	}
	if spec.ID == "" {
		return nil
	}
	id, err := findMemoryID(r.ctx, r.memories, r.caseDef.Key, spec.Content)
	if err != nil {
		return err
	}
	r.logicalMemoryIDs[spec.ID] = id
	return nil
}

func (r *inMemoryRun) updateMemory(spec *MemorySpec) error {
	if spec == nil {
		return nil
	}
	result := &memory.UpdateResult{}
	err := r.memories.UpdateMemory(
		r.ctx,
		memory.Key{
			AppName:  r.caseDef.Key.AppName,
			UserID:   r.caseDef.Key.UserID,
			MemoryID: r.logicalMemoryIDs[spec.ID],
		},
		spec.Content,
		spec.Topics,
		append(memoryUpdateOptions(spec), memory.WithUpdateResult(result))...,
	)
	if err != nil {
		return err
	}
	if result.MemoryID != "" {
		r.logicalMemoryIDs[spec.ID] = result.MemoryID
	}
	return nil
}

func (r *inMemoryRun) deleteMemory(spec *MemorySpec) error {
	if spec == nil {
		return nil
	}
	return r.memories.DeleteMemory(r.ctx, memory.Key{
		AppName:  r.caseDef.Key.AppName,
		UserID:   r.caseDef.Key.UserID,
		MemoryID: r.logicalMemoryIDs[spec.ID],
	})
}

func (r *inMemoryRun) applySummaryOperation(op Operation) error {
	if op.Summary == nil {
		return nil
	}
	if err := r.sessions.CreateSessionSummary(
		r.ctx,
		r.sess,
		op.Summary.FilterKey,
		op.Summary.Force,
	); err != nil {
		return err
	}
	fresh, err := r.sessions.GetSession(r.ctx, r.caseDef.Key)
	if err != nil {
		return err
	}
	r.sess = fresh
	return nil
}

func (r *inMemoryRun) applyTrackOperation(op Operation) error {
	if op.Track == nil {
		return nil
	}
	trackEvent, err := trackEventFromSpec(op.Track)
	if err != nil {
		return err
	}
	return r.sessions.AppendTrackEvent(r.ctx, r.sess, trackEvent)
}

func (r *inMemoryRun) recordUnsupported(cap Capability) {
	if r.backend.Supports(cap) {
		return
	}
	r.unsupported = append(r.unsupported, UnsupportedFeature{
		Capability:  cap,
		AllowedDiff: true,
		Explanation: r.backend.Unsupported(cap),
	})
}

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
	_ = ctx
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
	run := &jsonFileRun{
		backend:          b,
		caseDef:          c,
		path:             path,
		logicalMemoryIDs: map[string]string{},
		seenEvents:       map[string]struct{}{},
	}
	if err := run.applyOperations(); err != nil {
		return nil, err
	}
	finalStore, err := readStore(path)
	if err != nil {
		return nil, err
	}
	return normalizeSession(c.Name, b.Name(), finalStore.Session, finalStore.Memories, run.unsupported), nil
}

func (b *jsonFileBackend) Close() error { return nil }

type jsonFileRun struct {
	backend          *jsonFileBackend
	caseDef          ReplayCase
	path             string
	logicalMemoryIDs map[string]string
	seenEvents       map[string]struct{}
	unsupported      []UnsupportedFeature
}

func (r *jsonFileRun) applyOperations() error {
	for _, op := range r.caseDef.Operations {
		loaded, err := readStore(r.path)
		if err != nil {
			return err
		}
		if err := r.applyOperation(loaded, op); err != nil {
			return err
		}
		if err := writeStore(r.path, loaded); err != nil {
			return err
		}
	}
	return nil
}

func (r *jsonFileRun) applyOperation(store *fileStore, op Operation) error {
	switch op.Kind {
	case OpAppendEvent, OpRetryEvent:
		return r.applyEventOperation(store, op)
	case OpSetState, OpDeleteState, OpClearState:
		applyFileStateOperation(store.Session, op)
	case OpAddMemory, OpUpdateMemory, OpDeleteMemory, OpClearMemory:
		applyFileMemoryOperation(&store.Memories, r.caseDef.Key, r.logicalMemoryIDs, op)
	case OpWriteSummary:
		if op.Summary != nil {
			writeFileSummary(store.Session, op.Summary.FilterKey, op.Summary.Force)
		}
	case OpAppendTrack:
		return applyFileTrackOperation(store.Session, op)
	case OpUnsupportedProbe:
		r.recordUnsupported(op.Unsupported)
	}
	return nil
}

func (r *jsonFileRun) applyEventOperation(store *fileStore, op Operation) error {
	if err := appendFileEvent(store.Session, op.Event, r.seenEvents); err != nil {
		return err
	}
	if op.Kind == OpRetryEvent {
		return appendFileEvent(store.Session, op.Event, r.seenEvents)
	}
	return nil
}

func (r *jsonFileRun) recordUnsupported(cap Capability) {
	if r.backend.Supports(cap) {
		return
	}
	r.unsupported = append(r.unsupported, UnsupportedFeature{
		Capability:  cap,
		AllowedDiff: true,
		Explanation: r.backend.Unsupported(cap),
	})
}

func applyFileStateOperation(sess *session.Session, op Operation) {
	switch op.Kind {
	case OpSetState:
		if op.State != nil {
			sess.SetState(op.State.Key, cloneRaw(op.State.Value))
		}
	case OpDeleteState:
		if op.State != nil {
			sess.DeleteState(op.State.Key)
		}
	case OpClearState:
		sess.State = make(session.StateMap)
	}
}

func applyFileMemoryOperation(
	entries *[]*memory.Entry,
	key session.Key,
	logicalIDs map[string]string,
	op Operation,
) {
	switch op.Kind {
	case OpAddMemory:
		addFileMemory(entries, key, logicalIDs, op.Memory)
	case OpUpdateMemory:
		updateFileMemory(entries, key, logicalIDs, op.Memory)
	case OpDeleteMemory:
		if op.Memory != nil {
			*entries = deleteMemory(*entries, logicalIDs[op.Memory.ID])
		}
	case OpClearMemory:
		*entries = nil
	}
}

func addFileMemory(
	entries *[]*memory.Entry,
	key session.Key,
	logicalIDs map[string]string,
	spec *MemorySpec,
) {
	if spec == nil {
		return
	}
	entry := fileMemoryEntry(key, spec)
	*entries = upsertMemory(*entries, entry)
	if spec.ID != "" {
		logicalIDs[spec.ID] = entry.ID
	}
}

func updateFileMemory(
	entries *[]*memory.Entry,
	key session.Key,
	logicalIDs map[string]string,
	spec *MemorySpec,
) {
	if spec == nil {
		return
	}
	entry := fileMemoryEntry(key, spec)
	entry.ID = stableID(key.AppName, key.UserID, spec.Content, strings.Join(spec.Topics, ","), "")
	*entries = deleteMemory(*entries, logicalIDs[spec.ID])
	*entries = upsertMemory(*entries, entry)
	logicalIDs[spec.ID] = entry.ID
}

func applyFileTrackOperation(sess *session.Session, op Operation) error {
	if op.Track == nil {
		return nil
	}
	trackEvent, err := trackEventFromSpec(op.Track)
	if err != nil {
		return err
	}
	return sess.AppendTrackEvent(trackEvent)
}

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
	return os.WriteFile(path, data, 0o600)
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

func trackEventFromSpec(spec *TrackSpec) (*session.TrackEvent, error) {
	raw, err := json.Marshal(spec.Payload)
	if err != nil {
		return nil, err
	}
	ts := spec.Timestamp
	if ts.IsZero() {
		ts = deterministicEventTime(string(spec.Name))
	}
	return &session.TrackEvent{
		Track:     spec.Name,
		Payload:   raw,
		Timestamp: ts,
	}, nil
}
