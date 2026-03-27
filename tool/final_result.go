//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

// FinalResultChunk marks the final structured result of a streamable
// tool call. When present, the flow should preserve this result instead
// of merging only textual chunks.
type FinalResultChunk struct {
	Result any
}

// FinalResultStateChunk marks the final structured result of a streamable
// tool call and carries state delta that should be emitted as a synthetic
// tool.response event by the flow.
type FinalResultStateChunk struct {
	Result     any
	StateDelta map[string][]byte
}
