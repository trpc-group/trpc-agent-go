//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package debug provides a HTTP server for debugging and testing.
package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/service"
	evalservice "trpc.group/trpc-go/trpc-agent-go/evaluation/service/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/debug/internal/schema"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

// Server exposes HTTP endpoints compatible with the ADK Web UI. Internally it
// reuses the trpc-agent-go components for sessions, runners and events.
type Server struct {
	agents map[string]agent.Agent
	router *mux.Router

	mu      sync.RWMutex
	runners map[string]runner.Runner

	sessionSvc session.Service
	runnerOpts []runner.Option // Extra options applied when creating a runner.

	evalSetManager    evalset.Manager    // evalSetManager is the manager for evaluation sets.
	evalResultManager evalresult.Manager // evalResultManager is the manager for evaluation results.
	metricManager     metric.Manager     // metricManager persists configured eval metrics per eval set.
	metricRegistry    registry.Registry  // metricRegistry exposes the available evaluation metrics.

	traces         map[string]attribute.Set // key: event_id
	memoryExporter *inMemoryExporter
}

// Option configures the Server instance.
type Option func(*Server)

// WithSessionService allows providing a custom session storage backend.
// If omitted, an in-memory implementation is used.
func WithSessionService(svc session.Service) Option {
	return func(s *Server) { s.sessionSvc = svc }
}

// WithRunnerOptions appends additional runner.Option values applied when the
// server lazily constructs a Runner for an agent.
func WithRunnerOptions(opts ...runner.Option) Option {
	return func(s *Server) { s.runnerOpts = append(s.runnerOpts, opts...) }
}

// WithEvalSetManager overrides the default eval set manager.
func WithEvalSetManager(m evalset.Manager) Option {
	return func(s *Server) {
		if m != nil {
			s.evalSetManager = m
		}
	}
}

// WithEvalResultManager overrides the default eval result manager.
func WithEvalResultManager(m evalresult.Manager) Option {
	return func(s *Server) {
		if m != nil {
			s.evalResultManager = m
		}
	}
}

// WithMetricManager overrides the default eval metric manager used for persistence.
func WithMetricManager(m metric.Manager) Option {
	return func(s *Server) {
		if m != nil {
			s.metricManager = m
		}
	}
}

// WithMetricRegistry overrides the default evaluator registry used to describe metrics.
func WithMetricRegistry(reg registry.Registry) Option {
	return func(s *Server) {
		if reg != nil {
			s.metricRegistry = reg
		}
	}
}

// WithEvaluatorRegistry is kept for backward compatibility. Use WithMetricRegistry instead.
func WithEvaluatorRegistry(reg registry.Registry) Option {
	return WithMetricRegistry(reg)
}

// New creates a new CLI HTTP server with explicit agent registration. The
// behaviour can be tweaked via functional options.
func New(agents map[string]agent.Agent, opts ...Option) *Server {
	s := &Server{
		agents:            agents,
		router:            mux.NewRouter(),
		runners:           make(map[string]runner.Runner),
		traces:            make(map[string]attribute.Set),
		memoryExporter:    newInMemoryExporter(),
		sessionSvc:        sessioninmemory.NewSessionService(),
		evalSetManager:    evalsetinmemory.New(),
		evalResultManager: evalresultinmemory.New(),
		metricManager:     metricinmemory.New(),
		metricRegistry:    registry.New(),
	}

	// Apply user-provided options.
	for _, opt := range opts {
		opt(s)
	}

	// Add CORS middleware for ADK Web compatibility.
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"Content-Length", "Content-Type"},
	})
	s.router.Use(c.Handler)
	s.registerRoutes()
	var tp *sdktrace.TracerProvider

	if _, ok := atrace.TracerProvider.(noop.TracerProvider); ok {
		tp = sdktrace.NewTracerProvider()
	} else if tp, ok = atrace.TracerProvider.(*sdktrace.TracerProvider); !ok {
		log.Errorf("atrace.Tracer: %T provider is not the type of sdktrace.TracerProvider", atrace.TracerProvider)
	}
	tp.RegisterSpanProcessor(sdktrace.NewSimpleSpanProcessor(newApiServerSpanExporter(s.traces)))
	tp.RegisterSpanProcessor(sdktrace.NewSimpleSpanProcessor(s.memoryExporter))
	atrace.TracerProvider = tp
	atrace.Tracer = atrace.TracerProvider.Tracer(itelemetry.InstrumentName)
	setTraceInfo()
	return s
}

const (
	keyEventID      = "gcp.vertex.agent.event_id"
	keySessionID    = "gcp.vertex.agent.session_id"
	keyInvocationID = "gcp.vertex.agent.invocation_id"
	keyLLMRequest   = "gcp.vertex.agent.llm_request"
	keyLLMResponse  = "gcp.vertex.agent.llm_response"
)

func setTraceInfo() {
	itelemetry.KeyEventID = keyEventID
	itelemetry.KeyGenAIConversationID = keySessionID
	itelemetry.KeyLLMRequest = keyLLMRequest
	itelemetry.KeyLLMResponse = keyLLMResponse
	itelemetry.KeyInvocationID = keyInvocationID
	itelemetry.KeyRunnerInput = keyLLMRequest
	itelemetry.KeyRunnerOutput = keyLLMResponse
	itelemetry.KeyRunnerSessionID = keySessionID
}

type apiServerSpanExporter struct {
	traces map[string]attribute.Set
}

func newApiServerSpanExporter(ts map[string]attribute.Set) *apiServerSpanExporter {
	return &apiServerSpanExporter{traces: ts}
}

func (e *apiServerSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		if name := span.Name(); !strings.HasPrefix(name, itelemetry.OperationChat) && !strings.HasPrefix(name, itelemetry.OperationExecuteTool) {
			continue
		}
		baseAttrs := []attribute.KeyValue{
			attribute.String("trace_id", span.SpanContext().TraceID().String()),
			attribute.String("span_id", span.SpanContext().SpanID().String()),
		}
		allAttrs := append(baseAttrs, span.Attributes()...)
		attributes := attribute.NewSet(allAttrs...)

		if eventID, ok := attributes.Value(keyEventID); ok {
			e.traces[eventID.AsString()] = attributes
		}
	}
	return nil
}

func (e *apiServerSpanExporter) Shutdown(_ context.Context) error {
	return nil
}

type inMemoryExporter struct {
	sessionTraces map[string]map[string]struct{} // key: session_id, value: map[event_id]struct{}
	spans         []sdktrace.ReadOnlySpan
}

func newInMemoryExporter() *inMemoryExporter {
	return &inMemoryExporter{sessionTraces: make(map[string]map[string]struct{})}
}
func (e *inMemoryExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		if !strings.HasPrefix(span.Name(), itelemetry.OperationChat) {
			continue
		}
		for _, attr := range span.Attributes() {
			if attr.Key != keySessionID {
				continue
			}
			sessionID := attr.Value.AsString()
			traceID := span.SpanContext().TraceID().String()
			if _, ok := e.sessionTraces[sessionID]; !ok {
				e.sessionTraces[sessionID] = map[string]struct{}{
					traceID: {},
				}
			} else {
				e.sessionTraces[sessionID][traceID] = struct{}{}
			}
			break
		}
	}
	e.spans = append(e.spans, spans...)
	return nil
}

func (e *inMemoryExporter) Shutdown(_ context.Context) error {
	return nil
}

func (e *inMemoryExporter) getFinishedSpans(sessionID string) []sdktrace.ReadOnlySpan {
	traceIDs := e.sessionTraces[sessionID]
	var spans []sdktrace.ReadOnlySpan
	for traceID := range traceIDs {
		for _, s := range e.spans {
			if s.SpanContext().TraceID().String() == traceID {
				spans = append(spans, s)
			}
		}
	}
	return spans
}

func (e *inMemoryExporter) clear() {
	e.spans = make([]sdktrace.ReadOnlySpan, 0)
}

// Handler returns the http.Handler for the server.
func (s *Server) Handler() http.Handler { return s.router }

// registerRoutes sets up all REST endpoints expected by ADK Web.
func (s *Server) registerRoutes() {
	s.router.HandleFunc("/list-apps", s.handleListApps).Methods(http.MethodGet)

	// Session APIs.
	s.router.HandleFunc("/apps/{appName}/users/{userId}/sessions",
		s.handleListSessions).Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/users/{userId}/sessions",
		s.handleCreateSession).Methods(http.MethodPost)
	s.router.HandleFunc("/apps/{appName}/users/{userId}/sessions/{sessionId}",
		s.handleGetSession).Methods(http.MethodGet)

	// Evaluation APIs.
	s.router.HandleFunc("/apps/{appName}/eval-sets", s.handleCreateEvalSet).Methods(http.MethodPost)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}", s.handleCreateEvalSetLegacy).Methods(http.MethodPost)
	s.router.HandleFunc("/apps/{appName}/eval-sets", s.handleListEvalSets).Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/eval_sets", s.handleListEvalSetsLegacy).Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/eval-sets/{evalSetId}/add-session", s.handleAddSessionToEvalSet).
		Methods(http.MethodPost)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}/add_session", s.handleAddSessionToEvalSet).
		Methods(http.MethodPost)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}/evals", s.handleListEvalsInSet).
		Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/eval-sets/{evalSetId}/eval-cases/{evalCaseId}", s.handleGetEvalCase).
		Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}/evals/{evalCaseId}", s.handleGetEvalCase).
		Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/eval-sets/{evalSetId}/eval-cases/{evalCaseId}", s.handleUpdateEvalCase).
		Methods(http.MethodPut)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}/evals/{evalCaseId}", s.handleUpdateEvalCase).
		Methods(http.MethodPut)
	s.router.HandleFunc("/apps/{appName}/eval-sets/{evalSetId}/eval-cases/{evalCaseId}", s.handleDeleteEvalCase).
		Methods(http.MethodDelete)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}/evals/{evalCaseId}", s.handleDeleteEvalCase).
		Methods(http.MethodDelete)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}/run_eval", s.handleRunEvalLegacy).Methods(http.MethodPost)
	s.router.HandleFunc("/apps/{appName}/eval-sets/{evalSetId}/run", s.handleRunEval).Methods(http.MethodPost)
	s.router.HandleFunc("/apps/{appName}/eval_results/{evalResultId}", s.handleGetEvalResultLegacy).Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/eval-results/{evalResultId}", s.handleGetEvalResult).Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/eval_results", s.handleListEvalResultsLegacy).Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/eval-results", s.handleListEvalResults).Methods(http.MethodGet)
	s.router.HandleFunc("/apps/{appName}/metrics-info", s.handleListMetricsInfo).Methods(http.MethodGet)

	// Debug APIs
	s.router.HandleFunc("/debug/trace/{event_id}",
		s.handleEventTrace).Methods(http.MethodGet)
	s.router.HandleFunc("/debug/trace/session/{session_id}",
		s.handleSessionTrace).Methods(http.MethodGet)

	// Runner APIs.
	s.router.HandleFunc("/run", s.handleRun).Methods(http.MethodPost)
	s.router.HandleFunc("/run_sse", s.handleRunSSE).Methods(http.MethodPost)

	// OPTIONS handlers to allow CORS pre-flight
	preflight := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	s.router.HandleFunc("/run", preflight).Methods(http.MethodOptions)
	s.router.HandleFunc("/run_sse", preflight).Methods(http.MethodOptions)
	s.router.HandleFunc("/apps/{appName}/eval-sets", preflight).Methods(http.MethodOptions)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}", preflight).Methods(http.MethodOptions)
	s.router.HandleFunc("/apps/{appName}/eval-sets/{evalSetId}/add-session", preflight).Methods(http.MethodOptions)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}/add_session", preflight).Methods(http.MethodOptions)
	s.router.HandleFunc("/apps/{appName}/eval_sets/{evalSetId}/run_eval", preflight).Methods(http.MethodOptions)
}

// ---- Handlers -----------------------------------------------------------

func (s *Server) handleEventTrace(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleEventTrace called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	eventID := vars["event_id"]
	trace, ok := s.traces[eventID]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Trace not found"))
		return
	}
	s.writeJSON(w, buildTraceAttributes(trace))
}

func (s *Server) handleSessionTrace(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleSessionTrace called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	sessionID := vars["session_id"]
	var spans []schema.Span
	for _, span := range s.memoryExporter.getFinishedSpans(sessionID) {
		result := buildTraceAttributes(attribute.NewSet(span.Attributes()...))
		spans = append(spans, schema.Span{
			Name:         span.Name(),
			SpanID:       span.SpanContext().SpanID().String(),
			TraceID:      span.SpanContext().TraceID().String(),
			StartTime:    span.StartTime().UnixNano(),
			EndTime:      span.EndTime().UnixNano(),
			Attributes:   result,
			ParentSpanID: span.Parent().SpanID().String(),
		})
	}
	s.writeJSON(w, spans)
}

func buildTraceAttributes(attributes attribute.Set) map[string]any {
	result := make(map[string]any)
	for iter := attributes.Iter(); iter.Next(); {
		attr := iter.Attribute()
		if attr.Key == keyLLMRequest {
			var req model.Request
			if err := json.Unmarshal([]byte(attr.Value.AsString()), &req); err == nil {
				var contents []schema.Content
				for _, c := range req.Messages {
					contents = append(contents, schema.Content{
						Role: c.Role.String(),
						Parts: []schema.Part{
							{
								Text: c.Content,
							},
						},
					})
				}
				bts, _ := json.Marshal(&schema.TraceLLMRequest{
					Contents: contents,
				})
				result[string(attr.Key)] = string(bts)
			} else {
				log.Debugf("failed to unmarshal LLM request: %s", attr.Value.AsString())
			}
		} else {
			result[string(attr.Key)] = attr.Value.AsString()
		}
	}
	return result
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleListApps called: path=%s", r.URL.Path)
	var apps []string
	for name := range s.agents {
		apps = append(apps, name)
	}
	s.writeJSON(w, apps)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleListSessions called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	userID := vars["userId"]

	userKey := session.UserKey{AppName: appName, UserID: userID}
	sessions, err := s.sessionSvc.ListSessions(r.Context(), userKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Convert internal sessions to ADK format.
	adkSessions := make([]schema.ADKSession, 0, len(sessions))
	for _, sess := range sessions {
		// Filter out eval sessions, same as Python ADK.
		if !strings.HasPrefix(sess.ID, "eval-") {
			adkSessions = append(adkSessions, convertSessionToADKFormat(sess))
		}
	}
	s.writeJSON(w, adkSessions)
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleCreateSession called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	userID := vars["userId"]

	key := session.Key{AppName: appName, UserID: userID}
	sess, err := s.sessionSvc.CreateSession(r.Context(), key, session.StateMap{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeJSON(w, convertSessionToADKFormat(sess))
}

func (s *Server) handleGetSession(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleGetSession called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	userID := vars["userId"]
	sessionID := vars["sessionId"]
	sess, err := s.sessionSvc.GetSession(r.Context(), session.Key{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if sess == nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}
	s.writeJSON(w, convertSessionToADKFormat(sess))
}

// convertContentToMessage converts Google GenAI Content to trpc-agent model.Message
func convertContentToMessage(content schema.Content) model.Message {
	log.Debugf("convertContentToMessage: role=%s parts=%+v", content.Role, content.Parts)
	var textParts []string
	var toolCalls []model.ToolCall
	for _, part := range content.Parts {
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		}

		if part.FunctionCall != nil {
			argsBytes, _ := json.Marshal(part.FunctionCall.Args)
			toolCall := model.ToolCall{
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      part.FunctionCall.Name,
					Arguments: argsBytes,
				},
			}
			toolCalls = append(toolCalls, toolCall)
		}

		if part.InlineData != nil {
			dataType := "file"
			if part.InlineData.MimeType != "" {
				if strings.HasPrefix(part.InlineData.MimeType, "image") {
					dataType = "image"
				} else if strings.HasPrefix(part.InlineData.MimeType, "audio") {
					dataType = "audio"
				} else if strings.HasPrefix(part.InlineData.MimeType, "video") {
					dataType = "video"
				}
			}
			fileName := part.InlineData.DisplayName
			if fileName == "" {
				fileName = "attachment"
			}
			attachmentText := fmt.Sprintf("[%s: %s (%s)]", dataType, fileName, part.InlineData.MimeType)
			textParts = append(textParts, attachmentText)
		}

		if part.FunctionResponse != nil {
			responseJSON, _ := json.Marshal(part.FunctionResponse.Response)
			responseText := fmt.Sprintf("[Function %s responded: %s]", part.FunctionResponse.Name, string(responseJSON))
			textParts = append(textParts, responseText)
		}
	}
	var combinedText string
	if len(textParts) > 0 {
		combinedText = strings.Join(textParts, "\n")
	}
	msg := model.Message{
		Role:    model.Role(content.Role),
		Content: combinedText,
	}

	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	return msg
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleRun called: path=%s", r.URL.Path)

	var req schema.AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// If the request is for streaming, delegate to the SSE handler.
	if req.Streaming {
		// As we can't directly pass the decoded body, the SSE handler will re-decode.
		// A more optimized approach might involve passing the decoded struct via context.
		s.handleRunSSE(w, r)
		return
	}

	rn, err := s.getRunner(req.AppName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := newDetachedContext(r.Context())
	out, err := rn.Run(ctx, req.UserID, req.SessionID,
		convertContentToMessage(req.NewMessage))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// For non-streaming, we might want to collect all events or just return the final one.
	// ADK web might expect a list of events. Let's send all of them.
	var events []map[string]any
	for e := range out {
		if e.Response != nil && e.Response.IsPartial {
			continue // skip streaming chunks in non-streaming endpoint
		}
		if ev := convertEventToADKFormat(e, false); ev != nil {
			events = append(events, ev)
		}
	}
	s.writeJSON(w, events)
}

func (s *Server) handleRunSSE(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleRunSSE called: path=%s", r.URL.Path)

	var req schema.AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	rn, err := s.getRunner(req.AppName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := newDetachedContext(r.Context())
	out, err := rn.Run(ctx, req.UserID, req.SessionID,
		convertContentToMessage(req.NewMessage))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Streaming {
		for e := range out {
			sseEvent := convertEventToADKFormat(e, req.Streaming)
			if sseEvent == nil {
				continue
			}
			data, err := json.Marshal(sseEvent)
			if err != nil {
				log.Errorf("Error marshalling SSE event: %v", err)
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	} else {
		// Non-streaming mode: wait for the first complete event and send only that.
		for e := range out {
			sseEvent := convertEventToADKFormat(e, req.Streaming)
			if sseEvent == nil {
				continue
			}
			data, err := json.Marshal(sseEvent)
			if err != nil {
				log.Errorf("Error marshalling SSE event: %v", err)
				break
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}

	log.Infof("handleRunSSE finished for session %s", req.SessionID)
}

// handleCreateEvalSet creates an eval set.
func (s *Server) handleCreateEvalSet(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleCreateEvalSet called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	var req schema.CreateEvalSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	evalset, err := s.evalSetManager.Create(r.Context(), appName, req.EvalSet.EvalSetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.writeJSON(w, evalset)
}

// handleCreateEvalSetLegacy creates an eval set.
func (s *Server) handleCreateEvalSetLegacy(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleCreateEvalSetLegacy called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalSetID := vars["evalSetId"]
	evalset, err := s.evalSetManager.Create(r.Context(), appName, evalSetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.writeJSON(w, evalset)
}

// handleListEvalSetsLegacy lists all eval sets.
func (s *Server) handleListEvalSetsLegacy(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleListEvalSets called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	ids, err := s.evalSetManager.List(r.Context(), appName)
	if err != nil {
		ids = []string{}
	}
	s.writeJSON(w, ids)
}

// handleListEvalSets lists all eval sets.
func (s *Server) handleListEvalSets(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleListEvalSets called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	ids, err := s.evalSetManager.List(r.Context(), appName)
	if err != nil {
		ids = []string{}
	}
	s.writeJSON(w, &schema.ListEvalSetsResponse{EvalSetIds: ids})
}

// handleAddSessionToEvalSet adds a session to an eval set.
func (s *Server) handleAddSessionToEvalSet(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleAddSessionToEvalSet called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalSetID := vars["evalSetId"]

	var req schema.AddSessionToEvalSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Fetch session.
	sess, err := s.sessionSvc.GetSession(r.Context(), session.Key{AppName: appName, UserID: req.UserId, SessionID: req.SessionId})
	if err != nil || sess == nil {
		http.Error(w, "Session not found.", http.StatusBadRequest)
		return
	}
	// Convert to eval invocations.
	invocations := s.convertSessionToEvalInvocations(sess)
	initialState := map[string]any{}
	newCase := &evalset.EvalCase{
		EvalID:            req.EvalId,
		Conversation:      invocations,
		SessionInput:      &evalset.SessionInput{AppName: appName, UserID: req.UserId, State: initialState},
		CreationTimestamp: &epochtime.EpochTime{Time: time.Now()},
	}
	if err := s.evalSetManager.AddCase(r.Context(), appName, evalSetID, newCase); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleListEvalsInSet lists all eval cases in an eval set.
func (s *Server) handleListEvalsInSet(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleListEvalsInSet called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalSetID := vars["evalSetId"]

	evalSet, err := s.evalSetManager.Get(r.Context(), appName, evalSetID)
	if err != nil || evalSet == nil {
		http.Error(w, fmt.Sprintf("Eval set `%s` not found.", evalSetID), http.StatusBadRequest)
		return
	}
	ids := make([]string, 0, len(evalSet.EvalCases))
	for _, c := range evalSet.EvalCases {
		ids = append(ids, c.EvalID)
	}
	sort.Strings(ids)
	s.writeJSON(w, ids)
}

// handleGetEvalCase gets a single eval case.
func (s *Server) handleGetEvalCase(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleGetEvalCase called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalSetID := vars["evalSetId"]
	evalCaseID := vars["evalCaseId"]

	evalCase, err := s.evalSetManager.GetCase(r.Context(), appName, evalSetID, evalCaseID)
	if err != nil || evalCase == nil {
		http.Error(w, fmt.Sprintf("Eval set `%s` or Eval `%s` not found.", evalSetID, evalCaseID), http.StatusNotFound)
		return
	}
	s.writeJSON(w, evalCase)
}

// handleUpdateEvalCase updates a stored eval case.
func (s *Server) handleUpdateEvalCase(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleUpdateEvalCase called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalSetID := vars["evalSetId"]
	evalCaseID := vars["evalCaseId"]

	var evalCase evalset.EvalCase
	if err := json.NewDecoder(r.Body).Decode(&evalCase); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if evalCase.EvalID != "" && evalCase.EvalID != evalCaseID {
		http.Error(w, "Eval id in payload must match path parameter.", http.StatusBadRequest)
		return
	}
	evalCase.EvalID = evalCaseID
	if err := s.evalSetManager.UpdateCase(r.Context(), appName, evalSetID, &evalCase); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleDeleteEvalCase deletes an eval case.
func (s *Server) handleDeleteEvalCase(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleDeleteEvalCase called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalSetID := vars["evalSetId"]
	evalCaseID := vars["evalCaseId"]
	if err := s.evalSetManager.DeleteCase(r.Context(), appName, evalSetID, evalCaseID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleRunEvalLegacy runs an eval given the details in the eval request.
func (s *Server) handleRunEvalLegacy(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleRunEvalLegacy called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalSetID := vars["evalSetId"]

	var req schema.RunEvalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	caseIDs := req.EvalCaseIds
	metricConfigs := req.EvalMetrics

	runner, err := s.getRunner(appName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	serviceOpts := []service.Option{
		service.WithEvalSetManager(s.evalSetManager),
		service.WithEvalResultManager(s.evalResultManager),
		service.WithRegistry(s.metricRegistry),
	}
	evalService, err := evalservice.New(runner, serviceOpts...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	inferenceRequest := &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID, EvalCaseIDs: caseIDs}
	inferenceResults, err := evalService.Inference(r.Context(), inferenceRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	metrics, err := s.resolveEvalMetrics(r.Context(), appName, evalSetID, metricConfigs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	evaluateRequest := &service.EvaluateRequest{AppName: appName, EvalSetID: evalSetID, InferenceResults: inferenceResults, EvaluateConfig: &service.EvaluateConfig{EvalMetrics: metrics}}
	evalSetResult, err := evalService.Evaluate(r.Context(), evaluateRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	runEvalResults := make([]*schema.RunEvalResult, 0, len(evalSetResult.EvalCaseResults))
	for _, result := range evalSetResult.EvalCaseResults {
		runEvalResults = append(runEvalResults, &schema.RunEvalResult{
			EvalSetFile:                   evalSetID,
			EvalSetId:                     evalSetID,
			EvalId:                        result.EvalID,
			FinalEvalStatus:               int(result.FinalEvalStatus),
			OverallEvalMetricResults:      result.OverallEvalMetricResults,
			EvalMetricResultPerInvocation: result.EvalMetricResultPerInvocation,
			UserId:                        result.UserID,
			SessionId:                     result.SessionID,
		})
	}
	s.writeJSON(w, runEvalResults)
}

// handleRunEval runs an eval given the details in the eval request.
func (s *Server) handleRunEval(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleRunEval called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalSetID := vars["evalSetId"]

	var req schema.RunEvalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	caseIDs := req.EvalCaseIds
	metricConfigs := req.EvalMetrics

	runner, err := s.getRunner(appName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	serviceOpts := []service.Option{
		service.WithEvalSetManager(s.evalSetManager),
		service.WithEvalResultManager(s.evalResultManager),
		service.WithRegistry(s.metricRegistry),
	}
	evalService, err := evalservice.New(runner, serviceOpts...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	inferenceRequest := &service.InferenceRequest{AppName: appName, EvalSetID: evalSetID, EvalCaseIDs: caseIDs}
	inferenceResults, err := evalService.Inference(r.Context(), inferenceRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	metrics, err := s.resolveEvalMetrics(r.Context(), appName, evalSetID, metricConfigs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	evaluateRequest := &service.EvaluateRequest{AppName: appName, EvalSetID: evalSetID, InferenceResults: inferenceResults, EvaluateConfig: &service.EvaluateConfig{EvalMetrics: metrics}}
	evalSetResult, err := evalService.Evaluate(r.Context(), evaluateRequest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	runEvalResults := make([]*schema.RunEvalResult, 0, len(evalSetResult.EvalCaseResults))
	for _, result := range evalSetResult.EvalCaseResults {
		runEvalResults = append(runEvalResults, &schema.RunEvalResult{
			EvalSetFile:                   evalSetID,
			EvalSetId:                     evalSetID,
			EvalId:                        result.EvalID,
			FinalEvalStatus:               int(result.FinalEvalStatus),
			OverallEvalMetricResults:      result.OverallEvalMetricResults,
			EvalMetricResultPerInvocation: result.EvalMetricResultPerInvocation,
			UserId:                        result.UserID,
			SessionId:                     result.SessionID,
		})
	}
	s.writeJSON(w, &schema.RunEvalResponse{RunEvalResults: runEvalResults})
}

// handleGetEvalResultLegacy gets a full eval set result.
func (s *Server) handleGetEvalResultLegacy(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleGetEvalResultLegacy called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalResultID := vars["evalResultId"]

	evalResult, err := s.evalResultManager.Get(r.Context(), appName, evalResultID)
	if err != nil || evalResult == nil {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	s.writeJSON(w, evalResult)
}

// handleGetEvalResult gets a full eval set result.
func (s *Server) handleGetEvalResult(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleGetEvalResult called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	evalResultID := vars["evalResultId"]

	evalResult, err := s.evalResultManager.Get(r.Context(), appName, evalResultID)
	if err != nil || evalResult == nil {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	s.writeJSON(w, evalResult)
}

// handleListEvalResultsLegacy lists all eval result IDs for an app.
func (s *Server) handleListEvalResultsLegacy(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleListEvalResults called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	ids, err := s.evalResultManager.List(r.Context(), appName)
	if err != nil {
		ids = []string{}
	}
	s.writeJSON(w, ids)
}

// handleListEvalResults lists all eval results for an app.
func (s *Server) handleListEvalResults(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleListEvalResults called: path=%s", r.URL.Path)
	vars := mux.Vars(r)
	appName := vars["appName"]
	ids, err := s.evalResultManager.List(r.Context(), appName)
	if err != nil {
		ids = []string{}
	}
	s.writeJSON(w, &schema.ListEvalResultsResponse{EvalResultIds: ids})
}

// handleListMetricsInfo lists metadata for the registered evaluation metrics.
func (s *Server) handleListMetricsInfo(w http.ResponseWriter, r *http.Request) {
	log.Infof("handleListMetricsInfo called: path=%s", r.URL.Path)
	response := &schema.ListMetricsInfoResponse{MetricsInfo: s.buildMetricInfos()}
	s.writeJSON(w, response)
}

// buildMetricInfos collects metadata for the current evaluator registry.
func (s *Server) buildMetricInfos() []*schema.MetricInfo {
	if s.metricRegistry == nil {
		return []*schema.MetricInfo{}
	}
	names := s.metricRegistry.List()
	infos := make([]*schema.MetricInfo, 0, len(names))
	for _, name := range names {
		evaluator, err := s.metricRegistry.Get(name)
		if err != nil {
			log.Errorf("get evaluator %s: %v", name, err)
			continue
		}
		info := &schema.MetricInfo{
			MetricName:  evaluator.Name(),
			Description: evaluator.Description(),
			MetricValueInfo: &schema.MetricValueInfo{
				Interval: &schema.MetricInterval{
					MinValue:  0,
					OpenAtMin: false,
					MaxValue:  1,
					OpenAtMax: false,
				},
			},
		}
		infos = append(infos, info)
	}
	return infos
}

// resolveEvalMetrics returns the metric configuration for a run, optionally
// persisting values via the configured metric manager.
func (s *Server) resolveEvalMetrics(ctx context.Context, appName, evalSetID string,
	configs []metric.EvalMetric) ([]*metric.EvalMetric, error) {
	if len(configs) > 0 {
		metrics := make([]*metric.EvalMetric, 0, len(configs))
		for _, cfg := range configs {
			metricCopy := cfg
			metrics = append(metrics, &metricCopy)
			if s.metricManager == nil {
				continue
			}
			if err := s.metricManager.Update(ctx, appName, evalSetID, &metricCopy); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					if err := s.metricManager.Add(ctx, appName, evalSetID, &metricCopy); err != nil {
						return nil, fmt.Errorf("store metric %s.%s.%s: %w", appName, evalSetID,
							metricCopy.MetricName, err)
					}
					continue
				}
				return nil, fmt.Errorf("store metric %s.%s.%s: %w", appName, evalSetID,
					metricCopy.MetricName, err)
			}
		}
		return metrics, nil
	}
	if s.metricManager == nil {
		return nil, errors.New("eval metrics not provided")
	}
	names, err := s.metricManager.List(ctx, appName, evalSetID)
	if err != nil {
		return nil, fmt.Errorf("list metrics for %s.%s: %w", appName, evalSetID, err)
	}
	if len(names) == 0 {
		return nil, errors.New("no eval metrics configured for this eval set")
	}
	metrics := make([]*metric.EvalMetric, 0, len(names))
	for _, name := range names {
		m, err := s.metricManager.Get(ctx, appName, evalSetID, name)
		if err != nil {
			return nil, fmt.Errorf("get metric %s for %s.%s: %w", name, appName, evalSetID, err)
		}
		metrics = append(metrics, m)
	}
	return metrics, nil
}

// convertSessionToEvalInvocations builds eval invocations from a session's events.
func (s *Server) convertSessionToEvalInvocations(sess *session.Session) []*evalset.Invocation {
	var invocations []*evalset.Invocation
	if sess == nil {
		return invocations
	}
	events := sess.GetEvents()
	if len(events) == 0 {
		return invocations
	}
	var cur *evalset.Invocation
	for _, e := range events {
		if e.Response == nil || len(e.Response.Choices) == 0 {
			continue
		}
		// Start a new invocation on user message.
		msg := e.Response.Choices[0].Message
		if msg.Role == model.RoleUser {
			// Flush previous.
			if cur != nil {
				invocations = append(invocations, cur)
			}
			cur = &evalset.Invocation{
				InvocationID: e.InvocationID,
				UserContent: &genai.Content{
					Role:  string(model.RoleUser),
					Parts: []*genai.Part{{Text: msg.Content}},
				},
				CreationTimestamp: &epochtime.EpochTime{Time: e.Timestamp},
				IntermediateData:  &evalset.IntermediateData{},
			}
			continue
		}
		// If this is a final response, set finalResponse.
		if e.IsFinalResponse() && cur != nil {
			if msg.Content != "" {
				cur.FinalResponse = &genai.Content{Role: string(msg.Role), Parts: []*genai.Part{{Text: msg.Content}}}
			}
			continue
		}
		// Capture tool calls as tool uses.
		if e.IsToolCallResponse() && cur != nil {
			for _, tc := range msg.ToolCalls {
				if use := convertToolCallToFunctionCall(&tc); use != nil {
					cur.IntermediateData.ToolUses = append(cur.IntermediateData.ToolUses, use)
				}
			}
		}
	}
	if cur != nil {
		invocations = append(invocations, cur)
	}
	return invocations
}

// convertToolCallToFunctionCall converts model.ToolCall to genai.FunctionCall.
func convertToolCallToFunctionCall(tc *model.ToolCall) *genai.FunctionCall {
	if tc == nil || tc.Function.Name == "" {
		return nil
	}
	var args map[string]any
	if len(tc.Function.Arguments) > 0 {
		if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
			args = map[string]any{"raw": string(tc.Function.Arguments)}
		}
	}
	return &genai.FunctionCall{ID: tc.ID, Name: tc.Function.Name, Args: args}
}

// convertSessionToADKFormat converts an internal session object to the
// flattened structure the ADK Web UI expects.
func convertSessionToADKFormat(s *session.Session) schema.ADKSession {
	events := s.GetEvents()
	adkEvents := make([]map[string]any, 0, len(events))
	for _, e := range events {
		// Create a local copy to avoid implicit memory aliasing.
		e := e
		if ev := convertEventToADKFormat(&e, false); ev != nil {
			adkEvents = append(adkEvents, ev)
		}
	}
	return schema.ADKSession{
		AppName:        s.AppName,
		UserID:         s.UserID,
		ID:             s.ID,
		CreateTime:     s.CreatedAt.Unix(),
		LastUpdateTime: s.UpdatedAt.Unix(),
		State:          map[string][]byte(s.State),
		Events:         adkEvents,
	}
}

// buildADKEventEnvelope creates the basic ADK event envelope.
func buildADKEventEnvelope(e *event.Event) map[string]any {
	return map[string]any{
		"invocationId": e.InvocationID,
		"author":       e.Author,
		"actions": map[string]any{
			"stateDelta":           map[string]any{},
			"artifactDelta":        map[string]any{},
			"requestedAuthConfigs": map[string]any{},
		},
		"id":        e.ID,
		"timestamp": e.Timestamp.Unix(),
	}
}

// determineEventRole determines the role for the event content.
func determineEventRole(e *event.Event) string {
	role := e.Author // fallback
	if e.Response != nil {
		if e.Response.Object == model.ObjectTypeToolResponse {
			role = string(model.RoleTool)
		} else if len(e.Response.Choices) > 0 {
			role = string(e.Response.Choices[0].Message.Role)
		}
	}
	return role
}

// buildEventParts constructs the parts array for the event content.
func buildEventParts(e *event.Event) []map[string]any {
	var parts []map[string]any

	// Early separation: Handle Graph events completely separately
	if isGraphEvent(e) {
		return buildGraphEventParts(e) // Graph events use their own logic
	}

	// Handle LLM Agent events only (chat.completion, tool.response, etc.)
	if e.Response == nil {
		return parts
	}

	// Handle normal / streaming assistant or model messages.
	for _, choice := range e.Response.Choices {
		// Regular text (full message).
		if choice.Message.Content != "" {
			// For tool response events, we do NOT include the raw JSON string as a
			// separate text part, otherwise the ADK Web UI will render duplicated
			// information (both as plain text and as function_response). Keeping
			// only the structured function_response part provides a cleaner view.
			if e.Response.Object != model.ObjectTypeToolResponse {
				parts = append(parts, map[string]any{keyText: choice.Message.Content})
			}
		}

		// Tool calls in full message.
		for _, tc := range choice.Message.ToolCalls {
			parts = append(parts, buildFunctionCallPart(tc))
		}

		// Streaming delta text.
		if choice.Delta.Content != "" {
			parts = append(parts, map[string]any{keyText: choice.Delta.Content})
		}
		// Tool calls in streaming delta.
		for _, tc := range choice.Delta.ToolCalls {
			parts = append(parts, buildFunctionCallPart(tc))
		}
	}

	// Tool response events.
	if e.Response.Object == model.ObjectTypeToolResponse {
		for _, choice := range e.Response.Choices {
			var respObj any
			if choice.Message.Content != "" {
				if err := json.Unmarshal([]byte(choice.Message.Content), &respObj); err != nil {
					respObj = choice.Message.Content // raw string fallback
				}
			}
			parts = append(parts, buildFunctionResponsePart(respObj, choice.Message.ToolID, choice.Message.ToolName))
		}
	}

	return parts
}

// filterEventParts filters parts based on streaming mode and event type.
func filterEventParts(
	e *event.Event,
	parts []map[string]any,
	isStreaming bool,
) []map[string]any {
	// Early separation: Handle Graph events completely separately
	if isGraphEvent(e) {
		return filterGraphEventParts(e, parts, isStreaming)
	}

	// Handle LLM Agent events only (chat.completion, tool.response, etc.)
	if e.Response == nil {
		return parts // Non-LLM events without Response, return as-is
	}

	if isStreaming {
		// Drop aggregated final messages to avoid duplication with
		// the already streamed deltas. Graph final text is handled in
		// filterGraphEventParts.
		if !e.Response.IsPartial && e.Response.Done {
			return nil
		}
	} else {
		// Non-streaming endpoint should include:
		//   1. Final assistant messages (IsFinalResponse)
		//   2. Tool result events (object == tool.response)
		//   3. Function call events (IsToolCallResponse)
		//   4. User messages (IsUserMessage) for session replay.
		toolResp := isToolResponse(e)
		hasToolCall := e.Response.IsToolCallResponse()
		isUser := e.Response.IsUserMessage()
		isFinal := e.Response.IsFinalResponse()
		if !isFinal && !toolResp && !hasToolCall && !isUser {
			return nil
		}
	}

	return parts
}

// addResponseMetadata adds response-level metadata to the ADK event.
func addResponseMetadata(adkEvent map[string]any, e *event.Event) {
	if e.Response == nil {
		return
	}

	adkEvent["done"] = e.Response.Done
	adkEvent["partial"] = e.Response.IsPartial
	if e.Response.Object != "" {
		adkEvent["object"] = e.Response.Object
	}
	if e.Response.Created != 0 {
		adkEvent["created"] = e.Response.Created
	}
	if e.Response.Model != "" {
		adkEvent["model"] = e.Response.Model
	}
}

// addUsageMetadata adds usage metadata to the ADK event.
func addUsageMetadata(adkEvent map[string]any, e *event.Event) {
	if e.Usage == nil {
		return
	}

	adkEvent["usageMetadata"] = map[string]any{
		"promptTokenCount":     e.Usage.PromptTokens,
		"candidatesTokenCount": e.Usage.CompletionTokens,
		"totalTokenCount":      e.Usage.TotalTokens,
	}
}

// convertEventToADKFormat converts trpc-agent Event to ADK Web UI expected
// format. The isStreaming flag indicates whether the UI is currently
// displaying token-level streaming (true) or expecting a single complete
// response (false). In streaming mode we suppress the final aggregated
// "done" event content to avoid duplication.
func convertEventToADKFormat(e *event.Event, isStreaming bool) map[string]any {
	// Build basic envelope.
	adkEvent := buildADKEventEnvelope(e)

	// Determine role and build content.
	role := determineEventRole(e)
	content := map[string]any{
		"role": role,
	}

	// Build parts.
	parts := buildEventParts(e)

	// Filter parts based on streaming mode.
	parts = filterEventParts(e, parts, isStreaming)

	// Skip event if no meaningful parts.
	if len(parts) == 0 {
		return nil
	}

	content["parts"] = parts
	adkEvent["content"] = content

	// Add metadata.
	addResponseMetadata(adkEvent, e)
	addUsageMetadata(adkEvent, e)

	return adkEvent
}

// ---- helpers ------------------------------------------------------------

func (s *Server) getRunner(appName string) (runner.Runner, error) {
	s.mu.RLock()
	if r, ok := s.runners[appName]; ok {
		s.mu.RUnlock()
		return r, nil
	}
	s.mu.RUnlock()

	ag, ok := s.agents[appName]
	if !ok {
		return nil, fmt.Errorf("agent not found")
	}

	// Compose runner options: user-supplied first, then mandatory sessionSvc.
	allOpts := append([]runner.Option{}, s.runnerOpts...)
	allOpts = append(allOpts, runner.WithSessionService(s.sessionSvc))

	r := runner.NewRunner(appName, ag, allOpts...)
	s.mu.Lock()
	s.runners[appName] = r
	s.mu.Unlock()
	return r, nil
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------------
// Internal helpers for event conversion --------------------------------
// ---------------------------------------------------------------------

// ADK Web payload JSON keys. Keeping them as constants helps avoid
// typographical errors and makes refactoring easier.
const (
	keyText             = "text"             // Plain textual content part.
	keyFunctionCall     = "functionCall"     // Function call part key.
	keyFunctionResponse = "functionResponse" // Function response part key.
)

// isToolResponse reports whether the supplied event represents a tool
// response produced by the LLM flow.
func isToolResponse(e *event.Event) bool {
	return e.Response != nil && e.Response.Object == model.ObjectTypeToolResponse
}

// buildFunctionCallPart converts a model.ToolCall into the ADK Web part map.
// The returned map follows the schema expected by the Web UI.
func buildFunctionCallPart(tc model.ToolCall) map[string]any {
	var args any
	if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
		// Preserve raw string if not valid JSON.
		args = map[string]any{"raw": string(tc.Function.Arguments)}
	}
	return map[string]any{
		keyFunctionCall: map[string]any{
			"name": tc.Function.Name,
			"args": args,
			"id":   tc.ID,
		},
	}
}

// buildFunctionResponsePart builds a single functionResponse part.
// respObj can be either a structured object (decoded JSON) or the original
// raw string when JSON decoding fails. The name field is currently unknown
// from the upstream payload, so we intentionally leave it blank.
func buildFunctionResponsePart(respObj any, id string, name string) map[string]any {
	return map[string]any{
		keyFunctionResponse: map[string]any{
			"name":     name,
			"response": respObj,
			"id":       id,
		},
	}
}

// ---------------------------------------------------------------------
// Graph Agent Event Processing ----------------------------------------
// ---------------------------------------------------------------------

// buildGraphEventParts handles Graph events that store information in StateDelta.
// Returns parts array for Graph tool execution events, or empty array if not a Graph tool event.
func buildGraphEventParts(e *event.Event) []map[string]any {
	var parts []map[string]any

	// Handle graph.execution events (final results)
	if e.Object == graph.ObjectTypeGraphExecution {
		if e.Response != nil && len(e.Response.Choices) > 0 && e.Response.Choices[0].Message.Content != "" {
			parts = append(parts, map[string]any{
				"text": e.Response.Choices[0].Message.Content,
			})
		}
		return parts
	}

	// Only process Graph node execution events for tool calls
	if e.Object == graph.ObjectTypeGraphNodeExecution {
		// Continue to tool execution processing below
	} else if strings.HasPrefix(e.Object, "graph.") {
		// Other graph events, return empty parts unless they have special handling
		return parts
	} else {
		// Not a graph event
		return parts
	}

	// Check for tool execution metadata in StateDelta
	if e.StateDelta == nil {
		return parts
	}

	toolMetadataBytes, exists := e.StateDelta[graph.MetadataKeyTool]
	if !exists {
		return parts
	}

	// Parse tool execution metadata
	var toolMetadata struct {
		ToolName string `json:"toolName"`
		ToolID   string `json:"toolId"`
		Phase    string `json:"phase"`
		Input    string `json:"input,omitempty"`
		Output   string `json:"output,omitempty"`
	}

	if err := json.Unmarshal(toolMetadataBytes, &toolMetadata); err != nil {
		return parts
	}

	// Convert Graph tool events to ADK format
	// Strategy: Only show tool responses (complete/error), skip tool calls (start)
	// This avoids duplication with LLM Agent tool calls while still showing results
	switch toolMetadata.Phase {
	case "start":
		// Skip tool execution start to avoid duplication with LLM chat.completion tool calls

	case "complete":
		// Tool execution complete -> functionResponse
		parts = append(parts, buildGraphFunctionResponsePart(toolMetadata))

	case "error":
		// Tool execution error -> functionResponse with error
		parts = append(parts, buildGraphFunctionErrorPart(toolMetadata))
	}
	return parts
}

// buildGraphFunctionResponsePart builds a functionResponse part from Graph tool metadata.
func buildGraphFunctionResponsePart(toolMetadata struct {
	ToolName string `json:"toolName"`
	ToolID   string `json:"toolId"`
	Phase    string `json:"phase"`
	Input    string `json:"input,omitempty"`
	Output   string `json:"output,omitempty"`
}) map[string]any {
	var respObj any
	if toolMetadata.Output != "" {
		if err := json.Unmarshal([]byte(toolMetadata.Output), &respObj); err != nil {
			// Preserve raw string if not valid JSON
			respObj = toolMetadata.Output
		}
	} else {
		respObj = "No output"
	}

	return map[string]any{
		keyFunctionResponse: map[string]any{
			"name":     toolMetadata.ToolName,
			"response": respObj,
			"id":       toolMetadata.ToolID,
		},
	}
}

// buildGraphFunctionErrorPart builds a functionResponse part for Graph tool errors.
func buildGraphFunctionErrorPart(toolMetadata struct {
	ToolName string `json:"toolName"`
	ToolID   string `json:"toolId"`
	Phase    string `json:"phase"`
	Input    string `json:"input,omitempty"`
	Output   string `json:"output,omitempty"`
}) map[string]any {
	errorMsg := "Tool execution failed"
	if toolMetadata.Output != "" {
		errorMsg = toolMetadata.Output
	}

	return map[string]any{
		keyFunctionResponse: map[string]any{
			"name":     toolMetadata.ToolName,
			"response": map[string]any{"error": errorMsg},
			"id":       toolMetadata.ToolID,
		},
	}
}

// isGraphEvent checks if the event is a Graph-related event.
func isGraphEvent(e *event.Event) bool {
	return strings.HasPrefix(e.Object, "graph.")
}

// filterGraphEventParts handles filtering for Graph events only.
func filterGraphEventParts(
	e *event.Event,
	parts []map[string]any,
	isStreaming bool,
) []map[string]any {
	// For Graph tool execution events, always include them (they have functionCall/functionResponse)
	if isGraphToolEvent(e) && len(parts) > 0 {
		return parts
	}

	// For Graph execution completion events (final result), always include
	// so that streaming UIs receive the terminal text as well.
	if e.Object == graph.ObjectTypeGraphExecution && len(parts) > 0 {
		return parts
	}

	// Skip all other Graph events to avoid duplicates
	return nil
}

// isGraphToolEvent checks if the event is a Graph tool execution event.
func isGraphToolEvent(e *event.Event) bool {
	if e.Object != graph.ObjectTypeGraphNodeExecution {
		return false
	}
	if e.StateDelta == nil {
		return false
	}
	_, exists := e.StateDelta[graph.MetadataKeyTool]
	return exists
}

// detachedContext wraps a parent context but disables cancellation and
// deadlines while preserving all values. This allows us to keep trace and
// logging metadata from the incoming request context without being affected
// by HTTP‑level timeouts or client disconnects.
type detachedContext struct {
	context.Context
}

func (detachedContext) Deadline() (time.Time, bool) {
	return time.Time{}, false
}

func (detachedContext) Done() <-chan struct{} {
	return nil
}

func (detachedContext) Err() error {
	return nil
}

func newDetachedContext(ctx context.Context) context.Context {
	return detachedContext{Context: ctx}
}
