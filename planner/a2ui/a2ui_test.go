//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2ui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

func TestPlanner_New(t *testing.T) {
	plannerInstance := New()
	assert.NotNil(t, plannerInstance)
	var _ planner.Planner = plannerInstance
}

func TestPlanner_BuildPlanningInstruction_Default(t *testing.T) {
	p := New()
	instruction := p.BuildPlanningInstruction(context.Background(), &agent.Invocation{}, &model.Request{})
	assert.NotEmpty(t, instruction)
	assert.Contains(t, instruction, `"title": "A2UI (Agent to UI) Client-to-Server Event Schema"`)
	assert.Contains(t, instruction, `"title": "A2UI Message Schema"`)
	assert.Contains(t, instruction, "A2UI server-to-client output MUST be JSONL-compatible.")
	assert.Contains(t, instruction, "Each outbound text content message must contain exactly one complete JSON object")
	assert.Contains(t, instruction, "Only these message keys are allowed: beginRendering, surfaceUpdate, dataModelUpdate, deleteSurface.")
}

func TestPlanner_ProcessPlanningResponse_Nil(t *testing.T) {
	p := New(
		WithClientCapabilitiesSchema("client-cap"),
		WithCatalogDescriptionSchema("catalog-desc"),
		WithClientToServerSchema("client->server"),
		WithServerToClientSchema("server->client"),
		WithServerToClientWithStandardCatalogSchema("server->client-standard"),
		WithStandardCatalogDefinition("standard-catalog"),
	)
	result := p.ProcessPlanningResponse(context.Background(), &agent.Invocation{}, nil)
	assert.Nil(t, result)
}

func TestPlanner_BuildPlanningInstruction_WithOptions(t *testing.T) {
	p := New(
		WithClientCapabilitiesSchema("cap"),
		WithCatalogDescriptionSchema("catalog"),
		WithClientToServerSchema("cts"),
		WithServerToClientSchema("stc"),
		WithServerToClientWithStandardCatalogSchema("stc-std"),
		WithStandardCatalogDefinition("std"),
	)
	instruction := p.BuildPlanningInstruction(context.Background(), &agent.Invocation{}, &model.Request{})
	assert.Contains(t, instruction, "Client capabilities schema: cap")
	assert.Contains(t, instruction, "Catalog description schema: catalog")
	assert.Contains(t, instruction, "Client-to-server payload schema: cts")
	assert.Contains(t, instruction, "Server-to-client payload schema: stc")
	assert.Contains(t, instruction, "Server-to-client-with-standard-catalog payload schema: stc-std")
	assert.Contains(t, instruction, "Standard catalog definition: std")
}

func TestPlanner_BuildPlanningInstruction_WithInstruction(t *testing.T) {
	customInstruction := "CUSTOM INSTRUCTION: emit strict JSON objects only."
	p := New(WithInstruction(customInstruction))
	instruction := p.BuildPlanningInstruction(context.Background(), &agent.Invocation{}, &model.Request{})
	assert.Contains(t, instruction, customInstruction)
	assert.NotContains(t, instruction, "A2UI server-to-client output MUST be JSONL-compatible.")
}
