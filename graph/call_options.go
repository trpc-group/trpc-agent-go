//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const graphCallOptionsKey = "graph_call_options"

// NodePath identifies a node inside nested subgraphs.
//
// Each segment is a node ID. For example:
//   - {"child"} targets the "child" node on the current graph.
//   - {"child", "llm"} targets the "llm" node inside the "child" subgraph.
type NodePath []string

// CallOption configures per-invocation graph call options.
//
// CallOption is intentionally closed to external implementations.
type CallOption func(*callOptions)

// WithCallOptions sets graph call options for this run.
//
// Call options are stored on the invocation and will be propagated into
// nested GraphAgent subgraphs automatically.
func WithCallOptions(opts ...CallOption) agent.RunOption {
	built := newCallOptions(opts...)
	return func(runOpts *agent.RunOptions) {
		if runOpts == nil || built == nil {
			return
		}
		copied := copyCustomAgentConfigs(runOpts.CustomAgentConfigs)
		existing, _ := copied[graphCallOptionsKey].(*callOptions)
		if existing == nil {
			copied[graphCallOptionsKey] = built
		} else {
			copied[graphCallOptionsKey] = mergeCallOptions(existing, built)
		}
		runOpts.CustomAgentConfigs = copied
	}
}

// WithCallGenerationConfigPatch applies a GenerationConfigPatch to all LLM
// nodes in the current graph scope.
func WithCallGenerationConfigPatch(p model.GenerationConfigPatch) CallOption {
	return func(c *callOptions) {
		if c == nil {
			return
		}
		c.generation = mergeGenPatch(c.generation, p)
	}
}

// WithCallResumeStateOverrideKeys preserves caller-provided runtime state values
// for the selected keys when resuming from a checkpoint.
//
// When one of these keys is present in RunOptions.RuntimeState for the current
// graph scope, its value overrides the checkpoint-restored value during resume.
// Keys filtered by the existing resume merge logic are still ignored.
func WithCallResumeStateOverrideKeys(keys ...string) CallOption {
	return func(c *callOptions) {
		if c == nil || len(keys) == 0 {
			return
		}
		c.resumeStateOverrideKeys = mergeStringSet(
			c.resumeStateOverrideKeys,
			newStringSet(keys...),
		)
	}
}

// DesignateNode applies options to a specific node in the current graph
// scope.
//
// For agent/subgraph nodes, options are applied to the child invocation and
// therefore affect the nested graph.
func DesignateNode(nodeID string, opts ...CallOption) CallOption {
	return func(c *callOptions) {
		if c == nil || nodeID == "" {
			return
		}
		node := c.ensureNode(nodeID)
		scope := newCallOptions(opts...)
		if scope == nil {
			return
		}
		node.generation = mergeGenPatch(node.generation, scope.generation)
		if len(scope.resumeStateOverrideKeys) > 0 || len(scope.nodes) > 0 {
			if node.child == nil {
				node.child = &callOptions{}
			}
			node.child.resumeStateOverrideKeys = mergeStringSet(
				node.child.resumeStateOverrideKeys,
				scope.resumeStateOverrideKeys,
			)
			node.child.nodes = mergeCallNodes(node.child.nodes, scope.nodes)
		}
	}
}

// DesignateNodeWithPath applies options to a node inside nested subgraphs.
func DesignateNodeWithPath(path NodePath, opts ...CallOption) CallOption {
	cloned := append(NodePath(nil), path...)
	return func(c *callOptions) {
		if c == nil || len(cloned) == 0 {
			return
		}
		cur := c
		for i, seg := range cloned {
			if seg == "" {
				return
			}
			node := cur.ensureNode(seg)
			last := i == len(cloned)-1
			if last {
				DesignateNode(seg, opts...)(cur)
				return
			}
			if node.child == nil {
				node.child = &callOptions{}
			}
			cur = node.child
		}
	}
}

type callOptions struct {
	generation              model.GenerationConfigPatch
	resumeStateOverrideKeys map[string]struct{}
	nodes                   map[string]*callNodeOptions
}

type callNodeOptions struct {
	generation model.GenerationConfigPatch
	child      *callOptions
}

func newCallOptions(opts ...CallOption) *callOptions {
	if len(opts) == 0 {
		return nil
	}
	out := &callOptions{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(out)
	}
	if out.isEmpty() {
		return nil
	}
	return out
}

func (c *callOptions) isEmpty() bool {
	if c == nil {
		return true
	}
	if !isEmptyGenPatch(c.generation) {
		return false
	}
	if len(c.resumeStateOverrideKeys) > 0 {
		return false
	}
	return len(c.nodes) == 0
}

func (c *callOptions) ensureNode(nodeID string) *callNodeOptions {
	if c.nodes == nil {
		c.nodes = make(map[string]*callNodeOptions)
	}
	if n := c.nodes[nodeID]; n != nil {
		return n
	}
	n := &callNodeOptions{}
	c.nodes[nodeID] = n
	return n
}

func mergeCallOptions(a, b *callOptions) *callOptions {
	if a == nil {
		return cloneCallOptions(b)
	}
	if b == nil {
		return cloneCallOptions(a)
	}
	out := &callOptions{
		generation: mergeGenPatch(a.generation, b.generation),
		resumeStateOverrideKeys: mergeStringSet(
			a.resumeStateOverrideKeys,
			b.resumeStateOverrideKeys,
		),
		nodes: mergeCallNodes(a.nodes, b.nodes),
	}
	if out.isEmpty() {
		return nil
	}
	return out
}

func mergeCallNodes(
	a map[string]*callNodeOptions,
	b map[string]*callNodeOptions,
) map[string]*callNodeOptions {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]*callNodeOptions, len(a)+len(b))
	for k, v := range a {
		if v == nil {
			continue
		}
		if cloned := cloneCallNodeOptions(v); cloned != nil {
			out[k] = cloned
		}
	}
	for k, v := range b {
		if v == nil {
			continue
		}
		if existing := out[k]; existing != nil {
			existing.generation = mergeGenPatch(existing.generation, v.generation)
			existing.child = mergeCallOptions(existing.child, v.child)
			out[k] = existing
			continue
		}
		if cloned := cloneCallNodeOptions(v); cloned != nil {
			out[k] = cloned
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneCallOptions(in *callOptions) *callOptions {
	if in == nil {
		return nil
	}
	out := &callOptions{
		generation:              cloneGenPatch(in.generation),
		resumeStateOverrideKeys: cloneStringSet(in.resumeStateOverrideKeys),
		nodes:                   cloneCallNodeMap(in.nodes),
	}
	if out.isEmpty() {
		return nil
	}
	return out
}

func cloneCallNodeMap(
	in map[string]*callNodeOptions,
) map[string]*callNodeOptions {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*callNodeOptions, len(in))
	for k, v := range in {
		if v == nil {
			continue
		}
		if cloned := cloneCallNodeOptions(v); cloned != nil {
			out[k] = cloned
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneCallNodeOptions(in *callNodeOptions) *callNodeOptions {
	if in == nil {
		return nil
	}
	out := &callNodeOptions{
		generation: cloneGenPatch(in.generation),
		child:      cloneCallOptions(in.child),
	}
	if isEmptyGenPatch(out.generation) && out.child == nil {
		return nil
	}
	return out
}

func isEmptyGenPatch(p model.GenerationConfigPatch) bool {
	return p.MaxTokens == nil &&
		p.Temperature == nil &&
		p.TopP == nil &&
		p.Stream == nil &&
		p.Stop == nil &&
		p.PresencePenalty == nil &&
		p.FrequencyPenalty == nil &&
		p.ReasoningEffort == nil &&
		p.ThinkingEnabled == nil &&
		p.ThinkingTokens == nil
}

func cloneGenPatch(
	p model.GenerationConfigPatch,
) model.GenerationConfigPatch {
	out := p
	if p.Stop != nil {
		out.Stop = append([]string{}, p.Stop...)
	}
	return out
}

func mergeGenPatch(
	base model.GenerationConfigPatch,
	override model.GenerationConfigPatch,
) model.GenerationConfigPatch {
	out := cloneGenPatch(base)
	if override.MaxTokens != nil {
		out.MaxTokens = override.MaxTokens
	}
	if override.Temperature != nil {
		out.Temperature = override.Temperature
	}
	if override.TopP != nil {
		out.TopP = override.TopP
	}
	if override.Stream != nil {
		out.Stream = override.Stream
	}
	if override.Stop != nil {
		out.Stop = append([]string{}, override.Stop...)
	}
	if override.PresencePenalty != nil {
		out.PresencePenalty = override.PresencePenalty
	}
	if override.FrequencyPenalty != nil {
		out.FrequencyPenalty = override.FrequencyPenalty
	}
	if override.ReasoningEffort != nil {
		out.ReasoningEffort = override.ReasoningEffort
	}
	if override.ThinkingEnabled != nil {
		out.ThinkingEnabled = override.ThinkingEnabled
	}
	if override.ThinkingTokens != nil {
		out.ThinkingTokens = override.ThinkingTokens
	}
	return out
}

func copyCustomAgentConfigs(in map[string]any) map[string]any {
	if in == nil {
		return make(map[string]any)
	}
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func newStringSet(keys ...string) map[string]struct{} {
	if len(keys) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneStringSet(in map[string]struct{}) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for key := range in {
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeStringSet(
	a map[string]struct{},
	b map[string]struct{},
) map[string]struct{} {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := cloneStringSet(a)
	if out == nil && len(b) > 0 {
		out = make(map[string]struct{}, len(b))
	}
	for key := range b {
		if key == "" {
			continue
		}
		out[key] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func graphCallOptionsFromConfigs(cfgs map[string]any) *callOptions {
	if cfgs == nil {
		return nil
	}
	if v, ok := cfgs[graphCallOptionsKey]; ok {
		if opts, ok := v.(*callOptions); ok {
			return opts
		}
		if opts, ok := v.(callOptions); ok {
			return cloneCallOptions(&opts)
		}
	}
	return nil
}

func withScopedGraphCallOptions(
	cfgs map[string]any,
	nodeID string,
) map[string]any {
	parent := graphCallOptionsFromConfigs(cfgs)
	scoped := scopeCallOptionsForSubgraph(parent, nodeID)
	if parent == nil && scoped == nil {
		return cfgs
	}
	out := copyCustomAgentConfigs(cfgs)
	if scoped == nil {
		delete(out, graphCallOptionsKey)
	} else {
		out[graphCallOptionsKey] = scoped
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scopeCallOptionsForSubgraph(
	parent *callOptions,
	nodeID string,
) *callOptions {
	if parent == nil {
		return nil
	}
	if nodeID == "" {
		return cloneCallOptions(parent)
	}
	merged := cloneGenPatch(parent.generation)
	resumeStateOverrideKeys := cloneStringSet(parent.resumeStateOverrideKeys)
	var childNodes map[string]*callNodeOptions
	if parent.nodes != nil {
		if node := parent.nodes[nodeID]; node != nil {
			merged = mergeGenPatch(merged, node.generation)
			if node.child != nil {
				merged = mergeGenPatch(merged, node.child.generation)
				resumeStateOverrideKeys = mergeStringSet(
					resumeStateOverrideKeys,
					node.child.resumeStateOverrideKeys,
				)
				childNodes = node.child.nodes
			}
		}
	}
	out := &callOptions{
		generation:              merged,
		resumeStateOverrideKeys: resumeStateOverrideKeys,
		nodes:                   childNodes,
	}
	if out.isEmpty() {
		return nil
	}
	return out
}

func generationPatchForNode(
	opts *callOptions,
	nodeID string,
) model.GenerationConfigPatch {
	if opts == nil {
		return model.GenerationConfigPatch{}
	}
	out := cloneGenPatch(opts.generation)
	if nodeID == "" {
		return out
	}
	if opts.nodes == nil {
		return out
	}
	if node := opts.nodes[nodeID]; node != nil {
		out = mergeGenPatch(out, node.generation)
	}
	return out
}
