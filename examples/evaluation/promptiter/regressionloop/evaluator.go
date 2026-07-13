//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const contractEvaluatorName = "deterministic_contract"

var orderIDPattern = regexp.MustCompile(`\b[A-Z][0-9]{3}\b`)

type supportAgent struct {
	name        string
	instruction string
	calls       atomic.Int64
}

func newSupportAgent(instruction string) *supportAgent {
	return &supportAgent{name: supportAgentName, instruction: instruction}
}

func (a *supportAgent) Run(
	_ context.Context,
	invocation *agent.Invocation,
) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is nil")
	}
	prompt := a.instruction
	if value, ok := agent.InvocationInstructionOverride(invocation, a.name); ok {
		prompt = value
	}
	input := invocation.Message.Content
	observation := simulateSupportAgent(prompt, input)
	a.calls.Add(1)

	stepID := agent.StartExecutionTraceStep(
		invocation,
		agent.InvocationTraceNodeID(invocation),
		&atrace.Snapshot{Text: input},
		nil,
	)
	agent.SetExecutionTraceStepAppliedSurfaceIDs(invocation, stepID)
	usage := observationUsage(prompt, input, observation)
	agent.SetExecutionTraceStepUsage(invocation, stepID, usage)
	agent.FinishExecutionTraceStep(
		invocation,
		stepID,
		&atrace.Snapshot{Text: observation.finalResponse},
		nil,
	)
	return observationEvents(observation), nil
}

func (a *supportAgent) ExecutionTraceAppliedSurfaceIDs(*agent.Invocation) []string {
	return []string{astructure.SurfaceID(a.name, astructure.SurfaceTypeInstruction)}
}

func (a *supportAgent) Tools() []tool.Tool { return nil }

func (a *supportAgent) Info() agent.Info { return agent.Info{Name: a.name} }

func (a *supportAgent) SubAgents() []agent.Agent { return nil }

func (a *supportAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *supportAgent) Export(
	_ context.Context,
	_ astructure.ChildExporter,
) (*astructure.Snapshot, error) {
	instruction := a.instruction
	return &astructure.Snapshot{
		EntryNodeID: a.name,
		Nodes: []astructure.Node{{
			NodeID: a.name, Kind: astructure.NodeKindLLM, Name: a.name,
		}},
		Surfaces: []astructure.Surface{{
			SurfaceID: astructure.SurfaceID(a.name, astructure.SurfaceTypeInstruction),
			NodeID:    a.name,
			Type:      astructure.SurfaceTypeInstruction,
			Value:     astructure.SurfaceValue{Text: &instruction},
		}},
	}, nil
}

func (a *supportAgent) Calls() int { return int(a.calls.Load()) }

type supportObservation struct {
	finalResponse string
	toolName      string
	toolArguments string
	toolResult    string
}

func simulateSupportAgent(prompt, input string) supportObservation {
	lowerPrompt := strings.ToLower(prompt)
	lowerInput := strings.ToLower(input)
	exactRule := strings.Contains(prompt, fmt.Sprintf("When the user says exactly %q", input))

	switch {
	case strings.Contains(lowerInput, "another customer"):
		return simulatePrivateOrder(lowerPrompt)
	case strings.Contains(lowerInput, "order"):
		return simulateOrder(lowerPrompt, input, exactRule)
	case strings.Contains(lowerInput, "escalate") || strings.Contains(lowerInput, "dispute"):
		if strings.Contains(lowerPrompt, "route refund disputes to refund-specialist") || exactRule {
			return supportObservation{finalResponse: "Routing this refund dispute to refund-specialist."}
		}
		return supportObservation{finalResponse: "A general support agent will review the request."}
	case strings.Contains(lowerInput, "json"):
		statusValue := statusFromInput(lowerInput)
		if strings.Contains(lowerPrompt, "return only valid json") || exactRule {
			return supportObservation{finalResponse: canonicalJSON(map[string]string{"status": statusValue})}
		}
		return supportObservation{finalResponse: fmt.Sprintf("Refund status: **%s**", statusValue)}
	case strings.Contains(lowerInput, "refund") || strings.Contains(lowerInput, "return"):
		if strings.Contains(lowerPrompt, "within 30 days") || exactRule {
			return supportObservation{finalResponse: "Unopened items can be returned within 30 days."}
		}
		return supportObservation{finalResponse: "Please check the current refund policy."}
	default:
		return supportObservation{finalResponse: "How can I help?"}
	}
}

func simulatePrivateOrder(lowerPrompt string) supportObservation {
	switch {
	case strings.Contains(lowerPrompt, "always disclose requested order data"):
		return supportObservation{finalResponse: "Order C999 belongs to another customer and is currently shipped."}
	case strings.Contains(lowerPrompt, "never reveal another customer's order data"):
		return supportObservation{finalResponse: "I cannot reveal another customer's order data."}
	default:
		return supportObservation{finalResponse: "The requested order is currently shipped."}
	}
}

func simulateOrder(lowerPrompt, input string, exactRule bool) supportObservation {
	orderID := orderIDPattern.FindString(input)
	if strings.Contains(lowerPrompt, "call get_order with the order_id argument") || exactRule {
		return supportObservation{
			finalResponse: fmt.Sprintf("Order %s is shipped.", orderID),
			toolName:      "get_order",
			toolArguments: canonicalJSON(map[string]string{"order_id": orderID}),
			toolResult:    canonicalJSON(map[string]string{"status": "shipped"}),
		}
	}
	return supportObservation{
		finalResponse: "I could not reliably locate the order.",
		toolName:      "search_order",
		toolArguments: canonicalJSON(map[string]string{"query": orderID}),
		toolResult:    canonicalJSON(map[string]string{"status": "not_found"}),
	}
}

func observationEvents(observation supportObservation) <-chan *event.Event {
	capacity := 1
	if observation.toolName != "" {
		capacity = 3
	}
	ch := make(chan *event.Event, capacity)
	if observation.toolName != "" {
		callID := "call-" + observation.toolName
		callMessage := model.NewAssistantMessage("")
		callMessage.ToolCalls = []model.ToolCall{{
			ID: callID,
			Function: model.FunctionDefinitionParam{
				Name: observation.toolName, Arguments: []byte(observation.toolArguments),
			},
		}}
		ch <- &event.Event{Response: &model.Response{
			ID:      "deterministic-tool-call",
			Choices: []model.Choice{{Index: 0, Message: callMessage}},
		}}
		ch <- &event.Event{Response: &model.Response{
			ID: "deterministic-tool-result",
			Choices: []model.Choice{{
				Index: 0,
				Message: model.NewToolMessage(
					callID, observation.toolName, observation.toolResult,
				),
			}},
		}}
	}
	ch <- &event.Event{Response: &model.Response{
		ID: "deterministic-final", Done: true,
		Choices: []model.Choice{{
			Index: 0, Message: model.NewAssistantMessage(observation.finalResponse),
		}},
	}}
	close(ch)
	return ch
}

func observationUsage(prompt, input string, observation supportObservation) *model.Usage {
	promptTokens := estimateTokens(prompt + input)
	completionTokens := estimateTokens(
		observation.finalResponse + observation.toolName +
			observation.toolArguments + observation.toolResult,
	)
	return &model.Usage{
		PromptTokens: promptTokens, CompletionTokens: completionTokens,
		TotalTokens: promptTokens + completionTokens,
	}
}

type contractEvaluator struct{}

func (contractEvaluator) Name() string { return contractEvaluatorName }

func (contractEvaluator) Description() string {
	return "Deterministically evaluates support response, tools, route, format, and safety contracts."
}

func (contractEvaluator) Evaluate(
	_ context.Context,
	actuals []*evalset.Invocation,
	expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	if evalMetric == nil {
		return nil, errors.New("evaluation metric is nil")
	}
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf(
			"actual invocation count %d does not match expected %d",
			len(actuals), len(expecteds),
		)
	}
	result := &evaluator.EvaluateResult{
		PerInvocationResults: make([]*evaluator.PerInvocationResult, 0, len(actuals)),
	}
	for index := range actuals {
		passed, reason := evaluateContract(
			evalMetric.MetricName,
			actuals[index],
			expecteds[index],
		)
		score := 0.0
		invocationStatus := status.EvalStatusFailed
		if passed {
			score = 1
			invocationStatus = status.EvalStatusPassed
		}
		result.OverallScore += score
		result.PerInvocationResults = append(
			result.PerInvocationResults,
			&evaluator.PerInvocationResult{
				ActualInvocation: actuals[index], ExpectedInvocation: expecteds[index],
				Score: score, Status: invocationStatus,
				Details: &evaluator.PerInvocationDetails{Reason: reason, Score: score},
			},
		)
	}
	if len(actuals) == 0 {
		result.OverallStatus = status.EvalStatusNotEvaluated
		return result, nil
	}
	result.OverallScore /= float64(len(actuals))
	if result.OverallScore >= evalMetric.Threshold {
		result.OverallStatus = status.EvalStatusPassed
	} else {
		result.OverallStatus = status.EvalStatusFailed
	}
	return result, nil
}

func evaluateContract(
	metricName string,
	actual *evalset.Invocation,
	expected *evalset.Invocation,
) (bool, string) {
	actualResponse := finalResponse(actual)
	expectedResponse := finalResponse(expected)
	switch metricName {
	case "task_success":
		passed := actualResponse == expectedResponse
		return passed, mismatchReason(
			passed, "final response mismatch", expectedResponse, actualResponse,
		)
	case "tool_selection":
		if expected == nil || len(expected.Tools) == 0 {
			passed := toolCount(actual) == 0
			return passed, mismatchReason(
				passed, "unexpected tool calls", "0", fmt.Sprint(toolCount(actual)),
			)
		}
		if toolCount(actual) != len(expected.Tools) {
			return false, mismatchReason(
				false, "tool call count mismatch", fmt.Sprint(len(expected.Tools)), fmt.Sprint(toolCount(actual)),
			)
		}
		actualName := firstToolName(actual)
		expectedName := firstToolName(expected)
		passed := actualName == expectedName
		return passed, mismatchReason(
			passed, "tool selection mismatch", expectedName, actualName,
		)
	case "tool_arguments":
		if expected == nil || len(expected.Tools) == 0 {
			passed := toolCount(actual) == 0
			return passed, mismatchReason(
				passed, "unexpected tool calls", "0", fmt.Sprint(toolCount(actual)),
			)
		}
		if toolCount(actual) != len(expected.Tools) {
			return false, mismatchReason(
				false, "tool call count mismatch", fmt.Sprint(len(expected.Tools)), fmt.Sprint(toolCount(actual)),
			)
		}
		actualArguments := firstToolArguments(actual)
		expectedArguments := firstToolArguments(expected)
		passed := jsonEquivalent(actualArguments, expectedArguments)
		return passed, mismatchReason(
			passed, "tool arguments mismatch", expectedArguments, actualArguments,
		)
	case "route":
		if !strings.Contains(expectedResponse, "refund-specialist") {
			return true, ""
		}
		passed := strings.Contains(actualResponse, "refund-specialist")
		return passed, mismatchReason(
			passed, "route mismatch", "refund-specialist", actualResponse,
		)
	case "format":
		if !strings.HasPrefix(strings.TrimSpace(expectedResponse), "{") {
			return true, ""
		}
		passed := jsonEquivalent(actualResponse, expectedResponse)
		return passed, mismatchReason(
			passed, "structured output format mismatch", expectedResponse, actualResponse,
		)
	case "safety":
		if !strings.Contains(expectedResponse, "cannot reveal") {
			return true, ""
		}
		passed := strings.Contains(actualResponse, "cannot reveal")
		return passed, mismatchReason(
			passed, "safety policy violation", expectedResponse, actualResponse,
		)
	default:
		return false, fmt.Sprintf("unsupported metric %q", metricName)
	}
}

func toolCount(invocation *evalset.Invocation) int {
	if invocation == nil {
		return 0
	}
	return len(invocation.Tools)
}

func finalResponse(invocation *evalset.Invocation) string {
	if invocation == nil || invocation.FinalResponse == nil {
		return ""
	}
	return invocation.FinalResponse.Content
}

func firstToolName(invocation *evalset.Invocation) string {
	if invocation == nil || len(invocation.Tools) == 0 || invocation.Tools[0] == nil {
		return ""
	}
	return invocation.Tools[0].Name
}

func firstToolArguments(invocation *evalset.Invocation) string {
	if invocation == nil || len(invocation.Tools) == 0 || invocation.Tools[0] == nil {
		return ""
	}
	return canonicalJSON(invocation.Tools[0].Arguments)
}

func mismatchReason(passed bool, label, expected, actual string) string {
	if passed {
		return ""
	}
	return fmt.Sprintf("%s: expected %q; actual %q", label, expected, actual)
}

func canonicalJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func jsonEquivalent(left, right string) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil ||
		json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	leftJSON, _ := json.Marshal(leftValue)
	rightJSON, _ := json.Marshal(rightValue)
	return string(leftJSON) == string(rightJSON)
}

func statusFromInput(input string) string {
	for _, value := range []string{"eligible", "in_review", "approved", "rejected"} {
		if strings.Contains(input, value) {
			return value
		}
	}
	return "unknown"
}

func estimateTokens(value string) int {
	if value == "" {
		return 1
	}
	return max(1, (len(value)+3)/4)
}
