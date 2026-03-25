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
	"reflect"
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

func TestLoadConfigFromFile_ReturnsReadError(t *testing.T) {
	_, err := loadConfigFromFile(filepath.Join(t.TempDir(), "missing.yaml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read config")
}

func TestLoadConfigFromFile_ReturnsDecodeError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "langfuse.yaml")
	require.NoError(t, os.WriteFile(path, []byte("enabled: ["), 0o644))

	_, err := loadConfigFromFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode config")
}

func TestBuildStartOptions_AppliesAllConfiguredFields(t *testing.T) {
	insecure := true
	maxBytes := 256

	opts, err := buildStartOptions(langfuseConfig{
		PublicKey:                    "pk",
		SecretKey:                    "sk",
		Host:                         "localhost:3000",
		Insecure:                     &insecure,
		ObservationLeafValueMaxBytes: &maxBytes,
		Processor:                    "batch",
	})
	require.NoError(t, err)
	require.Len(t, opts, 6)

	cfg := applyBaseLangfuseOptions(t, opts)
	assert.Equal(t, "pk", cfg.FieldByName("publicKey").String())
	assert.Equal(t, "sk", cfg.FieldByName("secretKey").String())
	assert.Equal(t, "localhost:3000", cfg.FieldByName("host").String())
	assert.True(t, cfg.FieldByName("insecure").Bool())
	assert.Equal(t, string(baselangfuse.SpanProcessorModeBatch), cfg.FieldByName("spanProcessorMode").String())

	maxField := cfg.FieldByName("maxObservationLeafValueBytes")
	if assert.False(t, maxField.IsNil()) {
		assert.EqualValues(t, maxBytes, maxField.Elem().Int())
	}
}

func TestBuildStartOptions_UsesSecureModeWhenConfigured(t *testing.T) {
	insecure := false

	opts, err := buildStartOptions(langfuseConfig{
		Insecure:  &insecure,
		Processor: "simple",
	})
	require.NoError(t, err)
	require.Len(t, opts, 2)

	cfg := applyBaseLangfuseOptions(t, opts)
	assert.False(t, cfg.FieldByName("insecure").Bool())
	assert.Equal(t, string(baselangfuse.SpanProcessorModeSimple), cfg.FieldByName("spanProcessorMode").String())
	assert.True(t, cfg.FieldByName("maxObservationLeafValueBytes").IsNil())
}

func TestBuildStartOptions_ReturnsErrorForInvalidProcessor(t *testing.T) {
	_, err := buildStartOptions(langfuseConfig{Processor: "invalid"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported processor")
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

func TestGetBoolEnv_ReturnsErrorForInvalidValue(t *testing.T) {
	t.Setenv(envTelemetryEnabled, "definitely-not-bool")

	_, err := getBoolEnv(envTelemetryEnabled)
	require.Error(t, err)
	assert.Contains(t, err.Error(), envTelemetryEnabled)
}

func applyBaseLangfuseOptions(t *testing.T, opts []baselangfuse.Option) reflect.Value {
	t.Helper()
	require.NotEmpty(t, opts)

	cfgPtr := reflect.New(reflect.TypeOf(opts[0]).In(0).Elem())
	for _, opt := range opts {
		reflect.ValueOf(opt).Call([]reflect.Value{cfgPtr})
	}
	return cfgPtr.Elem()
}
