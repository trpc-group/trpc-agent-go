//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/dataset"
	"trpc.group/trpc-go/trpc-agent-go/benchmark/memory/trpc-agent-go-impl/evaluation/metrics"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// AgenticEvaluator evaluates using agent with memory tools.
// The agent can explicitly call memory_add and memory_search.
type AgenticEvaluator struct {
	model         model.Model
	evalModel     model.Model
	memoryService memory.Service
	config        Config
	llmJudge      *metrics.LLMJudge
}

// NewAgenticEvaluator creates a new agentic evaluator.
func NewAgenticEvaluator(
	m, evalModel model.Model,
	memSvc memory.Service,
	cfg Config,
) *AgenticEvaluator {
	e := &AgenticEvaluator{
		model:         m,
		evalModel:     evalModel,
		memoryService: memSvc,
		config:        cfg,
	}
	if cfg.EnableLLMJudge && evalModel != nil {
		e.llmJudge = metrics.NewLLMJudge(evalModel)
	}
	return e
}

// Name returns the evaluator name.
func (e *AgenticEvaluator) Name() string {
	return "agentic"
}

// Evaluate runs evaluation on a sample using agentic memory tools.
func (e *AgenticEvaluator) Evaluate(
	ctx context.Context,
	sample *dataset.LoCoMoSample,
) (*SampleResult, error) {
	startTime := time.Now()
	userKey := memory.UserKey{
		AppName: "memory-eval-agentic",
		UserID:  sample.SampleID,
	}

	// Clear existing memories for this sample.
	_ = e.memoryService.ClearMemories(ctx, userKey)

	// Phase 1: Process conversation to let agent store memories.
	if err := e.processConversation(ctx, userKey, sample); err != nil {
		return nil, fmt.Errorf("process conversation: %w", err)
	}

	// Phase 2: Answer QA questions using agent with search tool.
	result := &SampleResult{
		SampleID:  sample.SampleID,
		QAResults: make([]*QAResult, 0, len(sample.QA)),
	}
	catAgg := metrics.NewCategoryAggregator()

	for _, qa := range sample.QA {
		qaResult, err := e.evaluateQA(ctx, userKey, qa)
		if err != nil {
			return nil, fmt.Errorf("evaluate QA %s: %w", qa.QuestionID, err)
		}
		result.QAResults = append(result.QAResults, qaResult)
		catAgg.Add(qa.Category, qaResult.Metrics)
	}

	result.ByCategory = catAgg.GetCategoryMetrics()
	result.Overall = catAgg.GetOverall()
	result.TotalTimeMs = time.Since(startTime).Milliseconds()
	return result, nil
}

// processConversation lets the agent process conversation and store memories.
// It processes each session separately to avoid overwhelming the agent with
// too much content at once.
func (e *AgenticEvaluator) processConversation(
	ctx context.Context,
	userKey memory.UserKey,
	sample *dataset.LoCoMoSample,
) error {
	// Process each session separately to ensure memories are properly extracted.
	for _, sess := range sample.Conversation {
		if err := e.processSession(ctx, userKey, sess); err != nil {
			// Log but continue with other sessions.
			if e.config.Verbose {
				log.Printf("  Warning: failed to process session %s: %v",
					sess.SessionID, err)
			}
		}
	}
	return nil
}

// processSession extracts and stores memories from a single conversation session.
func (e *AgenticEvaluator) processSession(
	ctx context.Context,
	userKey memory.UserKey,
	sess dataset.Session,
) error {
	// Build session content.
	var conv strings.Builder
	if sess.SessionDate != "" {
		fmt.Fprintf(&conv, "Date: %s\n\n", sess.SessionDate)
	}
	for _, turn := range sess.Turns {
		fmt.Fprintf(&conv, "%s: %s\n", turn.Speaker, turn.Text)
	}
	sessionContent := conv.String()
	if strings.TrimSpace(sessionContent) == "" {
		return nil
	}

	// Ask agent to extract and store important information from this session.
	prompt := fmt.Sprintf(`Analyze this conversation session and extract important facts.

## Session: %s
%s

## Instructions
Extract and store key information using memory_add tool:
- Personal facts (names, relationships, preferences, habits)
- Events (what happened, when, where)
- Plans and intentions (future activities, goals)
- Emotional states and opinions

For each fact, call memory_add with a concise description.
Include dates/times when mentioned.
After storing all memories, respond with "Done." (without tool_call).`,
		sess.SessionID, sessionContent)

	// Run agent with tool calling (write mode).
	_, err := e.runAgentWithTools(ctx, userKey, prompt, true)
	return err
}

// evaluateQA evaluates a single QA using agent with search tool.
func (e *AgenticEvaluator) evaluateQA(
	ctx context.Context,
	userKey memory.UserKey,
	qa dataset.QAItem,
) (*QAResult, error) {
	start := time.Now()

	prompt := fmt.Sprintf(`You have access to stored memories about a conversation.
Use the memory_search tool to find relevant information, then answer the question.

## Question
%s

## Instructions
1. First, search for relevant memories using memory_search.
2. Based on the search results, provide your answer.
3. If no relevant information is found, say so.
4. Be concise and direct.

Search for relevant memories and then answer.`, qa.Question)

	predicted, err := e.runAgentWithTools(ctx, userKey, prompt, false)
	if err != nil {
		return nil, err
	}

	// Calculate metrics.
	m := metrics.QAMetrics{
		F1:   metrics.CalculateF1(predicted, qa.Answer),
		BLEU: metrics.CalculateBLEU(predicted, qa.Answer),
	}

	// LLM judge if enabled.
	if e.llmJudge != nil {
		judgeResult, err := e.llmJudge.Evaluate(ctx, qa.Question, qa.Answer, predicted)
		if err == nil && judgeResult.Correct {
			m.LLMScore = judgeResult.Confidence
		}
	}

	return &QAResult{
		QuestionID: qa.QuestionID,
		Question:   qa.Question,
		Category:   qa.Category,
		Expected:   qa.Answer,
		Predicted:  predicted,
		Metrics:    m,
		LatencyMs:  time.Since(start).Milliseconds(),
	}, nil
}

// Tool name constants.
const (
	toolNameMemoryAdd    = "memory_add"
	toolNameMemorySearch = "memory_search"
)

// agenticToolDef represents a tool definition for agentic evaluation.
type agenticToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// parsedToolCall represents a parsed tool call from model response.
type parsedToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// Maximum iterations for agent tool calling.
const (
	// maxWriteIterations is the max iterations for memory write phase.
	// Set higher to allow agent to store multiple memories per session.
	maxWriteIterations = 15
	// maxReadIterations is the max iterations for memory read/search phase.
	maxReadIterations = 5
)

// runAgentWithTools runs the agent with tool calling support.
func (e *AgenticEvaluator) runAgentWithTools(
	ctx context.Context,
	userKey memory.UserKey,
	prompt string,
	allowWrite bool,
) (string, error) {
	toolDefs := e.buildToolDefs(allowWrite)
	messages := []model.Message{
		{Role: model.RoleUser, Content: prompt},
	}

	// Build tools JSON description for the prompt.
	toolsDesc := e.buildToolsDescription(toolDefs)

	// Use different max iterations for write vs read phases.
	maxIter := maxReadIterations
	if allowWrite {
		maxIter = maxWriteIterations
	}

	for i := 0; i < maxIter; i++ {
		// Include tool description in system message for models without native
		// tool support.
		systemPrompt := fmt.Sprintf(`You are a helpful assistant with access to memory tools.

## Available Tools
%s

## How to Use Tools
When you need to use a tool, respond with a JSON object in this exact format:
{"tool_call": {"name": "tool_name", "arguments": {...}}}

When you have completed all tasks, respond with your final answer without any tool_call.

Important: Only use ONE tool at a time. Wait for the tool result before making another call.`, toolsDesc)

		req := &model.Request{
			Messages: append([]model.Message{
				{Role: model.RoleSystem, Content: systemPrompt},
			}, messages...),
			GenerationConfig: model.GenerationConfig{
				Stream:    false,
				MaxTokens: intPtr(1000),
			},
		}

		respCh, err := e.model.GenerateContent(ctx, req)
		if err != nil {
			return "", fmt.Errorf("generate content: %w", err)
		}

		var content string
		for resp := range respCh {
			if resp.Error != nil {
				return "", fmt.Errorf("response error: %s", resp.Error.Message)
			}
			if len(resp.Choices) > 0 {
				content = resp.Choices[0].Message.Content
			}
		}

		// Try to parse tool call from response.
		toolCall := e.parseToolCall(content)
		if toolCall == nil {
			// No tool call, return the content as final answer.
			return strings.TrimSpace(content), nil
		}

		// Validate tool call.
		if !allowWrite && toolCall.Name == toolNameMemoryAdd {
			return "", fmt.Errorf("write operation not allowed")
		}

		// Execute tool call.
		result := e.executeToolCallDirect(ctx, userKey, toolCall)

		// Add messages to history.
		messages = append(messages, model.Message{
			Role:    model.RoleAssistant,
			Content: content,
		})
		messages = append(messages, model.Message{
			Role:    model.RoleUser,
			Content: fmt.Sprintf("[Tool Result for %s]\n%s", toolCall.Name, result),
		})
	}

	if allowWrite {
		// In write phase, reaching max iterations is acceptable.
		// The agent may have stored memories but didn't give a final response.
		if e.config.Verbose {
			log.Printf("Max iterations (%d) reached in write phase.", maxIter)
		}
		return "", nil
	}
	return "", fmt.Errorf("max iterations reached without final response")
}

// buildToolDefs builds tool definitions for the agent.
func (e *AgenticEvaluator) buildToolDefs(allowWrite bool) []agenticToolDef {
	tools := []agenticToolDef{
		{
			Name:        toolNameMemorySearch,
			Description: "Search memories by query.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query",
					},
				},
				"required": []string{"query"},
			},
		},
	}

	if allowWrite {
		tools = append(tools, agenticToolDef{
			Name:        toolNameMemoryAdd,
			Description: "Add a new memory.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"memory": map[string]any{
						"type":        "string",
						"description": "Memory content to store",
					},
					"topics": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional topics/tags",
					},
				},
				"required": []string{"memory"},
			},
		})
	}
	return tools
}

// buildToolsDescription builds a text description of available tools.
func (e *AgenticEvaluator) buildToolsDescription(tools []agenticToolDef) string {
	var desc strings.Builder
	for i, tool := range tools {
		if i > 0 {
			desc.WriteString("\n\n")
		}
		fmt.Fprintf(&desc, "### %s\n%s\n", tool.Name, tool.Description)
		if params, ok := tool.Parameters["properties"].(map[string]any); ok {
			desc.WriteString("Parameters:\n")
			for name, props := range params {
				if propMap, ok := props.(map[string]any); ok {
					propType, _ := propMap["type"].(string)
					propDesc, _ := propMap["description"].(string)
					fmt.Fprintf(&desc, "  - %s (%s): %s\n", name, propType, propDesc)
				}
			}
		}
	}
	return desc.String()
}

// parseToolCall attempts to parse a tool call from model response.
func (e *AgenticEvaluator) parseToolCall(content string) *parsedToolCall {
	content = strings.TrimSpace(content)

	// Try to find JSON object with tool_call.
	start := strings.Index(content, "{")
	if start == -1 {
		return nil
	}

	// Find matching closing brace.
	depth := 0
	end := -1
outer:
	for i := start; i < len(content); i++ {
		switch content[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
				break outer
			}
		}
	}

	if end == -1 {
		return nil
	}

	jsonStr := content[start : end+1]

	// Parse JSON.
	var wrapper struct {
		ToolCall *parsedToolCall `json:"tool_call"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &wrapper); err != nil {
		return nil
	}
	return wrapper.ToolCall
}

// executeToolCallDirect executes a parsed tool call.
func (e *AgenticEvaluator) executeToolCallDirect(
	ctx context.Context,
	userKey memory.UserKey,
	tc *parsedToolCall,
) string {
	switch tc.Name {
	case toolNameMemorySearch:
		query, _ := tc.Arguments["query"].(string)
		if query == "" {
			return "Error: query is required"
		}
		memories, err := e.memoryService.SearchMemories(ctx, userKey, query)
		if err != nil {
			return fmt.Sprintf("Error searching memories: %v", err)
		}
		if len(memories) == 0 {
			return "No relevant memories found."
		}
		var result strings.Builder
		for i, mem := range memories {
			if i >= e.config.TopK {
				break
			}
			fmt.Fprintf(&result, "[%d] %s\n", i+1, mem.Memory.Memory)
		}
		return result.String()

	case toolNameMemoryAdd:
		memContent, _ := tc.Arguments["memory"].(string)
		if memContent == "" {
			return "Error: memory content is required"
		}
		var topics []string
		if t, ok := tc.Arguments["topics"].([]any); ok {
			for _, v := range t {
				if s, ok := v.(string); ok {
					topics = append(topics, s)
				}
			}
		}
		if err := e.memoryService.AddMemory(ctx, userKey, memContent, topics); err != nil {
			return fmt.Sprintf("Error adding memory: %v", err)
		}
		return "Memory added successfully."

	default:
		return fmt.Sprintf("Unknown tool: %s", tc.Name)
	}
}
