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
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestKeywordSearchSelectsMatchingCandidates(t *testing.T) {
	req := &responses.ToolSearchRequest{
		Query: "Is invoice inv_7 paid?",
		Candidates: []responses.ToolSearchCandidate{
			{ID: "billing_lookup_invoice", Namespace: "billing", NamespaceDescription: "Billing tools for invoices and payments.",
				Name: "lookup_invoice", Declaration: tool.Declaration{Description: "Look up an invoice and its payment status."}},
			{ID: "crm_list_orders", Namespace: "crm", NamespaceDescription: "CRM tools for customer lookup and order management.",
				Name: "list_orders", Declaration: tool.Declaration{Description: "List the open orders of a customer."}},
			{ID: "shipping_eta", Name: "shipping_eta", Declaration: tool.Declaration{Description: "Estimate the delivery date of a shipped order."}},
		},
	}
	got, err := keywordSearch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"billing_lookup_invoice"}; !reflect.DeepEqual(got.ToolIDs, want) {
		t.Fatalf("keywordSearch() = %v, want %v", got.ToolIDs, want)
	}
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
