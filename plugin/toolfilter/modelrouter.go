//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolfilter

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"

	systemoneclient "trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

// routeQuestionID is the single question ID of a routing decision.
const routeQuestionID = "model_route"

// ModelRouter selects the model of each run with one Choice decision.
// Construct it with NewModelRouter and pass Select to
// agent.WithModelSelector (run option) or llmagent.WithModelSelector
// (agent option). The selection is cached per invocation, so a ReAct loop
// pays for one decision per run.
type ModelRouter struct {
	client     *systemoneclient.Client
	candidates []modelCandidate
	byName     map[string]model.Model
	fallback   model.Model
	cache      *runCache[model.Model]
}

// NewModelRouter creates a model router backed by the System One decision
// API at baseURL. Candidates are registered with WithModelCandidate and must
// be at least one, with nonblank unique names and non-nil models.
// WithFallbackModel sets the model returned on any routing failure; when it
// is not configured, routing failures keep the invocation's base model.
// It returns an error for an invalid endpoint, invalid option values, or an
// invalid candidate table.
func NewModelRouter(baseURL string, opts ...Option) (*ModelRouter, error) {
	o := applyOptions(opts)
	client, err := newSystemOneClient(baseURL, o)
	if err != nil {
		return nil, err
	}
	if len(o.candidates) == 0 {
		return nil, fmt.Errorf("toolfilter: at least one WithModelCandidate is required")
	}
	candidates := make([]modelCandidate, 0, len(o.candidates))
	byName := make(map[string]model.Model, len(o.candidates))
	for _, cand := range o.candidates {
		name := strings.TrimSpace(cand.name)
		if name == "" {
			return nil, fmt.Errorf("toolfilter: model candidate name must not be blank")
		}
		if cand.m == nil {
			return nil, fmt.Errorf("toolfilter: model candidate %q requires a non-nil model", name)
		}
		if _, dup := byName[name]; dup {
			return nil, fmt.Errorf("toolfilter: duplicate model candidate name %q", name)
		}
		cand.name = name
		cand.criteria = strings.TrimSpace(cand.criteria)
		candidates = append(candidates, cand)
		byName[name] = cand.m
	}
	return &ModelRouter{
		client:     client,
		candidates: candidates,
		byName:     byName,
		fallback:   o.fallback,
		cache:      newRunCache[model.Model](defaultCacheSize),
	}, nil
}

// Select implements agent.ModelSelector. It never returns an error: a
// missing user message, a decision failure, or an unknown routed model falls
// back to the WithFallbackModel instance, or — when no fallback is
// configured — returns nil so the invocation's base model is kept. Concurrent
// calls for an invocation share a decision controlled by the first caller's
// context. A canceled waiter returns the fallback without caching it.
func (r *ModelRouter) Select(
	ctx context.Context,
	inv *agent.Invocation,
) (model.Model, error) {
	if inv == nil || inv.InvocationID == "" {
		return r.fallback, nil
	}
	m, ok := r.cache.getOrCompute(ctx, inv.InvocationID, func() (model.Model, bool) {
		return r.route(ctx, inv), true
	})
	if !ok {
		return r.fallback, nil
	}
	return m, nil
}

// route makes the Choice decision for one run.
func (r *ModelRouter) route(ctx context.Context, inv *agent.Invocation) model.Model {
	if inv == nil || conversationText(inv.Message) == "" {
		// No user intent to route on: keep the fallback (or base model).
		return r.fallback
	}
	userMessage, _ := decisionConversation(inv)

	// Registration order is preserved: compatible servers may use
	// positional input.
	options := make([]systemoneclient.ChoiceOption, 0, len(r.candidates))
	for _, cand := range r.candidates {
		options = append(options, systemoneclient.ChoiceOption{
			Name:        cand.name,
			Description: cand.criteria,
		})
	}
	resp, err := r.client.SystemOne(ctx, &systemoneclient.Request{
		State: clipRunes(userMessage, stateClipLen),
		Questions: map[string]systemoneclient.Question{
			routeQuestionID: systemoneclient.ChoiceQuestion{
				Instructions: "Select the model best suited for this user request.",
				Options:      options,
			},
		},
	})
	if err != nil {
		log.WarnfContext(ctx, "toolfilter: model routing failed, using fallback: %v", err)
		return r.fallback
	}
	answer, err := resp.Choice(routeQuestionID)
	if err != nil {
		log.WarnfContext(ctx, "toolfilter: model routing answer invalid, using fallback: %v", err)
		return r.fallback
	}
	m, ok := r.byName[answer.Choice]
	if !ok {
		log.WarnfContext(
			ctx, "toolfilter: routed model %q is not registered, using fallback", answer.Choice)
		return r.fallback
	}
	return m
}
