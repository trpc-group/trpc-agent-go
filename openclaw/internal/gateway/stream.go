//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
)

const (
	traceStatusOK       = "ok"
	traceStatusIgnored  = "ignored"
	traceStatusError    = "error"
	traceStatusCanceled = "canceled"

	streamToolExecCommand    = "exec_command"
	streamToolReadDocument   = "read_document"
	streamToolReadSheet      = "read_spreadsheet"
	streamToolReadFile       = "fs_read_file"
	streamToolSaveFile       = "fs_save_file"
	streamToolListDir        = "fs_list_dir"
	streamToolSearch         = "fs_search"
	streamToolApplyPatch     = "apply_patch"
	progressSummaryPrepare   = "Preparing request"
	progressSummaryDoc       = "Reading document"
	progressSummarySheet     = "Reading spreadsheet"
	progressSummaryTool      = "Running local tool"
	progressSummaryAnswering = "Preparing final answer"
	progressSummaryGoTest    = "Running go test"
	progressSummaryPytest    = "Running pytest"
	progressSummaryNPMTest   = "Running npm test"
	progressSummaryGit       = "Running git command"
	progressSummaryInspect   = "Inspecting workspace"
)

type streamOutcome struct {
	status string
	errMsg string
}

type progressState struct {
	startedAt time.Time
	stage     gwproto.StreamProgressStage
	summary   string
}

// StreamMessage processes a request and returns a stream of gateway
// events. Validation errors are returned as APIError/status pairs.
func (s *Server) StreamMessage(
	ctx context.Context,
	req gwproto.MessageRequest,
) (<-chan gwproto.StreamEvent, *gwproto.APIError, int) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil {
		return nil, &gwproto.APIError{
			Type:    errTypeInternal,
			Message: "nil server",
		}, http.StatusInternalServerError
	}

	trace, created := s.ensureTrace(ctx, req)
	if created && trace != nil {
		ctx = debugrecorder.WithTrace(ctx, trace)
	}
	if trace != nil {
		summary, err := debugrecorder.SummarizeRequest(trace, req)
		if err != nil {
			_ = trace.RecordError(err)
		}
		_ = trace.Record(debugrecorder.KindGatewayReq, summary)
	}

	prepared, earlyRsp, earlyStatus := s.prepareMessageRun(
		ctx,
		req,
		trace,
	)
	if earlyRsp != nil {
		if created && trace != nil {
			end := debugrecorder.TraceEnd{
				Duration: 0,
			}
			if earlyRsp.Ignored {
				end.Status = traceStatusIgnored
			} else {
				end.Status = traceStatusError
			}
			if earlyRsp.Error != nil {
				end.Error = earlyRsp.Error.Message
			}
			_ = trace.Close(end)
		}
		if earlyRsp.Ignored {
			events := singleStreamEvents(
				gwproto.StreamEvent{
					Type:    gwproto.StreamEventTypeRunIgnored,
					Ignored: true,
				},
				gwproto.StreamEvent{
					Type: gwproto.StreamEventTypeRunCompleted,
				},
			)
			return events, nil, http.StatusOK
		}
		return nil, earlyRsp.Error, earlyStatus
	}

	out := make(chan gwproto.StreamEvent, 16)
	go func() {
		startedAt := time.Now()
		outcome := streamOutcome{status: traceStatusError}
		defer close(out)
		if created && trace != nil {
			defer func() {
				_ = trace.Close(debugrecorder.TraceEnd{
					Duration: time.Since(startedAt),
					Status:   outcome.status,
					Error:    outcome.errMsg,
				})
			}()
		}

		s.lanes.withLock(prepared.sessionID, func() {
			outcome = s.streamLocked(
				ctx,
				prepared,
				trace,
				out,
			)
		})
	}()

	return out, nil, http.StatusOK
}

func (s *Server) handleMessagesStream(
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, methodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, gwproto.APIError{
			Type:    errTypeInternal,
			Message: "streaming not supported",
		}, http.StatusInternalServerError)
		return
	}

	var req gwproto.MessageRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, gwproto.APIError{
			Type:    errTypeInvalidRequest,
			Message: err.Error(),
		}, http.StatusBadRequest)
		return
	}

	events, apiErr, status := s.StreamMessage(r.Context(), req)
	if apiErr != nil {
		s.writeError(w, *apiErr, status)
		return
	}

	w.Header().Set(headerContentType, gwproto.SSEContentType)
	w.Header().Set(headerCacheCtrl, cacheControlNoCache)
	w.Header().Set(headerConnection, connectionKeepAlive)
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for evt := range events {
		if !writeSSEEvent(w, flusher, evt) {
			return
		}
	}
}

func writeSSEEvent(
	w http.ResponseWriter,
	flusher http.Flusher,
	evt gwproto.StreamEvent,
) bool {
	data, err := json.Marshal(evt)
	if err != nil {
		log.Warnf("gateway: marshal stream event: %v", err)
		return false
	}

	if _, err := fmt.Fprintf(
		w,
		"%s%s\n%s%s%s",
		gwproto.SSEEventLinePrefix,
		evt.Type,
		gwproto.SSEDataLinePrefix,
		data,
		gwproto.SSELineEnding,
	); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func (s *Server) streamLocked(
	ctx context.Context,
	run preparedMessageRun,
	trace *debugrecorder.Trace,
	out chan<- gwproto.StreamEvent,
) streamOutcome {
	if trace != nil {
		_ = trace.Record(
			debugrecorder.KindGatewayRun,
			map[string]any{
				"user_id":    run.userID,
				"session_id": run.sessionID,
				"request_id": run.requestID,
				"stream":     true,
			},
		)
	}

	if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
		Type:      gwproto.StreamEventTypeRunStarted,
		SessionID: run.sessionID,
		RequestID: run.requestID,
	}) {
		return streamOutcome{
			status: traceStatusError,
			errMsg: contextErrMessage(ctx),
		}
	}
	progress := progressState{startedAt: time.Now()}
	if !sendProgressUpdate(
		ctx,
		out,
		run,
		&progress,
		gwproto.StreamProgressStagePreparing,
		progressSummaryPrepare,
	) {
		return streamOutcome{
			status: traceStatusError,
			errMsg: contextErrMessage(ctx),
		}
	}

	ctx, runOpts := s.resolveRunOptions(ctx, run)
	events, err := s.runner.Run(
		ctx,
		run.userID,
		run.sessionID,
		run.userMsg,
		runOpts...,
	)
	if err != nil {
		apiErr := gwproto.APIError{
			Type:    errTypeInternal,
			Message: err.Error(),
		}
		_ = sendStreamEvent(ctx, out, gwproto.StreamEvent{
			Type:      gwproto.StreamEventTypeRunError,
			SessionID: run.sessionID,
			RequestID: run.requestID,
			Error:     &apiErr,
		})
		return streamOutcome{
			status: traceStatusError,
			errMsg: err.Error(),
		}
	}

	result := newReplyAccumulator()
	sentText := false
	lastPublicCompleted := ""
	pendingThought := false
	lastThoughtCompleted := ""
	for evt := range events {
		if trace != nil && evt != nil {
			_ = trace.Record(debugrecorder.KindRunnerEvent, evt)
		}
		if evt == nil {
			continue
		}

		if !sentText {
			if update, ok := progressUpdateFromRunnerEvent(evt); ok {
				if !sendProgressUpdate(
					ctx,
					out,
					run,
					&progress,
					update.stage,
					update.summary,
				) {
					return streamOutcome{
						status: traceStatusError,
						errMsg: contextErrMessage(ctx),
					}
				}
			}
		}

		if apiErr := apiErrorFromEvent(evt); apiErr != nil {
			requestID := resolvedStreamRequestID(
				evt.RequestID,
				run.requestID,
			)
			_ = sendStreamEvent(ctx, out, gwproto.StreamEvent{
				Type:      gwproto.StreamEventTypeRunError,
				SessionID: run.sessionID,
				RequestID: requestID,
				Error:     apiErr,
			})
			return streamOutcome{
				status: traceStatusError,
				errMsg: apiErr.Message,
			}
		}

		result.Consume(evt)
		publicDelta := streamPublicDelta(evt)
		if publicDelta != "" {
			if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
				Type:      gwproto.StreamEventTypePublicDelta,
				SessionID: run.sessionID,
				RequestID: resolvedStreamRequestID(
					result.RequestID,
					run.requestID,
				),
				Delta: publicDelta,
			}) {
				return streamOutcome{
					status: traceStatusError,
					errMsg: contextErrMessage(ctx),
				}
			}
		}
		publicReply := streamPublicCompleted(evt)
		if shouldSendPublicCompleted(
			evt,
			publicReply,
			lastPublicCompleted,
		) {
			if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
				Type:      gwproto.StreamEventTypePublicCompleted,
				SessionID: run.sessionID,
				RequestID: resolvedStreamRequestID(
					result.RequestID,
					run.requestID,
				),
				Reply: publicReply,
			}) {
				return streamOutcome{
					status: traceStatusError,
					errMsg: contextErrMessage(ctx),
				}
			}
			lastPublicCompleted = publicReply
		}
		thoughtDelta := streamThoughtDelta(evt)
		if thoughtDelta != "" {
			if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
				Type:      gwproto.StreamEventTypeThoughtDelta,
				SessionID: run.sessionID,
				RequestID: resolvedStreamRequestID(
					result.RequestID,
					run.requestID,
				),
				Delta: thoughtDelta,
			}) {
				return streamOutcome{
					status: traceStatusError,
					errMsg: contextErrMessage(ctx),
				}
			}
			pendingThought = true
		}
		thoughtReply := streamThoughtCompleted(evt)
		if shouldSendThoughtCompleted(
			evt,
			thoughtReply,
			pendingThought,
			lastThoughtCompleted,
		) {
			if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
				Type:      gwproto.StreamEventTypeThoughtCompleted,
				SessionID: run.sessionID,
				RequestID: resolvedStreamRequestID(
					result.RequestID,
					run.requestID,
				),
				Reply: thoughtReply,
			}) {
				return streamOutcome{
					status: traceStatusError,
					errMsg: contextErrMessage(ctx),
				}
			}
			lastThoughtCompleted = thoughtReply
			pendingThought = false
		}
		delta := streamDeltaText(evt, sentText)
		if delta == "" {
			continue
		}
		sentText = true
		if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
			Type:      gwproto.StreamEventTypeMessageDelta,
			SessionID: run.sessionID,
			RequestID: resolvedStreamRequestID(
				result.RequestID,
				run.requestID,
			),
			Delta: delta,
		}) {
			return streamOutcome{
				status: traceStatusError,
				errMsg: contextErrMessage(ctx),
			}
		}
	}

	if result.Error != nil {
		if errors.Is(result.Error, context.Canceled) {
			requestID := resolvedStreamRequestID(
				result.RequestID,
				run.requestID,
			)
			_ = sendStreamEvent(ctx, out, gwproto.StreamEvent{
				Type:      gwproto.StreamEventTypeRunCanceled,
				SessionID: run.sessionID,
				RequestID: requestID,
			})
			return streamOutcome{
				status: traceStatusCanceled,
				errMsg: context.Canceled.Error(),
			}
		}
		apiErr := gwproto.APIError{
			Type:    errTypeInternal,
			Message: result.Error.Error(),
		}
		_ = sendStreamEvent(ctx, out, gwproto.StreamEvent{
			Type:      gwproto.StreamEventTypeRunError,
			SessionID: run.sessionID,
			RequestID: resolvedStreamRequestID(
				result.RequestID,
				run.requestID,
			),
			Error: &apiErr,
		})
		return streamOutcome{
			status: traceStatusError,
			errMsg: result.Error.Error(),
		}
	}

	requestID := resolvedStreamRequestID(
		result.RequestID,
		run.requestID,
	)
	if s.canceled != nil && s.canceled.Take(requestID) {
		if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
			Type:      gwproto.StreamEventTypeRunCanceled,
			SessionID: run.sessionID,
			RequestID: requestID,
		}) {
			return streamOutcome{
				status: traceStatusError,
				errMsg: contextErrMessage(ctx),
			}
		}
		return streamOutcome{
			status: traceStatusCanceled,
			errMsg: runCanceledMessage,
		}
	}

	reply := strings.TrimSpace(result.Text)
	if reply == "" {
		reply = emptyReplyFallbackText
	}
	if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
		Type:      gwproto.StreamEventTypeMessageCompleted,
		SessionID: run.sessionID,
		RequestID: requestID,
		Reply:     reply,
		Usage:     cloneGatewayUsage(result.Usage),
	}) {
		return streamOutcome{
			status: traceStatusError,
			errMsg: contextErrMessage(ctx),
		}
	}
	if !sendStreamEvent(ctx, out, gwproto.StreamEvent{
		Type:      gwproto.StreamEventTypeRunCompleted,
		SessionID: run.sessionID,
		RequestID: requestID,
		Usage:     cloneGatewayUsage(result.Usage),
	}) {
		return streamOutcome{
			status: traceStatusError,
			errMsg: contextErrMessage(ctx),
		}
	}
	return streamOutcome{status: traceStatusOK}
}

func sendStreamEvent(
	ctx context.Context,
	out chan<- gwproto.StreamEvent,
	evt gwproto.StreamEvent,
) bool {
	select {
	case out <- evt:
		return true
	case <-ctx.Done():
		return false
	}
}

type progressUpdate struct {
	stage   gwproto.StreamProgressStage
	summary string
}

func progressUpdateFromRunnerEvent(
	evt *event.Event,
) (progressUpdate, bool) {
	if evt == nil || evt.Response == nil {
		return progressUpdate{}, false
	}
	if evt.Response.IsToolCallResponse() {
		toolCall, ok := firstToolCall(evt.Response)
		if !ok {
			return progressUpdate{}, false
		}
		return progressFromToolCall(toolCall)
	}
	if evt.Object == model.ObjectTypeToolResponse {
		return progressUpdate{
			stage:   gwproto.StreamProgressStageSummarizing,
			summary: progressSummaryAnswering,
		}, true
	}
	return progressUpdate{}, false
}

func firstToolCall(rsp *model.Response) (model.ToolCall, bool) {
	if rsp == nil {
		return model.ToolCall{}, false
	}
	for _, choice := range rsp.Choices {
		if len(choice.Message.ToolCalls) > 0 {
			return choice.Message.ToolCalls[0], true
		}
		if len(choice.Delta.ToolCalls) > 0 {
			return choice.Delta.ToolCalls[0], true
		}
	}
	return model.ToolCall{}, false
}

func progressFromToolCall(
	toolCall model.ToolCall,
) (progressUpdate, bool) {
	name := strings.TrimSpace(toolCall.Function.Name)
	switch name {
	case streamToolReadDocument:
		return progressUpdate{
			stage:   gwproto.StreamProgressStageReadingDocument,
			summary: readDocumentProgressSummary(toolCall),
		}, true
	case streamToolReadSheet:
		return progressUpdate{
			stage:   gwproto.StreamProgressStageReadingSpreadsheet,
			summary: readSpreadsheetProgressSummary(toolCall),
		}, true
	case streamToolExecCommand:
		return progressUpdate{
			stage:   gwproto.StreamProgressStageRunningTool,
			summary: execCommandProgressSummary(toolCall),
		}, true
	default:
		if name == "" {
			return progressUpdate{}, false
		}
		return progressUpdate{
			stage:   gwproto.StreamProgressStageRunningTool,
			summary: "Running " + name,
		}, true
	}
}

func execCommandProgressSummary(toolCall model.ToolCall) string {
	const defaultSummary = progressSummaryTool

	var args struct {
		Command string `json:"command,omitempty"`
	}
	if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
		return defaultSummary
	}

	command := normalizeExecCommand(args.Command)
	switch {
	case command == "":
		return defaultSummary
	case strings.HasPrefix(command, "go test"):
		return progressSummaryGoTest
	case strings.HasPrefix(command, "pytest"),
		strings.HasPrefix(command, "python -m pytest"):
		return progressSummaryPytest
	case strings.HasPrefix(command, "npm test"),
		strings.HasPrefix(command, "pnpm test"),
		strings.HasPrefix(command, "yarn test"),
		strings.HasPrefix(command, "bun test"):
		return progressSummaryNPMTest
	case strings.HasPrefix(command, "git "):
		return progressSummaryGit
	case looksLikeWorkspaceInspection(command):
		return progressSummaryInspect
	default:
		return defaultSummary
	}
}

func normalizeExecCommand(command string) string {
	command = strings.ToLower(strings.TrimSpace(command))
	return strings.Join(strings.Fields(command), " ")
}

func looksLikeWorkspaceInspection(command string) bool {
	for _, prefix := range []string{
		"ls",
		"find",
		"rg ",
		"cat ",
		"sed ",
		"head ",
		"tail ",
		"pwd",
	} {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func readDocumentProgressSummary(toolCall model.ToolCall) string {
	const defaultSummary = progressSummaryDoc

	var args struct {
		Page *int `json:"page,omitempty"`
	}
	if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
		return defaultSummary
	}
	if args.Page == nil || *args.Page <= 0 {
		return defaultSummary
	}
	return fmt.Sprintf("%s page %d", defaultSummary, *args.Page)
}

func readSpreadsheetProgressSummary(toolCall model.ToolCall) string {
	const defaultSummary = progressSummarySheet

	var args struct {
		Row      *int   `json:"row,omitempty"`
		StartRow *int   `json:"start_row,omitempty"`
		EndRow   *int   `json:"end_row,omitempty"`
		Sheet    string `json:"sheet,omitempty"`
	}
	if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
		return defaultSummary
	}
	if args.Row != nil && *args.Row > 0 {
		return fmt.Sprintf("%s row %d", defaultSummary, *args.Row)
	}
	if args.StartRow != nil && *args.StartRow > 0 {
		if args.EndRow != nil && *args.EndRow >= *args.StartRow {
			return fmt.Sprintf(
				"%s rows %d-%d",
				defaultSummary,
				*args.StartRow,
				*args.EndRow,
			)
		}
		return fmt.Sprintf("%s from row %d", defaultSummary,
			*args.StartRow)
	}
	if strings.TrimSpace(args.Sheet) != "" {
		return fmt.Sprintf("%s sheet %s", defaultSummary,
			strings.TrimSpace(args.Sheet))
	}
	return defaultSummary
}

func sendProgressUpdate(
	ctx context.Context,
	out chan<- gwproto.StreamEvent,
	run preparedMessageRun,
	state *progressState,
	stage gwproto.StreamProgressStage,
	summary string,
) bool {
	if state == nil || stage == "" {
		return true
	}
	if state.stage == stage && state.summary == summary {
		return true
	}
	state.stage = stage
	state.summary = summary
	return sendStreamEvent(ctx, out, gwproto.StreamEvent{
		Type:      gwproto.StreamEventTypeRunProgress,
		SessionID: run.sessionID,
		RequestID: run.requestID,
		Stage:     stage,
		Summary:   summary,
		ElapsedMS: time.Since(state.startedAt).Milliseconds(),
	})
}

func singleStreamEvents(
	events ...gwproto.StreamEvent,
) <-chan gwproto.StreamEvent {
	out := make(chan gwproto.StreamEvent, len(events))
	for _, evt := range events {
		out <- evt
	}
	close(out)
	return out
}

func contextErrMessage(ctx context.Context) string {
	if ctx == nil || ctx.Err() == nil {
		return "stream canceled"
	}
	return ctx.Err().Error()
}

func resolvedStreamRequestID(
	requestID string,
	fallback string,
) string {
	requestID = strings.TrimSpace(requestID)
	if requestID != "" {
		return requestID
	}
	return strings.TrimSpace(fallback)
}

func apiErrorFromEvent(evt *event.Event) *gwproto.APIError {
	if evt == nil || evt.Response == nil || evt.Error == nil {
		return nil
	}

	errType := strings.TrimSpace(evt.Error.Type)
	if errType == "" {
		errType = errTypeInternal
	}
	return &gwproto.APIError{
		Type:    errType,
		Message: evt.Error.Message,
	}
}

func streamDeltaText(
	evt *event.Event,
	sentText bool,
) string {
	if evt == nil || evt.Response == nil {
		return ""
	}

	switch evt.Object {
	case model.ObjectTypeChatCompletionChunk:
		return deltaTextFromResponse(evt.Response)
	case model.ObjectTypeChatCompletion:
		if sentText {
			return ""
		}
		return fullTextFromResponse(evt.Response)
	default:
		return ""
	}
}

func streamPublicDelta(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return ""
	}
	if evt.Object != model.ObjectTypeChatCompletionChunk {
		return ""
	}
	return deltaPublicFromResponse(evt.Response)
}

func streamPublicCompleted(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return ""
	}
	if evt.Object != model.ObjectTypeChatCompletion {
		return ""
	}
	return fullPublicFromResponse(evt.Response)
}

func shouldSendPublicCompleted(
	evt *event.Event,
	publicReply string,
	lastPublicCompleted string,
) bool {
	if evt == nil || publicReply == "" {
		return false
	}
	if evt.Object != model.ObjectTypeChatCompletion {
		return false
	}
	return publicReply != lastPublicCompleted
}

func streamThoughtDelta(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return ""
	}
	if evt.Object != model.ObjectTypeChatCompletionChunk {
		return ""
	}
	return deltaThoughtFromResponse(evt.Response)
}

func streamThoughtCompleted(evt *event.Event) string {
	if evt == nil || evt.Response == nil {
		return ""
	}
	if evt.Object == model.ObjectTypeChatCompletion {
		return fullThoughtFromResponse(evt.Response)
	}
	if !evt.IsRunnerCompletion() {
		return ""
	}
	return fullThoughtFromResponse(evt.Response)
}

func shouldSendThoughtCompleted(
	evt *event.Event,
	thoughtReply string,
	pendingThought bool,
	lastThoughtCompleted string,
) bool {
	if evt == nil || thoughtReply == "" {
		return false
	}
	if evt.Object == model.ObjectTypeChatCompletion {
		return true
	}
	if !evt.IsRunnerCompletion() {
		return false
	}
	if pendingThought {
		return true
	}
	return thoughtReply != lastThoughtCompleted
}

func fullTextFromResponse(rsp *model.Response) string {
	if responseHasPublicContent(rsp) {
		return ""
	}
	if rsp == nil || len(rsp.Choices) == 0 {
		return ""
	}
	return rsp.Choices[0].Message.Content
}

func fullPublicFromResponse(rsp *model.Response) string {
	if !responseHasPublicContent(rsp) {
		return ""
	}
	if rsp == nil || len(rsp.Choices) == 0 {
		return ""
	}
	return rsp.Choices[0].Message.Content
}

func fullThoughtFromResponse(rsp *model.Response) string {
	if rsp == nil || len(rsp.Choices) == 0 {
		return ""
	}
	return rsp.Choices[0].Message.ReasoningContent
}

func deltaTextFromResponse(rsp *model.Response) string {
	if responseHasPublicContent(rsp) {
		return ""
	}
	if rsp == nil {
		return ""
	}
	var builder strings.Builder
	for _, choice := range rsp.Choices {
		if choice.Delta.Content == "" {
			continue
		}
		builder.WriteString(choice.Delta.Content)
	}
	return builder.String()
}

func deltaPublicFromResponse(rsp *model.Response) string {
	if !responseHasPublicContent(rsp) {
		return ""
	}
	if rsp == nil {
		return ""
	}
	var builder strings.Builder
	for _, choice := range rsp.Choices {
		if choice.Delta.Content == "" {
			continue
		}
		builder.WriteString(choice.Delta.Content)
	}
	if builder.Len() != 0 {
		return builder.String()
	}
	if len(rsp.Choices) == 0 {
		return ""
	}
	return rsp.Choices[0].Message.Content
}

func deltaThoughtFromResponse(rsp *model.Response) string {
	if rsp == nil {
		return ""
	}
	var builder strings.Builder
	for _, choice := range rsp.Choices {
		if choice.Delta.ReasoningContent == "" {
			continue
		}
		builder.WriteString(choice.Delta.ReasoningContent)
	}
	return builder.String()
}

func responseHasPublicContent(rsp *model.Response) bool {
	if rsp == nil || !rsp.IsToolCallResponse() || len(rsp.Choices) == 0 {
		return false
	}
	for _, choice := range rsp.Choices {
		if strings.TrimSpace(choice.Message.Content) != "" {
			return true
		}
		if strings.TrimSpace(choice.Delta.Content) != "" {
			return true
		}
	}
	return false
}
