// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package a2ui provides an A2UI-specific planner.
package a2ui

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

// Verify that a2uiPlanner implements the planner interface.
var _ planner.Planner = (*a2uiPlanner)(nil)

// a2uiPlanner implements the planner interface for A2UI.
type a2uiPlanner struct {
	instruction                       string
	clientCapabilitiesSchema          string
	catalogDescriptionSchema          string
	clientToServer                    string
	serverToClient                    string
	serverToClientWithStandardCatalog string
	standardCatalogDefinition         string
}

// New creates a new A2UI planner.
func New(opts ...Option) planner.Planner {
	o := newOptions(opts...)
	return &a2uiPlanner{
		instruction:                       o.instruction,
		clientCapabilitiesSchema:          o.clientCapabilitiesSchema,
		catalogDescriptionSchema:          o.catalogDescriptionSchema,
		clientToServer:                    o.clientToServer,
		serverToClient:                    o.serverToClient,
		serverToClientWithStandardCatalog: o.serverToClientWithStandardCatalog,
		standardCatalogDefinition:         o.standardCatalogDefinition,
	}
}

// BuildPlanningInstruction injects A2UI protocol constraints.
func (p *a2uiPlanner) BuildPlanningInstruction(ctx context.Context, invocation *agent.Invocation,
	llmRequest *model.Request) string {
	instructions := make([]string, 0)
	if p.instruction != "" {
		instructions = append(instructions, p.instruction)
	}
	if p.serverToClientWithStandardCatalog != "" {
		instructions = append(instructions,
			fmt.Sprintf("Server-to-client-with-standard-catalog payload schema: %s", p.serverToClientWithStandardCatalog),
		)
	}
	if p.clientToServer != "" {
		instructions = append(instructions,
			fmt.Sprintf("Client-to-server payload schema: %s", p.clientToServer),
		)
	}
	if p.clientCapabilitiesSchema != "" {
		instructions = append(instructions,
			fmt.Sprintf("Client capabilities schema: %s", p.clientCapabilitiesSchema),
		)
	}
	if p.serverToClient != "" {
		instructions = append(instructions,
			fmt.Sprintf("Server-to-client payload schema: %s", p.serverToClient),
		)
	}
	if p.standardCatalogDefinition != "" {
		instructions = append(instructions,
			fmt.Sprintf("Standard catalog definition: %s", p.standardCatalogDefinition),
		)
	}
	if p.catalogDescriptionSchema != "" {
		instructions = append(instructions,
			fmt.Sprintf("Catalog description schema: %s", p.catalogDescriptionSchema),
		)
	}
	return strings.Join(instructions, "\n\n")
}

// ProcessPlanningResponse returns nil to indicate that no planning-specific response processing is needed.
func (p *a2uiPlanner) ProcessPlanningResponse(ctx context.Context, invocation *agent.Invocation,
	response *model.Response) *model.Response {
	return nil
}
