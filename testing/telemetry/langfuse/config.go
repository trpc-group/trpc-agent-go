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
	envTelemetryBackend = "TRPC_AGENT_TELEMETRY_BACKEND"
	envTelemetryConfig  = "TRPC_AGENT_TELEMETRY_CONFIG"

	telemetryBackendLangfuse = "langfuse"
)

type config struct {
	Enabled                      *bool  `yaml:"enabled"`
	PublicKey                    string `yaml:"public_key"`
	SecretKey                    string `yaml:"secret_key"`
	Host                         string `yaml:"host"`
	Insecure                     *bool  `yaml:"insecure"`
	ObservationLeafValueMaxBytes *int   `yaml:"observation_leaf_value_max_bytes"`
	Processor                    string `yaml:"processor"`
}

func resolveConfigFromEnv() (config, bool, error) {
	enabledOverride, err := getBoolEnv(envTelemetryEnabled)
	if err != nil {
		return config{}, false, err
	}
	if enabledOverride != nil && !*enabledOverride {
		return config{}, false, nil
	}

	configPath := strings.TrimSpace(os.Getenv(envTelemetryConfig))
	cfg := config{}
	if configPath != "" {
		cfg, err = loadConfigFromFile(configPath)
		if err != nil {
			return config{}, false, err
		}
	}

	enabled := false
	switch {
	case enabledOverride != nil:
		enabled = *enabledOverride
	case cfg.Enabled != nil:
		enabled = *cfg.Enabled
	}
	if !enabled {
		return cfg, false, nil
	}

	backend := strings.TrimSpace(os.Getenv(envTelemetryBackend))
	if backend != "" && !strings.EqualFold(backend, telemetryBackendLangfuse) {
		return config{}, false, fmt.Errorf("testing/telemetry/langfuse: unsupported backend %q", backend)
	}
	return cfg, true, nil
}

func loadConfigFromFile(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("testing/telemetry/langfuse: read config %q: %w", path, err)
	}
	cfg := config{}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return config{}, fmt.Errorf("testing/telemetry/langfuse: decode config %q: %w", path, err)
	}
	return cfg, nil
}

func buildStartOptions(cfg config) ([]baselangfuse.Option, error) {
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
