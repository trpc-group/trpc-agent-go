//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metricinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func main() {
	ctx := context.Background()
	agent := newMockAgent()
	evalSetManager := evalsetinmemory.New()
	metricManager := metricinmemory.New()
	const evalSetID = "demo_tool_evalset"
	if _, err := evalSetManager.Create(ctx, agent.Info().Name, evalSetID); err != nil {
		log.Fatalf("create eval set: %v", err)
	}
	if err := evalSetManager.AddCase(ctx, agent.Info().Name, evalSetID, buildDemoEvalCase(agent.Info().Name)); err != nil {
		log.Fatalf("add eval case: %v", err)
	}
	metrics := []*metric.EvalMetric{
		{
			MetricName: "tool_trajectory_avg_score",
			Threshold:  1.0,
		},
	}
	if err := metricManager.Save(ctx, agent.Info().Name, evalSetID, metrics); err != nil {
		log.Fatalf("save metrics: %v", err)
	}

	evaluator, err := evaluation.NewAgentEvaluator(
		agent,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
	)
	if err != nil {
		log.Fatalf("create agent evaluator: %v", err)
	}

	result, err := evaluator.Evaluate(ctx, evalSetID)
	if err != nil {
		log.Fatalf("evaluate: %v", err)
	}
	fmt.Printf("Evaluation summary for %s on set %s\n", result.AppName, result.EvalSetID)
	fmt.Printf("Overall status: %s (took %s)\n\n", result.OverallStatus, result.ExecutionTime)
	for _, caseResult := range result.EvalCases {
		fmt.Printf("Case %s -> %s\n", caseResult.EvalCaseID, caseResult.OverallStatus)
		for _, metricResult := range caseResult.MetricResults {
			fmt.Printf(
				"  Metric %s: score %.2f (threshold %.2f) -> %s\n",
				metricResult.MetricName,
				metricResult.Score,
				metricResult.Threshold,
				metricResult.Status,
			)
		}
		fmt.Println()
	}
}

type mockAgent struct {
	info   agent.Info
	script map[string]mockStep
}

type mockStep struct {
	final string
	tool  *toolCallSpec
}

type toolCallSpec struct {
	name string
	args map[string]any
}

type scenario struct {
	prompt string
	final  string
	tool   *toolCallSpec
}

func newMockAgent() *mockAgent {
	steps := make(map[string]mockStep)
	for _, scene := range demoScenarios() {
		steps[scene.prompt] = mockStep{final: scene.final, tool: scene.tool}
	}
	return &mockAgent{
		info: agent.Info{
			Name:        "demo-eval-agent",
			Description: "agent agent that mirrors expected evaluation outputs",
		},
		script: steps,
	}
}

func (a *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, fmt.Errorf("invocation is nil")
	}
	step, ok := a.script[invocation.Message.Content]
	if !ok {
		return nil, fmt.Errorf("no agent response for %q", invocation.Message.Content)
	}
	ch := make(chan *event.Event, 2)
	go func() {
		defer close(ch)
		if step.tool != nil {
			payload, err := json.Marshal(step.tool.args)
			if err != nil {
				log.Printf("marshal tool args: %v", err)
				return
			}
			toolEvent := event.NewResponseEvent(invocation.InvocationID, a.info.Name, &model.Response{
				Object:    model.ObjectTypeChatCompletionChunk,
				Created:   time.Now().Unix(),
				Done:      false,
				IsPartial: false,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{
								{
									Type: "function",
									ID:   fmt.Sprintf("%s-call", step.tool.name),
									Function: model.FunctionDefinitionParam{
										Name:      step.tool.name,
										Arguments: payload,
									},
								},
							},
						},
					},
				},
			})
			if err := event.EmitEvent(ctx, ch, toolEvent); err != nil {
				log.Printf("emit tool event: %v", err)
				return
			}
		}
		finalEvent := event.NewResponseEvent(invocation.InvocationID, a.info.Name, &model.Response{
			Object:  model.ObjectTypeChatCompletion,
			Created: time.Now().Unix(),
			Done:    true,
			Choices: []model.Choice{
				{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: step.final,
					},
				},
			},
		})
		if err := event.EmitEvent(ctx, ch, finalEvent); err != nil {
			log.Printf("emit final event: %v", err)
		}
	}()
	return ch, nil
}

func (a *mockAgent) Tools() []tool.Tool              { return nil }
func (a *mockAgent) Info() agent.Info                { return a.info }
func (a *mockAgent) SubAgents() []agent.Agent        { return nil }
func (a *mockAgent) FindSubAgent(string) agent.Agent { return nil }

func demoScenarios() []scenario {
	return []scenario{
		{
			prompt: "What's the weather like in Seattle today?",
			final:  "Forecast for Seattle: 22°C and sunny with light breeze.",
			tool: &toolCallSpec{
				name: "lookup_weather",
				args: map[string]any{"location": "Seattle"},
			},
		},
		{
			prompt: "Great, should I pack sunscreen or an umbrella?",
			final:  "Conditions stay sunny all day—bring sunscreen, no umbrella needed.",
			tool: &toolCallSpec{
				name: "packing_recommendation",
				args: map[string]any{
					"conditions":     "sunny",
					"duration_hours": 8,
				},
			},
		},
	}
}

func buildDemoEvalCase(appName string) *evalset.EvalCase {
	scenarios := demoScenarios()
	invocations := make([]*evalset.Invocation, 0, len(scenarios))
	now := time.Now().UTC()

	for idx, scene := range scenarios {
		var toolUses []*evalset.FunctionCall
		if scene.tool != nil {
			toolUses = []*evalset.FunctionCall{
				{
					Name: scene.tool.name,
					Args: cloneArgs(scene.tool.args),
				},
			}
		}

		invocations = append(invocations, &evalset.Invocation{
			InvocationID: fmt.Sprintf("invocation-%d", idx+1),
			UserContent: &evalset.Content{
				Role:  model.RoleUser,
				Parts: []evalset.Part{{Text: scene.prompt}},
			},
			FinalResponse: &evalset.Content{
				Role:  model.RoleAssistant,
				Parts: []evalset.Part{{Text: scene.final}},
			},
			IntermediateData:  &evalset.IntermediateData{ToolUses: toolUses},
			CreationTimestamp: evalset.EpochTime{Time: now},
		})
	}

	return &evalset.EvalCase{
		EvalID:       "tool-trajectory-case",
		Conversation: invocations,
		SessionInput: &evalset.SessionInput{
			AppName: appName,
			UserID:  "demo-user",
			State: map[string]any{
				"region": "US",
			},
		},
		CreationTimestamp: evalset.EpochTime{Time: now},
	}
}

func cloneArgs(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
