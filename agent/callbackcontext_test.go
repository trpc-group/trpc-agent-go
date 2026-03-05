//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/artifact"
)

func TestNewCallbackContext(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		expectError bool
		errorMsg    string
	}{
		{
			name:        "context without invocation",
			ctx:         context.Background(),
			expectError: true,
			errorMsg:    "invocation not found in context",
		},
		{
			name:        "context with nil invocation",
			ctx:         NewInvocationContext(context.Background(), nil),
			expectError: true,
			errorMsg:    "invocation not found in context",
		},
		{
			name: "context with valid invocation",
			ctx: NewInvocationContext(context.Background(), &Invocation{
				AgentName: "test-agent",
			}),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, err := NewCallbackContext(tt.ctx)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, cc)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, cc)
				assert.Equal(t, tt.ctx, cc.Context)
			}
		})
	}
}

func TestCallbackContext_ArtifactOperations_NoService(t *testing.T) {
	// Test all artifact operations when ArtifactService is nil
	inv := &Invocation{
		AgentName:       "test-agent",
		ArtifactService: nil, // No artifact service
	}
	ctx := NewInvocationContext(context.Background(), inv)
	cc, err := NewCallbackContext(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, cc)

	t.Run("PutArtifact without service", func(t *testing.T) {
		resp, err := cc.PutArtifact(&artifact.PutRequest{
			Name: "test.txt",
			Body: strings.NewReader(""),
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "artifact service is nil")
		assert.Nil(t, resp)
	})

	t.Run("HeadArtifact without service", func(t *testing.T) {
		resp, err := cc.HeadArtifact(&artifact.HeadRequest{
			Name: "test.txt",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "artifact service is nil")
		assert.Nil(t, resp)
	})

	t.Run("OpenArtifact without service", func(t *testing.T) {
		resp, err := cc.OpenArtifact(&artifact.OpenRequest{
			Name: "test.txt",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "artifact service is nil")
		assert.Nil(t, resp)
	})

	t.Run("ListArtifacts without service", func(t *testing.T) {
		resp, err := cc.ListArtifacts(&artifact.ListRequest{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "artifact service is nil")
		assert.Nil(t, resp)
	})

	t.Run("DeleteArtifact without service", func(t *testing.T) {
		resp, err := cc.DeleteArtifact(&artifact.DeleteRequest{
			Name: "test.txt",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "artifact service is nil")
		assert.Nil(t, resp)
	})

	t.Run("ListArtifactVersions without service", func(t *testing.T) {
		resp, err := cc.ListArtifactVersions(&artifact.VersionsRequest{
			Name: "test.txt",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "artifact service is nil")
		assert.Nil(t, resp)
	})
}

func TestCallbackContext_ArtifactOperations_NoSession(t *testing.T) {
	// Test all artifact operations when Session is nil
	mockService := &mockArtifactService{}
	inv := &Invocation{
		AgentName:       "test-agent",
		ArtifactService: mockService,
		Session:         nil, // No session
	}
	ctx := NewInvocationContext(context.Background(), inv)
	cc, err := NewCallbackContext(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, cc)

	t.Run("PutArtifact without session", func(t *testing.T) {
		resp, err := cc.PutArtifact(&artifact.PutRequest{
			Name: "test.txt",
			Body: strings.NewReader(""),
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no session available")
		assert.Nil(t, resp)
	})

	t.Run("HeadArtifact without session", func(t *testing.T) {
		resp, err := cc.HeadArtifact(&artifact.HeadRequest{
			Name: "test.txt",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no session available")
		assert.Nil(t, resp)
	})

	t.Run("OpenArtifact without session", func(t *testing.T) {
		resp, err := cc.OpenArtifact(&artifact.OpenRequest{
			Name: "test.txt",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no session available")
		assert.Nil(t, resp)
	})

	t.Run("ListArtifacts without session", func(t *testing.T) {
		resp, err := cc.ListArtifacts(&artifact.ListRequest{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no session available")
		assert.Nil(t, resp)
	})

	t.Run("DeleteArtifact without session", func(t *testing.T) {
		resp, err := cc.DeleteArtifact(&artifact.DeleteRequest{
			Name: "test.txt",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no session available")
		assert.Nil(t, resp)
	})

	t.Run("ListArtifactVersions without session", func(t *testing.T) {
		resp, err := cc.ListArtifactVersions(&artifact.VersionsRequest{
			Name: "test.txt",
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no session available")
		assert.Nil(t, resp)
	})
}
