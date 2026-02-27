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
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	defaultBasePath = "/v1"

	defaultMessagesPath = "/gateway/messages"
	defaultStatusPath   = "/gateway/status"
	defaultCancelPath   = "/gateway/cancel"
	defaultHealthPath   = "/healthz"

	defaultChannelName = "http"
	threadKindDM       = "dm"
	threadKindThread   = "thread"

	headerAllow       = "Allow"
	headerContentType = "Content-Type"

	contentTypeJSON = "application/json"

	methodPost = "POST"
	methodGet  = "GET"

	defaultMaxBodyBytes int64 = 1 << 20

	queryRequestID = "request_id"
)

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
	statusPath   string
	cancelPath   string
	healthPath   string

	maxBodyBytes int64

	runner  runner.Runner
	managed runner.ManagedRunner

	sessionIDFunc SessionIDFunc

	allowUsers      map[string]struct{}
	requireMention  bool
	mentionPatterns []string

	lanes *laneLocker

	handler http.Handler
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

	s := &Server{
		basePath:        options.basePath,
		messagesPath:    messagesPath,
		statusPath:      statusPath,
		cancelPath:      cancelPath,
		healthPath:      options.healthPath,
		maxBodyBytes:    options.maxBodyBytes,
		runner:          r,
		managed:         managed,
		sessionIDFunc:   sessionIDFunc,
		allowUsers:      options.allowUsers,
		requireMention:  options.requireMention,
		mentionPatterns: options.mentionPatterns,
		lanes:           newLaneLocker(),
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

type sendMessageRequest struct {
	Channel   string `json:"channel,omitempty"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	Thread    string `json:"thread,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Text      string `json:"text,omitempty"`

	UserID    string `json:"user_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type sendMessageResponse struct {
	SessionID string    `json:"session_id,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	Reply     string    `json:"reply,omitempty"`
	Ignored   bool      `json:"ignored,omitempty"`
	Error     *apiError `json:"error,omitempty"`
}

func (r sendMessageRequest) inbound() InboundMessage {
	channel := strings.TrimSpace(r.Channel)
	if channel == "" {
		channel = defaultChannelName
	}
	return InboundMessage{
		Channel:   channel,
		From:      strings.TrimSpace(r.From),
		To:        strings.TrimSpace(r.To),
		Thread:    strings.TrimSpace(r.Thread),
		MessageID: strings.TrimSpace(r.MessageID),
		Text:      r.Text,
	}
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
		s.writeError(w, apiError{
			Type:    errTypeUnsupported,
			Message: "runner does not support status",
		}, http.StatusNotImplemented)
		return
	}

	requestID := strings.TrimSpace(r.URL.Query().Get(queryRequestID))
	if requestID == "" {
		s.writeError(w, apiError{
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
		s.writeError(w, apiError{
			Type:    errTypeUnsupported,
			Message: "runner does not support cancel",
		}, http.StatusNotImplemented)
		return
	}

	var req cancelRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, apiError{
			Type:    errTypeInvalidRequest,
			Message: err.Error(),
		}, http.StatusBadRequest)
		return
	}

	requestID := strings.TrimSpace(req.RequestID)
	if requestID == "" {
		s.writeError(w, apiError{
			Type:    errTypeInvalidRequest,
			Message: "missing request_id",
		}, http.StatusBadRequest)
		return
	}

	canceled := s.managed.Cancel(requestID)

	w.Header().Set(headerContentType, contentTypeJSON)
	_ = json.NewEncoder(w).Encode(map[string]bool{"canceled": canceled})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set(headerAllow, methodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req sendMessageRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, apiError{
			Type:    errTypeInvalidRequest,
			Message: err.Error(),
		}, http.StatusBadRequest)
		return
	}

	msg := req.inbound()
	msg.Text = strings.TrimSpace(msg.Text)
	if msg.Text == "" {
		s.writeError(w, apiError{
			Type:    errTypeInvalidRequest,
			Message: "missing text",
		}, http.StatusBadRequest)
		return
	}

	userID := strings.TrimSpace(req.UserID)
	if userID == "" {
		userID = msg.From
	}
	if userID == "" {
		s.writeError(w, apiError{
			Type:    errTypeInvalidRequest,
			Message: "missing user_id or from",
		}, http.StatusBadRequest)
		return
	}

	if !s.isUserAllowed(userID) {
		s.writeError(w, apiError{
			Type:    errTypeUnauthorized,
			Message: "user is not allowed",
		}, http.StatusForbidden)
		return
	}

	if s.requireMention && msg.Thread != "" {
		if !containsAny(msg.Text, s.mentionPatterns) {
			s.writeJSON(w, sendMessageResponse{
				Ignored: true,
			}, http.StatusOK)
			return
		}
	}

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		var err error
		sessionID, err = s.sessionIDFunc(msg)
		if err != nil {
			s.writeError(w, apiError{
				Type:    errTypeInvalidRequest,
				Message: err.Error(),
			}, http.StatusBadRequest)
			return
		}
	}

	requestID := strings.TrimSpace(req.RequestID)
	reply, resolvedRequestID, err := s.run(
		r.Context(),
		userID,
		sessionID,
		requestID,
		msg.Text,
	)
	if err != nil {
		log.WarnfContext(
			r.Context(),
			"gateway: run failed: %v",
			err,
		)
		s.writeError(w, apiError{
			Type:    errTypeInternal,
			Message: err.Error(),
		}, http.StatusInternalServerError)
		return
	}

	s.writeJSON(w, sendMessageResponse{
		SessionID: sessionID,
		RequestID: resolvedRequestID,
		Reply:     reply,
	}, http.StatusOK)
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

func (s *Server) writeError(w http.ResponseWriter, err apiError, status int) {
	s.writeJSON(w, sendMessageResponse{Error: &err}, status)
}

func (s *Server) run(
	ctx context.Context,
	userID string,
	sessionID string,
	requestID string,
	text string,
) (string, string, error) {
	var (
		reply    string
		resolved string
		runErr   error
	)
	s.lanes.withLock(sessionID, func() {
		reply, resolved, runErr = s.runLocked(
			ctx,
			userID,
			sessionID,
			requestID,
			text,
		)
	})
	return reply, resolved, runErr
}

func (s *Server) runLocked(
	ctx context.Context,
	userID string,
	sessionID string,
	requestID string,
	text string,
) (string, string, error) {
	runOpts := make([]agent.RunOption, 0, 1)
	if requestID != "" {
		runOpts = append(runOpts, agent.WithRequestID(requestID))
	}

	events, err := s.runner.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(text),
		runOpts...,
	)
	if err != nil {
		return "", "", err
	}

	result := newReplyAccumulator()
	for evt := range events {
		result.Consume(evt)
	}

	if result.Error != nil {
		return "", result.RequestID, result.Error
	}
	if result.Text == "" {
		return "", result.RequestID, errors.New("gateway: empty reply")
	}
	return result.Text, result.RequestID, nil
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
