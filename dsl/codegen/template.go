package codegen

// singleFileTemplateData holds all data needed to render the generated Go code.
type singleFileTemplateData struct {
	PackageName                         string
	AppName                             string
	RunMode                             string // "interactive" or "agui"
	HasAgentNodes                       bool
	EnvVarInfos                         []envVarInfo // Detailed env var info for documentation
	NeedsApproval                       bool
	NeedsEnd                            bool
	NeedsMCP                            bool
	NeedsReflect                        bool
	NeedsExtractFirstJSONObjectFromText bool
	StateVars                           []stateVar
	StartNodes                          []startNode
	AgentNodes                          []agentNode
	TransformNodes                      []transformNode
	SetStateNodes                       []setStateNode
	MCPNodes                            []mcpNode
	ApprovalNodes                       []approvalNode
	EndNodes                            []endNode
	Edges                               []edge
	Conditions                          []condition
	EntryPoint                          string

	// Helper function usage flags.
	NeedsMustParseJSONAny       bool
	NeedsStructuredOutputMapper bool
}

const singleFileTemplate = `
// Generated from DSL workflow "{{ .AppName }}".
//
// How to run:
//   1. Put this file in an empty folder as main.go
//   2. go mod init example.com/mydslapp && go get trpc.group/trpc-go/trpc-agent-go@latest && go mod tidy
//   3. Set environment variables:
{{- if .EnvVarInfos }}
{{- range .EnvVarInfos }}
{{- if .Agents }}
//      export {{ .Name }}="..."  # {{ .BaseURL }}{{ if gt (len .Agents) 0 }} (used by: {{ range $i, $a := .Agents }}{{ if $i }}, {{ end }}{{ $a }}{{ end }}){{ end }}
{{- else }}
//      export {{ .Name }}="..."
{{- end }}
{{- end }}
{{- else }}
//      (none required)
{{- end }}
//   4. go run .
package {{ .PackageName }}

import (
	{{- if eq .RunMode "interactive" }}
	"bufio"
	{{- end }}
	"context"
	"encoding/json"
	{{- if eq .RunMode "agui" }}
	"flag"
	{{- end }}
	"fmt"
	{{- if eq .RunMode "agui" }}
	"net/http"
	{{- end }}
	{{- if .HasAgentNodes }}
	"os"
	{{- end }}
	{{- if .NeedsReflect }}
	"reflect"
	{{- end }}
	"strings"
	{{- if or .NeedsMCP (eq .RunMode "interactive") }}
	"time"
	{{- end }}

	{{- if .HasAgentNodes }}
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	{{- end }}
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	{{- if eq .RunMode "interactive" }}
	"trpc.group/trpc-go/trpc-agent-go/event"
	{{- end }}
	"trpc.group/trpc-go/trpc-agent-go/graph"
	{{- if eq .RunMode "agui" }}
	"trpc.group/trpc-go/trpc-agent-go/log"
	{{- end }}
	"trpc.group/trpc-go/trpc-agent-go/model"
	{{- if .HasAgentNodes }}
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	{{- end }}
	"trpc.group/trpc-go/trpc-agent-go/runner"
	{{- if eq .RunMode "agui" }}
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	{{- end }}
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	{{- if .NeedsMCP }}
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	{{- end }}
)

// =============================================================================
// Configuration
// =============================================================================

const appName = {{ printf "%q" .AppName }}

{{- if eq .RunMode "agui" }}

var (
	address = flag.String("address", "127.0.0.1:8080", "Listen address")
	path    = flag.String("path", "/agui", "HTTP path")
)
{{- end }}

// =============================================================================
// Entry Point
// =============================================================================

func main() {
{{- if eq .RunMode "agui" }}
	flag.Parse()

	g, err := BuildGraph()
	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}

	{{- if .HasAgentNodes }}
	ga, err := graphagent.New(appName, g, graphagent.WithSubAgents(createSubAgents()))
	{{- else }}
	ga, err := graphagent.New(appName, g)
	{{- end }}
	if err != nil {
		log.Fatalf("Failed to create graph agent: %v", err)
	}

	r := runner.NewRunner(appName, ga, runner.WithSessionService(inmemory.NewSessionService()))
	defer r.Close()

	server, err := agui.New(r, agui.WithPath(*path))
	if err != nil {
		log.Fatalf("Failed to create AG-UI server: %v", err)
	}

	log.Infof("AG-UI: serving agent %q on http://%s%s", appName, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
{{- else }}
	fmt.Println("Starting graph:", appName)

	g, err := BuildGraph()
	if err != nil {
		panic(err)
	}

	{{- if .HasAgentNodes }}
	ga, err := graphagent.New(appName, g, graphagent.WithSubAgents(createSubAgents()))
	{{- else }}
	ga, err := graphagent.New(appName, g)
	{{- end }}
	if err != nil {
		panic(err)
	}

	r := runner.NewRunner(appName, ga, runner.WithSessionService(inmemory.NewSessionService()))
	defer r.Close()

	userID := "user"
	sessionID := fmt.Sprintf("session-%d", time.Now().Unix())

	fmt.Println("Interactive mode. Type 'exit' to quit, 'new' for new session.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("Goodbye!")
			return
		case "new":
			sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
			fmt.Printf("New session: %s\n\n", sessionID)
			continue
		}

		events, err := r.Run(context.Background(), userID, sessionID, model.NewUserMessage(input))
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Print("Assistant: ")
		if err := processStreamingResponse(events); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
{{- end }}
}

{{- if eq .RunMode "interactive" }}

func processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		didStream       bool
		lastText        string
		interruptNode   string
		interruptValue  any
	)

	for ev := range eventChan {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			fmt.Printf("\nError: %s\n", ev.Error.Message)
			continue
		}

		// Check for interrupt.
		if ev.StateDelta != nil {
			if raw, ok := ev.StateDelta[graph.MetadataKeyPregel]; ok && raw != nil {
				var meta graph.PregelStepMetadata
				if err := json.Unmarshal(raw, &meta); err == nil && meta.NodeID != "" && meta.InterruptValue != nil {
					interruptNode = meta.NodeID
					interruptValue = meta.InterruptValue
				}
			}
		}

		// Process streaming content.
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			for _, ch := range ev.Response.Choices {
				if ch.Delta.Content != "" {
					fmt.Print(ch.Delta.Content)
					didStream = true
				}
				if ch.Message.Role == model.RoleAssistant && ch.Message.Content != "" {
					lastText = ch.Message.Content
				}
			}
		}
	}

	if didStream {
		fmt.Println()
	}

	if interruptNode != "" {
		b, _ := json.MarshalIndent(interruptValue, "", "  ")
		fmt.Printf("\n[interrupt] node=%q value=%s\n", interruptNode, string(b))
		fmt.Println("Graph interrupted. Resume with approval value to continue.")
		return nil
	}

	if !didStream && lastText != "" {
		fmt.Println(lastText)
	}

	return nil
}
{{- end }}

// =============================================================================
// Graph Definition
// =============================================================================

func BuildGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()

	{{- if .StateVars }}
	// User-defined state variables.
	{{- range .StateVars }}
	schema.AddField({{ printf "%q" .Name }}, graph.StateField{
		Type:    {{ .TypeGo }},
		Reducer: {{ .ReducerGo }},
		{{- if .HasDefault }}
		Default: func() any {
			{{- if .DefaultJSON }}
			return mustParseJSONAny({{ goString .DefaultJSON }})
			{{- else }}
			return {{ .DefaultLiteral }}
			{{- end }}
		},
		{{- end }}
	})
	{{- end }}
	{{- end }}

	{{- if .NeedsApproval }}
	schema.AddField("approval_result", graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	{{- end }}
	{{- if .NeedsEnd }}
	schema.AddField("end_structured_output", graph.StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: graph.DefaultReducer,
	})
	{{- end }}

	sg := graph.NewStateGraph(schema)

	// Nodes.
	{{- range .StartNodes }}
	sg.AddNode({{ printf "%q" .ID }}, func(ctx context.Context, state graph.State) (any, error) { return nil, nil })
	{{- end }}
	{{- range .AgentNodes }}
	{{- if .StructuredOutputSchemaJSON }}
	sg.AddAgentNode({{ printf "%q" .ID }}, graph.WithSubgraphOutputMapper(agentStructuredOutputMapper({{ printf "%q" .ID }})))
	{{- else }}
	sg.AddAgentNode({{ printf "%q" .ID }})
	{{- end }}
	{{- end }}
	{{- range .TransformNodes }}
	sg.AddNode({{ printf "%q" .ID }}, {{ .FuncName }}, graph.WithNodeType(graph.NodeTypeFunction))
	{{- end }}
	{{- range .SetStateNodes }}
	sg.AddNode({{ printf "%q" .ID }}, {{ .FuncName }}, graph.WithNodeType(graph.NodeTypeFunction))
	{{- end }}
	{{- range .MCPNodes }}
	sg.AddNode({{ printf "%q" .ID }}, {{ .FuncName }}, graph.WithNodeType(graph.NodeTypeTool))
	{{- end }}
	{{- range .ApprovalNodes }}
	sg.AddNode({{ printf "%q" .ID }}, {{ .FuncName }}, graph.WithNodeType(graph.NodeTypeRouter))
	{{- end }}
	{{- range .EndNodes }}
	sg.AddNode({{ printf "%q" .ID }}, {{ .FuncName }}, graph.WithNodeType(graph.NodeTypeFunction))
	{{- end }}

	// Edges.
	{{- range .Edges }}
	sg.AddEdge({{ printf "%q" .From }}, {{ printf "%q" .To }})
	{{- end }}
	{{- range .Conditions }}
	sg.AddConditionalEdges({{ printf "%q" .From }}, {{ .FuncName }}, nil)
	{{- end }}

	sg.SetEntryPoint({{ printf "%q" .EntryPoint }})
	return sg.Compile()
}

// =============================================================================
// Routing Functions
// =============================================================================
{{- range $c := .Conditions }}

func {{ $c.FuncName }}(ctx context.Context, state graph.State) (string, error) {
	_ = ctx
	parsedOutput, _ := state["{{ $c.From }}_parsed"].(map[string]any)
	{{- if $c.SwitchFieldName }}
	{{ $c.SwitchFieldName }}, _ := parsedOutput["{{ $c.SwitchFieldName }}"].(string)
	switch {{ $c.SwitchFieldName }} {
	{{- range $c.Cases }}
	case {{ printf "%q" .MatchValue }}:
		return {{ printf "%q" .Target }}, nil
	{{- end }}
	default:
		{{- if $c.DefaultTarget }}
		return {{ printf "%q" $c.DefaultTarget }}, nil
		{{- else }}
		return "", fmt.Errorf("no matching case for {{ $c.SwitchFieldName }}=%q", {{ $c.SwitchFieldName }})
		{{- end }}
	}
	{{- else if $c.SwitchExprGo }}
	rawOutput, _ := state["{{ $c.From }}_output"].(string)
	_, _ = parsedOutput, rawOutput
	v := {{ $c.SwitchExprGo }}
	switch v {
	{{- range $c.Cases }}
	case {{ printf "%q" .MatchValue }}:
		return {{ printf "%q" .Target }}, nil
	{{- end }}
	default:
		{{- if $c.DefaultTarget }}
		return {{ printf "%q" $c.DefaultTarget }}, nil
		{{- else }}
		return "", fmt.Errorf("no matching case")
		{{- end }}
	}
	{{- else }}
	rawOutput, _ := state["{{ $c.From }}_output"].(string)
	_, _ = parsedOutput, rawOutput
	{{- range $c.Cases }}
	if {{ .PredicateGo }} {
		return {{ printf "%q" .Target }}, nil
	}
	{{- end }}
	{{- if $c.DefaultTarget }}
	return {{ printf "%q" $c.DefaultTarget }}, nil
	{{- else }}
	return "", fmt.Errorf("no matching case")
	{{- end }}
	{{- end }}
}
{{- end }}

// =============================================================================
// Node Functions
// =============================================================================
{{- range .EndNodes }}

func {{ .FuncName }}(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	{{- if eq .ExprKind "cel" }}
	var input any
	_ = input
	value := {{ .ExprGo }}
	stateDelta := graph.State{"end_structured_output": value}
	if b, err := json.Marshal(value); err == nil && strings.TrimSpace(string(b)) != "" {
		stateDelta[graph.StateKeyLastResponse] = string(b)
	}
	return stateDelta, nil
	{{- else if eq .ExprKind "json" }}
	value := mustParseJSONAny({{ goString .ExprJSON }})
	stateDelta := graph.State{"end_structured_output": value}
	if b, err := json.Marshal(value); err == nil && strings.TrimSpace(string(b)) != "" {
		stateDelta[graph.StateKeyLastResponse] = string(b)
	}
	return stateDelta, nil
	{{- else }}
	return graph.State{}, nil
	{{- end }}
}
{{- end }}
{{- range .TransformNodes }}

func {{ .FuncName }}(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	{{- if .ExprGo }}
	var input any
	_ = input
	value := {{ .ExprGo }}
	return graph.State{"{{ .ID }}_parsed": value}, nil
	{{- else }}
	return graph.State{}, nil
	{{- end }}
}
{{- end }}
{{- range .SetStateNodes }}

func {{ .FuncName }}(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	{{- if .Assignments }}
	var input any
	_ = input
	stateDelta := graph.State{}
	{{- range $i, $a := .Assignments }}
	stateDelta[{{ printf "%q" $a.Field }}] = {{ $a.ExprGo }}
	{{- end }}
	return stateDelta, nil
	{{- else }}
	return graph.State{}, nil
	{{- end }}
}
{{- end }}
{{- range .ApprovalNodes }}

func {{ .FuncName }}(ctx context.Context, state graph.State) (any, error) {
	{{- if .AutoApprove }}
	return graph.State{"approval_result": "approve"}, nil
	{{- else }}
	resumeValue, err := graph.Interrupt(ctx, state, {{ printf "%q" .ID }}, map[string]any{
		"message": {{ printf "%q" .Message }},
		"node_id": {{ printf "%q" .ID }},
	})
	if err != nil {
		return nil, err
	}
	raw, _ := resumeValue.(string)
	decision := strings.ToLower(strings.TrimSpace(raw))
	normalized := "reject"
	if decision == "approve" || decision == "yes" || decision == "y" {
		normalized = "approve"
	}
	return graph.State{"approval_result": normalized}, nil
	{{- end }}
}
{{- end }}
{{- range .MCPNodes }}

func {{ .FuncName }}(ctx context.Context, state graph.State) (any, error) {
	{{- if .Headers }}
	toolSet, err := newMCPToolSet({{ printf "%q" .Transport }}, {{ printf "%q" .ServerURL }}, map[string]string{
		{{- range .Headers }}
		{{ printf "%q" .Key }}: {{ printf "%q" .Value }},
		{{- end }}
	}, nil)
	{{- else }}
	toolSet, err := newMCPToolSet({{ printf "%q" .Transport }}, {{ printf "%q" .ServerURL }}, nil, nil)
	{{- end }}
	if err != nil {
		return nil, err
	}

	var selected tool.Tool
	for _, t := range toolSet.Tools(ctx) {
		if decl := t.Declaration(); decl != nil && decl.Name == {{ printf "%q" .ToolName }} {
			selected = t
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("MCP tool %q not found", {{ printf "%q" .ToolName }})
	}
	callable, ok := selected.(tool.CallableTool)
	if !ok {
		return nil, fmt.Errorf("MCP tool %q is not callable", {{ printf "%q" .ToolName }})
	}

	{{- if .FromNodeID }}
	parsedOutput, _ := state["{{ .FromNodeID }}_parsed"].(map[string]any)
	_ = parsedOutput
	{{- end }}
	args := make(map[string]any)
	{{- range $i, $p := .Params }}
	args[{{ printf "%q" $p.Name }}] = {{ $p.ExprGo }}
	{{- end }}

	payload, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal args: %w", err)
	}
	result, err := callable.Call(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("MCP call failed: %w", err)
	}
	if result == nil {
		return nil, nil
	}

	// Extract text from MCP response.
	var textBuf []string
	if b, err := json.Marshal(result); err == nil {
		var items []map[string]any
		if json.Unmarshal(b, &items) == nil {
			for _, item := range items {
				if t, _ := item["type"].(string); t == "text" {
					if txt, _ := item["text"].(string); strings.TrimSpace(txt) != "" {
						textBuf = append(textBuf, txt)
					}
				}
			}
		}
	}
	resultsText := strings.Join(textBuf, "\n")

	// Try to parse JSON from response.
	var parsed any
	if strings.TrimSpace(resultsText) != "" {
		if jsonText, ok := extractFirstJSONObjectFromText(resultsText); ok {
			json.Unmarshal([]byte(jsonText), &parsed)
		}
	}

	return graph.State{
		"{{ .ID }}_output": resultsText,
		"{{ .ID }}_parsed": parsed,
	}, nil
}
{{- end }}

// =============================================================================
// Agent Constructors
// =============================================================================
{{- if .HasAgentNodes }}

func createSubAgents() []agent.Agent {
	return []agent.Agent{
		{{- range .AgentNodes }}
		new{{ .FuncSuffix }}SubAgent(),
		{{- end }}
	}
}
{{- range .AgentNodes }}

func new{{ .FuncSuffix }}SubAgent() agent.Agent {
	apiKey := os.Getenv({{ printf "%q" .ModelSpec.APIKeyEnvVar }})
	if apiKey == "" {
		panic("environment variable {{ .ModelSpec.APIKeyEnvVar }} is not set")
	}

	modelOpts := []openai.Option{openai.WithAPIKey(apiKey)}
	{{- if .ModelSpec.BaseURL }}
	modelOpts = append(modelOpts, openai.WithBaseURL({{ printf "%q" .ModelSpec.BaseURL }}))
	{{- end }}
	{{- if .ModelSpec.Headers }}
	modelOpts = append(modelOpts, openai.WithHeaders(map[string]string{
		{{- range .ModelSpec.Headers }}
		{{ printf "%q" .Key }}: {{ printf "%q" .Value }},
		{{- end }}
	}))
	{{- end }}
	{{- if .ModelSpec.ExtraFieldsJSON }}
	modelOpts = append(modelOpts, openai.WithExtraFields(mustParseJSONMap({{ goString .ModelSpec.ExtraFieldsJSON }})))
	{{- end }}
	llmModel := openai.New({{ printf "%q" .ModelSpec.ModelName }}, modelOpts...)

	opts := []llmagent.Option{llmagent.WithModel(llmModel)}
	{{- if .Instruction }}
	opts = append(opts, llmagent.WithInstruction({{ goString .Instruction }}))
	{{- end }}
	{{- if .StructuredOutputSchemaJSON }}
	opts = append(opts, llmagent.WithStructuredOutputJSONSchema({{ printf "%q" .StructuredOutputSchemaName }}, mustParseJSONMap({{ goString .StructuredOutputSchemaJSON }}), true, ""))
	{{- end }}

	{{- if .MCPTools }}
	{{- $agentID := .ID }}
	var mcpToolSets []tool.ToolSet
	{{- range $i, $ts := .MCPTools }}
	{
		{{- if $ts.Headers }}
		ts, err := newMCPToolSet({{ printf "%q" $ts.Transport }}, {{ printf "%q" $ts.ServerURL }}, map[string]string{
			{{- range $ts.Headers }}
			{{ printf "%q" .Key }}: {{ printf "%q" .Value }},
			{{- end }}
		}, []string{
			{{- range $ts.AllowedTools }}
			{{ printf "%q" . }},
			{{- end }}
		})
		{{- else }}
		ts, err := newMCPToolSet({{ printf "%q" $ts.Transport }}, {{ printf "%q" $ts.ServerURL }}, nil, []string{
			{{- range $ts.AllowedTools }}
			{{ printf "%q" . }},
			{{- end }}
		})
		{{- end }}
		if err != nil {
			panic(err)
		}
		mcpToolSets = append(mcpToolSets, ts)
	}
	{{- end }}
	opts = append(opts, llmagent.WithToolSets(mcpToolSets))
	{{- end }}

	{{- if .GenConfig.HasAny }}
	var genConfig model.GenerationConfig
	{{- if .GenConfig.HasTemperature }}
	{
		t := {{ .GenConfig.Temperature }}
		genConfig.Temperature = &t
	}
	{{- end }}
	{{- if .GenConfig.HasMaxTokens }}
	{
		mt := {{ .GenConfig.MaxTokens }}
		genConfig.MaxTokens = &mt
	}
	{{- end }}
	{{- if .GenConfig.HasTopP }}
	{
		tp := {{ .GenConfig.TopP }}
		genConfig.TopP = &tp
	}
	{{- end }}
	{{- if .GenConfig.HasStop }}
	genConfig.Stop = []string{
		{{- range .GenConfig.Stop }}
		{{ printf "%q" . }},
		{{- end }}
	}
	{{- end }}
	{{- if .GenConfig.HasPresencePenalty }}
	{
		pp := {{ .GenConfig.PresencePenalty }}
		genConfig.PresencePenalty = &pp
	}
	{{- end }}
	{{- if .GenConfig.HasFrequencyPenalty }}
	{
		fp := {{ .GenConfig.FrequencyPenalty }}
		genConfig.FrequencyPenalty = &fp
	}
	{{- end }}
	{{- if .GenConfig.HasReasoningEffort }}
	{
		re := {{ printf "%q" .GenConfig.ReasoningEffort }}
		genConfig.ReasoningEffort = &re
	}
	{{- end }}
	{{- if .GenConfig.HasThinkingEnabled }}
	{
		te := {{ if .GenConfig.ThinkingEnabled }}true{{ else }}false{{ end }}
		genConfig.ThinkingEnabled = &te
	}
	{{- end }}
	{{- if .GenConfig.HasThinkingTokens }}
	{
		tt := {{ .GenConfig.ThinkingTokens }}
		genConfig.ThinkingTokens = &tt
	}
	{{- end }}
	{{- if .GenConfig.HasStream }}
	genConfig.Stream = {{ if .GenConfig.Stream }}true{{ else }}false{{ end }}
	{{- end }}
	opts = append(opts, llmagent.WithGenerationConfig(genConfig))
	{{- end }}

	return llmagent.New({{ printf "%q" .ID }}, opts...)
}
{{- end }}
{{- end }}

// =============================================================================
// Infrastructure (do not edit below this line)
// =============================================================================

{{- if .NeedsStructuredOutputMapper }}
func agentStructuredOutputMapper(nodeID string) graph.SubgraphOutputMapper {
	return func(parent graph.State, result graph.SubgraphResult) graph.State {
		last := result.LastResponse
		upd := graph.State{
			graph.StateKeyLastResponse:  last,
			graph.StateKeyNodeResponses: map[string]any{nodeID: last},
			graph.StateKeyUserInput:     "",
			nodeID + "_output":          last,
		}
		if result.StructuredOutput != nil {
			upd[nodeID+"_parsed"] = result.StructuredOutput
		}
		return upd
	}
}
{{- end }}

{{- if .NeedsMCP }}
func newMCPToolSet(transport, serverURL string, headers map[string]string, allowedTools []string) (tool.ToolSet, error) {
	if transport == "" {
		return nil, fmt.Errorf("transport is required")
	}
	connConfig := mcp.ConnectionConfig{Transport: transport, Timeout: 10 * time.Second}
	switch transport {
	case "streamable_http", "sse":
		if serverURL == "" {
			return nil, fmt.Errorf("server_url is required for %s", transport)
		}
		connConfig.ServerURL = serverURL
		if len(headers) > 0 {
			connConfig.Headers = headers
		}
	default:
		return nil, fmt.Errorf("unsupported transport: %s", transport)
	}
	var opts []mcp.ToolSetOption
	if len(allowedTools) > 0 {
		opts = append(opts, mcp.WithToolFilterFunc(tool.NewIncludeToolNamesFilter(allowedTools...)))
	}
	return mcp.NewMCPToolSet(connConfig, opts...), nil
}
{{- end }}

{{- if .NeedsMustParseJSONAny }}
func mustParseJSONAny(raw string) any {
	if raw = strings.TrimSpace(raw); raw == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		panic(err)
	}
	return v
}
{{- end }}

{{- if .HasAgentNodes }}
func mustParseJSONMap(raw string) map[string]any {
	if raw = strings.TrimSpace(raw); raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}
{{- end }}

{{- if .NeedsExtractFirstJSONObjectFromText }}
func extractFirstJSONObjectFromText(s string) (string, bool) {
	start := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			start = i
			break
		}
	}
	if start == -1 {
		return "", false
	}
	stack := make([]byte, 0, 8)
	inString, escaped := false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if inString {
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			stack = append(stack, c)
		case '}', ']':
			if len(stack) == 0 {
				return "", false
			}
			top := stack[len(stack)-1]
			if (top == '{' && c == '}') || (top == '[' && c == ']') {
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return s[start : i+1], true
				}
			} else {
				return "", false
			}
		}
	}
	return "", false
}
{{- end }}
`
