// Generated from DSL workflow "classifier_mcp_example".
//
// How to run:
//  1. Put this file in an empty folder as main.go
//  2. go mod init example.com/mydslapp && go get trpc.group/trpc-go/trpc-agent-go@latest && go mod tidy
//  3. Set environment variables:
//     export OPENAI_API_KEY="..."  # https://api.deepseek.com/v1 (used by: classifier, simple_math_agent, complex_math_agent)
//  4. go run .
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// =============================================================================
// Configuration
// =============================================================================

const appName = "classifier_mcp_example"

// =============================================================================
// Entry Point
// =============================================================================

func main() {
	fmt.Println("Starting graph:", appName)

	g, err := BuildGraph()
	if err != nil {
		panic(err)
	}
	ga, err := graphagent.New(appName, g, graphagent.WithSubAgents(createSubAgents()))
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
}

func processStreamingResponse(eventChan <-chan *event.Event) error {
	var (
		didStream      bool
		lastText       string
		interruptNode  string
		interruptValue any
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

// =============================================================================
// Graph Definition
// =============================================================================

func BuildGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	schema.AddField("end_structured_output", graph.StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: graph.DefaultReducer,
	})

	sg := graph.NewStateGraph(schema)

	// Nodes.
	sg.AddNode("start", func(ctx context.Context, state graph.State) (any, error) { return nil, nil })
	sg.AddAgentNode("classifier", graph.WithSubgraphOutputMapper(agentStructuredOutputMapper("classifier")))
	sg.AddAgentNode("complex_math_agent")
	sg.AddAgentNode("simple_math_agent")
	sg.AddNode("complex_end", nodeComplexEnd, graph.WithNodeType(graph.NodeTypeFunction))
	sg.AddNode("simple_end", nodeSimpleEnd, graph.WithNodeType(graph.NodeTypeFunction))

	// Edges.
	sg.AddEdge("complex_end", "__end__")
	sg.AddEdge("complex_math_agent", "complex_end")
	sg.AddEdge("simple_end", "__end__")
	sg.AddEdge("simple_math_agent", "simple_end")
	sg.AddEdge("start", "classifier")
	sg.AddConditionalEdges("classifier", routeEdgeRouteByClassification, nil)

	sg.SetEntryPoint("start")
	return sg.Compile()
}

// =============================================================================
// Routing Functions
// =============================================================================

func routeEdgeRouteByClassification(ctx context.Context, state graph.State) (string, error) {
	_ = ctx
	parsedOutput, _ := state["classifier_parsed"].(map[string]any)
	classification, _ := parsedOutput["classification"].(string)
	switch classification {
	case "math_simple":
		return "simple_math_agent", nil
	case "math_complex":
		return "complex_math_agent", nil
	default:
		return "", fmt.Errorf("no matching case for classification=%q", classification)
	}
}

// =============================================================================
// Node Functions
// =============================================================================

func nodeComplexEnd(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	return graph.State{}, nil
}

func nodeSimpleEnd(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	return graph.State{}, nil
}

// =============================================================================
// Agent Constructors
// =============================================================================

func createSubAgents() []agent.Agent {
	return []agent.Agent{
		newClassifierSubAgent(),
		newComplexMathAgentSubAgent(),
		newSimpleMathAgentSubAgent(),
	}
}

func newClassifierSubAgent() agent.Agent {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		panic("environment variable OPENAI_API_KEY is not set")
	}

	modelOpts := []openai.Option{openai.WithAPIKey(apiKey)}
	modelOpts = append(modelOpts, openai.WithBaseURL("https://api.deepseek.com/v1"))
	llmModel := openai.New("deepseek-chat", modelOpts...)

	opts := []llmagent.Option{llmagent.WithModel(llmModel)}
	opts = append(opts, llmagent.WithInstruction(`You are a task classifier. Classify the user's request into one of two categories:

1. "math_simple" - Simple arithmetic operations like addition, subtraction (e.g., "1+1", "5-3", "add 2 and 3")
2. "math_complex" - Complex calculations involving multiplication, division, or multiple operations (e.g., "5*6", "10/2", "calculate (3+4)*2")

Analyze the user's request and output the classification.`))
	opts = append(opts, llmagent.WithStructuredOutputJSONSchema("schema_classifier", mustParseJSONMap(`{"properties":{"classification":{"description":"Classification of the math task","enum":["math_simple","math_complex"],"type":"string"},"reason":{"description":"Brief reason for the classification","type":"string"}},"required":["classification","reason"],"type":"object"}`), true, ""))
	var genConfig model.GenerationConfig
	{
		t := 0.3
		genConfig.Temperature = &t
	}
	opts = append(opts, llmagent.WithGenerationConfig(genConfig))

	return llmagent.New("classifier", opts...)
}

func newComplexMathAgentSubAgent() agent.Agent {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		panic("environment variable OPENAI_API_KEY is not set")
	}

	modelOpts := []openai.Option{openai.WithAPIKey(apiKey)}
	modelOpts = append(modelOpts, openai.WithBaseURL("https://api.deepseek.com/v1"))
	llmModel := openai.New("deepseek-chat", modelOpts...)

	opts := []llmagent.Option{llmagent.WithModel(llmModel)}
	opts = append(opts, llmagent.WithInstruction(`You are an advanced math assistant specializing in multiplication, division, and complex calculations. Use the calculator tools available via MCP to help users with their calculations. Always use the tools to compute results rather than calculating yourself.`))
	var mcpToolSets []tool.ToolSet
	{
		ts, err := newMCPToolSet("sse", "http://03.mcp-gateway.woa.com/245oyJGZ7scEkoA0", nil, []string{})
		if err != nil {
			panic(err)
		}
		mcpToolSets = append(mcpToolSets, ts)
	}
	opts = append(opts, llmagent.WithToolSets(mcpToolSets))
	var genConfig model.GenerationConfig
	{
		t := 0.5
		genConfig.Temperature = &t
	}
	{
		mt := 1024
		genConfig.MaxTokens = &mt
	}
	opts = append(opts, llmagent.WithGenerationConfig(genConfig))

	return llmagent.New("complex_math_agent", opts...)
}

func newSimpleMathAgentSubAgent() agent.Agent {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		panic("environment variable OPENAI_API_KEY is not set")
	}

	modelOpts := []openai.Option{openai.WithAPIKey(apiKey)}
	modelOpts = append(modelOpts, openai.WithBaseURL("https://api.deepseek.com/v1"))
	llmModel := openai.New("deepseek-chat", modelOpts...)

	opts := []llmagent.Option{llmagent.WithModel(llmModel)}
	opts = append(opts, llmagent.WithInstruction(`You are a simple math assistant specializing in addition and subtraction. Use the calculator tools available via MCP to help users with their calculations. Always use the tools to compute results rather than calculating yourself.`))
	var mcpToolSets []tool.ToolSet
	{
		ts, err := newMCPToolSet("sse", "http://03.mcp-gateway.woa.com/245oyJGZ7scEkoA0", nil, []string{})
		if err != nil {
			panic(err)
		}
		mcpToolSets = append(mcpToolSets, ts)
	}
	opts = append(opts, llmagent.WithToolSets(mcpToolSets))
	var genConfig model.GenerationConfig
	{
		t := 0.5
		genConfig.Temperature = &t
	}
	{
		mt := 1024
		genConfig.MaxTokens = &mt
	}
	opts = append(opts, llmagent.WithGenerationConfig(genConfig))

	return llmagent.New("simple_math_agent", opts...)
}

// =============================================================================
// Infrastructure (do not edit below this line)
// =============================================================================
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
