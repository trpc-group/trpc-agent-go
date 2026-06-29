//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolsurface"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

// DefaultDynamicToolName is the default tool name exposed to the model by
// NewDynamicTool. It is intentionally distinct from transfer_to_agent
// (control handoff) and the *_task_run tools (background tasks): it runs a
// short-lived sub-agent and returns its result synchronously.
const DefaultDynamicToolName = "dynamic_agent"

// defaultDynamicDescription is the default tool description exposed to the
// model. It pins the semantic boundary so the model does not confuse this with
// a transfer, a background task, or running a pre-registered sub-agent. It
// deliberately does not name the optional "tools"/"skills" fields (those are
// only present when exposed via the schema); it states the boundary as a fact
// that holds whether or not the model can narrow the surface.
const defaultDynamicDescription = "Run one short-lived sub-agent for a " +
	"single focused task and return its result. The sub-agent is created on the " +
	"fly for this call only and is destroyed afterward. It does NOT transfer " +
	"control, does NOT run a pre-registered agent by name, and does NOT start a " +
	"background task. To run several tasks, call this tool multiple times. Its " +
	"tools and skills stay within a code-defined capability boundary, which by " +
	"default is derived from what the current agent is already allowed to use " +
	"(or set explicitly in code), and it cannot select arbitrary agents, models, " +
	"or executors."

const (
	defaultRequestDescription = "The task for the sub-agent. Include all the " +
		"context it needs to complete the task on its own."
	defaultInstructionDescription = "Optional role, constraints, and " +
		"execution guidance for this sub-agent invocation. It acts as the " +
		"sub-agent's system prompt for this run."
	defaultToolsDescription = "Optional exact tool names this sub-agent may " +
		"use. Omit to allow all permitted tools."
	defaultSkillsDescription = "Optional exact skill names this sub-agent may " +
		"use. Omit to allow all permitted skills."
)

const (
	fieldRequest     = "request"
	fieldInstruction = "instruction"
	fieldTools       = "tools"
	fieldSkills      = "skills"
)

// skillRepository is a local alias so the dynamic option struct (declared in
// agent_tool.go) can reference a skill repository without that file importing
// the skill package directly.
type skillRepository = skill.Repository

// CapabilitySurfaceProvider returns the maximum capability surface (the set of
// tools the model may select from for a dynamic sub-agent invocation) together
// with the user-tool classification.
//
// It receives the parent invocation so a provider can derive the surface from
// the live parent context. The second return value maps user-tool names; tools
// not present in it are treated as framework-managed and are not selectable.
// When the map is nil all returned tools are treated as user tools.
type CapabilitySurfaceProvider func(
	ctx context.Context,
	parentInv *agent.Invocation,
) ([]tool.Tool, map[string]bool)

// CapabilitySkillsProvider returns the maximum skill repository the model may
// select from for one dynamic sub-agent invocation.
type CapabilitySkillsProvider func(
	ctx context.Context,
	parentInv *agent.Invocation,
) skill.Repository

// CapabilityUnavailableReason describes why a named capability exists in the
// system boundary but is not available for the current dynamic sub-agent call.
type CapabilityUnavailableReason string

const (
	// CapabilityUnavailableReasonUnknown is used when the provider does not
	// supply a more specific reason.
	CapabilityUnavailableReasonUnknown CapabilityUnavailableReason = "unknown"
	// CapabilityUnavailableReasonMissingCredential means a required credential
	// or token is not configured for this user, tenant, or runtime.
	CapabilityUnavailableReasonMissingCredential CapabilityUnavailableReason = "missing_credential"
	// CapabilityUnavailableReasonPermissionDenied means policy denied this
	// capability for the current user, tenant, or task.
	CapabilityUnavailableReasonPermissionDenied CapabilityUnavailableReason = "permission_denied"
	// CapabilityUnavailableReasonExecutorUnavailable means the required local,
	// remote, sandbox, or workflow executor is not available.
	CapabilityUnavailableReasonExecutorUnavailable CapabilityUnavailableReason = "executor_unavailable"
	// CapabilityUnavailableReasonNetworkDisabled means network access required
	// by the capability is disabled for this run.
	CapabilityUnavailableReasonNetworkDisabled CapabilityUnavailableReason = "network_disabled"
)

// UnavailableCapability records a named capability that should not be mounted
// into the child surface for this call, while still giving the parent model a
// concrete reason when it requested that capability.
type UnavailableCapability struct {
	Name   string
	Reason CapabilityUnavailableReason
	// Detail is included in the dynamic tool's model-visible warning note.
	// Keep it safe for prompt context: do not include secrets, credential
	// values, internal policy internals, or tenant-specific sensitive data.
	Detail string
}

// CapabilitySurface is the structured dynamic capability boundary returned by
// DetailedCapabilitySurfaceProvider.
type CapabilitySurface struct {
	// Tools is the maximum available tool surface.
	Tools []tool.Tool
	// UserToolNames classifies selectable user tools. Nil means every returned
	// tool is selectable unless excluded by framework rules.
	UserToolNames map[string]bool
	// ExternalToolNames marks caller-executed tools that must not be mounted
	// into the synchronous child invocation.
	ExternalToolNames map[string]bool
	// UnavailableTools names capabilities that are known but unavailable for
	// this call. They are not mounted. If requested, their reason is surfaced
	// to the parent model in the dynamic tool warning note.
	UnavailableTools []UnavailableCapability
}

// DetailedCapabilitySurfaceProvider returns a structured capability surface for
// dynamic sub-agent calls. Use it when the provider needs to distinguish
// "unknown tool" from "known but unavailable" and surface actionable reasons
// such as missing_credential or executor_unavailable.
type DetailedCapabilitySurfaceProvider func(
	ctx context.Context,
	parentInv *agent.Invocation,
) CapabilitySurface

// WithTemplateAgent sets the base/template agent that defines the execution
// boundary for the dynamic sub-agent (model, executor, callbacks, permission
// policy, max calls, ...).
//
// When omitted, the dynamic tool lazily derives the base agent from the parent
// invocation at call time. The model can never select an arbitrary agent; it
// can only run within this boundary.
func WithTemplateAgent(a agent.Agent) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().templateAgent = a
	}
}

// WithCapabilityProvider overrides how the maximum capability surface is
// resolved for a dynamic sub-agent. It is the legacy provider shape: it can
// return available tools, but not known-unavailable tools with reasons. Use
// WithCapabilitySurfaceProvider when the parent model needs actionable
// unavailable reasons. By default the surface is derived from the parent
// invocation's effective tool surface.
func WithCapabilityProvider(provider CapabilitySurfaceProvider) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().capabilityProvider = provider
	}
}

// WithCapabilitySurfaceProvider overrides how the maximum capability surface
// is resolved for a dynamic sub-agent and can also report known-but-unavailable
// capabilities with structured reasons. It takes precedence over
// WithCapabilityProvider, WithCapabilityTools, and the default parent-derived
// surface.
func WithCapabilitySurfaceProvider(provider DetailedCapabilitySurfaceProvider) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().capabilitySurfaceProvider = provider
	}
}

// WithCapabilityTools sets a fixed maximum tool surface for a dynamic
// sub-agent. The model may only select a subset of these tools. This takes
// precedence over the default parent-derived surface (but not over
// WithCapabilityProvider or WithCapabilitySurfaceProvider).
func WithCapabilityTools(tools []tool.Tool) Option {
	return func(opts *agentToolOptions) {
		cfg := opts.ensureDynamicOptions()
		cfg.capabilityTools = append([]tool.Tool(nil), tools...)
		cfg.capabilityToolsSet = true
	}
}

// WithCapabilityToolAliases maps model-facing aliases to canonical tool names
// when a dynamic sub-agent selects tools. Use this for stable runtime names,
// legacy names, or product names that users and models naturally mention but
// that differ from the actual tool declaration name. Aliases never create new
// capabilities; they only resolve to tools already present in the dynamic
// capability surface.
func WithCapabilityToolAliases(aliases map[string]string) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().toolAliases =
			normalizeToolAliases(aliases)
	}
}

// WithDynamicTimeout limits one dynamic sub-agent invocation. A non-positive
// timeout keeps the parent's context unchanged.
func WithDynamicTimeout(timeout time.Duration) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().timeout = timeout
	}
}

// WithCapabilitySkills sets a fixed maximum skill repository for a dynamic
// sub-agent. The model may only select a subset of these skills. This takes
// precedence over the default parent-derived skill repository.
func WithCapabilitySkills(repo skill.Repository) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().capabilitySkills = repo
	}
}

// WithCapabilitySkillsProvider overrides how the maximum skill repository is
// resolved for a dynamic sub-agent. By default the repository is derived from
// the parent invocation's effective skills. This takes precedence over
// WithCapabilitySkills.
func WithCapabilitySkillsProvider(provider CapabilitySkillsProvider) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().capabilitySkillProvider = provider
	}
}

// WithExposeToolSelection controls whether the dynamic tool exposes the
// "tools" field so the model can restrict the sub-agent's tools. Defaults to
// true. When disabled the sub-agent still receives the permitted tool surface
// (minus the dynamic tool itself), but the model cannot narrow it.
func WithExposeToolSelection(expose bool) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().exposeToolSelection = expose
	}
}

// WithExposeSkillSelection controls whether the dynamic tool exposes the
// "skills" field so the model can restrict the sub-agent's skills. Defaults to
// false because validating skill execution requirements is environment
// dependent.
func WithExposeSkillSelection(expose bool) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().exposeSkillSelection = expose
	}
}

// WithExposeInstruction controls whether the dynamic tool exposes the
// "instruction" field so the model can set a per-invocation role/system prompt
// for the sub-agent. Defaults to true.
func WithExposeInstruction(expose bool) Option {
	return func(opts *agentToolOptions) {
		opts.ensureDynamicOptions().exposeInstruction = expose
	}
}

// WithRequestDescription customizes the schema description of the "request"
// field for the dynamic tool.
func WithRequestDescription(description string) Option {
	return func(opts *agentToolOptions) {
		copied := description
		opts.ensureDynamicOptions().requestDescription = &copied
	}
}

// WithInstructionDescription customizes the schema description of the
// "instruction" field for the dynamic tool.
func WithInstructionDescription(description string) Option {
	return func(opts *agentToolOptions) {
		copied := description
		opts.ensureDynamicOptions().instructionDescription = &copied
	}
}

// WithToolsDescription customizes the schema description of the "tools" field
// for the dynamic tool.
func WithToolsDescription(description string) Option {
	return func(opts *agentToolOptions) {
		copied := description
		opts.ensureDynamicOptions().toolsDescription = &copied
	}
}

// WithSkillsDescription customizes the schema description of the "skills" field
// for the dynamic tool.
func WithSkillsDescription(description string) Option {
	return func(opts *agentToolOptions) {
		copied := description
		opts.ensureDynamicOptions().skillsDescription = &copied
	}
}

// NewDynamicTool creates a dynamic AgentTool: a single, code-defined entrypoint
// that runs a short-lived sub-agent whose capability surface (tools,
// skills, instruction) is selected per invocation within a safety boundary.
//
// The default behavior is to lazily derive the sub-agent from the parent
// invocation at call time:
//   - base agent: the parent invocation's agent (or WithTemplateAgent),
//   - tool surface: the parent invocation's effective user tools (minus this
//     tool, transfer_to_agent and any caller-executed external tools),
//     optionally narrowed by the model's "tools" field,
//   - skills: the parent invocation's effective skills, optionally narrowed by
//     the model's "skills" field,
//   - instruction: the model's "instruction" field (when exposed),
//   - context: isolated by default (HistoryScopeIsolated).
//
// The model cannot pick an arbitrary agent, model, or executor; it can only run
// within the configured boundary.
//
// Minimal usage exposes a tool named "dynamic_agent":
//
//	main := llmagent.New("main",
//	    llmagent.WithTools([]tool.Tool{agenttool.NewDynamicTool()}),
//	)
func NewDynamicTool(opts ...Option) *Tool {
	options := &agentToolOptions{
		historyScope: HistoryScopeIsolated,
		dynamic:      defaultDynamicOptions(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}

	name := DefaultDynamicToolName
	if options.name != nil && *options.name != "" {
		name = *options.name
	}
	if options.persistentHistory != nil && options.persistentHistory.enabled {
		log.Warnf(
			"AgentTool[%s]: WithPersistentHistory* is ignored by NewDynamicTool (not supported yet)",
			name,
		)
	}
	dynamicCfg := options.ensureDynamicOptions()
	description := buildDynamicDescription(dynamicCfg, options.historyScope)
	if options.description != nil {
		description = *options.description
	}

	return &Tool{
		agent:                  dynamicCfg.templateAgent,
		skipSummarization:      options.skipSummarization,
		streamInner:            options.streamInner,
		innerTextMode:          tool.NormalizeInnerTextMode(options.innerTextMode),
		structuredStreamErrors: options.structuredStreamErrors,
		historyScope:           options.historyScope,
		responseMode:           normalizeResponseMode(options.responseMode),
		name:                   name,
		description:            description,
		inputSchema:            buildDynamicInputSchema(name, dynamicCfg),
		outputSchema: &tool.Schema{
			Type:        "string",
			Description: "The sub-agent's final response.",
		},
		dynamic:    true,
		dynamicCfg: dynamicCfg,
	}
}

// buildDynamicDescription assembles the model-facing tool description: the
// fixed boundary statement, a context sentence matching the configured
// history scope, plus one short hint per per-call field that is
// actually exposed. The model learns it can configure instruction/tools/skills
// from the schema and these hints — not from the tool name — so the hints stay
// in lockstep with buildDynamicInputSchema and never advertise a field the
// schema omits.
func buildDynamicDescription(cfg *dynamicOptions, historyScope HistoryScope) string {
	var b strings.Builder
	b.WriteString(defaultDynamicDescription)
	if historyScope == HistoryScopeParentBranch {
		b.WriteString(" It can see the current conversation's history; still " +
			"describe the task in 'request'. Use it when delegated tool work " +
			"should run in a child invocation while continuing from the current " +
			"conversation.")
	} else {
		b.WriteString(" Use it for self-contained tool work, multiple " +
			"independent subtasks, or any task where delegating keeps the parent " +
			"conversation focused instead of filling it with tool details and " +
			"intermediate steps. It has no memory of this conversation, so put " +
			"everything it needs in 'request'.")
	}
	if cfg.exposeInstruction {
		b.WriteString(" Optionally set 'instruction' to give the sub-agent a " +
			"role or constraints for this run.")
	}
	if cfg.exposeToolSelection {
		b.WriteString(" Optionally set 'tools' to the exact subset of tool " +
			"names it may use; omit to allow all permitted tools.")
	}
	if cfg.exposeSkillSelection {
		b.WriteString(" Optionally set 'skills' to the exact subset of skill " +
			"names it may use; omit to allow all permitted skills.")
	}
	return b.String()
}

// buildDynamicInputSchema builds the JSON schema exposed to the model based on
// which fields are enabled.
func buildDynamicInputSchema(name string, cfg *dynamicOptions) *tool.Schema {
	properties := map[string]*tool.Schema{
		fieldRequest: {
			Type:        "string",
			Description: optionalString(cfg.requestDescription, defaultRequestDescription),
		},
	}
	if cfg.exposeInstruction {
		properties[fieldInstruction] = &tool.Schema{
			Type:        "string",
			Description: optionalString(cfg.instructionDescription, defaultInstructionDescription),
		}
	}
	if cfg.exposeToolSelection {
		toolsSchema := &tool.Schema{
			Type:        "array",
			Description: optionalString(cfg.toolsDescription, defaultToolsDescription),
			Items:       &tool.Schema{Type: "string"},
		}
		// When the effective capability surface is statically defined in code
		// via WithCapabilityTools, enumerate the selectable tool names so the
		// model picks from a known set instead of guessing. Provider-based and
		// default parent-derived surfaces are resolved per call, so their names
		// are not available at schema-build time.
		if cfg.capabilityToolsSet &&
			cfg.capabilityProvider == nil &&
			cfg.capabilitySurfaceProvider == nil {
			if names, enum := capabilityToolNameEnum(cfg.capabilityTools); len(enum) > 0 {
				toolsSchema.Items = &tool.Schema{Type: "string", Enum: enum}
				if cfg.toolsDescription == nil {
					toolsSchema.Description += " Available tool names: " +
						strings.Join(names, ", ") + "."
				}
			}
		}
		properties[fieldTools] = toolsSchema
	}
	if cfg.exposeSkillSelection {
		properties[fieldSkills] = &tool.Schema{
			Type:        "array",
			Description: optionalString(cfg.skillsDescription, defaultSkillsDescription),
			Items:       &tool.Schema{Type: "string"},
		}
	}
	return &tool.Schema{
		Type:        "object",
		Description: fmt.Sprintf("Input for the %s tool.", name),
		Properties:  properties,
		Required:    []string{fieldRequest},
	}
}

func optionalString(value *string, fallback string) string {
	if value != nil {
		return *value
	}
	return fallback
}

// maxDynamicToolEnumValues bounds JSON schema size when enumerating statically
// configured capability tool names.
const maxDynamicToolEnumValues = 256

// capabilityToolNameEnum returns the de-duplicated, sorted declaration names of
// the statically configured capability tools (for the schema enum and a short
// description hint), or nil when there are none, a name is missing, or the set
// is too large. Only a code-defined surface (WithCapabilityTools) can be
// enumerated at schema-build time; the parent-derived and provider surfaces are
// resolved per call.
func capabilityToolNameEnum(tools []tool.Tool) ([]string, []any) {
	if len(tools) == 0 {
		return nil, nil
	}
	seen := make(map[string]bool, len(tools))
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		n := declarationName(t)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	// Bound the schema size by the number of unique names actually enumerated,
	// not the raw slice length (which may contain duplicates).
	if len(names) == 0 || len(names) > maxDynamicToolEnumValues {
		return nil, nil
	}
	sort.Strings(names)
	enum := make([]any, 0, len(names))
	for _, n := range names {
		enum = append(enum, n)
	}
	return names, enum
}

// dynamicArgs is the wire format of the dynamic tool arguments. Tools and
// Skills are pointers so the tool can distinguish an omitted field (allow all)
// from an explicit empty array (allow none).
type dynamicArgs struct {
	Request     string    `json:"request"`
	Instruction string    `json:"instruction"`
	Tools       *[]string `json:"tools"`
	Skills      *[]string `json:"skills"`
}

// dynamicSpec is the parsed, validated form of a single dynamic invocation.
type dynamicSpec struct {
	request        string
	instruction    string
	tools          []string
	toolsProvided  bool
	skills         []string
	skillsProvided bool
}

func (at *Tool) parseDynamicArgs(jsonArgs []byte) dynamicSpec {
	var raw dynamicArgs
	if err := json.Unmarshal(jsonArgs, &raw); err != nil {
		// Be permissive: treat the raw payload as the request so a minimal
		// or non-conforming call still does something useful.
		return dynamicSpec{request: strings.TrimSpace(string(jsonArgs))}
	}
	spec := dynamicSpec{request: strings.TrimSpace(raw.Request)}
	if at.dynamicCfg.exposeInstruction {
		spec.instruction = strings.TrimSpace(raw.Instruction)
	}
	if at.dynamicCfg.exposeToolSelection && raw.Tools != nil {
		spec.toolsProvided = true
		spec.tools = dedupeNonEmpty(*raw.Tools)
	}
	if at.dynamicCfg.exposeSkillSelection && raw.Skills != nil {
		spec.skillsProvided = true
		spec.skills = dedupeNonEmpty(*raw.Skills)
	}
	return spec
}

// callDynamic runs the dynamic sub-agent and returns its final response.
func (at *Tool) callDynamic(ctx context.Context, jsonArgs []byte) (any, error) {
	subCtx, subInv, warnings, err := at.buildDynamicSubInvocation(ctx, jsonArgs)
	if err != nil {
		return "", err
	}
	subCtx, cancel := at.dynamicRunContext(subCtx)
	defer cancel()
	evCh, err := agent.RunWithPlugins(subCtx, subInv, subInv.Agent)
	if err != nil {
		return "", fmt.Errorf("failed to run sub-agent: %w", err)
	}
	response, err := at.collectResponse(
		subInv,
		at.wrapWithCallSemantics(subCtx, subInv, evCh),
	)
	if err != nil {
		return "", err
	}
	if response == "" {
		if err := subCtx.Err(); err != nil {
			return "", fmt.Errorf("dynamic sub-agent stopped: %w", err)
		}
	}
	return at.formatResponseWithWarnings(response, warnings), nil
}

// streamDynamic runs the dynamic sub-agent and forwards its streaming events.
func (at *Tool) streamDynamic(
	ctx context.Context,
	jsonArgs []byte,
	writer *tool.StreamWriter,
) {
	subCtx, subInv, warnings, err := at.buildDynamicSubInvocation(ctx, jsonArgs)
	if err != nil {
		sendStreamableCallError(ctx, writer, "dynamic sub-agent error: %w", err)
		return
	}
	subCtx, cancel := at.dynamicRunContext(subCtx)
	defer cancel()
	for _, w := range warnings {
		log.Warnf("AgentTool[%s]: %s", at.name, w)
	}
	evCh, err := agent.RunWithPlugins(subCtx, subInv, subInv.Agent)
	if err != nil {
		sendStreamableCallError(ctx, writer, "dynamic sub-agent run error: %w", err)
		return
	}
	// Surface warnings to the parent model in stream mode too (parity with the
	// Call path), not just to the logs.
	at.forwardSubInvocationStream(
		subCtx,
		subInv,
		at.wrapWithStreamSemantics(subCtx, subInv, evCh),
		writer,
		at.warningsNote(warnings),
	)
}

func (at *Tool) dynamicRunContext(ctx context.Context) (
	context.Context,
	context.CancelFunc,
) {
	timeout := at.dynamicCfg.timeout
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// buildDynamicSubInvocation resolves the base agent, capability surface and
// instruction for one dynamic call and returns a ready-to-run child invocation
// together with any non-fatal warnings to surface back to the parent model.
func (at *Tool) buildDynamicSubInvocation(
	ctx context.Context,
	jsonArgs []byte,
) (context.Context, *agent.Invocation, []string, error) {
	parentInv, ok := agent.InvocationFromContext(ctx)
	if !ok || parentInv == nil {
		return nil, nil, nil, fmt.Errorf(
			"agenttool: dynamic sub-agent requires a parent invocation; " +
				"register the tool on a running agent",
		)
	}
	if parentInv.Session == nil {
		return nil, nil, nil, fmt.Errorf(
			"agenttool: dynamic sub-agent requires a session on the parent invocation",
		)
	}

	spec := at.parseDynamicArgs(jsonArgs)
	if spec.request == "" {
		return nil, nil, nil, fmt.Errorf("agenttool: 'request' is required")
	}

	baseAgent := at.dynamicCfg.templateAgent
	if baseAgent == nil {
		baseAgent = parentInv.Agent
	}
	if baseAgent == nil {
		return nil, nil, nil, fmt.Errorf(
			"agenttool: no base agent available; set WithTemplateAgent or " +
				"call from an agent that exposes its invocation agent",
		)
	}

	// Flush events emitted before this tool call so the child sees a complete
	// snapshot of the parent history.
	if err := flush.Invoke(ctx, parentInv); err != nil {
		return nil, nil, nil, fmt.Errorf("flush parent invocation session: %w", err)
	}
	parentInv = parentInvocationWithLiveSession(parentInv)

	patch, warnings, err := at.buildDynamicPatch(ctx, parentInv, spec)
	if err != nil {
		return nil, nil, nil, err
	}

	message := model.NewUserMessage(spec.request)
	childKey := at.buildDynamicChildFilterKey(parentInv, baseAgent)
	nodeID := dynamicSurfaceNodeID(at.name)
	subInv := parentInv.Clone(
		at.dynamicChildInvocationOptions(baseAgent, message, childKey, nodeID, patch)...,
	)
	subCtx := agent.NewInvocationContext(ctx, subInv)
	return subCtx, subInv, warnings, nil
}

// buildDynamicPatch builds the surface patch that scopes the child invocation's
// tools, skills and instruction.
func (at *Tool) buildDynamicPatch(
	ctx context.Context,
	parentInv *agent.Invocation,
	spec dynamicSpec,
) (agent.SurfacePatch, []string, error) {
	var patch agent.SurfacePatch
	var warnings []string

	if at.dynamicCfg.exposeInstruction && spec.instruction != "" {
		patch.SetInstruction(spec.instruction)
	}

	// Tools: always set so the dynamic tool itself (and transfer_to_agent) are
	// excluded from the child, preventing runaway recursion.
	maxTools, userToolNames, externalNames, unavailableTools := at.dynamicMaxToolSurface(ctx, parentInv)
	selectedTools, toolWarnings, err := at.selectDynamicTools(
		maxTools, userToolNames, externalNames, unavailableTools, spec)
	if err != nil {
		return agent.SurfacePatch{}, nil, err
	}
	patch.SetTools(selectedTools)
	// SetTools only replaces the user tools. The framework re-derives
	// transfer_to_agent from the base/template agent's own sub-agents, so
	// suppress it explicitly: a short-lived sub-agent must never hand control
	// to another agent (and the README guarantees the model cannot reach it).
	patch.SetSuppressSubAgentTransfer()
	warnings = append(warnings, toolWarnings...)

	// Skills: selectDynamicSkills resolves both the repository and whether the
	// patch must set it (e.g. to stop a template agent's own skills from leaking
	// when the child should mirror the parent's — possibly empty — skills).
	skillRepo, patchSkills, skillWarnings := at.selectDynamicSkills(ctx, parentInv, spec)
	if patchSkills {
		patch.SetSkillRepository(skillRepo)
	}
	warnings = append(warnings, skillWarnings...)

	return patch, warnings, nil
}

// dynamicMaxToolSurface resolves the maximum tool surface the model may select
// from, honoring code-side overrides before deriving from the parent. It also
// returns the set of external (caller-executed) tool names so the caller can
// exclude them from selection.
//
// When deriving from the parent it uses the parent's effective surface: the
// base surface plus the run-scoped RunOptions.AdditionalTools/ExternalTools,
// with RunOptions.ToolFilter applied. This reuses the same resolution the LLM
// flow uses (toolsurface, which getFilteredTools also delegates to) so the
// child never sees tools the parent run filtered out and never misses business
// tools the parent run temporarily appended.
//
// Known limitation: toolsurface recomputes the surface rather than reading the
// flow's cached snapshot (the snapshot key is flow-internal and tool/agent
// cannot import the flow without a cycle). This keeps it correct for clones
// produced by parallel tool execution, but if a dynamic ToolSet / MCP ListTools
// changes between the parent model call and this tool call, the child reflects
// the current set rather than the exact snapshot the parent model just saw.
func (at *Tool) dynamicMaxToolSurface(
	ctx context.Context,
	parentInv *agent.Invocation,
) ([]tool.Tool, map[string]bool, map[string]bool, map[string]UnavailableCapability) {
	if at.dynamicCfg.capabilitySurfaceProvider != nil {
		surface := at.dynamicCfg.capabilitySurfaceProvider(ctx, parentInv)
		return surface.Tools,
			surface.UserToolNames,
			surface.ExternalToolNames,
			unavailableCapabilityMap(surface.UnavailableTools)
	}
	if at.dynamicCfg.capabilityProvider != nil {
		tools, userToolNames := at.dynamicCfg.capabilityProvider(ctx, parentInv)
		return tools, userToolNames, nil, nil
	}
	if at.dynamicCfg.capabilityToolsSet {
		return at.dynamicCfg.capabilityTools,
			toolNameSet(at.dynamicCfg.capabilityTools), nil, nil
	}
	if parentInv == nil || parentInv.Agent == nil {
		return nil, nil, nil, nil
	}
	tools, userToolNames, externalNames := toolsurface.EffectiveWithExternal(ctx, parentInv)
	return tools, userToolNames, externalNames, nil
}

// selectDynamicTools computes the child tool surface from the maximum surface
// and the (optional) model selection.
//
// Semantics of the model-facing "tools" field: it selects from the parent's
// currently-effective USER (business) tools only — those registered via
// WithTools/WithToolSets (plus run-scoped AdditionalTools). Framework-managed
// tools (transfer_to_agent, knowledge_*) are intentionally not hand-pickable;
// the child agent receives its own framework tools based on the template/base
// agent's configuration. The dynamic tool itself and transfer_to_agent are
// always excluded to prevent runaway recursion, and external (caller-executed)
// tools are excluded because a synchronous sub-agent has no channel to hand
// them back to the original caller for execution.
func (at *Tool) selectDynamicTools(
	maxTools []tool.Tool,
	userToolNames map[string]bool,
	externalNames map[string]bool,
	unavailableTools map[string]UnavailableCapability,
	spec dynamicSpec,
) ([]tool.Tool, []string, error) {
	excluded := map[string]bool{at.name: true, transfer.TransferToolName: true}
	for name := range externalNames {
		excluded[name] = true
	}
	isUserTool := func(name string) bool {
		if userToolNames == nil {
			return true
		}
		return userToolNames[name]
	}
	candidates := make([]tool.Tool, 0, len(maxTools))
	candidateByName := make(map[string]tool.Tool, len(maxTools))
	for _, t := range maxTools {
		if _, ok := t.(*Tool); ok {
			continue
		}
		name := declarationName(t)
		if name == "" || excluded[name] || !isUserTool(name) {
			continue
		}
		if _, unavailable := unavailableTools[name]; unavailable {
			continue
		}
		if _, seen := candidateByName[name]; seen {
			continue
		}
		candidates = append(candidates, t)
		candidateByName[name] = t
	}

	if !at.dynamicCfg.exposeToolSelection || !spec.toolsProvided {
		return candidates, nil, nil
	}
	// An explicit empty array ("tools": []) is a valid "allow none" selection:
	// the model deliberately ran a tool-free sub-agent. That is not an error,
	// so return an empty surface without a warning.
	if len(spec.tools) == 0 {
		return nil, nil, nil
	}

	selected := make([]tool.Tool, 0, len(spec.tools))
	selectedNames := make(map[string]bool, len(spec.tools))
	var warnings []string
	for _, name := range spec.tools {
		canonicalName := at.resolveDynamicToolName(name)
		t, ok := candidateByName[canonicalName]
		if !ok {
			if unavailable, exists := unavailableTools[canonicalName]; exists {
				warnings = append(warnings,
					formatUnavailableToolWarning(name, unavailable))
				continue
			}
			warnings = append(warnings, fmt.Sprintf(
				"requested tool %q is not available and was ignored", name))
			continue
		}
		if selectedNames[canonicalName] {
			continue
		}
		selectedNames[canonicalName] = true
		selected = append(selected, t)
	}
	if len(selected) == 0 {
		return nil, warnings, fmt.Errorf(
			"agenttool: none of the requested tools are available for the dynamic sub-agent; "+
				"omit tools to allow all permitted tools or choose from: %s",
			strings.Join(availableDynamicToolNames(candidates), ", "),
		)
	}
	return selected, warnings, nil
}

func availableDynamicToolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		if name := declarationName(t); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func (at *Tool) resolveDynamicToolName(name string) string {
	name = strings.TrimSpace(name)
	if at == nil || at.dynamicCfg == nil || len(at.dynamicCfg.toolAliases) == 0 {
		return name
	}
	if canonical, ok := at.dynamicCfg.toolAliases[name]; ok {
		return canonical
	}
	return name
}

// dynamicMaxSkillRepo resolves the maximum skill repository the model may
// select from: the code-side WithCapabilitySkillsProvider repository when set,
// else the code-side WithCapabilitySkills repository when set, else the parent
// invocation's effective repository. It never returns the template agent's own
// repository — the boundary is always the parent's skills (or an explicit
// capability override).
func (at *Tool) dynamicMaxSkillRepo(
	ctx context.Context,
	parentInv *agent.Invocation,
) skill.Repository {
	if at.dynamicCfg.capabilitySkillProvider != nil {
		return at.dynamicCfg.capabilitySkillProvider(ctx, parentInv)
	}
	if at.dynamicCfg.capabilitySkills != nil {
		return at.dynamicCfg.capabilitySkills
	}
	if parentInv == nil || parentInv.Agent == nil {
		return nil
	}
	if provider, ok := parentInv.Agent.(agent.InvocationSkillRepositoryProvider); ok {
		return provider.InvocationSkillRepository(ctx, parentInv)
	}
	return nil
}

// childCodeExecutor resolves, best-effort, the code executor the dynamic child
// will run with, so skill selection can warn when execution-dependent skills
// are requested without one. It mirrors the child's executor AFTER RunOptions
// sanitization:
//   - with a template agent the template's own executor is authoritative (the
//     parent's run-scoped executor is cleared on the child), so parentInv's
//     RunOptions.CodeExecutor is intentionally ignored here;
//   - without a template the child is the parent, so it inherits the parent's
//     run-scoped executor, falling back to the parent agent's executor.
func (at *Tool) childCodeExecutor(
	ctx context.Context,
	parentInv *agent.Invocation,
) codeexecutor.CodeExecutor {
	if at.dynamicCfg.templateAgent != nil {
		if p, ok := at.dynamicCfg.templateAgent.(agent.InvocationCodeExecutorProvider); ok {
			templateInv := agent.NewInvocation(
				agent.WithInvocationAgent(at.dynamicCfg.templateAgent),
			)
			return p.InvocationCodeExecutor(ctx, templateInv)
		}
		return nil
	}
	if parentInv != nil && parentInv.RunOptions.CodeExecutor != nil {
		return parentInv.RunOptions.CodeExecutor
	}
	if parentInv == nil || parentInv.Agent == nil {
		return nil
	}
	if p, ok := parentInv.Agent.(agent.InvocationCodeExecutorProvider); ok {
		return p.InvocationCodeExecutor(ctx, parentInv)
	}
	return nil
}

// selectDynamicSkills resolves the skill repository to mount on the child.
//
// It returns the repository, whether the surface patch must set it, and any
// non-fatal warnings. The skill boundary is WithCapabilitySkills when set,
// otherwise the parent invocation's effective repository (never the template
// agent's own repository).
//
//   - With no model selection it defaults the child to the boundary repository
//     and patches it whenever the boundary exists; when there is no boundary it
//     still applies an empty override if a template agent is configured, so the
//     template's own skills cannot leak into a child meant to mirror the parent.
//   - With a model selection it narrows the boundary to the selected names,
//     warning about unknown names and (best effort) about selected skills that
//     may need a code executor the child does not have.
func (at *Tool) selectDynamicSkills(
	ctx context.Context,
	parentInv *agent.Invocation,
	spec dynamicSpec,
) (skill.Repository, bool, []string) {
	maxRepo := at.dynamicMaxSkillRepo(ctx, parentInv)

	if !at.dynamicCfg.exposeSkillSelection || !spec.skillsProvided {
		if maxRepo != nil {
			return maxRepo, true, nil
		}
		// No boundary repository: override (with none) only when a template
		// agent is configured, otherwise leave the base (== parent) untouched.
		return nil, at.dynamicCfg.templateAgent != nil, nil
	}

	if maxRepo == nil {
		// Selection is ignored (no boundary repository is available), but a
		// template call must still override skills with none; otherwise the
		// template agent's own skills would leak to the child, outside the
		// code-defined dynamic boundary. This mirrors the skillsProvided==false
		// branch above.
		return nil, at.dynamicCfg.templateAgent != nil, []string{
			"skills selection was ignored because no skill repository is available",
		}
	}

	available := make(map[string]bool)
	for _, s := range skill.SummariesForContext(ctx, maxRepo) {
		available[s.Name] = true
	}
	selected := make(map[string]bool, len(spec.skills))
	var warnings []string
	for _, name := range spec.skills {
		if available[name] {
			selected[name] = true
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"requested skill %q is not available and was ignored", name))
	}
	if len(selected) > 0 && at.childCodeExecutor(ctx, parentInv) == nil {
		warnings = append(warnings,
			"the sub-agent has no code executor, so any selected skill that "+
				"requires running code may not work")
	}
	filtered := skill.NewFilteredRepository(
		maxRepo,
		func(_ context.Context, s skill.Summary) bool {
			return selected[s.Name]
		},
	)
	return filtered, true, warnings
}

// dynamicChildInvocationOptions builds the invocation options for a dynamic
// child run, mounting the surface patch on a dedicated root node and sanitizing
// the run-scoped options inherited from the parent clone so they cannot bypass
// the code-defined boundary.
func (at *Tool) dynamicChildInvocationOptions(
	baseAgent agent.Agent,
	message model.Message,
	childKey string,
	nodeID string,
	patch agent.SurfacePatch,
) []agent.InvocationOptions {
	// With a template agent the template defines the execution boundary
	// (model, executor, ...), so parent run-scoped overrides must not leak in.
	// Without a template the child IS the parent agent, so it keeps them.
	enforceTemplateBoundary := at.dynamicCfg.templateAgent != nil
	return []agent.InvocationOptions{
		agent.WithInvocationAgent(baseAgent),
		agent.WithInvocationMessage(message),
		agent.WithInvocationEventFilterKey(childKey),
		func(inv *agent.Invocation) {
			agent.SetInvocationSurfaceRootNodeID(inv, nodeID)
			runOpts := inv.RunOptions
			agent.WithSurfacePatchForNode(nodeID, patch)(&runOpts)
			at.sanitizeChildRunOptions(&runOpts, enforceTemplateBoundary)
			inv.RunOptions = runOpts
		},
	}
}

// sanitizeChildRunOptions strips run-scoped options inherited from the parent
// clone that would otherwise undermine the dynamic boundary.
//
// Tool surface (cleared unconditionally): the surface patch is the single
// source of truth for the child's tools, so every run-scoped tool input is
// cleared. AdditionalTools/ExternalTools/ExternalToolNames would otherwise be
// re-appended by the child flow on top of the selected tools, and ToolFilter
// would re-run in the child flow — wrongly dropping code-defined
// WithCapabilityTools/WithCapabilityProvider tools that never passed through it
// (the parent filter was already applied when deriving the candidate surface).
//
// Template boundary (cleared only when a template agent is configured): the
// template defines model, prompt and execution, so parent run-scoped overrides
// must not leak in:
//   - model: Model/ModelName/ModelSelector all outrank the template's own
//     model resolution;
//   - prompt: Instruction/GlobalInstruction outrank the template prompt — a
//     model-provided instruction still applies because it travels via the
//     surface patch, which is resolved before RunOptions;
//   - execution: CodeExecutor, plus the execution-policy filters
//     (ToolExecutionFilter defers tool calls, ToolPermissionPolicy gates them)
//     which have no natural external-continuation channel for a synchronous
//     sub-agent. Inheriting these requires an explicit future Option.
func (at *Tool) sanitizeChildRunOptions(
	runOpts *agent.RunOptions,
	enforceTemplateBoundary bool,
) {
	runOpts.AdditionalTools = nil
	runOpts.ExternalTools = nil
	runOpts.ExternalToolNames = nil
	runOpts.ToolFilter = nil
	if !enforceTemplateBoundary {
		return
	}
	runOpts.Model = nil
	runOpts.ModelName = ""
	runOpts.ModelSelector = nil
	runOpts.Instruction = ""
	runOpts.GlobalInstruction = ""
	runOpts.CodeExecutor = nil
	runOpts.ToolExecutionFilter = nil
	runOpts.ToolPermissionPolicy = nil
}

// buildDynamicChildFilterKey constructs the child filter key honoring the
// configured history scope (isolated by default).
func (at *Tool) buildDynamicChildFilterKey(
	parentInv *agent.Invocation,
	baseAgent agent.Agent,
) string {
	base := at.name
	if baseAgent != nil {
		if info := baseAgent.Info(); info.Name != "" {
			base = info.Name
		}
	}
	childKey := base + "-" + uuid.NewString()
	if at.historyScope == HistoryScopeParentBranch {
		if pk := parentInv.GetEventFilterKey(); pk != "" {
			childKey = pk + agent.EventFilterKeyDelimiter + childKey
		}
	}
	return childKey
}

// dynamicSurfaceNodeID returns a unique surface root node id for one dynamic
// invocation. Any unique string works because the surface patch is keyed by
// this id and looked up by equality.
func dynamicSurfaceNodeID(name string) string {
	return name + "/dynamic-" + uuid.NewString()
}

// warningsNote renders non-fatal warnings as a short note block attributed to
// the dynamic tool, or "" when there are none. It is shared by the Call and
// stream paths so the parent model sees the same warnings either way.
func (at *Tool) warningsNote(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Note from ")
	b.WriteString(at.name)
	b.WriteString(":\n")
	for _, w := range warnings {
		b.WriteString("- ")
		b.WriteString(w)
		b.WriteString("\n")
	}
	return b.String()
}

// formatResponseWithWarnings prepends any non-fatal warnings to the sub-agent
// response so the parent model learns about ignored selections.
func (at *Tool) formatResponseWithWarnings(response string, warnings []string) string {
	note := at.warningsNote(warnings)
	if note == "" {
		return response
	}
	if response != "" {
		return note + "\n" + response
	}
	return note
}

func unavailableCapabilityMap(values []UnavailableCapability) map[string]UnavailableCapability {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]UnavailableCapability, len(values))
	for _, value := range values {
		value.Name = strings.TrimSpace(value.Name)
		if value.Name == "" {
			continue
		}
		value.Detail = sanitizeUnavailableDetail(value.Detail)
		if value.Reason == "" {
			value.Reason = CapabilityUnavailableReasonUnknown
		}
		out[value.Name] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

const maxUnavailableDetailRunes = 256

func sanitizeUnavailableDetail(detail string) string {
	cleaned := strings.Join(strings.Fields(strings.TrimSpace(detail)), " ")
	runes := []rune(cleaned)
	if len(runes) <= maxUnavailableDetailRunes {
		return cleaned
	}
	return string(runes[:maxUnavailableDetailRunes]) + "..."
}

func formatUnavailableToolWarning(name string, unavailable UnavailableCapability) string {
	reason := unavailable.Reason
	if reason == "" {
		reason = CapabilityUnavailableReasonUnknown
	}
	detail := sanitizeUnavailableDetail(unavailable.Detail)
	if detail != "" {
		return fmt.Sprintf(
			"requested tool %q is unavailable (reason: %s; detail: %s) and was ignored",
			name, reason, detail,
		)
	}
	return fmt.Sprintf(
		"requested tool %q is unavailable (reason: %s) and was ignored",
		name, reason,
	)
}

func toolNameSet(tools []tool.Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		if name := declarationName(t); name != "" {
			names[name] = true
		}
	}
	return names
}

func normalizeToolAliases(aliases map[string]string) map[string]string {
	if len(aliases) == 0 {
		return nil
	}
	out := make(map[string]string, len(aliases))
	for alias, canonical := range aliases {
		alias = strings.TrimSpace(alias)
		canonical = strings.TrimSpace(canonical)
		if alias == "" || canonical == "" || alias == canonical {
			continue
		}
		out[alias] = canonical
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func declarationName(t tool.Tool) string {
	if t == nil {
		return ""
	}
	decl := t.Declaration()
	if decl == nil {
		return ""
	}
	return decl.Name
}

func dedupeNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
