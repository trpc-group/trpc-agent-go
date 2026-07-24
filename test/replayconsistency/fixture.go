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
	"sort"
	"strings"
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
	memoryReadLimit               = 100
	fixtureCleanupTimeout         = 5 * time.Second
	serviceCloseOperationCount    = 2
)

var replayScopeSequence atomic.Uint64

type replayFixture struct {
	mu             sync.Mutex
	summaryMu      sync.Mutex
	name           string
	sessionService session.Service
	memoryService  memory.Service
	summarizer     *replaySummarizer
	capabilities   replaytest.CapabilitySet
	appName        string
	userID         string
	sessionIDs     map[string]struct{}
	replayWindows  map[string]string
	memoryScopes   map[replaytest.MemoryScope]memory.UserKey
	stateDeletes   map[string]map[string]struct{}
	searches       []replaytest.MemorySearchSnapshot
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
		name:           config.name,
		sessionService: config.sessionService,
		memoryService:  config.memoryService,
		summarizer:     config.summarizer,
		capabilities:   capabilities,
		appName:        replayAppName + "-" + scopeID,
		userID:         replayUserID + "-" + scopeID,
		sessionIDs:     make(map[string]struct{}),
		replayWindows:  make(map[string]string),
		memoryScopes:   make(map[replaytest.MemoryScope]memory.UserKey),
		stateDeletes:   make(map[string]map[string]struct{}),
	}
}

func (fixture *replayFixture) Name() string {
	return fixture.name
}

func (fixture *replayFixture) Capabilities() replaytest.CapabilitySet {
	return fixture.capabilities
}

func (fixture *replayFixture) Apply(ctx context.Context, operation replaytest.Operation) error {
	return fixture.apply(ctx, operation)
}

func (fixture *replayFixture) ApplyWithFault(
	ctx context.Context,
	operation replaytest.Operation,
) error {
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
		search.Results = append(search.Results, toLogicalMemorySnapshot(entry, logicalScope))
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
		entries, err := fixture.memoryService.ReadMemories(ctx, scope.physical, memoryReadLimit)
		if err != nil {
			return replaytest.Snapshot{}, fmt.Errorf("read memories for %#v: %w", scope.logical, err)
		}
		for _, entry := range entries {
			if err := validatePhysicalMemoryScope(entry, scope.physical); err != nil {
				return replaytest.Snapshot{}, fmt.Errorf("read memories for %#v: %w", scope.logical, err)
			}
			snapshot.Memories = append(
				snapshot.Memories, toLogicalMemorySnapshot(entry, scope.logical),
			)
		}
	}
	snapshot.MemorySearches = bookkeeping.searches
	return snapshot, nil
}

type fixtureBookkeeping struct {
	sessionIDs    []string
	replayWindows map[string]string
	memoryScopes  []memoryScopeBinding
	stateDeletes  map[string]map[string]struct{}
	searches      []replaytest.MemorySearchSnapshot
}

type memoryScopeBinding struct {
	logical  replaytest.MemoryScope
	physical memory.UserKey
}

func (fixture *replayFixture) snapshotBookkeeping() fixtureBookkeeping {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	bookkeeping := fixtureBookkeeping{
		sessionIDs:    make([]string, 0, len(fixture.sessionIDs)),
		replayWindows: make(map[string]string, len(fixture.replayWindows)),
		memoryScopes:  make([]memoryScopeBinding, 0, len(fixture.memoryScopes)),
		stateDeletes:  make(map[string]map[string]struct{}, len(fixture.stateDeletes)),
		searches:      cloneMemorySearchSnapshots(fixture.searches),
	}
	for id := range fixture.sessionIDs {
		bookkeeping.sessionIDs = append(bookkeeping.sessionIDs, id)
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
	ctx, cancel := context.WithTimeout(context.Background(), fixtureCleanupTimeout)
	defer cancel()
	bookkeeping := fixture.snapshotBookkeeping()
	cleanupErrors := make([]error, 0,
		len(bookkeeping.sessionIDs)+len(bookkeeping.memoryScopes)+serviceCloseOperationCount)
	for _, id := range bookkeeping.sessionIDs {
		cleanupErrors = append(cleanupErrors, fixture.sessionService.DeleteSession(
			ctx, fixture.sessionKey(id),
		))
	}
	for _, scope := range bookkeeping.memoryScopes {
		cleanupErrors = append(cleanupErrors, fixture.memoryService.ClearMemories(ctx, scope.physical))
	}
	cleanupErrors = append(cleanupErrors, fixture.sessionService.Close(), fixture.memoryService.Close())
	return errors.Join(cleanupErrors...)
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

func toEvent(snapshot *replaytest.EventSnapshot) (*event.Event, error) {
	message := model.Message{
		Role:    model.Role(snapshot.Role),
		Content: snapshot.Content,
	}
	for _, call := range snapshot.ToolCalls {
		arguments, err := json.Marshal(call.Arguments)
		if err != nil {
			return nil, fmt.Errorf("marshal tool call %q arguments: %w", call.ID, err)
		}
		message.ToolCalls = append(message.ToolCalls, model.ToolCall{
			Type: "function",
			ID:   call.ID,
			Function: model.FunctionDefinitionParam{
				Name:      call.Name,
				Arguments: arguments,
			},
			ExtraFields: call.Extra,
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
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal event extension %q: %w", key, err)
		}
		extensions[key] = encoded
	}
	if snapshot.ToolResponse != nil && len(snapshot.ToolResponse.Extra) > 0 {
		if _, exists := extensions[toolResponseExtraExtensionKey]; exists {
			return nil, fmt.Errorf(
				"event extension %q is reserved", toolResponseExtraExtensionKey,
			)
		}
		encoded, err := json.Marshal(snapshot.ToolResponse.Extra)
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
		if key == "tracks" || strings.HasPrefix(key, "summary:") {
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
				if err := json.Unmarshal(trackEvent.Payload, &payload); err != nil {
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
	responseExtra := takeToolResponseExtra(snapshot.Extensions, allowNestedToolResponseExtra)
	if len(snapshot.Extensions) == 0 {
		snapshot.Extensions = nil
	}
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
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
		snapshot.ToolResponse = &replaytest.ToolResponse{
			ToolCallID: message.ToolID,
			Name:       message.ToolName,
			Content:    message.Content,
			Extra:      responseExtra,
		}
	}
	return snapshot
}

func takeToolResponseExtra(
	extensions map[string]any,
	allowNested bool,
) map[string]any {
	if extensions == nil {
		return nil
	}
	if value, ok := extensions[toolResponseExtraExtensionKey]; ok {
		delete(extensions, toolResponseExtraExtensionKey)
		responseExtra, _ := value.(map[string]any)
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
	delete(namespace, "tool_response_extra")
	if len(namespace) == 0 {
		delete(extensions, replayAppName)
	}
	responseExtra, _ := value.(map[string]any)
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
		if err := json.Unmarshal(value, &item); err != nil {
			item = string(value)
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, false
	}
	return decoded, true
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

func toLogicalMemorySnapshot(
	entry *memory.Entry,
	logical replaytest.MemoryScope,
) replaytest.MemorySnapshot {
	snapshot := toMemorySnapshot(entry)
	if entry != nil {
		snapshot.AppName = logical.AppName
		snapshot.UserID = logical.UserID
		snapshot.Scope = logical
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
