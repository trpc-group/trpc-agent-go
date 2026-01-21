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
//   - Detects intent descriptions (e.g., "I will...") without actual tool calls
//     and marks them as non-final to prevent premature termination
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
			if p.isIntentDescription(content) && !p.hasFinalAnswerTag(content) {
				// This is an intent description without FINAL_ANSWER tag,
				// likely a malformed response. Mark as not done to continue the loop.
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

// isIntentDescription checks if the content appears to be an intent description
// rather than a final answer. Intent descriptions typically indicate the agent
// wants to take an action but hasn't properly formed the tool call.
//
// To avoid false positives (e.g., "Let me know if you have questions" in a valid
// final answer), this function uses a conservative heuristic:
//  1. Action-related tags (/*ACTION*/, /*PLANNING*/, /*REPLANNING*/) are always
//     considered intent descriptions since they explicitly indicate ongoing planning.
//  2. Natural language intent patterns ("I will", "I'll", etc.) are only considered
//     intent descriptions if they appear at the start of content or a line,
//     suggesting the agent is declaring its next action rather than using these
//     phrases incidentally.
func (p *Planner) isIntentDescription(content string) bool {
	if content == "" {
		return false
	}

	// Action-related tags explicitly indicate ongoing planning - always match these.
	actionTags := []string{
		ActionTag,     // /*ACTION*/ tag without actual tool call
		PlanningTag,   // /*PLANNING*/ tag indicates still planning
		ReplanningTag, // /*REPLANNING*/ tag indicates replanning
	}
	for _, tag := range actionTags {
		if strings.Contains(content, tag) {
			return true
		}
	}

	// Natural language intent patterns - only match at the very beginning of content.
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

// hasFinalAnswerTag checks if the content contains the FINAL_ANSWER tag.
func (p *Planner) hasFinalAnswerTag(content string) bool {
	return strings.Contains(content, FinalAnswerTag)
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
		"When answering the question, try to leverage the available tools " +
			"to gather the information instead of your memorized knowledge.",
		"",
		"Follow this process when answering the question: (1) first come up " +
			"with a plan in natural language text format; (2) Then use tools to " +
			"execute the plan and provide reasoning between tool calls " +
			"to make a summary of current state and next step. Tool calls " +
			"and reasoning should be interleaved with each other. (3) " +
			"In the end, return one final answer.",
		"",
		"Follow this format when answering the question: (1) The planning " +
			"part should be under " + PlanningTag + ". (2) Tool calls " +
			"should be under " + ActionTag + ", and the " +
			"reasoning parts should be under " + ReasoningTag + ". (3) The " +
			"final answer part should be under " + FinalAnswerTag + ".",
	}, "\n")

	planningPreamble := strings.Join([]string{
		"Below are the requirements for the planning:",
		"The plan is made to answer the user query if following the " +
			"plan. The plan is coherent and covers all aspects of " +
			"information from user query, and " +
			"only involves the tools that are accessible by the agent.",
		"The plan contains the decomposed steps as a numbered list " +
			"where each step " +
			"should use one or multiple available tools.",
		"By reading the plan, you can intuitively know which tools to trigger or " +
			"what actions to take.",
		"If the initial plan cannot be successfully executed, you " +
			"should learn from previous execution results and revise " +
			"your plan. The revised plan should " +
			"be under " + ReplanningTag + ". Then use tools to follow the new plan.",
	}, "\n")

	actionPreamble := strings.Join([]string{
		"Below are the requirements for the action:",
		"If no tool is needed, explicitly state your next action in " +
			"the first person ('I will...').",
		"If a tool is needed, call it using tool calling (not plain text). " +
			"You may omit the 'I will...' sentence when calling tools.",
		"Do not write fake tool invocations like `functions.web_fetch` or " +
			"`web_fetch({...})` in your message content.",
		"Do not output JSON/code intended to represent a tool call.",
		"After a tool call, wait for the tool result message before " +
			"continuing.",
	}, "\n")

	reasoningPreamble := strings.Join([]string{
		"Below are the requirements for the reasoning:",
		"The reasoning makes a summary of the current trajectory " +
			"based on the user query and tool outputs.",
		"Based on the tool outputs and plan, the reasoning also comes up with " +
			"instructions to the next steps, making the trajectory closer to the " +
			"final answer.",
	}, "\n")

	finalAnswerPreamble := strings.Join([]string{
		"Below are the requirements for the final answer:",
		"The final answer should be precise and follow query formatting " +
			"requirements.",
		"Some queries may not be answerable with the available tools and " +
			"information. In those cases, inform the user why you cannot process " +
			"their query and ask for more information.",
	}, "\n")

	toolCodePreamble := strings.Join([]string{
		"Below are the requirements for tool calls:",
		"",
		"**Use tool calling, not text.**",
		"- Tool calls are structured; they are not executed from plain " +
			"text.",
		"- Do not output a JSON object that 'looks like' a tool call.",
		"- Use only tool names and parameters that are explicitly defined " +
			"in the provided tool schemas.",
		"- Never output tool-call placeholders like `functions.<tool>` in " +
			"the assistant message content.",
		"- If you cannot call a tool, do not pretend you did; ask for " +
			"clarification or proceed without it.",
	}, "\n")

	userInputPreamble := strings.Join([]string{
		"VERY IMPORTANT instruction that you MUST follow in addition " +
			"to the above instructions:",
		"",
		"You should ask for clarification if you need more information to answer " +
			"the question.",
		"You should prefer using the information available in the " +
			"context instead of repeated tool use.",
	}, "\n")

	return strings.Join([]string{
		highLevelPreamble,
		planningPreamble,
		actionPreamble,
		reasoningPreamble,
		finalAnswerPreamble,
		toolCodePreamble,
		userInputPreamble,
	}, "\n\n")
}
