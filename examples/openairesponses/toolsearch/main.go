//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates OpenAI Responses tool search with deferred
// LLMAgent tools, using hosted or client-executed search.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	openaiembedder "trpc.group/trpc-go/trpc-agent-go/knowledge/embedder/openai"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/deferred"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	modelName := flag.String("model", "gpt-5.4", "OpenAI model name; tool search needs a model that supports it")
	mode := flag.String("mode", "server", "Tool search mode: server (hosted), client (keyword), or semantic (embeddings)")
	streaming := flag.Bool("streaming", true, "Enable streaming responses")
	flag.Parse()

	var search responses.Option
	switch *mode {
	case "semantic":
		handler, err := toolsearch.NewSemantic(openaiembedder.New())
		if err != nil {
			log.Fatal(err)
		}
		search = responses.WithClientToolSearch(handler)
	case "server":
		search = responses.WithHostedToolSearch()
	case "client":
		search = responses.WithClientToolSearch(keywordSearch)
	default:
		log.Fatalf("unsupported -mode %q: use server, client, or semantic", *mode)
	}

	// The SDK reads OPENAI_API_KEY and OPENAI_BASE_URL.
	supportAgent := llmagent.New(
		"responses-tool-search-agent",
		llmagent.WithModel(responses.New(*modelName, search)),
		llmagent.WithDescription("A customer support assistant with CRM, billing, and shipping tools."),
		llmagent.WithInstruction("Answer customer support questions with data from the tools. Load the tools you need before answering."),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: *streaming}),
		// Deferral only changes what the model sees up front. Deferred tools
		// stay registered and run like any other tool once they are loaded.
		llmagent.WithTools([]tool.Tool{
			currentTimeTool(),
			deferred.Tool(shippingETATool()),
		}),
		llmagent.WithToolSets([]tool.ToolSet{
			deferred.ToolSet(crmToolSet(), deferred.WithFilter(tool.NewIncludeToolNamesFilter("list_orders"))),
			deferred.ToolSet(billingToolSet()),
		}),
	)
	// The in-memory session lives only until this process exits.
	sessions := sessioninmemory.NewSessionService()
	defer sessions.Close()
	r := runner.NewRunner(
		"responses-tool-search-example",
		supportAgent,
		runner.WithSessionService(sessions),
	)
	defer r.Close()

	sessionID := fmt.Sprintf("toolsearch-%d", time.Now().UnixNano())
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Printf("OpenAI Responses tool search (%s mode). Type /resume to retry a pending turn or /exit to quit.\n", *mode)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "/exit" {
			return
		}
		if text == "" {
			continue
		}
		message := model.NewUserMessage(text)
		var opts []agent.RunOption
		if text == "/resume" {
			message = model.Message{}
			opts = append(opts, agent.WithResume(true))
		}
		ch, err := r.Run(context.Background(), "demo-user", sessionID, message, opts...)
		if err != nil {
			log.Printf("run: %v", err)
			continue
		}
		printEvents(os.Stdout, ch, *streaming)
	}
	if err := scanner.Err(); err != nil {
		log.Printf("read input: %v", err)
	}
}

// keywordSearch scores whole-word matches and gives tool names extra weight.
func keywordSearch(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
	query := searchWords(req.Query)
	scores := make([]int, len(req.Candidates))
	best := 0
	for i, candidate := range req.Candidates {
		keywords := candidate.Namespace + " " + candidate.Name + " " + candidate.Tool.Declaration().Description
		scores[i] = countMatches(query, keywords) + countMatches(query, candidate.Name)
		best = max(best, scores[i])
	}
	// Allow a one-point gap for short matches and up to a third of a stronger score.
	margin := max(1, best/3)
	var result []responses.ToolSearchEntry
	for i, score := range scores {
		if score > 0 && best-score <= margin {
			result = append(result, req.Candidates[i])
		}
	}
	return result, nil
}

func countMatches(query map[string]bool, text string) int {
	count := 0
	for word := range searchWords(text) {
		if query[word] {
			count++
		}
	}
	return count
}

func searchWords(text string) map[string]bool {
	words := make(map[string]bool)
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		switch word {
		case "and", "for", "from", "get", "its", "look", "the", "tool", "tools", "with":
			continue
		}
		if len(word) > 2 {
			words[word] = true
		}
	}
	return words
}

// staticToolSet is a fixed ToolSet with a description. The description becomes
// the namespace description that the model sees before it loads the tools.
type staticToolSet struct {
	name        string
	description string
	tools       []tool.Tool
}

func (s staticToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s staticToolSet) Close() error                      { return nil }
func (s staticToolSet) Name() string                      { return s.name }
func (s staticToolSet) Description() string               { return s.description }

func currentTimeTool() tool.Tool {
	type result struct {
		Time string `json:"time"`
	}
	return function.NewFunctionTool(
		func(context.Context, struct{}) (result, error) {
			return result{Time: time.Now().Format(time.RFC3339)}, nil
		},
		function.WithName("current_time"),
		function.WithDescription("Return the current time."),
	)
}

func shippingETATool() tool.Tool {
	type arguments struct {
		OrderID string `json:"order_id" jsonschema:"description=The order ID"`
	}
	type result struct {
		OrderID string `json:"order_id"`
		ETA     string `json:"eta"`
	}
	return function.NewFunctionTool(
		func(_ context.Context, in arguments) (result, error) {
			return result{OrderID: in.OrderID, ETA: time.Now().AddDate(0, 0, 2).Format("2006-01-02")}, nil
		},
		function.WithName("shipping_eta"),
		function.WithDescription("Estimate the delivery date of a shipped order."),
	)
}

func crmToolSet() tool.ToolSet {
	type customerArguments struct {
		Email string `json:"email" jsonschema:"description=The customer email address"`
	}
	type customer struct {
		CustomerID string `json:"customer_id"`
		Name       string `json:"name"`
	}
	type ordersArguments struct {
		CustomerID string `json:"customer_id" jsonschema:"description=The customer ID"`
	}
	type order struct {
		OrderID string `json:"order_id"`
		Status  string `json:"status"`
	}
	return staticToolSet{
		name:        "crm",
		description: "CRM tools for customer lookup and order management.",
		tools: []tool.Tool{
			function.NewFunctionTool(
				func(_ context.Context, in customerArguments) (customer, error) {
					return customer{CustomerID: "cus_42", Name: "Ada Lovelace"}, nil
				},
				function.WithName("lookup_customer"),
				function.WithDescription("Look up a customer by email address."),
			),
			function.NewFunctionTool(
				func(_ context.Context, in ordersArguments) ([]order, error) {
					return []order{{OrderID: "ord_1001", Status: "shipped"}}, nil
				},
				function.WithName("list_orders"),
				function.WithDescription("List the open orders of a customer."),
			),
		},
	}
}

func billingToolSet() tool.ToolSet {
	type invoiceArguments struct {
		InvoiceID string `json:"invoice_id" jsonschema:"description=The invoice ID"`
	}
	type invoice struct {
		InvoiceID string  `json:"invoice_id"`
		Status    string  `json:"status"`
		Amount    float64 `json:"amount"`
	}
	type customerArguments struct {
		CustomerID string `json:"customer_id" jsonschema:"description=The customer ID"`
	}
	return staticToolSet{
		name:        "billing",
		description: "Billing tools for invoices and payments.",
		tools: []tool.Tool{
			function.NewFunctionTool(
				func(_ context.Context, in invoiceArguments) (invoice, error) {
					return invoice{InvoiceID: in.InvoiceID, Status: "paid", Amount: 120}, nil
				},
				function.WithName("lookup_invoice"),
				function.WithDescription("Look up an invoice and its payment status."),
			),
			function.NewFunctionTool(
				func(_ context.Context, in customerArguments) ([]invoice, error) {
					return []invoice{{InvoiceID: "inv_7", Status: "paid", Amount: 120}}, nil
				},
				function.WithName("list_invoices"),
				function.WithDescription("List the invoices of a customer."),
			),
		},
	}
}

// printEvents writes assistant text and committed tool activity. A client
// tool search result is a committed tool message named tool_search whose
// content lists the loaded tools. Hosted searches run inside the model
// response and are not printed; their loaded tools appear as tool calls.
func printEvents(w io.Writer, ch <-chan *event.Event, streaming bool) {
	p := &eventPrinter{w: w, streaming: streaming}
	for evt := range ch {
		p.print(evt)
	}
}

// eventPrinter tracks whether an assistant line is open and the text streamed
// into it.
type eventPrinter struct {
	w             io.Writer
	streaming     bool
	assistantOpen bool
	streamedText  strings.Builder
}

func (p *eventPrinter) print(evt *event.Event) {
	if evt == nil {
		return
	}
	if evt.Error != nil {
		p.closeAssistant()
		fmt.Fprintf(p.w, "Error: %s\n", evt.Error.Message)
		p.streamedText.Reset()
		return
	}
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}
	choice := evt.Response.Choices[0]
	if p.streaming && evt.IsPartial {
		if choice.Delta.Content != "" && !evt.IsToolResultResponse() {
			p.writeAssistant(choice.Delta.Content)
			p.streamedText.WriteString(choice.Delta.Content)
		}
	} else if choice.Message.Role == model.RoleAssistant {
		p.printCompleted(choice.Message.Content)
	}
	if !evt.IsPartial {
		p.printToolActivity(evt.Response.Choices)
	}
	if evt.IsFinalResponse() {
		p.closeAssistant()
	}
}

// printCompleted prints a completed assistant message unless the same text
// was already streamed.
func (p *eventPrinter) printCompleted(content string) {
	streamed := p.streamedText.String()
	p.streamedText.Reset()
	switch {
	case p.streaming && streamed != "" && streamed != content:
		p.closeAssistant()
		fmt.Fprintf(p.w, "Assistant (completed): %s", content)
		p.assistantOpen = true
	case (!p.streaming || streamed == "") && content != "":
		p.writeAssistant(content)
	}
}

func (p *eventPrinter) printToolActivity(choices []model.Choice) {
	for _, choice := range choices {
		for _, call := range choice.Message.ToolCalls {
			p.closeAssistant()
			fmt.Fprintf(p.w, "Tool call: %s (ID: %s)\n", call.Function.Name, call.ID)
			if len(call.Function.Arguments) > 0 {
				fmt.Fprintf(p.w, "  Args: %s\n", string(call.Function.Arguments))
			}
		}
		if msg := choice.Message; msg.Role == model.RoleTool && msg.ToolID != "" {
			p.closeAssistant()
			fmt.Fprintf(p.w, "Tool result: %s (ID: %s): %s\n", msg.ToolName, msg.ToolID, strings.TrimSpace(msg.Content))
		}
	}
}

func (p *eventPrinter) writeAssistant(text string) {
	if !p.assistantOpen {
		fmt.Fprint(p.w, "Assistant: ")
		p.assistantOpen = true
	}
	fmt.Fprint(p.w, text)
}

func (p *eventPrinter) closeAssistant() {
	if !p.assistantOpen {
		return
	}
	fmt.Fprintln(p.w)
	p.assistantOpen = false
}
