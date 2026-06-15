//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mragent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memorytool "trpc.group/trpc-go/trpc-agent-go/memory/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	nodeInit          = "init_state"
	nodeAbsorbRewrite = "absorb_rewrite"
	nodePrepareRecon  = "prepare_reconstruct"
	nodeDecideRecon   = "decide_reconstruct"
	nodeAbsorbTools   = "absorb_tools"
	nodePreparePrune  = "prepare_prune"
	nodeAbsorbPrune   = "absorb_prune"
	nodePrepareAnswer = "prepare_answer"
)

type toolNodeResponse struct {
	ToolID   string          `json:"tool_id"`
	ToolName string          `json:"tool_name"`
	Output   json.RawMessage `json:"output"`
}

func initStateNode(cfg Options) graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		question := questionFromState(state)
		if strings.TrimSpace(question) == "" {
			return nil, fmt.Errorf("mragent: user question is required")
		}
		budget := Budget{
			MaxToolRounds: cfg.MaxToolRounds,
			MaxCues:       cfg.MaxCues,
			MaxPaths:      cfg.MaxPaths,
		}
		return graph.State{
			StateKeyQuestion:      question,
			StateKeyBudget:        budget,
			StateKeyRouteDecision: RouteDecision{Next: routeReconstruct, Reason: "initialize reconstruction state"},
			graph.StateKeyMessages: []model.Message{
				model.NewUserMessage(question),
			},
			graph.StateKeyOneShotMessagesByNode: map[string][]model.Message{
				nodeRewrite: {
					model.NewUserMessage(buildRewritePrompt(question)),
				},
			},
		}, nil
	}
}

func absorbRewriteNode() graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		rewrite := strings.TrimSpace(stateString(state, graph.StateKeyLastResponse))
		question := stateString(state, StateKeyQuestion)
		if rewrite == "" {
			rewrite = question
		}
		return graph.State{
			StateKeyRewrite: rewrite,
			StateKeyRouteDecision: RouteDecision{
				Next:   routeReconstruct,
				Reason: "rewrite captured",
			},
		}, nil
	}
}

func prepareReconstructNode() graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		prompt := buildReconstructPrompt(state)
		return graph.SetOneShotMessagesForNode(
			nodeReconstruct,
			[]model.Message{model.NewUserMessage(prompt)},
		), nil
	}
}

func decideReconstructNode() graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		budget := budgetFromState(state)
		decision := RouteDecision{
			Next:   routePrune,
			Reason: "model stopped requesting tools",
		}
		if hasToolCalls(state) {
			if budget.ToolRounds >= budget.MaxToolRounds {
				decision = RouteDecision{
					Next:            routePrune,
					Reason:          "tool-round budget exhausted before executing another tool round",
					BudgetExhausted: true,
				}
			} else {
				decision = RouteDecision{
					Next:   routeTools,
					Reason: "model requested reconstruction tools",
				}
			}
		}
		return graph.State{StateKeyRouteDecision: decision}, nil
	}
}

func absorbToolsNode() graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		cues := cuesFromState(state)
		tags := tagsFromState(state)
		paths := pathsFromState(state)
		evidence := evidenceFromState(state)
		budget := budgetFromState(state)

		responses := toolResponsesFromState(state, nodeTools)
		if len(responses) > 0 {
			budget.ToolRounds++
		}
		for _, response := range responses {
			switch response.ToolName {
			case memory.CueSearchToolName:
				var out memorytool.CueSearchResponse
				if json.Unmarshal(response.Output, &out) == nil {
					budget.CueSearches++
					cues = mergeCues(cues, out.Cues, budget.MaxCues)
				}
			case memory.TagExpandToolName:
				var out memorytool.TagExpandResponse
				if json.Unmarshal(response.Output, &out) == nil {
					budget.TagExpansions++
					tags = mergeTags(tags, out.Tags)
					paths = mergePaths(paths, out.Paths, budget.MaxPaths)
					evidence = mergeEvidence(evidence, evidenceFromPaths(out.Paths), budget.MaxPaths)
				}
			case memory.ContentLoadToolName:
				var out memorytool.ContentLoadResponse
				if json.Unmarshal(response.Output, &out) == nil {
					budget.ContentLoads++
					evidence = mergeEvidence(evidence, evidenceFromContents(out.Contents), budget.MaxPaths)
				}
			case SessionLoadToolName:
				budget.ContentLoads++
				evidence = mergeEvidence(evidence, evidenceFromSessionLoad(response.Output), budget.MaxPaths)
			}
		}

		decision := RouteDecision{
			Next:   routeReconstruct,
			Reason: "continue active reconstruction with updated state",
		}
		if budget.ToolRounds >= budget.MaxToolRounds {
			decision = RouteDecision{
				Next:            routePrune,
				Reason:          "tool-round budget exhausted",
				BudgetExhausted: true,
			}
		}
		if budget.MaxPaths > 0 && len(evidence) >= budget.MaxPaths {
			decision = RouteDecision{
				Next:           routePrune,
				Reason:         "evidence budget reached",
				EnoughEvidence: true,
			}
		}

		return graph.State{
			StateKeyActiveCues:    cues,
			StateKeyActiveTags:    tags,
			StateKeyVisitedPaths:  paths,
			StateKeyEvidence:      evidence,
			StateKeyBudget:        budget,
			StateKeyRouteDecision: decision,
		}, nil
	}
}

func preparePruneNode() graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		return graph.SetOneShotMessagesForNode(
			nodePrune,
			[]model.Message{model.NewUserMessage(buildPrunePrompt(state))},
		), nil
	}
}

func absorbPruneNode() graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		pruned := strings.TrimSpace(stateString(state, graph.StateKeyLastResponse))
		evidence := evidenceFromState(state)
		evaluations := make([]RelationEvaluation, 0, len(evidence))
		for _, ev := range evidence {
			evaluations = append(evaluations, RelationEvaluation{
				EvidenceID: ev.ID,
				Decision:   "candidate",
				Reason:     "reviewed by prune node",
				Score:      ev.Score,
			})
		}
		return graph.State{
			StateKeyRelationEvaluations: evaluations,
			StateKeyRouteDecision: RouteDecision{
				Next:   routeAnswer,
				Reason: "evidence pruned",
			},
			graph.StateKeyMetadata: map[string]any{
				"mragent_pruned_evidence": pruned,
			},
		}, nil
	}
}

func prepareAnswerNode() graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		return graph.SetOneShotMessagesForNode(
			nodeAnswer,
			[]model.Message{model.NewUserMessage(buildAnswerPrompt(state))},
		), nil
	}
}

func routeFromDecision(state graph.State, fallback string) string {
	decision, ok := graph.GetStateValue[RouteDecision](state, StateKeyRouteDecision)
	if !ok || decision.Next == "" {
		return fallback
	}
	return decision.Next
}

func questionFromState(state graph.State) string {
	if question := stateString(state, StateKeyQuestion); question != "" {
		return question
	}
	if input := stateString(state, graph.StateKeyUserInput); input != "" {
		return input
	}
	if msgs, ok := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages); ok {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == model.RoleUser && strings.TrimSpace(msgs[i].Content) != "" {
				return msgs[i].Content
			}
		}
	}
	return ""
}

func buildRewritePrompt(question string) string {
	return fmt.Sprintf(`Original question:
%s

Rewrite this into a compact memory reconstruction brief. Include likely cue phrases, entities, dates, locations, relations, and evidence requirements.`, question)
}

func buildReconstructPrompt(state graph.State) string {
	return fmt.Sprintf(`Question:
%s

Reconstruction brief:
%s

Current state:
%s

Use the available tools to improve this state. If enough evidence is already present, stop calling tools and return an evidence dossier.`, stateString(state, StateKeyQuestion), stateString(state, StateKeyRewrite), stateSnapshot(state))
}

func buildPrunePrompt(state graph.State) string {
	return fmt.Sprintf(`Question:
%s

Prune and evaluate these reconstructed CTC paths and evidence. Keep only evidence directly useful for answering.

%s`, stateString(state, StateKeyQuestion), stateSnapshot(state))
}

func buildAnswerPrompt(state graph.State) string {
	pruned := metadataString(state, "mragent_pruned_evidence")
	return fmt.Sprintf(`Question:
%s

Pruned evidence:
%s

Structured evidence state:
%s

Answer using only the reconstructed evidence. If evidence is insufficient, say what is missing.`, stateString(state, StateKeyQuestion), pruned, stateSnapshot(state))
}

func stateSnapshot(state graph.State) string {
	snapshot := struct {
		ActiveCues          []memory.Cue         `json:"active_cues"`
		ActiveTags          []memory.Tag         `json:"active_tags"`
		VisitedPaths        []memory.Path        `json:"visited_paths"`
		Evidence            []Evidence           `json:"evidence"`
		RouteDecision       RouteDecision        `json:"route_decision"`
		Budget              Budget               `json:"budget"`
		RelationEvaluations []RelationEvaluation `json:"relation_evaluations"`
	}{
		ActiveCues:          cuesFromState(state),
		ActiveTags:          tagsFromState(state),
		VisitedPaths:        pathsFromState(state),
		Evidence:            evidenceFromState(state),
		RouteDecision:       routeDecisionFromState(state),
		Budget:              budgetFromState(state),
		RelationEvaluations: relationEvaluationsFromState(state),
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}
