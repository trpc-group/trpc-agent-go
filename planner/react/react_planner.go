//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package react implements the React planner that constrains the LLM response to
// generate a plan before any action/observation.
//
// The React planner is specifically designed for models that need explicit
// planning instructions. It guides the LLM to follow a structured format with
// specific tags for planning, reasoning, actions, and final answers.
//
// Supported workflow:
//   - Planning phase with /*PLANNING*/ tag
//   - Reasoning sections with /*REASONING*/ tag
//   - Action sections with /*ACTION*/ tag
//   - Replanning with /*REPLANNING*/ tag when needed
//   - Final answer with /*FINAL_ANSWER*/ tag
//
// Unlike the built-in planner, this planner provides explicit planning
// instructions and processes responses to organize different content types.
package react

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
)

// Tags used to structure the LLM response.
const (
	PlanningTag    = "/*PLANNING*/"
	ReplanningTag  = "/*REPLANNING*/"
	ReasoningTag   = "/*REASONING*/"
	ActionTag      = "/*ACTION*/"
	FinalAnswerTag = "/*FINAL_ANSWER*/"
)

// Verify that Planner implements the planner.Planner interface.
var _ planner.Planner = (*Planner)(nil)

// Planner represents the React planner that uses explicit planning instructions.
//
// This planner guides the LLM to follow a structured thinking process:
// 1. First create a plan to answer the user's question
// 2. Execute the plan using available tools with reasoning between steps
// 3. Provide a final answer based on the execution results
//
// The planner processes responses to organize content into appropriate sections
// and marks internal reasoning as thoughts for better response structure.
type Planner struct{}

// New creates a new React planner instance.
//
// The React planner doesn't require any configuration options as it uses
// a fixed instruction template for all interactions.
func New() *Planner {
	return &Planner{}
}

// BuildPlanningInstruction builds the system instruction for the React planner.
//
// This method provides comprehensive instructions that guide the LLM to:
// - Create explicit plans before taking action
// - Use structured tags to organize different types of content
// - Follow a reasoning process between tool executions
// - Provide clear final answers
//
// The instruction covers planning requirements, reasoning guidelines,
// tool usage patterns, and formatting expectations.
func (p *Planner) BuildPlanningInstruction(
	ctx context.Context,
	invocation *agent.Invocation,
	llmRequest *model.Request,
) string {
	return p.buildPlannerInstruction()
}

// ProcessPlanningResponse processes the LLM response to organize content
// according to the React planning structure.
//
// This method:
// - Identifies and preserves function calls while filtering empty ones
// - Splits text content based on planning tags
// - Marks planning, reasoning, and action content as thoughts
// - Separates final answers from internal reasoning
//
// Returns a processed response with properly organized content, or nil
// if no processing is needed.
func (p *Planner) ProcessPlanningResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	response *model.Response,
) *model.Response {
	if response == nil || len(response.Choices) == 0 {
		return nil
	}

	// Process each choice in the response.
	processedResponse := *response
	processedResponse.Choices = make([]model.Choice, len(response.Choices))

	for i, choice := range response.Choices {
		processedChoice := choice

		// Process tool calls first.
		if len(choice.Message.ToolCalls) > 0 {
			// Filter out tool calls with empty names.
			var filteredToolCalls []model.ToolCall
			for _, toolCall := range choice.Message.ToolCalls {
				if toolCall.Function.Name != "" {
					filteredToolCalls = append(filteredToolCalls, toolCall)
				}
			}
			processedChoice.Message.ToolCalls = filteredToolCalls
		}

		// Process text content if present.
		if choice.Message.Content != "" {
			processedChoice.Message.Content = p.processTextContent(choice.Message.Content)
		}

		// Process delta content for streaming responses.
		if choice.Delta.Content != "" {
			processedChoice.Delta.Content = p.processTextContent(choice.Delta.Content)
		}

		processedResponse.Choices[i] = processedChoice
	}

	return &processedResponse
}

// processTextContent handles the processing of text content according to
// React planning structure, splitting content by tags and organizing it.
func (p *Planner) processTextContent(content string) string {
	// If content contains final answer tag, split it.
	if strings.Contains(content, FinalAnswerTag) {
		_, finalAnswer := p.splitByLastPattern(content, FinalAnswerTag)
		return finalAnswer
	}
	return content
}

// splitByLastPattern splits text by the last occurrence of a separator.
// Returns the text before the last separator and the text after it.
// The separator itself is not included in either returned part.
func (p *Planner) splitByLastPattern(text, separator string) (string, string) {
	index := strings.LastIndex(text, separator)
	if index == -1 {
		return text, ""
	}
	return text[:index], text[index+len(separator):]
}

// buildPlannerInstruction builds the comprehensive planning instruction
// for the React planner.
func (p *Planner) buildPlannerInstruction() string {
	return strings.Join([]string{
		"You are a helpful AI assistant that uses a structured approach to solve problems.",
		"",
		"**IMPORTANT: You MUST follow this exact format for ALL responses:**",
		"",
		"1. Start with " + PlanningTag + " tag - Write your step-by-step plan",
		"2. Use " + ActionTag + " tag - Execute tools and show results",
		"3. Use " + ReasoningTag + " tag - Explain your thinking between actions",
		"4. End with " + FinalAnswerTag + " tag - Provide your final answer",
		"",
		"**If your initial plan fails or needs adjustment:**",
		"- Use " + ReplanningTag + " tag to create a new plan",
		"- Explain why the original plan didn't work",
		"- Continue with the new plan using " + ActionTag + " tag and " + ReasoningTag + " tag",
		"",
		"**Planning Rules:**",
		"- Break down the problem into clear, numbered steps",
		"- Each step should use available tools when possible",
		"- Keep your plan simple and actionable",
		"",
		"**Action Rules:**",
		"- Use tools exactly as shown in the available tools list",
		"- Show the tool call and its result clearly",
		"- One tool call per action step",
		"",
		"**Reasoning Rules:**",
		"- Explain what you learned from each action",
		"- Plan your next step based on results",
		"- Keep reasoning concise and focused",
		"",
		"**Final Answer Rules:**",
		"- Answer the user's question directly",
		"- Use information gathered from your actions",
		"- Be clear and complete",
		"",
		"**Available Tools:** The tools are described in the context and can be used directly.",
		"",
		"Remember: NEVER repeat tags, NEVER mix content between tags, and ALWAYS follow the exact order: " +
			PlanningTag + " → " + ActionTag + " → " + ReasoningTag + " → " + FinalAnswerTag,
		"If replanning is needed: " +
			PlanningTag + " → " + ActionTag + " → " + ReasoningTag + " → " + ReplanningTag + " → " + ActionTag +
			" → " + ReasoningTag + " → " + FinalAnswerTag,
	}, "\n")
}
