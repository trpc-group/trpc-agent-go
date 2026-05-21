//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// applyExtensionContributions merges extension contributions into
// options so downstream constructors see one unified callback chain
// and one unified extension-tool slice.
//
// Three contracts worth pinning down:
//
//   - User callbacks come first. Within each merged chain we keep
//     "user → extension" ordering so user code keeps the option
//     to short-circuit before extension callbacks run. This is
//     intentionally different from runner-scoped plugins: runner
//     plugins run before agent-local callbacks because they are
//     cross-cutting hooks applied by the runner; extensions are
//     folded into one LLMAgent, so LLMAgent keeps user-supplied
//     callbacks first and extension callbacks second. See
//     llmflow.runBeforeModelCallbacks for the runner ordering.
//
//   - Tools are cached on options.extensionContributedTools rather
//     than appended to options.Tools. registerTools then re-applies
//     them on every (re)build of the tool list, including the
//     AddToolSet / refreshToolsLocked hot-reload paths. Callbacks
//     are static once merged, but tool sets can be rebuilt at
//     runtime — both code paths must see the same set of extension
//     tools.
//
//   - Nil/empty contributions are a no-op. extension.Collect already
//     returns nil for empty input, so this helper does not need to
//     special-case "no extensions configured".
func applyExtensionContributions(options *Options, contrib *extension.Contributions) {
	if contrib == nil || options == nil {
		return
	}
	agentCallbacks := contrib.AgentCallbacks()
	if hasAgentContent(agentCallbacks) {
		options.AgentCallbacks = mergeAgentCallbacks(
			options.AgentCallbacks, agentCallbacks,
		)
	}
	modelCallbacks := contrib.ModelCallbacks()
	if hasModelContent(modelCallbacks) {
		options.ModelCallbacks = mergeModelCallbacks(
			options.ModelCallbacks, modelCallbacks,
		)
	}
	toolCallbacks := contrib.ToolCallbacks()
	if hasToolContent(toolCallbacks) {
		options.ToolCallbacks = mergeToolCallbacks(
			options.ToolCallbacks, toolCallbacks,
		)
	}
	options.extensionContributedTools = contrib.Tools()
}

// appendExtensionTools surfaces tools that agent-scoped extensions
// contributed via extension.Registry.Tools during Register.
//
// The cached slice on options.extensionContributedTools was
// populated once during New() (or rather, during
// applyExtensionContributions called from New). This function is
// cheap enough to call from every
// tool-surface builder (Tools, InvocationToolSurface, and friends),
// which keeps tools in sync across construction, per-invocation
// framework tools and the AddToolSet / refreshToolsLocked hot-reload
// paths.
//
// Extension tools are intentionally NOT folded into userToolNames:
// they behave like knowledge_search / workspace_exec — the model
// can call them, but UserTools / FilterTools should treat them as
// framework-managed. This is the simplest semantics that respects
// the existing "WithKnowledge / WithSkills auto-injection is
// framework, WithTools is user" rule.
//
// On a name collision with any tool already in allTools (user-
// registered or framework), the extension's copy is silently
// dropped. This protects the agent from a misbehaving extension
// that re-exports a name another tool already owns: most LLM
// providers reject two declarations for the same name, and
// silently overriding either side would be more surprising than
// skipping. Earlier entries win, matching the order tools were
// collected by each surface builder (user tools first, then
// framework, then extensions).
func appendExtensionTools(allTools []tool.Tool, options *Options) []tool.Tool {
	if options == nil || len(options.extensionContributedTools) == 0 {
		return allTools
	}
	taken := make(map[string]struct{}, len(allTools)+len(options.extensionContributedTools))
	for _, t := range allTools {
		if t == nil || t.Declaration() == nil {
			continue
		}
		taken[t.Declaration().Name] = struct{}{}
	}
	for _, t := range options.extensionContributedTools {
		if t == nil || t.Declaration() == nil {
			continue
		}
		name := t.Declaration().Name
		if _, dup := taken[name]; dup {
			continue
		}
		taken[name] = struct{}{}
		allTools = append(allTools, t)
	}
	return allTools
}

// mergeAgentCallbacks merges b on top of a, returning a fresh
// Callbacks chain. The merge order is "a then b" which, in the
// llmagent.New() callsite, becomes "user then extension" — user
// callbacks see the request first and keep the option to short-
// circuit before extension code runs. nil inputs short-circuit so
// the result is nil when neither side has content (lets downstream
// nil-check fast paths stay valid).
//
// The returned chain clones a before appending extension callbacks,
// preserving user-configured callback execution options such as
// WithContinueOnError / WithContinueOnResponse without mutating the
// caller-owned callback object.
func mergeAgentCallbacks(a, b *agent.Callbacks) *agent.Callbacks {
	bn := hasAgentContent(b)
	if !bn {
		return a
	}
	if a == nil {
		return b
	}
	out := a.Clone()
	for _, cb := range b.BeforeAgent {
		out.RegisterBeforeAgent(cb)
	}
	for _, cb := range b.AfterAgent {
		out.RegisterAfterAgent(cb)
	}
	return out
}

// mergeModelCallbacks mirrors mergeAgentCallbacks for model hooks.
// See its docstring for shared semantics.
func mergeModelCallbacks(a, b *model.Callbacks) *model.Callbacks {
	bn := hasModelContent(b)
	if !bn {
		return a
	}
	if a == nil {
		return b
	}
	out := a.Clone()
	for _, cb := range b.BeforeModel {
		out.RegisterBeforeModel(cb)
	}
	for _, cb := range b.AfterModel {
		out.RegisterAfterModel(cb)
	}
	return out
}

// mergeToolCallbacks mirrors mergeAgentCallbacks for tool hooks
// and additionally reconciles ToolResultMessages: at most one
// converter is meaningful per agent. When both sides set it, b
// wins (giving extensions the last word when the user did not opt
// out); otherwise whichever is non-nil. This matches how the rest
// of the framework treats ToolResultMessages as a single pluggable
// converter rather than a chain.
func mergeToolCallbacks(a, b *tool.Callbacks) *tool.Callbacks {
	bn := hasToolContent(b)
	if !bn {
		return a
	}
	if a == nil {
		return b
	}
	out := a.Clone()
	for _, cb := range b.BeforeTool {
		out.RegisterBeforeTool(cb)
	}
	for _, cb := range b.AfterTool {
		out.RegisterAfterTool(cb)
	}
	if b.ToolResultMessages != nil {
		out.ToolResultMessages = b.ToolResultMessages
	}
	return out
}

func hasAgentContent(c *agent.Callbacks) bool {
	return c != nil && (len(c.BeforeAgent) > 0 || len(c.AfterAgent) > 0)
}

func hasModelContent(c *model.Callbacks) bool {
	return c != nil && (len(c.BeforeModel) > 0 || len(c.AfterModel) > 0)
}

func hasToolContent(c *tool.Callbacks) bool {
	return c != nil &&
		(len(c.BeforeTool) > 0 || len(c.AfterTool) > 0 ||
			c.ToolResultMessages != nil)
}
