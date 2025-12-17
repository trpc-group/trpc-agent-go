package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strings"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/modelspec"
	"trpc.group/trpc-go/trpc-agent-go/dsl/internal/outputformat"
)

// Options controls how Go code is generated from a DSL graph.
type Options struct {
	// PackageName is the package name for generated files (defaults to "main").
	PackageName string
	// AppName is a human‑readable application name used in main.go logging.
	// When empty, graph.Name is used.
	AppName string
}

// Output contains the generated Go source files keyed by filename.
type Output struct {
	Files map[string][]byte
}

// GenerateNativeGo generates Go source code (main.go) from a DSL Graph,
// following an "AgentNode style" blueprint:
//
//   - builtin.start         -> no-op Node
//   - builtin.llmagent      -> AgentNode(id) + one llmagent.Agent per node
//   - builtin.user_approval -> Interrupt-based NodeFunc
//   - builtin.end           -> End NodeFunc that writes end_structured_output
//   - conditional_edges     -> Go routing functions (only simple == supported)
//
// The generated code does not import the dsl package or CEL; it only depends on
// low-level packages like graph/agent/llmagent/model.
func GenerateNativeGo(g *dsl.Graph, opts Options) (*Output, error) {
	if g == nil {
		return nil, fmt.Errorf("graph is nil")
	}

	pkg := opts.PackageName
	if pkg == "" {
		pkg = "main"
	}
	appName := opts.AppName
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
		PackageName:   pkg,
		AppName:       appName,
		HasAgentNodes: len(ir.AgentNodes) > 0,
		EnvVars:       ir.EnvVars,
		NeedsApproval: ir.NeedsApproval,
		NeedsEnd:      ir.NeedsEnd,
		StartNodes:    ir.StartNodes,
		AgentNodes:    ir.AgentNodes,
		ApprovalNodes: ir.ApprovalNodes,
		EndNodes:      ir.EndNodes,
		Edges:         ir.Edges,
		Conditions:    ir.Conditions,
		EntryPoint:    ir.EntryPoint,
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
	// StructuredOutputSchemaJSON is the output_format.schema serialized as JSON
	// when output_format.type == "json".
	StructuredOutputSchemaJSON  string
	StructuredOutputSchemaName string
}

type kvPair struct {
	Key   string
	Value string
}

type agentModelSpec struct {
	ModelName string
	APIKey    string
	BaseURL   string
	Headers   []kvPair
	// ExtraFieldsJSON is an optional JSON object (serialized) passed to the model constructor.
	ExtraFieldsJSON string
}

type startNode struct {
	ID string
}

type approvalNode struct {
	ID       string
	FuncName string
	Message  string
}

type endNode struct {
	ID       string
	FuncName string
	// For now we only distinguish "hasExpr" vs no expr; expr JSON can be
	// inlined later if more complex logic is needed.
	HasExpr bool
	// Optional fixed message extracted from expr (when it can be parsed in simple cases).
	FixedMessage string
}

type edge struct {
	From string
	To   string
}

type condCase struct {
	Value  string
	Target string
}

type condition struct {
	ID       string
	From     string
	FuncName string
	Kind     string // "node_output_parsed" or "state_field"
	// For Kind == "node_output_parsed"
	OutputParsedField string
	// For Kind == "state_field"
	StateField string
	Cases      []condCase
	// DefaultTarget corresponds to Condition.Default in DSL.
	DefaultTarget string
}

type irGraph struct {
	StartNodes   []startNode
	AgentNodes    []agentNode
	ApprovalNodes []approvalNode
	EndNodes      []endNode
	Edges         []edge
	Conditions    []condition
	EntryPoint    string
	EnvVars       []string
	NeedsApproval bool
	NeedsEnd      bool
	NeedsJSON     bool
	NeedsStrings  bool
	// HasErrorConditions indicates whether any condition omits a default
	// target and thus needs fmt.Errorf in the generated code.
	HasErrorConditions bool
}

func buildIR(g *dsl.Graph) (*irGraph, error) {
	ir := &irGraph{
		EntryPoint: g.StartNodeID,
	}
	envVars := map[string]struct{}{}
	collectEnvVarsFromString := func(raw string) {
		raw = strings.TrimSpace(raw)
		if !strings.HasPrefix(raw, "env:") {
			return
		}
		name := strings.TrimSpace(strings.TrimPrefix(raw, "env:"))
		if name == "" {
			return
		}
		envVars[name] = struct{}{}
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
			// Security: never embed plaintext api_key into generated code.
			// If the DSL already uses env:VAR, keep it; otherwise default to env:OPENAI_API_KEY.
			spec.APIKey = strings.TrimSpace(spec.APIKey)
			if !strings.HasPrefix(spec.APIKey, "env:") {
				spec.APIKey = "env:OPENAI_API_KEY"
			}
			collectEnvVarsFromString(spec.APIKey)
			collectEnvVarsFromString(spec.BaseURL)
			collectEnvVarsFromString(spec.ModelName)

			headers := make([]kvPair, 0, len(spec.Headers))
			for k, v := range spec.Headers {
				headers = append(headers, kvPair{Key: k, Value: v})
				collectEnvVarsFromString(v)
			}
			sort.Slice(headers, func(i, j int) bool { return headers[i].Key < headers[j].Key })

			var extraFieldsJSON string
			if len(spec.ExtraFields) > 0 {
				b, err := json.Marshal(spec.ExtraFields)
				if err != nil {
					return nil, fmt.Errorf("builtin.llmagent[%s]: failed to marshal model_spec.extra_fields: %w", n.ID, err)
				}
				extraFieldsJSON = string(b)
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
				ir.NeedsJSON = true
			}

			ir.AgentNodes = append(ir.AgentNodes, agentNode{
				ID:          n.ID,
				FuncSuffix:  toCamel(n.ID),
				Instruction: inst,
				ModelSpec: agentModelSpec{
					ModelName:       spec.ModelName,
					APIKey:          spec.APIKey,
					BaseURL:         spec.BaseURL,
					Headers:         headers,
					ExtraFieldsJSON: extraFieldsJSON,
				},
				StructuredOutputSchemaJSON:  structuredOutputSchemaJSON,
				StructuredOutputSchemaName: structuredOutputSchemaName,
			})
		case "builtin.start":
			ir.StartNodes = append(ir.StartNodes, startNode{ID: n.ID})
		case "builtin.user_approval":
			msg, _ := stringField(n.EngineNode.Config, "message")
			if msg == "" {
				msg = "Please approve this action (yes/no):"
			}
			ir.ApprovalNodes = append(ir.ApprovalNodes, approvalNode{
				ID:       n.ID,
				FuncName: "node" + toCamel(n.ID),
				Message:  msg,
			})
			ir.NeedsApproval = true
		case "builtin.end":
			e := endNode{
				ID:       n.ID,
				FuncName: "node" + toCamel(n.ID),
			}
			if exprAny, ok := n.EngineNode.Config["expr"]; ok {
				if exprMap, ok2 := exprAny.(map[string]any); ok2 {
					if format, _ := stringField(exprMap, "format"); format == "json" {
							if raw, _ := stringField(exprMap, "expression"); raw != "" {
								e.HasExpr = true
								// Non-strict JSON parsing: only try to extract {"message": "..."}.
								e.FixedMessage = extractMessageField(raw)
							}
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
			if c.DefaultTarget == "" {
				ir.HasErrorConditions = true
			}
		}
	}

	// Validate that node_output_parsed conditions reference nodes that
	// actually produce structured output.
	hasStructuredByID := make(map[string]bool, len(ir.AgentNodes))
	for _, n := range ir.AgentNodes {
		hasStructuredByID[n.ID] = strings.TrimSpace(n.StructuredOutputSchemaJSON) != ""
	}
	needsOutputParsed := false
	for _, c := range ir.Conditions {
		if c.Kind != "node_output_parsed" {
			continue
		}
		needsOutputParsed = true
		if strings.TrimSpace(c.OutputParsedField) == "" {
			return nil, fmt.Errorf("conditional_edge %q from %q: input.output_parsed field is empty", c.ID, c.From)
		}
		if !hasStructuredByID[c.From] {
			return nil, fmt.Errorf("conditional_edge %q from %q: uses input.output_parsed but the node has no output_format.type=json schema configured", c.ID, c.From)
		}
	}

	ir.NeedsStrings = ir.NeedsApproval || ir.NeedsEnd || ir.NeedsJSON || needsOutputParsed

	// Stabilize output ordering for deterministic output and readability.
	sort.Slice(ir.StartNodes, func(i, j int) bool { return ir.StartNodes[i].ID < ir.StartNodes[j].ID })
	sort.Slice(ir.AgentNodes, func(i, j int) bool { return ir.AgentNodes[i].ID < ir.AgentNodes[j].ID })
	sort.Slice(ir.ApprovalNodes, func(i, j int) bool { return ir.ApprovalNodes[i].ID < ir.ApprovalNodes[j].ID })
	sort.Slice(ir.EndNodes, func(i, j int) bool { return ir.EndNodes[i].ID < ir.EndNodes[j].ID })
	sort.Slice(ir.Edges, func(i, j int) bool {
		if ir.Edges[i].From == ir.Edges[j].From {
			return ir.Edges[i].To < ir.Edges[j].To
		}
		return ir.Edges[i].From < ir.Edges[j].From
	})
	sort.Slice(ir.Conditions, func(i, j int) bool {
		if ir.Conditions[i].From == ir.Conditions[j].From {
			return ir.Conditions[i].FuncName < ir.Conditions[j].FuncName
		}
		return ir.Conditions[i].From < ir.Conditions[j].From
	})

	if len(envVars) > 0 {
		ir.EnvVars = make([]string, 0, len(envVars))
		for k := range envVars {
			ir.EnvVars = append(ir.EnvVars, k)
		}
		sort.Strings(ir.EnvVars)
	}

	return ir, nil
}

func buildCondition(ce dsl.ConditionalEdge) (*condition, error) {
	if len(ce.Condition.Cases) == 0 {
		return nil, nil
	}

	funcName := ""
	if strings.TrimSpace(ce.ID) != "" {
		funcName = "route" + toCamel("edge_" + ce.ID)
	} else if strings.TrimSpace(ce.From) != "" {
		funcName = "route" + toCamel("from_" + ce.From)
	} else {
		funcName = "routeNode"
	}

	c := condition{
		ID:            ce.ID,
		From:          ce.From,
		FuncName:      funcName,
		DefaultTarget: ce.Condition.Default,
	}

	// Infer Kind from the first predicate expression.
	firstExpr := strings.TrimSpace(ce.Condition.Cases[0].Predicate.Expression)
	switch {
	case strings.HasPrefix(firstExpr, "input.output_parsed."):
		c.Kind = "node_output_parsed"
	case strings.HasPrefix(firstExpr, "state."):
		c.Kind = "state_field"
	default:
		return nil, fmt.Errorf("unsupported predicate %q (only input.output_parsed.* or state.* == \"value\" are supported)", firstExpr)
	}

	for _, kase := range ce.Condition.Cases {
		expr := strings.TrimSpace(kase.Predicate.Expression)
		value, field, err := parseEqualityExpr(expr)
		if err != nil {
			return nil, err
		}
		switch c.Kind {
		case "state_field":
			if c.StateField == "" {
				c.StateField = field
			} else if c.StateField != field {
				return nil, fmt.Errorf("mixed state fields in cases: %q vs %q", c.StateField, field)
			}
		case "node_output_parsed":
			if c.OutputParsedField == "" {
				c.OutputParsedField = field
			} else if c.OutputParsedField != field {
				return nil, fmt.Errorf("mixed output_parsed fields in cases: %q vs %q", c.OutputParsedField, field)
			}
		}
		c.Cases = append(c.Cases, condCase{
			Value:  value,
			Target: kase.Target,
		})
	}

	return &c, nil
}

// parseEqualityExpr only supports expressions like:
//
//	input.output_parsed.xxx == "value"
//	state.xxx == "value"
func parseEqualityExpr(expr string) (value string, field string, err error) {
	parts := strings.Split(expr, "==")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unsupported predicate %q (expected ==)", expr)
	}
	left := strings.TrimSpace(parts[0])
	right := strings.TrimSpace(parts[1])

	if strings.HasPrefix(left, "input.output_parsed.") {
		field = strings.TrimPrefix(left, "input.output_parsed.")
	} else if strings.HasPrefix(left, "state.") {
		field = strings.TrimPrefix(left, "state.")
	} else {
		return "", "", fmt.Errorf("unsupported left side %q (expected input.output_parsed.* or state.*)", left)
	}

	// Right side must be a quoted string literal.
	if len(right) < 2 || right[0] != '"' || right[len(right)-1] != '"' {
		return "", "", fmt.Errorf("unsupported right side %q (expected quoted string)", right)
	}
	value = right[1 : len(right)-1]
	return value, field, nil
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

// extractMessageField tries to extract the "message" field from a simple JSON object string.
// This is intentionally a very rough matcher to avoid extra dependencies.
func extractMessageField(raw string) string {
	raw = strings.TrimSpace(raw)
	// Expect something like {"message": "..."} (whitespace ignored).
	const key = `"message"`
	idx := strings.Index(raw, key)
	if idx == -1 {
		return ""
	}
	rest := raw[idx+len(key):]
	colon := strings.Index(rest, ":")
	if colon == -1 {
		return ""
	}
	valPart := strings.TrimSpace(rest[colon+1:])
	if len(valPart) < 2 || valPart[0] != '"' {
		return ""
	}
	valPart = valPart[1:]
	end := strings.Index(valPart, `"`)
	if end == -1 {
		return ""
	}
	return valPart[:end]
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

// ---- Template ----

type singleFileTemplateData struct {
	PackageName   string
	AppName       string
	HasAgentNodes bool
	EnvVars       []string
	NeedsApproval bool
	NeedsEnd      bool
	StartNodes    []startNode
	AgentNodes    []agentNode
	ApprovalNodes []approvalNode
	EndNodes      []endNode
	Edges         []edge
	Conditions    []condition
	EntryPoint    string
}

const singleFileTemplate = `
// Generated from DSL workflow "{{ .AppName }}".
//
// How to run this example (recommended: standalone folder + go.mod):
//   1) Put this file in an empty folder as main.go
//   2) Init a module and add deps:
//        go mod init example.com/mydslapp
//        go get trpc.group/trpc-go/trpc-agent-go@latest
//        go mod tidy
//   3) Configure env vars (only needed if you kept env:VAR placeholders):
//      NOTE: api_key is always read from env; plaintext keys in the DSL are ignored.
{{- if .EnvVars }}
{{- range .EnvVars }}
//        export {{ . }}="..."
{{- end }}
{{- else }}
//        (none)
{{- end }}
//   4) Run:
//        go run .
//
package {{ .PackageName }}

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	{{- if or .NeedsApproval .NeedsEnd }}
	"reflect"
	{{- end }}
	"strings"

	{{- if .HasAgentNodes }}
	"trpc.group/trpc-go/trpc-agent-go/agent"
	{{- end }}
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	{{- if .HasAgentNodes }}
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	{{- end }}
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	{{- if .HasAgentNodes }}
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	{{- end }}
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// ---- User Editable Section --------------------------------------------------

const appName = {{ printf "%q" .AppName }}

// demoInput is the example user prompt used by main(). Feel free to edit any
// part of this file; this is just a convenient starting point.
var demoInput = {{ goString "I'm thinking about cancelling my mobile plan, can you offer me a better deal?" }}

// ---- Entry Point ------------------------------------------------------------

func main() {
	fmt.Println("Starting graph (generated from DSL, AgentNode style):", appName)

	g, err := BuildGraph()
	if err != nil {
		panic(err)
	}

	{{- if .HasAgentNodes }}
	var subAgents []agent.Agent
	{{- range .AgentNodes }}
	subAgents = append(subAgents, new{{ .FuncSuffix }}SubAgent())
	{{- end }}
	ga, err := graphagent.New(
		appName,
		g,
		graphagent.WithSubAgents(subAgents),
	)
	{{- else }}
	ga, err := graphagent.New(appName, g)
	{{- end }}
	if err != nil {
		panic(err)
	}

	sessSvc := inmemory.NewSessionService()
	r := runner.NewRunner(appName, ga, runner.WithSessionService(sessSvc))
	defer r.Close()

	ctx := context.Background()
	msg := model.NewUserMessage(demoInput)

	events, err := r.Run(ctx, "demo-user", "demo-session", msg)
	if err != nil {
		panic(err)
	}

	var (
		lastText       string
		streamedText   strings.Builder
		didStream      bool
		interruptNode  string
		interruptValue any
	)
	for ev := range events {
		if ev == nil || ev.Response == nil {
			continue
		}
		if ev.Error != nil {
			fmt.Printf("Event error: %s\n", ev.Error.Message)
			continue
		}

		// Interrupts are emitted as graph.pregel.step events with _pregel_metadata.
		if ev.StateDelta != nil {
			if raw, ok := ev.StateDelta[graph.MetadataKeyPregel]; ok && raw != nil {
				var meta graph.PregelStepMetadata
				if err := json.Unmarshal(raw, &meta); err == nil {
					if meta.NodeID != "" && meta.InterruptValue != nil {
						interruptNode = meta.NodeID
						interruptValue = meta.InterruptValue
					}
				}
			}
		}

		for _, ch := range ev.Choices {
			if ch.Delta.Content != "" {
				fmt.Print(ch.Delta.Content)
				streamedText.WriteString(ch.Delta.Content)
				didStream = true
			}
			if ch.Message.Role == model.RoleAssistant && ch.Message.Content != "" {
				lastText = ch.Message.Content
			}
		}
	}

	if didStream {
		fmt.Println()
	}

	if interruptNode != "" {
		b, _ := json.MarshalIndent(interruptValue, "", "  ")
		fmt.Printf("\n[interrupt] node=%q value=%s\n", interruptNode, string(b))
		fmt.Println("This graph contains user-approval. Add a resume flow or rerun with a resume value to continue.")
		return
	}

	if !didStream && lastText != "" {
		fmt.Println("Final response:", lastText)
		return
	}
	if streamedText.Len() == 0 && strings.TrimSpace(lastText) == "" {
		fmt.Println("Graph completed but produced no assistant text.")
		return
	}
}

// ---- Graph Definition -------------------------------------------------------

func BuildGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
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

	// start nodes: runtime no-op
	{{- range .StartNodes }}
	sg.AddNode({{ printf "%q" .ID }}, func(ctx context.Context, state graph.State) (any, error) {
		return nil, nil
	})
	{{- end }}

	// Agent nodes backed by sub-agents.
	{{- range .AgentNodes }}
	{{- if .StructuredOutputSchemaJSON }}
	sg.AddAgentNode({{ printf "%q" .ID }}, graph.WithSubgraphOutputMapper(agentStructuredOutputMapper({{ printf "%q" .ID }})))
	{{- else }}
	sg.AddAgentNode({{ printf "%q" .ID }})
	{{- end }}
	{{- end }}

	// Approval nodes.
	{{- range .ApprovalNodes }}
	sg.AddNode({{ printf "%q" .ID }}, {{ .FuncName }})
	{{- end }}

	// End nodes.
	{{- range .EndNodes }}
	sg.AddNode({{ printf "%q" .ID }}, {{ .FuncName }})
	{{- end }}

	// Edges.
	{{- range .Edges }}
	sg.AddEdge({{ printf "%q" .From }}, {{ printf "%q" .To }})
	{{- end }}

	// Conditional edges.
	{{- range .Conditions }}
	sg.AddConditionalEdges({{ printf "%q" .From }}, {{ .FuncName }}, nil)
	{{- end }}

	sg.SetEntryPoint({{ printf "%q" .EntryPoint }})
	return sg.Compile()
}

{{- range .Conditions }}
// {{ .FuncName }} routes based on {{ if eq .Kind "node_output_parsed" }}input.output_parsed.{{ .OutputParsedField }}{{ else }}state field{{ end }}.
func {{ .FuncName }}(ctx context.Context, state graph.State) (string, error) {
	{{- if eq .Kind "node_output_parsed" }}
	if v, ok := getNodeStructuredFieldString(state, {{ printf "%q" .From }}, {{ printf "%q" .OutputParsedField }}); ok {
		switch v {
		{{- range .Cases }}
		case {{ printf "%q" .Value }}:
			return {{ printf "%q" .Target }}, nil
		{{- end }}
		}
	}
	{{- if .DefaultTarget }}
	return {{ printf "%q" .DefaultTarget }}, nil
	{{- else }}
	return "", fmt.Errorf("no matching case for conditional from %s (field=%s)", {{ printf "%q" .From }}, {{ printf "%q" .OutputParsedField }})
	{{- end }}
	{{- else if eq .Kind "state_field" }}
	v, _ := state[{{ printf "%q" .StateField }}].(string)
	switch v {
	{{- range .Cases }}
	case {{ printf "%q" .Value }}:
		return {{ printf "%q" .Target }}, nil
	{{- end }}
	default:
		{{- if .DefaultTarget }}
		return {{ printf "%q" .DefaultTarget }}, nil
		{{- else }}
		return "", fmt.Errorf("invalid value %q for state field {{ .StateField }}", v)
		{{- end }}
	}
	{{- else }}
	_ = ctx
	_ = state
	return "", fmt.Errorf("unsupported condition kind: {{ .Kind }}")
	{{- end }}
}
{{- end }}

{{- range .ApprovalNodes }}
// {{ .FuncName }} is a user-approval node that interrupts execution
// and waits for a resume value, normalizing it into "approve" / "reject".
func {{ .FuncName }}(ctx context.Context, state graph.State) (any, error) {
	const nodeID = {{ printf "%q" .ID }}

	payload := map[string]any{
		"message": {{ printf "%q" .Message }},
		"node_id": nodeID,
	}

	resumeValue, err := graph.Interrupt(ctx, state, nodeID, payload)
	if err != nil {
		return nil, err
	}

	raw, _ := resumeValue.(string)
	decision := strings.ToLower(strings.TrimSpace(raw))

	normalized := "reject"
	if decision == "approve" || decision == "yes" || decision == "y" {
		normalized = "approve"
	}

	return graph.State{
		"approval_result": normalized,
	}, nil
}
{{- end }}

{{- range .EndNodes }}
// {{ .FuncName }} writes the final structured output for {{ .ID }}.
func {{ .FuncName }}(ctx context.Context, state graph.State) (any, error) {
	{{- if .HasExpr }}
	{{- if .FixedMessage }}
	return graph.State{
		"end_structured_output": map[string]any{
			"message": {{ printf "%q" .FixedMessage }},
		},
	}, nil
	{{- else }}
	// No simple message could be extracted from expr; keep empty map.
	return graph.State{
		"end_structured_output": map[string]any{},
	}, nil
	{{- end }}
	{{- else }}
	last, _ := state[graph.StateKeyLastResponse].(string)
	if strings.TrimSpace(last) == "" {
		return nil, nil
	}
	return graph.State{
		"end_structured_output": map[string]any{
			"message": last,
		},
	}, nil
	{{- end }}
}
{{- end }}

{{- range .AgentNodes }}
// new{{ .FuncSuffix }}SubAgent constructs the LLMAgent backing the "{{ .ID }}" AgentNode.
func new{{ .FuncSuffix }}SubAgent() agent.Agent {
	apiKey, err := resolveEnvString({{ printf "%q" .ModelSpec.APIKey }}, "{{ .ID }}.model_spec.api_key")
	if err != nil {
		panic(err)
	}

	var opts []openai.Option
	opts = append(opts, openai.WithAPIKey(apiKey))

	baseURL, err := resolveEnvString({{ printf "%q" .ModelSpec.BaseURL }}, "{{ .ID }}.model_spec.base_url")
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}

	headers := map[string]string{
		{{- range .ModelSpec.Headers }}
		{{ printf "%q" .Key }}: {{ printf "%q" .Value }},
		{{- end }}
	}
	resolved := make(map[string]string, len(headers))
	for k, v := range headers {
		rv, err := resolveEnvString(v, fmt.Sprintf("{{ .ID }}.model_spec.headers[%q]", k))
		if err != nil {
			panic(err)
		}
		if strings.TrimSpace(rv) != "" {
			resolved[k] = rv
		}
	}
	if len(resolved) > 0 {
		opts = append(opts, openai.WithHeaders(resolved))
	}

	extraFieldsJSON := {{ goString .ModelSpec.ExtraFieldsJSON }}
	if strings.TrimSpace(extraFieldsJSON) != "" {
		opts = append(opts, openai.WithExtraFields(mustParseJSONMap(extraFieldsJSON)))
	}

	modelName, err := resolveEnvString({{ printf "%q" .ModelSpec.ModelName }}, "{{ .ID }}.model_spec.model_name")
	if err != nil {
		panic(err)
	}
	llmModel := openai.New(modelName, opts...)

	instruction := {{ goString .Instruction }}
	return llmagent.New(
		"{{ .ID }}",
		llmagent.WithModel(llmModel),
		llmagent.WithInstruction(instruction),
		{{- if .StructuredOutputSchemaJSON }}
		llmagent.WithStructuredOutputJSONSchema({{ printf "%q" .StructuredOutputSchemaName }}, mustParseJSONMap({{ goString .StructuredOutputSchemaJSON }}), true, ""),
		{{- end }}
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
	)
}
{{- end }}

// ---- Helpers ---------------------------------------------------------------

func agentStructuredOutputMapper(nodeID string) graph.SubgraphOutputMapper {
	return func(parent graph.State, result graph.SubgraphResult) graph.State {
		last := result.LastResponse
		upd := graph.State{
			graph.StateKeyLastResponse:  last,
			graph.StateKeyNodeResponses: map[string]any{nodeID: last},
			graph.StateKeyUserInput:     "",
		}

		if strings.TrimSpace(last) == "" {
			return upd
		}
		jsonText, ok := extractFirstJSONObjectFromText(last)
		if !ok {
			return upd
		}
		var parsed any
		if err := json.Unmarshal([]byte(jsonText), &parsed); err != nil {
			return upd
		}

		nodeStructured := map[string]any{}
		if existingRaw, ok := parent["node_structured"]; ok {
			if existingMap, ok := existingRaw.(map[string]any); ok && existingMap != nil {
				for k, v := range existingMap {
					nodeStructured[k] = v
				}
			}
		}
		nodeStructured[nodeID] = map[string]any{
			"output_raw":    jsonText,
			"output_parsed": parsed,
		}
		upd["node_structured"] = nodeStructured
		return upd
	}
}

// extractFirstJSONObjectFromText tries to extract the first balanced top-level
// JSON object or array from the given text.
func extractFirstJSONObjectFromText(s string) (string, bool) {
	start := findJSONStartInText(s)
	if start == -1 {
		return "", false
	}
	return scanBalancedJSONInText(s, start)
}

func findJSONStartInText(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			return i
		}
	}
	return -1
}

func scanBalancedJSONInText(s string, start int) (string, bool) {
	stack := make([]byte, 0, 8)
	inString := false
	escaped := false

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

func getNodeStructuredFieldString(state graph.State, nodeID string, fieldPath string) (string, bool) {
	root, ok := state["node_structured"].(map[string]any)
	if !ok || root == nil {
		return "", false
	}
	nodeAny, ok := root[nodeID]
	if !ok {
		return "", false
	}
	nodeMap, ok := nodeAny.(map[string]any)
	if !ok || nodeMap == nil {
		return "", false
	}
	parsed, ok := nodeMap["output_parsed"]
	if !ok || parsed == nil {
		return "", false
	}
	return extractStringByPath(parsed, fieldPath)
}

func extractStringByPath(v any, fieldPath string) (string, bool) {
	fieldPath = strings.TrimSpace(fieldPath)
	if fieldPath == "" {
		s, ok := v.(string)
		return s, ok
	}
	cur := v
	for _, key := range strings.Split(fieldPath, ".") {
		m, ok := cur.(map[string]any)
		if !ok || m == nil {
			return "", false
		}
		next, ok := m[key]
		if !ok {
			return "", false
		}
		cur = next
	}
	s, ok := cur.(string)
	return s, ok
}

func resolveEnvString(value string, fieldPath string) (string, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "env:") {
		return value, nil
	}
	varName := strings.TrimSpace(strings.TrimPrefix(value, "env:"))
	if varName == "" {
		return "", fmt.Errorf("%s env placeholder is invalid (expected env:VAR)", fieldPath)
	}
	envVal, ok := os.LookupEnv(varName)
	if !ok || strings.TrimSpace(envVal) == "" {
		return "", fmt.Errorf("environment variable %q is not set (required by %s)", varName, fieldPath)
	}
	return envVal, nil
}

func mustParseJSONMap(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		panic(err)
	}
	return m
}
`
