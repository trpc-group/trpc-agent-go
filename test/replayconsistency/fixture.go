//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

const (
	replayAppName                 = "replaytest"
	replayUserID                  = "user-1"
	filterKeyMain                 = "branch/main"
	clickHouseBackend             = "clickhouse"
	toolResponseExtraExtensionKey = "replaytest.tool_response_extra"
	fixtureCleanupTimeout         = 5 * time.Second
	serviceCloseOperationCount    = 2
)

var replayScopeSequence atomic.Uint64

// memoryTimeBase is the deterministic base for replay memory timestamps.
// Memory services generate wall-clock timestamps that differ between backends
// and platforms (for example, coarse clocks collapse fast in-memory writes to
// a single tick), so the fixture assigns a deterministic write-order time to
// every memory entry. Cross-backend memory chronology is then comparable
// after normalization.
var memoryTimeBase = time.Unix(1700000000, 0).UTC()

var errReplayFixtureClosed = errors.New("replay fixture is closed")

type replayFixture struct {
	lifecycleMu       sync.RWMutex
	closed            bool
	closeErr          error
	mu                sync.Mutex
	summaryMu         sync.Mutex
	name              string
	sessionService    session.Service
	memoryService     memory.Service
	summarizer        *replaySummarizer
	capabilities      replaytest.CapabilitySet
	appName           string
	userID            string
	sessionIDs        map[string]struct{}
	cleanupSessionIDs map[string]struct{}
	replayWindows     map[string]string
	memoryScopes      map[replaytest.MemoryScope]memory.UserKey
	stateDeletes      map[string]map[string]struct{}
	stateWriteLocks   map[string]*sync.Mutex
	searches          []replaytest.MemorySearchSnapshot
	memoryWriteTimes  map[string]time.Time
	memoryWriteOrder  int
}

type replayFixtureConfig struct {
	name           string
	sessionService session.Service
	memoryService  memory.Service
	summarizer     *replaySummarizer
	supported      []replaytest.Capability
	unsupported    []replaytest.Capability
}

func newReplayFixture(config replayFixtureConfig) *replayFixture {
	scopeID := fmt.Sprintf(
		"%d-%d-%d", os.Getpid(), time.Now().UnixNano(), replayScopeSequence.Add(1),
	)
	capabilities := replaytest.CapabilitySet{
		replaytest.CapabilitySession:       true,
		replaytest.CapabilityMemory:        true,
		replaytest.CapabilitySummary:       true,
		replaytest.CapabilityTrack:         true,
		replaytest.CapabilitySessionPaging: true,
		replaytest.CapabilityTTL:           true,
		replaytest.CapabilityMemorySearch:  true,
	}
	for _, capability := range config.supported {
		capabilities[capability] = true
	}
	for _, capability := range config.unsupported {
		capabilities[capability] = false
	}
	return &replayFixture{
		name:              config.name,
		sessionService:    config.sessionService,
		memoryService:     config.memoryService,
		summarizer:        config.summarizer,
		capabilities:      capabilities,
		appName:           replayAppName + "-" + scopeID,
		userID:            replayUserID + "-" + scopeID,
		sessionIDs:        make(map[string]struct{}),
		cleanupSessionIDs: make(map[string]struct{}),
		replayWindows:     make(map[string]string),
		memoryScopes:      make(map[replaytest.MemoryScope]memory.UserKey),
		stateDeletes:      make(map[string]map[string]struct{}),
		stateWriteLocks:   make(map[string]*sync.Mutex),
		memoryWriteTimes:  make(map[string]time.Time),
	}
}

func (fixture *replayFixture) Name() string {
	return fixture.name
}

func (fixture *replayFixture) Capabilities() replaytest.CapabilitySet {
	return fixture.capabilities
}

func (fixture *replayFixture) Apply(ctx context.Context, operation replaytest.Operation) error {
	fixture.lifecycleMu.RLock()
	defer fixture.lifecycleMu.RUnlock()
	if fixture.closed {
		return errReplayFixtureClosed
	}
	return fixture.apply(ctx, operation)
}

func (fixture *replayFixture) ApplyWithFault(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	fixture.lifecycleMu.RLock()
	defer fixture.lifecycleMu.RUnlock()
	if fixture.closed {
		return errReplayFixtureClosed
	}
	if operation.FailurePoint == replaytest.FailureAfterWrite {
		if err := fixture.apply(ctx, operation); err != nil {
			return fmt.Errorf("apply before injected failure: %w", err)
		}
	}
	return fmt.Errorf("%w: %s", replaytest.ErrInjectedFailure, operation.InjectedFailure)
}

func (fixture *replayFixture) apply(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	switch operation.Kind {
	case replaytest.OperationCreateSession:
		return fixture.applyCreateSession(ctx, operation)
	case replaytest.OperationAppendEvent:
		return fixture.applyAppendEvent(ctx, operation)
	case replaytest.OperationUpdateState:
		return fixture.applyUpdateState(ctx, operation)
	case replaytest.OperationWriteMemory:
		return fixture.applyWriteMemory(ctx, operation)
	case replaytest.OperationSearchMemory:
		return fixture.applySearchMemory(ctx, operation)
	case replaytest.OperationUpdateSummary:
		return fixture.applyUpdateSummary(ctx, operation)
	case replaytest.OperationSetReplayWindow:
		fixture.mu.Lock()
		fixture.replayWindows[operation.SessionID] = operation.ReplayWindowFilterKey
		fixture.mu.Unlock()
		return nil
	case replaytest.OperationAppendTrack:
		return fixture.applyAppendTrack(ctx, operation)
	default:
		return fmt.Errorf("unsupported operation %q", operation.Kind)
	}
}

func (fixture *replayFixture) applyCreateSession(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	fixture.mu.Lock()
	fixture.cleanupSessionIDs[operation.SessionID] = struct{}{}
	fixture.mu.Unlock()
	_, err := fixture.sessionService.CreateSession(ctx, fixture.sessionKey(operation.SessionID), nil)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	fixture.mu.Lock()
	fixture.sessionIDs[operation.SessionID] = struct{}{}
	fixture.mu.Unlock()
	return nil
}

func (fixture *replayFixture) applyAppendEvent(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	sess, err := fixture.getSession(ctx, operation.SessionID)
	if err != nil {
		return err
	}
	evt, err := toEvent(operation.Event)
	if err != nil {
		return err
	}
	if err := fixture.sessionService.AppendEvent(ctx, sess, evt); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

func (fixture *replayFixture) applyUpdateState(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	stateLock := fixture.stateWriteLock(operation.SessionID)
	stateLock.Lock()
	defer stateLock.Unlock()
	state, err := toStateMap(operation.StateUpdates, operation.StateDeletes)
	if err != nil {
		return err
	}
	if err := fixture.sessionService.UpdateSessionState(
		ctx, fixture.sessionKey(operation.SessionID), state,
	); err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	fixture.recordStateDeletes(operation)
	return nil
}

func (fixture *replayFixture) applyWriteMemory(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	metadata, err := toMemoryMetadata(operation.Memory.Metadata)
	if err != nil {
		return err
	}
	if err := fixture.memoryService.AddMemory(
		ctx,
		fixture.memoryKey(operation.Memory.AppName, operation.Memory.UserID),
		operation.Memory.Content,
		operation.Memory.Topics,
		memory.WithMetadata(metadata),
	); err != nil {
		return fmt.Errorf("write memory: %w", err)
	}
	fixture.mu.Lock()
	fixture.memoryWriteOrder++
	fixture.memoryWriteTimes[operation.Memory.Content] =
		memoryTimeBase.Add(time.Duration(fixture.memoryWriteOrder) * time.Millisecond)
	fixture.mu.Unlock()
	return nil
}

func (fixture *replayFixture) applySearchMemory(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	logicalScope := replaytest.MemoryScope{
		AppName: operation.SearchAppName,
		UserID:  operation.SearchUserID,
	}
	physicalScope := fixture.memoryKey(logicalScope.AppName, logicalScope.UserID)
	results, err := fixture.memoryService.SearchMemories(
		ctx,
		physicalScope,
		operation.SearchQuery,
		memory.WithSearchOptions(memory.SearchOptions{
			Query:               operation.SearchQuery,
			MaxResults:          operation.SearchLimit,
			SimilarityThreshold: operation.SearchMinScore,
		}),
	)
	if err != nil {
		return fmt.Errorf("search memories: %w", err)
	}
	search := replaytest.MemorySearchSnapshot{
		AppName: logicalScope.AppName,
		UserID:  logicalScope.UserID,
		Query:   operation.SearchQuery,
	}
	for _, entry := range results {
		if err := validatePhysicalMemoryScope(entry, physicalScope); err != nil {
			return fmt.Errorf("search memories for %#v: %w", logicalScope, err)
		}
		search.Results = append(search.Results, fixture.toLogicalMemorySnapshot(entry, logicalScope))
	}
	fixture.mu.Lock()
	fixture.searches = append(fixture.searches, cloneMemorySearchSnapshot(search))
	fixture.mu.Unlock()
	return nil
}

func (fixture *replayFixture) applyUpdateSummary(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	fixture.summaryMu.Lock()
	defer fixture.summaryMu.Unlock()
	sess, err := fixture.getSession(ctx, operation.SessionID)
	if err != nil {
		return err
	}
	fixture.summarizer.SetNext(operation.Summary.Text)
	if err := fixture.sessionService.CreateSessionSummary(
		ctx, sess, operation.Summary.FilterKey, true,
	); err != nil {
		return fmt.Errorf("update session summary: %w", err)
	}
	return nil
}

func (fixture *replayFixture) applyAppendTrack(
	ctx context.Context,
	operation replaytest.Operation,
) error {
	trackService, ok := fixture.sessionService.(session.TrackService)
	if !ok {
		return fmt.Errorf("session service does not implement track service")
	}
	sess, err := fixture.getSession(ctx, operation.SessionID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(trackPayload{
		EventType: operation.TrackEvent.EventType, InvocationID: operation.TrackEvent.InvocationID,
		Payload: operation.TrackEvent.Payload, Error: operation.TrackEvent.Error,
		Duration: operation.TrackEvent.Duration,
	})
	if err != nil {
		return fmt.Errorf("marshal track payload: %w", err)
	}
	trackEvent := &session.TrackEvent{
		Track: session.Track(operation.TrackName), Payload: payload,
		Timestamp: operation.TrackEvent.Timestamp,
	}
	if err := trackService.AppendTrackEvent(ctx, sess, trackEvent); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}
	return nil
}

func (fixture *replayFixture) Snapshot(ctx context.Context) (replaytest.Snapshot, error) {
	fixture.lifecycleMu.RLock()
	defer fixture.lifecycleMu.RUnlock()
	if fixture.closed {
		return replaytest.Snapshot{}, errReplayFixtureClosed
	}
	bookkeeping := fixture.snapshotBookkeeping()
	var snapshot replaytest.Snapshot
	for _, id := range bookkeeping.sessionIDs {
		sess, err := fixture.getSession(ctx, id)
		if err != nil {
			return replaytest.Snapshot{}, err
		}
		rawState := sess.SnapshotState()
		sessionSnapshot, err := toSessionSnapshot(
			sess,
			rawState,
			bookkeeping.replayWindows[id],
			fixture.name == clickHouseBackend,
		)
		if err != nil {
			return replaytest.Snapshot{}, err
		}
		normalizeDeletedState(
			sessionSnapshot.State, bookkeeping.stateDeletes[id], stateTombstones(rawState),
		)
		snapshot.Sessions = append(snapshot.Sessions, sessionSnapshot)
	}
	for _, scope := range bookkeeping.memoryScopes {
		entries, err := fixture.memoryService.ReadMemories(ctx, scope.physical, 0)
		if err != nil {
			return replaytest.Snapshot{}, fmt.Errorf("read memories for %#v: %w", scope.logical, err)
		}
		for _, entry := range entries {
			if err := validatePhysicalMemoryScope(entry, scope.physical); err != nil {
				return replaytest.Snapshot{}, fmt.Errorf("read memories for %#v: %w", scope.logical, err)
			}
			snapshot.Memories = append(
				snapshot.Memories, fixture.toLogicalMemorySnapshot(entry, scope.logical),
			)
		}
	}
	snapshot.MemorySearches = bookkeeping.searches
	return snapshot, nil
}

type fixtureBookkeeping struct {
	sessionIDs        []string
	cleanupSessionIDs []string
	replayWindows     map[string]string
	memoryScopes      []memoryScopeBinding
	stateDeletes      map[string]map[string]struct{}
	searches          []replaytest.MemorySearchSnapshot
}

type memoryScopeBinding struct {
	logical  replaytest.MemoryScope
	physical memory.UserKey
}

func (fixture *replayFixture) snapshotBookkeeping() fixtureBookkeeping {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	bookkeeping := fixtureBookkeeping{
		sessionIDs:        make([]string, 0, len(fixture.sessionIDs)),
		cleanupSessionIDs: make([]string, 0, len(fixture.cleanupSessionIDs)),
		replayWindows:     make(map[string]string, len(fixture.replayWindows)),
		memoryScopes:      make([]memoryScopeBinding, 0, len(fixture.memoryScopes)),
		stateDeletes:      make(map[string]map[string]struct{}, len(fixture.stateDeletes)),
		searches:          cloneMemorySearchSnapshots(fixture.searches),
	}
	for id := range fixture.sessionIDs {
		bookkeeping.sessionIDs = append(bookkeeping.sessionIDs, id)
	}
	for id := range fixture.cleanupSessionIDs {
		bookkeeping.cleanupSessionIDs = append(bookkeeping.cleanupSessionIDs, id)
	}
	for id, filterKey := range fixture.replayWindows {
		bookkeeping.replayWindows[id] = filterKey
	}
	for logical, physical := range fixture.memoryScopes {
		bookkeeping.memoryScopes = append(bookkeeping.memoryScopes, memoryScopeBinding{
			logical: logical, physical: physical,
		})
	}
	for id, keys := range fixture.stateDeletes {
		bookkeeping.stateDeletes[id] = make(map[string]struct{}, len(keys))
		for key := range keys {
			bookkeeping.stateDeletes[id][key] = struct{}{}
		}
	}
	sort.Strings(bookkeeping.sessionIDs)
	sort.Strings(bookkeeping.cleanupSessionIDs)
	sort.Slice(bookkeeping.memoryScopes, func(i, j int) bool {
		left, right := bookkeeping.memoryScopes[i].logical, bookkeeping.memoryScopes[j].logical
		if left.AppName != right.AppName {
			return left.AppName < right.AppName
		}
		return left.UserID < right.UserID
	})
	return bookkeeping
}

func (fixture *replayFixture) recordStateDeletes(operation replaytest.Operation) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	keys := fixture.stateDeletes[operation.SessionID]
	if keys == nil {
		keys = make(map[string]struct{})
		fixture.stateDeletes[operation.SessionID] = keys
	}
	for key := range operation.StateUpdates {
		delete(keys, key)
	}
	for _, key := range operation.StateDeletes {
		keys[key] = struct{}{}
	}
}

func (fixture *replayFixture) stateWriteLock(sessionID string) *sync.Mutex {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	stateLock := fixture.stateWriteLocks[sessionID]
	if stateLock == nil {
		stateLock = &sync.Mutex{}
		fixture.stateWriteLocks[sessionID] = stateLock
	}
	return stateLock
}

func normalizeDeletedState(
	state map[string]replaytest.StateValueSnapshot,
	deleted map[string]struct{},
	tombstones map[string]struct{},
) {
	for key := range deleted {
		if _, tombstone := tombstones[key]; tombstone {
			delete(state, key)
		}
	}
}

func stateTombstones(state session.StateMap) map[string]struct{} {
	tombstones := make(map[string]struct{})
	for key, value := range state {
		if value == nil {
			tombstones[key] = struct{}{}
		}
	}
	return tombstones
}

func (fixture *replayFixture) Close() error {
	fixture.lifecycleMu.Lock()
	defer fixture.lifecycleMu.Unlock()
	if fixture.closed {
		return fixture.closeErr
	}
	fixture.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), fixtureCleanupTimeout)
	defer cancel()
	bookkeeping := fixture.snapshotBookkeeping()
	cleanupErrors := make([]error, 0,
		len(bookkeeping.cleanupSessionIDs)+len(bookkeeping.memoryScopes)+serviceCloseOperationCount)
	for _, id := range bookkeeping.cleanupSessionIDs {
		cleanupErrors = append(cleanupErrors, fixture.sessionService.DeleteSession(
			ctx, fixture.sessionKey(id),
		))
	}
	for _, scope := range bookkeeping.memoryScopes {
		cleanupErrors = append(cleanupErrors, fixture.memoryService.ClearMemories(ctx, scope.physical))
	}
	cleanupErrors = append(cleanupErrors, fixture.sessionService.Close(), fixture.memoryService.Close())
	fixture.closeErr = errors.Join(cleanupErrors...)
	return fixture.closeErr
}

func (fixture *replayFixture) getSession(
	ctx context.Context,
	sessionID string,
) (*session.Session, error) {
	key := fixture.sessionKey(sessionID)
	sess, err := fixture.sessionService.GetSession(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	if err := validatePhysicalSessionScope(sess, key); err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return sess, nil
}

func (fixture *replayFixture) sessionKey(sessionID string) session.Key {
	return session.Key{AppName: fixture.appName, UserID: fixture.userID, SessionID: sessionID}
}

func (fixture *replayFixture) memoryKey(appName, userID string) memory.UserKey {
	logical := replaytest.MemoryScope{AppName: appName, UserID: userID}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if key, ok := fixture.memoryScopes[logical]; ok {
		return key
	}
	index := len(fixture.memoryScopes) + 1
	key := memory.UserKey{
		AppName: fmt.Sprintf("%s-memory-%d", fixture.appName, index),
		UserID:  fmt.Sprintf("%s-memory-%d", fixture.userID, index),
	}
	fixture.memoryScopes[logical] = key
	return key
}

func toStateMap(updates map[string]any, deletes []string) (session.StateMap, error) {
	state := make(session.StateMap, len(updates)+len(deletes))
	for key, value := range updates {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal state %q: %w", key, err)
		}
		state[key] = encoded
	}
	for _, key := range deletes {
		state[key] = nil
	}
	return state, nil
}

func toSnapshotStateMap(
	values map[string]replaytest.StateValueSnapshot,
) (session.StateMap, error) {
	if values == nil {
		return nil, nil
	}
	state := make(session.StateMap, len(values))
	for key, value := range values {
		encoded, err := encodeSnapshotStateValue(value)
		if err != nil {
			return nil, fmt.Errorf("encode state %q: %w", key, err)
		}
		state[key] = encoded
	}
	return state, nil
}

func encodeSnapshotStateValue(value replaytest.StateValueSnapshot) ([]byte, error) {
	switch value.Kind {
	case replaytest.StateValueNull:
		if value.Value != nil {
			return nil, errors.New("null state must not contain a value")
		}
		return []byte("null"), nil
	case replaytest.StateValueJSON:
		encoded, err := json.Marshal(value.Value)
		if err != nil {
			return nil, fmt.Errorf("marshal JSON state: %w", err)
		}
		return encoded, nil
	case replaytest.StateValueText:
		text, ok := value.Value.(string)
		if !ok {
			return nil, fmt.Errorf("text state has type %T", value.Value)
		}
		return []byte(text), nil
	case replaytest.StateValueBinary:
		binary, ok := value.Value.([]byte)
		if !ok {
			return nil, fmt.Errorf("binary state has type %T", value.Value)
		}
		return append([]byte(nil), binary...), nil
	default:
		return nil, fmt.Errorf("unknown state kind %q", value.Kind)
	}
}

func encodeJSONLikeRaw(value any) (json.RawMessage, error) {
	prepared, err := prepareJSONLikeRaw(value)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func prepareJSONLikeRawFields(values map[string]any) (map[string]any, error) {
	if values == nil {
		return nil, nil
	}
	prepared, err := prepareJSONLikeRaw(values)
	if err != nil {
		return nil, err
	}
	fields, ok := prepared.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("extra fields have type %T", prepared)
	}
	return fields, nil
}

func prepareJSONLikeRaw(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	preparer := jsonLikeRawPreparer{
		visiting: make(map[jsonLikeRawReference]struct{}),
	}
	return preparer.prepareValue(reflect.ValueOf(value))
}

type jsonLikeRawReference struct {
	typeOf   reflect.Type
	kind     reflect.Kind
	pointer  uintptr
	length   int
	capacity int
}

type jsonLikeRawPreparer struct {
	visiting map[jsonLikeRawReference]struct{}
}

func (preparer jsonLikeRawPreparer) prepareValue(value reflect.Value) (any, error) {
	if !value.IsValid() {
		return nil, nil
	}
	if value.CanInterface() {
		switch typed := value.Interface().(type) {
		case json.RawMessage:
			return validRawJSON(typed)
		case []byte:
			return validRawJSON(typed)
		}
		if value.Kind() != reflect.Pointer ||
			value.IsNil() || !isJSONLikeRawPointer(value) {
			if _, ok := value.Interface().(json.Marshaler); ok {
				return value.Interface(), nil
			}
		}
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil, nil
		}
		return preparer.prepareValue(value.Elem())
	case reflect.Pointer:
		return preparer.preparePointer(value)
	case reflect.Map:
		return preparer.prepareMap(value)
	case reflect.Slice, reflect.Array:
		return preparer.prepareSlice(value)
	default:
		if value.CanInterface() {
			return value.Interface(), nil
		}
		return fmt.Sprint(value), nil
	}
}

func (preparer jsonLikeRawPreparer) preparePointer(value reflect.Value) (any, error) {
	if value.IsNil() {
		return nil, nil
	}
	if isJSONLikeRawPointer(value) {
		reference := pointerJSONLikeRawReference(value)
		if err := preparer.enter(reference); err != nil {
			return nil, err
		}
		defer preparer.leave(reference)
		return preparer.prepareValue(value.Elem())
	}
	if value.CanInterface() {
		if _, ok := value.Interface().(json.Marshaler); ok {
			return value.Interface(), nil
		}
	}
	if !shouldPrepareJSONLikeRawPointer(value.Elem()) {
		if value.CanInterface() {
			return value.Interface(), nil
		}
		return fmt.Sprint(value), nil
	}
	reference := pointerJSONLikeRawReference(value)
	if err := preparer.enter(reference); err != nil {
		return nil, err
	}
	defer preparer.leave(reference)
	return preparer.prepareValue(value.Elem())
}

func isJSONLikeRawPointer(value reflect.Value) bool {
	if !value.Elem().CanInterface() {
		return false
	}
	switch value.Elem().Interface().(type) {
	case json.RawMessage, []byte:
		return true
	default:
		return false
	}
}

func shouldPrepareJSONLikeRawPointer(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	if value.CanInterface() {
		switch value.Interface().(type) {
		case json.RawMessage, []byte:
			return true
		}
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Map, reflect.Pointer,
		reflect.Slice, reflect.Array:
		return true
	default:
		return false
	}
}

func (preparer jsonLikeRawPreparer) prepareMap(value reflect.Value) (map[string]any, error) {
	if value.IsNil() {
		return nil, nil
	}
	if value.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("map key type %s is not supported", value.Type().Key())
	}
	reference := mapJSONLikeRawReference(value)
	if err := preparer.enter(reference); err != nil {
		return nil, err
	}
	defer preparer.leave(reference)
	prepared := make(map[string]any, value.Len())
	iterator := value.MapRange()
	for iterator.Next() {
		item, err := preparer.prepareValue(iterator.Value())
		if err != nil {
			return nil, err
		}
		prepared[iterator.Key().String()] = item
	}
	return prepared, nil
}

func (preparer jsonLikeRawPreparer) prepareSlice(value reflect.Value) ([]any, error) {
	if value.Kind() == reflect.Slice && value.IsNil() {
		return nil, nil
	}
	var reference jsonLikeRawReference
	if value.Kind() == reflect.Slice {
		reference = sliceJSONLikeRawReference(value)
		if err := preparer.enter(reference); err != nil {
			return nil, err
		}
		defer preparer.leave(reference)
	}
	prepared := make([]any, value.Len())
	for i := 0; i < value.Len(); i++ {
		item, err := preparer.prepareValue(value.Index(i))
		if err != nil {
			return nil, err
		}
		prepared[i] = item
	}
	return prepared, nil
}

func (preparer jsonLikeRawPreparer) enter(reference jsonLikeRawReference) error {
	if _, ok := preparer.visiting[reference]; ok {
		return fmt.Errorf("cyclic JSON value contains %s", reference.kind)
	}
	preparer.visiting[reference] = struct{}{}
	return nil
}

func (preparer jsonLikeRawPreparer) leave(reference jsonLikeRawReference) {
	delete(preparer.visiting, reference)
}

func mapJSONLikeRawReference(value reflect.Value) jsonLikeRawReference {
	return jsonLikeRawReference{
		typeOf: value.Type(), kind: value.Kind(), pointer: value.Pointer(),
	}
}

func pointerJSONLikeRawReference(value reflect.Value) jsonLikeRawReference {
	return jsonLikeRawReference{
		typeOf: value.Type(), kind: value.Kind(), pointer: value.Pointer(),
	}
}

func sliceJSONLikeRawReference(value reflect.Value) jsonLikeRawReference {
	return jsonLikeRawReference{
		typeOf: value.Type(), kind: value.Kind(), pointer: value.Pointer(),
		length: value.Len(), capacity: value.Cap(),
	}
}

func validRawJSON(raw []byte) (json.RawMessage, error) {
	if raw == nil {
		return nil, nil
	}
	var decoded any
	if err := decodeJSONUseNumber(raw, &decoded); err != nil {
		return nil, fmt.Errorf("invalid raw JSON: %w", err)
	}
	return json.RawMessage(append([]byte(nil), raw...)), nil
}

func toEvent(snapshot *replaytest.EventSnapshot) (*event.Event, error) {
	message := model.Message{
		Role:    model.Role(snapshot.Role),
		Content: snapshot.Content,
	}
	for _, call := range snapshot.ToolCalls {
		arguments, err := encodeJSONLikeRaw(call.Arguments)
		if err != nil {
			return nil, fmt.Errorf("marshal tool call %q arguments: %w", call.ID, err)
		}
		extra, err := prepareJSONLikeRawFields(call.Extra)
		if err != nil {
			return nil, fmt.Errorf("marshal tool call %q extra: %w", call.ID, err)
		}
		message.ToolCalls = append(message.ToolCalls, model.ToolCall{
			Type: "function",
			ID:   call.ID,
			Function: model.FunctionDefinitionParam{
				Name:      call.Name,
				Arguments: arguments,
			},
			ExtraFields: extra,
		})
	}
	if snapshot.ToolResponse != nil {
		message.ToolID = snapshot.ToolResponse.ToolCallID
		message.ToolName = snapshot.ToolResponse.Name
		message.Content = snapshot.ToolResponse.Content
		message.Role = model.RoleTool
	}
	extensions := make(map[string]json.RawMessage, len(snapshot.Extensions)+1)
	for key, value := range snapshot.Extensions {
		encoded, err := encodeJSONLikeRaw(value)
		if err != nil {
			return nil, fmt.Errorf("marshal event extension %q: %w", key, err)
		}
		extensions[key] = encoded
	}
	if hasEncodedReservedToolResponseExtraExtension(extensions) {
		return nil, fmt.Errorf(
			"event extension %q is reserved", toolResponseExtraExtensionKey,
		)
	}
	if snapshot.ToolResponse != nil && len(snapshot.ToolResponse.Extra) > 0 {
		encoded, err := encodeJSONLikeRaw(snapshot.ToolResponse.Extra)
		if err != nil {
			return nil, fmt.Errorf("marshal tool response extra: %w", err)
		}
		extensions[toolResponseExtraExtensionKey] = encoded
	}
	invocationID := snapshot.InvocationID
	evt := event.NewResponseEvent(invocationID, snapshot.Author, &model.Response{
		Object:    snapshot.Object,
		Done:      snapshot.Done,
		Timestamp: snapshot.Timestamp,
		Choices:   []model.Choice{{Message: message}},
	})
	evt.ID = snapshot.ID
	evt.Timestamp = snapshot.Timestamp
	evt.Branch = snapshot.Branch
	evt.Tag = snapshot.Tag
	evt.FilterKey = snapshot.FilterKey
	stateDelta, err := toSnapshotStateMap(snapshot.StateDelta)
	if err != nil {
		return nil, fmt.Errorf("encode event state delta: %w", err)
	}
	evt.StateDelta = stateDelta
	evt.Extensions = extensions
	return evt, nil
}

func toSessionSnapshot(
	sess *session.Session,
	rawState session.StateMap,
	replayWindowFilterKey string,
	allowNestedToolResponseExtra bool,
) (
	replaytest.SessionSnapshot,
	error,
) {
	snapshot := replaytest.SessionSnapshot{
		ID:        sess.ID,
		AppName:   sess.AppName,
		UserID:    sess.UserID,
		CreatedAt: sess.CreatedAt,
		UpdatedAt: sess.UpdatedAt,
		State:     make(map[string]replaytest.StateValueSnapshot),
	}
	snapshot.AppName = replayAppName
	snapshot.UserID = replayUserID
	for key, value := range rawState {
		if isInternalSessionStateKey(key) {
			continue
		}
		snapshot.State[key] = decodeStateValue(value)
	}
	events := sess.GetEvents()
	windowStart, err := replayWindowStart(sess, events, replayWindowFilterKey)
	if err != nil {
		return replaytest.SessionSnapshot{}, err
	}
	for i := windowStart; i < len(events); i++ {
		snapshot.Events = append(
			snapshot.Events,
			toEventSnapshot(&events[i], allowNestedToolResponseExtra),
		)
	}
	sess.SummariesMu.RLock()
	for filterKey, summary := range sess.Summaries {
		if summary == nil {
			continue
		}
		item := replaytest.SummarySnapshot{
			SessionID: sess.ID,
			FilterKey: filterKey,
			Text:      summary.Summary,
			UpdatedAt: summary.UpdatedAt,
		}
		if summary.Boundary != nil {
			item.Version = summary.Boundary.Version
			item.Boundary = map[string]any{
				"filter_key":    summary.Boundary.FilterKey,
				"cutoff_at":     summary.Boundary.CutoffAt,
				"last_event_id": summary.Boundary.LastEventID,
			}
		}
		snapshot.Summaries = append(snapshot.Summaries, item)
	}
	sess.SummariesMu.RUnlock()
	sess.TracksMu.RLock()
	for name, events := range sess.Tracks {
		track := replaytest.TrackSnapshot{Name: string(name)}
		if events != nil {
			for _, trackEvent := range events.Events {
				var payload trackPayload
				if err := decodeJSONUseNumber(trackEvent.Payload, &payload); err != nil {
					sess.TracksMu.RUnlock()
					return replaytest.SessionSnapshot{}, fmt.Errorf("decode track payload: %w", err)
				}
				track.Events = append(track.Events, replaytest.TrackEventSnapshot{
					EventType:    payload.EventType,
					InvocationID: payload.InvocationID,
					Payload:      payload.Payload,
					Error:        payload.Error,
					Duration:     payload.Duration,
					Timestamp:    trackEvent.Timestamp,
				})
			}
		}
		snapshot.Tracks = append(snapshot.Tracks, track)
	}
	sess.TracksMu.RUnlock()
	return snapshot, nil
}

func isInternalSessionStateKey(key string) bool {
	return session.IsInternalStateKey(key)
}

func replayWindowStart(
	sess *session.Session,
	events []event.Event,
	filterKey string,
) (int, error) {
	if filterKey == "" {
		return 0, nil
	}
	sess.SummariesMu.RLock()
	summary := sess.Summaries[filterKey]
	var lastEventID string
	if summary != nil && summary.Boundary != nil {
		lastEventID = summary.Boundary.LastEventID
	}
	sess.SummariesMu.RUnlock()
	if lastEventID == "" {
		return 0, fmt.Errorf(
			"set replay window for session %q: summary %q has no event boundary",
			sess.ID, filterKey,
		)
	}
	for i := range events {
		if events[i].ID == lastEventID {
			return i + 1, nil
		}
	}
	return 0, fmt.Errorf(
		"set replay window for session %q: boundary event %q not found",
		sess.ID, lastEventID,
	)
}

func toEventSnapshot(
	evt *event.Event,
	allowNestedToolResponseExtra bool,
) replaytest.EventSnapshot {
	snapshot := replaytest.EventSnapshot{
		ID:           evt.ID,
		InvocationID: evt.InvocationID,
		Author:       evt.Author,
		Branch:       evt.Branch,
		Tag:          evt.Tag,
		FilterKey:    evt.FilterKey,
		Timestamp:    evt.Timestamp,
		StateDelta:   decodeStateMap(evt.StateDelta),
		Extensions:   decodeRawMap(evt.Extensions),
	}
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		if len(snapshot.Extensions) == 0 {
			snapshot.Extensions = nil
		}
		return snapshot
	}
	snapshot.Object = evt.Response.Object
	snapshot.Done = evt.Response.Done
	message := evt.Response.Choices[0].Message
	snapshot.Role = string(message.Role)
	snapshot.Content = message.Content
	for _, call := range message.ToolCalls {
		snapshot.ToolCalls = append(snapshot.ToolCalls, replaytest.ToolCallSnapshot{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: json.RawMessage(call.Function.Arguments),
			Extra:     call.ExtraFields,
		})
	}
	if message.ToolID != "" || message.Role == model.RoleTool {
		responseExtra := takeToolResponseExtra(snapshot.Extensions, allowNestedToolResponseExtra)
		snapshot.ToolResponse = &replaytest.ToolResponse{
			ToolCallID: message.ToolID,
			Name:       message.ToolName,
			Content:    message.Content,
			Extra:      responseExtra,
		}
	}
	if len(snapshot.Extensions) == 0 {
		snapshot.Extensions = nil
	}
	return snapshot
}

func hasEncodedReservedToolResponseExtraExtension(
	extensions map[string]json.RawMessage,
) bool {
	if _, exists := extensions[toolResponseExtraExtensionKey]; exists {
		return true
	}
	namespace, exists := extensions[replayAppName]
	return exists && hasToolResponseExtraKey(namespace)
}

func hasToolResponseExtraKey(value any) bool {
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() &&
		(reflected.Kind() == reflect.Interface || reflected.Kind() == reflect.Pointer) {
		if reflected.IsNil() {
			return false
		}
		reflected = reflected.Elem()
	}
	if reflected.IsValid() && reflected.CanInterface() {
		if decoded, ok := decodeJSONLikeObject(reflected.Interface()); ok {
			reflected = reflect.ValueOf(decoded)
		}
	}
	if !reflected.IsValid() || reflected.Kind() != reflect.Map ||
		reflected.Type().Key().Kind() != reflect.String {
		return false
	}
	key := reflect.ValueOf("tool_response_extra").Convert(reflected.Type().Key())
	return reflected.MapIndex(key).IsValid()
}

func decodeJSONLikeObject(value any) (map[string]any, bool) {
	var raw []byte
	switch typed := value.(type) {
	case json.RawMessage:
		raw = typed
	case []byte:
		raw = typed
	default:
		return nil, false
	}
	var decoded map[string]any
	if err := decodeJSONUseNumber(raw, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func takeToolResponseExtra(
	extensions map[string]any,
	allowNested bool,
) map[string]any {
	if extensions == nil {
		return nil
	}
	if value, ok := extensions[toolResponseExtraExtensionKey]; ok {
		responseExtra, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		delete(extensions, toolResponseExtraExtensionKey)
		return responseExtra
	}
	if !allowNested {
		return nil
	}
	namespace, ok := extensions[replayAppName].(map[string]any)
	if !ok {
		return nil
	}
	value, ok := namespace["tool_response_extra"]
	if !ok {
		return nil
	}
	responseExtra, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	delete(namespace, "tool_response_extra")
	if len(namespace) == 0 {
		delete(extensions, replayAppName)
	}
	return responseExtra
}

func decodeRawMap[T ~[]byte](values map[string]T) map[string]any {
	if values == nil {
		return nil
	}
	decoded := make(map[string]any, len(values))
	for key, value := range values {
		if value == nil {
			decoded[key] = nil
			continue
		}
		var item any
		if err := decodeJSONUseNumber(value, &item); err != nil {
			item = json.RawMessage(append([]byte(nil), value...))
		}
		decoded[key] = item
	}
	return decoded
}

func decodeStateMap[T ~[]byte](
	values map[string]T,
) map[string]replaytest.StateValueSnapshot {
	if values == nil {
		return nil
	}
	decoded := make(map[string]replaytest.StateValueSnapshot, len(values))
	for key, value := range values {
		decoded[key] = decodeStateValue(value)
	}
	return decoded
}

func decodeStateValue[T ~[]byte](value T) replaytest.StateValueSnapshot {
	if value == nil {
		return replaytest.NullStateValue()
	}
	if decoded, valid := decodeJSONState(value); valid {
		if decoded == nil {
			return replaytest.NullStateValue()
		}
		return replaytest.JSONStateValue(decoded)
	}
	if utf8.Valid(value) {
		return replaytest.TextStateValue(string(value))
	}
	return replaytest.BinaryStateValue(value)
}

func decodeJSONState(data []byte) (any, bool) {
	var decoded any
	if err := decodeJSONUseNumber(data, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func decodeJSONUseNumber(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func toMemorySnapshot(entry *memory.Entry) replaytest.MemorySnapshot {
	if entry == nil || entry.Memory == nil {
		return replaytest.MemorySnapshot{}
	}
	metadata := map[string]any{}
	if entry.Memory.Kind != "" {
		metadata["kind"] = string(entry.Memory.Kind)
	}
	if entry.Memory.EventTime != nil {
		metadata["event_time"] = *entry.Memory.EventTime
	}
	if len(entry.Memory.Participants) > 0 {
		metadata["participants"] = append([]string(nil), entry.Memory.Participants...)
	}
	if entry.Memory.Location != "" {
		metadata["location"] = entry.Memory.Location
	}
	return replaytest.MemorySnapshot{
		ID:      entry.ID,
		AppName: entry.AppName,
		UserID:  entry.UserID,
		Scope: replaytest.MemoryScope{
			AppName: entry.AppName,
			UserID:  entry.UserID,
		},
		Content:   entry.Memory.Memory,
		Topics:    append([]string(nil), entry.Memory.Topics...),
		Metadata:  metadata,
		Score:     entry.Score,
		CreatedAt: entry.CreatedAt,
		UpdatedAt: entry.UpdatedAt,
	}
}

func (fixture *replayFixture) toLogicalMemorySnapshot(
	entry *memory.Entry,
	logical replaytest.MemoryScope,
) replaytest.MemorySnapshot {
	snapshot := toMemorySnapshot(entry)
	if entry != nil {
		snapshot.AppName = logical.AppName
		snapshot.UserID = logical.UserID
		snapshot.Scope = logical
	}
	if entry == nil || entry.Memory == nil {
		return snapshot
	}
	fixture.mu.Lock()
	writeTime, recorded := fixture.memoryWriteTimes[entry.Memory.Memory]
	fixture.mu.Unlock()
	if recorded {
		snapshot.CreatedAt = writeTime
		snapshot.UpdatedAt = writeTime
	}
	return snapshot
}

func validatePhysicalMemoryScope(entry *memory.Entry, want memory.UserKey) error {
	if entry == nil {
		return fmt.Errorf("backend returned a nil memory entry")
	}
	if entry.AppName != want.AppName || entry.UserID != want.UserID {
		return fmt.Errorf(
			"backend returned memory %q from scope {%q %q}, want {%q %q}",
			entry.ID,
			entry.AppName,
			entry.UserID,
			want.AppName,
			want.UserID,
		)
	}
	return nil
}

func validatePhysicalSessionScope(sess *session.Session, want session.Key) error {
	if sess == nil {
		return fmt.Errorf("session %q not found", want.SessionID)
	}
	if sess.AppName != want.AppName || sess.UserID != want.UserID || sess.ID != want.SessionID {
		return fmt.Errorf(
			"backend returned session {%q %q %q}, want {%q %q %q}",
			sess.AppName,
			sess.UserID,
			sess.ID,
			want.AppName,
			want.UserID,
			want.SessionID,
		)
	}
	return nil
}

func cloneMemorySearchSnapshots(
	searches []replaytest.MemorySearchSnapshot,
) []replaytest.MemorySearchSnapshot {
	if searches == nil {
		return nil
	}
	cloned := make([]replaytest.MemorySearchSnapshot, len(searches))
	for i, search := range searches {
		cloned[i] = cloneMemorySearchSnapshot(search)
	}
	return cloned
}

func cloneMemorySearchSnapshot(
	search replaytest.MemorySearchSnapshot,
) replaytest.MemorySearchSnapshot {
	cloned := search
	if search.Results == nil {
		return cloned
	}
	cloned.Results = make([]replaytest.MemorySnapshot, len(search.Results))
	for i, result := range search.Results {
		cloned.Results[i] = result
		cloned.Results[i].Topics = append([]string(nil), result.Topics...)
		if result.Metadata != nil {
			cloned.Results[i].Metadata = make(map[string]any, len(result.Metadata))
			for key, value := range result.Metadata {
				if stringsValue, ok := value.([]string); ok {
					value = append([]string(nil), stringsValue...)
				}
				cloned.Results[i].Metadata[key] = value
			}
		}
	}
	return cloned
}

func toMemoryMetadata(values map[string]any) (*memory.Metadata, error) {
	metadata := &memory.Metadata{}
	unsupported := make([]string, 0)
	for key := range values {
		switch key {
		case "kind", "event_time", "participants", "location":
		default:
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) > 0 {
		sort.Strings(unsupported)
		return nil, fmt.Errorf("memory metadata %q is unsupported", unsupported[0])
	}
	if value, ok := values["kind"]; ok {
		kind, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("memory metadata kind has type %T", value)
		}
		metadata.Kind = memory.Kind(kind)
	}
	if value, ok := values["event_time"]; ok {
		switch typed := value.(type) {
		case time.Time:
			metadata.EventTime = &typed
		case *time.Time:
			if typed != nil {
				copied := *typed
				metadata.EventTime = &copied
			}
		default:
			return nil, fmt.Errorf("memory metadata event_time has type %T", value)
		}
	}
	if value, ok := values["participants"]; ok {
		participants, ok := value.([]string)
		if !ok {
			return nil, fmt.Errorf("memory metadata participants has type %T", value)
		}
		metadata.Participants = append([]string(nil), participants...)
	}
	if value, ok := values["location"]; ok {
		location, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("memory metadata location has type %T", value)
		}
		metadata.Location = location
	}
	return metadata, nil
}

type trackPayload struct {
	EventType    string         `json:"event_type"`
	InvocationID string         `json:"invocation_id"`
	Payload      map[string]any `json:"payload"`
	Error        string         `json:"error,omitempty"`
	Duration     time.Duration  `json:"duration"`
}

type replaySummarizer struct {
	mu   sync.Mutex
	next string
}

func (*replaySummarizer) ShouldSummarize(*session.Session) bool {
	return true
}

func (summarizer *replaySummarizer) Summarize(
	context.Context,
	*session.Session,
) (string, error) {
	summarizer.mu.Lock()
	defer summarizer.mu.Unlock()
	return summarizer.next, nil
}

func (*replaySummarizer) SetPrompt(string) {}

func (*replaySummarizer) SetModel(model.Model) {}

func (*replaySummarizer) Metadata() map[string]any {
	return map[string]any{"type": "replaytest"}
}

func (summarizer *replaySummarizer) SetNext(next string) {
	summarizer.mu.Lock()
	defer summarizer.mu.Unlock()
	summarizer.next = next
}
