//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"testing"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
)

func TestIsStreamingToolResultActivityEvent(t *testing.T) {
	snapshot := aguievents.NewActivitySnapshotEvent("msg-1", StreamingToolResultActivityType, map[string]any{
		"toolCallId": "tool-1",
		"content":    "hello",
	})
	assert.True(t, IsStreamingToolResultActivityEvent(snapshot))

	delta := aguievents.NewActivityDeltaEvent("msg-1", StreamingToolResultActivityType, []aguievents.JSONPatchOperation{
		{Op: "add", Path: "/content", Value: "hello world"},
	})
	assert.True(t, IsStreamingToolResultActivityEvent(delta))

	otherSnapshot := aguievents.NewActivitySnapshotEvent("msg-2", "graph.node.lifecycle", map[string]any{
		"node": map[string]any{"nodeId": "node-1"},
	})
	assert.False(t, IsStreamingToolResultActivityEvent(otherSnapshot))

	toolResult := aguievents.NewToolCallResultEvent("msg-3", "tool-1", "done")
	assert.False(t, IsStreamingToolResultActivityEvent(toolResult))
}

func TestStreamingToolResultActivityMessageID(t *testing.T) {
	assert.Equal(t, "tool-result-activity-tool-1", StreamingToolResultActivityMessageID("tool-1"))
}
