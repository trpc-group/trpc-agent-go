//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package acp exposes a runner through Agent Client Protocol (ACP) v1.
package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultImplementationName    = "trpc-agent-go"
	defaultImplementationVersion = "dev"
	defaultUserID                = "acp"
	defaultShutdownTimeout       = 5 * time.Second
)

// Session describes an ACP session when run options are resolved.
type Session struct {
	ID  string
	CWD string
}

// RunOptionsFunc returns request-scoped runner options for an ACP session.
type RunOptionsFunc func(Session) []agent.RunOption

// Option configures a Server.
type Option func(*options)

type options struct {
	userID                 string
	implementationName     string
	implementationVer      string
	sessionIDGenerator     func() string
	runOptions             []agent.RunOption
	runOptionsFunc         RunOptionsFunc
	reasoningEnabled       bool
	clientFileReadEnabled  bool
	clientFileWriteEnabled bool
}

// WithUserID sets the runner user ID shared by sessions on this server.
func WithUserID(userID string) Option {
	return func(options *options) {
		options.userID = userID
	}
}

// WithImplementation sets the implementation metadata returned by initialize.
func WithImplementation(name, version string) Option {
	return func(options *options) {
		options.implementationName = name
		options.implementationVer = version
	}
}

// WithRunOptions adds static options to every runner invocation.
func WithRunOptions(runOptions ...agent.RunOption) Option {
	return func(options *options) {
		options.runOptions = append(options.runOptions, runOptions...)
	}
}

// WithRunOptionsFunc sets a function that derives runner options from an ACP
// session. It can use Session.CWD to configure a request-scoped coding agent.
func WithRunOptionsFunc(runOptionsFunc RunOptionsFunc) Option {
	return func(options *options) {
		options.runOptionsFunc = runOptionsFunc
	}
}

// WithReasoningContentEnabled controls whether model reasoning is exposed as
// ACP agent_thought_chunk updates. It is disabled by default.
func WithReasoningContentEnabled(enabled bool) Option {
	return func(options *options) {
		options.reasoningEnabled = enabled
	}
}

// WithClientFileReadEnabled controls whether ACP client file reads are exposed
// as model tools. It is disabled by default.
func WithClientFileReadEnabled(enabled bool) Option {
	return func(options *options) {
		options.clientFileReadEnabled = enabled
	}
}

// WithClientFileWriteEnabled controls whether ACP client file writes are
// exposed as model tools. It is disabled by default.
func WithClientFileWriteEnabled(enabled bool) Option {
	return func(options *options) {
		options.clientFileWriteEnabled = enabled
	}
}

// Server adapts a runner to ACP connections.
type Server struct {
	runner                 runner.Runner
	managedRunner          runner.ManagedRunner
	userID                 string
	implementation         acpsdk.Implementation
	sessionIDGenerator     func() string
	runOptions             []agent.RunOption
	runOptionsFunc         RunOptionsFunc
	reasoningEnabled       bool
	clientFileReadEnabled  bool
	clientFileWriteEnabled bool
	sessionMu              sync.Mutex
	activeSessionIDs       map[string]struct{}
}

// New creates an ACP server backed by a runner.
func New(r runner.Runner, opts ...Option) (*Server, error) {
	if r == nil {
		return nil, errors.New("acp: runner is required")
	}
	options := options{
		userID:             defaultUserID,
		implementationName: defaultImplementationName,
		implementationVer:  defaultImplementationVersion,
		sessionIDGenerator: uuid.NewString,
	}
	for _, opt := range opts {
		opt(&options)
	}
	if options.userID == "" {
		return nil, errors.New("acp: user ID is required")
	}
	if options.implementationName == "" {
		return nil, errors.New("acp: implementation name is required")
	}
	if options.implementationVer == "" {
		return nil, errors.New("acp: implementation version is required")
	}
	if options.sessionIDGenerator == nil {
		return nil, errors.New("acp: session ID generator is required")
	}
	server := &Server{
		runner:                 r,
		userID:                 options.userID,
		implementation:         acpsdk.Implementation{Name: options.implementationName, Version: options.implementationVer},
		sessionIDGenerator:     options.sessionIDGenerator,
		runOptions:             append([]agent.RunOption(nil), options.runOptions...),
		runOptionsFunc:         options.runOptionsFunc,
		reasoningEnabled:       options.reasoningEnabled,
		clientFileReadEnabled:  options.clientFileReadEnabled,
		clientFileWriteEnabled: options.clientFileWriteEnabled,
		activeSessionIDs:       make(map[string]struct{}),
	}
	server.managedRunner, _ = r.(runner.ManagedRunner)
	return server, nil
}

type connection struct {
	conn  *acpsdk.AgentSideConnection
	agent *protocolAgent
}

func (c *connection) done() <-chan struct{} {
	return c.conn.Done()
}

func (s *Server) connect(input io.Reader, output io.Writer) (*connection, error) {
	if input == nil {
		return nil, errors.New("acp: input is required")
	}
	if output == nil {
		return nil, errors.New("acp: output is required")
	}
	protocolAgent := newProtocolAgent(s)
	sdkConnection := acpsdk.NewAgentSideConnection(protocolAgent, output, input)
	protocolAgent.connection = sdkConnection
	return &connection{conn: sdkConnection, agent: protocolAgent}, nil
}

// Serve connects an ACP client and blocks until the peer disconnects or ctx is
// canceled. Callers remain responsible for closing their transport.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	connection, err := s.connect(input, output)
	if err != nil {
		log.ErrorContext(ctx, "Failed to create ACP connection", "error", err)
		return err
	}
	log.DebugContext(ctx, "ACP connection established")
	defer log.DebugContext(ctx, "ACP connection closed")
	defer connection.agent.shutdown()
	select {
	case <-connection.done():
		return nil
	case <-ctx.Done():
		log.DebugContext(ctx, "ACP connection canceled", "error", ctx.Err())
		return ctx.Err()
	}
}

type protocolAgent struct {
	server          *Server
	connection      clientConnection
	sessions        map[string]*sessionState
	clientFileMu    sync.RWMutex
	clientFileRead  bool
	clientFileWrite bool
	shuttingDown    bool
	mu              sync.Mutex
}

type clientConnection interface {
	ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error)
	WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error)
	RequestPermission(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error)
	SessionUpdate(context.Context, acpsdk.SessionNotification) error
}

type sessionState struct {
	id           string
	cwd          string
	tools        []tool.Tool
	toolSets     []tool.ToolSet
	cancel       context.CancelFunc
	requestID    string
	promptDone   chan struct{}
	toolCallIDs  *toolCallIDMapper
	writeAllowed bool
	closed       bool
	mu           sync.Mutex

	nextToolCallID atomic.Uint64
}

func newProtocolAgent(server *Server) *protocolAgent {
	return &protocolAgent{
		server:   server,
		sessions: make(map[string]*sessionState),
	}
}

func (a *protocolAgent) Initialize(
	ctx context.Context,
	request acpsdk.InitializeRequest,
) (acpsdk.InitializeResponse, error) {
	log.DebugContext(
		ctx,
		"Received ACP initialize request",
		"protocol_version",
		request.ProtocolVersion,
		"client_file_read",
		request.ClientCapabilities.Fs.ReadTextFile,
		"client_file_write",
		request.ClientCapabilities.Fs.WriteTextFile,
	)
	a.setClientFileCapabilities(request.ClientCapabilities.Fs)
	a.warnUnsupportedClientFileCapabilities(ctx, request.ClientCapabilities.Fs)
	protocolVersion := request.ProtocolVersion
	if protocolVersion != acpsdk.ProtocolVersionNumber {
		protocolVersion = acpsdk.ProtocolVersionNumber
	}
	implementation := a.server.implementation
	log.DebugContext(
		ctx,
		"ACP initialize completed",
		"protocol_version",
		protocolVersion,
		"implementation",
		implementation.Name,
		"implementation_version",
		implementation.Version,
	)
	return acpsdk.InitializeResponse{
		ProtocolVersion: protocolVersion,
		AgentCapabilities: acpsdk.AgentCapabilities{
			SessionCapabilities: acpsdk.SessionCapabilities{
				Close: &acpsdk.SessionCloseCapabilities{},
			},
		},
		AgentInfo:   &implementation,
		AuthMethods: []acpsdk.AuthMethod{},
	}, nil
}

func (a *protocolAgent) NewSession(
	ctx context.Context,
	request acpsdk.NewSessionRequest,
) (acpsdk.NewSessionResponse, error) {
	log.DebugContext(
		ctx,
		"Creating ACP session",
		"cwd",
		request.Cwd,
		"mcp_server_count",
		len(request.McpServers),
		"additional_directory_count",
		len(request.AdditionalDirectories),
	)
	if !filepath.IsAbs(request.Cwd) {
		log.DebugContext(ctx, "Rejected ACP session with relative cwd", "cwd", request.Cwd)
		return acpsdk.NewSessionResponse{}, acpsdk.NewInvalidParams(
			map[string]any{"cwd": "must be an absolute path"},
		)
	}
	if len(request.AdditionalDirectories) != 0 {
		log.DebugContext(
			ctx,
			"Rejected ACP session with additional directories",
			"additional_directory_count",
			len(request.AdditionalDirectories),
		)
		return acpsdk.NewSessionResponse{}, acpsdk.NewInvalidParams(
			map[string]any{"additionalDirectories": "additional directories are not supported"},
		)
	}
	if a.isShuttingDown() {
		log.DebugContext(
			ctx,
			"Rejected ACP session because the connection is shutting down",
			"cwd",
			request.Cwd,
		)
		return acpsdk.NewSessionResponse{}, protocolShuttingDownError()
	}
	tools, toolSets, err := initializeMCPServers(ctx, request.McpServers)
	if err != nil {
		log.DebugContext(
			ctx,
			"Failed to initialize ACP session tools",
			"cwd",
			request.Cwd,
			"mcp_server_count",
			len(request.McpServers),
			"error",
			err,
		)
		return acpsdk.NewSessionResponse{}, err
	}
	sessionID, err := a.server.reserveSessionID()
	if err != nil {
		log.ErrorContext(ctx, "Failed to allocate ACP session ID", "error", err)
		closeMCPServers(ctx, toolSets)
		return acpsdk.NewSessionResponse{}, err
	}
	session := &sessionState{
		id:       sessionID,
		cwd:      request.Cwd,
		tools:    tools,
		toolSets: toolSets,
	}
	session.toolCallIDs = newToolCallIDMapper(session.allocateToolCallID)
	session.tools = append(session.tools, a.newClientFileTools(session)...)
	a.mu.Lock()
	if a.shuttingDown {
		a.mu.Unlock()
		closeMCPServers(ctx, toolSets)
		a.server.releaseSessionID(sessionID)
		log.DebugContext(
			ctx,
			"Rejected ACP session because the connection is shutting down",
			"session_id",
			sessionID,
		)
		return acpsdk.NewSessionResponse{}, protocolShuttingDownError()
	}
	a.sessions[sessionID] = session
	a.mu.Unlock()
	log.DebugContext(
		ctx,
		"ACP session created",
		"session_id",
		sessionID,
		"cwd",
		session.cwd,
		"tool_count",
		len(session.tools),
		"mcp_server_count",
		len(session.toolSets),
	)
	return acpsdk.NewSessionResponse{SessionId: acpsdk.SessionId(sessionID)}, nil
}

func (a *protocolAgent) isShuttingDown() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.shuttingDown
}

func protocolShuttingDownError() error {
	return acpsdk.NewInternalError(map[string]any{
		"reason": "ACP server is shutting down",
	})
}

func (s *Server) reserveSessionID() (string, error) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	sessionID := s.sessionIDGenerator()
	if sessionID == "" {
		return "", errors.New("acp: generated empty session ID")
	}
	if _, exists := s.activeSessionIDs[sessionID]; exists {
		return "", fmt.Errorf("acp: generated duplicate session ID %q", sessionID)
	}
	s.activeSessionIDs[sessionID] = struct{}{}
	return sessionID, nil
}

func (s *Server) releaseSessionID(sessionID string) {
	s.sessionMu.Lock()
	delete(s.activeSessionIDs, sessionID)
	s.sessionMu.Unlock()
}

func (a *protocolAgent) Prompt(
	ctx context.Context,
	request acpsdk.PromptRequest,
) (response acpsdk.PromptResponse, err error) {
	session, err := a.getSession(string(request.SessionId))
	if err != nil {
		log.DebugContext(
			ctx,
			"Rejected ACP prompt for unknown session",
			"session_id",
			request.SessionId,
		)
		return acpsdk.PromptResponse{}, err
	}
	message, err := promptToMessage(request.Prompt)
	if err != nil {
		log.DebugContext(
			ctx,
			"Rejected ACP prompt with invalid content",
			"session_id",
			session.id,
			"content_block_count",
			len(request.Prompt),
			"error",
			err,
		)
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(
			map[string]any{"prompt": err.Error()},
		)
	}
	requestID := uuid.NewString()
	runCtx, cancel := context.WithCancel(ctx)
	toolCallIDs := newToolCallIDMapper(session.allocateToolCallID)
	if err := a.beginPrompt(session, requestID, cancel, toolCallIDs); err != nil {
		cancel()
		log.DebugContext(
			ctx,
			"Rejected ACP prompt",
			"session_id",
			session.id,
			"request_id",
			requestID,
			"error",
			err,
		)
		return acpsdk.PromptResponse{}, err
	}
	log.DebugContext(
		ctx,
		"ACP prompt started",
		"session_id",
		session.id,
		"request_id",
		requestID,
		"cwd",
		session.cwd,
		"content_block_count",
		len(request.Prompt),
	)
	defer cancel()
	defer func() {
		if err != nil {
			log.ErrorContext(
				ctx,
				"ACP prompt failed",
				"session_id",
				session.id,
				"request_id",
				requestID,
				"error",
				err,
			)
		} else {
			log.DebugContext(
				ctx,
				"ACP prompt completed",
				"session_id",
				session.id,
				"request_id",
				requestID,
				"stop_reason",
				response.StopReason,
			)
		}
		a.finishPrompt(ctx, session, requestID)
	}()

	runOptions := append([]agent.RunOption(nil), a.server.runOptions...)
	if a.server.runOptionsFunc != nil {
		runOptions = append(runOptions, a.server.runOptionsFunc(Session{
			ID:  session.id,
			CWD: session.cwd,
		})...)
	}
	runOptions = append(runOptions, agent.WithAdditionalTools(session.tools))
	runOptions = append(runOptions, agent.WithRequestID(requestID))
	events, err := a.server.runner.Run(
		runCtx,
		a.server.userID,
		session.id,
		message,
		runOptions...,
	)
	if err != nil {
		if runCtx.Err() != nil {
			a.stopPrompt(requestID, cancel)
			return canceledPromptResponse(), nil
		}
		return acpsdk.PromptResponse{}, err
	}

	turn := newTurnState(a.server.reasoningEnabled, toolCallIDs)
	for {
		select {
		case <-runCtx.Done():
			a.stopPrompt(requestID, cancel)
			drainEvents(events)
			return canceledPromptResponse(), nil
		case event, ok := <-events:
			if !ok {
				if runCtx.Err() != nil {
					return canceledPromptResponse(), nil
				}
				return turn.response(), nil
			}
			updates, err := turn.translate(runCtx, event)
			if err != nil {
				a.stopPrompt(requestID, cancel)
				drainEvents(events)
				return acpsdk.PromptResponse{}, err
			}
			for _, update := range updates {
				if err := a.connection.SessionUpdate(runCtx, acpsdk.SessionNotification{
					SessionId: request.SessionId,
					Update:    update,
				}); err != nil {
					if runCtx.Err() != nil {
						a.stopPrompt(requestID, cancel)
						drainEvents(events)
						return canceledPromptResponse(), nil
					}
					a.stopPrompt(requestID, cancel)
					drainEvents(events)
					return acpsdk.PromptResponse{}, err
				}
			}
		}
	}
}

func drainEvents(events <-chan *event.Event) {
	if events == nil {
		return
	}
	for range events {
	}
}

func canceledPromptResponse() acpsdk.PromptResponse {
	return acpsdk.PromptResponse{
		StopReason: acpsdk.StopReasonCancelled,
	}
}

func (a *protocolAgent) Cancel(
	ctx context.Context,
	request acpsdk.CancelNotification,
) error {
	session, err := a.getSession(string(request.SessionId))
	if err != nil {
		log.DebugContext(
			ctx,
			"Ignored ACP cancel for unknown session",
			"session_id",
			request.SessionId,
		)
		return nil
	}
	a.cancelPrompt(ctx, session)
	return nil
}

func (a *protocolAgent) CloseSession(
	ctx context.Context,
	request acpsdk.CloseSessionRequest,
) (acpsdk.CloseSessionResponse, error) {
	sessionID := string(request.SessionId)
	log.DebugContext(ctx, "Received ACP session close request", "session_id", sessionID)
	a.mu.Lock()
	session, ok := a.sessions[sessionID]
	if ok {
		delete(a.sessions, sessionID)
	}
	a.mu.Unlock()
	if !ok {
		log.DebugContext(ctx, "ACP session already closed", "session_id", sessionID)
		return acpsdk.CloseSessionResponse{}, nil
	}
	a.closeSession(session)
	a.server.releaseSessionID(sessionID)
	log.DebugContext(ctx, "ACP session closed", "session_id", sessionID)
	return acpsdk.CloseSessionResponse{}, nil
}

func (a *protocolAgent) getSession(sessionID string) (*sessionState, error) {
	a.mu.Lock()
	session := a.sessions[sessionID]
	a.mu.Unlock()
	if session == nil {
		return nil, acpsdk.NewInvalidParams(
			map[string]any{"sessionId": "unknown session"},
		)
	}
	return session, nil
}

func (a *protocolAgent) beginPrompt(
	session *sessionState,
	requestID string,
	cancel context.CancelFunc,
	toolCallIDs *toolCallIDMapper,
) error {
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return acpsdk.NewInvalidParams(
			map[string]any{"sessionId": "session is closed"},
		)
	}
	previousCancel := session.cancel
	previousRequestID := session.requestID
	session.cancel = cancel
	session.requestID = requestID
	session.promptDone = make(chan struct{})
	session.toolCallIDs = toolCallIDs
	session.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	if previousRequestID != "" && a.server.managedRunner != nil {
		a.server.managedRunner.Cancel(previousRequestID)
	}
	if previousRequestID != "" {
		log.DebugContext(
			context.Background(),
			"Replaced active ACP prompt",
			"session_id",
			session.id,
			"previous_request_id",
			previousRequestID,
			"request_id",
			requestID,
		)
	}
	return nil
}

func (a *protocolAgent) finishPrompt(
	ctx context.Context,
	session *sessionState,
	requestID string,
) {
	session.mu.Lock()
	if session.requestID == requestID {
		session.cancel = nil
		session.requestID = ""
		session.toolCallIDs = nil
		if session.promptDone != nil {
			close(session.promptDone)
			session.promptDone = nil
		}
	}
	session.mu.Unlock()
	log.DebugContext(
		ctx,
		"ACP prompt lifecycle finished",
		"session_id",
		session.id,
		"request_id",
		requestID,
	)
}

func (s *sessionState) allocateToolCallID() acpsdk.ToolCallId {
	return acpsdk.ToolCallId(fmt.Sprintf(
		"tool-%d",
		s.nextToolCallID.Add(1),
	))
}

func (s *sessionState) currentToolCallIDMapper() *toolCallIDMapper {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolCallIDs
}

func (a *protocolAgent) cancelPrompt(
	ctx context.Context,
	session *sessionState,
) {
	session.mu.Lock()
	cancel := session.cancel
	requestID := session.requestID
	session.mu.Unlock()
	log.DebugContext(
		ctx,
		"Canceling ACP prompt",
		"session_id",
		session.id,
		"request_id",
		requestID,
	)
	if cancel != nil {
		cancel()
	}
	if requestID != "" && a.server.managedRunner != nil {
		a.server.managedRunner.Cancel(requestID)
	}
}

func (a *protocolAgent) stopPrompt(
	requestID string,
	cancel context.CancelFunc,
) {
	cancel()
	if a.server.managedRunner != nil {
		a.server.managedRunner.Cancel(requestID)
	}
}

func (a *protocolAgent) closeSession(session *sessionState) {
	a.closeSessionWithTimeout(session, defaultShutdownTimeout)
}

func (a *protocolAgent) closeSessionWithTimeout(
	session *sessionState,
	timeout time.Duration,
) {
	promptDone := a.stopSession(session)
	waitForPromptAndCloseResources(session, promptDone, timeout)
}

func (a *protocolAgent) stopSession(session *sessionState) <-chan struct{} {
	session.mu.Lock()
	session.closed = true
	cancel := session.cancel
	requestID := session.requestID
	promptDone := session.promptDone
	session.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if requestID != "" && a.server.managedRunner != nil {
		a.server.managedRunner.Cancel(requestID)
	}
	return promptDone
}

func waitForPromptAndCloseResources(
	session *sessionState,
	promptDone <-chan struct{},
	timeout time.Duration,
) {
	if promptDone != nil {
		timer := time.NewTimer(timeout)
		select {
		case <-promptDone:
			timer.Stop()
		case <-timer.C:
			log.WarnContext(
				context.Background(),
				"Timed out waiting for ACP prompt shutdown; forcing MCP close",
				"session_id",
				session.id,
				"timeout",
				timeout,
			)
		}
	}
	closeMCPServers(context.Background(), session.toolSets)
	log.DebugContext(
		context.Background(),
		"ACP session resources closed",
		"session_id",
		session.id,
		"mcp_server_count",
		len(session.toolSets),
	)
}

func (a *protocolAgent) shutdown() {
	a.shutdownWithTimeout(defaultShutdownTimeout)
}

func (a *protocolAgent) shutdownWithTimeout(timeout time.Duration) {
	a.mu.Lock()
	a.shuttingDown = true
	sessions := make([]*sessionState, 0, len(a.sessions))
	for _, session := range a.sessions {
		sessions = append(sessions, session)
	}
	a.sessions = make(map[string]*sessionState)
	a.mu.Unlock()
	log.DebugContext(
		context.Background(),
		"Shutting down ACP connection",
		"session_count",
		len(sessions),
		"timeout",
		timeout,
	)
	promptDone := make([]<-chan struct{}, len(sessions))
	for index, session := range sessions {
		promptDone[index] = a.stopSession(session)
	}
	var waitGroup sync.WaitGroup
	for index, session := range sessions {
		waitGroup.Add(1)
		go func(session *sessionState, promptDone <-chan struct{}) {
			defer waitGroup.Done()
			defer a.server.releaseSessionID(session.id)
			waitForPromptAndCloseResources(
				session,
				promptDone,
				timeout,
			)
		}(session, promptDone[index])
	}
	waitGroup.Wait()
	log.DebugContext(context.Background(), "ACP connection shutdown completed")
}

func (*protocolAgent) Authenticate(
	context.Context,
	acpsdk.AuthenticateRequest,
) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, acpsdk.NewMethodNotFound(
		acpsdk.AgentMethodAuthenticate,
	)
}

func (*protocolAgent) Logout(
	context.Context,
	acpsdk.LogoutRequest,
) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, acpsdk.NewMethodNotFound(
		acpsdk.AgentMethodLogout,
	)
}

func (*protocolAgent) ListSessions(
	context.Context,
	acpsdk.ListSessionsRequest,
) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, acpsdk.NewMethodNotFound(
		acpsdk.AgentMethodSessionList,
	)
}

func (*protocolAgent) DeleteSession(
	context.Context,
	acpsdk.DeleteSessionRequest,
) (acpsdk.DeleteSessionResponse, error) {
	return acpsdk.DeleteSessionResponse{}, acpsdk.NewMethodNotFound(
		acpsdk.AgentMethodSessionDelete,
	)
}

func (*protocolAgent) ResumeSession(
	context.Context,
	acpsdk.ResumeSessionRequest,
) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, acpsdk.NewMethodNotFound(
		acpsdk.AgentMethodSessionResume,
	)
}

func (*protocolAgent) SetSessionConfigOption(
	context.Context,
	acpsdk.SetSessionConfigOptionRequest,
) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, acpsdk.NewMethodNotFound(
		acpsdk.AgentMethodSessionSetConfigOption,
	)
}

func (*protocolAgent) SetSessionMode(
	context.Context,
	acpsdk.SetSessionModeRequest,
) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, acpsdk.NewMethodNotFound(
		acpsdk.AgentMethodSessionSetMode,
	)
}

var _ acpsdk.Agent = (*protocolAgent)(nil)
