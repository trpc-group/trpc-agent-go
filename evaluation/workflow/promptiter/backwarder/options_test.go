//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package backwarder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestDefaultMessageBuilder(t *testing.T) {
	builder := defaultMessageBuilder()
	currentText := "current instruction"

	msg, err := builder(context.Background(), &Request{
		EvalSetID:  "set_a",
		EvalCaseID: "case_1",
		Node: &astructure.Node{
			NodeID: "node_1",
			Kind:   "llm",
			Name:   "responder",
		},
		StepID: "step_1",
		Input: &atrace.Snapshot{
			Text: "input text",
		},
		Output: &atrace.Snapshot{
			Text: "output text",
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "surf_1",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: &currentText,
				},
			},
		},
		Predecessors: []Predecessor{
			{
				StepID: "pred_1",
				NodeID: "node_pred",
				Output: &atrace.Snapshot{
					Text: "predecessor output",
				},
			},
		},
		Incoming: []GradientPacket{
			{
				FromStepID: "step_downstream",
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "need citations",
			},
		},
	})

	assert.NoError(t, err)
	assert.NotNil(t, msg)
	if msg == nil {
		return
	}
	assert.Equal(t, model.RoleUser, msg.Role)
	assert.Contains(t, msg.Content, "Compute PromptIter backward attribution for one step.")

	payloadContent, ok := extractRequestJSON(msg.Content)
	assert.True(t, ok)
	if !ok {
		return
	}

	var payload Request
	err = json.Unmarshal([]byte(payloadContent), &payload)
	assert.NoError(t, err)
	assert.Equal(t, &Request{
		EvalSetID:  "set_a",
		EvalCaseID: "case_1",
		Node: &astructure.Node{
			NodeID: "node_1",
			Kind:   "llm",
			Name:   "responder",
		},
		StepID: "step_1",
		Input: &atrace.Snapshot{
			Text: "input text",
		},
		Output: &atrace.Snapshot{
			Text: "output text",
		},
		Surfaces: []astructure.Surface{
			{
				SurfaceID: "surf_1",
				NodeID:    "node_1",
				Type:      astructure.SurfaceTypeInstruction,
				Value: astructure.SurfaceValue{
					Text: &currentText,
				},
			},
		},
		Predecessors: []Predecessor{
			{
				StepID: "pred_1",
				NodeID: "node_pred",
				Output: &atrace.Snapshot{
					Text: "predecessor output",
				},
			},
		},
		Incoming: []GradientPacket{
			{
				FromStepID: "step_downstream",
				Severity:   promptiter.LossSeverityP1,
				Gradient:   "need citations",
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
