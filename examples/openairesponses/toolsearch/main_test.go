//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses/toolsearch"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestKeywordSearchSelectsMatchingCandidates(t *testing.T) {
	search := toolsearch.NewKeyword()
	candidates := []responses.ToolSearchEntry{
		{Namespace: "billing", NamespaceDescription: "Billing tools for invoices and payments.",
			Name: "lookup_invoice", Tool: searchTool("billing_lookup_invoice", "Look up an invoice and its payment status.")},
		{Namespace: "billing", NamespaceDescription: "Billing tools for invoices and payments.",
			Name: "list_invoices", Tool: searchTool("billing_list_invoices", "List the invoices of a customer.")},
		{Namespace: "crm", NamespaceDescription: "CRM tools for customer lookup and order management.",
			Name: "list_orders", Tool: searchTool("crm_list_orders", "List the open orders of a customer.")},
		{Name: "shipping_eta", Tool: searchTool("shipping_eta", "Estimate the delivery date of a shipped order.")},
	}
	for _, tc := range []struct {
		name, query string
		want        []string
	}{
		{"invoice ID", "Is invoice inv_7 paid?", []string{"billing_lookup_invoice"}},
		{"invoice with namespace", "Look up or retrieve an invoice by invoice ID using billing tools, " +
			"including invoice ID, payment status, and amount.", []string{"billing_lookup_invoice", "billing_list_invoices"}},
		{"customer invoices", "List invoices for a customer.", []string{"billing_list_invoices", "crm_list_orders"}},
		{"customer invoices with ID in result", "Billing tool to list invoices for a customer using customer ID " +
			"and return invoice IDs.", []string{"billing_list_invoices", "billing_lookup_invoice", "crm_list_orders"}},
		{"orders and delivery", "Find/list orders for a customer using customer ID, then retrieve shipment tracking " +
			"estimated delivery date for shipped order by order ID or tracking details.", []string{"crm_list_orders", "billing_list_invoices", "shipping_eta"}},
		{"orders and shipping status", "Find/list orders belonging to a customer by customer ID, including order IDs " +
			"and shipping status; also get estimated delivery date for a shipped order using its order ID.",
			[]string{"crm_list_orders", "shipping_eta", "billing_list_invoices", "billing_lookup_invoice"}},
		{"short multi-tool query", "orders and delivery", []string{"crm_list_orders", "shipping_eta"}},
		{"whole namespace", "billing tools", []string{"billing_lookup_invoice", "billing_list_invoices"}},
		{"unrelated word and filler", "the voice tools", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := search(context.Background(), &responses.ToolSearchRequest{Query: tc.query, Candidates: candidates})
			if err != nil {
				t.Fatal(err)
			}
			var want []responses.ToolSearchEntry
			for _, name := range tc.want {
				for _, candidate := range candidates {
					if candidate.Tool.Declaration().Name == name {
						want = append(want, candidate)
					}
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("search(%q) = %v, want %v", tc.query, got, want)
			}
		})
	}
}

func searchTool(name, description string) tool.Tool {
	return function.NewFunctionTool(
		func(context.Context, struct{}) (string, error) { return "", nil },
		function.WithName(name),
		function.WithDescription(description),
	)
}

func TestPrintEventsShowsSearchResultAndLoadedCall(t *testing.T) {
	ch := make(chan *event.Event, 4)
	ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleTool, ToolID: "search_1", ToolName: responses.ToolSearchToolName,
		Content: `{"loaded_tools":["billing_lookup_invoice"]}`,
	}}}}}
	ch <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{ID: "call_1", Function: model.FunctionDefinitionParam{
			Name: "billing_lookup_invoice", Arguments: []byte(`{"invoice_id":"inv_7"}`)}}},
	}}}}}
	ch <- &event.Event{Response: &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleTool, ToolID: "call_1", ToolName: "billing_lookup_invoice", Content: `{"status":"paid"}`,
	}}}}}
	ch <- &event.Event{Response: &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("Paid.")}}}}
	close(ch)
	var out bytes.Buffer
	printEvents(&out, ch, false)
	const want = "" +
		"Tool result: tool_search (ID: search_1): {\"loaded_tools\":[\"billing_lookup_invoice\"]}\n" +
		"Tool call: billing_lookup_invoice (ID: call_1)\n" +
		"  Args: {\"invoice_id\":\"inv_7\"}\n" +
		"Tool result: billing_lookup_invoice (ID: call_1): {\"status\":\"paid\"}\n" +
		"Assistant: Paid.\n"
	if got := out.String(); got != want {
		t.Fatalf("printEvents() = %q, want %q", got, want)
	}
}
