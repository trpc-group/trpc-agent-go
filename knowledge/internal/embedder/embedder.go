// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package embedder provides utilities for text embedding.
package embedder

import (
	"net/url"
	"strconv"
)

// ServerAddrPortFromBaseURL extracts the server address and port from a base URL.
func ServerAddrPortFromBaseURL(baseURL string) (addr *string, port *int) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, nil
	}
	host := u.Hostname()
	if host == "" {
		return nil, nil
	}
	if p := u.Port(); p != "" {
		pi, err := strconv.Atoi(p)
		if err != nil {
			return &host, nil
		}
		return &host, &pi
	}
	switch u.Scheme {
	case "http":
		p := 80
		return &host, &p
	case "https":
		p := 443
		return &host, &p
	default:
		return &host, nil
	}
}
