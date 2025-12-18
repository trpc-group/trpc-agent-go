// Generated from DSL workflow "openai_custom_service".
//
// How to run:
//  1. Put this file in an empty folder as main.go
//  2. go mod init example.com/mydslapp && go get trpc.group/trpc-go/trpc-agent-go@latest && go mod tidy
//  3. Set environment variables:
//     export OPENAI_API_KEY="..."  # https://api.deepseek.com/v1 (used by: classifier, return_agent, retention_agent, information_agent)
//  4. go run .
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// =============================================================================
// Configuration
// =============================================================================

const appName = "openai_custom_service"

var demoInput = `I'm thinking about cancelling my mobile plan, can you offer me a better deal?`

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

	events, err := r.Run(context.Background(), "demo-user", "demo-session", model.NewUserMessage(demoInput))
	if err != nil {
		panic(err)
	}

	var lastText string
	var streamedText strings.Builder
	var didStream bool
	var interruptNode string
	var interruptValue any

	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.Response != nil && ev.Error != nil {
			fmt.Printf("Event error: %s\n", ev.Error.Message)
			continue
		}
		if ev.StateDelta != nil {
			if raw, ok := ev.StateDelta[graph.MetadataKeyPregel]; ok && raw != nil {
				var meta graph.PregelStepMetadata
				if err := json.Unmarshal(raw, &meta); err == nil && meta.NodeID != "" && meta.InterruptValue != nil {
					interruptNode = meta.NodeID
					interruptValue = meta.InterruptValue
				}
			}
		}
		if ev.Response != nil {
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
	}

	if didStream {
		fmt.Println()
	}
	if interruptNode != "" {
		b, _ := json.MarshalIndent(interruptValue, "", "  ")
		fmt.Printf("\n[interrupt] node=%q value=%s\n", interruptNode, string(b))
		fmt.Println("Graph interrupted. Resume with approval value to continue.")
		return
	}
	if !didStream && lastText != "" {
		fmt.Println("Final response:", lastText)
	} else if streamedText.Len() == 0 && strings.TrimSpace(lastText) == "" {
		fmt.Println("Graph completed with no output.")
	}
}

// =============================================================================
// Graph Definition
// =============================================================================

func BuildGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	schema.AddField("approval_result", graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField("end_structured_output", graph.StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: graph.DefaultReducer,
	})

	sg := graph.NewStateGraph(schema)

	// Nodes.
	sg.AddNode("start", func(ctx context.Context, state graph.State) (any, error) { return nil, nil })
	sg.AddAgentNode("classifier", graph.WithSubgraphOutputMapper(agentStructuredOutputMapper("classifier")))
	sg.AddAgentNode("information_agent")
	sg.AddAgentNode("retention_agent")
	sg.AddAgentNode("return_agent")
	sg.AddNode("retention_approval", nodeRetentionApproval, graph.WithNodeType(graph.NodeTypeRouter))
	sg.AddNode("info_end", nodeInfoEnd, graph.WithNodeType(graph.NodeTypeFunction))
	sg.AddNode("retention_end", nodeRetentionEnd, graph.WithNodeType(graph.NodeTypeFunction))
	sg.AddNode("retention_reject_end", nodeRetentionRejectEnd, graph.WithNodeType(graph.NodeTypeFunction))
	sg.AddNode("return_end", nodeReturnEnd, graph.WithNodeType(graph.NodeTypeFunction))

	// Edges.
	sg.AddEdge("info_end", "__end__")
	sg.AddEdge("information_agent", "info_end")
	sg.AddEdge("retention_agent", "retention_approval")
	sg.AddEdge("retention_end", "__end__")
	sg.AddEdge("retention_reject_end", "__end__")
	sg.AddEdge("return_agent", "return_end")
	sg.AddEdge("return_end", "__end__")
	sg.AddEdge("start", "classifier")
	sg.AddConditionalEdges("classifier", routeEdgeRouteByClassification, nil)
	sg.AddConditionalEdges("retention_approval", routeEdgeRouteAfterRetentionApproval, nil)

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
	case "return_item":
		return "return_agent", nil
	case "cancel_subscription":
		return "retention_agent", nil
	case "get_information":
		return "information_agent", nil
	default:
		return "", fmt.Errorf("no matching case for classification=%q", classification)
	}
}

func routeEdgeRouteAfterRetentionApproval(ctx context.Context, state graph.State) (string, error) {
	_ = ctx
	parsedOutput, _ := state["retention_approval_parsed"].(map[string]any)
	rawOutput, _ := state["retention_approval_output"].(string)
	_, _ = parsedOutput, rawOutput
	v := func() string { s, _ := state["approval_result"].(string); return s }()
	switch v {
	case "approve":
		return "retention_end", nil
	case "reject":
		return "retention_reject_end", nil
	default:
		return "", fmt.Errorf("no matching case")
	}
}

// =============================================================================
// Node Functions
// =============================================================================

func nodeInfoEnd(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	return graph.State{}, nil
}

func nodeRetentionEnd(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	value := mustParseJSONAny(`{"message": "Your retention offer has been accepted. Thank you for staying with us."}`)
	stateDelta := graph.State{"end_structured_output": value}
	if b, err := json.Marshal(value); err == nil && strings.TrimSpace(string(b)) != "" {
		stateDelta[graph.StateKeyLastResponse] = string(b)
	}
	return stateDelta, nil
}

func nodeRetentionRejectEnd(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	value := mustParseJSONAny(`{"message": "We understand your decision. If you change your mind, we are always here to help."}`)
	stateDelta := graph.State{"end_structured_output": value}
	if b, err := json.Marshal(value); err == nil && strings.TrimSpace(string(b)) != "" {
		stateDelta[graph.StateKeyLastResponse] = string(b)
	}
	return stateDelta, nil
}

func nodeReturnEnd(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	return graph.State{}, nil
}

func nodeRetentionApproval(ctx context.Context, state graph.State) (any, error) {
	resumeValue, err := graph.Interrupt(ctx, state, "retention_approval", map[string]any{
		"message": "Does this retention offer work for you?",
		"node_id": "retention_approval",
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
}

// =============================================================================
// Agent Constructors
// =============================================================================

func createSubAgents() []agent.Agent {
	return []agent.Agent{
		newClassifierSubAgent(),
		newInformationAgentSubAgent(),
		newRetentionAgentSubAgent(),
		newReturnAgentSubAgent(),
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
	opts = append(opts, llmagent.WithInstruction(`Classify the user’s intent into one of the following categories: "return_item", "cancel_subscription", or "get_information".

1. Any device-related return requests should route to return_item.
2. Any retention or cancellation risk, including any request for discounts should route to cancel_subscription.
3. Any other requests should go to get_information.`))
	opts = append(opts, llmagent.WithStructuredOutputJSONSchema("schema_classifier", mustParseJSONMap(`{"properties":{"classification":{"description":"Classification of user intent","enum":["return_item","cancel_subscription","get_information"],"type":"string"}},"required":["classification"],"type":"object"}`), true, ""))
	var genConfig model.GenerationConfig
	{
		t := 0.7
		genConfig.Temperature = &t
	}
	opts = append(opts, llmagent.WithGenerationConfig(genConfig))

	return llmagent.New("classifier", opts...)
}

func newInformationAgentSubAgent() agent.Agent {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		panic("environment variable OPENAI_API_KEY is not set")
	}

	modelOpts := []openai.Option{openai.WithAPIKey(apiKey)}
	modelOpts = append(modelOpts, openai.WithBaseURL("https://api.deepseek.com/v1"))
	llmModel := openai.New("deepseek-chat", modelOpts...)

	opts := []llmagent.Option{llmagent.WithModel(llmModel)}
	opts = append(opts, llmagent.WithInstruction(`You are an information agent for answering informational queries. Your aim is to provide clear, concise responses to user questions. Use the following policy to assemble your answer.

Company Name: HorizonTel Communications
Industry: Telecommunications
Region: North America

Policy Summary: Mobile Service Plan Adjustments
- Customers must have an active account in good standing (no outstanding balance > $50).
- Device upgrades are permitted once every 12 months if the customer is on an eligible plan.
- Early upgrades incur a $99 early-change fee unless the new plan’s monthly cost is higher by at least $15.
- Downgrades: Customers can switch to a lower-tier plan at any time; changes take effect at the next billing cycle.
- Overcharges under $10 are automatically credited to the next bill; above that require supervisor review.
- Refunds are issued to the original payment method within 7–10 business days.
- Customers experiencing service interruption exceeding 24 consecutive hours are eligible for a 1-day service credit upon request.

Always respond in a friendly, concise way and surface the most relevant parts of the policy.`))
	var genConfig model.GenerationConfig
	{
		t := 0.7
		genConfig.Temperature = &t
	}
	{
		mt := 1024
		genConfig.MaxTokens = &mt
	}
	opts = append(opts, llmagent.WithGenerationConfig(genConfig))

	return llmagent.New("information_agent", opts...)
}

func newRetentionAgentSubAgent() agent.Agent {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		panic("environment variable OPENAI_API_KEY is not set")
	}

	modelOpts := []openai.Option{openai.WithAPIKey(apiKey)}
	modelOpts = append(modelOpts, openai.WithBaseURL("https://api.deepseek.com/v1"))
	llmModel := openai.New("deepseek-chat", modelOpts...)

	opts := []llmagent.Option{llmagent.WithModel(llmModel)}
	opts = append(opts, llmagent.WithInstruction(`You are a customer retention conversational agent whose goal is to prevent subscription cancellations. Ask for their current plan and reason for dissatisfaction. For now, you may simply say there is a 20% offer available for 1 year if that seems appropriate.`))
	var genConfig model.GenerationConfig
	{
		t := 0.7
		genConfig.Temperature = &t
	}
	{
		mt := 512
		genConfig.MaxTokens = &mt
	}
	opts = append(opts, llmagent.WithGenerationConfig(genConfig))

	return llmagent.New("retention_agent", opts...)
}

func newReturnAgentSubAgent() agent.Agent {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		panic("environment variable OPENAI_API_KEY is not set")
	}

	modelOpts := []openai.Option{openai.WithAPIKey(apiKey)}
	modelOpts = append(modelOpts, openai.WithBaseURL("https://api.deepseek.com/v1"))
	llmModel := openai.New("deepseek-chat", modelOpts...)

	opts := []llmagent.Option{llmagent.WithModel(llmModel)}
	opts = append(opts, llmagent.WithInstruction(`Offer a replacement device with free shipping.`))
	var genConfig model.GenerationConfig
	{
		t := 0.7
		genConfig.Temperature = &t
	}
	{
		mt := 512
		genConfig.MaxTokens = &mt
	}
	opts = append(opts, llmagent.WithGenerationConfig(genConfig))

	return llmagent.New("return_agent", opts...)
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
