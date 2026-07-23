//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package chromadb provides a ChromaDB-backed memory service over the ChromaDB REST API.
package chromadb

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/embedder"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	imemory "trpc.group/trpc-go/trpc-agent-go/memory/internal/memory"
)

const (
	defaultCollectionName       = "memories"
	defaultTenant               = "default_tenant"
	defaultDatabase             = "default_database"
	defaultMaxResults           = 10
	defaultSimilarityThreshold  = 0.30
	defaultHybridCandidateLimit = 1000
	defaultRequestTimeout       = 10 * time.Second
	defaultInitTimeout          = 30 * time.Second
	defaultReadPageSize         = 300
	defaultWriteLockStripes     = 256
)

var collectionNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,510}[a-z0-9]$`)

type serviceOpts struct {
	baseURL    string
	apiKey     string
	bearer     string
	headers    map[string]string
	tenant     string
	database   string
	httpClient *http.Client
	timeout    time.Duration

	collectionName       string
	autoCreateCollection bool
	indexDimension       *int
	embedder             embedder.Embedder

	maxResults           int
	similarityThreshold  float64
	hybridCandidateLimit int
	memoryLimit          int
	softDelete           bool

	toolCreators      map[string]memory.ToolCreator
	enabledTools      map[string]struct{}
	toolExposed       map[string]struct{}
	toolHidden        map[string]struct{}
	userExplicitlySet map[string]struct{}

	extractor                          extractor.MemoryExtractor
	asyncMemoryNum                     int
	memoryQueueSize                    int
	memoryJobTimeout                   time.Duration
	disableAutoMemoryOnExternalContext bool
}

// defaultServiceOpts returns an isolated option set for one Service instance.
func defaultServiceOpts() serviceOpts {
	return serviceOpts{
		headers:              make(map[string]string),
		collectionName:       defaultCollectionName,
		autoCreateCollection: true,
		maxResults:           defaultMaxResults,
		similarityThreshold:  defaultSimilarityThreshold,
		hybridCandidateLimit: defaultHybridCandidateLimit,
		memoryLimit:          imemory.DefaultMemoryLimit,
		timeout:              defaultRequestTimeout,
		toolCreators:         maps.Clone(imemory.AllToolCreators),
		enabledTools:         maps.Clone(imemory.DefaultEnabledTools),
		toolExposed:          make(map[string]struct{}),
		toolHidden:           make(map[string]struct{}),
		userExplicitlySet:    make(map[string]struct{}),
		asyncMemoryNum:       imemory.DefaultAsyncMemoryNum,
		memoryQueueSize:      imemory.DefaultMemoryQueueSize,
		memoryJobTimeout:     imemory.DefaultMemoryJobTimeout,
	}
}

// normalizeAndValidateOptions canonicalizes the final option state and validates
// cross-option constraints after all options have been applied.
func normalizeAndValidateOptions(opts *serviceOpts) error {
	if opts == nil {
		return errors.New("chromadb options are nil")
	}
	if opts.baseURL == "" {
		return errors.New("base URL is required")
	}
	if opts.embedder == nil {
		return errors.New("embedder is required")
	}
	if err := validateCollectionName(opts.collectionName); err != nil {
		return err
	}
	if err := validateNumericOptions(*opts); err != nil {
		return err
	}
	if err := validateEmbeddingDimensions(*opts); err != nil {
		return err
	}
	if err := validateToolOptions(*opts); err != nil {
		return err
	}
	headers, err := canonicalHTTPHeaders(opts.headers)
	if err != nil {
		return err
	}
	opts.headers = headers
	parsed, err := validateBaseURL(*opts)
	if err != nil {
		return err
	}
	opts.baseURL = strings.TrimRight(parsed.String(), "/")
	return validateAuthentication(*opts)
}

// validateNumericOptions rejects invalid limits, thresholds, and timeouts.
func validateNumericOptions(opts serviceOpts) error {
	if opts.timeout <= 0 {
		return fmt.Errorf("timeout must be positive: %s", opts.timeout)
	}
	if opts.indexDimension != nil && *opts.indexDimension <= 0 {
		return fmt.Errorf("index dimension must be positive: %d", *opts.indexDimension)
	}
	if opts.maxResults <= 0 {
		return fmt.Errorf("max results must be positive: %d", opts.maxResults)
	}
	if math.IsNaN(opts.similarityThreshold) ||
		opts.similarityThreshold < 0 || opts.similarityThreshold > 1 {
		return fmt.Errorf(
			"similarity threshold must be between 0 and 1: %f",
			opts.similarityThreshold,
		)
	}
	if opts.hybridCandidateLimit <= 0 {
		return fmt.Errorf(
			"hybrid candidate limit must be positive: %d",
			opts.hybridCandidateLimit,
		)
	}
	if opts.memoryJobTimeout <= 0 {
		return fmt.Errorf("memory job timeout must be positive: %s", opts.memoryJobTimeout)
	}
	return nil
}

// validateEmbeddingDimensions checks the explicit and embedder-provided dimensions.
func validateEmbeddingDimensions(opts serviceOpts) error {
	embedderDimension := opts.embedder.GetDimensions()
	if embedderDimension < 0 {
		return fmt.Errorf("embedder returned invalid dimension: %d", embedderDimension)
	}
	if opts.indexDimension == nil || embedderDimension == 0 {
		return nil
	}
	if *opts.indexDimension != embedderDimension {
		return fmt.Errorf(
			"embedding dimension mismatch: configured %d, embedder reports %d",
			*opts.indexDimension,
			embedderDimension,
		)
	}
	return nil
}

// validateCollectionName applies Chroma's local collection naming rules.
func validateCollectionName(name string) error {
	if !collectionNamePattern.MatchString(name) {
		return fmt.Errorf("invalid collection name %q", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("invalid collection name %q: consecutive periods are not allowed", name)
	}
	if net.ParseIP(name) != nil {
		return fmt.Errorf("invalid collection name %q: IP addresses are not allowed", name)
	}
	return nil
}

// validateBaseURL validates the endpoint and enforces encrypted remote credentials.
func validateBaseURL(opts serviceOpts) (*url.URL, error) {
	parsed, err := url.Parse(opts.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("base URL scheme must be http or https: %s", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, errors.New("base URL host is required")
	}
	if parsed.User != nil {
		return nil, errors.New("base URL must not contain user information")
	}
	if parsed.RawQuery != "" {
		return nil, errors.New("base URL must not contain a query")
	}
	if parsed.Fragment != "" {
		return nil, errors.New("base URL must not contain a fragment")
	}
	hasSensitiveHeaders := opts.apiKey != "" || opts.bearer != "" || len(opts.headers) > 0
	if hasSensitiveHeaders && parsed.Scheme != "https" && !isLoopbackHost(parsed.Hostname()) {
		return nil, errors.New("https is required for remote authentication or custom HTTP headers")
	}
	return parsed, nil
}

// isLoopbackHost reports whether host resolves syntactically to a loopback endpoint.
func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// canonicalHTTPHeaders validates names and values while detecting canonical duplicates.
func canonicalHTTPHeaders(headers map[string]string) (map[string]string, error) {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make(map[string]string, len(headers))
	for _, name := range names {
		if name == "" || strings.TrimSpace(name) != name || !validHTTPHeaderName(name) {
			return nil, fmt.Errorf("invalid HTTP header name %q", name)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if _, exists := result[canonical]; exists {
			return nil, fmt.Errorf("duplicate HTTP header %q", canonical)
		}
		value := headers[name]
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("invalid value for HTTP header %q", canonical)
		}
		result[canonical] = value
	}
	return result, nil
}

// validHTTPHeaderName checks a header name against the HTTP token grammar.
func validHTTPHeaderName(name string) bool {
	for i := 0; i < len(name); i++ {
		char := name[i]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' {
			continue
		}
		if strings.ContainsRune("!#$%&'*+-.^_`|~", rune(char)) {
			continue
		}
		return false
	}
	return true
}

// validateAuthentication rejects ambiguous or unsafe combinations of credentials.
func validateAuthentication(opts serviceOpts) error {
	if opts.apiKey != "" && opts.bearer != "" {
		return errors.New("api key and bearer token are mutually exclusive")
	}
	_, hasAuthorization := opts.headers["Authorization"]
	_, hasChromaToken := opts.headers["X-Chroma-Token"]
	if hasAuthorization && hasChromaToken {
		return errors.New("custom authorization and x-chroma-token headers are mutually exclusive")
	}
	if (hasAuthorization || hasChromaToken) && (opts.apiKey != "" || opts.bearer != "") {
		return errors.New("custom authentication header conflicts with configured authentication")
	}
	if (hasAuthorization || hasChromaToken) && (opts.tenant == "" || opts.database == "") {
		return errors.New("tenant and database are required with custom authentication headers")
	}
	return nil
}

// validateToolOptions ensures every configured tool name belongs to the memory service.
func validateToolOptions(opts serviceOpts) error {
	for name, creator := range opts.toolCreators {
		if !imemory.IsValidToolName(name) {
			return fmt.Errorf("invalid memory tool name: %s", name)
		}
		if creator == nil {
			return fmt.Errorf("memory tool creator is nil: %s", name)
		}
	}
	for _, names := range []map[string]struct{}{
		opts.enabledTools,
		opts.toolExposed,
		opts.toolHidden,
		opts.userExplicitlySet,
	} {
		for name := range names {
			if !imemory.IsValidToolName(name) {
				return fmt.Errorf("invalid memory tool name: %s", name)
			}
		}
	}
	return nil
}

// ensureToolMaps initializes option maps before an option mutates them.
func ensureToolMaps(opts *serviceOpts) {
	if opts.toolCreators == nil {
		opts.toolCreators = make(map[string]memory.ToolCreator)
	}
	if opts.enabledTools == nil {
		opts.enabledTools = make(map[string]struct{})
	}
	if opts.toolExposed == nil {
		opts.toolExposed = make(map[string]struct{})
	}
	if opts.toolHidden == nil {
		opts.toolHidden = make(map[string]struct{})
	}
	if opts.userExplicitlySet == nil {
		opts.userExplicitlySet = make(map[string]struct{})
	}
}

// ServiceOpt configures a ChromaDB memory service.
type ServiceOpt func(*serviceOpts)

// WithBaseURL sets the ChromaDB REST server base URL.
func WithBaseURL(baseURL string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.baseURL = strings.TrimSpace(baseURL)
	}
}

// WithAPIKey sets the Chroma Cloud API key sent in X-Chroma-Token.
//
// The value is treated as sensitive. A non-loopback server must use HTTPS.
func WithAPIKey(apiKey string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.apiKey = strings.TrimSpace(apiKey)
	}
}

// WithBearerToken sets a bearer token for a proxy or custom ChromaDB gateway.
//
// The value is treated as sensitive. A non-loopback server must use HTTPS.
func WithBearerToken(token string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.bearer = strings.TrimSpace(token)
	}
}

// WithHTTPHeaders sets sensitive custom HTTP headers and defensively copies the input map.
//
// Custom authentication headers require explicit tenant and database values. Any
// custom header sent to a non-loopback server requires HTTPS.
func WithHTTPHeaders(headers map[string]string) ServiceOpt {
	copied := maps.Clone(headers)
	return func(opts *serviceOpts) {
		opts.headers = maps.Clone(copied)
	}
}

// WithTenant sets the ChromaDB tenant.
func WithTenant(tenant string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.tenant = strings.TrimSpace(tenant)
	}
}

// WithDatabase sets the ChromaDB database.
func WithDatabase(database string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.database = strings.TrimSpace(database)
	}
}

// WithHTTPClient sets the HTTP client used for ChromaDB requests.
//
// NewService shallow-copies the client to install its redirect policy. Close does not close
// the caller's client or transport.
func WithHTTPClient(client *http.Client) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.httpClient = client
	}
}

// WithTimeout sets the total timeout for one ChromaDB REST call, including retries.
//
// It does not bound a complete AddMemory, UpdateMemory, ReadMemories, or
// SearchMemories workflow, which may make multiple REST calls.
func WithTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.timeout = timeout
	}
}

// WithCollectionName sets the ChromaDB collection name.
func WithCollectionName(name string) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.collectionName = strings.TrimSpace(name)
	}
}

// WithAutoCreateCollection controls whether a missing collection is created.
func WithAutoCreateCollection(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.autoCreateCollection = enabled
	}
}

// WithIndexDimension sets the expected embedding dimension.
func WithIndexDimension(dimension int) ServiceOpt {
	return func(opts *serviceOpts) {
		value := dimension
		opts.indexDimension = &value
	}
}

// WithEmbedder sets the embedder used for memories and search queries.
func WithEmbedder(e embedder.Embedder) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.embedder = e
	}
}

// WithMaxResults sets the default maximum number of vector search results.
func WithMaxResults(maxResults int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.maxResults = maxResults
	}
}

// WithSimilarityThreshold sets the default minimum cosine similarity score in [0, 1].
//
// The score is calculated as one minus cosine distance and then clamped to [0, 1].
func WithSimilarityThreshold(threshold float64) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.similarityThreshold = threshold
	}
}

// WithHybridCandidateLimit sets the maximum number of records considered by local keyword search.
func WithHybridCandidateLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.hybridCandidateLimit = limit
	}
}

// WithMemoryLimit sets the maximum number of active memories per user.
//
// A non-positive value disables the limit. Enforcement is serialized within one Service
// instance and is best-effort across multiple instances.
func WithMemoryLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.memoryLimit = limit
	}
}

// WithSoftDelete controls whether delete operations retain tombstones.
func WithSoftDelete(enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.softDelete = enabled
	}
}

// WithExtractor sets the extractor used for automatic memory generation.
func WithExtractor(e extractor.MemoryExtractor) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.extractor = e
	}
}

// WithAsyncMemoryNum sets the number of automatic memory workers.
func WithAsyncMemoryNum(num int) ServiceOpt {
	return func(opts *serviceOpts) {
		if num > 0 {
			opts.asyncMemoryNum = num
		}
	}
}

// WithMemoryQueueSize sets the automatic memory queue size per worker.
func WithMemoryQueueSize(size int) ServiceOpt {
	return func(opts *serviceOpts) {
		if size > 0 {
			opts.memoryQueueSize = size
		}
	}
}

// WithMemoryJobTimeout sets the timeout for one automatic memory job.
func WithMemoryJobTimeout(timeout time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.memoryJobTimeout = timeout
	}
}

// WithDisableAutoMemoryOnExternalContext disables extraction for polluted sessions.
func WithDisableAutoMemoryOnExternalContext(disable bool) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.disableAutoMemoryOnExternalContext = disable
	}
}

// WithCustomTool sets and enables a custom memory tool implementation.
func WithCustomTool(name string, creator memory.ToolCreator) ServiceOpt {
	return func(opts *serviceOpts) {
		ensureToolMaps(opts)
		opts.toolCreators[name] = creator
		opts.enabledTools[name] = struct{}{}
		opts.userExplicitlySet[name] = struct{}{}
	}
}

// WithToolEnabled controls whether a memory tool is enabled.
func WithToolEnabled(name string, enabled bool) ServiceOpt {
	return func(opts *serviceOpts) {
		ensureToolMaps(opts)
		if enabled {
			opts.enabledTools[name] = struct{}{}
		} else {
			delete(opts.enabledTools, name)
		}
		opts.userExplicitlySet[name] = struct{}{}
	}
}

// WithAutoMemoryExposedTools exposes enabled tools in automatic memory mode.
func WithAutoMemoryExposedTools(names ...string) ServiceOpt {
	copied := append([]string(nil), names...)
	return func(opts *serviceOpts) {
		for _, name := range copied {
			setToolExposed(opts, name, true)
		}
	}
}

// WithToolExposed controls whether an enabled tool is returned by Tools.
func WithToolExposed(name string, exposed bool) ServiceOpt {
	return func(opts *serviceOpts) {
		setToolExposed(opts, name, exposed)
	}
}

// setToolExposed records whether a memory tool is visible to auto-memory extraction.
func setToolExposed(opts *serviceOpts, name string, exposed bool) {
	ensureToolMaps(opts)
	if exposed {
		opts.toolExposed[name] = struct{}{}
		delete(opts.toolHidden, name)
		return
	}
	opts.toolHidden[name] = struct{}{}
	delete(opts.toolExposed, name)
}
