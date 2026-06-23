// Package toolcode exposes capability-limited tool orchestration through generated code.
package toolcode

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	bridge "trpc.group/trpc-go/trpc-agent-go/codeexecutor/codeact"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Option configures the execute_tool_code tool.
type Option func(*config)
type config struct {
	name        string
	description string
}

func WithName(name string) Option { return func(c *config) { c.name = name } }
func WithDescription(description string) Option {
	return func(c *config) { c.description = description }
}

// NewTool creates an execute_tool_code tool. Only tools supplied here can be called
// by guest code; the guest cannot access the agent's other tools or credentials.
func NewTool(runtime bridge.Runtime, managedTools []tool.CallableTool, opts ...Option) (tool.CallableTool, error) {
	cfg := config{name: "execute_tool_code", description: defaultDescription}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if strings.TrimSpace(cfg.name) == "" {
		return nil, fmt.Errorf("codeact: tool name is required")
	}
	gateway, err := bridge.NewGateway(managedTools...)
	if err != nil {
		return nil, err
	}
	return &executeCodeTool{
		runtime:  runtime,
		gateway:  gateway,
		toolHelp: buildToolHelp(managedTools),
		cfg:      cfg,
	}, nil
}

const defaultDescription = "Run Python glue code to orchestrate explicitly allowlisted host tools. Use it for loops, branching, JSON transformation, and aggregation across dependent tool calls. Prefer one execute_tool_code call when it can complete the workflow. Inside the code, call only documented tools with await call_tool(\"tool_name\", **json_arguments); use keyword arguments and JSON-compatible values. Do not use direct HTTP clients, shell commands, filesystem APIs, environment variables, or imports to reach services or credentials: those are outside this tool's capability contract. The application chooses this independent tool registry; it is not inferred from the agent's direct tools."

type executeCodeTool struct {
	runtime  bridge.Runtime
	gateway  *bridge.Gateway
	toolHelp string
	cfg      config
}

func (t *executeCodeTool) Declaration() *tool.Declaration {
	description := t.cfg.description
	if t.toolHelp != "" {
		description += "\n\nHost capabilities available inside Python (call only with await call_tool(name, **json_arguments)):\n" + t.toolHelp
	}
	return &tool.Declaration{Name: t.cfg.name, Description: description, InputSchema: &tool.Schema{Type: "object", Required: []string{"code"}, Properties: map[string]*tool.Schema{"code": {Type: "string", Description: "Top-level async Python source. Return a JSON-serializable value and use await call_tool(name, **json_arguments) only for documented host capabilities."}}}, OutputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{"value": {Description: "JSON-serializable value returned by the Python program"}, "stdout": {Type: "string", Description: "Captured Python stdout"}}}}
}

func (t *executeCodeTool) Call(ctx context.Context, raw []byte) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return bridge.Execute(ctx, t.runtime, t.gateway, in.Code)
}

func buildToolHelp(tools []tool.CallableTool) string {
	type entry struct {
		name string
		decl *tool.Declaration
	}
	entries := make([]entry, 0, len(tools))
	for _, candidate := range tools {
		if candidate == nil || candidate.Declaration() == nil {
			continue
		}
		decl := candidate.Declaration()
		entries = append(entries, entry{name: decl.Name, decl: decl})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	var b strings.Builder
	for _, entry := range entries {
		inputSchema, err := json.Marshal(entry.decl.InputSchema)
		if err != nil {
			inputSchema = []byte("null")
		}
		fmt.Fprintf(&b, "- %s: %s\n  Input JSON Schema: %s\n", entry.name, entry.decl.Description, inputSchema)
		if entry.decl.OutputSchema != nil {
			outputSchema, err := json.Marshal(entry.decl.OutputSchema)
			if err != nil {
				outputSchema = []byte("null")
			}
			fmt.Fprintf(&b, "  Output JSON Schema: %s\n", outputSchema)
		}
	}
	return strings.TrimSpace(b.String())
}
