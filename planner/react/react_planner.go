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
	highLevelPreamble := strings.Join([]string{
		"When answering the question, try to leverage the available tools " +
			"to gather the information instead of your memorized knowledge.",
		"",
		"Follow this process when answering the question: (1) first come up " +
			"with a plan in natural language text format; (2) Then use tools to " +
			"execute the plan and provide reasoning between tool code snippets " +
			"to make a summary of current state and next step. Tool code " +
			"snippets and reasoning should be interleaved with each other. (3) " +
			"In the end, return one final answer.",
		"",
		"Follow this format when answering the question: (1) The planning " +
			"part should be under " + PlanningTag + ". (2) The tool code " +
			"snippets should be under " + ActionTag + ", and the reasoning " +
			"parts should be under " + ReasoningTag + ". (3) The final answer " +
			"part should be under " + FinalAnswerTag + ".",
	}, "\n")

	planningPreamble := strings.Join([]string{
		"Below are the requirements for the planning:",
		"The plan is made to answer the user query if following the plan. The plan " +
			"is coherent and covers all aspects of information from user query, and " +
			"only involves the tools that are accessible by the agent.",
		"The plan contains the decomposed steps as a numbered list where each step " +
			"should use one or multiple available tools.",
		"By reading the plan, you can intuitively know which tools to trigger or " +
			"what actions to take.",
		"If the initial plan cannot be successfully executed, you should learn from " +
			"previous execution results and revise your plan. The revised plan should " +
			"be under " + ReplanningTag + ". Then use tools to follow the new plan.",
	}, "\n")

	reasoningPreamble := strings.Join([]string{
		"Below are the requirements for the reasoning:",
		"The reasoning makes a summary of the current trajectory based on the user " +
			"query and tool outputs.",
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
		"Below are the requirements for the tool code:",
		"",
		"**Custom Tools:** The available tools are described in the context and " +
			"can be directly used.",
		"- Code must be valid self-contained snippets with no imports and no " +
			"references to tools or libraries that are not in the context.",
		"- You cannot use any parameters or fields that are not explicitly defined " +
			"in the APIs in the context.",
		"- The code snippets should be readable, efficient, and directly relevant to " +
			"the user query and reasoning steps.",
		"- When using the tools, you should use the tool name together with the " +
			"function name.",
		"- If libraries are not provided in the context, NEVER write your own code " +
			"other than the function calls using the provided tools.",
	}, "\n")

	userInputPreamble := strings.Join([]string{
		"VERY IMPORTANT instruction that you MUST follow in addition to the above " +
			"instructions:",
		"",
		"You should ask for clarification if you need more information to answer " +
			"the question.",
		"You should prefer using the information available in the context instead " +
			"of repeated tool use.",
	}, "\n")

	return strings.Join([]string{
		highLevelPreamble,
		planningPreamble,
		reasoningPreamble,
		finalAnswerPreamble,
		toolCodePreamble,
		userInputPreamble,
	}, "\n\n")
}
