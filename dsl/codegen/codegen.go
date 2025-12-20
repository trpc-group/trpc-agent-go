package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/mcpconfig"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/modelspec"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/numconv"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/outputformat"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/toolspec"
)

// Known limitations of the code generator:
//
// 1. Runtime type assumptions: Generated code uses type assertions like
//    .(map[string]any) and .([]any) for nested data access. If the actual
//    runtime data doesn't match this structure (e.g., []string instead of
//    []any), the generated code will panic. This assumes data comes from
//    JSON unmarshal which produces map[string]any and []any types.
//
// 2. Heuristic variable detection: The check for input.output_parsed/raw
//    usage relies on strings.Contains(goExpr, "parsedOutput"/"rawOutput").
//    This may produce false positives if user field names contain these
//    substrings (e.g., state.parsedOutput). This is a known trade-off for
//    simplicity over full AST-based variable tracking.
//
// 3. MCP single-edge constraint: MCP nodes are limited to at most one
//    incoming edge to simplify input.* semantics. This is a design choice.

// RunMode specifies how the generated code should be executed.
type RunMode string

const (
	// RunModeInteractive generates a terminal-interactive CLI application (default).
	RunModeInteractive RunMode = "interactive"
	// RunModeAGUI generates an AG-UI HTTP server.
	RunModeAGUI RunMode = "agui"
	// RunModeA2A generates an A2A (Agent-to-Agent) protocol server.
	RunModeA2A RunMode = "a2a"
	// RunModeOpenAI generates an OpenAI-compatible API server.
	RunModeOpenAI RunMode = "openai"
)

// Options controls how Go code is generated from a DSL graph.
type Options struct {
	// PackageName is the package name for generated files (defaults to "main").
	PackageName string
	// AppName is a human‑readable application name used in main.go logging.
	// When empty, graph.Name is used.
	AppName string
	// RunMode specifies the execution mode (defaults to "interactive").
	RunMode RunMode
}

// Option is a functional option for GenerateNativeGo.
type Option func(*Options)

// WithPackageName sets the package name for generated files.
func WithPackageName(name string) Option {
	return func(o *Options) {
		o.PackageName = name
	}
}

// WithAppName sets the application name used in logging.
func WithAppName(name string) Option {
	return func(o *Options) {
		o.AppName = name
	}
}

// WithRunMode sets the execution mode for generated code.
func WithRunMode(mode RunMode) Option {
	return func(o *Options) {
		o.RunMode = mode
	}
}

// Output contains the generated Go source files keyed by filename.
type Output struct {
	Files map[string][]byte
}

// GenerateNativeGo generates Go source code (main.go) from a DSL Graph.
//
// Supported nodes are generated as graph NodeFuncs:
//   - builtin.start         -> no-op
//   - builtin.llmagent      -> runs llmagent with model_spec, mcp_tools,
//     output_format (native structured output) and
//     generation config
//   - builtin.transform     -> evaluates CEL-lite and writes state[<id>_parsed]
//   - builtin.set_state     -> evaluates CEL-lite assignments and updates graph state
//   - builtin.mcp           -> calls an MCP server tool and writes state[<id>_output/<id>_parsed]
//   - builtin.user_approval -> graph.Interrupt
//   - builtin.end           -> optional CEL-lite/json expr into end_structured_output
//   - conditional_edges     -> routing functions compiled from CEL-lite
//
// The generated code does not import the dsl package and does not depend on
// cel-go; CEL-lite expressions are compiled into plain Go code plus a few small
// helper functions.
func GenerateNativeGo(g *dsl.Graph, opts ...Option) (*Output, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}

	// Apply options with defaults.
	o := &Options{
		PackageName: "main",
		RunMode:     RunModeInteractive,
	}
	for _, opt := range opts {
		opt(o)
	}

	appName := o.AppName
	if appName == "" {
		appName = g.Name
		if appName == "" {
			appName = "dsl_app"
		}
	}

	ir, err := buildIR(g)
	if err != nil {
		return nil, err
	}

	src, err := renderTemplate(singleFileTemplate, singleFileTemplateData{
		PackageName:    o.PackageName,
		AppName:        appName,
		RunMode:        string(o.RunMode),
		HasAgentNodes:  len(ir.AgentNodes) > 0,
		EnvVarInfos:    ir.EnvVarInfos,
		NeedsApproval:  ir.NeedsApproval,
		NeedsEnd:       ir.NeedsEnd,
		NeedsMCP:       ir.NeedsMCP,
		NeedsReflect:   ir.NeedsApproval || ir.NeedsEnd || len(ir.StateVars) > 0,
		NeedsExtractFirstJSONObjectFromText: len(ir.MCPNodes) > 0,
		StateVars:      ir.StateVars,
		StartNodes:     ir.StartNodes,
		AgentNodes:     ir.AgentNodes,
		TransformNodes: ir.TransformNodes,
		SetStateNodes:  ir.SetStateNodes,
		MCPNodes:       ir.MCPNodes,
		ApprovalNodes:  ir.ApprovalNodes,
		EndNodes:       ir.EndNodes,
		Edges:          ir.Edges,
		Conditions:     ir.Conditions,
		EntryPoint:     ir.EntryPoint,
		// Helper function usage flags.
		NeedsMustParseJSONAny:       ir.NeedsMustParseJSONAny,
		NeedsStructuredOutputMapper: ir.NeedsStructuredOutputMapper,
	})
	if err != nil {
		return nil, fmt.Errorf("render main.go: %w", err)
	}

	out := &Output{Files: map[string][]byte{
		"main.go": src,
	}}
	return out, nil
}

// ---- IR build ----

type agentNode struct {
	ID          string
	FuncSuffix  string // CamelCase identifier based on ID, e.g. "Classifier"
	Instruction string
	ModelSpec   agentModelSpec
	GenConfig   agentGenerationConfig
	MCPTools    []agentMCPToolSet
	// StructuredOutputSchemaJSON is the output_format.schema serialized as JSON
	// when output_format.type == "json".
	StructuredOutputSchemaJSON string
	StructuredOutputSchemaName string
}

type agentGenerationConfig struct {
	HasTemperature bool
	Temperature    string // float literal

	HasMaxTokens bool
	MaxTokens    string // int literal

	HasTopP bool
	TopP    string // float literal

	HasStop bool
	Stop    []string

	HasPresencePenalty bool
	PresencePenalty    string // float literal

	HasFrequencyPenalty bool
	FrequencyPenalty    string // float literal

	HasReasoningEffort bool
	ReasoningEffort    string // string literal (already trimmed, not quoted)

	HasThinkingEnabled bool
	ThinkingEnabled    bool

	HasThinkingTokens bool
	ThinkingTokens    string // int literal

	HasStream bool
	Stream    bool

	HasAny bool
}

type agentMCPToolSet struct {
	Transport    string
	ServerURL    string
	AllowedTools []string
	Headers      []kvPair
}

type kvPair struct {
	Key   string
	Value string
}

type agentModelSpec struct {
	ModelName string
	// APIKeyEnvVar is the environment variable name for the API key (e.g., "DEEPSEEK_API_KEY").
	APIKeyEnvVar string
	BaseURL      string
	Headers      []kvPair
	// ExtraFieldsJSON is an optional JSON object (serialized) passed to the model constructor.
	ExtraFieldsJSON string
}

// envVarInfo holds metadata about an environment variable for documentation.
type envVarInfo struct {
	Name    string   // e.g., "OPENAI_API_KEY"
	BaseURL string   // e.g., "https://api.deepseek.com/v1"
	Agents  []string // e.g., ["classifier", "flight_agent"]
}

// apiKeyAllocator assigns environment variable names based on provider and api_key.
// Environment variable names are derived from provider (e.g., OPENAI_API_KEY, OPENAI_API_KEY_2).
type apiKeyAllocator struct {
	// providerCount tracks how many unique API keys we've seen per provider.
	providerCount map[string]int
	// mapping maps (provider, api_key) -> env var name.
	mapping map[string]string
	// envVarInfos collects info for documentation.
	envVarInfos map[string]*envVarInfo
}

func newAPIKeyAllocator() *apiKeyAllocator {
	return &apiKeyAllocator{
		providerCount: make(map[string]int),
		mapping:       make(map[string]string),
		envVarInfos:   make(map[string]*envVarInfo),
	}
}

// allocate returns the environment variable name for a given (provider, api_key, base_url, agent_id) tuple.
// If the api_key already starts with "env:", it uses that directly.
// Environment variable names are based on provider (e.g., OPENAI_API_KEY, OPENAI_API_KEY_2).
func (a *apiKeyAllocator) allocate(provider, apiKey, baseURL, agentID string) string {
	apiKey = strings.TrimSpace(apiKey)
	baseURL = strings.TrimSpace(baseURL)
	provider = strings.TrimSpace(provider)

	// If user explicitly specified env:VAR, use it directly.
	if envVar, found := strings.CutPrefix(apiKey, "env:"); found {
		envVar = strings.TrimSpace(envVar)
		if envVar != "" {
			// Track for documentation.
			if info, ok := a.envVarInfos[envVar]; ok {
				info.Agents = append(info.Agents, agentID)
			} else {
				a.envVarInfos[envVar] = &envVarInfo{
					Name:    envVar,
					BaseURL: baseURL,
					Agents:  []string{agentID},
				}
			}
			return envVar
		}
	}

	// Normalize provider to uppercase for env var naming.
	providerUpper := strings.ToUpper(provider)
	if providerUpper == "" {
		providerUpper = "MODEL"
	}

	// Create a key for deduplication: (provider, api_key).
	// We use the actual api_key value to detect same vs different keys.
	dedupeKey := providerUpper + "\x00" + apiKey

	// Check if we've already assigned an env var for this combination.
	if envVar, ok := a.mapping[dedupeKey]; ok {
		// Same (provider, api_key) pair, reuse the env var.
		if info := a.envVarInfos[envVar]; info != nil {
			info.Agents = append(info.Agents, agentID)
		}
		return envVar
	}

	// Increment count for this provider.
	a.providerCount[providerUpper]++
	count := a.providerCount[providerUpper]

	// Generate env var name based on provider.
	var envVar string
	if count == 1 {
		envVar = providerUpper + "_API_KEY"
	} else {
		envVar = fmt.Sprintf("%s_API_KEY_%d", providerUpper, count)
	}

	// Store mapping.
	a.mapping[dedupeKey] = envVar
	a.envVarInfos[envVar] = &envVarInfo{
		Name:    envVar,
		BaseURL: baseURL,
		Agents:  []string{agentID},
	}

	return envVar
}

// getEnvVarInfos returns sorted environment variable info for documentation.
func (a *apiKeyAllocator) getEnvVarInfos() []envVarInfo {
	result := make([]envVarInfo, 0, len(a.envVarInfos))
	for _, info := range a.envVarInfos {
		result = append(result, *info)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

type startNode struct {
	ID string
}

type stateVar struct {
	Name      string
	TypeGo    string
	ReducerGo string
	// HasDefault indicates whether Default* fields are populated.
	HasDefault bool
	// DefaultLiteral is a Go literal expression (e.g. `"x"`, `true`, `123`).
	DefaultLiteral string
	// DefaultJSON is a JSON literal (serialized) parsed via mustParseJSONAny in generated code.
	DefaultJSON string
}

type approvalNode struct {
	ID          string
	FuncName    string
	Message     string
	AutoApprove bool
}

type endNode struct {
	ID       string
	FuncName string
	ExprKind string // "", "cel", "json"
	ExprGo   string // CEL-lite compiled Go expression returning a native Go value
	ExprJSON string // raw JSON string (when ExprKind=="json")
}

type transformNode struct {
	ID       string
	FuncName string
	ExprGo   string // CEL-lite compiled Go expression returning a native Go value; empty means no-op
}

type setStateAssignment struct {
	Field  string
	ExprGo string // CEL-lite compiled Go expression returning a native Go value
}

type setStateNode struct {
	ID          string
	FuncName    string
	Assignments []setStateAssignment
}

type mcpParam struct {
	Name   string
	ExprGo string // CEL-lite compiled Go expression returning a native Go value
}

type mcpNode struct {
	ID         string
	FuncName   string
	FromNodeID string

	Transport string
	ServerURL string
	Headers   []kvPair
	ToolName  string
	Params    []mcpParam
}

type edge struct {
	From string
	To   string
}

type condCase struct {
	Name string
	// PredicateGo is a Go expression that evaluates to a bool (when SwitchExprGo is empty).
	PredicateGo string
	// MatchValue is used when the condition can be compiled into a switch:
	// it is the string literal for the case match.
	MatchValue string
	Target     string
}

type condition struct {
	ID       string
	From     string
	FuncName string
	Cases    []condCase
	// SwitchExprGo is a Go string expression used to generate a switch statement.
	// When empty, the condition falls back to sequential if/else predicates.
	SwitchExprGo string
	// SwitchFieldName is the field name to switch on (e.g., "classification").
	// Used to generate cleaner code like: classification, _ := parsedOutput["classification"].(string)
	SwitchFieldName string
	// DefaultTarget corresponds to Condition.Default in DSL.
	DefaultTarget string
}

type irGraph struct {
	StateVars      []stateVar
	StartNodes     []startNode
	AgentNodes     []agentNode
	TransformNodes []transformNode
	SetStateNodes  []setStateNode
	MCPNodes       []mcpNode
	ApprovalNodes  []approvalNode
	EndNodes       []endNode
	Edges          []edge
	Conditions     []condition
	EntryPoint     string
	EnvVarInfos    []envVarInfo // Detailed env var info for documentation
	NeedsApproval  bool
	NeedsEnd       bool
	NeedsMCP       bool

	NeedsMustParseJSONAny bool
	// NeedsStructuredOutputMapper is true when any agent node has structured output.
	NeedsStructuredOutputMapper bool
}

func buildIR(g *dsl.Graph) (*irGraph, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}

	expanded, _, err := expandWhileNodes(g)
	if err != nil {
		return nil, fmt.Errorf("while expansion failed: %w", err)
	}
	g = expanded

	ir := &irGraph{EntryPoint: g.StartNodeID}

	// API key allocator for smart environment variable naming.
	apiKeyAlloc := newAPIKeyAllocator()

	// Graph-level state variables.
	reservedStateKeys := map[string]struct{}{
		// MessagesStateSchema built-ins.
		"messages":          {},
		"user_input":        {},
		"one_shot_messages": {},
		"last_response":     {},
		"last_response_id":  {},
		"node_responses":    {},
		"metadata":          {},
		// Framework/runtime keys (should not be authored as state variables).
		"session":         {},
		"exec_context":    {},
		"current_node_id": {},
		"parent_agent":    {},
		"tool_callbacks":  {},
		"model_callbacks": {},
		"agent_callbacks": {},
		// Codegen/system keys.
		"node_structured":       {},
		"approval_result":       {},
		"end_structured_output": {},
	}
	for _, sv := range g.StateVariables {
		name := strings.TrimSpace(sv.Name)
		if name == "" {
			continue
		}
		if _, isReserved := reservedStateKeys[name]; isReserved {
			// Skip keys owned by the framework/runtime.
			continue
		}

		reducerName := strings.TrimSpace(sv.Reducer)
		if reducerName == "" {
			reducerName = "default"
		}

		var reducerGo string
		switch reducerName {
		case "default":
			reducerGo = "graph.DefaultReducer"
		case "append":
			reducerGo = "graph.AppendReducer"
		case "merge":
			reducerGo = "graph.MergeReducer"
		case "message":
			reducerGo = "graph.MessageReducer"
		case "string_slice":
			reducerGo = "graph.StringSliceReducer"
		default:
			return nil, fmt.Errorf("state_variables[%s]: unknown reducer %q", name, reducerName)
		}

		kind := strings.TrimSpace(sv.Kind)
		if kind == "" {
			kind = "opaque"
		}

		// Choose a Go reflect.Type expression. Prefer reducer-specific types.
		typeGo := "reflect.TypeOf((*any)(nil)).Elem()"
		switch reducerName {
		case "message":
			typeGo = "reflect.TypeOf([]model.Message{})"
		case "string_slice":
			typeGo = "reflect.TypeOf([]string{})"
		case "merge":
			typeGo = "reflect.TypeOf(map[string]any{})"
		case "append":
			typeGo = "reflect.TypeOf([]any{})"
		default:
			switch kind {
			case "string":
				typeGo = "reflect.TypeOf(\"\")"
			case "number":
				typeGo = "reflect.TypeOf(float64(0))"
			case "boolean":
				typeGo = "reflect.TypeOf(false)"
			case "object":
				typeGo = "reflect.TypeOf(map[string]any{})"
			case "array":
				typeGo = "reflect.TypeOf([]any{})"
			case "opaque":
				typeGo = "reflect.TypeOf((*any)(nil)).Elem()"
			default:
				return nil, fmt.Errorf("state_variables[%s]: unknown kind %q", name, kind)
			}
		}

		outVar := stateVar{
			Name:      name,
			TypeGo:    typeGo,
			ReducerGo: reducerGo,
		}
		if sv.Default != nil {
			outVar.HasDefault = true
			switch v := sv.Default.(type) {
			case string:
				outVar.DefaultLiteral = fmt.Sprintf("%q", v)
			case bool:
				if v {
					outVar.DefaultLiteral = "true"
				} else {
					outVar.DefaultLiteral = "false"
				}
			case float64:
				outVar.DefaultLiteral = strconv.FormatFloat(v, 'f', -1, 64)
			default:
				b, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("state_variables[%s]: failed to marshal default: %w", name, err)
				}
				outVar.DefaultJSON = string(b)
			}
		}

		ir.StateVars = append(ir.StateVars, outVar)
	}

	// Build node index for lookups and compute MCP input sources.
	nodeByID := make(map[string]dsl.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeByID[n.ID] = n
	}

	mcpInputSource := make(map[string]string)
	for _, e := range g.Edges {
		targetNode, ok := nodeByID[e.Target]
		if !ok {
			continue
		}
		if targetNode.EngineNode.NodeType != "builtin.mcp" {
			continue
		}
		if existing, exists := mcpInputSource[e.Target]; exists && existing != e.Source {
			return nil, fmt.Errorf("builtin.mcp node %s has multiple incoming edges (%s, %s); it must have a single upstream node for input.* semantics", e.Target, existing, e.Source)
		}
		mcpInputSource[e.Target] = e.Source
	}

	for _, n := range g.Nodes {
		switch n.EngineNode.NodeType {
		case "builtin.llmagent":
			inst, _ := stringField(n.EngineNode.Config, "instruction")

			specRaw, ok := n.EngineNode.Config["model_spec"]
			if !ok || specRaw == nil {
				return nil, fmt.Errorf("builtin.llmagent[%s]: model_spec is required", n.ID)
			}
			spec, err := modelspec.Parse(specRaw)
			if err != nil {
				return nil, fmt.Errorf("builtin.llmagent[%s]: %w", n.ID, err)
			}
			if strings.TrimSpace(spec.Provider) != "openai" {
				return nil, fmt.Errorf("builtin.llmagent[%s]: unsupported model_spec.provider %q for codegen (only \"openai\" is supported)", n.ID, spec.Provider)
			}

			// Allocate environment variable name for API key.
			// This handles deduplication by (provider, api_key) and generates friendly names.
			apiKeyEnvVar := apiKeyAlloc.allocate(spec.Provider, spec.APIKey, spec.BaseURL, n.ID)

			headers := sortedKVPairs(spec.Headers)

			var extraFieldsJSON string
			if len(spec.ExtraFields) > 0 {
				b, err := json.Marshal(spec.ExtraFields)
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: failed to marshal model_spec.extra_fields: %w", n.ID, err)
				}
				extraFieldsJSON = string(b)
			}

			gen := agentGenerationConfig{}
			if temperatureRaw, ok := n.EngineNode.Config["temperature"]; ok {
				temperature, err := numconv.Float64(temperatureRaw, "temperature")
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: %w", n.ID, err)
				}
				gen.HasTemperature = true
				gen.Temperature = strconv.FormatFloat(temperature, 'f', -1, 64)
				gen.HasAny = true
			}
			if maxTokensRaw, ok := n.EngineNode.Config["max_tokens"]; ok {
				tokens, err := numconv.Int(maxTokensRaw, "max_tokens")
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: %w", n.ID, err)
				}
				if tokens <= 0 {
					return nil, fmt.Errorf("builtin.llmagent[%s]: max_tokens must be positive", n.ID)
				}
				gen.HasMaxTokens = true
				gen.MaxTokens = fmt.Sprintf("%d", tokens)
				gen.HasAny = true
			}
			if topPRaw, ok := n.EngineNode.Config["top_p"]; ok {
				topP, err := numconv.Float64(topPRaw, "top_p")
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: %w", n.ID, err)
				}
				gen.HasTopP = true
				gen.TopP = strconv.FormatFloat(topP, 'f', -1, 64)
				gen.HasAny = true
			}

			if stopRaw, ok := n.EngineNode.Config["stop"]; ok && stopRaw != nil {
				var stops []string
				switch v := stopRaw.(type) {
				case []any:
					for _, item := range v {
						s, ok := item.(string)
						if !ok {
							continue
						}
						s = strings.TrimSpace(s)
						if s != "" {
							stops = append(stops, s)
						}
					}
				case []string:
					for _, s := range v {
						s = strings.TrimSpace(s)
						if s != "" {
							stops = append(stops, s)
						}
					}
				}
				if len(stops) > 0 {
					gen.HasStop = true
					gen.Stop = stops
					gen.HasAny = true
				}
			}

			if presenceRaw, ok := n.EngineNode.Config["presence_penalty"]; ok {
				presence, err := numconv.Float64(presenceRaw, "presence_penalty")
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: %w", n.ID, err)
				}
				gen.HasPresencePenalty = true
				gen.PresencePenalty = strconv.FormatFloat(presence, 'f', -1, 64)
				gen.HasAny = true
			}

			if freqRaw, ok := n.EngineNode.Config["frequency_penalty"]; ok {
				freq, err := numconv.Float64(freqRaw, "frequency_penalty")
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: %w", n.ID, err)
				}
				gen.HasFrequencyPenalty = true
				gen.FrequencyPenalty = strconv.FormatFloat(freq, 'f', -1, 64)
				gen.HasAny = true
			}

			if re, ok := n.EngineNode.Config["reasoning_effort"].(string); ok && strings.TrimSpace(re) != "" {
				gen.HasReasoningEffort = true
				gen.ReasoningEffort = strings.TrimSpace(re)
				gen.HasAny = true
			}

			if thinkingEnabled, ok := n.EngineNode.Config["thinking_enabled"].(bool); ok {
				gen.HasThinkingEnabled = true
				gen.ThinkingEnabled = thinkingEnabled
				gen.HasAny = true
			}

			if thinkingTokensRaw, ok := n.EngineNode.Config["thinking_tokens"]; ok {
				tokens, err := numconv.Int(thinkingTokensRaw, "thinking_tokens")
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: %w", n.ID, err)
				}
				if tokens <= 0 {
					return nil, fmt.Errorf("builtin.llmagent[%s]: thinking_tokens must be positive", n.ID)
				}
				gen.HasThinkingTokens = true
				gen.ThinkingTokens = fmt.Sprintf("%d", tokens)
				gen.HasAny = true
			}

			if stream, ok := n.EngineNode.Config["stream"].(bool); ok {
				gen.HasStream = true
				gen.Stream = stream
				gen.HasAny = true
			}

			// Parse unified tools config for MCP tools
			var mcpToolSets []agentMCPToolSet
			if raw, ok := n.EngineNode.Config["tools"]; ok && raw != nil {
				parsed, err := toolspec.ParseTools(raw)
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: %w", n.ID, err)
				}
				for _, spec := range parsed.MCPTools {
					headers := sortedKVPairs(spec.Headers)

					allowed := append([]string(nil), spec.AllowedTools...)
					sort.Strings(allowed)

					mcpToolSets = append(mcpToolSets, agentMCPToolSet{
						Transport:    spec.Transport,
						ServerURL:    spec.ServerURL,
						AllowedTools: allowed,
						Headers:      headers,
					})
				}
				if len(mcpToolSets) > 0 {
					ir.NeedsMCP = true
				}
			}

			structuredSchema := outputformat.StructuredSchema(n.EngineNode.Config["output_format"])
			var structuredOutputSchemaJSON string
			var structuredOutputSchemaName string
			if len(structuredSchema) > 0 {
				b, err := json.Marshal(structuredSchema)
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: failed to marshal output_format.schema: %w", n.ID, err)
				}
				structuredOutputSchemaJSON = string(b)
				structuredOutputSchemaName = structuredSchemaName(n.ID)
				ir.NeedsStructuredOutputMapper = true
			}

			ir.AgentNodes = append(ir.AgentNodes, agentNode{
				ID:          n.ID,
				FuncSuffix:  toCamel(n.ID),
				Instruction: inst,
				ModelSpec: agentModelSpec{
					ModelName:       spec.ModelName,
					APIKeyEnvVar:    apiKeyEnvVar,
					BaseURL:         spec.BaseURL,
					Headers:         headers,
					ExtraFieldsJSON: extraFieldsJSON,
				},
				GenConfig:                  gen,
				MCPTools:                   mcpToolSets,
				StructuredOutputSchemaJSON: structuredOutputSchemaJSON,
				StructuredOutputSchemaName: structuredOutputSchemaName,
			})
		case "builtin.start":
			ir.StartNodes = append(ir.StartNodes, startNode{ID: n.ID})
		case "builtin.transform":
			exprGo, err := parseAndCompileCELExpr(n.EngineNode.Config, "expr", fmt.Sprintf("builtin.transform[%s].expr", n.ID))
			if err != nil {
				return nil, err
			}
			ir.TransformNodes = append(ir.TransformNodes, transformNode{
				ID:       n.ID,
				FuncName: "node" + toCamel(n.ID),
				ExprGo:   exprGo,
			})
		case "builtin.set_state":
			assignments, err := parseSetStateAssignments(n.ID, n.EngineNode.Config)
			if err != nil {
				return nil, err
			}
			ir.SetStateNodes = append(ir.SetStateNodes, setStateNode{
				ID:          n.ID,
				FuncName:    "node" + toCamel(n.ID),
				Assignments: assignments,
			})
		case "builtin.mcp":
			mcpIR, err := buildMCPNodeIR(n, mcpInputSource[n.ID])
			if err != nil {
				return nil, err
			}
			ir.MCPNodes = append(ir.MCPNodes, mcpIR)
			ir.NeedsMCP = true
		case "builtin.user_approval":
			msg, _ := stringField(n.EngineNode.Config, "message")
			if msg == "" {
				msg = "Please approve this action (yes/no):"
			}
			autoApprove := false
			if v, ok := n.EngineNode.Config["auto_approve"].(bool); ok {
				autoApprove = v
			}
			ir.ApprovalNodes = append(ir.ApprovalNodes, approvalNode{
				ID:          n.ID,
				FuncName:    "node" + toCamel(n.ID),
				Message:     msg,
				AutoApprove: autoApprove,
			})
			ir.NeedsApproval = true
		case "builtin.end":
			e := endNode{
				ID:       n.ID,
				FuncName: "node" + toCamel(n.ID),
			}
			if rawExpr, ok := n.EngineNode.Config["expr"]; ok && rawExpr != nil {
				exprMap, ok := rawExpr.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("builtin.end[%s]: expr must be an object when present", n.ID)
				}
				exprStr, _ := stringField(exprMap, "expression")
				exprStr = strings.TrimSpace(exprStr)
				if exprStr != "" {
					format, _ := stringField(exprMap, "format")
					format = strings.TrimSpace(format)
					if format == "" {
						format = "cel"
					}
					switch format {
					case "cel":
						goExpr, err := compileCELLiteToGoValue(exprStr)
						if err != nil {
							return nil, fmt.Errorf("builtin.end[%s]: invalid CEL expression: %w", n.ID, err)
						}
						// Reject input.output_parsed/input.output_raw in end nodes.
						// Note: uses heuristic substring matching; may false-positive on field names containing these substrings.
						if strings.Contains(goExpr, "parsedOutput") || strings.Contains(goExpr, "rawOutput") {
							return nil, fmt.Errorf("builtin.end[%s]: input.output_parsed and input.output_raw are only supported in conditional_edges routing functions (if your field name contains 'parsedOutput'/'rawOutput', this is a known limitation)", n.ID)
						}
						e.ExprKind = "cel"
						e.ExprGo = goExpr
					case "json":
				e.ExprKind = "json"
					e.ExprJSON = exprStr
					default:
						return nil, fmt.Errorf("builtin.end[%s]: unsupported expr.format %q (expected \"cel\" or \"json\")", n.ID, format)
					}
				}
			}
			ir.EndNodes = append(ir.EndNodes, e)
			ir.NeedsEnd = true
		default:
			return nil, fmt.Errorf("unsupported node_type %q for codegen", n.EngineNode.NodeType)
		}
	}

	for _, e := range g.Edges {
		ir.Edges = append(ir.Edges, edge{From: e.Source, To: e.Target})
	}

	for _, ce := range g.ConditionalEdges {
		c, err := buildCondition(ce)
		if err != nil {
			return nil, fmt.Errorf("conditional_edge %q from %q: %w", ce.ID, ce.From, err)
		}
		if c != nil {
			ir.Conditions = append(ir.Conditions, *c)
		}
	}

	// Stabilize output ordering for deterministic output and readability.
	sort.Slice(ir.StateVars, func(i, j int) bool { return ir.StateVars[i].Name < ir.StateVars[j].Name })
	sort.Slice(ir.StartNodes, func(i, j int) bool { return ir.StartNodes[i].ID < ir.StartNodes[j].ID })
	sort.Slice(ir.AgentNodes, func(i, j int) bool { return ir.AgentNodes[i].ID < ir.AgentNodes[j].ID })
	sort.Slice(ir.TransformNodes, func(i, j int) bool { return ir.TransformNodes[i].ID < ir.TransformNodes[j].ID })
	sort.Slice(ir.SetStateNodes, func(i, j int) bool { return ir.SetStateNodes[i].ID < ir.SetStateNodes[j].ID })
	sort.Slice(ir.MCPNodes, func(i, j int) bool { return ir.MCPNodes[i].ID < ir.MCPNodes[j].ID })
	sort.Slice(ir.ApprovalNodes, func(i, j int) bool { return ir.ApprovalNodes[i].ID < ir.ApprovalNodes[j].ID })
	sort.Slice(ir.EndNodes, func(i, j int) bool { return ir.EndNodes[i].ID < ir.EndNodes[j].ID })
	sort.Slice(ir.Edges, func(i, j int) bool {
		if ir.Edges[i].From == ir.Edges[j].From {
			return ir.Edges[i].To < ir.Edges[j].To
		}
		return ir.Edges[i].From < ir.Edges[j].From
	})

	ir.EnvVarInfos = apiKeyAlloc.getEnvVarInfos()

	// Detect which helper functions are needed by scanning generated Go expressions.
	detectHelperUsage(ir)

	return ir, nil
}

func buildCondition(ce dsl.ConditionalEdge) (*condition, error) {
	if len(ce.Condition.Cases) == 0 {
		return nil, nil
	}

	funcName := ""
	if strings.TrimSpace(ce.ID) != "" {
		funcName = "route" + toCamel("edge_"+ce.ID)
	} else if strings.TrimSpace(ce.From) != "" {
		funcName = "route" + toCamel("from_"+ce.From)
	} else {
		funcName = "routeNode"
	}

	c := condition{
		ID:            ce.ID,
		From:          ce.From,
		FuncName:      funcName,
		DefaultTarget: ce.Condition.Default,
	}

	// First pass: validate and try to recognize a switchable pattern:
	//   input.output_parsed.<field> == "literal"
	var (
		canSwitch   = true
		switchRoot  string
		switchSteps []celPathStep
		switchVals  []string
	)

	for idx, kase := range ce.Condition.Cases {
		expr := strings.TrimSpace(kase.Predicate.Expression)
		if expr == "" {
			return nil, fmt.Errorf("builtin case %d predicate.expression is required", idx)
		}
		format := strings.TrimSpace(kase.Predicate.Format)
		if format == "" {
			format = "cel"
		}
		if format != "cel" {
			return nil, fmt.Errorf("unsupported predicate.format %q (expected \"cel\")", format)
		}
		if strings.TrimSpace(kase.Target) == "" {
			return nil, fmt.Errorf("builtin case %d has empty target", idx)
		}

		root, steps, lit, ok, err := extractStringEqualityPredicate(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid predicate %q: %w", expr, err)
		}
		if !ok {
			canSwitch = false
			continue
		}
		if idx == 0 {
			switchRoot = root
			switchSteps = steps
		} else if switchRoot != root || !equalCelPathSteps(switchSteps, steps) {
			canSwitch = false
		}
		switchVals = append(switchVals, lit)
	}

	if canSwitch && switchRoot != "" && len(switchVals) == len(ce.Condition.Cases) {
		// Extract the field name for cleaner code generation.
		// Only use SwitchFieldName optimization for single-level field access:
		// input.output_parsed.field (exactly 2 steps: output_parsed + field)
		// For nested fields like input.output_parsed.a.b, fall back to SwitchExprGo.
		var fieldName string
		if switchRoot == "input" && len(switchSteps) == 2 && switchSteps[0].key == "output_parsed" {
			fieldName = switchSteps[1].key
		}

		if fieldName != "" {
			// Generate clean code: classification, _ := parsedOutput["classification"].(string)
			c.SwitchFieldName = fieldName
		} else {
			// Fallback to expression-based switch
			pathExpr, err := compileNativePath(switchRoot, switchSteps)
			if err == nil {
				c.SwitchExprGo = ensureString(pathExpr)
			}
		}

		for idx, kase := range ce.Condition.Cases {
			c.Cases = append(c.Cases, condCase{
				Name:       kase.Name,
				MatchValue: switchVals[idx],
				Target:     kase.Target,
			})
		}
		return &c, nil
	}

	// Fallback: sequential predicates.
	for _, kase := range ce.Condition.Cases {
		expr := strings.TrimSpace(kase.Predicate.Expression)
		goExpr, err := compileCELLiteToGoPredicate(expr)
		if err != nil {
			return nil, fmt.Errorf("invalid predicate %q: %w", expr, err)
		}
		c.Cases = append(c.Cases, condCase{
			Name:        kase.Name,
			PredicateGo: goExpr,
			Target:      kase.Target,
		})
	}

	return &c, nil
}

func parseAndCompileCELExpr(cfg map[string]any, key string, path string) (exprGo string, err error) {
	if cfg == nil {
		return "", nil
	}
	raw, ok := cfg[key]
	if !ok || raw == nil {
		return "", nil
	}
	exprMap, ok := raw.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s must be an object", path)
	}

	exprStr, _ := stringField(exprMap, "expression")
	exprStr = strings.TrimSpace(exprStr)
	if exprStr == "" {
		return "", nil
	}

	format, _ := stringField(exprMap, "format")
	format = strings.TrimSpace(format)
	if format == "" {
		format = "cel"
	}
	if format != "cel" {
		return "", fmt.Errorf("%s.format must be %q", path, "cel")
	}

	goExpr, err := compileCELLiteToGoValue(exprStr)
	if err != nil {
		return "", fmt.Errorf("%s: invalid CEL expression: %w", path, err)
	}

	// Reject input.output_parsed/input.output_raw in non-routing contexts.
	// These compile to parsedOutput/rawOutput which are only defined in routing functions.
	// Note: This uses substring matching which may produce false positives if user field
	// names contain "parsedOutput" or "rawOutput" (see known limitations in package doc).
	if strings.Contains(goExpr, "parsedOutput") || strings.Contains(goExpr, "rawOutput") {
		return "", fmt.Errorf("%s: input.output_parsed and input.output_raw are only supported in conditional_edges routing functions (if your field name contains 'parsedOutput'/'rawOutput', this is a known limitation)", path)
	}

	return goExpr, nil
}

func parseSetStateAssignments(nodeID string, cfg map[string]any) ([]setStateAssignment, error) {
	if cfg == nil {
		return nil, nil
	}
	raw := cfg["assignments"]
	if raw == nil {
		return nil, nil
	}

	var list []any
	switch v := raw.(type) {
	case []any:
		list = v
	case []map[string]any:
		list = make([]any, 0, len(v))
		for _, item := range v {
			list = append(list, item)
		}
	default:
		return nil, fmt.Errorf("builtin.set_state[%s]: assignments must be an array", nodeID)
	}

	out := make([]setStateAssignment, 0, len(list))

	for i, item := range list {
		assignMap, ok := item.(map[string]any)
		if !ok || assignMap == nil {
			return nil, fmt.Errorf("builtin.set_state[%s]: assignments[%d] must be an object", nodeID, i)
		}

		field, _ := assignMap["field"].(string)
		if field == "" {
			// Allow legacy naming.
			field, _ = assignMap["name"].(string)
		}
		field = strings.TrimSpace(field)
		if field == "" {
			return nil, fmt.Errorf("builtin.set_state[%s]: assignments[%d].field is required", nodeID, i)
		}

		exprMap, ok := assignMap["expr"].(map[string]any)
		if !ok || exprMap == nil {
			return nil, fmt.Errorf("builtin.set_state[%s]: assignments[%d].expr must be an object", nodeID, i)
		}

		exprStr, _ := stringField(exprMap, "expression")
		exprStr = strings.TrimSpace(exprStr)
		if exprStr == "" {
			return nil, fmt.Errorf("builtin.set_state[%s]: assignments[%d].expr.expression is required", nodeID, i)
		}

		format, _ := stringField(exprMap, "format")
		format = strings.TrimSpace(format)
		if format == "" {
			format = "cel"
		}
		if format != "cel" {
			return nil, fmt.Errorf("builtin.set_state[%s]: assignments[%d].expr.format must be %q", nodeID, i, "cel")
		}

		goExpr, err := compileCELLiteToGoValue(exprStr)
		if err != nil {
			return nil, fmt.Errorf("builtin.set_state[%s]: invalid CEL expression for field %q: %w", nodeID, field, err)
		}

		// Reject input.output_parsed/input.output_raw in set_state nodes.
		// Note: uses heuristic substring matching; may false-positive on field names containing these substrings.
		if strings.Contains(goExpr, "parsedOutput") || strings.Contains(goExpr, "rawOutput") {
			return nil, fmt.Errorf("builtin.set_state[%s]: input.output_parsed and input.output_raw are only supported in conditional_edges routing functions (if your field name contains 'parsedOutput'/'rawOutput', this is a known limitation)", nodeID)
		}

		out = append(out, setStateAssignment{
			Field:  field,
			ExprGo: goExpr,
		})
	}

	return out, nil
}

func buildMCPNodeIR(node dsl.Node, fromNodeID string) (mcpNode, error) {
	engine := node.EngineNode

	parsed, err := mcpconfig.ParseNodeConfig(engine.Config)
	if err != nil {
		return mcpNode{}, fmt.Errorf("builtin.mcp[%s]: %w", node.ID, err)
	}

	headers := sortedKVPairs(parsed.Headers)

	var params []mcpParam
	if len(parsed.Params) > 0 {
		names := make([]string, 0, len(parsed.Params))
		for name := range parsed.Params {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			raw := parsed.Params[name]
			exprMap, ok := raw.(map[string]any)
			if !ok || exprMap == nil {
				return mcpNode{}, fmt.Errorf("builtin.mcp[%s]: params[%q] must be an object", node.ID, name)
			}
			exprStr, _ := stringField(exprMap, "expression")
			exprStr = strings.TrimSpace(exprStr)
			if exprStr == "" {
				continue
			}
			format, _ := stringField(exprMap, "format")
			format = strings.TrimSpace(format)
			if format == "" {
				format = "cel"
			}
			if format != "cel" {
				return mcpNode{}, fmt.Errorf("builtin.mcp[%s]: params[%q].format must be %q", node.ID, name, "cel")
			}
			goExpr, err := compileCELLiteToGoValue(exprStr)
			if err != nil {
				return mcpNode{}, fmt.Errorf("builtin.mcp[%s]: invalid CEL expression for param %q: %w", node.ID, name, err)
			}
			// MCP nodes only have parsedOutput defined when there's an incoming edge.
			if strings.Contains(goExpr, "rawOutput") {
				return mcpNode{}, fmt.Errorf("builtin.mcp[%s]: input.output_raw is not supported in MCP node params (use input.output_parsed instead)", node.ID)
			}
			if strings.Contains(goExpr, "parsedOutput") && fromNodeID == "" {
				return mcpNode{}, fmt.Errorf("builtin.mcp[%s]: input.output_parsed requires an incoming edge (the node has no upstream node)", node.ID)
			}
			params = append(params, mcpParam{Name: name, ExprGo: goExpr})
		}
	}

	return mcpNode{
		ID:         node.ID,
		FuncName:   "node" + toCamel(node.ID),
		FromNodeID: fromNodeID,
		Transport:  parsed.Transport,
		ServerURL:  parsed.ServerURL,
		Headers:    headers,
		ToolName:   parsed.ToolName,
		Params:     params,
	}, nil
}

func equalCelPathSteps(a, b []celPathStep) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].isIndex != b[i].isIndex {
			return false
		}
		if a[i].isIndex {
			if a[i].index != b[i].index {
				return false
			}
			continue
		}
		if a[i].key != b[i].key {
			return false
		}
	}
	return true
}

// detectHelperUsage scans the IR and sets NeedsXxx flags.
func detectHelperUsage(ir *irGraph) {
	// Check state variables for mustParseJSONAny usage.
	for _, sv := range ir.StateVars {
		if sv.DefaultJSON != "" {
			ir.NeedsMustParseJSONAny = true
		}
	}

	// Check end nodes with JSON expressions for mustParseJSONAny usage.
	for _, e := range ir.EndNodes {
		if e.ExprKind == "json" && e.ExprJSON != "" {
			ir.NeedsMustParseJSONAny = true
		}
	}
}

// sortedKVPairs converts a map to a sorted slice of kvPair for deterministic output.
func sortedKVPairs(m map[string]string) []kvPair {
	pairs := make([]kvPair, 0, len(m))
	for k, v := range m {
		pairs = append(pairs, kvPair{Key: k, Value: v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Key < pairs[j].Key })
	return pairs
}

func stringField(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	if v, ok := m[key]; ok {
		if s, ok2 := v.(string); ok2 {
			return s, true
		}
	}
	return "", false
}

func toCamel(id string) string {
	seps := func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}
	parts := strings.FieldsFunc(id, seps)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
	}
	if len(parts) == 0 {
		return "Node"
	}
	out := strings.Join(parts, "")
	if out == "" {
		return "Node"
	}
	// Go identifiers cannot start with a digit.
	if out[0] >= '0' && out[0] <= '9' {
		out = "N" + out
	}
	return out
}

func structuredSchemaName(nodeID string) string {
	const (
		maxLen = 64
		prefix = "schema_"
	)

	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return prefix + "output"
	}

	var b strings.Builder
	b.Grow(len(prefix) + len(nodeID))
	b.WriteString(prefix)

	for i := 0; i < len(nodeID); i++ {
		c := nodeID[i]
		if (c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '_' || c == '-' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
	}

	name := b.String()
	trimmed := strings.Trim(name[len(prefix):], "_-")
	if trimmed == "" {
		name = prefix + "output"
	} else {
		name = prefix + trimmed
	}
	if len(name) > maxLen {
		name = name[:maxLen]
	}
	return name
}

func renderTemplate(tmpl string, data any) ([]byte, error) {
	t, err := template.New("code").Funcs(template.FuncMap{
		"goString": func(s string) string {
			if s == "" {
				return `""`
			}
			// Prefer raw string literals for readability when possible.
			// Fall back to quoted string when the text contains backticks.
			if strings.Contains(s, "`") {
				return fmt.Sprintf("%q", s)
			}
			return "`" + s + "`"
		},
		"truncateInstruction": func(s string, maxLen int) string {
			// Extract first line or truncate to maxLen for comment display.
			s = strings.TrimSpace(s)
			if idx := strings.Index(s, "\n"); idx >= 0 {
				s = s[:idx]
			}
			s = strings.TrimSpace(s)
			if len(s) > maxLen {
				return s[:maxLen-3] + "..."
			}
			return s
		},
	}).Parse(tmpl)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return nil, err
	}
	// gofmt
	src, err := format.Source(buf.Bytes())
	if err != nil {
		// If formatting fails, still return the unformatted code for easier debugging.
		return buf.Bytes(), nil
	}
	return src, nil
}
