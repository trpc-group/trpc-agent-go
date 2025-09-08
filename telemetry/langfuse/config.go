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
)

// Config holds Langfuse configuration options.
type Config struct {
	SecretKey string
	PublicKey string
	Host      string
}

// newConfigFromEnv creates a Langfuse config from environment variables.
func newConfigFromEnv() *Config {
	return &Config{
		SecretKey: getEnv("LANGFUSE_SECRET_KEY", "your-secret-key"),
		PublicKey: getEnv("LANGFUSE_PUBLIC_KEY", "your-public-key"),
		Host:      getEnv("LANGFUSE_HOST", "http://localhost:3000"),
	}
}

// getEnv returns the value of the environment variable or the default if not set.
func getEnv(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return defaultValue
}
