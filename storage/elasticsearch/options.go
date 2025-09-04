//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package elasticsearch provides Elasticsearch client interface, implementation and options.
package elasticsearch

// Registry and builder alignment to match other storage modules.

func init() {
	esRegistry = make(map[string][]ClientBuilderOpt)
}

// esRegistry stores named Elasticsearch instance builder options.
var esRegistry map[string][]ClientBuilderOpt

// clientBuilder builds an Elasticsearch ielasticsearch.Client from builder options.
type clientBuilder func(builderOpts ...ClientBuilderOpt) (any, error)

// clientBuilder is the function to build the global Elasticsearch client.
var globalBuilder clientBuilder = defaultClientBuilder

// SetClientBuilder sets the global Elasticsearch client builder.
func SetClientBuilder(builder clientBuilder) {
	globalBuilder = builder
}

// GetClientBuilder gets the global Elasticsearch client builder.
func GetClientBuilder() clientBuilder {
	return globalBuilder
}

// RegisterElasticsearchInstance registers a named Elasticsearch instance options.
func RegisterElasticsearchInstance(name string, opts ...ClientBuilderOpt) {
	esRegistry[name] = append(esRegistry[name], opts...)
}

// GetElasticsearchInstance gets the registered options for a named instance.
func GetElasticsearchInstance(name string) ([]ClientBuilderOpt, bool) {
	if _, ok := esRegistry[name]; !ok {
		return nil, false
	}
	return esRegistry[name], true
}

// ClientBuilderOpt is the option for the Elasticsearch client builder.
type ClientBuilderOpt func(*ClientBuilderOpts)

// ClientBuilderOpts is the options for the Elasticsearch client builder.
type ClientBuilderOpts struct {
	// Version allows selecting the target Elasticsearch major version.
	// Defaults to ESVersionUnspecified which implies auto or default.
	Version ESVersion

	// Common SDK config fields across v7/v8/v9.

	// Addresses is the list of Elasticsearch node addresses.
	Addresses []string
	// Username is the username for authentication.
	Username string
	// Password is the password for authentication.
	Password string
	// APIKey is the API key for authentication.
	APIKey string
	// CertificateFingerprint is the certificate fingerprint for authentication.
	CertificateFingerprint string
	// CompressRequestBody is the flag to enable request body compression.
	CompressRequestBody bool
	// EnableMetrics is the flag to enable metrics.
	EnableMetrics bool
	// EnableDebugLogger is the flag to enable debug logger.
	EnableDebugLogger bool
	// RetryOnStatus is the list of status codes to retry on.
	RetryOnStatus []int
	// MaxRetries is the maximum number of retries.
	MaxRetries int

	// ExtraOptions allows custom builders to accept extra parameters.
	ExtraOptions []any
}

// WithAddresses sets node addresses.
func WithAddresses(addresses []string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.Addresses = addresses }
}

// WithUsername sets username.
func WithUsername(username string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.Username = username }
}

// WithPassword sets password.
func WithPassword(password string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.Password = password }
}

// WithAPIKey sets API key.
func WithAPIKey(apiKey string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.APIKey = apiKey }
}

// WithCertificateFingerprint sets TLS certificate fingerprint.
func WithCertificateFingerprint(fp string) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.CertificateFingerprint = fp }
}

// WithCompressRequestBody toggles request body compression.
func WithCompressRequestBody(enabled bool) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.CompressRequestBody = enabled }
}

// WithEnableMetrics toggles transport metrics.
func WithEnableMetrics(enabled bool) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.EnableMetrics = enabled }
}

// WithEnableDebugLogger toggles debug logger.
func WithEnableDebugLogger(enabled bool) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.EnableDebugLogger = enabled }
}

// WithRetryOnStatus sets HTTP retry status codes.
func WithRetryOnStatus(codes []int) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.RetryOnStatus = codes }
}

// WithMaxRetries sets max retries.
func WithMaxRetries(n int) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.MaxRetries = n }
}

// WithExtraOptions adds extra, builder-specific options.
func WithExtraOptions(extraOptions ...any) ClientBuilderOpt {
	return func(opts *ClientBuilderOpts) {
		opts.ExtraOptions = append(opts.ExtraOptions, extraOptions...)
	}
}

// ESVersion represents the Elasticsearch major version.
type ESVersion string

const (
	// ESVersionUnspecified means no explicit version preference.
	ESVersionUnspecified ESVersion = "0"
	// ESVersionV7 selects Elasticsearch v7.
	ESVersionV7 ESVersion = "v7"
	// ESVersionV8 selects Elasticsearch v8.
	ESVersionV8 ESVersion = "v8"
	// ESVersionV9 selects Elasticsearch v9.
	ESVersionV9 ESVersion = "v9"
)

// WithVersion sets the preferred Elasticsearch major version.
func WithVersion(v ESVersion) ClientBuilderOpt {
	return func(o *ClientBuilderOpts) { o.Version = v }
}
