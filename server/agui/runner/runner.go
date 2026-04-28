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
	aguitool "trpc.group/trpc-go/trpc-agent-go/server/agui/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

var (
	// ErrRunAlreadyExists is returned when a run with the same key is already running.
	ErrRunAlreadyExists = errors.New("agui: run already exists")
	// ErrRunNotFound is returned when a run key cannot be found.
	ErrRunNotFound = errors.New("agui: run not found")
	// errExplicitCancel marks a run that was terminated by the AG-UI cancel API.
	errExplicitCancel = errors.New("agui: explicit cancel")
)

const (
	toolResultInputEventAuthor = "agui.runner"
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
		appNameResolver:                        opts.AppNameResolver,
		translatorFactory:                      opts.TranslatorFactory,
		graphNodeLifecycleActivityEnabled:      opts.GraphNodeLifecycleActivityEnabled,
		graphNodeInterruptActivityEnabled:      opts.GraphNodeInterruptActivityEnabled,
		graphNodeInterruptActivityTopLevelOnly: opts.GraphNodeInterruptActivityTopLevelOnly,
		reasoningContentEnabled:                opts.ReasoningContentEnabled,
		eventSourceMetadataEnabled:             opts.EventSourceMetadataEnabled,
		userIDResolver:                         opts.UserIDResolver,
		translateCallbacks:                     opts.TranslateCallbacks,
		runAgentInputHook:                      opts.RunAgentInputHook,
		stateResolver:                          opts.StateResolver,
		runOptionResolver:                      opts.RunOptionResolver,
		tracker:                                tracker,
		running:                                make(map[session.Key]*sessionContext),
		startSpan:                              opts.StartSpan,
		flushInterval:                          opts.FlushInterval,
		postRunFinalizationTimeout:             opts.PostRunFinalizationTimeout,
		timeout:                                opts.Timeout,
		cancelOnContextDoneEnabled:             opts.CancelOnContextDoneEnabled,
		messagesSnapshotFollowEnabled:          opts.MessagesSnapshotFollowEnabled,
		messagesSnapshotFollowMaxDuration:      opts.MessagesSnapshotFollowMaxDuration,
		messagesSnapshotRunLifecycleEventsEnabled: opts.MessagesSnapshotRunLifecycleEventsEnabled,
		toolResultInputTranslationEnabled:         opts.ToolResultInputTranslationEnabled,
		streamingToolResultActivityEnabled:        opts.StreamingToolResultActivityEnabled,
	}
	return run
}

// runner is the default implementation of the Runner.
type runner struct {
	appName                                   string
	appNameResolver                           AppNameResolver
	runner                                    trunner.Runner
	translatorFactory                         TranslatorFactory
	graphNodeLifecycleActivityEnabled         bool
	graphNodeInterruptActivityEnabled         bool
	graphNodeInterruptActivityTopLevelOnly    bool
	reasoningContentEnabled                   bool
	eventSourceMetadataEnabled                bool
	userIDResolver                            UserIDResolver
	translateCallbacks                        *translator.Callbacks
	runAgentInputHook                         RunAgentInputHook
	stateResolver                             StateResolver
	runOptionResolver                         RunOptionResolver
	tracker                                   track.Tracker
	runningMu                                 sync.Mutex
	running                                   map[session.Key]*sessionContext
	startSpan                                 StartSpan
	flushInterval                             time.Duration
	postRunFinalizationTimeout                time.Duration
	timeout                                   time.Duration
	cancelOnContextDoneEnabled                bool
	messagesSnapshotFollowEnabled             bool
	messagesSnapshotFollowMaxDuration         time.Duration
	messagesSnapshotRunLifecycleEventsEnabled bool
	toolResultInputTranslationEnabled         bool
	streamingToolResultActivityEnabled        bool
}

type sessionContext struct {
	ctx    context.Context
	cancel context.CancelCauseFunc
}

type runInput struct {
	key             session.Key
	threadID        string
	runID           string
	userID          string
	messages        *runAgentMessages
	runOption       []agent.RunOption
	translator      translator.Translator
	enableTrack     bool
	span            trace.Span
	resume          *resumeInfo
	terminalEmitted bool
}

type runAgentMessages struct {
	inputMessage *model.Message
	inputID      string
	userMessage  *types.Message
	toolMessages []toolResultInputMessage
}

type toolResultInputMessage struct {
	message   model.Message
	messageID string
}

type resumeInfo struct {
	lineageID    string
	checkpointID string
	resumeMap    map[string]any
	resumeSet    bool
	resumeValue  any
}

func inputMessagesFromRunAgentInput(input *adapter.RunAgentInput) (*runAgentMessages, error) {
	if len(input.Messages) == 0 {
		return nil, errors.New("no messages provided")
	}
	lastMessage := input.Messages[len(input.Messages)-1]
	if lastMessage.Role != types.RoleUser && lastMessage.Role != types.RoleTool {
		return nil, errors.New("last message role must be user or tool")
	}
	if lastMessage.Role == types.RoleTool {
		return toolMessagesFromRunAgentInput(input.Messages)
	}
	if content, ok := lastMessage.ContentString(); ok {
		inputMessage := model.Message{
			Role:    model.RoleUser,
			Content: content,
		}
		userMessage := lastMessage
		return &runAgentMessages{
			inputMessage: &inputMessage,
			inputID:      lastMessage.ID,
			userMessage:  &userMessage,
		}, nil
	}
	contents, ok := lastMessage.ContentInputContents()
	if !ok {
		return nil, errors.New("last message content is not a string")
	}
	inputMessage, err := multimodal.UserMessageFromInputContents(contents)
	if err != nil {
		return nil, fmt.Errorf("parse user message input contents: %w", err)
	}
	userMessage := lastMessage
	userMessage.Content = contents
	return &runAgentMessages{
		inputMessage: &inputMessage,
		inputID:      lastMessage.ID,
		userMessage:  &userMessage,
	}, nil
}

func toolMessagesFromRunAgentInput(messages []types.Message) (*runAgentMessages, error) {
	start := len(messages) - 1
	for start >= 0 && messages[start].Role == types.RoleTool {
		start--
	}
	toolMessages := make([]toolResultInputMessage, 0, len(messages)-start-1)
	for _, msg := range messages[start+1:] {
		if msg.ToolCallID == "" {
			return nil, errors.New("tool message missing tool call id")
		}
		content, ok := msg.ContentString()
		if !ok {
			return nil, fmt.Errorf("tool message %q content is not a string", msg.ID)
		}
		toolMessages = append(toolMessages, toolResultInputMessage{
			message: model.Message{
				Role:     model.RoleTool,
				Content:  content,
				ToolID:   msg.ToolCallID,
				ToolName: msg.Name,
			},
			messageID: msg.ID,
		})
	}
	inputMessage := toolMessages[len(toolMessages)-1].message
	return &runAgentMessages{
		inputMessage: &inputMessage,
		inputID:      toolMessages[len(toolMessages)-1].messageID,
		toolMessages: toolMessages,
	}, nil
}

func withToolResultMessageRewriter(toolMessages []toolResultInputMessage) agent.RunOption {
	currentTurnMessages := toolResultModelMessages(toolMessages)
	return func(opts *agent.RunOptions) {
		if len(toolMessages) == 1 {
			return
		}
		userMessageRewriter := opts.UserMessageRewriter
		if userMessageRewriter == nil {
			opts.UserMessageRewriter = func(
				context.Context,
				*agent.UserMessageRewriteArgs,
			) ([]model.Message, error) {
				return append([]model.Message(nil), currentTurnMessages...), nil
			}
			return
		}
		opts.UserMessageRewriter = func(
			ctx context.Context,
			args *agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			rewritten, err := userMessageRewriter(ctx, args)
			if err != nil {
				return nil, err
			}
			return mergeToolResultRewriteMessages(rewritten, currentTurnMessages), nil
		}
	}
}

func toolResultModelMessages(toolMessages []toolResultInputMessage) []model.Message {
	modelMessages := make([]model.Message, 0, len(toolMessages))
	for _, msg := range toolMessages {
		modelMessages = append(modelMessages, msg.message)
	}
	return modelMessages
}

func mergeToolResultRewriteMessages(
	rewritten []model.Message,
	toolResults []model.Message,
) []model.Message {
	toolResultIDs := make(map[string]struct{}, len(toolResults))
	rewrittenToolResults := make(map[string]model.Message, len(toolResults))
	for _, msg := range toolResults {
		if msg.ToolID != "" {
			toolResultIDs[msg.ToolID] = struct{}{}
		}
	}
	merged := make([]model.Message, 0, len(rewritten)+len(toolResults))
	for _, msg := range rewritten {
		if msg.Role == model.RoleTool && msg.ToolID != "" {
			if _, ok := toolResultIDs[msg.ToolID]; ok {
				rewrittenToolResults[msg.ToolID] = msg
				continue
			}
		}
		merged = append(merged, msg)
	}
	for _, msg := range toolResults {
		if rewrittenMsg, ok := rewrittenToolResults[msg.ToolID]; ok {
			rewrittenMsg.Role = model.RoleTool
			rewrittenMsg.ToolID = msg.ToolID
			if msg.ToolName != "" {
				rewrittenMsg.ToolName = msg.ToolName
			}
			merged = append(merged, rewrittenMsg)
			continue
		}
		merged = append(merged, msg)
	}
	return merged
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
	messages, err := inputMessagesFromRunAgentInput(runAgentInput)
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
	appName, err := r.resolveAppName(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve app name: %w", err)
	}
	runtimeState, err := r.stateResolver(ctx, runAgentInput)
	if err != nil {
		return nil, fmt.Errorf("resolve state: %w", err)
	}
	if runtimeState != nil {
		runOption = append(runOption, agent.WithRuntimeState(runtimeState))
	}
	if len(messages.toolMessages) > 0 {
		runOption = append(runOption, withToolResultMessageRewriter(messages.toolMessages))
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
		translator.WithEventSourceMetadataEnabled(r.eventSourceMetadataEnabled),
		translator.WithStreamingToolResultActivityEnabled(r.streamingToolResultActivityEnabled),
	)
	if err != nil {
		span.End()
		return nil, fmt.Errorf("create translator: %w", err)
	}
	input := &runInput{
		key: session.Key{
			AppName:   appName,
			UserID:    userID,
			SessionID: runAgentInput.ThreadID,
		},
		threadID:    threadID,
		runID:       runID,
		userID:      userID,
		messages:    messages,
		runOption:   runOption,
		translator:  trans,
		enableTrack: r.tracker != nil,
		span:        span,
		resume:      parseResumeInfo(runOption),
	}
	events := make(chan aguievents.Event)
	ctx, cancel := r.newExecutionContext(ctx, r.timeout)
	if err := r.register(input.key, ctx, cancel); err != nil {
		cancel(nil)
		span.End()
		return nil, fmt.Errorf("register running context: %w", err)
	}
	go r.run(ctx, cancel, input.key, input, events)
	return events, nil
}

func (r *runner) run(ctx context.Context, cancel context.CancelCauseFunc, key session.Key, input *runInput, events chan<- aguievents.Event) {
	defer r.unregister(key)
	defer cancel(nil)
	defer input.span.End()
	defer close(events)
	threadID := input.threadID
	runID := input.runID
	if input.enableTrack {
		defer func() {
			if err := r.flushTrack(ctx, input.key); err != nil {
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
		if input.messages.inputMessage.Role == model.RoleUser {
			if err := r.recordUserMessage(ctx, input.key, input.messages.userMessage); err != nil {
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
	if input.messages.inputMessage.Role == model.RoleTool {
		if !r.emitToolResultEvents(ctx, events, input) {
			return
		}
	}
	if input.resume != nil && r.graphNodeInterruptActivityEnabled {
		if !r.emitEvent(ctx, events, newGraphInterruptResumeEvent(input.resume), input) {
			return
		}
	}
	ch, err := r.runner.Run(ctx, input.userID, threadID, *input.messages.inputMessage, input.runOption...)
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
			r.emitPostRunTerminalEvent(ctx, events, input)
			return
		case agentEvent, ok := <-ch:
			if !ok {
				if ctx.Err() != nil {
					r.emitPostRunTerminalEvent(ctx, events, input)
				}
				return
			}
			if !r.handleAgentEvent(ctx, events, input, agentEvent) {
				return
			}
		}
	}
}

func (r *runner) flushTrack(ctx context.Context, key session.Key) error {
	flushCtx := agent.CloneContext(ctx)
	flushCtx = context.WithoutCancel(flushCtx)
	if r.postRunFinalizationTimeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(flushCtx, r.postRunFinalizationTimeout)
		defer cancel()
		flushCtx = timeoutCtx
	}
	return r.tracker.Flush(flushCtx, key)
}

func (r *runner) emitPostRunTerminalEvent(ctx context.Context, events chan<- aguievents.Event, input *runInput) {
	finalizationErr := r.emitPostRunFinalization(ctx, events, input)
	if input.terminalEmitted {
		return
	}
	emitCtx, cancel := r.newPostRunContext(ctx)
	defer cancel()
	var terminalEvent aguievents.Event
	if finalizationErr != nil {
		terminalEvent = aguievents.NewRunErrorEvent(
			fmt.Sprintf("post-run finalization: %v", finalizationErr),
			aguievents.WithRunID(input.runID),
		)
	} else if isExplicitRunCancel(ctx) {
		terminalEvent = aguievents.NewRunFinishedEvent(input.threadID, input.runID)
	} else {
		terminalEvent = aguievents.NewRunErrorEvent(
			contextDoneMessage(ctx),
			aguievents.WithRunID(input.runID),
		)
	}
	r.emitEvent(emitCtx, events, terminalEvent, input)
}

func (r *runner) newPostRunContext(ctx context.Context) (context.Context, context.CancelFunc) {
	emitCtx := agent.CloneContext(ctx)
	emitCtx = context.WithoutCancel(emitCtx)
	if r.postRunFinalizationTimeout > 0 {
		return context.WithTimeout(emitCtx, r.postRunFinalizationTimeout)
	}
	return context.WithCancel(emitCtx)
}

func (r *runner) emitPostRunFinalization(ctx context.Context, events chan<- aguievents.Event, input *runInput) error {
	emitCtx, cancel := r.newPostRunContext(ctx)
	defer cancel()
	finalizationErr := r.emitPostRunFinalizationEvents(emitCtx, events, input)
	if finalizationErr != nil {
		log.ErrorfContext(
			emitCtx,
			"agui post-run finalization: threadID: %s, runID: %s, err: %v",
			input.threadID,
			input.runID,
			finalizationErr,
		)
	}
	return finalizationErr
}

func (r *runner) emitPostRunFinalizationEvents(ctx context.Context, events chan<- aguievents.Event, input *runInput) error {
	finalizer, ok := input.translator.(translator.PostRunFinalizingTranslator)
	if !ok {
		return nil
	}
	pending, err := finalizer.PostRunFinalizationEvents(ctx)
	for _, evt := range pending {
		if !r.emitEvent(ctx, events, evt, input) {
			return ctx.Err()
		}
	}
	return err
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

func (r *runner) emitToolResultEvents(ctx context.Context, events chan<- aguievents.Event, input *runInput) bool {
	for _, msg := range input.messages.toolMessages {
		if !r.emitToolResultEvent(ctx, events, input, msg) {
			return false
		}
	}
	return true
}

func (r *runner) emitToolResultEvent(
	ctx context.Context,
	events chan<- aguievents.Event,
	input *runInput,
	toolMessage toolResultInputMessage,
) bool {
	msg := &toolMessage.message
	if msg.ToolID == "" {
		r.emitEvent(ctx, events, aguievents.NewRunErrorEvent("tool message missing tool id",
			aguievents.WithRunID(input.runID)), input)
		return false
	}
	messageID := toolMessage.messageID
	if messageID == "" {
		messageID = msg.ToolID
	}
	if r.toolResultInputTranslationEnabled {
		event := newToolResultInputEvent(messageID, msg)
		r.attachToolResultInputSourceMetadata(ctx, input.key, event, msg.ToolID)
		return r.handleAgentEvent(ctx, events, input, event)
	}
	toolResultEvent := aguievents.NewToolCallResultEvent(messageID, msg.ToolID, msg.Content)
	return r.emitEvent(ctx, events, toolResultEvent, input)
}

// newToolResultInputEvent normalizes a tool-result input into an internal event for translation.
func newToolResultInputEvent(messageID string, msg *model.Message) *event.Event {
	rsp := &model.Response{
		ID:     messageID,
		Object: model.ObjectTypeToolResponse,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:     model.RoleTool,
				Content:  msg.Content,
				ToolID:   msg.ToolID,
				ToolName: msg.ToolName,
			},
		}},
	}
	evt := event.NewResponseEvent("", toolResultInputEventAuthor, rsp)
	evt.ID = messageID
	return evt
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

func (r *runner) resolveAppName(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
	if r.appNameResolver == nil {
		return r.appName, nil
	}
	appName, err := r.appNameResolver(ctx, input)
	if err != nil {
		return "", err
	}
	if appName != "" {
		return appName, nil
	}
	return r.appName, nil
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
	if input != nil && input.terminalEmitted {
		return false
	}
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
		runErr := aguievents.NewRunErrorEvent(fmt.Sprintf("after translate callback: %v", err),
			aguievents.WithRunID(input.runID))
		select {
		case events <- runErr:
			if input != nil {
				input.terminalEmitted = true
			}
		case <-ctx.Done():
			log.ErrorfContext(ctx, "agui emit event: context done, threadID: %s, runID: %s, err: %v",
				input.threadID, input.runID, ctx.Err())
		}
		return false
	}
	isTerminal, _ := terminalRunSignal(event)
	log.TracefContext(
		ctx,
		"agui emit event: emitted event: %v, threadID: %s, runID: %s",
		event,
		input.threadID,
		input.runID,
	)
	if input.enableTrack && r.shouldTrackEvent(event) {
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
		if input != nil && isTerminal {
			input.terminalEmitted = true
		}
		return true
	case <-ctx.Done():
		log.ErrorfContext(ctx, "agui emit event: context done, threadID: %s, runID: %s, err: %v",
			input.threadID, input.runID, ctx.Err())
		return false
	}
}

func (r *runner) shouldTrackEvent(event aguievents.Event) bool {
	if !r.streamingToolResultActivityEnabled {
		return true
	}
	return !aguitool.IsStreamingToolResultActivityEvent(event)
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

func (r *runner) newExecutionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelCauseFunc) {
	var baseCancel context.CancelFunc
	if r.cancelOnContextDoneEnabled {
		ctx = agent.CloneContext(ctx)
		if timeout != 0 {
			ctx, baseCancel = context.WithTimeout(ctx, timeout)
		} else {
			ctx, baseCancel = context.WithCancel(ctx)
		}
	} else {
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
			ctx, baseCancel = context.WithTimeout(ctx, timeout)
		} else {
			ctx, baseCancel = context.WithCancel(ctx)
		}
	}
	ctx, cancelCause := context.WithCancelCause(ctx)
	return ctx, func(cause error) {
		cancelCause(cause)
		baseCancel()
	}
}

func (r *runner) register(key session.Key, ctx context.Context, cancel context.CancelCauseFunc) error {
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

func isExplicitRunCancel(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), errExplicitCancel)
}

func contextDoneMessage(ctx context.Context) string {
	if cause := context.Cause(ctx); cause != nil {
		return cause.Error()
	}
	if err := ctx.Err(); err != nil {
		return err.Error()
	}
	return "run terminated"
}
