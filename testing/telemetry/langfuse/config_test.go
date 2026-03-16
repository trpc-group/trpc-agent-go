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
	"path/filepath"
	"testing"

	baselangfuse "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "langfuse.yaml")
	content := []byte(`enabled: true
backend: langfuse
langfuse:
  public_key: pk
  secret_key: sk
  host: localhost:3000
  insecure: true
  observation_leaf_value_max_bytes: 256
  processor: batch
`)
	require.NoError(t, os.WriteFile(path, content, 0o644))

	cfg, err := loadConfigFromFile(path)
	require.NoError(t, err)
	if assert.NotNil(t, cfg.Enabled) {
		assert.True(t, *cfg.Enabled)
	}
	assert.Equal(t, telemetryBackendLangfuse, cfg.Backend)
	assert.Equal(t, "pk", cfg.Langfuse.PublicKey)
	assert.Equal(t, "sk", cfg.Langfuse.SecretKey)
	assert.Equal(t, "localhost:3000", cfg.Langfuse.Host)
	if assert.NotNil(t, cfg.Langfuse.Insecure) {
		assert.True(t, *cfg.Langfuse.Insecure)
	}
	if assert.NotNil(t, cfg.Langfuse.ObservationLeafValueMaxBytes) {
		assert.Equal(t, 256, *cfg.Langfuse.ObservationLeafValueMaxBytes)
	}
	assert.Equal(t, "batch", cfg.Langfuse.Processor)
}

func TestResolveConfigFromEnv_ExplicitDisableSkipsConfigLoad(t *testing.T) {
	t.Setenv(envTelemetryEnabled, "false")
	t.Setenv(envTelemetryConfig, filepath.Join(t.TempDir(), "missing.yaml"))

	_, enabled, err := resolveConfigFromEnv()
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestResolveConfigFromEnv_UsesYAMLWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "langfuse.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`enabled: true
backend: langfuse
langfuse:
  host: localhost:3000
  processor: batch
`), 0o644))
	t.Setenv(envTelemetryConfig, path)

	cfg, enabled, err := resolveConfigFromEnv()
	require.NoError(t, err)
	assert.True(t, enabled)
	assert.Equal(t, "localhost:3000", cfg.Host)
	assert.Equal(t, "batch", cfg.Processor)
}

func TestResolveConfigFromEnv_UsesBackendFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "langfuse.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`enabled: true
backend: langfuse
`), 0o644))
	t.Setenv(envTelemetryConfig, path)

	_, enabled, err := resolveConfigFromEnv()
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestResolveConfigFromEnv_RejectsMissingBackendInYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "langfuse.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`enabled: true
langfuse:
  host: localhost:3000
`), 0o644))
	t.Setenv(envTelemetryConfig, path)

	_, _, err := resolveConfigFromEnv()
	require.Error(t, err)
}

func TestResolveConfigFromEnv_RejectsUnsupportedBackendInYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "langfuse.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`enabled: true
backend: jaeger
`), 0o644))
	t.Setenv(envTelemetryConfig, path)

	_, _, err := resolveConfigFromEnv()
	require.Error(t, err)
}

func TestResolveConfigFromEnv_EnvOverrideWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "langfuse.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`enabled: false
backend: langfuse
`), 0o644))
	t.Setenv(envTelemetryEnabled, "true")
	t.Setenv(envTelemetryConfig, path)

	_, enabled, err := resolveConfigFromEnv()
	require.NoError(t, err)
	assert.True(t, enabled)
}

func TestResolveProcessorMode(t *testing.T) {
	mode, err := resolveProcessorMode("")
	require.NoError(t, err)
	assert.Equal(t, baselangfuse.SpanProcessorModeSimple, mode)

	mode, err = resolveProcessorMode("batch")
	require.NoError(t, err)
	assert.Equal(t, baselangfuse.SpanProcessorModeBatch, mode)

	_, err = resolveProcessorMode("unknown")
	require.Error(t, err)
}
