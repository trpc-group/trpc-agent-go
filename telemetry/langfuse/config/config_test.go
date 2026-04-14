//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFromEnv(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	t.Setenv("LANGFUSE_BASE_URL", "https://cloud.langfuse.com")

	cfg := FromEnv()

	assert.Equal(t, "pk-test", cfg.PublicKey)
	assert.Equal(t, "sk-test", cfg.SecretKey)
	assert.Equal(t, "https://cloud.langfuse.com", cfg.BaseURL)
}

func TestFromEnv_Defaults(t *testing.T) {
	cfg := FromEnv()

	assert.Empty(t, cfg.PublicKey)
	assert.Empty(t, cfg.SecretKey)
	assert.Empty(t, cfg.BaseURL)
}

func TestFromEnv_FallbackToHost(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pk-test")
	t.Setenv("LANGFUSE_SECRET_KEY", "sk-test")
	t.Setenv("LANGFUSE_HOST", "cloud.langfuse.com")

	cfg := FromEnv()

	assert.Equal(t, "pk-test", cfg.PublicKey)
	assert.Equal(t, "sk-test", cfg.SecretKey)
	assert.Equal(t, "https://cloud.langfuse.com", cfg.BaseURL)
}

func TestFromEnv_FallbackToLocalHost(t *testing.T) {
	t.Setenv("LANGFUSE_HOST", "localhost:3000")

	cfg := FromEnv()

	assert.Equal(t, "http://localhost:3000", cfg.BaseURL)
}
