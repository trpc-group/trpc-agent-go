//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package elasticsearch

import (
	"errors"
	"net/http"
)

// roundTripper allows mocking http.Transport for testing purposes.
type roundTripper func(*http.Request) *http.Response

// RoundTrip implements the http.RoundTripper interface.
func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}

// errorReader is a mock reader that always returns an error.
type errorReader struct{}

func (r *errorReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("mock read error")
}

func (r *errorReader) Close() error {
	return nil
}
