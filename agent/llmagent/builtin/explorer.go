//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package builtin provides ready-to-use agent presets that plug into the
// standard llmagent sub-agent and AgentTool mechanisms.
//
// The Explorer preset is a read-only, exploration-oriented agent. It is a
// plain agent.Agent, so it can be mounted as a sub-agent for transfer_to_agent
// handoff:
//
//	root := llmagent.New("assistant",
//		llmagent.WithModel(m),
//		llmagent.WithTools(tools),
//		llmagent.WithSubAgents([]agent.Agent{builtin.NewExplorer()}),
//	)
//
// or wrapped by AgentTool for synchronous call-return delegation:
//
//	root := llmagent.New("assistant",
//		llmagent.WithModel(m),
//		llmagent.WithTools(append(tools, agenttool.NewTool(builtin.NewExplorer()))),
//	)
//
// In both cases the explorer derives its default capability surface from the
// direct parent invocation when one is available, because both mechanisms run
// the sub-agent on a clone of the parent invocation.
package builtin

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/awaitreply"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

// Default identity and prompt for the explorer preset.
const (
	// DefaultExplorerName is the default agent name advertised to the model.
	DefaultExplorerName = "explorer"

	// DefaultExplorerDescription is the default description advertised to the
	// model (via transfer_to_agent or the AgentTool declaration).
	DefaultExplorerDescription = "A read-only exploration agent. Use it to " +
		"investigate and gather facts from the available context, files, and " +
		"knowledge sources. It inspects and reports findings but does not " +
		"modify state, write data, or take destructive actions."

	// DefaultExplorerInstruction is the default read-only system instruction.
	//
	// read-only here is an advisory prompt constraint, not a permission
	// boundary: if the parent agent exposes mutating tools, the explorer may
	// still inherit them. Callers that need a hard boundary should narrow the
	// surface with WithToolFilter or WithTools.
	DefaultExplorerInstruction = "You are a read-only exploration agent.\n\n" +
		"Your job is to investigate and gather information using the tools " +
		"available to you, then return clear, concise findings.\n\n" +
		"Constraints:\n" +
		"- Treat your role as strictly read-only. Do not modify files, write " +
		"data, change configuration, or run destructive or state-changing " +
		"actions, even if a tool would allow it.\n" +
		"- Prefer searching, reading, and retrieving over acting.\n" +
		"- If a request would require changing state, report what you found " +
		"and what would need to happen, but do not perform the change.\n\n" +
		"Return a focused summary of what you discovered, citing the concrete " +
		"sources (files, search results, knowledge entries) you relied on."
)

// ExplorerOption configures the explorer preset.
type ExplorerOption func(*explorerConfig)

type explorerConfig struct {
	name         string
	description  string
	instruction  string
	tools        []tool.Tool
	toolsSet     bool
	toolFilter   tool.FilterFunc
	skills       skill.Repository
	skillsSet    bool
	mdl          model.Model
	codeExecutor codeexecutor.CodeExecutor
	llmOptions   []llmagent.Option
}

// WithName overrides the explorer's advertised name (default "explorer").
func WithName(name string) ExplorerOption {
	return func(c *explorerConfig) { c.name = name }
}

// WithDescription overrides the explorer's advertised description.
func WithDescription(description string) ExplorerOption {
	return func(c *explorerConfig) { c.description = description }
}

// WithInstruction overrides the explorer's read-only system instruction.
func WithInstruction(instruction string) ExplorerOption {
	return func(c *explorerConfig) { c.instruction = instruction }
}

// WithTools sets an explicit tool surface for the explorer.
//
// When provided, the explorer does NOT inherit the parent agent's user tools
// or knowledge surface; it uses exactly these tools (still narrowed by
// WithToolFilter if set). Inherited skills/code executor still apply unless
// overridden by WithSkills/WithCodeExecutor.
func WithTools(tools []tool.Tool) ExplorerOption {
	return func(c *explorerConfig) {
		c.tools = tools
		c.toolsSet = true
	}
}

// WithToolFilter narrows the effective tool surface after inheritance (or
// after WithTools). It is the recommended way to enforce a hard read-only
// boundary, e.g. tool.NewIncludeToolNamesFilter("read_file", "search").
func WithToolFilter(filter tool.FilterFunc) ExplorerOption {
	return func(c *explorerConfig) { c.toolFilter = filter }
}

// WithSkills sets an explicit skill repository, replacing inheritance from the
// parent agent.
func WithSkills(repo skill.Repository) ExplorerOption {
	return func(c *explorerConfig) {
		c.skills = repo
		c.skillsSet = true
	}
}

// WithModel sets an explicit model. When unset, the explorer inherits the
// direct parent invocation's resolved model.
func WithModel(m model.Model) ExplorerOption {
	return func(c *explorerConfig) { c.mdl = m }
}

// WithCodeExecutor sets an explicit code executor, replacing inheritance from
// the parent agent.
func WithCodeExecutor(exec codeexecutor.CodeExecutor) ExplorerOption {
	return func(c *explorerConfig) { c.codeExecutor = exec }
}

// WithLLMAgentOptions is an advanced escape hatch that forwards extra options
// to the inner llmagent.LLMAgent built at run time. Use sparingly: these
// options are applied last and can override the explorer's defaults.
func WithLLMAgentOptions(opts ...llmagent.Option) ExplorerOption {
	return func(c *explorerConfig) {
		c.llmOptions = append(c.llmOptions, opts...)
	}
}

// explorer is an opaque agent.Agent that lazily builds an inner LLMAgent at
// run time. The lazy construction is required because the parent invocation
// (and thus the capability surface to inherit) only exists when Run is called,
// not when NewExplorer is called.
type explorer struct {
	cfg  explorerConfig
	info agent.Info
}

// Compile-time check that explorer satisfies agent.Agent.
var _ agent.Agent = (*explorer)(nil)

// NewExplorer returns a read-only exploration agent.Agent preset.
//
// By default it inherits the direct parent invocation's user tools, knowledge
// surface, skills, code executor, and model, and applies a read-only system
// instruction. See the package documentation and the With* options for
// details and overrides.
func NewExplorer(opts ...ExplorerOption) agent.Agent {
	cfg := explorerConfig{
		name:        DefaultExplorerName,
		description: DefaultExplorerDescription,
		instruction: DefaultExplorerInstruction,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.name == "" {
		cfg.name = DefaultExplorerName
	}
	if cfg.description == "" {
		cfg.description = DefaultExplorerDescription
	}
	if cfg.instruction == "" {
		cfg.instruction = DefaultExplorerInstruction
	}
	return &explorer{
		cfg: cfg,
		info: agent.Info{
			Name:        cfg.name,
			Description: cfg.description,
		},
	}
}

// Info implements agent.Agent. Name and description are static so the explorer
// can be advertised (via transfer_to_agent or AgentTool) before any inner
// agent is built.
func (e *explorer) Info() agent.Info { return e.info }

// Tools implements agent.Agent. The real surface is produced by the inner
// LLMAgent at run time, so the wrapper itself exposes none.
func (e *explorer) Tools() []tool.Tool { return nil }

// SubAgents implements agent.Agent. The explorer never owns sub-agents; this
// is also what structurally prevents transfer_to_agent recursion on the inner
// agent.
func (e *explorer) SubAgents() []agent.Agent { return nil }

// FindSubAgent implements agent.Agent.
func (e *explorer) FindSubAgent(string) agent.Agent { return nil }

// Run implements agent.Agent. It builds the inner LLMAgent from the direct
// parent invocation's capability surface, sanitizes the child invocation, and
// delegates execution to the inner agent.
func (e *explorer) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	if inv == nil {
		return nil, errors.New("builtin explorer: nil invocation")
	}
	inner, err := e.buildInner(ctx, inv)
	if err != nil {
		return nil, err
	}
	sanitizeChildInvocation(inv)
	// Hand identity to the inner agent (mirrors agent.NewLazyAgent). The inner
	// LLMAgent re-sets these in setupInvocation, but doing it here keeps the
	// invocation consistent for any observer between now and that point.
	inv.Agent = inner
	inv.AgentName = inner.Info().Name
	return inner.Run(ctx, inv)
}

// sanitizeChildInvocation strips run-scoped state that would otherwise leak
// back onto the inner agent's tool surface:
//   - ExternalTools / ExternalToolNames: caller-executed tools are re-appended
//     by the flow for any agent on this invocation; the explorer excludes them.
//   - ToolFilter: the parent's run-scoped filter was authored against the
//     parent's tool names and was already applied during inheritance; leaving
//     it would double-filter the explorer's (different) surface.
//
// AdditionalTools are intentionally preserved: they are run-scoped user tools
// and, under the default soft read-only semantics, the explorer inherits them.
func sanitizeChildInvocation(inv *agent.Invocation) {
	inv.RunOptions.ExternalTools = nil
	inv.RunOptions.ExternalToolNames = nil
	inv.RunOptions.ToolFilter = nil
}

// buildInner constructs the inner LLMAgent for one run, resolving each
// capability from explicit options first and inheriting from the direct parent
// invocation otherwise.
func (e *explorer) buildInner(
	ctx context.Context,
	inv *agent.Invocation,
) (agent.Agent, error) {
	parentInv := inv.GetParentInvocation()

	mdl := e.resolveModel(parentInv)
	if mdl == nil {
		return nil, errors.New(
			"builtin explorer: no model resolved; pass builtin.WithModel(...) " +
				"or mount the explorer under a model-bearing parent agent",
		)
	}

	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithInstruction(e.cfg.instruction),
		llmagent.WithTools(e.resolveTools(ctx, parentInv)),
	}
	if repo := e.resolveSkills(parentInv); repo != nil {
		opts = append(opts, llmagent.WithSkills(repo))
	}
	if exec := e.resolveCodeExecutor(parentInv); exec != nil {
		opts = append(opts, llmagent.WithCodeExecutor(exec))
	}
	// Knowledge is inherited (capability replay) only on the inherit-tools
	// path. An explicit WithTools means the caller takes full control of the
	// tool surface.
	if !e.cfg.toolsSet && parentInv != nil {
		if p, ok := parentInv.Agent.(invocationKnowledgeOptionsProvider); ok {
			opts = append(opts, p.InvocationKnowledgeOptions(parentInv)...)
		}
	}
	// Advanced escape hatch, applied last.
	opts = append(opts, e.cfg.llmOptions...)

	return llmagent.New(e.cfg.name, opts...), nil
}

func (e *explorer) resolveModel(parentInv *agent.Invocation) model.Model {
	if e.cfg.mdl != nil {
		return e.cfg.mdl
	}
	if parentInv != nil {
		return parentInv.Model
	}
	return nil
}

func (e *explorer) resolveTools(
	ctx context.Context,
	parentInv *agent.Invocation,
) []tool.Tool {
	if e.cfg.toolsSet {
		return applyToolFilter(ctx, e.cfg.tools, e.cfg.toolFilter)
	}
	return inheritParentUserTools(ctx, parentInv, e.cfg.toolFilter)
}

func (e *explorer) resolveSkills(parentInv *agent.Invocation) skill.Repository {
	if e.cfg.skillsSet {
		return e.cfg.skills
	}
	if parentInv == nil {
		return nil
	}
	if p, ok := parentInv.Agent.(invocationSkillRepositoryProvider); ok {
		return p.InvocationSkillRepository(parentInv)
	}
	return nil
}

func (e *explorer) resolveCodeExecutor(
	parentInv *agent.Invocation,
) codeexecutor.CodeExecutor {
	if e.cfg.codeExecutor != nil {
		return e.cfg.codeExecutor
	}
	if parentInv == nil {
		return nil
	}
	if p, ok := parentInv.Agent.(invocationCodeExecutorProvider); ok {
		return p.InvocationCodeExecutor(parentInv)
	}
	return nil
}

// inheritParentUserTools returns the parent invocation's user tools (the tools
// the parent registered via WithTools/WithToolSets/AdditionalTools), narrowed
// by an optional filter.
//
// It deliberately keeps only tools the parent classifies as user tools. All
// framework-injected tools (transfer_to_agent, knowledge, skill, workspace
// exec, await_user_reply, ...) are excluded here; the ones the explorer wants
// (knowledge/skills/workspace exec) are re-derived from the parent's
// capabilities instead, so they are bound to the child invocation rather than
// carrying the parent's runtime state.
func inheritParentUserTools(
	ctx context.Context,
	parentInv *agent.Invocation,
	filter tool.FilterFunc,
) []tool.Tool {
	if parentInv == nil || parentInv.Agent == nil {
		return nil
	}
	provider, ok := parentInv.Agent.(invocationToolSurfaceProvider)
	if !ok {
		return nil
	}
	surface, userToolNames := provider.InvocationToolSurface(ctx, parentInv)
	if len(surface) == 0 || len(userToolNames) == 0 {
		return nil
	}
	inherited := make([]tool.Tool, 0, len(userToolNames))
	for _, t := range surface {
		if t == nil || t.Declaration() == nil {
			continue
		}
		name := t.Declaration().Name
		if !userToolNames[name] {
			continue
		}
		if isExcludedFrameworkTool(name) {
			continue
		}
		if filter != nil && !filter(ctx, t) {
			continue
		}
		inherited = append(inherited, t)
	}
	return inherited
}

func applyToolFilter(
	ctx context.Context,
	tools []tool.Tool,
	filter tool.FilterFunc,
) []tool.Tool {
	if filter == nil {
		return tools
	}
	filtered := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		if t == nil || t.Declaration() == nil {
			continue
		}
		if filter(ctx, t) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// isExcludedFrameworkTool defends against a custom parent agent that classifies
// handoff/await tools as user tools. These must never be inherited: handoff
// would risk recursion and await would let the explorer take over the next
// user turn. (Under a standard LLMAgent parent these are not user tools and are
// already filtered out by the userToolNames check.)
func isExcludedFrameworkTool(name string) bool {
	switch name {
	case transfer.TransferToolName, awaitreply.ToolName:
		return true
	default:
		return false
	}
}

// Capability provider interfaces are defined locally (structural typing) so
// the builtin package does not require new public interface types in the agent
// package. *llmagent.LLMAgent satisfies all of them.
type invocationToolSurfaceProvider interface {
	InvocationToolSurface(
		ctx context.Context,
		inv *agent.Invocation,
	) ([]tool.Tool, map[string]bool)
}

type invocationSkillRepositoryProvider interface {
	InvocationSkillRepository(inv *agent.Invocation) skill.Repository
}

type invocationCodeExecutorProvider interface {
	InvocationCodeExecutor(inv *agent.Invocation) codeexecutor.CodeExecutor
}

type invocationKnowledgeOptionsProvider interface {
	InvocationKnowledgeOptions(inv *agent.Invocation) []llmagent.Option
}
