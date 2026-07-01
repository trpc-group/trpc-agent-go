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
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/toolsnapshot"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonmap"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonrepair"
	"trpc.group/trpc-go/trpc-agent-go/internal/modelcontext"
	"trpc.group/trpc-go/trpc-agent-go/internal/responseusage"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/steer"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsurface"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionsummary "trpc.group/trpc-go/trpc-agent-go/session/summary"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// Timeout for event completion signaling.
	eventCompletionTimeout    = 5 * time.Second
	generatedResponseIDPrefix = "llmflow-response-"
	queuedUserAuthor          = "user"

	errMsgNoModelResponse = "no response received from model"

	flowRunPanicLogFmt = log.PanicPrefix + " Flow execution panic (invocation: %s, " +
		"agent: %s): %v\n%s"

	flowRunPanicErrFmt = "flow panic: %v"

	defaultContextCompactionThresholdRatio = 0.7
	contextCompactionFallbackWindow        = 8192
	contextCompactionMinTokens             = 2000
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
	beforeContent    *model.Request
	contentProcessor *processor.ContentRequestProcessor
	tailProcessors   []contextCompactionTailProcessor
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

func traceSnapshotFromEvent(evt *event.Event) *atrace.Snapshot {
	if evt == nil || evt.Response == nil {
		return nil
	}
	bytes, err := json.Marshal(evt.Response)
	if err != nil {
		return nil
	}
	return &atrace.Snapshot{Text: string(bytes)}
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

	err = invocation.SessionService.CreateSessionSummary(
		ctx,
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

// runOneStep executes one step of the flow (one LLM call cycle).
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
	observabilityInvocation := invocationViewForModel(invocation, callModel)
	stepID := agent.StartExecutionTraceStep(
		invocation,
		agent.InvocationTraceNodeID(invocation),
		traceSnapshotFromMessages(llmRequest.Messages),
		nil,
	)
	agent.SetExecutionTraceStepAppliedSurfaceIDs(invocation, stepID)
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
	ctx, responseSeq, err := f.callLLM(ctx, invocation, llmRequest, callModel)
	if err != nil {
		agent.FinishExecutionTraceStep(invocation, stepID, nil, err)
		return nil, err
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
	agent.FinishExecutionTraceStep(invocation, stepID, traceSnapshotFromEvent(lastEvent), err)
	if lastEvent != nil && lastEvent.Response != nil {
		agent.SetExecutionTraceStepUsage(invocation, stepID, lastEvent.Response.Usage)
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
		metricsInvocationForCurrent(
			p.currentInvocation,
			p.observabilityInvocation,
		),
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
	itelemetry.TraceChat(p.span, &itelemetry.TraceChatAttributes{
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

func invocationViewForModel(
	invocation *agent.Invocation,
	callModel model.Model,
) *agent.Invocation {
	if invocation == nil {
		return nil
	}
	return invocation.View(agent.WithInvocationModel(callModel))
}

func metricsInvocationForCurrent(
	current *agent.Invocation,
	base *agent.Invocation,
) *agent.Invocation {
	if base == nil {
		return current
	}
	return observabilityInvocationForCurrent(current, base)
}

func observabilityInvocationForCurrent(
	current *agent.Invocation,
	base *agent.Invocation,
) *agent.Invocation {
	if base == nil {
		return current
	}
	if current == nil || current.Session == nil {
		return base
	}
	return base.View(
		agent.WithInvocationSession(current.Session),
		agent.WithInvocationModel(base.Model),
	)
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
	llmRequest.Messages = toolcall.SanitizeMessagesWithTools(ctx, llmRequest.Messages, llmRequest.Tools)
	return rebuildPlan
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
	decision := syncCompactContextDecision(
		ctx,
		invocation,
		req,
		f.contextCompactionThresholdRatio,
		rebuildPlan.contentProcessor.ContextCompactionConfig.TokenCounter,
	)
	if started {
		span.SetAttributes(contextCompactionAttrs(decision, req)...)
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
			MessageCount:  len(req.Messages),
			ToolCount:     len(req.Tools),
			FilterKey:     filterKey,
		},
	)
	summaryCtx, summarySpan, summaryStarted := startLatencySpan(
		ctx,
		invocation,
		latencySpanContextSummary,
		contextCompactionAttrs(decision, req)...,
	)
	summaryCtx = sessionsummary.ContextWithCacheSafeForkRequest(summaryCtx, req)
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
			MessageCount:  len(req.Messages),
			ToolCount:     len(req.Tools),
			FilterKey:     filterKey,
			Updated:       &updated,
		},
	)
	if !updated {
		if err != nil {
			log.DebugfContext(
				ctx,
				"Pre-LLM context compaction skipped for agent %s: %v",
				invocation.AgentName,
				err,
			)
		}
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
		log.DebugfContext(
			ctx,
			"Pre-LLM context compaction skipped for agent %s: safe rebuild unavailable",
			invocation.AgentName,
		)
		return req
	}

	if err != nil {
		log.WarnfContext(
			ctx,
			"Pre-LLM context compaction rebuilt request for agent %s after in-memory summary update; persistence failed: %v",
			invocation.AgentName,
			err,
		)
		return rebuilt
	}

	log.DebugfContext(
		ctx,
		"Pre-LLM context compaction rebuilt request for agent %s",
		invocation.AgentName,
	)
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
	rebuilt.Messages = toolcall.SanitizeMessagesWithTools(
		ctx,
		rebuilt.Messages,
		rebuilt.Tools,
	)
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

	decision.threshold = contextCompactionThreshold(inv, ratio)
	decision.contextWindow = contextCompactionWindow(inv)
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
	threshold := int(float64(contextWindow) * normalizeContextCompactionThresholdRatio(ratio))
	if threshold < contextCompactionMinTokens {
		threshold = contextCompactionMinTokens
	}
	if threshold > contextWindow {
		threshold = contextWindow
	}
	return threshold
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
) (context.Context, model.Seq[*model.Response], error) {
	ctx, span, started := startLatencySpan(
		ctx,
		invocation,
		latencySpanCallLLM,
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
	if callModel == nil {
		err = errors.New("no model available for LLM call")
		return ctx, nil, err
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
		return ctx, nil, err
	}
	// Run before model callbacks if they exist.
	ctx, customResp, err := f.runBeforeModelCallbacks(ctx, invocation, llmRequest)
	if err != nil {
		return ctx, nil, err
	}
	if customResp != nil {
		return ctx, func(yield func(*model.Response) bool) {
			yield(customResp)
		}, nil
	}
	seq, err := f.generateContentSeq(ctx, invocation, llmRequest, callModel)
	if err != nil {
		return ctx, nil, err
	}
	return ctx, seq, nil
}

func (f *Flow) runBeforeModelCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) (context.Context, *model.Response, error) {
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
			result, err := callback(ctx, args)
			if result != nil && result.Context != nil {
				clonedResult := *result
				clonedResult.Context = withInvocationContextIfMissing(
					result.Context,
					invocationFromContextOrFallback(ctx, invocation),
				)
				return &clonedResult, err
			}
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
