//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ACPAgent adapts an external ACP agent process to agent.Agent.
type ACPAgent struct {
	config              processConfig
	authMethodSelector  AuthMethodSelector
	terminalAuthHandler TerminalAuthHandler
	activeTurns         *activeTurnRegistry
	sessions            *acpSessionStore
	launchFingerprint   string
	connections         *connectionManager
	closeOnce           sync.Once
	closeErr            error
}

var _ agent.Agent = (*ACPAgent)(nil)

// New creates an ACP agent adapter without starting the external process.
func New(name string, opts ...Option) (*ACPAgent, error) {
	options := defaultOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	config := processConfig{
		Name:        name,
		Description: options.description,
		Command:     options.command,
		Args:        options.args,
		Env:         options.env,
		WorkDir:     options.workDir,
		IdleTimeout: options.idleTimeout,
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	config = config.clone()
	launchFingerprint, err := computeLaunchFingerprint(config)
	if err != nil {
		return nil, fmt.Errorf("acpagent: compute launch fingerprint: %w", err)
	}
	activeTurns := newActiveTurnRegistry()
	return &ACPAgent{
		config:              config,
		authMethodSelector:  options.authMethodSelector,
		terminalAuthHandler: options.terminalAuthHandler,
		activeTurns:         activeTurns,
		sessions:            newACPSessionStore(),
		launchFingerprint:   launchFingerprint,
		connections: newConnectionManager(
			config,
			options.stderr,
			activeTurns,
			options.authMethodSelector,
			options.terminalAuthHandler != nil,
		),
	}, nil
}

// Run implements agent.Agent.
func (a *ACPAgent) Run(
	ctx context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("acpagent: invocation is nil")
	}
	content, err := promptContentFromMessage(invocation.Message)
	if err != nil {
		return nil, err
	}
	releaseActivity := a.connections.beginActivity()
	session, err := a.acquireSession(ctx, invocation)
	if err != nil {
		releaseActivity()
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx := agent.CloneContext(ctx)
	out := make(chan *event.Event)
	go func() {
		defer releaseActivity()
		a.runPrompt(runCtx, invocation, session, content, out)
	}()
	return out, nil
}

// Tools implements agent.Agent.
func (*ACPAgent) Tools() []tool.Tool {
	return nil
}

// Info implements agent.Agent.
func (a *ACPAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.config.Name,
		Description: a.config.Description,
	}
}

// SubAgents implements agent.Agent.
func (*ACPAgent) SubAgents() []agent.Agent {
	return nil
}

// FindSubAgent implements agent.Agent.
func (*ACPAgent) FindSubAgent(string) agent.Agent {
	return nil
}

// Close stops the shared ACP process.
//
// Close is safe to call repeatedly and from multiple goroutines.
func (a *ACPAgent) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		a.closeErr = a.connections.close()
	})
	return a.closeErr
}
