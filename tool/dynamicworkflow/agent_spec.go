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

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

const maxStructuredOutputSchemaBytes = 64 << 10

type agentTemplate struct {
	name  string
	agent agent.Agent
	tools map[string]tool.Tool
}

type agentCallRequest struct {
	templateName string
	instanceID   string
	input        json.RawMessage
	spec         AgentSpec
	dynamic      bool
	newStyle     bool
}

func registerAgentTemplates(agents []agent.Agent) (map[string]agentTemplate, error) {
	registered := make(map[string]agentTemplate, len(agents))
	for _, candidate := range agents {
		if candidate == nil {
			return nil, fmt.Errorf("dynamicworkflow: agent is required")
		}
		name := strings.TrimSpace(candidate.Info().Name)
		if name == "" {
			return nil, fmt.Errorf("dynamicworkflow: agent name is required")
		}
		if _, exists := registered[name]; exists {
			return nil, fmt.Errorf("dynamicworkflow: duplicate agent %q", name)
		}
		tools, userToolNames := templateToolSurface(context.Background(), candidate)
		registered[name] = agentTemplate{
			name:  name,
			agent: candidate,
			tools: selectableToolMap(tools, userToolNames, name),
		}
	}
	return registered, nil
}

func templateToolSurface(
	ctx context.Context,
	candidate agent.Agent,
) ([]tool.Tool, map[string]bool) {
	provider, ok := candidate.(agent.InvocationToolSurfaceProvider)
	if !ok || provider == nil {
		return candidate.Tools(), nil
	}
	inv := agent.NewInvocation(agent.WithInvocationAgent(candidate))
	return provider.InvocationToolSurface(ctx, inv)
}

func parseAgentCall(call Call) (agentCallRequest, error) {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return agentCallRequest{}, fmt.Errorf(
			"dynamicworkflow: decode input for agent call: %w", err,
		)
	}
	input, ok := args["input"]
	if !ok || !json.Valid(input) {
		return agentCallRequest{}, fmt.Errorf(
			"dynamicworkflow: agent call requires JSON input",
		)
	}
	if name := strings.TrimSpace(call.Name); name != "" {
		return agentCallRequest{
			templateName: name,
			instanceID:   name,
			input:        input,
			spec:         AgentSpec{Template: name},
		}, nil
	}
	if rawAgent, ok := args["agent"]; ok {
		if !json.Valid(rawAgent) {
			return agentCallRequest{}, fmt.Errorf(
				"dynamicworkflow: agent selector must be valid JSON",
			)
		}
		spec, dynamic, err := decodeAgentSelector(rawAgent)
		if err != nil {
			return agentCallRequest{}, err
		}
		instanceID := strings.TrimSpace(spec.InstanceID)
		if instanceID == "" {
			if dynamic {
				instanceID = cleanPathSegment(call.ID)
				if instanceID == "" {
					instanceID = uuid.NewString()
				}
			} else {
				instanceID = spec.Template
			}
		}
		return agentCallRequest{
			templateName: spec.Template,
			instanceID:   instanceID,
			input:        input,
			spec:         spec,
			dynamic:      dynamic,
		}, nil
	}

	// The Python agent(input, options) DSL sends an optional options object.
	// Template resolution happens in workflowGateway so a sole registered
	// template can be inherited when options.template is omitted.
	var spec AgentSpec
	if rawOptions, ok := args["options"]; ok && strings.TrimSpace(string(rawOptions)) != "null" {
		decoded, err := decodeAgentOptions(rawOptions)
		if err != nil {
			return agentCallRequest{}, err
		}
		spec = decoded
	}
	return agentCallRequest{
		templateName: spec.Template,
		input:        input,
		spec:         spec,
		newStyle:     true,
	}, nil
}

func decodeAgentOptions(raw json.RawMessage) (AgentSpec, error) {
	if !json.Valid(raw) {
		return AgentSpec{}, fmt.Errorf("dynamicworkflow: agent options must be valid JSON")
	}
	var template string
	if err := json.Unmarshal(raw, &template); err == nil {
		return AgentSpec{Template: strings.TrimSpace(template)}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return AgentSpec{}, fmt.Errorf("dynamicworkflow: agent options must be a mapping or template name")
	}
	for name := range fields {
		if !isSupportedAgentOption(name) {
			return AgentSpec{}, fmt.Errorf("dynamicworkflow: unsupported agent option %q", name)
		}
	}
	if rawStructuredOutput, ok := fields["structured_output"]; ok {
		canonical, err := canonicalStructuredOutput(rawStructuredOutput)
		if err != nil {
			return AgentSpec{}, err
		}
		fields["structured_output"] = canonical
	}
	// schema is a concise options-level alias for structured_output.schema.
	if rawSchema, ok := fields["schema"]; ok {
		if _, hasStructuredOutput := fields["structured_output"]; !hasStructuredOutput {
			canonical, err := json.Marshal(map[string]json.RawMessage{"schema": rawSchema})
			if err != nil {
				return AgentSpec{}, fmt.Errorf("dynamicworkflow: encode agent schema: %w", err)
			}
			fields["structured_output"] = canonical
		}
		delete(fields, "schema")
	}
	canonical, err := json.Marshal(fields)
	if err != nil {
		return AgentSpec{}, fmt.Errorf("dynamicworkflow: encode agent options: %w", err)
	}
	var spec AgentSpec
	if err := json.Unmarshal(canonical, &spec); err != nil {
		return AgentSpec{}, fmt.Errorf("dynamicworkflow: decode agent options: %w", err)
	}
	return spec, nil
}

func isSupportedAgentOption(name string) bool {
	switch name {
	case "template", "instance_id", "instruction", "tools", "skills", "structured_output", "schema":
		return true
	default:
		return false
	}
}

func canonicalStructuredOutput(raw json.RawMessage) (json.RawMessage, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("dynamicworkflow: structured_output must be valid JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("dynamicworkflow: structured_output must be a JSON object")
	}
	if _, wrapped := fields["schema"]; wrapped {
		return raw, nil
	}
	// A bare JSON Schema is the ergonomic form most workflow code writes:
	// structured_output: {"type":"object", "properties": {...}}.
	// Wrap it into the richer internal representation while preserving the
	// existing name/strict/description form for callers that need it.
	canonical, err := json.Marshal(map[string]json.RawMessage{"schema": raw})
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: encode structured_output schema: %w", err)
	}
	return canonical, nil
}

func (g *workflowGateway) resolveAgentCall(call Call) (agentCallRequest, error) {
	req, err := parseAgentCall(call)
	if err != nil {
		return agentCallRequest{}, err
	}
	if req.newStyle {
		if req.templateName == "" {
			if len(g.agents) != 1 {
				return agentCallRequest{}, fmt.Errorf(
					"dynamicworkflow: agent options.template is required when multiple templates are registered",
				)
			}
			for name := range g.agents {
				req.templateName = name
			}
		}
		req.spec.Template = req.templateName
		if err := normalizeAgentSpec(&req.spec); err != nil {
			return agentCallRequest{}, err
		}
		req.dynamic = agentSpecHasOverrides(req.spec)
		if req.instanceID == "" {
			req.instanceID = req.spec.InstanceID
		}
		if req.instanceID == "" {
			req.instanceID = cleanPathSegment(call.ID)
			if req.instanceID == "" {
				req.instanceID = uuid.NewString()
			}
		}
	}
	return req, nil
}

func decodeAgentSelector(raw json.RawMessage) (AgentSpec, bool, error) {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		name = strings.TrimSpace(name)
		if name == "" {
			return AgentSpec{}, false, fmt.Errorf(
				"dynamicworkflow: agent name is required",
			)
		}
		return AgentSpec{Template: name}, false, nil
	}
	var spec AgentSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return AgentSpec{}, false, fmt.Errorf(
			"dynamicworkflow: decode agent spec: %w", err,
		)
	}
	if err := normalizeAgentSpec(&spec); err != nil {
		return AgentSpec{}, false, err
	}
	return spec, agentSpecHasOverrides(spec), nil
}

func normalizeAgentSpec(spec *AgentSpec) error {
	if spec == nil {
		return fmt.Errorf("dynamicworkflow: agent options are required")
	}
	spec.Template = strings.TrimSpace(spec.Template)
	if spec.Template == "" {
		return fmt.Errorf("dynamicworkflow: agent spec template is required")
	}
	spec.InstanceID = cleanPathSegment(spec.InstanceID)
	spec.Instruction = strings.TrimSpace(spec.Instruction)
	spec.Tools = normalizeSelection(spec.Tools)
	spec.Skills = normalizeSelection(spec.Skills)
	return normalizeStructuredOutputSpec(spec.Template, spec.StructuredOutput)
}

func agentSpecHasOverrides(spec AgentSpec) bool {
	return spec.Instruction != "" || spec.Tools != nil || spec.Skills != nil || spec.StructuredOutput != nil
}

func normalizeStructuredOutputSpec(template string, spec *StructuredOutputSpec) error {
	if spec == nil {
		return nil
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Description = strings.TrimSpace(spec.Description)
	if len(spec.Schema) == 0 {
		return fmt.Errorf("dynamicworkflow: structured_output schema is required")
	}
	if len(spec.Schema) > maxStructuredOutputSchemaBytes {
		return fmt.Errorf(
			"dynamicworkflow: structured_output schema exceeds %d bytes",
			maxStructuredOutputSchemaBytes,
		)
	}
	if !json.Valid(spec.Schema) {
		return fmt.Errorf("dynamicworkflow: structured_output schema must be valid JSON")
	}
	var schema map[string]any
	if err := json.Unmarshal(spec.Schema, &schema); err != nil || schema == nil {
		return fmt.Errorf("dynamicworkflow: structured_output schema must be a JSON object")
	}
	if spec.Name == "" {
		spec.Name = template + "_output"
	}
	if spec.Strict == nil {
		strict := true
		spec.Strict = &strict
	}
	return nil
}

func normalizeSelection(values []string) []string {
	if values == nil {
		return nil
	}
	if len(values) == 0 {
		return []string{}
	}
	return dedupeNonEmpty(values)
}

func dedupeNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cleanPathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_")
	return replacer.Replace(value)
}

func (g *workflowGateway) workflowChildInvocationOption(
	ctx context.Context,
	tmpl agentTemplate,
	req agentCallRequest,
) (agent.InvocationOptions, error) {
	patch, err := g.workflowChildPatch(ctx, tmpl, req.spec)
	if err != nil {
		return nil, err
	}
	structuredOutput, err := dynamicStructuredOutput(req.spec.StructuredOutput)
	if err != nil {
		return nil, err
	}
	nodeID := g.toolName + "/dynamic-" + uuid.NewString()
	return func(inv *agent.Invocation) {
		agent.SetInvocationSurfaceRootNodeID(inv, nodeID)
		runOpts := inv.RunOptions
		agent.WithSurfacePatchForNode(nodeID, patch)(&runOpts)
		sanitizeWorkflowChildRunOptions(&runOpts)
		if structuredOutput != nil {
			agent.WithStructuredOutputJSONSchema(
				structuredOutput.name,
				structuredOutput.schema,
				structuredOutput.strict,
				structuredOutput.description,
			)(&runOpts)
		}
		inv.RunOptions = runOpts
	}, nil
}

type resolvedStructuredOutput struct {
	name        string
	schema      map[string]any
	strict      bool
	description string
}

func dynamicStructuredOutput(spec *StructuredOutputSpec) (*resolvedStructuredOutput, error) {
	if spec == nil {
		return nil, nil
	}
	var schema map[string]any
	if err := json.Unmarshal(spec.Schema, &schema); err != nil || schema == nil {
		return nil, fmt.Errorf("dynamicworkflow: structured_output schema must be a JSON object")
	}
	strict := true
	if spec.Strict != nil {
		strict = *spec.Strict
	}
	if strict {
		normalizeStrictObjectSchemas(schema)
	}
	return &resolvedStructuredOutput{
		name:        spec.Name,
		schema:      schema,
		strict:      strict,
		description: spec.Description,
	}, nil
}

// normalizeStrictObjectSchemas supplies the closure required by OpenAI-style
// strict JSON Schema response formats: object schemas disallow extra fields
// and require every declared property. The map was decoded from workflow JSON,
// so this never mutates application-owned schema values.
func normalizeStrictObjectSchemas(schema map[string]any) {
	if schema == nil {
		return
	}
	if schemaDeclaresObject(schema) {
		if _, exists := schema["additionalProperties"]; !exists {
			schema["additionalProperties"] = false
		}
		if properties, ok := schema["properties"].(map[string]any); ok {
			required := make([]string, 0, len(properties))
			for name := range properties {
				required = append(required, name)
			}
			sort.Strings(required)
			schema["required"] = required
		}
	}
	for _, key := range []string{
		"properties", "$defs", "definitions", "patternProperties", "dependentSchemas",
	} {
		if children, ok := schema[key].(map[string]any); ok {
			for _, child := range children {
				normalizeStrictSchemaValue(child)
			}
		}
	}
	for _, key := range []string{
		"additionalProperties", "items", "contains", "not", "if", "then", "else",
	} {
		normalizeStrictSchemaValue(schema[key])
	}
	for _, key := range []string{"prefixItems", "allOf", "anyOf", "oneOf"} {
		normalizeStrictSchemaValue(schema[key])
	}
}

func schemaDeclaresObject(schema map[string]any) bool {
	if schemaType, ok := schema["type"].(string); ok {
		return schemaType == "object"
	}
	types, ok := schema["type"].([]any)
	if !ok {
		return false
	}
	for _, schemaType := range types {
		if schemaType == "object" {
			return true
		}
	}
	return false
}

func normalizeStrictSchemaValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		normalizeStrictObjectSchemas(typed)
	case []any:
		for _, item := range typed {
			normalizeStrictSchemaValue(item)
		}
	}
}

func (g *workflowGateway) workflowChildPatch(
	ctx context.Context,
	tmpl agentTemplate,
	spec AgentSpec,
) (agent.SurfacePatch, error) {
	var patch agent.SurfacePatch
	selectedTools, err := g.selectAgentTools(ctx, tmpl, spec.Tools)
	if err != nil {
		return patch, err
	}
	patch.SetTools(selectedTools)
	patch.SetSuppressSubAgentTransfer()
	if spec.Instruction != "" {
		patch.SetInstruction(spec.Instruction)
	}
	if spec.Skills != nil {
		repo, err := g.selectAgentSkills(ctx, tmpl, spec.Skills)
		if err != nil {
			return patch, err
		}
		patch.SetSkillRepository(repo)
	}
	return patch, nil
}

func (g *workflowGateway) selectAgentTools(
	ctx context.Context,
	tmpl agentTemplate,
	requested []string,
) ([]tool.Tool, error) {
	selectable := g.selectableAgentTools(ctx, tmpl)
	if requested == nil {
		return sortedTools(selectable), nil
	}
	if len(requested) == 0 {
		return nil, nil
	}
	selected := make([]tool.Tool, 0, len(requested))
	var missing []string
	for _, name := range requested {
		t, ok := selectable[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		selected = append(selected, t)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf(
			"dynamicworkflow: agent template %q does not allow tool(s): %s",
			tmpl.name, strings.Join(missing, ", "),
		)
	}
	return selected, nil
}

func (g *workflowGateway) selectableAgentTools(
	ctx context.Context,
	tmpl agentTemplate,
) map[string]tool.Tool {
	provider, ok := tmpl.agent.(agent.InvocationToolSurfaceProvider)
	if !ok || provider == nil {
		return copyToolMap(tmpl.tools)
	}
	probe := g.workflowChildProbe(tmpl)
	tools, userToolNames := provider.InvocationToolSurface(ctx, probe)
	return selectableToolMap(tools, userToolNames, tmpl.name, g.toolName)
}

func (g *workflowGateway) selectAgentSkills(
	ctx context.Context,
	tmpl agentTemplate,
	requested []string,
) (skill.Repository, error) {
	provider, ok := tmpl.agent.(agent.InvocationSkillRepositoryProvider)
	if !ok || provider == nil {
		if len(requested) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"dynamicworkflow: agent template %q does not expose skills",
			tmpl.name,
		)
	}
	probe := g.workflowChildProbe(tmpl)
	repo := provider.InvocationSkillRepository(ctx, probe)
	if repo == nil {
		if len(requested) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf(
			"dynamicworkflow: agent template %q does not expose skills",
			tmpl.name,
		)
	}
	if requested == nil {
		return repo, nil
	}
	if len(requested) == 0 {
		return skill.NewFilteredRepository(
			repo,
			func(context.Context, skill.Summary) bool { return false },
		), nil
	}
	available := map[string]bool{}
	for _, s := range skill.SummariesForContext(ctx, repo) {
		available[s.Name] = true
	}
	selected := map[string]bool{}
	var missing []string
	for _, name := range requested {
		if !available[name] {
			missing = append(missing, name)
			continue
		}
		selected[name] = true
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf(
			"dynamicworkflow: agent template %q does not allow skill(s): %s",
			tmpl.name, strings.Join(missing, ", "),
		)
	}
	return skill.NewFilteredRepository(
		repo,
		func(_ context.Context, s skill.Summary) bool {
			return selected[s.Name]
		},
	), nil
}

func (g *workflowGateway) workflowChildProbe(tmpl agentTemplate) *agent.Invocation {
	parent := parentWithLiveSession(g.parent)
	if parent == nil {
		parent = agent.NewInvocation()
	}
	return parent.View(
		agent.WithInvocationAgent(tmpl.agent),
		clearInheritedWorkflowRunOptions(),
	)
}

func sanitizeWorkflowChildRunOptions(runOpts *agent.RunOptions) {
	if runOpts == nil {
		return
	}
	runOpts.AdditionalTools = nil
	runOpts.ExternalTools = nil
	runOpts.ExternalToolNames = nil
	runOpts.ToolFilter = nil
	runOpts.Model = nil
	runOpts.ModelName = ""
	runOpts.ModelSelector = nil
	runOpts.ModelContextWindow = 0
	runOpts.ModelRequestExtraFields = nil
	runOpts.ModelRequestHeaders = nil
	runOpts.Instruction = ""
	runOpts.GlobalInstruction = ""
	runOpts.CodeExecutor = nil
	runOpts.ToolExecutionFilter = nil
	runOpts.StructuredOutput = nil
	runOpts.StructuredOutputType = nil
}

func selectableToolMap(
	tools []tool.Tool,
	userToolNames map[string]bool,
	excludedNames ...string,
) map[string]tool.Tool {
	excluded := make(map[string]bool, len(excludedNames)+1)
	excluded[transfer.TransferToolName] = true
	for _, name := range excludedNames {
		if name = strings.TrimSpace(name); name != "" {
			excluded[name] = true
		}
	}
	out := make(map[string]tool.Tool, len(tools))
	for _, candidate := range tools {
		name := declarationName(candidate)
		if name == "" || excluded[name] {
			continue
		}
		if userToolNames != nil && !userToolNames[name] {
			continue
		}
		if _, exists := out[name]; exists {
			continue
		}
		out[name] = candidate
	}
	return out
}

func copyToolMap(in map[string]tool.Tool) map[string]tool.Tool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]tool.Tool, len(in))
	for name, t := range in {
		out[name] = t
	}
	return out
}

func sortedTools(tools map[string]tool.Tool) []tool.Tool {
	if len(tools) == 0 {
		return nil
	}
	names := sortedNames(tools)
	out := make([]tool.Tool, 0, len(names))
	for _, name := range names {
		out = append(out, tools[name])
	}
	return out
}
