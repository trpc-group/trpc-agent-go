//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package aggregator

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestDefaultMessageBuilder(t *testing.T) {
	builder := defaultMessageBuilder()

	msg, err := builder(context.Background(), &Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{
				EvalSetID:  "set_a",
				EvalCaseID: "case_1",
				StepID:     "s1",
				SurfaceID:  "surf_1",
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "keep citation",
			},
		},
	})

	assert.NoError(t, err)
	assert.NotNil(t, msg)
	if msg == nil {
		return
	}
	assert.Equal(t, model.RoleUser, msg.Role)
	assert.Contains(t, msg.Content, "Aggregate PromptIter gradients for a single surface.")
	assert.NotContains(t, msg.Content, "Surface ID:")
	assert.NotContains(t, msg.Content, "Node ID:")
	assert.NotContains(t, msg.Content, "Surface Type:")
	assert.NotContains(t, msg.Content, "Gradient Count:")

	payloadContent, ok := extractRequestJSON(msg.Content)
	assert.True(t, ok)
	if !ok {
		return
	}

	var payload Request
	err = json.Unmarshal([]byte(payloadContent), &payload)
	assert.NoError(t, err)
	assert.Equal(t, &Request{
		SurfaceID: "surf_1",
		NodeID:    "node_1",
		Type:      astructure.SurfaceTypeInstruction,
		Gradients: []promptiter.SurfaceGradient{
			{
				EvalSetID:  "set_a",
				EvalCaseID: "case_1",
				StepID:     "s1",
				SurfaceID:  "surf_1",
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "keep citation",
			},
		},
	}, &payload)
}

func extractRequestJSON(content string) (string, bool) {
	const marker = "Request JSON:\n"
	start := strings.Index(content, marker)
	if start == -1 {
		return "", false
	}
	start += len(marker)
	return strings.TrimSpace(content[start:]), true
}
