//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimizer

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestDefaultMessageBuilder(t *testing.T) {
	builder := defaultMessageBuilder()
	currentText := "current instruction"

	msg, err := builder(context.Background(), &Request{
		Surface: &astructure.Surface{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &currentText,
			},
		},
		Gradient: &promptiter.AggregatedSurfaceGradient{
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
		},
	})

	assert.NoError(t, err)
	assert.NotNil(t, msg)
	if msg == nil {
		return
	}
	assert.Equal(t, model.RoleUser, msg.Role)
	assert.Contains(t, msg.Content, "Optimize one PromptIter surface from the provided current value and aggregated gradients.")
	assert.Contains(t, msg.Content, "Prefer the smallest high-confidence change that preserves working parts of the current value.")
	assert.Contains(t, msg.Content, "When the current value is mostly correct, prefer removing unsupported or speculative detail before adding new detail.")
	assert.Contains(t, msg.Content, "Do not trade factual precision for stylistic vividness.")
	assert.Contains(t, msg.Content, "Avoid broad rewrites unless the gradients indicate multiple independent failures.")
	assert.Contains(t, msg.Content, "Return only Value and Reason fields.")
	assert.NotContains(t, msg.Content, "SurfaceID")
	assert.NotContains(t, msg.Content, "EvalSetID")
	assert.NotContains(t, msg.Content, "EvalCaseID")
	assert.NotContains(t, msg.Content, "StepID")

	payloadContent, ok := extractRequestJSON(msg.Content)
	assert.True(t, ok)
	if !ok {
		return
	}

	var payload promptData
	err = json.Unmarshal([]byte(payloadContent), &payload)
	assert.NoError(t, err)
	assert.Equal(t, &promptData{
		Surface: promptSurface{
			Type: astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &currentText,
			},
		},
		Gradients: []promptGradient{
			{
				Severity: promptiter.LossSeverityP1,
				Gradient: "keep citation",
			},
		},
	}, &payload)
}

func TestNewPromptDataOmitsSurfaceIdentity(t *testing.T) {
	currentText := "current instruction"
	data := newPromptData(&Request{
		Surface: &astructure.Surface{
			SurfaceID: "surf_1",
			NodeID:    "node_1",
			Type:      astructure.SurfaceTypeInstruction,
			Value: astructure.SurfaceValue{
				Text: &currentText,
			},
		},
		Gradient: &promptiter.AggregatedSurfaceGradient{
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
		},
	})

	payload, err := json.Marshal(data)

	assert.NoError(t, err)
	assert.NotContains(t, string(payload), "SurfaceID")
	assert.NotContains(t, string(payload), "NodeID")
	assert.NotContains(t, string(payload), "EvalSetID")
	assert.NotContains(t, string(payload), "EvalCaseID")
	assert.NotContains(t, string(payload), "StepID")
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

func TestToPrettyJSONRejectsUnsupportedValue(t *testing.T) {
	rendered, err := toPrettyJSON(map[string]float64{"score": math.NaN()})
	assert.Empty(t, rendered)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "marshal optimization request")
}
