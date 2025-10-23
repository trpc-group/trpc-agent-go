//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithInsecure(t *testing.T) {
	cfg := &config{}
	WithInsecure()(cfg)
	assert.True(t, cfg.insecure)
}

func TestNewConfigFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected *config
	}{
		{
			name: "with environment variables",
			envVars: map[string]string{
				"LANGFUSE_SECRET_KEY": "test-secret",
				"LANGFUSE_PUBLIC_KEY": "test-public",
				"LANGFUSE_HOST":       "https://test.langfuse.com",
			},
			expected: &config{
				secretKey: "test-secret",
				publicKey: "test-public",
				host:      "https://test.langfuse.com",
			},
		},
		{
			name:    "without environment variables (defaults)",
			envVars: map[string]string{},
			expected: &config{
				secretKey: "",
				publicKey: "",
				host:      "",
			},
		},
		{
			name: "partial environment variables",
			envVars: map[string]string{
				"LANGFUSE_SECRET_KEY": "custom-secret",
			},
			expected: &config{
				secretKey: "custom-secret",
				publicKey: "",
				host:      "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all relevant environment variables first
			os.Unsetenv("LANGFUSE_SECRET_KEY")
			os.Unsetenv("LANGFUSE_PUBLIC_KEY")
			os.Unsetenv("LANGFUSE_HOST")

			// Set test environment variables
			for key, value := range tt.envVars {
				os.Setenv(key, value)
			}

			// Clean up after test
			defer func() {
				for key := range tt.envVars {
					os.Unsetenv(key)
				}
			}()

			config := newConfigFromEnv()
			assert.Equal(t, tt.expected, config)
		})
	}
}

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		defaultValue string
		envValue     string
		setEnv       bool
		expected     string
	}{
		{
			name:         "environment variable exists",
			key:          "TEST_KEY",
			defaultValue: "default",
			envValue:     "env-value",
			setEnv:       true,
			expected:     "env-value",
		},
		{
			name:         "environment variable does not exist",
			key:          "NON_EXISTENT_KEY",
			defaultValue: "default-value",
			setEnv:       false,
			expected:     "default-value",
		},
		{
			name:         "environment variable is empty string",
			key:          "EMPTY_KEY",
			defaultValue: "default",
			envValue:     "",
			setEnv:       true,
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up first
			os.Unsetenv(tt.key)

			if tt.setEnv {
				os.Setenv(tt.key, tt.envValue)
				defer os.Unsetenv(tt.key)
			}

			result := getEnv(tt.key, tt.defaultValue)
			assert.Equal(t, tt.expected, result)
		})
	}
}
