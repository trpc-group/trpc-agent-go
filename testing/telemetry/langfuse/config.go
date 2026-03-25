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
	"fmt"
	"os"
	"strconv"
	"strings"

	baselangfuse "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"

	"gopkg.in/yaml.v3"
)

const (
	envTelemetryEnabled = "TRPC_AGENT_TELEMETRY_ENABLED"
	envTelemetryConfig  = "TRPC_AGENT_TELEMETRY_CONFIG"

	telemetryBackendLangfuse = "langfuse"
)

type langfuseConfig struct {
	PublicKey                    string `yaml:"public_key"`
	SecretKey                    string `yaml:"secret_key"`
	Host                         string `yaml:"host"`
	Insecure                     *bool  `yaml:"insecure"`
	ObservationLeafValueMaxBytes *int   `yaml:"observation_leaf_value_max_bytes"`
	Processor                    string `yaml:"processor"`
}

type telemetryConfigFile struct {
	Enabled  *bool          `yaml:"enabled"`
	Backend  string         `yaml:"backend"`
	Langfuse langfuseConfig `yaml:"langfuse"`
}

func resolveConfigFromEnv() (langfuseConfig, bool, error) {
	enabledOverride, err := getBoolEnv(envTelemetryEnabled)
	if err != nil {
		return langfuseConfig{}, false, err
	}
	if enabledOverride != nil && !*enabledOverride {
		return langfuseConfig{}, false, nil
	}

	configPath := strings.TrimSpace(os.Getenv(envTelemetryConfig))
	fileCfg := telemetryConfigFile{}
	if configPath != "" {
		fileCfg, err = loadConfigFromFile(configPath)
		if err != nil {
			return langfuseConfig{}, false, err
		}
	}

	enabled := false
	switch {
	case enabledOverride != nil:
		enabled = *enabledOverride
	case fileCfg.Enabled != nil:
		enabled = *fileCfg.Enabled
	}
	if !enabled {
		return fileCfg.Langfuse, false, nil
	}

	if configPath != "" {
		if err := validateTelemetryConfig(fileCfg); err != nil {
			return langfuseConfig{}, false, err
		}
	}
	return fileCfg.Langfuse, true, nil
}

func loadConfigFromFile(path string) (telemetryConfigFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return telemetryConfigFile{}, fmt.Errorf("testing/telemetry/langfuse: read config %q: %w", path, err)
	}
	cfg := telemetryConfigFile{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return telemetryConfigFile{}, fmt.Errorf("testing/telemetry/langfuse: decode config %q: %w", path, err)
	}
	return cfg, nil
}

func validateTelemetryConfig(cfg telemetryConfigFile) error {
	backend := strings.TrimSpace(cfg.Backend)
	if backend == "" {
		return fmt.Errorf("testing/telemetry/langfuse: missing backend in telemetry config")
	}
	if !strings.EqualFold(backend, telemetryBackendLangfuse) {
		return fmt.Errorf("testing/telemetry/langfuse: unsupported backend %q", cfg.Backend)
	}
	return nil
}

func buildStartOptions(cfg langfuseConfig) ([]baselangfuse.Option, error) {
	processorMode, err := resolveProcessorMode(cfg.Processor)
	if err != nil {
		return nil, err
	}

	opts := []baselangfuse.Option{
		baselangfuse.WithSpanProcessorMode(processorMode),
	}
	if cfg.PublicKey != "" {
		opts = append(opts, baselangfuse.WithPublicKey(cfg.PublicKey))
	}
	if cfg.SecretKey != "" {
		opts = append(opts, baselangfuse.WithSecretKey(cfg.SecretKey))
	}
	if cfg.Host != "" {
		opts = append(opts, baselangfuse.WithHost(cfg.Host))
	}
	if cfg.Insecure != nil {
		if *cfg.Insecure {
			opts = append(opts, baselangfuse.WithInsecure())
		} else {
			opts = append(opts, baselangfuse.WithSecure())
		}
	}
	if cfg.ObservationLeafValueMaxBytes != nil {
		opts = append(opts, baselangfuse.WithObservationLeafValueMaxBytes(*cfg.ObservationLeafValueMaxBytes))
	}
	return opts, nil
}

func resolveProcessorMode(value string) (baselangfuse.SpanProcessorMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(baselangfuse.SpanProcessorModeSimple):
		return baselangfuse.SpanProcessorModeSimple, nil
	case string(baselangfuse.SpanProcessorModeBatch):
		return baselangfuse.SpanProcessorModeBatch, nil
	default:
		return "", fmt.Errorf("testing/telemetry/langfuse: unsupported processor %q", value)
	}
}

func getBoolEnv(key string) (*bool, error) {
	value, ok := os.LookupEnv(key)
	if !ok {
		return nil, nil
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("testing/telemetry/langfuse: parse %s: %w", key, err)
	}
	return &parsed, nil
}
