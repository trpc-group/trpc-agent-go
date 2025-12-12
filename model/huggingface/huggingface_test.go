//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package huggingface

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		opts      []Option
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "empty model name",
			modelName: "",
			opts:      []Option{WithAPIKey("test-key")},
			wantErr:   true,
			errMsg:    "model name cannot be empty",
		},
		{
			name:      "missing API key",
			modelName: "test-model",
			opts:      []Option{},
			wantErr:   true,
			errMsg:    "API key is required",
		},
		{
			name:      "valid configuration",
			modelName: "meta-llama/Llama-2-7b-chat-hf",
			opts:      []Option{WithAPIKey("test-key")},
			wantErr:   false,
		},
		{
			name:      "with custom base URL",
			modelName: "test-model",
			opts: []Option{
				WithAPIKey("test-key"),
				WithBaseURL("https://custom.api.com"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(tt.modelName, tt.opts...)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, m)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, m)
				assert.Equal(t, tt.modelName, m.name)
			}
		})
	}
}

func TestModel_Info(t *testing.T) {
	m, err := New("test-model", WithAPIKey("test-key"))
	require.NoError(t, err)

	info := m.Info()
	assert.Equal(t, "test-model", info.Name)
}

func TestWithOptions(t *testing.T) {
	tests := []struct {
		name     string
		opts     []Option
		validate func(t *testing.T, m *Model)
	}{
		{
			name: "WithChannelBufferSize",
			opts: []Option{
				WithAPIKey("test-key"),
				WithChannelBufferSize(512),
			},
			validate: func(t *testing.T, m *Model) {
				assert.Equal(t, 512, m.channelBufferSize)
			},
		},
		{
			name: "WithExtraHeaders",
			opts: []Option{
				WithAPIKey("test-key"),
				WithExtraHeaders(map[string]string{
					"X-Custom-Header": "custom-value",
				}),
			},
			validate: func(t *testing.T, m *Model) {
				assert.Equal(t, "custom-value", m.extraHeaders["X-Custom-Header"])
			},
		},
		{
			name: "WithTRPC",
			opts: []Option{
				WithAPIKey("test-key"),
				WithTRPC("test-service", 5000),
			},
			validate: func(t *testing.T, m *Model) {
				assert.True(t, m.useTRPC)
				assert.Equal(t, "test-service", m.trpcServiceName)
				assert.Equal(t, 5000, m.trpcTimeout)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New("test-model", tt.opts...)
			require.NoError(t, err)
			tt.validate(t, m)
		})
	}
}
