package main

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

func TestCreateAgentCallbacks(t *testing.T) {
	callbacks := createAgentCallbacks()
	if callbacks == nil {
		t.Fatal("Expected non-nil agent callbacks")
	}
	if len(callbacks.BeforeAgent) == 0 {
		t.Error("Expected at least one before agent callback")
	}
	if len(callbacks.AfterAgent) == 0 {
		t.Error("Expected at least one after agent callback")
	}
}

func TestCreateModelCallbacks(t *testing.T) {
	callbacks := createModelCallbacks()
	if callbacks == nil {
		t.Fatal("Expected non-nil model callbacks")
	}
	if len(callbacks.BeforeModel) == 0 {
		t.Error("Expected at least one before model callback")
	}
	if len(callbacks.AfterModel) == 0 {
		t.Error("Expected at least one after model callback")
	}
}

func TestCreateToolCallbacks(t *testing.T) {
	callbacks := createToolCallbacks()
	if callbacks == nil {
		t.Fatal("Expected non-nil tool callbacks")
	}
	if len(callbacks.BeforeTool) == 0 {
		t.Error("Expected at least one before tool callback")
	}
	if len(callbacks.AfterTool) == 0 {
		t.Error("Expected at least one after tool callback")
	}
}

func TestAgentCallbacks_SkipExecution(t *testing.T) {
	callbacks := createAgentCallbacks()

	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation",
		Message:      model.NewUserMessage("skip"),
	}

	customResponse, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !skip {
		t.Error("Expected skip to be true")
	}
	if customResponse == nil {
		t.Fatal("Expected non-nil custom response")
	}
	if len(customResponse.Choices) == 0 {
		t.Error("Expected at least one choice in response")
	}
}

func TestAgentCallbacks_CustomResponse(t *testing.T) {
	callbacks := createAgentCallbacks()

	invocation := &agent.Invocation{
		AgentName:    "test-agent",
		InvocationID: "test-invocation",
		Message:      model.NewUserMessage("custom"),
	}

	customResponse, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if skip {
		t.Error("Expected skip to be false")
	}
	if customResponse == nil {
		t.Fatal("Expected non-nil custom response")
	}
	if len(customResponse.Choices) == 0 {
		t.Error("Expected at least one choice in response")
	}
}

func TestModelCallbacks_SkipExecution(t *testing.T) {
	callbacks := createModelCallbacks()

	request := &model.Request{
		Messages: []model.Message{},
	}

	customResponse, skip, err := callbacks.RunBeforeModel(context.Background(), request)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !skip {
		t.Error("Expected skip to be true")
	}
	if customResponse == nil {
		t.Fatal("Expected non-nil custom response")
	}
	if len(customResponse.Choices) == 0 {
		t.Error("Expected at least one choice in response")
	}
}

func TestToolCallbacks_SkipExecution(t *testing.T) {
	callbacks := createToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "skip-tool",
		Description: "A tool to skip",
	}

	args := []byte(`{"test": "value"}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "skip-tool", declaration, args)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !skip {
		t.Error("Expected skip to be true")
	}
	if customResult == nil {
		t.Fatal("Expected non-nil custom result")
	}

	result, ok := customResult.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", customResult)
	}
	if result["skipped"] != "true" {
		t.Errorf("Expected 'skipped': 'true', got %v", result)
	}
}

func TestToolCallbacks_CustomResult(t *testing.T) {
	callbacks := createToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "calculator",
		Description: "A calculator tool",
	}

	args := []byte(`{"a":0,"b":0}`)

	customResult, skip, err := callbacks.RunBeforeTool(context.Background(), "calculator", declaration, args)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if skip {
		t.Error("Expected skip to be false")
	}
	if customResult == nil {
		t.Fatal("Expected non-nil custom result")
	}

	result, ok := customResult.(CalculatorOutput)
	if !ok {
		t.Fatalf("Expected CalculatorOutput, got %T", customResult)
	}
	if result.Result != 42 {
		t.Errorf("Expected result 42, got %d", result.Result)
	}
}

func TestToolCallbacks_OverrideResult(t *testing.T) {
	callbacks := createToolCallbacks()

	declaration := &tool.Declaration{
		Name:        "calculator",
		Description: "A calculator tool",
	}

	args := []byte(`{"a":5,"b":3}`)
	result := CalculatorOutput{Result: 8}

	customResult, override, err := callbacks.RunAfterTool(context.Background(), "calculator", declaration, args, result, nil)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !override {
		t.Error("Expected override to be true")
	}
	if customResult == nil {
		t.Fatal("Expected non-nil custom result")
	}

	formattedResult, ok := customResult.(map[string]string)
	if !ok {
		t.Fatalf("Expected map[string]string, got %T", customResult)
	}
	if formattedResult["formatted_result"] != "The answer is 8" {
		t.Errorf("Expected 'The answer is 8', got %s", formattedResult["formatted_result"])
	}
}
