//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package difyagent

import (
	"context"
	"fmt"
	"testing"

	"github.com/cloudernative/dify-sdk-go"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestNew(t *testing.T) {
	t.Run("success with direct agent card", func(t *testing.T) {
		agent, err := New(
			WithName("direct-agent"),
			WithDescription("Direct agent card"),
		)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if agent == nil {
			t.Fatal("expected agent, got nil")
		}
		if agent.name != "direct-agent" {
			t.Errorf("expected name 'direct-agent', got %s", agent.name)
		}
		if agent.description != "Direct agent card" {
			t.Errorf("expected description 'Direct agent card', got %s", agent.description)
		}
	})

	t.Run("error when no agent card", func(t *testing.T) {
		agent, err := New()
		if err == nil {
			t.Error("expected error when no agent card is set")
		}
		if agent != nil {
			t.Error("expected agent to be nil on error")
		}
	})
}

func TestDifyAgent_Info(t *testing.T) {
	agent := &DifyAgent{
		name:        "test-agent",
		description: "test description",
	}

	info := agent.Info()
	if info.Name != "test-agent" {
		t.Errorf("expected name 'test-agent', got '%s'", info.Name)
	}
	if info.Description != "test description" {
		t.Errorf("expected description 'test description', got '%s'", info.Description)
	}
}

func TestDifyAgent_Tools(t *testing.T) {
	agent := &DifyAgent{}
	tools := agent.Tools()
	if len(tools) != 0 {
		t.Errorf("expected 0 tools, got %d", len(tools))
	}
}

func TestDifyAgent_SubAgents(t *testing.T) {
	agent := &DifyAgent{}

	subAgents := agent.SubAgents()
	if len(subAgents) != 0 {
		t.Errorf("expected 0 sub agents, got %d", len(subAgents))
	}

	foundAgent := agent.FindSubAgent("any-name")
	if foundAgent != nil {
		t.Error("expected nil agent")
	}
}

func TestDifyAgent_Streaming(t *testing.T) {
	t.Run("shouldUseStreaming with explicit true", func(t *testing.T) {
		enableStreaming := true
		agent := &DifyAgent{enableStreaming: &enableStreaming}
		if !agent.shouldUseStreaming() {
			t.Error("should use streaming when explicitly enabled")
		}
	})

	t.Run("shouldUseStreaming with explicit false", func(t *testing.T) {
		enableStreaming := false
		agent := &DifyAgent{enableStreaming: &enableStreaming}
		if agent.shouldUseStreaming() {
			t.Error("should not use streaming when explicitly disabled")
		}
	})

	t.Run("shouldUseStreaming with nil defaults to false", func(t *testing.T) {
		agent := &DifyAgent{enableStreaming: nil}
		if agent.shouldUseStreaming() {
			t.Error("should default to non-streaming")
		}
	})
}

func TestDifyAgent_GetClient(t *testing.T) {
	t.Run("uses custom client function", func(t *testing.T) {
		expectedClient := &dify.Client{}
		difyAgent := &DifyAgent{
			getDifyClientFunc: func(*agent.Invocation) (*dify.Client, error) {
				return expectedClient, nil
			},
		}

		invocation := &agent.Invocation{}
		client, err := difyAgent.getDifyClient(invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if client != expectedClient {
			t.Error("should return client from custom function")
		}
	})

	t.Run("creates default client", func(t *testing.T) {
		difyAgent := &DifyAgent{
			baseUrl:   "http://test.com",
			apiSecret: "test-secret",
		}

		invocation := &agent.Invocation{}
		client, err := difyAgent.getDifyClient(invocation)
		if err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
		if client == nil {
			t.Error("should return a client")
		}
	})

	t.Run("custom function returns error", func(t *testing.T) {
		expectedErr := fmt.Errorf("custom client error")
		difyAgent := &DifyAgent{
			getDifyClientFunc: func(*agent.Invocation) (*dify.Client, error) {
				return nil, expectedErr
			},
		}

		invocation := &agent.Invocation{}
		client, err := difyAgent.getDifyClient(invocation)
		if err != expectedErr {
			t.Errorf("expected custom error, got: %v", err)
		}
		if client != nil {
			t.Error("should not return client on error")
		}
	})
}

func TestDifyAgent_RequestConverter(t *testing.T) {
	t.Run("buildDifyRequest with nil converter returns error", func(t *testing.T) {
		difyAgent := &DifyAgent{
			requestConverter: nil,
		}

		invocation := &agent.Invocation{}
		req, err := difyAgent.buildDifyRequest(context.Background(), invocation, false)
		if err == nil {
			t.Error("expected error when request converter is nil")
		}
		if req != nil {
			t.Error("expected nil request when converter is nil")
		}
	})
}

// Helper function to create bool pointer
func boolPtr(b bool) *bool {
	return &b
}

func TestDifyAgentOptions(t *testing.T) {
	t.Run("WithStreamingChannelBufSize", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithStreamingChannelBufSize(2048)(difyAgent)
		if difyAgent.streamingBufSize != 2048 {
			t.Errorf("expected streamingBufSize 2048, got %d", difyAgent.streamingBufSize)
		}
	})

	t.Run("WithEnableStreaming true", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithEnableStreaming(true)(difyAgent)
		if difyAgent.enableStreaming == nil || !*difyAgent.enableStreaming {
			t.Error("enableStreaming should be true")
		}
	})

	t.Run("WithEnableStreaming false", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithEnableStreaming(false)(difyAgent)
		if difyAgent.enableStreaming == nil || *difyAgent.enableStreaming {
			t.Error("enableStreaming should be false")
		}
	})

	t.Run("WithTransferStateKey", func(t *testing.T) {
		difyAgent := &DifyAgent{}
		WithTransferStateKey("key1", "key2")(difyAgent)
		if len(difyAgent.transferStateKey) != 2 {
			t.Errorf("expected 2 transfer keys, got %d", len(difyAgent.transferStateKey))
		}
		if difyAgent.transferStateKey[0] != "key1" || difyAgent.transferStateKey[1] != "key2" {
			t.Error("transfer keys not set correctly")
		}
	})
}
