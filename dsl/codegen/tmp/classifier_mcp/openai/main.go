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
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	openaiserver "trpc.group/trpc-go/trpc-agent-go/server/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
)

// =============================================================================
// Configuration
// =============================================================================

const appName = "classifier_mcp_example"

var (
	address = flag.String("address", "127.0.0.1:8080", "Listen address")
)

// =============================================================================
// Entry Point
// =============================================================================

func main() {
	flag.Parse()

	g, err := BuildGraph()
	if err != nil {
		log.Fatalf("Failed to build graph: %v", err)
	}
	ga, err := graphagent.New(appName, g, graphagent.WithSubAgents(createSubAgents()))
	if err != nil {
		log.Fatalf("Failed to create graph agent: %v", err)
	}

	server, err := openaiserver.New(
		openaiserver.WithAgent(ga),
		openaiserver.WithBasePath("/v1"),
		openaiserver.WithModelName(appName),
	)
	if err != nil {
		log.Fatalf("Failed to create OpenAI server: %v", err)
	}
	defer server.Close()

	log.Infof("OpenAI: serving agent %q on http://%s/v1/chat/completions", appName, *address)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("Server stopped with error: %v", err)
	}
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
