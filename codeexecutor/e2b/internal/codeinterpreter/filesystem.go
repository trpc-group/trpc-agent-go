//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeinterpreter

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/e2b/internal/envdfs"
)

// NewFilesystemClient snapshots the sandbox's current native endpoint and
// credentials without taking ownership of its lifecycle or HTTP transport.
// It preserves the process execution user, including custom Basic Authorization.
// The client's HTTP timeout defaults to the sandbox request timeout. Construct
// a new client after connection metadata changes. No workspace routes are
// changed, and unsupported file endpoints fail without an interpreter fallback.
// A nil sandbox or context returns an error.
func NewFilesystemClient(ctx context.Context, s *Sandbox) (*envdfs.Client, error) {
	if ctx == nil {
		return nil, errors.New("e2b: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil || s.connection == nil {
		return nil, errors.New("e2b: sandbox not initialized")
	}
	baseURL, _, err := s.envdConnection()
	if err != nil {
		return nil, err
	}
	client := s.connection.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	if client.Timeout == 0 {
		copied := *client
		copied.Timeout = s.connection.RequestTimeout
		if copied.Timeout <= 0 {
			copied.Timeout = DefaultRequestTimeout * time.Second
		}
		client = &copied
	}
	fs, err := envdfs.NewClient(baseURL, client, s.envdHeaders())
	if err != nil {
		return nil, fmt.Errorf("e2b: configure filesystem client: %w", err)
	}
	return fs, nil
}
