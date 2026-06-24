//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command codeact demonstrates an LLMAgent using execute_tool_code for a
// multi-step product-quote workflow. It does not use a GenerationConfig:
// openai.New reads OPENAI_API_KEY and OPENAI_BASE_URL from the environment.
//
// Run from the examples module:
//
//	cd examples && go run ./codeact -model gpt-5
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	bridge "trpc.group/trpc-go/trpc-agent-go/codeexecutor/codeact"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/toolcode"
)

var (
	modelName = flag.String("model", "gpt-5", "Model name; OpenAI credentials and base URL come from the environment")
	prompt    = flag.String("prompt", "Customer customer-42 needs 3 ergonomic office chairs. Find all options with enough stock, create a quote from those options, and report any unavailable options.", "Request to send to the quote agent")
)

type product struct {
	SKU       string  `json:"sku" jsonschema:"description=Stable product SKU,required"`
	Name      string  `json:"name" jsonschema:"description=Product name,required"`
	UnitPrice float64 `json:"unit_price" jsonschema:"description=Unit price in USD,required"`
}

type searchCatalogInput struct {
	Query string `json:"query" jsonschema:"description=Product search query,required"`
}

type searchCatalogOutput struct {
	Products []product `json:"products" jsonschema:"description=Matching catalog products,required"`
}

type inventoryInput struct {
	SKU string `json:"sku" jsonschema:"description=Product SKU,required"`
}

type inventoryOutput struct {
	SKU       string `json:"sku" jsonschema:"description=Product SKU,required"`
	Available int    `json:"available" jsonschema:"description=Units currently available to quote,required"`
}

type quoteItem struct {
	SKU      string `json:"sku" jsonschema:"description=Product SKU,required"`
	Quantity int    `json:"quantity" jsonschema:"description=Requested quantity,required"`
}

type createQuoteInput struct {
	CustomerID string      `json:"customer_id" jsonschema:"description=Customer identifier,required"`
	Items      []quoteItem `json:"items" jsonschema:"description=In-stock line items to quote,required"`
}

type createQuoteOutput struct {
	QuoteID string      `json:"quote_id" jsonschema:"description=Created quote identifier,required"`
	Total   float64     `json:"total" jsonschema:"description=Quote total in USD,required"`
	Items   []quoteItem `json:"items" jsonschema:"description=Quoted line items,required"`
}

func main() {
	flag.Parse()
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		log.Fatal("OPENAI_API_KEY is required")
	}

	quoteTool, err := newQuoteTool()
	if err != nil {
		log.Fatal(err)
	}

	agent := llmagent.New(
		"product-quote-agent",
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("Creates a product quote by orchestrating catalog, inventory, and quote tools"),
		llmagent.WithInstruction(`You are a product-quote assistant.

For requests that require finding products, checking stock, and creating a quote, use execute_tool_code. Generate Python that:
1. calls search_catalog;
2. loops through every returned product and calls get_inventory;
3. includes a product in the quote only when available stock is at least the requested quantity;
4. calls create_quote once, after the filtering is complete; and
5. returns structured JSON containing selected products, rejected products, and the quote.

Use only the tools documented in execute_tool_code. Do not use shell commands, filesystem APIs, transfer_to_agent, or nested agents. After the tool succeeds, explain the quote clearly to the user.`),
		llmagent.WithTools([]tool.Tool{quoteTool}),
	)

	r := runner.NewRunner("codeact-product-quote", agent)
	defer r.Close()

	fmt.Printf("CodeAct product quote agent (model: %s)\n", *modelName)
	fmt.Printf("Request: %s\n\n", *prompt)
	events, err := r.Run(context.Background(), "demo-user", "codeact-quote-session", model.NewUserMessage(*prompt))
	if err != nil {
		log.Fatalf("run agent: %v", err)
	}
	printEvents(events)
}

func newQuoteTool() (tool.CallableTool, error) {
	searchCatalog := function.NewFunctionTool(
		func(_ context.Context, in searchCatalogInput) (searchCatalogOutput, error) {
			return searchCatalogOutput{Products: []product{
				{SKU: "chair-ergonomic-pro", Name: "Ergonomic Pro Chair", UnitPrice: 399},
				{SKU: "chair-ergonomic-lite", Name: "Ergonomic Lite Chair", UnitPrice: 249},
				{SKU: "chair-standing", Name: "Standing Chair", UnitPrice: 179},
			}}, nil
		},
		function.WithName("search_catalog"),
		function.WithDescription("Return catalog products that match a product query."),
	)
	getInventory := function.NewFunctionTool(
		func(_ context.Context, in inventoryInput) (inventoryOutput, error) {
			available := map[string]int{
				"chair-ergonomic-pro":  8,
				"chair-ergonomic-lite": 1,
				"chair-standing":       5,
			}[in.SKU]
			return inventoryOutput{SKU: in.SKU, Available: available}, nil
		},
		function.WithName("get_inventory"),
		function.WithDescription("Return currently available inventory for one SKU."),
	)
	createQuote := function.NewFunctionTool(
		func(_ context.Context, in createQuoteInput) (createQuoteOutput, error) {
			if len(in.Items) == 0 {
				return createQuoteOutput{}, fmt.Errorf("cannot create a quote with no items")
			}
			priceBySKU := map[string]float64{
				"chair-ergonomic-pro":  399,
				"chair-ergonomic-lite": 249,
				"chair-standing":       179,
			}
			var total float64
			for _, item := range in.Items {
				total += priceBySKU[item.SKU] * float64(item.Quantity)
			}
			return createQuoteOutput{
				QuoteID: "quote-customer-42-demo",
				Total:   total,
				Items:   in.Items,
			}, nil
		},
		function.WithName("create_quote"),
		function.WithDescription("Create a quote from already validated in-stock line items."),
	)

	// Only this outer tool is visible to the model. The three business tools
	// are visible only to the generated Python through the allowlisted gateway.
	return toolcode.NewTool(
		bridge.LocalRunner{},
		[]tool.CallableTool{searchCatalog, getInventory, createQuote},
	)
}

func printEvents(events <-chan *event.Event) {
	for evt := range events {
		if evt.Error != nil {
			fmt.Printf("Agent error: %s\n", evt.Error.Message)
			continue
		}
		if evt.Response == nil {
			continue
		}
		if evt.Response.Error != nil {
			fmt.Printf("Model error: %s\n", evt.Response.Error.Message)
			continue
		}
		for _, choice := range evt.Response.Choices {
			for _, call := range choice.Message.ToolCalls {
				fmt.Printf("Tool call: %s\n%s\n\n", call.Function.Name, call.Function.Arguments)
			}
			if choice.Message.Role == model.RoleTool && choice.Message.Content != "" {
				fmt.Printf("Tool result: %s\n\n", choice.Message.Content)
			}
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			} else if choice.Message.Role == model.RoleAssistant && choice.Message.Content != "" {
				fmt.Print(choice.Message.Content)
			}
		}
	}
	fmt.Println()
}
