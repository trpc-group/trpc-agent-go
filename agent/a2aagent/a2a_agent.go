//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package a2aagent provides an agent that can communicate with remote A2A agents.
package a2aagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-a2a-go/client"
	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultStreamingChannelSize         = 1024
	defaultNonStreamingChannelSize      = 10
	defaultUserIDHeader                 = "X-User-ID"
	anonymousUserIDCookieName           = "trpc_agent_a2a_anon"
	anonymousUserIDPrefix               = "A2A_ANONYMOUS_"
	anonymousUserIDCookieStateKeyPrefix = "trpc.agent.a2a.anonymous_user_id_cookie."
	anonymousUserIDCookieEncodedBytes   = 16
)

// A2AAgent is an agent that communicates with a remote A2A agent via A2A protocol.
type A2AAgent struct {
	// options
	name                 string
	description          string
	agentCard            *server.AgentCard      // Agent card and resolution state
	agentURL             string                 // URL of the remote A2A agent
	eventConverter       A2AEventConverter      // Custom A2A event converters
	dataPartMappers      []A2ADataPartMapper    // Lightweight inbound DataPart mappers for default converter
	a2aMessageConverter  InvocationA2AConverter // Custom A2A message converters for requests
	extraA2AOptions      []client.Option        // Additional A2A client options
	streamingBufSize     int                    // Buffer size for streaming responses
	streamingRespHandler StreamingRespHandler   // Handler for streaming responses
	transferStateKey     []string               // Keys in session state to transfer to the A2A agent message by metadata
	buildMessageHook     BuildMessageHook       // Hook called after A2A message is built but before it is sent
	userIDHeader         string                 // HTTP header name to send UserID to A2A server
	enableStreaming      *bool                  // Explicitly set streaming mode; nil means use agent card capability

	a2aClient    *client.A2AClient
	a2aClientURL string

	anonymousCookieInitMu    sync.Mutex
	anonymousCookieInitLocks map[anonymousCookieInitScope]*anonymousCookieInitLock
}

type invocationA2AClient struct {
	client          *client.A2AClient
	anonymousCookie *anonymousCookieState
}

type anonymousCookieInitScope struct {
	session *session.Session
	key     string
}

type anonymousCookieInitLock struct {
	mu   sync.Mutex
	refs int
}

// New creates a new A2AAgent.
func New(opts ...Option) (*A2AAgent, error) {
	agent := &A2AAgent{
		eventConverter:      &defaultA2AEventConverter{},
		a2aMessageConverter: &defaultEventA2AConverter{},
		streamingBufSize:    defaultStreamingChannelSize,
	}

	for _, opt := range opts {
		opt(agent)
	}

	if len(agent.dataPartMappers) > 0 {
		if converter, ok := agent.eventConverter.(*defaultA2AEventConverter); ok {
			for _, mapper := range agent.dataPartMappers {
				if mapper == nil {
					continue
				}
				converter.dataPartMappers = append(converter.dataPartMappers, mapper)
			}
		} else {
			log.Warn(
				"WithA2ADataPartMapper is ignored because WithCustomEventConverter provided a custom converter",
			)
		}
	}

	var agentURL string
	if agent.agentCard != nil {
		agentURL = agent.agentCard.URL
	} else if agent.agentURL != "" {
		agentURL = agent.agentURL
	} else {
		log.Info("agent card or agent card url not set")
	}

	// Normalize the URL to ensure it has a proper scheme
	agentURL = ia2a.NormalizeURL(agentURL)

	// Create A2A client first
	a2aClient, err := client.NewA2AClient(agentURL, agent.extraA2AOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create A2A client for %s: %w", agentURL, err)
	}
	agent.a2aClient = a2aClient
	agent.a2aClientURL = agentURL

	// If agent card is not set, fetch it using A2A client's GetAgentCard method
	if agent.agentCard == nil {
		agentCard, err := a2aClient.GetAgentCard(context.Background(), "")
		if err != nil {
			return nil, fmt.Errorf("failed to fetch agent card from %s: %w", agentURL, err)
		}

		// Set name and description from agent card if not already set
		if agent.name == "" {
			agent.name = agentCard.Name
		}
		if agent.description == "" {
			agent.description = agentCard.Description
		}

		if agentCard.URL == "" {
			agentCard.URL = agentURL
		} else {
			// Normalize the agent card URL to ensure it has a proper scheme
			agentCard.URL = ia2a.NormalizeURL(agentCard.URL)
		}

		// Rebuild a2a client if URL changed
		if agentCard.URL != agentURL {
			a2aClient, err := client.NewA2AClient(agentCard.URL, agent.extraA2AOptions...)
			if err != nil {
				return nil, fmt.Errorf("failed to create A2A client for %s: %w", agentCard.URL, err)
			}
			agent.a2aClient = a2aClient
			agent.a2aClientURL = agentCard.URL
		}

		agent.agentCard = agentCard
	}

	return agent, nil
}

func (r *A2AAgent) clientForInvocation(
	invocation *agent.Invocation,
) (*invocationA2AClient, error) {
	if !needsAnonymousClient(invocation) || r.a2aClientURL == "" {
		return &invocationA2AClient{client: r.a2aClient}, nil
	}
	anonymousCookie := newAnonymousCookieState(
		anonymousSessionFromInvocation(invocation),
		anonymousPersistentSessionFromInvocation(invocation),
		anonymousSessionServiceFromInvocation(invocation),
		anonymousCookieStateKey(r.a2aClientURL),
	)
	a2aClient, err := r.newAnonymousClient(anonymousCookie)
	if err != nil {
		return nil, err
	}
	return &invocationA2AClient{
		client:          a2aClient,
		anonymousCookie: anonymousCookie,
	}, nil
}

func needsAnonymousClient(invocation *agent.Invocation) bool {
	return invocation == nil ||
		invocation.Session == nil ||
		strings.TrimSpace(invocation.Session.UserID) == ""
}

func anonymousSessionFromInvocation(invocation *agent.Invocation) *session.Session {
	if invocation == nil ||
		invocation.Session == nil {
		return nil
	}
	return invocation.Session
}

func anonymousSessionServiceFromInvocation(
	invocation *agent.Invocation,
) session.Service {
	if invocation == nil {
		return nil
	}
	return invocation.SessionService
}

func anonymousPersistentSessionFromInvocation(
	invocation *agent.Invocation,
) *session.Session {
	for current := invocation; current != nil; current = current.GetParentInvocation() {
		if hasPersistentSessionKey(current.Session) {
			return current.Session
		}
	}
	return nil
}

func hasPersistentSessionKey(sess *session.Session) bool {
	return sess != nil &&
		strings.TrimSpace(sess.AppName) != "" &&
		strings.TrimSpace(sess.UserID) != "" &&
		strings.TrimSpace(sess.ID) != ""
}

func (r *A2AAgent) newAnonymousClient(
	anonymousCookie *anonymousCookieState,
) (*client.A2AClient, error) {
	if hasCustomA2AHTTPReqHandlerOption(r.extraA2AOptions) {
		return nil, errors.New(
			"custom A2A HTTP request handler is not supported for anonymous invocations",
		)
	}
	opts := make([]client.Option, 0, len(r.extraA2AOptions)+1)
	opts = append(opts, r.extraA2AOptions...)
	opts = append(opts, client.WithHTTPReqHandler(&anonymousCookieHTTPReqHandler{
		cookie:                anonymousCookie,
		scope:                 anonymousCookieURLScopeFromAgentURL(r.a2aClientURL),
		acquireInitialization: r.acquireAnonymousCookieInitialization,
	}))
	a2aClient, err := client.NewA2AClient(r.a2aClientURL, opts...)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create session-scoped A2A client for %s: %w",
			r.a2aClientURL,
			err,
		)
	}
	return a2aClient, nil
}

func (r *A2AAgent) acquireAnonymousCookieInitialization(
	sess *session.Session,
	key string,
) func() {
	if r == nil || sess == nil || key == "" {
		return nil
	}
	scope := anonymousCookieInitScope{session: sess, key: key}
	r.anonymousCookieInitMu.Lock()
	if r.anonymousCookieInitLocks == nil {
		r.anonymousCookieInitLocks = make(map[anonymousCookieInitScope]*anonymousCookieInitLock)
	}
	entry := r.anonymousCookieInitLocks[scope]
	if entry == nil {
		entry = &anonymousCookieInitLock{}
		r.anonymousCookieInitLocks[scope] = entry
	}
	entry.refs++
	r.anonymousCookieInitMu.Unlock()

	entry.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			entry.mu.Unlock()
			r.anonymousCookieInitMu.Lock()
			entry.refs--
			if entry.refs == 0 && r.anonymousCookieInitLocks[scope] == entry {
				delete(r.anonymousCookieInitLocks, scope)
			}
			r.anonymousCookieInitMu.Unlock()
		})
	}
}

func hasCustomA2AHTTPReqHandlerOption(opts []client.Option) bool {
	if len(opts) == 0 {
		return false
	}
	probe, err := client.NewA2AClient("http://127.0.0.1/")
	if err != nil {
		return false
	}
	defaultHandlerType := a2aClientHTTPReqHandlerType(probe)
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(probe)
		if a2aClientHTTPReqHandlerType(probe) != defaultHandlerType {
			return true
		}
	}
	return false
}

func a2aClientHTTPReqHandlerType(a2aClient *client.A2AClient) string {
	if a2aClient == nil {
		return ""
	}
	field := reflect.ValueOf(a2aClient).Elem().FieldByName("httpReqHandler")
	if !field.IsValid() || field.Kind() != reflect.Interface || field.IsNil() {
		return ""
	}
	return field.Elem().Type().String()
}

type anonymousCookieState struct {
	session        *session.Session
	persistSession *session.Session
	sessionService session.Service
	key            string
}

func newAnonymousCookieState(
	sess *session.Session,
	persistSession *session.Session,
	sessionService session.Service,
	key string,
) *anonymousCookieState {
	return &anonymousCookieState{
		session:        sess,
		persistSession: persistSession,
		sessionService: sessionService,
		key:            key,
	}
}

func (s *anonymousCookieState) load() (string, bool) {
	if s == nil || s.session == nil || s.key == "" {
		return "", false
	}
	raw, ok := s.session.GetState(s.key)
	if !ok {
		return "", false
	}
	cookieValue := strings.TrimSpace(string(raw))
	if !isAnonymousUserIDCookieValue(cookieValue) {
		return "", false
	}
	return cookieValue, true
}

func (s *anonymousCookieState) capture(ctx context.Context, cookieValue string) {
	if s == nil || s.key == "" {
		return
	}
	cookieValue = strings.TrimSpace(cookieValue)
	if !isAnonymousUserIDCookieValue(cookieValue) {
		return
	}
	if s.session != nil {
		s.session.SetState(s.key, []byte(cookieValue))
	}
	s.persist(ctx, cookieValue)
}

func (s *anonymousCookieState) persist(ctx context.Context, cookieValue string) {
	if s == nil ||
		s.sessionService == nil ||
		!hasPersistentSessionKey(s.persistSession) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	state := session.StateMap{s.key: []byte(cookieValue)}
	s.persistSession.SetState(s.key, []byte(cookieValue))
	key := session.Key{
		AppName:   s.persistSession.AppName,
		UserID:    s.persistSession.UserID,
		SessionID: s.persistSession.ID,
	}
	if err := s.sessionService.UpdateSessionState(ctx, key, state); err != nil {
		log.WarnfContext(ctx, "persist anonymous A2A cookie state skipped or failed: %v", err)
	}
}

func anonymousCookieStateKey(agentURL string) string {
	scope := canonicalAnonymousCookieStateScope(agentURL)
	sum := sha256.Sum256([]byte(scope))
	return anonymousUserIDCookieStateKeyPrefix + hex.EncodeToString(sum[:])
}

func canonicalAnonymousCookieStateScope(agentURL string) string {
	normalized := ia2a.NormalizeURL(strings.TrimSpace(agentURL))
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(normalized, "/")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = canonicalAnonymousCookieURLPath(parsed)
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

type anonymousCookieHTTPReqHandler struct {
	cookie                *anonymousCookieState
	scope                 anonymousCookieURLScope
	acquireInitialization func(*session.Session, string) func()
}

func (h *anonymousCookieHTTPReqHandler) Handle(
	ctx context.Context,
	httpClient *http.Client,
	req *http.Request,
) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("a2a anonymous cookie handler: request is nil")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if release := h.acquireInitializationIfNeeded(req.URL); release != nil {
		defer release()
	}
	// Session state owns anonymous identity; a shared user-supplied Jar must
	// not replay another local session's remote principal.
	requestClient := *httpClient
	requestClient.Jar = &anonymousCookieJar{
		ctx:    ctx,
		base:   httpClient.Jar,
		cookie: h.cookie,
		scope:  h.scope,
	}
	return requestClient.Do(req.Clone(ctx))
}

func (h *anonymousCookieHTTPReqHandler) acquireInitializationIfNeeded(
	u *url.URL,
) func() {
	if h == nil ||
		h.cookie == nil ||
		h.acquireInitialization == nil ||
		!h.scope.matches(u) {
		return nil
	}
	if _, ok := h.cookie.load(); ok {
		return nil
	}
	release := h.acquireInitialization(h.cookie.session, h.cookie.key)
	if release == nil {
		return nil
	}
	if _, ok := h.cookie.load(); ok {
		release()
		return nil
	}
	return release
}

type anonymousCookieJar struct {
	ctx    context.Context
	base   http.CookieJar
	cookie *anonymousCookieState
	scope  anonymousCookieURLScope
}

func (j *anonymousCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	if len(cookies) == 0 {
		return
	}
	forwarded := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		if cookie.Name == anonymousUserIDCookieName {
			if j.cookie != nil && j.scope.matches(u) {
				j.cookie.capture(j.ctx, cookie.Value)
			}
			continue
		}
		forwarded = append(forwarded, cookie)
	}
	if j.base == nil || len(forwarded) == 0 {
		return
	}
	j.base.SetCookies(u, forwarded)
}

func (j *anonymousCookieJar) Cookies(u *url.URL) []*http.Cookie {
	var cookies []*http.Cookie
	if j.base != nil {
		for _, cookie := range j.base.Cookies(u) {
			if cookie == nil || cookie.Name == anonymousUserIDCookieName {
				continue
			}
			cookies = append(cookies, cookie)
		}
	}
	if j.cookie != nil {
		if cookieValue, ok := j.cookie.load(); ok && j.scope.matches(u) {
			cookies = append(cookies, &http.Cookie{
				Name:  anonymousUserIDCookieName,
				Value: cookieValue,
			})
		}
	}
	return cookies
}

type anonymousCookieURLScope struct {
	scheme string
	host   string
	path   string
}

func anonymousCookieURLScopeFromAgentURL(agentURL string) anonymousCookieURLScope {
	scope := canonicalAnonymousCookieStateScope(agentURL)
	parsed, err := url.Parse(scope)
	if err != nil {
		return anonymousCookieURLScope{}
	}
	return anonymousCookieURLScope{
		scheme: parsed.Scheme,
		host:   parsed.Host,
		path:   parsed.Path,
	}
}

func (s anonymousCookieURLScope) matches(u *url.URL) bool {
	if u == nil || s.scheme == "" || s.host == "" {
		return false
	}
	if !strings.EqualFold(u.Scheme, s.scheme) ||
		!strings.EqualFold(u.Host, s.host) {
		return false
	}
	basePath := s.path
	if basePath == "" {
		return true
	}
	reqPath := canonicalAnonymousCookieURLPath(u)
	return reqPath == basePath || strings.HasPrefix(reqPath, basePath+"/")
}

func canonicalAnonymousCookieURLPath(u *url.URL) string {
	if u == nil {
		return ""
	}
	urlPath := u.EscapedPath()
	if urlPath == "" {
		urlPath = u.Path
	}
	if unescaped, err := url.PathUnescape(urlPath); err == nil {
		urlPath = unescaped
	}
	cleaned := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if cleaned == "/" {
		return ""
	}
	return strings.TrimRight(cleaned, "/")
}

func isAnonymousUserIDCookieValue(value string) bool {
	if !strings.HasPrefix(value, anonymousUserIDPrefix) {
		return false
	}
	encoded := strings.TrimPrefix(value, anonymousUserIDPrefix)
	decoded, err := hex.DecodeString(encoded)
	return err == nil && len(decoded) == anonymousUserIDCookieEncodedBytes
}

// sendErrorEvent sends an error event to the event channel.
func (r *A2AAgent) sendErrorEvent(
	ctx context.Context,
	eventChan chan<- *event.Event,
	invocation *agent.Invocation,
	err error,
	_ ...*anonymousCookieState,
) *model.ResponseError {
	respErr := model.ResponseErrorFromError(err, model.ErrorTypeRunError)
	evt := event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			Object: model.ObjectTypeError,
			Error:  respErr,
		}),
	)
	agent.EmitEvent(ctx, invocation, eventChan, evt)
	return respErr
}

// validateA2ARequestOptions validates that all A2A request options are of the correct type
func (r *A2AAgent) validateA2ARequestOptions(invocation *agent.Invocation) error {
	if invocation.RunOptions.A2ARequestOptions == nil {
		return nil
	}

	for i, opt := range invocation.RunOptions.A2ARequestOptions {
		if _, ok := opt.(client.RequestOption); !ok {
			return fmt.Errorf("A2ARequestOptions[%d] is not a valid client.RequestOption, got type %T", i, opt)
		}
	}
	return nil
}

func (r *A2AAgent) setupInvocation(invocation *agent.Invocation) {
	invocation.Agent = r
	invocation.AgentName = r.name
}

// Run implements the Agent interface
func (r *A2AAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	var err error
	if invocation != nil {
		r.setupInvocation(invocation)
	}
	useStreaming := r.shouldUseStreaming(invocation)
	ctx, span, startedSpan := itrace.StartSpan(
		ctx,
		invocation,
		fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, r.name),
	)
	if startedSpan {
		itelemetry.TraceBeforeInvokeAgent(
			span,
			invocation,
			r.description,
			"",
			&model.GenerationConfig{Stream: useStreaming},
		)
	}
	tracker := itelemetry.NewInvokeAgentTracker(ctx, invocation, useStreaming, &err)
	// Validate A2A request options early
	if err := r.validateA2ARequestOptions(invocation); err != nil {
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	invocationClient, err := r.clientForInvocation(invocation)
	if err != nil {
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	if invocationClient == nil || invocationClient.client == nil {
		err = errors.New("A2A client is nil")
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	var (
		eventChan <-chan *event.Event
	)
	if useStreaming {
		eventChan, err = r.runStreamingWithClient(
			ctx,
			invocation,
			invocationClient.client,
			invocationClient.anonymousCookie,
		)
	} else {
		eventChan, err = r.runNonStreamingWithClient(
			ctx,
			invocation,
			invocationClient.client,
			invocationClient.anonymousCookie,
		)
	}
	if err != nil {
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	return r.wrapEventChannelWithTelemetry(ctx, invocation, eventChan, span, tracker, startedSpan), nil
}

// shouldUseStreaming determines whether to use streaming protocol.
//
// Priority:
//  1. Per-run override (agent.WithStream / invocation.RunOptions.Stream)
//  2. Agent option (WithEnableStreaming)
//  3. Agent card capability
//  4. Default false
func (r *A2AAgent) shouldUseStreaming(invocation *agent.Invocation) bool {
	// Per-run override.
	if invocation != nil && invocation.RunOptions.Stream != nil {
		return *invocation.RunOptions.Stream
	}

	// If explicitly set via option, use that value
	if r.enableStreaming != nil {
		return *r.enableStreaming
	}

	// Otherwise check if agent card supports streaming
	if r.agentCard != nil && r.agentCard.Capabilities.Streaming != nil {
		return *r.agentCard.Capabilities.Streaming
	}

	// Default to non-streaming if capabilities are not specified
	return false
}

// buildA2AMessage constructs A2A message from session events.
// It assembles a middleware chain around the base converter:
//
//	transferStateKey → user hook → base converter
//
// transferStateKey is the outermost layer so it always runs even if
// the user hook short-circuits (skips calling next).
func (r *A2AAgent) buildA2AMessage(invocation *agent.Invocation, isStream bool) (*protocol.Message, error) {
	if r.a2aMessageConverter == nil {
		return nil, fmt.Errorf("a2a message converter not set")
	}

	// Base converter function.
	convertFn := r.a2aMessageConverter.ConvertToA2AMessage

	// User hook layer wraps the base converter.
	if r.buildMessageHook != nil {
		convertFn = r.buildMessageHook(convertFn)
	}

	// Built-in layer (outermost): transfer state keys into message metadata.
	// Placed after hook so it always runs regardless of hook behavior.
	if len(r.transferStateKey) > 0 {
		convertFn = r.wrapWithTransferState(convertFn)
	}

	message, err := convertFn(isStream, r.name, invocation)
	if err != nil {
		return nil, fmt.Errorf("A2A message conversion failed: %w", err)
	}
	if message == nil {
		return nil, errors.New("A2A message conversion returned nil message")
	}
	return message, nil
}

// wrapWithTransferState returns a middleware that injects transferStateKey values
// from RuntimeState into the message metadata after calling next.
//
// Supported patterns:
//   - "*"        — transfer all keys
//   - "prefix*"  — transfer keys with the given prefix (e.g. "user.*" or "user*")
//   - "*suffix"  — transfer keys with the given suffix (e.g. "*.id" or "*id")
//   - "exact"    — transfer only the exact key
func (r *A2AAgent) wrapWithTransferState(next ConvertToA2AMessageFunc) ConvertToA2AMessageFunc {
	return func(isStream bool, agentName string, invocation *agent.Invocation) (*protocol.Message, error) {
		message, err := next(isStream, agentName, invocation)
		if err != nil {
			return nil, err
		}
		if message == nil {
			return nil, nil
		}
		if invocation.RunOptions.RuntimeState == nil {
			return message, nil
		}
		if message.Metadata == nil {
			message.Metadata = make(map[string]any)
		}
		for _, pattern := range r.transferStateKey {
			matchStateKeys(pattern, invocation.RunOptions.RuntimeState, message.Metadata)
		}
		return message, nil
	}
}

// matchStateKeys copies keys from src to dst that match the given pattern.
func matchStateKeys(pattern string, src map[string]any, dst map[string]any) {
	switch {
	case pattern == "*":
		for k, v := range src {
			dst[k] = v
		}
	case strings.HasPrefix(pattern, "*"):
		suffix := pattern[1:]
		for k, v := range src {
			if strings.HasSuffix(k, suffix) {
				dst[k] = v
			}
		}
	case strings.HasSuffix(pattern, "*"):
		prefix := pattern[:len(pattern)-1]
		for k, v := range src {
			if strings.HasPrefix(k, prefix) {
				dst[k] = v
			}
		}
	default:
		if v, ok := src[pattern]; ok {
			dst[pattern] = v
		}
	}
}

// runStreaming handles streaming A2A communication
func (r *A2AAgent) runStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return r.runStreamingWithClient(ctx, invocation, r.a2aClient, nil)
}

func (r *A2AAgent) runStreamingWithClient(
	ctx context.Context,
	invocation *agent.Invocation,
	a2aClient *client.A2AClient,
	anonymousCookie *anonymousCookieState,
) (<-chan *event.Event, error) {
	if r.eventConverter == nil {
		return nil, fmt.Errorf("event converter not set")
	}
	eventChan := make(chan *event.Event, r.streamingBufSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)
		r.executeStreaming(ctx, invocation, eventChan, a2aClient, anonymousCookie)
	}(runCtx)
	return eventChan, nil
}

// executeStreaming executes the streaming A2A communication workflow.
func (r *A2AAgent) executeStreaming(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	a2aClient *client.A2AClient,
	anonymousCookie *anonymousCookieState,
) {
	a2aMessage, err := r.buildA2AMessage(invocation, true)
	if err != nil {
		r.sendErrorEvent(
			ctx,
			eventChan,
			invocation,
			fmt.Errorf("failed to construct A2A message: %w", err),
			anonymousCookie,
		)
		return
	}

	requestOpts := r.buildRequestOptions(ctx, invocation)
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	streamChan, err := a2aClient.StreamMessage(
		streamCtx,
		protocol.SendMessageParams{Message: *a2aMessage},
		requestOpts...,
	)
	if err != nil {
		r.sendErrorEvent(
			ctx,
			eventChan,
			invocation,
			fmt.Errorf(
				"A2A streaming request failed to %s: %w",
				r.agentCard.URL,
				err,
			),
			anonymousCookie,
		)
		return
	}

	streamResult := r.processStreamingEvents(
		streamCtx,
		invocation,
		eventChan,
		streamChan,
		anonymousCookie,
	)
	if streamResult.terminalError != nil {
		return
	}
	r.emitFinalEvent(
		ctx,
		invocation,
		eventChan,
		streamResult.responseID,
		streamResult.aggregatedContent,
		anonymousCookie,
	)
}

// buildRequestOptions constructs A2A request options from invocation.
func (r *A2AAgent) buildRequestOptions(ctx context.Context, invocation *agent.Invocation) []client.RequestOption {
	var requestOpts []client.RequestOption
	if invocation.RunOptions.A2ARequestOptions != nil {
		for _, opt := range invocation.RunOptions.A2ARequestOptions {
			requestOpts = append(requestOpts, opt.(client.RequestOption))
		}
	}
	// Add UserID header if session has UserID
	if invocation.Session != nil && invocation.Session.UserID != "" {
		userIDHeader := r.userIDHeader
		if userIDHeader == "" {
			userIDHeader = defaultUserIDHeader
		}
		requestOpts = append(requestOpts, client.WithRequestHeader(userIDHeader, invocation.Session.UserID))
	}
	// Propagate trace context via HTTP headers (W3C Trace Context).
	traceHeaders := extractTraceHeaders(ctx)
	for k, v := range traceHeaders {
		requestOpts = append(requestOpts, client.WithRequestHeader(k, v))
	}
	return requestOpts
}

type streamingEventResult struct {
	responseID        string
	aggregatedContent string
	terminalError     *model.ResponseError
}

// processStreamingEvents processes streaming events and aggregates content.
// Returns the response ID, aggregated content, and terminal error state.
func (r *A2AAgent) processStreamingEvents(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	streamChan <-chan protocol.StreamingMessageEvent,
	anonymousCookie *anonymousCookieState,
) streamingEventResult {
	var result streamingEventResult
	var contentBuilder strings.Builder

	for streamEvent := range streamChan {
		if err := agent.CheckContextCancelled(ctx); err != nil {
			result.aggregatedContent = contentBuilder.String()
			return result
		}

		events, err := r.eventConverter.ConvertStreamingToEvents(streamEvent, r.name, invocation)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("custom event converter failed: %w", err),
				anonymousCookie,
			)
			result.aggregatedContent = contentBuilder.String()
			return result
		}

		for _, evt := range events {
			if evt == nil {
				continue
			}
			currentResponseID := result.responseID
			if evt.Response != nil && evt.Response.ID != "" {
				currentResponseID = evt.Response.ID
			}
			if evt.Response != nil && !evt.Response.IsPartial {
				r.flushBufferedContent(
					ctx,
					invocation,
					eventChan,
					currentResponseID,
					evt.Timestamp,
					&contentBuilder,
					anonymousCookie,
				)
			}
			var terminalError *model.ResponseError
			result.responseID, terminalError = r.aggregateEventContent(
				ctx,
				invocation,
				eventChan,
				evt,
				result.responseID,
				&contentBuilder,
				anonymousCookie,
			)
			if terminalError != nil {
				result.aggregatedContent = contentBuilder.String()
				result.terminalError = terminalError
				return result
			}
			agent.EmitEvent(ctx, invocation, eventChan, evt)
			if evt.Response != nil &&
				evt.Response.Error != nil &&
				evt.Response.Done {
				result.aggregatedContent = contentBuilder.String()
				result.terminalError = evt.Response.Error
				return result
			}
		}
	}
	result.aggregatedContent = contentBuilder.String()
	return result
}

// flushBufferedContent emits buffered streaming text as a complete assistant
// message before forwarding a non-partial event such as a tool call or tool
// response. This preserves the original turn order in session history.
func (r *A2AAgent) flushBufferedContent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	responseID string,
	anchorTimestamp time.Time,
	contentBuilder *strings.Builder,
	anonymousCookie *anonymousCookieState,
) {
	if contentBuilder == nil || contentBuilder.Len() == 0 {
		return
	}

	content := contentBuilder.String()
	contentBuilder.Reset()

	flushTime := time.Now()
	if !anchorTimestamp.IsZero() {
		flushTime = anchorTimestamp.Add(-1 * time.Nanosecond)
	}

	evt := event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			ID:        responseID,
			Object:    model.ObjectTypeChatCompletion,
			Done:      false,
			IsPartial: false,
			Timestamp: flushTime,
			Created:   flushTime.Unix(),
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: content,
				},
			}},
		}),
	)
	evt.Timestamp = flushTime
	agent.EmitEvent(ctx, invocation, eventChan, evt)
}

// aggregateEventContent aggregates content from event delta.
// Returns updated responseID and any terminal error that occurred.
func (r *A2AAgent) aggregateEventContent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	evt *event.Event,
	responseID string,
	contentBuilder *strings.Builder,
	anonymousCookie *anonymousCookieState,
) (string, *model.ResponseError) {
	if evt.Response == nil || evt.Response.Error != nil {
		return responseID, nil
	}
	if len(evt.Response.Choices) == 0 {
		return responseID, nil
	}

	if evt.Response.ID != "" {
		responseID = evt.Response.ID
	}

	if r.streamingRespHandler != nil {
		content, err := r.streamingRespHandler(evt.Response)
		if err != nil {
			respErr := r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("streaming resp handler failed: %w", err),
				anonymousCookie,
			)
			return responseID, respErr
		}
		if content != "" {
			contentBuilder.WriteString(content)
		}
	} else if evt.Response.Choices[0].Delta.Content != "" {
		contentBuilder.WriteString(evt.Response.Choices[0].Delta.Content)
	}
	return responseID, nil
}

// emitFinalEvent emits the final completion event.
func (r *A2AAgent) emitFinalEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	responseID string,
	aggregatedContent string,
	anonymousCookie *anonymousCookieState,
) {
	evt := event.New(
		invocation.InvocationID,
		r.name,
		event.WithResponse(&model.Response{
			ID:        responseID,
			Object:    model.ObjectTypeChatCompletion,
			Done:      true,
			IsPartial: false,
			Timestamp: time.Now(),
			Created:   time.Now().Unix(),
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: aggregatedContent,
				},
			}},
		}),
	)
	agent.EmitEvent(ctx, invocation, eventChan, evt)
}

// runNonStreaming handles non-streaming A2A communication
func (r *A2AAgent) runNonStreaming(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return r.runNonStreamingWithClient(ctx, invocation, r.a2aClient, nil)
}

func (r *A2AAgent) runNonStreamingWithClient(
	ctx context.Context,
	invocation *agent.Invocation,
	a2aClient *client.A2AClient,
	anonymousCookie *anonymousCookieState,
) (<-chan *event.Event, error) {
	eventChan := make(chan *event.Event, defaultNonStreamingChannelSize)
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		defer close(eventChan)

		// Construct A2A message from session
		a2aMessage, err := r.buildA2AMessage(invocation, false)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("failed to construct A2A message: %w", err),
				anonymousCookie,
			)
			return
		}

		params := protocol.SendMessageParams{
			Message: *a2aMessage,
		}
		requestOpts := r.buildRequestOptions(ctx, invocation)
		result, err := a2aClient.SendMessage(ctx, params, requestOpts...)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf(
					"A2A request failed to %s: %w",
					r.agentCard.URL,
					err,
				),
				anonymousCookie,
			)
			return
		}

		// Convert A2A response to multiple events
		msgResult := protocol.MessageResult{Result: result.Result}
		events, err := r.eventConverter.ConvertToEvents(msgResult, r.name, invocation)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("custom event converter failed: %w", err),
				anonymousCookie,
			)
			return
		}

		// Emit all events
		for _, evt := range events {
			agent.EmitEvent(ctx, invocation, eventChan, evt)
		}
	}(runCtx)
	return eventChan, nil
}

func (r *A2AAgent) wrapEventChannelWithTelemetry(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
	span sdktrace.Span,
	tracker *itelemetry.InvokeAgentTracker,
	startedSpan bool,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, cap(originalChan))
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		var fullRespEvent *event.Event
		var responseErrorType string
		tokenUsage := &itelemetry.TokenUsage{}
		defer func() {
			if fullRespEvent != nil && fullRespEvent.Response != nil {
				responseErrorType = ""
				if fullRespEvent.Response.Error != nil {
					responseErrorType = itelemetry.FormatResponseErrorLabel(
						fullRespEvent.Response.Error,
						model.ErrorTypeRunError,
					)
				}
			}
			if startedSpan && fullRespEvent != nil {
				log.DebugContext(ctx, "fullRespEvent is not ni")
				itelemetry.TraceAfterInvokeAgent(
					span,
					fullRespEvent,
					tokenUsage,
					tracker.FirstTokenTimeDuration(),
					model.ErrorTypeRunError,
				)
			}
			tracker.SetResponseErrorType(responseErrorType)
			tracker.RecordMetrics()()
			if startedSpan {
				span.End()
			}
			close(wrappedChan)
		}()
		for evt := range originalChan {
			if evt != nil && evt.Response != nil {
				tracker.TrackResponse(evt.Response)
				if !evt.Response.IsPartial {
					if evt.Response.Usage != nil {
						tokenUsage.PromptTokens += evt.Response.Usage.PromptTokens
						tokenUsage.CompletionTokens += evt.Response.Usage.CompletionTokens
						tokenUsage.TotalTokens += evt.Response.Usage.TotalTokens
					}
					fullRespEvent = evt
				}
			}
			if evt != nil && evt.Error != nil {
				responseErrorType = itelemetry.FormatResponseErrorLabel(
					evt.Error,
					model.ErrorTypeRunError,
				)
			}
			if err := event.EmitEvent(ctx, wrappedChan, evt); err != nil {
				return
			}
		}
	}(runCtx)

	return wrappedChan
}

// Tools implements the Agent interface
func (r *A2AAgent) Tools() []tool.Tool {
	// Remote A2A agents don't expose tools directly
	// Tools are handled by the remote agent
	return []tool.Tool{}
}

// Info implements the Agent interface
func (r *A2AAgent) Info() agent.Info {
	return agent.Info{
		Name:        r.name,
		Description: r.description,
	}
}

// SubAgents implements the Agent interface
func (r *A2AAgent) SubAgents() []agent.Agent {
	// Remote A2A agents don't have sub-agents in the local context
	return []agent.Agent{}
}

// FindSubAgent implements the Agent interface
func (r *A2AAgent) FindSubAgent(name string) agent.Agent {
	// Remote A2A agents don't have sub-agents in the local context
	return nil
}

// GetAgentCard returns the resolved agent card
func (r *A2AAgent) GetAgentCard() *server.AgentCard {
	return r.agentCard
}

// extractTraceHeaders extracts W3C Trace Context headers from ctx using the
// globally registered OpenTelemetry propagator. Returns a map of header
// key-value pairs (e.g. "traceparent" -> "00-..."). Returns nil when ctx
// carries no valid span context.
func extractTraceHeaders(ctx context.Context) map[string]string {
	propagator := otel.GetTextMapPropagator()
	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	return carrier
}
