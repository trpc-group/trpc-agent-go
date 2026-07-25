//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var errInjectedPreCommit = errors.New("injected pre-commit failure")

func executeWithRetry(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	operation Operation,
	path string,
	operationIndex int,
) error {
	attempts := operation.Retry.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var (
		lastErr  error
		failures int
	)
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt <= operation.Retry.FailBeforeAttempts {
			lastErr = errInjectedPreCommit
			failures++
			continue
		}
		lastErr = executeOperation(
			ctx,
			backend,
			replayCase,
			state,
			operation,
			path,
			operationIndex,
		)
		if lastErr == nil {
			if attempts > 1 || failures > 0 {
				state.addRecovery(RecoverySnapshot{
					Operation: path,
					Attempts:  attempt,
					Failures:  failures,
				})
			}
			return nil
		}
		failures++
	}
	return fmt.Errorf(
		"%s failed after %d attempt(s): %w",
		path,
		attempts,
		lastErr,
	)
}

func executeOperation(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	operation Operation,
	path string,
	operationIndex int,
) error {
	switch operation.Kind {
	case OperationAppendEvent:
		return executeAppendEvent(
			ctx,
			backend,
			replayCase,
			operation.Event,
			operationIndex,
		)
	case OperationSetState:
		return executeStateMutation(
			ctx,
			backend,
			replayCase,
			state,
			operation.State,
			false,
			operationIndex,
		)
	case OperationDeleteState:
		return executeStateMutation(
			ctx,
			backend,
			replayCase,
			state,
			operation.State,
			true,
			operationIndex,
		)
	case OperationAddMemory:
		return executeMemoryAdd(
			ctx,
			backend,
			replayCase,
			state,
			operation.Memory,
		)
	case OperationUpdateMemory:
		return executeMemoryUpdate(
			ctx,
			backend,
			replayCase,
			state,
			operation.Memory,
		)
	case OperationDeleteMemory:
		return executeMemoryDelete(
			ctx,
			backend,
			replayCase,
			state,
			operation.Memory,
		)
	case OperationSearchMemory:
		return executeMemorySearch(
			ctx,
			backend,
			replayCase,
			state,
			operation.Memory,
		)
	case OperationGenerateSummary:
		return executeSummary(
			ctx,
			backend,
			replayCase,
			state,
			operation.Summary,
		)
	case OperationAppendTrack:
		return executeTrack(
			ctx,
			backend,
			replayCase,
			operation.Track,
			operationIndex,
		)
	case OperationParallel:
		return executeParallel(
			ctx,
			backend,
			replayCase,
			state,
			operation.Parallel,
			path,
			operationIndex,
		)
	default:
		return fmt.Errorf("unsupported operation kind %q", operation.Kind)
	}
}

func executeAppendEvent(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	input *EventInput,
	operationIndex int,
) error {
	if input == nil {
		return fmt.Errorf("event input is nil")
	}
	if !backend.Capabilities.Events {
		return nil
	}
	sess, err := backend.Session.GetSession(ctx, replayCase.Key)
	if err != nil {
		return fmt.Errorf("get session before append: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found before append")
	}
	replayEvent := buildReplayEvent(*input, operationIndex)
	if err := backend.Session.AppendEvent(
		ctx,
		sess,
		replayEvent,
	); err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

func buildReplayEvent(input EventInput, operationIndex int) *event.Event {
	message := model.Message{
		Role:     input.Role,
		Content:  input.Content,
		ToolID:   input.ToolID,
		ToolName: input.ToolName,
	}
	for _, call := range input.ToolCalls {
		callType := call.Type
		if callType == "" {
			callType = "function"
		}
		extraFields := make(map[string]any, len(call.ExtraFields))
		for key, value := range call.ExtraFields {
			extraFields[key] = normalizeJSONBytes(value)
		}
		if len(extraFields) == 0 {
			extraFields = nil
		}
		message.ToolCalls = append(message.ToolCalls, model.ToolCall{
			ID:   call.ID,
			Type: callType,
			Function: model.FunctionDefinitionParam{
				Name:      call.Name,
				Arguments: append([]byte(nil), call.Arguments...),
			},
			ExtraFields: extraFields,
		})
	}
	object := model.ObjectTypeChatCompletion
	if input.Role == model.RoleTool {
		object = model.ObjectTypeToolResponse
	}
	response := &model.Response{
		Object: object,
		Done:   true,
		Choices: []model.Choice{{
			Message: message,
		}},
	}
	replayEvent := event.NewResponseEvent(
		input.InvocationID,
		input.Author,
		response,
	)
	if input.LogicalID != "" {
		replayEvent.ID = input.LogicalID
	}
	if !input.Timestamp.IsZero() {
		replayEvent.Timestamp = input.Timestamp
	} else {
		replayEvent.Timestamp = standardTime(operationIndex + 1)
	}
	replayEvent.ParentInvocationID = input.ParentInvocationID
	replayEvent.Branch = input.Branch
	replayEvent.Tag = input.Tag
	replayEvent.FilterKey = input.FilterKey
	replayEvent.Version = event.CurrentVersion
	if len(input.StateDelta) > 0 {
		replayEvent.StateDelta = make(
			map[string][]byte,
			len(input.StateDelta),
		)
		for key, value := range input.StateDelta {
			replayEvent.StateDelta[key] = append([]byte(nil), value...)
		}
	}
	replayEvent.Extensions = make(
		map[string]json.RawMessage,
		len(input.Extensions)+2,
	)
	for key, value := range input.Extensions {
		replayEvent.Extensions[key] = append(
			json.RawMessage(nil),
			value...,
		)
	}
	if input.LogicalID != "" {
		logicalID, _ := json.Marshal(input.LogicalID)
		replayEvent.Extensions[ExtensionLogicalID] = logicalID
	}
	if input.Sequence != 0 {
		sequence, _ := json.Marshal(input.Sequence)
		replayEvent.Extensions[ExtensionSequence] = sequence
	}
	if len(replayEvent.Extensions) == 0 {
		replayEvent.Extensions = nil
	}
	return replayEvent
}

func executeStateMutation(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	input *StateInput,
	deleteValue bool,
	operationIndex int,
) error {
	if input == nil {
		return fmt.Errorf("state input is nil")
	}
	if !backend.Capabilities.State {
		return nil
	}
	value := append([]byte(nil), input.Value...)
	var err error
	switch input.Scope {
	case StateScopeSession:
		if deleteValue {
			value = nil
		}
		err = backend.Session.UpdateSessionState(
			ctx,
			replayCase.Key,
			session.StateMap{input.Key: value},
		)
	case StateScopeUser:
		userKey := session.UserKey{
			AppName: replayCase.Key.AppName,
			UserID:  replayCase.Key.UserID,
		}
		if deleteValue {
			err = backend.Session.DeleteUserState(
				ctx,
				userKey,
				input.Key,
			)
		} else {
			err = backend.Session.UpdateUserState(
				ctx,
				userKey,
				session.StateMap{input.Key: value},
			)
		}
	case StateScopeApp:
		if deleteValue {
			err = backend.Session.DeleteAppState(
				ctx,
				replayCase.Key.AppName,
				input.Key,
			)
		} else {
			err = backend.Session.UpdateAppState(
				ctx,
				replayCase.Key.AppName,
				session.StateMap{input.Key: value},
			)
		}
	default:
		return fmt.Errorf("unsupported state scope %q", input.Scope)
	}
	if err != nil {
		return fmt.Errorf("mutate %s state: %w", input.Scope, err)
	}
	return captureStateTransition(
		ctx,
		backend,
		replayCase,
		state,
		input,
		operationIndex,
	)
}

func captureStateTransition(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	input *StateInput,
	operationIndex int,
) error {
	sess, err := backend.Session.GetSession(ctx, replayCase.Key)
	if err != nil {
		return fmt.Errorf("read state transition: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session missing after state mutation")
	}
	key := input.Key
	switch input.Scope {
	case StateScopeApp:
		key = session.StateAppPrefix + key
	case StateScopeUser:
		key = session.StateUserPrefix + key
	}
	value, exists := sess.GetState(key)
	state.addStateTransition(StateTransition{
		Operation: operationIndex,
		Scope:     input.Scope,
		Key:       input.Key,
		Exists:    exists,
		Value:     normalizeJSONBytes(value),
	})
	return nil
}

func executeMemoryAdd(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	input *MemoryInput,
) error {
	if input == nil {
		return fmt.Errorf("memory input is nil")
	}
	if !backend.Capabilities.Memory {
		return nil
	}
	userKey := replayMemoryUserKey(replayCase)
	var options []memory.AddOption
	if input.Metadata != nil {
		options = append(options, memory.WithMetadata(input.Metadata))
	}
	if err := backend.Memory.AddMemory(
		ctx,
		userKey,
		input.Content,
		append([]string(nil), input.Topics...),
		options...,
	); err != nil {
		return fmt.Errorf("add memory: %w", err)
	}
	id, err := findMemoryID(
		ctx,
		backend.Memory,
		userKey,
		input.Content,
	)
	if err != nil {
		return err
	}
	if input.Ref != "" {
		state.setMemoryRef(input.Ref, id)
	}
	return nil
}

func executeMemoryUpdate(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	input *MemoryInput,
) error {
	if input == nil {
		return fmt.Errorf("memory input is nil")
	}
	if !backend.Capabilities.Memory {
		return nil
	}
	id, ok := state.memoryRef(input.Ref)
	if !ok {
		return fmt.Errorf("memory ref %q is unknown", input.Ref)
	}
	result := &memory.UpdateResult{}
	options := []memory.UpdateOption{memory.WithUpdateResult(result)}
	if input.Metadata != nil {
		options = append(
			options,
			memory.WithUpdateMetadata(input.Metadata),
		)
	}
	key := memory.Key{
		AppName:  replayCase.Key.AppName,
		UserID:   replayCase.Key.UserID,
		MemoryID: id,
	}
	if err := backend.Memory.UpdateMemory(
		ctx,
		key,
		input.Content,
		append([]string(nil), input.Topics...),
		options...,
	); err != nil {
		return fmt.Errorf("update memory: %w", err)
	}
	if result.MemoryID == "" {
		var err error
		result.MemoryID, err = findMemoryID(
			ctx,
			backend.Memory,
			replayMemoryUserKey(replayCase),
			input.Content,
		)
		if err != nil {
			return err
		}
	}
	state.setMemoryRef(input.Ref, result.MemoryID)
	return nil
}

func executeMemoryDelete(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	input *MemoryInput,
) error {
	if input == nil {
		return fmt.Errorf("memory input is nil")
	}
	if !backend.Capabilities.Memory {
		return nil
	}
	id, ok := state.memoryRef(input.Ref)
	if !ok {
		return fmt.Errorf("memory ref %q is unknown", input.Ref)
	}
	if err := backend.Memory.DeleteMemory(ctx, memory.Key{
		AppName:  replayCase.Key.AppName,
		UserID:   replayCase.Key.UserID,
		MemoryID: id,
	}); err != nil {
		return fmt.Errorf("delete memory: %w", err)
	}
	return nil
}

func executeMemorySearch(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	input *MemoryInput,
) error {
	if input == nil {
		return fmt.Errorf("memory input is nil")
	}
	if !backend.Capabilities.Memory {
		return nil
	}
	var options []memory.SearchOption
	if input.Limit > 0 {
		options = append(options, memory.WithSearchOptions(
			memory.SearchOptions{
				Query:      input.Query,
				MaxResults: input.Limit,
			},
		))
	}
	entries, err := backend.Memory.SearchMemories(
		ctx,
		replayMemoryUserKey(replayCase),
		input.Query,
		options...,
	)
	if err != nil {
		return fmt.Errorf("search memories: %w", err)
	}
	search := MemorySearchSnapshot{Query: input.Query}
	for _, entry := range entries {
		search.Results = append(search.Results, NormalizeMemory(entry))
	}
	state.addMemorySearch(search)
	return nil
}

func replayMemoryUserKey(replayCase ReplayCase) memory.UserKey {
	return memory.UserKey{
		AppName: replayCase.Key.AppName,
		UserID:  replayCase.Key.UserID,
	}
}

func findMemoryID(
	ctx context.Context,
	service memory.Service,
	userKey memory.UserKey,
	content string,
) (string, error) {
	entries, err := service.ReadMemories(ctx, userKey, 0)
	if err != nil {
		return "", fmt.Errorf("read memory after write: %w", err)
	}
	for _, entry := range entries {
		if entry != nil &&
			entry.Memory != nil &&
			entry.Memory.Memory == content {
			return entry.ID, nil
		}
	}
	return "", fmt.Errorf("written memory %q was not found", content)
}

func executeSummary(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	input *SummaryInput,
) error {
	if input == nil {
		return fmt.Errorf("summary input is nil")
	}
	if !backend.Capabilities.Summary {
		return nil
	}
	sess, err := backend.Session.GetSession(ctx, replayCase.Key)
	if err != nil {
		return fmt.Errorf("get session before summary: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found before summary")
	}
	if err := backend.Session.CreateSessionSummary(
		ctx,
		sess,
		input.FilterKey,
		input.Force,
	); err != nil {
		return fmt.Errorf("generate summary: %w", err)
	}
	persisted, err := backend.Session.GetSession(ctx, replayCase.Key)
	if err != nil {
		return fmt.Errorf("read summary after write: %w", err)
	}
	if persisted == nil {
		return fmt.Errorf("session not found after summary")
	}
	persisted.SummariesMu.RLock()
	item := persisted.Summaries[input.FilterKey].Clone()
	persisted.SummariesMu.RUnlock()
	if item == nil {
		return fmt.Errorf(
			"summary %q was not persisted",
			input.FilterKey,
		)
	}
	state.addSummary(input.FilterKey, item)
	return nil
}

func executeTrack(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	input *TrackInput,
	operationIndex int,
) error {
	if input == nil {
		return fmt.Errorf("track input is nil")
	}
	if !backend.Capabilities.Track {
		return nil
	}
	service, ok := backend.Session.(session.TrackService)
	if !ok {
		return fmt.Errorf("track capability has no TrackService")
	}
	sess, err := backend.Session.GetSession(ctx, replayCase.Key)
	if err != nil {
		return fmt.Errorf("get session before track append: %w", err)
	}
	if sess == nil {
		return fmt.Errorf("session not found before track append")
	}
	timestamp := input.Timestamp
	if timestamp.IsZero() {
		timestamp = standardTime(operationIndex + 1)
	}
	if err := service.AppendTrackEvent(
		ctx,
		sess,
		&session.TrackEvent{
			Track:     session.Track(input.Name),
			Payload:   append(json.RawMessage(nil), input.Payload...),
			Timestamp: timestamp,
		},
	); err != nil {
		return fmt.Errorf("append track event: %w", err)
	}
	return nil
}

func executeParallel(
	ctx context.Context,
	backend *Backend,
	replayCase ReplayCase,
	state *executionState,
	operations []Operation,
	path string,
	operationIndex int,
) error {
	if len(operations) == 0 {
		return nil
	}
	start := make(chan struct{})
	errs := make([]error, len(operations))
	var wait sync.WaitGroup
	wait.Add(len(operations))
	for i := range operations {
		index := i
		go func() {
			defer wait.Done()
			select {
			case <-ctx.Done():
				errs[index] = ctx.Err()
				return
			case <-start:
			}
			errs[index] = executeWithRetry(
				ctx,
				backend,
				replayCase,
				state,
				operations[index],
				fmt.Sprintf("%s.parallel[%d]", path, index),
				operationIndex*1000+index,
			)
		}()
	}
	close(start)
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
