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

import "os"

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

// WithHost sets the Langfuse host URL.
func WithHost(host string) Option {
	return func(cfg *config) {
		cfg.host = host
	}
}

// config holds Langfuse configuration options.
type config struct {
	secretKey string
	publicKey string
	host      string
}

// newConfigFromEnv creates a Langfuse config from environment variables.
func newConfigFromEnv() *config {
	return &config{
		secretKey: getEnv("LANGFUSE_SECRET_KEY", ""),
		publicKey: getEnv("LANGFUSE_PUBLIC_KEY", ""),
		host:      getEnv("LANGFUSE_HOST", ""),
	}
}

// getEnv returns the value of the environment variable or the default if not set.
func getEnv(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}
