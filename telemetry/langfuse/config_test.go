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

func TestWithObservationLeafValueMaxBytes_SetsPointer(t *testing.T) {
	cfg := &config{}
	WithObservationLeafValueMaxBytes(123)(cfg)
	if assert.NotNil(t, cfg.maxObservationLeafValueBytes) {
		assert.Equal(t, 123, *cfg.maxObservationLeafValueBytes)
	}
}

func TestWithObservationLeafValueMaxBytes_ZeroMeansTruncateAll(t *testing.T) {
	cfg := &config{}
	WithObservationLeafValueMaxBytes(0)(cfg)
	if assert.NotNil(t, cfg.maxObservationLeafValueBytes) {
		assert.Equal(t, 0, *cfg.maxObservationLeafValueBytes)
	}
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
			os.Unsetenv("LANGFUSE_INSECURE")
			os.Unsetenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES")

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

func TestNewConfigFromEnv_MaxObservationLeafValueBytes(t *testing.T) {
	// Ensure env is clean
	os.Unsetenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES")
	cfg := newConfigFromEnv()
	assert.Nil(t, cfg.maxObservationLeafValueBytes)

	t.Run("invalid is ignored", func(t *testing.T) {
		os.Setenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES", "nope")
		defer os.Unsetenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES")
		cfg := newConfigFromEnv()
		assert.Nil(t, cfg.maxObservationLeafValueBytes)
	})

	t.Run("zero means truncate all", func(t *testing.T) {
		os.Setenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES", "0")
		defer os.Unsetenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES")
		cfg := newConfigFromEnv()
		if assert.NotNil(t, cfg.maxObservationLeafValueBytes) {
			assert.Equal(t, 0, *cfg.maxObservationLeafValueBytes)
		}
	})

	t.Run("positive value", func(t *testing.T) {
		os.Setenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES", "123")
		defer os.Unsetenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES")
		cfg := newConfigFromEnv()
		if assert.NotNil(t, cfg.maxObservationLeafValueBytes) {
			assert.Equal(t, 123, *cfg.maxObservationLeafValueBytes)
		}
	})

	t.Run("negative disables truncation", func(t *testing.T) {
		os.Setenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES", "-1")
		defer os.Unsetenv("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES")
		cfg := newConfigFromEnv()
		if assert.NotNil(t, cfg.maxObservationLeafValueBytes) {
			assert.Equal(t, -1, *cfg.maxObservationLeafValueBytes)
		}
	})
}

func TestGetEnvIntPtr(t *testing.T) {
	key := "TEST_INT_PTR"

	// not set => nil
	_ = os.Unsetenv(key)
	assert.Nil(t, getEnvIntPtr(key))

	// invalid => nil
	_ = os.Setenv(key, "abc")
	assert.Nil(t, getEnvIntPtr(key))
	_ = os.Unsetenv(key)

	// valid => pointer value
	_ = os.Setenv(key, "42")
	p := getEnvIntPtr(key)
	if assert.NotNil(t, p) {
		assert.Equal(t, 42, *p)
	}
	_ = os.Unsetenv(key)
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
