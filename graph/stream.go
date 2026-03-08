//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"io"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// OpenStreamWriter opens an invocation-scoped stream writer by name.
//
// Streams are ephemeral and are intended for in-graph node-to-node streaming.
func OpenStreamWriter(
	ctx context.Context,
	streamName string,
) (io.WriteCloser, error) {
	return agent.OpenStreamWriter(ctx, streamName)
}

// OpenStreamReader opens an invocation-scoped stream reader by name.
//
// Streams are ephemeral and are intended for in-graph node-to-node streaming.
func OpenStreamReader(
	ctx context.Context,
	streamName string,
) (io.ReadCloser, error) {
	return agent.OpenStreamReader(ctx, streamName)
}
