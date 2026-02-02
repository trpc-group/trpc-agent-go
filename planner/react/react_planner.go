//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package react implements the React planner that constrains the LLM
// response to generate a plan before any action/observation.
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

const (
	actionTagPrefix     = "/*ACTION"
	planningTagPrefix   = "/*PLANNING"
	replanningTagPrefix = "/*REPLANNING"
	finalAnswerPrefix   = "FINAL ANSWER:"
)

// Verify that Planner implements the planner.Planner interface.
var _ planner.Planner = (*Planner)(nil)

// Planner represents the React planner that uses explicit planning
// instructions.
//
// This planner guides the LLM to follow a structured thinking process:
// 1. First create a plan to answer the user's question
// 2. Execute the plan using available tools with reasoning between steps
// 3. Provide a final answer based on the execution results
//
// The planner processes responses to organize content into appropriate
// sections and marks internal reasoning as thoughts for better response
// structure.
type Planner struct{}

// New creates a new React planner instance.
//
// The React planner doesn't require any configuration options as it uses
// a fixed instruction template for all interactions.
func New() *Planner {
	return &Planner{}
}

// BuildPlanningInstruction builds the system instruction for the React
// planner.
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

// ProcessPlanningResponse processes the LLM response by filtering and
// cleaning tool calls to ensure only valid function calls are preserved.
//
// This method:
//   - Filters out tool calls with empty function names
//   - Detects intent descriptions (e.g., "I will...") without actual tool
//     calls and marks them as non-final to prevent premature termination
//   - Preserves all other response content unchanged
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
		processedResponse.Choices[i] = processedChoice
	}

	// Check if this looks like an intent description without actual tool calls.
	// If so, mark response as not done to prevent premature termination.
	// Only check the first choice to be consistent with other logic.
	if processedResponse.Done && len(processedResponse.Choices) > 0 {
		firstChoice := processedResponse.Choices[0]
		if len(firstChoice.Message.ToolCalls) == 0 {
			content := firstChoice.Message.Content
			hasFinalAnswer := p.hasFinalAnswer(content)
			containsFinalAnswerTag := strings.Contains(
				content,
				FinalAnswerTag,
			)
			if (p.isIntentDescription(content) ||
				containsFinalAnswerTag) &&
				!hasFinalAnswer {
				// The model appears to be in an intermediate state (e.g.,
				// planning/action tags or an empty FINAL_ANSWER section) without
				// actually providing a final answer. Mark as not done to
				// continue the loop.
				processedResponse.Done = false
			}
		}
	}

	return &processedResponse
}

// hasValidToolCalls checks if the response contains any valid tool calls.
func (p *Planner) hasValidToolCalls(response *model.Response) bool {
	if response == nil || len(response.Choices) == 0 {
		return false
	}
	for _, choice := range response.Choices {
		if len(choice.Message.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// getResponseContent extracts the text content from the response.
func (p *Planner) getResponseContent(response *model.Response) string {
	if response == nil || len(response.Choices) == 0 {
		return ""
	}
	return response.Choices[0].Message.Content
}

// isIntentDescription checks if the content appears to be an intent
// description rather than a final answer. Intent descriptions typically
// indicate the agent wants to take an action but hasn't properly formed
// the tool call.
//
// To avoid false positives (e.g., "Let me know if you have questions" in a
// valid final answer), this function uses a conservative heuristic:
//  1. Action-related tags (/*ACTION*/, /*PLANNING*/, /*REPLANNING*/) are
//     considered intent descriptions since they explicitly indicate
//     ongoing planning.
//  2. Natural language intent patterns ("I will", "I'll", etc.) are only
//     considered intent descriptions if they appear at the start of
//     content, suggesting the agent is declaring its next action rather
//     than using these phrases incidentally.
func (p *Planner) isIntentDescription(content string) bool {
	if content == "" {
		return false
	}

	// Action-related tags explicitly indicate ongoing planning.
	actionTagPrefixes := []string{
		actionTagPrefix,
		planningTagPrefix,
		replanningTagPrefix,
	}
	for _, prefix := range actionTagPrefixes {
		if strings.Contains(content, prefix) {
			return true
		}
	}

	// Natural language intent patterns only match at the beginning of
	// content.
	// This avoids false positives like "Let me know if I'll need to..." or
	// "I should also mention that I will...".
	intentPrefixes := []string{
		"I will ",
		"I'll ",
		"I am going to ",
		"I'm going to ",
	}

	// Only check if the first sentence starts with any intent prefix.
	trimmedContent := strings.TrimSpace(content)
	for _, prefix := range intentPrefixes {
		if strings.HasPrefix(trimmedContent, prefix) {
			return true
		}
	}

	return false
}

// hasFinalAnswer reports whether the content contains a valid final answer
// marker (either a non-empty FINAL_ANSWER section or a "FINAL ANSWER:" line).
func (p *Planner) hasFinalAnswer(content string) bool {
	if p.hasFinalAnswerTag(content) {
		return true
	}
	upper := strings.ToUpper(content)
	return strings.Contains(upper, finalAnswerPrefix)
}

// hasFinalAnswerTag checks if the content contains a non-empty FINAL_ANSWER
// section. A bare tag with no answer does not count as final.
func (p *Planner) hasFinalAnswerTag(content string) bool {
	idx := strings.LastIndex(content, FinalAnswerTag)
	if idx == -1 {
		return false
	}
	rest := strings.TrimSpace(content[idx+len(FinalAnswerTag):])
	if rest == "" {
		return false
	}

	tagPrefixes := []string{
		actionTagPrefix,
		planningTagPrefix,
		replanningTagPrefix,
		ReasoningTag,
	}
	for _, prefix := range tagPrefixes {
		if strings.HasPrefix(rest, prefix) {
			return false
		}
	}
	return true
}

// splitByLastPattern splits text by the last occurrence of a separator.
// Returns the text before the last separator and the text after it.
// The separator itself is not included in either returned part.
func (p *Planner) splitByLastPattern(
	text string,
	separator string,
) (string, string) {
	index := strings.LastIndex(text, separator)
	if index == -1 {
		return text, ""
	}
	return text[:index], text[index+len(separator):]
}

// buildPlannerInstruction builds the comprehensive planning instruction
// for the React planner.
func (p *Planner) buildPlannerInstruction() string {
	highLevelPreamble := strings.Join([]string{
		"You are an AI assistant that solves problems step by step using available tools.",
		"",
		"WORKFLOW (execute one step at a time):",
		"1. PLAN: Create a numbered plan under " + PlanningTag,
		"2. EXECUTE: For each step, output " + ActionTag + " with a brief description, then CALL the tool",
		"3. REASON: After receiving tool results, output " + ReasoningTag + " to analyze and decide next step",
		"4. REPEAT steps 2-3 until you have enough information",
		"5. ANSWER: Output " + FinalAnswerTag + " followed by ONLY the final answer",
	}, "\n")

	criticalRules := strings.Join([]string{
		"CRITICAL RULES:",
		"",
		"1. ONE STEP PER RESPONSE: Each response should contain only ONE action or the final answer.",
		"   - Do NOT output multiple " + ActionTag + " sections in a single response.",
		"   - Do NOT repeat or summarize previous steps.",
		"",
		"2. TOOL CALLS MUST USE FUNCTION CALLING API:",
		"   - NEVER write tool calls as text/JSON in your response (e.g., {\"query\": \"...\"}).",
		"   - Tools are executed via the function calling mechanism, not by writing code in text.",
		"   - After " + ActionTag + ", describe what you will do, then issue the actual tool call.",
		"",
		"3. FINAL ANSWER FORMAT:",
		"   - When ready to answer, output " + FinalAnswerTag + " followed by the answer.",
		"   - Do NOT include " + PlanningTag + ", " + ActionTag + ", or " + ReasoningTag + " after " + FinalAnswerTag + ".",
		"   - Do NOT summarize or repeat the reasoning process in the final answer.",
		"   - Include brief explanation only if necessary for clarity.",
		"   - Example: " + FinalAnswerTag + "\n   42",
		"",
		"4. ONLY USE AVAILABLE TOOLS:",
		"   - Only use tools explicitly provided in the context.",
		"   - Do NOT invent or assume tools that are not available.",
		"",
		"5. NO RETROSPECTIVE SUMMARIES:",
		"   - Do NOT output a \"summary\" of all previous steps at the end.",
		"   - Do NOT repeat the plan or actions you already executed.",
		"   - Each response should only contain NEW content for the current step.",
	}, "\n")

	planningPreamble := strings.Join([]string{
		"PLANNING REQUIREMENTS:",
		"- Create a coherent plan that covers all aspects of the user query.",
		"- The plan should be a numbered list where each step uses available tools.",
		"- If the initial plan fails, use " + ReplanningTag + " to revise it.",
	}, "\n")

	actionPreamble := strings.Join([]string{
		"ACTION REQUIREMENTS:",
		"- State your next action briefly: 'I will [action]'.",
		"- Then issue ONE tool call via the function calling API.",
		"- Wait for the tool result before proceeding.",
	}, "\n")

	reasoningPreamble := strings.Join([]string{
		"REASONING REQUIREMENTS:",
		"- Summarize what the tool result tells you.",
		"- Decide whether you have enough information or need another step.",
		"- Keep reasoning concise (2-3 sentences).",
	}, "\n")

	finalAnswerPreamble := strings.Join([]string{
		"FINAL ANSWER REQUIREMENTS:",
		"- The answer should be precise and match any formatting requirements in the query.",
		"- Output the answer directly. Include brief explanation only if necessary for clarity.",
		"- If the query cannot be answered, explain why briefly.",
	}, "\n")

	userInputPreamble := strings.Join([]string{
		"ADDITIONAL GUIDELINES:",
		"- Prefer using information already in context over repeated tool calls.",
		"- Ask for clarification if the query is ambiguous or lacks necessary details.",
	}, "\n")

	// Few-shot example demonstrating the expected format.
	fewShotExample := p.buildFewShotExample()

	return strings.Join([]string{
		highLevelPreamble,
		criticalRules,
		planningPreamble,
		actionPreamble,
		reasoningPreamble,
		finalAnswerPreamble,
		userInputPreamble,
		fewShotExample,
	}, "\n\n")
}

// buildFewShotExample builds a few-shot example demonstrating the expected
// React format based on actual successful execution patterns.
func (p *Planner) buildFewShotExample() string {
	return strings.Join([]string{
		"=== EXAMPLE ===",
		"User: What is the population of Tokyo in millions?",
		"",
		PlanningTag,
		"1. Search Wikipedia for Tokyo's population data",
		"2. Extract the population number and convert to millions",
		"3. Provide the final answer",
		"",
		ActionTag,
		"I will search Wikipedia for Tokyo's population information.",
		"",
		ReasoningTag,
		"The Wikipedia search returned Tokyo's population as approximately 13,960,000. Converting to millions: 13,960,000 / 1,000,000 = 13.96 million.",
		"",
		FinalAnswerTag,
		"13.96",
		"=== END EXAMPLE ===",
	}, "\n")
}
