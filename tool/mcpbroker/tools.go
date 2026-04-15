//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mcpbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

type listServersInput struct{}

type listServersOutput struct {
	Servers []listServersServer `json:"servers"`
}

type listServersServer struct {
	Name        string `json:"name"`
	Transport   string `json:"transport"`
	Description string `json:"description,omitempty"`
}

type listToolsInput struct {
	Selector  string            `json:"selector"`
	Transport string            `json:"transport,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type listToolsOutput struct {
	Tools []listedTool `json:"tools"`
}

type listedTool struct {
	Name            string `json:"name"`
	Selector        string `json:"selector,omitempty"`
	Signature       string `json:"signature,omitempty"`
	Description     string `json:"description,omitempty"`
	HasOutputSchema bool   `json:"has_output_schema"`
}

type inspectToolsInput struct {
	Selector            string            `json:"selector"`
	Tools               []string          `json:"tools"`
	Transport           string            `json:"transport,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	IncludeOutputSchema bool              `json:"include_output_schema,omitempty"`
}

type inspectToolsOutput struct {
	Tools []inspectedTool `json:"tools"`
}

type inspectedTool struct {
	Name            string         `json:"name"`
	Selector        string         `json:"selector,omitempty"`
	Description     string         `json:"description,omitempty"`
	HasOutputSchema bool           `json:"has_output_schema"`
	InputSchema     map[string]any `json:"input_schema,omitempty"`
	OutputSchema    map[string]any `json:"output_schema,omitempty"`
}

type callToolInput struct {
	Selector  string            `json:"selector"`
	Transport string            `json:"transport,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Arguments map[string]any    `json:"arguments,omitempty"`
}

type callToolOutput struct {
	Content           []any `json:"content,omitempty"`
	StructuredContent any   `json:"structured_content,omitempty"`
	IsError           bool  `json:"is_error,omitempty"`
}

func newBrokerTools(b *Broker) []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			b.listServers,
			function.WithName(listServersToolName),
			function.WithDescription("List named MCP servers already configured for this broker. Use this first when you expect the platform to have preconfigured MCP connections. When configured, server descriptions help choose which server to inspect next. This tool only returns named broker servers; it does not inspect arbitrary URLs."),
			function.WithInputSchema(emptyObjectSchema()),
			function.WithOutputSchema(listServersOutputSchema()),
		),
		function.NewFunctionTool(
			b.listTools,
			function.WithName(listToolsToolName),
			function.WithDescription("List lightweight MCP tool summaries for a selector. Use a named server selector like local_stdio_code, or an ad-hoc HTTP MCP endpoint like https://example.com/mcp. This tool is for discovery only: it returns names, descriptions, signatures, selectors, and output-schema availability, but not raw JSON schema. Use mcp_inspect_tools on the specific tools you plan to call."),
			function.WithInputSchema(listToolsInputSchema()),
			function.WithOutputSchema(listToolsOutputSchema()),
		),
		function.NewFunctionTool(
			b.inspectTools,
			function.WithName(inspectToolsToolName),
			function.WithDescription("Inspect specific MCP tools and return their input schema. Use this after mcp_list_tools when you need exact parameter structure for selected tools. Pass tool names exactly as returned by mcp_list_tools. By default this tool only returns input schema; set include_output_schema=true only when output schema is also needed."),
			function.WithInputSchema(inspectToolsInputSchema()),
			function.WithOutputSchema(inspectToolsOutputSchema()),
		),
		function.NewFunctionTool(
			b.callTool,
			function.WithName(callToolToolName),
			function.WithDescription("Call exactly one MCP tool by selector. Use a tool selector like local_stdio_code.add or https://example.com/mcp.add. Prefer selectors returned by mcp_list_tools or mcp_inspect_tools. If an ad-hoc HTTP endpoint would make dot-based parsing ambiguous, you may also use https://example.com/mcp#tool=add. Always pass the remote MCP tool parameters inside the arguments object, never as top-level wrapper fields. Example: mcp_call(selector=\"local_stdio_code.add\", arguments={\"a\": 12, \"b\": 30}). If you need exact parameter structure first, call mcp_inspect_tools before mcp_call. When the selected MCP tool takes no parameters, pass arguments={} explicitly."),
			function.WithInputSchema(callToolInputSchema()),
			function.WithOutputSchema(callToolOutputSchema()),
		),
	}
}

func (b *Broker) listTools(ctx context.Context, input listToolsInput) (listToolsOutput, error) {
	target, selectorPrefix, err := b.resolveListSelector(input.Selector, input.Transport, input.Headers)
	if err != nil {
		return listToolsOutput{}, err
	}

	cfg, err := b.withPreparedHTTPHeaders(ctx, target, operationMetadata{
		Selector: input.Selector,
		BaseURL:  target.Config.ServerURL,
		Phase:    phaseListTools,
	})
	if err != nil {
		return listToolsOutput{}, err
	}

	mcpTools, err := withOneShotClient(ctx, cfg, func(opCtx context.Context, client tmcp.Connector) ([]tmcp.Tool, error) {
		result, listErr := client.ListTools(opCtx, &tmcp.ListToolsRequest{})
		if listErr != nil {
			return nil, fmt.Errorf("list MCP tools: %w", listErr)
		}
		return result.Tools, nil
	})
	if err != nil {
		if handled, interceptErr := interceptHTTPOperationError(ctx, b, target, operationMetadata{
			Selector: input.Selector,
			BaseURL:  target.Config.ServerURL,
			Phase:    phaseListTools,
		}, err); handled {
			return listToolsOutput{}, interceptErr
		}
		return listToolsOutput{}, err
	}

	sort.Slice(mcpTools, func(i, j int) bool {
		return mcpTools[i].Name < mcpTools[j].Name
	})

	output := listToolsOutput{
		Tools: make([]listedTool, 0, len(mcpTools)),
	}
	for _, mcpTool := range mcpTools {
		output.Tools = append(output.Tools, listedTool{
			Name:            mcpTool.Name,
			Selector:        joinToolSelector(selectorPrefix, mcpTool.Name),
			Signature:       renderToolSignature(mcpTool),
			Description:     mcpTool.Description,
			HasOutputSchema: mcpTool.OutputSchema != nil,
		})
	}
	return output, nil
}

func (b *Broker) inspectTools(ctx context.Context, input inspectToolsInput) (inspectToolsOutput, error) {
	if len(input.Tools) == 0 {
		return inspectToolsOutput{}, fmt.Errorf("tools is required and must contain at least one tool name")
	}

	target, selectorPrefix, err := b.resolveListSelector(input.Selector, input.Transport, input.Headers)
	if err != nil {
		return inspectToolsOutput{}, err
	}

	cfg, err := b.withPreparedHTTPHeaders(ctx, target, operationMetadata{
		Selector: input.Selector,
		BaseURL:  target.Config.ServerURL,
		Phase:    phaseInspectTools,
	})
	if err != nil {
		return inspectToolsOutput{}, err
	}

	mcpTools, err := withOneShotClient(ctx, cfg, func(opCtx context.Context, client tmcp.Connector) ([]tmcp.Tool, error) {
		result, listErr := client.ListTools(opCtx, &tmcp.ListToolsRequest{})
		if listErr != nil {
			return nil, fmt.Errorf("list MCP tools: %w", listErr)
		}
		return result.Tools, nil
	})
	if err != nil {
		if handled, interceptErr := interceptHTTPOperationError(ctx, b, target, operationMetadata{
			Selector: input.Selector,
			BaseURL:  target.Config.ServerURL,
			Phase:    phaseInspectTools,
		}, err); handled {
			return inspectToolsOutput{}, interceptErr
		}
		return inspectToolsOutput{}, err
	}

	selectedTools, err := selectToolsForInspection(mcpTools, input.Tools)
	if err != nil {
		return inspectToolsOutput{}, err
	}

	output := inspectToolsOutput{
		Tools: make([]inspectedTool, 0, len(selectedTools)),
	}
	for _, mcpTool := range selectedTools {
		item := inspectedTool{
			Name:            mcpTool.Name,
			Selector:        joinToolSelector(selectorPrefix, mcpTool.Name),
			Description:     mcpTool.Description,
			HasOutputSchema: mcpTool.OutputSchema != nil,
			InputSchema:     schemaToMap(mcpTool.InputSchema),
		}
		if input.IncludeOutputSchema {
			item.OutputSchema = schemaToMap(mcpTool.OutputSchema)
		}
		output.Tools = append(output.Tools, item)
	}
	return output, nil
}

func (b *Broker) callTool(ctx context.Context, input callToolInput) (callToolOutput, error) {
	if input.Arguments == nil {
		return callToolOutput{}, fmt.Errorf("arguments is required; pass {} when the MCP tool takes no parameters")
	}

	target, _, toolName, err := b.resolveCallSelector(input.Selector, input.Transport, input.Headers)
	if err != nil {
		return callToolOutput{}, err
	}

	cfg, err := b.withPreparedHTTPHeaders(ctx, target, operationMetadata{
		Selector: input.Selector,
		BaseURL:  target.Config.ServerURL,
		ToolName: toolName,
		Phase:    phaseCallTool,
	})
	if err != nil {
		return callToolOutput{}, err
	}

	result, err := withOneShotClient(ctx, cfg, func(opCtx context.Context, client tmcp.Connector) (*tmcp.CallToolResult, error) {
		if validateErr := validateCallToolArguments(opCtx, client, toolName, input.Arguments); validateErr != nil {
			return nil, validateErr
		}
		callResult, callErr := client.CallTool(opCtx, &tmcp.CallToolRequest{
			Params: tmcp.CallToolParams{
				Name:      toolName,
				Arguments: input.Arguments,
			},
		})
		if callErr != nil {
			return nil, fmt.Errorf("call MCP tool %q: %w", toolName, callErr)
		}
		return callResult, nil
	})
	if err != nil {
		if handled, interceptErr := interceptHTTPOperationError(ctx, b, target, operationMetadata{
			Selector: input.Selector,
			BaseURL:  target.Config.ServerURL,
			ToolName: toolName,
			Phase:    phaseCallTool,
		}, err); handled {
			return callToolOutput{}, interceptErr
		}
		return callToolOutput{}, err
	}

	output := callToolOutput{
		StructuredContent: result.StructuredContent,
		IsError:           result.IsError,
	}
	if result.StructuredContent == nil && len(result.Content) > 0 {
		output.Content = make([]any, len(result.Content))
		for i, content := range result.Content {
			output.Content[i] = content
		}
	}
	return output, nil
}

func validateCallToolArguments(
	ctx context.Context,
	client tmcp.Connector,
	toolName string,
	arguments map[string]any,
) error {
	listResult, err := client.ListTools(ctx, &tmcp.ListToolsRequest{})
	if err != nil {
		return fmt.Errorf("list MCP tools for validation: %w", err)
	}

	var target *tmcp.Tool
	for i := range listResult.Tools {
		if listResult.Tools[i].Name == toolName {
			target = &listResult.Tools[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("MCP tool %q not found", toolName)
	}

	if target.InputSchema == nil || len(target.InputSchema.Required) == 0 {
		return nil
	}

	missing := make([]string, 0)
	for _, field := range target.InputSchema.Required {
		if arguments == nil {
			missing = append(missing, field)
			continue
		}
		if _, ok := arguments[field]; !ok {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required arguments for MCP tool %q: %s", toolName, strings.Join(missing, ", "))
	}
	return nil
}

func emptyObjectSchema() *tool.Schema {
	return &tool.Schema{
		Type:                 "object",
		AdditionalProperties: false,
	}
}

func listToolsInputSchema() *tool.Schema {
	return &tool.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required:             []string{"selector"},
		Properties: map[string]*tool.Schema{
			"selector":  {Type: "string", Description: "Required. Either a named server selector like local_stdio_code, or an ad-hoc HTTP MCP endpoint like https://example.com/mcp. Do not append the tool name here; this tool lists tools for the server/endpoint."},
			"transport": {Type: "string", Description: "Optional ad-hoc HTTP transport override for URL selectors. Ignored for named selectors. Prefer omitting this unless you know the endpoint needs a specific HTTP mode. Supported values: streamable, sse.", Enum: []any{"streamable", "sse", "streamable_http", "http"}},
			"headers": {
				Type:                 "object",
				Description:          "Optional non-sensitive headers for ad-hoc HTTP selectors. Ignored for named selectors. Do not put secrets here unless the platform explicitly allows it.",
				AdditionalProperties: &tool.Schema{Type: "string"},
			},
		},
	}
}

func inspectToolsInputSchema() *tool.Schema {
	return &tool.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required:             []string{"selector", "tools"},
		Properties: map[string]*tool.Schema{
			"selector":  {Type: "string", Description: "Required. Either a named server selector like local_stdio_code, or an ad-hoc HTTP MCP endpoint like https://example.com/mcp. Do not append the tool name here; this tool inspects specific tools on the server/endpoint."},
			"transport": {Type: "string", Description: "Optional ad-hoc HTTP transport override for URL selectors. Ignored for named selectors. Prefer omitting this unless you know the endpoint needs a specific HTTP mode. Supported values: streamable, sse.", Enum: []any{"streamable", "sse", "streamable_http", "http"}},
			"headers": {
				Type:                 "object",
				Description:          "Optional non-sensitive headers for ad-hoc HTTP selectors. Ignored for named selectors. Do not put secrets here unless the platform explicitly allows it.",
				AdditionalProperties: &tool.Schema{Type: "string"},
			},
			"tools": {
				Type:        "array",
				Description: "Required. The exact MCP tool names to inspect, for example [\"add\"] or [\"issue_create\", \"issue_comment\"]. Prefer names returned by mcp_list_tools.",
				Items:       &tool.Schema{Type: "string"},
			},
			"include_output_schema": {Type: "boolean", Description: "Optional. Set true only when you also need the output schema. By default this tool only returns input schema."},
		},
	}
}

func callToolInputSchema() *tool.Schema {
	return &tool.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required:             []string{"selector", "arguments"},
		Properties: map[string]*tool.Schema{
			"selector":  {Type: "string", Description: "Required. A tool selector like local_stdio_code.add or https://example.com/mcp.add. Prefer using the selector returned by mcp_list_tools instead of inventing one. If an ad-hoc HTTP endpoint would make dot-based parsing ambiguous, you may also use https://example.com/mcp#tool=add."},
			"transport": {Type: "string", Description: "Optional ad-hoc HTTP transport override for URL selectors. Ignored for named selectors. Prefer omitting this unless the endpoint requires a specific HTTP mode. Supported values: streamable, sse.", Enum: []any{"streamable", "sse", "streamable_http", "http"}},
			"headers": {
				Type:                 "object",
				Description:          "Optional non-sensitive headers for ad-hoc HTTP selectors. Ignored for named selectors. Do not put remote MCP tool parameters here.",
				AdditionalProperties: &tool.Schema{Type: "string"},
			},
			"arguments": {
				Type:                 "object",
				Description:          "Required. Always pass an object. Put the selected remote MCP tool parameters here, for example {\"a\": 12, \"b\": 30}. Never place remote MCP tool parameters at the top level beside selector/transport/headers. Use {} when the selected MCP tool takes no parameters. If you need the exact parameter shape first, use mcp_inspect_tools.",
				AdditionalProperties: true,
			},
		},
	}
}

func listServersOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required:             []string{"servers"},
		Properties: map[string]*tool.Schema{
			"servers": {
				Type: "array",
				Items: &tool.Schema{
					Type:                 "object",
					AdditionalProperties: false,
					Required:             []string{"name", "transport"},
					Properties: map[string]*tool.Schema{
						"name":        {Type: "string", Description: "Configured server name."},
						"transport":   {Type: "string", Description: "Resolved MCP transport."},
						"description": {Type: "string", Description: "Capability summary of the server. Use this to decide which server to explore next. Present only when configured."},
					},
				},
			},
		},
	}
}

func listToolsOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required:             []string{"tools"},
		Properties: map[string]*tool.Schema{
			"tools": {
				Type: "array",
				Items: &tool.Schema{
					Type:                 "object",
					AdditionalProperties: false,
					Required:             []string{"name", "has_output_schema"},
					Properties: map[string]*tool.Schema{
						"name":              {Type: "string", Description: "MCP tool name."},
						"selector":          {Type: "string", Description: "Tool selector to use with mcp_call."},
						"signature":         {Type: "string", Description: "Compact signature derived from the MCP tool input and output schemas."},
						"description":       {Type: "string", Description: "MCP tool description."},
						"has_output_schema": {Type: "boolean", Description: "Whether the MCP tool advertises an output schema."},
					},
				},
			},
		},
	}
}

func inspectToolsOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Required:             []string{"tools"},
		Properties: map[string]*tool.Schema{
			"tools": {
				Type: "array",
				Items: &tool.Schema{
					Type:                 "object",
					AdditionalProperties: false,
					Required:             []string{"name", "has_output_schema"},
					Properties: map[string]*tool.Schema{
						"name":              {Type: "string", Description: "MCP tool name."},
						"selector":          {Type: "string", Description: "Tool selector to use with mcp_call."},
						"description":       {Type: "string", Description: "MCP tool description."},
						"has_output_schema": {Type: "boolean", Description: "Whether the MCP tool advertises an output schema."},
						"input_schema": {
							Type:                 "object",
							Description:          "MCP tool input schema.",
							AdditionalProperties: true,
						},
						"output_schema": {
							Type:                 "object",
							Description:          "MCP tool output schema. Present only when include_output_schema=true and the MCP tool advertises one.",
							AdditionalProperties: true,
						},
					},
				},
			},
		},
	}
}

func callToolOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type:                 "object",
		AdditionalProperties: false,
		Properties: map[string]*tool.Schema{
			"content": {
				Type:        "array",
				Description: "MCP content blocks returned by the remote tool. Present when the remote tool does not return structured content.",
				Items: &tool.Schema{
					Type:                 "object",
					AdditionalProperties: true,
				},
			},
			"structured_content": {
				Type:                 "object",
				Description:          "Structured content returned by the remote tool. When present, content is omitted to avoid duplicate result payloads.",
				AdditionalProperties: true,
			},
			"is_error": {Type: "boolean", Description: "Present and true when the remote MCP tool reported an error result."},
		},
	}
}

func joinToolSelector(prefix string, toolName string) string {
	prefix = strings.TrimSpace(prefix)
	toolName = strings.TrimSpace(toolName)
	if prefix == "" || toolName == "" {
		return ""
	}
	if looksLikeHTTPSelector(prefix) && shouldUseFragmentHTTPToolSelector(prefix) {
		return prefix + "#tool=" + toolName
	}
	return prefix + "." + toolName
}

func renderToolSignature(mcpTool tmcp.Tool) string {
	params := make([]string, 0)
	if mcpTool.InputSchema != nil {
		requiredSet := make(map[string]struct{}, len(mcpTool.InputSchema.Required))
		for _, name := range mcpTool.InputSchema.Required {
			requiredSet[name] = struct{}{}
			if ref, ok := mcpTool.InputSchema.Properties[name]; ok && ref != nil && ref.Value != nil {
				params = append(params, fmt.Sprintf("%s: %s", name, schemaTypeName(ref.Value)))
				continue
			}
			params = append(params, fmt.Sprintf("%s: unknown", name))
		}

		optionalNames := make([]string, 0, len(mcpTool.InputSchema.Properties))
		for name := range mcpTool.InputSchema.Properties {
			if _, ok := requiredSet[name]; ok {
				continue
			}
			optionalNames = append(optionalNames, name)
		}
		sort.Strings(optionalNames)
		for _, name := range optionalNames {
			ref := mcpTool.InputSchema.Properties[name]
			if ref == nil || ref.Value == nil {
				params = append(params, fmt.Sprintf("%s?: unknown", name))
				continue
			}
			params = append(params, fmt.Sprintf("%s?: %s", name, schemaTypeName(ref.Value)))
		}
	}

	signature := fmt.Sprintf("%s(%s)", mcpTool.Name, strings.Join(params, ", "))
	returnType := schemaTypeName(mcpTool.OutputSchema)
	if returnType == "" || returnType == "object" || returnType == "unknown" {
		return signature
	}
	return signature + " -> " + returnType
}

func schemaTypeName(schema any) string {
	if schema == nil {
		return "unknown"
	}
	schemaMap := schemaToMap(schema)
	if len(schemaMap) == 0 {
		return "unknown"
	}

	schemaType, ok := firstSchemaType(schemaMap["type"])
	if !ok {
		return "unknown"
	}
	switch schemaType {
	case "array":
		items, _ := schemaMap["items"].(map[string]any)
		if len(items) == 0 {
			return "array"
		}
		return "array<" + schemaTypeName(items) + ">"
	case "object":
		return "object"
	default:
		return schemaType
	}
}

func firstSchemaType(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		typed = strings.TrimSpace(typed)
		return typed, typed != ""
	case []any:
		for _, item := range typed {
			text, ok := firstSchemaType(item)
			if ok {
				return text, true
			}
		}
	}
	return "", false
}

func schemaToMap(schema any) map[string]any {
	if schema == nil {
		return nil
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}
	return result
}

func selectToolsForInspection(mcpTools []tmcp.Tool, requested []string) ([]tmcp.Tool, error) {
	index := make(map[string]tmcp.Tool, len(mcpTools))
	for _, mcpTool := range mcpTools {
		index[strings.TrimSpace(mcpTool.Name)] = mcpTool
	}

	selected := make([]tmcp.Tool, 0, len(requested))
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(requested))
	for _, rawName := range requested {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("tools must not contain empty tool names")
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		mcpTool, ok := index[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		selected = append(selected, mcpTool)
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("requested MCP tools not found: %s", strings.Join(missing, ", "))
	}
	return selected, nil
}
