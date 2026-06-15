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
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

const (
	// StateKeyQuestion stores the original user question for reconstruction.
	StateKeyQuestion = "mragent_question"
	// StateKeyRewrite stores the rewritten reconstruction brief.
	StateKeyRewrite = "mragent_rewrite"
	// StateKeyActiveCues stores currently active cue nodes.
	StateKeyActiveCues = "active_cues"
	// StateKeyActiveTags stores currently active tag nodes.
	StateKeyActiveTags = "active_tags"
	// StateKeyVisitedPaths stores traversed cue-tag-content paths.
	StateKeyVisitedPaths = "visited_paths"
	// StateKeyEvidence stores deduplicated reconstructed evidence.
	StateKeyEvidence = "evidence"
	// StateKeyRouteDecision stores the latest state-machine route decision.
	StateKeyRouteDecision = "route_decision"
	// StateKeyBudget stores reconstruction budget counters.
	StateKeyBudget = "budget"
	// StateKeyRelationEvaluations stores relation judgments over evidence.
	StateKeyRelationEvaluations = "relation_evaluations"
)

const (
	routeTools       = "tools"
	routeReconstruct = "reconstruct"
	routePrune       = "prune"
	routeAnswer      = "answer"
)

// Budget tracks bounded active memory reconstruction work.
type Budget struct {
	MaxToolRounds int `json:"max_tool_rounds"`
	MaxCues       int `json:"max_cues"`
	MaxPaths      int `json:"max_paths"`
	ToolRounds    int `json:"tool_rounds"`
	CueSearches   int `json:"cue_searches"`
	TagExpansions int `json:"tag_expansions"`
	ContentLoads  int `json:"content_loads"`
}

// Exhausted reports whether the tool-loop budget is exhausted.
func (b Budget) Exhausted() bool {
	if b.MaxToolRounds > 0 && b.ToolRounds >= b.MaxToolRounds {
		return true
	}
	return false
}

// RouteDecision records the state-machine routing decision after each stage.
type RouteDecision struct {
	Next            string `json:"next"`
	Reason          string `json:"reason,omitempty"`
	EnoughEvidence  bool   `json:"enough_evidence,omitempty"`
	BudgetExhausted bool   `json:"budget_exhausted,omitempty"`
}

// Evidence is a production state entry reconstructed from CTC paths.
type Evidence struct {
	ID        string                     `json:"id"`
	ContentID string                     `json:"content_id,omitempty"`
	Text      string                     `json:"text"`
	Cue       string                     `json:"cue,omitempty"`
	Tag       string                     `json:"tag,omitempty"`
	Score     float64                    `json:"score,omitempty"`
	Ref       memory.ContentRef          `json:"ref,omitempty"`
	Metadata  memory.AssociationMetadata `json:"metadata,omitempty"`
}

// RelationEvaluation records whether evidence is useful for the question.
type RelationEvaluation struct {
	EvidenceID string  `json:"evidence_id,omitempty"`
	Decision   string  `json:"decision,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	Score      float64 `json:"score,omitempty"`
}

func stateSchema() *graph.StateSchema {
	schema := graph.MessagesStateSchema()
	schema.AddField(StateKeyQuestion, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField(StateKeyRewrite, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField(StateKeyActiveCues, graph.StateField{
		Type:    reflect.TypeOf([]memory.Cue{}),
		Reducer: graph.DefaultReducer,
		Default: func() any { return []memory.Cue{} },
	})
	schema.AddField(StateKeyActiveTags, graph.StateField{
		Type:    reflect.TypeOf([]memory.Tag{}),
		Reducer: graph.DefaultReducer,
		Default: func() any { return []memory.Tag{} },
	})
	schema.AddField(StateKeyVisitedPaths, graph.StateField{
		Type:    reflect.TypeOf([]memory.Path{}),
		Reducer: graph.DefaultReducer,
		Default: func() any { return []memory.Path{} },
	})
	schema.AddField(StateKeyEvidence, graph.StateField{
		Type:    reflect.TypeOf([]Evidence{}),
		Reducer: graph.DefaultReducer,
		Default: func() any { return []Evidence{} },
	})
	schema.AddField(StateKeyRouteDecision, graph.StateField{
		Type:    reflect.TypeOf(RouteDecision{}),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField(StateKeyBudget, graph.StateField{
		Type:    reflect.TypeOf(Budget{}),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField(StateKeyRelationEvaluations, graph.StateField{
		Type:    reflect.TypeOf([]RelationEvaluation{}),
		Reducer: graph.DefaultReducer,
		Default: func() any { return []RelationEvaluation{} },
	})
	return schema
}
