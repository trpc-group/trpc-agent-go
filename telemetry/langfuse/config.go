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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
)

// Option is a function that configures Start options.
type Option func(*config)

// BaggageAttributeFilter decides whether a baggage member is copied onto
// span attributes at span start. Integrators can replace the default Langfuse
// allowlist or compose with WithExtraBaggageAttributeKeys.
type BaggageAttributeFilter func(baggage.Member) bool

// AttributeRewriter transforms span attributes after Langfuse observation
// transforms (e.g. transformCallLLM) and immediately before OTLP upload.
// Returning a new slice leaves the in-memory span unchanged for local processors.
// A nil rewriter preserves attributes as stamped/transformed by the library (default).
type AttributeRewriter func(attrs []attribute.KeyValue) []attribute.KeyValue

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

// WithObservationLeafValueMaxBytes configures the max byte length for each leaf
// value in Langfuse observation JSON payloads (and plain string observation values).
//
// If this option is not set, truncation is disabled by default.
// If maxBytes is 0, it truncates everything.
// If maxBytes < 0, truncation is disabled.
func WithObservationLeafValueMaxBytes(maxBytes int) Option {
	return func(cfg *config) {
		v := maxBytes
		cfg.maxObservationLeafValueBytes = &v
	}
}

// WithServiceName overrides the service.name resource attribute when Start
// creates a new TracerProvider. Defaults to the library telemetry service name.
// Ignored when an existing SDK TracerProvider is already installed.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}

// WithServiceNamespace overrides the service.namespace resource attribute when
// Start creates a new TracerProvider. Defaults to the library namespace.
// Ignored when an existing SDK TracerProvider is already installed.
func WithServiceNamespace(serviceNamespace string) Option {
	return func(cfg *config) {
		cfg.serviceNamespace = serviceNamespace
	}
}

// WithServiceVersion overrides the service.version resource attribute when
// Start creates a new TracerProvider. Defaults to the library telemetry version.
// Ignored when an existing SDK TracerProvider is already installed.
func WithServiceVersion(serviceVersion string) Option {
	return func(cfg *config) {
		cfg.serviceVersion = serviceVersion
	}
}

// WithInstrumentName overrides the OpenTelemetry instrumentation scope name
// used for the global tracer. Defaults to the library instrument name.
func WithInstrumentName(instrumentName string) Option {
	return func(cfg *config) {
		cfg.instrumentName = instrumentName
	}
}

// WithGenAISystem overrides the gen_ai.system attribute value stamped by the
// library on agent/tool/chat spans. Defaults to "trpc.go.agent".
func WithGenAISystem(system string) Option {
	return func(cfg *config) {
		cfg.genAISystem = system
	}
}

// WithBaggageAttributeFilter replaces the default Langfuse baggage→attribute
// filter. When set, WithExtraBaggageAttributeKeys is ignored.
func WithBaggageAttributeFilter(filter BaggageAttributeFilter) Option {
	return func(cfg *config) {
		cfg.baggageFilter = filter
	}
}

// WithExtraBaggageAttributeKeys adds baggage keys that should be copied onto
// span attributes in addition to the default Langfuse allowlist.
func WithExtraBaggageAttributeKeys(keys ...string) Option {
	return func(cfg *config) {
		cfg.extraBaggageKeys = append(cfg.extraBaggageKeys, keys...)
	}
}

// WithAttributeRewriter registers a transform applied to span attributes after
// Langfuse observation transforms and before OTLP upload. Defaults to nil (no rewrite).
// Apply branding renames here; do not rename library keys that transformCallLLM
// consumes (e.g. trpc.go.agent.llm_request) before that transform runs — the
// exporter already runs this rewriter after those transforms.
func WithAttributeRewriter(rewriter AttributeRewriter) Option {
	return func(cfg *config) {
		cfg.attributeRewriter = rewriter
	}
}

// config holds Langfuse configuration options.
type config struct {
	secretKey                    string
	publicKey                    string
	host                         string
	insecure                     bool
	maxObservationLeafValueBytes *int
	serviceName                  string
	serviceNamespace             string
	serviceVersion               string
	instrumentName               string
	genAISystem                  string
	baggageFilter                BaggageAttributeFilter
	extraBaggageKeys             []string
	attributeRewriter            AttributeRewriter
}

// newConfigFromEnv creates a Langfuse config from environment variables.
// Supported environment variables:
//
//	LANGFUSE_SECRET_KEY: Langfuse secret key
//	LANGFUSE_PUBLIC_KEY: Langfuse public key
//	LANGFUSE_HOST: Langfuse host in "hostname:port" format (e.g., "cloud.langfuse.com:443")
//	LANGFUSE_INSECURE: Set to "true" for insecure connections (development only)
//	LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES: Optional; max byte length for each observation JSON leaf value (unset by default)
func newConfigFromEnv() *config {
	leafBytes := getEnvIntPtr("LANGFUSE_OBSERVATION_LEAF_VALUE_MAX_BYTES")
	return &config{
		secretKey:                    getEnv("LANGFUSE_SECRET_KEY", ""),
		publicKey:                    getEnv("LANGFUSE_PUBLIC_KEY", ""),
		host:                         getEnv("LANGFUSE_HOST", ""),
		insecure:                     getEnv("LANGFUSE_INSECURE", "") == "true",
		maxObservationLeafValueBytes: leafBytes,
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
