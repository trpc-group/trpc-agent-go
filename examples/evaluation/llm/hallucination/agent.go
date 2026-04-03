//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	productCatalogToolName = "product_catalog_lookup"
	defaultAgentName       = "hallucination-agent"
	judgeAgentName         = "hallucination-judge"
	productIDAuroraPadX2   = "aurora-pad-x2"
	productIDTerraWatchS   = "terra-watch-s"
	scriptedCandidateModel = "scripted-hallucination"
	productCatalogToolCall = "call_product_catalog_lookup"
	hallucinatedAnswer     = "AuroraPad X2 was released in 2025. It offers 24 hours of battery life. It is designed for classroom teachers."
)

func newQAAgent(modelName string, stream bool) agent.Agent {
	catalogTool := newProductCatalogTool()
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.0),
		Stream:      stream,
	}
	return llmagent.New(
		defaultAgentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithTools([]tool.Tool{catalogTool}),
		llmagent.WithInstruction("Use the product_catalog_lookup tool before answering any catalog question. Answer only with facts returned by the tool. If the requested field is missing, say you do not know."),
		llmagent.WithDescription("Simple LLM agent for hallucination evaluation with tool-grounded facts."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func newJudgeAgent(modelName string) agent.Agent {
	genCfg := model.GenerationConfig{
		MaxTokens:   intPtr(1024),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		judgeAgentName,
		llmagent.WithModel(openai.New(modelName)),
		llmagent.WithInstruction("Follow the provided evaluation instructions exactly and return only the requested judge output."),
		llmagent.WithDescription("Judge agent used by the hallucination evaluation example."),
		llmagent.WithGenerationConfig(genCfg),
	)
}

func newForcedHallucinationAgent() agent.Agent {
	return &scriptedHallucinationAgent{
		tools: []tool.Tool{newProductCatalogTool()},
	}
}

func newProductCatalogTool() tool.Tool {
	return function.NewFunctionTool(
		lookupProductCatalog,
		function.WithName(productCatalogToolName),
		function.WithDescription("Look up a fictional product catalog entry by product_id."),
		function.WithInputSchema(productCatalogInputSchema()),
	)
}

type productCatalogArgs struct {
	ProductID string `json:"product_id"`
}

type productCatalogResult struct {
	ProductID        string `json:"product_id"`
	Name             string `json:"name"`
	ReleaseYear      int    `json:"release_year"`
	BatteryLifeHours int    `json:"battery_life_hours"`
	MarketSegment    string `json:"market_segment"`
	Connectivity     string `json:"connectivity"`
}

type scriptedHallucinationAgent struct {
	tools []tool.Tool
}

func (a *scriptedHallucinationAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, fmt.Errorf("invocation is nil")
	}
	result, err := lookupProductCatalog(ctx, productCatalogArgs{ProductID: productIDAuroraPadX2})
	if err != nil {
		return nil, err
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshal product catalog result: %w", err)
	}
	ch := make(chan *event.Event, 3)
	toolCallResponse := &model.Response{
		ID:    "scripted-tool-call",
		Model: scriptedCandidateModel,
		Done:  true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   productCatalogToolCall,
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      productCatalogToolName,
						Arguments: []byte(`{"product_id":"aurora-pad-x2"}`),
					},
				}},
			},
		}},
	}
	if err := agent.EmitEvent(ctx, invocation, ch, event.NewResponseEvent(invocation.InvocationID, defaultAgentName, toolCallResponse)); err != nil {
		return nil, err
	}
	toolResultResponse := &model.Response{
		ID:    "scripted-tool-result",
		Model: scriptedCandidateModel,
		Done:  false,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:     model.RoleTool,
				ToolID:   productCatalogToolCall,
				ToolName: productCatalogToolName,
				Content:  string(resultJSON),
			},
		}},
	}
	if err := agent.EmitEvent(ctx, invocation, ch, event.NewResponseEvent(invocation.InvocationID, productCatalogToolName, toolResultResponse)); err != nil {
		return nil, err
	}
	finalResponse := &model.Response{
		ID:    "scripted-final-response",
		Model: scriptedCandidateModel,
		Done:  true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(hallucinatedAnswer),
		}},
	}
	if err := agent.EmitEvent(ctx, invocation, ch, event.NewResponseEvent(invocation.InvocationID, defaultAgentName, finalResponse)); err != nil {
		return nil, err
	}
	close(ch)
	return ch, nil
}

func (a *scriptedHallucinationAgent) Tools() []tool.Tool {
	return a.tools
}

func (a *scriptedHallucinationAgent) Info() agent.Info {
	return agent.Info{
		Name:        defaultAgentName,
		Description: "Scripted candidate agent used to validate hallucination failures.",
	}
}

func (a *scriptedHallucinationAgent) SubAgents() []agent.Agent {
	return nil
}

func (a *scriptedHallucinationAgent) FindSubAgent(string) agent.Agent {
	return nil
}

func lookupProductCatalog(_ context.Context, args productCatalogArgs) (productCatalogResult, error) {
	catalog := map[string]productCatalogResult{
		productIDAuroraPadX2: {
			ProductID:        productIDAuroraPadX2,
			Name:             "AuroraPad X2",
			ReleaseYear:      2024,
			BatteryLifeHours: 18,
			MarketSegment:    "field operations teams",
			Connectivity:     "wifi-6 and 5g",
		},
		productIDTerraWatchS: {
			ProductID:        productIDTerraWatchS,
			Name:             "TerraWatch S",
			ReleaseYear:      2023,
			BatteryLifeHours: 9,
			MarketSegment:    "warehouse supervisors",
			Connectivity:     "bluetooth-le and lte-m",
		},
	}
	result, ok := catalog[args.ProductID]
	if !ok {
		return productCatalogResult{}, fmt.Errorf("product %q not found in the catalog", args.ProductID)
	}
	return result, nil
}

func productCatalogInputSchema() *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: "Product catalog lookup input.",
		Properties: map[string]*tool.Schema{
			"product_id": {
				Type:        "string",
				Description: "The product identifier to look up.",
				Enum:        []any{productIDAuroraPadX2, productIDTerraWatchS},
			},
		},
		Required: []string{"product_id"},
	}
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}
