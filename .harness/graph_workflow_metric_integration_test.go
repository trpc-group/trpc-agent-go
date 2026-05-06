//go:build integration

//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
	semconvmetrics "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	runRealLLMGraphMetricEnv = "TRPC_AGENT_GO_RUN_REAL_LLM_GRAPH_METRIC"
	graphMetricHarnessApp    = "harness-graph-workflow-metric"
	graphMetricHarnessUser   = "harness-user"
	graphMetricHarnessNodeID = "llm"
)

func TestRealLLMGraphWorkflowMetric(t *testing.T) {
	if os.Getenv(runRealLLMGraphMetricEnv) != "1" {
		t.Skipf("set %s=1 to run real LLM graph workflow metric integration test", runRealLLMGraphMetricEnv)
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY is required for real LLM integration test")
	}

	modelName := firstNonEmptyEnv("MODEL_NAME", "OPENAI_MODEL")
	if modelName == "" {
		modelName = "gpt-4o-mini"
	}

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if err := ametric.InitMeterProvider(provider); err != nil {
		t.Fatalf("init meter provider: %v", err)
	}
	defer func() {
		_ = provider.Shutdown(context.Background())
	}()

	llm := openai.New(modelName)
	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddLLMNode(
		graphMetricHarnessNodeID,
		llm,
		"Reply with exactly: graph metric ok",
		nil,
		graph.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(32),
			Temperature: floatPtr(0),
			Stream:      false,
		}),
	)
	sg.SetEntryPoint(graphMetricHarnessNodeID)
	sg.SetFinishPoint(graphMetricHarnessNodeID)

	compiled, err := sg.Compile()
	if err != nil {
		t.Fatalf("compile graph: %v", err)
	}
	exec, err := graph.NewExecutor(compiled)
	if err != nil {
		t.Fatalf("new executor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	invocation := agent.NewInvocation(
		agent.WithInvocationID(fmt.Sprintf("harness-graph-workflow-metric-%d", time.Now().UnixNano())),
		agent.WithInvocationModel(llm),
		agent.WithInvocationSession(&session.Session{
			ID:      fmt.Sprintf("harness-session-%d", time.Now().UnixNano()),
			AppName: graphMetricHarnessApp,
			UserID:  graphMetricHarnessUser,
		}),
	)
	events, err := exec.Execute(
		ctx,
		graph.State{graph.StateKeyUserInput: "Reply with exactly: graph metric ok"},
		invocation,
	)
	if err != nil {
		t.Fatalf("execute graph: %v", err)
	}
	for event := range events {
		if event.Error != nil {
			t.Fatalf("graph event error: %s", event.Error.Message)
		}
	}

	point, ok := collectWorkflowMetricPoint(t, reader)
	if !ok {
		t.Fatalf("workflow metric datapoint not found")
	}
	if point.Count != 1 {
		t.Fatalf("workflow metric count = %d, want 1", point.Count)
	}
	assertAttr(t, point.Attributes, semconvtrace.KeyGenAIOperationName, "workflow")
	assertAttr(t, point.Attributes, semconvtrace.KeyGenAIAppName, graphMetricHarnessApp)
	assertAttr(t, point.Attributes, semconvtrace.KeyGenAIUserID, graphMetricHarnessUser)
	assertAttr(t, point.Attributes, semconvtrace.KeyGenAIAgentID, "")
	assertAttr(t, point.Attributes, semconvtrace.KeyGenAISystem, modelName)
	assertAttr(t, point.Attributes, semconvtrace.KeyGenAIWorkflowID, graphMetricHarnessNodeID)
	assertAttr(t, point.Attributes, semconvtrace.KeyGenAIWorkflowName, graphMetricHarnessNodeID)
	assertAttr(t, point.Attributes, semconvtrace.KeyGenAIWorkflowType, "llm")
	assertNoAttr(t, point.Attributes, semconvtrace.KeyErrorType)
}

func TestRunnerGraphAgentWorkflowTypesMetric(t *testing.T) {
	reader := newHarnessMetricReader(t)

	const (
		graphAgentName = "harness-workflow-types-graph-agent"
		routerNode     = "router_node"
		functionNode   = "function_node"
		llmNode        = "llm_node"
		prepareTool    = "prepare_tool"
		toolNode       = "tool_node"
		agentNode      = "agent_node"
		branchA        = "branch_a"
		branchB        = "branch_b"
		joinNode       = "join_node"
	)

	sg := graph.NewStateGraph(graph.MessagesStateSchema())
	sg.AddNode(routerNode, func(ctx context.Context, state graph.State) (any, error) {
		return nil, nil
	}, graph.WithNodeType(graph.NodeTypeRouter))
	sg.AddNode(functionNode, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyMetadata: map[string]any{"function": true}}, nil
	})
	sg.AddLLMNode(
		llmNode,
		&harnessModel{name: "harness-fake-llm"},
		"Return a short deterministic response.",
		nil,
		graph.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(16),
			Temperature: floatPtr(0),
			Stream:      false,
		}),
	)
	sg.AddNode(prepareTool, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{
			graph.StateKeyMessages: []model.Message{
				{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{
						{
							Type: "function",
							ID:   "call-echo",
							Function: model.FunctionDefinitionParam{
								Name:      "echo",
								Arguments: []byte(`{"text":"tool metric ok"}`),
							},
						},
					},
				},
			},
		}, nil
	})
	sg.AddToolsNode(toolNode, map[string]tool.Tool{"echo": &harnessEchoTool{}})
	sg.AddAgentNode(agentNode)
	sg.AddNode(branchA, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyMetadata: map[string]any{"branch_a": true}}, nil
	})
	sg.AddNode(branchB, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyMetadata: map[string]any{"branch_b": true}}, nil
	})
	sg.AddNode(joinNode, func(ctx context.Context, state graph.State) (any, error) {
		return graph.State{graph.StateKeyLastResponse: "workflow types ok"}, nil
	}, graph.WithNodeType(graph.NodeTypeJoin))

	sg.SetEntryPoint(routerNode)
	sg.AddConditionalEdges(routerNode, func(ctx context.Context, state graph.State) (string, error) {
		return "function", nil
	}, map[string]string{"function": functionNode})
	sg.AddEdge(functionNode, llmNode)
	sg.AddEdge(llmNode, prepareTool)
	sg.AddEdge(prepareTool, toolNode)
	sg.AddEdge(toolNode, agentNode)
	sg.AddMultiConditionalEdges(agentNode, func(ctx context.Context, state graph.State) ([]string, error) {
		return []string{"a", "b"}, nil
	}, map[string]string{
		"a": branchA,
		"b": branchB,
	})
	sg.AddJoinEdge([]string{branchA, branchB}, joinNode)
	sg.SetFinishPoint(joinNode)

	compiled, err := sg.Compile()
	if err != nil {
		t.Fatalf("compile graph: %v", err)
	}
	graphAgent, err := graphagent.New(
		graphAgentName,
		compiled,
		graphagent.WithSubAgents([]agent.Agent{&harnessAgent{name: agentNode}}),
	)
	if err != nil {
		t.Fatalf("new graph agent: %v", err)
	}
	r := runner.NewRunner(graphMetricHarnessApp, graphAgent)
	defer func() {
		_ = r.Close()
	}()

	events, err := r.Run(
		context.Background(),
		graphMetricHarnessUser,
		fmt.Sprintf("harness-workflow-types-%d", time.Now().UnixNano()),
		model.NewUserMessage("run workflow type metric harness"),
	)
	if err != nil {
		t.Fatalf("runner run: %v", err)
	}
	for evt := range events {
		if evt.Error != nil {
			t.Fatalf("runner event error: %s", evt.Error.Message)
		}
	}

	points := collectWorkflowMetricPoints(t, reader)
	expectedNodes := map[string]string{
		routerNode:   "router",
		functionNode: "function",
		llmNode:      "llm",
		toolNode:     "tool",
		agentNode:    "agent",
		joinNode:     "join",
	}
	for nodeID, workflowType := range expectedNodes {
		point, ok := pointForWorkflowNode(points, nodeID)
		if !ok {
			t.Fatalf("workflow metric datapoint for node %q not found", nodeID)
		}
		assertAttr(t, point.Attributes, semconvtrace.KeyGenAIOperationName, "workflow")
		assertAttr(t, point.Attributes, semconvtrace.KeyGenAIWorkflowID, nodeID)
		assertAttr(t, point.Attributes, semconvtrace.KeyGenAIWorkflowType, workflowType)
		assertAttr(t, point.Attributes, semconvtrace.KeyGenAIAppName, graphMetricHarnessApp)
		assertAttr(t, point.Attributes, semconvtrace.KeyGenAIUserID, graphMetricHarnessUser)
		assertNoAttr(t, point.Attributes, semconvtrace.KeyErrorType)
	}
	llmPoint, _ := pointForWorkflowNode(points, llmNode)
	assertAttr(t, llmPoint.Attributes, semconvtrace.KeyGenAISystem, "harness-fake-llm")
}

func collectWorkflowMetricPoint(
	t *testing.T,
	reader *sdkmetric.ManualReader,
) (metricdata.HistogramDataPoint[float64], bool) {
	t.Helper()

	points := collectWorkflowMetricPoints(t, reader)
	for _, point := range points {
		if attrMatches(point.Attributes, semconvtrace.KeyGenAIWorkflowID, graphMetricHarnessNodeID) &&
			attrMatches(point.Attributes, semconvtrace.KeyGenAIOperationName, "workflow") {
			return point, true
		}
	}
	return metricdata.HistogramDataPoint[float64]{}, false
}

func collectWorkflowMetricPoints(
	t *testing.T,
	reader *sdkmetric.ManualReader,
) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	var points []metricdata.HistogramDataPoint[float64]
	for _, scopeMetric := range rm.ScopeMetrics {
		if scopeMetric.Scope.Name != semconvmetrics.MeterNameWorkflow {
			continue
		}
		for _, metric := range scopeMetric.Metrics {
			if metric.Name != semconvmetrics.MetricGenAIClientOperationDuration {
				continue
			}
			if metric.Unit != "s" {
				t.Fatalf("metric unit = %q, want %q", metric.Unit, "s")
			}
			histogram, ok := metric.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("metric data type = %T, want metricdata.Histogram[float64]", metric.Data)
			}
			points = append(points, histogram.DataPoints...)
		}
	}
	return points
}

func pointForWorkflowNode(
	points []metricdata.HistogramDataPoint[float64],
	nodeID string,
) (metricdata.HistogramDataPoint[float64], bool) {
	for _, point := range points {
		if attrMatches(point.Attributes, semconvtrace.KeyGenAIWorkflowID, nodeID) &&
			attrMatches(point.Attributes, semconvtrace.KeyGenAIOperationName, "workflow") {
			return point, true
		}
	}
	return metricdata.HistogramDataPoint[float64]{}, false
}

func assertAttr(t *testing.T, attrs attribute.Set, key string, want string) {
	t.Helper()
	for _, attr := range attrs.ToSlice() {
		if string(attr.Key) == key {
			if got := attr.Value.AsString(); got != want {
				t.Fatalf("attribute %s = %q, want %q", key, got, want)
			}
			return
		}
	}
	t.Fatalf("attribute %s not found", key)
}

func assertNoAttr(t *testing.T, attrs attribute.Set, key string) {
	t.Helper()
	for _, attr := range attrs.ToSlice() {
		if string(attr.Key) == key {
			t.Fatalf("attribute %s unexpectedly found with value %q", key, attr.Value.AsString())
		}
	}
}

func attrMatches(attrs attribute.Set, key string, value string) bool {
	for _, attr := range attrs.ToSlice() {
		if string(attr.Key) == key && attr.Value.AsString() == value {
			return true
		}
	}
	return false
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}
	return ""
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

func newHarnessMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	if err := ametric.InitMeterProvider(provider); err != nil {
		t.Fatalf("init meter provider: %v", err)
	}
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})
	return reader
}

type harnessModel struct {
	name string
}

func (m *harnessModel) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{
		ID:      "harness-model-response",
		Object:  model.ObjectTypeChatCompletion,
		Model:   m.name,
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "llm metric ok"}}},
		Done:    true,
	}
	close(ch)
	return ch, nil
}

func (m *harnessModel) Info() model.Info {
	return model.Info{Name: m.name}
}

type harnessEchoTool struct{}

func (t *harnessEchoTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "echo",
		Description: "echoes a text argument",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"text": {Type: "string"},
			},
		},
	}
}

func (t *harnessEchoTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return map[string]string{"echo": string(jsonArgs)}, nil
}

type harnessAgent struct {
	name string
}

func (a *harnessAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- event.NewResponseEvent(invocation.InvocationID, a.name, &model.Response{
		ID:      "harness-agent-response",
		Object:  model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "agent metric ok"}}},
		Done:    true,
	})
	close(ch)
	return ch, nil
}

func (a *harnessAgent) Tools() []tool.Tool {
	return nil
}

func (a *harnessAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *harnessAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *harnessAgent) FindSubAgent(name string) agent.Agent {
	if name == a.name {
		return a
	}
	return nil
}
