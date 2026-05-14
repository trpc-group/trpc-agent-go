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
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode"

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

	streamToolExecCommand  = "exec_command"
	streamToolReadDocument = "read_document"
	streamToolReadSheet    = "read_spreadsheet"
	streamToolReadFile     = "fs_read_file"
	streamToolSaveFile     = "fs_save_file"
	streamToolListDir      = "fs_list_dir"
	streamToolSearch       = "fs_search"
	streamToolApplyPatch   = "apply_patch"

	streamToolArgCommand   = "command"
	streamToolArgPath      = "path"
	streamToolArgQuery     = "query"
	streamToolArgPattern   = "pattern"
	streamToolArgSkill     = "skill"
	streamToolArgDocs      = "docs"
	streamToolArgAction    = "action"
	streamToolArgOperation = "operation"
	streamToolArgURL       = "url"
	streamToolArgURLs      = "urls"
	streamToolArgFile      = "file"
	streamToolArgFilePath  = "file_path"
	streamToolArgFileName  = "file_name"
	streamToolArgFilename  = "filename"
	streamToolArgID        = "id"
	streamToolArgName      = "name"
	streamToolArgTitle     = "title"
	streamToolArgTarget    = "target"
	streamToolArgElement   = "element"
	streamToolArgSelector  = "selector"
	streamToolArgRef       = "ref"
	streamToolArgKey       = "key"
	streamToolArgJobID     = "job_id"
	streamToolArgSession   = "session_id"
	streamToolArgRow       = "row"
	streamToolArgStartRow  = "start_row"
	streamToolArgEndRow    = "end_row"
	streamToolArgSheet     = "sheet"

	streamToolDetailRowsPrefix  = "rows "
	streamToolDetailRowPrefix   = "row "
	streamToolDetailPagePrefix  = "page "
	streamToolDetailSheetPrefix = "sheet "
	streamToolDetailJobPrefix   = "job "
	streamToolDetailSessPrefix  = "session "
	streamToolDetailRefPrefix   = "ref "
	streamToolDetailKeyPrefix   = "key "
	streamToolDetailSeparator   = " "
	streamToolDetailMaxRunes    = 96
	streamToolDetailMaxParts    = 2
	streamToolPathEllipsis      = "..."
	streamToolPathSeparator     = "/"

	streamCommandCD        = "cd"
	streamCommandSet       = "set"
	streamCommandExport    = "export"
	streamCommandSource    = "source"
	streamCommandDot       = "."
	streamCommandFor       = "for"
	streamCommandShellLoop = "shell loop"
	streamCommandBash      = "bash"
	streamCommandSh        = "sh"
	streamCommandZsh       = "zsh"
	streamCommandGo        = "go"
	streamCommandGit       = "git"
	streamCommandRG        = "rg"
	streamCommandSed       = "sed"
	streamCommandCat       = "cat"
	streamCommandHead      = "head"
	streamCommandTail      = "tail"
	streamCommandLS        = "ls"
	streamCommandFind      = "find"
	streamCommandPytest    = "pytest"
	streamCommandPython    = "python"
	streamCommandPython3   = "python3"
	streamCommandNPM       = "npm"
	streamCommandPNPM      = "pnpm"
	streamCommandYarn      = "yarn"
	streamCommandBun       = "bun"

	streamCommandModuleFlag = "-m"
	streamCommandShellFlag  = "-c"
	streamCommandShellLogin = "-lc"
	streamCommandArgLimit   = 4

	streamSecretToken         = "token"
	streamSecretSecret        = "secret"
	streamSecretPassword      = "password"
	streamSecretAuthorization = "authorization"
	streamSecretBearer        = "bearer"
	streamSecretAPIKey        = "api_key"
	streamSecretAPIKeyFlat    = "apikey"
	streamSecretCookie        = "cookie"
	streamSecretKeySuffix     = "_key"
	streamSecretOpenAIKey     = "sk-"
	streamSecretGitHubToken   = "ghp_"
	streamSecretTencentToken  = "tgit_"

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
	startedAt  time.Time
	stage      gwproto.StreamProgressStage
	summary    string
	toolName   string
	toolDetail string
	toolCallID string
	toolStatus gwproto.StreamToolStatus
}

type streamToolArgKind int

const (
	streamToolArgKindText streamToolArgKind = iota
	streamToolArgKindPath
	streamToolArgKindURL
)

type streamToolDetailArg struct {
	key    string
	prefix string
	kind   streamToolArgKind
}

var streamGenericToolDetailArgs = []streamToolDetailArg{
	{key: streamToolArgSkill},
	{key: streamToolArgDocs, kind: streamToolArgKindPath},
	{key: streamToolArgAction},
	{key: streamToolArgOperation},
	{key: streamToolArgQuery},
	{key: streamToolArgPattern},
	{key: streamToolArgURL, kind: streamToolArgKindURL},
	{key: streamToolArgURLs, kind: streamToolArgKindURL},
	{key: streamToolArgPath, kind: streamToolArgKindPath},
	{key: streamToolArgFilePath, kind: streamToolArgKindPath},
	{key: streamToolArgFileName, kind: streamToolArgKindPath},
	{key: streamToolArgFile, kind: streamToolArgKindPath},
	{key: streamToolArgFilename, kind: streamToolArgKindPath},
	{key: streamToolArgID},
	{key: streamToolArgName},
	{key: streamToolArgTitle},
	{key: streamToolArgTarget},
	{key: streamToolArgElement},
	{key: streamToolArgSelector},
	{key: streamToolArgRef, prefix: streamToolDetailRefPrefix},
	{key: streamToolArgKey, prefix: streamToolDetailKeyPrefix},
	{key: streamToolArgJobID, prefix: streamToolDetailJobPrefix},
	{key: streamToolArgSession, prefix: streamToolDetailSessPrefix},
}

// StreamMessage processes a request and returns a stream of gateway
// events. Validation errors are returned as APIError/status pairs.
func (s *Server) StreamMessage(
	ctx context.Context,
	req gwproto.MessageRequest,
) (<-chan gwproto.StreamEvent, *gwproto.APIError, int) {
	return s.streamMessage(ctx, req, nil)
}

// StreamMessageWithOptions processes a request with opt-in stream behavior
// controls and returns a stream of gateway events.
func (s *Server) StreamMessageWithOptions(
	ctx context.Context,
	req gwproto.MessageRequest,
	opts *gwproto.MessageStreamOptions,
) (<-chan gwproto.StreamEvent, *gwproto.APIError, int) {
	return s.streamMessage(ctx, req, opts)
}

func (s *Server) streamMessage(
	ctx context.Context,
	req gwproto.MessageRequest,
	opts *gwproto.MessageStreamOptions,
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
		opts,
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

	var req streamMessageRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, gwproto.APIError{
			Type:    errTypeInvalidRequest,
			Message: err.Error(),
		}, http.StatusBadRequest)
		return
	}

	events, apiErr, status := s.streamMessage(
		r.Context(),
		req.MessageRequest,
		req.StreamOptions,
	)
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

type streamMessageRequest struct {
	gwproto.MessageRequest
	StreamOptions *gwproto.MessageStreamOptions `json:"stream_options,omitempty"`
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
		progressUpdate{
			stage:   gwproto.StreamProgressStagePreparing,
			summary: progressSummaryPrepare,
		},
	) {
		return streamOutcome{
			status: traceStatusError,
			errMsg: contextErrMessage(ctx),
		}
	}

	ctx, runOpts, err := s.resolveRunOptions(ctx, run)
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
	recordRuntimeProfile(debugrecorder.TraceFromContext(ctx), ctx)
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

		if update, ok := progressUpdateFromRunnerEvent(evt); ok &&
			shouldSendProgressForEvent(run.streamOptions, sentText) {
			if !sendProgressUpdate(
				ctx,
				out,
				run,
				&progress,
				update,
			) {
				return streamOutcome{
					status: traceStatusError,
					errMsg: contextErrMessage(ctx),
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

func shouldSendProgressForEvent(
	opts *gwproto.MessageStreamOptions,
	sentText bool,
) bool {
	if !sentText {
		return true
	}
	return opts != nil && opts.ProgressAfterTextDelta
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
	stage      gwproto.StreamProgressStage
	summary    string
	toolName   string
	toolDetail string
	toolCallID string
	toolStatus gwproto.StreamToolStatus
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
		return progressFromToolResult(evt.Response), true
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

func firstToolResult(rsp *model.Response) (model.Message, bool) {
	if rsp == nil {
		return model.Message{}, false
	}
	for _, choice := range rsp.Choices {
		if choice.Message.ToolID != "" || choice.Message.ToolName != "" {
			return choice.Message, true
		}
		if choice.Delta.ToolID != "" || choice.Delta.ToolName != "" {
			return choice.Delta, true
		}
	}
	return model.Message{}, false
}

func progressFromToolCall(
	toolCall model.ToolCall,
) (progressUpdate, bool) {
	name := strings.TrimSpace(toolCall.Function.Name)
	update := progressUpdate{
		toolName:   name,
		toolDetail: toolDetailFromToolCall(toolCall),
		toolCallID: strings.TrimSpace(toolCall.ID),
		toolStatus: gwproto.StreamToolStatusRunning,
	}
	switch name {
	case streamToolReadDocument:
		update.stage = gwproto.StreamProgressStageReadingDocument
		update.summary = readDocumentProgressSummary(toolCall)
		return update, true
	case streamToolReadSheet:
		update.stage = gwproto.StreamProgressStageReadingSpreadsheet
		update.summary = readSpreadsheetProgressSummary(toolCall)
		return update, true
	case streamToolExecCommand:
		update.stage = gwproto.StreamProgressStageRunningTool
		update.summary = execCommandProgressSummary(toolCall)
		return update, true
	default:
		if name == "" {
			return progressUpdate{}, false
		}
		update.stage = gwproto.StreamProgressStageRunningTool
		update.summary = progressSummaryTool
		return update, true
	}
}

func progressFromToolResult(rsp *model.Response) progressUpdate {
	update := progressUpdate{
		stage:   gwproto.StreamProgressStageSummarizing,
		summary: progressSummaryAnswering,
	}
	if rsp == nil || rsp.IsPartial {
		return update
	}
	msg, ok := firstToolResult(rsp)
	if !ok {
		return update
	}
	update.toolName = strings.TrimSpace(msg.ToolName)
	update.toolCallID = strings.TrimSpace(msg.ToolID)
	if update.toolName != "" || update.toolCallID != "" {
		update.toolStatus = gwproto.StreamToolStatusCompleted
	}
	return update
}

func toolDetailFromToolCall(toolCall model.ToolCall) string {
	name := strings.TrimSpace(toolCall.Function.Name)
	switch name {
	case streamToolExecCommand:
		return execCommandToolDetail(toolCall)
	case streamToolReadDocument:
		return readDocumentToolDetail(toolCall)
	case streamToolReadSheet:
		return readSpreadsheetToolDetail(toolCall)
	case streamToolReadFile, streamToolSaveFile, streamToolListDir:
		return toolPathDetail(toolCall)
	case streamToolSearch:
		return searchToolDetail(toolCall)
	default:
		return genericToolDetail(toolCall)
	}
}

func execCommandToolDetail(toolCall model.ToolCall) string {
	var args struct {
		Command string `json:"command,omitempty"`
	}
	if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
		return ""
	}
	return sanitizeStreamToolDetail(shellCommandDetail(args.Command))
}

func readDocumentToolDetail(toolCall model.ToolCall) string {
	var args struct {
		Page *int   `json:"page,omitempty"`
		Path string `json:"path,omitempty"`
	}
	if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
		return ""
	}
	details := make([]string, 0, 2)
	if path := safePathDetail(args.Path); path != "" {
		details = append(details, path)
	}
	if args.Page != nil && *args.Page > 0 {
		details = append(
			details,
			fmt.Sprintf("%s%d", streamToolDetailPagePrefix, *args.Page),
		)
	}
	return sanitizeStreamToolDetail(
		strings.Join(details, streamToolDetailSeparator),
	)
}

func readSpreadsheetToolDetail(toolCall model.ToolCall) string {
	var args struct {
		Path     string `json:"path,omitempty"`
		Row      *int   `json:"row,omitempty"`
		StartRow *int   `json:"start_row,omitempty"`
		EndRow   *int   `json:"end_row,omitempty"`
		Sheet    string `json:"sheet,omitempty"`
	}
	if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
		return ""
	}
	details := make([]string, 0, 3)
	if path := safePathDetail(args.Path); path != "" {
		details = append(details, path)
	}
	if sheet := safeDetailToken(args.Sheet); sheet != "" {
		details = append(details, streamToolDetailSheetPrefix+sheet)
	}
	switch {
	case args.Row != nil && *args.Row > 0:
		details = append(
			details,
			fmt.Sprintf("%s%d", streamToolDetailRowPrefix, *args.Row),
		)
	case args.StartRow != nil && *args.StartRow > 0 &&
		args.EndRow != nil && *args.EndRow >= *args.StartRow:
		details = append(
			details,
			fmt.Sprintf(
				"%s%d-%d",
				streamToolDetailRowsPrefix,
				*args.StartRow,
				*args.EndRow,
			),
		)
	case args.StartRow != nil && *args.StartRow > 0:
		details = append(
			details,
			fmt.Sprintf("%s%d", streamToolDetailRowPrefix, *args.StartRow),
		)
	}
	return sanitizeStreamToolDetail(
		strings.Join(details, streamToolDetailSeparator),
	)
}

func toolPathDetail(toolCall model.ToolCall) string {
	for _, key := range []string{
		streamToolArgPath,
		streamToolArgFilePath,
		streamToolArgFileName,
		streamToolArgFile,
		streamToolArgFilename,
	} {
		path, ok := stringArgFromToolCall(toolCall, key)
		if ok {
			return sanitizeStreamToolDetail(safePathDetail(path))
		}
	}
	return ""
}

func searchToolDetail(toolCall model.ToolCall) string {
	for _, key := range []string{
		streamToolArgQuery,
		streamToolArgPattern,
	} {
		if value, ok := stringArgFromToolCall(toolCall, key); ok {
			return sanitizeStreamToolDetail(safeDetailToken(value))
		}
	}
	return ""
}

func genericToolDetail(toolCall model.ToolCall) string {
	args, ok := toolCallArgs(toolCall)
	if !ok {
		return ""
	}
	parts := make([]string, 0, streamToolDetailMaxParts)
	for _, candidate := range streamGenericToolDetailArgs {
		if len(parts) >= streamToolDetailMaxParts {
			break
		}
		for _, value := range stringValuesFromMap(args, candidate.key) {
			if len(parts) >= streamToolDetailMaxParts {
				break
			}
			detail := toolDetailArgValue(candidate.kind, value)
			if detail == "" {
				continue
			}
			part := candidate.prefix + detail
			if containsToolDetailPart(parts, part) {
				continue
			}
			parts = append(parts, part)
		}
	}
	return sanitizeStreamToolDetail(
		strings.Join(parts, streamToolDetailSeparator),
	)
}

func toolDetailArgValue(kind streamToolArgKind, value string) string {
	switch kind {
	case streamToolArgKindPath:
		return safePathDetail(value)
	case streamToolArgKindURL:
		return safeURLDetail(value)
	default:
		return safeDetailToken(value)
	}
}

func containsToolDetailPart(parts []string, part string) bool {
	for _, existing := range parts {
		if existing == part {
			return true
		}
	}
	return false
}

func stringArgFromToolCall(
	toolCall model.ToolCall,
	key string,
) (string, bool) {
	args, ok := toolCallArgs(toolCall)
	if !ok {
		return "", false
	}
	return stringArgFromMap(args, key)
}

func toolCallArgs(toolCall model.ToolCall) (map[string]any, bool) {
	var args map[string]any
	if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
		return nil, false
	}
	return args, true
}

func stringArgFromMap(args map[string]any, key string) (string, bool) {
	values := stringValuesFromMap(args, key)
	if len(values) == 0 {
		return "", false
	}
	return values[0], true
}

func stringValuesFromMap(args map[string]any, key string) []string {
	if isSensitiveToolArgKey(key) {
		return nil
	}
	raw, ok := args[key]
	if !ok {
		return nil
	}
	switch value := raw.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		return []string{value}
	case []any:
		return stringValuesFromList(value)
	default:
		return nil
	}
}

func stringValuesFromList(values []any) []string {
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value, ok := raw.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func execCommandProgressSummary(toolCall model.ToolCall) string {
	const defaultSummary = progressSummaryTool

	var args struct {
		Command string `json:"command,omitempty"`
	}
	if err := json.Unmarshal(toolCall.Function.Arguments, &args); err != nil {
		return defaultSummary
	}

	command := normalizeExecCommand(shellCommandDetail(args.Command))
	switch {
	case command == "":
		return defaultSummary
	case strings.HasPrefix(command, "go test"):
		return progressSummaryGoTest
	case strings.HasPrefix(command, "pytest"),
		strings.HasPrefix(command, "python -m pytest"):
		return progressSummaryPytest
	case strings.HasPrefix(command, "npm test"),
		strings.HasPrefix(command, "npm run test"),
		strings.HasPrefix(command, "pnpm test"),
		strings.HasPrefix(command, "pnpm run test"),
		strings.HasPrefix(command, "yarn test"),
		strings.HasPrefix(command, "yarn run test"),
		strings.HasPrefix(command, "bun test"),
		strings.HasPrefix(command, "bun run test"):
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

func shellCommandDetail(command string) string {
	for _, segment := range shellCommandSegments(command) {
		detail := shellCommandSegmentDetail(segment)
		if detail != "" {
			return detail
		}
	}
	return ""
}

func shellCommandSegments(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	segments := splitShellSegments(command)
	return segments
}

func shellCommandSegmentDetail(segment string) string {
	fields := cleanCommandFields(segment)
	fields = trimShellCommandPrefix(fields)
	if len(fields) == 0 {
		return ""
	}
	command := safeCommandName(fields[0])
	if command == "" || skipShellSetupCommand(command) {
		return ""
	}
	switch command {
	case streamCommandFor:
		return streamCommandShellLoop
	case streamCommandBash, streamCommandSh, streamCommandZsh:
		return shellWrapperCommandDetail(fields[1:])
	case streamCommandGo:
		return commandWithSafeArgs(
			command,
			fields[1:],
			streamCommandArgLimit,
		)
	case streamCommandGit:
		return commandWithSafeArgs(
			command,
			fields[1:],
			streamCommandArgLimit,
		)
	case streamCommandRG:
		return commandWithSafeArgs(
			command,
			fields[1:],
			streamCommandArgLimit,
		)
	case streamCommandSed, streamCommandCat,
		streamCommandHead, streamCommandTail:
		return commandWithPathArg(command, fields[1:])
	case streamCommandLS, streamCommandFind:
		return commandWithPathArg(command, fields[1:])
	case streamCommandPytest:
		return commandWithSafeArgs(
			command,
			fields[1:],
			streamCommandArgLimit,
		)
	case streamCommandPython, streamCommandPython3:
		return pythonCommandDetail(command, fields[1:])
	case streamCommandNPM, streamCommandPNPM,
		streamCommandYarn, streamCommandBun:
		return packageCommandDetail(command, fields[1:])
	default:
		return commandWithSafeArgs(
			command,
			fields[1:],
			streamCommandArgLimit,
		)
	}
}

func splitShellSegments(command string) []string {
	runes := []rune(command)
	segments := make([]string, 0, 1)
	var builder strings.Builder
	var quote rune
	escaped := false
	for i := 0; i < len(runes); i++ {
		char := runes[i]
		if escaped {
			builder.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' {
			builder.WriteRune(char)
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			builder.WriteRune(char)
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
			builder.WriteRune(char)
		case '\n', ';', '|':
			segments = appendShellSegment(segments, builder.String())
			builder.Reset()
		case '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				segments = appendShellSegment(segments, builder.String())
				builder.Reset()
				i++
				continue
			}
			builder.WriteRune(char)
		default:
			builder.WriteRune(char)
		}
	}
	segments = appendShellSegment(segments, builder.String())
	return segments
}

func appendShellSegment(segments []string, segment string) []string {
	if segment = strings.TrimSpace(segment); segment != "" {
		segments = append(segments, segment)
	}
	return segments
}

func shellFields(segment string) []string {
	fields := make([]string, 0, 4)
	var builder strings.Builder
	var quote rune
	escaped := false
	for _, char := range segment {
		switch {
		case escaped:
			builder.WriteRune(char)
			escaped = false
		case char == '\\':
			escaped = true
		case quote != 0:
			if char == quote {
				quote = 0
				continue
			}
			builder.WriteRune(char)
		case char == '\'' || char == '"':
			quote = char
		case unicode.IsSpace(char):
			fields = appendShellField(fields, builder.String())
			builder.Reset()
		default:
			builder.WriteRune(char)
		}
	}
	fields = appendShellField(fields, builder.String())
	return fields
}

func appendShellField(fields []string, field string) []string {
	if field = strings.TrimSpace(field); field != "" {
		fields = append(fields, field)
	}
	return fields
}

func trimShellCommandPrefix(fields []string) []string {
	for len(fields) > 0 {
		first := strings.TrimSpace(fields[0])
		switch {
		case isShellEnvAssignment(first):
			fields = fields[1:]
		default:
			return fields
		}
	}
	return fields
}

func isShellEnvAssignment(field string) bool {
	if field == "" || strings.HasPrefix(field, "-") {
		return false
	}
	eq := strings.Index(field, "=")
	if eq <= 0 {
		return false
	}
	name := field[:eq]
	for _, char := range name {
		if char == '_' ||
			char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func cleanCommandFields(segment string) []string {
	return shellFields(segment)
}

func safeCommandName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	name = strings.ToLower(strings.Trim(name, "\"'"))
	if looksSensitiveValue(name) {
		return ""
	}
	return safeDetailToken(name)
}

func skipShellSetupCommand(command string) bool {
	switch command {
	case streamCommandCD, streamCommandSet, streamCommandExport,
		streamCommandSource, streamCommandDot:
		return true
	default:
		return false
	}
}

func commandWithSafeArgs(
	command string,
	args []string,
	limit int,
) string {
	parts := []string{command}
	skipCount := 0
	for _, arg := range args {
		if skipCount > 0 {
			skipCount--
			continue
		}
		if isSensitiveCommandFlag(arg) {
			skipCount = 1
			continue
		}
		if isSensitiveCommandKeyToken(arg) {
			skipCount = 2
			continue
		}
		if len(parts)-1 >= limit {
			break
		}
		if token := safeCommandArgDetail(arg); token != "" {
			parts = append(parts, token)
		}
	}
	return strings.Join(parts, streamToolDetailSeparator)
}

func isSensitiveCommandFlag(arg string) bool {
	arg = strings.TrimSpace(strings.Trim(arg, "\"'"))
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	flag := strings.TrimLeft(arg, "-")
	flag, _, _ = strings.Cut(flag, "=")
	return isSensitiveToolArgKey(flag)
}

func isSensitiveCommandKeyToken(arg string) bool {
	arg = strings.TrimSpace(strings.Trim(arg, "\"'"))
	if !strings.HasSuffix(arg, ":") {
		return false
	}
	key := strings.TrimSuffix(arg, ":")
	return isSensitiveToolArgKey(key)
}

func commandWithPathArg(command string, args []string) string {
	if path := lastSafePathArg(args); path != "" {
		return strings.Join(
			[]string{command, path},
			streamToolDetailSeparator,
		)
	}
	return command
}

func shellWrapperCommandDetail(args []string) string {
	for i, arg := range args {
		switch arg {
		case streamCommandShellFlag, streamCommandShellLogin:
			if i+1 >= len(args) {
				return ""
			}
			return shellCommandDetail(strings.Join(args[i+1:], " "))
		}
	}
	return ""
}

func pythonCommandDetail(command string, args []string) string {
	if len(args) >= 2 &&
		args[0] == streamCommandModuleFlag &&
		args[1] == streamCommandPytest {
		rest := append([]string{streamCommandPytest}, args[2:]...)
		return commandWithSafeArgs(
			streamCommandPytest,
			rest[1:],
			streamCommandArgLimit,
		)
	}
	return commandWithSafeArgs(command, args, streamCommandArgLimit)
}

func packageCommandDetail(command string, args []string) string {
	if len(args) == 0 {
		return command
	}
	return commandWithSafeArgs(command, args, streamCommandArgLimit)
}

func safeCommandArgDetail(arg string) string {
	arg = strings.TrimSpace(strings.Trim(arg, "\"'"))
	if arg == "" || looksSensitiveCommandArg(arg) {
		return ""
	}
	if strings.HasPrefix(arg, "-") && strings.Contains(arg, "=") {
		return ""
	}
	if strings.Contains(arg, "://") {
		return safeURLDetail(arg)
	}
	if path := safePathDetail(arg); path != "" {
		return path
	}
	return safeDetailToken(arg)
}

func lastSafePathArg(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		arg := strings.TrimSpace(args[i])
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		if path := safePathDetail(arg); path != "" {
			return path
		}
	}
	return ""
}

func safePathDetail(path string) string {
	path = strings.TrimSpace(strings.Trim(path, "\"'"))
	if path == "" || looksSensitiveValue(path) {
		return ""
	}
	if path == "./..." {
		return path
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == ".." {
		return clean
	}
	trimmed := strings.Trim(clean, streamToolPathSeparator)
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, streamToolPathSeparator)
	if len(parts) > 3 {
		parts = append([]string{streamToolPathEllipsis}, parts[len(parts)-3:]...)
	}
	return safeDetailToken(strings.Join(parts, streamToolPathSeparator))
}

func safeURLDetail(rawURL string) string {
	rawURL = strings.TrimSpace(strings.Trim(rawURL, "\"'"))
	if rawURL == "" || looksSensitiveValue(rawURL) {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return safePathDetail(rawURL)
	}
	parts := []string{parsed.Host}
	if path := safePathDetail(parsed.Path); path != "" && path != "." {
		parts = append(parts, path)
	}
	return safeDetailToken(
		strings.Join(parts, streamToolPathSeparator),
	)
}

func safeDetailToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" ||
		len([]rune(value)) > streamToolDetailMaxRunes ||
		looksSensitiveValue(value) {
		return ""
	}
	for _, char := range value {
		if isSafeToolDetailRune(char) {
			continue
		}
		return ""
	}
	return value
}

func sanitizeStreamToolDetail(detail string) string {
	return safeDetailToken(detail)
}

func isSafeToolDetailRune(char rune) bool {
	switch {
	case unicode.IsLetter(char), unicode.IsDigit(char):
		return true
	case char == '_', char == '-', char == '.', char == '/',
		char == ':', char == ' ', char == '*', char == '#',
		char == '@', char == '[', char == ']':
		return true
	default:
		return false
	}
}

func looksSensitiveCommandArg(arg string) bool {
	if looksSensitiveValue(arg) {
		return true
	}
	if !strings.HasPrefix(arg, "-") || !strings.Contains(arg, "=") {
		return false
	}
	key, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
	return isSensitiveToolArgKey(key)
}

func isSensitiveToolArgKey(key string) bool {
	key = normalizeSensitiveKey(key)
	if key == "" {
		return false
	}
	return strings.Contains(key, streamSecretToken) ||
		strings.Contains(key, streamSecretSecret) ||
		strings.Contains(key, streamSecretPassword) ||
		strings.Contains(key, streamSecretAuthorization) ||
		strings.Contains(key, streamSecretBearer) ||
		strings.Contains(key, streamSecretAPIKey) ||
		strings.Contains(key, streamSecretAPIKeyFlat) ||
		strings.Contains(key, streamSecretCookie) ||
		strings.HasSuffix(key, streamSecretKeySuffix)
}

func normalizeSensitiveKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	return key
}

func looksSensitiveValue(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return false
	}
	return value == streamSecretBearer ||
		strings.Contains(value, streamSecretBearer) ||
		strings.HasPrefix(value, streamSecretOpenAIKey) ||
		strings.HasPrefix(value, streamSecretGitHubToken) ||
		strings.HasPrefix(value, streamSecretTencentToken) ||
		looksLikeJWT(value)
}

func looksLikeJWT(value string) bool {
	if len(value) < 32 {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if len(part) < 8 || !isBase64URLLike(part) {
			return false
		}
	}
	return true
}

func isBase64URLLike(value string) bool {
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			continue
		case char >= 'A' && char <= 'Z':
			continue
		case char >= '0' && char <= '9':
			continue
		case char == '-', char == '_':
			continue
		default:
			return false
		}
	}
	return true
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
	update progressUpdate,
) bool {
	if state == nil || update.stage == "" {
		return true
	}
	if state.stage == update.stage &&
		state.summary == update.summary &&
		state.toolName == update.toolName &&
		state.toolDetail == update.toolDetail &&
		state.toolCallID == update.toolCallID &&
		state.toolStatus == update.toolStatus {
		return true
	}
	state.stage = update.stage
	state.summary = update.summary
	state.toolName = update.toolName
	state.toolDetail = update.toolDetail
	state.toolCallID = update.toolCallID
	state.toolStatus = update.toolStatus
	return sendStreamEvent(ctx, out, gwproto.StreamEvent{
		Type:       gwproto.StreamEventTypeRunProgress,
		SessionID:  run.sessionID,
		RequestID:  run.requestID,
		Stage:      update.stage,
		Summary:    update.summary,
		ToolName:   update.toolName,
		ToolDetail: update.toolDetail,
		ToolCallID: update.toolCallID,
		ToolStatus: update.toolStatus,
		ElapsedMS:  time.Since(state.startedAt).Milliseconds(),
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
