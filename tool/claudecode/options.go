//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package claudecode implements a Claude Code-compatible toolset.
package claudecode

import (
	"context"
	"time"
)

// Option mutates toolset construction options.
type Option func(*toolSetOptions)

// WebFetchOptions configures the WebFetch tool behavior.
type WebFetchOptions struct {
	AllowedDomains        []string
	BlockedDomains        []string
	AllowAll              bool
	Timeout               time.Duration
	MaxContentLength      int
	MaxTotalContentLength int
	PromptProcessor       WebFetchPromptProcessor
}

// WebSearchOptions configures the WebSearch tool behavior.
type WebSearchOptions struct {
	Provider  string
	BaseURL   string
	UserAgent string
	Timeout   time.Duration
	APIKey    string
	EngineID  string
	Size      int
	Offset    int
	Lang      string
}

// WebFetchPromptInput describes the extracted page content passed to a prompt processor.
type WebFetchPromptInput struct {
	URL         string
	Prompt      string
	Content     string
	ContentType string
}

// WebFetchPromptProcessor transforms fetched content into a prompt-aware result.
type WebFetchPromptProcessor func(context.Context, WebFetchPromptInput) (string, error)

type toolSetOptions struct {
	name         string
	baseDir      string
	readOnly     bool
	maxFileSize  int64
	webFetch     WebFetchOptions
	webSearch    *WebSearchOptions
	hasMaxSize   bool
	hasWebFetch  bool
	hasWebSearch bool
}

// WithBaseDir sets the base directory used by the toolset.
func WithBaseDir(baseDir string) Option {
	return func(options *toolSetOptions) {
		options.baseDir = baseDir
	}
}

// WithName overrides the toolset name.
func WithName(name string) Option {
	return func(options *toolSetOptions) {
		options.name = name
	}
}

// WithReadOnly disables mutating tools when set to true.
func WithReadOnly(readOnly bool) Option {
	return func(options *toolSetOptions) {
		options.readOnly = readOnly
	}
}

// WithMaxFileSize sets the maximum readable file size in bytes.
func WithMaxFileSize(maxFileSize int64) Option {
	return func(options *toolSetOptions) {
		options.maxFileSize = maxFileSize
		options.hasMaxSize = true
	}
}

// WithWebFetchOptions overrides WebFetch options.
func WithWebFetchOptions(webFetch WebFetchOptions) Option {
	return func(options *toolSetOptions) {
		options.webFetch = webFetch
		options.hasWebFetch = true
	}
}

// WithWebSearchOptions overrides WebSearch options.
func WithWebSearchOptions(webSearch WebSearchOptions) Option {
	return func(options *toolSetOptions) {
		webSearchCopy := webSearch
		options.webSearch = &webSearchCopy
		options.hasWebSearch = true
	}
}
