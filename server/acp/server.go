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

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	defaultImplementationName    = "trpc-agent-go"
	defaultImplementationVersion = "dev"
	defaultUserID                = "acp"
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
	userID             string
	implementationName string
	implementationVer  string
	sessionIDGenerator func() string
	runOptions         []agent.RunOption
	runOptionsFunc     RunOptionsFunc
	reasoningEnabled   bool
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

// WithSessionIDGenerator replaces the default UUID session ID generator.
func WithSessionIDGenerator(generator func() string) Option {
	return func(options *options) {
		options.sessionIDGenerator = generator
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

// Server adapts a runner to ACP connections.
type Server struct {
	runner             runner.Runner
	managedRunner      runner.ManagedRunner
	userID             string
	implementation     acpsdk.Implementation
	sessionIDGenerator func() string
	runOptions         []agent.RunOption
	runOptionsFunc     RunOptionsFunc
	reasoningEnabled   bool
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
		runner:             r,
		userID:             options.userID,
		implementation:     acpsdk.Implementation{Name: options.implementationName, Version: options.implementationVer},
		sessionIDGenerator: options.sessionIDGenerator,
		runOptions:         append([]agent.RunOption(nil), options.runOptions...),
		runOptionsFunc:     options.runOptionsFunc,
		reasoningEnabled:   options.reasoningEnabled,
	}
	server.managedRunner, _ = r.(runner.ManagedRunner)
	return server, nil
}

// Connection is an active ACP connection.
type Connection struct {
	connection *acpsdk.AgentSideConnection
	agent      *protocolAgent
}

// Done is closed when the ACP peer disconnects.
func (c *Connection) Done() <-chan struct{} {
	return c.connection.Done()
}

// Connect starts serving one ACP connection over line-delimited JSON-RPC.
// Input is read from the ACP client and output is written back to it.
func (s *Server) Connect(input io.Reader, output io.Writer) (*Connection, error) {
	if input == nil {
		return nil, errors.New("acp: input is required")
	}
	if output == nil {
		return nil, errors.New("acp: output is required")
	}
	protocolAgent := newProtocolAgent(s)
	connection := acpsdk.NewAgentSideConnection(protocolAgent, output, input)
	protocolAgent.connection = connection
	go func() {
		<-connection.Done()
		protocolAgent.cancelAll()
	}()
	return &Connection{connection: connection, agent: protocolAgent}, nil
}

// Serve connects an ACP client and blocks until the peer disconnects or ctx is
// canceled. Callers remain responsible for closing their transport.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	connection, err := s.Connect(input, output)
	if err != nil {
		return err
	}
	select {
	case <-connection.Done():
		return nil
	case <-ctx.Done():
		connection.agent.cancelAll()
		return ctx.Err()
	}
}

type protocolAgent struct {
	server     *Server
	connection *acpsdk.AgentSideConnection
	sessions   map[string]*sessionState
	mu         sync.Mutex
}

type sessionState struct {
	id        string
	cwd       string
	cancel    context.CancelFunc
	requestID string
	closed    bool
	mu        sync.Mutex
}

func newProtocolAgent(server *Server) *protocolAgent {
	return &protocolAgent{
		server:   server,
		sessions: make(map[string]*sessionState),
	}
}

func (a *protocolAgent) Initialize(
	_ context.Context,
	request acpsdk.InitializeRequest,
) (acpsdk.InitializeResponse, error) {
	protocolVersion := request.ProtocolVersion
	if protocolVersion != acpsdk.ProtocolVersionNumber {
		protocolVersion = acpsdk.ProtocolVersionNumber
	}
	implementation := a.server.implementation
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
	_ context.Context,
	request acpsdk.NewSessionRequest,
) (acpsdk.NewSessionResponse, error) {
	if !filepath.IsAbs(request.Cwd) {
		return acpsdk.NewSessionResponse{}, acpsdk.NewInvalidParams(
			map[string]any{"cwd": "must be an absolute path"},
		)
	}
	if len(request.McpServers) != 0 {
		return acpsdk.NewSessionResponse{}, acpsdk.NewInvalidParams(
			map[string]any{"mcpServers": "dynamic MCP servers are not supported"},
		)
	}
	if len(request.AdditionalDirectories) != 0 {
		return acpsdk.NewSessionResponse{}, acpsdk.NewInvalidParams(
			map[string]any{"additionalDirectories": "additional directories are not supported"},
		)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	sessionID := a.server.sessionIDGenerator()
	if sessionID == "" {
		return acpsdk.NewSessionResponse{}, errors.New("acp: generated empty session ID")
	}
	if _, exists := a.sessions[sessionID]; exists {
		return acpsdk.NewSessionResponse{}, fmt.Errorf(
			"acp: generated duplicate session ID %q",
			sessionID,
		)
	}
	a.sessions[sessionID] = &sessionState{id: sessionID, cwd: request.Cwd}
	return acpsdk.NewSessionResponse{SessionId: acpsdk.SessionId(sessionID)}, nil
}

func (a *protocolAgent) Prompt(
	ctx context.Context,
	request acpsdk.PromptRequest,
) (acpsdk.PromptResponse, error) {
	session, err := a.getSession(string(request.SessionId))
	if err != nil {
		return acpsdk.PromptResponse{}, err
	}
	message, err := promptToMessage(request.Prompt)
	if err != nil {
		return acpsdk.PromptResponse{}, acpsdk.NewInvalidParams(
			map[string]any{"prompt": err.Error()},
		)
	}
	requestID := uuid.NewString()
	runCtx, cancel := context.WithCancel(ctx)
	if err := a.beginPrompt(session, requestID, cancel); err != nil {
		cancel()
		return acpsdk.PromptResponse{}, err
	}
	defer cancel()
	defer a.finishPrompt(session, requestID)

	runOptions := append([]agent.RunOption(nil), a.server.runOptions...)
	if a.server.runOptionsFunc != nil {
		runOptions = append(runOptions, a.server.runOptionsFunc(Session{
			ID:  session.id,
			CWD: session.cwd,
		})...)
	}
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
			return canceledPromptResponse(request.MessageId), nil
		}
		return acpsdk.PromptResponse{}, err
	}

	turn := newTurnState(a.server.reasoningEnabled)
	for {
		select {
		case <-runCtx.Done():
			return canceledPromptResponse(request.MessageId), nil
		case event, ok := <-events:
			if !ok {
				if runCtx.Err() != nil {
					return canceledPromptResponse(request.MessageId), nil
				}
				return turn.response(request.MessageId), nil
			}
			updates, err := turn.translate(event)
			if err != nil {
				return acpsdk.PromptResponse{}, err
			}
			for _, update := range updates {
				if err := a.connection.SessionUpdate(runCtx, acpsdk.SessionNotification{
					SessionId: request.SessionId,
					Update:    update,
				}); err != nil {
					if runCtx.Err() != nil {
						return canceledPromptResponse(request.MessageId), nil
					}
					return acpsdk.PromptResponse{}, err
				}
			}
		}
	}
}

func canceledPromptResponse(messageID *string) acpsdk.PromptResponse {
	return acpsdk.PromptResponse{
		StopReason:    acpsdk.StopReasonCancelled,
		UserMessageId: messageID,
	}
}

func (a *protocolAgent) Cancel(
	_ context.Context,
	request acpsdk.CancelNotification,
) error {
	session, err := a.getSession(string(request.SessionId))
	if err != nil {
		return nil
	}
	a.cancelPrompt(session)
	return nil
}

func (a *protocolAgent) CloseSession(
	_ context.Context,
	request acpsdk.CloseSessionRequest,
) (acpsdk.CloseSessionResponse, error) {
	sessionID := string(request.SessionId)
	a.mu.Lock()
	session, ok := a.sessions[sessionID]
	if ok {
		delete(a.sessions, sessionID)
	}
	a.mu.Unlock()
	if !ok {
		return acpsdk.CloseSessionResponse{}, acpsdk.NewInvalidParams(
			map[string]any{"sessionId": "unknown session"},
		)
	}
	a.closeSession(session)
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
	session.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	if previousRequestID != "" && a.server.managedRunner != nil {
		a.server.managedRunner.Cancel(previousRequestID)
	}
	return nil
}

func (a *protocolAgent) finishPrompt(session *sessionState, requestID string) {
	session.mu.Lock()
	if session.requestID == requestID {
		session.cancel = nil
		session.requestID = ""
	}
	session.mu.Unlock()
}

func (a *protocolAgent) cancelPrompt(session *sessionState) {
	session.mu.Lock()
	cancel := session.cancel
	requestID := session.requestID
	session.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if requestID != "" && a.server.managedRunner != nil {
		a.server.managedRunner.Cancel(requestID)
	}
}

func (a *protocolAgent) closeSession(session *sessionState) {
	session.mu.Lock()
	session.closed = true
	cancel := session.cancel
	requestID := session.requestID
	session.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if requestID != "" && a.server.managedRunner != nil {
		a.server.managedRunner.Cancel(requestID)
	}
}

func (a *protocolAgent) cancelAll() {
	a.mu.Lock()
	sessions := make([]*sessionState, 0, len(a.sessions))
	for _, session := range a.sessions {
		sessions = append(sessions, session)
	}
	a.mu.Unlock()
	for _, session := range sessions {
		a.cancelPrompt(session)
	}
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
