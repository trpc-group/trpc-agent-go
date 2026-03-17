//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
	"encoding/json"
	"reflect"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	// StateKeyExecutionErrors is the default state key used to store
	// collected execution errors.
	StateKeyExecutionErrors = "execution_errors"

	// ExecutionErrorEventType is the default custom event type used when
	// emitting fallback state for fatal execution errors.
	ExecutionErrorEventType = "execution_error"
)

// ExecutionErrorSeverity describes whether a recorded error was recovered.
type ExecutionErrorSeverity string

const (
	// ExecutionErrorSeverityRecoverable marks errors that were recorded and
	// converted into a replacement node result.
	ExecutionErrorSeverityRecoverable ExecutionErrorSeverity = "recoverable"

	// ExecutionErrorSeverityFatal marks errors that still terminated the graph.
	ExecutionErrorSeverityFatal ExecutionErrorSeverity = "fatal"
)

// ExecutionError captures structured business-visible error details.
type ExecutionError struct {
	Severity   ExecutionErrorSeverity `json:"severity"`
	NodeID     string                 `json:"nodeId,omitempty"`
	NodeName   string                 `json:"nodeName,omitempty"`
	NodeType   NodeType               `json:"nodeType,omitempty"`
	StepNumber int                    `json:"stepNumber,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	Error      *model.ResponseError   `json:"error,omitempty"`
}

// ExecutionErrorPolicy describes how a node error should be handled.
type ExecutionErrorPolicy struct {
	Recover bool

	// Replacement optionally overrides the replacement result used when
	// Recover is true. Prefer State or *Command so the collector can merge the
	// execution_errors update automatically.
	Replacement any

	// ResponseError optionally overrides the structured error fields written
	// into the collected record.
	ResponseError *model.ResponseError
}

// ExecutionErrorPolicyFunc decides how a node error should be recorded.
type ExecutionErrorPolicyFunc func(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
	err error,
) ExecutionErrorPolicy

// ExecutionErrorCollectorOption configures an ExecutionErrorCollector.
type ExecutionErrorCollectorOption func(*ExecutionErrorCollector)

// ExecutionErrorCollector provides reusable graph error collection helpers.
type ExecutionErrorCollector struct {
	stateKey  string
	eventType string
	policy    ExecutionErrorPolicyFunc
}

// NewExecutionErrorCollector creates a new collector.
func NewExecutionErrorCollector(
	opts ...ExecutionErrorCollectorOption,
) *ExecutionErrorCollector {
	collector := &ExecutionErrorCollector{
		stateKey:  StateKeyExecutionErrors,
		eventType: ExecutionErrorEventType,
		policy: func(
			context.Context,
			*NodeCallbackContext,
			State,
			error,
		) ExecutionErrorPolicy {
			return ExecutionErrorPolicy{}
		},
	}
	for _, opt := range opts {
		opt(collector)
	}
	return collector
}

// WithExecutionErrorStateKey overrides the state key used by the collector.
func WithExecutionErrorStateKey(
	key string,
) ExecutionErrorCollectorOption {
	return func(c *ExecutionErrorCollector) {
		if key != "" {
			c.stateKey = key
		}
	}
}

// WithExecutionErrorEventType overrides the custom event type used on fatal
// fallback state emission.
func WithExecutionErrorEventType(
	eventType string,
) ExecutionErrorCollectorOption {
	return func(c *ExecutionErrorCollector) {
		if eventType != "" {
			c.eventType = eventType
		}
	}
}

// WithExecutionErrorPolicy sets a custom error handling policy.
func WithExecutionErrorPolicy(
	policy ExecutionErrorPolicyFunc,
) ExecutionErrorCollectorOption {
	return func(c *ExecutionErrorCollector) {
		if policy != nil {
			c.policy = policy
		}
	}
}

// WithRecoverableExecutionErrors marks matching errors as recoverable.
func WithRecoverableExecutionErrors(
	shouldRecover func(error) bool,
) ExecutionErrorCollectorOption {
	return WithExecutionErrorPolicy(func(
		ctx context.Context,
		callbackCtx *NodeCallbackContext,
		state State,
		err error,
	) ExecutionErrorPolicy {
		if shouldRecover == nil || !shouldRecover(err) {
			return ExecutionErrorPolicy{}
		}
		return ExecutionErrorPolicy{Recover: true}
	})
}

// StateKey returns the state key used by the collector.
func (c *ExecutionErrorCollector) StateKey() string {
	return c.stateKey
}

// StateField returns a StateField suitable for collecting execution errors.
func (c *ExecutionErrorCollector) StateField() StateField {
	return StateField{
		Type:    reflect.TypeOf([]ExecutionError{}),
		Reducer: ExecutionErrorSliceReducer,
		Default: func() any { return []ExecutionError{} },
	}
}

// AddField registers the collector's state field onto a schema.
func (c *ExecutionErrorCollector) AddField(
	schema *StateSchema,
) *StateSchema {
	if schema == nil {
		schema = NewStateSchema()
	}
	schema.AddField(c.stateKey, c.StateField())
	return schema
}

// NodeCallbacks returns callbacks that collect execution errors on node
// failure.
func (c *ExecutionErrorCollector) NodeCallbacks() *NodeCallbacks {
	return NewNodeCallbacks().RegisterAfterNode(c.afterNode)
}

// SubgraphStateUpdate extracts collected execution errors from a child agent
// result so the parent graph can merge them into its own state.
func (c *ExecutionErrorCollector) SubgraphStateUpdate(
	result SubgraphResult,
) State {
	executionErrors, err := ExecutionErrorsFromStateDelta(
		result.RawStateDelta,
		c.stateKey,
	)
	if err != nil || len(executionErrors) == 0 {
		return nil
	}
	return State{
		c.stateKey: executionErrors,
	}
}

// SubgraphOutputMapper returns a mapper that merges child execution errors
// into the parent graph state.
func (c *ExecutionErrorCollector) SubgraphOutputMapper() SubgraphOutputMapper {
	return func(parent State, result SubgraphResult) State {
		return c.SubgraphStateUpdate(result)
	}
}

// NewExecutionError creates a structured record from a node callback context.
func NewExecutionError(
	callbackCtx *NodeCallbackContext,
	err error,
	severity ExecutionErrorSeverity,
) ExecutionError {
	respErr := model.ResponseErrorFromError(err, model.ErrorTypeFlowError)
	record := ExecutionError{
		Severity:  severity,
		Timestamp: time.Now(),
		Error:     cloneExecutionResponseError(respErr),
	}
	if callbackCtx == nil {
		return record
	}
	record.NodeID = callbackCtx.NodeID
	record.NodeName = callbackCtx.NodeName
	record.NodeType = callbackCtx.NodeType
	record.StepNumber = callbackCtx.StepNumber
	return record
}

// ExecutionErrorSliceReducer appends execution error slices.
func ExecutionErrorSliceReducer(existing, update any) any {
	if existing == nil {
		existing = []ExecutionError{}
	}
	existingSlice, ok1 := existing.([]ExecutionError)
	updateSlice, ok2 := update.([]ExecutionError)
	if !ok1 || !ok2 {
		return update
	}

	merged := make([]ExecutionError, 0, len(existingSlice)+len(updateSlice))
	merged = append(merged, cloneExecutionErrors(existingSlice)...)
	merged = append(merged, cloneExecutionErrors(updateSlice)...)
	return merged
}

// DecodeExecutionErrors unmarshals a serialized execution error slice.
func DecodeExecutionErrors(
	raw []byte,
) ([]ExecutionError, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var executionErrors []ExecutionError
	if err := json.Unmarshal(raw, &executionErrors); err != nil {
		return nil, err
	}
	return cloneExecutionErrors(executionErrors), nil
}

// ExecutionErrorsFromStateDelta extracts execution errors from an event state
// delta using the provided state key.
func ExecutionErrorsFromStateDelta(
	stateDelta map[string][]byte,
	key string,
) ([]ExecutionError, error) {
	if len(stateDelta) == 0 {
		return nil, nil
	}
	raw, ok := stateDelta[key]
	if !ok {
		return nil, nil
	}
	return DecodeExecutionErrors(raw)
}

func (c *ExecutionErrorCollector) afterNode(
	ctx context.Context,
	callbackCtx *NodeCallbackContext,
	state State,
	result any,
	nodeErr error,
) (any, error) {
	if nodeErr == nil {
		return nil, nil
	}

	policy := c.policy(ctx, callbackCtx, state, nodeErr)
	severity := ExecutionErrorSeverityFatal
	if policy.Recover {
		severity = ExecutionErrorSeverityRecoverable
	}
	record := NewExecutionError(callbackCtx, nodeErr, severity)
	if policy.ResponseError != nil {
		record.Error = cloneExecutionResponseError(policy.ResponseError)
	}

	update := State{
		c.stateKey: []ExecutionError{record},
	}

	if policy.Recover {
		return mergeExecutionErrorReplacement(
			policy.Replacement,
			update,
		), nil
	}

	if err := EmitCustomStateDelta(
		ctx,
		state,
		update,
		WithStateDeltaEventType(c.eventType),
		WithStateDeltaEventMessage(executionErrorMessage(record)),
		WithStateDeltaEventPayload(record),
	); err != nil {
		log.WarnfContext(
			ctx,
			"graph: failed to emit execution error state delta: %v",
			err,
		)
	}
	return nil, nil
}

func mergeExecutionErrorReplacement(
	replacement any,
	update State,
) any {
	switch value := replacement.(type) {
	case nil:
		return update
	case State:
		return mergeStateForExecutionError(value, update)
	case *Command:
		if value == nil {
			return &Command{Update: update}
		}
		cloned := *value
		cloned.Update = mergeStateForExecutionError(cloned.Update, update)
		return &cloned
	default:
		return replacement
	}
}

func mergeStateForExecutionError(
	dst State,
	update State,
) State {
	if dst == nil {
		dst = State{}
	}
	merged := dst.Clone()
	for key, value := range update {
		merged[key] = value
	}
	return merged
}

func cloneExecutionErrors(
	executionErrors []ExecutionError,
) []ExecutionError {
	if len(executionErrors) == 0 {
		return nil
	}
	cloned := make([]ExecutionError, len(executionErrors))
	for i := range executionErrors {
		cloned[i] = executionErrors[i]
		cloned[i].Error = cloneExecutionResponseError(
			executionErrors[i].Error,
		)
	}
	return cloned
}

func cloneExecutionResponseError(
	err *model.ResponseError,
) *model.ResponseError {
	if err == nil {
		return nil
	}
	cloned := *err
	if err.Code != nil {
		code := *err.Code
		cloned.Code = &code
	}
	if err.Param != nil {
		param := *err.Param
		cloned.Param = &param
	}
	return &cloned
}

func executionErrorMessage(
	record ExecutionError,
) string {
	if record.Error == nil {
		return ""
	}
	return record.Error.Message
}
