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

package a2aagent

import (
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/server"
)

// Option configures the A2AAgent
type Option func(*A2AAgent)

// WithName sets the name of agent
func WithName(name string) Option {
	return func(a *A2AAgent) {
		a.name = name
	}
}

// WithDescription sets the agent description
func WithDescription(description string) Option {
	return func(a *A2AAgent) {
		a.description = description
	}
}

// WithTimeout sets the HTTP timeout
func WithTimeout(timeout time.Duration) Option {
	return func(a *A2AAgent) {
		a.timeout = timeout
	}
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(client *http.Client) Option {
	return func(a *A2AAgent) {
		a.httpClient = client
	}
}

// WithAgentCardURL set the agent card URL
func WithAgentCardURL(url string) Option {
	return func(a *A2AAgent) {
		a.agentURL = strings.TrimSpace(url)
	}
}

// WithAgentCard set the agent card
func WithAgentCard(agentCard *server.AgentCard) Option {
	return func(a *A2AAgent) {
		a.agentCard = agentCard
	}
}
