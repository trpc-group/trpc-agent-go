//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package langfuse provides Langfuse integration with custom span transformations.
package langfuse

import (
	"os"
	"strconv"
)

// Option is a function that configures Start options.
type Option func(*config)

// WithSecretKey sets the Langfuse secret key.
func WithSecretKey(secretKey string) Option {
	return func(cfg *config) {
		cfg.secretKey = secretKey
	}
}

// WithPublicKey sets the Langfuse public key.
func WithPublicKey(publicKey string) Option {
	return func(cfg *config) {
		cfg.publicKey = publicKey
	}
}

// WithHost sets the Langfuse host endpoint.
// The provided host should be in "hostname:port" format (no scheme or path).
// For cloud.langfuse.com, use "cloud.langfuse.com:443".
// For local development, use "localhost:3000".
//
// Example:
//
//	WithHost("cloud.langfuse.com:443")      // Production
//	WithHost("localhost:3000")              // Local development
func WithHost(host string) Option {
	return func(cfg *config) {
		cfg.host = host
	}
}

// WithInsecure configures the exporter to use insecure connections.
// This should only be used for development/testing environments.
// By default, secure connections are used.
func WithInsecure() Option {
	return func(cfg *config) {
		cfg.insecure = true
	}
}

// WithMaxObservationBytes configures the max byte size for Langfuse observation input/output.
//
// If this option is not set, truncation is disabled by default.
// If maxBytes is 0, it truncates everything.
// If maxBytes < 0, truncation is disabled.
func WithMaxObservationBytes(maxBytes int) Option {
	return func(cfg *config) {
		v := maxBytes
		cfg.maxObservationBytes = &v
	}
}

// config holds Langfuse configuration options.
type config struct {
	secretKey           string
	publicKey           string
	host                string
	insecure            bool
	maxObservationBytes *int
}

// newConfigFromEnv creates a Langfuse config from environment variables.
// Supported environment variables:
//
//	LANGFUSE_SECRET_KEY: Langfuse secret key
//	LANGFUSE_PUBLIC_KEY: Langfuse public key
//	LANGFUSE_HOST: Langfuse host in "hostname:port" format (e.g., "cloud.langfuse.com:443")
//	LANGFUSE_INSECURE: Set to "true" for insecure connections (development only)
//	LANGFUSE_MAX_OBSERVATION_BYTES: Optional; max byte size for observation.input/output (unset by default)
func newConfigFromEnv() *config {
	return &config{
		secretKey:           getEnv("LANGFUSE_SECRET_KEY", ""),
		publicKey:           getEnv("LANGFUSE_PUBLIC_KEY", ""),
		host:                getEnv("LANGFUSE_HOST", ""),
		insecure:            getEnv("LANGFUSE_INSECURE", "") == "true",
		maxObservationBytes: getEnvIntPtr("LANGFUSE_MAX_OBSERVATION_BYTES"),
	}
}

// getEnv returns the value of the environment variable or the default if not set.
func getEnv(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}

func getEnvIntPtr(key string) *int {
	v := getEnv(key, "")
	if v == "" {
		return nil
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return nil
	}
	return &i
}
