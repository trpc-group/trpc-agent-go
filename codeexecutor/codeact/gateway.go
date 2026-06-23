package codeact

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Gateway is the host-side capability boundary exposed to generated code.
// It deliberately accepts and returns JSON so a guest language never receives
// Go object references or host credentials.
type Gateway struct {
	tools map[string]registeredTool
}

type registeredTool struct {
	tool         tool.CallableTool
	inputSchema  *jsonschema.Schema
	outputSchema *jsonschema.Schema
}

// NewGateway creates an allowlist gateway. Duplicate and unnamed tools are
// rejected because ambiguity is unsafe for generated code.
func NewGateway(tools ...tool.CallableTool) (*Gateway, error) {
	g := &Gateway{tools: make(map[string]registeredTool, len(tools))}
	for _, candidate := range tools {
		if candidate == nil || candidate.Declaration() == nil {
			return nil, fmt.Errorf("codeact: tool declaration is required")
		}
		name := strings.TrimSpace(candidate.Declaration().Name)
		if name == "" {
			return nil, fmt.Errorf("codeact: tool name is required")
		}
		if _, ok := g.tools[name]; ok {
			return nil, fmt.Errorf("codeact: duplicate tool %q", name)
		}
		decl := candidate.Declaration()
		inputSchema, err := compileSchema(decl.InputSchema, name, "input")
		if err != nil {
			return nil, err
		}
		outputSchema, err := compileSchema(decl.OutputSchema, name, "output")
		if err != nil {
			return nil, err
		}
		g.tools[name] = registeredTool{
			tool:         candidate,
			inputSchema:  inputSchema,
			outputSchema: outputSchema,
		}
	}
	return g, nil
}

// Names returns the stable list of capabilities available to guest code.
func (g *Gateway) Names() []string {
	names := make([]string, 0, len(g.tools))
	for name := range g.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Call validates a JSON argument object, invokes one allowlisted tool, then
// validates and serializes its result before returning it to the guest.
func (g *Gateway) Call(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if g == nil {
		return nil, fmt.Errorf("codeact: nil gateway")
	}
	registered, ok := g.tools[name]
	if !ok {
		return nil, fmt.Errorf("codeact: tool %q is not allowlisted", name)
	}
	if err := validateJSON(args, registered.inputSchema, "input"); err != nil {
		return nil, err
	}
	value, err := registered.tool.Call(ctx, args)
	if err != nil {
		return nil, fmt.Errorf("codeact: call %q: %w", name, err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("codeact: encode result from %q: %w", name, err)
	}
	if registered.outputSchema != nil {
		if err := validateJSON(raw, registered.outputSchema, "output"); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// HandleToolCall implements ToolCallHandler.
func (g *Gateway) HandleToolCall(ctx context.Context, call ToolCall) (json.RawMessage, error) {
	return g.Call(ctx, call.Name, call.Args)
}

func compileSchema(schema *tool.Schema, toolName, direction string) (*jsonschema.Schema, error) {
	if schema == nil {
		return nil, nil
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("codeact: encode %s schema for %q: %w", direction, toolName, err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("codeact: decode %s schema for %q: %w", direction, toolName, err)
	}
	location := fmt.Sprintf("https://codeact.invalid/tools/%s/%s.json", url.PathEscape(toolName), direction)
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(rejectExternalSchemaLoader{})
	if err := compiler.AddResource(location, document); err != nil {
		return nil, fmt.Errorf("codeact: register %s schema for %q: %w", direction, toolName, err)
	}
	compiled, err := compiler.Compile(location)
	if err != nil {
		return nil, fmt.Errorf("codeact: compile %s schema for %q: %w", direction, toolName, err)
	}
	return compiled, nil
}

type rejectExternalSchemaLoader struct{}

func (rejectExternalSchemaLoader) Load(location string) (any, error) {
	return nil, fmt.Errorf("codeact: external schema reference %q is not allowed", location)
}

func validateJSON(raw []byte, schema *jsonschema.Schema, direction string) error {
	if schema == nil {
		return nil
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("codeact: invalid %s JSON: %w", direction, err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("codeact: invalid %s: %w", direction, err)
	}
	return nil
}
