//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gateway provides an OpenClaw-like gateway layer on top of Runner.
//
// The gateway is responsible for:
//   - Converting inbound requests into Runner invocations.
//   - Serializing runs per session to keep conversations coherent.
//   - Applying basic safety policies (allowlist, mention gating).
//
// This package is intentionally minimal and focuses on a single HTTP channel
// endpoint suitable for MVP usage.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwproto"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	defaultBasePath = "/v1"

	defaultMessagesPath       = "/gateway/messages"
	defaultMessagesStreamPath = defaultMessagesPath +
		gwproto.MessagesStreamSuffix
	defaultStatusPath = "/gateway/status"
	defaultCancelPath = "/gateway/cancel"
	defaultHealthPath = "/healthz"

	defaultChannelName = "http"
	threadKindDM       = "dm"
	threadKindThread   = "thread"

	headerAllow       = "Allow"
	headerContentType = "Content-Type"
	headerCacheCtrl   = "Cache-Control"
	headerConnection  = "Connection"

	contentTypeJSON     = "application/json"
	cacheControlNoCache = "no-cache"
	connectionKeepAlive = "keep-alive"

	methodPost = "POST"
	methodGet  = "GET"

	defaultMaxBodyBytes int64 = 1 << 20

	queryRequestID = "request_id"

	errEmptyReply = "gateway: empty reply"

	emptyReplyFallbackText = "I didn't produce a visible " +
		"reply. Please try again."
	runCanceledMessage = "request canceled"
)

var errEmptyReplyValue = errors.New(errEmptyReply)

const (
	errTypeInvalidRequest = "invalid_request"
	errTypeUnauthorized   = "unauthorized"
	errTypeInternal       = "internal_error"
	errTypeUnsupported    = "unsupported"
)

// InboundMessage represents a normalized inbound message.
type InboundMessage struct {
	Channel   string
	From      string
	To        string
	Thread    string
	MessageID string
	Text      string
}

// DefaultSessionID builds a stable session ID for the inbound message.
//
// It follows the format:
//   - Direct message:  "<channel>:dm:<from>"
//   - Thread message:  "<channel>:thread:<thread>"
func DefaultSessionID(msg InboundMessage) (string, error) {
	channel := strings.TrimSpace(msg.Channel)
	if channel == "" {
		channel = defaultChannelName
	}

	from := strings.TrimSpace(msg.From)
	thread := strings.TrimSpace(msg.Thread)
	if thread != "" {
		return fmt.Sprintf("%s:%s:%s", channel, threadKindThread, thread), nil
	}
	if from == "" {
		return "", errors.New("gateway: missing from for dm session id")
	}
	return fmt.Sprintf("%s:%s:%s", channel, threadKindDM, from), nil
}

// Server provides an HTTP gateway server.
type Server struct {
	basePath     string
	messagesPath string
	streamPath   string
	statusPath   string
	cancelPath   string
	healthPath   string

	maxBodyBytes int64
	maxPartBytes int64

	partFetcher partFetcher

	runner  runner.Runner
	managed runner.ManagedRunner

	appName       string
	sessionIDFunc SessionIDFunc

	allowUsers        map[string]struct{}
	requireMention    bool
	mentionPatterns   []string
	runOptionResolver RunOptionResolver

	lanes *laneLocker

	canceled *cancelTracker

	handler http.Handler

	recorder         *debugrecorder.Recorder
	uploads          *uploads.Store
	audioTranscriber audioTranscriber
	personaStore     *persona.Store
	memoryFileStore  *memoryfile.Store
}

// New creates a gateway server with the provided runner.
func New(r runner.Runner, opts ...Option) (*Server, error) {
	if r == nil {
		return nil, errors.New("gateway: runner must not be nil")
	}

	options := newOptions(opts...)
	if options.requireMention && len(options.mentionPatterns) == 0 {
		return nil, errors.New(
			"gateway: require mention enabled without patterns",
		)
	}

	messagesPath, err := joinURLPath(options.basePath, options.messagesPath)
	if err != nil {
		return nil, fmt.Errorf("gateway: join messages path: %w", err)
	}
	streamPath, err := joinURLPath(options.basePath, options.streamPath)
	if err != nil {
		return nil, fmt.Errorf("gateway: join stream path: %w", err)
	}
	statusPath, err := joinURLPath(options.basePath, options.statusPath)
	if err != nil {
		return nil, fmt.Errorf("gateway: join status path: %w", err)
	}
	cancelPath, err := joinURLPath(options.basePath, options.cancelPath)
	if err != nil {
		return nil, fmt.Errorf("gateway: join cancel path: %w", err)
	}

	sessionIDFunc := options.sessionIDFunc
	if sessionIDFunc == nil {
		sessionIDFunc = DefaultSessionID
	}

	var managed runner.ManagedRunner
	if mr, ok := r.(runner.ManagedRunner); ok {
		managed = mr
	}

	policy := partURLPolicy{
		allowPrivate:    options.allowPrivatePartURLs,
		allowedPatterns: options.allowedPartPatterns,
	}
	fetcher := options.partFetcher
	if fetcher == nil {
		fetcher = newURLPartFetcher(policy)
	} else {
		fetcher = validatingFetcher{
			next:   fetcher,
			policy: policy,
		}
	}
	audioTranscriber := options.audioTranscriber
	if audioTranscriber == nil {
		audioTranscriber = newDefaultAudioTranscriber()
	}

	s := &Server{
		basePath:          options.basePath,
		messagesPath:      messagesPath,
		streamPath:        streamPath,
		statusPath:        statusPath,
		cancelPath:        cancelPath,
		healthPath:        options.healthPath,
		maxBodyBytes:      options.maxBodyBytes,
		maxPartBytes:      options.maxPartBytes,
		partFetcher:       fetcher,
		runner:            r,
		managed:           managed,
		appName:           strings.TrimSpace(options.appName),
		sessionIDFunc:     sessionIDFunc,
		allowUsers:        options.allowUsers,
		requireMention:    options.requireMention,
		mentionPatterns:   options.mentionPatterns,
		runOptionResolver: options.runOptionResolver,
		lanes:             newLaneLocker(),
		canceled:          newCancelTracker(),
		recorder:          options.recorder,
		uploads:           options.uploads,
		audioTranscriber:  audioTranscriber,
		personaStore:      options.personaStore,
		memoryFileStore:   options.memoryFileStore,
	}

	mux := http.NewServeMux()
	s.setupRoutes(mux)
	s.handler = mux
	return s, nil
}

// Handler returns the HTTP handler for the gateway server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// BasePath returns the configured base path.
func (s *Server) BasePath() string {
	return s.basePath
}

// MessagesPath returns the full path for the messages endpoint.
func (s *Server) MessagesPath() string {
	return s.messagesPath
}

// MessagesStreamPath returns the full path for the streaming messages
// endpoint.
func (s *Server) MessagesStreamPath() string {
	return s.streamPath
}

// StatusPath returns the full path for the status endpoint.
func (s *Server) StatusPath() string {
	return s.statusPath
}

// CancelPath returns the full path for the cancel endpoint.
func (s *Server) CancelPath() string {
	return s.cancelPath
}

// HealthPath returns the health check endpoint path.
func (s *Server) HealthPath() string {
	return s.healthPath
}

func (s *Server) setupRoutes(mux *http.ServeMux) {
	mux.HandleFunc(s.messagesPath, s.handleMessages)
	mux.HandleFunc(s.messagesPath+"/", s.handleMessages)
	mux.HandleFunc(s.streamPath, s.handleMessagesStream)
	mux.HandleFunc(s.streamPath+"/", s.handleMessagesStream)

	mux.HandleFunc(s.statusPath, s.handleStatus)
	mux.HandleFunc(s.statusPath+"/", s.handleStatus)

	mux.HandleFunc(s.cancelPath, s.handleCancel)
	mux.HandleFunc(s.cancelPath+"/", s.handleCancel)

	mux.HandleFunc(s.healthPath, s.handleHealth)
	mux.HandleFunc(s.healthPath+"/", s.handleHealth)
}

func joinURLPath(basePath, path string) (string, error) {
	return url.JoinPath(basePath, path)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, methodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set(headerAllow, methodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.managed == nil {
		s.writeError(w, gwproto.APIError{
			Type:    errTypeUnsupported,
			Message: "runner does not support status",
		}, http.StatusNotImplemented)
		return
	}

	requestID := strings.TrimSpace(r.URL.Query().Get(queryRequestID))
	if requestID == "" {
		s.writeError(w, gwproto.APIError{
			Type:    errTypeInvalidRequest,
			Message: "missing request_id",
		}, http.StatusBadRequest)
		return
	}

	status, ok := s.managed.RunStatus(requestID)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(status)
}

type cancelRequest struct {
	RequestID string `json:"request_id,omitempty"`
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, methodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.managed == nil {
		s.writeError(w, gwproto.APIError{
			Type:    errTypeUnsupported,
			Message: "runner does not support cancel",
		}, http.StatusNotImplemented)
		return
	}

	var req cancelRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, gwproto.APIError{
			Type:    errTypeInvalidRequest,
			Message: err.Error(),
		}, http.StatusBadRequest)
		return
	}

	canceled, apiErr, status := s.CancelRequest(r.Context(), req.RequestID)
	if apiErr != nil {
		s.writeError(w, *apiErr, status)
		return
	}

	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(map[string]bool{"canceled": canceled})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, methodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

	rsp, status := s.ProcessMessage(r.Context(), req)
	s.writeJSON(w, rsp, status)
}

func (s *Server) isUserAllowed(userID string) bool {
	if s.allowUsers == nil {
		return true
	}
	_, ok := s.allowUsers[userID]
	return ok
}

func containsAny(text string, patterns []string) bool {
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func (s *Server) decodeJSON(r *http.Request, target any) error {
	if r == nil {
		return errors.New("nil request")
	}
	reader := io.LimitReader(r.Body, s.maxBodyBytes)
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}

func (s *Server) writeJSON(w http.ResponseWriter, payload any, status int) {
	w.Header().Set(headerContentType, contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) writeError(
	w http.ResponseWriter,
	err gwproto.APIError,
	status int,
) {
	s.writeJSON(
		w,
		gwproto.MessageResponse{Error: &err},
		status,
	)
}

func (s *Server) run(
	ctx context.Context,
	run preparedMessageRun,
) (string, string, error) {
	var (
		reply    string
		resolved string
		runErr   error
	)
	s.lanes.withLock(run.sessionID, func() {
		reply, resolved, runErr = s.runLocked(
			ctx,
			run,
		)
	})
	return reply, resolved, runErr
}

func (s *Server) runLocked(
	ctx context.Context,
	run preparedMessageRun,
) (string, string, error) {
	trace := debugrecorder.TraceFromContext(ctx)

	if trace != nil {
		_ = trace.Record(
			debugrecorder.KindGatewayRun,
			map[string]any{
				"user_id":    run.userID,
				"session_id": run.sessionID,
				"request_id": run.requestID,
			},
		)
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
		if trace != nil {
			_ = trace.RecordError(err)
		}
		return "", "", err
	}

	result := newReplyAccumulator()
	for evt := range events {
		if trace != nil && evt != nil {
			_ = trace.Record(debugrecorder.KindRunnerEvent, evt)
		}
		result.Consume(evt)
	}

	if result.Error != nil {
		if trace != nil {
			_ = trace.RecordError(result.Error)
		}
		return "", result.RequestID, result.Error
	}
	if result.Text == "" {
		if trace != nil {
			_ = trace.RecordError(errEmptyReplyValue)
		}
		return "", result.RequestID, errEmptyReplyValue
	}
	return result.Text, result.RequestID, nil
}

func (s *Server) resolveRunOptions(
	ctx context.Context,
	run preparedMessageRun,
) (context.Context, []agent.RunOption) {
	runOpts := s.runOptions(
		ctx,
		run.userID,
		run.sessionID,
		run.requestID,
		run.requestSystemPrompt,
	)
	if s == nil || s.runOptionResolver == nil {
		return ctx, runOpts
	}

	resolvedCtx, extra := s.runOptionResolver(
		ctx,
		RunOptionInput{
			Inbound:   run.inbound,
			UserID:    run.userID,
			SessionID: run.sessionID,
			RequestID: run.requestID,
			Message:   run.userMsg,
			Trace:     debugrecorder.TraceFromContext(ctx),
		},
	)
	if resolvedCtx != nil {
		ctx = resolvedCtx
	}
	if len(extra) == 0 {
		return ctx, runOpts
	}
	runOpts = append(runOpts, extra...)
	return ctx, runOpts
}

func (s *Server) runOptions(
	ctx context.Context,
	userID string,
	sessionID string,
	requestID string,
	requestSystemPrompt string,
) []agent.RunOption {
	runOpts := make([]agent.RunOption, 0, 1)
	if requestID != "" {
		runOpts = append(runOpts, agent.WithRequestID(requestID))
	}
	if messages := s.injectedContextMessages(
		ctx,
		userID,
		sessionID,
		requestSystemPrompt,
	); len(messages) > 0 {
		runOpts = append(
			runOpts,
			agent.WithInjectedContextMessages(messages),
		)
	}
	return runOpts
}

type replyAccumulator struct {
	Text      string
	RequestID string
	Error     error

	seenFull bool
	builder  strings.Builder
}

func newReplyAccumulator() *replyAccumulator {
	return &replyAccumulator{}
}

func (a *replyAccumulator) Consume(evt *event.Event) {
	if evt == nil {
		return
	}
	if evt.RequestID != "" {
		a.RequestID = evt.RequestID
	}
	if evt.Response == nil {
		return
	}
	if evt.Error != nil {
		a.Error = errors.New(evt.Error.Message)
		return
	}
	switch evt.Object {
	case model.ObjectTypeChatCompletion:
		a.consumeFull(evt.Response)
	case model.ObjectTypeChatCompletionChunk:
		a.consumeDelta(evt.Response)
	default:
		return
	}
}

func (a *replyAccumulator) consumeFull(rsp *model.Response) {
	if rsp == nil {
		return
	}
	if responseHasPublicContent(rsp) {
		a.builder.Reset()
		a.Text = ""
		return
	}
	if len(rsp.Choices) == 0 {
		return
	}
	content := rsp.Choices[0].Message.Content
	if content == "" {
		return
	}
	a.Text = content
	a.seenFull = true
}

func (a *replyAccumulator) consumeDelta(rsp *model.Response) {
	if rsp == nil {
		return
	}
	if responseHasPublicContent(rsp) {
		a.builder.Reset()
		a.Text = ""
		return
	}
	if a.seenFull {
		return
	}
	for _, choice := range rsp.Choices {
		if choice.Delta.Content == "" {
			continue
		}
		a.builder.WriteString(choice.Delta.Content)
	}
	a.Text = a.builder.String()
}

type laneLocker struct {
	mu    sync.Mutex
	lanes map[string]*laneEntry
}

type cancelTracker struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func newCancelTracker() *cancelTracker {
	return &cancelTracker{
		ids: make(map[string]struct{}),
	}
}

func (t *cancelTracker) Mark(requestID string) {
	if t == nil {
		return
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ids[requestID] = struct{}{}
}

func (t *cancelTracker) Take(requestID string) bool {
	if t == nil {
		return false
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.ids[requestID]; !ok {
		return false
	}
	delete(t.ids, requestID)
	return true
}

func newLaneLocker() *laneLocker {
	return &laneLocker{
		lanes: make(map[string]*laneEntry),
	}
}

func (l *laneLocker) withLock(key string, fn func()) {
	if l == nil {
		fn()
		return
	}

	entry := l.acquire(key)
	entry.lock.Lock()
	defer func() {
		entry.lock.Unlock()
		l.release(key, entry)
	}()
	fn()
}

type laneEntry struct {
	lock sync.Mutex
	refs int
}

func (l *laneLocker) acquire(key string) *laneEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.lanes[key]
	if ok {
		entry.refs++
		return entry
	}

	entry = &laneEntry{refs: 1}
	l.lanes[key] = entry
	return entry
}

func (l *laneLocker) release(key string, entry *laneEntry) {
	if l == nil || entry == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	current, ok := l.lanes[key]
	if !ok || current != entry {
		return
	}

	entry.refs--
	if entry.refs > 0 {
		return
	}
	delete(l.lanes, key)
}
