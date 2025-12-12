//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package openai provides an OpenAI-compatible API server.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	defaultBasePath  = "/v1"
	defaultPath      = "/chat/completions"
	defaultModelName = "gpt-3.5-turbo"
	defaultAppName   = "openai-server"

	headerAllow                = "Allow"
	headerSessionID            = "X-Session-ID"
	headerContentType          = "Content-Type"
	headerCacheControl         = "Cache-Control"
	headerConnection           = "Connection"
	headerAccessControlOrigin  = "Access-Control-Allow-Origin"
	headerAccessControlMethods = "Access-Control-Allow-Methods"
	headerAccessControlHeaders = "Access-Control-Allow-Headers"

	contentTypeJSON        = "application/json"
	contentTypeEventStream = "text/event-stream"
	cacheControlNoCache    = "no-cache"
	connectionKeepAlive    = "keep-alive"

	defaultUserID = "default"

	sseDataPrefix = "data: "
	sseLineEnding = "\n\n"
	sseDoneMarker = "[DONE]"
)

// Server provides OpenAI-compatible API server.
type Server struct {
	basePath       string
	path           string // path is the chat completions endpoint path.
	handler        http.Handler
	sessionService session.Service
	runner         runner.Runner
	agent          agent.Agent
	modelName      string
	converter      *converter
	ownedRunner    bool // Indicates if runner was created by this server.
	closeOnce      sync.Once
}

// New creates a new OpenAI server.
func New(opts ...Option) (*Server, error) {
	options := &options{
		basePath:  defaultBasePath,
		path:      defaultPath,
		modelName: defaultModelName,
		appName:   defaultAppName,
	}
	for _, opt := range opts {
		opt(options)
	}
	if options.agent == nil && options.runner == nil {
		return nil, errors.New("either agent or runner must be provided")
	}
	if options.sessionService == nil {
		options.sessionService = inmemory.NewSessionService()
	}
	chatPath, err := joinURLPath(options.basePath, options.path)
	if err != nil {
		return nil, fmt.Errorf("openai: url join chat path: %w", err)
	}
	var r runner.Runner
	var ownedRunner bool
	if options.runner != nil {
		r = options.runner
		ownedRunner = false
	} else {
		r = runner.NewRunner(options.appName, options.agent,
			runner.WithSessionService(options.sessionService))
		ownedRunner = true
	}
	conv := newConverter(options.modelName)
	s := &Server{
		basePath:       options.basePath,
		path:           chatPath,
		sessionService: options.sessionService,
		runner:         r,
		agent:          options.agent,
		modelName:      options.modelName,
		converter:      conv,
		ownedRunner:    ownedRunner,
	}
	s.setupHandler()
	return s, nil
}

// Handler returns the HTTP handler for the server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// BasePath returns the base path of the server.
func (s *Server) BasePath() string {
	return s.basePath
}

// Path returns the chat completions endpoint path joined with BasePath.
func (s *Server) Path() string {
	return s.path
}

// Close closes the server and releases owned resources.
// It's safe to call Close multiple times.
// Only resources created by this server (not provided by user) will be closed.
func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		// Only close runner if we created it.
		if s.ownedRunner && s.runner != nil {
			if err := s.runner.Close(); err != nil {
				closeErr = err
				log.Errorf("openai: failed to close runner: %v", err)
			}
		}
		// Note: sessionService is managed by runner if runner owns it,
		// or by user if user provided it. We don't close it here.
	})
	return closeErr
}

// setupHandler sets up the HTTP routes.
func (s *Server) setupHandler() {
	mux := http.NewServeMux()
	mux.HandleFunc(s.path, s.handleChatCompletions)
	mux.HandleFunc(s.path+"/", s.handleChatCompletions)
	s.handler = mux
}

// joinURLPath joins the base path and the path into a URL path.
func joinURLPath(basePath, path string) (string, error) {
	return url.JoinPath(basePath, path)
}

// handleChatCompletions handles the /v1/chat/completions endpoint.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		s.handleCORS(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx := r.Context()
	var req openAIRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.WarnfContext(
			ctx,
			"openai: failed to decode request: %v",
			err,
		)
		s.writeError(
			w,
			fmt.Errorf("invalid request: %w", err),
			errorTypeInvalidRequest,
			http.StatusBadRequest,
		)
		return
	}
	defer r.Body.Close()
	if req.Stream {
		s.handleStreaming(w, r, &req)
	} else {
		s.handleNonStreaming(w, r, &req)
	}
}

// handleCORS handles CORS preflight requests.
func (s *Server) handleCORS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(headerAccessControlOrigin, "*")
	w.Header().Set(headerAccessControlMethods, http.MethodPost)
	w.Header().Set(headerAccessControlHeaders, "Content-Type, Authorization")
	w.WriteHeader(http.StatusNoContent)
}

// handleNonStreaming handles non-streaming requests.
func (s *Server) handleNonStreaming(w http.ResponseWriter, r *http.Request, req *openAIRequest) {
	ctx := r.Context()
	messages, err := s.converter.convertRequest(ctx, req)
	if err != nil {
		log.WarnfContext(
			ctx,
			"openai: failed to convert request: %v",
			err,
		)
		s.writeError(
			w,
			err,
			errorTypeInvalidRequest,
			http.StatusBadRequest,
		)
		return
	}
	if len(messages) == 0 {
		s.writeError(w, errors.New("messages cannot be empty"), errorTypeInvalidRequest, http.StatusBadRequest)
		return
	}
	// Get the last message (user message).
	userMessage := messages[len(messages)-1]
	// Get session ID from header or generate one.
	sessionID := r.Header.Get(headerSessionID)
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	// Get user ID from header or use default.
	userID := req.User
	if userID == "" {
		userID = defaultUserID
	}
	// Build run options with history.
	runOpts := []agent.RunOption{}
	if len(messages) > 1 {
		runOpts = append(runOpts, agent.WithMessages(messages[:len(messages)-1]))
	}
	// Note: Generation config (temperature, max_tokens, etc.) should be set
	// when creating the agent, not at runtime. OpenAI API parameters are
	// ignored here for now. Users should configure the agent with desired
	// generation config when creating it.
	// Run the agent.
	eventCh, err := s.runner.Run(ctx, userID, sessionID, userMessage, runOpts...)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"openai: failed to run agent: %v",
			err,
		)
		s.writeError(
			w,
			err,
			errorTypeInternal,
			http.StatusInternalServerError,
		)
		return
	}
	// Collect all events.
	var events []*event.Event
	for evt := range eventCh {
		if evt != nil {
			events = append(events, evt)
		}
	}
	if len(events) == 0 {
		s.writeError(w, fmt.Errorf("no response from agent"), errorTypeInternal, http.StatusInternalServerError)
		return
	}
	// Convert events to response.
	// For non-streaming, we always aggregate all events because the agent
	// may return streaming events even for non-streaming requests.
	var response *openAIResponse
	response, err = s.converter.aggregateStreamingEvents(events)
	if err != nil {
		log.Errorf("openai: failed to aggregate events: %v", err)
		s.writeError(w, err, errorTypeInternal,
			http.StatusInternalServerError)
		return
	}
	if response == nil {
		s.writeError(w, fmt.Errorf("failed to generate response"), errorTypeInternal, http.StatusInternalServerError)
		return
	}
	// Ensure response ID is set.
	if response.ID == "" {
		response.ID = generateResponseID()
	}
	if response.Created == 0 {
		response.Created = time.Now().Unix()
	}
	s.writeJSON(w, response)
}

// handleStreaming handles streaming requests.
func (s *Server) handleStreaming(w http.ResponseWriter, r *http.Request, req *openAIRequest) {
	ctx := r.Context()
	messages, err := s.converter.convertRequest(ctx, req)
	if err != nil {
		log.WarnfContext(
			ctx,
			"openai: failed to convert request: %v",
			err,
		)
		s.writeError(
			w,
			err,
			errorTypeInvalidRequest,
			http.StatusBadRequest,
		)
		return
	}
	if len(messages) == 0 {
		s.writeError(w, errors.New("messages cannot be empty"), errorTypeInvalidRequest, http.StatusBadRequest)
		return
	}
	// Get the last message (user message).
	userMessage := messages[len(messages)-1]
	// Get session ID from header or generate one.
	sessionID := r.Header.Get(headerSessionID)
	if sessionID == "" {
		sessionID = uuid.New().String()
	}
	// Get user ID from header or use default.
	userID := req.User
	if userID == "" {
		userID = defaultUserID
	}
	// Build run options with history.
	runOpts := []agent.RunOption{}
	if len(messages) > 1 {
		runOpts = append(runOpts, agent.WithMessages(messages[:len(messages)-1]))
	}
	// Note: Generation config (temperature, max_tokens, etc.) should be set
	// when creating the agent, not at runtime. OpenAI API parameters are
	// ignored here for now. Users should configure the agent with desired
	// generation config when creating it.
	// Run the agent.
	eventCh, err := s.runner.Run(ctx, userID, sessionID, userMessage, runOpts...)
	if err != nil {
		log.ErrorfContext(
			ctx,
			"openai: failed to run agent: %v",
			err,
		)
		s.writeError(
			w,
			err,
			errorTypeInternal,
			http.StatusInternalServerError,
		)
		return
	}
	// Set up SSE headers.
	w.Header().Set(headerContentType, contentTypeEventStream)
	w.Header().Set(headerCacheControl, cacheControlNoCache)
	w.Header().Set(headerConnection, connectionKeepAlive)
	w.Header().Set(headerAccessControlOrigin, "*")
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeError(w, errors.New("streaming not supported"), errorTypeInternal, http.StatusInternalServerError)
		return
	}
	// Stream events.
	responseID := generateResponseID()
	created := time.Now().Unix()
	for {
		select {
		case <-ctx.Done():
			// Context cancelled, stop processing.
			return
		case evt, ok := <-eventCh:
			if !ok {
				// Channel closed, send done marker and exit.
				fmt.Fprintf(w, "%s%s%s", sseDataPrefix, sseDoneMarker, sseLineEnding)
				flusher.Flush()
				return
			}
			if evt == nil || evt.Response == nil {
				continue
			}
			// Skip partial events that are not meaningful.
			if evt.Response.IsPartial && evt.Response.Done {
				continue
			}
			// Process chunk and check if it's the final event.
			isFinal := s.processStreamingChunk(ctx, w, flusher, evt, responseID, created)
			if isFinal {
				// Send final chunk if needed.
				s.sendFinalChunk(w, flusher, evt, responseID, created)
				// Send done marker.
				fmt.Fprintf(w, "%s%s%s", sseDataPrefix, sseDoneMarker, sseLineEnding)
				flusher.Flush()
				return
			}
		}
	}
}

// processStreamingChunk processes a single streaming chunk and returns true if it's the final event.
func (s *Server) processStreamingChunk(
	_ context.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	evt *event.Event,
	responseID string,
	created int64,
) bool {
	chunkData, err := s.converter.convertToChunk(evt)
	if err != nil {
		log.Errorf("openai: failed to convert event: %v", err)
		return false
	}
	if chunkData == nil {
		return evt.Response.Done && !evt.Response.IsPartial
	}
	// Skip chunks with empty delta unless there's a finish reason.
	if !s.shouldSendChunk(chunkData) {
		return evt.Response.Done && !evt.Response.IsPartial
	}
	// Set consistent ID and created time.
	chunkData.ID = responseID
	chunkData.Created = created
	// Write chunk.
	if !s.writeChunk(w, flusher, chunkData) {
		return false
	}
	return evt.Response.Done && !evt.Response.IsPartial
}

// shouldSendChunk checks if a chunk should be sent (has content or finish reason).
func (s *Server) shouldSendChunk(chunk *openAIChunk) bool {
	if len(chunk.Choices) == 0 {
		return false
	}
	delta := chunk.Choices[0].Delta
	hasContent := delta.Content != "" || len(delta.ToolCalls) > 0 || delta.Role != ""
	hasFinishReason := chunk.Choices[0].FinishReason != nil
	return hasContent || hasFinishReason
}

// writeChunk marshals and writes a chunk to the response.
func (s *Server) writeChunk(w http.ResponseWriter, flusher http.Flusher, chunk *openAIChunk) bool {
	data, err := json.Marshal(chunk)
	if err != nil {
		log.Errorf("openai: failed to marshal chunk: %v", err)
		return false
	}
	fmt.Fprintf(w, "%s%s%s", sseDataPrefix, data, sseLineEnding)
	flusher.Flush()
	return true
}

// sendFinalChunk sends the final chunk with finish reason if usage is available.
func (s *Server) sendFinalChunk(
	w http.ResponseWriter,
	flusher http.Flusher,
	evt *event.Event,
	responseID string,
	created int64,
) {
	if evt.Response == nil || evt.Response.Usage == nil {
		return
	}
	finishReason := finishReasonStop
	if len(evt.Response.Choices) > 0 && evt.Response.Choices[0].FinishReason != nil {
		finishReason = *evt.Response.Choices[0].FinishReason
	}
	finalChunk := &openAIChunk{
		ID:      responseID,
		Object:  objectChatCompletionChunk,
		Created: created,
		Model:   s.modelName,
		Choices: []openAIChunkChoice{
			{
				Index:        0,
				Delta:        openAIMessage{},
				FinishReason: &finishReason,
			},
		},
	}
	// Note: OpenAI streaming doesn't include usage in chunks, but we can send it.
	s.writeChunk(w, flusher, finalChunk)
}

// writeJSON writes a JSON response.
func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set(headerContentType, contentTypeJSON)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Errorf("openai: failed to encode response: %v", err)
	}
}

// writeError writes an error response.
func (s *Server) writeError(w http.ResponseWriter, err error, errorType string, statusCode int) {
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(statusCode)
	errorResp := formatError(err, errorType)
	if err := json.NewEncoder(w).Encode(errorResp); err != nil {
		log.Errorf("openai: failed to encode error: %v", err)
	}
}
