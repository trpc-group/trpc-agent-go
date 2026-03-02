//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/duckduckgo"
	"trpc.group/trpc-go/trpc-agent-go/tool/file"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"

	arxivsearch "trpc.group/trpc-go/trpc-agent-go/tool/arxivsearch"
	email "trpc.group/trpc-go/trpc-agent-go/tool/email"
	googlesearch "trpc.group/trpc-go/trpc-agent-go/tool/google/search"
	openapitool "trpc.group/trpc-go/trpc-agent-go/tool/openapi"
	httpfetch "trpc.group/trpc-go/trpc-agent-go/tool/webfetch/httpfetch"
	"trpc.group/trpc-go/trpc-agent-go/tool/wikipedia"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
)

const (
	toolProviderDuckDuckGo = "duckduckgo"
	toolProviderWebFetch   = "webfetch_http"

	toolSetProviderMCP     = "mcp"
	toolSetProviderFile    = "file"
	toolSetProviderOpenAPI = "openapi"
	toolSetProviderGoogle  = "google"
	toolSetProviderWiki    = "wikipedia"
	toolSetProviderArxiv   = "arxivsearch"
	toolSetProviderEmail   = "email"

	defaultHTTPTimeout = 30 * time.Second

	envGoogleAPIKey   = "GOOGLE_API_KEY"
	envGoogleEngineID = "GOOGLE_SEARCH_ENGINE_ID"

	mcpTransportStdio      = "stdio"
	mcpTransportSSE        = "sse"
	mcpTransportStreamable = "streamable"
)

func init() {
	must(registry.RegisterToolProvider(
		toolProviderDuckDuckGo,
		newDuckDuckGoTools,
	))
	must(registry.RegisterToolProvider(
		toolProviderWebFetch,
		newHTTPWebFetchTools,
	))

	must(registry.RegisterToolSetProvider(
		toolSetProviderMCP,
		newMCPToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderFile,
		newFileToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderOpenAPI,
		newOpenAPIToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderGoogle,
		newGoogleToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderWiki,
		newWikipediaToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderArxiv,
		newArxivToolSet,
	))
	must(registry.RegisterToolSetProvider(
		toolSetProviderEmail,
		newEmailToolSet,
	))
}

type httpToolConfig struct {
	BaseURL   string        `yaml:"base_url,omitempty"`
	UserAgent string        `yaml:"user_agent,omitempty"`
	Timeout   time.Duration `yaml:"timeout,omitempty"`
}

func newDuckDuckGoTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg httpToolConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: defaultHTTPTimeout}
	if cfg.Timeout > 0 {
		client.Timeout = cfg.Timeout
	}

	opts := make([]duckduckgo.Option, 0, 3)
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		opts = append(opts, duckduckgo.WithBaseURL(baseURL))
	}
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		opts = append(opts, duckduckgo.WithUserAgent(ua))
	}
	opts = append(opts, duckduckgo.WithHTTPClient(client))

	return []tool.Tool{duckduckgo.NewTool(opts...)}, nil
}

type httpWebFetchConfig struct {
	AllowedDomains []string      `yaml:"allowed_domains,omitempty"`
	BlockedDomains []string      `yaml:"blocked_domains,omitempty"`
	AllowAll       bool          `yaml:"allow_all_domains,omitempty"`
	Timeout        time.Duration `yaml:"timeout,omitempty"`

	MaxContentLength      int `yaml:"max_content_length,omitempty"`
	MaxTotalContentLength int `yaml:"max_total_content_length,omitempty"`
}

func newHTTPWebFetchTools(
	_ registry.ToolProviderDeps,
	spec registry.PluginSpec,
) ([]tool.Tool, error) {
	var cfg httpWebFetchConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	if !cfg.AllowAll && len(cfg.AllowedDomains) == 0 {
		return nil, errors.New(
			"webfetch_http requires allowed_domains or allow_all_domains",
		)
	}

	client := &http.Client{Timeout: defaultHTTPTimeout}
	if cfg.Timeout > 0 {
		client.Timeout = cfg.Timeout
	}

	opts := make([]httpfetch.Option, 0, 6)
	opts = append(opts, httpfetch.WithHTTPClient(client))
	if cfg.MaxContentLength > 0 {
		opts = append(
			opts,
			httpfetch.WithMaxContentLength(cfg.MaxContentLength),
		)
	}
	if cfg.MaxTotalContentLength > 0 {
		opts = append(
			opts,
			httpfetch.WithMaxTotalContentLength(
				cfg.MaxTotalContentLength,
			),
		)
	}
	if len(cfg.AllowedDomains) > 0 {
		opts = append(opts, httpfetch.WithAllowedDomains(cfg.AllowedDomains))
	}
	if len(cfg.BlockedDomains) > 0 {
		opts = append(opts, httpfetch.WithBlockedDomains(cfg.BlockedDomains))
	}

	return []tool.Tool{httpfetch.NewTool(opts...)}, nil
}

type mcpFilterConfig struct {
	Mode  string   `yaml:"mode,omitempty"`
	Names []string `yaml:"names,omitempty"`
}

type mcpReconnectConfig struct {
	Enabled     bool `yaml:"enabled,omitempty"`
	MaxAttempts int  `yaml:"max_attempts,omitempty"`
}

type mcpToolSetConfig struct {
	Transport string            `yaml:"transport,omitempty"`
	ServerURL string            `yaml:"server_url,omitempty"`
	Headers   map[string]string `yaml:"headers,omitempty"`
	Command   string            `yaml:"command,omitempty"`
	Args      []string          `yaml:"args,omitempty"`
	Timeout   time.Duration     `yaml:"timeout,omitempty"`

	ToolFilter *mcpFilterConfig    `yaml:"tool_filter,omitempty"`
	Reconnect  *mcpReconnectConfig `yaml:"reconnect,omitempty"`
}

func newMCPToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg mcpToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	conn := mcp.ConnectionConfig{
		Transport: strings.TrimSpace(cfg.Transport),
		ServerURL: strings.TrimSpace(cfg.ServerURL),
		Headers:   cfg.Headers,
		Command:   strings.TrimSpace(cfg.Command),
		Args:      cfg.Args,
		Timeout:   cfg.Timeout,
	}

	if err := validateMCPConnection(conn); err != nil {
		return nil, err
	}

	options := make([]mcp.ToolSetOption, 0, 4)
	if name := strings.TrimSpace(spec.Name); name != "" {
		options = append(options, mcp.WithName(name))
	}

	filter, err := buildMCPToolFilter(cfg.ToolFilter)
	if err != nil {
		return nil, err
	}
	if filter != nil {
		options = append(options, mcp.WithToolFilterFunc(filter))
	}

	if cfg.Reconnect != nil && cfg.Reconnect.Enabled {
		attempts := cfg.Reconnect.MaxAttempts
		if attempts <= 0 {
			attempts = 3
		}
		options = append(options, mcp.WithSessionReconnect(attempts))
	}

	return mcp.NewMCPToolSet(conn, options...), nil
}

func validateMCPConnection(cfg mcp.ConnectionConfig) error {
	t := strings.ToLower(strings.TrimSpace(cfg.Transport))
	switch t {
	case mcpTransportStdio:
		if strings.TrimSpace(cfg.Command) == "" {
			return errors.New("mcp transport stdio requires command")
		}
		return nil
	case mcpTransportSSE, mcpTransportStreamable, "streamable_http":
		if strings.TrimSpace(cfg.ServerURL) == "" {
			return errors.New("mcp transport requires server_url")
		}
		return nil
	default:
		return fmt.Errorf("unsupported mcp transport: %s", cfg.Transport)
	}
}

func buildMCPToolFilter(cfg *mcpFilterConfig) (tool.FilterFunc, error) {
	if cfg == nil {
		return nil, nil
	}

	names := make([]string, 0, len(cfg.Names))
	for _, name := range cfg.Names {
		v := strings.TrimSpace(name)
		if v == "" {
			continue
		}
		names = append(names, v)
	}
	if len(names) == 0 {
		return nil, nil
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "", "include":
		return tool.NewIncludeToolNamesFilter(names...), nil
	case "exclude":
		return tool.NewExcludeToolNamesFilter(names...), nil
	default:
		return nil, fmt.Errorf("unsupported mcp tool_filter.mode: %s", cfg.Mode)
	}
}

type fileToolSetConfig struct {
	BaseDir       string `yaml:"base_dir,omitempty"`
	ReadOnly      *bool  `yaml:"read_only,omitempty"`
	EnableSave    *bool  `yaml:"enable_save,omitempty"`
	EnableReplace *bool  `yaml:"enable_replace,omitempty"`

	EnableRead          *bool `yaml:"enable_read,omitempty"`
	EnableReadMultiple  *bool `yaml:"enable_read_multiple,omitempty"`
	EnableList          *bool `yaml:"enable_list,omitempty"`
	EnableSearchFile    *bool `yaml:"enable_search_file,omitempty"`
	EnableSearchContent *bool `yaml:"enable_search_content,omitempty"`

	MaxFileSize int64 `yaml:"max_file_size,omitempty"`
}

func newFileToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg fileToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	readOnly := true
	if cfg.ReadOnly != nil {
		readOnly = *cfg.ReadOnly
	}

	saveEnabled := !readOnly
	if cfg.EnableSave != nil {
		saveEnabled = *cfg.EnableSave
	}
	replaceEnabled := !readOnly
	if cfg.EnableReplace != nil {
		replaceEnabled = *cfg.EnableReplace
	}

	opts := make([]file.Option, 0, 10)
	if baseDir := strings.TrimSpace(cfg.BaseDir); baseDir != "" {
		opts = append(opts, file.WithBaseDir(baseDir))
	}
	opts = append(opts, file.WithSaveFileEnabled(saveEnabled))
	opts = append(opts, file.WithReplaceContentEnabled(replaceEnabled))

	if cfg.EnableRead != nil {
		opts = append(opts, file.WithReadFileEnabled(*cfg.EnableRead))
	}
	if cfg.EnableReadMultiple != nil {
		opts = append(
			opts,
			file.WithReadMultipleFilesEnabled(*cfg.EnableReadMultiple),
		)
	}
	if cfg.EnableList != nil {
		opts = append(opts, file.WithListFileEnabled(*cfg.EnableList))
	}
	if cfg.EnableSearchFile != nil {
		opts = append(
			opts,
			file.WithSearchFileEnabled(*cfg.EnableSearchFile),
		)
	}
	if cfg.EnableSearchContent != nil {
		opts = append(
			opts,
			file.WithSearchContentEnabled(*cfg.EnableSearchContent),
		)
	}
	if cfg.MaxFileSize > 0 {
		opts = append(opts, file.WithMaxFileSize(cfg.MaxFileSize))
	}
	if name := strings.TrimSpace(spec.Name); name != "" {
		opts = append(opts, file.WithName(name))
	}

	return file.NewToolSet(opts...)
}

type openAPISpecConfig struct {
	File   string `yaml:"file,omitempty"`
	URL    string `yaml:"url,omitempty"`
	Inline string `yaml:"inline,omitempty"`
}

type openAPIToolSetConfig struct {
	Spec              *openAPISpecConfig `yaml:"spec,omitempty"`
	AllowExternalRefs bool               `yaml:"allow_external_refs,omitempty"`
	UserAgent         string             `yaml:"user_agent,omitempty"`
	Timeout           time.Duration      `yaml:"timeout,omitempty"`
}

func newOpenAPIToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg openAPIToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}
	if cfg.Spec == nil {
		return nil, errors.New("openapi requires config.spec")
	}

	loader, err := openAPILoader(*cfg.Spec, cfg.AllowExternalRefs)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	options := make([]openapitool.Option, 0, 4)
	options = append(options, openapitool.WithSpecLoader(loader))
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		options = append(options, openapitool.WithUserAgent(ua))
	}
	if cfg.Timeout > 0 {
		client := &http.Client{Timeout: cfg.Timeout}
		options = append(options, openapitool.WithHTTPClient(client))
	}
	if name := strings.TrimSpace(spec.Name); name != "" {
		options = append(options, openapitool.WithName(name))
	}

	return openapitool.NewToolSet(ctx, options...)
}

func openAPILoader(
	cfg openAPISpecConfig,
	allowExternalRefs bool,
) (openapitool.Loader, error) {
	opts := []openapitool.LoaderOption{
		openapitool.WithExternalRefs(allowExternalRefs),
	}

	filePath := strings.TrimSpace(cfg.File)
	urlStr := strings.TrimSpace(cfg.URL)
	inline := strings.TrimSpace(cfg.Inline)

	count := 0
	if filePath != "" {
		count++
	}
	if urlStr != "" {
		count++
	}
	if inline != "" {
		count++
	}
	if count != 1 {
		return nil, errors.New(
			"openapi.spec requires exactly one of file, url, inline",
		)
	}

	if filePath != "" {
		return openapitool.NewFileLoader(filePath, opts...)
	}
	if urlStr != "" {
		return openapitool.NewURILoader(urlStr, opts...)
	}
	return openapitool.NewDataLoader([]byte(inline), opts...)
}

type googleToolSetConfig struct {
	APIKey   string        `yaml:"api_key,omitempty"`
	EngineID string        `yaml:"engine_id,omitempty"`
	BaseURL  string        `yaml:"base_url,omitempty"`
	Size     int           `yaml:"size,omitempty"`
	Offset   int           `yaml:"offset,omitempty"`
	Lang     string        `yaml:"lang,omitempty"`
	Timeout  time.Duration `yaml:"timeout,omitempty"`
}

func newGoogleToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg googleToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(envGoogleAPIKey))
	}
	engineID := strings.TrimSpace(cfg.EngineID)
	if engineID == "" {
		engineID = strings.TrimSpace(os.Getenv(envGoogleEngineID))
	}

	ctx := context.Background()
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	options := make([]googlesearch.Option, 0, 6)
	options = append(options, googlesearch.WithAPIKey(apiKey))
	options = append(options, googlesearch.WithEngineID(engineID))
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		options = append(options, googlesearch.WithBaseURL(baseURL))
	}
	if cfg.Size > 0 {
		options = append(options, googlesearch.WithSize(cfg.Size))
	}
	if cfg.Offset > 0 {
		options = append(options, googlesearch.WithOffset(cfg.Offset))
	}
	if lang := strings.TrimSpace(cfg.Lang); lang != "" {
		options = append(options, googlesearch.WithLanguage(lang))
	}

	ts, err := googlesearch.NewToolSet(ctx, options...)
	if err != nil {
		return nil, err
	}
	return overrideToolSetName(ts, spec.Name), nil
}

type wikipediaToolSetConfig struct {
	Language   string        `yaml:"language,omitempty"`
	MaxResults int           `yaml:"max_results,omitempty"`
	UserAgent  string        `yaml:"user_agent,omitempty"`
	Timeout    time.Duration `yaml:"timeout,omitempty"`
}

func newWikipediaToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg wikipediaToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	options := make([]wikipedia.Option, 0, 4)
	if lang := strings.TrimSpace(cfg.Language); lang != "" {
		options = append(options, wikipedia.WithLanguage(lang))
	}
	if cfg.MaxResults > 0 {
		options = append(options, wikipedia.WithMaxResults(cfg.MaxResults))
	}
	if ua := strings.TrimSpace(cfg.UserAgent); ua != "" {
		options = append(options, wikipedia.WithUserAgent(ua))
	}
	if cfg.Timeout > 0 {
		options = append(options, wikipedia.WithTimeout(cfg.Timeout))
	}

	ts, err := wikipedia.NewToolSet(options...)
	if err != nil {
		return nil, err
	}
	return overrideToolSetName(ts, spec.Name), nil
}

type arxivToolSetConfig struct {
	BaseURL      string        `yaml:"base_url,omitempty"`
	PageSize     int           `yaml:"page_size,omitempty"`
	DelaySeconds time.Duration `yaml:"delay_seconds,omitempty"`
	NumRetries   int           `yaml:"num_retries,omitempty"`
}

func newArxivToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	var cfg arxivToolSetConfig
	if err := registry.DecodeStrict(spec.Config, &cfg); err != nil {
		return nil, err
	}

	options := make([]arxivsearch.Option, 0, 4)
	if baseURL := strings.TrimSpace(cfg.BaseURL); baseURL != "" {
		options = append(options, arxivsearch.WithBaseURL(baseURL))
	}
	if cfg.PageSize > 0 {
		options = append(options, arxivsearch.WithPageSize(cfg.PageSize))
	}
	if cfg.DelaySeconds > 0 {
		options = append(
			options,
			arxivsearch.WithDelaySeconds(cfg.DelaySeconds),
		)
	}
	if cfg.NumRetries > 0 {
		options = append(
			options,
			arxivsearch.WithNumRetries(cfg.NumRetries),
		)
	}

	ts, err := arxivsearch.NewToolSet(options...)
	if err != nil {
		return nil, err
	}
	return overrideToolSetName(ts, spec.Name), nil
}

func newEmailToolSet(
	_ registry.ToolSetProviderDeps,
	spec registry.PluginSpec,
) (tool.ToolSet, error) {
	ts, err := email.NewToolSet()
	if err != nil {
		return nil, err
	}
	return overrideToolSetName(ts, spec.Name), nil
}

type toolSetNameOverride struct {
	name string
	tool tool.ToolSet
}

func (t toolSetNameOverride) Tools(ctx context.Context) []tool.Tool {
	return t.tool.Tools(ctx)
}

func (t toolSetNameOverride) Close() error { return t.tool.Close() }

func (t toolSetNameOverride) Name() string { return t.name }

func overrideToolSetName(ts tool.ToolSet, name string) tool.ToolSet {
	if ts == nil {
		return nil
	}
	v := strings.TrimSpace(name)
	if v == "" || v == ts.Name() {
		return ts
	}
	return toolSetNameOverride{name: v, tool: ts}
}
