// Package react provides components for ReAct agents with advanced reasoning.
package react

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// DynamicReasoningThoughtGenerator enhances thought generation with dynamic planning
// and error awareness capabilities.
type DynamicReasoningThoughtGenerator struct {
	model       model.Model
	prompting   DynamicThoughtPromptStrategy
	thoughtType string
}

// DynamicThoughtPromptStrategy represents an advanced strategy for prompting thought
// generation with plan management and error awareness.
type DynamicThoughtPromptStrategy interface {
	// BuildThoughtPrompt builds a prompt for thought generation.
	BuildThoughtPrompt(history []*message.Message, previousCycles []*Cycle, planState *PlanState) string
}

// PlanState represents the current state of a plan being executed by a ReAct agent.
type PlanState struct {
	// Goals are the high-level objectives the agent is trying to achieve.
	Goals []string `json:"goals,omitempty"`

	// CurrentPlan is the sequence of steps the agent has planned to achieve the goals.
	CurrentPlan []string `json:"current_plan,omitempty"`

	// CompletedSteps tracks which steps from the plan have been completed.
	CompletedSteps []bool `json:"completed_steps,omitempty"`

	// CurrentStepIndex is the index of the step currently being worked on.
	CurrentStepIndex int `json:"current_step_index"`

	// LastError captures information about the most recent error encountered.
	LastError *ErrorInfo `json:"last_error,omitempty"`

	// ErrorRecoveryAttempts tracks the number of attempts to recover from errors.
	ErrorRecoveryAttempts int `json:"error_recovery_attempts"`

	// RecoveryStrategies contains potential strategies to recover from the current error.
	RecoveryStrategies []string `json:"recovery_strategies,omitempty"`

	// HasPlan indicates if a plan has been created.
	HasPlan bool `json:"has_plan"`

	// RequiresReplanning indicates if the current plan needs to be revised.
	RequiresReplanning bool `json:"requires_replanning"`

	// AdditionalContext stores any extra information that might be relevant.
	AdditionalContext map[string]interface{} `json:"additional_context,omitempty"`
}

// ErrorInfo contains information about an error that occurred during plan execution.
type ErrorInfo struct {
	// Step is the plan step during which the error occurred.
	Step int `json:"step"`

	// Message is the error message.
	Message string `json:"message"`

	// Source indicates where the error originated (e.g., "tool_execution", "planning").
	Source string `json:"source"`

	// Timestamp is when the error occurred.
	Timestamp int64 `json:"timestamp"`

	// ErrorCategory classifies the type of error for better handling.
	ErrorCategory string `json:"error_category,omitempty"`

	// RecoveryAttempts tracks how many times recovery has been attempted for this error.
	RecoveryAttempts int `json:"recovery_attempts"`
}

// DynamicPlanningPromptStrategy is an advanced prompt strategy that
// incorporates plan management and error handling.
type DynamicPlanningPromptStrategy struct {
	tools         []tool.Tool
	planningStyle string // "explicit" or "implicit"
}

// NewDynamicPlanningPromptStrategy creates a new dynamic planning prompt strategy.
func NewDynamicPlanningPromptStrategy(tools []tool.Tool, planningStyle string) *DynamicPlanningPromptStrategy {
	return &DynamicPlanningPromptStrategy{
		tools:         tools,
		planningStyle: planningStyle,
	}
}

// BuildThoughtPrompt builds a dynamic prompt for thought generation that incorporates
// planning and error recovery.
func (s *DynamicPlanningPromptStrategy) BuildThoughtPrompt(
	history []*message.Message,
	previousCycles []*Cycle,
	planState *PlanState,
) string {
	var prompt strings.Builder

	// Add system context and available tools
	prompt.WriteString("You are a ReAct agent with dynamic planning capabilities. Available tools:\n")
	for _, t := range s.tools {
		prompt.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), t.Description()))
	}

	// Add user query if present
	latestUserMsg := findLatestUserMessage(history)
	if latestUserMsg != nil {
		prompt.WriteString("\nUser query: " + latestUserMsg.Content + "\n")
	}

	// Include planning guidance based on planning style
	if s.planningStyle == "explicit" {
		prompt.WriteString("\nYou must follow these steps:\n")
		prompt.WriteString("1. If no plan exists, create a plan with specific steps to address the user's request.\n")
		prompt.WriteString("2. If a plan exists, work on the current plan step.\n")
		prompt.WriteString("3. If you encounter an error, analyze it and decide whether to try again with a different approach or revise the plan.\n")
		prompt.WriteString("4. When all plan steps are completed, provide a final answer.\n")
	} else {
		prompt.WriteString("\nAnalyze the problem and think through your approach carefully. You can create a plan if needed, but focus on making progress toward answering the user's query.\n")
	}

	// Add plan state if it exists
	if planState != nil && planState.HasPlan {
		prompt.WriteString("\nCurrent plan status:\n")

		if len(planState.Goals) > 0 {
			prompt.WriteString("Goals:\n")
			for i, goal := range planState.Goals {
				prompt.WriteString(fmt.Sprintf("- Goal %d: %s\n", i+1, goal))
			}
		}

		prompt.WriteString("\nPlan steps:\n")
		for i, step := range planState.CurrentPlan {
			status := " [ ]"
			if i < len(planState.CompletedSteps) && planState.CompletedSteps[i] {
				status = " [✓]"
			} else if i == planState.CurrentStepIndex {
				status = " [→]" // Current step indicator
			}
			prompt.WriteString(fmt.Sprintf("%d.%s %s\n", i+1, status, step))
		}

		if planState.RequiresReplanning {
			prompt.WriteString("\n❗ The current plan requires revision due to new information or obstacles.\n")
		}

		// Enhanced error handling with recovery suggestions
		if planState.LastError != nil {
			prompt.WriteString(fmt.Sprintf("\n❌ Error encountered: %s\n", planState.LastError.Message))

			if planState.LastError.ErrorCategory != "" {
				prompt.WriteString(fmt.Sprintf("Error category: %s\n", planState.LastError.ErrorCategory))
			}

			prompt.WriteString(fmt.Sprintf("Recovery attempts so far: %d\n", planState.LastError.RecoveryAttempts))

			if len(planState.RecoveryStrategies) > 0 {
				prompt.WriteString("\nPotential recovery strategies:\n")
				for i, strategy := range planState.RecoveryStrategies {
					prompt.WriteString(fmt.Sprintf("%d. %s\n", i+1, strategy))
				}
				prompt.WriteString("\nConsider these strategies or develop new ones to overcome this error.\n")
			} else {
				prompt.WriteString("\nYou need to analyze this error and develop recovery strategies. Consider:\n")
				prompt.WriteString("- Is this a temporary issue that could be resolved by retrying?\n")
				prompt.WriteString("- Should you modify your approach for this step?\n")
				prompt.WriteString("- Does the plan need to be revised because of this obstacle?\n")
				prompt.WriteString("- Could a different tool help accomplish this step?\n")
			}
		}
	} else if len(previousCycles) == 0 {
		// Guidance for creating an initial plan if this is the first cycle
		prompt.WriteString("\nSince this is your first step, consider creating a plan to address the user's request systematically.\n")
	}

	// Add previous reasoning steps
	if len(previousCycles) > 0 {
		prompt.WriteString("\nPrevious reasoning steps:\n")
		for i, cycle := range previousCycles {
			prompt.WriteString(fmt.Sprintf("Step %d:\n", i+1))
			if cycle.Thought != nil {
				prompt.WriteString(fmt.Sprintf("- Thought: %s\n", cycle.Thought.Content))
			}
			if cycle.Actions != nil {
				for _, action := range cycle.Actions {
					prompt.WriteString(fmt.Sprintf("- Action: %s\n", action.ToolName))
					prompt.WriteString(fmt.Sprintf("- Action Input: %v\n", action.ToolInput))
				}
			}
			if cycle.Observations != nil {
				for _, observation := range cycle.Observations {
					if observation.IsError {
						promptVal, ok := observation.ToolOutput["error"]
						if ok {
							prompt.WriteString(fmt.Sprintf("- Observation: Error - %v\n", promptVal))
						} else {
							prompt.WriteString("- Observation: An error occurred.\n")
						}
					} else {
						promptVal, ok := observation.ToolOutput["output"]
						if ok {
							prompt.WriteString(fmt.Sprintf("- Observation: %v\n", promptVal))
						} else {
							prompt.WriteString("- Observation: Tool execution was successful.\n")
						}
					}
				}
			}
			prompt.WriteString("\n")
		}
	}

	// Final guidance for the next thought
	prompt.WriteString("\nNow, generate your next thought.\n")

	if planState != nil && planState.HasPlan {
		if planState.LastError != nil {
			prompt.WriteString("Your priority should be addressing the current error before proceeding.\n")
		} else {
			prompt.WriteString(fmt.Sprintf("You are currently working on step %d: %s\n",
				planState.CurrentStepIndex+1,
				planState.CurrentPlan[planState.CurrentStepIndex]))
		}
	}

	prompt.WriteString("If you have enough information to provide a final answer, include 'Final Answer:' followed by your response.\n")

	return prompt.String()
}

// NewDynamicReasoningThoughtGenerator creates a new thought generator with dynamic reasoning capabilities.
func NewDynamicReasoningThoughtGenerator(model model.Model, prompting DynamicThoughtPromptStrategy) *DynamicReasoningThoughtGenerator {
	return &DynamicReasoningThoughtGenerator{
		model:       model,
		prompting:   prompting,
		thoughtType: "dynamic_reasoning",
	}
}

// Generate generates a thought with dynamic planning and error awareness.
func (g *DynamicReasoningThoughtGenerator) Generate(
	ctx context.Context,
	history []*message.Message,
	previousCycles []*Cycle,
) (*Thought, error) {
	if g.model == nil {
		return nil, fmt.Errorf("model is required for DynamicReasoningThoughtGenerator")
	}

	// Extract or initialize plan state from previous cycles
	planState := extractPlanState(previousCycles)

	// Build the prompt for thought generation
	promptText := ""
	if g.prompting != nil {
		promptText = g.prompting.BuildThoughtPrompt(history, previousCycles, planState)
	} else {
		// Fallback to a simple default prompt if no strategy is provided
		promptText = "Given the conversation history, think step by step about how to proceed. Consider creating a plan if needed, and tracking your progress against it."
	}

	// Create a system message with the prompt
	promptMsg := message.NewSystemMessage(promptText)

	// Combine prompt with history for the model input
	modelInput := make([]*message.Message, 0, len(history)+1)
	modelInput = append(modelInput, promptMsg)
	modelInput = append(modelInput, history...)

	// Generate the thought using the model
	opts := model.DefaultOptions()
	response, err := g.model.GenerateWithMessages(ctx, modelInput, opts)
	if err != nil {
		return nil, fmt.Errorf("thought generation failed: %w", err)
	}

	// Extract the content from the response
	thoughtContent := ""
	if len(response.Messages) > 0 {
		thoughtContent = response.Messages[0].Content
	} else if response.Text != "" {
		thoughtContent = response.Text
	} else {
		return nil, fmt.Errorf("model returned empty response")
	}

	// Process the response for plan updates
	if planState != nil {
		// Update the plan state based on the thought content
		updatePlanState(planState, thoughtContent)

		// Check for error recovery strategies in the thought
		extractErrorRecoveryStrategies(thoughtContent, planState)
	}

	return &Thought{
		ID:        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
		Content:   thoughtContent,
		Timestamp: time.Now().Unix(),
	}, nil
}

// extractErrorRecoveryStrategies looks for error recovery strategies in a thought.
func extractErrorRecoveryStrategies(thoughtContent string, planState *PlanState) {
	if planState == nil || planState.LastError == nil {
		return
	}

	// Check if the thought contains error recovery strategies
	recoveryPrefix := []string{
		"To recover from this error",
		"Recovery strategy",
		"Recovery plan",
		"To fix this error",
		"To address this issue",
		"Error recovery",
	}

	lines := strings.Split(thoughtContent, "\n")
	var strategies []string
	inRecoverySection := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check if this line starts a recovery section
		for _, prefix := range recoveryPrefix {
			if strings.HasPrefix(trimmedLine, prefix) {
				inRecoverySection = true
				if len(trimmedLine) > len(prefix) {
					strategies = append(strategies, strings.TrimSpace(trimmedLine[len(prefix):]))
				}
				break
			}
		}

		// If we're in a recovery section and the line starts with a numbered or bullet point
		if inRecoverySection && (strings.HasPrefix(trimmedLine, "-") ||
			strings.HasPrefix(trimmedLine, "*") ||
			(len(trimmedLine) > 0 && trimmedLine[0] >= '1' && trimmedLine[0] <= '9')) {
			strategies = append(strategies, strings.TrimSpace(trimmedLine[1:]))
		}

		// End of recovery section if we hit an empty line after collecting strategies
		if inRecoverySection && trimmedLine == "" && len(strategies) > 0 {
			inRecoverySection = false
		}
	}

	// If we found strategies, update the plan state
	if len(strategies) > 0 {
		planState.RecoveryStrategies = strategies

		// If this is a recovery attempt, increment the counter
		if planState.LastError != nil {
			planState.LastError.RecoveryAttempts++
			planState.ErrorRecoveryAttempts++
		}
	}
}

// extractPlanState analyzes previous cycles to extract or initialize a plan state.
func extractPlanState(cycles []*Cycle) *PlanState {
	if len(cycles) == 0 {
		// Initialize a new empty plan state
		return &PlanState{
			Goals:              []string{},
			CurrentPlan:        []string{},
			CompletedSteps:     []bool{},
			CurrentStepIndex:   0,
			HasPlan:            false,
			RequiresReplanning: false,
			AdditionalContext:  make(map[string]interface{}),
		}
	}

	// Check if plan information is embedded in the thoughts
	planState := &PlanState{
		Goals:              []string{},
		CurrentPlan:        []string{},
		CompletedSteps:     []bool{},
		CurrentStepIndex:   0,
		HasPlan:            false,
		RequiresReplanning: false,
		AdditionalContext:  make(map[string]interface{}),
	}

	// Look for plan information in the thought content
	for _, cycle := range cycles {
		if cycle.Thought == nil {
			continue
		}

		thought := cycle.Thought.Content

		// Check for plan creation or updates
		if strings.Contains(thought, "Plan:") || strings.Contains(thought, "plan:") {
			extractPlanFromThought(thought, planState)
		}

		// Check for step completion
		for i := range planState.CurrentPlan {
			stepMention := fmt.Sprintf("Step %d:", i+1)
			if strings.Contains(thought, stepMention) &&
				(strings.Contains(thought, "completed") ||
					strings.Contains(thought, "finished") ||
					strings.Contains(thought, "done")) {
				if i < len(planState.CompletedSteps) {
					planState.CompletedSteps[i] = true
				}
			}

			// Check for current step mentions
			currentStepMention := fmt.Sprintf("working on step %d", i+1)
			if strings.Contains(strings.ToLower(thought), currentStepMention) {
				planState.CurrentStepIndex = i
			}
		}

		// Check for error mentions
		for _, observation := range cycle.Observations {
			if observation.IsError {
				errMsg := ""
				if promptVal, ok := observation.ToolOutput["error"]; ok {
					errMsg = fmt.Sprintf("%v", promptVal)
				} else {
					errMsg = "An error occurred"
				}

				planState.LastError = &ErrorInfo{
					Step:      planState.CurrentStepIndex,
					Message:   errMsg,
					Source:    "tool_execution",
					Timestamp: observation.Timestamp,
				}
			}
		}

		// Check for replanning mentions
		if strings.Contains(thought, "replan") ||
			strings.Contains(thought, "revise plan") ||
			strings.Contains(thought, "update plan") {
			planState.RequiresReplanning = true
		}
	}

	return planState
}

// extractPlanFromThought parses a thought string to extract plan information.
func extractPlanFromThought(thoughtContent string, planState *PlanState) {
	lines := strings.Split(thoughtContent, "\n")

	inPlanSection := false
	inGoalsSection := false
	planSteps := []string{}
	goals := []string{}

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Check for section headers
		if strings.Contains(strings.ToLower(trimmedLine), "plan:") {
			inPlanSection = true
			inGoalsSection = false
			continue
		} else if strings.Contains(strings.ToLower(trimmedLine), "goals:") ||
			strings.Contains(strings.ToLower(trimmedLine), "objective:") {
			inGoalsSection = true
			inPlanSection = false
			continue
		}

		// Skip empty lines
		if trimmedLine == "" {
			continue
		}

		// Extract numbered steps or bullet points for the plan
		if inPlanSection {
			// Check for numbered steps like "1. Step one" or bullet points like "- Step one"
			if len(trimmedLine) > 2 &&
				((trimmedLine[0] >= '1' && trimmedLine[0] <= '9' && trimmedLine[1] == '.') ||
					trimmedLine[0] == '-' ||
					trimmedLine[0] == '*') {

				// Remove the number/bullet and get the step description
				stepText := ""
				if trimmedLine[0] >= '1' && trimmedLine[0] <= '9' {
					parts := strings.SplitN(trimmedLine, ".", 2)
					if len(parts) > 1 {
						stepText = strings.TrimSpace(parts[1])
					}
				} else {
					stepText = strings.TrimSpace(trimmedLine[1:])
				}

				if stepText != "" {
					planSteps = append(planSteps, stepText)
				}
			}
		} else if inGoalsSection {
			// Extract goals with similar pattern
			if len(trimmedLine) > 2 &&
				((trimmedLine[0] >= '1' && trimmedLine[0] <= '9' && trimmedLine[1] == '.') ||
					trimmedLine[0] == '-' ||
					trimmedLine[0] == '*') {

				goalText := ""
				if trimmedLine[0] >= '1' && trimmedLine[0] <= '9' {
					parts := strings.SplitN(trimmedLine, ".", 2)
					if len(parts) > 1 {
						goalText = strings.TrimSpace(parts[1])
					}
				} else {
					goalText = strings.TrimSpace(trimmedLine[1:])
				}

				if goalText != "" {
					goals = append(goals, goalText)
				}
			}
		}
	}

	// Update the plan state if we found plan steps
	if len(planSteps) > 0 {
		planState.CurrentPlan = planSteps
		planState.CompletedSteps = make([]bool, len(planSteps))
		planState.HasPlan = true
	}

	// Update goals if found
	if len(goals) > 0 {
		planState.Goals = goals
	}
}

// updatePlanState updates the plan state based on the latest thought.
func updatePlanState(planState *PlanState, thoughtContent string) {
	// Check if this thought contains an updated plan
	if strings.Contains(thoughtContent, "Plan:") || strings.Contains(thoughtContent, "plan:") {
		// Extract the new or updated plan
		extractPlanFromThought(thoughtContent, planState)
	}

	// Check for step completion mentions
	for i := range planState.CurrentPlan {
		stepMention := fmt.Sprintf("Step %d", i+1)
		if strings.Contains(thoughtContent, stepMention) &&
			(strings.Contains(thoughtContent, "completed") ||
				strings.Contains(thoughtContent, "finished") ||
				strings.Contains(thoughtContent, "done")) {
			if i < len(planState.CompletedSteps) {
				planState.CompletedSteps[i] = true
			}
		}
	}

	// Check if all steps are completed - used for potential future features
	// like automatically generating final answers when plan is complete
	//allCompleted := true
	//for _, completed := range planState.CompletedSteps {
	//	if !completed {
	//		allCompleted = false
	//		break
	//	}
	//}

	// Advance to the next step if appropriate
	if planState.HasPlan &&
		planState.CurrentStepIndex < len(planState.CompletedSteps) &&
		planState.CompletedSteps[planState.CurrentStepIndex] {
		// Find the next uncompleted step
		for i := planState.CurrentStepIndex + 1; i < len(planState.CompletedSteps); i++ {
			if !planState.CompletedSteps[i] {
				planState.CurrentStepIndex = i
				break
			}
		}
	}
}

// serializePlanState converts a PlanState to JSON for storage.
func serializePlanState(planState *PlanState) (string, error) {
	if planState == nil {
		return "", nil
	}

	jsonData, err := json.Marshal(planState)
	if err != nil {
		return "", fmt.Errorf("failed to serialize plan state: %w", err)
	}

	return string(jsonData), nil
}

// deserializePlanState converts a JSON string to a PlanState.
func deserializePlanState(jsonStr string) (*PlanState, error) {
	if jsonStr == "" {
		return nil, nil
	}

	var planState PlanState
	err := json.Unmarshal([]byte(jsonStr), &planState)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize plan state: %w", err)
	}

	return &planState, nil
}
