//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package model provides interfaces for working with LLMs.
package model

import (
	"net/http"
	"testing"
)

func TestDefaultNewHTTPClient(t *testing.T) {
	client := DefaultNewHTTPClient()

	if client == nil {
		t.Error("Expected non-nil HTTP client, got nil")
	}
}

func TestDefaultNewHTTPClient_WithOptions(t *testing.T) {
	customTransport := &http.Transport{}

	client := DefaultNewHTTPClient(
		WithHTTPClientName("test-client"),
		WithHTTPClientTransport(customTransport),
	)

	if client == nil {
		t.Error("Expected non-nil HTTP client, got nil")
	}

	if httpClient, ok := client.(*http.Client); ok {
		if httpClient.Transport != customTransport {
			t.Error("Custom transport was not set correctly")
		}
	} else {
		t.Error("Returned client is not of type *http.Client")
	}
}

func TestDefaultNewHTTPClient_NoOptions(t *testing.T) {
	client := DefaultNewHTTPClient()

	if client == nil {
		t.Error("Expected non-nil HTTP client, got nil")
	}

	if httpClient, ok := client.(*http.Client); ok {
		if httpClient.Transport != nil {
			t.Error("Expected nil transport for default client")
		}
	}
}

func TestWithHTTPClientName(t *testing.T) {
	options := &HTTPClientOptions{}

	WithHTTPClientName("test-name")(options)

	if options.Name != "test-name" {
		t.Errorf("Expected name 'test-name', got '%s'", options.Name)
	}
}

func TestWithHTTPClientTransport(t *testing.T) {
	customTransport := &http.Transport{}
	options := &HTTPClientOptions{}

	WithHTTPClientTransport(customTransport)(options)

	if options.Transport != customTransport {
		t.Error("Custom transport was not set correctly in options")
	}
}

func TestHTTPClientOptions_Merge(t *testing.T) {
	customTransport := &http.Transport{}
	options := &HTTPClientOptions{}

	WithHTTPClientName("merged-client")(options)
	WithHTTPClientTransport(customTransport)(options)

	if options.Name != "merged-client" {
		t.Errorf("Expected name 'merged-client', got '%s'", options.Name)
	}

	if options.Transport != customTransport {
		t.Error("Custom transport was not set correctly after merge")
	}
}

func TestHTTPClientInterface(t *testing.T) {
	var _ HTTPClient = &http.Client{}
}

func TestHTTPClientNewFunc(t *testing.T) {
	var newFunc HTTPClientNewFunc = func(opts ...HTTPClientOption) HTTPClient {
		return &http.Client{}
	}

	client := newFunc()
	if client == nil {
		t.Error("HTTPClientNewFunc should return a non-nil client")
	}
}

func TestDefaultImplementationCompleteness(t *testing.T) {
	options := &HTTPClientOptions{}

	opts := []HTTPClientOption{
		WithHTTPClientName("complete-test"),
		WithHTTPClientTransport(http.DefaultTransport),
	}

	for _, opt := range opts {
		opt(options)
	}

	if options.Name != "complete-test" {
		t.Errorf("Expected name 'complete-test', got '%s'", options.Name)
	}

	if options.Transport != http.DefaultTransport {
		t.Error("Expected default transport to be set")
	}
}

func TestEdgeCases(t *testing.T) {
	options := &HTTPClientOptions{}
	WithHTTPClientName("")(options)
	if options.Name != "" {
		t.Error("Expected empty name to be set")
	}

	options = &HTTPClientOptions{}
	WithHTTPClientTransport(nil)(options)
	if options.Transport != nil {
		t.Error("Expected nil transport to be set")
	}

	client := DefaultNewHTTPClient()
	if client == nil {
		t.Error("Client should be created even with no options")
	}
}

func TestMultipleOptionApplications(t *testing.T) {
	options := &HTTPClientOptions{}

	WithHTTPClientName("first")(options)
	WithHTTPClientName("second")(options)
	WithHTTPClientName("third")(options)

	if options.Name != "third" {
		t.Errorf("Expected last applied name 'third', got '%s'", options.Name)
	}
}
