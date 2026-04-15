//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/a2ui"
)

const (
	graphAgentName         = "sbti-a2ui-graph"
	directorAgentName      = "sbti_director"
	rendererAgentName      = "sbti_a2ui_renderer"
	directorStateOutputKey = "sbti_director_state"
	platformOutputTextKey  = "{{input.output_text}}"
)

func newAgent() (agent.Agent, error) {
	directorSchema, err := directorOutputSchemaMap()
	if err != nil {
		return nil, err
	}
	directorAgent := buildDirectorAgent(directorSchema)
	rendererAgent := buildRendererAgent()
	compiled, err := buildGraph()
	if err != nil {
		return nil, err
	}
	graphInstance, err := graphagent.New(
		graphAgentName,
		compiled,
		graphagent.WithDescription("Graph-orchestrated SBTI A2UI demo with separate director and renderer agent nodes."),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{directorAgent, rendererAgent}),
	)
	if err != nil {
		return nil, err
	}
	return graphInstance, nil
}

func buildGraph() (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddAgentNode(directorAgentName)
	sg.AddAgentNode(rendererAgentName)
	sg.AddEdge(directorAgentName, rendererAgentName)
	sg.SetEntryPoint(directorAgentName)
	sg.SetFinishPoint(rendererAgentName)
	return sg.Compile()
}

func buildDirectorAgent(schema map[string]any) agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:       intPtr(16384),
		Temperature:     floatPtr(1.0),
		ReasoningEffort: stringPtr("medium"),
		Stream:          *isStream,
	}
	return llmagent.New(
		directorAgentName,
		llmagent.WithModel(openai.New(*directorModelName)),
		llmagent.WithDescription("Owns SBTI state reconstruction, scoring, and minimal render-state generation."),
		llmagent.WithInstruction(localDirectorInstructionText()),
		llmagent.WithOutputSchema(schema),
		llmagent.WithOutputKey(directorStateOutputKey),
		llmagent.WithGenerationConfig(generationConfig),
	)
}

func localDirectorInstructionText() string {
	return strings.TrimSpace(sbtiLogicInstructionText) + "\n\nOfficial fixed type profiles:\n" + strings.TrimSpace(sbtiTypeProfilesText)
}

func buildRendererAgent() agent.Agent {
	generationConfig := model.GenerationConfig{
		MaxTokens:       intPtr(32768),
		Temperature:     floatPtr(1.0),
		ReasoningEffort: stringPtr("medium"),
		Stream:          *isStream,
	}
	return llmagent.New(
		rendererAgentName,
		llmagent.WithModel(openai.New(*rendererModelName)),
		llmagent.WithDescription("Consumes the latest director state object from the session output key and renders the final A2UI output."),
		llmagent.WithInstruction(localRendererInstructionText()),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithPlanner(a2ui.New()),
	)
}

func localRendererInstructionText() string {
	return strings.ReplaceAll(sbtiRenderInstructionText, platformOutputTextKey, "{"+directorStateOutputKey+"?}")
}

func directorOutputSchemaMap() (map[string]any, error) {
	var schema map[string]any
	if err := json.Unmarshal([]byte(directorOutputSchemaText), &schema); err != nil {
		return nil, fmt.Errorf("parse director output schema: %w", err)
	}
	return schema, nil
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}

func stringPtr(v string) *string {
	return &v
}
