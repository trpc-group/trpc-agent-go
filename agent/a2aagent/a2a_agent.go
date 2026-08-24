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
	"strconv"
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
	defaultStreamingChannelSize          = 1024
	defaultNonStreamingChannelSize       = 10
	defaultUserIDHeader                  = "X-User-ID"
	anonymousUserIDCookieName            = "trpc_agent_a2a_anon"
	anonymousUserIDPrefix                = "A2A_ANONYMOUS_"
	anonymousUserIDScopeSeparator        = "_"
	anonymousUserIDCookieStateKeyPrefix  = "trpc.agent.a2a.anonymous_user_id_cookie."
	anonymousUserIDCookieSecureKeySuffix = ".secure"
	anonymousUserIDCookiePathKeySuffix   = ".path"
	anonymousUserIDCookieDomainKeySuffix = ".domain"
	anonymousUserIDCookieExpiryKeySuffix = ".expires"
	anonymousUserIDCookieEncodedBytes    = 16
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

	// anonymousCookieInitLocks serializes first anonymous-cookie acquisition
	// only within this A2AAgent instance. It does not coordinate separate
	// A2AAgent instances or processes that share the same SessionService; those
	// callers may still race during first-cookie initialization until
	// persistence-backed lease/CAS coordination is added.
	anonymousCookieInitMu       sync.Mutex
	anonymousCookieInitLocks    map[anonymousCookieInitScope]*anonymousCookieInitLock
	anonymousCookieInitWaitHook func(anonymousCookieInitScope)
}

type invocationA2AClient struct {
	client          *client.A2AClient
	anonymousCookie *anonymousCookieState
}

type anonymousCookieInitScope struct {
	persistentSession session.Key
	transientSession  *session.Session
	cookieStateKey    string
}

type anonymousCookieInitLock struct {
	gate chan struct{}
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
	a2aClient, err := agent.newConfiguredA2AClient(agentURL)
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
			a2aClient, err := agent.newConfiguredA2AClient(agentCard.URL)
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
	return &invocationA2AClient{
		client:          r.a2aClient,
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

func (r *A2AAgent) newConfiguredA2AClient(
	agentURL string,
) (*client.A2AClient, error) {
	// Register the stateless anonymous-cookie middleware once on the base client.
	// The invocation-specific cookie state is carried by the request context.
	opts := make([]client.Option, 0, len(r.extraA2AOptions)+1)
	opts = append(opts, client.WithMiddleware(a2aHTTPReqMiddleware(
		func(next client.HTTPReqHandler) client.HTTPReqHandler {
			return &anonymousCookieHTTPReqHandler{
				next:                  next,
				scope:                 anonymousCookieURLScopeFromAgentURL(agentURL),
				acquireInitialization: r.acquireAnonymousCookieInitialization,
			}
		},
	)))
	opts = append(opts, r.extraA2AOptions...)
	return client.NewA2AClient(agentURL, opts...)
}

type a2aHTTPReqMiddleware func(next client.HTTPReqHandler) client.HTTPReqHandler

func (m a2aHTTPReqMiddleware) Wrap(next client.HTTPReqHandler) client.HTTPReqHandler {
	return m(next)
}

func (r *A2AAgent) acquireAnonymousCookieInitialization(
	ctx context.Context,
	cookie *anonymousCookieState,
) (func(), error) {
	if r == nil {
		return nil, nil
	}
	scope, ok := cookie.initializationScope()
	if !ok {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.anonymousCookieInitMu.Lock()
	if r.anonymousCookieInitLocks == nil {
		r.anonymousCookieInitLocks = make(map[anonymousCookieInitScope]*anonymousCookieInitLock)
	}
	entry := r.anonymousCookieInitLocks[scope]
	if entry == nil {
		entry = &anonymousCookieInitLock{gate: make(chan struct{}, 1)}
		r.anonymousCookieInitLocks[scope] = entry
	}
	entry.refs++
	r.anonymousCookieInitMu.Unlock()

	releaseRef := func() {
		r.anonymousCookieInitMu.Lock()
		entry.refs--
		if entry.refs == 0 && r.anonymousCookieInitLocks[scope] == entry {
			delete(r.anonymousCookieInitLocks, scope)
		}
		r.anonymousCookieInitMu.Unlock()
	}
	select {
	case entry.gate <- struct{}{}:
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	default:
		if r.anonymousCookieInitWaitHook != nil {
			r.anonymousCookieInitWaitHook(scope)
		}
		select {
		case entry.gate <- struct{}{}:
		case <-ctx.Done():
			releaseRef()
			return nil, ctx.Err()
		}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-entry.gate
			releaseRef()
		})
	}, nil
}

type anonymousCookieState struct {
	session        *session.Session
	persistSession *session.Session
	sessionService session.Service
	key            string
}

type anonymousCookieRecord struct {
	value   string
	secure  bool
	path    string
	domain  string
	expires time.Time
}

type anonymousCookieContextKey struct{}

func withAnonymousCookieState(
	ctx context.Context,
	cookie *anonymousCookieState,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, anonymousCookieContextKey{}, cookie)
}

func anonymousCookieStateFromContext(ctx context.Context) *anonymousCookieState {
	if ctx == nil {
		return nil
	}
	cookie, _ := ctx.Value(anonymousCookieContextKey{}).(*anonymousCookieState)
	return cookie
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

func (s *anonymousCookieState) persistentSessionKey() (session.Key, bool) {
	if s == nil || !hasPersistentSessionKey(s.persistSession) {
		return session.Key{}, false
	}
	return session.Key{
		AppName:   s.persistSession.AppName,
		UserID:    s.persistSession.UserID,
		SessionID: s.persistSession.ID,
	}, true
}

func (s *anonymousCookieState) initializationScope() (anonymousCookieInitScope, bool) {
	if s == nil || s.key == "" {
		return anonymousCookieInitScope{}, false
	}
	if persistentSessionKey, ok := s.persistentSessionKey(); ok {
		return anonymousCookieInitScope{
			persistentSession: persistentSessionKey,
			cookieStateKey:    s.key,
		}, true
	}
	if s.session == nil {
		return anonymousCookieInitScope{}, false
	}
	return anonymousCookieInitScope{
		transientSession: s.session,
		cookieStateKey:   s.key,
	}, true
}

func (s *anonymousCookieState) load() (string, bool) {
	cookieValue, _, ok := s.loadWithSecurity()
	return cookieValue, ok
}

func (s *anonymousCookieState) loadWithSecurity() (string, bool, bool) {
	record, ok := s.loadRecord()
	if !ok {
		return "", false, false
	}
	return record.value, record.secure, true

}

func (s *anonymousCookieState) loadRecord() (anonymousCookieRecord, bool) {
	if s == nil {
		return anonymousCookieRecord{}, false
	}
	if hasPersistentSessionKey(s.persistSession) {
		if record, ok := loadAnonymousCookieStateFromSession(s.persistSession, s.key); ok {
			return record, true
		}
		// A persistent state entry that is expired, malformed, or explicitly
		// cleared must not fall back to a stale transient copy.
		if anonymousCookieStatePresent(s.persistSession, s.key) {
			return anonymousCookieRecord{}, false
		}
	}
	if record, ok := loadAnonymousCookieStateFromSession(s.session, s.key); ok {
		return record, true
	}
	if s.persistSession != s.session {
		return loadAnonymousCookieStateFromSession(s.persistSession, s.key)
	}
	return anonymousCookieRecord{}, false
}

func loadAnonymousCookieFromSession(sess *session.Session, key string) (string, bool) {
	if sess == nil || key == "" {
		return "", false
	}
	raw, ok := sess.GetState(key)
	if !ok {
		return "", false
	}
	cookieValue := strings.TrimSpace(string(raw))
	if !isAnonymousUserIDCookieValue(cookieValue) {
		return "", false
	}
	return cookieValue, true
}

func anonymousCookieStatePresent(sess *session.Session, key string) bool {
	if sess == nil || key == "" {
		return false
	}
	_, ok := sess.GetState(key)
	return ok
}

func loadAnonymousCookieStateFromSession(
	sess *session.Session,
	key string,
) (anonymousCookieRecord, bool) {
	cookieValue, ok := loadAnonymousCookieFromSession(sess, key)
	if !ok {
		return anonymousCookieRecord{}, false
	}
	rawSecure, ok := sess.GetState(key + anonymousUserIDCookieSecureKeySuffix)
	secure := ok && strings.EqualFold(strings.TrimSpace(string(rawSecure)), "true")
	record := anonymousCookieRecord{
		value:  cookieValue,
		secure: secure,
	}
	if rawPath, pathOK := sess.GetState(key + anonymousUserIDCookiePathKeySuffix); pathOK {
		record.path = strings.TrimSpace(string(rawPath))
	}
	if rawDomain, domainOK := sess.GetState(key + anonymousUserIDCookieDomainKeySuffix); domainOK {
		record.domain = normalizeAnonymousCookieDomain(string(rawDomain))
	}
	if rawExpiry, expiryOK := sess.GetState(key + anonymousUserIDCookieExpiryKeySuffix); expiryOK && len(rawExpiry) > 0 {
		expires, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(rawExpiry)))
		if err != nil || !expires.After(time.Now()) {
			return anonymousCookieRecord{}, false
		}
		record.expires = expires
	}
	return record, true
}

func storeAnonymousCookieRecord(
	sess *session.Session,
	key string,
	record anonymousCookieRecord,
) {
	if sess == nil || key == "" {
		return
	}
	// Store restrictions first so a concurrent reader never observes a newly
	// acquired credential without its send restrictions.
	sess.SetState(
		key+anonymousUserIDCookieSecureKeySuffix,
		[]byte(strconv.FormatBool(record.secure)),
	)
	storeAnonymousCookieMetadata(sess, key+anonymousUserIDCookiePathKeySuffix, record.path)
	storeAnonymousCookieMetadata(sess, key+anonymousUserIDCookieDomainKeySuffix, record.domain)
	expires := ""
	if !record.expires.IsZero() {
		expires = record.expires.UTC().Format(time.RFC3339Nano)
	}
	storeAnonymousCookieMetadata(sess, key+anonymousUserIDCookieExpiryKeySuffix, expires)
	sess.SetState(key, []byte(record.value))
}

func storeAnonymousCookieMetadata(sess *session.Session, key, value string) {
	if value == "" {
		sess.SetState(key, nil)
		return
	}
	sess.SetState(key, []byte(value))
}

func clearAnonymousCookieState(sess *session.Session, key string) {
	if sess == nil || key == "" {
		return
	}
	// Clear the value first so a concurrent reader cannot replay the credential
	// while the remaining metadata is being removed.
	sess.SetState(key, nil)
	sess.SetState(key+anonymousUserIDCookieSecureKeySuffix, nil)
	sess.SetState(key+anonymousUserIDCookiePathKeySuffix, nil)
	sess.SetState(key+anonymousUserIDCookieDomainKeySuffix, nil)
	sess.SetState(key+anonymousUserIDCookieExpiryKeySuffix, nil)
}

func (s *anonymousCookieState) reload(ctx context.Context) error {
	if s == nil || s.sessionService == nil {
		return nil
	}
	persistentSessionKey, ok := s.persistentSessionKey()
	if !ok {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	persistedSession, err := s.sessionService.GetSession(ctx, persistentSessionKey)
	if err != nil {
		return fmt.Errorf("reload anonymous A2A cookie state: %w", err)
	}
	record, ok := loadAnonymousCookieStateFromSession(persistedSession, s.key)
	if !ok {
		return nil
	}
	storeAnonymousCookieRecord(s.session, s.key, record)
	storeAnonymousCookieRecord(s.persistSession, s.key, record)
	return nil
}

func (s *anonymousCookieState) capture(ctx context.Context, cookieValue string) error {
	return s.captureWithSecurity(ctx, cookieValue, false)
}

func (s *anonymousCookieState) captureWithSecurity(
	ctx context.Context,
	cookieValue string,
	secure bool,
) error {
	return s.captureRecord(ctx, anonymousCookieRecord{
		value:  cookieValue,
		secure: secure,
	})
}

func (s *anonymousCookieState) captureRecord(
	ctx context.Context,
	record anonymousCookieRecord,
) error {
	if s == nil || s.key == "" {
		return nil
	}
	record.value = strings.TrimSpace(record.value)
	if !isAnonymousUserIDCookieValue(record.value) {
		return nil
	}
	if !record.expires.IsZero() && !record.expires.After(time.Now()) {
		return s.clear(ctx)
	}
	if current, ok := s.loadRecord(); ok && current.value == record.value {
		record.secure = record.secure || current.secure
		if current.equal(record) {
			storeAnonymousCookieRecord(s.session, s.key, current)
			return nil
		}
	}
	if err := s.persist(ctx, record); err != nil {
		return err
	}
	storeAnonymousCookieRecord(s.session, s.key, record)
	return nil
}

func (s *anonymousCookieState) persist(
	ctx context.Context,
	record anonymousCookieRecord,
) error {
	if s == nil || !hasPersistentSessionKey(s.persistSession) {
		return nil
	}
	if s.sessionService == nil {
		storeAnonymousCookieRecord(s.persistSession, s.key, record)
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	state := anonymousCookieRecordStateMap(s.key, record)
	key := session.Key{
		AppName:   s.persistSession.AppName,
		UserID:    s.persistSession.UserID,
		SessionID: s.persistSession.ID,
	}
	if err := s.sessionService.UpdateSessionState(ctx, key, state); err != nil {
		return fmt.Errorf("persist anonymous A2A cookie state: %w", err)
	}
	storeAnonymousCookieRecord(s.persistSession, s.key, record)
	return nil
}

func (s *anonymousCookieState) clear(ctx context.Context) error {
	if s == nil || s.key == "" {
		return nil
	}
	if hasPersistentSessionKey(s.persistSession) && s.sessionService != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		key := session.Key{
			AppName:   s.persistSession.AppName,
			UserID:    s.persistSession.UserID,
			SessionID: s.persistSession.ID,
		}
		if err := s.sessionService.UpdateSessionState(ctx, key, anonymousCookieClearedStateMap(s.key)); err != nil {
			return fmt.Errorf("clear anonymous A2A cookie state: %w", err)
		}
	}
	clearAnonymousCookieState(s.session, s.key)
	clearAnonymousCookieState(s.persistSession, s.key)
	return nil
}

func anonymousCookieRecordStateMap(key string, record anonymousCookieRecord) session.StateMap {
	state := anonymousCookieClearedStateMap(key)
	state[key] = []byte(record.value)
	state[key+anonymousUserIDCookieSecureKeySuffix] = []byte(strconv.FormatBool(record.secure))
	if record.path != "" {
		state[key+anonymousUserIDCookiePathKeySuffix] = []byte(record.path)
	}
	if record.domain != "" {
		state[key+anonymousUserIDCookieDomainKeySuffix] = []byte(record.domain)
	}
	if !record.expires.IsZero() {
		state[key+anonymousUserIDCookieExpiryKeySuffix] = []byte(record.expires.UTC().Format(time.RFC3339Nano))
	}
	return state
}

func anonymousCookieClearedStateMap(key string) session.StateMap {
	return session.StateMap{
		key: nil,
		key + anonymousUserIDCookieSecureKeySuffix: nil,
		key + anonymousUserIDCookiePathKeySuffix:   nil,
		key + anonymousUserIDCookieDomainKeySuffix: nil,
		key + anonymousUserIDCookieExpiryKeySuffix: nil,
	}
}

func (r anonymousCookieRecord) equal(other anonymousCookieRecord) bool {
	return r.value == other.value &&
		r.secure == other.secure &&
		r.path == other.path &&
		r.domain == other.domain &&
		r.expires.Equal(other.expires)
}

func (r anonymousCookieRecord) matchesForSend(u *url.URL) bool {
	if u == nil || (!r.expires.IsZero() && !r.expires.After(time.Now())) {
		return false
	}
	if r.secure && !strings.EqualFold(u.Scheme, "https") {
		return false
	}
	if r.path != "" && !anonymousCookiePathMatches(u.Path, r.path) {
		return false
	}
	if r.domain != "" && !anonymousCookieDomainMatchesURL(u, r.domain) {
		return false
	}
	return true
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
	next client.HTTPReqHandler
	// cookie is only used by directly constructed handlers in package tests.
	// The client middleware created by newConfiguredA2AClient reads state from
	// the request context so the shared handler remains stateless.
	cookie                *anonymousCookieState
	scope                 anonymousCookieURLScope
	acquireInitialization func(context.Context, *anonymousCookieState) (func(), error)
}

type anonymousCookieCaptureResult struct {
	mu       sync.Mutex
	err      error
	captured map[anonymousCookieCaptureKey]struct{}
}

type anonymousCookieCaptureKey struct {
	value   string
	secure  bool
	path    string
	domain  string
	expires int64
	clear   bool
}

func (r *anonymousCookieCaptureResult) record(err error) {
	if r == nil || err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *anonymousCookieCaptureResult) error() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *anonymousCookieCaptureResult) capture(
	ctx context.Context,
	cookie *anonymousCookieState,
	cookieValue string,
	secure bool,
) {
	r.captureRecord(ctx, cookie, anonymousCookieRecord{
		value:  cookieValue,
		secure: secure,
	})
}

func (r *anonymousCookieCaptureResult) captureRecord(
	ctx context.Context,
	cookie *anonymousCookieState,
	record anonymousCookieRecord,
) {
	if cookie == nil || !isAnonymousUserIDCookieValue(strings.TrimSpace(record.value)) {
		return
	}
	record.value = strings.TrimSpace(record.value)
	if r != nil {
		r.mu.Lock()
		if r.captured == nil {
			r.captured = make(map[anonymousCookieCaptureKey]struct{})
		}
		captureKey := anonymousCookieCaptureKey{
			value:  record.value,
			secure: record.secure,
			path:   record.path,
			domain: record.domain,
		}
		if !record.expires.IsZero() {
			// Max-Age is relative to the response, so repeated interception of
			// one response can differ by a few nanoseconds. Seconds are enough
			// to deduplicate those observations without hiding a later refresh.
			captureKey.expires = record.expires.Unix()
		}
		if _, ok := r.captured[captureKey]; ok {
			r.mu.Unlock()
			return
		}
		r.captured[captureKey] = struct{}{}
		r.mu.Unlock()
	}
	r.record(cookie.captureRecord(ctx, record))
}

func (r *anonymousCookieCaptureResult) captureCookie(
	ctx context.Context,
	cookie *anonymousCookieState,
	requestURL *url.URL,
	responseCookie *http.Cookie,
) {
	if cookie == nil || responseCookie == nil {
		return
	}
	record, deleted, ok := anonymousCookieRecordFromResponse(requestURL, responseCookie)
	if !ok {
		return
	}
	if deleted {
		captureKey := anonymousCookieCaptureKey{
			path:   record.path,
			domain: record.domain,
			clear:  true,
		}
		if r != nil {
			r.mu.Lock()
			if r.captured == nil {
				r.captured = make(map[anonymousCookieCaptureKey]struct{})
			}
			if _, alreadyCaptured := r.captured[captureKey]; alreadyCaptured {
				r.mu.Unlock()
				return
			}
			r.captured[captureKey] = struct{}{}
			r.mu.Unlock()
		}
		r.record(cookie.clearForCookie(ctx, record))
		return
	}
	r.captureRecord(ctx, cookie, record)
}

func anonymousCookieRecordFromResponse(
	requestURL *url.URL,
	responseCookie *http.Cookie,
) (anonymousCookieRecord, bool, bool) {
	if requestURL == nil || responseCookie == nil || responseCookie.Name != anonymousUserIDCookieName {
		return anonymousCookieRecord{}, false, false
	}
	cookiePath, ok := anonymousCookiePathForResponse(requestURL, responseCookie.Path)
	if !ok {
		return anonymousCookieRecord{}, false, false
	}
	domain := normalizeAnonymousCookieDomain(responseCookie.Domain)
	if domain != "" && !anonymousCookieDomainMatchesURL(requestURL, domain) {
		return anonymousCookieRecord{}, false, false
	}
	if responseCookie.MaxAge < 0 ||
		(responseCookie.MaxAge == 0 && !responseCookie.Expires.IsZero() &&
			!responseCookie.Expires.After(time.Now())) {
		return anonymousCookieRecord{path: cookiePath, domain: domain}, true, true
	}
	cookieValue := strings.TrimSpace(responseCookie.Value)
	if !isAnonymousUserIDCookieValue(cookieValue) {
		return anonymousCookieRecord{}, false, false
	}
	expires := responseCookie.Expires
	if responseCookie.MaxAge > 0 {
		expires = time.Now().Add(time.Duration(responseCookie.MaxAge) * time.Second)
	}
	return anonymousCookieRecord{
		value:   cookieValue,
		secure:  anonymousCookieResponseIsSecure(requestURL, responseCookie),
		path:    cookiePath,
		domain:  domain,
		expires: expires,
	}, false, true
}

func anonymousCookiePathForResponse(u *url.URL, cookiePath string) (string, bool) {
	if u == nil {
		return "", false
	}
	if cookiePath == "" || !strings.HasPrefix(cookiePath, "/") {
		cookiePath = anonymousCookieDefaultPath(u.Path)
	}
	return cookiePath, anonymousCookiePathMatches(u.Path, cookiePath)
}

func anonymousCookieDefaultPath(requestPath string) string {
	if requestPath == "" || !strings.HasPrefix(requestPath, "/") {
		return "/"
	}
	index := strings.LastIndex(requestPath, "/")
	if index <= 0 {
		return "/"
	}
	return requestPath[:index]
}

func anonymousCookiePathMatches(requestPath, cookiePath string) bool {
	if requestPath == "" {
		requestPath = "/"
	}
	if cookiePath == "" || cookiePath[0] != '/' {
		cookiePath = "/"
	}
	if requestPath == cookiePath {
		return true
	}
	if !strings.HasPrefix(requestPath, cookiePath) {
		return false
	}
	return strings.HasSuffix(cookiePath, "/") ||
		(len(requestPath) > len(cookiePath) && requestPath[len(cookiePath)] == '/')
}

func normalizeAnonymousCookieDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	return strings.TrimPrefix(domain, ".")
}

func anonymousCookieDomainMatchesURL(u *url.URL, domain string) bool {
	if u == nil || domain == "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func (s *anonymousCookieState) clearForCookie(
	ctx context.Context,
	deletion anonymousCookieRecord,
) error {
	if s == nil {
		return nil
	}
	if current, ok := s.loadRecord(); ok {
		if current.path != "" && current.path != deletion.path {
			return nil
		}
		if current.domain != deletion.domain {
			return nil
		}
	}
	return s.clear(ctx)
}

func (h *anonymousCookieHTTPReqHandler) Handle(
	ctx context.Context,
	httpClient *http.Client,
	req *http.Request,
) (*http.Response, error) {
	if h == nil {
		return nil, errors.New("a2a anonymous cookie handler: handler is nil")
	}
	if req == nil {
		return nil, errors.New("a2a anonymous cookie handler: request is nil")
	}
	if h.next == nil {
		return nil, errors.New("a2a anonymous cookie handler: next handler is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cookie := h.cookie
	if cookie == nil {
		cookie = anonymousCookieStateFromContext(ctx)
	}
	if cookie == nil {
		return h.next.Handle(ctx, httpClient, req)
	}
	if httpClient == nil {
		return nil, errors.New("a2a anonymous cookie handler: HTTP client is nil")
	}
	release, err := h.acquireInitializationIfNeeded(ctx, req.URL, cookie)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
	}
	captureResult := &anonymousCookieCaptureResult{}
	// Session state owns anonymous identity; a shared user-supplied Jar must
	// not replay another local session's remote principal.
	requestClient := *httpClient
	requestClient.Jar = &anonymousCookieJar{
		ctx:    ctx,
		base:   httpClient.Jar,
		cookie: cookie,
		scope:  h.scope,
		result: captureResult,
	}
	requestClient.Transport = &anonymousCookieRoundTripper{
		base:   httpClient.Transport,
		cookie: cookie,
		scope:  h.scope,
		result: captureResult,
	}
	request := req.Clone(ctx)
	setAnonymousCookieHeader(request, cookie, h.scope)
	resp, err := h.next.Handle(ctx, &requestClient, request)
	h.captureResponseCookie(ctx, request, resp, cookie, captureResult)
	if captureErr := captureResult.error(); captureErr != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			return nil, errors.Join(err, captureErr)
		}
		return nil, captureErr
	}
	return resp, err
}

func (h *anonymousCookieHTTPReqHandler) captureResponseCookie(
	ctx context.Context,
	req *http.Request,
	resp *http.Response,
	cookie *anonymousCookieState,
	result *anonymousCookieCaptureResult,
) {
	if h == nil || cookie == nil || resp == nil || req == nil || !h.scope.matches(req.URL) {
		return
	}
	responseURL := req.URL
	if resp.Request != nil && resp.Request.URL != nil {
		responseURL = resp.Request.URL
	}
	if !h.scope.matches(responseURL) {
		return
	}
	for _, responseCookie := range resp.Cookies() {
		if responseCookie != nil && responseCookie.Name == anonymousUserIDCookieName {
			result.captureCookie(ctx, cookie, responseURL, responseCookie)
		}
	}
}

type anonymousCookieRoundTripper struct {
	base   http.RoundTripper
	cookie *anonymousCookieState
	scope  anonymousCookieURLScope
	result *anonymousCookieCaptureResult
}

func (t *anonymousCookieRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, errors.New("a2a anonymous cookie transport: request is nil")
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	request := req.Clone(req.Context())
	setAnonymousCookieHeader(request, t.cookie, t.scope)
	resp, err := base.RoundTrip(request)
	if resp != nil && t.cookie != nil && t.scope.matches(request.URL) {
		for _, responseCookie := range resp.Cookies() {
			if responseCookie != nil && responseCookie.Name == anonymousUserIDCookieName {
				t.result.captureCookie(request.Context(), t.cookie, request.URL, responseCookie)
			}
		}
	}
	return resp, err
}

func setAnonymousCookieHeader(
	req *http.Request,
	cookie *anonymousCookieState,
	scope anonymousCookieURLScope,
) {
	if req == nil {
		return
	}
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	stripAnonymousCookieHeader(req)
	if cookie == nil {
		return
	}
	if record, ok := cookie.loadRecord(); ok &&
		scope.matchesForSend(req.URL, record.secure) &&
		record.matchesForSend(req.URL) {
		req.AddCookie(&http.Cookie{
			Name:  anonymousUserIDCookieName,
			Value: record.value,
		})
	}
}

func stripAnonymousCookieHeader(req *http.Request) {
	if req == nil {
		return
	}
	cookies := req.Cookies()
	req.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == anonymousUserIDCookieName {
			continue
		}
		req.AddCookie(cookie)
	}
}

type httpClientDoHandler struct{}

func (*httpClientDoHandler) Handle(
	_ context.Context,
	httpClient *http.Client,
	req *http.Request,
) (*http.Response, error) {
	if httpClient == nil {
		return nil, errors.New("a2a HTTP request handler: HTTP client is nil")
	}
	resp, err := httpClient.Do(req)
	if err == nil {
		return resp, nil
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return nil, err
}

func (h *anonymousCookieHTTPReqHandler) acquireInitializationIfNeeded(
	ctx context.Context,
	u *url.URL,
	cookie *anonymousCookieState,
) (func(), error) {
	if h == nil ||
		cookie == nil ||
		h.acquireInitialization == nil ||
		!h.scope.matches(u) {
		return nil, nil
	}
	if _, ok := cookie.load(); ok {
		return nil, nil
	}
	release, err := h.acquireInitialization(ctx, cookie)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, nil
	}
	if _, ok := cookie.load(); ok {
		release()
		return nil, nil
	}
	if err := cookie.reload(ctx); err != nil {
		release()
		return nil, err
	}
	if _, ok := cookie.load(); ok {
		release()
		return nil, nil
	}
	return release, nil
}

type anonymousCookieJar struct {
	ctx    context.Context
	base   http.CookieJar
	cookie *anonymousCookieState
	scope  anonymousCookieURLScope
	result *anonymousCookieCaptureResult
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
				j.result.captureCookie(j.ctx, j.cookie, u, cookie)
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
		if record, ok := j.cookie.loadRecord(); ok &&
			j.scope.matchesForSend(u, record.secure) &&
			record.matchesForSend(u) {
			cookies = append(cookies, &http.Cookie{
				Name:  anonymousUserIDCookieName,
				Value: record.value,
			})
		}
	}
	return cookies
}

type anonymousCookieURLScope struct {
	scheme   string
	hostname string
	port     int
	path     string
}

func anonymousCookieURLScopeFromAgentURL(agentURL string) anonymousCookieURLScope {
	scope := canonicalAnonymousCookieStateScope(agentURL)
	parsed, err := url.Parse(scope)
	if err != nil {
		return anonymousCookieURLScope{}
	}
	return anonymousCookieURLScope{
		scheme:   parsed.Scheme,
		hostname: parsed.Hostname(),
		port:     anonymousCookieURLPort(parsed),
		path:     parsed.Path,
	}
}

func (s anonymousCookieURLScope) matches(u *url.URL) bool {
	if u == nil || s.scheme == "" || s.hostname == "" {
		return false
	}
	requestScheme := strings.ToLower(u.Scheme)
	if requestScheme != s.scheme &&
		!(s.scheme == "http" && requestScheme == "https") {
		return false
	}
	if !strings.EqualFold(u.Hostname(), s.hostname) {
		return false
	}
	requestPort := anonymousCookieURLPort(u)
	if s.scheme == "http" && requestScheme == "https" && s.port == 80 {
		if requestPort != 443 {
			return false
		}
	} else if requestPort != s.port {
		return false
	}
	basePath := s.path
	if basePath == "" {
		return true
	}
	reqPath := canonicalAnonymousCookieURLPath(u)
	return reqPath == basePath || strings.HasPrefix(reqPath, basePath+"/")
}

func (s anonymousCookieURLScope) matchesForSend(u *url.URL, secure bool) bool {
	if secure && (u == nil || !strings.EqualFold(u.Scheme, "https")) {
		return false
	}
	return s.matches(u)
}

func anonymousCookieResponseIsSecure(u *url.URL, cookie *http.Cookie) bool {
	return cookie != nil &&
		(cookie.Secure || (u != nil && strings.EqualFold(u.Scheme, "https")))
}

func anonymousCookieURLPort(u *url.URL) int {
	if u == nil {
		return 0
	}
	if port := u.Port(); port != "" {
		parsedPort, err := strconv.Atoi(port)
		if err == nil {
			return parsedPort
		}
		return 0
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return 80
	case "https":
		return 443
	default:
		return 0
	}
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
	if decoded, err := hex.DecodeString(encoded); err == nil &&
		len(decoded) == anonymousUserIDCookieEncodedBytes {
		return true
	}
	parts := strings.Split(encoded, anonymousUserIDScopeSeparator)
	if len(parts) != 2 {
		return false
	}
	scope, scopeErr := hex.DecodeString(parts[0])
	principal, principalErr := hex.DecodeString(parts[1])
	return scopeErr == nil && len(scope) == sha256.Size &&
		principalErr == nil && len(principal) == anonymousUserIDCookieEncodedBytes
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
	runCtx := withAnonymousCookieState(agent.CloneContext(ctx), anonymousCookie)
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
		streamResult.aggregatedContentParts,
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
	responseID             string
	aggregatedContent      string
	aggregatedContentParts []model.ContentPart
	terminalError          *model.ResponseError
}

// processStreamingEvents processes streaming events and aggregates content.
// Returns the response ID, aggregated content, and terminal error state.
func (r *A2AAgent) processStreamingEvents(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	streamChan <-chan protocol.StreamingMessageEvent,
	anonymousCookie *anonymousCookieState,
) (result streamingEventResult) {
	var contentBuffer ia2a.StreamingTextBuffer
	var contentPartsBuffer ia2a.StreamingTextBuffer
	defer func() {
		result.aggregatedContent = contentBuffer.Content()
		result.aggregatedContentParts = contentPartsBuffer.ContentParts()
	}()

	for streamEvent := range streamChan {
		if err := agent.CheckContextCancelled(ctx); err != nil {
			result.aggregatedContent = contentBuffer.Content()
			return result
		}
		artifactUpdate, _ := streamEvent.Result.(*protocol.TaskArtifactUpdateEvent)
		artifactContentRecorded := false

		events, err := r.eventConverter.ConvertStreamingToEvents(streamEvent, r.name, invocation)
		if err != nil {
			r.sendErrorEvent(
				ctx,
				eventChan,
				invocation,
				fmt.Errorf("custom event converter failed: %w", err),
				anonymousCookie,
			)
			result.aggregatedContent = contentBuffer.Content()
			return result
		}
		replacesArtifact := v0ArtifactUpdateReplacesBufferedContent(
			artifactUpdate,
			events,
		)

		for _, evt := range events {
			if evt == nil {
				continue
			}
			r.flushPendingContentBeforeEvent(
				ctx,
				invocation,
				eventChan,
				evt,
				result.responseID,
				artifactUpdate,
				replacesArtifact,
				&contentBuffer,
				anonymousCookie,
			)
			var eventContent strings.Builder
			var eventContentParts []model.ContentPart
			var terminalError *model.ResponseError
			result.responseID, terminalError = r.aggregateEventContent(
				ctx,
				invocation,
				eventChan,
				evt,
				result.responseID,
				&eventContent,
				anonymousCookie,
				&eventContentParts,
			)
			responseID := ""
			if evt.Response != nil {
				responseID = evt.Response.ID
			}
			if artifactUpdate == nil {
				contentBuffer.AppendContent(
					responseID,
					eventContent.String(),
					eventContentParts,
				)
				contentPartsBuffer.AppendContent(
					responseID,
					"",
					eventContentParts,
				)
			} else {
				content := eventContent.String()
				replace := !artifactContentRecorded && replacesArtifact
				if evt.Response == nil || evt.Response.IsPartial {
					contentBuffer.UpdateArtifactContent(
						responseID,
						artifactUpdate.Artifact.ArtifactID,
						content,
						eventContentParts,
						replace,
					)
				} else if content != "" {
					// A complete artifact Message owns its raw snapshot, but text
					// explicitly aggregated from the event still belongs in the final
					// result. This includes public streaming handler output.
					contentBuffer.Append(responseID, content)
				}
				contentPartsBuffer.UpdateArtifactContent(
					responseID,
					artifactUpdate.Artifact.ArtifactID,
					content,
					eventContentParts,
					replace,
				)
				artifactContentRecorded = true
			}
			if terminalError != nil {
				result.aggregatedContent = contentBuffer.Content()
				result.terminalError = terminalError
				return result
			}
			agent.EmitEvent(ctx, invocation, eventChan, evt)
			if evt.Response != nil &&
				evt.Response.Error != nil &&
				evt.Response.Done {
				result.aggregatedContent = contentBuffer.Content()
				result.terminalError = evt.Response.Error
				return result
			}
		}
		clearFilteredArtifactReplacement(
			&contentBuffer,
			&contentPartsBuffer,
			artifactUpdate,
			replacesArtifact,
			artifactContentRecorded,
		)
	}
	return result
}

func (r *A2AAgent) flushPendingContentBeforeEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	evt *event.Event,
	fallbackResponseID string,
	artifactUpdate *protocol.TaskArtifactUpdateEvent,
	replacesArtifact bool,
	contentBuffer *ia2a.StreamingTextBuffer,
	anonymousCookie *anonymousCookieState,
) {
	if evt.Response == nil || evt.Response.IsPartial {
		return
	}
	responseID := fallbackResponseID
	if evt.Response.ID != "" {
		responseID = evt.Response.ID
	}
	if artifactUpdate != nil && replacesArtifact {
		// The complete event owns this artifact snapshot. Remove its stale
		// partial text before flushing unrelated pending text.
		contentBuffer.UpdateArtifact(
			"",
			artifactUpdate.Artifact.ArtifactID,
			"",
			true,
		)
	}
	r.flushBufferedContent(
		ctx,
		invocation,
		eventChan,
		responseID,
		evt.Timestamp,
		contentBuffer,
		anonymousCookie,
	)
}

func clearFilteredArtifactReplacement(
	contentBuffer *ia2a.StreamingTextBuffer,
	contentPartsBuffer *ia2a.StreamingTextBuffer,
	update *protocol.TaskArtifactUpdateEvent,
	replacesArtifact bool,
	artifactContentRecorded bool,
) {
	if update == nil || !replacesArtifact || artifactContentRecorded {
		return
	}
	artifactID := update.Artifact.ArtifactID
	contentBuffer.UpdateArtifact("", artifactID, "", true)
	contentPartsBuffer.UpdateArtifact("", artifactID, "", true)
}

func v0ArtifactUpdateReplacesBufferedContent(
	update *protocol.TaskArtifactUpdateEvent,
	events []*event.Event,
) bool {
	if update == nil {
		return false
	}
	if update.Append != nil {
		return !*update.Append
	}
	for _, evt := range events {
		if evt != nil && evt.Response != nil &&
			evt.Response.Object == model.ObjectTypeChatCompletion &&
			responseHasSnapshotContent(evt.Response) {
			return true
		}
	}
	return false
}

func responseHasSnapshotContent(response *model.Response) bool {
	if response == nil {
		return false
	}
	for _, choice := range response.Choices {
		for _, message := range []model.Message{choice.Message, choice.Delta} {
			if message.Content != "" || len(message.ContentParts) > 0 {
				return true
			}
		}
	}
	return false
}

// flushBufferedContent emits buffered streaming text as a complete assistant
// message before forwarding a non-partial event such as a tool call or tool
// response. This preserves the original turn order in session history.
func (r *A2AAgent) flushBufferedContent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	fallbackResponseID string,
	anchorTimestamp time.Time,
	contentBuffer *ia2a.StreamingTextBuffer,
	anonymousCookie *anonymousCookieState,
) {
	responseID, content, contentParts, ok := contentBuffer.TakeContent(
		fallbackResponseID,
	)
	if !ok {
		return
	}

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
					Role:         model.RoleAssistant,
					Content:      content,
					ContentParts: contentParts,
				},
			}},
		}),
	)
	evt.Timestamp = flushTime
	agent.EmitEvent(ctx, invocation, eventChan, evt)
}

// aggregateEventContent aggregates content from a streaming event.
// Returns updated responseID and any terminal error that occurred.
func (r *A2AAgent) aggregateEventContent(
	ctx context.Context,
	invocation *agent.Invocation,
	eventChan chan<- *event.Event,
	evt *event.Event,
	responseID string,
	contentBuilder *strings.Builder,
	anonymousCookie *anonymousCookieState,
	contentParts *[]model.ContentPart,
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

	choice := evt.Response.Choices[0]
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
	} else if choice.Delta.Content != "" {
		contentBuilder.WriteString(choice.Delta.Content)
	}
	if contentParts != nil {
		*contentParts = append(
			*contentParts,
			choice.Delta.ContentParts...,
		)
		if !evt.Response.IsPartial {
			*contentParts = append(
				*contentParts,
				choice.Message.ContentParts...,
			)
		}
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
	aggregatedContentParts []model.ContentPart,
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
					Role:         model.RoleAssistant,
					Content:      aggregatedContent,
					ContentParts: aggregatedContentParts,
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
	runCtx := withAnonymousCookieState(agent.CloneContext(ctx), anonymousCookie)
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
