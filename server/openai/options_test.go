//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestWithBasePath(t *testing.T) {
	opts := &options{}
	WithBasePath("/api/v1")(opts)
	assert.Equal(t, "/api/v1", opts.basePath)
}

func TestWithPath(t *testing.T) {
	opts := &options{}
	WithPath("/completions")(opts)
	assert.Equal(t, "/completions", opts.path)
}

func TestWithSessionService(t *testing.T) {
	svc := inmemory.NewSessionService()
	opts := &options{}
	WithSessionService(svc)(opts)
	assert.Equal(t, svc, opts.sessionService)
}

func TestWithAgent(t *testing.T) {
	mockAgent := &mockAgent{name: "test-agent"}
	opts := &options{}
	WithAgent(mockAgent)(opts)
	assert.Equal(t, mockAgent, opts.agent)
}

func TestWithRunner(t *testing.T) {
	// Create a proper mock runner that implements the interface
	opts := &options{}
	mockRunner := &testMockRunner{events: make(chan *event.Event)}
	WithRunner(mockRunner)(opts)
	assert.Equal(t, mockRunner, opts.runner)
}

// testMockRunner is a mock runner for testing options.
type testMockRunner struct {
	events chan *event.Event
	err    error
}

func (m *testMockRunner) Run(ctx context.Context, userID, sessionID string, message model.Message, opts ...agent.RunOption) (<-chan *event.Event, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.events, nil
}

func (m *testMockRunner) Close() error {
	if m.events != nil {
		close(m.events)
	}
	return nil
}

func TestWithModelName(t *testing.T) {
	opts := &options{}
	WithModelName("gpt-4")(opts)
	assert.Equal(t, "gpt-4", opts.modelName)
}

func TestWithAppName(t *testing.T) {
	opts := &options{}
	WithAppName("my-app")(opts)
	assert.Equal(t, "my-app", opts.appName)
}

func TestOptions_DefaultValues(t *testing.T) {
	opts := &options{}
	// Apply default options
	WithBasePath(defaultBasePath)(opts)
	WithPath(defaultPath)(opts)
	WithModelName(defaultModelName)(opts)
	WithAppName(defaultAppName)(opts)

	assert.Equal(t, defaultBasePath, opts.basePath)
	assert.Equal(t, defaultPath, opts.path)
	assert.Equal(t, defaultModelName, opts.modelName)
	assert.Equal(t, defaultAppName, opts.appName)
}
