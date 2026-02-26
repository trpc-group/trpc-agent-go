//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

func TestNewMySQLSessionBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newMySQLSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewPostgresSessionBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newPostgresSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewClickHouseSessionBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newClickHouseSessionBackend(
		registry.SessionDeps{},
		registry.SessionBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewMySQLMemoryBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newMySQLMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewPostgresMemoryBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newPostgresMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewPGVectorMemoryBackend_MissingConfigFails(t *testing.T) {
	t.Parallel()

	_, err := newPGVectorMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires dsn or instance")
}

func TestNewPGVectorMemoryBackend_EmbedderTypeFails(t *testing.T) {
	t.Parallel()

	var node yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(`
dsn: "postgres://example"
embedder:
  type: "not-supported"
`), &node))

	_, err := newPGVectorMemoryBackend(
		registry.MemoryDeps{},
		registry.MemoryBackendSpec{
			Config: &node,
		},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported embedder type")
}

func TestNewOpenAIEmbedder_InvalidTypeFails(t *testing.T) {
	t.Parallel()

	_, err := newOpenAIEmbedder(&openAIEmbedderConfig{Type: "bad"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported embedder type")
}

func TestNewOpenAIEmbedder_OpenAITypeSucceeds(t *testing.T) {
	t.Parallel()

	emb, err := newOpenAIEmbedder(&openAIEmbedderConfig{
		Type:       "openai",
		Model:      "text-embedding-3-small",
		Dimensions: 1536,
		BaseURL:    "https://example.invalid",
	})
	require.NoError(t, err)
	require.NotNil(t, emb)
}

func TestSafeOption_RecoversPanic(t *testing.T) {
	t.Parallel()

	_, err := safeOption(func(string) int {
		panic("bad")
	}, "demo")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid value")
}
