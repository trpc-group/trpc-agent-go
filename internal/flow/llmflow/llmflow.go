//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llmflow provides an LLM-based flow implementation.
package llmflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/calllimit"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/toolsnapshot"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonmap"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonrepair"
	"trpc.group/trpc-go/trpc-agent-go/internal/modelcontext"
	imodelrequest "trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/internal/responseusage"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryfork"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryinject"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/summaryview"
	"trpc.group/trpc-go/trpc-agent-go/internal/summarydiag"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsurface"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/internal/tracecapture"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// Timeout for event completion signaling.
	eventCompletionTimeout    = 5 * time.Second
	generatedResponseIDPrefix = "llmflow-response-"
	queuedUserAuthor          = "user"

	errMsgNoModelResponse = "no response received from model"
	errMsgNoLLMMessages   = "no messages available for LLM call"

	flowRunPanicLogFmt = log.PanicPrefix + " Flow execution panic (invocation: %s, " +
		"agent: %s): %v\n%s"

	flowRunPanicErrFmt = "flow panic: %v"

	defaultContextCompactionThresholdRatio = 0.7
	contextCompactionFallbackWindow        = 8192
	contextCompactionMinTokens             = 2000

	contextCompactionOutcomeSuccess            = "success"
	contextCompactionOutcomeNoUpdate           = "no_update"
	contextCompactionOutcomeSummaryError       = "summary_error"
	contextCompactionOutcomeRebuildUnavailable = "rebuild_unavailable"
	contextCompactionOutcomePersistenceError   = "persistence_error"
	contextCompactionOutcomePostCountError     = "post_count_error"

	// Session summary injection outcomes report whether a stored summary
	// selected for this request is still observable in the same framework
	// model.Request after the response sequence has been observed. They do
	// not describe a provider's final payload.
	summaryInjectionOutcomeBlockTextPresent = "block_text_present"
	summaryInjectionOutcomeBlockTextMissing = "block_text_missing"
	summaryInjectionOutcomeNotSelected      = "not_selected"
	summaryInjectionOutcomeLookupMiss       = "lookup_miss"
	summaryInjectionOutcomeScopeMismatch    = "scope_mismatch"
)

// InvocationHasFilteredUserTools reports whether the cached filtered tool
// snapshot for this invocation still contains any user tool.
func InvocationHasFilteredUserTools(invocation *agent.Invocation) (bool, bool) {
	return toolsnapshot.HasFilteredUserTools(invocation)
}

// InvocationFilteredTraceableUserToolNames reports filtered user tool names that have structure surfaces.
func InvocationFilteredTraceableUserToolNames(invocation *agent.Invocation) ([]string, bool) {
	return toolsnapshot.FilteredTraceableUserToolNames(invocation)
}

// Options contains configuration options for creating a Flow.
type Options struct {
	ChannelBufferSize               int // Buffer size for event channels (default: 256).
	ModelCallbacks                  *model.Callbacks
	BaseModelResolver               BaseModelResolver
	ModelSelector                   agent.ModelSelector
	SyncSummaryIntraRun             bool
	EnableContextCompaction         bool
	ContextCompactionThresholdRatio float64
	ToolActivationApplier           ToolActivationApplier
}

// ToolActivationApplier applies invocation-specific tool activation.
type ToolActivationApplier func(
	ctx context.Context,
	invocation *agent.Invocation,
	tools []tool.Tool,
	userToolNames map[string]bool,
	externalToolNames map[string]bool,
) ([]tool.Tool, map[string]bool, map[string]bool)

// ModelBaseResolution describes the base model for one LLM call.
type ModelBaseResolution struct {
	Model              model.Model
	AllowAgentSelector bool
}

// BaseModelResolver resolves the base model before one LLM call.
type BaseModelResolver func(inv *agent.Invocation) ModelBaseResolution

// Flow provides the basic flow implementation.
type Flow struct {
	requestProcessors               []flow.RequestProcessor
	responseProcessors              []flow.ResponseProcessor
	channelBufferSize               int
	modelCallbacks                  *model.Callbacks
	baseModelResolver               BaseModelResolver
	modelSelector                   agent.ModelSelector
	syncSummaryIntraRun             bool
	enableContextCompaction         bool
	contextCompactionThresholdRatio float64
	toolActivationApplier           ToolActivationApplier
}

type contextCompactionTailProcessor interface {
	SupportsContextCompactionRebuild(
		invocation *agent.Invocation,
	) bool
	RebuildRequestForContextCompaction(
		ctx context.Context,
		invocation *agent.Invocation,
		req *model.Request,
	)
}

type contextCompactionRebuildPlan struct {
	beforeContent                *model.Request
	contentProcessor             *processor.ContentRequestProcessor
	tailProcessors               []contextCompactionTailProcessor
	callLimitFinalizationMessage *model.Message
}

type summarySnapshot struct {
	exists              bool
	summary             string
	updatedAt           time.Time
	boundaryCutoff      time.Time
	boundaryLastEventID string
}

// New creates a new basic flow instance with the provided processors.
// Processors are immutable after creation.
func New(
	requestProcessors []flow.RequestProcessor,
	responseProcessors []flow.ResponseProcessor,
	opts Options,
) *Flow {
	return &Flow{
		requestProcessors:       requestProcessors,
		responseProcessors:      responseProcessors,
		channelBufferSize:       opts.ChannelBufferSize,
		modelCallbacks:          opts.ModelCallbacks,
		baseModelResolver:       opts.BaseModelResolver,
		modelSelector:           opts.ModelSelector,
		syncSummaryIntraRun:     opts.SyncSummaryIntraRun,
		enableContextCompaction: opts.EnableContextCompaction,
		toolActivationApplier:   opts.ToolActivationApplier,
		contextCompactionThresholdRatio: normalizeContextCompactionThresholdRatio(
			opts.ContextCompactionThresholdRatio,
		),
	}
}

// Run executes the flow in a loop until completion.
func (f *Flow) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, f.channelBufferSize) // Configurable buffered channel for events.

	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		ctx, runSpan, runStarted := startLatencySpan(
			ctx,
			invocation,
			latencySpanFlowRun,
			latencyInvocationAttrs(invocation)...,
		)
		var runErr error
		defer func() {
			finishLatencySpan(runSpan, runStarted, runErr)
		}()
		defer close(eventChan)
		defer steer.Close(invocation)
		defer recoverFlowRunPanic(ctx, invocation, eventChan)

		// Mark the invocation so the runner skips redundant async
		// summary enqueue when sync intra-run summary handles it.
		if f.syncSummaryIntraRun && invocation != nil {
			invocation.SetState(
				agent.SyncSummaryIntraRunStateKey, true,
			)
		}

		// Optionally resume from pending tool calls before starting a new
		// LLM cycle. This covers scenarios where the previous run stopped
		// after an assistant tool_call response but before tools executed.
		f.maybeResumePendingToolCalls(ctx, invocation, eventChan)

		firstIteration := true
		for {
			// emit start event and wait for completion notice.
			if err := f.emitStartEventAndWait(ctx, invocation, eventChan); err != nil {
				runErr = err
				return
			}

			// Run sync intra-run summary only between iterations.
			if !firstIteration {
				f.maybeSyncSummaryIntraRun(ctx, invocation)
			}
			firstIteration = false

			if err := f.maybeConsumeQueuedUserMessages(
				ctx,
				invocation,
				eventChan,
			); err != nil {
				runErr = err
				return
			}

			// Run one step (one LLM call cycle).
			lastEvent, err := f.runOneStep(ctx, invocation, eventChan)
			if err != nil {
				runErr = err
				steer.Close(invocation)
				// Treat context cancellation as graceful termination (common in streaming
				// pipelines where the client closes the stream after final event).
				if errors.Is(err, context.Canceled) {
					log.DebugfContext(
						ctx,
						"Flow context canceled for agent %s; exiting "+
							"without error",
						invocation.AgentName,
					)
					return
				}
				var errorEvent *event.Event
				if _, ok := agent.AsStopError(err); ok {
					errorEvent = event.NewErrorEvent(
						invocation.InvocationID,
						invocation.AgentName,
						agent.ErrorTypeStopAgentError,
						err.Error(),
					)
					log.ErrorfContext(
						ctx,
						"Flow step stopped for agent %s: %v",
						invocation.AgentName,
						err,
					)
				} else {
					// Send error event through channel instead of just logging.
					errorEvent = event.NewErrorEvent(
						invocation.InvocationID,
						invocation.AgentName,
						model.ErrorTypeFlowError,
						err.Error(),
					)
					log.ErrorfContext(
						ctx,
						"Flow step failed for agent %s: %v",
						invocation.AgentName,
						err,
					)
				}

				agent.EmitEvent(ctx, invocation, eventChan, errorEvent)
				return
			}

			// Exit conditions.
			// If no events were produced in this step, treat as terminal to avoid busy loop.
			// Also break when EndInvocation is set or a final response is observed.
			if lastEvent == nil || invocation.EndInvocation || lastEvent.IsFinalResponse() {
				steer.Close(invocation)
				break
			}
		}
	}(runCtx)

	return eventChan, nil
}

func recoverFlowRunPanic(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	recovered := recover()
	if recovered == nil {
		return
	}

	stack := debug.Stack()
	log.ErrorfContext(
		ctx,
		flowRunPanicLogFmt,
		flowInvocationID(invocation),
		flowAgentName(invocation),
		recovered,
		string(stack),
	)

	errorEvent := event.NewErrorEvent(
		flowInvocationID(invocation),
		flowAgentName(invocation),
		model.ErrorTypeFlowError,
		fmt.Sprintf(flowRunPanicErrFmt, recovered),
	)
	agent.EmitEvent(ctx, invocation, eventChan, errorEvent)
}

func flowInvocationID(invocation *agent.Invocation) string {
	if invocation == nil {
		return ""
	}
	return invocation.InvocationID
}

func flowAgentName(invocation *agent.Invocation) string {
	if invocation == nil {
		return ""
	}
	return invocation.AgentName
}

func traceSnapshotFromMessages(messages []model.Message) *atrace.Snapshot {
	if len(messages) == 0 {
		return nil
	}
	bytes, err := json.Marshal(messages)
	if err != nil {
		return nil
	}
	return &atrace.Snapshot{Text: string(bytes)}
}

func executionTraceAppliedSurfaceIDs(invocation *agent.Invocation) []string {
	if invocation == nil || invocation.Agent == nil {
		return nil
	}
	reporter, ok := invocation.Agent.(interface {
		ExecutionTraceAppliedSurfaceIDs(inv *agent.Invocation) []string
	})
	if !ok {
		return nil
	}
	return reporter.ExecutionTraceAppliedSurfaceIDs(invocation)
}

func (f *Flow) maybeConsumeQueuedUserMessages(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) (err error) {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanQueuedMessages,
	)
	var drained int
	defer func() {
		if started {
			span.SetAttributes(attribute.Int("llmflow.queued_messages", drained))
		}
		finishLatencySpan(span, started, err)
	}()
	if !steer.IsAttached(invocation) {
		return nil
	}

	messages := steer.Drain(invocation)
	drained = len(messages)
	if len(messages) == 0 {
		return nil
	}

	for _, message := range messages {
		invocation.Message = message

		evt := event.NewResponseEvent(
			invocation.InvocationID,
			queuedUserAuthor,
			&model.Response{
				Done: false,
				Choices: []model.Choice{{
					Index:   0,
					Message: message,
				}},
			},
			event.WithExtension(
				steer.ExtensionKeyQueuedUserMessage,
				steer.QueuedUserMessageMetadata{
					Status: steer.QueuedUserMessageStatusConsumed,
				},
			),
		)
		evt.RequiresCompletion = true

		if err := agent.EmitEvent(
			ctx,
			invocation,
			eventChan,
			evt,
		); err != nil {
			return err
		}

		completionID := agent.GetAppendEventNoticeKey(evt.ID)
		err := invocation.AddNoticeChannelAndWait(
			ctx,
			completionID,
			flowEventWaitTimeout(ctx),
		)
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if err != nil {
			log.WarnfContext(
				ctx,
				"Wait for queued user message persistence failed: %v",
				err,
			)
		}
	}

	return nil
}

func flowEventWaitTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return eventCompletionTimeout
}

// maybeResumePendingToolCalls inspects the latest session events and, when
// RunOptions.Resume is enabled, executes any pending tool calls before the
// next LLM request. A pending tool call is defined as the latest persisted
// event being an assistant response that contains tool calls but no tool
// results after it.
func (f *Flow) maybeResumePendingToolCalls(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanResumeTools,
	)
	var resumed bool
	defer func() {
		if started {
			span.SetAttributes(attribute.Bool("llmflow.resume_tools.resumed", resumed))
		}
		finishLatencySpan(span, started, nil)
	}()
	if invocation == nil || !invocation.RunOptions.Resume {
		return
	}
	if invocation.Session == nil {
		return
	}

	invocation.Session.EventMu.RLock()
	events := invocation.Session.Events
	var lastResp *model.Response
	if len(events) > 0 {
		last := events[len(events)-1]
		if last.Response != nil && !last.IsPartial &&
			last.IsValidContent() && last.Response.IsToolCallResponse() {
			lastResp = last.Response
		}
	}
	invocation.Session.EventMu.RUnlock()

	if lastResp == nil {
		return
	}
	resumed = true

	req := &model.Request{
		Tools: make(map[string]tool.Tool),
	}
	f.populateRequestTools(ctx, invocation, req)

	for _, rp := range f.responseProcessors {
		if toolRP, ok := rp.(*processor.FunctionCallResponseProcessor); ok {
			toolRP.ProcessResponse(ctx, invocation, req, lastResp, eventChan)
			break
		}
	}
}

func (f *Flow) maybeSyncSummaryIntraRun(
	ctx context.Context,
	invocation *agent.Invocation,
) {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanSyncSummary,
	)
	var err error
	defer func() {
		finishLatencySpan(span, started, err)
	}()
	if !f.syncSummaryIntraRun || invocation == nil || invocation.Session == nil ||
		invocation.SessionService == nil {
		return
	}

	summaryCtx := ctx
	if parentRequest, ok := summaryfork.Request(invocation); ok {
		summaryCtx = summary.ContextWithCacheSafeForkRequest(
			summaryCtx,
			parentRequest,
		)
	}
	if view, ok := summaryview.Snapshot(invocation); ok {
		summaryCtx = summaryview.ContextWithView(summaryCtx, view)
	}

	err = invocation.SessionService.CreateSessionSummary(
		summaryCtx,
		invocation.Session,
		invocation.GetEventFilterKey(),
		false,
	)
	if err != nil {
		log.DebugfContext(
			ctx,
			"Intra-run summary skipped or failed for agent %s: %v",
			invocation.AgentName,
			err,
		)
	}
}

func (f *Flow) emitStartEventAndWait(ctx context.Context, invocation *agent.Invocation,
	eventChan chan<- *event.Event) error {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanEmitStartWait,
	)
	var err error
	defer func() {
		finishLatencySpan(span, started, err)
	}()

	invocationID, agentName := "", ""
	if invocation != nil {
		invocationID = invocation.InvocationID
		agentName = invocation.AgentName
	}
	startEvent := event.New(invocationID, agentName)
	startEvent.RequiresCompletion = true
	agent.EmitEvent(ctx, invocation, eventChan, startEvent)

	// Wait for completion notice.
	// Ensure that the events of the previous agent or the previous step have been synchronized to the session.
	completionID := agent.GetAppendEventNoticeKey(startEvent.ID)
	err = invocation.AddNoticeChannelAndWait(ctx, completionID, eventCompletionTimeout)
	if errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func (f *Flow) selectModelForStep(
	ctx context.Context,
	invocation *agent.Invocation,
) (selectedModel model.Model, err error) {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanSelectModel,
	)
	defer func() {
		if selectedModel != nil && started {
			span.SetAttributes(
				attribute.String(
					"llmflow.model",
					selectedModel.Info().Name,
				),
			)
		}
		finishLatencySpan(span, started, err)
	}()

	if invocation == nil {
		return nil, nil
	}
	resolution := ModelBaseResolution{
		Model:              invocation.Model,
		AllowAgentSelector: true,
	}
	if f.baseModelResolver != nil {
		resolution = f.baseModelResolver(invocation)
	}
	baseModel := resolution.Model
	selector := invocation.RunOptions.ModelSelector
	if selector == nil && resolution.AllowAgentSelector {
		selector = f.modelSelector
	}
	if selector == nil {
		return baseModel, nil
	}
	originalModel := invocation.Model
	invocation.Model = baseModel
	selected, err := runModelSelector(ctx, selector, invocation)
	invocation.Model = originalModel
	if err != nil {
		return baseModel, fmt.Errorf("model selector failed: %w", err)
	}
	if selected == nil {
		return baseModel, nil
	}
	return selected, nil
}

func runModelSelector(
	ctx context.Context,
	selector agent.ModelSelector,
	invocation *agent.Invocation,
) (selected model.Model, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf(log.PanicPrefix+" model selector panic: %v\n%s", r, debug.Stack())
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return selector(ctx, invocation)
}

// runOneStep executes one LLM call cycle. Despite the legacy name, this is
// not a structural execution-trace Step; every cycle updates the Step owned by
// the surrounding agent run.
// Returns the last event generated, or nil if no events.
func (f *Flow) runOneStep(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
) (lastEvent *event.Event, err error) {
	ctx, stepSpan, stepStarted := startLatencySpan(
		ctx,
		invocation,
		latencySpanRunOneStep,
		latencyInvocationAttrs(invocation)...,
	)
	defer func() {
		if stepStarted && lastEvent != nil {
			stepSpan.SetAttributes(
				attribute.String("llmflow.last_event.object", lastEvent.Object),
				attribute.Bool("llmflow.last_event.final", lastEvent.IsFinalResponse()),
			)
		}
		finishLatencySpan(stepSpan, stepStarted, err)
	}()
	defer func() {
		if calllimit.Active(invocation) {
			invocation.EndInvocation = true
			calllimit.Finish(invocation)
		}
	}()
	// Initialize empty LLM request.
	llmRequest := &model.Request{
		Tools: make(map[string]tool.Tool), // Initialize tools map
	}
	callModel, err := f.selectModelForStep(ctx, invocation)
	if err != nil {
		return nil, err
	}
	if invocation != nil {
		originalModel := invocation.Model
		invocation.Model = callModel
		defer func() {
			invocation.Model = originalModel
		}()
	}
	// 1. Preprocess (prepare request).
	rebuildPlan := f.preprocess(ctx, invocation, llmRequest, eventChan)
	if invocation.EndInvocation {
		return lastEvent, nil
	}
	if instruction, ok := calllimit.PreviewForLLM(
		invocation,
		invocation.MaxLLMCalls,
	); ok && rebuildPlan != nil {
		message := model.NewUserMessage(instruction)
		rebuildPlan.callLimitFinalizationMessage = &message
	}
	llmRequest = f.maybeCompactContextBeforeLLM(
		ctx,
		invocation,
		eventChan,
		llmRequest,
		rebuildPlan,
	)
	if invocation.EndInvocation {
		return lastEvent, nil
	}
	observabilityInvocation := observabilityInvocationForModel(invocation, callModel)
	var span oteltrace.Span
	var modelName string
	if callModel != nil {
		modelName = callModel.Info().Name
	}
	_, span, startedSpan := itrace.StartSpan(ctx, invocation, itelemetry.NewChatSpanName(modelName))
	if startedSpan {
		defer span.End()
	}
	// 2. Call LLM (get response sequence).
	ctx, responseSeq, modelCalled, err := f.callLLM(ctx, invocation, llmRequest, callModel)
	if err != nil {
		return nil, err
	}
	var lastCompleteUsage *model.Usage
	if modelCalled && invocation != nil && invocation.RunOptions.ExecutionTraceEnabled {
		modelResponseSeq := responseSeq
		responseSeq = func(yield func(*model.Response) bool) {
			modelResponseSeq(func(response *model.Response) bool {
				if response != nil && !response.IsPartial && response.Usage != nil {
					usage := *response.Usage
					lastCompleteUsage = &usage
				}
				return yield(response)
			})
		}
	}
	// 3. Process streaming responses.
	lastEvent, err = f.processStreamingResponses(
		ctx,
		invocation,
		observabilityInvocation,
		llmRequest,
		responseSeq,
		eventChan,
		span,
		startedSpan,
	)
	if lastCompleteUsage != nil {
		tracecapture.AddInvocationStepUsage(
			agent.NewInvocationContext(ctx, invocation),
			lastCompleteUsage,
		)
	}
	return lastEvent, err
}

// processStreamingResponses handles the streaming response processing logic.
func (f *Flow) processStreamingResponses(
	ctx context.Context,
	invocation *agent.Invocation,
	observabilityInvocation *agent.Invocation,
	llmRequest *model.Request,
	responseSeq model.Seq[*model.Response],
	eventChan chan<- *event.Event,
	span oteltrace.Span,
	startedSpan bool,
) (lastEvent *event.Event, err error) {
	ctx, streamSpan, streamStarted := startLatencySpan(
		ctx,
		invocation,
		latencySpanStreamResponses,
		latencyRequestAttrs(llmRequest)...,
	)
	processor := newStreamingResponseProcessor(
		f,
		ctx,
		invocation,
		observabilityInvocation,
		llmRequest,
		eventChan,
		span,
		startedSpan,
		&err,
	)
	defer func() {
		if streamStarted {
			streamSpan.SetAttributes(
				attribute.Int(
					"llmflow.response.count",
					processor.responseCount,
				),
				attribute.Int(
					"llmflow.response.partial_count",
					processor.partialResponseCount,
				),
				attribute.Int(
					"llmflow.response.terminal_count",
					processor.terminalResponseCount,
				),
				attribute.Int(
					"llmflow.response.error_count",
					processor.errorResponseCount,
				),
				attribute.Int(
					"llmflow.response.tool_count",
					processor.toolResponseCount,
				),
				attribute.Int(
					"llmflow.response.detail_span_count",
					processor.detailSpanCount,
				),
			)
		}
		finishLatencySpan(streamSpan, streamStarted, err)
	}()
	if processor.tracker != nil {
		defer processor.tracker.RecordMetrics()()
	}
	responseSeq(func(response *model.Response) bool {
		return processor.process(response)
	})
	if err != nil {
		return nil, err
	}
	return processor.lastEvent, nil
}

type streamingResponseProcessor struct {
	flow                    *Flow
	ctx                     context.Context
	invocation              *agent.Invocation
	observabilityInvocation *agent.Invocation
	currentInvocation       *agent.Invocation
	llmRequest              *model.Request
	eventChan               chan<- *event.Event
	span                    oteltrace.Span
	startedSpan             bool
	chatTraceState          itelemetry.ChatTraceState
	tracker                 *itelemetry.ChatMetricsTracker
	timingInfo              *model.TimingInfo
	partialUsageState       responseusage.PartialState
	lastEvent               *event.Event
	err                     *error
	responseCount           int
	partialResponseCount    int
	terminalResponseCount   int
	errorResponseCount      int
	toolResponseCount       int
	detailSpanCount         int
}

func newStreamingResponseProcessor(
	flow *Flow,
	ctx context.Context,
	invocation *agent.Invocation,
	observabilityInvocation *agent.Invocation,
	llmRequest *model.Request,
	eventChan chan<- *event.Event,
	span oteltrace.Span,
	startedSpan bool,
	err *error,
) *streamingResponseProcessor {
	currentInvocation := invocationFromContextOrDefault(ctx, invocation)
	metricsInvocation := observabilityInvocation
	if metricsInvocation == nil {
		metricsInvocation = invocation
	}
	if metricsInvocation == nil {
		metricsInvocation = currentInvocation
	}
	processor := &streamingResponseProcessor{
		flow:                    flow,
		ctx:                     ctx,
		invocation:              invocation,
		observabilityInvocation: observabilityInvocation,
		currentInvocation:       currentInvocation,
		llmRequest:              llmRequest,
		eventChan:               eventChan,
		span:                    span,
		startedSpan:             startedSpan,
		err:                     err,
	}
	if metricsInvocation != nil {
		processor.timingInfo = responseUsageTimingInfo(currentInvocation)
		processor.tracker = itelemetry.NewChatMetricsTracker(
			ctx,
			metricsInvocation,
			llmRequest,
			processor.timingInfo,
			nil,
			err,
		)
	}
	return processor
}

func (p *streamingResponseProcessor) process(
	response *model.Response,
) bool {
	p.recordResponseStats(response)
	traceDetails := latencyTraceResponseDetails(response)
	responseCtx := p.ctx
	var responseSpan oteltrace.Span
	responseStarted := false
	if traceDetails {
		p.detailSpanCount++
		responseCtx, responseSpan, responseStarted = startLatencySpan(
			p.ctx,
			p.invocation,
			latencySpanProcessResponse,
			latencyResponseAttrs(response)...,
		)
	}
	responseErr := error(nil)
	defer func() {
		finishLatencySpan(responseSpan, responseStarted, responseErr)
	}()
	p.ctx = responseCtx
	p.currentInvocation = invocationFromContextOrDefault(
		p.ctx,
		p.currentInvocation,
	)
	p.updateMetricsState()
	trackModelResponseTelemetry(response, p.tracker)
	callbackTimingAttachment := responseusage.AttachTimingForCallback(
		response,
		p.timingInfo,
		&p.partialUsageState,
	)
	eventInvocation := p.eventInvocation()
	updatedCtx, customResp, cbErr := p.flow.handleAfterModelCallbacks(
		p.ctx,
		eventInvocation,
		p.currentInvocation,
		p.llmRequest,
		response,
		p.eventChan,
		traceDetails,
	)
	if cbErr != nil {
		*p.err = cbErr
		responseErr = cbErr
		return false
	}
	p.ctx = updatedCtx
	p.currentInvocation = invocationFromContextOrDefault(
		p.ctx,
		p.currentInvocation,
	)
	p.updateMetricsState()
	response = p.applyCallbackResponse(response, customResp, callbackTimingAttachment)
	responseusage.AttachTiming(response, p.timingInfo, &p.partialUsageState)
	p.repairToolCallArguments(response)
	p.repairToolCallTextAndStats(response)
	if err := validateCompletedToolCallNames(response); err != nil {
		*p.err = err
		responseErr = err
		return false
	}
	if p.shouldBufferToolCallTextPartial(response) {
		if err := agent.CheckContextCancelled(p.ctx); err != nil {
			*p.err = err
			responseErr = err
			return false
		}
		if responseStarted && response != nil {
			responseSpan.SetAttributes(
				latencyResponseAttrs(response)...,
			)
		}
		return true
	}
	llmResponseEvent := p.emitLLMResponse(
		eventInvocation,
		response,
		traceDetails,
	)
	p.lastEvent = llmResponseEvent
	if p.tracker != nil {
		p.tracker.SetLastEvent(p.lastEvent)
	}
	if err := agent.CheckContextCancelled(p.ctx); err != nil {
		*p.err = err
		responseErr = err
		return false
	}
	p.flow.postprocessWithLatencySpans(
		p.ctx,
		eventInvocation,
		p.llmRequest,
		response,
		p.eventChan,
		traceDetails,
	)
	if err := agent.CheckContextCancelled(p.ctx); err != nil {
		*p.err = err
		responseErr = err
		return false
	}
	p.traceChat(eventInvocation, response, llmResponseEvent)
	if responseStarted && response != nil {
		responseSpan.SetAttributes(latencyResponseAttrs(response)...)
	}
	return true
}

func validateCompletedToolCallNames(response *model.Response) error {
	if response == nil || response.IsPartial {
		return nil
	}
	for choiceIndex, choice := range response.Choices {
		messages := []struct {
			location  string
			toolCalls []model.ToolCall
		}{
			{location: "message", toolCalls: choice.Message.ToolCalls},
			{location: "delta", toolCalls: choice.Delta.ToolCalls},
		}
		for _, message := range messages {
			for toolCallIndex, toolCall := range message.toolCalls {
				if strings.TrimSpace(toolCall.Function.Name) != "" {
					continue
				}
				return fmt.Errorf(
					"invalid model response: tool call function name is empty: response_id=%q choice=%d location=%s tool_call=%d id=%q",
					response.ID,
					choiceIndex,
					message.location,
					toolCallIndex,
					toolCall.ID,
				)
			}
		}
	}
	return nil
}

func (p *streamingResponseProcessor) recordResponseStats(response *model.Response) {
	p.responseCount++
	if response == nil {
		return
	}
	if response.IsPartial {
		p.partialResponseCount++
	}
	if response.Done {
		p.terminalResponseCount++
	}
	if response.Error != nil {
		p.errorResponseCount++
	}
	if response.IsToolCallResponse() || response.IsToolResultResponse() {
		p.toolResponseCount++
	}
}

func (p *streamingResponseProcessor) updateMetricsState() {
	p.timingInfo = responseUsageTimingInfo(p.currentInvocation)
	if p.tracker == nil {
		return
	}
	p.tracker.SetInvocationState(
		p.currentInvocation,
		p.timingInfo,
	)
}

func (p *streamingResponseProcessor) eventInvocation() *agent.Invocation {
	if p.invocation != nil {
		return p.invocation
	}
	return p.currentInvocation
}

func (p *streamingResponseProcessor) applyCallbackResponse(
	response *model.Response,
	customResp *model.Response,
	callbackTimingAttachment responseusage.TimingAttachment,
) *model.Response {
	if customResp != nil {
		callbackTimingAttachment.Restore()
		return customResp
	}
	callbackTimingAttachment.RestoreIfTimingInfoChanged(p.timingInfo)
	return response
}

func (p *streamingResponseProcessor) repairToolCallArguments(
	response *model.Response,
) {
	if p.currentInvocation == nil {
		return
	}
	if !jsonrepair.IsToolCallArgumentsJSONRepairEnabled(p.currentInvocation) {
		return
	}
	jsonrepair.RepairResponseToolCallArgumentsInPlace(p.ctx, response)
}

func (p *streamingResponseProcessor) repairToolCallTextAndStats(
	response *model.Response,
) {
	wasToolResponse := response != nil &&
		(response.IsToolCallResponse() || response.IsToolResultResponse())
	if p.repairToolCallText(response) && !wasToolResponse &&
		response.IsToolCallResponse() {
		p.toolResponseCount++
	}
}

func (p *streamingResponseProcessor) repairToolCallText(
	response *model.Response,
) bool {
	if p.currentInvocation == nil {
		return false
	}
	if !isToolCallTextRepairEnabled(p.currentInvocation) {
		return false
	}
	return repairResponseToolCallTextInPlace(p.ctx, p.llmRequest, response)
}

func (p *streamingResponseProcessor) shouldBufferToolCallTextPartial(
	response *model.Response,
) bool {
	return response != nil && response.IsPartial &&
		p.currentInvocation != nil &&
		isToolCallTextRepairEnabled(p.currentInvocation) &&
		p.llmRequest != nil && len(p.llmRequest.Tools) > 0 &&
		responseMayContainTextToolCall(response)
}

func responseMayContainTextToolCall(response *model.Response) bool {
	if response == nil {
		return false
	}
	for i := range response.Choices {
		msg := &response.Choices[i].Message
		if msg.Role != model.RoleAssistant {
			continue
		}
		text, ok := repairableMessageText(msg)
		if !ok {
			continue
		}
		if strings.Contains(text, textToolCallOpenTag) {
			return true
		}
		for prefixLen := 1; prefixLen < len(textToolCallOpenTag); prefixLen++ {
			if strings.HasSuffix(text, textToolCallOpenTag[:prefixLen]) {
				return true
			}
		}
	}
	return false
}

func (p *streamingResponseProcessor) emitLLMResponse(
	eventInvocation *agent.Invocation,
	response *model.Response,
	traceDetails bool,
) *event.Event {
	llmResponseEvent := p.flow.createLLMResponseEvent(
		eventInvocation,
		p.currentInvocation,
		response,
		p.llmRequest,
	)
	emitCtx := p.ctx
	var emitSpan oteltrace.Span
	emitStarted := false
	if traceDetails {
		emitCtx, emitSpan, emitStarted = startLatencySpan(
			p.ctx,
			eventInvocation,
			latencySpanEmitResponse,
			latencyResponseAttrs(response)...,
		)
	}
	agent.EmitEvent(emitCtx, eventInvocation, p.eventChan, llmResponseEvent)
	finishLatencySpan(emitSpan, emitStarted, nil)
	return llmResponseEvent
}

func (p *streamingResponseProcessor) traceChat(
	eventInvocation *agent.Invocation,
	response *model.Response,
	llmResponseEvent *event.Event,
) {
	if !p.startedSpan {
		return
	}
	var ttfb time.Duration
	if p.tracker != nil {
		ttfb = p.tracker.FirstTokenTimeDuration()
	}
	p.chatTraceState.TraceChat(p.span, &itelemetry.TraceChatAttributes{
		Invocation: observabilityInvocationForCurrent(
			eventInvocation,
			p.observabilityInvocation,
		),
		Request:          p.llmRequest,
		Response:         response,
		EventID:          llmResponseEvent.ID,
		TimeToFirstToken: ttfb,
	})
}

// handleAfterModelCallbacks processes after model callbacks.
func (f *Flow) handleAfterModelCallbacks(
	ctx context.Context,
	eventInvocation *agent.Invocation,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	response *model.Response,
	eventChan chan<- *event.Event,
	traceDetails bool,
) (context.Context, *model.Response, error) {
	if !traceDetails {
		updatedCtx, customResp, err := f.runAfterModelCallbacks(
			ctx,
			invocation,
			llmRequest,
			response,
		)
		return f.handleAfterModelCallbackResult(
			updatedCtx,
			eventInvocation,
			eventChan,
			customResp,
			err,
		)
	}
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanAfterModel,
		latencyResponseAttrs(response)...,
	)
	var err error
	var customResp *model.Response
	defer func() {
		if started {
			span.SetAttributes(
				attribute.Bool("llmflow.callback.custom_response", customResp != nil),
			)
		}
		finishLatencySpan(span, started, err)
	}()
	ctx, customResp, err = f.runAfterModelCallbacks(
		ctx,
		invocation,
		llmRequest,
		response,
	)
	return f.handleAfterModelCallbackResult(
		ctx,
		eventInvocation,
		eventChan,
		customResp,
		err,
	)
}

func (f *Flow) handleAfterModelCallbackResult(
	ctx context.Context,
	eventInvocation *agent.Invocation,
	eventChan chan<- *event.Event,
	customResp *model.Response,
	err error,
) (context.Context, *model.Response, error) {
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return ctx, nil, err
		}
		log.ErrorfContext(
			ctx,
			"After model callback failed for agent %s: %v",
			flowAgentName(eventInvocation),
			err,
		)
		agent.EmitEvent(ctx, eventInvocation, eventChan, event.NewErrorEvent(
			flowInvocationID(eventInvocation),
			flowAgentName(eventInvocation),
			model.ErrorTypeFlowError,
			err.Error(),
		))
		return ctx, nil, err
	}
	return ctx, customResp, nil
}

// createLLMResponseEvent creates a new LLM response event.
func (f *Flow) createLLMResponseEvent(
	eventInvocation *agent.Invocation,
	optionsInvocation *agent.Invocation,
	response *model.Response,
	llmRequest *model.Request,
) *event.Event {
	invocationID, agentName := "", ""
	if eventInvocation != nil {
		invocationID = eventInvocation.InvocationID
		agentName = eventInvocation.AgentName
	}
	llmResponseEvent := event.New(
		invocationID,
		agentName,
		event.WithResponse(response),
	)
	applyPartialEventMetadataOverrides(
		llmResponseEvent,
		response,
		optionsInvocation,
	)
	if len(response.Choices) > 0 && len(response.Choices[0].Message.ToolCalls) > 0 {
		llmResponseEvent.LongRunningToolIDs = collectLongRunningToolIDs(response.Choices[0].Message.ToolCalls, llmRequest.Tools)
	}
	return llmResponseEvent
}

func invocationFromContextOrDefault(
	ctx context.Context,
	invocation *agent.Invocation,
) *agent.Invocation {
	if updatedInvocation, ok := agent.InvocationFromContext(ctx); ok &&
		updatedInvocation != nil {
		return updatedInvocation
	}
	return invocation
}

func observabilityInvocationForModel(
	invocation *agent.Invocation,
	callModel model.Model,
) *agent.Invocation {
	if invocation == nil {
		return nil
	}
	return newObservabilityInvocation(
		invocation,
		invocation.Session,
		callModel,
	)
}

func observabilityInvocationForCurrent(
	current *agent.Invocation,
	base *agent.Invocation,
) *agent.Invocation {
	if base == nil {
		return current
	}
	if current == nil || current.Session == nil ||
		current.Session == base.Session {
		return base
	}
	return newObservabilityInvocation(base, current.Session, base.Model)
}

// newObservabilityInvocation intentionally excludes invocation state: metrics
// and tracing consume only identity, session, and model metadata, while state
// can contain large model-visible history snapshots.
func newObservabilityInvocation(
	base *agent.Invocation,
	sess *session.Session,
	callModel model.Model,
) *agent.Invocation {
	return &agent.Invocation{
		AgentName:    base.AgentName,
		InvocationID: base.InvocationID,
		Session:      sess,
		Model:        callModel,
	}
}

func trackModelResponseTelemetry(
	response *model.Response,
	tracker *itelemetry.ChatMetricsTracker,
) {
	if tracker == nil || response == nil {
		return
	}
	tracker.TrackResponse(response)
}

func responseUsageTimingInfo(invocation *agent.Invocation) *model.TimingInfo {
	if invocation == nil || invocation.RunOptions.DisableResponseUsageTracking {
		return nil
	}
	return invocation.GetOrCreateTimingInfo()
}

func applyPartialEventMetadataOverrides(
	ev *event.Event,
	response *model.Response,
	invocation *agent.Invocation,
) {
	if ev == nil || response == nil || !response.IsPartial || invocation == nil {
		return
	}
	if invocation.RunOptions.DisablePartialEventIDs {
		ev.ID = ""
	}
	if invocation.RunOptions.DisablePartialEventTimestamps {
		ev.Timestamp = response.Timestamp
	}
}

func collectLongRunningToolIDs(ToolCalls []model.ToolCall, tools map[string]tool.Tool) map[string]struct{} {
	longRunningToolIDs := make(map[string]struct{})
	for _, toolCall := range ToolCalls {
		t, ok := tools[toolCall.Function.Name]
		if !ok {
			continue
		}
		caller, ok := itool.ResolveDeclaration(t).(function.LongRunner)
		if !ok {
			continue
		}
		if caller.LongRunning() {
			longRunningToolIDs[toolCall.ID] = struct{}{}
		}
	}
	return longRunningToolIDs
}

func (f *Flow) runAfterModelCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	response *model.Response,
) (context.Context, *model.Response, error) {
	var (
		override bool
		err      error
	)
	if invocation != nil && invocation.Plugins != nil {
		callbacks := invocation.Plugins.ModelCallbacks()
		ctx, response, override, err = runAfterModelCallbackSet(
			ctx,
			callbacks,
			req,
			response,
		)
		if err != nil {
			return ctx, nil, err
		}
		if override {
			return ctx, response, nil
		}
	}

	ctx, response, _, err = runAfterModelCallbackSet(
		ctx,
		f.modelCallbacks,
		req,
		response,
	)
	return ctx, response, err
}

func runAfterModelCallbackSet(
	ctx context.Context,
	callbacks *model.Callbacks,
	req *model.Request,
	response *model.Response,
) (context.Context, *model.Response, bool, error) {
	if callbacks == nil {
		return ctx, response, false, nil
	}

	var modelErr error
	if response != nil && response.Error != nil {
		modelErr = fmt.Errorf(
			"%s: %s",
			response.Error.Type,
			response.Error.Message,
		)
	}

	result, err := callbacks.RunAfterModel(ctx, &model.AfterModelArgs{
		Request:  req,
		Response: response,
		Error:    modelErr,
	})
	if err != nil {
		return ctx, nil, false, err
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		return ctx, result.CustomResponse, true, nil
	}
	return ctx, response, false, nil
}

// preprocess handles pre-LLM call preparation using request processors.
func (f *Flow) preprocess(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	eventChan chan<- *event.Event,
) *contextCompactionRebuildPlan {
	var rebuildPlan *contextCompactionRebuildPlan
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanPreprocess,
		latencyRequestAttrs(llmRequest)...,
	)
	defer func() {
		if started {
			span.SetAttributes(latencyRequestAttrs(llmRequest)...)
		}
		finishLatencySpan(span, started, nil)
	}()

	f.populateRequestTools(ctx, invocation, llmRequest)
	// Run request processors - they send events directly to the channel.
	for _, requestProcessor := range f.requestProcessors {
		if rebuildPlan == nil {
			contentProcessor, ok := requestProcessor.(*processor.ContentRequestProcessor)
			if ok &&
				contentProcessor.AddSessionSummary &&
				contentProcessor.TimelineFilterMode == processor.TimelineFilterAll {
				rebuildPlan = &contextCompactionRebuildPlan{
					beforeContent:    cloneRequestForContextCompaction(llmRequest),
					contentProcessor: contentProcessor,
				}
			}
		} else {
			tailProcessor, ok := requestProcessor.(contextCompactionTailProcessor)
			if !ok ||
				!tailProcessor.SupportsContextCompactionRebuild(invocation) {
				rebuildPlan = nil
			} else {
				rebuildPlan.tailProcessors = append(rebuildPlan.tailProcessors, tailProcessor)
			}
		}
		stageCtx, stageSpan, stageStarted := startLatencySpan(
			ctx,
			invocation,
			latencyProcessorStageSpanName(
				latencySpanPreprocessStage,
				requestProcessor,
			),
			attribute.String(
				"llmflow.preprocess.stage",
				latencyProcessorName(requestProcessor),
			),
		)
		requestProcessor.ProcessRequest(
			stageCtx,
			invocation,
			llmRequest,
			eventChan,
		)
		if stageStarted {
			stageSpan.SetAttributes(latencyRequestAttrs(llmRequest)...)
		}
		finishLatencySpan(stageSpan, stageStarted, nil)
	}
	// Sanitize invalid tool calls in history to avoid poisoning future requests.
	sanitizeRequestMessages(ctx, invocation, llmRequest)
	return rebuildPlan
}

func sanitizeRequestMessages(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
) {
	if req == nil {
		return
	}
	before := req.Messages
	result := toolcall.SanitizeMessagesWithToolsResult(
		ctx,
		before,
		req.Tools,
	)
	req.Messages = result.Messages
	summaryview.RebaseAfterTransform(
		invocation,
		before,
		result.Messages,
		result.SourceIndexes,
	)
}

func normalizeContextCompactionThresholdRatio(ratio float64) float64 {
	if ratio > 0 && ratio <= 1 {
		return ratio
	}
	return defaultContextCompactionThresholdRatio
}

func (f *Flow) maybeCompactContextBeforeLLM(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	req *model.Request,
	rebuildPlan *contextCompactionRebuildPlan,
) *model.Request {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanContextCheck,
		latencyRequestAttrs(req)...,
	)
	defer func() {
		finishLatencySpan(span, started, nil)
	}()
	if req == nil || !f.enableContextCompaction || invocation == nil ||
		invocation.Session == nil || invocation.SessionService == nil ||
		!f.supportsSyncSummaryRetry() || rebuildPlan == nil ||
		rebuildPlan.beforeContent == nil || rebuildPlan.contentProcessor == nil {
		if started {
			span.SetAttributes(
				attribute.Bool(
					"llmflow.context_compaction.available",
					false,
				),
			)
		}
		return req
	}
	decisionRequest := requestWithCallLimitFinalizationMessage(
		req,
		rebuildPlan.callLimitFinalizationMessage,
	)
	decision := syncCompactContextDecision(
		ctx,
		invocation,
		decisionRequest,
		f.contextCompactionThresholdRatio,
		rebuildPlan.contentProcessor.ContextCompactionConfig.TokenCounter,
	)
	if decision.err == nil {
		summaryview.Finalize(invocation, decisionRequest, decision.tokenCount)
	}
	if started {
		span.SetAttributes(contextCompactionAttrs(decision, decisionRequest)...)
	}
	if decision.err != nil {
		if started {
			span.RecordError(decision.err)
			span.SetStatus(codes.Error, decision.err.Error())
		}
	}
	if !decision.shouldCompact {
		return req
	}
	return f.runContextCompaction(
		ctx,
		invocation,
		eventChan,
		req,
		rebuildPlan,
		decision,
	)
}

func (f *Flow) runContextCompaction(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	req *model.Request,
	rebuildPlan *contextCompactionRebuildPlan,
	decision contextCompactionDecision,
) *model.Request {
	startedAt := time.Now()
	decisionRequest := requestWithCallLimitFinalizationMessage(
		req,
		rebuildPlan.callLimitFinalizationMessage,
	)
	filterKey := invocation.GetEventFilterKey()
	before := snapshotSummary(invocation.Session, filterKey)
	emitLatencyDiagnosticEvent(
		ctx,
		invocation,
		eventChan,
		event.LatencyDiagnostic{
			Stage:         latencyDiagnosticStageCompact,
			Status:        latencyDiagnosticStatusStart,
			Summary:       "Context compaction is running.",
			TokenCount:    decision.tokenCount,
			Threshold:     decision.threshold,
			ContextWindow: decision.contextWindow,
			MessageCount:  len(decisionRequest.Messages),
			ToolCount:     len(decisionRequest.Tools),
			FilterKey:     filterKey,
		},
	)
	summaryCtx, summarySpan, summaryStarted := startLatencySpan(
		ctx,
		invocation,
		latencySpanContextSummary,
		contextCompactionAttrs(decision, decisionRequest)...,
	)
	summaryCtx = summary.ContextWithCacheSafeForkRequest(summaryCtx, req)
	view, viewPresent := summaryview.Snapshot(invocation)
	if viewPresent {
		summaryCtx = summaryview.ContextWithView(summaryCtx, view)
	}
	var usedRebuild bool
	logResult := func(
		outcome string,
		result *model.Request,
		postRequestTokens int,
	) {
		// After a rebuild, even when the post-rebuild token count fails,
		// the invocation holds the latest view. Paths that never rebuild
		// keep the snapshot frozen before summarization.
		binding := summaryview.BindingFromContext(summaryCtx)
		if usedRebuild {
			binding = summaryview.BindingFromInvocation(invocation)
		}
		filterKeyDisplay, filterKeyTruncated :=
			summarydiag.FormatFilterKey(filterKey)
		format := "Pre-LLM context compaction result: schema_version=%d, " +
			"outcome=%s, agent=%q, agent_truncated=%t, filter_key=%q, " +
			"filter_key_truncated=%t, " +
			"request_tokens=%d, threshold=%d, " +
			"context_window=%d, messages=%d->%d, post_request_tokens=%d, " +
			"summary_view_present=%t, summary_view_bound=%t, " +
			"summary_view_items=%d, binding_reason=%s, duration_ms=%d"
		agentName, agentTruncated :=
			summarydiag.FormatAgentName(invocation.AgentName)
		args := []any{
			summarydiag.SchemaVersion,
			outcome,
			agentName,
			agentTruncated,
			filterKeyDisplay,
			filterKeyTruncated,
			decision.tokenCount,
			decision.threshold,
			decision.contextWindow,
			len(decisionRequest.Messages),
			len(result.Messages),
			postRequestTokens,
			binding.Present,
			binding.Bound,
			binding.Items,
			binding.Reason,
			time.Since(startedAt).Milliseconds(),
		}
		if outcome == contextCompactionOutcomeSuccess {
			log.InfofContext(ctx, format, args...)
			return
		}
		log.WarnfContext(ctx, format, args...)
	}
	err := invocation.SessionService.CreateSessionSummary(
		summaryCtx,
		invocation.Session,
		filterKey,
		false,
	)
	finishLatencySpan(summarySpan, summaryStarted, err)
	after := snapshotSummary(invocation.Session, filterKey)
	updated := before.advanced(after)
	status := latencyDiagnosticStatusDone
	if !updated {
		status = latencyDiagnosticStatusSkip
	}
	if err != nil {
		status = latencyDiagnosticStatusError
	}
	emitLatencyDiagnosticEvent(
		ctx,
		invocation,
		eventChan,
		event.LatencyDiagnostic{
			Stage:         latencyDiagnosticStageCompact,
			Status:        status,
			Summary:       "Context compaction finished.",
			TokenCount:    decision.tokenCount,
			Threshold:     decision.threshold,
			ContextWindow: decision.contextWindow,
			MessageCount:  len(decisionRequest.Messages),
			ToolCount:     len(decisionRequest.Tools),
			FilterKey:     filterKey,
			Updated:       &updated,
		},
	)
	if !updated {
		outcome := contextCompactionOutcomeNoUpdate
		if err != nil {
			outcome = contextCompactionOutcomeSummaryError
		}
		logResult(outcome, decisionRequest, decision.tokenCount)
		return req
	}

	rebuildCtx, rebuildSpan, rebuildStarted := startLatencySpan(
		ctx,
		invocation,
		latencySpanContextRebuild,
	)
	rebuilt := f.rebuildRequestForContextCompaction(
		rebuildCtx,
		invocation,
		rebuildPlan,
	)
	if rebuildStarted && rebuilt != nil {
		rebuildSpan.SetAttributes(latencyRequestAttrs(rebuilt)...)
	}
	finishLatencySpan(rebuildSpan, rebuildStarted, nil)
	if rebuilt == nil {
		logResult(
			contextCompactionOutcomeRebuildUnavailable,
			decisionRequest,
			decision.tokenCount,
		)
		return req
	}
	usedRebuild = true
	postDecisionRequest := requestWithCallLimitFinalizationMessage(
		rebuilt,
		rebuildPlan.callLimitFinalizationMessage,
	)
	postDecision := syncCompactContextDecision(
		rebuildCtx,
		invocation,
		postDecisionRequest,
		f.contextCompactionThresholdRatio,
		rebuildPlan.contentProcessor.ContextCompactionConfig.TokenCounter,
	)
	postRequestTokens := postDecision.tokenCount
	if postDecision.err == nil {
		summaryview.Finalize(
			invocation,
			postDecisionRequest,
			postDecision.tokenCount,
		)
	} else {
		log.DebugfContext(
			ctx,
			"Post-compaction request token count failed for agent %s: %v",
			invocation.AgentName,
			postDecision.err,
		)
		postRequestTokens = -1
	}

	if err != nil {
		logResult(
			contextCompactionOutcomePersistenceError,
			postDecisionRequest,
			postRequestTokens,
		)
		return rebuilt
	}

	outcome := contextCompactionOutcomeSuccess
	if postDecision.err != nil {
		outcome = contextCompactionOutcomePostCountError
	}
	logResult(outcome, postDecisionRequest, postRequestTokens)
	return rebuilt
}

func (f *Flow) rebuildRequestForContextCompaction(
	ctx context.Context,
	invocation *agent.Invocation,
	rebuildPlan *contextCompactionRebuildPlan,
) *model.Request {
	if rebuildPlan == nil || rebuildPlan.beforeContent == nil ||
		rebuildPlan.contentProcessor == nil {
		return nil
	}

	rebuilt := cloneRequestForContextCompaction(rebuildPlan.beforeContent)
	if rebuilt == nil {
		return nil
	}
	if rebuilt.Tools == nil {
		rebuilt.Tools = make(map[string]tool.Tool)
	}
	rebuildPlan.contentProcessor.ProcessRequest(ctx, invocation, rebuilt, nil)
	for _, tailProcessor := range rebuildPlan.tailProcessors {
		tailProcessor.RebuildRequestForContextCompaction(
			ctx,
			invocation,
			rebuilt,
		)
	}
	sanitizeRequestMessages(ctx, invocation, rebuilt)
	return rebuilt
}

func (f *Flow) supportsSyncSummaryRetry() bool {
	for _, requestProcessor := range f.requestProcessors {
		contentProcessor, ok := requestProcessor.(*processor.ContentRequestProcessor)
		if !ok {
			continue
		}
		if contentProcessor.AddSessionSummary &&
			contentProcessor.TimelineFilterMode == processor.TimelineFilterAll {
			return true
		}
	}
	return false
}

func cloneRequestForContextCompaction(req *model.Request) *model.Request {
	if req == nil {
		return nil
	}

	cloned := *req
	cloned.Messages = cloneMessagesForContextCompaction(req.Messages)
	cloned.GenerationConfig = cloneGenerationConfigForContextCompaction(
		req.GenerationConfig,
	)
	cloned.StructuredOutput = cloneStructuredOutputForContextCompaction(
		req.StructuredOutput,
	)
	cloned.ExtraFields = cloneJSONMapForContextCompaction(req.ExtraFields)
	if req.Tools != nil {
		cloned.Tools = make(map[string]tool.Tool, len(req.Tools))
		for name, t := range req.Tools {
			cloned.Tools[name] = t
		}
	}
	return &cloned
}

func requestWithCallLimitFinalizationMessage(
	req *model.Request,
	message *model.Message,
) *model.Request {
	if req == nil || message == nil {
		return req
	}
	cloned := *req
	cloned.Messages = append(
		append([]model.Message(nil), req.Messages...),
		*message,
	)
	return &cloned
}

func cloneMessagesForContextCompaction(msgs []model.Message) []model.Message {
	if msgs == nil {
		return nil
	}

	cloned := make([]model.Message, len(msgs))
	for i := range msgs {
		cloned[i] = cloneMessageForContextCompaction(msgs[i])
	}
	return cloned
}

func cloneMessageForContextCompaction(msg model.Message) model.Message {
	cloned := msg
	cloned.ContentParts = cloneContentPartsForContextCompaction(
		msg.ContentParts,
	)
	cloned.ToolCalls = cloneToolCallsForContextCompaction(msg.ToolCalls)
	return cloned
}

func cloneContentPartsForContextCompaction(
	parts []model.ContentPart,
) []model.ContentPart {
	if parts == nil {
		return nil
	}

	cloned := make([]model.ContentPart, len(parts))
	for i := range parts {
		cloned[i] = cloneContentPartForContextCompaction(parts[i])
	}
	return cloned
}

func cloneContentPartForContextCompaction(
	part model.ContentPart,
) model.ContentPart {
	cloned := part
	if part.Text != nil {
		text := *part.Text
		cloned.Text = &text
	}
	if part.Image != nil {
		image := *part.Image
		if part.Image.Data != nil {
			image.Data = append([]byte(nil), part.Image.Data...)
		}
		cloned.Image = &image
	}
	if part.Audio != nil {
		audio := *part.Audio
		if part.Audio.Data != nil {
			audio.Data = append([]byte(nil), part.Audio.Data...)
		}
		cloned.Audio = &audio
	}
	if part.Video != nil {
		video := *part.Video
		if part.Video.Data != nil {
			video.Data = append([]byte(nil), part.Video.Data...)
		}
		cloned.Video = &video
	}
	if part.File != nil {
		file := *part.File
		if part.File.Data != nil {
			file.Data = append([]byte(nil), part.File.Data...)
		}
		cloned.File = &file
	}
	return cloned
}

func cloneToolCallsForContextCompaction(
	toolCalls []model.ToolCall,
) []model.ToolCall {
	if toolCalls == nil {
		return nil
	}

	cloned := make([]model.ToolCall, len(toolCalls))
	for i := range toolCalls {
		cloned[i] = toolCalls[i]
		if toolCalls[i].Function.Arguments != nil {
			cloned[i].Function.Arguments = append(
				[]byte(nil),
				toolCalls[i].Function.Arguments...,
			)
		}
		if toolCalls[i].Index != nil {
			index := *toolCalls[i].Index
			cloned[i].Index = &index
		}
		cloned[i].ExtraFields = cloneJSONMapForContextCompaction(
			toolCalls[i].ExtraFields,
		)
	}
	return cloned
}

func cloneGenerationConfigForContextCompaction(
	cfg model.GenerationConfig,
) model.GenerationConfig {
	cloned := cfg
	if cfg.Stop != nil {
		cloned.Stop = append([]string(nil), cfg.Stop...)
	}
	return cloned
}

func cloneStructuredOutputForContextCompaction(
	out *model.StructuredOutput,
) *model.StructuredOutput {
	if out == nil {
		return nil
	}

	cloned := *out
	if out.JSONSchema != nil {
		schema := *out.JSONSchema
		schema.Schema = cloneJSONMapForContextCompaction(out.JSONSchema.Schema)
		cloned.JSONSchema = &schema
	}
	return &cloned
}

func cloneJSONMapForContextCompaction(
	src map[string]any,
) map[string]any {
	return jsonmap.Clone(src)
}

func snapshotSummary(sess *session.Session, filterKey string) summarySnapshot {
	if sess == nil {
		return summarySnapshot{}
	}

	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()

	summary := sess.Summaries[filterKey]
	if summary == nil {
		return summarySnapshot{}
	}
	boundary := summary.CutoffBoundary()
	var boundaryCutoff time.Time
	var boundaryLastEventID string
	if boundary != nil {
		boundaryCutoff = boundary.CutoffTime()
		boundaryLastEventID = boundary.LastEventID
	}
	return summarySnapshot{
		exists:              true,
		summary:             summary.Summary,
		updatedAt:           summary.UpdatedAt,
		boundaryCutoff:      boundaryCutoff,
		boundaryLastEventID: boundaryLastEventID,
	}
}

func (s summarySnapshot) advanced(next summarySnapshot) bool {
	if !next.exists {
		return false
	}
	if !s.exists {
		return true
	}
	if next.boundaryCutoff.After(s.boundaryCutoff) {
		return true
	}
	if next.boundaryCutoff.Equal(s.boundaryCutoff) &&
		next.boundaryLastEventID != s.boundaryLastEventID {
		return true
	}
	if next.updatedAt.After(s.updatedAt) {
		return true
	}
	return next.summary != s.summary
}

func shouldSyncCompactContext(
	ctx context.Context,
	inv *agent.Invocation,
	req *model.Request,
	ratio float64,
	counter model.TokenCounter,
) bool {
	return syncCompactContextDecision(
		ctx,
		inv,
		req,
		ratio,
		counter,
	).shouldCompact
}

func syncCompactContextDecision(
	ctx context.Context,
	inv *agent.Invocation,
	req *model.Request,
	ratio float64,
	counter model.TokenCounter,
) contextCompactionDecision {
	decision := contextCompactionDecision{}
	if inv == nil || inv.Model == nil || req == nil || len(req.Messages) == 0 {
		return decision
	}

	decision.contextWindow = contextCompactionWindow(inv)
	decision.threshold, decision.thresholdBasis = contextCompactionThresholdForWindow(
		decision.contextWindow,
		ratio,
	)
	if counter == nil {
		counter = model.NewSimpleTokenCounter()
	}
	tokens, err := counter.CountTokensRange(ctx, req.Messages, 0, len(req.Messages))
	decision.tokenCount = tokens
	if err != nil {
		decision.err = err
		return decision
	}

	decision.shouldCompact = tokens >= decision.threshold
	return decision
}

func contextCompactionWindow(inv *agent.Invocation) int {
	contextWindow := contextCompactionFallbackWindow
	if inv != nil {
		if window, ok := agent.ModelContextWindowFromRunOptions(
			&inv.RunOptions,
		); ok {
			contextWindow = window
		} else if inv.Model != nil {
			if window, ok := modelcontext.ResolveContextWindow(inv.Model); ok {
				contextWindow = window
			}
		}
	}

	if contextWindow <= 0 {
		contextWindow = contextCompactionFallbackWindow
	}
	return contextWindow
}

func contextCompactionThreshold(inv *agent.Invocation, ratio float64) int {
	contextWindow := contextCompactionWindow(inv)
	threshold, _ := contextCompactionThresholdForWindow(contextWindow, ratio)
	return threshold
}

func contextCompactionThresholdForWindow(
	contextWindow int,
	ratio float64,
) (int, string) {
	threshold := int(float64(contextWindow) * normalizeContextCompactionThresholdRatio(ratio))
	basis := contextCompactionThresholdBasisContextWindow
	if threshold < contextCompactionMinTokens {
		threshold = contextCompactionMinTokens
		basis = contextCompactionThresholdBasisMinimumTokens
	}
	if threshold > contextWindow {
		threshold = contextWindow
		basis = contextCompactionThresholdBasisContextWindow
	}
	return threshold, basis
}

// getFilteredTools returns the list of tools for this invocation after applying the filter.
//
// User tools (can be filtered):
//   - Tools registered via WithTools
//   - Tools registered via WithToolSets
//
// Framework tools (never filtered):
//   - transfer_to_agent (auto-added when SubAgents are configured)
//   - knowledge_search / agentic_knowledge_search (auto-added when Knowledge is configured)
//
// This method is called during the preprocess stage, before sending the request to the model.
func (f *Flow) getFilteredTools(
	ctx context.Context,
	invocation *agent.Invocation,
) (resolved []tool.Tool) {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanResolveTools,
	)
	defer func() {
		if started {
			span.SetAttributes(
				attribute.Int("llmflow.tools.count", len(resolved)),
			)
		}
		finishLatencySpan(span, started, nil)
	}()

	if invocation == nil || invocation.Agent == nil {
		return nil
	}

	if cached, ok := toolsnapshot.Get(invocation); ok && cached != nil {
		return cached
	}

	allTools, userToolNames, hasUserToolTracking := toolsurface.ResolveBase(
		ctx,
		invocation,
	)
	traceableUserToolNames := trackedUserToolNames(
		allTools,
		hasUserToolTracking,
		userToolNames,
	)
	allTools, userToolNames, hasUserToolTracking, externalToolNames :=
		toolsurface.AppendRunOptionTools(
			allTools,
			userToolNames,
			hasUserToolTracking,
			invocation.RunOptions,
		)
	if f.toolActivationApplier != nil {
		allTools = append([]tool.Tool(nil), allTools...)
		if userToolNames != nil {
			userToolNames = copyToolNames(userToolNames)
		}
		if externalToolNames != nil {
			externalToolNames = copyToolNames(externalToolNames)
		}
		allTools, userToolNames, externalToolNames =
			f.toolActivationApplier(
				ctx,
				invocation,
				allTools,
				userToolNames,
				externalToolNames,
			)
		hasUserToolTracking = userToolNames != nil
	}

	// If no filter is specified, return all tools for this invocation.
	if invocation.RunOptions.ToolFilter == nil {
		allTools = sanitizeTools(allTools)
		setVisibleExternalToolNames(invocation, allTools, externalToolNames)
		toolsnapshot.Set(
			invocation,
			allTools,
			len(trackedUserToolNames(allTools, hasUserToolTracking, userToolNames)) > 0,
			filteredTraceableToolNames(allTools, traceableUserToolNames),
		)
		return allTools
	}

	// Framework tools are never filtered; user tools must pass the run-scoped
	// filter. Shared via toolsurface so getFilteredTools and the dynamic tool's
	// surface derivation stay in lockstep.
	filtered := toolsurface.ApplyToolFilter(
		ctx,
		allTools,
		userToolNames,
		hasUserToolTracking,
		invocation.RunOptions,
	)

	setVisibleExternalToolNames(invocation, filtered, externalToolNames)
	toolsnapshot.Set(
		invocation,
		filtered,
		len(trackedUserToolNames(filtered, hasUserToolTracking, userToolNames)) > 0,
		filteredTraceableToolNames(filtered, traceableUserToolNames),
	)

	return filtered
}

func (f *Flow) populateRequestTools(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
) {
	if req == nil || invocation == nil || invocation.Agent == nil {
		return
	}
	if req.Tools == nil {
		req.Tools = make(map[string]tool.Tool)
	}
	for _, tl := range f.getFilteredTools(ctx, invocation) {
		name := toolName(tl)
		if name == "" {
			continue
		}
		req.Tools[name] = tl
	}
}

func sanitizeTools(tools []tool.Tool) []tool.Tool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]tool.Tool, 0, len(tools))
	for _, tl := range tools {
		if toolName(tl) != "" {
			out = append(out, tl)
		}
	}
	return out
}

func setVisibleExternalToolNames(
	invocation *agent.Invocation,
	tools []tool.Tool,
	externalNames map[string]bool,
) {
	if invocation == nil || externalNames == nil {
		return
	}
	visible := make(map[string]bool, len(externalNames))
	for _, tl := range tools {
		name := toolName(tl)
		if name != "" && externalNames[name] {
			visible[name] = true
		}
	}
	invocation.RunOptions.ExternalToolNames = visible
}

func copyToolNames(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for name, ok := range src {
		dst[name] = ok
	}
	return dst
}

func toolName(tl tool.Tool) string {
	if tl == nil {
		return ""
	}
	decl := tl.Declaration()
	if decl == nil {
		return ""
	}
	return decl.Name
}

func trackedUserToolNames(
	tools []tool.Tool,
	hasUserToolTracking bool,
	userToolNames map[string]bool,
) []string {
	if len(tools) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(tools))
	if !hasUserToolTracking {
		for _, tl := range tools {
			if name := toolName(tl); name != "" {
				seen[name] = struct{}{}
			}
		}
		return sortedToolNames(seen)
	}
	for _, tl := range tools {
		name := toolName(tl)
		if name != "" && userToolNames[name] {
			seen[name] = struct{}{}
		}
	}
	return sortedToolNames(seen)
}

func sortedToolNames(names map[string]struct{}) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for name := range names {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func filteredTraceableToolNames(
	tools []tool.Tool,
	traceableToolNames []string,
) []string {
	if len(tools) == 0 || len(traceableToolNames) == 0 {
		return nil
	}
	traceable := make(map[string]struct{}, len(traceableToolNames))
	for _, name := range traceableToolNames {
		traceable[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(tools))
	for _, tl := range tools {
		name := toolName(tl)
		if name == "" {
			continue
		}
		if _, ok := traceable[name]; ok {
			seen[name] = struct{}{}
		}
	}
	return sortedToolNames(seen)
}

// callLLM performs the actual LLM call using core/model.
func (f *Flow) callLLM(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	callModel model.Model,
) (context.Context, model.Seq[*model.Response], bool, error) {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanCallLLM,
		latencyRequestAttrs(llmRequest)...,
	)
	var err error
	finishSpanOnReturn := true
	finishCallSpan := func(finishErr error) {
		if started && callModel != nil {
			span.SetAttributes(
				attribute.String("llmflow.model", callModel.Info().Name),
			)
		}
		finishLatencySpan(span, started, finishErr)
	}
	defer func() {
		if finishSpanOnReturn {
			finishCallSpan(err)
		}
	}()
	if callModel == nil {
		err = errors.New("no model available for LLM call")
		return ctx, nil, false, err
	}
	log.DebugfContext(
		ctx,
		"Calling LLM for agent %s",
		invocation.AgentName,
	)
	// Enforce optional per-invocation LLM call limit. When the limit is not
	// configured (<= 0), this is a no-op and preserves existing behavior.
	if err = invocation.IncLLMCallCount(); err != nil {
		log.Errorf("LLM call limit exceeded for agent %s: %v", invocation.AgentName, err)
		return ctx, nil, false, err
	}
	llmLimitReached := calllimit.RecordLLMCall(
		invocation,
		invocation.MaxLLMCalls,
	)
	finalizationInstruction, finalizing := calllimit.ActivateForLLM(
		invocation,
		llmLimitReached,
	)
	var finalizationMessage *callLimitFinalizationMessage
	if finalizing {
		finalizationMessage = appendCallLimitFinalizationMessage(
			llmRequest,
			finalizationInstruction,
		)
	}
	// Run before model callbacks if they exist.
	ctx, customResp, err := f.runBeforeModelCallbacks(ctx, invocation, llmRequest)
	if err != nil {
		reportSummaryInjection(ctx, invocation, llmRequest)
		return ctx, nil, false, err
	}
	if customResp != nil {
		// Keep the original callLLM return and span-on-return contract.
		// The seq finalizer reports once on drain or early stop. An
		// unused seq is never consumed, so it reports nothing.
		return ctx, withResponseSeqFinalizer(
			func(yield func(*model.Response) bool) {
				yield(customResp)
			},
			func() {
				reportSummaryInjection(ctx, invocation, llmRequest)
			},
		), false, nil
	}
	if llmRequest == nil || len(llmRequest.Messages) == 0 {
		err = errors.New(errMsgNoLLMMessages)
		return ctx, nil, false, err
	}
	if invocation != nil && invocation.RunOptions.ExecutionTraceEnabled {
		traceCtx := agent.NewInvocationContext(ctx, invocation)
		tracecapture.SetInvocationStepInput(
			traceCtx,
			traceSnapshotFromMessages(llmRequest.Messages),
		)
		tracecapture.MergeInvocationStepAppliedSurfaceIDs(
			traceCtx,
			executionTraceAppliedSurfaceIDs(invocation),
		)
	}
	ctx = contextWithModelRetryCallbacks(ctx, f, invocation, callModel)
	finalizeSummaryView(
		ctx,
		invocation,
		llmRequest,
		f.summaryViewTokenCounter(),
	)
	summaryfork.Attach(
		invocation,
		requestWithoutCallLimitFinalizationMessage(
			llmRequest,
			finalizationMessage,
		),
	)
	ctx, tailoringObserver := imodelrequest.ObserveTokenTailoring(
		ctx,
		func(record imodelrequest.TokenTailoringRecord) {
			summaryview.InvalidateBinding(invocation)
			summaryfork.Invalidate(invocation)
			if tokenTailoringCollapsedHistory(record) {
				log.WarnfContext(
					ctx,
					"Model request token tailoring collapsed history: "+
						"provider=%s, max_input_tokens=%d, messages=%d->%d",
					record.Provider,
					record.MaxInputTokens,
					record.BeforeMessages,
					record.AfterMessages,
				)
				return
			}
			log.DebugfContext(
				ctx,
				"Model request token tailoring applied: provider=%s, "+
					"max_input_tokens=%d, messages=%d->%d",
				record.Provider,
				record.MaxInputTokens,
				record.BeforeMessages,
				record.AfterMessages,
			)
		},
	)
	seq, err := f.generateContentSeq(ctx, invocation, llmRequest, callModel)
	if err != nil {
		// generateContentSeq failed before returning a seq. Report once
		// against the request as observed at that failure; do not also
		// attach a seq finalizer.
		reportSummaryInjection(ctx, invocation, llmRequest)
		return ctx, nil, true, err
	}
	// Eager GenerateContent has already mutated llmRequest. A lazy
	// IterModel may tailor or drop the selected summary only while the
	// seq runs. Report once after the seq ends or is stopped early, and
	// always attach that finalizer so a disabled call span cannot skip
	// the record.
	reportInjection := func() {
		reportSummaryInjection(ctx, invocation, llmRequest)
	}
	if started {
		finishSpanOnReturn = false
		seq = withResponseSeqFinalizer(seq, func() {
			reportInjection()
			span.SetAttributes(
				tokenTailoringAttrs(tailoringObserver.Snapshot())...,
			)
			finishCallSpan(nil)
		})
	} else {
		seq = withResponseSeqFinalizer(seq, reportInjection)
	}
	return ctx, seq, true, nil
}

func tokenTailoringCollapsedHistory(
	record imodelrequest.TokenTailoringRecord,
) bool {
	return record.BeforeMessages > 2 && record.AfterMessages <= 2
}

func withResponseSeqFinalizer(
	seq model.Seq[*model.Response],
	finalize func(),
) model.Seq[*model.Response] {
	var once sync.Once
	return func(yield func(*model.Response) bool) {
		defer once.Do(finalize)
		seq(yield)
	}
}

type callLimitFinalizationMessage struct {
	instruction  string
	index        int
	messageCount int
	priorMatches int
}

// appendCallLimitFinalizationMessage adds the request-scoped instruction as
// the final user message and returns its cache-safe exclusion marker. The
// request is not the session event history, so this does not create or persist
// a user event.
func appendCallLimitFinalizationMessage(
	req *model.Request,
	instruction string,
) *callLimitFinalizationMessage {
	if req == nil {
		return nil
	}
	marker := &callLimitFinalizationMessage{
		instruction:  instruction,
		index:        len(req.Messages),
		messageCount: len(req.Messages) + 1,
	}
	for _, message := range req.Messages {
		if isCallLimitFinalizationMessage(message, instruction) {
			marker.priorMatches++
		}
	}
	req.Messages = append(
		req.Messages,
		model.NewUserMessage(instruction),
	)
	return marker
}

// requestWithoutCallLimitFinalizationMessage returns a request view for
// cache-safe summarization without the transient finalization instruction.
// The provider request remains unchanged.
func requestWithoutCallLimitFinalizationMessage(
	req *model.Request,
	marker *callLimitFinalizationMessage,
) *model.Request {
	if req == nil || marker == nil {
		return req
	}
	index := -1
	if marker.index < len(req.Messages) && isCallLimitFinalizationMessage(
		req.Messages[marker.index],
		marker.instruction,
	) {
		index = marker.index
	} else {
		remainingPriorMatches := marker.priorMatches
		for i, message := range req.Messages {
			if !isCallLimitFinalizationMessage(message, marker.instruction) {
				continue
			}
			if remainingPriorMatches == 0 {
				index = i
				break
			}
			remainingPriorMatches--
		}
	}
	// A callback may rewrite fields on the synthetic message while leaving the
	// message slice structurally unchanged. In that case its original slot is
	// the provenance marker even though its payload no longer matches.
	if index < 0 && len(req.Messages) == marker.messageCount &&
		marker.index < len(req.Messages) {
		index = marker.index
	}
	if index < 0 {
		return req
	}
	cloned := *req
	cloned.Messages = make([]model.Message, 0, len(req.Messages)-1)
	cloned.Messages = append(cloned.Messages, req.Messages[:index]...)
	cloned.Messages = append(cloned.Messages, req.Messages[index+1:]...)
	return &cloned
}

func isCallLimitFinalizationMessage(
	message model.Message,
	instruction string,
) bool {
	return message.Role == model.RoleUser &&
		message.Content == instruction &&
		len(message.ContentParts) == 0 &&
		message.ToolID == "" &&
		message.ToolName == "" &&
		len(message.ToolCalls) == 0 &&
		message.ReasoningContent == "" &&
		message.ReasoningSignature == ""
}

// enforceCallLimitFinalizationToolFree keeps finalization requests tool-free
// across callback groups and retry callbacks. It is intentionally idempotent.
func enforceCallLimitFinalizationToolFree(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
) context.Context {
	if !calllimit.Active(invocation) {
		return ctx
	}
	if req == nil {
		return imodelrequest.WithToolsDisabled(ctx)
	}
	req.Tools = nil
	imodelrequest.DeleteToolControlFields(req.ExtraFields)
	return imodelrequest.WithToolsDisabled(ctx)
}

func finalizeSummaryView(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	counter model.TokenCounter,
) {
	if invocation == nil || req == nil || len(req.Messages) == 0 {
		return
	}
	if counter == nil {
		counter = model.NewSimpleTokenCounter()
	}
	tokens, err := counter.CountTokensRange(
		ctx,
		req.Messages,
		0,
		len(req.Messages),
	)
	if err != nil {
		log.DebugfContext(ctx, "final model-visible request token count failed: %v", err)
		return
	}
	summaryview.Finalize(invocation, req, tokens)
}

// reportSummaryInjection reports whether the session summary selected while
// building this request is still present in the same model.Request after the
// response sequence has been observed. Eager GenerateContent mutates that
// request before returning a seq; a lazy IterModel may mutate it only while
// the seq runs. Built-in providers, including the OpenAI adapter, mutate the
// request in place during token tailoring, so the record then reflects the
// tailored framework request. A custom Model may copy the request, in which
// case the record does not claim to describe the payload that Model sent.
// Requests that do not use session summaries report nothing.
func reportSummaryInjection(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
) {
	if invocation == nil || req == nil {
		return
	}
	selection, ok := summaryinject.FromInvocation(invocation)
	if !ok {
		return
	}
	blockPresent := selection.BlockPresent(req.Messages)
	outcome := summaryInjectionOutcome(selection, blockPresent)
	filterKey, filterKeyTruncated :=
		summarydiag.FormatFilterKey(invocation.GetEventFilterKey())
	agentName, agentTruncated :=
		summarydiag.FormatAgentName(invocation.AgentName)
	format := "Session summary injection result: schema_version=%d, " +
		"outcome=%s, agent=%q, agent_truncated=%t, filter_key=%q, " +
		"filter_key_truncated=%t, lookup_strategy=%s, lookup_result=%s, " +
		"selected=%t, block_text_present=%t, boundary_present=%t, " +
		"stored_summaries=%d, matching_candidates=%d, " +
		"full_session_summary=%t, session_events=%d, history_messages=%d, " +
		"request_messages=%d"
	args := []any{
		summarydiag.SchemaVersion,
		outcome,
		agentName,
		agentTruncated,
		filterKey,
		filterKeyTruncated,
		selection.LookupStrategy,
		selection.LookupResult,
		selection.Selected,
		blockPresent,
		selection.BoundaryPresent,
		selection.StoredSummaries,
		selection.MatchingCandidates,
		selection.FullSessionPresent,
		selection.SessionEvents,
		selection.HistoryMessages,
		len(req.Messages),
	}
	switch outcome {
	case summaryInjectionOutcomeBlockTextMissing:
		// A selected summary whose recorded block text is missing from every
		// framework request message is the only injection defect that Warns.
		// This is a substring observation of the same model.Request after
		// the response sequence has been observed; it does not claim what
		// a provider sent, or that the original injection slot is intact.
		log.WarnfContext(ctx, format, args...)
	default:
		// Requests that found no in-scope summary, including a branch miss
		// next to an unused full-session summary, are routine.
		log.DebugfContext(ctx, format, args...)
	}
}

// summaryInjectionOutcome classifies one request's summary injection. A
// selected summary whose recorded block text is missing from every message
// Content in the same framework model.Request is reported as
// block_text_missing. That observation does not describe a provider's
// final payload or prove the original injection slot. A scope mismatch
// names the unused full-session summary; it does not mean the scoped
// history was dropped from this request.
func summaryInjectionOutcome(
	selection summaryinject.Selection,
	blockPresent bool,
) string {
	if selection.Selected {
		if blockPresent {
			return summaryInjectionOutcomeBlockTextPresent
		}
		return summaryInjectionOutcomeBlockTextMissing
	}
	if selection.ScopeMismatch() {
		return summaryInjectionOutcomeScopeMismatch
	}
	if selection.StoredSummaries > 0 {
		return summaryInjectionOutcomeLookupMiss
	}
	return summaryInjectionOutcomeNotSelected
}

func (f *Flow) summaryViewTokenCounter() model.TokenCounter {
	for i := len(f.requestProcessors) - 1; i >= 0; i-- {
		contentProcessor, ok := f.requestProcessors[i].(*processor.ContentRequestProcessor)
		if ok {
			return contentProcessor.ContextCompactionConfig.TokenCounter
		}
	}
	return nil
}

func (f *Flow) runBeforeModelCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) (context.Context, *model.Response, error) {
	ctx = enforceCallLimitFinalizationToolFree(
		ctx,
		invocation,
		llmRequest,
	)
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanBeforeModel,
		latencyRequestAttrs(llmRequest)...,
	)
	var err error
	var resp *model.Response
	defer func() {
		if started {
			span.SetAttributes(
				attribute.Bool("llmflow.callback.custom_response", resp != nil),
			)
		}
		finishLatencySpan(span, started, err)
	}()
	var pluginCallbacks *model.Callbacks
	if invocation != nil && invocation.Plugins != nil {
		pluginCallbacks = invocation.Plugins.ModelCallbacks()
	}
	callbacksAttached := pluginCallbacks != nil || f.modelCallbacks != nil
	if !callbacksAttached {
		return ctx, nil, nil
	}
	callbackCtx := withInvocationContextIfMissing(ctx, invocation)
	ctx, resp, err = runBeforeModelCallbacksWith(callbackCtx, invocation, llmRequest, pluginCallbacks)
	if err != nil {
		log.ErrorfContext(ctx, "Before model plugin failed for agent %s: %v", invocation.AgentName, err)
		return ctx, nil, err
	}
	if resp != nil {
		return withInvocationContextIfMissing(ctx, invocation), resp, nil
	}
	ctx = withInvocationContextIfMissing(ctx, invocation)
	newCtx, resp, err := runBeforeModelCallbacksWith(ctx, invocation, llmRequest, f.modelCallbacks)
	if err != nil {
		log.ErrorfContext(newCtx, "Before model callback failed for agent %s: %v", invocation.AgentName, err)
	}
	return withInvocationContextIfMissing(newCtx, invocation), resp, err
}

func withInvocationContextIfMissing(ctx context.Context, invocation *agent.Invocation) context.Context {
	if invocation == nil {
		return ctx
	}
	existingInvocation, ok := agent.InvocationFromContext(ctx)
	if ok && existingInvocation != nil {
		return ctx
	}
	return agent.NewInvocationContext(ctx, invocation)
}

func invocationFromContextOrFallback(ctx context.Context, fallback *agent.Invocation) *agent.Invocation {
	existingInvocation, ok := agent.InvocationFromContext(ctx)
	if ok && existingInvocation != nil {
		return existingInvocation
	}
	return fallback
}

func runBeforeModelCallbacksWith(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	callbacks *model.Callbacks,
) (context.Context, *model.Response, error) {
	if callbacks == nil {
		return ctx, nil, nil
	}
	result, err := wrapBeforeModelCallbacksWithInvocation(callbacks, invocation).
		RunBeforeModel(ctx, &model.BeforeModelArgs{Request: llmRequest})
	if err != nil {
		return ctx, nil, err
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResponse != nil {
		return ctx, result.CustomResponse, nil
	}
	return ctx, nil, nil
}

func wrapBeforeModelCallbacksWithInvocation(
	callbacks *model.Callbacks,
	invocation *agent.Invocation,
) *model.Callbacks {
	if callbacks == nil || invocation == nil || len(callbacks.BeforeModel) == 0 {
		return callbacks
	}
	wrapped := *callbacks
	wrapped.BeforeModel = make([]model.BeforeModelCallbackStructured, len(callbacks.BeforeModel))
	for i, cb := range callbacks.BeforeModel {
		callback := cb
		wrapped.BeforeModel[i] = func(
			ctx context.Context,
			args *model.BeforeModelArgs,
		) (*model.BeforeModelResult, error) {
			ctx = withInvocationContextIfMissing(ctx, invocation)
			ctx = enforceCallLimitFinalizationToolFree(
				ctx,
				invocation,
				args.Request,
			)
			result, err := callback(ctx, args)
			if result != nil && result.Context != nil {
				clonedResult := *result
				resultCtx := withInvocationContextIfMissing(
					result.Context,
					invocationFromContextOrFallback(ctx, invocation),
				)
				clonedResult.Context = enforceCallLimitFinalizationToolFree(
					resultCtx,
					invocation,
					args.Request,
				)
				return &clonedResult, err
			}
			enforceCallLimitFinalizationToolFree(
				ctx,
				invocation,
				args.Request,
			)
			return result, err
		}
	}
	return &wrapped
}

func (f *Flow) generateContentSeq(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	callModel model.Model,
) (model.Seq[*model.Response], error) {
	if llmRequest == nil || len(llmRequest.Messages) == 0 {
		return nil, errors.New(errMsgNoLLMMessages)
	}
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanGenerateContent,
		latencyRequestAttrs(llmRequest)...,
	)
	var err error
	defer func() {
		if started && callModel != nil {
			span.SetAttributes(
				attribute.String("llmflow.model", callModel.Info().Name),
			)
		}
		finishLatencySpan(span, started, err)
	}()
	if iterModel, ok := callModel.(model.IterModel); ok {
		seq, genErr := iterModel.GenerateContentIter(ctx, llmRequest)
		err = genErr
		if err != nil {
			log.ErrorfContext(
				ctx,
				"LLM call failed for agent %s: %v",
				invocation.AgentName,
				err,
			)
			return nil, err
		}
		if seq == nil {
			return nil, errors.New(errMsgNoModelResponse)
		}
		return normalizeResponseIDs(seq), nil
	}

	responseChan, genErr := callModel.GenerateContent(ctx, llmRequest)
	err = genErr
	if err != nil {
		log.ErrorfContext(
			ctx,
			"LLM call failed for agent %s: %v",
			invocation.AgentName,
			err,
		)
		return nil, err
	}

	return normalizeResponseIDs(func(yield func(*model.Response) bool) {
		for resp := range responseChan {
			if !yield(resp) {
				return
			}
		}
	}), nil
}

func normalizeResponseIDs(seq model.Seq[*model.Response]) model.Seq[*model.Response] {
	if seq == nil {
		return nil
	}
	return func(yield func(*model.Response) bool) {
		currentID := ""
		seq(func(resp *model.Response) bool {
			normalized := normalizeResponseID(resp, &currentID)
			keepGoing := yield(normalized)
			if normalized != nil && normalized.Done && !normalized.IsPartial {
				currentID = ""
			}
			return keepGoing
		})
	}
}

func normalizeResponseID(resp *model.Response, currentID *string) *model.Response {
	if resp == nil {
		return nil
	}
	if currentID == nil {
		return resp
	}
	// Preserve one stable ID for the entire active response stream.
	if *currentID == "" {
		if resp.ID != "" {
			*currentID = resp.ID
		} else {
			*currentID = generatedResponseIDPrefix + uuid.NewString()
		}
	}
	if resp.ID == *currentID {
		return resp
	}
	cloned := resp.Clone()
	cloned.ID = *currentID
	return cloned
}

// postprocess handles post-LLM call processing using response processors.
func (f *Flow) postprocess(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	llmResponse *model.Response,
	eventChan chan<- *event.Event,
) {
	f.postprocessWithLatencySpans(
		ctx,
		invocation,
		llmRequest,
		llmResponse,
		eventChan,
		true,
	)
}

func (f *Flow) postprocessWithLatencySpans(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
	llmResponse *model.Response,
	eventChan chan<- *event.Event,
	traceDetails bool,
) {
	if !traceDetails {
		for _, processor := range f.responseProcessors {
			processor.ProcessResponse(
				ctx,
				invocation,
				llmRequest,
				llmResponse,
				eventChan,
			)
		}
		return
	}
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanPostprocess,
		latencyResponseAttrs(llmResponse)...,
	)
	defer func() {
		if started {
			span.SetAttributes(
				attribute.Int(
					"llmflow.postprocess.stages",
					len(f.responseProcessors),
				),
			)
		}
		finishLatencySpan(span, started, nil)
	}()
	if llmResponse == nil {
		return
	}

	// Run response processors - they send events directly to the channel.
	for _, processor := range f.responseProcessors {
		stageCtx, stageSpan, stageStarted := startLatencySpan(
			ctx,
			invocation,
			latencyProcessorStageSpanName(
				latencySpanPostprocessStage,
				processor,
			),
			attribute.String(
				"llmflow.postprocess.stage",
				latencyProcessorName(processor),
			),
		)
		processor.ProcessResponse(
			stageCtx,
			invocation,
			llmRequest,
			llmResponse,
			eventChan,
		)
		finishLatencySpan(stageSpan, stageStarted, nil)
	}
}

// WaitEventTimeout returns the remaining time until the context deadline.
// If the context has no deadline, it returns the default event completion timeout.
func WaitEventTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return eventCompletionTimeout
}
