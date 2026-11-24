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
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var errRunNotImplemented = errors.New("acpagent: run is not implemented")

// ACPAgent adapts an external ACP agent process to agent.Agent.
type ACPAgent struct {
	config              ACPConfig
	terminalAuthHandler TerminalAuthHandler
	activeTurns         *activeTurnRegistry
	connections         *connectionManager
	closeOnce           sync.Once
	closeErr            error
}

var _ agent.Agent = (*ACPAgent)(nil)

// New creates an ACP agent adapter without starting the external process.
func New(config ACPConfig, opts ...Option) (*ACPAgent, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	options := options{stderr: discardWriter{}}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if options.stderr == nil {
		options.stderr = discardWriter{}
	}
	config = config.clone()
	activeTurns := newActiveTurnRegistry()
	return &ACPAgent{
		config:              config,
		terminalAuthHandler: options.terminalAuthHandler,
		activeTurns:         activeTurns,
		connections: newConnectionManager(
			config,
			options.stderr,
			activeTurns,
			options.terminalAuthHandler != nil,
		),
	}, nil
}

// Run implements agent.Agent.
func (a *ACPAgent) Run(_ context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("acpagent: invocation is nil")
	}
	return nil, errRunNotImplemented
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

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
