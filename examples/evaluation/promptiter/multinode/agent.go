//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	sportsRecapAgentName  = "promptiter-sports-recap-agent"
	headlineAgentName     = "headline_agent"
	highlightsAgentName   = "highlights_agent"
	statsAngleAgentName   = "stats_angle_agent"
	recapWriterAgentName  = "recap_writer"
	sportsEditorAgentName = "sports_editor"
	nodePrepareGameInput  = "prepare_game_input"
	nodeJoinRecapParts    = "join_recap_parts"
	keyHeadlineInput      = "headline_input"
	keyHighlightsInput    = "highlights_input"
	keyStatsAngleInput    = "stats_angle_input"
	keyHeadline           = "headline"
	keyHighlights         = "highlights"
	keyStatsAngle         = "stats_angle"
	keyWriterInput        = "writer_input"
	keyEditorInput        = "editor_input"
	keyFinalRecap         = "final_recap"
)

func newSportsRecapAgent(m model.Model) (agent.Agent, error) {
	cfg := model.GenerationConfig{
		MaxTokens:   intPtr(32768),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	subAgents := []agent.Agent{
		newStageAgent(headlineAgentName, headlineInstruction, m, cfg),
		newStageAgent(highlightsAgentName, highlightsInstruction, m, cfg),
		newStageAgent(statsAngleAgentName, statsAngleInstruction, m, cfg),
		newStageAgent(recapWriterAgentName, recapWriterInstruction, m, cfg),
		newStageAgent(sportsEditorAgentName, sportsEditorInstruction, m, cfg),
	}
	g, err := buildSportsRecapGraph()
	if err != nil {
		return nil, fmt.Errorf("build graph: %w", err)
	}
	return graphagent.New(
		sportsRecapAgentName,
		g,
		graphagent.WithDescription("PromptIter multinode sports recap example."),
		graphagent.WithSubAgents(subAgents),
	)
}

func newStageAgent(name string, instruction string, m model.Model, cfg model.GenerationConfig) agent.Agent {
	return llmagent.New(
		name,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(cfg),
	)
}

func buildSportsRecapGraph() (*graph.Graph, error) {
	sg := graph.NewStateGraph(graph.NewStateSchema())
	sg.AddNode(nodePrepareGameInput, prepareGameInput)
	sg.AddAgentNode(headlineAgentName, branchOptions(keyHeadlineInput, keyHeadline)...)
	sg.AddAgentNode(highlightsAgentName, branchOptions(keyHighlightsInput, keyHighlights)...)
	sg.AddAgentNode(statsAngleAgentName, branchOptions(keyStatsAngleInput, keyStatsAngle)...)
	sg.AddNode(nodeJoinRecapParts, joinRecapParts)
	sg.AddAgentNode(
		recapWriterAgentName,
		graph.WithUserInputKey(keyWriterInput),
		graph.WithSubgraphIsolatedMessages(true),
		graph.WithSubgraphOutputMapper(storeDraft),
	)
	sg.AddAgentNode(
		sportsEditorAgentName,
		graph.WithUserInputKey(keyEditorInput),
		graph.WithSubgraphIsolatedMessages(true),
		graph.WithSubgraphOutputMapper(storeFinal),
	)
	sg.SetEntryPoint(nodePrepareGameInput)
	sg.AddEdge(nodePrepareGameInput, headlineAgentName)
	sg.AddEdge(nodePrepareGameInput, highlightsAgentName)
	sg.AddEdge(nodePrepareGameInput, statsAngleAgentName)
	sg.AddJoinEdge([]string{headlineAgentName, highlightsAgentName, statsAngleAgentName}, nodeJoinRecapParts)
	sg.AddEdge(nodeJoinRecapParts, recapWriterAgentName)
	sg.AddEdge(recapWriterAgentName, sportsEditorAgentName)
	sg.SetFinishPoint(sportsEditorAgentName)
	return sg.Compile()
}

func branchOptions(inputKey string, outputKey string) []graph.Option {
	return []graph.Option{
		graph.WithUserInputKey(inputKey),
		graph.WithSubgraphIsolatedMessages(true),
		graph.WithSubgraphOutputMapper(func(parent graph.State, result graph.SubgraphResult) graph.State {
			return graph.State{outputKey: strings.TrimSpace(result.LastResponse)}
		}),
	}
}

func prepareGameInput(ctx context.Context, state graph.State) (any, error) {
	gameJSON, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
	if strings.TrimSpace(gameJSON) == "" {
		return nil, errors.New("game input is empty")
	}
	return graph.State{
		keyHeadlineInput:   gameJSON,
		keyHighlightsInput: gameJSON,
		keyStatsAngleInput: gameJSON,
	}, nil
}

func joinRecapParts(ctx context.Context, state graph.State) (any, error) {
	gameJSON, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
	headline, _ := graph.GetStateValue[string](state, keyHeadline)
	highlights, _ := graph.GetStateValue[string](state, keyHighlights)
	statsAngle, _ := graph.GetStateValue[string](state, keyStatsAngle)
	if strings.TrimSpace(headline) == "" || strings.TrimSpace(highlights) == "" || strings.TrimSpace(statsAngle) == "" {
		return nil, errors.New("recap parts are incomplete")
	}
	brief := strings.Join([]string{
		"GAME_JSON:",
		gameJSON,
		"HEADLINE:",
		headline,
		"HIGHLIGHTS:",
		highlights,
		"STATS_ANGLE:",
		statsAngle,
	}, "\n\n")
	return graph.State{keyWriterInput: brief}, nil
}

func storeDraft(parent graph.State, result graph.SubgraphResult) graph.State {
	gameJSON, _ := graph.GetStateValue[string](parent, graph.StateKeyUserInput)
	headline, _ := graph.GetStateValue[string](parent, keyHeadline)
	highlights, _ := graph.GetStateValue[string](parent, keyHighlights)
	statsAngle, _ := graph.GetStateValue[string](parent, keyStatsAngle)
	draft := strings.TrimSpace(result.LastResponse)
	return graph.State{
		keyEditorInput: strings.Join([]string{
			"GAME_JSON:",
			gameJSON,
			"HEADLINE:",
			headline,
			"HIGHLIGHTS:",
			highlights,
			"STATS_ANGLE:",
			statsAngle,
			"DRAFT:",
			draft,
		}, "\n\n"),
	}
}

func storeFinal(parent graph.State, result graph.SubgraphResult) graph.State {
	final := strings.TrimSpace(result.LastResponse)
	return graph.State{
		keyFinalRecap:              final,
		graph.StateKeyLastResponse: final,
	}
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

const headlineInstruction = `生成标题。`

const highlightsInstruction = `提取比赛高光。`

const statsAngleInstruction = `选择数据角度。`

const recapWriterInstruction = `生成中文战报。`

const sportsEditorInstruction = `润色中文战报。`
