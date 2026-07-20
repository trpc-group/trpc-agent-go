//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonrepair"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonutils"
	"trpc.group/trpc-go/trpc-agent-go/internal/jsonx"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolretry"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultcodec"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

const (
	// ErrorToolNotFound is the error message for tool not found.
	ErrorToolNotFound = "Error: tool not found"
	// ErrorCallableToolExecution is the error message for callable tool execution failed.
	ErrorCallableToolExecution = "Error: callable tool execution failed"
	// ErrorStreamableToolExecution is the error message for streamable tool execution failed.
	ErrorStreamableToolExecution = "Error: streamable tool execution failed"
	// ErrorMarshalResult is the error message for failed to marshal result.
	ErrorMarshalResult = "Error: failed to marshal result"
	// ErrorEncodeResult is the error message for a result codec failing to
	// encode the tool result into model-visible text.
	ErrorEncodeResult = "Error: failed to encode tool result"
)

const (
	maxToolNameSuggestions    = 3
	maxToolNameDistance       = 3
	maxToolNameErrorNameRunes = 160
)

// funcRespCompletionTimeout is the default wait duration for ensuring a
// tool.response event has been processed by the session persistence layer.
const funcRespCompletionTimeout = 5 * time.Second

const (
	knowledgeSearchToolName                    = "knowledge_search"
	knowledgeSearchWithAgenticFilterName       = "knowledge_search_with_agentic_filter"
	knowledgeSearchToolNameSuffix              = "_knowledge_search"
	knowledgeSearchWithAgenticFilterNameSuffix = "_knowledge_search_with_agentic_filter"
)

// summarizationSkipper is implemented by tools that can indicate whether
// the flow should skip a post-tool summarization step. This allows tools
// like AgentTool to mark their tool.response as final for the turn.
type summarizationSkipper interface {
	SkipSummarization() bool
}

// streamInnerPreference is implemented by tools that want to control whether
// the flow should treat them as streamable (forwarding inner deltas) or fall
// back to the callable path. When this returns false, the flow will not use
// the StreamableTool path even if the tool implements it.
type streamInnerPreference interface {
	StreamInner() bool
}

type innerTextModePreference interface {
	InnerTextMode() tool.InnerTextMode
}

type autoMemoryPollutionSource interface {
	PollutesAutoMemory() bool
}

type originalToolProvider interface {
	Original() tool.Tool
}

type toolEventStateDelta struct {
	tool            tool.Tool
	invocation      *agent.Invocation
	sessionBaseline session.StateMap
	args            []byte
	choice          model.Choice
	// content holds the JSON-encoded tool result used to compute stateful-tool
	// state deltas. It keeps state deltas on JSON even when a result codec
	// changes the model-visible message. When nil, the choice message content is
	// used (the default JSON behavior).
	content []byte
}

type resolvedToolContextKey struct{}
type stateDeltaSessionBaselineContextKey struct{}
type executingToolArgsContextKey struct{}
type skipToolStateDeltaContextKey struct{}
type skipToolSkipSummarizationContextKey struct{}
type stateDeltaContentContextKey struct{}

// withStateDeltaContent stores the JSON-encoded tool result used to compute
// stateful-tool state deltas, so a per-tool result codec only changes the
// model-visible message and not the bytes stateful tools parse.
func withStateDeltaContent(ctx context.Context, content []byte) context.Context {
	return context.WithValue(ctx, stateDeltaContentContextKey{}, content)
}

func stateDeltaContentFromContext(ctx context.Context) []byte {
	content, _ := ctx.Value(stateDeltaContentContextKey{}).([]byte)
	return content
}

// toolResult holds the result of a single tool execution.
type toolResult struct {
	index      int
	event      *event.Event
	err        error
	stateDelta *toolEventStateDelta
	toolArgs   []byte
}

// Default message used when transferring to a sub-agent without an explicit message.
// Users can override or disable it via SetDefaultTransferMessage.
var defaultTransferMessage = "Task delegated from coordinator"

// SetDefaultTransferMessage configures the message to inject when a sub-agent is
// called without an explicit message (model directly calls the sub-agent name).
func SetDefaultTransferMessage(message string) {
	defaultTransferMessage = message
}

// subAgentCall defines the input format for direct sub-agent tool calls.
// This handles cases where models call sub-agent names directly instead of using transfer_to_agent.
type subAgentCall struct {
	Message string `json:"message,omitempty"`
}

// FunctionCallResponseProcessor handles agent transfer operations after LLM responses.
type FunctionCallResponseProcessor struct {
	enableParallelTools       bool
	toolCallbacks             *tool.Callbacks
	toolRetryPolicy           *tool.RetryPolicy
	postToolResultHooks       []PostToolResultHook
	attachmentBudget          int
	toolNameSuggestionOptions toolNameSuggestionOptions
}

// FunctionCallResponseProcessorOption configures a function-call response processor.
type FunctionCallResponseProcessorOption func(*FunctionCallResponseProcessor)

type toolNameSuggestionOptions struct {
	maxSuggestions int
	maxDistance    int
}

// PostToolResultHook observes and may mutate a completed tool result event.
type PostToolResultHook func(
	ctx context.Context,
	invocation *agent.Invocation,
	ev *event.Event,
)

// WithToolCallRetryPolicy sets the retry policy used for single callable tool invocations.
func WithToolCallRetryPolicy(policy *tool.RetryPolicy) FunctionCallResponseProcessorOption {
	return func(p *FunctionCallResponseProcessor) {
		if policy == nil {
			p.toolRetryPolicy = nil
			return
		}
		p.toolRetryPolicy = policy
	}
}

// WithPostToolResultHook appends an internal hook after tool state delta is attached.
func WithPostToolResultHook(
	hook PostToolResultHook,
) FunctionCallResponseProcessorOption {
	return func(p *FunctionCallResponseProcessor) {
		if hook == nil {
			return
		}
		p.postToolResultHooks = append(p.postToolResultHooks, hook)
	}
}

// WithToolResultAttachmentBudget limits callback-managed attachments across
// one tool response processing pass. Non-positive values preserve the legacy
// unlimited behavior.
func WithToolResultAttachmentBudget(
	maxAttachments int,
) FunctionCallResponseProcessorOption {
	return func(p *FunctionCallResponseProcessor) {
		p.attachmentBudget = maxAttachments
	}
}

// WithToolNameSuggestions configures tool-not-found suggestion generation.
// Non-positive maxSuggestions or negative maxDistance disables suggestions.
func WithToolNameSuggestions(
	maxSuggestions int,
	maxDistance int,
) FunctionCallResponseProcessorOption {
	return func(p *FunctionCallResponseProcessor) {
		if maxSuggestions <= 0 || maxDistance < 0 {
			p.toolNameSuggestionOptions = toolNameSuggestionOptions{}
			return
		}
		p.toolNameSuggestionOptions = toolNameSuggestionOptions{
			maxSuggestions: maxSuggestions,
			maxDistance:    maxDistance,
		}
	}
}

// NewFunctionCallResponseProcessor creates a new transfer response processor.
func NewFunctionCallResponseProcessor(
	enableParallelTools bool,
	toolCallbacks *tool.Callbacks,
	opts ...FunctionCallResponseProcessorOption,
) *FunctionCallResponseProcessor {
	processor := &FunctionCallResponseProcessor{
		enableParallelTools:       enableParallelTools,
		toolCallbacks:             toolCallbacks,
		toolNameSuggestionOptions: defaultToolNameSuggestionOptions(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(processor)
		}
	}
	return processor
}

// ProcessResponse implements the flow.ResponseProcessor interface.
// It checks for transfer requests and handles agent handoffs by actually calling
// the target agent's Run method.
func (p *FunctionCallResponseProcessor) ProcessResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
	ch chan<- *event.Event,
) {
	if invocation == nil || rsp == nil || rsp.IsPartial || !rsp.IsToolCallResponse() {
		return
	}

	// Enforce optional per-invocation tool iteration limit. A "tool iteration"
	// is defined as an assistant response that contains tool calls and reaches
	// this processor. When the limit is not configured (<= 0), this check is a
	// no-op and preserves existing behavior.
	if invocation.IncToolIteration() {
		// Mark the invocation as ended so the flow will not issue another LLM call.
		invocation.EndInvocation = true

		// Emit an error response event describing the limit breach instead of
		// executing any tools. This makes the termination visible to callers
		// while avoiding additional model or tool invocations.
		resp := &model.Response{
			Object: model.ObjectTypeError,
			Error: &model.ResponseError{
				Type:    model.ErrorTypeFlowError,
				Message: fmt.Sprintf("max tool iterations (%d) exceeded", invocation.MaxToolIterations),
			},
			Done: true,
		}
		agent.EmitEvent(ctx, invocation, ch, event.NewResponseEvent(
			invocation.InvocationID,
			invocation.AgentName,
			resp,
		))
		return
	}

	deferred, executable, unknown := p.toolExecutionDecision(
		ctx,
		invocation,
		req,
		rsp,
	)
	if deferred && !executable && !unknown {
		invocation.EndInvocation = true
		return
	}

	functioncallResponseEvent, err := p.handleFunctionCallsAndSendEventWithRequest(ctx, invocation, req, rsp, ch)

	// Option one: set invocation.EndInvocation is true, and stop next step.
	// Option two: emit error event, maybe the LLM can correct this error and also need to wait for notice completion.
	// maybe the Option two is better.
	// Allow users to intervene in error handling through callbacks.
	if _, ok := agent.AsStopError(err); ok {
		invocation.EndInvocation = true
		return
	}

	if deferred {
		invocation.EndInvocation = true
	}

	if err != nil || functioncallResponseEvent == nil {
		return
	}

	if invocation.EndInvocation {
		return
	}

	// If the tool indicates skipping outer summarization, mark the invocation to end
	// after this tool response so the flow does not perform an extra LLM call.
	if functioncallResponseEvent.Actions != nil && functioncallResponseEvent.Actions.SkipSummarization {
		invocation.EndInvocation = true
		return
	}
}

func (p *FunctionCallResponseProcessor) toolExecutionDecision(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	rsp *model.Response,
) (deferred bool, executable bool, unknown bool) {
	if invocation == nil {
		return false, true, false
	}
	if req == nil || req.Tools == nil || rsp == nil {
		return false, true, false
	}
	if len(rsp.Choices) == 0 {
		return false, true, false
	}
	for _, tc := range rsp.Choices[0].Message.ToolCalls {
		tl, ok := req.Tools[tc.Function.Name]
		if !ok {
			unknown = true
			continue
		}
		if invocation.RunOptions.ShouldExecuteTool(ctx, tl) {
			executable = true
			continue
		}
		deferred = true
	}
	return deferred, executable, unknown
}

func (p *FunctionCallResponseProcessor) handleFunctionCallsAndSendEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	return p.handleFunctionCallsAndSendEventWithRequest(
		ctx,
		invocation,
		&model.Request{Tools: tools},
		llmResponse,
		eventChan,
	)
}

func (p *FunctionCallResponseProcessor) handleFunctionCallsAndSendEventWithRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	llmResponse *model.Response,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	var tools map[string]tool.Tool
	if req != nil {
		tools = req.Tools
	}
	functionResponseEvent, err := p.handleFunctionCallsWithRequest(
		ctx,
		invocation,
		req,
		llmResponse,
		tools,
		eventChan,
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Function call handling failed for agent %s: %v",
			invocation.AgentName,
			err,
		)
		agent.EmitEvent(ctx, invocation, eventChan, event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			model.ErrorTypeFlowError,
			err.Error(),
		))
		return nil, err
	}

	if functionResponseEvent == nil {
		return nil, nil
	}

	functionResponseEvent.RequiresCompletion = true
	agent.EmitEvent(ctx, invocation, eventChan, functionResponseEvent)

	if !appender.IsAttached(invocation) {
		return functionResponseEvent, nil
	}

	completionID :=
		agent.GetAppendEventNoticeKey(functionResponseEvent.ID)
	timeout := funcRespWaitTimeout(ctx)
	err = invocation.AddNoticeChannelAndWait(ctx, completionID, timeout)
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if err != nil {
		log.WarnfContext(
			ctx,
			"Wait for tool response persistence failed: %v",
			err,
		)
	}
	return functionResponseEvent, nil
}

func funcRespWaitTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return time.Until(deadline)
	}
	return funcRespCompletionTimeout
}

// handleFunctionCalls executes tool calls and returns a merged response event.
func (p *FunctionCallResponseProcessor) handleFunctionCalls(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	return p.handleFunctionCallsWithRequest(
		ctx,
		invocation,
		&model.Request{Tools: tools},
		llmResponse,
		tools,
		eventChan,
	)
}

func (p *FunctionCallResponseProcessor) handleFunctionCallsWithRequest(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	if p.attachmentBudget > 0 {
		ctx = tool.WithToolResultAttachmentBudget(
			ctx,
			p.attachmentBudget,
		)
	}
	toolCalls := llmResponse.Choices[0].Message.ToolCalls

	// If parallel tools are enabled AND multiple tool calls, execute concurrently
	if p.enableParallelTools && len(toolCalls) > 1 {
		mergedEvent, err := p.executeToolCallsInParallel(
			ctx,
			invocation,
			llmResponse,
			toolCalls,
			tools,
			eventChan,
		)
		if err != nil {
			return mergedEvent, err
		}
		if err := p.applyAfterToolMessagesHooks(
			ctx,
			invocation,
			req,
			llmResponse,
			mergedEvent,
		); err != nil {
			return nil, err
		}
		return mergedEvent, nil
	}

	toolResults := make([]toolResult, 0, len(toolCalls))
	for i, tc := range toolCalls {
		result, err := p.executeSingleToolCallSequentialResult(
			ctx, invocation, llmResponse, tools, eventChan, i, tc,
		)
		if err != nil {
			return nil, err
		}
		toolResults = append(toolResults, result)
	}
	toolCallResponsesEvents := p.attachStateDeltaToToolResults(
		ctx,
		invocation,
		toolResults,
	)

	if len(toolCallResponsesEvents) == 0 && invocation != nil {
		for _, tc := range toolCalls {
			tl, ok := tools[tc.Function.Name]
			if ok && !invocation.RunOptions.ShouldExecuteTool(ctx, tl) {
				return nil, nil
			}
		}
	}

	mergedEvent := p.buildMergedParallelEvent(
		ctx, invocation, llmResponse, tools, toolCalls, toolResults,
		toolCallResponsesEvents,
	)
	if err := p.applyAfterToolMessagesHooks(
		ctx,
		invocation,
		req,
		llmResponse,
		mergedEvent,
	); err != nil {
		return nil, err
	}
	return mergedEvent, nil
}

type afterToolMessagesManager interface {
	AfterToolMessages(
		context.Context,
		*plugin.AfterToolMessagesArgs,
	) (*plugin.AfterToolMessagesResult, error)
}

func (p *FunctionCallResponseProcessor) applyAfterToolMessagesHooks(
	ctx context.Context,
	invocation *agent.Invocation,
	req *model.Request,
	llmResponse *model.Response,
	toolResultEvent *event.Event,
) error {
	if invocation == nil || invocation.Plugins == nil ||
		toolResultEvent == nil || toolResultEvent.Response == nil {
		return nil
	}
	hooks, ok := invocation.Plugins.(afterToolMessagesManager)
	if !ok {
		return nil
	}
	toolResultMessages := toolResultMessagesFromEvent(toolResultEvent)
	if len(toolResultMessages) == 0 {
		return nil
	}
	args := &plugin.AfterToolMessagesArgs{
		Invocation:         invocation,
		Request:            req,
		ToolCallResponse:   llmResponse,
		ToolResultEvent:    toolResultEvent,
		Messages:           afterToolMessagesView(req, llmResponse, toolResultMessages),
		ToolCalls:          toolCallsFromResponse(llmResponse),
		ToolResultMessages: cloneModelMessages(toolResultMessages),
	}
	result, err := hooks.AfterToolMessages(ctx, args)
	if err != nil {
		return err
	}
	if result == nil || len(result.ToolResultMessages) == 0 {
		return nil
	}
	choices, err := replacementToolChoices(
		toolResultEvent.Response.Choices,
		result.ToolResultMessages,
	)
	if err != nil {
		return err
	}
	toolResultEvent.Response.Choices = choices
	return nil
}

func toolResultMessagesFromEvent(ev *event.Event) []model.Message {
	if ev == nil || ev.Response == nil {
		return nil
	}
	messages := make([]model.Message, 0, len(ev.Response.Choices))
	for _, choice := range ev.Response.Choices {
		msg := choice.Message
		if msg.ToolID == "" && choice.Delta.ToolID != "" {
			msg = choice.Delta
		}
		if msg.ToolID == "" || !model.HasPayload(msg) {
			continue
		}
		messages = append(messages, msg)
	}
	return messages
}

func afterToolMessagesView(
	req *model.Request,
	llmResponse *model.Response,
	toolResultMessages []model.Message,
) []model.Message {
	var messages []model.Message
	if req != nil && len(req.Messages) > 0 {
		messages = append(messages, req.Messages...)
	}
	if msg, ok := assistantMessageFromToolCallResponse(llmResponse); ok {
		messages = append(messages, msg)
	}
	messages = append(messages, toolResultMessages...)
	return cloneModelMessages(messages)
}

func assistantMessageFromToolCallResponse(
	rsp *model.Response,
) (model.Message, bool) {
	if rsp == nil || len(rsp.Choices) == 0 {
		return model.Message{}, false
	}
	msg := rsp.Choices[0].Message
	if len(msg.ToolCalls) > 0 || model.HasPayload(msg) {
		return msg, true
	}
	delta := rsp.Choices[0].Delta
	if len(delta.ToolCalls) > 0 || model.HasPayload(delta) {
		return delta, true
	}
	return model.Message{}, false
}

func toolCallsFromResponse(rsp *model.Response) []model.ToolCall {
	if rsp == nil || len(rsp.Choices) == 0 {
		return nil
	}
	calls := rsp.Choices[0].Message.ToolCalls
	if len(calls) == 0 {
		calls = rsp.Choices[0].Delta.ToolCalls
	}
	if len(calls) == 0 {
		return nil
	}
	out := make([]model.ToolCall, len(calls))
	copy(out, calls)
	return out
}

func replacementToolChoices(
	originalChoices []model.Choice,
	replacements []model.Message,
) ([]model.Choice, error) {
	original := toolChoiceIndexByID(originalChoices)
	if len(original) == 0 {
		return nil, errors.New("after tool messages: original tool result messages are empty")
	}
	if len(replacements) != len(original) {
		return nil, fmt.Errorf(
			"after tool messages: replacement count %d does not match original tool result count %d",
			len(replacements),
			len(original),
		)
	}
	byID := make(map[string]model.Message, len(replacements))
	for _, msg := range replacements {
		if msg.ToolID == "" {
			return nil, errors.New("after tool messages: replacement tool message missing tool id")
		}
		if msg.Role != model.RoleTool {
			return nil, fmt.Errorf(
				"after tool messages: replacement for tool id %q must use role %q",
				msg.ToolID,
				model.RoleTool,
			)
		}
		if _, ok := byID[msg.ToolID]; ok {
			return nil, fmt.Errorf(
				"after tool messages: replacement contains duplicate tool id %q",
				msg.ToolID,
			)
		}
		byID[msg.ToolID] = msg
	}
	choices := make([]model.Choice, 0, len(original))
	for _, choice := range originalChoices {
		msg := choice.Message
		if msg.ToolID == "" && choice.Delta.ToolID != "" {
			msg = choice.Delta
		}
		if msg.ToolID == "" {
			continue
		}
		replacement, ok := byID[msg.ToolID]
		if !ok {
			return nil, fmt.Errorf(
				"after tool messages: replacement missing tool id %q",
				msg.ToolID,
			)
		}
		choices = append(choices, replaceChoiceToolMessage(choice, replacement))
		delete(byID, msg.ToolID)
	}
	for toolID := range byID {
		return nil, fmt.Errorf(
			"after tool messages: replacement contains unknown tool id %q",
			toolID,
		)
	}
	return choices, nil
}

func replaceChoiceToolMessage(choice model.Choice, msg model.Message) model.Choice {
	updated := choice
	if updated.Message.ToolID != "" {
		updated.Message = msg
	}
	if updated.Delta.ToolID != "" {
		updated.Delta = msg
	}
	if updated.Message.ToolID == "" && updated.Delta.ToolID == "" {
		updated.Message = msg
	}
	return updated
}

func toolChoiceIndexByID(choices []model.Choice) map[string]int {
	out := make(map[string]int, len(choices))
	for _, choice := range choices {
		msg := choice.Message
		if msg.ToolID == "" && choice.Delta.ToolID != "" {
			msg = choice.Delta
		}
		if msg.ToolID == "" {
			continue
		}
		out[msg.ToolID] = choice.Index
	}
	return out
}

func cloneModelMessages(messages []model.Message) []model.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]model.Message, len(messages))
	copy(out, messages)
	return out
}

// executeSingleToolCallSequential runs one tool call and returns its event.
func (p *FunctionCallResponseProcessor) executeSingleToolCallSequential(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
	index int,
	toolCall model.ToolCall,
) (*event.Event, error) {
	result, err := p.executeSingleToolCallSequentialResult(
		ctx,
		invocation,
		llmResponse,
		tools,
		eventChan,
		index,
		toolCall,
	)
	if result.event == nil {
		return nil, err
	}
	toolEvents := p.attachStateDeltaToToolResults(
		ctx,
		invocation,
		[]toolResult{result},
	)
	if len(toolEvents) == 0 {
		return nil, err
	}
	return toolEvents[0], err
}

func (p *FunctionCallResponseProcessor) executeSingleToolCallSequentialResult(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
	index int,
	toolCall model.ToolCall,
) (toolResult, error) {
	ctx, span, startedSpan := itrace.StartSpan(ctx, invocation, itelemetry.NewExecuteToolSpanName(toolCall.Function.Name))
	if startedSpan {
		defer span.End()
	}
	startTime := time.Now()
	ctx, choices, modifiedArgs, shouldIgnoreError, skipSummarization, err :=
		p.executeToolCall(
			ctx,
			invocation,
			toolCall,
			tools,
			index,
			eventChan,
		)
	if err != nil {
		if shouldIgnoreError {
			// Create error choice for ignorable errors
			choice := p.createErrorChoice(index, toolCall.ID, err.Error())
			choices = []model.Choice{*choice}
		} else {
			// Return critical errors (e.g., stop errors) immediately
			return toolResult{}, err
		}
	}
	toolEvent := p.buildToolCallResponseEvent(
		ctx,
		invocation,
		llmResponse,
		choices,
		tools,
		toolCall,
		index,
		modifiedArgs,
		skipSummarization,
	)
	if toolEvent == nil {
		return toolResult{index: index, toolArgs: modifiedArgs}, nil
	}
	decl := p.lookupDeclaration(tools, toolCall.Function.Name)
	var stateDelta *toolEventStateDelta
	if err == nil {
		markSessionAutoMemoryPolluted(
			invocation,
			toolEvent,
			tools[toolCall.Function.Name],
			toolCall.Function.Name,
		)
		stateDelta = p.buildToolEventStateDelta(
			ctx,
			invocation,
			modifiedArgs,
			choices,
		)
	}

	var (
		sess      = &session.Session{}
		modelName string
		agentName string
	)
	if invocation != nil {
		if invocation.Session != nil {
			sess = invocation.Session
		}
		if invocation.Model != nil {
			modelName = invocation.Model.Info().Name
		}
		if invocation.AgentName != "" {
			agentName = invocation.AgentName
		}
	}

	if startedSpan {
		itelemetry.TraceToolCall(span, sess, decl, modifiedArgs, toolEvent, err)
	}
	itelemetry.ReportExecuteToolMetrics(ctx, itelemetry.ExecuteToolAttributes{
		RequestModelName: modelName,
		ToolName:         toolCall.Function.Name,
		AppName:          sess.AppName,
		UserID:           sess.UserID,
		SessionID:        sess.ID,
		AgentName:        agentName,
		Error:            err,
	}, time.Since(startTime))
	return toolResult{
		index:      index,
		event:      toolEvent,
		stateDelta: stateDelta,
		toolArgs:   modifiedArgs,
	}, nil
}

func markSessionAutoMemoryPolluted(
	invocation *agent.Invocation,
	ev *event.Event,
	tl tool.Tool,
	toolName string,
) {
	if !toolPollutesAutoMemory(tl, toolName) {
		return
	}
	value := []byte(memory.MemoryModePolluted)
	if invocation != nil && invocation.Session != nil {
		invocation.Session.SetState(memory.SessionStateKeyMemoryMode, value)
	}
	if ev != nil {
		if ev.StateDelta == nil {
			ev.StateDelta = make(map[string][]byte)
		}
		ev.StateDelta[memory.SessionStateKeyMemoryMode] = value
	}
}

func toolPollutesAutoMemory(tl tool.Tool, name string) bool {
	if toolCapabilityPollutesAutoMemory(tl) {
		return true
	}
	return toolNamePollutesAutoMemory(name)
}

func toolCapabilityPollutesAutoMemory(tl tool.Tool) bool {
	// Traverse both NamedTool (Original) and transparent wrappers such as
	// resultcodec.Wrap (TransparentUnwrap) so wrapping a knowledge tool does not
	// hide its PollutesAutoMemory capability. Depth-bounded for cycle safety.
	//
	// Fail closed: if the chain cannot be fully traversed (a self-cycle or deeper
	// than the bound), a pollution source may be hidden past the limit, so treat
	// the tool as polluting rather than risk persisting external content into
	// automatic memory. A fully traversed chain with no source is not polluting.
	for i := 0; i < maxToolWrapperTraversalDepth && tl != nil; i++ {
		if source, ok := tl.(autoMemoryPollutionSource); ok && source.PollutesAutoMemory() {
			return true
		}
		next := unwrapAutoMemoryTool(tl)
		if next == nil {
			return false
		}
		if next == tl {
			return true
		}
		tl = next
	}
	return true
}

// maxToolWrapperTraversalDepth bounds wrapper-chain traversal for capability
// discovery so a cyclic wrapper cannot loop forever.
const maxToolWrapperTraversalDepth = 128

// unwrapAutoMemoryTool returns the next tool in the wrapper chain, following
// NamedTool (Original) and explicitly transparent wrappers (TransparentUnwrap).
func unwrapAutoMemoryTool(tl tool.Tool) tool.Tool {
	if wrapper, ok := tl.(originalToolProvider); ok {
		if original := wrapper.Original(); original != nil {
			return original
		}
	}
	if wrapper, ok := tl.(interface{ TransparentUnwrap() tool.Tool }); ok {
		return wrapper.TransparentUnwrap()
	}
	return nil
}

func toolNamePollutesAutoMemory(name string) bool {
	switch name {
	case knowledgeSearchToolName, knowledgeSearchWithAgenticFilterName:
		return true
	default:
		return strings.HasSuffix(name, knowledgeSearchToolNameSuffix) ||
			strings.HasSuffix(name, knowledgeSearchWithAgenticFilterNameSuffix)
	}
}

// executeToolCallsInParallel runs multiple tool calls concurrently and merges
// their results into a single event.
//
// Concurrency model: each tool call is dispatched on its own goroutine via an
// [errgroup.Group]. The group ctx is derived from the parent ctx so that a
// parent-side cancellation (e.g. agent timeout) cancels every sibling
// immediately. When a sibling reports a *critical* (non-ignorable) tool
// execution error, the group ctx is also cancelled and the remaining
// siblings observe `ctx.Done()` and stop early instead of burning compute on
// work whose result will never be consumed. Panics inside a tool execution
// are recovered locally and surfaced as a tool error in the merged response;
// they do NOT cancel sibling goroutines. Normal tool errors that callbacks
// flag as "ignorable" are likewise non-cancelling.
func (p *FunctionCallResponseProcessor) executeToolCallsInParallel(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	toolCalls []model.ToolCall,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
) (*event.Event, error) {
	resultChan := make(chan toolResult, len(toolCalls))

	g, gctx := errgroup.WithContext(ctx)
	for i, tc := range toolCalls {
		i, tc := i, tc
		g.Go(func() error {
			runCtx := agent.CloneContext(gctx)
			runInv := invocation
			if invocation != nil {
				runInv = newParallelInvocationView(invocation)
				if runCtx == nil {
					runCtx = context.Background()
				}
				runCtx = agent.NewInvocationContext(runCtx, runInv)
			}
			return p.runParallelToolCall(
				runCtx, runInv, llmResponse, tools, eventChan, resultChan, i, tc,
			)
		})
	}

	// Wait for all siblings to finish in a separate goroutine so the
	// collector can drain results as they arrive. Closing resultChan is
	// what signals "no more results" to collectParallelToolResults.
	// errgroup.Wait is safe to call multiple times — it returns the same
	// stored error — so racing with the post-collect Wait below is OK.
	go func() {
		_ = g.Wait()
		close(resultChan)
	}()

	// Drain results in arrival order, preserving slot index. Use the
	// parent ctx (not gctx) here: gctx is cancelled automatically when
	// errgroup.Wait returns, so reading on gctx would race with the normal
	// "all siblings done, channel closed" path and falsely report a
	// cancellation on every successful run. The parent ctx is the right
	// signal for "the caller has abandoned this work".
	toolResults, drainErr := p.collectParallelToolResults(
		ctx, resultChan, len(toolCalls),
	)

	// Read the first critical sibling error from the group; prefer it over
	// the collector's view because it carries the causal failure (the
	// collector typically just sees ctx.Done()).
	err := firstNonNilErr(g.Wait(), drainErr)

	toolCallResponsesEvents := p.attachStateDeltaToToolResults(
		ctx,
		invocation,
		toolResults,
	)
	if len(toolCallResponsesEvents) == 0 && invocation != nil {
		for _, tc := range toolCalls {
			tl, ok := tools[tc.Function.Name]
			if ok && !invocation.RunOptions.ShouldExecuteTool(ctx, tl) {
				return nil, nil
			}
		}
	}
	mergedEvent := p.buildMergedParallelEvent(
		ctx, invocation, llmResponse, tools, toolCalls, toolResults,
		toolCallResponsesEvents,
	)
	return mergedEvent, err
}

// firstNonNilErr returns the first non-nil error from its arguments, in
// order. Used to prefer the group-level critical error (which carries the
// causal failure) over a derived collector error.
func firstNonNilErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// runParallelToolCall executes one tool call and reports the result.
//
// The returned error is non-nil only when the failure is critical enough
// that sibling goroutines should be cancelled — i.e. the underlying call
// returned a non-ignorable error. All other outcomes — successes, ignorable
// errors, and recovered panics — return nil so the errgroup keeps remaining
// siblings running. The same "critical" classification is also propagated
// through toolResult.err so it flows into the merged event as before.
func (p *FunctionCallResponseProcessor) runParallelToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	eventChan chan<- *event.Event,
	resultChan chan<- toolResult,
	index int,
	tc model.ToolCall,
) (rerr error) {
	toolArgs := tc.Function.Arguments
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithValue(ctx, executingToolArgsContextKey{}, &toolArgs)
	// Recover from panics to avoid breaking sibling goroutines. Panics are
	// surfaced as a tool error in the merged response (no sibling cancel).
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(
				ctx,
				log.PanicPrefix+" Tool execution panic for %s (index: %d, ID: %s, agent: %s): %v",
				tc.Function.Name,
				index,
				tc.ID,
				invocation.AgentName,
				r,
			)
			errorChoice := p.createErrorChoice(
				index, tc.ID, fmt.Sprintf("tool execution panic: %v", r),
			)
			errorChoice.Message.ToolName = tc.Function.Name
			errorEvent := newToolCallResponseEvent(
				invocation, llmResponse, []model.Choice{*errorChoice},
			)
			annotateToolCallArgs(errorEvent, tc, toolArgs)
			if tc.Function.Name == transfer.TransferToolName {
				errorEvent.Tag = event.TransferTag
			}
			p.sendToolResult(ctx, resultChan, toolResult{
				index:    index,
				event:    errorEvent,
				toolArgs: toolArgs,
			})
			// Recovered panic — do NOT cancel siblings.
			rerr = nil
		}
	}()
	// Trace the tool execution for observability.
	ctx, span, startedSpan := itrace.StartSpan(ctx, invocation, itelemetry.NewExecuteToolSpanName(tc.Function.Name))
	if startedSpan {
		defer span.End()
	}
	startTime := time.Now()
	// Execute the tool (streamable or callable) with callbacks.
	ctx, choices, modifiedArgs, shouldIgnoreError, skipSummarization, err :=
		p.executeToolCall(
			ctx,
			invocation,
			tc,
			tools,
			index,
			eventChan,
		)
	toolArgs = modifiedArgs
	// Handle errors based on whether they are ignorable or critical.
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Tool execution error for %s (index: %d, ID: %s, agent: %s): %v",
			tc.Function.Name,
			index,
			tc.ID,
			invocation.AgentName,
			err,
		)
		errorChoice := p.createErrorChoice(
			index, tc.ID, fmt.Sprintf("tool execution error: %v", err),
		)
		errorChoice.Message.ToolName = tc.Function.Name
		errorEvent := newToolCallResponseEvent(
			invocation, llmResponse, []model.Choice{*errorChoice},
		)
		annotateToolCallArgs(errorEvent, tc, modifiedArgs)
		errorEvent = p.decorateToolCallResponseEvent(
			errorEvent,
			tools,
			tc,
			skipSummarization,
			!shouldSkipToolSkipSummarization(ctx),
		)
		// Only propagate the error if it's not ignorable (e.g., stop errors)
		var returnErr error
		if !shouldIgnoreError {
			returnErr = err
		}
		p.sendToolResult(ctx, resultChan, toolResult{
			index:    index,
			event:    errorEvent,
			err:      returnErr,
			toolArgs: modifiedArgs,
		})
		// Return the critical error so the errgroup cancels siblings.
		// Ignorable errors return nil here and travel only via toolResult.err.
		return returnErr
	}

	// No error and at least one choice means we have tool result messages.
	toolCallResponseEvent := p.buildToolCallResponseEvent(
		ctx,
		invocation,
		llmResponse,
		choices,
		tools,
		tc,
		index,
		modifiedArgs,
		skipSummarization,
	)
	if toolCallResponseEvent == nil {
		p.sendToolResult(ctx, resultChan, toolResult{
			index:    index,
			toolArgs: modifiedArgs,
		})
		return nil
	}
	// Include declaration for telemetry even when tool is missing.
	decl := p.lookupDeclaration(tools, tc.Function.Name)

	var (
		sess      = &session.Session{}
		modelName string
		agentName string
	)
	if invocation != nil {
		if invocation.Session != nil {
			sess = invocation.Session
		}
		if invocation.Model != nil {
			modelName = invocation.Model.Info().Name
		}
		if invocation.AgentName != "" {
			agentName = invocation.AgentName
		}
	}
	markSessionAutoMemoryPolluted(
		invocation,
		toolCallResponseEvent,
		tools[tc.Function.Name],
		tc.Function.Name,
	)
	stateDelta := p.buildToolEventStateDelta(
		ctx,
		invocation,
		modifiedArgs,
		choices,
	)
	if startedSpan {
		itelemetry.TraceToolCall(span, sess, decl, modifiedArgs, toolCallResponseEvent, err)
	}
	itelemetry.ReportExecuteToolMetrics(ctx, itelemetry.ExecuteToolAttributes{
		RequestModelName: modelName,
		ToolName:         tc.Function.Name,
		AppName:          sess.AppName,
		UserID:           sess.UserID,
		SessionID:        sess.ID,
		AgentName:        agentName,
		Error:            err,
	}, time.Since(startTime))
	// Send result back to aggregator.
	p.sendToolResult(
		ctx, resultChan, toolResult{
			index:      index,
			event:      toolCallResponseEvent,
			stateDelta: stateDelta,
			toolArgs:   modifiedArgs,
		},
	)
	return nil
}

func (p *FunctionCallResponseProcessor) buildToolCallResponseEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	choices []model.Choice,
	tools map[string]tool.Tool,
	toolCall model.ToolCall,
	index int,
	toolArgs []byte,
	skipSummarization bool,
) *event.Event {
	if len(choices) == 0 {
		if !skipSummarization {
			return nil
		}
		return p.decorateToolCallResponseEvent(
			newMinimalToolCallResponseEvent(
				invocation,
				llmResponse,
				toolCall,
				index,
				toolArgs,
			),
			tools,
			toolCall,
			skipSummarization,
			!shouldSkipToolSkipSummarization(ctx),
		)
	}
	annotateToolChoicesWithName(choices, toolCall.Function.Name)
	ev := newToolCallResponseEvent(invocation, llmResponse, choices)
	annotateToolCallArgs(ev, toolCall, toolArgs)
	return p.decorateToolCallResponseEvent(
		ev,
		tools,
		toolCall,
		skipSummarization,
		!shouldSkipToolSkipSummarization(ctx),
	)
}

func annotateToolChoicesWithName(choices []model.Choice, toolName string) {
	for i := range choices {
		if choices[i].Message.Role == model.RoleTool &&
			choices[i].Message.ToolName == "" {
			choices[i].Message.ToolName = toolName
		}
	}
}

func annotateToolCallArgs(
	ev *event.Event,
	toolCall model.ToolCall,
	toolArgs []byte,
) {
	if toolArgs == nil {
		toolArgs = toolCall.Function.Arguments
	}
	if err := setToolCallArgs(ev, toolCall.ID, toolArgs); err != nil {
		log.Warnf("Failed to set tool call args extension: %v", err)
	}
}

func setToolCallArgs(
	ev *event.Event,
	toolCallID string,
	toolArgs []byte,
) error {
	if ev == nil || toolCallID == "" {
		return nil
	}
	args, _, err := event.GetExtension[map[string]string](
		ev,
		event.ToolCallArgsExtensionKey,
	)
	if err != nil {
		return err
	}
	if args == nil {
		args = make(map[string]string)
	}
	args[toolCallID] = string(toolArgs)
	return event.SetExtension(
		ev,
		event.ToolCallArgsExtensionKey,
		args,
	)
}

func (p *FunctionCallResponseProcessor) decorateToolCallResponseEvent(
	ev *event.Event,
	tools map[string]tool.Tool,
	toolCall model.ToolCall,
	skipSummarization bool,
	allowToolSkipSummarization bool,
) *event.Event {
	if ev == nil {
		return nil
	}
	if toolCall.Function.Name == transfer.TransferToolName {
		ev.Tag = event.TransferTag
	}
	if tl, ok := tools[toolCall.Function.Name]; ok {
		p.annotateSkipSummarization(
			ev,
			tl,
			skipSummarization,
			allowToolSkipSummarization,
		)
	} else if skipSummarization {
		markSkipSummarization(ev)
	}
	return ev
}

type stateDeltaProvider interface {
	StateDelta(string, []byte, []byte) map[string][]byte
}

type invocationStateDeltaProvider interface {
	StateDeltaForInvocation(
		*agent.Invocation,
		string,
		[]byte,
		[]byte,
	) map[string][]byte
}

// toolProvidesStateDelta reports whether the tool (through the wrapper chain)
// contributes a state delta from its result content.
func toolProvidesStateDelta(tl tool.Tool) bool {
	semantic := itool.ResolveSemantic(tl)
	if _, ok := semantic.(invocationStateDeltaProvider); ok {
		return true
	}
	_, ok := semantic.(stateDeltaProvider)
	return ok
}

// marshalStateDeltaContent serializes result to the JSON bytes used as the
// state-delta input, under panic protection. It is only called for stateful
// tools; a Custom codec may wrap non-JSON-friendly results, so a failure here is
// returned to the caller, which fails the call rather than silently dropping the
// state delta.
func marshalStateDeltaContent(result any) (b []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("state delta content marshal panic: %v", r)
		}
	}()
	return marshalJSONNoHTMLEscape(result)
}

// attachStateDelta copies tool-provided state delta to the event. stateContent
// is the JSON-encoded tool result used as the state-delta input; when nil the
// choice message content is used (the default JSON behavior). This keeps state
// deltas on JSON even when a result codec changes the model-visible message.
func (p *FunctionCallResponseProcessor) attachStateDelta(
	inv *agent.Invocation,
	tl tool.Tool,
	args []byte,
	stateContent []byte,
	choice *model.Choice,
	ev *event.Event,
) {
	if tl == nil || choice == nil || ev == nil {
		return
	}
	b := stateContent
	if b == nil {
		b = []byte(choice.Message.Content)
	}
	toolCallID := choice.Message.ToolID

	var delta map[string][]byte
	providerTool := itool.ResolveSemantic(tl)
	if isdp, ok := providerTool.(invocationStateDeltaProvider); ok {
		delta = isdp.StateDeltaForInvocation(inv, toolCallID, args, b)
	} else if sdp, ok := providerTool.(stateDeltaProvider); ok {
		delta = sdp.StateDelta(toolCallID, args, b)
	} else {
		return
	}
	if len(delta) == 0 {
		return
	}
	if ev.StateDelta == nil {
		ev.StateDelta = map[string][]byte{}
	}
	for k, v := range delta {
		ev.StateDelta[k] = v
	}
}

// annotateSkipSummarization marks an event to skip outer summarization.
func (p *FunctionCallResponseProcessor) annotateSkipSummarization(
	ev *event.Event,
	tl tool.Tool,
	dynamic bool,
	allowToolPreference bool,
) {
	if dynamic || (allowToolPreference && toolPrefersSkipSummarization(tl)) {
		markSkipSummarization(ev)
	}
}

func (p *FunctionCallResponseProcessor) buildToolEventStateDelta(
	ctx context.Context,
	invocation *agent.Invocation,
	args []byte,
	choices []model.Choice,
) *toolEventStateDelta {
	if len(choices) == 0 ||
		hasSyntheticStateOnlyToolChoice(ctx) ||
		shouldSkipToolStateDelta(ctx) {
		return nil
	}
	tl, ok := resolvedToolFromContext(ctx)
	if !ok {
		return nil
	}
	stateDeltaInv := newStateDeltaSnapshot(ctx, invocation)
	return &toolEventStateDelta{
		tool:            tl,
		invocation:      stateDeltaInv,
		sessionBaseline: stateDeltaSessionBaseline(ctx),
		args:            args,
		choice:          choices[0],
		content:         stateDeltaContentFromContext(ctx),
	}
}

func newStateDeltaSnapshot(
	ctx context.Context,
	invocation *agent.Invocation,
) *agent.Invocation {
	if ctx == nil {
		return cloneStateDeltaSession(invocationView(invocation))
	}
	ctxInv, hasCtxInv := agent.InvocationFromContext(ctx)
	if !hasCtxInv || ctxInv == nil {
		return cloneStateDeltaSession(invocationView(invocation))
	}
	if invocation == nil || ctxInv == invocation {
		return cloneStateDeltaSession(invocationView(ctxInv))
	}
	view := ctxInv.View()
	preserveStateDeltaInvocationDefaults(view, invocation)
	return cloneStateDeltaSession(view)
}

func newParallelInvocationView(
	invocation *agent.Invocation,
) *agent.Invocation {
	return cloneStateDeltaSession(invocationView(invocation))
}

func invocationView(invocation *agent.Invocation) *agent.Invocation {
	if invocation == nil {
		return nil
	}
	return invocation.View()
}

func cloneStateDeltaSession(invocation *agent.Invocation) *agent.Invocation {
	if invocation != nil && invocation.Session != nil {
		invocation.Session = invocation.Session.Clone()
	}
	return invocation
}

func withStateDeltaSessionBaseline(
	ctx context.Context,
	invocation *agent.Invocation,
) context.Context {
	if ctx == nil {
		return nil
	}
	var baseline session.StateMap
	baselineSession := stateDeltaBaselineSession(ctx, invocation)
	if baselineSession != nil {
		baseline = baselineSession.SnapshotState()
	}
	return context.WithValue(
		ctx,
		stateDeltaSessionBaselineContextKey{},
		baseline,
	)
}

func stateDeltaBaselineSession(
	ctx context.Context,
	invocation *agent.Invocation,
) *session.Session {
	if ctx != nil {
		if ctxInv, ok := agent.InvocationFromContext(ctx); ok &&
			ctxInv != nil &&
			ctxInv.Session != nil {
			return ctxInv.Session
		}
	}
	if invocation == nil {
		return nil
	}
	return invocation.Session
}

func stateDeltaSessionBaseline(ctx context.Context) session.StateMap {
	if ctx == nil {
		return nil
	}
	baseline, _ := ctx.Value(
		stateDeltaSessionBaselineContextKey{},
	).(session.StateMap)
	return baseline
}

func preserveStateDeltaInvocationDefaults(
	view *agent.Invocation,
	base *agent.Invocation,
) {
	if view == nil || base == nil {
		return
	}
	view.Agent = base.Agent
	view.AgentName = base.AgentName
	view.InvocationID = base.InvocationID
	view.Branch = base.Branch
	view.Model = base.Model
	view.Message = base.Message
	view.RunOptions = base.RunOptions
	view.TransferInfo = base.TransferInfo
	view.Plugins = base.Plugins
	view.StructuredOutput = base.StructuredOutput
	view.StructuredOutputType = base.StructuredOutputType
	view.MemoryService = base.MemoryService
	view.MemoryReader = base.MemoryReader
	view.ArtifactService = base.ArtifactService
	view.MaxLLMCalls = base.MaxLLMCalls
	view.MaxToolIterations = base.MaxToolIterations
	if view.Session == nil {
		view.Session = base.Session
	}
	if view.SessionService == nil {
		view.SessionService = base.SessionService
	}
}

func newStateDeltaInvocationView(
	invocation *agent.Invocation,
) *agent.Invocation {
	if invocation == nil {
		return nil
	}
	view := invocation.View()
	if invocation.Session != nil {
		view.Session = invocation.Session.Clone()
	}
	return view
}

func withResolvedToolContext(
	ctx context.Context,
	tl tool.Tool,
) context.Context {
	if ctx == nil || tl == nil {
		return ctx
	}
	return context.WithValue(ctx, resolvedToolContextKey{}, tl)
}

func resolvedToolFromContext(ctx context.Context) (tool.Tool, bool) {
	if ctx == nil {
		return nil, false
	}
	tl, ok := ctx.Value(resolvedToolContextKey{}).(tool.Tool)
	return tl, ok
}

func withSkippedToolStateDelta(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, skipToolStateDeltaContextKey{}, true)
}

func shouldSkipToolStateDelta(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	skip, _ := ctx.Value(skipToolStateDeltaContextKey{}).(bool)
	return skip
}

func withSkippedToolSkipSummarization(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, skipToolSkipSummarizationContextKey{}, true)
}

func shouldSkipToolSkipSummarization(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	skip, _ := ctx.Value(skipToolSkipSummarizationContextKey{}).(bool)
	return skip
}

func (p *FunctionCallResponseProcessor) attachStateDeltaToToolResults(
	ctx context.Context,
	invocation *agent.Invocation,
	results []toolResult,
) []*event.Event {
	var priorStateDelta []*event.Event
	events := make([]*event.Event, 0, len(results))
	for i := 0; i < len(results); i++ {
		result := results[i]
		if result.event == nil {
			continue
		}
		if result.stateDelta != nil {
			stateDeltaInv := stateDeltaInvocationView(
				invocation,
				result.stateDelta,
			)
			if stateDeltaInv != nil && stateDeltaInv.Session != nil {
				applyPriorStateDeltas(
					stateDeltaInv.Session,
					result.stateDelta.sessionBaseline,
					priorStateDelta,
				)
			}
			p.attachStateDelta(
				stateDeltaInv,
				result.stateDelta.tool,
				result.stateDelta.args,
				result.stateDelta.content,
				&result.stateDelta.choice,
				result.event,
			)
		}
		p.runPostToolResultHooks(ctx, invocation, result.event)
		if len(result.event.StateDelta) > 0 {
			priorStateDelta = append(
				priorStateDelta,
				result.event,
			)
		}
		events = append(events, result.event)
	}
	return events
}

func (p *FunctionCallResponseProcessor) runPostToolResultHooks(
	ctx context.Context,
	invocation *agent.Invocation,
	ev *event.Event,
) {
	for _, hook := range p.postToolResultHooks {
		hook(ctx, invocation, ev)
	}
}

func applyPriorStateDeltas(
	sess *session.Session,
	baseline session.StateMap,
	events []*event.Event,
) {
	if sess == nil || len(events) == 0 {
		return
	}
	changed := changedSessionKeys(baseline, sess.SnapshotState())
	for i := 0; i < len(events); i++ {
		applyReplayableStateDelta(sess, changed, events[i])
	}
}

func changedSessionKeys(
	baseline session.StateMap,
	current session.StateMap,
) map[string]bool {
	changed := map[string]bool{}
	for key, currentValue := range current {
		baselineValue, ok := baseline[key]
		if !ok || !bytes.Equal(currentValue, baselineValue) {
			changed[key] = true
		}
	}
	for key := range baseline {
		if _, ok := current[key]; !ok {
			changed[key] = true
		}
	}
	return changed
}

func applyReplayableStateDelta(
	sess *session.Session,
	changed map[string]bool,
	e *event.Event,
) {
	if e == nil {
		return
	}
	for key, value := range e.StateDelta {
		if changed[key] {
			continue
		}
		sess.SetState(key, value)
	}
}

func stateDeltaInvocationView(
	invocation *agent.Invocation,
	stateDelta *toolEventStateDelta,
) *agent.Invocation {
	if stateDelta != nil && stateDelta.invocation != nil {
		return stateDelta.invocation.View()
	}
	return newStateDeltaInvocationView(invocation)
}

// lookupDeclaration returns a declaration or a safe placeholder.
func (p *FunctionCallResponseProcessor) lookupDeclaration(
	tools map[string]tool.Tool, name string,
) *tool.Declaration {
	if tl, ok := tools[name]; ok {
		return tl.Declaration()
	}
	return &tool.Declaration{Name: "<not found>", Description: "<not found>"}
}

// sendToolResult sends without blocking when the context is cancelled.
func (p *FunctionCallResponseProcessor) sendToolResult(
	ctx context.Context, ch chan<- toolResult, res toolResult,
) {
	select {
	case ch <- res:
	case <-ctx.Done():
	}
}

// buildMergedParallelEvent merges child tool events or builds minimal choices.
func (p *FunctionCallResponseProcessor) buildMergedParallelEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	llmResponse *model.Response,
	tools map[string]tool.Tool,
	toolCalls []model.ToolCall,
	toolResults []toolResult,
	toolCallEvents []*event.Event,
) *event.Event {
	toolArgsByIndex := make(map[int][]byte, len(toolResults))
	for _, result := range toolResults {
		if result.index >= 0 && result.index < len(toolCalls) &&
			result.toolArgs != nil {
			toolArgsByIndex[result.index] = result.toolArgs
		}
	}

	var mergedEvent *event.Event
	if len(toolCallEvents) == 0 {
		minimal := make([]model.Choice, 0, len(toolCalls))
		for i, tc := range toolCalls {
			minimal = append(minimal, newMinimalToolChoice(tc, i))
		}
		mergedEvent = newToolCallResponseEvent(invocation, llmResponse, minimal)
		for _, tc := range toolCalls {
			if tl, ok := tools[tc.Function.Name]; ok &&
				toolPrefersSkipSummarization(tl) {
				markSkipSummarization(mergedEvent)
				break
			}
		}
	} else {
		mergedEvent = mergeParallelToolCallResponseEvents(toolCallEvents)
	}
	for i, tc := range toolCalls {
		annotateToolCallArgs(mergedEvent, tc, toolArgsByIndex[i])
	}
	if len(toolCallEvents) > 1 {
		_, span, startedSpan := itrace.StartSpan(
			ctx,
			invocation,
			itelemetry.NewExecuteToolSpanName(itelemetry.ToolNameMergedTools),
		)
		if startedSpan {
			itelemetry.TraceMergedToolCalls(span, mergedEvent)
			span.End()
		}
	}
	return mergedEvent
}

// executeToolCall executes a single tool call and returns the choice.
// Parameters:
//   - ctx: context for cancellation and tracing
//   - invocation: agent invocation context containing agent name, model info, etc.
//   - toolCall: the tool call to execute, including function name and arguments
//   - tools: map of available tools by name
//   - index: index of this tool call in the batch (for error reporting)
//   - eventChan: channel for emitting events during execution
//
// Returns:
//   - context.Context: updated context from callbacks (if any)
//   - []model.Choice: tool response choices (nil if no response is emitted)
//   - []byte: the modified arguments after before-tool callbacks (for telemetry)
//   - bool: shouldIgnoreError - true if the error is ignorable (e.g., tool not found, marshal error), false for critical errors (e.g., stop errors)
//   - bool: skipSummarization - true if callbacks requested ending the turn
//     after the tool response
//   - error: any error that occurred during execution (no longer swallowed)
func (p *FunctionCallResponseProcessor) executeToolCall(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
	index int,
	eventChan chan<- *event.Event,
) (context.Context, []model.Choice, []byte, bool, bool, error) {
	toolCall, tl, shouldIgnoreError, err := p.resolveToolCallTarget(ctx, invocation, toolCall, tools)
	if err != nil || tl == nil {
		return ctx, nil, toolCall.Function.Arguments, shouldIgnoreError,
			false, err
	}

	log.DebugfContext(
		ctx,
		"Executing tool %s with args: %s",
		toolCall.Function.Name,
		string(toolCall.Function.Arguments),
	)

	// Execute the tool with callbacks.
	ctx, result, modifiedArgs, suppressDefaultToolMessage,
		skipSummarization, err := p.executeToolWithCallbacks(
		ctx,
		invocation,
		toolCall,
		tl,
		eventChan,
	)
	ctx = withResolvedToolContext(ctx, tl)
	// Only return error when it's a stop error
	if err != nil {
		if _, ok := agent.AsStopError(err); ok {
			return ctx, nil, modifiedArgs, false, skipSummarization, err
		}
		return ctx, nil, modifiedArgs, true, skipSummarization, err
	}
	//  allow to return nil not provide function response.
	if r, ok := itool.ResolveDeclaration(tl).(function.LongRunner); ok && r.LongRunning() {
		if result == nil {
			return ctx, nil, modifiedArgs, true, skipSummarization, nil
		}
	}
	codec := itool.ResolveResultCodec(tl)
	if isPermissionResult(result) {
		// Permission results are framework control protocol, not normal tool
		// output. Never run the tool's result codec on them so denied and
		// approval-required messages keep their default encoding.
		codec = nil
	}
	if codec != nil && toolProvidesStateDelta(tl) {
		// A result codec only changes the model-visible message. Stateful tools
		// compute their state delta from the tool result content, which must stay
		// JSON so JSON-parsing tools are not broken by the codec. Only stateful
		// tools need this, so the extra marshal is avoided for every other
		// codec'd call. If the result cannot be serialized to JSON (a Custom
		// codec may accept non-JSON results), the state delta cannot be produced;
		// fail the call rather than silently succeed and drop the state update,
		// matching the default (no-codec) behavior where a marshal failure fails
		// the tool result.
		stateContent, jerr := marshalStateDeltaContent(result)
		if jerr != nil {
			log.WarnfContext(
				ctx,
				"Failed to serialize state delta content for %s: %v",
				toolCall.Function.Name,
				jerr,
			)
			return ctx, nil, modifiedArgs, true, skipSummarization,
				fmt.Errorf("%s: %w", ErrorMarshalResult, jerr)
		}
		ctx = withStateDeltaContent(ctx, stateContent)
	}
	if suppressDefaultToolMessage {
		defaultMsg, err := buildDefaultToolMessage(ctx, toolCall.ID, result, codec)
		if err != nil {
			log.WarnfContext(
				ctx,
				"Failed to encode tool result for %s: %v",
				toolCall.Function.Name,
				err,
			)
			return ctx, nil, modifiedArgs, true, skipSummarization, err
		}
		defaultMsg.ToolName = toolCall.Function.Name
		ctx = markSyntheticStateOnlyToolChoice(ctx)
		choices, cbErr := p.buildToolResultChoices(
			ctx,
			toolCall,
			tl,
			result,
			modifiedArgs,
			index,
			defaultMsg,
		)
		if cbErr != nil {
			return ctx, nil, modifiedArgs, true, skipSummarization, cbErr
		}
		return ctx, choices, modifiedArgs, true,
			skipSummarization, nil
	}

	defaultMsg, err := buildDefaultToolMessage(ctx, toolCall.ID, result, codec)
	if err != nil {
		// Marshal/encode failures (for example, NaN in floats) do not
		// affect the overall flow. Downgrade to warning to avoid
		// noisy alerts while still surfacing the issue.
		log.WarnfContext(
			ctx,
			"Failed to encode tool result for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, nil, modifiedArgs, true, skipSummarization, err
	}
	defaultMsg.ToolName = toolCall.Function.Name

	choices, cbErr := p.buildToolResultChoices(
		ctx,
		toolCall,
		tl,
		result,
		modifiedArgs,
		index,
		defaultMsg,
	)
	if cbErr != nil {
		return ctx, nil, modifiedArgs, true, skipSummarization, cbErr
	}

	log.DebugfContext(
		ctx,
		"CallableTool %s executed successfully, result: %s",
		toolCall.Function.Name,
		defaultMsg.Content,
	)

	return ctx, choices, modifiedArgs, true, skipSummarization, nil
}

func (p *FunctionCallResponseProcessor) buildToolResultChoices(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.Tool,
	result any,
	modifiedArgs []byte,
	index int,
	defaultMsg model.Message,
) ([]model.Choice, error) {
	defaultChoices := []model.Choice{
		{Index: index, Message: defaultMsg},
	}
	if isPermissionResult(result) ||
		p.toolCallbacks == nil ||
		p.toolCallbacks.ToolResultMessages == nil {
		return defaultChoices, nil
	}
	customChoices, overridden, err := p.applyToolResultMessagesCallback(
		ctx,
		toolCall,
		tl,
		result,
		modifiedArgs,
		index,
		defaultMsg,
	)
	if err != nil {
		return nil, err
	}
	if overridden {
		return customChoices, nil
	}
	return defaultChoices, nil
}

func isPermissionResult(result any) bool {
	switch v := result.(type) {
	case tool.PermissionResult:
		return isPermissionResultStatus(v.Status)
	case *tool.PermissionResult:
		return v != nil && isPermissionResultStatus(v.Status)
	default:
		return false
	}
}

func isPermissionResultStatus(status string) bool {
	switch status {
	case tool.PermissionResultStatusDenied,
		tool.PermissionResultStatusApprovalRequired:
		return true
	default:
		return false
	}
}

// resolveToolCallTarget resolves the callable tool, applies compatibility remapping,
// and evaluates the optional tool execution filter.
func (p *FunctionCallResponseProcessor) resolveToolCallTarget(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tools map[string]tool.Tool,
) (model.ToolCall, tool.Tool, bool, error) {
	tl, exists := tools[toolCall.Function.Name]
	if !exists {
		// Compatibility: map sub-agent name calls to transfer_to_agent if present.
		if mapped := findCompatibleTool(toolCall.Function.Name, tools, invocation); mapped != nil {
			tl = mapped
			if newArgs := convertToolArguments(
				invocation,
				toolCall.Function.Name, toolCall.Function.Arguments,
				mapped.Declaration().Name,
			); newArgs != nil {
				toolCall.Function.Name = mapped.Declaration().Name
				toolCall.Function.Arguments = newArgs
			}
		} else {
			toolNotFoundErr := toolNotFoundError(
				toolCall.Function.Name,
				tools,
				p.toolNameSuggestionOptions,
			)
			log.ErrorfContext(
				ctx,
				"CallableTool %s not found (agent=%s, model=%s)",
				toolCall.Function.Name,
				invocation.AgentName,
				invocation.Model.Info().Name,
			)
			return toolCall, nil, true, fmt.Errorf(
				"executeToolCall: %s",
				toolNotFoundErr,
			)
		}
	}
	if invocation != nil &&
		!invocation.RunOptions.ShouldExecuteTool(ctx, tl) {
		return toolCall, nil, true, nil
	}
	return toolCall, tl, false, nil
}

func defaultToolNameSuggestionOptions() toolNameSuggestionOptions {
	return toolNameSuggestionOptions{
		maxSuggestions: maxToolNameSuggestions,
		maxDistance:    maxToolNameDistance,
	}
}

func toolNotFoundError(
	name string,
	tools map[string]tool.Tool,
	options toolNameSuggestionOptions,
) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrorToolNotFound
	}
	suggestions := similarToolNames(name, tools, options)
	displayName := displayToolName(name)
	if len(suggestions) == 0 {
		return fmt.Sprintf("%s: %s", ErrorToolNotFound, displayName)
	}
	if len(suggestions) == 1 {
		return fmt.Sprintf(
			"%s: %s; did you mean %q?",
			ErrorToolNotFound,
			displayName,
			suggestions[0],
		)
	}
	return fmt.Sprintf(
		"%s: %s; did you mean one of %s?",
		ErrorToolNotFound,
		displayName,
		quotedToolNames(suggestions),
	)
}

func similarToolNames(
	name string,
	tools map[string]tool.Tool,
	options toolNameSuggestionOptions,
) []string {
	if options.maxSuggestions <= 0 || options.maxDistance < 0 {
		return nil
	}
	type candidate struct {
		name     string
		distance int
	}
	needle := strings.ToLower(strings.TrimSpace(name))
	candidates := make([]candidate, 0, len(tools))
	for toolName := range tools {
		trimmed := strings.TrimSpace(toolName)
		if trimmed == "" {
			continue
		}
		distance := toolNameEditDistance(
			needle,
			strings.ToLower(trimmed),
		)
		if distance <= options.maxDistance {
			candidates = append(candidates, candidate{
				name:     trimmed,
				distance: distance,
			})
			continue
		}
		if strings.Contains(needle, strings.ToLower(trimmed)) {
			candidates = append(candidates, candidate{
				name:     trimmed,
				distance: options.maxDistance + 1,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].distance != candidates[j].distance {
			return candidates[i].distance < candidates[j].distance
		}
		return candidates[i].name < candidates[j].name
	})
	limit := len(candidates)
	if limit > options.maxSuggestions {
		limit = options.maxSuggestions
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, candidates[i].name)
	}
	return out
}

func displayToolName(name string) string {
	name = collapseToolNameWhitespace(name)
	runes := []rune(name)
	if len(runes) <= maxToolNameErrorNameRunes {
		return name
	}
	return string(runes[:maxToolNameErrorNameRunes]) + "..."
}

func collapseToolNameWhitespace(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

func quotedToolNames(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, fmt.Sprintf("%q", name))
	}
	return strings.Join(quoted, ", ")
}

func toolNameEditDistance(a string, b string) int {
	if a == b {
		return 0
	}
	ar := []rune(a)
	br := []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ca := range ar {
		curr[0] = i + 1
		for j, cb := range br {
			cost := 1
			if ca == cb {
				cost = 0
			}
			curr[j+1] = min(
				curr[j]+1,
				prev[j+1]+1,
				prev[j]+cost,
			)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

// applyToolResultMessagesCallback invokes the optional ToolResultMessages callback and
// converts its return value into choices. It returns:
//   - customChoices: the choices derived from callback output
//   - overridden: whether the default tool message should be replaced
//   - err: non-nil when the callback itself fails
func (p *FunctionCallResponseProcessor) applyToolResultMessagesCallback(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.Tool,
	result any,
	modifiedArgs []byte,
	index int,
	defaultMsg model.Message,
) ([]model.Choice, bool, error) {
	raw, cbErr := p.toolCallbacks.RunToolResultMessages(
		ctx,
		&tool.ToolResultMessagesInput{
			ToolName:           toolCall.Function.Name,
			Declaration:        tl.Declaration(),
			Arguments:          modifiedArgs,
			Result:             result,
			ToolCallID:         toolCall.ID,
			DefaultToolMessage: defaultMsg,
		},
	)
	if cbErr != nil {
		log.Errorf("ToolResultMessages callback failed for %s: %v", toolCall.Function.Name, cbErr)
		return nil, false, fmt.Errorf("tool callback error: %w", cbErr)
	}

	var msgs []model.Message
	switch v := raw.(type) {
	case nil:
		// No override.
	case model.Message:
		msgs = []model.Message{v}
	case []model.Message:
		msgs = v
	default:
		log.Warnf("ToolResultMessages callback for %s returned unsupported type %T; expected model.Message or []model.Message", toolCall.Function.Name, v)
	}

	if len(msgs) == 0 {
		return nil, false, nil
	}

	customChoices := make([]model.Choice, 0, len(msgs))
	for _, msg := range msgs {
		msg = ensureToolResultMessageName(msg, toolCall)
		customChoices = append(customChoices, model.Choice{
			Index:   index,
			Message: msg,
		})
	}
	// When a callback is provided and returns non-empty messages,
	// the framework defers entirely to the callback for correctness.
	return customChoices, true, nil
}

func ensureToolResultMessageName(
	msg model.Message,
	toolCall model.ToolCall,
) model.Message {
	if msg.Role != model.RoleTool ||
		msg.ToolID != toolCall.ID ||
		msg.ToolName != "" {
		return msg
	}
	msg.ToolName = toolCall.Function.Name
	return msg
}

// createErrorChoice creates an error choice for tool execution failures.
func (p *FunctionCallResponseProcessor) createErrorChoice(index int, toolID string,
	errorMsg string) *model.Choice {
	return &model.Choice{
		Index: index,
		Message: model.Message{
			Role:    model.RoleTool,
			Content: errorMsg,
			ToolID:  toolID,
		},
	}
}

// collectParallelToolResults drains resultChan and preserves order by index.
// It returns only non-nil events. When ctx is cancelled mid-drain it returns
// the partial set together with ctx.Err() so callers can distinguish a
// completed-but-erroring run from a cancelled-mid-flight run.
func (p *FunctionCallResponseProcessor) collectParallelToolResults(
	ctx context.Context,
	resultChan <-chan toolResult,
	toolCallsCount int,
) ([]toolResult, error) {
	results := make([]toolResult, toolCallsCount)
	var err error
	for {
		select {
		case result, ok := <-resultChan:
			if !ok {
				// Channel closed, all results received.
				return p.compactToolResults(results), err
			}
			if result.index >= 0 && result.index < len(results) {
				results[result.index] = result
				if err == nil && result.err != nil {
					err = result.err
				}
			} else {
				log.ErrorfContext(
					ctx,
					"Tool result index %d out of range [0, %d)",
					result.index,
					len(results),
				)
			}
		case <-ctx.Done():
			// Context cancelled — siblings are aborting. Return what we
			// have plus ctx.Err so the caller knows results may be partial.
			log.WarnfContext(
				ctx,
				"Context cancelled while waiting for tool results: %v",
				ctx.Err(),
			)
			if err == nil {
				err = ctx.Err()
			}
			return p.compactToolResults(results), err
		}
	}
}

func (p *FunctionCallResponseProcessor) runBeforeToolPluginCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	toolDeclaration *tool.Declaration,
) (context.Context, model.ToolCall, any, error) {
	if invocation == nil || invocation.Plugins == nil {
		return ctx, toolCall, nil, nil
	}

	callbacks := invocation.Plugins.ToolCallbacks()
	if callbacks == nil {
		return ctx, toolCall, nil, nil
	}

	args := &tool.BeforeToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: toolDeclaration,
		Arguments:   toolCall.Function.Arguments,
	}
	result, err := callbacks.RunBeforeTool(ctx, args)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Before tool plugin failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, toolCall, nil,
			fmt.Errorf("tool callback error: %w", err)
	}
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResult != nil {
		return ctx, toolCall, result.CustomResult, nil
	}
	if result != nil && result.ModifiedArguments != nil {
		toolCall.Function.Arguments = result.ModifiedArguments
	}
	return ctx, toolCall, nil, nil
}

func (p *FunctionCallResponseProcessor) runBeforeToolCallbacks(
	ctx context.Context,
	toolCall model.ToolCall,
	toolDeclaration *tool.Declaration,
) (context.Context, model.ToolCall, any, error) {
	if p.toolCallbacks == nil {
		return ctx, toolCall, nil, nil
	}

	args := &tool.BeforeToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: toolDeclaration,
		Arguments:   toolCall.Function.Arguments,
	}
	result, err := p.toolCallbacks.RunBeforeTool(ctx, args)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"Before tool callback failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, toolCall, nil,
			fmt.Errorf("tool callback error: %w", err)
	}

	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	if result != nil && result.CustomResult != nil {
		return ctx, toolCall, result.CustomResult, nil
	}
	if result != nil && result.ModifiedArguments != nil {
		toolCall.Function.Arguments = result.ModifiedArguments
	}
	return ctx, toolCall, nil, nil
}

func (p *FunctionCallResponseProcessor) runAfterToolPluginCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	toolDeclaration *tool.Declaration,
	toolResult any,
	toolErr error,
) (context.Context, any, bool, bool, error) {
	if invocation == nil || invocation.Plugins == nil {
		return ctx, toolResult, false, false, nil
	}

	callbacks := invocation.Plugins.ToolCallbacks()
	if callbacks == nil {
		return ctx, toolResult, false, false, nil
	}

	args := &tool.AfterToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: toolDeclaration,
		Arguments:   toolCall.Function.Arguments,
		Result:      toolResult,
		Error:       toolErr,
		Meta:        extractMetaFromResult(toolResult),
	}
	afterResult, err := callbacks.RunAfterTool(ctx, args)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"After tool plugin failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, toolResult, false, false,
			fmt.Errorf("tool callback error: %w", err)
	}
	if afterResult != nil && afterResult.Context != nil {
		ctx = afterResult.Context
	}
	skipSummarization := afterResult != nil &&
		afterResult.SkipSummarization
	if afterResult != nil && afterResult.CustomResult != nil {
		return ctx, afterResult.CustomResult, true, skipSummarization, nil
	}
	return ctx, toolResult, false, skipSummarization, nil
}

func (p *FunctionCallResponseProcessor) runAfterToolCallbacks(
	ctx context.Context,
	toolCall model.ToolCall,
	toolDeclaration *tool.Declaration,
	toolResult any,
	toolErr error,
) (context.Context, any, bool, error) {
	if p.toolCallbacks == nil {
		return ctx, toolResult, false, nil
	}

	args := &tool.AfterToolArgs{
		ToolCallID:  toolCall.ID,
		ToolName:    toolCall.Function.Name,
		Declaration: toolDeclaration,
		Arguments:   toolCall.Function.Arguments,
		Result:      toolResult,
		Error:       toolErr,
		Meta:        extractMetaFromResult(toolResult),
	}
	afterResult, err := p.toolCallbacks.RunAfterTool(ctx, args)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"After tool callback failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, toolResult, false,
			fmt.Errorf("tool callback error: %w", err)
	}

	if afterResult != nil && afterResult.Context != nil {
		ctx = afterResult.Context
	}
	skipSummarization := afterResult != nil &&
		afterResult.SkipSummarization
	if afterResult != nil && afterResult.CustomResult != nil {
		toolResult = afterResult.CustomResult
	}
	return ctx, toolResult, skipSummarization, nil
}

// extractMetaFromResult extracts metadata from tool result.
// For MCP mcpToolResult, returns the Meta field.
func extractMetaFromResult(result any) map[string]any {
	if result == nil {
		return nil
	}
	// Check for our wrapped mcpToolResult type (from tool/mcp package)
	type metaGetter interface {
		GetMeta() map[string]any
	}
	if mg, ok := result.(metaGetter); ok {
		return mg.GetMeta()
	}
	return nil
}

// executeToolWithCallbacks executes a tool with before/after callbacks.
// Returns (context, result, modifiedArguments, suppressDefaultToolMessage,
// skipSummarization, error).
func (p *FunctionCallResponseProcessor) executeToolWithCallbacks(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
	eventChan chan<- *event.Event,
) (context.Context, any, []byte, bool, bool, error) {
	// Inject tool call ID into context for callbacks to use.
	ctx = context.WithValue(ctx, tool.ContextKeyToolCallID{}, toolCall.ID)
	// Repair tool call arguments in place when needed.
	if jsonrepair.IsToolCallArgumentsJSONRepairEnabled(invocation) {
		jsonrepair.RepairToolCallArgumentsInPlace(ctx, &toolCall)
	}
	rememberExecutingToolArgs(ctx, toolCall.Function.Arguments)
	toolDeclaration := tl.Declaration()
	ctx, toolCall, customResult, err := p.runBeforeToolPluginCallbacks(
		ctx,
		invocation,
		toolCall,
		toolDeclaration,
	)
	if err != nil {
		return ctx, nil, toolCall.Function.Arguments, false, false, err
	}
	rememberExecutingToolArgs(ctx, toolCall.Function.Arguments)
	if customResult != nil {
		ctx = withStateDeltaSessionBaseline(ctx, invocation)
		return ctx, customResult, toolCall.Function.Arguments, false,
			false, nil
	}
	ctx, toolCall, customResult, err = p.runBeforeToolCallbacks(
		ctx,
		toolCall,
		toolDeclaration,
	)
	if err != nil {
		return ctx, nil, toolCall.Function.Arguments, false, false, err
	}
	ctx = withStateDeltaSessionBaseline(ctx, invocation)
	rememberExecutingToolArgs(ctx, toolCall.Function.Arguments)
	if customResult != nil {
		return ctx, customResult, toolCall.Function.Arguments, false,
			false, nil
	}
	permissionResult, err := p.checkToolPermission(
		ctx,
		invocation,
		toolCall,
		tl,
		toolDeclaration,
	)
	if err != nil {
		return ctx, nil, toolCall.Function.Arguments, false, false, err
	}
	if permissionResult != nil {
		ctx = withSkippedToolStateDelta(ctx)
		ctx = withSkippedToolSkipSummarization(ctx)
		return ctx, *permissionResult, toolCall.Function.Arguments, false,
			false, nil
	}
	// Execute the actual tool.
	ctx, toolResult, suppressDefaultToolMessage, toolErr := p.executeTool(
		ctx,
		invocation,
		toolCall,
		tl,
		eventChan,
	)
	if toolErr != nil {
		log.WarnfContext(
			ctx,
			"tool execute failed, function name: %v, arguments: %s, "+
				"result: %v, err: %v",
			toolCall.Function.Name,
			string(toolCall.Function.Arguments),
			toolResult,
			toolErr,
		)
	}
	ctx, toolResult, pluginOverride, skipSummarization, err :=
		p.runAfterToolPluginCallbacks(
			ctx,
			invocation,
			toolCall,
			toolDeclaration,
			toolResult,
			toolErr,
		)
	if err != nil {
		return ctx, toolResult, toolCall.Function.Arguments,
			suppressDefaultToolMessage, skipSummarization, err
	}
	if pluginOverride {
		if toolResult != nil {
			suppressDefaultToolMessage = false
		}
		pluginErr := toolErr
		if afterCallbackReplacedResult(toolErr, toolResult) {
			pluginErr = nil
		}
		return ctx, toolResult, toolCall.Function.Arguments,
			suppressDefaultToolMessage, skipSummarization, pluginErr
	}
	ctx, toolResult, localSkip, err := p.runAfterToolCallbacks(
		ctx,
		toolCall,
		toolDeclaration,
		toolResult,
		toolErr,
	)
	if err != nil {
		return ctx, toolResult, toolCall.Function.Arguments,
			suppressDefaultToolMessage, skipSummarization || localSkip, err
	}
	if toolResult != nil {
		suppressDefaultToolMessage = false
	}
	// When the after-tool callback replaced the result with a CustomResult,
	// the original tool execution error should be cleared so that the
	// caller uses the replacement result as the tool response message
	// instead of discarding it due to a non-nil error.
	if afterCallbackReplacedResult(toolErr, toolResult) {
		toolErr = nil
	}
	return ctx, toolResult, toolCall.Function.Arguments,
		suppressDefaultToolMessage, skipSummarization || localSkip, toolErr
}

func (p *FunctionCallResponseProcessor) checkToolPermission(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
	decl *tool.Declaration,
) (*tool.PermissionResult, error) {
	// Resolve metadata outermost-first (capability-aware) rather than fully
	// unwrapping to the innermost tool, so the permission policy sees the
	// effective metadata the business tool exposes (including an intermediate
	// transparent wrapper's own ConcurrencySafe/Destructive declaration).
	req := &tool.PermissionRequest{
		Tool:        tl,
		ToolName:    toolCall.Function.Name,
		ToolCallID:  toolCall.ID,
		Declaration: decl,
		Arguments:   toolCall.Function.Arguments,
		Metadata:    itool.ResolveMetadata(tl),
	}
	// Resolve the permission checker from the outermost wrapper inward so a
	// transparent wrapper's decision is never skipped by unwrapping past it. If
	// the wrapper chain cannot be fully traversed (overly deep or cyclic), fail
	// closed: deny rather than allow, since a deny may be hidden past the bound.
	checker, permErr := itool.ResolvePermissionChecker(tl)
	if permErr != nil {
		return normalizeToolPermissionResult(
			req,
			tool.DenyPermission("tool permission could not be resolved: "+permErr.Error()),
			nil,
		)
	}
	if checker != nil {
		decision, err := checker.CheckPermission(ctx, req)
		result, err := normalizeToolPermissionResult(req, decision, err)
		if result != nil || err != nil {
			return result, err
		}
	}
	if invocation == nil || invocation.RunOptions.ToolPermissionPolicy == nil {
		return nil, nil
	}
	decision, err := invocation.RunOptions.ToolPermissionPolicy.CheckToolPermission(ctx, req)
	return normalizeToolPermissionResult(req, decision, err)
}

func normalizeToolPermissionResult(
	req *tool.PermissionRequest,
	decision tool.PermissionDecision,
	checkErr error,
) (*tool.PermissionResult, error) {
	if checkErr != nil {
		return nil, checkErr
	}
	decision, err := tool.NormalizePermissionDecision(decision)
	if err != nil {
		return nil, err
	}
	if decision.Action == tool.PermissionActionAllow {
		return nil, nil
	}
	result := tool.PermissionResultFor(req.ToolName, decision)
	return &result, nil
}

func rememberExecutingToolArgs(ctx context.Context, args []byte) {
	if ctx == nil {
		return
	}
	tracker, ok := ctx.Value(executingToolArgsContextKey{}).(*[]byte)
	if !ok || tracker == nil {
		return
	}
	*tracker = args
}

// afterCallbackReplacedResult returns true when the after-tool callback has
// replaced the original (failed) tool result with a non-nil custom result.
// In that case the original toolErr should be cleared so that the framework
// sends the replacement result as the tool response message to the model.
// StopError is excluded because it carries a control-flow signal that must
// not be silently swallowed by a callback result replacement.
func afterCallbackReplacedResult(toolErr error, toolResult any) bool {
	if toolErr == nil || toolResult == nil {
		return false
	}
	if _, ok := agent.AsStopError(toolErr); ok {
		return false
	}
	return true
}

// isStreamable returns true if the tool supports streaming and its stream
// preference is enabled.
func isStreamable(t tool.Tool) bool {
	_, ok := streamableTool(t)
	return ok
}

func streamableTool(t tool.Tool) (tool.StreamableTool, bool) {
	streamable, ok := t.(tool.StreamableTool)
	if !ok {
		return nil, false
	}
	probe := itool.ResolveSemantic(t)
	if _, ok := probe.(tool.StreamableTool); !ok {
		return nil, false
	}
	// Check if the tool has a stream preference and if it is enabled.
	if pref, ok := probe.(streamInnerPreference); ok && !pref.StreamInner() {
		return nil, false
	}
	return streamable, true
}

// executeTool executes the tool based on its capabilities.
func (f *FunctionCallResponseProcessor) executeTool(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.Tool,
	eventChan chan<- *event.Event,
) (context.Context, any, bool, error) {
	// Prefer streaming execution if the tool supports it.
	if streamable, ok := streamableTool(tl); ok {
		return f.executeStreamableTool(
			ctx, invocation, toolCall, streamable, eventChan,
		)
	}
	// Fallback to callable tool execution if supported.
	if callable, ok := tl.(tool.CallableTool); ok {
		ctx, result, err := f.executeCallableTool(ctx, toolCall, callable)
		return ctx, result, false, err
	}
	return ctx, nil, false, fmt.Errorf("unsupported tool type: %T", tl)
}

// executeCallableTool executes a callable tool.
func (p *FunctionCallResponseProcessor) executeCallableTool(
	ctx context.Context,
	toolCall model.ToolCall,
	tl tool.CallableTool,
) (context.Context, any, error) {
	callCtx := tool.WithoutToolResultAttachmentBudget(ctx)
	if p.toolRetryPolicy == nil {
		result, err := tl.Call(callCtx, toolCall.Function.Arguments)
		if err != nil {
			log.ErrorfContext(
				ctx,
				"CallableTool execution failed for %s: %v",
				toolCall.Function.Name,
				err,
			)
			return ctx, nil, fmt.Errorf("%s: %w", ErrorCallableToolExecution, err)
		}
		return ctx, result, nil
	}
	runResult := toolretry.Execute(ctx, toolretry.ExecuteInput{
		ToolName:   toolCall.Function.Name,
		ToolCallID: toolCall.ID,
		Arguments:  toolCall.Function.Arguments,
		Policy:     p.toolRetryPolicy,
		Call: func(_ context.Context, args []byte) (any, error) {
			return tl.Call(callCtx, args)
		},
		ResultError: func(result any) bool {
			return extractResultError(result)
		},
		IsTerminalError: func(err error) bool {
			_, ok := agent.AsStopError(err)
			return ok
		},
	})
	if runResult.Error != nil {
		log.ErrorfContext(
			ctx,
			"CallableTool execution failed for %s: %v",
			toolCall.Function.Name,
			runResult.Error,
		)
		return ctx, nil, fmt.Errorf("%s: %w", ErrorCallableToolExecution, runResult.Error)
	}
	return ctx, runResult.Result, nil
}

func extractResultError(result any) bool {
	if result == nil {
		return false
	}
	type resultErrorGetter interface {
		RetryResultError() bool
	}
	rg, ok := result.(resultErrorGetter)
	if !ok {
		return false
	}
	return rg.RetryResultError()
}

func buildDefaultToolMessage(
	ctx context.Context,
	toolCallID string,
	result any,
	codec resultcodec.Codec,
) (model.Message, error) {
	var content string
	if codec == nil {
		// Preserve legacy tool message serialization for default fallback
		// content. Use marshalJSONNoHTMLEscape so that <, >, & in tool output
		// (e.g. Go source code containing "<-done") are preserved verbatim
		// instead of being escaped to \u003c, \u003e, \u0026 which confuses
		// LLMs reading the content.
		resultBytes, err := marshalJSONNoHTMLEscape(result)
		if err != nil {
			return model.Message{}, fmt.Errorf("%s: %w", ErrorMarshalResult, err)
		}
		content = string(resultBytes)
	} else {
		encoded, err := encodeWithRecover(ctx, codec, result)
		if err != nil {
			return model.Message{}, fmt.Errorf("%s: %w", ErrorEncodeResult, err)
		}
		content = encoded
	}
	return model.Message{
		Role:    model.RoleTool,
		Content: content,
		ToolID:  toolCallID,
	}, nil
}

// encodeWithRecover runs the codec under panic protection so a misbehaving
// custom encoder becomes an observable error instead of crashing the flow.
// Built-in codecs do not panic; this is defense-in-depth.
func encodeWithRecover(
	ctx context.Context,
	codec resultcodec.Codec,
	result any,
) (content string, err error) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(
				ctx,
				log.PanicPrefix+" Tool result codec panic: %v",
				r,
			)
			err = fmt.Errorf("tool result codec panic: %v", r)
		}
	}()
	return codec.Encode(ctx, result)
}

// marshalJSONNoHTMLEscape serializes v to JSON without escaping <, >, & characters.
// It delegates to jsonx.MarshalNoHTMLEscape, the single source of truth for the
// framework's default tool result encoding, so this path stays byte-for-byte
// identical to resultcodec.JSON().
func marshalJSONNoHTMLEscape(v any) ([]byte, error) {
	return jsonx.MarshalNoHTMLEscape(v)
}

type structuredStreamErrorOptIn interface {
	TRPCAgentGoStructuredStreamErrorsOptIn() bool
}

func streamableToolCallContext(
	ctx context.Context,
	tl tool.StreamableTool,
) context.Context {
	ctx = tool.WithFinalResultChunks(ctx)
	if !shouldRequestStructuredStreamErrors(tl) {
		return ctx
	}
	return tool.WithStructuredStreamErrors(ctx)
}

func shouldRequestStructuredStreamErrors(tl tool.StreamableTool) bool {
	if tl == nil {
		return false
	}
	pref, ok := itool.ResolveSemantic(tl).(structuredStreamErrorOptIn)
	return ok && pref.TRPCAgentGoStructuredStreamErrorsOptIn()
}

func innerTextModeForTool(tl tool.StreamableTool) tool.InnerTextMode {
	if tl == nil {
		return tool.InnerTextModeInclude
	}
	pref, ok := itool.ResolveSemantic(tl).(innerTextModePreference)
	if !ok {
		return tool.InnerTextModeInclude
	}
	return pref.InnerTextMode()
}

// executeStreamableTool executes a streamable tool.
func (f *FunctionCallResponseProcessor) executeStreamableTool(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	tl tool.StreamableTool,
	eventChan chan<- *event.Event,
) (context.Context, any, bool, error) {
	reader, err := tl.StreamableCall(
		streamableToolCallContext(
			tool.WithoutToolResultAttachmentBudget(ctx),
			tl,
		),
		toolCall.Function.Arguments,
	)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"StreamableTool execution failed for %s: %v",
			toolCall.Function.Name,
			err,
		)
		return ctx, nil, false, fmt.Errorf("%s: %w", ErrorStreamableToolExecution, err)
	}
	defer reader.Close()

	// Process stream chunks, handling:
	// Case 1: Raw sub-agent event passthrough.
	// Case 2: Plain text-like chunk. Emit partial tool.response event.
	contents, finalResult, err := f.consumeStream(
		ctx,
		invocation,
		toolCall,
		reader,
		eventChan,
		innerTextModeForTool(tl),
		shouldRequestStructuredStreamErrors(tl),
	)
	if err != nil {
		return ctx, nil, false, err
	}
	if finalResult.seen {
		return ctx, finalResult.value, finalResult.value == nil, nil
	}
	// If we forwarded inner events, still return the merged content as the tool
	// result so it can be recorded in the tool response message for the next LLM
	// turn (to satisfy providers that require tool messages). The UI example
	// suppresses printing these aggregated strings to avoid duplication; they are
	// primarily for model consumption.
	return ctx, tool.Merge(contents), false, nil
}

type streamFinalResult struct {
	seen  bool
	value any
}

type streamInnerEventState struct {
	pendingGraphToolErrorEvent *event.Event
}

type normalizedFinalResultChunk struct {
	result     any
	stateDelta map[string][]byte
}

type syntheticStateOnlyToolChoiceKey struct{}

func markSyntheticStateOnlyToolChoice(ctx context.Context) context.Context {
	return context.WithValue(ctx, syntheticStateOnlyToolChoiceKey{}, true)
}

func hasSyntheticStateOnlyToolChoice(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	synthetic, _ := ctx.Value(syntheticStateOnlyToolChoiceKey{}).(bool)
	return synthetic
}

// consumeStream reads all chunks from the reader and processes them.
func (f *FunctionCallResponseProcessor) consumeStream(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	reader *tool.StreamReader,
	eventChan chan<- *event.Event,
	innerTextMode tool.InnerTextMode,
	structuredErrors bool,
) ([]any, streamFinalResult, error) {
	var contents []any
	var finalResult streamFinalResult
	var innerEventState streamInnerEventState
	for {
		chunk, err := reader.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.ErrorfContext(
				ctx,
				"StreamableTool execution failed for %s: receive chunk "+
					"from stream reader failed: %v, may merge "+
					"incomplete data",
				toolCall.Function.Name,
				err,
			)
			break
		}

		if err := f.processStreamChunk(
			ctx,
			invocation,
			toolCall,
			chunk,
			eventChan,
			&contents,
			&finalResult,
			&innerEventState,
			innerTextMode,
			structuredErrors,
		); err != nil {
			return contents, finalResult, err
		}
	}
	if structuredErrors && innerEventState.pendingGraphToolErrorEvent != nil {
		return contents, finalResult, streamToolEventError(
			innerEventState.pendingGraphToolErrorEvent,
		)
	}
	return contents, finalResult, nil
}

// appendInnerEventContent extracts textual content from an inner event and appends it.
func (f *FunctionCallResponseProcessor) appendInnerEventContent(
	ev *event.Event,
	contents *[]any,
) {
	if ev.Response != nil && len(ev.Response.Choices) > 0 {
		ch := ev.Response.Choices[0]
		if ch.Delta.Content != "" {
			*contents = append(*contents, ch.Delta.Content)
		} else if ch.Message.Role == model.RoleAssistant &&
			ch.Message.Content != "" {
			*contents = append(*contents, ch.Message.Content)
		}
	}
}

// buildPartialToolResponseEvent constructs a partial tool.response event.
func (f *FunctionCallResponseProcessor) buildPartialToolResponseEvent(
	inv *agent.Invocation,
	toolCall model.ToolCall,
	text string,
) *event.Event {
	resp := &model.Response{
		ID:      uuid.New().String(),
		Object:  model.ObjectTypeToolResponse,
		Created: time.Now().Unix(),
		Model:   inv.Model.Info().Name,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleTool, ToolID: toolCall.ID},
			Delta:   model.Message{Role: model.RoleTool, Content: text, ToolID: toolCall.ID},
		}},
		Timestamp: time.Now(),
		Done:      false,
		IsPartial: true,
	}
	return event.New(
		inv.InvocationID,
		inv.AgentName,
		event.WithResponse(resp),
	)
}

func (f *FunctionCallResponseProcessor) buildStateDeltaToolResponseEvent(
	inv *agent.Invocation,
	toolCall model.ToolCall,
	stateDelta map[string][]byte,
) *event.Event {
	evt := f.buildPartialToolResponseEvent(inv, toolCall, "")
	if evt != nil && len(stateDelta) > 0 {
		evt.StateDelta = cloneEventStateDelta(stateDelta)
	}
	return evt
}

func cloneEventStateDelta(stateDelta map[string][]byte) map[string][]byte {
	if len(stateDelta) == 0 {
		return nil
	}
	cloned := make(map[string][]byte, len(stateDelta))
	for key, value := range stateDelta {
		if value == nil {
			cloned[key] = nil
			continue
		}
		cloned[key] = append([]byte(nil), value...)
	}
	return cloned
}

func streamToolEventError(ev *event.Event) error {
	if ev == nil || !ev.IsError() {
		return nil
	}
	if ev.Error != nil && ev.Error.Type == agent.ErrorTypeStopAgentError {
		return agent.NewStopError(ev.Error.Message)
	}
	if isRetryingGraphNodeErrorEvent(ev) {
		return nil
	}
	if ev.Error != nil {
		return fmt.Errorf(
			"%s: %s: %s",
			ErrorStreamableToolExecution,
			ev.Error.Type,
			ev.Error.Message,
		)
	}
	return fmt.Errorf(ErrorStreamableToolExecution)
}

func isGraphToolExecutionErrorEvent(ev *event.Event) bool {
	if ev == nil || ev.Response == nil || ev.StateDelta == nil {
		return false
	}
	if ev.Response.Object != model.ObjectTypeToolResponse {
		return false
	}
	_, ok := ev.StateDelta[graph.MetadataKeyTool]
	return ok
}

func isRetryingGraphNodeErrorEvent(ev *event.Event) bool {
	if ev == nil ||
		ev.Error == nil ||
		ev.StateDelta == nil {
		return false
	}
	rawMetadata := ev.StateDelta[graph.MetadataKeyNode]
	if len(rawMetadata) == 0 {
		return false
	}
	var metadata graph.NodeExecutionMetadata
	if err := json.Unmarshal(rawMetadata, &metadata); err != nil {
		return false
	}
	return metadata.Phase == graph.ExecutionPhaseError && metadata.Retrying
}

// marshalChunkToText converts a chunk content into a string representation.
func marshalChunkToText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	default:
		if bts, e := marshalJSONNoHTMLEscape(v); e == nil {
			return string(bts)
		}
		return fmt.Sprintf("%v", v)
	}
}

// compactToolResults keeps received tool results while preserving order.
// Results without an event may still carry executed args for merged metadata.
func (p *FunctionCallResponseProcessor) compactToolResults(
	results []toolResult,
) []toolResult {
	filtered := make([]toolResult, 0, len(results))
	for _, result := range results {
		if result.event != nil || result.toolArgs != nil ||
			result.err != nil || result.stateDelta != nil {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func newToolCallResponseEvent(
	invocation *agent.Invocation,
	functionCallResponse *model.Response,
	functionResponses []model.Choice) *event.Event {
	// Create function response event.
	e := event.NewResponseEvent(
		invocation.InvocationID,
		invocation.AgentName,
		&model.Response{
			Object:    model.ObjectTypeToolResponse,
			Created:   time.Now().Unix(),
			Model:     functionCallResponse.Model,
			Choices:   functionResponses,
			Timestamp: time.Now(),
		},
	)
	agent.InjectIntoEvent(invocation, e)
	return e
}

func newMinimalToolCallResponseEvent(
	invocation *agent.Invocation,
	functionCallResponse *model.Response,
	toolCall model.ToolCall,
	index int,
	toolArgs []byte,
) *event.Event {
	ev := newToolCallResponseEvent(
		invocation,
		functionCallResponse,
		[]model.Choice{newMinimalToolChoice(toolCall, index)},
	)
	annotateToolCallArgs(ev, toolCall, toolArgs)
	return ev
}

func newMinimalToolChoice(
	toolCall model.ToolCall,
	index int,
) model.Choice {
	return model.Choice{
		Index: index,
		Message: model.Message{
			Role:     model.RoleTool,
			ToolID:   toolCall.ID,
			ToolName: toolCall.Function.Name,
		},
	}
}

func mergeParallelToolCallResponseEvents(es []*event.Event) *event.Event {
	switch len(es) {
	case 0:
		return nil
	case 1:
		return es[0]
	default:
	}

	mergedChoices := collectMergedChoices(es)
	mergedDelta := collectStateDelta(es)
	mergedToolArgs := collectToolCallArgs(es)
	baseEvent := findBaseEvent(es)
	resp := buildMergedToolResponse(baseEvent, mergedChoices)
	mergedEvent := buildMergedEvent(baseEvent, resp)

	if len(mergedDelta) > 0 {
		mergedEvent.StateDelta = mergedDelta
	}
	if len(mergedToolArgs) > 0 {
		if err := event.SetExtension(
			mergedEvent,
			event.ToolCallArgsExtensionKey,
			mergedToolArgs,
		); err != nil {
			log.Warnf("Failed to merge tool call args extension: %v", err)
		}
	}
	if shouldSkipSummarization(es) {
		markSkipSummarization(mergedEvent)
	}
	return mergedEvent
}

// collectMergedChoices collects the choices from all events.
func collectMergedChoices(es []*event.Event) []model.Choice {
	totalChoices := 0
	for _, e := range es {
		if e != nil && e.Response != nil {
			totalChoices += len(e.Response.Choices)
		}
	}

	mergedChoices := make([]model.Choice, 0, totalChoices)
	for _, e := range es {
		// Add nil checks to prevent panic
		if e != nil && e.Response != nil {
			mergedChoices = append(mergedChoices, e.Response.Choices...)
		}
	}
	return mergedChoices
}

// collectStateDelta collects the state delta from all events.
func collectStateDelta(es []*event.Event) map[string][]byte {
	mergedDelta := map[string][]byte{}
	for _, e := range es {
		if e == nil || len(e.StateDelta) == 0 {
			continue
		}
		for k, v := range e.StateDelta {
			mergedDelta[k] = v
		}
	}
	return mergedDelta
}

func collectToolCallArgs(es []*event.Event) map[string]string {
	var merged map[string]string
	for _, e := range es {
		args, ok, err := event.GetExtension[map[string]string](
			e,
			event.ToolCallArgsExtensionKey,
		)
		if err != nil {
			log.Warnf("Failed to decode tool call args extension: %v", err)
			continue
		}
		if !ok || len(args) == 0 {
			continue
		}
		if merged == nil {
			merged = make(map[string]string)
		}
		for toolCallID, toolArgs := range args {
			merged[toolCallID] = toolArgs
		}
	}
	return merged
}

// findBaseEvent finds a valid base event for metadata.
func findBaseEvent(es []*event.Event) *event.Event {
	for _, e := range es {
		if e != nil {
			return e
		}
	}
	return nil
}

// buildMergedToolResponse builds the merged tool response.
func buildMergedToolResponse(baseEvent *event.Event, mergedChoices []model.Choice) *model.Response {
	modelName := "unknown"
	if baseEvent != nil && baseEvent.Response != nil {
		modelName = baseEvent.Response.Model
	}
	return &model.Response{
		ID:        uuid.New().String(),
		Object:    model.ObjectTypeToolResponse,
		Created:   time.Now().Unix(),
		Model:     modelName,
		Choices:   mergedChoices,
		Timestamp: time.Now(),
	}
}

// buildMergedEvent builds the merged event.
func buildMergedEvent(baseEvent *event.Event, resp *model.Response) *event.Event {
	// If we have a base event, carry over invocation, author and branch.
	if baseEvent != nil {
		return event.New(baseEvent.InvocationID, baseEvent.Author, event.WithResponse(resp))
	}
	// Fallback: construct without base metadata.
	return event.New("", "", event.WithResponse(resp))
}

// shouldSkipSummarization checks if any event prefers skipping summarization.
func shouldSkipSummarization(es []*event.Event) bool {
	for _, e := range es {
		if e != nil && e.Actions != nil && e.Actions.SkipSummarization {
			return true
		}
	}
	return false
}

func markSkipSummarization(ev *event.Event) {
	if ev == nil {
		return
	}
	if ev.Actions == nil {
		ev.Actions = &event.EventActions{}
	}
	ev.Actions.SkipSummarization = true
}

func toolPrefersSkipSummarization(tl tool.Tool) bool {
	if skipper, ok := itool.ResolveSemantic(tl).(summarizationSkipper); ok {
		return skipper.SkipSummarization()
	}
	return false
}

// findCompatibleTool attempts to map a requested (missing) tool name to a compatible tool.
// For models that directly call sub-agent names, map to transfer_to_agent when available.
func findCompatibleTool(requested string, tools map[string]tool.Tool, invocation *agent.Invocation) tool.Tool {
	transfer, ok := tools[transfer.TransferToolName]
	if !ok || invocation == nil || invocation.Agent == nil {
		return nil
	}
	for _, a := range invocation.Agent.SubAgents() {
		if a.Info().Name == requested {
			return transfer
		}
	}
	return nil
}

// convertToolArguments converts original args to the mapped tool args when needed.
// When mapping sub-agent name -> transfer_to_agent, wrap message and set agent_name.
func convertToolArguments(
	invocation *agent.Invocation,
	originalName string,
	originalArgs []byte,
	targetName string,
) []byte {
	if targetName != transfer.TransferToolName {
		return nil
	}

	var input subAgentCall
	if len(originalArgs) > 0 {
		var err error
		if jsonrepair.IsToolCallArgumentsJSONRepairEnabled(invocation) {
			err = jsonutils.DecodeLeadingJSON(string(originalArgs), &input)
		} else {
			err = json.Unmarshal(originalArgs, &input)
		}
		if err != nil {
			log.Warnf("Failed to unmarshal sub-agent call arguments for %s: %v", originalName, err)
			return nil
		}
	}

	message := input.Message
	if message == "" {
		message = defaultTransferMessage
	}

	req := &transfer.Request{
		AgentName: originalName,
		Message:   message,
	}

	b, err := json.Marshal(req)
	if err != nil {
		log.Warnf("Failed to marshal transfer request for %s: %v", originalName, err)
		return nil
	}
	return b
}

// processStreamChunk handles a single streamed chunk and updates contents and events.
func (f *FunctionCallResponseProcessor) processStreamChunk(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	chunk tool.StreamChunk,
	eventChan chan<- *event.Event,
	contents *[]any,
	finalResult *streamFinalResult,
	innerEventState *streamInnerEventState,
	innerTextMode tool.InnerTextMode,
	structuredErrors bool,
) error {
	if finalChunk, ok := normalizeFinalResultChunk(chunk.Content); ok {
		return f.handleFinalResultChunk(
			ctx,
			invocation,
			toolCall,
			eventChan,
			finalResult,
			innerEventState,
			finalChunk,
			structuredErrors,
		)
	}
	if ev, ok := chunk.Content.(*event.Event); ok {
		return f.handleStreamInnerEvent(
			ctx,
			eventChan,
			contents,
			innerEventState,
			ev,
			innerTextMode,
			structuredErrors,
		)
	}
	return f.handlePlainStreamChunk(
		ctx,
		invocation,
		toolCall,
		eventChan,
		contents,
		innerEventState,
		chunk.Content,
		structuredErrors,
	)
}

func normalizeFinalResultChunk(content any) (*normalizedFinalResultChunk, bool) {
	switch v := content.(type) {
	case tool.FinalResultChunk:
		if v.Result == nil {
			return nil, true
		}
		return &normalizedFinalResultChunk{result: v.Result}, true
	case *tool.FinalResultChunk:
		if v == nil || v.Result == nil {
			return nil, true
		}
		return &normalizedFinalResultChunk{result: v.Result}, true
	case tool.FinalResultStateChunk:
		if v.Result == nil && len(v.StateDelta) == 0 {
			return nil, true
		}
		return &normalizedFinalResultChunk{
			result:     v.Result,
			stateDelta: cloneEventStateDelta(v.StateDelta),
		}, true
	case *tool.FinalResultStateChunk:
		if v == nil || (v.Result == nil && len(v.StateDelta) == 0) {
			return nil, true
		}
		return &normalizedFinalResultChunk{
			result:     v.Result,
			stateDelta: cloneEventStateDelta(v.StateDelta),
		}, true
	default:
		return nil, false
	}
}

func (f *FunctionCallResponseProcessor) handleFinalResultChunk(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	eventChan chan<- *event.Event,
	finalResult *streamFinalResult,
	innerEventState *streamInnerEventState,
	finalChunk *normalizedFinalResultChunk,
	structuredErrors bool,
) error {
	if err := flushPendingGraphToolError(innerEventState, nil, structuredErrors); err != nil {
		return err
	}
	if finalChunk != nil && finalResult != nil {
		finalResult.seen = true
		finalResult.value = finalChunk.result
	}
	if finalChunk == nil || len(finalChunk.stateDelta) == 0 || eventChan == nil {
		return nil
	}
	deltaEvent := f.buildStateDeltaToolResponseEvent(invocation, toolCall, finalChunk.stateDelta)
	return agent.EmitEvent(ctx, invocation, eventChan, deltaEvent)
}

func (f *FunctionCallResponseProcessor) handleStreamInnerEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	contents *[]any,
	innerEventState *streamInnerEventState,
	ev *event.Event,
	innerTextMode tool.InnerTextMode,
	structuredErrors bool,
) error {
	if err := flushPendingGraphToolError(innerEventState, ev, structuredErrors); err != nil {
		return err
	}
	filteredEvent, shouldEmit := filterForwardedInnerTextEvent(
		ev,
		innerTextMode,
	)
	if shouldEmit {
		if err := event.EmitEvent(ctx, eventChan, filteredEvent); err != nil {
			return err
		}
	}
	f.appendInnerEventContent(ev, contents)
	if !structuredErrors {
		return nil
	}
	if isGraphToolExecutionErrorEvent(ev) {
		if innerEventState != nil {
			innerEventState.pendingGraphToolErrorEvent = ev
		}
		return nil
	}
	return streamToolEventError(ev)
}

func filterForwardedInnerTextEvent(
	ev *event.Event,
	mode tool.InnerTextMode,
) (*event.Event, bool) {
	if ev == nil || mode != tool.InnerTextModeExclude {
		return ev, ev != nil
	}
	if ev.Response == nil || len(ev.Response.Choices) == 0 {
		return ev, true
	}

	filtered := *ev
	filtered.Response = ev.Response.Clone()
	modified := false
	for i := range filtered.Response.Choices {
		choice := &filtered.Response.Choices[i]
		if filtered.Response.Object == model.ObjectTypeChatCompletionChunk &&
			clearForwardedMessageText(&choice.Delta) {
			modified = true
		}
		if choice.Message.Role == model.RoleAssistant &&
			clearForwardedMessageText(&choice.Message) {
			modified = true
		}
	}
	if !modified {
		return ev, true
	}
	return &filtered, shouldEmitFilteredInnerEvent(&filtered)
}

func shouldEmitFilteredInnerEvent(ev *event.Event) bool {
	if ev == nil {
		return false
	}
	if responseHasForwardablePayload(ev.Response) {
		return true
	}
	if len(ev.StateDelta) > 0 || ev.Error != nil ||
		ev.StructuredOutput != nil || ev.ExecutionTrace != nil ||
		ev.Actions != nil || ev.RequiresCompletion {
		return true
	}
	return ev.Object != "" &&
		ev.Object != model.ObjectTypeChatCompletion &&
		ev.Object != model.ObjectTypeChatCompletionChunk
}

func clearForwardedMessageText(msg *model.Message) bool {
	if msg == nil {
		return false
	}
	modified := false
	if msg.Content != "" {
		msg.Content = ""
		modified = true
	}
	filteredParts, removed := removeForwardedTextContentParts(
		msg.ContentParts,
	)
	if removed {
		msg.ContentParts = filteredParts
		modified = true
	}
	return modified
}

func removeForwardedTextContentParts(
	parts []model.ContentPart,
) ([]model.ContentPart, bool) {
	if len(parts) == 0 {
		return parts, false
	}
	kept := make([]model.ContentPart, 0, len(parts))
	removed := false
	for _, part := range parts {
		if part.Type == model.ContentTypeText {
			removed = true
			continue
		}
		kept = append(kept, part)
	}
	if !removed {
		return parts, false
	}
	return kept, true
}

func responseHasForwardablePayload(rsp *model.Response) bool {
	if rsp == nil {
		return false
	}
	if rsp.Error != nil {
		return true
	}
	for i := range rsp.Choices {
		choice := rsp.Choices[i]
		if choice.Delta.Content != "" || choice.Delta.ToolID != "" ||
			len(choice.Delta.ContentParts) > 0 ||
			len(choice.Delta.ToolCalls) > 0 ||
			choice.Delta.ReasoningContent != "" {
			return true
		}
		if choice.Message.Content != "" || choice.Message.ToolID != "" ||
			len(choice.Message.ContentParts) > 0 ||
			len(choice.Message.ToolCalls) > 0 ||
			choice.Message.ReasoningContent != "" {
			return true
		}
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			return true
		}
	}
	return false
}

func (f *FunctionCallResponseProcessor) handlePlainStreamChunk(
	ctx context.Context,
	invocation *agent.Invocation,
	toolCall model.ToolCall,
	eventChan chan<- *event.Event,
	contents *[]any,
	innerEventState *streamInnerEventState,
	content any,
	structuredErrors bool,
) error {
	if err := flushPendingGraphToolError(innerEventState, nil, structuredErrors); err != nil {
		return err
	}
	text := marshalChunkToText(content)
	if text == "" {
		return nil
	}
	*contents = append(*contents, text)
	if eventChan == nil {
		return nil
	}
	partial := f.buildPartialToolResponseEvent(invocation, toolCall, text)
	return agent.EmitEvent(ctx, invocation, eventChan, partial)
}

func flushPendingGraphToolError(
	state *streamInnerEventState,
	nextEvent *event.Event,
	structuredErrors bool,
) error {
	if !structuredErrors {
		return nil
	}
	if state == nil || state.pendingGraphToolErrorEvent == nil {
		return nil
	}
	if nextEvent != nil {
		if isRetryingGraphNodeErrorEvent(nextEvent) {
			state.pendingGraphToolErrorEvent = nil
			return nil
		}
		if nextEvent.IsError() {
			state.pendingGraphToolErrorEvent = nil
			return nil
		}
	}
	err := streamToolEventError(state.pendingGraphToolErrorEvent)
	state.pendingGraphToolErrorEvent = nil
	return err
}
