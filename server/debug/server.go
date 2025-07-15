//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package debug provides a HTTP server for debugging and testing.
package debug

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/debug/internal/schema"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
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

// New creates a new CLI HTTP server with explicit agent registration. The
// behaviour can be tweaked via functional options.
func New(agents map[string]agent.Agent, opts ...Option) *Server {
	s := &Server{
		agents:     agents,
		router:     mux.NewRouter(),
		runners:    make(map[string]runner.Runner),
		sessionSvc: sessioninmemory.NewSessionService(),
	}

	// Apply user-provided options.
	for _, opt := range opts {
		opt(s)
	}

	// Add CORS middleware for ADK Web compatibility.
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"Content-Length", "Content-Type"},
	})
	s.router.Use(c.Handler)
	s.registerRoutes()
	return s
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

	// Runner APIs.
	s.router.HandleFunc("/run", s.handleRun).Methods(http.MethodPost)
	s.router.HandleFunc("/run_sse", s.handleRunSSE).Methods(http.MethodPost)

	// OPTIONS handlers to allow CORS pre-flight
	preflight := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	s.router.HandleFunc("/run", preflight).Methods(http.MethodOptions)
	s.router.HandleFunc("/run_sse", preflight).Methods(http.MethodOptions)
}

// ---- Handlers -----------------------------------------------------------

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

	out, err := rn.Run(r.Context(), req.UserID, req.SessionID,
		convertContentToMessage(req.NewMessage))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// For non-streaming, we might want to collect all events or just return the final one.
	// ADK web might expect a list of events. Let's send all of them.
	var events []map[string]interface{}
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
	out, err := rn.Run(context.Background(), req.UserID, req.SessionID,
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

// convertSessionToADKFormat converts an internal session object to the
// flattened structure the ADK Web UI expects.
func convertSessionToADKFormat(s *session.Session) schema.ADKSession {
	adkEvents := make([]map[string]interface{}, 0, len(s.Events))
	for _, e := range s.Events {
		if ev := convertEventToADKFormat(&e, false); ev != nil {
			adkEvents = append(adkEvents, ev)
		}
	}
	return schema.ADKSession{
		AppName:    s.AppName,
		UserID:     s.UserID,
		ID:         s.ID,
		CreateTime: s.CreatedAt.UnixMilli(),
		UpdateTime: s.UpdatedAt.UnixMilli(),
		State:      map[string][]byte(s.State),
		Events:     adkEvents,
	}
}

// convertEventToADKFormat converts trpc-agent Event to ADK Web UI expected
// format. The isStreaming flag indicates whether the UI is currently
// displaying token-level streaming (true) or expecting a single complete
// response (false). In streaming mode we suppress the final aggregated
// "done" event content to avoid duplication.
func convertEventToADKFormat(e *event.Event, isStreaming bool) map[string]interface{} {
	// ---------------------------------------------------------------------
	// Basic envelope ------------------------------------------------------
	// ---------------------------------------------------------------------
	id := eventID(e)

	adkEvent := map[string]interface{}{
		"invocationId": e.InvocationID,
		"author":       e.Author,
		"actions": map[string]interface{}{
			"stateDelta":           map[string]interface{}{},
			"artifactDelta":        map[string]interface{}{},
			"requestedAuthConfigs": map[string]interface{}{},
		},
		"id":        id,
		"timestamp": e.Timestamp.UnixMilli(),
	}

	// ---------------------------------------------------------------------
	// Determine role for content ------------------------------------------
	// ---------------------------------------------------------------------
	role := e.Author // fallback
	if e.Response != nil {
		if e.Response.Object == model.ObjectTypeToolResponse {
			role = string(model.RoleTool)
		} else if len(e.Response.Choices) > 0 {
			role = string(e.Response.Choices[0].Message.Role)
		}
	}

	content := map[string]interface{}{
		"role": role,
	}

	var parts []map[string]interface{}

	// ---------------------------------------------------------------------
	// Construct parts ------------------------------------------------------
	// ---------------------------------------------------------------------
	if e.Response != nil {
		// Handle normal / streaming assistant or model messages.
		for _, choice := range e.Response.Choices {
			// Regular text (full message).
			if choice.Message.Content != "" {
				// For tool response events, we do NOT include the raw JSON string as a
				// separate text part, otherwise the ADK Web UI will render duplicated
				// information (both as plain text and as function_response). Keeping
				// only the structured function_response part provides a cleaner view.
				if e.Response.Object != model.ObjectTypeToolResponse {
					parts = append(parts, map[string]interface{}{keyText: choice.Message.Content})
				}
			}

			// Tool calls in full message.
			for _, tc := range choice.Message.ToolCalls {
				parts = append(parts, buildFunctionCallPart(tc))
			}

			// Streaming delta text.
			if choice.Delta.Content != "" {
				parts = append(parts, map[string]interface{}{keyText: choice.Delta.Content})
			}
			// Tool calls in streaming delta.
			for _, tc := range choice.Delta.ToolCalls {
				parts = append(parts, buildFunctionCallPart(tc))
			}
		}

		// -----------------------------------------------------------------
		// Tool response events -------------------------------------------
		// -----------------------------------------------------------------
		if e.Response.Object == model.ObjectTypeToolResponse {
			for _, choice := range e.Response.Choices {
				var respObj interface{}
				if choice.Message.Content != "" {
					if err := json.Unmarshal([]byte(choice.Message.Content), &respObj); err != nil {
						respObj = choice.Message.Content // raw string fallback
					}
				}
				parts = append(parts, buildFunctionResponsePart(respObj, choice.Message.ToolID, choice.Message.ToolName))
			}
		}
	}

	// ---------------------------------------------------------------------
	// Streaming vs non-streaming filtering --------------------------------
	// ---------------------------------------------------------------------
	if e.Response != nil {
		if isStreaming {
			// Drop duplicate aggregated message at end of streaming sequence.
			if !e.Response.IsPartial && e.Response.Done {
				parts = nil
			}
		} else {
			// Non-streaming endpoint should include:
			//   1. Final assistant messages (Done == true)
			//   2. Tool response events (object == tool.response)
			//   3. Function call events which contain ToolCalls.
			toolResp := isToolResponse(e)
			hasToolCall := false
			if len(e.Response.Choices) > 0 && len(e.Response.Choices[0].Message.ToolCalls) > 0 {
				hasToolCall = true
			}
			if !e.Response.Done && !toolResp && !hasToolCall {
				return nil
			}
		}
	}

	// Skip event if no meaningful parts.
	if len(parts) == 0 {
		return nil
	}

	content["parts"] = parts
	adkEvent["content"] = content

	// ---------------------------------------------------------------------
	// Response-level metadata --------------------------------------------
	// ---------------------------------------------------------------------
	if e.Response != nil {
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

	// Add usage metadata if available.
	if e.Usage != nil {
		adkEvent["usageMetadata"] = map[string]interface{}{
			"promptTokenCount":     e.Usage.PromptTokens,
			"candidatesTokenCount": e.Usage.CompletionTokens,
			"totalTokenCount":      e.Usage.TotalTokens,
		}
	}
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

// eventID returns the canonical identifier for an event.
// If the underlying model.Response already contains a non-empty ID we
// prefer it; otherwise we fall back to the envelope‐level event ID.
func eventID(e *event.Event) string {
	if e.Response != nil && e.Response.ID != "" {
		return e.Response.ID
	}
	return e.ID
}

// isToolResponse reports whether the supplied event represents a tool
// response produced by the LLM flow.
func isToolResponse(e *event.Event) bool {
	return e.Response != nil && e.Response.Object == model.ObjectTypeToolResponse
}

// buildFunctionCallPart converts a model.ToolCall into the ADK Web part map.
// The returned map follows the schema expected by the Web UI.
func buildFunctionCallPart(tc model.ToolCall) map[string]interface{} {
	var args interface{}
	if err := json.Unmarshal(tc.Function.Arguments, &args); err != nil {
		// Preserve raw string if not valid JSON.
		args = map[string]interface{}{"raw": string(tc.Function.Arguments)}
	}
	return map[string]interface{}{
		keyFunctionCall: map[string]interface{}{
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
func buildFunctionResponsePart(respObj interface{}, id string, name string) map[string]interface{} {
	return map[string]interface{}{
		keyFunctionResponse: map[string]interface{}{
			"name":     name,
			"response": respObj,
			"id":       id,
		},
	}
}
