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
	"net/http"
)

// roundTripper allows mocking http.Transport for testing purposes.
type roundTripper func(*http.Request) *http.Response

// RoundTrip implements the http.RoundTripper interface.
func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req), nil
}
