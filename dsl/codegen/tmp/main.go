// Generated from DSL workflow "openai_custom_service".
//
// How to run this example (recommended: standalone folder + go.mod):
//  1. Put this file in an empty folder as main.go
//  2. Init a module and add deps:
//     go mod init example.com/mydslapp
//     go get trpc.group/trpc-go/trpc-agent-go@latest
//     go mod tidy
//  3. Configure env vars (only needed if you kept env:VAR placeholders):
//     NOTE: api_key is always read from env; plaintext keys in the DSL are ignored.
//     export OPENAI_API_KEY="..."
//  4. Run:
//     go run .
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

// ---- User Editable Section --------------------------------------------------

const appName = "openai_custom_service"

// demoInput is the example user prompt used by main(). Feel free to edit any
// part of this file; this is just a convenient starting point.
var demoInput = `I'm thinking about cancelling my mobile plan, can you offer me a better deal?`

// ---- Entry Point ------------------------------------------------------------

func main() {
	fmt.Println("Starting graph (generated from DSL, AgentNode style):", appName)

	g, err := BuildGraph()
	if err != nil {
		panic(err)
	}
	var subAgents []agent.Agent
	subAgents = append(subAgents, newClassifierSubAgent())
	subAgents = append(subAgents, newInformationAgentSubAgent())
	subAgents = append(subAgents, newRetentionAgentSubAgent())
	subAgents = append(subAgents, newReturnAgentSubAgent())
	ga, err := graphagent.New(
		appName,
		g,
		graphagent.WithSubAgents(subAgents),
	)
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
	schema.AddField("approval_result", graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
	})
	schema.AddField("end_structured_output", graph.StateField{
		Type:    reflect.TypeOf(map[string]any{}),
		Reducer: graph.DefaultReducer,
	})

	sg := graph.NewStateGraph(schema)

	// start nodes: runtime no-op
	sg.AddNode("start", func(ctx context.Context, state graph.State) (any, error) {
		return nil, nil
	})

	// Agent nodes backed by sub-agents.
	sg.AddAgentNode("classifier", graph.WithSubgraphOutputMapper(agentStructuredOutputMapper("classifier")))
	sg.AddAgentNode("information_agent")
	sg.AddAgentNode("retention_agent")
	sg.AddAgentNode("return_agent")

	// Approval nodes.
	sg.AddNode("retention_approval", nodeRetentionApproval)

	// End nodes.
	sg.AddNode("info_end", nodeInfoEnd)
	sg.AddNode("retention_end", nodeRetentionEnd)
	sg.AddNode("retention_reject_end", nodeRetentionRejectEnd)
	sg.AddNode("return_end", nodeReturnEnd)

	// Edges.
	sg.AddEdge("info_end", "__end__")
	sg.AddEdge("information_agent", "info_end")
	sg.AddEdge("retention_agent", "retention_approval")
	sg.AddEdge("retention_end", "__end__")
	sg.AddEdge("retention_reject_end", "__end__")
	sg.AddEdge("return_agent", "return_end")
	sg.AddEdge("return_end", "__end__")
	sg.AddEdge("start", "classifier")

	// Conditional edges.
	sg.AddConditionalEdges("classifier", routeEdgeRouteByClassification, nil)
	sg.AddConditionalEdges("retention_approval", routeEdgeRouteAfterRetentionApproval, nil)

	sg.SetEntryPoint("start")
	return sg.Compile()
}

// routeEdgeRouteByClassification routes based on input.output_parsed.classification.
func routeEdgeRouteByClassification(ctx context.Context, state graph.State) (string, error) {
	if v, ok := getNodeStructuredFieldString(state, "classifier", "classification"); ok {
		switch v {
		case "return_item":
			return "return_agent", nil
		case "cancel_subscription":
			return "retention_agent", nil
		case "get_information":
			return "information_agent", nil
		}
	}
	return "", fmt.Errorf("no matching case for conditional from %s (field=%s)", "classifier", "classification")
}

// routeEdgeRouteAfterRetentionApproval routes based on state field.
func routeEdgeRouteAfterRetentionApproval(ctx context.Context, state graph.State) (string, error) {
	v, _ := state["approval_result"].(string)
	switch v {
	case "approve":
		return "retention_end", nil
	case "reject":
		return "retention_reject_end", nil
	default:
		return "", fmt.Errorf("invalid value %q for state field approval_result", v)
	}
}

// nodeRetentionApproval is a user-approval node that interrupts execution
// and waits for a resume value, normalizing it into "approve" / "reject".
func nodeRetentionApproval(ctx context.Context, state graph.State) (any, error) {
	const nodeID = "retention_approval"

	payload := map[string]any{
		"message": "Does this retention offer work for you?",
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

// nodeInfoEnd writes the final structured output for info_end.
func nodeInfoEnd(ctx context.Context, state graph.State) (any, error) {
	last, _ := state[graph.StateKeyLastResponse].(string)
	if strings.TrimSpace(last) == "" {
		return nil, nil
	}
	return graph.State{
		"end_structured_output": map[string]any{
			"message": last,
		},
	}, nil
}

// nodeRetentionEnd writes the final structured output for retention_end.
func nodeRetentionEnd(ctx context.Context, state graph.State) (any, error) {
	return graph.State{
		"end_structured_output": map[string]any{
			"message": "Your retention offer has been accepted. Thank you for staying with us.",
		},
	}, nil
}

// nodeRetentionRejectEnd writes the final structured output for retention_reject_end.
func nodeRetentionRejectEnd(ctx context.Context, state graph.State) (any, error) {
	return graph.State{
		"end_structured_output": map[string]any{
			"message": "We understand your decision. If you change your mind, we are always here to help.",
		},
	}, nil
}

// nodeReturnEnd writes the final structured output for return_end.
func nodeReturnEnd(ctx context.Context, state graph.State) (any, error) {
	last, _ := state[graph.StateKeyLastResponse].(string)
	if strings.TrimSpace(last) == "" {
		return nil, nil
	}
	return graph.State{
		"end_structured_output": map[string]any{
			"message": last,
		},
	}, nil
}

// newClassifierSubAgent constructs the LLMAgent backing the "classifier" AgentNode.
func newClassifierSubAgent() agent.Agent {
	apiKey, err := resolveEnvString("env:OPENAI_API_KEY", "classifier.model_spec.api_key")
	if err != nil {
		panic(err)
	}

	var opts []openai.Option
	opts = append(opts, openai.WithAPIKey(apiKey))

	baseURL, err := resolveEnvString("https://api.deepseek.com/v1", "classifier.model_spec.base_url")
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}

	headers := map[string]string{}
	resolved := make(map[string]string, len(headers))
	for k, v := range headers {
		rv, err := resolveEnvString(v, fmt.Sprintf("classifier.model_spec.headers[%q]", k))
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

	extraFieldsJSON := ""
	if strings.TrimSpace(extraFieldsJSON) != "" {
		opts = append(opts, openai.WithExtraFields(mustParseJSONMap(extraFieldsJSON)))
	}

	modelName, err := resolveEnvString("deepseek-chat", "classifier.model_spec.model_name")
	if err != nil {
		panic(err)
	}
	llmModel := openai.New(modelName, opts...)

	instruction := `Classify the user’s intent into one of the following categories: "return_item", "cancel_subscription", or "get_information".

1. Any device-related return requests should route to return_item.
2. Any retention or cancellation risk, including any request for discounts should route to cancel_subscription.
3. Any other requests should go to get_information.`
	return llmagent.New(
		"classifier",
		llmagent.WithModel(llmModel),
		llmagent.WithInstruction(instruction),
		llmagent.WithStructuredOutputJSONSchema("schema_classifier", mustParseJSONMap(`{"properties":{"classification":{"description":"Classification of user intent","enum":["return_item","cancel_subscription","get_information"],"type":"string"}},"required":["classification"],"type":"object"}`), true, ""),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
	)
}

// newInformationAgentSubAgent constructs the LLMAgent backing the "information_agent" AgentNode.
func newInformationAgentSubAgent() agent.Agent {
	apiKey, err := resolveEnvString("env:OPENAI_API_KEY", "information_agent.model_spec.api_key")
	if err != nil {
		panic(err)
	}

	var opts []openai.Option
	opts = append(opts, openai.WithAPIKey(apiKey))

	baseURL, err := resolveEnvString("https://api.deepseek.com/v1", "information_agent.model_spec.base_url")
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}

	headers := map[string]string{}
	resolved := make(map[string]string, len(headers))
	for k, v := range headers {
		rv, err := resolveEnvString(v, fmt.Sprintf("information_agent.model_spec.headers[%q]", k))
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

	extraFieldsJSON := ""
	if strings.TrimSpace(extraFieldsJSON) != "" {
		opts = append(opts, openai.WithExtraFields(mustParseJSONMap(extraFieldsJSON)))
	}

	modelName, err := resolveEnvString("deepseek-chat", "information_agent.model_spec.model_name")
	if err != nil {
		panic(err)
	}
	llmModel := openai.New(modelName, opts...)

	instruction := `You are an information agent for answering informational queries. Your aim is to provide clear, concise responses to user questions. Use the following policy to assemble your answer.

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

Always respond in a friendly, concise way and surface the most relevant parts of the policy.`
	return llmagent.New(
		"information_agent",
		llmagent.WithModel(llmModel),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
	)
}

// newRetentionAgentSubAgent constructs the LLMAgent backing the "retention_agent" AgentNode.
func newRetentionAgentSubAgent() agent.Agent {
	apiKey, err := resolveEnvString("env:OPENAI_API_KEY", "retention_agent.model_spec.api_key")
	if err != nil {
		panic(err)
	}

	var opts []openai.Option
	opts = append(opts, openai.WithAPIKey(apiKey))

	baseURL, err := resolveEnvString("https://api.deepseek.com/v1", "retention_agent.model_spec.base_url")
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}

	headers := map[string]string{}
	resolved := make(map[string]string, len(headers))
	for k, v := range headers {
		rv, err := resolveEnvString(v, fmt.Sprintf("retention_agent.model_spec.headers[%q]", k))
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

	extraFieldsJSON := ""
	if strings.TrimSpace(extraFieldsJSON) != "" {
		opts = append(opts, openai.WithExtraFields(mustParseJSONMap(extraFieldsJSON)))
	}

	modelName, err := resolveEnvString("deepseek-chat", "retention_agent.model_spec.model_name")
	if err != nil {
		panic(err)
	}
	llmModel := openai.New(modelName, opts...)

	instruction := `You are a customer retention conversational agent whose goal is to prevent subscription cancellations. Ask for their current plan and reason for dissatisfaction. For now, you may simply say there is a 20% offer available for 1 year if that seems appropriate.`
	return llmagent.New(
		"retention_agent",
		llmagent.WithModel(llmModel),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
	)
}

// newReturnAgentSubAgent constructs the LLMAgent backing the "return_agent" AgentNode.
func newReturnAgentSubAgent() agent.Agent {
	apiKey, err := resolveEnvString("env:OPENAI_API_KEY", "return_agent.model_spec.api_key")
	if err != nil {
		panic(err)
	}

	var opts []openai.Option
	opts = append(opts, openai.WithAPIKey(apiKey))

	baseURL, err := resolveEnvString("https://api.deepseek.com/v1", "return_agent.model_spec.base_url")
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}

	headers := map[string]string{}
	resolved := make(map[string]string, len(headers))
	for k, v := range headers {
		rv, err := resolveEnvString(v, fmt.Sprintf("return_agent.model_spec.headers[%q]", k))
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

	extraFieldsJSON := ""
	if strings.TrimSpace(extraFieldsJSON) != "" {
		opts = append(opts, openai.WithExtraFields(mustParseJSONMap(extraFieldsJSON)))
	}

	modelName, err := resolveEnvString("deepseek-chat", "return_agent.model_spec.model_name")
	if err != nil {
		panic(err)
	}
	llmModel := openai.New(modelName, opts...)

	instruction := `Offer a replacement device with free shipping.`
	return llmagent.New(
		"return_agent",
		llmagent.WithModel(llmModel),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: true}),
	)
}

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
