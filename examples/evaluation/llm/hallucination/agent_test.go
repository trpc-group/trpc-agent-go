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
	"io"
	"os"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

func TestLookupProductCatalog(t *testing.T) {
	result, err := lookupProductCatalog(context.Background(), productCatalogArgs{ProductID: productIDAuroraPadX2})
	if err != nil {
		t.Fatalf("lookupProductCatalog() error = %v", err)
	}
	if result.Name != "AuroraPad X2" {
		t.Fatalf("lookupProductCatalog() name = %q, want %q", result.Name, "AuroraPad X2")
	}
	if result.ReleaseYear != 2024 {
		t.Fatalf("lookupProductCatalog() release year = %d, want %d", result.ReleaseYear, 2024)
	}
	if result.BatteryLifeHours != 18 {
		t.Fatalf("lookupProductCatalog() battery life = %d, want %d", result.BatteryLifeHours, 18)
	}
	if result.MarketSegment != "field operations teams" {
		t.Fatalf("lookupProductCatalog() market segment = %q, want %q", result.MarketSegment, "field operations teams")
	}
	_, err = lookupProductCatalog(context.Background(), productCatalogArgs{ProductID: "missing-product"})
	if err == nil || err.Error() != `product "missing-product" not found in the catalog` {
		t.Fatalf("lookupProductCatalog() missing product error = %v", err)
	}
}

func TestProductCatalogInputSchema(t *testing.T) {
	schema := productCatalogInputSchema()
	if schema == nil {
		t.Fatal("productCatalogInputSchema() returned nil")
	}
	if schema.Type != "object" {
		t.Fatalf("productCatalogInputSchema() type = %q, want %q", schema.Type, "object")
	}
	if schema.Description != "Product catalog lookup input." {
		t.Fatalf("productCatalogInputSchema() description = %q", schema.Description)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "product_id" {
		t.Fatalf("productCatalogInputSchema() required = %#v", schema.Required)
	}
	property, ok := schema.Properties["product_id"]
	if !ok {
		t.Fatal(`productCatalogInputSchema() missing "product_id" property`)
	}
	if property.Type != "string" {
		t.Fatalf("productCatalogInputSchema() property type = %q, want %q", property.Type, "string")
	}
	if len(property.Enum) != 2 || property.Enum[0] != productIDAuroraPadX2 || property.Enum[1] != productIDTerraWatchS {
		t.Fatalf("productCatalogInputSchema() enum = %#v", property.Enum)
	}
}

func TestNewQAAgent(t *testing.T) {
	qa := newQAAgent("gpt-5.4", true)
	if qa.Info().Name != defaultAgentName {
		t.Fatalf("newQAAgent() name = %q, want %q", qa.Info().Name, defaultAgentName)
	}
	if qa.Info().Description == "" {
		t.Fatal("newQAAgent() description is empty")
	}
	if len(qa.Tools()) != 1 {
		t.Fatalf("newQAAgent() tools len = %d, want %d", len(qa.Tools()), 1)
	}
	if qa.Tools()[0].Declaration().Name != productCatalogToolName {
		t.Fatalf("newQAAgent() tool name = %q, want %q", qa.Tools()[0].Declaration().Name, productCatalogToolName)
	}
}

func TestNewJudgeAgent(t *testing.T) {
	judge := newJudgeAgent("gpt-5.4")
	if judge.Info().Name != judgeAgentName {
		t.Fatalf("newJudgeAgent() name = %q, want %q", judge.Info().Name, judgeAgentName)
	}
	if judge.Info().Description == "" {
		t.Fatal("newJudgeAgent() description is empty")
	}
	if len(judge.Tools()) != 0 {
		t.Fatalf("newJudgeAgent() tools len = %d, want %d", len(judge.Tools()), 0)
	}
}

func TestScriptedHallucinationAgentRun(t *testing.T) {
	candidate := newForcedHallucinationAgent()
	if len(candidate.Tools()) != 1 {
		t.Fatalf("Tools() len = %d, want %d", len(candidate.Tools()), 1)
	}
	if candidate.Info().Name != defaultAgentName {
		t.Fatalf("Info().Name = %q, want %q", candidate.Info().Name, defaultAgentName)
	}
	if candidate.SubAgents() != nil {
		t.Fatalf("SubAgents() = %#v, want nil", candidate.SubAgents())
	}
	if candidate.FindSubAgent("missing") != nil {
		t.Fatalf("FindSubAgent() = %#v, want nil", candidate.FindSubAgent("missing"))
	}
	invocation := &agent.Invocation{InvocationID: "inv-1", AgentName: defaultAgentName}
	ch, err := candidate.Run(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events := collectEvents(ch)
	if len(events) != 3 {
		t.Fatalf("Run() emitted %d events, want %d", len(events), 3)
	}
	if events[0].InvocationID != "inv-1" {
		t.Fatalf("first event invocation id = %q, want %q", events[0].InvocationID, "inv-1")
	}
	if events[0].Author != defaultAgentName {
		t.Fatalf("first event author = %q, want %q", events[0].Author, defaultAgentName)
	}
	if len(events[0].Choices) != 1 || len(events[0].Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("first event tool calls = %#v", events[0].Choices)
	}
	if events[0].Choices[0].Message.ToolCalls[0].ID != productCatalogToolCall {
		t.Fatalf("first event tool call id = %q, want %q", events[0].Choices[0].Message.ToolCalls[0].ID, productCatalogToolCall)
	}
	if events[0].Choices[0].Message.ToolCalls[0].Function.Name != productCatalogToolName {
		t.Fatalf("first event tool name = %q, want %q", events[0].Choices[0].Message.ToolCalls[0].Function.Name, productCatalogToolName)
	}
	if events[1].Author != productCatalogToolName {
		t.Fatalf("second event author = %q, want %q", events[1].Author, productCatalogToolName)
	}
	if events[1].Done {
		t.Fatal("second event Done = true, want false")
	}
	if len(events[1].Choices) != 1 || events[1].Choices[0].Message.Content == "" {
		t.Fatalf("second event content = %#v", events[1].Choices)
	}
	if !strings.Contains(events[1].Choices[0].Message.Content, `"release_year":2024`) {
		t.Fatalf("second event content = %q", events[1].Choices[0].Message.Content)
	}
	if events[2].Author != defaultAgentName {
		t.Fatalf("third event author = %q, want %q", events[2].Author, defaultAgentName)
	}
	if !events[2].Done {
		t.Fatal("third event Done = false, want true")
	}
	if len(events[2].Choices) != 1 {
		t.Fatalf("third event choices = %#v", events[2].Choices)
	}
	if events[2].Choices[0].Message.Content != hallucinatedAnswer {
		t.Fatalf("third event content = %q, want %q", events[2].Choices[0].Message.Content, hallucinatedAnswer)
	}
}

func TestScriptedHallucinationAgentRunWithNilInvocation(t *testing.T) {
	candidate := newForcedHallucinationAgent()
	ch, err := candidate.Run(context.Background(), nil)
	if err == nil || err.Error() != "invocation is nil" {
		t.Fatalf("Run() nil invocation error = %v", err)
	}
	if ch != nil {
		t.Fatalf("Run() channel = %#v, want nil", ch)
	}
}

func TestPrintSummary(t *testing.T) {
	result := &evaluation.EvaluationResult{
		AppName:       appName,
		EvalSetID:     "hallucination-basic",
		OverallStatus: status.EvalStatusPassed,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID:    "hallucination-check",
			OverallStatus: status.EvalStatusPassed,
			EvalCaseResults: []*evalresult.EvalCaseResult{{
				EvalID: "hallucination-check",
				RunID:  1,
			}},
			MetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "llm_hallucinations",
				Score:      1.0,
				Threshold:  0.9,
				EvalStatus: status.EvalStatusPassed,
			}},
		}},
	}
	output := captureStdout(t, func() {
		printSummary(result, "/tmp/hallucination-output")
	})
	for _, expected := range []string{
		"Hallucination evaluation completed with local storage",
		"App: hallucination-eval-app",
		"Eval Set: hallucination-basic",
		"Overall Status: passed",
		"Runs: 1",
		"Case hallucination-check -> passed",
		"Metric llm_hallucinations: score 1.00 (threshold 0.90) => passed",
		"Results saved under: /tmp/hallucination-output",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("printSummary() output missing %q in %q", expected, output)
		}
	}
}

func collectEvents(ch <-chan *event.Event) []*event.Event {
	events := make([]*event.Event, 0)
	for evt := range ch {
		events = append(events, evt)
	}
	return events
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()
	fn()
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("reader.Close() error = %v", err)
	}
	return string(output)
}
