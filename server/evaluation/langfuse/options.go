//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package langfuse

import (
	"context"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	lfconfig "trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse/config"
)

const (
	defaultPath        = "/langfuse/remote-experiment"
	defaultUserID      = "langfuse-remote-user"
	defaultEnvironment = "development"
	defaultTimeout     = time.Hour
)

// Option configures the Langfuse remote experiment handler.
type Option func(*options)

type options struct {
	path           string
	baseURL        string
	publicKey      string
	secretKey      string
	caseBuilder    CaseBuilder
	traceTags      []string
	userIDSupplier UserIDSupplier
	environment    string
	timeout        time.Duration
	httpClient     *http.Client
	runOptions     []agent.RunOption
}

func newOptions(opts ...Option) *options {
	connectionConfig := lfconfig.FromEnv()
	options := &options{
		path:        defaultPath,
		caseBuilder: buildCaseSpec,
		baseURL:     connectionConfig.BaseURL,
		publicKey:   connectionConfig.PublicKey,
		secretKey:   connectionConfig.SecretKey,
		traceTags:   []string{"remote-experiment", "trpc-agent-go"},
		userIDSupplier: func(_ context.Context) string {
			return defaultUserID
		},
		environment: defaultEnvironment,
		timeout:     defaultTimeout,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
	for _, opt := range opts {
		opt(options)
	}
	return options
}

// WithPath sets the handler route path.
func WithPath(path string) Option {
	return func(opts *options) {
		opts.path = path
	}
}

// WithBaseURL sets the Langfuse public API base URL.
func WithBaseURL(baseURL string) Option {
	return func(opts *options) {
		opts.baseURL = baseURL
	}
}

// WithPublicKey sets the Langfuse public API key.
func WithPublicKey(publicKey string) Option {
	return func(opts *options) {
		opts.publicKey = publicKey
	}
}

// WithSecretKey sets the Langfuse secret API key.
func WithSecretKey(secretKey string) Option {
	return func(opts *options) {
		opts.secretKey = secretKey
	}
}

// WithCaseBuilder sets the dataset item to case conversion function.
func WithCaseBuilder(caseBuilder CaseBuilder) Option {
	return func(opts *options) {
		opts.caseBuilder = caseBuilder
	}
}

// WithTraceTags sets the default trace tags used when the payload does not override them.
func WithTraceTags(tags ...string) Option {
	return func(opts *options) {
		opts.traceTags = append(opts.traceTags, tags...)
	}
}

// UserIDSupplier returns the default user ID used by one remote experiment run.
type UserIDSupplier func(ctx context.Context) string

// WithUserIDSupplier sets the user ID supplier used when the case spec does not provide one.
func WithUserIDSupplier(supplier UserIDSupplier) Option {
	return func(opts *options) {
		opts.userIDSupplier = supplier
	}
}

// WithEnvironment sets the default Langfuse environment attached to traces and scores.
func WithEnvironment(environment string) Option {
	return func(opts *options) {
		opts.environment = environment
	}
}

// WithTimeout sets the maximum execution time for one remote experiment request.
func WithTimeout(timeout time.Duration) Option {
	return func(opts *options) {
		opts.timeout = timeout
	}
}

// WithHTTPClient sets the HTTP client used for Langfuse public API calls.
func WithHTTPClient(client *http.Client) Option {
	return func(opts *options) {
		opts.httpClient = client
	}
}

// WithRunOptions appends agent run options applied to every remote experiment case.
func WithRunOptions(runOptions ...agent.RunOption) Option {
	return func(opts *options) {
		opts.runOptions = append(opts.runOptions, runOptions...)
	}
}
