//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package runner wraps a trpc-agent-go runner and translates it to AG-UI events.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	// ErrRunAlreadyExists is returned when a run with the same key is already running.
	ErrRunAlreadyExists = errors.New("agui: run already exists")
	// ErrRunNotFound is returned when a run key cannot be found.
	ErrRunNotFound = errors.New("agui: run not found")
)

// Runner executes AG-UI runs and emits AG-UI events.
type Runner interface {
	// Run starts processing one AG-UI run request and returns a channel of AG-UI events.
	Run(ctx context.Context, runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error)
}

// New wraps a trpc-agent-go runner with AG-UI specific translation logic.
func New(r trunner.Runner, opt ...Option) Runner {
	opts := NewOptions(opt...)
	var tracker track.Tracker
	if opts.SessionService != nil {
		var err error
		tracker, err = track.New(opts.SessionService,
			track.WithAggregatorFactory(opts.AggregatorFactory),
			track.WithAggregationOption(opts.AggregationOption...),
			track.WithFlushInterval(opts.FlushInterval),
		)
		if err != nil {
			log.Warnf("agui: tracker disabled: %v", err)
		}
	}
	run := &runner{
		runner:                                 r,
		appName:                                opts.AppName,
		translatorFactory:                      opts.TranslatorFactory,
		graphNodeLifecycleActivityEnabled:      opts.GraphNodeLifecycleActivityEnabled,
		graphNodeInterruptActivityEnabled:      opts.GraphNodeInterruptActivityEnabled,
		graphNodeInterruptActivityTopLevelOnly: opts.GraphNodeInterruptActivityTopLevelOnly,
		reasoningContentEnabled:                opts.ReasoningContentEnabled,
		userIDResolver:                         opts.UserIDResolver,
		translateCallbacks:                     opts.TranslateCallbacks,
		runAgentInputHook:                      opts.RunAgentInputHook,
		stateResolver:                          opts.StateResolver,
		runOptionResolver:                      opts.RunOptionResolver,
		tracker:                                tracker,
		running:                                make(map[session.Key]*sessionContext),
		startSpan:                              opts.StartSpan,
		flushInterval:                          opts.FlushInterval,
		timeout:                                opts.Timeout,
		cancelOnContextDoneEnabled:             opts.CancelOnContextDoneEnabled,
		messagesSnapshotFollowEnabled:          opts.MessagesSnapshotFollowEnabled,
		messagesSnapshotFollowMaxDuration:      opts.MessagesSnapshotFollowMaxDuration,
	}
	return run
}

// runner is the default implementation of the Runner.
type runner struct {
	appName                                string
	runner                                 trunner.Runner
	translatorFactory                      TranslatorFactory
	graphNodeLifecycleActivityEnabled      bool
	graphNodeInterruptActivityEnabled      bool
	graphNodeInterruptActivityTopLevelOnly bool
	reasoningContentEnabled                bool
	userIDResolver                         UserIDResolver
	translateCallbacks                     *translator.Callbacks
	runAgentInputHook                      RunAgentInputHook
	stateResolver                          StateResolver
	runOptionResolver                      RunOptionResolver
	tracker                                track.Tracker
	runningMu                              sync.Mutex
	running                                map[session.Key]*sessionContext
	startSpan                              StartSpan
	flushInterval                          time.Duration
	timeout                                time.Duration
	cancelOnContextDoneEnabled             bool
	messagesSnapshotFollowEnabled          bool
	messagesSnapshotFollowMaxDuration      time.Duration
}

type sessionContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

type runInput struct {
	key            session.Key
	threadID       string
	runID          string
	userID         string
	inputMessage   *model.Message
	inputMessageID string
	userMessage    *types.Message
	runOption      []agent.RunOption
	translator     translator.Translator
	enableTrack    bool
	span           trace.Span
	resume         *resumeInfo
}

type resumeInfo struct {
	lineageID    string
	checkpointID string
	resumeMap    map[string]any
	resumeSet    bool
	resumeValue  any
}

func inputMessageFromRunAgentInput(input *adapter.RunAgentInput) (*model.Message, string, *types.Message, error) {
	if len(input.Messages) == 0 {
		return nil, "", nil, errors.New("no messages provided")
	}
	lastMessage := input.Messages[len(input.Messages)-1]
	if lastMessage.Role != types.RoleUser && lastMessage.Role != types.RoleTool {
		return nil, "", nil, errors.New("last message role must be user or tool")
	}
	if lastMessage.Role == types.RoleTool {
		if lastMessage.ToolCallID == "" {
			return nil, "", nil, errors.New("tool message missing tool call id")
		}
		content, ok := lastMessage.ContentString()
		if !ok {
			return nil, "", nil, errors.New("last message content is not a string")
		}
		inputMessage := model.Message{
			Role:     model.RoleTool,
			Content:  content,
			ToolID:   lastMessage.ToolCallID,
			ToolName: lastMessage.Name,
		}
		return &inputMessage, lastMessage.ID, nil, nil
	}
	if content, ok := lastMessage.ContentString(); ok {
		inputMessage := model.Message{
			Role:    model.RoleUser,
			Content: content,
		}
		userMessage := lastMessage
		return &inputMessage, lastMessage.ID, &userMessage, nil
	}
	contents, ok := lastMessage.ContentInputContents()
	if !ok {
		return nil, "", nil, errors.New("last message content is not a string")
	}
	inputMessage, err := multimodal.UserMessageFromInputContents(contents)
	if err != nil {
		return nil, "", nil, fmt.Errorf("parse user message input contents: %w", err)
	}
	userMessage := lastMessage
	userMessage.Content = contents
	return &inputMessage, lastMessage.ID, &userMessage, nil
}

// Run starts processing one AG-UI run request and returns a channel of AG-UI events.
func (r *runner) Run(ctx context.Context, runAgentInput *adapter.RunAgentInput) (<-chan aguievents.Event, error) {
	if r.runner == nil {
		return nil, errors.New("runner is nil")
	}
	if runAgentInput == nil {
		return nil, errors.New("run input cannot be nil")
	}
	runAgentInput, err := r.applyRunAgentInputHook(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("run input hook: %w", err)
	}
	threadID := runAgentInput.ThreadID
	runID := runAgentInput.RunID
	inputMessage, inputMessageID, userMessage, err := inputMessageFromRunAgentInput(runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("build input message: %w", err)
	}
	userID, err := r.userIDResolver(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve user ID: %w", err)
	}
	runOption, err := r.runOptionResolver(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve run option: %w", err)
	}
	runtimeState, err := r.stateResolver(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve state: %w", err)
	}
	if runtimeState != nil {
		runOption = append(runOption, agent.WithRuntimeState(runtimeState))
	}
	ctx, span, err := r.startSpan(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("start span: %w", err)
	}
	trans, err := r.translatorFactory(
		ctx,
		runAgentInput,
		translator.WithGraphNodeLifecycleActivityEnabled(r.graphNodeLifecycleActivityEnabled),
		translator.WithGraphNodeInterruptActivityEnabled(r.graphNodeInterruptActivityEnabled),
		translator.WithGraphNodeInterruptActivityTopLevelOnly(r.graphNodeInterruptActivityTopLevelOnly),
		translator.WithReasoningContentEnabled(r.reasoningContentEnabled),
	)
	if err != nil {
		span.End()
		return nil, fmt.Errorf("create translator: %w", err)
	}
	input := &runInput{
		key: session.Key{
			AppName:   r.appName,
			UserID:    userID,
			SessionID: runAgentInput.ThreadID,
		},
		threadID:       threadID,
		runID:          runID,
		userID:         userID,
		inputMessage:   inputMessage,
		inputMessageID: inputMessageID,
		userMessage:    userMessage,
		runOption:      runOption,
		translator:     trans,
		enableTrack:    r.tracker != nil,
		span:           span,
		resume:         parseResumeInfo(runOption),
	}
	events := make(chan aguievents.Event)
	ctx, cancel := r.newExecutionContext(ctx, r.timeout)
	if err := r.register(input.key, ctx, cancel); err != nil {
		cancel()
		span.End()
		return nil, fmt.Errorf("register running context: %w", err)
	}
	go r.run(ctx, cancel, input.key, input, events)
	return events, nil
}

func (r *runner) run(ctx context.Context, cancel context.CancelFunc, key session.Key, input *runInput, events chan<- aguievents.Event) {
	defer r.unregister(key)
	defer cancel()
	defer input.span.End()
	defer close(events)
	threadID := input.threadID
	runID := input.runID
	if input.enableTrack {
		defer func() {
			if err := r.tracker.Flush(ctx, input.key); err != nil {
				log.WarnfContext(
					ctx,
					"agui run: threadID: %s, runID: %s, "+
						"flush track events: %v",
					threadID,
					runID,
					err,
				)
			}
		}()
		if input.inputMessage.Role == model.RoleUser {
			if err := r.recordUserMessage(ctx, input.key, input.userMessage); err != nil {
				log.WarnfContext(
					ctx,
					"agui run: threadID: %s, runID: %s, record input "+
						"message failed, disable tracking: %v",
					threadID,
					runID,
					err,
				)
			}
		}
	}
	if !r.emitEvent(ctx, events, aguievents.NewRunStartedEvent(threadID, runID), input) {
		return
	}
	if input.inputMessage.Role == model.RoleTool {
		if !r.emitToolResultEvent(ctx, events, input) {
			return
		}
	}
	if input.resume != nil && r.graphNodeInterruptActivityEnabled {
		if !r.emitEvent(ctx, events, newGraphInterruptResumeEvent(input.resume), input) {
			return
		}
	}
	ch, err := r.runner.Run(ctx, input.userID, threadID, *input.inputMessage, input.runOption...)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui run: threadID: %s, runID: %s, run agent: %v",
			threadID,
			runID,
			err,
		)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("run agent: %v", err),
			aguievents.WithRunID(runID)), input)
		return
	}
	for {
		select {
		case <-ctx.Done():
			log.ErrorfContext(ctx, "agui run: threadID: %s, runID: %s, err: %v", threadID, runID, ctx.Err())
			return
		case agentEvent, ok := <-ch:
			if !ok {
				return
			}
			if !r.handleAgentEvent(ctx, events, input, agentEvent) {
				return
			}
		}
	}
}

func parseResumeInfo(opt []agent.RunOption) *resumeInfo {
	if len(opt) == 0 {
		return nil
	}
	opts := &agent.RunOptions{}
	for _, o := range opt {
		o(opts)
	}
	state := opts.RuntimeState
	if len(state) == 0 {
		return nil
	}
	var cmd *graph.Command
	var resumeCmd *graph.ResumeCommand
	if rawCmd, ok := state[graph.StateKeyCommand]; ok {
		cmd, _ = rawCmd.(*graph.Command)
		if cmd == nil {
			resumeCmd, _ = rawCmd.(*graph.ResumeCommand)
		}
	}
	var resumeMap map[string]any
	if cmd != nil && cmd.ResumeMap != nil && len(cmd.ResumeMap) > 0 {
		resumeMap = cmd.ResumeMap
	}
	cmdBindsResumeMap := cmd != nil && cmd.ResumeMap != nil
	if resumeMap == nil &&
		resumeCmd != nil &&
		resumeCmd.ResumeMap != nil &&
		len(resumeCmd.ResumeMap) > 0 {
		resumeMap = resumeCmd.ResumeMap
	}
	cmdBindsResumeMap = cmdBindsResumeMap ||
		(resumeCmd != nil && resumeCmd.ResumeMap != nil)
	if resumeMap == nil && !cmdBindsResumeMap {
		switch v := state[graph.StateKeyResumeMap].(type) {
		case map[string]any:
			if len(v) > 0 {
				resumeMap = v
			}
		case graph.State:
			if len(v) > 0 {
				resumeMap = map[string]any(v)
			}
		default:
		}
	}
	var resumeValue any
	resumeSet := false
	if cmd != nil && cmd.Resume != nil {
		resumeSet = true
		resumeValue = cmd.Resume
	}
	if !resumeSet && resumeCmd != nil && resumeCmd.Resume != nil {
		resumeSet = true
		resumeValue = resumeCmd.Resume
	}
	if !resumeSet {
		if rawResume, ok := state[graph.ResumeChannel]; ok {
			resumeSet = true
			resumeValue = rawResume
		}
	}
	if resumeMap == nil && !resumeSet {
		return nil
	}
	var lineageID, checkpointID string
	if rawLineageID, ok := state[graph.CfgKeyLineageID].(string); ok {
		lineageID = rawLineageID
	}
	if rawCheckpointID, ok := state[graph.CfgKeyCheckpointID].(string); ok {
		checkpointID = rawCheckpointID
	}
	return &resumeInfo{
		lineageID:    lineageID,
		checkpointID: checkpointID,
		resumeMap:    resumeMap,
		resumeSet:    resumeSet,
		resumeValue:  resumeValue,
	}
}

func newGraphInterruptResumeEvent(info *resumeInfo) *aguievents.ActivityDeltaEvent {
	if info == nil {
		return nil
	}
	resumeValue := make(map[string]any)
	if info.resumeMap != nil {
		resumeValue["resumeMap"] = info.resumeMap
	}
	if info.lineageID != "" {
		resumeValue["lineageId"] = info.lineageID
	}
	if info.checkpointID != "" {
		resumeValue["checkpointId"] = info.checkpointID
	}
	if info.resumeSet {
		resumeValue["resume"] = info.resumeValue
	}
	patch := []aguievents.JSONPatchOperation{
		{Op: "add", Path: "/interrupt", Value: json.RawMessage("null")},
		{Op: "add", Path: "/resume", Value: resumeValue},
	}
	return aguievents.NewActivityDeltaEvent(uuid.NewString(), "graph.node.interrupt", patch)
}

func (r *runner) emitToolResultEvent(ctx context.Context, events chan<- aguievents.Event, input *runInput) bool {
	msg := input.inputMessage
	if msg.ToolID == "" {
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent("tool message missing tool id",
			aguievents.WithRunID(input.runID)), input)
		return false
	}
	messageID := input.inputMessageID
	if messageID == "" {
		messageID = msg.ToolID
	}
	toolResultEvent := aguievents.NewToolCallResultEvent(messageID, msg.ToolID, msg.Content)
	return r.emitEvent(ctx, events, toolResultEvent, input)
}

func (r *runner) handleAgentEvent(ctx context.Context, events chan<- aguievents.Event, input *runInput, event *event.Event) bool {
	threadID := input.threadID
	runID := input.runID
	customEvent, err := r.handleBeforeTranslate(ctx, event)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui run: threadID: %s, runID: %s, before "+
				"translate callback: %v",
			threadID,
			runID,
			err,
		)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("before translate callback: %v", err),
			aguievents.WithRunID(runID)), input)
		return false
	}
	aguiEvents, err := input.translator.Translate(ctx, customEvent)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui run: threadID: %s, runID: %s, translate "+
				"event: %v",
			threadID,
			runID,
			err,
		)
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent(fmt.Sprintf("translate event: %v", err),
			aguievents.WithRunID(runID)), input)
		return false
	}
	for _, aguiEvent := range aguiEvents {
		if !r.emitEvent(ctx, events, aguiEvent, input) {
			return false
		}
	}
	return true
}

func (r *runner) applyRunAgentInputHook(ctx context.Context,
	input *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
	if r.runAgentInputHook == nil {
		return input, nil
	}
	newInput, err := r.runAgentInputHook(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("run agent input hook: %w", err)
	}
	if newInput == nil {
		return input, nil
	}
	return newInput, nil
}

func (r *runner) handleBeforeTranslate(ctx context.Context, event *event.Event) (*event.Event, error) {
	if r.translateCallbacks == nil {
		return event, nil
	}
	customEvent, err := r.translateCallbacks.RunBeforeTranslate(ctx, event)
	if err != nil {
		return nil, fmt.Errorf("translate callbacks before translate: %w", err)
	}
	if customEvent != nil {
		return customEvent, nil
	}
	return event, nil
}

func (r *runner) handleAfterTranslate(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
	if r.translateCallbacks == nil {
		return event, nil
	}
	customEvent, err := r.translateCallbacks.RunAfterTranslate(ctx, event)
	if err != nil {
		return nil, fmt.Errorf("translate callbacks after translate: %w", err)
	}
	if customEvent != nil {
		return customEvent, nil
	}
	return event, nil
}

func (r *runner) emitEvent(ctx context.Context, events chan<- aguievents.Event, event aguievents.Event,
	input *runInput) bool {
	event, err := r.handleAfterTranslate(ctx, event)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"agui emit event: original event: %v, threadID: %s, "+
				"runID: %s, after translate callback: %v",
			event,
			input.threadID,
			input.runID,
			err,
		)
		select {
		case events <- aguievents.NewRunErrorEvent(fmt.Sprintf("after translate callback: %v", err),
			aguievents.WithRunID(input.runID)):
		case <-ctx.Done():
			log.ErrorfContext(ctx, "agui emit event: context done, threadID: %s, runID: %s, err: %v",
				input.threadID, input.runID, ctx.Err())
		}
		return false
	}
	log.DebugfContext(
		ctx,
		"agui emit event: emitted event: %v, threadID: %s, runID: %s",
		event,
		input.threadID,
		input.runID,
	)
	if input.enableTrack {
		if err := r.recordTrackEvent(ctx, input.key, event); err != nil {
			log.WarnfContext(
				ctx,
				"agui emit event: record track event failed: "+
					"threadID: %s, runID: %s, err: %v",
				input.threadID,
				input.runID,
				err,
			)
		}
	}
	select {
	case events <- event:
		return true
	case <-ctx.Done():
		log.ErrorfContext(ctx, "agui emit event: context done, threadID: %s, runID: %s, err: %v",
			input.threadID, input.runID, ctx.Err())
		return false
	}
}

func (r *runner) recordUserMessage(ctx context.Context, key session.Key, message *types.Message) error {
	if message == nil {
		return errors.New("user message is nil")
	}
	if message.Role != types.RoleUser {
		return fmt.Errorf("user message role must be user: %s", message.Role)
	}
	userMessage := *message
	if userMessage.ID == "" {
		userMessage.ID = uuid.NewString()
	}
	if userMessage.Name == "" {
		userMessage.Name = key.UserID
	}
	evt := aguievents.NewCustomEvent(multimodal.CustomEventNameUserMessage, aguievents.WithValue(userMessage))
	if err := r.recordTrackEvent(ctx, key, evt); err != nil {
		return fmt.Errorf("record track event: %w", err)
	}
	return nil
}

func (r *runner) newExecutionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if r.cancelOnContextDoneEnabled {
		ctx = agent.CloneContext(ctx)
		if timeout != 0 {
			return context.WithTimeout(ctx, timeout)
		}
		return context.WithCancel(ctx)
	}
	deadline, ok := ctx.Deadline()
	if ok {
		remaining := time.Until(deadline)
		if timeout == 0 || remaining < timeout {
			timeout = remaining
		}
	}
	ctx = agent.CloneContext(ctx)
	ctx = context.WithoutCancel(ctx)
	if timeout != 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}

func (r *runner) register(key session.Key, ctx context.Context, cancel context.CancelFunc) error {
	r.runningMu.Lock()
	defer r.runningMu.Unlock()
	if r.running == nil {
		r.running = make(map[session.Key]*sessionContext)
	}
	if _, ok := r.running[key]; ok {
		return fmt.Errorf("%w: session: %v", ErrRunAlreadyExists, key)
	}
	r.running[key] = &sessionContext{ctx: ctx, cancel: cancel}
	return nil
}

func (r *runner) unregister(key session.Key) {
	r.runningMu.Lock()
	defer r.runningMu.Unlock()
	delete(r.running, key)
}

func (r *runner) recordTrackEvent(ctx context.Context, key session.Key, event aguievents.Event) error {
	return r.tracker.AppendEvent(ctx, key, event)
}
