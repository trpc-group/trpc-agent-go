//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	defaultBasePath = "/trpc-agent/v1/apps"

	headerAllow               = "Allow"
	headerContentType         = "Content-Type"
	headerAccessControlOrigin = "Access-Control-Allow-Origin"

	contentTypeJSON = "application/json"
)

// Server exposes one registered app through the tRPC-Agent API.
type Server struct {
	basePath string
	timeout  time.Duration
	appName  string
	agent    agent.Agent
	runner   runner.Runner
	handler  http.Handler
}

// New creates a tRPC-Agent API server.
func New(opts ...Option) (*Server, error) {
	options := newOptions(opts...)
	appName := options.appName
	if appName == "" {
		return nil, errors.New("trpcagent: app name must not be empty")
	}
	server := &Server{
		basePath: options.basePath,
		timeout:  options.timeout,
		appName:  appName,
		agent:    options.agent,
		runner:   options.runner,
	}
	if err := server.setupHandler(); err != nil {
		return nil, err
	}
	return server, nil
}

// Handler returns the HTTP handler exposed by the server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// BasePath returns the base path exposed by the tRPC-Agent API server.
func (s *Server) BasePath() string {
	return s.basePath
}

func (s *Server) setupHandler() error {
	mux := http.NewServeMux()
	if s.agent != nil {
		path, err := s.appResourcePath("structure")
		if err != nil {
			return fmt.Errorf("build structure route: %w", err)
		}
		mux.HandleFunc(path, s.handleStructure)
	}
	if s.runner != nil {
		path, err := s.appResourcePath("runs")
		if err != nil {
			return fmt.Errorf("build runs route: %w", err)
		}
		mux.HandleFunc(path, s.handleRuns)
	}
	s.handler = mux
	return nil
}

func (s *Server) appResourcePath(resource string) (string, error) {
	return url.JoinPath(s.basePath, s.appName, resource)
}

func (s *Server) handleStructure(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, http.MethodGet)
		s.respondError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	structure, err := s.exportStructure(ctx)
	if err != nil {
		log.ErrorfContext(ctx, "trpcagent: export structure for app %q: %v", s.appName, err)
		s.respondError(w, r, http.StatusInternalServerError, "export structure failed")
		return
	}
	s.respondJSON(w, r, http.StatusOK, structureResponse{Structure: structure})
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, http.MethodPost)
		s.respondError(w, r, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx, cancel := newExecutionContext(r.Context(), s.timeout)
	defer cancel()
	var req runRequest
	if !s.decodeJSONRequestBody(w, r, &req) {
		return
	}
	if err := validateRunRequest(&req); err != nil {
		s.respondError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if req.RunOptions.RequestID == "" {
		req.RunOptions.RequestID = uuid.NewString()
	}
	runOptions, err := profilecompiler.CompileRunOptions(
		req.Profile,
		req.RunOptions.ExecutionTraceEnabled,
	)
	if err != nil {
		s.respondError(w, r, http.StatusBadRequest, fmt.Sprintf("compile profile: %v", err))
		return
	}
	runOptions = append(runOptions, agent.WithRequestID(req.RunOptions.RequestID))
	runOptions = append(runOptions, agent.WithAppName(s.appName))
	eventCh, err := s.runner.Run(ctx, req.Session.UserID, req.Session.SessionID, req.Input, runOptions...)
	if err != nil {
		log.ErrorfContext(ctx, "trpcagent: run app %q: %v", s.appName, err)
		s.respondJSON(w, r, http.StatusOK, runErrorResponse(req.Input, s.appName, req.RunOptions.RequestID, err))
		return
	}
	if eventCh == nil {
		log.ErrorfContext(ctx, "trpcagent: run app %q returned nil event channel", s.appName)
		s.respondJSON(w, r, http.StatusOK, runErrorResponse(req.Input, s.appName, req.RunOptions.RequestID, errors.New("runner returned nil event channel")))
		return
	}
	response, err := collectRunResponse(ctx, req.Input, eventCh, req.RunOptions.RequestID)
	if err != nil {
		log.ErrorfContext(ctx, "trpcagent: collect run response for app %q: %v", s.appName, err)
		s.respondError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	s.respondJSON(w, r, http.StatusOK, response)
}

func (s *Server) exportStructure(ctx context.Context) (*astructure.Snapshot, error) {
	snapshot, err := astructure.Export(ctx, s.agent)
	if err != nil {
		return nil, err
	}
	return profilecompiler.NormalizeStructureSnapshot(snapshot)
}

func runErrorResponse(input model.Message, appName string, requestID string, err error) runResponse {
	status := atrace.TraceStatusFailed
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = atrace.TraceStatusIncomplete
	}
	response := runResponse{
		Status:       status,
		Messages:     []model.Message{input},
		ErrorMessage: err.Error(),
	}
	appendRunTerminalEvents(&response, appName, requestID, err)
	return response
}

func validateRunRequest(req *runRequest) error {
	if req == nil {
		return errors.New("request is nil")
	}
	if req.Session.UserID == "" {
		return errors.New("session.userId is required")
	}
	if req.Session.SessionID == "" {
		return errors.New("session.sessionId is required")
	}
	if !req.Input.Role.IsValid() {
		return errors.New("input.role is invalid")
	}
	if req.Input.Role != model.RoleUser {
		return errors.New("input.role must be user")
	}
	if !model.HasPayload(req.Input) && len(req.Input.ToolCalls) == 0 && req.Input.ToolID == "" {
		return errors.New("input payload is required")
	}
	return nil
}

func collectRunResponse(
	ctx context.Context,
	input model.Message,
	eventCh <-chan *event.Event,
	requestID string,
) (runResponse, error) {
	collector := newMessageCollector(input)
	response := runResponse{Status: atrace.TraceStatusCompleted}
	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				collector.flushAll()
				response.Messages = collector.messagesList()
				if !lastEventIsRunnerCompletion(response.Events) {
					err := errors.New("runner event stream closed without terminal runner completion")
					response.Status = atrace.TraceStatusIncomplete
					if response.ErrorMessage == "" {
						response.ErrorMessage = err.Error()
					}
					return response, err
				}
				return response, nil
			}
			if evt == nil {
				continue
			}
			eventValue := *evt
			if eventValue.RequestID == "" {
				return response, errors.New("runner event request id is empty")
			}
			if eventValue.RequestID != requestID {
				return response, fmt.Errorf("runner event request id %q does not match run request id %q", eventValue.RequestID, requestID)
			}
			response.Events = append(response.Events, eventValue)
			collector.addEvent(&eventValue)
			if eventValue.ExecutionTrace != nil {
				response.ExecutionTrace = eventValue.ExecutionTrace
				response.Status = eventValue.ExecutionTrace.Status
			}
			if eventValue.Response != nil && eventValue.Response.Error != nil {
				response.ErrorMessage = eventValue.Response.Error.Message
				if response.ExecutionTrace == nil {
					response.Status = atrace.TraceStatusFailed
				}
			}
			if eventValue.IsRunnerCompletion() {
				collector.flushAll()
				response.Messages = collector.messagesList()
				return response, nil
			}
		case <-ctx.Done():
			return runResponse{}, ctx.Err()
		}
	}
}

func appendRunTerminalEvents(response *runResponse, appName string, requestID string, err error) {
	invocationID := terminalInvocationID(response)
	if !lastEventIsTerminalError(response.Events) {
		evt := event.NewErrorEvent(invocationID, appName, model.ErrorTypeRunError, err.Error())
		evt.RequestID = requestID
		response.Events = append(response.Events, *evt)
	}
	evt := event.NewResponseEvent(invocationID, appName, &model.Response{
		ID:      "trpcagent-runner-completion-" + uuid.NewString(),
		Object:  model.ObjectTypeRunnerCompletion,
		Created: time.Now().Unix(),
		Done:    true,
		Error: &model.ResponseError{
			Type:    model.ErrorTypeRunError,
			Message: err.Error(),
		},
	})
	evt.RequestID = requestID
	evt.ExecutionTrace = response.ExecutionTrace
	response.Events = append(response.Events, *evt)
}

func terminalInvocationID(response *runResponse) string {
	if response.ExecutionTrace != nil && response.ExecutionTrace.RootInvocationID != "" {
		return response.ExecutionTrace.RootInvocationID
	}
	for i := len(response.Events) - 1; i >= 0; i-- {
		if response.Events[i].InvocationID != "" {
			return response.Events[i].InvocationID
		}
	}
	return "trpcagent-run-" + uuid.NewString()
}

func lastEventIsRunnerCompletion(events []event.Event) bool {
	return len(events) > 0 && events[len(events)-1].IsRunnerCompletion()
}

func lastEventIsTerminalError(events []event.Event) bool {
	return len(events) > 0 && events[len(events)-1].IsTerminalError()
}

func (s *Server) decodeJSONRequestBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		s.respondError(w, r, http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
		return false
	}
	extraErr := decoder.Decode(&struct{}{})
	if extraErr != io.EOF {
		s.respondError(w, r, http.StatusBadRequest, "invalid request body: request body must contain a single JSON object")
		return false
	}
	return true
}

func (s *Server) respondJSON(w http.ResponseWriter, r *http.Request, statusCode int, payload any) {
	var body bytes.Buffer
	if err := json.NewEncoder(&body).Encode(payload); err != nil {
		log.Errorf("trpcagent: encode response for %s %s: %v", r.Method, r.URL.RequestURI(), err)
		w.Header().Set(headerContentType, contentTypeJSON)
		w.Header().Set(headerAccessControlOrigin, "*")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	w.Header().Set(headerAccessControlOrigin, "*")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		log.Errorf("trpcagent: write response for %s %s: %v", r.Method, r.URL.RequestURI(), err)
	}
}

func (s *Server) respondError(w http.ResponseWriter, r *http.Request, statusCode int, message string) {
	s.respondJSON(w, r, statusCode, map[string]string{"error": message})
}

func (s *Server) handleCORS(w http.ResponseWriter) {
	w.Header().Set(headerAccessControlOrigin, "*")
	w.Header().Set("Access-Control-Allow-Methods", strings.Join([]string{http.MethodGet, http.MethodPost, http.MethodOptions}, ", "))
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.WriteHeader(http.StatusNoContent)
}

func newExecutionContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if timeout == 0 || remaining < timeout {
			timeout = remaining
		}
	}
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return context.WithCancel(ctx)
}
