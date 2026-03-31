//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
)

// Recorder records runner event streams into evalset assets via an injected evalset.Manager.
type Recorder struct {
	manager            evalset.Manager
	name               string
	asyncWriteEnabled  bool
	writeTimeout       time.Duration
	evalSetIDResolver  EvalSetIDResolver
	evalCaseIDResolver EvalCaseIDResolver
	traceModeEnabled   bool
	accumulators       sync.Map // It maps request IDs to in-progress accumulators.
	locker             *keyedLocker
	writeMu            sync.Mutex
	closed             bool
	writesWg           sync.WaitGroup
}

var _ plugin.Plugin = (*Recorder)(nil)
var _ plugin.Closer = (*Recorder)(nil)

// New creates a Recorder plugin.
func New(manager evalset.Manager, opts ...Option) (*Recorder, error) {
	if manager == nil {
		return nil, errors.New("evalset manager is nil")
	}
	opt, err := newOptions(opts...)
	if err != nil {
		return nil, err
	}
	return &Recorder{
		manager:            manager,
		name:               opt.name,
		asyncWriteEnabled:  opt.asyncWriteEnabled,
		writeTimeout:       opt.writeTimeout,
		evalSetIDResolver:  opt.evalSetIDResolver,
		evalCaseIDResolver: opt.evalCaseIDResolver,
		traceModeEnabled:   opt.traceModeEnabled,
		locker:             newKeyedLocker(),
	}, nil
}

// Name implements plugin.Plugin.
func (r *Recorder) Name() string {
	return r.name
}

// Register implements plugin.Plugin.
func (r *Recorder) Register(reg *plugin.Registry) {
	reg.OnEvent(r.onEvent)
}

// Close waits for in-flight async writes to finish.
func (r *Recorder) Close(ctx context.Context) error {
	r.writeMu.Lock()
	r.closed = true
	r.writeMu.Unlock()
	done := make(chan struct{})
	go func() {
		r.writesWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) onEvent(
	ctx context.Context,
	inv *agent.Invocation,
	e *event.Event,
) (*event.Event, error) {
	if inv == nil || e == nil {
		return nil, nil
	}
	requestID := e.RequestID
	if requestID == "" {
		requestID = inv.RunOptions.RequestID
	}
	if requestID == "" {
		return nil, nil
	}
	isCompletion := e.IsRunnerCompletion()
	isError := e.IsTerminalError()
	var acc *accumulator
	if isCompletion {
		v, ok := r.accumulators.Load(requestID)
		if !ok {
			return nil, nil
		}
		typed, ok := v.(*accumulator)
		if !ok || typed == nil {
			r.accumulators.Delete(requestID)
			return nil, nil
		}
		acc = typed
	} else {
		actual, loaded := r.accumulators.LoadOrStore(requestID, newAccumulator())
		typed, ok := actual.(*accumulator)
		if !ok || typed == nil {
			r.accumulators.Delete(requestID)
			return nil, nil
		}
		acc = typed
		if !loaded {
			acc.captureRunInputs(
				inv.RunOptions.RuntimeState,
				inv.RunOptions.InjectedContextMessages,
			)
			acc.setUserContent(inv.Message)
		}
	}
	if !isCompletion && acc.isFinalized() {
		return nil, nil
	}
	if isError {
		r.handleRunError(ctx, inv, requestID, acc, e)
		return nil, nil
	}
	if e.Response != nil {
		r.handleResponseEvent(acc, e.Response)
	}
	if isCompletion {
		r.handleRunCompletion(ctx, inv, requestID, acc, e.Timestamp)
	}
	return nil, nil
}

func (r *Recorder) handleRunError(
	ctx context.Context,
	inv *agent.Invocation,
	requestID string,
	acc *accumulator,
	e *event.Event,
) {
	if acc.isFinalized() {
		r.accumulators.Delete(requestID)
		return
	}
	if e.Error != nil {
		acc.setRunError(*e.Error)
	} else {
		acc.setRunError(model.ResponseError{Type: "unknown", Message: "unknown"})
	}
	snapshot := acc.finalizeAndSnapshot()
	turn, err := r.buildTurn(ctx, inv, requestID, snapshot, true, e.Timestamp)
	if err != nil {
		log.ErrorfContext(ctx, "evalset recorder: build error turn failed: %v", err)
		r.accumulators.Delete(requestID)
		return
	}
	r.accumulators.Delete(requestID)
	r.startWrite(ctx, turn)
}

func (r *Recorder) handleResponseEvent(acc *accumulator, rsp *model.Response) {
	if rsp.IsToolCallResponse() {
		for _, choice := range rsp.Choices {
			for _, tc := range choice.Message.ToolCalls {
				acc.addToolCall(tc)
			}
		}
	}
	if rsp.IsToolResultResponse() {
		for _, choice := range rsp.Choices {
			toolID := choice.Message.ToolID
			toolName := choice.Message.ToolName
			content := choice.Message.Content
			acc.addToolResult(toolID, toolName, content)
		}
	}
	if rsp.IsFinalResponse() {
		msg, ok := extractAssistantContentMessage(rsp)
		if ok {
			acc.setFinalResponse(msg)
		}
		return
	}
	if rsp.IsPartial || rsp.IsToolCallResponse() || rsp.IsToolResultResponse() {
		return
	}
	msg, ok := extractAssistantContentMessage(rsp)
	if ok {
		acc.addIntermediateResponse(msg)
	}
}

func (r *Recorder) handleRunCompletion(
	ctx context.Context,
	inv *agent.Invocation,
	requestID string,
	acc *accumulator,
	completionTime time.Time,
) {
	if acc.isFinalized() {
		r.accumulators.Delete(requestID)
		return
	}
	snapshot := acc.finalizeAndSnapshot()
	turn, err := r.buildTurn(ctx, inv, requestID, snapshot, false, completionTime)
	if err != nil {
		log.WarnfContext(ctx, "evalset recorder: build completion turn failed: %v", err)
		r.accumulators.Delete(requestID)
		return
	}
	r.accumulators.Delete(requestID)
	r.startWrite(ctx, turn)
}

func (r *Recorder) buildTurn(
	ctx context.Context,
	inv *agent.Invocation,
	requestID string,
	snapshot turnSnapshot,
	isError bool,
	createdAt time.Time,
) (*turnToPersist, error) {
	if inv == nil {
		return nil, errors.New("invocation is nil")
	}
	if inv.Session == nil {
		return nil, errors.New("session is nil")
	}
	if requestID == "" {
		return nil, errors.New("request id is empty")
	}
	if !snapshot.hasUserContent {
		return nil, errors.New("user content is missing")
	}
	appName := inv.Session.AppName
	if appName == "" {
		return nil, errors.New("app name is empty")
	}
	evalSetID, err := r.resolveEvalSetID(ctx, inv)
	if err != nil {
		return nil, fmt.Errorf("resolve eval set id: %w", err)
	}
	evalCaseID, err := r.resolveEvalCaseID(ctx, inv)
	if err != nil {
		return nil, fmt.Errorf("resolve eval case id: %w", err)
	}
	if evalSetID == "" || evalCaseID == "" {
		return nil, errors.New("eval set id or eval case id is empty")
	}
	userContent := snapshot.userContent
	finalResponse, err := buildFinalResponse(snapshot, isError)
	if err != nil {
		return nil, err
	}
	intermediate := make([]*model.Message, 0, len(snapshot.intermediateResponses))
	for _, msg := range snapshot.intermediateResponses {
		copied := msg
		intermediate = append(intermediate, &copied)
	}
	contextMessages := make([]*model.Message, 0, len(snapshot.contextMessages))
	for _, msg := range snapshot.contextMessages {
		copied := msg
		contextMessages = append(contextMessages, &copied)
	}
	invocation := &evalset.Invocation{
		InvocationID:          requestID,
		ContextMessages:       contextMessages,
		UserContent:           &userContent,
		FinalResponse:         finalResponse,
		Tools:                 snapshot.tools,
		IntermediateResponses: intermediate,
	}
	if !createdAt.IsZero() {
		invocation.CreationTimestamp = &epochtime.EpochTime{Time: createdAt}
	}
	userID := inv.Session.UserID
	sessionIn := &evalset.SessionInput{
		AppName: appName,
		UserID:  userID,
		State:   snapshot.sessionInputState,
	}
	evalMode := evalset.EvalModeDefault
	if r.traceModeEnabled {
		evalMode = evalset.EvalModeTrace
	}
	return &turnToPersist{
		appName:         appName,
		evalSetID:       evalSetID,
		evalCaseID:      evalCaseID,
		evalMode:        evalMode,
		sessionIn:       sessionIn,
		contextMessages: contextMessages,
		invocation:      invocation,
	}, nil
}

func buildFinalResponse(snapshot turnSnapshot, isError bool) (*model.Message, error) {
	if isError {
		if !snapshot.hasRunError {
			return nil, errors.New("run error is missing")
		}
		msg := model.NewAssistantMessage(formatRunError(snapshot.runError))
		return &msg, nil
	}
	if snapshot.hasFinalResponse && model.HasPayload(snapshot.finalResponse) {
		final := snapshot.finalResponse
		return &final, nil
	}
	return nil, nil
}

func formatRunError(err model.ResponseError) string {
	errType := err.Type
	if errType == "" {
		errType = "unknown"
	}
	errMsg := err.Message
	if errMsg == "" {
		errMsg = "unknown"
	}
	return fmt.Sprintf("[RUN_ERROR] %s: %s", errType, errMsg)
}

func (r *Recorder) resolveEvalSetID(ctx context.Context, inv *agent.Invocation) (string, error) {
	if r.evalSetIDResolver != nil {
		return r.evalSetIDResolver(ctx, inv)
	}
	return inv.Session.ID, nil
}

func (r *Recorder) resolveEvalCaseID(ctx context.Context, inv *agent.Invocation) (string, error) {
	if r.evalCaseIDResolver != nil {
		return r.evalCaseIDResolver(ctx, inv)
	}
	return inv.Session.ID, nil
}

func (r *Recorder) startWrite(ctx context.Context, turn *turnToPersist) {
	if err := ctx.Err(); err != nil {
		log.ErrorfContext(ctx, "evalset recorder: start write failed: %v", err)
		return
	}
	if !r.asyncWriteEnabled {
		persistCtx := ctx
		cancel := func() {}
		if r.writeTimeout > 0 {
			persistCtx, cancel = context.WithTimeout(ctx, r.writeTimeout)
		}
		if err := r.persistTurn(persistCtx, turn); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.ErrorfContext(persistCtx, "evalset recorder: persist turn failed: %v", err)
		}
		cancel()
		return
	}
	r.writeMu.Lock()
	if r.closed {
		r.writeMu.Unlock()
		return
	}
	r.writesWg.Add(1)
	r.writeMu.Unlock()
	persistBase := context.WithoutCancel(agent.CloneContext(ctx))
	persistCtx := persistBase
	cancel := func() {}
	if r.writeTimeout > 0 {
		persistCtx, cancel = context.WithTimeout(persistBase, r.writeTimeout)
	}
	go func() {
		defer r.writesWg.Done()
		defer cancel()
		if err := r.persistTurn(persistCtx, turn); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			log.ErrorfContext(persistCtx, "evalset recorder: persist turn failed: %v", err)
		}
	}()
}

func extractAssistantContentMessage(rsp *model.Response) (model.Message, bool) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return model.Message{}, false
	}
	for _, choice := range rsp.Choices {
		if choice.Message.Role == model.RoleAssistant && model.HasPayload(choice.Message) {
			return choice.Message, true
		}
	}
	return model.Message{}, false
}
