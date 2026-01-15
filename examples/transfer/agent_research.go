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
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// createResearchAgent creates a specialized research agent.
func (c *transferChat) createResearchAgent(modelInstance model.Model) agent.Agent {
	// Search tool.
	searchTool := function.NewFunctionTool(
		c.search,
		function.WithName("search"),
		function.WithDescription("Search for information on a given topic"),
	)

	genConfig := model.GenerationConfig{
		MaxCompletionTokens: intPtr(3000),
		Temperature:         floatPtr(0.7),
		Stream:              true,
	}

	return llmagent.New(
		"research-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A specialized research and information gathering agent"),
		llmagent.WithInstruction("You are a research expert. Gather comprehensive information and provide well-structured answers."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{searchTool}),
	)
}

// search performs information search.
func (c *transferChat) search(_ context.Context, args searchArgs) (searchResult, error) {
	// Simulate search results based on query.
	query := strings.ToLower(args.Query)

	var results []string
	if strings.Contains(query, "renewable energy") {
		results = []string{
			"Renewable energy capacity increased by 295 GW in 2022",
			"Solar and wind power account for 90% of new renewable capacity",
			"Global investment in renewable energy reached $1.8 trillion",
			"Renewable energy costs have decreased by 85% since 2010",
		}
	} else if strings.Contains(query, "ai") || strings.Contains(query, "artificial intelligence") {
		results = []string{
			"AI market expected to reach $1.8 trillion by 2030",
			"Large language models showing breakthrough capabilities",
			"AI adoption accelerating across healthcare and finance",
			"Concerns about AI safety and regulation increasing",
		}
	} else if strings.Contains(query, "climate") {
		results = []string{
			"Global temperatures have risen 1.1°C since pre-industrial times",
			"Arctic sea ice declining at 13% per decade",
			"Extreme weather events becoming more frequent",
			"Countries committing to net-zero emissions by 2050",
		}
	} else {
		results = []string{
			fmt.Sprintf("Search result 1 for '%s'", args.Query),
			fmt.Sprintf("Search result 2 for '%s'", args.Query),
			fmt.Sprintf("Search result 3 for '%s'", args.Query),
		}
	}

	return searchResult{
		Query:   args.Query,
		Results: results,
		Count:   len(results),
	}, nil
}

// Data structures for search tool.
type searchArgs struct {
	Query string `json:"query" jsonschema:"description=The search query,required"`
}

type searchResult struct {
	Query   string   `json:"query"`
	Results []string `json:"results"`
	Count   int      `json:"count"`
}
