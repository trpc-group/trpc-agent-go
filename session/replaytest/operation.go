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
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Operation is one typed replay action.
type Operation interface {
	// OperationID returns the stable identifier used in errors and checkpoints.
	OperationID() string
	// Execute applies the operation to one isolated backend runtime.
	Execute(context.Context, *Runtime) error
}

// Runtime contains one isolated backend execution.
type Runtime struct {
	Backend    Backend
	Ledger     *IdentityLedger
	Normalizer Normalizer

	mu            sync.Mutex
	clockBase     time.Time
	logicalClock  int64
	memoryQueries map[string][]*memory.Entry
	checkpoints   []CheckpointSnapshot
}

// NewRuntime creates a runtime for one backend and case.
func NewRuntime(backend Backend, options NormalizeOptions) *Runtime {
	return &Runtime{
		Backend: backend, Ledger: NewIdentityLedger(), Normalizer: NewNormalizer(options),
		clockBase:     time.Now().UTC(),
		memoryQueries: make(map[string][]*memory.Entry),
	}
}

func (r *Runtime) nextTimestamp() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logicalClock++
	return r.clockBase.Add(time.Duration(r.logicalClock) * time.Second)
}

func (r *Runtime) setMemoryQuery(name string, values []*memory.Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memoryQueries[name] = cloneMemoryEntries(values)
}

func (r *Runtime) memoryQuerySnapshot() map[string][]*memory.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string][]*memory.Entry, len(r.memoryQueries))
	for name, values := range r.memoryQueries {
		result[name] = cloneMemoryEntries(values)
	}
	return result
}

// CreateSessionOperation creates the case session.
type CreateSessionOperation struct {
	ID    string
	State session.StateMap
}

// OperationID returns the stable operation identifier.
func (o CreateSessionOperation) OperationID() string { return o.ID }

// Execute creates the replay session with a defensive copy of its initial state.
func (o CreateSessionOperation) Execute(ctx context.Context, runtime *Runtime) error {
	_, err := runtime.Backend.Session.CreateSession(ctx, runtime.Backend.SessionKey, cloneStateMap(o.State))
	return err
}

// ToolCallSpec describes one tool call with a logical ID.
type ToolCallSpec struct {
	LogicalID string
	Name      string
	Arguments json.RawMessage
}

// EventSpec describes a replay event without backend-generated identifiers.
type EventSpec struct {
	Author              string
	Role                model.Role
	Content             string
	InvocationLogicalID string
	ParentInvocationID  string
	ParentTriggerID     string
	ParentTriggerType   string
	ParentTriggerName   string
	Branch              string
	Tag                 string
	FilterKey           string
	ToolCalls           []ToolCallSpec
	ToolResponseID      string
	ToolResponseName    string
	ToolCallArgs        map[string]json.RawMessage
	StateDelta          session.StateMap
	Extensions          map[string]json.RawMessage
	Actions             *event.EventActions
}

// AppendEventOperation appends one event and registers all identity relations.
type AppendEventOperation struct {
	ID   string
	Spec EventSpec
}

// OperationID returns the stable operation identifier.
func (o AppendEventOperation) OperationID() string { return o.ID }

// Execute builds and appends an event while registering its logical identities.
func (o AppendEventOperation) Execute(ctx context.Context, runtime *Runtime) error {
	evt, err := buildReplayEvent(runtime, o.ID, o.Spec)
	if err != nil {
		return err
	}
	sess, err := runtime.Backend.Session.GetSession(ctx, runtime.Backend.SessionKey)
	if err != nil {
		return err
	}
	return runtime.Backend.Session.AppendEvent(ctx, sess, evt)
}

func buildReplayEvent(runtime *Runtime, logicalID string, spec EventSpec) (*event.Event, error) {
	if logicalID == "" {
		return nil, fmt.Errorf("event logical id is required")
	}
	rawEventID := rawIdentity(runtime.Backend.Name, IdentityEvent, logicalID)
	if err := runtime.Ledger.Register(IdentityEvent, rawEventID, logicalID); err != nil {
		return nil, err
	}
	invocationLogical := spec.InvocationLogicalID
	if invocationLogical == "" {
		invocationLogical = "root"
	}
	rawInvocation := rawIdentity(runtime.Backend.Name, IdentityInvocation, invocationLogical)
	if err := runtime.Ledger.Register(IdentityInvocation, rawInvocation, invocationLogical); err != nil {
		return nil, err
	}
	message := model.Message{Role: spec.Role, Content: spec.Content}
	for _, call := range spec.ToolCalls {
		rawCall := rawIdentity(runtime.Backend.Name, IdentityToolCall, call.LogicalID)
		if err := runtime.Ledger.Register(IdentityToolCall, rawCall, call.LogicalID); err != nil {
			return nil, err
		}
		message.ToolCalls = append(message.ToolCalls, model.ToolCall{
			Type: "function", ID: rawCall,
			Function: model.FunctionDefinitionParam{Name: call.Name, Arguments: append([]byte(nil), call.Arguments...)},
		})
	}
	if spec.ToolResponseID != "" {
		rawCall, ok := runtime.Ledger.Raw(IdentityToolCall, spec.ToolResponseID)
		if !ok {
			return nil, fmt.Errorf("tool response references unknown logical tool call %q", spec.ToolResponseID)
		}
		message.ToolID = rawCall
		message.ToolName = spec.ToolResponseName
	}
	timestamp := runtime.nextTimestamp()
	evt := &event.Event{
		Response: &model.Response{
			Choices:   []model.Choice{{Message: message}},
			Timestamp: timestamp,
		},
		InvocationID: rawInvocation, Author: spec.Author, ID: rawEventID,
		Timestamp: timestamp, Branch: spec.Branch, Tag: spec.Tag,
		FilterKey: spec.FilterKey, StateDelta: cloneStateMap(spec.StateDelta),
		Extensions: cloneRawMap(spec.Extensions), Actions: cloneActions(spec.Actions),
		Version: event.CurrentVersion,
	}
	if spec.ParentInvocationID != "" {
		rawParent := rawIdentity(runtime.Backend.Name, IdentityInvocation, spec.ParentInvocationID)
		if err := runtime.Ledger.Register(IdentityInvocation, rawParent, spec.ParentInvocationID); err != nil {
			return nil, err
		}
		evt.ParentInvocationID = rawParent
	}
	if spec.ParentTriggerID != "" {
		rawTrigger, ok := runtime.Ledger.Raw(IdentityToolCall, spec.ParentTriggerID)
		if !ok {
			return nil, fmt.Errorf("parent metadata references unknown tool call %q", spec.ParentTriggerID)
		}
		evt.ParentMetadata = &event.ParentInvocationMetadata{
			TriggerType: spec.ParentTriggerType, TriggerID: rawTrigger, TriggerName: spec.ParentTriggerName,
		}
	}
	if len(spec.ToolCallArgs) > 0 {
		args := make(map[string]json.RawMessage, len(spec.ToolCallArgs))
		for logicalCall, value := range spec.ToolCallArgs {
			rawCall, ok := runtime.Ledger.Raw(IdentityToolCall, logicalCall)
			if !ok {
				return nil, fmt.Errorf("tool args reference unknown tool call %q", logicalCall)
			}
			args[rawCall] = append(json.RawMessage(nil), value...)
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("marshal tool call args extension: %w", err)
		}
		if evt.Extensions == nil {
			evt.Extensions = make(map[string]json.RawMessage)
		}
		evt.Extensions[event.ToolCallArgsExtensionKey] = raw
	}
	return evt, nil
}

// StateScope identifies app, user, or session state.
type StateScope string

// State scopes select the public state API used by an operation.
const (
	StateScopeApp     StateScope = "app"
	StateScopeUser    StateScope = "user"
	StateScopeSession StateScope = "session"
)

// SetStateOperation writes state through the public scope API.
type SetStateOperation struct {
	ID     string
	Scope  StateScope
	Values session.StateMap
}

// OperationID returns the stable operation identifier.
func (o SetStateOperation) OperationID() string { return o.ID }

// Execute writes the configured values through the selected state scope.
func (o SetStateOperation) Execute(ctx context.Context, runtime *Runtime) error {
	switch o.Scope {
	case StateScopeApp:
		return runtime.Backend.Session.UpdateAppState(ctx, runtime.Backend.SessionKey.AppName, cloneStateMap(o.Values))
	case StateScopeUser:
		return runtime.Backend.Session.UpdateUserState(ctx, session.UserKey{AppName: runtime.Backend.SessionKey.AppName, UserID: runtime.Backend.SessionKey.UserID}, cloneStateMap(o.Values))
	case StateScopeSession:
		return runtime.Backend.Session.UpdateSessionState(ctx, runtime.Backend.SessionKey, cloneStateMap(o.Values))
	default:
		return fmt.Errorf("unsupported state scope %q", o.Scope)
	}
}

// DeleteStateOperation deletes app or user state keys through public APIs.
type DeleteStateOperation struct {
	ID    string
	Scope StateScope
	Keys  []string
}

// OperationID returns the stable operation identifier.
func (o DeleteStateOperation) OperationID() string { return o.ID }

// Execute deletes the configured keys through the selected public state API.
func (o DeleteStateOperation) Execute(ctx context.Context, runtime *Runtime) error {
	for _, key := range o.Keys {
		var err error
		switch o.Scope {
		case StateScopeApp:
			err = runtime.Backend.Session.DeleteAppState(ctx, runtime.Backend.SessionKey.AppName, key)
		case StateScopeUser:
			err = runtime.Backend.Session.DeleteUserState(ctx, session.UserKey{AppName: runtime.Backend.SessionKey.AppName, UserID: runtime.Backend.SessionKey.UserID}, key)
		default:
			return fmt.Errorf("state delete is unsupported for scope %q", o.Scope)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ClearStateOperation clears app or user state one key at a time.
type ClearStateOperation struct {
	ID    string
	Scope StateScope
}

// OperationID returns the stable operation identifier.
func (o ClearStateOperation) OperationID() string { return o.ID }

// Execute removes every key currently visible in the selected state scope.
func (o ClearStateOperation) Execute(ctx context.Context, runtime *Runtime) error {
	switch o.Scope {
	case StateScopeApp:
		values, err := runtime.Backend.Session.ListAppStates(ctx, runtime.Backend.SessionKey.AppName)
		if err != nil {
			return err
		}
		return DeleteStateOperation{Scope: StateScopeApp, Keys: sortedStateKeys(values)}.Execute(ctx, runtime)
	case StateScopeUser:
		userKey := session.UserKey{AppName: runtime.Backend.SessionKey.AppName, UserID: runtime.Backend.SessionKey.UserID}
		values, err := runtime.Backend.Session.ListUserStates(ctx, userKey)
		if err != nil {
			return err
		}
		return DeleteStateOperation{Scope: StateScopeUser, Keys: sortedStateKeys(values)}.Execute(ctx, runtime)
	default:
		return fmt.Errorf("state clear is unsupported for scope %q", o.Scope)
	}
}

// AddMemoryOperation writes one logical memory and discovers its backend ID.
type AddMemoryOperation struct {
	ID, Content string
	Topics      []string
	Metadata    *memory.Metadata
}

// OperationID returns the stable operation identifier.
func (o AddMemoryOperation) OperationID() string { return o.ID }

// Execute adds a memory and associates its backend ID with the logical ID.
func (o AddMemoryOperation) Execute(ctx context.Context, runtime *Runtime) error {
	if runtime.Backend.Memory == nil {
		return fmt.Errorf("backend %q has no memory service", runtime.Backend.Name)
	}
	userKey := replayMemoryUserKey(runtime.Backend)
	before, err := runtime.Backend.Memory.ReadMemories(ctx, userKey, 1000)
	if err != nil {
		return err
	}
	var options []memory.AddOption
	if o.Metadata != nil {
		options = append(options, memory.WithMetadata(cloneMetadata(o.Metadata)))
	}
	if err := runtime.Backend.Memory.AddMemory(ctx, userKey, o.Content, append([]string(nil), o.Topics...), options...); err != nil {
		return err
	}
	after, err := runtime.Backend.Memory.ReadMemories(ctx, userKey, 1000)
	if err != nil {
		return err
	}
	rawID, err := identifyMemoryID(before, after, o.Content, o.Topics, o.Metadata)
	if err != nil {
		return err
	}
	return runtime.Ledger.Register(IdentityMemory, rawID, o.ID)
}

// UpdateMemoryOperation updates a logical memory and handles ID rotation.
type UpdateMemoryOperation struct {
	ID, MemoryID, Content string
	Topics                []string
	Metadata              *memory.Metadata
}

// OperationID returns the stable operation identifier.
func (o UpdateMemoryOperation) OperationID() string { return o.ID }

// Execute updates a logical memory and records any backend-side ID rotation.
func (o UpdateMemoryOperation) Execute(ctx context.Context, runtime *Runtime) error {
	rawID, ok := runtime.Ledger.Raw(IdentityMemory, o.MemoryID)
	if !ok {
		return fmt.Errorf("unknown logical memory %q", o.MemoryID)
	}
	result := &memory.UpdateResult{}
	options := []memory.UpdateOption{memory.WithUpdateResult(result)}
	if o.Metadata != nil {
		options = append(options, memory.WithUpdateMetadata(cloneMetadata(o.Metadata)))
	}
	key := memory.Key{AppName: runtime.Backend.SessionKey.AppName, UserID: runtime.Backend.SessionKey.UserID, MemoryID: rawID}
	if err := runtime.Backend.Memory.UpdateMemory(ctx, key, o.Content, append([]string(nil), o.Topics...), options...); err != nil {
		return err
	}
	newID := result.MemoryID
	if newID == "" {
		values, err := runtime.Backend.Memory.ReadMemories(ctx, replayMemoryUserKey(runtime.Backend), 1000)
		if err != nil {
			return err
		}
		newID, err = identifyMemoryID(nil, values, o.Content, o.Topics, o.Metadata)
		if err != nil {
			return err
		}
	}
	return runtime.Ledger.Replace(IdentityMemory, rawID, newID, o.MemoryID)
}

// DeleteMemoryOperation removes a logical memory.
type DeleteMemoryOperation struct{ ID, MemoryID string }

// OperationID returns the stable operation identifier.
func (o DeleteMemoryOperation) OperationID() string { return o.ID }

// Execute deletes the backend memory associated with the logical memory ID.
func (o DeleteMemoryOperation) Execute(ctx context.Context, runtime *Runtime) error {
	rawID, ok := runtime.Ledger.Raw(IdentityMemory, o.MemoryID)
	if !ok {
		return fmt.Errorf("unknown logical memory %q", o.MemoryID)
	}
	return runtime.Backend.Memory.DeleteMemory(ctx, memory.Key{AppName: runtime.Backend.SessionKey.AppName, UserID: runtime.Backend.SessionKey.UserID, MemoryID: rawID})
}

// ClearMemoryOperation removes every memory for the case user.
type ClearMemoryOperation struct{ ID string }

// OperationID returns the stable operation identifier.
func (o ClearMemoryOperation) OperationID() string { return o.ID }

// Execute clears every memory owned by the isolated replay user.
func (o ClearMemoryOperation) Execute(ctx context.Context, runtime *Runtime) error {
	return runtime.Backend.Memory.ClearMemories(ctx, replayMemoryUserKey(runtime.Backend))
}

// SearchMemoryOperation records one replay-visible query result.
type SearchMemoryOperation struct {
	ID, Query string
	Options   []memory.SearchOption
}

// OperationID returns the stable operation identifier.
func (o SearchMemoryOperation) OperationID() string { return o.ID }

// Execute runs a memory query and stores its result for later normalization.
func (o SearchMemoryOperation) Execute(ctx context.Context, runtime *Runtime) error {
	values, err := runtime.Backend.Memory.SearchMemories(ctx, replayMemoryUserKey(runtime.Backend), o.Query, o.Options...)
	if err != nil {
		return err
	}
	runtime.setMemoryQuery(o.ID, values)
	return nil
}

// CreateSummaryOperation generates or overwrites a summary.
type CreateSummaryOperation struct {
	ID, FilterKey string
	Force         bool
}

// OperationID returns the stable operation identifier.
func (o CreateSummaryOperation) OperationID() string { return o.ID }

// Execute requests summary creation or overwrite for the configured filter key.
func (o CreateSummaryOperation) Execute(ctx context.Context, runtime *Runtime) error {
	sess, err := runtime.Backend.Session.GetSession(ctx, runtime.Backend.SessionKey)
	if err != nil {
		return err
	}
	return runtime.Backend.Session.CreateSessionSummary(ctx, sess, o.FilterKey, o.Force)
}

// WaitSummaryOperation waits until the requested summary is persisted.
type WaitSummaryOperation struct {
	ID                         string
	FilterKey                  string
	ExpectedLastEventLogicalID string
	Timeout                    time.Duration
}

// OperationID returns the stable operation identifier.
func (o WaitSummaryOperation) OperationID() string { return o.ID }

// Execute waits for a non-stale summary that reaches the expected event boundary.
func (o WaitSummaryOperation) Execute(ctx context.Context, runtime *Runtime) error {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		sess, err := runtime.Backend.Session.GetSession(ctx, runtime.Backend.SessionKey)
		if err != nil {
			return err
		}
		sess.SummariesMu.RLock()
		value, exists := sess.Summaries[o.FilterKey]
		var summary *session.Summary
		if value != nil {
			summary = value.Clone()
		}
		sess.SummariesMu.RUnlock()
		if exists && summaryReachedExpectedEvent(
			summary,
			o.ExpectedLastEventLogicalID,
			runtime.Ledger,
		) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("summary %q was not persisted within %s", o.FilterKey, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func summaryReachedExpectedEvent(
	value *session.Summary,
	expectedLogicalID string,
	ledger *IdentityLedger,
) bool {
	if value == nil || value.Summary == "" {
		return false
	}
	if expectedLogicalID == "" {
		return true
	}
	rawEventID, ok := ledger.Raw(IdentityEvent, expectedLogicalID)
	if !ok {
		return false
	}
	boundary := value.CutoffBoundary()
	return boundary != nil && boundary.LastEventID == rawEventID
}

// AppendTrackOperation appends one track event.
type AppendTrackOperation struct {
	ID      string
	Track   session.Track
	Payload json.RawMessage
}

// OperationID returns the stable operation identifier.
func (o AppendTrackOperation) OperationID() string { return o.ID }

// Execute expands logical payload identities and appends one track event.
func (o AppendTrackOperation) Execute(ctx context.Context, runtime *Runtime) error {
	trackService := runtime.Backend.Track
	if trackService == nil {
		if value, ok := runtime.Backend.Session.(session.TrackService); ok {
			trackService = value
		}
	}
	if trackService == nil {
		return fmt.Errorf("backend %q has no track service", runtime.Backend.Name)
	}
	sess, err := runtime.Backend.Session.GetSession(ctx, runtime.Backend.SessionKey)
	if err != nil {
		return err
	}
	payload, err := expandTrackPayloadIdentifiers(o.Payload, runtime)
	if err != nil {
		return err
	}
	return trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track: o.Track, Payload: payload, Timestamp: runtime.nextTimestamp(),
	})
}

func expandTrackPayloadIdentifiers(
	payload json.RawMessage,
	runtime *Runtime,
) (json.RawMessage, error) {
	if payload == nil {
		return nil, nil
	}
	var value any
	if err := decodeJSON(payload, &value); err != nil {
		return append(json.RawMessage(nil), payload...), nil
	}
	value, err := expandKnownPayloadIdentifiers(value, runtime)
	if err != nil {
		return nil, err
	}
	expanded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal track payload: %w", err)
	}
	return expanded, nil
}

func expandKnownPayloadIdentifiers(value any, runtime *Runtime) (any, error) {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			switch strings.ToLower(key) {
			case "invocationid", "invocation_id",
				"parentinvocationid", "parent_invocation_id":
				if logical, ok := item.(string); ok {
					raw := rawIdentity(
						runtime.Backend.Name,
						IdentityInvocation,
						logical,
					)
					if err := runtime.Ledger.Register(
						IdentityInvocation,
						raw,
						logical,
					); err != nil {
						return nil, err
					}
					result[key] = raw
					continue
				}
			case "toolcallid", "tool_call_id", "toolid", "tool_id",
				"triggerid", "trigger_id":
				if logical, ok := item.(string); ok {
					raw := rawIdentity(
						runtime.Backend.Name,
						IdentityToolCall,
						logical,
					)
					if err := runtime.Ledger.Register(
						IdentityToolCall,
						raw,
						logical,
					); err != nil {
						return nil, err
					}
					result[key] = raw
					continue
				}
			}
			expanded, err := expandKnownPayloadIdentifiers(item, runtime)
			if err != nil {
				return nil, err
			}
			result[key] = expanded
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for i := range typed {
			expanded, err := expandKnownPayloadIdentifiers(typed[i], runtime)
			if err != nil {
				return nil, err
			}
			result[i] = expanded
		}
		return result, nil
	default:
		return value, nil
	}
}

// CheckpointOperation records a transition snapshot.
type CheckpointOperation struct{ ID, Name string }

// OperationID returns the stable operation identifier.
func (o CheckpointOperation) OperationID() string { return o.ID }

// Execute captures a normalized checkpoint after the current transition.
func (o CheckpointOperation) Execute(ctx context.Context, runtime *Runtime) error {
	name := o.Name
	if name == "" {
		name = o.ID
	}
	return runtime.captureCheckpoint(ctx, name, o.ID)
}

// ParallelOperation starts child operations together and waits for all results.
type ParallelOperation struct {
	ID         string
	Operations []Operation
}

// OperationID returns the stable operation identifier.
func (o ParallelOperation) OperationID() string { return o.ID }

// Execute starts all child operations together and joins their errors.
func (o ParallelOperation) Execute(ctx context.Context, runtime *Runtime) error {
	start := make(chan struct{})
	errorsCh := make(chan error, len(o.Operations))
	var group sync.WaitGroup
	for _, operation := range o.Operations {
		operation := operation
		group.Add(1)
		go func() { defer group.Done(); <-start; errorsCh <- operation.Execute(ctx, runtime) }()
	}
	close(start)
	group.Wait()
	close(errorsCh)
	var result error
	for err := range errorsCh {
		result = errors.Join(result, err)
	}
	return result
}

// SessionWindowCheckpointOperation captures a truncated event window with summaries.
type SessionWindowCheckpointOperation struct {
	ID       string
	Name     string
	EventNum int
}

// OperationID returns the stable operation identifier.
func (o SessionWindowCheckpointOperation) OperationID() string { return o.ID }

// Execute captures a normalized session view restricted to the requested window.
func (o SessionWindowCheckpointOperation) Execute(ctx context.Context, runtime *Runtime) error {
	sess, err := runtime.Backend.Session.GetSession(ctx, runtime.Backend.SessionKey, session.WithEventNum(o.EventNum))
	if err != nil {
		return err
	}
	appState, err := runtime.Backend.Session.ListAppStates(ctx, runtime.Backend.SessionKey.AppName)
	if err != nil {
		return err
	}
	userKey := session.UserKey{AppName: runtime.Backend.SessionKey.AppName, UserID: runtime.Backend.SessionKey.UserID}
	userState, err := runtime.Backend.Session.ListUserStates(ctx, userKey)
	if err != nil {
		return err
	}
	var memories []*memory.Entry
	if runtime.Backend.Memory != nil && runtime.Backend.Capabilities.Supports(CapabilityMemory) {
		memories, err = runtime.Backend.Memory.ReadMemories(ctx, replayMemoryUserKey(runtime.Backend), 1000)
		if err != nil {
			return err
		}
	}
	snapshot, err := runtime.Normalizer.Normalize(CaptureInput{
		Session: sess, AppState: appState, UserState: userState, Memories: memories,
		MemoryQueries: runtime.memoryQuerySnapshot(), Unsupported: unsupportedCapabilities(runtime.Backend.Capabilities),
	}, runtime.Ledger)
	if err != nil {
		return err
	}
	name := o.Name
	if name == "" {
		name = o.ID
	}
	runtime.mu.Lock()
	runtime.checkpoints = append(runtime.checkpoints, CheckpointSnapshot{Name: name, AfterOp: o.ID, Snapshot: snapshot})
	runtime.mu.Unlock()
	return nil
}

// RecoveryAppendEventOperation simulates a pre-commit failure followed by a
// successful write whose acknowledgement is lost, then performs read-before-retry.
type RecoveryAppendEventOperation struct {
	ID   string
	Spec EventSpec
}

// OperationID returns the stable operation identifier.
func (o RecoveryAppendEventOperation) OperationID() string { return o.ID }

// Execute verifies read-before-retry recovery after pre-commit and lost-ack faults.
func (o RecoveryAppendEventOperation) Execute(ctx context.Context, runtime *Runtime) error {
	evt, err := buildReplayEvent(runtime, o.ID, o.Spec)
	if err != nil {
		return err
	}
	sess, err := runtime.Backend.Session.GetSession(
		ctx,
		runtime.Backend.SessionKey,
	)
	if err != nil {
		return err
	}
	preCommit := &FaultySessionService{
		Service: runtime.Backend.Session,
		Mode:    SessionFaultPreCommitEventError,
	}
	if err := preCommit.AppendEvent(ctx, sess, evt); err == nil {
		return fmt.Errorf("pre-commit fault did not fail")
	}
	lostAck := &FaultySessionService{
		Service: runtime.Backend.Session,
		Mode:    SessionFaultLostEventAck,
	}
	if err := lostAck.AppendEvent(ctx, sess, evt); err == nil {
		return fmt.Errorf("lost-ack fault did not fail")
	}
	stored, err := runtime.Backend.Session.GetSession(ctx, runtime.Backend.SessionKey)
	if err != nil {
		return err
	}
	count := 0
	for i := range stored.Events {
		if stored.Events[i].ID == evt.ID {
			count++
		}
	}
	if count == 0 {
		if err := runtime.Backend.Session.AppendEvent(ctx, stored, evt); err != nil {
			return err
		}
		count = 1
	}
	if count != 1 {
		return fmt.Errorf("recovery produced %d copies of event %q", count, o.ID)
	}
	return nil
}
func rawIdentity(backend string, namespace IdentityNamespace, logical string) string {
	return backend + "/" + string(namespace) + "/" + logical
}
func replayMemoryUserKey(backend Backend) memory.UserKey {
	return memory.UserKey{AppName: backend.SessionKey.AppName, UserID: backend.SessionKey.UserID}
}

func identifyMemoryID(before, after []*memory.Entry, content string, topics []string, metadata *memory.Metadata) (string, error) {
	beforeIDs := make(map[string]struct{}, len(before))
	for _, entry := range before {
		if entry != nil {
			beforeIDs[entry.ID] = struct{}{}
		}
	}
	var fallback string
	for _, entry := range after {
		if !memoryMatches(entry, content, topics, metadata) {
			continue
		}
		if _, existed := beforeIDs[entry.ID]; !existed {
			return entry.ID, nil
		}
		fallback = entry.ID
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("memory %q was not found after write", content)
}

func memoryMatches(entry *memory.Entry, content string, topics []string, metadata *memory.Metadata) bool {
	if entry == nil || entry.Memory == nil || entry.Memory.Memory != content {
		return false
	}
	left := append([]string(nil), entry.Memory.Topics...)
	right := append([]string(nil), topics...)
	sort.Strings(left)
	sort.Strings(right)
	if fmt.Sprint(left) != fmt.Sprint(right) {
		return false
	}
	if metadata == nil {
		return true
	}
	return entry.Memory.Kind == metadata.Kind && entry.Memory.Location == metadata.Location && fmt.Sprint(sortedStrings(entry.Memory.Participants)) == fmt.Sprint(sortedStrings(metadata.Participants))
}

func cloneMetadata(value *memory.Metadata) *memory.Metadata {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Participants = append([]string(nil), value.Participants...)
	if value.EventTime != nil {
		t := *value.EventTime
		clone.EventTime = &t
	}
	return &clone
}
func cloneStateMap(value session.StateMap) session.StateMap {
	if value == nil {
		return nil
	}
	result := make(session.StateMap, len(value))
	for key, raw := range value {
		if raw == nil {
			result[key] = nil
		} else {
			result[key] = append([]byte(nil), raw...)
		}
	}
	return result
}
func cloneRawMap(value map[string]json.RawMessage) map[string]json.RawMessage {
	if value == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(value))
	for key, raw := range value {
		result[key] = append(json.RawMessage(nil), raw...)
	}
	return result
}
func cloneActions(value *event.EventActions) *event.EventActions {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
func cloneMemoryEntries(values []*memory.Entry) []*memory.Entry {
	result := make([]*memory.Entry, len(values))
	for i, entry := range values {
		if entry == nil {
			continue
		}
		clone := *entry
		if entry.Memory != nil {
			m := *entry.Memory
			m.Topics = append([]string(nil), entry.Memory.Topics...)
			m.Participants = append([]string(nil), entry.Memory.Participants...)
			clone.Memory = &m
		}
		result[i] = &clone
	}
	return result
}
func sortedStateKeys(values session.StateMap) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
