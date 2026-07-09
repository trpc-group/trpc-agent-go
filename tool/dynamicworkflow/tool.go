//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dynamicworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/appender"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/eventstream"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/flush"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/livesession"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option configures a run_workflow tool.
type Option func(*config)

type config struct {
	name        string
	description string
	tools       []tool.CallableTool
}

const defaultMaxConcurrentAgents = 8

// WithCodeCallableTools exposes host tools to workflow code. Workflow Python
// can call them with call_tool(...). These tools are not added to any child
// Agent and are never inferred from the root Agent.
func WithCodeCallableTools(tools ...tool.CallableTool) Option {
	return func(c *config) {
		c.tools = append(c.tools, tools...)
	}
}

// WithName changes the model-visible tool name. The default is run_workflow.
func WithName(name string) Option {
	return func(c *config) { c.name = name }
}

// WithDescription replaces the default model-visible description.
func WithDescription(description string) Option {
	return func(c *config) { c.description = description }
}

// NewTool creates a run_workflow tool. At least one sub-agent is required so
// this capability remains distinct from execute_tool_code, which is intended
// for tool-only orchestration.
func NewTool(runtime Runtime, agents []agent.Agent, opts ...Option) (tool.CallableTool, error) {
	if runtime == nil {
		return nil, required("runtime")
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("dynamicworkflow: at least one agent is required")
	}
	cfg := config{name: "run_workflow", description: defaultDescription}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if strings.TrimSpace(cfg.name) == "" {
		return nil, required("tool name")
	}
	agentRegistry, err := registerAgentTemplates(agents)
	if err != nil {
		return nil, err
	}
	for name, tmpl := range agentRegistry {
		delete(tmpl.tools, cfg.name)
		agentRegistry[name] = tmpl
	}
	toolRegistry, err := registerTools(cfg.tools)
	if err != nil {
		return nil, err
	}
	if _, exists := toolRegistry[cfg.name]; exists {
		return nil, fmt.Errorf("dynamicworkflow: workflow tool %q cannot call itself", cfg.name)
	}
	return &workflowTool{
		runtime: runtime,
		agents:  agentRegistry,
		tools:   toolRegistry,
		cfg:     cfg,
	}, nil
}

const defaultDescription = "Run one temporary Python workflow for tasks that need role delegation, parallel analysis, conditional iteration, or data flow between workflow-local child agents. Registered templates fix model, executor, callbacks, permissions, and other runtime policy; each agent call's dynamic instruction defines that child role's temporary business job. The code argument is executable workflow code, not a code sample. Use only declared workflow capabilities and return a JSON-compatible value."

const codeCallableToolDescription = "Code-callable host tools are enabled for this workflow. Call await call_tool(\"tool_name\", **json_arguments) only for the documented allowlisted tools below. Do not parallelize mutating tool calls; use child agents for concurrent work."

type workflowTool struct {
	runtime Runtime
	agents  map[string]agentTemplate
	tools   map[string]tool.CallableTool
	cfg     config
}

// Declaration implements tool.Tool.
func (t *workflowTool) Declaration() *tool.Declaration {
	description := t.cfg.description
	if len(t.tools) > 0 {
		description += "\n\n" + codeCallableToolDescription
	}
	if capabilities := buildCapabilityHelp(t.agents, t.tools); capabilities != "" {
		description += "\n\nHost capabilities available inside Python:\n" + capabilities
	}
	codeDescription := `Write the executable workflow body itself: use await directly and finish with return. Do not assign the program to code, put it in a quoted string or Markdown fence, return Python source, define an uncalled wrapper such as async def run(), define or run main(), call asyncio.run(), import modules, or access undeclared host capabilities. Use Python True/False/None in source (JSON-style true/false/null aliases are also accepted in generated AgentSpec dictionaries). Use await agent(input, options=None) or await agent(input, **options) to create and run one child Agent. options may set template, instruction, instance_id, tools, skills, and structured_output; schema is shorthand for structured_output.schema. If exactly one template is registered, template may be omitted. Omitted tools and skills inherit eligible template capabilities; use [] to disable a capability type or a non-empty list to narrow it. Pass schema as a Python dict literal; do not import json or call json.dumps. agent returns an envelope with text and optional structured fields; prefer result["structured"], and missing result["field"] / result.get("field") fall back to structured.

Canonical one-shot pattern:
draft = await agent("Write a concise draft.", instruction="Write the first draft.", tools=[])
for _ in range(3):
    review = await agent({"draft": draft}, instruction="Review the draft and return approved plus feedback.", schema={"type": "object", "properties": {"approved": {"type": "boolean"}, "feedback": {"type": "string"}}})
    if review["approved"]:
        break
    draft = await agent({"draft": draft, "feedback": review["feedback"]}, instruction="Revise the draft.", tools=[])
return {"draft": draft, "review": review}

Short parallel idiom: analyses = await parallel([lambda: agent("Option A", instruction="Analyze"), lambda: agent("Option B", instruction="Analyze")]). parallel([lambda: agent(...), ...]) runs independent branches concurrently and preserves input order. pipeline(items, stage1, ...) runs each item through async stages; each stage receives (previous_result, original_item, index).`
	if len(t.tools) > 0 {
		codeDescription += " Code-callable host tools are enabled: use await call_tool(name, **json_arguments) only for the documented allowlisted tools."
	}
	return &tool.Declaration{
		Name:        t.cfg.name,
		Description: description,
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"code"},
			Properties: map[string]*tool.Schema{
				"code": {
					Type:        "string",
					Description: codeDescription,
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"value":  {Description: "JSON-serializable value returned by the workflow"},
				"stdout": {Type: "string", Description: "Captured Python stdout"},
			},
		},
	}
}

// Call implements tool.CallableTool.
func (t *workflowTool) Call(ctx context.Context, raw []byte) (any, error) {
	var input struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("dynamicworkflow: decode input: %w", err)
	}
	parent, ok := agent.InvocationFromContext(ctx)
	if !ok || parent == nil {
		return nil, fmt.Errorf("dynamicworkflow: run_workflow requires a running agent invocation")
	}
	if parent.Session == nil {
		return nil, fmt.Errorf("dynamicworkflow: run_workflow requires a parent session")
	}
	gateway := &workflowGateway{
		parent:     parent,
		agents:     t.agents,
		tools:      t.tools,
		workflow:   uuid.NewString(),
		toolName:   t.cfg.name,
		agentSlots: make(chan struct{}, defaultMaxConcurrentAgents),
	}
	if err := flush.Invoke(ctx, parent); err != nil {
		return nil, fmt.Errorf("dynamicworkflow: flush parent session: %w", err)
	}
	return Execute(ctx, t.runtime, gateway, input.Code)
}

type workflowGateway struct {
	parent   *agent.Invocation
	agents   map[string]agentTemplate
	tools    map[string]tool.CallableTool
	workflow string
	toolName string

	agentSlots    chan struct{}
	instanceLocks sync.Map
	appendMu      sync.Mutex
}

// HandleWorkflowCall implements CallHandler.
func (g *workflowGateway) HandleWorkflowCall(
	ctx context.Context,
	call Call,
) (json.RawMessage, error) {
	if g == nil {
		return nil, fmt.Errorf("dynamicworkflow: nil gateway")
	}
	switch call.Kind {
	case CallKindTool:
		return g.callTool(ctx, call)
	case CallKindAgent:
		return g.callAgent(ctx, call)
	default:
		return nil, fmt.Errorf("dynamicworkflow: unsupported call kind %q", call.Kind)
	}
}

func (g *workflowGateway) callTool(ctx context.Context, call Call) (json.RawMessage, error) {
	candidate, ok := g.tools[call.Name]
	if !ok {
		return nil, fmt.Errorf("dynamicworkflow: tool %q is not allowlisted", call.Name)
	}
	if !json.Valid(call.Args) {
		return nil, fmt.Errorf("dynamicworkflow: tool %q received invalid JSON arguments", call.Name)
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Args, &args); err != nil || args == nil {
		return nil, fmt.Errorf("dynamicworkflow: tool %q requires a JSON object argument", call.Name)
	}
	permissionResult, err := g.checkToolPermission(ctx, call, candidate)
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: check permission for tool %q: %w", call.Name, err)
	}
	if permissionResult != nil {
		raw, err := json.Marshal(permissionResult)
		if err != nil {
			return nil, fmt.Errorf("dynamicworkflow: encode permission result for tool %q: %w", call.Name, err)
		}
		return raw, nil
	}
	value, err := candidate.Call(ctx, call.Args)
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: call tool %q: %w", call.Name, err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: encode result from tool %q: %w", call.Name, err)
	}
	return raw, nil
}

func (g *workflowGateway) checkToolPermission(
	ctx context.Context,
	call Call,
	candidate tool.CallableTool,
) (*tool.PermissionResult, error) {
	req := &tool.PermissionRequest{
		Tool:        candidate,
		ToolName:    call.Name,
		ToolCallID:  call.ID,
		Declaration: candidate.Declaration(),
		Arguments:   append([]byte(nil), call.Args...),
		Metadata:    tool.MetadataOf(candidate),
	}
	if checker, ok := candidate.(tool.PermissionChecker); ok {
		decision, err := checker.CheckPermission(ctx, req)
		result, err := normalizeWorkflowToolPermissionResult(req, decision, err)
		if result != nil || err != nil {
			return result, err
		}
	}
	if g == nil || g.parent == nil || g.parent.RunOptions.ToolPermissionPolicy == nil {
		return nil, nil
	}
	decision, err := g.parent.RunOptions.ToolPermissionPolicy.CheckToolPermission(ctx, req)
	return normalizeWorkflowToolPermissionResult(req, decision, err)
}

func normalizeWorkflowToolPermissionResult(
	req *tool.PermissionRequest,
	decision tool.PermissionDecision,
	checkErr error,
) (*tool.PermissionResult, error) {
	if checkErr != nil {
		return nil, checkErr
	}
	decision, err := tool.NormalizePermissionDecision(decision)
	if err != nil {
		return nil, err
	}
	if decision.Action == tool.PermissionActionAllow {
		return nil, nil
	}
	result := tool.PermissionResultFor(req.ToolName, decision)
	return &result, nil
}

func (g *workflowGateway) callAgent(ctx context.Context, call Call) (json.RawMessage, error) {
	req, err := g.resolveAgentCall(call)
	if err != nil {
		return nil, err
	}
	candidate, ok := g.agents[req.templateName]
	if !ok {
		return nil, fmt.Errorf("dynamicworkflow: agent %q is not registered", req.templateName)
	}
	parent := parentWithLiveSession(g.parent)
	childKey := workflowChildKey(parent, g.workflow, req.instanceID)
	unlock, err := g.lockChildInstance(ctx, childKey)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := g.acquireAgentSlot(ctx); err != nil {
		return nil, err
	}
	defer g.releaseAgentSlot()
	message := model.NewUserMessage(workflowInputText(req.input))
	invocationOptions := []agent.InvocationOptions{
		agent.WithInvocationAgent(candidate.agent),
		agent.WithInvocationMessage(message),
		agent.WithInvocationEventFilterKey(childKey),
		clearInheritedWorkflowRunOptions(),
		agent.WithInvocationParentMetadata(&agent.ParentInvocationMetadata{
			TriggerType: agent.TriggerTypeDynamicWorkflow,
			TriggerID:   g.workflow + "/" + call.ID,
			TriggerName: g.toolName,
		}),
	}
	childSurfaceOpt, err := g.workflowChildInvocationOption(ctx, candidate, req)
	if err != nil {
		return nil, err
	}
	invocationOptions = append(invocationOptions, childSurfaceOpt)
	child := parent.Clone(invocationOptions...)
	if err := g.appendChildUserMessage(ctx, child); err != nil {
		return nil, err
	}
	childCtx := agent.NewInvocationContext(ctx, child)
	events, err := agent.RunWithPlugins(childCtx, child, candidate.agent)
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: run agent %q: %w", req.templateName, err)
	}
	result, err := g.collectChildResult(childCtx, child, events)
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: collect agent %q: %w", req.templateName, err)
	}
	result.HistoryKey = childKey
	if child.Session != nil {
		result.SessionID = child.Session.ID
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: encode agent %q result: %w", req.templateName, err)
	}
	return raw, nil
}

func (g *workflowGateway) acquireAgentSlot(ctx context.Context) error {
	if g == nil || g.agentSlots == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case g.agentSlots <- struct{}{}:
		return nil
	}
}

func (g *workflowGateway) releaseAgentSlot() {
	if g == nil || g.agentSlots == nil {
		return
	}
	<-g.agentSlots
}

func (g *workflowGateway) lockChildInstance(ctx context.Context, key string) (func(), error) {
	if g == nil || key == "" {
		return func() {}, nil
	}
	value, _ := g.instanceLocks.LoadOrStore(key, newChildInstanceLock())
	lock := value.(chan struct{})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-lock:
		return func() { lock <- struct{}{} }, nil
	}
}

func newChildInstanceLock() chan struct{} {
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	return lock
}

// clearInheritedWorkflowRunOptions prevents root run-scoped overrides from
// leaking into a workflow child. A child should start from its template's
// configured behavior and then apply only the dynamic spec requested by the
// workflow code.
func clearInheritedWorkflowRunOptions() agent.InvocationOptions {
	return func(inv *agent.Invocation) {
		if inv == nil {
			return
		}
		runOpts := inv.RunOptions
		sanitizeWorkflowChildRunOptions(&runOpts)
		inv.RunOptions = runOpts
		inv.StructuredOutput = nil
		inv.StructuredOutputType = nil
	}
}

type agentResult struct {
	Text         string          `json:"text"`
	Structured   json.RawMessage `json:"structured,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	HistoryKey   string          `json:"history_key"`
	InvocationID string          `json:"invocation_id"`
}

func parentWithLiveSession(parent *agent.Invocation) *agent.Invocation {
	if parent == nil || parent.Session == nil {
		return parent
	}
	live, ok := livesession.Get(parent)
	if !ok || live == nil || live == parent.Session {
		return parent
	}
	return parent.View(agent.WithInvocationSession(live))
}

func workflowChildKey(parent *agent.Invocation, workflowID, agentName string) string {
	prefix := "dynamic_workflow/" + workflowID + "/" + agentName
	if parent == nil || parent.GetEventFilterKey() == "" {
		return prefix
	}
	return parent.GetEventFilterKey() + agent.EventFilterKeyDelimiter + prefix
}

func workflowInputText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	return string(raw)
}

func (g *workflowGateway) appendChildUserMessage(ctx context.Context, inv *agent.Invocation) error {
	if inv == nil {
		return fmt.Errorf("dynamicworkflow: child invocation is nil")
	}
	userEvent := event.NewResponseEvent(inv.InvocationID, "user", &model.Response{
		Choices: []model.Choice{{Index: 0, Message: inv.Message}},
	})
	agent.InjectIntoEvent(inv, userEvent)
	if err := g.appendSessionEvent(ctx, inv, userEvent); err != nil {
		return fmt.Errorf("dynamicworkflow: append child input: %w", err)
	}
	return nil
}

func (g *workflowGateway) collectChildResult(
	ctx context.Context,
	inv *agent.Invocation,
	events <-chan *event.Event,
) (agentResult, error) {
	result := agentResult{InvocationID: inv.InvocationID}
	var partial strings.Builder
	for evt := range events {
		if evt == nil {
			continue
		}
		agent.InjectIntoEvent(inv, evt)
		if evt.Response != nil && evt.Response.Error != nil {
			return agentResult{}, errorsFromResponse(evt.Response.Error)
		}
		if evt.StructuredOutput != nil {
			raw, err := json.Marshal(evt.StructuredOutput)
			if err != nil {
				return agentResult{}, fmt.Errorf("dynamicworkflow: encode structured output: %w", err)
			}
			result.Structured = json.RawMessage(raw)
		}
		forwarded, err := eventstream.Invoke(ctx, inv, evt)
		if err != nil {
			return agentResult{}, err
		}
		if !forwarded {
			if err := g.appendSessionEvent(ctx, inv, evt); err != nil {
				return agentResult{}, err
			}
		}
		if evt.RequiresCompletion {
			if err := inv.NotifyCompletion(ctx, agent.GetAppendEventNoticeKey(evt.ID)); err != nil {
				return agentResult{}, err
			}
		}
		content, assistant := assistantEventContent(evt)
		if !assistant || content == "" {
			continue
		}
		if evt.IsPartial {
			partial.WriteString(content)
			continue
		}
		result.Text = content
		partial.Reset()
	}
	if result.Text == "" {
		result.Text = partial.String()
	}
	if result.Structured == nil && json.Valid([]byte(result.Text)) {
		result.Structured = json.RawMessage(append([]byte(nil), result.Text...))
	}
	return result, nil
}

func (g *workflowGateway) appendSessionEvent(
	ctx context.Context,
	inv *agent.Invocation,
	evt *event.Event,
) error {
	if g == nil {
		return appendSessionEvent(ctx, inv, evt)
	}
	g.appendMu.Lock()
	defer g.appendMu.Unlock()
	return appendSessionEvent(ctx, inv, evt)
}

func appendSessionEvent(ctx context.Context, inv *agent.Invocation, evt *event.Event) error {
	if inv == nil || evt == nil {
		return nil
	}
	attached, err := appender.Invoke(ctx, inv, evt)
	if err != nil {
		return err
	}
	if attached {
		return nil
	}
	if inv.SessionService != nil && inv.Session != nil {
		return inv.SessionService.AppendEvent(ctx, inv.Session, evt)
	}
	return nil
}

func assistantEventContent(evt *event.Event) (string, bool) {
	if evt == nil || evt.Response == nil || len(evt.Response.Choices) == 0 {
		return "", false
	}
	choice := evt.Response.Choices[0]
	if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
		return choice.Message.Content, true
	}
	if choice.Delta.Role == model.RoleAssistant && choice.Delta.Content != "" {
		return choice.Delta.Content, true
	}
	return "", false
}

func errorsFromResponse(responseErr *model.ResponseError) error {
	if responseErr == nil {
		return nil
	}
	return fmt.Errorf("agent error: %s", responseErr.Message)
}

func registerTools(tools []tool.CallableTool) (map[string]tool.CallableTool, error) {
	registered := make(map[string]tool.CallableTool, len(tools))
	for _, candidate := range tools {
		if candidate == nil || candidate.Declaration() == nil {
			return nil, fmt.Errorf("dynamicworkflow: tool declaration is required")
		}
		name := strings.TrimSpace(candidate.Declaration().Name)
		if name == "" {
			return nil, fmt.Errorf("dynamicworkflow: tool name is required")
		}
		if _, exists := registered[name]; exists {
			return nil, fmt.Errorf("dynamicworkflow: duplicate tool %q", name)
		}
		registered[name] = candidate
	}
	return registered, nil
}

func declarationName(candidate tool.Tool) string {
	if candidate == nil || candidate.Declaration() == nil {
		return ""
	}
	return strings.TrimSpace(candidate.Declaration().Name)
}

func buildCapabilityHelp(agents map[string]agentTemplate, tools map[string]tool.CallableTool) string {
	var lines []string
	agentNames := sortedNames(agents)
	for _, name := range agentNames {
		tmpl := agents[name]
		info := tmpl.agent.Info()
		line := fmt.Sprintf("- template %q: %s", name, info.Description)
		if toolNames := sortedNames(tmpl.tools); len(toolNames) > 0 {
			line += "\n  Dynamic narrowing tools: " + strings.Join(toolNames, ", ")
		}
		if len(info.InputSchema) > 0 {
			line += "\n  Input JSON Schema: " + marshalOrNull(info.InputSchema)
		}
		if len(info.OutputSchema) > 0 {
			line += "\n  Default structured output JSON Schema: " + marshalOrNull(info.OutputSchema)
		}
		lines = append(lines, line)
	}
	for _, name := range sortedNames(tools) {
		decl := tools[name].Declaration()
		line := fmt.Sprintf("- call_tool(%q, **json_arguments): %s", name, decl.Description)
		line += "\n  Input JSON Schema: " + marshalOrNull(decl.InputSchema)
		if decl.OutputSchema != nil {
			line += "\n  Output JSON Schema: " + marshalOrNull(decl.OutputSchema)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func sortedNames[T any](values map[string]T) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func marshalOrNull(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(raw)
}
