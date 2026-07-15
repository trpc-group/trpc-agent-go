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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
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

// NewServiceBackend returns a replay backend backed by concrete session and
// memory services created by factory. It is intended for optional backend
// modules that should reuse the canonical replay cases without importing those
// backend modules into the root go.mod.
func NewServiceBackend(
	name string,
	factory ServiceFactory,
	opts ...ServiceBackendOption,
) Backend {
	b := &serviceBackend{
		name:        name,
		factory:     factory,
		supported:   map[Capability]bool{},
		unsupported: map[Capability]string{},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// WithSupportedCapabilities marks capabilities as supported by a service
// backend.
func WithSupportedCapabilities(caps ...Capability) ServiceBackendOption {
	return func(b *serviceBackend) {
		for _, cap := range caps {
			b.supported[cap] = true
		}
	}
}

// WithUnsupportedCapability documents a capability that is intentionally not
// covered by a service backend.
func WithUnsupportedCapability(cap Capability, explanation string) ServiceBackendOption {
	return func(b *serviceBackend) {
		b.supported[cap] = false
		b.unsupported[cap] = explanation
	}
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
		logicalMemoryIDs: map[string]string{},
		seenEvents:       map[string]struct{}{},
		eventSeq:         0,
	}

	if err := run.applyOperations(); err != nil {
		return nil, err
	}
	got, err := sessions.GetSession(ctx, c.Key, getSessionReadOptions(c)...)
	if err != nil {
		return nil, err
	}
	memoryEntries, err := memories.ReadMemories(ctx, userKey(c.Key), 100)
	if err != nil {
		return nil, err
	}
	snapshot := normalizeSession(
		c.Name,
		b.Name(),
		got,
		memoryEntries,
		run.unsupported,
		normalizesEventOrder(c),
		c,
	)
	if err := run.runCapabilityProbes(snapshot); err != nil {
		return nil, err
	}
	memoryQueries, err := normalizeMemoryQueries(ctx, memories, c.Key, c.MemoryQueries, c)
	if err != nil {
		return nil, err
	}
	snapshot.MemoryQuery = memoryQueries
	return snapshot, nil
}

func (b *inMemoryBackend) Close() error { return nil }

type serviceBackend struct {
	name        string
	factory     ServiceFactory
	supported   map[Capability]bool
	unsupported map[Capability]string
}

func (b *serviceBackend) Name() string { return b.name }

func (b *serviceBackend) Supports(cap Capability) bool {
	return b.supported[cap]
}

func (b *serviceBackend) Unsupported(cap Capability) string {
	if b.Supports(cap) {
		return ""
	}
	if explanation := b.unsupported[cap]; explanation != "" {
		return explanation
	}
	return "capability is not exposed by this replay service backend"
}

func (b *serviceBackend) Apply(ctx context.Context, c ReplayCase) (*Snapshot, error) {
	if b.factory == nil {
		return nil, fmt.Errorf("service backend %q has no factory", b.name)
	}
	bundle, err := b.factory(ctx, c)
	if err != nil {
		return nil, err
	}
	if bundle == nil {
		return nil, fmt.Errorf("service backend %q returned nil bundle", b.name)
	}
	if bundle.Close != nil {
		defer bundle.Close()
	}
	if bundle.SessionService == nil {
		return nil, fmt.Errorf("service backend %q returned nil session service", b.name)
	}
	if bundle.MemoryService == nil {
		return nil, fmt.Errorf("service backend %q returned nil memory service", b.name)
	}

	sess, err := bundle.SessionService.CreateSession(ctx, c.Key, nil)
	if err != nil {
		return nil, err
	}
	run := &serviceRun{
		backend:            b,
		ctx:                ctx,
		caseDef:            c,
		sessions:           bundle.SessionService,
		tracks:             bundle.TrackService,
		memories:           bundle.MemoryService,
		ttlProbe:           bundle.TTLProbe,
		deleteSessionState: bundle.DeleteSessionState,
		clearSessionState:  bundle.ClearSessionState,
		sess:               sess,
		logicalMemoryIDs:   map[string]string{},
		seenEvents:         map[string]struct{}{},
		eventSeq:           0,
	}
	if err := run.applyOperations(); err != nil {
		return nil, err
	}
	got, err := bundle.SessionService.GetSession(ctx, c.Key, getSessionReadOptions(c)...)
	if err != nil {
		return nil, err
	}
	memoryEntries, err := bundle.MemoryService.ReadMemories(ctx, userKey(c.Key), 100)
	if err != nil {
		return nil, err
	}
	snapshot := normalizeSession(
		c.Name,
		b.Name(),
		got,
		memoryEntries,
		run.unsupported,
		normalizesEventOrder(c),
		c,
	)
	if err := run.runCapabilityProbes(snapshot); err != nil {
		return nil, err
	}
	if len(c.MemoryQueries) > 0 {
		if !b.Supports(CapabilityMemorySearch) {
			addUnsupported(snapshot, CapabilityMemorySearch, b.Unsupported(CapabilityMemorySearch))
		} else {
			memoryQueries, err := normalizeMemoryQueries(ctx, bundle.MemoryService, c.Key, c.MemoryQueries, c)
			if err != nil {
				return nil, err
			}
			snapshot.MemoryQuery = memoryQueries
		}
	}
	return snapshot, nil
}

func (b *serviceBackend) Close() error { return nil }

type serviceRun struct {
	backend            *serviceBackend
	ctx                context.Context
	caseDef            ReplayCase
	sessions           session.Service
	tracks             session.TrackService
	memories           memory.Service
	ttlProbe           func(context.Context) error
	deleteSessionState func(context.Context, session.Key, string) error
	clearSessionState  func(context.Context, session.Key) error
	sess               *session.Session
	sessMu             sync.Mutex
	logicalMemoryIDs   map[string]string
	logicalMemoryIDsMu sync.Mutex
	seenEvents         map[string]struct{}
	seenEventsMu       sync.Mutex
	eventSeq           int
	unsupported        []UnsupportedFeature
}

func (r *serviceRun) applyOperations() error {
	for _, op := range r.caseDef.Operations {
		if err := r.applyOperation(op); err != nil {
			return err
		}
	}
	return nil
}

func (r *serviceRun) applyOperation(op Operation) error {
	switch op.Kind {
	case OpAppendEvent, OpRetryEvent:
		return r.applyEventOperation(op)
	case OpConcurrent:
		return r.applyConcurrentOperation(op.Concurrent)
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
	case OpTTLProbe:
		// TTL is probed after the replay snapshot is collected.
	}
	return nil
}

func (r *serviceRun) applyEventOperation(op Operation) error {
	sess, err := r.sessionForBackend()
	if err != nil {
		return err
	}
	if op.Kind == OpRetryEvent {
		return appendEventRetry(r.ctx, r.sessions, sess, op.Event, r.seenEvents, &r.seenEventsMu, &r.eventSeq)
	}
	if err := appendEventOnce(r.ctx, r.sessions, sess, op.Event, r.seenEvents, &r.seenEventsMu, &r.eventSeq); err != nil {
		return err
	}
	return nil
}

func (r *serviceRun) applyConcurrentOperation(ops []Operation) error {
	ops = assignConcurrentSequences(ops, &r.eventSeq)
	return applyConcurrentOperations(ops, func(op Operation) error {
		return r.applyOperation(op)
	})
}

func (r *serviceRun) applyStateOperation(op Operation) error {
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
		r.sessMu.Lock()
		r.sess.SetState(op.State.Key, value)
		r.sessMu.Unlock()
	case OpDeleteState:
		if op.State == nil {
			return nil
		}
		if r.backend.Supports(CapabilityStateDelete) && r.deleteSessionState != nil {
			if err := r.deleteSessionState(r.ctx, r.caseDef.Key, op.State.Key); err != nil {
				return err
			}
			r.sessMu.Lock()
			r.sess.DeleteState(op.State.Key)
			r.sessMu.Unlock()
			return nil
		}
		r.recordUnsupported(CapabilityStateDelete)
	case OpClearState:
		if r.backend.Supports(CapabilityStateClear) && r.clearSessionState != nil {
			if err := r.clearSessionState(r.ctx, r.caseDef.Key); err != nil {
				return err
			}
			r.sessMu.Lock()
			r.sess.State = make(session.StateMap)
			r.sessMu.Unlock()
			return nil
		}
		r.recordUnsupported(CapabilityStateClear)
	}
	return nil
}

func (r *serviceRun) applyMemoryOperation(op Operation) error {
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

func (r *serviceRun) addMemory(spec *MemorySpec) error {
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
	r.logicalMemoryIDsMu.Lock()
	r.logicalMemoryIDs[spec.ID] = id
	r.logicalMemoryIDsMu.Unlock()
	return nil
}

func (r *serviceRun) updateMemory(spec *MemorySpec) error {
	if spec == nil {
		return nil
	}
	r.logicalMemoryIDsMu.Lock()
	memoryID := r.logicalMemoryIDs[spec.ID]
	r.logicalMemoryIDsMu.Unlock()
	result := &memory.UpdateResult{}
	err := r.memories.UpdateMemory(
		r.ctx,
		memory.Key{
			AppName:  r.caseDef.Key.AppName,
			UserID:   r.caseDef.Key.UserID,
			MemoryID: memoryID,
		},
		spec.Content,
		spec.Topics,
		append(memoryUpdateOptions(spec), memory.WithUpdateResult(result))...,
	)
	if err != nil {
		return err
	}
	if result.MemoryID != "" {
		r.logicalMemoryIDsMu.Lock()
		r.logicalMemoryIDs[spec.ID] = result.MemoryID
		r.logicalMemoryIDsMu.Unlock()
	}
	return nil
}

func (r *serviceRun) deleteMemory(spec *MemorySpec) error {
	if spec == nil {
		return nil
	}
	r.logicalMemoryIDsMu.Lock()
	memoryID := r.logicalMemoryIDs[spec.ID]
	r.logicalMemoryIDsMu.Unlock()
	return r.memories.DeleteMemory(r.ctx, memory.Key{
		AppName:  r.caseDef.Key.AppName,
		UserID:   r.caseDef.Key.UserID,
		MemoryID: memoryID,
	})
}

func (r *serviceRun) applySummaryOperation(op Operation) error {
	if op.Summary == nil {
		return nil
	}
	sess, err := r.sessionForBackend()
	if err != nil {
		return err
	}
	if err := r.sessions.CreateSessionSummary(
		r.ctx,
		sess,
		op.Summary.FilterKey,
		op.Summary.Force,
	); err != nil {
		return err
	}
	fresh, err := r.sessions.GetSession(r.ctx, r.caseDef.Key)
	if err != nil {
		return err
	}
	r.sessMu.Lock()
	r.sess = fresh
	r.sessMu.Unlock()
	return nil
}

func (r *serviceRun) applyTrackOperation(op Operation) error {
	if op.Track == nil {
		return nil
	}
	if r.tracks == nil {
		r.recordUnsupported(CapabilityTrack)
		return nil
	}
	trackEvent, err := trackEventFromSpec(op.Track)
	if err != nil {
		return err
	}
	sess, err := r.sessionForBackend()
	if err != nil {
		return err
	}
	return r.tracks.AppendTrackEvent(r.ctx, sess, trackEvent)
}

func (r *serviceRun) sessionForBackend() (*session.Session, error) {
	r.sessMu.Lock()
	hasSession := r.sess != nil
	r.sessMu.Unlock()
	if !hasSession {
		return nil, fmt.Errorf("service backend %q has no active session", r.backend.Name())
	}
	sess, err := r.sessions.GetSession(r.ctx, r.caseDef.Key)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("service backend %q returned nil session", r.backend.Name())
	}
	return sess.Clone(), nil
}

func (r *serviceRun) recordUnsupported(cap Capability) {
	if r.backend.Supports(cap) {
		return
	}
	r.unsupported = append(r.unsupported, UnsupportedFeature{
		Capability:  cap,
		AllowedDiff: true,
		Explanation: r.backend.Unsupported(cap),
	})
}

func (r *serviceRun) runCapabilityProbes(snapshot *Snapshot) error {
	if err := probeEventPage(r.ctx, r.sessions, r.caseDef.Key, r.backend, snapshot); err != nil {
		return err
	}
	return probeTTL(r.ctx, r.backend, snapshot, caseRequestsTTLProbe(r.caseDef), r.ttlProbe)
}

type inMemoryRun struct {
	backend            *inMemoryBackend
	ctx                context.Context
	caseDef            ReplayCase
	sessions           *sessinmemory.SessionService
	memories           memory.Service
	sess               *session.Session
	sessMu             sync.Mutex
	logicalMemoryIDs   map[string]string
	logicalMemoryIDsMu sync.Mutex
	seenEvents         map[string]struct{}
	seenEventsMu       sync.Mutex
	eventSeq           int
	unsupported        []UnsupportedFeature
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
	case OpConcurrent:
		return r.applyConcurrentOperation(op.Concurrent)
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
	case OpTTLProbe:
		// TTL is probed after the replay snapshot is collected.
	}
	return nil
}

func (r *inMemoryRun) applyEventOperation(op Operation) error {
	sess, err := r.sessionForBackend()
	if err != nil {
		return err
	}
	if op.Kind == OpRetryEvent {
		if err := appendEventRetry(r.ctx, r.sessions, sess, op.Event, r.seenEvents, &r.seenEventsMu, &r.eventSeq); err != nil {
			return err
		}
		r.applyEventStateOracle(op.Event)
		return nil
	}
	if err := appendEventOnce(r.ctx, r.sessions, sess, op.Event, r.seenEvents, &r.seenEventsMu, &r.eventSeq); err != nil {
		return err
	}
	r.applyEventStateOracle(op.Event)
	return nil
}

func (r *inMemoryRun) applyConcurrentOperation(ops []Operation) error {
	ops = assignConcurrentSequences(ops, &r.eventSeq)
	return applyConcurrentOperations(ops, func(op Operation) error {
		return r.applyOperation(op)
	})
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
		r.sessMu.Lock()
		r.sess.SetState(op.State.Key, value)
		r.sessMu.Unlock()
	case OpDeleteState:
		if op.State != nil {
			r.recordUnsupported(CapabilityStateDelete)
		}
	case OpClearState:
		r.recordUnsupported(CapabilityStateClear)
	}
	return nil
}

func (r *inMemoryRun) applyEventStateOracle(spec *EventSpec) {
	if spec == nil || len(spec.StateDelta) == 0 {
		return
	}
	r.sessMu.Lock()
	defer r.sessMu.Unlock()
	for key, value := range spec.StateDelta {
		r.sess.SetState(key, cloneRaw(value))
	}
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
	r.logicalMemoryIDsMu.Lock()
	r.logicalMemoryIDs[spec.ID] = id
	r.logicalMemoryIDsMu.Unlock()
	return nil
}

func (r *inMemoryRun) updateMemory(spec *MemorySpec) error {
	if spec == nil {
		return nil
	}
	r.logicalMemoryIDsMu.Lock()
	memoryID := r.logicalMemoryIDs[spec.ID]
	r.logicalMemoryIDsMu.Unlock()
	result := &memory.UpdateResult{}
	err := r.memories.UpdateMemory(
		r.ctx,
		memory.Key{
			AppName:  r.caseDef.Key.AppName,
			UserID:   r.caseDef.Key.UserID,
			MemoryID: memoryID,
		},
		spec.Content,
		spec.Topics,
		append(memoryUpdateOptions(spec), memory.WithUpdateResult(result))...,
	)
	if err != nil {
		return err
	}
	if result.MemoryID != "" {
		r.logicalMemoryIDsMu.Lock()
		r.logicalMemoryIDs[spec.ID] = result.MemoryID
		r.logicalMemoryIDsMu.Unlock()
	}
	return nil
}

func (r *inMemoryRun) deleteMemory(spec *MemorySpec) error {
	if spec == nil {
		return nil
	}
	r.logicalMemoryIDsMu.Lock()
	memoryID := r.logicalMemoryIDs[spec.ID]
	r.logicalMemoryIDsMu.Unlock()
	return r.memories.DeleteMemory(r.ctx, memory.Key{
		AppName:  r.caseDef.Key.AppName,
		UserID:   r.caseDef.Key.UserID,
		MemoryID: memoryID,
	})
}

func (r *inMemoryRun) applySummaryOperation(op Operation) error {
	if op.Summary == nil {
		return nil
	}
	sess, err := r.sessionForBackend()
	if err != nil {
		return err
	}
	if err := r.sessions.CreateSessionSummary(
		r.ctx,
		sess,
		op.Summary.FilterKey,
		op.Summary.Force,
	); err != nil {
		return err
	}
	fresh, err := r.sessions.GetSession(r.ctx, r.caseDef.Key)
	if err != nil {
		return err
	}
	r.sessMu.Lock()
	r.sess = fresh
	r.sessMu.Unlock()
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
	sess, err := r.sessionForBackend()
	if err != nil {
		return err
	}
	return r.sessions.AppendTrackEvent(r.ctx, sess, trackEvent)
}

func (r *inMemoryRun) sessionForBackend() (*session.Session, error) {
	r.sessMu.Lock()
	hasSession := r.sess != nil
	r.sessMu.Unlock()
	if !hasSession {
		return nil, fmt.Errorf("in-memory replay backend has no active session")
	}
	sess, err := r.sessions.GetSession(r.ctx, r.caseDef.Key)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		return nil, fmt.Errorf("in-memory replay backend returned nil session")
	}
	return sess.Clone(), nil
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

func (r *inMemoryRun) runCapabilityProbes(snapshot *Snapshot) error {
	if err := probeEventPage(r.ctx, r.sessions, r.caseDef.Key, r.backend, snapshot); err != nil {
		return err
	}
	return probeTTL(r.ctx, r.backend, snapshot, caseRequestsTTLProbe(r.caseDef), func(ctx context.Context) error {
		svc := sessinmemory.NewSessionService(
			sessinmemory.WithSessionTTL(80*time.Millisecond),
			sessinmemory.WithCleanupInterval(0),
		)
		defer svc.Close()
		key := session.Key{
			AppName:   r.caseDef.Key.AppName,
			UserID:    r.caseDef.Key.UserID,
			SessionID: r.caseDef.Key.SessionID + "-ttl-probe",
		}
		return ProbeSessionTTLExpiration(ctx, svc, key, 180*time.Millisecond)
	})
}

type jsonFileBackend struct {
	dir string
}

func (b *jsonFileBackend) Name() string { return "session/jsonfile+memory/jsonfile" }

func (b *jsonFileBackend) Supports(cap Capability) bool {
	switch cap {
	case CapabilityTrack, CapabilityMemorySearch, CapabilityStateDelete, CapabilityStateClear:
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
		eventSeq:         0,
	}
	if err := run.applyOperations(); err != nil {
		return nil, err
	}
	finalStore, err := readStore(path)
	if err != nil {
		return nil, err
	}
	applyFileReadEventLimit(finalStore.Session, c.ReadEventLimit)
	snapshot := normalizeSession(
		c.Name,
		b.Name(),
		finalStore.Session,
		finalStore.Memories,
		run.unsupported,
		normalizesEventOrder(c),
		c,
	)
	addUnsupported(snapshot, CapabilityEventPage, b.Unsupported(CapabilityEventPage))
	if err := probeTTL(context.Background(), b, snapshot, caseRequestsTTLProbe(c), nil); err != nil {
		return nil, err
	}
	snapshot.MemoryQuery = normalizeFileMemoryQueries(c.Key, finalStore.Memories, c.MemoryQueries, c)
	return snapshot, nil
}

func (b *jsonFileBackend) Close() error { return nil }

type jsonFileRun struct {
	backend          *jsonFileBackend
	caseDef          ReplayCase
	path             string
	logicalMemoryIDs map[string]string
	seenEvents       map[string]struct{}
	seenEventsMu     sync.Mutex
	eventSeq         int
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
	case OpConcurrent:
		return r.applyConcurrentOperation(store, op.Concurrent)
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
	case OpTTLProbe:
		r.recordUnsupported(CapabilityTTL)
	}
	return nil
}

func (r *jsonFileRun) applyConcurrentOperation(store *fileStore, ops []Operation) error {
	ops = assignConcurrentSequences(ops, &r.eventSeq)
	var storeMu sync.Mutex
	return applyConcurrentOperations(ops, func(op Operation) error {
		storeMu.Lock()
		defer storeMu.Unlock()
		return r.applyOperation(store, op)
	})
}

func (r *jsonFileRun) applyEventOperation(store *fileStore, op Operation) error {
	if op.Kind == OpRetryEvent {
		return appendFileEventRetry(store.Session, op.Event, r.seenEvents, &r.seenEventsMu, &r.eventSeq)
	}
	if err := appendFileEvent(store.Session, op.Event, r.seenEvents, &r.seenEventsMu, &r.eventSeq); err != nil {
		return err
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

func getSessionReadOptions(c ReplayCase) []session.Option {
	if c.ReadEventLimit <= 0 {
		return nil
	}
	return []session.Option{session.WithEventNum(c.ReadEventLimit)}
}

func applyFileReadEventLimit(sess *session.Session, limit int) {
	if sess == nil || limit <= 0 {
		return
	}
	sess.ApplyEventFiltering(session.WithEventNum(limit))
}

func normalizesEventOrder(c ReplayCase) bool {
	return operationsContain(c.Operations, OpConcurrent)
}

func operationsContain(ops []Operation, kind OperationKind) bool {
	for _, op := range ops {
		if op.Kind == kind {
			return true
		}
		if operationsContain(op.Concurrent, kind) {
			return true
		}
	}
	return false
}

func caseRequestsTTLProbe(c ReplayCase) bool {
	return operationsContain(c.Operations, OpTTLProbe)
}

type fileStore struct {
	Session  *session.Session `json:"session"`
	Memories []*memory.Entry  `json:"memories"`
}

func applyConcurrentOperations(ops []Operation, apply func(Operation) error) error {
	if len(ops) == 0 {
		return nil
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(ops))
	start := make(chan struct{})
	for _, op := range ops {
		op := op
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := apply(op); err != nil {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

func assignConcurrentSequences(ops []Operation, seq *int) []Operation {
	out := make([]Operation, len(ops))
	copy(out, ops)
	for i := len(out) - 1; i >= 0; i-- {
		switch out[i].Kind {
		case OpAppendEvent, OpRetryEvent:
			if out[i].Event == nil {
				continue
			}
			eventCopy := *out[i].Event
			eventCopy.UseSequence = true
			eventCopy.Sequence = *seq
			out[i].Event = &eventCopy
			*seq++
		case OpConcurrent:
			out[i].Concurrent = assignConcurrentSequences(out[i].Concurrent, seq)
		}
	}
	return out
}

func probeEventPage(
	ctx context.Context,
	svc session.Service,
	key session.Key,
	backend Backend,
	snapshot *Snapshot,
) error {
	if snapshot == nil || len(snapshot.Events) < 2 {
		return nil
	}
	got, err := svc.GetSession(ctx, key, session.WithGetSessionEventPage(0, 1))
	if err != nil {
		if errors.Is(err, session.ErrEventPageUnsupported) {
			addUnsupported(snapshot, CapabilityEventPage, backend.Unsupported(CapabilityEventPage))
			return nil
		}
		return err
	}
	if !backend.Supports(CapabilityEventPage) {
		addUnsupported(snapshot, CapabilityEventPage, backend.Unsupported(CapabilityEventPage))
		return nil
	}
	if got == nil {
		return fmt.Errorf("event page probe returned nil session")
	}
	events := got.GetEvents()
	if len(events) != 1 {
		return fmt.Errorf("event page probe returned %d events, want 1", len(events))
	}
	pageEvent := normalizeEvent(snapshot.Events[len(snapshot.Events)-1].Index, events[0])
	if !reflect.DeepEqual(pageEvent, snapshot.Events[len(snapshot.Events)-1]) {
		return fmt.Errorf("event page probe returned wrong latest event")
	}
	return nil
}

func probeTTL(
	ctx context.Context,
	backend Backend,
	snapshot *Snapshot,
	requested bool,
	probe func(context.Context) error,
) error {
	if snapshot == nil {
		return nil
	}
	if !backend.Supports(CapabilityTTL) {
		addUnsupported(snapshot, CapabilityTTL, backend.Unsupported(CapabilityTTL))
		return nil
	}
	if !requested {
		return nil
	}
	if probe == nil {
		return fmt.Errorf("backend %s declares TTL support but does not provide a TTL probe", backend.Name())
	}
	if err := probe(ctx); err != nil {
		return fmt.Errorf("backend %s TTL probe failed: %w", backend.Name(), err)
	}
	return nil
}

// ProbeSessionTTLExpiration verifies that a service configured with a short
// session TTL hides an expired session on subsequent reads.
func ProbeSessionTTLExpiration(
	ctx context.Context,
	svc session.Service,
	key session.Key,
	wait time.Duration,
) error {
	return ProbeSessionTTLExpirationWithAdvance(ctx, svc, key, wait, nil)
}

// ProbeSessionTTLExpirationWithAdvance is like ProbeSessionTTLExpiration, but
// lets tests backed by fake clocks advance backend time instead of sleeping.
func ProbeSessionTTLExpirationWithAdvance(
	ctx context.Context,
	svc session.Service,
	key session.Key,
	wait time.Duration,
	advance func(time.Duration),
) error {
	if svc == nil {
		return fmt.Errorf("ttl probe requires a session service")
	}
	if wait <= 0 {
		wait = 200 * time.Millisecond
	}
	if _, err := svc.CreateSession(ctx, key, session.StateMap{
		"ttl_probe": []byte(`"alive"`),
	}); err != nil {
		return fmt.Errorf("create ttl probe session: %w", err)
	}
	got, err := svc.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("read ttl probe session before expiry: %w", err)
	}
	if got == nil {
		return fmt.Errorf("ttl probe session expired before wait")
	}
	if advance != nil {
		advance(wait)
	} else {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	got, err = svc.GetSession(ctx, key)
	if err != nil {
		return fmt.Errorf("read ttl probe session after expiry: %w", err)
	}
	if got != nil {
		return fmt.Errorf("ttl probe session still readable after %s", wait)
	}
	return nil
}

func addUnsupported(snapshot *Snapshot, cap Capability, explanation string) {
	if snapshot == nil || cap == "" {
		return
	}
	for _, feature := range snapshot.Unsupported {
		if feature.Capability == cap {
			return
		}
	}
	if explanation == "" {
		explanation = "capability is not exposed by this replay backend"
	}
	snapshot.Unsupported = append(snapshot.Unsupported, UnsupportedFeature{
		Capability:  cap,
		AllowedDiff: true,
		Explanation: explanation,
	})
}

func appendEventOnce(
	ctx context.Context,
	svc session.Service,
	sess *session.Session,
	spec *EventSpec,
	seen map[string]struct{},
	seenMu *sync.Mutex,
	seq *int,
) error {
	if spec == nil {
		return nil
	}
	sequence, ok := reserveEventSequence(spec, seen, seenMu, seq)
	if !ok {
		return nil
	}
	evt, err := eventFromSpec(*spec, sequence)
	if err != nil {
		return err
	}
	return svc.AppendEvent(ctx, sess, evt)
}

func appendEventRetry(
	ctx context.Context,
	svc session.Service,
	sess *session.Session,
	spec *EventSpec,
	seen map[string]struct{},
	seenMu *sync.Mutex,
	seq *int,
) error {
	if spec == nil {
		return nil
	}
	sequence, ok := reserveEventSequence(spec, seen, seenMu, seq)
	if !ok {
		return nil
	}
	attempt := *spec
	attempt.Partial = true
	if err := appendEventAttempt(ctx, svc, sess, attempt, sequence); err != nil {
		return err
	}
	return appendEventAttempt(ctx, svc, sess, *spec, sequence)
}

func appendEventAttempt(
	ctx context.Context,
	svc session.Service,
	sess *session.Session,
	spec EventSpec,
	sequence int,
) error {
	evt, err := eventFromSpec(spec, sequence)
	if err != nil {
		return err
	}
	return svc.AppendEvent(ctx, sess, evt)
}

func appendFileEvent(
	sess *session.Session,
	spec *EventSpec,
	seen map[string]struct{},
	seenMu *sync.Mutex,
	seq *int,
) error {
	if spec == nil {
		return nil
	}
	sequence, ok := reserveEventSequence(spec, seen, seenMu, seq)
	if !ok {
		return nil
	}
	evt, err := eventFromSpec(*spec, sequence)
	if err != nil {
		return err
	}
	sess.UpdateUserSession(evt)
	return nil
}

func appendFileEventRetry(
	sess *session.Session,
	spec *EventSpec,
	seen map[string]struct{},
	seenMu *sync.Mutex,
	seq *int,
) error {
	if spec == nil {
		return nil
	}
	sequence, ok := reserveEventSequence(spec, seen, seenMu, seq)
	if !ok {
		return nil
	}
	attempt := *spec
	attempt.Partial = true
	if err := appendFileEventAttempt(sess, attempt, sequence); err != nil {
		return err
	}
	return appendFileEventAttempt(sess, *spec, sequence)
}

func appendFileEventAttempt(sess *session.Session, spec EventSpec, sequence int) error {
	evt, err := eventFromSpec(spec, sequence)
	if err != nil {
		return err
	}
	sess.UpdateUserSession(evt)
	return nil
}

func reserveEventSequence(
	spec *EventSpec,
	seen map[string]struct{},
	seenMu *sync.Mutex,
	seq *int,
) (int, bool) {
	if seenMu != nil {
		seenMu.Lock()
		defer seenMu.Unlock()
	}
	if _, ok := seen[spec.LogicalID]; ok {
		return 0, false
	}
	sequence := *seq
	if spec.UseSequence {
		sequence = spec.Sequence
	} else {
		*seq++
	}
	seen[spec.LogicalID] = struct{}{}
	return sequence, true
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
			Timestamp: deterministicSummaryTime(prev),
		})
	}
	input = append(input, delta...)
	tmp := session.NewSession(sess.AppName, sess.UserID, sess.ID+":"+filterKey, session.WithSessionEvents(input))
	text, _ := deterministicSummarizer{}.Summarize(context.Background(), tmp)
	latest := latestBoundary(filterKey, delta)
	if latest == nil {
		latest = session.NewSummaryBoundary(filterKey, deterministicSummaryTime(prev))
	}
	sess.Summaries[filterKey] = &session.Summary{
		Summary:   text,
		UpdatedAt: latest.CutoffTime(),
		Boundary:  latest,
	}
}

func deterministicSummaryTime(prev *session.Summary) time.Time {
	if prev != nil {
		if boundary := prev.CutoffBoundary(); boundary != nil && !boundary.CutoffTime().IsZero() {
			return boundary.CutoffTime()
		}
		if !prev.UpdatedAt.IsZero() {
			return prev.UpdatedAt.UTC()
		}
	}
	return deterministicEventTime(0)
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
	metadata := normalizedMemoryMetadata(m.Kind, m.EventTime, m.Participants, m.Location)
	return &memory.Entry{
		ID:        stableMemoryID(key.AppName, key.UserID, spec.Content, topics, metadata),
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

func trackEventFromSpec(spec *TrackSpec) (*session.TrackEvent, error) {
	raw, err := json.Marshal(spec.Payload)
	if err != nil {
		return nil, err
	}
	ts := spec.Timestamp
	if ts.IsZero() {
		ts = deterministicEventTime(0)
	}
	return &session.TrackEvent{
		Track:     spec.Name,
		Payload:   raw,
		Timestamp: ts,
	}, nil
}
